# MCP-Pi Gateway

MCP-Pi is a small private MCP security gateway for delegating controlled work to machines on a trusted network. It does not run an LLM. It authenticates clients, applies policy, audits decisions and sends bounded operations to configured Targets over pinned SSH.

## Current baseline

| Contract | Candidate source |
| --- | --- |
| Gateway | 1.5.0-rc.1 |
| Runtime | Go-only |
| Core API | 1 |
| Bridge API | 1 |
| Tool catalog | v4 / 21 tools |
| Registry schema | 5 |
| MCP protocol | 2026-07-28 |

The source candidate is 1.5.0-rc.1. Production remains a separate authority until an exact candidate commit is deployed and accepted. Historical immutable releases remain under docs/releases and Git tags.

## Security model

MCP-Pi follows KISS, Reuse First, Least Privilege, Deny by Default, Fail Closed and Evidence Before PASS.

- Admin and MCP use the same Go Core and SQLite Registry.
- Fresh Registries enable the gateway but disable structured writes and trusted shell.
- Clients receive explicit grants scoped by Target, Project and capability.
- SSH host keys are pinned; endpoint changes do not redefine Target identity.
- Structured filesystem tools are preferred over shell.
- Trusted shell and Target administrative privilege require separate explicit authorization.
- Runtime code is root-owned; mutable data/config belongs to the service account.
- Admin binds to loopback by default.
- Missing Core/Registry state never falls back to another runtime.

## Install

For the Raspberry Pi A+ use the release bundle with its prebuilt ARMv6 binary:

    ./install.sh --check
    sudo ./install.sh
    sudo -u mcp-gateway mcp-gateway setup

No Python runtime is required on the appliance. A source checkout may build the Go binary only when Go is already installed; compilation belongs on a development host.

See [Getting started](docs/getting-started.md) and [Installation](docs/installation.md).

## Normal operation

    sudo -u mcp-gateway mcp-gateway status
    sudo -u mcp-gateway mcp-gateway doctor
    sudo -u mcp-gateway mcp-gateway backup
    sudo -u mcp-gateway mcp-gateway maintenance
    sudo -u mcp-gateway mcp-gateway version --json

Fresh Registry defaults:

    gateway_enabled = true
    writes_enabled  = false
    shell_enabled   = false

## Update and rollback

User update and maintainer promotion remain separate:

    user install/update -> release bundle -> sudo ./install.sh
    user rollback       -> installed install.sh --rollback
    maintainer deploy   -> run-resumable.sh -> deploy-pi.sh <exact-sha>

The deployment path builds the same Go-only bundle used for releases, creates an online SQLite backup, validates the candidate, activates it transactionally and preserves a rollback set. See [Update and rollback](docs/update-rollback.md).

## Architecture

One Go executable contains MCP transport, Core, policy, Admin Console, Registry migrations, Doctor, backup/restore and maintenance. SQLite is accessed with modernc.org/sqlite. Remote work uses the existing OpenSSH transport and native POSIX/PowerShell capabilities according to the Target platform.

See [Architecture](docs/architecture.md) and [Security](docs/security.md).

## Documentation map

| Need | Document |
| --- | --- |
| First installation | [docs/getting-started.md](docs/getting-started.md) |
| Install details | [docs/installation.md](docs/installation.md) |
| Runtime configuration | [docs/configuration.md](docs/configuration.md) |
| Daily operations | [docs/operations.md](docs/operations.md) |
| Update / rollback | [docs/update-rollback.md](docs/update-rollback.md) |
| Backup / recovery | [docs/recovery.md](docs/recovery.md) |
| Troubleshooting | [docs/troubleshooting.md](docs/troubleshooting.md) |
| Architecture | [docs/architecture.md](docs/architecture.md) |
| Security | [docs/security.md](docs/security.md) |
| Version contract | [docs/reference/compatibility.md](docs/reference/compatibility.md) |
| Maintainers | [CONTRIBUTING.md](CONTRIBUTING.md) |
| Automation agents | [AGENTS.md](AGENTS.md) |

## Development

Run heavy validation on a development host:

    cd mcp-adapter
    go test -count=1 ./...
    go vet ./...
    go mod tidy -diff
    cd ..
    scripts/verify-go-only.sh
    node mcp-adapter/internal/admin/app_js_test.mjs
    (cd tailwind && npm run build)
    ./install.sh --check
    git diff --check

The ARMv6 release build and exact-commit deployment are maintainer operations. Do not treat a local or CI PASS as evidence that production is already running that commit.

## Project status

[docs/project-state.md](docs/project-state.md) defines the source/integration state and the evidence required before promotion. MCP-Pi intentionally avoids adding dependencies or abstractions without a concrete operational benefit.
