# Maintainer deployment

This is the exact-commit promotion path to the reference ARMv6 appliance. It is not a second installer.

## Local prerequisites

- clean named Git branch;
- requested SHA equals local HEAD and `origin/<branch>`;
- repository gates pass locally;
- the exact pushed SHA has a completed successful remote CI run before any version tag is created;
- Go/OpenSSH/scp/tar/gzip/sha256sum available;
- Node only when rebuilding frontend assets;
- private connection settings, when needed, in ignored `.mcp-pi.local.env`.

## Candidate gates

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

Cross-build ARMv6 and inspect the canonical release bundle before promotion. Then push the exact candidate SHA and require its remote CI to succeed **before** creating the immutable version tag:

    SHA=$(git rev-parse HEAD)
    git push origin develop
    gh run list --commit "$SHA" --limit 10
    gh run watch <run-id> --exit-status
    test "$(gh run view <run-id> --json headSha --jq .headSha)" = "$SHA"
    git tag -a <version-tag> "$SHA" -m '<version>'
    git push origin <version-tag>

A tag is evidence of a candidate that already passed the remote commit gate; tagging is not itself the CI gate.

## Deploy

Run through the resumable wrapper:

    scripts/run-resumable.sh start --expect-marker DEPLOYMENT_VERIFIED -- scripts/deploy-pi.sh <exact-sha>

The deployer:

1. proves Git/source provenance;
2. builds the canonical immutable bundle;
3. transfers and verifies package/adapter hashes;
4. runs candidate `install.sh --check` and validates systemd units on ARMv6;
5. delegates activation/update to candidate `install.sh --no-setup`;
6. performs production acceptance;
7. records exact commit/package/binary provenance.

Registry backup/migration, runtime/system-asset activation and rollback belong only to `install.sh`.

## Rollback

If deployment acceptance fails after installation, the deployer delegates to:

    /home/mcp-gateway/mcp-gateway/install.sh --rollback

The installer restores the exact pre-install Registry snapshot without migration, previous runtime/system assets, services and Doctor acceptance.

## Production acceptance

Verify:
- exact deployed SHA/package/binary hashes;
- root-owned runtime;
- Admin/MCP active;
- MCP `/live` and `/ready`;
- Registry compatibility/integrity;
- Doctor, maintenance timer and postboot state;
- optional tunnel state when enabled;
- critical security behavior;
- ARMv6 benchmark when performance is in scope.

Source/CI PASS is never production PASS.
