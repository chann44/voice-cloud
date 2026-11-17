package main

import (
	"encoding/json"
	"math"
	"voice-cloud/db"
)

type usagePricing struct {
	Request         *int64 `json:"request_nanos,omitempty"`
	InputTokens     *int64 `json:"input_token_nanos,omitempty"`
	OutputTokens    *int64 `json:"output_token_nanos,omitempty"`
	AudioMs         *int64 `json:"audio_ms_nanos,omitempty"`
	InputCharacters *int64 `json:"input_character_nanos,omitempty"`
}

func parsePricing(raw string) (usagePricing, error) {
	var pricing usagePricing
	if err := json.Unmarshal([]byte(raw), &pricing); err != nil {
		return pricing, invalid("pricing rates must be nonnegative integer nanodollars per unit")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &fields) != nil || fields == nil {
		return pricing, invalid("pricing must be an object")
	}
	for name, rate := range map[string]*int64{"request_nanos": pricing.Request, "input_token_nanos": pricing.InputTokens, "output_token_nanos": pricing.OutputTokens, "audio_ms_nanos": pricing.AudioMs, "input_character_nanos": pricing.InputCharacters} {
		if value, ok := fields[name]; ok && (string(value) == "null" || rate == nil || *rate < 0) {
			return pricing, invalid(name + " must be a nonnegative integer")
		}
	}
	return pricing, nil
}

func metricCost(pricing usagePricing, metric string, quantity int64) *int64 {
	var rate *int64
	switch metric {
	case "requests":
		rate = pricing.Request
	case "input_tokens":
		rate = pricing.InputTokens
	case "output_tokens":
		rate = pricing.OutputTokens
	case "audio_ms":
		rate = pricing.AudioMs
	case "input_characters":
		rate = pricing.InputCharacters
	}
	if rate == nil || quantity < 0 || *rate < 0 || quantity > 0 && *rate > math.MaxInt64/quantity {
		return nil
	}
	cost := quantity * *rate
	return &cost
}

func decisionCost(pricing *usagePricing, usage json.RawMessage) *int64 {
	if pricing == nil || pricing.InputTokens == nil || pricing.OutputTokens == nil {
		return nil
	}
	var tokens struct {
		Input  *int64 `json:"input_tokens"`
		Output *int64 `json:"output_tokens"`
	}
	if json.Unmarshal(usage, &tokens) != nil || tokens.Input == nil || tokens.Output == nil {
		return nil
	}
	input := metricCost(*pricing, "input_tokens", *tokens.Input)
	output := metricCost(*pricing, "output_tokens", *tokens.Output)
	if input == nil || output == nil || *input > math.MaxInt64-*output {
		return nil
	}
	total := *input + *output
	return &total
}

// This is a configured upper estimate, not a provider invoice or tokenizer.
func estimatedRoutingCost(input *inferenceInput, req requestRequirements, deployment db.Deployment) *int64 {
	pricing, err := parsePricing(deployment.PricingJson)
	if err != nil {
		return nil
	}
	if pricing.Request != nil {
		return metricCost(pricing, "requests", 1)
	}
	if input == nil {
		return nil
	}
	if input.task == "tts" {
		return metricCost(pricing, "input_characters", req.inputCharacters)
	}
	return nil
}
