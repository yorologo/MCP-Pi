package policy

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"mcp-gateway-adapter/internal/registry"
)

type frozenPolicyCases struct {
	Schema             int                       `json:"schema"`
	AuthorizationCases []frozenAuthorizationCase `json:"authorization_cases"`
	PrivilegeCases     []frozenPrivilegeCase     `json:"privilege_cases"`
}

type frozenAuthorizationCase struct {
	Name      string  `json:"name"`
	ClientID  *string `json:"client_id"`
	TargetID  *string `json:"target_id"`
	ProjectID *string `json:"project_id"`
	Tool      string  `json:"tool"`
	Allowed   bool    `json:"allowed"`
	ErrorCode *string `json:"error_code"`
	Reason    *string `json:"reason"`
}

type frozenPrivilegeCase struct {
	Name         string   `json:"name"`
	Capabilities []string `json:"capabilities"`
	Allowed      bool     `json:"allowed"`
	ErrorCode    *string  `json:"error_code"`
	Reason       *string  `json:"reason"`
}

func loadFrozenPolicyCases(t *testing.T) frozenPolicyCases {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "contracts", "policy_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture frozenPolicyCases
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Schema != 1 {
		t.Fatalf("fixture schema=%d want=1", fixture.Schema)
	}
	return fixture
}

func policyStore(t *testing.T) (*registry.Store, *sql.DB) {
	t.Helper()
	ctx := context.Background()
	db, err := registry.Open(ctx, filepath.Join(t.TempDir(), "policy.db"))
	if err != nil {
		t.Fatal(err)
	}
	stmts := []string{
		`INSERT INTO targets(id,display_name,host,user,enabled) VALUES ('t','Target','127.0.0.1','user',1)`,
		`INSERT INTO projects(id,target_id,display_name,root,read_enabled,write_enabled,enabled)
		  VALUES ('p','t','Project','/project',1,0,1)`,
		`INSERT INTO ai_clients(id,display_name,enabled) VALUES ('client','Client',1)`,
		`INSERT INTO ai_clients(id,display_name,enabled) VALUES ('disabled','Disabled',0)`,
	}
	for _, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = db.Close() })
	return registry.NewStore(db), db
}

func addGrant(t *testing.T, db *sql.DB, capability, targetID, projectID string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO grants(client_id,target_id,project_id,capability,enabled) VALUES ('client',?,?,?,1)`,
		registry.ScopeToDB(targetID),
		registry.ScopeToDB(projectID),
		capability,
	); err != nil {
		t.Fatal(err)
	}
}

func configureAuthorizationCase(t *testing.T, name string, db *sql.DB) {
	t.Helper()
	switch name {
	case "anonymous_denied", "unknown_tool_before_registry_state", "missing_client", "disabled_client", "no_grants_denied":
		return
	case "grant_precedes_write_switch":
		addGrant(t, db, "read", "*", "*")
	case "writes_global_kill_switch":
		addGrant(t, db, "write", "*", "*")
	case "shell_kill_switch":
		addGrant(t, db, "target_shell", "*", "*")
	case "gateway_disabled_health_exception":
		addGrant(t, db, "health", "*", "*")
		if _, err := db.Exec("UPDATE settings SET value='false' WHERE key='gateway_enabled'"); err != nil {
			t.Fatal(err)
		}
	case "gateway_disabled_other_tool":
		addGrant(t, db, "read", "*", "*")
		if _, err := db.Exec("UPDATE settings SET value='false' WHERE key='gateway_enabled'"); err != nil {
			t.Fatal(err)
		}
	case "target_disabled":
		addGrant(t, db, "read", "*", "*")
		if _, err := db.Exec("UPDATE targets SET enabled=0 WHERE id='t'"); err != nil {
			t.Fatal(err)
		}
	case "project_disabled":
		addGrant(t, db, "read", "*", "*")
		if _, err := db.Exec("UPDATE projects SET enabled=0 WHERE target_id='t' AND id='p'"); err != nil {
			t.Fatal(err)
		}
	case "project_write_disabled":
		addGrant(t, db, "write", "*", "*")
		if _, err := db.Exec("UPDATE settings SET value='true' WHERE key='writes_enabled'"); err != nil {
			t.Fatal(err)
		}
	case "write_allowed":
		addGrant(t, db, "write", "*", "*")
		if _, err := db.Exec("UPDATE settings SET value='true' WHERE key='writes_enabled'"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("UPDATE projects SET write_enabled=1 WHERE target_id='t' AND id='p'"); err != nil {
			t.Fatal(err)
		}
	case "read_allowed":
		addGrant(t, db, "read", "*", "*")
	default:
		t.Fatalf("unknown frozen policy case %q", name)
	}
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func TestAuthorizationMatchesFrozenPythonPolicy(t *testing.T) {
	fixture := loadFrozenPolicyCases(t)
	for _, tc := range fixture.AuthorizationCases {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			store, db := policyStore(t)
			configureAuthorizationCase(t, tc.Name, db)

			decision, err := AuthorizeClient(
				context.Background(),
				store,
				deref(tc.ClientID),
				deref(tc.TargetID),
				deref(tc.ProjectID),
				tc.Tool,
				false,
			)
			if err != nil {
				t.Fatal(err)
			}
			if decision.Allowed != tc.Allowed {
				t.Fatalf("allowed=%v want=%v", decision.Allowed, tc.Allowed)
			}
			if decision.Code != deref(tc.ErrorCode) {
				t.Fatalf("code=%q want=%q", decision.Code, deref(tc.ErrorCode))
			}
			if decision.Reason != deref(tc.Reason) {
				t.Fatalf("reason=%q want=%q", decision.Reason, deref(tc.Reason))
			}
		})
	}
}

func TestPrivilegeAuthorizationMatchesFrozenPythonPolicy(t *testing.T) {
	fixture := loadFrozenPolicyCases(t)
	for _, tc := range fixture.PrivilegeCases {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			store, db := policyStore(t)
			for _, capability := range tc.Capabilities {
				addGrant(t, db, capability, "t", "p")
			}

			decision, err := AuthorizePrivilegeRequest(context.Background(), store, "client", "t", "p")
			if err != nil {
				t.Fatal(err)
			}
			if decision.Allowed != tc.Allowed {
				t.Fatalf("allowed=%v want=%v", decision.Allowed, tc.Allowed)
			}
			if decision.Code != deref(tc.ErrorCode) {
				t.Fatalf("code=%q want=%q", decision.Code, deref(tc.ErrorCode))
			}
			if decision.Reason != deref(tc.Reason) {
				t.Fatalf("reason=%q want=%q", decision.Reason, deref(tc.Reason))
			}
		})
	}
}

func TestCatalogFilteringUsesGrantSemanticsOnly(t *testing.T) {
	store, db := policyStore(t)
	addGrant(t, db, "read", "t", "p")
	if _, err := db.Exec("UPDATE settings SET value='false' WHERE key='gateway_enabled'"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE targets SET enabled=0 WHERE id='t'"); err != nil {
		t.Fatal(err)
	}

	tools := []string{"health", "read_file", "write_file", "gateway_status"}
	got, err := CatalogForClient(context.Background(), store, "client", tools)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"health", "read_file"}
	if len(got) != len(want) {
		t.Fatalf("catalog=%v want=%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("catalog=%v want=%v", got, want)
		}
	}
}

func TestLocalTrustedCallerPreservesBypass(t *testing.T) {
	store, _ := policyStore(t)
	decision, err := AuthorizeClient(context.Background(), store, "local", "", "", "not_a_tool", false)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Allowed {
		t.Fatalf("trusted local caller denied: %+v", decision)
	}
}
