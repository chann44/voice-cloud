"""Verified SQLite backup/restore and a small daily backup scheduler."""

import argparse
from contextlib import closing
from datetime import datetime, timezone
import os
from pathlib import Path
import re
import signal
import sqlite3
import sys
import tempfile
import threading
import time
from uuid import uuid4

SCHEMA_VERSION = 5
REQUIRED_COLUMNS = {
    'connections': (
        'id', 'name', 'adapter', 'location_kind', 'base_url',
        'credential_ref', 'enabled', 'created_at', 'updated_at',
    ),
    'models': (
        'id', 'name', 'publisher', 'task', 'version',
        'created_at', 'updated_at',
    ),
    'deployments': (
        'id', 'name', 'model_id', 'connection_id', 'upstream_model_id',
        'enabled', 'capabilities_json', 'pricing_json', 'settings_json', 'created_at',
        'updated_at',
    ),
    'deployment_languages': (
        'deployment_id', 'language_tag',
    ),
    'routing_policies': (
        'id', 'name', 'task', 'objective', 'fallback_enabled',
        'max_attempts', 'constraints_json', 'created_at', 'updated_at', 'decision_json',
    ),
    'policy_targets': (
        'policy_id', 'deployment_id', 'priority',
    ),
    'api_keys': (
        'id', 'name', 'key_hash', 'key_prefix', 'default_policy_id',
        'expires_at', 'revoked_at', 'created_at', 'updated_at', 'created_by_user_id',
    ),
    'requests': (
        'id', 'api_key_id', 'task', 'transport', 'language_tag',
        'policy_id', 'routing_snapshot_json', 'status', 'started_at', 'finished_at',
        'error_code', 'trace_id', 'idempotency_key', 'request_fingerprint',
    ),
    'attempts': (
        'id', 'request_id', 'deployment_id', 'attempt_number', 'provider_request_id',
        'status', 'started_at', 'first_output_at', 'finished_at', 'error_code',
        'selection_reason',
    ),
    'attempt_usage': (
        'attempt_id', 'metric', 'quantity', 'unit', 'rate_snapshot_json',
        'cost_nanos', 'currency', 'cost_status', 'created_at', 'updated_at',
    ),
    'users': (
        'id', 'email', 'name', 'password_hash', 'role',
        'status', 'email_verified_at', 'created_at', 'updated_at',
    ),
    'user_sessions': (
        'token_hash', 'user_id', 'created_at', 'expires_at', 'revoked_at',
    ),
    'user_tokens': (
        'token_hash', 'user_id', 'purpose', 'created_at', 'expires_at',
        'used_at',
    ),
    'deployment_health': (
        'deployment_id', 'probe_kind', 'status', 'checked_at', 'expires_at',
        'latency_ms', 'error_code', 'config_hash',
    ),
}
BACKUP_NAME = re.compile(r'voice-cloud-\d{8}T\d{6}Z-[0-9a-f]{8}\.db\Z')


def validate(database):
    version = database.execute('''
        SELECT version_id FROM goose_db_version
        WHERE id IN (SELECT max(id) FROM goose_db_version GROUP BY version_id)
          AND is_applied = 1 ORDER BY version_id DESC LIMIT 1
    ''').fetchone()
    if version != (SCHEMA_VERSION,):
        raise ValueError(f'Expected Voice Cloud schema {SCHEMA_VERSION}, found {version}')
    tables = {row[0] for row in database.execute("SELECT name FROM sqlite_schema WHERE type='table'")}
    if not set(REQUIRED_COLUMNS) <= tables:
        raise ValueError('Database is missing required Voice Cloud tables')
    for table, columns in REQUIRED_COLUMNS.items():
        database.execute('SELECT ' + ','.join(columns) + ' FROM ' + table + ' LIMIT 0')
    if database.execute('PRAGMA integrity_check').fetchall() != [('ok',)]:
        raise ValueError('SQLite integrity check failed')
    if database.execute('PRAGMA foreign_key_check').fetchone() is not None:
        raise ValueError('SQLite foreign-key check failed')


def copy_database(source, target):
    """Publish only a verified complete copy; never replace an existing path."""
    source, target = Path(source).resolve(strict=True), Path(target).absolute()
    if os.path.lexists(target):
        raise FileExistsError(f'Target already exists: {target}')
    target.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    descriptor, temporary = tempfile.mkstemp(prefix='.voice-cloud-', suffix='.tmp', dir=target.parent)
    os.close(descriptor)
    try:
        deadline = time.monotonic() + 300

        def progress(status, remaining, total):
            if time.monotonic() >= deadline:
                raise TimeoutError('Database copy exceeded five minutes')

        with closing(sqlite3.connect(source.as_uri() + '?mode=ro', uri=True, timeout=30)) as src:
            validate(src)
            with closing(sqlite3.connect(temporary)) as dst:
                src.backup(dst, pages=256, progress=progress, sleep=0.1)
                dst.execute('PRAGMA journal_mode=DELETE')
                validate(dst)
        with open(temporary, 'rb') as complete:
            os.fsync(complete.fileno())
        # A hard link publishes the complete file atomically without overwriting a concurrent target.
        os.link(temporary, target)
        directory = os.open(target.parent, os.O_RDONLY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    finally:
        Path(temporary).unlink(missing_ok=True)
    return target


def scheduled_backup(source, directory, keep):
    directory = Path(directory)
    stamp = datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%SZ')
    target = directory / f'voice-cloud-{stamp}-{uuid4().hex[:8]}.db'
    copy_database(source, target)
    backups = sorted((path for path in directory.iterdir()
                      if BACKUP_NAME.fullmatch(path.name) and path.is_file() and not path.is_symlink()),
                     key=lambda path: (path.stat().st_mtime_ns, path.name))
    # Prune only our own files, and only after the new backup was verified and published.
    for expired in backups[:-keep]:
        expired.unlink()
    return target


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest='command', required=True)
    for command in ('backup', 'restore'):
        copy = commands.add_parser(command)
        copy.add_argument('source')
        copy.add_argument('target', help='New file; existing paths are never overwritten')
    schedule = commands.add_parser('schedule')
    schedule.add_argument('--db', default=os.environ.get('DB_PATH', 'data/voice-cloud.db'))
    schedule.add_argument('--directory', default='/backups')
    schedule.add_argument('--interval-seconds', type=int, default=86400)
    schedule.add_argument('--keep', type=int, default=7)
    args = parser.parse_args()
    if args.command != 'schedule':
        print(copy_database(args.source, args.target))
        return
    if args.keep < 1 or args.interval_seconds < 60:
        parser.error('--keep must be positive and --interval-seconds must be at least 60')
    stopped = threading.Event()
    for signum in (signal.SIGINT, signal.SIGTERM):
        signal.signal(signum, lambda *_: stopped.set())
    while not stopped.is_set():
        delay = args.interval_seconds
        try:
            target = scheduled_backup(args.db, args.directory, args.keep)
            print(f'Backup verified: {target}', flush=True)
        except (OSError, sqlite3.Error, ValueError) as error:
            print(f'Backup failed: {error}', file=sys.stderr, flush=True)
            delay = min(args.interval_seconds, 300)
        stopped.wait(delay)


if __name__ == '__main__':
    try:
        main()
    except (OSError, sqlite3.Error, ValueError) as error:
        print(f'Database operation failed: {error}', file=sys.stderr)
        raise SystemExit(1)
