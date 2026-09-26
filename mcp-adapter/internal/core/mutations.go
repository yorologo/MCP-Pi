package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"mcp-gateway-adapter/internal/policy"
	"mcp-gateway-adapter/internal/registry"
	"mcp-gateway-adapter/internal/remote"
)

type writeRequest struct {
	Path           string
	Content        string
	ExpectedSHA256 string
	DryRun         bool
	Create         bool
}

func (c *Core) WriteFile(
	ctx context.Context,
	requestID, actor, targetID, projectID string,
	req writeRequest,
) (response Response) {
	started := time.Now()
	defer func() {
		if response.OK || response.Error == nil {
			return
		}
		c.auditMutationBestEffort(
			ctx, actor, requestID, "DENY", targetID, projectID,
			false, response.Error.Code, 0,
			map[string]any{"path": req.Path, "tool": "write_file"},
			response.DurationMS,
		)
	}()
	target, project, transport, blocked := c.mutationContext(
		ctx, "write_file", requestID, targetID, projectID, true, started,
	)
	if blocked != nil {
		return *blocked
	}

	content, err := policy.ValidateContentUTF8(req.Content)
	if err != nil {
		return corePolicyError("write_file", err, requestID, targetID, projectID, started)
	}
	maxWriteBytes, err := c.intSetting(ctx, "max_write_bytes", 262144)
	if err != nil {
		return errorResponse("write_file", "INTERNAL_ERROR", err.Error(), requestID, targetID, projectID, started)
	}
	if err := policy.ValidateWriteSize(content, maxWriteBytes); err != nil {
		return corePolicyError("write_file", err, requestID, targetID, projectID, started)
	}

	normPath, err := policy.ValidateWriteRelativePath(req.Path)
	if err != nil {
		return corePolicyError("write_file", err, requestID, targetID, projectID, started)
	}
	rootCanonical, err := c.canonicalProjectRoot(ctx, transport, target, project)
	if err != nil {
		return corePolicyError("write_file", err, requestID, targetID, projectID, started)
	}
	candidate := joinRemotePath(rootCanonical, normPath, target.Platform)
	safe, err := transport.ResolveSafeDestination(
		ctx, target, rootCanonical, candidate, false, 10*time.Second,
	)
	if err != nil {
		return corePolicyError("write_file", err, requestID, targetID, projectID, started)
	}
	probe, err := transport.ProbePath(ctx, target, safe.DestinationPath, 10*time.Second)
	if err != nil {
		return corePolicyError("write_file", err, requestID, targetID, projectID, started)
	}
	if probe.IsSymlink {
		return errorResponse(
			"write_file",
			"SYMLINK_WRITE_DENIED",
			fmt.Sprintf("Target path is a symlink: %s", req.Path),
			requestID, targetID, projectID, started,
		)
	}
	if !probe.ParentExists {
		return errorResponse(
			"write_file",
			"NOT_FOUND",
			fmt.Sprintf("Parent directory does not exist for path: %s", req.Path),
			requestID, targetID, projectID, started,
		)
	}
	parentCanonical := probe.ParentCanonicalPath
	if parentCanonical == "" {
		parentCanonical = safe.ParentCanonical
	}
	if err := policy.ValidateCanonicalPath(parentCanonical, rootCanonical, target.Platform); err != nil {
		return corePolicyError("write_file", err, requestID, targetID, projectID, started)
	}

	if req.Create {
		if probe.Exists {
			return errorResponse(
				"write_file", "FILE_ALREADY_EXISTS",
				fmt.Sprintf("File already exists: %s", req.Path),
				requestID, targetID, projectID, started,
			)
		}
	} else {
		if !probe.Exists {
			return errorResponse(
				"write_file", "NOT_FOUND",
				fmt.Sprintf("File not found: %s", req.Path),
				requestID, targetID, projectID, started,
			)
		}
		if probe.IsDir {
			return errorResponse(
				"write_file", "INVALID_PATH",
				fmt.Sprintf("Target path is a directory: %s", req.Path),
				requestID, targetID, projectID, started,
			)
		}
		if err := policy.ValidateCanonicalPath(probe.CanonicalPath, rootCanonical, target.Platform); err != nil {
			return corePolicyError("write_file", err, requestID, targetID, projectID, started)
		}
		if strings.TrimSpace(req.ExpectedSHA256) == "" {
			return errorResponse(
				"write_file", "WRITE_CONFLICT",
				"expected_sha256 is required when overwriting an existing file",
				requestID, targetID, projectID, started,
			)
		}
		if !strings.EqualFold(probe.SHA256, req.ExpectedSHA256) {
			return errorResponse(
				"write_file", "WRITE_CONFLICT",
				fmt.Sprintf("Hash mismatch: expected %s, found %s", req.ExpectedSHA256, probe.SHA256),
				requestID, targetID, projectID, started,
			)
		}
	}

	sum := sha256.Sum256(content)
	proposedSHA := hex.EncodeToString(sum[:])
	if req.DryRun {
		current := ""
		var currentSHA any
		sizeBefore := int64(0)
		if probe.Exists {
			current = probe.Content
			currentSHA = probe.SHA256
			sizeBefore = probe.Size
		}
		diff := unifiedDiff(req.Path, current, req.Content)
		maxDiffBytes, err := c.intSetting(ctx, "max_diff_bytes", 65536)
		if err != nil {
			return errorResponse("write_file", "INTERNAL_ERROR", err.Error(), requestID, targetID, projectID, started)
		}
		diff, truncated := truncateUTF8(diff, maxDiffBytes)
		response = successResponse("write_file", map[string]any{
			"path":            req.Path,
			"current_sha256":  currentSHA,
			"proposed_sha256": proposedSHA,
			"size_before":     sizeBefore,
			"size_after":      len(content),
			"unified_diff":    diff,
			"diff":            diff,
			"diff_truncated":  truncated,
			"dry_run":         true,
		}, requestID, targetID, projectID, started)
		c.auditMutationBestEffort(
			ctx, actor, requestID, "DRY_RUN", targetID, projectID,
			true, "", int64(len(content)), map[string]any{"path": req.Path, "dry_run": true},
			response.DurationMS,
		)
		return response
	}

	var backupPath any
	if !req.Create && probe.Exists {
		if path, err := c.backupExistingMutation(targetID, projectID, normPath, probe); err == nil && path != "" {
			backupPath = path
		}
	}
	if err := c.auditMutationRequired(
		ctx, actor, requestID, "WRITE_ATTEMPT", targetID, projectID,
		map[string]any{"path": req.Path, "create": req.Create},
		durationMS(started),
	); err != nil {
		return errorResponse("write_file", "AUDIT_UNAVAILABLE", "Audit sink is unavailable for a critical operation", requestID, targetID, projectID, started)
	}

	written, err := transport.WriteFileAtomic(
		ctx, target, safe.DestinationPath, content, req.Create,
		req.ExpectedSHA256, maxWriteBytes, 20*time.Second,
	)
	if err != nil {
		return corePolicyError("write_file", err, requestID, targetID, projectID, started)
	}
	result := map[string]any{
		"path":          req.Path,
		"created":       written.Created,
		"old_sha256":    nullableString(written.OldSHA256),
		"new_sha256":    written.NewSHA256,
		"sha256":        written.NewSHA256,
		"bytes_written": written.BytesWritten,
		"backup_path":   backupPath,
		"atomic":        written.Atomic,
	}
	response = successResponse("write_file", result, requestID, targetID, projectID, started)
	c.auditMutationBestEffort(
		ctx, actor, requestID, "WRITE", targetID, projectID,
		true, "", int64(len(content)), result, response.DurationMS,
	)
	return response
}

func (c *Core) AppendFile(
	ctx context.Context,
	requestID, actor, targetID, projectID, relativePath, contentText string,
) Response {
	started := time.Now()
	target, project, transport, blocked := c.mutationContext(
		ctx, "append_file", requestID, targetID, projectID, false, started,
	)
	if blocked != nil {
		return *blocked
	}

	content, err := policy.ValidateContentUTF8(contentText)
	if err != nil {
		return corePolicyError("append_file", err, requestID, targetID, projectID, started)
	}
	maxWriteBytes, err := c.intSetting(ctx, "max_write_bytes", 262144)
	if err != nil {
		return errorResponse("append_file", "INTERNAL_ERROR", err.Error(), requestID, targetID, projectID, started)
	}
	if err := policy.ValidateWriteSize(content, maxWriteBytes); err != nil {
		return corePolicyError("append_file", err, requestID, targetID, projectID, started)
	}
	normPath, err := policy.ValidateWriteRelativePath(relativePath)
	if err != nil {
		return corePolicyError("append_file", err, requestID, targetID, projectID, started)
	}
	rootCanonical, safe, probe, err := c.prepareMutationDestination(
		ctx, transport, target, project, normPath, false,
	)
	if err != nil {
		return corePolicyError("append_file", err, requestID, targetID, projectID, started)
	}
	if !probe.Exists {
		return errorResponse("append_file", "NOT_FOUND", fmt.Sprintf("File not found: %s", relativePath), requestID, targetID, projectID, started)
	}
	if probe.IsDir || probe.IsSymlink {
		return errorResponse("append_file", "INVALID_PATH", fmt.Sprintf("Cannot append to directory or symlink: %s", relativePath), requestID, targetID, projectID, started)
	}
	destCanonical := probe.CanonicalPath
	if destCanonical == "" {
		destCanonical = safe.DestinationPath
	}
	if err := policy.ValidateCanonicalPath(destCanonical, rootCanonical, target.Platform); err != nil {
		return corePolicyError("append_file", err, requestID, targetID, projectID, started)
	}

	if err := c.auditMutationRequired(
		ctx, actor, requestID, "APPEND_FILE_ATTEMPT", targetID, projectID,
		map[string]any{"path": normPath, "bytes": len(content)}, durationMS(started),
	); err != nil {
		return errorResponse("append_file", "AUDIT_UNAVAILABLE", "Audit sink is unavailable for a critical operation", requestID, targetID, projectID, started)
	}
	script := `import sys, hashlib
p = sys.argv[1]
data = sys.stdin.buffer.read()
open(p, 'ab').write(data)
print(hashlib.sha256(open(p, 'rb').read()).hexdigest())
`
	run, err := transport.RunPython(ctx, target, script, []string{destCanonical}, content, 15*time.Second)
	if err != nil {
		return corePolicyError("append_file", err, requestID, targetID, projectID, started)
	}
	if !run.OK() {
		return errorResponse("append_file", "WRITE_FAILED", "Append failed: "+run.Stderr, requestID, targetID, projectID, started)
	}
	result := map[string]any{
		"path":           normPath,
		"bytes_appended": len(content),
		"new_sha256":     strings.TrimSpace(run.Stdout),
	}
	response := successResponse("append_file", result, requestID, targetID, projectID, started)
	c.auditMutationBestEffort(ctx, actor, requestID, "APPEND_FILE", targetID, projectID, true, "", 0, map[string]any{"path": normPath, "bytes": len(content)}, response.DurationMS)
	return response
}

func (c *Core) DeleteFile(
	ctx context.Context,
	requestID, actor, targetID, projectID, relativePath string,
) Response {
	started := time.Now()
	target, project, transport, blocked := c.mutationContext(
		ctx, "delete_file", requestID, targetID, projectID, false, started,
	)
	if blocked != nil {
		return *blocked
	}
	normPath, err := policy.ValidateWriteRelativePath(relativePath)
	if err != nil {
		return corePolicyError("delete_file", err, requestID, targetID, projectID, started)
	}
	rootCanonical, safe, probe, err := c.prepareMutationDestination(ctx, transport, target, project, normPath, false)
	if err != nil {
		return corePolicyError("delete_file", err, requestID, targetID, projectID, started)
	}
	if safe.DestinationPath == rootCanonical {
		return errorResponse("delete_file", "INVALID_PATH", "Deleting project root is forbidden", requestID, targetID, projectID, started)
	}
	if probe.IsSymlink {
		return errorResponse("delete_file", "SYMLINK_WRITE_DENIED", fmt.Sprintf("Deleting symlink paths is denied: %s", relativePath), requestID, targetID, projectID, started)
	}
	if !probe.Exists {
		return errorResponse("delete_file", "NOT_FOUND", fmt.Sprintf("File not found: %s", relativePath), requestID, targetID, projectID, started)
	}
	destCanonical := probe.CanonicalPath
	if destCanonical == "" {
		destCanonical = safe.DestinationPath
	}
	if err := policy.ValidateCanonicalPath(destCanonical, rootCanonical, target.Platform); err != nil {
		return corePolicyError("delete_file", err, requestID, targetID, projectID, started)
	}

	if err := c.auditMutationRequired(ctx, actor, requestID, "DELETE_FILE_ATTEMPT", targetID, projectID, map[string]any{"path": normPath}, durationMS(started)); err != nil {
		return errorResponse("delete_file", "AUDIT_UNAVAILABLE", "Audit sink is unavailable for a critical operation", requestID, targetID, projectID, started)
	}
	script := `import os, sys
p = sys.argv[1]
os.remove(p) if os.path.isfile(p) or os.path.islink(p) else os.rmdir(p)
`
	run, err := transport.RunPython(ctx, target, script, []string{destCanonical}, nil, 15*time.Second)
	if err != nil {
		return corePolicyError("delete_file", err, requestID, targetID, projectID, started)
	}
	if !run.OK() {
		return errorResponse("delete_file", "DELETE_FAILED", "Delete failed: "+run.Stderr, requestID, targetID, projectID, started)
	}
	result := map[string]any{"path": normPath, "deleted": true}
	response := successResponse("delete_file", result, requestID, targetID, projectID, started)
	c.auditMutationBestEffort(ctx, actor, requestID, "DELETE_FILE", targetID, projectID, true, "", 0, result, response.DurationMS)
	return response
}

func (c *Core) CopyFile(
	ctx context.Context,
	requestID, actor, targetID, projectID, sourcePath, destPath string,
) Response {
	return c.copyOrMoveFile(ctx, requestID, actor, "copy_file", targetID, projectID, sourcePath, destPath, false)
}

func (c *Core) MoveFile(
	ctx context.Context,
	requestID, actor, targetID, projectID, sourcePath, destPath string,
) Response {
	return c.copyOrMoveFile(ctx, requestID, actor, "move_file", targetID, projectID, sourcePath, destPath, true)
}

func (c *Core) copyOrMoveFile(
	ctx context.Context,
	requestID, actor, tool, targetID, projectID, sourcePath, destPath string,
	move bool,
) Response {
	started := time.Now()
	target, project, transport, blocked := c.mutationContext(
		ctx, tool, requestID, targetID, projectID, false, started,
	)
	if blocked != nil {
		return *blocked
	}
	normSource, err := policy.ValidateWriteRelativePath(sourcePath)
	if err != nil {
		return corePolicyError(tool, err, requestID, targetID, projectID, started)
	}
	normDest, err := policy.ValidateWriteRelativePath(destPath)
	if err != nil {
		return corePolicyError(tool, err, requestID, targetID, projectID, started)
	}
	rootCanonical, err := c.canonicalProjectRoot(ctx, transport, target, project)
	if err != nil {
		return corePolicyError(tool, err, requestID, targetID, projectID, started)
	}
	sourceFull := joinRemotePath(rootCanonical, normSource, target.Platform)
	destFull := joinRemotePath(rootCanonical, normDest, target.Platform)
	sourceProbe, err := transport.ProbePath(ctx, target, sourceFull, 10*time.Second)
	if err != nil {
		return corePolicyError(tool, err, requestID, targetID, projectID, started)
	}
	if !sourceProbe.Exists {
		return errorResponse(tool, "NOT_FOUND", fmt.Sprintf("Source file not found: %s", sourcePath), requestID, targetID, projectID, started)
	}
	sourceCanonical := sourceProbe.CanonicalPath
	if sourceCanonical == "" {
		sourceCanonical = sourceFull
	}
	if err := policy.ValidateCanonicalPath(sourceCanonical, rootCanonical, target.Platform); err != nil {
		return corePolicyError(tool, err, requestID, targetID, projectID, started)
	}
	if sourceProbe.IsSymlink {
		action := "copy"
		if move {
			action = "move"
		}
		return errorResponse(tool, "SYMLINK_WRITE_DENIED", fmt.Sprintf("Source symlink is not accepted for structured %s: %s", action, sourcePath), requestID, targetID, projectID, started)
	}
	safe, err := transport.ResolveSafeDestination(ctx, target, rootCanonical, destFull, false, 10*time.Second)
	if err != nil {
		return corePolicyError(tool, err, requestID, targetID, projectID, started)
	}

	attempt := "COPY_FILE_ATTEMPT"
	action := "COPY_FILE"
	if move {
		attempt = "MOVE_FILE_ATTEMPT"
		action = "MOVE_FILE"
	}
	detail := map[string]any{"source": normSource, "dest": normDest}
	if err := c.auditMutationRequired(ctx, actor, requestID, attempt, targetID, projectID, detail, durationMS(started)); err != nil {
		return errorResponse(tool, "AUDIT_UNAVAILABLE", "Audit sink is unavailable for a critical operation", requestID, targetID, projectID, started)
	}

	script := `import shutil, sys
source = sys.argv[1]
dest = sys.argv[2]
shutil.move(source, dest) if sys.argv[3] == '1' else shutil.copy2(source, dest)
`
	moveFlag := "0"
	if move {
		moveFlag = "1"
	}
	run, err := transport.RunPython(ctx, target, script, []string{sourceCanonical, safe.DestinationPath, moveFlag}, nil, 15*time.Second)
	if err != nil {
		return corePolicyError(tool, err, requestID, targetID, projectID, started)
	}
	if !run.OK() {
		label := "Copy"
		if move {
			label = "Move"
		}
		return errorResponse(tool, "WRITE_FAILED", label+" failed: "+run.Stderr, requestID, targetID, projectID, started)
	}

	result := map[string]any{"source": normSource, "dest": normDest}
	if move {
		result["moved"] = true
	} else {
		result["copied"] = true
	}
	response := successResponse(tool, result, requestID, targetID, projectID, started)
	c.auditMutationBestEffort(ctx, actor, requestID, action, targetID, projectID, true, "", 0, detail, response.DurationMS)
	return response
}

func (c *Core) Mkdir(
	ctx context.Context,
	requestID, actor, targetID, projectID, relativePath string,
	parents bool,
) Response {
	started := time.Now()
	target, project, transport, blocked := c.mutationContext(
		ctx, "mkdir", requestID, targetID, projectID, false, started,
	)
	if blocked != nil {
		return *blocked
	}
	normPath, err := policy.ValidateWriteRelativePath(relativePath)
	if err != nil {
		return corePolicyError("mkdir", err, requestID, targetID, projectID, started)
	}
	rootCanonical, err := c.canonicalProjectRoot(ctx, transport, target, project)
	if err != nil {
		return corePolicyError("mkdir", err, requestID, targetID, projectID, started)
	}
	candidate := joinRemotePath(rootCanonical, normPath, target.Platform)
	safe, err := transport.ResolveSafeDestination(ctx, target, rootCanonical, candidate, parents, 10*time.Second)
	if err != nil {
		return corePolicyError("mkdir", err, requestID, targetID, projectID, started)
	}
	if err := policy.ValidateCanonicalPath(safe.DestinationPath, rootCanonical, target.Platform); err != nil {
		return corePolicyError("mkdir", err, requestID, targetID, projectID, started)
	}

	detail := map[string]any{"path": normPath, "parents": parents}
	if err := c.auditMutationRequired(ctx, actor, requestID, "MKDIR_ATTEMPT", targetID, projectID, detail, durationMS(started)); err != nil {
		return errorResponse("mkdir", "AUDIT_UNAVAILABLE", "Audit sink is unavailable for a critical operation", requestID, targetID, projectID, started)
	}
	script := `import os, sys
os.makedirs(sys.argv[1], exist_ok=(sys.argv[2] == '1'))
`
	parentsFlag := "0"
	if parents {
		parentsFlag = "1"
	}
	run, err := transport.RunPython(ctx, target, script, []string{safe.DestinationPath, parentsFlag}, nil, 15*time.Second)
	if err != nil {
		return corePolicyError("mkdir", err, requestID, targetID, projectID, started)
	}
	if !run.OK() {
		return errorResponse("mkdir", "WRITE_FAILED", "mkdir failed: "+run.Stderr, requestID, targetID, projectID, started)
	}
	result := map[string]any{"path": normPath, "created": true}
	response := successResponse("mkdir", result, requestID, targetID, projectID, started)
	c.auditMutationBestEffort(ctx, actor, requestID, "MKDIR", targetID, projectID, true, "", 0, map[string]any{"path": normPath}, response.DurationMS)
	return response
}

func (c *Core) mutationContext(
	ctx context.Context,
	tool, requestID, targetID, projectID string,
	writeFileMessage bool,
	started time.Time,
) (registry.Target, registry.Project, remote.Transport, *Response) {
	gatewayEnabled, err := c.boolSetting(ctx, "gateway_enabled", true)
	if err != nil {
		response := errorResponse(tool, "INTERNAL_ERROR", err.Error(), requestID, targetID, projectID, started)
		return registry.Target{}, registry.Project{}, nil, &response
	}
	if !gatewayEnabled {
		response := errorResponse(tool, "GATEWAY_DISABLED", "Gateway operations are disabled by administrator kill switch", requestID, targetID, projectID, started)
		return registry.Target{}, registry.Project{}, nil, &response
	}
	writesEnabled, err := c.boolSetting(ctx, "writes_enabled", false)
	if err != nil {
		response := errorResponse(tool, "INTERNAL_ERROR", err.Error(), requestID, targetID, projectID, started)
		return registry.Target{}, registry.Project{}, nil, &response
	}
	if !writesEnabled {
		message := "Controlled writes are disabled"
		if writeFileMessage {
			message = "Controlled writes are disabled by administrator setting"
		}
		response := errorResponse(tool, "WRITES_DISABLED", message, requestID, targetID, projectID, started)
		return registry.Target{}, registry.Project{}, nil, &response
	}

	target, project, err := c.projectContext(ctx, targetID, projectID, false)
	if err != nil {
		response := corePolicyError(tool, err, requestID, targetID, projectID, started)
		return registry.Target{}, registry.Project{}, nil, &response
	}
	read := project.Read
	write := project.Write
	if err := policy.CheckCapability(policy.CapabilityState{Read: &read, Write: &write}, "write"); err != nil {
		response := corePolicyError(tool, err, requestID, targetID, projectID, started)
		return registry.Target{}, registry.Project{}, nil, &response
	}
	transport, err := remote.Required(c.remote)
	if err != nil {
		response := errorResponse(tool, "INTERNAL_ERROR", err.Error(), requestID, targetID, projectID, started)
		return registry.Target{}, registry.Project{}, nil, &response
	}
	return target, project, transport, nil
}

func (c *Core) canonicalProjectRoot(
	ctx context.Context,
	transport remote.Transport,
	target registry.Target,
	project registry.Project,
) (string, error) {
	canonical, err := transport.ResolveCanonicalPath(ctx, target, project.Root, 10*time.Second)
	if err != nil {
		return "", err
	}
	if err := policy.ValidateCanonicalPath(canonical, project.Root, target.Platform); err != nil {
		return "", err
	}
	return canonical, nil
}

func (c *Core) prepareMutationDestination(
	ctx context.Context,
	transport remote.Transport,
	target registry.Target,
	project registry.Project,
	normPath string,
	allowMissingParents bool,
) (string, remote.SafeDestination, remote.PathProbe, error) {
	rootCanonical, err := c.canonicalProjectRoot(ctx, transport, target, project)
	if err != nil {
		return "", remote.SafeDestination{}, remote.PathProbe{}, err
	}
	candidate := joinRemotePath(rootCanonical, normPath, target.Platform)
	safe, err := transport.ResolveSafeDestination(
		ctx, target, rootCanonical, candidate, allowMissingParents, 10*time.Second,
	)
	if err != nil {
		return "", remote.SafeDestination{}, remote.PathProbe{}, err
	}
	probe, err := transport.ProbePath(ctx, target, safe.DestinationPath, 10*time.Second)
	if err != nil {
		return "", remote.SafeDestination{}, remote.PathProbe{}, err
	}
	return rootCanonical, safe, probe, nil
}

func (c *Core) auditMutationRequired(
	ctx context.Context,
	actor, requestID, action, targetID, projectID string,
	detail map[string]any,
	duration int64,
) error {
	if requestID != "" {
		detail = cloneDetail(detail)
		detail["request_id"] = requestID
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	target := targetID
	project := projectID
	if err := c.store.RecordActivity(ctx, registry.Activity{
		Actor:      nonEmpty(actor, "mcp-local"),
		Action:     action,
		TargetID:   &target,
		ProjectID:  &project,
		DurationMS: &duration,
		Success:    true,
		Detail:     string(raw),
	}); err != nil {
		return err
	}
	return nil
}

func (c *Core) auditMutationBestEffort(
	ctx context.Context,
	actor, requestID, action, targetID, projectID string,
	success bool,
	errorCode string,
	bytesTransferred int64,
	detail map[string]any,
	duration int64,
) {
	if requestID != "" {
		detail = cloneDetail(detail)
		detail["request_id"] = requestID
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		return
	}
	target := targetID
	project := projectID
	var code *string
	if errorCode != "" {
		value := errorCode
		code = &value
	}
	var bytes *int64
	if bytesTransferred > 0 {
		value := bytesTransferred
		bytes = &value
	}
	_ = c.store.RecordActivity(ctx, registry.Activity{
		Actor:            nonEmpty(actor, "mcp-local"),
		Action:           action,
		TargetID:         &target,
		ProjectID:        &project,
		DurationMS:       &duration,
		Success:          success,
		ErrorCode:        code,
		BytesTransferred: bytes,
		Detail:           string(raw),
	})
}

func cloneDetail(detail map[string]any) map[string]any {
	out := make(map[string]any, len(detail)+1)
	for key, value := range detail {
		out[key] = value
	}
	return out
}

func (c *Core) backupExistingMutation(
	targetID, projectID, normPath string,
	probe remote.PathProbe,
) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	backupDir := filepath.Join(
		home, ".local", "share", "mcp-gateway", "backups",
		targetID, projectID, filepath.FromSlash(normPath),
	)
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		return "", err
	}
	name := fmt.Sprintf(
		"%s_%s.bak",
		time.Now().Format("20060102_150405"),
		prefix(probe.SHA256, 12),
	)
	backupPath := filepath.Join(backupDir, name)
	if err := os.WriteFile(backupPath, []byte(probe.Content), 0600); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return backupPath, nil
	}
	var backups []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".bak") {
			backups = append(backups, filepath.Join(backupDir, entry.Name()))
		}
	}
	sort.Strings(backups)
	if len(backups) > 5 {
		for _, old := range backups[:len(backups)-5] {
			_ = os.Remove(old)
		}
	}
	return backupPath, nil
}

func unifiedDiff(name, before, after string) string {
	if before == after {
		return ""
	}
	oldLines := splitKeepEnds(before)
	newLines := splitKeepEnds(after)
	var builder strings.Builder
	fmt.Fprintf(&builder, "--- a/%s\n+++ b/%s\n", name, name)
	fmt.Fprintf(
		&builder,
		"@@ -%s +%s @@\n",
		unifiedRange(1, len(oldLines), len(oldLines) == 0),
		unifiedRange(1, len(newLines), len(newLines) == 0),
	)
	for _, line := range oldLines {
		builder.WriteByte('-')
		builder.WriteString(line)
		if !strings.HasSuffix(line, "\n") {
			builder.WriteByte('\n')
		}
	}
	for _, line := range newLines {
		builder.WriteByte('+')
		builder.WriteString(line)
		if !strings.HasSuffix(line, "\n") {
			builder.WriteByte('\n')
		}
	}
	return builder.String()
}

func unifiedRange(start, count int, empty bool) string {
	if empty {
		return "0,0"
	}
	if count == 1 {
		return fmt.Sprintf("%d", start)
	}
	return fmt.Sprintf("%d,%d", start, count)
}

func splitKeepEnds(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.SplitAfter(value, "\n")
	if parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

func truncateUTF8(value string, maxBytes int) (string, bool) {
	raw := []byte(value)
	if len(raw) <= maxBytes {
		return value, false
	}
	return strings.ToValidUTF8(string(raw[:maxBytes]), "�"), true
}

func nullableString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func prefix(value string, n int) string {
	if len(value) <= n {
		return value
	}
	return value[:n]
}
