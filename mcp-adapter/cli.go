package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"mcp-gateway-adapter/internal/admin"
	"mcp-gateway-adapter/internal/buildinfo"
	"mcp-gateway-adapter/internal/core"
	"mcp-gateway-adapter/internal/discovery"
	"mcp-gateway-adapter/internal/registry"
	"mcp-gateway-adapter/internal/remote"
	"mcp-gateway-adapter/internal/sqliteutil"

	"golang.org/x/term"
)

func resolveDBPath(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if env := os.Getenv("MCP_GATEWAY_DB"); env != "" {
		return env
	}
	home := os.Getenv("MCP_GATEWAY_HOME")
	if home == "" {
		home = "/home/mcp-gateway"
	}
	defaultPath := filepath.Join(home, ".local", "share", "mcp-gateway", "gateway.db")
	if _, err := os.Stat(defaultPath); err == nil {
		return defaultPath
	}
	if _, err := os.Stat("gateway.db"); err == nil {
		return "gateway.db"
	}
	return defaultPath
}

func initStoreAndCore(dbPath string) (*registry.Store, *core.Core, error) {
	store, err := registry.OpenStore(context.Background(), dbPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open registry store: %w", err)
	}
	transport := remote.NewSSHTransport()
	backupDir := filepath.Join(filepath.Dir(dbPath), "backups")
	c := core.NewWithRemote(store, core.Config{
		GatewayVersion:     buildinfo.GatewayVersion,
		CoreAPIVersion:     buildinfo.CoreAPIVersion,
		ToolCatalogVersion: buildinfo.ToolCatalogVersion,
		MCPProtocol:        buildinfo.MCPProtocol,
		DBPath:             dbPath,
		BackupDir:          backupDir,
	}, transport)
	return store, c, nil
}

func cmdVersion(args []string) int {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	jsonFlag := fs.Bool("json", false, "Print version contract as JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	info := buildinfo.Current()
	if *jsonFlag {
		payload := map[string]any{
			"gateway_version":         info.GatewayVersion,
			"core_api_version":        info.CoreAPIVersion,
			"bridge_api_version":      info.BridgeAPIVersion,
			"tool_catalog_version":    info.ToolCatalogVersion,
			"registry_schema_version": registry.SchemaVersion,
			"mcp_protocol":            info.MCPProtocol,
			"go_version":              info.GoVersion,
			"commit":                  info.Commit,
			"build_date":              info.BuildDate,
			"modified":                info.Modified,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(payload); err != nil {
			fmt.Fprintf(os.Stderr, "[ERROR] Could not encode version contract: %v\n", err)
			return 1
		}
		return 0
	}
	fmt.Printf(
		"mcp-gateway v%s (Core API v%d, MCP Protocol %s, Registry Schema v%d)\n",
		info.GatewayVersion,
		info.CoreAPIVersion,
		info.MCPProtocol,
		registry.SchemaVersion,
	)
	return 0
}

func cmdStatus(args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	dbFlag := fs.String("db", "", "Path to SQLite database")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	dbPath := resolveDBPath(*dbFlag)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	info, err := sqliteutil.Inspect(ctx, dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Could not inspect Registry: %v\n", err)
		return 1
	}
	if info.Integrity != "ok" {
		fmt.Fprintf(os.Stderr, "[ERROR] Registry integrity check failed: %s\n", info.Integrity)
		return 1
	}

	fmt.Println("==================================================")
	fmt.Printf("MCP Gateway: %s (Architecture: %s)\n", buildinfo.GatewayVersion, runtime.GOARCH)
	fmt.Printf("Registry:    integrity=%s schema=%d\n", info.Integrity, info.SchemaVersion)
	if info.SchemaVersion != registry.SchemaVersion {
		fmt.Printf("Runtime:     REQUIRES SCHEMA %d\n", registry.SchemaVersion)
		fmt.Println("==================================================")
		fmt.Fprintf(os.Stderr, "[ERROR] Registry schema %d is not current; run explicit migration during a coordinated lifecycle operation.\n", info.SchemaVersion)
		return 1
	}

	store, err := registry.OpenReadOnlyStore(ctx, dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Could not open current Registry read-only: %v\n", err)
		return 1
	}
	defer store.Close()

	writes, err := store.GetSetting(ctx, "writes_enabled", "false")
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Could not read writes setting: %v\n", err)
		return 1
	}
	shell, err := store.GetSetting(ctx, "shell_enabled", "false")
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Could not read shell setting: %v\n", err)
		return 1
	}
	targets, err := store.ListTargets(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Could not enumerate Targets: %v\n", err)
		return 1
	}

	fmt.Println("Runtime:     Registry contract compatible")
	fmt.Printf("Writes:      %s\n", strings.ToUpper(writes))
	fmt.Printf("Shell:       %s\n", strings.ToUpper(shell))
	fmt.Printf("Targets:     %d configured\n", len(targets))
	fmt.Println("==================================================")
	return 0
}

func cmdMigrate(args []string) int {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	dbFlag := fs.String("db", "", "Path to SQLite database")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	dbPath := resolveDBPath(*dbFlag)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if active := activeGatewayServices(); len(active) > 0 {
		fmt.Fprintf(os.Stderr, "[ERROR] Refusing migration while database users are active: %s\n", strings.Join(active, ", "))
		fmt.Fprintln(os.Stderr, "Stop Admin/MCP/maintenance database users first, then retry migration.")
		return 1
	}

	before := 0
	if _, err := os.Stat(dbPath); err == nil {
		info, err := sqliteutil.Inspect(ctx, dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[ERROR] Could not inspect Registry before migration: %v\n", err)
			return 1
		}
		if info.Integrity != "ok" {
			fmt.Fprintf(os.Stderr, "[ERROR] Refusing migration because Registry integrity is %s\n", info.Integrity)
			return 1
		}
		before = info.SchemaVersion
	} else if !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "[ERROR] Could not inspect Registry path: %v\n", err)
		return 1
	}

	if err := registry.MigratePath(ctx, dbPath); err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Registry migration failed: %v\n", err)
		return 1
	}
	after, err := sqliteutil.Inspect(ctx, dbPath)
	if err != nil || after.Integrity != "ok" || after.SchemaVersion != registry.SchemaVersion {
		fmt.Fprintf(os.Stderr, "[ERROR] Post-migration verification failed: integrity=%s schema=%d err=%v\n", after.Integrity, after.SchemaVersion, err)
		return 1
	}
	fmt.Printf("[OK] Registry migration verified: schema %d -> %d, integrity=%s\n", before, after.SchemaVersion, after.Integrity)
	return 0
}

func cmdDoctor(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	verboseFlag := fs.Bool("verbose", false, "Include verbose diagnostic details")
	checkTargetsFlag := fs.Bool("check-targets", false, "Probe remote target connectivity")
	dbFlag := fs.String("db", "", "Path to SQLite database")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	dbPath := resolveDBPath(*dbFlag)
	_, c, err := initStoreAndCore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Could not initialize gateway core: %v\n", err)
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp := c.Invoke(ctx, core.Invocation{ClientID: "local"}, "gateway_doctor", map[string]any{
		"verbose":       *verboseFlag,
		"check_targets": *checkTargetsFlag,
	})

	fmt.Println("==================================================")
	fmt.Println("=== MCP GATEWAY DOCTOR: SYSTEM HEALTH CHECK    ===")
	fmt.Println("==================================================")

	b, _ := json.Marshal(resp["result"])
	var res map[string]any
	_ = json.Unmarshal(b, &res)
	if res == nil {
		fmt.Fprintf(os.Stderr, "[FAIL] Doctor invocation failed: %v\n", resp["error"])
		return 1
	}

	checks, _ := res["checks"].([]any)
	for _, raw := range checks {
		chk, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		passed, _ := chk["passed"].(bool)
		severity, _ := chk["severity"].(string)
		name, _ := chk["name"].(string)
		msg, _ := chk["message"].(string)

		tag := "\033[92m[PASS]\033[0m"
		if !passed {
			if severity == "warning" {
				tag = "\033[93m[WARN]\033[0m"
			} else {
				tag = "\033[91m[FAIL]\033[0m"
			}
		}
		fmt.Printf("%s %-28s : %s\n", tag, name, msg)
	}

	overall, _ := res["status"].(string)
	if overall == "" {
		overall, _ = res["overall"].(string)
	}
	if overall == "" {
		overall = "ERROR"
	}
	color := "\033[92m"
	if overall == "WARNING" || overall == "DEGRADED" {
		color = "\033[93m"
	} else if overall != "HEALTHY" {
		color = "\033[91m"
	}
	fmt.Println("==================================================")
	fmt.Printf("OVERALL STATUS: %s%s\033[0m\n", color, overall)
	fmt.Println("==================================================")

	if overall == "HEALTHY" || overall == "WARNING" || overall == "DEGRADED" {
		return 0
	}
	return 1
}

func cmdMaintenance(args []string) int {
	fs := flag.NewFlagSet("maintenance", flag.ContinueOnError)
	dbFlag := fs.String("db", "", "Path to SQLite database")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	fmt.Println("==================================================")
	fmt.Println("=== MCP GATEWAY APPLIANCE SAFE MAINTENANCE     ===")
	fmt.Println("==================================================")

	dbPath := resolveDBPath(*dbFlag)
	_, c, err := initStoreAndCore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Could not initialize gateway core: %v\n", err)
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resp := c.Invoke(ctx, core.Invocation{ClientID: "local"}, "gateway_maintenance", map[string]any{})
	if ok, _ := resp["ok"].(bool); !ok {
		fmt.Fprintf(os.Stderr, "[ERROR] Maintenance failed: %v\n", resp["error"])
		return 1
	}

	r, _ := resp["result"].(map[string]any)
	fmt.Printf("[OK] Database Backup      : %v (%v bytes)\n", r["backup_created"], r["backup_size_bytes"])
	fmt.Printf("[OK] Pruned Old Backups   : %v managed backup(s) pruned\n", r["pruned_backups_count"])
	fmt.Printf("[OK] SQLite Integrity     : %v\n", r["database_integrity"])
	fmt.Printf("[OK] Doctor Overall       : %v\n", r["doctor_status"])
	fmt.Printf("[INFO] Security Updates   : %v\n", r["security_updates"])
	if resInfo, ok := r["resources"].(map[string]any); ok {
		fmt.Printf("[OK] Rootfs Free Space    : %v GB\n", resInfo["disk_free_gb"])
		fmt.Printf("[OK] Available Memory     : %v MB\n", resInfo["memory_available_mb"])
	}
	fmt.Println("==================================================")
	fmt.Println("STATUS: MAINTENANCE COMPLETED SUCCESSFULLY")
	fmt.Println("==================================================")
	return 0
}

func cmdBackup(args []string) int {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	dbFlag := fs.String("db", "", "Path to SQLite database")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	dbPath := resolveDBPath(*dbFlag)
	destPath := ""
	if fs.NArg() > 0 {
		destPath = fs.Arg(0)
	} else {
		destPath = filepath.Join(
			filepath.Dir(dbPath),
			"backups",
			fmt.Sprintf("gateway_backup_%s.db", time.Now().UTC().Format("20060102_150405.000000000")),
		)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	db, err := sqliteutil.OpenRaw(ctx, dbPath, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Could not open source database without migration: %v\n", err)
		return 1
	}
	defer db.Close()

	info, err := sqliteutil.Backup(ctx, db, destPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Backup failed: %v\n", err)
		return 1
	}
	fmt.Printf("[OK] Database backed up successfully to: %s\n", destPath)
	fmt.Printf("[OK] SQLite integrity: %s; schema: %d; sha256: %s\n", info.Integrity, info.SchemaVersion, info.SHA256)
	return 0
}

func activeGatewayServices() []string {
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return nil
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return nil
	}
	var active []string
	for _, svc := range core.RegistryQuiescenceServiceUnits() {
		if err := exec.Command("systemctl", "is-active", "--quiet", svc).Run(); err == nil {
			active = append(active, svc)
		}
	}
	return active
}

func cmdRestore(args []string) int {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	dbFlag := fs.String("db", "", "Path to SQLite database")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "Usage: mcp-gateway restore [-db path] <backup_file>")
		return 2
	}

	backupFile := fs.Arg(0)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	sourceInfo, err := sqliteutil.Inspect(ctx, backupFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Backup inspection failed: %v\n", err)
		return 1
	}
	if sourceInfo.Integrity != "ok" {
		fmt.Fprintf(os.Stderr, "[ERROR] Backup integrity is not ok: %s\n", sourceInfo.Integrity)
		return 1
	}
	if sourceInfo.SchemaVersion != 4 && sourceInfo.SchemaVersion != 5 && sourceInfo.SchemaVersion != registry.SchemaVersion {
		fmt.Fprintf(os.Stderr, "[ERROR] Unsupported backup schema %d; Go-only restore supports schemas 4, 5 or %d\n", sourceInfo.SchemaVersion, registry.SchemaVersion)
		return 1
	}
	if active := activeGatewayServices(); len(active) > 0 {
		fmt.Fprintf(os.Stderr, "[ERROR] Refusing restore while database users are active: %s\n", strings.Join(active, ", "))
		fmt.Fprintln(os.Stderr, "Stop the listed Registry users/schedulers first, then retry restore.")
		return 1
	}

	dbPath := resolveDBPath(*dbFlag)
	if fi, err := os.Stat(dbPath); err == nil && fi.Size() > 0 {
		preRestore := filepath.Join(
			filepath.Dir(dbPath),
			"backups",
			fmt.Sprintf("pre_restore_%s.db", time.Now().UTC().Format("20060102_150405.000000000")),
		)
		current, err := sqliteutil.OpenRaw(ctx, dbPath, false)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[ERROR] Could not open current database for pre-restore backup: %v\n", err)
			return 1
		}
		_, backupErr := sqliteutil.Backup(ctx, current, preRestore)
		closeErr := current.Close()
		if backupErr != nil {
			fmt.Fprintf(os.Stderr, "[ERROR] Pre-restore backup failed: %v\n", backupErr)
			return 1
		}
		if closeErr != nil {
			fmt.Fprintf(os.Stderr, "[ERROR] Closing current database after backup failed: %v\n", closeErr)
			return 1
		}
		fmt.Printf("[OK] Pre-restore rollback backup: %s\n", preRestore)
	} else if err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "[ERROR] Could not inspect target database: %v\n", err)
		return 1
	}

	target, err := sqliteutil.OpenRaw(ctx, dbPath, true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Could not open restore destination: %v\n", err)
		return 1
	}
	if err := sqliteutil.Restore(ctx, target, backupFile); err != nil {
		_ = target.Close()
		fmt.Fprintf(os.Stderr, "[ERROR] SQLite restore failed: %v\n", err)
		return 1
	}
	if err := target.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Closing restored database failed: %v\n", err)
		return 1
	}

	restoredInfo, err := sqliteutil.Inspect(ctx, dbPath)
	if err != nil || restoredInfo.Integrity != "ok" || restoredInfo.SchemaVersion != sourceInfo.SchemaVersion {
		fmt.Fprintf(os.Stderr, "[ERROR] Post-restore verification failed: integrity=%s schema=%d expected_schema=%d err=%v\n", restoredInfo.Integrity, restoredInfo.SchemaVersion, sourceInfo.SchemaVersion, err)
		return 1
	}
	fmt.Printf("[OK] Restore verified without migration: schema=%d sha256=%s\n", restoredInfo.SchemaVersion, restoredInfo.SHA256)
	if restoredInfo.SchemaVersion != registry.SchemaVersion {
		fmt.Printf("[INFO] Restored schema %d is preserved exactly; run explicit migration only when activating a runtime that requires schema %d.\n", restoredInfo.SchemaVersion, registry.SchemaVersion)
	}
	return 0
}

func cmdSetup(args []string) int {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	adminUser := fs.String("admin-user", "admin", "Admin username to configure")
	passwordStdin := fs.Bool("password-stdin", false, "Read password from stdin")
	dbFlag := fs.String("db", "", "Path to SQLite database")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	fmt.Println("==================================================")
	fmt.Println("=== MCP Gateway Initial Setup                  ===")
	fmt.Println("==================================================")
	fmt.Printf("Contract: Gateway v%s, MCP Protocol %s (Go-Only)\n", buildinfo.GatewayVersion, buildinfo.MCPProtocol)

	dbPath := resolveDBPath(*dbFlag)
	ctx := context.Background()

	store, _, err := initStoreAndCore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Setup requires a current Registry; run 'mcp-gateway migrate -db %s' first: %v\n", dbPath, err)
		return 1
	}

	existing, err := store.GetAdminUser(ctx, *adminUser)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Failed to inspect Admin user: %v\n", err)
		return 1
	}

	{
		shouldSet := *passwordStdin || existing == nil
		if shouldSet {
			var password string
			if *passwordStdin {
				reader := bufio.NewReader(os.Stdin)
				line, err := reader.ReadString('\n')
				if err != nil && len(line) == 0 {
					fmt.Fprintf(os.Stderr, "[ERROR] Failed to read password from stdin: %v\n", err)
					return 1
				}
				password = strings.TrimSpace(line)
			} else if term.IsTerminal(int(os.Stdin.Fd())) {
				fmt.Printf("Enter new password for '%s': ", *adminUser)
				raw, err := term.ReadPassword(int(os.Stdin.Fd()))
				fmt.Println()
				if err != nil {
					fmt.Fprintf(os.Stderr, "[ERROR] Failed to read password: %v\n", err)
					return 1
				}
				password = strings.TrimSpace(string(raw))
			} else {
				fmt.Fprintln(os.Stderr, "[ERROR] Interactive password setup requires a terminal; use --password-stdin for automation.")
				return 2
			}
			if password == "" {
				fmt.Fprintln(os.Stderr, "[ERROR] Password cannot be empty.")
				return 2
			}
			hash, err := admin.GeneratePasswordHash(password)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[ERROR] Failed to hash password: %v\n", err)
				return 1
			}
			err = store.SetAdminPassword(ctx, *adminUser, hash)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[ERROR] Failed to store admin user: %v\n", err)
				return 1
			}
			fmt.Printf("[OK] Admin user '%s' configured successfully.\n", *adminUser)
		} else {
			fmt.Printf("[OK] Admin user '%s' is already configured; password left unchanged.\n", *adminUser)
		}
	}

	admins, err := store.ListAdminUsers(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Failed to verify Admin users: %v\n", err)
		return 1
	}
	enabledAdmins := 0
	for _, user := range admins {
		if user.Enabled {
			enabledAdmins++
		}
	}
	if enabledAdmins == 0 {
		fmt.Fprintln(os.Stderr, "[ERROR] Setup is incomplete: no enabled Admin user exists.")
		return 1
	}
	if err := os.Remove(admin.BootstrapTokenPath(os.Getenv("MCP_ADMIN_SECRET_FILE"))); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "[ERROR] Admin configured but bootstrap token could not be removed: %v\n", err)
		return 1
	}

	fmt.Println("\nRunning Doctor...")
	rc := cmdDoctor([]string{"-db", dbPath})
	if rc != 0 {
		return rc
	}

	host, _ := os.Hostname()
	if host == "" {
		host = "localhost"
	}
	fmt.Println("\nSetup baseline complete.")
	fmt.Printf("Admin Console: http://%s/\n", host)
	fmt.Println("Next: sign in to Admin Console and add the first Target, Project, client and grant.")
	fmt.Println("Security defaults keep structured writes and trusted Target shell disabled until explicitly enabled.")
	return 0
}

func cmdBenchmark(args []string) int {
	fs := flag.NewFlagSet("benchmark", flag.ContinueOnError)
	iterations := fs.Int("n", 100, "Number of benchmark iterations")
	dbFlag := fs.String("db", "", "Path to SQLite database")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	dbPath := resolveDBPath(*dbFlag)
	_, c, err := initStoreAndCore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Could not initialize gateway core: %v\n", err)
		return 1
	}

	fmt.Println("==================================================")
	fmt.Printf("=== MCP-PI GO-ONLY RUNTIME LATENCY BENCHMARK   ===\n")
	fmt.Printf("=== Iterations: %-30d ===\n", *iterations)
	fmt.Println("==================================================")

	ctx := context.Background()

	// Warmup
	for i := 0; i < 5; i++ {
		_ = c.Health(ctx, "warmup")
	}

	durations := make([]time.Duration, *iterations)
	for i := 0; i < *iterations; i++ {
		start := time.Now()
		resp := c.Health(ctx, fmt.Sprintf("bench-%d", i))
		dur := time.Since(start)
		if !resp.OK {
			fmt.Fprintf(os.Stderr, "Health failed during bench: %v\n", resp.Error)
			return 1
		}
		durations[i] = dur
	}

	sort.Slice(durations, func(i, j int) bool {
		return durations[i] < durations[j]
	})

	min := durations[0]
	p50 := durations[*iterations*50/100]
	p95 := durations[*iterations*95/100]
	p99 := durations[*iterations*99/100]
	max := durations[*iterations-1]

	fmt.Printf("In-process Core /health benchmark:\n")
	fmt.Printf("  Min:  %10.3f ms\n", float64(min.Microseconds())/1000.0)
	fmt.Printf("  p50:  %10.3f ms\n", float64(p50.Microseconds())/1000.0)
	fmt.Printf("  p95:  %10.3f ms\n", float64(p95.Microseconds())/1000.0)
	fmt.Printf("  p99:  %10.3f ms\n", float64(p99.Microseconds())/1000.0)
	fmt.Printf("  Max:  %10.3f ms\n", float64(max.Microseconds())/1000.0)
	fmt.Println("==================================================")
	return 0
}

func cmdServeAdmin(args []string) int {
	fs := flag.NewFlagSet("serve-admin", flag.ContinueOnError)
	hostFlag := fs.String("host", "", "Host address to bind")
	portFlag := fs.Int("port", 0, "Port to bind")
	allowedHostsFlag := fs.String("allowed-hosts", "", "Comma-separated allowed Host headers")
	secretDirFlag := fs.String("secret-dir", "", "Path to admin secret directory")
	dbFlag := fs.String("db", "", "Path to SQLite database")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	host := *hostFlag
	if host == "" {
		host = os.Getenv("MCP_ADMIN_HOST")
	}
	if host == "" {
		host = "127.0.0.1"
	}

	port := *portFlag
	if port <= 0 {
		if pEnv := os.Getenv("MCP_ADMIN_PORT"); pEnv != "" {
			port, _ = strconv.Atoi(pEnv)
		}
	}
	if port <= 0 {
		port = 80
	}

	allowedHostsStr := *allowedHostsFlag
	if allowedHostsStr == "" {
		allowedHostsStr = os.Getenv("MCP_ADMIN_ALLOWED_HOSTS")
	}
	var allowedHosts []string
	if allowedHostsStr != "" {
		for _, h := range strings.Split(allowedHostsStr, ",") {
			if clean := strings.TrimSpace(h); clean != "" {
				allowedHosts = append(allowedHosts, clean)
			}
		}
	} else {
		allowedHosts = []string{"127.0.0.1", "localhost", host}
	}

	secretFile := strings.TrimSpace(os.Getenv("MCP_ADMIN_SECRET_FILE"))
	if secretFile == "" {
		secretDir := strings.TrimSpace(*secretDirFlag)
		if secretDir == "" {
			home := os.Getenv("MCP_GATEWAY_HOME")
			if home == "" {
				home = "/home/mcp-gateway"
			}
			secretDir = filepath.Join(home, ".config", "mcp-gateway")
		}
		secretFile = filepath.Join(secretDir, "admin-secret")
	}

	dbPath := resolveDBPath(*dbFlag)
	store, c, err := initStoreAndCore(dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize gateway core: %v", err)
	}

	targetDisc := discovery.NewTargetDiscovery("")

	adminServer, err := admin.NewServer(admin.ServerConfig{
		Host:         host,
		Port:         port,
		AllowedHosts: allowedHosts,
		SecretFile:   secretFile,
		Store:        store,
		Core:         c,
		Discovery:    targetDisc,
		Version:      buildinfo.GatewayVersion,
	})
	if err != nil {
		log.Fatalf("Failed to create admin server: %v", err)
	}

	addr := fmt.Sprintf("%s:%d", host, port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("Failed to bind admin console to %s: %v", addr, err)
	}
	defer listener.Close()

	log.Printf("MCP Gateway Admin Console listening on http://%s (allowed hosts: %v)", addr, allowedHosts)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	httpServer := &http.Server{
		Handler:      adminServer.Handler(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	errChan := make(chan error, 1)
	go func() {
		errChan <- httpServer.Serve(listener)
	}()

	select {
	case sig := <-sigChan:
		log.Printf("Received signal %v, shutting down Admin Console gracefully...", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(ctx)
		return 0
	case err := <-errChan:
		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("Admin server error: %v", err)
			return 1
		}
		return 0
	}
}

func printHelp() {
	fmt.Println(`MCP Gateway Unified Management CLI (Go-Only)

Usage:
  mcp-gateway <command> [options]

Commands:
  status         Inspect Registry/runtime compatibility without changing schema
  migrate        Explicitly create/migrate the Registry to the current schema
  doctor         Run comprehensive system diagnostics
  maintenance    Run safe automated maintenance (backup, rotation, integrity)
  backup [path]  Create an online database backup
  restore <file> Restore a database backup exactly, without schema migration
  setup          Configure initial Admin user password and verify system health
  benchmark      Run in-process Core performance and latency benchmarks
  serve-mcp      Start the MCP Protocol adapter (Streamable HTTP or stdio)
  serve-admin    Start the Go Admin Web Console
  version        Print version and protocol information
  help           Display this help screen`)
}
