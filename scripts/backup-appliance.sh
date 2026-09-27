#!/usr/bin/env bash
# Go-only disaster-recovery backup for the deployed MCP-Pi appliance.
set -Eeuo pipefail

fail(){ echo "ERROR: $*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || fail "backup-appliance.sh must be executed as root"

DEST_DIR="${1:-/home/yorologo/backups}"
TIMESTAMP="$(date -u +%Y%m%dT%H%M%SZ)"
SERVICE_USER=mcp-gateway
SERVICE_GROUP=mcp-gateway
RUNTIME_DIR=/home/mcp-gateway/mcp-gateway
DATA_DIR=/home/mcp-gateway/.local/share/mcp-gateway
CONFIG_DIR=/home/mcp-gateway/.config/mcp-gateway
DB_SRC="$DATA_DIR/gateway.db"
GATEWAY_BIN="$RUNTIME_DIR/bin/mcp-gateway-adapter"
BACKUP_AGE_RECIPIENT="${BACKUP_AGE_RECIPIENT:-}"

[ -x "$GATEWAY_BIN" ] || fail "deployed Go gateway binary is missing: $GATEWAY_BIN"
for cmd in tar sha256sum stat find sort ssh-keygen df mktemp grep sed head; do
    command -v "$cmd" >/dev/null 2>&1 || fail "$cmd is required"
done
if [ -n "$BACKUP_AGE_RECIPIENT" ] && ! command -v age >/dev/null 2>&1; then
    fail "BACKUP_AGE_RECIPIENT is set but the optional 'age' binary is not installed; refusing plaintext fallback"
fi

as_service() {
    if command -v runuser >/dev/null 2>&1; then
        runuser -u "$SERVICE_USER" -- "$@"
    elif command -v sudo >/dev/null 2>&1; then
        sudo -u "$SERVICE_USER" "$@"
    else
        fail "runuser or sudo is required"
    fi
}

VERSION_JSON="$("$GATEWAY_BIN" version --json)"
RELEASE_VERSION="$(printf '%s\n' "$VERSION_JSON" | sed -n 's/.*"gateway_version":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1)"
SCHEMA_VERSION="$(printf '%s\n' "$VERSION_JSON" | sed -n 's/.*"registry_schema_version":[[:space:]]*\([0-9][0-9]*\).*/\1/p' | head -n1)"
[ -n "$RELEASE_VERSION" ] || fail "gateway version contract did not contain gateway_version"
[ -n "$SCHEMA_VERSION" ] || fail "gateway version contract did not contain registry_schema_version"
RELEASE_TAG="v$RELEASE_VERSION"
ARCHIVE_NAME="mcp-pi-${RELEASE_TAG}-${TIMESTAMP}.tar.gz"
ARCHIVE_PATH="$DEST_DIR/$ARCHIVE_NAME"

echo "=== MCP-Pi Appliance Backup ==="
echo "Timestamp:   $TIMESTAMP (UTC)"
echo "Release:     $RELEASE_TAG"
echo "Runtime:     Go-only"
echo "Destination: $DEST_DIR"

mkdir -p "$DEST_DIR"
chmod 0700 "$DEST_DIR"
chown yorologo:yorologo "$DEST_DIR" 2>/dev/null || true

AVAILABLE_KB="$(df -k --output=avail "$DEST_DIR" | tail -n1 | tr -dc '0-9')"
[ "${AVAILABLE_KB:-0}" -ge 51200 ] ||
    fail "insufficient disk space at $DEST_DIR (available ${AVAILABLE_KB:-0} KB; require 51200 KB)"

STAGING_DIR="$(mktemp -d /tmp/mcp-pi-backup-staging.XXXXXX)"
trap 'rm -rf "$STAGING_DIR"; [ -z "${LIVE_DB_TMP:-}" ] || rm -f "$LIVE_DB_TMP"' EXIT
chmod 0700 "$STAGING_DIR"
mkdir -p "$STAGING_DIR"/{data,config,gateway-ssh,host-ssh,admin-ssh,systemd,polkit}

# SQLite is copied through the gateway's existing Online Backup API. The
# service user writes to its own backup directory; root then moves the verified
# snapshot into the private staging tree.
DB_SHA=""
if [ -f "$DB_SRC" ]; then
    LIVE_DB_TMP="$DATA_DIR/backups/disaster-recovery-$TIMESTAMP.db"
    install -d -o "$SERVICE_USER" -g "$SERVICE_GROUP" -m 0700 "$DATA_DIR/backups"
    as_service "$GATEWAY_BIN" backup -db "$DB_SRC" "$LIVE_DB_TMP" >/dev/null
    "$GATEWAY_BIN" status -db "$LIVE_DB_TMP" >/dev/null
    cp "$LIVE_DB_TMP" "$STAGING_DIR/data/gateway.db"
    chmod 0600 "$STAGING_DIR/data/gateway.db"
    DB_SHA="$(sha256sum "$STAGING_DIR/data/gateway.db" | awk '{print $1}')"
    rm -f "$LIVE_DB_TMP"
    LIVE_DB_TMP=""
else
    echo "WARNING: Registry not found: $DB_SRC" >&2
fi

# Stateful configuration and identities required for disaster recovery.
if [ -d "$CONFIG_DIR" ]; then
    for f in admin-secret tunnel.env tunnel-mcp.token; do
        [ ! -f "$CONFIG_DIR/$f" ] || cp -p "$CONFIG_DIR/$f" "$STAGING_DIR/config/"
    done
fi
if [ -d /home/mcp-gateway/.ssh ]; then
    for f in mcp_gateway_ed25519 mcp_gateway_ed25519.pub known_hosts config; do
        [ ! -f "/home/mcp-gateway/.ssh/$f" ] || cp -p "/home/mcp-gateway/.ssh/$f" "$STAGING_DIR/gateway-ssh/"
    done
fi
for f in /etc/ssh/ssh_host_*; do
    [ ! -f "$f" ] || cp -p "$f" "$STAGING_DIR/host-ssh/"
done
if [ -f /home/yorologo/.ssh/authorized_keys ]; then
    cp -p /home/yorologo/.ssh/authorized_keys "$STAGING_DIR/admin-ssh/"
    chmod 0600 "$STAGING_DIR/admin-ssh/authorized_keys"
fi
for u in /etc/systemd/system/mcp-gateway*.service /etc/systemd/system/mcp-gateway*.timer; do
    [ ! -f "$u" ] || cp -p "$u" "$STAGING_DIR/systemd/"
done
if [ -f /etc/polkit-1/rules.d/49-mcp-gateway-reboot.rules ]; then
    cp -p /etc/polkit-1/rules.d/49-mcp-gateway-reboot.rules "$STAGING_DIR/polkit/"
fi

# Fingerprints are stored as plain text so arbitrary SSH key comments never
# need custom JSON escaping.
{
    echo "host_ed25519:"
    [ ! -f "$STAGING_DIR/host-ssh/ssh_host_ed25519_key.pub" ] ||
        ssh-keygen -lf "$STAGING_DIR/host-ssh/ssh_host_ed25519_key.pub"
    echo "gateway_client:"
    [ ! -f "$STAGING_DIR/gateway-ssh/mcp_gateway_ed25519.pub" ] ||
        ssh-keygen -lf "$STAGING_DIR/gateway-ssh/mcp_gateway_ed25519.pub"
    echo "target_pins:"
    [ ! -f "$STAGING_DIR/gateway-ssh/known_hosts" ] ||
        ssh-keygen -lf "$STAGING_DIR/gateway-ssh/known_hosts" || true
} > "$STAGING_DIR/fingerprints.txt"

# Manifest fields are deliberately limited to values generated by this script
# or the canonical Go version contract.
cat > "$STAGING_DIR/manifest.json" <<EOF
{
  "appliance": "MCP-Pi Gateway",
  "release": "$RELEASE_TAG",
  "backup_timestamp_utc": "$TIMESTAMP",
  "runtime": "go-only",
  "database": {
    "present": $([ -n "$DB_SHA" ] && printf true || printf false),
    "integrity": "$([ -n "$DB_SHA" ] && printf ok || printf absent)",
    "user_version": $SCHEMA_VERSION,
    "sha256": "$DB_SHA"
  },
  "inventory": "SHA256SUMS",
  "file_metadata": "FILE-METADATA.tsv",
  "fingerprints": "fingerprints.txt"
}
EOF

(
    cd "$STAGING_DIR"
    find . -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 sha256sum > SHA256SUMS
    {
        printf 'path\tmode\tuid\tgid\tsize\n'
        while IFS= read -r -d '' file; do
            stat -c '%n\t%a\t%u\t%g\t%s' "$file"
        done < <(find . -type f -print0 | sort -z)
    } > FILE-METADATA.tsv
)

echo "Packaging private backup archive..."
tar -czf "$ARCHIVE_PATH" -C "$STAGING_DIR" .
chmod 0600 "$ARCHIVE_PATH"
chown yorologo:yorologo "$ARCHIVE_PATH" 2>/dev/null || true

MANIFEST_PATH="$DEST_DIR/manifest-${RELEASE_TAG}-${TIMESTAMP}.json"
tar -xzf "$ARCHIVE_PATH" ./manifest.json -O > "$MANIFEST_PATH"
chmod 0644 "$MANIFEST_PATH"
chown yorologo:yorologo "$MANIFEST_PATH" 2>/dev/null || true

OUTPUT_PATH="$ARCHIVE_PATH"
if [ -n "$BACKUP_AGE_RECIPIENT" ]; then
    ENCRYPTED_PATH="$ARCHIVE_PATH.age"
    echo "Encrypting backup with age for configured recipient..."
    age -r "$BACKUP_AGE_RECIPIENT" -o "$ENCRYPTED_PATH" "$ARCHIVE_PATH"
    chmod 0600 "$ENCRYPTED_PATH"
    chown yorologo:yorologo "$ENCRYPTED_PATH" 2>/dev/null || true
    rm -f "$ARCHIVE_PATH"
    OUTPUT_PATH="$ENCRYPTED_PATH"
fi

OUTPUT_NAME="$(basename "$OUTPUT_PATH")"
SHA_PATH="$OUTPUT_PATH.sha256"
(cd "$DEST_DIR" && sha256sum "$OUTPUT_NAME" > "$OUTPUT_NAME.sha256")
chmod 0644 "$SHA_PATH"
chown yorologo:yorologo "$SHA_PATH" 2>/dev/null || true

ARCHIVE_SIZE="$(stat -c%s "$OUTPUT_PATH")"
ARCHIVE_SHA256="$(cut -d' ' -f1 "$SHA_PATH")"
echo "=== Backup Complete ==="
echo "Archive:   $OUTPUT_PATH"
echo "Encrypted: $([ -n "$BACKUP_AGE_RECIPIENT" ] && echo yes || echo no)"
echo "Size:      $ARCHIVE_SIZE bytes"
echo "SHA256:    $ARCHIVE_SHA256"
echo "Manifest:  $MANIFEST_PATH"
