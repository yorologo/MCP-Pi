# Deployment Guide

## Production target

Current MCP-Pi production endpoint:

- Host: `192.168.68.55`
- SSH user: configured locally through `.mcp-pi.local.env`
- Admin Console: `http://192.168.68.55`
- Admin bind: `0.0.0.0:80` (all IPv4 interfaces; access uses the real host IP, never `0.0.0.0`)
- MCP adapter: `127.0.0.1:8090/mcp` (loopback only; exposed to OpenAI only through the Secure MCP Tunnel)

The Admin Console LAN binding is deliberate. The service remains `User=mcp-gateway`; systemd grants only `CAP_NET_BIND_SERVICE` so it can bind TCP/80 without running as root. Authentication, CSRF, strict security headers, and an explicit Host allowlist remain enforced. No reverse proxy or external tunnel is required for LAN access. On an untrusted network, use an SSH tunnel instead of direct LAN HTTP.

## Local prerequisites

- Python 3.11+
- Node.js/npm only for rebuilding Tailwind CSS
- `ssh`, `scp`, `tar`, `sha256sum` and an authorized SSH key (default: `~/.ssh/id_rsa`)
- Go only when rebuilding the MCP adapter
- `config/targets.local.json`
- local `.mcp-pi.local.env` with deployment credentials; never commit this file

## Build and verification

```bash
python -m unittest discover -s tests -p 'test_*.py' -v
node tests/test_app_js.mjs
cd tailwind && npm run build
cd ..
git diff --check
```

## Deploy

```bash
SHA="$(git rev-parse HEAD)"
JOB="deploy-${SHA:0:12}"
scripts/run-resumable.sh start --expect-marker DEPLOYMENT_VERIFIED "$JOB" -- scripts/deploy-pi.sh "$SHA"
```

The deploy script refuses direct execution outside the resumable runner (except explicit `MCP_DEPLOY_ALLOW_DIRECT=1` break-glass recovery), refuses dirty trees and requires the requested SHA to equal both local `HEAD` and `origin/<branch>`. It packages that exact commit with `git archive`, builds/validates an ARMv6 candidate from the archive, creates a rollback copy of the active runtime and systemd units, activates the candidate, restarts Admin → MCP → Tunnel in control-plane-safe order, runs lightweight production acceptance and Doctor, then writes `.deployment.json` with commit/branch/timestamp/package+adapter hashes. `verified=true` is written only after acceptance. `MCP_DEPLOY_INJECT_FAILURE=after-activation` is reserved for a controlled rollback test and must restore the previous known-good runtime.

## Post-deployment checks

From MCP-Pi itself:

```bash
curl -fsS -o /dev/null -w '%{http_code}\n' http://127.0.0.1/login
```

From any other device on the same LAN:

```bash
curl -fsS -o /dev/null -w '%{http_code}\n' http://192.168.68.55/login
```

For the MCP adapter:

```bash
curl -fsS -H 'Host: 127.0.0.1' http://127.0.0.1:8090/live
```

On MCP-Pi also verify:

```bash
systemctl is-enabled mcp-gateway-admin
systemctl is-active mcp-gateway-admin mcp-gateway-mcp mcp-gateway-tunnel
sudo ss -lntp | grep ':80 '
sudo -u mcp-gateway /home/mcp-gateway/mcp-gateway/bin/mcp-gateway doctor
```

Expected Admin listener: `0.0.0.0:80` (shown by `ss` as an IPv4 wildcard listener).

Acceptance requires `.deployment.json.commit == local HEAD == origin/develop`, matching adapter SHA256, healthy services/endpoints/Doctor and successful Target checks. The Raspberry Pi intentionally does not run the full development suite because of its constrained RAM; the full gate runs on the development workstation before push/deploy.
