package remote

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"mcp-gateway-adapter/internal/registry"
)

func (s *SSHTransport) RunPython(
	ctx context.Context,
	target registry.Target,
	script string,
	argv []string,
	stdin []byte,
	timeout time.Duration,
) (CommandResult, error) {
	command, err := buildRemotePythonCommand(script, argv)
	if err != nil {
		return CommandResult{}, err
	}
	return s.RunCommand(ctx, target, command, CommandOptions{
		Stdin:   stdin,
		Timeout: timeout,
	})
}

func (s *SSHTransport) ProbePath(
	ctx context.Context,
	target registry.Target,
	candidatePath string,
	timeout time.Duration,
) (PathProbe, error) {
	script := `import os, sys, json, hashlib
p = sys.argv[1]
lexists = os.path.lexists(p)
is_link = os.path.islink(p)
is_file = os.path.isfile(p) and not is_link
is_dir = os.path.isdir(p) and not is_link
canon = os.path.realpath(p) if lexists else ''
parent = os.path.dirname(p) or '.'
parent_exists = os.path.exists(parent)
parent_is_link = os.path.islink(parent)
parent_canon = os.path.realpath(parent) if parent_exists else ''
sha = ''
size = 0
content = ''
if is_file:
    try:
        with open(p, 'rb') as f:
            data = f.read()
        sha = hashlib.sha256(data).hexdigest()
        size = len(data)
        content = data.decode('utf-8', errors='replace')
    except Exception:
        pass
print(json.dumps({
    'exists': lexists,
    'is_symlink': is_link,
    'is_file': is_file,
    'is_dir': is_dir,
    'canonical_path': canon,
    'parent_exists': parent_exists,
    'parent_is_symlink': parent_is_link,
    'parent_canonical_path': parent_canon,
    'sha256': sha,
    'size': size,
    'content': content,
}))
`
	result, err := s.RunPython(ctx, target, script, []string{candidatePath}, nil, timeout)
	if err != nil {
		return PathProbe{}, err
	}
	if !result.OK() || strings.TrimSpace(result.Stdout) == "" {
		return PathProbe{}, NewError(
			"SSH_FAILED",
			"Probe failed: "+strings.TrimSpace(result.Stderr),
			result.ExitCode,
		)
	}
	var probe PathProbe
	if err := json.Unmarshal([]byte(lastNonEmptyLine(result.Stdout)), &probe); err != nil {
		return PathProbe{}, NewError(
			"SSH_FAILED",
			"Invalid probe JSON response: "+err.Error(),
			result.ExitCode,
		)
	}
	return probe, nil
}

func (s *SSHTransport) ResolveSafeDestination(
	ctx context.Context,
	target registry.Target,
	projectRoot, candidatePath string,
	allowMissingParents bool,
	timeout time.Duration,
) (SafeDestination, error) {
	script := `import os, sys, json
root = sys.argv[1]
candidate = sys.argv[2]
allow_missing = sys.argv[3] == '1'
root_real = os.path.realpath(root)
def emit(ok, code='', message='', **extra):
    d = {'ok': ok, 'code': code, 'message': message}; d.update(extra); print(json.dumps(d))
if not os.path.isdir(root_real):
    emit(False, 'PROJECT_ROOT_INVALID', f'Project root is not a directory: {root}'); sys.exit(10)
if os.path.lexists(candidate) and os.path.islink(candidate):
    emit(False, 'SYMLINK_WRITE_DENIED', f'Destination is a symlink: {candidate}'); sys.exit(11)
parent = os.path.dirname(candidate) or '.'
cursor = parent
missing = []
while not os.path.exists(cursor):
    if not allow_missing:
        emit(False, 'NOT_FOUND', f'Parent directory does not exist: {parent}'); sys.exit(12)
    base = os.path.basename(cursor)
    if not base or base in ('.', '..'):
        emit(False, 'INVALID_PATH', f'Invalid destination parent: {parent}'); sys.exit(13)
    missing.append(base)
    nxt = os.path.dirname(cursor)
    if nxt == cursor:
        emit(False, 'PATH_OUTSIDE_ALLOWED_ROOT', f'Unable to anchor destination beneath project root: {candidate}'); sys.exit(14)
    cursor = nxt
anchor_real = os.path.realpath(cursor)
try:
    if os.path.commonpath([root_real, anchor_real]) != root_real:
        emit(False, 'PATH_OUTSIDE_ALLOWED_ROOT', f'Destination parent resolves outside project root: {parent}'); sys.exit(15)
except ValueError:
    emit(False, 'PATH_OUTSIDE_ALLOWED_ROOT', f'Destination parent is on a different path domain: {parent}'); sys.exit(16)
parent_real = os.path.normpath(os.path.join(anchor_real, *reversed(missing)))
try:
    if os.path.commonpath([root_real, parent_real]) != root_real:
        emit(False, 'PATH_OUTSIDE_ALLOWED_ROOT', f'Destination parent escapes project root: {parent}'); sys.exit(17)
except ValueError:
    emit(False, 'PATH_OUTSIDE_ALLOWED_ROOT', f'Destination parent is on a different path domain: {parent}'); sys.exit(18)
dest = os.path.join(parent_real, os.path.basename(candidate))
dest_exists = os.path.lexists(candidate)
dest_real = os.path.realpath(candidate) if dest_exists else ''
if dest_exists:
    try:
        if os.path.commonpath([root_real, dest_real]) != root_real:
            emit(False, 'PATH_OUTSIDE_ALLOWED_ROOT', f'Destination resolves outside project root: {candidate}'); sys.exit(19)
    except ValueError:
        emit(False, 'PATH_OUTSIDE_ALLOWED_ROOT', f'Destination is on a different path domain: {candidate}'); sys.exit(20)
emit(True, root_canonical=root_real, parent_canonical=parent_real, destination_path=dest, exists=dest_exists, canonical_path=dest_real)
`
	allow := "0"
	if allowMissingParents {
		allow = "1"
	}
	result, err := s.RunPython(ctx, target, script, []string{projectRoot, candidatePath, allow}, nil, timeout)
	if err != nil {
		return SafeDestination{}, err
	}

	var envelope struct {
		OK      bool   `json:"ok"`
		Code    string `json:"code"`
		Message string `json:"message"`
		SafeDestination
	}
	line := lastNonEmptyLine(result.Stdout)
	if line != "" {
		_ = json.Unmarshal([]byte(line), &envelope)
	}
	if !envelope.OK {
		code := envelope.Code
		if code == "" {
			code = "SSH_FAILED"
		}
		message := envelope.Message
		if message == "" {
			message = nonEmptyRemote(result.Stderr, "Unable to resolve safe structured destination")
		}
		return SafeDestination{}, NewError(code, message, result.ExitCode)
	}
	return envelope.SafeDestination, nil
}

func (s *SSHTransport) WriteFileAtomic(
	ctx context.Context,
	target registry.Target,
	destPath string,
	content []byte,
	create bool,
	expectedSHA256 string,
	maxWriteBytes int,
	timeout time.Duration,
) (AtomicWriteResult, error) {
	script := `import sys, os, tempfile, hashlib, json
dest = sys.argv[1]
create = sys.argv[2] == '1'
expected_sha = sys.argv[3].strip()
max_bytes = int(sys.argv[4])
content_bytes = sys.stdin.buffer.read()
if len(content_bytes) > max_bytes:
    print(json.dumps({'ok': False, 'code': 'FILE_TOO_LARGE', 'message': f'Content size ({len(content_bytes)}) exceeds allowed limit of {max_bytes} bytes'}))
    sys.exit(10)
if b'\x00' in content_bytes:
    print(json.dumps({'ok': False, 'code': 'INVALID_ENCODING', 'message': 'Content contains NUL byte'}))
    sys.exit(11)
try:
    content_bytes.decode('utf-8')
except UnicodeDecodeError as e:
    print(json.dumps({'ok': False, 'code': 'INVALID_ENCODING', 'message': f'Content is not valid UTF-8: {e}'}))
    sys.exit(12)
parent_dir = os.path.dirname(dest) or '.'
if not os.path.exists(parent_dir):
    print(json.dumps({'ok': False, 'code': 'NOT_FOUND', 'message': f'Parent directory does not exist: {parent_dir}'}))
    sys.exit(13)
if not os.path.isdir(parent_dir):
    print(json.dumps({'ok': False, 'code': 'INVALID_PATH', 'message': f'Parent path is not a directory: {parent_dir}'}))
    sys.exit(14)
if os.path.islink(parent_dir):
    print(json.dumps({'ok': False, 'code': 'SYMLINK_WRITE_DENIED', 'message': f'Parent directory is a symlink: {parent_dir}'}))
    sys.exit(15)
dest_exists = os.path.lexists(dest)
if dest_exists and os.path.islink(dest):
    print(json.dumps({'ok': False, 'code': 'SYMLINK_WRITE_DENIED', 'message': f'Target file is a symlink: {dest}'}))
    sys.exit(16)
old_sha256 = None
old_mode = None
if create:
    if dest_exists:
        print(json.dumps({'ok': False, 'code': 'FILE_ALREADY_EXISTS', 'message': f'File already exists: {dest}'}))
        sys.exit(17)
else:
    if not dest_exists:
        print(json.dumps({'ok': False, 'code': 'NOT_FOUND', 'message': f'File not found: {dest}'}))
        sys.exit(18)
    if os.path.isdir(dest):
        print(json.dumps({'ok': False, 'code': 'INVALID_PATH', 'message': f'Destination is a directory: {dest}'}))
        sys.exit(19)
    with open(dest, 'rb') as f:
        current_data = f.read()
    old_sha256 = hashlib.sha256(current_data).hexdigest()
    old_mode = os.stat(dest).st_mode
    if not expected_sha:
        print(json.dumps({'ok': False, 'code': 'WRITE_CONFLICT', 'message': 'expected_sha256 is required for overwriting existing file'}))
        sys.exit(20)
    if old_sha256.lower() != expected_sha.lower():
        print(json.dumps({'ok': False, 'code': 'WRITE_CONFLICT', 'message': f'Hash mismatch: expected {expected_sha}, found {old_sha256}'}))
        sys.exit(21)
new_sha256 = hashlib.sha256(content_bytes).hexdigest()
temp_fd, temp_path = tempfile.mkstemp(dir=parent_dir, prefix='.mcp_tmp_')
try:
    with os.fdopen(temp_fd, 'wb') as tf:
        tf.write(content_bytes)
        tf.flush()
        os.fsync(tf.fileno())
    if old_mode is not None:
        os.chmod(temp_path, old_mode)
    os.replace(temp_path, dest)
    try:
        dir_fd = os.open(parent_dir, os.O_RDONLY)
        try: os.fsync(dir_fd)
        finally: os.close(dir_fd)
    except Exception:
        pass
    print(json.dumps({'ok': True, 'created': create, 'old_sha256': old_sha256, 'new_sha256': new_sha256, 'bytes_written': len(content_bytes), 'atomic': True}))
except Exception as e:
    if os.path.exists(temp_path):
        try: os.unlink(temp_path)
        except OSError: pass
    print(json.dumps({'ok': False, 'code': 'WRITE_FAILED', 'message': str(e)}))
    sys.exit(30)
`
	createFlag := "0"
	if create {
		createFlag = "1"
	}
	if maxWriteBytes <= 0 {
		maxWriteBytes = 262144
	}
	result, err := s.RunPython(
		ctx,
		target,
		script,
		[]string{destPath, createFlag, expectedSHA256, strconv.Itoa(maxWriteBytes)},
		content,
		timeout,
	)
	if err != nil {
		return AtomicWriteResult{}, err
	}

	var envelope struct {
		OK      bool   `json:"ok"`
		Code    string `json:"code"`
		Message string `json:"message"`
		AtomicWriteResult
	}
	line := lastNonEmptyLine(result.Stdout)
	if line != "" {
		_ = json.Unmarshal([]byte(line), &envelope)
	}
	if !envelope.OK {
		code := envelope.Code
		if code == "" {
			if !result.OK() {
				code = "SSH_FAILED"
			} else {
				code = "WRITE_FAILED"
			}
		}
		message := envelope.Message
		if message == "" {
			message = nonEmptyRemote(result.Stderr, "Invalid response from remote write helper")
		}
		return AtomicWriteResult{}, NewError(code, message, result.ExitCode)
	}
	return envelope.AtomicWriteResult, nil
}
