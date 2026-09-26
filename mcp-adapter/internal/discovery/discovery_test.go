package discovery

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"path/filepath"
	"testing"
)

func generateTestHostKey(t *testing.T) (string, []byte, string) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}
	// OpenSSH wire format for ed25519 key:
	// string "ssh-ed25519" + string key_bytes
	wireKey := append([]byte("\x00\x00\x00\x0bssh-ed25519\x00\x00\x00\x20"), pub...)
	keyB64 := base64.StdEncoding.EncodeToString(wireKey)
	fp := ComputeSHA256Fingerprint(wireKey)
	return keyB64, wireKey, fp
}

func TestFingerprintAndParsing(t *testing.T) {
	keyB64, rawKey, expectedFP := generateTestHostKey(t)
	fp := ComputeSHA256Fingerprint(rawKey)
	if fp != expectedFP {
		t.Fatalf("expected fingerprint %s, got %s", expectedFP, fp)
	}

	line := "termux-main,192.168.68.84 ssh-ed25519 " + keyB64
	parsed := ParseKnownHostsLine(line)
	if parsed == nil {
		t.Fatalf("failed to parse known_hosts line")
	}

	if len(parsed.Patterns) != 2 || parsed.Patterns[0] != "termux-main" || parsed.Patterns[1] != "192.168.68.84" {
		t.Errorf("unexpected patterns: %v", parsed.Patterns)
	}
	if parsed.KeyType != "ssh-ed25519" {
		t.Errorf("unexpected key type: %s", parsed.KeyType)
	}
	if parsed.Fingerprint != expectedFP {
		t.Errorf("fingerprint mismatch: got %s, want %s", parsed.Fingerprint, expectedFP)
	}
}

func TestKnownHostsRewriteAndCanonical(t *testing.T) {
	tmpDir := t.TempDir()
	khPath := filepath.Join(tmpDir, "known_hosts")

	disc := NewTargetDiscovery(khPath)

	key1B64, _, fp1 := generateTestHostKey(t)
	key2B64, _, fp2 := generateTestHostKey(t)

	// Add key1
	line1 := "target1 ssh-ed25519 " + key1B64
	if err := disc.RewriteKnownHosts("target1", "", line1); err != nil {
		t.Fatalf("RewriteKnownHosts failed: %v", err)
	}

	// Verify target1 has key1
	keys, err := disc.GetCanonicalKeys("target1", "")
	if err != nil {
		t.Fatalf("GetCanonicalKeys failed: %v", err)
	}
	if len(keys) != 1 || keys[0].Fingerprint != fp1 {
		t.Fatalf("expected 1 key with fp %s, got %v", fp1, keys)
	}

	// Add target2
	line2 := "target2 ssh-ed25519 " + key2B64
	if err := disc.RewriteKnownHosts("target2", "", line2); err != nil {
		t.Fatalf("RewriteKnownHosts failed: %v", err)
	}

	// Verify both exist
	k1, _ := disc.GetCanonicalKeys("target1", "")
	k2, _ := disc.GetCanonicalKeys("target2", "")
	if len(k1) != 1 || k1[0].Fingerprint != fp1 {
		t.Errorf("target1 key lost after adding target2")
	}
	if len(k2) != 1 || k2[0].Fingerprint != fp2 {
		t.Errorf("target2 key not found")
	}

	// Replace target1 key
	key3B64, _, fp3 := generateTestHostKey(t)
	line3 := "target1 ssh-ed25519 " + key3B64
	if err := disc.RewriteKnownHosts("target1", "", line3); err != nil {
		t.Fatalf("failed to replace key: %v", err)
	}
	k1After, _ := disc.GetCanonicalKeys("target1", "")
	if len(k1After) != 1 || k1After[0].Fingerprint != fp3 {
		t.Errorf("expected target1 replaced with %s, got %v", fp3, k1After)
	}

	// Remove target1
	if err := disc.RewriteKnownHosts("target1", "", ""); err != nil {
		t.Fatalf("failed to remove target1: %v", err)
	}
	k1Removed, _ := disc.GetCanonicalKeys("target1", "")
	if len(k1Removed) != 0 {
		t.Errorf("expected target1 removed, found: %v", k1Removed)
	}
	// Verify target2 still untouched
	k2Untouched, _ := disc.GetCanonicalKeys("target2", "")
	if len(k2Untouched) != 1 || k2Untouched[0].Fingerprint != fp2 {
		t.Errorf("target2 was affected by target1 deletion")
	}
}

func TestInspectTargetIdentityOffline(t *testing.T) {
	tmpDir := t.TempDir()
	khPath := filepath.Join(tmpDir, "known_hosts")
	disc := NewTargetDiscovery(khPath)

	keyB64, _, _ := generateTestHostKey(t)
	_ = disc.RewriteKnownHosts("offline-target", "", "offline-target ssh-ed25519 "+keyB64)

	res, err := disc.InspectTargetIdentity(TargetConfig{
		ID:   "offline-target",
		Host: "127.0.0.1",
		Port: 65432, // Non-listening port
	})
	if err != nil {
		t.Fatalf("InspectTargetIdentity returned error: %v", err)
	}
	if res.Status != "UNAVAILABLE" {
		t.Errorf("expected status UNAVAILABLE for offline port, got %s", res.Status)
	}
	if len(res.TrustedKeys) != 1 {
		t.Errorf("expected 1 trusted key, got %d", len(res.TrustedKeys))
	}
}
