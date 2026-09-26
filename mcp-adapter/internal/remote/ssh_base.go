package remote

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"mcp-gateway-adapter/internal/registry"
)

const (
	defaultTimeout     = 30 * time.Second
	defaultMaxOutput   = 256 * 1024
	defaultIdentityRel = ".ssh/mcp_gateway_ed25519"
)

type SSHTransport struct {
	SSHBinary      string
	IdentityFile   string
	MaxOutputBytes int
	DefaultTimeout time.Duration
}

func NewSSHTransport() *SSHTransport {
	identity := strings.TrimSpace(os.Getenv("MCP_GATEWAY_SSH_IDENTITY_FILE"))
	if identity == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			identity = filepath.Join(home, defaultIdentityRel)
		} else {
			identity = "~/.ssh/mcp_gateway_ed25519"
		}
	}
	return &SSHTransport{
		SSHBinary:      "ssh",
		IdentityFile:   identity,
		MaxOutputBytes: defaultMaxOutput,
		DefaultTimeout: defaultTimeout,
	}
}

func (s *SSHTransport) RunCommand(
	ctx context.Context,
	target registry.Target,
	command string,
	options CommandOptions,
) (CommandResult, error) {
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = s.DefaultTimeout
		if timeout <= 0 {
			timeout = defaultTimeout
		}
	}

	sshBinary := strings.TrimSpace(s.SSHBinary)
	if sshBinary == "" {
		sshBinary = "ssh"
	}
	maxOutput := s.MaxOutputBytes
	if maxOutput <= 0 {
		maxOutput = defaultMaxOutput
	}

	fullCommand := command
	if strings.TrimSpace(options.CWD) != "" {
		if strings.EqualFold(strings.TrimSpace(target.Platform), "windows") {
			bootstrap := "import os, subprocess, sys\n" +
				"command = sys.argv[1]\n" +
				"cwd = sys.argv[2]\n" +
				"if cwd:\n    os.chdir(cwd)\n" +
				"sys.exit(subprocess.run(command, shell=True).returncode)\n"
			var err error
			fullCommand, err = buildRemotePythonCommand(bootstrap, []string{command, options.CWD})
			if err != nil {
				return CommandResult{}, err
			}
		} else {
			fullCommand = "cd " + shellQuote(options.CWD) + " && " + command
		}
	}

	args, err := s.buildSSHArgs(target, timeout)
	if err != nil {
		return CommandResult{}, err
	}
	args = append(args, fullCommand)

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, sshBinary, args...)
	if len(options.Stdin) > 0 {
		cmd.Stdin = bytes.NewReader(options.Stdin)
	}

	var stdout, stderr limitedBuffer
	stdout.max = maxOutput
	stderr.max = maxOutput
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	started := time.Now()
	err = cmd.Run()
	duration := time.Since(started).Milliseconds()

	if runCtx.Err() == context.DeadlineExceeded {
		return CommandResult{}, NewError(
			"SSH_TIMEOUT",
			fmt.Sprintf("SSH command timed out after %s", timeout),
			-1,
		)
	}

	result := CommandResult{
		ExitCode:   0,
		Stdout:     validUTF8(stdout.Bytes()),
		Stderr:     validUTF8(stderr.Bytes()),
		DurationMS: duration,
	}
	if err == nil {
		return result, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	return CommandResult{}, NewError(
		"SSH_FAILED",
		fmt.Sprintf("Failed to invoke ssh binary '%s': %v", sshBinary, err),
		-1,
	)
}

func (s *SSHTransport) buildSSHArgs(target registry.Target, timeout time.Duration) ([]string, error) {
	host := strings.TrimSpace(target.Host)
	alias := strings.TrimSpace(target.SSHAlias)
	user := strings.TrimSpace(target.User)
	destination := alias
	if destination == "" {
		destination = host
	}
	if destination == "" {
		return nil, NewError("SSH_FAILED", "Target has no SSH host or alias configured", -1)
	}
	if user == "" {
		return nil, NewError("SSH_FAILED", "Target has no SSH user configured", -1)
	}

	connectSeconds := int(timeout / time.Second)
	if connectSeconds < 1 {
		connectSeconds = 1
	}
	if connectSeconds > 10 {
		connectSeconds = 10
	}

	identity := strings.TrimSpace(s.IdentityFile)
	if identity == "" {
		identity = NewSSHTransport().IdentityFile
	}

	args := []string{
		"-o", "BatchMode=yes",
		"-o", fmt.Sprintf("ConnectTimeout=%d", connectSeconds),
		"-o", "StrictHostKeyChecking=yes",
		"-o", "IdentitiesOnly=yes",
		"-o", "IdentityFile=" + identity,
		"-o", "User=" + user,
	}
	if host != "" {
		args = append(args, "-o", "HostName="+host)
	}
	port := target.Port
	if port <= 0 {
		port = 22
	}
	args = append(args, "-o", fmt.Sprintf("Port=%d", port))
	if strings.TrimSpace(target.ID) != "" {
		args = append(args, "-o", "HostKeyAlias="+target.ID)
	}
	args = append(args, destination)
	return args, nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func lastNonEmptyLine(value string) string {
	lines := strings.Split(strings.TrimSpace(value), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return strings.TrimSpace(lines[i])
		}
	}
	return ""
}

func validUTF8(value []byte) string {
	if utf8.Valid(value) {
		return string(value)
	}
	return strings.ToValidUTF8(string(value), "�")
}

func nonEmptyRemote(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

type limitedBuffer struct {
	max  int
	data []byte
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.max <= 0 {
		return len(p), nil
	}
	remaining := b.max - len(b.data)
	if remaining > 0 {
		if remaining > len(p) {
			remaining = len(p)
		}
		b.data = append(b.data, p[:remaining]...)
	}
	return len(p), nil
}

func (b *limitedBuffer) Bytes() []byte { return b.data }

var _ Transport = (*SSHTransport)(nil)
