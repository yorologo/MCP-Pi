package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"mcp-gateway-adapter/internal/registry"
)

type frozenCoreBasic struct {
	Schema         int                  `json:"schema"`
	HealthSemantic frozenHealthSemantic `json:"health_semantic"`
	ListTargets    map[string]any       `json:"list_targets"`
}

type frozenHealthSemantic struct {
	OK            bool           `json:"ok"`
	Tool          string         `json:"tool"`
	Result        map[string]any `json:"result"`
	RuntimeFields []string       `json:"runtime_fields"`
}

func loadCoreFixture(t *testing.T) frozenCoreBasic {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "contracts", "core_basic_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture frozenCoreBasic
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Schema != 1 {
		t.Fatalf("fixture schema=%d want=1", fixture.Schema)
	}
	return fixture
}

func seededCore(t *testing.T) (*Core, *sql.DB) {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "core.db")
	db, err := registry.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}

	stmts := []string{
		`INSERT INTO targets(id,display_name,platform,host,port,user,privilege_policy,enabled)
		  VALUES ('t','Target','linux','127.0.0.1',22,'user','never',1)`,
		`INSERT INTO projects(id,target_id,display_name,root,read_enabled,write_enabled,enabled)
		  VALUES ('p','t','Project','/srv/project',1,0,1)`,
		`INSERT INTO ai_clients(id,display_name,enabled) VALUES ('client','Client',1)`,
		`INSERT INTO grants(client_id,target_id,project_id,capability,enabled)
		  VALUES ('client',NULL,NULL,'read',1)`,
	}
	for _, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = db.Close() })

	return New(registry.NewStore(db), Config{
		GatewayVersion:     "1.4.0",
		CoreAPIVersion:     1,
		ToolCatalogVersion: 4,
		MCPProtocol:        "2026-07-28",
		DBPath:             dbPath,
		BackupDir:          filepath.Join(filepath.Dir(dbPath), "backups"),
	}), db
}

func TestHealthMatchesFrozenPythonSemanticsWithGoRuntimeIdentity(t *testing.T) {
	fixture := loadCoreFixture(t)
	core, _ := seededCore(t)

	got := core.Health(context.Background(), "")
	if !got.OK || got.Tool != fixture.HealthSemantic.Tool {
		t.Fatalf("health envelope=%+v", got)
	}

	result, ok := got.Result.(map[string]any)
	if !ok {
		t.Fatalf("health result type=%T", got.Result)
	}
	normalizedResult := normalizeJSON(t, result)
	for key, want := range fixture.HealthSemantic.Result {
		if !reflect.DeepEqual(normalizedResult[key], want) {
			t.Fatalf("health[%s]=%v want=%v", key, normalizedResult[key], want)
		}
	}

	for _, legacy := range fixture.HealthSemantic.RuntimeFields {
		if legacy == "python_version" {
			if _, exists := result[legacy]; exists {
				t.Fatalf("Go-only candidate must not expose %q", legacy)
			}
			continue
		}
		if _, exists := result[legacy]; !exists {
			t.Fatalf("health runtime field %q missing", legacy)
		}
	}
	if gotVersion, ok := result["go_version"].(string); !ok || gotVersion != runtime.Version() {
		t.Fatalf("go_version=%v want=%s", result["go_version"], runtime.Version())
	}
	if got.DurationMS < 0 {
		t.Fatalf("duration_ms=%d", got.DurationMS)
	}
}

func TestListTargetsMatchesFrozenPythonOutput(t *testing.T) {
	fixture := loadCoreFixture(t)
	core, _ := seededCore(t)

	got := core.ListTargets(context.Background(), "")
	gotMap := normalizeJSON(t, got)
	delete(gotMap, "duration_ms")

	if !reflect.DeepEqual(gotMap, fixture.ListTargets) {
		t.Fatalf("list_targets mismatch\nGo: %#v\nPython: %#v", gotMap, fixture.ListTargets)
	}
}

func TestHealthDisabledStateAndRequestID(t *testing.T) {
	core, db := seededCore(t)
	if _, err := db.Exec("UPDATE settings SET value='false' WHERE key='gateway_enabled'"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE settings SET value='true' WHERE key='writes_enabled'"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE settings SET value='true' WHERE key='shell_enabled'"); err != nil {
		t.Fatal(err)
	}

	got := core.Health(context.Background(), "req-test")
	if got.RequestID != "req-test" {
		t.Fatalf("request_id=%q", got.RequestID)
	}
	result := got.Result.(map[string]any)
	if result["gateway_status"] != "disabled" ||
		result["gateway_enabled"] != false ||
		result["writes_enabled"] != true ||
		result["shell_enabled"] != true {
		t.Fatalf("unexpected disabled health result: %#v", result)
	}
}

func TestCandidateVersionUsesSchemaV5(t *testing.T) {
	if registry.SchemaVersion != 5 {
		t.Fatalf("registry.SchemaVersion=%d want=5", registry.SchemaVersion)
	}
	core, _ := seededCore(t)
	got := core.Version()
	if !got.OK ||
		got.GatewayVersion != "1.4.0" ||
		got.CoreAPIVersion != 1 ||
		got.ToolCatalogVersion != 4 ||
		got.RegistrySchemaVersion != registry.SchemaVersion ||
		got.MCPProtocol != "2026-07-28" {
		t.Fatalf("version=%+v", got)
	}
}

func TestCatalogDelegatesToGoPolicy(t *testing.T) {
	core, _ := seededCore(t)
	tools := []string{"health", "list_targets", "write_file", "gateway_status"}

	got, err := core.Catalog(context.Background(), "client", tools)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"health", "list_targets"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("catalog=%v want=%v", got, want)
	}

	anonymous, err := core.Catalog(context.Background(), "NONE", tools)
	if err != nil {
		t.Fatal(err)
	}
	if len(anonymous) != 0 {
		t.Fatalf("anonymous catalog=%v want empty", anonymous)
	}
}

func normalizeJSON(t *testing.T, value any) map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
