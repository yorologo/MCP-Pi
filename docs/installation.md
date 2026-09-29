# Installation

`install.sh` is the canonical install/reinstall/update/rollback engine. `scripts/deploy-pi.sh` is a maintainer promotion wrapper around the same installer, not a second activation engine. Publication status belongs to immutable Git tags/GitHub Releases rather than mutable source text.

## Supported path

Recommended appliance path:

    release bundle
      -> ./install.sh --check
      -> sudo ./install.sh
      -> initial Admin setup (/setup one-time token or local CLI)
      -> Admin Console
      -> status / doctor

## Release bundle versus source checkout

An official release bundle contains:
- prebuilt Linux ARMv6 Go gateway binary;
- thin CLI wrappers;
- systemd units and narrow reboot polkit rule;
- manifest/compatibility metadata;
- `install.sh`;
- current operational documentation;
- `SHA256SUMS`.

It does not contain a compiler or second runtime implementation.

A source checkout may build only when Go is already installed; the installer never installs a compiler.

## Preflight

    ./install.sh --check

Preflight is non-mutating. For a release bundle it verifies `SHA256SUMS` before executing the candidate, then checks that the binary version/API/catalog/schema/protocol contract matches release metadata.

## Install or reinstall

    sudo ./install.sh

The installer:
1. validates required tools and release integrity;
2. creates/reuses the `mcp-gateway` service account;
3. stages the Go runtime/configuration;
4. generates missing local secrets from `/dev/urandom`;
5. defaults Admin to `127.0.0.1`;
6. creates a verified online SQLite backup before schema-changing work;
7. saves the previous runtime and system assets;
8. stops Admin/MCP and maintenance database users;
9. activates the root-owned candidate;
10. explicitly creates/migrates the Registry with `mcp-gateway migrate`;
11. verifies Registry/runtime compatibility with non-mutating `status`;
12. installs systemd/polkit assets;
13. starts Admin/MCP, maintenance timer and postboot verification;
14. runs Doctor;
15. offers one-time Web bootstrap on a fresh non-interactive install, or optionally runs interactive CLI Admin setup.

Persistent state remains outside the runtime tree:

    /home/mcp-gateway/mcp-gateway/               root-owned application
    /home/mcp-gateway/.local/share/mcp-gateway/ Registry and backups
    /home/mcp-gateway/.config/mcp-gateway/       private local config/secrets

## Registry compatibility

Fresh install explicitly creates schema 6. Direct explicit migration supports schema 4 -> 5 -> 6, schema 5 -> 6, and already-current schema 6.

Normal runtime open, `status`, Doctor and Restore never silently migrate. Older schemas must first be upgraded by a release that explicitly supports them.

## Security defaults

    gateway_enabled = true
    writes_enabled  = false
    shell_enabled   = false

Existing settings are preserved through normal update.

## Setup

Interactive/local recovery path:

    sudo -u mcp-gateway mcp-gateway setup

For non-interactive automation:

    printf '%s\n' "$PASSWORD" | sudo -u mcp-gateway mcp-gateway setup --password-stdin

Setup requires the Registry to already be current; it never creates or migrates schema.

On a **fresh non-interactive** installation, the installer creates a private one-time token at:

    /home/mcp-gateway/.config/mcp-gateway/admin-bootstrap.token

The installer prints only the local command needed to read it. Visit `/setup`, supply that token and choose the Admin password. The token is mode `0600`, expires after 15 minutes, is accepted only while no enabled Admin exists, and is deleted after successful setup. If it expires, use the local CLI setup command.

Do not place passwords or bootstrap tokens in command arguments, Git, logs or release artifacts. `sudo ./install.sh --no-setup` skips interactive CLI bootstrap; on a fresh Registry it still enables the bounded one-time Web setup flow.

## Rollback

    sudo /home/mcp-gateway/mcp-gateway/install.sh --rollback

Rollback restores the pre-install Registry **without migration**, restores previous runtime/system assets, restarts services and requires Doctor before reporting `ROLLBACK_VERIFIED`.

## Tunnel

The base gateway works without a cloud tunnel. Existing private tunnel credentials are preserved; the installer does not invent or enable a new external path automatically. Tunnel startup waits for MCP `/ready`, not merely `/live`.
