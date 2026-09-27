# Update and rollback

The 1.5.0-rc.2 candidate has one release artifact path and two consumers: user installation and maintainer exact-commit deployment.

## User update

Extract an official bundle and run:

    ./install.sh --check
    sudo ./install.sh

The previous installer-managed runtime and a pre-install online Registry backup are retained until successful completion.

## User rollback

    sudo /home/mcp-gateway/mcp-gateway/install.sh --rollback

Rollback restores Registry and system assets before restarting services and requiring Doctor.

## Candidate validation

Before promotion:

    cd mcp-adapter
    go test -count=1 ./...
    go vet ./...
    go mod tidy -diff
    cd ..
    scripts/verify-go-only.sh
    ./install.sh --check
    git diff --check

Cross-build ARMv6 and inspect the final bundle as well.

## Maintainer exact-commit deployment

Deployment requires a clean named branch whose HEAD equals origin/<branch> and the requested exact SHA.

Run through the resumable wrapper:

    scripts/run-resumable.sh start --expect-marker DEPLOYMENT_VERIFIED -- scripts/deploy-pi.sh <exact-sha>

The deployer:
- builds the canonical immutable release bundle;
- verifies candidate binary/hash/schema/systemd files remotely;
- creates an online Registry backup;
- saves units/polkit state;
- stops the control plane;
- activates the root-owned candidate;
- migrates/validates Registry with Go;
- starts services and performs acceptance;
- writes verified deployment provenance;
- preserves one rollback set.

A failure after activation invokes rollback.

## Controlled rollback test

MCP_DEPLOY_INJECT_FAILURE=after-activation may be used only in an explicit deployment-validation exercise. Do not inject failures into production outside that test.

## Database backup versus application rollback

Application rollback and Registry restore are coordinated but distinct. Never replace a live SQLite file with an ordinary rename/copy while gateway processes are still using it.
