# Compatibility contract

## Current candidate

| Contract | Value |
| --- | --- |
| Gateway | 1.5.0-rc.1 |
| Runtime | Go-only |
| Core API | 1 |
| Bridge API | 1 |
| Tool catalog | 4 / 21 tools |
| Registry schema | 5 |
| Fresh schema | 5 |
| Direct upgrade input | 4 or 5 |
| MCP protocol | 2026-07-28 |
| MCP Go SDK | 1.7.0 |
| Build toolchain contract | Go 1.27.1 |

compatibility.json and manifest.json are checked against the Go build metadata and Registry constants by Go tests.

## Fail-closed compatibility

The server is ready only when the in-process Go Core and Registry initialize and their API/catalog/protocol contract matches the adapter build. Missing or incompatible state does not route through a fallback runtime.

## Tool catalog

The canonical Core catalog contains 21 tools. Client-visible tools/list can contain fewer because grants/policy filter the catalog.

No-argument administrative tools reject undeclared properties. gateway_doctor declares check_targets and verbose explicitly.

## Registry schema

Schema 5 is canonical. A fresh Registry is created directly at v5. The current Go migration path supports v4 to v5 and already-current v5.

Older schemas are not claimed as directly supported by this candidate. They must first be upgraded using a release that explicitly supports them. Newer-than-runtime schemas fail closed.

Before schema-changing install/deploy work, MCP-Pi creates a verified SQLite online backup.

## Architecture declaration versus appliance validation

Metadata declares supported artifact architectures, but real release acceptance still requires the reference ARMv6 appliance. A successful cross-build does not prove production readiness.

## Protocol security

Authorization remains independent of transport. Authentication, client identity, grants, Target/Project scope, kill switches and Target privilege policy are evaluated in the Go Core/Registry path.

## Release identity

Do not reuse an immutable published version number for changed software. 1.5.0-rc.1 is a prerelease candidate until later validation justifies promotion.
