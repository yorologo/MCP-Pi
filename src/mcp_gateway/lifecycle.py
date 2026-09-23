"""Lifecycle management for MCP Gateway: setup, backup, restore, update, rollback, uninstall."""

import hashlib
import json
import os
import platform
import shutil
import sqlite3
import subprocess
import sys
import time
from typing import Any, Dict, List, Optional, Tuple

from . import compatibility
from .doctor import run_doctor


def get_paths() -> Dict[str, str]:
    home = os.path.expanduser("~")
    base_install = os.environ.get("MCP_GATEWAY_HOME", "/home/mcp-gateway")
    return {
        "home": base_install,
        "releases": os.path.join(base_install, "releases"),
        "current": os.path.join(base_install, "current"),
        "previous": os.path.join(base_install, "previous"),
        "data": os.path.join(base_install, ".local", "share", "mcp-gateway"),
        "config": os.path.join(base_install, ".config", "mcp-gateway"),
        "db": os.environ.get("MCP_GATEWAY_DB", os.path.join(base_install, ".local", "share", "mcp-gateway", "gateway.db")),
        "backups": os.path.join(base_install, ".local", "share", "mcp-gateway", "backups"),
    }


def create_manifest(version: Optional[str] = None, output_path: Optional[str] = None) -> Dict[str, Any]:
    compat = compatibility.get_compatibility()
    manifest = {
        "version": version or compat.get("gateway_version", "1.3.5"),
        "architecture": "armv6l",

        "architectures": ["armv6l", "aarch64", "x86_64"],
        "core_api": compat.get("core_api_version", 1),
        "bridge_api": compat.get("bridge_api_version", 1),
        "tool_catalog": compat.get("tool_catalog_version", 3),
        "registry_schema_range": f">={compat.get('registry_schema_version', 1)}",
        "mcp_sdk": compat.get("mcp", {}).get("sdk", "go-sdk"),
        "mcp_sdk_version": compat.get("mcp", {}).get("version", "1.7.0"),
        "mcp_protocol": compat.get("mcp", {}).get("protocol", "2026-07-28"),
        "minimum_python": "3.9",
        "created_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
    }
    if output_path:
        with open(output_path, "w", encoding="utf-8") as f:
            json.dump(manifest, f, indent=2)
    return manifest


def verify_manifest(manifest_path: str) -> Tuple[bool, List[str]]:
    if not os.path.isfile(manifest_path):
        return False, [f"Manifest file not found: {manifest_path}"]

    try:
        with open(manifest_path, "r", encoding="utf-8") as f:
            data = json.load(f)
    except Exception as e:
        return False, [f"Failed to parse manifest: {e}"]

    errors = []
    # Check architecture
    arch = platform.machine()
    norm_arch = "x86_64" if arch in ("AMD64", "amd64") else arch
    allowed_archs = data.get("architectures")
    if allowed_archs:
        if norm_arch not in allowed_archs and "all" not in allowed_archs:
            errors.append(f"Architecture mismatch: package supports {allowed_archs}, system is '{arch}'")
    else:
        m_arch = data.get("architecture")
        if m_arch and m_arch != "all" and m_arch != norm_arch:
            errors.append(f"Architecture mismatch: package is '{m_arch}', system is '{arch}'")

    # Check contract compatibility
    ok, comp_errors = compatibility.verify_compatibility(data)
    if not ok:
        errors.extend(comp_errors)

    return len(errors) == 0, errors


def backup_database(dest_path: Optional[str] = None) -> str:
    paths = get_paths()
    src_db = paths["db"]
    if not os.path.isfile(src_db):
        raise FileNotFoundError(f"Database not found at {src_db}")

    if not dest_path:
        os.makedirs(paths["backups"], exist_ok=True)
        ts = time.strftime("%Y%m%d_%H%M%S")
        dest_path = os.path.join(paths["backups"], f"gateway_backup_{ts}.db")

    os.makedirs(os.path.dirname(dest_path), exist_ok=True)
    src_conn = sqlite3.connect(src_db)
    dst_conn = sqlite3.connect(dest_path)
    with dst_conn:
        src_conn.backup(dst_conn)
    dst_conn.close()
    src_conn.close()
    return dest_path


def restore_database(backup_path: str) -> bool:
    if not os.path.isfile(backup_path):
        raise FileNotFoundError(f"Backup file not found: {backup_path}")

    # Validate backup integrity
    conn = sqlite3.connect(backup_path)
    cur = conn.cursor()
    cur.execute("PRAGMA integrity_check;")
    row = cur.fetchone()
    if not row or row[0] != "ok":
        conn.close()
        raise ValueError(f"Backup file integrity check failed: {row}")

    cur.execute("PRAGMA user_version;")
    ver = cur.fetchone()[0]
    expected_ver = compatibility.get_registry_schema_version()
    if ver < expected_ver:
        conn.close()
        raise ValueError(f"Backup schema version {ver} is incompatible with expected {expected_ver}")
    conn.close()

    paths = get_paths()
    target_db = paths["db"]
    os.makedirs(os.path.dirname(target_db), exist_ok=True)

    # Perform online restore
    src_conn = sqlite3.connect(backup_path)
    dst_conn = sqlite3.connect(target_db)
    with dst_conn:
        src_conn.backup(dst_conn)
    dst_conn.close()
    src_conn.close()
    return True


def verify_checksums(release_dir: str) -> Tuple[bool, List[str]]:
    sums_file = os.path.join(release_dir, "SHA256SUMS")
    if not os.path.isfile(sums_file):
        return True, []  # Optional if no SHA256SUMS file

    errors = []
    with open(sums_file, "r", encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            parts = line.split(None, 1)
            if len(parts) != 2:
                continue
            expected_sha, rel_path = parts[0], parts[1].lstrip("./")
            target_file = os.path.join(release_dir, rel_path)
            if not os.path.isfile(target_file):
                errors.append(f"Missing file listed in SHA256SUMS: {rel_path}")
                continue
            h = hashlib.sha256()
            with open(target_file, "rb") as bf:
                while chunk := bf.read(65536):
                    h.update(chunk)
            actual_sha = h.hexdigest()
            if actual_sha != expected_sha:
                errors.append(f"Checksum mismatch for {rel_path}: expected {expected_sha}, got {actual_sha}")
    return len(errors) == 0, errors


def check_candidate(candidate_path: str) -> Tuple[bool, List[str], Dict]:
    """Validate candidate release manifest and checksums non-destructively."""
    if not os.path.exists(candidate_path):
        return False, [f"Candidate release path does not exist: {candidate_path}"], {}

    staging_dir = candidate_path
    extracted_tmp = None
    if os.path.isfile(candidate_path) and (candidate_path.endswith(".tar.gz") or candidate_path.endswith(".tgz")):
        import tempfile
        import tarfile
        extracted_tmp = tempfile.mkdtemp(prefix="mcp_check_")
        with tarfile.open(candidate_path, "r:*") as tar:
            tar.extractall(extracted_tmp)
        staging_dir = extracted_tmp

    errors = []
    meta = {}
    try:
        manifest_file = os.path.join(staging_dir, "manifest.json")
        if not os.path.isfile(manifest_file):
            return False, ["Candidate release is missing manifest.json"], {}

        ok, errs = verify_manifest(manifest_file)
        if not ok:
            errors.extend(errs)

        with open(manifest_file, "r", encoding="utf-8") as f:
            meta = json.load(f)

        chk_ok, chk_errs = verify_checksums(staging_dir)
        if not chk_ok:
            errors.extend(chk_errs)

        return len(errors) == 0, errors, meta
    finally:
        if extracted_tmp and os.path.isdir(extracted_tmp):
            shutil.rmtree(extracted_tmp, ignore_errors=True)


def update_release(candidate_path: str) -> Tuple[bool, str]:
    """Perform safe release update with backup, manifest, checksums, doctor and rollback."""
    if not os.path.exists(candidate_path):
        return False, f"Candidate release path does not exist: {candidate_path}"

    paths = get_paths()
    staging_dir = candidate_path

    # Extract tarball if compressed archive
    extracted_tmp = None
    if os.path.isfile(candidate_path) and (candidate_path.endswith(".tar.gz") or candidate_path.endswith(".tgz")):
        import tempfile
        import tarfile
        extracted_tmp = tempfile.mkdtemp(prefix="mcp_update_")
        with tarfile.open(candidate_path, "r:*") as tar:
            tar.extractall(extracted_tmp)
        staging_dir = extracted_tmp

    try:
        # 1. Verify manifest and contract compatibility
        manifest_file = os.path.join(staging_dir, "manifest.json")
        if not os.path.isfile(manifest_file):
            return False, "Candidate release is missing manifest.json"

        ok, errs = verify_manifest(manifest_file)
        if not ok:
            return False, f"Candidate compatibility verification failed: {errs}"

        with open(manifest_file, "r", encoding="utf-8") as f:
            m_data = json.load(f)
        version = m_data.get("version", "unknown")

        # 2. Verify checksums if present
        chk_ok, chk_errs = verify_checksums(staging_dir)
        if not chk_ok:
            return False, f"Candidate checksum verification failed: {chk_errs}"

        # 3. Pre-update database backup
        try:
            bak_path = backup_database()
        except Exception as e:
            return False, f"Pre-update database backup failed: {e}"

        # 4. Copy candidate release into releases directory
        os.makedirs(paths["releases"], exist_ok=True)
        rel_target = os.path.join(paths["releases"], version)
        if os.path.exists(rel_target):
            shutil.rmtree(rel_target, ignore_errors=True)
        shutil.copytree(staging_dir, rel_target)

        # 5. Swap current and previous release symlinks
        current = paths["current"]
        previous = paths["previous"]
        old_target = os.path.realpath(current) if (os.path.islink(current) or os.path.isdir(current)) else None

        if old_target and os.path.isdir(old_target):
            temp_prev = previous + ".update_tmp"
            if os.path.lexists(temp_prev):
                os.remove(temp_prev)
            os.symlink(old_target, temp_prev)
            os.replace(temp_prev, previous)

        temp_curr = current + ".update_tmp"
        if os.path.lexists(temp_curr):
            os.remove(temp_curr)
        os.symlink(rel_target, temp_curr)
        os.replace(temp_curr, current)

        # 6. Execute doctor health check on updated release
        doc_status, _ = run_doctor(verbose=False)
        if doc_status == "UNHEALTHY":
            # Auto-rollback to previous
            if old_target:
                os.symlink(old_target, temp_curr)
                os.replace(temp_curr, current)
            return False, f"Doctor check failed (status: {doc_status}) after update. Rolled back to {old_target}."

        # 7. Restart services if on Linux
        if shutil.which("systemctl"):
            subprocess.run(["systemctl", "restart", "mcp-gateway-admin", "mcp-gateway-mcp"], check=False)
        return True, f"Release successfully updated to v{version} (backup at {bak_path})"

    finally:
        if extracted_tmp and os.path.isdir(extracted_tmp):
            shutil.rmtree(extracted_tmp, ignore_errors=True)


def rollback_release() -> Tuple[bool, str]:
    paths = get_paths()

    previous = paths["previous"]
    current = paths["current"]

    if not os.path.islink(previous) and not os.path.isdir(previous):
        return False, "No previous release symlink found for rollback"

    prev_target = os.path.realpath(previous)
    if not os.path.isdir(prev_target):
        return False, f"Previous release target {prev_target} does not exist"

    # Verify compatibility of previous release
    manifest_file = os.path.join(prev_target, "manifest.json")
    if os.path.isfile(manifest_file):
        ok, errs = verify_manifest(manifest_file)
        if not ok:
            return False, f"Cannot rollback to incompatible release: {errs}"

    # Swap symlinks
    temp_link = current + ".rollback_tmp"
    if os.path.lexists(temp_link):
        os.remove(temp_link)
    os.symlink(prev_target, temp_link)
    os.replace(temp_link, current)

    # Restart services
    if shutil.which("systemctl"):
        subprocess.run(["systemctl", "restart", "mcp-gateway-admin", "mcp-gateway-mcp"], check=False)
    return True, f"Successfully rolled back current release to {prev_target}"


def uninstall(purge: bool = False) -> List[str]:
    actions = []
    # 1. Stop and disable services
    for svc in ("mcp-gateway-mcp", "mcp-gateway-admin"):
        try:
            subprocess.run(["systemctl", "stop", svc], check=False)
            subprocess.run(["systemctl", "disable", svc], check=False)
            actions.append(f"Stopped and disabled service {svc}")
        except Exception:
            pass

    # 2. Remove systemd unit files
    for unit in ("/etc/systemd/system/mcp-gateway-mcp.service", "/etc/systemd/system/mcp-gateway-admin.service"):
        if os.path.isfile(unit):
            try:
                os.remove(unit)
                actions.append(f"Removed systemd unit {unit}")
            except Exception:
                pass
    subprocess.run(["systemctl", "daemon-reload"], check=False)

    # 3. Remove CLI symlink if present
    for cli in ("/usr/local/bin/mcp-gateway", "/usr/bin/mcp-gateway"):
        if os.path.islink(cli) or os.path.isfile(cli):
            try:
                os.remove(cli)
                actions.append(f"Removed CLI executable {cli}")
            except Exception:
                pass

    paths = get_paths()
    if purge:
        if os.path.isdir(paths["data"]):
            shutil.rmtree(paths["data"], ignore_errors=True)
            actions.append(f"Purged data directory {paths['data']}")
        if os.path.isdir(paths["config"]):
            shutil.rmtree(paths["config"], ignore_errors=True)
            actions.append(f"Purged config directory {paths['config']}")
    else:
        actions.append(f"Preserved data directory {paths['data']} and config {paths['config']}")

    return actions
