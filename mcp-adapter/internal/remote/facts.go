package remote

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"mcp-gateway-adapter/internal/registry"
)

func (s *SSHTransport) ProbeFacts(
	ctx context.Context,
	target registry.Target,
	includeBootID bool,
	timeout time.Duration,
) (map[string]any, error) {
	include := "False"
	if includeBootID {
		include = "True"
	}
	script := "include_boot_id = " + include + "\n" + `import ctypes, getpass, json, os, platform, shutil, subprocess
facts = {'probe_status': 'ok'}
system = platform.system().lower() or os.name
facts['os'] = system
facts['arch'] = platform.machine()
facts['shell'] = os.environ.get('SHELL') or os.environ.get('COMSPEC') or None
prefix = os.environ.get('PREFIX', '')
termux = bool(os.environ.get('TERMUX_VERSION')) or 'com.termux' in prefix
facts['environment'] = 'termux' if termux else ('wsl' if 'microsoft' in platform.release().lower() else 'native')
facts['package_managers'] = [x for x in ('pkg','apt','apt-get','dnf','yum','pacman','apk','brew','winget','choco') if shutil.which(x)]
facts['runtimes'] = {x: shutil.which(x) for x in ('python3','python','node','go','git') if shutil.which(x)}
sm = None
for name in ('systemctl','rc-service','launchctl'):
    if shutil.which(name):
        sm = {'systemctl':'systemd','rc-service':'openrc','launchctl':'launchd'}[name]
        break
facts['service_manager'] = sm
ram = None
boot_id = None
if system == 'windows':
    try:
        class MS(ctypes.Structure):
            _fields_=[('dwLength',ctypes.c_ulong),('dwMemoryLoad',ctypes.c_ulong),('ullTotalPhys',ctypes.c_ulonglong),('ullAvailPhys',ctypes.c_ulonglong),('ullTotalPageFile',ctypes.c_ulonglong),('ullAvailPageFile',ctypes.c_ulonglong),('ullTotalVirtual',ctypes.c_ulonglong),('ullAvailVirtual',ctypes.c_ulonglong),('ullAvailExtendedVirtual',ctypes.c_ulonglong)]
        ms=MS(); ms.dwLength=ctypes.sizeof(MS)
        if ctypes.windll.kernel32.GlobalMemoryStatusEx(ctypes.byref(ms)):
            ram=int(ms.ullTotalPhys // (1024*1024))
    except Exception:
        pass
    if include_boot_id:
        try:
            cp=subprocess.run(['powershell','-NoProfile','-NonInteractive','-Command','(Get-CimInstance Win32_OperatingSystem).LastBootUpTime.ToFileTimeUtc()'],capture_output=True,text=True,timeout=4)
            if cp.returncode == 0 and cp.stdout.strip():
                boot_id='windows:'+cp.stdout.strip().splitlines()[-1].strip()
        except Exception:
            pass
else:
    try:
        with open('/proc/meminfo', 'r', encoding='utf-8', errors='replace') as fh:
            for line in fh:
                if line.startswith('MemTotal:'):
                    ram = int(line.split()[1]) // 1024
                    break
    except Exception:
        pass
    if include_boot_id:
        try:
            with open('/proc/sys/kernel/random/boot_id', 'r', encoding='ascii') as fh:
                boot_id=fh.read().strip() or None
        except Exception:
            pass
facts['ram_mb'] = ram
facts['boot_id'] = boot_id
identity = {'user': getpass.getuser()}
uid = None
euid = None
try:
    uid = os.getuid()
    euid = os.geteuid()
    identity.update({'uid': uid, 'euid': euid})
except AttributeError:
    pass
current_level = 'standard'
maximum_level = 'standard'
backend = None
backend_ready = False
backend_reason = None
shell_can_elevate = False
independent_elevator = None
if system == 'windows':
    is_admin = False
    try:
        is_admin = bool(ctypes.windll.shell32.IsUserAnAdmin())
    except Exception:
        pass
    if is_admin:
        current_level = maximum_level = 'administrator'
        backend = 'direct'
        backend_ready = True
    elif shutil.which('sudo'):
        maximum_level = 'administrator'
        backend = 'windows-sudo'
        backend_reason = 'Windows sudo is present but no verified non-interactive MCP-Pi elevation backend is configured'
        shell_can_elevate = True
        independent_elevator = 'windows-sudo'
elif euid == 0:
    current_level = maximum_level = 'root'
    backend = 'direct'
    backend_ready = True
elif termux and shutil.which('rish'):
    backend = 'shizuku'
    try:
        cp=subprocess.run([shutil.which('rish'),'-c','id -u'],capture_output=True,text=True,timeout=3)
        if cp.returncode == 0:
            uid_tokens=[x for x in (cp.stdout+'\n'+cp.stderr).split() if x.isdigit()]
            remote_uid=uid_tokens[-1] if uid_tokens else ''
            if remote_uid:
                maximum_level = 'root' if remote_uid == '0' else ('android_shell' if remote_uid == '2000' else 'elevated')
                backend_ready = True
                shell_can_elevate = True
                independent_elevator = 'shizuku'
            else:
                backend_reason = 'Shizuku returned no usable UID'
        else:
            backend_reason = (cp.stderr.strip() or cp.stdout.strip() or 'Shizuku probe failed')[:240]
    except subprocess.TimeoutExpired:
        backend_reason = 'Shizuku probe timed out'
    except Exception as exc:
        backend_reason = ('Shizuku probe failed: ' + str(exc))[:240]
elif shutil.which('sudo'):
    maximum_level = 'root'
    backend = 'sudo'
    backend_ready = False
    backend_reason = 'sudo is installed, but arbitrary sudo shell execution is intentionally not accepted as a safe MCP-Pi backend'
    try:
        cp=subprocess.run(['sudo','-n','true'],capture_output=True,text=True,timeout=2)
        if cp.returncode == 0:
            shell_can_elevate = True
            independent_elevator = 'sudo-noninteractive'
    except Exception:
        pass
facts['effective_identity'] = identity
facts['privilege'] = {
    'current_level': current_level,
    'maximum_level': maximum_level,
    'backend': backend,
    'backend_ready': backend_ready,
    'backend_reason': backend_reason,
    'transport_already_elevated': current_level in ('root','administrator'),
    'shell_can_elevate': shell_can_elevate,
    'independent_elevator': independent_elevator,
}
facts['features'] = {
    'sudo': bool(shutil.which('sudo')),
    'termux': termux,
    'shizuku': bool(shutil.which('rish')),
}
try:
    if os.path.isfile('/etc/os-release'):
        data = {}
        for line in open('/etc/os-release', encoding='utf-8', errors='replace'):
            if '=' in line:
                k,v=line.rstrip().split('=',1)
                data[k]=v.strip().strip(chr(34))
        facts['os_release'] = {'id': data.get('ID'), 'version_id': data.get('VERSION_ID')}
except Exception:
    pass
print(json.dumps(facts, separators=(',', ':')))
`
	command, err := buildRemotePythonCommand(script, nil)
	if err != nil {
		return nil, err
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

	var facts map[string]any
	if err := json.Unmarshal([]byte(lastNonEmptyLine(result.Stdout)), &facts); err != nil {
		return map[string]any{
			"probe_status": "unavailable",
			"reason":       "facts probe returned invalid JSON",
		}, nil
	}
	if ram, ok := facts["ram_mb"].(float64); ok && ram == 0 {
		facts["ram_mb"] = nil
	}
	return facts, nil
}
