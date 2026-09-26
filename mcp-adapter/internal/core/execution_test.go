package core

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"mcp-gateway-adapter/internal/registry"
	"mcp-gateway-adapter/internal/remote"
)

type executionFakeRemote struct {
	lastCommand string
	lastCWD     string
	lastEnv     map[string]string
	facts       map[string]any
	factsErr    error
	runErr      error
	runResult   remote.CommandResult
}

func (f *executionFakeRemote) RunCommand(_ context.Context, _ registry.Target, command string, options remote.CommandOptions) (remote.CommandResult, error) {
	f.lastCommand = command
	f.lastCWD = options.CWD
	f.lastEnv = options.Env
	if f.runErr != nil {
		return remote.CommandResult{}, f.runErr
	}
	return f.runResult, nil
}

func (f *executionFakeRemote) RunPython(_ context.Context, _ registry.Target, _ string, _ []string, _ []byte, _ time.Duration) (remote.CommandResult, error) {
	return remote.CommandResult{ExitCode: 0}, nil
}

func (f *executionFakeRemote) ProbeFacts(_ context.Context, _ registry.Target, _ bool, _ time.Duration) (map[string]any, error) {
	if f.factsErr != nil {
		return nil, f.factsErr
	}
	if f.facts != nil {
		return f.facts, nil
	}
	return map[string]any{
		"probe_status": "ok",
		"boot_id":      "boot-test-1",
		"privilege": map[string]any{
			"current_level":              "standard",
			"maximum_level":              "root",
			"backend":                    "shizuku",
			"backend_ready":              true,
			"transport_already_elevated": false,
			"shell_can_elevate":          false,
			"independent_elevator":       nil,
		},
	}, nil
}

func (f *executionFakeRemote) ResolveCanonicalPath(_ context.Context, _ registry.Target, candidatePath string, _ time.Duration) (string, error) {
	return candidatePath, nil
}

func (f *executionFakeRemote) ListDirectory(_ context.Context, _ registry.Target, _ string, _ int, _ time.Duration) ([]remote.DirectoryEntry, error) {
	return nil, nil
}

func (f *executionFakeRemote) FileStat(_ context.Context, _ registry.Target, _ string, _ time.Duration) (remote.FileStat, error) {
	return remote.FileStat{Exists: true}, nil
}

func (f *executionFakeRemote) ReadFile(_ context.Context, _ registry.Target, _ string, _ int64, _ time.Duration) (string, error) {
	return "", nil
}

func (f *executionFakeRemote) GitStatus(_ context.Context, _ registry.Target, _ string, _ time.Duration) (string, error) {
	return "", nil
}

func (f *executionFakeRemote) Search(_ context.Context, _ registry.Target, _, _, _ string, _ bool, _ int, _ time.Duration) ([]remote.SearchMatch, error) {
	return nil, nil
}

func (f *executionFakeRemote) ProbePath(_ context.Context, _ registry.Target, candidate string, _ time.Duration) (remote.PathProbe, error) {
	return remote.PathProbe{Exists: true, CanonicalPath: candidate}, nil
}

func (f *executionFakeRemote) ResolveSafeDestination(_ context.Context, _ registry.Target, root, candidate string, _ bool, _ time.Duration) (remote.SafeDestination, error) {
	return remote.SafeDestination{DestinationPath: candidate, CanonicalPath: candidate}, nil
}

func (f *executionFakeRemote) WriteFileAtomic(_ context.Context, _ registry.Target, _ string, _ []byte, _ bool, _ string, _ int, _ time.Duration) (remote.AtomicWriteResult, error) {
	return remote.AtomicWriteResult{Created: true}, nil
}

func seededExecutionCore(t *testing.T) (*Core, *executionFakeRemote, context.Context) {
	t.Helper()
	ctx := context.Background()
	db, err := registry.Open(ctx, filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}

	stmts := []string{
		`INSERT OR REPLACE INTO settings(key, value) VALUES ('gateway_enabled', 'true'), ('shell_enabled', 'true')`,
		`INSERT INTO targets(id, display_name, platform, host, port, user, privilege_policy, enabled)
		 VALUES ('termux-main', 'Termux', 'android', '127.0.0.1', 8022, 'u0_a123', 'never', 1)`,
		`INSERT INTO targets(id, display_name, platform, host, port, user, privilege_policy, enabled)
		 VALUES ('target-elevated', 'Elevated Target', 'android', '127.0.0.1', 8022, 'u0_a123', 'ask_always', 1)`,
		`INSERT INTO projects(id, target_id, display_name, root, read_enabled, write_enabled, enabled)
		 VALUES ('MCP_Local', 'termux-main', 'Local Proj', '/data/local/proj', 1, 1, 1)`,
		`INSERT INTO projects(id, target_id, display_name, root, read_enabled, write_enabled, enabled)
		 VALUES ('MCP_Local', 'target-elevated', 'Elevated Proj', '/data/local/proj', 1, 1, 1)`,
		`INSERT INTO project_tasks(target_id, project_id, task_name, argv_json, timeout, enabled)
		 VALUES ('termux-main', 'MCP_Local', 'build', '["go","build","./..."]', 45, 1)`,
		`INSERT INTO ai_clients(id, display_name, enabled) VALUES ('test-client', 'Test Client', 1)`,
		`INSERT INTO grants(client_id, target_id, project_id, capability, enabled)
		 VALUES ('test-client', 'termux-main', 'MCP_Local', 'target_shell', 1)`,
		`INSERT INTO grants(client_id, target_id, project_id, capability, enabled)
		 VALUES ('test-client', 'target-elevated', 'MCP_Local', 'target_shell', 1)`,
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			t.Fatalf("seed db failed: %v", err)
		}
	}

	store := registry.NewStore(db)
	fake := &executionFakeRemote{
		runResult: remote.CommandResult{ExitCode: 0, Stdout: "executed\n", DurationMS: 20},
	}
	core := NewWithRemote(store, Config{
		GatewayVersion:     "1.4.0",
		CoreAPIVersion:     1,
		ToolCatalogVersion: 4,
		MCPProtocol:        "2026-07-28",
	}, fake)

	t.Cleanup(func() { _ = db.Close() })
	return core, fake, ctx
}

func TestRunCommandBasics(t *testing.T) {
	core, fake, ctx := seededExecutionCore(t)

	// Empty command fails
	resp := core.RunCommand(ctx, "req-1", "test-client", "termux-main", "MCP_Local", "", CommandRequest{})
	if resp.OK || resp.Error.Code != "INVALID_ARGUMENTS" {
		t.Fatalf("expected INVALID_ARGUMENTS, got: %+v", resp)
	}

	// Successful standard run
	resp = core.RunCommand(ctx, "req-2", "test-client", "termux-main", "MCP_Local", "ls -la", CommandRequest{})
	if !resp.OK {
		t.Fatalf("expected OK, got error: %+v", resp.Error)
	}
	if fake.lastCommand != "ls -la" {
		t.Fatalf("unexpected remote command: %s", fake.lastCommand)
	}
	resMap, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got: %T", resp.Result)
	}
	if resMap["exit_code"] != 0 || resMap["stdout"] != "executed\n" {
		t.Fatalf("unexpected result: %+v", resMap)
	}

	// Shell disabled kill switch
	_ = core.store.SetSetting(ctx, "shell_enabled", "false")
	resp = core.RunCommand(ctx, "req-3", "test-client", "termux-main", "MCP_Local", "ls -la", CommandRequest{})
	if resp.OK || resp.Error.Code != "TARGET_SHELL_DISABLED" {
		t.Fatalf("expected TARGET_SHELL_DISABLED, got: %+v", resp)
	}
}

func TestRunCommandPrivilegeGating(t *testing.T) {
	core, fake, ctx := seededExecutionCore(t)

	// Client has target_shell, but lacks target_admin grant -> denied
	resp := core.RunCommand(ctx, "req-1", "test-client", "target-elevated", "MCP_Local", "id", CommandRequest{
		Privilege: "required",
	})
	if resp.OK || resp.Error.Code != "PRIVILEGE_GRANT_REQUIRED" {
		t.Fatalf("expected PRIVILEGE_GRANT_REQUIRED, got: %+v", resp)
	}

	// Add target_admin grant
	_, err := core.store.DB().ExecContext(ctx, `
INSERT INTO grants(client_id, target_id, project_id, capability, enabled)
VALUES ('test-client', 'target-elevated', 'MCP_Local', 'target_admin', 1)
`)
	if err != nil {
		t.Fatal(err)
	}

	// Target policy is ask_always, but no approval recorded yet -> PRIVILEGE_APPROVAL_REQUIRED
	resp = core.RunCommand(ctx, "req-2", "test-client", "target-elevated", "MCP_Local", "id", CommandRequest{
		Privilege: "required",
	})
	if resp.OK || resp.Error.Code != "PRIVILEGE_APPROVAL_REQUIRED" {
		t.Fatalf("expected PRIVILEGE_APPROVAL_REQUIRED, got: %+v", resp)
	}

	// Set approval
	err = core.store.SetPrivilegeApproval(ctx, "target-elevated", "ask_always", "test-client", "MCP_Local", "")
	if err != nil {
		t.Fatal(err)
	}

	// Now it should succeed and wrap with Shizuku backend ("rish -c ...")
	resp = core.RunCommand(ctx, "req-3", "test-client", "target-elevated", "MCP_Local", "id", CommandRequest{
		Privilege: "required",
	})
	if !resp.OK {
		t.Fatalf("expected OK with approval, got: %+v", resp.Error)
	}
	if fake.lastCWD != "/" {
		t.Fatalf("expected execution CWD=/ for shizuku, got: %s", fake.lastCWD)
	}
	if fake.lastCommand != "rish -c 'cd / && id'" {
		t.Fatalf("expected rish command wrapper, got: %s", fake.lastCommand)
	}

	// Consumed approval: second call fails without new approval
	resp = core.RunCommand(ctx, "req-4", "test-client", "target-elevated", "MCP_Local", "id", CommandRequest{
		Privilege: "required",
	})
	if resp.OK || resp.Error.Code != "PRIVILEGE_APPROVAL_REQUIRED" {
		t.Fatalf("expected PRIVILEGE_APPROVAL_REQUIRED after consumption, got: %+v", resp)
	}
}

func TestRunCommandTimeoutHandling(t *testing.T) {
	core, fake, ctx := seededExecutionCore(t)
	fake.runErr = remote.NewError("SSH_TIMEOUT", "SSH command timed out", -1)

	resp := core.RunCommand(ctx, "req-timeout", "test-client", "termux-main", "MCP_Local", "sleep 100", CommandRequest{})
	if !resp.OK {
		t.Fatalf("timeout should return OK=true with timed_out=true according to Python contract, got: %+v", resp)
	}
	resMap := resp.Result.(map[string]any)
	if resMap["timed_out"] != true || resMap["exit_code"] != 124 {
		t.Fatalf("unexpected timeout result: %+v", resMap)
	}
}

func TestRunTask(t *testing.T) {
	core, fake, ctx := seededExecutionCore(t)

	// Existing task
	resp := core.RunTask(ctx, "req-task-1", "test-client", "termux-main", "MCP_Local", "build")
	if !resp.OK {
		t.Fatalf("expected OK, got: %+v", resp.Error)
	}
	if fake.lastCommand != "'go' 'build' './...'" {
		t.Fatalf("unexpected task command: %s", fake.lastCommand)
	}

	// Missing task
	resp = core.RunTask(ctx, "req-task-2", "test-client", "termux-main", "MCP_Local", "nonexistent")
	if resp.OK || resp.Error.Code != "TASK_NOT_FOUND" {
		t.Fatalf("expected TASK_NOT_FOUND, got: %+v", resp)
	}
}
