import os
import tempfile
import unittest
from unittest import mock

from werkzeug.datastructures import MultiDict
from werkzeug.security import generate_password_hash

from mcp_gateway.policy import authorize_client
from mcp_gateway.registry import SQLiteRegistry
from mcp_gateway.tools import GatewayTools
from mcp_gateway.web import create_app, get_or_create_secret_key


class HardeningRegressionTests(unittest.TestCase):
    def setUp(self):
        self.temp_dir = tempfile.TemporaryDirectory()
        self.db_path = os.path.join(self.temp_dir.name, "hardening.db")
        self.registry = SQLiteRegistry(self.db_path)
        self.registry.set_admin_password(
            "admin", generate_password_hash("Password123!")
        )
        self.registry.add_target(
            {
                "id": "t1",
                "display_name": "Target 1",
                "platform": "linux",
                "host": "127.0.0.1",
                "port": 22,
                "user": "worker",
                "ssh_alias": "",
                "privilege_user": "",
                "privilege_policy": "never",
                "enabled": True,
            }
        )
        self.registry.add_project(
            "t1",
            {
                "id": "p1",
                "display_name": "Project 1",
                "root": "/srv/p1",
                "read": True,
                "write": False,
                "enabled": True,
                "tasks": {},
            },
        )
        self.registry.add_client(
            {
                "id": "client-a",
                "display_name": "Client A",
                "provider": "test",
                "protocol": "mcp",
                "enabled": True,
                "notes": "",
            }
        )
        self.app = create_app(
            registry=self.registry,
            secret_key="test-hardening-secret",
            test_config={"TESTING": True},
        )
        self.client = self.app.test_client()

    def tearDown(self):
        self.temp_dir.cleanup()

    def _login(self):
        self.client.get("/login")
        with self.client.session_transaction() as session:
            token = session["csrf_token"]
        response = self.client.post(
            "/login",
            data={
                "username": "admin",
                "password": "Password123!",
                "csrf_token": token,
            },
        )
        self.assertEqual(response.status_code, 302)
        with self.client.session_transaction() as session:
            return session["csrf_token"]

    def test_login_next_rejects_network_path_redirect(self):
        self.client.get("/login")
        with self.client.session_transaction() as session:
            token = session["csrf_token"]
        response = self.client.post(
            "/login?next=//example.com/phish",
            data={
                "username": "admin",
                "password": "Password123!",
                "csrf_token": token,
            },
            follow_redirects=False,
        )
        self.assertEqual(response.status_code, 302)
        self.assertTrue(response.headers["Location"].endswith("/dashboard"))
        self.assertNotIn("example.com", response.headers["Location"])

    def test_registry_can_inspect_disabled_objects_without_enabling_them(self):
        self.registry.update_target("t1", {"enabled": False})
        with self.assertRaises(Exception):
            self.registry.get_target("t1")
        target = self.registry.get_target("t1", include_disabled=True)
        self.assertFalse(target["enabled"])

        self.registry.update_target("t1", {"enabled": True})
        self.registry.update_project("t1", "p1", {"enabled": False})
        with self.assertRaises(Exception):
            self.registry.get_project("t1", "p1")
        project = self.registry.get_project(
            "t1", "p1", include_disabled=True
        )
        self.assertFalse(project["enabled"])

    def test_enabling_project_write_fails_closed_when_audit_is_unavailable(self):
        csrf = self._login()
        original = self.registry.record_activity

        def broken_audit(_entry):
            raise OSError("audit unavailable")

        self.registry.record_activity = broken_audit
        try:
            with self.assertRaises(RuntimeError):
                self.client.post(
                    "/projects/t1/p1/toggle-write",
                    data={"csrf_token": csrf},
                )
            self.assertFalse(
                self.registry.get_project("t1", "p1")["write"]
            )
        finally:
            self.registry.record_activity = original

    def test_disabling_project_write_remains_available_if_audit_is_unavailable(self):
        csrf = self._login()
        self.registry.update_project("t1", "p1", {"write": True})
        original = self.registry.record_activity

        def broken_audit(_entry):
            raise OSError("audit unavailable")

        self.registry.record_activity = broken_audit
        try:
            response = self.client.post(
                "/projects/t1/p1/toggle-write",
                data={"csrf_token": csrf},
            )
            self.assertEqual(response.status_code, 302)
            self.assertFalse(
                self.registry.get_project("t1", "p1")["write"]
            )
        finally:
            self.registry.record_activity = original

    def test_enabling_global_writes_fails_closed_when_audit_is_unavailable(self):
        csrf = self._login()
        self.registry.set_setting("writes_enabled", "false")
        original = self.registry.record_activity

        def broken_audit(_entry):
            raise OSError("audit unavailable")

        self.registry.record_activity = broken_audit
        try:
            with self.assertRaises(RuntimeError):
                self.client.post(
                    "/settings/toggle-writes",
                    data={"csrf_token": csrf},
                )
            self.assertEqual(
                self.registry.get_setting("writes_enabled"), "false"
            )
        finally:
            self.registry.record_activity = original

    def test_semantic_grant_form_reuses_canonical_csv_storage(self):
        csrf = self._login()
        response = self.client.post(
            "/clients/client-a/grants/add",
            data=MultiDict(
                [
                    ("target_id", "t1"),
                    ("project_id", "p1"),
                    ("capabilities", "read"),
                    ("capabilities", "write"),
                    ("target_admin", "on"),
                    ("enabled", "on"),
                    ("csrf_token", csrf),
                ]
            ),
        )
        self.assertEqual(response.status_code, 302)
        grant = self.registry.list_grants(client_id="client-a")[0]
        self.assertEqual(grant["capability"], "read,write,target_admin")

    def test_wildcard_can_carry_explicit_target_admin_without_implying_it(self):
        csrf = self._login()
        response = self.client.post(
            "/clients/client-a/grants/add",
            data=MultiDict(
                [
                    ("target_id", "t1"),
                    ("project_id", "p1"),
                    ("capabilities", "*"),
                    ("target_admin", "on"),
                    ("enabled", "on"),
                    ("csrf_token", csrf),
                ]
            ),
        )
        self.assertEqual(response.status_code, 302)
        grant = self.registry.list_grants(client_id="client-a")[0]
        self.assertEqual(grant["capability"], "*,target_admin")

    def test_activity_filters_are_applied_server_side(self):
        csrf = self._login()
        self.assertTrue(csrf)
        self.registry.record_activity(
            {
                "actor": "client-a",
                "action": "special_pass_event",
                "target_id": "t1",
                "project_id": "p1",
                "success": True,
            }
        )
        self.registry.record_activity(
            {
                "actor": "client-a",
                "action": "special_deny_event",
                "target_id": "t1",
                "project_id": "p1",
                "success": False,
                "error_code": "DENIED",
            }
        )
        response = self.client.get(
            "/activity?action=special&result=deny&target_id=t1"
        )
        self.assertEqual(response.status_code, 200)
        self.assertIn(b"special_deny_event", response.data)
        self.assertNotIn(b"special_pass_event", response.data)

        rows = self.registry.list_activity(
            filters={"action": "special", "success": False}
        )
        self.assertEqual([row["action"] for row in rows], ["special_deny_event"])
        self.assertEqual(
            self.registry.get_activity_count(
                filters={"action": "special", "success": False}
            ),
            1,
        )

    def test_missing_shell_and_write_settings_fail_closed_in_policy(self):
        class MissingSettingsRegistry:
            def get_client(self, _client_id):
                return {"enabled": True}

            def get_client_grants(self, _client_id):
                return [
                    {
                        "enabled": True,
                        "target_id": "t1",
                        "project_id": "p1",
                        "capability": "target_shell,write",
                    }
                ]

            def get_setting(self, _key, default=None):
                return default

            def get_target(self, _target_id):
                return {"enabled": True}

            def get_project(self, _target_id, _project_id):
                return {"enabled": True, "write": True}

        registry = MissingSettingsRegistry()

        allowed, reason = authorize_client(
            "client-a", "t1", "p1", "run_command", registry=registry
        )
        self.assertFalse(allowed)
        self.assertIn("TARGET_SHELL_DISABLED", reason)

        allowed, reason = authorize_client(
            "client-a", "t1", "p1", "write_file", registry=registry
        )
        self.assertFalse(allowed)
        self.assertIn("WRITES_DISABLED", reason)

    def test_gateway_tools_missing_shell_and_write_settings_fail_closed(self):
        class MissingSettingsConfig:
            def get_setting(self, _key, default=None):
                return default

        transport = mock.MagicMock()
        tools = GatewayTools(config=MissingSettingsConfig(), transport=transport)
        self.assertFalse(tools._is_shell_enabled())
        self.assertFalse(tools._is_writes_enabled())

    def test_activity_live_mode_is_explicit_and_htmx_driven(self):
        self._login()

        normal = self.client.get("/activity")
        self.assertEqual(normal.status_code, 200)
        self.assertIn(b"LIVE", normal.data)
        self.assertNotIn(b'hx-trigger="every 3s"', normal.data)

        live = self.client.get("/activity?live=1")
        self.assertEqual(live.status_code, 200)
        self.assertIn(b"Pause LIVE", live.data)
        self.assertIn(b'hx-trigger="every 3s"', live.data)
        self.assertIn(b'hx-swap="outerHTML show:none"', live.data)

    def test_secret_key_persistence_failure_is_not_silently_ignored(self):
        path = os.path.join(self.temp_dir.name, "admin-secret")
        with mock.patch(
            "mcp_gateway.web.os.open",
            side_effect=PermissionError("read-only filesystem"),
        ):
            with self.assertRaises(PermissionError):
                get_or_create_secret_key(path)


if __name__ == "__main__":
    unittest.main()
