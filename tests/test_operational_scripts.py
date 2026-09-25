import os
import subprocess
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]


class TestOperationalScripts(unittest.TestCase):
    def test_shell_syntax(self):
        checks = [
            (["sh", "-n", str(ROOT / "install.sh")], ROOT),
            (["bash", "-n", str(ROOT / "scripts" / "deploy-pi.sh")], ROOT),
            (["bash", "-n", str(ROOT / "scripts" / "backup-appliance.sh")], ROOT),
            (["bash", "-n", str(ROOT / "scripts" / "build-release-package.sh")], ROOT),
            (["bash", "-n", str(ROOT / "scripts" / "run-resumable.sh")], ROOT),
        ]
        for argv, cwd in checks:
            with self.subTest(argv=argv):
                cp = subprocess.run(argv, cwd=cwd, capture_output=True, text=True)
                self.assertEqual(cp.returncode, 0, cp.stderr)

    def test_installer_fail_fast_contract(self):
        text = (ROOT / "install.sh").read_text(encoding="utf-8")
        for required in (
            "install.sh must run as root",
            "INSTALLATION_VERIFIED",
            "ROLLBACK_VERIFIED",
            "--check",
            "--rollback",
            "MCP_GATEWAY_ADAPTER_BINARY",
            "mcp-gateway.previous-install",
            "admin.env",
            "gateway-pre-install-",
            "mcp-gateway-cli.absent",
            "mcp-gateway-tunnel-check.absent",
            'restore_system_files',
            'as_service "$INSTALL_DIR/bin/mcp-gateway" doctor',
            '"$adapter" -help',
            '-pythonpath "${SOURCE_DIR}/src" -version',
            'adapter/Core version contract validation failed',
        ):
            self.assertIn(required, text)
        self.assertNotIn("trap rollback_on_error ERR", text)
        self.assertNotIn('doctor || true', text)
        self.assertNotIn('systemctl restart mcp-gateway-mcp || true', text)

    def test_release_package_is_exact_commit_and_contains_prebuilt_adapter(self):
        text = (ROOT / "scripts" / "build-release-package.sh").read_text(encoding="utf-8")
        for required in (
            "release packaging requires a clean worktree",
            'git -C "$ROOT" archive "$SHA"',
            "GOARCH=arm GOARM=6",
            'bin/mcp-gateway-adapter',
            'sha256sum',
            'SOURCE_DATE_EPOCH=',
            'gzip -n -9',
        ):
            self.assertIn(required, text)

    def test_deploy_is_exact_commit_and_has_rollback(self):
        text = (ROOT / "scripts" / "deploy-pi.sh").read_text(encoding="utf-8")
        for required in (
            'git -C "${PROJECT_ROOT}" archive',
            'deployment requires a completely clean Git working tree',
            'origin/${DEPLOY_BRANCH}',
            'MCP_DEPLOY_INJECT_FAILURE',
            '/tmp/mcp-gateway-deploy-',
            'sudo tar -xzf "$archive"',
            'sudo chown -R mcp-gateway:mcp-gateway "$candidate"',
            'sudo test -f "$unit_backup/$unit"',
            'admin_env="$config_dir/admin.env"',
            'if ! sudo test -s "$admin_env"; then',
            'MCP_ADMIN_ALLOWED_HOSTS=${allowed}',
            'ALREADY_DEPLOYED commit=${DEPLOY_SHA}',
            'DEPLOYMENT_VERIFIED already_deployed=true',
            'MCP_DEPLOY_FORCE',
            'MCP_PI_RESUMABLE_JOB_ID',
            'MCP_DEPLOY_ALLOW_DIRECT',
            'CONTROL_PLANE_RESTART=EXPECTED',
            'CONTROL_PLANE_RESTORED',
            'ROLLBACK_REMOTE_FAILED',
            'ROLLBACK_VERIFIED',
            "REMOTE_REGISTRY_BACKUP",
            'backup "$registry_backup"',
            "restore_database",
            "rollback_registry=",
            '.deployment.json',
            "'verified': True",
        ):
            self.assertIn(required, text)
        verified_index = text.index("'verified': True")
        acceptance_index = text.index('[7/10] Running lightweight production acceptance')
        self.assertGreater(verified_index, acceptance_index)
        self.assertIn('"$candidate/manifest.json"', text)
        self.assertIn("manifest['tool_catalog']", text)
        self.assertIn("m['tool_count'] == len(ALLOWED_TOOLS)", text)
        self.assertNotIn("m['tool_catalog_version'] == 3", text)
        restore_index = text.index("if restore_database(sys.argv[1]) is not True")
        rollback_restart_index = text.index("sudo systemctl restart mcp-gateway-admin")
        self.assertLess(restore_index, rollback_restart_index)

    def test_deploy_refuses_direct_execution_without_break_glass(self):
        env = os.environ.copy()
        env.pop("MCP_PI_RESUMABLE_JOB_ID", None)
        env.pop("MCP_DEPLOY_ALLOW_DIRECT", None)
        cp = subprocess.run(
            ["bash", str(ROOT / "scripts" / "deploy-pi.sh"), "HEAD"],
            cwd=ROOT,
            env=env,
            capture_output=True,
            text=True,
        )
        self.assertNotEqual(cp.returncode, 0)
        self.assertIn("must run via scripts/run-resumable.sh", cp.stderr)

    def test_backup_age_is_optional_and_fail_closed(self):
        text = (ROOT / "scripts" / "backup-appliance.sh").read_text(encoding="utf-8")
        self.assertIn('BACKUP_AGE_RECIPIENT="${BACKUP_AGE_RECIPIENT:-}"', text)
        self.assertIn("optional 'age' binary is not installed", text)
        self.assertIn('refusing plaintext fallback', text)
        self.assertIn('age -r "${BACKUP_AGE_RECIPIENT}"', text)


if __name__ == "__main__":
    unittest.main()
