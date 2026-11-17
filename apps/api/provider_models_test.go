package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"voice-cloud/db"
)

func TestProviderDiscoveryImportAndInference(t *testing.T) {
	var calls atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("provider credential missing")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			if r.Method != "GET" {
				t.Error("discovery must use GET")
			}
			fmt.Fprint(w, `{"data":[{"id":"z-model","owned_by":"publisher","task":"stt"},{"id":"a-model","task":"stt"},{"id":"a-model","task":"stt"}]}`)
		case "/v1/audio/transcriptions":
			if err := r.ParseMultipartForm(1024); err != nil {
				t.Error(err)
			}
			defer r.MultipartForm.RemoveAll()
			if r.FormValue("model") != "a-model" {
				t.Error("upstream model not substituted")
			}
			fmt.Fprint(w, `{"text":"Works"}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer provider.Close()
	t.Setenv("CATALOG_KEY", "secret")
	a := setup(t)
	a.call("POST", "/v1/connections", fmt.Sprintf(`{"name":"Cloud","adapter":"custom","location_kind":"cloud","base_url":%q,"credential_ref":"CATALOG_KEY"}`, provider.URL), 201)
	found := a.call("POST", "/v1/connections/1/discover", "", 200)
	if found.Body.String() != `[{"id":"a-model","owned_by":"","task":"stt"},{"id":"z-model","owned_by":"publisher","task":"stt"}]`+"\n" {
		t.Fatal(found.Body.String())
	}
	imported := jsonObject(t, a.call("POST", "/v1/connections/1/models", `{"upstream_model_id":"a-model","task":"stt","capabilities_json":{"max_output_tokens":128}}`, 201))
	if imported["upstream_model_id"] != "a-model" || imported["enabled"] != true {
		t.Fatal(imported)
	}
	a.call("POST", "/v1/connections/1/models", `{"upstream_model_id":"a-model","task":"stt","capabilities_json":{"tools":true}}`, 200)
	var models, deployments int
	_ = a.db.QueryRow("SELECT count(*) FROM models").Scan(&models)
	_ = a.db.QueryRow("SELECT count(*) FROM deployments").Scan(&deployments)
	if models != 1 || deployments != 1 {
		t.Fatalf("duplicate import: %d %d", models, deployments)
	}
	saved := jsonObject(t, a.call("GET", "/v1/deployments/1", "", 200))
	if _, exists := saved["capabilities_json"].(map[string]any)["tools"]; exists {
		t.Fatal("repeat import overwrote operator capabilities")
	}
	key := jsonObject(t, a.call("POST", "/v1/api-keys", `{"name":"App"}`, 201))["key"].(string)
	raw, media := audioUpload(t, "deployment:1", "json")
	callInference(t, a, key, "/v1/audio/transcriptions", media, raw, nil, 200)
	if calls.Load() != 2 {
		t.Fatal("import should not run or bill inference")
	}
}

func TestProviderDiscoveryRejectsInvalidCatalogsSafely(t *testing.T) {
	for _, body := range []string{`<html>login</html>`, `{}`, `{"data":null}`, `{"data":[],"has_more":true}`, `{"data":[{"id":""}]}`, `{"data":[{"id":"bad\nmodel"}]}`, strings.Repeat("x", (4<<20)+1)} {
		t.Run(fmt.Sprint(len(body), body[:min(len(body), 20)]), func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
			defer provider.Close()
			a := setup(t)
			a.call("POST", "/v1/connections", fmt.Sprintf(`{"name":"Cloud","adapter":"custom","location_kind":"cloud","base_url":%q}`, provider.URL), 201)
			a.call("POST", "/v1/connections/1/discover", "", 502)
		})
	}
}

func TestProviderDiscoveryConfigurationAndRedirects(t *testing.T) {
	var calls atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer target.Close()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer provider.Close()
	a := setup(t)
	for _, body := range []string{
		fmt.Sprintf(`{"name":"Disabled","adapter":"custom","location_kind":"cloud","base_url":%q,"enabled":false}`, provider.URL),
		fmt.Sprintf(`{"name":"Native","adapter":"cartesia","location_kind":"cloud","base_url":%q}`, provider.URL),
		fmt.Sprintf(`{"name":"Missing key","adapter":"custom","location_kind":"cloud","base_url":%q,"credential_ref":"MISSING_CATALOG_KEY"}`, provider.URL),
	} {
		a.call("POST", "/v1/connections", body, 201)
	}
	t.Setenv("MISSING_CATALOG_KEY", "")
	for id := 1; id <= 3; id++ {
		a.call("POST", fmt.Sprintf("/v1/connections/%d/discover", id), "", 400)
		a.call("POST", fmt.Sprintf("/v1/connections/%d/models", id), `{"upstream_model_id":"model","task":"stt"}`, 400)
	}
	a.call("POST", "/v1/connections", fmt.Sprintf(`{"name":"Redirect","adapter":"custom","location_kind":"cloud","base_url":%q}`, provider.URL), 201)
	a.call("POST", "/v1/connections/4/discover", "", 502)
	if calls.Load() != 0 {
		t.Fatal("discovery followed redirect")
	}
	list := a.call("GET", "/v1/connections", "", 200).Body.String()
	if !strings.Contains(list, `"inference_supported":false`) || !strings.Contains(list, `"credential_ready":false`) {
		t.Fatal(list)
	}
	a.call("POST", "/v1/connections/4/models", `{"upstream_model_id":"model","task":"image"}`, 400)
	a.call("POST", "/v1/connections/4/models", `{"upstream_model_id":"model","task":"tts","capabilities_json":[]}`, 400)
}

func TestConnectionEndpointCompatibleRoots(t *testing.T) {
	for _, tt := range []struct{ adapter, base, want string }{
		{"openai", "https://api.example", "https://api.example/v1"},
		{"together", "https://api.example/v1/", "https://api.example/v1"},
		{"deepinfra", "https://api.example", "https://api.example/v1/openai"},
		{"openai-compatible", "https://api.example/v1/openai", "https://api.example/v1/openai"},
		{"local", "http://localhost:9000/prefix", "http://localhost:9000/prefix/v1"},
	} {
		endpoint, err := connectionEndpoint(db.Connection{Adapter: tt.adapter, BaseUrl: tt.base})
		if err != nil || endpoint.String() != tt.want {
			t.Fatalf("%+v: %v %v", tt, endpoint, err)
		}
	}
	for _, base := range []string{"https://user:pass@api.example", "https://api.example?secret=key", "https://api.example/#fragment", "file:///tmp/socket"} {
		if _, err := connectionEndpoint(db.Connection{BaseUrl: base}); err == nil {
			t.Fatal("accepted invalid root", base)
		}
	}
}

func TestProviderDiscoveryArrayAndImportRollback(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"id":"model","owned_by":"publisher","task":"stt"}]`)
	}))
	defer provider.Close()
	a := setup(t)
	a.call("POST", "/v1/connections", fmt.Sprintf(`{"name":"Cloud","adapter":"custom","location_kind":"cloud","base_url":%q}`, provider.URL), 201)
	found := a.call("POST", "/v1/connections/1/discover", "", 200)
	if !strings.Contains(found.Body.String(), `"id":"model"`) {
		t.Fatal(found.Body.String())
	}
	a.call("POST", "/v1/models", `{"name":"Existing","publisher":"test","task":"stt"}`, 201)
	a.call("POST", "/v1/deployments", `{"name":"model / stt / provider:1","model_id":1,"connection_id":1,"upstream_model_id":"another"}`, 201)
	a.call("POST", "/v1/connections/1/models", `{"upstream_model_id":"model","task":"stt"}`, 409)
	var count int
	if err := a.db.QueryRow("SELECT count(*) FROM models").Scan(&count); err != nil || count != 1 {
		t.Fatalf("orphan model after failed import: %d %v", count, err)
	}
}
