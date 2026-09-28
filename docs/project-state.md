# Current project state

This file records source/integration state, not live production.

The current stable source release is **1.5.0**. `develop` and `main` converge on the exact validated release SHA after promotion.

Numeric build/API/catalog/schema/protocol values are not duplicated here; use `manifest.json`, `compatibility.json` and `mcp-gateway version --json`.

## Live production authority

For a running appliance use:
- deployment provenance (`.deployment.json`, `.deployed-git-sha`);
- systemd service/timer state;
- MCP `/live` and `/ready`;
- `mcp-gateway status` and Doctor;
- the live Registry/settings.

Do not copy mutable production state into this file and then treat prose as authority.

## Reference appliance

    Hardware: Raspberry Pi Model A+ Rev 1.1
    Architecture: ARMv6
    Service user: mcp-gateway
    Admin default: loopback
    MCP default: loopback 127.0.0.1:8090

Build/test work belongs on a development host.

## Canonical dogfood Target

    Target ID: termux-main
    Platform: Android / Termux
    Project: MCP_Local
    Transport: pinned SSH

Endpoint address may change; pinned SSH identity remains authoritative.

## Promotion objective

Before promotion:
1. review the complete diff;
2. pass Go/frontend/shell/module/Go-only gates;
3. cross-build ARMv6;
4. build/inspect the canonical bundle from the exact clean commit;
5. verify current documentation contains no duplicate/legacy lifecycle path;
6. push the exact integration commit and require its remote CI to pass;
7. create/push the immutable version tag, then promote the same SHA to `main`;
8. deploy through the resumable exact-commit path and run real ARMv6 service/readiness/Doctor/recovery/security acceptance.

Source validation does not equal deployment PASS.
