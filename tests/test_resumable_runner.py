import os
import stat
import subprocess
import tempfile
import time
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
RUNNER = ROOT / "scripts" / "run-resumable.sh"


class TestResumableRunner(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.state = Path(self.tmp.name) / "jobs"
        self.env = os.environ.copy()
        self.env["LOCAL_MINIMCP_STATE_DIR"] = str(self.state)

    def tearDown(self):
        self.tmp.cleanup()

    def run_runner(self, *args, check=False):
        return subprocess.run(
            ["bash", str(RUNNER), *args],
            cwd=ROOT,
            env=self.env,
            text=True,
            capture_output=True,
            check=check,
        )

    def wait_terminal(self, job, timeout=10.0):
        deadline = time.time() + timeout
        last = ""
        while time.time() < deadline:
            cp = self.run_runner("status", job)
            self.assertEqual(cp.returncode, 0, cp.stderr)
            last = cp.stdout
            if any(f"STATE={state}" in last for state in ("VERIFIED", "FINISHED", "FAILED", "INTERRUPTED")):
                return last
            time.sleep(0.05)
        self.fail(f"job did not reach terminal state: {last}")

    def test_verified_job_persists_state_without_command_arguments(self):
        secret = "do-not-persist-this-argument"
        cp = self.run_runner(
            "start",
            "--expect-marker",
            "DEPLOYMENT_VERIFIED",
            "verify-job",
            "--",
            "sh",
            "-c",
            'printf "DEPLOYMENT_VERIFIED\\n"; : "$1"',
            "runner-test",
            secret,
        )
        self.assertEqual(cp.returncode, 0, cp.stderr)
        status = self.wait_terminal("verify-job")
        self.assertIn("STATE=VERIFIED", status)
        self.assertIn("RC=0", status)
        job_dir = self.state / "verify-job"
        meta = (job_dir / "meta").read_text()
        self.assertNotIn(secret, meta)
        self.assertIn("command_name=sh", meta)
        self.assertEqual(stat.S_IMODE(job_dir.stat().st_mode), 0o700)
        for name in ("meta", "log", "rc"):
            self.assertEqual(stat.S_IMODE((job_dir / name).stat().st_mode), 0o600)
        log = self.run_runner("log", "verify-job").stdout
        self.assertIn("DEPLOYMENT_VERIFIED", log)

    def test_worker_exports_resumable_job_context(self):
        cp = self.run_runner(
            "start",
            "context-job",
            "--",
            "sh",
            "-c",
            'printf "JOB_ID=%s\nSTATE_DIR=%s\n" "$MCP_PI_RESUMABLE_JOB_ID" "$MCP_PI_RESUMABLE_STATE_DIR"',
        )
        self.assertEqual(cp.returncode, 0, cp.stderr)
        status = self.wait_terminal("context-job")
        self.assertIn("STATE=FINISHED", status)
        log = self.run_runner("log", "context-job").stdout
        self.assertIn("JOB_ID=context-job", log)
        self.assertIn(str(self.state / "context-job"), log)

    def test_duplicate_job_id_is_denied_while_running(self):
        first = self.run_runner("start", "same-job", "--", "sh", "-c", "sleep 0.8")
        self.assertEqual(first.returncode, 0, first.stderr)
        second = self.run_runner("start", "same-job", "--", "true")
        self.assertNotEqual(second.returncode, 0)
        self.assertIn("already running", second.stderr)
        self.wait_terminal("same-job")

    def test_missing_rc_with_free_lock_is_interrupted_not_retried(self):
        job = self.state / "interrupted"
        job.mkdir(parents=True)
        (job / "lock").touch()
        (job / "log").write_text("")
        (job / "meta").write_text("job=interrupted\nexpected_marker=\n")
        cp = self.run_runner("status", "interrupted")
        self.assertEqual(cp.returncode, 0, cp.stderr)
        self.assertIn("STATE=INTERRUPTED", cp.stdout)

    def test_cleanup_refuses_running_job_then_removes_terminal_job(self):
        cp = self.run_runner("start", "cleanup-job", "--", "sh", "-c", "sleep 0.8")
        self.assertEqual(cp.returncode, 0, cp.stderr)
        denied = self.run_runner("cleanup", "cleanup-job")
        self.assertNotEqual(denied.returncode, 0)
        self.assertIn("refusing cleanup", denied.stderr)
        self.wait_terminal("cleanup-job")
        cleaned = self.run_runner("cleanup", "cleanup-job")
        self.assertEqual(cleaned.returncode, 0, cleaned.stderr)
        self.assertFalse((self.state / "cleanup-job").exists())


if __name__ == "__main__":
    unittest.main()
