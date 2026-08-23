#!/usr/bin/env python3
"""Integration tests for durable AGY worker execution and cancellation."""

from __future__ import annotations

import os
import subprocess
import sys
import tempfile
import threading
import time
import unittest
from pathlib import Path
from unittest import mock

from agy_control_plane import ControlPlane
from agy_worker import execute_task, parse_duration_seconds, run_once


FAKE_DISPATCH = """#!/usr/bin/env python3
import argparse
import json
import os
import subprocess
import sys
import time
from pathlib import Path

parser = argparse.ArgumentParser(add_help=False)
parser.add_argument('--prompt-file')
parser.add_argument('--out')
args, _ = parser.parse_known_args()
prompt = Path(args.prompt_file).read_text()
if 'sleep' in prompt:
    if 'signal' in prompt:
        Path(args.prompt_file).with_name('child.pid').write_text(str(os.getpid()))
    time.sleep(10)
if 'orphan child' in prompt:
    child = subprocess.Popen([sys.executable, '-c', 'import time; time.sleep(10)'])
    Path(args.prompt_file).with_name('child.pid').write_text(str(child.pid))
if 'large output' in prompt:
    print('x' * 200000)
if 'non utf8' in prompt:
    os.write(1, b'valid\\xffinvalid')
Path(args.out).parent.mkdir(parents=True, exist_ok=True)
result = (
    {'ok': False, 'status': 'NEEDS_INFO', 'required_inputs': ['scope']}
    if 'needs info' in prompt
    else {'ok': True, 'status': 'ok'}
)
Path(args.out).write_text(json.dumps(result))
"""


class AgyWorkerTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temp_dir = tempfile.TemporaryDirectory()
        self.root = Path(self.temp_dir.name)
        self.control_plane = ControlPlane(self.root / "state.sqlite3")
        self.fake_dispatch = self.root / "fake_dispatch.py"
        self.fake_dispatch.write_text(FAKE_DISPATCH, encoding="utf-8")

    def tearDown(self) -> None:
        self.temp_dir.cleanup()

    def submit(self, key: str, prompt: str, timeout: str = "2s", max_attempts: int = 1) -> dict:
        return self.control_plane.submit_task(
            request={
                "prompt": prompt,
                "model": "Gemini 3.5 Flash (Low)",
                "role": "collector",
                "permission": "read_only",
                "timeout": timeout,
                "add_dirs": [str(self.root)],
                "skip_permissions": False,
                "no_sandbox": False,
            },
            idempotency_key=key,
            max_attempts=max_attempts,
        )

    def wait_for_state(self, task_id: str, state: str, timeout: float = 3.0) -> dict:
        deadline = time.time() + timeout
        while time.time() < deadline:
            task = self.control_plane.get_task(task_id)
            if task and task["state"] == state:
                return task
            time.sleep(0.02)
        self.fail(f"task {task_id} did not reach {state}")

    def assert_process_exited(self, pid: int, timeout: float = 3.0) -> None:
        deadline = time.time() + timeout
        while time.time() < deadline:
            try:
                os.kill(pid, 0)
            except ProcessLookupError:
                return
            time.sleep(0.02)
        self.fail(f"process {pid} is still alive")

    def test_parse_duration(self) -> None:
        self.assertEqual(0.25, parse_duration_seconds("250ms"))
        self.assertEqual(62.0, parse_duration_seconds("1m2s"))
        with self.assertRaises(ValueError):
            parse_duration_seconds("0s")

    def test_successful_worker_run_exports_receipt(self) -> None:
        task = self.submit("success", "collect")

        result = run_once(
            self.control_plane,
            worker_id="worker-a",
            lease_seconds=2,
            poll_seconds=0.02,
            dispatch_script=self.fake_dispatch,
        )

        self.assertEqual("SUCCEEDED", result["state"])
        receipt = self.root / "runs" / task["task_id"] / "attempt-1" / "receipt.json"
        self.assertTrue(receipt.exists())
        prompt = self.root / "runs" / task["task_id"] / "attempt-1" / "prompt.txt"
        self.assertFalse(prompt.exists())

    def test_running_task_cancellation_kills_process_group(self) -> None:
        task = self.submit("cancel", "sleep", timeout="5s")
        holder: list[dict | None] = []

        thread = threading.Thread(
            target=lambda: holder.append(
                run_once(
                    self.control_plane,
                    worker_id="worker-cancel",
                    lease_seconds=2,
                    poll_seconds=0.02,
                    dispatch_script=self.fake_dispatch,
                )
            )
        )
        thread.start()
        self.wait_for_state(task["task_id"], "RUNNING")
        self.control_plane.cancel_task(
            task["task_id"], actor="test", reason="cancel integration probe"
        )
        thread.join(timeout=5)

        self.assertFalse(thread.is_alive())
        self.assertEqual("CANCELLED", self.control_plane.get_task(task["task_id"])["state"])
        self.assertEqual("CANCELLED", holder[0]["state"])

    def test_timeout_exhausts_single_attempt(self) -> None:
        task = self.submit("timeout", "sleep", timeout="200ms")

        result = run_once(
            self.control_plane,
            worker_id="worker-timeout",
            lease_seconds=2,
            poll_seconds=0.02,
            dispatch_script=self.fake_dispatch,
        )

        self.assertEqual("FAILED", result["state"])
        self.assertEqual("timeout", result["result"]["status"])
        self.assertEqual("FAILED", self.control_plane.get_task(task["task_id"])["state"])

    def test_worker_can_pause_for_required_information(self) -> None:
        task = self.submit("needs-info", "needs info")

        result = run_once(
            self.control_plane,
            worker_id="worker-needs-info",
            lease_seconds=2,
            poll_seconds=0.02,
            dispatch_script=self.fake_dispatch,
        )

        self.assertEqual("NEEDS_INFO", result["state"])
        self.assertIsNone(result["lease_owner"])
        self.assertEqual("NEEDS_INFO", self.control_plane.get_task(task["task_id"])["state"])

    def test_large_and_non_utf8_output_do_not_deadlock_worker(self) -> None:
        for key, prompt in (("large-output", "large output"), ("non-utf8", "non utf8")):
            task = self.submit(key, prompt)
            result = run_once(
                self.control_plane,
                worker_id=f"worker-{key}",
                lease_seconds=2,
                poll_seconds=0.02,
                dispatch_script=self.fake_dispatch,
            )
            self.assertEqual("SUCCEEDED", result["state"])
            run_dir = self.root / "runs" / task["task_id"] / "attempt-1"
            self.assertFalse((run_dir / "worker.stdout").exists())
            self.assertFalse((run_dir / "worker.stderr").exists())

    def test_unhandled_exception_kills_child_and_removes_prompt(self) -> None:
        task = self.submit("exception-cleanup", "sleep", timeout="5s")
        claimed = self.control_plane.claim_next("worker-exception", lease_seconds=2)
        with mock.patch.object(
            self.control_plane,
            "start_task",
            side_effect=RuntimeError("injected failure"),
        ):
            with self.assertRaisesRegex(RuntimeError, "injected failure"):
                execute_task(
                    self.control_plane,
                    claimed,
                    worker_id="worker-exception",
                    lease_seconds=2,
                    poll_seconds=0.02,
                    dispatch_script=self.fake_dispatch,
                )
        run_dir = self.root / "runs" / task["task_id"] / "attempt-1"
        self.assertFalse((run_dir / "prompt.txt").exists())

    @unittest.skipIf(os.name == "nt", "POSIX process-group assertion")
    def test_worker_sigterm_kills_active_child(self) -> None:
        task = self.submit("signal-cleanup", "signal sleep", timeout="5s")
        worker = subprocess.Popen(
            [
                sys.executable,
                str(Path(__file__).with_name("agy_worker.py")),
                "--db",
                str(self.control_plane.db_path),
                "--once",
                "--worker-id",
                "worker-signal",
                "--poll-seconds",
                "0.02",
                "--dispatch-script",
                str(self.fake_dispatch),
            ],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        self.wait_for_state(task["task_id"], "RUNNING")
        pid_path = self.root / "runs" / task["task_id"] / "attempt-1" / "child.pid"
        deadline = time.time() + 3
        while not pid_path.exists() and time.time() < deadline:
            time.sleep(0.02)
        child_pid = int(pid_path.read_text())

        worker.terminate()
        self.assertEqual(143, worker.wait(timeout=5))
        self.assert_process_exited(child_pid)

    @unittest.skipIf(os.name == "nt", "POSIX process-group assertion")
    def test_successful_leader_exit_does_not_leave_background_child(self) -> None:
        task = self.submit("orphan-cleanup", "orphan child")
        result = run_once(
            self.control_plane,
            worker_id="worker-orphan",
            lease_seconds=2,
            poll_seconds=0.02,
            dispatch_script=self.fake_dispatch,
        )
        pid_path = self.root / "runs" / task["task_id"] / "attempt-1" / "child.pid"

        self.assertEqual("SUCCEEDED", result["state"])
        self.assert_process_exited(int(pid_path.read_text()))


if __name__ == "__main__":
    unittest.main()
