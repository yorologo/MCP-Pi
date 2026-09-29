package core

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"mcp-gateway-adapter/internal/policy"
	"mcp-gateway-adapter/internal/registry"
	"mcp-gateway-adapter/internal/remote"
)

type CommandRequest struct {
	CWD       string
	Env       map[string]string
	Timeout   int
	Stdin     string
	Privilege string
}

type executionPrivilegeState struct {
	PolicyName               string
	CurrentLevel             string
	MaximumLevel             string
	Backend                  string
	IndependentElevator      string
	BackendReady             bool
	TransportAlreadyElevated bool
	ShellCanElevate          bool
	Guarded                  bool
	Effective                bool
}

type executionPrivilegeError struct {
	Code    string
	Message string
}

func (c *Core) authorizeExecutionPrivilege(
	ctx context.Context,
	actor, targetID, projectID, privilegeRequest string,
	target registry.Target,
	transport remote.Transport,
	consumeApproval bool,
) (executionPrivilegeState, *executionPrivilegeError) {
	state := executionPrivilegeState{
		PolicyName: strings.ToLower(strings.TrimSpace(target.PrivilegePolicy)),
	}
	if state.PolicyName == "" {
		state.PolicyName = "never"
	}

	includeBootID := state.PolicyName == "ask_once_per_boot"
	facts, err := transport.ProbeFacts(ctx, target, includeBootID, privilegeRequest == "required", 10*time.Second)
	if err != nil || facts == nil || facts["probe_status"] != "ok" {
		reason := "Target privilege probe failed"
		if facts != nil {
			if r, ok := facts["reason"].(string); ok && r != "" {
				reason = r
			}
		}
		if err != nil {
			reason = fmt.Sprintf("Target privilege probe failed: %v", err)
		}
		return state, &executionPrivilegeError{Code: "PRIVILEGE_STATUS_UNAVAILABLE", Message: reason}
	}

	privilegeInfo, ok := facts["privilege"].(map[string]any)
	if !ok || privilegeInfo == nil {
		return state, &executionPrivilegeError{
			Code:    "PRIVILEGE_STATUS_UNAVAILABLE",
			Message: "Target privilege status is unavailable",
		}
	}

	state.CurrentLevel, _ = privilegeInfo["current_level"].(string)
	state.CurrentLevel = strings.ToLower(strings.TrimSpace(state.CurrentLevel))
	if state.CurrentLevel == "" {
		return state, &executionPrivilegeError{
			Code:    "PRIVILEGE_STATUS_UNAVAILABLE",
			Message: "Target privilege level is unavailable",
		}
	}
	state.MaximumLevel, _ = privilegeInfo["maximum_level"].(string)
	state.Backend, _ = privilegeInfo["backend"].(string)
	state.BackendReady, _ = privilegeInfo["backend_ready"].(bool)
	state.ShellCanElevate, _ = privilegeInfo["shell_can_elevate"].(bool)
	state.IndependentElevator, _ = privilegeInfo["independent_elevator"].(string)
	state.TransportAlreadyElevated = state.CurrentLevel == "root" || state.CurrentLevel == "administrator"

	if privilegeRequest != "required" && !state.TransportAlreadyElevated {
		return state, nil
	}

	state.Guarded = true
	requireBackend := privilegeRequest == "required" || state.TransportAlreadyElevated

	decision, err := policy.AuthorizePrivilegeRequest(ctx, c.store, actor, targetID, projectID)
	if err != nil || !decision.Allowed {
		reason := decision.Reason
		if reason == "" {
			reason = fmt.Sprintf("Client '%s' requires an explicit target_admin grant for %s/%s", actor, targetID, projectID)
		}
		return state, &executionPrivilegeError{Code: "PRIVILEGE_GRANT_REQUIRED", Message: reason}
	}

	if state.PolicyName == "never" {
		return state, &executionPrivilegeError{
			Code:    "PRIVILEGE_DISABLED",
			Message: "Target privilege policy denies elevation",
		}
	}

	if requireBackend && !state.BackendReady && !state.TransportAlreadyElevated {
		return state, &executionPrivilegeError{
			Code:    "PRIVILEGE_SETUP_REQUIRED",
			Message: "No verified privileged backend is ready on this Target",
		}
	}

	if state.PolicyName != "always_allow" {
		bootID, _ := facts["boot_id"].(string)
		if state.PolicyName == "ask_once_per_boot" && strings.TrimSpace(bootID) == "" {
			return state, &executionPrivilegeError{
				Code:    "PRIVILEGE_BOOT_ID_UNAVAILABLE",
				Message: "Target boot identity is unavailable; per-boot approval fails closed",
			}
		}

		var approved bool
		var err error
		if consumeApproval {
			approved, err = c.store.ConsumePrivilegeApproval(ctx, targetID, state.PolicyName, actor, projectID, bootID, 300)
		} else {
			approved, err = c.store.CheckPrivilegeApproval(ctx, targetID, state.PolicyName, actor, projectID, bootID, 300)
		}
		if err != nil || !approved {
			return state, &executionPrivilegeError{
				Code:    "PRIVILEGE_APPROVAL_REQUIRED",
				Message: "Human approval is required by the Target privilege policy",
			}
		}
	}

	state.Effective = privilegeRequest == "required" || state.TransportAlreadyElevated
	return state, nil
}

type ExecutionAccessCheck struct {
	Allowed   bool
	Code      string
	Reason    string
	Privilege map[string]any
}

func (c *Core) CheckRunCommandAccess(
	ctx context.Context,
	actor, targetID, projectID, privilegeRequest string,
) (ExecutionAccessCheck, error) {
	normalizedPrivilege, err := policy.NormalizePrivilegeRequest(privilegeRequest)
	if err != nil {
		return ExecutionAccessCheck{
			Code:   "INVALID_ARGUMENTS",
			Reason: err.Error(),
		}, nil
	}
	privilegeRequest = normalizedPrivilege
	if strings.TrimSpace(targetID) == "" || strings.TrimSpace(projectID) == "" {
		return ExecutionAccessCheck{
			Code:   "CONCRETE_SCOPE_REQUIRED",
			Reason: "run_command access requires a specific Target and Project to evaluate execution privilege safely",
		}, nil
	}

	ordinary, err := policy.AuthorizeClient(ctx, c.store, actor, targetID, projectID, "run_command", false)
	if err != nil {
		return ExecutionAccessCheck{}, err
	}
	if !ordinary.Allowed {
		return ExecutionAccessCheck{Code: ordinary.Code, Reason: ordinary.Reason}, nil
	}

	target, err := c.store.GetTarget(ctx, targetID, false)
	if err != nil {
		return ExecutionAccessCheck{}, err
	}
	transport, err := remote.Required(c.remote)
	if err != nil {
		return ExecutionAccessCheck{}, err
	}

	state, privilegeErr := c.authorizeExecutionPrivilege(
		ctx, actor, targetID, projectID, privilegeRequest, target, transport, false,
	)
	privilege := map[string]any{
		"requested":            privilegeRequest,
		"guarded":              state.Guarded,
		"effective":            state.Effective,
		"policy":               state.PolicyName,
		"current_level":        state.CurrentLevel,
		"maximum_level":        state.MaximumLevel,
		"backend":              state.Backend,
		"backend_ready":        state.BackendReady,
		"shell_can_elevate":    state.ShellCanElevate,
		"independent_elevator": state.IndependentElevator,
	}
	if privilegeErr != nil {
		return ExecutionAccessCheck{
			Code:      privilegeErr.Code,
			Reason:    privilegeErr.Message,
			Privilege: privilege,
		}, nil
	}

	if state.Effective && privilegeRequest == "required" {
		switch {
		case state.Backend == "privileged-ssh" && strings.TrimSpace(target.PrivilegeUser) == "":
			return ExecutionAccessCheck{
				Code:      "PRIVILEGE_SETUP_REQUIRED",
				Reason:    "Privileged SSH user is not configured",
				Privilege: privilege,
			}, nil
		case state.Backend != "privileged-ssh" && state.Backend != "shizuku" && !state.TransportAlreadyElevated:
			return ExecutionAccessCheck{
				Code:      "PRIVILEGE_SETUP_REQUIRED",
				Reason:    fmt.Sprintf("Privilege backend %q cannot execute this request safely", state.Backend),
				Privilege: privilege,
			}, nil
		}
	}

	return ExecutionAccessCheck{
		Allowed:   true,
		Privilege: privilege,
	}, nil
}

func (c *Core) RunCommand(
	ctx context.Context,
	requestID, actor, targetID, projectID, command string,
	req CommandRequest,
) Response {
	started := time.Now()
	if strings.TrimSpace(command) == "" {
		return errorResponse("run_command", "INVALID_ARGUMENTS", "Command must be a non-empty string", requestID, targetID, projectID, started)
	}

	target, err := c.store.GetTarget(ctx, targetID, false)
	if err != nil {
		if registry.ErrorCode(err) == "TARGET_DISABLED" {
			return errorResponse("run_command", "TARGET_DISABLED", fmt.Sprintf("Target '%s' is disabled", targetID), requestID, targetID, projectID, started)
		}
		return errorResponse("run_command", "TARGET_NOT_FOUND", fmt.Sprintf("Target '%s' is not configured", targetID), requestID, targetID, projectID, started)
	}

	if strings.TrimSpace(projectID) == "" {
		return errorResponse("run_command", "INVALID_ARGUMENTS", "Trusted target shell requires an explicit project scope for authorization and audit", requestID, targetID, projectID, started)
	}

	if blocked := c.operationalGate(ctx, "run_command", requestID, targetID, projectID); blocked != nil {
		return responseFromMap(blocked)
	}

	shellEnabled, err := c.boolSetting(ctx, "shell_enabled", false)
	if err != nil {
		return errorResponse("run_command", "INTERNAL_ERROR", err.Error(), requestID, targetID, projectID, started)
	}
	if !shellEnabled {
		return errorResponse("run_command", "TARGET_SHELL_DISABLED", "Trusted target shell execution is disabled", requestID, targetID, projectID, started)
	}

	project, err := c.store.GetProject(ctx, targetID, projectID, false)
	if err != nil {
		if registry.ErrorCode(err) == "PROJECT_DISABLED" {
			return errorResponse("run_command", "PROJECT_DISABLED", fmt.Sprintf("Project '%s' is disabled", projectID), requestID, targetID, projectID, started)
		}
		return errorResponse("run_command", "PROJECT_NOT_FOUND", fmt.Sprintf("Project '%s' not found in target '%s'", projectID, targetID), requestID, targetID, projectID, started)
	}

	transport, err := remote.Required(c.remote)
	if err != nil {
		return errorResponse("run_command", "INTERNAL_ERROR", err.Error(), requestID, targetID, projectID, started)
	}

	rootCanonical, err := transport.ResolveCanonicalPath(ctx, target, project.Root, 10*time.Second)
	if err != nil {
		return errorResponse("run_command", "PATH_NOT_FOUND", fmt.Sprintf("Failed to resolve project root: %v", err), requestID, targetID, projectID, started)
	}

	effectiveCWD := rootCanonical
	rawCWD := strings.TrimSpace(req.CWD)
	if rawCWD != "" && rawCWD != "." {
		if filepath.IsAbs(rawCWD) {
			effectiveCWD = filepath.Clean(rawCWD)
		} else {
			effectiveCWD = filepath.Clean(filepath.Join(rootCanonical, rawCWD))
		}
	}

	privReq, err := policy.NormalizePrivilegeRequest(req.Privilege)
	if err != nil {
		return errorResponse("run_command", "INVALID_ARGUMENTS", err.Error(), requestID, targetID, projectID, started)
	}

	privilegeState, privilegeErr := c.authorizeExecutionPrivilege(ctx, actor, targetID, projectID, privReq, target, transport, true)
	if privilegeErr != nil {
		c.auditRunCommandDeny(ctx, actor, requestID, targetID, projectID, command, privReq, privilegeErr.Code, privilegeErr.Message, started)
		return errorResponse("run_command", privilegeErr.Code, privilegeErr.Message, requestID, targetID, projectID, started)
	}

	policyName := privilegeState.PolicyName
	maximumLevel := privilegeState.MaximumLevel
	transportAlreadyElevated := privilegeState.TransportAlreadyElevated
	backend := privilegeState.Backend
	independentElevator := privilegeState.IndependentElevator
	privilegeGuarded := privilegeState.Guarded
	privilegeEffective := privilegeState.Effective

	remoteCommand := command
	executionTarget := target
	executionCWD := effectiveCWD
	executionEnv := req.Env

	if privilegeEffective && privReq == "required" {
		if backend == "privileged-ssh" {
			if strings.TrimSpace(target.PrivilegeUser) == "" {
				return errorResponse("run_command", "PRIVILEGE_SETUP_REQUIRED", "Privileged SSH user is not configured", requestID, targetID, projectID, started)
			}
			executionTarget.User = target.PrivilegeUser
		} else if backend == "shizuku" {
			executionEnv = nil
			executionCWD = "/"
			remoteCommand = prepareShizukuCommand(command, req.Env)
		} else if !transportAlreadyElevated {
			return errorResponse("run_command", "PRIVILEGE_SETUP_REQUIRED", fmt.Sprintf("Privilege backend '%s' cannot execute this request safely", backend), requestID, targetID, projectID, started)
		}
	}

	cmdPrefix := command
	if len(cmdPrefix) > 100 {
		cmdPrefix = cmdPrefix[:100]
	}

	attemptDetail := map[string]any{
		"command":                cmdPrefix,
		"authorization_cwd":      effectiveCWD,
		"execution_cwd":          executionCWD,
		"privilege_request":      privReq,
		"privilege_guarded":      privilegeGuarded,
		"privilege_effective":    privilegeEffective,
		"independent_elevator":   independentElevator,
		"privilege_policy":       policyName,
		"privilege_backend":      backend,
		"privilege_backend_user": target.PrivilegeUser,
	}
	if err := c.recordAuditRequired(ctx, actor, "RUN_COMMAND_ATTEMPT", targetID, projectID, attemptDetail, started); err != nil {
		return errorResponse("run_command", "AUDIT_UNAVAILABLE", "Audit sink is unavailable for a critical operation", requestID, targetID, projectID, started)
	}

	timeoutSec := req.Timeout
	if timeoutSec <= 0 {
		timeoutSec = 30
	}
	timeoutDuration := time.Duration(timeoutSec) * time.Second

	res, err := transport.RunCommand(ctx, executionTarget, remoteCommand, remote.CommandOptions{
		CWD:     executionCWD,
		Env:     executionEnv,
		Stdin:   []byte(req.Stdin),
		Timeout: timeoutDuration,
	})

	if remote.ErrorCode(err) == "SSH_TIMEOUT" {
		durSec := roundDurationSec(started)
		resultData := map[string]any{
			"stdout":            "",
			"stderr":            err.Error(),
			"exit_code":         124,
			"timed_out":         true,
			"duration":          durSec,
			"effective_cwd":     executionCWD,
			"authorization_cwd": effectiveCWD,
			"privilege": map[string]any{
				"requested": privReq,
				"effective": privilegeEffective,
			},
		}
		c.auditRunCommandResult(ctx, actor, requestID, targetID, projectID, cmdPrefix, effectiveCWD, executionCWD, privReq, privilegeGuarded, privilegeEffective, independentElevator, backend, 124, true, started)
		return errorResponseWithResult("run_command", "SSH_TIMEOUT", err.Error(), resultData, requestID, targetID, projectID, started)
	}

	if err != nil {
		c.auditRunCommandError(ctx, actor, requestID, targetID, projectID, remote.ErrorCode(err), err.Error(), started)
		return errorResponse("run_command", remote.ErrorCode(err), err.Error(), requestID, targetID, projectID, started)
	}

	durSec := roundDurationSec(started)
	effectiveLevel := "standard"
	if privilegeEffective {
		effectiveLevel = maximumLevel
	}

	resultData := map[string]any{
		"stdout":            res.Stdout,
		"stderr":            res.Stderr,
		"exit_code":         res.ExitCode,
		"timed_out":         false,
		"duration":          durSec,
		"effective_cwd":     executionCWD,
		"authorization_cwd": effectiveCWD,
		"privilege": map[string]any{
			"requested":            privReq,
			"guarded":              privilegeGuarded,
			"effective":            privilegeEffective,
			"independent_elevator": independentElevator,
			"policy":               policyName,
			"backend":              backend,
			"backend_user":         target.PrivilegeUser,
			"level":                effectiveLevel,
		},
	}

	c.auditRunCommandResult(ctx, actor, requestID, targetID, projectID, cmdPrefix, effectiveCWD, executionCWD, privReq, privilegeGuarded, privilegeEffective, independentElevator, backend, res.ExitCode, false, started)
	if res.ExitCode != 0 {
		return errorResponseWithResult("run_command", "COMMAND_EXIT_NONZERO", fmt.Sprintf("Command exited with status %d", res.ExitCode), resultData, requestID, targetID, projectID, started)
	}
	return successResponse("run_command", resultData, requestID, targetID, projectID, started)
}

func (c *Core) RunTask(
	ctx context.Context,
	requestID, actor, targetID, projectID, taskName string,
) Response {
	started := time.Now()
	taskName = strings.TrimSpace(taskName)
	if taskName == "" {
		return errorResponse("run_task", "INVALID_ARGUMENTS", "Task name must be a non-empty string", requestID, targetID, projectID, started)
	}

	if blocked := c.operationalGate(ctx, "run_task", requestID, targetID, projectID); blocked != nil {
		return responseFromMap(blocked)
	}

	target, err := c.store.GetTarget(ctx, targetID, false)
	if err != nil {
		if registry.ErrorCode(err) == "TARGET_DISABLED" {
			return errorResponse("run_task", "TARGET_DISABLED", fmt.Sprintf("Target '%s' is disabled", targetID), requestID, targetID, projectID, started)
		}
		return errorResponse("run_task", "TARGET_NOT_FOUND", fmt.Sprintf("Target '%s' is not configured", targetID), requestID, targetID, projectID, started)
	}

	project, err := c.store.GetProject(ctx, targetID, projectID, false)
	if err != nil {
		if registry.ErrorCode(err) == "PROJECT_DISABLED" {
			return errorResponse("run_task", "PROJECT_DISABLED", fmt.Sprintf("Project '%s' is disabled", projectID), requestID, targetID, projectID, started)
		}
		return errorResponse("run_task", "PROJECT_NOT_FOUND", fmt.Sprintf("Project '%s' not found in target '%s'", projectID, targetID), requestID, targetID, projectID, started)
	}

	taskDef, ok := project.Tasks[taskName]
	if !ok || !taskDef.Enabled || len(taskDef.Argv) == 0 {
		return errorResponse("run_task", "TASK_NOT_FOUND", fmt.Sprintf("Task '%s' is not defined or not enabled for project '%s'", taskName, projectID), requestID, targetID, projectID, started)
	}

	transport, err := remote.Required(c.remote)
	if err != nil {
		return errorResponse("run_task", "INTERNAL_ERROR", err.Error(), requestID, targetID, projectID, started)
	}

	rootCanonical, err := transport.ResolveCanonicalPath(ctx, target, project.Root, 10*time.Second)
	if err != nil {
		return errorResponse("run_task", "PATH_NOT_FOUND", fmt.Sprintf("Failed to resolve project root: %v", err), requestID, targetID, projectID, started)
	}

	quotedArgs := make([]string, len(taskDef.Argv))
	for i, arg := range taskDef.Argv {
		quotedArgs[i] = shellQuote(arg)
	}
	quotedCmd := strings.Join(quotedArgs, " ")

	privilegeState, privilegeErr := c.authorizeExecutionPrivilege(ctx, actor, targetID, projectID, "standard", target, transport, true)
	if privilegeErr != nil {
		c.auditRunCommandDeny(ctx, actor, requestID, targetID, projectID, quotedCmd, "standard", privilegeErr.Code, privilegeErr.Message, started)
		return errorResponse("run_task", privilegeErr.Code, privilegeErr.Message, requestID, targetID, projectID, started)
	}

	attemptDetail := map[string]any{
		"task":                taskName,
		"privilege_guarded":   privilegeState.Guarded,
		"privilege_effective": privilegeState.Effective,
		"privilege_backend":   privilegeState.Backend,
		"privilege_policy":    privilegeState.PolicyName,
	}
	if err := c.recordAuditRequired(ctx, actor, "RUN_TASK_ATTEMPT", targetID, projectID, attemptDetail, started); err != nil {
		return errorResponse("run_task", "AUDIT_UNAVAILABLE", "Audit sink is unavailable for a critical operation", requestID, targetID, projectID, started)
	}

	timeoutSec := taskDef.Timeout
	if timeoutSec <= 0 {
		timeoutSec = 30
	}

	res, err := transport.RunCommand(ctx, target, quotedCmd, remote.CommandOptions{
		CWD:     rootCanonical,
		Timeout: time.Duration(timeoutSec) * time.Second,
	})
	if err != nil {
		c.auditRunCommandError(ctx, actor, requestID, targetID, projectID, remote.ErrorCode(err), err.Error(), started)
		return errorResponse("run_task", remote.ErrorCode(err), err.Error(), requestID, targetID, projectID, started)
	}

	resultDetail := map[string]any{
		"task":                taskName,
		"exit_code":           res.ExitCode,
		"privilege_effective": privilegeState.Effective,
		"privilege_backend":   privilegeState.Backend,
	}
	durMS := time.Since(started).Milliseconds()
	_ = c.store.RecordActivity(ctx, registry.Activity{
		Actor:      nonEmpty(actor, "mcp-local"),
		Action:     "RUN_TASK",
		TargetID:   &targetID,
		ProjectID:  &projectID,
		DurationMS: &durMS,
		Success:    res.ExitCode == 0,
		Detail:     mustJSON(resultDetail),
	})

	result := map[string]any{
		"task":        taskName,
		"exit_code":   res.ExitCode,
		"stdout":      res.Stdout,
		"stderr":      res.Stderr,
		"duration_ms": res.DurationMS,
	}
	if res.ExitCode != 0 {
		return errorResponseWithResult("run_task", "TASK_EXIT_NONZERO", fmt.Sprintf("Task %q exited with status %d", taskName, res.ExitCode), result, requestID, targetID, projectID, started)
	}
	return successResponse("run_task", result, requestID, targetID, projectID, started)
}

func prepareShizukuCommand(command string, env map[string]string) string {
	var envParts []string
	for k, v := range env {
		if isSafeEnvKey(k) {
			envParts = append(envParts, fmt.Sprintf("export %s=%s;", k, shellQuote(v)))
		}
	}
	envPrefix := strings.Join(envParts, " ")
	if envPrefix != "" {
		envPrefix += " "
	}
	inner := fmt.Sprintf("%scd / && %s", envPrefix, command)
	return fmt.Sprintf("rish -c %s", shellQuote(inner))
}

func isSafeEnvKey(key string) bool {
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

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func roundDurationSec(started time.Time) float64 {
	dur := time.Since(started).Seconds()
	return float64(int(dur*1000+0.5)) / 1000.0
}

func (c *Core) auditRunCommandDeny(
	ctx context.Context,
	actor, requestID, targetID, projectID, command, privReq, code, reason string,
	started time.Time,
) {
	cmdPrefix := command
	if len(cmdPrefix) > 100 {
		cmdPrefix = cmdPrefix[:100]
	}
	durMS := time.Since(started).Milliseconds()
	detail := map[string]any{
		"error":             reason,
		"command":           cmdPrefix,
		"privilege_request": privReq,
	}
	if requestID != "" {
		detail["request_id"] = requestID
	}
	_ = c.store.RecordActivity(ctx, registry.Activity{
		Actor:      nonEmpty(actor, "mcp-local"),
		Action:     "DENY",
		TargetID:   &targetID,
		ProjectID:  &projectID,
		DurationMS: &durMS,
		Success:    false,
		ErrorCode:  &code,
		Detail:     mustJSON(detail),
	})
}

func (c *Core) auditRunCommandResult(
	ctx context.Context,
	actor, requestID, targetID, projectID, cmdPrefix, authCWD, execCWD, privReq string,
	privGuarded, privEffective bool,
	elevator, backend string,
	exitCode int,
	timedOut bool,
	started time.Time,
) {
	durMS := time.Since(started).Milliseconds()
	detail := map[string]any{
		"command":              cmdPrefix,
		"exit_code":            exitCode,
		"authorization_cwd":    authCWD,
		"execution_cwd":        execCWD,
		"privilege_request":    privReq,
		"privilege_guarded":    privGuarded,
		"privilege_effective":  privEffective,
		"independent_elevator": elevator,
		"privilege_backend":    backend,
	}
	if timedOut {
		detail["timed_out"] = true
	}
	if requestID != "" {
		detail["request_id"] = requestID
	}
	_ = c.store.RecordActivity(ctx, registry.Activity{
		Actor:      nonEmpty(actor, "mcp-local"),
		Action:     "RUN_COMMAND",
		TargetID:   &targetID,
		ProjectID:  &projectID,
		DurationMS: &durMS,
		Success:    exitCode == 0,
		Detail:     mustJSON(detail),
	})
}

func (c *Core) auditRunCommandError(
	ctx context.Context,
	actor, requestID, targetID, projectID, code, errStr string,
	started time.Time,
) {
	durMS := time.Since(started).Milliseconds()
	detail := map[string]any{"error": errStr}
	if requestID != "" {
		detail["request_id"] = requestID
	}
	_ = c.store.RecordActivity(ctx, registry.Activity{
		Actor:      nonEmpty(actor, "mcp-local"),
		Action:     "ERROR",
		TargetID:   &targetID,
		ProjectID:  &projectID,
		DurationMS: &durMS,
		Success:    false,
		ErrorCode:  &code,
		Detail:     mustJSON(detail),
	})
}

func (c *Core) recordAuditRequired(
	ctx context.Context,
	actor, action, targetID, projectID string,
	detail map[string]any,
	started time.Time,
) error {
	durMS := time.Since(started).Milliseconds()
	return c.store.RecordActivity(ctx, registry.Activity{
		Actor:      nonEmpty(actor, "mcp-local"),
		Action:     action,
		TargetID:   &targetID,
		ProjectID:  &projectID,
		DurationMS: &durMS,
		Success:    true,
		Detail:     mustJSON(detail),
	})
}

func responseFromMap(m map[string]any) Response {
	var resp Response
	raw, err := json.Marshal(m)
	if err == nil {
		_ = json.Unmarshal(raw, &resp)
	}
	return resp
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
