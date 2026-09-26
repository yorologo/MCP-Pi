"""Single source of truth and validation for MCP Gateway contract versioning."""

import json
import os
from typing import Any, Dict, List, Optional, Tuple

_DEFAULT_COMPATIBILITY: Dict[str, Any] = {
    "gateway_version": "1.4.0",
    "core_api_version": 1,

    "bridge_api_version": 1,
    "tool_catalog_version": 4,
    "registry_schema_version": 4,
    "mcp": {
        "sdk": "go-sdk",
        "version": "1.7.0",
        "protocol": "2026-07-28",
        "protocol_legacy": "2025-11-25",
    },
    "runtime": {
        "python": "3.11+",
        "architecture": ["armv6l", "aarch64", "x86_64"],
    },
}

_CACHED_CONFIG: Optional[Dict[str, Any]] = None


def find_compatibility_file() -> Optional[str]:
    """Locate compatibility.json using standard search paths."""
    env_path = os.environ.get("MCP_GATEWAY_COMPATIBILITY_JSON")
    if env_path and os.path.isfile(env_path):
        return env_path

    # Check relative to this file
    base_dir = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
    candidates = [
        os.path.join(base_dir, "compatibility.json"),
        os.path.join(base_dir, "config", "compatibility.json"),
        "/home/mcp-gateway/mcp-gateway/compatibility.json",
        "/etc/mcp-gateway/compatibility.json",
    ]
    for c in candidates:
        if os.path.isfile(c):
            return c
    return None


def get_compatibility() -> Dict[str, Any]:
    """Return the loaded compatibility specification."""
    global _CACHED_CONFIG
    if _CACHED_CONFIG is not None:
        return _CACHED_CONFIG

    file_path = find_compatibility_file()
    if file_path:
        try:
            with open(file_path, "r", encoding="utf-8") as f:
                data = json.load(f)
                _CACHED_CONFIG = data
                return _CACHED_CONFIG
        except Exception:
            pass

    _CACHED_CONFIG = dict(_DEFAULT_COMPATIBILITY)
    return _CACHED_CONFIG


def get_gateway_version() -> str:
    return str(get_compatibility().get("gateway_version", "1.4.0"))



def get_core_api_version() -> int:
    return int(get_compatibility().get("core_api_version", 1))


def get_bridge_api_version() -> int:
    return int(get_compatibility().get("bridge_api_version", 1))


def get_tool_catalog_version() -> int:
    return int(get_compatibility().get("tool_catalog_version", 4))


def get_registry_schema_version() -> int:
    return int(get_compatibility().get("registry_schema_version", 4))


def verify_compatibility(candidate: Dict[str, Any]) -> Tuple[bool, List[str]]:
    """Validate a candidate manifest or version payload against this gateway contract.
    Returns (is_compatible, list_of_reasons_if_incompatible).
    """
    current = get_compatibility()
    errors = []

    # Check core_api
    cand_core = candidate.get("core_api_version") or candidate.get("core_api")
    if cand_core is not None and cand_core != current.get("core_api_version"):
        errors.append(f"Incompatible core_api: expected {current.get('core_api_version')}, got {cand_core}")

    # Check bridge_api
    cand_bridge = candidate.get("bridge_api_version") or candidate.get("bridge_api")
    if cand_bridge is not None and cand_bridge != current.get("bridge_api_version"):
        errors.append(f"Incompatible bridge_api: expected {current.get('bridge_api_version')}, got {cand_bridge}")

    # Check registry_schema_version
    cand_schema = candidate.get("registry_schema_version")
    if cand_schema is not None and cand_schema != current.get("registry_schema_version"):
        errors.append(f"Incompatible registry_schema: expected {current.get('registry_schema_version')}, got {cand_schema}")

    return (len(errors) == 0, errors)
