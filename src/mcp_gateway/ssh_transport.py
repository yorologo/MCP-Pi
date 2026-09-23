"""SSH transport wrapper for remote execution across configured targets."""

import base64
import json
import os
import shlex
import subprocess
import time
import zlib
from typing import Any, Dict, List, Optional, Protocol, Tuple


def build_remote_python_command(py_code: str, argv: Optional[List[Any]] = None) -> str:
    """Build a shell-safe Python helper command without exposing dynamic data to the remote shell."""
    args = [str(arg) for arg in (argv or [])]
    script = "import sys\nsys.argv = ['mcp-helper'] + " + json.dumps(args) + "\n" + py_code
    payload = base64.b64encode(zlib.compress(script.encode("utf-8"))).decode("ascii")
    return (
        'python3 -c "import base64,zlib;'
        "exec(zlib.decompress(base64.b64decode('" + payload + "')))"
        '"'
    )


class SSHError(Exception):
    """Raised when an SSH transport execution fails or times out."""

    def __init__(self, message: str, code: str = "SSH_FAILED", exit_code: Optional[int] = None):
        super().__init__(message)
        self.code = code
        self.exit_code = exit_code


class SSHTransportResult:
    """Result of an SSH remote command execution."""

    def __init__(self, exit_code: int, stdout: str, stderr: str, duration_ms: int, request_id: Optional[str] = None, timed_out: bool = False):
        self.exit_code = exit_code
        self.stdout = stdout
        self.stderr = stderr
        self.duration_ms = duration_ms
        self.request_id = request_id
        self.timed_out = timed_out

    @property
    def ok(self) -> bool:
        return self.exit_code == 0


def classify_ssh_transport_failure(
    exit_code: int, stderr: Optional[str], timed_out: bool = False
) -> Tuple[bool, str]:
    """Classify SSH transport failure into actionable categories.
    
    Returns:
        (should_trigger_discovery: bool, failure_classification: str)
        
    Categories:
        - "NETWORK_CONNECTIVITY_ERROR": Timeout, connection refused, no route, network unreachable.
        - "ENDPOINT_IDENTITY_MISMATCH": Host key verification failed or identification changed.
          The stored endpoint answered with a key differing from the target's pinned canonical key.
          The stored endpoint is rejected (never accepted, never saved to known_hosts).
          Discovery is permitted to locate the canonical pinned key on another network endpoint.
        - "AUTH_FAILURE": Permission denied, authentication failed. Strict fail-closed, NO discovery.
        - "NONE": Success (exit code 0).
        - "OTHER_ERROR": Unclassified error. Fail-closed, NO discovery.
    """
    if not timed_out and exit_code == 0:
        return False, "NONE"

    err = (stderr or "").lower()

    # 1. Auth failure: Strict Fail-Closed. NEVER trigger discovery.
    if "permission denied" in err or "authentication failed" in err:
        return False, "AUTH_FAILURE"

    # 2. Endpoint Identity Mismatch: Host key verification failed on stored endpoint.
    # The stored endpoint responded, but its key did not match the pinned key for this target.
    # e.g., DHCP IP reuse by a foreign host.
    # Stored endpoint is rejected. Discovery is triggered to search for the canonical key elsewhere.
    if "host key verification failed" in err or "identification has changed" in err:
        return True, "ENDPOINT_IDENTITY_MISMATCH"

    # 3. Network timeout or connectivity errors
    if timed_out:
        return True, "NETWORK_CONNECTIVITY_ERROR"

    network_patterns = [
        "connection refused",
        "connection timed out",
        "no route to host",
        "network is unreachable",
        "host is down",
        "operation timed out",
        "could not resolve hostname",
        "connect to host",
    ]
    if any(p in err for p in network_patterns):
        return True, "NETWORK_CONNECTIVITY_ERROR"

    return False, "OTHER_ERROR"


def is_network_connectivity_error(exit_code: int, stderr: Optional[str], timed_out: bool = False) -> bool:
    """Helper returning True if discovery should be triggered (network error or endpoint mismatch)."""
    should_discover, _ = classify_ssh_transport_failure(exit_code, stderr, timed_out=timed_out)
    return should_discover


class RemoteTransport(Protocol):
    """Minimal target transport contract consumed by Gateway Core."""

    registry: Any

    def run_command(self, target: Dict[str, Any], remote_cmd: str, **kwargs: Any) -> SSHTransportResult: ...
    def resolve_canonical_path(self, target: Dict[str, Any], candidate_path: str, timeout: int = 10) -> str: ...
    def resolve_safe_destination(
        self, target: Dict[str, Any], project_root: str, candidate_path: str,
        allow_missing_parents: bool = False, timeout: int = 10
    ) -> Dict[str, Any]: ...
    def read_remote_file_content(self, target: Dict[str, Any], canonical_path: str, max_bytes: Optional[int] = None) -> str: ...
    def probe_remote_path(self, target: Dict[str, Any], candidate_path: str, timeout: int = 10) -> Dict[str, Any]: ...
    def write_remote_file_atomic(self, target: Dict[str, Any], dest_path: str, content_bytes: bytes, create: bool, **kwargs: Any) -> Dict[str, Any]: ...


class SSHTransport:
    """Invokes system OpenSSH via subprocess for target operations."""

    DEFAULT_TIMEOUT = 30
    MAX_OUTPUT_BYTES = 262144     # 256 KiB
    MAX_FILE_READ_BYTES = 1048576  # 1 MiB

    def __init__(
        self,
        default_timeout: int = DEFAULT_TIMEOUT,
        max_output_bytes: int = MAX_OUTPUT_BYTES,
        max_file_read_bytes: int = MAX_FILE_READ_BYTES,
        ssh_binary: str = "ssh",
        discovery: Optional[Any] = None,
        registry: Optional[Any] = None,
        identity_file: Optional[str] = None,
    ):
        self.default_timeout = default_timeout
        self.max_output_bytes = max_output_bytes
        self.max_file_read_bytes = max_file_read_bytes
        self.ssh_binary = ssh_binary
        self.discovery = discovery
        self.registry = registry
        self.identity_file = os.path.expanduser(
            identity_file
            or os.environ.get("MCP_GATEWAY_SSH_IDENTITY_FILE", "~/.ssh/mcp_gateway_ed25519")
        )

    def _build_ssh_args(self, target: Dict[str, Any], timeout: int) -> List[str]:
        """Construct SSH argument list ensuring Registry is single source of truth for endpoints."""
        alias = target.get("ssh_alias")
        host = target.get("host")
        port = str(target.get("port", 22))
        target_id = target.get("id") or alias

        user = target["user"]
        base_cmd = [
            self.ssh_binary,
            "-o", "BatchMode=yes",
            "-o", f"ConnectTimeout={min(timeout, 10)}",
            "-o", "StrictHostKeyChecking=yes",
            "-o", "IdentitiesOnly=yes",
            "-o", f"IdentityFile={self.identity_file}",
            "-o", f"User={user}",
        ]

        # Registry values are authoritative for endpoint and remote account.
        # An optional SSH alias may still contribute non-authoritative options
        # such as ProxyJump, but it cannot override host, port, user, pinned
        # host identity, or the Gateway client identity.
        if host:
            base_cmd.extend(["-o", f"HostName={host}"])
        if port:
            base_cmd.extend(["-o", f"Port={port}"])
        if target_id:
            base_cmd.extend(["-o", f"HostKeyAlias={target_id}"])

        base_cmd.append(alias or host)
        return base_cmd

    def _attempt_discovery_and_retry(
        self,
        target: Dict[str, Any],
        remote_cmd: str,
        timeout: Optional[int],
        cwd: Optional[str],
        env: Optional[Dict[str, str]],
        input_data: Optional[bytes],
        request_id: Optional[str],
        trigger_reason: str = "NETWORK_CONNECTIVITY_ERROR",
    ) -> Optional[SSHTransportResult]:
        """Attempt on-demand cryptographic discovery and retry the SSH command exactly once."""
        if not self.discovery or not self.registry:
            return None

        target_id = target.get("id") or target.get("ssh_alias")
        if not target_id:
            return None

        disc_res = self.discovery.discover_target(target)
        if disc_res.status == "IDENTITY_MATCH" and disc_res.new_host:
            old_host = target.get("host")
            new_host = disc_res.new_host
            port = target.get("port", 22)

            if old_host != new_host:
                # Update endpoint in registry (atomic single SQL statement on targets table)
                if hasattr(self.registry, "update_target"):
                    self.registry.update_target(target_id, {"host": new_host})
                # Record audit event in activity table
                if hasattr(self.registry, "record_activity"):
                    self.registry.record_activity({
                        "actor": "system",
                        "action": "target_endpoint_updated",
                        "target_id": target_id,
                        "duration_ms": disc_res.duration_ms,
                        "success": True,
                        "detail": json.dumps({
                            "old_endpoint": f"{old_host}:{port}",
                            "new_endpoint": f"{new_host}:{port}",
                            "discovery_method": disc_res.method,
                            "trigger_reason": trigger_reason,
                            "identity_verified": True,
                        }),
                    })
                target["host"] = new_host

            # Retry original command EXACTLY ONCE
            return self.run_command(
                target,
                remote_cmd,
                timeout=timeout,
                cwd=cwd,
                env=env,
                input_data=input_data,
                request_id=request_id,
                is_retry=True,
            )
        return None

    def run_command(
        self,
        target: Dict[str, Any],
        remote_cmd: str,
        timeout: Optional[int] = None,
        cwd: Optional[str] = None,
        env: Optional[Dict[str, str]] = None,
        input_data: Optional[bytes] = None,
        request_id: Optional[str] = None,
        is_retry: bool = False,
    ) -> SSHTransportResult:
        """Execute a remote shell command string safely constructed by the gateway."""
        effective_timeout = timeout or self.default_timeout

        platform_name = str(target.get("platform") or "").lower()
        if platform_name == "windows" and (cwd or env):
            safe_env = {
                str(k): str(v)
                for k, v in (env or {}).items()
                if str(k).isidentifier()
            }
            bootstrap = (
                "import os, subprocess, sys\n"
                f"command = {json.dumps(remote_cmd)}\n"
                f"cwd = {json.dumps(cwd)}\n"
                f"env = {json.dumps(safe_env)}\n"
                "if cwd:\n"
                "    os.chdir(cwd)\n"
                "if env:\n"
                "    os.environ.update(env)\n"
                "sys.exit(subprocess.run(command, shell=True).returncode)\n"
            )
            full_remote_cmd = build_remote_python_command(bootstrap)
        else:
            cmd_parts = []
            if env:
                for k, v in env.items():
                    if str(k).isidentifier():
                        cmd_parts.append(f"export {k}={shlex.quote(str(v))};")
            if cwd:
                cmd_parts.append(f"cd {shlex.quote(cwd)} &&")
            cmd_parts.append(remote_cmd)
            full_remote_cmd = " ".join(cmd_parts)

        ssh_args = self._build_ssh_args(target, effective_timeout)
        ssh_args.append(full_remote_cmd)

        start_time = time.monotonic()
        try:
            proc = subprocess.run(
                ssh_args,
                input=input_data,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                timeout=effective_timeout,
                check=False,
            )
            duration_ms = int((time.monotonic() - start_time) * 1000)

        except subprocess.TimeoutExpired:
            duration_ms = int((time.monotonic() - start_time) * 1000)
            should_discover, failure_class = classify_ssh_transport_failure(-1, "", timed_out=True)
            if not is_retry and should_discover:
                retry_res = self._attempt_discovery_and_retry(
                    target,
                    remote_cmd,
                    timeout,
                    cwd,
                    env,
                    input_data,
                    request_id,
                    trigger_reason=failure_class,
                )
                if retry_res is not None:
                    return retry_res

            raise SSHError(
                f"SSH command timed out after {effective_timeout}s",
                code="SSH_TIMEOUT",
            )
        except OSError as e:
            raise SSHError(f"Failed to invoke ssh binary '{self.ssh_binary}': {e}", code="SSH_FAILED")

        # Truncate output if it exceeds max_output_bytes
        stdout_raw = proc.stdout
        stderr_raw = proc.stderr

        stdout_str = stdout_raw[:self.max_output_bytes].decode("utf-8", errors="replace")
        stderr_str = stderr_raw[:self.max_output_bytes].decode("utf-8", errors="replace")

        # Fast Path: success returns immediately
        if proc.returncode != 0 and not is_retry:
            should_discover, failure_class = classify_ssh_transport_failure(proc.returncode, stderr_str)
            if should_discover:
                retry_res = self._attempt_discovery_and_retry(
                    target,
                    remote_cmd,
                    timeout,
                    cwd,
                    env,
                    input_data,
                    request_id,
                    trigger_reason=failure_class,
                )
                if retry_res is not None:
                    return retry_res

        return SSHTransportResult(
            exit_code=proc.returncode,
            stdout=stdout_str,
            stderr=stderr_str,
            duration_ms=duration_ms,
            request_id=request_id,
        )

    def resolve_canonical_path(
        self, target: Dict[str, Any], candidate_path: str, timeout: int = 10
    ) -> str:
        """Resolve the remote canonical realpath of a candidate path."""
        py_code = "import os, sys; print(os.path.realpath(sys.argv[1]))"
        remote_cmd = build_remote_python_command(py_code, [candidate_path])

        res = self.run_command(target, remote_cmd, timeout=timeout)
        if not res.ok or not res.stdout.strip():
            raise SSHError(
                f"Unable to resolve canonical path on target: {res.stderr.strip()}",
                code="SSH_FAILED",
                exit_code=res.exit_code,
            )

        canonical = res.stdout.strip().splitlines()[-1].strip()
        return canonical

    def read_remote_file_content(
        self, target: Dict[str, Any], canonical_path: str, max_bytes: Optional[int] = None
    ) -> str:
        """Read text content of a remote file with size and binary checks."""
        limit = max_bytes or self.max_file_read_bytes
        # Read limit + 1 bytes to detect truncation
        py_code = (
            "import sys, os\n"
            "path = sys.argv[1]\n"
            "limit = int(sys.argv[2])\n"
            "if not os.path.exists(path):\n"
            "    sys.exit(2)\n"
            "if os.path.isdir(path):\n"
            "    sys.exit(3)\n"
            "size = os.path.getsize(path)\n"
            "if size > limit:\n"
            "    sys.exit(4)\n"
            "with open(path, 'rb') as f:\n"
            "    data = f.read(limit + 1)\n"
            "if b'\\x00' in data:\n"
            "    sys.exit(5)\n"
            "sys.stdout.buffer.write(data)\n"
        )

        remote_cmd = build_remote_python_command(py_code, [canonical_path, limit])
        res = self.run_command(target, remote_cmd, timeout=15)

        if res.exit_code == 2:
            raise SSHError(f"File not found: {canonical_path}", code="NOT_FOUND")
        if res.exit_code == 3:
            raise SSHError(f"Target path is a directory: {canonical_path}", code="INVALID_PATH")
        if res.exit_code == 4:
            raise SSHError(
                f"File size exceeds allowed limit of {limit} bytes",
                code="FILE_TOO_LARGE",
            )
        if res.exit_code == 5:
            raise SSHError("Binary file not supported", code="BINARY_FILE_NOT_SUPPORTED")
        if not res.ok:
            raise SSHError(
                f"Error reading file '{canonical_path}': {res.stderr.strip()}",
                code="SSH_FAILED",
                exit_code=res.exit_code,
            )

        return res.stdout

    def probe_remote_path(
        self, target: Dict[str, Any], candidate_path: str, timeout: int = 10
    ) -> Dict[str, Any]:
        """Probe remote path for existence, type, symlink status, canonical path, and sha256."""
        py_code = (
            "import os, sys, json, hashlib\n"
            "p = sys.argv[1]\n"
            "lexists = os.path.lexists(p)\n"
            "is_link = os.path.islink(p)\n"
            "is_file = os.path.isfile(p) and not is_link\n"
            "is_dir = os.path.isdir(p) and not is_link\n"
            "canon = os.path.realpath(p) if lexists else ''\n"
            "parent = os.path.dirname(p) or '.'\n"
            "parent_exists = os.path.exists(parent)\n"
            "parent_is_link = os.path.islink(parent)\n"
            "parent_canon = os.path.realpath(parent) if parent_exists else ''\n"
            "sha = ''\n"
            "size = 0\n"
            "content = ''\n"
            "if is_file:\n"
            "    try:\n"
            "        with open(p, 'rb') as f:\n"
            "            data = f.read()\n"
            "        sha = hashlib.sha256(data).hexdigest()\n"
            "        size = len(data)\n"
            "        content = data.decode('utf-8', errors='replace')\n"
            "    except Exception:\n"
            "        pass\n"
            "print(json.dumps({\n"
            "    'exists': lexists,\n"
            "    'is_symlink': is_link,\n"
            "    'is_file': is_file,\n"
            "    'is_dir': is_dir,\n"
            "    'canonical_path': canon,\n"
            "    'parent_exists': parent_exists,\n"
            "    'parent_is_symlink': parent_is_link,\n"
            "    'parent_canonical_path': parent_canon,\n"
            "    'sha256': sha,\n"
            "    'size': size,\n"
            "    'content': content,\n"
            "}))\n"
        )
        cmd = build_remote_python_command(py_code, [candidate_path])
        res = self.run_command(target, cmd, timeout=timeout)
        if not res.ok or not res.stdout.strip():
            raise SSHError(f"Probe failed: {res.stderr.strip()}", code="SSH_FAILED", exit_code=res.exit_code)
        try:
            return json.loads(res.stdout.strip().splitlines()[-1])
        except Exception as e:
            raise SSHError(f"Invalid probe JSON response: {e}", code="SSH_FAILED")

    def resolve_safe_destination(
        self,
        target: Dict[str, Any],
        project_root: str,
        candidate_path: str,
        allow_missing_parents: bool = False,
        timeout: int = 10,
    ) -> Dict[str, Any]:
        """Resolve a structured-mutation destination remotely and fail closed on escapes/symlinks."""
        py_code = (
            "import os, sys, json\n"
            "root = sys.argv[1]\n"
            "candidate = sys.argv[2]\n"
            "allow_missing = sys.argv[3] == '1'\n"
            "root_real = os.path.realpath(root)\n"
            "def emit(ok, code='', message='', **extra):\n"
            "    d = {'ok': ok, 'code': code, 'message': message}; d.update(extra); print(json.dumps(d))\n"
            "if not os.path.isdir(root_real):\n"
            "    emit(False, 'PROJECT_ROOT_INVALID', f'Project root is not a directory: {root}'); sys.exit(10)\n"
            "if os.path.lexists(candidate) and os.path.islink(candidate):\n"
            "    emit(False, 'SYMLINK_WRITE_DENIED', f'Destination is a symlink: {candidate}'); sys.exit(11)\n"
            "parent = os.path.dirname(candidate) or '.'\n"
            "cursor = parent\n"
            "missing = []\n"
            "while not os.path.exists(cursor):\n"
            "    if not allow_missing:\n"
            "        emit(False, 'NOT_FOUND', f'Parent directory does not exist: {parent}'); sys.exit(12)\n"
            "    base = os.path.basename(cursor)\n"
            "    if not base or base in ('.', '..'):\n"
            "        emit(False, 'INVALID_PATH', f'Invalid destination parent: {parent}'); sys.exit(13)\n"
            "    missing.append(base)\n"
            "    nxt = os.path.dirname(cursor)\n"
            "    if nxt == cursor:\n"
            "        emit(False, 'PATH_OUTSIDE_ALLOWED_ROOT', f'Unable to anchor destination beneath project root: {candidate}'); sys.exit(14)\n"
            "    cursor = nxt\n"
            "anchor_real = os.path.realpath(cursor)\n"
            "try:\n"
            "    if os.path.commonpath([root_real, anchor_real]) != root_real:\n"
            "        emit(False, 'PATH_OUTSIDE_ALLOWED_ROOT', f'Destination parent resolves outside project root: {parent}'); sys.exit(15)\n"
            "except ValueError:\n"
            "    emit(False, 'PATH_OUTSIDE_ALLOWED_ROOT', f'Destination parent is on a different path domain: {parent}'); sys.exit(16)\n"
            "parent_real = os.path.normpath(os.path.join(anchor_real, *reversed(missing)))\n"
            "try:\n"
            "    if os.path.commonpath([root_real, parent_real]) != root_real:\n"
            "        emit(False, 'PATH_OUTSIDE_ALLOWED_ROOT', f'Destination parent escapes project root: {parent}'); sys.exit(17)\n"
            "except ValueError:\n"
            "    emit(False, 'PATH_OUTSIDE_ALLOWED_ROOT', f'Destination parent is on a different path domain: {parent}'); sys.exit(18)\n"
            "dest = os.path.join(parent_real, os.path.basename(candidate))\n"
            "dest_exists = os.path.lexists(candidate)\n"
            "dest_real = os.path.realpath(candidate) if dest_exists else ''\n"
            "if dest_exists:\n"
            "    try:\n"
            "        if os.path.commonpath([root_real, dest_real]) != root_real:\n"
            "            emit(False, 'PATH_OUTSIDE_ALLOWED_ROOT', f'Destination resolves outside project root: {candidate}'); sys.exit(19)\n"
            "    except ValueError:\n"
            "        emit(False, 'PATH_OUTSIDE_ALLOWED_ROOT', f'Destination is on a different path domain: {candidate}'); sys.exit(20)\n"
            "emit(True, root_canonical=root_real, parent_canonical=parent_real, destination_path=dest, exists=dest_exists, canonical_path=dest_real)\n"
        )
        cmd = build_remote_python_command(
            py_code,
            [project_root, candidate_path, "1" if allow_missing_parents else "0"],
        )
        res = self.run_command(target, cmd, timeout=timeout)
        line = res.stdout.strip().splitlines()[-1] if res.stdout.strip() else ""
        try:
            data = json.loads(line) if line else {}
        except json.JSONDecodeError:
            data = {}
        if not data.get("ok"):
            raise SSHError(
                data.get("message") or res.stderr.strip() or "Unable to resolve safe structured destination",
                code=data.get("code") or "SSH_FAILED",
                exit_code=res.exit_code,
            )
        return data

    def write_remote_file_atomic(
        self,
        target: Dict[str, Any],
        dest_path: str,
        content_bytes: bytes,
        create: bool,
        expected_sha256: Optional[str] = None,
        backup_dir: Optional[str] = None,
        max_write_bytes: int = 262144,
        timeout: int = 20,
    ) -> Dict[str, Any]:
        """Atomically write content to a remote file with precondition, backup, and symlink checks."""
        py_code = (
            "import sys, os, tempfile, hashlib, json, time\n"
            "dest = sys.argv[1]\n"
            "create = sys.argv[2] == '1'\n"
            "expected_sha = sys.argv[3].strip()\n"
            "backup_dir = sys.argv[4].strip()\n"
            "max_bytes = int(sys.argv[5])\n"
            "content_bytes = sys.stdin.buffer.read()\n"
            "if len(content_bytes) > max_bytes:\n"
            "    print(json.dumps({'ok': False, 'code': 'FILE_TOO_LARGE', 'message': f'Content size ({len(content_bytes)}) exceeds allowed limit of {max_bytes} bytes'}))\n"
            "    sys.exit(10)\n"
            "if b'\\x00' in content_bytes:\n"
            "    print(json.dumps({'ok': False, 'code': 'INVALID_ENCODING', 'message': 'Content contains NUL byte'}))\n"
            "    sys.exit(11)\n"
            "try:\n"
            "    content_bytes.decode('utf-8')\n"
            "except UnicodeDecodeError as e:\n"
            "    print(json.dumps({'ok': False, 'code': 'INVALID_ENCODING', 'message': f'Content is not valid UTF-8: {e}'}))\n"
            "    sys.exit(12)\n"
            "parent_dir = os.path.dirname(dest) or '.'\n"
            "if not os.path.exists(parent_dir):\n"
            "    print(json.dumps({'ok': False, 'code': 'NOT_FOUND', 'message': f'Parent directory does not exist: {parent_dir}'}))\n"
            "    sys.exit(13)\n"
            "if not os.path.isdir(parent_dir):\n"
            "    print(json.dumps({'ok': False, 'code': 'INVALID_PATH', 'message': f'Parent path is not a directory: {parent_dir}'}))\n"
            "    sys.exit(14)\n"
            "if os.path.islink(parent_dir):\n"
            "    print(json.dumps({'ok': False, 'code': 'SYMLINK_WRITE_DENIED', 'message': f'Parent directory is a symlink: {parent_dir}'}))\n"
            "    sys.exit(15)\n"
            "dest_exists = os.path.lexists(dest)\n"
            "if dest_exists and os.path.islink(dest):\n"
            "    print(json.dumps({'ok': False, 'code': 'SYMLINK_WRITE_DENIED', 'message': f'Target file is a symlink: {dest}'}))\n"
            "    sys.exit(16)\n"
            "old_sha256 = None\n"
            "old_mode = None\n"
            "if create:\n"
            "    if dest_exists:\n"
            "        print(json.dumps({'ok': False, 'code': 'FILE_ALREADY_EXISTS', 'message': f'File already exists: {dest}'}))\n"
            "        sys.exit(17)\n"
            "else:\n"
            "    if not dest_exists:\n"
            "        print(json.dumps({'ok': False, 'code': 'NOT_FOUND', 'message': f'File not found: {dest}'}))\n"
            "        sys.exit(18)\n"
            "    if os.path.isdir(dest):\n"
            "        print(json.dumps({'ok': False, 'code': 'INVALID_PATH', 'message': f'Destination is a directory: {dest}'}))\n"
            "        sys.exit(19)\n"
            "    with open(dest, 'rb') as f:\n"
            "        current_data = f.read()\n"
            "    old_sha256 = hashlib.sha256(current_data).hexdigest()\n"
            "    old_mode = os.stat(dest).st_mode\n"
            "    if not expected_sha:\n"
            "        print(json.dumps({'ok': False, 'code': 'WRITE_CONFLICT', 'message': 'expected_sha256 is required for overwriting existing file'}))\n"
            "        sys.exit(20)\n"
            "    if old_sha256.lower() != expected_sha.lower():\n"
            "        print(json.dumps({'ok': False, 'code': 'WRITE_CONFLICT', 'message': f'Hash mismatch: expected {expected_sha}, found {old_sha256}', 'current_sha256': old_sha256}))\n"
            "        sys.exit(21)\n"
            "    if backup_dir:\n"
            "        try:\n"
            "            os.makedirs(backup_dir, exist_ok=True)\n"
            "            ts = time.strftime('%Y%m%d_%H%M%S')\n"
            "            backup_file = os.path.join(backup_dir, f'{ts}_{old_sha256[:12]}.bak')\n"
            "            with open(backup_file, 'wb') as bf:\n"
            "                bf.write(current_data)\n"
            "            backups = sorted([os.path.join(backup_dir, f) for f in os.listdir(backup_dir) if f.endswith('.bak')])\n"
            "            if len(backups) > 5:\n"
            "                for old_b in backups[:-5]:\n"
            "                    try: os.unlink(old_b)\n"
            "                    except OSError: pass\n"
            "        except Exception:\n"
            "            pass\n"
            "new_sha256 = hashlib.sha256(content_bytes).hexdigest()\n"
            "temp_fd, temp_path = tempfile.mkstemp(dir=parent_dir, prefix='.mcp_tmp_')\n"
            "try:\n"
            "    with os.fdopen(temp_fd, 'wb') as tf:\n"
            "        tf.write(content_bytes)\n"
            "        tf.flush()\n"
            "        os.fsync(tf.fileno())\n"
            "    if old_mode is not None:\n"
            "        os.chmod(temp_path, old_mode)\n"
            "    os.replace(temp_path, dest)\n"
            "    try:\n"
            "        dir_fd = os.open(parent_dir, os.O_RDONLY)\n"
            "        try: os.fsync(dir_fd)\n"
            "        finally: os.close(dir_fd)\n"
            "    except Exception:\n"
            "        pass\n"
            "    print(json.dumps({'ok': True, 'created': create, 'old_sha256': old_sha256, 'new_sha256': new_sha256, 'bytes_written': len(content_bytes), 'atomic': True}))\n"
            "    sys.exit(0)\n"
            "except Exception as e:\n"
            "    if os.path.exists(temp_path):\n"
            "        try: os.unlink(temp_path)\n"
            "        except OSError: pass\n"
            "    print(json.dumps({'ok': False, 'code': 'WRITE_FAILED', 'message': str(e)}))\n"
            "    sys.exit(30)\n"
        )
        c_flag = "1" if create else "0"
        e_sha = expected_sha256 or ""
        b_dir = backup_dir or ""
        remote_cmd = build_remote_python_command(
            py_code,
            [dest_path, c_flag, e_sha, b_dir, max_write_bytes],
        )
        res = self.run_command(target, remote_cmd, timeout=timeout, input_data=content_bytes)

        out_str = res.stdout.strip()
        if out_str:
            try:
                lines = out_str.splitlines()
                data = json.loads(lines[-1])
                if not data.get("ok"):
                    raise SSHError(data.get("message", "Write failed"), code=data.get("code", "WRITE_FAILED"))
                return data
            except json.JSONDecodeError:
                pass

        if not res.ok:
            raise SSHError(f"Write execution failed: {res.stderr.strip()}", code="SSH_FAILED", exit_code=res.exit_code)

        raise SSHError("Invalid response from remote write helper", code="WRITE_FAILED")

