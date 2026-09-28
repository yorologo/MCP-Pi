#!/data/data/com.termux/files/usr/bin/env bash
set -Eeuo pipefail

# Exact-commit, Go-only, control-plane-safe deployment for MCP-Pi.
# Activation/update/rollback are delegated to the canonical release install.sh.
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

for cmd in git go tar gzip sha256sum file ssh scp grep awk find; do
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
if systemctl is-enabled --quiet mcp-gateway-gemini 2>/dev/null; then
    systemctl is-active --quiet mcp-gateway-gemini
    curl -fsS http://127.0.0.1:8092/ready >/dev/null
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
OLD_DEPLOY_SHA=""
TUNNEL_WAS_ENABLED=0
GEMINI_WAS_ENABLED=0
ACTIVATED=0

cleanup_local(){ rm -rf "$STAGE_DIR"; }
cleanup_remote_transfer(){ ssh_pi "sudo rm -rf '$REMOTE_UPLOAD_DIR'" >/dev/null 2>&1 || true; }
trap cleanup_local EXIT

rollback() {
    echo "ROLLBACK: delegating to canonical installed lifecycle..." >&2
    echo "CONTROL_PLANE_RESTART=EXPECTED reason=rollback" >&2
    ssh_pi "sudo '$REMOTE_TARGET_DIR/install.sh' --rollback"
    if [ -n "$OLD_DEPLOY_SHA" ]; then
        ssh_pi "test \"$(cat '$REMOTE_TARGET_DIR/.deployed-git-sha' 2>/dev/null || true)\" = '$OLD_DEPLOY_SHA'"
    fi
    ACTIVATED=0
    echo "CONTROL_PLANE_RESTORED reason=rollback"
    echo "ROLLBACK_VERIFIED old_sha=${OLD_DEPLOY_SHA:-unknown}"
}

on_error() {
    local rc=$?
    trap - ERR
    echo "DEPLOYMENT_FAILED rc=$rc" >&2
    if [ "$ACTIVATED" -eq 1 ]; then
        rollback || {
            echo "ROLLBACK_FAILED: manual recovery required; runtime=$REMOTE_TARGET_DIR.previous-install state=$REMOTE_DATA_DIR/install-unit-backup" >&2
            cleanup_remote_transfer
            exit 90
        }
    fi
    cleanup_remote_transfer
    exit "$rc"
}
trap on_error ERR

echo "=== MCP-Pi exact-commit Go-only deployment ==="
echo "branch=$DEPLOY_BRANCH"
echo "commit=$DEPLOY_SHA"

echo "[1/8] Building canonical Go-only release bundle..."
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
if find "$PKG_ROOT" -type f \( -name '*.py' -o -name requirements.txt \) -print -quit | grep -q .; then
    fail "release package contains Python payload"
fi
echo "package_sha256=$PACKAGE_SHA"
echo "adapter_sha256=$LOCAL_ADAPTER_SHA"

echo "[2/8] Remote preflight..."
ssh_pi "test -d '$REMOTE_TARGET_DIR'; command -v sudo >/dev/null; command -v systemctl >/dev/null; command -v curl >/dev/null; command -v tar >/dev/null; command -v sha256sum >/dev/null; command -v systemd-analyze >/dev/null"
OLD_DEPLOY_SHA="$(ssh_pi "cat '$REMOTE_TARGET_DIR/.deployed-git-sha' 2>/dev/null || true")"
if ssh_pi "systemctl is-enabled --quiet mcp-gateway-tunnel" >/dev/null 2>&1; then
    TUNNEL_WAS_ENABLED=1
fi
if ssh_pi "systemctl is-enabled --quiet mcp-gateway-gemini" >/dev/null 2>&1; then
    GEMINI_WAS_ENABLED=1
fi

echo "[3/8] Transferring immutable bundle..."
ssh_pi "mkdir -p '$REMOTE_UPLOAD_DIR' && chmod 700 '$REMOTE_UPLOAD_DIR'"
scp_pi "$PACKAGE" "$PI_USER@$PI_HOST:$REMOTE_UPLOAD_DIR/release.tar.gz"

echo "[4/8] Verifying package and candidate contracts on ARMv6..."
ssh_pi bash -s -- "$REMOTE_UPLOAD_DIR" "$PACKAGE_SHA" "$LOCAL_ADAPTER_SHA" <<'REMOTE'
set -euo pipefail
upload="$1"; expected_package="$2"; expected_adapter="$3"
test "$(sha256sum "$upload/release.tar.gz" | awk '{print $1}')" = "$expected_package"
mkdir -p "$upload/extracted"
umask 022
tar -xzf "$upload/release.tar.gz" -C "$upload/extracted"
candidate="$(find "$upload/extracted" -mindepth 1 -maxdepth 1 -type d -name 'MCP-Pi-*' -print -quit)"
test -n "$candidate"
test "$(sha256sum "$candidate/bin/mcp-gateway-adapter" | awk '{print $1}')" = "$expected_adapter"
sudo sha256sum "$candidate/bin/mcp-gateway-adapter" >/dev/null
sudo "$candidate/install.sh" --check >/dev/null
if sudo find "$candidate" -type f \( -name '*.py' -o -name requirements.txt \) -print -quit | grep -q .; then
    exit 45
fi
for unit in mcp-gateway-admin.service mcp-gateway-mcp.service mcp-gateway-gemini.service mcp-gateway-tunnel.service mcp-gateway-maintenance.service mcp-gateway-maintenance.timer mcp-gateway-postboot.service; do
    sudo systemd-analyze verify "$candidate/config/systemd/$unit"
done
REMOTE

echo "[5/8] Activating through canonical installer..."
echo "CONTROL_PLANE_RESTART=EXPECTED reason=deploy"
ssh_pi bash -s -- "$REMOTE_UPLOAD_DIR" <<'REMOTE'
set -euo pipefail
upload="$1"
candidate="$(find "$upload/extracted" -mindepth 1 -maxdepth 1 -type d -name 'MCP-Pi-*' -print -quit)"
test -n "$candidate"
sudo "$candidate/install.sh" --no-setup
REMOTE
ACTIVATED=1
echo "CONTROL_PLANE_RESTORED reason=deploy"

if [ "${MCP_DEPLOY_INJECT_FAILURE:-}" = after-activation ]; then
    echo "CONTROLLED_FAILURE_INJECTED after-activation" >&2
    false
fi

echo "[6/8] Running production acceptance..."
ssh_pi bash -s -- "$REMOTE_TARGET_DIR" "$LOCAL_ADAPTER_SHA" "$TUNNEL_WAS_ENABLED" "$GEMINI_WAS_ENABLED" <<'REMOTE'
set -euo pipefail
current="$1"; expected_sha="$2"; tunnel_enabled="$3"; gemini_enabled="$4"
db=/home/mcp-gateway/.local/share/mcp-gateway/gateway.db
systemctl is-active --quiet mcp-gateway-admin
systemctl is-active --quiet mcp-gateway-mcp
systemctl is-enabled --quiet mcp-gateway-maintenance.timer
curl -fsS http://127.0.0.1/login >/dev/null
curl -fsS http://127.0.0.1:8090/live >/dev/null
curl -fsS http://127.0.0.1:8090/ready >/dev/null
test "$(sha256sum "$current/bin/mcp-gateway-adapter" | awk '{print $1}')" = "$expected_sha"
test "$(stat -c '%U:%G' "$current/bin/mcp-gateway-adapter")" = root:root
sudo -u mcp-gateway "$current/bin/mcp-gateway" status -db "$db" >/dev/null
sudo -u mcp-gateway "$current/bin/mcp-gateway" doctor -db "$db" >/dev/null
if [ "$tunnel_enabled" = 1 ]; then
    systemctl is-enabled --quiet mcp-gateway-tunnel
    systemctl is-active --quiet mcp-gateway-tunnel
fi
if [ "$gemini_enabled" = 1 ]; then
    systemctl is-enabled --quiet mcp-gateway-gemini
    systemctl is-active --quiet mcp-gateway-gemini
    curl -fsS http://127.0.0.1:8092/ready >/dev/null
fi
REMOTE

echo "[7/8] Writing and verifying deployment provenance..."
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

echo "[8/8] Cleaning transfer artifacts; preserving installer rollback set..."
cleanup_remote_transfer
ACTIVATED=0
trap - ERR

echo "DEPLOYMENT_VERIFIED"
echo "commit=$DEPLOY_SHA"
echo "branch=$DEPLOY_BRANCH"
echo "adapter_sha256=$LOCAL_ADAPTER_SHA"
echo "package_sha256=$PACKAGE_SHA"
echo "rollback_runtime=$REMOTE_TARGET_DIR.previous-install"
echo "rollback_state=$REMOTE_DATA_DIR/install-unit-backup"
