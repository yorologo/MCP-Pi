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

After creation, verify the presented SSH host identity and **Check Connection** before considering the Target ready. Normal SSH operations can recover a moved endpoint after a recognized connection/host-identity failure only when exactly one candidate presents the already pinned key; discovery checks known neighbors first and then an eligible bounded local private IPv4 subnet. The endpoint update is audited and retried once; trust is never rewritten automatically. **Find moved Target** uses the same discovery mechanism but remains a non-mutating administrative check that only reports the candidate.

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

**Edit** on any Grant edits the complete capability set for that client's exact Target/Project scope using the same builder and presets as **Add Grants**. The scope itself is fixed during Edit; use **Add Grants** for another Target/Project. Existing Grants that remain selected keep their ID and enabled/disabled state, while newly selected capabilities are created enabled. **Enable / Disable** remains an individual Grant operation. Saving an empty capability set requires explicit remove-all confirmation, and a stale editor is rejected rather than overwriting a concurrently changed scope.

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

Complete the base MCP-Pi path before configuring any external AI client:

    install/bootstrap
      -> Target
      -> Project
      -> Client
      -> minimal Grants
      -> Check Effective Access
      -> status / doctor
      -> external client integration

External integrations are optional. MCP-Pi installs their systemd units, but it does not invent provider credentials, buy/configure domains or silently install provider-owned tunnel binaries. A missing external prerequisite is a stop condition, not a reason to weaken the local security model.

Never store MCP bearer tokens, tunnel credentials, OAuth client secrets or private SSH keys in Git. Start with a read-only capability such as `health`, verify the complete path, and broaden Grants only when needed.

| Client | External prerequisites | Local optional services | Final acceptance |
| --- | --- | --- | --- |
| ChatGPT | OpenAI tunnel access, official `openai-tunnel-client`, tunnel credentials, ChatGPT-side MCP-Pi app/connector provisioned for the account | `mcp-gateway-tunnel.service` | ChatGPT executes live MCP-Pi `health` |
| Gemini Spark | Cloudflare Zero Trust, a controlled HTTPS domain/hostname, official `cloudflared`, Cloudflare Tunnel/MCP Portal access | `mcp-gateway-gemini.service`, `mcp-gateway-cloudflared.service` | Gemini executes live `gemini-main` `health` |

### ChatGPT through the secure OpenAI tunnel

Server-side flow:

    ChatGPT
      -> approved OpenAI secure tunnel
      -> 127.0.0.1:8090/mcp
      -> chatgpt-main
      -> Go Core / Policy
      -> authorized Targets / Projects

#### Prerequisites

Before changing MCP-Pi, obtain through the approved OpenAI product workflow:

- access to the supported OpenAI secure MCP tunnel;
- the official tunnel client, installed as executable `/usr/local/bin/openai-tunnel-client`;
- `CONTROL_PLANE_TUNNEL_ID`;
- `CONTROL_PLANE_API_KEY`;
- the ChatGPT-side MCP-Pi app/connector provisioned for the intended account.

MCP-Pi cannot generate or recover those provider credentials and does not vendor the OpenAI tunnel binary. If any item above is unavailable, stop here. The local preflight intentionally reports `OPENAI_PRODUCT_GATE_PENDING` rather than exposing another ingress path.

Verify the provider binary before continuing:

    test -x /usr/local/bin/openai-tunnel-client

#### Configure MCP-Pi

1. In **AI Clients**, create or reuse `chatgpt-main` and keep it enabled.
2. Add only the Grants required for the intended work. Use **Check Effective Access** before widening scope.
3. Verify the local MCP endpoint:

       systemctl is-active mcp-gateway-mcp.service
       curl -fsS http://127.0.0.1:8090/ready

4. Create the private tunnel environment file without placing credentials in shell arguments or Git:

       sudo -u mcp-gateway sh -c 'umask 077; cat > /home/mcp-gateway/.config/mcp-gateway/tunnel.env'

   Paste exactly the provider-issued values in environment-file form, then press **Ctrl-D**:

       CONTROL_PLANE_TUNNEL_ID=<provider-issued-value>
       CONTROL_PLANE_API_KEY=<provider-issued-value>

5. Verify ownership/mode without printing the credentials:

       sudo stat -c '%U:%G %a %n' /home/mcp-gateway/.config/mcp-gateway/tunnel.env
       sudo -u mcp-gateway /usr/local/bin/mcp-gateway-tunnel-check credentials

   Expected file ownership is `mcp-gateway:mcp-gateway` with mode `600`, and the credential preflight must exit successfully.

6. Enable the optional tunnel only after MCP readiness and Client policy are correct:

       sudo systemctl enable --now mcp-gateway-tunnel.service
       systemctl is-enabled mcp-gateway-tunnel.service
       systemctl is-active mcp-gateway-tunnel.service
       sudo -u mcp-gateway mcp-gateway doctor

#### Connect ChatGPT and verify

1. In ChatGPT, enable/connect the MCP-Pi app/plugin already provisioned for the account.
2. In a chat, invoke MCP-Pi and request `health`.
3. Accept the integration only when ChatGPT returns live MCP-Pi health data through the connector.

If the ChatGPT-side app/connector has not been provisioned, that is an external product gate; changing Grants or exposing Admin/MCP directly will not solve it.

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

#### Prerequisites

Before enabling the Gemini ingress, prepare:

- a Cloudflare account with Zero Trust/MCP Portal access;
- a controlled domain/HTTPS hostname for the public MCP Portal;
- the official `cloudflared` executable installed at `/usr/local/bin/cloudflared`;
- permission to create a Cloudflare Tunnel and private hostname route;
- a way for the connector host to resolve `gemini-mcp.internal` to the MCP-Pi LAN address.

MCP-Pi installs the `mcp-gateway-cloudflared.service` unit but intentionally does not install or update the provider-owned `cloudflared` binary and does not create the external Cloudflare account/domain configuration.

Install `cloudflared` using Cloudflare's official procedure for the appliance architecture, then verify the exact path expected by the packaged unit:

    test -x /usr/local/bin/cloudflared

#### 1. Prepare MCP-Pi

1. In **AI Clients**, create or reuse `gemini-main`.
2. Grant only the capability required for initial acceptance; `health` is sufficient.
3. Generate the dedicated MCP bearer token using the same native entropy source/format used by the installer:

       sudo -u mcp-gateway sh -c 'umask 077; od -An -N48 -tx1 /dev/urandom | tr -d "[:space:]" > /home/mcp-gateway/.config/mcp-gateway/gemini-mcp.token'

4. Verify the token file without printing its value:

       sudo stat -c '%U:%G %a %s %n' /home/mcp-gateway/.config/mcp-gateway/gemini-mcp.token

   Expected ownership is `mcp-gateway:mcp-gateway`, mode `600`, and a non-zero size.

5. Enable and verify the dedicated ingress:

       sudo systemctl enable --now mcp-gateway-gemini.service
       systemctl is-enabled mcp-gateway-gemini.service
       systemctl is-active mcp-gateway-gemini.service
       curl -fsS http://127.0.0.1:8092/ready

The packaged Gemini unit binds `0.0.0.0:8092` deliberately so a private connector can reach the appliance LAN address. Its explicit Host allowlist still restricts the accepted MCP hostname.

#### 2. Prepare the private Cloudflare route

1. Create a Cloudflare Tunnel and a private hostname route for `gemini-mcp.internal`.
2. When Cloudflare provides the connector/tunnel token, store it through stdin rather than placing the token in a command argument or repository file:

       sudo -u mcp-gateway sh -c 'umask 077; cat > /home/mcp-gateway/.config/mcp-gateway/cloudflared.token'

   Paste only the Cloudflare-issued tunnel token, then press **Ctrl-D**.

3. Verify the file without printing the token:

       sudo stat -c '%U:%G %a %s %n' /home/mcp-gateway/.config/mcp-gateway/cloudflared.token

4. Make `gemini-mcp.internal` resolve from the connector host to the MCP-Pi LAN address. Do not resolve it to `127.0.0.1`.
5. Check system resolution and local overrides before starting the connector:

       getent ahostsv4 gemini-mcp.internal
       grep -n 'gemini-mcp.internal' /etc/hosts

   The effective result must be the appliance LAN address; a stale `/etc/hosts` loopback entry takes precedence over DNS on common NSS configurations.

6. Enable and verify the connector:

       sudo systemctl enable --now mcp-gateway-cloudflared.service
       systemctl is-enabled mcp-gateway-cloudflared.service
       systemctl is-active mcp-gateway-cloudflared.service
       sudo -u mcp-gateway mcp-gateway doctor

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

1. create a portal on the controlled HTTPS hostname, for example `https://mcp-pi.example.com/mcp`;
2. add the synchronized MCP-Pi server;
3. protect the portal with a narrow Access policy for the intended user/account;
4. enable **Managed OAuth**;
5. keep localhost and loopback redirect clients disabled unless a real client requires them;
6. allow the exact Gemini redirect URI when practical. If Spark uses callbacks under the same Google path, use the narrow path wildcard:

       https://oauth-redirect.googleusercontent.com/r/*

Do not widen this to all of `googleusercontent.com`.

Before opening Gemini, verify the public portal is actually protected: an unauthenticated request to the MCP resource should not return an authorized MCP response, and the protected-resource metadata should be available at:

    https://<PORTAL_HOST>/.well-known/oauth-protected-resource/mcp

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

- the base installation/bootstrap is complete and `status`/Doctor are healthy;
- the intended AI Client is enabled;
- its effective Grants match the requested scope;
- the local MCP ingress is ready;
- each required provider binary/credential preflight passes;
- each required optional tunnel/connector is enabled and active;
- Admin was not exposed as part of the MCP path;
- the external client discovers only the expected tools;
- the external client executes `health` successfully;
- temporary write, shell or `target_admin` access used during troubleshooting has been removed.

### Troubleshooting matrix

| Symptom | Check first |
| --- | --- |
| OpenAI tunnel exits before starting | Run `mcp-gateway-tunnel-check credentials`; missing provider credentials are an external product gate, not an MCP readiness failure. |
| OpenAI tunnel credentials pass but the service waits/fails | Verify `http://127.0.0.1:8090/ready` and `mcp-gateway-mcp.service` before changing tunnel configuration. |
| Gemini service does not start | Verify `gemini-mcp.token` exists, is non-empty and is readable by `mcp-gateway`. |
| Cloudflare connector does not start | Verify both `/usr/local/bin/cloudflared` and the private non-empty `cloudflared.token`. |
| Cloudflare reports **Unable to connect to server** | Verify `gemini-mcp.internal` resolves to the appliance LAN IP, `:8092` is listening and `mcp-gateway-cloudflared.service` is active. |
| Local `127.0.0.1:8092` works but Cloudflare does not | Verify the packaged Gemini unit listens on `0.0.0.0:8092` and the private route uses the LAN address rather than loopback. |
| DNS tooling returns the LAN IP but the connector still uses `127.0.0.1` | Inspect `/etc/hosts` and NSS ordering for a stale local override. |
| MCP rejects the Host header | Re-check private-hostname resolution and the explicit `gemini-mcp.internal` Host path; do not bypass Host validation. |
| Cloudflare is ready but expected tools are missing | Check `gemini-main` Grants and **Check Effective Access**; do not broaden the tunnel. |
| Spark says automatic registration failed | Use the OAuth fallback above with the exact copied redirect URI. |
| Spark returns from OAuth but says the account is not linked | Verify the confidential client uses `client_secret_post` and the token endpoint metadata supports it. |
| Integration stops after an update | Run Doctor, then verify the optional Gemini/Cloudflare/OpenAI units remain enabled and active. The installer preserves explicitly enabled optional ingress. |
