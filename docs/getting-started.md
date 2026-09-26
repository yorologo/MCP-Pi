# Getting started

This is the shortest supported path from a release bundle to a working MCP-Pi appliance.

## 1. Requirements

Recommended/validated appliance:

- Linux with `systemd`;
- Python 3.11+;
- administrative account with `sudo`/root access;
- network access to the Target(s) you intend to manage.

The primary production validation is Raspberry Pi OS / Debian 13 on ARMv6. The compatibility contract also declares `aarch64` and `x86_64`; those architectures still require their own hardware acceptance before claiming equivalent appliance certification.

## 2. Install

Extract the official release bundle, then:

```bash
sudo ./install.sh --check
sudo ./install.sh
```

The release bundle includes the Linux ARMv6 MCP adapter so a Raspberry Pi A+ does not need Go.

## 3. Configure Admin access

If the installer was non-interactive:

```bash
sudo -u mcp-gateway mcp-gateway setup
```

Set the Admin password when prompted. The setup command then runs Doctor and prints the next action.

## 4. Open Admin Console

Use the appliance LAN address, for example:

```text
http://<gateway-ip>/
```

The installer records LAN binding/Host allowlist in the private file:

```text
/home/mcp-gateway/.config/mcp-gateway/admin.env
```

## 5. Add the first Target and Project

In Admin Console:

1. **Targets → Add Target** — ID, host, port, user and SSH alias if used.
2. Open **Targets → Edit Target**, compare the presented SHA256 host fingerprint with the fingerprint obtained independently on the Target, then choose **Trust Host Key**. Changed fingerprints stay blocked until explicitly replaced.
3. **Projects → Add Project** — select Target and define the allowed root.
4. **AI Clients → Grants** — create the client identity, open its Grants page and grant only the Target/Project capabilities it needs; use **Check Effective Access** before relying on the permission.
5. Enable structured writes or trusted Target shell only if required.

Fresh security defaults are:

```text
gateway_enabled = true
writes_enabled  = false
shell_enabled   = false
```

## 6. Verify

```bash
sudo -u mcp-gateway mcp-gateway status
sudo -u mcp-gateway mcp-gateway doctor --check-targets
```

The exact number of Doctor checks may evolve with the release; success is the reported healthy/degraded status with no error-severity failures and the expected current contracts.

## What you should not need

A normal first install must not require manually editing SQLite, setting `PYTHONPATH`, copying source into `/home/mcp-gateway`, or hand-editing systemd units.

For details see [installation.md](installation.md). For failures see [troubleshooting.md](troubleshooting.md).
