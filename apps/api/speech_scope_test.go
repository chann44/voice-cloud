package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"voice-cloud/db"
)

func TestSpeechOnlyProductScope(t *testing.T) {
	a := setup(t)
	a.call("POST", "/v1/models", `{"name":"No chat","publisher":"test","task":"llm"}`, 400)
	a.call("POST", "/v1/policies", `{"name":"No chat","task":"llm"}`, 400)
	a.call("POST", "/v1/chat/completions", `{"messages":[]}`, 410)
	a.call("POST", "/v1/models", `{"name":"Native speech","publisher":"test","task":"s2s"}`, 201)
	for _, adapter := range supportedAdapters {
		for _, task := range adapter.Tasks {
			if task == "llm" {
				t.Fatal("chat adapter advertised", adapter)
			}
		}
	}
	if adapterSupport("anthropic") != nil || adapterSupport("cerebras") != nil {
		t.Fatal("text-only provider supported")
	}
	// Existing data remains in SQLite, but active model/policy catalogs are speech-only.
	_, err := a.db.Exec(`INSERT INTO models(name,publisher,task) VALUES('Legacy chat','old','llm'); INSERT INTO routing_policies(name,task) VALUES('Legacy chat','llm')`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(a.call("GET", "/v1/models", "", 200).Body.String(), "Legacy chat") || strings.Contains(a.call("GET", "/v1/policies", "", 200).Body.String(), "Legacy chat") {
		t.Fatal("legacy text models still offered")
	}
}

func s2sUpload(t *testing.T, model, voice, target string) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range map[string]string{"model": model, "voice": voice, "target_language": target, "response_format": "mp3"} {
		if value != "" {
			if err := writer.WriteField(key, value); err != nil {
				t.Fatal(err)
			}
		}
	}
	part, err := writer.CreateFormFile("file", "recording.wav")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("native input speech"))
	_ = writer.Close()
	return body.Bytes(), writer.FormDataContentType()
}

func TestNativeSpeechToSpeechAndTranslation(t *testing.T) {
	for _, adapter := range []string{"elevenlabs", "local"} {
		t.Run(adapter, func(t *testing.T) {
			enableSecretStorage(t)
			var calls atomic.Int64
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" {
					w.Header().Set("Content-Type", "application/json")
					if adapter == "elevenlabs" {
						fmt.Fprint(w, `[{"model_id":"native-s2s","can_do_voice_conversion":true}]`)
					} else {
						fmt.Fprint(w, `{"data":[{"id":"native-s2s","task":"s2s"},{"id":"text-only","task":"llm"},{"id":"unknown"}]}`)
					}
					return
				}
				calls.Add(1)
				if err := r.ParseMultipartForm(1024); err != nil {
					t.Error(err)
					return
				}
				defer r.MultipartForm.RemoveAll()
				field := "file"
				if adapter == "elevenlabs" {
					field = "audio"
					if r.URL.Path != "/v1/voice-changer/voice_test" || r.Header.Get("xi-api-key") != "secret" || r.FormValue("model_id") != "native-s2s" {
						t.Error("wrong native voice conversion request", r.URL)
					}
				} else if r.URL.Path != "/v1/audio/speech-to-speech" || r.FormValue("target_language") != "fr" || r.FormValue("model") != "native-s2s" {
					t.Error("wrong translation request", r.URL)
				}
				file, _, err := r.FormFile(field)
				if err != nil {
					t.Error(err)
					return
				}
				defer file.Close()
				raw, _ := io.ReadAll(file)
				if string(raw) != "native input speech" {
					t.Error("audio changed")
				}
				w.Header().Set("Content-Type", "audio/mpeg")
				fmt.Fprint(w, "ID3-native-output")
			}))
			defer provider.Close()
			a := setup(t)
			a.call("POST", "/v1/connections", fmt.Sprintf(`{"name":"Speech","adapter":%q,"location_kind":"local","base_url":%q,"api_key":"secret"}`, adapter, provider.URL), 201)
			catalog := a.call("POST", "/v1/connections/1/discover", "", 200).Body.String()
			if strings.Contains(catalog, "text-only") || strings.Contains(catalog, "unknown") || !strings.Contains(catalog, `"task":"s2s"`) {
				t.Fatal(catalog)
			}
			a.call("POST", "/v1/connections/1/models", `{"upstream_model_id":"native-s2s","task":"s2s","capabilities_json":{"voices":["voice_test"],"response_formats":["mp3"],"target_languages":["fr"]}}`, 201)
			key := jsonObject(t, a.call("POST", "/v1/api-keys", `{"name":"App"}`, 201))["key"].(string)
			target := ""
			if adapter == "local" {
				target = "fr"
			}
			raw, media := s2sUpload(t, "deployment:1", "voice_test", target)
			response := callInference(t, a, key, "/v1/audio/speech-to-speech", media, raw, nil, 200)
			if response.Body.String() != "ID3-native-output" || calls.Load() != 1 {
				t.Fatal("S2S did not return provider audio")
			}
			raw, media = s2sUpload(t, "deployment:1", "voice_test", "de")
			callInference(t, a, key, "/v1/audio/speech-to-speech", media, raw, nil, 400)
			if calls.Load() != 1 {
				t.Fatal("unsupported translation reached provider")
			}
			var tasks string
			if err := a.db.QueryRow("SELECT task FROM requests LIMIT 1").Scan(&tasks); err != nil || tasks != "s2s" {
				t.Fatal(tasks, err)
			}
		})
	}
}

func TestSpeechCostAndInputValidation(t *testing.T) {
	var fields map[string]json.RawMessage
	_ = json.Unmarshal([]byte(`{"input":"Hello","voice":"voice"}`), &fields)
	input := &inferenceInput{task: "tts", fields: fields}
	req, err := requiredCapabilities(input)
	if err != nil {
		t.Fatal(err)
	}
	// Text character cost is known; uploaded audio duration is unknown until reported.
	if cost := estimatedRoutingCost(input, req, db.Deployment{PricingJson: `{"input_character_nanos":2}`}); cost == nil || *cost != 10 {
		t.Fatal(cost)
	}
}
