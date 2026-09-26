"""Views and route handlers for MCP Gateway Admin Console."""

import json
import os
import platform
import shutil
import socket
import sys
import time
from typing import Any, Dict
from urllib.parse import urlsplit
from flask import (
    Blueprint,
    abort,
    current_app,
    flash,
    redirect,
    render_template,
    request,
    session,
    url_for,
)

from .. import __version__
from ..policy import (
    TARGET_PRIVILEGE_CAPABILITY,
    TOOL_CAPABILITIES,
    authorize_client,
    authorize_privilege_request,
    normalize_privilege_policy,
    summarize_grant_capabilities,
)
from .auth import (
    check_password_hash,
    is_rate_limited,
    login_required,
    record_login_failure,
    reset_login_failures,
)
from .csrf import get_csrf_token

bp = Blueprint("admin", __name__)
auth_bp = Blueprint("auth", __name__)


def get_registry():
    return current_app.config["REGISTRY"]


def get_tools():
    return current_app.config["TOOLS"]


ALWAYS_ALLOW_CONFIRM_TTL_SECONDS = 300


def _safe_local_redirect(value: str):
    """Return a same-origin absolute path or None."""
    value = str(value or "").strip()
    if not value or "\\" in value or "\r" in value or "\n" in value:
        return None
    try:
        parsed = urlsplit(value)
    except ValueError:
        return None
    if (
        parsed.scheme
        or parsed.netloc
        or not parsed.path.startswith("/")
        or parsed.path.startswith("//")
    ):
        return None
    return value


def _target_privilege_scopes(target_id: str):
    """Return concrete client/project scopes that currently have target_admin."""
    registry = get_registry()
    scopes = []
    for client in registry.list_clients():
        if not client.get("enabled", True):
            continue
        client_id = client["id"]
        for project in registry.list_projects(target_id):
            if not project.get("enabled", True):
                continue
            project_id = project["id"]
            allowed, _ = authorize_privilege_request(
                client_id, target_id, project_id, registry
            )
            if allowed:
                scopes.append(
                    {
                        "client_id": client_id,
                        "project_id": project_id,
                        "value": json.dumps(
                            [client_id, project_id],
                            ensure_ascii=False,
                            separators=(",", ":"),
                        ),
                    }
                )
    return sorted(scopes, key=lambda item: (item["client_id"], item["project_id"]))


def _target_ssh_context(target_id: str) -> Dict[str, Any]:
    tools = get_tools()
    return {
        "ssh_identity": tools.target_ssh_identity(target_id),
        "gateway_public_key": tools.gateway_ssh_public_key(),
        "privilege_status": tools.target_privilege_status(target_id),
        "privilege_scopes": _target_privilege_scopes(target_id),
    }


def record_audit(
    action: str,
    target_id: str = None,
    project_id: str = None,
    success: bool = True,
    error_code: str = None,
    detail: str = "",
    required: bool = False,
) -> bool:
    try:
        reg = get_registry()
        reg.record_activity({
            "actor": session.get("user", "system"),
            "action": action,
            "target_id": target_id,
            "project_id": project_id,
            "success": success,
            "error_code": error_code,
            "detail": detail,
        })
        return True
    except Exception as exc:
        if required:
            raise RuntimeError("AUDIT_UNAVAILABLE: security-sensitive change was not applied") from exc
        return False


# ==========================================
# Authentication Routes
# ==========================================

@auth_bp.route("/login", methods=["GET", "POST"])
def login():
    if request.method in ("GET", "HEAD"):
        if session.get("user"):
            return redirect(url_for("admin.dashboard"))
        return render_template("login.html")

    # POST login
    username = request.form.get("username", "").strip()
    password = request.form.get("password", "")
    ip = request.remote_addr or "127.0.0.1"

    if is_rate_limited(ip) or is_rate_limited(username):
        record_audit("admin_login_rate_limited", success=False, error_code="RATE_LIMITED", detail=f"IP {ip} or user {username} locked out")
        flash("Too many failed login attempts. Please wait 1 minute.", "danger")
        return render_template("login.html"), 429

    registry = get_registry()
    user_record = registry.get_admin_user(username)

    if not user_record or not user_record.get("enabled", True):
        record_login_failure(ip)
        record_login_failure(username)
        record_audit("admin_login_failed", success=False, error_code="AUTH_FAILED", detail=f"User '{username}' not found or disabled")
        flash("Invalid username or password.", "danger")
        return render_template("login.html"), 401

    if not check_password_hash(user_record["password_hash"], password):
        record_login_failure(ip)
        record_login_failure(username)
        record_audit("admin_login_failed", success=False, error_code="AUTH_FAILED", detail=f"Bad password for user '{username}'")
        flash("Invalid username or password.", "danger")
        return render_template("login.html"), 401

    # Successful login: reset session and rate limit
    reset_login_failures(ip)
    reset_login_failures(username)
    session.clear()
    session["user"] = username
    session["last_active"] = time.time()
    get_csrf_token()  # Generate fresh CSRF token

    registry.update_admin_login(username)
    record_audit("admin_login_success", success=True, detail=f"Admin '{username}' logged in")

    next_url = _safe_local_redirect(request.args.get("next"))
    return redirect(next_url or url_for("admin.dashboard"))


@auth_bp.route("/logout", methods=["POST"])
def logout():
    user = session.get("user", "unknown")
    record_audit("admin_logout", success=True, detail=f"User '{user}' logged out")
    session.clear()
    flash("You have been successfully logged out.", "info")
    return redirect(url_for("auth.login"))


# ==========================================
# Dashboard & Main Routes
# ==========================================

@bp.route("/")
def index():
    return redirect(url_for("admin.dashboard"))


@bp.route("/dashboard")
@login_required
def dashboard():
    registry = get_registry()
    tools = get_tools()

    gw_enabled_str = str(registry.get_setting("gateway_enabled", "true")).lower()
    gateway_enabled = gw_enabled_str in ("true", "1", "yes", "on")

    writes_enabled_str = str(registry.get_setting("writes_enabled", "false")).lower()
    writes_enabled = writes_enabled_str in ("true", "1", "yes", "on")

    targets = registry.list_targets()
    total_targets = len(targets)
    online_targets = sum(1 for t in targets if t.get("enabled", True))

    projects = registry.list_projects()
    total_projects = len(projects)

    clients = registry.list_clients()
    total_clients = len(clients)

    total_requests = registry.get_activity_count()
    recent_activity = registry.list_activity(limit=8)
    denied_count = sum(1 for a in recent_activity if not a.get("success", True))

    # Basic system info
    uptime_sec = 0
    try:
        with open("/proc/uptime", "r") as f:
            uptime_sec = int(float(f.readline().split()[0]))
    except Exception:
        uptime_sec = 0

    # MCP Adapter status check on 127.0.0.1:8090
    import socket
    mcp_online = False
    try:
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
            s.settimeout(0.15)
            mcp_online = (s.connect_ex(("127.0.0.1", 8090)) == 0)
    except Exception:
        mcp_online = False

    return render_template(
        "dashboard.html",
        gateway_enabled=gateway_enabled,
        writes_enabled=writes_enabled,
        gateway_version=__version__,
        total_targets=total_targets,
        online_targets=online_targets,
        total_projects=total_projects,
        total_clients=total_clients,
        total_requests=total_requests,
        denied_count=denied_count,
        uptime_sec=uptime_sec,
        recent_activity=recent_activity,
        mcp_online=mcp_online,
    )


# ==========================================
# Targets Management
# ==========================================

@bp.route("/targets")
@login_required
def targets_list():
    registry = get_registry()
    targets = registry.list_targets()
    return render_template("targets.html", targets=targets)


@bp.route("/targets/add", methods=["GET", "POST"])
@login_required
def target_add():
    if request.method == "GET":
        return render_template("target_form.html", target=None, is_edit=False)

    # POST add target
    data = {
        "id": request.form.get("id", "").strip(),
        "display_name": request.form.get("display_name", "").strip(),
        "platform": request.form.get("platform", "linux").strip(),
        "host": request.form.get("host", "").strip(),
        "port": int(request.form.get("port", 22)),
        "user": request.form.get("user", "").strip(),
        "ssh_alias": request.form.get("ssh_alias", "").strip(),
        "privilege_user": request.form.get("privilege_user", "").strip(),
        "privilege_policy": request.form.get("privilege_policy", "never").strip(),
        "enabled": request.form.get("enabled") == "on",
    }
    if not data["id"] or not data["host"] or not data["user"]:
        flash("ID, host, and user are required fields.", "danger")
        return render_template("target_form.html", target=data, is_edit=False)

    try:
        data["privilege_policy"] = normalize_privilege_policy(data["privilege_policy"])
        if data["privilege_policy"] == "always_allow":
            raise ValueError(
                "Create the Target with a safer privilege policy, then enable Always allow from Edit Target with the required second confirmation."
            )
        if data["privilege_policy"] != "never":
            record_audit(
                "target_privilege_policy_change_attempt",
                target_id=data["id"],
                detail=json.dumps(
                    {"old_policy": None, "new_policy": data["privilege_policy"]},
                    sort_keys=True,
                ),
                required=True,
            )
        registry = get_registry()
        if data["enabled"]:
            record_audit(
                "add_target_attempt",
                target_id=data["id"],
                detail=f"Add enabled target '{data['id']}'",
                required=True,
            )
        registry.add_target(data)
        record_audit("add_target", target_id=data["id"], success=True, detail=f"Added target '{data['id']}'")
        flash(f"Target '{data['id']}' created successfully.", "success")
        return redirect(url_for("admin.targets_list"))
    except Exception as e:
        flash(f"Error creating target: {e}", "danger")
        return render_template("target_form.html", target=data, is_edit=False)


@bp.route("/targets/<target_id>/edit", methods=["GET", "POST"])
@login_required
def target_edit(target_id: str):
    registry = get_registry()
    try:
        target = registry.get_target(target_id, include_disabled=True)
    except Exception:
        abort(404, description="Target not found")

    if request.method == "GET":
        target["id"] = target_id
        return render_template(
            "target_form.html",
            target=target,
            is_edit=True,
            **_target_ssh_context(target_id),
        )

    # POST edit target
    update_data = {
        "display_name": request.form.get("display_name", "").strip(),
        "platform": request.form.get("platform", "linux").strip(),
        "host": request.form.get("host", "").strip(),
        "port": int(request.form.get("port", 22)),
        "user": request.form.get("user", "").strip(),
        "ssh_alias": request.form.get("ssh_alias", "").strip(),
        "privilege_user": request.form.get("privilege_user", "").strip(),
        "privilege_policy": request.form.get("privilege_policy", "never").strip(),
        "enabled": request.form.get("enabled") == "on",
    }
    try:
        old_privilege_policy = normalize_privilege_policy(
            target.get("privilege_policy", "never")
        )
        old_privilege_user = str(target.get("privilege_user") or "").strip()
        update_data["privilege_policy"] = normalize_privilege_policy(
            update_data["privilege_policy"]
        )
        new_privilege_policy = update_data["privilege_policy"]
        new_privilege_user = update_data["privilege_user"]

        if old_privilege_policy != "always_allow" and new_privilege_policy == "always_allow":
            # Do not mutate the Target at all until the separate confirmation
            # completes. Other edits should be saved independently first.
            session["pending_always_allow"] = {
                "target_id": target_id,
                "expected_policy": old_privilege_policy,
                "requested_at": time.time(),
            }
            flash(
                "Always allow requires a separate risk confirmation and Admin password re-authentication. Other Target edits were not saved; save them separately first if needed.",
                "warning",
            )
            return redirect(
                url_for("admin.target_privilege_always_allow", target_id=target_id)
            )

        if old_privilege_user != new_privilege_user:
            record_audit(
                "target_privilege_user_change_attempt",
                target_id=target_id,
                detail=json.dumps(
                    {
                        "old_privilege_user": old_privilege_user,
                        "new_privilege_user": new_privilege_user,
                    },
                    sort_keys=True,
                ),
                required=True,
            )
        if old_privilege_policy != new_privilege_policy:
            record_audit(
                "target_privilege_policy_change_attempt",
                target_id=target_id,
                detail=json.dumps(
                    {
                        "old_policy": old_privilege_policy,
                        "new_policy": new_privilege_policy,
                    },
                    sort_keys=True,
                ),
                required=True,
            )
        # privilege_user/policy already have dedicated required audit events.
        security_fields = (
            "platform",
            "host",
            "port",
            "user",
            "ssh_alias",
            "enabled",
        )
        security_changed = any(
            update_data.get(field) != target.get(field)
            for field in security_fields
        )
        if update_data["enabled"] and security_changed:
            record_audit(
                "update_target_attempt",
                target_id=target_id,
                detail=f"Update active Target security context for '{target_id}'",
                required=True,
            )
        registry.update_target(target_id, update_data)
        record_audit(
            "update_target",
            target_id=target_id,
            success=True,
            detail=f"Updated target '{target_id}'",
        )
        if old_privilege_user != new_privilege_user:
            record_audit(
                "target_privilege_user_changed",
                target_id=target_id,
                success=True,
                detail=json.dumps(
                    {
                        "old_privilege_user": old_privilege_user,
                        "new_privilege_user": new_privilege_user,
                        "cached_approval_revoked": True,
                    },
                    sort_keys=True,
                ),
            )
        if old_privilege_policy != new_privilege_policy:
            record_audit(
                "target_privilege_policy_changed",
                target_id=target_id,
                success=True,
                detail=json.dumps(
                    {
                        "old_policy": old_privilege_policy,
                        "new_policy": new_privilege_policy,
                        "cached_approval_revoked": True,
                    },
                    sort_keys=True,
                ),
            )
        flash(f"Target '{target_id}' updated successfully.", "success")
        return redirect(url_for("admin.targets_list"))
    except Exception as e:
        flash(f"Error updating target: {e}", "danger")
        target["id"] = target_id
        target.update(update_data)
        return render_template(
            "target_form.html",
            target=target,
            is_edit=True,
            **_target_ssh_context(target_id),
        )


@bp.route("/targets/<target_id>/privileges/always-allow", methods=["GET", "POST"])
@login_required
def target_privilege_always_allow(target_id: str):
    registry = get_registry()
    pending = session.get("pending_always_allow")
    now = time.time()
    try:
        requested_at = (
            float(pending.get("requested_at", 0))
            if isinstance(pending, dict)
            else 0.0
        )
    except (TypeError, ValueError):
        requested_at = 0.0
    pending_age = now - requested_at
    if (
        not isinstance(pending, dict)
        or pending.get("target_id") != target_id
        or pending_age < 0
        or pending_age > ALWAYS_ALLOW_CONFIRM_TTL_SECONDS
    ):
        session.pop("pending_always_allow", None)
        flash("Always allow confirmation is missing or expired. Start again from Edit Target.", "danger")
        return redirect(url_for("admin.target_edit", target_id=target_id))

    try:
        target = registry.get_target(target_id)
    except Exception:
        session.pop("pending_always_allow", None)
        abort(404, description="Target not found")

    current_policy = normalize_privilege_policy(target.get("privilege_policy", "never"))
    expected_policy = normalize_privilege_policy(pending.get("expected_policy", "never"))
    if current_policy != expected_policy or current_policy == "always_allow":
        session.pop("pending_always_allow", None)
        flash("Target privilege policy changed while confirmation was pending. Start again.", "danger")
        return redirect(url_for("admin.target_edit", target_id=target_id))

    if request.method == "GET":
        return render_template(
            "target_always_allow_confirm.html",
            target=target,
            current_policy=current_policy,
        )

    confirm_target_id = request.form.get("confirm_target_id", "").strip()
    password = request.form.get("password", "")
    username = str(session.get("user") or "").strip()
    rate_key = f"reauth:{username}:{request.remote_addr or 'unknown'}"

    if is_rate_limited(rate_key):
        flash("Too many failed re-authentication attempts. Try again after the lockout.", "danger")
        return render_template(
            "target_always_allow_confirm.html",
            target=target,
            current_policy=current_policy,
        )

    if confirm_target_id != target_id:
        flash("Target ID confirmation does not match.", "danger")
        return render_template(
            "target_always_allow_confirm.html",
            target=target,
            current_policy=current_policy,
        )

    user_record = registry.get_admin_user(username) if username else None
    if (
        not user_record
        or not user_record.get("enabled", True)
        or not check_password_hash(user_record["password_hash"], password)
    ):
        record_login_failure(rate_key)
        flash("Admin password confirmation failed.", "danger")
        return render_template(
            "target_always_allow_confirm.html",
            target=target,
            current_policy=current_policy,
        )

    reset_login_failures(rate_key)
    try:
        record_audit(
            "target_privilege_policy_change_attempt",
            target_id=target_id,
            detail=json.dumps(
                {
                    "old_policy": current_policy,
                    "new_policy": "always_allow",
                    "second_confirmation": True,
                    "reauthenticated_user": username,
                },
                sort_keys=True,
            ),
            required=True,
        )
        registry.update_target(target_id, {"privilege_policy": "always_allow"})
        record_audit(
            "target_privilege_policy_changed",
            target_id=target_id,
            success=True,
            detail=json.dumps(
                {
                    "old_policy": current_policy,
                    "new_policy": "always_allow",
                    "cached_approval_revoked": True,
                    "second_confirmation": True,
                    "reauthenticated_user": username,
                },
                sort_keys=True,
            ),
        )
    except Exception as exc:
        flash(f"Always allow was not enabled: {exc}", "danger")
        return render_template(
            "target_always_allow_confirm.html",
            target=target,
            current_policy=current_policy,
        )

    session.pop("pending_always_allow", None)
    flash(
        f"Always allow enabled for '{target_id}'. Authorized target_admin requests will no longer require per-request approval.",
        "warning",
    )
    return redirect(url_for("admin.target_edit", target_id=target_id))


@bp.route("/targets/<target_id>/toggle", methods=["POST"])
@login_required
def target_toggle(target_id: str):
    registry = get_registry()
    try:
        target = registry.get_target(target_id, include_disabled=True)
    except Exception:
        abort(404, description="Target not found")
    new_state = not bool(target["enabled"])
    if new_state:
        record_audit(
            "toggle_target_attempt",
            target_id=target_id,
            detail=f"Enable target '{target_id}'",
            required=True,
        )
    registry.update_target(target_id, {"enabled": new_state})
    record_audit("toggle_target", target_id=target_id, success=True, detail=f"Set target '{target_id}' enabled={new_state}")
    status_txt = "enabled" if new_state else "disabled"
    flash(f"Target '{target_id}' is now {status_txt}.", "info")
    return redirect(url_for("admin.targets_list"))


@bp.route("/targets/<target_id>/test", methods=["POST"])
@login_required
def target_test(target_id: str):
    tools = get_tools()
    res = tools.target_status(target_id)
    record_audit("test_target", target_id=target_id, success=res.get("ok", False), detail=f"Status test: {res}")
    if res.get("ok"):
        latency = res.get("result", {}).get("latency_ms", res.get("duration_ms", 0))
        flash(f"Target '{target_id}' reachable! Latency: {latency} ms", "success")
    else:
        err = res.get("error", {})
        flash(f"Target check failed [{err.get('code')}]: {err.get('message')}", "danger")
    return_to = request.form.get("return_to", "")
    if return_to == "edit":
        return redirect(url_for("admin.target_edit", target_id=target_id))
    return redirect(url_for("admin.targets_list"))


@bp.route("/targets/<target_id>/ssh/trust", methods=["POST"])
@login_required
def target_ssh_trust(target_id: str):
    fingerprint = request.form.get("fingerprint", "").strip()
    replace = request.form.get("replace") == "1"
    res = get_tools().trust_target_ssh_identity(target_id, fingerprint, replace=replace)
    if res.get("ok"):
        action = "replaced" if replace else "trusted"
        flash(f"SSH host identity for '{target_id}' {action} successfully.", "success")
    else:
        err = res.get("error", {})
        flash(f"SSH trust update failed [{err.get('code')}]: {err.get('message')}", "danger")
    return redirect(url_for("admin.target_edit", target_id=target_id))


@bp.route("/targets/<target_id>/ssh/untrust", methods=["POST"])
@login_required
def target_ssh_untrust(target_id: str):
    res = get_tools().remove_target_ssh_identity(target_id)
    if res.get("ok"):
        flash(f"SSH host identity for '{target_id}' removed. Connections remain fail-closed until re-trusted.", "info")
    else:
        err = res.get("error", {})
        flash(f"SSH trust removal failed [{err.get('code')}]: {err.get('message')}", "danger")
    return redirect(url_for("admin.target_edit", target_id=target_id))


@bp.route("/targets/<target_id>/privileges/approve", methods=["POST"])
@login_required
def target_privilege_approve(target_id: str):
    tools = get_tools()
    scopes = _target_privilege_scopes(target_id)
    try:
        requested_scope = json.loads(request.form.get("scope", ""))
        if (
            not isinstance(requested_scope, list)
            or len(requested_scope) != 2
            or not all(isinstance(value, str) for value in requested_scope)
        ):
            raise ValueError("invalid scope")
        requested_pair = tuple(requested_scope)
        current_pairs = {
            (scope["client_id"], scope["project_id"]) for scope in scopes
        }
        if requested_pair not in current_pairs:
            raise ValueError("stale or unauthorized scope")
        client_id, project_id = requested_pair
    except (TypeError, ValueError):
        flash("Select a valid client/project scope with an explicit target_admin grant.", "danger")
        return redirect(url_for("admin.target_edit", target_id=target_id))

    res = tools.approve_target_privilege(
        target_id,
        client_id,
        project_id,
        actor=f"admin:{session.get('user', 'unknown')}",
    )
    if res.get("ok"):
        result = res.get("result", {})
        scope = result.get("scope", "not required")
        flash(f"Privilege approval recorded for '{target_id}' ({scope}).", "success")
    else:
        err = res.get("error", {})
        flash(f"Privilege approval failed [{err.get('code')}]: {err.get('message')}", "danger")
    return redirect(url_for("admin.target_edit", target_id=target_id))


@bp.route("/targets/<target_id>/privileges/revoke", methods=["POST"])
@login_required
def target_privilege_revoke(target_id: str):
    tools = get_tools()
    res = tools.revoke_target_privilege(
        target_id,
        actor=f"admin:{session.get('user', 'unknown')}",
    )
    if res.get("ok"):
        flash(f"Cached privilege approval revoked for '{target_id}'.", "success")
    else:
        err = res.get("error", {})
        flash(f"Privilege revocation failed [{err.get('code')}]: {err.get('message')}", "danger")
    return redirect(url_for("admin.target_edit", target_id=target_id))


# ==========================================
# Projects Management
# ==========================================

@bp.route("/projects")
@login_required
def projects_list():
    registry = get_registry()
    target_filter = request.args.get("target", "").strip()
    projects = registry.list_projects(target_filter or None)
    return render_template(
        "projects.html",
        projects=projects,
        target_filter=target_filter,
    )


@bp.route("/projects/add", methods=["GET", "POST"])
@login_required
def project_add():
    registry = get_registry()
    targets = registry.list_targets()
    if request.method == "GET":
        return render_template("project_form.html", project=None, targets=targets, is_edit=False)

    target_id = request.form.get("target_id", "").strip()
    data = {
        "id": request.form.get("id", "").strip(),
        "display_name": request.form.get("display_name", "").strip(),
        "root": request.form.get("root", "").strip(),
        "read": request.form.get("read") == "on",
        "write": request.form.get("write") == "on",
        "enabled": request.form.get("enabled") == "on",
        "tasks": {},
    }
    if not target_id or not data["id"] or not data["root"]:
        flash("Target, Project ID, and Root path are required.", "danger")
        return render_template("project_form.html", project=data, targets=targets, is_edit=False)

    try:
        if data["enabled"]:
            record_audit(
                "add_project_attempt",
                target_id=target_id,
                project_id=data["id"],
                detail=f"Add enabled project '{data['id']}'",
                required=True,
            )
        registry.add_project(target_id, data)
        record_audit("add_project", target_id=target_id, project_id=data["id"], success=True, detail=f"Added project '{data['id']}'")
        flash(f"Project '{data['id']}' created under target '{target_id}'.", "success")
        return redirect(url_for("admin.projects_list"))
    except Exception as e:
        flash(f"Error adding project: {e}", "danger")
        return render_template("project_form.html", project=data, targets=targets, is_edit=False)


@bp.route("/projects/<target_id>/<project_id>/edit", methods=["GET", "POST"])
@login_required
def project_edit(target_id: str, project_id: str):
    registry = get_registry()
    targets = registry.list_targets()

    try:
        project = registry.get_project(target_id, project_id, include_disabled=True)
    except Exception:
        abort(404, description="Project not found")

    if request.method == "GET":
        return render_template("project_form.html", project=project, targets=targets, is_edit=True)

    # POST edit
    update_data = {
        "display_name": request.form.get("display_name", "").strip(),
        "root": request.form.get("root", "").strip(),
        "read": request.form.get("read") == "on",
        "write": request.form.get("write") == "on",
        "enabled": request.form.get("enabled") == "on",
    }
    try:
        security_changed = any(
            update_data.get(field) != project.get(field)
            for field in ("root", "read", "write", "enabled")
        )
        if update_data["enabled"] and security_changed:
            record_audit(
                "update_project_attempt",
                target_id=target_id,
                project_id=project_id,
                detail=f"Update active project security context for '{project_id}'",
                required=True,
            )
        registry.update_project(target_id, project_id, update_data)
        record_audit("update_project", target_id=target_id, project_id=project_id, success=True, detail=f"Updated project '{project_id}'")
        flash(f"Project '{project_id}' updated.", "success")
        return redirect(url_for("admin.projects_list"))
    except Exception as e:
        flash(f"Error updating project: {e}", "danger")
        project.update(update_data)
        return render_template("project_form.html", project=project, targets=targets, is_edit=True)


@bp.route("/projects/<target_id>/<project_id>/toggle", methods=["POST"])
@login_required
def project_toggle(target_id: str, project_id: str):
    registry = get_registry()
    try:
        project = registry.get_project(target_id, project_id, include_disabled=True)
    except Exception:
        abort(404, description="Project not found")
    new_state = not bool(project["enabled"])
    if new_state:
        record_audit(
            "toggle_project_attempt",
            target_id=target_id,
            project_id=project_id,
            detail=f"Enable project '{project_id}'",
            required=True,
        )
    registry.update_project(target_id, project_id, {"enabled": new_state})
    record_audit("toggle_project", target_id=target_id, project_id=project_id, success=True, detail=f"Set project '{project_id}' enabled={new_state}")
    status_txt = "enabled" if new_state else "disabled"
    flash(f"Project '{project_id}' is now {status_txt}.", "info")
    return redirect(url_for("admin.projects_list"))


@bp.route("/projects/<target_id>/<project_id>/toggle-write", methods=["POST"])
@login_required
def project_toggle_write(target_id: str, project_id: str):
    registry = get_registry()
    try:
        project = registry.get_project(target_id, project_id, include_disabled=True)
    except Exception:
        abort(404, description="Project not found")
    new_state = not bool(project["write"])
    if new_state:
        record_audit(
            "toggle_project_write_attempt",
            target_id=target_id,
            project_id=project_id,
            detail=f"Enable write for project '{project_id}'",
            required=True,
        )
    registry.update_project(target_id, project_id, {"write": new_state})
    record_audit("toggle_project_write", target_id=target_id, project_id=project_id, success=True, detail=f"Set project '{project_id}' write={new_state}")
    status_txt = "ENABLED" if new_state else "DISABLED"
    flash(f"Project '{project_id}' write capability is now {status_txt}.", "warning" if new_state else "info")
    return redirect(url_for("admin.projects_list"))



# ==========================================
# AI Clients Management
# ==========================================

GRANT_COMMON_CAPABILITIES = (
    ("read", "Read"),
    ("write", "Write"),
    ("execute", "Tasks / Execute"),
    ("admin", "Gateway appliance admin"),
    ("*", "* — All compatible tool capabilities"),
)


def _known_grant_capabilities():
    caps = {"*", TARGET_PRIVILEGE_CAPABILITY}
    for allowed in TOOL_CAPABILITIES.values():
        caps.update(allowed)
    return caps


def _normalize_grant_capability(raw: str) -> str:
    tokens = []
    for token in str(raw or "").split(","):
        token = token.strip()
        if token and token not in tokens:
            tokens.append(token)
    if not tokens:
        raise ValueError("Capability is required.")
    known = _known_grant_capabilities()
    unknown = [token for token in tokens if token not in known]
    if unknown:
        raise ValueError(f"Unknown grant capability: {', '.join(unknown)}")
    if "*" in tokens:
        non_wildcard = [token for token in tokens if token != "*"]
        if any(token != TARGET_PRIVILEGE_CAPABILITY for token in non_wildcard):
            raise ValueError(
                "'*' cannot be combined with ordinary capabilities; target_admin is the only explicit exception."
            )
        tokens = ["*"] + (
            [TARGET_PRIVILEGE_CAPABILITY]
            if TARGET_PRIVILEGE_CAPABILITY in non_wildcard
            else []
        )
    return ",".join(tokens)


def _grant_form_data(registry, client_id: str):
    # Validate the client first so malformed URLs fail closed.
    registry.get_client(client_id)
    target_id = request.form.get("target_id", "").strip()
    project_id = request.form.get("project_id", "").strip() or "*"

    # New UI submits semantic multi-value controls. Keep the legacy CSV field as
    # an input compatibility path for existing clients/tests.
    capability_values = [
        value.strip()
        for value in request.form.getlist("capabilities")
        if value.strip()
    ]
    if request.form.get("target_shell") == "on":
        capability_values.append("target_shell")
    if request.form.get("target_admin") == "on":
        capability_values.append(TARGET_PRIVILEGE_CAPABILITY)
    raw_capability = (
        ",".join(capability_values)
        if capability_values
        else request.form.get("capability", "")
    )
    capability = _normalize_grant_capability(raw_capability)

    target_ids = {t["id"] for t in registry.list_targets()}
    projects = registry.list_projects()
    if target_id != "*" and target_id not in target_ids:
        raise ValueError(f"Unknown Target: {target_id}")
    if target_id == "*" and project_id != "*":
        raise ValueError("Project must be '*' when Target is '*'.")
    if project_id != "*":
        if not any(p["target_id"] == target_id and p["id"] == project_id for p in projects):
            raise ValueError(f"Project '{project_id}' is not configured under Target '{target_id}'.")

    capability_tokens = {token.strip() for token in capability.split(",") if token.strip()}
    if (
        target_id == "*"
        and project_id == "*"
        and ("*" in capability_tokens or TARGET_PRIVILEGE_CAPABILITY in capability_tokens)
    ):
        if request.form.get("confirm_global") != "on":
            raise ValueError(
                "Global wildcard or target_admin grant requires explicit confirmation."
            )

    return {
        "client_id": client_id,
        "target_id": target_id,
        "project_id": project_id,
        "capability": capability,
        "enabled": request.form.get("enabled") == "on",
    }


def _grant_for_client_or_404(registry, client_id: str, grant_id: int):
    try:
        grant = registry.get_grant(grant_id)
    except KeyError:
        abort(404, description="Grant not found")
    if grant.get("client_id") != client_id:
        abort(404, description="Grant not found")
    return grant


def _client_grants_context(registry, client_id: str, editing_grant=None, access_result=None):
    client = registry.get_client(client_id)
    targets = registry.list_targets()
    projects = registry.list_projects()
    grants = registry.list_grants(client_id=client_id)
    common_values = {value for value, _ in GRANT_COMMON_CAPABILITIES}
    privilege_values = {"target_shell", TARGET_PRIVILEGE_CAPABILITY}
    specific = sorted(
        _known_grant_capabilities() - common_values - privilege_values
    )
    selected = {
        token.strip()
        for token in str(
            editing_grant.get("capability", "") if editing_grant else "read"
        ).split(",")
        if token.strip()
    }
    return {
        "client": client,
        "grants": grants,
        "targets": targets,
        "projects": projects,
        "common_capabilities": GRANT_COMMON_CAPABILITIES,
        "specific_capabilities": specific,
        "selected_capabilities": selected,
        "tool_names": sorted(TOOL_CAPABILITIES),
        "editing_grant": editing_grant,
        "access_result": access_result,
    }

@bp.route("/clients")
@login_required
def clients_list():
    registry = get_registry()
    clients = registry.list_clients()
    for client in clients:
        grants = registry.get_client_grants(client["id"])
        client["grant_summary"] = summarize_grant_capabilities(grants)
        client["grant_count"] = len([g for g in grants if g.get("enabled", True)])
    return render_template("clients.html", clients=clients)


@bp.route("/clients/add", methods=["GET", "POST"])
@login_required
def client_add():
    if request.method == "GET":
        return render_template("client_form.html", client=None, is_edit=False)

    data = {
        "id": request.form.get("id", "").strip(),
        "display_name": request.form.get("display_name", "").strip(),
        "provider": request.form.get("provider", "").strip(),
        "protocol": request.form.get("protocol", "mcp").strip(),
        "enabled": request.form.get("enabled") == "on",
        "notes": request.form.get("notes", "").strip(),
    }
    if not data["id"] or not data["display_name"]:
        flash("Client ID and Display Name are required.", "danger")
        return render_template("client_form.html", client=data, is_edit=False)

    try:
        registry = get_registry()
        if data["enabled"]:
            record_audit(
                "add_client_attempt",
                detail=f"Add enabled AI client '{data['id']}'",
                required=True,
            )
        registry.add_client(data)
        record_audit("add_client", success=True, detail=f"Added AI client '{data['id']}'")
        flash(f"AI Client '{data['id']}' created.", "success")
        return redirect(url_for("admin.clients_list"))
    except Exception as e:
        flash(f"Error adding client: {e}", "danger")
        return render_template("client_form.html", client=data, is_edit=False)


@bp.route("/clients/<client_id>/edit", methods=["GET", "POST"])
@login_required
def client_edit(client_id: str):
    registry = get_registry()
    try:
        client = registry.get_client(client_id)
    except KeyError:
        abort(404, description="AI Client not found")

    if request.method == "GET":
        return render_template("client_form.html", client=client, is_edit=True)

    update_data = {
        "display_name": request.form.get("display_name", "").strip(),
        "provider": request.form.get("provider", "").strip(),
        "protocol": request.form.get("protocol", "mcp").strip(),
        "enabled": request.form.get("enabled") == "on",
        "notes": request.form.get("notes", "").strip(),
    }
    try:
        if update_data["enabled"] and not client.get("enabled", True):
            record_audit(
                "update_client_attempt",
                detail=f"Enable AI client '{client_id}'",
                required=True,
            )
        registry.update_client(client_id, update_data)
        record_audit("update_client", success=True, detail=f"Updated AI client '{client_id}'")
        flash(f"AI Client '{client_id}' updated.", "success")
        return redirect(url_for("admin.clients_list"))
    except Exception as e:
        flash(f"Error updating client: {e}", "danger")
        client.update(update_data)
        return render_template("client_form.html", client=client, is_edit=True)


@bp.route("/clients/<client_id>/toggle", methods=["POST"])
@login_required
def client_toggle(client_id: str):
    registry = get_registry()
    try:
        client = registry.get_client(client_id)
        new_state = not client.get("enabled", True)
        if new_state:
            record_audit(
                "toggle_client_attempt",
                detail=f"Enable AI client '{client_id}'",
                required=True,
            )
        registry.update_client(client_id, {"enabled": new_state})
        record_audit("toggle_client", success=True, detail=f"Set client '{client_id}' enabled={new_state}")
        status_txt = "enabled" if new_state else "disabled"
        flash(f"AI Client '{client_id}' is now {status_txt}.", "info")
    except KeyError:
        abort(404, description="AI Client not found")
    return redirect(url_for("admin.clients_list"))


@bp.route("/clients/<client_id>/grants")
@login_required
def client_grants(client_id: str):
    registry = get_registry()
    try:
        editing_grant = None
        edit_id = request.args.get("edit", "").strip()
        if edit_id:
            editing_grant = _grant_for_client_or_404(registry, client_id, int(edit_id))
        return render_template("client_grants.html", **_client_grants_context(registry, client_id, editing_grant=editing_grant))
    except KeyError:
        abort(404, description="AI Client not found")
    except ValueError:
        abort(404, description="Grant not found")


@bp.route("/clients/<client_id>/grants/add", methods=["POST"])
@login_required
def client_grant_add(client_id: str):
    registry = get_registry()
    try:
        data = _grant_form_data(registry, client_id)
        if data["enabled"]:
            record_audit(
                "add_grant_attempt",
                target_id=data["target_id"],
                project_id=data["project_id"],
                detail=f"client={client_id} capability={data['capability']}",
                required=True,
            )
        grant_id = registry.add_grant(data)
        record_audit(
            "add_grant",
            target_id=data["target_id"],
            project_id=data["project_id"],
            detail=f"client={client_id} grant_id={grant_id} capability={data['capability']} enabled={data['enabled']}",
        )
        flash(f"Grant #{grant_id} created for '{client_id}'.", "success")
    except KeyError:
        abort(404, description="AI Client not found")
    except ValueError as exc:
        flash(str(exc), "danger")
    return redirect(url_for("admin.client_grants", client_id=client_id))


@bp.route("/clients/<client_id>/grants/<int:grant_id>/edit", methods=["POST"])
@login_required
def client_grant_edit(client_id: str, grant_id: int):
    registry = get_registry()
    current_grant = _grant_for_client_or_404(registry, client_id, grant_id)
    try:
        data = _grant_form_data(registry, client_id)
        if data["enabled"]:
            record_audit(
                "update_grant_attempt",
                target_id=data["target_id"],
                project_id=data["project_id"],
                detail=(
                    f"client={client_id} grant_id={grant_id} "
                    f"old_capability={current_grant.get('capability')} "
                    f"new_capability={data['capability']}"
                ),
                required=True,
            )
        registry.update_grant(grant_id, {k: v for k, v in data.items() if k != "client_id"})
        record_audit(
            "update_grant",
            target_id=data["target_id"],
            project_id=data["project_id"],
            detail=f"client={client_id} grant_id={grant_id} capability={data['capability']} enabled={data['enabled']}",
        )
        flash(f"Grant #{grant_id} updated.", "success")
    except ValueError as exc:
        flash(str(exc), "danger")
        return redirect(url_for("admin.client_grants", client_id=client_id, edit=grant_id))
    return redirect(url_for("admin.client_grants", client_id=client_id))


@bp.route("/clients/<client_id>/grants/<int:grant_id>/toggle", methods=["POST"])
@login_required
def client_grant_toggle(client_id: str, grant_id: int):
    registry = get_registry()
    grant = _grant_for_client_or_404(registry, client_id, grant_id)
    new_state = not grant.get("enabled", True)
    if new_state:
        record_audit(
            "toggle_grant_attempt",
            target_id=grant.get("target_id"),
            project_id=grant.get("project_id"),
            detail=f"client={client_id} grant_id={grant_id} enable=true",
            required=True,
        )
    registry.update_grant(grant_id, {"enabled": new_state})
    record_audit(
        "toggle_grant",
        target_id=grant.get("target_id"),
        project_id=grant.get("project_id"),
        detail=f"client={client_id} grant_id={grant_id} enabled={new_state}",
    )
    flash(f"Grant #{grant_id} is now {'enabled' if new_state else 'disabled'}.", "info")
    return redirect(url_for("admin.client_grants", client_id=client_id))


@bp.route("/clients/<client_id>/grants/<int:grant_id>/delete", methods=["POST"])
@login_required
def client_grant_delete(client_id: str, grant_id: int):
    registry = get_registry()
    grant = _grant_for_client_or_404(registry, client_id, grant_id)
    registry.delete_grant(grant_id)
    record_audit(
        "delete_grant",
        target_id=grant.get("target_id"),
        project_id=grant.get("project_id"),
        detail=f"client={client_id} grant_id={grant_id} capability={grant.get('capability')}",
    )
    flash(f"Grant #{grant_id} deleted.", "warning")
    return redirect(url_for("admin.client_grants", client_id=client_id))


@bp.route("/clients/<client_id>/grants/check", methods=["POST"])
@login_required
def client_grant_check(client_id: str):
    registry = get_registry()
    try:
        registry.get_client(client_id)
    except KeyError:
        abort(404, description="AI Client not found")

    target_id = request.form.get("target_id", "").strip()
    project_id = request.form.get("project_id", "").strip()
    tool_name = request.form.get("tool_name", "").strip()
    if tool_name not in TOOL_CAPABILITIES:
        flash("Unknown tool selected for access check.", "danger")
        return redirect(url_for("admin.client_grants", client_id=client_id))

    policy_target = None if target_id in ("", "*") else target_id
    policy_project = None if project_id in ("", "*") else project_id
    allowed, error = authorize_client(
        client_id=client_id,
        target_id=policy_target,
        project_id=policy_project,
        tool_name=tool_name,
        registry=registry,
    )
    access_result = {
        "allowed": allowed,
        "message": "ALLOWED" if allowed else (error or "DENIED"),
        "target_id": target_id or "*",
        "project_id": project_id or "*",
        "tool_name": tool_name,
    }
    record_audit(
        "check_grant_access",
        target_id=policy_target,
        project_id=policy_project,
        success=allowed,
        error_code=None if allowed else "ACCESS_DENIED",
        detail=f"client={client_id} tool={tool_name} result={access_result['message']}",
    )
    return render_template("client_grants.html", **_client_grants_context(registry, client_id, access_result=access_result))


# ==========================================
# Activity Audit
# ==========================================

def _activity_datetime_bound(value: str, *, end: bool = False) -> str:
    value = str(value or "").strip().replace("T", " ")
    if len(value) == 16:
        value += ":59" if end else ":00"
    return value


@bp.route("/activity")
@login_required
def activity_view():
    registry = get_registry()
    try:
        page = max(1, int(request.args.get("page", 1)))
    except ValueError:
        page = 1
    per_page = 50
    offset = (page - 1) * per_page

    result = request.args.get("result", "").strip().lower()
    filters = {
        "actor": request.args.get("actor", "").strip(),
        "action": request.args.get("action", "").strip(),
        "target_id": request.args.get("target_id", "").strip(),
        "project_id": request.args.get("project_id", "").strip(),
        "from_timestamp": _activity_datetime_bound(request.args.get("from", "")),
        "to_timestamp": _activity_datetime_bound(request.args.get("to", ""), end=True),
    }
    if result == "pass":
        filters["success"] = True
    elif result == "deny":
        filters["success"] = False
    filters = {key: value for key, value in filters.items() if value not in ("", None)}

    total = registry.get_activity_count(filters=filters)
    total_pages = max(1, (total + per_page - 1) // per_page)
    if page > total_pages:
        page = total_pages
        offset = (page - 1) * per_page
    items = registry.list_activity(limit=per_page, offset=offset, filters=filters)

    query_filters = {
        "actor": request.args.get("actor", "").strip(),
        "action": request.args.get("action", "").strip(),
        "target_id": request.args.get("target_id", "").strip(),
        "project_id": request.args.get("project_id", "").strip(),
        "result": result if result in ("pass", "deny") else "",
        "from": request.args.get("from", "").strip(),
        "to": request.args.get("to", "").strip(),
    }
    query_args = {
        key: value
        for key, value in query_filters.items()
        if value
    }
    live = request.args.get("live", "").strip() == "1" and page == 1
    live_url = url_for(
        "admin.activity_view",
        **query_args,
        page=1,
        live="1",
    )
    pause_live_url = url_for(
        "admin.activity_view",
        **query_args,
        page=1,
    )

    return render_template(
        "activity.html",
        items=items,
        page=page,
        total_pages=total_pages,
        total=total,
        filters=query_filters,
        live=live,
        live_url=live_url,
        pause_live_url=pause_live_url,
    )


# ==========================================
# System Info (Read-Only)
# ==========================================

@bp.route("/system")
@login_required
def system_view():
    registry = get_registry()

    # Hardware & OS details
    hostname = socket.gethostname()
    os_name = platform.platform()
    python_ver = platform.python_version()
    arch = platform.machine()
    kernel = platform.release()

    # Device model (Raspberry Pi specific)
    model = "Generic Linux"
    if os.path.exists("/proc/device-tree/model"):
        try:
            with open("/proc/device-tree/model", "r") as f:
                model = f.read().replace("\x00", "").strip()
        except Exception:
            pass

    # Disk usage
    total_disk, used_disk, free_disk = 0, 0, 0
    try:
        du = shutil.disk_usage("/")
        total_disk = du.total // (1024 * 1024)
        free_disk = du.free // (1024 * 1024)
        used_disk = du.used // (1024 * 1024)
    except Exception:
        pass

    # Memory usage
    mem_total, mem_available = 0, 0
    try:
        with open("/proc/meminfo", "r") as f:
            for line in f:
                if line.startswith("MemTotal:"):
                    mem_total = int(line.split()[1]) // 1024
                elif line.startswith("MemAvailable:"):
                    mem_available = int(line.split()[1]) // 1024
    except Exception:
        pass

    # DB size
    db_path = getattr(registry, "db_path", "in-memory")
    db_size_bytes = 0
    if os.path.exists(db_path):
        db_size_bytes = os.path.getsize(db_path)

    backend_type = type(registry).__name__

    return render_template(
        "system.html",
        hostname=hostname,
        model=model,
        arch=arch,
        os_name=os_name,
        kernel=kernel,
        python_ver=python_ver,
        total_disk=total_disk,
        used_disk=used_disk,
        free_disk=free_disk,
        mem_total=mem_total,
        mem_available=mem_available,
        gateway_version=__version__,
        backend_type=backend_type,
        db_path=db_path,
        db_size_bytes=db_size_bytes,
    )


# ==========================================
# Settings & Kill Switch
# ==========================================

@bp.route("/settings", methods=["GET", "POST"])
@login_required
def settings_view():
    registry = get_registry()

    if request.method == "GET":
        settings = {
            "gateway_enabled": str(registry.get_setting("gateway_enabled", "true")).lower() in ("true", "1", "yes", "on"),
            "writes_enabled": str(registry.get_setting("writes_enabled", "false")).lower() in ("true", "1", "yes", "on"),
            "shell_enabled": str(registry.get_setting("shell_enabled", "false")).lower() in ("true", "1", "yes", "on"),
            "default_timeout": int(registry.get_setting("default_timeout", 30)),
            "max_output_bytes": int(registry.get_setting("max_output_bytes", 262144)),
            "max_file_read_bytes": int(registry.get_setting("max_file_read_bytes", 1048576)),
            "max_write_bytes": int(registry.get_setting("max_write_bytes", 262144)),
            "max_diff_bytes": int(registry.get_setting("max_diff_bytes", 65536)),
            "activity_retention": int(registry.get_setting("activity_retention", 5000)),
        }
        return render_template("settings.html", settings=settings)

    # POST update settings
    try:
        timeout = int(request.form.get("default_timeout", 30))
        max_output = int(request.form.get("max_output_bytes", 262144))
        max_read = int(request.form.get("max_file_read_bytes", 1048576))
        max_write = int(request.form.get("max_write_bytes", 262144))
        retention = int(request.form.get("activity_retention", 5000))

        if not (5 <= timeout <= 300):
            flash("Timeout must be between 5 and 300 seconds.", "danger")
            return redirect(url_for("admin.settings_view"))
        if not (1024 <= max_output <= 10485760):
            flash("Max output bytes must be between 1KB and 10MB.", "danger")
            return redirect(url_for("admin.settings_view"))
        if not (1024 <= max_read <= 10485760):
            flash("Max file read bytes must be between 1KB and 10MB.", "danger")
            return redirect(url_for("admin.settings_view"))
        if not (1024 <= max_write <= 10485760):
            flash("Max write bytes must be between 1KB and 10MB.", "danger")
            return redirect(url_for("admin.settings_view"))
        if not (100 <= retention <= 50000):
            flash("Activity retention must be between 100 and 50000 rows.", "danger")
            return redirect(url_for("admin.settings_view"))

        record_audit(
            "update_settings_attempt",
            detail="Update operational limits",
            required=True,
        )
        registry.set_setting("default_timeout", timeout)
        registry.set_setting("max_output_bytes", max_output)
        registry.set_setting("max_file_read_bytes", max_read)
        registry.set_setting("max_write_bytes", max_write)
        registry.set_setting("activity_retention", retention)

        record_audit("update_settings", success=True, detail="Updated configuration settings")
        flash("Settings saved successfully.", "success")
    except ValueError:
        flash("Invalid numeric value provided.", "danger")

    return redirect(url_for("admin.settings_view"))


@bp.route("/settings/kill-switch", methods=["POST"])
@login_required
def toggle_kill_switch():
    registry = get_registry()
    curr = str(registry.get_setting("gateway_enabled", "true")).lower() in ("true", "1", "yes", "on")
    new_state = not curr
    if new_state:
        record_audit(
            "toggle_kill_switch_attempt",
            detail="Enable gateway operations",
            required=True,
        )
    registry.set_setting("gateway_enabled", "true" if new_state else "false")
    record_audit(
        "toggle_kill_switch",
        success=True,
        detail=f"Admin toggled gateway_enabled to {new_state}"
    )
    msg = "Gateway enabled. Operations permitted." if new_state else "KILL SWITCH ACTIVATED: Gateway disabled. Operations blocked."
    cat = "success" if new_state else "danger"
    flash(msg, cat)
    return redirect(url_for("admin.settings_view"))


@bp.route("/settings/toggle-writes", methods=["POST"])
@login_required
def toggle_writes_switch():
    registry = get_registry()
    curr = str(registry.get_setting("writes_enabled", "false")).lower() in ("true", "1", "yes", "on")
    new_state = not curr
    if new_state:
        record_audit(
            "toggle_writes_switch_attempt",
            detail="Enable structured filesystem writes",
            required=True,
        )
    registry.set_setting("writes_enabled", "true" if new_state else "false")
    record_audit(
        "toggle_writes_switch",
        success=True,
        detail=f"Admin toggled writes_enabled to {new_state}"
    )
    msg = "Structured filesystem writes ENABLED for authorized projects." if new_state else "Structured filesystem writes DISABLED globally."
    cat = "warning" if new_state else "info"
    flash(msg, cat)
    return redirect(url_for("admin.settings_view"))


@bp.route("/settings/toggle-shell", methods=["POST"])
@login_required
def toggle_shell_switch():
    registry = get_registry()
    curr = str(registry.get_setting("shell_enabled", "false")).lower() in ("true", "1", "yes", "on")
    new_state = not curr
    if new_state:
        record_audit(
            "toggle_shell_switch_attempt",
            detail="Enable trusted Target shell",
            required=True,
        )
    registry.set_setting("shell_enabled", "true" if new_state else "false")
    record_audit("toggle_shell_switch", success=True, detail=f"Admin toggled shell_enabled to {new_state}")
    msg = "Trusted target shell ENABLED for explicitly granted clients." if new_state else "Trusted target shell DISABLED globally."
    flash(msg, "warning" if new_state else "info")
    return redirect(url_for("admin.settings_view"))


@bp.route("/settings/disable-writes", methods=["POST"])
@login_required
def disable_writes():
    """Emergency Panic Switch for writes: immediately disables all write mutations without affecting read tools."""
    registry = get_registry()
    registry.set_setting("writes_enabled", "false")
    record_audit(
        "disable_writes_panic",
        success=True,
        detail="Admin activated emergency disable for controlled writes"
    )
    flash("PANIC: Structured filesystem writes are DISABLED. Read tools and separately granted run_command remain independent.", "warning")
    return redirect(url_for("admin.settings_view"))


# ==========================================
# Maintenance & Diagnostics
# ==========================================

@bp.route("/maintenance", methods=["GET"])
@login_required
def maintenance_view():
    from .. import compatibility
    from ..doctor import run_doctor
    from ..lifecycle import get_paths
    from ..bridge import ALLOWED_TOOLS

    overall, checks = run_doctor(verbose=False)
    compat = compatibility.get_compatibility()
    paths = get_paths()

    backups = []
    if os.path.isdir(paths["backups"]):
        for fname in sorted(os.listdir(paths["backups"]), reverse=True):
            fpath = os.path.join(paths["backups"], fname)
            if os.path.isfile(fpath):
                st = os.stat(fpath)
                backups.append({
                    "name": fname,
                    "size_kb": int(st.st_size / 1024),
                    "mtime": time.strftime("%Y-%m-%d %H:%M:%S", time.localtime(st.st_mtime)),
                })

    current_target = os.path.realpath(paths["current"]) if os.path.exists(paths["current"]) else "Standard Installation (/home/mcp-gateway/mcp-gateway)"
    previous_target = os.path.realpath(paths["previous"]) if os.path.exists(paths["previous"]) else None

    return render_template(
        "maintenance.html",
        overall_status=overall,
        checks=checks,
        compat=compat,
        tool_count=len(ALLOWED_TOOLS),
        backups=backups,
        current_target=current_target,
        previous_target=previous_target,
    )


@bp.route("/maintenance/doctor", methods=["POST"])
@login_required
def maintenance_doctor():
    flash("Diagnostics refreshed.", "info")
    return redirect(url_for("admin.maintenance_view"))


@bp.route("/maintenance/backup", methods=["POST"])
@login_required
def maintenance_backup():
    from ..lifecycle import backup_database
    try:
        path = backup_database()
        record_audit("admin_backup", success=True, detail=f"Created backup at {path}")
        flash(f"Online database backup created: {os.path.basename(path)}", "success")
    except Exception as e:
        record_audit("admin_backup", success=False, detail=str(e))
        flash(f"Backup failed: {e}", "danger")
    return redirect(url_for("admin.maintenance_view"))


@bp.route("/maintenance/repair", methods=["POST"])
@login_required
def maintenance_repair():
    from ..doctor import run_repair
    repairs = run_repair()
    record_audit("admin_repair", success=True, detail=f"Executed repairs: {repairs}")
    if repairs:
        flash(f"Repairs completed: {', '.join(repairs)}", "success")
    else:
        flash("No repair actions required; permissions and units are in desired state.", "info")
    return redirect(url_for("admin.maintenance_view"))


@bp.route("/maintenance/rollback", methods=["POST"])
@login_required
def maintenance_rollback():
    from ..lifecycle import rollback_release
    ok, msg = rollback_release()
    record_audit("admin_rollback", success=ok, detail=msg)
    flash(msg, "warning" if ok else "danger")
    return redirect(url_for("admin.maintenance_view"))


