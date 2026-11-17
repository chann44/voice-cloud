.DEFAULT_GOAL := help

DB_PATH ?= data/voice-cloud.db
API_ADDR ?= 127.0.0.1:8080
MIGRATIONS_DIR := db/migrations
GOOSE := goose -env=none -dir "$(MIGRATIONS_DIR)" sqlite3 "$(DB_PATH)"

.PHONY: help api-run api-check worker-run worker-check db-generate db-seed db-new db-up db-down db-status db-version db-shell db-backup db-restore db-check backup-check

help:
	@printf '%s\n' \
	  'make api-run                    Migrate and run the API' \
	  'make api-check                  Run API integration tests with race detection' \
	  'make worker-run                 Migrate and run the background worker' \
	  'make worker-check               Run worker integration tests with race detection' \
	  'make db-generate                Generate Go code from SQL queries (sqlc)' \
	  'make db-seed                    Migrate and seed provider connections' \
	  'make db-new NAME=add_feature    Create a SQL migration (Goose)' \
	  'make db-up                      Apply pending migrations' \
	  'make db-down                    Roll back one migration (may delete data)' \
	  'make db-status                  Show migration status' \
	  'make db-version                 Show the current migration version' \
	  'make db-shell                   Open SQLite with foreign keys enabled' \
	  'make db-backup BACKUP=copy.db    Back up the database to a new file' \
	  'make db-restore BACKUP=copy.db   Restore a verified backup to a new DB_PATH' \
	  'make backup-check               Run offline backup and restore tests' \
	  'make db-check                   Test migrations, queries, and Go generation' \
	  'Override the database with DB_PATH=/path/to/database.db'

db-generate:
	sqlc generate

db-seed: db-up
	python3 scripts/seed.py --db "$(DB_PATH)"

api-run: db-up
	DB_PATH="$(abspath $(DB_PATH))" API_ADDR="$(API_ADDR)" go -C apps/api run .

api-check:
	go -C apps/api test -race ./...

worker-run: db-up
	DB_PATH="$(abspath $(DB_PATH))" go -C apps/worker run .

worker-check:
	go -C apps/worker test -race ./...

db-new:
	@test -n "$(NAME)" || { echo 'Usage: make db-new NAME=add_feature'; exit 1; }
	goose -env=none -dir "$(MIGRATIONS_DIR)" create "$(NAME)" sql

db-up:
	@mkdir -p "$$(dirname "$(DB_PATH)")"
	$(GOOSE) up

db-down:
	@test -f "$(DB_PATH)" || { echo 'Database does not exist; run make db-up first'; exit 1; }
	$(GOOSE) down

db-status:
	@mkdir -p "$$(dirname "$(DB_PATH)")"
	$(GOOSE) status

db-version:
	@test -f "$(DB_PATH)" || { echo 'Database does not exist; run make db-up first'; exit 1; }
	$(GOOSE) version

db-shell:
	@test -f "$(DB_PATH)" || { echo 'Database does not exist; run make db-up first'; exit 1; }
	sqlite3 -init /dev/null -cmd 'PRAGMA foreign_keys = ON;' "$(DB_PATH)"

db-backup:
	@test -f "$(DB_PATH)" || { echo 'Database does not exist; run make db-up first'; exit 1; }
	@test -n "$(BACKUP)" || { echo 'Usage: make db-backup BACKUP=copy.db'; exit 1; }
	python3 scripts/database.py backup "$(DB_PATH)" "$(BACKUP)"

db-restore:
	@test -n "$(BACKUP)" || { echo 'Usage: make db-restore BACKUP=copy.db DB_PATH=new.db'; exit 1; }
	python3 scripts/database.py restore "$(BACKUP)" "$(DB_PATH)"

backup-check:
	python3 -B -m unittest discover -s scripts -p 'test_database.py' -v

db-check:
	go -C db test -race ./...
	goose -env=none -dir "$(MIGRATIONS_DIR)" validate
	python3 -B -m unittest discover -s db -p 'test_*.py' -v

.PHONY: check web-check examples-check e2e-check

web-check:
	cd apps/web && bun run check && bun run build && bun test tests/playground.test.ts

examples-check:
	bun test examples/typescript/client.test.ts
	python3 -B -m unittest discover -s examples/python -p 'test_*.py' -v

check: api-check worker-check db-check backup-check examples-check web-check

e2e-check:
	cd apps/web && bun run build
	python3 -B scripts/test_e2e.py
