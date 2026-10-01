# Recovery and troubleshooting

Use the same lifecycle primitives as normal operation. Do not invent a second recovery path because a component is unhealthy.

## Diagnose before mutating

Start with:

    systemctl status mcp-gateway.target
    sudo -u mcp-gateway mcp-gateway status
    sudo -u mcp-gateway mcp-gateway doctor

Then inspect the relevant unit journal; the canonical logging commands are listed in [operations.md](operations.md). A live process does not imply readiness.

## Common failures

### Installation has no compatible binary

Use an official release bundle on the ARMv6 appliance. A source checkout only builds when Go is already available; the installer does not install a compiler.

### Candidate check fails

Run:

    ./install.sh --check

Read the first explicit error. This checks candidate integrity/contract only; the real installer separately validates host preconditions.

### Registry schema is not current

`status` is intentionally non-mutating. Do not repeatedly restart services hoping for an implicit migration.

For a manual coordinated recovery:

1. create/verify a backup;
2. stop `mcp-gateway.target`;
3. run `mcp-gateway migrate`;
4. run `status`;
5. start the target and run Doctor.

Normal install/update performs its supported migration inside the installer lifecycle.

### Admin is unreachable

Admin binds to loopback by default. Verify the SSH port forward, then inspect:

    systemctl status mcp-gateway-admin
    journalctl -u mcp-gateway-admin

### MCP /ready fails

Inspect:

    systemctl status mcp-gateway-mcp
    journalctl -u mcp-gateway-mcp
    sudo -u mcp-gateway mcp-gateway doctor

Core/Registry initialization failure intentionally keeps readiness unavailable.

### Target unreachable or moved

Check endpoint, SSH credentials, Target enablement and the pinned host key. A changed host key must be investigated; it is never auto-trusted.

For Android/Termux Targets, `Bad packet length` / `Connection corrupted` can also indicate bytes were injected into the SSH transport by the Android process environment rather than a host-key or cipher problem. One observed case was Bionic systrace output (`B|...|...E|`) caused by enabled Bionic tracing. Inspect the Target locally and start `sshd` from a clean loader environment before changing SSH cryptography or trust. Android's Bionic tracing implementation is documented upstream at <https://android.googlesource.com/platform/bionic/+/refs/heads/main/libc/bionic/bionic_systrace.cpp>.

If a diagnostic trace ever captures private-key contents, treat that client key as compromised and rotate it through the normal SSH identity workflow. Deleting the local trace does not remove copies already exposed elsewhere.

### Appliance disappears from the LAN

If both the OpenAI tunnel and direct SSH to the appliance disappear together, diagnose the network/USB layer before restarting `mcp-gateway-tunnel.service`.

After connectivity returns, preserve evidence first:

    journalctl -k --since '<start>' --until '<end>'
    journalctl -u NetworkManager.service -u wpa_supplicant.service --since '<start>' --until '<end>'
    journalctl -u mcp-gateway-network-recovery.service --since '<start>' --until '<end>'

If the optional network recovery timer is enabled, verify it is active and run its non-mutating precondition check:

    systemctl is-enabled mcp-gateway-network-recovery.timer
    systemctl is-active mcp-gateway-network-recovery.timer
    sudo /bin/sh /home/mcp-gateway/mcp-gateway/config/systemd/mcp-gateway-network-recovery --check

A healthy route migration or successful network-recovery action now refreshes the USB baseline and recycles an already-active OpenAI tunnel once, because long-poll sockets can remain stale across interface changes. Manual tunnel restart should therefore be a diagnostic exception rather than the normal recovery path. Do not add global USB autosuspend overrides, replace the in-kernel Wi-Fi driver, reboot the appliance or create a second watchdog merely because the tunnel was stale. Escalate only from evidence showing the failing layer.

### TOOL_NOT_ALLOWED

Check client enablement, scoped Grants and **Check Effective Access**. `tools/list` is policy-filtered and can expose fewer tools than the Core catalog.

### WRITES_DISABLED / TARGET_SHELL_DISABLED

Confirm the relevant Project/Grant and global switch instead of bypassing the denial; see [configuration.md](configuration.md) and [security.md](security.md).

### Privileged Target command denied

Check ordinary shell/task authorization first, then `target_admin`, Target privilege policy, approval/boot identity and the verified privilege backend.

### Backup or restore fails

Do not copy a live SQLite database as an ordinary file. Use the Go backup/restore commands and inspect their explicit filesystem/write, integrity and quiescence errors.

### Reboot fails

Do not grant general sudo to work around a reboot failure. Gateway reboot uses the constrained native mechanism described in [security.md](security.md).

### Update/deployment interrupted

Preserve rollback evidence. Do not start a second deployment merely because the control session disappeared; maintainers should inspect the resumable deployment job as described in `CONTRIBUTING.md` in a source checkout.

## Installer rollback

For an installer-managed update:

    sudo /home/mcp-gateway/mcp-gateway/install.sh --rollback

Rollback first validates that the previous runtime, installer rollback metadata and required Registry snapshot are present. It then stops the control plane, proves runtime quiescence before mutation, restores the matching pre-install Registry snapshot without migration, restores previous runtime/system assets and service state, and requires Doctor before reporting `ROLLBACK_VERIFIED`.

If an update was interrupted while rollback metadata was being promoted, invoke the canonical installer again rather than reconstructing state manually; it recovers the last complete rollback set before proceeding.

## Registry backup and restore

Create a backup:

    sudo -u mcp-gateway mcp-gateway backup

For manual restore, stop the appliance:

    sudo systemctl stop mcp-gateway.target

Then:

    sudo -u mcp-gateway mcp-gateway restore <backup.db>

Restore verifies the backup and preserves its schema exactly; it does not migrate. It also refuses to proceed while semantic Registry users/schedulers are still active.

If the runtime needs a newer directly supported schema, migrate explicitly while the appliance remains stopped:

    sudo -u mcp-gateway mcp-gateway migrate

Then:

    sudo systemctl start mcp-gateway.target
    sudo -u mcp-gateway mcp-gateway doctor --check-targets

## Disaster recovery

The supported contract is:

1. reinstall an immutable MCP-Pi runtime;
2. restore the Registry from a verified backup;
3. reprovision private SSH/host/tunnel credentials from their authoritative secure source;
4. verify identity, readiness and effective access.

MCP-Pi does not maintain a second supported archive of all host secrets.

## Recovery acceptance

Verify what applies:

- Registry integrity and runtime compatibility;
- `mcp-gateway.target` enabled/active;
- Admin and MCP readiness;
- maintenance timer/postboot state;
- optional ingress when enabled;
- Target SSH identity/reachability;
- client/grant effective access;
- deployment provenance when relevant.

Do not declare recovery complete from a zero exit code or process state alone.
