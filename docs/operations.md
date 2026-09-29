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
    mcp-gateway-cloudflared.service

`mcp-gateway-gemini.service` is a second isolated MCP ingress for `gemini-main`. It listens on `0.0.0.0:8092` specifically so a private Cloudflare Tunnel hostname route can reach the service through the appliance LAN address, and it is installed disabled by default. Enable it only after creating `/home/mcp-gateway/.config/mcp-gateway/gemini-mcp.token` as private mutable state and configuring the `gemini-main` client/grants in Admin. The ingress still requires that dedicated Bearer token; its HTTP Host allowlist keeps the loopback defaults and adds only `gemini-mcp.internal`, so unrelated Host headers remain rejected.

`mcp-gateway-cloudflared.service` is the optional private connector for that ingress. It is installed disabled by default and uses `/home/mcp-gateway/.config/mcp-gateway/cloudflared.token` as private mutable state. When explicitly enabled, upgrades preserve that state and restart/verify the connector after the Gemini ingress. The unit uses `Wants=` rather than `Requires=` for Gemini so a short Gemini restart does not permanently stop the tunnel. Doctor also records safe provenance for an enabled connector: the resolved cloudflared binary path, version, SHA-256 and size, plus only token file ownership/mode metadata. It never returns token contents or a token hash; an unreadable/missing binary, insecure token metadata or inactive enabled service is reported as a warning.

Stop the base control plane:

    sudo systemctl stop mcp-gateway-mcp mcp-gateway-admin

Start it:

    sudo systemctl start mcp-gateway-admin
    sudo systemctl start mcp-gateway-mcp

Restart it:

    sudo systemctl restart mcp-gateway-admin
    sudo systemctl restart mcp-gateway-mcp
    sudo -u mcp-gateway mcp-gateway doctor

For a schema-changing lifecycle operation, quiesce every Registry user/scheduler first: Admin, primary MCP, Gemini when enabled, maintenance timer/service and post-boot verification. Edge tunnel processes do not open the Registry and are not classified as database users.

Do not add wrapper scripts for these operations; systemd is the service manager.

## Daily model

Admin Console changes configuration in the same Registry used by MCP. Prefer structured capabilities, keep writes/shell disabled until needed, and verify effective access for AI clients.

## Kill switches

`gateway_enabled` gates delegated operations globally. It is a required security setting at runtime: a missing or malformed value fails closed instead of falling back to enabled. `writes_enabled` gates structured mutations. `shell_enabled` gates trusted Target shell. Disabled state is a denial, not a warning.

Diagnostic/recovery lifecycle commands remain available so a disabled gateway can still be inspected and recovered.

## Target privilege

Administrative Target execution requires normal authorization plus `target_admin` and the Target privilege policy. The same effective-privilege gate protects both `run_command` and allowlisted `run_task` execution.

Use `ask_always` for temporary privileged maintenance whenever practical. Keep the Client/Target/Project grant narrow, approve only the next request, and revoke/avoid persistent host-side credentials when the maintenance path no longer needs them. Doctor reports Privilege Hygiene warnings for enabled `always_allow` Targets and wildcard `target_admin` grants; treat those warnings as explicit review items rather than silently leaving break-glass state behind.

A command or allowlisted task that exits non-zero is a tool execution error, not a successful tool call. MCP-Pi preserves stdout/stderr/exit code in the structured result while setting the tool error signal; `run_command` timeouts are reported the same way.

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
