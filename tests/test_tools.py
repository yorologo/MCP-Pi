import base64
import json
import os
import sys
import unittest
import zlib
from unittest.mock import MagicMock

sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..")))
sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "src")))

try:
    from mcp_gateway.config import GatewayConfig
    from mcp_gateway.registry import JsonRegistry
    from mcp_gateway.ssh_transport import SSHError, SSHTransportResult
    from mcp_gateway.tools import GatewayTools
except ImportError:
    from src.mcp_gateway.config import GatewayConfig
    from src.mcp_gateway.registry import JsonRegistry
    from src.mcp_gateway.ssh_transport import SSHError, SSHTransportResult
    from src.mcp_gateway.tools import GatewayTools


class DummyTransport:
    """Mock SSH transport for deterministic unit testing."""

    def __init__(self):
        self.cmd_responses = {}
        self.canonical_responses = {}
        self.file_responses = {}
        self.last_remote_cmd = None
        self.last_cwd = None
        self.last_env = None
        self.last_target_user = None

    def run_command(self, target, remote_cmd, timeout=None, cwd=None, **kwargs):
        self.last_remote_cmd = remote_cmd
        self.last_target_user = target.get("user")
        self.last_cwd = cwd
        self.last_env = kwargs.get("env")
        if "hostname" in remote_cmd:
            return SSHTransportResult(0, "mock-host\n", "", 15)
        if "git status" in remote_cmd:
            return SSHTransportResult(0, " M README.md\n", "", 25)
        if "argv_test" in remote_cmd:
            return SSHTransportResult(0, "task output\n", "", 30)

        # Model the lightweight effective-privilege probe used before ordinary
        # command/task execution. Other compressed helpers keep their existing
        # deterministic mock behavior.
        payload_marker = "b64decode('"
        if payload_marker in remote_cmd:
            try:
                payload = remote_cmd.split(payload_marker, 1)[1].split("')", 1)[0]
                helper = zlib.decompress(base64.b64decode(payload)).decode("utf-8")
            except Exception:
                helper = ""
            if (
                "identity = {'user': getpass.getuser()}" in helper
                and "'maximum_level':current_level" in helper
            ):
                facts = {
                    "probe_status": "ok",
                    "boot_id": None,
                    "effective_identity": {
                        "user": "tester",
                        "uid": 1000,
                        "euid": 1000,
                    },
                    "privilege": {
                        "current_level": "standard",
                        "maximum_level": "standard",
                        "backend": None,
                        "backend_ready": False,
                        "backend_reason": None,
                        "transport_already_elevated": False,
                    },
                }
                return SSHTransportResult(
                    0,
                    json.dumps(facts, separators=(",", ":")) + "\n",
                    "",
                    5,
                )
        return SSHTransportResult(0, "", "", 10)

    def resolve_canonical_path(self, target, candidate_path, timeout=10):
        if "evil" in candidate_path or "escape" in candidate_path:
            return "/outside/path/evil.txt"
        return candidate_path

    def read_remote_file_content(self, target, canonical_path, max_bytes=None):
        return "MOCK FILE CONTENT"

    def probe_remote_path(self, target, candidate_path, timeout=10):
        import hashlib
        if "symlink" in candidate_path:
            return {
                "exists": True, "is_symlink": True, "is_file": False, "is_dir": False,
                "canonical_path": candidate_path, "parent_exists": True, "parent_is_symlink": False,
                "parent_canonical_path": os.path.dirname(candidate_path), "sha256": "", "size": 0, "content": ""
            }
        if "existing.txt" in candidate_path:
            content = "hello existing\n"
            sha = hashlib.sha256(content.encode()).hexdigest()
            return {
                "exists": True, "is_symlink": False, "is_file": True, "is_dir": False,
                "canonical_path": candidate_path, "parent_exists": True, "parent_is_symlink": False,
                "parent_canonical_path": os.path.dirname(candidate_path), "sha256": sha, "size": len(content), "content": content
            }
        return {
            "exists": False, "is_symlink": False, "is_file": False, "is_dir": False,
            "canonical_path": "", "parent_exists": True, "parent_is_symlink": False,
            "parent_canonical_path": os.path.dirname(candidate_path), "sha256": "", "size": 0, "content": ""
        }

    def resolve_safe_destination(self, target, project_root, candidate_path, allow_missing_parents=False, timeout=10):
        if "nested_symlink" in candidate_path or "escape_parent" in candidate_path:
            raise SSHError("Destination parent resolves outside project root", code="PATH_OUTSIDE_ALLOWED_ROOT")
        if "dest_symlink" in candidate_path or "symlink_dest" in candidate_path:
            raise SSHError("Destination is a symlink", code="SYMLINK_WRITE_DENIED")
        return {
            "ok": True,
            "root_canonical": project_root,
            "parent_canonical": os.path.dirname(candidate_path),
            "destination_path": candidate_path,
            "exists": False,
            "canonical_path": "",
        }

    def write_remote_file_atomic(self, target, dest_path, content_bytes, create, expected_sha256=None, backup_dir=None, max_write_bytes=262144, timeout=20):
        import hashlib
        sha = hashlib.sha256(content_bytes).hexdigest()
        return {
            "ok": True,
            "created": create,
            "old_sha256": expected_sha256,
            "new_sha256": sha,
            "bytes_written": len(content_bytes),
            "atomic": True
        }



class TestGatewayTools(unittest.TestCase):

    def setUp(self):
        self.test_data = {
            "targets": {
                "mock-target": {
                    "platform": "linux",
                    "host": "127.0.0.1",
                    "port": 22,
                    "user": "tester",
                    "ssh_alias": "mock-alias",
                    "enabled": True,
                    "projects": {
                        "mock-proj": {
                            "root": "/home/tester/proj",
                            "read": True,
                            "write": False,
                            "tasks": {
                                "status": {
                                    "enabled": True,
                                    "argv": ["echo", "argv_test"],
                                    "timeout": 15,
                                }
                            }
                        }
                    }
                }
            }
        }
        self.config = GatewayConfig(raw_data=self.test_data)
        self.config.record_activity = lambda event: None
        self.transport = DummyTransport()
        self.tools = GatewayTools(config=self.config, transport=self.transport)

    def _privileged_tools(self, client_id="c1"):
        registry = JsonRegistry(self.config)
        registry.set_setting("gateway_enabled", "true")
        registry.set_setting("shell_enabled", "true")
        registry.add_client({"id": client_id, "enabled": True})
        registry.add_grant({
            "client_id": client_id,
            "target_id": "mock-target",
            "project_id": "mock-proj",
            "capability": "target_shell",
            "enabled": True,
        })
        registry.add_grant({
            "client_id": client_id,
            "target_id": "mock-target",
            "project_id": "mock-proj",
            "capability": "target_admin",
            "enabled": True,
        })
        return GatewayTools(
            registry=registry,
            transport=self.transport,
            client_id=client_id,
        )

    def test_health_structure(self):
        res = self.tools.health()
        self.assertTrue(res["ok"])
        self.assertEqual(res["tool"], "health")
        self.assertIn("gateway_status", res["result"])
        self.assertIn("python_version", res["result"])
        self.assertEqual(res["result"]["configured_targets"], 1)

    def test_target_facts_never_report_zero_ram_as_valid(self):
        original = self.transport.run_command

        def fake_probe(target, remote_cmd, timeout=None, cwd=None, **kwargs):
            return SSHTransportResult(
                0,
                '{"probe_status":"ok","os":"windows","arch":"AMD64","ram_mb":0,'
                '"boot_id":"windows:1","effective_identity":{"user":"tester"},'
                '"privilege":{"current_level":"standard","maximum_level":"standard",'
                '"backend":null,"backend_ready":false,"transport_already_elevated":false}}\n',
                "",
                5,
            )

        self.transport.run_command = fake_probe
        try:
            facts = self.tools._probe_target_facts(
                self.config.get_target("mock-target")
            )
        finally:
            self.transport.run_command = original

        self.assertEqual(facts["probe_status"], "ok")
        self.assertIsNone(facts["ram_mb"])
        self.assertEqual(facts["boot_id"], "windows:1")

    def test_list_targets_structure(self):
        res = self.tools.list_targets()
        self.assertTrue(res["ok"])
        self.assertEqual(res["tool"], "list_targets")
        targets = res["result"]["targets"]
        self.assertEqual(len(targets), 1)
        self.assertEqual(targets[0]["id"], "mock-target")

    def test_target_status(self):
        res = self.tools.target_status("mock-target")
        self.assertTrue(res["ok"])
        self.assertEqual(res["tool"], "target_status")
        self.assertTrue(res["result"]["reachable"])
        self.assertEqual(res["result"]["remote_hostname"], "mock-host")

    def test_unknown_target_returns_error(self):
        res = self.tools.target_status("nonexistent")
        self.assertFalse(res["ok"])
        self.assertEqual(res["error"]["code"], "UNKNOWN_TARGET")

    def test_unknown_project_returns_error(self):
        res = self.tools.read_file("mock-target", "nonexistent-proj", "test.txt")
        self.assertFalse(res["ok"])
        self.assertEqual(res["error"]["code"], "UNKNOWN_PROJECT")

    def test_parent_traversal_rejected(self):
        res = self.tools.read_file("mock-target", "mock-proj", "../etc/passwd")
        self.assertFalse(res["ok"])
        self.assertEqual(res["error"]["code"], "INVALID_PATH")

    def test_absolute_path_rejected(self):
        res = self.tools.read_file("mock-target", "mock-proj", "/etc/passwd")
        self.assertFalse(res["ok"])
        self.assertEqual(res["error"]["code"], "INVALID_PATH")

    def test_symlink_escape_rejected(self):
        res = self.tools.read_file("mock-target", "mock-proj", "evil-link.txt")
        self.assertFalse(res["ok"])
        self.assertEqual(res["error"]["code"], "PATH_OUTSIDE_ALLOWED_ROOT")

    def test_git_status_allowed(self):
        res = self.tools.git_status("mock-target", "mock-proj")
        self.assertTrue(res["ok"])
        self.assertIn("status_output", res["result"])

    def test_run_task_allowed(self):
        res = self.tools.run_task("mock-target", "mock-proj", "status")
        self.assertTrue(res["ok"])
        self.assertEqual(res["result"]["task"], "status")
        self.assertEqual(res["result"]["exit_code"], 0)

    def test_arbitrary_task_denied(self):
        res = self.tools.run_task("mock-target", "mock-proj", "arbitrary_cmd")
        self.assertFalse(res["ok"])
        self.assertEqual(res["error"]["code"], "TASK_NOT_ALLOWED")


    def test_read_file_includes_sha256(self):
        res = self.tools.read_file("mock-target", "mock-proj", "hello.txt")
        self.assertTrue(res["ok"])
        self.assertIn("sha256", res["result"])
        self.assertEqual(len(res["result"]["sha256"]), 64)

    def test_write_file_writes_globally_disabled(self):
        # By default, writes_enabled is false
        res = self.tools.write_file("mock-target", "mock-proj", "new.txt", "hello", create=True)
        self.assertFalse(res["ok"])
        self.assertEqual(res["error"]["code"], "WRITES_DISABLED")

    def test_write_file_project_write_disabled(self):
        # Mock writes_enabled
        self.tools._is_writes_enabled = lambda: True

        # Project mock-proj has write: False
        res = self.tools.write_file("mock-target", "mock-proj", "new.txt", "hello", create=True)
        self.assertFalse(res["ok"])
        self.assertEqual(res["error"]["code"], "WRITE_NOT_ALLOWED")

    def test_write_file_dry_run_and_create(self):
        self.tools._is_writes_enabled = lambda: True
        self.config._data["targets"]["mock-target"]["projects"]["mock-proj"]["write"] = True

        # Dry run on new file
        res = self.tools.write_file("mock-target", "mock-proj", "new.txt", "hello new\n", dry_run=True, create=True)
        self.assertTrue(res["ok"])
        self.assertTrue(res["result"]["dry_run"])
        self.assertEqual(res["result"]["size_before"], 0)
        self.assertEqual(res["result"]["size_after"], 10)
        self.assertIn("diff_truncated", res["result"])

        # Real create on new file
        res = self.tools.write_file("mock-target", "mock-proj", "new.txt", "hello new\n", create=True)
        self.assertTrue(res["ok"])
        self.assertTrue(res["result"]["created"])
        self.assertEqual(res["result"]["bytes_written"], 10)

    def test_write_file_overwrite_with_sha(self):
        import hashlib
        self.tools._is_writes_enabled = lambda: True
        self.config._data["targets"]["mock-target"]["projects"]["mock-proj"]["write"] = True

        existing_sha = hashlib.sha256(b"hello existing\n").hexdigest()

        # Overwrite with wrong sha
        res = self.tools.write_file("mock-target", "mock-proj", "existing.txt", "new data", expected_sha256="wronghash")
        self.assertFalse(res["ok"])
        self.assertEqual(res["error"]["code"], "WRITE_CONFLICT")

        # Overwrite without sha
        res = self.tools.write_file("mock-target", "mock-proj", "existing.txt", "new data")
        self.assertFalse(res["ok"])
        self.assertEqual(res["error"]["code"], "WRITE_CONFLICT")

        # Overwrite with correct sha
        res = self.tools.write_file("mock-target", "mock-proj", "existing.txt", "new data", expected_sha256=existing_sha)
        self.assertTrue(res["ok"])
        self.assertFalse(res["result"]["created"])

    def test_write_file_negative_cases(self):
        self.tools._is_writes_enabled = lambda: True
        self.config._data["targets"]["mock-target"]["projects"]["mock-proj"]["write"] = True

        # Traversal
        res = self.tools.write_file("mock-target", "mock-proj", "../etc/passwd", "evil")
        self.assertEqual(res["error"]["code"], "INVALID_PATH")

        # Absolute path
        res = self.tools.write_file("mock-target", "mock-proj", "/etc/passwd", "evil")
        self.assertEqual(res["error"]["code"], "INVALID_PATH")

        # Symlink target
        res = self.tools.write_file("mock-target", "mock-proj", "symlink_file.txt", "evil")
        self.assertEqual(res["error"]["code"], "SYMLINK_WRITE_DENIED")

        # NUL byte in content
        res = self.tools.write_file("mock-target", "mock-proj", "bad.txt", "bad\0content", create=True)
        self.assertEqual(res["error"]["code"], "INVALID_ENCODING")

    def test_run_command_and_extended_fs_tools(self):
        self.tools._is_writes_enabled = lambda: True
        self.tools._is_shell_enabled = lambda: True
        self.tools._get_setting = lambda key, default=None: (
            "true" if key in {"writes_enabled", "shell_enabled"} else default
        )
        self.config._data["targets"]["mock-target"]["projects"]["mock-proj"]["write"] = True

        # run_command default cwd
        res = self.tools.run_command("mock-target", "mock-proj", "echo hello")
        self.assertTrue(res["ok"], f"run_command failed: {res}")
        self.assertIn("stdout", res["result"])
        self.assertIn("effective_cwd", res["result"])
        self.assertEqual(res["result"]["effective_cwd"], "/home/tester/proj")

        # append_file
        res = self.tools.append_file("mock-target", "mock-proj", "existing.txt", "more text\n")
        self.assertTrue(res["ok"], f"append_file failed: {res}")
        self.assertEqual(res["result"]["bytes_appended"], 10)

        # mkdir
        res = self.tools.mkdir("mock-target", "mock-proj", "new_dir")
        self.assertTrue(res["ok"], f"mkdir failed: {res}")
        self.assertTrue(res["result"]["created"])

        # copy_file
        res = self.tools.copy_file("mock-target", "mock-proj", "existing.txt", "existing_copy.txt")
        self.assertTrue(res["ok"], f"copy_file failed: {res}")
        self.assertTrue(res["result"]["copied"])

        # move_file
        res = self.tools.move_file("mock-target", "mock-proj", "existing.txt", "existing_moved.txt")
        self.assertTrue(res["ok"], f"move_file failed: {res}")
        self.assertTrue(res["result"]["moved"])

        # delete_file
        res = self.tools.delete_file("mock-target", "mock-proj", "existing.txt")
        self.assertTrue(res["ok"], f"delete_file failed: {res}")
        self.assertTrue(res["result"]["deleted"])

        # search
        res = self.tools.search("mock-target", "mock-proj", "hello")
        self.assertTrue(res["ok"], f"search failed: {res}")
        self.assertIn("matches", res["result"])


    def test_run_task_on_already_elevated_transport_requires_target_admin(self):
        self.config._data["targets"]["mock-target"]["platform"] = "windows"
        self.config._data["targets"]["mock-target"]["privilege_policy"] = "always_allow"
        registry = JsonRegistry(self.config)
        registry.set_setting("gateway_enabled", "true")
        registry.add_client({"id": "c1", "enabled": True})
        registry.add_grant({
            "client_id": "c1",
            "target_id": "mock-target",
            "project_id": "mock-proj",
            "capability": "execute",
            "enabled": True,
        })
        tools = GatewayTools(registry=registry, transport=self.transport, client_id="c1")
        tools._probe_current_privilege = lambda target_cfg, **kwargs: {
            "probe_status": "ok",
            "boot_id": "windows:1",
            "effective_identity": {"user": "Administrator"},
            "privilege": {
                "current_level": "administrator",
                "maximum_level": "administrator",
                "backend": "direct",
                "backend_ready": True,
                "transport_already_elevated": True,
            },
        }

        denied = tools.run_task("mock-target", "mock-proj", "status")
        self.assertFalse(denied["ok"])
        self.assertEqual(denied["error"]["code"], "PRIVILEGE_GRANT_REQUIRED")

        registry.add_grant({
            "client_id": "c1",
            "target_id": "mock-target",
            "project_id": "mock-proj",
            "capability": "target_admin",
            "enabled": True,
        })
        allowed = tools.run_task("mock-target", "mock-proj", "status")
        self.assertTrue(allowed["ok"], allowed)

    def test_run_task_preconditions_do_not_consume_one_use_privilege_approval(self):
        self.config._data["targets"]["mock-target"]["platform"] = "windows"
        self.config._data["targets"]["mock-target"]["privilege_policy"] = "ask_always"
        self.config._data["targets"]["mock-target"]["projects"]["mock-proj"]["root"] = "/evil/root"
        registry = JsonRegistry(self.config)
        registry.set_setting("gateway_enabled", "true")
        registry.add_client({"id": "c1", "enabled": True})
        for capability in ("execute", "target_admin"):
            registry.add_grant({
                "client_id": "c1",
                "target_id": "mock-target",
                "project_id": "mock-proj",
                "capability": capability,
                "enabled": True,
            })
        tools = GatewayTools(registry=registry, transport=self.transport, client_id="c1")
        tools._probe_target_facts = lambda target_cfg, **kwargs: {
            "probe_status": "ok",
            "boot_id": "windows:1",
            "effective_identity": {"user": "Administrator"},
            "privilege": {
                "current_level": "administrator",
                "maximum_level": "administrator",
                "backend": "direct",
                "backend_ready": True,
                "transport_already_elevated": True,
            },
        }

        approved = tools.approve_target_privilege("mock-target", "c1", "mock-proj")
        self.assertTrue(approved["ok"], approved)
        before = registry.get_privilege_approval("mock-target")
        self.assertIsNotNone(before)

        denied = tools.run_task("mock-target", "mock-proj", "status")
        self.assertFalse(denied["ok"])
        self.assertEqual(denied["error"]["code"], "PATH_OUTSIDE_ALLOWED_ROOT")
        self.assertIsNotNone(registry.get_privilege_approval("mock-target"))

    def test_privileged_run_requires_explicit_target_admin_even_with_wildcard(self):
        self.config._data["targets"]["mock-target"]["privilege_policy"] = "always_allow"
        registry = JsonRegistry(self.config)
        registry.set_setting("shell_enabled", "true")
        registry.add_client({"id": "c1", "enabled": True})
        registry.add_grant({
            "client_id": "c1",
            "target_id": "mock-target",
            "project_id": "mock-proj",
            "capability": "*",
            "enabled": True,
        })
        tools = GatewayTools(
            registry=registry,
            transport=self.transport,
            client_id="c1",
        )
        tools._probe_target_facts = lambda target_cfg, **kwargs: {
            "probe_status": "ok",
            "boot_id": "boot-1",
            "effective_identity": {"user": "tester", "uid": 1000, "euid": 1000},
            "privilege": {
                "current_level": "root",
                "maximum_level": "root",
                "backend": "direct",
                "backend_ready": True,
                "transport_already_elevated": True,
            },
        }

        denied = tools.run_command(
            "mock-target", "mock-proj", "id", privilege="required"
        )
        self.assertFalse(denied["ok"])
        self.assertEqual(denied["error"]["code"], "PRIVILEGE_GRANT_REQUIRED")

        registry.add_grant({
            "client_id": "c1",
            "target_id": "mock-target",
            "project_id": "mock-proj",
            "capability": "target_admin",
            "enabled": True,
        })
        allowed = tools.run_command(
            "mock-target", "mock-proj", "id", privilege="required"
        )
        self.assertTrue(allowed["ok"], allowed)
        self.assertTrue(allowed["result"]["privilege"]["effective"])

    def test_required_privilege_denied_by_default(self):
        self.config._data["targets"]["mock-target"]["privilege_policy"] = "never"
        tools = self._privileged_tools()
        tools._probe_target_facts = lambda target_cfg, **kwargs: {
            "probe_status": "ok",
            "boot_id": "boot-1",
            "effective_identity": {"user": "tester", "uid": 1000, "euid": 1000},
            "privilege": {
                "current_level": "root",
                "maximum_level": "root",
                "backend": "direct",
                "backend_ready": True,
                "transport_already_elevated": True,
            },
        }

        res = tools.run_command(
            "mock-target", "mock-proj", "id", privilege="required"
        )
        self.assertFalse(res["ok"])
        self.assertEqual(res["error"]["code"], "PRIVILEGE_DISABLED")

    def test_unscoped_sudo_is_not_accepted_as_privilege_backend(self):
        with self.assertRaises(Exception) as ctx:
            self.tools._prepare_privileged_command(
                self.config.get_target("mock-target"),
                "id",
                "/home/tester/proj",
                {
                    "current_level": "standard",
                    "maximum_level": "root",
                    "backend": "sudo",
                    "backend_ready": True,
                },
            )
        self.assertEqual(getattr(ctx.exception, "code", None), "PRIVILEGE_SETUP_REQUIRED")

    def test_required_privilege_reuses_shizuku_when_policy_allows(self):
        self.config._data["targets"]["mock-target"]["platform"] = "android-termux"
        self.config._data["targets"]["mock-target"]["privilege_policy"] = "always_allow"
        tools = self._privileged_tools()
        tools._probe_target_facts = lambda target_cfg, **kwargs: {
            "probe_status": "ok",
            "boot_id": "boot-1",
            "effective_identity": {"user": "u0_test", "uid": 10000, "euid": 10000},
            "privilege": {
                "current_level": "standard",
                "maximum_level": "android_shell",
                "backend": "shizuku",
                "backend_ready": True,
                "transport_already_elevated": False,
            },
        }

        res = tools.run_command(
            "mock-target",
            "mock-proj",
            "id",
            env={"MCP_TEST_VALUE": "hello world"},
            privilege="required",
        )
        self.assertTrue(res["ok"], res)
        self.assertTrue(self.transport.last_remote_cmd.startswith("rish -c "))
        self.assertIn("export MCP_TEST_VALUE=", self.transport.last_remote_cmd)
        self.assertIn("cd / && id", self.transport.last_remote_cmd)
        self.assertEqual(self.transport.last_cwd, "/")
        self.assertIsNone(self.transport.last_env)
        self.assertEqual(res["result"]["effective_cwd"], "/")
        self.assertEqual(
            res["result"]["authorization_cwd"], "/home/tester/proj"
        )
        self.assertTrue(res["result"]["privilege"]["effective"])
        self.assertEqual(res["result"]["privilege"]["backend"], "shizuku")
        self.assertEqual(res["result"]["privilege"]["level"], "android_shell")

    def test_required_privilege_uses_separate_privileged_ssh_identity(self):
        self.config._data["targets"]["mock-target"]["privilege_policy"] = "always_allow"
        self.config._data["targets"]["mock-target"]["privilege_user"] = "root"
        tools = self._privileged_tools()
        tools._probe_target_facts = lambda target_cfg, **kwargs: {
            "probe_status": "ok",
            "boot_id": "boot-1",
            "effective_identity": {"user": "tester", "uid": 1000, "euid": 1000},
            "privilege": {
                "current_level": "standard",
                "maximum_level": "root",
                "backend": "privileged-ssh",
                "backend_user": "root",
                "backend_ready": True,
                "transport_already_elevated": False,
            },
        }

        res = tools.run_command(
            "mock-target", "mock-proj", "id", privilege="required"
        )
        self.assertTrue(res["ok"], res)
        self.assertEqual(self.transport.last_target_user, "root")
        self.assertEqual(self.transport.last_remote_cmd, "id")
        self.assertEqual(res["result"]["privilege"]["backend"], "privileged-ssh")
        self.assertEqual(res["result"]["privilege"]["backend_user"], "root")
        self.assertEqual(res["result"]["privilege"]["level"], "root")

    def test_privileged_ssh_backend_requires_verified_os_admin_identity(self):
        self.config._data["targets"]["mock-target"]["privilege_user"] = "root"
        target = self.config.get_target("mock-target")

        def fake_run(target_cfg, remote_cmd, **kwargs):
            if target_cfg.get("user") == "root":
                return SSHTransportResult(
                    0,
                    json.dumps({
                        "probe_status": "ok",
                        "boot_id": None,
                        "effective_identity": {"user": "root", "uid": 0, "euid": 0},
                        "privilege": {
                            "current_level": "root",
                            "maximum_level": "root",
                            "backend": "direct",
                            "backend_ready": True,
                            "backend_reason": None,
                            "transport_already_elevated": True,
                        },
                    }) + "\n",
                    "",
                    5,
                )
            return SSHTransportResult(
                0,
                json.dumps({
                    "probe_status": "ok",
                    "os": "linux",
                    "ram_mb": 1024,
                    "boot_id": None,
                    "effective_identity": {"user": "tester", "uid": 1000, "euid": 1000},
                    "privilege": {
                        "current_level": "standard",
                        "maximum_level": "root",
                        "backend": "sudo",
                        "backend_ready": False,
                        "backend_reason": "sudo is not accepted",
                        "transport_already_elevated": False,
                    },
                    "features": {},
                }) + "\n",
                "",
                5,
            )

        original = self.transport.run_command
        self.transport.run_command = fake_run
        try:
            facts = self.tools._probe_target_facts(target, include_boot_id=False)
        finally:
            self.transport.run_command = original

        self.assertEqual(facts["privilege"]["backend"], "privileged-ssh")
        self.assertEqual(facts["privilege"]["backend_user"], "root")
        self.assertTrue(facts["privilege"]["backend_ready"])
        self.assertEqual(facts["privilege"]["maximum_level"], "root")

    def test_windows_privileged_ssh_fails_closed_without_administrator_token(self):
        self.config._data["targets"]["mock-target"]["platform"] = "windows"
        self.config._data["targets"]["mock-target"]["privilege_user"] = "Administrator"
        target = self.config.get_target("mock-target")

        def fake_run(target_cfg, remote_cmd, **kwargs):
            if target_cfg.get("user") == "Administrator":
                payload = {
                    "probe_status": "ok",
                    "boot_id": None,
                    "effective_identity": {"user": "Administrator"},
                    "privilege": {
                        "current_level": "standard",
                        "maximum_level": "standard",
                        "backend": None,
                        "backend_ready": False,
                        "backend_reason": None,
                        "transport_already_elevated": False,
                    },
                }
            else:
                payload = {
                    "probe_status": "ok",
                    "os": "windows",
                    "ram_mb": 4096,
                    "boot_id": None,
                    "effective_identity": {"user": "worker"},
                    "privilege": {
                        "current_level": "standard",
                        "maximum_level": "administrator",
                        "backend": "windows-sudo",
                        "backend_ready": False,
                        "backend_reason": "UAC interactive elevation is not a backend",
                        "transport_already_elevated": False,
                    },
                    "features": {},
                }
            return SSHTransportResult(0, json.dumps(payload) + "\n", "", 5)

        original = self.transport.run_command
        self.transport.run_command = fake_run
        try:
            facts = self.tools._probe_target_facts(target, include_boot_id=False)
        finally:
            self.transport.run_command = original

        self.assertEqual(facts["privilege"]["backend"], "privileged-ssh")
        self.assertEqual(facts["privilege"]["backend_user"], "Administrator")
        self.assertFalse(facts["privilege"]["backend_ready"])
        self.assertIn("not administrator", facts["privilege"]["backend_reason"])

    def test_privilege_approval_fails_closed_when_audit_unavailable(self):
        self.config._data["targets"]["mock-target"]["privilege_policy"] = "ask_always"
        tools = self._privileged_tools()
        tools._probe_target_facts = lambda target_cfg, **kwargs: {
            "probe_status": "ok",
            "boot_id": "boot-a",
            "effective_identity": {"user": "worker", "uid": 1000, "euid": 1000},
            "privilege": {
                "current_level": "root",
                "maximum_level": "root",
                "backend": "direct",
                "backend_ready": True,
                "transport_already_elevated": True,
            },
        }

        def broken_audit(event):
            raise OSError("audit unavailable")

        tools.config.record_activity = broken_audit
        res = tools.approve_target_privilege("mock-target", "c1", "mock-proj")
        self.assertFalse(res["ok"])
        self.assertEqual(res["error"]["code"], "AUDIT_UNAVAILABLE")
        self.assertIsNone(tools.config.get_privilege_approval("mock-target"))

    def test_ask_always_approval_is_single_use(self):
        data = {
            "targets": {
                "t1": {
                    "platform": "linux",
                    "host": "127.0.0.1",
                    "port": 22,
                    "user": "worker",
                    "ssh_alias": "t1",
                    "privilege_policy": "ask_always",
                    "enabled": True,
                    "projects": {
                        "p1": {
                            "root": "/srv/p1",
                            "read": True,
                            "write": False,
                            "enabled": True,
                            "tasks": {},
                        }
                    },
                }
            }
        }
        registry = JsonRegistry(GatewayConfig(raw_data=data))
        registry.set_setting("gateway_enabled", "true")
        registry.set_setting("shell_enabled", "true")
        registry.add_client({"id": "c1", "enabled": True})
        registry.add_grant({
            "client_id": "c1", "target_id": "t1", "project_id": "p1",
            "capability": "target_shell", "enabled": True,
        })
        registry.add_grant({
            "client_id": "c1", "target_id": "t1", "project_id": "p1",
            "capability": "target_admin", "enabled": True,
        })
        tools = GatewayTools(registry=registry, transport=self.transport, client_id="c1")
        tools._probe_target_facts = lambda target_cfg, **kwargs: {
            "probe_status": "ok",
            "boot_id": "boot-a",
            "effective_identity": {"user": "worker", "uid": 1000, "euid": 1000},
            "privilege": {
                "current_level": "root",
                "maximum_level": "root",
                "backend": "direct",
                "backend_ready": True,
                "transport_already_elevated": True,
            },
        }

        approved = tools.approve_target_privilege("t1", "c1", "p1")
        self.assertTrue(approved["ok"], approved)

        first = tools.run_command("t1", "p1", "id", privilege="required")
        self.assertTrue(first["ok"], first)

        second = tools.run_command("t1", "p1", "id", privilege="required")
        self.assertFalse(second["ok"])
        self.assertEqual(second["error"]["code"], "PRIVILEGE_APPROVAL_REQUIRED")

    def test_ask_always_approval_is_bound_to_client_and_project(self):
        self.config._data["targets"]["mock-target"]["privilege_policy"] = "ask_always"
        tools = self._privileged_tools("c1")
        tools._probe_target_facts = lambda target_cfg, **kwargs: {
            "probe_status": "ok",
            "boot_id": "boot-a",
            "effective_identity": {"user": "worker", "uid": 0, "euid": 0},
            "privilege": {
                "current_level": "root",
                "maximum_level": "root",
                "backend": "direct",
                "backend_ready": True,
                "transport_already_elevated": True,
            },
        }

        approved = tools.approve_target_privilege(
            "mock-target", "c1", "mock-proj"
        )
        self.assertTrue(approved["ok"], approved)

        tools.client_id = "c2"
        tools.config.add_client({"id": "c2", "enabled": True})
        tools.config.add_grant({
            "client_id": "c2",
            "target_id": "mock-target",
            "project_id": "mock-proj",
            "capability": "target_shell,target_admin",
            "enabled": True,
        })
        denied = tools.run_command(
            "mock-target", "mock-proj", "id", privilege="required"
        )
        self.assertFalse(denied["ok"])
        self.assertEqual(
            denied["error"]["code"], "PRIVILEGE_APPROVAL_REQUIRED"
        )
        self.assertIsNotNone(
            tools.config.get_privilege_approval("mock-target")
        )

    def test_ask_once_per_boot_approval_expires_when_boot_changes(self):
        data = {
            "targets": {
                "t1": {
                    "platform": "linux",
                    "host": "127.0.0.1",
                    "port": 22,
                    "user": "worker",
                    "ssh_alias": "t1",
                    "privilege_policy": "ask_once_per_boot",
                    "enabled": True,
                    "projects": {
                        "p1": {
                            "root": "/srv/p1",
                            "read": True,
                            "write": False,
                            "enabled": True,
                            "tasks": {},
                        }
                    },
                }
            }
        }
        registry = JsonRegistry(GatewayConfig(raw_data=data))
        registry.set_setting("gateway_enabled", "true")
        registry.set_setting("shell_enabled", "true")
        registry.add_client({"id": "c1", "enabled": True})
        registry.add_grant({
            "client_id": "c1", "target_id": "t1", "project_id": "p1",
            "capability": "target_shell", "enabled": True,
        })
        registry.add_grant({
            "client_id": "c1", "target_id": "t1", "project_id": "p1",
            "capability": "target_admin", "enabled": True,
        })
        tools = GatewayTools(registry=registry, transport=self.transport, client_id="c1")
        state = {"boot_id": "boot-a"}
        tools._probe_target_facts = lambda target_cfg, **kwargs: {
            "probe_status": "ok",
            "boot_id": state["boot_id"],
            "effective_identity": {"user": "worker", "uid": 1000, "euid": 1000},
            "privilege": {
                "current_level": "root",
                "maximum_level": "root",
                "backend": "direct",
                "backend_ready": True,
                "transport_already_elevated": True,
            },
        }

        approved = tools.approve_target_privilege("t1", "c1", "p1")
        self.assertTrue(approved["ok"], approved)
        self.assertEqual(approved["result"]["scope"], "current_boot")

        first = tools.run_command("t1", "p1", "id", privilege="required")
        self.assertTrue(first["ok"], first)
        again = tools.run_command("t1", "p1", "id", privilege="required")
        self.assertTrue(again["ok"], again)

        state["boot_id"] = "boot-b"
        after_reboot = tools.run_command("t1", "p1", "id", privilege="required")
        self.assertFalse(after_reboot["ok"])
        self.assertEqual(after_reboot["error"]["code"], "PRIVILEGE_APPROVAL_REQUIRED")

    def test_privilege_capable_standard_shell_cannot_bypass_never_policy(self):
        self.config._data["targets"]["mock-target"]["privilege_policy"] = "never"
        tools = self._privileged_tools()
        tools._probe_current_privilege = lambda target_cfg, **kwargs: {
            "probe_status": "ok",
            "boot_id": "android:1",
            "effective_identity": {"user": "tester", "uid": 1000, "euid": 1000},
            "privilege": {
                "current_level": "standard",
                "maximum_level": "standard",
                "backend": None,
                "backend_ready": False,
                "transport_already_elevated": False,
                "shell_can_elevate": True,
                "independent_elevator": "shizuku",
            },
        }

        res = tools.run_command("mock-target", "mock-proj", "id")
        self.assertFalse(res["ok"])
        self.assertEqual(res["error"]["code"], "PRIVILEGE_DISABLED")

    def test_wildcard_does_not_authorize_privilege_capable_standard_shell(self):
        self.config._data["targets"]["mock-target"]["privilege_policy"] = "always_allow"
        registry = JsonRegistry(self.config)
        registry.set_setting("gateway_enabled", "true")
        registry.set_setting("shell_enabled", "true")
        registry.add_client({"id": "c1", "enabled": True})
        registry.add_grant({
            "client_id": "c1",
            "target_id": "mock-target",
            "project_id": "mock-proj",
            "capability": "*",
            "enabled": True,
        })
        tools = GatewayTools(registry=registry, transport=self.transport, client_id="c1")
        tools._probe_current_privilege = lambda target_cfg, **kwargs: {
            "probe_status": "ok",
            "boot_id": "android:1",
            "effective_identity": {"user": "tester", "uid": 1000, "euid": 1000},
            "privilege": {
                "current_level": "standard",
                "maximum_level": "standard",
                "backend": None,
                "backend_ready": False,
                "transport_already_elevated": False,
                "shell_can_elevate": True,
                "independent_elevator": "shizuku",
            },
        }

        res = tools.run_command("mock-target", "mock-proj", "id")
        self.assertFalse(res["ok"])
        self.assertEqual(res["error"]["code"], "PRIVILEGE_GRANT_REQUIRED")

    def test_authorized_privilege_capable_standard_shell_stays_standard(self):
        self.config._data["targets"]["mock-target"]["privilege_policy"] = "always_allow"
        tools = self._privileged_tools()
        tools._probe_current_privilege = lambda target_cfg, **kwargs: {
            "probe_status": "ok",
            "boot_id": "android:1",
            "effective_identity": {"user": "tester", "uid": 1000, "euid": 1000},
            "privilege": {
                "current_level": "standard",
                "maximum_level": "standard",
                "backend": None,
                "backend_ready": False,
                "transport_already_elevated": False,
                "shell_can_elevate": True,
                "independent_elevator": "shizuku",
            },
        }

        res = tools.run_command("mock-target", "mock-proj", "echo safe")
        self.assertTrue(res["ok"], res)
        self.assertEqual(self.transport.last_target_user, "tester")
        self.assertEqual(self.transport.last_remote_cmd, "echo safe")
        self.assertTrue(res["result"]["privilege"]["guarded"])
        self.assertFalse(res["result"]["privilege"]["effective"])
        self.assertEqual(
            res["result"]["privilege"]["independent_elevator"], "shizuku"
        )
        self.assertEqual(res["result"]["privilege"]["level"], "standard")

    def test_already_elevated_transport_cannot_bypass_never_policy(self):
        self.config._data["targets"]["mock-target"]["platform"] = "windows"
        self.config._data["targets"]["mock-target"]["privilege_policy"] = "never"
        tools = self._privileged_tools()
        tools._probe_current_privilege = lambda target_cfg, **kwargs: {
            "probe_status": "ok",
            "boot_id": "windows:123",
            "effective_identity": {"user": "Administrator"},
            "privilege": {
                "current_level": "administrator",
                "maximum_level": "administrator",
                "backend": "direct",
                "backend_ready": True,
                "transport_already_elevated": True,
            },
        }

        res = tools.run_command("mock-target", "mock-proj", "whoami")
        self.assertFalse(res["ok"])
        self.assertEqual(res["error"]["code"], "PRIVILEGE_DISABLED")

    def test_ask_always_requires_cached_human_approval(self):
        self.config._data["targets"]["mock-target"]["privilege_policy"] = "ask_always"
        tools = self._privileged_tools()
        tools._probe_target_facts = lambda target_cfg, **kwargs: {
            "probe_status": "ok",
            "boot_id": "boot-1",
            "effective_identity": {"user": "tester"},
            "privilege": {
                "current_level": "root",
                "maximum_level": "root",
                "backend": "direct",
                "backend_ready": True,
                "transport_already_elevated": True,
            },
        }

        res = tools.run_command(
            "mock-target", "mock-proj", "id", privilege="required"
        )
        self.assertFalse(res["ok"])
        self.assertEqual(res["error"]["code"], "PRIVILEGE_APPROVAL_REQUIRED")

    def test_structured_mutations_fail_closed_on_destination_symlink_escapes(self):
        self.tools._is_writes_enabled = lambda: True
        self.tools._get_setting = lambda key, default=None: (
            "true" if key == "writes_enabled" else default
        )
        self.config._data["targets"]["mock-target"]["projects"]["mock-proj"]["write"] = True

        cases = [
            ("write", lambda: self.tools.write_file("mock-target", "mock-proj", "nested_symlink/new.txt", "x", create=True)),
            ("append", lambda: self.tools.append_file("mock-target", "mock-proj", "dest_symlink.txt", "x")),
            ("delete", lambda: self.tools.delete_file("mock-target", "mock-proj", "dest_symlink.txt")),
            ("copy", lambda: self.tools.copy_file("mock-target", "mock-proj", "existing.txt", "nested_symlink/copied.txt")),
            ("move", lambda: self.tools.move_file("mock-target", "mock-proj", "existing.txt", "nested_symlink/moved.txt")),
            ("mkdir", lambda: self.tools.mkdir("mock-target", "mock-proj", "nested_symlink/new_dir", parents=True)),
        ]
        for name, call in cases:
            with self.subTest(name=name):
                res = call()
                self.assertFalse(res["ok"])
                self.assertIn(res["error"]["code"], {"PATH_OUTSIDE_ALLOWED_ROOT", "SYMLINK_WRITE_DENIED"})

    def test_ssh_trust_change_fails_closed_when_audit_unavailable(self):
        discovery = MagicMock()
        discovery.trust_presented_key.return_value = {"status": "TRUSTED"}
        self.transport.discovery = discovery

        def broken_audit(event):
            raise OSError("audit disk unavailable")

        self.config.record_activity = broken_audit
        res = self.tools.trust_target_ssh_identity(
            "mock-target",
            "SHA256:reviewed",
        )

        self.assertFalse(res["ok"])
        self.assertEqual(res["error"]["code"], "AUDIT_UNAVAILABLE")
        discovery.trust_presented_key.assert_not_called()

    def test_critical_mutation_fails_closed_when_audit_unavailable(self):
        self.tools._is_writes_enabled = lambda: True
        self.config._data["targets"]["mock-target"]["projects"]["mock-proj"]["write"] = True
        def broken_audit(event):
            raise OSError("audit disk unavailable")
        self.config.record_activity = broken_audit
        res = self.tools.write_file("mock-target", "mock-proj", "new.txt", "hello", create=True)
        self.assertFalse(res["ok"])
        self.assertEqual(res["error"]["code"], "AUDIT_UNAVAILABLE")



if __name__ == "__main__":
    unittest.main()


