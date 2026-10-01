package admin

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"mcp-gateway-adapter/internal/core"
	"mcp-gateway-adapter/internal/policy"
	"mcp-gateway-adapter/internal/registry"
)

func adminFormRequest(method, target string, form url.Values) *http.Request {
	if form == nil {
		form = url.Values{}
	}
	req := httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx := context.WithValue(req.Context(), sessionContextKey, &SessionData{
		Username:  "admin",
		CSRFToken: "test-csrf",
	})
	return req.WithContext(ctx)
}

func TestProjectPermissionTogglePersistsAndRerenders(t *testing.T) {
	srv, store := setupTestAdminServer(t)
	ctx := context.Background()

	rec := httptest.NewRecorder()
	srv.handleProjectSubroutes(rec, adminFormRequest(http.MethodPost, "/projects/test-target/test-proj/toggle-write", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("toggle write returned %d: %s", rec.Code, rec.Body.String())
	}
	project, err := store.GetProject(ctx, "test-target", "test-proj", true)
	if err != nil {
		t.Fatal(err)
	}
	if !project.Write {
		t.Fatal("write capability was not persisted")
	}

	rec = httptest.NewRecorder()
	srv.handleProjectsList(rec, adminTestRequest(http.MethodGet, "/projects"))
	body := rec.Body.String()
	for _, want := range []string{"WRITE ✓", "aria-pressed=\"true\""} {
		if !strings.Contains(body, want) {
			t.Fatalf("projects page missing %q after write toggle", want)
		}
	}
	if strings.Contains(body, "Allow Write") || strings.Contains(body, "Revoke Write") {
		t.Fatal("redundant Allow/Revoke Write action is still rendered")
	}

	project.Read = true
	if err := store.UpdateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	srv.handleProjectSubroutes(rec, adminTestRequest(http.MethodGet, "/projects/test-target/test-proj/edit"))
	body = rec.Body.String()
	for _, id := range []string{"read", "write"} {
		idx := strings.Index(body, fmt.Sprintf("id=\"%s\"", id))
		if idx < 0 {
			t.Fatalf("project edit missing %s checkbox", id)
		}
		end := idx + 300
		if end > len(body) {
			end = len(body)
		}
		if !strings.Contains(body[idx:end], "checked") {
			t.Fatalf("project edit did not prefill %s=true", id)
		}
	}
}

func TestProjectTargetFilterIsVisible(t *testing.T) {
	srv, _ := setupTestAdminServer(t)
	rec := httptest.NewRecorder()
	srv.handleProjectsList(rec, adminTestRequest(http.MethodGet, "/projects?target=test-target"))
	if rec.Code != http.StatusOK {
		t.Fatalf("projects returned %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Showing projects for Target") ||
		!strings.Contains(rec.Body.String(), "test-target") {
		t.Fatal("target filter is applied but not visible in the page")
	}
}

func TestGrantAddRequiresExplicitCapability(t *testing.T) {
	srv, store := setupTestAdminServer(t)
	ctx := context.Background()
	before, err := store.ListGrants(ctx, "test-client")
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.handleClientSubroutes(rec, adminFormRequest(http.MethodPost, "/clients/test-client/grants/add", url.Values{
		"target_id":  {"test-target"},
		"project_id": {"test-proj"},
		"enabled":    {"on"},
	}))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid add returned %d", rec.Code)
	}
	after, err := store.ListGrants(ctx, "test-client")
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("missing capability created a grant: before=%d after=%d", len(before), len(after))
	}

	rec = httptest.NewRecorder()
	srv.handleClientSubroutes(rec, adminFormRequest(http.MethodPost, "/clients/test-client/grants/add", url.Values{
		"target_id":  {"test-target"},
		"project_id": {"test-proj"},
		"capability": {"write"},
		"enabled":    {"on"},
	}))
	if rec.Code != http.StatusFound {
		t.Fatalf("valid add returned %d: %s", rec.Code, rec.Body.String())
	}
	after, err = store.ListGrants(ctx, "test-client")
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before)+1 || after[len(after)-1].Capability != "write" {
		t.Fatalf("explicit capability was not preserved: %+v", after)
	}
}

func TestGrantEditRoundTripAndClientScope(t *testing.T) {
	srv, store := setupTestAdminServer(t)
	ctx := context.Background()
	grants, err := store.ListGrants(ctx, "test-client")
	if err != nil || len(grants) == 0 {
		t.Fatalf("seed grants: %v %+v", err, grants)
	}
	grantID := grants[0].ID
	if _, err := store.AddGrant(ctx, registry.Grant{
		ClientID: "test-client", TargetID: "test-target", ProjectID: "test-proj",
		Capability: "write", Enabled: false,
	}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.handleClientSubroutes(rec, adminTestRequest(http.MethodGet, fmt.Sprintf("/clients/test-client/grants/%d/edit", grantID)))
	if rec.Code != http.StatusOK {
		t.Fatalf("edit GET returned %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Edit Access",
		"name=\"capability\" value=\"read\" class=\"mt-1 rounded\" data-grant-capability checked",
		"name=\"capability\" value=\"write\" class=\"mt-1 rounded\" data-grant-capability checked",
		"name=\"expected_capability\" value=\"read\"",
		"name=\"expected_capability\" value=\"write\"",
		"Save Access",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("grouped edit form missing %q: %s", want, body)
		}
	}

	rec = httptest.NewRecorder()
	srv.handleClientSubroutes(rec, adminFormRequest(http.MethodPost, fmt.Sprintf("/clients/test-client/grants/%d/edit", grantID), url.Values{
		"target_id":           {"forged-target"},
		"project_id":          {"forged-project"},
		"expected_capability": {"read", "write"},
		"capability":          {"read", "tasks"},
	}))
	if rec.Code != http.StatusFound {
		t.Fatalf("edit POST returned %d: %s", rec.Code, rec.Body.String())
	}
	updated, err := store.GetGrantForClient(ctx, "test-client", grantID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Capability != "read" || updated.TargetID != "test-target" || updated.ProjectID != "test-proj" {
		t.Fatalf("anchor grant or immutable scope changed: %+v", updated)
	}

	after, err := store.ListGrants(ctx, "test-client")
	if err != nil {
		t.Fatal(err)
	}
	byCapability := map[string]registry.Grant{}
	for _, candidate := range after {
		if candidate.TargetID == "test-target" && candidate.ProjectID == "test-proj" {
			byCapability[candidate.Capability] = candidate
		}
	}
	if _, ok := byCapability["write"]; ok {
		t.Fatal("write grant was not removed by grouped edit")
	}
	if taskGrant, ok := byCapability["tasks"]; !ok || !taskGrant.Enabled {
		t.Fatalf("new tasks grant missing or disabled: %+v", taskGrant)
	}
	if readGrant := byCapability["read"]; readGrant.ID != grantID {
		t.Fatalf("unchanged read grant was rewritten: %+v", readGrant)
	}

	if err := store.AddClient(ctx, registry.Client{ID: "other", DisplayName: "Other", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	otherID, err := store.AddGrant(ctx, registry.Grant{
		ClientID: "other", TargetID: "test-target", ProjectID: "test-proj", Capability: "read", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	srv.handleClientSubroutes(rec, adminFormRequest(http.MethodPost, fmt.Sprintf("/clients/test-client/grants/%d/toggle", otherID), nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-client toggle returned %d want 404", rec.Code)
	}
	otherGrant, err := store.GetGrantForClient(ctx, "other", otherID)
	if err != nil {
		t.Fatal(err)
	}
	if !otherGrant.Enabled {
		t.Fatal("cross-client route modified another client's grant")
	}
}

func TestGrantScopeEditRejectsStaleExpectedSet(t *testing.T) {
	srv, store := setupTestAdminServer(t)
	ctx := context.Background()
	grants, err := store.ListGrants(ctx, "test-client")
	if err != nil || len(grants) == 0 {
		t.Fatalf("seed grants: %v %+v", err, grants)
	}
	grantID := grants[0].ID
	if _, err := store.AddGrant(ctx, registry.Grant{
		ClientID: "test-client", TargetID: "test-target", ProjectID: "test-proj",
		Capability: "write", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.handleClientSubroutes(rec, adminFormRequest(http.MethodPost, fmt.Sprintf("/clients/test-client/grants/%d/edit", grantID), url.Values{
		"expected_capability": {"read"},
		"capability":          {"read", "tasks"},
	}))
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale scope edit returned %d want 409: %s", rec.Code, rec.Body.String())
	}

	after, err := store.ListGrants(ctx, "test-client")
	if err != nil {
		t.Fatal(err)
	}
	caps := map[string]bool{}
	for _, grant := range after {
		if grant.TargetID == "test-target" && grant.ProjectID == "test-proj" {
			caps[grant.Capability] = true
		}
	}
	if !caps["read"] || !caps["write"] || caps["tasks"] {
		t.Fatalf("stale edit mutated current scope: %+v", caps)
	}
}

func TestGrantScopeEditRequiresExplicitRemoveAllConfirmation(t *testing.T) {
	srv, store := setupTestAdminServer(t)
	ctx := context.Background()
	grants, err := store.ListGrants(ctx, "test-client")
	if err != nil || len(grants) == 0 {
		t.Fatalf("seed grants: %v %+v", err, grants)
	}
	grantID := grants[0].ID

	rec := httptest.NewRecorder()
	srv.handleClientSubroutes(rec, adminFormRequest(http.MethodPost, fmt.Sprintf("/clients/test-client/grants/%d/edit", grantID), url.Values{
		"expected_capability": {"read"},
	}))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unconfirmed remove-all returned %d want 422: %s", rec.Code, rec.Body.String())
	}
	if _, err := store.GetGrantForClient(ctx, "test-client", grantID); err != nil {
		t.Fatalf("unconfirmed remove-all deleted anchor grant: %v", err)
	}

	rec = httptest.NewRecorder()
	srv.handleClientSubroutes(rec, adminFormRequest(http.MethodPost, fmt.Sprintf("/clients/test-client/grants/%d/edit", grantID), url.Values{
		"expected_capability": {"read"},
		"confirm_remove_all":  {"on"},
	}))
	if rec.Code != http.StatusFound {
		t.Fatalf("confirmed remove-all returned %d: %s", rec.Code, rec.Body.String())
	}
	after, err := store.ListGrants(ctx, "test-client")
	if err != nil {
		t.Fatal(err)
	}
	for _, grant := range after {
		if grant.TargetID == "test-target" && grant.ProjectID == "test-proj" {
			t.Fatalf("confirmed remove-all left scope grant: %+v", grant)
		}
	}
}

func TestSecurityMutationRollsBackWhenSuccessAuditFails(t *testing.T) {
	srv, store := setupTestAdminServer(t)
	ctx := context.Background()
	trigger := "CREATE TRIGGER block_security_success_audit " +
		"BEFORE INSERT ON activity " +
		"WHEN NEW.action IN ('toggle_project_write', 'add_grant') " +
		"BEGIN SELECT RAISE(ABORT, 'audit blocked'); END;"
	if _, err := store.DB().ExecContext(ctx, trigger); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.handleProjectSubroutes(rec, adminFormRequest(http.MethodPost, "/projects/test-target/test-proj/toggle-write", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("toggle write audit failure returned %d want 500", rec.Code)
	}
	project, err := store.GetProject(ctx, "test-target", "test-proj", true)
	if err != nil {
		t.Fatal(err)
	}
	if project.Write {
		t.Fatal("project write change survived failed success audit")
	}

	before, err := store.ListGrants(ctx, "test-client")
	if err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	srv.handleClientSubroutes(rec, adminFormRequest(http.MethodPost, "/clients/test-client/grants/add", url.Values{
		"target_id":  {"test-target"},
		"project_id": {"test-proj"},
		"capability": {"write"},
		"enabled":    {"on"},
	}))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("add grant audit failure returned %d want 500", rec.Code)
	}
	after, err := store.ListGrants(ctx, "test-client")
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("grant survived failed success audit: before=%d after=%d", len(before), len(after))
	}
}

func TestTargetEditRejectsInvalidConfigurationWithoutMutation(t *testing.T) {
	srv, store := setupTestAdminServer(t)
	ctx := context.Background()
	before, err := store.GetTarget(ctx, "test-target", true)
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.handleTargetEdit(rec, adminFormRequest(http.MethodPost, "/targets/test-target/edit", url.Values{
		"display_name":     {"Broken"},
		"platform":         {"linux"},
		"host":             {""},
		"port":             {"not-a-number"},
		"user":             {""},
		"privilege_policy": {"nonsense"},
		"enabled":          {"on"},
	}), "test-target")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid target edit returned %d", rec.Code)
	}
	after, err := store.GetTarget(ctx, "test-target", true)
	if err != nil {
		t.Fatal(err)
	}
	if after.Host != before.Host || after.Port != before.Port || after.User != before.User ||
		after.PrivilegePolicy != before.PrivilegePolicy || after.DisplayName != before.DisplayName {
		t.Fatalf("invalid target edit mutated state: before=%+v after=%+v", before, after)
	}
}

func TestReservedInternalClientIDCannotBeRegistered(t *testing.T) {
	srv, store := setupTestAdminServer(t)
	rec := httptest.NewRecorder()
	srv.handleClientAdd(rec, adminFormRequest(http.MethodPost, "/clients/add", url.Values{
		"id":           {"admin"},
		"display_name": {"External Admin"},
		"protocol":     {"mcp"},
		"enabled":      {"on"},
	}))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("reserved client form returned %d", rec.Code)
	}
	if _, err := store.GetClient(context.Background(), "admin"); err == nil {
		t.Fatal("reserved internal client ID was registered externally")
	}
}

func TestSettingsTimezonePersistsAndRendersEndToEnd(t *testing.T) {
	srv, store := setupTestAdminServer(t)
	form := url.Values{
		"default_timeout":     {"30"},
		"max_output_bytes":    {"262144"},
		"max_file_read_bytes": {"1048576"},
		"max_write_bytes":     {"262144"},
		"activity_retention":  {"5000"},
		"admin_timezone":      {"America/Mexico_City"},
	}
	rec := httptest.NewRecorder()
	srv.handleSettings(rec, adminFormRequest(http.MethodPost, "/settings", form))
	if rec.Code != http.StatusFound {
		t.Fatalf("settings POST returned %d: %s", rec.Code, rec.Body.String())
	}
	tz, err := store.GetSetting(context.Background(), "admin_timezone", "")
	if err != nil {
		t.Fatal(err)
	}
	if tz != "America/Mexico_City" {
		t.Fatalf("stored timezone=%q", tz)
	}

	rec = httptest.NewRecorder()
	srv.handleSettings(rec, adminTestRequest(http.MethodGet, "/settings"))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Effective display time zone") ||
		!strings.Contains(rec.Body.String(), "America/Mexico_City") {
		t.Fatal("settings page does not expose effective time zone")
	}

	rec = httptest.NewRecorder()
	srv.handleActivity(rec, adminTestRequest(http.MethodGet, "/activity"))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Timestamp (America/Mexico_City)") {
		t.Fatal("activity page did not use persisted time zone")
	}
}

func TestEffectiveAccessWildcardMeansNoScopeConstraint(t *testing.T) {
	srv, _ := setupTestAdminServer(t)
	rec := httptest.NewRecorder()
	srv.handleClientSubroutes(rec, adminFormRequest(http.MethodPost, "/clients/test-client/grants/check", url.Values{
		"target_id":  {"*"},
		"project_id": {"*"},
		"tool_name":  {"read_file"},
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("access check returned %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "TARGET_NOT_FOUND") || !strings.Contains(body, "ALLOWED") {
		t.Fatalf("wildcard access check was interpreted as a literal Target: %s", body)
	}
}

func TestTargetFormRejectsUnsupportedPlatform(t *testing.T) {
	req := adminFormRequest(http.MethodPost, "/targets/add", url.Values{
		"id":               {"bad-platform"},
		"display_name":     {"Bad Platform"},
		"platform":         {"plan9"},
		"host":             {"127.0.0.1"},
		"port":             {"22"},
		"user":             {"tester"},
		"privilege_policy": {"never"},
		"enabled":          {"on"},
	})
	if _, err := parseTargetForm(req, ""); err == nil || !strings.Contains(err.Error(), "unsupported platform") {
		t.Fatalf("unsupported platform was not rejected: %v", err)
	}
}

func testPrivilegeStatusResponse(bootID string) core.Response {
	return core.Response{
		OK: true,
		Result: map[string]any{
			"facts": map[string]any{
				"probe_status": "ok",
				"boot_id":      bootID,
				"privilege": map[string]any{
					"current_level":              "standard",
					"maximum_level":              "root",
					"backend":                    "sudo",
					"backend_ready":              true,
					"transport_already_elevated": false,
					"shell_can_elevate":          true,
					"independent_elevator":       "sudo",
				},
			},
		},
	}
}

func TestTargetPrivilegeStatusViewMatchesTemplateContract(t *testing.T) {
	target := registry.Target{
		ID:              "t",
		PrivilegePolicy: "ask_once_per_boot",
		PrivilegeUser:   "root",
	}
	approval := &registry.PrivilegeApproval{
		TargetID:  "t",
		Policy:    "ask_once_per_boot",
		ClientID:  "client",
		ProjectID: "project",
		BootID:    "boot-1",
	}
	scopes := []map[string]interface{}{{
		"client_id":  "client",
		"project_id": "project",
	}}

	view := targetPrivilegeStatusView(target, approval, scopes, testPrivilegeStatusResponse("boot-1"))
	ok, _ := view["ok"].(bool)
	if !ok {
		t.Fatalf("privilege status unexpectedly unavailable: %+v", view)
	}
	result, ok := view["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("result type=%T", view["result"])
	}
	if result["policy"] != "ask_once_per_boot" || result["current_level"] != "standard" ||
		result["maximum_level"] != "root" || result["backend_user"] != "root" {
		t.Fatalf("unexpected privilege view: %+v", result)
	}
	if valid, _ := result["approval_valid"].(bool); !valid {
		t.Fatalf("matching per-boot approval was not marked active: %+v", result)
	}

	stale := targetPrivilegeStatusView(target, approval, scopes, testPrivilegeStatusResponse("boot-2"))
	staleResult := stale["result"].(map[string]interface{})
	if valid, _ := staleResult["approval_valid"].(bool); valid {
		t.Fatal("approval from a different boot was marked active")
	}
}

func TestTargetApprovalBootIDFailsClosed(t *testing.T) {
	bootID, err := targetApprovalBootID("ask_once_per_boot", testPrivilegeStatusResponse("boot-1"))
	if err != nil || bootID != "boot-1" {
		t.Fatalf("boot approval context = %q, %v", bootID, err)
	}
	if _, err := targetApprovalBootID("ask_once_per_boot", testPrivilegeStatusResponse("")); err == nil {
		t.Fatal("missing Target boot identity did not fail closed")
	}
	if bootID, err := targetApprovalBootID("ask_always", core.Response{}); err != nil || bootID != "" {
		t.Fatalf("ask_always unexpectedly depends on boot identity: %q, %v", bootID, err)
	}
}

func TestTargetPrivilegeScopesRequireTargetAdmin(t *testing.T) {
	srv, store := setupTestAdminServer(t)
	ctx := context.Background()
	if err := store.SetSetting(ctx, "shell_enabled", "true"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddGrant(ctx, registry.Grant{
		ClientID: "test-client", TargetID: "test-target", ProjectID: "test-proj",
		Capability: "target_shell", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	scopes, err := srv.targetPrivilegeScopes(ctx, "test-target")
	if err != nil {
		t.Fatal(err)
	}
	if len(scopes) != 0 {
		t.Fatalf("scope without target_admin was offered for approval: %+v", scopes)
	}

	if _, err := store.AddGrant(ctx, registry.Grant{
		ClientID: "test-client", TargetID: "test-target", ProjectID: "test-proj",
		Capability: "target_admin", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	scopes, err = srv.targetPrivilegeScopes(ctx, "test-target")
	if err != nil {
		t.Fatal(err)
	}
	if len(scopes) != 1 || scopes[0]["client_id"] != "test-client" || scopes[0]["project_id"] != "test-proj" {
		t.Fatalf("expected one eligible privilege scope, got %+v", scopes)
	}
}

func TestTargetPrivilegeApproveUsesCurrentPolicyAndRejectsForgedScope(t *testing.T) {
	srv, store := setupTestAdminServer(t)
	ctx := context.Background()
	target, err := store.GetTarget(ctx, "test-target", true)
	if err != nil {
		t.Fatal(err)
	}
	target.PrivilegePolicy = "ask_always"
	if err := store.UpdateTarget(ctx, target); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSetting(ctx, "shell_enabled", "true"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddGrant(ctx, registry.Grant{
		ClientID: "test-client", TargetID: "test-target", ProjectID: "test-proj",
		Capability: "target_shell", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	scope := `["test-client","test-proj"]`
	rec := httptest.NewRecorder()
	srv.handleTargetPrivilegeApprove(rec, adminFormRequest(http.MethodPost, "/targets/test-target/privileges/approve", url.Values{
		"scope": {scope},
	}), "test-target")
	if rec.Code != http.StatusFound {
		t.Fatalf("forged approval returned %d", rec.Code)
	}
	if approval, err := store.GetPrivilegeApproval(ctx, "test-target"); err != nil || approval != nil {
		t.Fatalf("approval without target_admin was persisted: %+v, %v", approval, err)
	}

	if _, err := store.AddGrant(ctx, registry.Grant{
		ClientID: "test-client", TargetID: "test-target", ProjectID: "test-proj",
		Capability: "target_admin", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	srv.handleTargetPrivilegeApprove(rec, adminFormRequest(http.MethodPost, "/targets/test-target/privileges/approve", url.Values{
		"scope": {scope},
	}), "test-target")
	if rec.Code != http.StatusFound {
		t.Fatalf("authorized approval returned %d: %s", rec.Code, rec.Body.String())
	}
	approval, err := store.GetPrivilegeApproval(ctx, "test-target")
	if err != nil {
		t.Fatal(err)
	}
	if approval == nil || approval.Policy != "ask_always" || approval.BootID != "" ||
		approval.ClientID != "test-client" || approval.ProjectID != "test-proj" {
		t.Fatalf("unexpected persisted approval: %+v", approval)
	}
}

func TestCapabilityBundleIsRejectedByAdminValidation(t *testing.T) {
	_, store := setupTestAdminServer(t)
	req := adminFormRequest(http.MethodPost, "/clients/test-client/grants/add", url.Values{
		"target_id":  {"test-target"},
		"project_id": {"test-proj"},
		"capability": {"target_shell,target_admin"},
		"enabled":    {"on"},
	})
	if _, _, err := validateGrantBatchForm(context.Background(), store, req, "test-client"); err == nil ||
		!strings.Contains(err.Error(), "unsupported capability") {
		t.Fatalf("capability bundle was not rejected: %v", err)
	}
}

func TestGrantScopeEditHighImpactRequiresExplicitConfirmation(t *testing.T) {
	req := adminFormRequest(http.MethodPost, "/clients/test-client/grants/1/edit", url.Values{
		"expected_capability": {"read"},
		"capability":          {"read", "target_admin"},
	})
	if _, _, err := validateGrantScopeEditForm(req); err == nil ||
		!strings.Contains(err.Error(), "high-impact access requires explicit confirmation") {
		t.Fatalf("high-impact scope edit was not rejected: %v", err)
	}

	req = adminFormRequest(http.MethodPost, "/clients/test-client/grants/1/edit", url.Values{
		"expected_capability": {"read"},
		"capability":          {"read", "target_admin"},
		"confirm_high_impact": {"on"},
	})
	desired, expected, err := validateGrantScopeEditForm(req)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(expected, ",") != "read" || strings.Join(desired, ",") != "read,target_admin" {
		t.Fatalf("unexpected normalized scope expected=%v desired=%v", expected, desired)
	}
}

func TestGrantPresetsMatchPolicyCatalog(t *testing.T) {
	expected := map[string][]string{
		"read-only":             {"read"},
		"structured-operator":   {"read", "write", "tasks"},
		"trusted-shell":         {"read", "write", "tasks", "target_shell"},
		"privileged-shell":      {"read", "write", "tasks", "target_shell", "target_admin"},
		"gateway-diagnostics":   {"status", "doctor"},
		"gateway-maintenance":   {"status", "doctor", "backup", "maintenance"},
		"gateway-administrator": {"admin"},
		"all-ordinary-access":   {"*"},
	}
	presets := grantPresets()
	if len(presets) != len(expected) {
		t.Fatalf("preset count=%d want %d", len(presets), len(expected))
	}
	for _, preset := range presets {
		want, ok := expected[preset.ID]
		if !ok {
			t.Fatalf("unexpected preset %q", preset.ID)
		}
		if strings.Join(preset.Capabilities, ",") != strings.Join(want, ",") {
			t.Fatalf("preset %s=%v want %v", preset.ID, preset.Capabilities, want)
		}
		for _, capability := range preset.Capabilities {
			if !policy.IsGrantCapability(capability) {
				t.Fatalf("preset %s contains unsupported capability %q", preset.ID, capability)
			}
		}
	}
}

func TestGrantBatchNormalizesDeduplicatesAndAddsAtomically(t *testing.T) {
	srv, store := setupTestAdminServer(t)
	ctx := context.Background()
	before, err := store.ListGrants(ctx, "test-client")
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.handleClientSubroutes(rec, adminFormRequest(http.MethodPost, "/clients/test-client/grants/add", url.Values{
		"target_id":  {"test-target"},
		"project_id": {"test-proj"},
		"capability": {" write ", "tasks", "write"},
		"enabled":    {"on"},
	}))
	if rec.Code != http.StatusFound {
		t.Fatalf("batch add returned %d: %s", rec.Code, rec.Body.String())
	}
	after, err := store.ListGrants(ctx, "test-client")
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before)+2 {
		t.Fatalf("batch add count=%d want %d: %+v", len(after), len(before)+2, after)
	}
	got := map[string]int{}
	for _, grant := range after {
		got[grant.Capability]++
	}
	if got["write"] != 1 || got["tasks"] != 1 {
		t.Fatalf("batch normalization/dedupe failed: %+v", got)
	}

	before = after
	rec = httptest.NewRecorder()
	srv.handleClientSubroutes(rec, adminFormRequest(http.MethodPost, "/clients/test-client/grants/add", url.Values{
		"target_id":  {"test-target"},
		"project_id": {"test-proj"},
		"capability": {"doctor", "not-a-capability"},
		"enabled":    {"on"},
	}))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid batch returned %d want 422", rec.Code)
	}
	after, err = store.ListGrants(ctx, "test-client")
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("invalid batch partially mutated grants: before=%d after=%d", len(before), len(after))
	}
}

func TestGrantBatchRejectsExistingDuplicateWithoutPartialInsert(t *testing.T) {
	srv, store := setupTestAdminServer(t)
	ctx := context.Background()
	before, err := store.ListGrants(ctx, "test-client")
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.handleClientSubroutes(rec, adminFormRequest(http.MethodPost, "/clients/test-client/grants/add", url.Values{
		"target_id":  {"test-target"},
		"project_id": {"test-proj"},
		"capability": {"write", "read"},
		"enabled":    {"on"},
	}))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate batch returned %d want 422", rec.Code)
	}
	after, err := store.ListGrants(ctx, "test-client")
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("duplicate batch partially mutated grants: before=%d after=%d", len(before), len(after))
	}
}

func TestGrantHighImpactConfirmationIsScopeIndependent(t *testing.T) {
	tests := []struct {
		name       string
		capability string
	}{
		{name: "target_admin", capability: "target_admin"},
		{name: "wildcard", capability: "*"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, store := setupTestAdminServer(t)
			ctx := context.Background()
			before, err := store.ListGrants(ctx, "test-client")
			if err != nil {
				t.Fatal(err)
			}
			form := url.Values{
				"target_id":  {"test-target"},
				"project_id": {"test-proj"},
				"capability": {tt.capability},
				"enabled":    {"on"},
			}
			rec := httptest.NewRecorder()
			srv.handleClientSubroutes(rec, adminFormRequest(http.MethodPost, "/clients/test-client/grants/add", form))
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("without confirmation returned %d want 422", rec.Code)
			}
			after, err := store.ListGrants(ctx, "test-client")
			if err != nil {
				t.Fatal(err)
			}
			if len(after) != len(before) {
				t.Fatalf("high-impact rejection mutated grants: before=%d after=%d", len(before), len(after))
			}

			form.Set("confirm_high_impact", "on")
			rec = httptest.NewRecorder()
			srv.handleClientSubroutes(rec, adminFormRequest(http.MethodPost, "/clients/test-client/grants/add", form))
			if rec.Code != http.StatusFound {
				t.Fatalf("with confirmation returned %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestNormalizeGrantCapabilitiesRejectsEmptyAndInvalid(t *testing.T) {
	if _, err := normalizeGrantCapabilities([]string{"", "  "}); err == nil {
		t.Fatal("empty capability selection was accepted")
	}
	if _, err := normalizeGrantCapabilities([]string{"read", "invalid"}); err == nil {
		t.Fatal("invalid capability selection was accepted")
	}
	got, err := normalizeGrantCapabilities([]string{"tasks", "read", "tasks"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "read,tasks" {
		t.Fatalf("normalized capabilities=%v want [read tasks]", got)
	}
}
