package remote

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"mcp-gateway-adapter/internal/discovery"
	"mcp-gateway-adapter/internal/registry"
)

const (
	defaultTimeout     = 30 * time.Second
	defaultMaxOutput   = 256 * 1024
	defaultIdentityRel = ".ssh/mcp_gateway_ed25519"
)

type targetDiscoverer interface {
	FindMovedTarget(target discovery.TargetConfig) (*discovery.DiscoveryResult, error)
}

type SSHTransport struct {
	SSHBinary      string
	IdentityFile   string
	MaxOutputBytes int
	DefaultTimeout time.Duration
	Store          *registry.Store
	Discovery      targetDiscoverer
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

func NewSSHTransportWithRecovery(store *registry.Store, targetDiscovery *discovery.TargetDiscovery) *SSHTransport {
	transport := NewSSHTransport()
	transport.Store = store
	transport.Discovery = targetDiscovery
	return transport
}

func classifySSHFailure(stderr string) (bool, string) {
	message := strings.ToLower(stderr)
	for _, pattern := range []string{
		"permission denied",
		"authentication failed",
		"too many authentication failures",
	} {
		if strings.Contains(message, pattern) {
			return false, "AUTH_FAILURE"
		}
	}
	for _, pattern := range []string{
		"host key verification failed",
		"remote host identification has changed",
		"offending ",
	} {
		if strings.Contains(message, pattern) {
			return true, "ENDPOINT_IDENTITY_MISMATCH"
		}
	}
	for _, pattern := range []string{
		"connection refused",
		"connection timed out",
		"no route to host",
		"network is unreachable",
		"host is down",
		"operation timed out",
		"could not resolve hostname",
	} {
		if strings.Contains(message, pattern) {
			return true, "NETWORK_CONNECTIVITY_ERROR"
		}
	}
	return false, "OTHER_ERROR"
}

func (s *SSHTransport) RunCommand(
	ctx context.Context,
	target registry.Target,
	command string,
	options CommandOptions,
) (CommandResult, error) {
	return s.runCommand(ctx, target, command, options, true)
}

func (s *SSHTransport) runCommand(
	ctx context.Context,
	target registry.Target,
	command string,
	options CommandOptions,
	allowRecovery bool,
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
	maxOutput := options.MaxOutputBytes
	if maxOutput <= 0 {
		maxOutput = s.MaxOutputBytes
	}
	if maxOutput <= 0 {
		maxOutput = defaultMaxOutput
	}

	fullCommand := command
	if isWindowsTarget(target) {
		if strings.TrimSpace(options.CWD) != "" || len(options.Env) > 0 {
			var script strings.Builder
			script.WriteString("$ErrorActionPreference='Stop';")
			if strings.TrimSpace(options.CWD) != "" {
				script.WriteString("Set-Location -LiteralPath ")
				script.WriteString(powerShellQuote(options.CWD))
				script.WriteString(";")
			}
			for k, v := range options.Env {
				if !isSafeEnvIdentifier(k) {
					continue
				}
				script.WriteString("$env:")
				script.WriteString(k)
				script.WriteString("=")
				script.WriteString(powerShellQuote(v))
				script.WriteString(";")
			}
			script.WriteString("$cmd=")
			script.WriteString(powerShellQuote(command))
			script.WriteString(";& cmd.exe /d /s /c $cmd; exit $LASTEXITCODE")
			fullCommand = buildPowerShellCommand(script.String())
		}
	} else {
		var prefixes []string
		for k, v := range options.Env {
			if isSafeEnvIdentifier(k) {
				prefixes = append(prefixes, fmt.Sprintf("export %s=%s;", k, shellQuote(v)))
			}
		}
		if strings.TrimSpace(options.CWD) != "" {
			prefixes = append(prefixes, "cd "+shellQuote(options.CWD)+" &&")
		}
		if len(prefixes) > 0 {
			fullCommand = strings.Join(prefixes, " ") + " " + command
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
		if allowRecovery && result.ExitCode == 255 {
			if shouldRecover, reason := classifySSHFailure(result.Stderr); shouldRecover {
				if recovered, recoveryErr, handled := s.attemptRecovery(
					ctx, target, command, options, reason,
				); handled {
					return recovered, recoveryErr
				}
			}
		}
		return result, nil
	}
	return CommandResult{}, NewError(
		"SSH_FAILED",
		fmt.Sprintf("Failed to invoke ssh binary '%s': %v", sshBinary, err),
		-1,
	)
}

func (s *SSHTransport) attemptRecovery(
	ctx context.Context,
	target registry.Target,
	command string,
	options CommandOptions,
	triggerReason string,
) (CommandResult, error, bool) {
	if s.Store == nil || s.Discovery == nil || strings.TrimSpace(target.ID) == "" {
		return CommandResult{}, nil, false
	}

	found, err := s.Discovery.FindMovedTarget(discovery.TargetConfig{
		ID:       target.ID,
		Host:     target.Host,
		Port:     target.Port,
		SSHAlias: target.SSHAlias,
		User:     target.User,
		Platform: target.Platform,
		Enabled:  target.Enabled,
	})
	if err != nil || found == nil || found.Status != "IDENTITY_MATCH" || strings.TrimSpace(found.NewHost) == "" {
		return CommandResult{}, nil, false
	}
	newPort := found.NewPort
	if newPort <= 0 {
		newPort = target.Port
	}
	if found.NewHost == target.Host && newPort == target.Port {
		return CommandResult{}, nil, false
	}

	targetID := target.ID
	duration := int64(found.DurationMs)
	detailBytes, _ := json.Marshal(map[string]any{
		"old_endpoint":      fmt.Sprintf("%s:%d", target.Host, target.Port),
		"new_endpoint":      fmt.Sprintf("%s:%d", found.NewHost, newPort),
		"discovery_method":  found.Method,
		"trigger_reason":    triggerReason,
		"identity_verified": true,
		"host_fingerprint":  found.Fingerprint,
	})
	activity := registry.Activity{
		Actor:      "system",
		Action:     "target_endpoint_recovered",
		TargetID:   &targetID,
		DurationMS: &duration,
		Success:    true,
		Detail:     string(detailBytes),
	}
	if err := s.Store.UpdateTargetEndpointAudited(
		ctx,
		target.ID,
		target.Host,
		target.Port,
		found.NewHost,
		newPort,
		activity,
	); err != nil {
		current, getErr := s.Store.GetTarget(ctx, target.ID, false)
		if getErr != nil || current.Host != found.NewHost || current.Port != newPort {
			return CommandResult{}, NewError(
				"SSH_RECOVERY_FAILED",
				fmt.Sprintf("verified Target endpoint could not be persisted safely: %v", err),
				-1,
			), true
		}
	}

	target.Host = found.NewHost
	target.Port = newPort
	result, retryErr := s.runCommand(ctx, target, command, options, false)
	return result, retryErr, true
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

	totalSeconds := int(timeout / time.Second)
	connectSeconds := totalSeconds
	if connectSeconds < 1 {
		connectSeconds = 1
	}
	if connectSeconds > 10 {
		connectSeconds = 10
	}
	// Leave a small process-level margin on normal Target probes so OpenSSH can
	// report a connection timeout (exit 255) before the whole remote command
	// deadline fires. Short sub-5s operations retain their existing budget.
	if totalSeconds >= 5 && connectSeconds >= totalSeconds {
		connectSeconds = totalSeconds - 1
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
	args = append(args, "--", destination)
	return args, nil
}

func isWindowsTarget(target registry.Target) bool {
	return strings.EqualFold(strings.TrimSpace(target.Platform), "windows")
}

func powerShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func buildPowerShellCommand(script string) string {
	runes := utf16.Encode([]rune(script))
	raw := make([]byte, len(runes)*2)
	for i, r := range runes {
		raw[i*2] = byte(r)
		raw[i*2+1] = byte(r >> 8)
	}
	return "powershell.exe -NoLogo -NoProfile -NonInteractive -EncodedCommand " + base64.StdEncoding.EncodeToString(raw)
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

func isSafeEnvIdentifier(key string) bool {
	if len(key) == 0 {
		return false
	}
	for i, r := range key {
		if r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			continue
		}
		if i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

var _ Transport = (*SSHTransport)(nil)
