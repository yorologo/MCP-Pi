package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mcp-gateway-adapter/internal/buildinfo"
	"mcp-gateway-adapter/internal/registry"
)

func seededApplianceCore(t *testing.T) (*Core, context.Context, string, string) {
	t.Helper()
	ctx := context.Background()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "gateway.db")
	backupDir := filepath.Join(tmpDir, "backups")

	if err := registry.MigratePath(ctx, dbPath); err != nil {
		t.Fatal(err)
	}
	db, err := registry.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}

	store := registry.NewStore(db)
	core := New(store, Config{
		GatewayVersion:     buildinfo.GatewayVersion,
		CoreAPIVersion:     buildinfo.CoreAPIVersion,
		ToolCatalogVersion: buildinfo.ToolCatalogVersion,
		MCPProtocol:        buildinfo.MCPProtocol,
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
	if resMap["gateway_status"] != "ok" || resMap["gateway_version"] != buildinfo.GatewayVersion {
		t.Fatalf("unexpected status result: %+v", resMap)
	}
	dbInfo, ok := resMap["database"].(map[string]any)
	if !ok || dbInfo["path"] != dbPath {
		t.Fatalf("unexpected database info: %+v", dbInfo)
	}
}

func TestLoadDeploymentProvenanceFromRuntime(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MCP_GATEWAY_HOME", home)
	runtimeDir := filepath.Join(home, "mcp-gateway")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `{"commit":"abc123","branch":"develop","deployed_at":"2026-09-27T03:35:13Z","verified":true}`
	if err := os.WriteFile(filepath.Join(runtimeDir, ".deployment.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	got := loadDeploymentProvenance()
	if got["available"] != true || got["commit"] != "abc123" || got["branch"] != "develop" || got["verified"] != true {
		t.Fatalf("unexpected deployment provenance: %#v", got)
	}
}

func TestLoadDeploymentProvenanceMissing(t *testing.T) {
	t.Setenv("MCP_GATEWAY_HOME", t.TempDir())
	got := loadDeploymentProvenance()
	if got["available"] != false {
		t.Fatalf("missing deployment provenance must be unavailable: %#v", got)
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

	resp := core.GatewayDoctor(ctx, "req-doc", DoctorOptions{})
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

func TestGatewayDoctorUsesTargetReachabilityTimeout(t *testing.T) {
	c, fake, _ := seededRemoteCore(t)
	resp := c.GatewayDoctor(context.Background(), "req-doc-targets", DoctorOptions{CheckTargets: true})
	if !resp.OK {
		t.Fatalf("GatewayDoctor failed: %+v", resp.Error)
	}
	if fake.lastCommandTimeout != targetReachabilityTimeout {
		t.Fatalf("doctor target timeout=%s want=%s", fake.lastCommandTimeout, targetReachabilityTimeout)
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

	previous := scheduleReboot
	t.Cleanup(func() { scheduleReboot = previous })
	scheduleReboot = func(context.Context, time.Time) error { return nil }

	resp = core.GatewayReboot(ctx, "req-reboot-2", "test-admin", true)
	if !resp.OK {
		t.Fatalf("expected accepted scheduled reboot, got: %+v", resp.Error)
	}
	resMap := resp.Result.(map[string]any)
	if resMap["reboot_scheduled"] != true || resMap["requested_by"] != "test-admin" {
		t.Fatalf("unexpected reboot result: %+v", resMap)
	}

	scheduleReboot = func(context.Context, time.Time) error { return fmt.Errorf("denied") }
	resp = core.GatewayReboot(ctx, "req-reboot-3", "test-admin", true)
	if resp.OK || resp.Error.Code != "REBOOT_SCHEDULE_FAILED" {
		t.Fatalf("expected REBOOT_SCHEDULE_FAILED, got: %+v", resp)
	}
}
