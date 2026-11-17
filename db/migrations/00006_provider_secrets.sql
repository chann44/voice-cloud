-- +goose Up
-- Ciphertext only. The AES-256 master key is held outside the database.
CREATE TABLE provider_secrets (
    connection_id INTEGER PRIMARY KEY REFERENCES connections(id) ON DELETE CASCADE,
    ciphertext BLOB NOT NULL CHECK (length(ciphertext) >= 29),
    updated_at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE provider_secrets;
