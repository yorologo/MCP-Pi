# Installation

install.sh is the canonical user install/reinstall/rollback entrypoint for the 1.5.0-rc.2 Go-only candidate. scripts/deploy-pi.sh is a separate maintainer exact-commit promotion path.

## Supported path

Recommended appliance path:

    release bundle
      -> ./install.sh --check
      -> sudo ./install.sh
      -> mcp-gateway setup
      -> Admin Console
      -> status / doctor

## Release bundle versus source checkout

An official release bundle contains:
- the prebuilt Linux ARMv6 Go gateway binary;
- thin CLI wrappers;
- systemd units and the narrow reboot polkit rule;
- manifest/compatibility metadata;
- install.sh;
- selected CURRENT operational documentation;
- immutable file checksums.

It does not contain a second runtime implementation.

A source checkout can run ./install.sh --check. If no compatible prebuilt binary exists, it builds only when Go is already installed. The installer does not install a compiler.

## Preflight

    ./install.sh --check

Preflight is non-mutating. It validates required assets and executes the candidate binary's version contract for the current architecture.

## Install or reinstall

    sudo ./install.sh

The installer:
1. verifies required system tools;
2. creates/reuses the mcp-gateway service account;
3. stages only the Go runtime/configuration;
4. generates local secrets from /dev/urandom when absent;
5. defaults Admin to 127.0.0.1;
6. creates an online SQLite backup before schema-changing work;
7. initializes/migrates the Registry with the Go binary;
8. saves the previous runtime and system assets;
9. activates the new runtime as root:root;
10. installs systemd units and least-privilege reboot policy;
11. starts Admin, MCP, maintenance timer and postboot checks;
12. restarts the tunnel only when it was deliberately provisioned;
13. runs Doctor;
14. optionally runs interactive setup.

Persistent state remains outside the runtime tree:

    /home/mcp-gateway/mcp-gateway/               root-owned application
    /home/mcp-gateway/.local/share/mcp-gateway/ Registry and backups
    /home/mcp-gateway/.config/mcp-gateway/       private local config/secrets

## Registry compatibility

Fresh install creates schema 5. The Go migration path directly supports schema 4 to 5 and current schema 5. Older Registries are not silently guessed or rewritten; upgrade them through a supported older release first.

## Security defaults

    gateway_enabled = true
    writes_enabled  = false
    shell_enabled   = false

Existing settings are preserved through normal update.

## Setup

    sudo -u mcp-gateway mcp-gateway setup

For non-interactive automation use password-stdin. Do not store passwords in Git or shell arguments.

## Rollback

    sudo /home/mcp-gateway/mcp-gateway/install.sh --rollback

Rollback stops the control plane, restores the pre-install Registry with the Go restore path, restores previous runtime/system assets, restarts services and requires Doctor before reporting ROLLBACK_VERIFIED.

## Tunnel

The base gateway works without a cloud tunnel. Existing private tunnel credentials are preserved; the installer does not invent or enable a new external path automatically.
