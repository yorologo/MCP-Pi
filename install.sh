#!/bin/sh
# KISS installer/updater for a local MCP Gateway release tree.
set -eu

fail() {
    echo "ERROR: $*" >&2
    exit 1
}

warn() {
    echo "WARNING: $*" >&2
}

usage() {
    cat <<'USAGE'
Usage: ./install.sh [--check] [--rollback] [--no-setup]

  --check      Validate this source/release without changing the system.
  --rollback   Restore the previous installer-managed runtime and units.
  --no-setup   Do not launch the interactive admin bootstrap after install.
USAGE
}

MODE=install
RUN_SETUP=1
while [ "$#" -gt 0 ]; do
    case "$1" in
        --check) MODE=check ;;
        --rollback) MODE=rollback ;;
        --no-setup) RUN_SETUP=0 ;;
        -h|--help) usage; exit 0 ;;
        *) usage >&2; fail "unknown argument: $1" ;;
    esac
    shift
done

SOURCE_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
SERVICE_USER=mcp-gateway
SERVICE_GROUP=mcp-gateway
SERVICE_HOME=/home/mcp-gateway
INSTALL_DIR=${MCP_GATEWAY_INSTALL_DIR:-${SERVICE_HOME}/mcp-gateway}
PREVIOUS_DIR=${SERVICE_HOME}/mcp-gateway.previous-install
DATA_DIR=${SERVICE_HOME}/.local/share/mcp-gateway
CONFIG_DIR=${SERVICE_HOME}/.config/mcp-gateway
UNIT_BACKUP=${DATA_DIR}/install-unit-backup
SYSTEMD_DIR=/etc/systemd/system
BIN_DIR=/usr/local/bin
TOKEN_FILE=${CONFIG_DIR}/tunnel-mcp.token
ADMIN_ENV=${CONFIG_DIR}/admin.env
TUNNEL_CHECK=${BIN_DIR}/mcp-gateway-tunnel-check
CLI_LINK=${BIN_DIR}/mcp-gateway

required_source() {
    for path in \
        bin/mcp-gateway \
        bin/mcp-gateway-client-stdio \
        src/mcp_gateway \
        config/systemd/mcp-gateway-admin.service \
        config/systemd/mcp-gateway-mcp.service \
        compatibility.json \
        manifest.json \
        requirements.txt; do
        [ -e "${SOURCE_DIR}/${path}" ] || fail "release source is missing: ${path}"
    done
}

python_ok() {
    python3 -c 'import sys; raise SystemExit(0 if sys.version_info >= (3, 9) else 1)' >/dev/null 2>&1
}

python_deps_ok() {
    python3 -c 'import sqlite3, flask, werkzeug' >/dev/null 2>&1
}

install_python_deps_if_needed() {
    if python_deps_ok; then
        return 0
    fi
    if command -v apt-get >/dev/null 2>&1; then
        echo "      Installing Python runtime dependencies from the OS package manager..."
        DEBIAN_FRONTEND=noninteractive apt-get update
        DEBIAN_FRONTEND=noninteractive apt-get install -y python3-flask python3-werkzeug
        python_deps_ok || fail "Flask/Werkzeug are still unavailable after apt installation"
        return 0
    fi
    fail "Python dependencies are missing. Install requirements.txt (Flask/Werkzeug) and rerun."
}

copy_adapter() {
    dest=$1
    adapter=""
    if [ -n "${MCP_GATEWAY_ADAPTER_BINARY:-}" ]; then
        adapter=${MCP_GATEWAY_ADAPTER_BINARY}
    elif [ -x "${SOURCE_DIR}/bin/mcp-gateway-adapter" ]; then
        adapter=${SOURCE_DIR}/bin/mcp-gateway-adapter
    fi

    if [ -n "$adapter" ]; then
        [ -r "$adapter" ] || fail "adapter binary is not readable: $adapter"
        cp "$adapter" "$dest"
        chmod 0755 "$dest"
        return 0
    fi

    if command -v go >/dev/null 2>&1 && [ -d "${SOURCE_DIR}/mcp-adapter" ]; then
        arch=$(uname -m)
        case "$arch" in
            armv6*) goarch=arm; goarm=6 ;;
            armv7*) goarch=arm; goarm=7 ;;
            aarch64|arm64) goarch=arm64; goarm= ;;
            x86_64|amd64) goarch=amd64; goarm= ;;
            *) fail "cannot build adapter automatically for architecture: $arch" ;;
        esac
        echo "      No prebuilt adapter found; building natively because Go is already installed..."
        (
            cd "${SOURCE_DIR}/mcp-adapter"
            if [ -n "$goarm" ]; then
                CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" GOARM="$goarm" go build -trimpath -ldflags='-s -w' -o "$dest" .
            else
                CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" go build -trimpath -ldflags='-s -w' -o "$dest" .
            fi
        )
        chmod 0755 "$dest"
        return 0
    fi

    fail "no prebuilt adapter is present. Use an official release bundle, set MCP_GATEWAY_ADAPTER_BINARY, or install Go on a development-capable host."
}

validate_adapter() {
    adapter=$1
    [ -x "$adapter" ] || fail "adapter is not executable: $adapter"
    "$adapter" -help >/dev/null 2>&1 || fail "adapter cannot execute on this architecture"
    "$adapter" -python "$(command -v python3)" -pythonpath "${SOURCE_DIR}/src" -version >/dev/null 2>&1 ||
        fail "adapter/Core version contract validation failed"
}

validate_source() {
    required_source
    command -v python3 >/dev/null 2>&1 || fail "python3 is required"
    python_ok || fail "Python 3.9 or higher is required"
    python_deps_ok || fail "Python runtime dependencies are missing; install requirements.txt"
    tmp=$(mktemp -d "${TMPDIR:-/tmp}/mcp-install-check.XXXXXX")
    trap 'rm -rf "$tmp"' 0 HUP INT TERM
    copy_adapter "$tmp/mcp-gateway-adapter"
    validate_adapter "$tmp/mcp-gateway-adapter"
    rm -rf "$tmp"
    trap - 0 HUP INT TERM
}

as_service() {
    if command -v runuser >/dev/null 2>&1; then
        runuser -u "$SERVICE_USER" -- "$@"
    elif command -v sudo >/dev/null 2>&1; then
        sudo -u "$SERVICE_USER" "$@"
    else
        fail "runuser or sudo is required to execute service-user checks"
    fi
}

wait_url() {
    url=$1
    tries=$2
    i=1
    while [ "$i" -le "$tries" ]; do
        if python3 - "$url" <<'PY' >/dev/null 2>&1
import sys, urllib.request
urllib.request.urlopen(sys.argv[1], timeout=2).read()
PY
        then
            return 0
        fi
        i=$((i + 1))
        sleep 1
    done
    return 1
}

restore_system_files() {
    [ -d "$UNIT_BACKUP" ] || return 0
    for unit in mcp-gateway-admin.service mcp-gateway-mcp.service mcp-gateway-tunnel.service; do
        if [ -f "${UNIT_BACKUP}/${unit}" ]; then
            install -m 0644 "${UNIT_BACKUP}/${unit}" "${SYSTEMD_DIR}/${unit}"
        elif [ -f "${UNIT_BACKUP}/${unit}.absent" ]; then
            systemctl disable "$unit" >/dev/null 2>&1 || true
            rm -f "${SYSTEMD_DIR}/${unit}"
        fi
    done

    if [ -f "${UNIT_BACKUP}/mcp-gateway-tunnel-check" ]; then
        install -m 0755 "${UNIT_BACKUP}/mcp-gateway-tunnel-check" "$TUNNEL_CHECK"
    elif [ -f "${UNIT_BACKUP}/mcp-gateway-tunnel-check.absent" ]; then
        rm -f "$TUNNEL_CHECK"
    fi

    if [ -e "${UNIT_BACKUP}/mcp-gateway-cli" ] || [ -L "${UNIT_BACKUP}/mcp-gateway-cli" ]; then
        rm -f "$CLI_LINK"
        cp -a "${UNIT_BACKUP}/mcp-gateway-cli" "$CLI_LINK"
    elif [ -f "${UNIT_BACKUP}/mcp-gateway-cli.absent" ]; then
        rm -f "$CLI_LINK"
    fi

    systemctl daemon-reload
}

restart_runtime() {
    systemctl reset-failed mcp-gateway-admin mcp-gateway-mcp mcp-gateway-tunnel 2>/dev/null || true
    systemctl restart mcp-gateway-admin
    wait_url http://127.0.0.1/login 45 || fail "mcp-gateway-admin did not become ready"
    systemctl restart mcp-gateway-mcp
    wait_url http://127.0.0.1:8090/ready 60 || fail "mcp-gateway-mcp did not become ready within 60 seconds"

    if systemctl is-enabled --quiet mcp-gateway-tunnel 2>/dev/null; then
        if [ -s "${CONFIG_DIR}/tunnel.env" ] && [ -x /usr/local/bin/openai-tunnel-client ]; then
            systemctl restart mcp-gateway-tunnel
        else
            warn "tunnel service is enabled but credentials/client are incomplete; leaving it stopped"
            systemctl stop mcp-gateway-tunnel 2>/dev/null || true
        fi
    fi
}

rollback_runtime() {
    echo "ROLLBACK: restoring previous installer-managed runtime..." >&2
    systemctl stop mcp-gateway-tunnel 2>/dev/null || true
    systemctl stop mcp-gateway-mcp 2>/dev/null || true
    systemctl stop mcp-gateway-admin 2>/dev/null || true

    failed="${SERVICE_HOME}/mcp-gateway.failed-install.$(date -u +%Y%m%dT%H%M%SZ)"
    if [ -d "$INSTALL_DIR" ]; then
        mv "$INSTALL_DIR" "$failed"
    fi
    [ -d "$PREVIOUS_DIR" ] || fail "previous installer-managed runtime is missing: $PREVIOUS_DIR"
    mv "$PREVIOUS_DIR" "$INSTALL_DIR"
    restore_system_files
    restart_runtime
    as_service "$INSTALL_DIR/bin/mcp-gateway" doctor >/dev/null
    rm -rf "$failed" "$UNIT_BACKUP"
    echo "ROLLBACK_VERIFIED"
}

if [ "$MODE" = check ]; then
    echo "=== MCP Gateway release/install preflight ==="
    validate_source
    echo "INSTALL_CHECK=PASS"
    exit 0
fi

[ "$(id -u)" = "0" ] || fail "install.sh must run as root (for example: sudo ./install.sh)"

for cmd in systemctl useradd install cp mv rm mktemp touch; do
    command -v "$cmd" >/dev/null 2>&1 || fail "$cmd is required but not installed"
done

if [ "$MODE" = rollback ]; then
    rollback_runtime
    exit 0
fi

required_source
command -v python3 >/dev/null 2>&1 || {
    if command -v apt-get >/dev/null 2>&1; then
        DEBIAN_FRONTEND=noninteractive apt-get update
        DEBIAN_FRONTEND=noninteractive apt-get install -y python3
    else
        fail "python3 is required"
    fi
}
python_ok || fail "Python 3.9 or higher is required"
install_python_deps_if_needed

arch=$(uname -m)
case "$arch" in
    armv6*|armv7*|aarch64|arm64|x86_64|amd64) ;;
    *) warn "architecture $arch is not in the verified compatibility set" ;;
esac

echo "=================================================="
echo "=== MCP Gateway Installer / Updater            ==="
echo "=================================================="
echo "Source: ${SOURCE_DIR}"
echo "Target: ${INSTALL_DIR}"

if id -u "$SERVICE_USER" >/dev/null 2>&1; then
    echo "[1/9] Service user exists."
else
    echo "[1/9] Creating service user ${SERVICE_USER}..."
    useradd -r -s /bin/bash -m -d "$SERVICE_HOME" "$SERVICE_USER"
fi

mkdir -p "$DATA_DIR/backups" "$CONFIG_DIR"
chmod 0700 "$DATA_DIR" "$CONFIG_DIR"
chown -R "$SERVICE_USER:$SERVICE_GROUP" "$DATA_DIR" "$CONFIG_DIR"

STAGE=$(mktemp -d "${SERVICE_HOME}/mcp-gateway.install.XXXXXX")
ACTIVATED=0
install_exit() {
    rc=$?
    trap - 0 HUP INT TERM
    if [ "$rc" -ne 0 ]; then
        if [ "$ACTIVATED" -eq 1 ] && [ -d "$PREVIOUS_DIR" ]; then
            rollback_runtime || true
        elif [ "$ACTIVATED" -eq 1 ]; then
            systemctl stop mcp-gateway-tunnel 2>/dev/null || true
            systemctl stop mcp-gateway-mcp 2>/dev/null || true
            systemctl stop mcp-gateway-admin 2>/dev/null || true
            rm -rf "$INSTALL_DIR"
            restore_system_files || true
        fi
    fi
    if [ "$ACTIVATED" -eq 0 ]; then
        rm -rf "$STAGE" 2>/dev/null || true
    fi
    exit "$rc"
}
trap install_exit 0
trap 'exit 130' HUP INT TERM

echo "[2/9] Staging release tree..."
mkdir -p "$STAGE/bin"
cp -a "$SOURCE_DIR/src" "$STAGE/"
cp -a "$SOURCE_DIR/config" "$STAGE/"
cp "$SOURCE_DIR/bin/mcp-gateway" "$STAGE/bin/"
cp "$SOURCE_DIR/bin/mcp-gateway-client-stdio" "$STAGE/bin/"
cp "$SOURCE_DIR/compatibility.json" "$SOURCE_DIR/manifest.json" "$SOURCE_DIR/requirements.txt" "$SOURCE_DIR/install.sh" "$STAGE/"
[ -f "$SOURCE_DIR/SHA256SUMS" ] && cp "$SOURCE_DIR/SHA256SUMS" "$STAGE/" || true
chmod 0755 "$STAGE/bin/mcp-gateway" "$STAGE/bin/mcp-gateway-client-stdio" "$STAGE/install.sh"
copy_adapter "$STAGE/bin/mcp-gateway-adapter"
validate_adapter "$STAGE/bin/mcp-gateway-adapter"

echo "[3/9] Preparing local secrets and runtime configuration..."
if [ ! -s "$TOKEN_FILE" ]; then
    python3 -c 'import secrets; print(secrets.token_urlsafe(48))' > "$TOKEN_FILE"
    chmod 0600 "$TOKEN_FILE"
    chown "$SERVICE_USER:$SERVICE_GROUP" "$TOKEN_FILE"
fi
if [ ! -s "$ADMIN_ENV" ]; then
    host=$(hostname -s 2>/dev/null || printf 'mcp-pi')
    ip=$(hostname -I 2>/dev/null | awk '{print $1}' || true)
    allowed="127.0.0.1,localhost,${host}"
    [ -n "$ip" ] && allowed="${allowed},${ip}"
    {
        echo "MCP_ADMIN_HOST=0.0.0.0"
        echo "MCP_ADMIN_PORT=80"
        echo "MCP_ADMIN_ALLOWED_HOSTS=${allowed}"
    } > "$ADMIN_ENV"
    chmod 0600 "$ADMIN_ENV"
    chown "$SERVICE_USER:$SERVICE_GROUP" "$ADMIN_ENV"
fi

echo "[4/9] Initializing/preserving Registry..."
DB_PATH="${DATA_DIR}/gateway.db"
if [ -f "$DB_PATH" ]; then
    stamp=$(date -u +%Y%m%dT%H%M%SZ)
    db_backup="${DATA_DIR}/backups/gateway-pre-install-${stamp}.db"
    as_service env MCP_GATEWAY_DB="$DB_PATH" python3 - "$DB_PATH" "$db_backup" <<'PYDB'
import sqlite3, sys
src, dst = sys.argv[1:3]
a = sqlite3.connect(src)
b = sqlite3.connect(dst)
try:
    a.backup(b)
finally:
    b.close(); a.close()
PYDB
    echo "      Registry backup: $db_backup"
fi
as_service env MCP_GATEWAY_DB="$DB_PATH" PYTHONPATH="$STAGE/src" python3 -c "from mcp_gateway.schema import init_db; init_db('${DB_PATH}')"

echo "[5/9] Saving previous runtime and system files..."
rm -rf "$UNIT_BACKUP"
mkdir -p "$UNIT_BACKUP"
chmod 0700 "$UNIT_BACKUP"
for unit in mcp-gateway-admin.service mcp-gateway-mcp.service mcp-gateway-tunnel.service; do
    if [ -f "${SYSTEMD_DIR}/${unit}" ]; then
        cp "${SYSTEMD_DIR}/${unit}" "$UNIT_BACKUP/$unit"
    else
        touch "$UNIT_BACKUP/$unit.absent"
    fi
done
if [ -e "$TUNNEL_CHECK" ] || [ -L "$TUNNEL_CHECK" ]; then
    cp -a "$TUNNEL_CHECK" "$UNIT_BACKUP/mcp-gateway-tunnel-check"
else
    touch "$UNIT_BACKUP/mcp-gateway-tunnel-check.absent"
fi
if [ -e "$CLI_LINK" ] || [ -L "$CLI_LINK" ]; then
    cp -a "$CLI_LINK" "$UNIT_BACKUP/mcp-gateway-cli"
else
    touch "$UNIT_BACKUP/mcp-gateway-cli.absent"
fi
chown -R "$SERVICE_USER:$SERVICE_GROUP" "$UNIT_BACKUP"
rm -rf "$PREVIOUS_DIR"
if [ -d "$INSTALL_DIR" ]; then
    mv "$INSTALL_DIR" "$PREVIOUS_DIR"
fi
mv "$STAGE" "$INSTALL_DIR"
ACTIVATED=1
chown -R "$SERVICE_USER:$SERVICE_GROUP" "$INSTALL_DIR"

echo "[6/9] Installing systemd units..."
for unit in mcp-gateway-admin.service mcp-gateway-mcp.service mcp-gateway-tunnel.service; do
    if [ -f "$INSTALL_DIR/config/systemd/$unit" ]; then
        install -m 0644 "$INSTALL_DIR/config/systemd/$unit" "$SYSTEMD_DIR/$unit"
    fi
done
if [ -f "$INSTALL_DIR/config/systemd/mcp-gateway-tunnel-check" ]; then
    install -m 0755 "$INSTALL_DIR/config/systemd/mcp-gateway-tunnel-check" /usr/local/bin/mcp-gateway-tunnel-check
fi
ln -sf "$INSTALL_DIR/bin/mcp-gateway" "$CLI_LINK"
chmod 0755 "$CLI_LINK"
systemctl daemon-reload
systemctl enable mcp-gateway-admin mcp-gateway-mcp >/dev/null

echo "[7/9] Starting and verifying services..."
restart_runtime

echo "[8/9] Running Doctor..."
as_service "$INSTALL_DIR/bin/mcp-gateway" doctor

echo "[9/9] Initial admin bootstrap..."
if [ "$RUN_SETUP" -eq 1 ] && [ -t 0 ]; then
    as_service "$INSTALL_DIR/bin/mcp-gateway" setup || fail "initial setup did not complete"
else
    echo "      Run: sudo -u ${SERVICE_USER} mcp-gateway setup"
fi

trap - 0 HUP INT TERM
if [ ! -d "$PREVIOUS_DIR" ]; then
    rm -rf "$UNIT_BACKUP"
fi

echo "=================================================="
echo "INSTALLATION_VERIFIED"
echo "Admin configuration: ${ADMIN_ENV}"
echo "Next: open the Admin Console, then add Targets, Projects, clients and grants."
echo "=================================================="
