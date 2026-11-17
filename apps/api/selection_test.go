package main

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"voice-cloud/db"
)

func TestRouteSelection(t *testing.T) {
	test := setup(t)
	seedConfig(test)
	test.call("POST", "/v1/deployments", `{"name":"Second","model_id":1,"connection_id":1,"upstream_model_id":"second","pricing_json":{"request_nanos":10}}`, 201)
	test.call("PUT", "/v1/policies/1/targets/1", `{"priority":0}`, 200)
	test.call("PUT", "/v1/policies/1/targets/2", `{"priority":0}`, 200)
	test.call("PUT", "/v1/deployments/1/languages/en-US", "", 200)
	queries := db.New(test.db)
	a := &api{db: test.db, queries: queries}
	key := db.ApiKey{DefaultPolicyID: sql.NullInt64{Int64: 1, Valid: true}}
	ctx := context.Background()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := test.db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	selectIDs := func(model, language string, ids ...int64) {
		t.Helper()
		_, deployments, err := a.selectRoute(ctx, key, "stt", model, language, nil)
		if err != nil || len(deployments) != len(ids) {
			t.Fatalf("selection %q/%q: %v, deployments %#v", model, language, err, deployments)
		}
		for i, id := range ids {
			if deployments[i].ID != id {
				t.Fatalf("deployment %d: got %d, want %d", i, deployments[i].ID, id)
			}
		}
	}
	selectIDs("", "", 1, 2)
	selectIDs("policy:1", "en", 1, 2)
	selectIDs("policy:1", "EN-us", 1, 2)
	selectIDs("policy:1", "en-GB", 2)
	policy, deployments, err := a.selectRoute(ctx, key, "stt", "deployment:1", "", nil)
	if err != nil || policy.ID != 0 || policy.MaxAttempts != 1 || len(deployments) != 1 {
		t.Fatalf("explicit route: %#v %#v %v", policy, deployments, err)
	}
	for _, model := range []string{"large-v3", "policy:0", "deployment:-1", "policy:nope"} {
		if _, _, err := a.selectRoute(ctx, key, "stt", model, "", nil); err == nil {
			t.Fatalf("accepted invalid model %q", model)
		}
	}
	if _, _, err := a.selectRoute(ctx, db.ApiKey{}, "stt", "", "", nil); err == nil {
		t.Fatal("accepted missing default policy")
	}
	if _, _, err := a.selectRoute(ctx, key, "tts", "policy:1", "", nil); err == nil {
		t.Fatal("accepted task mismatch")
	}
	exec(`UPDATE routing_policies SET objective = 'cost'`)
	selectIDs("", "", 2, 1)
	exec(`UPDATE policy_targets SET priority = 1 WHERE deployment_id = 2`)
	selectIDs("", "", 1, 2)
	exec(`UPDATE policy_targets SET priority = 0`)
	exec(`UPDATE routing_policies SET objective = 'latency'`)
	deployment, err := queries.GetDeployment(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := queries.GetConnection(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	_, err = queries.SetDeploymentHealth(ctx, db.SetDeploymentHealthParams{
		DeploymentID: 2, ProbeKind: "reachability", Status: "healthy", CheckedAt: now,
		ExpiresAt: now + 60000, LatencyMs: sql.NullInt64{Int64: 10, Valid: true},
		ConfigHash: db.HealthConfigHash(deployment, connection),
	})
	if err != nil {
		t.Fatal(err)
	}
	selectIDs("", "", 1, 2) // A fast reachability probe is not inference latency.
	test.call("POST", "/v1/api-keys", `{"name":"Metrics"}`, 201)
	exec(`INSERT INTO requests (id,api_key_id,task,transport,routing_snapshot_json,status,started_at,finished_at) VALUES ('metrics',1,'stt','http','{}','succeeded',?,?)`, now, now)
	exec(`INSERT INTO attempts (id,request_id,deployment_id,attempt_number,status,started_at,first_output_at,finished_at,selection_reason) VALUES ('metrics','metrics',2,1,'succeeded',?,?,?,'test')`, now, now, now)
	selectIDs("", "", 2, 1)
	exec(`UPDATE routing_policies SET constraints_json = '{"location_kind":"local","max_latency_ms":20}'`)
	selectIDs("", "", 2)
	exec(`UPDATE deployment_health SET status = 'unhealthy'`)
	_, _, err = a.selectRoute(ctx, key, "stt", "", "", nil)
	var problem httpError
	if !errors.As(err, &problem) || problem.status != 503 {
		t.Fatalf("unhealthy route: %v", err)
	}
	exec(`UPDATE routing_policies SET constraints_json = '{}'`)
	selectIDs("", "", 1)
	exec(`UPDATE deployment_health SET checked_at = ?, expires_at = ?`, now-2000, now-1)
	selectIDs("", "", 2, 1)
	for _, constraints := range []string{`{"unsupported":true}`, `{"location_kind":null}`, `{"location_kind":""}`, `{"max_latency_ms":null}`, `{"max_latency_ms":-1}`} {
		exec(`UPDATE routing_policies SET constraints_json = ?`, constraints)
		if _, _, err := a.selectRoute(ctx, key, "stt", "", "", nil); err == nil {
			t.Fatalf("ignored unsupported constraint: %s", constraints)
		}
	}
	exec(`UPDATE routing_policies SET constraints_json = '{}'`)
	exec(`UPDATE connections SET enabled = 0`)
	if _, _, err := a.selectRoute(ctx, key, "stt", "deployment:1", "", nil); err == nil {
		t.Fatal("selected disabled connection")
	}
}
