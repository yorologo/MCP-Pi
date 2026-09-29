package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"mcp-gateway-adapter/internal/buildinfo"
	"mcp-gateway-adapter/internal/core"
	"mcp-gateway-adapter/internal/registry"
)

func setupTestAdminServer(t *testing.T) (*Server, *registry.Store) {
	t.Helper()
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "registry.db")
	if err := registry.MigratePath(ctx, dbPath); err != nil {
		t.Fatalf("failed to initialize registry: %v", err)
	}
	store, err := registry.OpenStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to open registry: %v", err)
	}

	hash, err := GeneratePasswordHash("testpass")
	if err != nil {
		t.Fatalf("failed to hash password: %v", err)
	}
	_, err = store.DB().ExecContext(ctx, `INSERT INTO admin_users(username, password_hash, enabled) VALUES ('admin', ?, 1)`, hash)
	if err != nil {
		t.Fatalf("failed to insert admin user: %v", err)
	}

	_, _ = store.DB().ExecContext(ctx, `INSERT INTO targets(id, display_name, platform, host, port, user, privilege_policy, enabled) VALUES ('test-target', 'Test Target', 'linux', '127.0.0.1', 22, 'testuser', 'never', 1)`)
	_, _ = store.DB().ExecContext(ctx, `INSERT INTO projects(id, target_id, display_name, root, read_enabled, write_enabled, enabled) VALUES ('test-proj', 'test-target', 'Test Project', '/srv/test', 1, 0, 1)`)
	_, _ = store.DB().ExecContext(ctx, `INSERT INTO ai_clients(id, display_name, provider, protocol, enabled) VALUES ('test-client', 'Test AI Client', 'openai', 'mcp', 1)`)
	_, _ = store.DB().ExecContext(ctx, `INSERT INTO grants(client_id, target_id, project_id, capability, enabled) VALUES ('test-client', 'test-target', 'test-proj', 'read', 1)`)

	coreInstance := core.New(store, core.Config{
		GatewayVersion:     buildinfo.GatewayVersion,
		CoreAPIVersion:     buildinfo.CoreAPIVersion,
		ToolCatalogVersion: buildinfo.ToolCatalogVersion,
		MCPProtocol:        buildinfo.MCPProtocol,
		DBPath:             dbPath,
		BackupDir:          filepath.Join(tempDir, "backups"),
	})

	srv, err := NewServer(ServerConfig{
		Host:         "127.0.0.1",
		Port:         8080,
		AllowedHosts: []string{"127.0.0.1", "localhost", "testserver"},
		SecretFile:   filepath.Join(tempDir, "admin-secret"),
		Store:        store,
		Core:         coreInstance,
		Version:      buildinfo.GatewayVersion,
	})
	if err != nil {
		t.Fatalf("failed to create admin server: %v", err)
	}

	return srv, store
}

func extractCookie(res *http.Response, name string) *http.Cookie {
	for _, c := range res.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func extractCSRFToken(body string) string {
	idx := strings.Index(body, `name="csrf_token" value="`)
	if idx == -1 {
		return ""
	}
	sub := body[idx+len(`name="csrf_token" value="`):]
	end := strings.Index(sub, `"`)
	if end == -1 {
		return ""
	}
	return sub[:end]
}

func adminPostForm(target string, values url.Values) *http.Request {
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(values.Encode()))
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx := context.WithValue(req.Context(), sessionContextKey, &SessionData{
		Username:  "admin",
		CSRFToken: "test-csrf",
	})
	return req.WithContext(ctx)
}

func TestHostHeaderSecurity(t *testing.T) {
	srv, _ := setupTestAdminServer(t)
	handler := srv.Handler()

	// 1. Untrusted host should be rejected with 403 Forbidden
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	req.Host = "evil.attacker.com"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for untrusted Host header, got %d", rec.Code)
	}

	// 2. Allowed host should succeed
	req = httptest.NewRequest(http.MethodGet, "/login", nil)
	req.Host = "127.0.0.1:8080"
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for allowed Host header, got %d", rec.Code)
	}
}

func TestSecurityHeadersPresent(t *testing.T) {
	srv, _ := setupTestAdminServer(t)
	handler := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	req.Host = "127.0.0.1:8080"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	headers := []string{
		"Content-Security-Policy",
		"X-Content-Type-Options",
		"X-Frame-Options",
		"Referrer-Policy",
		"Permissions-Policy",
		"Cache-Control",
	}

	for _, h := range headers {
		if rec.Header().Get(h) == "" {
			t.Errorf("missing expected security header %s", h)
		}
	}

	if rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Errorf("expected X-Frame-Options: DENY, got %s", rec.Header().Get("X-Frame-Options"))
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("expected X-Content-Type-Options: nosniff, got %s", rec.Header().Get("X-Content-Type-Options"))
	}
}

func TestUnauthenticatedAccessRedirects(t *testing.T) {
	srv, _ := setupTestAdminServer(t)
	handler := srv.Handler()

	protectedURLs := []string{"/dashboard", "/targets", "/projects", "/clients", "/activity", "/settings", "/maintenance"}
	for _, u := range protectedURLs {
		req := httptest.NewRequest(http.MethodGet, u, nil)
		req.Host = "127.0.0.1:8080"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusFound {
			t.Errorf("expected redirect 302 for %s, got %d", u, rec.Code)
		}
		loc := rec.Header().Get("Location")
		if !strings.HasPrefix(loc, "/login") {
			t.Errorf("expected redirect to /login for %s, got %s", u, loc)
		}
	}
}

func TestLoginAndSessionLifecycle(t *testing.T) {
	srv, _ := setupTestAdminServer(t)
	handler := srv.Handler()

	// 1. GET /login to get initial CSRF token and cookie
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	req.Host = "127.0.0.1:8080"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /login returned %d", rec.Code)
	}

	cookie := extractCookie(rec.Result(), "mcp_admin_session")
	csrfToken := extractCSRFToken(rec.Body.String())
	if csrfToken == "" {
		t.Fatal("failed to extract CSRF token from login page")
	}

	// 2. Failed login attempt (wrong password)
	form := url.Values{
		"username":   {"admin"},
		"password":   {"wrongpassword"},
		"csrf_token": {csrfToken},
	}
	req = httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("failed login attempt expected status 200 re-render, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Invalid username or password") {
		t.Error("expected error message in body for failed login")
	}

	// 3. Successful login attempt
	form.Set("password", "testpass")
	req = httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("successful login expected 302 redirect, got %d", rec.Code)
	}
	if rec.Header().Get("Location") != "/dashboard" {
		t.Fatalf("expected redirect to /dashboard, got %s", rec.Header().Get("Location"))
	}

	authCookie := extractCookie(rec.Result(), "mcp_admin_session")
	if authCookie == nil {
		t.Fatal("expected session cookie to be set on successful login")
	}

	// 4. Access authenticated dashboard
	req = httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.Host = "127.0.0.1:8080"
	req.AddCookie(authCookie)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /dashboard with auth cookie failed with status %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Dashboard") {
		t.Error("dashboard page content missing 'Dashboard'")
	}
	if !strings.Contains(body, "admin") {
		t.Error("dashboard does not display logged in user")
	}

	// 5. Test CSRF rejection on mutating endpoint without token
	toggleForm := url.Values{}
	req = httptest.NewRequest(http.MethodPost, "/settings/toggle-writes", strings.NewReader(toggleForm.Encode()))
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(authCookie)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for POST without CSRF token, got %d", rec.Code)
	}

	// 6. Test valid mutating request with CSRF token
	dashCSRF := extractCSRFToken(body)
	if dashCSRF == "" {
		t.Fatal("could not extract CSRF token from dashboard")
	}
	toggleForm.Set("csrf_token", dashCSRF)

	req = httptest.NewRequest(http.MethodPost, "/settings/toggle-writes", strings.NewReader(toggleForm.Encode()))
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(authCookie)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302 Found for valid CSRF settings toggle, got %d", rec.Code)
	}
}

func TestAdminPagesRender(t *testing.T) {
	srv, _ := setupTestAdminServer(t)
	handler := srv.Handler()

	// Perform login to get auth cookie
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	req.Host = "127.0.0.1:8080"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	cookie := extractCookie(rec.Result(), "mcp_admin_session")
	csrfToken := extractCSRFToken(rec.Body.String())

	form := url.Values{
		"username":   {"admin"},
		"password":   {"testpass"},
		"csrf_token": {csrfToken},
	}
	req = httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	authCookie := extractCookie(rec.Result(), "mcp_admin_session")

	pages := []struct {
		url     string
		content string
	}{
		{"/targets", "Test Target"},
		{"/targets/test-target/edit", "Test Target"},
		{"/projects", "Test Project"},
		{"/clients", "Test AI Client"},
		{"/clients/test-client/grants", "Test Project"},
		{"/activity", "Activity Audit Log"},
		{"/settings", "Settings"},
		{"/maintenance", "Maintenance"},
		{"/system", "System & Diagnostics"},
	}

	for _, p := range pages {
		req = httptest.NewRequest(http.MethodGet, p.url, nil)
		req.Host = "127.0.0.1:8080"
		req.AddCookie(authCookie)
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("GET %s returned code %d", p.url, rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, p.content) {
			t.Errorf("GET %s body missing '%s'", p.url, p.content)
		}
		if strings.Contains(body, ">True<") {
			t.Errorf("GET %s rendered boolean True as presentation text", p.url)
		}
	}
}

func TestActivityAttemptFilterAndBadge(t *testing.T) {
	srv, store := setupTestAdminServer(t)
	if _, err := store.DB().ExecContext(context.Background(), `
INSERT INTO activity(actor, action, success, detail)
VALUES ('admin', 'update_settings_attempt', 1, 'attempt-row')
`); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.handleActivity(rec, adminTestRequest(http.MethodGet, "/activity?result=attempt"))
	if rec.Code != http.StatusOK {
		t.Fatalf("attempt filter returned %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "update_settings_attempt") || !strings.Contains(body, ">ATTEMPT<") {
		t.Fatalf("attempt row was not rendered with neutral ATTEMPT result: %s", body)
	}
}

func TestDashboardUsesInProcessCoreHealthForReadiness(t *testing.T) {
	srv, _ := setupTestAdminServer(t)
	rec := httptest.NewRecorder()
	srv.handleDashboard(rec, adminTestRequest(http.MethodGet, "/dashboard"))
	if rec.Code != http.StatusOK {
		t.Fatalf("dashboard status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "MCP Adapter") || !strings.Contains(body, "READY") {
		t.Fatalf("dashboard did not report in-process Core readiness: %s", body)
	}
}

func TestTargetCreateRedirectsToSecuritySetup(t *testing.T) {
	srv, store := setupTestAdminServer(t)
	rec := httptest.NewRecorder()
	srv.handleTargetAdd(rec, adminPostForm("/targets/add", url.Values{
		"id":               {"new-target"},
		"display_name":     {"New Target"},
		"platform":         {"linux"},
		"host":             {"127.0.0.2"},
		"port":             {"22"},
		"user":             {"tester"},
		"privilege_policy": {"never"},
		"enabled":          {"on"},
	}))
	if rec.Code != http.StatusFound {
		t.Fatalf("target create status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/targets/new-target/edit" {
		t.Fatalf("target create redirect=%q want edit onboarding", got)
	}
	if _, err := store.GetTarget(context.Background(), "new-target", true); err != nil {
		t.Fatalf("created Target missing after redirect: %v", err)
	}
}

func TestInvalidFormsPreserveSubmittedValues(t *testing.T) {
	t.Run("target add", func(t *testing.T) {
		srv, _ := setupTestAdminServer(t)
		rec := httptest.NewRecorder()
		srv.handleTargetAdd(rec, adminPostForm("/targets/add", url.Values{
			"id": {"new-target"}, "display_name": {"Keep Target Name"}, "platform": {"linux"},
			"port": {"22"}, "host": {""}, "user": {"tester"}, "enabled": {"on"},
		}))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("target invalid form status=%d body=%s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.Contains(body, `value="new-target"`) || !strings.Contains(body, `value="Keep Target Name"`) {
			t.Fatalf("target submitted values were lost: %s", body)
		}
	})

	t.Run("project edit", func(t *testing.T) {
		srv, _ := setupTestAdminServer(t)
		rec := httptest.NewRecorder()
		srv.handleProjectSubroutes(rec, adminPostForm("/projects/test-target/test-proj/edit", url.Values{
			"display_name": {"Changed Project"}, "root": {""}, "read": {"on"}, "enabled": {"on"},
		}))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("project invalid form status=%d body=%s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `value="Changed Project"`) {
			t.Fatalf("project submitted values were lost: %s", rec.Body.String())
		}
	})

	t.Run("client edit", func(t *testing.T) {
		srv, _ := setupTestAdminServer(t)
		rec := httptest.NewRecorder()
		srv.handleClientSubroutes(rec, adminPostForm("/clients/test-client/edit", url.Values{
			"display_name": {""}, "provider": {"Keep Provider"}, "protocol": {"mcp"}, "notes": {"Keep Notes"}, "enabled": {"on"},
		}))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("client invalid form status=%d body=%s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.Contains(body, `value="Keep Provider"`) || !strings.Contains(body, "Keep Notes") {
			t.Fatalf("client submitted values were lost: %s", body)
		}
	})

	t.Run("settings", func(t *testing.T) {
		srv, _ := setupTestAdminServer(t)
		rec := httptest.NewRecorder()
		srv.handleSettings(rec, adminPostForm("/settings", url.Values{
			"default_timeout":     {"oops"},
			"max_output_bytes":    {"333333"},
			"max_file_read_bytes": {"444444"},
			"max_write_bytes":     {"555555"},
			"activity_retention":  {"4321"},
			"admin_timezone":      {"UTC"},
		}))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("settings invalid form status=%d body=%s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		for _, want := range []string{`value="oops"`, `value="333333"`, `value="4321"`} {
			if !strings.Contains(body, want) {
				t.Fatalf("settings submitted value %s was lost: %s", want, body)
			}
		}
	})
}

func TestGrantFormPreservesScopeAndFiltersProjectsByTarget(t *testing.T) {
	srv, store := setupTestAdminServer(t)
	client, err := store.GetClient(context.Background(), "test-client")
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.handleClientGrants(rec, adminPostForm("/clients/test-client/grants/add", url.Values{
		"target_id":  {"test-target"},
		"project_id": {"*"},
		"capability": {"target_admin"},
		"enabled":    {"on"},
	}), client, []string{"add"})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid grant status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`value="test-target" selected`,
		`value="*" selected`,
		`value="target_admin" selected`,
		`data-target-id="test-target"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("grant form did not preserve/filter context %q: %s", want, body)
		}
	}
}

func TestDuplicateGrantValidationPreservesFormAndReturns422(t *testing.T) {
	srv, store := setupTestAdminServer(t)
	client, err := store.GetClient(context.Background(), "test-client")
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.handleClientGrants(rec, adminPostForm("/clients/test-client/grants/add", url.Values{
		"target_id":  {"test-target"},
		"project_id": {"test-proj"},
		"capability": {"read"},
		"enabled":    {"on"},
	}), client, []string{"add"})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate grant status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "duplicate grant already exists") ||
		!strings.Contains(body, "value=\"read\" selected") {
		t.Fatalf("duplicate grant validation did not preserve useful context: %s", body)
	}
}

func TestEffectiveAccessPreservesSelections(t *testing.T) {
	srv, store := setupTestAdminServer(t)
	client, err := store.GetClient(context.Background(), "test-client")
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.handleClientGrants(rec, adminPostForm("/clients/test-client/grants/check", url.Values{
		"target_id":  {"test-target"},
		"project_id": {"test-proj"},
		"tool_name":  {"read_file"},
	}), client, []string{"check"})
	if rec.Code != http.StatusOK {
		t.Fatalf("effective access status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`value="test-target" selected`,
		`value="test-proj" data-target-id="test-target" selected`,
		`value="read_file" selected`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("effective access selection %q was not preserved: %s", want, body)
		}
	}
}

func TestEffectiveRunCommandAccessRequiresConcreteScopeAndPreservesPrivilege(t *testing.T) {
	srv, store := setupTestAdminServer(t)
	client, err := store.GetClient(context.Background(), "test-client")
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.handleClientGrants(rec, adminPostForm("/clients/test-client/grants/check", url.Values{
		"target_id":  {"*"},
		"project_id": {"*"},
		"tool_name":  {"run_command"},
		"privilege":  {"required"},
	}), client, []string{"check"})
	if rec.Code != http.StatusOK {
		t.Fatalf("effective run_command access status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"value=\"run_command\" selected",
		"value=\"required\" selected",
		"CONCRETE_SCOPE_REQUIRED",
		"DENIED",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("effective run_command result missing %q: %s", want, body)
		}
	}
}

func TestMalformedFormFailsClosedBeforeCSRF(t *testing.T) {
	srv, _ := setupTestAdminServer(t)
	handler := srv.Handler()

	get := httptest.NewRequest(http.MethodGet, "/login", nil)
	get.Host = "127.0.0.1:8080"
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, get)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET /login returned %d", getRec.Code)
	}
	cookie := extractCookie(getRec.Result(), "mcp_admin_session")
	csrfToken := extractCSRFToken(getRec.Body.String())
	if cookie == nil || csrfToken == "" {
		t.Fatal("failed to establish session/CSRF fixture")
	}

	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("username=admin&bad=%ZZ"))
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRF-Token", csrfToken)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed form with valid CSRF header returned %d, want 400", rec.Code)
	}
}
