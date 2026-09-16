# Compatibility contract

`compatibility.json` is the machine-readable source for versioned software contracts. Documentation must match it; historical release notes must not be rewritten to look current.

## Current 1.3.3 contracts

| Contract | Value |
| --- | --- |
| Gateway | `1.3.3` |
| Core API | `1` |
| Bridge API | `1` |
| Tool catalog | `3` |
| Registry schema | `1` |
| MCP SDK | Go SDK `1.7.0` |
| MCP protocol | `2026-07-28` |
| Legacy MCP protocol | `2025-11-25` |
| Python | `3.9+` |
| Declared architectures | `armv6l`, `aarch64`, `x86_64` |

The current deterministic Core catalog contains **21 tools**. Client-visible `tools/list` may contain fewer because policy/grants filter the catalog.

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

SQLite uses `PRAGMA user_version=1`. Application updates must preserve compatible persistent state and should back up the Registry before schema-changing work.
