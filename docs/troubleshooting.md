# Troubleshooting

## Installation: no compatible binary

Use an official release bundle for the appliance. A source checkout only builds when Go is already present; `install.sh` does not install a compiler.

## Preflight fails

Run:

    ./install.sh --check

Read the first explicit ERROR. A release bundle must pass its own `SHA256SUMS`, contain required assets and provide an executable binary whose version/API/schema/protocol metadata matches the package contract.

## Registry schema is not current

`status` is intentionally non-mutating. If it reports an older directly supported schema, do not work around it by starting services repeatedly.

For a coordinated lifecycle operation:
1. create/verify a backup;
2. stop Admin/MCP and maintenance DB users;
3. run `mcp-gateway migrate`;
4. run `status`;
5. start services and run Doctor.

Normal install/update performs these steps through `install.sh`.

## Admin Console is unreachable

Fresh install binds Admin to 127.0.0.1. Use an SSH port forward or deliberately configure trusted exposure. Inspect:

    systemctl status mcp-gateway-admin
    journalctl -u mcp-gateway-admin

## MCP /ready fails

Inspect:

    systemctl status mcp-gateway-mcp
    journalctl -u mcp-gateway-mcp
    sudo -u mcp-gateway mcp-gateway doctor

A Core/Registry initialization failure intentionally leaves readiness unavailable.

## Target unreachable

Check endpoint, SSH key, pinned host key and Target enablement. A changed host key must be investigated, not auto-accepted.

## TOOL_NOT_ALLOWED

Verify client enablement and effective grants in Admin Console. `tools/list` may expose fewer tools than the Core catalog because it is authorization-filtered.

## WRITES_DISABLED

Enable structured writes only after Project boundaries and grants are correct.

## TARGET_SHELL_DISABLED

Trusted shell requires `shell_enabled` plus `target_shell` authorization. Prefer structured tools when sufficient.

## Privileged Target command denied

Check normal shell/task authorization first, then `target_admin`, Target privilege policy, approval/boot identity and verified backend.

## Backup or restore fails

Do not fall back to raw live-database copying. Check ownership/free space and the Go backup/restore error. Restore refuses active Admin/MCP/maintenance database users and does not migrate schema.

## Reboot fails

`gateway_reboot` uses systemd-logind and a narrow polkit rule while `NoNewPrivileges` remains enabled. Missing logind/policy support is a failure, not a reason to grant general sudo.

## Update/deployment fails

Both paths use the same canonical installer lifecycle. Inspect the first installer/deployment gate and preserve its rollback evidence before retrying.

For maintainer deployment, use `run-resumable.sh status` / `log` rather than starting a second deployment after a control-session interruption.
