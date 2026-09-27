# Admin Console

The Admin Console in MCP-Pi 1.5.0-rc.2 is served by the same Go binary and Registry used by MCP.

## Access

Fresh install binds to 127.0.0.1. Prefer an SSH port forward for remote administration. Do not expose it broadly merely for convenience.

## First login

Run:

    sudo -u mcp-gateway mcp-gateway setup

Interactive password entry is hidden. The signing secret comes from MCP_ADMIN_SECRET_FILE or the default private config location.

## Sections

Admin manages Targets, Projects, AI Clients, Grants, Settings, Activity, System and Maintenance.

Activity Audit stores its immutable timestamps in UTC but displays them in the configured Admin IANA time zone. LIVE mode reuses HTMX polling every three seconds and preserves the active filters; no WebSocket service is required.

## Administrative tables

Mutations check Store errors before reporting success. Related multi-step Registry updates use the existing transaction mechanism where atomicity is required.

## Client grants

Use AI Clients → Grants to add, edit, toggle or delete explicit capabilities. Check Effective Access evaluates actual policy rather than inferring it from table rows.

## Target privilege policy

Administrative Target capability is distinct from ordinary shell access. Configure the target_admin grant and Target policy deliberately. Both arbitrary `run_command` execution and allowlisted `run_task` execution fail closed when the effective Target transport is privileged unless the `target_admin` gate and Target policy/approval pass.

## Security

Admin uses Host validation, CSRF protection, signed cookies, HttpOnly and SameSite Strict behavior, safe response headers and login rate limiting. Runtime code remains read-only to the service account.

## Frontend development

Go Admin templates/static files are canonical:

    mcp-adapter/internal/admin/templates
    mcp-adapter/internal/admin/static

Validate frontend changes with:

    node mcp-adapter/internal/admin/app_js_test.mjs
    cd tailwind && npm run build

Tailwind writes directly to the embedded Go Admin static directory, avoiding duplicate asset trees.
