package db

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Open requires an existing database with the current migrations applied.
func Open(path string) (*sql.DB, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	uri := url.URL{Scheme: "file", Path: absolute}
	uri.RawQuery = url.Values{"mode": {"rw"}, "_pragma": {
		"foreign_keys(1)", "busy_timeout(5000)", "journal_mode(WAL)",
	}}.Encode()
	database, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ValidateSchema(ctx, database); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("open migrated database (run make db-up first): %w", err)
	}
	return database, nil
}

// SchemaVersion changes with the latest checked-in Goose migration.
const SchemaVersion = 5

func ValidateSchema(ctx context.Context, database DBTX) error {
	var version int64
	if err := database.QueryRowContext(ctx, "SELECT version_id FROM goose_db_version WHERE id IN (SELECT max(id) FROM goose_db_version GROUP BY version_id) AND is_applied = 1 ORDER BY version_id DESC LIMIT 1").Scan(&version); err != nil {
		return err
	}
	if version != SchemaVersion {
		return fmt.Errorf("expected schema version %d, found %d", SchemaVersion, version)
	}
	for table, model := range map[string]any{
		"api_keys": ApiKey{}, "attempts": Attempt{}, "attempt_usage": AttemptUsage{},
		"connections": Connection{}, "deployments": Deployment{}, "deployment_health": DeploymentHealth{},
		"deployment_languages": DeploymentLanguage{}, "models": Model{}, "policy_targets": PolicyTarget{},
		"requests": Request{}, "routing_policies": RoutingPolicy{}, "users": User{},
		"user_sessions": UserSession{}, "user_tokens": UserToken{},
	} {
		typeOf := reflect.TypeOf(model)
		columns := make([]string, typeOf.NumField())
		for index := range columns {
			columns[index] = typeOf.Field(index).Tag.Get("json")
		}
		rows, err := database.QueryContext(ctx, "SELECT "+strings.Join(columns, ",")+" FROM "+table+" LIMIT 0")
		if err != nil {
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	return nil
}
