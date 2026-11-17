package main

import (
	"database/sql"
	"net/http"
	"time"
)

type trafficHour struct {
	StartedAt int64 `json:"started_at"`
	Requests  int64 `json:"requests"`
}

type trafficAnalytics struct {
	Since                  int64         `json:"since"`
	Until                  int64         `json:"until"`
	Requests               int64         `json:"requests"`
	Succeeded              int64         `json:"succeeded"`
	Failed                 int64         `json:"failed"`
	Cancelled              int64         `json:"cancelled"`
	InProgress             int64         `json:"in_progress"`
	Attempts               int64         `json:"attempts"`
	FallbackRequests       int64         `json:"fallback_requests"`
	AverageDuration        any           `json:"avg_duration_ms"`
	P50Duration            any           `json:"p50_duration_ms"`
	P95Duration            any           `json:"p95_duration_ms"`
	KnownCostNanos         int64         `json:"known_cost_nanos"`
	UnknownCostAttempts    int64         `json:"unknown_cost_attempts"`
	DecisionKnownCostNanos int64         `json:"decision_cost_nanos"`
	UnknownDecisions       int64         `json:"unknown_decisions"`
	DecisionCostIncluded   bool          `json:"decision_cost_included"`
	Hourly                 []trafficHour `json:"hourly"`
}

func (a *api) analytics(w http.ResponseWriter, r *http.Request) error {
	until := time.Now().UnixMilli()
	since := until - (24 * time.Hour).Milliseconds()
	report := trafficAnalytics{Since: since, Until: until, Hourly: make([]trafficHour, 24), DecisionCostIncluded: true}
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var average sql.NullFloat64
	var p50, p95 sql.NullInt64
	err = tx.QueryRowContext(r.Context(), `
 WITH recent AS (SELECT * FROM requests WHERE started_at >= ? AND started_at < ?),
 completed AS (SELECT finished_at-started_at AS duration FROM recent WHERE finished_at IS NOT NULL),
 recent_attempts AS (SELECT a.* FROM attempts a JOIN recent r ON r.id=a.request_id)
 SELECT COUNT(*), COUNT(CASE WHEN status='succeeded' THEN 1 END),
 COUNT(CASE WHEN status='failed' THEN 1 END), COUNT(CASE WHEN status='cancelled' THEN 1 END),
 COUNT(CASE WHEN status IN ('pending','running') THEN 1 END),
 (SELECT COUNT(*) FROM recent_attempts),
 (SELECT COUNT(*) FROM recent WHERE EXISTS (SELECT 1 FROM attempts WHERE request_id=recent.id AND attempt_number>1)),
 (SELECT AVG(duration) FROM completed),
 (SELECT duration FROM completed ORDER BY duration LIMIT 1 OFFSET (SELECT MAX(0,(COUNT(*)*50+99)/100-1) FROM completed)),
 (SELECT duration FROM completed ORDER BY duration LIMIT 1 OFFSET (SELECT MAX(0,(COUNT(*)*95+99)/100-1) FROM completed)),
 (SELECT COALESCE(SUM(u.cost_nanos),0) FROM attempt_usage u JOIN recent_attempts a ON a.id=u.attempt_id),
 (SELECT COUNT(*) FROM recent_attempts a WHERE NOT EXISTS (SELECT 1 FROM attempt_usage WHERE attempt_id=a.id AND cost_nanos IS NOT NULL)
   OR EXISTS (SELECT 1 FROM attempt_usage WHERE attempt_id=a.id AND metric!='total_tokens' AND cost_status='unknown')),
 (SELECT COALESCE(SUM(json_extract(routing_snapshot_json,'$.decision.cost_nanos')),0) FROM recent),
 (SELECT COUNT(*) FROM recent WHERE json_extract(routing_snapshot_json,'$.decision.attempted')=1 AND json_extract(routing_snapshot_json,'$.decision.cost_nanos') IS NULL)
 FROM recent`, since, until).Scan(&report.Requests, &report.Succeeded, &report.Failed, &report.Cancelled, &report.InProgress, &report.Attempts, &report.FallbackRequests, &average, &p50, &p95, &report.KnownCostNanos, &report.UnknownCostAttempts, &report.DecisionKnownCostNanos, &report.UnknownDecisions)
	if err != nil {
		return err
	}
	report.KnownCostNanos += report.DecisionKnownCostNanos
	if average.Valid {
		report.AverageDuration = average.Float64
	}
	report.P50Duration = intValue(p50)
	report.P95Duration = intValue(p95)
	for index := range report.Hourly {
		report.Hourly[index].StartedAt = since + int64(index)*time.Hour.Milliseconds()
	}
	rows, err := tx.QueryContext(r.Context(), `SELECT (started_at-?)/3600000 AS bucket, COUNT(*) FROM requests WHERE started_at>=? AND started_at<? GROUP BY bucket`, since, since, until)
	if err != nil {
		return err
	}
	for rows.Next() {
		var bucket, count int64
		if err := rows.Scan(&bucket, &count); err != nil {
			rows.Close()
			return err
		}
		if bucket >= 0 && bucket < int64(len(report.Hourly)) {
			report.Hourly[bucket].Requests = count
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return send(w, http.StatusOK, report)
}
