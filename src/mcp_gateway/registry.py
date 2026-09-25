import abc
import contextlib
import json
import os
import sqlite3
from typing import Any, Dict, List, Optional
import uuid
import time

from .config import GatewayConfig, ConfigError
from .policy import PRIVILEGE_APPROVAL_TTL_SECONDS, normalize_privilege_policy
from .schema import init_db

class RegistryBase(abc.ABC):
    """Abstract base class for MCP Gateway registries."""

    @abc.abstractmethod
    def list_targets(self) -> List[Dict]:
        pass

    @abc.abstractmethod
    def get_target(self, target_id: str) -> Dict:
        pass

    @abc.abstractmethod
    def list_projects(self, target_id: str = None) -> List[Dict]:
        pass

    @abc.abstractmethod
    def get_project(self, target_id: str, project_id: str) -> Dict:
        pass

    @abc.abstractmethod
    def list_clients(self) -> List[Dict]:
        pass

    @abc.abstractmethod
    def get_client(self, client_id: str) -> Dict:
        pass

    @abc.abstractmethod
    def add_target(self, data: Dict) -> str:
        pass

    @abc.abstractmethod
    def update_target(self, target_id: str, data: Dict) -> None:
        pass

    @abc.abstractmethod
    def add_project(self, target_id: str, data: Dict) -> str:
        pass

    @abc.abstractmethod
    def update_project(self, target_id: str, project_id: str, data: Dict) -> None:
        pass

    @abc.abstractmethod
    def add_client(self, data: Dict) -> str:
        pass

    @abc.abstractmethod
    def update_client(self, client_id: str, data: Dict) -> None:
        pass

    @abc.abstractmethod
    def list_grants(self, client_id: Optional[str] = None) -> List[Dict]:
        pass

    @abc.abstractmethod
    def get_grant(self, grant_id: int) -> Dict:
        pass

    @abc.abstractmethod
    def add_grant(self, data: Dict) -> int:
        pass

    @abc.abstractmethod
    def update_grant(self, grant_id: int, data: Dict) -> None:
        pass

    @abc.abstractmethod
    def delete_grant(self, grant_id: int) -> None:
        pass

    @abc.abstractmethod
    def get_privilege_approval(self, target_id: str) -> Optional[Dict]:
        pass

    @abc.abstractmethod
    def set_privilege_approval(
        self,
        target_id: str,
        policy: str,
        client_id: str,
        project_id: str,
        boot_id: str = "",
    ) -> None:
        pass

    @abc.abstractmethod
    def clear_privilege_approval(self, target_id: str) -> None:
        pass

    @abc.abstractmethod
    def consume_privilege_approval(
        self,
        target_id: str,
        policy: str,
        client_id: str,
        project_id: str,
        boot_id: str = "",
        max_age_seconds: int = PRIVILEGE_APPROVAL_TTL_SECONDS,
    ) -> bool:
        pass

    def get_client_grants(self, client_id: str) -> List[Dict]:
        return [g for g in self.list_grants(client_id=client_id) if g.get("enabled", True)]

    @abc.abstractmethod
    def get_setting(self, key: str, default=None) -> Any:
        pass

    @abc.abstractmethod
    def set_setting(self, key: str, value: Any) -> None:
        pass

    @abc.abstractmethod
    def record_activity(self, entry: Dict) -> None:
        pass

    @abc.abstractmethod
    def list_activity(self, limit: int = 50, offset: int = 0) -> List[Dict]:
        pass

    @abc.abstractmethod
    def get_admin_user(self, username: str) -> Optional[Dict]:
        pass

    @abc.abstractmethod
    def set_admin_password(self, username: str, password_hash: str) -> None:
        pass

    @abc.abstractmethod
    def update_admin_login(self, username: str) -> None:
        pass

    @abc.abstractmethod
    def get_activity_count(self) -> int:
        pass

    @abc.abstractmethod
    def prune_activity(self, keep: int) -> int:
        pass

    @property
    def target_count(self) -> int:
        return len(self.list_targets())


class JsonRegistry(RegistryBase):
    """Registry adapter that wraps a GatewayConfig for read-only target/project access."""

    def __init__(self, config: GatewayConfig):
        self.config = config
        self._settings: Dict[str, Any] = {}
        self._clients: Dict[str, Dict] = {}
        self._grants: Dict[int, Dict] = {}
        self._next_grant_id: int = 1
        self._privilege_approvals: Dict[str, Dict] = {}
        self._activity: List[Dict] = []
        self._admin_users: Dict[str, Dict] = {}

    def _clear_privilege_approvals_for_client(self, client_id: str) -> None:
        client_id = str(client_id or "").strip()
        if not client_id:
            return
        stale = [
            target_id
            for target_id, approval in self._privilege_approvals.items()
            if approval.get("client_id") == client_id
        ]
        for target_id in stale:
            self._privilege_approvals.pop(target_id, None)

    def list_targets(self) -> List[Dict]:
        return self.config.list_targets()

    def get_target(self, target_id: str) -> Dict:
        return self.config.get_target(target_id)

    def list_projects(self, target_id: Optional[str] = None) -> List[Dict]:
        projects = []
        if target_id:
            target = self.config.get_target(target_id)
            for pid, pinfo in target.get("projects", {}).items():
                projects.append({"target_id": target_id, "id": pid, **pinfo})
        else:
            for t in self.config.list_targets():
                t_id = t["id"]
                try:
                    target_data = self.config.get_target(t_id)
                    for pid, pinfo in target_data.get("projects", {}).items():
                        projects.append({"target_id": t_id, "id": pid, **pinfo})
                except ConfigError:
                    pass
        return projects

    def get_project(self, target_id: str, project_id: str) -> Dict:
        return self.config.get_project(target_id, project_id)

    def list_clients(self) -> List[Dict]:
        return list(self._clients.values())

    def get_client(self, client_id: str) -> Dict:
        if client_id not in self._clients:
            raise KeyError(f"Client {client_id} not found")
        return self._clients[client_id]

    def add_target(self, data: Dict) -> str:
        raise NotImplementedError("JsonRegistry is read-only for targets/projects")

    def update_target(self, target_id: str, data: Dict) -> None:
        raise NotImplementedError("JsonRegistry is read-only for targets/projects")

    def add_project(self, target_id: str, data: Dict) -> str:
        raise NotImplementedError("JsonRegistry is read-only for targets/projects")

    def update_project(self, target_id: str, project_id: str, data: Dict) -> None:
        raise NotImplementedError("JsonRegistry is read-only for targets/projects")

    def add_client(self, data: Dict) -> str:
        client_id = data.get("id") or str(uuid.uuid4())
        client_data = {"id": client_id, **data}
        self._clients[client_id] = client_data
        return client_id

    def update_client(self, client_id: str, data: Dict) -> None:
        if client_id not in self._clients:
            raise KeyError(f"Client {client_id} not found")
        previous_enabled = bool(self._clients[client_id].get("enabled", True))
        self._clients[client_id].update(data)
        if "enabled" in data and bool(data["enabled"]) != previous_enabled:
            self._clear_privilege_approvals_for_client(client_id)

    def list_grants(self, client_id: Optional[str] = None) -> List[Dict]:
        if client_id:
            return [g for g in self._grants.values() if g.get("client_id") == client_id]
        return list(self._grants.values())

    def get_grant(self, grant_id: int) -> Dict:
        if grant_id not in self._grants:
            raise KeyError(f"Grant {grant_id} not found")
        return self._grants[grant_id]

    def add_grant(self, data: Dict) -> int:
        gid = self._next_grant_id
        self._next_grant_id += 1
        gdata = {"id": gid, "enabled": True, **data}
        self._grants[gid] = gdata
        self._clear_privilege_approvals_for_client(gdata.get("client_id", ""))
        return gid

    def update_grant(self, grant_id: int, data: Dict) -> None:
        if grant_id not in self._grants:
            raise KeyError(f"Grant {grant_id} not found")
        old_client_id = self._grants[grant_id].get("client_id", "")
        self._grants[grant_id].update(data)
        new_client_id = self._grants[grant_id].get("client_id", "")
        self._clear_privilege_approvals_for_client(old_client_id)
        if new_client_id != old_client_id:
            self._clear_privilege_approvals_for_client(new_client_id)

    def delete_grant(self, grant_id: int) -> None:
        if grant_id not in self._grants:
            raise KeyError(f"Grant {grant_id} not found")
        client_id = self._grants[grant_id].get("client_id", "")
        del self._grants[grant_id]
        self._clear_privilege_approvals_for_client(client_id)

    def get_privilege_approval(self, target_id: str) -> Optional[Dict]:
        approval = self._privilege_approvals.get(target_id)
        return dict(approval) if approval else None

    def set_privilege_approval(
        self,
        target_id: str,
        policy: str,
        client_id: str,
        project_id: str,
        boot_id: str = "",
    ) -> None:
        policy = normalize_privilege_policy(policy)
        client_id = str(client_id or "").strip()
        project_id = str(project_id or "").strip()
        if policy not in ("ask_always", "ask_once_per_boot"):
            raise ValueError(f"Policy '{policy}' does not use cached approval")
        if not client_id or not project_id:
            raise ValueError("Privilege approval requires client and project scope")
        self._privilege_approvals[target_id] = {
            "target_id": target_id,
            "policy": policy,
            "client_id": client_id,
            "project_id": project_id,
            "boot_id": str(boot_id or ""),
            "approved_at": time.time(),
        }

    def clear_privilege_approval(self, target_id: str) -> None:
        self._privilege_approvals.pop(target_id, None)

    def consume_privilege_approval(
        self,
        target_id: str,
        policy: str,
        client_id: str,
        project_id: str,
        boot_id: str = "",
        max_age_seconds: int = PRIVILEGE_APPROVAL_TTL_SECONDS,
    ) -> bool:
        policy = normalize_privilege_policy(policy)
        approval = self._privilege_approvals.get(target_id)
        if not approval or approval.get("policy") != policy:
            return False
        if approval.get("client_id") != str(client_id or "").strip():
            return False
        if approval.get("project_id") != str(project_id or "").strip():
            return False
        if policy == "ask_once_per_boot":
            if bool(boot_id) and approval.get("boot_id") == boot_id:
                return True
            self._privilege_approvals.pop(target_id, None)
            return False
        if policy == "ask_always":
            try:
                age = time.time() - float(approval.get("approved_at", 0))
            except (TypeError, ValueError):
                age = max_age_seconds + 1
            if age < 0 or age > max_age_seconds:
                self._privilege_approvals.pop(target_id, None)
                return False
            self._privilege_approvals.pop(target_id, None)
            return True
        return False

    def get_setting(self, key: str, default=None) -> Any:
        return self._settings.get(key, default)

    def set_setting(self, key: str, value: Any) -> None:
        self._settings[key] = value

    def record_activity(self, entry: Dict) -> None:
        entry_with_id = {"id": str(uuid.uuid4()), "timestamp": time.time(), **entry}
        self._activity.append(entry_with_id)

    def list_activity(self, limit: int = 50, offset: int = 0) -> List[Dict]:
        return self._activity[offset:offset+limit]

    def get_admin_user(self, username: str) -> Optional[Dict]:
        return self._admin_users.get(username)

    def set_admin_password(self, username: str, password_hash: str) -> None:
        if username not in self._admin_users:
            self._admin_users[username] = {"username": username}
        self._admin_users[username]["password_hash"] = password_hash

    def update_admin_login(self, username: str) -> None:
        if username in self._admin_users:
            self._admin_users[username]["last_login"] = time.time()

    def get_activity_count(self) -> int:
        return len(self._activity)

    def prune_activity(self, keep: int) -> int:
        if len(self._activity) <= keep:
            return 0
        pruned = len(self._activity) - keep
        self._activity = self._activity[-keep:]
        return pruned

    @property
    def target_count(self) -> int:
        return self.config.target_count


class SQLiteRegistry(RegistryBase):
    def __init__(self, db_path: str):
        self.db_path = db_path
        init_db(db_path)

    @contextlib.contextmanager
    def _get_conn(self):
        conn = sqlite3.connect(self.db_path)
        conn.row_factory = sqlite3.Row
        try:
            yield conn
        finally:
            conn.close()

    @staticmethod
    def _clear_privilege_approvals_in_conn(
        conn: sqlite3.Connection,
        *,
        target_id: Optional[str] = None,
        client_id: Optional[str] = None,
        project_id: Optional[str] = None,
    ) -> None:
        clauses = []
        values = []
        if target_id is not None:
            clauses.append("target_id = ?")
            values.append(target_id)
        if client_id is not None:
            clauses.append("client_id = ?")
            values.append(client_id)
        if project_id is not None:
            clauses.append("project_id = ?")
            values.append(project_id)
        if not clauses:
            return
        conn.execute(
            f"DELETE FROM privilege_approvals WHERE {' AND '.join(clauses)}",
            tuple(values),
        )

    def list_targets(self) -> List[Dict]:
        with self._get_conn() as conn:
            cursor = conn.cursor()
            cursor.execute("SELECT id, display_name, platform, privilege_policy, enabled FROM targets")
            results = []
            for row in cursor.fetchall():
                t = dict(row)
                t["enabled"] = bool(t["enabled"])
                
                # Get projects
                cursor.execute("SELECT id FROM projects WHERE target_id = ?", (t["id"],))
                project_ids = [p["id"] for p in cursor.fetchall()]
                t["project_count"] = len(project_ids)
                t["projects"] = sorted(project_ids)
                
                results.append(t)
            return results

    def get_target(self, target_id: str) -> Dict:
        with self._get_conn() as conn:
            cursor = conn.cursor()
            cursor.execute("SELECT * FROM targets WHERE id = ?", (target_id,))
            row = cursor.fetchone()
            if not row:
                raise ConfigError(f"Target '{target_id}' is not configured", code="UNKNOWN_TARGET")
            
            target = dict(row)
            target["enabled"] = bool(target["enabled"])
            
            if not target["enabled"]:
                raise ConfigError(f"Target '{target_id}' is disabled", code="TARGET_DISABLED")

            # Remove db-only fields (retain id for cryptographic and transport identity)
            target["id"] = target_id
            target.pop("created_at", None)
            target.pop("updated_at", None)
            target.pop("display_name", None)

            # Build projects dict
            cursor.execute("SELECT * FROM projects WHERE target_id = ?", (target_id,))
            projects_data = {}
            for prow in cursor.fetchall():
                pid = prow["id"]
                pdict = {
                    "root": prow["root"],
                    "read": bool(prow["read_enabled"]),
                    "write": bool(prow["write_enabled"]),
                    "enabled": bool(prow["enabled"]),
                    "tasks": {}
                }
                
                cursor.execute("SELECT * FROM project_tasks WHERE target_id = ? AND project_id = ?", (target_id, pid))
                for trow in cursor.fetchall():
                    task_name = trow["task_name"]
                    pdict["tasks"][task_name] = {
                        "enabled": bool(trow["enabled"]),
                        "argv": json.loads(trow["argv_json"]),
                        "timeout": trow["timeout"]
                    }
                
                projects_data[pid] = pdict
                
            target["projects"] = projects_data
            return target

    def list_projects(self, target_id: str = None) -> List[Dict]:
        with self._get_conn() as conn:
            cursor = conn.cursor()
            if target_id:
                cursor.execute("SELECT * FROM projects WHERE target_id = ?", (target_id,))
            else:
                cursor.execute("SELECT * FROM projects")
                
            projects = []
            for prow in cursor.fetchall():
                pdict = dict(prow)
                pdict["read"] = bool(pdict.pop("read_enabled"))
                pdict["write"] = bool(pdict.pop("write_enabled"))
                pdict["enabled"] = bool(pdict["enabled"])
                
                # Fetch tasks
                cursor.execute("SELECT * FROM project_tasks WHERE target_id = ? AND project_id = ?", (pdict["target_id"], pdict["id"]))
                tasks = {}
                for trow in cursor.fetchall():
                    tasks[trow["task_name"]] = {
                        "enabled": bool(trow["enabled"]),
                        "argv": json.loads(trow["argv_json"]),
                        "timeout": trow["timeout"]
                    }
                pdict["tasks"] = tasks
                
                # Remove db-only fields not in target output schema
                pdict.pop("created_at", None)
                pdict.pop("updated_at", None)
                pdict.pop("display_name", None)
                
                projects.append(pdict)
            return projects

    def get_project(self, target_id: str, project_id: str) -> Dict:
        target = self.get_target(target_id)
        projects = target.get("projects", {})
        if project_id not in projects:
            raise ConfigError(
                f"Project '{project_id}' is not configured under target '{target_id}'",
                code="UNKNOWN_PROJECT",
            )
        project = projects[project_id]
        if not project.get("enabled", True):
            raise ConfigError(
                f"Project '{project_id}' in target '{target_id}' is disabled",
                code="PROJECT_DISABLED",
            )
        return project

    def list_clients(self) -> List[Dict]:
        with self._get_conn() as conn:
            cursor = conn.cursor()
            cursor.execute("SELECT * FROM ai_clients")
            clients = []
            for row in cursor.fetchall():
                c = dict(row)
                c["enabled"] = bool(c["enabled"])
                clients.append(c)
            return clients

    def get_client(self, client_id: str) -> Dict:
        with self._get_conn() as conn:
            cursor = conn.cursor()
            cursor.execute("SELECT * FROM ai_clients WHERE id = ?", (client_id,))
            row = cursor.fetchone()
            if not row:
                raise KeyError(f"Client {client_id} not found")
            c = dict(row)
            c["enabled"] = bool(c["enabled"])
            return c

    def add_target(self, data: Dict) -> str:
        tid = data.get("id") or str(uuid.uuid4())
        with self._get_conn() as conn:
            cursor = conn.cursor()
            cursor.execute('''
                INSERT INTO targets (id, display_name, platform, host, port, user, ssh_alias, privilege_user, privilege_policy, enabled)
                VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            ''', (
                tid,
                data.get("display_name", tid),
                data.get("platform", "linux"),
                data.get("host", ""),
                data.get("port", 22),
                data.get("user", ""),
                data.get("ssh_alias", ""),
                str(data.get("privilege_user", "") or "").strip(),
                normalize_privilege_policy(data.get("privilege_policy", "never")),
                int(data.get("enabled", True))
            ))
            conn.commit()
        return tid

    def update_target(self, target_id: str, data: Dict) -> None:
        set_clauses = []
        values = []
        normalized_updates: Dict[str, Any] = {}
        for key in [
            "display_name",
            "platform",
            "host",
            "port",
            "user",
            "ssh_alias",
            "privilege_user",
            "privilege_policy",
            "enabled",
        ]:
            if key not in data:
                continue
            val = data[key]
            if key == "enabled":
                val = int(bool(val))
            elif key == "privilege_policy":
                val = normalize_privilege_policy(val)
            elif key == "privilege_user":
                val = str(val or "").strip()
            elif key == "port":
                val = int(val)
            normalized_updates[key] = val
            set_clauses.append(f"{key} = ?")
            values.append(val)

        if not set_clauses:
            return

        set_clauses.append("updated_at = datetime('now')")
        values.append(target_id)
        query = f"UPDATE targets SET {', '.join(set_clauses)} WHERE id = ?"
        security_fields = {
            "platform",
            "host",
            "port",
            "user",
            "ssh_alias",
            "privilege_user",
            "privilege_policy",
            "enabled",
        }

        with self._get_conn() as conn:
            cursor = conn.cursor()
            cursor.execute("SELECT * FROM targets WHERE id = ?", (target_id,))
            current = cursor.fetchone()
            if not current:
                raise KeyError(f"Target {target_id} not found")

            security_context_changed = any(
                key in normalized_updates and normalized_updates[key] != current[key]
                for key in security_fields
            )

            cursor.execute(query, tuple(values))
            if security_context_changed:
                self._clear_privilege_approvals_in_conn(conn, target_id=target_id)
            conn.commit()

    def add_project(self, target_id: str, data: Dict) -> str:
        pid = data.get("id") or str(uuid.uuid4())
        with self._get_conn() as conn:
            cursor = conn.cursor()
            cursor.execute("SELECT id FROM targets WHERE id = ?", (target_id,))
            if not cursor.fetchone():
                raise KeyError(f"Target {target_id} not found")
                
            cursor.execute('''
                INSERT INTO projects (id, target_id, display_name, root, read_enabled, write_enabled, enabled)
                VALUES (?, ?, ?, ?, ?, ?, ?)
            ''', (
                pid,
                target_id,
                data.get("display_name", pid),
                data.get("root", "/"),
                int(data.get("read", True)),
                int(data.get("write", False)),
                int(data.get("enabled", True))
            ))
            
            tasks = data.get("tasks", {})
            for task_name, task_info in tasks.items():
                cursor.execute('''
                    INSERT INTO project_tasks (target_id, project_id, task_name, argv_json, timeout, enabled)
                    VALUES (?, ?, ?, ?, ?, ?)
                ''', (
                    target_id,
                    pid,
                    task_name,
                    json.dumps(task_info.get("argv", [])),
                    task_info.get("timeout", 30),
                    int(task_info.get("enabled", True))
                ))
            
            conn.commit()
        return pid

    def update_project(self, target_id: str, project_id: str, data: Dict) -> None:
        set_clauses = []
        values = []
        normalized_updates: Dict[str, Any] = {}
        mapping = {
            "display_name": "display_name",
            "root": "root",
            "read": "read_enabled",
            "write": "write_enabled",
            "enabled": "enabled",
        }

        for key, db_col in mapping.items():
            if key not in data:
                continue
            val = data[key]
            if key in {"read", "write", "enabled"}:
                val = int(bool(val))
            normalized_updates[db_col] = val
            set_clauses.append(f"{db_col} = ?")
            values.append(val)

        with self._get_conn() as conn:
            cursor = conn.cursor()
            cursor.execute(
                "SELECT * FROM projects WHERE target_id = ? AND id = ?",
                (target_id, project_id),
            )
            current = cursor.fetchone()
            if not current:
                raise KeyError(f"Project {project_id} in {target_id} not found")

            security_context_changed = any(
                col != "display_name"
                and normalized_updates.get(col, current[col]) != current[col]
                for col in normalized_updates
            )
            if "tasks" in data:
                security_context_changed = True

            if set_clauses:
                clauses = list(set_clauses)
                clauses.append("updated_at = datetime('now')")
                query_values = list(values)
                query_values.extend([target_id, project_id])
                query = (
                    f"UPDATE projects SET {', '.join(clauses)} "
                    "WHERE target_id = ? AND id = ?"
                )
                cursor.execute(query, tuple(query_values))

            if "tasks" in data:
                cursor.execute(
                    "DELETE FROM project_tasks WHERE target_id = ? AND project_id = ?",
                    (target_id, project_id),
                )
                for task_name, task_info in data["tasks"].items():
                    cursor.execute(
                        """
                        INSERT INTO project_tasks (
                            target_id, project_id, task_name, argv_json, timeout, enabled
                        )
                        VALUES (?, ?, ?, ?, ?, ?)
                        """,
                        (
                            target_id,
                            project_id,
                            task_name,
                            json.dumps(task_info.get("argv", [])),
                            task_info.get("timeout", 30),
                            int(task_info.get("enabled", True)),
                        ),
                    )

            if security_context_changed:
                self._clear_privilege_approvals_in_conn(
                    conn,
                    target_id=target_id,
                    project_id=project_id,
                )
            conn.commit()

    def add_client(self, data: Dict) -> str:
        cid = data.get("id") or str(uuid.uuid4())
        with self._get_conn() as conn:
            cursor = conn.cursor()
            cursor.execute('''
                INSERT INTO ai_clients (id, display_name, provider, protocol, enabled, notes)
                VALUES (?, ?, ?, ?, ?, ?)
            ''', (
                cid,
                data.get("display_name", cid),
                data.get("provider", ""),
                data.get("protocol", ""),
                int(data.get("enabled", True)),
                data.get("notes", "")
            ))
            conn.commit()
        return cid

    def update_client(self, client_id: str, data: Dict) -> None:
        set_clauses = []
        values = []
        normalized_updates: Dict[str, Any] = {}
        for key in ["display_name", "provider", "protocol", "enabled", "notes"]:
            if key not in data:
                continue
            val = data[key]
            if key == "enabled":
                val = int(bool(val))
            normalized_updates[key] = val
            set_clauses.append(f"{key} = ?")
            values.append(val)

        if not set_clauses:
            return

        with self._get_conn() as conn:
            cursor = conn.cursor()
            cursor.execute("SELECT * FROM ai_clients WHERE id = ?", (client_id,))
            current = cursor.fetchone()
            if not current:
                raise KeyError(f"Client {client_id} not found")

            enabled_changed = (
                "enabled" in normalized_updates
                and normalized_updates["enabled"] != current["enabled"]
            )

            clauses = list(set_clauses)
            clauses.append("updated_at = datetime('now')")
            query_values = list(values)
            query_values.append(client_id)
            query = f"UPDATE ai_clients SET {', '.join(clauses)} WHERE id = ?"
            cursor.execute(query, tuple(query_values))
            if enabled_changed:
                self._clear_privilege_approvals_in_conn(conn, client_id=client_id)
            conn.commit()

    def list_grants(self, client_id: Optional[str] = None) -> List[Dict]:
        with self._get_conn() as conn:
            cursor = conn.cursor()
            if client_id:
                cursor.execute("SELECT * FROM grants WHERE client_id = ?", (client_id,))
            else:
                cursor.execute("SELECT * FROM grants")
            grants = []
            for row in cursor.fetchall():
                g = dict(row)
                g["enabled"] = bool(g["enabled"])
                grants.append(g)
            return grants

    def get_grant(self, grant_id: int) -> Dict:
        with self._get_conn() as conn:
            cursor = conn.cursor()
            cursor.execute("SELECT * FROM grants WHERE id = ?", (grant_id,))
            row = cursor.fetchone()
            if not row:
                raise KeyError(f"Grant {grant_id} not found")
            g = dict(row)
            g["enabled"] = bool(g["enabled"])
            return g

    def add_grant(self, data: Dict) -> int:
        client_id = data["client_id"]
        with self._get_conn() as conn:
            cursor = conn.cursor()
            cursor.execute(
                """
                INSERT INTO grants (client_id, target_id, project_id, capability, enabled)
                VALUES (?, ?, ?, ?, ?)
                """,
                (
                    client_id,
                    data.get("target_id", "*"),
                    data.get("project_id", "*"),
                    data.get("capability", "read"),
                    1 if data.get("enabled", True) else 0,
                ),
            )
            gid = cursor.lastrowid
            self._clear_privilege_approvals_in_conn(conn, client_id=client_id)
            conn.commit()
            return gid

    def update_grant(self, grant_id: int, data: Dict) -> None:
        set_clauses = []
        values = []
        for col in ["client_id", "target_id", "project_id", "capability"]:
            if col in data:
                set_clauses.append(f"{col} = ?")
                values.append(data[col])
        if "enabled" in data:
            set_clauses.append("enabled = ?")
            values.append(1 if data["enabled"] else 0)

        if not set_clauses:
            return

        with self._get_conn() as conn:
            cursor = conn.cursor()
            cursor.execute("SELECT * FROM grants WHERE id = ?", (grant_id,))
            current = cursor.fetchone()
            if not current:
                raise KeyError(f"Grant {grant_id} not found")

            values.append(grant_id)
            query = f"UPDATE grants SET {', '.join(set_clauses)} WHERE id = ?"
            cursor.execute(query, tuple(values))

            old_client_id = current["client_id"]
            new_client_id = str(data.get("client_id", old_client_id))
            self._clear_privilege_approvals_in_conn(
                conn, client_id=old_client_id
            )
            if new_client_id != old_client_id:
                self._clear_privilege_approvals_in_conn(
                    conn, client_id=new_client_id
                )
            conn.commit()

    def delete_grant(self, grant_id: int) -> None:
        with self._get_conn() as conn:
            cursor = conn.cursor()
            cursor.execute("SELECT client_id FROM grants WHERE id = ?", (grant_id,))
            current = cursor.fetchone()
            if not current:
                raise KeyError(f"Grant {grant_id} not found")
            cursor.execute("DELETE FROM grants WHERE id = ?", (grant_id,))
            self._clear_privilege_approvals_in_conn(
                conn, client_id=current["client_id"]
            )
            conn.commit()

    def get_privilege_approval(self, target_id: str) -> Optional[Dict]:
        with self._get_conn() as conn:
            cursor = conn.cursor()
            cursor.execute("SELECT * FROM privilege_approvals WHERE target_id = ?", (target_id,))
            row = cursor.fetchone()
            return dict(row) if row else None

    def set_privilege_approval(
        self,
        target_id: str,
        policy: str,
        client_id: str,
        project_id: str,
        boot_id: str = "",
    ) -> None:
        policy = normalize_privilege_policy(policy)
        client_id = str(client_id or "").strip()
        project_id = str(project_id or "").strip()
        if policy not in ("ask_always", "ask_once_per_boot"):
            raise ValueError(f"Policy '{policy}' does not use cached approval")
        if not client_id or not project_id:
            raise ValueError("Privilege approval requires client and project scope")
        with self._get_conn() as conn:
            cursor = conn.cursor()
            cursor.execute(
                """
                INSERT INTO privilege_approvals (
                    target_id, policy, client_id, project_id, boot_id, approved_at
                )
                VALUES (?, ?, ?, ?, ?, ?)
                ON CONFLICT(target_id) DO UPDATE SET
                    policy = excluded.policy,
                    client_id = excluded.client_id,
                    project_id = excluded.project_id,
                    boot_id = excluded.boot_id,
                    approved_at = excluded.approved_at
                """,
                (
                    target_id,
                    policy,
                    client_id,
                    project_id,
                    str(boot_id or ""),
                    time.time(),
                ),
            )
            conn.commit()

    def clear_privilege_approval(self, target_id: str) -> None:
        with self._get_conn() as conn:
            conn.execute("DELETE FROM privilege_approvals WHERE target_id = ?", (target_id,))
            conn.commit()

    def consume_privilege_approval(
        self,
        target_id: str,
        policy: str,
        client_id: str,
        project_id: str,
        boot_id: str = "",
        max_age_seconds: int = PRIVILEGE_APPROVAL_TTL_SECONDS,
    ) -> bool:
        policy = normalize_privilege_policy(policy)
        client_id = str(client_id or "").strip()
        project_id = str(project_id or "").strip()
        with self._get_conn() as conn:
            cursor = conn.cursor()
            cursor.execute("BEGIN IMMEDIATE")
            cursor.execute("SELECT * FROM privilege_approvals WHERE target_id = ?", (target_id,))
            row = cursor.fetchone()
            if not row or row["policy"] != policy:
                conn.rollback()
                return False
            if row["client_id"] != client_id or row["project_id"] != project_id:
                conn.rollback()
                return False
            if policy == "ask_once_per_boot":
                if bool(boot_id) and row["boot_id"] == boot_id:
                    conn.commit()
                    return True
                cursor.execute("DELETE FROM privilege_approvals WHERE target_id = ?", (target_id,))
                conn.commit()
                return False
            if policy == "ask_always":
                try:
                    age = time.time() - float(row["approved_at"])
                except (TypeError, ValueError):
                    age = max_age_seconds + 1
                cursor.execute("DELETE FROM privilege_approvals WHERE target_id = ?", (target_id,))
                if age < 0 or age > max_age_seconds:
                    conn.commit()
                    return False
                conn.commit()
                return True
            conn.rollback()
            return False

    def get_setting(self, key: str, default=None) -> Any:
        with self._get_conn() as conn:
            cursor = conn.cursor()
            cursor.execute("SELECT value FROM settings WHERE key = ?", (key,))
            row = cursor.fetchone()
            if row:
                return row["value"]
            return default

    def set_setting(self, key: str, value: Any) -> None:
        with self._get_conn() as conn:
            cursor = conn.cursor()
            cursor.execute('''
                INSERT INTO settings (key, value) VALUES (?, ?)
                ON CONFLICT(key) DO UPDATE SET value = ?, updated_at = datetime('now')
            ''', (key, str(value), str(value)))
            conn.commit()

    def record_activity(self, entry: Dict) -> None:
        with self._get_conn() as conn:
            cursor = conn.cursor()
            cursor.execute('''
                INSERT INTO activity (actor, action, target_id, project_id, duration_ms, success, error_code, bytes_transferred, detail)
                VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
            ''', (
                entry.get("actor", ""),
                entry.get("action", ""),
                entry.get("target_id"),
                entry.get("project_id"),
                entry.get("duration_ms"),
                int(entry.get("success", 1)),
                entry.get("error_code"),
                entry.get("bytes_transferred"),
                entry.get("detail", "")
            ))
            conn.commit()

    def list_activity(self, limit: int = 50, offset: int = 0) -> List[Dict]:
        with self._get_conn() as conn:
            cursor = conn.cursor()
            cursor.execute("SELECT * FROM activity ORDER BY timestamp DESC, id DESC LIMIT ? OFFSET ?", (limit, offset))
            activities = []
            for row in cursor.fetchall():
                a = dict(row)
                a["success"] = bool(a["success"])
                activities.append(a)
            return activities

    def get_admin_user(self, username: str) -> Optional[Dict]:
        with self._get_conn() as conn:
            cursor = conn.cursor()
            cursor.execute("SELECT * FROM admin_users WHERE username = ?", (username,))
            row = cursor.fetchone()
            if row:
                u = dict(row)
                u["enabled"] = bool(u["enabled"])
                return u
            return None

    def set_admin_password(self, username: str, password_hash: str) -> None:
        with self._get_conn() as conn:
            cursor = conn.cursor()
            cursor.execute('''
                INSERT INTO admin_users (username, password_hash) VALUES (?, ?)
                ON CONFLICT(username) DO UPDATE SET password_hash = ?
            ''', (username, password_hash, password_hash))
            conn.commit()

    def update_admin_login(self, username: str) -> None:
        with self._get_conn() as conn:
            cursor = conn.cursor()
            cursor.execute("UPDATE admin_users SET last_login = datetime('now') WHERE username = ?", (username,))
            conn.commit()

    def get_activity_count(self) -> int:
        with self._get_conn() as conn:
            cursor = conn.cursor()
            cursor.execute("SELECT COUNT(*) as c FROM activity")
            return cursor.fetchone()["c"]

    def prune_activity(self, keep: int) -> int:
        with self._get_conn() as conn:
            cursor = conn.cursor()
            cursor.execute("SELECT id FROM activity ORDER BY timestamp DESC, id DESC LIMIT 1 OFFSET ?", (keep - 1,))
            row = cursor.fetchone()
            if not row:
                return 0
                
            cutoff_id = row["id"]
            cursor.execute("DELETE FROM activity WHERE id < ?", (cutoff_id,))
            deleted = cursor.rowcount
            conn.commit()
            return deleted


def import_from_json(config: GatewayConfig, registry: SQLiteRegistry) -> Dict[str, Any]:
    """
    Import targets and projects from a GatewayConfig instance into a SQLiteRegistry idempotently.
    Returns a summary dict: {'targets_imported': int, 'projects_imported': int, 'tasks_imported': int}
    """
    targets_count = 0
    projects_count = 0
    tasks_count = 0

    with registry._get_conn() as conn:
        cursor = conn.cursor()
        for target_id, target_data in config._data.get("targets", {}).items():
            cursor.execute("SELECT id FROM targets WHERE id = ?", (target_id,))
            existing = cursor.fetchone()
            if existing:
                cursor.execute("""
                    UPDATE targets SET
                        display_name = ?,
                        platform = ?,
                        host = ?,
                        port = ?,
                        user = ?,
                        ssh_alias = ?,
                        privilege_user = ?,
                        privilege_policy = ?,
                        enabled = ?,
                        updated_at = datetime('now')
                    WHERE id = ?
                """, (
                    target_data.get("display_name", target_id),
                    target_data.get("platform", "linux"),
                    target_data.get("host", ""),
                    target_data.get("port", 22),
                    target_data.get("user", ""),
                    target_data.get("ssh_alias", ""),
                    str(target_data.get("privilege_user", "") or "").strip(),
                    normalize_privilege_policy(target_data.get("privilege_policy", "never")),
                    int(target_data.get("enabled", True)),
                    target_id,
                ))
            else:
                cursor.execute("""
                    INSERT INTO targets (id, display_name, platform, host, port, user, ssh_alias, privilege_user, privilege_policy, enabled)
                    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                """, (
                    target_id,
                    target_data.get("display_name", target_id),
                    target_data.get("platform", "linux"),
                    target_data.get("host", ""),
                    target_data.get("port", 22),
                    target_data.get("user", ""),
                    target_data.get("ssh_alias", ""),
                    str(target_data.get("privilege_user", "") or "").strip(),
                    normalize_privilege_policy(target_data.get("privilege_policy", "never")),
                    int(target_data.get("enabled", True)),
                ))
            # Privilege approvals are temporary runtime authorization and are never
            # imported/exported. Re-importing Target configuration must not
            # preserve a stale approval across a policy/configuration change.
            cursor.execute("DELETE FROM privilege_approvals WHERE target_id = ?", (target_id,))
            targets_count += 1

            for project_id, project_data in target_data.get("projects", {}).items():
                cursor.execute("SELECT id FROM projects WHERE target_id = ? AND id = ?", (target_id, project_id))
                existing_p = cursor.fetchone()
                if existing_p:
                    cursor.execute("""
                        UPDATE projects SET
                            display_name = ?,
                            root = ?,
                            read_enabled = ?,
                            write_enabled = ?,
                            enabled = ?,
                            updated_at = datetime('now')
                        WHERE target_id = ? AND id = ?
                    """, (
                        project_data.get("display_name", project_id),
                        project_data.get("root", "/"),
                        int(project_data.get("read", True)),
                        int(project_data.get("write", False)),
                        int(project_data.get("enabled", True)),
                        target_id,
                        project_id,
                    ))
                else:
                    cursor.execute("""
                        INSERT INTO projects (id, target_id, display_name, root, read_enabled, write_enabled, enabled)
                        VALUES (?, ?, ?, ?, ?, ?, ?)
                    """, (
                        project_id,
                        target_id,
                        project_data.get("display_name", project_id),
                        project_data.get("root", "/"),
                        int(project_data.get("read", True)),
                        int(project_data.get("write", False)),
                        int(project_data.get("enabled", True)),
                    ))
                projects_count += 1

                cursor.execute("DELETE FROM project_tasks WHERE target_id = ? AND project_id = ?", (target_id, project_id))
                for task_name, task_data in project_data.get("tasks", {}).items():
                    cursor.execute("""
                        INSERT INTO project_tasks (target_id, project_id, task_name, argv_json, timeout, enabled)
                        VALUES (?, ?, ?, ?, ?, ?)
                    """, (
                        target_id,
                        project_id,
                        task_name,
                        json.dumps(task_data.get("argv", [])),
                        task_data.get("timeout", 30),
                        int(task_data.get("enabled", True)),
                    ))
                    tasks_count += 1

        conn.commit()

    return {
        "targets_imported": targets_count,
        "projects_imported": projects_count,
        "tasks_imported": tasks_count,
    }


def get_default_db_path() -> str:
    env_db = os.environ.get("MCP_GATEWAY_DB")
    if env_db:
        return env_db
    user_data_path = "/home/mcp-gateway/.local/share/mcp-gateway/gateway.db"
    if os.path.exists(os.path.dirname(user_data_path)):
        return user_data_path
    return "gateway.db"


def get_registry(
    backend: Optional[str] = None,
    config_path: Optional[str] = None,
    db_path: Optional[str] = None,
) -> RegistryBase:
    """
    Get registry instance based on environment or parameter.
    Allows easy rollback: MCP_GATEWAY_REGISTRY=json vs sqlite.
    """
    chosen_backend = (backend or os.environ.get("MCP_GATEWAY_REGISTRY", "sqlite")).lower()

    if chosen_backend == "json":
        cfg = GatewayConfig.load(config_path)
        return JsonRegistry(cfg)

    resolved_db = db_path or get_default_db_path()
    registry = SQLiteRegistry(resolved_db)

    # Auto-bootstrap from json if sqlite database is brand new and has 0 targets
    if registry.target_count == 0:
        try:
            cfg = GatewayConfig.load(config_path)
            import_from_json(cfg, registry)
        except Exception:
            pass

    return registry

