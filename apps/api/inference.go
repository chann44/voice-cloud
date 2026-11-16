package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
		t.ResponseHeaderTimeout = 30 * time.Second
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
	input, err := readInference(w, r)
	if err != nil {
		return err
	}
	if input.form != nil {
		defer input.form.RemoveAll()
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	policy, deployments, routeErr := a.selectRoute(ctx, key, input.task, input.model, input.language)
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
	fingerprint := tokenHash(r.URL.Path + "\n" + string(input.raw))
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
		if ctx.Err() != nil {
			code = "timeout"
			return httpError{504, "inference cancelled or timed out"}
		}
		snapshotValue["decision"] = decision
		snapshotValue["deployment_ids"] = deploymentIDs(deployments)
		snapshot, err = json.Marshal(snapshotValue)
		if err != nil {
			return err
		}
		if _, err = a.queries.SetRequestRoutingSnapshot(ctx, db.SetRequestRoutingSnapshotParams{ID: id, RoutingSnapshotJson: string(snapshot)}); err != nil {
			return err
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
		response, attemptCode, dispatchErr := a.dispatchInference(ctx, input, deployment)
		retry := true
		var observer *usageObserver
		firstOutput := int64(0)
		providerID := ""
		committed := false
		if response != nil {
			providerID = response.Header.Get("X-Request-ID")
			if len(providerID) > 256 {
				providerID = ""
			}
			if response.StatusCode >= 200 && response.StatusCode < 300 {
				committed = true
				observer = &usageObserver{sse: strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream")}
				firstOutput, dispatchErr = copyInference(w, response, observer)
				retry = false
				attemptCode = ""
				if dispatchErr != nil {
					attemptCode = "response_interrupted"
				}
				if dispatchErr == nil && (firstOutput == 0 || observer.failed) {
					attemptCode = "invalid_upstream_response"
					dispatchErr = errors.New("empty or error response")
				}
				if dispatchErr == nil && input.task == "llm" && input.stream && (!observer.sse || !observer.done) {
					attemptCode = "stream_incomplete"
					dispatchErr = errors.New("incomplete stream")
				}
			} else {
				attemptCode = fmt.Sprintf("upstream_status_%d", response.StatusCode)
				retry = response.StatusCode == 408 || response.StatusCode == 429 || response.StatusCode >= 500
			}
			response.Body.Close()
		}
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
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
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

func copyInference(w http.ResponseWriter, response *http.Response, observer *usageObserver) (int64, error) {
	contentType := response.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if response.ContentLength >= 0 {
		w.Header().Set("Content-Length", fmt.Sprint(response.ContentLength))
	}
	w.WriteHeader(response.StatusCode)
	controller := http.NewResponseController(w)
	buffer := make([]byte, 32<<10)
	first := int64(0)
	for {
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			if first == 0 {
				first = time.Now().UnixMilli()
			}
			observer.Write(buffer[:n])
			if _, err := w.Write(buffer[:n]); err != nil {
				return first, err
			}
			if err := controller.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
				return first, err
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				observer.finish()
				return first, nil
			}
			return first, readErr
		}
	}
}
