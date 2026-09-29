package remote

import (
	"context"
	"encoding/base64"
	"strconv"
	"strings"
	"time"

	"mcp-gateway-adapter/internal/registry"
)

func encodeFactsShellValue(value string) string {
	return base64.StdEncoding.EncodeToString([]byte(value))
}

func factsString(kv map[string]string, key string) string {
	value, err := decodeB64(kv[key])
	if err != nil {
		return ""
	}
	return value
}

func factsBool(kv map[string]string, key string) bool {
	return kv[key] == "1" || strings.EqualFold(kv[key], "true")
}

func nullableFact(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func (s *SSHTransport) probePrivilegedSSH(
	ctx context.Context,
	target registry.Target,
	timeout time.Duration,
) (string, bool, string) {
	privilegedUser := strings.TrimSpace(target.PrivilegeUser)
	if privilegedUser == "" {
		return "", false, ""
	}

	privilegedTarget := target
	privilegedTarget.User = privilegedUser

	command := "id -u"
	expectedLevel := "root"
	if isWindowsTarget(target) {
		command = buildPowerShellCommand(
			"$ErrorActionPreference='Stop';" +
				"$id=[Security.Principal.WindowsIdentity]::GetCurrent();" +
				"$principal=New-Object Security.Principal.WindowsPrincipal($id);" +
				"if($principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)){Write-Output 'administrator'}else{Write-Output 'standard'}",
		)
		expectedLevel = "administrator"
	}

	result, err := s.RunCommand(ctx, privilegedTarget, command, CommandOptions{Timeout: timeout})
	if err != nil {
		return "", false, "Configured privileged SSH user could not be verified"
	}
	if !result.OK() {
		return "", false, "Configured privileged SSH user could not be verified"
	}

	observed := strings.ToLower(strings.TrimSpace(result.Stdout))
	if isWindowsTarget(target) {
		if observed != expectedLevel {
			return "", false, "Configured privileged SSH user is not Administrator"
		}
		return expectedLevel, true, ""
	}
	if observed != "0" {
		return "", false, "Configured privileged SSH user is not root"
	}
	return expectedLevel, true, ""
}

func (s *SSHTransport) probeShizukuUID(
	ctx context.Context,
	target registry.Target,
	timeout time.Duration,
) (string, bool, string) {
	probeTimeout := 4 * time.Second
	if timeout > 0 && timeout < probeTimeout {
		probeTimeout = timeout
	}
	result, err := s.RunCommand(
		ctx,
		target,
		"{ if command -v timeout >/dev/null 2>&1; then timeout -k 1s 2s rish -c 'id -u' 2>/dev/null; fi; } | tr -cd '0-9\\n' | tail -n 1",
		CommandOptions{Timeout: probeTimeout},
	)
	if err != nil || !result.OK() {
		return "", false, "Shizuku readiness probe did not complete"
	}
	uid := strings.TrimSpace(result.Stdout)
	switch uid {
	case "0":
		return "root", true, ""
	case "2000":
		return "android_shell", true, ""
	default:
		if uid != "" {
			if _, err := strconv.Atoi(uid); err == nil {
				return "elevated", true, ""
			}
		}
		return "", false, "Shizuku returned no usable UID"
	}
}

func (s *SSHTransport) ProbeFacts(
	ctx context.Context,
	target registry.Target,
	includeBootID bool,
	timeout time.Duration,
) (map[string]any, error) {
	include := "0"
	if includeBootID {
		include = "1"
	}

	var command string
	if isWindowsTarget(target) {
		script := "$ErrorActionPreference='SilentlyContinue';$includeBoot=" + include + ";" +
			"function B64([string]$v){if([string]::IsNullOrEmpty($v)){return ''};[Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($v))};" +
			"$os='windows';$arch=$env:PROCESSOR_ARCHITECTURE;$shell=$env:COMSPEC;$environment='native';" +
			"$ram='';try{$ram=[int64]((Get-CimInstance Win32_ComputerSystem).TotalPhysicalMemory/1MB)}catch{};" +
			"$boot='';if($includeBoot -eq 1){try{$boot='windows:'+((Get-CimInstance Win32_OperatingSystem).LastBootUpTime.ToFileTimeUtc())}catch{}};" +
			"$user=[Environment]::UserName;$isAdmin=$false;try{$id=[Security.Principal.WindowsIdentity]::GetCurrent();$principal=New-Object Security.Principal.WindowsPrincipal($id);$isAdmin=$principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)}catch{};" +
			"$hasSudo=[bool](Get-Command sudo.exe -ErrorAction SilentlyContinue);$current='standard';$maximum='standard';$backend='';$ready=0;$reason='';$shellElevate=0;$independent='';" +
			"if($isAdmin){$current='administrator';$maximum='administrator';$backend='direct';$ready=1}elseif($hasSudo){$maximum='administrator';$backend='windows-sudo';$reason='Windows sudo is present but no verified non-interactive MCP-Pi elevation backend is configured'};" +
			"$pm=@();foreach($n in @('winget','choco')){if(Get-Command $n -ErrorAction SilentlyContinue){$pm+=$n}};" +
			"$runtimes=@{};foreach($n in @('node','go','git')){$c=Get-Command $n -ErrorAction SilentlyContinue;if($c){$runtimes[$n]=$c.Source}};" +
			"Write-Output 'probe_status=ok';Write-Output ('os_b64='+(B64 $os));Write-Output ('arch_b64='+(B64 $arch));Write-Output ('shell_b64='+(B64 $shell));Write-Output ('environment='+$environment);" +
			"Write-Output ('ram_mb='+$ram);Write-Output ('boot_id_b64='+(B64 $boot));Write-Output ('identity_user_b64='+(B64 $user));Write-Output 'uid=';Write-Output 'euid=';" +
			"Write-Output ('current_level='+$current);Write-Output ('maximum_level='+$maximum);Write-Output ('backend='+$backend);Write-Output ('backend_ready='+$ready);Write-Output ('backend_reason_b64='+(B64 $reason));" +
			"Write-Output ('transport_already_elevated='+([int]$isAdmin));Write-Output ('shell_can_elevate='+$shellElevate);Write-Output ('independent_elevator='+$independent);" +
			"Write-Output ('feature_sudo='+([int]$hasSudo));Write-Output 'feature_termux=0';Write-Output 'feature_shizuku=0';Write-Output 'service_manager=windows-service-control';" +
			"Write-Output ('package_managers='+($pm -join ','));foreach($n in $runtimes.Keys){Write-Output ('runtime_'+$n+'_b64='+(B64 $runtimes[$n]))};Write-Output 'os_release_id_b64=';Write-Output 'os_release_version_b64='"
		command = buildPowerShellCommand(script)
	} else {
		command = "include_boot=" + include + `; ` +
			`b64(){ if [ -z "$1" ]; then printf ''; else printf '%s' "$1" | base64 | tr -d '\n'; fi; }; ` +
			`os=$(uname -s 2>/dev/null | tr '[:upper:]' '[:lower:]'); arch=$(uname -m 2>/dev/null); shell=${SHELL:-}; release=$(uname -r 2>/dev/null); ` +
			`termux=0; environment=native; if [ -n "${TERMUX_VERSION:-}" ]; then termux=1; environment=termux; else case "${PREFIX:-}" in *com.termux*) termux=1; environment=termux;; esac; fi; ` +
			`case "$release" in *[Mm]icrosoft*) [ "$termux" -eq 1 ] || environment=wsl;; esac; ` +
			`ram=''; if [ -r /proc/meminfo ]; then ram=$(awk '/^MemTotal:/{print int($2/1024); exit}' /proc/meminfo 2>/dev/null); fi; ` +
			`boot=''; if [ "$include_boot" -eq 1 ] && [ -r /proc/sys/kernel/random/boot_id ]; then boot=$(cat /proc/sys/kernel/random/boot_id 2>/dev/null | tr -d '\r\n'); fi; ` +
			`user=$(id -un 2>/dev/null || printf ''); uid=$(id -u 2>/dev/null || printf ''); euid=$uid; ` +
			`current=standard; maximum=standard; backend=''; ready=0; reason=''; shell_elevate=0; independent=''; ` +
			`has_sudo=0; command -v sudo >/dev/null 2>&1 && has_sudo=1; has_rish=0; command -v rish >/dev/null 2>&1 && has_rish=1; ` +
			`if [ "$euid" = "0" ]; then current=root; maximum=root; backend=direct; ready=1; ` +
			`elif [ "$termux" -eq 1 ] && [ "$has_rish" -eq 1 ]; then backend=shizuku; maximum=elevated; shell_elevate=1; independent=shizuku; reason='Shizuku readiness not verified'; ` +
			`elif [ "$has_sudo" -eq 1 ]; then maximum=root; backend=sudo; reason='sudo is installed, but arbitrary sudo shell execution is intentionally not accepted as a safe MCP-Pi backend'; ` +
			`if sudo -n true >/dev/null 2>&1; then shell_elevate=1; independent=sudo-noninteractive; fi; fi; ` +
			`service=''; command -v systemctl >/dev/null 2>&1 && service=systemd; [ -n "$service" ] || { command -v rc-service >/dev/null 2>&1 && service=openrc; }; [ -n "$service" ] || { command -v launchctl >/dev/null 2>&1 && service=launchd; }; ` +
			`pm=''; for n in pkg apt apt-get dnf yum pacman apk brew; do if command -v "$n" >/dev/null 2>&1; then [ -z "$pm" ] && pm=$n || pm="$pm,$n"; fi; done; ` +
			`nodep=$(command -v node 2>/dev/null || true); gop=$(command -v go 2>/dev/null || true); gitp=$(command -v git 2>/dev/null || true); ` +
			`osid=''; osver=''; if [ -r /etc/os-release ]; then osid=$(sed -n 's/^ID=//p' /etc/os-release | head -n1 | tr -d '"'); osver=$(sed -n 's/^VERSION_ID=//p' /etc/os-release | head -n1 | tr -d '"'); fi; ` +
			`printf 'probe_status=ok\nos_b64=%s\narch_b64=%s\nshell_b64=%s\nenvironment=%s\nram_mb=%s\nboot_id_b64=%s\nidentity_user_b64=%s\nuid=%s\neuid=%s\ncurrent_level=%s\nmaximum_level=%s\nbackend=%s\nbackend_ready=%s\nbackend_reason_b64=%s\ntransport_already_elevated=%s\nshell_can_elevate=%s\nindependent_elevator=%s\nfeature_sudo=%s\nfeature_termux=%s\nfeature_shizuku=%s\nservice_manager=%s\npackage_managers=%s\nruntime_node_b64=%s\nruntime_go_b64=%s\nruntime_git_b64=%s\nos_release_id_b64=%s\nos_release_version_b64=%s\n' ` +
			`"$(b64 "$os")" "$(b64 "$arch")" "$(b64 "$shell")" "$environment" "$ram" "$(b64 "$boot")" "$(b64 "$user")" "$uid" "$euid" "$current" "$maximum" "$backend" "$ready" "$(b64 "$reason")" ` +
			`"$([ "$current" = root ] && printf 1 || printf 0)" "$shell_elevate" "$independent" "$has_sudo" "$termux" "$has_rish" "$service" "$pm" "$(b64 "$nodep")" "$(b64 "$gop")" "$(b64 "$gitp")" "$(b64 "$osid")" "$(b64 "$osver")"`
	}

	result, err := s.RunCommand(ctx, target, command, CommandOptions{Timeout: timeout})
	if err != nil {
		return nil, err
	}
	if !result.OK() || strings.TrimSpace(result.Stdout) == "" {
		return map[string]any{
			"probe_status": "unavailable",
			"reason":       nonEmptyRemote(result.Stderr, "facts probe failed"),
		}, nil
	}

	kv := parseKVLines(result.Stdout)
	if kv["probe_status"] != "ok" {
		return map[string]any{
			"probe_status": "unavailable",
			"reason":       "facts probe returned an invalid response",
		}, nil
	}

	identity := map[string]any{"user": factsString(kv, "identity_user_b64")}
	if uid, err := strconv.Atoi(kv["uid"]); err == nil {
		identity["uid"] = uid
	}
	if euid, err := strconv.Atoi(kv["euid"]); err == nil {
		identity["euid"] = euid
	}

	var ram any
	if n, err := strconv.Atoi(kv["ram_mb"]); err == nil && n > 0 {
		ram = n
	}
	var boot any
	if value := factsString(kv, "boot_id_b64"); value != "" {
		boot = value
	}

	packages := []string{}
	if raw := strings.TrimSpace(kv["package_managers"]); raw != "" {
		for _, item := range strings.Split(raw, ",") {
			if item = strings.TrimSpace(item); item != "" {
				packages = append(packages, item)
			}
		}
	}
	runtimes := map[string]any{}
	for _, name := range []string{"node", "go", "git"} {
		if value := factsString(kv, "runtime_"+name+"_b64"); value != "" {
			runtimes[name] = value
		}
	}

	facts := map[string]any{
		"probe_status":       "ok",
		"os":                 factsString(kv, "os_b64"),
		"arch":               factsString(kv, "arch_b64"),
		"shell":              nullableFact(factsString(kv, "shell_b64")),
		"environment":        kv["environment"],
		"package_managers":   packages,
		"runtimes":           runtimes,
		"service_manager":    nullableFact(kv["service_manager"]),
		"ram_mb":             ram,
		"boot_id":            boot,
		"effective_identity": identity,
		"privilege": map[string]any{
			"current_level":              kv["current_level"],
			"maximum_level":              kv["maximum_level"],
			"backend":                    nullableFact(kv["backend"]),
			"backend_ready":              factsBool(kv, "backend_ready"),
			"backend_reason":             nullableFact(factsString(kv, "backend_reason_b64")),
			"transport_already_elevated": factsBool(kv, "transport_already_elevated"),
			"shell_can_elevate":          factsBool(kv, "shell_can_elevate"),
			"independent_elevator":       nullableFact(kv["independent_elevator"]),
		},
		"features": map[string]any{
			"sudo":    factsBool(kv, "feature_sudo"),
			"termux":  factsBool(kv, "feature_termux"),
			"shizuku": factsBool(kv, "feature_shizuku"),
		},
	}
	osID := factsString(kv, "os_release_id_b64")
	osVersion := factsString(kv, "os_release_version_b64")
	if osID != "" || osVersion != "" {
		facts["os_release"] = map[string]any{
			"id":         nullableFact(osID),
			"version_id": nullableFact(osVersion),
		}
	}

	if strings.TrimSpace(target.PrivilegeUser) == "" {
		features := facts["features"].(map[string]any)
		privilege := facts["privilege"].(map[string]any)
		if features["termux"] == true && features["shizuku"] == true && privilege["current_level"] != "root" {
			level, ready, reason := s.probeShizukuUID(ctx, target, timeout)
			privilege["backend"] = "shizuku"
			privilege["shell_can_elevate"] = true
			privilege["independent_elevator"] = "shizuku"
			if ready {
				privilege["maximum_level"] = level
				privilege["backend_ready"] = true
				privilege["backend_reason"] = nil
			} else {
				privilege["backend_ready"] = false
				privilege["backend_reason"] = reason
			}
		}
	}

	if strings.TrimSpace(target.PrivilegeUser) != "" {
		privilege := facts["privilege"].(map[string]any)
		privilege["backend"] = "privileged-ssh"
		privilege["backend_ready"] = false
		privilege["backend_reason"] = "Configured privileged SSH user could not be verified"

		if level, ready, reason := s.probePrivilegedSSH(ctx, target, timeout); ready {
			privilege["maximum_level"] = level
			privilege["backend_ready"] = true
			privilege["backend_reason"] = nil
		} else if reason != "" {
			privilege["backend_reason"] = reason
		}
	}
	return facts, nil
}
