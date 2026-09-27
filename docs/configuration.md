# Configuration

This document describes the 1.5.0-rc.1 Go-only runtime.

## Paths

    runtime: /home/mcp-gateway/mcp-gateway
    Registry/backups: /home/mcp-gateway/.local/share/mcp-gateway
    private config: /home/mcp-gateway/.config/mcp-gateway

Runtime files are root-owned. Mutable state required by the daemon belongs to mcp-gateway.

## Gateway / Registry environment

MCP_GATEWAY_DB can override the SQLite path for CLI/tests. Production uses the persistent data path above.

Fresh Registry defaults are gateway_enabled=true, writes_enabled=false and shell_enabled=false. default_timeout controls bounded remote operations; it is not the Admin session lifetime.

## Admin Console

Common local environment values:
- MCP_ADMIN_HOST — default 127.0.0.1;
- MCP_ADMIN_PORT — default service port from admin.env;
- MCP_ADMIN_ALLOWED_HOSTS — explicit Host allowlist;
- MCP_ADMIN_SECRET_FILE — exact file containing the secure-cookie signing secret.

The signing secret is local private state. Regenerating it invalidates existing Admin sessions.

## Targets and Projects

Target identity is the configured Target plus its pinned SSH host key. Host/IP/port are endpoint data.

Projects define authorized roots and read/write enablement. Native remote operations use POSIX utilities on Unix-like Targets and PowerShell on Windows. No additional language runtime is required on Targets.

## Privilege policy

Target administrative privilege remains separate from ordinary target_shell access. Configure target_admin grants and the per-Target privilege policy deliberately; unavailable privilege backends fail closed.

## MCP HTTP token

The private token is stored under the service config directory and read by the MCP service. Do not commit it or echo it into logs.

## Secure tunnel

Tunnel configuration is optional and private. Base installation does not enable it merely because the unit exists.

## Maintainer deployment

Local maintainer connection details belong in .mcp-pi.local.env, which is not a release artifact. Exact-commit deployment must still pass the repository gates and remote preflight.
