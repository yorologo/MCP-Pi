# Recovery

Recovery for the 1.5.0-rc.1 Go-only runtime is deliberately small.

## 1. Service/runtime issue

Inspect:

    systemctl status mcp-gateway-admin mcp-gateway-mcp
    journalctl -u mcp-gateway-admin -u mcp-gateway-mcp
    sudo -u mcp-gateway mcp-gateway doctor

A live endpoint does not imply readiness; Core/Registry initialization must succeed.

## 2. Application rollback

For an installer-managed update:

    sudo /home/mcp-gateway/mcp-gateway/install.sh --rollback

For a maintainer deployment, use the rollback set reported by deploy-pi.sh and the deployment runbook. Do not improvise a second deployment mechanism.

## 3. Registry backup

    sudo -u mcp-gateway mcp-gateway backup

The command uses SQLite's online backup API through the existing Go SQLite dependency and verifies integrity/schema/SHA.

## 4. Registry restore

Stop Admin and MCP before restore so no process retains the old database state, then use:

    sudo -u mcp-gateway mcp-gateway restore <backup.db>

Restart services and run Doctor afterward. Unsupported schema versions fail closed.

## 5. Disaster recovery

    sudo scripts/backup-appliance.sh

The private archive includes the verified Registry snapshot, private local config required for recovery, gateway/host SSH identities, relevant systemd/polkit state, fingerprints and checksums. Optional age encryption is fail-closed.

## Acceptance after recovery

Verify:
- Admin and MCP services active;
- /live and /ready behavior;
- Doctor;
- Registry schema/integrity;
- Target SSH identity;
- client/grant effective access;
- deployment provenance when recovering a deployed release.

Do not declare recovery complete solely because systemd reports a process running.
