package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"

	"voice-cloud/db"
)

var voiceIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func jsonString(fields map[string]json.RawMessage, name string) string {
	var value string
	_ = json.Unmarshal(fields[name], &value)
	return value
}

func speechAdapterReason(adapter string, input *inferenceInput, deployment db.Deployment) string {
	if input.stream {
		return "adapter_streaming"
	}
	if input.task == "s2s" {
		if adapter != "elevenlabs" || input.form == nil {
			return "adapter_task"
		}
		for field := range input.form.Value {
			if !slices.Contains([]string{"model", "language", "voice", "response_format", "stream"}, field) {
				return "adapter_option"
			}
		}
		if !voiceIDPattern.MatchString(firstForm(input.form, "voice")) {
			return "adapter_voice"
		}
		format := firstForm(input.form, "response_format")
		if format != "" && format != "mp3" {
			return "adapter_response_format"
		}
		return ""
	}
	if input.task == "stt" {
		if input.form == nil {
			return "adapter_input"
		}
		for name := range input.form.Value {
			if !slices.Contains([]string{"model", "language", "response_format", "stream"}, name) {
				return "adapter_option"
			}
		}
		for name := range input.form.File {
			if name != "file" {
				return "adapter_option"
			}
		}
		format := firstForm(input.form, "response_format")
		if format != "" && format != "json" && format != "text" {
			return "adapter_response_format"
		}
		return ""
	}
	for name := range input.fields {
		if !slices.Contains([]string{"model", "input", "voice", "response_format", "speed", "language", "stream"}, name) {
			return "adapter_option"
		}
	}
	if strings.TrimSpace(jsonString(input.fields, "input")) == "" {
		return "adapter_input"
	}
	voice := jsonString(input.fields, "voice")
	if adapter == "deepgram" && voice != deployment.UpstreamModelID {
		return "adapter_voice"
	}
	if adapter == "elevenlabs" && !voiceIDPattern.MatchString(voice) {
		return "adapter_voice"
	}
	format := jsonString(input.fields, "response_format")
	if format != "" && format != "mp3" && (format != "wav" || adapter != "deepgram") {
		return "adapter_response_format"
	}
	if raw, ok := input.fields["speed"]; ok {
		var speed float64
		if json.Unmarshal(raw, &speed) != nil || speed < 0.7 || speed > 1.5 || adapter == "elevenlabs" && speed != 1 {
			return "adapter_speed"
		}
	}
	return ""
}

func (a *api) dispatchSpeech(ctx context.Context, input *inferenceInput, deployment db.Deployment, connection db.Connection) (*http.Response, string, error) {
	endpoint, err := connectionEndpoint(connection)
	if err != nil {
		return nil, "invalid_base_url", err
	}
	var body io.Reader
	contentType := "application/json"
	if input.task == "tts" {
		text := jsonString(input.fields, "input")
		format := jsonString(input.fields, "response_format")
		values := url.Values{}
		var raw []byte
		if connection.Adapter == "deepgram" {
			endpoint.Path += "/speak"
			values.Set("model", deployment.UpstreamModelID)
			if format == "wav" {
				values.Set("encoding", "linear16")
				values.Set("container", "wav")
			} else {
				values.Set("encoding", "mp3")
			}
			if speed, ok := input.fields["speed"]; ok {
				values.Set("speed", string(speed))
			}
			raw, err = json.Marshal(map[string]string{"text": text})
		} else {
			endpoint.Path += "/text-to-speech/" + jsonString(input.fields, "voice")
			values.Set("output_format", "mp3_44100_128")
			fields := map[string]string{"text": text, "model_id": deployment.UpstreamModelID}
			if input.language != "" {
				fields["language_code"] = strings.Split(input.language, "-")[0]
			}
			raw, err = json.Marshal(fields)
		}
		if err != nil {
			return nil, "invalid_request", err
		}
		endpoint.RawQuery = values.Encode()
		body = bytes.NewReader(raw)
	} else {
		file := input.form.File["file"][0]
		source, err := file.Open()
		if err != nil {
			return nil, "invalid_request", err
		}
		defer source.Close()
		if connection.Adapter == "deepgram" {
			endpoint.Path += "/listen"
			values := url.Values{"model": {deployment.UpstreamModelID}}
			if input.language != "" {
				values.Set("language", input.language)
			}
			endpoint.RawQuery = values.Encode()
			contentType = file.Header.Get("Content-Type")
			if contentType == "" {
				contentType = "application/octet-stream"
			}
			body = source
		} else {
			endpoint.Path += "/speech-to-text"
			if input.task == "s2s" {
				endpoint.Path = strings.TrimSuffix(endpoint.Path, "/speech-to-text") + "/voice-changer/" + firstForm(input.form, "voice")
				endpoint.RawQuery = "output_format=mp3_44100_128"
			}
			var buffer bytes.Buffer
			writer := multipart.NewWriter(&buffer)
			if err := writer.WriteField("model_id", deployment.UpstreamModelID); err != nil {
				return nil, "invalid_request", err
			}
			if input.language != "" && input.task == "stt" {
				if err := writer.WriteField("language_code", strings.Split(input.language, "-")[0]); err != nil {
					return nil, "invalid_request", err
				}
			}
			field := "file"
			if input.task == "s2s" {
				field = "audio"
			}
			part, err := writer.CreateFormFile(field, file.Filename)
			if err != nil {
				return nil, "invalid_request", err
			}
			if _, err := io.Copy(part, source); err != nil {
				return nil, "invalid_request", err
			}
			if err := writer.Close(); err != nil {
				return nil, "invalid_request", err
			}
			contentType = writer.FormDataContentType()
			body = bytes.NewReader(buffer.Bytes())
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), body)
	if err != nil {
		return nil, "invalid_base_url", err
	}
	request.Header.Set("Content-Type", contentType)
	if err := a.authenticateProvider(ctx, request, connection); err != nil {
		return nil, "credential_unavailable", err
	}
	response, err := inferenceClient.Do(request)
	if err != nil {
		return nil, "upstream_network_error", err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || (input.task == "tts" || input.task == "s2s") {
		return response, "", nil
	}
	defer response.Body.Close()
	media, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return nil, "invalid_upstream_response", errors.New("expected native transcription JSON")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxJSONResponse+1))
	if err != nil || len(raw) > maxJSONResponse {
		return nil, "invalid_upstream_response", errors.New("native transcription is unreadable or oversized")
	}
	var text *string
	result := map[string]any{}
	if connection.Adapter == "deepgram" {
		var transcript struct {
			Metadata struct {
				Duration  *float64 `json:"duration"`
				RequestID string   `json:"request_id"`
			} `json:"metadata"`
			Results struct {
				Channels []struct {
					Alternatives []struct {
						Transcript *string `json:"transcript"`
					} `json:"alternatives"`
				} `json:"channels"`
			} `json:"results"`
		}
		if json.Unmarshal(raw, &transcript) != nil || len(transcript.Results.Channels) != 1 || len(transcript.Results.Channels[0].Alternatives) == 0 {
			return nil, "invalid_upstream_response", errors.New("invalid native transcription")
		}
		text = transcript.Results.Channels[0].Alternatives[0].Transcript
		if transcript.Metadata.Duration != nil {
			result["duration"] = *transcript.Metadata.Duration
		}
		if transcript.Metadata.RequestID != "" {
			response.Header.Set("X-Request-ID", transcript.Metadata.RequestID)
		}
	} else {
		var transcript struct {
			Text *string `json:"text"`
		}
		if json.Unmarshal(raw, &transcript) != nil {
			return nil, "invalid_upstream_response", errors.New("invalid native transcription")
		}
		text = transcript.Text
	}
	if text == nil {
		return nil, "invalid_upstream_response", errors.New("native transcription has no text")
	}
	result["text"] = *text
	if firstForm(input.form, "response_format") == "text" {
		raw = []byte(*text)
		response.Header.Set("Content-Type", "text/plain; charset=utf-8")
	} else {
		raw, err = json.Marshal(result)
		if err != nil {
			return nil, "invalid_upstream_response", err
		}
		response.Header.Set("Content-Type", "application/json")
	}
	response.Body = io.NopCloser(bytes.NewReader(raw))
	response.ContentLength = int64(len(raw))
	response.Header.Del("Content-Length")
	return response, "", nil
}

func sendSpeechCatalog(w http.ResponseWriter, adapter string, raw []byte) error {
	models := []providerModel{}
	if adapter == "elevenlabs" {
		var rows []struct {
			ID  string `json:"model_id"`
			TTS bool   `json:"can_do_text_to_speech"`
			S2S bool   `json:"can_do_voice_conversion"`
		}
		if json.Unmarshal(raw, &rows) != nil || rows == nil {
			return httpError{502, "invalid ElevenLabs model catalog"}
		}
		for _, row := range rows {
			if row.S2S {
				models = append(models, providerModel{row.ID, "ElevenLabs", "s2s"})
			}
			if row.TTS {
				models = append(models, providerModel{row.ID, "ElevenLabs", "tts"})
			}
		}
	} else {
		type entry struct {
			Name      string `json:"name"`
			Canonical string `json:"canonical_name"`
			Batch     bool   `json:"batch"`
		}
		var rows struct {
			STT []entry `json:"stt"`
			TTS []entry `json:"tts"`
		}
		if json.Unmarshal(raw, &rows) != nil || rows.STT == nil && rows.TTS == nil {
			return httpError{502, "invalid Deepgram model catalog"}
		}
		for _, row := range rows.STT {
			if row.Batch {
				id := row.Canonical
				if id == "" {
					id = row.Name
				}
				models = append(models, providerModel{id, "Deepgram", "stt"})
			}
		}
		for _, row := range rows.TTS {
			id := row.Canonical
			if id == "" {
				id = row.Name
			}
			models = append(models, providerModel{id, "Deepgram", "tts"})
		}
	}
	if len(models) > 10000 {
		return httpError{502, "provider model catalog exceeds 10000 entries"}
	}
	seen := map[string]bool{}
	unique := []providerModel{}
	for _, model := range models {
		if strings.TrimSpace(model.ID) == "" || len(model.ID) > 500 || strings.ContainsAny(model.ID, "\r\n\x00") {
			return httpError{502, "provider returned an invalid model identifier"}
		}
		if !seen[model.ID+"/"+model.Task] {
			unique = append(unique, model)
			seen[model.ID+"/"+model.Task] = true
		}
	}
	sort.Slice(unique, func(i, j int) bool { return unique[i].ID < unique[j].ID })
	return send(w, 200, unique)
}
