package main

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"voice-cloud/db"
)

const (
	probeInterval    = 30 * time.Second
	probeTimeout     = 5 * time.Second
	healthLifetime   = 60 * time.Second
	cleanupInterval  = time.Minute
	probeConcurrency = 4
)

type worker struct {
	database      *sql.DB
	queries       *db.Queries
	client        *http.Client
	retentionDays int
}

func newWorker(database *sql.DB) *worker {
	return &worker{
		database:      database,
		queries:       db.New(database),
		retentionDays: 30,
		client: &http.Client{
			Timeout:       probeTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

func schedule(ctx context.Context, interval time.Duration, job func(context.Context) error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if err := job(ctx); err != nil && ctx.Err() == nil {
			slog.Error("worker job failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *worker) run(ctx context.Context) {
	var jobs sync.WaitGroup
	jobs.Add(2)
	go func() { defer jobs.Done(); schedule(ctx, cleanupInterval, w.cleanup) }()
	go func() { defer jobs.Done(); schedule(ctx, probeInterval, w.probeAll) }()
	jobs.Wait()
}

func (w *worker) cleanup(ctx context.Context) error {
	tx, err := w.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	queries := w.queries.WithTx(tx)
	now := time.Now().UnixMilli()
	sessions, err := queries.DeleteExpiredUserSessions(ctx, now)
	if err != nil {
		return err
	}
	tokens, err := queries.DeleteExpiredUserTokens(ctx, now)
	if err != nil {
		return err
	}
	recovered, err := queries.RecoverAbandoned(ctx, now-(6*time.Minute).Milliseconds(), now)
	if err != nil {
		return err
	}
	var pruned int64
	if w.retentionDays > 0 {
		pruned, err = queries.PruneRequestHistory(ctx, now-int64(w.retentionDays)*24*time.Hour.Milliseconds())
		if err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	slog.Info("maintenance completed", "sessions", sessions, "tokens", tokens, "recovered_requests", recovered, "pruned_requests", pruned)
	return nil
}

func (w *worker) probeAll(ctx context.Context) error {
	ids, err := w.queries.ListHealthDeploymentIDs(ctx)
	if err != nil {
		return err
	}
	jobs := make(chan int64)
	var probes sync.WaitGroup
	for range probeConcurrency {
		probes.Add(1)
		go func() {
			defer probes.Done()
			for id := range jobs {
				if ctx.Err() != nil {
					return
				}
				if err := w.probe(ctx, id); err != nil && ctx.Err() == nil {
					slog.Error("deployment probe failed", "deployment_id", id, "error", err)
				}
			}
		}()
	}
	for _, id := range ids {
		select {
		case jobs <- id:
		case <-ctx.Done():
			close(jobs)
			probes.Wait()
			return ctx.Err()
		}
	}
	close(jobs)
	probes.Wait()
	return ctx.Err()
}
