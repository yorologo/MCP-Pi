package registry

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

const SchemaVersion = 6

const schemaV6 = `
CREATE TABLE targets (
    id TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    platform TEXT NOT NULL DEFAULT 'linux',
    host TEXT NOT NULL,
    port INTEGER NOT NULL DEFAULT 22,
    user TEXT NOT NULL,
    ssh_alias TEXT,
    privilege_user TEXT NOT NULL DEFAULT '',
    privilege_policy TEXT NOT NULL DEFAULT 'never',
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE projects (
    id TEXT NOT NULL,
    target_id TEXT NOT NULL REFERENCES targets(id),
    display_name TEXT NOT NULL,
    root TEXT NOT NULL,
    read_enabled INTEGER NOT NULL DEFAULT 1,
    write_enabled INTEGER NOT NULL DEFAULT 0,
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (target_id, id)
);

CREATE TABLE project_tasks (
    target_id TEXT NOT NULL,
    project_id TEXT NOT NULL,
    task_name TEXT NOT NULL,
    argv_json TEXT NOT NULL,
    timeout INTEGER NOT NULL DEFAULT 30,
    enabled INTEGER NOT NULL DEFAULT 1,
    PRIMARY KEY (target_id, project_id, task_name),
    FOREIGN KEY (target_id, project_id) REFERENCES projects(target_id, id)
);

CREATE TABLE ai_clients (
    id TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT '',
    protocol TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1,
    notes TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE grants (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    client_id TEXT NOT NULL REFERENCES ai_clients(id),
    target_id TEXT REFERENCES targets(id),
    project_id TEXT,
    capability TEXT NOT NULL DEFAULT 'read',
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (target_id, project_id) REFERENCES projects(target_id, id),
    CHECK(project_id IS NULL OR target_id IS NOT NULL),
    CHECK(length(trim(capability)) > 0 AND instr(capability, ',') = 0)
);

CREATE INDEX idx_grants_client_id ON grants(client_id);
CREATE INDEX idx_grants_target_project ON grants(target_id, project_id);
CREATE UNIQUE INDEX idx_grants_unique_scope_capability
ON grants(client_id, COALESCE(target_id, '*'), COALESCE(project_id, '*'), capability);

CREATE TABLE privilege_approvals (
    target_id TEXT PRIMARY KEY REFERENCES targets(id),
    policy TEXT NOT NULL,
    client_id TEXT NOT NULL,
    project_id TEXT NOT NULL,
    boot_id TEXT NOT NULL DEFAULT '',
    approved_at REAL NOT NULL
);

CREATE TABLE activity (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp TEXT NOT NULL DEFAULT (datetime('now')),
    actor TEXT NOT NULL DEFAULT '',
    action TEXT NOT NULL,
    target_id TEXT,
    project_id TEXT,
    duration_ms INTEGER,
    success INTEGER NOT NULL DEFAULT 1,
    error_code TEXT,
    bytes_transferred INTEGER,
    detail TEXT NOT NULL DEFAULT ''
);

CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE admin_users (
    username TEXT PRIMARY KEY,
    password_hash TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    last_login TEXT
);

PRAGMA user_version = 6;
`

const createGrantsV5 = `
CREATE TABLE grants (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    client_id TEXT NOT NULL REFERENCES ai_clients(id),
    target_id TEXT REFERENCES targets(id),
    project_id TEXT,
    capability TEXT NOT NULL DEFAULT 'read',
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (target_id, project_id) REFERENCES projects(target_id, id),
    CHECK(project_id IS NULL OR target_id IS NOT NULL)
);
CREATE INDEX idx_grants_client_id ON grants(client_id);
CREATE INDEX idx_grants_target_project ON grants(target_id, project_id);
`

const createGrantsV6 = `
CREATE TABLE grants (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    client_id TEXT NOT NULL REFERENCES ai_clients(id),
    target_id TEXT REFERENCES targets(id),
    project_id TEXT,
    capability TEXT NOT NULL DEFAULT 'read',
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (target_id, project_id) REFERENCES projects(target_id, id),
    CHECK(project_id IS NULL OR target_id IS NOT NULL),
    CHECK(length(trim(capability)) > 0 AND instr(capability, ',') = 0)
);
CREATE INDEX idx_grants_client_id ON grants(client_id);
CREATE INDEX idx_grants_target_project ON grants(target_id, project_id);
CREATE UNIQUE INDEX idx_grants_unique_scope_capability
ON grants(client_id, COALESCE(target_id, '*'), COALESCE(project_id, '*'), capability);
`

var defaultSettings = [][2]string{
	{"gateway_enabled", "true"},
	{"writes_enabled", "false"},
	{"shell_enabled", "false"},
	{"default_timeout", "30"},
	{"max_output_bytes", "262144"},
	{"max_file_read_bytes", "1048576"},
	{"max_write_bytes", "262144"},
	{"max_diff_bytes", "65536"},
	{"activity_retention", "5000"},
	{"admin_timezone", "UTC"},
}

// Open opens an existing Registry for normal runtime use. It is deliberately
// non-migrating: schema changes must happen through MigratePath while the
// lifecycle controller has stopped database users and created a rollback backup.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	return openCurrent(ctx, path, "rw")
}

// OpenReadOnly opens an existing current-schema Registry using SQLite mode=ro.
// SQLite itself enforces the read-only boundary.
func OpenReadOnly(ctx context.Context, path string) (*sql.DB, error) {
	return openCurrent(ctx, path, "ro")
}

func openCurrent(ctx context.Context, path, mode string) (*sql.DB, error) {
	db, err := openSQLite(ctx, path, mode, false)
	if err != nil {
		return nil, err
	}
	version, err := Version(ctx, db)
	if err != nil {
		db.Close()
		return nil, err
	}
	if version != SchemaVersion {
		db.Close()
		return nil, fmt.Errorf("unsupported database schema version: %d (runtime requires %d; run explicit migration)", version, SchemaVersion)
	}
	if err := checkForeignKeysConnDB(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// MigratePath is the explicit schema-creation/migration boundary. Callers are
// responsible for lifecycle coordination (backup and stopped database users).
func MigratePath(ctx context.Context, path string) error {
	db, err := openSQLite(ctx, path, "rwc", true)
	if err != nil {
		return err
	}
	defer db.Close()
	return Migrate(ctx, db)
}

func openSQLite(ctx context.Context, path, mode string, createDir bool) (*sql.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve registry path: %w", err)
	}
	if createDir {
		if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
			return nil, fmt.Errorf("create registry directory: %w", err)
		}
	}

	u := &url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
	q := u.Query()
	q.Set("mode", mode)
	q.Set("_foreign_keys", "on")
	q.Set("_busy_timeout", "5000")
	u.RawQuery = q.Encode()

	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("open registry: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping registry: %w", err)
	}
	return db, nil
}

// Version returns PRAGMA user_version.
func Version(ctx context.Context, db *sql.DB) (int, error) {
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return version, nil
}

// Migrate upgrades a supported database to SchemaVersion. Migration is
// transactional and fails closed if relational integrity is not clean.
func Migrate(ctx context.Context, db *sql.DB) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire registry connection: %w", err)
	}
	defer conn.Close()

	var version int
	if err := conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}

	switch version {
	case 0:
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin schema v6 creation: %w", err)
		}
		if _, err := tx.ExecContext(ctx, schemaV6); err != nil {
			tx.Rollback()
			return fmt.Errorf("create schema v6: %w", err)
		}
		if err := ensureDefaults(ctx, tx); err != nil {
			tx.Rollback()
			return err
		}
		if err := checkForeignKeys(ctx, tx); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit schema v6 creation: %w", err)
		}

	case 4:
		if err := migrateV4ToV6(ctx, conn); err != nil {
			return err
		}

	case 5:
		if err := migrateV5ToV6(ctx, conn); err != nil {
			return err
		}

	case SchemaVersion:
		// Already current.

	default:
		return fmt.Errorf("unsupported database schema version: %d", version)
	}

	if err := ensureDefaultsConn(ctx, conn); err != nil {
		return err
	}
	if err := checkForeignKeysConn(ctx, conn); err != nil {
		return err
	}
	return nil
}

func runMigrationTx(
	ctx context.Context,
	conn *sql.Conn,
	label string,
	steps func(*sql.Tx) error,
) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin %s migration: %w", label, err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := steps(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit %s migration: %w", label, err)
	}
	return nil
}

func migrateV4ToV5(ctx context.Context, conn *sql.Conn) error {
	return runMigrationTx(ctx, conn, "v4 to v5", func(tx *sql.Tx) error {
		return migrateV4ToV5Tx(ctx, tx)
	})
}

func migrateV4ToV6(ctx context.Context, conn *sql.Conn) error {
	return runMigrationTx(ctx, conn, "v4 to v6", func(tx *sql.Tx) error {
		if err := migrateV4ToV5Tx(ctx, tx); err != nil {
			return err
		}
		return migrateV5ToV6Tx(ctx, tx)
	})
}

func migrateV4ToV5Tx(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, "ALTER TABLE grants RENAME TO grants_v4"); err != nil {
		return fmt.Errorf("rename v4 grants: %w", err)
	}
	if _, err := tx.ExecContext(ctx, createGrantsV5); err != nil {
		return fmt.Errorf("create v5 grants: %w", err)
	}

	const copySQL = `
INSERT INTO grants (id, client_id, target_id, project_id, capability, enabled, created_at)
SELECT
    id,
    client_id,
    CASE WHEN target_id = '*' THEN NULL ELSE target_id END,
    CASE WHEN project_id = '*' THEN NULL ELSE project_id END,
    capability,
    enabled,
    created_at
FROM grants_v4
ORDER BY id
`
	if _, err := tx.ExecContext(ctx, copySQL); err != nil {
		return fmt.Errorf("copy v4 grants into v5: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DROP TABLE grants_v4"); err != nil {
		return fmt.Errorf("drop v4 grants: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "PRAGMA user_version = 5"); err != nil {
		return fmt.Errorf("set schema version 5: %w", err)
	}
	if err := ensureDefaults(ctx, tx); err != nil {
		return err
	}
	if err := checkForeignKeys(ctx, tx); err != nil {
		return err
	}
	return nil
}

type grantV6MigrationKey struct {
	ClientID     string
	TargetValid  bool
	TargetID     string
	ProjectValid bool
	ProjectID    string
	Capability   string
}

type grantV6MigrationRow struct {
	ID         int64
	ClientID   string
	TargetID   sql.NullString
	ProjectID  sql.NullString
	Capability string
	Enabled    bool
	CreatedAt  string
}

func migrateV5ToV6(ctx context.Context, conn *sql.Conn) error {
	return runMigrationTx(ctx, conn, "v5 to v6", func(tx *sql.Tx) error {
		return migrateV5ToV6Tx(ctx, tx)
	})
}

func migrateV5ToV6Tx(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, "ALTER TABLE grants RENAME TO grants_v5"); err != nil {
		return fmt.Errorf("rename v5 grants: %w", err)
	}
	for _, indexName := range []string{"idx_grants_client_id", "idx_grants_target_project"} {
		if _, err := tx.ExecContext(ctx, "DROP INDEX IF EXISTS "+indexName); err != nil {
			return fmt.Errorf("drop v5 grant index %s: %w", indexName, err)
		}
	}

	rows, err := tx.QueryContext(ctx, "SELECT id, client_id, target_id, project_id, capability, enabled, created_at FROM grants_v5 ORDER BY id")
	if err != nil {
		return fmt.Errorf("read v5 grants: %w", err)
	}

	byKey := make(map[grantV6MigrationKey]*grantV6MigrationRow)
	var normalized []*grantV6MigrationRow
	for rows.Next() {
		var (
			id         int64
			clientID   string
			targetID   sql.NullString
			projectID  sql.NullString
			capability string
			enabled    int
			createdAt  string
		)
		if err := rows.Scan(&id, &clientID, &targetID, &projectID, &capability, &enabled, &createdAt); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan v5 grant: %w", err)
		}

		parts := strings.Split(capability, ",")
		added := 0
		for _, part := range parts {
			capabilityPart := strings.TrimSpace(part)
			if capabilityPart == "" {
				continue
			}
			key := grantV6MigrationKey{
				ClientID:     clientID,
				TargetValid:  targetID.Valid,
				TargetID:     targetID.String,
				ProjectValid: projectID.Valid,
				ProjectID:    projectID.String,
				Capability:   capabilityPart,
			}
			if existing := byKey[key]; existing != nil {
				existing.Enabled = existing.Enabled || enabled != 0
				if createdAt < existing.CreatedAt {
					existing.CreatedAt = createdAt
				}
				added++
				continue
			}

			migratedID := int64(0)
			if added == 0 {
				migratedID = id
			}
			row := &grantV6MigrationRow{
				ID:         migratedID,
				ClientID:   clientID,
				TargetID:   targetID,
				ProjectID:  projectID,
				Capability: capabilityPart,
				Enabled:    enabled != 0,
				CreatedAt:  createdAt,
			}
			byKey[key] = row
			normalized = append(normalized, row)
			added++
		}
		if added == 0 {
			_ = rows.Close()
			return fmt.Errorf("grant %d has no usable capability after normalization", id)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate v5 grants: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close v5 grant rows: %w", err)
	}

	if _, err := tx.ExecContext(ctx, createGrantsV6); err != nil {
		return fmt.Errorf("create v6 grants: %w", err)
	}

	for _, row := range normalized {
		if row.ID == 0 {
			continue
		}
		if _, err := tx.ExecContext(
			ctx,
			"INSERT INTO grants(id,client_id,target_id,project_id,capability,enabled,created_at) VALUES(?,?,?,?,?,?,?)",
			row.ID, row.ClientID, nullableStringValue(row.TargetID), nullableStringValue(row.ProjectID),
			row.Capability, boolInt(row.Enabled), row.CreatedAt,
		); err != nil {
			return fmt.Errorf("insert normalized v6 grant %d: %w", row.ID, err)
		}
	}
	for _, row := range normalized {
		if row.ID != 0 {
			continue
		}
		if _, err := tx.ExecContext(
			ctx,
			"INSERT INTO grants(client_id,target_id,project_id,capability,enabled,created_at) VALUES(?,?,?,?,?,?)",
			row.ClientID, nullableStringValue(row.TargetID), nullableStringValue(row.ProjectID),
			row.Capability, boolInt(row.Enabled), row.CreatedAt,
		); err != nil {
			return fmt.Errorf("insert split v6 grant: %w", err)
		}
	}

	if _, err := tx.ExecContext(ctx, "DROP TABLE grants_v5"); err != nil {
		return fmt.Errorf("drop v5 grants: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "PRAGMA user_version = 6"); err != nil {
		return fmt.Errorf("set schema version 6: %w", err)
	}
	if err := ensureDefaults(ctx, tx); err != nil {
		return err
	}
	if err := checkForeignKeys(ctx, tx); err != nil {
		return err
	}
	return nil
}

func nullableStringValue(value sql.NullString) any {
	if !value.Valid {
		return nil
	}
	return value.String
}

type sqlExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func ensureDefaults(ctx context.Context, exec sqlExecutor) error {
	for _, item := range defaultSettings {
		if _, err := exec.ExecContext(
			ctx,
			"INSERT OR IGNORE INTO settings (key, value) VALUES (?, ?)",
			item[0],
			item[1],
		); err != nil {
			return fmt.Errorf("ensure default setting %q: %w", item[0], err)
		}
	}
	return nil
}

func ensureDefaultsConn(ctx context.Context, conn *sql.Conn) error {
	return ensureDefaults(ctx, conn)
}

type rowQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func checkForeignKeys(ctx context.Context, q rowQueryer) error {
	rows, err := q.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("foreign_key_check: %w", err)
	}
	defer rows.Close()

	var violations []string
	for rows.Next() {
		var table, parent string
		var rowID sql.NullInt64
		var fkID int64
		if err := rows.Scan(&table, &rowID, &parent, &fkID); err != nil {
			return fmt.Errorf("scan foreign_key_check: %w", err)
		}
		row := "NULL"
		if rowID.Valid {
			row = fmt.Sprintf("%d", rowID.Int64)
		}
		violations = append(violations, fmt.Sprintf("%s[rowid=%s]->%s[fk=%d]", table, row, parent, fkID))
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("foreign_key_check rows: %w", err)
	}
	if len(violations) != 0 {
		return fmt.Errorf("foreign key violations: %s", strings.Join(violations, ", "))
	}
	return nil
}

func checkForeignKeysConn(ctx context.Context, conn *sql.Conn) error {
	return checkForeignKeys(ctx, conn)
}

func checkForeignKeysConnDB(ctx context.Context, db *sql.DB) error {
	return checkForeignKeys(ctx, db)
}

// IsUnsupportedVersion reports whether err represents a deliberate refusal to
// operate on a schema version this runtime does not know how to migrate.
func IsUnsupportedVersion(err error) bool {
	return err != nil && strings.Contains(err.Error(), "unsupported database schema version")
}
