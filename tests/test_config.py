import json
import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..")))
sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "src")))

try:
    from mcp_gateway.config import ConfigError, GatewayConfig
except ImportError:
    from src.mcp_gateway.config import ConfigError, GatewayConfig


class TestGatewayConfig(unittest.TestCase):

    def setUp(self):
        self.valid_data = {
            "version": "1.0",
            "targets": {
                "test-target": {
                    "platform": "linux",
                    "host": "192.168.1.50",
                    "port": 22,
                    "user": "testuser",
                    "ssh_alias": "test-alias",
                    "enabled": True,
                    "projects": {
                        "test-proj": {
                            "root": "/tmp/test-proj",
                            "read": True,
                            "write": False,
                            "tasks": {
                                "echo_test": {
                                    "enabled": True,
                                    "argv": ["echo", "hello"],
                                    "timeout": 10,
                                }
                            }
                        }
                    }
                }
            }
        }

    def test_valid_config_loading(self):
        cfg = GatewayConfig(raw_data=self.valid_data)
        self.assertEqual(cfg.target_count, 1)
        target = cfg.get_target("test-target")
        self.assertEqual(target["host"], "192.168.1.50")
        project = cfg.get_project("test-target", "test-proj")
        self.assertEqual(project["root"], "/tmp/test-proj")
        self.assertTrue(project["read"])
        self.assertFalse(project["write"])

    def test_privilege_policy_defaults_to_never_and_rejects_unknown_values(self):
        cfg = GatewayConfig(raw_data=self.valid_data)
        target = cfg.get_target("test-target")
        self.assertEqual(target["privilege_policy"], "never")
        self.assertEqual(target["privilege_user"], "")

        invalid = {
            **self.valid_data,
            "targets": {
                **self.valid_data["targets"],
                "test-target": {
                    **self.valid_data["targets"]["test-target"],
                    "privilege_policy": "magic",
                },
            },
        }
        with self.assertRaises(ConfigError) as ctx:
            GatewayConfig(raw_data=invalid)
        self.assertEqual(ctx.exception.code, "CONFIG_ERROR")

        invalid_user = {
            **self.valid_data,
            "targets": {
                **self.valid_data["targets"],
                "test-target": {
                    **self.valid_data["targets"]["test-target"],
                    "privilege_user": ["root"],
                },
            },
        }
        with self.assertRaises(ConfigError) as ctx:
            GatewayConfig(raw_data=invalid_user)
        self.assertEqual(ctx.exception.code, "CONFIG_ERROR")

    def test_unknown_target_raises(self):
        cfg = GatewayConfig(raw_data=self.valid_data)
        with self.assertRaises(ConfigError) as ctx:
            cfg.get_target("nonexistent")
        self.assertEqual(ctx.exception.code, "UNKNOWN_TARGET")

    def test_unknown_project_raises(self):
        cfg = GatewayConfig(raw_data=self.valid_data)
        with self.assertRaises(ConfigError) as ctx:
            cfg.get_project("test-target", "nonexistent")
        self.assertEqual(ctx.exception.code, "UNKNOWN_PROJECT")

    def test_missing_file_raises(self):
        with self.assertRaises(ConfigError) as ctx:
            GatewayConfig.load("/nonexistent/path/targets.json")
        self.assertEqual(ctx.exception.code, "CONFIG_ERROR")

    def test_invalid_json_raises(self):
        with tempfile.NamedTemporaryFile("w", delete=False) as f:
            f.write("{invalid json")
            tmp_path = f.name
        try:
            with self.assertRaises(ConfigError) as ctx:
                GatewayConfig.load(tmp_path)
            self.assertEqual(ctx.exception.code, "CONFIG_ERROR")
        finally:
            os.remove(tmp_path)

    def test_relative_project_root_raises(self):
        invalid_data = dict(self.valid_data)
        invalid_data["targets"]["test-target"]["projects"]["test-proj"]["root"] = "relative/path"
        with self.assertRaises(ConfigError):
            GatewayConfig(raw_data=invalid_data)

    def test_list_targets_sanitized(self):
        cfg = GatewayConfig(raw_data=self.valid_data)
        targets = cfg.list_targets()
        self.assertEqual(len(targets), 1)
        t0 = targets[0]
        self.assertEqual(t0["id"], "test-target")
        self.assertEqual(t0["platform"], "linux")
        self.assertNotIn("host", t0)
        self.assertNotIn("password", t0)
        self.assertNotIn("private_key", t0)


if __name__ == "__main__":
    unittest.main()
