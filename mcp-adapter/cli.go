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
	if err := os.MkdirAll(filepath.Dir(dbPath), 0700); err != nil {
		return nil, nil, fmt.Errorf("failed to create db directory: %w", err)
	}
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
	_, c, err := initStoreAndCore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Could not initialize gateway core: %v\n", err)
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp := c.Health(ctx, "cli-status")
	if !resp.OK {
		fmt.Fprintf(os.Stderr, "[ERROR] Health check failed: %v\n", resp.Error)
		return 1
	}

	res, _ := resp.Result.(map[string]any)
	fmt.Println("==================================================")
	fmt.Printf("MCP Gateway: %v (Architecture: %v)\n", res["gateway_version"], res["architecture"])
	fmt.Printf("Status:      %s\n", func() string {
		if resp.OK {
			return "ONLINE"
		}
		return "OFFLINE"
	}())
	fmt.Printf("Writes:      %s\n", func() string {
		if w, _ := res["writes_enabled"].(bool); w {
			return "ENABLED"
		}
		return "DISABLED"
	}())
	fmt.Printf("Shell:       %s\n", func() string {
		if s, _ := res["shell_enabled"].(bool); s {
			return "ENABLED"
		}
		return "DISABLED"
	}())
	fmt.Printf("Targets:     %v configured\n", res["configured_targets"])
	fmt.Println("==================================================")
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
		overall = "HEALTHY"
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
	fmt.Printf("[OK] Pruned Old Backups   : %v backup(s) pruned (kept latest 5)\n", r["pruned_backups_count"])
	fmt.Printf("[OK] SQLite Integrity     : %v\n", r["database_integrity"])
	fmt.Printf("[OK] Doctor Overall       : %v\n", r["doctor_status"])
	fmt.Printf("[OK] Security Updates     : %v\n", r["security_updates"])
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
	for _, svc := range []string{"mcp-gateway-admin.service", "mcp-gateway-mcp.service"} {
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
	if sourceInfo.SchemaVersion != 4 && sourceInfo.SchemaVersion != registry.SchemaVersion {
		fmt.Fprintf(os.Stderr, "[ERROR] Unsupported backup schema %d; Go-only restore supports schema 4 or %d\n", sourceInfo.SchemaVersion, registry.SchemaVersion)
		return 1
	}
	if active := activeGatewayServices(); len(active) > 0 {
		fmt.Fprintf(os.Stderr, "[ERROR] Refusing restore while database users are active: %s\n", strings.Join(active, ", "))
		fmt.Fprintln(os.Stderr, "Stop Admin/MCP services first, then retry restore.")
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

	store, err := registry.OpenStore(ctx, dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Restored database migration/validation failed: %v\n", err)
		return 1
	}
	if err := store.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Closing validated Registry failed: %v\n", err)
		return 1
	}

	restoredInfo, err := sqliteutil.Inspect(ctx, dbPath)
	if err != nil || restoredInfo.Integrity != "ok" || restoredInfo.SchemaVersion != registry.SchemaVersion {
		fmt.Fprintf(os.Stderr, "[ERROR] Post-restore verification failed: integrity=%s schema=%d err=%v\n", restoredInfo.Integrity, restoredInfo.SchemaVersion, err)
		return 1
	}
	fmt.Printf("[OK] Restore verified: schema=%d sha256=%s\n", restoredInfo.SchemaVersion, restoredInfo.SHA256)
	return cmdDoctor([]string{"-db", dbPath})
}

func cmdRepair(args []string) int {
	fmt.Println("Executing safe repair actions...")
	fs := flag.NewFlagSet("repair", flag.ContinueOnError)
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

	resp := c.Invoke(ctx, core.Invocation{ClientID: "local"}, "gateway_maintenance", map[string]any{})
	if ok, _ := resp["ok"].(bool); ok {
		fmt.Println("- Database VACUUM and schema repair completed.")
	} else {
		fmt.Fprintf(os.Stderr, "- Database repair action reported: %v\n", resp["error"])
	}

	fmt.Println("\nRe-evaluating system health:")
	return cmdDoctor([]string{"-db", dbPath})
}

func cmdSetup(args []string) int {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	adminUser := fs.String("admin-user", "admin", "Admin username to configure")
	skipPassword := fs.Bool("skip-password", false, "Skip Admin password bootstrap")
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
	store, _, err := initStoreAndCore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Failed to open registry: %v\n", err)
		return 1
	}

	ctx := context.Background()
	existing, _ := store.GetAdminUser(ctx, *adminUser)

	if !*skipPassword {
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
			_, err = store.DB().ExecContext(ctx, `
				INSERT INTO admin_users(username, password_hash, enabled)
				VALUES (?, ?, 1)
				ON CONFLICT(username) DO UPDATE SET password_hash = excluded.password_hash, enabled = 1
			`, *adminUser, hash)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[ERROR] Failed to store admin user: %v\n", err)
				return 1
			}
			fmt.Printf("[OK] Admin user '%s' configured successfully.\n", *adminUser)
		} else {
			fmt.Printf("[OK] Admin user '%s' is already configured; password left unchanged.\n", *adminUser)
		}
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
  status         Show gateway status summary
  doctor         Run comprehensive system diagnostics
  maintenance    Run safe automated maintenance (backup, rotation, integrity)
  backup [path]  Create an online database backup
  restore <file> Restore database from a backup file with integrity verification
  repair         Perform database vacuum and integrity repair
  setup          Configure initial Admin user password and verify system health
  benchmark      Run in-process Core performance and latency benchmarks
  serve-mcp      Start the MCP Protocol adapter (Streamable HTTP or stdio)
  serve-admin    Start the Go Admin Web Console
  version        Print version and protocol information
  help           Display this help screen`)
}
