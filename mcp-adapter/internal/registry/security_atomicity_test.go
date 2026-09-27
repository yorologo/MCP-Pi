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
