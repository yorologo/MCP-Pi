package core

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"mcp-gateway-adapter/internal/policy"
	"mcp-gateway-adapter/internal/registry"
	"mcp-gateway-adapter/internal/remote"
)

type Config struct {
	GatewayVersion     string
	CoreAPIVersion     int
	ToolCatalogVersion int
	MCPProtocol        string
}

type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Response struct {
	OK         bool       `json:"ok"`
	Tool       string     `json:"tool"`
	DurationMS int64      `json:"duration_ms"`
	Result     any        `json:"result,omitempty"`
	Error      *ErrorBody `json:"error,omitempty"`
	RequestID  string     `json:"request_id,omitempty"`
	Target     string     `json:"target,omitempty"`
	Project    string     `json:"project,omitempty"`
}

type VersionInfo struct {
	OK                    bool   `json:"ok"`
	GatewayVersion        string `json:"gateway_version"`
	CoreAPIVersion        int    `json:"core_api_version"`
	ToolCatalogVersion    int    `json:"tool_catalog_version"`
	RegistrySchemaVersion int    `json:"registry_schema_version"`
	MCPProtocol           string `json:"mcp_protocol"`
}

type Core struct {
	store  *registry.Store
	config Config
	remote remote.Transport
}

func New(store *registry.Store, config Config) *Core {
	return &Core{store: store, config: config}
}

func NewWithRemote(store *registry.Store, config Config, remoteTransport remote.Transport) *Core {
	return &Core{store: store, config: config, remote: remoteTransport}
}

func (c *Core) Version() VersionInfo {
	return VersionInfo{
		OK:                    true,
		GatewayVersion:        c.config.GatewayVersion,
		CoreAPIVersion:        c.config.CoreAPIVersion,
		ToolCatalogVersion:    c.config.ToolCatalogVersion,
		RegistrySchemaVersion: registry.SchemaVersion,
		MCPProtocol:           c.config.MCPProtocol,
	}
}

func (c *Core) Catalog(ctx context.Context, clientID string, tools []string) ([]string, error) {
	return policy.CatalogForClient(ctx, c.store, clientID, tools)
}

func (c *Core) Health(ctx context.Context, requestID string) Response {
	started := time.Now()

	gatewayEnabled, err := c.boolSetting(ctx, "gateway_enabled", true)
	if err != nil {
		return errorResponse("health", "INTERNAL_ERROR", err.Error(), requestID, "", "", started)
	}
	writesEnabled, err := c.boolSetting(ctx, "writes_enabled", false)
	if err != nil {
		return errorResponse("health", "INTERNAL_ERROR", err.Error(), requestID, "", "", started)
	}
	shellEnabled, err := c.boolSetting(ctx, "shell_enabled", false)
	if err != nil {
		return errorResponse("health", "INTERNAL_ERROR", err.Error(), requestID, "", "", started)
	}
	targetCount, err := c.store.TargetCount(ctx)
	if err != nil {
		return errorResponse("health", "INTERNAL_ERROR", err.Error(), requestID, "", "", started)
	}
	hostname, err := os.Hostname()
	if err != nil {
		return errorResponse("health", "INTERNAL_ERROR", err.Error(), requestID, "", "", started)
	}

	status := "ok"
	if !gatewayEnabled {
		status = "disabled"
	}
	result := map[string]any{
		"gateway_status":     status,
		"gateway_enabled":    gatewayEnabled,
		"writes_enabled":     writesEnabled,
		"shell_enabled":      shellEnabled,
		"hostname":           hostname,
		"gateway_version":    c.config.GatewayVersion,
		"architecture":       runtime.GOARCH,
		"go_version":         runtime.Version(),
		"config_loaded":      c.store != nil,
		"configured_targets": targetCount,
	}
	return successResponse("health", result, requestID, "", "", started)
}

func (c *Core) ListTargets(ctx context.Context, requestID string) Response {
	started := time.Now()
	targets, err := c.store.ListTargets(ctx)
	if err != nil {
		return errorResponse("list_targets", "INTERNAL_ERROR", err.Error(), requestID, "", "", started)
	}

	out := make([]map[string]any, 0, len(targets))
	for _, target := range targets {
		projects := target.Projects
		if projects == nil {
			projects = []string{}
		}
		out = append(out, map[string]any{
			"id":               target.ID,
			"display_name":     target.DisplayName,
			"platform":         target.Platform,
			"privilege_policy": target.PrivilegePolicy,
			"enabled":          target.Enabled,
			"project_count":    target.ProjectCount,
			"projects":         projects,
		})
	}
	return successResponse("list_targets", map[string]any{"targets": out}, requestID, "", "", started)
}

func (c *Core) Authorize(
	ctx context.Context,
	clientID, targetID, projectID, toolName string,
	forCatalog bool,
) (policy.Decision, error) {
	return policy.AuthorizeClient(ctx, c.store, clientID, targetID, projectID, toolName, forCatalog)
}

func (c *Core) boolSetting(ctx context.Context, key string, defaultValue bool) (bool, error) {
	fallback := "false"
	if defaultValue {
		fallback = "true"
	}
	value, err := c.store.GetSetting(ctx, key, fallback)
	if err != nil {
		return false, err
	}
	return truthy(value), nil
}

func truthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes", "on":
		return true
	default:
		return false
	}
}

func successResponse(tool string, result any, requestID, target, project string, started time.Time) Response {
	return Response{
		OK:         true,
		Tool:       tool,
		DurationMS: durationMS(started),
		Result:     result,
		RequestID:  requestID,
		Target:     target,
		Project:    project,
	}
}

func errorResponse(tool, code, message, requestID, target, project string, started time.Time) Response {
	if code == "" {
		code = "INTERNAL_ERROR"
	}
	if message == "" {
		message = fmt.Sprintf("%s failed", tool)
	}
	return Response{
		OK:         false,
		Tool:       tool,
		DurationMS: durationMS(started),
		Error:      &ErrorBody{Code: code, Message: message},
		RequestID:  requestID,
		Target:     target,
		Project:    project,
	}
}

func durationMS(started time.Time) int64 {
	elapsed := time.Since(started).Milliseconds()
	if elapsed < 0 {
		return 0
	}
	return elapsed
}
