import os
import sqlite3
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "src")))

from mcp_gateway import lifecycle, schema


class TestLifecycle(unittest.TestCase):
    def test_create_and_verify_manifest(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            m_path = os.path.join(tmpdir, "manifest.json")
            m = lifecycle.create_manifest(version="1.2.1", output_path=m_path)
            self.assertEqual(m["version"], "1.2.1")
            self.assertEqual(m["registry_schema_range"], "1..4")

            self.assertTrue(os.path.isfile(m_path))

            ok, errors = lifecycle.verify_manifest(m_path)
            self.assertTrue(ok)
            self.assertEqual(len(errors), 0)

    def test_root_manifest(self):
        root_manifest = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "manifest.json"))
        if os.path.isfile(root_manifest):
            ok, errors = lifecycle.verify_manifest(root_manifest)
            self.assertTrue(ok, f"Root manifest verification failed: {errors}")

    def test_architecture_mismatch(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            m_path = os.path.join(tmpdir, "manifest.json")
            m = lifecycle.create_manifest(version="1.0.1", output_path=m_path)
            # Tamper with architecture
            m["architecture"] = "non_existent_arch_999"
            m["architectures"] = ["non_existent_arch_999"]
            import json
            with open(m_path, "w", encoding="utf-8") as f:
                json.dump(m, f)

            ok, errors = lifecycle.verify_manifest(m_path)
            self.assertFalse(ok)
            self.assertTrue(any("Architecture mismatch" in err for err in errors))

    def test_backup_and_restore_database(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            db_path = os.path.join(tmpdir, "test.db")
            schema.init_db(db_path)

            seed = sqlite3.connect(db_path)
            seed.execute(
                "INSERT INTO targets (id, display_name, host, user) VALUES (?, ?, ?, ?)",
                ("t1", "Target 1", "127.0.0.1", "worker"),
            )
            seed.execute(
                "INSERT INTO privilege_approvals (target_id, policy, client_id, project_id, boot_id, approved_at) VALUES (?, ?, ?, ?, ?, ?)",
                ("t1", "ask_always", "client-old", "project-old", "boot-old", 1.0),
            )
            seed.commit()
            seed.close()

            os.environ["MCP_GATEWAY_DB"] = db_path
            backup_path = os.path.join(tmpdir, "backup.db")

            # Perform backup
            result_path = lifecycle.backup_database(backup_path)
            self.assertEqual(result_path, backup_path)
            self.assertTrue(os.path.isfile(backup_path))

            # Modify db
            conn = sqlite3.connect(db_path)
            conn.execute("INSERT INTO settings (key, value) VALUES ('test_key', 'test_val')")
            conn.commit()
            conn.close()

            # Restore backup
            restored = lifecycle.restore_database(backup_path)
            self.assertTrue(restored)

            # Check that test_key is gone after restore
            conn2 = sqlite3.connect(db_path)
            cur = conn2.cursor()
            cur.execute("SELECT value FROM settings WHERE key = 'test_key'")
            row = cur.fetchone()
            cur.execute("SELECT COUNT(*) FROM privilege_approvals")
            approval_count = cur.fetchone()[0]
            cur.execute("SELECT COUNT(*) FROM targets WHERE id = 't1'")
            target_count = cur.fetchone()[0]
            conn2.close()
            self.assertIsNone(row)
            self.assertEqual(target_count, 1)
            self.assertEqual(approval_count, 0)

    def test_restore_migrates_supported_v1_backup_to_current_schema(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            db_path = os.path.join(tmpdir, "current.db")
            backup_path = os.path.join(tmpdir, "legacy-v1.db")

            conn = sqlite3.connect(backup_path)
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
                INSERT INTO targets (id, display_name, host, user)
                VALUES ('legacy', 'Legacy', '127.0.0.1', 'worker');
                PRAGMA user_version = 1;
            """)
            conn.commit()
            conn.close()

            old_db = os.environ.get("MCP_GATEWAY_DB")
            os.environ["MCP_GATEWAY_DB"] = db_path
            try:
                self.assertTrue(lifecycle.restore_database(backup_path))
                conn = sqlite3.connect(db_path)
                self.assertEqual(
                    conn.execute("PRAGMA user_version").fetchone()[0],
                    4,
                )
                self.assertEqual(
                    conn.execute(
                        "SELECT privilege_policy FROM targets WHERE id='legacy'"
                    ).fetchone()[0],
                    "never",
                )
                conn.close()
            finally:
                if old_db is None:
                    os.environ.pop("MCP_GATEWAY_DB", None)
                else:
                    os.environ["MCP_GATEWAY_DB"] = old_db

    def test_update_release(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            cand_dir = os.path.join(tmpdir, "candidate")
            os.makedirs(cand_dir)
            lifecycle.create_manifest(version="0.7.0", output_path=os.path.join(cand_dir, "manifest.json"))

            # Mock get_paths
            home_dir = os.path.join(tmpdir, "home")
            os.makedirs(home_dir)
            db_file = os.path.join(home_dir, "gateway.db")
            schema.init_db(db_file)

            paths = {
                "home": home_dir,
                "releases": os.path.join(home_dir, "releases"),
                "current": os.path.join(home_dir, "current"),
                "previous": os.path.join(home_dir, "previous"),
                "data": home_dir,
                "config": home_dir,
                "db": db_file,
                "backups": os.path.join(home_dir, "backups"),
            }

            orig_get_paths = lifecycle.get_paths
            lifecycle.get_paths = lambda: paths
            try:
                ok, msg = lifecycle.update_release(cand_dir)
                self.assertTrue(ok, f"update_release failed: {msg}")
                self.assertTrue(os.path.islink(paths["current"]))
                self.assertTrue(os.path.isdir(os.path.realpath(paths["current"])))
            finally:
                lifecycle.get_paths = orig_get_paths


if __name__ == "__main__":
    unittest.main()
