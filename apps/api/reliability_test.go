package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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

func TestSpeechFirstOutputFallback(t *testing.T) {
	for _, mode := range []string{"html", "headers_stall", "metadata_only"} {
		t.Run(mode, func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseMultipartForm(1024); err != nil {
					t.Error(err)
					return
				}
				defer r.MultipartForm.RemoveAll()
				if r.FormValue("model") == "upstream-primary" {
					switch mode {
					case "html":
						w.Header().Set("Content-Type", "text/html")
						fmt.Fprint(w, "<html>discarded</html>")
					case "headers_stall":
						<-r.Context().Done()
					case "metadata_only":
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprint(w, "data: {\"type\":\"transcript.metadata\",\"value\":\"discarded\"}\n\n")
						w.(http.Flusher).Flush()
						<-r.Context().Done()
					}
					return
				}
				if mode == "metadata_only" {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: {\"type\":\"transcript.text.delta\",\"delta\":\"Recovered\"}\n\ndata: {\"type\":\"transcript.text.done\",\"text\":\"Recovered\"}\n\n")
				} else {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"text":"Recovered"}`)
				}
			}))
			defer provider.Close()
			a, key := setupInference(t, "stt", provider.URL)
			if _, err := a.db.Exec(`UPDATE deployments SET settings_json='{"first_output_timeout_ms":50}' WHERE id=1`); err != nil {
				t.Fatal(err)
			}
			a.call("POST", "/v1/deployments", `{"name":"Backup","model_id":1,"connection_id":1,"upstream_model_id":"backup","capabilities_json":{"streaming":true}}`, 201)
			a.call("PUT", "/v1/policies/1/targets/2", `{"priority":1}`, 200)
			raw, media := audioUpload(t, "policy:1", "json")
			if mode == "metadata_only" {
				raw, media = audioUploadStream(t, "policy:1")
			}
			result := callInference(t, a, key, "/v1/audio/transcriptions", media, raw, nil, 200)
			if !strings.Contains(result.Body.String(), "Recovered") || strings.Contains(result.Body.String(), "discarded") {
				t.Fatal("invalid primary response committed", result.Body.String())
			}
		})
	}
}
