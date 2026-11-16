package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"voice-cloud/db"
)

func decisionPolicy(t *testing.T, endpoint string) db.RoutingPolicy {
	t.Helper()
	config, err := json.Marshal(map[string]any{"url": endpoint, "model": "jev-latest", "credential_ref": "TEST_DECISION_SECRET", "timeout_ms": 500, "min_confidence": 0.6})
	if err != nil {
		t.Fatal(err)
	}
	return db.RoutingPolicy{ID: 1, Task: "llm", Objective: "balanced", DecisionJson: string(config)}
}

func decisionInput(t *testing.T, body string) *inferenceInput {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &fields); err != nil {
		t.Fatal(err)
	}
	return &inferenceInput{task: "llm", fields: fields}
}

func decisionDeployments() []db.Deployment {
	return []db.Deployment{
		{ID: 1, Name: "Fast", UpstreamModelID: "fast", CapabilitiesJson: `{"routing_description":"Fast answers to simple questions"}`, PricingJson: `{"request_nanos":5}`},
		{ID: 2, Name: "Reasoning", UpstreamModelID: "reasoning", CapabilitiesJson: `{"routing_description":"Multi-step reasoning and coding"}`, PricingJson: `{"request_nanos":10}`},
		{ID: 3, Name: "Backup", UpstreamModelID: "backup", CapabilitiesJson: `{"routing_description":"General purpose backup"}`, PricingJson: `{}`},
	}
}

func TestDecisionConfigValidation(t *testing.T) {
	config, err := parseDecisionConfig(json.RawMessage(`{}`), "stt")
	if err != nil || config != nil {
		t.Fatalf("disabled: %#v %v", config, err)
	}
	config, err = parseDecisionConfig(json.RawMessage(`{"url":"https://api.typesafe.ai/v1/systemone","model":"jev-latest"}`), "llm")
	if err != nil || config == nil || *config.TimeoutMs != 500 || *config.MinConfidence != 0.6 {
		t.Fatalf("defaults: %#v %v", config, err)
	}
	for _, raw := range []string{
		`null`, `[]`, `{"url":"https://example.test","model":"jev","unknown":true}`,
		`{"url":"https://example.test","model":"jev","timeout_ms":null}`,
		`{"url":"https://example.test","model":"jev","timeout_ms":49}`,
		`{"url":"https://example.test","model":"jev","timeout_ms":5001}`,
		`{"url":"https://example.test","model":"jev","min_confidence":-0.1}`,
		`{"url":"https://example.test","model":"jev","min_confidence":1.1}`,
		`{"url":"https://example.test","model":"jev","min_confidence":null}`,
		`{"url":"https://user:secret@example.test","model":"jev"}`,
		`{"url":"https://example.test?secret=value","model":"jev"}`,
		`{"url":"https://example.test#fragment","model":"jev"}`,
		`{"url":"file:///tmp/decision","model":"jev"}`,
		`{"url":"https://example.test","model":""}`,
		`{"url":"https://example.test","model":"jev","credential_ref":""}`,
	} {
		if _, err := parseDecisionConfig(json.RawMessage(raw), "llm"); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	for _, task := range []string{"stt", "tts"} {
		if _, err := parseDecisionConfig(json.RawMessage(`{"url":"https://example.test","model":"jev"}`), task); err == nil {
			t.Errorf("accepted decision for %s", task)
		}
	}
}

func TestDecisionChoiceAndFallback(t *testing.T) {
	t.Setenv("TEST_DECISION_SECRET", "decision-secret")
	for _, scenario := range []struct {
		name, body, status string
		delay              bool
	}{
		{"selected", `{"model":"jev-served","answers":{"deployment":{"type":"choice","choice":"2","confidence":0.9}},"usage":{"input_tokens":50,"output_tokens":3}}`, "selected", false},
		{"low confidence", `{"answers":{"deployment":{"type":"choice","choice":"2","confidence":0.2}}}`, "low_confidence", false},
		{"outside eligible pool", `{"answers":{"deployment":{"type":"choice","choice":"999","confidence":1}}}`, "invalid_response", false},
		{"missing confidence", `{"answers":{"deployment":{"type":"choice","choice":"2"}}}`, "invalid_response", false},
		{"invalid confidence", `{"answers":{"deployment":{"type":"choice","choice":"2","confidence":2}}}`, "invalid_response", false},
		{"wrong answer type", `{"answers":{"deployment":{"type":"score","choice":"2","confidence":1}}}`, "invalid_response", false},
		{"malformed JSON", `not-json`, "invalid_response", false},
		{"timeout", ``, "timeout", true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if scenario.delay {
					select {
					case <-r.Context().Done():
					case <-time.After(time.Second):
					}
					return
				}
				if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer decision-secret" {
					t.Errorf("decision auth/method: %s %q", r.Method, r.Header.Get("Authorization"))
				}
				var payload struct {
					Model string `json:"model"`
					State struct {
						Messages []map[string]string `json:"messages"`
					} `json:"state"`
					Questions map[string]struct {
						Type     string                     `json:"type"`
						Criteria map[string]json.RawMessage `json:"criteria"`
					} `json:"questions"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if payload.Model != "jev-latest" || len(payload.State.Messages) != 1 || payload.State.Messages[0]["content"] != "Solve this problem" || payload.Questions["deployment"].Type != "choice" || len(payload.Questions["deployment"].Criteria) != 3 {
					t.Errorf("wrong typed decision request: %#v", payload)
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, scenario.body)
			}))
			defer server.Close()
			policy := decisionPolicy(t, server.URL)
			if scenario.delay {
				policy.DecisionJson = strings.Replace(policy.DecisionJson, `"timeout_ms":500`, `"timeout_ms":50`, 1)
			}
			a := &api{}
			deployments := decisionDeployments()
			ordered, result := a.decideRoute(context.Background(), policy, decisionInput(t, `{"messages":[{"role":"user","content":"Solve this problem","private_metadata":"omit this"}]}`), deployments)
			if result.Status != scenario.status {
				t.Fatalf("status %q, want %q", result.Status, scenario.status)
			}
			want := []int64{1, 2, 3}
			if scenario.status == "selected" {
				want = []int64{2, 1, 3}
				if result.ServedModel != "jev-served" || result.DeploymentID != 2 || !strings.Contains(string(result.Usage), `"input_tokens":50`) {
					t.Fatalf("metadata: %#v", result)
				}
			}
			for index, id := range want {
				if ordered[index].ID != id {
					t.Fatalf("order %#v, want %v", ordered, want)
				}
			}
			if deployments[0].ID != 1 {
				t.Fatal("mutated original fallback order")
			}
		})
	}
}

func TestDecisionBypassesDoNotSendContent(t *testing.T) {
	t.Setenv("TEST_DECISION_SECRET", "decision-secret")
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		t.Error("bypassed input reached decision service")
	}))
	defer server.Close()
	cases := []struct {
		name, body, status                                                               string
		single, missingDescription, disabled, missingCredential, largeName, largeContext bool
	}{
		{name: "multimodal", body: `{"messages":[{"role":"user","content":[{"type":"text","text":"private"},{"type":"image_url","image_url":{"url":"data:private-image"}}]}]}`, status: "unsupported_input"},
		{name: "tools", body: `{"messages":[{"role":"user","content":"private"}],"tools":[{"type":"function"}]}`, status: "unsupported_input"},
		{name: "tool call", body: `{"messages":[{"role":"assistant","content":"private","tool_calls":[{"arguments":"secret"}]}]}`, status: "unsupported_input"},
		{name: "function call", body: `{"messages":[{"role":"assistant","content":"private","function_call":{"arguments":"secret"}}]}`, status: "unsupported_input"},
		{name: "large input", body: fmt.Sprintf(`{"messages":[{"role":"user","content":%q}]}`, strings.Repeat("x", 16<<10)), status: "input_too_large"},
		{name: "single candidate", body: `{"messages":[{"role":"user","content":"private"}]}`, status: "single_candidate", single: true},
		{name: "missing description", body: `{"messages":[{"role":"user","content":"private"}]}`, status: "missing_description", missingDescription: true},
		{name: "disabled", body: `{"messages":[{"role":"user","content":"private"}]}`, status: "disabled", disabled: true},
		{name: "missing credential", body: `{"messages":[{"role":"user","content":"private"}]}`, status: "credential_unavailable", missingCredential: true},
		{name: "large candidate name", body: `{"messages":[{"role":"user","content":"private"}]}`, status: "candidate_context_too_large", largeName: true},
		{name: "large candidate payload", body: `{"messages":[{"role":"user","content":"private"}]}`, status: "candidate_context_too_large", largeContext: true},
	}
	for _, scenario := range cases {
		t.Run(scenario.name, func(t *testing.T) {
			policy := decisionPolicy(t, server.URL)
			if scenario.disabled {
				policy.DecisionJson = `{}`
			}
			deployments := decisionDeployments()
			if scenario.missingCredential {
				t.Setenv("TEST_DECISION_SECRET", "")
			}
			if scenario.largeName {
				deployments[0].Name = strings.Repeat("x", 501)
			}
			if scenario.largeContext {
				descriptions, err := json.Marshal(map[string]string{"routing_description": strings.Repeat("x", 2000)})
				if err != nil {
					t.Fatal(err)
				}
				deployments = nil
				for id := int64(1); id <= 100; id++ {
					deployments = append(deployments, db.Deployment{ID: id, Name: "Candidate", UpstreamModelID: "model", CapabilitiesJson: string(descriptions), PricingJson: "{}"})
				}
			}
			if scenario.single {
				deployments = deployments[:1]
			}
			if scenario.missingDescription {
				deployments[1].CapabilitiesJson = `{}`
			}
			ordered, result := (&api{}).decideRoute(context.Background(), policy, decisionInput(t, scenario.body), deployments)
			if result.Status != scenario.status || ordered[0].ID != 1 {
				t.Fatalf("bypass: %#v %#v", result, ordered)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("decision calls: %d", calls.Load())
	}
}

func TestDecisionRoutingActivityAndIdempotency(t *testing.T) {
	var decisions atomic.Int64
	chooser := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		decisions.Add(1)
		if r.Header.Get("Authorization") != "Bearer decision-secret" {
			t.Errorf("decision credential %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"model":"jev-served","answers":{"deployment":{"type":"choice","choice":"2","confidence":0.95}},"usage":{"input_tokens":40,"output_tokens":2}}`)
	}))
	defer chooser.Close()
	upstream := make(chan string, 8)
	var providerCalls atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer provider-secret" {
			t.Errorf("provider credential %q", r.Header.Get("Authorization"))
		}
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		providerCalls.Add(1)
		upstream <- body.Model
		if body.Model == "upstream-backup" {
			w.WriteHeader(503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"content":"Answer"}}]}`)
	}))
	defer provider.Close()
	a, key := setupInference(t, "llm", provider.URL)
	t.Setenv("TEST_DECISION_SECRET", "decision-secret")
	if _, err := a.db.Exec(`UPDATE deployments SET capabilities_json = '{"routing_description":"Fast simple answers"}' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	a.call("POST", "/v1/deployments", `{"name":"Backup","model_id":1,"connection_id":1,"upstream_model_id":"upstream-backup","capabilities_json":{"routing_description":"Multi-step reasoning"}}`, 201)
	a.call("PUT", "/v1/policies/1/targets/2", `{"priority":1}`, 200)
	config := decisionPolicy(t, chooser.URL)
	a.call("PUT", "/v1/policies/1", fmt.Sprintf(`{"name":"Route","task":"llm","objective":"balanced","fallback_enabled":true,"max_attempts":2,"decision_json":%s}`, config.DecisionJson), 200)
	body := []byte(`{"model":"policy:1","messages":[{"role":"user","content":"private-request-text"}]}`)
	headers := map[string]string{"Idempotency-Key": "decision-once"}
	response := callInference(t, a, key, "/v1/chat/completions", "application/json", body, headers, 200)
	if len(upstream) != 2 {
		t.Fatalf("provider calls %d, want 2", len(upstream))
	}
	first, second := <-upstream, <-upstream
	if first != "upstream-backup" || second != "upstream-primary" {
		t.Fatalf("provider order %s, %s", first, second)
	}
	requestID := response.Header().Get("X-Request-ID")
	q := db.New(a.db)
	request, err := q.GetRequest(context.Background(), requestID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(request.RoutingSnapshotJson, "private-request-text") || strings.Contains(request.RoutingSnapshotJson, "decision-secret") || strings.Contains(request.RoutingSnapshotJson, key) {
		t.Fatalf("sensitive content persisted: %s", request.RoutingSnapshotJson)
	}
	var snapshot struct {
		Decision      routeDecision `json:"decision"`
		DeploymentIDs []int64       `json:"deployment_ids"`
	}
	if err := json.Unmarshal([]byte(request.RoutingSnapshotJson), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Decision.Status != "selected" || snapshot.Decision.DeploymentID != 2 || len(snapshot.DeploymentIDs) != 2 || snapshot.DeploymentIDs[0] != 2 {
		t.Fatalf("snapshot %#v", snapshot)
	}
	attempts, err := q.ListRequestAttempts(context.Background(), requestID)
	if err != nil || len(attempts) != 2 || attempts[0].DeploymentID != 2 || attempts[0].SelectionReason != "decision_selected" || attempts[1].SelectionReason != "decision_fallback_balanced" {
		t.Fatalf("attempts %#v %v", attempts, err)
	}
	callInference(t, a, key, "/v1/chat/completions", "application/json", body, headers, 409)
	if decisions.Load() != 1 || providerCalls.Load() != 2 {
		t.Fatal("duplicate request reran decision or provider")
	}
	callInference(t, a, key, "/v1/chat/completions", "application/json", []byte(`{"model":"deployment:1","messages":[{"role":"user","content":"Explicit"}]}`), nil, 200)
	if decisions.Load() != 1 {
		t.Fatal("explicit deployment called decision service")
	}
	a.call("PUT", "/v1/policies/1", `{"name":"Route","task":"llm","objective":"balanced"}`, 200)
	callInference(t, a, key, "/v1/chat/completions", "application/json", body, nil, 200)
	if decisions.Load() != 1 {
		t.Fatal("disabled policy called decision service")
	}
}
