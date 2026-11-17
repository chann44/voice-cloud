package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
)

const maxJSONResponse = 8 << 20
const maxAudioResponse = 128 << 20

type responseLimits struct{ FirstOutputMs, IdleMs int64 }

func deploymentResponseLimits(settings string) (responseLimits, error) {
	limits := responseLimits{30000, 15000}
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(settings), &fields) != nil {
		return limits, invalid("invalid deployment settings")
	}
	for name, dest := range map[string]*int64{"first_output_timeout_ms": &limits.FirstOutputMs, "idle_timeout_ms": &limits.IdleMs} {
		if raw, ok := fields[name]; ok {
			if string(raw) == "null" || json.Unmarshal(raw, dest) != nil || *dest < 50 || *dest > 120000 {
				return limits, invalid(name + " must be an integer between 50 and 120000")
			}
		}
	}
	return limits, nil
}

type deadlineBody struct {
	io.ReadCloser
	timer       *time.Timer
	idle        time.Duration
	first       int64
	idleEnabled bool
}

func (b *deadlineBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		if b.first == 0 {
			b.first = time.Now().UnixMilli()
		}
		if b.idleEnabled {
			b.timer.Reset(b.idle)
		}
	}
	return n, err
}

func responseHeaders(w http.ResponseWriter, response *http.Response) {
	w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(response.StatusCode)
}

// Buffer ordinary JSON until validated; streaming commits only after usable output.
func relayInference(w http.ResponseWriter, response *http.Response, input *inferenceInput, observer *usageObserver, body *deadlineBody) (bool, error) {
	media, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil {
		return false, errors.New("invalid upstream Content-Type")
	}
	if input.stream {
		if media != "text/event-stream" {
			return false, errors.New("expected an event stream")
		}
		observer.sse = true
		return relayEvents(w, response, input, observer, body)
	}
	if input.task == "llm" && media != "application/json" {
		return false, errors.New("expected JSON chat response")
	}
	if input.task == "tts" {
		if !strings.HasPrefix(media, "audio/") && media != "application/octet-stream" {
			return false, errors.New("expected audio")
		}
		prefix := make([]byte, 4096)
		n, readErr := body.Read(prefix)
		if n == 0 {
			return false, errors.New("empty audio response")
		}
		trimmed := bytes.TrimSpace(prefix[:n])
		if bytes.HasPrefix(trimmed, []byte(`{"error"`)) || bytes.HasPrefix(trimmed, []byte("<html")) || bytes.HasPrefix(trimmed, []byte("<!DOCTYPE")) {
			return false, errors.New("upstream returned text instead of audio")
		}
		if readErr != nil && readErr != io.EOF {
			return false, readErr
		}
		responseHeaders(w, response)
		if _, err = w.Write(prefix[:n]); err != nil {
			return true, err
		}
		controller := http.NewResponseController(w)
		if err = controller.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return true, err
		}
		if readErr == io.EOF {
			return true, nil
		}
		buffer := make([]byte, 32<<10)
		total := int64(n)
		for {
			n, readErr = body.Read(buffer)
			if n > 0 {
				total += int64(n)
				if total > maxAudioResponse {
					return true, errors.New("audio response exceeded limit")
				}
				if _, err = w.Write(buffer[:n]); err != nil {
					return true, err
				}
				if err = controller.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
					return true, err
				}
			}
			if readErr != nil {
				if readErr == io.EOF {
					return true, nil
				}
				return true, readErr
			}
		}
	}
	raw, err := io.ReadAll(io.LimitReader(body, maxJSONResponse+1))
	if err != nil {
		return false, err
	}
	if len(raw) == 0 || len(raw) > maxJSONResponse {
		return false, errors.New("empty or oversized upstream response")
	}
	if input.task == "llm" {
		if media != "application/json" || !validChatResponse(raw) {
			return false, errors.New("invalid chat response")
		}
	} else {
		format := "json"
		if input.form != nil && firstForm(input.form, "response_format") != "" {
			format = firstForm(input.form, "response_format")
		}
		if format == "json" || format == "verbose_json" || format == "diarized_json" {
			var result struct {
				Text  *string         `json:"text"`
				Error json.RawMessage `json:"error"`
			}
			if media != "application/json" || json.Unmarshal(raw, &result) != nil || result.Text == nil || len(result.Error) > 0 && string(result.Error) != "null" {
				return false, errors.New("invalid transcription response")
			}
		} else if !strings.HasPrefix(media, "text/") || media == "text/html" {
			return false, errors.New("invalid transcription content type")
		}
	}
	observer.inspect(raw)
	if observer.failed {
		return false, errors.New("upstream reported an error")
	}
	body.first = time.Now().UnixMilli()
	responseHeaders(w, response)
	_, err = w.Write(raw)
	return true, err
}

func validChatResponse(raw []byte) bool {
	var envelope struct {
		Choices []struct {
			Message *struct {
				Role         string          `json:"role"`
				Content      json.RawMessage `json:"content"`
				ToolCalls    json.RawMessage `json:"tool_calls"`
				FunctionCall json.RawMessage `json:"function_call"`
				Refusal      json.RawMessage `json:"refusal"`
				Audio        json.RawMessage `json:"audio"`
			} `json:"message"`
		} `json:"choices"`
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(raw, &envelope) != nil || len(envelope.Choices) == 0 || len(envelope.Error) > 0 && string(envelope.Error) != "null" {
		return false
	}
	for _, choice := range envelope.Choices {
		if choice.Message == nil {
			return false
		}
		m := choice.Message
		if m.Role != "" && m.Role != "assistant" {
			return false
		}
		valid := false
		if len(m.Content) > 0 && string(m.Content) != "null" {
			var text string
			if json.Unmarshal(m.Content, &text) != nil {
				return false
			}
			valid = true
		}
		if len(m.Refusal) > 0 && string(m.Refusal) != "null" {
			var text string
			if json.Unmarshal(m.Refusal, &text) != nil {
				return false
			}
			valid = valid || text != ""
		}
		if len(m.ToolCalls) > 0 && string(m.ToolCalls) != "null" {
			var calls []struct {
				Type     string `json:"type"`
				Function struct {
					Name      string  `json:"name"`
					Arguments *string `json:"arguments"`
				} `json:"function"`
			}
			if json.Unmarshal(m.ToolCalls, &calls) != nil {
				return false
			}
			for _, call := range calls {
				if call.Type != "function" || call.Function.Name == "" || call.Function.Arguments == nil {
					return false
				}
				valid = true
			}
		}
		if len(m.FunctionCall) > 0 && string(m.FunctionCall) != "null" {
			var call struct {
				Name      string  `json:"name"`
				Arguments *string `json:"arguments"`
			}
			if json.Unmarshal(m.FunctionCall, &call) != nil || call.Name == "" || call.Arguments == nil {
				return false
			}
			valid = true
		}
		if len(m.Audio) > 0 && string(m.Audio) != "null" {
			var audio struct {
				Data string `json:"data"`
			}
			if json.Unmarshal(m.Audio, &audio) != nil || audio.Data == "" {
				return false
			}
			valid = true
		}
		if !valid {
			return false
		}
	}
	return true
}

func relayEvents(w http.ResponseWriter, response *http.Response, input *inferenceInput, observer *usageObserver, body *deadlineBody) (bool, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 4096), usageInspectionLimit)
	committed := false
	var event bytes.Buffer
	var pending bytes.Buffer
	controller := http.NewResponseController(w)
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			if event.Len()+len(line) > usageInspectionLimit {
				return committed, errors.New("event exceeded limit")
			}
			event.WriteString(line)
			event.WriteByte('\n')
			continue
		}
		if event.Len() == 0 {
			continue
		}
		event.WriteByte('\n')
		raw := append([]byte(nil), event.Bytes()...)
		event.Reset()
		usable := false
		for _, line := range bytes.Split(raw, []byte{'\n'}) {
			if !bytes.HasPrefix(line, []byte("data:")) {
				continue
			}
			data := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
			if bytes.Equal(data, []byte("[DONE]")) {
				observer.done = true
				continue
			}
			var result map[string]json.RawMessage
			if json.Unmarshal(data, &result) != nil || result == nil {
				return committed, errors.New("invalid stream JSON")
			}
			observer.inspect(data)
			if observer.failed {
				return committed, errors.New("upstream stream error")
			}
			if input.task == "llm" {
				var choices []struct {
					Delta        json.RawMessage `json:"delta"`
					FinishReason json.RawMessage `json:"finish_reason"`
				}
				if rawChoices, ok := result["choices"]; ok {
					if json.Unmarshal(rawChoices, &choices) != nil {
						return committed, errors.New("invalid chat stream choices")
					}
					for _, choice := range choices {
						var delta map[string]json.RawMessage
						if len(choice.Delta) > 0 && string(choice.Delta) != "null" && json.Unmarshal(choice.Delta, &delta) != nil {
							return committed, errors.New("invalid chat stream delta")
						}
						for _, field := range []string{"content", "tool_calls", "function_call", "refusal", "audio"} {
							value, ok := delta[field]
							if ok && string(value) != "null" && string(value) != `""` && string(value) != "[]" && string(value) != "{}" {
								usable = true
							}
						}
						if len(choice.FinishReason) > 0 && string(choice.FinishReason) != "null" {
							usable = true
						}
					}
				} else if _, ok := result["usage"]; !ok {
					return committed, errors.New("missing chat stream choices")
				}
			} else {
				var kind string
				json.Unmarshal(result["type"], &kind)
				usable = kind == "transcript.text.delta" || kind == "transcript.text.done" || kind == "transcript.text.segment"
				if kind == "transcript.text.done" {
					observer.done = true
				}
			}
		}
		if !committed && !usable {
			if observer.done {
				return false, errors.New("stream ended before output")
			}
			if pending.Len()+len(raw) > usageInspectionLimit {
				return false, errors.New("stream preamble exceeded limit")
			}
			pending.Write(raw)
			continue
		}
		if !committed {
			body.first = time.Now().UnixMilli()
			body.idleEnabled = true
			body.timer.Reset(body.idle)
			responseHeaders(w, response)
			committed = true
			if pending.Len() > 0 {
				if _, err := w.Write(pending.Bytes()); err != nil {
					return true, err
				}
				pending.Reset()
			}
		}
		if _, err := w.Write(raw); err != nil {
			return committed, err
		}
		if err := controller.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return committed, err
		}
		if observer.done {
			return committed, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return committed, err
	}
	if event.Len() != 0 {
		return committed, errors.New("incomplete event framing")
	}
	if !observer.done {
		return committed, errors.New("stream ended without completion")
	}
	if !committed {
		return false, fmt.Errorf("stream contains no output")
	}
	return true, nil
}
