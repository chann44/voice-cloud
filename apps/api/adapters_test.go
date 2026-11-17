package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"voice-cloud/db"
)

func audioUpload(t *testing.T, model, format string) ([]byte, string) {
	t.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	_ = writer.WriteField("model", model)
	_ = writer.WriteField("language", "en")
	if format != "" {
		_ = writer.WriteField("response_format", format)
	}
	part, err := writer.CreateFormFile("file", "voice.wav")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("recorded audio"))
	_ = writer.Close()
	return buffer.Bytes(), writer.FormDataContentType()
}

func TestNativeSpeechAdapters(t *testing.T) {
	for _, adapter := range []string{"deepgram", "elevenlabs"} {
		t.Run(adapter, func(t *testing.T) {
			enableSecretStorage(t)
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if adapter == "deepgram" {
					if r.Header.Get("Authorization") != "Token secret" {
						t.Error("wrong Deepgram auth")
					}
				} else if r.Header.Get("xi-api-key") != "secret" {
					t.Error("wrong ElevenLabs auth")
				}
				switch r.URL.Path {
				case "/v1/models":
					if adapter == "deepgram" {
						fmt.Fprint(w, `{"stt":[{"canonical_name":"nova-test","batch":true},{"canonical_name":"realtime-only","batch":false}],"tts":[{"canonical_name":"aura-test"}]}`)
					} else {
						fmt.Fprint(w, `[{"model_id":"speech-test","can_do_text_to_speech":true},{"model_id":"voice-conversion-only","can_do_text_to_speech":false}]`)
					}
				case "/v1/listen":
					raw, _ := io.ReadAll(r.Body)
					if string(raw) != "recorded audio" || r.URL.Query().Get("model") != "nova-test" || r.URL.Query().Get("language") != "en" {
						t.Error("wrong Deepgram upload")
					}
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"metadata":{"duration":1.25,"request_id":"native-id"},"results":{"channels":[{"alternatives":[{"transcript":"hello native"}]}]}}`)
				case "/v1/speech-to-text":
					if err := r.ParseMultipartForm(1024); err != nil {
						t.Error(err)
					}
					defer r.MultipartForm.RemoveAll()
					file, _, err := r.FormFile("file")
					if err != nil {
						t.Error(err)
						return
					}
					defer file.Close()
					raw, _ := io.ReadAll(file)
					if string(raw) != "recorded audio" || r.FormValue("model_id") != "scribe-test" || r.FormValue("language_code") != "en" {
						t.Error("wrong ElevenLabs upload")
					}
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"text":"hello native"}`)
				case "/v1/speak", "/v1/text-to-speech/voice_test":
					var fields map[string]string
					_ = json.NewDecoder(r.Body).Decode(&fields)
					if fields["text"] != "hello" {
						t.Error("wrong TTS text")
					}
					if adapter == "deepgram" {
						if r.URL.Query().Get("model") != "aura-test" || r.URL.Query().Get("encoding") != "mp3" {
							t.Error("wrong Deepgram synthesis")
						}
					} else if fields["model_id"] != "speech-test" || r.URL.Query().Get("output_format") != "mp3_44100_128" {
						t.Error("wrong ElevenLabs synthesis")
					}
					w.Header().Set("Content-Type", "audio/mpeg")
					_, _ = w.Write([]byte("ID3-native-audio"))
				default:
					t.Error("unexpected native endpoint", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer provider.Close()
			a := setup(t)
			a.call("POST", "/v1/connections", fmt.Sprintf(`{"name":"Native","adapter":%q,"location_kind":"cloud","base_url":%q,"api_key":"secret"}`, adapter, provider.URL), 201)
			catalog := a.call("POST", "/v1/connections/1/discover", "", 200).Body.String()
			if strings.Contains(catalog, "realtime-only") || strings.Contains(catalog, "voice-conversion-only") {
				t.Fatal("unsupported route advertised", catalog)
			}
			stt, tts, voice := "nova-test", "aura-test", "aura-test"
			if adapter == "elevenlabs" {
				stt, tts, voice = "scribe-test", "speech-test", "voice_test"
			}
			a.call("POST", "/v1/connections/1/models", fmt.Sprintf(`{"upstream_model_id":%q,"task":"stt","capabilities_json":{"response_formats":["json","text"]}}`, stt), 201)
			a.call("POST", "/v1/connections/1/models", fmt.Sprintf(`{"upstream_model_id":%q,"task":"tts","capabilities_json":{"voices":[%q],"response_formats":["mp3"]}}`, tts, voice), 201)
			key := jsonObject(t, a.call("POST", "/v1/api-keys", `{"name":"App"}`, 201))["key"].(string)
			for _, format := range []string{"json", "text"} {
				raw, contentType := audioUpload(t, "deployment:1", format)
				result := callInference(t, a, key, "/v1/audio/transcriptions", contentType, raw, nil, 200)
				if !strings.Contains(result.Body.String(), "hello native") {
					t.Fatal(result.Body.String())
				}
			}
			audio := callInference(t, a, key, "/v1/audio/speech", "application/json", []byte(fmt.Sprintf(`{"model":"deployment:2","input":"hello","voice":%q}`, voice)), nil, 200)
			if audio.Body.String() != "ID3-native-audio" {
				t.Fatal("audio changed")
			}
			callInference(t, a, key, "/v1/audio/speech", "application/json", []byte(fmt.Sprintf(`{"model":"deployment:2","input":"hello","voice":%q,"speed":2}`, voice)), nil, 400)

		})
	}
}

func TestNativeTranscriptionMalformedResponseUsesFallback(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/listen" {
			fmt.Fprint(w, `{"results":{}}`)
			return
		}
		if r.URL.Path != "/v1/audio/transcriptions" {
			t.Error("unexpected fallback path", r.URL.Path)
		}
		fmt.Fprint(w, `{"text":"fallback transcript"}`)
	}))
	defer provider.Close()
	a, key := setupInference(t, "stt", provider.URL)
	a.call("PUT", "/v1/connections/1", fmt.Sprintf(`{"name":"Native","adapter":"deepgram","location_kind":"cloud","base_url":%q,"credential_ref":"TEST_INFERENCE_SECRET"}`, provider.URL), 200)
	a.call("POST", "/v1/connections", fmt.Sprintf(`{"name":"Backup","adapter":"openai-compatible","location_kind":"cloud","base_url":%q}`, provider.URL), 201)
	a.call("POST", "/v1/deployments", `{"name":"Backup","model_id":1,"connection_id":2,"upstream_model_id":"backup"}`, 201)
	a.call("PUT", "/v1/policies/1/targets/2", `{"priority":1}`, 200)
	body, contentType := audioUpload(t, "policy:1", "json")
	result := callInference(t, a, key, "/v1/audio/transcriptions", contentType, body, nil, 200)
	if !strings.Contains(result.Body.String(), "fallback transcript") {
		t.Fatal(result.Body.String())
	}
	attempts, err := db.New(a.db).ListRequestAttempts(context.Background(), result.Header().Get("X-Request-ID"))
	if err != nil || len(attempts) != 2 || attempts[0].Status != "failed" || attempts[0].ErrorCode.String != "invalid_upstream_response" || attempts[1].Status != "succeeded" {
		t.Fatal("native fallback not recorded", attempts, err)
	}
}

func audioUploadStream(t *testing.T, model string) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("model", model)
	_ = writer.WriteField("stream", "true")
	part, err := writer.CreateFormFile("file", "recording.wav")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("recorded audio"))
	_ = writer.Close()
	return body.Bytes(), writer.FormDataContentType()
}
