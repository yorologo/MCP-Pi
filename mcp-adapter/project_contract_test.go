package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mcp-gateway-adapter/internal/buildinfo"
	"mcp-gateway-adapter/internal/registry"
)

func readProjectJSON(t *testing.T, name string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return value
}

func TestProjectMetadataMatchesGoContract(t *testing.T) {
	compat := readProjectJSON(t, "compatibility.json")
	manifest := readProjectJSON(t, "manifest.json")

	if got := compat["gateway_version"]; got != buildinfo.GatewayVersion {
		t.Fatalf("compatibility gateway_version=%v want=%s", got, buildinfo.GatewayVersion)
	}
	if got := manifest["version"]; got != buildinfo.GatewayVersion {
		t.Fatalf("manifest version=%v want=%s", got, buildinfo.GatewayVersion)
	}
	if got := int(compat["registry_schema_version"].(float64)); got != registry.SchemaVersion {
		t.Fatalf("compatibility schema=%d want=%d", got, registry.SchemaVersion)
	}
	if got := int(manifest["registry_schema"].(float64)); got != registry.SchemaVersion {
		t.Fatalf("manifest schema=%d want=%d", got, registry.SchemaVersion)
	}
	if got := compat["runtime"].(map[string]any)["implementation"]; got != "go" {
		t.Fatalf("compatibility runtime implementation=%v want=go", got)
	}
	if got := manifest["runtime"]; got != "go" {
		t.Fatalf("manifest runtime=%v want=go", got)
	}
	if _, exists := manifest["minimum_python"]; exists {
		t.Fatal("manifest must not declare minimum_python")
	}
	if len(allKnownTools) != 21 {
		t.Fatalf("tool catalog count=%d want=21", len(allKnownTools))
	}
}

func TestFreshRegistrySafeDefaults(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "gateway.db")
	if err := registry.MigratePath(ctx, dbPath); err != nil {
		t.Fatal(err)
	}
	store, err := registry.OpenStore(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	for key, want := range map[string]string{
		"gateway_enabled": "true",
		"writes_enabled":  "false",
		"shell_enabled":   "false",
		"default_timeout": "30",
		"admin_timezone":  "UTC",
	} {
		got, err := store.GetSetting(ctx, key, "")
		if err != nil {
			t.Fatalf("get setting %s: %v", key, err)
		}
		if got != want {
			t.Fatalf("fresh setting %s=%q want=%q", key, got, want)
		}
	}
}

func TestCurrentDocumentationDescribesGoOnlyCandidate(t *testing.T) {
	current := []string{
		"README.md",
		"AGENTS.md",
		"CONTRIBUTING.md",
		"docs/README.md",
		"docs/installation.md",
		"docs/configuration.md",
		"docs/operations.md",
		"docs/recovery.md",
		"docs/troubleshooting.md",
		"docs/architecture.md",
		"docs/security.md",
		"docs/admin-console.md",
		"docs/project-state.md",
		"docs/reference/compatibility.md",
		"docs/reference/deployment.md",
		"docs/reference/client-grants.md",
		"docs/reference/controlled-write.md",
		"docs/reference/performance.md",
		"docs/reference/chatgpt-gate.md",
	}
	forbidden := []string{
		"Python 3.11+",
		"Python Gateway Core",
		"python scripts/audit-docs.py",
		"python -m unittest",
		"src/mcp_gateway",
		"scripts/benchmark_runtime.py",
		"Flask Admin",
		"Flask session",
		"go-only-migration",
		"backup-appliance.sh",
		"build-armv6.sh",
		"mcp-gateway repair",
		"--skip-password",
	}
	for _, rel := range current {
		data, err := os.ReadFile(filepath.Join("..", filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read current documentation %s: %v", rel, err)
		}
		text := string(data)
		for _, stale := range forbidden {
			if strings.Contains(text, stale) {
				t.Errorf("%s contains stale active-runtime text %q", rel, stale)
			}
		}
	}
	for _, rel := range []string{"README.md", "docs/installation.md", "docs/project-state.md"} {
		data, _ := os.ReadFile(filepath.Join("..", filepath.FromSlash(rel)))
		if !strings.Contains(string(data), buildinfo.GatewayVersion) {
			t.Errorf("%s does not mention candidate %s", rel, buildinfo.GatewayVersion)
		}
	}
}

func TestOperationalContractsRemainExplicit(t *testing.T) {
	checks := map[string][]string{
		"install.sh": {
			"--check",
			"--rollback",
			"INSTALL_VERIFIED",
			"MCP_ADMIN_HOST=127.0.0.1",
			"mcp-gateway.previous-install",
		},
		"scripts/deploy-pi.sh": {
			"MCP_PI_RESUMABLE_JOB_ID",
			"MCP_DEPLOY_ALLOW_DIRECT",
			"CONTROL_PLANE_RESTART=EXPECTED",
			"CONTROL_PLANE_RESTORED",
			"ROLLBACK_VERIFIED",
			`"runtime": "go-only"`,
			"umask 022",
		},
		"scripts/build-release-package.sh": {
			"release packaging requires a clean worktree",
			"GOARCH=arm GOARM=6",
			"runtime=go-only",
			"chmod -R u=rwX,go=rX",
		},
	}
	for rel, required := range checks {
		data, err := os.ReadFile(filepath.Join("..", filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		for _, token := range required {
			if !strings.Contains(text, token) {
				t.Errorf("%s missing contract token %q", rel, token)
			}
		}
	}
}

func TestPostbootWaitsForRealMCPReadiness(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "config", "systemd", "mcp-gateway-postboot.service"))
	if err != nil {
		t.Fatal(err)
	}
	unit := string(data)
	for _, required := range []string{
		"ExecStartPre=/usr/bin/curl",
		"--retry 60",
		"--retry-max-time 90",
		"--retry-connrefused",
		"--max-time 2",
		"http://127.0.0.1:8090/ready",
	} {
		if !strings.Contains(unit, required) {
			t.Errorf("postboot service missing readiness gate token %q", required)
		}
	}
	if strings.Contains(unit, "ExecStartPre=/bin/sleep") {
		t.Error("postboot readiness must not depend on a fixed sleep")
	}
}

func TestGeminiIngressSupportsPrivateTunnelRoute(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "config", "systemd", "mcp-gateway-gemini.service"))
	if err != nil {
		t.Fatal(err)
	}
	unit := string(data)
	for _, required := range []string{
		"-bind 0.0.0.0:8092",
		"-auth-token-file /home/mcp-gateway/.config/mcp-gateway/gemini-mcp.token",
		"-client-id gemini-main",
		"-allowed-hosts gemini-mcp.internal",
	} {
		if !strings.Contains(unit, required) {
			t.Errorf("Gemini service missing private-tunnel contract token %q", required)
		}
	}
}

func TestCloudflaredConnectorLifecycleContract(t *testing.T) {
	unitData, err := os.ReadFile(filepath.Join("..", "config", "systemd", "mcp-gateway-cloudflared.service"))
	if err != nil {
		t.Fatal(err)
	}
	unit := string(unitData)
	for _, required := range []string{
		"Wants=network-online.target mcp-gateway-gemini.service",
		"After=network-online.target mcp-gateway-gemini.service",
		"ExecCondition=/usr/bin/test -s /home/mcp-gateway/.config/mcp-gateway/cloudflared.token",
		"ExecStart=/usr/local/bin/cloudflared tunnel --no-autoupdate run --token-file /home/mcp-gateway/.config/mcp-gateway/cloudflared.token",
		"Restart=on-failure",
	} {
		if !strings.Contains(unit, required) {
			t.Errorf("cloudflared unit missing %q", required)
		}
	}
	if strings.Contains(unit, "Requires=mcp-gateway-gemini.service") {
		t.Error("cloudflared must not stop permanently when Gemini is restarted")
	}

	installData, err := os.ReadFile(filepath.Join("..", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(installData), "systemctl is-enabled --quiet mcp-gateway-cloudflared") {
		t.Error("installer must preserve explicitly enabled cloudflared service")
	}

	deployData, err := os.ReadFile(filepath.Join("..", "scripts", "deploy-pi.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(deployData), "CLOUDFLARED_WAS_ENABLED") {
		t.Error("deploy acceptance must preserve cloudflared enabled state")
	}
}

func TestLifecyclePreservesRollbackRegistryBeforeMigration(t *testing.T) {
	readText := func(rel string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join("..", filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		return string(data)
	}

	installer := readText("install.sh")
	activation := strings.Index(installer, "Activating root-owned runtime and migrating Registry with services stopped")
	if activation < 0 {
		t.Fatal("installer activation/migration boundary is missing")
	}
	stopRel := strings.Index(installer[activation:], "systemctl stop mcp-gateway-maintenance.timer mcp-gateway-maintenance.service mcp-gateway-tunnel mcp-gateway-cloudflared mcp-gateway-gemini mcp-gateway-postboot mcp-gateway-mcp mcp-gateway-admin")
	migrateRel := strings.Index(installer[activation:], "migrate -db \"$DB_PATH\"")
	if stopRel < 0 || migrateRel < 0 || stopRel >= migrateRel {
		t.Fatal("installer must stop database users before explicit Registry migration")
	}
	if strings.Contains(installer[:activation], "migrate -db \"$DB_PATH\"") {
		t.Fatal("installer must not migrate the live Registry before the activation stop boundary")
	}
	if !strings.Contains(installer, "verify_source_integrity") || !strings.Contains(installer, "sha256sum -c SHA256SUMS") {
		t.Fatal("installer must verify release-bundle checksums before mutation")
	}
	if !strings.Contains(installer, "json_number_array_file") || !strings.Contains(installer, "registry_upgrade_from") {
		t.Fatal("installer backup schema validation must reuse the candidate registry_upgrade_from contract")
	}
	if strings.Contains(installer, "schema: (4|5)") {
		t.Fatal("installer backup schema validation must not freeze a historical schema allowlist")
	}

	deployer := readText("scripts/deploy-pi.sh")
	for _, forbidden := range []string{
		"backup -db \"$db\"",
		"restore -db \"$db\"",
		"migrate -db \"$db\"",
		"sudo mv \"$candidate\" \"$current\"",
	} {
		if strings.Contains(deployer, forbidden) {
			t.Fatalf("deployment must delegate lifecycle mutation to install.sh; found %q", forbidden)
		}
	}
	if !strings.Contains(deployer, "sudo \"$candidate/install.sh\" --check") {
		t.Fatal("deployment must validate the canonical candidate installer")
	}
	if !strings.Contains(deployer, "sudo \"$candidate/install.sh\" --no-setup") {
		t.Fatal("deployment must use install.sh as the single activation/update engine")
	}
	if !strings.Contains(deployer, "sudo '$REMOTE_TARGET_DIR/install.sh' --rollback") {
		t.Fatal("deployment rollback must reuse the installed canonical installer")
	}

	candidateValidation := []string{
		"sha256sum \"$upload/release.tar.gz\"",
		"sudo sha256sum \"$candidate/bin/mcp-gateway-adapter\"",
		"sudo \"$candidate/install.sh\" --check",
		"sudo find \"$candidate\"",
		"sudo systemd-analyze verify \"$candidate/config/systemd/$unit\"",
	}
	for _, required := range candidateValidation {
		if !strings.Contains(deployer, required) {
			t.Errorf("deployment candidate validation missing %q", required)
		}
	}
}
