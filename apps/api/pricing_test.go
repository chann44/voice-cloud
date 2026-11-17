package main

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
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

func TestReportedUnitCostsAndMissingUsage(t *testing.T) {
	for _, reported := range []bool{false, true} {
		t.Run(fmt.Sprint(reported), func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if reported {
					fmt.Fprint(w, `{"choices":[{"message":{"content":"Hello"}}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`)
				} else {
					fmt.Fprint(w, `{"choices":[{"message":{"content":"Hello"}}]}`)
				}
			}))
			defer provider.Close()
			a, key := setupInference(t, "llm", provider.URL)
			if _, err := a.db.Exec(`UPDATE deployments SET pricing_json='{"input_token_nanos":10,"output_token_nanos":20}'`); err != nil {
				t.Fatal(err)
			}
			response := callInference(t, a, key, "/v1/chat/completions", "application/json", []byte(`{"messages":[{"role":"user","content":"Hello"}]}`), nil, 200)
			var cost sql.NullInt64
			if err := a.db.QueryRow(`SELECT sum(u.cost_nanos) FROM attempt_usage u JOIN attempts a ON a.id=u.attempt_id WHERE a.request_id=?`, response.Header().Get("X-Request-ID")).Scan(&cost); err != nil {
				t.Fatal(err)
			}
			if reported && (!cost.Valid || cost.Int64 != 70) {
				t.Fatalf("unit cost: %+v", cost)
			}
			if !reported && cost.Valid {
				t.Fatalf("missing usage appeared free: %+v", cost)
			}
		})
	}
}

func TestEstimatedRoutingCostRequiresKnownBounds(t *testing.T) {
	deployment := db.Deployment{PricingJson: `{"input_token_nanos":10,"output_token_nanos":20}`}
	input := &inferenceInput{task: "llm"}
	if estimatedRoutingCost(input, requestRequirements{contextBudget: 100}, deployment) != nil {
		t.Fatal("unbounded output got a cost promise")
	}
	cost := estimatedRoutingCost(input, requestRequirements{contextBudget: 100, outputTokens: 20}, deployment)
	if cost == nil || *cost != 1400 {
		t.Fatalf("bounded text estimate: %v", cost)
	}
	if estimatedRoutingCost(input, requestRequirements{contextBudget: 100, outputTokens: 20, vision: true}, deployment) != nil {
		t.Fatal("image cost guessed from text bytes")
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
