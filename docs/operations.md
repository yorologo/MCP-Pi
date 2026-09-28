# Operations

## Quick health

    sudo -u mcp-gateway mcp-gateway status
    sudo -u mcp-gateway mcp-gateway doctor

`status` is non-mutating and reports Registry/runtime compatibility. It does not prove that the whole appliance is ready. Doctor is the canonical appliance diagnostic.

Use:

    sudo -u mcp-gateway mcp-gateway doctor --check-targets

when Target reachability must participate in the verdict.

## Service lifecycle

The base control plane is:

    mcp-gateway-admin.service
    mcp-gateway-mcp.service

Automation:

    mcp-gateway-maintenance.timer
    mcp-gateway-postboot.service

Optional:

    mcp-gateway-gemini.service
    mcp-gateway-tunnel.service

`mcp-gateway-gemini.service` is a second isolated MCP ingress for `gemini-main`. It listens on `0.0.0.0:8092` specifically so a private Cloudflare Tunnel hostname route can reach the service through the appliance LAN address, and it is installed disabled by default. Enable it only after creating `/home/mcp-gateway/.config/mcp-gateway/gemini-mcp.token` as private mutable state and configuring the `gemini-main` client/grants in Admin. The ingress still requires that dedicated Bearer token; its HTTP Host allowlist keeps the loopback defaults and adds only `gemini-mcp.internal`, so unrelated Host headers remain rejected.

Stop the base control plane:

    sudo systemctl stop mcp-gateway-mcp mcp-gateway-admin

Start it:

    sudo systemctl start mcp-gateway-admin
    sudo systemctl start mcp-gateway-mcp

Restart it:

    sudo systemctl restart mcp-gateway-admin
    sudo systemctl restart mcp-gateway-mcp
    sudo -u mcp-gateway mcp-gateway doctor

For a schema-changing lifecycle operation also stop the maintenance timer/service before touching the Registry.

Do not add wrapper scripts for these operations; systemd is the service manager.

## Daily model

Admin Console changes configuration in the same Registry used by MCP. Prefer structured capabilities, keep writes/shell disabled until needed, and verify effective access for AI clients.

## Kill switches

`gateway_enabled` gates delegated operations globally. `writes_enabled` gates structured mutations. `shell_enabled` gates trusted Target shell. Disabled state is a denial, not a warning.

Diagnostic/recovery lifecycle commands remain available so a disabled gateway can still be inspected and recovered.

## Target privilege

Administrative Target execution requires normal authorization plus `target_admin` and the Target privilege policy. The same effective-privilege gate protects both `run_command` and allowlisted `run_task` execution.

## Backup

Registry-only online backup is canonical:

    sudo -u mcp-gateway mcp-gateway backup

There is no separate supported host-wide secret backup format.

## Maintenance

    sudo -u mcp-gateway mcp-gateway maintenance

Maintenance creates a verified online backup, rotates backups, checks SQLite integrity and runs Doctor. Required failure stops the operation.

## Logs

systemd/journald is the canonical runtime log:

    journalctl -u mcp-gateway-admin
    journalctl -u mcp-gateway-mcp
    journalctl -u mcp-gateway-gemini
    journalctl -u mcp-gateway-maintenance
    journalctl -u mcp-gateway-postboot

The optional tunnel uses its own unit journal as well.

## Updates

User update:

    ./install.sh --check
    sudo ./install.sh

Maintainer exact-commit promotion:

    scripts/run-resumable.sh start --expect-marker DEPLOYMENT_VERIFIED -- scripts/deploy-pi.sh <exact-sha>

The deployer delegates activation and rollback to the bundle's canonical installer.

## Recovery

See [recovery.md](recovery.md). Restore is exact-schema; migration is a distinct explicit operation.
