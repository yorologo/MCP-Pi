# Installation

`install.sh` is the canonical install/reinstall/update/rollback engine for the 1.5.0-rc.2 Go-only candidate. `scripts/deploy-pi.sh` is a maintainer promotion wrapper around the same installer, not a second activation engine.

## Supported path

Recommended appliance path:

    release bundle
      -> ./install.sh --check
      -> sudo ./install.sh
      -> sudo -u mcp-gateway mcp-gateway setup
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
15. optionally runs interactive Admin setup.

Persistent state remains outside the runtime tree:

    /home/mcp-gateway/mcp-gateway/               root-owned application
    /home/mcp-gateway/.local/share/mcp-gateway/ Registry and backups
    /home/mcp-gateway/.config/mcp-gateway/       private local config/secrets

## Registry compatibility

Fresh install explicitly creates schema 5. Direct explicit migration supports schema 4 -> 5 and already-current schema 5.

Normal runtime open, `status`, Doctor and Restore never silently migrate. Older schemas must first be upgraded by a release that explicitly supports them.

## Security defaults

    gateway_enabled = true
    writes_enabled  = false
    shell_enabled   = false

Existing settings are preserved through normal update.

## Setup

    sudo -u mcp-gateway mcp-gateway setup

For non-interactive automation:

    printf '%s\n' "$PASSWORD" | sudo -u mcp-gateway mcp-gateway setup --password-stdin

Setup requires the Registry to already be current; it never creates or migrates schema.

Do not place passwords in command arguments or Git. To install without interactive bootstrap, use:

    sudo ./install.sh --no-setup

and run Setup later.

## Rollback

    sudo /home/mcp-gateway/mcp-gateway/install.sh --rollback

Rollback restores the pre-install Registry **without migration**, restores previous runtime/system assets, restarts services and requires Doctor before reporting `ROLLBACK_VERIFIED`.

## Tunnel

The base gateway works without a cloud tunnel. Existing private tunnel credentials are preserved; the installer does not invent or enable a new external path automatically. Tunnel startup waits for MCP `/ready`, not merely `/live`.
