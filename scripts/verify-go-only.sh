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

if rg -n --glob '!**/*_test.go'   '(^|[;&|[:space:]])python(3)?([[:space:]]|$)|PYTHONPATH'   "${PRODUCT_PATHS[@]}"; then
  fail "active product/lifecycle path executes Python"
fi

legacy_refs="$(rg -n --glob '!**/*_test.go' 'src/mcp_gateway|mcp_gateway\.' "${PRODUCT_PATHS[@]}" || true)"
legacy_refs="$(printf '%s\n' "$legacy_refs" |
  grep -v 'Go-only release invariant failed' |
  grep -v 'legacy Python runtime found' || true)"
if [ -n "$legacy_refs" ]; then
  printf '%s\n' "$legacy_refs"
  fail "active product/lifecycle path references legacy Python modules"
fi

rg -q '"implementation"[[:space:]]*:[[:space:]]*"go"' compatibility.json ||
  fail "compatibility.json does not declare Go runtime"
rg -q '"runtime"[[:space:]]*:[[:space:]]*"go"' manifest.json ||
  fail "manifest.json does not declare Go runtime"
if rg -q 'minimum_python|"python"[[:space:]]*:' compatibility.json manifest.json; then
  fail "active metadata still declares Python runtime requirements"
fi

rg -q 'MCP_ADMIN_HOST=127\.0\.0\.1' install.sh ||
  fail "installer does not default Admin to loopback"
if rg -q 'MCP_ADMIN_HOST=0\.0\.0\.0' install.sh scripts/deploy-pi.sh; then
  fail "lifecycle reintroduces wildcard Admin bind"
fi

if rg -q 'chown -R[[:space:]]+mcp-gateway' install.sh scripts/deploy-pi.sh; then
  fail "runtime ownership is writable by the service account"
fi

if rg -q -- '-python(path)?([[:space:]]|$)' mcp-adapter bin; then
  fail "legacy Python bridge flags remain active"
fi

echo "GO_ONLY_GATE=PASS"
