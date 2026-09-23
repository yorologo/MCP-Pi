# Troubleshooting

Diagnose the failing layer before changing configuration.

## Installation: no adapter available

Symptom:

```text
no prebuilt adapter is present
```

For an appliance, use an official release bundle containing `bin/mcp-gateway-adapter`. A source checkout can build only when Go is already installed. Do not install a heavy development toolchain on constrained hardware merely to avoid obtaining the correct release artifact.

## Installation preflight

Run without mutation:

```bash
./install.sh --check
```

Fix the first explicit error before executing `sudo ./install.sh`.

## Admin Console is unreachable

Check:

```bash
systemctl status mcp-gateway-admin --no-pager
journalctl -u mcp-gateway-admin -n 100 --no-pager
cat /home/mcp-gateway/.config/mcp-gateway/admin.env
```

Do not publish secret files. `admin.env` should contain only non-secret bind/Host allowlist settings.

A 403 commonly means the requested HTTP `Host` is not allowlisted. Add the real trusted hostname/IP to private `admin.env`, reload systemd and restart only Admin.

## MCP `/ready` fails

```bash
systemctl status mcp-gateway-mcp --no-pager
journalctl -u mcp-gateway-mcp -n 100 --no-pager
curl -i http://127.0.0.1:8090/live
curl -i http://127.0.0.1:8090/ready
```

Check adapter/Python compatibility and token-file permissions before touching the Registry.

## Tunnel says backend unavailable

The optional tunnel depends on MCP readiness. Verify `/ready` first, then inspect:

```bash
systemctl status mcp-gateway-tunnel --no-pager
journalctl -u mcp-gateway-tunnel -n 100 --no-pager
```

Do not regenerate credentials merely because the backend was temporarily restarting.

## Target unreachable

1. Confirm the Target is online.
2. Confirm SSH daemon/port.
3. Open **Targets → Edit Target** and compare the pinned and currently presented fingerprints.
4. If the Target is new, verify the presented fingerprint independently and choose **Trust Host Key**. If it changed, keep the connection blocked unless you independently verify the replacement.
5. Use **Check Connection**/`target_status`.
6. If DHCP changed only the endpoint, allow normal discovery to verify the existing pinned identity before updating it.

Never use `StrictHostKeyChecking=no` or `accept-new` as a diagnostic shortcut.

## `TOOL_NOT_ALLOWED` for a registered client

Open **AI Clients → Grants** for the affected client and confirm that an enabled grant matches the requested Target, Project and tool capability. Use **Check Effective Access** on that page to run the real Policy Engine and surface the exact next gate (`TOOL_NOT_ALLOWED`, `TARGET_SHELL_DISABLED`, `WRITE_NOT_ALLOWED`, etc.). Do not edit SQLite directly or broaden the grant to `*` merely to make the error disappear.

## `WRITES_DISABLED`

Structured filesystem mutations are intentionally disabled when `writes_enabled=false`. Enable them only after verifying Project write scope and client grants.

## `TARGET_SHELL_DISABLED`

`run_command` is controlled separately by `shell_enabled`. Enabling structured writes does not enable trusted Target shell and vice versa.

## Fewer tools than expected

The Core catalog for 1.3.4 contains 21 tools. A client may see fewer because `tools/list` is filtered by its grants. Compare:

- Core catalog metadata;
- registered client identity;
- effective grants;
- MCP adapter/tunnel health;
- actual `tools/list` returned to that client.

Do not broaden grants just to make the number equal 21.

## Update fails

For user updates, use the extracted release bundle and `sudo ./install.sh`. For maintainer exact-commit deployment, launch `scripts/deploy-pi.sh <sha>` only through `scripts/run-resumable.sh`; direct execution is break-glass only.

If activation fails, preserve the previous runtime and evidence before retrying. See [update-rollback.md](update-rollback.md).
