"""Flask Application Factory for MCP Gateway Admin Console."""

import os
import secrets
from datetime import timedelta
from typing import Optional
from flask import Flask, render_template

from ..tools import GatewayTools
from ..registry import SQLiteRegistry, get_default_db_path, get_registry
from .csrf import check_csrf, get_csrf_token
from .security import apply_security_headers, check_trusted_host
from .views import bp as admin_bp, auth_bp


def get_or_create_secret_key(secret_file: Optional[str] = None) -> str:
    """Retrieve secret key from a protected file or generate one with 600 permissions."""
    if not secret_file:
        secret_file = os.environ.get("MCP_ADMIN_SECRET_FILE")
        if not secret_file:
            config_dir = os.path.expanduser("~/.config/mcp-gateway")
            secret_file = os.path.join(config_dir, "admin-secret")

    if os.path.exists(secret_file):
        with open(secret_file, "r", encoding="utf-8") as f:
            key = f.read().strip()
            if key:
                return key

    key = secrets.token_hex(32)
    secret_dir = os.path.dirname(os.path.abspath(secret_file))
    os.makedirs(secret_dir, exist_ok=True)
    # Create with 600 permissions. Runtime startup fails closed if persistence fails.
    flags = os.O_WRONLY | os.O_CREAT | os.O_TRUNC
    mode = 0o600
    fd = os.open(secret_file, flags, mode)
    with os.fdopen(fd, "w", encoding="utf-8") as f:
        f.write(key)
    return key


def create_app(
    registry=None,
    tools: Optional[GatewayTools] = None,
    secret_key: Optional[str] = None,
    test_config: Optional[dict] = None,
) -> Flask:
    """Create and configure an instance of the Flask application."""
    app = Flask(
        __name__,
        template_folder=os.path.join(os.path.dirname(__file__), "templates"),
        static_folder=os.path.join(os.path.dirname(__file__), "static"),
    )

    resolved_secret = secret_key or get_or_create_secret_key()
    admin_host = os.environ.get("MCP_ADMIN_HOST", "127.0.0.1")
    allowed_hosts = os.environ.get(
        "MCP_ADMIN_ALLOWED_HOSTS",
        f"127.0.0.1,localhost,{admin_host}",
    )
    app.config.from_mapping(
        SECRET_KEY=resolved_secret,
        SESSION_COOKIE_HTTPONLY=True,
        SESSION_COOKIE_SAMESITE="Strict",
        SESSION_COOKIE_SECURE=False,  # LAN HTTP supported; prefer SSH tunnel on untrusted networks.
        PERMANENT_SESSION_LIFETIME=timedelta(minutes=30),
        ADMIN_ALLOWED_HOSTS={h.strip().lower() for h in allowed_hosts.split(",") if h.strip()},
    )

    if test_config:
        app.config.update(test_config)

    # Injected dependencies
    reg = registry or get_registry()
    app.config["REGISTRY"] = reg
    app.config["TOOLS"] = tools or GatewayTools(registry=reg)

    # Reject DNS rebinding / unexpected Host headers before auth and CSRF handling.
    app.before_request(check_trusted_host)

    # CSRF check on mutating requests
    app.before_request(check_csrf)

    # Security headers on all responses
    app.after_request(apply_security_headers)

    # Register blueprints
    app.register_blueprint(auth_bp)
    app.register_blueprint(admin_bp)

    # Template helpers
    @app.context_processor
    def inject_helpers():
        return {
            "csrf_token": get_csrf_token,
        }

    # Error handlers
    @app.errorhandler(403)
    def forbidden(e):
        return render_template("login.html", error=str(e)), 403

    @app.errorhandler(404)
    def not_found(e):
        return "Not Found", 404

    @app.errorhandler(500)
    def server_error(e):
        return "Internal Server Error", 500

    return app


def run_server(host: str = "127.0.0.1", port: int = 8080):
    """Run server binding strictly to localhost via standard WSGI server."""
    from wsgiref.simple_server import make_server

    app = create_app()
    print(f"Starting MCP Gateway Admin Console on http://{host}:{port}")
    if host in ("127.0.0.1", "localhost"):
        print("Admin Console is loopback-only; use an SSH tunnel for remote access.")
    else:
        print("Admin Console LAN binding enabled; auth, CSRF, and Host allowlist remain enforced.")

    httpd = make_server(host, port, app)
    try:
        httpd.serve_forever()
    except KeyboardInterrupt:
        print("\nStopping Admin Console...")


if __name__ == "__main__":
    run_server()
