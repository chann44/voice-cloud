package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"voice-cloud/db"
)

func (a *api) selectRoute(ctx context.Context, key db.ApiKey, task, model, language string, input *inferenceInput) (db.RoutingPolicy, []db.Deployment, error) {
	if err := taskValid(task); err != nil {
		return db.RoutingPolicy{}, nil, err
	}
	requirements, err := requiredCapabilities(input)
	if err != nil {
		return db.RoutingPolicy{}, nil, err
	}
	policy := db.RoutingPolicy{Task: task, Objective: "balanced", MaxAttempts: 1, ConstraintsJson: "{}", DecisionJson: "{}"}
	if language != "" && !languageTag.MatchString(language) {
		return policy, nil, invalid("invalid language tag")
	}
	kind, value, explicit := strings.Cut(model, ":")
	id := key.DefaultPolicyID.Int64
	if model != "" {
		var err error
		id, err = strconv.ParseInt(value, 10, 64)
		if !explicit || (kind != "policy" && kind != "deployment") || err != nil || id < 1 {
			return policy, nil, invalid("model must be policy:<id> or deployment:<id>")
		}
	} else if !key.DefaultPolicyID.Valid {
		return policy, nil, invalid("model or a default routing policy is required")
	}
	tx, err := a.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return policy, nil, err
	}
	defer tx.Rollback()
	queries := a.queries.WithTx(tx)
	var candidates []db.Deployment
	priorities := map[int64]int64{}
	var constraints struct {
		LocationKind                 string `json:"location_kind"`
		MaxLatencyMs                 *int64 `json:"max_latency_ms"`
		MaxEstimatedAttemptCostNanos *int64 `json:"max_estimated_attempt_cost_nanos"`
	}
	if kind == "deployment" {
		deployment, err := queries.GetDeployment(ctx, id)
		if err != nil {
			return policy, nil, err
		}
		candidates = []db.Deployment{deployment}
	} else {
		policy, err = queries.GetRoutingPolicy(ctx, id)
		if err != nil {
			return policy, nil, err
		}
		if policy.Task != task {
			return policy, nil, invalid("policy task does not match endpoint")
		}
		decoder := json.NewDecoder(strings.NewReader(policy.ConstraintsJson))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&constraints); err != nil ||
			(constraints.LocationKind != "" && constraints.LocationKind != "local" && constraints.LocationKind != "cloud") ||
			(constraints.MaxLatencyMs != nil && *constraints.MaxLatencyMs < 0) ||
			(constraints.MaxEstimatedAttemptCostNanos != nil && *constraints.MaxEstimatedAttemptCostNanos < 0) {
			return policy, nil, invalid("unsupported routing constraints; use location_kind, max_latency_ms, and max_estimated_attempt_cost_nanos")
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal([]byte(policy.ConstraintsJson), &fields); err != nil {
			return policy, nil, err
		}
		for name, value := range fields {
			if string(value) == "null" || (name == "location_kind" && constraints.LocationKind == "") {
				return policy, nil, invalid("routing constraints must have a nonnull supported value")
			}
		}
		candidates, err = queries.ListPolicyDeployments(ctx, id)
		if err != nil {
			return policy, nil, err
		}
		targets, err := queries.ListPolicyTargets(ctx, id)
		if err != nil {
			return policy, nil, err
		}
		for _, target := range targets {
			priorities[target.DeploymentID] = target.Priority
		}
	}
	eligible := make([]db.Deployment, 0, len(candidates))
	metrics := map[int64]int64{}
	exclusions := map[string]int{}
	now := time.Now().UnixMilli()
	for _, deployment := range candidates {
		connection, err := queries.GetConnection(ctx, deployment.ConnectionID)
		if err != nil {
			return policy, nil, err
		}
		configuredModel, err := queries.GetModel(ctx, deployment.ModelID)
		if err != nil {
			return policy, nil, err
		}
		if configuredModel.Task != task {
			return policy, nil, invalid("deployment task does not match endpoint")
		}
		if deployment.Enabled != 1 || connection.Enabled != 1 {
			exclusions["disabled"]++
			continue
		}
		if _, err := deploymentResponseLimits(deployment.SettingsJson); err != nil {
			exclusions["invalid_settings"]++
			continue
		}
		if reason := adapterRequestReason(connection.Adapter, input, deployment); reason != "" {
			if kind == "deployment" {
				return policy, nil, invalid("deployment is incompatible with this request: " + reason)
			}
			exclusions[reason]++
			continue
		}
		if constraints.LocationKind != "" && constraints.LocationKind != connection.LocationKind {
			exclusions["location"]++
			continue
		}
		if reason := capabilityReason(input, requirements, deployment); reason != "" {
			if kind == "deployment" {
				return policy, nil, invalid("deployment is incompatible with this request: " + reason)
			}
			exclusions[reason]++
			continue
		}
		if language != "" {
			languages, err := queries.ListDeploymentLanguages(ctx, deployment.ID)
			if err != nil {
				return policy, nil, err
			}
			matches := len(languages) == 0
			for _, supported := range languages {
				left, right := strings.ToLower(language), strings.ToLower(supported.LanguageTag)
				if left == right || strings.HasPrefix(left, right+"-") || strings.HasPrefix(right, left+"-") {
					matches = true
				}
			}
			if !matches {
				exclusions["language"]++
				continue
			}
		}
		health, err := queries.GetDeploymentHealth(ctx, deployment.ID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return policy, nil, err
		}
		fresh := err == nil && health.CheckedAt <= now && health.ExpiresAt > now && health.ConfigHash == db.HealthConfigHash(deployment, connection)
		if fresh && health.Status == "unhealthy" {
			exclusions["unhealthy"]++
			continue
		}
		since := now - 10*time.Minute.Milliseconds()
		if deployment.UpdatedAt > since {
			since = deployment.UpdatedAt
		}
		if connection.UpdatedAt > since {
			since = connection.UpdatedAt
		}
		recent, err := queries.ListRecentDeploymentAttempts(ctx, db.ListRecentDeploymentAttemptsParams{DeploymentID: deployment.ID, StartedAt: since})
		if err != nil {
			return policy, nil, err
		}
		latency, cooldown := inferencePerformance(recent, now)
		if cooldown {
			exclusions["cooldown"]++
			continue
		}
		if constraints.MaxLatencyMs != nil && (latency == math.MaxInt64 || latency > *constraints.MaxLatencyMs) {
			exclusions["latency"]++
			continue
		}
		cost := estimatedRoutingCost(input, requirements, deployment)
		if constraints.MaxEstimatedAttemptCostNanos != nil {
			if cost == nil {
				exclusions["pricing_unknown"]++
				continue
			}
			if *cost > *constraints.MaxEstimatedAttemptCostNanos {
				exclusions["cost_limit"]++
				continue
			}
		}
		metrics[deployment.ID] = latency
		if policy.Objective == "cost" {
			metrics[deployment.ID] = math.MaxInt64
			if cost != nil {
				metrics[deployment.ID] = *cost
			}
		}
		eligible = append(eligible, deployment)
	}
	if policy.Objective == "cost" || policy.Objective == "latency" {
		sort.SliceStable(eligible, func(i, j int) bool {
			left, right := eligible[i].ID, eligible[j].ID
			if priorities[left] != priorities[right] {
				return priorities[left] < priorities[right]
			}
			return metrics[left] < metrics[right]
		})
	}
	if len(eligible) == 0 {
		message := "no eligible deployment"
		if len(exclusions) > 0 {
			message += " (" + exclusionSummary(exclusions) + ")"
		} else {
			message += " (no enabled matching targets)"
		}
		return policy, nil, httpError{503, message}
	}
	if err := tx.Commit(); err != nil {
		return policy, nil, err
	}
	return policy, eligible, nil
}

func inferencePerformance(recent []db.Attempt, now int64) (int64, bool) {
	samples := make([]int64, 0, len(recent))
	consecutiveFailures := 0
	latestFailure := int64(0)
	readingFailures := true
	for _, attempt := range recent {
		if attempt.StartedAt > now {
			continue
		}
		if attempt.Status == "succeeded" {
			readingFailures = false
			if attempt.FirstOutputAt.Valid && attempt.FirstOutputAt.Int64 >= attempt.StartedAt {
				samples = append(samples, attempt.FirstOutputAt.Int64-attempt.StartedAt)
			}
			continue
		}
		if !transientInferenceFailure(attempt.ErrorCode.String) {
			continue
		}
		if readingFailures {
			consecutiveFailures++
			if attempt.FinishedAt.Valid && attempt.FinishedAt.Int64 > latestFailure {
				latestFailure = attempt.FinishedAt.Int64
			}
		}
	}
	if consecutiveFailures >= 3 && latestFailure > now-30*time.Second.Milliseconds() {
		return math.MaxInt64, true
	}
	if len(samples) == 0 {
		return math.MaxInt64, false
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	return samples[len(samples)/2], false
}

func transientInferenceFailure(code string) bool {
	if code == "upstream_timeout" || code == "upstream_network_error" || code == "timeout" || code == "response_interrupted" || code == "stream_incomplete" || code == "invalid_upstream_response" || code == "first_output_timeout" || code == "idle_timeout" {
		return true
	}
	if code == "upstream_status_408" || code == "upstream_status_429" {
		return true
	}
	if strings.HasPrefix(code, "upstream_status_") {
		status, err := strconv.Atoi(strings.TrimPrefix(code, "upstream_status_"))
		return err == nil && status >= 500 && status < 600
	}
	return false
}
