# Security model

MCP-Pi uses explicit authorization and fail-closed behavior instead of trusting a client, IP address or shell by default.

## Core invariants

- deny by default;
- least privilege;
- fail closed on ambiguous identity, policy or compatibility;
- explicit AI client identity and grants;
- Target and Project must both be enabled;
- `StrictHostKeyChecking=yes` for SSH;
- secrets outside Git;
- Admin and MCP use one Gateway Core;
- audit attempt/result around security-sensitive mutations;
- global kill switches can stop classes of actions without deleting configuration.

## Fresh-install defaults

A newly initialized Registry starts with:

```text
gateway_enabled = true
writes_enabled  = false
shell_enabled   = false
```

These are **defaults**, not a claim about a particular running installation. Existing Registries preserve their current values across reinstall/update.

## Structured writes versus trusted Target shell

```mermaid
flowchart TD
    REQ[Requested operation] --> Q{Operation type}
    Q -->|Structured file mutation| W[writes_enabled]
    W --> PW[Project write permission]
    PW --> CP[Canonical path / symlink checks]
    CP --> FILE[Bounded filesystem mutation]

    Q -->|run_command| SH[shell_enabled]
    SH --> GR[target_shell / compatible grant]
    GR --> SC[Target + Project authorization scope]
    SC --> CMD[Trusted Target shell]
```

### Structured filesystem tools

`write_file`, `append_file`, `delete_file`, `copy_file`, `move_file` and `mkdir` require the write switch and Project write permission. Destinations are resolved on the Target and must remain under the canonical Project root. Symlink escapes fail closed.

### `run_command`

`run_command` is intentionally **not** presented as an arbitrary anonymous shell and not as a Project sandbox. It is available only to an authenticated client with explicit shell capability/grant, enabled Target/Project scope, `gateway_enabled=true`, `shell_enabled=true`, and audit availability.

The Project supplies authorization scope and default cwd. A shell command may have system effects outside that filesystem root, which is why this capability is higher risk and disabled by default on a fresh Registry.

## Target administrative privileges

Target system privilege is a second gate on top of normal execution authorization. It does not create another shell tool or another ACL system. Explicit elevation is requested through `run_command`; an allowlisted `run_task` also crosses this gate when MCP-Pi observes that its base transport is already root/Administrator.

A privileged `run_command` requires all normal shell gates plus:

- an explicit `target_admin` capability in a matching client grant; a wildcard `*` capability deliberately does **not** imply this newly introduced system privilege;
- a Target `privilege_policy` of `ask_always`, `ask_once_per_boot` or `always_allow`;
- any human approval required by that policy;
- a privilege backend whose real state was successfully probed.

Policies are deny-by-default:

| Policy | Behavior |
| --- | --- |
| `never` | privileged Target execution is denied |
| `ask_always` | one Admin Console approval authorizes exactly the next privileged request for the selected client/project scope and expires after a short window |
| `ask_once_per_boot` | a client/project-scoped approval remains valid only while the observed Target `boot_id` is unchanged |
| `always_allow` | no per-request approval is required after a separate risk confirmation and Admin-password re-authentication; grants, shell gates, Target/Project state, audit and backend verification still apply |

The platform backend is deliberately small and native-first. Android/Termux reuses Shizuku/`rish` when available, while an already elevated root/Administrator transport is detected rather than silently treated as standard. Linux and Windows may instead use the Target's optional `privilege_user`: MCP-Pi opens a second SSH login to the **same pinned Target** with the same Gateway key only after authorization succeeds, and accepts it only when a probe observes `root` or `Administrator`. A generic Linux `sudo sh -lc` path and interactive Windows/UAC elevation are deliberately **not** treated as ready backends; they would move or weaken the privilege boundary outside the policy engine. Because Android's `shell` UID cannot traverse Termux private app storage, Shizuku-backed commands execute from `/`; the Project remains authorization scope, not an inherited privileged filesystem cwd.

A transport that is already root/Administrator cannot bypass the policy by requesting `privilege=standard`: observed effective privilege is authoritative and the same target-admin/policy gates are applied. The lightweight pre-execution probe also treats an arbitrary shell as privilege-capable when the normal account exposes a detectable independent elevation path (for example Termux `rish`, Windows `sudo`, or working non-interactive Linux `sudo`). Such a standard shell still starts as the normal account, but it must cross `target_admin` plus the Target policy because the command could invoke that elevator itself.

`target_admin` governs elevation that MCP-Pi requests or can observe on the transport; it is not an operating-system sandbox around `run_command`. MCP-Pi therefore does not attempt brittle command-string filtering. Native least-privilege Target accounts remain preferred, and structured filesystem tools keep their separate bounded Project-path authorization model.

## Kill switches

| Setting | Effect |
| --- | --- |
| `gateway_enabled` | blocks normal client operations globally while keeping health diagnostics available |
| `writes_enabled` | blocks structured project filesystem mutations |
| `shell_enabled` | blocks trusted Target shell execution |

The switches do not erase clients/grants/projects. They are reversible operational controls.

## SSH trust

Never replace host-key pinning with `StrictHostKeyChecking=no` or `accept-new` on production paths. A mutable IP is not identity.

Service identities should use the minimum required key options (for example `restrict` where compatible with required transport behavior) and file permissions such as `0600` for private keys/tokens.

## Admin Console

Admin security includes:

- password-hashed local Admin accounts;
- rate limiting;
- CSRF on mutations;
- Host allowlist;
- CSP and security headers;
- `HttpOnly` / `SameSite=Strict` session cookie;
- service runs as `mcp-gateway`, not root;
- only `CAP_NET_BIND_SERVICE` is granted when binding TCP/80.

Fresh unit defaults are loopback-safe. The installer may create private `admin.env` to opt the appliance into trusted-LAN access with an explicit Host allowlist.

## MCP HTTP

The MCP adapter is loopback-first. Production cloud ingress, when used, occurs through an authenticated tunnel client rather than exposing the MCP port directly to the Internet.

## Audit

Critical mutations require the audit sink to be available before execution. If audit cannot be recorded where required, the operation must fail rather than execute silently.

Activity records include actor, Target/Project scope, action, success/failure and request correlation metadata; secrets should not be logged.

## Service privileges

The runtime service user must not gain general sudo. Administrative installation/update actions are performed by a separate administrator/root path, while normal runtime remains unprivileged.

See [architecture.md](architecture.md) and [operations.md](operations.md).
