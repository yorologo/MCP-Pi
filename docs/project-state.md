# Current project state

This document is intentionally short and dynamic. Detailed historical evidence belongs in `docs/releases/`, `docs/archive/` and Git history.

**Software baseline:** MCP-Pi Gateway 1.4.0
**Development branch:** `develop`
**Latest immutable tag:** `v1.4.0`
**Current work:** v1.4.0 release prepared and validated; production remains on v1.3.6 until exact-commit deployment and acceptance complete

## Current production baseline

```text
Gateway: 1.3.6
Core API: 1
Bridge API: 1
Tool catalog: 3 / 21 tools
Registry schema: 1
Doctor: HEALTHY after production acceptance
rollback: VERIFIED by the release deployment path
deployment provenance: REQUIRED / VERIFIED after acceptance
```

The deployed Git SHA is intentionally **not duplicated in tracked prose**. Its authoritative sources are the runtime `.deployed-git-sha` and `.deployment.json`; this avoids self-referential documentation drift every time the state document itself changes.

The current privilege-policy feature branch advances the **source Registry schema to 4** with supported v1→v4, v2→v4 and v3→v4 migrations and the **source tool catalog contract to v4** while keeping the same 21 tool names. Schema 3 introduced client/project-scoped temporary privilege approvals; schema 4 adds the optional `privilege_user` used only by the verified privileged-SSH backend. The v2→v3 migration intentionally discards older unscoped temporary approvals, and v3→v4 preserves Target policy while adding `privilege_user` with an empty, fail-closed default. Production remains schema 1 / catalog v3 until that exact candidate passes validation and is deployed; this document does not treat source work as production state.

## Reference production appliance

```text
Role: MCP-Pi Gateway
Hardware: Raspberry Pi Model A+ Rev 1.1
Architecture: ARMv6
OS baseline: Debian/Raspberry Pi OS 13 Trixie
Service user: mcp-gateway (no general sudo)
Admin: private trusted LAN or loopback according to admin.env
MCP: loopback 127.0.0.1:8090
Registry: SQLite schema 1
```

The separate Pi-hole appliance is outside project scope.

## Canonical Target used for production dogfooding

```text
Target ID: termux-main
Platform: Android / Termux
Transport: SSH with strict host-key pinning
Project: MCP_Local
```

Target IP is mutable endpoint state, not identity; the pinned SSH host key is authoritative.

## Security settings: defaults versus this production instance

Fresh Registry defaults for the release-readiness work are:

```text
gateway_enabled=true
writes_enabled=false
shell_enabled=false
```

The last observed production instance intentionally had:

```text
gateway_enabled=true
writes_enabled=true
shell_enabled=true
```

Those production values are persisted local policy, not defaults for new users.

## Current release-readiness objective

Before promoting the new documentation/installer experience as a release:

1. beginner/current documentation must be consolidated and semantically audited;
2. `install.sh --check` and release packaging must pass;
3. full local CI gates must pass;
4. an ARMv6 release bundle must pass non-destructive preflight on the real appliance;
5. the exact final commit must pass maintainer deploy and production acceptance;
6. local/remote/deployed SHAs must match before any release promotion.

Do not open unrelated feature work during this phase.
