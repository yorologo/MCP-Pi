//go:build !windows

package remote

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestReadFileRejectsNonRegularFileBeforeReading(t *testing.T) {
	transport := localSSHShim(t)
	fifo := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := transport.ReadFile(ctx, localTarget(), fifo, 1024, 500*time.Millisecond)
	if ErrorCode(err) != "INVALID_PATH" {
		t.Fatalf("FIFO read err=%v code=%q want INVALID_PATH", err, ErrorCode(err))
	}
}
