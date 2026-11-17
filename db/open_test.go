package db

import (
	"database/sql"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func migratedDatabase(t *testing.T, version string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "router.db")
	args := []string{"-env=none", "-dir", "migrations", "sqlite3", path, "up"}
	if version != "" {
		args = append(args[:len(args)-1], "up-to", version)
	}
	if output, err := exec.Command("goose", args...).CombinedOutput(); err != nil {
		t.Fatalf("migrate: %v: %s", err, output)
	}
	return path
}

func TestOpenRejectsOutdatedSchema(t *testing.T) {
	database, err := Open(migratedDatabase(t, "3"))
	if database != nil {
		database.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "make db-up") {
		t.Fatalf("old schema accepted: %v", err)
	}
}

func TestOpenAcceptsCurrentSchema(t *testing.T) {
	database, err := Open(migratedDatabase(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	database.Close()
}

func TestOpenRejectsMissingRequiredColumn(t *testing.T) {
	path := migratedDatabase(t, "")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec(`ALTER TABLE routing_policies DROP COLUMN decision_json`); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	database, err := Open(path)
	if database != nil {
		database.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "make db-up") {
		t.Fatalf("missing column accepted: %v", err)
	}
}
