# Compatibility contract

Machine-readable authority is:

- `manifest.json` — release/package contract;
- `compatibility.json` — runtime/API/catalog/schema/protocol compatibility;
- `mcp-gateway version --json` — exact binary contract;
- Registry schema constants/migrations — database implementation.

Project contract tests and the installer require these authorities to agree. This document describes semantics rather than maintaining another copy of every numeric field.

## Registry compatibility

Normal runtime open is non-migrating and requires the runtime's current schema.

The explicit `migrate` command is the only supported schema transformation path. The current stable release supports fresh creation and the directly tested previous-schema upgrade encoded by Registry migrations.

`status`, service startup, Doctor and `restore` never migrate as a side effect. Restore preserves the backup schema exactly.

Newer-than-runtime or otherwise unsupported schemas fail closed.

## Tool/API/protocol compatibility

The Core catalog is canonical; client-visible `tools/list` can contain fewer tools because policy filters it.

Install preflight compares the candidate binary against `manifest.json` and `compatibility.json` before mutation. Release packaging/CI additionally checks the same source contract.

## Architecture compatibility

A declared/cross-built architecture is not production evidence. The reference ARMv6 appliance must still pass real service/readiness/Doctor acceptance.

## Release identity

Published tags/releases are immutable identities. Changed software receives a new candidate/release identifier rather than reusing an already published version.
