# Operations

## Health

Non-mutating Registry/runtime compatibility:

    sudo -u mcp-gateway mcp-gateway status

Appliance health:

    sudo -u mcp-gateway mcp-gateway doctor

Include remote Target reachability when needed:

    sudo -u mcp-gateway mcp-gateway doctor --check-targets

A running process is not readiness evidence. Doctor is the normal appliance verdict; MCP `/ready` proves initialized Core/Registry for traffic.

## Start, stop and restart

`mcp-gateway.target` is the canonical lifecycle unit. Required services/timer join that target; optional ingress joins it only when explicitly enabled.

    sudo systemctl start mcp-gateway.target
    sudo systemctl stop mcp-gateway.target
    sudo systemctl restart mcp-gateway.target

After a manual restart, verify:

    sudo -u mcp-gateway mcp-gateway doctor

Do not add wrapper scripts around normal service lifecycle; systemd is the service manager.

## Daily operating model

Admin Console modifies the same Registry used by MCP. Prefer structured capabilities and allowlisted Tasks. Keep writes/shell disabled until needed and use **Check Effective Access** before broadening permissions.

Kill switches are denials, not warnings:

- `gateway_enabled` gates delegated operations globally;
- `writes_enabled` gates structured filesystem mutation;
- `shell_enabled` gates trusted Target shell.

Diagnostic/recovery commands remain available so a disabled gateway can still be inspected.

## Target privilege

Administrative execution is a second authorization gate; [security.md](security.md) is the source for Grants, approvals and privilege-backend semantics. During routine maintenance keep scope narrow and treat Doctor privilege-hygiene warnings as review items.

A remote command/task that exits non-zero is an execution error. MCP-Pi preserves stdout/stderr/exit code in the structured result rather than treating transport success as command success.

## Backup

Canonical Registry backup:

    sudo -u mcp-gateway mcp-gateway backup

This uses SQLite online backup and records verification evidence. MCP-Pi does not define a second appliance-wide secret backup format.

## Maintenance

    sudo -u mcp-gateway mcp-gateway maintenance

Maintenance creates a verified Registry backup, rotates only MCP-Pi-managed backup classes, protects the snapshot referenced by the active installer rollback set, checks SQLite integrity, runs Doctor and requires its final audit record before reporting success. Unknown/manual backup names are not pruned.

The maintenance timer is part of the appliance lifecycle; the maintenance oneshot itself is not restart-coupled to `mcp-gateway.target`.

## Logs

journald is the runtime log authority:

    journalctl -u mcp-gateway-admin
    journalctl -u mcp-gateway-mcp
    journalctl -u mcp-gateway-maintenance
    journalctl -u mcp-gateway-postboot

Use the corresponding Gemini/Cloudflare/tunnel unit journal when an optional ingress is enabled.

## Network recovery

MCP-Pi can optionally monitor the appliance LAN path with the packaged `mcp-gateway-network-recovery.timer`. It is deliberately **disabled by default**: first prove that the current default-route interface is USB-backed, managed by NetworkManager and that its local gateway responds to the bounded probe:

    sudo /bin/sh /home/mcp-gateway/mcp-gateway/config/systemd/mcp-gateway-network-recovery --check

Only after `NETWORK_RECOVERY_CHECK=PASS` should an operator enable it:

    sudo systemctl enable --now mcp-gateway-network-recovery.timer

The timer runs once per minute. A healthy observation only refreshes a volatile baseline under `/run`. Recovery requires three consecutive LAN failures. It first asks NetworkManager to reconnect the known interface; only if that fails, a previously verified USB interface may be unbound/rebound from its recorded driver. USB rebinds are rate-limited to once per 15 minutes. Unknown interfaces, non-USB drivers, missing baseline state or unavailable bind/unbind controls fail closed without mutation.

The probe targets the current local default gateway, not DNS, Internet or the OpenAI tunnel. The recovery worker does not restart MCP services and never reboots the appliance.

Operational evidence is in journald:

    journalctl -u mcp-gateway-network-recovery.service
    journalctl -u mcp-gateway-network-recovery.timer
    journalctl -k
    journalctl -u NetworkManager.service -u wpa_supplicant.service

Disable the optional timer without changing normal MCP-Pi lifecycle:

    sudo systemctl disable --now mcp-gateway-network-recovery.timer

## Performance

Measure before optimizing:

    sudo -u mcp-gateway mcp-gateway benchmark

The command reports measurements from the current execution. Development-host results are useful for regression detection but are not ARMv6 production evidence.

When performance matters, separate in-process Core latency from MCP HTTP overhead and SSH/network/Target execution, and record the exact deployed commit/hardware/configuration with the result.

## Update

Normal user/operator update uses the same installer described in [installation.md](installation.md). Maintainer exact-commit promotion belongs in `CONTRIBUTING.md` in a source checkout, not in the operator lifecycle.

## Recovery

Failure-oriented diagnosis, backup restore and rollback are in [recovery.md](recovery.md).
