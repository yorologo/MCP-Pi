package remote

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"mcp-gateway-adapter/internal/registry"
)

func TestBuildSSHArgsUsesRegistryAuthoritativeEndpoint(t *testing.T) {
	transport := &SSHTransport{IdentityFile: "/tmp/test-key"}
	target := registry.Target{
		ID:       "target-a",
		Host:     "10.0.0.2",
		Port:     2222,
		User:     "pi",
		SSHAlias: "target-alias",
	}

	args, err := transport.buildSSHArgs(target, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, "\n")
	for _, want := range []string{
		"BatchMode=yes",
		"ConnectTimeout=10",
		"StrictHostKeyChecking=yes",
		"IdentitiesOnly=yes",
		"IdentityFile=/tmp/test-key",
		"User=pi",
		"HostName=10.0.0.2",
		"Port=2222",
		"HostKeyAlias=target-a",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("ssh args missing %q: %v", want, args)
		}
	}
	if got := args[len(args)-1]; got != "target-alias" {
		t.Fatalf("destination=%q want=target-alias", got)
	}
	if len(args) < 2 || args[len(args)-2] != "--" {
		t.Fatalf("ssh destination must be separated from options with --: %v", args)
	}
}

func TestBuildSSHArgsFallsBackToHostAndFailsClosed(t *testing.T) {
	transport := &SSHTransport{IdentityFile: "/tmp/test-key"}

	args, err := transport.buildSSHArgs(registry.Target{
		ID:   "t",
		Host: "192.0.2.10",
		Port: 22,
		User: "user",
	}, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got := args[len(args)-1]; got != "192.0.2.10" {
		t.Fatalf("destination=%q want host", got)
	}
	if !strings.Contains(strings.Join(args, "\n"), "ConnectTimeout=2") {
		t.Fatalf("connect timeout not bounded to request: %v", args)
	}

	args, err = transport.buildSSHArgs(registry.Target{
		ID: "t", Host: "192.0.2.10", Port: 22, User: "user",
	}, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(args, "\n"), "ConnectTimeout=9") {
		t.Fatalf("normal probe must leave process timeout margin: %v", args)
	}

	if _, err := transport.buildSSHArgs(registry.Target{Host: "192.0.2.10"}, 10*time.Second); ErrorCode(err) != "SSH_FAILED" {
		t.Fatalf("missing user err=%v code=%q", err, ErrorCode(err))
	}
	if _, err := transport.buildSSHArgs(registry.Target{User: "user"}, 10*time.Second); ErrorCode(err) != "SSH_FAILED" {
		t.Fatalf("missing endpoint err=%v code=%q", err, ErrorCode(err))
	}
}

func TestRunCommandHonorsContextAndDoesNotNeedNetworkForValidation(t *testing.T) {
	transport := &SSHTransport{
		SSHBinary:      "/definitely/not/a/real/ssh",
		IdentityFile:   "/tmp/test-key",
		MaxOutputBytes: 1024,
		DefaultTimeout: time.Second,
	}
	_, err := transport.RunCommand(
		context.Background(),
		registry.Target{ID: "t", Host: "192.0.2.10", Port: 22, User: "user"},
		"hostname",
		CommandOptions{Timeout: time.Second},
	)
	if ErrorCode(err) != "SSH_FAILED" {
		t.Fatalf("err=%v code=%q", err, ErrorCode(err))
	}
}

func privilegedSSHShim(t *testing.T, allowRoot bool) *SSHTransport {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX SSH shim is not available on Windows")
	}
	shim := filepath.Join(t.TempDir(), "ssh-priv-shim")
	rootAction := "exit 255"
	if allowRoot {
		rootAction = "printf '0\\n'; exit 0"
	}
	script := "#!/bin/sh\n" +
		"user=\"\"\n" +
		"last=\"\"\n" +
		"for arg\n" +
		"do\n" +
		"  case \"$arg\" in User=*) user=$(printf '%s' \"$arg\" | cut -d= -f2-) ;; esac\n" +
		"  last=\"$arg\"\n" +
		"done\n" +
		"if [ \"$user\" = \"root\" ]; then " + rootAction + "; fi\n" +
		"exec sh -c \"$last\"\n"
	if err := os.WriteFile(shim, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return &SSHTransport{
		SSHBinary:      shim,
		IdentityFile:   "/tmp/test-key",
		MaxOutputBytes: defaultMaxOutput,
		DefaultTimeout: 10 * time.Second,
	}
}

func TestProbeFactsVerifiesConfiguredPrivilegedSSHUser(t *testing.T) {
	transport := privilegedSSHShim(t, true)
	target := localTarget()
	target.PrivilegeUser = "root"

	facts, err := transport.ProbeFacts(context.Background(), target, false, true, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	privilege, ok := facts["privilege"].(map[string]any)
	if !ok {
		t.Fatalf("privilege facts missing: %#v", facts)
	}
	if got := privilege["backend"]; got != "privileged-ssh" {
		t.Fatalf("backend=%v want=privileged-ssh", got)
	}
	if got := privilege["backend_ready"]; got != true {
		t.Fatalf("backend_ready=%v want=true", got)
	}
	if got := privilege["maximum_level"]; got != "root" {
		t.Fatalf("maximum_level=%v want=root", got)
	}
}

func TestProbeFactsFailsClosedWhenConfiguredPrivilegedSSHUserCannotAuthenticate(t *testing.T) {
	transport := privilegedSSHShim(t, false)
	target := localTarget()
	target.PrivilegeUser = "root"

	facts, err := transport.ProbeFacts(context.Background(), target, false, true, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	privilege, ok := facts["privilege"].(map[string]any)
	if !ok {
		t.Fatalf("privilege facts missing: %#v", facts)
	}
	if got := privilege["backend"]; got != "privileged-ssh" {
		t.Fatalf("backend=%v want=privileged-ssh", got)
	}
	if got := privilege["backend_ready"]; got != false {
		t.Fatalf("backend_ready=%v want=false", got)
	}
	if reason, _ := privilege["backend_reason"].(string); !strings.Contains(reason, "could not be verified") {
		t.Fatalf("backend_reason=%q", reason)
	}
}

func TestProbeFactsBoundsSlowShizukuReadinessWithoutDroppingGuard(t *testing.T) {
	dir := t.TempDir()
	rish := filepath.Join(dir, "rish")
	if err := os.WriteFile(rish, []byte("#!/bin/sh\nsleep 10\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TERMUX_VERSION", "test")

	transport := localSSHShim(t)
	start := time.Now()
	facts, err := transport.ProbeFacts(context.Background(), localTarget(), false, true, 8*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed >= 5*time.Second {
		t.Fatalf("slow Shizuku readiness blocked facts probe for %s", elapsed)
	}
	privilege, ok := facts["privilege"].(map[string]any)
	if !ok {
		t.Fatalf("privilege facts missing: %#v", facts)
	}
	if got := privilege["backend"]; got != "shizuku" {
		t.Fatalf("backend=%v want=shizuku", got)
	}
	if got := privilege["backend_ready"]; got != false {
		t.Fatalf("backend_ready=%v want=false", got)
	}
	if got := privilege["shell_can_elevate"]; got != true {
		t.Fatalf("shell_can_elevate=%v want=true", got)
	}
	if got := privilege["independent_elevator"]; got != "shizuku" {
		t.Fatalf("independent_elevator=%v want=shizuku", got)
	}
}

func TestProbeFactsVerifiesResponsiveShizukuUID(t *testing.T) {
	dir := t.TempDir()
	rish := filepath.Join(dir, "rish")
	if err := os.WriteFile(rish, []byte("#!/bin/sh\nprintf '2000\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TERMUX_VERSION", "test")

	transport := localSSHShim(t)
	facts, err := transport.ProbeFacts(context.Background(), localTarget(), false, true, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	privilege, ok := facts["privilege"].(map[string]any)
	if !ok {
		t.Fatalf("privilege facts missing: %#v", facts)
	}
	if got := privilege["backend"]; got != "shizuku" {
		t.Fatalf("backend=%v want=shizuku", got)
	}
	if got := privilege["backend_ready"]; got != true {
		t.Fatalf("backend_ready=%v want=true", got)
	}
	if got := privilege["maximum_level"]; got != "android_shell" {
		t.Fatalf("maximum_level=%v want=android_shell", got)
	}
}

func TestProbeFactsRetriesTransientEmptyShizukuUID(t *testing.T) {
	dir := t.TempDir()
	rish := filepath.Join(dir, "rish")
	counter := filepath.Join(dir, "rish-count")
	script := fmt.Sprintf(
		"#!/bin/sh\n"+
			"n=0\n"+
			"[ ! -f %q ] || n=$(cat %q)\n"+
			"n=$((n+1))\n"+
			"printf '%%s\\n' \"$n\" > %q\n"+
			"if [ \"$n\" -ge 2 ]; then printf '2000\\n'; fi\n",
		counter, counter, counter,
	)
	if err := os.WriteFile(rish, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TERMUX_VERSION", "test")

	transport := localSSHShim(t)
	facts, err := transport.ProbeFacts(context.Background(), localTarget(), false, true, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	privilege, ok := facts["privilege"].(map[string]any)
	if !ok {
		t.Fatalf("privilege facts missing: %#v", facts)
	}
	if got := privilege["backend_ready"]; got != true {
		t.Fatalf("backend_ready=%v want=true", got)
	}
	if got := privilege["maximum_level"]; got != "android_shell" {
		t.Fatalf("maximum_level=%v want=android_shell", got)
	}
	data, err := os.ReadFile(counter)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(data)); got != "2" {
		t.Fatalf("rish attempts=%q want=2", got)
	}
}

func TestLimitedBufferTruncatesWithoutShortWrite(t *testing.T) {
	var buffer limitedBuffer
	buffer.max = 4
	n, err := buffer.Write([]byte("abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 6 {
		t.Fatalf("reported write=%d want=6", n)
	}
	if got := string(buffer.Bytes()); got != "abcd" {
		t.Fatalf("buffer=%q want=abcd", got)
	}
}

func localSSHShim(t *testing.T) *SSHTransport {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX SSH shim is not available on Windows")
	}
	shim := filepath.Join(t.TempDir(), "ssh-shim")
	script := "#!/bin/sh\nfor last\ndo\n  :\ndone\nexec sh -c \"$last\"\n"
	if err := os.WriteFile(shim, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return &SSHTransport{
		SSHBinary:      shim,
		IdentityFile:   "/tmp/test-key",
		MaxOutputBytes: defaultMaxOutput,
		DefaultTimeout: 10 * time.Second,
	}
}

func localTarget() registry.Target {
	return registry.Target{ID: "local-test", Host: "127.0.0.1", Port: 22, User: "tester", Platform: "linux"}
}

func generatedWindowsScriptsForTest() map[string]string {
	return map[string]string{
		"path_probe":      windowsProbePathScript("C:\\Users\\Example\\project\\new.txt"),
		"canonical_probe": windowsResolveCanonicalPathScript("C:\\Users\\Example\\project"),
		"privilege_probe": windowsPrivilegeProbeScript("1"),
		"facts_probe":     windowsFactsProbeScript("1"),
	}
}

func TestGeneratedWindowsScriptsHaveBalancedBlocks(t *testing.T) {
	for name, script := range generatedWindowsScriptsForTest() {
		t.Run(name, func(t *testing.T) {
			depth := 0
			inSingleQuote := false
			for i := 0; i < len(script); i++ {
				switch script[i] {
				case '\'':
					if inSingleQuote && i+1 < len(script) && script[i+1] == '\'' {
						i++
						continue
					}
					inSingleQuote = !inSingleQuote
				case '{':
					if !inSingleQuote {
						depth++
					}
				case '}':
					if !inSingleQuote {
						depth--
						if depth < 0 {
							t.Fatal("PowerShell script closes a block before opening it")
						}
					}
				}
			}
			if inSingleQuote {
				t.Fatal("PowerShell script has an unterminated single-quoted string")
			}
			if depth != 0 {
				t.Fatalf("PowerShell script block depth=%d want=0", depth)
			}
		})
	}
}

func TestGeneratedWindowsScriptsParseWithNativePowerShell(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native PowerShell parser is validated on the Windows CI runner")
	}
	parser := "$src=[Console]::In.ReadToEnd();$tokens=$null;$errors=$null;$null=[System.Management.Automation.Language.Parser]::ParseInput($src,[ref]$tokens,[ref]$errors);if($errors.Count -gt 0){$errors | ForEach-Object {$_.Message} | Write-Error;exit 1}"
	for name, script := range generatedWindowsScriptsForTest() {
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command("powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", parser)
			cmd.Stdin = strings.NewReader(script)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("generated PowerShell does not parse: %v\n%s", err, output)
			}
		})
	}
}

func TestStructuredRemoteErrorPreservesPermissionDenied(t *testing.T) {
	err := structuredRemoteError(CommandResult{
		ExitCode: 25,
		Stdout:   "MCPERR|PERMISSION_DENIED|Access denied to path\n",
	}, "fallback")
	if ErrorCode(err) != "PERMISSION_DENIED" {
		t.Fatalf("error code=%q want PERMISSION_DENIED: %v", ErrorCode(err), err)
	}
}

func TestProbePrivilegeIsFocused(t *testing.T) {
	transport := localSSHShim(t)
	facts, err := transport.ProbePrivilege(context.Background(), localTarget(), false, false, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if facts["probe_status"] != "ok" {
		t.Fatalf("probe_status=%v facts=%#v", facts["probe_status"], facts)
	}
	if _, exists := facts["ram_mb"]; exists {
		t.Fatal("focused privilege probe unexpectedly returned full inventory field ram_mb")
	}
	if _, exists := facts["runtimes"]; exists {
		t.Fatal("focused privilege probe unexpectedly returned full inventory field runtimes")
	}
	privilege, ok := facts["privilege"].(map[string]any)
	if !ok || privilege == nil || privilege["current_level"] == "" {
		t.Fatalf("focused privilege state missing: %#v", facts)
	}
}

func TestStructuredMutationHelpersFailClosedOnSymlinkEscapes(t *testing.T) {
	transport := localSSHShim(t)
	root := filepath.Join(t.TempDir(), "project")
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.MkdirAll(filepath.Join(root, "safe"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "safe", "escape")); err != nil {
		t.Fatal(err)
	}
	_, err := transport.ResolveSafeDestination(
		context.Background(), localTarget(), root, filepath.Join(root, "safe", "escape", "new.txt"), false, 5*time.Second,
	)
	if ErrorCode(err) != "SYMLINK_WRITE_DENIED" {
		t.Fatalf("nested symlink err=%v code=%q", err, ErrorCode(err))
	}

	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	_, err = transport.ResolveSafeDestination(
		context.Background(), localTarget(), root, link, false, 5*time.Second,
	)
	if ErrorCode(err) != "SYMLINK_WRITE_DENIED" {
		t.Fatalf("destination symlink err=%v code=%q", err, ErrorCode(err))
	}
}

func TestWriteFileAtomicHelperEnforcesCreateAndSHA(t *testing.T) {
	transport := localSSHShim(t)
	root := t.TempDir()
	dest := filepath.Join(root, "file.txt")

	created, err := transport.WriteFileAtomic(
		context.Background(), localTarget(), dest, []byte("hello"), true, "", 1024, 5*time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !created.Created || !created.Atomic || created.BytesWritten != 5 {
		t.Fatalf("create result=%+v", created)
	}
	if data, err := os.ReadFile(dest); err != nil || string(data) != "hello" {
		t.Fatalf("created file data=%q err=%v", data, err)
	}

	_, err = transport.WriteFileAtomic(
		context.Background(), localTarget(), dest, []byte("changed"), false, "", 1024, 5*time.Second,
	)
	if ErrorCode(err) != "WRITE_CONFLICT" {
		t.Fatalf("missing sha err=%v code=%q", err, ErrorCode(err))
	}

	sum := sha256.Sum256([]byte("hello"))
	overwritten, err := transport.WriteFileAtomic(
		context.Background(), localTarget(), dest, []byte("changed"), false, fmt.Sprintf("%x", sum), 1024, 5*time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	if overwritten.Created || !overwritten.Atomic || overwritten.BytesWritten != len("changed") {
		t.Fatalf("overwrite result=%+v", overwritten)
	}
	if data, err := os.ReadFile(dest); err != nil || string(data) != "changed" {
		t.Fatalf("overwritten file data=%q err=%v", data, err)
	}
}
