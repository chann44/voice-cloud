package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"voice-cloud/db"
)

func enableSecretStorage(t *testing.T) {
	t.Helper()
	t.Setenv("SECRETS_MASTER_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
}

func TestStoredProviderKeyLifecycle(t *testing.T) {
	enableSecretStorage(t)
	a := setup(t)
	key := "provider-plaintext-never-return"
	body := fmt.Sprintf(`{"name":"Cloud","adapter":"custom","location_kind":"cloud","base_url":"https://provider.test","api_key":%q,"credential_ref":"OLD_ENV_KEY"}`, key)
	response := a.call("POST", "/v1/connections", body, 201)
	if strings.Contains(response.Body.String(), key) || strings.Contains(response.Body.String(), `"api_key":`) {
		t.Fatal("key returned from mutation")
	}
	row := jsonObject(t, response)
	if row["has_api_key"] != true || row["credential_ready"] != true || row["credential_ref"] != nil {
		t.Fatal(row)
	}
	secret, err := db.New(a.db).GetProviderSecret(context.Background(), 1)
	if err != nil || bytes.Contains(secret.Ciphertext, []byte(key)) {
		t.Fatal("plaintext persisted", err)
	}
	cipher, err := configuredSecretCipher()
	if err != nil {
		t.Fatal(err)
	}
	if err := validateStoredSecrets(context.Background(), a.db, cipher); err != nil {
		t.Fatal(err)
	}
	a.handler = newHandler(a.db, testToken)
	read := a.call("GET", "/v1/connections", "", 200)
	if strings.Contains(read.Body.String(), key) {
		t.Fatal("key returned from read")
	}
	update := `{"name":"Renamed","adapter":"custom","location_kind":"cloud","base_url":"https://provider.test","api_key":""}`
	a.call("PUT", "/v1/connections/1", update, 200)
	kept, _ := db.New(a.db).GetProviderSecret(context.Background(), 1)
	if !bytes.Equal(secret.Ciphertext, kept.Ciphertext) {
		t.Fatal("blank key should preserve ciphertext")
	}
	a.call("PUT", "/v1/connections/1", strings.Replace(update, `"https://provider.test"`, `"https://other.test"`, 1), 400)
	updated := strings.Replace(update, `"api_key":""`, `"api_key":"replacement"`, 1)
	a.call("PUT", "/v1/connections/1", updated, 200)
	replacement, _ := db.New(a.db).GetProviderSecret(context.Background(), 1)
	if bytes.Equal(secret.Ciphertext, replacement.Ciphertext) {
		t.Fatal("key not replaced")
	}
	connection, _ := db.New(a.db).GetConnection(context.Background(), 1)
	service := &api{queries: db.New(a.db), secrets: cipher}
	request := httptest.NewRequest("GET", "https://provider.test/v1/models", nil)
	if err := service.authenticateProvider(context.Background(), request, connection); err != nil || request.Header.Get("Authorization") != "Bearer replacement" {
		t.Fatal("replacement not used", err)
	}
	a.call("PUT", "/v1/connections/1", strings.TrimSuffix(update, "}")+`,"remove_api_key":true,"credential_ref":"OLD_ENV_KEY"}`, 200)
	removed := jsonObject(t, a.call("GET", "/v1/connections/1", "", 200))
	if removed["has_api_key"] != false || removed["credential_ref"] != nil {
		t.Fatal("removal did not clear both credential sources", removed)
	}
	var count int
	_ = a.db.QueryRow("SELECT count(*) FROM provider_secrets").Scan(&count)
	if count != 0 {
		t.Fatal("removed secret still persisted")
	}
}

func TestStoredProviderKeyActuallyAuthenticatesDiscoveryAndInference(t *testing.T) {
	enableSecretStorage(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer saved-provider-key" {
			t.Error("saved credential not forwarded")
		}
		if r.Method == "GET" {
			fmt.Fprint(w, `{"data":[{"id":"model"}]}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"Works"}`)
	}))
	defer upstream.Close()
	a := setup(t)
	a.call("POST", "/v1/connections", fmt.Sprintf(`{"name":"Cloud","adapter":"custom","location_kind":"cloud","base_url":%q,"api_key":"saved-provider-key"}`, upstream.URL), 201)
	a.call("POST", "/v1/connections/1/discover", "", 200)
	a.call("POST", "/v1/connections/1/models", `{"upstream_model_id":"model","task":"stt"}`, 201)
	key := jsonObject(t, a.call("POST", "/v1/api-keys", `{"name":"App"}`, 201))["key"].(string)
	raw, media := audioUpload(t, "deployment:1", "json")
	callInference(t, a, key, "/v1/audio/transcriptions", media, raw, nil, 200)
}

func TestStoredSecretsFailClosedAndBindToConnection(t *testing.T) {
	enableSecretStorage(t)
	a := setup(t)
	a.call("POST", "/v1/connections", `{"name":"One","adapter":"custom","location_kind":"cloud","base_url":"https://provider.test","api_key":"one-key"}`, 201)
	a.call("POST", "/v1/connections", `{"name":"Two","adapter":"custom","location_kind":"cloud","base_url":"https://provider.test","api_key":"two-key"}`, 201)
	cipher, _ := configuredSecretCipher()
	service := &api{queries: db.New(a.db), secrets: cipher}
	second, _ := service.queries.GetConnection(context.Background(), 2)
	first, _ := service.queries.GetProviderSecret(context.Background(), 1)
	if _, err := service.decryptProviderSecret(second, first.Ciphertext); err == nil {
		t.Fatal("ciphertext moved between connections")
	}
	if err := validateStoredSecrets(context.Background(), a.db, nil); err == nil {
		t.Fatal("missing master key accepted")
	}
	t.Setenv("SECRETS_MASTER_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)))
	wrong, _ := configuredSecretCipher()
	if err := validateStoredSecrets(context.Background(), a.db, wrong); err == nil {
		t.Fatal("wrong master key accepted")
	}
	first.Ciphertext[len(first.Ciphertext)-1] ^= 1
	if _, err := a.db.Exec("UPDATE provider_secrets SET ciphertext=? WHERE connection_id=1", first.Ciphertext); err != nil {
		t.Fatal(err)
	}
	if err := validateStoredSecrets(context.Background(), a.db, cipher); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
	a.call("DELETE", "/v1/connections/1", "", 204)
	var count int
	_ = a.db.QueryRow("SELECT count(*) FROM provider_secrets WHERE connection_id=1").Scan(&count)
	if count != 0 {
		t.Fatal("connection deletion left secret")
	}
}

func TestProviderKeyConfigurationAndValidation(t *testing.T) {
	t.Setenv("SECRETS_MASTER_KEY", "")
	a := setup(t)
	base := `{"name":"Cloud","adapter":"custom","location_kind":"cloud","base_url":"https://provider.test"`
	a.call("POST", "/v1/connections", base+`,"api_key":"secret"}`, 503)
	rows := a.call("GET", "/v1/connections", "", 200)
	if strings.TrimSpace(rows.Body.String()) != "[]" {
		t.Fatal("failed credential save created connection")
	}
	for _, key := range []string{"bad\nkey", "bad key", strings.Repeat("x", 4097)} {
		a.call("POST", "/v1/connections", base+fmt.Sprintf(`,"api_key":%q}`, key), 400)
	}
	a.call("POST", "/v1/connections", base+`,"api_key":"secret","remove_api_key":true}`, 400)
	for _, value := range []string{"not-base64", base64.StdEncoding.EncodeToString([]byte("short"))} {
		t.Setenv("SECRETS_MASTER_KEY", value)
		if _, err := configuredSecretCipher(); err == nil {
			t.Fatal("invalid master key accepted")
		}
	}
}

func TestMasterKeyCannotBeUsedAsUpstreamCredential(t *testing.T) {
	enableSecretStorage(t)
	a := setup(t)
	a.call("POST", "/v1/connections", `{"name":"Cloud","adapter":"custom","location_kind":"cloud","base_url":"https://provider.test","credential_ref":"SECRETS_MASTER_KEY"}`, 400)
	if environmentCredential("SECRETS_MASTER_KEY") != "" {
		t.Fatal("master key available to upstream credential lookup")
	}
	if _, err := parseDecisionConfig([]byte(`{"url":"https://decision.test","model":"decision","credential_ref":"SECRETS_MASTER_KEY"}`), "stt"); err == nil {
		t.Fatal("master key accepted for decision service")
	}
}
