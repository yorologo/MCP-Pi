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
