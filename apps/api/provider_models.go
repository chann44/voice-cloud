package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"voice-cloud/db"
)

// Use the same API root and credentials for discovery and inference.
func connectionEndpoint(connection db.Connection) (*url.URL, error) {
	endpoint, err := url.Parse(connection.BaseUrl)
	if err != nil || endpoint.User != nil || endpoint.Hostname() == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, invalid("base_url must be an HTTP(S) API root without credentials, query, or fragment")
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/")
	if endpoint.Path == "" {
		switch connection.Adapter {
		case "azure":
			endpoint.Path = "/openai/v1"
		case "deepinfra":
			endpoint.Path = "/v1/openai"
		default:
			endpoint.Path = "/v1"
		}
	} else if !strings.HasSuffix(endpoint.Path, "/v1") && !strings.HasSuffix(endpoint.Path, "/openai") {
		endpoint.Path += "/v1"
	}
	endpoint.RawPath = ""
	return endpoint, nil
}

type providerModel struct {
	ID      string `json:"id"`
	OwnedBy string `json:"owned_by"`
	Task    string `json:"task,omitempty"`
}

func (a *api) discoverModels(w http.ResponseWriter, r *http.Request) error {
	id, err := resourceID(r)
	if err != nil {
		return err
	}
	connection, err := a.queries.GetConnection(r.Context(), id)
	if err != nil {
		return err
	}
	if !supportsInferenceAdapter(connection.Adapter) {
		return invalid("this provider requires a native adapter that is not implemented; use a compatible gateway connection")
	}
	if connection.Enabled != 1 {
		return invalid("enable this provider before discovering models")
	}
	if !adapterSupport(connection.Adapter).Discovery {
		return invalid("this adapter uses manual model registration; enter the upstream model or deployment ID")
	}
	endpoint, err := connectionEndpoint(connection)
	if err != nil {
		return err
	}
	endpoint.Path += "/models"

	ctx, cancel := context.WithTimeout(r.Context(), 7*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return err
	}
	if err := a.authenticateProvider(ctx, request, connection); err != nil {
		return err
	}
	response, err := inferenceClient.Do(request)
	if err != nil {
		return httpError{502, "model discovery failed; check the provider API root and network"}
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return httpError{502, fmt.Sprintf("provider model discovery returned HTTP %d; check credentials, account access, and API root", response.StatusCode)}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if err != nil || len(body) > 4<<20 {
		return httpError{502, "provider model catalog is unreadable or exceeds 4 MiB"}
	}
	if connection.Adapter == "deepgram" || connection.Adapter == "elevenlabs" {
		return sendSpeechCatalog(w, connection.Adapter, body)
	}
	var catalog struct {
		Data    []providerModel `json:"data"`
		HasMore bool            `json:"has_more"`
	}
	// Together and some local runtimes return a bare array rather than data.
	if len(body) > 0 && strings.HasPrefix(strings.TrimSpace(string(body)), "[") {
		if json.Unmarshal(body, &catalog.Data) != nil {
			return httpError{502, "provider did not return a supported model catalog"}
		}
	} else if json.Unmarshal(body, &catalog) != nil {
		return httpError{502, "provider did not return a supported OpenAI model catalog"}
	}
	if catalog.HasMore {
		return httpError{502, "provider catalog requires pagination; register the exact model ID manually"}
	}
	if catalog.Data == nil || len(catalog.Data) > 10000 {
		return httpError{502, "provider did not return a supported OpenAI model catalog"}
	}
	models := make([]providerModel, 0, len(catalog.Data))
	seen := map[string]bool{}
	for _, model := range catalog.Data {
		if strings.TrimSpace(model.ID) == "" || len(model.ID) > 500 || len(model.OwnedBy) > 500 || strings.ContainsAny(model.ID, "\r\n\x00") {
			return httpError{502, "provider returned an invalid model identifier"}
		}
		if model.Task != "stt" && model.Task != "tts" && model.Task != "s2s" {
			continue
		}
		if !seen[model.ID+"/"+model.Task] {
			models = append(models, model)
			seen[model.ID+"/"+model.Task] = true
		}
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return send(w, 200, models)
}

// Register the catalog model and its deployment atomically; repeated imports preserve edits.
func (a *api) importModel(w http.ResponseWriter, r *http.Request) error {
	id, err := resourceID(r)
	if err != nil {
		return err
	}
	var body struct {
		UpstreamModelID string          `json:"upstream_model_id"`
		Task            string          `json:"task"`
		Capabilities    json.RawMessage `json:"capabilities_json"`
	}
	if err := decode(w, r, &body); err != nil {
		return err
	}
	if strings.TrimSpace(body.UpstreamModelID) == "" || len(body.UpstreamModelID) > 500 || strings.ContainsAny(body.UpstreamModelID, "\r\n\x00") {
		return invalid("upstream_model_id must contain 1–500 bytes without control characters")
	}
	if err := taskValid(body.Task); err != nil {
		return err
	}
	if err := object(&body.Capabilities); err != nil {
		return err
	}
	ctx := r.Context()
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	queries := a.queries.WithTx(tx)
	connection, err := queries.GetConnection(ctx, id)
	if err != nil {
		return err
	}
	if !supportsInferenceAdapter(connection.Adapter) {
		return invalid("this provider needs an implemented native adapter or a compatible gateway")
	}
	if connection.Enabled != 1 {
		return invalid("enable this provider before registering models")
	}
	if !slices.Contains(adapterSupport(connection.Adapter).Tasks, body.Task) {
		return invalid("this adapter does not support the selected task")
	}
	if _, err := connectionEndpoint(connection); err != nil {
		return err
	}
	if _, _, err := a.providerCredential(ctx, queries, connection); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO models (name,publisher,task,version) VALUES (?,?,?,'') ON CONFLICT (publisher,name,task,version) DO NOTHING`, body.UpstreamModelID, connection.Name, body.Task); err != nil {
		return err
	}
	var modelID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM models WHERE publisher=? AND name=? AND task=? AND version=''`, connection.Name, body.UpstreamModelID, body.Task).Scan(&modelID); err != nil {
		return err
	}
	deployments, err := queries.ListModelDeployments(ctx, modelID)
	if err != nil {
		return err
	}
	for _, deployment := range deployments {
		if deployment.ConnectionID == id && deployment.UpstreamModelID == body.UpstreamModelID {
			if err := tx.Commit(); err != nil {
				return err
			}
			return send(w, 200, deploymentJSON(deployment))
		}
	}
	deployment, err := queries.CreateDeployment(ctx, db.CreateDeploymentParams{
		Name:    fmt.Sprintf("%s / %s / provider:%d", body.UpstreamModelID, body.Task, id),
		ModelID: modelID, ConnectionID: id, UpstreamModelID: body.UpstreamModelID, Enabled: 1,
		CapabilitiesJson: string(body.Capabilities), PricingJson: "{}", SettingsJson: "{}",
	})
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return send(w, 201, deploymentJSON(deployment))
}
