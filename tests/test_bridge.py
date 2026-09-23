"""Unit tests for MCP Gateway Python Core Bridge."""

import io
import json
import os
import sys
import unittest
from unittest.mock import MagicMock, patch

sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "src")))

from mcp_gateway.bridge import invoke_tool, main


class TestBridge(unittest.TestCase):

    def setUp(self):
        self.mock_registry = MagicMock()
        self.mock_registry.list_targets.return_value = [{"id": "t1", "enabled": True}]
        self.mock_registry.target_count = 1
        self.mock_registry.get_setting.return_value = "true"

    def test_unallowlisted_tool_rejected(self):
        res = invoke_tool("arbitrary_exec", {}, registry=self.mock_registry)
        self.assertFalse(res["ok"])
        self.assertEqual(res["error"]["code"], "TOOL_NOT_ALLOWED")

    def test_health_invocation(self):
        res = invoke_tool("health", {}, registry=self.mock_registry)
        self.assertTrue(res["ok"])
        self.assertEqual(res["tool"], "health")
        self.assertIn("gateway_status", res["result"])

    def test_list_targets_invocation(self):
        res = invoke_tool("list_targets", {}, registry=self.mock_registry)
        self.assertTrue(res["ok"])
        self.assertEqual(res["tool"], "list_targets")
        self.assertIn("targets", res["result"])

    def test_missing_required_argument(self):
        res = invoke_tool("target_status", {}, registry=self.mock_registry)
        self.assertFalse(res["ok"])
        self.assertEqual(res["error"]["code"], "INVALID_ARGUMENTS")

        res2 = invoke_tool("read_file", {"target": "t1"}, registry=self.mock_registry)
        self.assertFalse(res2["ok"])
        self.assertEqual(res2["error"]["code"], "INVALID_ARGUMENTS")

    def test_cli_invoke_health(self):
        buf = io.StringIO()
        with patch("sys.stdout", buf):
            code = main(["invoke", "health"])
        self.assertEqual(code, 0)
        output = json.loads(buf.getvalue())
        self.assertTrue(output["ok"])
        self.assertEqual(output["tool"], "health")

    def test_cli_invalid_json(self):
        buf = io.StringIO()
        with patch("sys.stdout", buf):
            code = main(["invoke", "target_status", "not_valid_json"])
        self.assertEqual(code, 1)
        output = json.loads(buf.getvalue())
        self.assertFalse(output["ok"])
        self.assertEqual(output["error"]["code"], "INVALID_ARGUMENTS")

    def test_write_file_argument_validation(self):
        res = invoke_tool("write_file", {"target": "t1"}, registry=self.mock_registry)
        self.assertFalse(res["ok"])
        self.assertEqual(res["error"]["code"], "INVALID_ARGUMENTS")

    def test_cli_version(self):
        buf = io.StringIO()
        with patch("sys.stdout", buf):
            code = main(["version"])
        self.assertEqual(code, 0)
        output = json.loads(buf.getvalue())
        self.assertTrue(output["ok"])
        self.assertEqual(output["gateway_version"], "1.3.5")
        self.assertEqual(output["core_api_version"], 1)

        self.assertEqual(output["bridge_api_version"], 1)
        self.assertEqual(output["tool_catalog_version"], 3)

    def test_cli_tools(self):
        buf = io.StringIO()
        with patch("sys.stdout", buf):
            code = main(["tools"])
        self.assertEqual(code, 0)
        output = json.loads(buf.getvalue())
        self.assertTrue(output["ok"])
        tools = output["tools"]
        self.assertIsInstance(tools, list)
        self.assertEqual(len(tools), 21)
        self.assertEqual(output["tool_count"], 21)
        self.assertEqual(output["tool_catalog_version"], 3)
        self.assertEqual(len(output["catalog_hash"]), 64)
        # Check alphabetical order
        self.assertEqual(tools, sorted(tools))

    def test_invoke_with_request_id(self):
        buf = io.StringIO()
        with patch("sys.stdout", buf):
            code = main(["invoke", "health", "{}", "--request-id", "req-test-123"])
        self.assertEqual(code, 0)
        output = json.loads(buf.getvalue())
        self.assertTrue(output["ok"])
        self.assertEqual(output.get("request_id"), "req-test-123")

    def test_anonymous_client_denied(self):
        buf = io.StringIO()
        with patch("sys.stdout", buf):
            code = main(["tools", "--client-id", "NONE"])
        self.assertEqual(code, 0)
        output = json.loads(buf.getvalue())
        self.assertEqual(output["tools"], [])

        res = invoke_tool("health", {}, registry=self.mock_registry, client_id="NONE")
        self.assertFalse(res["ok"])
        self.assertEqual(res["error"]["code"], "TOOL_NOT_ALLOWED")
        self.assertIn("ANONYMOUS_CLIENT_DENIED", res["error"]["message"])

    def test_client_grants_filtering_and_hidden_call(self):
        reg = MagicMock()
        reg.get_client.return_value = {"id": "c1", "enabled": True}
        reg.get_setting.return_value = "true"
        # Client only has grant for health and list_targets
        reg.get_client_grants.return_value = [
            {"client_id": "c1", "target_id": "*", "project_id": "*", "capability": "health,list_targets", "enabled": True}
        ]

        from mcp_gateway.bridge import get_tools_catalog
        tools = get_tools_catalog(client_id="c1", registry=reg)
        self.assertEqual(tools, ["health", "list_targets"])
        self.assertNotIn("write_file", tools)
        self.assertNotIn("read_file", tools)

        # Call allowed tool
        res_allowed = invoke_tool("health", {}, registry=reg, client_id="c1")
        self.assertTrue(res_allowed["ok"])

        # Call hidden tool
        res_hidden = invoke_tool("write_file", {"target": "t1", "project": "p1", "path": "a.txt", "content": "foo"}, registry=reg, client_id="c1")
        self.assertFalse(res_hidden["ok"])
        self.assertEqual(res_hidden["error"]["code"], "TOOL_NOT_ALLOWED")
        self.assertIn("lacks grant capability", res_hidden["error"]["message"])

    def test_invoke_run_command_bridge(self):
        with patch("mcp_gateway.bridge.GatewayTools") as mock_gw_cls:
            mock_gw = mock_gw_cls.return_value
            mock_gw.run_command.return_value = {
                "ok": True,
                "tool": "run_command",
                "result": {"stdout": "hello\n", "exit_code": 0},
            }
            res = invoke_tool("run_command", {"target": "t1", "command": "echo hello", "client_id": "admin"}, registry=self.mock_registry)
            self.assertTrue(res["ok"])
            mock_gw.run_command.assert_called_once_with(
                target="t1",
                command="echo hello",
                project=None,
                cwd=None,
                env=None,
                timeout=None,
                stdin=None,
            )


if __name__ == "__main__":
    unittest.main()
