package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"voice-cloud/db"
)

func run(ctx context.Context) error {
	retentionDays, err := requestRetentionDays()
	if err != nil {
		return err
	}
	path := os.Getenv("DB_PATH")
	if path == "" {
		path = "data/voice-cloud.db"
	}
	database, err := db.Open(path)
	if err != nil {
		return err
	}
	defer database.Close()
	slog.Info("worker started")
	worker := newWorker(database)
	worker.retentionDays = retentionDays
	worker.run(ctx)
	slog.Info("worker stopped")
	return nil
}

func requestRetentionDays() (int, error) {
	value := os.Getenv("REQUEST_RETENTION_DAYS")
	if value == "" {
		return 30, nil
	}
	days, err := strconv.Atoi(value)
	if err != nil || days < 0 || days > 3650 {
		return 0, fmt.Errorf("REQUEST_RETENTION_DAYS must be an integer from 0 to 3650")
	}
	return days, nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("worker stopped", "error", err)
		os.Exit(1)
	}
}
