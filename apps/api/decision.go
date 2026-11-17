package main

import "encoding/json"

// Keep the legacy field readable, but do not configure or call generation/decision models.
func parseDecisionConfig(raw json.RawMessage, task string) (*struct{}, error) {
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return nil, invalid("decision_json must be an empty object")
	}
	if len(value) != 0 {
		return nil, invalid("decision models are unavailable; Voice Cloud supports speech tasks only")
	}
	return nil, nil
}
