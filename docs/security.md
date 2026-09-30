# Security model

MCP-Pi applies **KISS + Least Privilege + Deny by Default + Fail Closed**.

## Core invariants

- one Go Core and one Registry authority;
- explicit Client/Target/Project/capability scope;
- internal principals cannot be claimed by external clients;
- pinned SSH host keys;
- writes and trusted shell disabled on a fresh Registry;
- no fallback runtime/policy path;
- auditable critical mutations;
- root-owned program files and service-owned mutable state only.

## Grants and scope

A Grant authorizes one explicit capability at a scope:

    Client -> Target -> Project -> Capability -> Enabled

Global/wildcard access exists only as a deliberate configuration choice. The Core policy engine is the authority for both discovery and execution; **Check Effective Access** reuses that policy instead of implementing a UI-only approximation.

A global appliance tool requires global scope, a Target operation requires Target scope, and a Project operation requires its Project scope. A capability being present in the catalog does not widen its execution scope.

## Structured writes

Structured mutation tools require all applicable gates simultaneously:

- compatible write Grant;
- global writes enabled;
- Project writes enabled;
- enabled Target/Project;
- path/canonicalization/symlink/reparse validation;
- bounds/conflict checks;
- required audit availability.

Existing-file mutation can create recovery evidence before change. Registry backup is a different SQLite operation.

A structured operation never silently degrades into `run_command`. Unsupported or ambiguous path state is denied.

## Trusted shell

`run_command` requires ordinary authorization, an explicit Project and the global shell switch. Its Project supplies authorization scope/initial cwd; trusted shell is not a filesystem sandbox.

Prefer structured tools or allowlisted Tasks whenever they cover the required operation.

## Target administrative privilege

OS privilege is a second authorization gate, not another tool and not implied by ordinary shell access.

Privileged execution requires:

    ordinary execution authorization
      AND explicit target_admin
      AND Target privilege policy
      AND required approval / boot identity
      AND verified privilege backend

Wildcard ordinary access does not silently grant Target administrative privilege.

`run_command` and allowlisted `run_task` use the same effective-privilege gate. An already-elevated transport is also treated as privileged and must satisfy that gate.

Human approval is scoped to the same Client/Target/Project. One-use approval is consumed only by an authorized privileged request; boot-scoped approval fails closed when boot identity cannot be proven.

Native privilege mechanisms are reused after authorization (for example verified root/Administrator SSH or Shizuku/rish where supported). MCP-Pi does not weaken host sudo/UAC policy or filter arbitrary shell strings as a substitute for authorization.

## Kill switches and SSH trust

Disabled gateway/write/shell state denies the operation; it never routes elsewhere.

Strict host-key checking remains enabled. Endpoint rediscovery may find a new address only for the already pinned Target identity; it cannot replace trust in a changed key.

## Admin and MCP HTTP

Admin defaults to loopback and enforces Host validation, CSRF protection, signed HttpOnly/SameSite cookies, safe response headers and login rate limiting.

MCP bearer material remains outside Git. Invalid supplied credentials or an unauthenticated asserted Client ID are rejected. An anonymous/NONE principal receives no delegated authority.

`/live` proves process liveness only; `/ready` requires initialized Core/Registry.

## Backup and recovery

Registry backup/restore uses SQLite online APIs. Restore requires Registry users quiesced and preserves the source schema exactly. Migration is a separate explicit lifecycle operation.

Private SSH/host/tunnel credentials are reprovisioned from their authoritative secure source; MCP-Pi does not maintain an untested second secret archive.

## Appliance privilege

Services keep `NoNewPrivileges`. Gateway reboot uses systemd-logind and a narrow polkit action; the service account is not granted general sudo.

## Audit

Security-sensitive operations must not report success when required audit persistence fails.

Registry-backed Admin mutations persist state and successful Activity evidence transactionally where applicable. External security mutations require durable audit intent before the external effect plus observed result afterward; MCP-Pi does not pretend SQLite and the host filesystem share one transaction.

Audit data must not contain credentials or private key material.
