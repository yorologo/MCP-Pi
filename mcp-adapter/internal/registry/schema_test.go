package registry

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

const schemaV4Fixture = `
PRAGMA foreign_keys = OFF;
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
    target_id TEXT NOT NULL REFERENCES targets(id),
    project_id TEXT,
    capability TEXT NOT NULL DEFAULT 'read',
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
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
PRAGMA user_version = 4;
`

func rawV4(t *testing.T, invalidProjectScope bool) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gateway.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.Exec(schemaV4Fixture); err != nil {
		t.Fatalf("create v4 fixture: %v", err)
	}
	stmts := []string{
		`INSERT INTO targets(id, display_name, host, user) VALUES ('termux-main','Termux','127.0.0.1','u0_a435')`,
		`INSERT INTO projects(id,target_id,display_name,root,write_enabled) VALUES ('MCP_Local','termux-main','MCP Local','/tmp/project',1)`,
		`INSERT INTO ai_clients(id,display_name,enabled) VALUES ('client-a','Client A',1)`,
		`INSERT INTO settings(key,value) VALUES ('custom_setting','preserve-me')`,
		`INSERT INTO activity(actor,action,success,detail) VALUES ('tester','fixture',1,'preserve-me')`,
		`INSERT INTO admin_users(username,password_hash,enabled) VALUES ('admin','fixture-hash',1)`,
		`INSERT INTO grants(client_id,target_id,project_id,capability,enabled) VALUES ('client-a','*','*','read',1)`,
		`INSERT INTO grants(client_id,target_id,project_id,capability,enabled) VALUES ('client-a','termux-main','*','target_shell',1)`,
		`INSERT INTO grants(client_id,target_id,project_id,capability,enabled) VALUES ('client-a','termux-main','MCP_Local','write',0)`,
	}
	if invalidProjectScope {
		stmts = append(stmts,
			`INSERT INTO grants(client_id,target_id,project_id,capability,enabled) VALUES ('client-a','*','MCP_Local','read',1)`)
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("seed v4 fixture: %v", err)
		}
	}
	return path
}

func TestOpenCreatesSchemaV5WithConservativeSettings(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "new.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	version, err := Version(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if version != SchemaVersion {
		t.Fatalf("version=%d want=%d", version, SchemaVersion)
	}
	if got := db.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("MaxOpenConnections=%d want=1", got)
	}

	var fk int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if fk != 1 {
		t.Fatalf("foreign_keys=%d want=1", fk)
	}

	var journal string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil {
		t.Fatal(err)
	}
	if journal != "delete" {
		t.Fatalf("journal_mode=%q want default delete; Open must not force WAL", journal)
	}

	var writes string
	if err := db.QueryRow("SELECT value FROM settings WHERE key='writes_enabled'").Scan(&writes); err != nil {
		t.Fatal(err)
	}
	if writes != "false" {
		t.Fatalf("writes_enabled=%q want=false", writes)
	}
}

func TestMigrateV4ToV5PreservesRowsAndConvertsWildcardScopes(t *testing.T) {
	ctx := context.Background()
	path := rawV4(t, false)

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	version, err := Version(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if version != 5 {
		t.Fatalf("version=%d want=5", version)
	}

	type grant struct {
		id      int
		target  sql.NullString
		project sql.NullString
		cap     string
		enabled int
	}
	rows, err := db.Query("SELECT id,target_id,project_id,capability,enabled FROM grants ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var got []grant
	for rows.Next() {
		var g grant
		if err := rows.Scan(&g.id, &g.target, &g.project, &g.cap, &g.enabled); err != nil {
			t.Fatal(err)
		}
		got = append(got, g)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("grant count=%d want=3", len(got))
	}
	if got[0].target.Valid || got[0].project.Valid {
		t.Fatalf("global wildcard not converted to NULL: %+v", got[0])
	}
	if !got[1].target.Valid || got[1].target.String != "termux-main" || got[1].project.Valid {
		t.Fatalf("target wildcard project conversion wrong: %+v", got[1])
	}
	if got[2].enabled != 0 || !got[2].project.Valid || got[2].project.String != "MCP_Local" {
		t.Fatalf("disabled project grant not preserved: %+v", got[2])
	}

	for _, tc := range []struct {
		query string
		want  string
	}{
		{"SELECT value FROM settings WHERE key='custom_setting'", "preserve-me"},
		{"SELECT detail FROM activity WHERE action='fixture'", "preserve-me"},
		{"SELECT password_hash FROM admin_users WHERE username='admin'", "fixture-hash"},
	} {
		var value string
		if err := db.QueryRow(tc.query).Scan(&value); err != nil {
			t.Fatal(err)
		}
		if value != tc.want {
			t.Fatalf("%s=%q want=%q", tc.query, value, tc.want)
		}
	}

	var violations int
	if err := db.QueryRow("SELECT count(*) FROM pragma_foreign_key_check").Scan(&violations); err != nil {
		t.Fatal(err)
	}
	if violations != 0 {
		t.Fatalf("foreign_key_check violations=%d want=0", violations)
	}

	res, err := db.Exec(`INSERT INTO grants(client_id,target_id,project_id,capability) VALUES ('client-a',NULL,NULL,'read')`)
	if err != nil {
		t.Fatalf("insert global v5 grant: %v", err)
	}
	newID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if newID <= int64(got[len(got)-1].id) {
		t.Fatalf("AUTOINCREMENT did not advance: new=%d last_old=%d", newID, got[len(got)-1].id)
	}

	if _, err := db.Exec(`INSERT INTO grants(client_id,target_id,project_id,capability) VALUES ('client-a','termux-main','missing','read')`); err == nil {
		t.Fatal("missing project scope unexpectedly accepted")
	}
}

func TestMigrateV4FailsClosedAndRollsBackInvalidWildcardProjectScope(t *testing.T) {
	ctx := context.Background()
	path := rawV4(t, true)

	if db, err := Open(ctx, path); err == nil {
		db.Close()
		t.Fatal("migration unexpectedly accepted target wildcard with specific project")
	}

	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()

	var version int
	if err := raw.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 4 {
		t.Fatalf("failed migration changed version to %d want=4", version)
	}

	var grantsTable, renamedTable int
	if err := raw.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='grants'").Scan(&grantsTable); err != nil {
		t.Fatal(err)
	}
	if err := raw.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='grants_v4'").Scan(&renamedTable); err != nil {
		t.Fatal(err)
	}
	if grantsTable != 1 || renamedTable != 0 {
		t.Fatalf("rollback incomplete: grants=%d grants_v4=%d", grantsTable, renamedTable)
	}
}

func TestOpenRejectsUnknownSchemaVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "future.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec("PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	db, err := Open(context.Background(), path)
	if db != nil {
		db.Close()
	}
	if err == nil || !IsUnsupportedVersion(err) {
		t.Fatalf("Open error=%v want unsupported-version failure", err)
	}
}
