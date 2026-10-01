package registry

import (
	"context"
	"path/filepath"
	"testing"
)

func newSecurityTestStore(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "registry.db")
	if err := MigratePath(ctx, path); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.AddTarget(ctx, Target{
		ID: "t", DisplayName: "Target", Platform: "linux", Host: "host-a", Port: 22,
		User: "user", PrivilegePolicy: "ask_always", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AddProject(ctx, Project{
		ID: "p", TargetID: "t", DisplayName: "Project", Root: "/tmp", Read: true, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AddClient(ctx, Client{ID: "c", DisplayName: "Client", Protocol: "mcp", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestTargetMutationRollsBackWhenApprovalRevocationFails(t *testing.T) {
	store := newSecurityTestStore(t)
	ctx := context.Background()
	if err := store.SetPrivilegeApproval(ctx, "t", "ask_always", "c", "p", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx,
		"CREATE TRIGGER block_approval_delete BEFORE DELETE ON privilege_approvals BEGIN SELECT RAISE(ABORT, 'blocked'); END;",
	); err != nil {
		t.Fatal(err)
	}

	target, err := store.GetTarget(ctx, "t", false)
	if err != nil {
		t.Fatal(err)
	}
	target.Host = "host-b"
	if err := store.UpdateTarget(ctx, target); err == nil {
		t.Fatal("UpdateTarget succeeded even though approval revocation failed")
	}
	after, err := store.GetTarget(ctx, "t", false)
	if err != nil {
		t.Fatal(err)
	}
	if after.Host != "host-a" {
		t.Fatalf("target mutation was not rolled back: host=%q", after.Host)
	}
	approval, err := store.GetPrivilegeApproval(ctx, "t")
	if err != nil {
		t.Fatal(err)
	}
	if approval == nil {
		t.Fatal("failed transaction unexpectedly removed approval")
	}
}

func TestAskAlwaysApprovalFailsClosedWhenConsumptionDeleteFails(t *testing.T) {
	store := newSecurityTestStore(t)
	ctx := context.Background()
	if err := store.SetPrivilegeApproval(ctx, "t", "ask_always", "c", "p", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx,
		"CREATE TRIGGER block_approval_delete BEFORE DELETE ON privilege_approvals BEGIN SELECT RAISE(ABORT, 'blocked'); END;",
	); err != nil {
		t.Fatal(err)
	}

	allowed, err := store.ConsumePrivilegeApproval(ctx, "t", "ask_always", "c", "p", "", 300)
	if err == nil {
		t.Fatal("approval consumption did not report delete failure")
	}
	if allowed {
		t.Fatal("approval was allowed even though one-time consumption failed")
	}
}

func TestSettingsMutationRollsBackWhenAuditFails(t *testing.T) {
	store := newSecurityTestStore(t)
	ctx := context.Background()
	if err := store.SetSetting(ctx, "writes_enabled", "false"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx,
		"CREATE TRIGGER block_settings_audit BEFORE INSERT ON activity WHEN NEW.action = 'settings_mutation' BEGIN SELECT RAISE(ABORT, 'blocked'); END;",
	); err != nil {
		t.Fatal(err)
	}

	err := store.SetSettingsAudited(ctx, map[string]string{
		"writes_enabled": "true",
	}, Activity{
		Actor:   "admin",
		Action:  "settings_mutation",
		Success: true,
		Detail:  "test",
	})
	if err == nil {
		t.Fatal("settings mutation succeeded despite failed success audit")
	}
	value, err := store.GetSetting(ctx, "writes_enabled", "")
	if err != nil {
		t.Fatal(err)
	}
	if value != "false" {
		t.Fatalf("settings mutation survived failed audit: writes_enabled=%q", value)
	}
}

func TestAddGrantsAuditedIsAtomicAcrossInsertsAndAudit(t *testing.T) {
	t.Run("duplicate insert rolls back earlier grants", func(t *testing.T) {
		store := newSecurityTestStore(t)
		ctx := context.Background()
		if _, err := store.AddGrant(ctx, Grant{ClientID: "c", TargetID: "t", ProjectID: "p", Capability: "read", Enabled: true}); err != nil {
			t.Fatal(err)
		}
		before, err := store.ListGrants(ctx, "c")
		if err != nil {
			t.Fatal(err)
		}
		_, err = store.AddGrantsAudited(ctx,
			[]Grant{
				{ClientID: "c", TargetID: "t", ProjectID: "p", Capability: "write", Enabled: true},
				{ClientID: "c", TargetID: "t", ProjectID: "p", Capability: "read", Enabled: true},
			},
			[]Activity{
				{Actor: "admin", Action: "add_grant", Success: true, Detail: "write"},
				{Actor: "admin", Action: "add_grant", Success: true, Detail: "read"},
			},
		)
		if err == nil {
			t.Fatal("duplicate batch unexpectedly succeeded")
		}
		after, err := store.ListGrants(ctx, "c")
		if err != nil {
			t.Fatal(err)
		}
		if len(after) != len(before) {
			t.Fatalf("duplicate batch left partial grants: before=%d after=%d", len(before), len(after))
		}
	})

	t.Run("audit failure rolls back complete batch", func(t *testing.T) {
		store := newSecurityTestStore(t)
		ctx := context.Background()
		if _, err := store.DB().ExecContext(ctx,
			"CREATE TRIGGER block_second_grant_audit BEFORE INSERT ON activity WHEN NEW.detail = 'block' BEGIN SELECT RAISE(ABORT, 'blocked'); END;",
		); err != nil {
			t.Fatal(err)
		}
		_, err := store.AddGrantsAudited(ctx,
			[]Grant{
				{ClientID: "c", TargetID: "t", ProjectID: "p", Capability: "write", Enabled: true},
				{ClientID: "c", TargetID: "t", ProjectID: "p", Capability: "tasks", Enabled: true},
			},
			[]Activity{
				{Actor: "admin", Action: "add_grant", Success: true, Detail: "ok"},
				{Actor: "admin", Action: "add_grant", Success: true, Detail: "block"},
			},
		)
		if err == nil {
			t.Fatal("batch succeeded despite audit failure")
		}
		grants, err := store.ListGrants(ctx, "c")
		if err != nil {
			t.Fatal(err)
		}
		if len(grants) != 0 {
			t.Fatalf("audit failure left partial grants: %+v", grants)
		}
	})
}

func TestAddGrantsAuditedRejectsEmptyOrMismatchedBatch(t *testing.T) {
	store := newSecurityTestStore(t)
	ctx := context.Background()
	if _, err := store.AddGrantsAudited(ctx, nil, nil); err == nil {
		t.Fatal("empty grant batch was accepted")
	}
	if _, err := store.AddGrantsAudited(ctx,
		[]Grant{{ClientID: "c", TargetID: "t", ProjectID: "p", Capability: "read", Enabled: true}},
		nil,
	); err == nil {
		t.Fatal("grant/activity length mismatch was accepted")
	}
	grants, err := store.ListGrants(ctx, "c")
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 0 {
		t.Fatalf("invalid batch mutated grants: %+v", grants)
	}
}

func TestAddGrantsAuditedCreatesAllGrantsAndActivities(t *testing.T) {
	store := newSecurityTestStore(t)
	ctx := context.Background()
	ids, err := store.AddGrantsAudited(ctx,
		[]Grant{
			{ClientID: "c", TargetID: "t", ProjectID: "p", Capability: "write", Enabled: true},
			{ClientID: "c", TargetID: "t", ProjectID: "p", Capability: "tasks", Enabled: true},
		},
		[]Activity{
			{Actor: "admin", Action: "add_grant", Success: true, Detail: "write"},
			{Actor: "admin", Action: "add_grant", Success: true, Detail: "tasks"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatalf("ids=%v want two ids", ids)
	}
	grants, err := store.ListGrants(ctx, "c")
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 2 {
		t.Fatalf("grants=%+v want two grants", grants)
	}
	var activities int
	if err := store.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM activity WHERE action = 'add_grant'").Scan(&activities); err != nil {
		t.Fatal(err)
	}
	if activities != 2 {
		t.Fatalf("activity rows=%d want 2", activities)
	}
}

func TestTargetEndpointAuditedCASPreservesPrivilegeApproval(t *testing.T) {
	store := newSecurityTestStore(t)
	ctx := context.Background()
	if err := store.SetPrivilegeApproval(ctx, "t", "ask_always", "c", "p", ""); err != nil {
		t.Fatal(err)
	}

	targetID := "t"
	activity := Activity{
		Actor:    "system",
		Action:   "target_endpoint_recovered",
		TargetID: &targetID,
		Success:  true,
		Detail:   "host-a:22 -> host-b:2222",
	}
	if err := store.UpdateTargetEndpointAudited(ctx, "t", "host-a", 22, "host-b", 2222, activity); err != nil {
		t.Fatal(err)
	}

	target, err := store.GetTarget(ctx, "t", false)
	if err != nil {
		t.Fatal(err)
	}
	if target.Host != "host-b" || target.Port != 2222 {
		t.Fatalf("endpoint=%s:%d want host-b:2222", target.Host, target.Port)
	}
	approval, err := store.GetPrivilegeApproval(ctx, "t")
	if err != nil {
		t.Fatal(err)
	}
	if approval == nil {
		t.Fatal("endpoint-only recovery unexpectedly revoked privilege approval")
	}
	var activities int
	if err := store.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM activity WHERE action = 'target_endpoint_recovered'").Scan(&activities); err != nil {
		t.Fatal(err)
	}
	if activities != 1 {
		t.Fatalf("activity rows=%d want=1", activities)
	}
}

func TestTargetEndpointAuditedFailsClosedOnConflict(t *testing.T) {
	store := newSecurityTestStore(t)
	ctx := context.Background()
	targetID := "t"
	err := store.UpdateTargetEndpointAudited(ctx, "t", "stale-host", 22, "host-b", 22, Activity{
		Actor:    "system",
		Action:   "target_endpoint_recovered",
		TargetID: &targetID,
		Success:  true,
	})
	if ErrorCode(err) != "TARGET_ENDPOINT_CONFLICT" {
		t.Fatalf("err=%v code=%q want TARGET_ENDPOINT_CONFLICT", err, ErrorCode(err))
	}
	target, err := store.GetTarget(ctx, "t", false)
	if err != nil {
		t.Fatal(err)
	}
	if target.Host != "host-a" || target.Port != 22 {
		t.Fatalf("conflicting CAS mutated endpoint to %s:%d", target.Host, target.Port)
	}
}

func TestTargetEndpointAuditedRollsBackWhenAuditFails(t *testing.T) {
	store := newSecurityTestStore(t)
	ctx := context.Background()
	if _, err := store.DB().ExecContext(ctx,
		"CREATE TRIGGER block_endpoint_audit BEFORE INSERT ON activity WHEN NEW.action = 'target_endpoint_recovered' BEGIN SELECT RAISE(ABORT, 'blocked'); END;",
	); err != nil {
		t.Fatal(err)
	}
	targetID := "t"
	err := store.UpdateTargetEndpointAudited(ctx, "t", "host-a", 22, "host-b", 22, Activity{
		Actor:    "system",
		Action:   "target_endpoint_recovered",
		TargetID: &targetID,
		Success:  true,
	})
	if err == nil {
		t.Fatal("endpoint mutation succeeded despite failed success audit")
	}
	target, err := store.GetTarget(ctx, "t", false)
	if err != nil {
		t.Fatal(err)
	}
	if target.Host != "host-a" || target.Port != 22 {
		t.Fatalf("endpoint mutation survived failed audit: %s:%d", target.Host, target.Port)
	}
}

func TestSyncGrantScopeAuditedPreservesUnchangedGrantState(t *testing.T) {
	store := newSecurityTestStore(t)
	ctx := context.Background()
	readID, err := store.AddGrant(ctx, Grant{ClientID: "c", TargetID: "t", ProjectID: "p", Capability: "read", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddGrant(ctx, Grant{ClientID: "c", TargetID: "t", ProjectID: "p", Capability: "write", Enabled: true}); err != nil {
		t.Fatal(err)
	}

	added, removed, err := store.SyncGrantScopeAudited(
		ctx, "c", "t", "p",
		[]string{"read", "write"},
		[]string{"read", "tasks"},
		Activity{Actor: "admin", Success: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 1 || added[0] != "tasks" || len(removed) != 1 || removed[0] != "write" {
		t.Fatalf("unexpected sync diff added=%v removed=%v", added, removed)
	}

	grants, err := store.ListGrants(ctx, "c")
	if err != nil {
		t.Fatal(err)
	}
	byCapability := map[string]Grant{}
	for _, grant := range grants {
		if grant.TargetID == "t" && grant.ProjectID == "p" {
			byCapability[grant.Capability] = grant
		}
	}
	if got := byCapability["read"]; got.ID != readID || got.Enabled {
		t.Fatalf("unchanged disabled read grant was rewritten: %+v", got)
	}
	if _, exists := byCapability["write"]; exists {
		t.Fatal("removed write grant still exists")
	}
	if got := byCapability["tasks"]; got.ID == 0 || !got.Enabled {
		t.Fatalf("new tasks grant must be enabled: %+v", got)
	}
	var addedActivities, removedActivities int
	if err := store.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM activity WHERE action = 'add_grant'").Scan(&addedActivities); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM activity WHERE action = 'delete_grant'").Scan(&removedActivities); err != nil {
		t.Fatal(err)
	}
	if addedActivities != 1 || removedActivities != 1 {
		t.Fatalf("granular grant activities add=%d delete=%d want 1/1", addedActivities, removedActivities)
	}
}

func TestSyncGrantScopeAuditedFailsClosedOnStaleExpectedSet(t *testing.T) {
	store := newSecurityTestStore(t)
	ctx := context.Background()
	for _, capability := range []string{"read", "write"} {
		if _, err := store.AddGrant(ctx, Grant{ClientID: "c", TargetID: "t", ProjectID: "p", Capability: capability, Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}

	_, _, err := store.SyncGrantScopeAudited(
		ctx, "c", "t", "p",
		[]string{"read"},
		[]string{"read", "tasks"},
		Activity{Actor: "admin", Success: true},
	)
	if ErrorCode(err) != "GRANT_SCOPE_CONFLICT" {
		t.Fatalf("err=%v code=%q want GRANT_SCOPE_CONFLICT", err, ErrorCode(err))
	}
	grants, err := store.ListGrants(ctx, "c")
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 2 || grants[0].Capability != "read" || grants[1].Capability != "write" {
		t.Fatalf("stale sync mutated scope: %+v", grants)
	}
}

func TestSyncGrantScopeAuditedRollsBackCompleteDiffWhenAuditFails(t *testing.T) {
	store := newSecurityTestStore(t)
	ctx := context.Background()
	for _, grant := range []Grant{
		{ClientID: "c", TargetID: "t", ProjectID: "p", Capability: "read", Enabled: false},
		{ClientID: "c", TargetID: "t", ProjectID: "p", Capability: "write", Enabled: true},
	} {
		if _, err := store.AddGrant(ctx, grant); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.DB().ExecContext(ctx,
		"CREATE TRIGGER block_scope_add_audit BEFORE INSERT ON activity WHEN NEW.action = 'add_grant' BEGIN SELECT RAISE(ABORT, 'blocked'); END;",
	); err != nil {
		t.Fatal(err)
	}

	_, _, err := store.SyncGrantScopeAudited(
		ctx, "c", "t", "p",
		[]string{"read", "write"},
		[]string{"read", "tasks"},
		Activity{Actor: "admin", Success: true},
	)
	if err == nil {
		t.Fatal("scope sync succeeded despite audit failure")
	}
	grants, err := store.ListGrants(ctx, "c")
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 2 || grants[0].Capability != "read" || grants[1].Capability != "write" || grants[0].Enabled {
		t.Fatalf("failed scope sync left partial mutation: %+v", grants)
	}
}

func TestSyncGrantScopeAuditedNoopDoesNotRewriteOrAudit(t *testing.T) {
	store := newSecurityTestStore(t)
	ctx := context.Background()
	id, err := store.AddGrant(ctx, Grant{ClientID: "c", TargetID: "t", ProjectID: "p", Capability: "read", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}

	added, removed, err := store.SyncGrantScopeAudited(
		ctx, "c", "t", "p",
		[]string{"read"},
		[]string{"read"},
		Activity{Actor: "admin", Success: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 0 || len(removed) != 0 {
		t.Fatalf("no-op sync produced changes added=%v removed=%v", added, removed)
	}
	grant, err := store.GetGrantForClient(ctx, "c", id)
	if err != nil {
		t.Fatal(err)
	}
	if grant.Enabled {
		t.Fatal("no-op sync changed disabled grant")
	}
	var activities int
	if err := store.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM activity WHERE action IN ('add_grant','delete_grant')").Scan(&activities); err != nil {
		t.Fatal(err)
	}
	if activities != 0 {
		t.Fatalf("no-op sync wrote %d grant activities", activities)
	}
}
