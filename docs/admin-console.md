# Admin Console

The Admin Console is served by the same Go binary and Registry used by MCP.

## Access and first login

Fresh install binds Admin to `127.0.0.1`. Prefer an SSH port forward for remote administration.

Configure the Admin user with:

    sudo -u mcp-gateway mcp-gateway setup

Interactive password entry is hidden; automation can use `--password-stdin`. Setup requires an already-current Registry and never migrates it.

## Sections

Admin manages Targets, Projects, AI Clients, Grants, Settings, Activity, System and Maintenance.

Activity Audit stores timestamps in UTC and displays them in the configured IANA time zone. LIVE mode uses HTMX polling every three seconds and preserves active filters.

## Identity and grants

Tables prioritize display names while stable Target/Project/Client keys remain secondary operational identifiers. Internal Grant row IDs are not normal user-facing identity.

Use **AI Clients → Grants** to manage explicit capabilities. **Check Effective Access** evaluates the actual Policy Engine.

## Target privilege

Administrative Target privilege is distinct from ordinary shell access. `run_command` and allowlisted `run_task` fail closed when effective execution is privileged unless `target_admin`, Target policy and any required approval pass.

## Security

Admin enforces Host validation, CSRF protection, signed HttpOnly/SameSite cookies, safe response headers and login rate limiting. Runtime code remains read-only to the service account.

Frontend development and build gates are documented in `CONTRIBUTING.md` in a source checkout.
