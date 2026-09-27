# Current project state

This file tracks source/integration state, not live production. The current candidate is MCP-Pi Gateway 1.5.0-rc.1 on go-only-migration.

main remains stable release history; production is not assumed to match this branch until exact-commit deployment and acceptance. Historical releases remain under docs/releases and Git tags.

## Live production authority

For a running appliance, use:
- gateway_status;
- .deployment.json;
- .deployed-git-sha;
- the live Registry/schema/settings;
- systemd and endpoint checks.

Do not copy a mutable production version or SHA into this file and then treat prose as authority.

## Current source contract

    Gateway candidate: 1.5.0-rc.1
    Runtime: Go-only
    Core API: 1
    Bridge API: 1
    Tool catalog: 4 / 21 tools
    Registry schema: 5
    Direct schema upgrade: 4 -> 5
    MCP protocol: 2026-07-28

The old runtime implementation and its active test/tooling tree have been removed. Frozen JSON fixtures remain only where they protect compatibility behavior in Go tests.

## Reference appliance

    Hardware: Raspberry Pi Model A+ Rev 1.1
    Architecture: ARMv6
    Service user: mcp-gateway
    Admin default: loopback
    MCP default: loopback 127.0.0.1:8090

The appliance runtime is intentionally small; build/test work belongs on a development host.

## Canonical dogfood Target

    Target ID: termux-main
    Platform: Android / Termux
    Transport: SSH with strict host-key pinning
    Project: MCP_Local

The Target address may change; pinned SSH identity remains authoritative.

## Fresh-install defaults

    gateway_enabled=true
    writes_enabled=false
    shell_enabled=false

## Current validation objective

This implementation phase must not deploy production. Before a later promotion decision:
1. review the complete diff;
2. pass all Go, frontend, shell, module and Go-only gates;
3. cross-build ARMv6;
4. build and inspect the canonical release bundle from a clean exact commit;
5. verify documentation and remove redundant legacy paths;
6. only in the later deployment phase, promote that exact commit;
7. run real ARMv6 service, recovery, Target and security acceptance;
8. compare measured runtime behavior without presenting historical baselines as current measurements.

Source validation does not equal deployment PASS.
