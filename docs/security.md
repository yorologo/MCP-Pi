# Security model

MCP-Pi 1.5.0-rc.2 applies KISS + Least Privilege + Deny by Default + Fail Closed.

## Core invariants

- one Go Core and one Registry authority;
- explicit client/grant/Target/Project scope;
- pinned SSH host keys;
- safe fresh-install kill-switch defaults;
- no runtime fallback;
- auditable critical mutations;
- root-owned program files;
- service-owned mutable state only.

## Fresh-install defaults

    gateway_enabled=true
    writes_enabled=false
    shell_enabled=false

## Structured writes

Structured filesystem tools validate relative paths, Project scope, destination type, symlink/reparse conditions and optimistic SHA where required. Critical writes require audit availability. An unsupported or ambiguous path state is denied.

## Trusted shell

run_command is intentionally broader than structured tools and therefore requires target_shell plus shell_enabled. Do not present it as a filesystem sandbox.

## Target administrative privilege

target_admin is separate from target_shell. A privilege request also requires Target consent policy and a verified native backend. General sudo on the gateway is not an accepted substitute.

## Kill switches

Disabled global/write/shell states produce denial. A disabled capability does not silently route elsewhere.

## SSH trust

Strict host-key checking remains enabled. Endpoint rediscovery cannot replace Target identity without matching the pinned host key.

## Admin Console

Fresh install binds to loopback. Sessions use signed cookies, HttpOnly and SameSite Strict behavior. Host allowlisting and CSRF checks remain enforced. The signing secret is local private state.

## MCP HTTP

Private bearer material remains outside Git. Readiness requires initialized Go Core/Registry. Liveness alone is not authorization/readiness proof.

## Backup and recovery

SQLite uses online backup/restore APIs. Restore is coordinated with service shutdown. Disaster-recovery archives are private mode 0600 and may be encrypted with age; requested encryption fails closed if age is unavailable.

## Appliance reboot

NoNewPrivileges remains enabled. Reboot is requested through systemd-logind with a narrow polkit action; mcp-gateway is not granted arbitrary sudo.

## Audit

Critical mutation/reboot/maintenance paths must not report success when required audit persistence fails. Audit data must not contain credentials or private key material.

## Privileged Target execution

`run_command` and allowlisted `run_task` share the same effective-privilege gate. If the Target transport is already root/Administrator or otherwise crosses the configured privilege boundary, an explicit `target_admin` grant plus the Target privilege policy and any required human approval must pass before execution. Privilege probing fails closed when the effective level cannot be established.
