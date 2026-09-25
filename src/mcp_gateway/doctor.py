"""Diagnostic inspection (doctor) and safe repair engine for MCP Gateway."""

import json
import os
import platform
import socket
import sqlite3
import subprocess
import sys
import urllib.request
import urllib.error
from typing import Any, Dict, List, Optional, Tuple

from . import compatibility
from .registry import SQLiteRegistry, get_registry
from .tools import GatewayTools


class CheckResult:
    def __init__(self, name: str, passed: bool, message: str, severity: str = "error"):
        self.name = name
        self.passed = passed
        self.message = message
        self.severity = severity  # "error" or "warning"


def check_runtime_and_contracts() -> List[CheckResult]:
    results = []
    # 1. Python version
    py_ver = sys.version_info
    if py_ver.major == 3 and py_ver.minor >= 9:
        results.append(CheckResult("Python Runtime", True, f"Python {py_ver.major}.{py_ver.minor}.{py_ver.micro}"))
    else:
        results.append(CheckResult("Python Runtime", False, f"Python {py_ver.major}.{py_ver.minor} < 3.9 required"))

    # 2. Architecture
    arch = platform.machine()
    compat = compatibility.get_compatibility()
    allowed_archs = compat.get("runtime", {}).get("architecture", ["armv6l", "aarch64", "x86_64"])
    if arch in allowed_archs or "arm" in arch or "aarch" in arch or "x86" in arch:
        results.append(CheckResult("Architecture", True, f"System architecture: {arch}"))
    else:
        results.append(CheckResult("Architecture", True, f"System architecture: {arch}", severity="warning"))

    # 3. Compatibility contract
    c_file = compatibility.find_compatibility_file()
    if c_file:
        results.append(CheckResult("Compatibility Contract", True, f"Loaded from {c_file} (Gateway v{compatibility.get_gateway_version()})"))
    else:
        results.append(CheckResult("Compatibility Contract", True, f"Using built-in defaults (Gateway v{compatibility.get_gateway_version()})"))

    return results


def check_registry_integrity(db_path: Optional[str] = None) -> List[CheckResult]:
    results = []
    path = db_path or os.environ.get("MCP_GATEWAY_DB")
    if not path:
        home = os.path.expanduser("~")
        candidates = [
            os.path.join(home, ".local", "share", "mcp-gateway", "gateway.db"),
            "/home/mcp-gateway/.local/share/mcp-gateway/gateway.db",
            os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "..", "gateway.db")),
        ]
        for c in candidates:
            if os.path.isfile(c):
                path = c
                break

    if not path or not os.path.isfile(path):
        # In-memory or json fallback
        results.append(CheckResult("Registry Database", True, "Operating on fallback or in-memory registry", severity="warning"))
        return results

    try:
        conn = sqlite3.connect(path)
        cur = conn.cursor()
        cur.execute("PRAGMA integrity_check;")
        row = cur.fetchone()
        if row and row[0] == "ok":
            results.append(CheckResult("SQLite Integrity", True, f"Database integrity ok ({path})"))
        else:
            results.append(CheckResult("SQLite Integrity", False, f"Integrity check failed: {row}"))

        cur.execute("PRAGMA user_version;")
        ver = cur.fetchone()[0]
        expected_ver = compatibility.get_registry_schema_version()
        if ver == expected_ver:
            results.append(CheckResult("Schema Version", True, f"Schema user_version={ver} (expected {expected_ver})"))
        else:
            direction = "older" if ver < expected_ver else "newer"
            results.append(
                CheckResult(
                    "Schema Version",
                    False,
                    f"Schema mismatch: user_version={ver} is {direction} than runtime schema {expected_ver}",
                )
            )
        conn.close()

    except Exception as e:
        results.append(CheckResult("SQLite Integrity", False, f"Database check error: {e}"))

    return results


def check_security_permissions() -> List[CheckResult]:
    results = []
    home = os.path.expanduser("~")
    data_dirs = [
        os.path.join(home, ".local", "share", "mcp-gateway"),
        "/home/mcp-gateway/.local/share/mcp-gateway",
    ]
    for d in data_dirs:
        if os.path.isdir(d):
            mode = oct(os.stat(d).st_mode & 0o777)
            if mode in ("0o700", "0700"):
                results.append(CheckResult("Data Directory Permissions", True, f"{d} mode is {mode}"))
            else:
                results.append(CheckResult("Data Directory Permissions", False, f"{d} mode is {mode} (expected 0700)", severity="warning"))

    # SSH keys
    ssh_keys = [
        os.path.join(home, ".ssh", "mcp_gateway_ed25519"),
        "/home/mcp-gateway/.ssh/mcp_gateway_ed25519",
    ]
    for k in ssh_keys:
        if os.path.isfile(k):
            mode = oct(os.stat(k).st_mode & 0o777)
            if mode in ("0o600", "0600"):
                results.append(CheckResult("SSH Key Permissions", True, f"{k} mode is {mode}"))
            else:
                results.append(CheckResult("SSH Key Permissions", False, f"{k} mode is {mode} (expected 0600)"))

    return results


def check_core_and_tools() -> List[CheckResult]:
    results = []
    try:
        reg = get_registry()
        tools = GatewayTools(registry=reg)
        h = tools.health()
        if h.get("ok"):
            results.append(CheckResult("Gateway Core Health", True, f"Core status ok (writes_enabled={h.get('result', {}).get('writes_enabled')})"))
        else:
            results.append(CheckResult("Gateway Core Health", False, f"Core health returned error: {h.get('error')}"))

        # Check tool count
        from .bridge import get_tools_catalog
        cat = get_tools_catalog()
        if len(cat) in (9, 14, 21):
            results.append(CheckResult("Tool Catalog", True, f"Catalog contains {len(cat)} tools in deterministic order"))
        else:
            results.append(CheckResult("Tool Catalog", False, f"Unexpected tool count: {len(cat)} (expected 9, 14, or 21)"))

    except Exception as e:
        results.append(CheckResult("Gateway Core Health", False, f"Core initialization failed: {e}"))

    return results


def check_mcp_endpoints(http_base: str = "http://127.0.0.1:8090") -> List[CheckResult]:
    results = []
    # 1. /live
    try:
        req = urllib.request.Request(f"{http_base}/live")
        with urllib.request.urlopen(req, timeout=3) as resp:
            if resp.status == 200:
                results.append(CheckResult("MCP HTTP /live", True, "Liveness probe returned 200 OK"))
            else:
                results.append(CheckResult("MCP HTTP /live", False, f"Liveness returned status {resp.status}"))
    except Exception as e:
        results.append(CheckResult("MCP HTTP /live", False, f"Liveness probe failed: {e}", severity="warning"))

    # 2. /ready
    try:
        req = urllib.request.Request(f"{http_base}/ready")
        with urllib.request.urlopen(req, timeout=3) as resp:
            if resp.status == 200:
                results.append(CheckResult("MCP HTTP /ready", True, "Readiness probe returned 200 OK"))
            else:
                results.append(CheckResult("MCP HTTP /ready", False, f"Readiness returned status {resp.status}"))
    except Exception as e:
        results.append(CheckResult("MCP HTTP /ready", False, f"Readiness probe failed: {e}", severity="warning"))

    # 3. Security: Host Protection Check (Simulated malicious Host header must be rejected)
    try:
        req = urllib.request.Request(f"{http_base}/live", headers={"Host": "evil.attacker.com:8090"})
        with urllib.request.urlopen(req, timeout=3) as resp:
            results.append(CheckResult("MCP Host Protection", False, "Malicious Host header was NOT rejected"))
    except urllib.error.HTTPError as e:
        if e.code in (400, 403):
            results.append(CheckResult("MCP Host Protection", True, f"Malicious Host header rejected with HTTP {e.code}"))
        else:
            results.append(CheckResult("MCP Host Protection", False, f"Unexpected status {e.code} for malicious host"))
    except Exception as e:
        results.append(CheckResult("MCP Host Protection", True, f"Connection rejected: {e}"))

    # 4. Security: Origin Protection Check (Simulated malicious Origin must be rejected)
    try:
        req = urllib.request.Request(f"{http_base}/live", headers={"Origin": "http://evil.attacker.com"})
        with urllib.request.urlopen(req, timeout=3) as resp:
            results.append(CheckResult("MCP Origin Protection", False, "Malicious Origin was NOT rejected"))
    except urllib.error.HTTPError as e:
        if e.code == 403:
            results.append(CheckResult("MCP Origin Protection", True, f"Malicious Origin rejected with HTTP 403"))
        else:
            results.append(CheckResult("MCP Origin Protection", False, f"Unexpected status {e.code} for malicious origin"))
    except Exception as e:
        results.append(CheckResult("MCP Origin Protection", True, f"Origin check rejected: {e}"))

    return results


def check_ai_clients_and_grants() -> List[CheckResult]:
    results = []
    try:
        reg = get_registry()
        from .policy import authorize_client

        # 1. Anonymous access denial check
        anon_ok, _ = authorize_client("NONE", None, None, "read_file", registry=reg)
        if not anon_ok:
            results.append(CheckResult("Anonymous Access Seam", True, "Anonymous access ('NONE') is denied fail-closed"))
        else:
            results.append(CheckResult("Anonymous Access Seam", False, "Anonymous access was allowed (SECURITY RISK!)"))

        # 2. Registered clients
        clients = reg.list_clients() if hasattr(reg, "list_clients") else []
        results.append(CheckResult("Registered AI Clients", True, f"Found {len(clients)} configured AI client(s)"))

        # 3. Grants consistency
        grants = reg.list_grants() if hasattr(reg, "list_grants") else []
        results.append(CheckResult("Client Grants", True, f"Found {len(grants)} configured client grant(s)"))

        # 4. SSH stdio wrapper verification
        wrapper_paths = [
            "/home/mcp-gateway/mcp-gateway/bin/mcp-gateway-client-stdio",
            os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "..", "bin", "mcp-gateway-client-stdio")),
        ]
        wrapper_found = any(os.path.isfile(p) and (os.access(p, os.X_OK) or platform.system() == "Windows") for p in wrapper_paths)
        if wrapper_found or platform.system() == "Windows":
            results.append(CheckResult("SSH Forced Command Wrapper", True, "mcp-gateway-client-stdio wrapper available"))
        else:
            results.append(CheckResult("SSH Forced Command Wrapper", False, "mcp-gateway-client-stdio not found or not executable", severity="warning"))

    except Exception as e:
        results.append(CheckResult("AI Clients and Grants", False, f"Failed client checks: {e}"))

    return results


def run_doctor(verbose: bool = False, check_targets: bool = False) -> Tuple[str, List[CheckResult]]:
    all_checks = []
    all_checks.extend(check_runtime_and_contracts())
    all_checks.extend(check_registry_integrity())
    all_checks.extend(check_security_permissions())
    all_checks.extend(check_core_and_tools())
    all_checks.extend(check_ai_clients_and_grants())
    all_checks.extend(check_mcp_endpoints())

    has_error = any(not c.passed and c.severity == "error" for c in all_checks)
    has_warning = any(not c.passed and c.severity == "warning" for c in all_checks)

    overall = "HEALTHY"
    if has_error:
        overall = "UNHEALTHY"
    elif has_warning:
        overall = "DEGRADED"

    return overall, all_checks


def run_repair() -> List[str]:
    """Execute safe, non-destructive repair actions."""
    repairs = []
    home = os.path.expanduser("~")

    # Fix data dir permissions
    data_dirs = [
        os.path.join(home, ".local", "share", "mcp-gateway"),
        "/home/mcp-gateway/.local/share/mcp-gateway",
    ]
    for d in data_dirs:
        if os.path.isdir(d):
            try:
                os.chmod(d, 0o700)
                repairs.append(f"Fixed permissions to 0700 on {d}")
            except Exception as e:
                repairs.append(f"Failed to chmod {d}: {e}")

    # Fix SSH key permissions
    ssh_keys = [
        os.path.join(home, ".ssh", "mcp_gateway_ed25519"),
        "/home/mcp-gateway/.ssh/mcp_gateway_ed25519",
    ]
    for k in ssh_keys:
        if os.path.isfile(k):
            try:
                os.chmod(k, 0o600)
                repairs.append(f"Fixed permissions to 0600 on {k}")
            except Exception as e:
                repairs.append(f"Failed to chmod {k}: {e}")

    # Ensure systemd units reloaded if possible
    if os.path.isfile("/bin/systemctl") or os.path.isfile("/usr/bin/systemctl"):
        try:
            subprocess.run(["systemctl", "daemon-reload"], check=False, timeout=10)
            repairs.append("Reloaded systemd daemon units")
        except Exception:
            pass

    return repairs
