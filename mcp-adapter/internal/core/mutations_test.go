package core

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"mcp-gateway-adapter/internal/registry"
	"mcp-gateway-adapter/internal/remote"
)

type mutationRemote struct {
	fakeRemote
	writeCalls int
}

func (f *mutationRemote) WriteFileAtomic(
	_ context.Context,
	_ registry.Target,
	_ string,
	content []byte,
	create bool,
	expectedSHA256 string,
	_ int,
	_ time.Duration,
) (remote.AtomicWriteResult, error) {
	f.writeCalls++
	sum := sha256.Sum256(content)
	var old *string
	if expectedSHA256 != "" {
		value := expectedSHA256
		old = &value
	}
	return remote.AtomicWriteResult{
		Created: create, OldSHA256: old, NewSHA256: fmt.Sprintf("%x", sum),
		BytesWritten: len(content), Atomic: true,
	}, nil
}

func (f *mutationRemote) ProbePath(_ context.Context, _ registry.Target, candidatePath string, _ time.Duration) (remote.PathProbe, error) {
	parent := filepath.Dir(candidatePath)
	if strings.Contains(candidatePath, "symlink_file.txt") {
		return remote.PathProbe{Exists: true, IsSymlink: true, CanonicalPath: candidatePath, ParentExists: true, ParentCanonicalPath: parent}, nil
	}
	if strings.HasSuffix(candidatePath, "/existing.txt") || strings.HasSuffix(candidatePath, "\\existing.txt") {
		content := "hello existing\n"
		sum := sha256.Sum256([]byte(content))
		return remote.PathProbe{
			Exists: true, IsFile: true, CanonicalPath: candidatePath, ParentExists: true,
			ParentCanonicalPath: parent, SHA256: fmt.Sprintf("%x", sum), Size: int64(len(content)), Content: content,
		}, nil
	}
	return remote.PathProbe{CanonicalPath: candidatePath, ParentExists: true, ParentCanonicalPath: parent}, nil
}

func (f *mutationRemote) ResolveSafeDestination(
	_ context.Context, _ registry.Target, projectRoot, candidatePath string, _ bool, _ time.Duration,
) (remote.SafeDestination, error) {
	if strings.Contains(candidatePath, "nested_symlink") {
		return remote.SafeDestination{}, remote.NewError("PATH_OUTSIDE_ALLOWED_ROOT", "Destination parent resolves outside project root", 15)
	}
	if strings.Contains(candidatePath, "dest_symlink") {
		return remote.SafeDestination{}, remote.NewError("SYMLINK_WRITE_DENIED", "Destination is a symlink", 11)
	}
	return remote.SafeDestination{
		RootCanonical: projectRoot, ParentCanonical: filepath.Dir(candidatePath), DestinationPath: candidatePath,
	}, nil
}

func loadMutationFixture(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "contracts", "structured_mutation_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture map[string]any
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture["schema"] != float64(1) {
		t.Fatalf("fixture schema=%v want=1", fixture["schema"])
	}
	return fixture
}

func seededMutationCore(t *testing.T) (*Core, *mutationRemote, *sql.DB) {
	t.Helper()
	ctx := context.Background()
	db, err := registry.Open(ctx, filepath.Join(t.TempDir(), "mutation.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	stmts := []string{
		"INSERT INTO targets(id,display_name,platform,host,port,user,privilege_policy,enabled) VALUES ('mock-target','Mock Target','linux','127.0.0.1',22,'tester','never',1)",
		"INSERT INTO projects(id,target_id,display_name,root,read_enabled,write_enabled,enabled) VALUES ('mock-proj','mock-target','Mock Project','/home/tester/proj',1,1,1)",
		"INSERT INTO ai_clients(id,display_name,enabled) VALUES ('client','Client',1)",
		"INSERT INTO grants(client_id,target_id,project_id,capability,enabled) VALUES ('client','mock-target','mock-proj','write',1)",
	}
	for _, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	store := registry.NewStore(db)
	if err := store.SetSetting(ctx, "gateway_enabled", "true"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSetting(ctx, "writes_enabled", "true"); err != nil {
		t.Fatal(err)
	}

	fake := &mutationRemote{}
	return NewWithRemote(store, Config{
		GatewayVersion: "1.4.0", CoreAPIVersion: 1, ToolCatalogVersion: 4, MCPProtocol: "2026-07-28",
	}, fake), fake, db
}

func TestStructuredMutationsMatchFrozenPythonOutputs(t *testing.T) {
	fixture := loadMutationFixture(t)
	c, _, _ := seededMutationCore(t)
	ctx := context.Background()
	cases := []struct {
		name string
		got  Response
	}{
		{"write_create_dry", c.WriteFile(ctx, "", "client", "mock-target", "mock-proj", writeRequest{Path: "new.txt", Content: "hello new\n", DryRun: true, Create: true})},
		{"write_create", c.WriteFile(ctx, "", "client", "mock-target", "mock-proj", writeRequest{Path: "new.txt", Content: "hello new\n", Create: true})},
		{"append", c.AppendFile(ctx, "", "client", "mock-target", "mock-proj", "existing.txt", "more text\n")},
		{"mkdir", c.Mkdir(ctx, "", "client", "mock-target", "mock-proj", "new_dir", true)},
		{"copy", c.CopyFile(ctx, "", "client", "mock-target", "mock-proj", "existing.txt", "existing_copy.txt")},
		{"move", c.MoveFile(ctx, "", "client", "mock-target", "mock-proj", "existing.txt", "existing_moved.txt")},
		{"delete", c.DeleteFile(ctx, "", "client", "mock-target", "mock-proj", "existing.txt")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := fixture[tc.name].(map[string]any)
			got := normalizedResponse(t, tc.got)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s mismatch\nGo: %#v\nPython: %#v", tc.name, got, want)
			}
		})
	}
}

func TestStructuredMutationErrorsMatchFrozenPythonOutputs(t *testing.T) {
	fixture := loadMutationFixture(t)
	errorsFixture := fixture["errors"].(map[string]any)
	ctx := context.Background()
	type mutationCase struct {
		name string
		run  func(*Core, *sql.DB) Response
	}
	cases := []mutationCase{
		{"write_writes_disabled", func(c *Core, _ *sql.DB) Response {
			if err := c.store.SetSetting(ctx, "writes_enabled", "false"); err != nil {
				t.Fatal(err)
			}
			return c.WriteFile(ctx, "", "client", "mock-target", "mock-proj", writeRequest{Path: "new.txt", Content: "hello", Create: true})
		}},
		{"write_project_disabled", func(c *Core, db *sql.DB) Response {
			if _, err := db.Exec("UPDATE projects SET write_enabled=0 WHERE target_id='mock-target' AND id='mock-proj'"); err != nil {
				t.Fatal(err)
			}
			return c.WriteFile(ctx, "", "client", "mock-target", "mock-proj", writeRequest{Path: "new.txt", Content: "hello", Create: true})
		}},
		{"write_traversal", func(c *Core, _ *sql.DB) Response {
			return c.WriteFile(ctx, "", "client", "mock-target", "mock-proj", writeRequest{Path: "../etc/passwd", Content: "evil"})
		}},
		{"write_absolute", func(c *Core, _ *sql.DB) Response {
			return c.WriteFile(ctx, "", "client", "mock-target", "mock-proj", writeRequest{Path: "/etc/passwd", Content: "evil"})
		}},
		{"write_symlink", func(c *Core, _ *sql.DB) Response {
			return c.WriteFile(ctx, "", "client", "mock-target", "mock-proj", writeRequest{Path: "symlink_file.txt", Content: "evil"})
		}},
		{"write_nul", func(c *Core, _ *sql.DB) Response {
			return c.WriteFile(ctx, "", "client", "mock-target", "mock-proj", writeRequest{Path: "bad.txt", Content: "bad\x00content", Create: true})
		}},
		{"write_missing_sha", func(c *Core, _ *sql.DB) Response {
			return c.WriteFile(ctx, "", "client", "mock-target", "mock-proj", writeRequest{Path: "existing.txt", Content: "new data"})
		}},
		{"write_wrong_sha", func(c *Core, _ *sql.DB) Response {
			return c.WriteFile(ctx, "", "client", "mock-target", "mock-proj", writeRequest{Path: "existing.txt", Content: "new data", ExpectedSHA256: "wronghash"})
		}},
		{"write_nested_symlink", func(c *Core, _ *sql.DB) Response {
			return c.WriteFile(ctx, "", "client", "mock-target", "mock-proj", writeRequest{Path: "nested_symlink/new.txt", Content: "x", Create: true})
		}},
		{"append_dest_symlink", func(c *Core, _ *sql.DB) Response {
			return c.AppendFile(ctx, "", "client", "mock-target", "mock-proj", "dest_symlink.txt", "x")
		}},
		{"delete_dest_symlink", func(c *Core, _ *sql.DB) Response {
			return c.DeleteFile(ctx, "", "client", "mock-target", "mock-proj", "dest_symlink.txt")
		}},
		{"copy_nested_symlink", func(c *Core, _ *sql.DB) Response {
			return c.CopyFile(ctx, "", "client", "mock-target", "mock-proj", "existing.txt", "nested_symlink/copied.txt")
		}},
		{"move_nested_symlink", func(c *Core, _ *sql.DB) Response {
			return c.MoveFile(ctx, "", "client", "mock-target", "mock-proj", "existing.txt", "nested_symlink/moved.txt")
		}},
		{"mkdir_nested_symlink", func(c *Core, _ *sql.DB) Response {
			return c.Mkdir(ctx, "", "client", "mock-target", "mock-proj", "nested_symlink/new_dir", true)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _, db := seededMutationCore(t)
			want := errorsFixture[tc.name].(map[string]any)
			got := normalizedResponse(t, tc.run(c, db))
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s mismatch\nGo: %#v\nPython: %#v", tc.name, got, want)
			}
		})
	}
}

func TestCriticalWriteFailsClosedWhenAuditUnavailable(t *testing.T) {
	c, fake, db := seededMutationCore(t)
	if _, err := db.Exec("DROP TABLE activity"); err != nil {
		t.Fatal(err)
	}
	got := c.WriteFile(context.Background(), "req-audit", "client", "mock-target", "mock-proj", writeRequest{
		Path: "new.txt", Content: "hello", Create: true,
	})
	if got.OK || got.Error == nil || got.Error.Code != "AUDIT_UNAVAILABLE" {
		t.Fatalf("write with broken audit=%+v", got)
	}
	if fake.writeCalls != 0 {
		t.Fatalf("remote write executed despite unavailable audit: calls=%d", fake.writeCalls)
	}
}

func TestWriteFileDenialsAreAuditedBestEffort(t *testing.T) {
	c, _, db := seededMutationCore(t)
	if err := c.store.SetSetting(context.Background(), "writes_enabled", "false"); err != nil {
		t.Fatal(err)
	}
	got := c.WriteFile(context.Background(), "req-deny", "client", "mock-target", "mock-proj", writeRequest{
		Path: "new.txt", Content: "hello", Create: true,
	})
	if got.OK || got.Error == nil || got.Error.Code != "WRITES_DISABLED" {
		t.Fatalf("denied write=%+v", got)
	}
	var action string
	var success int
	var code sql.NullString
	var detail string
	if err := db.QueryRow(
		"SELECT action,success,error_code,detail FROM activity ORDER BY id DESC LIMIT 1",
	).Scan(&action, &success, &code, &detail); err != nil {
		t.Fatal(err)
	}
	if action != "DENY" || success != 0 || !code.Valid || code.String != "WRITES_DISABLED" {
		t.Fatalf("audit action=%q success=%d code=%v", action, success, code)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(detail), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["tool"] != "write_file" || decoded["path"] != "new.txt" || decoded["request_id"] != "req-deny" {
		t.Fatalf("audit detail=%#v", decoded)
	}
}

func TestInvokeStructuredMutationsUseBridgeArgumentSemantics(t *testing.T) {
	c, _, _ := seededMutationCore(t)
	ctx := context.Background()
	create := c.Invoke(ctx, Invocation{ClientID: "client"}, "write_file", map[string]any{
		"target": "mock-target", "project": "mock-proj", "path": "new.txt", "content": "hello new\n", "create": true,
	})
	if create["ok"] != true {
		t.Fatalf("write_file invoke=%#v", create)
	}
	appendResult := c.Invoke(ctx, Invocation{ClientID: "client"}, "append_file", map[string]any{
		"target": "mock-target", "project": "mock-proj", "relative_path": "existing.txt", "content": "more text\n",
	})
	if appendResult["ok"] != true {
		t.Fatalf("append_file invoke=%#v", appendResult)
	}
	mkdirResult := c.Invoke(ctx, Invocation{ClientID: "client"}, "mkdir", map[string]any{
		"target": "mock-target", "project": "mock-proj", "path": "new_dir",
	})
	if mkdirResult["ok"] != true {
		t.Fatalf("mkdir invoke=%#v", mkdirResult)
	}
	invalid := c.Invoke(ctx, Invocation{ClientID: "client"}, "copy_file", map[string]any{
		"target": "mock-target", "project": "mock-proj",
	})
	code, message := invokeError(t, invalid)
	if code != "INVALID_ARGUMENTS" || message != "Missing or invalid required arguments: 'target', 'project', 'source_path', and 'dest_path' are required" {
		t.Fatalf("copy_file invalid args=%#v", invalid)
	}
}
