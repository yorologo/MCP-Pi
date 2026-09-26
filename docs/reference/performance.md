# Runtime performance and Go consolidation gate

MCP-Pi runs on a deliberately constrained reference appliance. Performance work therefore follows **measure first, simplify second**. A language migration is not justified by generic Python-vs-Go expectations alone.

## Current request boundary

The current MCP request path is:

```text
MCP client
  -> Go adapter
  -> exec python -m mcp_gateway.bridge invoke ...
  -> Python Gateway Core
  -> Policy / Registry
  -> optional SSH Target operation
```

The Go adapter currently starts a new Python process for bridge version checks, catalog queries and individual tool invocations. This process boundary is a concrete optimization candidate because interpreter startup and module imports are paid outside the useful Core operation.

Do not infer the ARMv6 cost from development-host measurements. The reference Raspberry Pi must provide the production evidence.

## Canonical benchmark

Use the repository benchmark after deploying the exact candidate commit:

```bash
sudo -u mcp-gateway \
  /usr/bin/python3 \
  /home/mcp-gateway/mcp-gateway/scripts/benchmark_runtime.py \
  --samples 9 \
  --json
```

The script is intentionally stdlib-only and does not invoke any mutating Gateway tool. It requires an existing Registry and measures these layers:

| Measurement | What it isolates |
| --- | --- |
| `python_startup` | interpreter process startup |
| `bridge_version_subprocess` | Python startup + Gateway bridge imports |
| `core_health_inprocess` | useful Python Core health work without process startup |
| `bridge_health_subprocess` | current Python bridge subprocess path |
| `adapter_live_http` | Go HTTP/adapter path without Python Core invocation |
| `adapter_health_http` | Go HTTP path plus current Python bridge subprocess |

It also records appliance memory availability, load, temperature when available, and RSS/VSZ for the MCP, Admin and Tunnel services.

The derived values are evidence inputs, not automatic PASS/FAIL decisions:

- `bridge_process_tax_p50_ms`: bridge subprocess p50 minus in-process Core p50;
- `bridge_process_tax_pct_of_bridge_health`: fraction of bridge health latency outside useful in-process Core work;
- `adapter_health_minus_go_live_p50_ms`: approximate additional latency introduced after the pure Go HTTP layer;
- `python_startup_pct_of_bridge_health`: how much bare interpreter startup contributes to the bridge path.

Run the benchmark at least three times on the reference appliance after services and temperature have stabilized. Keep the raw JSON with the deployment acceptance evidence.

## How to decide whether to consolidate into Go

A Go-only runtime is a **migration candidate**, not an assumed destination. Evidence should support all of the following before a full rewrite begins:

1. the subprocess/bridge boundary is a material part of local request latency on ARMv6;
2. Python runtime/service RSS is material relative to available appliance memory;
3. the relevant latency is local gateway overhead rather than SSH/network/Target work;
4. a migration can preserve the existing Policy, Registry, audit and security contracts with parity tests;
5. the end state removes the Python runtime path instead of permanently maintaining two canonical implementations.

If the benchmark shows that most request time is remote SSH/Target work, rewriting the Core provides little end-to-end benefit and should not be prioritized.

If the benchmark shows that the per-request Python subprocess dominates local latency, the first architectural objective is to **remove the process-per-call boundary**. Two implementation directions may then be compared:

- migrate the canonical Core incrementally into Go until the Python bridge can be deleted;
- use a persistent Python bridge only as a bounded transitional experiment if it demonstrates the value of eliminating process startup with much less implementation risk.

A permanent second policy engine or Admin-to-SSH bypass is not acceptable. During any migration there must remain exactly one canonical authorization decision for a request.

## Migration safety

A Go consolidation effort must preserve:

- tool catalog and JSON result/error contracts;
- fail-closed defaults and kill switches;
- Target/Project/client/grant authorization semantics;
- explicit `target_admin` handling;
- SQLite schema/migration compatibility or a deliberate one-time migration;
- SSH identity pinning and endpoint rediscovery rules;
- audit attempt/result semantics;
- exact-commit deploy and rollback provenance.

The existing Python tests are behavioral evidence, not disposable implementation detail. Before deleting a migrated Python subsystem, equivalent Go/golden parity coverage must exist and the reference ARMv6 appliance must pass the same functional/security acceptance.

## Development-host measurements

Development-host results are useful only to validate the benchmark and identify obvious architecture costs. They are not production performance claims. Only the reference ARMv6 deployment may be used to decide whether the Go consolidation is justified.
