# Installation

`install.sh` is the single install/reinstall/update/rollback engine. Maintainer deployment delegates lifecycle mutation to the same installer rather than implementing a second path.

## Requirements

Use an official GitHub Release bundle on the reference ARMv6 appliance whenever possible; it contains the prebuilt gateway and does not require Go.

A source checkout can use the same installer only when a compatible prebuilt gateway is present or Go is already installed. The installer never installs a compiler.

The real install requires root, a usable systemd host and the native commands it uses. Missing preconditions fail before activation.

## Obtain the source or release

Development/source path:

    git clone https://github.com/yorologo/MCP-Pi.git
    cd MCP-Pi

For an appliance, download and extract the desired immutable GitHub Release bundle, then run the same installer from the extracted directory.

A release bundle contains the gateway binary, thin wrappers, systemd/polkit assets, metadata, current operator docs, installer and checksums. It contains no development runtime.

## Optional candidate check

    ./install.sh --check

This is non-mutating. It validates source/release integrity plus the binary/API/catalog/schema/protocol contract and emits `CANDIDATE_CHECK=PASS` on success. It intentionally does not claim that the current machine is an installable appliance.

## Install or reinstall

    sudo ./install.sh

The installer, at a high level:

1. validates host/source/release preconditions;
2. creates or reuses the service account and private persistent directories;
3. stages the candidate runtime without replacing the accepted runtime;
4. prepares a verified SQLite backup plus matching system assets as one rollback set;
5. keeps the previously accepted rollback set intact until the activation boundary;
6. quiesces the appliance;
7. promotes the rollback evidence and activates the candidate;
8. explicitly creates/migrates the Registry;
9. verifies Registry/runtime compatibility;
10. installs systemd/polkit assets while preserving explicitly enabled optional ingress;
11. starts `mcp-gateway.target`;
12. verifies readiness and Doctor before reporting `INSTALL_VERIFIED`.

Persistent state is outside the runtime tree:

    /home/mcp-gateway/mcp-gateway/               root-owned runtime
    /home/mcp-gateway/.local/share/mcp-gateway/ Registry and backups
    /home/mcp-gateway/.config/mcp-gateway/       private config/secrets

## Bootstrap

Interactive/local path:

    sudo -u mcp-gateway mcp-gateway setup

Automation can pass the password through stdin; see:

    mcp-gateway setup --help

A fresh non-interactive install creates:

    /home/mcp-gateway/.config/mcp-gateway/admin-bootstrap.token

The token is private, one-time and short-lived. It is accepted only while no enabled Admin exists and is removed after successful Web or CLI setup.

`INSTALL_VERIFIED` proves the runtime installation. The following `bootstrap=complete|required` marker reports Admin-bootstrap state separately.

Do not place passwords or bootstrap tokens in command arguments, Git, logs or release artifacts.

## First configuration

After bootstrap, use Admin Console to create:

1. Target;
2. Project;
3. AI/MCP Client;
4. minimal Grants.

Verify Target identity and **Check Effective Access** before broadening writes, shell or privilege. The detailed UI/configuration contract is in [configuration.md](configuration.md).

## Updates

Use the new release/source candidate and run the same installer:

    ./install.sh --check     # optional candidate-only check
    sudo ./install.sh

Existing Registry/settings and private configuration are preserved. Schema changes happen only through the explicit migration phase inside the coordinated installer lifecycle; ordinary runtime open, `status`, Doctor and restore do not silently migrate.

Exact supported schema/API/catalog/protocol values belong to `compatibility.json`, `manifest.json`, migrations/tests and `mcp-gateway version --json`.

## Installer rollback

    sudo /home/mcp-gateway/mcp-gateway/install.sh --rollback

Rollback restores the matching pre-install Registry snapshot, previous runtime/system assets and prior service enablement without migrating the restored Registry. It restarts and verifies the appliance before emitting `ROLLBACK_VERIFIED`.

If an update is interrupted while rollback metadata is being promoted, the installer recovers the last complete rollback set on the next invocation.

## Optional ingress

Base installation does not require or automatically enable cloud ingress. Existing private credentials are preserved. Enable optional ingress only after its client identity, token/configuration and grants are ready.
