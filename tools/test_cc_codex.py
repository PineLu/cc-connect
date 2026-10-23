"""Regression tests for the project-scoped Codex CLI entrypoint.

All functional checks use a temporary HOME and never read the user's desktop
Codex credentials. They invoke --help/--version only, not interactive prompts.
"""
import os
import stat
import subprocess
import tempfile
import tomllib
import unittest
from pathlib import Path

LAUNCHER = Path(__file__).with_name("cc-codex")
CLI = Path("/opt/homebrew/bin/codex")


@unittest.skipUnless(CLI.is_file(), "Codex CLI not installed at Homebrew path")
class CodexLauncherTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="cc-codex-test-")
        self.addCleanup(self.tmp.cleanup)
        self.home = Path(self.tmp.name)
        self.parent = self.home / ".cc-connect"
        self.parent.mkdir(mode=0o700)
        self.private_home = self.parent / ".codex-home"
        self.env = {**os.environ, "HOME": str(self.home)}
        self.env.pop("CODEX_HOME", None)

    def command(self, *args):
        return subprocess.run(
            [str(LAUNCHER), *args], env=self.env,
            text=True, capture_output=True, timeout=15,
        )

    def bootstrap(self):
        proc = self.command("login", "--help")
        self.assertEqual(proc.returncode, 0, proc.stderr)
        return self.private_home / "config.toml"

    def test_first_login_bootstraps_private_home_and_luna_medium(self):
        self.assertNotEqual(self.command("--version").returncode, 0)
        self.assertFalse(self.private_home.exists())
        cfg = self.bootstrap()
        self.assertTrue(self.private_home.is_dir())
        self.assertEqual(stat.S_IMODE(self.private_home.stat().st_mode), 0o700)
        self.assertEqual(stat.S_IMODE(cfg.stat().st_mode), 0o600)
        data = tomllib.loads(cfg.read_text())
        self.assertEqual(data["model"], "gpt-6-luna")
        self.assertEqual(data["model_reasoning_effort"], "medium")
        self.assertEqual(data["sandbox_mode"], "workspace-write")
        self.assertFalse((self.private_home / "auth.json").exists())
        self.assertFalse((self.home / ".codex").exists())

    def test_no_args_dispatches_to_interactive_cli_not_acp(self):
        self.bootstrap()
        env = {**self.env, "TERM": "dumb"}
        result = subprocess.run(
            [str(LAUNCHER)], env=env, stdin=subprocess.DEVNULL,
            text=True, capture_output=True, timeout=12,
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue(
            "interactive TUI" in result.stderr or "terminal" in result.stderr,
            result.stderr,
        )

    def test_native_cli_commands_and_options_are_forwarded(self):
        self.bootstrap()
        for args, expected in (
            (("--version",), "codex-cli"),
            (("--help",), "Usage: codex"),
            (("exec", "--help"), "Usage: codex exec"),
            (("resume", "--help"), "Usage: codex resume"),
            (("review", "--help"), "Usage: codex review"),
            (("-m", "gpt-6-luna", "--help"), "Usage: codex"),
        ):
            with self.subTest(args=args):
                result = self.command(*args)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn(expected, result.stdout)
        self.assertFalse((self.home / ".codex").exists())

    def test_existing_config_is_preserved_by_login(self):
        self.private_home.mkdir(mode=0o700)
        config = self.private_home / "config.toml"
        config.write_text('model = "gpt-6-luna"\nmodel_reasoning_effort = "high"\n')
        before = config.read_bytes()
        self.assertEqual(self.command("login", "--help").returncode, 0)
        self.assertEqual(config.read_bytes(), before)

    def test_refuses_symlinked_auth_and_config(self):
        self.private_home.mkdir(mode=0o700)
        desktop_dir = self.home / ".codex"
        desktop_dir.mkdir()
        auth = desktop_dir / "auth.json"
        auth.write_text('{"test":"private"}')
        (self.private_home / "auth.json").symlink_to(auth)
        result = self.command("--version")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("must not be symlinked", result.stderr)
        (self.private_home / "auth.json").unlink()
        cfg = desktop_dir / "config.toml"
        cfg.write_text('model = "gpt-6-sol"\n')
        (self.private_home / "config.toml").symlink_to(cfg)
        result = self.command("--version")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("must not be symlinked", result.stderr)

    def test_refuses_missing_parent_and_home_symlink(self):
        self.parent.rmdir()
        result = self.command("--version")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("data directory missing", result.stderr)
        self.parent.mkdir(mode=0o700)
        other = self.home / "elsewhere"
        other.mkdir()
        self.private_home.symlink_to(other, target_is_directory=True)
        result = self.command("--version")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("private CODEX_HOME missing", result.stderr)


if __name__ == "__main__":
    unittest.main()
