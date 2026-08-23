#!/usr/bin/env python3
"""Tests for the macOS LaunchAgent service manager."""

from __future__ import annotations

import json
import os
import plistlib
import stat
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from agy_control_plane import ControlPlane
from agy_service import (
    ServiceError,
    build_config,
    build_plist,
    install_service,
    restart_service,
    service_status,
    uninstall_service,
    validate_config,
)


class FakeRunner:
    def __init__(
        self,
        *,
        loaded: bool = False,
        fail_bootstrap: bool = False,
        callback: object | None = None,
    ):
        self.loaded = loaded
        self.fail_bootstrap = fail_bootstrap
        self.callback = callback
        self.commands: list[list[str]] = []

    def __call__(self, command: list[str], **_kwargs: object) -> subprocess.CompletedProcess[str]:
        self.commands.append(command)
        if callable(self.callback):
            self.callback(command)
        if command[:2] == ["launchctl", "print"]:
            return subprocess.CompletedProcess(command, 0 if self.loaded else 113, "", "")
        if command[:2] == ["launchctl", "bootout"]:
            self.loaded = False
            return subprocess.CompletedProcess(command, 0, "", "")
        if command[:2] == ["launchctl", "bootstrap"]:
            if self.fail_bootstrap:
                return subprocess.CompletedProcess(command, 5, "", "bootstrap failed")
            self.loaded = True
            return subprocess.CompletedProcess(command, 0, "", "")
        if command[:2] == ["launchctl", "kickstart"]:
            return subprocess.CompletedProcess(command, 0, "4242\n", "")
        if command[:2] == ["plutil", "-lint"]:
            return subprocess.CompletedProcess(command, 0, "OK\n", "")
        return subprocess.CompletedProcess(command, 0, "", "")


class TimeoutRunner:
    def __call__(self, command: list[str], **_kwargs: object) -> subprocess.CompletedProcess[str]:
        raise subprocess.TimeoutExpired(command, 30)


class AgyServiceTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temp_dir = tempfile.TemporaryDirectory()
        self.root = Path(self.temp_dir.name)
        self.scripts = self.root / "scripts"
        self.scripts.mkdir()
        self.worker = self.scripts / "agy_worker.py"
        self.worker.write_text("#!/usr/bin/env python3\n", encoding="utf-8")
        self.agy = self.root / "agy"
        self.agy.write_text("#!/bin/sh\nexit 0\n", encoding="utf-8")
        self.agy.chmod(0o700)
        self.uid = os.getuid()
        self.db_path = self.root / "state" / "control-plane.sqlite3"
        self.plist_path = self.root / "LaunchAgents" / "com.test.agy-worker.plist"
        self.config = build_config(
            label="com.test.agy-worker",
            python_path=sys.executable,
            worker_script=self.worker,
            agy_path=self.agy,
            db_path=self.db_path,
            plist_path=self.plist_path,
            uid=self.uid,
        )

    def tearDown(self) -> None:
        self.temp_dir.cleanup()

    def request(self) -> dict:
        return {
            "prompt": "Collect paths only.",
            "model": "Gemini 3.5 Flash (Low)",
            "role": "collector",
            "permission": "read_only",
            "timeout": "5s",
            "add_dirs": [str(self.root)],
            "skip_permissions": False,
            "no_sandbox": False,
        }

    def test_plist_is_private_bounded_and_uses_quiet_worker(self) -> None:
        payload = build_plist(self.config)
        encoded = plistlib.dumps(payload)
        decoded = plistlib.loads(encoded)

        self.assertEqual("com.test.agy-worker", decoded["Label"])
        self.assertTrue(decoded["KeepAlive"])
        self.assertNotIn("RunAtLoad", decoded)
        self.assertEqual("Background", decoded["ProcessType"])
        self.assertEqual(0o077, decoded["Umask"])
        self.assertIn("--quiet", decoded["ProgramArguments"])
        self.assertEqual(str(self.config.agy_path), decoded["EnvironmentVariables"]["AGY_BIN"])
        self.assertNotIn(str(Path.home() / ".local" / "bin"), decoded["EnvironmentVariables"]["PATH"])
        self.assertTrue(all(Path(item).is_absolute() for item in decoded["ProgramArguments"][:2]))
        self.assertNotIn("API", encoded.decode("utf-8"))
        self.assertNotIn("TOKEN", encoded.decode("utf-8"))

    def test_install_writes_plist_bootstraps_and_reports_loaded(self) -> None:
        runner = FakeRunner()

        result = install_service(self.config, runner=runner, platform_name="darwin")

        self.assertTrue(result["loaded"])
        self.assertEqual("install", result["operation"])
        self.assertTrue(self.plist_path.is_file())
        self.assertEqual(0o644, stat.S_IMODE(self.plist_path.stat().st_mode))
        self.assertEqual(0o600, stat.S_IMODE(self.config.stdout_path.stat().st_mode))
        self.assertIn(
            ["launchctl", "bootstrap", f"gui/{self.uid}", str(self.config.plist_path)],
            runner.commands,
        )
        self.assertIn(
            ["launchctl", "kickstart", "-p", f"gui/{self.uid}/com.test.agy-worker"],
            runner.commands,
        )

    def test_reinstall_boots_out_loaded_service_before_bootstrap(self) -> None:
        self.plist_path.parent.mkdir(parents=True)
        self.plist_path.write_bytes(b"previous")
        runner = FakeRunner(loaded=True)

        result = install_service(self.config, runner=runner, platform_name="darwin")

        self.assertTrue(result["replaced"])
        bootout_index = runner.commands.index(
            ["launchctl", "bootout", f"gui/{self.uid}/com.test.agy-worker"]
        )
        bootstrap_index = runner.commands.index(
            ["launchctl", "bootstrap", f"gui/{self.uid}", str(self.config.plist_path)]
        )
        self.assertLess(bootout_index, bootstrap_index)

    def test_python_symlink_is_pinned_to_canonical_path(self) -> None:
        executable = self.root / "python3"
        executable.symlink_to(Path(sys.executable))

        config = build_config(
            label="com.test.python-symlink",
            python_path=executable,
            worker_script=self.worker,
            agy_path=self.agy,
            db_path=self.db_path,
            plist_path=self.plist_path,
            uid=self.uid,
        )

        self.assertEqual(executable.resolve(), config.python_path)

    def test_world_writable_executable_is_rejected(self) -> None:
        self.agy.chmod(0o777)

        with self.assertRaisesRegex(ServiceError, "group/world-writable"):
            validate_config(self.config, platform_name="darwin")

    def test_failed_bootstrap_restores_previous_plist(self) -> None:
        previous = b"previous-plist"
        self.plist_path.parent.mkdir(parents=True)
        self.plist_path.write_bytes(previous)
        runner = FakeRunner(fail_bootstrap=True)

        with self.assertRaisesRegex(ServiceError, "bootstrap failed"):
            install_service(self.config, runner=runner, platform_name="darwin")

        self.assertEqual(previous, self.plist_path.read_bytes())

    def test_lifecycle_change_refuses_active_task_without_force(self) -> None:
        control_plane = ControlPlane(self.db_path)
        task = control_plane.submit_task(request=self.request(), idempotency_key="active-task")
        claimed = control_plane.claim_next("worker", lease_seconds=30)
        control_plane.start_task(task["task_id"], "worker", claimed["lease_token"])
        runner = FakeRunner()

        with self.assertRaisesRegex(ServiceError, "leased or running"):
            install_service(self.config, runner=runner, platform_name="darwin")
        self.assertEqual([], runner.commands)

    def test_maintenance_blocks_claim_during_install(self) -> None:
        control_plane = ControlPlane(self.db_path)
        task = control_plane.submit_task(request=self.request(), idempotency_key="race-task")
        claims: list[dict | None] = []

        def attempt_claim(command: list[str]) -> None:
            if command[:2] == ["launchctl", "print"]:
                claims.append(control_plane.claim_next("racing-worker", lease_seconds=30))

        install_service(
            self.config,
            runner=FakeRunner(callback=attempt_claim),
            platform_name="darwin",
        )

        self.assertTrue(claims)
        self.assertTrue(all(claim is None for claim in claims))
        self.assertEqual(
            task["task_id"],
            control_plane.claim_next("after-maintenance", lease_seconds=30)["task_id"],
        )

    def test_symlinked_log_is_rejected_without_touching_target(self) -> None:
        victim = self.root / "victim.log"
        victim.write_text("unchanged", encoding="utf-8")
        self.config.stdout_path.parent.mkdir(parents=True)
        self.config.stdout_path.symlink_to(victim)

        with self.assertRaisesRegex(ServiceError, "cannot prepare private service log"):
            install_service(self.config, runner=FakeRunner(), platform_name="darwin")

        self.assertEqual("unchanged", victim.read_text(encoding="utf-8"))

    def test_symlinked_plist_is_rejected_without_touching_target(self) -> None:
        victim = self.root / "victim.plist"
        victim.write_text("unchanged", encoding="utf-8")
        self.plist_path.parent.mkdir(parents=True)
        self.plist_path.symlink_to(victim)

        with self.assertRaisesRegex(ServiceError, "symlinked LaunchAgent plist"):
            install_service(self.config, runner=FakeRunner(), platform_name="darwin")

        self.assertEqual("unchanged", victim.read_text(encoding="utf-8"))

    def test_restart_requires_loaded_installation(self) -> None:
        with self.assertRaisesRegex(ServiceError, "not installed and loaded"):
            restart_service(self.config, runner=FakeRunner(), platform_name="darwin")

    def test_launchctl_timeout_fails_closed(self) -> None:
        with self.assertRaisesRegex(ServiceError, "timed out"):
            install_service(self.config, runner=TimeoutRunner(), platform_name="darwin")

    def test_uninstall_removes_plist_but_preserves_database(self) -> None:
        ControlPlane(self.db_path)
        self.plist_path.parent.mkdir(parents=True)
        self.plist_path.write_bytes(plistlib.dumps(build_plist(self.config)))
        runner = FakeRunner(loaded=True)

        result = uninstall_service(self.config, runner=runner, platform_name="darwin")

        self.assertFalse(self.plist_path.exists())
        self.assertTrue(self.db_path.exists())
        self.assertTrue(result["state_preserved"])
        self.assertIn(
            ["launchctl", "bootout", f"gui/{self.uid}/com.test.agy-worker"], runner.commands
        )

    def test_status_is_sanitized_and_does_not_create_database(self) -> None:
        result = service_status(self.config, runner=FakeRunner(), platform_name="darwin")

        self.assertFalse(result["loaded"])
        self.assertFalse(result["database_exists"])
        self.assertNotIn("prompt", json.dumps(result))
        self.assertFalse(self.db_path.exists())

    def test_invalid_database_fails_closed(self) -> None:
        self.db_path.parent.mkdir(parents=True)
        self.db_path.write_bytes(b"not sqlite")

        with self.assertRaisesRegex(ServiceError, "cannot inspect control-plane state"):
            service_status(self.config, runner=FakeRunner(), platform_name="darwin")

    def test_non_macos_is_rejected(self) -> None:
        with self.assertRaisesRegex(ServiceError, "only on macOS"):
            validate_config(self.config, platform_name="linux")


if __name__ == "__main__":
    unittest.main()
