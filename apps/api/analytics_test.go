package main

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"voice-cloud/db"
)

func TestAnalyticsCountsWholeWindowAndSeparatesUnknownCost(t *testing.T) {
	test := setup(t)
	seedConfig(test)
	test.call("POST", "/v1/api-keys", `{"name":"App"}`, 201)
	now := time.Now().Add(-time.Minute).UnixMilli()
	for index := range 125 {
		started := now - int64(index)*1000
		id := fmt.Sprintf("r%d", index)
		if _, err := test.db.Exec(`INSERT INTO requests (id,api_key_id,task,transport,routing_snapshot_json,status,started_at,finished_at) VALUES (?,1,'stt','http','{}','succeeded',?,?)`, id, started, started+100); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := test.db.Exec(`INSERT INTO requests (id,api_key_id,task,transport,routing_snapshot_json,status,started_at,finished_at) VALUES ('old',1,'stt','http','{}','failed',?,?)`, now-(25*time.Hour).Milliseconds(), now-(25*time.Hour).Milliseconds()+100); err != nil {
		t.Fatal(err)
	}
	if _, err := test.db.Exec(`INSERT INTO attempts (id,request_id,deployment_id,attempt_number,status,started_at,finished_at,selection_reason) VALUES ('a1','r0',1,1,'failed',?,?, 'priority'), ('a2','r0',1,2,'succeeded',?,?,'fallback')`, now, now+10, now+10, now+100); err != nil {
		t.Fatal(err)
	}
	if _, err := test.db.Exec(`INSERT INTO attempt_usage (attempt_id,metric,quantity,unit,cost_nanos,cost_status) VALUES ('a2','requests',1,'requests',25,'estimated')`); err != nil {
		t.Fatal(err)
	}
	if _, err := test.db.Exec(`UPDATE requests SET routing_snapshot_json='{"decision":{"attempted":true,"cost_nanos":5,"cost_status":"estimated"}}' WHERE id='r0'`); err != nil {
		t.Fatal(err)
	}
	if _, err := test.db.Exec(`UPDATE requests SET routing_snapshot_json='{"decision":{"attempted":true,"cost_nanos":null,"cost_status":"unknown"}}' WHERE id='r1'`); err != nil {
		t.Fatal(err)
	}
	a := &api{db: test.db, queries: db.New(test.db)}
	response := httptest.NewRecorder()
	if err := a.analytics(response, httptest.NewRequest("GET", "/v1/analytics", nil)); err != nil {
		t.Fatal(err)
	}
	var report trafficAnalytics
	if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Requests != 125 || report.Succeeded != 125 || report.Failed != 0 || report.Attempts != 2 || report.FallbackRequests != 1 || report.KnownCostNanos != 30 || report.UnknownCostAttempts != 1 || !report.DecisionCostIncluded || report.DecisionKnownCostNanos != 5 || report.UnknownDecisions != 1 {
		t.Fatalf("incorrect totals: %#v", report)
	}
	var hourly int64
	for _, hour := range report.Hourly {
		hourly += hour.Requests
	}
	if len(report.Hourly) != 24 || hourly != 125 || report.AverageDuration != float64(100) || report.P95Duration != float64(100) {
		t.Fatalf("incorrect window: %#v", report)
	}
}

func TestAnalyticsEmptyInstallation(t *testing.T) {
	test := setup(t)
	a := &api{db: test.db, queries: db.New(test.db)}
	response := httptest.NewRecorder()
	if err := a.analytics(response, httptest.NewRequest("GET", "/v1/analytics", nil)); err != nil {
		t.Fatal(err)
	}
	var report trafficAnalytics
	if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Requests != 0 || report.AverageDuration != nil || report.P50Duration != nil || report.P95Duration != nil || len(report.Hourly) != 24 {
		t.Fatalf("unexpected empty analytics: %#v", report)
	}
}
