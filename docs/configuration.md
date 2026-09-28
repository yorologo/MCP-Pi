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

## MCP HTTP tokens and optional ingress

Private bearer/token material remains under the service config directory and outside Git/logs.

The primary ChatGPT ingress uses `tunnel-mcp.token` on `127.0.0.1:8090`. An optional Gemini ingress uses a distinct `gemini-mcp.token` on port `8092` and a distinct Registry identity, `gemini-main`. The packaged unit listens on `0.0.0.0:8092` so a private Cloudflare Tunnel hostname route can reach it through the appliance LAN address; it remains installed disabled by default, requires the dedicated Bearer token, and allows only the explicit `gemini-mcp.internal` Host in addition to loopback defaults. The installer never invents or copies the token into release artifacts.

External ingress remains replaceable infrastructure. The OpenAI secure tunnel is optional and base installation does not enable it automatically. A Cloudflare Tunnel or another provider-specific edge may forward to the optional Gemini ingress, but provider credentials and tunnel configuration remain private mutable state outside MCP-Pi Core/Registry policy.
