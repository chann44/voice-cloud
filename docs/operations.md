# Operations and recovery

Voice Cloud currently runs as one API process and one worker sharing SQLite.
Use a persistent local filesystem for the database, and keep both processes on
the same supported release. Startup verifies the migration version and all
required columns, and exits with `run make db-up first` if the schema differs.
The Compose backend migrates before starting; `make api-run` and `make worker-run`
also migrate first. Stop the API and worker before manual migrations or restores.

## Interrupted requests and retention

The worker runs maintenance immediately and every minute. Requests still pending
or running more than six minutes after their start are marked `failed` with
`process_interrupted`; their running attempts are finished in the same transaction.
The six-minute boundary includes a margin beyond the API's five-minute inference
limit. Recovery does not retry provider calls or claim that the provider did not
charge for an interrupted request.

Terminal request history is retained for 30 days by default. Set
`REQUEST_RETENTION_DAYS` in the worker environment (or the Compose host environment)
to an integer from 0 to 3650. Zero disables history deletion. Maintenance deletes
at most 100 expired requests and their attempts/usage per pass, so a large backlog
is removed gradually. Active requests, configuration, API keys, and users remain.

An `Idempotency-Key` claim lives with its request history. Once that request is
pruned, the key may be used again and can cause a new provider call. Applications
should use a fresh key for each intentional operation and retain their own durable
business-operation identifiers when they need deduplication beyond router retention.

Provider credentials use the [encrypted store](secrets.md). Preserve its master
key separately from database backups; both are required for recovery.

## Automatic Compose backups

The default Compose stack includes a `backup` service. It creates a verified
backup immediately after the backend is healthy, then every 24 hours, retaining
the seven most recent generated backups in the separate `backups` volume. The
SQLite source is opened read-only, including committed WAL data. Its volume is
mounted read/write because SQLite readers may need WAL coordination files.

```sh
docker compose -f docker/compose.yml up --build -d
docker compose -f docker/compose.yml logs backup
docker compose -f docker/compose.yml ps backup
```

Successful runs log `Backup verified: /backups/voice-cloud-...db`. Failures are
logged and retried after five minutes, or the configured interval when shorter.
After a successful retry, the normal 24-hour schedule resumes. The service becomes unhealthy
when no published backup is less than 26 hours old; monitor that health status and copy
successful files to storage on another host. A separate volume on the same machine
protects against some application mistakes, **not host loss**. `docker compose down
-v` deletes both volumes. Backup files include users, password hashes, configuration,
and request metadata, so restrict access and encrypt off-host storage as appropriate.

Copy a named backup off the container to your host:

```sh
docker compose -f docker/compose.yml cp \
  backup:/backups/voice-cloud-YYYYMMDDTHHMMSSZ-XXXXXXXX.db ./backup.db
```

Configure your host's backup system to copy these verified files off-machine.
Off-host upload credentials and a destination are deployment-specific; the project
does not upload your database to any third party automatically.

## Manual verified backup

```sh
make db-backup DB_PATH=data/voice-cloud.db BACKUP=/path/to/new-backup.db
```

This uses SQLite's online backup API, so a running API can continue writing.
The tool verifies schema, SQLite integrity, and foreign keys before publishing the
copy. Files are created with owner-only permissions. Publication is atomic and
refuses to replace an existing path, including one created during the copy.
Generated backups are self-contained SQLite files; no `-wal` or `-shm` sidecars
need to be copied with them.

## Restore to a new database

Restore never overwrites your current database. For a local installation:

```sh
# Stop the API and worker first.
make db-restore BACKUP=/path/to/backup.db DB_PATH=data/recovered.db
# Run both processes with DB_PATH=data/recovered.db after verifying the result.
```

The backup must match the checked-out release's schema. To recover an older
release, first use that release's restore tool, then upgrade using its documented
migration path. Keep the previous database and backups until the restored service
has been checked. For Compose, restore one of the named backups to a **new file**
in the database volume:

```sh
docker compose -f docker/compose.yml stop backend backup
docker compose -f docker/compose.yml run --rm --no-deps backup \
  restore /backups/voice-cloud-YYYYMMDDTHHMMSSZ-XXXXXXXX.db /data/recovered.db
```

Create a `recovery.compose.yml` override pointing both processes at that file:

```yaml
services:
  backend:
    environment:
      DB_PATH: /data/recovered.db
  backup:
    command: ["schedule", "--db", "/data/recovered.db", "--directory", "/backups", "--keep", "7"]
```

Restart with both files, then verify your configured routes and request history:

```sh
docker compose -f docker/compose.yml -f recovery.compose.yml up -d
```

For a backup recovered from off-host storage, the same local restore command can
write a new file before you import it into the database volume. Preserve owner-only
permissions and give the restored file to the backend container's UID/GID 10001.

Run the offline restore drill, which backs up committed WAL data and checks a
fresh restored database, without using provider services:

```sh
make backup-check
go -C db test ./...
make worker-check
```


## Speech-only upgrade

Migration 7 adds native S2S task storage without deleting existing models,
credentials, policies, keys, attempts or history. Old text-task rows stay in the
database for historical references; active model/policy catalogs exclude them.
New text-model writes and chat inference are unavailable. Policies/default keys
pointing to old text routes must be replaced with speech routes before use.
Rollback to the previous schema refuses to run while any S2S data exists, rather
than discard it. Upgrade the backend, worker and console together.
