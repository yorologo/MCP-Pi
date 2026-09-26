# Current project state

This document tracks the **source/integration state**, not a cached copy of live production. Detailed historical evidence belongs in `docs/releases/`, `docs/archive/` and Git history.

**Source baseline:** MCP-Pi Gateway 1.4.0
**Development branch:** `develop`
**Stable branch:** `main`
**Latest immutable tag:** `v1.4.0`
**Current integration work:** Admin Console hardening, auditability and operator UX improvements are being validated on `develop`. They are not production state until an exact commit is deployed and accepted.

## Live production authority

Do not duplicate the deployed version, SHA, Registry schema or live kill-switch values in tracked prose. Those values change independently of this file.

For a running appliance, the authoritative evidence is:

- `gateway_status` for runtime version, services, resources and deployment provenance;
- `.deployment.json` for verified deployment metadata;
- `.deployed-git-sha` for the exact deployed Git commit;
- the live Registry for schema and persisted policy values.

A successful source or CI validation does **not** imply that production is running the same commit.

## Current source contract

```text
Gateway source baseline: 1.4.0
Core API: 1
Bridge API: 1
Tool catalog: 4 / 21 tools
Registry schema: 4
MCP protocol: 2026-07-28
Python: 3.11+
```

Schema 4 supports migrations from the earlier supported schemas. Temporary privilege approvals remain scoped operational state and are not configuration to be restored blindly.

## Reference production appliance

```text
Role: MCP-Pi Gateway
Hardware: Raspberry Pi Model A+ Rev 1.1
Architecture: ARMv6
OS baseline: Debian/Raspberry Pi OS 13 Trixie
Service user: mcp-gateway (no general sudo)
Admin: private trusted LAN or loopback according to admin.env
MCP: loopback 127.0.0.1:8090
```

The separate Pi-hole appliance is outside project scope.

## Canonical Target used for dogfooding

```text
Target ID: termux-main
Platform: Android / Termux
Transport: SSH with strict host-key pinning
Project: MCP_Local
```

Target IP is mutable endpoint state, not identity; the pinned SSH host key is authoritative.

## Fresh-install security defaults

A fresh Registry starts with:

```text
gateway_enabled=true
writes_enabled=false
shell_enabled=false
```

These are defaults only. Query `gateway_status` / the live Registry before making claims about an installed appliance.

## Current validation objective

Before these integration changes may be promoted:

1. review the complete diff and remove accidental complexity;
2. pass focused and full Python, Go and JavaScript tests;
3. pass installer, documentation and build gates;
4. validate the declared Python minimum independently of the reference runtime;
5. commit and push the exact validated state to `develop`;
6. only in the later deployment phase, deploy that exact commit to the reference ARMv6 appliance and run functional/security acceptance plus `scripts/benchmark_runtime.py`;
7. use the ARMv6 benchmark to quantify the current Go-to-Python process boundary before approving any Python-to-Go Core migration;
8. keep `main` unchanged until release promotion is explicitly justified.

Evidence from the later appliance acceptance, not this document, determines whether deployment is PASS.
