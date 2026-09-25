import json
import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..")))
sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "src")))

from mcp_gateway.admin_cli import backup_cmd, export_json_cmd, import_json_cmd, set_password_cmd
from mcp_gateway.config import GatewayConfig
from mcp_gateway.registry import SQLiteRegistry, import_from_json


class TestAdminCLI(unittest.TestCase):
    def setUp(self):
        self.temp_dir = tempfile.TemporaryDirectory()
        self.db_path = os.path.join(self.temp_dir.name, "admin_test.db")
        self.registry = SQLiteRegistry(self.db_path)

        # Seed data
        raw_data = {
            "targets": {
                "t1": {
                    "platform": "linux",
                    "host": "192.168.1.10",
                    "port": 22,
                    "user": "u1",
                    "ssh_alias": "alias1",
                    "enabled": True,
                    "projects": {
                        "p1": {
                            "root": "/srv/p1",
                            "read": True,
                            "write": False,
                            "tasks": {
                                "build": {"argv": ["make"], "timeout": 30}
                            }
                        }
                    }
                }
            }
        }
        cfg = GatewayConfig(raw_data)
        import_from_json(cfg, self.registry)

    def tearDown(self):
        self.temp_dir.cleanup()

    def test_set_password(self):
        ret = set_password_cmd("admin", self.db_path, password="SecurePassword123!")
        self.assertEqual(ret, 0)

        user = self.registry.get_admin_user("admin")
        self.assertIsNotNone(user)
        self.assertNotEqual(user["password_hash"], "SecurePassword123!")
        self.assertTrue(user["password_hash"].startswith("pbkdf2:") or user["password_hash"].startswith("scrypt:"))

    def test_backup(self):
        backup_path = os.path.join(self.temp_dir.name, "backup.db")
        ret = backup_cmd(self.db_path, backup_path)
        self.assertEqual(ret, 0)
        self.assertTrue(os.path.exists(backup_path))

        # Check backup content
        backup_reg = SQLiteRegistry(backup_path)
        targets = backup_reg.list_targets()
        self.assertEqual(len(targets), 1)
        self.assertEqual(targets[0]["id"], "t1")

    def test_export_json_sanitized(self):
        # Add an admin user with password hash and an ai_client
        set_password_cmd("admin", self.db_path, password="SecretPassword")
        self.registry.add_client({"id": "c1", "display_name": "ChatGPT", "provider": "openai"})

        export_path = os.path.join(self.temp_dir.name, "export.json")
        ret = export_json_cmd(self.db_path, export_path)
        self.assertEqual(ret, 0)

        with open(export_path, "r", encoding="utf-8") as f:
            data = json.load(f)

        self.assertIn("targets", data)
        self.assertIn("t1", data["targets"])
        self.assertIn("ai_clients", data)
        self.assertEqual(data["ai_clients"][0]["id"], "c1")
        self.assertIn("settings", data)
        self.assertEqual(data["settings"]["gateway_enabled"], "true")
        self.assertEqual(data["targets"]["t1"]["privilege_policy"], "never")

        # Sanity check: NO passwords, NO hashes, NO admin_users
        with open(export_path, "r", encoding="utf-8") as f:
            export_raw = f.read()
        self.assertNotIn("SecretPassword", export_raw)
        self.assertNotIn("password_hash", export_raw)
        self.assertNotIn("admin_users", export_raw)
        self.assertNotIn("privilege_approvals", export_raw)

    def test_import_json(self):
        json_path = os.path.join(self.temp_dir.name, "targets_import.json")
        data = {
            "version": "1.0",
            "targets": {
                "import_target": {
                    "platform": "linux",
                    "host": "10.0.0.1",
                    "port": 2222,
                    "user": "imported_user",
                    "ssh_alias": "imported_alias",
                    "privilege_policy": "ask_once_per_boot",
                    "enabled": True,
                    "projects": {
                        "import_proj": {
                            "root": "/import/root",
                            "read": True,
                            "write": False,
                            "tasks": {}
                        }
                    }
                }
            }
        }
        with open(json_path, "w") as f:
            json.dump(data, f)

        ret = import_json_cmd(json_path, self.db_path)
        self.assertEqual(ret, 0)

        t = self.registry.get_target("import_target")
        self.assertEqual(t["host"], "10.0.0.1")
        self.assertEqual(t["privilege_policy"], "ask_once_per_boot")
        self.assertIn("import_proj", t["projects"])


if __name__ == "__main__":
    unittest.main()
