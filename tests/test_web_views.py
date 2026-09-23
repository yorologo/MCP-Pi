import os
import sys
import tempfile
import time
import unittest
from html.parser import HTMLParser

sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..")))
sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "src")))

from werkzeug.security import generate_password_hash
from mcp_gateway.registry import SQLiteRegistry
from mcp_gateway.ssh_transport import SSHTransportResult
from mcp_gateway.discovery import TargetDiscovery, parse_known_hosts_line
from mcp_gateway.tools import GatewayTools
from mcp_gateway.web import create_app


class FormAccessibilityParser(HTMLParser):
    def __init__(self):
        super().__init__()
        self.control_ids = []
        self.label_targets = set()

    def handle_starttag(self, tag, attrs):
        attrs = dict(attrs)
        if tag == "label" and attrs.get("for"):
            self.label_targets.add(attrs["for"])
        if tag in ("input", "select", "textarea") and attrs.get("type") != "hidden":
            self.control_ids.append(attrs.get("id"))


class CurrentNavigationParser(HTMLParser):
    def __init__(self):
        super().__init__()
        self.current_links = []

    def handle_starttag(self, tag, attrs):
        attrs = dict(attrs)
        if tag == "a" and attrs.get("aria-current") == "page":
            self.current_links.append(attrs.get("href"))


class MockTransport:
    def run_command(self, target, remote_cmd, timeout=None, cwd=None, **kwargs):
        return SSHTransportResult(0, "mock-host\n", "", 10)

    def resolve_canonical_path(self, target, candidate_path, timeout=10):
        return candidate_path


class TestWebViews(unittest.TestCase):
    def setUp(self):
        self.temp_dir = tempfile.TemporaryDirectory()
        self.db_path = os.path.join(self.temp_dir.name, "web_views_test.db")
        self.registry = SQLiteRegistry(self.db_path)
        self.registry.set_admin_password("admin", generate_password_hash("Password123!"))

        # Seed a target and a project
        self.registry.add_target({
            "id": "t1",
            "display_name": "Target 1",
            "platform": "linux",
            "host": "192.168.1.50",
            "port": 22,
            "user": "worker",
            "enabled": True,
        })
        self.registry.add_project("t1", {
            "id": "p1",
            "display_name": "Project 1",
            "root": "/srv/app",
            "read": True,
            "enabled": True,
        })

        self.tools = GatewayTools(registry=self.registry, transport=MockTransport())
        self.app = create_app(
            registry=self.registry,
            tools=self.tools,
            secret_key="test-key-views",
            test_config={"TESTING": True},
        )
        self.client = self.app.test_client()

    def tearDown(self):
        self.temp_dir.cleanup()

    def _login(self):
        with self.client.session_transaction() as sess:
            sess["user"] = "admin"
            sess["last_active"] = time.time()
            sess["csrf_token"] = "valid-token"

    def test_dashboard_view(self):
        self._login()
        res = self.client.get("/dashboard")
        self.assertEqual(res.status_code, 200)
        self.assertIn(b"System Dashboard", res.data)
        self.assertIn(b"Targets Online", res.data)

    def test_authenticated_layout_has_accessible_mobile_navigation(self):
        self._login()
        res = self.client.get("/dashboard")

        self.assertIn(b'aria-controls="primary-navigation"', res.data)
        self.assertIn(b'aria-expanded="false"', res.data)
        self.assertIn(b'id="primary-navigation"', res.data)
        self.assertIn(b'aria-current="page"', res.data)
        self.assertIn(b'id="primary-navigation" class="flex', res.data)

    def test_primary_navigation_marks_the_exact_current_section(self):
        self._login()
        for path in ("/dashboard", "/targets", "/projects", "/clients", "/activity", "/system", "/settings", "/maintenance"):
            with self.subTest(path=path):
                parser = CurrentNavigationParser()
                parser.feed(self.client.get(path).get_data(as_text=True))
                self.assertEqual(parser.current_links, [path])

    def test_data_tables_expose_responsive_scroll_regions(self):
        self._login()
        for path in ("/dashboard", "/targets", "/projects", "/clients", "/activity"):
            with self.subTest(path=path):
                res = self.client.get(path)
                self.assertEqual(res.status_code, 200)
                self.assertIn(b'role="region"', res.data)
                self.assertIn(b'tabindex="0"', res.data)

    def test_form_controls_have_programmatic_labels(self):
        self._login()
        for path in ("/targets/add", "/projects/add", "/clients/add", "/settings"):
            with self.subTest(path=path):
                parser = FormAccessibilityParser()
                parser.feed(self.client.get(path).get_data(as_text=True))
                self.assertTrue(parser.control_ids)
                self.assertNotIn(None, parser.control_ids)
                self.assertTrue(set(parser.control_ids).issubset(parser.label_targets))

    def test_targets_crud(self):
        self._login()
        # 1. List targets
        res = self.client.get("/targets")
        self.assertEqual(res.status_code, 200)
        self.assertIn(b"t1", res.data)

        # 2. Add target form
        res_form = self.client.get("/targets/add")
        self.assertEqual(res_form.status_code, 200)

        # 3. Add target POST
        res_add = self.client.post(
            "/targets/add",
            data={
                "id": "t2",
                "display_name": "Target 2",
                "platform": "linux",
                "host": "192.168.1.51",
                "port": "22",
                "user": "worker2",
                "enabled": "on",
                "csrf_token": "valid-token",
            },
            follow_redirects=True,
        )
        self.assertEqual(res_add.status_code, 200)
        self.assertIn(b"t2", res_add.data)

        # 4. Edit target POST
        res_edit = self.client.post(
            "/targets/t2/edit",
            data={
                "display_name": "Target 2 Updated",
                "platform": "linux",
                "host": "192.168.1.52",
                "port": "2222",
                "user": "worker2_new",
                "enabled": "on",
                "csrf_token": "valid-token",
            },
            follow_redirects=True,
        )
        self.assertEqual(res_edit.status_code, 200)
        t2 = self.registry.get_target("t2")
        self.assertEqual(t2["host"], "192.168.1.52")

        # 5. Toggle target
        res_toggle = self.client.post(
            "/targets/t2/toggle",
            data={"csrf_token": "valid-token"},
            follow_redirects=True,
        )
        self.assertEqual(res_toggle.status_code, 200)
        self.assertIn(b"DISABLED", res_toggle.data)

        # 6. Test target
        res_test = self.client.post(
            "/targets/t1/test",
            data={"csrf_token": "valid-token"},
            follow_redirects=True,
        )
        self.assertEqual(res_test.status_code, 200)
        self.assertIn(b"reachable", res_test.data)

    def test_edit_target_manages_ssh_host_trust_fail_closed(self):
        self._login()
        known_hosts = os.path.join(self.temp_dir.name, "known_hosts")
        discovery = TargetDiscovery(known_hosts_path=known_hosts)
        key_b64 = "AAAAC3NzaC1lZDI1NTE5AAAAIO558VBc3DlRhK/vRg5CPZV4kTD0DaY5GXoEEvjyCLmR"
        offered = parse_known_hosts_line(f"192.168.1.50 ssh-ed25519 {key_b64}")
        discovery.get_remote_host_keys = lambda host, port, timeout=2.0: [offered]
        self.tools.transport.discovery = discovery

        res = self.client.get("/targets/t1/edit")
        self.assertEqual(res.status_code, 200)
        self.assertIn(b"SSH Host Identity", res.data)
        self.assertIn(b"Not trusted", res.data)
        self.assertIn(offered["fingerprint"].encode(), res.data)

        bad = self.client.post(
            "/targets/t1/ssh/trust",
            data={"csrf_token": "valid-token", "fingerprint": "SHA256:wrong"},
            follow_redirects=True,
        )
        self.assertIn(b"SSH_IDENTITY_CHANGED", bad.data)
        self.assertEqual(discovery.get_canonical_keys("t1"), [])

        trusted = self.client.post(
            "/targets/t1/ssh/trust",
            data={"csrf_token": "valid-token", "fingerprint": offered["fingerprint"]},
            follow_redirects=True,
        )
        self.assertEqual(trusted.status_code, 200)
        self.assertIn(b"Trusted", trusted.data)
        self.assertEqual(discovery.get_canonical_keys("t1")[0]["fingerprint"], offered["fingerprint"])

        removed = self.client.post(
            "/targets/t1/ssh/untrust",
            data={"csrf_token": "valid-token"},
            follow_redirects=True,
        )
        self.assertEqual(removed.status_code, 200)
        self.assertIn(b"Not trusted", removed.data)
        self.assertEqual(discovery.get_canonical_keys("t1"), [])

    def test_projects_crud(self):
        self._login()
        # 1. List projects
        res = self.client.get("/projects")
        self.assertEqual(res.status_code, 200)
        self.assertIn(b"p1", res.data)

        # 2. Add project
        res_add = self.client.post(
            "/projects/add",
            data={
                "target_id": "t1",
                "id": "p2",
                "display_name": "Project 2",
                "root": "/srv/p2",
                "read": "on",
                "enabled": "on",
                "csrf_token": "valid-token",
            },
            follow_redirects=True,
        )
        self.assertEqual(res_add.status_code, 200)
        p2 = self.registry.get_project("t1", "p2")
        self.assertEqual(p2["root"], "/srv/p2")

        # 3. Toggle project enabled -> disabled
        res_toggle = self.client.post(
            "/projects/t1/p2/toggle",
            data={"csrf_token": "valid-token"},
            follow_redirects=True,
        )
        self.assertEqual(res_toggle.status_code, 200)
        self.assertIn(b"disabled", res_toggle.data)

        # Re-enable p2
        self.client.post(
            "/projects/t1/p2/toggle",
            data={"csrf_token": "valid-token"},
            follow_redirects=True,
        )

        # 4. Toggle project write capability
        self.assertFalse(self.registry.get_project("t1", "p2").get("write", False))
        res_toggle_write = self.client.post(
            "/projects/t1/p2/toggle-write",
            data={"csrf_token": "valid-token"},
            follow_redirects=True,
        )
        self.assertEqual(res_toggle_write.status_code, 200)
        self.assertTrue(self.registry.get_project("t1", "p2").get("write", False))
        self.assertIn(b"WRITE \xe2\x9c\x93", res_toggle_write.data)  # WRITE ✓

        # 5. Revoke write capability
        res_revoke = self.client.post(
            "/projects/t1/p2/toggle-write",
            data={"csrf_token": "valid-token"},
            follow_redirects=True,
        )
        self.assertEqual(res_revoke.status_code, 200)
        self.assertFalse(self.registry.get_project("t1", "p2").get("write", False))

    def test_clients_crud(self):
        self._login()
        # 1. Add AI Client
        res_add = self.client.post(
            "/clients/add",
            data={
                "id": "gemini-client",
                "display_name": "Gemini Assistant",
                "provider": "Google",
                "protocol": "mcp",
                "enabled": "on",
                "notes": "Primary agent client",
                "csrf_token": "valid-token",
            },
            follow_redirects=True,
        )
        self.assertEqual(res_add.status_code, 200)
        client = self.registry.get_client("gemini-client")
        self.assertEqual(client["display_name"], "Gemini Assistant")

        # 2. Toggle AI Client
        res_toggle = self.client.post(
            "/clients/gemini-client/toggle",
            data={"csrf_token": "valid-token"},
            follow_redirects=True,
        )
        self.assertEqual(res_toggle.status_code, 200)
        client_after = self.registry.get_client("gemini-client")
        self.assertFalse(client_after["enabled"])

    def test_client_grants_crud_and_effective_access(self):
        self._login()
        self.registry.add_client({
            "id": "grant-client",
            "display_name": "Grant Client",
            "provider": "test",
            "protocol": "mcp",
            "enabled": True,
            "notes": "",
        })

        # Grant management page exists and is reachable from the real client scope.
        res_page = self.client.get("/clients/grant-client/grants")
        self.assertEqual(res_page.status_code, 200)
        self.assertIn(b"Client Grants: grant-client", res_page.data)
        self.assertIn(b"Check Effective Access", res_page.data)

        # Add a read-only grant.
        res_add = self.client.post(
            "/clients/grant-client/grants/add",
            data={
                "target_id": "t1",
                "project_id": "p1",
                "capability": "read",
                "enabled": "on",
                "csrf_token": "valid-token",
            },
            follow_redirects=True,
        )
        self.assertEqual(res_add.status_code, 200)
        grants = self.registry.list_grants(client_id="grant-client")
        self.assertEqual(len(grants), 1)
        grant_id = grants[0]["id"]
        self.assertEqual(grants[0]["capability"], "read")

        # Real Policy Engine says read_file is allowed, run_command is not.
        res_check_read = self.client.post(
            "/clients/grant-client/grants/check",
            data={
                "target_id": "t1",
                "project_id": "p1",
                "tool_name": "read_file",
                "csrf_token": "valid-token",
            },
        )
        self.assertEqual(res_check_read.status_code, 200)
        self.assertIn(b"ALLOWED", res_check_read.data)

        res_check_shell = self.client.post(
            "/clients/grant-client/grants/check",
            data={
                "target_id": "t1",
                "project_id": "p1",
                "tool_name": "run_command",
                "csrf_token": "valid-token",
            },
        )
        self.assertEqual(res_check_shell.status_code, 200)
        self.assertIn(b"DENIED", res_check_shell.data)
        self.assertIn(b"TOOL_NOT_ALLOWED", res_check_shell.data)

        # Edit the same grant to target_shell, then Policy Engine allows run_command.
        res_edit = self.client.post(
            f"/clients/grant-client/grants/{grant_id}/edit",
            data={
                "target_id": "t1",
                "project_id": "p1",
                "capability": "target_shell",
                "enabled": "on",
                "csrf_token": "valid-token",
            },
            follow_redirects=True,
        )
        self.assertEqual(res_edit.status_code, 200)
        self.assertEqual(self.registry.get_grant(grant_id)["capability"], "target_shell")
        self.registry.set_setting("shell_enabled", "true")

        res_check_shell = self.client.post(
            "/clients/grant-client/grants/check",
            data={
                "target_id": "t1",
                "project_id": "p1",
                "tool_name": "run_command",
                "csrf_token": "valid-token",
            },
        )
        self.assertEqual(res_check_shell.status_code, 200)
        self.assertIn(b"ALLOWED", res_check_shell.data)

        # Disable/enable is reversible and immediately changes effective policy.
        res_toggle = self.client.post(
            f"/clients/grant-client/grants/{grant_id}/toggle",
            data={"csrf_token": "valid-token"},
            follow_redirects=True,
        )
        self.assertEqual(res_toggle.status_code, 200)
        self.assertFalse(self.registry.get_grant(grant_id)["enabled"])

        res_check_disabled = self.client.post(
            "/clients/grant-client/grants/check",
            data={
                "target_id": "t1",
                "project_id": "p1",
                "tool_name": "run_command",
                "csrf_token": "valid-token",
            },
        )
        self.assertIn(b"DENIED", res_check_disabled.data)

        self.client.post(
            f"/clients/grant-client/grants/{grant_id}/toggle",
            data={"csrf_token": "valid-token"},
        )
        self.assertTrue(self.registry.get_grant(grant_id)["enabled"])

        # Delete removes the grant rather than silently disabling it.
        res_delete = self.client.post(
            f"/clients/grant-client/grants/{grant_id}/delete",
            data={"csrf_token": "valid-token"},
            follow_redirects=True,
        )
        self.assertEqual(res_delete.status_code, 200)
        self.assertEqual(self.registry.list_grants(client_id="grant-client"), [])

    def test_client_grant_validation_and_scope_ownership(self):
        self._login()
        for client_id in ("client-a", "client-b"):
            self.registry.add_client({
                "id": client_id,
                "display_name": client_id,
                "provider": "test",
                "protocol": "mcp",
                "enabled": True,
                "notes": "",
            })

        # Wildcard Target + named Project is ambiguous and rejected.
        res_invalid_scope = self.client.post(
            "/clients/client-a/grants/add",
            data={
                "target_id": "*",
                "project_id": "p1",
                "capability": "read",
                "enabled": "on",
                "csrf_token": "valid-token",
            },
            follow_redirects=True,
        )
        self.assertEqual(res_invalid_scope.status_code, 200)
        self.assertIn(b"Project must be", res_invalid_scope.data)
        self.assertEqual(self.registry.list_grants(client_id="client-a"), [])

        # Fully global */*/* requires explicit acknowledgement.
        res_global_denied = self.client.post(
            "/clients/client-a/grants/add",
            data={
                "target_id": "*",
                "project_id": "*",
                "capability": "*",
                "enabled": "on",
                "csrf_token": "valid-token",
            },
            follow_redirects=True,
        )
        self.assertIn(b"requires explicit confirmation", res_global_denied.data)
        self.assertEqual(self.registry.list_grants(client_id="client-a"), [])

        self.client.post(
            "/clients/client-a/grants/add",
            data={
                "target_id": "*",
                "project_id": "*",
                "capability": "*",
                "enabled": "on",
                "confirm_global": "on",
                "csrf_token": "valid-token",
            },
        )
        grant_id = self.registry.list_grants(client_id="client-a")[0]["id"]

        # A grant cannot be manipulated through another client's URL.
        res_cross_client = self.client.post(
            f"/clients/client-b/grants/{grant_id}/toggle",
            data={"csrf_token": "valid-token"},
        )
        self.assertEqual(res_cross_client.status_code, 404)
        self.assertTrue(self.registry.get_grant(grant_id)["enabled"])

    def test_activity_view(self):
        self._login()
        self.registry.record_activity({"action": "sample_tool_run", "target_id": "t1", "success": True})
        res = self.client.get("/activity")
        self.assertEqual(res.status_code, 200)
        self.assertIn(b"sample_tool_run", res.data)

    def test_system_view(self):
        self._login()
        res = self.client.get("/system")
        self.assertEqual(res.status_code, 200)
        self.assertIn(b"Gateway Runtime", res.data)
        self.assertIn(b"SQLiteRegistry", res.data)

    def test_settings_view_and_update(self):
        self._login()
        res = self.client.get("/settings")
        self.assertEqual(res.status_code, 200)
        self.assertIn(b"Emergency Kill Switch", res.data)

        # Update valid settings
        res_post = self.client.post(
            "/settings",
            data={
                "default_timeout": "45",
                "max_output_bytes": "524288",
                "max_file_read_bytes": "2097152",
                "activity_retention": "2000",
                "csrf_token": "valid-token",
            },
            follow_redirects=True,
        )
        self.assertEqual(res_post.status_code, 200)
        self.assertEqual(self.registry.get_setting("default_timeout"), "45")

        # Range validation rejection
        res_invalid = self.client.post(
            "/settings",
            data={
                "default_timeout": "9999",  # exceeds max 300
                "max_output_bytes": "524288",
                "max_file_read_bytes": "2097152",
                "activity_retention": "2000",
                "csrf_token": "valid-token",
            },
            follow_redirects=True,
        )
        self.assertEqual(res_invalid.status_code, 200)
        self.assertIn(b"Timeout must be between", res_invalid.data)

        # Toggle writes switch (enable)
        self.assertEqual(self.registry.get_setting("writes_enabled"), "false")
        res_toggle_writes = self.client.post(
            "/settings/toggle-writes",
            data={"csrf_token": "valid-token"},
            follow_redirects=True,
        )
        self.assertEqual(res_toggle_writes.status_code, 200)
        self.assertEqual(self.registry.get_setting("writes_enabled"), "true")

        # Disable writes panic button
        res_panic = self.client.post(
            "/settings/disable-writes",
            data={"csrf_token": "valid-token"},
            follow_redirects=True,
        )
        self.assertEqual(res_panic.status_code, 200)
        self.assertEqual(self.registry.get_setting("writes_enabled"), "false")
        self.assertIn(b"Structured filesystem writes are DISABLED", res_panic.data)

    def test_target_shell_kill_switch(self):
        self._login()
        self.assertEqual(self.registry.get_setting("shell_enabled", "false"), "false")
        res = self.client.get("/settings")
        self.assertEqual(res.status_code, 200)
        self.assertIn(b"TARGET_SHELL_DISABLED", res.data)

        res = self.client.post(
            "/settings/toggle-shell",
            data={"csrf_token": "valid-token"},
            follow_redirects=False,
        )
        self.assertEqual(res.status_code, 302)
        self.assertEqual(self.registry.get_setting("shell_enabled"), "true")


    def test_maintenance_views(self):
        self._login()
        # 1. Maintenance dashboard GET
        res = self.client.get("/maintenance")
        self.assertEqual(res.status_code, 200)
        self.assertIn(b"Maintenance & Diagnostics", res.data)
        self.assertIn(b"Doctor Health Check", res.data)
        self.assertIn(b"Contract Versioning", res.data)

        # 2. Diagnostics refresh POST
        res_doc = self.client.post("/maintenance/doctor", data={"csrf_token": "valid-token"}, follow_redirects=True)
        self.assertEqual(res_doc.status_code, 200)
        self.assertIn(b"Diagnostics refreshed", res_doc.data)

        # 3. Safe repair POST
        res_repair = self.client.post("/maintenance/repair", data={"csrf_token": "valid-token"}, follow_redirects=True)
        self.assertEqual(res_repair.status_code, 200)

        # 4. Backup POST
        res_backup = self.client.post("/maintenance/backup", data={"csrf_token": "valid-token"}, follow_redirects=True)
        self.assertEqual(res_backup.status_code, 200)


if __name__ == "__main__":
    unittest.main()
