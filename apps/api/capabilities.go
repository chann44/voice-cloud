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
	stream                      bool
	inputBytes, inputCharacters int64
	voice, format               string
	targetLanguage              string
}

type deploymentCapabilities struct {
	Streaming          *bool    `json:"streaming"`
	MaxInputBytes      *int64   `json:"max_input_bytes"`
	MaxInputCharacters *int64   `json:"max_input_characters"`
	Voices             []string `json:"voices"`
	ResponseFormats    []string `json:"response_formats"`
	TargetLanguages    []string `json:"target_languages"`
}

func requiredCapabilities(input *inferenceInput) (requestRequirements, error) {
	req := requestRequirements{}
	if input == nil {
		return req, nil
	}
	if err := taskValid(input.task); err != nil {
		return req, err
	}
	req.stream = input.stream
	if input.form != nil {
		req.inputBytes = input.size
		req.format = firstForm(input.form, "response_format")
		if req.format == "" {
			req.format = "json"
		}
		if input.task == "s2s" {
			req.voice = firstForm(input.form, "voice")
			req.targetLanguage = firstForm(input.form, "target_language")
			req.format = firstForm(input.form, "response_format")
			if req.format == "" {
				req.format = "mp3"
			}
			if req.targetLanguage != "" && !languageTag.MatchString(req.targetLanguage) {
				return req, invalid("target_language must be a language tag")
			}
		}
		return req, nil
	}
	raw, err := json.Marshal(input.fields)
	if err != nil {
		return req, invalid("invalid request fields")
	}
	req.inputBytes = int64(len(raw))
	if input.task != "tts" {
		return req, invalid("audio input requires a multipart upload")
	}
	var text string
	if json.Unmarshal(input.fields["input"], &text) != nil || json.Unmarshal(input.fields["voice"], &req.voice) != nil {
		return req, invalid("input and voice must be strings")
	}
	req.inputCharacters = int64(utf8.RuneCountInString(text))
	if value, ok := input.fields["response_format"]; ok && json.Unmarshal(value, &req.format) != nil {
		return req, invalid("response_format must be a string")
	}
	if req.format == "" {
		req.format = "mp3"
	}
	return req, nil
}

func capabilityReason(input *inferenceInput, req requestRequirements, deployment db.Deployment) string {
	if input == nil {
		return ""
	}
	var caps deploymentCapabilities
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(deployment.CapabilitiesJson), &caps) != nil || json.Unmarshal([]byte(deployment.CapabilitiesJson), &fields) != nil || fields == nil {
		return "invalid_capabilities"
	}
	for _, name := range []string{"streaming", "max_input_bytes", "max_input_characters", "voices", "response_formats", "target_languages"} {
		if raw, ok := fields[name]; ok && string(raw) == "null" {
			return "invalid_capabilities"
		}
	}
	for _, limit := range []*int64{caps.MaxInputBytes, caps.MaxInputCharacters} {
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
	if req.stream && (caps.Streaming == nil || !*caps.Streaming) {
		return "capability_streaming"
	}
	if caps.MaxInputBytes != nil && req.inputBytes > *caps.MaxInputBytes {
		return "input_limit"
	}
	if caps.MaxInputCharacters != nil && req.inputCharacters > *caps.MaxInputCharacters {
		return "input_character_limit"
	}
	if input.task == "tts" || input.task == "s2s" {
		if (input.task == "tts" || req.voice != "") && !containsCapability(caps.Voices, req.voice) {
			return "unsupported_voice"
		}
		if !containsCapability(caps.ResponseFormats, req.format) {
			return "unsupported_format"
		}
	}
	if req.targetLanguage != "" && !containsCapability(caps.TargetLanguages, req.targetLanguage) {
		return "unsupported_target_language"
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
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s:%d", name, reasons[name]))
	}
	return strings.Join(parts, ", ")
}
