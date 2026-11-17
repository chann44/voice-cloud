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
	"sync/atomic"
	"testing"

	"voice-cloud/db"
)

func setupInference(t *testing.T, task, providerURL string) (*testAPI, string) {
	t.Helper()
	a := setup(t)
	a.call("POST", "/v1/connections", fmt.Sprintf(`{"name":"Provider","adapter":"custom","location_kind":"local","base_url":%q,"credential_ref":"TEST_INFERENCE_SECRET"}`, providerURL), 201)
	a.call("POST", "/v1/models", fmt.Sprintf(`{"name":"Model","publisher":"test","task":%q}`, task), 201)
	a.call("POST", "/v1/deployments", `{"name":"Primary","model_id":1,"connection_id":1,"upstream_model_id":"upstream-primary","pricing_json":{"request_nanos":7},"capabilities_json":{"streaming":true,"voices":["alloy"],"response_formats":["json","text","verbose_json","mp3"],"max_input_bytes":32768,"max_output_tokens":4096}}`, 201)
	a.call("POST", "/v1/policies", fmt.Sprintf(`{"name":"Route","task":%q}`, task), 201)
	a.call("PUT", "/v1/policies/1/targets/1", `{"priority":0}`, 200)
	created := jsonObject(t, a.call("POST", "/v1/api-keys", `{"name":"App","default_policy_id":1}`, 201))
	t.Setenv("TEST_INFERENCE_SECRET", "provider-secret")
	return a, created["key"].(string)
}

func callInference(t *testing.T, a *testAPI, token, path, contentType string, body []byte, headers map[string]string, status int, expectAbort ...bool) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", path, bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", contentType)
	for name, value := range headers {
		r.Header.Set(name, value)
	}
	w := httptest.NewRecorder()
	aborted := false
	func() {
		defer func() {
			if problem := recover(); problem != nil {
				if problem != http.ErrAbortHandler {
					panic(problem)
				}
				aborted = true
			}
		}()
		a.handler.ServeHTTP(w, r)
	}()
	wantAbort := len(expectAbort) == 1 && expectAbort[0]
	if aborted != wantAbort {
		t.Fatalf("handler aborted=%v, want %v", aborted, wantAbort)
	}
	if w.Code != status {
		t.Fatalf("inference %s: got %d, want %d: %s", path, w.Code, status, w.Body.String())
	}
	return w
}

func TestInferenceFallback(t *testing.T) {
	for _, upstreamStatus := range []int{503, 401, 403, 404, 400} {
		t.Run(fmt.Sprint(upstreamStatus), func(t *testing.T) {
			var primary, backup atomic.Int64
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct{ Model string }
				_ = r.ParseMultipartForm(1024)
				defer r.MultipartForm.RemoveAll()
				body.Model = r.FormValue("model")

				if body.Model == "upstream-primary" {
					primary.Add(1)
					w.WriteHeader(upstreamStatus)
					io.WriteString(w, `{"error":"private-provider-error"}`)
					return
				}
				backup.Add(1)
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"text":"Hello"}`)
			}))
			defer provider.Close()
			a, token := setupInference(t, "stt", provider.URL)
			a.call("POST", "/v1/deployments", `{"name":"Backup","model_id":1,"connection_id":1,"upstream_model_id":"upstream-backup"}`, 201)
			a.call("PUT", "/v1/policies/1/targets/2", `{"priority":1}`, 200)
			wantStatus, wantAttempts := 200, 2
			if upstreamStatus == 400 {
				wantStatus, wantAttempts = 502, 1
			}
			raw, media := audioUpload(t, "policy:1", "json")
			response := callInference(t, a, token, "/v1/audio/transcriptions", media, raw, nil, wantStatus)
			if primary.Load() != 1 || backup.Load() != int64(wantAttempts-1) || strings.Contains(response.Body.String(), "private-provider-error") {
				t.Fatalf("fallback calls primary=%d backup=%d response=%s", primary.Load(), backup.Load(), response.Body.String())
			}
			attempts, err := db.New(a.db).ListRequestAttempts(context.Background(), response.Header().Get("X-Request-ID"))
			if err != nil || len(attempts) != wantAttempts || attempts[0].ErrorCode.String != fmt.Sprintf("upstream_status_%d", upstreamStatus) {
				t.Fatalf("fallback attempts: %#v %v", attempts, err)
			}
		})
	}
}

func TestInferenceMultipartAndSpeech(t *testing.T) {
	t.Run("transcription", func(t *testing.T) {
		provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/audio/transcriptions" || r.Header.Get("Authorization") != "Bearer provider-secret" {
				t.Errorf("transcription request: %s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
			}
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("multipart parse: %v", err)
				w.WriteHeader(400)
				return
			}
			defer r.MultipartForm.RemoveAll()
			file, _, err := r.FormFile("file")
			if err != nil {
				t.Errorf("upstream file: %v", err)
				w.WriteHeader(400)
				return
			}
			defer file.Close()
			data, err := io.ReadAll(file)
			if err != nil || string(data) != "audio-bytes" || r.FormValue("model") != "upstream-primary" || len(r.MultipartForm.Value["model"]) != 1 || r.FormValue("language") != "en" {
				t.Errorf("multipart forwarding: file=%q form=%v err=%v", data, r.MultipartForm.Value, err)
			}
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"text":"Hello","duration":1.25}`)
		}))
		defer provider.Close()
		a, token := setupInference(t, "stt", provider.URL)
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		for name, value := range map[string]string{"model": "policy:1", "language": "en"} {
			if err := writer.WriteField(name, value); err != nil {
				t.Fatal(err)
			}
		}
		part, err := writer.CreateFormFile("file", "audio.wav")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(part, "audio-bytes"); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		response := callInference(t, a, token, "/v1/audio/transcriptions", writer.FormDataContentType(), body.Bytes(), nil, 200)
		var duration int64
		err = a.db.QueryRow(`SELECT u.quantity FROM attempt_usage u JOIN attempts a ON a.id = u.attempt_id WHERE a.request_id = ? AND u.metric = 'audio_ms'`, response.Header().Get("X-Request-ID")).Scan(&duration)
		if err != nil || duration != 1250 {
			t.Fatalf("audio duration: %d %v", duration, err)
		}
	})
	t.Run("speech", func(t *testing.T) {
		provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || r.URL.Path != "/v1/audio/speech" || body["model"] != "upstream-primary" || body["voice"] != "alloy" {
				t.Errorf("speech request: %s %#v %v", r.URL.Path, body, err)
			}
			w.Header().Set("Content-Type", "audio/mpeg")
			w.Write([]byte{0, 1, 2, 255})
		}))
		defer provider.Close()
		a, token := setupInference(t, "tts", provider.URL)
		response := callInference(t, a, token, "/v1/audio/speech", "application/json", []byte(`{"model":"deployment:1","input":"Hi ☀","voice":"alloy"}`), nil, 200)
		if response.Header().Get("Content-Type") != "audio/mpeg" || !bytes.Equal(response.Body.Bytes(), []byte{0, 1, 2, 255}) {
			t.Fatalf("speech bytes: %v", response.Body.Bytes())
		}
		var characters int64
		err := a.db.QueryRow(`SELECT u.quantity FROM attempt_usage u JOIN attempts a ON a.id = u.attempt_id WHERE a.request_id = ? AND u.metric = 'input_characters'`, response.Header().Get("X-Request-ID")).Scan(&characters)
		if err != nil || characters != 4 {
			t.Fatalf("character usage: %d %v", characters, err)
		}
	})
}

func TestInferenceStreams(t *testing.T) {
	for _, mode := range []string{"complete", "incomplete", "interrupted"} {
		t.Run(mode, func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				if mode == "interrupted" {
					w.Header().Set("Content-Length", "99999")
				}
				io.WriteString(w, "data: {\"type\":\"transcript.text.delta\",\"delta\":\"Hello\"}\n\n")
				w.(http.Flusher).Flush()
				io.WriteString(w, "data: {\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2}}\n\n")
				w.(http.Flusher).Flush()
				if mode == "complete" {
					io.WriteString(w, "data: [DONE]\n\n")
				}
			}))
			defer provider.Close()
			a, token := setupInference(t, "stt", provider.URL)
			a.call("POST", "/v1/deployments", `{"name":"Backup","model_id":1,"connection_id":1,"upstream_model_id":"backup","capabilities_json":{"streaming":true}}`, 201)
			a.call("PUT", "/v1/policies/1/targets/2", `{"priority":1}`, 200)
			raw, media := audioUploadStream(t, "policy:1")
			response := callInference(t, a, token, "/v1/audio/transcriptions", media, raw, nil, 200, mode != "complete")
			requestID := response.Header().Get("X-Request-ID")
			request, err := db.New(a.db).GetRequest(context.Background(), requestID)
			wantStatus, wantCode := "succeeded", ""
			if mode == "incomplete" {
				wantStatus, wantCode = "failed", "response_interrupted"
			} else if mode == "interrupted" {
				wantStatus, wantCode = "failed", "response_interrupted"
			}
			if err != nil || request.Status != wantStatus || request.ErrorCode.String != wantCode || request.Transport != "sse" {
				t.Fatalf("stream activity: %#v %v", request, err)
			}
			var attempts, inputTokens int64
			if err := a.db.QueryRow(`SELECT count(*) FROM attempts WHERE request_id = ?`, requestID).Scan(&attempts); err != nil || attempts != 1 {
				t.Fatalf("committed stream retried: attempts=%d err=%v", attempts, err)
			}
			if err := a.db.QueryRow(`SELECT u.quantity FROM attempt_usage u JOIN attempts a ON a.id = u.attempt_id WHERE a.request_id = ? AND u.metric = 'input_tokens'`, requestID).Scan(&inputTokens); err != nil || inputTokens != 3 {
				t.Fatalf("stream usage: %d %v", inputTokens, err)
			}
			if !strings.Contains(response.Body.String(), "Hello") || strings.Contains(response.Body.String(), `"error"`) {
				t.Fatalf("stream response corrupted: %s", response.Body.String())
			}
		})
	}
}
