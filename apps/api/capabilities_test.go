package main

import (
	"database/sql"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"voice-cloud/db"
)

func TestCapabilityFilteringBeforeDispatch(t *testing.T) {
	var incompatibleCalls atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Model == "upstream-primary" {
			incompatibleCalls.Add(1)
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"Tool capable answer"},"finish_reason":"stop"}]}`)
	}))
	defer provider.Close()
	api, key := setupInference(t, "llm", provider.URL)
	api.call("POST", "/v1/deployments", `{"name":"Tools","model_id":1,"connection_id":1,"upstream_model_id":"tools","capabilities_json":{"tools":true}}`, 201)
	api.call("PUT", "/v1/policies/1/targets/2", `{"priority":1}`, 200)
	body := `{"messages":[{"role":"user","content":"Use a tool"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]}`
	response := callInference(t, api, key, "/v1/chat/completions", "application/json", []byte(body), nil, 200)
	if incompatibleCalls.Load() != 0 || !strings.Contains(response.Body.String(), "Tool capable answer") {
		t.Fatal("incompatible model received the request")
	}
}

func TestCapabilityRequirements(t *testing.T) {
	for _, test := range []struct{ name, task, body, caps, want string }{
		{"basic text", "llm", `{"messages":[{"role":"user","content":"Hello"}]}`, `{}`, ""},
		{"tools require declaration", "llm", `{"messages":[{"role":"user","content":"Hello"}],"tools":[{}]}`, `{}`, "capability_tools"},
		{"tools supported", "llm", `{"messages":[{"role":"user","content":"Hello"}],"tools":[{}]}`, `{"tools":true}`, ""},
		{"image content", "llm", `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.test/a.png"}}]}]}`, `{}`, "capability_vision"},
		{"audio content", "llm", `{"messages":[{"role":"user","content":[{"type":"input_audio","input_audio":{"data":"a"}}]}]}`, `{}`, "capability_audio_input"},
		{"audio output", "llm", `{"messages":[{"role":"user","content":"Hello"}],"modalities":["audio"]}`, `{}`, "capability_audio_output"},
		{"json object", "llm", `{"messages":[{"role":"user","content":"Hello"}],"response_format":{"type":"json_object"}}`, `{"json_output":true}`, ""},
		{"json schema", "llm", `{"messages":[{"role":"user","content":"Hello"}],"response_format":{"type":"json_schema"}}`, `{"json_output":true}`, "capability_json_schema"},
		{"output limit required", "llm", `{"messages":[{"role":"user","content":"Hello"}],"max_tokens":100}`, `{}`, "output_limit"},
		{"output limit", "llm", `{"messages":[{"role":"user","content":"Hello"}],"max_tokens":100}`, `{"max_output_tokens":99}`, "output_limit"},
		{"context budget", "llm", `{"messages":[{"role":"user","content":"Hello"}]}`, `{"context_window":20}`, "context_limit"},
		{"input bytes", "llm", `{"messages":[{"role":"user","content":"Hello"}]}`, `{"max_input_bytes":10}`, "input_limit"},
		{"malformed capabilities", "llm", `{"messages":[{"role":"user","content":"Hello"}]}`, `{"tools":"yes"}`, "invalid_capabilities"},
		{"null capability", "llm", `{"messages":[{"role":"user","content":"Hello"}]}`, `{"tools":null}`, "invalid_capabilities"},
		{"voice must be declared", "tts", `{"input":"Hello","voice":"alloy"}`, `{}`, "unsupported_voice"},
		{"voice and format", "tts", `{"input":"Hello","voice":"alloy"}`, `{"voices":["alloy"],"response_formats":["mp3"]}`, ""},
		{"format must match", "tts", `{"input":"Hello","voice":"alloy","response_format":"wav"}`, `{"voices":["alloy"],"response_formats":["mp3"]}`, "unsupported_format"},
		{"characters bound", "tts", `{"input":"Hello","voice":"alloy"}`, `{"voices":["alloy"],"response_formats":["mp3"],"max_input_characters":4}`, "input_character_limit"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal([]byte(test.body), &fields); err != nil {
				t.Fatal(err)
			}
			input := &inferenceInput{task: test.task, fields: fields}
			req, err := requiredCapabilities(input)
			if err != nil {
				t.Fatal(err)
			}
			if got := capabilityReason(input, req, db.Deployment{CapabilitiesJson: test.caps}); got != test.want {
				t.Fatalf("reason %q want %q", got, test.want)
			}
		})
	}
	input := &inferenceInput{task: "llm", fields: map[string]json.RawMessage{"messages": json.RawMessage(`[{"role":"user","content":"Hello"}]`)}, stream: true}
	req, err := requiredCapabilities(input)
	if err != nil {
		t.Fatal(err)
	}
	if got := capabilityReason(input, req, db.Deployment{CapabilitiesJson: `{}`}); got != "capability_streaming" {
		t.Fatal(got)
	}
	if got := capabilityReason(input, req, db.Deployment{CapabilitiesJson: `{"streaming":true}`}); got != "" {
		t.Fatal(got)
	}
}

func TestInferencePerformance(t *testing.T) {
	now := time.Now().UnixMilli()
	attempt := func(status, code string, age, latency int64) db.Attempt {
		return db.Attempt{Status: status, ErrorCode: sql.NullString{String: code, Valid: code != ""}, StartedAt: now - age, FinishedAt: sql.NullInt64{Int64: now - age + latency, Valid: true}, FirstOutputAt: sql.NullInt64{Int64: now - age + latency, Valid: status == "succeeded"}}
	}
	failures := []db.Attempt{attempt("failed", "upstream_status_503", 10, 0), attempt("failed", "upstream_status_429", 20, 0), attempt("failed", "upstream_network_error", 30, 0)}
	if _, cooldown := inferencePerformance(failures, now); !cooldown {
		t.Fatal("three consecutive transient failures did not open cooldown")
	}
	if _, cooldown := inferencePerformance(failures, now+30001); cooldown {
		t.Fatal("cooldown never recovered")
	}
	recovered := append([]db.Attempt{attempt("succeeded", "", 5, 3)}, failures...)
	if latency, cooldown := inferencePerformance(recovered, now); cooldown || latency != 3 {
		t.Fatalf("success did not recover: %d %v", latency, cooldown)
	}
	ignored := []db.Attempt{attempt("failed", "credential_unavailable", 1, 0), attempt("failed", "upstream_status_400", 2, 0)}
	if latency, cooldown := inferencePerformance(ignored, now); cooldown || latency != math.MaxInt64 {
		t.Fatal("client/config errors polluted performance")
	}
}
