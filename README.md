# MCP-Pi Gateway

MCP-Pi is a small private MCP security gateway for delegating controlled work to authorized Targets. It does not run an LLM. One Go runtime authenticates clients, evaluates policy, audits decisions and performs bounded operations over pinned SSH.

The current source candidate is **1.5.0-rc.3**. Production is a separate authority until an exact commit is deployed and accepted. Machine-readable runtime/API/schema/protocol metadata lives in `manifest.json`, `compatibility.json` and `mcp-gateway version --json`.

## Security model

MCP-Pi follows **KISS + Reuse First + Least Privilege + Deny by Default + Fail Closed + Evidence Before PASS**.

- one Go Core and one SQLite Registry authority;
- explicit client → Target → Project → capability grants;
- pinned SSH host identity;
- structured tools preferred over trusted shell;
- Target administrative privilege is a separate authorization gate;
- root-owned runtime with service-owned mutable state;
- Admin and MCP bind locally by default;
- no runtime fallback when Core/Registry state is unavailable.

## Quick start

For the reference Raspberry Pi appliance, an official release bundle is preferred because it already contains the ARMv6 binary.

    ./install.sh --check
    sudo ./install.sh

The installer validates the bundle, creates/migrates the Registry explicitly, installs systemd units, starts Admin/MCP and runs Doctor. If interactive setup was skipped, configure the Admin user afterward:

    sudo -u mcp-gateway mcp-gateway setup

Then verify:

    sudo -u mcp-gateway mcp-gateway status
    sudo -u mcp-gateway mcp-gateway doctor

`status` is non-mutating. Doctor is the appliance health check.

Admin binds to loopback by default. Use an SSH port forward unless another trusted exposure is deliberately configured.

A source checkout can use the same installer when a matching prebuilt gateway exists or Go is already installed. The appliance installer does not install a compiler.

See [Installation](docs/installation.md) for the complete lifecycle and [Admin Console](docs/admin-console.md) for first configuration.

## First configuration

In Admin Console:

1. add a Target and verify its pinned SSH identity;
2. add a Project root;
3. register the client that will use MCP-Pi;
4. grant only the required capabilities;
5. use **Check Effective Access** before enabling broader operations.

Fresh Registry defaults keep structured writes and trusted shell disabled.

## Normal operation

    sudo -u mcp-gateway mcp-gateway status
    sudo -u mcp-gateway mcp-gateway doctor
    sudo -u mcp-gateway mcp-gateway backup
    sudo -u mcp-gateway mcp-gateway maintenance

systemd is the canonical start/stop/restart interface. Exact commands are in [Operations](docs/operations.md).

Updates use a new release bundle plus the same `install.sh`. Rollback uses the installed `install.sh --rollback`. Registry restore preserves the backup schema exactly; schema migration is a separate explicit operation.

## Architecture

    MCP client ---> Go MCP server ----+
                                      |
    Admin browser -> Go Admin --------+-> Go Core -> Policy/Registry -> SSH -> Target
                                                     |
                                                     +-> Audit

There is no second backend or per-call bridge runtime. See [Architecture](docs/architecture.md) and [Security](docs/security.md).

## Documentation

| Need | Canonical document |
| --- | --- |
| Install / bootstrap / update / rollback | [docs/installation.md](docs/installation.md) |
| Configuration | [docs/configuration.md](docs/configuration.md) |
| Daily operation / maintenance / diagnostics | [docs/operations.md](docs/operations.md) |
| Backup / restore / recovery | [docs/recovery.md](docs/recovery.md) |
| Troubleshooting | [docs/troubleshooting.md](docs/troubleshooting.md) |
| Admin Console | [docs/admin-console.md](docs/admin-console.md) |
| Architecture | [docs/architecture.md](docs/architecture.md) |
| Security | [docs/security.md](docs/security.md) |
Source checkouts also contain maintainer/automation references under `docs/README.md`, `CONTRIBUTING.md` and `AGENTS.md`; those are intentionally separate from release-bundle operator guidance.

Historical release/implementation evidence is isolated under `docs/releases/` and `docs/archive/`.
