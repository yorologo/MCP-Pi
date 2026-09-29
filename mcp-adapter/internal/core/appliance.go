package core

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
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

const (
	gatewayAdminServiceUnit       = "mcp-gateway-admin.service"
	gatewayMCPServiceUnit         = "mcp-gateway-mcp.service"
	gatewayGeminiServiceUnit      = "mcp-gateway-gemini.service"
	gatewayTunnelServiceUnit      = "mcp-gateway-tunnel.service"
	gatewayCloudflaredServiceUnit = "mcp-gateway-cloudflared.service"
	gatewayMaintenanceServiceUnit = "mcp-gateway-maintenance.service"
	gatewayMaintenanceTimerUnit   = "mcp-gateway-maintenance.timer"
	gatewayPostbootServiceUnit    = "mcp-gateway-postboot.service"

	cloudflaredBinaryPath = "/usr/local/bin/cloudflared"
	cloudflaredTokenPath  = "/home/mcp-gateway/.config/mcp-gateway/cloudflared.token"
)

var applianceObservedServiceUnits = []string{
	gatewayAdminServiceUnit,
	gatewayMCPServiceUnit,
	gatewayGeminiServiceUnit,
	gatewayTunnelServiceUnit,
	gatewayCloudflaredServiceUnit,
	gatewayMaintenanceServiceUnit,
	gatewayMaintenanceTimerUnit,
	gatewayPostbootServiceUnit,
}

var registryQuiescenceServiceUnits = []string{
	gatewayAdminServiceUnit,
	gatewayMCPServiceUnit,
	gatewayGeminiServiceUnit,
	gatewayMaintenanceServiceUnit,
	gatewayMaintenanceTimerUnit,
	gatewayPostbootServiceUnit,
}

func ApplianceObservedServiceUnits() []string {
	return append([]string(nil), applianceObservedServiceUnits...)
}

func RegistryQuiescenceServiceUnits() []string {
	return append([]string(nil), registryQuiescenceServiceUnits...)
}

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
	services := getServiceStates(ApplianceObservedServiceUnits())
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

type ExternalDependencyEvidence struct {
	Passed        bool   `json:"passed"`
	BinaryPath    string `json:"binary_path"`
	ResolvedPath  string `json:"resolved_path,omitempty"`
	Version       string `json:"version,omitempty"`
	SHA256        string `json:"sha256,omitempty"`
	SizeBytes     int64  `json:"size_bytes,omitempty"`
	TokenPath     string `json:"token_path"`
	TokenMode     string `json:"token_mode,omitempty"`
	TokenOwnerUID uint32 `json:"token_owner_uid,omitempty"`
	Message       string `json:"message"`
}

func inspectCloudflaredDependency(ctx context.Context, binaryPath, tokenPath string) ExternalDependencyEvidence {
	evidence := ExternalDependencyEvidence{
		BinaryPath: binaryPath,
		TokenPath:  tokenPath,
	}

	resolved, err := filepath.EvalSymlinks(binaryPath)
	if err != nil {
		evidence.Message = "cloudflared binary is unavailable: " + err.Error()
		return evidence
	}
	evidence.ResolvedPath = resolved

	info, err := os.Stat(resolved)
	if err != nil {
		evidence.Message = "cloudflared binary stat failed: " + err.Error()
		return evidence
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		evidence.Message = "cloudflared binary is not a regular executable file"
		return evidence
	}
	evidence.SizeBytes = info.Size()

	file, err := os.Open(resolved)
	if err != nil {
		evidence.Message = "cloudflared binary cannot be read for provenance: " + err.Error()
		return evidence
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		_ = file.Close()
		evidence.Message = "cloudflared binary hash failed: " + err.Error()
		return evidence
	}
	if err := file.Close(); err != nil {
		evidence.Message = "cloudflared binary close failed: " + err.Error()
		return evidence
	}
	evidence.SHA256 = hex.EncodeToString(hash.Sum(nil))

	versionCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	versionOut, err := exec.CommandContext(versionCtx, resolved, "--version").CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(versionOut))
		if msg == "" {
			msg = err.Error()
		}
		evidence.Message = "cloudflared version check failed: " + msg
		return evidence
	}
	evidence.Version = strings.TrimSpace(string(versionOut))
	if evidence.Version == "" {
		evidence.Message = "cloudflared version check returned empty output"
		return evidence
	}

	tokenInfo, err := os.Lstat(tokenPath)
	if err != nil {
		evidence.Message = "Cloudflare token metadata is unavailable: " + err.Error()
		return evidence
	}
	if tokenInfo.Mode()&os.ModeSymlink != 0 || !tokenInfo.Mode().IsRegular() || tokenInfo.Size() <= 0 {
		evidence.Message = "Cloudflare token must be a non-empty regular file and not a symlink"
		return evidence
	}
	evidence.TokenMode = fmt.Sprintf("%04o", tokenInfo.Mode().Perm())

	stat, ok := tokenInfo.Sys().(*syscall.Stat_t)
	if !ok {
		evidence.Message = "Cloudflare token ownership metadata is unavailable"
		return evidence
	}
	evidence.TokenOwnerUID = stat.Uid
	if int(stat.Uid) != os.Getuid() {
		evidence.Message = fmt.Sprintf("Cloudflare token owner uid=%d does not match gateway uid=%d", stat.Uid, os.Getuid())
		return evidence
	}
	if tokenInfo.Mode().Perm()&0o077 != 0 {
		evidence.Message = "Cloudflare token permissions are too broad; group/other access must be removed"
		return evidence
	}

	evidence.Passed = true
	evidence.Message = "cloudflared binary provenance and private token metadata verified"
	return evidence
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

func (c *Core) privilegeHygieneChecks(ctx context.Context) []DoctorCheck {
	targets, err := c.store.ListTargets(ctx)
	if err != nil {
		return []DoctorCheck{{
			Name: "Privilege Hygiene: Target Policies", Passed: false,
			Message:  "unable to inspect Target privilege policies: " + err.Error(),
			Severity: "warning", Required: false,
		}}
	}
	var alwaysAllow []string
	for _, target := range targets {
		if target.Enabled && target.PrivilegePolicy == "always_allow" {
			alwaysAllow = append(alwaysAllow, target.ID)
		}
	}
	sort.Strings(alwaysAllow)

	targetCheck := DoctorCheck{
		Name: "Privilege Hygiene: Target Policies", Passed: len(alwaysAllow) == 0,
		Message:  "no enabled Targets use always_allow",
		Severity: "warning", Required: false,
	}
	if len(alwaysAllow) > 0 {
		targetCheck.Message = "enabled Targets using persistent always_allow: " + strings.Join(alwaysAllow, ", ")
	}

	grants, err := c.store.ListGrants(ctx, "")
	if err != nil {
		return append([]DoctorCheck{targetCheck}, DoctorCheck{
			Name: "Privilege Hygiene: target_admin Scope", Passed: false,
			Message:  "unable to inspect target_admin grants: " + err.Error(),
			Severity: "warning", Required: false,
		})
	}
	var broadAdmin []string
	for _, grant := range grants {
		if !grant.Enabled || grant.Capability != "target_admin" {
			continue
		}
		if grant.TargetID == "*" || grant.ProjectID == "*" {
			broadAdmin = append(broadAdmin, fmt.Sprintf("%s:%s/%s", grant.ClientID, grant.TargetID, grant.ProjectID))
		}
	}
	sort.Strings(broadAdmin)

	grantCheck := DoctorCheck{
		Name: "Privilege Hygiene: target_admin Scope", Passed: len(broadAdmin) == 0,
		Message:  "no enabled target_admin grants use wildcard scope",
		Severity: "warning", Required: false,
	}
	if len(broadAdmin) > 0 {
		grantCheck.Message = "enabled target_admin grants with wildcard scope: " + strings.Join(broadAdmin, ", ")
	}
	return []DoctorCheck{targetCheck, grantCheck}
}

func (c *Core) GatewayDoctor(ctx context.Context, requestID string, opts DoctorOptions) Response {
	started := time.Now()
	checks := []DoctorCheck{{
		Name:     "Gateway Core Health",
		Passed:   true,
		Message:  "Core is initialized and responding",
		Severity: "error",
		Required: true,
	}}
	checks = append(checks, c.privilegeHygieneChecks(ctx)...)

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
				res, err := c.remote.RunCommand(ctx, target, "hostname", remote.CommandOptions{Timeout: targetReachabilityTimeout})
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

	applianceStatus := "WARN"
	applianceMsg := "systemd appliance checks unavailable on this platform"
	mcpAdapterStatus := "SKIP"
	mcpAdapterMsg := "MCP readiness not evaluated"
	tunnelStatus := "SKIP"
	tunnelMsg := "optional tunnel not evaluated"
	geminiStatus := "SKIP"
	geminiMsg := "optional Gemini ingress not evaluated"
	cloudflareStatus := "SKIP"
	cloudflareMsg := "optional Cloudflare connector not evaluated"
	var cloudflareDependency *ExternalDependencyEvidence

	if applianceSystemdAvailable() {
		adminActive := serviceIsActive(gatewayAdminServiceUnit)
		mcpActive := serviceIsActive(gatewayMCPServiceUnit)
		servicesPassed := adminActive && mcpActive
		checks = append(checks,
			DoctorCheck{
				Name: "Admin Service", Passed: adminActive,
				Message: serviceStateMessage(gatewayAdminServiceUnit, adminActive), Severity: "error", Required: true,
			},
			DoctorCheck{
				Name: "MCP Service", Passed: mcpActive,
				Message: serviceStateMessage(gatewayMCPServiceUnit, mcpActive), Severity: "error", Required: true,
			},
		)

		mcpReady, mcpReadyMsg := httpEndpointReady(ctx, "http://127.0.0.1:8090/ready")
		checks = append(checks, DoctorCheck{
			Name: "MCP Readiness", Passed: mcpReady,
			Message: mcpReadyMsg, Severity: "error", Required: true,
		})
		mcpAdapterStatus, mcpAdapterMsg = "FAIL", mcpReadyMsg
		if mcpReady {
			mcpAdapterStatus, mcpAdapterMsg = "PASS", mcpReadyMsg
		}

		timerEnabled := serviceIsEnabled(gatewayMaintenanceTimerUnit)
		checks = append(checks, DoctorCheck{
			Name: "Maintenance Timer", Passed: timerEnabled,
			Message: enabledStateMessage(gatewayMaintenanceTimerUnit, timerEnabled), Severity: "warning", Required: true,
		})

		postbootFailed := serviceIsFailed(gatewayPostbootServiceUnit)
		checks = append(checks, DoctorCheck{
			Name: "Post-Boot Verification", Passed: !postbootFailed,
			Message: failedStateMessage(gatewayPostbootServiceUnit, postbootFailed), Severity: "error", Required: true,
		})

		applianceStatus, applianceMsg = "FAIL", "required appliance service/readiness checks failed"
		if servicesPassed && mcpReady && !postbootFailed {
			applianceStatus, applianceMsg = "PASS", "Admin/MCP services active and MCP readiness verified"
		}

		if serviceIsEnabled(gatewayTunnelServiceUnit) {
			if serviceIsActive(gatewayTunnelServiceUnit) {
				tunnelStatus, tunnelMsg = "PASS", "enabled and active"
			} else {
				tunnelStatus, tunnelMsg = "WARN", "enabled but not active"
				checks = append(checks, DoctorCheck{
					Name: "Optional Tunnel", Passed: false, Message: tunnelMsg, Severity: "warning", Required: false,
				})
			}
		} else {
			tunnelStatus, tunnelMsg = "SKIP", "not enabled"
		}

		if serviceIsEnabled(gatewayGeminiServiceUnit) {
			geminiActive := serviceIsActive(gatewayGeminiServiceUnit)
			geminiPassed := geminiActive
			geminiMsg = serviceStateMessage(gatewayGeminiServiceUnit, geminiActive)
			if geminiActive {
				geminiPassed, geminiMsg = httpEndpointReady(ctx, "http://127.0.0.1:8092/ready")
			}
			geminiStatus = "PASS"
			if !geminiPassed {
				geminiStatus = "WARN"
			}
			checks = append(checks, DoctorCheck{
				Name: "Gemini MCP Ingress", Passed: geminiPassed,
				Message: geminiMsg, Severity: "warning", Required: false,
			})
		} else {
			geminiStatus, geminiMsg = "SKIP", "not enabled"
		}

		if serviceIsEnabled(gatewayCloudflaredServiceUnit) {
			cloudflareActive := serviceIsActive(gatewayCloudflaredServiceUnit)
			dependency := inspectCloudflaredDependency(ctx, cloudflaredBinaryPath, cloudflaredTokenPath)
			cloudflareDependency = &dependency
			cloudflarePassed := cloudflareActive && dependency.Passed
			cloudflareMsg = serviceStateMessage(gatewayCloudflaredServiceUnit, cloudflareActive) + "; " + dependency.Message
			cloudflareStatus = "PASS"
			if !cloudflarePassed {
				cloudflareStatus = "WARN"
			}
			checks = append(checks, DoctorCheck{
				Name: "Cloudflare Connector", Passed: cloudflarePassed,
				Message: cloudflareMsg, Severity: "warning", Required: false,
			})
		} else {
			cloudflareStatus, cloudflareMsg = "SKIP", "not enabled"
		}
	} else {
		checks = append(checks, DoctorCheck{
			Name: "Appliance Services", Passed: false,
			Message: applianceMsg, Severity: "warning", Required: true,
		})
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
		"appliance":         map[string]string{"status": applianceStatus, "message": applianceMsg},
		"client_entrypoint": map[string]string{"status": clientStatus, "message": clientMsg},
		"tunnel":            map[string]string{"status": tunnelStatus, "message": tunnelMsg},
		"gemini_ingress":    map[string]string{"status": geminiStatus, "message": geminiMsg},
		"cloudflare_tunnel": map[string]string{"status": cloudflareStatus, "message": cloudflareMsg},
		"mcp_adapter":       map[string]string{"status": mcpAdapterStatus, "message": mcpAdapterMsg},
		"gateway_core":      map[string]string{"status": "PASS", "message": "Core initialized with current Registry schema"},
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
	if cloudflareDependency != nil {
		res["external_dependencies"] = map[string]any{
			"cloudflared": *cloudflareDependency,
		}
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

	if overall == "ERROR" {
		return errorResponseWithResult("gateway_doctor", "DOCTOR_FAILED", "Doctor detected required check failures", res, requestID, "", "", started)
	}
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
	entries, err := os.ReadDir(backupsDir)
	if err != nil {
		return errorResponse("gateway_maintenance", "BACKUP_ENUMERATION_FAILED", "Failed to enumerate backup directory: "+err.Error(), requestID, "", "", started)
	}
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

func getServiceStates(services []string) map[string]string {
	states := make(map[string]string, len(services))
	if !systemdAvailable() {
		for _, service := range services {
			states[service] = "unknown"
		}
		return states
	}
	for _, service := range services {
		switch {
		case serviceIsActive(service):
			states[service] = "active"
		case serviceIsFailed(service):
			states[service] = "failed"
		default:
			states[service] = "inactive"
		}
	}
	return states
}

func systemdAvailable() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return false
	}
	_, err := exec.LookPath("systemctl")
	return err == nil
}

func applianceSystemdAvailable() bool {
	if !systemdAvailable() {
		return false
	}
	for _, path := range []string{
		"/etc/systemd/system/mcp-gateway-admin.service",
		"/etc/systemd/system/mcp-gateway-mcp.service",
	} {
		if _, err := os.Stat(path); err != nil {
			return false
		}
	}
	return true
}

func serviceIsActive(service string) bool {
	return exec.Command("systemctl", "is-active", "--quiet", service).Run() == nil
}

func serviceIsEnabled(service string) bool {
	return exec.Command("systemctl", "is-enabled", "--quiet", service).Run() == nil
}

func serviceIsFailed(service string) bool {
	return exec.Command("systemctl", "is-failed", "--quiet", service).Run() == nil
}

func serviceStateMessage(service string, active bool) string {
	if active {
		return service + " is active"
	}
	return service + " is not active"
}

func enabledStateMessage(service string, enabled bool) string {
	if enabled {
		return service + " is enabled"
	}
	return service + " is not enabled"
}

func failedStateMessage(service string, failed bool) string {
	if failed {
		return service + " is failed"
	}
	return service + " is not failed"
}

func httpEndpointReady(ctx context.Context, endpoint string) (bool, string) {
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false, "invalid readiness endpoint: " + err.Error()
	}
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		return false, endpoint + " unavailable: " + err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Sprintf("%s returned HTTP %d", endpoint, resp.StatusCode)
	}
	return true, endpoint + " returned HTTP 200"
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

	paths := []string{
		filepath.Join(home, "mcp-gateway", ".deployment.json"),
		filepath.Join(home, ".config", "mcp-gateway", "deployment.json"),
	}
	for _, provPath := range paths {
		data, err := os.ReadFile(provPath)
		if err != nil {
			continue
		}
		var out map[string]any
		if err := json.Unmarshal(data, &out); err != nil {
			return map[string]any{"available": false}
		}
		out["available"] = true
		return out
	}
	return map[string]any{"available": false}
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

func round1(v float64) float64 {
	return math.Round(v*10) / 10
}
