import unittest
import sqlite3
import tempfile
import os
import sys

sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "src")))

from mcp_gateway.schema import init_db, get_schema_version, migrate_db

class TestSchema(unittest.TestCase):
    def setUp(self):
        self.fd, self.db_path = tempfile.mkstemp()
        os.close(self.fd)

    def tearDown(self):
        import gc
        gc.collect()
        if os.path.exists(self.db_path):
            try:
                os.remove(self.db_path)
            except Exception:
                pass

    def test_init_db(self):
        init_db(self.db_path)

        conn = sqlite3.connect(self.db_path)

        # Test version
        version = get_schema_version(conn)
        self.assertEqual(version, 4)

        # Test tables
        cursor = conn.cursor()
        cursor.execute("SELECT name FROM sqlite_master WHERE type='table'")
        tables = {row[0] for row in cursor.fetchall()}

        expected_tables = {
            "targets", "projects", "project_tasks", "ai_clients",
            "grants", "privilege_approvals", "activity", "settings", "admin_users"
        }
        self.assertTrue(expected_tables.issubset(tables))

        # Test defaults
        cursor.execute("SELECT key, value FROM settings")
        settings = dict(cursor.fetchall())

        self.assertEqual(settings.get("gateway_enabled"), "true")
        self.assertEqual(settings.get("writes_enabled"), "false")
        self.assertEqual(settings.get("shell_enabled"), "false")
        self.assertEqual(settings.get("default_timeout"), "30")
        self.assertEqual(settings.get("max_output_bytes"), "262144")
        self.assertEqual(settings.get("max_file_read_bytes"), "1048576")
        self.assertEqual(settings.get("max_write_bytes"), "262144")
        self.assertEqual(settings.get("max_diff_bytes"), "65536")
        self.assertEqual(settings.get("activity_retention"), "5000")

        conn.close()

    def test_migrate_v1_preserves_targets_and_adds_privilege_defaults(self):
        conn = sqlite3.connect(self.db_path)
        conn.executescript("""
            CREATE TABLE targets (
                id TEXT PRIMARY KEY,
                display_name TEXT NOT NULL,
                platform TEXT NOT NULL DEFAULT 'linux',
                host TEXT NOT NULL,
                port INTEGER NOT NULL DEFAULT 22,
                user TEXT NOT NULL,
                ssh_alias TEXT,
                enabled INTEGER NOT NULL DEFAULT 1,
                created_at TEXT NOT NULL DEFAULT (datetime('now')),
                updated_at TEXT NOT NULL DEFAULT (datetime('now'))
            );
            CREATE TABLE settings (
                key TEXT PRIMARY KEY,
                value TEXT NOT NULL,
                updated_at TEXT NOT NULL DEFAULT (datetime('now'))
            );
            INSERT INTO targets (id, display_name, host, user) VALUES ('t1', 'T1', '127.0.0.1', 'worker');
            PRAGMA user_version = 1;
        """)
        conn.commit()

        migrate_db(conn)

        self.assertEqual(get_schema_version(conn), 4)
        row = conn.execute(
            "SELECT privilege_policy, privilege_user FROM targets WHERE id='t1'"
        ).fetchone()
        self.assertEqual(row[0], "never")
        self.assertEqual(row[1], "")
        tables = {r[0] for r in conn.execute("SELECT name FROM sqlite_master WHERE type='table'")}
        self.assertIn("privilege_approvals", tables)
        conn.close()


    def test_migrate_v2_discards_unscoped_temporary_approvals(self):
        conn = sqlite3.connect(self.db_path)
        conn.executescript("""
            CREATE TABLE targets (
                id TEXT PRIMARY KEY,
                display_name TEXT NOT NULL,
                platform TEXT NOT NULL DEFAULT 'linux',
                host TEXT NOT NULL,
                port INTEGER NOT NULL DEFAULT 22,
                user TEXT NOT NULL,
                ssh_alias TEXT,
                privilege_policy TEXT NOT NULL DEFAULT 'never',
                enabled INTEGER NOT NULL DEFAULT 1,
                created_at TEXT NOT NULL DEFAULT (datetime('now')),
                updated_at TEXT NOT NULL DEFAULT (datetime('now'))
            );
            CREATE TABLE settings (
                key TEXT PRIMARY KEY,
                value TEXT NOT NULL,
                updated_at TEXT NOT NULL DEFAULT (datetime('now'))
            );
            CREATE TABLE privilege_approvals (
                target_id TEXT PRIMARY KEY REFERENCES targets(id),
                policy TEXT NOT NULL,
                boot_id TEXT NOT NULL DEFAULT '',
                uses_remaining INTEGER NOT NULL DEFAULT 0,
                approved_at TEXT NOT NULL DEFAULT (datetime('now'))
            );
            INSERT INTO targets (
                id, display_name, host, user, privilege_policy
            ) VALUES ('t1', 'T1', '127.0.0.1', 'worker', 'ask_always');
            INSERT INTO privilege_approvals (
                target_id, policy, boot_id, uses_remaining
            ) VALUES ('t1', 'ask_always', 'old-boot', 1);
            PRAGMA user_version = 2;
        """)
        conn.commit()

        migrate_db(conn)

        self.assertEqual(get_schema_version(conn), 4)
        self.assertEqual(
            conn.execute("SELECT COUNT(*) FROM privilege_approvals").fetchone()[0],
            0,
        )
        columns = {
            row[1] for row in conn.execute("PRAGMA table_info(privilege_approvals)")
        }
        self.assertTrue({"client_id", "project_id", "approved_at"}.issubset(columns))
        target_columns = {
            row[1] for row in conn.execute("PRAGMA table_info(targets)")
        }
        self.assertIn("privilege_user", target_columns)
        conn.close()

    def test_migrate_v3_adds_privileged_ssh_user_without_changing_target_identity(self):
        conn = sqlite3.connect(self.db_path)
        conn.executescript("""
            CREATE TABLE targets (
                id TEXT PRIMARY KEY,
                display_name TEXT NOT NULL,
                platform TEXT NOT NULL DEFAULT 'linux',
                host TEXT NOT NULL,
                port INTEGER NOT NULL DEFAULT 22,
                user TEXT NOT NULL,
                ssh_alias TEXT,
                privilege_policy TEXT NOT NULL DEFAULT 'never',
                enabled INTEGER NOT NULL DEFAULT 1,
                created_at TEXT NOT NULL DEFAULT (datetime('now')),
                updated_at TEXT NOT NULL DEFAULT (datetime('now'))
            );
            CREATE TABLE settings (
                key TEXT PRIMARY KEY,
                value TEXT NOT NULL,
                updated_at TEXT NOT NULL DEFAULT (datetime('now'))
            );
            INSERT INTO targets (
                id, display_name, platform, host, port, user, ssh_alias, privilege_policy
            ) VALUES (
                't1', 'T1', 'windows', '192.168.1.10', 22, 'worker', 't1', 'ask_always'
            );
            PRAGMA user_version = 3;
        """)
        conn.commit()

        migrate_db(conn)

        self.assertEqual(get_schema_version(conn), 4)
        row = conn.execute(
            "SELECT platform, host, port, user, ssh_alias, privilege_policy, privilege_user "
            "FROM targets WHERE id='t1'"
        ).fetchone()
        self.assertEqual(
            row,
            ("windows", "192.168.1.10", 22, "worker", "t1", "ask_always", ""),
        )
        conn.close()
