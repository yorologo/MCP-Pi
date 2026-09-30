# Configuration

This document is the operator source for Admin Console, Targets/Projects/Clients and optional ingress. Exact CLI flags belong to `mcp-gateway <command> --help`.

## Runtime paths

    runtime:          /home/mcp-gateway/mcp-gateway
    Registry/backups: /home/mcp-gateway/.local/share/mcp-gateway
    private config:   /home/mcp-gateway/.config/mcp-gateway

Runtime files are root-owned. Mutable daemon state belongs to the `mcp-gateway` service account.

`MCP_GATEWAY_DB` can override the SQLite path for CLI/tests. Production uses the persistent data path above.

## Admin access and first login

Admin binds to loopback by default. From a remote workstation use an SSH forward:

    ssh -L 8080:127.0.0.1:80 <user>@<appliance>

Then open `http://127.0.0.1:8080/`.

A fresh non-interactive install exposes `/setup` only while no enabled Admin exists and a valid one-time bootstrap token is present. Successful Web or CLI setup removes the token.

Local setup/recovery:

    sudo -u mcp-gateway mcp-gateway setup

Common Admin environment overrides are:

- `MCP_ADMIN_HOST` — bind override; Go supplies the default;
- `MCP_ADMIN_PORT` — port override; Go supplies the default;
- `MCP_ADMIN_ALLOWED_HOSTS` — explicit Host allowlist;
- `MCP_ADMIN_SECRET_FILE` — private cookie-signing secret.

Regenerating the signing secret invalidates existing Admin sessions.

`admin_timezone` is a Registry setting using an IANA timezone name. It changes presentation/date filters only; audit timestamps remain UTC.

## Initial model

The normal configuration order is:

    Target -> Project -> Client -> Grants -> Check Effective Access

### Target

Target identity is the configured Target plus its pinned SSH host key. Host/IP/port are mutable endpoint data.

After creation, verify the presented SSH host identity and **Check Connection** before considering the Target ready. **Find moved Target** can locate a changed endpoint among known neighbors only when the pinned key matches; it reports a candidate and never silently rewrites trust.

### Project

A Project defines the authorized root and read/write policy. `run_command` requires an explicit Project; MCP-Pi does not guess scope.

Projects can also define typed, allowlisted Tasks for `run_task`. Prefer those or structured tools when they satisfy the job instead of broad trusted shell.

### Client and Grants

Each persisted Grant authorizes exactly one capability at an explicit Target/Project scope. Use the narrowest scope and capability that work. Project read/write policy, global kill switches and Target privilege policy remain independent gates.

Admin **Add Grants** can create several capabilities for one common Target/Project scope in a single all-or-nothing operation. The preset controls are interface shortcuts only:

- **Read only** → `read`
- **Structured operator** → `read`, `write`, `tasks`
- **Trusted shell** → `read`, `write`, `tasks`, `target_shell`
- **Privileged shell** → Trusted shell plus explicit `target_admin`
- **Gateway diagnostics** → `status`, `doctor`
- **Gateway maintenance** → diagnostics plus `backup`, `maintenance`
- **Gateway administrator** → `admin`
- **All ordinary access** → `*`

Presets are not roles and are never stored or consulted by Policy. **Advanced / Customize capabilities** exposes the real current Policy capability catalog as ordinary form checkboxes. Whether chosen through a preset or manually, each resulting capability remains a separate Grant and a separately auditable authorization record.

Selections containing `*` or `target_admin` require explicit **High-impact access** confirmation even for a specific Target and Project. The server repeats this check; JavaScript is only progressive enhancement. `*` covers compatible ordinary capabilities but never grants `target_admin`, and `target_admin` does not grant `target_shell`.

**Check Effective Access** continues to use the real Policy Engine. For `run_command` it also evaluates the live Target privilege gate without consuming a pending one-use approval.

## Safety defaults

Fresh Registry defaults keep:

    gateway_enabled=true
    writes_enabled=false
    shell_enabled=false

Enable structured writes or trusted shell only after Project boundaries and grants are correct.

Target administrative privilege is separate from ordinary shell access; see [security.md](security.md).

## Optional MCP ingress

Private tokens/configuration live under the service config directory and never in Git or release artifacts.

The primary external-client path terminates at the loopback MCP service. Before any secure tunnel is considered healthy, the MCP service must be ready, its private bearer token must exist and the intended client must have effective grants. The tunnel transports MCP; authorization remains in Go Core/Policy.

An optional Gemini ingress has a distinct token and Registry identity and is installed disabled by default. It may be exposed only through deliberately configured trusted/private infrastructure.

The packaged Cloudflare connector is also disabled by default. Its token remains private mutable state. Doctor may report only safe local evidence such as resolved binary path, file size/SHA-256 fingerprint and token ownership/mode; it does not expose token contents or claim external provenance from a locally computed hash.

Admin is not part of the external MCP path and should remain loopback unless the operator deliberately establishes another trusted access model.

## Connecting AI clients

These procedures cover the two externally validated client paths. Connectivity only transports MCP traffic; Go Core/Policy remains authoritative for discovery and execution.

Never store MCP bearer tokens, tunnel credentials, OAuth client secrets or private SSH keys in Git. Start with a read-only capability such as `health`, verify the complete path, and broaden Grants only when needed.

### ChatGPT through the secure OpenAI tunnel

Server-side flow:

    ChatGPT
      -> approved OpenAI secure tunnel
      -> 127.0.0.1:8090/mcp
      -> chatgpt-main
      -> Go Core / Policy
      -> authorized Targets / Projects

1. In **AI Clients**, create or reuse `chatgpt-main` and keep it enabled.
2. Add only the Grants required for the intended work. Use **Check Effective Access** before widening scope.
3. Verify the local MCP endpoint:

       systemctl is-active mcp-gateway-mcp.service
       curl -fsS http://127.0.0.1:8090/ready

4. Provision the official tunnel client and private `/home/mcp-gateway/.config/mcp-gateway/tunnel.env` through the approved operator workflow. These credentials are mutable private state, not release artifacts.
5. Enable the optional tunnel only after MCP readiness and Client policy are correct:

       sudo systemctl enable --now mcp-gateway-tunnel.service
       systemctl is-enabled mcp-gateway-tunnel.service
       systemctl is-active mcp-gateway-tunnel.service
       sudo -u mcp-gateway mcp-gateway doctor

6. In ChatGPT, connect or enable the MCP-Pi app/plugin provisioned for the account.
7. In a chat, invoke MCP-Pi and request `health`.
8. Accept the integration only when ChatGPT returns live MCP-Pi health data through the connector.

If ChatGPT can see the connector but a tool is missing, inspect `chatgpt-main` Grants and **Check Effective Access** before changing the tunnel. A connected transport does not widen the tool catalog.

### Gemini Spark through Cloudflare

Validated flow:

    Gemini Spark
      -> Cloudflare Managed OAuth / MCP Portal
      -> Cloudflare private tunnel
      -> gemini-mcp.internal:8092/mcp
      -> gemini-main
      -> Go Core / Policy
      -> authorized Targets / Projects

#### 1. Prepare MCP-Pi

1. In **AI Clients**, create or reuse `gemini-main`.
2. Grant only the capability required for initial acceptance; `health` is sufficient.
3. Provision `/home/mcp-gateway/.config/mcp-gateway/gemini-mcp.token` as private mutable state owned by `mcp-gateway` with mode `0600`.
4. Enable and verify the dedicated ingress:

       sudo systemctl enable --now mcp-gateway-gemini.service
       systemctl is-enabled mcp-gateway-gemini.service
       systemctl is-active mcp-gateway-gemini.service
       curl -fsS http://127.0.0.1:8092/ready

The packaged Gemini unit binds `0.0.0.0:8092` deliberately so a private connector can reach the appliance LAN address. Its explicit Host allowlist still restricts the accepted MCP hostname.

#### 2. Prepare the private Cloudflare route

1. Create a Cloudflare Tunnel and a private hostname route for `gemini-mcp.internal`.
2. Make `gemini-mcp.internal` resolve from the connector host to the MCP-Pi LAN address. Do not resolve it to `127.0.0.1`.
3. Check local resolution and overrides:

       getent ahostsv4 gemini-mcp.internal
       grep -n 'gemini-mcp.internal' /etc/hosts

4. Provision the Cloudflare tunnel token in `/home/mcp-gateway/.config/mcp-gateway/cloudflared.token` as private `0600` state.
5. Enable and verify the connector:

       sudo systemctl enable --now mcp-gateway-cloudflared.service
       systemctl is-enabled mcp-gateway-cloudflared.service
       systemctl is-active mcp-gateway-cloudflared.service

The Cloudflare tunnel token and the Gemini MCP bearer token are different secrets and must not be reused.

#### 3. Register the upstream MCP server in Cloudflare

In **Cloudflare Zero Trust -> MCP servers**:

1. create a server for MCP-Pi;
2. use the private upstream URL:

       http://gemini-mcp.internal:8092/mcp

3. route it through Cloudflare Gateway when using the private hostname route;
4. authenticate the upstream with a custom `Authorization: Bearer <GEMINI_MCP_TOKEN>` header whose value matches `gemini-mcp.token`;
5. synchronize capabilities.

The server must reach **Ready/Prepared** and expose only the tools authorized to `gemini-main`.

#### 4. Create the MCP Portal

In **Cloudflare Zero Trust -> MCP Portals**:

1. create a portal on a controlled HTTPS hostname such as `https://mcp-pi.example.com/mcp`;
2. add the synchronized MCP-Pi server;
3. protect the portal with a narrow Access policy for the intended user/account;
4. enable **Managed OAuth**;
5. keep localhost and loopback redirect clients disabled unless a real client requires them;
6. allow the exact Gemini redirect URI when practical. If Spark uses callbacks under the same Google path, use the narrow path wildcard:

       https://oauth-redirect.googleusercontent.com/r/*

Do not widen this to all of `googleusercontent.com`.

#### 5. Connect Gemini Spark

In Gemini Spark:

1. open **Apps connected -> Custom apps**;
2. add the portal URL, for example `https://mcp-pi.example.com/mcp`;
3. let Spark attempt automatic OAuth registration first;
4. complete the Cloudflare Access authorization flow;
5. when the portal asks which server the client may use, select MCP-Pi and continue;
6. confirm Gemini lists the expected MCP-Pi actions, then save/connect the custom app;
7. ask Gemini to execute `health`.

Acceptance requires a real tool result from `gemini-main`, not merely a connected or ready badge in Cloudflare.

#### Gemini OAuth fallback

If Spark says automatic registration could not be completed and asks for **Client ID** and **Client Secret**:

1. use **Copy redirect URI** in Spark and retain that exact HTTPS URI;
2. read the portal protected-resource metadata at `https://<PORTAL_HOST>/.well-known/oauth-protected-resource/mcp`;
3. follow its `authorization_servers` issuer and read `<ISSUER>/.well-known/oauth-authorization-server`;
4. fail closed unless the metadata exposes a `registration_endpoint` and supports `client_secret_post`;
5. register a confidential OAuth client at that endpoint with the exact Spark redirect URI and:

       {
         "client_name": "Gemini Spark - MCP-Pi",
         "redirect_uris": ["<EXACT_SPARK_REDIRECT_URI>"],
         "grant_types": ["authorization_code", "refresh_token"],
         "response_types": ["code"],
         "token_endpoint_auth_method": "client_secret_post"
       }

6. keep the returned `client_id` and `client_secret` outside Git and enter them in Spark;
7. repeat the authorization flow.

If authorization returns to Gemini but Gemini still says the account must be linked, verify `client_secret_post` before assuming the successful callback proved token exchange succeeded.

### External-client acceptance checklist

Before declaring either integration complete:

- the intended AI Client is enabled;
- its effective Grants match the requested scope;
- the local MCP ingress is ready;
- each required optional tunnel/connector is enabled and active;
- Admin was not exposed as part of the MCP path;
- the external client discovers only the expected tools;
- the external client executes `health` successfully;
- temporary write, shell or `target_admin` access used during troubleshooting has been removed.

### Troubleshooting matrix

| Symptom | Check first |
| --- | --- |
| Cloudflare reports **Unable to connect to server** | Verify `gemini-mcp.internal` resolves to the appliance LAN IP, `:8092` is listening and `mcp-gateway-cloudflared.service` is active. |
| Local `127.0.0.1:8092` works but Cloudflare does not | Verify the packaged Gemini unit listens on `0.0.0.0:8092` and the private route uses the LAN address rather than loopback. |
| DNS tooling returns the LAN IP but the connector still uses `127.0.0.1` | Inspect `/etc/hosts` and NSS ordering for a stale local override. |
| MCP rejects the Host header | Re-check private-hostname resolution and the explicit `gemini-mcp.internal` Host path; do not bypass Host validation. |
| Cloudflare is ready but expected tools are missing | Check `gemini-main` Grants and **Check Effective Access**; do not broaden the tunnel. |
| Spark says automatic registration failed | Use the OAuth fallback above with the exact copied redirect URI. |
| Spark returns from OAuth but says the account is not linked | Verify the confidential client uses `client_secret_post` and the token endpoint metadata supports it. |
| Integration stops after an update | Run Doctor, then verify the optional Gemini/Cloudflare/OpenAI units remain enabled and active. The installer preserves explicitly enabled optional ingress. |
