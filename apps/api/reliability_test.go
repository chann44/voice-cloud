package main

import (
	"context"
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

func TestInvalidSuccessResponseUsesFallback(t *testing.T) {
	var backup atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The two targets use different upstream model identifiers.
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "upstream-backup") {
			backup.Add(1)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"Recovered"},"finish_reason":"stop"}]}`)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html>Gateway login</html>")
	}))
	defer provider.Close()
	a, key := setupInference(t, "llm", provider.URL)
	a.call("POST", "/v1/deployments", `{"name":"Backup","model_id":1,"connection_id":1,"upstream_model_id":"upstream-backup"}`, 201)
	a.call("PUT", "/v1/policies/1/targets/2", `{"priority":1}`, 200)
	response := callInference(t, a, key, "/v1/chat/completions", "application/json", []byte(`{"messages":[{"role":"user","content":"Hello"}]}`), nil, 200)
	if backup.Load() != 1 || !strings.Contains(response.Body.String(), "Recovered") {
		t.Fatalf("invalid success reached client instead of backup: %s", response.Body.String())
	}
	attempts, err := db.New(a.db).ListRequestAttempts(context.Background(), response.Header().Get("X-Request-ID"))
	if err != nil || len(attempts) != 2 || attempts[0].Status != "failed" {
		t.Fatalf("invalid response was not recorded failed: %+v %v", attempts, err)
	}
}

func TestIdleHeaderOnlyResponseUsesFallback(t *testing.T) {
	// This uses the real network transport; a provider can flush 200 without body.
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body strings.Builder
		_, _ = io.Copy(&body, r.Body)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(body.String(), "upstream-backup") {
			fmt.Fprint(w, `{"choices":[{"message":{"content":"Recovered"},"finish_reason":"stop"}]}`)
			return
		}
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer provider.Close()
	a, key := setupInference(t, "llm", provider.URL)
	a.call("POST", "/v1/deployments", `{"name":"Backup","model_id":1,"connection_id":1,"upstream_model_id":"upstream-backup","settings_json":{"first_output_timeout_ms":50}}`, 201)
	a.call("PUT", "/v1/policies/1/targets/2", `{"priority":1}`, 200)
	if _, err := a.db.Exec(`UPDATE deployments SET settings_json='{"first_output_timeout_ms":50}' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"Hello"}]}`)).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+key)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.handler.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Recovered") {
		t.Fatalf("header-only primary prevented fallback: %d %s", w.Code, w.Body.String())
	}
}

func TestChatResponseValidation(t *testing.T) {
	for _, raw := range []string{`{"choices":[{"message":{"content":null}}]}`, `{"choices":[{"message":{"content":42}}]}`, `{"choices":[{"message":{"role":"user","content":"wrong"}}]}`, `{"choices":[{"message":{"tool_calls":{}}}]}`, `<html>error</html>`} {
		if validChatResponse([]byte(raw)) {
			t.Errorf("accepted invalid completion: %s", raw)
		}
	}
	for _, raw := range []string{`{"choices":[{"message":{"role":"assistant","content":"OK"}}]}`, `{"choices":[{"message":{"content":null,"tool_calls":[{"type":"function","function":{"name":"lookup","arguments":"{}"}}]}}]}`} {
		if !validChatResponse([]byte(raw)) {
			t.Errorf("rejected valid completion: %s", raw)
		}
	}
}

func TestStreamCompletionDoesNotWaitForEOF(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Complete\"}}]}\n\ndata: [DONE]\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer provider.Close()
	a, key := setupInference(t, "llm", provider.URL)
	if _, err := a.db.Exec(`UPDATE deployments SET capabilities_json='{"streaming":true}' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	response := callInference(t, a, key, "/v1/chat/completions", "application/json", []byte(`{"messages":[{"role":"user","content":"Hello"}],"stream":true}`), nil, 200)
	if time.Since(started) > time.Second || !strings.Contains(response.Body.String(), "[DONE]") {
		t.Fatal("completed stream waited for provider to close")
	}
}

func TestRoleOnlyStreamCanUseFallback(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		if strings.Contains(string(raw), "upstream-backup") {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Recovered\"}}]}\n\ndata: [DONE]\n\n")
			return
		}
		fmt.Fprint(w, "data: {\"id\":\"discarded-primary\",\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer provider.Close()
	a, key := setupInference(t, "llm", provider.URL)
	if _, err := a.db.Exec(`UPDATE deployments SET capabilities_json='{"streaming":true}',settings_json='{"first_output_timeout_ms":50}' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	a.call("POST", "/v1/deployments", `{"name":"Backup","model_id":1,"connection_id":1,"upstream_model_id":"upstream-backup","capabilities_json":{"streaming":true}}`, 201)
	a.call("PUT", "/v1/policies/1/targets/2", `{"priority":1}`, 200)
	response := callInference(t, a, key, "/v1/chat/completions", "application/json", []byte(`{"messages":[{"role":"user","content":"Hello"}],"stream":true}`), nil, 200)
	if !strings.Contains(response.Body.String(), "Recovered") || strings.Contains(response.Body.String(), "discarded-primary") {
		t.Fatalf("role-only preamble prevented fallback: %s", response.Body.String())
	}
}

func TestDeploymentResponseDeadlineSettings(t *testing.T) {
	limits, err := deploymentResponseLimits(`{"healthcheck":{"url":"http://localhost/health"}}`)
	if err != nil || limits.FirstOutputMs != 30000 || limits.IdleMs != 15000 {
		t.Fatalf("defaults: %+v %v", limits, err)
	}
	for _, raw := range []string{`{"first_output_timeout_ms":49}`, `{"idle_timeout_ms":120001}`, `{"idle_timeout_ms":null}`, `{"first_output_timeout_ms":1.5}`, `[]`} {
		if _, err := deploymentResponseLimits(raw); err == nil {
			t.Fatalf("invalid timeout accepted: %s", raw)
		}
	}
}
