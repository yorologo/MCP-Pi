# MCP Gateway lifecycle and operations

MCP-Pi 1.5.0-rc.1 keeps lifecycle responsibilities in the Go CLI while thin shell scripts orchestrate operating-system services and release files.

## Runtime tree

    bin/mcp-gateway
    bin/mcp-gateway-adapter
    bin/mcp-gateway-client-stdio
    config/systemd/
    config/polkit/
    compatibility.json
    manifest.json
    install.sh

Persistent data/config is outside the runtime tree.

## Unified CLI

Important commands:
- version / version --json;
- status;
- doctor;
- maintenance;
- backup;
- restore;
- repair;
- setup;
- benchmark;
- serve-admin;
- serve-mcp.

## Doctor

Doctor verifies Go Core availability, SQLite connection/integrity, local resource warnings and optionally enabled Target reachability. Requested Target checks participate in the overall verdict.

## Backup and restore

Backup and restore use modernc.org/sqlite online APIs. Restore validates the source and refuses unsafe live-service replacement. Installer/deployer stop or coordinate services before recovery.

## Installation

install.sh stages a candidate, backs up Registry, initializes/migrates through the Go binary, saves prior system assets, installs root-owned runtime files, starts services and runs Doctor.

## Deployment

scripts/deploy-pi.sh consumes the canonical immutable bundle produced by scripts/build-release-package.sh. Exact Git SHA and final binary/package hashes become deployment provenance only after acceptance.

## Maintenance

maintenance creates a verified backup, rotates retained gateway backups, checks SQLite integrity and runs Doctor. Required failure stops the operation.

## Reboot

gateway_reboot schedules through systemd-logind. The service keeps NoNewPrivileges and receives only the narrow polkit reboot permission.
