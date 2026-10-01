package sqliteutil

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBackupAndRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "live.db")
	backupPath := filepath.Join(dir, "backup.db")

	db, err := OpenRaw(ctx, dbPath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, "PRAGMA user_version = 5; CREATE TABLE sample(v TEXT); INSERT INTO sample(v) VALUES ('before');"); err != nil {
		t.Fatal(err)
	}

	info, err := Backup(ctx, db, backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Integrity != "ok" || info.SchemaVersion != 5 || len(info.SHA256) != 64 {
		t.Fatalf("unexpected backup info: %+v", info)
	}
	if _, err := Backup(ctx, db, backupPath); err == nil {
		t.Fatal("expected existing destination to be rejected")
	}

	if _, err := db.ExecContext(ctx, "DELETE FROM sample; INSERT INTO sample(v) VALUES ('after');"); err != nil {
		t.Fatal(err)
	}
	if err := Restore(ctx, db, backupPath); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := db.QueryRowContext(ctx, "SELECT v FROM sample").Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "before" {
		t.Fatalf("restored value=%q want before", got)
	}
}

func TestInspectRejectsNonSQLite(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "broken.db")
	if err := os.WriteFile(path, []byte("not sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(ctx, path); err == nil {
		t.Fatal("expected invalid SQLite input to fail inspection")
	}
}

func TestInspectWaitsForTransientSQLiteLock(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "locked.db")
	db, err := OpenRaw(ctx, path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, "PRAGMA user_version = 6; CREATE TABLE sample(v TEXT);"); err != nil {
		t.Fatal(err)
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN EXCLUSIVE"); err != nil {
		t.Fatal(err)
	}
	released := make(chan error, 1)
	go func() {
		time.Sleep(150 * time.Millisecond)
		_, err := conn.ExecContext(context.Background(), "COMMIT")
		released <- err
	}()

	started := time.Now()
	inspectCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	info, err := Inspect(inspectCtx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-released; err != nil {
		t.Fatal(err)
	}
	if info.Integrity != "ok" || info.SchemaVersion != 6 {
		t.Fatalf("unexpected inspection after lock release: %+v", info)
	}
	if elapsed := time.Since(started); elapsed < 100*time.Millisecond {
		t.Fatalf("inspection did not wait for transient SQLite lock: %s", elapsed)
	}
}
