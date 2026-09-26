package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"mcp-gateway-adapter/internal/core"
	"mcp-gateway-adapter/internal/registry"
)

func setupTestAdminServer(t *testing.T) (*Server, *registry.Store) {
	t.Helper()
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "registry.db")
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
	_, _ = store.DB().ExecContext(ctx, `INSERT INTO ai_clients(id, display_name, enabled) VALUES ('test-client', 'Test AI Client', 1)`)

	coreInstance := core.New(store, core.Config{
		GatewayVersion:     "1.4.0",
		CoreAPIVersion:     1,
		ToolCatalogVersion: 4,
		MCPProtocol:        "2026-07-28",
		DBPath:             dbPath,
		BackupDir:          filepath.Join(tempDir, "backups"),
	})

	srv, err := NewServer(ServerConfig{
		Host:         "127.0.0.1",
		Port:         8080,
		AllowedHosts: []string{"127.0.0.1", "localhost", "testserver"},
		SecretDir:    tempDir,
		Store:        store,
		Core:         coreInstance,
		Version:      "1.4.0",
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
		{"/targets", "Targets"},
		{"/targets/test-target/edit", "Test Target"},
		{"/projects", "Projects"},
		{"/clients", "Clients"},
		{"/clients/test-client/grants", "test-client"},
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
		if !strings.Contains(rec.Body.String(), p.content) {
			t.Errorf("GET %s body missing '%s'", p.url, p.content)
		}
	}
}
