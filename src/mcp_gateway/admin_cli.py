"""Admin CLI for MCP Gateway."""

import argparse
import getpass
import json
import os
import sqlite3
import sys
import time
from typing import Any, Dict, List, Optional

try:
    from werkzeug.security import generate_password_hash
except ImportError:
    # Fallback to standard hashlib if werkzeug is absent
    import hashlib
    def generate_password_hash(password: str) -> str:
        salt = os.urandom(16).hex()
        digest = hashlib.sha256((salt + password).encode("utf-8")).hexdigest()
        return f"pbkdf2:sha256:{salt}${digest}"

from .config import GatewayConfig
from .registry import SQLiteRegistry, get_default_db_path, import_from_json


def set_password_cmd(username: str, db_path: str, password: Optional[str] = None) -> int:
    """Set password for an admin user using secure getpass without echo."""
    if password is None:
        if sys.stdin.isatty():
            p1 = getpass.getpass(f"Enter password for admin user '{username}': ")
            p2 = getpass.getpass("Confirm password: ")
            if p1 != p2:
                print("Error: Passwords do not match.", file=sys.stderr)
                return 1
            if not p1:
                print("Error: Password cannot be empty.", file=sys.stderr)
                return 1
            password = p1
        else:
            # Read from stdin for non-interactive scripting/testing
            password = sys.stdin.readline().rstrip("\r\n")
            if not password:
                print("Error: Password cannot be empty.", file=sys.stderr)
                return 1

    pwd_hash = generate_password_hash(password)
    registry = SQLiteRegistry(db_path)
    registry.set_admin_password(username, pwd_hash)
    print(f"Password set successfully for user '{username}'.")
    return 0


def backup_cmd(db_path: str, dest_path: Optional[str] = None) -> int:
    """Create a consistent online backup using sqlite3.Connection.backup."""
    if not os.path.exists(db_path):
        print(f"Error: Database file does not exist: {db_path}", file=sys.stderr)
        return 1

    if not dest_path:
        timestamp = time.strftime("%Y%m%d-%H%M%S")
        dest_path = f"{db_path}.backup-{timestamp}"

    dest_dir = os.path.dirname(dest_path)
    if dest_dir:
        os.makedirs(dest_dir, exist_ok=True)

    src_conn = sqlite3.connect(db_path)
    dest_conn = sqlite3.connect(dest_path)
    try:
        src_conn.backup(dest_conn)
        print(f"Backup created successfully: {dest_path}")
        return 0
    except Exception as e:
        print(f"Error during backup: {e}", file=sys.stderr)
        return 1
    finally:
        src_conn.close()
        dest_conn.close()


def export_json_cmd(db_path: str, output_file: Optional[str] = None) -> int:
    """Export sanitized configuration to JSON without secrets, hashes, or sensitive activity."""
    if not os.path.exists(db_path):
        print(f"Error: Database file does not exist: {db_path}", file=sys.stderr)
        return 1

    registry = SQLiteRegistry(db_path)
    targets_list = registry.list_targets()

    sanitized_targets: Dict[str, Any] = {}
    for t_summary in targets_list:
        tid = t_summary["id"]
        # Fetch target details
        with registry._get_conn() as conn:
            cursor = conn.cursor()
            cursor.execute("SELECT * FROM targets WHERE id = ?", (tid,))
            t_row = cursor.fetchone()
            if not t_row:
                continue
            
            t_dict = {
                "display_name": t_row["display_name"],
                "platform": t_row["platform"],
                "host": t_row["host"],
                "port": t_row["port"],
                "user": t_row["user"],
                "ssh_alias": t_row["ssh_alias"],
                "privilege_user": t_row["privilege_user"],
                "privilege_policy": t_row["privilege_policy"],
                "enabled": bool(t_row["enabled"]),
                "projects": {},
            }

            cursor.execute("SELECT * FROM projects WHERE target_id = ?", (tid,))
            for p_row in cursor.fetchall():
                pid = p_row["id"]
                p_dict = {
                    "display_name": p_row["display_name"],
                    "root": p_row["root"],
                    "read": bool(p_row["read_enabled"]),
                    "write": bool(p_row["write_enabled"]),
                    "enabled": bool(p_row["enabled"]),
                    "tasks": {},
                }

                cursor.execute(
                    "SELECT * FROM project_tasks WHERE target_id = ? AND project_id = ?",
                    (tid, pid),
                )
                for task_row in cursor.fetchall():
                    task_name = task_row["task_name"]
                    p_dict["tasks"][task_name] = {
                        "enabled": bool(task_row["enabled"]),
                        "argv": json.loads(task_row["argv_json"]),
                        "timeout": task_row["timeout"],
                    }

                t_dict["projects"][pid] = p_dict

            sanitized_targets[tid] = t_dict

    clients = registry.list_clients()
    sanitized_clients = [
        {
            "id": c["id"],
            "display_name": c["display_name"],
            "provider": c["provider"],
            "protocol": c["protocol"],
            "enabled": c["enabled"],
            "notes": c.get("notes", ""),
        }
        for c in clients
    ]

    # Non-secret settings
    allowed_settings = [
        "gateway_enabled",
        "default_timeout",
        "max_output_bytes",
        "max_file_read_bytes",
        "activity_retention",
    ]
    sanitized_settings: Dict[str, Any] = {}
    for key in allowed_settings:
        val = registry.get_setting(key)
        if val is not None:
            sanitized_settings[key] = val

    export_data = {
        "version": "1.0",
        "exported_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "targets": sanitized_targets,
        "ai_clients": sanitized_clients,
        "settings": sanitized_settings,
    }

    formatted = json.dumps(export_data, indent=2)
    if output_file:
        with open(output_file, "w", encoding="utf-8") as f:
            f.write(formatted + "\n")
        print(f"Sanitized export saved to: {output_file}")
    else:
        print(formatted)
    return 0


def import_json_cmd(json_file: str, db_path: str) -> int:
    """Import JSON configuration into SQLite database."""
    if not os.path.exists(json_file):
        print(f"Error: JSON file not found: {json_file}", file=sys.stderr)
        return 1

    try:
        cfg = GatewayConfig.load(json_file)
        registry = SQLiteRegistry(db_path)
        summary = import_from_json(cfg, registry)
        print(f"Import completed successfully from {json_file}:")
        print(f"  Targets imported: {summary['targets_imported']}")
        print(f"  Projects imported: {summary['projects_imported']}")
        print(f"  Tasks imported: {summary['tasks_imported']}")
        return 0
    except Exception as e:
        print(f"Error importing JSON: {e}", file=sys.stderr)
        return 1


def main(args_list: Optional[List[str]] = None) -> int:
    parser = argparse.ArgumentParser(description="MCP Gateway Administrative CLI")
    parser.add_argument("--db", default=None, help="Path to SQLite database")

    subparsers = parser.add_subparsers(dest="command", required=True)

    # set-password
    p_pwd = subparsers.add_parser("set-password", help="Set password for an admin user")
    p_pwd.add_argument("username", default="admin", nargs="?", help="Admin username (default: admin)")

    # backup
    p_backup = subparsers.add_parser("backup", help="Create a consistent online backup of the database")
    p_backup.add_argument("dest", nargs="?", default=None, help="Destination backup file path")

    # export-json
    p_export = subparsers.add_parser("export-json", help="Export sanitized configuration to JSON")
    p_export.add_argument("output", nargs="?", default=None, help="Output JSON file path (default stdout)")

    # import-json
    p_import = subparsers.add_parser("import-json", help="Import configuration from a JSON file")
    p_import.add_argument("file", help="Source JSON configuration file")

    parsed = parser.parse_args(args_list)
    db_path = parsed.db or get_default_db_path()

    if parsed.command == "set-password":
        return set_password_cmd(parsed.username, db_path)
    elif parsed.command == "backup":
        return backup_cmd(db_path, parsed.dest)
    elif parsed.command == "export-json":
        return export_json_cmd(db_path, parsed.output)
    elif parsed.command == "import-json":
        return import_json_cmd(parsed.file, db_path)

    return 1


if __name__ == "__main__":
    sys.exit(main())
