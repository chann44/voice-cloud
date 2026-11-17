-- +goose Up
CREATE INDEX requests_active_started ON requests(started_at, id)
    WHERE status IN ('pending', 'running');
CREATE INDEX requests_terminal_finished ON requests(finished_at, id)
    WHERE status IN ('succeeded', 'failed', 'cancelled');

-- +goose Down
DROP INDEX requests_terminal_finished;
DROP INDEX requests_active_started;
