package policy

import (
	"context"
	"fmt"
	"strings"

	"mcp-gateway-adapter/internal/registry"
)

const TargetPrivilegeCapability = "target_admin"

var ToolCapabilities = map[string][]string{
	"health":              {"health", "read", "*"},
	"list_targets":        {"read", "list_targets", "*"},
	"target_status":       {"read", "target_status", "*"},
	"list_directory":      {"read", "list_directory", "*"},
	"file_stat":           {"read", "file_stat", "*"},
	"read_file":           {"read", "read_file", "*"},
	"git_status":          {"read", "git_status", "*"},
	"run_task":            {"execute", "run_task", "tasks", "*"},
	"write_file":          {"write", "write_file", "*"},
	"append_file":         {"write", "append_file", "*"},
	"delete_file":         {"write", "delete_file", "*"},
	"copy_file":           {"write", "copy_file", "*"},
	"move_file":           {"write", "move_file", "*"},
	"mkdir":               {"write", "mkdir", "*"},
	"search":              {"read", "search", "*"},
	"run_command":         {"target_shell", "execute", "run_command", "environment_management", "system_package_management", "*"},
	"gateway_status":      {"admin", "status", "*"},
	"gateway_doctor":      {"admin", "doctor", "*"},
	"gateway_backup":      {"admin", "backup", "*"},
	"gateway_maintenance": {"admin", "maintenance", "*"},
	"gateway_reboot":      {"admin", "reboot", "*"},
}

var mutatingTools = map[string]bool{
	"write_file":  true,
	"append_file": true,
	"delete_file": true,
	"copy_file":   true,
	"move_file":   true,
	"mkdir":       true,
}

type Decision struct {
	Allowed bool
	Code    string
	Reason  string
}

func allow() Decision {
	return Decision{Allowed: true}
}

func deny(code, reason string) Decision {
	return Decision{Allowed: false, Code: code, Reason: code + ": " + reason}
}

func AuthorizeClient(
	ctx context.Context,
	store *registry.Store,
	clientID, targetID, projectID, toolName string,
	forCatalog bool,
) (Decision, error) {
	clientID = strings.TrimSpace(clientID)
	upperClientID := strings.ToUpper(clientID)
	if clientID == "" || upperClientID == "NONE" || upperClientID == "ANONYMOUS" {
		return deny("ANONYMOUS_CLIENT_DENIED", "Client identity required"), nil
	}

	switch clientID {
	case "local", "admin", "system", "test":
		return allow(), nil
	}

	allowedCaps, ok := ToolCapabilities[toolName]
	if !ok {
		return deny("TOOL_NOT_RECOGNIZED", fmt.Sprintf("Tool '%s' not in gateway catalog", toolName)), nil
	}

	client, err := store.GetClient(ctx, clientID)
	if err != nil {
		return deny("CLIENT_NOT_FOUND", fmt.Sprintf("Client '%s' is not registered", clientID)), nil
	}
	if !client.Enabled {
		return deny("CLIENT_DISABLED", fmt.Sprintf("Client '%s' is disabled", clientID)), nil
	}

	grants, err := store.GetClientGrants(ctx, clientID)
	if err != nil {
		return Decision{}, fmt.Errorf("load client grants: %w", err)
	}
	if len(grants) == 0 {
		return deny("TOOL_NOT_ALLOWED", fmt.Sprintf("Client '%s' has no active grants", clientID)), nil
	}

	matched := false
	for _, grant := range grants {
		if grantMatches(grant, targetID, projectID, allowedCaps, true) {
			matched = true
			break
		}
	}
	if !matched {
		return deny("TOOL_NOT_ALLOWED", fmt.Sprintf("Client '%s' lacks grant capability for tool '%s'", clientID, toolName)), nil
	}

	if forCatalog {
		return allow(), nil
	}

	gatewayEnabled, err := store.GetSetting(ctx, "gateway_enabled", "true")
	if err != nil {
		return Decision{}, err
	}
	if strings.ToLower(gatewayEnabled) == "false" && toolName != "health" {
		return deny("GATEWAY_DISABLED", "Global kill switch is active"), nil
	}

	if toolName == "run_command" {
		shellEnabled, err := store.GetSetting(ctx, "shell_enabled", "false")
		if err != nil {
			return Decision{}, err
		}
		if strings.ToLower(shellEnabled) != "true" {
			return deny("TARGET_SHELL_DISABLED", "Trusted target shell execution is disabled"), nil
		}
	}

	if targetID != "" {
		_, err := store.GetTarget(ctx, targetID, false)
		if err != nil {
			if registry.ErrorCode(err) == "TARGET_DISABLED" {
				return deny("TARGET_DISABLED", fmt.Sprintf("Target '%s' is disabled", targetID)), nil
			}
			return deny("TARGET_NOT_FOUND", fmt.Sprintf("Target '%s' is not configured", targetID)), nil
		}
	}

	if targetID != "" && projectID != "" {
		_, err := store.GetProject(ctx, targetID, projectID, false)
		if err != nil {
			if registry.ErrorCode(err) == "PROJECT_DISABLED" {
				return deny("PROJECT_DISABLED", fmt.Sprintf("Project '%s' is disabled", projectID)), nil
			}
			return deny("PROJECT_NOT_FOUND", fmt.Sprintf("Project '%s' not found in target '%s'", projectID, targetID)), nil
		}
	}

	if mutatingTools[toolName] {
		writesEnabled, err := store.GetSetting(ctx, "writes_enabled", "false")
		if err != nil {
			return Decision{}, err
		}
		if strings.ToLower(writesEnabled) != "true" {
			return deny("WRITES_DISABLED", "Global writes are disabled"), nil
		}
		if targetID != "" && projectID != "" {
			project, err := store.GetProject(ctx, targetID, projectID, false)
			if err == nil && !project.Write {
				return deny("WRITE_NOT_ALLOWED", fmt.Sprintf("Write disabled for project '%s'", projectID)), nil
			}
		}
	}

	return allow(), nil
}

func AuthorizePrivilegeRequest(
	ctx context.Context,
	store *registry.Store,
	clientID, targetID, projectID string,
) (Decision, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return deny("PRIVILEGE_GRANT_REQUIRED", "Explicit target_admin capability is required"), nil
	}

	client, err := store.GetClient(ctx, clientID)
	if err != nil {
		return deny("PRIVILEGE_GRANT_REQUIRED", fmt.Sprintf("Client '%s' is not registered", clientID)), nil
	}
	if !client.Enabled {
		return deny("PRIVILEGE_GRANT_REQUIRED", fmt.Sprintf("Client '%s' is disabled", clientID)), nil
	}

	grants, err := store.GetClientGrants(ctx, clientID)
	if err != nil {
		grants = nil
	}
	for _, grant := range grants {
		if grantMatches(grant, targetID, projectID, []string{TargetPrivilegeCapability}, false) {
			return allow(), nil
		}
	}

	return deny(
		"PRIVILEGE_GRANT_REQUIRED",
		fmt.Sprintf("Client '%s' requires an explicit %s grant for %s/%s", clientID, TargetPrivilegeCapability, targetID, projectID),
	), nil
}

func grantMatches(
	grant registry.Grant,
	targetID, projectID string,
	allowedCapabilities []string,
	allowCapabilityWildcard bool,
) bool {
	if !grant.Enabled {
		return false
	}

	grantCaps := splitCapabilities(grant.Capability)
	allowed := make(map[string]struct{}, len(allowedCapabilities))
	for _, capability := range allowedCapabilities {
		allowed[capability] = struct{}{}
	}

	capabilityMatched := false
	for capability := range grantCaps {
		if _, ok := allowed[capability]; ok {
			capabilityMatched = true
			break
		}
	}
	if !capabilityMatched {
		if _, wildcard := grantCaps["*"]; !allowCapabilityWildcard || !wildcard {
			return false
		}
	}

	if targetID != "" && grant.TargetID != "*" && grant.TargetID != targetID {
		return false
	}
	if projectID != "" && grant.ProjectID != "*" && grant.ProjectID != projectID {
		return false
	}
	return true
}

func splitCapabilities(raw string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			out[item] = struct{}{}
		}
	}
	return out
}

func CatalogForClient(ctx context.Context, store *registry.Store, clientID string, tools []string) ([]string, error) {
	if strings.TrimSpace(clientID) == "" || strings.EqualFold(strings.TrimSpace(clientID), "NONE") ||
		strings.EqualFold(strings.TrimSpace(clientID), "ANONYMOUS") {
		return []string{}, nil
	}
	switch strings.TrimSpace(clientID) {
	case "local", "admin", "system", "test":
		return append([]string(nil), tools...), nil
	}

	out := make([]string, 0, len(tools))
	for _, tool := range tools {
		decision, err := AuthorizeClient(ctx, store, clientID, "", "", tool, true)
		if err != nil {
			return nil, err
		}
		if decision.Allowed {
			out = append(out, tool)
		}
	}
	return out, nil
}
