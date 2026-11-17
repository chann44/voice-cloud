package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"voice-cloud/db"
)

type requestRequirements struct {
	stream, tools, vision, audioInput, audioOutput, jsonOutput, jsonSchema bool
	inputBytes, contextBudget, outputTokens, inputCharacters               int64
	voice, format                                                          string
}

type deploymentCapabilities struct {
	Streaming          *bool    `json:"streaming"`
	Tools              *bool    `json:"tools"`
	Vision             *bool    `json:"vision"`
	AudioInput         *bool    `json:"audio_input"`
	AudioOutput        *bool    `json:"audio_output"`
	JSONOutput         *bool    `json:"json_output"`
	JSONSchema         *bool    `json:"json_schema"`
	MaxInputBytes      *int64   `json:"max_input_bytes"`
	MaxInputCharacters *int64   `json:"max_input_characters"`
	MaxOutputTokens    *int64   `json:"max_output_tokens"`
	ContextWindow      *int64   `json:"context_window"`
	Voices             []string `json:"voices"`
	ResponseFormats    []string `json:"response_formats"`
}

func requiredCapabilities(input *inferenceInput) (requestRequirements, error) {
	req := requestRequirements{}
	if input == nil {
		return req, nil
	}
	req.stream = input.stream
	if input.form != nil {
		req.inputBytes = input.size
		req.format = firstForm(input.form, "response_format")
		if req.format == "" {
			req.format = "json"
		}
		return req, nil
	}
	encoded, err := json.Marshal(input.fields)
	if err != nil {
		return req, invalid("invalid request fields")
	}
	req.inputBytes = int64(len(encoded))
	if input.task == "tts" {
		var text string
		if json.Unmarshal(input.fields["input"], &text) != nil || json.Unmarshal(input.fields["voice"], &req.voice) != nil {
			return req, invalid("input and voice must be strings")
		}
		req.inputCharacters = int64(utf8.RuneCountInString(text))
		if raw, ok := input.fields["response_format"]; ok && json.Unmarshal(raw, &req.format) != nil {
			return req, invalid("response_format must be a string")
		}
		if req.format == "" {
			req.format = "mp3"
		}
		return req, nil
	}
	for _, field := range []string{"tools", "functions"} {
		if raw, ok := input.fields[field]; ok {
			var values []json.RawMessage
			if string(raw) == "null" || json.Unmarshal(raw, &values) != nil {
				return req, invalid(field + " must be an array")
			}
			req.tools = req.tools || len(values) > 0
		}
	}
	for _, field := range []string{"tool_choice", "function_call"} {
		if raw, ok := input.fields[field]; ok && string(raw) != `"none"` {
			req.tools = true
		}
	}
	var messages []map[string]json.RawMessage
	if json.Unmarshal(input.fields["messages"], &messages) != nil || len(messages) == 0 {
		return req, invalid("messages must be a nonempty array of objects")
	}
	for _, message := range messages {
		var role string
		if json.Unmarshal(message["role"], &role) != nil {
			return req, invalid("each message requires a role")
		}
		switch role {
		case "system", "developer", "user", "assistant":
		case "tool", "function":
			req.tools = true
		default:
			return req, invalid("unsupported message role")
		}
		for _, field := range []string{"tool_calls", "function_call"} {
			if raw, ok := message[field]; ok && string(raw) != "null" {
				req.tools = true
			}
		}
		if _, ok := message["audio"]; ok {
			req.audioInput = true
		}
		raw, ok := message["content"]
		if !ok || string(raw) == "null" {
			if role == "assistant" && req.tools {
				continue
			}
			return req, invalid("each message requires content")
		}
		var text string
		if json.Unmarshal(raw, &text) == nil {
			continue
		}
		var parts []map[string]json.RawMessage
		if json.Unmarshal(raw, &parts) != nil || len(parts) == 0 {
			return req, invalid("message content must be text or content parts")
		}
		for _, part := range parts {
			var kind string
			if json.Unmarshal(part["type"], &kind) != nil {
				return req, invalid("content part requires a type")
			}
			switch kind {
			case "text", "input_text":
			case "image_url", "input_image":
				req.vision = true
			case "input_audio", "audio":
				req.audioInput = true
			default:
				return req, invalid("unsupported content part type")
			}
		}
	}
	req.contextBudget = req.inputBytes + int64(len(messages))*64
	if raw, ok := input.fields["modalities"]; ok {
		var modalities []string
		if json.Unmarshal(raw, &modalities) != nil {
			return req, invalid("modalities must be an array")
		}
		for _, modality := range modalities {
			if modality == "audio" {
				req.audioOutput = true
			} else if modality != "text" {
				return req, invalid("unsupported output modality")
			}
		}
	}
	if _, ok := input.fields["audio"]; ok {
		req.audioOutput = true
	}
	if raw, ok := input.fields["response_format"]; ok {
		var format struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &format) != nil {
			return req, invalid("response_format must be an object")
		}
		switch format.Type {
		case "text":
		case "json_object":
			req.jsonOutput = true
		case "json_schema":
			req.jsonSchema = true
		default:
			return req, invalid("unsupported response_format type")
		}
	}
	for _, field := range []string{"max_tokens", "max_completion_tokens"} {
		if raw, ok := input.fields[field]; ok {
			var value int64
			if string(raw) == "null" || json.Unmarshal(raw, &value) != nil || value < 1 {
				return req, invalid(field + " must be a positive integer")
			}
			if req.outputTokens != 0 {
				return req, invalid("use only one output token limit")
			}
			req.outputTokens = value
		}
	}
	return req, nil
}

func supportsInferenceAdapter(adapter string) bool {
	switch adapter {
	case "openai", "openai-compatible", "local", "custom", "whisper":
		return true
	default:
		return false
	}
}

func capabilityReason(input *inferenceInput, req requestRequirements, deployment db.Deployment) string {
	if input == nil {
		return ""
	}
	var caps deploymentCapabilities
	if json.Unmarshal([]byte(deployment.CapabilitiesJson), &caps) != nil {
		return "invalid_capabilities"
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(deployment.CapabilitiesJson), &fields) != nil || fields == nil {
		return "invalid_capabilities"
	}
	for _, name := range []string{"streaming", "tools", "vision", "audio_input", "audio_output", "json_output", "json_schema", "max_input_bytes", "max_input_characters", "max_output_tokens", "context_window", "voices", "response_formats"} {
		if raw, present := fields[name]; present && string(raw) == "null" {
			return "invalid_capabilities"
		}
	}
	for _, limit := range []*int64{caps.MaxInputBytes, caps.MaxInputCharacters, caps.MaxOutputTokens, caps.ContextWindow} {
		if limit != nil && *limit < 1 {
			return "invalid_capabilities"
		}
	}
	for _, values := range [][]string{caps.Voices, caps.ResponseFormats} {
		for _, value := range values {
			if strings.TrimSpace(value) == "" {
				return "invalid_capabilities"
			}
		}
	}
	for _, feature := range []struct {
		needed    bool
		supported *bool
		name      string
	}{
		{req.stream, caps.Streaming, "streaming"}, {req.tools, caps.Tools, "tools"}, {req.vision, caps.Vision, "vision"},
		{req.audioInput, caps.AudioInput, "audio_input"}, {req.audioOutput, caps.AudioOutput, "audio_output"},
		{req.jsonOutput, caps.JSONOutput, "json_output"}, {req.jsonSchema, caps.JSONSchema, "json_schema"},
	} {
		if feature.needed && (feature.supported == nil || !*feature.supported) {
			return "capability_" + feature.name
		}
	}
	if caps.MaxInputBytes != nil && req.inputBytes > *caps.MaxInputBytes {
		return "input_limit"
	}
	if caps.MaxInputCharacters != nil && req.inputCharacters > *caps.MaxInputCharacters {
		return "input_character_limit"
	}
	if input.task == "llm" {
		// shortcut: byte length plus framing is a conservative token budget; use provider tokenizers when less conservative limits are needed.
		if caps.ContextWindow != nil && (req.contextBudget > *caps.ContextWindow || req.outputTokens > *caps.ContextWindow-req.contextBudget) {
			return "context_limit"
		}
		if req.outputTokens > 0 && (caps.MaxOutputTokens == nil || req.outputTokens > *caps.MaxOutputTokens) {
			return "output_limit"
		}
		if caps.MaxInputBytes == nil && caps.ContextWindow == nil && req.inputBytes > 4096 {
			return "context_limit_unknown"
		}
	}
	if input.task == "tts" {
		if !containsCapability(caps.Voices, req.voice) {
			return "unsupported_voice"
		}
		if !containsCapability(caps.ResponseFormats, req.format) {
			return "unsupported_format"
		}
	}
	if input.task == "stt" && req.format != "json" && !containsCapability(caps.ResponseFormats, req.format) {
		return "unsupported_format"
	}
	return ""
}

func containsCapability(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func exclusionSummary(reasons map[string]int) string {
	names := make([]string, 0, len(reasons))
	for reason := range reasons {
		names = append(names, reason)
	}
	// Stable messages make routing diagnostics usable in clients and regression checks.
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s:%d", name, reasons[name]))
	}
	return strings.Join(parts, ", ")
}
