# Getting started

The current source candidate is MCP-Pi 1.5.0-rc.1 and uses a Go-only appliance runtime.

## 1. Requirements

For a release-bundle install:
- supported Linux appliance and systemd;
- OpenSSH client;
- curl and standard Unix utilities;
- root access for installation.

Python is not required. Go is not required when the release bundle contains the matching prebuilt gateway binary.

## 2. Install

    ./install.sh --check
    sudo ./install.sh

The installer creates or reuses the mcp-gateway service account, preserves persistent state, installs root-owned runtime files and starts the Go Admin/MCP services.

## 3. Configure Admin access

Run:

    sudo -u mcp-gateway mcp-gateway setup

Interactive password input is not echoed. For automation, use the documented password-stdin option instead of placing secrets in command arguments.

Admin binds to 127.0.0.1 by default. Access it through an SSH port forward unless you deliberately configure another trusted exposure.

## 4. Add the first Target and Project

In Admin Console:
1. add a Target and verify its SSH identity;
2. add a Project root for that Target;
3. add an AI client;
4. grant only the required capabilities;
5. use Check Effective Access before enabling broader operations.

## 5. Verify

    sudo -u mcp-gateway mcp-gateway status
    sudo -u mcp-gateway mcp-gateway doctor

A fresh Registry starts with writes and trusted shell disabled.

## What you should not need

A normal release install should not require a compiler, container engine, reverse proxy, separate database server or language runtime.
