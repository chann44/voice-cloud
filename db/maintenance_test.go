package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

func TestMaintenanceRecoversAndPrunesOnlyExpiredTerminalHistory(t *testing.T) {
	database, err := Open(migratedDatabase(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	for _, query := range []string{
		`INSERT INTO connections (id,name,adapter,location_kind,base_url) VALUES (1,'Local','custom','local','http://localhost:9000')`,
		`INSERT INTO models (id,name,publisher,task) VALUES (1,'Model','test','llm')`,
		`INSERT INTO deployments (id,name,model_id,connection_id,upstream_model_id) VALUES (1,'Local',1,1,'model')`,
		`INSERT INTO api_keys (id,name,key_hash,key_prefix) VALUES (1,'App','hash','prefix')`,
	} {
		if _, err := database.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	for index := 0; index < 102; index++ {
		if _, err := database.Exec(`INSERT INTO requests (id,api_key_id,task,transport,routing_snapshot_json,status,started_at,idempotency_key,request_fingerprint) VALUES (?,1,'llm','http','{}','running',1000,?,'fingerprint')`, fmt.Sprint(index), fmt.Sprint(index)); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`INSERT INTO attempts (id,request_id,deployment_id,attempt_number,started_at,selection_reason) VALUES (?,?,1,1,1000,'test')`, fmt.Sprint(index), fmt.Sprint(index)); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`INSERT INTO attempt_usage (attempt_id,metric,quantity,unit) VALUES (?,'requests',1,'requests')`, fmt.Sprint(index)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.Exec(`INSERT INTO requests (id,api_key_id,task,transport,routing_snapshot_json,status,started_at) VALUES ('active',1,'llm','http','{}','running',9500)`); err != nil {
		t.Fatal(err)
	}
	maintain := func(recover bool) int64 {
		t.Helper()
		tx, err := database.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		var count int64
		if recover {
			count, err = New(tx).RecoverAbandoned(ctx, 9000, 10000)
		} else {
			count, err = New(tx).PruneRequestHistory(ctx, 11000)
		}
		if err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		return count
	}
	if count := maintain(true); count != maintenanceBatchSize {
		t.Fatalf("recovery count %d", count)
	}
	var status, code string
	if err := database.QueryRow(`SELECT status,error_code FROM requests WHERE id='0'`).Scan(&status, &code); err != nil || status != "failed" || code != "process_interrupted" {
		t.Fatalf("recovery status %s/%s: %v", status, code, err)
	}
	if err := database.QueryRow(`SELECT status,error_code FROM attempts WHERE id='0'`).Scan(&status, &code); err != nil || status != "failed" || code != "process_interrupted" {
		t.Fatalf("attempt recovery %s/%s: %v", status, code, err)
	}
	if count := maintain(false); count != maintenanceBatchSize {
		t.Fatalf("retention count %d", count)
	}
	if count := maintain(true); count != 2 {
		t.Fatalf("remaining recovery count %d", count)
	}
	if count := maintain(false); count != 2 {
		t.Fatalf("remaining retention count %d", count)
	}
	if err := database.QueryRow(`SELECT status FROM requests WHERE id='active'`).Scan(&status); err != nil || status != "running" {
		t.Fatalf("active request changed: %s %v", status, err)
	}
	var count int
	if err := database.QueryRow(`SELECT count(*) FROM attempts`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("attempts not deleted: %d %v", count, err)
	}
	if err := database.QueryRow(`SELECT count(*) FROM attempt_usage`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("usage not deleted: %d %v", count, err)
	}
	if err := database.QueryRow(`SELECT count(*) FROM api_keys`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("keys changed: %d %v", count, err)
	}
	rows, err := database.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("orphan references after pruning")
	}
	if err := database.QueryRow(`SELECT id FROM requests WHERE idempotency_key='0'`).Scan(&status); err != sql.ErrNoRows {
		t.Fatalf("claim not expired with history: %v", err)
	}
}

func TestMaintenanceCandidatesUsePartialIndexes(t *testing.T) {
	database, err := Open(migratedDatabase(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for query, index := range map[string]string{abandonedRequests: "requests_active_started", expiredRequestHistory: "requests_terminal_finished"} {
		rows, err := database.Query("EXPLAIN QUERY PLAN "+query, 10000)
		if err != nil {
			t.Fatal(err)
		}
		plan := ""
		for rows.Next() {
			var id, parent, aux int
			var detail string
			if err := rows.Scan(&id, &parent, &aux, &detail); err != nil {
				t.Fatal(err)
			}
			plan += detail
		}
		rows.Close()
		if !strings.Contains(plan, index) || strings.Contains(plan, "TEMP B-TREE") {
			t.Fatalf("unbounded maintenance plan: %s", plan)
		}
	}
}
