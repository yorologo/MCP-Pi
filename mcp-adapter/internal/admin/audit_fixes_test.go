package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mcp-gateway-adapter/internal/registry"
)

func adminTestRequest(method, target string) *http.Request {
	return adminTestRequestAs(method, target, "admin")
}

func adminTestRequestAs(method, target, username string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	req.Host = "127.0.0.1:8080"
	ctx := context.WithValue(req.Context(), sessionContextKey, &SessionData{
		Username:  username,
		CSRFToken: "test-csrf",
	})
	return req.WithContext(ctx)
}

func TestTargetRediscoveryWithoutPinnedIdentityDoesNotMutateTarget(t *testing.T) {
	srv, store := setupTestAdminServer(t)
	before, err := store.GetTarget(context.Background(), "test-target", true)
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.handleTargetRediscover(rec, adminTestRequest(http.MethodPost, "/targets/test-target/rediscover"), "test-target")
	if rec.Code != http.StatusFound {
		t.Fatalf("rediscovery returned %d want 302: %s", rec.Code, rec.Body.String())
	}

	after, err := store.GetTarget(context.Background(), "test-target", true)
	if err != nil {
		t.Fatal(err)
	}
	if after.Host != before.Host || after.Port != before.Port {
		t.Fatalf("failed rediscovery mutated endpoint: before=%s:%d after=%s:%d", before.Host, before.Port, after.Host, after.Port)
	}
}

func TestGrantProjectScopeRenderingHandlesDuplicateProjectIDs(t *testing.T) {
	srv, store := setupTestAdminServer(t)
	ctx := context.Background()
	sqlText := "INSERT INTO targets(id, display_name, platform, host, port, user, privilege_policy, enabled) VALUES ('target-b', 'Target B', 'linux', '127.0.0.2', 22, 'user', 'never', 1);" +
		"INSERT INTO projects(id, target_id, display_name, root, read_enabled, write_enabled, enabled) VALUES ('test-proj', 'target-b', 'Duplicate Project', '/srv/other', 1, 0, 1);"
	if _, err := store.DB().ExecContext(ctx, sqlText); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	access := map[string]interface{}{
		"allowed":    true,
		"target_id":  "target-b",
		"project_id": "test-proj",
		"tool_name":  "read_file",
		"message":    "Allowed by current policy.",
	}
	if !srv.renderClientGrants(rec, adminTestRequest(http.MethodGet, "/clients/test-client/grants"), registry.Client{ID: "test-client"}, nil, access) {
		t.Fatal("renderClientGrants failed")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("render returned %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"data-project-select=\"grant-project\"",
		"data-project-select=\"check-project\"",
		"value=\"test-proj\" data-target-id=\"test-target\"",
		"value=\"test-proj\" data-target-id=\"target-b\" selected",
		"value=\"read_file\" selected",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("grants render missing %q", want)
		}
	}
}

func TestTemplateDataPreservesIntegerSemantics(t *testing.T) {
	type sample struct {
		Count   int   `json:"count"`
		GrantID int64 `json:"grant_id"`
	}
	got, ok := toTemplateData(sample{Count: 30, GrantID: 15}).(map[string]any)
	if !ok {
		t.Fatalf("unexpected converted type %T", toTemplateData(sample{}))
	}
	if _, ok := got["count"].(int64); !ok {
		t.Fatalf("count type=%T want int64", got["count"])
	}
	if _, ok := got["grant_id"].(int64); !ok {
		t.Fatalf("grant_id type=%T want int64", got["grant_id"])
	}
}

func TestActivityLiveURLsAndPolling(t *testing.T) {
	srv, _ := setupTestAdminServer(t)

	rec := httptest.NewRecorder()
	srv.handleActivity(rec, adminTestRequest(http.MethodGet, "/activity?actor=admin"))
	if rec.Code != http.StatusOK {
		t.Fatalf("activity returned %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `value="admin"`) {
		t.Fatalf("activity filter value was not rendered as a scalar: %s", body)
	}
	if !strings.Contains(body, "/activity?actor=admin&amp;live=1") {
		t.Fatalf("LIVE URL does not preserve filters and enable live mode: %s", body)
	}

	rec = httptest.NewRecorder()
	srv.handleActivity(rec, adminTestRequest(http.MethodGet, "/activity?actor=admin&live=1&page=2"))
	if rec.Code != http.StatusOK {
		t.Fatalf("live activity returned %d: %s", rec.Code, rec.Body.String())
	}
	body = rec.Body.String()
	for _, want := range []string{
		"Page 1 of",
		"Pause LIVE",
		`hx-trigger="every 3s"`,
		`hx-get="/activity?actor=admin&amp;live=1"`,
		`name="live" value="1"`,
		`href="/activity?actor=admin"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("live activity missing %q", want)
		}
	}
}

func TestActivityTimezoneAndLowercaseResultFilter(t *testing.T) {
	srv, store := setupTestAdminServer(t)
	ctx := context.Background()
	if err := store.SetSetting(ctx, "admin_timezone", "America/Mexico_City"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx, `
INSERT INTO activity(timestamp, actor, action, success, detail)
VALUES
('2026-09-27 04:00:00', 'admin', 'FILTER_PASS_MARKER', 1, 'pass-row'),
('2026-09-27 04:01:00', 'admin', 'FILTER_DENY_MARKER', 0, 'deny-row')
`); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.handleActivity(rec, adminTestRequest(http.MethodGet, "/activity?result=deny"))
	if rec.Code != http.StatusOK {
		t.Fatalf("activity returned %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "FILTER_DENY_MARKER") || strings.Contains(body, "FILTER_PASS_MARKER") {
		t.Fatalf("lowercase result filter was not applied correctly")
	}
	if !strings.Contains(body, "Timestamp (America/Mexico_City)") {
		t.Fatalf("configured time zone not rendered")
	}
	if !strings.Contains(body, "2026-09-26 22:01:00") {
		t.Fatalf("UTC timestamp was not localized: %s", body)
	}
}

func TestActivityRejectsInvalidFilters(t *testing.T) {
	srv, _ := setupTestAdminServer(t)

	for _, target := range []string{
		"/activity?result=maybe",
		"/activity?from=2026-09-27T05%3A00&to=2026-09-27T04%3A00",
	} {
		rec := httptest.NewRecorder()
		srv.handleActivity(rec, adminTestRequest(http.MethodGet, target))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s returned %d want 400: %s", target, rec.Code, rec.Body.String())
		}
	}
}

func TestTimezoneValidationFailsClosed(t *testing.T) {
	if _, err := validateAdminTimezone("Not/A_Real_Zone"); err == nil {
		t.Fatal("expected invalid IANA time zone to be rejected")
	}
	if _, err := validateAdminTimezone(""); err == nil {
		t.Fatal("expected empty time zone to be rejected")
	}
}

func TestMaintenanceUsesRealContractsAndNoWebRollback(t *testing.T) {
	srv, _ := setupTestAdminServer(t)
	rec := httptest.NewRecorder()
	srv.handleMaintenance(rec, adminTestRequest(http.MethodGet, "/maintenance"))
	if rec.Code != http.StatusOK {
		t.Fatalf("maintenance returned %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "mcp-golang") || strings.Contains(body, "/maintenance/rollback") {
		t.Fatalf("maintenance still exposes stale contract or web rollback")
	}
	for _, want := range []string{
		"Gateway Core Health",
		"go-sdk",
		"sudo /home/mcp-gateway/mcp-gateway/install.sh --rollback",
		"Run Safe Maintenance",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("maintenance missing %q", want)
		}
	}
}

func TestMaintenanceBackupUsesAuthenticatedAdminActor(t *testing.T) {
	srv, store := setupTestAdminServer(t)
	rec := httptest.NewRecorder()
	srv.handleMaintenanceBackup(rec, adminTestRequestAs(http.MethodPost, "/maintenance/backup", "operator"))

	if rec.Code != http.StatusFound {
		t.Fatalf("maintenance backup returned %d: %s", rec.Code, rec.Body.String())
	}

	items, err := store.ListActivity(context.Background(), 20, 0, registry.ActivityFilter{
		Actor:  "operator",
		Action: "REGISTRY_BACKUP",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 || !items[0].Success {
		t.Fatalf("expected successful backup audited as authenticated admin actor, got %+v", items)
	}
}
