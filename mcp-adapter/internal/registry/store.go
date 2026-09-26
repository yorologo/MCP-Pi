package registry

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type LookupError struct {
	Code    string
	Message string
}

func (e *LookupError) Error() string { return e.Message }

func ErrorCode(err error) string {
	var lookup *LookupError
	if errors.As(err, &lookup) {
		return lookup.Code
	}
	return ""
}

type Task struct {
	Enabled bool     `json:"enabled"`
	Argv    []string `json:"argv"`
	Timeout int      `json:"timeout"`
}

type Project struct {
	ID          string          `json:"id"`
	TargetID    string          `json:"target_id"`
	DisplayName string          `json:"display_name"`
	Root        string          `json:"root"`
	Read        bool            `json:"read_enabled"`
	Write       bool            `json:"write_enabled"`
	Enabled     bool            `json:"enabled"`
	Tasks       map[string]Task `json:"tasks"`
}

type Target struct {
	ID              string             `json:"id"`
	DisplayName     string             `json:"display_name"`
	Platform        string             `json:"platform"`
	Host            string             `json:"host"`
	Port            int                `json:"port"`
	User            string             `json:"user"`
	SSHAlias        string             `json:"ssh_alias"`
	PrivilegeUser   string             `json:"privilege_user"`
	PrivilegePolicy string             `json:"privilege_policy"`
	Enabled         bool               `json:"enabled"`
	Projects        map[string]Project `json:"projects"`
}

type TargetSummary struct {
	ID              string   `json:"id"`
	DisplayName     string   `json:"display_name"`
	Platform        string   `json:"platform"`
	PrivilegePolicy string   `json:"privilege_policy"`
	Enabled         bool     `json:"enabled"`
	ProjectCount    int      `json:"project_count"`
	Projects        []string `json:"projects"`
}

type Client struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Provider    string `json:"provider"`
	Protocol    string `json:"protocol"`
	Enabled     bool   `json:"enabled"`
	Notes       string `json:"notes"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type PrivilegeApproval struct {
	TargetID   string  `json:"target_id"`
	Policy     string  `json:"policy"`
	ClientID   string  `json:"client_id"`
	ProjectID  string  `json:"project_id"`
	BootID     string  `json:"boot_id"`
	ApprovedAt float64 `json:"approved_at"`
}

type Grant struct {
	ID         int64  `json:"id"`
	ClientID   string `json:"client_id"`
	TargetID   string `json:"target_id"`
	ProjectID  string `json:"project_id"`
	Capability string `json:"capability"`
	Enabled    bool   `json:"enabled"`
	CreatedAt  string `json:"created_at"`
}

type Activity struct {
	Actor            string
	Action           string
	TargetID         *string
	ProjectID        *string
	DurationMS       *int64
	Success          bool
	ErrorCode        *string
	BytesTransferred *int64
	Detail           string
}

type Store struct {
	db   *sql.DB
	path string
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func OpenStore(ctx context.Context, path string) (*Store, error) {
	db, err := Open(ctx, path)
	if err != nil {
		return nil, err
	}
	return &Store{db: db, path: path}, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) DB() *sql.DB {
	if s == nil {
		return nil
	}
	return s.db
}

func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

func (s *Store) ListTargets(ctx context.Context) ([]TargetSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, display_name, platform, privilege_policy, enabled
FROM targets
ORDER BY id
`)
	if err != nil {
		return nil, fmt.Errorf("list targets: %w", err)
	}

	var out []TargetSummary
	for rows.Next() {
		var item TargetSummary
		var enabled int
		if err := rows.Scan(&item.ID, &item.DisplayName, &item.Platform, &item.PrivilegePolicy, &enabled); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan target: %w", err)
		}
		item.Enabled = enabled != 0
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate targets: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close target rows: %w", err)
	}

	for i := range out {
		projectRows, err := s.db.QueryContext(ctx, "SELECT id FROM projects WHERE target_id = ? ORDER BY id", out[i].ID)
		if err != nil {
			return nil, fmt.Errorf("list target projects %q: %w", out[i].ID, err)
		}
		for projectRows.Next() {
			var id string
			if err := projectRows.Scan(&id); err != nil {
				projectRows.Close()
				return nil, fmt.Errorf("scan target project %q: %w", out[i].ID, err)
			}
			out[i].Projects = append(out[i].Projects, id)
		}
		if err := projectRows.Err(); err != nil {
			projectRows.Close()
			return nil, fmt.Errorf("iterate target projects %q: %w", out[i].ID, err)
		}
		if err := projectRows.Close(); err != nil {
			return nil, fmt.Errorf("close target project rows %q: %w", out[i].ID, err)
		}
		out[i].ProjectCount = len(out[i].Projects)
	}
	return out, nil
}

func (s *Store) GetTarget(ctx context.Context, targetID string, includeDisabled bool) (Target, error) {
	var t Target
	var enabled int
	err := s.db.QueryRowContext(ctx, `
SELECT id, display_name, platform, host, port, user, COALESCE(ssh_alias, ''),
       privilege_user, privilege_policy, enabled
FROM targets
WHERE id = ?
`, targetID).Scan(
		&t.ID,
		&t.DisplayName,
		&t.Platform,
		&t.Host,
		&t.Port,
		&t.User,
		&t.SSHAlias,
		&t.PrivilegeUser,
		&t.PrivilegePolicy,
		&enabled,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Target{}, &LookupError{Code: "UNKNOWN_TARGET", Message: fmt.Sprintf("Target '%s' is not configured", targetID)}
	}
	if err != nil {
		return Target{}, fmt.Errorf("get target %q: %w", targetID, err)
	}
	t.Enabled = enabled != 0
	if !includeDisabled && !t.Enabled {
		return Target{}, &LookupError{Code: "TARGET_DISABLED", Message: fmt.Sprintf("Target '%s' is disabled", targetID)}
	}

	projects, err := s.projectsByTarget(ctx, targetID)
	if err != nil {
		return Target{}, err
	}
	t.Projects = projects
	return t, nil
}

func (s *Store) ListProjects(ctx context.Context, targetID string) ([]Project, error) {
	query := `
SELECT id, target_id, display_name, root, read_enabled, write_enabled, enabled
FROM projects
`
	var args []any
	if targetID != "" {
		query += " WHERE target_id = ? ORDER BY id"
		args = append(args, targetID)
	} else {
		query += " ORDER BY target_id, id"
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}

	var out []Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate projects: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close project rows: %w", err)
	}

	for i := range out {
		out[i].Tasks, err = s.tasksForProject(ctx, out[i].TargetID, out[i].ID)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) GetProject(ctx context.Context, targetID, projectID string, includeDisabled bool) (Project, error) {
	target, err := s.GetTarget(ctx, targetID, includeDisabled)
	if err != nil {
		return Project{}, err
	}
	project, ok := target.Projects[projectID]
	if !ok {
		return Project{}, &LookupError{
			Code:    "UNKNOWN_PROJECT",
			Message: fmt.Sprintf("Project '%s' is not configured under target '%s'", projectID, targetID),
		}
	}
	if !includeDisabled && !project.Enabled {
		return Project{}, &LookupError{
			Code:    "PROJECT_DISABLED",
			Message: fmt.Sprintf("Project '%s' in target '%s' is disabled", projectID, targetID),
		}
	}
	return project, nil
}

func (s *Store) projectsByTarget(ctx context.Context, targetID string) (map[string]Project, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, target_id, display_name, root, read_enabled, write_enabled, enabled
FROM projects
WHERE target_id = ?
ORDER BY id
`, targetID)
	if err != nil {
		return nil, fmt.Errorf("list projects for target %q: %w", targetID, err)
	}

	var projects []Project
	for rows.Next() {
		project, err := scanProject(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		projects = append(projects, project)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate projects for target %q: %w", targetID, err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close projects for target %q: %w", targetID, err)
	}

	out := make(map[string]Project, len(projects))
	for _, project := range projects {
		project.Tasks, err = s.tasksForProject(ctx, targetID, project.ID)
		if err != nil {
			return nil, err
		}
		out[project.ID] = project
	}
	return out, nil
}

type scanner interface {
	Scan(...any) error
}

func scanProject(row scanner) (Project, error) {
	var p Project
	var readEnabled, writeEnabled, enabled int
	if err := row.Scan(
		&p.ID,
		&p.TargetID,
		&p.DisplayName,
		&p.Root,
		&readEnabled,
		&writeEnabled,
		&enabled,
	); err != nil {
		return Project{}, fmt.Errorf("scan project: %w", err)
	}
	p.Read = readEnabled != 0
	p.Write = writeEnabled != 0
	p.Enabled = enabled != 0
	return p, nil
}

func (s *Store) tasksForProject(ctx context.Context, targetID, projectID string) (map[string]Task, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT task_name, argv_json, timeout, enabled
FROM project_tasks
WHERE target_id = ? AND project_id = ?
ORDER BY task_name
`, targetID, projectID)
	if err != nil {
		return nil, fmt.Errorf("list tasks for %s/%s: %w", targetID, projectID, err)
	}
	defer rows.Close()

	out := make(map[string]Task)
	for rows.Next() {
		var name, rawArgv string
		var task Task
		var enabled int
		if err := rows.Scan(&name, &rawArgv, &task.Timeout, &enabled); err != nil {
			return nil, fmt.Errorf("scan task for %s/%s: %w", targetID, projectID, err)
		}
		if err := json.Unmarshal([]byte(rawArgv), &task.Argv); err != nil {
			return nil, fmt.Errorf("decode task argv for %s/%s/%s: %w", targetID, projectID, name, err)
		}
		task.Enabled = enabled != 0
		out[name] = task
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tasks for %s/%s: %w", targetID, projectID, err)
	}
	return out, nil
}

func (s *Store) ListClients(ctx context.Context) ([]Client, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, display_name, provider, protocol, enabled, notes, created_at, updated_at
FROM ai_clients
ORDER BY id
`)
	if err != nil {
		return nil, fmt.Errorf("list clients: %w", err)
	}
	defer rows.Close()

	var out []Client
	for rows.Next() {
		client, err := scanClient(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, client)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate clients: %w", err)
	}
	return out, nil
}

func (s *Store) GetClient(ctx context.Context, clientID string) (Client, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, display_name, provider, protocol, enabled, notes, created_at, updated_at
FROM ai_clients
WHERE id = ?
`, clientID)
	client, err := scanClient(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Client{}, &LookupError{Code: "CLIENT_NOT_FOUND", Message: fmt.Sprintf("Client %s not found", clientID)}
	}
	if err != nil {
		return Client{}, fmt.Errorf("get client %q: %w", clientID, err)
	}
	return client, nil
}

func scanClient(row scanner) (Client, error) {
	var c Client
	var enabled int
	if err := row.Scan(
		&c.ID,
		&c.DisplayName,
		&c.Provider,
		&c.Protocol,
		&enabled,
		&c.Notes,
		&c.CreatedAt,
		&c.UpdatedAt,
	); err != nil {
		return Client{}, err
	}
	c.Enabled = enabled != 0
	return c, nil
}

func (s *Store) ListGrants(ctx context.Context, clientID string) ([]Grant, error) {
	query := `
SELECT id, client_id, target_id, project_id, capability, enabled, created_at
FROM grants
`
	var args []any
	if clientID != "" {
		query += " WHERE client_id = ? ORDER BY id"
		args = append(args, clientID)
	} else {
		query += " ORDER BY client_id, id"
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list grants: %w", err)
	}
	defer rows.Close()

	var out []Grant
	for rows.Next() {
		grant, err := scanGrant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, grant)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate grants: %w", err)
	}
	return out, nil
}

func (s *Store) GetClientGrants(ctx context.Context, clientID string) ([]Grant, error) {
	grants, err := s.ListGrants(ctx, clientID)
	if err != nil {
		return nil, err
	}
	out := grants[:0]
	for _, grant := range grants {
		if grant.Enabled {
			out = append(out, grant)
		}
	}
	return out, nil
}

func scanGrant(row scanner) (Grant, error) {
	var g Grant
	var targetID, projectID sql.NullString
	var enabled int
	if err := row.Scan(
		&g.ID,
		&g.ClientID,
		&targetID,
		&projectID,
		&g.Capability,
		&enabled,
		&g.CreatedAt,
	); err != nil {
		return Grant{}, fmt.Errorf("scan grant: %w", err)
	}
	g.TargetID = scopeFromDB(targetID)
	g.ProjectID = scopeFromDB(projectID)
	g.Enabled = enabled != 0
	return g, nil
}

func scopeFromDB(value sql.NullString) string {
	if !value.Valid {
		return "*"
	}
	return value.String
}

func ScopeToDB(value string) any {
	if strings.TrimSpace(value) == "" || strings.TrimSpace(value) == "*" {
		return nil
	}
	return strings.TrimSpace(value)
}

func (s *Store) GetSetting(ctx context.Context, key, defaultValue string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return defaultValue, nil
	}
	if err != nil {
		return "", fmt.Errorf("get setting %q: %w", key, err)
	}
	return value, nil
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO settings (key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = datetime('now')
`, key, value)
	if err != nil {
		return fmt.Errorf("set setting %q: %w", key, err)
	}
	return nil
}

func (s *Store) RecordActivity(ctx context.Context, entry Activity) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO activity (
    actor, action, target_id, project_id, duration_ms, success,
    error_code, bytes_transferred, detail
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
`,
		entry.Actor,
		entry.Action,
		entry.TargetID,
		entry.ProjectID,
		entry.DurationMS,
		boolInt(entry.Success),
		entry.ErrorCode,
		entry.BytesTransferred,
		entry.Detail,
	)
	if err != nil {
		return fmt.Errorf("record activity: %w", err)
	}
	return nil
}

func (s *Store) TargetCount(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM targets").Scan(&count); err != nil {
		return 0, fmt.Errorf("count targets: %w", err)
	}
	return count, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (s *Store) GetPrivilegeApproval(ctx context.Context, targetID string) (*PrivilegeApproval, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT target_id, policy, client_id, project_id, boot_id, approved_at
FROM privilege_approvals
WHERE target_id = ?
`, targetID)
	var a PrivilegeApproval
	if err := row.Scan(&a.TargetID, &a.Policy, &a.ClientID, &a.ProjectID, &a.BootID, &a.ApprovedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get privilege approval: %w", err)
	}
	return &a, nil
}

func (s *Store) SetPrivilegeApproval(ctx context.Context, targetID, policy, clientID, projectID, bootID string) error {
	policy = strings.ToLower(strings.TrimSpace(policy))
	clientID = strings.TrimSpace(clientID)
	projectID = strings.TrimSpace(projectID)
	if policy != "ask_always" && policy != "ask_once_per_boot" {
		return fmt.Errorf("policy '%s' does not use cached approval", policy)
	}
	if clientID == "" || projectID == "" {
		return fmt.Errorf("privilege approval requires client and project scope")
	}
	now := float64(time.Now().UnixNano()) / 1e9
	_, err := s.db.ExecContext(ctx, `
INSERT INTO privilege_approvals (target_id, policy, client_id, project_id, boot_id, approved_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(target_id) DO UPDATE SET
    policy = excluded.policy,
    client_id = excluded.client_id,
    project_id = excluded.project_id,
    boot_id = excluded.boot_id,
    approved_at = excluded.approved_at
`, targetID, policy, clientID, projectID, bootID, now)
	if err != nil {
		return fmt.Errorf("set privilege approval: %w", err)
	}
	return nil
}

func (s *Store) ClearPrivilegeApproval(ctx context.Context, targetID string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM privilege_approvals WHERE target_id = ?", targetID)
	if err != nil {
		return fmt.Errorf("clear privilege approval: %w", err)
	}
	return nil
}

func (s *Store) ClearPrivilegeApprovalsScoped(ctx context.Context, targetID, clientID, projectID string) error {
	var conditions []string
	var args []any
	if targetID != "" {
		conditions = append(conditions, "target_id = ?")
		args = append(args, targetID)
	}
	if clientID != "" {
		conditions = append(conditions, "client_id = ?")
		args = append(args, clientID)
	}
	if projectID != "" {
		conditions = append(conditions, "project_id = ?")
		args = append(args, projectID)
	}
	if len(conditions) == 0 {
		_, err := s.db.ExecContext(ctx, "DELETE FROM privilege_approvals")
		return err
	}
	query := "DELETE FROM privilege_approvals WHERE " + strings.Join(conditions, " AND ")
	_, err := s.db.ExecContext(ctx, query, args...)
	return err
}

func (s *Store) ConsumePrivilegeApproval(
	ctx context.Context,
	targetID, policy, clientID, projectID, bootID string,
	maxAgeSeconds int,
) (bool, error) {
	policy = strings.ToLower(strings.TrimSpace(policy))
	clientID = strings.TrimSpace(clientID)
	projectID = strings.TrimSpace(projectID)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	var rowPolicy, rowClient, rowProject, rowBoot string
	var approvedAt float64
	err = tx.QueryRowContext(ctx, `
SELECT policy, client_id, project_id, boot_id, approved_at
FROM privilege_approvals
WHERE target_id = ?
`, targetID).Scan(&rowPolicy, &rowClient, &rowProject, &rowBoot, &approvedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}

	if rowPolicy != policy || rowClient != clientID || rowProject != projectID {
		return false, nil
	}

	if policy == "ask_once_per_boot" {
		if bootID != "" && rowBoot == bootID {
			return true, tx.Commit()
		}
		_, _ = tx.ExecContext(ctx, "DELETE FROM privilege_approvals WHERE target_id = ?", targetID)
		_ = tx.Commit()
		return false, nil
	}

	if policy == "ask_always" {
		now := float64(time.Now().UnixNano()) / 1e9
		age := now - approvedAt
		_, _ = tx.ExecContext(ctx, "DELETE FROM privilege_approvals WHERE target_id = ?", targetID)
		if age < 0 || age > float64(maxAgeSeconds) {
			_ = tx.Commit()
			return false, nil
		}
		return true, tx.Commit()
	}

	return false, nil
}
