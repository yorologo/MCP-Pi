# Configuration

Runtime configuration is split between versioned defaults and private persistent state. Secrets and machine-specific values do not belong in Git.

## Paths

```text
Application: /home/mcp-gateway/mcp-gateway
Registry:    /home/mcp-gateway/.local/share/mcp-gateway/gateway.db
Backups:     /home/mcp-gateway/.local/share/mcp-gateway/backups/
Private cfg: /home/mcp-gateway/.config/mcp-gateway/
```

## Gateway / Registry environment

| Variable | Purpose | Normal appliance value |
| --- | --- | --- |
| `MCP_GATEWAY_REGISTRY` | Registry backend | `sqlite` |
| `MCP_GATEWAY_DB` | SQLite path | persistent path above |
| `MCP_ADMIN_SECRET_FILE` | Flask session secret path | private config directory |

## Admin Console

The versioned systemd unit has safe loopback defaults:

```text
MCP_ADMIN_HOST=127.0.0.1
MCP_ADMIN_PORT=80
MCP_ADMIN_ALLOWED_HOSTS=127.0.0.1,localhost,mcp-pi
```

`install.sh` may create the private file:

```text
/home/mcp-gateway/.config/mcp-gateway/admin.env
```

with the detected hostname/LAN IP and `MCP_ADMIN_HOST=0.0.0.0` for trusted-LAN access. Because the file is persistent and outside Git, updates do not hardcode one developer's network into the product.

On an untrusted network, keep Admin loopback-only and use an SSH tunnel rather than exposing it broadly.

## Persistent Registry settings

Fresh defaults:

| Setting | Default | Meaning |
| --- | --- | --- |
| `gateway_enabled` | `true` | master operational switch |
| `writes_enabled` | `false` | structured filesystem mutations |
| `shell_enabled` | `false` | trusted Target shell (`run_command`) |
| `default_timeout` | `30` | default interactive execution timeout seconds; long/self-restarting work belongs in `run-resumable.sh` |
| `max_output_bytes` | `262144` | bounded command/tool output |
| `max_file_read_bytes` | `1048576` | bounded file reads |
| `max_write_bytes` | `262144` | bounded structured writes |
| `max_diff_bytes` | `65536` | bounded returned diff |
| `activity_retention` | `5000` | audit activity retention |

Existing installations preserve these persisted values when application code is reinstalled.

## Targets and Projects

Targets, Projects, AI clients and grants live in the Registry and should normally be managed through Admin Console. Client grants are managed under **AI Clients → Grants**; direct SQLite editing is not part of the normal workflow. `config/targets.local.json` is a local compatibility/bootstrap file, not the production source of truth once SQLite is active.

A Target includes endpoint/user/platform information; identity is the Target ID plus its pinned SSH host key. **Targets → Edit Target** displays the pinned and currently presented SHA256 fingerprints and is the normal place to trust, explicitly replace or remove that pin. The Admin UI reuses the existing OpenSSH/known_hosts identity mechanism; it never auto-accepts a first or changed key.

A Project defines an authorized root plus read/write/task policy. A Project root confines structured filesystem tools but does not turn trusted shell into a filesystem sandbox.

## MCP adapter token

The MCP HTTP service reads:

```text
/home/mcp-gateway/.config/mcp-gateway/tunnel-mcp.token
```

The installer generates it when missing, mode `0600`. Never commit or print the token.

## Secure MCP Tunnel

Optional cloud tunnel configuration lives in:

```text
/home/mcp-gateway/.config/mcp-gateway/tunnel.env
```

It may include control-plane credentials, tunnel identity, backend URL and extra auth headers. The base installer does not invent cloud credentials. If tunnel configuration/client is absent, the local Gateway remains usable.

## Maintainer deployment config

Exact-commit deployment from a development host can use a local ignored `.mcp-pi.local.env`:

```bash
MCP_PI_HOST=<gateway-address>
MCP_PI_USER=<admin-user>
MCP_PI_IDENTITY_FILE=~/.ssh/<key>
```

Never commit this file.
