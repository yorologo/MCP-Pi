package registry

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func seededStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "registry.db")
	if err := MigratePath(ctx, path); err != nil {
		t.Fatal(err)
	}
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}

	stmts := []string{
		`INSERT INTO targets(id,display_name,platform,host,port,user,privilege_policy,enabled)
		  VALUES ('t','Target','linux','127.0.0.1',22,'user','never',1)`,
		`INSERT INTO projects(id,target_id,display_name,root,read_enabled,write_enabled,enabled)
		  VALUES ('p','t','Project','/srv/project',1,0,1)`,
		`INSERT INTO projects(id,target_id,display_name,root,read_enabled,write_enabled,enabled)
		  VALUES ('z','t','Second','/srv/second',1,1,1)`,
		`INSERT INTO project_tasks(target_id,project_id,task_name,argv_json,timeout,enabled)
		  VALUES ('t','p','check','["git","status","--short"]',30,1)`,
		`INSERT INTO ai_clients(id,display_name,enabled) VALUES ('client','Client',1)`,
		`INSERT INTO grants(client_id,target_id,project_id,capability,enabled)
		  VALUES ('client',NULL,NULL,'read',1)`,
		`INSERT INTO grants(client_id,target_id,project_id,capability,enabled)
		  VALUES ('client','t',NULL,'target_shell',1)`,
		`INSERT INTO grants(client_id,target_id,project_id,capability,enabled)
		  VALUES ('client','t','p','write',0)`,
	}
	for _, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			db.Close()
			t.Fatalf("seed store: %v", err)
		}
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewStore(db), ctx
}

func TestStoreReadsWithSingleConnectionPool(t *testing.T) {
	store, _ := seededStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	targets, err := store.ListTargets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 {
		t.Fatalf("targets=%d want=1", len(targets))
	}
	if targets[0].ProjectCount != 2 {
		t.Fatalf("project_count=%d want=2", targets[0].ProjectCount)
	}
	if got := targets[0].Projects; len(got) != 2 || got[0] != "p" || got[1] != "z" {
		t.Fatalf("projects=%v want=[p z]", got)
	}

	target, err := store.GetTarget(ctx, "t", false)
	if err != nil {
		t.Fatal(err)
	}
	project := target.Projects["p"]
	if project.Root != "/srv/project" || !project.Read || project.Write || !project.Enabled {
		t.Fatalf("unexpected project: %+v", project)
	}
	task, ok := project.Tasks["check"]
	if !ok {
		t.Fatal("task check missing")
	}
	if !task.Enabled || task.Timeout != 30 || len(task.Argv) != 3 || task.Argv[0] != "git" {
		t.Fatalf("unexpected task: %+v", task)
	}

	projects, err := store.ListProjects(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 2 {
		t.Fatalf("projects=%d want=2", len(projects))
	}
}

func TestStoreNormalizesV5NullScopesToWildcardContract(t *testing.T) {
	store, ctx := seededStore(t)
	grants, err := store.ListGrants(ctx, "client")
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 3 {
		t.Fatalf("grants=%d want=3", len(grants))
	}
	if grants[0].TargetID != "*" || grants[0].ProjectID != "*" {
		t.Fatalf("global V5 NULL scope not normalized: %+v", grants[0])
	}
	if grants[1].TargetID != "t" || grants[1].ProjectID != "*" {
		t.Fatalf("target-scoped wildcard not normalized: %+v", grants[1])
	}

	active, err := store.GetClientGrants(ctx, "client")
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 2 {
		t.Fatalf("active grants=%d want=2", len(active))
	}

	if ScopeToDB("*") != nil || ScopeToDB("") != nil {
		t.Fatal("wildcard scope must map to SQL NULL")
	}
	if got := ScopeToDB(" t "); got != "t" {
		t.Fatalf("specific scope=%v want=t", got)
	}
}

func TestStoreSettingsAndActivityPrimitives(t *testing.T) {
	store, ctx := seededStore(t)

	if got, err := store.GetSetting(ctx, "writes_enabled", "missing"); err != nil || got != "false" {
		t.Fatalf("writes_enabled=%q err=%v", got, err)
	}
	if err := store.SetSetting(ctx, "writes_enabled", "true"); err != nil {
		t.Fatal(err)
	}
	if got, err := store.GetSetting(ctx, "writes_enabled", "missing"); err != nil || got != "true" {
		t.Fatalf("writes_enabled after set=%q err=%v", got, err)
	}

	targetID := "t"
	projectID := "p"
	duration := int64(12)
	bytes := int64(34)
	if err := store.RecordActivity(ctx, Activity{
		Actor:            "client",
		Action:           "read_file",
		TargetID:         &targetID,
		ProjectID:        &projectID,
		DurationMS:       &duration,
		Success:          true,
		BytesTransferred: &bytes,
		Detail:           "fixture",
	}); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM activity WHERE actor='client' AND action='read_file' AND success=1").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("activity count=%d want=1", count)
	}
}

func TestActivityResultFiltersSeparateAttempts(t *testing.T) {
	store, ctx := seededStore(t)

	for _, entry := range []Activity{
		{Actor: "admin", Action: "config_change_attempt", Success: true, Detail: "attempt"},
		{Actor: "admin", Action: "config_change", Success: true, Detail: "pass"},
		{Actor: "admin", Action: "config_denied", Success: false, Detail: "deny"},
	} {
		if err := store.RecordActivity(ctx, entry); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		result string
		want   string
	}{
		{result: "PASS", want: "config_change"},
		{result: "DENY", want: "config_denied"},
		{result: "ATTEMPT", want: "config_change_attempt"},
	}
	for _, tc := range tests {
		t.Run(tc.result, func(t *testing.T) {
			items, err := store.ListActivity(ctx, 10, 0, ActivityFilter{Result: tc.result})
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != 1 || items[0].Action != tc.want {
				t.Fatalf("result=%s items=%+v want action=%s", tc.result, items, tc.want)
			}
		})
	}
}

func TestStoreRejectsUnsafeStableIdentifiers(t *testing.T) {
	store, ctx := seededStore(t)

	if err := store.AddTarget(ctx, Target{
		ID: "bad/target", Platform: "linux", Host: "127.0.0.1", Port: 22, User: "user", Enabled: true,
	}); err == nil {
		t.Fatal("target ID containing slash must be rejected")
	}
	if err := store.AddProject(ctx, Project{
		ID: "bad/project", TargetID: "t", Root: "/tmp/project", Read: true, Enabled: true,
	}); err == nil {
		t.Fatal("project ID containing slash must be rejected")
	}
	if err := store.AddClient(ctx, Client{
		ID: "bad/client", DisplayName: "Bad Client", Protocol: "mcp", Enabled: true,
	}); err == nil {
		t.Fatal("client ID containing slash must be rejected")
	}
}

func TestStoreDisabledAndMissingLookupCodes(t *testing.T) {
	store, ctx := seededStore(t)

	if _, err := store.GetTarget(ctx, "missing", false); ErrorCode(err) != "UNKNOWN_TARGET" {
		t.Fatalf("missing target error=%v code=%q", err, ErrorCode(err))
	}
	if _, err := store.db.ExecContext(ctx, "UPDATE targets SET enabled=0 WHERE id='t'"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetTarget(ctx, "t", false); ErrorCode(err) != "TARGET_DISABLED" {
		t.Fatalf("disabled target error=%v code=%q", err, ErrorCode(err))
	}

	if _, err := store.db.ExecContext(ctx, "UPDATE targets SET enabled=1 WHERE id='t'"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, "UPDATE projects SET enabled=0 WHERE target_id='t' AND id='p'"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetProject(ctx, "t", "p", false); ErrorCode(err) != "PROJECT_DISABLED" {
		t.Fatalf("disabled project error=%v code=%q", err, ErrorCode(err))
	}
}

func TestStorePrivilegeApproval(t *testing.T) {
	store, ctx := seededStore(t)

	// Set approval for ask_always
	err := store.SetPrivilegeApproval(ctx, "t", "ask_always", "client", "p", "")
	if err != nil {
		t.Fatalf("SetPrivilegeApproval failed: %v", err)
	}

	app, err := store.GetPrivilegeApproval(ctx, "t")
	if err != nil || app == nil {
		t.Fatalf("GetPrivilegeApproval failed: %v, app: %v", err, app)
	}
	if app.ClientID != "client" || app.ProjectID != "p" || app.Policy != "ask_always" {
		t.Fatalf("unexpected approval: %+v", app)
	}

	// Consume approval with matching client and project
	ok, err := store.ConsumePrivilegeApproval(ctx, "t", "ask_always", "client", "p", "", 300)
	if err != nil || !ok {
		t.Fatalf("ConsumePrivilegeApproval failed: %v, ok=%v", err, ok)
	}

	// Second consume should fail (consumed/one-use)
	ok, err = store.ConsumePrivilegeApproval(ctx, "t", "ask_always", "client", "p", "", 300)
	if err != nil || ok {
		t.Fatalf("ConsumePrivilegeApproval should fail second time: %v, ok=%v", err, ok)
	}

	// Test ask_once_per_boot
	err = store.SetPrivilegeApproval(ctx, "t", "ask_once_per_boot", "client", "p", "boot-123")
	if err != nil {
		t.Fatalf("SetPrivilegeApproval failed: %v", err)
	}

	// Wrong boot ID fails and clears approval
	ok, err = store.ConsumePrivilegeApproval(ctx, "t", "ask_once_per_boot", "client", "p", "boot-wrong", 300)
	if err != nil || ok {
		t.Fatalf("ConsumePrivilegeApproval should fail with wrong boot ID: ok=%v", ok)
	}

	// Now set again with correct boot ID
	_ = store.SetPrivilegeApproval(ctx, "t", "ask_once_per_boot", "client", "p", "boot-123")
	ok, err = store.ConsumePrivilegeApproval(ctx, "t", "ask_once_per_boot", "client", "p", "boot-123", 300)
	if err != nil || !ok {
		t.Fatalf("ConsumePrivilegeApproval should succeed with correct boot ID: ok=%v", ok)
	}
	// ask_once_per_boot remains valid across multiple commands during that boot!
	ok, err = store.ConsumePrivilegeApproval(ctx, "t", "ask_once_per_boot", "client", "p", "boot-123", 300)
	if err != nil || !ok {
		t.Fatalf("ConsumePrivilegeApproval should still be valid for same boot: ok=%v", ok)
	}
}

func TestCheckPrivilegeApprovalDoesNotConsume(t *testing.T) {
	store, ctx := seededStore(t)

	if err := store.SetPrivilegeApproval(ctx, "t", "ask_always", "client", "p", ""); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		ok, err := store.CheckPrivilegeApproval(ctx, "t", "ask_always", "client", "p", "", 300)
		if err != nil || !ok {
			t.Fatalf("non-consuming ask_always check %d: ok=%v err=%v", i, ok, err)
		}
	}
	if approval, err := store.GetPrivilegeApproval(ctx, "t"); err != nil || approval == nil {
		t.Fatalf("non-consuming check removed approval: approval=%+v err=%v", approval, err)
	}
	ok, err := store.ConsumePrivilegeApproval(ctx, "t", "ask_always", "client", "p", "", 300)
	if err != nil || !ok {
		t.Fatalf("consume after checks: ok=%v err=%v", ok, err)
	}
	ok, err = store.CheckPrivilegeApproval(ctx, "t", "ask_always", "client", "p", "", 300)
	if err != nil || ok {
		t.Fatalf("consumed approval remained valid: ok=%v err=%v", ok, err)
	}

	if err := store.SetPrivilegeApproval(ctx, "t", "ask_once_per_boot", "client", "p", "boot-1"); err != nil {
		t.Fatal(err)
	}
	ok, err = store.CheckPrivilegeApproval(ctx, "t", "ask_once_per_boot", "client", "p", "boot-wrong", 300)
	if err != nil || ok {
		t.Fatalf("wrong boot unexpectedly valid: ok=%v err=%v", ok, err)
	}
	ok, err = store.CheckPrivilegeApproval(ctx, "t", "ask_once_per_boot", "client", "p", "boot-1", 300)
	if err != nil || !ok {
		t.Fatalf("diagnostic wrong-boot check mutated approval: ok=%v err=%v", ok, err)
	}
}

func TestGrantStorageRejectsBundlesAndLogicalDuplicates(t *testing.T) {
	store, ctx := seededStore(t)

	base := Grant{
		ClientID:   "client",
		TargetID:   "t",
		ProjectID:  "p",
		Capability: "admin",
		Enabled:    true,
	}
	if _, err := store.AddGrant(ctx, base); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddGrant(ctx, base); err == nil {
		t.Fatal("logical duplicate grant unexpectedly accepted")
	}

	bundle := base
	bundle.Capability = "admin,write"
	if _, err := store.AddGrant(ctx, bundle); err == nil {
		t.Fatal("new capability bundle unexpectedly accepted")
	}
}

func TestValidateStableIDForNewEntities(t *testing.T) {
	for _, good := range []string{"termux-main", "MCP_Local", "client.v2", "A1"} {
		if got, err := ValidateStableID(good); err != nil || got != good {
			t.Fatalf("ValidateStableID(%q) = %q, %v", good, got, err)
		}
	}
	for _, bad := range []string{"", ".hidden", "-flag", "has space", "a/b", "../escape"} {
		if _, err := ValidateStableID(bad); err == nil {
			t.Fatalf("ValidateStableID(%q) unexpectedly succeeded", bad)
		}
	}
}

func TestLegacyStableIDsRemainEditable(t *testing.T) {
	store, ctx := seededStore(t)
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO targets(id,display_name,platform,host,port,user,privilege_policy,enabled) VALUES ('legacy target','Legacy','linux','127.0.0.2',22,'user','never',1)`); err != nil {
		t.Fatal(err)
	}
	target, err := store.GetTarget(ctx, "legacy target", false)
	if err != nil {
		t.Fatal(err)
	}
	target.DisplayName = "Legacy Updated"
	if err := store.UpdateTarget(ctx, target); err != nil {
		t.Fatalf("legacy target became uneditable: %v", err)
	}
}

func TestStoreCRUD(t *testing.T) {
	store, ctx := seededStore(t)

	// Target CRUD
	target := Target{
		ID:              "target-2",
		DisplayName:     "Target 2",
		Platform:        "linux",
		Host:            "192.168.1.10",
		Port:            22,
		User:            "admin",
		PrivilegePolicy: "ask_always",
		Enabled:         true,
	}
	if err := store.AddTarget(ctx, target); err != nil {
		t.Fatalf("AddTarget: %v", err)
	}
	tGot, err := store.GetTarget(ctx, "target-2", false)
	if err != nil || tGot.ID != "target-2" {
		t.Fatalf("GetTarget: %v, got: %+v", err, tGot)
	}

	target.DisplayName = "Target 2 Updated"
	if err := store.UpdateTarget(ctx, target); err != nil {
		t.Fatalf("UpdateTarget: %v", err)
	}

	// Project CRUD
	project := Project{
		ID:          "proj-2",
		TargetID:    "target-2",
		DisplayName: "Project 2",
		Root:        "/srv/proj2",
		Read:        true,
		Write:       true,
		Enabled:     true,
	}
	if err := store.AddProject(ctx, project); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	pGot, err := store.GetProject(ctx, "target-2", "proj-2", false)
	if err != nil || pGot.ID != "proj-2" {
		t.Fatalf("GetProject: %v, got: %+v", err, pGot)
	}

	// Client CRUD
	client := Client{
		ID:          "client-2",
		DisplayName: "Client 2",
		Provider:    "test",
		Protocol:    "mcp",
		Enabled:     true,
	}
	if err := store.AddClient(ctx, client); err != nil {
		t.Fatalf("AddClient: %v", err)
	}
	cGot, err := store.GetClient(ctx, "client-2")
	if err != nil || cGot.ID != "client-2" {
		t.Fatalf("GetClient: %v, got: %+v", err, cGot)
	}

	// Grant CRUD
	gid, err := store.AddGrant(ctx, Grant{
		ClientID:   "client-2",
		TargetID:   "target-2",
		ProjectID:  "proj-2",
		Capability: "target_shell",
		Enabled:    true,
	})
	if err != nil || gid <= 0 {
		t.Fatalf("AddGrant: %v, id=%d", err, gid)
	}
	gGot, err := store.GetGrant(ctx, gid)
	if err != nil || gGot.Capability != "target_shell" {
		t.Fatalf("GetGrant: %v, got: %+v", err, gGot)
	}

	// Admin User CRUD
	if err := store.SetAdminPassword(ctx, "admin", "hash123"); err != nil {
		t.Fatalf("SetAdminPassword: %v", err)
	}
	uGot, err := store.GetAdminUser(ctx, "admin")
	if err != nil || uGot == nil || uGot.PasswordHash != "hash123" {
		t.Fatalf("GetAdminUser: %v, got: %+v", err, uGot)
	}
	if err := store.UpdateAdminLogin(ctx, "admin"); err != nil {
		t.Fatalf("UpdateAdminLogin: %v", err)
	}

	// Activity count and prune
	count, err := store.GetActivityCount(ctx)
	if err != nil {
		t.Fatalf("GetActivityCount: %v", err)
	}
	if count < 0 {
		t.Fatalf("invalid activity count: %d", count)
	}
	if _, err := store.PruneActivity(ctx, 100); err != nil {
		t.Fatalf("PruneActivity: %v", err)
	}
}

func TestSetSettingsAtomic(t *testing.T) {
	store, ctx := seededStore(t)

	values := map[string]string{
		"default_timeout":    "45",
		"max_output_bytes":   "524288",
		"activity_retention": "6000",
	}
	if err := store.SetSettings(ctx, values); err != nil {
		t.Fatalf("SetSettings failed: %v", err)
	}
	for key, want := range values {
		got, err := store.GetSetting(ctx, key, "")
		if err != nil {
			t.Fatalf("GetSetting(%s): %v", key, err)
		}
		if got != want {
			t.Fatalf("setting %s=%q want %q", key, got, want)
		}
	}

	if err := store.SetSettings(ctx, map[string]string{"default_timeout": "60", "": "bad"}); err == nil {
		t.Fatal("expected empty setting key to fail")
	}
	got, err := store.GetSetting(ctx, "default_timeout", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "45" {
		t.Fatalf("transaction was not atomic: default_timeout=%q want 45", got)
	}
}
