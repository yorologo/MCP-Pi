package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func TestServiceRoleInventoryIncludesOptionalIngressAndRestoreQuiescence(t *testing.T) {
	contains := func(items []string, want string) bool {
		for _, item := range items {
			if item == want {
				return true
			}
		}
		return false
	}

	observed := ApplianceObservedServiceUnits()
	for _, unit := range []string{gatewayGeminiServiceUnit, gatewayCloudflaredServiceUnit, gatewayTunnelServiceUnit} {
		if !contains(observed, unit) {
			t.Fatalf("observed service inventory missing %s: %v", unit, observed)
		}
	}

	quiescence := RegistryQuiescenceServiceUnits()
	for _, unit := range []string{gatewayAdminServiceUnit, gatewayMCPServiceUnit, gatewayGeminiServiceUnit, gatewayMaintenanceServiceUnit, gatewayMaintenanceTimerUnit, gatewayPostbootServiceUnit} {
		if !contains(quiescence, unit) {
			t.Fatalf("restore quiescence inventory missing %s: %v", unit, quiescence)
		}
	}
	for _, edgeOnly := range []string{gatewayTunnelServiceUnit, gatewayCloudflaredServiceUnit} {
		if contains(quiescence, edgeOnly) {
			t.Fatalf("edge-only service %s must not be classified as a Registry user", edgeOnly)
		}
	}
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
	if err := os.MkdirAll(filepath.Join(runtimeDir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	adapterContent := []byte("adapter-binary")
	sum := sha256.Sum256(adapterContent)
	adapterSHA := hex.EncodeToString(sum[:])
	if err := os.WriteFile(filepath.Join(runtimeDir, "bin", "mcp-gateway-adapter"), adapterContent, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, ".deployed-git-sha"), []byte("abc123\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	content := fmt.Sprintf(`{"commit":"abc123","branch":"develop","deployed_at":"2026-09-27T03:35:13Z","adapter_sha256":%q,"runtime":"go-only","verified":true}`, adapterSHA)
	if err := os.WriteFile(filepath.Join(runtimeDir, ".deployment.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	got := loadDeploymentProvenance()
	if got["available"] != true || got["commit"] != "abc123" || got["branch"] != "develop" || got["verified"] != true {
		t.Fatalf("unexpected deployment provenance: %#v", got)
	}
	if got["recorded_verified"] != true || got["live_adapter_sha256"] != adapterSHA {
		t.Fatalf("live verification evidence missing: %#v", got)
	}
}

func TestLoadDeploymentProvenanceRejectsStaleAcceptedRecord(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MCP_GATEWAY_HOME", home)
	runtimeDir := filepath.Join(home, "mcp-gateway")
	if err := os.MkdirAll(filepath.Join(runtimeDir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "bin", "mcp-gateway-adapter"), []byte("changed"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, ".deployed-git-sha"), []byte("abc123\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	content := `{"commit":"abc123","adapter_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","runtime":"go-only","verified":true}`
	if err := os.WriteFile(filepath.Join(runtimeDir, ".deployment.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	got := loadDeploymentProvenance()
	if got["recorded_verified"] != true || got["verified"] != false {
		t.Fatalf("stale accepted record must not verify live: %#v", got)
	}
	if !strings.Contains(fmt.Sprint(got["verification_message"]), "does not match") {
		t.Fatalf("missing mismatch evidence: %#v", got)
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

func TestInspectCloudflaredDependencyReportsSafeFingerprint(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "cloudflared")
	token := filepath.Join(dir, "cloudflared.token")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\necho 'cloudflared version test-1'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(token, []byte("secret-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := inspectCloudflaredDependency(context.Background(), binary, token)
	if !got.Passed {
		t.Fatalf("dependency evidence failed: %+v", got)
	}
	if got.Version != "" || len(got.SHA256) != 64 || got.TokenMode != "0600" {
		t.Fatalf("unexpected dependency evidence: %+v", got)
	}
	if strings.Contains(got.Message, "secret-token") || strings.Contains(got.Version, "secret-token") {
		t.Fatal("dependency evidence leaked token content")
	}
}

func TestInspectCloudflaredDependencyDoesNotExecuteBinaryForFingerprint(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "cloudflared")
	token := filepath.Join(dir, "cloudflared.token")
	marker := filepath.Join(dir, "executed")
	script := "#!/bin/sh\nprintf executed > " + marker + "\n"
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(token, []byte("secret-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := inspectCloudflaredDependency(context.Background(), binary, token)
	if !got.Passed {
		t.Fatalf("dependency evidence failed: %+v", got)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("cloudflared binary was executed during provenance inspection: %v", err)
	}
	if !strings.Contains(got.Message, "SHA-256 fingerprint") {
		t.Fatalf("dependency evidence did not explain provenance basis: %+v", got)
	}
}

func TestInspectCloudflaredDependencyRejectsBroadTokenPermissions(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "cloudflared")
	token := filepath.Join(dir, "cloudflared.token")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\necho 'cloudflared version test-1'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(token, []byte("secret-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(token, 0o644); err != nil {
		t.Fatal(err)
	}

	got := inspectCloudflaredDependency(context.Background(), binary, token)
	if got.Passed || !strings.Contains(got.Message, "permissions are too broad") {
		t.Fatalf("broad token permissions were not rejected: %+v", got)
	}
}

func TestSummarizeDoctorChecksRequiredFailureWinsOverWarningSeverity(t *testing.T) {
	passed, failed, warnings := summarizeDoctorChecks([]DoctorCheck{
		{Name: "required", Passed: false, Severity: "warning", Required: true},
		{Name: "optional", Passed: false, Severity: "warning", Required: false},
		{Name: "healthy", Passed: true, Severity: "error", Required: true},
	})
	if passed != 1 || failed != 1 || warnings != 1 {
		t.Fatalf("unexpected Doctor summary: passed=%d failed=%d warnings=%d", passed, failed, warnings)
	}
}

func TestPrivilegeHygieneFlagsPersistentHighImpactState(t *testing.T) {
	core, ctx, _, _ := seededApplianceCore(t)
	if _, err := core.store.DB().ExecContext(ctx,
		"INSERT INTO targets(id,display_name,platform,host,port,user,privilege_policy,enabled) VALUES ('breakglass','Break Glass','linux','127.0.0.1',22,'root','always_allow',1)"); err != nil {
		t.Fatal(err)
	}
	if _, err := core.store.DB().ExecContext(ctx,
		"INSERT INTO ai_clients(id,display_name,provider,protocol,enabled) VALUES ('ops-client','Ops Client','test','mcp',1)"); err != nil {
		t.Fatal(err)
	}
	if _, err := core.store.AddGrant(ctx, registry.Grant{
		ClientID: "ops-client", TargetID: "*", ProjectID: "*",
		Capability: "target_admin", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	checks := core.privilegeHygieneChecks(ctx)
	if len(checks) != 2 {
		t.Fatalf("hygiene checks=%d want=2: %+v", len(checks), checks)
	}
	if checks[0].Passed || !strings.Contains(checks[0].Message, "breakglass") {
		t.Fatalf("always_allow Target was not flagged: %+v", checks[0])
	}
	if checks[1].Passed || !strings.Contains(checks[1].Message, "ops-client:*/*") {
		t.Fatalf("wildcard target_admin grant was not flagged: %+v", checks[1])
	}
}

func TestPrivilegeHygienePassesOnConservativeDefaults(t *testing.T) {
	core, ctx, _, _ := seededApplianceCore(t)
	checks := core.privilegeHygieneChecks(ctx)
	if len(checks) != 2 || !checks[0].Passed || !checks[1].Passed {
		t.Fatalf("conservative defaults failed privilege hygiene: %+v", checks)
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

func TestGatewayDoctorRequiredFailureIsToolErrorWithDiagnostics(t *testing.T) {
	core, ctx, _, _ := seededApplianceCore(t)
	if _, err := core.store.DB().ExecContext(ctx, `
INSERT INTO targets(id, display_name, platform, host, port, user, privilege_policy, enabled)
VALUES ('offline', 'Offline', 'linux', '127.0.0.1', 22, 'user', 'never', 1)
`); err != nil {
		t.Fatal(err)
	}

	resp := core.GatewayDoctor(ctx, "req-doc-error", DoctorOptions{CheckTargets: true})
	if resp.OK {
		t.Fatalf("required Doctor failure must be a tool error: %+v", resp)
	}
	resMap := resp.Result.(map[string]any)
	if resMap["status"] != "ERROR" {
		t.Fatalf("Doctor diagnostics not preserved: %+v", resMap)
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

func TestManagedBackupRetentionProtectsActiveRollbackSnapshot(t *testing.T) {
	root := t.TempDir()
	backupsDir := filepath.Join(root, "backups")
	if err := os.MkdirAll(backupsDir, 0o700); err != nil {
		t.Fatal(err)
	}

	var files []string
	for i := 0; i < 5; i++ {
		path := filepath.Join(backupsDir, fmt.Sprintf("gateway-pre-install-%02d.db", i))
		if err := os.WriteFile(path, []byte("backup"), 0o600); err != nil {
			t.Fatal(err)
		}
		stamp := time.Unix(int64(100+i), 0)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		files = append(files, path)
	}
	protected := files[0]
	stateDir := filepath.Join(root, "install-unit-backup")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "registry-backup.path"), []byte(protected+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manual := filepath.Join(backupsDir, "manual-important.db")
	if err := os.WriteFile(manual, []byte("manual"), 0o600); err != nil {
		t.Fatal(err)
	}

	gotProtected := currentRollbackBackup(backupsDir, root)
	if gotProtected != protected {
		t.Fatalf("protected rollback backup=%q want %q", gotProtected, protected)
	}
	pruned, err := pruneManagedBackups(files, 3, gotProtected)
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 1 {
		t.Fatalf("pruned=%d want 1", pruned)
	}
	if _, err := os.Stat(protected); err != nil {
		t.Fatalf("active rollback snapshot was pruned: %v", err)
	}
	if _, err := os.Stat(manual); err != nil {
		t.Fatalf("manual backup was touched: %v", err)
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
