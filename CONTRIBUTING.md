# Contributing to MCP-Pi

Keep changes small, reviewable and aligned with **KISS + Reuse First + Least Privilege + Fail Closed + Evidence Before PASS**.

## Branches and authority

`develop` is integration and `main` is stable release history. Feature branches are not production authority.

Source/API/schema/protocol truth comes from Go code/tests plus `manifest.json`, `compatibility.json` and `mcp-gateway version --json`. CI defines the executable repository gates. Live production is authoritative only for its deployed commit/runtime/Registry/service evidence.

## Development flow

1. prove the repository, branch and worktree state;
2. inspect and reuse the existing mechanism;
3. change only the required surface;
4. add tests for meaningful contracts/regressions;
5. run focused validation;
6. run the full relevant repository gates;
7. update the canonical documentation instead of adding parallel prose;
8. review the complete diff before commit.

Local gates mirror CI as closely as the development host allows:

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

CI additionally performs vulnerability reachability analysis, shell static analysis, ARMv6 cross-build and release-package inspection.

## Frontend

Go Admin templates/static files are canonical:

    mcp-adapter/internal/admin/templates
    mcp-adapter/internal/admin/static

Tailwind writes directly to the embedded static directory. Do not create a second asset tree/framework without a concrete requirement.

## Documentation

Current documentation has one entry point and six responsibility-based guides:

- `README.md` — onboarding and navigation;
- `docs/installation.md` — install/bootstrap/update/installer rollback;
- `docs/configuration.md` — Admin/Targets/Projects/Clients/ingress;
- `docs/operations.md` — lifecycle/health/maintenance/logs/performance;
- `docs/recovery.md` — troubleshooting/restore/disaster recovery;
- `docs/architecture.md` — system and compatibility semantics;
- `docs/security.md` — grants/writes/shell/privilege/security;
- `CHANGELOG.md` — repository change history.

`AGENTS.md` contains only automation-specific operating rules. `docs/archive/` keeps selected historical evidence, never current runbooks.

Do not duplicate numeric compatibility values, SQL definitions, CLI flags, systemd dependency graphs or CI command lists in prose when code/metadata/`--help`/units/CI are the better authority.

## Dependencies and security-sensitive changes

Prefer the standard library/platform or an existing dependency. A new runtime dependency needs a concrete operational reason; release bundles must not require development tooling.

Policy, grants, filesystem confinement, trusted shell, Target privilege, SSH identity, backup/restore, Admin sessions and deployment changes require explicit negative/fail-closed tests.

## Release candidate workflow

A release tag is created only after the exact pushed candidate commit has passed remote CI.

Before pushing a candidate:

1. complete local gates and diff review;
2. cross-build Linux ARMv6;
3. build/inspect the canonical release bundle from the exact clean commit;
4. ensure CURRENT docs have no parallel lifecycle/release path.

Then push the exact integration SHA and verify its CI run:

    SHA=$(git rev-parse HEAD)
    git push origin develop
    gh run list --commit "$SHA" --limit 10
    gh run watch <run-id> --exit-status
    test "$(gh run view <run-id> --json headSha --jq .headSha)" = "$SHA"

Only after that evidence create/push the immutable annotated version tag. Tag CI reruns validation and the release job publishes or verifies the canonical ARMv6 bundle plus checksum as GitHub Release assets.

Do not reuse an already published version identifier for changed software.

## Exact-commit appliance deployment

Deployment is a maintainer workflow around the same installer used by users; it is not another lifecycle engine.

Configure the destination explicitly through environment or ignored `.mcp-pi.local.env`:

    MCP_PI_HOST=<appliance-host>
    MCP_PI_USER=<ssh-user>
    MCP_PI_IDENTITY_FILE=<optional-key-path>

No repository-specific host/user fallback is allowed.

Launch through the resumable wrapper:

    scripts/run-resumable.sh start --expect-marker DEPLOYMENT_VERIFIED -- scripts/deploy-pi.sh <exact-sha>

Use `run-resumable.sh status` / `log` after a control-session interruption instead of starting a second deployment.

The deployer proves Git/source provenance, builds/transfers/verifies the canonical bundle, validates the candidate and systemd units, delegates activation/rollback to bundled `install.sh`, performs production acceptance and records deployed commit/package/binary evidence.

If acceptance fails after activation, rollback remains the installer's responsibility.

## Production acceptance

Source/CI PASS is not production PASS. Verify, as applicable:

- exact deployed commit/package/binary hashes;
- root-owned runtime and expected persistent ownership;
- `mcp-gateway.target`, required services, maintenance timer and postboot;
- MCP liveness/readiness;
- Registry integrity/compatibility;
- Doctor and Target reachability;
- explicitly enabled optional ingress;
- critical negative security behavior;
- ARMv6 performance when performance changed.

Commit, tag, release and deployment are separate evidence boundaries.
