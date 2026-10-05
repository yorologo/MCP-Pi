package remote

import (
	"context"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"mcp-gateway-adapter/internal/registry"
)

func windowsResolveCanonicalPathScript(candidatePath string) string {
	return "$ErrorActionPreference='Stop';$p=" + powerShellQuote(candidatePath) + ";" +
		"try{$item=Get-Item -LiteralPath $p -Force -ErrorAction Stop}" +
		"catch [System.Management.Automation.ItemNotFoundException]{Write-Output 'MCPERR|NOT_FOUND|Path does not exist';exit 2}" +
		"catch [System.UnauthorizedAccessException]{Write-Output 'MCPERR|PERMISSION_DENIED|Access denied to path';exit 25}" +
		"catch{if($_.Exception -is [System.UnauthorizedAccessException] -or $_.Exception.InnerException -is [System.UnauthorizedAccessException]){Write-Output 'MCPERR|PERMISSION_DENIED|Access denied to path';exit 25};" +
		"Write-Output ('MCPERR|SSH_FAILED|Canonical path probe failed: '+$_.Exception.Message);exit 26};" +
		"if(($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0){Write-Output 'MCPERR|SYMLINK_WRITE_DENIED|Reparse-point paths are not accepted as canonical roots on Windows';exit 6};" +
		"[IO.Path]::GetFullPath($item.FullName)"
}

func (s *SSHTransport) ResolveCanonicalPath(
	ctx context.Context,
	target registry.Target,
	candidatePath string,
	timeout time.Duration,
) (string, error) {
	var command string
	if isWindowsTarget(target) {
		command = buildPowerShellCommand(windowsResolveCanonicalPathScript(candidatePath))
	} else {
		command = "p=" + shellQuote(candidatePath) + "; if [ ! -e \"$p\" ] && [ ! -L \"$p\" ]; then exit 2; fi; realpath -e -- \"$p\""
	}
	result, err := s.RunCommand(ctx, target, command, CommandOptions{Timeout: timeout})
	if err != nil {
		return "", err
	}
	if !result.OK() {
		if isWindowsTarget(target) {
			return "", structuredRemoteError(result, "Unable to resolve canonical path on target")
		}
		if result.ExitCode == 2 {
			return "", NewError("NOT_FOUND", "Path does not exist: "+candidatePath, result.ExitCode)
		}
		return "", NewError("SSH_FAILED", nonEmptyRemote(result.Stderr, "Unable to resolve canonical path on target"), result.ExitCode)
	}
	canonical := lastNonEmptyLine(result.Stdout)
	if canonical == "" {
		return "", NewError("SSH_FAILED", "Canonical path probe returned no path", 0)
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
	var command string
	if isWindowsTarget(target) {
		script := "$p=" + powerShellQuote(canonicalPath) + ";" +
			"$limit=" + strconv.Itoa(limit) + ";" +
			"if(-not (Test-Path -LiteralPath $p)){exit 2};" +
			"$item=Get-Item -LiteralPath $p -Force;if(-not $item.PSIsContainer){exit 3};" +
			"Get-ChildItem -LiteralPath $p -Force | Sort-Object Name | Select-Object -First $limit | ForEach-Object {" +
			"$name=[Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($_.Name));" +
			"$type=if(($_.Attributes -band [IO.FileAttributes]::ReparsePoint)-ne 0){'symlink'}elseif($_.PSIsContainer){'directory'}else{'file'};" +
			"$size=if($_.PSIsContainer){''}else{$_.Length};Write-Output ($name+\"`t\"+$type+\"`t\"+$size)}"
		command = buildPowerShellCommand(script)
	} else {
		command = "p=" + shellQuote(canonicalPath) + "; limit=" + strconv.Itoa(limit) + `; ` +
			`if [ ! -e "$p" ]; then exit 2; fi; if [ ! -d "$p" ]; then exit 3; fi; ` +
			`find "$p" -mindepth 1 -maxdepth 1 -printf '%f\0%y\0%s\0' | head -z -n $((limit * 3))`
	}
	result, err := s.RunCommand(ctx, target, command, CommandOptions{Timeout: timeout})
	if err != nil {
		return nil, err
	}
	switch result.ExitCode {
	case 0:
	case 2:
		return nil, NewError("NOT_FOUND", "Directory not found: "+canonicalPath, result.ExitCode)
	case 3:
		return nil, NewError("NOT_DIRECTORY", "Path is not a directory: "+canonicalPath, result.ExitCode)
	default:
		return nil, NewError("SSH_FAILED", nonEmptyRemote(result.Stderr, "Failed to list directory"), result.ExitCode)
	}

	var entries []DirectoryEntry
	if isWindowsTarget(target) {
		for _, line := range strings.Split(strings.TrimSpace(result.Stdout), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			parts := strings.SplitN(strings.TrimSuffix(line, "\r"), "\t", 3)
			if len(parts) != 3 {
				return nil, NewError("SSH_FAILED", "Invalid Windows directory listing response", 0)
			}
			nameBytes, err := base64.StdEncoding.DecodeString(parts[0])
			if err != nil {
				return nil, NewError("SSH_FAILED", "Invalid Windows directory entry encoding", 0)
			}
			entry := DirectoryEntry{Name: string(nameBytes), Type: parts[1]}
			if parts[2] != "" {
				size, err := strconv.ParseInt(strings.TrimSpace(parts[2]), 10, 64)
				if err != nil {
					return nil, NewError("SSH_FAILED", "Invalid Windows directory entry size", 0)
				}
				entry.Size = &size
			}
			entries = append(entries, entry)
		}
	} else {
		parts := strings.Split(result.Stdout, "\x00")
		for i := 0; i+2 < len(parts); i += 3 {
			if parts[i] == "" && parts[i+1] == "" && parts[i+2] == "" {
				continue
			}
			entryType := "other"
			switch parts[i+1] {
			case "f":
				entryType = "file"
			case "d":
				entryType = "directory"
			case "l":
				entryType = "symlink"
			}
			entry := DirectoryEntry{Name: parts[i], Type: entryType}
			if entryType == "file" {
				size, err := strconv.ParseInt(parts[i+2], 10, 64)
				if err == nil {
					entry.Size = &size
				}
			}
			entries = append(entries, entry)
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
		if len(entries) > limit {
			entries = entries[:limit]
		}
	}
	return entries, nil
}

func (s *SSHTransport) FileStat(
	ctx context.Context,
	target registry.Target,
	canonicalPath string,
	timeout time.Duration,
) (FileStat, error) {
	var command string
	if isWindowsTarget(target) {
		script := "$p=" + powerShellQuote(canonicalPath) + ";" +
			"if(-not (Test-Path -LiteralPath $p)){exit 2};$i=Get-Item -LiteralPath $p -Force;" +
			"$type=if(($i.Attributes -band [IO.FileAttributes]::ReparsePoint)-ne 0){'symlink'}elseif($i.PSIsContainer){'directory'}else{'file'};" +
			"$size=if($i.PSIsContainer){0}else{$i.Length};$mtime=([DateTimeOffset]$i.LastWriteTimeUtc).ToUnixTimeSeconds();" +
			"Write-Output ($type+\"`t\"+$size+\"`t\"+$mtime)"
		command = buildPowerShellCommand(script)
	} else {
		command = "p=" + shellQuote(canonicalPath) + `; if [ ! -e "$p" ] && [ ! -L "$p" ]; then exit 2; fi; ` +
			`type=other; [ -L "$p" ] && type=symlink || { [ -d "$p" ] && type=directory || { [ -f "$p" ] && type=file || true; }; }; ` +
			`size=$(stat -c %s -- "$p") || exit 4; mtime=$(stat -c %Y -- "$p") || exit 4; printf '%s\t%s\t%s\n' "$type" "$size" "$mtime"`
	}
	result, err := s.RunCommand(ctx, target, command, CommandOptions{Timeout: timeout})
	if err != nil {
		return FileStat{}, err
	}
	if result.ExitCode == 2 {
		return FileStat{}, NewError("NOT_FOUND", "Path not found: "+canonicalPath, result.ExitCode)
	}
	if !result.OK() {
		return FileStat{}, NewError("SSH_FAILED", nonEmptyRemote(result.Stderr, "Stat failed"), result.ExitCode)
	}
	parts := strings.Split(strings.TrimSpace(result.Stdout), "\t")
	if len(parts) != 3 {
		return FileStat{}, NewError("SSH_FAILED", "Invalid file stat response", 0)
	}
	size, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
	if err != nil {
		return FileStat{}, NewError("SSH_FAILED", "Invalid file size in stat response", 0)
	}
	mtime, err := strconv.ParseInt(strings.TrimSpace(parts[2]), 10, 64)
	if err != nil {
		return FileStat{}, NewError("SSH_FAILED", "Invalid mtime in stat response", 0)
	}
	return FileStat{Exists: true, Type: parts[0], Size: size, MTime: mtime}, nil
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
	var command string
	if isWindowsTarget(target) {
		script := "$p=" + powerShellQuote(canonicalPath) + ";$limit=" + strconv.FormatInt(maxBytes, 10) + ";" +
			"if(-not (Test-Path -LiteralPath $p)){exit 2};$i=Get-Item -LiteralPath $p -Force;if($i.PSIsContainer -or (($i.Attributes -band [IO.FileAttributes]::ReparsePoint)-ne 0)){exit 3};if($i.Length -gt $limit){exit 4};" +
			"[Convert]::ToBase64String([IO.File]::ReadAllBytes($p))"
		command = buildPowerShellCommand(script)
	} else {
		command = "p=" + shellQuote(canonicalPath) + "; limit=" + strconv.FormatInt(maxBytes, 10) + `; ` +
			`if [ ! -e "$p" ]; then exit 2; fi; if [ -L "$p" ] || [ ! -f "$p" ]; then exit 3; fi; size=$(wc -c < "$p") || exit 5; ` +
			`if [ "$size" -gt "$limit" ]; then exit 4; fi; base64 < "$p" | tr -d '\n'`
	}
	maxOutput := int(maxBytes + maxBytes/3 + 8192)
	if maxOutput < defaultMaxOutput {
		maxOutput = defaultMaxOutput
	}
	result, err := s.RunCommand(ctx, target, command, CommandOptions{Timeout: timeout, MaxOutputBytes: maxOutput})
	if err != nil {
		return "", err
	}
	switch result.ExitCode {
	case 0:
	case 2:
		return "", NewError("NOT_FOUND", "File not found: "+canonicalPath, result.ExitCode)
	case 3:
		return "", NewError("INVALID_PATH", "Target path is not a regular file: "+canonicalPath, result.ExitCode)
	case 4:
		return "", NewError("FILE_TOO_LARGE", fmt.Sprintf("File size exceeds allowed limit of %d bytes", maxBytes), result.ExitCode)
	default:
		return "", NewError("SSH_FAILED", nonEmptyRemote(result.Stderr, "Error reading file"), result.ExitCode)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(result.Stdout))
	if err != nil {
		return "", NewError("SSH_FAILED", "Remote file payload was not valid base64", 0)
	}
	if len(raw) > int(maxBytes) {
		return "", NewError("FILE_TOO_LARGE", fmt.Sprintf("File size exceeds allowed limit of %d bytes", maxBytes), 0)
	}
	if strings.IndexByte(string(raw), 0) >= 0 {
		return "", NewError("BINARY_FILE_NOT_SUPPORTED", "Binary file not supported", 0)
	}
	if !utf8.Valid(raw) {
		return "", NewError("INVALID_ENCODING", "File is not valid UTF-8", 0)
	}
	return string(raw), nil
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

var grepLineRE = regexp.MustCompile(`^(.*):([0-9]+):(.*)$`)

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
	var command string
	if isWindowsTarget(target) {
		simple := "$false"
		if !isRegex {
			simple = "$true"
		}
		script := "$base=" + powerShellQuote(searchRoot) + ";$pattern=" + powerShellQuote(pattern) + ";$limit=" + strconv.Itoa(limit) + ";" +
			"if(-not (Test-Path -LiteralPath $base)){exit 2};" +
			"Get-ChildItem -LiteralPath $base -File -Recurse -ErrorAction SilentlyContinue | Select-String -Pattern $pattern -SimpleMatch:" + simple + " -ErrorAction SilentlyContinue | Select-Object -First $limit | ForEach-Object {" +
			"$p=[Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($_.Path));$t=[Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($_.Line));Write-Output ($p+\"`t\"+$_.LineNumber+\"`t\"+$t)}"
		command = buildPowerShellCommand(script)
	} else {
		mode := "-E"
		if !isRegex {
			mode = "-F"
		}
		command = "base=" + shellQuote(searchRoot) + "; pattern=" + shellQuote(pattern) + "; limit=" + strconv.Itoa(limit) + `; ` +
			`if [ ! -d "$base" ]; then exit 2; fi; ` +
			`{ grep -RInI ` + mode + ` -- "$pattern" "$base" || [ "$?" -eq 1 ]; } | head -n "$limit"`
	}
	result, err := s.RunCommand(ctx, target, command, CommandOptions{Timeout: timeout})
	if err != nil {
		return nil, err
	}
	if result.ExitCode == 2 {
		return nil, NewError("NOT_FOUND", "Search root not found: "+searchRoot, result.ExitCode)
	}
	if !result.OK() {
		return nil, NewError("SSH_FAILED", nonEmptyRemote(result.Stderr, "Search failed"), result.ExitCode)
	}
	var matches []SearchMatch
	for _, line := range strings.Split(strings.TrimSpace(result.Stdout), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		if isWindowsTarget(target) {
			parts := strings.SplitN(line, "\t", 3)
			if len(parts) != 3 {
				return nil, NewError("SSH_FAILED", "Invalid Windows search response", 0)
			}
			pb, err := base64.StdEncoding.DecodeString(parts[0])
			if err != nil {
				return nil, NewError("SSH_FAILED", "Invalid Windows search path encoding", 0)
			}
			tb, err := base64.StdEncoding.DecodeString(parts[2])
			if err != nil {
				return nil, NewError("SSH_FAILED", "Invalid Windows search text encoding", 0)
			}
			lineNo, err := strconv.Atoi(parts[1])
			if err != nil {
				return nil, NewError("SSH_FAILED", "Invalid Windows search line number", 0)
			}
			matches = append(matches, SearchMatch{File: remoteRelative(projectRoot, string(pb), true), Line: lineNo, Text: string(tb)})
			continue
		}
		m := grepLineRE.FindStringSubmatch(line)
		if len(m) != 4 {
			continue
		}
		lineNo, err := strconv.Atoi(m[2])
		if err != nil {
			continue
		}
		matches = append(matches, SearchMatch{File: remoteRelative(projectRoot, m[1], false), Line: lineNo, Text: m[3]})
	}
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches, nil
}

func remoteRelative(root, candidate string, windows bool) string {
	if windows {
		r := strings.ReplaceAll(strings.TrimSpace(root), "/", `\`)
		c := strings.ReplaceAll(strings.TrimSpace(candidate), "/", `\`)
		rl := strings.ToLower(strings.TrimRight(r, `\`))
		cl := strings.ToLower(c)
		if cl == rl {
			return "."
		}
		prefix := rl + `\`
		if strings.HasPrefix(cl, prefix) {
			return strings.ReplaceAll(c[len(prefix):], `\`, "/")
		}
		return strings.ReplaceAll(c, `\`, "/")
	}
	if rel, err := filepath.Rel(root, candidate); err == nil {
		return filepath.ToSlash(rel)
	}
	return candidate
}
