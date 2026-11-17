package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"voice-cloud/db"
)

var inferenceClient = &http.Client{
	Transport: func() *http.Transport {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.ResponseHeaderTimeout = 0 // Each attempt has its own first-output deadline.
		return t
	}(),
	CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
}

func newInferenceID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func (a *api) inference(w http.ResponseWriter, r *http.Request) error {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || !strings.HasPrefix(token, "vc_") {
		w.Header().Set("WWW-Authenticate", "Bearer")
		return httpError{401, "a router API key is required"}
	}
	key, err := a.queries.GetActiveAPIKeyByHash(r.Context(), db.GetActiveAPIKeyByHashParams{KeyHash: tokenHash(token), NowMs: sql.NullInt64{Int64: time.Now().UnixMilli(), Valid: true}})
	if errors.Is(err, sql.ErrNoRows) {
		return httpError{401, "invalid or expired router API key"}
	}
	if err != nil {
		return err
	}
	release, err := a.gate.acquire(r.Context(), a.db, key.ID)
	if err != nil {
		w.Header().Set("Retry-After", "60")
		return err
	}
	defer release()
	input, err := readInference(w, r)
	if err != nil {
		return err
	}
	if input.form != nil {
		defer input.form.RemoveAll()
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	policy, deployments, routeErr := a.selectRoute(ctx, key, input.task, input.model, input.language, input)
	// Record valid routes even when health or language constraints leave no targets.
	var problem httpError
	if routeErr != nil && (!errors.As(routeErr, &problem) || problem.status != 503) {
		return routeErr
	}
	idempotency := r.Header.Get("Idempotency-Key")
	if len(idempotency) > 256 || (idempotency != "" && strings.TrimSpace(idempotency) == "") {
		return invalid("Idempotency-Key must contain 1–256 nonblank bytes")
	}
	id, err := newInferenceID()
	if err != nil {
		return err
	}
	w.Header().Set("X-Request-ID", id)
	transport := "http"
	if input.stream {
		transport = "sse"
	}
	snapshotValue := map[string]any{"model": input.model, "policy": policyJSON(policy), "deployment_ids": deploymentIDs(deployments)}
	snapshot, err := json.Marshal(snapshotValue)
	if err != nil {
		return err
	}
	fingerprint := tokenHash(r.URL.Path + "\n" + input.fingerprint)
	_, err = a.queries.CreateRequest(ctx, db.CreateRequestParams{
		ID: id, ApiKeyID: key.ID, Task: input.task, Transport: transport,
		LanguageTag: sql.NullString{String: input.language, Valid: input.language != ""},
		PolicyID:    sql.NullInt64{Int64: policy.ID, Valid: policy.ID != 0}, RoutingSnapshotJson: string(snapshot), StartedAt: time.Now().UnixMilli(),
		IdempotencyKey: sql.NullString{String: idempotency, Valid: idempotency != ""}, RequestFingerprint: sql.NullString{String: fingerprint, Valid: idempotency != ""},
	})
	if err != nil {
		if idempotency != "" {
			existing, lookupErr := a.queries.GetRequestByIdempotencyKey(ctx, db.GetRequestByIdempotencyKeyParams{ApiKeyID: key.ID, IdempotencyKey: sql.NullString{String: idempotency, Valid: true}})
			if lookupErr == nil {
				w.Header().Set("X-Request-ID", existing.ID)
				return httpError{409, "idempotency key already claimed; responses are not replayed"}
			}
		}
		return err
	}
	status, code := "failed", "internal_error"
	defer func() {
		if r.Context().Err() != nil {
			status, code = "cancelled", "client_cancelled"
		}
		saveCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, err := a.queries.FinishRequest(saveCtx, db.FinishRequestParams{ID: id, Status: status, FinishedAt: sql.NullInt64{Int64: time.Now().UnixMilli(), Valid: true}, ErrorCode: sql.NullString{String: code, Valid: code != ""}})
		if err != nil {
			slog.Error("finish inference request", "request_id", id, "error", err)
		}
	}()
	if routeErr != nil {
		code = "no_eligible_deployment"
		return routeErr
	}
	if _, err = a.queries.StartRequest(ctx, id); err != nil {
		return err
	}
	decision := routeDecision{Status: "disabled"}
	if policy.ID != 0 && policy.DecisionJson != "{}" {
		deployments, decision = a.decideRoute(ctx, policy, input, deployments)
		snapshotValue["decision"] = decision
		snapshotValue["deployment_ids"] = deploymentIDs(deployments)
		snapshot, err = json.Marshal(snapshotValue)
		if err != nil {
			return err
		}
		saveCtx, stopSave := context.WithTimeout(context.Background(), 5*time.Second)
		_, err = a.queries.SetRequestRoutingSnapshot(saveCtx, db.SetRequestRoutingSnapshotParams{ID: id, RoutingSnapshotJson: string(snapshot)})
		stopSave()
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			code = "timeout"
			return httpError{504, "inference cancelled or timed out"}
		}
	}
	attempts := policy.MaxAttempts
	if policy.FallbackEnabled != 1 {
		attempts = 1
	}
	if attempts > int64(len(deployments)) {
		attempts = int64(len(deployments))
	}
	for index, deployment := range deployments[:int(attempts)] {
		limits, limitErr := deploymentResponseLimits(deployment.SettingsJson)
		if limitErr != nil {
			return limitErr
		}
		attemptID, err := newInferenceID()
		if err != nil {
			return err
		}
		reason := "policy_priority_" + policy.Objective
		if decision.Status == "selected" {
			reason = "decision_selected"
			if index > 0 {
				reason = "decision_fallback_" + policy.Objective
			}
		}
		_, err = a.queries.CreateAttempt(ctx, db.CreateAttemptParams{ID: attemptID, RequestID: id, DeploymentID: deployment.ID, AttemptNumber: int64(index + 1), StartedAt: time.Now().UnixMilli(), SelectionReason: reason})
		if err != nil {
			return err
		}
		attemptCtx, cancelAttempt := context.WithCancel(ctx)
		timer := time.AfterFunc(time.Duration(limits.FirstOutputMs)*time.Millisecond, cancelAttempt)
		response, attemptCode, dispatchErr := a.dispatchInference(attemptCtx, input, deployment)
		retry, committed := true, false
		var observer *usageObserver
		firstOutput := int64(0)
		providerID := ""
		if response != nil {
			providerID = response.Header.Get("X-Request-ID")
			if len(providerID) > 256 {
				providerID = ""
			}
			if response.StatusCode >= 200 && response.StatusCode < 300 {
				observer = &usageObserver{}
				body := &deadlineBody{ReadCloser: response.Body, timer: timer, idle: time.Duration(limits.IdleMs) * time.Millisecond, idleEnabled: !input.stream}
				committed, dispatchErr = relayInference(w, response, input, observer, body)
				firstOutput = body.first
				retry = !committed
				attemptCode = ""
				if dispatchErr != nil {
					attemptCode = "invalid_upstream_response"
					if committed {
						attemptCode = "response_interrupted"
					}
					if attemptCtx.Err() != nil && ctx.Err() == nil {
						attemptCode = "upstream_timeout"
					}
				}
			} else {
				attemptCode = fmt.Sprintf("upstream_status_%d", response.StatusCode)
				retry = response.StatusCode == 401 || response.StatusCode == 403 || response.StatusCode == 404 || response.StatusCode == 408 || response.StatusCode == 429 || response.StatusCode >= 500
			}
			response.Body.Close()
		} else if attemptCtx.Err() != nil && ctx.Err() == nil {
			attemptCode = "upstream_timeout"
		}
		timer.Stop()
		cancelAttempt()
		attemptStatus := "failed"
		if committed && dispatchErr == nil {
			attemptStatus = "succeeded"
		}
		if r.Context().Err() != nil {
			attemptStatus, attemptCode, retry = "cancelled", "client_cancelled", false
		} else if ctx.Err() != nil {
			attemptStatus, attemptCode, retry = "failed", "timeout", false
		}
		if err := a.finishInferenceAttempt(attemptID, attemptStatus, attemptCode, providerID, firstOutput, deployment, input, observer); err != nil {
			code = "activity_recording_failed"
			if committed {
				slog.Error("finish inference attempt", "request_id", id, "error", err)
				if dispatchErr != nil {
					panic(http.ErrAbortHandler)
				}
				return nil
			}
			return err
		}
		code = attemptCode
		if committed {
			status = attemptStatus
			if status == "succeeded" {
				code = ""
			}
			if dispatchErr != nil {
				slog.Warn("inference response interrupted", "request_id", id, "code", code)
				panic(http.ErrAbortHandler)
			}
			return nil
		}
		if !retry {
			break
		}
	}
	if ctx.Err() != nil {
		code = "timeout"
		return httpError{504, "inference timed out"}
	}
	return httpError{502, "no deployment completed the request; inspect request attempts"}
}

func deploymentIDs(deployments []db.Deployment) []int64 {
	ids := make([]int64, 0, len(deployments))
	for _, d := range deployments {
		ids = append(ids, d.ID)
	}
	return ids
}

func (a *api) dispatchInference(ctx context.Context, input *inferenceInput, deployment db.Deployment) (*http.Response, string, error) {
	connection, err := a.queries.GetConnection(ctx, deployment.ConnectionID)
	if err != nil {
		return nil, "configuration_error", err
	}
	if connection.Enabled != 1 {
		return nil, "connection_disabled", nil
	}
	switch connection.Adapter {
	case "openai", "openai-compatible", "local", "custom", "whisper":
	default:
		return nil, "unsupported_adapter", nil
	}
	endpoint, err := url.Parse(connection.BaseUrl)
	if err != nil || endpoint.User != nil || endpoint.Hostname() == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, "invalid_base_url", nil
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/")
	if !strings.HasSuffix(endpoint.Path, "/v1") {
		endpoint.Path += "/v1"
	}
	switch input.task {
	case "llm":
		endpoint.Path += "/chat/completions"
	case "tts":
		endpoint.Path += "/audio/speech"
	case "stt":
		endpoint.Path += "/audio/transcriptions"
	}
	endpoint.RawPath = ""
	body, contentType, err := input.upstreamBody(deployment)
	if err != nil {
		return nil, "invalid_deployment_settings", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), body)
	if err != nil {
		return nil, "invalid_base_url", err
	}
	request.Header.Set("Content-Type", contentType)
	if connection.CredentialRef.Valid {
		credential := strings.TrimSpace(os.Getenv(connection.CredentialRef.String))
		if credential == "" || strings.ContainsAny(credential, "\r\n") {
			return nil, "credential_unavailable", nil
		}
		request.Header.Set("Authorization", "Bearer "+credential)
	}
	response, err := inferenceClient.Do(request)
	if err != nil {
		return nil, "upstream_network_error", err
	}
	return response, "", nil
}
