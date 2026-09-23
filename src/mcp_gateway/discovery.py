"""Dynamic IP Target Resolution & Cryptographic Discovery for MCP-Pi Gateway.

Principles:
- TARGET_ID + SSH HOST KEY = IDENTITY
- IP + PORT = MUTABLE ENDPOINT
- IP is never identity. MAC is never identity.
- Fail-Closed: Never guess. Wrong fingerprint -> REJECT. Ambiguous -> FAIL-CLOSED.
- Tiered on-demand discovery: Fast (kernel neighbors) -> Fallback (active LAN subnet scan).
"""

import os
import sys
import time
import socket
import shlex
import base64
import hashlib
import ipaddress
import threading
import subprocess
import tempfile
from dataclasses import dataclass, field
from typing import Dict, Any, List, Optional, Tuple, Set
import concurrent.futures


@dataclass
class DiscoveryResult:
    status: str  # "IDENTITY_MATCH", "TARGET_NOT_FOUND", "AMBIGUOUS_TARGET_IDENTITY", "ERROR"
    new_host: Optional[str] = None
    new_port: Optional[int] = None
    method: Optional[str] = None  # "fast_kernel_neighbors", "subnet_scan", "cached"
    duration_ms: int = 0
    candidate_key: Optional[str] = None
    fingerprint: Optional[str] = None
    error: Optional[str] = None


class TargetIdentityError(Exception):
    """Raised when target SSH identity inspection or mutation fails safely."""

    def __init__(self, message: str, code: str = "SSH_IDENTITY_ERROR"):
        super().__init__(message)
        self.code = code


def compute_sha256_fingerprint(raw_key_bytes: bytes) -> str:
    """Compute OpenSSH standard SHA256 base64 fingerprint."""
    digest = hashlib.sha256(raw_key_bytes).digest()
    b64 = base64.b64encode(digest).decode("utf-8").rstrip("=")
    return f"SHA256:{b64}"


def parse_known_hosts_line(line: str) -> Optional[Dict[str, Any]]:
    """Parse an OpenSSH known_hosts line into patterns, key_type, and raw bytes."""
    line = line.strip()
    if not line or line.startswith("#"):
        return None
    
    parts = line.split()
    if len(parts) < 3:
        return None
    
    # Handle marker if present (@cert-authority, @revoked)
    idx = 0
    marker = None
    if parts[0].startswith("@"):
        marker = parts[0]
        idx += 1
        if len(parts) < idx + 3:
            return None

    host_pattern = parts[idx]
    key_type = parts[idx + 1]
    key_b64 = parts[idx + 2]

    patterns = [p.strip() for p in host_pattern.split(",")]
    try:
        raw_key = base64.b64decode(key_b64)
        fp = compute_sha256_fingerprint(raw_key)
    except Exception:
        return None

    return {
        "patterns": patterns,
        "key_type": key_type,
        "key_b64": key_b64,
        "raw_key": raw_key,
        "fingerprint": fp,
        "marker": marker,
    }


class TargetDiscovery:
    """Discovers target workers on dynamic network endpoints with cryptographic verification."""

    DEFAULT_KNOWN_HOSTS = "/home/mcp-gateway/.ssh/known_hosts"

    def __init__(
        self,
        known_hosts_path: Optional[str] = None,
        cooldown_sec: float = 20.0,
        scan_timeout: float = 0.35,
        max_workers: int = 35,
    ):
        if known_hosts_path:
            self.known_hosts_path = os.path.expanduser(known_hosts_path)
        elif os.path.isfile(self.DEFAULT_KNOWN_HOSTS):
            self.known_hosts_path = self.DEFAULT_KNOWN_HOSTS
        else:
            self.known_hosts_path = os.path.expanduser("~/.ssh/known_hosts")

        self.cooldown_sec = cooldown_sec
        self.scan_timeout = scan_timeout
        self.max_workers = max_workers

        self._lock = threading.Lock()
        self._target_locks: Dict[str, threading.Lock] = {}
        self._last_attempt: Dict[str, float] = {}
        self._last_result: Dict[str, DiscoveryResult] = {}
        self._known_hosts_lock = threading.Lock()

    def _get_target_lock(self, target_id: str) -> threading.Lock:
        with self._lock:
            if target_id not in self._target_locks:
                self._target_locks[target_id] = threading.Lock()
            return self._target_locks[target_id]

    def get_canonical_keys(self, target_id: str, alias: Optional[str] = None) -> List[Dict[str, Any]]:
        """Return list of canonical host key entries matching target_id or alias in known_hosts."""
        if not os.path.isfile(self.known_hosts_path):
            return []

        search_names = {target_id}
        if alias:
            search_names.add(alias)

        matched = []
        try:
            with open(self.known_hosts_path, "r", encoding="utf-8", errors="replace") as f:
                for line in f:
                    parsed = parse_known_hosts_line(line)
                    if not parsed:
                        continue
                    # Match pattern
                    for name in search_names:
                        if name in parsed["patterns"]:
                            matched.append(parsed)
                            break
        except Exception:
            return []

        return matched

    @staticmethod
    def _validate_known_hosts_name(value: str) -> str:
        value = (value or "").strip()
        if not value or any(ch.isspace() for ch in value) or "," in value or value.startswith("@"):
            raise TargetIdentityError("Invalid Target ID or SSH alias for known_hosts", code="INVALID_TARGET_IDENTITY")
        return value

    @staticmethod
    def _preferred_remote_key(keys: List[Dict[str, Any]]) -> Optional[Dict[str, Any]]:
        if not keys:
            return None
        priority = {
            "ssh-ed25519": 0,
            "ecdsa-sha2-nistp256": 1,
            "ecdsa-sha2-nistp384": 2,
            "ecdsa-sha2-nistp521": 3,
            "ssh-rsa": 4,
        }
        return sorted(keys, key=lambda item: priority.get(item.get("key_type", ""), 99))[0]

    def inspect_target_identity(self, target: Dict[str, Any]) -> Dict[str, Any]:
        """Inspect pinned and currently presented host identity without mutating trust."""
        target_id = self._validate_known_hosts_name(target.get("id") or target.get("ssh_alias", ""))
        alias = target.get("ssh_alias") or None
        if alias:
            self._validate_known_hosts_name(alias)
        host = (target.get("host") or "").strip()
        port = int(target.get("port", 22))
        if not host or port <= 0 or port > 65535:
            raise TargetIdentityError("Target endpoint is incomplete or invalid", code="INVALID_TARGET_ENDPOINT")

        trusted = self.get_canonical_keys(target_id, alias)
        presented_keys = self.get_remote_host_keys(host, port)
        trusted_fingerprints = {item["fingerprint"] for item in trusted}
        matched = next(
            (item for item in presented_keys if item["fingerprint"] in trusted_fingerprints),
            None,
        )
        presented = matched or self._preferred_remote_key(presented_keys)

        trusted_public = [
            {"key_type": item["key_type"], "fingerprint": item["fingerprint"]}
            for item in trusted
        ]
        presented_public = (
            {"key_type": presented["key_type"], "fingerprint": presented["fingerprint"]}
            if presented
            else None
        )

        if not presented:
            status = "UNAVAILABLE"
        elif not trusted:
            status = "UNTRUSTED"
        elif matched:
            status = "TRUSTED"
        else:
            status = "CHANGED"

        return {
            "status": status,
            "target_id": target_id,
            "endpoint": f"{host}:{port}",
            "trusted_keys": trusted_public,
            "presented_key": presented_public,
        }

    def _rewrite_known_hosts(self, target_id: str, alias: Optional[str], new_line: Optional[str]) -> None:
        """Atomically replace this target's pin while preserving unrelated known_hosts entries."""
        target_id = self._validate_known_hosts_name(target_id)
        names = {target_id}
        if alias:
            names.add(self._validate_known_hosts_name(alias))

        path = self.known_hosts_path
        directory = os.path.dirname(path) or "."
        os.makedirs(directory, mode=0o700, exist_ok=True)

        with self._known_hosts_lock:
            existing_lines: List[str] = []
            if os.path.exists(path):
                with open(path, "r", encoding="utf-8", errors="replace") as f:
                    existing_lines = f.readlines()

            kept: List[str] = []
            for line in existing_lines:
                parsed = parse_known_hosts_line(line)
                if parsed and names.intersection(parsed["patterns"]):
                    continue
                kept.append(line if line.endswith("\n") else line + "\n")

            if new_line:
                kept.append(new_line.rstrip("\n") + "\n")

            old_mode = 0o600
            if os.path.exists(path):
                old_mode = os.stat(path).st_mode & 0o777

            fd, tmp_path = tempfile.mkstemp(prefix=".known_hosts.", dir=directory, text=True)
            try:
                os.fchmod(fd, old_mode)
                with os.fdopen(fd, "w", encoding="utf-8") as f:
                    f.writelines(kept)
                    f.flush()
                    os.fsync(f.fileno())
                os.replace(tmp_path, path)
            except Exception:
                try:
                    os.close(fd)
                except OSError:
                    pass
                try:
                    os.unlink(tmp_path)
                except OSError:
                    pass
                raise

    def trust_presented_key(
        self,
        target: Dict[str, Any],
        expected_fingerprint: str,
        *,
        replace: bool = False,
    ) -> Dict[str, Any]:
        """Pin exactly the host key fingerprint that the administrator reviewed."""
        target_id = self._validate_known_hosts_name(target.get("id") or target.get("ssh_alias", ""))
        alias = target.get("ssh_alias") or None
        host = (target.get("host") or "").strip()
        port = int(target.get("port", 22))
        if not expected_fingerprint or not expected_fingerprint.startswith("SHA256:"):
            raise TargetIdentityError("A reviewed SHA256 fingerprint is required", code="INVALID_FINGERPRINT")

        presented_keys = self.get_remote_host_keys(host, port)
        presented = next(
            (item for item in presented_keys if item.get("fingerprint") == expected_fingerprint),
            None,
        )
        if not presented:
            raise TargetIdentityError(
                "The Target no longer presents the reviewed host fingerprint",
                code="SSH_IDENTITY_CHANGED",
            )

        trusted = self.get_canonical_keys(target_id, alias)
        if any(item["fingerprint"] == expected_fingerprint for item in trusted):
            return self.inspect_target_identity(target)
        if trusted and not replace:
            raise TargetIdentityError(
                "A different host fingerprint is already pinned; explicit replacement is required",
                code="SSH_IDENTITY_REPLACE_REQUIRED",
            )

        self._rewrite_known_hosts(
            target_id,
            alias,
            f"{target_id} {presented['key_type']} {presented['key_b64']}",
        )
        result = self.inspect_target_identity(target)
        if result["status"] != "TRUSTED":
            raise TargetIdentityError(
                "The reviewed key was pinned, but the Target no longer presents it",
                code="SSH_IDENTITY_CHANGED",
            )
        return result

    def remove_trusted_key(self, target: Dict[str, Any]) -> Dict[str, Any]:
        """Remove this Target's pinned host identity without trusting any replacement."""
        target_id = self._validate_known_hosts_name(target.get("id") or target.get("ssh_alias", ""))
        alias = target.get("ssh_alias") or None
        self._rewrite_known_hosts(target_id, alias, None)
        return self.inspect_target_identity(target)

    def get_kernel_neighbors(self) -> List[str]:
        """Fast Tier 1 discovery: read ARP cache / kernel neighbor table."""
        neighbors: Set[str] = set()

        # Method A: /proc/net/arp (standard Linux, near 0 ms)
        if os.path.isfile("/proc/net/arp"):
            try:
                with open("/proc/net/arp", "r") as f:
                    lines = f.readlines()
                for line in lines[1:]:
                    parts = line.split()
                    if len(parts) >= 4:
                        ip = parts[0]
                        flags = parts[2]
                        # 0x0 means incomplete / unresolved
                        if flags != "0x0" and not ip.startswith("0.") and not ip.startswith("127."):
                            neighbors.add(ip)
            except Exception:
                pass

        # Method B: ip -j neigh (fallback or supplement)
        if not neighbors:
            try:
                proc = subprocess.run(
                    ["ip", "-j", "neigh"],
                    stdout=subprocess.PIPE,
                    stderr=subprocess.PIPE,
                    text=True,
                    timeout=2,
                    check=False,
                )
                if proc.returncode == 0 and proc.stdout.strip():
                    import json
                    entries = json.loads(proc.stdout)
                    for e in entries:
                        dst = e.get("dst")
                        state = e.get("state", [])
                        if dst and "FAILED" not in state:
                            neighbors.add(dst)
            except Exception:
                pass

        return sorted(list(neighbors))

    def get_active_subnets(self) -> List[str]:
        """Discover active IPv4 LAN subnets dynamically (e.g. 192.168.68.0/22)."""
        subnets: Set[str] = set()

        # Try ip -j -4 route show
        try:
            proc = subprocess.run(
                ["ip", "-j", "-4", "route", "show"],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
                timeout=2,
                check=False,
            )
            if proc.returncode == 0 and proc.stdout.strip():
                import json
                routes = json.loads(proc.stdout)
                for r in routes:
                    dst = r.get("dst")
                    dev = r.get("dev", "")
                    # Skip default, loopback, and link-local (169.254)
                    if dst and dst != "default" and not dev.startswith("lo") and not dst.startswith("169.254."):
                        subnets.add(dst)
        except Exception:
            pass

        # Try ip -j -4 addr show if routes didn't give a network
        if not subnets:
            try:
                proc = subprocess.run(
                    ["ip", "-j", "-4", "addr", "show"],
                    stdout=subprocess.PIPE,
                    stderr=subprocess.PIPE,
                    text=True,
                    timeout=2,
                    check=False,
                )
                if proc.returncode == 0 and proc.stdout.strip():
                    import json
                    ifaces = json.loads(proc.stdout)
                    for iface in ifaces:
                        if iface.get("ifname", "").startswith("lo"):
                            continue
                        for addr in iface.get("addr_info", []):
                            local = addr.get("local")
                            prefix = addr.get("prefixlen")
                            if local and prefix and not local.startswith("127.") and not local.startswith("169.254."):
                                net = ipaddress.IPv4Interface(f"{local}/{prefix}").network
                                subnets.add(str(net))
            except Exception:
                pass

        return sorted(list(subnets))

    def probe_port(self, ip: str, port: int, timeout: Optional[float] = None) -> bool:
        """Test if TCP port is open using socket connect with strict timeout."""
        to = timeout or self.scan_timeout
        try:
            s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
            s.settimeout(to)
            res = s.connect_ex((ip, port))
            s.close()
            return res == 0
        except Exception:
            return False

    def scan_ips_for_port(self, ips: List[str], port: int) -> List[str]:
        """Concurrently probe a list of candidate IPs for open TCP port."""
        if not ips:
            return []

        open_ips = []
        workers = min(self.max_workers, len(ips))
        with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as executor:
            futures = {executor.submit(self.probe_port, ip, port): ip for ip in ips}
            for fut in concurrent.futures.as_completed(futures):
                ip = futures[fut]
                try:
                    if fut.result():
                        open_ips.append(ip)
                except Exception:
                    pass
        return sorted(open_ips)

    def get_remote_host_keys(self, ip: str, port: int, timeout: float = 2.0) -> List[Dict[str, Any]]:
        """Retrieve all remote SSH host keys using ssh-keyscan."""
        keys = []
        try:
            proc = subprocess.run(
                ["ssh-keyscan", "-p", str(port), "-t", "ed25519,ecdsa,rsa", "-T", str(int(timeout)), ip],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
                timeout=timeout + 1.0,
                check=False,
            )
            if proc.returncode == 0 and proc.stdout.strip():
                for line in proc.stdout.splitlines():
                    parsed = parse_known_hosts_line(line)
                    if parsed:
                        keys.append(parsed)
        except Exception:
            pass

        return keys

    def get_remote_host_key(self, ip: str, port: int, timeout: float = 2.0) -> Optional[Dict[str, Any]]:
        """Retrieve remote SSH host key (compatibility helper)."""
        keys = self.get_remote_host_keys(ip, port, timeout)
        return keys[0] if keys else None

    def _prioritize_hosts(self, subnets: List[str], current_ip: Optional[str] = None) -> List[str]:
        """Expand subnets into host IPs, prioritizing the local /24 segment first."""
        all_hosts: List[str] = []
        priority_hosts: List[str] = []

        local_prefix = None
        if current_ip:
            parts = current_ip.split(".")
            if len(parts) == 4:
                local_prefix = f"{parts[0]}.{parts[1]}.{parts[2]}."

        for sub in subnets:
            try:
                net = ipaddress.ip_network(sub, strict=False)
                for h in net.hosts():
                    h_str = str(h)
                    if local_prefix and h_str.startswith(local_prefix):
                        priority_hosts.append(h_str)
                    else:
                        all_hosts.append(h_str)
            except Exception:
                continue

        return priority_hosts + all_hosts

    def discover_target(
        self,
        target: Dict[str, Any],
        known_hosts_path: Optional[str] = None,
    ) -> DiscoveryResult:
        """Orchestrate tiered discovery: Fast (neighbors) -> Fallback (subnet scan).
        
        Enforces storm cooldown and strict identity matching against pinned host keys.
        """
        start_time = time.monotonic()
        target_id = target.get("id") or target.get("ssh_alias", "unknown")
        alias = target.get("ssh_alias")
        port = int(target.get("port", 22))
        current_host = target.get("host")

        # Use overridden known_hosts if supplied for testing
        if known_hosts_path:
            old_kh = self.known_hosts_path
            self.known_hosts_path = known_hosts_path

        target_lock = self._get_target_lock(target_id)
        if not target_lock.acquire(blocking=False):
            # Another thread is actively discovering this target; wait or return cached
            target_lock.acquire()  # Wait for it to complete
            target_lock.release()
            duration_ms = int((time.monotonic() - start_time) * 1000)
            with self._lock:
                last_res = self._last_result.get(target_id)
                if last_res:
                    return DiscoveryResult(
                        status=last_res.status,
                        new_host=last_res.new_host,
                        new_port=last_res.new_port,
                        method="cached",
                        duration_ms=duration_ms,
                        fingerprint=last_res.fingerprint,
                    )

        try:
            # Check cooldown
            now = time.monotonic()
            with self._lock:
                last_time = self._last_attempt.get(target_id, 0.0)
                if (now - last_time) < self.cooldown_sec and target_id in self._last_result:
                    last_res = self._last_result[target_id]
                    duration_ms = int((time.monotonic() - start_time) * 1000)
                    return DiscoveryResult(
                        status=last_res.status,
                        new_host=last_res.new_host,
                        new_port=last_res.new_port,
                        method="cached",
                        duration_ms=duration_ms,
                        fingerprint=last_res.fingerprint,
                        error=last_res.error,
                    )

            # Step 0: Retrieve pinned canonical keys for target_id
            canonical_entries = self.get_canonical_keys(target_id, alias)
            if not canonical_entries:
                res = DiscoveryResult(
                    status="ERROR",
                    error=f"No pinned host key found in known_hosts for target '{target_id}'",
                    duration_ms=int((time.monotonic() - start_time) * 1000),
                )
                with self._lock:
                    self._last_attempt[target_id] = now
                    self._last_result[target_id] = res
                return res

            canonical_fingerprints = {e["fingerprint"] for e in canonical_entries}
            canonical_raw_keys = {e["raw_key"] for e in canonical_entries}

            def verify_candidate(ip: str) -> Optional[Tuple[str, str]]:
                """Returns (fingerprint, raw_key_b64) if candidate matches, else None."""
                remote_keys = self.get_remote_host_keys(ip, port)
                for remote_key_info in remote_keys:
                    if (
                        remote_key_info["fingerprint"] in canonical_fingerprints
                        or remote_key_info["raw_key"] in canonical_raw_keys
                    ):
                        return (remote_key_info["fingerprint"], remote_key_info["key_b64"])
                return None

            # --- TIER 1: FAST DISCOVERY (Kernel neighbors / ARP cache) ---
            neighbors = self.get_kernel_neighbors()
            # If current_host in neighbors, exclude it to avoid redundant re-probe
            candidate_neighbors = [n for n in neighbors if n != current_host]

            open_neighbor_ips = self.scan_ips_for_port(candidate_neighbors, port)
            tier1_matches = []
            for c_ip in open_neighbor_ips:
                v = verify_candidate(c_ip)
                if v:
                    tier1_matches.append((c_ip, v[0], v[1]))

            if len(tier1_matches) == 1:
                match_ip, fp, key_b64 = tier1_matches[0]
                duration_ms = int((time.monotonic() - start_time) * 1000)
                res = DiscoveryResult(
                    status="IDENTITY_MATCH",
                    new_host=match_ip,
                    new_port=port,
                    method="fast_kernel_neighbors",
                    duration_ms=duration_ms,
                    candidate_key=key_b64,
                    fingerprint=fp,
                )
                with self._lock:
                    self._last_attempt[target_id] = now
                    self._last_result[target_id] = res
                return res

            elif len(tier1_matches) > 1:
                duration_ms = int((time.monotonic() - start_time) * 1000)
                res = DiscoveryResult(
                    status="AMBIGUOUS_TARGET_IDENTITY",
                    error=f"Multiple hosts ({[m[0] for m in tier1_matches]}) matched target identity",
                    duration_ms=duration_ms,
                )
                with self._lock:
                    self._last_attempt[target_id] = now
                    self._last_result[target_id] = res
                return res

            # --- TIER 2: FALLBACK DISCOVERY (Dynamic subnet scan) ---
            subnets = self.get_active_subnets()
            all_candidate_hosts = self._prioritize_hosts(subnets, current_ip=current_host)
            # Remove current_host and already-checked neighbors
            checked_set = set(candidate_neighbors)
            if current_host:
                checked_set.add(current_host)
            remaining_hosts = [h for h in all_candidate_hosts if h not in checked_set]

            open_subnet_ips = self.scan_ips_for_port(remaining_hosts, port)
            tier2_matches = []
            for c_ip in open_subnet_ips:
                v = verify_candidate(c_ip)
                if v:
                    tier2_matches.append((c_ip, v[0], v[1]))

            duration_ms = int((time.monotonic() - start_time) * 1000)

            if len(tier2_matches) == 1:
                match_ip, fp, key_b64 = tier2_matches[0]
                res = DiscoveryResult(
                    status="IDENTITY_MATCH",
                    new_host=match_ip,
                    new_port=port,
                    method="subnet_scan",
                    duration_ms=duration_ms,
                    candidate_key=key_b64,
                    fingerprint=fp,
                )
            elif len(tier2_matches) > 1:
                res = DiscoveryResult(
                    status="AMBIGUOUS_TARGET_IDENTITY",
                    error=f"Multiple hosts ({[m[0] for m in tier2_matches]}) matched target identity",
                    duration_ms=duration_ms,
                )
            else:
                res = DiscoveryResult(
                    status="TARGET_NOT_FOUND",
                    duration_ms=duration_ms,
                    error=f"Target '{target_id}' not found on port {port} across active subnets",
                )

            with self._lock:
                self._last_attempt[target_id] = now
                self._last_result[target_id] = res
            return res

        finally:
            if known_hosts_path:
                self.known_hosts_path = old_kh
            target_lock.release()
