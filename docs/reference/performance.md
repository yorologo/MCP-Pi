# Runtime performance

MCP-Pi runs on a constrained ARMv6 reference appliance. Performance claims follow measure first, simplify second.

## Current boundary

The 1.6.0 request path is in-process Go for MCP transport, Core, policy and Registry access. Remote Target work remains SSH-bound.

## Canonical benchmark

Use:

    sudo -u mcp-gateway mcp-gateway benchmark

The command reports only measurements made during the current execution. It must not print a historical migration baseline as though it were live data.

Run repeated measurements on the exact deployed candidate after services and temperature stabilize. Record raw output with deployment acceptance evidence.

## Interpretation

Separate:
- in-process Core latency;
- MCP HTTP overhead;
- SSH/network/Target execution;
- memory and CPU pressure on ARMv6.

Development-host results are useful for regressions but are not appliance performance claims.

## Historical comparisons

Historical pre-consolidation numbers may be stored as explicitly dated/versioned evidence. When compared, label hardware, commit, date, configuration and whether the value is historical. Never mix a hardcoded historical value into a current-run benchmark.

## Acceptance

The later ARMv6 validation phase determines whether performance is acceptable. A fast development machine or successful cross-build is not sufficient.
