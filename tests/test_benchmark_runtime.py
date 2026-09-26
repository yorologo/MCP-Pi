import contextlib
import importlib.util
import io
from pathlib import Path
import tempfile
import unittest


MODULE_PATH = Path(__file__).resolve().parents[1] / "scripts" / "benchmark_runtime.py"
SPEC = importlib.util.spec_from_file_location("benchmark_runtime", MODULE_PATH)
benchmark_runtime = importlib.util.module_from_spec(SPEC)
assert SPEC and SPEC.loader
SPEC.loader.exec_module(benchmark_runtime)


class BenchmarkRuntimeTests(unittest.TestCase):
    def test_percentile_interpolates(self):
        self.assertEqual(benchmark_runtime.percentile([1.0], 0.95), 1.0)
        self.assertEqual(benchmark_runtime.percentile([1.0, 3.0], 0.50), 2.0)
        self.assertAlmostEqual(
            benchmark_runtime.percentile([1.0, 2.0, 3.0, 4.0], 0.95),
            3.85,
        )

    def test_summarize_has_stable_fields(self):
        result = benchmark_runtime.summarize([1.0, 2.0, 3.0])
        self.assertEqual(result["samples"], 3)
        self.assertEqual(result["min_ms"], 1.0)
        self.assertEqual(result["p50_ms"], 2.0)
        self.assertEqual(result["max_ms"], 3.0)

    def test_decision_inputs_decomposes_bridge_tax(self):
        results = {
            "python_startup": {"ok": True, "p50_ms": 20.0},
            "bridge_health_subprocess": {"ok": True, "p50_ms": 120.0},
            "core_health_inprocess": {"ok": True, "p50_ms": 10.0},
            "adapter_live_http": {"ok": True, "p50_ms": 2.0},
            "adapter_health_http": {"ok": True, "p50_ms": 130.0},
        }
        derived = benchmark_runtime.decision_inputs(results)
        self.assertEqual(derived["bridge_process_tax_p50_ms"], 110.0)
        self.assertEqual(derived["adapter_health_minus_go_live_p50_ms"], 128.0)
        self.assertAlmostEqual(
            derived["bridge_process_tax_pct_of_bridge_health"],
            91.67,
            places=2,
        )

    def test_parse_args_rejects_too_few_samples(self):
        with contextlib.redirect_stderr(io.StringIO()):
            with self.assertRaises(SystemExit):
                benchmark_runtime.parse_args(["--samples", "2"])

    def test_resolve_db_path_refuses_to_create_missing_registry(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            missing = root / "missing.db"
            with self.assertRaises(FileNotFoundError):
                benchmark_runtime.resolve_db_path(root, str(missing))
            self.assertFalse(missing.exists())
            self.assertFalse((root / "gateway.db").exists())


if __name__ == "__main__":
    unittest.main()
