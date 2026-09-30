#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

fail() { echo "GO_ONLY_GATE=FAIL: $*" >&2; exit 1; }

PRODUCT_PATHS=(
  bin
  install.sh
  mcp-adapter
  config/systemd
  config/polkit
  scripts/deploy-pi.sh
  scripts/build-release-package.sh
  scripts/run-resumable.sh
)

for path in "${PRODUCT_PATHS[@]}"; do
  [ -e "$path" ] || fail "required product path is missing: $path"
done

tracked_python="$(git ls-files '*.py' 'requirements.txt' 'pytest.ini' || true)"
if [ -n "$tracked_python" ]; then
  printf '%s\n' "$tracked_python"
  fail "tracked Python implementation/test files remain in the active repository"
fi

if grep -R -I -n -E --exclude='*_test.go'   '(^|[;&|[:space:]])python(3)?([[:space:]]|$)|PYTHONPATH'   "${PRODUCT_PATHS[@]}"; then
  fail "active product/lifecycle path executes Python"
fi

legacy_refs="$(grep -R -I -n -E --exclude='*_test.go'   'src/mcp_gateway|mcp_gateway\.' "${PRODUCT_PATHS[@]}" || true)"
legacy_refs="$(printf '%s\n' "$legacy_refs" |
  grep -v 'Go-only release invariant failed' |
  grep -v 'legacy Python runtime found' || true)"
if [ -n "$legacy_refs" ]; then
  printf '%s\n' "$legacy_refs"
  fail "active product/lifecycle path references legacy Python modules"
fi

grep -Eq '"implementation"[[:space:]]*:[[:space:]]*"go"' compatibility.json ||
  fail "compatibility.json does not declare Go runtime"
grep -Eq '"runtime"[[:space:]]*:[[:space:]]*"go"' manifest.json ||
  fail "manifest.json does not declare Go runtime"
if grep -Eq 'minimum_python|"python"[[:space:]]*:' compatibility.json manifest.json; then
  fail "active metadata still declares Python runtime requirements"
fi

grep -Eq 'host = "127\.0\.0\.1"' mcp-adapter/cli.go ||
  fail "Go Admin runtime does not default to loopback"
if grep -Eq '^Environment=MCP_ADMIN_HOST=' config/systemd/mcp-gateway-admin.service; then
  fail "Admin systemd unit duplicates the Go bind default"
fi
if grep -Eq 'MCP_ADMIN_HOST=0\.0\.0\.0' install.sh scripts/deploy-pi.sh config/systemd/mcp-gateway-admin.service; then
  fail "lifecycle reintroduces wildcard Admin bind"
fi

[ -f config/systemd/mcp-gateway.target ] ||
  fail "canonical appliance lifecycle target is missing"
grep -Eq '^PartOf=mcp-gateway[.]target$' config/systemd/mcp-gateway-admin.service ||
  fail "Admin service is not attached to the appliance lifecycle target"
grep -Eq '^PartOf=mcp-gateway[.]target$' config/systemd/mcp-gateway-mcp.service ||
  fail "MCP service is not attached to the appliance lifecycle target"
if grep -Eq 'MCP_PI_HOST:-[0-9]|MCP_PI_USER:-[A-Za-z0-9]' scripts/deploy-pi.sh; then
  fail "deployment-specific host/user defaults remain embedded in deploy-pi.sh"
fi

if grep -Eq 'chown -R[[:space:]]+mcp-gateway' install.sh scripts/deploy-pi.sh; then
  fail "runtime ownership is writable by the service account"
fi

if git grep -I -q -E -- '-python(path)?([[:space:]]|$)' -- mcp-adapter bin; then
  fail "legacy Python bridge flags remain active"
fi

echo "GO_ONLY_GATE=PASS"
