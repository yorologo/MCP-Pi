# Configuration

## Runtime paths

    runtime:          /home/mcp-gateway/mcp-gateway
    Registry/backups: /home/mcp-gateway/.local/share/mcp-gateway
    private config:   /home/mcp-gateway/.config/mcp-gateway

Runtime files are root-owned. Mutable daemon state belongs to the `mcp-gateway` service account.

## Gateway / Registry

`MCP_GATEWAY_DB` can override the SQLite path for CLI/tests. Production uses the persistent data path above.

Fresh Registry defaults are:

    gateway_enabled=true
    writes_enabled=false
    shell_enabled=false

`default_timeout` bounds remote operations; it is not the Admin session lifetime.

## Admin Console

Common local environment values:
- `MCP_ADMIN_HOST` — default `127.0.0.1`;
- `MCP_ADMIN_PORT` — service port;
- `MCP_ADMIN_ALLOWED_HOSTS` — explicit Host allowlist;
- `MCP_ADMIN_SECRET_FILE` — secure-cookie signing secret file.

The signing secret is private mutable state. Regenerating it invalidates existing Admin sessions.

`admin_timezone` is stored in Registry settings and defaults to `UTC`. It accepts an IANA identifier such as `America/Mexico_City`. It affects presentation/date filters only; audit timestamps remain stored in UTC.

## Targets and Projects

Target identity is the configured Target plus its pinned SSH host key. Host/IP/port are endpoint data.

Projects define authorized roots and read/write enablement. Unix-like Targets use native POSIX capabilities; Windows Targets use PowerShell.

## Privilege policy

`target_admin` is separate from ordinary `target_shell`. Configure both the grant and per-Target privilege policy deliberately. An unavailable privilege backend fails closed.

## MCP HTTP token and optional tunnel

Private bearer/token material remains under the service config directory and outside Git/logs.

The secure tunnel is optional. Base installation does not enable it automatically. Tunnel startup waits for local MCP readiness before connecting.
