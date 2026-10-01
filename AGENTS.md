# AGENTS.md — automation operational contract

## Purpose

This file adds rules for automated contributors. Human development/release/deployment procedure is canonical in `CONTRIBUTING.md`; product behavior is canonical in code/tests/metadata and the CURRENT operator guides.

## Principles

Use **KISS + Reuse First + Least Privilege + Deny by Default + Fail Closed + Evidence Before PASS**.

Before mutation:

1. prove Target/project/repository/branch/worktree identity;
2. inspect the existing mechanism before adding another;
3. limit changes to the requested cause;
4. preserve rollback/audit evidence;
5. verify observable state rather than treating exit code zero as sufficient evidence.

## Product boundaries

MCP, Admin, CLI lifecycle operations and policy decisions share one Go Core and one SQLite Registry. Do not add a parallel policy path, secondary runtime or hidden compatibility bridge.

The appliance runtime is a static Go gateway plus SQLite, systemd, thin lifecycle shell and OpenSSH. Release installation must not require a compiler/development runtime.

Runtime code is root-owned. Only persistent data, private config, backups and SSH material required by the service are writable by `mcp-gateway`.

## Authorization and identity

Normal delegated access requires authenticated Client identity plus explicit Target/Project/capability scope and applicable kill switches.

Structured writes additionally require write policy. Trusted shell additionally requires shell policy. Target administrative privilege is a second gate requiring explicit `target_admin`, Target privilege policy, any required approval/boot identity and a verified native backend. Missing proof means deny.

Treat Target ID + pinned SSH host key as identity. Host/IP/port are endpoints. Preserve strict host-key checking and never auto-trust a replacement key.

## Lifecycle authority

User install/reinstall/update/rollback goes through `install.sh`. systemd/`mcp-gateway.target` owns normal process lifecycle. Maintainer deployment uses the resumable exact-commit path documented in `CONTRIBUTING.md` and delegates activation/rollback to the same installer.

Registry open is non-migrating. Schema transformation is explicit; restore preserves backup schema. Use the Go SQLite backup/restore implementation rather than copying a live database.

Keep `NoNewPrivileges`; gateway reboot uses systemd-logind plus narrow polkit, never arbitrary sudo.

## Documentation

Do not add another documentation tree for a behavior already covered by README, the CURRENT guides, CONTRIBUTING, AGENTS, CHANGELOG, executable `--help`, machine-readable metadata, systemd units or tests.

Historical reconstruction belongs to Git history. Do not keep a parallel archive tree in HEAD unless a concrete current audit requirement cannot be satisfied by immutable Git/tag/release evidence.

When behavior changes, update the single relevant CURRENT source and remove obsolete parallel instructions.

## Workflow

For substantive work:

1. prove Git/environment state;
2. inspect code/tests/docs;
3. implement the smallest coherent change;
4. review the complete diff;
5. run focused tests;
6. run the relevant full gates defined by the repository/CI;
7. correct documentation and redundant paths;
8. commit/push only after local evidence is clean;
9. require successful exact-SHA remote CI before any release tag;
10. deploy only in an explicit later promotion/validation phase.

Do not mutate production merely to make it match source/documentation.

## STOP conditions

Stop mutation and investigate when:

- Git or target/project identity is unexpected;
- a destructive operation lacks verified recovery evidence;
- Core/Registry readiness is unavailable;
- a host key changes unexpectedly;
- Registry compatibility is unsupported;
- requested privilege cannot be proven;
- validation contradicts the intended change.

## Source-of-truth order

For source behavior: Go code/tests and executable/config metadata first, then CURRENT documentation.

For live production: gateway/deployment status, live Registry and systemd/readiness evidence.

## Final rule

Never report PASS because an implementation looks plausible. Report only evidence actually observed.
