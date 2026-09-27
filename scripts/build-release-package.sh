#!/usr/bin/env bash
set -Eeuo pipefail

# Build an immutable Go-only ARMv6 release bundle from one exact Git commit.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
SHA_INPUT="${1:-HEAD}"
OUTPUT_DIR="${2:-${ROOT}/dist}"

fail(){ echo "ERROR: $*" >&2; exit 1; }

for cmd in git go tar gzip sha256sum awk grep find; do
    command -v "$cmd" >/dev/null 2>&1 || fail "$cmd is required"
done

SHA="$(git -C "$ROOT" rev-parse "${SHA_INPUT}^{commit}")"
HEAD_SHA="$(git -C "$ROOT" rev-parse HEAD)"
[ "$SHA" = "$HEAD_SHA" ] || fail "release package commit must equal local HEAD ($HEAD_SHA)"
[ -z "$(git -C "$ROOT" status --porcelain --untracked-files=normal)" ] || fail "release packaging requires a clean worktree"

VERSION="$(git -C "$ROOT" show "$SHA:mcp-adapter/internal/buildinfo/buildinfo.go" | awk -F'"' '/GatewayVersion[[:space:]]*=/{print $2; exit}')"
[ -n "$VERSION" ] || fail "could not read canonical GatewayVersion from Go buildinfo"
SHORT="${SHA:0:12}"
TMP_BASE="${TMPDIR:-${PREFIX:-/tmp}/tmp}"
mkdir -p "$TMP_BASE" "$OUTPUT_DIR"
TMP="$(mktemp -d "$TMP_BASE/mcp-release.XXXXXX")"
trap 'rm -rf "$TMP"' EXIT
BUILD_ROOT="$TMP/build"
PKG_ROOT="$TMP/MCP-Pi-$VERSION"
mkdir -p "$BUILD_ROOT" "$PKG_ROOT/bin"

# Build source is isolated from the distributed package.
git -C "$ROOT" archive "$SHA" mcp-adapter | tar -xf - -C "$BUILD_ROOT"
(
    cd "$BUILD_ROOT/mcp-adapter"
    CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=6 \
        go build -trimpath \
        -ldflags="-s -w -X mcp-gateway-adapter/internal/buildinfo.Commit=$SHA" \
        -o "$PKG_ROOT/bin/mcp-gateway-adapter" .
)

# Runtime/package allowlist. Development Python or Go source is never copied.
for path in \
    README.md CHANGELOG.md compatibility.json manifest.json install.sh \
    bin/mcp-gateway bin/mcp-gateway-client-stdio; do
    git -C "$ROOT" show "$SHA:$path" > "$PKG_ROOT/$path"
done
mkdir -p "$PKG_ROOT/config"
git -C "$ROOT" archive "$SHA" config | tar -xf - -C "$PKG_ROOT"

mkdir -p "$PKG_ROOT/docs"
for doc in \
    installation.md configuration.md operations.md recovery.md \
    security.md troubleshooting.md admin-console.md architecture.md; do
    if git -C "$ROOT" cat-file -e "$SHA:docs/$doc" 2>/dev/null; then
        git -C "$ROOT" show "$SHA:docs/$doc" > "$PKG_ROOT/docs/$doc"
    fi
done

# Normalize package modes so the artifact is independent of the caller's umask.
chmod -R u=rwX,go=rX "$PKG_ROOT"
chmod 0755 "$PKG_ROOT/install.sh" "$PKG_ROOT/bin/mcp-gateway" \
    "$PKG_ROOT/bin/mcp-gateway-client-stdio" "$PKG_ROOT/bin/mcp-gateway-adapter" \
    "$PKG_ROOT/config/systemd/mcp-gateway-tunnel-check"
sh -n "$PKG_ROOT/install.sh"
if find "$PKG_ROOT" -type d ! -perm -0005 -print -quit | grep -q .; then
    fail "release package contains a directory that is not traversable by the service account"
fi

# Product invariant: the distributed appliance bundle contains no Python payload.
if find "$PKG_ROOT" -type f \( -name '*.py' -o -name 'requirements.txt' \) -print -quit | grep -q .; then
    fail "Go-only release invariant failed: Python payload found"
fi
[ ! -e "$PKG_ROOT/src/mcp_gateway" ] || fail "Go-only release invariant failed: legacy Python runtime found"

(
    cd "$PKG_ROOT"
    find . -type f ! -name SHA256SUMS -print0 |
        sort -z |
        xargs -0 sha256sum > SHA256SUMS
)

SOURCE_DATE_EPOCH="$(git -C "$ROOT" show -s --format=%ct "$SHA")"
OUT="$OUTPUT_DIR/MCP-Pi-$VERSION-linux-armv6-$SHORT.tar.gz"
tar -C "$TMP" --sort=name --mtime="@${SOURCE_DATE_EPOCH}" --owner=0 --group=0 --numeric-owner \
    -cf - "$(basename "$PKG_ROOT")" | gzip -n -9 > "$OUT"

PACKAGE_SHA="$(sha256sum "$OUT" | awk '{print $1}')"
ADAPTER_SHA="$(sha256sum "$PKG_ROOT/bin/mcp-gateway-adapter" | awk '{print $1}')"

echo "RELEASE_PACKAGE_BUILT"
echo "version=$VERSION"
echo "commit=$SHA"
echo "package=$OUT"
echo "package_sha256=$PACKAGE_SHA"
echo "adapter_sha256=$ADAPTER_SHA"
echo "runtime=go-only"
