# Documentation

The documentation is deliberately split into **CURRENT**, **REFERENCE** and **ARCHIVE** so an operator never has to guess whether an old command still applies.

## CURRENT — authoritative operational guidance

Use these documents for the current 1.3.6 codebase:

- [`../README.md`](../README.md) — project landing and quick start.
- [`getting-started.md`](getting-started.md) — shortest path for a new user.
- [`installation.md`](installation.md) — fresh install and reinstall contract.
- [`configuration.md`](configuration.md) — environment, Registry and security settings.
- [`operations.md`](operations.md) — normal administration and maintenance.
- [`update-rollback.md`](update-rollback.md) — user update/rollback and maintainer deployment boundary.
- [`recovery.md`](recovery.md) — backup, restore and disaster recovery.
- [`troubleshooting.md`](troubleshooting.md) — diagnosis by layer.
- [`architecture.md`](architecture.md) — current component and request flows.
- [`security.md`](security.md) — trust boundaries, grants, kill switches and shell semantics.
- [`admin-console.md`](admin-console.md) — Admin UI behavior.
- [`project-state.md`](project-state.md) — concise dynamic state of this repository/production baseline.

Project context, not step-by-step operating instructions:


## REFERENCE — deep technical detail

[`reference/`](reference/) keeps specialized protocol, lifecycle, deployment and integration material that is useful to maintainers but should not compete with the beginner path.

Reference documents are not the source of truth for a command when a CURRENT guide says otherwise.

## ARCHIVE — historical evidence

[`archive/`](archive/) preserves completed migrations, old runbooks, implementation plans/specs and acceptance evidence. Old IPs, versions, tool counts and procedures may intentionally appear there.

Do **not** execute archived commands as current operating guidance.

Release notes under [`releases/`](releases/) are historical and immutable by version.

## Authority rule

```text
Observed runtime / code contract
        ↓
CURRENT documentation
        ↓
REFERENCE rationale/details
        ↓
ARCHIVE historical evidence
```

If CURRENT documentation disagrees with the running software, stop, determine which source drifted, correct it and re-run `python scripts/audit-docs.py`. Never change production merely to make it match old archived text.
