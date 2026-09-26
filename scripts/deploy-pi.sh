#!/data/data/com.termux/files/usr/bin/env bash
set -Eeuo pipefail

# Exact-commit, control-plane-safe development deployment for MCP-Pi.
# Usage: scripts/deploy-pi.sh <exact-git-sha>
# Optional controlled rollback test: MCP_DEPLOY_INJECT_FAILURE=after-activation ...

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
TMP_BASE="${TMPDIR:-${PREFIX:-/data/data/com.termux/files/usr}/tmp}"
REMOTE_TARGET_DIR="/home/mcp-gateway/mcp-gateway"
REMOTE_DATA_DIR="/home/mcp-gateway/.local/share/mcp-gateway"

fail() {
    echo "ERROR: $*" >&2
    exit 1
}

[ "$#" -eq 1 ] || fail "usage: $0 <exact-git-sha>"
if [ -z "${MCP_PI_RESUMABLE_JOB_ID:-}" ] && [ "${MCP_DEPLOY_ALLOW_DIRECT:-0}" != "1" ]; then
    fail "deploy-pi.sh must run via scripts/run-resumable.sh; set MCP_DEPLOY_ALLOW_DIRECT=1 only for explicit break-glass recovery"
fi
command -v git >/dev/null 2>&1 || fail "git is required"

DEPLOY_SHA="$(git -C "${PROJECT_ROOT}" rev-parse "$1^{commit}")"
HEAD_SHA="$(git -C "${PROJECT_ROOT}" rev-parse HEAD)"
DEPLOY_BRANCH="$(git -C "${PROJECT_ROOT}" branch --show-current)"
[ "${DEPLOY_SHA}" = "${HEAD_SHA}" ] || fail "requested commit must equal local HEAD (${HEAD_SHA})"
[ -n "${DEPLOY_BRANCH}" ] || fail "deployment requires a named Git branch"
[ -z "$(git -C "${PROJECT_ROOT}" status --porcelain --untracked-files=normal)" ] || fail "deployment requires a completely clean Git working tree"

git -C "${PROJECT_ROOT}" fetch origin "${DEPLOY_BRANCH}" --quiet
REMOTE_BRANCH_SHA="$(git -C "${PROJECT_ROOT}" rev-parse "origin/${DEPLOY_BRANCH}")"
[ "${REMOTE_BRANCH_SHA}" = "${DEPLOY_SHA}" ] || fail "origin/${DEPLOY_BRANCH} (${REMOTE_BRANCH_SHA}) does not equal requested commit (${DEPLOY_SHA})"

if [ -f "${PROJECT_ROOT}/.mcp-pi.local.env" ]; then
    set -a
    # shellcheck disable=SC1091
    source "${PROJECT_ROOT}/.mcp-pi.local.env"
    set +a
fi

PI_HOST="${MCP_PI_HOST:-192.168.68.55}"
PI_USER="${MCP_PI_USER:-yorologo}"
PI_IDENTITY_FILE="${MCP_PI_IDENTITY_FILE:-${HOME}/.ssh/id_rsa}"

for cmd in go tar gzip sha256sum file strings ssh scp; do
    command -v "${cmd}" >/dev/null 2>&1 || fail "${cmd} is required for deployment"
done
[ -r "${PI_IDENTITY_FILE}" ] || fail "SSH identity not readable: ${PI_IDENTITY_FILE}"
mkdir -p "${TMP_BASE}"

SSH_OPTS=(
    -i "${PI_IDENTITY_FILE}"
    -o BatchMode=yes
    -o StrictHostKeyChecking=yes
)

ssh_pi() {
    ssh "${SSH_OPTS[@]}" "${PI_USER}@${PI_HOST}" "$@"
}

scp_pi() {
    scp -O "${SSH_OPTS[@]}" "$@"
}

already_deployed() {
    ssh_pi bash -s -- "${REMOTE_TARGET_DIR}" "${DEPLOY_SHA}" <<'REMOTE'
set -euo pipefail
current="$1"
expected="$2"
test "$(cat "$current/.deployed-git-sha" 2>/dev/null || true)" = "$expected"
python3 - "$current/.deployment.json" "$expected" <<'PY'
import json, sys
p, expected = sys.argv[1:]
d = json.load(open(p, encoding='utf-8'))
assert d.get('verified') is True
assert d.get('commit') == expected
PY
systemctl is-active --quiet mcp-gateway-admin
systemctl is-active --quiet mcp-gateway-mcp
systemctl is-active --quiet mcp-gateway-tunnel
curl -fsS http://127.0.0.1/login >/dev/null
curl -fsS http://127.0.0.1:8090/ready >/dev/null
REMOTE
}

if [ "${MCP_DEPLOY_FORCE:-0}" != "1" ] && [ -z "${MCP_DEPLOY_INJECT_FAILURE:-}" ]; then
    if already_deployed >/dev/null 2>&1; then
        echo "ALREADY_DEPLOYED commit=${DEPLOY_SHA}"
        echo "DEPLOYMENT_VERIFIED already_deployed=true"
        exit 0
    fi
fi

SHORT_SHA="${DEPLOY_SHA:0:12}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
STAGE_DIR="$(mktemp -d "${TMP_BASE}/mcp-pi-deploy.XXXXXX")"
SOURCE_DIR="${STAGE_DIR}/source"
SOURCE_ARCHIVE="${STAGE_DIR}/source-${SHORT_SHA}.tar.gz"
ADAPTER_STAGE="${STAGE_DIR}/mcp-gateway-adapter"
STRINGS_STAGE="${STAGE_DIR}/adapter.strings"
DEPLOYMENT_STAGE="${STAGE_DIR}/.deployment.json"
mkdir -p "${SOURCE_DIR}"

REMOTE_UPLOAD_DIR=""
REMOTE_CANDIDATE="${REMOTE_TARGET_DIR}.candidate-${SHORT_SHA}-${STAMP}"
REMOTE_PREVIOUS="${REMOTE_TARGET_DIR}.previous-${STAMP}"
REMOTE_FAILED="${REMOTE_TARGET_DIR}.failed-${STAMP}"
REMOTE_UNIT_BACKUP="${REMOTE_DATA_DIR}/deploy-unit-backup-${STAMP}"
REMOTE_REGISTRY_BACKUP="${REMOTE_DATA_DIR}/backups/deploy-pre-${SHORT_SHA}-${STAMP}.db"
OLD_DEPLOY_SHA=""
ACTIVATED=0

cleanup_local() {
    rm -rf "${STAGE_DIR}"
}
trap cleanup_local EXIT

wait_remote_http() {
    local url="$1"
    local tries="${2:-60}"
    ssh_pi bash -s -- "${url}" "${tries}" <<'REMOTE'
set -euo pipefail
url="$1"
tries="$2"
i=1
while [ "$i" -le "$tries" ]; do
    if curl -fsS "$url" >/dev/null 2>&1; then
        exit 0
    fi
    i=$((i + 1))
    sleep 1
done
exit 1
REMOTE
}

rollback() {
    echo "ROLLBACK: restoring previous runtime and systemd units..." >&2
    echo "CONTROL_PLANE_RESTART=EXPECTED reason=rollback" >&2
    local rollback_rc=0
    ssh_pi bash -s -- \
        "${REMOTE_TARGET_DIR}" "${REMOTE_PREVIOUS}" "${REMOTE_FAILED}" \
        "${REMOTE_UNIT_BACKUP}" "${OLD_DEPLOY_SHA}" "${REMOTE_REGISTRY_BACKUP}" <<'REMOTE' || rollback_rc=$?
set -euo pipefail
current="$1"
previous="$2"
failed="$3"
unit_backup="$4"
old_sha="$5"
registry_backup="$6"

sudo systemctl stop mcp-gateway-tunnel 2>/dev/null || true
sudo systemctl stop mcp-gateway-mcp 2>/dev/null || true
sudo systemctl stop mcp-gateway-admin 2>/dev/null || true
sudo rm -rf "$failed"
if [ -d "$current" ]; then
    sudo mv "$current" "$failed"
fi
[ -d "$previous" ] || { echo "ROLLBACK ERROR: previous runtime missing" >&2; exit 80; }
sudo mv "$previous" "$current"

for unit in mcp-gateway-admin.service mcp-gateway-mcp.service mcp-gateway-tunnel.service; do
    sudo test -f "$unit_backup/$unit" || { echo "ROLLBACK ERROR: unit backup missing: $unit" >&2; exit 81; }
    sudo install -m 0644 "$unit_backup/$unit" "/etc/systemd/system/$unit"
done
sudo test -s "$registry_backup" || { echo "ROLLBACK ERROR: Registry backup missing: $registry_backup" >&2; exit 86; }
sudo -u mcp-gateway env PYTHONPATH="$current/src" MCP_GATEWAY_DB=/home/mcp-gateway/.local/share/mcp-gateway/gateway.db \
    python3 - "$registry_backup" <<'PY'
import sys
from mcp_gateway.lifecycle import restore_database

if restore_database(sys.argv[1]) is not True:
    raise SystemExit("Registry restore did not report success")
PY
sudo systemctl daemon-reload
sudo systemctl reset-failed mcp-gateway-admin mcp-gateway-mcp mcp-gateway-tunnel
sudo systemctl restart mcp-gateway-admin
for i in $(seq 1 45); do
    systemctl is-active --quiet mcp-gateway-admin && curl -fsS http://127.0.0.1/login >/dev/null 2>&1 && break
    [ "$i" -eq 45 ] && exit 82
    sleep 1
done
sudo systemctl restart mcp-gateway-mcp
for i in $(seq 1 60); do
    systemctl is-active --quiet mcp-gateway-mcp && curl -fsS http://127.0.0.1:8090/ready >/dev/null 2>&1 && break
    [ "$i" -eq 60 ] && exit 83
    sleep 1
done
sudo systemctl start mcp-gateway-tunnel
for i in $(seq 1 60); do
    systemctl is-active --quiet mcp-gateway-tunnel && break
    [ "$i" -eq 60 ] && exit 84
    sleep 1
done
if [ -n "$old_sha" ]; then
    test "$(cat "$current/.deployed-git-sha" 2>/dev/null || true)" = "$old_sha" || exit 85
fi
sudo -u mcp-gateway "$current/bin/mcp-gateway" doctor >/dev/null
sudo rm -rf "$failed" "$unit_backup"
sudo rm -f "$registry_backup"
REMOTE
    if [ "${rollback_rc}" -ne 0 ]; then
        echo "ROLLBACK_REMOTE_FAILED rc=${rollback_rc}" >&2
        return "${rollback_rc}"
    fi
    echo "CONTROL_PLANE_RESTORED reason=rollback"
    ACTIVATED=0
    echo "ROLLBACK_VERIFIED old_sha=${OLD_DEPLOY_SHA:-unknown}"
}

cleanup_remote_transfer() {
    if [ -n "${REMOTE_UPLOAD_DIR}" ]; then
        ssh_pi "sudo rm -rf '${REMOTE_UPLOAD_DIR}'" >/dev/null 2>&1 || true
    fi
    if [ "${ACTIVATED}" -eq 0 ]; then
        ssh_pi "sudo rm -rf '${REMOTE_CANDIDATE}'; sudo rm -f '${REMOTE_REGISTRY_BACKUP}'" >/dev/null 2>&1 || true
    fi
}

on_error() {
    local rc=$?
    trap - ERR
    echo "DEPLOYMENT_FAILED rc=${rc}" >&2
    if [ "${ACTIVATED}" -eq 1 ]; then
        if ! rollback; then
            echo "ROLLBACK_FAILED: manual recovery required; previous=${REMOTE_PREVIOUS}" >&2
            cleanup_remote_transfer
            exit 90
        fi
    fi
    cleanup_remote_transfer
    exit "${rc}"
}
trap on_error ERR

echo "=== MCP-Pi exact-commit deployment ==="
echo "branch=${DEPLOY_BRANCH}"
echo "commit=${DEPLOY_SHA}"

echo "[1/10] Creating deterministic source package from Git commit..."
git -C "${PROJECT_ROOT}" archive --format=tar "${DEPLOY_SHA}" | gzip -n -9 > "${SOURCE_ARCHIVE}"
tar -xzf "${SOURCE_ARCHIVE}" -C "${SOURCE_DIR}"
PACKAGE_SHA="$(sha256sum "${SOURCE_ARCHIVE}" | awk '{print $1}')"
echo "package_sha256=${PACKAGE_SHA}"

echo "[2/10] Building and validating ARMv6 adapter from archived source..."
(
    cd "${SOURCE_DIR}/mcp-adapter"
    GOOS=linux GOARCH=arm GOARM=6 CGO_ENABLED=0 \
        go build -trimpath -ldflags='-s -w' -o "${ADAPTER_STAGE}" .
)
file "${ADAPTER_STAGE}" | grep -Eq 'ELF 32-bit.*ARM' || fail "adapter is not a Linux ARM executable"
strings "${ADAPTER_STAGE}" > "${STRINGS_STAGE}"
grep -Fq -- 'auth-token-file' "${STRINGS_STAGE}" || fail "adapter is missing -auth-token-file"
grep -Fq -- 'client-id' "${STRINGS_STAGE}" || fail "adapter is missing -client-id"
LOCAL_ADAPTER_SHA="$(sha256sum "${ADAPTER_STAGE}" | awk '{print $1}')"
echo "adapter_sha256=${LOCAL_ADAPTER_SHA}"

echo "[3/10] Remote preflight and transfer..."
ssh_pi "test -d '${REMOTE_TARGET_DIR}'; command -v sudo >/dev/null; command -v systemctl >/dev/null; command -v curl >/dev/null; command -v python3 >/dev/null; command -v tar >/dev/null; command -v sha256sum >/dev/null"
REMOTE_UPLOAD_DIR="/tmp/mcp-gateway-deploy-${SHORT_SHA}-${STAMP}"
ssh_pi "mkdir -p '${REMOTE_UPLOAD_DIR}' && chmod 700 '${REMOTE_UPLOAD_DIR}'"
scp_pi "${SOURCE_ARCHIVE}" "${ADAPTER_STAGE}" "${PI_USER}@${PI_HOST}:${REMOTE_UPLOAD_DIR}/"
OLD_DEPLOY_SHA="$(ssh_pi "cat '${REMOTE_TARGET_DIR}/.deployed-git-sha' 2>/dev/null || true")"

echo "[4/10] Preparing and validating candidate on the real ARMv6 appliance..."
ssh_pi bash -s -- "${REMOTE_CANDIDATE}" "${REMOTE_TARGET_DIR}" "${REMOTE_UPLOAD_DIR}" "${LOCAL_ADAPTER_SHA}" <<'REMOTE'
set -euo pipefail
candidate="$1"
current="$2"
upload="$3"
expected_adapter_sha="$4"
archive="$(find "$upload" -maxdepth 1 -name 'source-*.tar.gz' -print -quit)"
adapter="$upload/mcp-gateway-adapter"
[ -f "$archive" ]
[ -f "$adapter" ]
chmod 0644 "$archive"
chmod 0755 "$adapter"
sudo rm -rf "$candidate"
sudo mkdir -p "$candidate"
sudo tar -xzf "$archive" -C "$candidate"
if [ -f "$current/config/targets.local.json" ]; then
    sudo mkdir -p "$candidate/config"
    sudo cp -p "$current/config/targets.local.json" "$candidate/config/targets.local.json"
fi
sudo chown -R mcp-gateway:mcp-gateway "$candidate"
sudo install -o mcp-gateway -g mcp-gateway -m 0755 "$adapter" "$candidate/bin/mcp-gateway-adapter"
sudo chmod 0755 "$candidate/bin/mcp-gateway" "$candidate/bin/mcp-gateway-client-stdio" "$candidate/install.sh"
test "$(sha256sum "$candidate/bin/mcp-gateway-adapter" | awk '{print $1}')" = "$expected_adapter_sha"
HELP=$("$candidate/bin/mcp-gateway-adapter" -help 2>&1 || true)
printf '%s\n' "$HELP" | grep -q -- '-auth-token-file'
printf '%s\n' "$HELP" | grep -q -- '-client-id'
sudo -u mcp-gateway env PYTHONPATH="$candidate/src" MCP_GATEWAY_DB=/home/mcp-gateway/.local/share/mcp-gateway/gateway.db \
    python3 -m mcp_gateway.bridge version >/dev/null
sudo -u mcp-gateway env PYTHONPATH="$candidate/src" python3 - "$candidate/manifest.json" <<PY
import json
import sys
from mcp_gateway.bridge import ALLOWED_TOOLS, get_catalog_metadata

with open(sys.argv[1], encoding="utf-8") as fh:
    manifest = json.load(fh)
m = get_catalog_metadata(sorted(ALLOWED_TOOLS))
assert m['tool_count'] == len(ALLOWED_TOOLS), m
assert m['tool_catalog_version'] == manifest['tool_catalog'], (m, manifest)
assert len(m['catalog_hash']) == 64, m
PY
for unit in mcp-gateway-admin.service mcp-gateway-mcp.service mcp-gateway-tunnel.service; do
    systemd-analyze verify "$candidate/config/systemd/$unit"
done
REMOTE

echo "[5/10] Backing up Registry/runtime/unit set and atomically activating candidate..."
ssh_pi bash -s -- "${REMOTE_TARGET_DIR}" "${REMOTE_CANDIDATE}" "${REMOTE_PREVIOUS}" "${REMOTE_UNIT_BACKUP}" "${REMOTE_REGISTRY_BACKUP}" <<'REMOTE'
set -euo pipefail
current="$1"
candidate="$2"
previous="$3"
unit_backup="$4"
registry_backup="$5"
[ -d "$current" ]
[ -d "$candidate" ]
sudo rm -rf "$previous" "$unit_backup"
sudo mkdir -p "$unit_backup"
sudo install -d -o mcp-gateway -g mcp-gateway -m 0700 "$(dirname "$registry_backup")"
sudo -u mcp-gateway "$current/bin/mcp-gateway" backup "$registry_backup" >/dev/null
sudo -u mcp-gateway python3 - "$registry_backup" <<'PY'
import sqlite3
import sys

conn = sqlite3.connect(sys.argv[1])
try:
    assert conn.execute("PRAGMA integrity_check").fetchone()[0] == "ok"
    assert conn.execute("PRAGMA user_version").fetchone()[0] >= 1
finally:
    conn.close()
PY
for unit in mcp-gateway-admin.service mcp-gateway-mcp.service mcp-gateway-tunnel.service; do
    sudo cp -a "/etc/systemd/system/$unit" "$unit_backup/$unit"
done
moved=0
restore_on_error() {
    if [ "$moved" -eq 1 ] && [ ! -d "$current" ] && [ -d "$previous" ]; then
        sudo mv "$previous" "$current" || true
    fi
}
trap restore_on_error ERR
sudo mv "$current" "$previous"
moved=1
sudo mv "$candidate" "$current"
moved=2
trap - ERR
REMOTE
ACTIVATED=1

echo "[6/10] Installing candidate units and restarting in control-plane-safe order..."
echo "CONTROL_PLANE_RESTART=EXPECTED reason=deploy"
ssh_pi bash -s -- "${REMOTE_TARGET_DIR}" <<'REMOTE'
set -euo pipefail
current="$1"
config_dir=/home/mcp-gateway/.config/mcp-gateway
admin_env="$config_dir/admin.env"
if ! sudo test -s "$admin_env"; then
    host="$(hostname -s 2>/dev/null || printf 'mcp-pi')"
    ip="$(hostname -I 2>/dev/null | awk '{print $1}' || true)"
    allowed="127.0.0.1,localhost,${host}"
    [ -n "$ip" ] && allowed="${allowed},${ip}"
    tmp="$(mktemp)"
    chmod 0600 "$tmp"
    {
        echo "MCP_ADMIN_HOST=0.0.0.0"
        echo "MCP_ADMIN_PORT=80"
        echo "MCP_ADMIN_ALLOWED_HOSTS=${allowed}"
    } > "$tmp"
    sudo install -o mcp-gateway -g mcp-gateway -m 0600 "$tmp" "$admin_env"
    rm -f "$tmp"
fi
for unit in mcp-gateway-admin.service mcp-gateway-mcp.service mcp-gateway-tunnel.service; do
    sudo install -m 0644 "$current/config/systemd/$unit" "/etc/systemd/system/$unit"
done
sudo systemctl daemon-reload
sudo systemctl enable mcp-gateway-admin mcp-gateway-mcp mcp-gateway-tunnel >/dev/null
sudo systemctl reset-failed mcp-gateway-admin mcp-gateway-mcp mcp-gateway-tunnel
sudo systemctl restart mcp-gateway-admin
for i in $(seq 1 45); do
    systemctl is-active --quiet mcp-gateway-admin && curl -fsS http://127.0.0.1/login >/dev/null 2>&1 && break
    [ "$i" -eq 45 ] && exit 61
    sleep 1
done
sudo systemctl stop mcp-gateway-tunnel 2>/dev/null || true
sudo systemctl restart mcp-gateway-mcp
for i in $(seq 1 60); do
    systemctl is-active --quiet mcp-gateway-mcp && curl -fsS http://127.0.0.1:8090/ready >/dev/null 2>&1 && break
    [ "$i" -eq 60 ] && exit 62
    sleep 1
done
sudo systemctl start mcp-gateway-tunnel
for i in $(seq 1 60); do
    systemctl is-active --quiet mcp-gateway-tunnel && break
    [ "$i" -eq 60 ] && exit 63
    sleep 1
done
REMOTE
echo "CONTROL_PLANE_RESTORED reason=deploy"

if [ "${MCP_DEPLOY_INJECT_FAILURE:-}" = "after-activation" ]; then
    echo "CONTROLLED_FAILURE_INJECTED after-activation" >&2
    false
fi

echo "[7/10] Running lightweight production acceptance..."
ssh_pi bash -s -- "${REMOTE_TARGET_DIR}" "${LOCAL_ADAPTER_SHA}" <<'REMOTE'
set -euo pipefail
current="$1"
expected_adapter_sha="$2"
systemctl is-active --quiet mcp-gateway-admin
systemctl is-active --quiet mcp-gateway-mcp
systemctl is-active --quiet mcp-gateway-tunnel
curl -fsS http://127.0.0.1/login >/dev/null
curl -fsS http://127.0.0.1:8090/live >/dev/null
curl -fsS http://127.0.0.1:8090/ready >/dev/null
test "$(sha256sum "$current/bin/mcp-gateway-adapter" | awk '{print $1}')" = "$expected_adapter_sha"
sudo -u mcp-gateway python3 - <<'PY'
import sqlite3
p='/home/mcp-gateway/.local/share/mcp-gateway/gateway.db'
c=sqlite3.connect(p)
assert c.execute('PRAGMA integrity_check').fetchone()[0] == 'ok'
c.close()
PY
sudo -u mcp-gateway "$current/bin/mcp-gateway" doctor >/dev/null
REMOTE

echo "[8/10] Writing verified deployment provenance..."
DEPLOYED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
python - "${DEPLOYMENT_STAGE}" "${DEPLOY_SHA}" "${DEPLOY_BRANCH}" "${DEPLOYED_AT}" "${LOCAL_ADAPTER_SHA}" "${PACKAGE_SHA}" <<'PY'
import json, sys
path, commit, branch, deployed_at, adapter_sha, package_sha = sys.argv[1:]
data = {
    'commit': commit,
    'branch': branch,
    'deployed_at': deployed_at,
    'adapter_sha256': adapter_sha,
    'package_sha256': package_sha,
    'verified': True,
}
with open(path, 'w', encoding='utf-8') as f:
    json.dump(data, f, indent=2, sort_keys=True)
    f.write('\n')
PY
scp_pi "${DEPLOYMENT_STAGE}" "${PI_USER}@${PI_HOST}:${REMOTE_UPLOAD_DIR}/deployment.json"
ssh_pi "sudo install -o mcp-gateway -g mcp-gateway -m 0644 '${REMOTE_UPLOAD_DIR}/deployment.json' '${REMOTE_TARGET_DIR}/.deployment.json'; printf '%s\n' '${DEPLOY_SHA}' | sudo -u mcp-gateway tee '${REMOTE_TARGET_DIR}/.deployed-git-sha' >/dev/null; printf '%s\n' '${DEPLOY_BRANCH}' | sudo -u mcp-gateway tee '${REMOTE_TARGET_DIR}/.deployed-git-branch' >/dev/null"

echo "[9/10] Verifying recorded commit, provenance and adapter hash..."
ssh_pi bash -s -- "${REMOTE_TARGET_DIR}" "${DEPLOY_SHA}" "${LOCAL_ADAPTER_SHA}" <<'REMOTE'
set -euo pipefail
current="$1"
expected_commit="$2"
expected_adapter="$3"
test "$(cat "$current/.deployed-git-sha")" = "$expected_commit"
test "$(sha256sum "$current/bin/mcp-gateway-adapter" | awk '{print $1}')" = "$expected_adapter"
python3 - "$current/.deployment.json" "$expected_commit" "$expected_adapter" <<'PY'
import json, sys
p, commit, adapter = sys.argv[1:]
d=json.load(open(p, encoding='utf-8'))
assert d['verified'] is True
assert d['commit'] == commit
assert d['adapter_sha256'] == adapter
PY
REMOTE

echo "[10/10] Cleaning transfer artifacts; preserving one rollback set until final external acceptance..."
ssh_pi "sudo rm -rf '${REMOTE_UPLOAD_DIR}'"
REMOTE_UPLOAD_DIR=""
ACTIVATED=0
trap - ERR

echo "DEPLOYMENT_VERIFIED"
echo "commit=${DEPLOY_SHA}"
echo "branch=${DEPLOY_BRANCH}"
echo "adapter_sha256=${LOCAL_ADAPTER_SHA}"
echo "package_sha256=${PACKAGE_SHA}"
echo "rollback_runtime=${REMOTE_PREVIOUS}"
echo "rollback_units=${REMOTE_UNIT_BACKUP}"
echo "rollback_registry=${REMOTE_REGISTRY_BACKUP}"
