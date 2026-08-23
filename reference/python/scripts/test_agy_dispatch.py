#!/usr/bin/env python3
"""Focused tests for the AGY dispatch wrapper."""

from __future__ import annotations

import argparse
import io
import json
import subprocess
import sys
import tempfile
import unittest
from contextlib import redirect_stdout
from pathlib import Path
from unittest import mock

import agy_dispatch


def _args(**overrides: object) -> argparse.Namespace:
    values = {
        "model": "Gemini 3.5 Flash (Low)",
        "timeout": "1s",
        "skip_permissions": False,
        "no_sandbox": False,
        "add_dir": [],
    }
    values.update(overrides)
    return argparse.Namespace(**values)


class AgyDispatchGuardTest(unittest.TestCase):
    def test_resolve_agy_binary_prefers_explicit_path(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            explicit = Path(tmp) / "agy"
            env_agy = Path(tmp) / "env-agy"
            explicit.write_text("# explicit\n", encoding="utf-8")
            env_agy.write_text("# env\n", encoding="utf-8")

            resolved = agy_dispatch.resolve_agy_binary(
                str(explicit),
                env={"AGY_BIN": str(env_agy)},
                which=lambda _name: None,
            )

            self.assertEqual(explicit, resolved)

    def test_resolve_agy_binary_uses_environment_before_path(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            env_agy = Path(tmp) / "env-agy"
            path_agy = Path(tmp) / "path-agy"
            env_agy.write_text("# env\n", encoding="utf-8")
            path_agy.write_text("# path\n", encoding="utf-8")

            resolved = agy_dispatch.resolve_agy_binary(
                None,
                env={"AGY_BIN": str(env_agy)},
                which=lambda _name: str(path_agy),
            )

            self.assertEqual(env_agy, resolved)

    def test_resolve_agy_binary_uses_path_lookup(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            path_agy = Path(tmp) / "agy"
            path_agy.write_text("# path\n", encoding="utf-8")

            resolved = agy_dispatch.resolve_agy_binary(
                None,
                env={},
                which=lambda name: str(path_agy) if name == "agy" else None,
            )

            self.assertEqual(path_agy, resolved)

    def test_resolve_agy_binary_adds_windows_suffix_for_explicit_base(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp) / "agy"
            cmd_file = Path(f"{base}.cmd")
            cmd_file.write_text("@echo off\n", encoding="utf-8")

            resolved = agy_dispatch.resolve_agy_binary(
                str(base),
                env={},
                platform="win32",
                which=lambda _name: None,
            )

            self.assertEqual(cmd_file, resolved)

    def test_resolve_agy_binary_checks_home_fallback_suffixes(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            home = Path(tmp)
            fallback = home / "AppData" / "Roaming" / "npm" / "agy.cmd"
            fallback.parent.mkdir(parents=True)
            fallback.write_text("@echo off\n", encoding="utf-8")

            resolved = agy_dispatch.resolve_agy_binary(
                None,
                env={},
                platform="win32",
                home=home,
                which=lambda _name: None,
            )

            self.assertEqual(fallback, resolved)

    def test_skip_permissions_keeps_sandbox_by_default(self) -> None:
        cmd = agy_dispatch.build_agy_command(
            _args(skip_permissions=True),
            Path("/tmp/agy"),
            "collect read-only evidence",
        )

        self.assertIn("--dangerously-skip-permissions", cmd)
        self.assertIn("--sandbox", cmd)

    def test_no_sandbox_explicitly_omits_sandbox(self) -> None:
        cmd = agy_dispatch.build_agy_command(
            _args(skip_permissions=True, no_sandbox=True),
            Path("/tmp/agy"),
            "collect read-only evidence",
        )

        self.assertIn("--dangerously-skip-permissions", cmd)
        self.assertNotIn("--sandbox", cmd)

    def test_read_only_detector_ignores_negative_instruction(self) -> None:
        violations = agy_dispatch.detect_read_only_contract_violations(
            "Do not run wiki.py reflect. Report only.",
            "",
        )

        self.assertEqual([], violations)

    def test_read_only_detector_flags_reflect_side_effect(self) -> None:
        violations = agy_dispatch.detect_read_only_contract_violations(
            "OK\nSession logged to log.md\n",
            "",
        )

        self.assertEqual("wiki_reflect_side_effect", violations[0]["type"])

    def test_main_turns_zero_exit_side_effect_into_harness_failure(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            fake_agy = Path(tmp) / "agy"
            fake_agy.write_text("# fake binary placeholder\n", encoding="utf-8")
            out_path = Path(tmp) / "result.json"
            argv = [
                "agy_dispatch.py",
                "--prompt",
                "Collect paths only.",
                "--agy-bin",
                str(fake_agy),
                "--permission",
                "automation_read",
                "--skip-permissions",
                "--out",
                str(out_path),
            ]
            completed = subprocess.CompletedProcess(
                args=[],
                returncode=0,
                stdout="Session logged to log.md\n",
                stderr="",
            )

            with mock.patch.object(sys, "argv", argv), mock.patch(
                "agy_dispatch.run_agy_command", return_value=completed
            ) as run_mock:
                exit_code = agy_dispatch.main()

            self.assertEqual(agy_dispatch.READ_ONLY_CONTRACT_EXIT, exit_code)
            cmd = run_mock.call_args.args[0]
            self.assertIn("--dangerously-skip-permissions", cmd)
            self.assertIn("--sandbox", cmd)

            result = json.loads(out_path.read_text(encoding="utf-8"))
            self.assertFalse(result["ok"])
            self.assertEqual(0, result["returncode"])
            self.assertEqual(agy_dispatch.READ_ONLY_CONTRACT_EXIT, result["harness_returncode"])
            self.assertEqual("read_only", result["contract_violation"]["policy"])

    def test_print_stdout_is_sanitized(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            fake_agy = Path(tmp) / "agy"
            fake_agy.write_text("# fake binary placeholder\n", encoding="utf-8")
            argv = [
                "agy_dispatch.py",
                "--prompt",
                "Collect paths only.",
                "--agy-bin",
                str(fake_agy),
                "--print-stdout",
            ]
            completed = subprocess.CompletedProcess(
                args=[],
                returncode=0,
                stdout="postgresql://user:password@example.invalid/db\n",
                stderr="",
            )
            output = io.StringIO()

            with mock.patch.object(sys, "argv", argv), mock.patch(
                "agy_dispatch.run_agy_command", return_value=completed
            ), redirect_stdout(output):
                exit_code = agy_dispatch.main()

            self.assertEqual(0, exit_code)
            self.assertNotIn("password", output.getvalue())
            self.assertIn("<REDACTED>", output.getvalue())

    def test_command_capture_replaces_invalid_utf8_and_bounds_output(self) -> None:
        completed = agy_dispatch.run_agy_command(
            [
                sys.executable,
                "-c",
                "import os; os.write(1, b'valid\\xffinvalid' + b'x' * 3000000)",
            ]
        )

        self.assertEqual(0, completed.returncode)
        self.assertIn("valid\ufffdinvalid", completed.stdout)
        self.assertIn("<OUTPUT_TRUNCATED>", completed.stdout)
        self.assertLess(len(completed.stdout), agy_dispatch.MAX_CAPTURE_BYTES + 100)


if __name__ == "__main__":
    unittest.main()
