package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"mcp-gateway-adapter/internal/policy"
	"mcp-gateway-adapter/internal/registry"
)

type Invocation struct {
	ClientID  string
	RequestID string
}

// Invoke is the in-process replacement boundary for mcp_gateway.bridge invoke.
// It preserves bridge authorization precedence and dispatches only tools that
// have already demonstrated Go parity. Unported tools fail closed.
func (c *Core) Invoke(ctx context.Context, invocation Invocation, toolName string, args map[string]any) map[string]any {
	if args == nil {
		args = map[string]any{}
	}
	if _, ok := policy.ToolCapabilities[toolName]; !ok {
		return directError(toolName, "TOOL_NOT_ALLOWED", fmt.Sprintf("Tool '%s' is not in the allowlisted gateway tools", toolName))
	}

	clientID := strings.TrimSpace(invocation.ClientID)
	if clientID == "" {
		clientID = "local"
	}

	targetID, _ := args["target"].(string)
	projectID, _ := args["project"].(string)

	decision, err := c.Authorize(ctx, clientID, targetID, projectID, toolName, false)
	if err != nil {
		return directError(toolName, "CORE_INIT_ERROR", "Failed to initialize gateway core: "+err.Error())
	}
	if !decision.Allowed {
		message := decision.Reason
		if message == "" {
			message = fmt.Sprintf("Client '%s' is not authorized to invoke tool '%s'", clientID, toolName)
		}
		// bridge.py intentionally wraps authorization failures with the
		// public TOOL_NOT_ALLOWED code while retaining the specific reason.
		return directError(toolName, "TOOL_NOT_ALLOWED", message)
	}

	switch toolName {
	case "health":
		return responseMap(c.Health(ctx, invocation.RequestID))
	case "list_targets":
		return responseMap(c.ListTargets(ctx, invocation.RequestID))
	case "target_status":
		target, ok := requiredString(args, "target")
		if !ok {
			return directError(toolName, "INVALID_ARGUMENTS", "Missing or invalid required argument 'target'")
		}
		if blocked := c.operationalGate(ctx, toolName, invocation.RequestID, target, ""); blocked != nil {
			return blocked
		}
		return responseMap(c.TargetStatus(ctx, invocation.RequestID, target))

	case "list_directory":
		target, ok := requiredString(args, "target")
		if !ok {
			return directError(toolName, "INVALID_ARGUMENTS", "Missing or invalid required argument 'target'")
		}
		project, ok := requiredString(args, "project")
		if !ok {
			return directError(toolName, "INVALID_ARGUMENTS", "Missing or invalid required argument 'project'")
		}
		relPath := "."
		if raw, exists := args["relative_path"]; exists {
			value, ok := raw.(string)
			if !ok {
				started := time.Now()
				return responseMap(errorResponse(toolName, "INVALID_PATH", "Relative path must be a string", invocation.RequestID, target, project, started))
			}
			relPath = value
		}
		if blocked := c.operationalGate(ctx, toolName, invocation.RequestID, target, project); blocked != nil {
			return blocked
		}
		return responseMap(c.ListDirectory(ctx, invocation.RequestID, target, project, relPath))

	case "file_stat", "read_file":
		target, project, relPath, ok := requiredTargetProjectPath(args)
		if !ok {
			return directError(toolName, "INVALID_ARGUMENTS", "Missing or invalid required arguments: 'target', 'project', and 'relative_path' are required")
		}
		if blocked := c.operationalGate(ctx, toolName, invocation.RequestID, target, project); blocked != nil {
			return blocked
		}
		if toolName == "file_stat" {
			return responseMap(c.FileStat(ctx, invocation.RequestID, target, project, relPath))
		}
		return responseMap(c.ReadFile(ctx, invocation.RequestID, target, project, relPath))

	case "git_status":
		target, okTarget := requiredString(args, "target")
		project, okProject := requiredString(args, "project")
		if !okTarget || !okProject {
			return directError(toolName, "INVALID_ARGUMENTS", "Missing or invalid required arguments: 'target' and 'project' are required")
		}
		if blocked := c.operationalGate(ctx, toolName, invocation.RequestID, target, project); blocked != nil {
			return blocked
		}
		return responseMap(c.GitStatus(ctx, invocation.RequestID, target, project))

	case "search":
		target, okTarget := requiredString(args, "target")
		project, okProject := requiredString(args, "project")
		patternText, okPattern := requiredString(args, "pattern")
		if !okTarget || !okProject || !okPattern {
			return directError(toolName, "INVALID_ARGUMENTS", "Missing or invalid required arguments: 'target', 'project', and 'pattern' are required")
		}
		relativePath := "."
		if raw, exists := args["relative_path"]; exists && pythonTruthy(raw) {
			if value, ok := raw.(string); ok {
				relativePath = value
			}
		} else if raw, exists := args["path"]; exists {
			if value, ok := raw.(string); ok {
				relativePath = value
			}
		}
		isRegex := pythonTruthy(args["is_regex"])
		if blocked := c.operationalGate(ctx, toolName, invocation.RequestID, target, project); blocked != nil {
			return blocked
		}
		response := c.Search(ctx, invocation.RequestID, target, project, patternText, relativePath, isRegex)
		if response.OK {
			c.auditSearchBestEffort(ctx, clientID, invocation.RequestID, target, project, patternText, response)
		}
		return responseMap(response)

	default:
		return directError(toolName, "TOOL_NOT_IMPLEMENTED", fmt.Sprintf("Tool '%s' has not been ported to Go core yet", toolName))
	}
}

func (c *Core) operationalGate(ctx context.Context, tool, requestID, targetID, projectID string) map[string]any {
	enabled, err := c.boolSetting(ctx, "gateway_enabled", true)
	if err != nil {
		return responseMap(errorResponse(tool, "INTERNAL_ERROR", err.Error(), requestID, targetID, projectID, time.Now()))
	}
	if !enabled {
		return responseMap(errorResponse(
			tool,
			"GATEWAY_DISABLED",
			"Gateway operations are disabled by administrator kill switch",
			requestID,
			targetID,
			projectID,
			time.Now(),
		))
	}
	return nil
}

func (c *Core) auditSearchBestEffort(
	ctx context.Context,
	clientID, requestID, targetID, projectID, patternText string,
	response Response,
) {
	result, _ := response.Result.(map[string]any)
	count, _ := result["count"].(int)
	detail := map[string]any{"pattern": patternText, "count": count}
	if requestID != "" {
		detail["request_id"] = requestID
	}
	rawDetail, err := json.Marshal(detail)
	if err != nil {
		return
	}
	duration := response.DurationMS
	target := targetID
	project := projectID
	_ = c.store.RecordActivity(ctx, registry.Activity{
		Actor:      nonEmpty(clientID, "mcp-local"),
		Action:     "SEARCH",
		TargetID:   &target,
		ProjectID:  &project,
		DurationMS: &duration,
		Success:    true,
		Detail:     string(rawDetail),
	})
}

func requiredTargetProjectPath(args map[string]any) (string, string, string, bool) {
	target, okTarget := requiredString(args, "target")
	project, okProject := requiredString(args, "project")
	relativePath, okPath := requiredString(args, "relative_path")
	return target, project, relativePath, okTarget && okProject && okPath
}

func requiredString(args map[string]any, key string) (string, bool) {
	value, ok := args[key].(string)
	if !ok || value == "" {
		return "", false
	}
	return value, true
}

func pythonTruthy(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case float64:
		return v != 0
	case float32:
		return v != 0
	case int:
		return v != 0
	case int64:
		return v != 0
	case int32:
		return v != 0
	case []any:
		return len(v) != 0
	case map[string]any:
		return len(v) != 0
	default:
		return true
	}
}

func directError(tool, code, message string) map[string]any {
	return map[string]any{
		"ok":   false,
		"tool": tool,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	}
}

func responseMap(response Response) map[string]any {
	out := map[string]any{
		"ok":          response.OK,
		"tool":        response.Tool,
		"duration_ms": response.DurationMS,
	}
	if response.Result != nil {
		out["result"] = response.Result
	}
	if response.Error != nil {
		out["error"] = map[string]any{
			"code":    response.Error.Code,
			"message": response.Error.Message,
		}
	}
	if response.RequestID != "" {
		out["request_id"] = response.RequestID
	}
	if response.Target != "" {
		out["target"] = response.Target
	}
	if response.Project != "" {
		out["project"] = response.Project
	}
	return out
}
