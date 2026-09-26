package registry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type AdminUser struct {
	Username     string
	PasswordHash string
	Enabled      bool
	CreatedAt    string
	LastLogin    *string
}

func (s *Store) AddTarget(ctx context.Context, target Target) error {
	id := strings.TrimSpace(target.ID)
	if id == "" {
		return fmt.Errorf("target id cannot be empty")
	}
	policy := strings.ToLower(strings.TrimSpace(target.PrivilegePolicy))
	if policy == "" {
		policy = "never"
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO targets (id, display_name, platform, host, port, user, ssh_alias, privilege_user, privilege_policy, enabled)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`,
		id,
		nonEmpty(target.DisplayName, id),
		nonEmpty(target.Platform, "linux"),
		nonEmpty(target.Host, "127.0.0.1"),
		target.Port,
		target.User,
		target.SSHAlias,
		target.PrivilegeUser,
		policy,
		boolInt(target.Enabled),
	)
	if err != nil {
		return fmt.Errorf("add target: %w", err)
	}
	return nil
}

func (s *Store) UpdateTarget(ctx context.Context, target Target) error {
	id := strings.TrimSpace(target.ID)
	if id == "" {
		return fmt.Errorf("target id cannot be empty")
	}
	policy := strings.ToLower(strings.TrimSpace(target.PrivilegePolicy))
	if policy == "" {
		policy = "never"
	}
	res, err := s.db.ExecContext(ctx, `
UPDATE targets
SET display_name = ?, platform = ?, host = ?, port = ?, user = ?, ssh_alias = ?, privilege_user = ?, privilege_policy = ?, enabled = ?
WHERE id = ?
`,
		nonEmpty(target.DisplayName, id),
		nonEmpty(target.Platform, "linux"),
		nonEmpty(target.Host, "127.0.0.1"),
		target.Port,
		target.User,
		target.SSHAlias,
		target.PrivilegeUser,
		policy,
		boolInt(target.Enabled),
		id,
	)
	if err != nil {
		return fmt.Errorf("update target: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return &LookupError{Code: "UNKNOWN_TARGET", Message: fmt.Sprintf("target '%s' not found", id)}
	}
	// Revoke cached privilege approval if target changes
	_ = s.ClearPrivilegeApproval(ctx, id)
	return nil
}

func (s *Store) DeleteTarget(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	res, err := s.db.ExecContext(ctx, "DELETE FROM targets WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete target: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return &LookupError{Code: "UNKNOWN_TARGET", Message: fmt.Sprintf("target '%s' not found", id)}
	}
	_ = s.ClearPrivilegeApproval(ctx, id)
	return nil
}

func (s *Store) AddProject(ctx context.Context, project Project) error {
	id := strings.TrimSpace(project.ID)
	targetID := strings.TrimSpace(project.TargetID)
	if id == "" || targetID == "" {
		return fmt.Errorf("project id and target id cannot be empty")
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO projects (id, target_id, display_name, root, read_enabled, write_enabled, enabled)
VALUES (?, ?, ?, ?, ?, ?, ?)
`,
		id,
		targetID,
		nonEmpty(project.DisplayName, id),
		project.Root,
		boolInt(project.Read),
		boolInt(project.Write),
		boolInt(project.Enabled),
	)
	if err != nil {
		return fmt.Errorf("add project: %w", err)
	}
	return nil
}

func (s *Store) UpdateProject(ctx context.Context, project Project) error {
	id := strings.TrimSpace(project.ID)
	targetID := strings.TrimSpace(project.TargetID)
	if id == "" || targetID == "" {
		return fmt.Errorf("project id and target id cannot be empty")
	}
	res, err := s.db.ExecContext(ctx, `
UPDATE projects
SET display_name = ?, root = ?, read_enabled = ?, write_enabled = ?, enabled = ?
WHERE id = ? AND target_id = ?
`,
		nonEmpty(project.DisplayName, id),
		project.Root,
		boolInt(project.Read),
		boolInt(project.Write),
		boolInt(project.Enabled),
		id,
		targetID,
	)
	if err != nil {
		return fmt.Errorf("update project: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return &LookupError{Code: "UNKNOWN_PROJECT", Message: fmt.Sprintf("project '%s' not found in target '%s'", id, targetID)}
	}
	_ = s.ClearPrivilegeApprovalsScoped(ctx, targetID, "", id)
	return nil
}

func (s *Store) DeleteProject(ctx context.Context, targetID, projectID string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM projects WHERE target_id = ? AND id = ?", targetID, projectID)
	if err != nil {
		return fmt.Errorf("delete project: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return &LookupError{Code: "UNKNOWN_PROJECT", Message: fmt.Sprintf("project '%s' not found in target '%s'", projectID, targetID)}
	}
	_ = s.ClearPrivilegeApprovalsScoped(ctx, targetID, "", projectID)
	return nil
}

func (s *Store) AddClient(ctx context.Context, client Client) error {
	id := strings.TrimSpace(client.ID)
	if id == "" {
		return fmt.Errorf("client id cannot be empty")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO ai_clients (id, display_name, provider, protocol, enabled, notes, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
`,
		id,
		nonEmpty(client.DisplayName, id),
		client.Provider,
		client.Protocol,
		boolInt(client.Enabled),
		client.Notes,
		now,
		now,
	)
	if err != nil {
		return fmt.Errorf("add client: %w", err)
	}
	return nil
}

func (s *Store) UpdateClient(ctx context.Context, client Client) error {
	id := strings.TrimSpace(client.ID)
	if id == "" {
		return fmt.Errorf("client id cannot be empty")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx, `
UPDATE ai_clients
SET display_name = ?, provider = ?, protocol = ?, enabled = ?, notes = ?, updated_at = ?
WHERE id = ?
`,
		nonEmpty(client.DisplayName, id),
		client.Provider,
		client.Protocol,
		boolInt(client.Enabled),
		client.Notes,
		now,
		id,
	)
	if err != nil {
		return fmt.Errorf("update client: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return &LookupError{Code: "UNKNOWN_CLIENT", Message: fmt.Sprintf("client '%s' not found", id)}
	}
	if !client.Enabled {
		_ = s.ClearPrivilegeApprovalsScoped(ctx, "", id, "")
	}
	return nil
}

func (s *Store) DeleteClient(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM ai_clients WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete client: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return &LookupError{Code: "UNKNOWN_CLIENT", Message: fmt.Sprintf("client '%s' not found", id)}
	}
	_ = s.ClearPrivilegeApprovalsScoped(ctx, "", id, "")
	return nil
}

func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func (s *Store) GetGrant(ctx context.Context, grantID int64) (Grant, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, client_id, target_id, project_id, capability, enabled, created_at
FROM grants
WHERE id = ?
`, grantID)
	var g Grant
	var targetID, projectID sql.NullString
	var enabled int
	if err := row.Scan(&g.ID, &g.ClientID, &targetID, &projectID, &g.Capability, &enabled, &g.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Grant{}, &LookupError{Code: "UNKNOWN_GRANT", Message: fmt.Sprintf("grant %d not found", grantID)}
		}
		return Grant{}, err
	}
	g.Enabled = enabled == 1
	g.TargetID = "*"
	if targetID.Valid && targetID.String != "" {
		g.TargetID = targetID.String
	}
	g.ProjectID = "*"
	if projectID.Valid && projectID.String != "" {
		g.ProjectID = projectID.String
	}
	return g, nil
}

func (s *Store) AddGrant(ctx context.Context, grant Grant) (int64, error) {
	clientID := strings.TrimSpace(grant.ClientID)
	capability := strings.TrimSpace(grant.Capability)
	if clientID == "" || capability == "" {
		return 0, fmt.Errorf("client id and capability cannot be empty")
	}

	var targetVal any = nil
	if grant.TargetID != "" && grant.TargetID != "*" {
		targetVal = grant.TargetID
	}
	var projectVal any = nil
	if grant.ProjectID != "" && grant.ProjectID != "*" {
		projectVal = grant.ProjectID
	}

	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx, `
INSERT INTO grants (client_id, target_id, project_id, capability, enabled, created_at)
VALUES (?, ?, ?, ?, ?, ?)
`, clientID, targetVal, projectVal, capability, boolInt(grant.Enabled), now)
	if err != nil {
		return 0, fmt.Errorf("add grant: %w", err)
	}
	return res.LastInsertId()
}

func (s *Store) UpdateGrant(ctx context.Context, grant Grant) error {
	var targetVal any = nil
	if grant.TargetID != "" && grant.TargetID != "*" {
		targetVal = grant.TargetID
	}
	var projectVal any = nil
	if grant.ProjectID != "" && grant.ProjectID != "*" {
		projectVal = grant.ProjectID
	}

	res, err := s.db.ExecContext(ctx, `
UPDATE grants
SET target_id = ?, project_id = ?, capability = ?, enabled = ?
WHERE id = ?
`, targetVal, projectVal, grant.Capability, boolInt(grant.Enabled), grant.ID)
	if err != nil {
		return fmt.Errorf("update grant: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return &LookupError{Code: "UNKNOWN_GRANT", Message: fmt.Sprintf("grant %d not found", grant.ID)}
	}
	return nil
}

func (s *Store) DeleteGrant(ctx context.Context, grantID int64) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM grants WHERE id = ?", grantID)
	if err != nil {
		return fmt.Errorf("delete grant: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return &LookupError{Code: "UNKNOWN_GRANT", Message: fmt.Sprintf("grant %d not found", grantID)}
	}
	return nil
}

func (s *Store) GetAdminUser(ctx context.Context, username string) (*AdminUser, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT username, password_hash, enabled, created_at, last_login
FROM admin_users
WHERE username = ?
`, username)
	var u AdminUser
	var enabled int
	var lastLogin sql.NullString
	if err := row.Scan(&u.Username, &u.PasswordHash, &enabled, &u.CreatedAt, &lastLogin); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get admin user: %w", err)
	}
	u.Enabled = enabled == 1
	if lastLogin.Valid {
		u.LastLogin = &lastLogin.String
	}
	return &u, nil
}

func (s *Store) SetAdminPassword(ctx context.Context, username, passwordHash string) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return fmt.Errorf("username cannot be empty")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO admin_users (username, password_hash, enabled, created_at)
VALUES (?, ?, 1, ?)
ON CONFLICT(username) DO UPDATE SET password_hash = excluded.password_hash
`, username, passwordHash, now)
	if err != nil {
		return fmt.Errorf("set admin password: %w", err)
	}
	return nil
}

func (s *Store) UpdateAdminLogin(ctx context.Context, username string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, "UPDATE admin_users SET last_login = ? WHERE username = ?", now, username)
	return err
}

func (s *Store) ListAdminUsers(ctx context.Context) ([]AdminUser, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT username, password_hash, enabled, created_at, last_login FROM admin_users ORDER BY username")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []AdminUser
	for rows.Next() {
		var u AdminUser
		var enabled int
		var lastLogin sql.NullString
		if err := rows.Scan(&u.Username, &u.PasswordHash, &enabled, &u.CreatedAt, &lastLogin); err != nil {
			return nil, err
		}
		u.Enabled = enabled == 1
		if lastLogin.Valid {
			u.LastLogin = &lastLogin.String
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *Store) GetActivityCount(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM activity").Scan(&count); err != nil {
		return 0, fmt.Errorf("get activity count: %w", err)
	}
	return count, nil
}

func (s *Store) PruneActivity(ctx context.Context, keep int) (int64, error) {
	if keep <= 0 {
		keep = 1000
	}
	res, err := s.db.ExecContext(ctx, `
DELETE FROM activity
WHERE id NOT IN (
    SELECT id FROM activity ORDER BY id DESC LIMIT ?
)
`, keep)
	if err != nil {
		return 0, fmt.Errorf("prune activity: %w", err)
	}
	return res.RowsAffected()
}

type ActivityEvent struct {
	ID               int64  `json:"id"`
	Timestamp        string `json:"timestamp"`
	Actor            string `json:"actor"`
	Action           string `json:"action"`
	TargetID         string `json:"target_id"`
	ProjectID        string `json:"project_id"`
	DurationMS       int64  `json:"duration_ms"`
	Success          bool   `json:"success"`
	ErrorCode        string `json:"error_code"`
	BytesTransferred int64  `json:"bytes_transferred"`
	Detail           string `json:"detail"`
}

type ActivityFilter struct {
	Actor     string
	Action    string
	TargetID  string
	ProjectID string
	Result    string
	From      string
	To        string
}

func buildActivityWhere(filter ActivityFilter) (string, []interface{}) {
	var conds []string
	var args []interface{}
	if filter.Actor != "" {
		conds = append(conds, "actor LIKE ?")
		args = append(args, "%"+filter.Actor+"%")
	}
	if filter.Action != "" {
		conds = append(conds, "action LIKE ?")
		args = append(args, "%"+filter.Action+"%")
	}
	if filter.TargetID != "" {
		conds = append(conds, "target_id = ?")
		args = append(args, filter.TargetID)
	}
	if filter.ProjectID != "" {
		conds = append(conds, "project_id = ?")
		args = append(args, filter.ProjectID)
	}
	if filter.Result == "PASS" {
		conds = append(conds, "success = 1")
	} else if filter.Result == "DENY" {
		conds = append(conds, "success = 0")
	}
	if filter.From != "" {
		conds = append(conds, "timestamp >= ?")
		args = append(args, filter.From)
	}
	if filter.To != "" {
		conds = append(conds, "timestamp <= ?")
		args = append(args, filter.To)
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

func (s *Store) ListActivity(ctx context.Context, limit, offset int, filter ActivityFilter) ([]ActivityEvent, error) {
	where, args := buildActivityWhere(filter)
	q := "SELECT id, timestamp, actor, action, COALESCE(target_id, ''), COALESCE(project_id, ''), COALESCE(duration_ms, 0), success, COALESCE(error_code, ''), COALESCE(bytes_transferred, 0), COALESCE(detail, '') FROM activity" + where + " ORDER BY id DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list activity: %w", err)
	}
	defer rows.Close()

	var events []ActivityEvent
	for rows.Next() {
		var e ActivityEvent
		var succ int
		if err := rows.Scan(&e.ID, &e.Timestamp, &e.Actor, &e.Action, &e.TargetID, &e.ProjectID, &e.DurationMS, &succ, &e.ErrorCode, &e.BytesTransferred, &e.Detail); err != nil {
			return nil, fmt.Errorf("scan activity: %w", err)
		}
		e.Success = succ != 0
		events = append(events, e)
	}
	return events, rows.Err()
}

func (s *Store) GetActivityCountFiltered(ctx context.Context, filter ActivityFilter) (int, error) {
	where, args := buildActivityWhere(filter)
	q := "SELECT COUNT(*) FROM activity" + where
	var count int
	if err := s.db.QueryRowContext(ctx, q, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("get filtered activity count: %w", err)
	}
	return count, nil
}
