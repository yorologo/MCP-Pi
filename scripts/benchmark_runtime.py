#!/usr/bin/env python3
"""Read-only runtime benchmark for the MCP-Pi reference appliance.

The benchmark decomposes local gateway overhead without mutating Registry state:
- pure Python process startup;
- Python bridge startup/import/version;
- Python Core health in-process;
- Python bridge health via subprocess;
- pure Go adapter /live;
- Go adapter /health (which includes the current Python bridge subprocess).

It is intentionally stdlib-only and suitable for constrained ARMv6 hardware.
"""

from __future__ import annotations

import argparse
import json
import math
import os
from pathlib import Path
import statistics
import sqlite3
import subprocess
import sys
import time
from typing import Any, Callable
from urllib.request import Request, urlopen


DEFAULT_ROOT = Path("/home/mcp-gateway/mcp-gateway")
DEFAULT_HTTP_BASE = "http://127.0.0.1:8090"
DEFAULT_SERVICES = (
    "mcp-gateway-mcp.service",
    "mcp-gateway-admin.service",
    "mcp-gateway-tunnel.service",
)


def percentile(values: list[float], quantile: float) -> float:
    if not values:
        raise ValueError("percentile requires at least one value")
    if not 0 <= quantile <= 1:
        raise ValueError("quantile must be between 0 and 1")
    ordered = sorted(values)
    if len(ordered) == 1:
        return ordered[0]
    position = (len(ordered) - 1) * quantile
    lower = math.floor(position)
    upper = math.ceil(position)
    if lower == upper:
        return ordered[lower]
    weight = position - lower
    return ordered[lower] * (1 - weight) + ordered[upper] * weight


def summarize(values: list[float]) -> dict[str, float | int]:
    if not values:
        raise ValueError("summarize requires at least one value")
    return {
        "samples": len(values),
        "min_ms": round(min(values), 3),
        "mean_ms": round(statistics.fmean(values), 3),
        "p50_ms": round(percentile(values, 0.50), 3),
        "p95_ms": round(percentile(values, 0.95), 3),
        "max_ms": round(max(values), 3),
    }


def measure(fn: Callable[[], Any], samples: int, warmup: int = 1) -> dict[str, Any]:
    for _ in range(max(0, warmup)):
        fn()
    values: list[float] = []
    for _ in range(samples):
        started = time.perf_counter_ns()
        fn()
        values.append((time.perf_counter_ns() - started) / 1_000_000)
    return {"ok": True, **summarize(values)}


def command_runner(
    argv: list[str],
    *,
    env: dict[str, str] | None = None,
    timeout: float = 30,
    validate_json_ok: bool = False,
) -> Callable[[], str]:
    def run() -> str:
        result = subprocess.run(
            argv,
            env=env,
            text=True,
            capture_output=True,
            timeout=timeout,
            check=False,
        )
        if result.returncode != 0:
            raise RuntimeError(
                f"command failed rc={result.returncode}: {' '.join(argv)}; "
                f"stderr={result.stderr.strip()}"
            )
        if validate_json_ok:
            payload = json.loads(result.stdout)
            if payload.get("ok") is not True:
                raise RuntimeError(f"command returned non-ok payload: {payload}")
        return result.stdout

    return run


def adapter_health_ready(payload: dict[str, Any]) -> bool:
    core_health = payload.get("core_health")
    return (
        payload.get("ready") is True
        and payload.get("adapter_status") == "ready"
        and isinstance(core_health, dict)
        and core_health.get("ok") is True
    )


def http_runner(
    url: str,
    *,
    expect_json_ok: bool = False,
    expect_adapter_ready: bool = False,
) -> Callable[[], bytes]:
    def run() -> bytes:
        request = Request(url, headers={"Accept": "application/json"})
        with urlopen(request, timeout=30) as response:
            body = response.read()
            if response.status != 200:
                raise RuntimeError(f"{url} returned HTTP {response.status}")
        if expect_json_ok or expect_adapter_ready:
            payload = json.loads(body)
            if expect_json_ok and payload.get("ok") is not True:
                raise RuntimeError(f"{url} returned non-ok payload: {payload}")
            if expect_adapter_ready and not adapter_health_ready(payload):
                raise RuntimeError(f"{url} returned non-ready adapter health: {payload}")
        return body

    return run


def detect_project_root() -> Path:
    configured = os.environ.get("MCP_GATEWAY_ROOT")
    if configured:
        return Path(configured).expanduser().resolve()
    local = Path(__file__).resolve().parents[1]
    if (local / "src" / "mcp_gateway").is_dir():
        return local
    return DEFAULT_ROOT


def resolve_db_path(root: Path, configured: str | None = None) -> Path:
    candidates = []
    if configured:
        candidates.append(Path(configured).expanduser())
    env_db = os.environ.get("MCP_GATEWAY_DB")
    if env_db:
        candidates.append(Path(env_db).expanduser())
    candidates.extend(
        [
            Path("/home/mcp-gateway/.local/share/mcp-gateway/gateway.db"),
            root / "gateway.db",
        ]
    )
    for candidate in candidates:
        try:
            resolved = candidate.resolve()
        except OSError:
            continue
        if resolved.is_file():
            return resolved
    raise FileNotFoundError(
        "No existing gateway Registry was found; refusing to create a benchmark fallback DB"
    )


def bridge_env(root: Path, db_path: Path) -> dict[str, str]:
    env = dict(os.environ)
    src = str(root / "src")
    current = env.get("PYTHONPATH", "")
    env["PYTHONPATH"] = src if not current else f"{src}{os.pathsep}{current}"
    env["MCP_GATEWAY_DB"] = str(db_path)
    return env


def meminfo() -> dict[str, int]:
    values: dict[str, int] = {}
    path = Path("/proc/meminfo")
    if not path.exists():
        return values
    for line in path.read_text(encoding="utf-8").splitlines():
        key, _, raw = line.partition(":")
        if key not in {"MemTotal", "MemAvailable", "SwapTotal", "SwapFree"}:
            continue
        token = raw.strip().split()[0]
        values[f"{key.lower()}_kb"] = int(token)
    return values


def temperature_c() -> float | None:
    candidates = (
        Path("/sys/class/thermal/thermal_zone0/temp"),
        Path("/sys/class/hwmon/hwmon0/temp1_input"),
    )
    for path in candidates:
        try:
            raw = float(path.read_text(encoding="utf-8").strip())
            return round(raw / 1000 if raw > 200 else raw, 2)
        except (OSError, ValueError):
            continue
    return None


def service_snapshot(service: str) -> dict[str, Any]:
    try:
        result = subprocess.run(
            [
                "systemctl",
                "show",
                "--property=MainPID",
                "--property=ActiveState",
                "--no-page",
                service,
            ],
            text=True,
            capture_output=True,
            timeout=5,
            check=False,
        )
    except (OSError, subprocess.TimeoutExpired) as exc:
        return {"service": service, "available": False, "error": str(exc)}

    properties = {}
    for line in result.stdout.splitlines():
        key, sep, value = line.partition("=")
        if sep:
            properties[key.strip()] = value.strip()
    pid_text = properties.get("MainPID", "0")
    state = properties.get("ActiveState", "unknown")
    try:
        pid = int(pid_text)
    except ValueError:
        pid = 0

    snapshot: dict[str, Any] = {
        "service": service,
        "available": result.returncode == 0,
        "active_state": state,
        "pid": pid,
    }
    if pid <= 0:
        return snapshot

    status_path = Path(f"/proc/{pid}/status")
    try:
        for line in status_path.read_text(encoding="utf-8").splitlines():
            if line.startswith("VmRSS:"):
                snapshot["rss_kb"] = int(line.split()[1])
            elif line.startswith("VmSize:"):
                snapshot["vmsize_kb"] = int(line.split()[1])
    except OSError as exc:
        snapshot["proc_error"] = str(exc)
    return snapshot


def safe_measure(
    name: str,
    fn: Callable[[], Any],
    *,
    samples: int,
    warmup: int = 1,
) -> tuple[str, dict[str, Any]]:
    try:
        return name, measure(fn, samples=samples, warmup=warmup)
    except Exception as exc:  # Evidence collection should report, not hide, unavailable layers.
        return name, {"ok": False, "error": str(exc)}


class ReadOnlyHealthRegistry:
    """Minimum Registry surface required by GatewayTools.health(), opened read-only."""

    def __init__(self, db_path: Path):
        self.db_path = db_path
        self.uri = f"file:{db_path}?mode=ro"

    def _connect(self):
        return sqlite3.connect(self.uri, uri=True)

    def get_setting(self, key: str, default: Any = None) -> Any:
        with self._connect() as conn:
            row = conn.execute(
                "SELECT value FROM settings WHERE key = ?",
                (key,),
            ).fetchone()
        return row[0] if row else default

    @property
    def target_count(self) -> int:
        with self._connect() as conn:
            row = conn.execute("SELECT COUNT(*) FROM targets").fetchone()
        return int(row[0]) if row else 0


def build_inprocess_health(root: Path, db_path: Path) -> Callable[[], Any]:
    src = str(root / "src")
    if src not in sys.path:
        sys.path.insert(0, src)
    from mcp_gateway.tools import GatewayTools

    gateway = GatewayTools(registry=ReadOnlyHealthRegistry(db_path))

    def run() -> Any:
        result = gateway.health()
        if result.get("ok") is not True:
            raise RuntimeError(f"in-process health failed: {result}")
        return result

    return run


def decision_inputs(results: dict[str, Any]) -> dict[str, Any]:
    def p50(name: str) -> float | None:
        value = results.get(name, {})
        if value.get("ok") is True:
            return float(value["p50_ms"])
        return None

    startup = p50("python_startup")
    bridge_health = p50("bridge_health_subprocess")
    core_health = p50("core_health_inprocess")
    go_live = p50("adapter_live_http")
    adapter_health = p50("adapter_health_http")

    derived: dict[str, Any] = {}
    if bridge_health is not None and core_health is not None:
        derived["bridge_process_tax_p50_ms"] = round(max(0.0, bridge_health - core_health), 3)
        if bridge_health > 0:
            derived["bridge_process_tax_pct_of_bridge_health"] = round(
                max(0.0, bridge_health - core_health) / bridge_health * 100,
                2,
            )
    if adapter_health is not None and go_live is not None:
        derived["adapter_health_minus_go_live_p50_ms"] = round(
            max(0.0, adapter_health - go_live), 3
        )
    if startup is not None and bridge_health is not None:
        derived["python_startup_pct_of_bridge_health"] = round(
            startup / bridge_health * 100 if bridge_health else 0.0,
            2,
        )
    return derived


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Measure MCP-Pi runtime overhead without mutating appliance state."
    )
    parser.add_argument("--samples", type=int, default=7)
    parser.add_argument("--db", default=None, help="Existing gateway.db to benchmark against")
    parser.add_argument("--http-base", default=os.environ.get("MCP_GATEWAY_HTTP", DEFAULT_HTTP_BASE))
    parser.add_argument("--json", action="store_true", dest="as_json")
    parser.add_argument(
        "--allow-partial",
        action="store_true",
        help="Return success even if an expected measurement layer is unavailable.",
    )
    args = parser.parse_args(argv)
    if not 3 <= args.samples <= 50:
        parser.error("--samples must be between 3 and 50")
    return args


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    root = detect_project_root()
    db_path = resolve_db_path(root, args.db)
    env = bridge_env(root, db_path)
    python = sys.executable

    benchmarks: dict[str, dict[str, Any]] = {}
    cases = [
        (
            "python_startup",
            command_runner([python, "-c", "pass"], env=env),
        ),
        (
            "bridge_version_subprocess",
            command_runner(
                [python, "-m", "mcp_gateway.bridge", "version"],
                env=env,
                validate_json_ok=True,
            ),
        ),
        (
            "core_health_inprocess",
            build_inprocess_health(root, db_path),
        ),
        (
            "bridge_health_subprocess",
            command_runner(
                [
                    python,
                    "-m",
                    "mcp_gateway.bridge",
                    "invoke",
                    "health",
                    "{}",
                    "--client-id",
                    "local",
                ],
                env=env,
                validate_json_ok=True,
            ),
        ),
        (
            "adapter_live_http",
            http_runner(f"{args.http_base.rstrip('/')}/live"),
        ),
        (
            "adapter_health_http",
            http_runner(
                f"{args.http_base.rstrip('/')}/health",
                expect_adapter_ready=True,
            ),
        ),
    ]

    for name, fn in cases:
        case_name, result = safe_measure(name, fn, samples=args.samples)
        benchmarks[case_name] = result

    load = None
    try:
        load = [round(value, 3) for value in os.getloadavg()]
    except (AttributeError, OSError):
        pass

    payload = {
        "benchmark_version": 1,
        "project_root": str(root),
        "db_path": str(db_path),
        "python": sys.version.split()[0],
        "samples": args.samples,
        "read_only": True,
        "system": {
            **meminfo(),
            "load_1m_5m_15m": load,
            "temperature_c": temperature_c(),
            "services": [service_snapshot(name) for name in DEFAULT_SERVICES],
        },
        "benchmarks": benchmarks,
        "decision_inputs": decision_inputs(benchmarks),
    }

    if args.as_json:
        print(json.dumps(payload, indent=2, sort_keys=True))
    else:
        print("MCP-Pi runtime benchmark (read-only)")
        print(f"project_root={root}")
        print(f"python={payload['python']} samples={args.samples}")
        for name, result in benchmarks.items():
            if result.get("ok"):
                print(
                    f"{name}: p50={result['p50_ms']:.3f}ms "
                    f"p95={result['p95_ms']:.3f}ms mean={result['mean_ms']:.3f}ms"
                )
            else:
                print(f"{name}: UNAVAILABLE ({result.get('error')})")
        print("decision_inputs=" + json.dumps(payload["decision_inputs"], sort_keys=True))
        print("services=" + json.dumps(payload["system"]["services"], sort_keys=True))

    complete = all(result.get("ok") is True for result in benchmarks.values())
    return 0 if complete or args.allow_partial else 2


if __name__ == "__main__":
    raise SystemExit(main())
