# Operations

## Quick health

    sudo -u mcp-gateway mcp-gateway status
    sudo -u mcp-gateway mcp-gateway doctor

Use doctor --check-targets when Target reachability must participate in the overall verdict.

## Daily model

Admin Console changes configuration in the same Registry used by MCP. Prefer structured capabilities, keep writes/shell disabled until needed, and verify effective access for AI clients.

## Kill switches

gateway_enabled gates operations globally. writes_enabled gates structured mutations. shell_enabled gates trusted Target shell. Disabled state is a denial, not a warning.

## Target privilege

Administrative Target execution requires normal authorization plus target_admin and the Target privilege policy. Do not use general sudo on the appliance as a substitute.

## Backup

Registry-only online backup:

    sudo -u mcp-gateway mcp-gateway backup

Disaster-recovery appliance bundle:

    sudo scripts/backup-appliance.sh

If BACKUP_AGE_RECIPIENT is set, backup-appliance refuses plaintext fallback when age is unavailable.

## Maintenance

    sudo -u mcp-gateway mcp-gateway maintenance

Maintenance creates a verified online backup, rotates gateway backups, checks SQLite integrity and runs Doctor. Required failure stops the operation.

## Logs

Use journalctl for mcp-gateway-admin, mcp-gateway-mcp, mcp-gateway-maintenance, mcp-gateway-postboot and the optional tunnel service.

## Restart order

When manual service recovery is necessary, restore Admin/MCP first, then postboot/maintenance and the optional tunnel. Do not restart merely to hide a failed readiness condition; inspect Core/Registry errors first.

## Updates

User update uses install.sh. Maintainer exact-commit deployment uses:

    scripts/run-resumable.sh start --expect-marker DEPLOYMENT_VERIFIED -- scripts/deploy-pi.sh <sha>

Use run-resumable.sh status and log if the control connection is intentionally restarted.

## Recovery

See [recovery.md](recovery.md). Restore is designed to run with Admin/MCP stopped so all processes reopen the same SQLite database state.
