package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"mcp-gateway-adapter/internal/registry"
	"mcp-gateway-adapter/internal/remote"
)

type fakeRemote struct {
	lastCanonicalCandidate string
	lastReadLimit          int64
}

func (f *fakeRemote) RunCommand(_ context.Context, _ registry.Target, command string, _ remote.CommandOptions) (remote.CommandResult, error) {
	if command == "hostname" {
		return remote.CommandResult{ExitCode: 0, Stdout: "remote-box\n", DurationMS: 12}, nil
	}
	return remote.CommandResult{ExitCode: 1, Stderr: "unexpected command"}, nil
}

func (f *fakeRemote) ProbeFacts(_ context.Context, _ registry.Target, _ bool, _ time.Duration) (map[string]any, error) {
	return map[string]any{"probe_status": "ok", "arch": "armv6l"}, nil
}

func (f *fakeRemote) ResolveCanonicalPath(_ context.Context, _ registry.Target, candidatePath string, _ time.Duration) (string, error) {
	f.lastCanonicalCandidate = candidatePath
	if strings.Contains(candidatePath, "escape") {
		return "/outside/escape", nil
	}
	return candidatePath, nil
}

func (f *fakeRemote) ListDirectory(_ context.Context, _ registry.Target, canonicalPath string, _ int, _ time.Duration) ([]remote.DirectoryEntry, error) {
	if strings.HasSuffix(canonicalPath, "/missing") || strings.HasSuffix(canonicalPath, "\\missing") {
		return nil, remote.NewError("NOT_FOUND", "", 2)
	}
	if strings.HasSuffix(canonicalPath, "/a.txt") || strings.HasSuffix(canonicalPath, "\\a.txt") {
		return nil, remote.NewError("NOT_DIRECTORY", "", 3)
	}
	size := int64(5)
	return []remote.DirectoryEntry{
		{Name: "a.txt", Type: "file", Size: &size},
		{Name: "dir", Type: "directory"},
		{Name: "link", Type: "symlink"},
	}, nil
}

func (f *fakeRemote) FileStat(_ context.Context, _ registry.Target, canonicalPath string, _ time.Duration) (remote.FileStat, error) {
	if strings.HasSuffix(canonicalPath, "/missing") || strings.HasSuffix(canonicalPath, "\\missing") {
		return remote.FileStat{}, remote.NewError("NOT_FOUND", "", 2)
	}
	return remote.FileStat{Exists: true, Type: "file", Size: 5, MTime: 1700000000}, nil
}

func (f *fakeRemote) ReadFile(_ context.Context, _ registry.Target, _ string, maxBytes int64, _ time.Duration) (string, error) {
	f.lastReadLimit = maxBytes
	return "Hello á\n", nil
}

func (f *fakeRemote) GitStatus(_ context.Context, _ registry.Target, _ string, _ time.Duration) (string, error) {
	return " M README.md\n?? new.txt\n", nil
}

func (f *fakeRemote) Search(_ context.Context, _ registry.Target, _, _, _ string, _ bool, _ int, _ time.Duration) ([]remote.SearchMatch, error) {
	return []remote.SearchMatch{
		{File: "a.txt", Line: 2, Text: "needle here"},
		{File: "dir/b.txt", Line: 7, Text: "another needle"},
	}, nil
}

func loadReadOnlyFixture(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "contracts", "read_only_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture map[string]any
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture["schema"] != float64(1) {
		t.Fatalf("fixture schema=%v", fixture["schema"])
	}
	return fixture
}

func seededRemoteCore(t *testing.T) (*Core, *fakeRemote, *sql.DB) {
	t.Helper()
	base, db := seededCore(t)
	fake := &fakeRemote{}
	return NewWithRemote(base.store, base.config, fake), fake, db
}

func normalizedResponse(t *testing.T, response Response) map[string]any {
	t.Helper()
	got := normalizeJSON(t, response)
	delete(got, "duration_ms")
	return got
}

func TestTargetStatusPreservesPythonEnvelopeSemantics(t *testing.T) {
	c, _, _ := seededRemoteCore(t)
	got := c.TargetStatus(context.Background(), "", "t")
	if !got.OK || got.Tool != "target_status" || got.Target != "t" {
		t.Fatalf("target_status envelope=%+v", got)
	}
	result, ok := got.Result.(map[string]any)
	if !ok {
		t.Fatalf("target_status result type=%T", got.Result)
	}
	if result["reachable"] != true ||
		result["remote_hostname"] != "remote-box" ||
		result["latency_ms"] != int64(12) ||
		result["platform"] != "linux" {
		t.Fatalf("target_status result=%#v", result)
	}
	facts, ok := result["facts"].(map[string]any)
	if !ok || facts["probe_status"] != "ok" || facts["arch"] != "armv6l" {
		t.Fatalf("target_status facts=%#v", result["facts"])
	}

	missing := c.TargetStatus(context.Background(), "", "missing")
	if missing.OK || missing.Error == nil || missing.Error.Code != "UNKNOWN_TARGET" {
		t.Fatalf("missing target response=%+v", missing)
	}
}

func TestReadOnlyToolsMatchFrozenPythonOutputs(t *testing.T) {
	fixture := loadReadOnlyFixture(t)
	c, _, _ := seededRemoteCore(t)
	ctx := context.Background()

	cases := []struct {
		name string
		got  Response
	}{
		{"list_directory", c.ListDirectory(ctx, "", "t", "p", "sub")},
		{"file_stat", c.FileStat(ctx, "", "t", "p", "a.txt")},
		{"read_file", c.ReadFile(ctx, "", "t", "p", "a.txt")},
		{"git_status", c.GitStatus(ctx, "", "t", "p")},
		{"search", c.Search(ctx, "", "t", "p", "needle", "sub", false)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want, ok := fixture[tc.name].(map[string]any)
			if !ok {
				t.Fatalf("missing fixture %s", tc.name)
			}
			got := normalizedResponse(t, tc.got)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s mismatch\nGo: %#v\nPython: %#v", tc.name, got, want)
			}
		})
	}
}

func TestReadOnlyErrorsMatchFrozenPythonOutputs(t *testing.T) {
	fixture := loadReadOnlyFixture(t)
	errorsFixture := fixture["errors"].(map[string]any)
	c, _, _ := seededRemoteCore(t)
	ctx := context.Background()

	cases := []struct {
		name string
		got  Response
	}{
		{"list_directory_missing", c.ListDirectory(ctx, "", "t", "p", "missing")},
		{"list_directory_not_dir", c.ListDirectory(ctx, "", "t", "p", "a.txt")},
		{"file_stat_missing", c.FileStat(ctx, "", "t", "p", "missing")},
		{"read_escape", c.ReadFile(ctx, "", "t", "p", "escape.txt")},
		{"search_empty", c.Search(ctx, "", "t", "p", "", ".", false)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := errorsFixture[tc.name].(map[string]any)
			got := normalizedResponse(t, tc.got)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s mismatch\nGo: %#v\nPython: %#v", tc.name, got, want)
			}
		})
	}
}

func TestGitStatusPreservesCurrentProjectReadFlagSemantics(t *testing.T) {
	c, _, db := seededRemoteCore(t)
	if _, err := db.Exec("UPDATE projects SET read_enabled=0 WHERE target_id='t' AND id='p'"); err != nil {
		t.Fatal(err)
	}

	git := c.GitStatus(context.Background(), "", "t", "p")
	if !git.OK {
		t.Fatalf("git_status unexpectedly denied: %+v", git)
	}
	list := c.ListDirectory(context.Background(), "", "t", "p", ".")
	if list.OK || list.Error == nil || list.Error.Code != "TOOL_NOT_ALLOWED" {
		t.Fatalf("list_directory should preserve read gate: %+v", list)
	}
}

func TestReadFilePassesConfiguredLimitToTransport(t *testing.T) {
	c, fake, _ := seededRemoteCore(t)
	if err := c.store.SetSetting(context.Background(), "max_file_read_bytes", "123"); err != nil {
		t.Fatal(err)
	}
	got := c.ReadFile(context.Background(), "", "t", "p", "a.txt")
	if !got.OK {
		t.Fatalf("read_file failed: %+v", got)
	}
	if fake.lastReadLimit != 123 {
		t.Fatalf("read limit=%d want=123", fake.lastReadLimit)
	}
}

func TestWindowsCandidateJoinDoesNotUseHostPathSemantics(t *testing.T) {
	c, fake, db := seededRemoteCore(t)
	if _, err := db.Exec("UPDATE targets SET platform='windows' WHERE id='t'"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE projects SET root='C:\\Work\\Repo' WHERE target_id='t' AND id='p'"); err != nil {
		t.Fatal(err)
	}

	got := c.FileStat(context.Background(), "", "t", "p", "sub/file.txt")
	if !got.OK {
		t.Fatalf("file_stat failed: %+v", got)
	}
	if fake.lastCanonicalCandidate != "C:\\Work\\Repo\\sub\\file.txt" {
		t.Fatalf("candidate=%q", fake.lastCanonicalCandidate)
	}
}
