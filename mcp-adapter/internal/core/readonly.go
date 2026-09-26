package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	"mcp-gateway-adapter/internal/policy"
	"mcp-gateway-adapter/internal/registry"
	"mcp-gateway-adapter/internal/remote"
)

func (c *Core) TargetStatus(ctx context.Context, requestID, targetID string) Response {
	started := time.Now()
	target, err := c.store.GetTarget(ctx, targetID, false)
	if err != nil {
		return corePolicyError("target_status", err, requestID, targetID, "", started)
	}
	transport, err := remote.Required(c.remote)
	if err != nil {
		return errorResponse("target_status", "INTERNAL_ERROR", err.Error(), requestID, targetID, "", started)
	}

	status, err := transport.RunCommand(
		ctx,
		target,
		"hostname",
		remote.CommandOptions{Timeout: 10 * time.Second},
	)
	if err != nil {
		return errorResponse("target_status", remoteCodeOr(err, "SSH_FAILED"), err.Error(), requestID, targetID, "", started)
	}
	if !status.OK() {
		return errorResponse(
			"target_status",
			"SSH_FAILED",
			"Remote status check failed: "+strings.TrimSpace(status.Stderr),
			requestID,
			targetID,
			"",
			started,
		)
	}

	facts, err := transport.ProbeFacts(ctx, target, true, 12*time.Second)
	if err != nil {
		facts = map[string]any{"probe_status": "unavailable", "reason": err.Error()}
	}

	return successResponse("target_status", map[string]any{
		"reachable":       true,
		"remote_hostname": strings.TrimSpace(status.Stdout),
		"latency_ms":      status.DurationMS,
		"platform":        target.Platform,
		"facts":           facts,
	}, requestID, targetID, "", started)
}

func (c *Core) ListDirectory(ctx context.Context, requestID, targetID, projectID, relativePath string) Response {
	started := time.Now()
	target, project, err := c.readContext(ctx, targetID, projectID)
	if err != nil {
		return corePolicyError("list_directory", err, requestID, targetID, projectID, started)
	}
	canonical, err := c.resolveProjectPath(ctx, target, project, relativePath)
	if err != nil {
		return corePolicyError("list_directory", err, requestID, targetID, projectID, started)
	}
	transport, err := remote.Required(c.remote)
	if err != nil {
		return errorResponse("list_directory", "INTERNAL_ERROR", err.Error(), requestID, targetID, projectID, started)
	}
	entries, err := transport.ListDirectory(ctx, target, canonical, 200, 15*time.Second)
	if err != nil {
		switch remote.ErrorCode(err) {
		case "NOT_FOUND":
			return errorResponse("list_directory", "NOT_FOUND", fmt.Sprintf("Directory not found: %s", relativePath), requestID, targetID, projectID, started)
		case "NOT_DIRECTORY":
			return errorResponse("list_directory", "INVALID_PATH", fmt.Sprintf("Path is not a directory: %s", relativePath), requestID, targetID, projectID, started)
		default:
			message := strings.TrimSpace(err.Error())
			if message == "" {
				message = "Failed to list directory"
			}
			return errorResponse("list_directory", "SSH_FAILED", message, requestID, targetID, projectID, started)
		}
	}

	out := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		var size any
		if entry.Size != nil {
			size = *entry.Size
		}
		out = append(out, map[string]any{
			"name": entry.Name,
			"type": entry.Type,
			"size": size,
		})
	}
	return successResponse("list_directory", map[string]any{
		"relative_path":  relativePath,
		"canonical_path": canonical,
		"count":          len(out),
		"entries":        out,
	}, requestID, targetID, projectID, started)
}

func (c *Core) FileStat(ctx context.Context, requestID, targetID, projectID, relativePath string) Response {
	started := time.Now()
	target, project, err := c.readContext(ctx, targetID, projectID)
	if err != nil {
		return corePolicyError("file_stat", err, requestID, targetID, projectID, started)
	}
	canonical, err := c.resolveProjectPath(ctx, target, project, relativePath)
	if err != nil {
		return corePolicyError("file_stat", err, requestID, targetID, projectID, started)
	}
	transport, err := remote.Required(c.remote)
	if err != nil {
		return errorResponse("file_stat", "INTERNAL_ERROR", err.Error(), requestID, targetID, projectID, started)
	}
	stat, err := transport.FileStat(ctx, target, canonical, 10*time.Second)
	if err != nil {
		if remote.ErrorCode(err) == "NOT_FOUND" {
			return errorResponse("file_stat", "NOT_FOUND", fmt.Sprintf("File not found: %s", relativePath), requestID, targetID, projectID, started)
		}
		return errorResponse("file_stat", "SSH_FAILED", nonEmpty(err.Error(), "Stat failed"), requestID, targetID, projectID, started)
	}

	return successResponse("file_stat", map[string]any{
		"exists":         stat.Exists,
		"type":           stat.Type,
		"size":           stat.Size,
		"mtime":          stat.MTime,
		"relative_path":  relativePath,
		"canonical_path": canonical,
	}, requestID, targetID, projectID, started)
}

func (c *Core) ReadFile(ctx context.Context, requestID, targetID, projectID, relativePath string) Response {
	started := time.Now()
	target, project, err := c.readContext(ctx, targetID, projectID)
	if err != nil {
		return corePolicyError("read_file", err, requestID, targetID, projectID, started)
	}
	canonical, err := c.resolveProjectPath(ctx, target, project, relativePath)
	if err != nil {
		return corePolicyError("read_file", err, requestID, targetID, projectID, started)
	}
	transport, err := remote.Required(c.remote)
	if err != nil {
		return errorResponse("read_file", "INTERNAL_ERROR", err.Error(), requestID, targetID, projectID, started)
	}

	limit, err := c.intSetting(ctx, "max_file_read_bytes", 1048576)
	if err != nil {
		return errorResponse("read_file", "INTERNAL_ERROR", err.Error(), requestID, targetID, projectID, started)
	}
	content, err := transport.ReadFile(ctx, target, canonical, int64(limit), 15*time.Second)
	if err != nil {
		code := remote.ErrorCode(err)
		if code == "" {
			code = "SSH_FAILED"
		}
		return errorResponse("read_file", code, err.Error(), requestID, targetID, projectID, started)
	}

	sum := sha256.Sum256([]byte(content))
	return successResponse("read_file", map[string]any{
		"relative_path":  relativePath,
		"canonical_path": canonical,
		"size_bytes":     len([]byte(content)),
		"content":        content,
		"sha256":         hex.EncodeToString(sum[:]),
	}, requestID, targetID, projectID, started)
}

func (c *Core) GitStatus(ctx context.Context, requestID, targetID, projectID string) Response {
	started := time.Now()
	target, project, err := c.projectContext(ctx, targetID, projectID, false)
	if err != nil {
		return corePolicyError("git_status", err, requestID, targetID, projectID, started)
	}
	transport, err := remote.Required(c.remote)
	if err != nil {
		return errorResponse("git_status", "INTERNAL_ERROR", err.Error(), requestID, targetID, projectID, started)
	}
	canonical, err := transport.ResolveCanonicalPath(ctx, target, project.Root, 10*time.Second)
	if err != nil {
		return errorResponse("git_status", remoteCodeOr(err, "SSH_FAILED"), err.Error(), requestID, targetID, projectID, started)
	}
	if err := policy.ValidateCanonicalPath(canonical, project.Root, target.Platform); err != nil {
		return corePolicyError("git_status", err, requestID, targetID, projectID, started)
	}

	status, err := transport.GitStatus(ctx, target, canonical, 20*time.Second)
	if err != nil {
		return errorResponse("git_status", "SSH_FAILED", "git status failed: "+strings.TrimSpace(err.Error()), requestID, targetID, projectID, started)
	}
	return successResponse("git_status", map[string]any{
		"status_output": strings.TrimSpace(status),
	}, requestID, targetID, projectID, started)
}

func (c *Core) Search(ctx context.Context, requestID, targetID, projectID, patternText, relativePath string, isRegex bool) Response {
	started := time.Now()
	if patternText == "" {
		return errorResponse("search", "INVALID_ARGUMENTS", "Pattern must not be empty", requestID, targetID, projectID, started)
	}

	target, project, err := c.readContext(ctx, targetID, projectID)
	if err != nil {
		return corePolicyError("search", err, requestID, targetID, projectID, started)
	}
	relDir := relativePath
	if relDir == "" {
		relDir = "."
	}
	relDir, err = policy.ValidateRelativePath(relDir)
	if err != nil {
		return corePolicyError("search", err, requestID, targetID, projectID, started)
	}

	transport, err := remote.Required(c.remote)
	if err != nil {
		return errorResponse("search", "INTERNAL_ERROR", err.Error(), requestID, targetID, projectID, started)
	}
	rootCanonical, err := transport.ResolveCanonicalPath(ctx, target, project.Root, 10*time.Second)
	if err != nil {
		return errorResponse("search", remoteCodeOr(err, "SSH_FAILED"), err.Error(), requestID, targetID, projectID, started)
	}
	if err := policy.ValidateCanonicalPath(rootCanonical, project.Root, target.Platform); err != nil {
		return corePolicyError("search", err, requestID, targetID, projectID, started)
	}

	searchRoot := joinRemotePath(rootCanonical, relDir, target.Platform)
	if err := policy.ValidateCanonicalPath(searchRoot, rootCanonical, target.Platform); err != nil {
		return corePolicyError("search", err, requestID, targetID, projectID, started)
	}
	matches, err := transport.Search(ctx, target, rootCanonical, searchRoot, patternText, isRegex, 100, 20*time.Second)
	if err != nil {
		return errorResponse("search", remoteCodeOr(err, "INTERNAL_ERROR"), err.Error(), requestID, targetID, projectID, started)
	}

	out := make([]map[string]any, 0, len(matches))
	for _, match := range matches {
		out = append(out, map[string]any{
			"file": match.File,
			"line": match.Line,
			"text": match.Text,
		})
	}
	return successResponse("search", map[string]any{
		"pattern": patternText,
		"path":    relDir,
		"matches": out,
		"count":   len(out),
	}, requestID, targetID, projectID, started)
}

func (c *Core) readContext(ctx context.Context, targetID, projectID string) (registry.Target, registry.Project, error) {
	return c.projectContext(ctx, targetID, projectID, true)
}

func (c *Core) projectContext(ctx context.Context, targetID, projectID string, requireRead bool) (registry.Target, registry.Project, error) {
	target, err := c.store.GetTarget(ctx, targetID, false)
	if err != nil {
		return registry.Target{}, registry.Project{}, err
	}
	project, err := c.store.GetProject(ctx, targetID, projectID, false)
	if err != nil {
		return registry.Target{}, registry.Project{}, err
	}
	if requireRead {
		read := project.Read
		write := project.Write
		if err := policy.CheckCapability(policy.CapabilityState{Read: &read, Write: &write}, "read"); err != nil {
			return registry.Target{}, registry.Project{}, err
		}
	}
	return target, project, nil
}

func (c *Core) resolveProjectPath(ctx context.Context, target registry.Target, project registry.Project, relativePath string) (string, error) {
	clean, err := policy.ValidateRelativePath(relativePath)
	if err != nil {
		return "", err
	}
	candidate := project.Root
	if clean != "." {
		candidate = joinRemotePath(project.Root, clean, target.Platform)
	}
	transport, err := remote.Required(c.remote)
	if err != nil {
		return "", err
	}
	canonical, err := transport.ResolveCanonicalPath(ctx, target, candidate, 10*time.Second)
	if err != nil {
		return "", err
	}
	if err := policy.ValidateCanonicalPath(canonical, project.Root, target.Platform); err != nil {
		return "", err
	}
	return canonical, nil
}

func joinRemotePath(root, relativePath, platformName string) string {
	if relativePath == "." || relativePath == "" {
		return root
	}
	if strings.EqualFold(strings.TrimSpace(platformName), "windows") {
		root = strings.ReplaceAll(root, "\\", "/")
		joined := path.Join(root, relativePath)
		return strings.ReplaceAll(joined, "/", "\\")
	}
	return path.Join(root, relativePath)
}

func corePolicyError(tool string, err error, requestID, targetID, projectID string, started time.Time) Response {
	code := policy.ValidationErrorCode(err)
	if code == "" {
		code = registry.ErrorCode(err)
	}
	if code == "" {
		code = remote.ErrorCode(err)
	}
	if code == "" {
		code = "INTERNAL_ERROR"
	}
	return errorResponse(tool, code, err.Error(), requestID, targetID, projectID, started)
}

func remoteCodeOr(err error, fallback string) string {
	if code := remote.ErrorCode(err); code != "" {
		return code
	}
	return fallback
}

func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func (c *Core) intSetting(ctx context.Context, key string, fallback int) (int, error) {
	raw, err := c.store.GetSetting(ctx, key, strconv.Itoa(fallback))
	if err != nil {
		return 0, err
	}
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("invalid setting %s=%q", key, raw)
	}
	return value, nil
}
