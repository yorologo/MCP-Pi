# Configuration

This document is the operator source for Admin Console, Targets/Projects/Clients and optional ingress. Exact CLI flags belong to `mcp-gateway <command> --help`.

## Runtime paths

    runtime:          /home/mcp-gateway/mcp-gateway
    Registry/backups: /home/mcp-gateway/.local/share/mcp-gateway
    private config:   /home/mcp-gateway/.config/mcp-gateway

Runtime files are root-owned. Mutable daemon state belongs to the `mcp-gateway` service account.

`MCP_GATEWAY_DB` can override the SQLite path for CLI/tests. Production uses the persistent data path above.

## Admin access and first login

Admin binds to loopback by default. From a remote workstation use an SSH forward:

    ssh -L 8080:127.0.0.1:80 <user>@<appliance>

Then open `http://127.0.0.1:8080/`.

A fresh non-interactive install exposes `/setup` only while no enabled Admin exists and a valid one-time bootstrap token is present. Successful Web or CLI setup removes the token.

Local setup/recovery:

    sudo -u mcp-gateway mcp-gateway setup

Common Admin environment overrides are:

- `MCP_ADMIN_HOST` — bind override; Go supplies the default;
- `MCP_ADMIN_PORT` — port override; Go supplies the default;
- `MCP_ADMIN_ALLOWED_HOSTS` — explicit Host allowlist;
- `MCP_ADMIN_SECRET_FILE` — private cookie-signing secret.

Regenerating the signing secret invalidates existing Admin sessions.

`admin_timezone` is a Registry setting using an IANA timezone name. It changes presentation/date filters only; audit timestamps remain UTC.

## Initial model

The normal configuration order is:

    Target -> Project -> Client -> Grants -> Check Effective Access

### Target

Target identity is the configured Target plus its pinned SSH host key. Host/IP/port are mutable endpoint data.

After creation, verify the presented SSH host identity and **Check Connection** before considering the Target ready. **Find moved Target** can locate a changed endpoint among known neighbors only when the pinned key matches; it reports a candidate and never silently rewrites trust.

### Project

A Project defines the authorized root and read/write policy. `run_command` requires an explicit Project; MCP-Pi does not guess scope.

Projects can also define typed, allowlisted Tasks for `run_task`. Prefer those or structured tools when they satisfy the job instead of broad trusted shell.

### Client and Grants

Each Grant authorizes one capability at an explicit scope. Use the narrowest Target/Project/capability that works. Wildcard access and broad administrative privilege are deliberate high-impact choices.

**Check Effective Access** uses the real policy engine. For `run_command` it also evaluates the live Target privilege gate without consuming a pending one-use approval.

## Safety defaults

Fresh Registry defaults keep:

    gateway_enabled=true
    writes_enabled=false
    shell_enabled=false

Enable structured writes or trusted shell only after Project boundaries and grants are correct.

Target administrative privilege is separate from ordinary shell access; see [security.md](security.md).

## Optional MCP ingress

Private tokens/configuration live under the service config directory and never in Git or release artifacts.

The primary external-client path terminates at the loopback MCP service. Before any secure tunnel is considered healthy, the MCP service must be ready, its private bearer token must exist and the intended client must have effective grants. The tunnel transports MCP; authorization remains in Go Core/Policy.

An optional Gemini ingress has a distinct token and Registry identity and is installed disabled by default. It may be exposed only through deliberately configured trusted/private infrastructure.

The packaged Cloudflare connector is also disabled by default. Its token remains private mutable state. Doctor may report only safe local evidence such as resolved binary path, file size/SHA-256 fingerprint and token ownership/mode; it does not expose token contents or claim external provenance from a locally computed hash.

Admin is not part of the external MCP path and should remain loopback unless the operator deliberately establishes another trusted access model.
