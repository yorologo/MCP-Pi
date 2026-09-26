package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"mcp-gateway-adapter/internal/registry"
)

func seededApplianceCore(t *testing.T) (*Core, context.Context, string, string) {
	t.Helper()
	ctx := context.Background()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "gateway.db")
	backupDir := filepath.Join(tmpDir, "backups")

	db, err := registry.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}

	store := registry.NewStore(db)
	core := New(store, Config{
		GatewayVersion:     "1.4.0",
		CoreAPIVersion:     1,
		ToolCatalogVersion: 4,
		MCPProtocol:        "2026-07-28",
		DBPath:             dbPath,
		BackupDir:          backupDir,
	})

	t.Cleanup(func() { _ = db.Close() })
	return core, ctx, dbPath, backupDir
}

func TestGatewayStatus(t *testing.T) {
	core, ctx, dbPath, _ := seededApplianceCore(t)

	resp := core.GatewayStatus(ctx, "req-status")
	if !resp.OK {
		t.Fatalf("GatewayStatus failed: %+v", resp.Error)
	}

	resMap, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", resp.Result)
	}
	if resMap["gateway_status"] != "ok" || resMap["gateway_version"] != "1.4.0" {
		t.Fatalf("unexpected status result: %+v", resMap)
	}
	dbInfo, ok := resMap["database"].(map[string]any)
	if !ok || dbInfo["path"] != dbPath {
		t.Fatalf("unexpected database info: %+v", dbInfo)
	}
}

func TestGatewayBackup(t *testing.T) {
	core, ctx, _, backupDir := seededApplianceCore(t)

	resp := core.GatewayBackup(ctx, "req-backup", "test-admin", "")
	if !resp.OK {
		t.Fatalf("GatewayBackup failed: %+v", resp.Error)
	}

	resMap := resp.Result.(map[string]any)
	bakPath, ok := resMap["backup_path"].(string)
	if !ok || bakPath == "" {
		t.Fatalf("missing backup_path in result: %+v", resMap)
	}
	if filepath.Dir(bakPath) != backupDir {
		t.Fatalf("backup path %s not in backupDir %s", bakPath, backupDir)
	}
	if _, err := os.Stat(bakPath); err != nil {
		t.Fatalf("backup file does not exist on disk: %v", err)
	}
	sha, ok := resMap["sha256"].(string)
	if !ok || len(sha) != 64 {
		t.Fatalf("invalid sha256 in backup result: %v", sha)
	}
}

func TestGatewayDoctor(t *testing.T) {
	core, ctx, _, _ := seededApplianceCore(t)

	resp := core.GatewayDoctor(ctx, "req-doc")
	if !resp.OK {
		t.Fatalf("GatewayDoctor failed: %+v", resp.Error)
	}

	resMap := resp.Result.(map[string]any)
	if resMap["status"] != "HEALTHY" && resMap["status"] != "WARNING" {
		t.Fatalf("unexpected doctor status: %+v", resMap["status"])
	}
	checks, ok := resMap["checks"].([]DoctorCheck)
	if !ok || len(checks) == 0 {
		t.Fatalf("expected non-empty checks array, got %v", resMap["checks"])
	}
	controlPath, ok := resMap["control_path"].(map[string]any)
	if !ok || controlPath["gateway_core"] == nil {
		t.Fatalf("expected control_path with gateway_core, got %+v", controlPath)
	}
}

func TestGatewayMaintenance(t *testing.T) {
	core, ctx, _, backupDir := seededApplianceCore(t)

	resp := core.GatewayMaintenance(ctx, "req-maint", "test-admin")
	if !resp.OK {
		t.Fatalf("GatewayMaintenance failed: %+v", resp.Error)
	}

	resMap := resp.Result.(map[string]any)
	if resMap["message"] != "Appliance maintenance executed successfully" {
		t.Fatalf("unexpected maintenance message: %+v", resMap)
	}
	if resMap["database_integrity"] != "ok" {
		t.Fatalf("unexpected integrity: %+v", resMap["database_integrity"])
	}

	// Verify backup was created
	entries, err := os.ReadDir(backupDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("expected backups created in %s, got err=%v count=%d", backupDir, err, len(entries))
	}
}

func TestGatewayReboot(t *testing.T) {
	core, ctx, _, _ := seededApplianceCore(t)

	// Refused without confirm=true
	resp := core.GatewayReboot(ctx, "req-reboot-1", "test-admin", false)
	if resp.OK || resp.Error.Code != "INVALID_ARGUMENTS" {
		t.Fatalf("expected INVALID_ARGUMENTS without confirm, got: %+v", resp)
	}

	// Accepted with confirm=true
	resp = core.GatewayReboot(ctx, "req-reboot-2", "test-admin", true)
	if !resp.OK {
		t.Fatalf("expected OK with confirm=true, got: %+v", resp.Error)
	}
	resMap := resp.Result.(map[string]any)
	if resMap["requested_by"] != "test-admin" {
		t.Fatalf("unexpected requested_by: %+v", resMap)
	}
}
