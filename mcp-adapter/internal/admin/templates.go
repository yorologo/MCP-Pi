package admin

import (
	"bytes"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"path"
	"path/filepath"
	"strings"

	"github.com/flosch/pongo2/v6"
)

//go:embed templates/*
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS

type embedTemplateLoader struct {
	fs fs.FS
}

func (l *embedTemplateLoader) Abs(base, name string) string {
	if filepath.IsAbs(name) {
		return filepath.Clean(name)
	}
	cleaned := filepath.Clean(filepath.Join(filepath.Dir(base), name))
	return strings.TrimPrefix(cleaned, "/")
}

func (l *embedTemplateLoader) Get(p string) (io.Reader, error) {
	cleanPath := strings.TrimPrefix(p, "/")
	cleanPath = strings.TrimPrefix(cleanPath, "templates/")
	fullPath := path.Join("templates", cleanPath)

	content, err := fs.ReadFile(l.fs, fullPath)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(content), nil
}

// URLFor generates a relative URL path matching the Flask admin endpoints.
func URLFor(endpoint string, args ...interface{}) string {
	switch endpoint {
	case "static":
		if len(args) > 0 {
			return "/static/" + fmt.Sprint(args[0])
		}
		return "/static/"
	case "admin.dashboard":
		return "/dashboard"
	case "admin.targets_list":
		return "/targets"
	case "admin.target_add":
		return "/targets/add"
	case "admin.target_edit":
		if len(args) > 0 {
			return fmt.Sprintf("/targets/%v/edit", args[0])
		}
		return "/targets"
	case "admin.target_toggle":
		if len(args) > 0 {
			return fmt.Sprintf("/targets/%v/toggle", args[0])
		}
		return "/targets"
	case "admin.target_test":
		if len(args) > 0 {
			return fmt.Sprintf("/targets/%v/test", args[0])
		}
		return "/targets"
	case "admin.target_ssh_trust":
		if len(args) > 0 {
			return fmt.Sprintf("/targets/%v/ssh/trust", args[0])
		}
		return "/targets"
	case "admin.target_ssh_untrust":
		if len(args) > 0 {
			return fmt.Sprintf("/targets/%v/ssh/untrust", args[0])
		}
		return "/targets"
	case "admin.target_privilege_approve":
		if len(args) > 0 {
			return fmt.Sprintf("/targets/%v/privileges/approve", args[0])
		}
		return "/targets"
	case "admin.target_privilege_revoke":
		if len(args) > 0 {
			return fmt.Sprintf("/targets/%v/privileges/revoke", args[0])
		}
		return "/targets"
	case "admin.target_privilege_always_allow":
		if len(args) > 0 {
			return fmt.Sprintf("/targets/%v/privileges/always-allow", args[0])
		}
		return "/targets"
	case "admin.projects_list":
		if len(args) > 0 && fmt.Sprint(args[0]) != "" {
			return fmt.Sprintf("/projects?target=%v", url.QueryEscape(fmt.Sprint(args[0])))
		}
		return "/projects"
	case "admin.project_add":
		return "/projects/add"
	case "admin.project_edit":
		if len(args) >= 2 {
			return fmt.Sprintf("/projects/%v/%v/edit", args[0], args[1])
		}
		return "/projects"
	case "admin.project_toggle":
		if len(args) >= 2 {
			return fmt.Sprintf("/projects/%v/%v/toggle", args[0], args[1])
		}
		return "/projects"
	case "admin.project_toggle_write":
		if len(args) >= 2 {
			return fmt.Sprintf("/projects/%v/%v/toggle-write", args[0], args[1])
		}
		return "/projects"
	case "admin.clients_list":
		return "/clients"
	case "admin.client_add":
		return "/clients/add"
	case "admin.client_edit":
		if len(args) > 0 {
			return fmt.Sprintf("/clients/%v/edit", args[0])
		}
		return "/clients"
	case "admin.client_toggle":
		if len(args) > 0 {
			return fmt.Sprintf("/clients/%v/toggle", args[0])
		}
		return "/clients"
	case "admin.client_grants":
		if len(args) > 0 {
			return fmt.Sprintf("/clients/%v/grants", args[0])
		}
		return "/clients"
	case "admin.client_grant_add":
		if len(args) > 0 {
			return fmt.Sprintf("/clients/%v/grants/add", args[0])
		}
		return "/clients"
	case "admin.client_grant_edit":
		if len(args) >= 2 {
			return fmt.Sprintf("/clients/%v/grants/%v/edit", args[0], args[1])
		}
		return "/clients"
	case "admin.client_grant_toggle":
		if len(args) >= 2 {
			return fmt.Sprintf("/clients/%v/grants/%v/toggle", args[0], args[1])
		}
		return "/clients"
	case "admin.client_grant_delete":
		if len(args) >= 2 {
			return fmt.Sprintf("/clients/%v/grants/%v/delete", args[0], args[1])
		}
		return "/clients"
	case "admin.client_grant_check":
		if len(args) > 0 {
			return fmt.Sprintf("/clients/%v/grants/check", args[0])
		}
		return "/clients"
	case "admin.activity_view":
		if len(args) > 0 && fmt.Sprint(args[0]) != "" {
			return fmt.Sprintf("/activity?page=%v", args[0])
		}
		return "/activity"
	case "admin.system_view":
		return "/system"
	case "admin.settings_view":
		return "/settings"
	case "admin.toggle_kill_switch":
		return "/settings/kill-switch"
	case "admin.toggle_writes_switch":
		return "/settings/toggle-writes"
	case "admin.disable_writes":
		return "/settings/disable-writes"
	case "admin.toggle_shell_switch":
		return "/settings/toggle-shell"
	case "admin.maintenance_view":
		return "/maintenance"
	case "admin.maintenance_doctor":
		return "/maintenance/doctor"
	case "admin.maintenance_backup":
		return "/maintenance/backup"
	case "admin.maintenance_repair":
		return "/maintenance/repair"
	case "admin.maintenance_rollback":
		return "/maintenance/rollback"
	case "auth.login":
		if len(args) > 0 && fmt.Sprint(args[0]) != "" {
			return fmt.Sprintf("/login?next=%s", url.QueryEscape(fmt.Sprint(args[0])))
		}
		return "/login"
	case "auth.logout":
		return "/logout"
	default:
		return "/" + strings.TrimPrefix(endpoint, "/")
	}
}

// NewTemplateSet initializes a pongo2 TemplateSet configured with embed.FS and helper functions.
func NewTemplateSet() *pongo2.TemplateSet {
	loader := &embedTemplateLoader{fs: templatesFS}
	set := pongo2.NewSet("admin-templates", loader)

	set.Globals["url_for"] = func(endpoint string, args ...interface{}) string {
		return URLFor(endpoint, args...)
	}

	return set
}
