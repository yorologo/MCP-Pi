import os
import sys
import unittest

sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "src")))

from mcp_gateway import compatibility


class TestCompatibility(unittest.TestCase):
    def test_default_compatibility(self):
        compat = compatibility.get_compatibility()
        self.assertIn("gateway_version", compat)
        self.assertIn("core_api_version", compat)
        self.assertIn("bridge_api_version", compat)
        self.assertIn("tool_catalog_version", compat)
        self.assertIn("registry_schema_version", compat)
        self.assertEqual(compatibility.get_core_api_version(), 1)
        self.assertEqual(compatibility.get_bridge_api_version(), 1)
        self.assertEqual(compatibility.get_tool_catalog_version(), 4)
        self.assertEqual(compatibility.get_registry_schema_version(), 4)

    def test_verify_compatibility_success(self):
        valid_candidate = {
            "core_api_version": 1,
            "bridge_api_version": 1,
            "registry_schema_version": 4,
        }
        ok, errors = compatibility.verify_compatibility(valid_candidate)
        self.assertTrue(ok)
        self.assertEqual(len(errors), 0)

    def test_verify_compatibility_mismatch(self):
        invalid_candidate = {
            "core_api_version": 99,
            "bridge_api_version": 1,
        }
        ok, errors = compatibility.verify_compatibility(invalid_candidate)
        self.assertFalse(ok)
        self.assertTrue(any("core_api" in e for e in errors))


if __name__ == "__main__":
    unittest.main()
