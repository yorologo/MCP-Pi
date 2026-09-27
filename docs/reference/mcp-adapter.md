# Official MCP adapter and Go Core

## Overview

MCP-Pi 1.5.0-rc.1 uses github.com/modelcontextprotocol/go-sdk v1.7.0. MCP transport and the canonical Gateway Core live in the same Go process.

## Boundaries

| Component | Responsibility |
| --- | --- |
| MCP server | stdio/Streamable HTTP protocol, client identity, tool registration |
| Go Core | canonical tool behavior, limits, policy orchestration and audit |
| Registry | schema 5 state for Targets, Projects, clients, grants and settings |
| Remote transport | pinned OpenSSH and platform-native execution |
| Go Admin | human management of the same Registry/policy model |

There is no per-call bridge process and no alternate Core.

## Tool catalog

Catalog v4 contains 21 deterministic tools. tools/list is projected through client authorization, so an individual client can see fewer.

run_command accepts privilege=standard|required. required is not a bypass; the Core still requires target_shell, target_admin where applicable, Target policy and a verified privilege backend.

Administrative no-argument tools use closed schemas where appropriate.

## Transports

stdio is used for forced-command/local MCP integration. Streamable HTTP is used by the appliance MCP service and optional secure tunnel. HTTP readiness is false unless the Go Core/Registry contract is valid.

## Identity

Forced SSH command or HTTP authentication binds a client ID before tool discovery/invocation. A request cannot supply arbitrary client identity to override that binding.

## Version contract

mcp-gateway version --json exposes the canonical build/API/catalog/schema/protocol contract for installers, deployment and validation tooling.
