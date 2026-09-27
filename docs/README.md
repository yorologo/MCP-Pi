# Documentation

MCP-Pi documentation is split by authority.

## CURRENT — authoritative operational guidance

- [Getting started](getting-started.md)
- [Installation](installation.md)
- [Configuration](configuration.md)
- [Operations](operations.md)
- [Update and rollback](update-rollback.md)
- [Recovery](recovery.md)
- [Troubleshooting](troubleshooting.md)
- [Architecture](architecture.md)
- [Security](security.md)
- [Admin Console](admin-console.md)
- [Project state](project-state.md)

These documents describe the 1.5.0-rc.2 Go-only source candidate. They do not claim that production has already been promoted.

## REFERENCE — deep technical detail

- [Compatibility contract](reference/compatibility.md)
- [MCP adapter/Core](reference/mcp-adapter.md)
- [Lifecycle](reference/lifecycle.md)
- [Deployment](reference/deployment.md)
- [Controlled writes](reference/controlled-write.md)
- [Performance](reference/performance.md)
- [Diagrams](reference/diagrams.md)

Reference material must agree with CURRENT docs for the active candidate.

## ARCHIVE — historical evidence

docs/releases and docs/archive may describe older runtime designs and are not current operating instructions.

## Authority rule

If documentation and code disagree, do not change production to fit prose. Establish the actual source contract from Go code/tests and metadata, correct the documentation, then rerun:

    cd mcp-adapter && go test -count=1 ./...
    cd ..
    scripts/verify-go-only.sh

Live production remains authoritative only for its own deployed state through gateway_status, deployment provenance and the live Registry.
