# Contributing to MCP-Pi

Keep changes small, reviewable and aligned with **KISS + Reuse First + Least Privilege + Fail Closed + Evidence Before PASS**.

## Branches

`develop` is integration and `main` is stable release history. Feature/migration branches are not production authority.

## Development flow

1. start from a clean known branch;
2. inspect and reuse the existing mechanism;
3. change only the required surface;
4. add tests only for meaningful contracts/regressions;
5. run focused then full validation;
6. update the canonical documentation, not parallel copies;
7. review the complete diff before commit.

Core gates:

    cd mcp-adapter
    go test -count=1 ./...
    go vet ./...
    go mod tidy -diff
    cd ..
    scripts/verify-go-only.sh
    node mcp-adapter/internal/admin/app_js_test.mjs
    (cd tailwind && npm run build)
    ./install.sh --check
    git diff --check

CI additionally runs vulnerability reachability checks and ARMv6 cross-build/package inspection.

## Frontend

Go Admin templates/static files are canonical:

    mcp-adapter/internal/admin/templates
    mcp-adapter/internal/admin/static

Tailwind writes directly to the embedded static directory. Do not create a second asset tree or frontend framework without a concrete requirement.

## Installation and release UX

User install/update/rollback goes through `install.sh` and an immutable release bundle. Maintainer promotion uses `scripts/run-resumable.sh` + `scripts/deploy-pi.sh`, which delegates lifecycle mutation to that same installer.

Do not add another installer, updater, migration daemon, service wrapper or release packaging path without a demonstrated need.

## Documentation

- README.md is onboarding.
- `docs/*.md` contains current operator/user guidance.
- `docs/reference/*.md` is maintainer detail that cannot be expressed better by code/metadata.
- `docs/releases/` and `docs/archive/` are historical and are never current operating instructions.
- AGENTS.md is the automation operational contract.

Numeric build/API/schema/protocol values belong to machine-readable metadata and `version --json` rather than duplicated prose where possible.

## Dependencies

Prefer the standard library or an existing dependency. A runtime dependency needs a concrete operational reason. The appliance release must remain self-contained and must not require development runtimes.

## Security-sensitive changes

Policy, grants, filesystem confinement, trusted shell, privilege, SSH identity, backup/restore, sessions and deployment changes require explicit negative tests and fail-closed behavior.

## Hardware acceptance

The reference target is ARMv6. Cross-build/package validation precedes real appliance acceptance; development-host PASS is not production PASS.
