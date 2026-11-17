package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"voice-cloud/db"
)

func TestInferenceGateLimitsAndDurableQuota(t *testing.T) {
	a := setup(t)
	seedConfig(a)
	a.call("POST", "/v1/api-keys", `{"name":"App"}`, 201)
	ctx := context.Background()
	g := inferenceGate{concurrency: 2, perKey: 1, perMinute: 2, daily: 3}
	release, err := g.acquire(ctx, a.db, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.acquire(ctx, a.db, 1); err == nil {
		t.Fatal("per-key concurrent requests exceeded limit")
	}
	other, err := g.acquire(ctx, a.db, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.acquire(ctx, a.db, 3); err == nil {
		t.Fatal("global capacity exceeded")
	}
	other()
	release()
	release, err = g.acquire(ctx, a.db, 1)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if _, err = g.acquire(ctx, a.db, 1); err == nil {
		t.Fatal("minute rate exceeded")
	}
	for _, id := range []string{"one", "two", "three"} {
		if _, err = a.db.Exec(`INSERT INTO requests(id,api_key_id,task,transport,routing_snapshot_json,started_at)VALUES(?,1,'llm','http','{}',?)`, id, time.Now().UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	restarted := inferenceGate{concurrency: 2, perKey: 1, perMinute: 2, daily: 3}
	if _, err = restarted.acquire(ctx, a.db, 1); err == nil || !strings.Contains(err.Error(), "daily request quota") {
		t.Fatalf("daily quota did not survive limiter restart: %v", err)
	}
}

func TestInferenceLimitConfiguration(t *testing.T) {
	t.Setenv("INFERENCE_MAX_CONCURRENCY", "0")
	if _, err := configuredGate(); err == nil {
		t.Fatal("unsafe zero concurrency accepted")
	}
	t.Setenv("INFERENCE_MAX_CONCURRENCY", "4")
	gate, err := configuredGate()
	if err != nil || gate.concurrency != 4 {
		t.Fatalf("valid limits: %+v %v", gate, err)
	}
}

func TestBusyInstanceRejectsBeforeReadingBody(t *testing.T) {
	test := setup(t)
	created := jsonObject(t, test.call("POST", "/v1/api-keys", `{"name":"App"}`, 201))
	gate := &inferenceGate{concurrency: 1, perKey: 1, perMinute: 10, daily: 10}
	release, err := gate.acquire(context.Background(), test.db, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	api := &api{db: test.db, queries: db.New(test.db), gate: gate}
	body := &countedBody{}
	request := httptest.NewRequest("POST", "/v1/audio/speech", body)
	request.Header.Set("Authorization", "Bearer "+created["key"].(string))
	request.Header.Set("Content-Type", "application/json")
	err = api.inference(httptest.NewRecorder(), request)
	var problem httpError
	if !errors.As(err, &problem) || problem.status != 429 || body.reads != 0 {
		t.Fatalf("busy request consumed upload: reads=%d err=%v", body.reads, err)
	}
}

type countedBody struct{ reads int }

func (b *countedBody) Read(_ []byte) (int, error) { b.reads++; return 0, io.EOF }

func TestConcurrentTrafficStaysWithinCapacity(t *testing.T) {
	t.Setenv("INFERENCE_MAX_CONCURRENCY", "2")
	t.Setenv("INFERENCE_PER_KEY_CONCURRENCY", "2")
	t.Setenv("INFERENCE_REQUESTS_PER_MINUTE", "100")
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var once sync.Once
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		fmt.Fprint(w, `ID3-audio`)
	}))
	defer provider.Close()
	defer once.Do(func() { close(release) })
	a, key := setupInference(t, "tts", provider.URL)
	server := httptest.NewServer(a.handler)
	defer func() { once.Do(func() { close(release) }); server.Close() }()
	results := make(chan int, 12)
	for range 12 {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			request, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/v1/audio/speech", strings.NewReader(`{"input":"Hi","voice":"alloy"}`))
			request.Header.Set("Authorization", "Bearer "+key)
			request.Header.Set("Content-Type", "application/json")
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				results <- 0
				return
			}
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			results <- response.StatusCode
		}()
	}
	for range 2 {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("admitted calls did not reach provider")
		}
	}
	for range 10 {
		select {
		case status := <-results:
			if status != 429 {
				t.Fatalf("expected admission rejection, got %d", status)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("overload did not fail promptly")
		}
	}
	once.Do(func() { close(release) })
	for range 2 {
		if status := <-results; status != 200 {
			t.Fatalf("admitted request failed: %d", status)
		}
	}
}
