package remote

import (
	"context"
	"encoding/base64"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"mcp-gateway-adapter/internal/registry"
)

func parseKVLines(stdout string) map[string]string {
	out := make(map[string]string)
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		if key, value, ok := strings.Cut(line, "="); ok {
			out[key] = value
		}
	}
	return out
}

func decodeB64(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func structuredRemoteError(result CommandResult, fallback string) error {
	for _, line := range strings.Split(result.Stdout+"\n"+result.Stderr, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "MCPERR|") {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) == 3 {
			return NewError(parts[1], parts[2], result.ExitCode)
		}
	}
	return NewError("SSH_FAILED", nonEmptyRemote(result.Stderr, fallback), result.ExitCode)
}

func remoteClean(p string, windows bool) string {
	p = strings.TrimSpace(p)
	if windows {
		p = strings.ReplaceAll(p, "/", `\`)
		for strings.Contains(p, `\\`) && !strings.HasPrefix(p, `\\`) {
			p = strings.ReplaceAll(p, `\\`, `\`)
		}
		return strings.TrimRight(p, `\`)
	}
	return path.Clean(p)
}

func remoteDir(p string, windows bool) string {
	p = remoteClean(p, windows)
	if !windows {
		return path.Dir(p)
	}
	if len(p) == 2 && p[1] == ':' {
		return p + `\`
	}
	i := strings.LastIndex(p, `\`)
	if i < 0 {
		return "."
	}
	if i == 2 && len(p) >= 3 && p[1] == ':' {
		return p[:3]
	}
	if i == 0 {
		return `\`
	}
	return p[:i]
}

func remoteBase(p string, windows bool) string {
	p = remoteClean(p, windows)
	if !windows {
		return path.Base(p)
	}
	i := strings.LastIndex(p, `\`)
	if i < 0 {
		return p
	}
	return p[i+1:]
}

func remoteJoin(base, elem string, windows bool) string {
	if !windows {
		return path.Join(base, elem)
	}
	base = strings.TrimRight(remoteClean(base, true), `\`)
	elem = strings.TrimLeft(strings.ReplaceAll(elem, "/", `\`), `\`)
	if len(base) == 2 && base[1] == ':' {
		return base + `\` + elem
	}
	return base + `\` + elem
}

func (s *SSHTransport) ProbePath(
	ctx context.Context,
	target registry.Target,
	candidatePath string,
	timeout time.Duration,
) (PathProbe, error) {
	var command string
	if isWindowsTarget(target) {
		script := "$ErrorActionPreference='Stop';" +
			"$p=" + powerShellQuote(candidatePath) + ";" +
			"$exists=Test-Path -LiteralPath $p;" +
			"$lexists=$exists;" +
			"$islink=$false;$isfile=$false;$isdir=$false;$canon='';$sha='';$size=0;$content='';" +
			"if($exists){$i=Get-Item -LiteralPath $p -Force;$islink=(($i.Attributes -band [IO.FileAttributes]::ReparsePoint)-ne 0);" +
			"$isdir=$i.PSIsContainer -and -not $islink;$isfile=(-not $i.PSIsContainer) -and -not $islink;" +
			"$canon=[IO.Path]::GetFullPath($i.FullName);" +
			"if($isfile){$size=$i.Length;$sha=(Get-FileHash -LiteralPath $p -Algorithm SHA256).Hash.ToLowerInvariant()};" +
			"$parent=Split-Path -Parent $p;if([string]::IsNullOrWhiteSpace($parent)){$parent='.'};" +
			"$parentExists=Test-Path -LiteralPath $parent;$parentLink=$false;$parentCanon='';" +
			"if($parentExists){$pi=Get-Item -LiteralPath $parent -Force;$parentLink=(($pi.Attributes -band [IO.FileAttributes]::ReparsePoint)-ne 0);" +
			"$parentCanon=[IO.Path]::GetFullPath($pi.FullName)};" +
			"function B64([string]$v){if([string]::IsNullOrEmpty($v)){return ''};return [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($v))};" +
			"Write-Output ('exists='+([int]$lexists));Write-Output ('is_symlink='+([int]$islink));Write-Output ('is_file='+([int]$isfile));Write-Output ('is_dir='+([int]$isdir));" +
			"Write-Output ('canonical_b64='+(B64 $canon));Write-Output ('parent_exists='+([int]$parentExists));Write-Output ('parent_is_symlink='+([int]$parentLink));" +
			"Write-Output ('parent_canonical_b64='+(B64 $parentCanon));Write-Output ('sha256='+$sha);Write-Output ('size='+$size);Write-Output ('content_b64='+$content)"
		command = buildPowerShellCommand(script)
	} else {
		command = "p=" + shellQuote(candidatePath) + `; ` +
			`b64(){ if [ -z "$1" ]; then printf ''; else printf '%s' "$1" | base64 | tr -d '\n'; fi; }; ` +
			`exists=0; islink=0; isfile=0; isdir=0; canon=''; sha=''; size=0; content=''; ` +
			`if [ -L "$p" ]; then exists=1; islink=1; canon=$(realpath -m -- "$p" 2>/dev/null || true); ` +
			`elif [ -e "$p" ]; then exists=1; canon=$(realpath -e -- "$p") || exit 9; ` +
			`if [ -f "$p" ]; then isfile=1; size=$(wc -c < "$p") || exit 9; sha=$(sha256sum -- "$p" | awk '{print $1}') || exit 9; content=$(base64 < "$p" | tr -d '\n') || exit 9; ` +
			`elif [ -d "$p" ]; then isdir=1; fi; fi; ` +
			`parent=$(dirname -- "$p"); parent_exists=0; parent_link=0; parent_canon=''; ` +
			`if [ -L "$parent" ]; then parent_exists=1; parent_link=1; parent_canon=$(realpath -m -- "$parent" 2>/dev/null || true); ` +
			`elif [ -e "$parent" ]; then parent_exists=1; parent_canon=$(realpath -e -- "$parent") || exit 9; fi; ` +
			`printf 'exists=%s\nis_symlink=%s\nis_file=%s\nis_dir=%s\ncanonical_b64=%s\nparent_exists=%s\nparent_is_symlink=%s\nparent_canonical_b64=%s\nsha256=%s\nsize=%s\ncontent_b64=%s\n' ` +
			`"$exists" "$islink" "$isfile" "$isdir" "$(b64 "$canon")" "$parent_exists" "$parent_link" "$(b64 "$parent_canon")" "$sha" "$size" "$content"`
	}
	result, err := s.RunCommand(ctx, target, command, CommandOptions{Timeout: timeout})
	if err != nil {
		return PathProbe{}, err
	}
	if !result.OK() {
		return PathProbe{}, structuredRemoteError(result, "Path probe failed")
	}
	kv := parseKVLines(result.Stdout)
	canonical, err := decodeB64(kv["canonical_b64"])
	if err != nil {
		return PathProbe{}, NewError("SSH_FAILED", "Invalid canonical path encoding in probe", 0)
	}
	parentCanonical, err := decodeB64(kv["parent_canonical_b64"])
	if err != nil {
		return PathProbe{}, NewError("SSH_FAILED", "Invalid parent canonical path encoding in probe", 0)
	}
	contentBytes, err := base64.StdEncoding.DecodeString(kv["content_b64"])
	if err != nil && kv["content_b64"] != "" {
		return PathProbe{}, NewError("SSH_FAILED", "Invalid file content encoding in probe", 0)
	}
	if len(contentBytes) > 0 && !utf8.Valid(contentBytes) {
		return PathProbe{}, NewError("INVALID_ENCODING", "Existing file is not valid UTF-8", 0)
	}
	size, _ := strconv.ParseInt(kv["size"], 10, 64)
	return PathProbe{
		Exists:              kv["exists"] == "1",
		IsSymlink:           kv["is_symlink"] == "1",
		IsFile:              kv["is_file"] == "1",
		IsDir:               kv["is_dir"] == "1",
		CanonicalPath:       canonical,
		ParentExists:        kv["parent_exists"] == "1",
		ParentIsSymlink:     kv["parent_is_symlink"] == "1",
		ParentCanonicalPath: parentCanonical,
		SHA256:              strings.TrimSpace(kv["sha256"]),
		Size:                size,
		Content:             string(contentBytes),
	}, nil
}

func (s *SSHTransport) ResolveSafeDestination(
	ctx context.Context,
	target registry.Target,
	projectRoot, candidatePath string,
	allowMissingParents bool,
	timeout time.Duration,
) (SafeDestination, error) {
	windows := isWindowsTarget(target)
	rootCanonical, err := s.ResolveCanonicalPath(ctx, target, projectRoot, timeout)
	if err != nil {
		return SafeDestination{}, err
	}
	rootProbe, err := s.ProbePath(ctx, target, rootCanonical, timeout)
	if err != nil {
		return SafeDestination{}, err
	}
	if !rootProbe.Exists || !rootProbe.IsDir || rootProbe.IsSymlink {
		return SafeDestination{}, NewError("PROJECT_ROOT_INVALID", "Project root is not a real directory: "+projectRoot, -1)
	}

	destProbe, err := s.ProbePath(ctx, target, candidatePath, timeout)
	if err != nil {
		return SafeDestination{}, err
	}
	if destProbe.IsSymlink {
		return SafeDestination{}, NewError("SYMLINK_WRITE_DENIED", "Destination is a symlink/reparse point: "+candidatePath, -1)
	}

	parent := remoteDir(candidatePath, windows)
	var missing []string
	cursor := parent
	var parentCanonical string
	for {
		probe, err := s.ProbePath(ctx, target, cursor, timeout)
		if err != nil {
			return SafeDestination{}, err
		}
		if probe.Exists {
			if probe.IsSymlink {
				return SafeDestination{}, NewError("SYMLINK_WRITE_DENIED", "Destination ancestor is a symlink/reparse point: "+cursor, -1)
			}
			if !probe.IsDir {
				return SafeDestination{}, NewError("INVALID_PATH", "Destination ancestor is not a directory: "+cursor, -1)
			}
			parentCanonical = probe.CanonicalPath
			if parentCanonical == "" {
				parentCanonical, err = s.ResolveCanonicalPath(ctx, target, cursor, timeout)
				if err != nil {
					return SafeDestination{}, err
				}
			}
			break
		}
		if !allowMissingParents {
			return SafeDestination{}, NewError("NOT_FOUND", "Parent directory does not exist: "+parent, -1)
		}
		base := remoteBase(cursor, windows)
		next := remoteDir(cursor, windows)
		if base == "" || base == "." || next == cursor {
			return SafeDestination{}, NewError("PATH_OUTSIDE_ALLOWED_ROOT", "Unable to anchor destination beneath project root: "+candidatePath, -1)
		}
		missing = append(missing, base)
		cursor = next
	}
	for i := len(missing) - 1; i >= 0; i-- {
		parentCanonical = remoteJoin(parentCanonical, missing[i], windows)
	}
	destination := remoteJoin(parentCanonical, remoteBase(candidatePath, windows), windows)
	canonical := ""
	if destProbe.Exists {
		canonical = destProbe.CanonicalPath
		if canonical == "" {
			canonical, err = s.ResolveCanonicalPath(ctx, target, candidatePath, timeout)
			if err != nil {
				return SafeDestination{}, err
			}
		}
	}
	return SafeDestination{
		RootCanonical:   rootCanonical,
		ParentCanonical: parentCanonical,
		DestinationPath: destination,
		Exists:          destProbe.Exists,
		CanonicalPath:   canonical,
	}, nil
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
	if maxWriteBytes <= 0 {
		maxWriteBytes = 262144
	}
	if len(content) > maxWriteBytes {
		return AtomicWriteResult{}, NewError("FILE_TOO_LARGE", fmt.Sprintf("Content size (%d) exceeds allowed limit of %d bytes", len(content), maxWriteBytes), -1)
	}
	if !utf8.Valid(content) || strings.IndexByte(string(content), 0) >= 0 {
		return AtomicWriteResult{}, NewError("INVALID_ENCODING", "Content must be valid UTF-8 without NUL bytes", -1)
	}
	expectedSHA256 = strings.ToLower(strings.TrimSpace(expectedSHA256))
	createFlag := "0"
	if create {
		createFlag = "1"
	}
	var command string
	if isWindowsTarget(target) {
		script := "$ErrorActionPreference='Stop';$dest=" + powerShellQuote(destPath) + ";$create=" + createFlag + ";$expected=" + powerShellQuote(expectedSHA256) + ";" +
			"$parent=Split-Path -Parent $dest;if(-not (Test-Path -LiteralPath $parent)){Write-Output 'MCPERR|NOT_FOUND|Parent directory does not exist';exit 13};" +
			"$pi=Get-Item -LiteralPath $parent -Force;if(-not $pi.PSIsContainer){Write-Output 'MCPERR|INVALID_PATH|Parent is not a directory';exit 14};" +
			"if(($pi.Attributes -band [IO.FileAttributes]::ReparsePoint)-ne 0){Write-Output 'MCPERR|SYMLINK_WRITE_DENIED|Parent is a reparse point';exit 15};" +
			"$exists=Test-Path -LiteralPath $dest;$old='';" +
			"if($exists){$di=Get-Item -LiteralPath $dest -Force;if(($di.Attributes -band [IO.FileAttributes]::ReparsePoint)-ne 0){Write-Output 'MCPERR|SYMLINK_WRITE_DENIED|Destination is a reparse point';exit 16};" +
			"if($di.PSIsContainer){Write-Output 'MCPERR|INVALID_PATH|Destination is a directory';exit 19};$old=(Get-FileHash -LiteralPath $dest -Algorithm SHA256).Hash.ToLowerInvariant()};" +
			"if($create -eq 1 -and $exists){Write-Output 'MCPERR|FILE_ALREADY_EXISTS|File already exists';exit 17};" +
			"if($create -eq 0 -and -not $exists){Write-Output 'MCPERR|NOT_FOUND|File not found';exit 18};" +
			"if($create -eq 0 -and [string]::IsNullOrWhiteSpace($expected)){Write-Output 'MCPERR|WRITE_CONFLICT|Expected SHA256 is required';exit 20};" +
			"if($create -eq 0 -and $old -ne $expected){Write-Output ('MCPERR|WRITE_CONFLICT|Hash mismatch: found '+$old);exit 21};" +
			"$ms=New-Object IO.MemoryStream;[Console]::OpenStandardInput().CopyTo($ms);$bytes=$ms.ToArray();" +
			"if($bytes.Length -gt " + strconv.Itoa(maxWriteBytes) + "){Write-Output 'MCPERR|FILE_TOO_LARGE|Content exceeds configured limit';exit 22};" +
			"$tmp=Join-Path $parent ('.mcp-gateway.'+[Guid]::NewGuid().ToString('N')+'.tmp');" +
			"try{[IO.File]::WriteAllBytes($tmp,$bytes);$new=(Get-FileHash -LiteralPath $tmp -Algorithm SHA256).Hash.ToLowerInvariant();" +
			"if($create -eq 1){[IO.File]::Move($tmp,$dest)}else{$now=(Get-FileHash -LiteralPath $dest -Algorithm SHA256).Hash.ToLowerInvariant();if($now -ne $expected){Write-Output ('MCPERR|WRITE_CONFLICT|File changed during write: found '+$now);exit 23};[IO.File]::Replace($tmp,$dest,$null)};" +
			"Write-Output ('old_sha256='+$old);Write-Output ('new_sha256='+$new);Write-Output ('bytes='+$bytes.Length)}finally{if(Test-Path -LiteralPath $tmp){Remove-Item -LiteralPath $tmp -Force -ErrorAction SilentlyContinue}}"
		command = buildPowerShellCommand(script)
	} else {
		command = "dest=" + shellQuote(destPath) + "; create=" + createFlag + "; expected=" + shellQuote(expectedSHA256) + "; max=" + strconv.Itoa(maxWriteBytes) + `; ` +
			`parent=$(dirname -- "$dest"); [ -d "$parent" ] || { printf 'MCPERR|NOT_FOUND|Parent directory does not exist\n'; exit 13; }; ` +
			`[ ! -L "$parent" ] || { printf 'MCPERR|SYMLINK_WRITE_DENIED|Parent is a symlink\n'; exit 15; }; ` +
			`exists=0; old=''; if [ -L "$dest" ]; then printf 'MCPERR|SYMLINK_WRITE_DENIED|Destination is a symlink\n'; exit 16; elif [ -e "$dest" ]; then exists=1; ` +
			`[ -f "$dest" ] || { printf 'MCPERR|INVALID_PATH|Destination is not a regular file\n'; exit 19; }; old=$(sha256sum -- "$dest" | awk '{print $1}') || exit 24; fi; ` +
			`if [ "$create" -eq 1 ] && [ "$exists" -eq 1 ]; then printf 'MCPERR|FILE_ALREADY_EXISTS|File already exists\n'; exit 17; fi; ` +
			`if [ "$create" -eq 0 ] && [ "$exists" -eq 0 ]; then printf 'MCPERR|NOT_FOUND|File not found\n'; exit 18; fi; ` +
			`if [ "$create" -eq 0 ] && [ -z "$expected" ]; then printf 'MCPERR|WRITE_CONFLICT|Expected SHA256 is required\n'; exit 20; fi; ` +
			`if [ "$create" -eq 0 ] && [ "$old" != "$expected" ]; then printf 'MCPERR|WRITE_CONFLICT|Hash mismatch: found %s\n' "$old"; exit 21; fi; ` +
			`tmp=$(mktemp "$parent/.mcp-gateway.XXXXXX") || exit 24; trap 'rm -f -- "$tmp"' EXIT HUP INT TERM; cat > "$tmp" || exit 24; ` +
			`bytes=$(wc -c < "$tmp") || exit 24; if [ "$bytes" -gt "$max" ]; then printf 'MCPERR|FILE_TOO_LARGE|Content exceeds configured limit\n'; exit 22; fi; ` +
			`new=$(sha256sum -- "$tmp" | awk '{print $1}') || exit 24; ` +
			`if [ "$create" -eq 1 ]; then chmod 600 "$tmp" || exit 24; mv -n -- "$tmp" "$dest" || { printf 'MCPERR|WRITE_FAILED|Atomic create rename failed\n'; exit 24; }; [ ! -e "$tmp" ] || { printf 'MCPERR|FILE_ALREADY_EXISTS|File appeared during create\n'; exit 17; }; ` +
			`else now=$(sha256sum -- "$dest" | awk '{print $1}') || exit 24; [ "$now" = "$expected" ] || { printf 'MCPERR|WRITE_CONFLICT|File changed during write: found %s\n' "$now"; exit 23; }; ` +
			`chmod --reference="$dest" "$tmp" 2>/dev/null || chmod 600 "$tmp" || exit 24; mv -f -- "$tmp" "$dest" || exit 24; fi; ` +
			`trap - EXIT HUP INT TERM; printf 'old_sha256=%s\nnew_sha256=%s\nbytes=%s\n' "$old" "$new" "$bytes"`
	}
	result, err := s.RunCommand(ctx, target, command, CommandOptions{Stdin: content, Timeout: timeout})
	if err != nil {
		return AtomicWriteResult{}, err
	}
	if !result.OK() {
		return AtomicWriteResult{}, structuredRemoteError(result, "Atomic write failed")
	}
	kv := parseKVLines(result.Stdout)
	newSHA := strings.TrimSpace(kv["new_sha256"])
	if len(newSHA) != 64 {
		return AtomicWriteResult{}, NewError("SSH_FAILED", "Atomic write returned invalid SHA256", 0)
	}
	bytesWritten, err := strconv.Atoi(strings.TrimSpace(kv["bytes"]))
	if err != nil {
		return AtomicWriteResult{}, NewError("SSH_FAILED", "Atomic write returned invalid byte count", 0)
	}
	var old *string
	if v := strings.TrimSpace(kv["old_sha256"]); v != "" {
		old = &v
	}
	return AtomicWriteResult{
		Created:      create,
		OldSHA256:    old,
		NewSHA256:    newSHA,
		BytesWritten: bytesWritten,
		Atomic:       true,
	}, nil
}

func (s *SSHTransport) AppendFile(
	ctx context.Context,
	target registry.Target,
	destPath string,
	content []byte,
	maxWriteBytes int,
	timeout time.Duration,
) (string, error) {
	if maxWriteBytes <= 0 {
		maxWriteBytes = 262144
	}
	if len(content) > maxWriteBytes {
		return "", NewError("FILE_TOO_LARGE", "Append content exceeds configured limit", -1)
	}
	if !utf8.Valid(content) || strings.IndexByte(string(content), 0) >= 0 {
		return "", NewError("INVALID_ENCODING", "Content must be valid UTF-8 without NUL bytes", -1)
	}
	var command string
	if isWindowsTarget(target) {
		script := "$ErrorActionPreference='Stop';$p=" + powerShellQuote(destPath) + ";" +
			"if(-not (Test-Path -LiteralPath $p)){Write-Output 'MCPERR|NOT_FOUND|File not found';exit 2};$i=Get-Item -LiteralPath $p -Force;" +
			"if($i.PSIsContainer -or (($i.Attributes -band [IO.FileAttributes]::ReparsePoint)-ne 0)){Write-Output 'MCPERR|INVALID_PATH|Append target is not a regular file';exit 3};" +
			"$ms=New-Object IO.MemoryStream;[Console]::OpenStandardInput().CopyTo($ms);$bytes=$ms.ToArray();if($bytes.Length -gt " + strconv.Itoa(maxWriteBytes) + "){Write-Output 'MCPERR|FILE_TOO_LARGE|Append exceeds configured limit';exit 4};" +
			"$fs=[IO.File]::Open($p,[IO.FileMode]::Append,[IO.FileAccess]::Write,[IO.FileShare]::Read);try{$fs.Write($bytes,0,$bytes.Length);$fs.Flush($true)}finally{$fs.Dispose()};" +
			"(Get-FileHash -LiteralPath $p -Algorithm SHA256).Hash.ToLowerInvariant()"
		command = buildPowerShellCommand(script)
	} else {
		command = "p=" + shellQuote(destPath) + "; max=" + strconv.Itoa(maxWriteBytes) + `; ` +
			`[ -e "$p" ] || { printf 'MCPERR|NOT_FOUND|File not found\n'; exit 2; }; [ ! -L "$p" ] && [ -f "$p" ] || { printf 'MCPERR|INVALID_PATH|Append target is not a regular file\n'; exit 3; }; ` +
			`tmp=$(mktemp) || exit 5; trap 'rm -f -- "$tmp"' EXIT HUP INT TERM; cat > "$tmp" || exit 5; bytes=$(wc -c < "$tmp") || exit 5; ` +
			`[ "$bytes" -le "$max" ] || { printf 'MCPERR|FILE_TOO_LARGE|Append exceeds configured limit\n'; exit 4; }; cat "$tmp" >> "$p" || exit 5; ` +
			`sha256sum -- "$p" | awk '{print $1}'`
	}
	result, err := s.RunCommand(ctx, target, command, CommandOptions{Stdin: content, Timeout: timeout})
	if err != nil {
		return "", err
	}
	if !result.OK() {
		return "", structuredRemoteError(result, "Append failed")
	}
	sha := lastNonEmptyLine(result.Stdout)
	if len(sha) != 64 {
		return "", NewError("SSH_FAILED", "Append returned invalid SHA256", 0)
	}
	return strings.ToLower(sha), nil
}

func (s *SSHTransport) DeletePath(
	ctx context.Context,
	target registry.Target,
	targetPath string,
	timeout time.Duration,
) error {
	var command string
	if isWindowsTarget(target) {
		script := "$ErrorActionPreference='Stop';$p=" + powerShellQuote(targetPath) + ";" +
			"if(-not (Test-Path -LiteralPath $p)){Write-Output 'MCPERR|NOT_FOUND|Path not found';exit 2};$i=Get-Item -LiteralPath $p -Force;" +
			"if(($i.Attributes -band [IO.FileAttributes]::ReparsePoint)-ne 0){Write-Output 'MCPERR|SYMLINK_WRITE_DENIED|Reparse point deletion denied';exit 3};" +
			"if($i.PSIsContainer){[IO.Directory]::Delete($p,$false)}else{[IO.File]::Delete($p)}"
		command = buildPowerShellCommand(script)
	} else {
		command = "p=" + shellQuote(targetPath) + `; ` +
			`if [ -L "$p" ]; then printf 'MCPERR|SYMLINK_WRITE_DENIED|Symlink deletion denied\n'; exit 3; fi; ` +
			`[ -e "$p" ] || { printf 'MCPERR|NOT_FOUND|Path not found\n'; exit 2; }; ` +
			`if [ -d "$p" ]; then rmdir -- "$p"; else rm -- "$p"; fi`
	}
	result, err := s.RunCommand(ctx, target, command, CommandOptions{Timeout: timeout})
	if err != nil {
		return err
	}
	if !result.OK() {
		return structuredRemoteError(result, "Delete failed")
	}
	return nil
}

func (s *SSHTransport) CopyMovePath(
	ctx context.Context,
	target registry.Target,
	sourcePath, destPath string,
	move bool,
	timeout time.Duration,
) error {
	moveFlag := "0"
	if move {
		moveFlag = "1"
	}
	var command string
	if isWindowsTarget(target) {
		script := "$ErrorActionPreference='Stop';$src=" + powerShellQuote(sourcePath) + ";$dst=" + powerShellQuote(destPath) + ";$move=" + moveFlag + ";" +
			"if(-not (Test-Path -LiteralPath $src)){Write-Output 'MCPERR|NOT_FOUND|Source file not found';exit 2};$i=Get-Item -LiteralPath $src -Force;" +
			"if($i.PSIsContainer){Write-Output 'MCPERR|INVALID_PATH|Source is not a file';exit 3};if(($i.Attributes -band [IO.FileAttributes]::ReparsePoint)-ne 0){Write-Output 'MCPERR|SYMLINK_WRITE_DENIED|Source reparse point denied';exit 4};" +
			"if(Test-Path -LiteralPath $dst){Write-Output 'MCPERR|FILE_ALREADY_EXISTS|Destination already exists';exit 5};$parent=Split-Path -Parent $dst;if(-not (Test-Path -LiteralPath $parent)){Write-Output 'MCPERR|NOT_FOUND|Destination parent not found';exit 6};" +
			"if($move -eq 1){[IO.File]::Move($src,$dst)}else{[IO.File]::Copy($src,$dst,$false)|Out-Null}"
		command = buildPowerShellCommand(script)
	} else {
		command = "src=" + shellQuote(sourcePath) + "; dst=" + shellQuote(destPath) + "; move=" + moveFlag + `; ` +
			`if [ -L "$src" ]; then printf 'MCPERR|SYMLINK_WRITE_DENIED|Source symlink denied\n'; exit 4; fi; ` +
			`[ -f "$src" ] || { printf 'MCPERR|NOT_FOUND|Source file not found or not regular\n'; exit 2; }; ` +
			`[ ! -e "$dst" ] && [ ! -L "$dst" ] || { printf 'MCPERR|FILE_ALREADY_EXISTS|Destination already exists\n'; exit 5; }; ` +
			`parent=$(dirname -- "$dst"); [ -d "$parent" ] && [ ! -L "$parent" ] || { printf 'MCPERR|NOT_FOUND|Destination parent not found\n'; exit 6; }; ` +
			`if [ "$move" -eq 1 ]; then mv -- "$src" "$dst"; else cp -p -- "$src" "$dst"; fi`
	}
	result, err := s.RunCommand(ctx, target, command, CommandOptions{Timeout: timeout})
	if err != nil {
		return err
	}
	if !result.OK() {
		return structuredRemoteError(result, "Copy/move failed")
	}
	return nil
}

func (s *SSHTransport) Mkdir(
	ctx context.Context,
	target registry.Target,
	targetPath string,
	parents bool,
	timeout time.Duration,
) error {
	parentsFlag := "0"
	if parents {
		parentsFlag = "1"
	}
	var command string
	if isWindowsTarget(target) {
		script := "$ErrorActionPreference='Stop';$p=" + powerShellQuote(targetPath) + ";$parents=" + parentsFlag + ";" +
			"if(Test-Path -LiteralPath $p){Write-Output 'MCPERR|FILE_ALREADY_EXISTS|Path already exists';exit 2};" +
			"if($parents -eq 1){[IO.Directory]::CreateDirectory($p)|Out-Null}else{$parent=Split-Path -Parent $p;if(-not (Test-Path -LiteralPath $parent)){Write-Output 'MCPERR|NOT_FOUND|Parent directory not found';exit 3};[IO.Directory]::CreateDirectory($p)|Out-Null}"
		command = buildPowerShellCommand(script)
	} else {
		command = "p=" + shellQuote(targetPath) + "; parents=" + parentsFlag + `; ` +
			`if [ -e "$p" ] || [ -L "$p" ]; then printf 'MCPERR|FILE_ALREADY_EXISTS|Path already exists\n'; exit 2; fi; ` +
			`if [ "$parents" -eq 1 ]; then mkdir -p -- "$p"; else mkdir -- "$p"; fi`
	}
	result, err := s.RunCommand(ctx, target, command, CommandOptions{Timeout: timeout})
	if err != nil {
		return err
	}
	if !result.OK() {
		return structuredRemoteError(result, "mkdir failed")
	}
	return nil
}
