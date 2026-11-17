package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

type trafficWindow struct {
	count, active int
	until         time.Time
}
type inferenceGate struct {
	sync.Mutex
	active                                int
	windows                               map[int64]trafficWindow
	concurrency, perKey, perMinute, daily int
}

func configuredGate() (*inferenceGate, error) {
	gate := &inferenceGate{concurrency: 2, perKey: 2, perMinute: 60, daily: 10000}
	for name, dest := range map[string]*int{
		"INFERENCE_MAX_CONCURRENCY": &gate.concurrency, "INFERENCE_PER_KEY_CONCURRENCY": &gate.perKey,
		"INFERENCE_REQUESTS_PER_MINUTE": &gate.perMinute, "INFERENCE_DAILY_REQUEST_LIMIT": &gate.daily,
	} {
		if value := os.Getenv(name); value != "" {
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 || n > 1000000 {
				return gate, fmt.Errorf("%s must be an integer between 1 and 1000000", name)
			}
			*dest = n
		}
	}
	return gate, nil
}

func (g *inferenceGate) acquire(ctx context.Context, database *sql.DB, key int64) (func(), error) {
	g.Lock()
	defer g.Unlock()
	// Zero-value api instances in tests retain the same safe limits.
	if g.concurrency == 0 {
		g.concurrency = 2
		g.perKey = 2
		g.perMinute = 60
		g.daily = 10000
	}
	if g.windows == nil {
		g.windows = make(map[int64]trafficWindow)
	}
	now := time.Now()
	for id, value := range g.windows {
		if value.active == 0 && !now.Before(value.until) {
			delete(g.windows, id)
		}
	}
	window := g.windows[key]
	if !now.Before(window.until) {
		window.count = 0
		window.until = now.Add(time.Minute)
	}
	if g.active >= g.concurrency || window.active >= g.perKey || window.count >= g.perMinute || len(g.windows) >= 4096 && window.count == 0 {
		return nil, httpError{http.StatusTooManyRequests, "inference capacity or request rate exceeded; retry later"}
	}
	var count int
	midnight := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC).UnixMilli()
	if err := database.QueryRowContext(ctx, `SELECT count(*) FROM requests WHERE api_key_id=? AND started_at>=?`, key, midnight).Scan(&count); err != nil {
		return nil, err
	}
	// In-flight uploads have not all claimed database records yet. Count them conservatively.
	if count+window.active >= g.daily {
		return nil, httpError{http.StatusTooManyRequests, "daily request quota reached; resets at midnight UTC"}
	}
	window.count++
	window.active++
	g.active++
	g.windows[key] = window
	return func() {
		g.Lock()
		defer g.Unlock()
		value := g.windows[key]
		value.active--
		g.active--
		g.windows[key] = value
	}, nil
}
