package main

import (
	"context"
	"database/sql"
	"math"
	"testing"
	"voice-cloud/db"
)

func TestPricingRatesAndOverflow(t *testing.T) {
	pricing, err := parsePricing(`{"input_token_nanos":10,"output_token_nanos":20}`)
	if err != nil {
		t.Fatal(err)
	}
	if cost := metricCost(pricing, "input_tokens", 100); cost == nil || *cost != 1000 {
		t.Fatalf("input cost %v", cost)
	}
	if cost := decisionCost(&pricing, []byte(`{"input_tokens":100,"output_tokens":25}`)); cost == nil || *cost != 1500 {
		t.Fatalf("decision cost %v", cost)
	}
	if decisionCost(&pricing, []byte(`{"input_tokens":100}`)) != nil {
		t.Fatal("missing usage treated as free")
	}
	if metricCost(pricing, "input_tokens", math.MaxInt64) != nil {
		t.Fatal("overflow accepted")
	}
	for _, raw := range []string{`{"request_nanos":null}`, `{"audio_ms_nanos":-1}`, `{"input_character_nanos":0.5}`, `[]`} {
		if _, err := parsePricing(raw); err == nil {
			t.Fatalf("invalid pricing accepted %s", raw)
		}
	}
}

func TestPolicyEstimatedCostCapFailsClosed(t *testing.T) {
	test := setup(t)
	seedConfig(test)
	test.call("POST", "/v1/deployments", `{"name":"Priced","model_id":1,"connection_id":1,"upstream_model_id":"priced","pricing_json":{"request_nanos":10}}`, 201)
	test.call("PUT", "/v1/policies/1/targets/1", `{"priority":0}`, 200)
	test.call("PUT", "/v1/policies/1/targets/2", `{"priority":0}`, 200)
	a := &api{db: test.db, queries: db.New(test.db)}
	key := db.ApiKey{DefaultPolicyID: sql.NullInt64{Int64: 1, Valid: true}}
	if _, err := test.db.Exec(`UPDATE routing_policies SET constraints_json='{"max_estimated_attempt_cost_nanos":15}'`); err != nil {
		t.Fatal(err)
	}
	_, deployments, err := a.selectRoute(context.Background(), key, "stt", "", "", nil)
	if err != nil || len(deployments) != 1 || deployments[0].ID != 2 {
		t.Fatalf("unknown cost passed budget: %+v %v", deployments, err)
	}
	if _, err := test.db.Exec(`UPDATE routing_policies SET constraints_json='{"max_estimated_attempt_cost_nanos":5}'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.selectRoute(context.Background(), key, "stt", "", "", nil); err == nil {
		t.Fatal("over-budget route accepted")
	}
}
