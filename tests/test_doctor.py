import os
import sys
import sqlite3
import tempfile
import unittest

sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "src")))

from mcp_gateway import doctor


class TestDoctor(unittest.TestCase):
    def test_check_runtime_and_contracts(self):
        results = doctor.check_runtime_and_contracts()
        self.assertTrue(len(results) >= 2)
        py_check = next((r for r in results if r.name == "Python Runtime"), None)
        self.assertIsNotNone(py_check)
        self.assertTrue(py_check.passed)

    def test_run_doctor_basic(self):
        overall, checks = doctor.run_doctor(verbose=False)
        self.assertIn(overall, ("HEALTHY", "DEGRADED", "UNHEALTHY"))
        self.assertTrue(len(checks) > 5)

    def test_registry_schema_must_match_runtime_exactly(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            path = os.path.join(tmpdir, "future.db")
            conn = sqlite3.connect(path)
            conn.execute("PRAGMA user_version = 999")
            conn.commit()
            conn.close()

            checks = doctor.check_registry_integrity(path)
            schema_check = next(c for c in checks if c.name == "Schema Version")
            self.assertFalse(schema_check.passed)
            self.assertIn("newer than runtime schema", schema_check.message)

    def test_run_repair_safe(self):
        repairs = doctor.run_repair()
        self.assertIsInstance(repairs, list)


if __name__ == "__main__":
    unittest.main()
