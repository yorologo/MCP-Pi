package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"mcp-gateway-adapter/internal/core"
	"mcp-gateway-adapter/internal/registry"
)

func getTestBridgeConfig() *BridgeConfig {
	pyPath, _ := filepath.Abs("../src")
	pyBin := "python3"
	if runtime.GOOS == "windows" {
		pyBin = "python"
	}
	dbPath, _ := filepath.Abs("../gateway.db")
	return &BridgeConfig{
		PythonBin:  pyBin,
		PythonPath: pyPath,
		DBPath:     dbPath,
		Timeout:    10 * time.Second,
	}
}

func getSeededClientBridgeConfig(t *testing.T, clientID, capability string, enabled bool) *BridgeConfig {
	t.Helper()
	bridge := getTestBridgeConfig()
	bridge.DBPath = filepath.Join(t.TempDir(), "gateway.db")
	enabledArg := "0"
	if enabled {
		enabledArg = "1"
	}
	seed := `
from mcp_gateway.registry import SQLiteRegistry
import sys
r = SQLiteRegistry(sys.argv[1])
client_id = sys.argv[2]
capability = sys.argv[3]
enabled = sys.argv[4] == "1"
r.add_client({"id": client_id, "display_name": client_id, "enabled": enabled})
if enabled and capability:
    r.add_grant({"client_id": client_id, "target_id": "*", "project_id": "*", "capability": capability, "enabled": True})
`
	cmd := exec.Command(bridge.PythonBin, "-c", seed, bridge.DBPath, clientID, capability, enabledArg)
	cmd.Env = bridge.buildEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("seed client registry failed: %v: %s", err, string(out))
	}
	bridge.ClientID = clientID
	return bridge
}

func TestServerToolDiscovery(t *testing.T) {
	ctx := context.Background()
	bridge := getTestBridgeConfig()
	state := NewAdapterState()
	state.SetReady(true, "ready", nil)
	server := NewGatewayServer(bridge, state)

	tServer, tClient := mcp.NewInMemoryTransports()
	go func() {
		_ = server.Run(ctx, tServer)
	}()

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test-client",
		Version: "1.0.0",
	}, nil)

	session, err := client.Connect(ctx, tClient, nil)
	if err != nil {
		t.Fatalf("client.Connect failed: %v", err)
	}
	defer session.Close()

	toolsList, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}

	expectedTools := []string{
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

	if len(toolsList.Tools) != len(expectedTools) {
		t.Fatalf("Expected %d tools, got %d", len(expectedTools), len(toolsList.Tools))
	}

	// Verify deterministic alphabetical ordering
	for i, expected := range expectedTools {
		if toolsList.Tools[i].Name != expected {
			t.Errorf("Tool at index %d expected %s, got %s", i, expected, toolsList.Tools[i].Name)
		}
	}
}

func TestRunCommandSchemaIncludesPrivilegeIntent(t *testing.T) {
	ctx := context.Background()
	bridge := getTestBridgeConfig()
	state := NewAdapterState()
	state.SetReady(true, "ready", nil)
	server := NewGatewayServer(bridge, state)

	tServer, tClient := mcp.NewInMemoryTransports()
	go server.Run(ctx, tServer)
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, tClient, nil)
	if err != nil {
		t.Fatalf("client.Connect failed: %v", err)
	}
	defer session.Close()

	toolsList, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}

	var runCommand *mcp.Tool
	for _, tool := range toolsList.Tools {
		if tool.Name == "run_command" {
			runCommand = tool
			break
		}
	}
	if runCommand == nil {
		t.Fatal("run_command tool missing")
	}
	raw, err := json.Marshal(runCommand.InputSchema)
	if err != nil {
		t.Fatalf("marshal run_command schema: %v", err)
	}
	schema := string(raw)
	if !strings.Contains(schema, "\"privilege\"") ||
		!strings.Contains(schema, "\"standard\"") ||
		!strings.Contains(schema, "\"required\"") {
		t.Fatalf("run_command privilege schema missing or incomplete: %s", schema)
	}
}

func TestServerHealthCall(t *testing.T) {
	ctx := context.Background()
	bridge := getTestBridgeConfig()
	state := NewAdapterState()
	state.SetReady(true, "ready", nil)
	server := NewGatewayServer(bridge, state)

	tServer, tClient := mcp.NewInMemoryTransports()
	go func() {
		_ = server.Run(ctx, tServer)
	}()

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test-client",
		Version: "1.0.0",
	}, nil)

	session, err := client.Connect(ctx, tClient, nil)
	if err != nil {
		t.Fatalf("client.Connect failed: %v", err)
	}
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "health",
	})
	if err != nil {
		t.Fatalf("session.CallTool health failed: %v", err)
	}

	if res.IsError {
		t.Fatalf("health returned isError=true: %+v", res)
	}

	if len(res.Content) == 0 {
		t.Fatalf("health returned no content")
	}

	textContent, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}

	var jsonOut map[string]any
	if err := json.Unmarshal([]byte(textContent.Text), &jsonOut); err != nil {
		t.Fatalf("failed to parse JSON from health: %v", err)
	}

	if jsonOut["ok"] != true {
		t.Errorf("expected ok=true, got %v", jsonOut["ok"])
	}
}

func TestFailClosedWhenNotReady(t *testing.T) {
	ctx := context.Background()
	bridge := getTestBridgeConfig()
	state := NewAdapterState()
	state.SetReady(false, "ADAPTER_NOT_READY: simulated incompatibility", nil)
	server := NewGatewayServer(bridge, state)

	tServer, tClient := mcp.NewInMemoryTransports()
	go func() {
		_ = server.Run(ctx, tServer)
	}()

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test-client",
		Version: "1.0.0",
	}, nil)

	session, err := client.Connect(ctx, tClient, nil)
	if err != nil {
		t.Fatalf("client.Connect failed: %v", err)
	}
	defer session.Close()

	// Calling tool when adapter is not ready must fail closed
	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "health",
	})
	if err != nil {
		t.Fatalf("unexpected protocol err: %v", err)
	}

	if !res.IsError {
		t.Fatalf("expected isError=true when adapter not ready, got false")
	}

	textContent := res.Content[0].(*mcp.TextContent)
	if !strings.Contains(textContent.Text, "ADAPTER_NOT_READY") {
		t.Errorf("expected ADAPTER_NOT_READY in response, got: %s", textContent.Text)
	}
}

func TestAdapterVersionUsesBridgeState(t *testing.T) {
	state := NewAdapterState()
	if got := adapterVersion(state); got != "unknown" {
		t.Fatalf("expected unknown before compatibility is known, got %q", got)
	}
	state.SetReady(true, "ready", &BridgeVersionInfo{GatewayVersion: "9.8.7"})
	if got := adapterVersion(state); got != "9.8.7" {
		t.Fatalf("expected bridge Gateway version, got %q", got)
	}
}

func TestHealthEndpoints(t *testing.T) {
	bridge := getTestBridgeConfig()
	state := NewAdapterState()
	state.SetReady(true, "ready", &BridgeVersionInfo{
		BridgeAPIVersion: 1,
		CoreAPIVersion:   1,
		GatewayVersion:   "1.3.3",
		MCPProtocol:      "2026-07-28",
	})
	server := NewGatewayServer(bridge, state)

	handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return server
	}, &mcp.StreamableHTTPOptions{
		Stateless: true,
	})

	mux := http.NewServeMux()
	mux.Handle("/mcp", handler)
	mux.HandleFunc("/live", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status":"alive"}`)
	})
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !state.IsReady() {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintf(w, `{"status":"not_ready"}`)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status":"ready"}`)
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"gateway":"MCP-Pi","status":"ok"}`)
	})
	mux.HandleFunc("/server/discover", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"server":{"name":"mcp-gateway-adapter","version":"1.3.3"},"protocol":"2026-07-28"}`)
	})

	ts := httptest.NewServer(SecurityMiddleware(mux))
	defer ts.Close()

	// 1. /live
	reqLive, _ := http.NewRequest("GET", ts.URL+"/live", nil)
	reqLive.Host = "127.0.0.1"
	respLive, err := http.DefaultClient.Do(reqLive)
	if err != nil || respLive.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for /live, got %v (err: %v)", respLive.StatusCode, err)
	}

	// 2. /ready when ready
	reqReady, _ := http.NewRequest("GET", ts.URL+"/ready", nil)
	reqReady.Host = "localhost"
	respReady, err := http.DefaultClient.Do(reqReady)
	if err != nil || respReady.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for /ready, got %v (err: %v)", respReady.StatusCode, err)
	}

	// 3. /ready when NOT ready -> 503
	state.SetReady(false, "not ready", nil)
	reqNotReady, _ := http.NewRequest("GET", ts.URL+"/ready", nil)
	reqNotReady.Host = "localhost"
	respNotReady, err := http.DefaultClient.Do(reqNotReady)
	if err != nil || respNotReady.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected 503 for /ready when not ready, got %v", respNotReady.StatusCode)
	}

	// 4. /health
	reqHealth, _ := http.NewRequest("GET", ts.URL+"/health", nil)
	reqHealth.Host = "127.0.0.1"
	respHealth, err := http.DefaultClient.Do(reqHealth)
	if err != nil || respHealth.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for /health, got %v", respHealth.StatusCode)
	}

	// 5. /server/discover
	reqDiscover, _ := http.NewRequest("GET", ts.URL+"/server/discover", nil)
	reqDiscover.Host = "127.0.0.1"
	respDiscover, err := http.DefaultClient.Do(reqDiscover)
	if err != nil || respDiscover.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for /server/discover, got %v", respDiscover.StatusCode)
	}
}

func TestHostAndOriginSecurity(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/live", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	secured := SecurityMiddleware(mux)

	// Case 1: Valid localhost Host -> 200
	req1, _ := http.NewRequest("GET", "/live", nil)
	req1.Host = "localhost:8090"
	rr1 := httptest.NewRecorder()
	secured.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusOK {
		t.Errorf("expected 200 for localhost Host, got %d", rr1.Code)
	}

	// Case 2: Valid 127.0.0.1 Host -> 200
	req2, _ := http.NewRequest("GET", "/live", nil)
	req2.Host = "127.0.0.1:8090"
	rr2 := httptest.NewRecorder()
	secured.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Errorf("expected 200 for 127.0.0.1 Host, got %d", rr2.Code)
	}

	// Case 3: Malicious Host header (DNS rebinding simulation) -> 403
	req3, _ := http.NewRequest("GET", "/live", nil)
	req3.Host = "attacker.com:8090"
	rr3 := httptest.NewRecorder()
	secured.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusForbidden {
		t.Errorf("expected 403 for attacker.com Host, got %d", rr3.Code)
	}

	// Case 4: Valid Origin (http://localhost:3000) -> 200
	req4, _ := http.NewRequest("GET", "/live", nil)
	req4.Host = "127.0.0.1:8090"
	req4.Header.Set("Origin", "http://localhost:3000")
	rr4 := httptest.NewRecorder()
	secured.ServeHTTP(rr4, req4)
	if rr4.Code != http.StatusOK {
		t.Errorf("expected 200 for localhost Origin, got %d", rr4.Code)
	}

	// Case 5: Malicious Origin (http://evil.com) -> 403
	req5, _ := http.NewRequest("GET", "/live", nil)
	req5.Host = "127.0.0.1:8090"
	req5.Header.Set("Origin", "http://evil.com")
	rr5 := httptest.NewRecorder()
	secured.ServeHTTP(rr5, req5)
	if rr5.Code != http.StatusForbidden {
		t.Errorf("expected 403 for evil.com Origin, got %d", rr5.Code)
	}

	// Case 6: Request body size limit (> 1 MiB rejected)
	bigBody := bytes.Repeat([]byte("a"), 1048576+100)
	req6, _ := http.NewRequest("POST", "/live", bytes.NewReader(bigBody))
	req6.Host = "127.0.0.1:8090"
	rr6 := httptest.NewRecorder()
	handlerWithRead := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Body too large", http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	SecurityMiddleware(handlerWithRead).ServeHTTP(rr6, req6)
	if rr6.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413 Request Entity Too Large for oversized body, got %d", rr6.Code)
	}
}

func TestStatelessMCPNegativeSessions(t *testing.T) {
	bridge := getTestBridgeConfig()
	state := NewAdapterState()
	state.SetReady(true, "ready", nil)
	server := NewGatewayServer(bridge, state)

	handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return server
	}, &mcp.StreamableHTTPOptions{
		Stateless: true,
	})

	ts := httptest.NewServer(handler)
	defer ts.Close()

	// In stateless mode, GET /mcp must return 405 Method Not Allowed
	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatalf("GET /mcp failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for GET in stateless mode, got %d", resp.StatusCode)
	}
}

func TestClientToolFiltering(t *testing.T) {
	ctx := context.Background()
	bridge := getTestBridgeConfig()
	bridge.ClientID = "NONE" // Deny all anonymous tools
	state := NewAdapterState()
	state.SetReady(true, "ready", nil)
	server := NewGatewayServer(bridge, state)

	tServer, tClient := mcp.NewInMemoryTransports()
	go func() {
		_ = server.Run(ctx, tServer)
	}()

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test-client",
		Version: "1.0.0",
	}, nil)

	session, err := client.Connect(ctx, tClient, nil)
	if err != nil {
		t.Fatalf("client.Connect failed: %v", err)
	}
	defer session.Close()

	toolsList, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}

	if len(toolsList.Tools) != 0 {
		t.Fatalf("Expected 0 tools for client NONE, got %d", len(toolsList.Tools))
	}
}

func TestClientAwareToolsListAndCall(t *testing.T) {
	ctx := context.Background()
	bridge := getSeededClientBridgeConfig(t, "claude-desktop", "read", true) // Read-only client profile
	state := NewAdapterState()
	state.SetReady(true, "ready", nil)
	server := NewGatewayServer(bridge, state)

	tServer, tClient := mcp.NewInMemoryTransports()
	go func() {
		_ = server.Run(ctx, tServer)
	}()

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "claude-desktop-test",
		Version: "1.0.0",
	}, nil)

	session, err := client.Connect(ctx, tClient, nil)
	if err != nil {
		t.Fatalf("client.Connect failed: %v", err)
	}
	defer session.Close()

	toolsList, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}

	toolNames := make(map[string]bool)
	for _, tool := range toolsList.Tools {
		toolNames[tool.Name] = true
	}

	// claude-desktop must NOT have run_task or write_file
	if toolNames["write_file"] {
		t.Errorf("write_file should not be present for claude-desktop")
	}
	if toolNames["run_task"] {
		t.Errorf("run_task should not be present for claude-desktop")
	}
	if !toolNames["health"] {
		t.Errorf("health should be present for claude-desktop")
	}

	// Call health tool - should succeed
	callResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "health",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool health failed: %v", err)
	}
	if callResult.IsError {
		t.Errorf("Expected health call not to be an error")
	}
}

func TestDisabledClientGo(t *testing.T) {
	ctx := context.Background()
	bridge := getSeededClientBridgeConfig(t, "disabled-test-client", "", false)
	state := NewAdapterState()
	state.SetReady(true, "ready", nil)
	server := NewGatewayServer(bridge, state)

	tServer, tClient := mcp.NewInMemoryTransports()
	go func() {
		_ = server.Run(ctx, tServer)
	}()

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "disabled-test",
		Version: "1.0.0",
	}, nil)

	session, err := client.Connect(ctx, tClient, nil)
	if err != nil {
		t.Fatalf("client.Connect failed: %v", err)
	}
	defer session.Close()

	toolsList, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}

	if len(toolsList.Tools) != 0 {
		t.Fatalf("Expected 0 tools for disabled client, got %d", len(toolsList.Tools))
	}
}

func TestStdioTransportSimulation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tServer, tClient := mcp.NewInMemoryTransports()

	bridge := getSeededClientBridgeConfig(t, "gemini-main", "read", true)
	state := NewAdapterState()
	state.SetReady(true, "ready", nil)
	server := NewGatewayServer(bridge, state)

	go func() {
		_ = server.Run(ctx, tServer)
	}()

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "stdio-sim-client",
		Version: "1.0.0",
	}, nil)

	session, err := client.Connect(ctx, tClient, nil)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer session.Close()

	toolsList, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}

	if len(toolsList.Tools) == 0 {
		t.Fatalf("Expected tools for gemini-main over simulated stdio transport")
	}
}

func TestTokenAuthenticationAndAntiSpoofing(t *testing.T) {
	bridge := getSeededClientBridgeConfig(t, "chatgpt-main", "read", true)
	bridge.AuthToken = "secret-token-xyz-12345"
	state := NewAdapterState()
	state.SetReady(true, "ready", nil)
	server := NewGatewayServer(bridge, state)

	var anonServer *mcp.Server
	anonBridge := *bridge
	anonBridge.ClientID = "NONE"
	anonServer = NewGatewayServer(&anonBridge, state)

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
		Stateless: true,
	})

	mcpAuthHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claimedID := r.Header.Get("X-MCP-Client-ID")

		var hasToken, isValid bool
		if bridge != nil && bridge.AuthToken != "" {
			hasToken, isValid = ValidateToken(r, bridge.AuthToken)
		}

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
	ts := httptest.NewServer(SecurityMiddleware(mux))
	defer ts.Close()

	// 1. Negative Test: Client ID spoofing attempt without token -> MUST BE 401 CLIENT_ID_SPOOFING_DENIED
	reqSpoof, _ := http.NewRequest("POST", ts.URL+"/mcp", bytes.NewReader([]byte("{}")))
	reqSpoof.Host = "127.0.0.1"
	reqSpoof.Header.Set("X-MCP-Client-ID", "chatgpt-main")
	respSpoof, err := http.DefaultClient.Do(reqSpoof)
	if err != nil {
		t.Fatalf("Spoof request failed: %v", err)
	}
	defer respSpoof.Body.Close()
	if respSpoof.StatusCode != http.StatusUnauthorized {
		t.Errorf("Expected 401 for client ID spoofing, got %d", respSpoof.StatusCode)
	}
	bodySpoof, _ := io.ReadAll(respSpoof.Body)
	if !strings.Contains(string(bodySpoof), "CLIENT_ID_SPOOFING_DENIED") {
		t.Errorf("Expected CLIENT_ID_SPOOFING_DENIED error in body, got: %s", string(bodySpoof))
	}

	// 2. Negative Test: Invalid Bearer token -> MUST BE 401 INVALID_AUTH_TOKEN
	reqBadToken, _ := http.NewRequest("POST", ts.URL+"/mcp", bytes.NewReader([]byte("{}")))
	reqBadToken.Host = "127.0.0.1"
	reqBadToken.Header.Set("Authorization", "Bearer wrong-token-999")
	respBadToken, err := http.DefaultClient.Do(reqBadToken)
	if err != nil {
		t.Fatalf("Bad token request failed: %v", err)
	}
	defer respBadToken.Body.Close()
	if respBadToken.StatusCode != http.StatusUnauthorized {
		t.Errorf("Expected 401 for bad token, got %d", respBadToken.StatusCode)
	}
	bodyBadToken, _ := io.ReadAll(respBadToken.Body)
	if !strings.Contains(string(bodyBadToken), "INVALID_AUTH_TOKEN") {
		t.Errorf("Expected INVALID_AUTH_TOKEN error in body, got: %s", string(bodyBadToken))
	}

	// 3. Negative Test: Invalid X-MCP-Gateway-Auth token -> MUST BE 401 INVALID_AUTH_TOKEN
	reqBadGW, _ := http.NewRequest("POST", ts.URL+"/mcp", bytes.NewReader([]byte("{}")))
	reqBadGW.Host = "127.0.0.1"
	reqBadGW.Header.Set("X-MCP-Gateway-Auth", "wrong-secret-token")
	respBadGW, err := http.DefaultClient.Do(reqBadGW)
	if err != nil {
		t.Fatalf("Bad GW token request failed: %v", err)
	}
	defer respBadGW.Body.Close()
	if respBadGW.StatusCode != http.StatusUnauthorized {
		t.Errorf("Expected 401 for bad gateway auth token, got %d", respBadGW.StatusCode)
	}
	bodyBadGW, _ := io.ReadAll(respBadGW.Body)
	if !strings.Contains(string(bodyBadGW), "INVALID_AUTH_TOKEN") {
		t.Errorf("Expected INVALID_AUTH_TOKEN error in body, got: %s", string(bodyBadGW))
	}

	// 4. Negative Test: Valid token but asserting unauthorized client ID -> MUST BE 401 CLIENT_ID_SPOOFING_DENIED
	reqMismatch, _ := http.NewRequest("POST", ts.URL+"/mcp", bytes.NewReader([]byte("{}")))
	reqMismatch.Host = "127.0.0.1"
	reqMismatch.Header.Set("X-MCP-Gateway-Auth", "secret-token-xyz-12345")
	reqMismatch.Header.Set("X-MCP-Client-ID", "admin")
	respMismatch, err := http.DefaultClient.Do(reqMismatch)
	if err != nil {
		t.Fatalf("Mismatch request failed: %v", err)
	}
	defer respMismatch.Body.Close()
	if respMismatch.StatusCode != http.StatusUnauthorized {
		t.Errorf("Expected 401 for mismatched client ID, got %d", respMismatch.StatusCode)
	}

	// 5. Positive Test: Dedicated header X-MCP-Gateway-Auth reaches MCP Streamable handler
	reqValidGW, _ := http.NewRequest("GET", ts.URL+"/mcp", nil)
	reqValidGW.Host = "127.0.0.1"
	reqValidGW.Header.Set("X-MCP-Gateway-Auth", "secret-token-xyz-12345")
	respValidGW, err := http.DefaultClient.Do(reqValidGW)
	if err != nil {
		t.Fatalf("Valid GW token request failed: %v", err)
	}
	defer respValidGW.Body.Close()
	if respValidGW.StatusCode == http.StatusUnauthorized {
		t.Errorf("Valid GW token must not be unauthorized, got 401")
	}
	if respValidGW.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("Expected 405 Method Not Allowed from MCP handler, got %d", respValidGW.StatusCode)
	}

	// 6. Positive Test: Dedicated header X-MCP-Gateway-Auth coexists with remote connector Authorization header
	reqCoexist, _ := http.NewRequest("GET", ts.URL+"/mcp", nil)
	reqCoexist.Host = "127.0.0.1"
	reqCoexist.Header.Set("X-MCP-Gateway-Auth", "secret-token-xyz-12345")
	reqCoexist.Header.Set("Authorization", "Bearer remote-connector-user-token")
	respCoexist, err := http.DefaultClient.Do(reqCoexist)
	if err != nil {
		t.Fatalf("Coexist token request failed: %v", err)
	}
	defer respCoexist.Body.Close()
	if respCoexist.StatusCode == http.StatusUnauthorized {
		t.Errorf("Valid GW token with remote Authorization header must not be unauthorized, got 401")
	}
	if respCoexist.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("Expected 405 Method Not Allowed from MCP handler, got %d", respCoexist.StatusCode)
	}

}

func TestToolAnnotationsRiskHints(t *testing.T) {
	read := toolAnnotations("read_file")
	if read == nil || !read.ReadOnlyHint || read.DestructiveHint == nil || *read.DestructiveHint {
		t.Fatalf("read_file annotations are not conservative read-only hints: %+v", read)
	}
	shell := toolAnnotations("run_command")
	if shell == nil || shell.ReadOnlyHint || shell.DestructiveHint == nil || !*shell.DestructiveHint || shell.OpenWorldHint == nil || !*shell.OpenWorldHint {
		t.Fatalf("run_command must be marked high-risk/open-world: %+v", shell)
	}
}

func TestToolSetEquality(t *testing.T) {
	a := map[string]bool{"health": true, "read_file": true}
	b := map[string]bool{"read_file": true, "health": true}
	if !toolSetsEqual(a, b) {
		t.Fatal("equivalent tool sets should compare equal")
	}
	b["run_command"] = true
	if toolSetsEqual(a, b) {
		t.Fatal("changed tool set should compare different")
	}
}

func TestInProcessCoreExecution(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "gateway.db")
	db, err := registry.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	store := registry.NewStore(db)
	coreInstance := core.New(store, core.Config{
		GatewayVersion:     "1.4.0",
		CoreAPIVersion:     1,
		ToolCatalogVersion: 4,
		MCPProtocol:        "2026-07-28",
		DBPath:             dbPath,
	})

	bridge := &BridgeConfig{
		Core:      coreInstance,
		DBPath:    dbPath,
		ClientID:  "local",
		Timeout:   5 * time.Second,
		PythonBin: "/invalid/nonexistent/python", // Proves python is NOT invoked!
	}

	state := NewAdapterState()
	info, err := bridge.CheckBridgeCompatibility(ctx)
	if err != nil {
		t.Fatalf("in-process compatibility check failed: %v", err)
	}
	if info.GatewayVersion != "1.4.0" || !info.OK {
		t.Fatalf("unexpected version info: %+v", info)
	}
	state.SetReady(true, "ready", info)

	server := NewGatewayServer(bridge, state)
	if server == nil {
		t.Fatal("failed to initialize gateway server")
	}

	// Invoke health through CallBridge in-process
	outBytes, isErr, err := bridge.CallBridge(ctx, "health", []byte("{}"), "req-health-test")
	if err != nil || isErr {
		t.Fatalf("in-process health call failed: err=%v, isErr=%v, out=%s", err, isErr, string(outBytes))
	}

	var res map[string]any
	if err := json.Unmarshal(outBytes, &res); err != nil {
		t.Fatalf("failed to parse result json: %v", err)
	}
	if res["ok"] != true || res["tool"] != "health" {
		t.Fatalf("unexpected health response: %+v", res)
	}
}
