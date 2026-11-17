package db

import "context"

const maintenanceBatchSize = 100
const abandonedRequests = `SELECT id FROM requests WHERE status IN ('pending','running') AND started_at < ? ORDER BY started_at,id LIMIT 100`
const expiredRequestHistory = `SELECT id FROM requests WHERE status IN ('succeeded','failed','cancelled') AND finished_at < ? ORDER BY finished_at,id LIMIT 100`

// RecoverAbandoned finishes requests that outlived the API's maximum request lifetime.
// The caller supplies a transaction so attempts and their request change together.
func (q *Queries) RecoverAbandoned(ctx context.Context, before, now int64) (int64, error) {
	if _, err := q.db.ExecContext(ctx, `UPDATE attempts SET status='failed', finished_at=?, error_code='process_interrupted'
  WHERE status='running' AND request_id IN (`+abandonedRequests+`)`, now, before); err != nil {
		return 0, err
	}
	result, err := q.db.ExecContext(ctx, `UPDATE requests SET status='failed', finished_at=?, error_code='process_interrupted'
  WHERE id IN (`+abandonedRequests+`)`, now, before)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// PruneRequestHistory deletes a bounded terminal batch; idempotency claims expire with their history.
func (q *Queries) PruneRequestHistory(ctx context.Context, before int64) (int64, error) {
	for _, query := range []string{
		`DELETE FROM attempt_usage WHERE attempt_id IN (SELECT id FROM attempts WHERE request_id IN (` + expiredRequestHistory + `))`,
		`DELETE FROM attempts WHERE request_id IN (` + expiredRequestHistory + `)`,
	} {
		if _, err := q.db.ExecContext(ctx, query, before); err != nil {
			return 0, err
		}
	}
	result, err := q.db.ExecContext(ctx, `DELETE FROM requests WHERE id IN (`+expiredRequestHistory+`)`, before)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
