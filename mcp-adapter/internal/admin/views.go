package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/flosch/pongo2/v6"
	"mcp-gateway-adapter/internal/core"
	"mcp-gateway-adapter/internal/discovery"
	"mcp-gateway-adapter/internal/policy"
	"mcp-gateway-adapter/internal/registry"
)

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

func invokeStatus(res map[string]any) (bool, string) {
	if res == nil {
		return false, "no response from core"
	}
	ok, _ := res["ok"].(bool)
	if ok {
		return true, ""
	}
	errMsg := "unknown error"
	if errMap, isMap := res["error"].(map[string]any); isMap {
		if m, isStr := errMap["message"].(string); isStr && m != "" {
			errMsg = m
		}
	}
	return false, errMsg
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

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	sess := getSession(r)
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
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

	_ = r.ParseForm()
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

	_ = s.cfg.Store.UpdateAdminLogin(r.Context(), username)
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
	gwEnabledStr, _ := s.cfg.Store.GetSetting(r.Context(), "gateway_enabled", "true")
	gwEnabled := gwEnabledStr == "true"
	writesEnabledStr, _ := s.cfg.Store.GetSetting(r.Context(), "writes_enabled", "false")
	writesEnabled := writesEnabledStr == "true"

	targets, _ := s.cfg.Store.ListTargets(r.Context())
	totalTargets := len(targets)
	onlineTargets := 0
	for _, t := range targets {
		if t.Enabled {
			onlineTargets++
		}
	}

	projects, _ := s.cfg.Store.ListProjects(r.Context(), "")
	totalProjects := len(projects)

	clients, _ := s.cfg.Store.ListClients(r.Context())
	totalClients := len(clients)

	totalRequests, _ := s.cfg.Store.GetActivityCount(r.Context())
	recentActivity, _ := s.cfg.Store.ListActivity(r.Context(), 8, 0, registry.ActivityFilter{})
	deniedCount := 0
	for _, a := range recentActivity {
		if !a.Success {
			deniedCount++
		}
	}

	uptimeSec := getUptimeSec()

	// MCP Adapter online check
	mcpOnline := false
	conn, err := net.DialTimeout("tcp", "127.0.0.1:8090", 150*time.Millisecond)
	if err == nil {
		mcpOnline = true
		conn.Close()
	}

	s.render(w, r, "dashboard.html", pongo2.Context{
		"gateway_enabled": gwEnabled,
		"writes_enabled":  writesEnabled,
		"gateway_version": s.cfg.Version,
		"total_targets":   totalTargets,
		"online_targets":  onlineTargets,
		"total_projects":  totalProjects,
		"total_clients":   totalClients,
		"total_requests":  totalRequests,
		"denied_count":    deniedCount,
		"uptime_sec":      uptimeSec,
		"recent_activity": recentActivity,
		"mcp_online":      mcpOnline,
		"section":         "dashboard",
	})
}

// ---------------------------------------------------------------------------
// Targets Management
// ---------------------------------------------------------------------------

func (s *Server) handleTargetsList(w http.ResponseWriter, r *http.Request) {
	targets, _ := s.cfg.Store.ListTargets(r.Context())

	var viewTargets []map[string]interface{}
	for _, t := range targets {
		projects, _ := s.cfg.Store.ListProjects(r.Context(), t.ID)
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

	_ = r.ParseForm()
	port, _ := strconv.Atoi(r.FormValue("port"))
	if port <= 0 {
		port = 22
	}
	target := registry.Target{
		ID:              strings.TrimSpace(r.FormValue("id")),
		DisplayName:     strings.TrimSpace(r.FormValue("display_name")),
		Platform:        strings.TrimSpace(r.FormValue("platform")),
		Host:            strings.TrimSpace(r.FormValue("host")),
		Port:            port,
		User:            strings.TrimSpace(r.FormValue("user")),
		SSHAlias:        strings.TrimSpace(r.FormValue("ssh_alias")),
		PrivilegeUser:   strings.TrimSpace(r.FormValue("privilege_user")),
		PrivilegePolicy: strings.TrimSpace(r.FormValue("privilege_policy")),
		Enabled:         r.FormValue("enabled") == "on",
	}

	if target.ID == "" || target.Host == "" || target.User == "" {
		sess := getSession(r)
		sess.Flash("ID, host, and user are required fields.", "danger")
		s.render(w, r, "target_form.html", pongo2.Context{
			"is_edit": false,
			"target":  target,
			"section": "targets",
		})
		return
	}

	if target.PrivilegePolicy == "always_allow" {
		sess := getSession(r)
		sess.Flash("Create the Target with a safer privilege policy, then enable Always allow from Edit Target with the required second confirmation.", "danger")
		s.render(w, r, "target_form.html", pongo2.Context{
			"is_edit": false,
			"target":  target,
			"section": "targets",
		})
		return
	}
	if target.PrivilegePolicy == "" {
		target.PrivilegePolicy = "never"
	}

	if target.Enabled {
		s.RecordAudit(r, "add_target_attempt", target.ID, "", true, "", fmt.Sprintf("Add enabled target '%s'", target.ID), true)
	}

	err := s.cfg.Store.AddTarget(r.Context(), target)
	if err != nil {
		sess := getSession(r)
		sess.Flash(fmt.Sprintf("Error creating target: %v", err), "danger")
		s.render(w, r, "target_form.html", pongo2.Context{
			"is_edit": false,
			"target":  target,
			"section": "targets",
		})
		return
	}

	s.RecordAudit(r, "add_target", target.ID, "", true, "", fmt.Sprintf("Added target '%s'", target.ID), false)
	sess := getSession(r)
	sess.Flash(fmt.Sprintf("Target '%s' created successfully.", target.ID), "success")
	http.Redirect(w, r, "/targets", http.StatusFound)
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

func (s *Server) handleTargetEdit(w http.ResponseWriter, r *http.Request, targetID string) {
	target, err := s.cfg.Store.GetTarget(r.Context(), targetID, true)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if r.Method == http.MethodGet {
		sshIdent, _ := s.cfg.Discovery.InspectTargetIdentity(discovery.TargetConfig{
			ID:       target.ID,
			Host:     target.Host,
			Port:     target.Port,
			SSHAlias: target.SSHAlias,
			User:     target.User,
			Platform: target.Platform,
			Enabled:  target.Enabled,
		})

		approval, _ := s.cfg.Store.GetPrivilegeApproval(r.Context(), targetID)
		privScopes := s.targetPrivilegeScopes(r.Context(), targetID)

		s.render(w, r, "target_form.html", pongo2.Context{
			"target":  target,
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
			"privilege_status": map[string]interface{}{
				"policy":   target.PrivilegePolicy,
				"approval": approval,
			},
			"privilege_scopes": privScopes,
			"section":          "targets",
		})
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	_ = r.ParseForm()
	port, _ := strconv.Atoi(r.FormValue("port"))
	if port <= 0 {
		port = 22
	}
	newPolicy := strings.TrimSpace(r.FormValue("privilege_policy"))
	if newPolicy == "" {
		newPolicy = "never"
	}

	if target.PrivilegePolicy != "always_allow" && newPolicy == "always_allow" {
		sess := getSession(r)
		if sess.Pending == nil {
			sess.Pending = make(map[string]interface{})
		}
		sess.Pending["always_allow_target"] = targetID
		sess.Flash("Always allow requires a separate risk confirmation and Admin password re-authentication. Other Target edits were not saved; save them separately first if needed.", "warning")
		http.Redirect(w, r, fmt.Sprintf("/targets/%s/privileges/always-allow", targetID), http.StatusFound)
		return
	}

	updated := registry.Target{
		ID:              targetID,
		DisplayName:     strings.TrimSpace(r.FormValue("display_name")),
		Platform:        strings.TrimSpace(r.FormValue("platform")),
		Host:            strings.TrimSpace(r.FormValue("host")),
		Port:            port,
		User:            strings.TrimSpace(r.FormValue("user")),
		SSHAlias:        strings.TrimSpace(r.FormValue("ssh_alias")),
		PrivilegeUser:   strings.TrimSpace(r.FormValue("privilege_user")),
		PrivilegePolicy: newPolicy,
		Enabled:         r.FormValue("enabled") == "on",
	}

	if updated.Enabled {
		s.RecordAudit(r, "update_target_attempt", targetID, "", true, "", fmt.Sprintf("Update active Target security context for '%s'", targetID), true)
	}

	err = s.cfg.Store.UpdateTarget(r.Context(), updated)
	if err != nil {
		sess := getSession(r)
		sess.Flash(fmt.Sprintf("Error updating target: %v", err), "danger")
		http.Redirect(w, r, fmt.Sprintf("/targets/%s/edit", targetID), http.StatusFound)
		return
	}

	s.RecordAudit(r, "update_target", targetID, "", true, "", fmt.Sprintf("Updated target '%s'", targetID), false)
	sess := getSession(r)
	sess.Flash(fmt.Sprintf("Target '%s' updated successfully.", targetID), "success")
	http.Redirect(w, r, "/targets", http.StatusFound)
}

func (s *Server) targetPrivilegeScopes(ctx context.Context, targetID string) []map[string]interface{} {
	clients, _ := s.cfg.Store.ListClients(ctx)
	projects, _ := s.cfg.Store.ListProjects(ctx, targetID)

	var scopes []map[string]interface{}
	for _, c := range clients {
		for _, p := range projects {
			res, _ := policy.AuthorizeClient(ctx, s.cfg.Store, c.ID, targetID, p.ID, "run_command", false)
			if res.Allowed {
				jsonVal, _ := json.Marshal([]string{c.ID, p.ID})
				scopes = append(scopes, map[string]interface{}{
					"client_id":  c.ID,
					"project_id": p.ID,
					"value":      string(jsonVal),
				})
			}
		}
	}
	return scopes
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
	_ = s.cfg.Store.UpdateTarget(r.Context(), target)

	action := "enabled"
	if !target.Enabled {
		action = "disabled"
	}
	s.RecordAudit(r, "toggle_target", targetID, "", true, "", fmt.Sprintf("Target '%s' %s", targetID, action), false)
	sess := getSession(r)
	sess.Flash(fmt.Sprintf("Target '%s' %s.", targetID, action), "success")
	http.Redirect(w, r, "/targets", http.StatusFound)
}

func (s *Server) handleTargetTest(w http.ResponseWriter, r *http.Request, targetID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sess := getSession(r)
	res := s.cfg.Core.Invoke(ctx, core.Invocation{ClientID: sess.Username}, "target_status", map[string]interface{}{
		"target": targetID,
	})

	returnTo := r.FormValue("return_to")
	dest := "/targets"
	if returnTo == "edit" {
		dest = fmt.Sprintf("/targets/%s/edit", targetID)
	}

	if ok, errMsg := invokeStatus(res); ok {
		sess.Flash(fmt.Sprintf("Connection test passed for Target '%s'.", targetID), "success")
	} else {
		sess.Flash(fmt.Sprintf("Connection test failed for Target '%s': %s", targetID, errMsg), "danger")
	}

	http.Redirect(w, r, dest, http.StatusFound)
}

func (s *Server) handleTargetSSHTrust(w http.ResponseWriter, r *http.Request, targetID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	_ = r.ParseForm()
	fingerprint := strings.TrimSpace(r.FormValue("fingerprint"))
	replace := r.FormValue("replace") == "1"

	target, err := s.cfg.Store.GetTarget(r.Context(), targetID, true)
	sess := getSession(r)
	if err != nil {
		sess.Flash("Target not found", "danger")
		http.Redirect(w, r, "/targets", http.StatusFound)
		return
	}

	_, err = s.cfg.Discovery.TrustPresentedKey(discovery.TargetConfig{
		ID:       target.ID,
		Host:     target.Host,
		Port:     target.Port,
		SSHAlias: target.SSHAlias,
	}, fingerprint, replace)

	if err != nil {
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

	_, err = s.cfg.Discovery.RemoveTrustedKey(discovery.TargetConfig{
		ID:       target.ID,
		SSHAlias: target.SSHAlias,
	})
	if err != nil {
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
	_ = r.ParseForm()
	scopeVal := r.FormValue("scope")
	var pair []string
	if err := json.Unmarshal([]byte(scopeVal), &pair); err != nil || len(pair) != 2 {
		sess := getSession(r)
		sess.Flash("Invalid approval scope format", "danger")
		http.Redirect(w, r, fmt.Sprintf("/targets/%s/edit", targetID), http.StatusFound)
		return
	}

	clientID := pair[0]
	projectID := pair[1]

	_ = s.cfg.Store.SetPrivilegeApproval(r.Context(), targetID, "ask_always", clientID, projectID, "")
	s.RecordAudit(r, "approve_target_privilege", targetID, projectID, true, "", fmt.Sprintf("Approved privilege lease for client %s", clientID), false)

	sess := getSession(r)
	sess.Flash(fmt.Sprintf("Privilege approval granted for %s on project %s (5m TTL).", clientID, projectID), "success")
	http.Redirect(w, r, fmt.Sprintf("/targets/%s/edit", targetID), http.StatusFound)
}

func (s *Server) handleTargetPrivilegeRevoke(w http.ResponseWriter, r *http.Request, targetID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	_ = s.cfg.Store.ClearPrivilegeApproval(r.Context(), targetID)
	s.RecordAudit(r, "revoke_target_privilege", targetID, "", true, "", "Revoked cached privilege approval", false)
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

	_ = r.ParseForm()
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
	_ = s.cfg.Store.UpdateTarget(r.Context(), target)

	s.RecordAudit(r, "target_privilege_policy_change", targetID, "", true, "", "Enabled always_allow with re-authentication", false)
	sess.Flash(fmt.Sprintf("Privilege policy set to always_allow for Target '%s'.", targetID), "warning")
	http.Redirect(w, r, fmt.Sprintf("/targets/%s/edit", targetID), http.StatusFound)
}

// ---------------------------------------------------------------------------
// Projects Management
// ---------------------------------------------------------------------------

func (s *Server) handleProjectsList(w http.ResponseWriter, r *http.Request) {
	targetFilter := r.URL.Query().Get("target")
	projects, _ := s.cfg.Store.ListProjects(r.Context(), targetFilter)

	s.render(w, r, "projects.html", pongo2.Context{
		"projects": projects,
		"section":  "projects",
	})
}

func (s *Server) handleProjectAdd(w http.ResponseWriter, r *http.Request) {
	targets, _ := s.cfg.Store.ListTargets(r.Context())

	if r.Method == http.MethodGet {
		s.render(w, r, "project_form.html", pongo2.Context{
			"is_edit": false,
			"project": nil,
			"targets": targets,
			"section": "projects",
		})
		return
	}

	_ = r.ParseForm()
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
		s.render(w, r, "project_form.html", pongo2.Context{
			"is_edit": false,
			"project": project,
			"targets": targets,
			"section": "projects",
		})
		return
	}

	err := s.cfg.Store.AddProject(r.Context(), project)
	if err != nil {
		sess.Flash(fmt.Sprintf("Error creating project: %v", err), "danger")
		s.render(w, r, "project_form.html", pongo2.Context{
			"is_edit": false,
			"project": project,
			"targets": targets,
			"section": "projects",
		})
		return
	}

	s.RecordAudit(r, "add_project", project.TargetID, project.ID, true, "", fmt.Sprintf("Added project '%s'", project.ID), false)
	sess.Flash(fmt.Sprintf("Project '%s' created successfully.", project.ID), "success")
	http.Redirect(w, r, "/projects", http.StatusFound)
}

func (s *Server) handleProjectSubroutes(w http.ResponseWriter, r *http.Request) {
	relPath := strings.TrimPrefix(r.URL.Path, "/projects/")
	parts := strings.Split(relPath, "/")
	if len(parts) < 3 {
		http.Redirect(w, r, "/projects", http.StatusFound)
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
	if action == "edit" {
		if r.Method == http.MethodGet {
			targets, _ := s.cfg.Store.ListTargets(r.Context())
			s.render(w, r, "project_form.html", pongo2.Context{
				"project": project,
				"is_edit": true,
				"targets": targets,
				"section": "projects",
			})
			return
		}
		_ = r.ParseForm()
		project.DisplayName = strings.TrimSpace(r.FormValue("display_name"))
		project.Root = strings.TrimSpace(r.FormValue("root"))
		project.Read = r.FormValue("read") == "on"
		project.Write = r.FormValue("write") == "on"
		project.Enabled = r.FormValue("enabled") == "on"

		_ = s.cfg.Store.UpdateProject(r.Context(), project)
		s.RecordAudit(r, "update_project", targetID, projectID, true, "", fmt.Sprintf("Updated project '%s'", projectID), false)
		sess.Flash(fmt.Sprintf("Project '%s' updated.", projectID), "success")
		http.Redirect(w, r, "/projects", http.StatusFound)
		return
	}

	if action == "toggle" && r.Method == http.MethodPost {
		project.Enabled = !project.Enabled
		_ = s.cfg.Store.UpdateProject(r.Context(), project)
		s.RecordAudit(r, "toggle_project", targetID, projectID, true, "", fmt.Sprintf("Project enabled=%v", project.Enabled), false)
		sess.Flash(fmt.Sprintf("Project '%s' enabled=%v.", projectID, project.Enabled), "success")
		http.Redirect(w, r, "/projects", http.StatusFound)
		return
	}

	if action == "toggle-write" && r.Method == http.MethodPost {
		project.Write = !project.Write
		_ = s.cfg.Store.UpdateProject(r.Context(), project)
		s.RecordAudit(r, "toggle_project_write", targetID, projectID, true, "", fmt.Sprintf("Project write=%v", project.Write), false)
		sess.Flash(fmt.Sprintf("Project '%s' write capability set to %v.", projectID, project.Write), "success")
		http.Redirect(w, r, "/projects", http.StatusFound)
		return
	}

	http.NotFound(w, r)
}

// ---------------------------------------------------------------------------
// Clients Management
// ---------------------------------------------------------------------------

func (s *Server) handleClientsList(w http.ResponseWriter, r *http.Request) {
	clients, _ := s.cfg.Store.ListClients(r.Context())

	var viewClients []map[string]interface{}
	for _, c := range clients {
		grants, _ := s.cfg.Store.ListGrants(r.Context(), c.ID)
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
			cap := g.Capability
			if cap == "*" || strings.Contains(cap, "write") {
				summary["structured_write"] = true
			}
			if cap == "*" || strings.Contains(cap, "tasks") {
				summary["tasks"] = true
			}
			if cap == "*" || strings.Contains(cap, "target_shell") {
				summary["target_shell"] = true
			}
			if strings.Contains(cap, "target_admin") {
				summary["target_admin"] = true
			}
			if cap == "*" || strings.Contains(cap, "gateway_admin") {
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

	_ = r.ParseForm()
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
		s.render(w, r, "client_form.html", pongo2.Context{
			"is_edit": false,
			"client":  client,
			"section": "clients",
		})
		return
	}

	err := s.cfg.Store.AddClient(r.Context(), client)
	if err != nil {
		sess.Flash(fmt.Sprintf("Error creating client: %v", err), "danger")
		s.render(w, r, "client_form.html", pongo2.Context{
			"is_edit": false,
			"client":  client,
			"section": "clients",
		})
		return
	}

	s.RecordAudit(r, "add_client", "", "", true, "", fmt.Sprintf("Added AI client '%s'", client.ID), false)
	sess.Flash(fmt.Sprintf("Client '%s' registered.", client.ID), "success")
	http.Redirect(w, r, "/clients", http.StatusFound)
}

func (s *Server) handleClientSubroutes(w http.ResponseWriter, r *http.Request) {
	relPath := strings.TrimPrefix(r.URL.Path, "/clients/")
	parts := strings.Split(relPath, "/")
	if len(parts) == 0 || parts[0] == "" {
		http.Redirect(w, r, "/clients", http.StatusFound)
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
		_ = r.ParseForm()
		client.DisplayName = strings.TrimSpace(r.FormValue("display_name"))
		client.Provider = strings.TrimSpace(r.FormValue("provider"))
		client.Protocol = strings.TrimSpace(r.FormValue("protocol"))
		client.Notes = strings.TrimSpace(r.FormValue("notes"))
		client.Enabled = r.FormValue("enabled") == "on"

		_ = s.cfg.Store.UpdateClient(r.Context(), client)
		s.RecordAudit(r, "update_client", "", "", true, "", fmt.Sprintf("Updated client '%s'", clientID), false)
		sess.Flash(fmt.Sprintf("Client '%s' updated.", clientID), "success")
		http.Redirect(w, r, "/clients", http.StatusFound)
		return
	}

	if len(parts) == 2 && parts[1] == "toggle" && r.Method == http.MethodPost {
		client.Enabled = !client.Enabled
		_ = s.cfg.Store.UpdateClient(r.Context(), client)
		s.RecordAudit(r, "toggle_client", "", "", true, "", fmt.Sprintf("Client enabled=%v", client.Enabled), false)
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

func (s *Server) handleClientGrants(w http.ResponseWriter, r *http.Request, client registry.Client, sub []string) {
	sess := getSession(r)

	// POST /clients/{id}/grants/add
	if len(sub) == 1 && sub[0] == "add" && r.Method == http.MethodPost {
		_ = r.ParseForm()
		targetID := strings.TrimSpace(r.FormValue("target_id"))
		projectID := strings.TrimSpace(r.FormValue("project_id"))
		cap := strings.TrimSpace(r.FormValue("capability"))
		if cap == "" {
			cap = "*"
		}

		grant := registry.Grant{
			ClientID:   client.ID,
			TargetID:   targetID,
			ProjectID:  projectID,
			Capability: cap,
			Enabled:    true,
		}
		_, _ = s.cfg.Store.AddGrant(r.Context(), grant)
		s.RecordAudit(r, "add_grant", targetID, projectID, true, "", fmt.Sprintf("Added grant '%s' for client '%s'", cap, client.ID), false)
		sess.Flash("Grant added successfully.", "success")
		http.Redirect(w, r, fmt.Sprintf("/clients/%s/grants", client.ID), http.StatusFound)
		return
	}

	// POST /clients/{id}/grants/check
	if len(sub) == 1 && sub[0] == "check" && r.Method == http.MethodPost {
		_ = r.ParseForm()
		targetID := strings.TrimSpace(r.FormValue("target_id"))
		projectID := strings.TrimSpace(r.FormValue("project_id"))
		toolName := strings.TrimSpace(r.FormValue("tool_name"))

		res, _ := policy.AuthorizeClient(r.Context(), s.cfg.Store, client.ID, targetID, projectID, toolName, false)

		grants, _ := s.cfg.Store.ListGrants(r.Context(), client.ID)
		targets, _ := s.cfg.Store.ListTargets(r.Context())
		projects, _ := s.cfg.Store.ListProjects(r.Context(), "")

		s.render(w, r, "client_grants.html", pongo2.Context{
			"client":        client,
			"grants":        grants,
			"targets":       targets,
			"projects":      projects,
			"tool_names":    core.CatalogTools(),
			"access_result": res,
			"section":       "clients",
		})
		return
	}

	// Actions on specific grant ID: /clients/{id}/grants/{grant_id}/[toggle|delete]
	if len(sub) == 2 && r.Method == http.MethodPost {
		grantID, _ := strconv.ParseInt(sub[0], 10, 64)
		action := sub[1]

		if action == "toggle" {
			grant, err := s.cfg.Store.GetGrant(r.Context(), grantID)
			if err == nil {
				grant.Enabled = !grant.Enabled
				_ = s.cfg.Store.UpdateGrant(r.Context(), grant)
				s.RecordAudit(r, "toggle_grant", grant.TargetID, grant.ProjectID, true, "", fmt.Sprintf("Grant %d enabled=%v", grantID, grant.Enabled), false)
				sess.Flash("Grant status updated.", "success")
			}
			http.Redirect(w, r, fmt.Sprintf("/clients/%s/grants", client.ID), http.StatusFound)
			return
		}

		if action == "delete" {
			_ = s.cfg.Store.DeleteGrant(r.Context(), grantID)
			s.RecordAudit(r, "delete_grant", "", "", true, "", fmt.Sprintf("Deleted grant %d for client %s", grantID, client.ID), false)
			sess.Flash("Grant deleted.", "info")
			http.Redirect(w, r, fmt.Sprintf("/clients/%s/grants", client.ID), http.StatusFound)
			return
		}
	}

	// Default GET /clients/{id}/grants
	grants, _ := s.cfg.Store.ListGrants(r.Context(), client.ID)
	targets, _ := s.cfg.Store.ListTargets(r.Context())
	projects, _ := s.cfg.Store.ListProjects(r.Context(), "")

	var editingGrant *registry.Grant
	if editParam := r.URL.Query().Get("edit"); editParam != "" {
		gID, _ := strconv.ParseInt(editParam, 10, 64)
		g, err := s.cfg.Store.GetGrant(r.Context(), gID)
		if err == nil {
			editingGrant = &g
		}
	}

	s.render(w, r, "client_grants.html", pongo2.Context{
		"client":        client,
		"grants":        grants,
		"targets":       targets,
		"projects":      projects,
		"tool_names":    core.CatalogTools(),
		"editing_grant": editingGrant,
		"section":       "clients",
	})
}

// ---------------------------------------------------------------------------
// Activity Audit
// ---------------------------------------------------------------------------

func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit := 25
	offset := (page - 1) * limit

	filter := registry.ActivityFilter{
		Actor:     r.URL.Query().Get("actor"),
		Action:    r.URL.Query().Get("action"),
		TargetID:  r.URL.Query().Get("target_id"),
		ProjectID: r.URL.Query().Get("project_id"),
		Result:    r.URL.Query().Get("result"),
		From:      r.URL.Query().Get("from"),
		To:        r.URL.Query().Get("to"),
	}

	total, _ := s.cfg.Store.GetActivityCountFiltered(r.Context(), filter)
	totalPages := int(math.Ceil(float64(total) / float64(limit)))
	if totalPages < 1 {
		totalPages = 1
	}

	items, _ := s.cfg.Store.ListActivity(r.Context(), limit, offset, filter)

	buildURL := func(p int) string {
		q := r.URL.Query()
		q.Set("page", strconv.Itoa(p))
		return "/activity?" + q.Encode()
	}

	prevURL := ""
	if page > 1 {
		prevURL = buildURL(page - 1)
	}
	nextURL := ""
	if page < totalPages {
		nextURL = buildURL(page + 1)
	}

	s.render(w, r, "activity.html", pongo2.Context{
		"items":       items,
		"page":        page,
		"total_pages": totalPages,
		"total":       total,
		"prev_url":    prevURL,
		"next_url":    nextURL,
		"live":        r.URL.Query().Get("live") == "1",
		"filters":     r.URL.Query(),
		"section":     "activity",
	})
}

// ---------------------------------------------------------------------------
// Settings & Kill Switch
// ---------------------------------------------------------------------------

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		_ = r.ParseForm()
		timeout := r.FormValue("admin_session_timeout")
		if timeout != "" {
			_ = s.cfg.Store.SetSetting(r.Context(), "admin_session_timeout", timeout)
			s.RecordAudit(r, "update_settings", "", "", true, "", fmt.Sprintf("Updated admin_session_timeout to %s", timeout), false)
		}
		sess := getSession(r)
		sess.Flash("Settings updated successfully.", "success")
		http.Redirect(w, r, "/settings", http.StatusFound)
		return
	}

	gwEnabled, _ := s.cfg.Store.GetSetting(r.Context(), "gateway_enabled", "true")
	writesEnabled, _ := s.cfg.Store.GetSetting(r.Context(), "writes_enabled", "false")
	shellEnabled, _ := s.cfg.Store.GetSetting(r.Context(), "shell_enabled", "false")
	timeout, _ := s.cfg.Store.GetSetting(r.Context(), "admin_session_timeout", "1800")

	settings := map[string]interface{}{
		"gateway_enabled":       gwEnabled == "true",
		"writes_enabled":        writesEnabled == "true",
		"shell_enabled":         shellEnabled == "true",
		"admin_session_timeout": timeout,
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
	currStr, _ := s.cfg.Store.GetSetting(r.Context(), "gateway_enabled", "true")
	curr := currStr == "true"
	newVal := "true"
	if curr {
		newVal = "false"
	}
	s.RecordAudit(r, "kill_switch_toggle", "", "", true, "", fmt.Sprintf("gateway_enabled set to %s", newVal), true)
	_ = s.cfg.Store.SetSetting(r.Context(), "gateway_enabled", newVal)

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
	currStr, _ := s.cfg.Store.GetSetting(r.Context(), "writes_enabled", "false")
	curr := currStr == "true"
	newVal := "true"
	if curr {
		newVal = "false"
	}
	s.RecordAudit(r, "toggle_writes", "", "", true, "", fmt.Sprintf("writes_enabled set to %s", newVal), true)
	_ = s.cfg.Store.SetSetting(r.Context(), "writes_enabled", newVal)

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
	_ = s.cfg.Store.SetSetting(r.Context(), "writes_enabled", "false")
	s.RecordAudit(r, "disable_writes", "", "", true, "", "writes_enabled set to false", true)

	sess := getSession(r)
	sess.Flash("Filesystem writes disabled.", "info")
	http.Redirect(w, r, "/settings", http.StatusFound)
}

func (s *Server) handleSettingsToggleShell(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	currStr, _ := s.cfg.Store.GetSetting(r.Context(), "shell_enabled", "false")
	curr := currStr == "true"
	newVal := "true"
	if curr {
		newVal = "false"
	}
	s.RecordAudit(r, "toggle_shell", "", "", true, "", fmt.Sprintf("shell_enabled set to %s", newVal), true)
	_ = s.cfg.Store.SetSetting(r.Context(), "shell_enabled", newVal)

	sess := getSession(r)
	if newVal == "true" {
		sess.Flash("High-risk trusted target shell enabled for authorized clients.", "warning")
	} else {
		sess.Flash("Target shell disabled globally.", "info")
	}
	http.Redirect(w, r, "/settings", http.StatusFound)
}

// ---------------------------------------------------------------------------
// Maintenance & Diagnostics
// ---------------------------------------------------------------------------

func (s *Server) handleMaintenance(w http.ResponseWriter, r *http.Request) {
	home, _ := os.UserHomeDir()
	backupDir := filepath.Join(home, ".local", "share", "mcp-gateway", "backups")
	entries, _ := os.ReadDir(backupDir)

	var backups []map[string]interface{}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".db") {
			info, err := e.Info()
			if err == nil {
				backups = append(backups, map[string]interface{}{
					"name":    e.Name(),
					"size_kb": info.Size() / 1024,
					"mtime":   info.ModTime().Format(time.RFC3339),
				})
			}
		}
	}

	compat := map[string]interface{}{
		"bridge_api_version":      1,
		"tool_catalog_version":    4,
		"registry_schema_version": 5,
		"mcp": map[string]interface{}{
			"protocol": "2026-07-28",
			"sdk":      "mcp-golang",
			"version":  "v1.4.0",
		},
	}

	s.render(w, r, "maintenance.html", pongo2.Context{
		"overall_status": "HEALTHY",
		"compat":         compat,
		"tool_count":     len(core.CatalogTools()),
		"backups":        backups,
		"section":        "maintenance",
	})
}

func (s *Server) handleMaintenanceDoctor(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	sess := getSession(r)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res := s.cfg.Core.Invoke(ctx, core.Invocation{ClientID: sess.Username}, "gateway_doctor", nil)
	if ok, errMsg := invokeStatus(res); ok {
		sess.Flash("Doctor diagnostics passed: PRAGMA integrity check OK.", "success")
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	res := s.cfg.Core.Invoke(ctx, core.Invocation{ClientID: sess.Username}, "gateway_backup", nil)
	if ok, errMsg := invokeStatus(res); ok {
		sess.Flash("Online VACUUM backup created successfully.", "success")
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
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	res := s.cfg.Core.Invoke(ctx, core.Invocation{ClientID: sess.Username}, "gateway_maintenance", map[string]interface{}{
		"action": "vacuum",
	})
	if ok, errMsg := invokeStatus(res); ok {
		sess.Flash("SQLite VACUUM and integrity repair completed.", "success")
	} else {
		sess.Flash(fmt.Sprintf("Maintenance action failed: %s", errMsg), "danger")
	}
	http.Redirect(w, r, "/maintenance", http.StatusFound)
}

func (s *Server) handleMaintenanceRollback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	sess := getSession(r)
	sess.Flash("Rollback requires maintainer runner: scripts/run-resumable.sh rollback", "warning")
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
