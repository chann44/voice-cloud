package main

import (
	"database/sql"
	"encoding/json"
	"math"
	"mime/multipart"
	"testing"
	"time"
	"voice-cloud/db"
)

func TestCapabilityRequirements(t *testing.T) {
	for _, test := range []struct{ name, task, body, caps, want string }{
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
	input := &inferenceInput{task: "stt", form: &multipart.Form{}, stream: true}
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
