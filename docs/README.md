# Documentation

README.md is the onboarding entry point. The files below describe the current source architecture; publication/promotion state is intentionally kept outside source prose. Historical material is isolated under `docs/releases/` and `docs/archive/`.

## User / operator

- [Installation](installation.md) — install, bootstrap, update and rollback.
- [Configuration](configuration.md) — runtime paths, Admin, Targets/Projects and private configuration.
- [Operations](operations.md) — start/stop/restart, status, Doctor, maintenance and logs.
- [Recovery](recovery.md) — Registry backup/restore and application rollback.
- [Troubleshooting](troubleshooting.md) — failure-oriented procedures.
- [Admin Console](admin-console.md) — UI operation.
- [Architecture](architecture.md) — current system boundaries.
- [Security](security.md) — security model and invariants.

## Maintainer reference

- [Project state](project-state.md) — source versus production authority and promotion objective.
- [Compatibility](reference/compatibility.md) — machine-readable contract and schema compatibility semantics.
- [Deployment](reference/deployment.md) — exact-commit promotion to the ARMv6 appliance.
- [Client grants](reference/client-grants.md) — authorization semantics.
- [Controlled writes](reference/controlled-write.md) — structured mutation semantics.
- [Performance](reference/performance.md) — measurement contract.
- [ChatGPT / secure tunnel](reference/chatgpt-gate.md) — optional external MCP path.

Contributor workflow belongs in [../CONTRIBUTING.md](../CONTRIBUTING.md). Automation-specific operational rules belong in [../AGENTS.md](../AGENTS.md).

## Authority

For numeric build/API/schema/protocol values use `manifest.json`, `compatibility.json` and `mcp-gateway version --json`; do not maintain parallel prose constants unnecessarily.

When prose and code disagree, establish behavior from Go code/tests and machine-readable metadata, correct the prose, then rerun repository gates.

Live production is authoritative only for its deployed state through systemd/endpoints, the live Registry and deployment provenance.
