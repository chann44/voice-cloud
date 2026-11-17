package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"voice-cloud/db"
)

// shortcut: inspect at most 1 MiB per JSON response or SSE line; use incremental JSON parsing for larger usage envelopes.
const usageInspectionLimit = 1 << 20

type usageObserver struct {
	sse, done, failed, overflow bool
	buffer                      []byte
	metrics                     map[string]int64
}

func (o *usageObserver) Write(data []byte) {
	if !o.sse {
		if o.overflow {
			return
		}
		if len(o.buffer)+len(data) > usageInspectionLimit {
			o.buffer = nil
			o.overflow = true
			return
		}
		o.buffer = append(o.buffer, data...)
		return
	}
	for len(data) > 0 {
		line, rest, found := bytes.Cut(data, []byte{'\n'})
		if !o.overflow {
			if len(o.buffer)+len(line) > usageInspectionLimit {
				o.buffer = nil
				o.overflow = true
			} else {
				o.buffer = append(o.buffer, line...)
			}
		}
		if !found {
			return
		}
		if !o.overflow {
			o.event(o.buffer)
		}
		o.buffer = nil
		o.overflow = false
		data = rest
	}
}

func (o *usageObserver) event(line []byte) {
	if !bytes.HasPrefix(line, []byte("data:")) {
		return
	}
	value := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
	if bytes.Equal(value, []byte("[DONE]")) {
		o.done = true
		return
	}
	o.inspect(value)
}

func (o *usageObserver) finish() {
	if o.overflow {
		return
	}
	if o.sse {
		o.event(o.buffer)
	} else {
		o.inspect(o.buffer)
	}
}

func (o *usageObserver) inspect(value []byte) {
	var envelope struct {
		Usage    map[string]json.RawMessage `json:"usage"`
		Error    json.RawMessage            `json:"error"`
		Type     string                     `json:"type"`
		Duration *float64                   `json:"duration"`
	}
	if json.Unmarshal(value, &envelope) != nil {
		return
	}
	if (len(envelope.Error) > 0 && string(envelope.Error) != "null") || envelope.Type == "error" {
		o.failed = true
	}
	if o.metrics == nil {
		o.metrics = map[string]int64{}
	}
	for _, metric := range []struct {
		target  string
		sources []string
	}{
		{"input_tokens", []string{"input_tokens", "prompt_tokens"}},
		{"output_tokens", []string{"output_tokens", "completion_tokens"}},
		{"total_tokens", []string{"total_tokens"}},
	} {
		for _, source := range metric.sources {
			var quantity int64
			if raw, ok := envelope.Usage[source]; ok && string(raw) != "null" && json.Unmarshal(raw, &quantity) == nil && quantity >= 0 {
				o.metrics[metric.target] = quantity
				break
			}
		}
	}
	seconds := envelope.Duration
	if seconds == nil {
		var duration float64
		if raw, ok := envelope.Usage["seconds"]; ok && string(raw) != "null" && json.Unmarshal(raw, &duration) == nil {
			seconds = &duration
		}
	}
	if seconds != nil && *seconds >= 0 && *seconds < float64(math.MaxInt64)/1000 {
		o.metrics["audio_ms"] = int64(math.Round(*seconds * 1000))
	}
}

func (a *api) finishInferenceAttempt(id, status, code, providerID string, first int64, deployment db.Deployment, input *inferenceInput, observer *usageObserver) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	q := a.queries.WithTx(tx)
	if providerID != "" {
		if _, err = q.SetAttemptProviderRequestID(ctx, db.SetAttemptProviderRequestIDParams{ID: id, ProviderRequestID: sql.NullString{String: providerID, Valid: true}}); err != nil {
			return err
		}
	}
	if first != 0 {
		if _, err = q.SetAttemptFirstOutput(ctx, db.SetAttemptFirstOutputParams{ID: id, FirstOutputAt: sql.NullInt64{Int64: first, Valid: true}}); err != nil {
			return err
		}
	}
	if _, err = q.FinishAttempt(ctx, db.FinishAttemptParams{ID: id, Status: status, FinishedAt: sql.NullInt64{Int64: time.Now().UnixMilli(), Valid: true}, ErrorCode: sql.NullString{String: code, Valid: code != ""}}); err != nil {
		return err
	}
	usage := map[string]int64{"requests": 1}
	if observer != nil {
		observer.finish()
		for metric, quantity := range observer.metrics {
			usage[metric] = quantity
		}
		if input.task == "tts" {
			var text string
			if json.Unmarshal(input.fields["input"], &text) == nil {
				usage["input_characters"] = int64(utf8.RuneCountInString(text))
			}
		}
	}
	pricing, pricingErr := parsePricing(deployment.PricingJson)
	completeUsage := true
	expected := []string{"input_tokens", "output_tokens"}
	if input.task == "tts" {
		expected = []string{"input_characters"}
	} else if input.task == "stt" {
		expected = []string{"audio_ms"}
	}
	for _, metric := range expected {
		quantity, ok := usage[metric]
		if !ok || metricCost(pricing, metric, quantity) == nil {
			completeUsage = false
		}
	}
	for metric, quantity := range usage {
		unit := "tokens"
		switch {
		case metric == "requests":
			unit = "requests"
		case metric == "audio_ms":
			unit = "audio_ms"
		case strings.HasSuffix(metric, "characters"):
			unit = "characters"
		}
		row := db.CreateAttemptUsageParams{AttemptID: id, Metric: metric, Quantity: quantity, Unit: unit, RateSnapshotJson: deployment.PricingJson, Currency: "USD", CostStatus: "unknown"}
		cost := metricCost(pricing, metric, quantity)
		if pricingErr == nil && status == "succeeded" && pricing.Request != nil {
			// A flat request price covers the entire attempt; other metrics are informational.
			value := int64(0)
			if metric == "requests" {
				value = *pricing.Request
			}
			cost = &value
		} else if metric == "total_tokens" || metric == "requests" && pricing.Request == nil && completeUsage {
			value := int64(0)
			cost = &value
		}
		if pricingErr == nil && cost != nil && (status == "succeeded" || metric != "requests") {
			row.CostNanos = sql.NullInt64{Int64: *cost, Valid: true}
			row.CostStatus = "estimated"
		}
		if _, err = q.CreateAttemptUsage(ctx, row); err != nil {
			return err
		}
	}
	return tx.Commit()
}
