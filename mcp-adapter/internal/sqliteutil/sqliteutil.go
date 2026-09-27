package sqliteutil

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	msqlite "modernc.org/sqlite"
)

type backuper interface {
	NewBackup(string) (*msqlite.Backup, error)
	NewRestore(string) (*msqlite.Backup, error)
}

func OpenRaw(ctx context.Context, path string, create bool) (*sql.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve database path: %w", err)
	}
	if create {
		if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}
	u := &url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
	q := u.Query()
	if !create {
		q.Set("mode", "rw")
	}
	q.Set("_busy_timeout", "5000")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return db, nil
}

type FileInfo struct {
	Integrity     string
	SchemaVersion int
	SizeBytes     int64
	SHA256        string
}

func runBackup(ctx context.Context, db *sql.DB, path string, restore bool) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Raw(func(driverConn any) error {
		b, ok := driverConn.(backuper)
		if !ok {
			return fmt.Errorf("sqlite driver does not expose backup API")
		}
		var op *msqlite.Backup
		if restore {
			op, err = b.NewRestore(path)
		} else {
			op, err = b.NewBackup(path)
		}
		if err != nil {
			return err
		}
		for more := true; more; {
			more, err = op.Step(-1)
			if err != nil {
				_ = op.Finish()
				return err
			}
		}
		return op.Finish()
	})
}

func Backup(ctx context.Context, db *sql.DB, destination string) (FileInfo, error) {
	if db == nil {
		return FileInfo{}, fmt.Errorf("database is nil")
	}
	destination = filepath.Clean(strings.TrimSpace(destination))
	if destination == "." || destination == "" {
		return FileInfo{}, fmt.Errorf("backup destination is required")
	}
	if _, err := os.Stat(destination); err == nil {
		return FileInfo{}, fmt.Errorf("backup destination already exists: %s", destination)
	} else if !os.IsNotExist(err) {
		return FileInfo{}, err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return FileInfo{}, err
	}
	if err := runBackup(ctx, db, destination, false); err != nil {
		_ = os.Remove(destination)
		return FileInfo{}, err
	}
	if err := os.Chmod(destination, 0o600); err != nil {
		return FileInfo{}, err
	}
	return Inspect(ctx, destination)
}

func Restore(ctx context.Context, db *sql.DB, source string) error {
	if db == nil {
		return fmt.Errorf("database is nil")
	}
	info, err := Inspect(ctx, source)
	if err != nil {
		return err
	}
	if info.Integrity != "ok" {
		return fmt.Errorf("source integrity check failed: %s", info.Integrity)
	}
	return runBackup(ctx, db, source, true)
}

func Inspect(ctx context.Context, path string) (FileInfo, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "." || path == "" {
		return FileInfo{}, fmt.Errorf("database path is required")
	}
	f, err := os.Open(path)
	if err != nil {
		return FileInfo{}, err
	}
	h := sha256.New()
	size, err := io.Copy(h, f)
	closeErr := f.Close()
	if err != nil {
		return FileInfo{}, err
	}
	if closeErr != nil {
		return FileInfo{}, closeErr
	}

	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		return FileInfo{}, err
	}
	defer db.Close()
	var integrity string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check;").Scan(&integrity); err != nil {
		return FileInfo{}, err
	}
	var schemaVersion int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version;").Scan(&schemaVersion); err != nil {
		return FileInfo{}, err
	}
	return FileInfo{
		Integrity:     integrity,
		SchemaVersion: schemaVersion,
		SizeBytes:     size,
		SHA256:        hex.EncodeToString(h.Sum(nil)),
	}, nil
}
