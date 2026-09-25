package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ExpectedBridgeAPIVersion specifies the required bridge API contract.
const ExpectedBridgeAPIVersion = 1

// BridgeVersionInfo models the version payload from 'mcp_gateway.bridge version'.
type BridgeVersionInfo struct {
	OK                    bool   `json:"ok"`
	GatewayVersion        string `json:"gateway_version"`
	CoreAPIVersion        int    `json:"core_api_version"`
	BridgeAPIVersion      int    `json:"bridge_api_version"`
	ToolCatalogVersion    int    `json:"tool_catalog_version"`
	RegistrySchemaVersion int    `json:"registry_schema_version"`
	MCPProtocol           string `json:"mcp_protocol"`
	Error                 string `json:"error,omitempty"`
}

// AdapterState maintains lifecycle readiness and contract version status.
type AdapterState struct {
	mu          sync.RWMutex
	ready       bool
	notReadyMsg string
	versionInfo *BridgeVersionInfo
	startTime   time.Time
}

// NewAdapterState initializes an AdapterState instance.
func NewAdapterState() *AdapterState {
	return &AdapterState{
		ready:       false,
		notReadyMsg: "ADAPTER_NOT_READY: Initializing",
		startTime:   time.Now(),
	}
}

func (s *AdapterState) IsReady() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ready
}

func (s *AdapterState) GetStatus() (bool, string, *BridgeVersionInfo) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ready, s.notReadyMsg, s.versionInfo
}

func (s *AdapterState) SetReady(ready bool, msg string, info *BridgeVersionInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ready = ready
	s.notReadyMsg = msg
	s.versionInfo = info
}

func adapterVersion(state *AdapterState) string {
	if state == nil {
		return "unknown"
	}
	_, _, info := state.GetStatus()
	if info == nil || strings.TrimSpace(info.GatewayVersion) == "" {
		return "unknown"
	}
	return strings.TrimSpace(info.GatewayVersion)
}

// BridgeConfig holds configuration for invoking the Python Core Bridge.
type BridgeConfig struct {
	PythonBin  string
	PythonPath string
	DBPath     string
	ClientID   string
	AuthToken  string
	Timeout    time.Duration
}

// DefaultBridgeConfig returns reasonable defaults for development and Pi runtime.
func DefaultBridgeConfig() *BridgeConfig {
	pyBin := "python3"
	if runtime.GOOS == "windows" {
		pyBin = "python"
	}
	if _, err := os.Stat("/usr/bin/python3"); err == nil {
		pyBin = "/usr/bin/python3"
	}

	pyPath := "src"
	if _, err := os.Stat("/home/mcp-gateway/mcp-gateway/src"); err == nil {
		pyPath = "/home/mcp-gateway/mcp-gateway/src"
	}

	return &BridgeConfig{
		PythonBin:  pyBin,
		PythonPath: pyPath,
		Timeout:    35 * time.Second,
	}
}

func (b *BridgeConfig) buildEnv() []string {
	env := os.Environ()
	if b.PythonPath != "" {
		absPath, err := filepath.Abs(b.PythonPath)
		if err == nil {
			env = append(env, fmt.Sprintf("PYTHONPATH=%s", absPath))
		} else {
			env = append(env, fmt.Sprintf("PYTHONPATH=%s", b.PythonPath))
		}
	}
	if b.DBPath != "" {
		env = append(env, fmt.Sprintf("MCP_GATEWAY_DB=%s", b.DBPath))
	}
	if b.ClientID != "" {
		env = append(env, fmt.Sprintf("MCP_CLIENT_ID=%s", b.ClientID))
	}
	return env
}

// CheckBridgeCompatibility queries the Python bridge for its version contract.
func (b *BridgeConfig) CheckBridgeCompatibility(ctx context.Context) (*BridgeVersionInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, b.PythonBin, "-m", "mcp_gateway.bridge", "version")
	cmd.Env = b.buildEnv()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("bridge version check failed: %w, stderr: %s", err, stderr.String())
	}

	var info BridgeVersionInfo
	if err := json.Unmarshal(stdout.Bytes(), &info); err != nil {
		return nil, fmt.Errorf("invalid json from bridge version: %w", err)
	}

	if !info.OK {
		return &info, fmt.Errorf("bridge error: %s", info.Error)
	}

	if info.BridgeAPIVersion != ExpectedBridgeAPIVersion {
		return &info, fmt.Errorf("bridge_api_version mismatch (expected %d, got %d)", ExpectedBridgeAPIVersion, info.BridgeAPIVersion)
	}

	return &info, nil
}

// CallBridge invokes python3 -m mcp_gateway.bridge invoke <tool> <argsJSON> [--request-id <reqID>].
func (b *BridgeConfig) CallBridge(ctx context.Context, toolName string, argsJSON []byte, requestID string) ([]byte, bool, error) {
	if len(argsJSON) == 0 {
		argsJSON = []byte("{}")
	}

	ctx, cancel := context.WithTimeout(ctx, b.Timeout)
	defer cancel()

	args := []string{"-m", "mcp_gateway.bridge", "invoke", toolName, string(argsJSON)}
	if requestID != "" {
		args = append(args, "--request-id", requestID)
	}
	if b.ClientID != "" {
		args = append(args, "--client-id", b.ClientID)
	}

	cmd := exec.CommandContext(ctx, b.PythonBin, args...)
	cmd.Env = b.buildEnv()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	outBytes := stdout.Bytes()

	// Parse JSON to determine Core-level success
	var resp struct {
		OK    bool           `json:"ok"`
		Tool  string         `json:"tool"`
		Error map[string]any `json:"error"`
	}

	if jsonErr := json.Unmarshal(outBytes, &resp); jsonErr == nil {
		return outBytes, !resp.OK, nil
	}

	// If not JSON, it was a process error
	if err != nil {
		errResp, _ := json.Marshal(map[string]any{
			"ok":   false,
			"tool": toolName,
			"error": map[string]any{
				"code":    "BRIDGE_EXEC_ERROR",
				"message": fmt.Sprintf("Subprocess error: %v, stderr: %s", err, stderr.String()),
			},
		})
		return errResp, true, nil
	}

	return outBytes, false, nil
}

// GetAllowedTools returns a set of allowlisted tool names for the configured ClientID.
// If ClientID is empty, "local", or "admin", returns nil (all tools allowed).
// If client has no grants or is invalid, returns an empty map (no tools allowed).
func (b *BridgeConfig) GetAllowedTools(ctx context.Context) (map[string]bool, error) {
	if b.ClientID == "" || b.ClientID == "local" || b.ClientID == "admin" {
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, b.PythonBin, "-m", "mcp_gateway.bridge", "tools", "--client-id", b.ClientID)
	cmd.Env = b.buildEnv()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("bridge tools failed for client %s: %w, stderr: %s", b.ClientID, err, stderr.String())
	}

	var resp struct {
		OK    bool     `json:"ok"`
		Tools []string `json:"tools"`
		Error string   `json:"error,omitempty"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("invalid json from bridge tools: %w", err)
	}

	if !resp.OK {
		return nil, fmt.Errorf("bridge tools error: %s", resp.Error)
	}

	allowed := make(map[string]bool)
	for _, t := range resp.Tools {
		allowed[t] = true
	}
	return allowed, nil
}

func generateRequestID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return fmt.Sprintf("req-%x", b)
}

// SecurityMiddleware enforces Host and Origin protection, plus body size limits.
func SecurityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Limit request body size to 1 MiB
		r.Body = http.MaxBytesReader(w, r.Body, 1048576)

		// 2. Validate Host header (must be 127.0.0.1 or localhost)
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if host != "127.0.0.1" && host != "localhost" {
			http.Error(w, "Forbidden: Invalid Host header (loopback only)", http.StatusForbidden)
			return
		}

		// 3. Validate Origin header (if present, must originate from localhost/127.0.0.1)
		origin := r.Header.Get("Origin")
		if origin != "" {
			u, err := url.Parse(origin)
			if err != nil {
				http.Error(w, "Forbidden: Malformed Origin header", http.StatusForbidden)
				return
			}
			origHost := u.Hostname()
			if origHost != "127.0.0.1" && origHost != "localhost" {
				http.Error(w, "Forbidden: Cross-origin access denied", http.StatusForbidden)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// NewGatewayServer creates an official MCP server registering tools in deterministic order.
func NewGatewayServer(bridge *BridgeConfig, state *AdapterState) *mcp.Server {
	opts := &mcp.ServerOptions{
		Capabilities: &mcp.ServerCapabilities{
			Tools: &mcp.ToolCapabilities{
				ListChanged: true,
			},
		},
	}
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "mcp-gateway-adapter",
		Title:   "MCP Raspberry Pi Gateway Official Adapter",
		Version: adapterVersion(state),
	}, opts)

	var allowedTools map[string]bool
	if bridge != nil {
		var err error
		allowedTools, err = bridge.GetAllowedTools(context.Background())
		if err != nil {
			log.Printf("[WARN] Failed to query allowed tools for client %q: %v. Defaulting to fail-closed empty catalog.", bridge.ClientID, err)
			allowedTools = make(map[string]bool)
		}
	}

	syncServerTools(server, bridge, state, allowedTools)
	currentAllowed := cloneToolSet(allowedTools)
	var catalogMu sync.Mutex

	if bridge != nil && bridge.ClientID != "" && bridge.ClientID != "local" && bridge.ClientID != "admin" {
		server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
			return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
				if method == "tools/list" {
					allowed, err := bridge.GetAllowedTools(ctx)
					if err == nil {
						catalogMu.Lock()
						if !toolSetsEqual(currentAllowed, allowed) {
							syncServerTools(server, bridge, state, allowed)
							currentAllowed = cloneToolSet(allowed)
						}
						catalogMu.Unlock()
					}
				}
				return next(ctx, method, req)
			}
		})
	}

	return server
}

func cloneToolSet(src map[string]bool) map[string]bool {
	if src == nil {
		return nil
	}
	out := make(map[string]bool, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func toolSetsEqual(a, b map[string]bool) bool {
	if a == nil && b == nil {
		return true
	}
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func boolPtr(v bool) *bool { return &v }

func toolAnnotations(name string) *mcp.ToolAnnotations {
	readOnly := map[string]bool{
		"health": true, "list_targets": true, "target_status": true,
		"list_directory": true, "file_stat": true, "read_file": true,
		"git_status": true, "search": true, "gateway_status": true,
		"gateway_doctor": true,
	}
	if readOnly[name] {
		return &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: boolPtr(false), IdempotentHint: true, OpenWorldHint: boolPtr(name == "target_status")}
	}
	destructive := map[string]bool{
		"run_command": true, "run_task": true, "write_file": true,
		"delete_file": true, "move_file": true, "copy_file": true,
		"gateway_reboot": true, "gateway_maintenance": true,
	}
	idempotent := map[string]bool{
		"write_file": true, "copy_file": true, "move_file": false,
		"delete_file": true, "mkdir": true,
	}
	openWorld := name == "run_command" || name == "run_task"
	return &mcp.ToolAnnotations{
		ReadOnlyHint:    false,
		DestructiveHint: boolPtr(destructive[name]),
		IdempotentHint:  idempotent[name],
		OpenWorldHint:   boolPtr(openWorld),
	}
}

func addGatewayTool(server *mcp.Server, tool *mcp.Tool, handler mcp.ToolHandler) {
	tool.Annotations = toolAnnotations(tool.Name)
	server.AddTool(tool, handler)
}

var allKnownTools = []string{
	"append_file",
	"copy_file",
	"delete_file",
	"file_stat",
	"gateway_backup",
	"gateway_doctor",
	"gateway_maintenance",
	"gateway_reboot",
	"gateway_status",
	"git_status",
	"health",
	"list_directory",
	"list_targets",
	"mkdir",
	"move_file",
	"read_file",
	"run_command",
	"run_task",
	"search",
	"target_status",
	"write_file",
}

func syncServerTools(server *mcp.Server, bridge *BridgeConfig, state *AdapterState, allowed map[string]bool) {
	server.RemoveTools(allKnownTools...)
	for _, toolName := range allKnownTools {
		if allowed == nil || allowed[toolName] {
			registerToolByName(server, toolName, bridge, state)
		}
	}
}

func makeToolHandler(toolName string, bridge *BridgeConfig, state *AdapterState) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if bridge != nil && bridge.ClientID == "NONE" {
			errJSON, _ := json.Marshal(map[string]any{
				"ok":   false,
				"tool": toolName,
				"error": map[string]any{
					"code":    "ANONYMOUS_CLIENT_DENIED",
					"message": "Anonymous callers are not authorized to call tools",
				},
			})
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: string(errJSON)},
				},
			}, nil
		}

		if state != nil && !state.IsReady() {
			_, notReadyMsg, _ := state.GetStatus()
			errJSON, _ := json.Marshal(map[string]any{
				"ok":   false,
				"tool": toolName,
				"error": map[string]any{
					"code":    "ADAPTER_NOT_READY",
					"message": notReadyMsg,
				},
			})
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: string(errJSON)},
				},
			}, nil
		}

		rawArgs := req.Params.Arguments
		reqID := generateRequestID()

		outBytes, isErr, err := bridge.CallBridge(ctx, toolName, rawArgs, reqID)
		if err != nil {
			return nil, err
		}

		var structured map[string]any
		_ = json.Unmarshal(outBytes, &structured)

		return &mcp.CallToolResult{
			IsError: isErr,
			Content: []mcp.Content{
				&mcp.TextContent{Text: string(outBytes)},
			},
			StructuredContent: structured,
		}, nil
	}
}

func registerToolByName(server *mcp.Server, toolName string, bridge *BridgeConfig, state *AdapterState) {
	handler := makeToolHandler(toolName, bridge, state)
	switch toolName {
	case "gateway_backup":
		addGatewayTool(server, &mcp.Tool{
			Name:        "gateway_backup",
			Description: "Generate safe online SQLite backup of gateway registry database",
			InputSchema: map[string]any{
				"type": "object",
			},
		}, handler)
	case "gateway_doctor":
		addGatewayTool(server, &mcp.Tool{
			Name:        "gateway_doctor",
			Description: "Execute unified 19-point system health and integrity check",
			InputSchema: map[string]any{
				"type": "object",
			},
		}, handler)
	case "gateway_maintenance":
		addGatewayTool(server, &mcp.Tool{
			Name:        "gateway_maintenance",
			Description: "Perform safe automated maintenance (disk check, backup rotation, registry integrity, update preview)",
			InputSchema: map[string]any{
				"type": "object",
			},
		}, handler)
	case "gateway_reboot":
		addGatewayTool(server, &mcp.Tool{
			Name:        "gateway_reboot",
			Description: "Request controlled reboot of the MCP-Pi appliance",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"confirm": map[string]any{
						"type":        "boolean",
						"description": "Explicit confirmation to reboot appliance (must be true)",
					},
				},
				"required": []string{"confirm"},
			},
		}, handler)
	case "gateway_status":
		addGatewayTool(server, &mcp.Tool{
			Name:        "gateway_status",
			Description: "Check appliance health, system metrics (RAM, zram, CPU, temp, storage), and service status",
			InputSchema: map[string]any{
				"type": "object",
			},
		}, handler)
	case "file_stat":
		addGatewayTool(server, &mcp.Tool{
			Name:        "file_stat",
			Description: "Get metadata of a file or directory within a project",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"target": map[string]any{
						"type":        "string",
						"description": "Target ID",
					},
					"project": map[string]any{
						"type":        "string",
						"description": "Project ID",
					},
					"relative_path": map[string]any{
						"type":        "string",
						"description": "Relative path to file or directory",
					},
				},
				"required": []string{"target", "project", "relative_path"},
			},
		}, handler)
	case "git_status":
		addGatewayTool(server, &mcp.Tool{
			Name:        "git_status",
			Description: "Run 'git status --short' on authorized project repository",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"target": map[string]any{
						"type":        "string",
						"description": "Target ID",
					},
					"project": map[string]any{
						"type":        "string",
						"description": "Project ID",
					},
				},
				"required": []string{"target", "project"},
			},
		}, handler)
	case "health":
		addGatewayTool(server, &mcp.Tool{
			Name:        "health",
			Description: "Check gateway health, uptime, version, and target count",
			InputSchema: map[string]any{
				"type": "object",
			},
		}, handler)
	case "list_directory":
		addGatewayTool(server, &mcp.Tool{
			Name:        "list_directory",
			Description: "List directory contents under an authorized project",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"target": map[string]any{
						"type":        "string",
						"description": "Target ID",
					},
					"project": map[string]any{
						"type":        "string",
						"description": "Project ID",
					},
					"relative_path": map[string]any{
						"type":        "string",
						"description": "Relative path within project root (default: '.')",
					},
				},
				"required": []string{"target", "project"},
			},
		}, handler)
	case "list_targets":
		addGatewayTool(server, &mcp.Tool{
			Name:        "list_targets",
			Description: "Return safe list of configured targets without secrets",
			InputSchema: map[string]any{
				"type": "object",
			},
		}, handler)
	case "read_file":
		addGatewayTool(server, &mcp.Tool{
			Name:        "read_file",
			Description: "Read text file content safely within project boundaries",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"target": map[string]any{
						"type":        "string",
						"description": "Target ID",
					},
					"project": map[string]any{
						"type":        "string",
						"description": "Project ID",
					},
					"relative_path": map[string]any{
						"type":        "string",
						"description": "Relative path to text file",
					},
				},
				"required": []string{"target", "project", "relative_path"},
			},
		}, handler)
	case "run_task":
		addGatewayTool(server, &mcp.Tool{
			Name:        "run_task",
			Description: "Execute an allowlisted pre-configured task",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"target": map[string]any{
						"type":        "string",
						"description": "Target ID",
					},
					"project": map[string]any{
						"type":        "string",
						"description": "Project ID",
					},
					"task": map[string]any{
						"type":        "string",
						"description": "Pre-configured task name in project allowlist",
					},
				},
				"required": []string{"target", "project", "task"},
			},
		}, handler)
	case "target_status":
		addGatewayTool(server, &mcp.Tool{
			Name:        "target_status",
			Description: "Verify reachability and latency of a target machine",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"target": map[string]any{
						"type":        "string",
						"description": "Configured Target ID (e.g. termux-main)",
					},
				},
				"required": []string{"target"},
			},
		}, handler)
	case "write_file":
		addGatewayTool(server, &mcp.Tool{
			Name:        "write_file",
			Description: "Safely write or mutate a text file in an authorized project with atomic replacement and hash verification",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"target": map[string]any{
						"type":        "string",
						"description": "Target ID",
					},
					"project": map[string]any{
						"type":        "string",
						"description": "Project ID",
					},
					"relative_path": map[string]any{
						"type":        "string",
						"description": "Relative path to file within project root",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "UTF-8 text content to write",
					},
					"expected_sha256": map[string]any{
						"type":        "string",
						"description": "Expected SHA256 of existing file before overwrite (required for overwrite unless create=true)",
					},
					"dry_run": map[string]any{
						"type":        "boolean",
						"description": "If true, returns diff and sha256 without mutating the target file",
					},
					"create": map[string]any{
						"type":        "boolean",
						"description": "If true, creates a new file (fails if file already exists)",
					},
				},
				"required": []string{"target", "project", "relative_path", "content"},
			},
		}, handler)
	case "append_file":
		addGatewayTool(server, &mcp.Tool{
			Name:        "append_file",
			Description: "Append UTF-8 text content to an existing file in an authorized project",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"target": map[string]any{
						"type":        "string",
						"description": "Target ID",
					},
					"project": map[string]any{
						"type":        "string",
						"description": "Project ID",
					},
					"relative_path": map[string]any{
						"type":        "string",
						"description": "Relative path to file within project root",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "UTF-8 text content to append",
					},
				},
				"required": []string{"target", "project", "relative_path", "content"},
			},
		}, handler)
	case "delete_file":
		addGatewayTool(server, &mcp.Tool{
			Name:        "delete_file",
			Description: "Delete a file or empty directory within an authorized project",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"target": map[string]any{
						"type":        "string",
						"description": "Target ID",
					},
					"project": map[string]any{
						"type":        "string",
						"description": "Project ID",
					},
					"relative_path": map[string]any{
						"type":        "string",
						"description": "Relative path to file or empty directory within project root",
					},
				},
				"required": []string{"target", "project", "relative_path"},
			},
		}, handler)
	case "copy_file":
		addGatewayTool(server, &mcp.Tool{
			Name:        "copy_file",
			Description: "Copy a file within an authorized project",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"target": map[string]any{
						"type":        "string",
						"description": "Target ID",
					},
					"project": map[string]any{
						"type":        "string",
						"description": "Project ID",
					},
					"source_path": map[string]any{
						"type":        "string",
						"description": "Relative path to source file",
					},
					"dest_path": map[string]any{
						"type":        "string",
						"description": "Relative path to destination file",
					},
				},
				"required": []string{"target", "project", "source_path", "dest_path"},
			},
		}, handler)
	case "move_file":
		addGatewayTool(server, &mcp.Tool{
			Name:        "move_file",
			Description: "Move or rename a file within an authorized project",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"target": map[string]any{
						"type":        "string",
						"description": "Target ID",
					},
					"project": map[string]any{
						"type":        "string",
						"description": "Project ID",
					},
					"source_path": map[string]any{
						"type":        "string",
						"description": "Relative path to source file",
					},
					"dest_path": map[string]any{
						"type":        "string",
						"description": "Relative path to destination file",
					},
				},
				"required": []string{"target", "project", "source_path", "dest_path"},
			},
		}, handler)
	case "mkdir":
		addGatewayTool(server, &mcp.Tool{
			Name:        "mkdir",
			Description: "Create a directory within an authorized project",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"target": map[string]any{
						"type":        "string",
						"description": "Target ID",
					},
					"project": map[string]any{
						"type":        "string",
						"description": "Project ID",
					},
					"relative_path": map[string]any{
						"type":        "string",
						"description": "Relative path of directory to create",
					},
					"parents": map[string]any{
						"type":        "boolean",
						"description": "Create parent directories if needed (default: true)",
					},
				},
				"required": []string{"target", "project", "relative_path"},
			},
		}, handler)
	case "search":
		addGatewayTool(server, &mcp.Tool{
			Name:        "search",
			Description: "Search text or regular expression patterns within files in an authorized project",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"target": map[string]any{
						"type":        "string",
						"description": "Target ID",
					},
					"project": map[string]any{
						"type":        "string",
						"description": "Project ID",
					},
					"pattern": map[string]any{
						"type":        "string",
						"description": "Text pattern or regex to search for",
					},
					"relative_path": map[string]any{
						"type":        "string",
						"description": "Relative path to search within (default: '.')",
					},
					"is_regex": map[string]any{
						"type":        "boolean",
						"description": "Treat pattern as regex (default: false)",
					},
				},
				"required": []string{"target", "project", "pattern"},
			},
		}, handler)
	case "run_command":
		addGatewayTool(server, &mcp.Tool{
			Name:        "run_command",
			Description: "Execute a trusted Target shell command with explicit standard or required privilege intent",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"target": map[string]any{
						"type":        "string",
						"description": "Target ID (e.g. termux-main)",
					},
					"command": map[string]any{
						"type":        "string",
						"description": "Shell command line to execute",
					},
					"project": map[string]any{
						"type":        "string",
						"description": "Authorized project ID (defaults to active project)",
					},
					"cwd": map[string]any{
						"type":        "string",
						"description": "Working directory (defaults to project root)",
					},
					"timeout": map[string]any{
						"type":        "integer",
						"description": "Execution timeout in seconds",
					},
					"stdin": map[string]any{
						"type":        "string",
						"description": "Standard input string to supply to command",
					},
					"env": map[string]any{
						"type":        "object",
						"description": "Optional environment variables key-value map",
					},
					"privilege": map[string]any{
						"type":        "string",
						"enum":        []string{"standard", "required"},
						"description": "Privilege intent. Defaults to standard; required is governed by an explicit target_admin grant and the Target privilege policy.",
					},
				},
				"required": []string{"target", "command"},
			},
		}, handler)
	}
}

// RunStdio starts the MCP server over standard input/output.
func RunStdio(ctx context.Context, server *mcp.Server) error {
	return server.Run(ctx, &mcp.StdioTransport{})
}

// ValidateToken checks authentication tokens from either the dedicated internal
// header X-MCP-Gateway-Auth or the standard Authorization header.
func ValidateToken(r *http.Request, expectedToken string) (hasToken bool, isValid bool) {
	if expectedToken == "" {
		return false, false
	}
	// 1. Check dedicated internal header X-MCP-Gateway-Auth (set by tunnel-client via extra-headers)
	if gwAuth := r.Header.Get("X-MCP-Gateway-Auth"); gwAuth != "" {
		hasToken = true
		tok := strings.TrimSpace(gwAuth)
		if strings.HasPrefix(tok, "Bearer ") {
			tok = strings.TrimSpace(strings.TrimPrefix(tok, "Bearer "))
		}
		if subtle.ConstantTimeCompare([]byte(tok), []byte(expectedToken)) == 1 {
			return true, true
		}
		return true, false
	}
	// 2. Check standard Authorization header (Bearer <token> or direct token)
	if authHeader := r.Header.Get("Authorization"); authHeader != "" {
		hasToken = true
		tok := strings.TrimSpace(authHeader)
		if strings.HasPrefix(tok, "Bearer ") {
			tok = strings.TrimSpace(strings.TrimPrefix(tok, "Bearer "))
		}
		if subtle.ConstantTimeCompare([]byte(tok), []byte(expectedToken)) == 1 {
			return true, true
		}
		return true, false
	}
	return false, false
}

// RunHTTP starts the Streamable HTTP server on the specified bind address.
func RunHTTP(ctx context.Context, server *mcp.Server, bindAddr string, state *AdapterState, bridge *BridgeConfig) error {
	var anonServer *mcp.Server
	if bridge != nil && bridge.AuthToken != "" {
		anonBridge := *bridge
		anonBridge.ClientID = "NONE"
		anonServer = NewGatewayServer(&anonBridge, state)
	}

	handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		if bridge != nil && bridge.AuthToken != "" {
			_, isValid := ValidateToken(r, bridge.AuthToken)
			if isValid {
				return server
			}
			return anonServer
		}
		return server
	}, &mcp.StreamableHTTPOptions{
		Stateless:                    true,
		PropagateRequestCancellation: true,
		MaxRequestBodyBytes:          1048576,
	})

	mcpAuthHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claimedID := r.Header.Get("X-MCP-Client-ID")

		var hasToken, isValid bool
		if bridge != nil && bridge.AuthToken != "" {
			hasToken, isValid = ValidateToken(r, bridge.AuthToken)
		}

		// 1. Anti-Spoofing: Reject X-MCP-Client-ID without valid authentication
		if claimedID != "" {
			if !isValid {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]any{
					"error":   "CLIENT_ID_SPOOFING_DENIED",
					"message": "X-MCP-Client-ID cannot be asserted without valid authentication",
				})
				return
			}
			if bridge != nil && claimedID != bridge.ClientID {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]any{
					"error":   "CLIENT_ID_SPOOFING_DENIED",
					"message": fmt.Sprintf("Token does not authorize client ID %q", claimedID),
				})
				return
			}
		}

		// 2. If an authentication token was supplied but is invalid
		if hasToken && !isValid {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]any{
				"error":   "INVALID_AUTH_TOKEN",
				"message": "Authentication token is invalid",
			})
			return
		}

		handler.ServeHTTP(w, r)
	})

	mux := http.NewServeMux()
	mux.Handle("/mcp", mcpAuthHandler)

	// /live endpoint: only indicates process is alive, no DB, no SSH
	mux.HandleFunc("/live", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status":"alive"}`)
	})

	// /ready endpoint: checks Adapter, Bridge API, and Registry
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if state != nil && !state.IsReady() && bridge != nil {
			if info, err := bridge.CheckBridgeCompatibility(r.Context()); err == nil {
				state.SetReady(true, "ready", info)
			}
		}
		if state == nil || !state.IsReady() {
			w.WriteHeader(http.StatusServiceUnavailable)
			msg := "ADAPTER_NOT_READY"
			if state != nil {
				_, msg, _ = state.GetStatus()
			}
			resp, _ := json.Marshal(map[string]any{
				"status": "not_ready",
				"error":  msg,
			})
			w.Write(resp)
			return
		}
		_, _, info := state.GetStatus()
		w.WriteHeader(http.StatusOK)
		resp, _ := json.Marshal(map[string]any{
			"status":             "ready",
			"bridge_api_version": info.BridgeAPIVersion,
			"core_api_version":   info.CoreAPIVersion,
			"gateway_version":    info.GatewayVersion,
			"protocol":           info.MCPProtocol,
		})
		w.Write(resp)
	})

	// /health endpoint: comprehensive status summary without secrets
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		reqID := r.Header.Get("X-Request-ID")
		if reqID == "" {
			reqID = generateRequestID()
		}

		var coreHealth map[string]any
		if bridge != nil {
			outBytes, _, err := bridge.CallBridge(r.Context(), "health", nil, reqID)
			if err == nil {
				_ = json.Unmarshal(outBytes, &coreHealth)
			}
		}

		ready := false
		readyMsg := "uninitialized"
		var vInfo *BridgeVersionInfo
		if state != nil {
			ready, readyMsg, vInfo = state.GetStatus()
		}

		healthResp := map[string]any{
			"gateway":        "MCP-Pi",
			"ready":          ready,
			"adapter_status": readyMsg,
			"mcp": map[string]any{
				"sdk":       "go-sdk",
				"version":   "1.7.0",
				"protocol":  "2026-07-28",
				"stateless": true,
			},
			"version_info": vInfo,
			"core_health":  coreHealth,
		}
		w.WriteHeader(http.StatusOK)
		resp, _ := json.Marshal(healthResp)
		w.Write(resp)
	})

	// /server/discover: discovery endpoint for modern MCP clients
	mux.HandleFunc("/server/discover", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp, _ := json.Marshal(map[string]any{
			"server": map[string]any{
				"name":    "mcp-gateway-adapter",
				"version": adapterVersion(state),
			},
			"protocol": "2026-07-28",
			"capabilities": map[string]any{
				"tools": map[string]any{
					"listChanged": true,
				},
			},
			"endpoints": map[string]any{
				"mcp":      "/mcp",
				"live":     "/live",
				"ready":    "/ready",
				"health":   "/health",
				"discover": "/server/discover",
			},
		})
		w.Write(resp)
	})

	srv := &http.Server{
		Addr:           bindAddr,
		Handler:        SecurityMiddleware(mux),
		ReadTimeout:    60 * time.Second,
		WriteTimeout:   60 * time.Second,
		MaxHeaderBytes: 65536,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Printf("Starting MCP Streamable HTTP server on http://%s/mcp (stateless)", bindAddr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
