#!/bin/sh
# Go-only installer/updater for MCP Gateway.
set -eu

fail(){ echo "ERROR: $*" >&2; exit 1; }
warn(){ echo "WARNING: $*" >&2; }

usage() {
cat <<'USAGE'
Usage: ./install.sh [--check] [--rollback] [--no-setup]
  --check      Validate this source/release without changing the system.
  --rollback   Restore previous installer-managed runtime, Registry and units.
  --no-setup   Skip interactive admin bootstrap.
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

SOURCE_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
SERVICE_USER=mcp-gateway
SERVICE_GROUP=mcp-gateway
SERVICE_HOME=/home/mcp-gateway
INSTALL_DIR=${MCP_GATEWAY_INSTALL_DIR:-${SERVICE_HOME}/mcp-gateway}
PREVIOUS_DIR=${SERVICE_HOME}/mcp-gateway.previous-install
DATA_DIR=${SERVICE_HOME}/.local/share/mcp-gateway
CONFIG_DIR=${SERVICE_HOME}/.config/mcp-gateway
UNIT_BACKUP=${DATA_DIR}/install-unit-backup
SYSTEMD_DIR=/etc/systemd/system
POLKIT_DIR=/etc/polkit-1/rules.d
POLKIT_RULE=${POLKIT_DIR}/49-mcp-gateway-reboot.rules
BIN_DIR=/usr/local/bin
TOKEN_FILE=${CONFIG_DIR}/tunnel-mcp.token
ADMIN_ENV=${CONFIG_DIR}/admin.env
TUNNEL_CHECK=${BIN_DIR}/mcp-gateway-tunnel-check
CLI_LINK=${BIN_DIR}/mcp-gateway
DB_PATH=${DATA_DIR}/gateway.db
RUNTIME_UNITS="mcp-gateway-admin.service mcp-gateway-mcp.service mcp-gateway-gemini.service mcp-gateway-tunnel.service mcp-gateway-maintenance.service mcp-gateway-maintenance.timer mcp-gateway-postboot.service"

required_source() {
    for path in         bin/mcp-gateway         bin/mcp-gateway-client-stdio         config/systemd/mcp-gateway-admin.service         config/systemd/mcp-gateway-mcp.service         config/systemd/mcp-gateway-gemini.service         config/systemd/mcp-gateway-tunnel.service         config/systemd/mcp-gateway-maintenance.service         config/systemd/mcp-gateway-maintenance.timer         config/systemd/mcp-gateway-postboot.service         config/systemd/mcp-gateway-tunnel-check         config/polkit/49-mcp-gateway-reboot.rules         compatibility.json manifest.json; do
        [ -e "${SOURCE_DIR}/${path}" ] || fail "release source is missing: ${path}"
    done
}

copy_adapter() {
    dest=$1
    adapter=""
    if [ -n "${MCP_GATEWAY_ADAPTER_BINARY:-}" ]; then
        adapter=${MCP_GATEWAY_ADAPTER_BINARY}
    elif [ -x "${SOURCE_DIR}/bin/mcp-gateway-adapter" ]; then
        adapter=${SOURCE_DIR}/bin/mcp-gateway-adapter
    else
        arch=$(uname -m)
        case "$arch" in
            armv6*) candidate=${SOURCE_DIR}/bin/mcp-gateway-adapter-linux-armv6 ;;
            armv7*) candidate=${SOURCE_DIR}/bin/mcp-gateway-adapter-linux-armv7 ;;
            aarch64|arm64) candidate=${SOURCE_DIR}/bin/mcp-gateway-adapter-linux-arm64 ;;
            x86_64|amd64) candidate=${SOURCE_DIR}/bin/mcp-gateway-adapter-linux-amd64 ;;
            *) candidate="" ;;
        esac
        [ -n "$candidate" ] && [ -x "$candidate" ] && adapter=$candidate
    fi

    if [ -n "$adapter" ]; then
        [ -r "$adapter" ] || fail "gateway binary is not readable: $adapter"
        cp "$adapter" "$dest"
        chmod 0755 "$dest"
        return
    fi

    if command -v go >/dev/null 2>&1 && [ -d "${SOURCE_DIR}/mcp-adapter" ]; then
        arch=$(uname -m)
        case "$arch" in
            armv6*) goarch=arm; goarm=6 ;;
            armv7*) goarch=arm; goarm=7 ;;
            aarch64|arm64) goarch=arm64; goarm= ;;
            x86_64|amd64) goarch=amd64; goarm= ;;
            *) fail "cannot build gateway automatically for architecture: $arch" ;;
        esac
        echo "      No matching prebuilt binary; building because Go is already installed..."
        (
            cd "${SOURCE_DIR}/mcp-adapter"
            if [ -n "$goarm" ]; then
                CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" GOARM="$goarm" go build -trimpath -ldflags='-s -w' -o "$dest" .
            else
                CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" go build -trimpath -ldflags='-s -w' -o "$dest" .
            fi
        )
        chmod 0755 "$dest"
        return
    fi
    fail "no compatible Go binary is present; use an official release bundle or build on a development host"
}

json_string_file() {
    file=$1
    key=$2
    sed -n "s/.*\"${key}\":[[:space:]]*\"\([^\"]*\)\".*/\1/p" "$file" | head -n1
}

json_number_file() {
    file=$1
    key=$2
    sed -n "s/.*\"${key}\":[[:space:]]*\([0-9][0-9]*\).*/\1/p" "$file" | head -n1
}

json_string_text() {
    text=$1
    key=$2
    printf '%s\n' "$text" | sed -n "s/.*\"${key}\":[[:space:]]*\"\([^\"]*\)\".*/\1/p" | head -n1
}

json_number_text() {
    text=$1
    key=$2
    printf '%s\n' "$text" | sed -n "s/.*\"${key}\":[[:space:]]*\([0-9][0-9]*\).*/\1/p" | head -n1
}

verify_source_integrity() {
    if [ -f "${SOURCE_DIR}/SHA256SUMS" ]; then
        command -v sha256sum >/dev/null 2>&1 || fail "sha256sum is required to verify release bundles"
        (cd "$SOURCE_DIR" && sha256sum -c SHA256SUMS) || fail "release bundle checksum verification failed"
    elif [ ! -d "${SOURCE_DIR}/mcp-adapter" ]; then
        fail "release bundle is missing SHA256SUMS"
    fi
}
validate_adapter() {
    adapter=$1
    [ -x "$adapter" ] || fail "gateway binary is not executable: $adapter"
    version_json=$("$adapter" version --json 2>/dev/null) || fail "gateway binary cannot execute on this architecture"

    bin_version=$(json_string_text "$version_json" gateway_version)
    bin_core=$(json_number_text "$version_json" core_api_version)
    bin_bridge=$(json_number_text "$version_json" bridge_api_version)
    bin_tools=$(json_number_text "$version_json" tool_catalog_version)
    bin_schema=$(json_number_text "$version_json" registry_schema_version)
    bin_protocol=$(json_string_text "$version_json" mcp_protocol)

    [ -n "$bin_version" ] && [ -n "$bin_core" ] && [ -n "$bin_bridge" ] &&
        [ -n "$bin_tools" ] && [ -n "$bin_schema" ] && [ -n "$bin_protocol" ] ||
        fail "gateway binary version contract is incomplete"

    [ "$bin_version" = "$(json_string_file "${SOURCE_DIR}/manifest.json" version)" ] ||
        fail "gateway binary version does not match manifest.json"
    [ "$bin_version" = "$(json_string_file "${SOURCE_DIR}/compatibility.json" gateway_version)" ] ||
        fail "gateway binary version does not match compatibility.json"
    [ "$bin_core" = "$(json_number_file "${SOURCE_DIR}/manifest.json" core_api)" ] ||
        fail "Core API does not match manifest.json"
    [ "$bin_core" = "$(json_number_file "${SOURCE_DIR}/compatibility.json" core_api_version)" ] ||
        fail "Core API does not match compatibility.json"
    [ "$bin_bridge" = "$(json_number_file "${SOURCE_DIR}/manifest.json" bridge_api)" ] ||
        fail "Bridge API does not match manifest.json"
    [ "$bin_bridge" = "$(json_number_file "${SOURCE_DIR}/compatibility.json" bridge_api_version)" ] ||
        fail "Bridge API does not match compatibility.json"
    [ "$bin_tools" = "$(json_number_file "${SOURCE_DIR}/manifest.json" tool_catalog)" ] ||
        fail "Tool catalog does not match manifest.json"
    [ "$bin_tools" = "$(json_number_file "${SOURCE_DIR}/compatibility.json" tool_catalog_version)" ] ||
        fail "Tool catalog does not match compatibility.json"
    [ "$bin_schema" = "$(json_number_file "${SOURCE_DIR}/manifest.json" registry_schema)" ] ||
        fail "Registry schema does not match manifest.json"
    [ "$bin_schema" = "$(json_number_file "${SOURCE_DIR}/compatibility.json" registry_schema_version)" ] ||
        fail "Registry schema does not match compatibility.json"
    [ "$bin_protocol" = "$(json_string_file "${SOURCE_DIR}/manifest.json" mcp_protocol)" ] ||
        fail "MCP protocol does not match manifest.json"
    [ "$bin_protocol" = "$(json_string_file "${SOURCE_DIR}/compatibility.json" protocol)" ] ||
        fail "MCP protocol does not match compatibility.json"
}

validate_source() {
    required_source
    for cmd in grep sed head mktemp rm cp chmod uname; do command -v "$cmd" >/dev/null 2>&1 || fail "$cmd is required"; done
    verify_source_integrity
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
        fail "runuser or sudo is required"
    fi
}

wait_url() {
    url=$1
    tries=$2
    i=1
    while [ "$i" -le "$tries" ]; do
        curl -fsS --max-time 2 "$url" >/dev/null 2>&1 && return 0
        i=$((i+1))
        sleep 1
    done
    return 1
}

generate_secret() {
    [ -r /dev/urandom ] || fail "/dev/urandom is required"
    od -An -N48 -tx1 /dev/urandom | tr -d '[:space:]'
    printf '
'
}

save_system_files() {
    rm -rf "$UNIT_BACKUP"
    mkdir -p "$UNIT_BACKUP"
    chmod 0700 "$UNIT_BACKUP"
    for unit in $RUNTIME_UNITS; do
        if [ -f "${SYSTEMD_DIR}/${unit}" ]; then cp "${SYSTEMD_DIR}/${unit}" "$UNIT_BACKUP/$unit"; else touch "$UNIT_BACKUP/$unit.absent"; fi
    done
    if [ -f "$POLKIT_RULE" ]; then cp "$POLKIT_RULE" "$UNIT_BACKUP/49-mcp-gateway-reboot.rules"; else touch "$UNIT_BACKUP/49-mcp-gateway-reboot.rules.absent"; fi
    if [ -e "$TUNNEL_CHECK" ] || [ -L "$TUNNEL_CHECK" ]; then cp -a "$TUNNEL_CHECK" "$UNIT_BACKUP/mcp-gateway-tunnel-check"; else touch "$UNIT_BACKUP/mcp-gateway-tunnel-check.absent"; fi
    if [ -e "$CLI_LINK" ] || [ -L "$CLI_LINK" ]; then cp -a "$CLI_LINK" "$UNIT_BACKUP/mcp-gateway-cli"; else touch "$UNIT_BACKUP/mcp-gateway-cli.absent"; fi
}

restore_system_files() {
    [ -d "$UNIT_BACKUP" ] || return 0
    for unit in $RUNTIME_UNITS; do
        if [ -f "$UNIT_BACKUP/$unit" ]; then
            install -m 0644 "$UNIT_BACKUP/$unit" "${SYSTEMD_DIR}/${unit}"
        elif [ -f "$UNIT_BACKUP/$unit.absent" ]; then
            systemctl disable "$unit" >/dev/null 2>&1 || true
            rm -f "${SYSTEMD_DIR}/${unit}"
        fi
    done
    if [ -d "$POLKIT_DIR" ]; then
        if [ -f "$UNIT_BACKUP/49-mcp-gateway-reboot.rules" ]; then
            install -o root -g root -m 0644 "$UNIT_BACKUP/49-mcp-gateway-reboot.rules" "$POLKIT_RULE"
        elif [ -f "$UNIT_BACKUP/49-mcp-gateway-reboot.rules.absent" ]; then
            rm -f "$POLKIT_RULE"
        fi
    fi
    if [ -f "$UNIT_BACKUP/mcp-gateway-tunnel-check" ]; then install -m 0755 "$UNIT_BACKUP/mcp-gateway-tunnel-check" "$TUNNEL_CHECK"; elif [ -f "$UNIT_BACKUP/mcp-gateway-tunnel-check.absent" ]; then rm -f "$TUNNEL_CHECK"; fi
    if [ -e "$UNIT_BACKUP/mcp-gateway-cli" ] || [ -L "$UNIT_BACKUP/mcp-gateway-cli" ]; then rm -f "$CLI_LINK"; cp -a "$UNIT_BACKUP/mcp-gateway-cli" "$CLI_LINK"; elif [ -f "$UNIT_BACKUP/mcp-gateway-cli.absent" ]; then rm -f "$CLI_LINK"; fi
    systemctl daemon-reload
}

restart_runtime() {
    systemctl reset-failed mcp-gateway-admin mcp-gateway-mcp mcp-gateway-gemini mcp-gateway-tunnel mcp-gateway-postboot 2>/dev/null || true
    systemctl restart mcp-gateway-admin
    wait_url http://127.0.0.1/login 45 || fail "mcp-gateway-admin did not become ready"
    systemctl restart mcp-gateway-mcp
    wait_url http://127.0.0.1:8090/ready 60 || fail "mcp-gateway-mcp did not become ready"
    if systemctl is-enabled --quiet mcp-gateway-gemini 2>/dev/null; then
        if [ -s "${CONFIG_DIR}/gemini-mcp.token" ]; then
            systemctl restart mcp-gateway-gemini
            wait_url http://127.0.0.1:8092/ready 60 || fail "mcp-gateway-gemini did not become ready"
        else
            warn "Gemini MCP service is enabled but gemini-mcp.token is missing; leaving it stopped"
            systemctl stop mcp-gateway-gemini 2>/dev/null || true
        fi
    fi
    systemctl restart mcp-gateway-postboot.service
    systemctl start mcp-gateway-maintenance.timer
    if systemctl is-enabled --quiet mcp-gateway-tunnel 2>/dev/null; then
        if [ -s "${CONFIG_DIR}/tunnel.env" ] && [ -x /usr/local/bin/openai-tunnel-client ]; then systemctl restart mcp-gateway-tunnel; else warn "tunnel is enabled but credentials/client are incomplete; leaving it stopped"; systemctl stop mcp-gateway-tunnel 2>/dev/null || true; fi
    fi
}

restore_registry_for_rollback() {
    [ -d "$UNIT_BACKUP" ] || return 0
    if [ -f "$UNIT_BACKUP/registry.absent" ]; then
        rm -f "$DB_PATH" "$DB_PATH-shm" "$DB_PATH-wal"
        return
    fi
    [ -f "$UNIT_BACKUP/registry-backup.path" ] || return 0
    registry_backup=$(cat "$UNIT_BACKUP/registry-backup.path")
    [ -s "$registry_backup" ] || fail "rollback Registry backup is missing: $registry_backup"
    [ -x "$INSTALL_DIR/bin/mcp-gateway-adapter" ] || fail "current Go binary is required to restore Registry"
    as_service "$INSTALL_DIR/bin/mcp-gateway-adapter" restore -db "$DB_PATH" "$registry_backup" >/dev/null
}

rollback_runtime() {
    echo "ROLLBACK: restoring previous installer-managed runtime..." >&2
    systemctl stop mcp-gateway-maintenance.timer 2>/dev/null || true
    systemctl stop mcp-gateway-maintenance.service 2>/dev/null || true
    systemctl stop mcp-gateway-tunnel 2>/dev/null || true
    systemctl stop mcp-gateway-gemini 2>/dev/null || true
    systemctl stop mcp-gateway-postboot 2>/dev/null || true
    systemctl stop mcp-gateway-mcp 2>/dev/null || true
    systemctl stop mcp-gateway-admin 2>/dev/null || true
    restore_registry_for_rollback
    failed="${SERVICE_HOME}/mcp-gateway.failed-install.$(date -u +%Y%m%dT%H%M%SZ)"
    [ ! -d "$INSTALL_DIR" ] || mv "$INSTALL_DIR" "$failed"
    [ -d "$PREVIOUS_DIR" ] || fail "previous installer-managed runtime is missing: $PREVIOUS_DIR"
    mv "$PREVIOUS_DIR" "$INSTALL_DIR"
    restore_system_files
    restart_runtime
    as_service "$INSTALL_DIR/bin/mcp-gateway" doctor -db "$DB_PATH" >/dev/null
    rm -rf "$failed" "$UNIT_BACKUP"
    echo "ROLLBACK_VERIFIED"
}

if [ "$MODE" = check ]; then
    echo "=== MCP Gateway Go-only release/install preflight ==="
    validate_source
    echo "INSTALL_CHECK=PASS"
    exit 0
fi

[ "$(id -u)" = "0" ] || fail "install.sh must run as root"
for cmd in systemctl useradd install cp mv rm mktemp touch curl od tr grep sed head find; do command -v "$cmd" >/dev/null 2>&1 || fail "$cmd is required"; done
if [ "$MODE" = rollback ]; then rollback_runtime; exit 0; fi

required_source
verify_source_integrity
arch=$(uname -m)
case "$arch" in armv6*|armv7*|aarch64|arm64|x86_64|amd64) ;; *) warn "architecture $arch is not in the verified compatibility set" ;; esac

echo "=================================================="
echo "=== MCP Gateway Go-only Installer / Updater     ==="
echo "=================================================="
echo "Source: ${SOURCE_DIR}"
echo "Target: ${INSTALL_DIR}"

if id -u "$SERVICE_USER" >/dev/null 2>&1; then echo "[1/9] Service user exists."; else echo "[1/9] Creating service user ${SERVICE_USER}..."; useradd -r -s /bin/bash -m -d "$SERVICE_HOME" "$SERVICE_USER"; fi

mkdir -p "$DATA_DIR/backups" "$CONFIG_DIR"
chmod 0700 "$DATA_DIR" "$CONFIG_DIR"
chown -R "$SERVICE_USER:$SERVICE_GROUP" "$DATA_DIR" "$CONFIG_DIR"

STAGE=$(mktemp -d "${SERVICE_HOME}/mcp-gateway.install.XXXXXX")
ACTIVATED=0
CONTROL_PLANE_STOPPED=0
install_exit() {
    rc=$?
    trap - 0 HUP INT TERM
    if [ "$rc" -ne 0 ]; then
        if [ "$ACTIVATED" -eq 1 ] && [ -d "$PREVIOUS_DIR" ]; then
            rollback_runtime
        elif [ "$ACTIVATED" -eq 1 ]; then
            systemctl stop mcp-gateway-maintenance.timer mcp-gateway-maintenance.service mcp-gateway-tunnel mcp-gateway-gemini mcp-gateway-postboot mcp-gateway-mcp mcp-gateway-admin 2>/dev/null || true
            restore_registry_for_rollback
            rm -rf "$INSTALL_DIR"
            restore_system_files
        elif [ "$CONTROL_PLANE_STOPPED" -eq 1 ] && [ -d "$PREVIOUS_DIR" ]; then
            [ -d "$INSTALL_DIR" ] || mv "$PREVIOUS_DIR" "$INSTALL_DIR"
            restore_system_files || true
            restart_runtime
        elif [ "$CONTROL_PLANE_STOPPED" -eq 1 ] && [ -d "$INSTALL_DIR" ]; then
            restart_runtime || true
        fi
    fi
    [ "$ACTIVATED" -ne 0 ] || rm -rf "$STAGE" 2>/dev/null || true
    exit "$rc"
}
trap install_exit 0
trap 'exit 130' HUP INT TERM

echo "[2/9] Staging Go-only release tree..."
chmod 0755 "$STAGE"
mkdir -p "$STAGE/bin"
cp -a "$SOURCE_DIR/config" "$STAGE/"
cp "$SOURCE_DIR/bin/mcp-gateway" "$SOURCE_DIR/bin/mcp-gateway-client-stdio" "$STAGE/bin/"
cp "$SOURCE_DIR/compatibility.json" "$SOURCE_DIR/manifest.json" "$SOURCE_DIR/install.sh" "$STAGE/"
chmod 0755 "$STAGE/bin/mcp-gateway" "$STAGE/bin/mcp-gateway-client-stdio" "$STAGE/install.sh"
copy_adapter "$STAGE/bin/mcp-gateway-adapter"
validate_adapter "$STAGE/bin/mcp-gateway-adapter"

echo "[3/9] Preparing local secrets and safe runtime defaults..."
if [ ! -s "$TOKEN_FILE" ]; then umask 077; generate_secret > "$TOKEN_FILE"; chmod 0600 "$TOKEN_FILE"; chown "$SERVICE_USER:$SERVICE_GROUP" "$TOKEN_FILE"; fi
if [ ! -s "$ADMIN_ENV" ]; then
    host=$(hostname -s 2>/dev/null || printf 'mcp-pi')
    { echo "MCP_ADMIN_HOST=127.0.0.1"; echo "MCP_ADMIN_PORT=80"; echo "MCP_ADMIN_ALLOWED_HOSTS=127.0.0.1,localhost,${host}"; } > "$ADMIN_ENV"
    chmod 0600 "$ADMIN_ENV"
    chown "$SERVICE_USER:$SERVICE_GROUP" "$ADMIN_ENV"
fi

echo "[4/9] Backing up Registry with Go (no migration while services are active)..."
save_system_files
if [ -f "$DB_PATH" ]; then
    stamp=$(date -u +%Y%m%dT%H%M%SZ)
    db_backup="${DATA_DIR}/backups/gateway-pre-install-${stamp}.db"
    backup_output=$(as_service "$STAGE/bin/mcp-gateway-adapter" backup -db "$DB_PATH" "$db_backup")
    printf '%s\n' "$backup_output" | grep -Eq 'SQLite integrity: ok; schema: (4|5);' || fail "Registry backup has unsupported schema or failed integrity validation"
    printf '%s\n' "$db_backup" > "$UNIT_BACKUP/registry-backup.path"
    echo "      Registry backup: $db_backup"
else
    touch "$UNIT_BACKUP/registry.absent"
fi

echo "[5/9] Activating root-owned runtime and migrating Registry with services stopped..."
systemctl stop mcp-gateway-maintenance.timer mcp-gateway-maintenance.service mcp-gateway-tunnel mcp-gateway-gemini mcp-gateway-postboot mcp-gateway-mcp mcp-gateway-admin 2>/dev/null || true
CONTROL_PLANE_STOPPED=1
rm -rf "$PREVIOUS_DIR"
[ ! -d "$INSTALL_DIR" ] || mv "$INSTALL_DIR" "$PREVIOUS_DIR"
mv "$STAGE" "$INSTALL_DIR"
ACTIVATED=1
chown -R root:root "$INSTALL_DIR"
find "$INSTALL_DIR" -type d -exec chmod 0755 {} +
chmod 0755 "$INSTALL_DIR/bin/mcp-gateway" "$INSTALL_DIR/bin/mcp-gateway-client-stdio" "$INSTALL_DIR/bin/mcp-gateway-adapter" "$INSTALL_DIR/install.sh"
as_service "$INSTALL_DIR/bin/mcp-gateway-adapter" migrate -db "$DB_PATH"
as_service "$INSTALL_DIR/bin/mcp-gateway-adapter" status -db "$DB_PATH" >/dev/null

echo "[6/9] Installing systemd and least-privilege policy assets..."
for unit in $RUNTIME_UNITS; do install -o root -g root -m 0644 "$INSTALL_DIR/config/systemd/$unit" "${SYSTEMD_DIR}/${unit}"; done
install -o root -g root -m 0755 "$INSTALL_DIR/config/systemd/mcp-gateway-tunnel-check" "$TUNNEL_CHECK"
if [ -d "$POLKIT_DIR" ]; then install -o root -g root -m 0644 "$INSTALL_DIR/config/polkit/49-mcp-gateway-reboot.rules" "$POLKIT_RULE"; else warn "polkit rules directory unavailable; gateway_reboot remains fail-closed"; fi
ln -sf "$INSTALL_DIR/bin/mcp-gateway" "$CLI_LINK"
systemctl daemon-reload
systemctl enable mcp-gateway-admin mcp-gateway-mcp mcp-gateway-maintenance.timer mcp-gateway-postboot.service >/dev/null

echo "[7/9] Starting and verifying Go services..."
restart_runtime

echo "[8/9] Running Doctor..."
as_service "$INSTALL_DIR/bin/mcp-gateway" doctor -db "$DB_PATH"

echo "[9/9] Initial admin bootstrap..."
if [ "$RUN_SETUP" -eq 1 ] && [ -t 0 ]; then
    as_service "$INSTALL_DIR/bin/mcp-gateway" setup -db "$DB_PATH" || fail "initial setup did not complete"
else
    echo "      Run: sudo -u ${SERVICE_USER} mcp-gateway setup -db ${DB_PATH}"
fi

trap - 0 HUP INT TERM
echo "INSTALL_VERIFIED"
echo "runtime=go-only"
echo "admin_bind_default=127.0.0.1"
