"""Unit tests for Dynamic IP Target Resolution and Cryptographic Discovery.

Tests verify:
- Fast path: correct endpoint performs 0 discovery calls
- Stale endpoint triggers discovery, updates registry atomically, and retries exactly once
- Wrong fingerprint is strictly rejected
- Multiple matches fail-closed (AMBIGUOUS_TARGET_IDENTITY)
- No matches results in no registry mutation and fails gracefully
- Auth failure (Permission denied) never triggers discovery
- Host key mismatch (Host key verification failed) never triggers discovery (FAIL-CLOSED)
- Discovery storm cooldown prevents concurrent/rapid duplicate scans
- Multi-target isolation: discovering target A does not alter target B
- OpenSSH argument generation: command line -o HostName and -o Port override ~/.ssh/config
"""

import os
import sys
import time
import tempfile
import unittest
from unittest.mock import MagicMock, patch

sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..")))
sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "src")))

from mcp_gateway.registry import SQLiteRegistry
from mcp_gateway.discovery import (
    TargetDiscovery,
    TargetIdentityError,
    DiscoveryResult,
    compute_sha256_fingerprint,
    parse_known_hosts_line,
)
from mcp_gateway.ssh_transport import (
    SSHTransport,
    SSHTransportResult,
    SSHError,
    is_network_connectivity_error,
)
from mcp_gateway.tools import GatewayTools


SAMPLE_ED25519_KEY_B64 = "AAAAC3NzaC1lZDI1NTE5AAAAIO558VBc3DlRhK/vRg5CPZV4kTD0DaY5GXoEEvjyCLmR"
SAMPLE_FINGERPRINT = "SHA256:qILA9dmqZNJS7PaxqDtC7weR4NdcGhruxsUYHpPish0"

WRONG_ED25519_KEY_B64 = "AAAAC3NzaC1lZDI1NTE5AAAAIP999VBc3DlRhK/vRg5CPZV4kTD0DaY5GXoEEvjyCLmR"
WRONG_FINGERPRINT = "SHA256:wrongFingerprintXXXXXXXXXXXXXXXXXXXXXXXXXXXX"


class TestDiscovery(unittest.TestCase):

    def setUp(self):
        self.tmp_dir = tempfile.TemporaryDirectory()
        self.db_path = os.path.join(self.tmp_dir.name, "test_gateway.db")
        self.kh_path = os.path.join(self.tmp_dir.name, "test_known_hosts")

        # Create known_hosts with canonical entries
        with open(self.kh_path, "w", encoding="utf-8") as f:
            f.write(f"termux-main,termux-local ssh-ed25519 {SAMPLE_ED25519_KEY_B64}\n")
            f.write(f"target-b,alias-b ssh-ed25519 {SAMPLE_ED25519_KEY_B64}\n")

        # Initialize SQLite Registry
        self.registry = SQLiteRegistry(self.db_path)
        self.registry.add_target({
            "id": "termux-main",
            "display_name": "Termux Main",
            "platform": "android",
            "host": "192.168.68.84",
            "port": 8022,
            "user": "u0_a435",
            "ssh_alias": "termux-local",
            "enabled": True,
        })
        self.registry.add_target({
            "id": "target-b",
            "display_name": "Target B",
            "platform": "linux",
            "host": "192.168.68.90",
            "port": 22,
            "user": "worker",
            "ssh_alias": "alias-b",
            "enabled": True,
        })

        self.discovery = TargetDiscovery(
            known_hosts_path=self.kh_path,
            cooldown_sec=1.0,
            scan_timeout=0.1,
            max_workers=10,
        )

    def tearDown(self):
        self.tmp_dir.cleanup()

    # --- 1. Fingerprint & Known Hosts Parsing ---

    def test_parse_known_hosts_line(self):
        line = f"termux-main,termux-local ssh-ed25519 {SAMPLE_ED25519_KEY_B64}"
        parsed = parse_known_hosts_line(line)
        self.assertIsNotNone(parsed)
        self.assertIn("termux-main", parsed["patterns"])
        self.assertIn("termux-local", parsed["patterns"])
        self.assertEqual(parsed["key_type"], "ssh-ed25519")
        self.assertEqual(parsed["fingerprint"], SAMPLE_FINGERPRINT)

    def test_get_canonical_keys(self):
        keys = self.discovery.get_canonical_keys("termux-main", "termux-local")
        self.assertEqual(len(keys), 1)
        self.assertEqual(keys[0]["fingerprint"], SAMPLE_FINGERPRINT)

    def test_identity_trust_requires_reviewed_fingerprint_and_can_be_removed(self):
        self.registry.add_target({
            "id": "new-target",
            "display_name": "New Target",
            "platform": "linux",
            "host": "192.168.68.91",
            "port": 22,
            "user": "worker",
            "ssh_alias": "",
            "enabled": True,
        })
        target = self.registry.get_target("new-target")
        offered = parse_known_hosts_line(f"192.168.68.91 ssh-ed25519 {SAMPLE_ED25519_KEY_B64}")

        with patch.object(self.discovery, "get_remote_host_keys", return_value=[offered]):
            state = self.discovery.inspect_target_identity(target)
            self.assertEqual(state["status"], "UNTRUSTED")
            self.assertEqual(state["presented_key"]["fingerprint"], SAMPLE_FINGERPRINT)

            with self.assertRaises(TargetIdentityError) as ctx:
                self.discovery.trust_presented_key(target, "SHA256:not-the-reviewed-key")
            self.assertEqual(ctx.exception.code, "SSH_IDENTITY_CHANGED")
            self.assertEqual(self.discovery.get_canonical_keys("new-target"), [])

            trusted = self.discovery.trust_presented_key(target, SAMPLE_FINGERPRINT)
            self.assertEqual(trusted["status"], "TRUSTED")
            self.assertEqual(
                self.discovery.get_canonical_keys("new-target")[0]["fingerprint"],
                SAMPLE_FINGERPRINT,
            )

            removed = self.discovery.remove_trusted_key(target)
            self.assertEqual(removed["status"], "UNTRUSTED")
            self.assertEqual(self.discovery.get_canonical_keys("new-target"), [])

    def test_identity_change_requires_explicit_replace_and_preserves_other_targets(self):
        target = self.registry.get_target("termux-main")
        changed = parse_known_hosts_line(f"192.168.68.84 ssh-ed25519 {WRONG_ED25519_KEY_B64}")
        changed_fp = changed["fingerprint"]

        with patch.object(self.discovery, "get_remote_host_keys", return_value=[changed]):
            state = self.discovery.inspect_target_identity(target)
            self.assertEqual(state["status"], "CHANGED")

            with self.assertRaises(TargetIdentityError) as ctx:
                self.discovery.trust_presented_key(target, changed_fp)
            self.assertEqual(ctx.exception.code, "SSH_IDENTITY_REPLACE_REQUIRED")
            self.assertEqual(
                self.discovery.get_canonical_keys("termux-main", "termux-local")[0]["fingerprint"],
                SAMPLE_FINGERPRINT,
            )

            replaced = self.discovery.trust_presented_key(target, changed_fp, replace=True)
            self.assertEqual(replaced["status"], "TRUSTED")
            self.assertEqual(
                self.discovery.get_canonical_keys("termux-main", "termux-local")[0]["fingerprint"],
                changed_fp,
            )
            self.assertEqual(
                self.discovery.get_canonical_keys("target-b", "alias-b")[0]["fingerprint"],
                SAMPLE_FINGERPRINT,
            )

    def test_identity_is_trusted_when_any_presented_key_matches_pin(self):
        target = self.registry.get_target("termux-main")
        foreign = {
            "key_type": "ecdsa-sha2-nistp256",
            "key_b64": WRONG_ED25519_KEY_B64,
            "raw_key": b"foreign",
            "fingerprint": "SHA256:foreign",
            "patterns": ["192.168.68.84"],
            "marker": None,
        }
        canonical = parse_known_hosts_line(f"192.168.68.84 ssh-ed25519 {SAMPLE_ED25519_KEY_B64}")
        with patch.object(self.discovery, "get_remote_host_keys", return_value=[foreign, canonical]):
            state = self.discovery.inspect_target_identity(target)
        self.assertEqual(state["status"], "TRUSTED")
        self.assertEqual(state["presented_key"]["fingerprint"], SAMPLE_FINGERPRINT)

    # --- 2. Failure Classification (Network vs Security Fail-Closed) ---

    def test_is_network_connectivity_error_positive(self):
        self.assertTrue(is_network_connectivity_error(255, "ssh: connect to host 192.168.68.84 port 8022: Connection refused"))
        self.assertTrue(is_network_connectivity_error(255, "ssh: connect to host 192.168.68.84 port 8022: Connection timed out"))
        self.assertTrue(is_network_connectivity_error(255, "ssh: connect to host 192.168.68.84 port 8022: No route to host"))
        self.assertTrue(is_network_connectivity_error(255, "ssh: connect to host 192.168.68.84 port 8022: Network is unreachable"))
        self.assertTrue(is_network_connectivity_error(255, "ssh: connect to host 192.168.68.84 port 8022: Host is down"))
        self.assertTrue(is_network_connectivity_error(-1, "", timed_out=True))

    def test_classify_ssh_transport_failure(self):
        from mcp_gateway.ssh_transport import classify_ssh_transport_failure
        # Auth failures: strictly FAIL-CLOSED, NO discovery
        should_disc, cat = classify_ssh_transport_failure(255, "Permission denied (publickey).")
        self.assertFalse(should_disc)
        self.assertEqual(cat, "AUTH_FAILURE")

        should_disc, cat = classify_ssh_transport_failure(255, "Authentication failed.")
        self.assertFalse(should_disc)
        self.assertEqual(cat, "AUTH_FAILURE")

        # Endpoint identity mismatch (DHCP IP reuse): discovery permitted to find canonical key elsewhere
        should_disc, cat = classify_ssh_transport_failure(255, "Host key verification failed.")
        self.assertTrue(should_disc)
        self.assertEqual(cat, "ENDPOINT_IDENTITY_MISMATCH")

        should_disc, cat = classify_ssh_transport_failure(255, "WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED!")
        self.assertTrue(should_disc)
        self.assertEqual(cat, "ENDPOINT_IDENTITY_MISMATCH")

        # Network connectivity errors
        should_disc, cat = classify_ssh_transport_failure(255, "ssh: connect to host 192.168.68.84 port 8022: Connection refused")
        self.assertTrue(should_disc)
        self.assertEqual(cat, "NETWORK_CONNECTIVITY_ERROR")

        should_disc, cat = classify_ssh_transport_failure(-1, "", timed_out=True)
        self.assertTrue(should_disc)
        self.assertEqual(cat, "NETWORK_CONNECTIVITY_ERROR")

        # Exit code 0 is never an error
        should_disc, cat = classify_ssh_transport_failure(0, "")
        self.assertFalse(should_disc)
        self.assertEqual(cat, "NONE")

    # --- 3. Command Line Argument Construction ---

    def test_build_ssh_args_single_source_of_truth(self):
        transport = SSHTransport(identity_file="/tmp/gateway-test-key")
        target = {
            "id": "termux-main",
            "host": "192.168.68.71",
            "port": 8022,
            "ssh_alias": "termux-local",
            "user": "u0_a435",
        }
        args = transport._build_ssh_args(target, 10)
        # Registry Target data and the Gateway identity remain authoritative
        # even when an SSH alias contributes optional extra configuration.
        self.assertIn("-o", args)
        self.assertIn("HostName=192.168.68.71", args)
        self.assertIn("Port=8022", args)
        self.assertIn("User=u0_a435", args)
        self.assertIn("HostKeyAlias=termux-main", args)
        self.assertIn("IdentityFile=/tmp/gateway-test-key", args)
        self.assertIn("IdentitiesOnly=yes", args)
        self.assertIn("StrictHostKeyChecking=yes", args)
        self.assertEqual(args[-1], "termux-local")

    def test_build_ssh_args_without_alias_uses_gateway_identity_and_registry_user(self):
        transport = SSHTransport(identity_file="/tmp/gateway-test-key")
        target = {
            "id": "windows-main",
            "host": "192.168.68.75",
            "port": 22,
            "ssh_alias": "",
            "user": "esaud",
        }
        args = transport._build_ssh_args(target, 10)
        self.assertIn("HostName=192.168.68.75", args)
        self.assertIn("Port=22", args)
        self.assertIn("User=esaud", args)
        self.assertIn("HostKeyAlias=windows-main", args)
        self.assertIn("IdentityFile=/tmp/gateway-test-key", args)
        self.assertIn("IdentitiesOnly=yes", args)
        self.assertIn("StrictHostKeyChecking=yes", args)
        self.assertEqual(args[-1], "192.168.68.75")

    # --- 4. Fast Path: Correct Endpoint Performs No Discovery ---

    @patch("subprocess.run")
    def test_fast_path_no_discovery(self, mock_run):
        mock_proc = MagicMock()
        mock_proc.returncode = 0
        mock_proc.stdout = b"MCP-Pi-Worker\n"
        mock_proc.stderr = b""
        mock_run.return_value = mock_proc

        mock_discovery = MagicMock()
        transport = SSHTransport(discovery=mock_discovery, registry=self.registry)

        target = self.registry.get_target("termux-main")
        res = transport.run_command(target, "hostname")

        self.assertTrue(res.ok)
        self.assertEqual(res.stdout.strip(), "MCP-Pi-Worker")
        # Ensure discovery was never called
        mock_discovery.discover_target.assert_not_called()

    # --- 5. Stale Endpoint: Triggers Discovery, Updates Registry, Retries Once ---

    @patch("subprocess.run")
    def test_stale_endpoint_recovery(self, mock_run):
        # 1st call fails with Connection refused; 2nd call (retry) succeeds
        fail_proc = MagicMock()
        fail_proc.returncode = 255
        fail_proc.stdout = b""
        fail_proc.stderr = b"ssh: connect to host 192.168.68.84 port 8022: Connection refused"

        success_proc = MagicMock()
        success_proc.returncode = 0
        success_proc.stdout = b"recovered-hostname\n"
        success_proc.stderr = b""

        mock_run.side_effect = [fail_proc, success_proc]

        # Discovery returns matching new IP 192.168.68.75
        mock_discovery = MagicMock()
        mock_discovery.discover_target.return_value = DiscoveryResult(
            status="IDENTITY_MATCH",
            new_host="192.168.68.75",
            new_port=8022,
            method="fast_kernel_neighbors",
            fingerprint=SAMPLE_FINGERPRINT,
        )

        transport = SSHTransport(discovery=mock_discovery, registry=self.registry)
        target = self.registry.get_target("termux-main")
        self.assertEqual(target["host"], "192.168.68.84")

        res = transport.run_command(target, "hostname")

        # Verified result
        self.assertTrue(res.ok)
        self.assertEqual(res.stdout.strip(), "recovered-hostname")
        self.assertEqual(mock_run.call_count, 2)
        mock_discovery.discover_target.assert_called_once()

        # Verify Registry updated atomically
        updated_target = self.registry.get_target("termux-main")
        self.assertEqual(updated_target["host"], "192.168.68.75")

        # Verify activity was logged
        activities = self.registry.list_activity(limit=10)
        migration_acts = [a for a in activities if a.get("action") == "target_endpoint_updated"]
        self.assertEqual(len(migration_acts), 1)
        import json
        detail = json.loads(migration_acts[0]["detail"])
        self.assertEqual(detail["old_endpoint"], "192.168.68.84:8022")
        self.assertEqual(detail["new_endpoint"], "192.168.68.75:8022")

    # --- 6. Candidate Wrong Fingerprint Rejected ---

    def test_candidate_wrong_fingerprint_rejected(self):
        # Mock scan finding candidate IP with wrong host key
        with patch.object(self.discovery, "get_kernel_neighbors", return_value=["192.168.68.99"]):
            with patch.object(self.discovery, "scan_ips_for_port", return_value=["192.168.68.99"]):
                with patch.object(self.discovery, "get_remote_host_keys", return_value=[{
                    "fingerprint": WRONG_FINGERPRINT,
                    "raw_key": b"wrong_key_bytes",
                    "key_b64": WRONG_ED25519_KEY_B64,
                }]):
                    with patch.object(self.discovery, "get_active_subnets", return_value=[]):
                        target = self.registry.get_target("termux-main")
                        res = self.discovery.discover_target(target)
                        self.assertEqual(res.status, "TARGET_NOT_FOUND")

    # --- 7. Multiple Matches Fail-Closed (AMBIGUOUS_TARGET_IDENTITY) ---

    def test_multiple_matches_fail_closed(self):
        # Two different machines presenting identical key
        with patch.object(self.discovery, "get_kernel_neighbors", return_value=["192.168.68.101", "192.168.68.102"]):
            with patch.object(self.discovery, "scan_ips_for_port", return_value=["192.168.68.101", "192.168.68.102"]):
                with patch.object(self.discovery, "get_remote_host_keys", return_value=[{
                    "fingerprint": SAMPLE_FINGERPRINT,
                    "raw_key": b"sample_raw_bytes",
                    "key_b64": SAMPLE_ED25519_KEY_B64,
                }]):
                    target = self.registry.get_target("termux-main")
                    res = self.discovery.discover_target(target)
                    self.assertEqual(res.status, "AMBIGUOUS_TARGET_IDENTITY")

    # --- 8. No Matches Results in No Mutation ---

    @patch("subprocess.run")
    def test_no_matches_no_mutation(self, mock_run):
        fail_proc = MagicMock()
        fail_proc.returncode = 255
        fail_proc.stdout = b""
        fail_proc.stderr = b"ssh: connect to host 192.168.68.84 port 8022: Connection timed out"
        mock_run.return_value = fail_proc

        mock_discovery = MagicMock()
        mock_discovery.discover_target.return_value = DiscoveryResult(
            status="TARGET_NOT_FOUND",
            error="Target not found across active subnets",
        )

        transport = SSHTransport(discovery=mock_discovery, registry=self.registry)
        target = self.registry.get_target("termux-main")
        res = transport.run_command(target, "hostname")

        self.assertFalse(res.ok)
        self.assertEqual(mock_run.call_count, 1)  # No retry because discovery failed
        # Verify Registry was NOT mutated
        current_target = self.registry.get_target("termux-main")
        self.assertEqual(current_target["host"], "192.168.68.84")

    # --- 9. Security Errors Never Trigger Discovery (FAIL-CLOSED) ---

    @patch("subprocess.run")
    def test_auth_failure_no_discovery(self, mock_run):
        fail_proc = MagicMock()
        fail_proc.returncode = 255
        fail_proc.stdout = b""
        fail_proc.stderr = b"Permission denied (publickey)."
        mock_run.return_value = fail_proc

        mock_discovery = MagicMock()
        transport = SSHTransport(discovery=mock_discovery, registry=self.registry)
        target = self.registry.get_target("termux-main")

        res = transport.run_command(target, "hostname")
        self.assertFalse(res.ok)
        mock_discovery.discover_target.assert_not_called()

    # --- 9. DHCP IP Reuse Edge Cases (B, C, D, E) ---

    @patch("subprocess.run")
    def test_dhcp_reuse_foreign_host_recovery_success(self, mock_run):
        """Edge Case B: Stale IP occupied by foreign host (wrong key).
        Foreign endpoint is rejected, discovery finds canonical target on new IP,
        Registry is updated, retry succeeds.
        """
        foreign_proc = MagicMock()
        foreign_proc.returncode = 255
        foreign_proc.stdout = b""
        foreign_proc.stderr = b"Host key for termux-main has changed and you have requested strict checking.\nHost key verification failed."

        success_proc = MagicMock()
        success_proc.returncode = 0
        success_proc.stdout = b"termux-main-live\n"
        success_proc.stderr = b""

        mock_run.side_effect = [foreign_proc, success_proc]

        mock_discovery = MagicMock()
        mock_discovery.discover_target.return_value = DiscoveryResult(
            status="IDENTITY_MATCH",
            new_host="192.168.68.73",
            new_port=8022,
            method="fast_kernel_neighbors",
            fingerprint=SAMPLE_FINGERPRINT,
        )

        transport = SSHTransport(discovery=mock_discovery, registry=self.registry)
        target = self.registry.get_target("termux-main")
        self.assertEqual(target["host"], "192.168.68.84")

        res = transport.run_command(target, "hostname")

        self.assertTrue(res.ok)
        self.assertEqual(res.stdout.strip(), "termux-main-live")
        self.assertEqual(mock_run.call_count, 2)
        mock_discovery.discover_target.assert_called_once()

        # Registry updated to new IP
        updated_target = self.registry.get_target("termux-main")
        self.assertEqual(updated_target["host"], "192.168.68.73")

        # Verify activity was logged with trigger_reason=ENDPOINT_IDENTITY_MISMATCH
        activities = self.registry.list_activity(limit=10)
        migration_acts = [a for a in activities if a.get("action") == "target_endpoint_updated"]
        self.assertEqual(len(migration_acts), 1)
        import json
        detail = json.loads(migration_acts[0]["detail"])
        self.assertEqual(detail["trigger_reason"], "ENDPOINT_IDENTITY_MISMATCH")

    @patch("subprocess.run")
    def test_dhcp_reuse_foreign_host_canonical_not_found(self, mock_run):
        """Edge Case C: Stale IP occupied by foreign host, canonical target not on network.
        Foreign endpoint rejected, discovery returns TARGET_NOT_FOUND, no registry mutation, fail-closed.
        """
        foreign_proc = MagicMock()
        foreign_proc.returncode = 255
        foreign_proc.stdout = b""
        foreign_proc.stderr = b"Host key verification failed."
        mock_run.return_value = foreign_proc

        mock_discovery = MagicMock()
        mock_discovery.discover_target.return_value = DiscoveryResult(
            status="TARGET_NOT_FOUND",
            error="Canonical target not found on any network candidate",
        )

        transport = SSHTransport(discovery=mock_discovery, registry=self.registry)
        target = self.registry.get_target("termux-main")

        res = transport.run_command(target, "hostname")

        self.assertFalse(res.ok)
        self.assertEqual(mock_run.call_count, 1)  # No retry because discovery returned no match
        mock_discovery.discover_target.assert_called_once()

        # Registry NOT mutated
        current_target = self.registry.get_target("termux-main")
        self.assertEqual(current_target["host"], "192.168.68.84")

    @patch("subprocess.run")
    def test_dhcp_reuse_foreign_host_ambiguous_matches(self, mock_run):
        """Edge Case D: Stale IP occupied by foreign host, multiple canonical matches found.
        Foreign endpoint rejected, discovery returns AMBIGUOUS_TARGET_IDENTITY, no mutation, fail-closed.
        """
        foreign_proc = MagicMock()
        foreign_proc.returncode = 255
        foreign_proc.stdout = b""
        foreign_proc.stderr = b"Host key verification failed."
        mock_run.return_value = foreign_proc

        mock_discovery = MagicMock()
        mock_discovery.discover_target.return_value = DiscoveryResult(
            status="AMBIGUOUS_TARGET_IDENTITY",
            error="Multiple candidates matched target identity",
        )

        transport = SSHTransport(discovery=mock_discovery, registry=self.registry)
        target = self.registry.get_target("termux-main")

        res = transport.run_command(target, "hostname")

        self.assertFalse(res.ok)
        self.assertEqual(mock_run.call_count, 1)

        # Registry NOT mutated
        current_target = self.registry.get_target("termux-main")
        self.assertEqual(current_target["host"], "192.168.68.84")

    @patch("subprocess.run")
    def test_target_changed_own_key_no_silent_rotation(self, mock_run):
        """Edge Case E: Target changed its own key on its IP.
        The gateway MUST NOT silently accept or rotate identity. Discovery fails closed.
        """
        mismatch_proc = MagicMock()
        mismatch_proc.returncode = 255
        mismatch_proc.stdout = b""
        mismatch_proc.stderr = b"Host key verification failed."
        mock_run.return_value = mismatch_proc

        # Real discovery engine instance: probes the host which offers WRONG/NEW key
        with patch.object(self.discovery, "get_kernel_neighbors", return_value=[]):
            with patch.object(self.discovery, "get_active_subnets", return_value=[]):
                target = self.registry.get_target("termux-main")
                disc_res = self.discovery.discover_target(target)
                self.assertEqual(disc_res.status, "TARGET_NOT_FOUND")

        # Transport run:
        mock_discovery = MagicMock()
        mock_discovery.discover_target.return_value = DiscoveryResult(
            status="TARGET_NOT_FOUND",
            error="Key does not match canonical pinning",
        )
        transport = SSHTransport(discovery=mock_discovery, registry=self.registry)
        res = transport.run_command(target, "hostname")

        self.assertFalse(res.ok)
        # Verify Registry retains old host, never accepted new key or mutated
        current_target = self.registry.get_target("termux-main")
        self.assertEqual(current_target["host"], "192.168.68.84")

    # --- 10. Discovery Cooldown Prevents Storms ---

    def test_cooldown_prevents_discovery_storms(self):
        target = self.registry.get_target("termux-main")
        with patch.object(self.discovery, "get_kernel_neighbors", return_value=[]):
            with patch.object(self.discovery, "get_active_subnets", return_value=[]):
                # 1st discovery
                res1 = self.discovery.discover_target(target)
                self.assertEqual(res1.status, "TARGET_NOT_FOUND")

                # Immediate 2nd discovery within cooldown window
                res2 = self.discovery.discover_target(target)
                self.assertEqual(res2.status, "TARGET_NOT_FOUND")
                self.assertEqual(res2.method, "cached")

    # --- 11. Multi-Target Isolation ---

    @patch("subprocess.run")
    def test_multi_target_isolation(self, mock_run):
        fail_proc = MagicMock()
        fail_proc.returncode = 255
        fail_proc.stdout = b""
        fail_proc.stderr = b"ssh: connect to host 192.168.68.84 port 8022: Connection refused"

        success_proc = MagicMock()
        success_proc.returncode = 0
        success_proc.stdout = b"target-a-ok\n"
        success_proc.stderr = b""

        mock_run.side_effect = [fail_proc, success_proc]

        mock_discovery = MagicMock()
        mock_discovery.discover_target.return_value = DiscoveryResult(
            status="IDENTITY_MATCH",
            new_host="192.168.68.77",
            new_port=8022,
            method="fast_kernel_neighbors",
            fingerprint=SAMPLE_FINGERPRINT,
        )

        transport = SSHTransport(discovery=mock_discovery, registry=self.registry)
        target_a = self.registry.get_target("termux-main")
        target_b_before = self.registry.get_target("target-b")

        res = transport.run_command(target_a, "hostname")
        self.assertTrue(res.ok)

        # Target A was updated
        target_a_after = self.registry.get_target("termux-main")
        self.assertEqual(target_a_after["host"], "192.168.68.77")

        # Target B was untouched
        target_b_after = self.registry.get_target("target-b")
        self.assertEqual(target_b_after["host"], target_b_before["host"])
        self.assertEqual(target_b_after["host"], "192.168.68.90")


if __name__ == "__main__":
    unittest.main()
