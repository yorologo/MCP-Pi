# Recovery

Recovery for the 1.5.0-rc.5 Go-only runtime deliberately uses the same lifecycle primitives as normal operation.

## 1. Diagnose before mutating

Inspect:

    systemctl status mcp-gateway-admin mcp-gateway-mcp
    journalctl -u mcp-gateway-admin -u mcp-gateway-mcp
    sudo -u mcp-gateway mcp-gateway status
    sudo -u mcp-gateway mcp-gateway doctor

A live process does not imply readiness. Doctor and `/ready` are the canonical appliance evidence.

## 2. Application rollback

For an installer-managed update:

    sudo /home/mcp-gateway/mcp-gateway/install.sh --rollback

Exact-commit deployment delegates activation and rollback to the same installed `install.sh`; do not create a second rollback procedure.

Rollback stops database users, restores the pre-install Registry without migration, restores the previous runtime/system assets, restarts services and requires Doctor before reporting `ROLLBACK_VERIFIED`.

## 3. Registry backup

    sudo -u mcp-gateway mcp-gateway backup

The command uses SQLite's online backup API and records integrity, schema and SHA-256 evidence.

## 4. Registry restore

Stop all database users first:

    sudo systemctl stop mcp-gateway-maintenance.timer
    sudo systemctl stop mcp-gateway-maintenance.service
    sudo systemctl stop mcp-gateway-mcp
    sudo systemctl stop mcp-gateway-admin

Then restore:

    sudo -u mcp-gateway mcp-gateway restore <backup.db>

Restore preserves the backup schema exactly. It does not migrate.

If the runtime to be activated requires a newer directly supported schema, run migration explicitly while services remain stopped:

    sudo -u mcp-gateway mcp-gateway migrate

Then start/verify the appliance using the procedures in [operations.md](operations.md).

## 5. Disaster recovery scope

The supported recovery contract is:
- reinstall the immutable MCP-Pi runtime;
- restore the Registry from a verified backup;
- reprovision private host/SSH/tunnel secrets from their authoritative secure source.

MCP-Pi does not maintain a second appliance-wide secret-archive format without a complete tested restore contract.

## Acceptance after recovery

Verify:
- Registry integrity and expected schema;
- Admin and MCP services active;
- MCP `/live` and `/ready`;
- Doctor;
- maintenance timer enabled;
- optional tunnel state if configured;
- Target SSH identity;
- client/grant effective access;
- deployment provenance when applicable.

Do not declare recovery complete solely because systemd reports a process running.
