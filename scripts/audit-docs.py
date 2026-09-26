#!/usr/bin/env python3
"""KISS semantic documentation contract for the CURRENT MCP-Pi surface."""
from __future__ import annotations

import json
import os
import re
import sqlite3
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DOCS = ROOT / "docs"

CURRENT_FILES = [
    ROOT / "README.md",
    ROOT / "AGENTS.md",
    ROOT / "CONTRIBUTING.md",
    DOCS / "README.md",
    DOCS / "getting-started.md",
    DOCS / "installation.md",
    DOCS / "configuration.md",
    DOCS / "operations.md",
    DOCS / "update-rollback.md",
    DOCS / "recovery.md",
    DOCS / "troubleshooting.md",
    DOCS / "architecture.md",
    DOCS / "security.md",
    DOCS / "admin-console.md",
    DOCS / "project-state.md",
]

CONTEXT_FILES = [
    ROOT / "CHANGELOG.md",
    DOCS / "releases" / "v1.3.3.md",
    DOCS / "reference" / "compatibility.md",
    DOCS / "reference" / "performance.md",
    DOCS / "reference" / "mcp-adapter.md",
    DOCS / "reference" / "roadmap.md",
]
LINK_RE = re.compile(r"\[[^\]]*\]\(([^)]+)\)")


def add(errors: list[str], message: str) -> None:
    errors.append(message)


def markdown_links(errors: list[str], path: Path) -> None:
    text = path.read_text(encoding="utf-8", errors="replace")
    for match in LINK_RE.finditer(text):
        target = match.group(1).strip()
        if not target or target.startswith(("http://", "https://", "mailto:", "#")):
            continue
        target = target.split("#", 1)[0]
        if not target:
            continue
        resolved = (path.parent / target).resolve()
        try:
            resolved.relative_to(ROOT.resolve())
        except ValueError:
            add(errors, f"link escapes repository in {path.relative_to(ROOT)}: {target}")
            continue
        if not resolved.exists():
            add(errors, f"broken local link in {path.relative_to(ROOT)}: {target}")


def fresh_registry_defaults() -> dict[str, str]:
    sys.path.insert(0, str(ROOT / "src"))
    from mcp_gateway.schema import init_db  # noqa: E402

    with tempfile.TemporaryDirectory() as td:
        db = Path(td) / "gateway.db"
        init_db(str(db))
        con = sqlite3.connect(db)
        try:
            return dict(con.execute("SELECT key, value FROM settings"))
        finally:
            con.close()


def main() -> int:
    errors: list[str] = []
    compat = json.loads((ROOT / "compatibility.json").read_text(encoding="utf-8"))
    manifest = json.loads((ROOT / "manifest.json").read_text(encoding="utf-8"))
    version = str(compat["gateway_version"])
    catalog_version = int(compat["tool_catalog_version"])

    if manifest.get("version") != version:
        add(errors, f"manifest version {manifest.get('version')} != compatibility {version}")
    if int(manifest.get("tool_catalog", -1)) != catalog_version:
        add(errors, "manifest tool catalog differs from compatibility.json")

    minimum_python = str(manifest.get("minimum_python", "")).strip()
    if not minimum_python:
        add(errors, "manifest minimum_python is missing")
    if str(compat.get("runtime", {}).get("python", "")) != f"{minimum_python}+":
        add(errors, "manifest and compatibility Python contracts differ")

    sys.path.insert(0, str(ROOT / "src"))
    from mcp_gateway.bridge import ALLOWED_TOOLS, get_catalog_metadata  # noqa: E402

    metadata = get_catalog_metadata(sorted(ALLOWED_TOOLS))
    if metadata["tool_count"] != 21:
        add(errors, f"expected 21 tools, found {metadata['tool_count']}")
    if metadata["tool_catalog_version"] != catalog_version:
        add(errors, "runtime catalog version differs from compatibility.json")
    for required in ("read_file", "write_file", "run_task", "run_command", "gateway_doctor"):
        if required not in ALLOWED_TOOLS:
            add(errors, f"required tool missing: {required}")

    defaults = fresh_registry_defaults()
    expected_defaults = {
        "gateway_enabled": "true",
        "writes_enabled": "false",
        "shell_enabled": "false",
    }
    for key, expected in expected_defaults.items():
        if defaults.get(key) != expected:
            add(errors, f"fresh Registry default {key}={defaults.get(key)!r}, expected {expected!r}")

    if len(CURRENT_FILES) > 15:
        add(errors, f"CURRENT documentation surface grew to {len(CURRENT_FILES)} files (limit 15)")

    stale_patterns = {
        "old appliance IP": re.compile(r"192\.168\.68\.(?:72|85)"),
        "old admin account casing": re.compile(r"\bYorologo\b"),
        "old Admin port": re.compile(r"(?:127\.0\.0\.1|192\.168\.68\.\d+):8080"),
        "old active tool count": re.compile(r"\b(?:8|9|14)\s+(?:active\s+)?(?:tools|herramientas)\b", re.I),
        "legacy active deploy script": re.compile(r"deploy_(?:scp|to_pi|v101|appliance_update)\.py"),
        "old runbooks as current": re.compile(r"docs/runbooks/|\]\(runbooks/"),
    }

    for path in CURRENT_FILES + CONTEXT_FILES:
        if not path.exists():
            add(errors, f"documentation file missing: {path.relative_to(ROOT)}")
            continue
        text = path.read_text(encoding="utf-8", errors="replace")
        if "file://" in text:
            add(errors, f"local file:// URL in {path.relative_to(ROOT)}")
        if path not in (ROOT / "CHANGELOG.md", DOCS / "project-state.md"):
            for label, pattern in stale_patterns.items():
                if pattern.search(text):
                    add(errors, f"{label} in {path.relative_to(ROOT)}")
        markdown_links(errors, path)

    readme = (ROOT / "README.md").read_text(encoding="utf-8")
    if version not in readme:
        add(errors, f"README does not declare current version {version}")
    if len(readme.encode("utf-8")) > 12_000:
        add(errors, "README exceeds 12 KiB; move deep detail into docs/")
    for required in ("docs/getting-started.md", "docs/installation.md", "docs/update-rollback.md"):
        if required not in readme:
            add(errors, f"README missing beginner path link: {required}")

    docs_index = (DOCS / "README.md").read_text(encoding="utf-8")
    if "archive/" not in docs_index or "reference/" not in docs_index:
        add(errors, "docs/README.md must classify reference and archive material")
    if "reference/performance.md" not in docs_index:
        add(errors, "docs/README.md must link the runtime performance reference")

    performance_doc = (DOCS / "reference" / "performance.md").read_text(encoding="utf-8")
    for required in ("benchmark_runtime.py", "bridge_process_tax_p50_ms", "Go-only"):
        if required not in performance_doc:
            add(errors, f"performance reference missing {required}")

    benchmark_path = ROOT / "scripts" / "benchmark_runtime.py"
    if not benchmark_path.exists():
        add(errors, "runtime benchmark is missing: scripts/benchmark_runtime.py")

    project_state = (DOCS / "project-state.md").read_text(encoding="utf-8")
    for required in ("gateway_status", ".deployment.json", ".deployed-git-sha"):
        if required not in project_state:
            add(errors, f"project-state must point live production state to {required}")
    if re.search(r"production remains on v[0-9]", project_state, re.I):
        add(errors, "project-state duplicates a mutable production version")

    active_deployers = sorted(p.name for p in (ROOT / "scripts").glob("deploy*"))
    if active_deployers != ["deploy-pi.sh"]:
        add(errors, f"active deployment entrypoints are ambiguous: {active_deployers}")

    installer = (ROOT / "install.sh").read_text(encoding="utf-8")
    if minimum_python and f"Python {minimum_python} or higher is required" not in installer:
        add(errors, "installer does not enforce manifest minimum_python")
    for required in ("--check", "--rollback", "INSTALLATION_VERIFIED", "mcp-gateway.previous-install"):
        if required not in installer:
            add(errors, f"installer contract missing {required}")

    runner_path = ROOT / "scripts" / "run-resumable.sh"
    if not runner_path.exists():
        add(errors, "resumable runner is missing: scripts/run-resumable.sh")
    else:
        runner = runner_path.read_text(encoding="utf-8")
        for required in ("nohup setsid flock", ".local/state", "STATE=", "INTERRUPTED", "--expect-marker", "MCP_PI_RESUMABLE_JOB_ID"):
            if required not in runner:
                add(errors, f"resumable runner contract missing {required}")

    grant_template = ROOT / "src" / "mcp_gateway" / "web" / "templates" / "client_grants.html"
    grant_views = (ROOT / "src" / "mcp_gateway" / "web" / "views.py").read_text(encoding="utf-8")
    if not grant_template.exists():
        add(errors, "Admin grant management template is missing")
    for required in ("client_grants", "client_grant_add", "client_grant_edit", "client_grant_toggle", "client_grant_delete", "client_grant_check", "authorize_client"):
        if required not in grant_views:
            add(errors, f"Admin grant management contract missing {required}")
    admin_doc = (DOCS / "admin-console.md").read_text(encoding="utf-8")
    for required in ("AI Clients → Grants", "Check Effective Access", "authorize_client()", "Administrative tables", "server-side audit filtering"):
        if required not in admin_doc:
            add(errors, f"Admin Console documentation missing {required}")

    deployer = (ROOT / "scripts" / "deploy-pi.sh").read_text(encoding="utf-8")
    for required in ("MCP_PI_RESUMABLE_JOB_ID", "MCP_DEPLOY_ALLOW_DIRECT", "CONTROL_PLANE_RESTART=EXPECTED", "CONTROL_PLANE_RESTORED"):
        if required not in deployer:
            add(errors, f"self-restarting deploy contract missing {required}")

    for doc in (DOCS / "operations.md", DOCS / "update-rollback.md"):
        text = doc.read_text(encoding="utf-8")
        for required in ("run-resumable.sh", "status", "log", "CONTROL_PLANE_RESTART=EXPECTED", "CONTROL_PLANE_RESTORED"):
            if required not in text:
                add(errors, f"resumable operations guidance missing {required} in {doc.relative_to(ROOT)}")

    if defaults.get("default_timeout") != "30":
        add(errors, f"fresh Registry default default_timeout={defaults.get('default_timeout')!r}, expected '30'")

    if errors:
        print("DOCS_AUDIT=FAIL")
        for error in errors:
            print(f"- {error}")
        return 1

    print("DOCS_AUDIT=PASS")
    print(f"gateway_version={version}")
    print(f"tool_catalog_version={catalog_version}")
    print(f"tool_count={metadata['tool_count']}")
    print(f"catalog_hash={metadata['catalog_hash']}")
    print(f"current_docs={len(CURRENT_FILES)}")
    print("fresh_defaults=gateway:true,writes:false,shell:false")
    print("active_deployer=scripts/deploy-pi.sh")
    print("resumable_runner=scripts/run-resumable.sh")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
