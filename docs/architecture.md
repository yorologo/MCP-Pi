# Architecture

MCP-Pi is a Go-only security gateway. MCP transport, Admin, policy, audit and lifecycle logic share one Core and one SQLite Registry.

## System boundary

    MCP client ---> Go MCP server ----+
                                      |
    Admin browser -> Go Admin --------+-> Go Core -> Policy/Registry -> SSH -> Target
                                                     |
                                                     +-> Audit

OS integrations remain outside the process:

    systemd -> service lifecycle / timer / postboot
    logind + polkit -> controlled reboot
    OpenSSH -> pinned Target transport

There is no per-call bridge process or alternate Core.

## Responsibilities

| Component | Responsibility |
| --- | --- |
| MCP server | stdio/Streamable HTTP transport and bound client identity |
| Go Core | tool behavior, limits, policy orchestration and audit |
| Policy | Client/Grant/Target/Project/capability decisions |
| Registry | settings, Targets, Projects, Clients, Grants and Activity |
| Go Admin | human management of the same Registry/policy model |
| SSH transport | strict-host-key remote execution/native Target operations |
| systemd | appliance lifecycle, maintenance timer, postboot, optional ingress |
| `install.sh` | install/update/migrate/rollback lifecycle |
| `deploy-pi.sh` | maintainer exact-commit transport/acceptance around the installer |

## Identity and request flow

Client identity is bound before tool discovery/invocation. Request arguments cannot replace the authenticated/forced identity.

`tools/list` is projected through the same authorization model used by execution, so a client can see fewer tools than exist in the Core catalog.

Request flow:

1. bind client identity;
2. validate tool/arguments;
3. evaluate global and scoped policy;
4. deny immediately when a precondition fails;
5. execute the bounded local/remote operation;
6. persist required audit evidence;
7. return structured result/error.

Missing Core/Registry state never falls back to another implementation.

## Target execution

Unix-like Targets use native POSIX utilities over pinned SSH; Windows Targets use PowerShell. Target ID plus pinned SSH host key is identity. Address/port are endpoint data.

Structured filesystem mutation validates Project root, canonical path, symlink/reparse state, bounds/conflicts and audit availability. It never silently degrades into trusted shell.

Privilege is a second authorization gate over an otherwise authorized execution; its security semantics are documented in [security.md](security.md).

## Runtime and Registry

    /home/mcp-gateway/mcp-gateway/               root-owned runtime
    /home/mcp-gateway/.local/share/mcp-gateway/ Registry/backups
    /home/mcp-gateway/.config/mcp-gateway/       private mutable config

Runtime Registry open is non-migrating and requires the runtime's current schema. Read-only inspection is non-migrating. Restore preserves the backup schema exactly.

Schema transformation is explicit and coordinated:

    quiesce Registry users
      -> verified backup
      -> migrate
      -> status
      -> start appliance
      -> Doctor

The exact supported migrations live in Registry migration code/tests rather than duplicated prose.

## Compatibility contract

Machine-readable authority:

- `manifest.json` — package/release contract;
- `compatibility.json` — runtime/API/catalog/schema/protocol contract;
- `mcp-gateway version --json` — exact binary contract;
- Registry constants/migrations/tests — database implementation;
- Core catalog/policy — available and client-visible tool behavior.

Installer and project-contract tests require these sources to agree. Newer-than-runtime or unsupported Registry schemas fail closed.

A declared/cross-built architecture is not production evidence; ARMv6 acceptance still requires the real appliance.

## Installation, lifecycle and deployment

`install.sh` is the single activation/update/rollback engine. `mcp-gateway.target` is the canonical systemd lifecycle unit. Release packaging builds the immutable ARMv6 bundle; maintainer deployment proves exact Git provenance, transports that bundle, delegates activation/rollback to its installer and performs live acceptance.

The human release/deployment workflow belongs in `CONTRIBUTING.md` in a source checkout; executable gates belong in CI/scripts.

## Resource model

The reference appliance is constrained ARMv6 hardware. Tests, frontend builds, vulnerability analysis and cross-compilation belong on a development host; the appliance runs the static gateway, SQLite and system services.
