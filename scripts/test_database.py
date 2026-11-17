"""Offline backup/restore checks using real Goose migrations and SQLite WAL."""

from pathlib import Path
import os
import sqlite3
import subprocess
import tempfile
import unittest
from unittest.mock import MagicMock, call, patch

import database

ROOT = Path(__file__).resolve().parent.parent


class DatabaseOperationsTest(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix='voice cloud recovery ')
        self.addCleanup(temporary.cleanup)
        self.directory = Path(temporary.name)
        self.source = self.directory / 'live.db'
        subprocess.run(['goose', '-env=none', '-dir', str(ROOT / 'db/migrations'),
                        'sqlite3', str(self.source), 'up'], capture_output=True, text=True, check=True)

    def test_backup_copies_committed_wal_and_restore_preserves_data(self):
        writer = sqlite3.connect(self.source)
        self.addCleanup(writer.close)
        writer.execute('PRAGMA journal_mode=WAL')
        writer.execute("INSERT INTO models (name,publisher,task) VALUES ('WAL model','test','llm')")
        writer.commit()
        self.assertTrue(Path(str(self.source) + '-wal').exists())
        backup = self.directory / 'backups' / 'first.db'
        database.copy_database(self.source, backup)
        self.assertEqual(backup.stat().st_mode & 0o777, 0o600)
        restored = self.directory / 'restored.db'
        database.copy_database(backup, restored)
        with sqlite3.connect(restored) as actual:
            database.validate(actual)
            self.assertEqual(actual.execute('SELECT name FROM models').fetchall(), [('WAL model',)])
        self.assertFalse(list(backup.parent.glob('.voice-cloud-*.tmp')))

    def test_existing_target_and_invalid_source_are_not_modified(self):
        target = self.directory / 'protected.db'
        target.write_bytes(b'keep this')
        with self.assertRaises(FileExistsError):
            database.copy_database(self.source, target)
        self.assertEqual(target.read_bytes(), b'keep this')
        invalid = self.directory / 'invalid.db'
        invalid.write_bytes(b'not a database')
        destination = self.directory / 'new.db'
        with self.assertRaises(sqlite3.DatabaseError):
            database.copy_database(invalid, destination)
        self.assertFalse(destination.exists())
        self.assertFalse(list(self.directory.glob('.voice-cloud-*.tmp')))

    def test_outdated_schema_is_rejected(self):
        subprocess.run(['goose', '-env=none', '-dir', str(ROOT / 'db/migrations'),
                        'sqlite3', str(self.source), 'down'], capture_output=True, text=True, check=True)
        with self.assertRaisesRegex(ValueError, 'schema'):
            database.copy_database(self.source, self.directory / 'rejected.db')
        self.assertFalse((self.directory / 'rejected.db').exists())

    def test_missing_runtime_column_is_rejected(self):
        with sqlite3.connect(self.source) as source:
            source.execute('ALTER TABLE user_sessions DROP COLUMN revoked_at')
        destination = self.directory / 'bad-schema.db'
        with self.assertRaises(sqlite3.OperationalError):
            database.copy_database(self.source, destination)
        self.assertFalse(destination.exists())

    def test_schedule_retains_only_its_own_verified_backups(self):
        backups = self.directory / 'backups'
        backups.mkdir()
        other = backups / 'manual.db'
        other.write_bytes(b'keep manual files')
        for _ in range(3):
            database.scheduled_backup(self.source, backups, keep=2)
        names = [path for path in backups.iterdir() if database.BACKUP_NAME.fullmatch(path.name)]
        self.assertEqual(len(names), 2)
        self.assertEqual(other.read_bytes(), b'keep manual files')
        with patch.object(database, 'copy_database', side_effect=OSError('disk full')):
            with self.assertRaises(OSError):
                database.scheduled_backup(self.source, backups, keep=1)
        self.assertEqual(len([path for path in backups.iterdir()
                              if database.BACKUP_NAME.fullmatch(path.name)]), 2)

    def test_atomic_publish_refuses_a_target_created_during_backup(self):
        target = self.directory / 'raced.db'
        original_link = os.link

        def concurrent_link(source, destination):
            target.write_bytes(b'concurrent owner')
            return original_link(source, destination)

        with patch.object(database.os, 'link', side_effect=concurrent_link):
            with self.assertRaises(FileExistsError):
                database.copy_database(self.source, target)
        self.assertEqual(target.read_bytes(), b'concurrent owner')


class BackupScheduleTest(unittest.TestCase):
    def test_failed_backup_retries_soon_then_resumes_daily_schedule(self):
        stopped = MagicMock()
        stopped.is_set.side_effect = [False, False, True]
        with patch.object(database.threading, 'Event', return_value=stopped), \
                patch.object(database.signal, 'signal'), \
                patch.object(database.sys, 'argv', ['database.py', 'schedule']), \
                patch.object(database, 'scheduled_backup', side_effect=[OSError('disk full'), Path('verified.db')]), \
                patch('builtins.print'):
            database.main()
        self.assertEqual(stopped.wait.call_args_list, [call(300), call(86400)])

    def test_retry_does_not_exceed_a_shorter_configured_interval(self):
        stopped = MagicMock()
        stopped.is_set.side_effect = [False, True]
        with patch.object(database.threading, 'Event', return_value=stopped), \
                patch.object(database.signal, 'signal'), \
                patch.object(database.sys, 'argv', ['database.py', 'schedule', '--interval-seconds', '60']), \
                patch.object(database, 'scheduled_backup', side_effect=OSError('disk full')), \
                patch('builtins.print'):
            database.main()
        stopped.wait.assert_called_once_with(60)


if __name__ == '__main__':
    unittest.main()
