# Architecture

MCP-Pi is a Go-only security gateway. MCP transport, Admin, policy, audit and lifecycle logic use one Core and one SQLite Registry.

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
| Go Core | canonical tool behavior, limits, policy orchestration and audit |
| Policy | client/grant/Target/Project/capability decisions |
| Registry | settings, Targets, Projects, clients, grants and activity |
| Go Admin | human management of the same Registry/policy model |
| SSH transport | strict-host-key remote execution and native Target operations |
| systemd | Admin/MCP services, maintenance timer, postboot and optional tunnel |
| `install.sh` | canonical install/update/migrate/rollback lifecycle |
| `deploy-pi.sh` | exact-commit promotion, transport, acceptance and provenance |

## MCP transports and identity

Stdio is used for forced-command/local integrations. Streamable HTTP is used by the appliance MCP service and optional secure tunnel.

Client identity is bound before tool discovery/invocation. A forced SSH command can bind a registered client ID to a dedicated SSH key; HTTP authentication binds its configured client identity. Request arguments cannot replace that identity.

`tools/list` is projected through the same authorization model used by execution, so a client may see fewer tools than the Core catalog.

## Request flow

1. bind authenticated client identity;
2. validate tool and argument schema;
3. evaluate global and scoped policy;
4. deny immediately when a precondition fails;
5. execute the bounded local/remote operation;
6. persist required audit evidence;
7. return the structured result/error.

Missing Core/Registry state never falls back to another implementation.

## Target execution

Unix-like Targets use native POSIX utilities through pinned SSH. Windows Targets use PowerShell. Target identity is the configured Target plus its pinned SSH host key; address and port are mutable endpoint data.

Structured filesystem mutations validate Project root, canonical path, symlink/reparse state, size/conflict rules and audit availability. They never silently degrade into trusted shell.

## Target privilege

Privilege is a second gate over otherwise authorized execution:

    normal execution authorization
      + explicit target_admin
      + Target privilege policy
      + required approval/boot identity
      + verified native backend

`run_command` and allowlisted `run_task` share this effective-privilege gate.

## Runtime and Registry lifecycle

    /home/mcp-gateway/mcp-gateway/               root-owned runtime
    /home/mcp-gateway/.local/share/mcp-gateway/ Registry/backups
    /home/mcp-gateway/.config/mcp-gateway/       private mutable config

Runtime Registry open is non-migrating and requires the current schema. Read-only inspection uses SQLite `mode=ro`.

Schema change is explicit:

    stop DB users
      -> verified backup
      -> mcp-gateway migrate
      -> non-mutating status
      -> start services
      -> Doctor

Restore preserves the source schema exactly.

## Installation and deployment

`install.sh` is the single activation/update/rollback engine. The release builder creates the immutable ARMv6 bundle. `deploy-pi.sh` validates exact Git provenance, transfers the canonical bundle, delegates activation/rollback to its installer, performs production acceptance and records provenance.

## Version contract

Do not duplicate build/API/schema/protocol constants in architecture prose. Machine-readable authority is `manifest.json`, `compatibility.json` and `mcp-gateway version --json`. Source checkouts provide deeper compatibility semantics in `docs/reference/compatibility.md`.

## Resource model

The reference appliance is constrained ARMv6 hardware. Tests, frontend builds, vulnerability analysis and cross-compilation belong on a development host; the appliance runs the static gateway binary, SQLite and system services.
