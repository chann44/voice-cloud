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

func (a *api) selectRoute(ctx context.Context, key db.ApiKey, task, model, language string) (db.RoutingPolicy, []db.Deployment, error) {
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
		LocationKind string `json:"location_kind"`
		MaxLatencyMs *int64 `json:"max_latency_ms"`
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
			(constraints.MaxLatencyMs != nil && *constraints.MaxLatencyMs < 0) {
			return policy, nil, invalid("unsupported routing constraints; use location_kind and max_latency_ms")
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
		if deployment.Enabled != 1 || connection.Enabled != 1 ||
			(constraints.LocationKind != "" && constraints.LocationKind != connection.LocationKind) {
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
				continue
			}
		}
		health, err := queries.GetDeploymentHealth(ctx, deployment.ID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return policy, nil, err
		}
		fresh := err == nil && health.CheckedAt <= now && health.ExpiresAt > now && health.ConfigHash == db.HealthConfigHash(deployment, connection)
		if fresh && health.Status == "unhealthy" {
			continue
		}
		latency := int64(math.MaxInt64)
		if fresh && health.Status == "healthy" && health.LatencyMs.Valid {
			latency = health.LatencyMs.Int64
		}
		if constraints.MaxLatencyMs != nil && (latency == math.MaxInt64 || latency > *constraints.MaxLatencyMs) {
			continue
		}
		metrics[deployment.ID] = latency
		if policy.Objective == "cost" {
			var pricing struct {
				RequestNanos *int64 `json:"request_nanos"`
			}
			metrics[deployment.ID] = math.MaxInt64
			if json.Unmarshal([]byte(deployment.PricingJson), &pricing) == nil && pricing.RequestNanos != nil && *pricing.RequestNanos >= 0 {
				metrics[deployment.ID] = *pricing.RequestNanos
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
		return policy, nil, httpError{503, "no eligible deployment"}
	}
	if err := tx.Commit(); err != nil {
		return policy, nil, err
	}
	return policy, eligible, nil
}
