# Deployment guide

This is the maintainer path for promoting an exact 1.5.0-rc.2 Go-only candidate. It is not the normal user installer.

## Production target

Reference acceptance target: Raspberry Pi Model A+ / Linux ARMv6.

## Local prerequisites

- clean Git worktree;
- named branch;
- exact requested SHA equal to local HEAD and origin branch;
- Go toolchain matching go.mod;
- OpenSSH/scp;
- tar/gzip/sha256sum;
- Node only when rebuilding frontend assets.

## Build and verification

Before deployment:

    cd mcp-adapter
    go test -count=1 ./...
    go vet ./...
    go mod tidy -diff
    cd ..
    scripts/verify-go-only.sh
    ./install.sh --check
    git diff --check

Cross-build ARMv6 and build the canonical release bundle. The bundle must contain no legacy runtime payload.

## Deploy

Use the resumable runner:

    scripts/run-resumable.sh start --expect-marker DEPLOYMENT_VERIFIED -- scripts/deploy-pi.sh <exact-sha>

The deployer reuses scripts/build-release-package.sh, uploads the immutable bundle, validates the candidate on ARMv6, creates a Go online Registry backup, saves system assets, performs controlled service restart/activation and writes verified provenance only after production acceptance.

## Rollback

A failure after the activation boundary triggers rollback. Registry restore occurs while Admin/MCP are stopped, then previous runtime/system assets are restored and Doctor must pass.

## Post-deployment checks

A later validation phase must verify:
- exact deployed SHA and binary hash;
- root-owned runtime;
- Admin/MCP service health;
- /live and /ready;
- Registry schema/integrity;
- Doctor and Target checks;
- optional tunnel state;
- critical security behavior;
- measured ARMv6 benchmark.

Source validation alone is not a deployment PASS.
