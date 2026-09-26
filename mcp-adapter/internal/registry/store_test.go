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
	db, err := Open(ctx, filepath.Join(t.TempDir(), "registry.db"))
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
