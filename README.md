# MCP-Pi Gateway

MCP-Pi is a small private MCP security gateway for delegating controlled work to authorized Targets. It does not run an LLM. One Go runtime authenticates clients, evaluates policy, audits decisions and performs bounded operations over pinned SSH.

Machine-readable version and compatibility authority is `manifest.json`, `compatibility.json` and `mcp-gateway version --json`. Published software is identified by immutable Git tags/GitHub Releases; a running appliance is authoritative only for its own live deployment provenance and acceptance evidence.

## Principles

MCP-Pi follows **KISS + Reuse First + Least Privilege + Deny by Default + Fail Closed + Evidence Before PASS**.

- one Go Core and one SQLite Registry authority;
- explicit Client → Target → Project → capability grants;
- pinned SSH host identity;
- structured tools preferred over trusted shell;
- Target administrative privilege is a separate authorization gate;
- root-owned runtime with service-owned mutable state;
- no runtime fallback when Core/Registry state is unavailable.

## Quick start

### 1. Obtain MCP-Pi

For the reference Raspberry Pi appliance, prefer an official GitHub Release bundle because it already contains the Linux ARMv6 binary.

For development or a machine with Go already installed:

    git clone https://github.com/yorologo/MCP-Pi.git
    cd MCP-Pi

A source checkout does not install a compiler. See [Installation](docs/installation.md) for release-bundle contents and requirements.

### 2. Install and start

    sudo ./install.sh

The installer validates preconditions, installs the runtime, creates or explicitly migrates the Registry, installs systemd units, starts the canonical `mcp-gateway.target` and runs Doctor.

Optional non-mutating candidate validation:

    ./install.sh --check

A successful candidate-only check emits `CANDIDATE_CHECK=PASS`; it is not proof that the current host is installable.

### 3. Complete Admin bootstrap

A fresh interactive install can configure Admin directly. A fresh non-interactive install creates a private, 15-minute one-time token for `/setup` and prints only the local token-file path.

Local recovery/automation path:

    sudo -u mcp-gateway mcp-gateway setup

Admin binds to loopback by default. From another machine, use an SSH port forward instead of exposing Admin unnecessarily:

    ssh -L 8080:127.0.0.1:80 <user>@<appliance>

Then open `http://127.0.0.1:8080/`.

### 4. Minimal configuration

In Admin Console:

1. add a Target and verify its pinned SSH host identity;
2. add a Project root and, when possible, allowlisted Tasks;
3. register the AI/MCP client;
4. grant only the capabilities it needs;
5. use **Check Effective Access** before enabling broader permissions.

Fresh Registry defaults keep structured writes and trusted shell disabled.

### 5. Verify

    sudo -u mcp-gateway mcp-gateway status
    sudo -u mcp-gateway mcp-gateway doctor

Use `doctor --check-targets` when remote Target reachability must participate in the verdict.

## Normal operation

systemd owns service lifecycle through `mcp-gateway.target`. The Go CLI provides `status`, `doctor`, `backup` and `maintenance`. Exact operational commands and logging are in [Operations](docs/operations.md).

Updates and installer rollback reuse `install.sh`; Registry restore and schema migration remain separate explicit recovery operations. See [Installation](docs/installation.md) and [Recovery](docs/recovery.md).

## Documentation

| Need | Source |
| --- | --- |
| Install, bootstrap, update, rollback | [docs/installation.md](docs/installation.md) |
| Admin, Targets, Projects, Clients, ChatGPT/Gemini connection and ingress | [docs/configuration.md](docs/configuration.md) |
| Start/stop, health, maintenance, logs, benchmark | [docs/operations.md](docs/operations.md) |
| Troubleshooting, backup/restore, recovery | [docs/recovery.md](docs/recovery.md) |
| Components, lifecycle and compatibility semantics | [docs/architecture.md](docs/architecture.md) |
| Authorization, privilege and security invariants | [docs/security.md](docs/security.md) |
| Development, CI, release and deployment (source checkout) | `CONTRIBUTING.md` |
| Automation-specific rules (source checkout) | `AGENTS.md` |
| Change history | [CHANGELOG.md](CHANGELOG.md) |

Historical evidence that still has value is isolated under `docs/archive/`; it is not current operating guidance.
