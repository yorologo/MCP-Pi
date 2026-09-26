package remote

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mcp-gateway-adapter/internal/registry"
)

func TestBuildSSHArgsUsesRegistryAuthoritativeEndpoint(t *testing.T) {
	transport := &SSHTransport{IdentityFile: "/tmp/test-key"}
	target := registry.Target{
		ID:       "target-a",
		Host:     "10.0.0.2",
		Port:     2222,
		User:     "pi",
		SSHAlias: "target-alias",
	}

	args, err := transport.buildSSHArgs(target, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, "\n")
	for _, want := range []string{
		"BatchMode=yes",
		"ConnectTimeout=10",
		"StrictHostKeyChecking=yes",
		"IdentitiesOnly=yes",
		"IdentityFile=/tmp/test-key",
		"User=pi",
		"HostName=10.0.0.2",
		"Port=2222",
		"HostKeyAlias=target-a",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("ssh args missing %q: %v", want, args)
		}
	}
	if got := args[len(args)-1]; got != "target-alias" {
		t.Fatalf("destination=%q want=target-alias", got)
	}
}

func TestBuildSSHArgsFallsBackToHostAndFailsClosed(t *testing.T) {
	transport := &SSHTransport{IdentityFile: "/tmp/test-key"}

	args, err := transport.buildSSHArgs(registry.Target{
		ID:   "t",
		Host: "192.0.2.10",
		Port: 22,
		User: "user",
	}, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got := args[len(args)-1]; got != "192.0.2.10" {
		t.Fatalf("destination=%q want host", got)
	}
	if !strings.Contains(strings.Join(args, "\n"), "ConnectTimeout=2") {
		t.Fatalf("connect timeout not bounded to request: %v", args)
	}

	if _, err := transport.buildSSHArgs(registry.Target{Host: "192.0.2.10"}, 10*time.Second); ErrorCode(err) != "SSH_FAILED" {
		t.Fatalf("missing user err=%v code=%q", err, ErrorCode(err))
	}
	if _, err := transport.buildSSHArgs(registry.Target{User: "user"}, 10*time.Second); ErrorCode(err) != "SSH_FAILED" {
		t.Fatalf("missing endpoint err=%v code=%q", err, ErrorCode(err))
	}
}

func TestRunCommandHonorsContextAndDoesNotNeedNetworkForValidation(t *testing.T) {
	transport := &SSHTransport{
		SSHBinary:      "/definitely/not/a/real/ssh",
		IdentityFile:   "/tmp/test-key",
		MaxOutputBytes: 1024,
		DefaultTimeout: time.Second,
	}
	_, err := transport.RunCommand(
		context.Background(),
		registry.Target{ID: "t", Host: "192.0.2.10", Port: 22, User: "user"},
		"hostname",
		CommandOptions{Timeout: time.Second},
	)
	if ErrorCode(err) != "SSH_FAILED" {
		t.Fatalf("err=%v code=%q", err, ErrorCode(err))
	}
}

func TestLimitedBufferTruncatesWithoutShortWrite(t *testing.T) {
	var buffer limitedBuffer
	buffer.max = 4
	n, err := buffer.Write([]byte("abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 6 {
		t.Fatalf("reported write=%d want=6", n)
	}
	if got := string(buffer.Bytes()); got != "abcd" {
		t.Fatalf("buffer=%q want=abcd", got)
	}
}

func localSSHShim(t *testing.T) *SSHTransport {
	t.Helper()
	shim := filepath.Join(t.TempDir(), "ssh-shim")
	script := "#!/bin/sh\nfor last\ndo\n  :\ndone\nexec sh -c \"$last\"\n"
	if err := os.WriteFile(shim, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return &SSHTransport{
		SSHBinary:      shim,
		IdentityFile:   "/tmp/test-key",
		MaxOutputBytes: defaultMaxOutput,
		DefaultTimeout: 10 * time.Second,
	}
}

func localTarget() registry.Target {
	return registry.Target{ID: "local-test", Host: "127.0.0.1", Port: 22, User: "tester", Platform: "linux"}
}

func TestStructuredMutationHelpersFailClosedOnSymlinkEscapes(t *testing.T) {
	transport := localSSHShim(t)
	root := filepath.Join(t.TempDir(), "project")
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.MkdirAll(filepath.Join(root, "safe"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "safe", "escape")); err != nil {
		t.Fatal(err)
	}
	_, err := transport.ResolveSafeDestination(
		context.Background(), localTarget(), root, filepath.Join(root, "safe", "escape", "new.txt"), false, 5*time.Second,
	)
	if ErrorCode(err) != "PATH_OUTSIDE_ALLOWED_ROOT" {
		t.Fatalf("nested symlink err=%v code=%q", err, ErrorCode(err))
	}

	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	_, err = transport.ResolveSafeDestination(
		context.Background(), localTarget(), root, link, false, 5*time.Second,
	)
	if ErrorCode(err) != "SYMLINK_WRITE_DENIED" {
		t.Fatalf("destination symlink err=%v code=%q", err, ErrorCode(err))
	}
}

func TestWriteFileAtomicHelperEnforcesCreateAndSHA(t *testing.T) {
	transport := localSSHShim(t)
	root := t.TempDir()
	dest := filepath.Join(root, "file.txt")

	created, err := transport.WriteFileAtomic(
		context.Background(), localTarget(), dest, []byte("hello"), true, "", 1024, 5*time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !created.Created || !created.Atomic || created.BytesWritten != 5 {
		t.Fatalf("create result=%+v", created)
	}
	if data, err := os.ReadFile(dest); err != nil || string(data) != "hello" {
		t.Fatalf("created file data=%q err=%v", data, err)
	}

	_, err = transport.WriteFileAtomic(
		context.Background(), localTarget(), dest, []byte("changed"), false, "", 1024, 5*time.Second,
	)
	if ErrorCode(err) != "WRITE_CONFLICT" {
		t.Fatalf("missing sha err=%v code=%q", err, ErrorCode(err))
	}

	sum := sha256.Sum256([]byte("hello"))
	overwritten, err := transport.WriteFileAtomic(
		context.Background(), localTarget(), dest, []byte("changed"), false, fmt.Sprintf("%x", sum), 1024, 5*time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	if overwritten.Created || !overwritten.Atomic || overwritten.BytesWritten != len("changed") {
		t.Fatalf("overwrite result=%+v", overwritten)
	}
	if data, err := os.ReadFile(dest); err != nil || string(data) != "changed" {
		t.Fatalf("overwritten file data=%q err=%v", data, err)
	}
}
