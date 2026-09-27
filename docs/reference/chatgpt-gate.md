# ChatGPT integration gate and secure MCP tunnel

This reference describes the optional external MCP access path for the 1.5.0-rc.4 Go-only gateway.

## Local gate

Before any tunnel is considered healthy:
- mcp-gateway-mcp must be active;
- /live must respond;
- /ready must confirm initialized Go Core/Registry;
- the private bearer token must be present;
- the tunnel client must target loopback MCP, not bypass policy;
- effective client grants must be checked.

Admin is not part of the external MCP exposure path and remains loopback by default.

## Security boundary

Request flow:

    ChatGPT / MCP client
      -> authorized secure tunnel
      -> loopback Go MCP endpoint
      -> Go Core / policy
      -> SQLite Registry
      -> pinned SSH Target

The tunnel transports MCP. It does not make authorization decisions and cannot bypass the Core.

## systemd service

mcp-gateway-tunnel.service is optional. Base installation does not enable it automatically. Existing credentials/configuration are private mutable state and are not release artifacts.

## Failure behavior

If the local MCP endpoint is not ready, the tunnel must not be treated as healthy. Missing token, client, configuration or backend readiness is a failure, not a reason to expose another port.

## Provisioning

Provision the official tunnel client and private tunnel.env through the approved operator workflow, then enable/restart the tunnel service deliberately. Verify MCP policy with the intended client identity after connectivity succeeds.
