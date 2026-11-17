-- +goose NO TRANSACTION
-- Existing llm rows remain readable for historical records; the API only accepts speech tasks.
-- +goose Up
PRAGMA foreign_keys=OFF;
BEGIN IMMEDIATE;
CREATE TABLE models_speech (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL CHECK (length(trim(name)) > 0),
    publisher TEXT NOT NULL CHECK (length(trim(publisher)) > 0),
    task TEXT NOT NULL CHECK (task IN ('stt', 'tts', 's2s', 'llm')),
    version TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL DEFAULT (unixepoch() * 1000),
    updated_at INTEGER NOT NULL DEFAULT (unixepoch() * 1000),
    UNIQUE (publisher, name, task, version)
);


INSERT INTO models_speech SELECT * FROM models;
DROP TABLE models;
ALTER TABLE models_speech RENAME TO models;

CREATE TABLE routing_policies_speech (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE CHECK (length(trim(name)) > 0),
    task TEXT NOT NULL CHECK (task IN ('stt', 'tts', 's2s', 'llm')),
    objective TEXT NOT NULL DEFAULT 'balanced'
        CHECK (objective IN ('balanced', 'latency', 'cost', 'quality')),
    fallback_enabled INTEGER NOT NULL DEFAULT 1 CHECK (fallback_enabled IN (0, 1)),
    max_attempts INTEGER NOT NULL DEFAULT 2 CHECK (max_attempts >= 1),
    constraints_json TEXT NOT NULL DEFAULT '{}'
        CHECK (json_valid(constraints_json) AND json_type(constraints_json) = 'object'),
    created_at INTEGER NOT NULL DEFAULT (unixepoch() * 1000),
    updated_at INTEGER NOT NULL DEFAULT (unixepoch() * 1000),
    decision_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(decision_json) AND json_type(decision_json) = 'object')
);


INSERT INTO routing_policies_speech SELECT * FROM routing_policies;
DROP TABLE routing_policies;
ALTER TABLE routing_policies_speech RENAME TO routing_policies;

CREATE TABLE requests_speech (
    id TEXT PRIMARY KEY NOT NULL CHECK (length(trim(id)) > 0),
    api_key_id INTEGER NOT NULL REFERENCES api_keys(id),
    task TEXT NOT NULL CHECK (task IN ('stt', 'tts', 's2s', 'llm')),
    transport TEXT NOT NULL CHECK (transport IN ('http', 'sse', 'websocket')),
    language_tag TEXT,
    policy_id INTEGER REFERENCES routing_policies(id),
    -- Includes explicit routing, client overrides, and the effective policy settings.
    routing_snapshot_json TEXT NOT NULL
        CHECK (json_valid(routing_snapshot_json) AND json_type(routing_snapshot_json) = 'object'),
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'cancelled')),
    started_at INTEGER NOT NULL,
    finished_at INTEGER,
    error_code TEXT,
    trace_id TEXT,
    -- Claim this key before dispatch; duplicate streams are not replayed.
    idempotency_key TEXT CHECK (idempotency_key IS NULL OR length(trim(idempotency_key)) > 0),
    request_fingerprint TEXT,
    CHECK (finished_at IS NULL OR finished_at >= started_at),
    CHECK (idempotency_key IS NULL OR
        (request_fingerprint IS NOT NULL AND length(trim(request_fingerprint)) > 0)),
    UNIQUE (api_key_id, idempotency_key)
);


INSERT INTO requests_speech SELECT * FROM requests;
DROP TABLE requests;
ALTER TABLE requests_speech RENAME TO requests;
CREATE INDEX requests_started ON requests(started_at);
CREATE INDEX requests_key_started ON requests(api_key_id, started_at);
CREATE INDEX requests_policy ON requests(policy_id);
CREATE INDEX requests_trace ON requests(trace_id) WHERE trace_id IS NOT NULL;



CREATE INDEX requests_active_started ON requests(started_at, id)
    WHERE status IN ('pending', 'running');
CREATE INDEX requests_terminal_finished ON requests(finished_at, id)
    WHERE status IN ('succeeded', 'failed', 'cancelled');


COMMIT;
PRAGMA foreign_keys=ON;
-- +goose Down
-- Refuse rollback rather than discard speech-to-speech data.
PRAGMA foreign_keys=OFF;
BEGIN IMMEDIATE;
CREATE TEMP TABLE speech_rollback_guard (n INTEGER CHECK (n = 0));
INSERT INTO speech_rollback_guard SELECT (SELECT count(*) FROM models WHERE task='s2s') + (SELECT count(*) FROM routing_policies WHERE task='s2s') + (SELECT count(*) FROM requests WHERE task='s2s');
DROP TABLE speech_rollback_guard;
CREATE TABLE models_speech (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL CHECK (length(trim(name)) > 0),
    publisher TEXT NOT NULL CHECK (length(trim(publisher)) > 0),
    task TEXT NOT NULL CHECK (task IN ('stt', 'tts', 'llm')),
    version TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL DEFAULT (unixepoch() * 1000),
    updated_at INTEGER NOT NULL DEFAULT (unixepoch() * 1000),
    UNIQUE (publisher, name, task, version)
);


INSERT INTO models_speech SELECT * FROM models;
DROP TABLE models;
ALTER TABLE models_speech RENAME TO models;

CREATE TABLE routing_policies_speech (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE CHECK (length(trim(name)) > 0),
    task TEXT NOT NULL CHECK (task IN ('stt', 'tts', 'llm')),
    objective TEXT NOT NULL DEFAULT 'balanced'
        CHECK (objective IN ('balanced', 'latency', 'cost', 'quality')),
    fallback_enabled INTEGER NOT NULL DEFAULT 1 CHECK (fallback_enabled IN (0, 1)),
    max_attempts INTEGER NOT NULL DEFAULT 2 CHECK (max_attempts >= 1),
    constraints_json TEXT NOT NULL DEFAULT '{}'
        CHECK (json_valid(constraints_json) AND json_type(constraints_json) = 'object'),
    created_at INTEGER NOT NULL DEFAULT (unixepoch() * 1000),
    updated_at INTEGER NOT NULL DEFAULT (unixepoch() * 1000),
    decision_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(decision_json) AND json_type(decision_json) = 'object')
);


INSERT INTO routing_policies_speech SELECT * FROM routing_policies;
DROP TABLE routing_policies;
ALTER TABLE routing_policies_speech RENAME TO routing_policies;

CREATE TABLE requests_speech (
    id TEXT PRIMARY KEY NOT NULL CHECK (length(trim(id)) > 0),
    api_key_id INTEGER NOT NULL REFERENCES api_keys(id),
    task TEXT NOT NULL CHECK (task IN ('stt', 'tts', 'llm')),
    transport TEXT NOT NULL CHECK (transport IN ('http', 'sse', 'websocket')),
    language_tag TEXT,
    policy_id INTEGER REFERENCES routing_policies(id),
    -- Includes explicit routing, client overrides, and the effective policy settings.
    routing_snapshot_json TEXT NOT NULL
        CHECK (json_valid(routing_snapshot_json) AND json_type(routing_snapshot_json) = 'object'),
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'cancelled')),
    started_at INTEGER NOT NULL,
    finished_at INTEGER,
    error_code TEXT,
    trace_id TEXT,
    -- Claim this key before dispatch; duplicate streams are not replayed.
    idempotency_key TEXT CHECK (idempotency_key IS NULL OR length(trim(idempotency_key)) > 0),
    request_fingerprint TEXT,
    CHECK (finished_at IS NULL OR finished_at >= started_at),
    CHECK (idempotency_key IS NULL OR
        (request_fingerprint IS NOT NULL AND length(trim(request_fingerprint)) > 0)),
    UNIQUE (api_key_id, idempotency_key)
);


INSERT INTO requests_speech SELECT * FROM requests;
DROP TABLE requests;
ALTER TABLE requests_speech RENAME TO requests;
CREATE INDEX requests_started ON requests(started_at);
CREATE INDEX requests_key_started ON requests(api_key_id, started_at);
CREATE INDEX requests_policy ON requests(policy_id);
CREATE INDEX requests_trace ON requests(trace_id) WHERE trace_id IS NOT NULL;



CREATE INDEX requests_active_started ON requests(started_at, id)
    WHERE status IN ('pending', 'running');
CREATE INDEX requests_terminal_finished ON requests(finished_at, id)
    WHERE status IN ('succeeded', 'failed', 'cancelled');


COMMIT;
PRAGMA foreign_keys=ON;
