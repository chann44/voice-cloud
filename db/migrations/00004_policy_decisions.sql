-- +goose Up
ALTER TABLE routing_policies ADD COLUMN decision_json TEXT NOT NULL DEFAULT '{}'
    CHECK (json_valid(decision_json) AND json_type(decision_json) = 'object');

-- +goose Down
ALTER TABLE routing_policies DROP COLUMN decision_json;
