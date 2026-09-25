import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..")))
sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "src")))

from mcp_gateway.config import GatewayConfig
from mcp_gateway.registry import SQLiteRegistry, import_from_json
from mcp_gateway.ssh_transport import SSHTransportResult
from mcp_gateway.tools import GatewayTools


class MockTransport:
    def run_command(self, target, remote_cmd, timeout=None, cwd=None, **kwargs):
        if "hostname" in remote_cmd:
            return SSHTransportResult(0, "mock-host\n", "", 10)
        return SSHTransportResult(0, "ok\n", "", 10)

    def resolve_canonical_path(self, target, candidate_path, timeout=10):
        return candidate_path


class TestKillSwitchAndDisable(unittest.TestCase):
    def setUp(self):
        self.raw_data = {
            "targets": {
                "t1": {
                    "platform": "linux",
                    "host": "localhost",
                    "port": 22,
                    "user": "root",
                    "ssh_alias": "test",
                    "enabled": True,
                    "projects": {
                        "p1": {
                            "root": "/app",
                            "read": True,
                            "write": False,
                            "enabled": True,
                            "tasks": {
                                "echo": {"argv": ["echo", "hi"], "timeout": 5}
                            }
                        },
                        "p_disabled": {
                            "root": "/app/disabled",
                            "read": True,
                            "write": False,
                            "enabled": False,
                            "tasks": {}
                        }
                    }
                },
                "t_disabled": {
                    "platform": "linux",
                    "host": "localhost",
                    "port": 22,
                    "user": "root",
                    "ssh_alias": "test",
                    "enabled": False,
                    "projects": {
                        "p1": {
                            "root": "/app",
                            "read": True,
                            "write": False,
                            "enabled": True,
                            "tasks": {}
                        }
                    }
                }
            }
        }
        self.temp_dir = tempfile.TemporaryDirectory()
        self.db_path = os.path.join(self.temp_dir.name, "test_ks.db")
        self.registry = SQLiteRegistry(self.db_path)
        cfg = GatewayConfig(self.raw_data)
        import_from_json(cfg, self.registry)
        self.tools = GatewayTools(registry=self.registry, transport=MockTransport())

    def tearDown(self):
        self.temp_dir.cleanup()

    def test_kill_switch_cycle(self):
        # 1. Enabled by default
        self.assertEqual(self.registry.get_setting("gateway_enabled"), "true")
        res = self.tools.target_status("t1")
        self.assertTrue(res["ok"])

        # 2. Disable gateway (kill switch)
        self.registry.set_setting("gateway_enabled", "false")
        res_dis = self.tools.target_status("t1")
        self.assertFalse(res_dis["ok"])
        self.assertEqual(res_dis["error"]["code"], "GATEWAY_DISABLED")

        # Health should still succeed and reflect disabled state
        h = self.tools.health()
        self.assertTrue(h["ok"])
        self.assertEqual(h["result"]["gateway_status"], "disabled")
        self.assertFalse(h["result"]["gateway_enabled"])

        # Other tools also blocked
        res_task = self.tools.run_task("t1", "p1", "echo")
        self.assertFalse(res_task["ok"])
        self.assertEqual(res_task["error"]["code"], "GATEWAY_DISABLED")

        # 3. Re-enable gateway
        self.registry.set_setting("gateway_enabled", "true")
        res_re = self.tools.target_status("t1")
        self.assertTrue(res_re["ok"])

    def test_shell_kill_switch_applies_to_local_internal_caller(self):
        self.registry.set_setting("shell_enabled", "false")
        res = self.tools.run_command("t1", "p1", "echo blocked")
        self.assertFalse(res["ok"])
        self.assertEqual(res["error"]["code"], "TARGET_SHELL_DISABLED")

    def test_target_disable_enforced(self):
        res = self.tools.target_status("t_disabled")
        self.assertFalse(res["ok"])
        self.assertEqual(res["error"]["code"], "TARGET_DISABLED")

    def test_project_disable_enforced(self):
        res = self.tools.git_status("t1", "p_disabled")
        self.assertFalse(res["ok"])
        self.assertEqual(res["error"]["code"], "PROJECT_DISABLED")


if __name__ == "__main__":
    unittest.main()
