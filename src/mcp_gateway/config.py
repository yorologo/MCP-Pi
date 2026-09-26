"""Target and project configuration loader and validator for MCP Gateway."""

import json
import os
from typing import Any, Dict, List, Optional

from .policy import normalize_privilege_policy


class ConfigError(Exception):
    """Raised when configuration cannot be loaded or is invalid."""

    def __init__(self, message: str, code: str = "CONFIG_ERROR"):
        super().__init__(message)
        self.code = code


class GatewayConfig:
    """Manages target and project configurations for the gateway."""

    DEFAULT_PATHS = [
        "config/targets.local.json",
        "/home/mcp-gateway/mcp-gateway/config/targets.local.json",
        "/opt/mcp-gateway/config/targets.local.json",
        "config/targets.example.json",
    ]

    def __init__(self, raw_data: Optional[Dict[str, Any]] = None, source_path: Optional[str] = None):
        self.source_path = source_path
        self._data: Dict[str, Any] = {}
        if raw_data is not None:
            self._validate_and_set(raw_data)

    @classmethod
    def load(cls, config_path: Optional[str] = None) -> "GatewayConfig":
        """Load configuration from a file path, environment variable, or default locations."""
        target_path = config_path or os.environ.get("MCP_GATEWAY_CONFIG")

        if target_path:
            if not os.path.exists(target_path):
                raise ConfigError(f"Config file not found: {target_path}", code="CONFIG_ERROR")
            chosen_path = target_path
        else:
            chosen_path = None
            for candidate in cls.DEFAULT_PATHS:
                if os.path.exists(candidate):
                    chosen_path = candidate
                    break

            if not chosen_path:
                raise ConfigError(
                    "No configuration file found in default search paths.",
                    code="CONFIG_ERROR",
                )

        try:
            with open(chosen_path, "r", encoding="utf-8") as f:
                data = json.load(f)
        except json.JSONDecodeError as e:
            raise ConfigError(f"Invalid JSON in config file {chosen_path}: {e}", code="CONFIG_ERROR")
        except OSError as e:
            raise ConfigError(f"Cannot read config file {chosen_path}: {e}", code="CONFIG_ERROR")

        instance = cls(source_path=chosen_path)
        instance._validate_and_set(data)
        return instance

    def _validate_and_set(self, data: Any) -> None:
        if not isinstance(data, dict):
            raise ConfigError("Config root must be a JSON object", code="CONFIG_ERROR")

        targets = data.get("targets")
        if not isinstance(targets, dict):
            raise ConfigError("'targets' must be a JSON object", code="CONFIG_ERROR")

        validated_targets: Dict[str, Any] = {}
        for target_id, target_info in targets.items():
            if not isinstance(target_info, dict):
                raise ConfigError(f"Target '{target_id}' must be an object", code="CONFIG_ERROR")

            platform = target_info.get("platform")
            if not platform or not isinstance(platform, str):
                raise ConfigError(f"Target '{target_id}' missing valid 'platform'", code="CONFIG_ERROR")

            host = target_info.get("host")
            if not host or not isinstance(host, str):
                raise ConfigError(f"Target '{target_id}' missing valid 'host'", code="CONFIG_ERROR")

            port = target_info.get("port", 22)
            if not isinstance(port, int) or port <= 0 or port > 65535:
                raise ConfigError(f"Target '{target_id}' has invalid port: {port}", code="CONFIG_ERROR")

            user = target_info.get("user")
            if not user or not isinstance(user, str):
                raise ConfigError(f"Target '{target_id}' missing valid 'user'", code="CONFIG_ERROR")

            ssh_alias = target_info.get("ssh_alias")
            if not ssh_alias or not isinstance(ssh_alias, str):
                raise ConfigError(f"Target '{target_id}' missing valid 'ssh_alias'", code="CONFIG_ERROR")

            privilege_user = target_info.get("privilege_user", "")
            if privilege_user is None:
                privilege_user = ""
            if not isinstance(privilege_user, str):
                raise ConfigError(
                    f"Target '{target_id}' 'privilege_user' must be a string",
                    code="CONFIG_ERROR",
                )
            privilege_user = privilege_user.strip()

            enabled = target_info.get("enabled", True)
            if not isinstance(enabled, bool):
                raise ConfigError(f"Target '{target_id}' 'enabled' must be boolean", code="CONFIG_ERROR")

            try:
                privilege_policy = normalize_privilege_policy(
                    target_info.get("privilege_policy", "never")
                )
            except Exception as exc:
                raise ConfigError(
                    f"Target '{target_id}' has invalid privilege_policy: {exc}",
                    code="CONFIG_ERROR",
                ) from exc

            raw_projects = target_info.get("projects", {})
            if not isinstance(raw_projects, dict):
                raise ConfigError(f"Target '{target_id}' 'projects' must be an object", code="CONFIG_ERROR")

            validated_projects: Dict[str, Any] = {}
            for project_id, project_info in raw_projects.items():
                if not isinstance(project_info, dict):
                    raise ConfigError(f"Project '{project_id}' in target '{target_id}' must be an object", code="CONFIG_ERROR")

                root = project_info.get("root")
                if not root or not isinstance(root, str) or not (root.startswith("/") or (len(root) > 2 and root[1:3] == ":\\")):
                    raise ConfigError(f"Project '{project_id}' in '{target_id}' must have an absolute 'root' path", code="CONFIG_ERROR")

                read_perm = project_info.get("read", True)
                write_perm = project_info.get("write", False)
                if not isinstance(read_perm, bool) or not isinstance(write_perm, bool):
                    raise ConfigError(f"Project '{project_id}' permissions must be booleans", code="CONFIG_ERROR")

                raw_tasks = project_info.get("tasks", {})
                if not isinstance(raw_tasks, dict):
                    raise ConfigError(f"Project '{project_id}' tasks must be an object", code="CONFIG_ERROR")

                validated_tasks: Dict[str, Any] = {}
                for task_name, task_def in raw_tasks.items():
                    if isinstance(task_def, bool):
                        if task_def:
                            validated_tasks[task_name] = {"enabled": True, "argv": [task_name], "timeout": 30}
                    elif isinstance(task_def, dict):
                        t_enabled = task_def.get("enabled", True)
                        t_argv = task_def.get("argv", [])
                        t_timeout = task_def.get("timeout", 30)
                        if not isinstance(t_argv, list) or not t_argv or not all(isinstance(a, str) for a in t_argv):
                            raise ConfigError(f"Task '{task_name}' in project '{project_id}' must have a non-empty list of string argv", code="CONFIG_ERROR")
                        validated_tasks[task_name] = {
                            "enabled": bool(t_enabled),
                            "argv": list(t_argv),
                            "timeout": int(t_timeout),
                        }
                    else:
                        raise ConfigError(f"Invalid task definition for '{task_name}' in '{project_id}'", code="CONFIG_ERROR")

                # Normalize trailing slash in root
                normalized_root = root.rstrip("/") if root != "/" else "/"
                validated_projects[project_id] = {
                    "root": normalized_root,
                    "read": read_perm,
                    "write": write_perm,
                    "enabled": bool(project_info.get("enabled", True)),
                    "tasks": validated_tasks,
                }

            validated_targets[target_id] = {
                "platform": platform,
                "host": host,
                "port": port,
                "user": user,
                "ssh_alias": ssh_alias,
                "privilege_user": privilege_user,
                "privilege_policy": privilege_policy,
                "enabled": enabled,
                "projects": validated_projects,
            }

        self._data = {"targets": validated_targets}

    def get_target(self, target_id: str, include_disabled: bool = False) -> Dict[str, Any]:
        """Retrieve target configuration or raise ConfigError."""
        targets = self._data.get("targets", {})
        if target_id not in targets:
            raise ConfigError(f"Target '{target_id}' is not configured", code="UNKNOWN_TARGET")
        target = dict(targets[target_id])
        target.setdefault("id", target_id)
        if not include_disabled and not target.get("enabled", True):
            raise ConfigError(f"Target '{target_id}' is disabled", code="TARGET_DISABLED")
        return target

    def get_project(
        self,
        target_id: str,
        project_id: str,
        include_disabled: bool = False,
    ) -> Dict[str, Any]:
        """Retrieve project configuration under target or raise ConfigError."""
        target = self.get_target(target_id, include_disabled=include_disabled)
        projects = target.get("projects", {})
        if project_id not in projects:
            raise ConfigError(
                f"Project '{project_id}' is not configured under target '{target_id}'",
                code="UNKNOWN_PROJECT",
            )
        project = dict(projects[project_id])
        project.setdefault("id", project_id)
        project.setdefault("target_id", target_id)
        if not include_disabled and not project.get("enabled", True):
            raise ConfigError(
                f"Project '{project_id}' in target '{target_id}' is disabled",
                code="PROJECT_DISABLED",
            )
        return project

    def list_targets(self) -> List[Dict[str, Any]]:
        """Return public, safe metadata for all configured targets."""
        result = []
        for target_id, target in self._data.get("targets", {}).items():
            projects = target.get("projects", {})
            result.append({
                "id": target_id,
                "platform": target.get("platform"),
                "privilege_user": target.get("privilege_user", ""),
                "privilege_policy": target.get("privilege_policy", "never"),
                "enabled": target.get("enabled", True),
                "project_count": len(projects),
                "projects": sorted(list(projects.keys())),
            })
        return result

    @property
    def target_count(self) -> int:
        return len(self._data.get("targets", {}))
