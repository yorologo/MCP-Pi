# Compatibility contract

`compatibility.json` is the machine-readable source for versioned software contracts. Documentation must match it; historical release notes must not be rewritten to look current.

## Current 1.4.0 source contracts

| Contract | Value |
| --- | --- |
| Gateway | `1.4.0` |
| Core API | `1` |
| Bridge API | `1` |
| Tool catalog | `4` |
| Registry schema | `4` |
| MCP SDK | Go SDK `1.7.0` |
| MCP protocol | `2026-07-28` |
| Legacy MCP protocol | `2025-11-25` |
| Python | `3.9+` |
| Declared architectures | `armv6l`, `aarch64`, `x86_64` |

The current deterministic Core catalog contains **21 tools**. Catalog v4 keeps the same names but extends the `run_command` input contract with the explicit `privilege=standard|required` field, so schema-aware clients can distinguish it from v3. Client-visible `tools/list` may contain fewer because policy/grants filter the catalog.

## Fail-closed compatibility

The Go adapter checks the Python bridge contract. Incompatible API/runtime state marks readiness unavailable instead of routing requests optimistically.

```mermaid
flowchart LR
    A[Adapter starts] --> B[Bridge version probe]
    B --> C{Core/Bridge compatible?}
    C -->|yes| R[/ready = 200]
    C -->|no| F[/ready = 503 + deny tool routing]
```

## Architecture declaration versus appliance validation

The software contract declares several Linux architectures, but hardware acceptance is a separate claim. The reference production appliance is ARMv6. A new architecture should pass installation, Doctor, protocol and security acceptance before being described as equally production-verified.

## Protocol security

The HTTP adapter is loopback-first and validates Host/Origin semantics. Authentication tokens are read from private runtime files/environment rather than committed configuration.

## Catalog metadata

The Gateway exposes deterministic metadata including tool count, catalog version and catalog hash so client projection problems can be distinguished from server catalog drift.

## Registry schema

SQLite uses `PRAGMA user_version=4`. Schema v1 migrates through the privilege-policy changes to v4; `privilege_policy` defaults safely to `never`, temporary approvals are scoped to client/project, and schema v4 adds optional `privilege_user` with an empty default. Schema v2 existed only on the privilege-policy feature branch, so its unscoped approvals are intentionally discarded during migration; schema v3 approvals remain scoped while v3→v4 only adds the privileged SSH username field. Backup/restore accepts v1..v4, migrates older supported databases immediately to v4, and clears temporary privilege approvals after restore so authorization state cannot be resurrected. Newer-than-runtime schemas fail closed. Application updates must back up the Registry before schema-changing work.
