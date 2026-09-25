import os
import sys
import unittest

sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..")))
sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "src")))

try:
    from mcp_gateway.policy import (
        PolicyError,
        check_capability,
        validate_canonical_path,
        validate_relative_path,
        validate_task,
    )
except ImportError:
    from src.mcp_gateway.policy import (
        PolicyError,
        check_capability,
        validate_canonical_path,
        validate_relative_path,
        validate_task,
    )


class TestPolicy(unittest.TestCase):

    def test_valid_relative_paths(self):
        self.assertEqual(validate_relative_path("."), ".")
        self.assertEqual(validate_relative_path("file.txt"), "file.txt")
        self.assertEqual(validate_relative_path("src/main.py"), "src/main.py")
        self.assertEqual(validate_relative_path("src/sub/deep.txt"), "src/sub/deep.txt")

    def test_absolute_paths_rejected(self):
        for bad_path in ["/etc/passwd", "/root", "\\Windows\\System32", "C:\\boot.ini", "~/keys"]:
            with self.subTest(bad_path=bad_path):
                with self.assertRaises(PolicyError) as ctx:
                    validate_relative_path(bad_path)
                self.assertEqual(ctx.exception.code, "INVALID_PATH")

    def test_traversal_paths_rejected(self):
        for bad_path in ["..", "../", "../foo", "foo/..", "foo/../../bar", "a/../b/../../c"]:
            with self.subTest(bad_path=bad_path):
                with self.assertRaises(PolicyError) as ctx:
                    validate_relative_path(bad_path)
                self.assertEqual(ctx.exception.code, "INVALID_PATH")

    def test_empty_and_nul_paths_rejected(self):
        for bad_path in ["", "   ", "foo\0bar"]:
            with self.subTest(bad_path=bad_path):
                with self.assertRaises(PolicyError) as ctx:
                    validate_relative_path(bad_path)
                self.assertEqual(ctx.exception.code, "INVALID_PATH")

    def test_canonical_root_match(self):
        root = "/data/projects/myproj"
        # Exact root
        validate_canonical_path("/data/projects/myproj", root)
        # Inside root
        validate_canonical_path("/data/projects/myproj/src/main.py", root)

    def test_sibling_prefix_escape_rejected(self):
        root = "/data/projects/myproj"
        evil_sibling = "/data/projects/myproj-evil/hack.txt"
        with self.assertRaises(PolicyError) as ctx:
            validate_canonical_path(evil_sibling, root)
        self.assertEqual(ctx.exception.code, "PATH_OUTSIDE_ALLOWED_ROOT")

    def test_canonical_traversal_escape_rejected(self):
        root = "/data/projects/myproj"
        outside_path = "/data/projects/other/secret.txt"
        with self.assertRaises(PolicyError) as ctx:
            validate_canonical_path(outside_path, root)
        self.assertEqual(ctx.exception.code, "PATH_OUTSIDE_ALLOWED_ROOT")

    def test_check_capability_read(self):
        proj_ok = {"read": True, "write": False}
        proj_no = {"read": False, "write": False}
        check_capability(proj_ok, "read")
        with self.assertRaises(PolicyError) as ctx:
            check_capability(proj_no, "read")
        self.assertEqual(ctx.exception.code, "TOOL_NOT_ALLOWED")

    def test_check_capability_write(self):
        proj_ok = {"read": True, "write": True}
        proj_no = {"read": True, "write": False}
        check_capability(proj_ok, "write")
        with self.assertRaises(PolicyError) as ctx:
            check_capability(proj_no, "write")
        self.assertEqual(ctx.exception.code, "WRITE_NOT_ALLOWED")

    def test_validate_write_relative_path(self):
        from mcp_gateway.policy import validate_write_relative_path
        self.assertEqual(validate_write_relative_path("foo.txt"), "foo.txt")
        self.assertEqual(validate_write_relative_path("dir/foo.txt"), "dir/foo.txt")
        with self.assertRaises(PolicyError) as ctx:
            validate_write_relative_path(".")
        self.assertEqual(ctx.exception.code, "INVALID_PATH")

    def test_validate_content_utf8(self):
        from mcp_gateway.policy import validate_content_utf8
        self.assertEqual(validate_content_utf8("hello world"), b"hello world")
        with self.assertRaises(PolicyError) as ctx:
            validate_content_utf8("bad\0nul")
        self.assertEqual(ctx.exception.code, "INVALID_ENCODING")

    def test_validate_write_size(self):
        from mcp_gateway.policy import validate_write_size
        validate_write_size(b"small", 100)
        with self.assertRaises(PolicyError) as ctx:
            validate_write_size(b"toolargecontent", 5)
        self.assertEqual(ctx.exception.code, "FILE_TOO_LARGE")

    def test_validate_task_allowlisted(self):
        proj = {
            "tasks": {
                "git_status": {
                    "enabled": True,
                    "argv": ["git", "status", "--short"],
                    "timeout": 20,
                }
            }
        }
        res = validate_task(proj, "git_status")
        self.assertEqual(res["argv"], ["git", "status", "--short"])
        self.assertEqual(res["timeout"], 20)

    def test_validate_task_arbitrary_rejected(self):
        proj = {"tasks": {}}
        with self.assertRaises(PolicyError) as ctx:
            validate_task(proj, "rm -rf /")
        self.assertEqual(ctx.exception.code, "TASK_NOT_ALLOWED")

    def test_authorize_client_rules(self):
        from unittest.mock import MagicMock
        from mcp_gateway.policy import authorize_client

        # 1. Anonymous denied
        ok, err = authorize_client(None, "t1", "p1", "health")
        self.assertFalse(ok)
        self.assertIn("ANONYMOUS_CLIENT_DENIED", err)

        ok, err = authorize_client("NONE", "t1", "p1", "health")
        self.assertFalse(ok)
        self.assertIn("ANONYMOUS_CLIENT_DENIED", err)

        # 2. Unknown tool
        ok, err = authorize_client("c1", "t1", "p1", "unknown_tool_xyz")
        self.assertFalse(ok)
        self.assertIn("TOOL_NOT_RECOGNIZED", err)

        # 3. Client disabled / not found
        reg = MagicMock()
        reg.get_client.side_effect = KeyError("Not found")
        ok, err = authorize_client("c_missing", "t1", "p1", "health", registry=reg)
        self.assertFalse(ok)
        self.assertIn("CLIENT_NOT_FOUND", err)

        reg.get_client.side_effect = None
        reg.get_client.return_value = {"id": "c_disabled", "enabled": False}
        ok, err = authorize_client("c_disabled", "t1", "p1", "health", registry=reg)
        self.assertFalse(ok)
        self.assertIn("CLIENT_DISABLED", err)

        # 4. Client enabled with grants
        reg.get_client.return_value = {"id": "c1", "enabled": True}
        reg.get_setting.return_value = "true"
        reg.get_target.return_value = {"id": "t1", "enabled": True}
        reg.get_project.return_value = {"id": "p1", "enabled": True, "read": True, "write": False}
        reg.get_client_grants.return_value = [
            {"client_id": "c1", "target_id": "t1", "project_id": "p1", "capability": "read", "enabled": True}
        ]

        # Read tool allowed
        ok, err = authorize_client("c1", "t1", "p1", "read_file", registry=reg)
        self.assertTrue(ok)

        # Write tool denied (grant is only read): MUST be TOOL_NOT_ALLOWED
        ok, err = authorize_client("c1", "t1", "p1", "write_file", registry=reg)
        self.assertFalse(ok)
        self.assertIn("TOOL_NOT_ALLOWED", err)

        # Precedence check: Even when global writes are disabled, client without grant gets TOOL_NOT_ALLOWED (no state leak)
        reg.get_setting.return_value = "false"
        ok, err = authorize_client("c1", "t1", "p1", "write_file", registry=reg)
        self.assertFalse(ok)
        self.assertIn("TOOL_NOT_ALLOWED", err)
        reg.get_setting.return_value = "true"

        # Grant write capability, but project write is False
        reg.get_client_grants.return_value = [
            {"client_id": "c1", "target_id": "t1", "project_id": "p1", "capability": "read,write", "enabled": True}
        ]
        ok, err = authorize_client("c1", "t1", "p1", "write_file", registry=reg)
        self.assertFalse(ok)
        self.assertIn("WRITE_NOT_ALLOWED", err)

        # Enable project write -> write tool allowed
        reg.get_project.return_value = {"id": "p1", "enabled": True, "read": True, "write": True}
        ok, err = authorize_client("c1", "t1", "p1", "write_file", registry=reg)
        self.assertTrue(ok)

        # Target disabled -> denied
        reg.get_target.return_value = {"id": "t1", "enabled": False}
        ok, err = authorize_client("c1", "t1", "p1", "read_file", registry=reg)
        self.assertFalse(ok)
        self.assertIn("TARGET_DISABLED", err)

    def test_run_command_and_extended_fs_capabilities(self):
        from unittest.mock import MagicMock
        from mcp_gateway.policy import authorize_client

        reg = MagicMock()
        reg.get_client.return_value = {"id": "c_all", "enabled": True}
        reg.get_setting.return_value = "true"
        reg.get_target.return_value = {"id": "t1", "enabled": True}
        reg.get_project.return_value = {"id": "p1", "enabled": True, "read": True, "write": True}
        reg.get_client_grants.return_value = [
            {"client_id": "c_all", "target_id": "t1", "project_id": "p1", "capability": "*", "enabled": True}
        ]

        for tool in ["run_command", "append_file", "delete_file", "copy_file", "move_file", "mkdir", "search"]:
            ok, err = authorize_client("c_all", "t1", "p1", tool, registry=reg)
            self.assertTrue(ok, f"{tool} should be authorized for c_all with *: err={err}")

        # c_ro with only read capability
        reg.get_client_grants.return_value = [
            {"client_id": "c_ro", "target_id": "t1", "project_id": "p1", "capability": "read", "enabled": True}
        ]
        ok, err = authorize_client("c_ro", "t1", "p1", "run_command", registry=reg)
        self.assertFalse(ok)
        self.assertIn("TOOL_NOT_ALLOWED", err)


    def test_target_shell_capability_and_kill_switch(self):
        from unittest.mock import MagicMock
        from mcp_gateway.policy import authorize_client
        reg = MagicMock()
        reg.get_client.return_value = {"id": "c1", "enabled": True}
        reg.get_target.return_value = {"id": "t1", "enabled": True}
        reg.get_project.return_value = {"id": "p1", "enabled": True, "read": True, "write": True}
        reg.get_client_grants.return_value = [
            {"client_id": "c1", "target_id": "t1", "project_id": "p1", "capability": "target_shell", "enabled": True}
        ]
        reg.get_setting.side_effect = lambda key, default=None: {"gateway_enabled": "true", "shell_enabled": "true"}.get(key, default)
        ok, err = authorize_client("c1", "t1", "p1", "run_command", registry=reg)
        self.assertTrue(ok, err)
        reg.get_setting.side_effect = lambda key, default=None: {"gateway_enabled": "true", "shell_enabled": "false"}.get(key, default)
        ok, err = authorize_client("c1", "t1", "p1", "run_command", registry=reg)
        self.assertFalse(ok)
        self.assertIn("TARGET_SHELL_DISABLED", err)

    def test_grant_summary_keeps_target_admin_explicit(self):
        from mcp_gateway.policy import summarize_grant_capabilities

        wildcard = summarize_grant_capabilities([
            {"capability": "*", "enabled": True}
        ])
        self.assertTrue(wildcard["all"])
        self.assertFalse(wildcard["target_admin"])

        explicit = summarize_grant_capabilities([
            {"capability": "target_admin", "enabled": True}
        ])
        self.assertTrue(explicit["target_admin"])
        self.assertFalse(explicit["gateway_admin"])

    def test_validate_task_rejects_invalid_argv_and_timeout(self):
        from mcp_gateway.policy import validate_task
        for task in [
            {"enabled": True, "argv": [], "timeout": 30},
            {"enabled": True, "argv": ["echo", ""], "timeout": 30},
            {"enabled": True, "argv": ["echo"], "timeout": 0},
            {"enabled": True, "argv": ["echo"], "timeout": 3601},
        ]:
            with self.assertRaises(PolicyError) as ctx:
                validate_task({"tasks": {"bad": task}}, "bad")
            self.assertEqual(ctx.exception.code, "TASK_INVALID")



    def test_privilege_policy_validation_and_explicit_target_admin_grant(self):
        from unittest.mock import MagicMock
        from mcp_gateway.policy import (
            authorize_privilege_request,
            normalize_privilege_policy,
            normalize_privilege_request,
        )

        self.assertEqual(normalize_privilege_policy("always_allow"), "always_allow")
        self.assertEqual(normalize_privilege_request(None), "standard")

        with self.assertRaises(PolicyError) as ctx:
            normalize_privilege_policy("magic")
        self.assertEqual(ctx.exception.code, "INVALID_PRIVILEGE_POLICY")

        with self.assertRaises(PolicyError) as ctx:
            normalize_privilege_request("auto")
        self.assertEqual(ctx.exception.code, "INVALID_PRIVILEGE_REQUEST")

        reg = MagicMock()
        reg.get_client_grants.return_value = []
        ok, err = authorize_privilege_request("local", "t1", "p1", reg)
        self.assertFalse(ok)
        self.assertIn("PRIVILEGE_GRANT_REQUIRED", err)

        reg.get_client_grants.return_value = [
            {
                "client_id": "c1",
                "target_id": "t1",
                "project_id": "p1",
                "capability": "*",
                "enabled": True,
            }
        ]
        ok, err = authorize_privilege_request("c1", "t1", "p1", reg)
        self.assertFalse(ok)
        self.assertIn("PRIVILEGE_GRANT_REQUIRED", err)

        # Gateway-appliance admin remains a separate capability.
        reg.get_client_grants.return_value = [
            {
                "client_id": "c1",
                "target_id": "t1",
                "project_id": "p1",
                "capability": "target_shell,admin",
                "enabled": True,
            }
        ]
        ok, err = authorize_privilege_request("c1", "t1", "p1", reg)
        self.assertFalse(ok)
        self.assertIn("PRIVILEGE_GRANT_REQUIRED", err)

        reg.get_client_grants.return_value = [
            {
                "client_id": "c1",
                "target_id": "t1",
                "project_id": "p1",
                "capability": "target_shell,target_admin",
                "enabled": True,
            }
        ]
        ok, err = authorize_privilege_request("c1", "t1", "p1", reg)
        self.assertTrue(ok)
        self.assertIsNone(err)



if __name__ == "__main__":
    unittest.main()


