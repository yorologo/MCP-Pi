package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func invokeError(t *testing.T, response map[string]any) (string, string) {
	t.Helper()
	raw, ok := response["error"].(map[string]any)
	if !ok {
		t.Fatalf("response has no error: %#v", response)
	}
	code, _ := raw["code"].(string)
	message, _ := raw["message"].(string)
	return code, message
}

func TestInvokeFailsClosedForUnknownAnonymousAndNoGrant(t *testing.T) {
	c, _, db := seededRemoteCore(t)
	ctx := context.Background()

	unknown := c.Invoke(ctx, Invocation{ClientID: "local"}, "no_such_tool", nil)
	code, message := invokeError(t, unknown)
	if code != "TOOL_NOT_ALLOWED" || message != "Tool 'no_such_tool' is not in the allowlisted gateway tools" {
		t.Fatalf("unknown=%#v", unknown)
	}

	anonymous := c.Invoke(ctx, Invocation{ClientID: "NONE"}, "read_file", map[string]any{
		"target": "t", "project": "p", "relative_path": "a.txt",
	})
	code, message = invokeError(t, anonymous)
	if code != "TOOL_NOT_ALLOWED" || !strings.HasPrefix(message, "ANONYMOUS_CLIENT_DENIED:") {
		t.Fatalf("anonymous=%#v", anonymous)
	}

	if _, err := db.Exec("INSERT INTO ai_clients(id,display_name,enabled) VALUES ('nogrant','No Grant',1)"); err != nil {
		t.Fatal(err)
	}
	noGrant := c.Invoke(ctx, Invocation{ClientID: "nogrant"}, "read_file", map[string]any{
		"target": "t", "project": "p", "relative_path": "a.txt",
	})
	code, message = invokeError(t, noGrant)
	if code != "TOOL_NOT_ALLOWED" || !strings.HasPrefix(message, "TOOL_NOT_ALLOWED: Client 'nogrant' has no active grants") {
		t.Fatalf("noGrant=%#v", noGrant)
	}
}

func TestInvokeAuthorizationPrecedesArgumentValidation(t *testing.T) {
	c, _, db := seededRemoteCore(t)
	ctx := context.Background()
	if _, err := db.Exec("INSERT INTO ai_clients(id,display_name,enabled) VALUES ('nogrant','No Grant',1)"); err != nil {
		t.Fatal(err)
	}

	noGrant := c.Invoke(ctx, Invocation{ClientID: "nogrant"}, "file_stat", map[string]any{
		"target": "t", "project": "p",
	})
	code, _ := invokeError(t, noGrant)
	if code != "TOOL_NOT_ALLOWED" {
		t.Fatalf("authorization must precede args: %#v", noGrant)
	}

	authorized := c.Invoke(ctx, Invocation{ClientID: "client"}, "file_stat", map[string]any{
		"target": "t", "project": "p",
	})
	code, message := invokeError(t, authorized)
	if code != "INVALID_ARGUMENTS" ||
		message != "Missing or invalid required arguments: 'target', 'project', and 'relative_path' are required" {
		t.Fatalf("authorized invalid args=%#v", authorized)
	}
	if _, exists := authorized["duration_ms"]; exists {
		t.Fatalf("bridge-level INVALID_ARGUMENTS must not include duration_ms: %#v", authorized)
	}
}

func TestInvokePreservesBridgeAndOperationalKillSwitchPrecedence(t *testing.T) {
	c, _, _ := seededRemoteCore(t)
	ctx := context.Background()
	if err := c.store.SetSetting(ctx, "gateway_enabled", "false"); err != nil {
		t.Fatal(err)
	}

	nonLocal := c.Invoke(ctx, Invocation{ClientID: "client"}, "read_file", map[string]any{
		"target": "t", "project": "p", "relative_path": "a.txt",
	})
	code, message := invokeError(t, nonLocal)
	if code != "TOOL_NOT_ALLOWED" || message != "GATEWAY_DISABLED: Global kill switch is active" {
		t.Fatalf("nonlocal disabled=%#v", nonLocal)
	}

	local := c.Invoke(ctx, Invocation{ClientID: "local"}, "read_file", map[string]any{
		"target": "t", "project": "p", "relative_path": "a.txt",
	})
	code, message = invokeError(t, local)
	if code != "GATEWAY_DISABLED" || message != "Gateway operations are disabled by administrator kill switch" {
		t.Fatalf("local disabled=%#v", local)
	}

	health := c.Invoke(ctx, Invocation{ClientID: "local"}, "health", nil)
	if health["ok"] != true {
		t.Fatalf("health should remain available: %#v", health)
	}
	result := health["result"].(map[string]any)
	if result["gateway_status"] != "disabled" {
		t.Fatalf("health status=%#v", result)
	}

	targets := c.Invoke(ctx, Invocation{ClientID: "local"}, "list_targets", nil)
	if targets["ok"] != true {
		t.Fatalf("list_targets should preserve current trusted behavior: %#v", targets)
	}
}

func TestInvokeTargetStatusDispatchAndArgumentValidation(t *testing.T) {
	c, _, _ := seededRemoteCore(t)
	ctx := context.Background()

	got := c.Invoke(ctx, Invocation{ClientID: "client"}, "target_status", map[string]any{"target": "t"})
	if got["ok"] != true {
		t.Fatalf("target_status invoke=%#v", got)
	}
	result, ok := got["result"].(map[string]any)
	if !ok || result["reachable"] != true || result["remote_hostname"] != "remote-box" {
		t.Fatalf("target_status result=%#v", got["result"])
	}

	missing := c.Invoke(ctx, Invocation{ClientID: "client"}, "target_status", map[string]any{})
	code, message := invokeError(t, missing)
	if code != "INVALID_ARGUMENTS" || message != "Missing or invalid required argument 'target'" {
		t.Fatalf("target_status invalid args=%#v", missing)
	}
}

func TestInvokeSearchRecordsBestEffortAudit(t *testing.T) {
	c, _, db := seededRemoteCore(t)
	ctx := context.Background()

	response := c.Invoke(ctx, Invocation{ClientID: "client", RequestID: "req-search"}, "search", map[string]any{
		"target": "t", "project": "p", "pattern": "needle", "relative_path": "sub",
	})
	if response["ok"] != true {
		t.Fatalf("search=%#v", response)
	}

	var actor, action, targetID, projectID, detail string
	var success int
	if err := db.QueryRow(
		"SELECT actor,action,target_id,project_id,success,detail FROM activity WHERE action='SEARCH' ORDER BY id DESC LIMIT 1",
	).Scan(&actor, &action, &targetID, &projectID, &success, &detail); err != nil {
		t.Fatal(err)
	}
	if actor != "client" || action != "SEARCH" || targetID != "t" || projectID != "p" || success != 1 {
		t.Fatalf("audit actor=%q action=%q target=%q project=%q success=%d", actor, action, targetID, projectID, success)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(detail), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["pattern"] != "needle" || decoded["count"] != float64(2) || decoded["request_id"] != "req-search" {
		t.Fatalf("audit detail=%#v", decoded)
	}
}

func TestInvokeUnportedToolFailsClosed(t *testing.T) {
	c, _, _ := seededRemoteCore(t)
	response := c.Invoke(context.Background(), Invocation{ClientID: "local"}, "write_file", map[string]any{
		"target": "t", "project": "p", "relative_path": "x", "content": "x",
	})
	code, _ := invokeError(t, response)
	if code != "TOOL_NOT_IMPLEMENTED" {
		t.Fatalf("unported tool=%#v", response)
	}
}
