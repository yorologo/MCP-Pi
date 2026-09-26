package main

import (
	"context"
	"path/filepath"
	"testing"

	"mcp-gateway-adapter/internal/registry"
)

func createSeededCLIDatabase(t *testing.T) string {
	t.Helper()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "gateway.db")

	ctx := context.Background()
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

func TestCLIRepair(t *testing.T) {
	dbPath := createSeededCLIDatabase(t)
	rc := cmdRepair([]string{"-db", dbPath})
	if rc != 0 {
		t.Fatalf("cmdRepair returned %d, expected 0", rc)
	}
}

func TestCLIBenchmark(t *testing.T) {
	dbPath := createSeededCLIDatabase(t)
	rc := cmdBenchmark([]string{"-db", dbPath, "-n", "20"})
	if rc != 0 {
		t.Fatalf("cmdBenchmark returned %d, expected 0", rc)
	}
}

func TestCLISetup(t *testing.T) {
	dbPath := createSeededCLIDatabase(t)
	rc := cmdSetup([]string{"-db", dbPath, "--admin-user", "testadmin", "--skip-password"})
	if rc != 0 {
		t.Fatalf("cmdSetup returned %d, expected 0", rc)
	}
}
