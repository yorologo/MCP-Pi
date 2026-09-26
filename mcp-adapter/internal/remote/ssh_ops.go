package remote

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"mcp-gateway-adapter/internal/registry"
)

func buildRemotePythonCommand(pyCode string, argv []string) (string, error) {
	argsJSON, err := json.Marshal(argv)
	if err != nil {
		return "", fmt.Errorf("encode remote python argv: %w", err)
	}
	script := "import sys\nsys.argv = ['mcp-helper'] + " + string(argsJSON) + "\n" + pyCode

	var compressed bytes.Buffer
	writer := zlib.NewWriter(&compressed)
	if _, err := writer.Write([]byte(script)); err != nil {
		return "", fmt.Errorf("compress remote python helper: %w", err)
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("close remote python compressor: %w", err)
	}
	payload := base64.StdEncoding.EncodeToString(compressed.Bytes())
	return `python3 -c "import base64,zlib;exec(zlib.decompress(base64.b64decode('` + payload + `')))"`, nil
}

func (s *SSHTransport) ResolveCanonicalPath(
	ctx context.Context,
	target registry.Target,
	candidatePath string,
	timeout time.Duration,
) (string, error) {
	command, err := buildRemotePythonCommand(
		"import os, sys; print(os.path.realpath(sys.argv[1]))",
		[]string{candidatePath},
	)
	if err != nil {
		return "", err
	}
	result, err := s.RunCommand(ctx, target, command, CommandOptions{Timeout: timeout})
	if err != nil {
		return "", err
	}
	canonical := lastNonEmptyLine(result.Stdout)
	if !result.OK() || canonical == "" {
		return "", NewError(
			"SSH_FAILED",
			"Unable to resolve canonical path on target: "+strings.TrimSpace(result.Stderr),
			result.ExitCode,
		)
	}
	return canonical, nil
}

func (s *SSHTransport) ListDirectory(
	ctx context.Context,
	target registry.Target,
	canonicalPath string,
	limit int,
	timeout time.Duration,
) ([]DirectoryEntry, error) {
	if limit <= 0 {
		limit = 200
	}
	script := `import os, sys, json
p = sys.argv[1]
limit = int(sys.argv[2])
if not os.path.exists(p):
    sys.exit(2)
if not os.path.isdir(p):
    sys.exit(3)
entries = []
try:
    for name in sorted(os.listdir(p))[:limit]:
        fp = os.path.join(p, name)
        t = 'symlink' if os.path.islink(fp) else ('directory' if os.path.isdir(fp) else ('file' if os.path.isfile(fp) else 'other'))
        s = os.path.getsize(fp) if t == 'file' else None
        entries.append({'name': name, 'type': t, 'size': s})
    print(json.dumps(entries))
except Exception as exc:
    print(str(exc), file=sys.stderr)
    sys.exit(4)
`
	command, err := buildRemotePythonCommand(script, []string{canonicalPath, strconv.Itoa(limit)})
	if err != nil {
		return nil, err
	}
	result, err := s.RunCommand(ctx, target, command, CommandOptions{Timeout: timeout})
	if err != nil {
		return nil, err
	}
	switch result.ExitCode {
	case 0:
	case 2:
		return nil, NewError("NOT_FOUND", strings.TrimSpace(result.Stderr), result.ExitCode)
	case 3:
		return nil, NewError("NOT_DIRECTORY", strings.TrimSpace(result.Stderr), result.ExitCode)
	default:
		return nil, NewError("SSH_FAILED", nonEmptyRemote(result.Stderr, "Failed to list directory"), result.ExitCode)
	}
	var entries []DirectoryEntry
	if err := json.Unmarshal([]byte(strings.TrimSpace(result.Stdout)), &entries); err != nil {
		return nil, NewError("SSH_FAILED", "Failed to decode remote directory listing", result.ExitCode)
	}
	return entries, nil
}

func (s *SSHTransport) FileStat(
	ctx context.Context,
	target registry.Target,
	canonicalPath string,
	timeout time.Duration,
) (FileStat, error) {
	script := `import os, sys, json
p = sys.argv[1]
if not os.path.exists(p):
    sys.exit(2)
st = os.stat(p)
t = 'symlink' if os.path.islink(p) else ('directory' if os.path.isdir(p) else ('file' if os.path.isfile(p) else 'other'))
print(json.dumps({'exists': True, 'type': t, 'size': st.st_size, 'mtime': int(st.st_mtime)}))
`
	command, err := buildRemotePythonCommand(script, []string{canonicalPath})
	if err != nil {
		return FileStat{}, err
	}
	result, err := s.RunCommand(ctx, target, command, CommandOptions{Timeout: timeout})
	if err != nil {
		return FileStat{}, err
	}
	if result.ExitCode == 2 {
		return FileStat{}, NewError("NOT_FOUND", strings.TrimSpace(result.Stderr), result.ExitCode)
	}
	if !result.OK() {
		return FileStat{}, NewError("SSH_FAILED", nonEmptyRemote(result.Stderr, "Stat failed"), result.ExitCode)
	}
	var stat FileStat
	if err := json.Unmarshal([]byte(strings.TrimSpace(result.Stdout)), &stat); err != nil {
		return FileStat{}, NewError("SSH_FAILED", "Failed to decode remote file stat", result.ExitCode)
	}
	return stat, nil
}

func (s *SSHTransport) ReadFile(
	ctx context.Context,
	target registry.Target,
	canonicalPath string,
	maxBytes int64,
	timeout time.Duration,
) (string, error) {
	if maxBytes <= 0 {
		maxBytes = 1048576
	}
	script := `import sys, os
path = sys.argv[1]
limit = int(sys.argv[2])
if not os.path.exists(path):
    sys.exit(2)
if os.path.isdir(path):
    sys.exit(3)
size = os.path.getsize(path)
if size > limit:
    sys.exit(4)
with open(path, 'rb') as fh:
    data = fh.read(limit + 1)
if b'\x00' in data:
    sys.exit(5)
sys.stdout.buffer.write(data)
`
	command, err := buildRemotePythonCommand(script, []string{canonicalPath, strconv.FormatInt(maxBytes, 10)})
	if err != nil {
		return "", err
	}
	result, err := s.RunCommand(ctx, target, command, CommandOptions{Timeout: timeout})
	if err != nil {
		return "", err
	}
	switch result.ExitCode {
	case 0:
		return result.Stdout, nil
	case 2:
		return "", NewError("NOT_FOUND", fmt.Sprintf("File not found: %s", canonicalPath), result.ExitCode)
	case 3:
		return "", NewError("INVALID_PATH", fmt.Sprintf("Target path is a directory: %s", canonicalPath), result.ExitCode)
	case 4:
		return "", NewError("FILE_TOO_LARGE", fmt.Sprintf("File size exceeds allowed limit of %d bytes", maxBytes), result.ExitCode)
	case 5:
		return "", NewError("BINARY_FILE_NOT_SUPPORTED", "Binary file not supported", result.ExitCode)
	default:
		return "", NewError(
			"SSH_FAILED",
			fmt.Sprintf("Error reading file '%s': %s", canonicalPath, strings.TrimSpace(result.Stderr)),
			result.ExitCode,
		)
	}
}

func (s *SSHTransport) GitStatus(
	ctx context.Context,
	target registry.Target,
	canonicalRoot string,
	timeout time.Duration,
) (string, error) {
	result, err := s.RunCommand(
		ctx,
		target,
		"git status --short",
		CommandOptions{CWD: canonicalRoot, Timeout: timeout},
	)
	if err != nil {
		return "", err
	}
	if !result.OK() {
		return "", NewError("SSH_FAILED", strings.TrimSpace(result.Stderr), result.ExitCode)
	}
	return result.Stdout, nil
}

func (s *SSHTransport) Search(
	ctx context.Context,
	target registry.Target,
	projectRoot, searchRoot, pattern string,
	isRegex bool,
	limit int,
	timeout time.Duration,
) ([]SearchMatch, error) {
	if limit <= 0 {
		limit = 100
	}
	script := `import os, re, json, sys
root = sys.argv[1]
base = sys.argv[2]
pat = sys.argv[3]
is_re = sys.argv[4].lower() == 'true'
limit = int(sys.argv[5])
regex = re.compile(pat) if is_re else None
matches = []
for current, dirs, files in os.walk(base):
    dirs[:] = [d for d in dirs if not d.startswith('.git') and not d.startswith('__pycache__')]
    for name in files:
        fp = os.path.join(current, name)
        try:
            with open(fp, 'r', encoding='utf-8', errors='ignore') as fh:
                for idx, line in enumerate(fh, 1):
                    hit = regex.search(line) if is_re else (pat in line)
                    if hit:
                        matches.append({'file': os.path.relpath(fp, root), 'line': idx, 'text': line.strip()[:200]})
                        if len(matches) >= limit:
                            break
        except Exception:
            pass
        if len(matches) >= limit:
            break
    if len(matches) >= limit:
        break
print(json.dumps(matches))
`
	command, err := buildRemotePythonCommand(
		script,
		[]string{projectRoot, searchRoot, pattern, strconv.FormatBool(isRegex), strconv.Itoa(limit)},
	)
	if err != nil {
		return nil, err
	}
	result, err := s.RunCommand(ctx, target, command, CommandOptions{Timeout: timeout})
	if err != nil {
		return nil, err
	}
	if !result.OK() || strings.TrimSpace(result.Stdout) == "" {
		// Preserve current Python semantics: command/JSON failures yield no matches.
		return []SearchMatch{}, nil
	}
	var matches []SearchMatch
	if err := json.Unmarshal([]byte(result.Stdout), &matches); err != nil {
		return []SearchMatch{}, nil
	}
	return matches, nil
}
