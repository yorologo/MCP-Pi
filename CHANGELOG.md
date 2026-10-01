# Changelog

All notable user-visible changes are documented here. Immutable Git tags/GitHub Releases identify published artifacts; Git history preserves removed historical detail.

## Unreleased

### Distribution
- Tag-release verification now re-fetches the immutable remote tag object after `actions/checkout`, avoiding checkout's local annotated-tag normalization while preserving the existing exact-commit and `--verify-tag` gates.

## 1.6.7

### Admin / authorization
- Client Grant creation now supports atomic multi-capability **Add Grants** while preserving one capability per persisted/audited Grant and the existing Registry uniqueness constraint.
- Admin presets are presentation-only shortcuts over the Policy capability catalog; Advanced customization submits real capability values and does not introduce roles, permission inheritance or a second authorization path.
- `*` and `target_admin` now require explicit high-impact confirmation regardless of Target/Project wildcard scope; `*` still never implies `target_admin`, and `target_admin` still never implies trusted shell.
- Grant selection remains usable without JavaScript; progressive enhancement adds preset expansion, compact selection summaries and accessible hover/focus/touch descriptions.

### Reliability / network recovery
- Adds an opt-in systemd network-recovery timer for USB Wi-Fi appliances. It establishes a healthy USB baseline, waits for consecutive local-gateway failures, reuses NetworkManager first, and only then permits a rate-limited bind/unbind of that exact USB interface.
- Network recovery is disabled by default, fails closed when interface/driver/bind evidence is incomplete, never restarts MCP ingress or reboots the host, and reports its state through existing systemd/journald plus Doctor/status inventory.
- Installer rollback and exact-SHA deployment now preserve and verify the optional timer when it was already enabled.
- Recovery documentation distinguishes appliance Wi-Fi loss from tunnel failure and records the Android/Termux Bionic tracing failure mode that can corrupt SSH transport bytes.

### Fixed
- Restores fail-closed dynamic Target endpoint recovery in the Go SSH transport: recognized connection/host-identity failures reuse the pinned-key discovery path, atomically update only the mutable endpoint with audit evidence, and retry exactly once; authentication failures and command timeouts do not trigger discovery.
- Makes read-only SQLite inspection honor the same bounded 5 s busy timeout as normal Registry connections, preventing transient post-restart locks from causing false deployment-acceptance failures.
- Termux/Shizuku readiness probing now retries transient empty `rish` responses within the existing bounded probe window, reducing false `backend_ready=false` results without weakening fail-closed privilege checks.
- Doctor now validates the maintenance timer by active lifecycle state under `mcp-gateway.target` instead of requiring a redundant independent enablement symlink.
- Local HTTP readiness probes remain bounded but allow five seconds, preventing false postboot failure on the constrained ARMv6 appliance under startup load.

### Lifecycle / recovery
- Adds `mcp-gateway.target` as the canonical systemd lifecycle unit for start/stop/restart while keeping optional ingress services opt-in.
- Installer updates now stage the complete rollback set and promote it only at the activation boundary, keeping runtime/system/Registry rollback evidence aligned.
- Candidate-only `install.sh --check` now reports `CANDIDATE_CHECK=PASS`; real install separately verifies root, required host tooling and a usable systemd manager before mutation.
- Install success now reports Admin bootstrap state separately as `bootstrap=complete|required`.

### Maintenance / bootstrap
- Maintenance rotates managed periodic/install/restore backups while protecting the Registry snapshot referenced by the active rollback set and leaving unknown/manual backups untouched.
- Successful maintenance now requires its terminal audit record to persist before PASS is returned.
- CLI Admin setup reuses `Store.SetAdminPassword` and invalidates any remaining one-time Web bootstrap token; security-update package state is informational rather than mislabeled as OK.

### Distribution / operations
- ChatGPT/Gemini onboarding now makes provider-owned prerequisites, local secret provisioning, binary preflights and end-to-end acceptance explicit in the existing configuration guide instead of relying on hidden/manual setup knowledge.
- Maintainer deployment requires explicit `MCP_PI_HOST` and `MCP_PI_USER` configuration instead of repository-specific fallbacks.
- Version-tag CI validates the exact tag and can publish or verify the canonical ARMv6 bundle plus checksum as GitHub Release assets after the normal validation job passes.
- CURRENT documentation is consolidated into README plus five responsibility-based guides: README owns install/bootstrap/first run, Operations owns updates, and Recovery owns rollback/restore. The obsolete installation guide and dated archive tree are removed from HEAD while Git preserves historical reconstruction.

### Compatibility
- No Registry schema, Tool Catalog, Core API, Bridge API or MCP protocol change; machine-readable metadata remains authoritative.

### Validation
- Release promotion requires the complete local Go/frontend/lifecycle/build gates, exact-SHA remote CI and canonical bundle inspection. Production deployment/acceptance remains a separate evidence boundary.

## 1.6.6

### Security / authorization
- `run_command` now requires an explicit Project ID in the MCP contract and runtime; authorization scope is never guessed.
- Standard shell execution no longer depends on or probes an optional privilege backend such as Shizuku. Explicit `required` elevation and already-elevated transports remain guarded by `target_admin` and the Target privilege policy.
- Go systemd services now reuse the stricter appliance sandbox baseline (`ProtectSystem=strict`, `ProtectHome=read-only`, bounded writable paths and socket families) while Admin retains only the capability required to bind its privileged port.

### Reliability / evidence
- Deployment provenance is now live evidence: the recorded commit, Go-only runtime and adapter SHA-256 must match the installed runtime before `verified=true` is reported.
- The deployment fast path now verifies the live adapter hash, runtime marker, positive postboot state, Admin `/login` and MCP `/ready` before reporting an already-deployed success.
- Doctor now gives required failures precedence over warning severity, positively checks Admin readiness and postboot state, and keeps storage/memory thresholds advisory on constrained appliances.
- Cloudflare diagnostics now describe the locally computed SHA-256 as a fingerprint rather than claiming cryptographic provenance without a trusted external hash.

### Admin / operations
- Projects can manage typed allowlisted Tasks directly in the existing Admin Project screen with audited add/update/delete operations.
- Fresh non-interactive installs can complete first Admin setup through a bounded one-time `/setup` flow backed by a private 0600 token that expires after 15 minutes and is deleted after success; local CLI setup remains the recovery path.
- Dashboard gateway state now uses the same required fail-closed setting read as runtime policy.
- Admin bind/port defaults have one source in Go; systemd and installer retain only explicit appliance overrides.
- Active source documentation no longer embeds mutable stable/candidate status; immutable Git tags/Releases and live deployment evidence are authoritative for publication/runtime state.

### Compatibility
- Tool catalog version is **5** because the `run_command` input contract now requires `project`.
- Registry schema remains **6**, Core API remains **1**, Bridge API remains **1**, and the MCP protocol remains **2026-07-28**.

### Validation
- `v1.6.6` points to `2060a4910310c4d85f70e9295afac2e04522d2df`; exact-SHA CI passed on `develop` and `main`, and the ARMv6 appliance accepted that same SHA with verified adapter hash, required readiness, postboot and systemd sandbox evidence.

## 1.6.5

### Fixed
- Post-boot Doctor now observes optional ingress services after their startup order instead of racing them: the installer starts the optional OpenAI tunnel before postboot verification, and the postboot unit orders itself after Gemini, Cloudflare and the tunnel without making any of them hard dependencies.
- This removes the transient `Optional Tunnel: enabled but not active` warning seen during successful deployments while preserving optional-service semantics and fail-closed readiness for required MCP services.

### Validation
- `v1.6.5` was published at commit `878701e23a6626f3a3e40c223e7adf1bd502e902`; `main` and `develop` converged on that exact SHA.
- The ARMv6 appliance was subsequently observed running Gateway 1.6.5 with its Registry/services/endpoints available; 1.6.6 supersedes it only after repeating exact-SHA CI, canonical deployment and live acceptance.

## 1.6.4

### Fixed
- Post-boot verification now gates on the real MCP `/ready` endpoint using bounded native `curl` retries instead of a fixed five-second delay. This removes the ARMv6 startup race where the service process was active but not yet responsive enough for Doctor.
- The readiness gate is bounded per attempt and overall, retries connection refusal/timeouts, and still lets Doctor perform the final fail-closed health decision.

### Validation
- `v1.6.3` remains immutable. Its production deployment failed at post-boot readiness and was automatically rolled back to the accepted 1.6.2 runtime.
- Exact-SHA CI, reproducible ARMv6 bundle validation, canonical deployment and live post-boot acceptance passed; 1.6.4 became the accepted production release.

## 1.6.3 — tagged; production acceptance failed

### Fixed
- Termux/Shizuku privilege discovery no longer lets a slow `rish` readiness probe consume the entire Target facts timeout. Presence of `rish` still marks the shell as independently elevable and therefore still requires explicit `target_admin`; readiness/UID verification is bounded separately and fails closed for privileged execution.
- Cloudflare dependency provenance no longer executes the large external `cloudflared` binary merely to collect a version string. Doctor now relies on the stronger SHA-256/file/token evidence already present, while service/tunnel health remains validated by their existing runtime checks.

### Validation
- 1.6.2 remains immutable and is the currently deployed stable release.
- 1.6.3 must repeat exact-SHA CI, ARMv6 bundle validation, canonical deployment and live acceptance before promotion.

## 1.6.2

### Fixed
- Carries forward the 1.6.1 authorization-scope correction for safe observability and Target-only status checks.
- Installer pre-activation backup validation now derives accepted Registry schemas from manifest.json registry_upgrade_from instead of a stale hard-coded 4/5 regex. This restores safe repeat upgrades when the live Registry is already schema 6.
- Added an operational contract regression that rejects a frozen historical backup-schema allowlist.

### Validation
- v1.6.1 remains immutable as a failed deployment candidate. Its deployment stopped fail-closed before service shutdown or runtime replacement, so production remained on 1.6.0.
- 1.6.2 must repeat exact-SHA CI, ARMv6 preflight, canonical install and live client authorization acceptance.

## 1.6.1 — failed deployment candidate

### Fixed
- Authorization scope evaluation now follows each tool's real request dimensions: safe gateway observability (health, list_targets, gateway_status, gateway_doctor) remains usable from an existing scoped capability grant, and target_status evaluates Target scope without inventing a Project requirement.
- Sensitive global operations (gateway_backup, gateway_maintenance, gateway_reboot) continue to require a genuinely global grant scope.
- Added regression coverage for the production-discovered case where a scoped wildcard grant advertised safe observability in the catalog but invocation was denied after the 1.6.0 scope hardening.

### Validation
- Exact-SHA CI passed and v1.6.1 was published immutably, but deployment stopped before activation because the installer backup check accepted only schemas 4/5 while the live Registry was already schema 6.
- No service shutdown, runtime replacement or Registry mutation occurred during the failed 1.6.1 activation attempt.

## 1.6.0

### Security
- The critical gateway_enabled kill-switch setting now fails closed when its required Registry row is missing instead of inheriting the fresh-install default at runtime.
- Effective Access for run_command reuses the live Target privilege gate, including explicit target_admin, Target policy, approval/boot identity and backend readiness, without consuming diagnostic approvals.
- Registry schema 6 enforces one capability per Grant and logical uniqueness; migration splits historical comma-separated bundles and safely consolidates exact duplicates.
- Legacy sudoers reboot policy and its obsolete helper are removed from source/release paths; appliance reboot remains delegated through systemd-logind + polkit.

### Fixed
- Registry recovery/service inventory includes all Registry-using services needed to keep restore fail-closed.
- Admin validation preserves submitted form state on recoverable errors and returns validation responses instead of redirecting away from unsaved input.
- Client Grant Target/Project selectors are scope-aware, preserve Effective Access selections, and reject duplicate Grants before SQLite reports a constraint error.
- Target creation now leads directly to the existing security/setup controls on Edit Target.
- Remote file reads reject non-regular files instead of allowing FIFO/device reads to block until timeout.
- Dashboard readiness uses in-process Core health instead of a synchronous self-HTTP probe.

### Changed
- Registry schema advances from 5 to 6. Explicit migration supports 4 -> 5 -> 6 and 5 -> 6; restore preserves supported backup schemas 4, 5 and 6 exactly.
- CI now executes the exact shell syntax gates required by the repository contract.
- Host-specific smoke fixtures and retired reboot artifacts are removed from the tracked product tree.

### Verification
- Promotion remains blocked until the complete local gates, ARMv6 package inspection, exact pushed-SHA CI, immutable tag creation and later production acceptance all pass.

## 1.5.0

### Security
- Security-sensitive Admin mutations now record their successful Activity entry in the same SQLite transaction as the authorization/state change; audit failure rolls the mutation back.
- Target/Project/Client changes revoke cached privilege approvals transactionally, and one-use `ask_always` approval consumption fails closed if deletion or commit fails.
- Grant edit/toggle/delete routes are scoped by both Client ID and Grant ID, and externally configured MCP transports cannot claim the reserved internal principals `local`, `admin`, `system` or `test`.
- Scheduled maintenance and post-boot units inherit the existing systemd hardening baseline.

### Fixed
- Project READ/WRITE indicators now render the persisted `read_enabled` / `write_enabled` values and act as the single controls for those permissions; the redundant Allow/Revoke Write action is removed.
- Client Grants now use one explicit capability per new Grant, have a functional canonical Edit route, preserve legacy comma-separated bundles only for deliberate migration, and no longer default a missing capability to wildcard `*`.
- Target form validation rejects missing host/user, invalid ports, unsupported platforms and invalid privilege policies instead of silently normalizing dangerous input.
- Target privilege administration now renders the live Core privilege status contract, only offers scopes that pass both ordinary shell authorization and explicit `target_admin`, and persists `ask_once_per_boot` approvals with the observed Target boot ID instead of hardcoding `ask_always`.
- Admin Registry reads that are required to render Targets/Projects/Clients/Grants fail closed instead of presenting empty healthy-looking state.
- Settings now show the effective configured Admin time zone explicitly.

### Changed
- Release guidance requires successful CI for the exact pushed SHA before an immutable version tag is created.


## 1.5.0-rc.4

### Fixed
- Admin Console text fallbacks no longer use boolean `or` expressions in Pongo2 output tags, which rendered literal `True` instead of display names, provider/privilege values, grant scopes and other fallback text.
- Added render regression coverage for Targets, Projects, AI Clients and Client Grants so human-readable values are asserted and boolean `True` cannot silently replace presentation text.

### Compatibility
- Core API, Bridge API, Tool Catalog, MCP protocol and Registry schema are unchanged from rc.3.


## 1.5.0-rc.3

### Fixed
- Doctor now distinguishes a real MCP-Pi systemd installation from a generic Linux/systemd host, so CI and development Linux hosts no longer fail appliance-only service/readiness checks.
- The rc.2 tag is preserved as an immutable failed-CI candidate; rc.3 carries the corrected environment detection.

### Compatibility
- Core API, Bridge API, Tool Catalog, MCP protocol and Registry schema are unchanged from rc.2.

## 1.5.0-rc.2

### Security
- `run_task` now reuses the same effective Target privilege gate as `run_command`; allowlisted tasks cannot execute through an already root/Administrator transport without explicit `target_admin` authorization and the configured privilege policy/approval.
- Privilege probing for task execution fails closed when the effective level cannot be established.

### Fixed
- Activity Audit LIVE now has functional enable/pause URLs, preserves active filters, and activates the existing HTMX three-second polling path.
- Activity result filters normalize PASS/DENY consistently instead of silently missing lowercase form values.
- Nested numeric values rendered by the Admin Console preserve integer semantics instead of becoming `.000000` floats.
- Maintenance & Diagnostics now consumes real Doctor/build metadata, invokes the existing maintenance contract correctly, and no longer exposes a non-existent web rollback path.
- Dashboard wording distinguishes enabled configuration from reachability and uses MCP `/ready` rather than a raw TCP listener check.
- Registry `status` is non-migrating and refuses incompatible schemas instead of changing them as a side effect.
- Registry `restore` preserves the backup schema exactly; migration is now a distinct explicit lifecycle action.
- Setup no longer has a successful path that can leave the appliance without an enabled Admin user.
- Doctor no longer turns missing status into HEALTHY and now checks deployed systemd services plus MCP readiness when running on the appliance.
- Maintenance fails closed when the backup directory cannot be enumerated.

### Added
- Admin Console display time zone setting using IANA identifiers. Audit storage remains UTC; Activity timestamps and date filters are converted at the presentation boundary.
- Explicit `mcp-gateway migrate` command for fresh Registry creation and supported schema migration.
- Focused regression coverage for privilege boundaries, Activity LIVE/time-zone/filtering, numeric presentation, exact-schema restore and non-migrating status.
- Release-bundle installation verifies `SHA256SUMS` and checks candidate binary metadata against manifest/compatibility contracts before mutation.

### Changed
- Admin tables prioritize display names while retaining stable Target/Project/Client keys as secondary operational metadata; internal Grant IDs are no longer a primary table column.
- `install.sh` is now the single activation/update/rollback engine; exact-commit deployment delegates lifecycle mutation to the candidate installer and retains only Git/provenance/transport/acceptance responsibilities.
- Admin/MCP systemd units use `Type=exec`; postboot and maintenance log through journald; tunnel startup waits for MCP `/ready`.
- Removed the redundant standalone ARMv6 build helper and unsupported appliance-wide secret backup helper. Canonical release build plus Registry backup/restore remain.
- Removed the misleading `repair` command; `maintenance` is the single safe maintenance operation.
- Consolidated current documentation around README + installation/configuration/operations/recovery/troubleshooting; removed duplicate getting-started/update-rollback/lifecycle/adapter/diagram pages and archived deployment-specific client/tunnel/roadmap snapshots.
- Integration work is documented on `develop`; the retired `go-only-migration` branch is no longer part of active CI.

### Compatibility
- Gateway candidate advances from 1.5.0-rc.1 to 1.5.0-rc.2.
- Core API, Bridge API, Tool Catalog, MCP protocol and Registry schema remain unchanged.

## 1.4.0 — 2026-09-24

### Added
- Target administrative privilege management in the Admin Console with explicit `target_admin` Grants, per-Target `never` / `ask_always` / `ask_once_per_boot` / `always_allow` policies, approval/revocation controls and hardened confirmation for `always_allow`.
- Verified Android/Termux Shizuku (`rish`) elevation and optional Linux/Windows `privilege_user` support using a second SSH identity on the same pinned Target and Gateway key.
- Privilege status in Target administration, including observed effective identity, backend readiness and independent-elevator warnings.

### Security
- `target_admin` is independent from Gateway `admin` and is never inherited by wildcard `*` Grants. Missing Grant, policy, approval, backend or boot identity fails closed.
- Already elevated root/Administrator transports cannot bypass Target privilege policy with `privilege=standard`. Arbitrary shells that expose a detectable independent elevator such as `rish`, Windows `sudo` or non-interactive Linux `sudo` also cross `target_admin` + Target policy before execution; commands are not secured by brittle string filtering.
- Temporary approvals are scoped to client + Target + Project, one-use approvals are short-lived, and authorization-context changes revoke affected approvals.

### Compatibility
- Tool Catalog advances from v3 to v4 while retaining the same 21 tool names; `run_command` adds the explicit `privilege=standard|required` input.
- Registry schema advances from v1 in production to v4 with supported v1/v2/v3 migrations. Existing Targets migrate to `privilege_policy=never`, and `privilege_user` defaults empty.
- The Go adapter now reports the Gateway version obtained from the Core compatibility contract instead of carrying a separate hard-coded adapter release number.

### Verification
- Promotion requires the full Python/Go/frontend/Tailwind/documentation gates, native transport E2E, ARMv6 adapter build, deterministic release packaging, CI, exact-commit deployment and live Admin/MCP/Doctor/Target/provenance acceptance.

## 1.3.6 — 2026-09-23

### Fixed
- Windows Targets now use their native path semantics for Project roots, relative paths and trusted-shell working directories instead of interpreting them with the Gateway host's POSIX path rules.
- Internal remote Python helpers no longer depend on POSIX shell quoting, redirection or the external `realpath` command, restoring structured read/write and Git operations through the normal SSH transport on Windows.
- Windows trusted-shell `cwd` and environment setup now reuse the existing target-side Python runtime instead of emitting POSIX `export`/`cd` prefixes.

### Changed
- Removed the unused `posix-python` write-adapter gate; the existing Python helper path is now the single cross-platform mechanism.
- CI now includes focused Windows transport coverage, including a real `cmd.exe` helper smoke test, without duplicating the full Linux/ARM release job.

### Compatibility
- No Core API, Bridge API, Tool Catalog, Registry schema, MCP protocol or grant-policy changes.
- Target project helpers continue to require `python3`; Windows Targets do not require WSL or Unix compatibility utilities.

### Verification
- Promotion requires the normal full local/CI gates plus live acceptance against the configured Windows Target before production is considered verified.

## 1.3.5 — 2026-09-23

### Fixed
- Target SSH connections now always use the Target Registry username instead of silently inheriting the Gateway service account from an undefined or mismatched SSH alias.
- Target SSH connections now explicitly use the Gateway target identity key, IdentitiesOnly=yes and StrictHostKeyChecking=yes, so the public key displayed in **Edit Target** is the same identity actually offered during authentication.
- SSH aliases are now optional advanced configuration only; they can contribute non-authoritative options such as ProxyJump but cannot override Target host, port, username, Gateway identity or pinned host identity.

### Verification
- Added regression coverage for both configured-alias and no-alias Targets, including Windows-style Target usernames.
- Promotion requires full local/CI validation and exact-commit deployment before final Target authentication acceptance.

## 1.3.4 — 2026-09-23

### Added
- SSH host identity management in **Targets → Edit Target**, reusing the existing pinned-identity discovery layer and OpenSSH known_hosts.
- Explicit review flows for first trust, changed-key replacement and trust removal, plus visibility of the Gateway public key for Target authorization.
- Fail-closed tests covering reviewed-fingerprint pinning, changed-key replacement, unrelated Target preservation and Admin UI trust operations.

### Changed
- Target edit now shows the currently presented and pinned SHA256 host fingerprints and provides an in-place connection check.
- Host trust updates re-scan the endpoint and require the exact fingerprint the administrator reviewed before mutating known_hosts.

### Compatibility
- No Registry schema, Tool Catalog, MCP protocol or grant-policy changes.
- Existing pinned Target entries remain authoritative; no trust is migrated or accepted automatically.

### Verification
- 1.3.4 promotion requires the full local gate, immutable v1.3.4 tag, exact-commit deployment and production Doctor/Target acceptance on the same SHA.

## 1.3.3 — 2026-09-16

### Added
- Admin Console Grant management under **AI Clients → Grants** using the existing Registry CRUD and authorization model.
- Real-policy **Check Effective Access** backed directly by `authorize_client()`.
- Validation, CSRF-protected mutations, audit events, cross-client ownership checks and explicit confirmation for global `* / * / *` grants.

### Compatibility
- No Registry schema, Tool Catalog, MCP protocol or authorization-semantics changes.
- Direct SQLite editing is no longer required for normal Grant administration.

### Verification
- 1.3.3 promotion requires the full local gate, CI on `develop` and `main`, deterministic ARMv6 packaging from the immutable tag, and post-release production deployment/Doctor acceptance on the same SHA.

## 1.3.2 — 2026-09-15

### Changed
- Self-restarting maintainer deployments must run through the resumable runner; direct `deploy-pi.sh` execution fails closed unless the explicit break-glass override is set.
- Deployment/rollback logs now mark expected control-plane interruption and confirmed restoration.
- Reconnect guidance and documentation audit now enforce evidence-first recovery and the 30-second interactive timeout contract.

### Verification
- 1.3.2 promotion requires the full local gate, CI on `develop` and `main`, deterministic ARMv6 packaging from the immutable tag, and post-release production deployment/Doctor acceptance on the same SHA.

## 1.3.1 — 2026-09-15

### Changed
- Standardized project and repository branding as `MCP-Pi`, including canonical GitHub URLs and release bundle filename prefix.
- Release metadata now reports Gateway version 1.3.1 while preserving the 1.3.0 historical release unchanged.

### Compatibility
- No Core API, Bridge API, Tool Catalog, Registry schema, MCP protocol, security-policy, or data-format break.
- Existing `LOCAL_MINIMCP_STATE_DIR` / `~/.local/state/local-minimcp/` identifiers remain supported for compatibility and are intentionally not renamed.

## 1.3.0 — 2026-09-15

### Added
- Resumable long-job runner using `nohup` + `setsid` + `flock`, durable non-secret state, explicit status/log/cleanup, optional short-lived Termux wake lock, and no automatic retries after interrupted control sessions.

- trusted Target-shell capability with independent `shell_enabled` kill switch and readable effective-capability UI;
- Target Environment Facts, deterministic MCP catalog diagnostics and standard Tool Annotations;
- CI and semantic documentation audit;
- exact-commit maintainer deployment with verified `.deployment.json` provenance and transactional rollback;
- optional fail-closed `age` encryption for private off-device backups;
- beginner-facing release-package builder with prebuilt ARMv6 adapter;
- source/release-tree installer preflight (`install.sh --check`) and installer-managed rollback;
- minimal real Admin bootstrap through `mcp-gateway setup`.

### Changed
- Project/repository branding, canonical GitHub URL and release-package filename prefix are standardized as `MCP-Pi`.

- Exact-commit deployment now exits safely as `ALREADY_DEPLOYED` when the same verified SHA is already healthy; `MCP_DEPLOY_FORCE=1` is the explicit redeploy override.

- structured filesystem mutations share remote canonical destination resolution and reject symlink-parent escapes;
- critical mutations require audit availability before execution;
- `run_task` validates enabled/allowlisted argv/cwd/timeout;
- `run_command` is explicitly a trusted Target shell, not a Project filesystem sandbox;
- systemd hardening was strengthened without arbitrary memory limits;
- Admin systemd defaults are generic/loopback-safe; machine-specific trusted-LAN binding lives in private `admin.env`;
- fresh Registry defaults keep both structured writes and trusted Target shell disabled;
- `install.sh` now installs from the directory containing the release/source, preserves persistent state, stages application updates, verifies readiness/Doctor and provides rollback;
- `mcp-gateway update/rollback` no longer present the historical symlink lifecycle as the production update path;
- active documentation is consolidated into a small CURRENT set; historical evidence is isolated from operating guidance;
- only `scripts/deploy-pi.sh` remains an active production deploy entrypoint.

### Fixed

- transactional maintainer rollback reads protected systemd unit backups with required privilege and propagates remote rollback failures instead of emitting false success;
- stale documentation assumptions about old IPs, tool counts, release baselines and setup behavior are removed from CURRENT guidance;
- installer no longer assumes application files are already copied into `/home/mcp-gateway/mcp-gateway`.

### Verification

1.3.0 promotion requires Python, Go, ARMv6 build, JavaScript, Tailwind, documentation, release-package, installer preflight, CI, exact-commit production deployment and live acceptance gates to pass on the same final commit.

## 1.2.1 — September 2026

### Changed
- Integrated the secure MCP tunnel with deterministic backend readiness and automatic recovery when the local MCP endpoint becomes available late.
- Separated permanent product/credential gates from transient backend startup failure so systemd can retry the latter without weakening fail-closed behavior.

## 1.2.0 — September 2026

### Added
- Added dynamic Target endpoint rediscovery while keeping Target ID plus pinned SSH host key as immutable identity and host/port as mutable endpoint data.
- Distinguished stale/DHCP-reused endpoints from authentication failures and required an exact pinned-key match before updating the Registry.

## 1.1.1 — September 2026

### Fixed
- Corrected secure-tunnel header wiring and introduced the dedicated gateway-auth header while preserving anti-spoofing and constant-time token verification.
- Verified private token/config ownership and restrictive permissions on the appliance.

## 1.1.0 — September 2026

### Added
- Added authenticated external MCP ingress, client-ID anti-spoofing, appliance status/Doctor/backup/maintenance/reboot operations and the initial secure tunnel integration.
- Introduced conservative appliance security-update reporting and explicit post-boot/health acceptance.

## 1.0.1 — September 2026

### Changed
- Maintenance release aligning metadata/documentation and replacing environment-specific example endpoints while preserving pinned Target identity semantics.
