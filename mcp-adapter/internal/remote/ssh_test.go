package remote

import (
	"context"
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
