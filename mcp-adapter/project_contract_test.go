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
	store, err := registry.OpenStore(ctx, filepath.Join(t.TempDir(), "gateway.db"))
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
		"docs/getting-started.md",
		"docs/installation.md",
		"docs/configuration.md",
		"docs/operations.md",
		"docs/update-rollback.md",
		"docs/recovery.md",
		"docs/troubleshooting.md",
		"docs/architecture.md",
		"docs/security.md",
		"docs/admin-console.md",
		"docs/project-state.md",
		"docs/reference/compatibility.md",
		"docs/reference/mcp-adapter.md",
		"docs/reference/lifecycle.md",
		"docs/reference/deployment.md",
		"docs/reference/controlled-write.md",
		"docs/reference/performance.md",
		"docs/reference/diagrams.md",
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
	for _, rel := range []string{"README.md", "docs/installation.md", "docs/architecture.md", "docs/project-state.md"} {
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
	stopRel := strings.Index(installer[activation:], "systemctl stop mcp-gateway-tunnel mcp-gateway-postboot mcp-gateway-mcp mcp-gateway-admin")
	migrateRel := strings.Index(installer[activation:], "status -db \"$DB_PATH\"")
	if stopRel < 0 || migrateRel < 0 || stopRel >= migrateRel {
		t.Fatal("installer must stop database users before opening/migrating the live Registry")
	}
	if strings.Contains(installer[:activation], "status -db \"$DB_PATH\"") {
		t.Fatal("installer must not migrate the live Registry before the activation stop boundary")
	}

	deployer := readText("scripts/deploy-pi.sh")
	if strings.Contains(deployer, "status -db \"$registry_backup\"") {
		t.Fatal("deployment must not migrate the rollback Registry backup during validation")
	}
	if !strings.Contains(deployer, "SQLite integrity: ok; schema: (4|5);") {
		t.Fatal("deployment must validate rollback backup integrity/schema without migration")
	}

	candidateValidation := []string{
		`sudo sha256sum "$candidate/bin/mcp-gateway-adapter"`,
		`sudo -u mcp-gateway "$candidate/bin/mcp-gateway-adapter" version --json`,
		`sudo "$candidate/install.sh" --check`,
		`sudo find "$candidate"`,
		`sudo systemd-analyze verify "$candidate/config/systemd/$unit"`,
	}
	for _, required := range candidateValidation {
		if !strings.Contains(deployer, required) {
			t.Errorf("root-owned deployment candidate validation missing %q", required)
		}
	}
}
