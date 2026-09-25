import os
import sys
import tempfile
import time
import unittest

sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..")))
sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "src")))

from mcp_gateway.config import GatewayConfig, ConfigError
from mcp_gateway.registry import JsonRegistry, SQLiteRegistry, import_from_json, get_registry


class TestJsonRegistry(unittest.TestCase):
    def setUp(self):
        # A mock dict to act as config backend
        raw_data = {
            "targets": {
                "t1": {
                    "platform": "linux",
                    "host": "localhost",
                    "user": "root",
                    "ssh_alias": "test",
                    "enabled": True,
                    "projects": {
                        "p1": {
                            "root": "/tmp",
                            "tasks": {}
                        }
                    }
                },
                "t2": {
                    "platform": "linux",
                    "host": "localhost",
                    "user": "root",
                    "ssh_alias": "test2",
                    "enabled": False,
                    "projects": {}
                }
            }
        }
        self.config = GatewayConfig(raw_data)
        self.registry = JsonRegistry(self.config)

    def test_list_targets(self):
        targets = self.registry.list_targets()
        self.assertEqual(len(targets), 2)
        self.assertEqual(targets[0]["id"], "t1")
        self.assertEqual(targets[1]["id"], "t2")
        self.assertEqual(self.registry.target_count, 2)

    def test_get_target(self):
        t1 = self.registry.get_target("t1")
        self.assertEqual(t1["platform"], "linux")
        self.assertEqual(t1["host"], "localhost")

    def test_get_target_unknown(self):
        with self.assertRaises(ConfigError) as ctx:
            self.registry.get_target("nonexistent")
        self.assertEqual(ctx.exception.code, "UNKNOWN_TARGET")

    def test_get_target_disabled(self):
        with self.assertRaises(ConfigError) as ctx:
            self.registry.get_target("t2")
        self.assertEqual(ctx.exception.code, "TARGET_DISABLED")

    def test_get_project(self):
        p1 = self.registry.get_project("t1", "p1")
        self.assertEqual(p1["root"], "/tmp")

    def test_get_project_unknown(self):
        with self.assertRaises(ConfigError) as ctx:
            self.registry.get_project("t1", "nonexistent")
        self.assertEqual(ctx.exception.code, "UNKNOWN_PROJECT")

    def test_mutations_raise(self):
        with self.assertRaises(NotImplementedError):
            self.registry.add_target({})
        with self.assertRaises(NotImplementedError):
            self.registry.update_target("t1", {})
        with self.assertRaises(NotImplementedError):
            self.registry.add_project("t1", {})
        with self.assertRaises(NotImplementedError):
            self.registry.update_project("t1", "p1", {})

    def test_in_memory_stores(self):
        self.registry.set_setting("foo", "bar")
        self.assertEqual(self.registry.get_setting("foo"), "bar")
        self.assertIsNone(self.registry.get_setting("nonexistent"))

        client_id = self.registry.add_client({"name": "c1"})
        c1 = self.registry.get_client(client_id)
        self.assertEqual(c1["name"], "c1")

        self.registry.update_client(client_id, {"name": "c2"})
        self.assertEqual(self.registry.get_client(client_id)["name"], "c2")

        self.registry.record_activity({"action": "test"})
        self.assertEqual(self.registry.get_activity_count(), 1)
        activities = self.registry.list_activity()
        self.assertEqual(activities[0]["action"], "test")

        self.registry.set_admin_password("admin", "hash")
        admin = self.registry.get_admin_user("admin")
        self.assertEqual(admin["username"], "admin")
        self.assertEqual(admin["password_hash"], "hash")

        # Test grants in JsonRegistry
        gid = self.registry.add_grant({"client_id": client_id, "capability": "read"})
        self.assertEqual(len(self.registry.list_grants(client_id=client_id)), 1)
        self.assertEqual(self.registry.get_grant(gid)["capability"], "read")
        self.registry.update_grant(gid, {"capability": "write"})
        self.assertEqual(self.registry.get_grant(gid)["capability"], "write")
        self.registry.delete_grant(gid)
        self.assertEqual(len(self.registry.list_grants(client_id=client_id)), 0)

import tempfile
import os
from mcp_gateway.registry import SQLiteRegistry

class TestSQLiteRegistry(unittest.TestCase):
    def setUp(self):
        self.fd, self.db_path = tempfile.mkstemp()
        os.close(self.fd)
        self.registry = SQLiteRegistry(self.db_path)

    def tearDown(self):
        import gc
        gc.collect()
        if os.path.exists(self.db_path):
            try:
                os.remove(self.db_path)
            except Exception:
                pass

    def test_grants_crud(self):
        client_id = self.registry.add_client({"display_name": "Test Client"})
        target_id = self.registry.add_target({"display_name": "Target 1", "host": "127.0.0.1", "user": "test"})

        # 1. Add grant
        gid = self.registry.add_grant({
            "client_id": client_id,
            "target_id": target_id,
            "project_id": "*",
            "capability": "read",
            "enabled": True,
        })
        self.assertGreater(gid, 0)

        # 2. Get grant
        g = self.registry.get_grant(gid)
        self.assertEqual(g["client_id"], client_id)
        self.assertEqual(g["capability"], "read")
        self.assertTrue(g["enabled"])

        # 3. List grants
        grants = self.registry.list_grants(client_id=client_id)
        self.assertEqual(len(grants), 1)
        self.assertEqual(grants[0]["id"], gid)

        active = self.registry.get_client_grants(client_id)
        self.assertEqual(len(active), 1)

        # 4. Update grant
        self.registry.update_grant(gid, {"capability": "write", "enabled": False})
        g_updated = self.registry.get_grant(gid)
        self.assertEqual(g_updated["capability"], "write")
        self.assertFalse(g_updated["enabled"])

        active_after = self.registry.get_client_grants(client_id)
        self.assertEqual(len(active_after), 0)

        # 5. Delete grant
        self.registry.delete_grant(gid)
        with self.assertRaises(KeyError):
            self.registry.get_grant(gid)

    def test_crud_targets(self):
        tid = self.registry.add_target({
            "display_name": "Test",
            "host": "localhost",
            "user": "root"
        })
        targets = self.registry.list_targets()
        self.assertEqual(len(targets), 1)
        self.assertEqual(targets[0]["id"], tid)
        
        target = self.registry.get_target(tid)
        self.assertEqual(target["host"], "localhost")
        self.assertEqual(target["enabled"], True)
        self.assertEqual(target["projects"], {})

        self.registry.update_target(tid, {"host": "remote"})
        target = self.registry.get_target(tid)
        self.assertEqual(target["host"], "remote")
        
        self.registry.update_target(tid, {"enabled": False})
        with self.assertRaises(ConfigError) as ctx:
            self.registry.get_target(tid)
        self.assertEqual(ctx.exception.code, "TARGET_DISABLED")

    def test_target_privilege_policy_and_approvals(self):
        tid = self.registry.add_target({
            "id": "priv-target",
            "host": "127.0.0.1",
            "user": "worker",
            "privilege_user": "root",
            "privilege_policy": "ask_always",
        })
        target = self.registry.get_target(tid)
        self.assertEqual(target["privilege_policy"], "ask_always")
        self.assertEqual(target["privilege_user"], "root")

        self.registry.set_privilege_approval(
            tid,
            "ask_always",
            "c1",
            "p1",
        )
        approval = self.registry.get_privilege_approval(tid)
        self.assertIsNotNone(approval)
        self.assertEqual(approval["client_id"], "c1")
        self.assertEqual(approval["project_id"], "p1")

        # Cached approval is scoped; another client or project cannot consume it.
        self.assertFalse(
            self.registry.consume_privilege_approval(
                tid, "ask_always", "c2", "p1"
            )
        )
        self.assertFalse(
            self.registry.consume_privilege_approval(
                tid, "ask_always", "c1", "p2"
            )
        )
        self.assertIsNotNone(self.registry.get_privilege_approval(tid))

        self.assertTrue(
            self.registry.consume_privilege_approval(
                tid, "ask_always", "c1", "p1"
            )
        )
        self.assertIsNone(self.registry.get_privilege_approval(tid))
        self.assertFalse(
            self.registry.consume_privilege_approval(
                tid, "ask_always", "c1", "p1"
            )
        )

        # ask_always approvals expire rather than becoming indefinite capabilities.
        self.registry.set_privilege_approval(
            tid,
            "ask_always",
            "c1",
            "p1",
        )
        with self.registry._get_conn() as conn:
            conn.execute(
                "UPDATE privilege_approvals SET approved_at = ? WHERE target_id = ?",
                (time.time() - 3600, tid),
            )
            conn.commit()
        self.assertFalse(
            self.registry.consume_privilege_approval(
                tid, "ask_always", "c1", "p1", max_age_seconds=300
            )
        )
        self.assertIsNone(self.registry.get_privilege_approval(tid))

        self.registry.update_target(tid, {"privilege_policy": "ask_once_per_boot"})
        self.registry.set_privilege_approval(
            tid,
            "ask_once_per_boot",
            "c1",
            "p1",
            boot_id="boot-a",
        )
        self.assertTrue(
            self.registry.consume_privilege_approval(
                tid, "ask_once_per_boot", "c1", "p1", boot_id="boot-a"
            )
        )
        self.assertFalse(
            self.registry.consume_privilege_approval(
                tid, "ask_once_per_boot", "c1", "p1", boot_id="boot-b"
            )
        )
        self.assertIsNone(self.registry.get_privilege_approval(tid))

        self.registry.set_privilege_approval(
            tid,
            "ask_once_per_boot",
            "c1",
            "p1",
            boot_id="boot-a",
        )
        # Cosmetic Target edits preserve approval.
        self.registry.update_target(tid, {"display_name": "Renamed only"})
        self.assertIsNotNone(self.registry.get_privilege_approval(tid))

        # Operational Target identity changes revoke cached approval.
        self.registry.update_target(tid, {"host": "localhost"})
        self.assertIsNone(self.registry.get_privilege_approval(tid))

        self.registry.set_privilege_approval(
            tid,
            "ask_once_per_boot",
            "c1",
            "p1",
            boot_id="boot-a",
        )
        # Changing the privileged SSH identity also revokes cached approval.
        self.registry.update_target(tid, {"host": "127.0.0.1"})
        self.registry.set_privilege_approval(
            tid,
            "ask_once_per_boot",
            "c1",
            "p1",
            boot_id="boot-a",
        )
        self.registry.update_target(tid, {"privilege_user": "admin-root"})
        self.assertIsNone(self.registry.get_privilege_approval(tid))
        self.assertEqual(self.registry.get_target(tid)["privilege_user"], "admin-root")

        self.registry.set_privilege_approval(
            tid,
            "ask_once_per_boot",
            "c1",
            "p1",
            boot_id="boot-a",
        )
        # Changing policy also revokes cached approval.
        self.registry.update_target(tid, {"privilege_policy": "never"})
        self.assertIsNone(self.registry.get_privilege_approval(tid))
        self.assertEqual(self.registry.get_target(tid)["privilege_policy"], "never")

    def test_privilege_approval_revoked_when_authorization_context_changes(self):
        tid = self.registry.add_target({
            "id": "ctx-target",
            "host": "127.0.0.1",
            "user": "worker",
            "privilege_policy": "ask_once_per_boot",
        })
        pid = self.registry.add_project(tid, {
            "id": "ctx-project",
            "root": "/tmp",
            "enabled": True,
        })
        cid = self.registry.add_client({
            "id": "ctx-client",
            "display_name": "Context Client",
            "enabled": True,
        })
        gid = self.registry.add_grant({
            "client_id": cid,
            "target_id": tid,
            "project_id": pid,
            "capability": "target_admin",
            "enabled": True,
        })

        def approve():
            self.registry.set_privilege_approval(
                tid,
                "ask_once_per_boot",
                cid,
                pid,
                boot_id="boot-a",
            )
            self.assertIsNotNone(self.registry.get_privilege_approval(tid))

        approve()
        self.registry.update_project(tid, pid, {"display_name": "Cosmetic"})
        self.assertIsNotNone(self.registry.get_privilege_approval(tid))
        self.registry.update_project(tid, pid, {"root": "/var/tmp"})
        self.assertIsNone(self.registry.get_privilege_approval(tid))

        approve()
        self.registry.update_client(cid, {"display_name": "Cosmetic Client"})
        self.assertIsNotNone(self.registry.get_privilege_approval(tid))
        self.registry.update_client(cid, {"enabled": False})
        self.assertIsNone(self.registry.get_privilege_approval(tid))

        self.registry.update_client(cid, {"enabled": True})
        approve()
        self.registry.update_grant(gid, {"capability": "target_admin,read"})
        self.assertIsNone(self.registry.get_privilege_approval(tid))

        approve()
        self.registry.add_grant({
            "client_id": cid,
            "target_id": tid,
            "project_id": pid,
            "capability": "read",
            "enabled": True,
        })
        self.assertIsNone(self.registry.get_privilege_approval(tid))

        approve()
        self.registry.delete_grant(gid)
        self.assertIsNone(self.registry.get_privilege_approval(tid))

    def test_crud_projects(self):
        tid = self.registry.add_target({"host": "local", "user": "test"})
        pid = self.registry.add_project(tid, {
            "root": "/tmp",
            "tasks": {
                "test": {"argv": ["ls"], "timeout": 10}
            }
        })
        
        projects = self.registry.list_projects(tid)
        self.assertEqual(len(projects), 1)
        self.assertEqual(projects[0]["id"], pid)
        
        target = self.registry.get_target(tid)
        self.assertIn(pid, target["projects"])
        self.assertEqual(target["projects"][pid]["root"], "/tmp")
        self.assertEqual(target["projects"][pid]["tasks"]["test"]["argv"], ["ls"])
        
        self.registry.update_project(tid, pid, {"root": "/var"})
        target = self.registry.get_target(tid)
        self.assertEqual(target["projects"][pid]["root"], "/var")

    def test_crud_clients(self):
        cid = self.registry.add_client({"display_name": "Client1"})
        clients = self.registry.list_clients()
        self.assertEqual(len(clients), 1)
        self.assertEqual(clients[0]["id"], cid)
        
        client = self.registry.get_client(cid)
        self.assertEqual(client["display_name"], "Client1")
        
        self.registry.update_client(cid, {"display_name": "Client2"})
        client = self.registry.get_client(cid)
        self.assertEqual(client["display_name"], "Client2")

    def test_settings(self):
        self.assertEqual(self.registry.get_setting("gateway_enabled"), "true")
        self.registry.set_setting("new_key", "val")
        self.assertEqual(self.registry.get_setting("new_key"), "val")
        self.registry.set_setting("new_key", "val2")
        self.assertEqual(self.registry.get_setting("new_key"), "val2")

    def test_activity(self):
        self.registry.record_activity({"action": "test1"})
        self.registry.record_activity({"action": "test2"})
        self.assertEqual(self.registry.get_activity_count(), 2)
        
        activities = self.registry.list_activity()
        self.assertEqual(len(activities), 2)
        self.assertEqual(activities[0]["action"], "test2") # Descending timestamp
        
        pruned = self.registry.prune_activity(1)
        self.assertEqual(pruned, 1)
        self.assertEqual(self.registry.get_activity_count(), 1)
        self.assertEqual(self.registry.list_activity()[0]["action"], "test2")

    def test_admin_users(self):
        self.registry.set_admin_password("admin", "hash1")
        admin = self.registry.get_admin_user("admin")
        self.assertEqual(admin["password_hash"], "hash1")
        self.assertIsNone(admin["last_login"])
        
        self.registry.update_admin_login("admin")
        admin = self.registry.get_admin_user("admin")
        self.assertIsNotNone(admin["last_login"])


class TestRegistryParityAndImport(unittest.TestCase):
    def setUp(self):
        self.raw_data = {
            "targets": {
                "termux-main": {
                    "platform": "android-termux",
                    "host": "192.168.68.84",
                    "port": 8022,
                    "user": "u0_a435",
                    "ssh_alias": "termux-local",
                    "privilege_user": "root",
                    "enabled": True,
                    "projects": {
                        "MCP_Local": {
                            "root": "/data/data/com.termux/files/home/Projects/test/MCP_Local",
                            "read": True,
                            "write": False,
                            "tasks": {
                                "git_status": {
                                    "enabled": True,
                                    "argv": ["git", "status", "--short"],
                                    "timeout": 30
                                }
                            }
                        }
                    }
                },
                "disabled-box": {
                    "platform": "linux",
                    "host": "192.168.1.99",
                    "port": 22,
                    "user": "worker",
                    "ssh_alias": "disabled-alias",
                    "enabled": False,
                    "projects": {
                        "dormant": {
                            "root": "/home/worker/dormant",
                            "read": True,
                            "write": False,
                            "tasks": {}
                        }
                    }
                }
            }
        }
        self.config = GatewayConfig(self.raw_data)
        self.json_registry = JsonRegistry(self.config)

        self.temp_dir = tempfile.TemporaryDirectory()
        self.db_path = os.path.join(self.temp_dir.name, "parity_test.db")
        self.sqlite_registry = SQLiteRegistry(self.db_path)

    def tearDown(self):
        self.temp_dir.cleanup()

    def test_import_and_parity(self):
        summary = import_from_json(self.config, self.sqlite_registry)
        self.assertEqual(summary["targets_imported"], 2)
        self.assertEqual(summary["projects_imported"], 2)
        self.assertEqual(summary["tasks_imported"], 1)

        # 1. Same target lookup by ID
        j_target = self.json_registry.get_target("termux-main")
        s_target = self.sqlite_registry.get_target("termux-main")
        self.assertEqual(j_target["host"], s_target["host"])
        self.assertEqual(j_target["port"], s_target["port"])
        self.assertEqual(j_target["user"], s_target["user"])
        self.assertEqual(j_target["platform"], s_target["platform"])
        self.assertEqual(j_target["ssh_alias"], s_target["ssh_alias"])
        self.assertEqual(j_target.get("privilege_user", ""), s_target["privilege_user"])
        self.assertEqual(j_target["enabled"], s_target["enabled"])

        # 2. Same project lookup by (target_id, project_id)
        j_proj = self.json_registry.get_project("termux-main", "MCP_Local")
        s_proj = self.sqlite_registry.get_project("termux-main", "MCP_Local")
        self.assertEqual(j_proj["root"], s_proj["root"])
        self.assertEqual(j_proj["read"], s_proj["read"])
        self.assertEqual(j_proj["write"], s_proj["write"])
        self.assertEqual(j_proj["tasks"]["git_status"]["argv"], s_proj["tasks"]["git_status"]["argv"])
        self.assertEqual(j_proj["tasks"]["git_status"]["timeout"], s_proj["tasks"]["git_status"]["timeout"])

        # 3. Same unknown-target denial
        with self.assertRaises(ConfigError) as j_err:
            self.json_registry.get_target("nonexistent")
        with self.assertRaises(ConfigError) as s_err:
            self.sqlite_registry.get_target("nonexistent")
        self.assertEqual(j_err.exception.code, "UNKNOWN_TARGET")
        self.assertEqual(s_err.exception.code, "UNKNOWN_TARGET")

        # 4. Same disabled-target denial
        with self.assertRaises(ConfigError) as j_dis:
            self.json_registry.get_target("disabled-box")
        with self.assertRaises(ConfigError) as s_dis:
            self.sqlite_registry.get_target("disabled-box")
        self.assertEqual(j_dis.exception.code, "TARGET_DISABLED")
        self.assertEqual(s_dis.exception.code, "TARGET_DISABLED")

        # 5. Same unknown-project denial
        with self.assertRaises(ConfigError) as j_perr:
            self.json_registry.get_project("termux-main", "unknown-proj")
        with self.assertRaises(ConfigError) as s_perr:
            self.sqlite_registry.get_project("termux-main", "unknown-proj")
        self.assertEqual(j_perr.exception.code, "UNKNOWN_PROJECT")
        self.assertEqual(s_perr.exception.code, "UNKNOWN_PROJECT")

        # Idempotence: re-importing should not fail or duplicate
        summary2 = import_from_json(self.config, self.sqlite_registry)
        self.assertEqual(summary2["targets_imported"], 2)
        s_target2 = self.sqlite_registry.get_target("termux-main")
        self.assertEqual(s_target2["host"], "192.168.68.84")

    def test_get_registry_backend_selection(self):
        example_cfg = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "config", "targets.example.json"))
        # Json backend
        reg_json = get_registry(backend="json", config_path=example_cfg)
        self.assertIsInstance(reg_json, JsonRegistry)

        # SQLite backend
        reg_sqlite = get_registry(backend="sqlite", db_path=self.db_path)
        self.assertIsInstance(reg_sqlite, SQLiteRegistry)

        # Rollback via environment variable
        os.environ["MCP_GATEWAY_REGISTRY"] = "json"
        try:
            reg_env = get_registry(config_path=example_cfg)
            self.assertIsInstance(reg_env, JsonRegistry)
        finally:
            os.environ.pop("MCP_GATEWAY_REGISTRY", None)

