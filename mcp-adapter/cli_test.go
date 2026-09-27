package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"mcp-gateway-adapter/internal/registry"
	"mcp-gateway-adapter/internal/sqliteutil"
)

func createSeededCLIDatabase(t *testing.T) string {
	t.Helper()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "gateway.db")

	ctx := context.Background()
	if err := registry.MigratePath(ctx, dbPath); err != nil {
		t.Fatalf("failed to initialize registry: %v", err)
	}
	store, err := registry.OpenStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to open registry store: %v", err)
	}

	_, _ = store.DB().ExecContext(ctx, `
		INSERT INTO targets(id, display_name, platform, host, port, user, privilege_policy, enabled)
		VALUES ('test-target', 'Test Target', 'linux', '127.0.0.1', 22, 'testuser', 'never', 1)
	`)
	_, _ = store.DB().ExecContext(ctx, `
		INSERT INTO projects(id, target_id, display_name, root, read_enabled, write_enabled, enabled)
		VALUES ('test-proj', 'test-target', 'Test Project', '/srv/test', 1, 0, 1)
	`)
	_, _ = store.DB().ExecContext(ctx, `
		INSERT INTO ai_clients(id, display_name, enabled)
		VALUES ('test-client', 'Test Client', 1)
	`)

	return dbPath
}

func TestCLIStatus(t *testing.T) {
	dbPath := createSeededCLIDatabase(t)
	rc := cmdStatus([]string{"-db", dbPath})
	if rc != 0 {
		t.Fatalf("cmdStatus returned %d, expected 0", rc)
	}
}

func TestCLIDoctor(t *testing.T) {
	dbPath := createSeededCLIDatabase(t)
	rc := cmdDoctor([]string{"-db", dbPath, "-verbose"})
	if rc != 0 {
		t.Fatalf("cmdDoctor returned %d, expected 0", rc)
	}
}

func TestCLIMaintenance(t *testing.T) {
	dbPath := createSeededCLIDatabase(t)
	rc := cmdMaintenance([]string{"-db", dbPath})
	if rc != 0 {
		t.Fatalf("cmdMaintenance returned %d, expected 0", rc)
	}
}

func TestCLIBackupAndRestore(t *testing.T) {
	dbPath := createSeededCLIDatabase(t)
	backupDst := filepath.Join(t.TempDir(), "manual_backup.db")

	rc := cmdBackup([]string{"-db", dbPath, backupDst})
	if rc != 0 {
		t.Fatalf("cmdBackup returned %d, expected 0", rc)
	}

	targetRestoreDB := filepath.Join(t.TempDir(), "restored.db")
	rc = cmdRestore([]string{"-db", targetRestoreDB, backupDst})
	if rc != 0 {
		t.Fatalf("cmdRestore returned %d, expected 0", rc)
	}

	// Verify restored db can be queried
	ctx := context.Background()
	store, err := registry.OpenStore(ctx, targetRestoreDB)
	if err != nil {
		t.Fatalf("failed to open restored store: %v", err)
	}
	target, err := store.GetTarget(ctx, "test-target", true)
	if err != nil || target.ID == "" {
		t.Fatalf("restored db missing test-target: %v", err)
	}
}

func TestCLIBenchmark(t *testing.T) {
	dbPath := createSeededCLIDatabase(t)
	rc := cmdBenchmark([]string{"-db", dbPath, "-n", "20"})
	if rc != 0 {
		t.Fatalf("cmdBenchmark returned %d, expected 0", rc)
	}
}

func TestCLISetupExistingAdminIsIdempotent(t *testing.T) {
	dbPath := createSeededCLIDatabase(t)
	ctx := context.Background()
	store, err := registry.OpenStore(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetAdminPassword(ctx, "testadmin", "existing-hash"); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()

	rc := cmdSetup([]string{"-db", dbPath, "--admin-user", "testadmin"})
	if rc != 0 {
		t.Fatalf("cmdSetup returned %d, expected 0", rc)
	}
}

func TestCLIStatusDoesNotMigrateOutdatedRegistry(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "v4.db")
	db, err := sqliteutil.OpenRaw(ctx, dbPath, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA user_version = 4"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if rc := cmdStatus([]string{"-db", dbPath}); rc == 0 {
		t.Fatal("status must reject an outdated Registry instead of migrating it")
	}
	info, err := sqliteutil.Inspect(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.SchemaVersion != 4 {
		t.Fatalf("status changed schema to %d want 4", info.SchemaVersion)
	}
}

func TestCLIRestorePreservesSourceSchema(t *testing.T) {
	ctx := context.Background()
	source := filepath.Join(t.TempDir(), "backup-v4.db")
	db, err := sqliteutil.OpenRaw(ctx, source, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA user_version = 4; CREATE TABLE marker(v TEXT); INSERT INTO marker(v) VALUES ('preserve');"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(t.TempDir(), "restored.db")
	if rc := cmdRestore([]string{"-db", target, source}); rc != 0 {
		t.Fatalf("restore returned %d want 0", rc)
	}
	info, err := sqliteutil.Inspect(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	if info.SchemaVersion != 4 {
		t.Fatalf("restore migrated schema to %d want 4", info.SchemaVersion)
	}
}

func TestCLISetupRejectsRemovedSkipPasswordPath(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gateway.db")
	if rc := cmdSetup([]string{"-db", dbPath, "--skip-password"}); rc != 2 {
		t.Fatalf("setup --skip-password returned %d want usage error 2", rc)
	}
}

func TestCLIMigrateIsExplicitSchemaBoundary(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "fresh.db")
	if rc := cmdMigrate([]string{"-db", dbPath}); rc != 0 {
		t.Fatalf("migrate returned %d want 0", rc)
	}
	info, err := sqliteutil.Inspect(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.SchemaVersion != registry.SchemaVersion {
		t.Fatalf("migrate created schema %d want %d", info.SchemaVersion, registry.SchemaVersion)
	}
	if info.Integrity != "ok" {
		t.Fatalf("migrate created Registry with integrity=%s", info.Integrity)
	}
}

func TestCLISetupRequiresCurrentRegistry(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "missing.db")
	if rc := cmdSetup([]string{"-db", dbPath, "--admin-user", "admin"}); rc == 0 {
		t.Fatal("setup unexpectedly created or migrated a missing Registry")
	}
	if _, err := filepath.Abs(dbPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("setup mutated missing Registry path; stat err=%v", err)
	}
}
