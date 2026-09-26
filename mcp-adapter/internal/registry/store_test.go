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
