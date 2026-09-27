# AGENTS.md — operational contract for AI/automation

## Purpose

This file defines how automated contributors must work on MCP-Pi. The current source candidate is 1.5.0-rc.1 and the product runtime is Go-only.

## Non-negotiable principles

Use KISS + Reuse First + Least Privilege + Deny by Default + Fail Closed + Evidence Before PASS.

Before mutating:
1. prove the repository/Target/project and current Git state;
2. understand the existing mechanism before adding another;
3. limit changes to the requested cause;
4. preserve rollback and audit evidence;
5. never infer success from a command merely returning zero when observable state can be checked.

## One Core

MCP, Admin, CLI lifecycle commands and policy decisions use the same Go Core and Registry. Do not add a parallel policy path, secondary runtime or hidden compatibility bridge.

Python is not a project/runtime dependency. Historical behavior that remains valuable is represented by frozen JSON fixtures and Go tests.

## Authorization model

Normal access is explicit:
- AI client identity;
- Target;
- Project;
- capability grant;
- global and Target kill switches.

Structured writes require writes_enabled and Project write permission. Trusted shell additionally requires shell_enabled and target_shell authorization.

### Target administrative privilege

Administrative execution is a second gate, not a shortcut. Require normal authorization first, then an explicit target_admin grant, Target privilege policy and a verified native privilege backend. Missing proof means deny.

## SSH identity

Treat Target ID + pinned host key as identity. Host/IP/port are endpoints. Preserve StrictHostKeyChecking and do not auto-trust replacement keys.

## Runtime boundaries

Production runtime:
- one Go gateway executable;
- SQLite Registry;
- systemd;
- thin shell lifecycle scripts;
- OpenSSH transport;
- embedded Admin templates/static files.

The appliance must not require a compiler or development tooling when installed from a release bundle.

Runtime code is root-owned. Only persistent data, local config, backups and SSH material that the service must update are writable by mcp-gateway.

## Installation versus deployment

install.sh is the user install/reinstall/rollback entrypoint.

scripts/deploy-pi.sh is the maintainer exact-commit promotion path and must be launched through scripts/run-resumable.sh unless explicit break-glass is used. It must build the same canonical release bundle, validate it before activation and preserve rollback state.

Do not deploy production merely to make it match source or documentation.

## Registry lifecycle

Schema version is 5. Fresh install creates v5. Direct Go migration supports v4 to v5 and current v5. Do not claim support for older schemas unless migrations are implemented and tested.

Use the Go Online Backup/Restore implementation. Do not copy a live SQLite database as an ordinary file.

## Reboots

Keep NoNewPrivileges. Appliance reboot is delegated through systemd-logind and the narrowly scoped polkit rule for mcp-gateway. Never replace this with arbitrary sudo access.

## Documentation contract

CURRENT docs describe the current candidate architecture. Release notes and archive material may describe older implementations, but must not be presented as current guidance.

The executable contract is checked by Go tests plus scripts/verify-go-only.sh.

## Change workflow

For a substantive change:
1. prove Git state and branch;
2. inspect relevant code/tests/docs;
3. implement the smallest coherent fix;
4. review the diff;
5. run focused tests;
6. run full relevant gates;
7. update documentation and remove redundant paths;
8. commit/push only after local evidence is clean;
9. deploy only in an explicit later promotion/validation phase.

## Required gates

At minimum before a candidate commit:

    cd mcp-adapter
    go test -count=1 ./...
    go vet ./...
    go mod tidy -diff
    cd ..
    scripts/verify-go-only.sh
    node mcp-adapter/internal/admin/app_js_test.mjs
    (cd tailwind && npm run build)
    sh -n install.sh
    bash -n scripts/build-release-package.sh
    bash -n scripts/deploy-pi.sh
    bash -n scripts/backup-appliance.sh
    git diff --check

Also cross-build Linux ARMv6 before promotion. The canonical release package must contain no legacy runtime payload.

## STOP conditions

Stop mutation and investigate when:
- Git state is unexpected;
- target/project identity is uncertain;
- a destructive operation lacks a verified backup/rollback path;
- readiness depends on unavailable Core/Registry state;
- a host key changes unexpectedly;
- schema is newer than supported or older than the supported direct migration floor;
- a requested privileged backend cannot be proven;
- validation contradicts the intended change.

## Source-of-truth order

For source contracts: Go code/tests, manifest/compatibility metadata, CURRENT docs.

For live production: gateway_status, .deployment.json, .deployed-git-sha and the live Registry.

## Final rule

Do not report PASS because the implementation looks plausible. Report only what was actually verified, and keep production separate from source validation.
