package remote

import (
	"context"
	"fmt"
	"time"

	"mcp-gateway-adapter/internal/registry"
)

type Error struct {
	Code     string
	Message  string
	ExitCode int
}

func (e *Error) Error() string { return e.Message }

func NewError(code, message string, exitCode int) error {
	return &Error{Code: code, Message: message, ExitCode: exitCode}
}

func ErrorCode(err error) string {
	if e, ok := err.(*Error); ok {
		return e.Code
	}
	return ""
}

type CommandOptions struct {
	CWD     string
	Stdin   []byte
	Timeout time.Duration
}

type CommandResult struct {
	ExitCode   int
	Stdout     string
	Stderr     string
	DurationMS int64
}

func (r CommandResult) OK() bool { return r.ExitCode == 0 }

type DirectoryEntry struct {
	Name string
	Type string
	Size *int64
}

type FileStat struct {
	Exists bool
	Type   string
	Size   int64
	MTime  int64
}

type SearchMatch struct {
	File string
	Line int
	Text string
}

// Transport is the controlled remote execution boundary used by Go Core.
// Production uses system OpenSSH; tests use deterministic fakes.
type Transport interface {
	RunCommand(ctx context.Context, target registry.Target, command string, options CommandOptions) (CommandResult, error)
	ProbeFacts(ctx context.Context, target registry.Target, includeBootID bool, timeout time.Duration) (map[string]any, error)
	ResolveCanonicalPath(ctx context.Context, target registry.Target, candidatePath string, timeout time.Duration) (string, error)
	ListDirectory(ctx context.Context, target registry.Target, canonicalPath string, limit int, timeout time.Duration) ([]DirectoryEntry, error)
	FileStat(ctx context.Context, target registry.Target, canonicalPath string, timeout time.Duration) (FileStat, error)
	ReadFile(ctx context.Context, target registry.Target, canonicalPath string, maxBytes int64, timeout time.Duration) (string, error)
	GitStatus(ctx context.Context, target registry.Target, canonicalRoot string, timeout time.Duration) (string, error)
	Search(ctx context.Context, target registry.Target, projectRoot, searchRoot, pattern string, isRegex bool, limit int, timeout time.Duration) ([]SearchMatch, error)
}

func Required(t Transport) (Transport, error) {
	if t == nil {
		return nil, fmt.Errorf("remote transport is not configured")
	}
	return t, nil
}
