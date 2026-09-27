package core

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"mcp-gateway-adapter/internal/registry"
	"mcp-gateway-adapter/internal/remote"
	"mcp-gateway-adapter/internal/sqliteutil"
)

func (c *Core) dbPath() string {
	if c.config.DBPath != "" {
		return c.config.DBPath
	}
	if p := os.Getenv("MCP_GATEWAY_DB"); p != "" {
		return p
	}
	baseInstall := os.Getenv("MCP_GATEWAY_HOME")
	if baseInstall == "" {
		baseInstall = "/home/mcp-gateway"
	}
	return filepath.Join(baseInstall, ".local", "share", "mcp-gateway", "gateway.db")
}

func (c *Core) backupDir() string {
	if c.config.BackupDir != "" {
		return c.config.BackupDir
	}
	return filepath.Join(filepath.Dir(c.dbPath()), "backups")
}

func (c *Core) GatewayStatus(ctx context.Context, requestID string) Response {
	started := time.Now()
	if blocked := c.operationalGate(ctx, "gateway_status", requestID, "", ""); blocked != nil {
		return responseFromMap(blocked)
	}

	uptimeSec := getUptimeSeconds()
	load1, load5, load15 := getCPULoad()
	mem := getMemoryInfo()
	zramUsedMB := getZRAMUsedMB()
	storage := getDiskInfo("/")
	tempC := getTemperature()
	throttled := getThrottled()
	services := getServicesStatus([]string{"mcp-gateway-admin", "mcp-gateway-mcp", "mcp-gateway-tunnel"})
	writesEnabled, _ := c.boolSetting(ctx, "writes_enabled", false)
	targetCount, _ := c.store.TargetCount(ctx)

	dbPath := c.dbPath()
	var dbSize int64
	if fi, err := os.Stat(dbPath); err == nil {
		dbSize = fi.Size()
	}

	res := map[string]any{
		"gateway_status":  "ok",
		"gateway_version": c.config.GatewayVersion,
		"architecture":    runtime.GOARCH,
		"uptime_seconds":  uptimeSec,
		"cpu_load": map[string]any{
			"1m":  round2(load1),
			"5m":  round2(load5),
			"15m": round2(load15),
		},
		"memory":        mem,
		"zram_used_mb":  zramUsedMB,
		"storage":       storage,
		"temperature_c": tempC,
		"throttled":     throttled,
		"services":      services,
		"deployment":    loadDeploymentProvenance(),
		"database": map[string]any{
			"path":           dbPath,
			"size_bytes":     dbSize,
			"writes_enabled": writesEnabled,
			"targets_count":  targetCount,
		},
	}

	durMS := time.Since(started).Milliseconds()
	_ = c.store.RecordActivity(ctx, registry.Activity{
		Actor:      "system",
		Action:     "STATUS_CHECK",
		DurationMS: &durMS,
		Success:    true,
		Detail:     mustJSON(map[string]any{"tool": "gateway_status"}),
	})

	return successResponse("gateway_status", res, requestID, "", "", started)
}

func (c *Core) GatewayBackup(ctx context.Context, requestID, actor, destPath string) Response {
	started := time.Now()
	if blocked := c.operationalGate(ctx, "gateway_backup", requestID, "", ""); blocked != nil {
		return responseFromMap(blocked)
	}

	if err := c.recordAuditRequired(ctx, actor, "REGISTRY_BACKUP_ATTEMPT", "", "", map[string]any{}, started); err != nil {
		return errorResponse("gateway_backup", "AUDIT_UNAVAILABLE", "Audit sink is unavailable for a critical operation", requestID, "", "", started)
	}

	if strings.TrimSpace(destPath) == "" {
		backupDir := c.backupDir()
		destPath = filepath.Join(backupDir, fmt.Sprintf("gateway_backup_%s.db", time.Now().UTC().Format("20060102_150405.000000000")))
	}
	info, err := sqliteutil.Backup(ctx, c.store.DB(), destPath)
	if err != nil {
		code := "INTERNAL_ERROR"
		if strings.Contains(err.Error(), "already exists") {
			code = "ALREADY_EXISTS"
		}
		return errorResponse("gateway_backup", code, "Failed to backup database: "+err.Error(), requestID, "", "", started)
	}
	if info.Integrity != "ok" {
		_ = os.Remove(destPath)
		return errorResponse("gateway_backup", "BACKUP_INTEGRITY_FAILED", "Backup integrity check failed: "+info.Integrity, requestID, "", "", started)
	}

	size := info.SizeBytes
	durMS := time.Since(started).Milliseconds()
	_ = c.store.RecordActivity(ctx, registry.Activity{
		Actor:            nonEmpty(actor, "mcp-local"),
		Action:           "REGISTRY_BACKUP",
		DurationMS:       &durMS,
		Success:          true,
		BytesTransferred: &size,
		Detail:           mustJSON(map[string]any{"backup_path": destPath, "sha256": info.SHA256, "schema_version": info.SchemaVersion}),
	})

	res := map[string]any{
		"backup_path":    destPath,
		"sha256":         info.SHA256,
		"size_bytes":     info.SizeBytes,
		"schema_version": info.SchemaVersion,
		"integrity":      info.Integrity,
		"created_at":     time.Now().UTC().Format(time.RFC3339),
	}
	return successResponse("gateway_backup", res, requestID, "", "", started)
}

type DoctorOptions struct {
	CheckTargets bool `json:"check_targets"`
	Verbose      bool `json:"verbose"`
}

type DoctorCheck struct {
	Name     string `json:"name"`
	Passed   bool   `json:"passed"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
	Required bool   `json:"required"`
}

func (c *Core) GatewayDoctor(ctx context.Context, requestID string, opts DoctorOptions) Response {
	started := time.Now()
	if blocked := c.operationalGate(ctx, "gateway_doctor", requestID, "", ""); blocked != nil {
		return responseFromMap(blocked)
	}

	checks := []DoctorCheck{{
		Name:     "Gateway Core Health",
		Passed:   true,
		Message:  "Core is initialized and responding",
		Severity: "error",
		Required: true,
	}}

	dbHealthy := c.store != nil && c.store.DB() != nil
	checks = append(checks, DoctorCheck{
		Name: "Database Connection", Passed: dbHealthy,
		Message: "SQLite registry store connected", Severity: "error", Required: true,
	})

	integrity := ""
	if dbHealthy {
		if err := c.store.DB().QueryRowContext(ctx, "PRAGMA integrity_check;").Scan(&integrity); err != nil {
			integrity = "query failed: " + err.Error()
		}
	}
	integrityPassed := integrity == "ok"
	integMsg := "PRAGMA integrity_check passed: ok"
	if !integrityPassed {
		integMsg = "Database integrity check failed: " + integrity
	}
	checks = append(checks, DoctorCheck{
		Name: "Database Integrity", Passed: integrityPassed,
		Message: integMsg, Severity: "error", Required: true,
	})

	disk := getDiskInfo("/")
	diskPassed := true
	diskMsg := fmt.Sprintf("Root storage available: %v GB free (%.1f%% used)", disk["root_free_gb"], disk["root_used_pct"])
	if free, ok := disk["root_free_gb"].(float64); ok && free < 0.5 {
		diskPassed = false
		diskMsg = fmt.Sprintf("Low root disk space: %v GB free", free)
	}
	checks = append(checks, DoctorCheck{
		Name: "Storage Space", Passed: diskPassed, Message: diskMsg, Severity: "warning", Required: true,
	})

	mem := getMemoryInfo()
	memPassed := true
	memMsg := fmt.Sprintf("Memory available: %v MB", mem["available_mb"])
	if avail, ok := mem["available_mb"].(int); ok && avail < 50 && avail > 0 {
		memPassed = false
		memMsg = fmt.Sprintf("Low available memory: %v MB", avail)
	}
	checks = append(checks, DoctorCheck{
		Name: "Memory Resources", Passed: memPassed, Message: memMsg, Severity: "warning", Required: true,
	})

	targetStatus := "SKIP"
	targetMsg := "Target probes not requested"
	if opts.CheckTargets {
		targets, err := c.store.ListTargets(ctx)
		if err != nil {
			checks = append(checks, DoctorCheck{
				Name: "Target Registry", Passed: false, Message: err.Error(), Severity: "error", Required: true,
			})
			targetStatus, targetMsg = "FAIL", "Unable to enumerate targets"
		} else {
			probed := 0
			failed := 0
			for _, summary := range targets {
				if !summary.Enabled {
					continue
				}
				probed++
				target, err := c.store.GetTarget(ctx, summary.ID, false)
				if err != nil {
					failed++
					checks = append(checks, DoctorCheck{
						Name: "Target " + summary.ID, Passed: false, Message: err.Error(), Severity: "error", Required: true,
					})
					continue
				}
				if c.remote == nil {
					failed++
					checks = append(checks, DoctorCheck{
						Name: "Target " + summary.ID, Passed: false, Message: "remote transport unavailable", Severity: "error", Required: true,
					})
					continue
				}
				res, err := c.remote.RunCommand(ctx, target, "hostname", remote.CommandOptions{Timeout: 5 * time.Second})
				if err != nil || !res.OK() {
					failed++
					msg := "target probe failed"
					if err != nil {
						msg = err.Error()
					} else if strings.TrimSpace(res.Stderr) != "" {
						msg = strings.TrimSpace(res.Stderr)
					}
					checks = append(checks, DoctorCheck{
						Name: "Target " + summary.ID, Passed: false, Message: msg, Severity: "error", Required: true,
					})
					continue
				}
				checks = append(checks, DoctorCheck{
					Name: "Target " + summary.ID, Passed: true,
					Message: "reachable: " + strings.TrimSpace(res.Stdout), Severity: "error", Required: true,
				})
			}
			switch {
			case probed == 0:
				targetStatus, targetMsg = "WARN", "No enabled targets configured"
				checks = append(checks, DoctorCheck{
					Name: "Target Connectivity", Passed: false, Message: targetMsg, Severity: "warning", Required: true,
				})
			case failed > 0:
				targetStatus, targetMsg = "FAIL", fmt.Sprintf("%d of %d target probes failed", failed, probed)
			default:
				targetStatus, targetMsg = "PASS", fmt.Sprintf("%d target(s) reachable", probed)
			}
		}
	}

	services := getServicesStatus([]string{"mcp-gateway-tunnel"})
	tunnelStatus := "SKIP"
	tunnelMsg := "systemd tunnel probe unavailable on this platform"
	if t, ok := services["mcp-gateway-tunnel"]; ok && t != "unknown" {
		if t == "active" {
			tunnelStatus, tunnelMsg = "PASS", "active"
		} else {
			tunnelStatus, tunnelMsg = "WARN", "tunnel service status: "+t
		}
	}

	clients, err := c.store.ListClients(ctx)
	clientStatus := "SKIP"
	clientMsg := "No registered clients"
	if err != nil {
		clientStatus, clientMsg = "FAIL", err.Error()
	} else if len(clients) > 0 {
		clientStatus, clientMsg = "PASS", "Registered client entrypoint available"
	}

	controlPath := map[string]any{
		"client_entrypoint": map[string]string{"status": clientStatus, "message": clientMsg},
		"tunnel":            map[string]string{"status": tunnelStatus, "message": tunnelMsg},
		"mcp_adapter":       map[string]string{"status": "PASS", "message": "Go-only in-process MCP adapter active"},
		"gateway_core":      map[string]string{"status": "PASS", "message": "Derived from Gateway Core Doctor check"},
		"targets":           map[string]string{"status": targetStatus, "message": targetMsg},
	}

	passedCnt, failedCnt, warnCnt := 0, 0, 0
	for _, ch := range checks {
		if ch.Passed {
			passedCnt++
		} else if ch.Severity == "warning" {
			warnCnt++
		} else if ch.Required {
			failedCnt++
		}
	}

	overall := "HEALTHY"
	if failedCnt > 0 {
		overall = "ERROR"
	} else if warnCnt > 0 {
		overall = "WARNING"
	}

	res := map[string]any{
		"control_path": controlPath,
		"status":       overall,
		"checks_count": len(checks),
		"passed":       passedCnt,
		"failed":       failedCnt,
		"warnings":     warnCnt,
		"checks":       checks,
	}
	if opts.Verbose {
		res["database_path"] = c.dbPath()
		res["backup_dir"] = c.backupDir()
	}

	durMS := time.Since(started).Milliseconds()
	_ = c.store.RecordActivity(ctx, registry.Activity{
		Actor:      "system",
		Action:     "DOCTOR_CHECK",
		DurationMS: &durMS,
		Success:    failedCnt == 0,
		Detail:     mustJSON(map[string]any{"status": overall, "passed": passedCnt, "failed": failedCnt, "warnings": warnCnt}),
	})

	return successResponse("gateway_doctor", res, requestID, "", "", started)
}

func (c *Core) GatewayMaintenance(ctx context.Context, requestID, actor string) Response {
	started := time.Now()
	if blocked := c.operationalGate(ctx, "gateway_maintenance", requestID, "", ""); blocked != nil {
		return responseFromMap(blocked)
	}
	if err := c.recordAuditRequired(ctx, actor, "MAINTENANCE_ATTEMPT", "", "", map[string]any{}, started); err != nil {
		return errorResponse("gateway_maintenance", "AUDIT_UNAVAILABLE", "Audit sink is unavailable for a critical operation", requestID, "", "", started)
	}

	bakResp := c.GatewayBackup(ctx, requestID, actor, "")
	if !bakResp.OK {
		return errorResponse("gateway_maintenance", "BACKUP_FAILED", "Maintenance backup failed: "+bakResp.Error.Message, requestID, "", "", started)
	}
	bakResult, _ := bakResp.Result.(map[string]any)
	bakPath, _ := bakResult["backup_path"].(string)
	bakSize, _ := bakResult["size_bytes"].(int64)

	backupsDir := c.backupDir()
	prunedCount := 0
	if entries, err := os.ReadDir(backupsDir); err == nil {
		var backupFiles []string
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "gateway_backup_") && strings.HasSuffix(e.Name(), ".db") {
				backupFiles = append(backupFiles, filepath.Join(backupsDir, e.Name()))
			}
		}
		sort.Slice(backupFiles, func(i, j int) bool {
			fi, err1 := os.Stat(backupFiles[i])
			fj, err2 := os.Stat(backupFiles[j])
			if err1 != nil || err2 != nil {
				return backupFiles[i] < backupFiles[j]
			}
			return fi.ModTime().Before(fj.ModTime())
		})
		for _, old := range backupFiles[:max(0, len(backupFiles)-5)] {
			if err := os.Remove(old); err != nil {
				return errorResponse("gateway_maintenance", "BACKUP_PRUNE_FAILED", "Failed to prune old backup: "+err.Error(), requestID, "", "", started)
			}
			prunedCount++
		}
	}

	var integrity string
	if err := c.store.DB().QueryRowContext(ctx, "PRAGMA integrity_check;").Scan(&integrity); err != nil {
		return errorResponse("gateway_maintenance", "DATABASE_INTEGRITY_FAILED", "SQLite integrity check failed to execute: "+err.Error(), requestID, "", "", started)
	}
	if integrity != "ok" {
		return errorResponse("gateway_maintenance", "DATABASE_INTEGRITY_FAILED", "SQLite integrity check failed: "+integrity, requestID, "", "", started)
	}

	docResp := c.GatewayDoctor(ctx, requestID, DoctorOptions{})
	if !docResp.OK {
		return errorResponse("gateway_maintenance", "DOCTOR_FAILED", "Doctor execution failed", requestID, "", "", started)
	}
	docResult, ok := docResp.Result.(map[string]any)
	if !ok {
		return errorResponse("gateway_maintenance", "DOCTOR_FAILED", "Doctor returned an invalid result", requestID, "", "", started)
	}
	docStatus, _ := docResult["status"].(string)
	if docStatus == "" || docStatus == "ERROR" {
		return errorResponse("gateway_maintenance", "DOCTOR_FAILED", "Doctor reported "+nonEmpty(docStatus, "UNKNOWN"), requestID, "", "", started)
	}

	disk := getDiskInfo("/")
	mem := getMemoryInfo()
	secStatus := getSecurityUpdatesStatus()
	res := map[string]any{
		"message":              "Appliance maintenance executed successfully",
		"backup_created":       bakPath,
		"backup_size_bytes":    bakSize,
		"pruned_backups_count": prunedCount,
		"database_integrity":   integrity,
		"doctor_status":        docStatus,
		"security_updates":     secStatus,
		"resources": map[string]any{
			"disk_free_gb":        disk["root_free_gb"],
			"memory_available_mb": mem["available_mb"],
		},
		"completed_at": time.Now().UTC().Format(time.RFC3339),
	}

	durMS := time.Since(started).Milliseconds()
	_ = c.store.RecordActivity(ctx, registry.Activity{
		Actor:      nonEmpty(actor, "mcp-local"),
		Action:     "MAINTENANCE_RUN",
		DurationMS: &durMS,
		Success:    true,
		Detail:     mustJSON(map[string]any{"doctor_status": docStatus, "pruned": prunedCount}),
	})
	return successResponse("gateway_maintenance", res, requestID, "", "", started)
}

var scheduleReboot = scheduleRebootWithLogind

func scheduleRebootWithLogind(ctx context.Context, when time.Time) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("controlled appliance reboot is supported only on Linux")
	}
	if _, err := exec.LookPath("busctl"); err != nil {
		return fmt.Errorf("systemd busctl is unavailable: %w", err)
	}
	cmd := exec.CommandContext(
		ctx,
		"busctl",
		"call",
		"org.freedesktop.login1",
		"/org/freedesktop/login1",
		"org.freedesktop.login1.Manager",
		"ScheduleShutdown",
		"st",
		"reboot",
		strconv.FormatInt(when.UnixMicro(), 10),
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("logind rejected reboot scheduling: %s", message)
	}
	return nil
}

func (c *Core) GatewayReboot(ctx context.Context, requestID, actor string, confirm bool) Response {
	started := time.Now()
	if blocked := c.operationalGate(ctx, "gateway_reboot", requestID, "", ""); blocked != nil {
		return responseFromMap(blocked)
	}
	if !confirm {
		return errorResponse("gateway_reboot", "INVALID_ARGUMENTS", "Appliance reboot requires explicit confirmation parameter 'confirm=true'", requestID, "", "", started)
	}
	if err := c.recordAuditRequired(ctx, actor, "REBOOT_REQUESTED", "", "", map[string]any{"actor": actor, "tool": "gateway_reboot"}, started); err != nil {
		return errorResponse("gateway_reboot", "AUDIT_UNAVAILABLE", "Audit sink is unavailable for a critical operation", requestID, "", "", started)
	}

	when := time.Now().Add(2 * time.Second)
	if err := scheduleReboot(ctx, when); err != nil {
		return errorResponse("gateway_reboot", "REBOOT_SCHEDULE_FAILED", err.Error(), requestID, "", "", started)
	}

	return successResponse("gateway_reboot", map[string]any{
		"reboot_scheduled": true,
		"message":          "Controlled appliance reboot accepted by systemd-logind",
		"requested_by":     actor,
		"timestamp":        time.Now().UTC().Format(time.RFC3339),
	}, requestID, "", "", started)
}

func getUptimeSeconds() int {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(data))
	if len(fields) > 0 {
		if val, err := strconv.ParseFloat(fields[0], 64); err == nil {
			return int(val)
		}
	}
	return 0
}

func getCPULoad() (float64, float64, float64) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, 0, 0
	}
	fields := strings.Fields(string(data))
	if len(fields) >= 3 {
		l1, _ := strconv.ParseFloat(fields[0], 64)
		l5, _ := strconv.ParseFloat(fields[1], 64)
		l15, _ := strconv.ParseFloat(fields[2], 64)
		return l1, l5, l15
	}
	return 0, 0, 0
}

func getMemoryInfo() map[string]any {
	out := map[string]any{"total_mb": 0, "available_mb": 0, "used_mb": 0}
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return out
	}
	defer f.Close()

	var totalKB, availKB int
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		parts := strings.Split(scanner.Text(), ":")
		if len(parts) == 2 {
			k := strings.TrimSpace(parts[0])
			valStr := strings.Fields(parts[1])
			if len(valStr) > 0 {
				if v, err := strconv.Atoi(valStr[0]); err == nil {
					if k == "MemTotal" {
						totalKB = v
					} else if k == "MemAvailable" {
						availKB = v
					}
				}
			}
		}
	}
	out["total_mb"] = totalKB / 1024
	out["available_mb"] = availKB / 1024
	out["used_mb"] = (totalKB - availKB) / 1024
	return out
}

func getZRAMUsedMB() int {
	data, err := os.ReadFile("/sys/block/zram0/mem_used_total")
	if err != nil {
		return 0
	}
	val, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return val / (1024 * 1024)
}

func getDiskInfo(path string) map[string]any {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return map[string]any{
			"root_total_gb": 0.0,
			"root_used_gb":  0.0,
			"root_free_gb":  0.0,
			"root_used_pct": 0.0,
		}
	}
	bsize := uint64(stat.Bsize)
	totalBytes := stat.Blocks * bsize
	freeBytes := stat.Bavail * bsize
	usedBytes := totalBytes - freeBytes

	gb := 1024.0 * 1024.0 * 1024.0
	totalGB := round2(float64(totalBytes) / gb)
	freeGB := round2(float64(freeBytes) / gb)
	usedGB := round2(float64(usedBytes) / gb)
	pct := 0.0
	if totalBytes > 0 {
		pct = round1((float64(usedBytes) / float64(totalBytes)) * 100.0)
	}

	return map[string]any{
		"root_total_gb": totalGB,
		"root_used_gb":  usedGB,
		"root_free_gb":  freeGB,
		"root_used_pct": pct,
	}
}

func getTemperature() any {
	data, err := os.ReadFile("/sys/class/thermal/thermal_zone0/temp")
	if err != nil {
		return nil
	}
	val, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
	if err != nil {
		return nil
	}
	return round1(val / 1000.0)
}

func getThrottled() string {
	if _, err := exec.LookPath("vcgencmd"); err == nil {
		out, err := exec.Command("vcgencmd", "get_throttled").Output()
		if err == nil {
			parts := strings.Split(strings.TrimSpace(string(out)), "=")
			if len(parts) == 2 {
				return parts[1]
			}
		}
	}
	return "unknown"
}

func getServicesStatus(services []string) map[string]string {
	out := make(map[string]string)
	if _, err := exec.LookPath("systemctl"); err == nil {
		for _, svc := range services {
			cp, err := exec.Command("systemctl", "is-active", svc).Output()
			if err == nil {
				out[svc] = strings.TrimSpace(string(cp))
			} else {
				out[svc] = "inactive"
			}
		}
	} else {
		for _, svc := range services {
			out[svc] = "unknown"
		}
	}
	return out
}

func getSecurityUpdatesStatus() string {
	logPath := "/var/log/unattended-upgrades/unattended-upgrades.log"
	confPath := "/etc/apt/apt.conf.d/50unattended-upgrades"
	if fi, err := os.Stat(logPath); err == nil && !fi.IsDir() {
		lines, err := os.ReadFile(logPath)
		if err == nil {
			nonEmpty := ""
			for _, line := range strings.Split(string(lines), "\n") {
				if strings.TrimSpace(line) != "" {
					nonEmpty = strings.TrimSpace(line)
				}
			}
			if nonEmpty != "" {
				return nonEmpty
			}
			return "active (idle)"
		}
	}
	if fi, err := os.Stat(confPath); err == nil && !fi.IsDir() {
		return "configured (security-only, no reboot)"
	}
	return "unattended-upgrades not installed"
}

func loadDeploymentProvenance() map[string]any {
	home := os.Getenv("MCP_GATEWAY_HOME")
	if home == "" {
		home = "/home/mcp-gateway"
	}
	provPath := filepath.Join(home, ".config", "mcp-gateway", "deployment.json")
	data, err := os.ReadFile(provPath)
	if err != nil {
		return map[string]any{
			"deployed_at": "",
			"git_sha":     "",
		}
	}
	var out map[string]any
	_ = json.Unmarshal(data, &out)
	return out
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

func round1(v float64) float64 {
	return math.Round(v*10) / 10
}
