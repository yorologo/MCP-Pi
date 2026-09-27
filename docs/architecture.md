# Architecture

MCP-Pi 1.5.0-rc.2 is a Go-only security gateway. The reference appliance centralizes MCP transport, policy, audit, Admin and lifecycle logic in one executable; Targets perform the delegated work.

## Components

    AI/MCP client -> Go MCP server -> Go Core -> Policy/SQLite Registry -> SSH -> Target
                          ^
                          |
                    Go Admin Console

There is no subprocess bridge and no second canonical backend.

## Responsibilities

| Component | Responsibility |
| --- | --- |
| Go MCP server | stdio/HTTP MCP transport, authentication handoff, tool catalog |
| Go Core | canonical tool behavior, limits, audit and orchestration |
| Policy | client/grant/Target/Project/capability decisions |
| Registry | schema 5 settings, Targets, Projects, clients, grants, activity |
| Go Admin | human management of the same Registry/policy model |
| SSH transport | strict-host-key remote execution and native Target operations |
| systemd | service lifecycle, timer/postboot integration |
| logind/polkit | narrowly authorized appliance reboot |

## Request flow

1. authenticate/bind client identity;
2. resolve the tool and validate its schema;
3. evaluate global and scoped policy;
4. deny immediately when a required precondition fails;
5. execute the bounded local/remote operation;
6. record audit evidence;
7. return a structured result/error.

Missing Go Core or Registry state cannot route to a fallback implementation.

## Tool families

The catalog contains 21 tools covering read/introspection, structured filesystem mutations, allowlisted tasks/trusted shell and appliance administration.

run_command is a trusted Target shell for explicitly authorized clients; Project scope supplies authorization and initial CWD, not a general filesystem sandbox.

## Target execution

Unix-like Targets use native POSIX utilities through pinned SSH. Windows Targets use PowerShell. Target facts include platform/runtime/privilege evidence without requiring an additional language runtime.

Structured mutation safety is treated separately from general shell authorization; Project-root checks, symlink/reparse handling, optimistic hashes and atomic replace semantics must remain fail-closed.

## Privileged Target execution

Privilege is a second policy gate:
- normal operation authorization first;
- explicit target_admin grant;
- Target privilege policy;
- approval/boot constraint when required;
- verified native backend.

Do not infer administrator capability from installed tooling alone.

## Identity versus endpoint

Target ID + pinned SSH host key establish identity. Address and port are mutable endpoints.

## Runtime layout

    /home/mcp-gateway/mcp-gateway/               root-owned runtime
    /home/mcp-gateway/.local/share/mcp-gateway/ Registry/backups
    /home/mcp-gateway/.config/mcp-gateway/       private mutable config
    /etc/systemd/system/                          installed units

## Registry lifecycle

Schema 5 is canonical. Fresh install creates v5; the direct Go migration path supports v4 to v5 and v5. SQLite backup/restore uses the existing modernc.org/sqlite online API.

## Installation and deployment

install.sh is the user lifecycle entrypoint. scripts/build-release-package.sh builds the immutable Go-only ARMv6 artifact. scripts/deploy-pi.sh promotes exactly that artifact and exact Git commit.

## Resource model

Heavy Go tests, frontend builds, vulnerability analysis and cross-compilation run off-appliance. The Raspberry Pi A+ runs the static gateway binary, SQLite and system services.

Current performance evidence comes from mcp-gateway benchmark and must represent measurements from that execution; historical migration baselines are not printed as if they were current results.
