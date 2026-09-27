#!/data/data/com.termux/files/usr/bin/env bash
set -Eeuo pipefail

# Exact-commit, Go-only, control-plane-safe deployment for MCP-Pi.
# Usage: scripts/deploy-pi.sh <exact-git-sha>
# Optional rollback test: MCP_DEPLOY_INJECT_FAILURE=after-activation ...

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
TMP_BASE="${TMPDIR:-${PREFIX:-/data/data/com.termux/files/usr}/tmp}"
REMOTE_TARGET_DIR=/home/mcp-gateway/mcp-gateway
REMOTE_DATA_DIR=/home/mcp-gateway/.local/share/mcp-gateway

fail(){ echo "ERROR: $*" >&2; exit 1; }

[ "$#" -eq 1 ] || fail "usage: $0 <exact-git-sha>"
if [ -z "${MCP_PI_RESUMABLE_JOB_ID:-}" ] && [ "${MCP_DEPLOY_ALLOW_DIRECT:-0}" != 1 ]; then
    fail "deploy-pi.sh must run via scripts/run-resumable.sh; set MCP_DEPLOY_ALLOW_DIRECT=1 only for explicit break-glass recovery"
fi

for cmd in git go tar gzip sha256sum file strings ssh scp grep awk find; do
    command -v "$cmd" >/dev/null 2>&1 || fail "$cmd is required for deployment"
done

DEPLOY_SHA="$(git -C "$PROJECT_ROOT" rev-parse "$1^{commit}")"
HEAD_SHA="$(git -C "$PROJECT_ROOT" rev-parse HEAD)"
DEPLOY_BRANCH="$(git -C "$PROJECT_ROOT" branch --show-current)"
[ "$DEPLOY_SHA" = "$HEAD_SHA" ] || fail "requested commit must equal local HEAD ($HEAD_SHA)"
[ -n "$DEPLOY_BRANCH" ] || fail "deployment requires a named Git branch"
printf '%s' "$DEPLOY_BRANCH" | grep -Eq '^[A-Za-z0-9._/-]+$' || fail "deployment branch contains characters unsafe for provenance"
[ -z "$(git -C "$PROJECT_ROOT" status --porcelain --untracked-files=normal)" ] || fail "deployment requires a completely clean Git worktree"

git -C "$PROJECT_ROOT" fetch origin "$DEPLOY_BRANCH" --quiet
REMOTE_BRANCH_SHA="$(git -C "$PROJECT_ROOT" rev-parse "origin/$DEPLOY_BRANCH")"
[ "$REMOTE_BRANCH_SHA" = "$DEPLOY_SHA" ] || fail "origin/$DEPLOY_BRANCH ($REMOTE_BRANCH_SHA) does not equal requested commit ($DEPLOY_SHA)"

if [ -f "$PROJECT_ROOT/.mcp-pi.local.env" ]; then
    set -a
    source "$PROJECT_ROOT/.mcp-pi.local.env"
    set +a
fi

PI_HOST="${MCP_PI_HOST:-192.168.68.55}"
PI_USER="${MCP_PI_USER:-yorologo}"
PI_IDENTITY_FILE="${MCP_PI_IDENTITY_FILE:-$HOME/.ssh/id_rsa}"
[ -r "$PI_IDENTITY_FILE" ] || fail "SSH identity not readable: $PI_IDENTITY_FILE"

SSH_OPTS=(-i "$PI_IDENTITY_FILE" -o BatchMode=yes -o StrictHostKeyChecking=yes)
ssh_pi(){ ssh "${SSH_OPTS[@]}" "$PI_USER@$PI_HOST" "$@"; }
scp_pi(){ scp -O "${SSH_OPTS[@]}" "$@"; }

already_deployed() {
    ssh_pi bash -s -- "$REMOTE_TARGET_DIR" "$DEPLOY_SHA" <<'REMOTE'
set -euo pipefail
current="$1"
expected="$2"
test "$(cat "$current/.deployed-git-sha" 2>/dev/null || true)" = "$expected"
grep -Fq "\"commit\": \"$expected\"" "$current/.deployment.json"
grep -Fq '"verified": true' "$current/.deployment.json"
systemctl is-active --quiet mcp-gateway-admin
systemctl is-active --quiet mcp-gateway-mcp
curl -fsS http://127.0.0.1/login >/dev/null
curl -fsS http://127.0.0.1:8090/ready >/dev/null
if systemctl is-enabled --quiet mcp-gateway-tunnel 2>/dev/null; then
    systemctl is-active --quiet mcp-gateway-tunnel
fi
REMOTE
}

if [ "${MCP_DEPLOY_FORCE:-0}" != 1 ] && [ -z "${MCP_DEPLOY_INJECT_FAILURE:-}" ]; then
    if already_deployed >/dev/null 2>&1; then
        echo "ALREADY_DEPLOYED commit=$DEPLOY_SHA"
        echo "DEPLOYMENT_VERIFIED already_deployed=true"
        exit 0
    fi
fi

SHORT_SHA="$(printf '%s' "$DEPLOY_SHA" | cut -c1-12)"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$TMP_BASE"
STAGE_DIR="$(mktemp -d "$TMP_BASE/mcp-pi-deploy.XXXXXX")"
DIST_DIR="$STAGE_DIR/dist"
EXTRACT_DIR="$STAGE_DIR/extract"
DEPLOYMENT_STAGE="$STAGE_DIR/.deployment.json"
mkdir -p "$DIST_DIR" "$EXTRACT_DIR"

REMOTE_UPLOAD_DIR="/tmp/mcp-gateway-deploy-$SHORT_SHA-$STAMP"
REMOTE_CANDIDATE="$REMOTE_TARGET_DIR.candidate-$SHORT_SHA-$STAMP"
REMOTE_PREVIOUS="$REMOTE_TARGET_DIR.previous-$STAMP"
REMOTE_FAILED="$REMOTE_TARGET_DIR.failed-$STAMP"
REMOTE_UNIT_BACKUP="$REMOTE_DATA_DIR/deploy-unit-backup-$STAMP"
REMOTE_REGISTRY_BACKUP="$REMOTE_DATA_DIR/backups/deploy-pre-$SHORT_SHA-$STAMP.db"
OLD_DEPLOY_SHA=""
ACTIVATED=0
TUNNEL_WAS_ENABLED=0

cleanup_local(){ rm -rf "$STAGE_DIR"; }
trap cleanup_local EXIT

rollback() {
    echo "ROLLBACK: restoring previous runtime, Registry and system assets..." >&2
    echo "CONTROL_PLANE_RESTART=EXPECTED reason=rollback" >&2
    local rc=0
    ssh_pi bash -s -- "$REMOTE_TARGET_DIR" "$REMOTE_PREVIOUS" "$REMOTE_FAILED" "$REMOTE_UNIT_BACKUP" "$REMOTE_REGISTRY_BACKUP" "$OLD_DEPLOY_SHA" "$TUNNEL_WAS_ENABLED" <<'REMOTE' || rc=$?
set -euo pipefail
current="$1"; previous="$2"; failed="$3"; unit_backup="$4"; registry_backup="$5"; old_sha="$6"; tunnel_enabled="$7"
db=/home/mcp-gateway/.local/share/mcp-gateway/gateway.db

sudo systemctl stop mcp-gateway-tunnel mcp-gateway-postboot mcp-gateway-mcp mcp-gateway-admin 2>/dev/null || true
if [ -d "$previous" ]; then
    sudo test -s "$registry_backup" || { echo "ROLLBACK ERROR: Registry backup missing" >&2; exit 86; }
    if [ -x "$current/bin/mcp-gateway-adapter" ]; then
        sudo -u mcp-gateway "$current/bin/mcp-gateway-adapter" restore -db "$db" "$registry_backup" >/dev/null
    elif [ -d "$current" ]; then
        echo "ROLLBACK ERROR: active candidate Go binary missing" >&2
        exit 87
    fi
    sudo rm -rf "$failed"
    if [ -d "$current" ]; then sudo mv "$current" "$failed"; fi
    sudo mv "$previous" "$current"
else
    [ -d "$current" ] || { echo "ROLLBACK ERROR: neither current nor previous runtime exists" >&2; exit 80; }
fi

for unit in mcp-gateway-admin.service mcp-gateway-mcp.service mcp-gateway-tunnel.service mcp-gateway-maintenance.service mcp-gateway-maintenance.timer mcp-gateway-postboot.service; do
    if sudo test -f "$unit_backup/$unit"; then
        sudo install -o root -g root -m 0644 "$unit_backup/$unit" "/etc/systemd/system/$unit"
    elif sudo test -f "$unit_backup/$unit.absent"; then
        sudo systemctl disable "$unit" >/dev/null 2>&1 || true
        sudo rm -f "/etc/systemd/system/$unit"
    fi
done
if sudo test -d /etc/polkit-1/rules.d; then
    if sudo test -f "$unit_backup/49-mcp-gateway-reboot.rules"; then
        sudo install -o root -g root -m 0644 "$unit_backup/49-mcp-gateway-reboot.rules" /etc/polkit-1/rules.d/49-mcp-gateway-reboot.rules
    elif sudo test -f "$unit_backup/49-mcp-gateway-reboot.rules.absent"; then
        sudo rm -f /etc/polkit-1/rules.d/49-mcp-gateway-reboot.rules
    fi
fi
if sudo test -f "$unit_backup/mcp-gateway-tunnel-check"; then
    sudo install -o root -g root -m 0755 "$unit_backup/mcp-gateway-tunnel-check" /usr/local/bin/mcp-gateway-tunnel-check
fi

sudo systemctl daemon-reload
sudo systemctl reset-failed mcp-gateway-admin mcp-gateway-mcp mcp-gateway-postboot mcp-gateway-tunnel 2>/dev/null || true
sudo systemctl restart mcp-gateway-admin
for i in $(seq 1 45); do curl -fsS http://127.0.0.1/login >/dev/null 2>&1 && break; [ "$i" -lt 45 ] || exit 82; sleep 1; done
sudo systemctl restart mcp-gateway-mcp
for i in $(seq 1 60); do curl -fsS http://127.0.0.1:8090/ready >/dev/null 2>&1 && break; [ "$i" -lt 60 ] || exit 83; sleep 1; done
sudo systemctl start mcp-gateway-maintenance.timer 2>/dev/null || true
sudo systemctl restart mcp-gateway-postboot.service 2>/dev/null || true
if [ "$tunnel_enabled" = 1 ]; then
    sudo systemctl restart mcp-gateway-tunnel
    for i in $(seq 1 60); do systemctl is-active --quiet mcp-gateway-tunnel && break; [ "$i" -lt 60 ] || exit 84; sleep 1; done
else
    sudo systemctl stop mcp-gateway-tunnel 2>/dev/null || true
fi
[ -z "$old_sha" ] || test "$(cat "$current/.deployed-git-sha" 2>/dev/null || true)" = "$old_sha"
sudo -u mcp-gateway "$current/bin/mcp-gateway" doctor -db "$db" >/dev/null
sudo rm -rf "$failed" "$unit_backup"
sudo rm -f "$registry_backup"
REMOTE
    [ "$rc" -eq 0 ] || { echo "ROLLBACK_REMOTE_FAILED rc=$rc" >&2; return "$rc"; }
    ACTIVATED=0
    echo "CONTROL_PLANE_RESTORED reason=rollback"
    echo "ROLLBACK_VERIFIED old_sha=${OLD_DEPLOY_SHA:-unknown}"
}

cleanup_remote_transfer() {
    ssh_pi "sudo rm -rf '$REMOTE_UPLOAD_DIR' '$REMOTE_CANDIDATE'" >/dev/null 2>&1 || true
}

on_error() {
    local rc=$?
    trap - ERR
    echo "DEPLOYMENT_FAILED rc=$rc" >&2
    if [ "$ACTIVATED" -eq 1 ]; then
        rollback || { echo "ROLLBACK_FAILED: manual recovery required; previous=$REMOTE_PREVIOUS" >&2; cleanup_remote_transfer; exit 90; }
    else
        ssh_pi "sudo rm -rf '$REMOTE_UNIT_BACKUP'; sudo rm -f '$REMOTE_REGISTRY_BACKUP'" >/dev/null 2>&1 || true
    fi
    cleanup_remote_transfer
    exit "$rc"
}
trap on_error ERR

echo "=== MCP-Pi exact-commit Go-only deployment ==="
echo "branch=$DEPLOY_BRANCH"
echo "commit=$DEPLOY_SHA"

echo "[1/10] Building canonical Go-only release bundle..."
"$PROJECT_ROOT/scripts/build-release-package.sh" "$DEPLOY_SHA" "$DIST_DIR" >/dev/null
PACKAGE="$(find "$DIST_DIR" -maxdepth 1 -type f -name 'MCP-Pi-*-linux-armv6-*.tar.gz' -print -quit)"
[ -n "$PACKAGE" ] || fail "release builder produced no ARMv6 package"
PACKAGE_SHA="$(sha256sum "$PACKAGE" | awk '{print $1}')"
tar -xzf "$PACKAGE" -C "$EXTRACT_DIR"
PKG_ROOT="$(find "$EXTRACT_DIR" -mindepth 1 -maxdepth 1 -type d -name 'MCP-Pi-*' -print -quit)"
[ -n "$PKG_ROOT" ] || fail "release package has no package root"
ADAPTER="$PKG_ROOT/bin/mcp-gateway-adapter"
file "$ADAPTER" | grep -Eq 'ELF 32-bit.*ARM' || fail "release adapter is not Linux ARMv6-compatible"
LOCAL_ADAPTER_SHA="$(sha256sum "$ADAPTER" | awk '{print $1}')"
if find "$PKG_ROOT" -type f \( -name '*.py' -o -name requirements.txt \) -print -quit | grep -q .; then fail "release package contains Python payload"; fi
echo "package_sha256=$PACKAGE_SHA"
echo "adapter_sha256=$LOCAL_ADAPTER_SHA"

echo "[2/10] Remote preflight..."
ssh_pi "test -d '$REMOTE_TARGET_DIR'; command -v sudo >/dev/null; command -v systemctl >/dev/null; command -v curl >/dev/null; command -v tar >/dev/null; command -v sha256sum >/dev/null; command -v systemd-analyze >/dev/null"
OLD_DEPLOY_SHA="$(ssh_pi "cat '$REMOTE_TARGET_DIR/.deployed-git-sha' 2>/dev/null || true")"
if ssh_pi "systemctl is-active --quiet mcp-gateway-tunnel" >/dev/null 2>&1; then TUNNEL_WAS_ENABLED=1; fi

echo "[3/10] Transferring immutable bundle..."
ssh_pi "mkdir -p '$REMOTE_UPLOAD_DIR' && chmod 700 '$REMOTE_UPLOAD_DIR'"
scp_pi "$PACKAGE" "$PI_USER@$PI_HOST:$REMOTE_UPLOAD_DIR/release.tar.gz"

echo "[4/10] Preparing and validating candidate on ARMv6..."
ssh_pi bash -s -- "$REMOTE_CANDIDATE" "$REMOTE_UPLOAD_DIR" "$LOCAL_ADAPTER_SHA" <<'REMOTE'
set -euo pipefail
candidate="$1"; upload="$2"; expected_sha="$3"
work="$upload/extracted"
rm -rf "$work"
mkdir -p "$work"
umask 022
tar -xzf "$upload/release.tar.gz" -C "$work"
pkg="$(find "$work" -mindepth 1 -maxdepth 1 -type d -name 'MCP-Pi-*' -print -quit)"
[ -n "$pkg" ]
sudo rm -rf "$candidate"
sudo mkdir -p "$candidate"
sudo cp -a "$pkg/." "$candidate/"
sudo chown -R root:root "$candidate"
test "$(sudo sha256sum "$candidate/bin/mcp-gateway-adapter" | awk '{print $1}')" = "$expected_sha"
sudo -u mcp-gateway "$candidate/bin/mcp-gateway-adapter" version --json | grep -q '"registry_schema_version": 5'
sudo "$candidate/install.sh" --check >/dev/null
if sudo find "$candidate" -type f \( -name '*.py' -o -name requirements.txt \) -print -quit | grep -q .; then exit 45; fi
for unit in mcp-gateway-admin.service mcp-gateway-mcp.service mcp-gateway-tunnel.service mcp-gateway-maintenance.service mcp-gateway-maintenance.timer mcp-gateway-postboot.service; do
    sudo systemd-analyze verify "$candidate/config/systemd/$unit"
done
REMOTE

echo "[5/10] Backing up Registry and activation state..."
ssh_pi bash -s -- "$REMOTE_TARGET_DIR" "$REMOTE_CANDIDATE" "$REMOTE_UNIT_BACKUP" "$REMOTE_REGISTRY_BACKUP" <<'REMOTE'
set -euo pipefail
current="$1"; candidate="$2"; unit_backup="$3"; registry_backup="$4"
db=/home/mcp-gateway/.local/share/mcp-gateway/gateway.db
sudo rm -rf "$unit_backup"
sudo mkdir -p "$unit_backup"
for unit in mcp-gateway-admin.service mcp-gateway-mcp.service mcp-gateway-tunnel.service mcp-gateway-maintenance.service mcp-gateway-maintenance.timer mcp-gateway-postboot.service; do
    if sudo test -f "/etc/systemd/system/$unit"; then sudo cp -a "/etc/systemd/system/$unit" "$unit_backup/$unit"; else sudo touch "$unit_backup/$unit.absent"; fi
done
if sudo test -f /etc/polkit-1/rules.d/49-mcp-gateway-reboot.rules; then sudo cp -a /etc/polkit-1/rules.d/49-mcp-gateway-reboot.rules "$unit_backup/49-mcp-gateway-reboot.rules"; else sudo touch "$unit_backup/49-mcp-gateway-reboot.rules.absent"; fi
if sudo test -f /usr/local/bin/mcp-gateway-tunnel-check; then sudo cp -a /usr/local/bin/mcp-gateway-tunnel-check "$unit_backup/mcp-gateway-tunnel-check"; fi
sudo install -d -o mcp-gateway -g mcp-gateway -m 0700 "$(dirname "$registry_backup")"
backup_output=$(sudo -u mcp-gateway "$candidate/bin/mcp-gateway-adapter" backup -db "$db" "$registry_backup")
printf '%s\n' "$backup_output" | grep -Eq 'SQLite integrity: ok; schema: (4|5);' || {
    echo "ERROR: rollback Registry backup has unsupported schema or failed integrity validation" >&2
    exit 46
}
REMOTE

echo "[6/10] Activating candidate and migrating Registry with Go..."
echo "CONTROL_PLANE_RESTART=EXPECTED reason=deploy"
ACTIVATED=1
ssh_pi bash -s -- "$REMOTE_TARGET_DIR" "$REMOTE_CANDIDATE" "$REMOTE_PREVIOUS" "$TUNNEL_WAS_ENABLED" <<'REMOTE'
set -euo pipefail
current="$1"; candidate="$2"; previous="$3"; tunnel_enabled="$4"
db=/home/mcp-gateway/.local/share/mcp-gateway/gateway.db
sudo systemctl stop mcp-gateway-tunnel mcp-gateway-postboot mcp-gateway-mcp mcp-gateway-admin 2>/dev/null || true
sudo rm -rf "$previous"
sudo mv "$current" "$previous"
sudo mv "$candidate" "$current"
sudo chown -R root:root "$current"

for unit in mcp-gateway-admin.service mcp-gateway-mcp.service mcp-gateway-tunnel.service mcp-gateway-maintenance.service mcp-gateway-maintenance.timer mcp-gateway-postboot.service; do
    sudo install -o root -g root -m 0644 "$current/config/systemd/$unit" "/etc/systemd/system/$unit"
done
sudo install -o root -g root -m 0755 "$current/config/systemd/mcp-gateway-tunnel-check" /usr/local/bin/mcp-gateway-tunnel-check
if sudo test -d /etc/polkit-1/rules.d; then
    sudo install -o root -g root -m 0644 "$current/config/polkit/49-mcp-gateway-reboot.rules" /etc/polkit-1/rules.d/49-mcp-gateway-reboot.rules
fi
sudo systemctl daemon-reload
sudo systemctl enable mcp-gateway-admin mcp-gateway-mcp mcp-gateway-maintenance.timer mcp-gateway-postboot.service >/dev/null
sudo -u mcp-gateway "$current/bin/mcp-gateway" status -db "$db" >/dev/null

sudo systemctl reset-failed mcp-gateway-admin mcp-gateway-mcp mcp-gateway-postboot mcp-gateway-tunnel 2>/dev/null || true
sudo systemctl restart mcp-gateway-admin
for i in $(seq 1 45); do curl -fsS http://127.0.0.1/login >/dev/null 2>&1 && break; [ "$i" -lt 45 ] || exit 61; sleep 1; done
sudo systemctl restart mcp-gateway-mcp
for i in $(seq 1 60); do curl -fsS http://127.0.0.1:8090/ready >/dev/null 2>&1 && break; [ "$i" -lt 60 ] || exit 62; sleep 1; done
sudo systemctl start mcp-gateway-maintenance.timer
sudo systemctl restart mcp-gateway-postboot.service
if [ "$tunnel_enabled" = 1 ] && sudo test -s /home/mcp-gateway/.config/mcp-gateway/tunnel.env && sudo test -x /usr/local/bin/openai-tunnel-client; then
    sudo systemctl restart mcp-gateway-tunnel
else
    sudo systemctl stop mcp-gateway-tunnel 2>/dev/null || true
fi
REMOTE
echo "CONTROL_PLANE_RESTORED reason=deploy"

if [ "${MCP_DEPLOY_INJECT_FAILURE:-}" = after-activation ]; then
    echo "CONTROLLED_FAILURE_INJECTED after-activation" >&2
    false
fi

echo "[7/10] Running production acceptance..."
ssh_pi bash -s -- "$REMOTE_TARGET_DIR" "$LOCAL_ADAPTER_SHA" "$TUNNEL_WAS_ENABLED" <<'REMOTE'
set -euo pipefail
current="$1"; expected_sha="$2"; tunnel_enabled="$3"
db=/home/mcp-gateway/.local/share/mcp-gateway/gateway.db
systemctl is-active --quiet mcp-gateway-admin
systemctl is-active --quiet mcp-gateway-mcp
curl -fsS http://127.0.0.1/login >/dev/null
curl -fsS http://127.0.0.1:8090/live >/dev/null
curl -fsS http://127.0.0.1:8090/ready >/dev/null
test "$(sha256sum "$current/bin/mcp-gateway-adapter" | awk '{print $1}')" = "$expected_sha"
test "$(stat -c '%U:%G' "$current/bin/mcp-gateway-adapter")" = root:root
"$current/bin/mcp-gateway-adapter" version --json | grep -q '"registry_schema_version": 5'
sudo -u mcp-gateway "$current/bin/mcp-gateway" doctor -db "$db" >/dev/null
if [ "$tunnel_enabled" = 1 ]; then systemctl is-active --quiet mcp-gateway-tunnel; fi
REMOTE

echo "[8/10] Writing verified deployment provenance..."
DEPLOYED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
cat > "$DEPLOYMENT_STAGE" <<EOF
{
  "adapter_sha256": "$LOCAL_ADAPTER_SHA",
  "branch": "$DEPLOY_BRANCH",
  "commit": "$DEPLOY_SHA",
  "deployed_at": "$DEPLOYED_AT",
  "package_sha256": "$PACKAGE_SHA",
  "runtime": "go-only",
  "verified": true
}
EOF
scp_pi "$DEPLOYMENT_STAGE" "$PI_USER@$PI_HOST:$REMOTE_UPLOAD_DIR/deployment.json"
ssh_pi "sudo install -o root -g root -m 0644 '$REMOTE_UPLOAD_DIR/deployment.json' '$REMOTE_TARGET_DIR/.deployment.json'; printf '%s\n' '$DEPLOY_SHA' | sudo tee '$REMOTE_TARGET_DIR/.deployed-git-sha' >/dev/null; printf '%s\n' '$DEPLOY_BRANCH' | sudo tee '$REMOTE_TARGET_DIR/.deployed-git-branch' >/dev/null"

echo "[9/10] Verifying provenance and exact binary..."
ssh_pi bash -s -- "$REMOTE_TARGET_DIR" "$DEPLOY_SHA" "$LOCAL_ADAPTER_SHA" <<'REMOTE'
set -euo pipefail
current="$1"; expected_commit="$2"; expected_adapter="$3"
test "$(cat "$current/.deployed-git-sha")" = "$expected_commit"
test "$(sha256sum "$current/bin/mcp-gateway-adapter" | awk '{print $1}')" = "$expected_adapter"
grep -Fq "\"commit\": \"$expected_commit\"" "$current/.deployment.json"
grep -Fq "\"adapter_sha256\": \"$expected_adapter\"" "$current/.deployment.json"
grep -Fq '"runtime": "go-only"' "$current/.deployment.json"
grep -Fq '"verified": true' "$current/.deployment.json"
REMOTE

echo "[10/10] Cleaning transfer artifacts; preserving one rollback set..."
ssh_pi "sudo rm -rf '$REMOTE_UPLOAD_DIR'"
ACTIVATED=0
trap - ERR

echo "DEPLOYMENT_VERIFIED"
echo "commit=$DEPLOY_SHA"
echo "branch=$DEPLOY_BRANCH"
echo "adapter_sha256=$LOCAL_ADAPTER_SHA"
echo "package_sha256=$PACKAGE_SHA"
echo "rollback_runtime=$REMOTE_PREVIOUS"
echo "rollback_units=$REMOTE_UNIT_BACKUP"
echo "rollback_registry=$REMOTE_REGISTRY_BACKUP"
