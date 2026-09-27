# Contributing to MCP-Pi

MCP-Pi 1.5.0-rc.2 is a Go-only candidate. Keep contributions small, reviewable and aligned with KISS + Reuse First + Least Privilege + Fail Closed + Evidence Before PASS.

## Branches

main is stable release history. develop is integration. Feature/migration branches must not be treated as production until an exact commit is promoted and accepted.

## Development flow

1. start from a clean, known branch;
2. inspect the existing mechanism;
3. change only the necessary surface;
4. add tests when they protect a meaningful contract;
5. run focused and full validation;
6. update CURRENT documentation;
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

CI additionally runs vulnerability reachability checks and ARMv6 cross-build/package validation.

## Installation and release UX

User installation goes through install.sh and an immutable release bundle. Maintainer deployment goes through scripts/run-resumable.sh and scripts/deploy-pi.sh with an exact commit.

Do not add another installer, updater, migration daemon or release packaging path without a concrete requirement.

## Documentation

CURRENT docs live at README.md, AGENTS.md and docs/*. Historical material belongs under docs/releases or docs/archive.

The Go project contract test checks metadata, Registry defaults and stale CURRENT runtime descriptions.

## Dependencies

Prefer the standard library or an existing dependency. Runtime dependencies need a concrete reason, not generic best-practice value. The appliance release must remain self-contained and must not require Go or other development runtimes.

## Security-sensitive changes

Changes to policy, grants, filesystem confinement, trusted shell, privilege, SSH identity, backup/restore, sessions or deployment require explicit negative tests and fail-closed behavior.

## Hardware acceptance

The reference deployment target is ARMv6. Cross-build locally/CI first; real appliance acceptance happens only in the deployment validation phase.
