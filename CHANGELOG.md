# Changelog

All notable user-visible changes are documented here. Historical release details remain under `docs/releases/`.

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

- Exact-commit deployment now exits safely as `ALREADY_DEPLOYED` when the same verified SHA is already healthy; `MCP_DEPLOY_FORCE=1` is the explicit repair override.

- structured filesystem mutations share remote canonical destination resolution and reject symlink-parent escapes;
- critical mutations require audit availability before execution;
- `run_task` validates enabled/allowlisted argv/cwd/timeout;
- `run_command` is explicitly a trusted Target shell, not a Project filesystem sandbox;
- systemd hardening was strengthened without arbitrary memory limits;
- Admin systemd defaults are generic/loopback-safe; machine-specific trusted-LAN binding lives in private `admin.env`;
- fresh Registry defaults keep both structured writes and trusted Target shell disabled;
- `install.sh` now installs from the directory containing the release/source, preserves persistent state, stages application updates, verifies readiness/Doctor and provides rollback;
- `mcp-gateway update/rollback` no longer present the historical symlink lifecycle as the production update path;
- active documentation is consolidated into a small CURRENT set; technical detail and historical evidence are separated into `reference/` and `archive/`;
- only `scripts/deploy-pi.sh` remains an active production deploy entrypoint.

### Fixed

- transactional maintainer rollback reads protected systemd unit backups with required privilege and propagates remote rollback failures instead of emitting false success;
- stale documentation assumptions about old IPs, tool counts, release baselines and setup behavior are removed from CURRENT guidance;
- installer no longer assumes application files are already copied into `/home/mcp-gateway/mcp-gateway`.

### Verification

1.3.0 promotion requires Python, Go, ARMv6 build, JavaScript, Tailwind, documentation, release-package, installer preflight, CI, exact-commit production deployment and live acceptance gates to pass on the same final commit.
