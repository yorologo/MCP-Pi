package remote

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mcp-gateway-adapter/internal/discovery"
	"mcp-gateway-adapter/internal/registry"
)

type fakeTargetDiscoverer struct {
	result *discovery.DiscoveryResult
	err    error
	calls  int
}

func (f *fakeTargetDiscoverer) FindMovedTarget(discovery.TargetConfig) (*discovery.DiscoveryResult, error) {
	f.calls++
	return f.result, f.err
}

func newSSHRecoveryStore(t *testing.T, target registry.Target) *registry.Store {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "recovery.db")
	if err := registry.MigratePath(ctx, path); err != nil {
		t.Fatal(err)
	}
	store, err := registry.OpenStore(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.AddTarget(ctx, target); err != nil {
		t.Fatal(err)
	}
	return store
}

func recoverySSHShim(t *testing.T, newHostSucceeds bool, stderrMessage string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	shim := filepath.Join(dir, "ssh-recovery-shim")
	counter := filepath.Join(dir, "count")
	newAction := "printf 'recovered\\n'; exit 0"
	if !newHostSucceeds {
		newAction = "printf '%s\\n' 'ssh: connect to host new-host port 22: Connection refused' >&2; exit 255"
	}
	script := fmt.Sprintf("#!/bin/sh\n"+
		"count=0\n"+
		"[ ! -f %q ] || count=$(cat %q)\n"+
		"count=$((count+1))\n"+
		"printf '%%s\\n' \"$count\" > %q\n"+
		"host=\"\"\n"+
		"for arg\n"+
		"do\n"+
		"  case \"$arg\" in HostName=*) host=$(printf '%%s' \"$arg\" | sed 's/^HostName=//') ;; esac\n"+
		"done\n"+
		"if [ \"$host\" = \"new-host\" ]; then\n"+
		"  %s\n"+
		"fi\n"+
		"printf '%%s\\n' %q >&2\n"+
		"exit 255\n",
		counter, counter, counter, newAction, stderrMessage)
	if err := os.WriteFile(shim, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return shim, counter
}

func TestClassifySSHFailureIsFailClosed(t *testing.T) {
	tests := []struct {
		name       string
		stderr     string
		want       bool
		wantReason string
	}{
		{"refused", "ssh: connect to host 192.0.2.1 port 22: Connection refused", true, "NETWORK_CONNECTIVITY_ERROR"},
		{"connect timeout", "ssh: connect to host 192.0.2.1 port 22: Connection timed out", true, "NETWORK_CONNECTIVITY_ERROR"},
		{"host key mismatch", "WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED!", true, "ENDPOINT_IDENTITY_MISMATCH"},
		{"auth", "Permission denied (publickey).", false, "AUTH_FAILURE"},
		{"unknown", "ssh: protocol error", false, "OTHER_ERROR"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := classifySSHFailure(tc.stderr)
			if got != tc.want || reason != tc.wantReason {
				t.Fatalf("got=(%v,%q) want=(%v,%q)", got, reason, tc.want, tc.wantReason)
			}
		})
	}
}

func TestRunCommandRecoversVerifiedMovedTargetAndAudits(t *testing.T) {
	target := registry.Target{ID: "pc", Host: "old-host", Port: 22, User: "user", Platform: "linux", Enabled: true}
	store := newSSHRecoveryStore(t, target)
	disc := &fakeTargetDiscoverer{result: &discovery.DiscoveryResult{
		Status: "IDENTITY_MATCH", NewHost: "new-host", NewPort: 22,
		Method: "local-subnet+ssh-keyscan", Fingerprint: "SHA256:test", DurationMs: 12,
	}}
	shim, counter := recoverySSHShim(t, true, "ssh: connect to host old-host port 22: Connection refused")
	transport := NewSSHTransport()
	transport.SSHBinary = shim
	transport.IdentityFile = "/tmp/test-key"
	transport.Store = store
	transport.Discovery = disc

	result, err := transport.RunCommand(context.Background(), target, "hostname", CommandOptions{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK() || strings.TrimSpace(result.Stdout) != "recovered" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if disc.calls != 1 {
		t.Fatalf("discovery calls=%d want=1", disc.calls)
	}
	data, err := os.ReadFile(counter)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(data)); got != "2" {
		t.Fatalf("ssh attempts=%q want=2", got)
	}
	stored, err := store.GetTarget(context.Background(), "pc", false)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Host != "new-host" || stored.Port != 22 {
		t.Fatalf("stored endpoint=%s:%d", stored.Host, stored.Port)
	}
	var activities int
	if err := store.DB().QueryRow("SELECT COUNT(*) FROM activity WHERE action='target_endpoint_recovered'").Scan(&activities); err != nil {
		t.Fatal(err)
	}
	if activities != 1 {
		t.Fatalf("recovery activities=%d want=1", activities)
	}
}

func TestRunCommandDoesNotDiscoverAuthenticationFailure(t *testing.T) {
	target := registry.Target{ID: "pc", Host: "old-host", Port: 22, User: "user", Platform: "linux", Enabled: true}
	store := newSSHRecoveryStore(t, target)
	disc := &fakeTargetDiscoverer{result: &discovery.DiscoveryResult{
		Status: "IDENTITY_MATCH", NewHost: "new-host", NewPort: 22,
	}}
	shim, _ := recoverySSHShim(t, true, "Permission denied (publickey).")
	transport := NewSSHTransport()
	transport.SSHBinary = shim
	transport.IdentityFile = "/tmp/test-key"
	transport.Store = store
	transport.Discovery = disc

	result, err := transport.RunCommand(context.Background(), target, "hostname", CommandOptions{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 255 {
		t.Fatalf("exit=%d want=255", result.ExitCode)
	}
	if disc.calls != 0 {
		t.Fatalf("authentication failure triggered %d discovery calls", disc.calls)
	}
}

func TestRunCommandRetriesOnlyOnceAfterRecovery(t *testing.T) {
	target := registry.Target{ID: "pc", Host: "old-host", Port: 22, User: "user", Platform: "linux", Enabled: true}
	store := newSSHRecoveryStore(t, target)
	disc := &fakeTargetDiscoverer{result: &discovery.DiscoveryResult{
		Status: "IDENTITY_MATCH", NewHost: "new-host", NewPort: 22,
	}}
	shim, counter := recoverySSHShim(t, false, "ssh: connect to host old-host port 22: Connection refused")
	transport := NewSSHTransport()
	transport.SSHBinary = shim
	transport.IdentityFile = "/tmp/test-key"
	transport.Store = store
	transport.Discovery = disc

	result, err := transport.RunCommand(context.Background(), target, "hostname", CommandOptions{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 255 {
		t.Fatalf("exit=%d want=255", result.ExitCode)
	}
	if disc.calls != 1 {
		t.Fatalf("discovery calls=%d want=1", disc.calls)
	}
	data, err := os.ReadFile(counter)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(data)); got != "2" {
		t.Fatalf("ssh attempts=%q want=2", got)
	}
}

func TestRunCommandTimeoutDoesNotTriggerDiscovery(t *testing.T) {
	target := registry.Target{ID: "pc", Host: "old-host", Port: 22, User: "user", Platform: "linux", Enabled: true}
	store := newSSHRecoveryStore(t, target)
	disc := &fakeTargetDiscoverer{result: &discovery.DiscoveryResult{
		Status: "IDENTITY_MATCH", NewHost: "new-host", NewPort: 22,
	}}
	dir := t.TempDir()
	shim := filepath.Join(dir, "ssh-timeout-shim")
	if err := os.WriteFile(shim, []byte("#!/bin/sh\nsleep 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	transport := NewSSHTransport()
	transport.SSHBinary = shim
	transport.IdentityFile = "/tmp/test-key"
	transport.Store = store
	transport.Discovery = disc

	_, err := transport.RunCommand(context.Background(), target, "hostname", CommandOptions{Timeout: 50 * time.Millisecond})
	if ErrorCode(err) != "SSH_TIMEOUT" {
		t.Fatalf("err=%v code=%q want SSH_TIMEOUT", err, ErrorCode(err))
	}
	if disc.calls != 0 {
		t.Fatalf("command timeout triggered %d discovery calls", disc.calls)
	}
}
