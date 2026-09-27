# Troubleshooting

## Installation: no compatible binary

Use an official release bundle for the appliance. A source checkout only builds when Go is already present; install.sh does not install a compiler.

## Preflight fails

Run:

    ./install.sh --check

Read the first explicit ERROR. Required release assets or an executable Go binary must be present.

## Admin Console is unreachable

Fresh install binds Admin to 127.0.0.1. Use an SSH port forward or deliberately configure trusted exposure. Check mcp-gateway-admin logs and admin.env.

## MCP /ready fails

Check mcp-gateway-mcp logs and:

    sudo -u mcp-gateway mcp-gateway doctor

A Core/Registry initialization failure intentionally leaves readiness unavailable. There is no alternate runtime fallback.

## Target unreachable

Check endpoint, SSH key, pinned host key and Target enablement. A changed host key must be investigated, not auto-accepted.

## TOOL_NOT_ALLOWED

Verify client enablement and effective grants in Admin Console. tools/list may expose fewer than the 21 catalog tools because it is authorization-filtered.

## WRITES_DISABLED

Enable structured writes only after Project boundaries and grants are correct. writes_enabled is an intentional kill switch.

## TARGET_SHELL_DISABLED

Trusted shell requires shell_enabled plus target_shell authorization. Prefer structured tools when sufficient.

## Privileged Target command denied

Check normal shell/task authorization first, then target_admin grant, Target privilege policy, required approval/boot identity and verified backend. Presence of sudo alone is not enough.

## Backup or restore fails

Do not fall back to raw live-database copying. Check destination ownership/free space and the error from the Go backup/restore command. Restore requires Admin/MCP stopped.

## Reboot fails

gateway_reboot uses systemd-logind and a narrow polkit rule while NoNewPrivileges remains enabled. Missing busctl/logind/policy support is a failure, not a reason to grant general sudo.

## Update fails

Installer/deployer preserve rollback evidence. Use the documented rollback path and inspect the first failed gate before retrying.
