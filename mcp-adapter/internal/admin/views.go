package admin

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/flosch/pongo2/v6"
	"mcp-gateway-adapter/internal/buildinfo"
	"mcp-gateway-adapter/internal/core"
	"mcp-gateway-adapter/internal/discovery"
	"mcp-gateway-adapter/internal/policy"
	"mcp-gateway-adapter/internal/registry"
)

func parseProjectTaskForm(r *http.Request) (string, registry.Task, error) {
	name := strings.TrimSpace(r.FormValue("task_name"))
	if name == "" {
		return "", registry.Task{}, fmt.Errorf("Task name is required")
	}
	rawArgv := strings.TrimSpace(r.FormValue("argv_json"))
	var argv []string
	if rawArgv == "" || json.Unmarshal([]byte(rawArgv), &argv) != nil || len(argv) == 0 {
		return "", registry.Task{}, fmt.Errorf("Arguments must be a non-empty JSON string array")
	}
	for _, arg := range argv {
		if arg == "" {
			return "", registry.Task{}, fmt.Errorf("Arguments cannot contain empty strings")
		}
	}
	timeout := 30
	if rawTimeout := strings.TrimSpace(r.FormValue("timeout")); rawTimeout != "" {
		parsed, err := strconv.Atoi(rawTimeout)
		if err != nil || parsed < 1 || parsed > 3600 {
			return "", registry.Task{}, fmt.Errorf("Timeout must be between 1 and 3600 seconds")
		}
		timeout = parsed
	}
	return name, registry.Task{
		Argv:    argv,
		Timeout: timeout,
		Enabled: r.FormValue("task_enabled") == "on",
	}, nil
}

func projectTaskRows(project registry.Project) []map[string]interface{} {
	names := make([]string, 0, len(project.Tasks))
	for name := range project.Tasks {
		names = append(names, name)
	}
	sort.Strings(names)
	rows := make([]map[string]interface{}, 0, len(names))
	for _, name := range names {
		task := project.Tasks[name]
		rawArgv, _ := json.Marshal(task.Argv)
		rows = append(rows, map[string]interface{}{
			"name":      name,
			"argv_json": string(rawArgv),
			"timeout":   task.Timeout,
			"enabled":   task.Enabled,
		})
	}
	return rows
}

func (s *Server) failStoreMutation(w http.ResponseWriter, r *http.Request, action, targetID, projectID string, err error) bool {
	if err == nil {
		return false
	}
	s.RecordAudit(r, action, targetID, projectID, false, "STORE_ERROR", err.Error(), false)
	http.Error(w, "Failed to persist requested change.", http.StatusInternalServerError)
	return true
}

func (s *Server) failStoreRead(w http.ResponseWriter, r *http.Request, action string, err error) bool {
	if err == nil {
		return false
	}
	s.RecordAudit(r, action, "", "", false, "STORE_ERROR", err.Error(), false)
	http.Error(w, "Required administrative data is unavailable.", http.StatusInternalServerError)
	return true
}

func parseTargetForm(r *http.Request, targetID string) (registry.Target, error) {
	if err := r.ParseForm(); err != nil {
		return registry.Target{}, fmt.Errorf("parse target form: %w", err)
	}
	id := strings.TrimSpace(targetID)
	if id == "" {
		id = strings.TrimSpace(r.FormValue("id"))
	}
	target := registry.Target{
		ID:              id,
		DisplayName:     strings.TrimSpace(r.FormValue("display_name")),
		Platform:        strings.ToLower(strings.TrimSpace(r.FormValue("platform"))),
		Host:            strings.TrimSpace(r.FormValue("host")),
		User:            strings.TrimSpace(r.FormValue("user")),
		SSHAlias:        strings.TrimSpace(r.FormValue("ssh_alias")),
		PrivilegeUser:   strings.TrimSpace(r.FormValue("privilege_user")),
		PrivilegePolicy: strings.TrimSpace(r.FormValue("privilege_policy")),
		Enabled:         r.FormValue("enabled") == "on",
	}
	if target.ID == "" {
		return target, fmt.Errorf("target id is required")
	}
	if target.Host == "" || target.User == "" {
		return target, fmt.Errorf("host and user are required")
	}
	switch target.Platform {
	case "linux", "windows", "android-termux":
	default:
		return target, fmt.Errorf("unsupported platform %q", r.FormValue("platform"))
	}
	port, err := parseBoundedFormInt(r, "port", 1, 65535)
	target.Port = port
	if err != nil {
		return target, err
	}
	privilegePolicy, err := policy.NormalizePrivilegePolicy(target.PrivilegePolicy)
	if err != nil {
		return target, err
	}
	target.PrivilegePolicy = privilegePolicy
	return target, nil
}

func parseBoundedFormInt(r *http.Request, key string, minValue, maxValue int) (int, error) {
	raw := strings.TrimSpace(r.FormValue(key))
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", key)
	}
	if value < minValue || value > maxValue {
		return 0, fmt.Errorf("%s must be between %d and %d", key, minValue, maxValue)
	}
	return value, nil
}

func getIntSetting(ctx context.Context, store *registry.Store, key string, defaultValue int) (int, error) {
	raw, err := store.GetSetting(ctx, key, strconv.Itoa(defaultValue))
	if err != nil {
		return 0, err
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("setting %s has invalid integer value %q", key, raw)
	}
	return value, nil
}

func getUptimeSec() int {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	parts := strings.Fields(string(data))
	if len(parts) == 0 {
		return 0
	}
	f, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0
	}
	return int(f)
}

func coreResponseStatus(res core.Response) (bool, string) {
	if res.OK {
		return true, ""
	}
	if res.Error != nil && res.Error.Message != "" {
		return false, res.Error.Message
	}
	return false, "unknown error"
}

func safeLocalRedirect(val string) string {
	val = strings.TrimSpace(val)
	if val == "" || strings.ContainsAny(val, "\\\r\n") {
		return "/dashboard"
	}
	u, err := url.Parse(val)
	if err != nil || u.Scheme != "" || u.Host != "" || !strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") {
		return "/dashboard"
	}
	return val
}

// ---------------------------------------------------------------------------
// Authentication Handlers
// ---------------------------------------------------------------------------

const bootstrapTokenTTL = 15 * time.Minute

func (s *Server) hasEnabledAdmin(ctx context.Context) (bool, error) {
	admins, err := s.cfg.Store.ListAdminUsers(ctx)
	if err != nil {
		return false, err
	}
	for _, user := range admins {
		if user.Enabled {
			return true, nil
		}
	}
	return false, nil
}

func (s *Server) readBootstrapToken() (string, error) {
	info, err := os.Lstat(s.cfg.BootstrapTokenFile)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() <= 0 {
		return "", fmt.Errorf("bootstrap token must be a non-empty regular file and not a symlink")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("bootstrap token permissions are too broad")
	}
	if time.Since(info.ModTime()) > bootstrapTokenTTL {
		return "", fmt.Errorf("bootstrap token expired")
	}
	data, err := os.ReadFile(s.cfg.BootstrapTokenFile)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", fmt.Errorf("bootstrap token is empty")
	}
	return token, nil
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	hasAdmin, err := s.hasEnabledAdmin(r.Context())
	if err != nil {
		http.Error(w, "Failed to inspect Admin bootstrap state.", http.StatusInternalServerError)
		return
	}
	if hasAdmin {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	expectedToken, err := s.readBootstrapToken()
	if err != nil {
		http.Error(w, "Initial setup is unavailable; use the local CLI setup command.", http.StatusServiceUnavailable)
		return
	}

	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		s.render(w, r, "setup.html", nil)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form data.", http.StatusBadRequest)
		return
	}

	providedToken := strings.TrimSpace(r.FormValue("bootstrap_token"))
	if len(providedToken) != len(expectedToken) || subtle.ConstantTimeCompare([]byte(providedToken), []byte(expectedToken)) != 1 {
		s.RecordAudit(r, "admin_bootstrap_denied", "", "", false, "INVALID_BOOTSTRAP_TOKEN", "Initial Admin bootstrap token rejected", false)
		s.renderStatus(w, r, "setup.html", pongo2.Context{"error": "Bootstrap token is invalid or expired."}, http.StatusForbidden)
		return
	}

	password := r.FormValue("password")
	confirmation := r.FormValue("password_confirm")
	if password == "" || password != confirmation {
		s.renderStatus(w, r, "setup.html", pongo2.Context{"error": "Passwords must be non-empty and match."}, http.StatusUnprocessableEntity)
		return
	}

	hash, err := GeneratePasswordHash(password)
	if err != nil {
		http.Error(w, "Failed to prepare Admin credentials.", http.StatusInternalServerError)
		return
	}
	if err := s.cfg.Store.SetAdminPassword(r.Context(), "admin", hash); err != nil {
		http.Error(w, "Failed to store Admin credentials.", http.StatusInternalServerError)
		return
	}
	if err := os.Remove(s.cfg.BootstrapTokenFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.RecordAudit(r, "admin_bootstrap_token_cleanup", "", "", false, "TOKEN_CLEANUP_FAILED", err.Error(), false)
	}
	s.RecordAudit(r, "admin_bootstrap", "", "", true, "", "Initial Admin user configured", false)
	getSession(r).Flash("Initial Admin account configured. Sign in to continue.", "success")
	http.Redirect(w, r, "/login", http.StatusFound)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	sess := getSession(r)
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		if sess.Username == "" {
			hasAdmin, err := s.hasEnabledAdmin(r.Context())
			if err != nil {
				http.Error(w, "Failed to inspect Admin state.", http.StatusInternalServerError)
				return
			}
			if !hasAdmin {
				if _, err := s.readBootstrapToken(); err == nil {
					http.Redirect(w, r, "/setup", http.StatusFound)
					return
				}
			}
		}
		if sess.Username != "" {
			http.Redirect(w, r, "/dashboard", http.StatusFound)
			return
		}
		next := r.URL.Query().Get("next")
		s.render(w, r, "login.html", pongo2.Context{
			"next": next,
		})
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form data.", http.StatusBadRequest)
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	next := r.URL.Query().Get("next")
	if next == "" {
		next = r.FormValue("next")
	}

	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}

	// Rate limiting
	if s.loginLimiter.IsRateLimited(ip) || s.loginLimiter.IsRateLimited(username) {
		s.RecordAudit(r, "admin_login_rate_limited", "", "", false, "RATE_LIMITED", fmt.Sprintf("IP %s or user %s locked out", ip, username), false)
		sess.Flash("Too many failed login attempts. Please wait 1 minute.", "danger")
		s.render(w, r, "login.html", pongo2.Context{
			"error": "Too many failed login attempts. Please wait 1 minute.",
			"next":  next,
		})
		return
	}

	adminUser, err := s.cfg.Store.GetAdminUser(r.Context(), username)
	if err != nil || adminUser == nil || !adminUser.Enabled {
		s.loginLimiter.RecordFailure(ip)
		s.loginLimiter.RecordFailure(username)
		s.RecordAudit(r, "admin_login_failed", "", "", false, "AUTH_FAILED", fmt.Sprintf("User '%s' not found or disabled", username), false)
		s.render(w, r, "login.html", pongo2.Context{
			"error": "Invalid username or password.",
			"next":  next,
		})
		return
	}

	if !CheckPasswordHash(adminUser.PasswordHash, password) {
		s.loginLimiter.RecordFailure(ip)
		s.loginLimiter.RecordFailure(username)
		s.RecordAudit(r, "admin_login_failed", "", "", false, "AUTH_FAILED", fmt.Sprintf("Bad password for user '%s'", username), false)
		s.render(w, r, "login.html", pongo2.Context{
			"error": "Invalid username or password.",
			"next":  next,
		})
		return
	}

	// Login successful
	s.loginLimiter.ResetFailures(ip)
	s.loginLimiter.ResetFailures(username)

	sess.Username = username
	sess.LastActive = time.Now()
	sess.CSRFToken = GenerateCSRFToken()

	if err := s.cfg.Store.UpdateAdminLogin(r.Context(), username); err != nil {
		s.RecordAudit(r, "admin_login_metadata", "", "", false, "STORE_ERROR", err.Error(), false)
	}
	s.RecordAudit(r, "admin_login_success", "", "", true, "", fmt.Sprintf("Admin '%s' logged in", username), false)

	http.Redirect(w, r, safeLocalRedirect(next), http.StatusFound)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	sess := getSession(r)
	user := sess.Username
	s.RecordAudit(r, "admin_logout", "", "", true, "", fmt.Sprintf("User '%s' logged out", user), false)

	s.sessionMgr.ClearCookie(w)
	*sess = SessionData{CSRFToken: GenerateCSRFToken()}
	sess.Flash("You have been successfully logged out.", "info")

	http.Redirect(w, r, "/login", http.StatusFound)
}

// ---------------------------------------------------------------------------
// Dashboard
// ---------------------------------------------------------------------------

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/dashboard", http.StatusFound)
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	gatewayEnabled, err := s.cfg.Store.GetRequiredBoolSetting(ctx, "gateway_enabled")
	if err != nil {
		http.Error(w, "Failed to load gateway state.", http.StatusInternalServerError)
		return
	}
	writesEnabledStr, err := s.cfg.Store.GetSetting(ctx, "writes_enabled", "false")
	if err != nil {
		http.Error(w, "Failed to load writes state.", http.StatusInternalServerError)
		return
	}

	targets, targetsErr := s.cfg.Store.ListTargets(ctx)
	totalTargets := 0
	enabledTargets := 0
	if targetsErr == nil {
		totalTargets = len(targets)
		for _, target := range targets {
			if target.Enabled {
				enabledTargets++
			}
		}
	}

	projects, projectsErr := s.cfg.Store.ListProjects(ctx, "")
	totalProjects := 0
	if projectsErr == nil {
		totalProjects = len(projects)
	}

	clients, clientsErr := s.cfg.Store.ListClients(ctx)
	totalClients := 0
	if clientsErr == nil {
		totalClients = len(clients)
	}

	totalRequests, activityCountErr := s.cfg.Store.GetActivityCount(ctx)
	recentActivity, recentActivityErr := s.cfg.Store.ListActivity(ctx, 8, 0, registry.ActivityFilter{})

	loc, timezoneName, err := loadAdminLocation(ctx, s.cfg.Store)
	if err != nil {
		http.Error(w, "Failed to load display time zone.", http.StatusInternalServerError)
		return
	}
	if recentActivityErr == nil {
		recentActivity, err = localizeActivity(recentActivity, loc)
		if err != nil {
			http.Error(w, "Failed to render activity timestamps.", http.StatusInternalServerError)
			return
		}
	}

	mcpReady := s.cfg.Core.Health(ctx, "").OK

	s.render(w, r, "dashboard.html", pongo2.Context{
		"gateway_enabled":          gatewayEnabled,
		"writes_enabled":           writesEnabledStr == "true",
		"gateway_version":          s.cfg.Version,
		"total_targets":            totalTargets,
		"enabled_targets":          enabledTargets,
		"targets_available":        targetsErr == nil,
		"total_projects":           totalProjects,
		"projects_available":       projectsErr == nil,
		"total_clients":            totalClients,
		"clients_available":        clientsErr == nil,
		"total_requests":           totalRequests,
		"activity_count_available": activityCountErr == nil,
		"recent_activity":          recentActivity,
		"activity_available":       recentActivityErr == nil,
		"timezone":                 timezoneName,
		"uptime_sec":               getUptimeSec(),
		"mcp_ready":                mcpReady,
		"mcp_protocol":             buildinfo.MCPProtocol,
		"section":                  "dashboard",
	})
}

// ---------------------------------------------------------------------------
// Targets Management
// ---------------------------------------------------------------------------

func (s *Server) handleTargetsList(w http.ResponseWriter, r *http.Request) {
	targets, err := s.cfg.Store.ListTargets(r.Context())
	if s.failStoreRead(w, r, "list_targets", err) {
		return
	}

	var viewTargets []map[string]interface{}
	for _, t := range targets {
		projects, err := s.cfg.Store.ListProjects(r.Context(), t.ID)
		if s.failStoreRead(w, r, "list_target_projects", err) {
			return
		}
		viewTargets = append(viewTargets, map[string]interface{}{
			"id":               t.ID,
			"display_name":     t.DisplayName,
			"platform":         t.Platform,
			"privilege_policy": t.PrivilegePolicy,
			"enabled":          t.Enabled,
			"project_count":    len(projects),
		})
	}

	s.render(w, r, "targets.html", pongo2.Context{
		"targets": viewTargets,
		"section": "targets",
	})
}

func (s *Server) handleTargetAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.render(w, r, "target_form.html", pongo2.Context{
			"is_edit": false,
			"target":  nil,
			"section": "targets",
		})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	target, err := parseTargetForm(r, "")
	if err != nil {
		getSession(r).Flash(fmt.Sprintf("Invalid Target configuration: %v", err), "danger")
		s.renderStatus(w, r, "target_form.html", pongo2.Context{
			"is_edit": false,
			"target":  target,
			"section": "targets",
		}, http.StatusUnprocessableEntity)
		return
	}
	if target.PrivilegePolicy == "always_allow" {
		sess := getSession(r)
		sess.Flash("Create the Target with a safer privilege policy, then enable Always allow from Edit Target with the required second confirmation.", "danger")
		s.renderStatus(w, r, "target_form.html", pongo2.Context{
			"is_edit": false,
			"target":  target,
			"section": "targets",
		}, http.StatusUnprocessableEntity)
		return
	}
	s.RecordAudit(r, "add_target_attempt", target.ID, "", true, "", fmt.Sprintf("Add target '%s'", target.ID), true)
	activity := s.auditEntry(r, "add_target", target.ID, "", true, "", fmt.Sprintf("Added target '%s'", target.ID))
	if err := s.cfg.Store.AddTargetAudited(r.Context(), target, activity); err != nil {
		sess := getSession(r)
		sess.Flash(fmt.Sprintf("Error creating target: %v", err), "danger")
		s.render(w, r, "target_form.html", pongo2.Context{
			"is_edit": false,
			"target":  target,
			"section": "targets",
		})
		return
	}
	sess := getSession(r)
	sess.Flash(
		fmt.Sprintf("Target '%s' created. Next: verify its pinned SSH host identity, install the gateway public key on the Target account, then Check Connection.", target.ID),
		"success",
	)
	http.Redirect(w, r, fmt.Sprintf("/targets/%s/edit", target.ID), http.StatusFound)
}

func (s *Server) handleTargetSubroutes(w http.ResponseWriter, r *http.Request) {
	relPath := strings.TrimPrefix(r.URL.Path, "/targets/")
	parts := strings.Split(relPath, "/")
	if len(parts) == 0 || parts[0] == "" {
		http.Redirect(w, r, "/targets", http.StatusFound)
		return
	}
	targetID := parts[0]

	if len(parts) == 2 && parts[1] == "edit" {
		s.handleTargetEdit(w, r, targetID)
		return
	}
	if len(parts) == 2 && parts[1] == "toggle" {
		s.handleTargetToggle(w, r, targetID)
		return
	}
	if len(parts) == 2 && parts[1] == "test" {
		s.handleTargetTest(w, r, targetID)
		return
	}
	if len(parts) == 2 && parts[1] == "rediscover" {
		s.handleTargetRediscover(w, r, targetID)
		return
	}
	if len(parts) == 3 && parts[1] == "ssh" && parts[2] == "trust" {
		s.handleTargetSSHTrust(w, r, targetID)
		return
	}
	if len(parts) == 3 && parts[1] == "ssh" && parts[2] == "untrust" {
		s.handleTargetSSHUntrust(w, r, targetID)
		return
	}
	if len(parts) == 3 && parts[1] == "privileges" && parts[2] == "approve" {
		s.handleTargetPrivilegeApprove(w, r, targetID)
		return
	}
	if len(parts) == 3 && parts[1] == "privileges" && parts[2] == "revoke" {
		s.handleTargetPrivilegeRevoke(w, r, targetID)
		return
	}
	if len(parts) == 3 && parts[1] == "privileges" && parts[2] == "always-allow" {
		s.handleTargetPrivilegeAlwaysAllow(w, r, targetID)
		return
	}

	http.NotFound(w, r)
}

func targetStatusFacts(res core.Response) (map[string]any, error) {
	if !res.OK {
		_, message := coreResponseStatus(res)
		return nil, fmt.Errorf("%s", message)
	}
	result, ok := res.Result.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("Target status result has unexpected type %T", res.Result)
	}
	facts, ok := result["facts"].(map[string]any)
	if !ok || facts == nil {
		return nil, fmt.Errorf("Target privilege facts are unavailable")
	}
	if status, _ := facts["probe_status"].(string); status != "ok" {
		reason, _ := facts["reason"].(string)
		if strings.TrimSpace(reason) == "" {
			reason = "Target privilege probe is unavailable"
		}
		return nil, fmt.Errorf("%s", reason)
	}
	return facts, nil
}

func targetApprovalBootID(policyName string, status core.Response) (string, error) {
	if policyName != "ask_once_per_boot" {
		return "", nil
	}
	facts, err := targetStatusFacts(status)
	if err != nil {
		return "", err
	}
	bootID, _ := facts["boot_id"].(string)
	bootID = strings.TrimSpace(bootID)
	if bootID == "" {
		return "", fmt.Errorf("Target boot identity is unavailable")
	}
	return bootID, nil
}

func privilegeScopeAllowed(scopes []map[string]interface{}, clientID, projectID string) bool {
	for _, scope := range scopes {
		if fmt.Sprint(scope["client_id"]) == clientID && fmt.Sprint(scope["project_id"]) == projectID {
			return true
		}
	}
	return false
}

func targetApprovalValid(target registry.Target, approval *registry.PrivilegeApproval, scopes []map[string]interface{}, facts map[string]any) bool {
	if approval == nil || approval.Policy != target.PrivilegePolicy ||
		!privilegeScopeAllowed(scopes, approval.ClientID, approval.ProjectID) {
		return false
	}

	switch target.PrivilegePolicy {
	case "ask_always":
		age := float64(time.Now().UnixNano())/1e9 - approval.ApprovedAt
		return age >= 0 && age <= 300
	case "ask_once_per_boot":
		bootID, _ := facts["boot_id"].(string)
		return strings.TrimSpace(bootID) != "" && approval.BootID == bootID
	default:
		return false
	}
}

func targetPrivilegeStatusView(target registry.Target, approval *registry.PrivilegeApproval, scopes []map[string]interface{}, status core.Response) map[string]interface{} {
	facts, err := targetStatusFacts(status)
	if err != nil {
		return map[string]interface{}{
			"ok": false,
			"error": map[string]string{
				"message": err.Error(),
			},
		}
	}
	privilege, ok := facts["privilege"].(map[string]any)
	if !ok || privilege == nil {
		return map[string]interface{}{
			"ok": false,
			"error": map[string]string{
				"message": "Target privilege facts are unavailable",
			},
		}
	}

	result := make(map[string]interface{}, len(privilege)+5)
	for key, value := range privilege {
		result[key] = value
	}
	result["policy"] = target.PrivilegePolicy
	result["backend_user"] = target.PrivilegeUser
	result["approval"] = approval
	result["approval_valid"] = targetApprovalValid(target, approval, scopes, facts)
	result["boot_id"] = facts["boot_id"]

	return map[string]interface{}{
		"ok":     true,
		"result": result,
	}
}

func (s *Server) renderTargetEdit(w http.ResponseWriter, r *http.Request, formTarget, persistedTarget registry.Target, status int) bool {
	sshIdent, _ := s.cfg.Discovery.InspectTargetIdentity(discovery.TargetConfig{
		ID:       persistedTarget.ID,
		Host:     persistedTarget.Host,
		Port:     persistedTarget.Port,
		SSHAlias: persistedTarget.SSHAlias,
		User:     persistedTarget.User,
		Platform: persistedTarget.Platform,
		Enabled:  persistedTarget.Enabled,
	})
	approval, err := s.cfg.Store.GetPrivilegeApproval(r.Context(), persistedTarget.ID)
	if s.failStoreRead(w, r, "load_target_privilege_approval", err) {
		return false
	}
	privScopes, err := s.targetPrivilegeScopes(r.Context(), persistedTarget.ID)
	if s.failStoreRead(w, r, "load_target_privilege_scopes", err) {
		return false
	}
	privilegeStatus := targetPrivilegeStatusView(
		persistedTarget,
		approval,
		privScopes,
		s.cfg.Core.TargetStatus(r.Context(), "", persistedTarget.ID),
	)
	s.renderStatus(w, r, "target_form.html", pongo2.Context{
		"target":  formTarget,
		"is_edit": true,
		"ssh_identity": map[string]interface{}{
			"ok":     sshIdent != nil,
			"result": sshIdent,
		},
		"gateway_public_key": map[string]interface{}{
			"ok": s.gatewayPubKey != "",
			"result": map[string]string{
				"public_key": s.gatewayPubKey,
			},
		},
		"privilege_status": privilegeStatus,
		"privilege_scopes": privScopes,
		"section":          "targets",
	}, status)
	return true
}

func (s *Server) handleTargetEdit(w http.ResponseWriter, r *http.Request, targetID string) {
	target, err := s.cfg.Store.GetTarget(r.Context(), targetID, true)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if r.Method == http.MethodGet {
		s.renderTargetEdit(w, r, target, target, http.StatusOK)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	updated, err := parseTargetForm(r, targetID)
	if err != nil {
		getSession(r).Flash(fmt.Sprintf("Invalid Target configuration: %v", err), "danger")
		s.renderTargetEdit(w, r, updated, target, http.StatusUnprocessableEntity)
		return
	}
	if target.PrivilegePolicy != "always_allow" && updated.PrivilegePolicy == "always_allow" {
		sess := getSession(r)
		if sess.Pending == nil {
			sess.Pending = make(map[string]interface{})
		}
		sess.Pending["always_allow_target"] = targetID
		sess.Flash("Always allow requires a separate risk confirmation and Admin password re-authentication. Other Target edits were not saved; save them separately first if needed.", "warning")
		http.Redirect(w, r, fmt.Sprintf("/targets/%s/privileges/always-allow", targetID), http.StatusFound)
		return
	}

	s.RecordAudit(r, "update_target_attempt", targetID, "", true, "", fmt.Sprintf("Update Target security context for '%s'", targetID), true)
	activity := s.auditEntry(r, "update_target", targetID, "", true, "", fmt.Sprintf("Updated target '%s'", targetID))
	if s.failStoreMutation(w, r, "update_target", targetID, "", s.cfg.Store.UpdateTargetAudited(r.Context(), updated, activity)) {
		return
	}

	sess := getSession(r)
	sess.Flash(fmt.Sprintf("Target '%s' updated successfully.", targetID), "success")
	http.Redirect(w, r, "/targets", http.StatusFound)
}

func (s *Server) targetPrivilegeScopes(ctx context.Context, targetID string) ([]map[string]interface{}, error) {
	clients, err := s.cfg.Store.ListClients(ctx)
	if err != nil {
		return nil, err
	}
	projects, err := s.cfg.Store.ListProjects(ctx, targetID)
	if err != nil {
		return nil, err
	}
	var scopes []map[string]interface{}
	for _, c := range clients {
		for _, p := range projects {
			ordinary, err := policy.AuthorizeClient(ctx, s.cfg.Store, c.ID, targetID, p.ID, "run_command", false)
			if err != nil {
				return nil, err
			}
			if !ordinary.Allowed {
				continue
			}
			privileged, err := policy.AuthorizePrivilegeRequest(ctx, s.cfg.Store, c.ID, targetID, p.ID)
			if err != nil {
				return nil, err
			}
			if !privileged.Allowed {
				continue
			}
			jsonVal, err := json.Marshal([]string{c.ID, p.ID})
			if err != nil {
				return nil, err
			}
			scopes = append(scopes, map[string]interface{}{
				"client_id":  c.ID,
				"project_id": p.ID,
				"value":      string(jsonVal),
			})
		}
	}
	return scopes, nil
}

func (s *Server) handleTargetToggle(w http.ResponseWriter, r *http.Request, targetID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	target, err := s.cfg.Store.GetTarget(r.Context(), targetID, true)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	target.Enabled = !target.Enabled
	action := "enabled"
	if !target.Enabled {
		action = "disabled"
	}
	s.RecordAudit(r, "toggle_target_attempt", targetID, "", true, "", fmt.Sprintf("Target '%s' -> %s", targetID, action), true)
	activity := s.auditEntry(r, "toggle_target", targetID, "", true, "", fmt.Sprintf("Target '%s' %s", targetID, action))
	if s.failStoreMutation(w, r, "toggle_target", targetID, "", s.cfg.Store.UpdateTargetAudited(r.Context(), target, activity)) {
		return
	}
	sess := getSession(r)
	sess.Flash(fmt.Sprintf("Target '%s' %s.", targetID, action), "success")
	http.Redirect(w, r, "/targets", http.StatusFound)
}

func (s *Server) handleTargetTest(w http.ResponseWriter, r *http.Request, targetID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	sess := getSession(r)
	res := s.cfg.Core.TargetStatus(ctx, "", targetID)

	returnTo := r.FormValue("return_to")
	dest := "/targets"
	if returnTo == "edit" {
		dest = fmt.Sprintf("/targets/%s/edit", targetID)
	}

	if ok, errMsg := coreResponseStatus(res); ok {
		sess.Flash(fmt.Sprintf("Connection test passed for Target '%s'.", targetID), "success")
	} else {
		sess.Flash(fmt.Sprintf("Connection test failed for Target '%s': %s", targetID, errMsg), "danger")
	}

	http.Redirect(w, r, dest, http.StatusFound)
}

func (s *Server) handleTargetRediscover(w http.ResponseWriter, r *http.Request, targetID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	target, err := s.cfg.Store.GetTarget(r.Context(), targetID, true)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	sess := getSession(r)

	result, err := s.cfg.Discovery.FindMovedTarget(discovery.TargetConfig{
		ID:       target.ID,
		Host:     target.Host,
		Port:     target.Port,
		SSHAlias: target.SSHAlias,
		User:     target.User,
		Platform: target.Platform,
		Enabled:  target.Enabled,
	})
	if err != nil {
		s.RecordAudit(r, "rediscover_target", targetID, "", false, "TARGET_REDISCOVERY_FAILED", err.Error(), false)
		sess.Flash(fmt.Sprintf("Target rediscovery failed: %v", err), "danger")
		http.Redirect(w, r, fmt.Sprintf("/targets/%s/edit", targetID), http.StatusFound)
		return
	}

	switch result.Status {
	case "IDENTITY_MATCH":
		s.RecordAudit(r, "rediscover_target", targetID, "", true, "", fmt.Sprintf("Pinned SSH identity found at %s:%d", result.NewHost, result.NewPort), false)
		sess.Flash(
			fmt.Sprintf("Pinned SSH identity found at %s:%d (%s). The Target was not changed. Review the endpoint, then update Host IP / FQDN and Save Target if appropriate.", result.NewHost, result.NewPort, result.Fingerprint),
			"success",
		)
	case "AMBIGUOUS_TARGET_IDENTITY":
		s.RecordAudit(r, "rediscover_target", targetID, "", false, "AMBIGUOUS_TARGET_IDENTITY", result.Error, false)
		sess.Flash("Rediscovery found more than one endpoint presenting the pinned SSH identity. No Target change was made.", "danger")
	default:
		s.RecordAudit(r, "rediscover_target", targetID, "", false, "TARGET_NOT_FOUND", "Pinned SSH identity not found among current kernel neighbors", false)
		sess.Flash("Pinned SSH identity was not found among currently known network neighbors. No Target change was made.", "warning")
	}

	http.Redirect(w, r, fmt.Sprintf("/targets/%s/edit", targetID), http.StatusFound)
}

func (s *Server) handleTargetSSHTrust(w http.ResponseWriter, r *http.Request, targetID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form data.", http.StatusBadRequest)
		return
	}
	fingerprint := strings.TrimSpace(r.FormValue("fingerprint"))
	replace := r.FormValue("replace") == "1"

	target, err := s.cfg.Store.GetTarget(r.Context(), targetID, true)
	sess := getSession(r)
	if err != nil {
		sess.Flash("Target not found", "danger")
		http.Redirect(w, r, "/targets", http.StatusFound)
		return
	}

	s.RecordAudit(r, "trust_target_ssh_identity_attempt", targetID, "", true, "", fmt.Sprintf("Pin host fingerprint %s (replace=%v)", fingerprint, replace), true)
	_, err = s.cfg.Discovery.TrustPresentedKey(discovery.TargetConfig{
		ID:       target.ID,
		Host:     target.Host,
		Port:     target.Port,
		SSHAlias: target.SSHAlias,
	}, fingerprint, replace)

	if err != nil {
		s.RecordAudit(r, "trust_target_ssh_identity", targetID, "", false, "SSH_TRUST_FAILED", err.Error(), false)
		sess.Flash(fmt.Sprintf("Failed to pin host key: %v", err), "danger")
	} else {
		s.RecordAudit(r, "trust_target_ssh_identity", targetID, "", true, "", fmt.Sprintf("Pinned host fingerprint %s", fingerprint), false)
		sess.Flash(fmt.Sprintf("SSH host fingerprint %s successfully pinned for %s.", fingerprint, targetID), "success")
	}
	http.Redirect(w, r, fmt.Sprintf("/targets/%s/edit", targetID), http.StatusFound)
}

func (s *Server) handleTargetSSHUntrust(w http.ResponseWriter, r *http.Request, targetID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	target, err := s.cfg.Store.GetTarget(r.Context(), targetID, true)
	sess := getSession(r)
	if err != nil {
		sess.Flash("Target not found", "danger")
		http.Redirect(w, r, "/targets", http.StatusFound)
		return
	}

	s.RecordAudit(r, "remove_trusted_key_attempt", targetID, "", true, "", "Remove pinned host identity", true)
	_, err = s.cfg.Discovery.RemoveTrustedKey(discovery.TargetConfig{
		ID:       target.ID,
		SSHAlias: target.SSHAlias,
	})
	if err != nil {
		s.RecordAudit(r, "remove_trusted_key", targetID, "", false, "SSH_UNTRUST_FAILED", err.Error(), false)
		sess.Flash(fmt.Sprintf("Failed to remove host key: %v", err), "danger")
	} else {
		s.RecordAudit(r, "remove_trusted_key", targetID, "", true, "", "Removed pinned host identity", false)
		sess.Flash(fmt.Sprintf("Pinned SSH identity removed for %s.", targetID), "info")
	}
	http.Redirect(w, r, fmt.Sprintf("/targets/%s/edit", targetID), http.StatusFound)
}

func (s *Server) handleTargetPrivilegeApprove(w http.ResponseWriter, r *http.Request, targetID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form data.", http.StatusBadRequest)
		return
	}

	target, err := s.cfg.Store.GetTarget(r.Context(), targetID, true)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	policyName := strings.ToLower(strings.TrimSpace(target.PrivilegePolicy))
	if policyName != "ask_always" && policyName != "ask_once_per_boot" {
		getSession(r).Flash("The current Target privilege policy does not accept cached approvals.", "danger")
		http.Redirect(w, r, fmt.Sprintf("/targets/%s/edit", targetID), http.StatusFound)
		return
	}

	scopeVal := r.FormValue("scope")
	var pair []string
	if err := json.Unmarshal([]byte(scopeVal), &pair); err != nil || len(pair) != 2 {
		getSession(r).Flash("Invalid approval scope format", "danger")
		http.Redirect(w, r, fmt.Sprintf("/targets/%s/edit", targetID), http.StatusFound)
		return
	}
	clientID := strings.TrimSpace(pair[0])
	projectID := strings.TrimSpace(pair[1])
	if clientID == "" || projectID == "" {
		getSession(r).Flash("Approval scope requires a client and project.", "danger")
		http.Redirect(w, r, fmt.Sprintf("/targets/%s/edit", targetID), http.StatusFound)
		return
	}

	ordinary, err := policy.AuthorizeClient(r.Context(), s.cfg.Store, clientID, targetID, projectID, "run_command", false)
	if err != nil {
		http.Error(w, "Failed to evaluate approval scope.", http.StatusInternalServerError)
		return
	}
	privileged, err := policy.AuthorizePrivilegeRequest(r.Context(), s.cfg.Store, clientID, targetID, projectID)
	if err != nil {
		http.Error(w, "Failed to evaluate privilege grant.", http.StatusInternalServerError)
		return
	}
	if !ordinary.Allowed || !privileged.Allowed {
		getSession(r).Flash("Approval scope is not currently authorized for both Target shell and target_admin.", "danger")
		http.Redirect(w, r, fmt.Sprintf("/targets/%s/edit", targetID), http.StatusFound)
		return
	}

	bootID := ""
	if policyName == "ask_once_per_boot" {
		bootID, err = targetApprovalBootID(policyName, s.cfg.Core.TargetStatus(r.Context(), "", targetID))
		if err != nil {
			getSession(r).Flash(fmt.Sprintf("Cannot approve current boot: %v", err), "danger")
			http.Redirect(w, r, fmt.Sprintf("/targets/%s/edit", targetID), http.StatusFound)
			return
		}
	}

	s.RecordAudit(r, "approve_target_privilege_attempt", targetID, projectID, true, "", fmt.Sprintf("Approve %s privilege lease for client %s", policyName, clientID), true)
	activity := s.auditEntry(r, "approve_target_privilege", targetID, projectID, true, "", fmt.Sprintf("Approved %s privilege lease for client %s", policyName, clientID))
	if s.failStoreMutation(w, r, "approve_target_privilege", targetID, projectID, s.cfg.Store.SetPrivilegeApprovalAudited(r.Context(), targetID, policyName, clientID, projectID, bootID, activity)) {
		return
	}

	sess := getSession(r)
	if policyName == "ask_once_per_boot" {
		sess.Flash(fmt.Sprintf("Privilege approval granted for %s on project %s for the current Target boot.", clientID, projectID), "success")
	} else {
		sess.Flash(fmt.Sprintf("Privilege approval granted for %s on project %s for the next request (5m TTL).", clientID, projectID), "success")
	}
	http.Redirect(w, r, fmt.Sprintf("/targets/%s/edit", targetID), http.StatusFound)
}

func (s *Server) handleTargetPrivilegeRevoke(w http.ResponseWriter, r *http.Request, targetID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	s.RecordAudit(r, "revoke_target_privilege_attempt", targetID, "", true, "", "Revoke cached privilege approval", true)
	activity := s.auditEntry(r, "revoke_target_privilege", targetID, "", true, "", "Revoked cached privilege approval")
	if s.failStoreMutation(w, r, "revoke_target_privilege", targetID, "", s.cfg.Store.ClearPrivilegeApprovalAudited(r.Context(), targetID, activity)) {
		return
	}
	sess := getSession(r)
	sess.Flash("Cached privilege approval revoked.", "info")
	http.Redirect(w, r, fmt.Sprintf("/targets/%s/edit", targetID), http.StatusFound)
}

func (s *Server) handleTargetPrivilegeAlwaysAllow(w http.ResponseWriter, r *http.Request, targetID string) {
	target, err := s.cfg.Store.GetTarget(r.Context(), targetID, true)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if r.Method == http.MethodGet {
		s.render(w, r, "target_always_allow_confirm.html", pongo2.Context{
			"target":         target,
			"current_policy": target.PrivilegePolicy,
			"section":        "targets",
		})
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form data.", http.StatusBadRequest)
		return
	}
	confirmID := strings.TrimSpace(r.FormValue("confirm_target_id"))
	password := r.FormValue("password")
	sess := getSession(r)

	if confirmID != targetID {
		sess.Flash("Target ID confirmation mismatch.", "danger")
		http.Redirect(w, r, fmt.Sprintf("/targets/%s/privileges/always-allow", targetID), http.StatusFound)
		return
	}

	adminUser, err := s.cfg.Store.GetAdminUser(r.Context(), sess.Username)
	if err != nil || adminUser == nil || !CheckPasswordHash(adminUser.PasswordHash, password) {
		sess.Flash("Invalid admin password for confirmation.", "danger")
		http.Redirect(w, r, fmt.Sprintf("/targets/%s/privileges/always-allow", targetID), http.StatusFound)
		return
	}

	s.RecordAudit(r, "target_privilege_policy_change_attempt", targetID, "", true, "", fmt.Sprintf("{\"new_policy\":\"always_allow\",\"old_policy\":\"%s\"}", target.PrivilegePolicy), true)

	target.PrivilegePolicy = "always_allow"
	activity := s.auditEntry(r, "target_privilege_policy_change", targetID, "", true, "", "Enabled always_allow with re-authentication")
	if s.failStoreMutation(w, r, "target_privilege_policy_change", targetID, "", s.cfg.Store.UpdateTargetAudited(r.Context(), target, activity)) {
		return
	}
	sess.Flash(fmt.Sprintf("Privilege policy set to always_allow for Target '%s'.", targetID), "warning")
	http.Redirect(w, r, fmt.Sprintf("/targets/%s/edit", targetID), http.StatusFound)
}

// ---------------------------------------------------------------------------
// Projects Management
// ---------------------------------------------------------------------------

func (s *Server) handleProjectsList(w http.ResponseWriter, r *http.Request) {
	targetFilter := r.URL.Query().Get("target")
	projects, err := s.cfg.Store.ListProjects(r.Context(), targetFilter)
	if s.failStoreRead(w, r, "list_projects", err) {
		return
	}
	s.render(w, r, "projects.html", pongo2.Context{
		"projects":      projects,
		"target_filter": targetFilter,
		"section":       "projects",
	})
}

func (s *Server) handleProjectAdd(w http.ResponseWriter, r *http.Request) {
	targets, err := s.cfg.Store.ListTargets(r.Context())
	if s.failStoreRead(w, r, "list_targets_for_project", err) {
		return
	}
	if r.Method == http.MethodGet {
		s.render(w, r, "project_form.html", pongo2.Context{
			"is_edit": false,
			"project": nil,
			"targets": targets,
			"section": "projects",
		})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form data.", http.StatusBadRequest)
		return
	}
	project := registry.Project{
		TargetID:    strings.TrimSpace(r.FormValue("target_id")),
		ID:          strings.TrimSpace(r.FormValue("id")),
		DisplayName: strings.TrimSpace(r.FormValue("display_name")),
		Root:        strings.TrimSpace(r.FormValue("root")),
		Read:        r.FormValue("read") == "on",
		Write:       r.FormValue("write") == "on",
		Enabled:     r.FormValue("enabled") == "on",
	}
	sess := getSession(r)
	if project.TargetID == "" || project.ID == "" || project.Root == "" {
		sess.Flash("Target, Project ID, and Root are required.", "danger")
		s.renderStatus(w, r, "project_form.html", pongo2.Context{
			"is_edit": false,
			"project": project,
			"targets": targets,
			"section": "projects",
		}, http.StatusUnprocessableEntity)
		return
	}
	s.RecordAudit(r, "add_project_attempt", project.TargetID, project.ID, true, "", fmt.Sprintf("Add project '%s'", project.ID), true)
	activity := s.auditEntry(r, "add_project", project.TargetID, project.ID, true, "", fmt.Sprintf("Added project '%s'", project.ID))
	if s.failStoreMutation(w, r, "add_project", project.TargetID, project.ID, s.cfg.Store.AddProjectAudited(r.Context(), project, activity)) {
		return
	}
	sess.Flash(fmt.Sprintf("Project '%s' created successfully.", project.ID), "success")
	http.Redirect(w, r, "/projects", http.StatusFound)
}

func (s *Server) handleProjectSubroutes(w http.ResponseWriter, r *http.Request) {
	relPath := strings.TrimPrefix(r.URL.Path, "/projects/")
	parts := strings.Split(relPath, "/")
	if len(parts) < 3 {
		http.NotFound(w, r)
		return
	}
	targetID := parts[0]
	projectID := parts[1]
	action := parts[2]

	project, err := s.cfg.Store.GetProject(r.Context(), targetID, projectID, true)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	sess := getSession(r)

	if action == "task-add" || action == "task-update" || action == "task-delete" {
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid form data.", http.StatusBadRequest)
			return
		}
		editURL := fmt.Sprintf("/projects/%s/%s/edit", targetID, projectID)
		taskName := strings.TrimSpace(r.FormValue("task_name"))

		if action == "task-delete" {
			if taskName == "" {
				sess.Flash("Task name is required.", "danger")
				http.Redirect(w, r, editURL, http.StatusFound)
				return
			}
			if _, exists := project.Tasks[taskName]; !exists {
				http.NotFound(w, r)
				return
			}
			s.RecordAudit(r, "delete_task_attempt", targetID, projectID, true, "", fmt.Sprintf("Delete task '%s'", taskName), true)
			activity := s.auditEntry(r, "delete_task", targetID, projectID, true, "", fmt.Sprintf("Deleted task '%s'", taskName))
			if s.failStoreMutation(w, r, "delete_task", targetID, projectID, s.cfg.Store.DeleteTaskAudited(r.Context(), targetID, projectID, taskName, activity)) {
				return
			}
			sess.Flash(fmt.Sprintf("Task '%s' deleted.", taskName), "success")
			http.Redirect(w, r, editURL, http.StatusFound)
			return
		}

		taskName, task, err := parseProjectTaskForm(r)
		if err != nil {
			sess.Flash(err.Error(), "danger")
			http.Redirect(w, r, editURL, http.StatusFound)
			return
		}
		if action == "task-add" {
			if _, exists := project.Tasks[taskName]; exists {
				sess.Flash(fmt.Sprintf("Task '%s' already exists.", taskName), "danger")
				http.Redirect(w, r, editURL, http.StatusFound)
				return
			}
			s.RecordAudit(r, "add_task_attempt", targetID, projectID, true, "", fmt.Sprintf("Add task '%s'", taskName), true)
			activity := s.auditEntry(r, "add_task", targetID, projectID, true, "", fmt.Sprintf("Added task '%s'", taskName))
			if s.failStoreMutation(w, r, "add_task", targetID, projectID, s.cfg.Store.AddTaskAudited(r.Context(), targetID, projectID, taskName, task, activity)) {
				return
			}
			sess.Flash(fmt.Sprintf("Task '%s' added.", taskName), "success")
		} else {
			if _, exists := project.Tasks[taskName]; !exists {
				http.NotFound(w, r)
				return
			}
			s.RecordAudit(r, "update_task_attempt", targetID, projectID, true, "", fmt.Sprintf("Update task '%s'", taskName), true)
			activity := s.auditEntry(r, "update_task", targetID, projectID, true, "", fmt.Sprintf("Updated task '%s'", taskName))
			if s.failStoreMutation(w, r, "update_task", targetID, projectID, s.cfg.Store.UpdateTaskAudited(r.Context(), targetID, projectID, taskName, task, activity)) {
				return
			}
			sess.Flash(fmt.Sprintf("Task '%s' updated.", taskName), "success")
		}
		http.Redirect(w, r, editURL, http.StatusFound)
		return
	}

	if action == "edit" {
		if r.Method == http.MethodGet {
			targets, err := s.cfg.Store.ListTargets(r.Context())
			if s.failStoreRead(w, r, "list_targets_for_project_edit", err) {
				return
			}
			s.render(w, r, "project_form.html", pongo2.Context{
				"project":   project,
				"is_edit":   true,
				"targets":   targets,
				"task_rows": projectTaskRows(project),
				"section":   "projects",
			})
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid form data.", http.StatusBadRequest)
			return
		}
		project.DisplayName = strings.TrimSpace(r.FormValue("display_name"))
		project.Root = strings.TrimSpace(r.FormValue("root"))
		project.Read = r.FormValue("read") == "on"
		project.Write = r.FormValue("write") == "on"
		project.Enabled = r.FormValue("enabled") == "on"
		if project.Root == "" {
			sess.Flash("Root Directory Path is required.", "danger")
			targets, listErr := s.cfg.Store.ListTargets(r.Context())
			if s.failStoreRead(w, r, "list_targets_for_project_edit_validation", listErr) {
				return
			}
			s.renderStatus(w, r, "project_form.html", pongo2.Context{
				"project":   project,
				"is_edit":   true,
				"targets":   targets,
				"task_rows": projectTaskRows(project),
				"section":   "projects",
			}, http.StatusUnprocessableEntity)
			return
		}
		s.RecordAudit(r, "update_project_attempt", targetID, projectID, true, "", fmt.Sprintf("Update project '%s'", projectID), true)
		activity := s.auditEntry(r, "update_project", targetID, projectID, true, "", fmt.Sprintf("Updated project '%s'", projectID))
		if s.failStoreMutation(w, r, "update_project", targetID, projectID, s.cfg.Store.UpdateProjectAudited(r.Context(), project, activity)) {
			return
		}
		sess.Flash(fmt.Sprintf("Project '%s' updated.", projectID), "success")
		http.Redirect(w, r, "/projects", http.StatusFound)
		return
	}

	if action == "toggle" {
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		project.Enabled = !project.Enabled
		s.RecordAudit(r, "toggle_project_attempt", targetID, projectID, true, "", fmt.Sprintf("Project enabled -> %v", project.Enabled), true)
		activity := s.auditEntry(r, "toggle_project", targetID, projectID, true, "", fmt.Sprintf("Project enabled=%v", project.Enabled))
		if s.failStoreMutation(w, r, "toggle_project", targetID, projectID, s.cfg.Store.UpdateProjectAudited(r.Context(), project, activity)) {
			return
		}
		sess.Flash(fmt.Sprintf("Project '%s' enabled=%v.", projectID, project.Enabled), "success")
		http.Redirect(w, r, "/projects", http.StatusFound)
		return
	}

	if action == "toggle-read" || action == "toggle-write" {
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		field := "read"
		if action == "toggle-read" {
			project.Read = !project.Read
		} else {
			field = "write"
			project.Write = !project.Write
		}
		value := project.Read
		if field == "write" {
			value = project.Write
		}
		auditAction := "toggle_project_" + field
		s.RecordAudit(r, auditAction+"_attempt", targetID, projectID, true, "", fmt.Sprintf("Project %s -> %v", field, value), true)
		activity := s.auditEntry(r, auditAction, targetID, projectID, true, "", fmt.Sprintf("Project %s=%v", field, value))
		if s.failStoreMutation(w, r, auditAction, targetID, projectID, s.cfg.Store.UpdateProjectAudited(r.Context(), project, activity)) {
			return
		}
		sess.Flash(fmt.Sprintf("Project '%s' %s capability set to %v.", projectID, field, value), "success")
		http.Redirect(w, r, "/projects", http.StatusFound)
		return
	}

	http.NotFound(w, r)
}

// ---------------------------------------------------------------------------
// Clients Management

// ---------------------------------------------------------------------------
// Clients Management
// ---------------------------------------------------------------------------

func (s *Server) handleClientsList(w http.ResponseWriter, r *http.Request) {
	clients, err := s.cfg.Store.ListClients(r.Context())
	if s.failStoreRead(w, r, "list_clients", err) {
		return
	}

	var viewClients []map[string]interface{}
	for _, c := range clients {
		grants, err := s.cfg.Store.ListGrants(r.Context(), c.ID)
		if s.failStoreRead(w, r, "list_client_grants", err) {
			return
		}
		summary := map[string]bool{
			"structured_write": false,
			"tasks":            false,
			"target_shell":     false,
			"target_admin":     false,
			"gateway_admin":    false,
		}
		for _, g := range grants {
			if !g.Enabled {
				continue
			}
			capability := strings.TrimSpace(g.Capability)
			if capability == "*" || capability == "write" {
				summary["structured_write"] = true
			}
			if capability == "*" || capability == "tasks" {
				summary["tasks"] = true
			}
			if capability == "*" || capability == "target_shell" {
				summary["target_shell"] = true
			}
			if capability == "target_admin" {
				summary["target_admin"] = true
			}
			if capability == "*" || capability == "admin" {
				summary["gateway_admin"] = true
			}
		}
		viewClients = append(viewClients, map[string]interface{}{
			"id":            c.ID,
			"display_name":  c.DisplayName,
			"provider":      c.Provider,
			"protocol":      c.Protocol,
			"notes":         c.Notes,
			"enabled":       c.Enabled,
			"grant_summary": summary,
			"grant_count":   len(grants),
		})
	}

	s.render(w, r, "clients.html", pongo2.Context{
		"clients": viewClients,
		"section": "clients",
	})
}

func (s *Server) handleClientAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.render(w, r, "client_form.html", pongo2.Context{
			"is_edit": false,
			"client":  nil,
			"section": "clients",
		})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form data.", http.StatusBadRequest)
		return
	}
	client := registry.Client{
		ID:          strings.TrimSpace(r.FormValue("id")),
		DisplayName: strings.TrimSpace(r.FormValue("display_name")),
		Provider:    strings.TrimSpace(r.FormValue("provider")),
		Protocol:    strings.TrimSpace(r.FormValue("protocol")),
		Notes:       strings.TrimSpace(r.FormValue("notes")),
		Enabled:     r.FormValue("enabled") == "on",
	}
	if client.Protocol == "" {
		client.Protocol = "mcp"
	}

	sess := getSession(r)
	if client.ID == "" || client.DisplayName == "" {
		sess.Flash("Client ID and Display Name are required.", "danger")
		s.renderStatus(w, r, "client_form.html", pongo2.Context{
			"is_edit": false,
			"client":  client,
			"section": "clients",
		}, http.StatusUnprocessableEntity)
		return
	}
	if policy.IsReservedInternalClientID(client.ID) {
		sess.Flash("That Client ID is reserved for internal gateway principals.", "danger")
		s.renderStatus(w, r, "client_form.html", pongo2.Context{
			"is_edit": false,
			"client":  client,
			"section": "clients",
		}, http.StatusUnprocessableEntity)
		return
	}

	s.RecordAudit(r, "add_client_attempt", "", "", true, "", fmt.Sprintf("Add AI client '%s'", client.ID), true)
	activity := s.auditEntry(r, "add_client", "", "", true, "", fmt.Sprintf("Added AI client '%s'", client.ID))
	if s.failStoreMutation(w, r, "add_client", "", "", s.cfg.Store.AddClientAudited(r.Context(), client, activity)) {
		return
	}
	sess.Flash(fmt.Sprintf("Client '%s' registered.", client.ID), "success")
	http.Redirect(w, r, "/clients", http.StatusFound)
}

func (s *Server) handleClientSubroutes(w http.ResponseWriter, r *http.Request) {
	relPath := strings.TrimPrefix(r.URL.Path, "/clients/")
	parts := strings.Split(relPath, "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	clientID := parts[0]
	client, err := s.cfg.Store.GetClient(r.Context(), clientID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	sess := getSession(r)
	if len(parts) == 2 && parts[1] == "edit" {
		if r.Method == http.MethodGet {
			s.render(w, r, "client_form.html", pongo2.Context{
				"client":  client,
				"is_edit": true,
				"section": "clients",
			})
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid form data.", http.StatusBadRequest)
			return
		}
		client.DisplayName = strings.TrimSpace(r.FormValue("display_name"))
		client.Provider = strings.TrimSpace(r.FormValue("provider"))
		client.Protocol = strings.TrimSpace(r.FormValue("protocol"))
		client.Notes = strings.TrimSpace(r.FormValue("notes"))
		client.Enabled = r.FormValue("enabled") == "on"
		if client.DisplayName == "" {
			sess.Flash("Display Name is required.", "danger")
			s.renderStatus(w, r, "client_form.html", pongo2.Context{
				"client":  client,
				"is_edit": true,
				"section": "clients",
			}, http.StatusUnprocessableEntity)
			return
		}
		if client.Protocol == "" {
			client.Protocol = "mcp"
		}
		s.RecordAudit(r, "update_client_attempt", "", "", true, "", fmt.Sprintf("Update AI client '%s'", clientID), true)
		activity := s.auditEntry(r, "update_client", "", "", true, "", fmt.Sprintf("Updated client '%s'", clientID))
		if s.failStoreMutation(w, r, "update_client", "", "", s.cfg.Store.UpdateClientAudited(r.Context(), client, activity)) {
			return
		}
		sess.Flash(fmt.Sprintf("Client '%s' updated.", clientID), "success")
		http.Redirect(w, r, "/clients", http.StatusFound)
		return
	}

	if len(parts) == 2 && parts[1] == "toggle" {
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		client.Enabled = !client.Enabled
		s.RecordAudit(r, "toggle_client_attempt", "", "", true, "", fmt.Sprintf("Client '%s' enabled -> %v", clientID, client.Enabled), true)
		activity := s.auditEntry(r, "toggle_client", "", "", true, "", fmt.Sprintf("Client enabled=%v", client.Enabled))
		if s.failStoreMutation(w, r, "toggle_client", "", "", s.cfg.Store.UpdateClientAudited(r.Context(), client, activity)) {
			return
		}
		sess.Flash(fmt.Sprintf("Client '%s' enabled=%v.", clientID, client.Enabled), "success")
		http.Redirect(w, r, "/clients", http.StatusFound)
		return
	}

	if len(parts) >= 2 && parts[1] == "grants" {
		s.handleClientGrants(w, r, client, parts[2:])
		return
	}

	http.NotFound(w, r)
}

func (s *Server) renderClientGrants(w http.ResponseWriter, r *http.Request, client registry.Client, editingGrant *registry.Grant, accessResult interface{}) bool {
	return s.renderClientGrantsStatus(w, r, client, editingGrant, editingGrant, accessResult, http.StatusOK)
}

func (s *Server) renderClientGrantsStatus(
	w http.ResponseWriter,
	r *http.Request,
	client registry.Client,
	editingGrant *registry.Grant,
	formGrant *registry.Grant,
	accessResult interface{},
	status int,
) bool {
	grants, err := s.cfg.Store.ListGrants(r.Context(), client.ID)
	if s.failStoreRead(w, r, "list_client_grants", err) {
		return false
	}
	targets, err := s.cfg.Store.ListTargets(r.Context())
	if s.failStoreRead(w, r, "list_targets_for_grants", err) {
		return false
	}
	projects, err := s.cfg.Store.ListProjects(r.Context(), "")
	if s.failStoreRead(w, r, "list_projects_for_grants", err) {
		return false
	}
	s.renderStatus(w, r, "client_grants.html", pongo2.Context{
		"client":             client,
		"grants":             grants,
		"targets":            targets,
		"projects":           projects,
		"tool_names":         core.CatalogTools(),
		"grant_capabilities": policy.GrantCapabilities(),
		"editing_grant":      editingGrant,
		"grant_form":         formGrant,
		"access_result":      accessResult,
		"section":            "clients",
	}, status)
	return true
}

func validateGrantForm(ctx context.Context, store *registry.Store, r *http.Request, clientID string, existing *registry.Grant) (registry.Grant, error) {
	if err := r.ParseForm(); err != nil {
		return registry.Grant{}, fmt.Errorf("parse grant form: %w", err)
	}
	targetID := strings.TrimSpace(r.FormValue("target_id"))
	projectID := strings.TrimSpace(r.FormValue("project_id"))
	capability := strings.TrimSpace(r.FormValue("capability"))
	if projectID == "" {
		projectID = "*"
	}
	grant := registry.Grant{
		ClientID:   clientID,
		TargetID:   targetID,
		ProjectID:  projectID,
		Capability: capability,
		Enabled:    r.FormValue("enabled") == "on",
	}
	if existing != nil {
		grant.ID = existing.ID
	}
	if targetID == "" {
		return grant, fmt.Errorf("Target is required")
	}
	if capability == "" {
		return grant, fmt.Errorf("Capability is required")
	}
	if !policy.IsGrantCapability(capability) {
		return grant, fmt.Errorf("unsupported capability %q", capability)
	}
	if targetID != "*" {
		if _, err := store.GetTarget(ctx, targetID, true); err != nil {
			return grant, fmt.Errorf("invalid Target scope: %w", err)
		}
	}
	if projectID != "*" {
		if targetID == "*" {
			return grant, fmt.Errorf("a specific Project requires a specific Target")
		}
		if _, err := store.GetProject(ctx, targetID, projectID, true); err != nil {
			return grant, fmt.Errorf("invalid Project scope: %w", err)
		}
	}
	highImpactCapability := capability == "*" || capability == policy.TargetPrivilegeCapability
	if highImpactCapability && (targetID == "*" || projectID == "*") && r.FormValue("confirm_global") != "on" {
		return grant, fmt.Errorf("high-impact wildcard scope requires explicit confirmation")
	}

	grants, err := store.ListGrants(ctx, clientID)
	if err != nil {
		return grant, fmt.Errorf("list existing grants: %w", err)
	}
	for _, candidate := range grants {
		if existing != nil && candidate.ID == existing.ID {
			continue
		}
		if candidate.TargetID == targetID && candidate.ProjectID == projectID && strings.TrimSpace(candidate.Capability) == capability {
			return grant, fmt.Errorf("duplicate grant already exists for this client, Target, Project, and capability")
		}
	}
	return grant, nil
}

func (s *Server) handleClientGrants(w http.ResponseWriter, r *http.Request, client registry.Client, sub []string) {
	sess := getSession(r)

	if len(sub) == 0 {
		if r.Method != http.MethodGet {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		s.renderClientGrants(w, r, client, nil, nil)
		return
	}

	if len(sub) == 1 && sub[0] == "add" {
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		grant, err := validateGrantForm(r.Context(), s.cfg.Store, r, client.ID, nil)
		if err != nil {
			sess.Flash(fmt.Sprintf("Invalid grant: %v", err), "danger")
			s.renderClientGrantsStatus(w, r, client, nil, &grant, nil, http.StatusUnprocessableEntity)
			return
		}
		s.RecordAudit(r, "add_grant_attempt", grant.TargetID, grant.ProjectID, true, "", fmt.Sprintf("Add grant '%s' for client '%s'", grant.Capability, client.ID), true)
		activity := s.auditEntry(r, "add_grant", grant.TargetID, grant.ProjectID, true, "", fmt.Sprintf("Added grant '%s' for client '%s'", grant.Capability, client.ID))
		if _, err := s.cfg.Store.AddGrantAudited(r.Context(), grant, activity); s.failStoreMutation(w, r, "add_grant", grant.TargetID, grant.ProjectID, err) {
			return
		}
		sess.Flash("Grant added successfully.", "success")
		http.Redirect(w, r, fmt.Sprintf("/clients/%s/grants", client.ID), http.StatusFound)
		return
	}

	if len(sub) == 1 && sub[0] == "check" {
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid form data.", http.StatusBadRequest)
			return
		}
		targetID := strings.TrimSpace(r.FormValue("target_id"))
		projectID := strings.TrimSpace(r.FormValue("project_id"))
		toolName := strings.TrimSpace(r.FormValue("tool_name"))
		privilegeRequest := strings.ToLower(strings.TrimSpace(r.FormValue("privilege")))
		if privilegeRequest == "" {
			privilegeRequest = "standard"
		}
		evalTargetID := targetID
		evalProjectID := projectID
		if evalTargetID == "*" {
			evalTargetID = ""
		}
		if evalProjectID == "*" {
			evalProjectID = ""
		}

		allowed := false
		code := ""
		message := ""
		var privilege map[string]any
		if toolName == "run_command" {
			check, err := s.cfg.Core.CheckRunCommandAccess(r.Context(), client.ID, evalTargetID, evalProjectID, privilegeRequest)
			if s.failStoreRead(w, r, "check_effective_run_command_access", err) {
				return
			}
			allowed = check.Allowed
			code = check.Code
			message = check.Reason
			privilege = check.Privilege
			if allowed && message == "" {
				message = "Allowed by current policy and Target privilege gate."
			}
		} else {
			res, err := policy.AuthorizeClient(r.Context(), s.cfg.Store, client.ID, evalTargetID, evalProjectID, toolName, false)
			if s.failStoreRead(w, r, "check_effective_access", err) {
				return
			}
			allowed = res.Allowed
			code = res.Code
			message = res.Reason
			if allowed && message == "" {
				message = "Allowed by current policy."
			}
		}

		accessResult := map[string]interface{}{
			"allowed":           allowed,
			"code":              code,
			"tool_name":         toolName,
			"target_id":         targetID,
			"project_id":        projectID,
			"privilege_request": privilegeRequest,
			"privilege":         privilege,
			"message":           message,
		}
		s.renderClientGrants(w, r, client, nil, accessResult)
		return
	}

	if len(sub) == 2 {
		grantID, err := strconv.ParseInt(sub[0], 10, 64)
		if err != nil || grantID <= 0 {
			http.NotFound(w, r)
			return
		}
		action := sub[1]
		grant, err := s.cfg.Store.GetGrantForClient(r.Context(), client.ID, grantID)
		if err != nil {
			http.NotFound(w, r)
			return
		}

		if action == "edit" {
			if r.Method == http.MethodGet {
				s.renderClientGrants(w, r, client, &grant, nil)
				return
			}
			if r.Method != http.MethodPost {
				http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
				return
			}
			updated, err := validateGrantForm(r.Context(), s.cfg.Store, r, client.ID, &grant)
			if err != nil {
				sess.Flash(fmt.Sprintf("Invalid grant: %v", err), "danger")
				s.renderClientGrantsStatus(w, r, client, &grant, &updated, nil, http.StatusUnprocessableEntity)
				return
			}
			s.RecordAudit(r, "update_grant_attempt", updated.TargetID, updated.ProjectID, true, "", fmt.Sprintf("Update grant %d for client '%s'", grantID, client.ID), true)
			activity := s.auditEntry(r, "update_grant", updated.TargetID, updated.ProjectID, true, "", fmt.Sprintf("Updated grant %d for client '%s'", grantID, client.ID))
			if s.failStoreMutation(w, r, "update_grant", updated.TargetID, updated.ProjectID, s.cfg.Store.UpdateGrantForClientAudited(r.Context(), client.ID, updated, activity)) {
				return
			}
			sess.Flash("Grant updated.", "success")
			http.Redirect(w, r, fmt.Sprintf("/clients/%s/grants", client.ID), http.StatusFound)
			return
		}

		if action == "toggle" {
			if r.Method != http.MethodPost {
				http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
				return
			}
			grant.Enabled = !grant.Enabled
			s.RecordAudit(r, "toggle_grant_attempt", grant.TargetID, grant.ProjectID, true, "", fmt.Sprintf("Grant %d enabled -> %v", grantID, grant.Enabled), true)
			activity := s.auditEntry(r, "toggle_grant", grant.TargetID, grant.ProjectID, true, "", fmt.Sprintf("Grant %d enabled=%v", grantID, grant.Enabled))
			if s.failStoreMutation(w, r, "toggle_grant", grant.TargetID, grant.ProjectID, s.cfg.Store.UpdateGrantForClientAudited(r.Context(), client.ID, grant, activity)) {
				return
			}
			sess.Flash("Grant status updated.", "success")
			http.Redirect(w, r, fmt.Sprintf("/clients/%s/grants", client.ID), http.StatusFound)
			return
		}

		if action == "delete" {
			if r.Method != http.MethodPost {
				http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
				return
			}
			s.RecordAudit(r, "delete_grant_attempt", grant.TargetID, grant.ProjectID, true, "", fmt.Sprintf("Delete grant %d for client '%s'", grantID, client.ID), true)
			activity := s.auditEntry(r, "delete_grant", grant.TargetID, grant.ProjectID, true, "", fmt.Sprintf("Deleted grant %d for client %s", grantID, client.ID))
			if s.failStoreMutation(w, r, "delete_grant", grant.TargetID, grant.ProjectID, s.cfg.Store.DeleteGrantForClientAudited(r.Context(), client.ID, grantID, activity)) {
				return
			}
			sess.Flash("Grant deleted.", "info")
			http.Redirect(w, r, fmt.Sprintf("/clients/%s/grants", client.ID), http.StatusFound)
			return
		}
	}

	http.NotFound(w, r)
}

// ---------------------------------------------------------------------------
// Activity Audit
// ---------------------------------------------------------------------------

func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	live := r.URL.Query().Get("live") == "1"
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 || live {
		page = 1
	}
	limit := 25
	offset := (page - 1) * limit

	loc, timezoneName, err := loadAdminLocation(ctx, s.cfg.Store)
	if err != nil {
		http.Error(w, "Failed to load display time zone.", http.StatusInternalServerError)
		return
	}
	actorFilter := r.URL.Query().Get("actor")
	actionFilter := r.URL.Query().Get("action")
	targetFilter := r.URL.Query().Get("target_id")
	projectFilter := r.URL.Query().Get("project_id")
	resultFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("result")))
	if resultFilter != "" && resultFilter != "pass" && resultFilter != "deny" && resultFilter != "attempt" {
		http.Error(w, "Invalid result filter.", http.StatusBadRequest)
		return
	}
	fromFilter := r.URL.Query().Get("from")
	toFilter := r.URL.Query().Get("to")

	fromUTC, err := localFilterToUTC(fromFilter, loc)
	if err != nil {
		http.Error(w, "Invalid From date/time filter.", http.StatusBadRequest)
		return
	}
	toUTC, err := localFilterToUTC(toFilter, loc)
	if err != nil {
		http.Error(w, "Invalid To date/time filter.", http.StatusBadRequest)
		return
	}
	if fromUTC != "" && toUTC != "" && fromUTC > toUTC {
		http.Error(w, "From date/time must not be after To date/time.", http.StatusBadRequest)
		return
	}

	filter := registry.ActivityFilter{
		Actor:     actorFilter,
		Action:    actionFilter,
		TargetID:  targetFilter,
		ProjectID: projectFilter,
		Result:    strings.ToUpper(resultFilter),
		From:      fromUTC,
		To:        toUTC,
	}
	displayFilters := map[string]string{
		"actor":      actorFilter,
		"action":     actionFilter,
		"target_id":  targetFilter,
		"project_id": projectFilter,
		"result":     resultFilter,
		"from":       fromFilter,
		"to":         toFilter,
	}

	total, err := s.cfg.Store.GetActivityCountFiltered(ctx, filter)
	if err != nil {
		http.Error(w, "Failed to count activity.", http.StatusInternalServerError)
		return
	}
	totalPages := int(math.Ceil(float64(total) / float64(limit)))
	if totalPages < 1 {
		totalPages = 1
	}

	items, err := s.cfg.Store.ListActivity(ctx, limit, offset, filter)
	if err != nil {
		http.Error(w, "Failed to load activity.", http.StatusInternalServerError)
		return
	}
	items, err = localizeActivity(items, loc)
	if err != nil {
		http.Error(w, "Failed to render activity timestamps.", http.StatusInternalServerError)
		return
	}

	buildURL := func(p int) string {
		q := r.URL.Query()
		q.Set("page", strconv.Itoa(p))
		return "/activity?" + q.Encode()
	}
	buildLiveURL := func(enabled bool) string {
		q := r.URL.Query()
		q.Del("page")
		if enabled {
			q.Set("live", "1")
		} else {
			q.Del("live")
		}
		encoded := q.Encode()
		if encoded == "" {
			return "/activity"
		}
		return "/activity?" + encoded
	}

	prevURL := ""
	if page > 1 {
		prevURL = buildURL(page - 1)
	}
	nextURL := ""
	if page < totalPages {
		nextURL = buildURL(page + 1)
	}
	clearURL := "/activity"
	if live {
		clearURL = "/activity?live=1"
	}

	s.render(w, r, "activity.html", pongo2.Context{
		"items":          items,
		"page":           page,
		"total_pages":    totalPages,
		"total":          total,
		"prev_url":       prevURL,
		"next_url":       nextURL,
		"live":           live,
		"live_url":       buildLiveURL(true),
		"pause_live_url": buildLiveURL(false),
		"clear_url":      clearURL,
		"filters":        displayFilters,
		"timezone":       timezoneName,
		"section":        "activity",
	})
}

// ---------------------------------------------------------------------------
// Settings & Kill Switch
// ---------------------------------------------------------------------------

func (s *Server) loadSettingsView(ctx context.Context) (map[string]interface{}, error) {
	gatewayEnabled, err := s.cfg.Store.GetRequiredBoolSetting(ctx, "gateway_enabled")
	if err != nil {
		return nil, err
	}
	writesEnabled, err := s.cfg.Store.GetSetting(ctx, "writes_enabled", "false")
	if err != nil {
		return nil, err
	}
	shellEnabled, err := s.cfg.Store.GetSetting(ctx, "shell_enabled", "false")
	if err != nil {
		return nil, err
	}
	defaultTimeout, err := getIntSetting(ctx, s.cfg.Store, "default_timeout", 30)
	if err != nil {
		return nil, err
	}
	maxOutput, err := getIntSetting(ctx, s.cfg.Store, "max_output_bytes", 262144)
	if err != nil {
		return nil, err
	}
	maxRead, err := getIntSetting(ctx, s.cfg.Store, "max_file_read_bytes", 1048576)
	if err != nil {
		return nil, err
	}
	maxWrite, err := getIntSetting(ctx, s.cfg.Store, "max_write_bytes", 262144)
	if err != nil {
		return nil, err
	}
	retention, err := getIntSetting(ctx, s.cfg.Store, "activity_retention", 5000)
	if err != nil {
		return nil, err
	}
	_, adminTimezone, err := loadAdminLocation(ctx, s.cfg.Store)
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"gateway_enabled":     gatewayEnabled,
		"writes_enabled":      writesEnabled == "true",
		"shell_enabled":       shellEnabled == "true",
		"default_timeout":     defaultTimeout,
		"max_output_bytes":    maxOutput,
		"max_file_read_bytes": maxRead,
		"max_write_bytes":     maxWrite,
		"activity_retention":  retention,
		"admin_timezone":      adminTimezone,
	}, nil
}

func submittedSettingsView(r *http.Request, persisted map[string]interface{}) map[string]interface{} {
	settings := make(map[string]interface{}, len(persisted))
	for key, value := range persisted {
		settings[key] = value
	}
	for _, key := range []string{
		"default_timeout",
		"max_output_bytes",
		"max_file_read_bytes",
		"max_write_bytes",
		"activity_retention",
		"admin_timezone",
	} {
		settings[key] = strings.TrimSpace(r.FormValue(key))
	}
	return settings
}

func (s *Server) renderSettingsValidationError(w http.ResponseWriter, r *http.Request, message string) {
	settings, err := s.loadSettingsView(r.Context())
	if err != nil {
		http.Error(w, "Failed to load settings.", http.StatusInternalServerError)
		return
	}
	getSession(r).Flash(message, "danger")
	s.renderStatus(w, r, "settings.html", pongo2.Context{
		"settings": submittedSettingsView(r, settings),
		"section":  "settings",
	}, http.StatusUnprocessableEntity)
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid form data.", http.StatusBadRequest)
			return
		}
		timeout, err := parseBoundedFormInt(r, "default_timeout", 5, 300)
		if err != nil {
			s.renderSettingsValidationError(w, r, err.Error())
			return
		}
		maxOutput, err := parseBoundedFormInt(r, "max_output_bytes", 1024, 10485760)
		if err != nil {
			s.renderSettingsValidationError(w, r, err.Error())
			return
		}
		maxRead, err := parseBoundedFormInt(r, "max_file_read_bytes", 1024, 10485760)
		if err != nil {
			s.renderSettingsValidationError(w, r, err.Error())
			return
		}
		maxWrite, err := parseBoundedFormInt(r, "max_write_bytes", 1024, 10485760)
		if err != nil {
			s.renderSettingsValidationError(w, r, err.Error())
			return
		}
		retention, err := parseBoundedFormInt(r, "activity_retention", 100, 50000)
		if err != nil {
			s.renderSettingsValidationError(w, r, err.Error())
			return
		}
		adminTimezone, err := validateAdminTimezone(r.FormValue("admin_timezone"))
		if err != nil {
			s.renderSettingsValidationError(w, r, err.Error())
			return
		}

		s.RecordAudit(r, "update_settings_attempt", "", "", true, "", "Update operational limits and display preferences", true)
		values := map[string]string{
			"default_timeout":     strconv.Itoa(timeout),
			"max_output_bytes":    strconv.Itoa(maxOutput),
			"max_file_read_bytes": strconv.Itoa(maxRead),
			"max_write_bytes":     strconv.Itoa(maxWrite),
			"activity_retention":  strconv.Itoa(retention),
			"admin_timezone":      adminTimezone,
		}
		activity := s.auditEntry(r, "update_settings", "", "", true, "", "Updated configuration settings")
		if s.failStoreMutation(w, r, "update_settings", "", "", s.cfg.Store.SetSettingsAudited(ctx, values, activity)) {
			return
		}
		getSession(r).Flash("Settings saved successfully.", "success")
		http.Redirect(w, r, "/settings", http.StatusFound)
		return
	}

	settings, err := s.loadSettingsView(ctx)
	if err != nil {
		http.Error(w, "Failed to load settings.", http.StatusInternalServerError)
		return
	}
	s.render(w, r, "settings.html", pongo2.Context{
		"settings": settings,
		"section":  "settings",
	})
}

func (s *Server) handleSettingsKillSwitch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	current, err := s.cfg.Store.GetRequiredBoolSetting(r.Context(), "gateway_enabled")
	if err != nil {
		http.Error(w, "Failed to load gateway state.", http.StatusInternalServerError)
		return
	}
	newVal := strconv.FormatBool(!current)
	s.RecordAudit(r, "kill_switch_toggle_attempt", "", "", true, "", fmt.Sprintf("gateway_enabled -> %s", newVal), true)
	activity := s.auditEntry(r, "kill_switch_toggle", "", "", true, "", fmt.Sprintf("gateway_enabled set to %s", newVal))
	if s.failStoreMutation(w, r, "kill_switch_toggle", "", "", s.cfg.Store.SetSettingAudited(r.Context(), "gateway_enabled", newVal, activity)) {
		return
	}

	sess := getSession(r)
	if newVal == "true" {
		sess.Flash("Global gateway enabled: client traffic permitted.", "success")
	} else {
		sess.Flash("KILL SWITCH ACTIVATED: all client requests blocked immediately.", "danger")
	}
	http.Redirect(w, r, "/settings", http.StatusFound)
}

func (s *Server) handleSettingsToggleWrites(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	currStr, err := s.cfg.Store.GetSetting(r.Context(), "writes_enabled", "false")
	if err != nil {
		http.Error(w, "Failed to load writes state.", http.StatusInternalServerError)
		return
	}
	newVal := "true"
	if currStr == "true" {
		newVal = "false"
	}
	s.RecordAudit(r, "toggle_writes_attempt", "", "", true, "", fmt.Sprintf("writes_enabled -> %s", newVal), true)
	activity := s.auditEntry(r, "toggle_writes", "", "", true, "", fmt.Sprintf("writes_enabled set to %s", newVal))
	if s.failStoreMutation(w, r, "toggle_writes", "", "", s.cfg.Store.SetSettingAudited(r.Context(), "writes_enabled", newVal, activity)) {
		return
	}

	sess := getSession(r)
	if newVal == "true" {
		sess.Flash("Controlled writes enabled for authorized projects.", "warning")
	} else {
		sess.Flash("Structured filesystem writes disabled globally.", "info")
	}
	http.Redirect(w, r, "/settings", http.StatusFound)
}

func (s *Server) handleSettingsDisableWrites(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	s.RecordAudit(r, "disable_writes_attempt", "", "", true, "", "writes_enabled -> false", true)
	activity := s.auditEntry(r, "disable_writes", "", "", true, "", "writes_enabled set to false")
	if s.failStoreMutation(w, r, "disable_writes", "", "", s.cfg.Store.SetSettingAudited(r.Context(), "writes_enabled", "false", activity)) {
		return
	}
	getSession(r).Flash("Filesystem writes disabled.", "info")
	http.Redirect(w, r, "/settings", http.StatusFound)
}

func (s *Server) handleSettingsToggleShell(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	currStr, err := s.cfg.Store.GetSetting(r.Context(), "shell_enabled", "false")
	if err != nil {
		http.Error(w, "Failed to load shell state.", http.StatusInternalServerError)
		return
	}
	newVal := "true"
	if currStr == "true" {
		newVal = "false"
	}
	s.RecordAudit(r, "toggle_shell_attempt", "", "", true, "", fmt.Sprintf("shell_enabled -> %s", newVal), true)
	activity := s.auditEntry(r, "toggle_shell", "", "", true, "", fmt.Sprintf("shell_enabled set to %s", newVal))
	if s.failStoreMutation(w, r, "toggle_shell", "", "", s.cfg.Store.SetSettingAudited(r.Context(), "shell_enabled", newVal, activity)) {
		return
	}

	sess := getSession(r)
	if newVal == "true" {
		sess.Flash("High-risk trusted target shell enabled for authorized clients.", "warning")
	} else {
		sess.Flash("Target shell disabled globally.", "info")
	}
	http.Redirect(w, r, "/settings", http.StatusFound)
}

func (s *Server) handleMaintenance(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	loc, timezoneName, err := loadAdminLocation(ctx, s.cfg.Store)
	if err != nil {
		http.Error(w, "Failed to load display time zone.", http.StatusInternalServerError)
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		http.Error(w, "Failed to resolve gateway home.", http.StatusInternalServerError)
		return
	}
	backupDir := filepath.Join(home, ".local", "share", "mcp-gateway", "backups")
	entries, err := os.ReadDir(backupDir)
	backupError := ""
	if err != nil && !os.IsNotExist(err) {
		backupError = err.Error()
	}

	var backups []map[string]interface{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".db") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			backupError = err.Error()
			continue
		}
		backups = append(backups, map[string]interface{}{
			"name":    entry.Name(),
			"size_kb": info.Size() / 1024,
			"mtime":   info.ModTime().In(loc).Format(time.RFC3339),
		})
	}

	doctor := s.cfg.Core.GatewayDoctor(ctx, "", core.DoctorOptions{CheckTargets: false})
	overallStatus := "ERROR"
	var checks []core.DoctorCheck
	if doctor.OK {
		if result, ok := doctor.Result.(map[string]any); ok {
			if status, ok := result["status"].(string); ok && status != "" {
				overallStatus = status
			}
			if doctorChecks, ok := result["checks"].([]core.DoctorCheck); ok {
				checks = doctorChecks
			}
		}
	}

	info := buildinfo.Current()
	sdkVersion := info.MCPSDKVersion
	if sdkVersion == "" {
		sdkVersion = "unknown"
	}
	compat := map[string]interface{}{
		"gateway_version":         info.GatewayVersion,
		"core_api_version":        info.CoreAPIVersion,
		"bridge_api_version":      info.BridgeAPIVersion,
		"tool_catalog_version":    info.ToolCatalogVersion,
		"registry_schema_version": registry.SchemaVersion,
		"mcp": map[string]interface{}{
			"protocol": info.MCPProtocol,
			"sdk":      "go-sdk",
			"version":  sdkVersion,
		},
	}

	s.render(w, r, "maintenance.html", pongo2.Context{
		"overall_status": overallStatus,
		"checks":         checks,
		"compat":         compat,
		"tool_count":     len(core.CatalogTools()),
		"backups":        backups,
		"backup_error":   backupError,
		"timezone":       timezoneName,
		"section":        "maintenance",
	})
}

func (s *Server) handleMaintenanceDoctor(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	sess := getSession(r)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	res := s.cfg.Core.GatewayDoctor(ctx, "", core.DoctorOptions{CheckTargets: true})
	if ok, errMsg := coreResponseStatus(res); ok {
		sess.Flash("Doctor diagnostics completed successfully.", "success")
	} else {
		sess.Flash(fmt.Sprintf("Doctor reported issues: %s", errMsg), "danger")
	}
	http.Redirect(w, r, "/maintenance", http.StatusFound)
}

func (s *Server) handleMaintenanceBackup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	sess := getSession(r)
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	res := s.cfg.Core.GatewayBackup(ctx, "", sess.Username, "")
	if ok, errMsg := coreResponseStatus(res); ok {
		sess.Flash("Verified online Registry backup created successfully.", "success")
	} else {
		sess.Flash(fmt.Sprintf("Backup failed: %s", errMsg), "danger")
	}
	http.Redirect(w, r, "/maintenance", http.StatusFound)
}

func (s *Server) handleMaintenanceRepair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	sess := getSession(r)
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	res := s.cfg.Core.GatewayMaintenance(ctx, "", sess.Username)
	if ok, errMsg := coreResponseStatus(res); ok {
		sess.Flash("Safe gateway maintenance completed successfully.", "success")
	} else {
		sess.Flash(fmt.Sprintf("Maintenance action failed: %s", errMsg), "danger")
	}
	http.Redirect(w, r, "/maintenance", http.StatusFound)
}

// ---------------------------------------------------------------------------
// System Info
// ---------------------------------------------------------------------------

func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request) {
	hostname, _ := os.Hostname()

	model := "Linux Device"
	if data, err := os.ReadFile("/proc/device-tree/model"); err == nil {
		model = strings.Trim(string(data), "\x00\r\n ")
	}

	kernel := "unknown"
	var uname syscall.Utsname
	if err := syscall.Uname(&uname); err == nil {
		var b strings.Builder
		for _, c := range uname.Release {
			if c == 0 {
				break
			}
			b.WriteByte(byte(c))
		}
		kernel = b.String()
	}

	memTotal := 0
	memAvailable := 0
	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		lines := strings.Split(string(data), "\n")
		for _, l := range lines {
			if strings.HasPrefix(l, "MemTotal:") {
				fields := strings.Fields(l)
				if len(fields) >= 2 {
					val, _ := strconv.Atoi(fields[1])
					memTotal = val / 1024
				}
			}
			if strings.HasPrefix(l, "MemAvailable:") {
				fields := strings.Fields(l)
				if len(fields) >= 2 {
					val, _ := strconv.Atoi(fields[1])
					memAvailable = val / 1024
				}
			}
		}
	}

	var statfs syscall.Statfs_t
	totalDisk := 0
	freeDisk := 0
	usedDisk := 0
	if err := syscall.Statfs("/", &statfs); err == nil {
		totalDisk = int(uint64(statfs.Blocks) * uint64(statfs.Bsize) / (1024 * 1024))
		freeDisk = int(uint64(statfs.Bavail) * uint64(statfs.Bsize) / (1024 * 1024))
		usedDisk = totalDisk - freeDisk
	}

	dbPath := s.cfg.Store.Path()
	var dbSizeKB int64
	if fi, err := os.Stat(dbPath); err == nil {
		dbSizeKB = fi.Size() / 1024
	}

	s.render(w, r, "system.html", pongo2.Context{
		"gateway_version": s.cfg.Version,
		"backend_type":    "sqlite (schema v5)",
		"db_path":         dbPath,
		"db_size_kb":      dbSizeKB,
		"go_ver":          runtime.Version(),
		"hostname":        hostname,
		"model":           model,
		"arch":            runtime.GOARCH,
		"kernel":          kernel,
		"mem_total":       memTotal,
		"mem_available":   memAvailable,
		"total_disk":      totalDisk,
		"free_disk":       freeDisk,
		"used_disk":       usedDisk,
		"section":         "system",
	})
}
