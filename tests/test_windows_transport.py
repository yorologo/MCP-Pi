import base64
import os
import re
import subprocess
import sys
import unittest
import zlib
from types import SimpleNamespace
from unittest.mock import patch

sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "src")))

from mcp_gateway.policy import PolicyError, path_module_for_platform, validate_canonical_path
from mcp_gateway.ssh_transport import SSHTransport, build_remote_python_command


def decode_helper(command: str) -> str:
    match = re.search(r"b64decode\('([^']+)'\)", command)
    if not match:
        raise AssertionError(f"helper payload not found: {command}")
    return zlib.decompress(base64.b64decode(match.group(1))).decode("utf-8")


class TestWindowsTransport(unittest.TestCase):
    def test_python_helper_keeps_dynamic_values_out_of_shell_command(self):
        path = r"C:\Users\esaud\OneDrive\Escritorio\Proyectos\Me\file with spaces.txt"
        command = build_remote_python_command(
            "import sys; print(sys.argv[1])",
            [path],
        )

        self.assertTrue(command.startswith('python3 -c "'))
        self.assertNotIn(path, command)
        self.assertNotIn("realpath -m", command)
        self.assertNotIn("2>/dev/null", command)

        script = decode_helper(command)
        self.assertIn(path.replace("\\", "\\\\"), script)
        self.assertIn("print(sys.argv[1])", script)

    def test_windows_paths_are_case_insensitive_and_drive_scoped(self):
        root = r"C:\Users\esaud\OneDrive\Escritorio\Proyectos\Me"

        validate_canonical_path(
            r"c:\users\ESAUD\OneDrive\Escritorio\Proyectos\Me\src\main.py",
            root,
            "windows",
        )

        for outside in (
            r"C:\Users\esaud\OneDrive\Escritorio\Proyectos\Me-evil\secret.txt",
            r"D:\Me\secret.txt",
        ):
            with self.subTest(outside=outside):
                with self.assertRaises(PolicyError) as ctx:
                    validate_canonical_path(outside, root, "windows")
                self.assertEqual(ctx.exception.code, "PATH_OUTSIDE_ALLOWED_ROOT")

    def test_windows_path_module_normalizes_remote_paths(self):
        pathmod = path_module_for_platform("windows")
        path = pathmod.normpath(
            pathmod.join(
                r"C:\Users\esaud\OneDrive\Escritorio\Proyectos\Me",
                "src/main.py",
            )
        )
        self.assertEqual(
            path,
            r"C:\Users\esaud\OneDrive\Escritorio\Proyectos\Me\src\main.py",
        )

    def test_windows_cwd_and_env_are_applied_without_posix_shell_prefixes(self):
        transport = SSHTransport(identity_file="/tmp/unused")
        target = {
            "id": "MSI",
            "platform": "windows",
            "host": "192.0.2.10",
            "port": 22,
            "user": "tester",
        }
        cwd = r"C:\Users\esaud\OneDrive\Escritorio\Proyectos\Me"

        completed = SimpleNamespace(returncode=0, stdout=b"ok\r\n", stderr=b"")
        with patch.object(transport, "_build_ssh_args", return_value=["ssh", "MSI"]), patch(
            "mcp_gateway.ssh_transport.subprocess.run",
            return_value=completed,
        ) as run:
            result = transport.run_command(
                target,
                "git status --short",
                cwd=cwd,
                env={"MCP_TEST": "value with spaces"},
            )

        self.assertTrue(result.ok)
        ssh_args = run.call_args.args[0]
        remote_command = ssh_args[-1]
        self.assertNotIn("export ", remote_command)
        self.assertNotIn("cd ", remote_command)
        self.assertNotIn(cwd, remote_command)

        script = decode_helper(remote_command)
        self.assertIn("git status --short", script)
        self.assertIn(cwd.replace("\\", "\\\\"), script)
        self.assertIn('"MCP_TEST": "value with spaces"', script)
        self.assertIn("os.chdir(cwd)", script)
        self.assertIn("subprocess.run(command, shell=True)", script)

    @unittest.skipUnless(os.name == "nt", "requires cmd.exe")
    def test_helper_executes_under_windows_cmd(self):
        value = r"C:\Path With Spaces\file.txt"
        command = build_remote_python_command("import sys; print(sys.argv[1])", [value])
        command = command.replace("python3 -c ", f'"{sys.executable}" -c ', 1)

        proc = subprocess.run(
            command,
            shell=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            check=False,
        )

        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertEqual(proc.stdout.strip(), value)


if __name__ == "__main__":
    unittest.main()
