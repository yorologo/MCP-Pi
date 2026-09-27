# Security model

MCP-Pi applies **KISS + Least Privilege + Deny by Default + Fail Closed**.

## Core invariants

- one Go Core and one Registry authority;
- explicit client/grant/Target/Project scope;
- `local`, `admin`, `system` and `test` are reserved internal principals; external MCP transports and Admin-managed AI Clients may not claim them;
- pinned SSH host keys;
- writes and trusted shell disabled on a fresh Registry;
- no runtime fallback;
- auditable critical mutations;
- root-owned program files;
- service-owned mutable state only.

## Structured writes

Structured filesystem tools validate Project scope, relative/canonical paths, destination type, symlink/reparse conditions, bounds and optimistic SHA where required. Unsupported or ambiguous path state is denied. Critical mutations require audit availability.

## Trusted shell and Target privilege

`run_command` requires ordinary shell authorization plus the global shell switch. It is not a filesystem sandbox.

Administrative Target privilege is a second gate. `run_command` and allowlisted `run_task` share the same effective-privilege check. Crossing into root/Administrator requires explicit `target_admin`, Target privilege policy, any required human approval/boot identity and a verified backend.

Admin may offer an approval scope only when the same Client/Target/Project passes both ordinary Target-shell authorization and the explicit `target_admin` gate. `ask_always` approvals are one-use and short-lived; `ask_once_per_boot` approvals are bound to the Target's observed boot ID and fail closed when boot identity is unavailable.

General sudo on the gateway is not a substitute.

## Kill switches and SSH trust

Disabled gateway/write/shell states deny the operation; they never route elsewhere.

Strict host-key checking remains enabled. Endpoint rediscovery cannot replace Target identity without the pinned host key.

## Admin and MCP HTTP

Admin binds to loopback by default and enforces signed HttpOnly/SameSite cookies, Host allowlisting and CSRF.

MCP private bearer material remains outside Git. `/live` only proves process liveness; `/ready` requires initialized Core/Registry and is the traffic-readiness signal.

## Backup and recovery

Registry backup/restore uses SQLite online APIs. Restore requires database users stopped and preserves the source schema exactly. Migration is a separate explicit lifecycle operation.

MCP-Pi does not maintain a second supported archive of host SSH/private/tunnel secrets; those are reprovisioned from their authoritative secure source during disaster recovery.

## Appliance reboot

`NoNewPrivileges` remains enabled. Reboot is requested through systemd-logind with a narrow polkit action; the service account is not granted arbitrary sudo.

## Audit

Critical mutation/reboot/maintenance paths must not report success when required audit persistence fails. Security-sensitive Admin mutations backed by the Registry persist the successful Activity entry in the same SQLite transaction as the state change, so audit failure rolls the mutation back. Cached privilege-approval cleanup and one-use approval consumption also propagate persistence/commit failures instead of authorizing through them.

Security mutations outside SQLite, such as SSH host-key trust changes, require a durable audit-intent entry before the external mutation and then record its observed success/failure result. This preserves fail-closed audit availability without pretending the filesystem and SQLite share one transaction.

Audit data must not contain credentials or private key material.
