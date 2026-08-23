#!/usr/bin/env python3
"""Conformance tests for the durable AGY control plane."""

from __future__ import annotations

import json
import sqlite3
import tempfile
import threading
import unittest
from pathlib import Path

from agy_control_plane import ControlPlane, content_hash


class FakeClock:
    def __init__(self, value: float = 100.0):
        self.value = value

    def __call__(self) -> float:
        return self.value

    def advance(self, seconds: float) -> None:
        self.value += seconds


class AgyControlPlaneTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temp_dir = tempfile.TemporaryDirectory()
        self.root = Path(self.temp_dir.name)
        self.db_path = self.root / "control-plane.sqlite3"
        self.clock = FakeClock()
        self.control_plane = ControlPlane(self.db_path, clock=self.clock)

    def tearDown(self) -> None:
        self.temp_dir.cleanup()

    def request(self, prompt: str = "Collect paths only.", **overrides: object) -> dict:
        request = {
            "prompt": prompt,
            "model": "Gemini 3.5 Flash (Low)",
            "role": "collector",
            "permission": "read_only",
            "timeout": "5s",
            "add_dirs": [str(self.root)],
            "skip_permissions": False,
            "no_sandbox": False,
        }
        request.update(overrides)
        return request

    def submit(self, key: str, **kwargs: object) -> dict:
        return self.control_plane.submit_task(
            request=self.request(),
            idempotency_key=key,
            **kwargs,
        )

    def test_submit_is_idempotent_for_same_request(self) -> None:
        first = self.submit("same-task")
        second = self.submit("same-task")

        self.assertEqual(first["task_id"], second["task_id"])
        self.assertTrue(second["deduplicated"])
        self.assertEqual(1, len(self.control_plane.list_tasks()))

    def test_idempotency_collision_rejects_different_request(self) -> None:
        self.submit("collision")

        with self.assertRaisesRegex(ValueError, "different request"):
            self.control_plane.submit_task(
                request=self.request(prompt="Different task."),
                idempotency_key="collision",
            )

    def test_child_task_preserves_lineage_and_requires_known_parent(self) -> None:
        parent = self.submit("parent")
        child = self.control_plane.submit_task(
            request=self.request(prompt="Continue with clarified scope."),
            idempotency_key="child",
            parent_task_id=parent["task_id"],
        )

        self.assertEqual(parent["task_id"], child["parent_task_id"])
        self.assertEqual(parent["task_id"], self.control_plane.build_receipt(child["task_id"])["parent_task_id"])
        with self.assertRaisesRegex(ValueError, "unknown parent task"):
            self.control_plane.submit_task(
                request=self.request(),
                idempotency_key="orphan",
                parent_task_id="00000000-0000-0000-0000-000000000000",
            )

    def test_idempotency_collision_rejects_different_parent(self) -> None:
        first_parent = self.submit("first-parent")
        second_parent = self.submit("second-parent")
        self.control_plane.submit_task(
            request=self.request(),
            idempotency_key="lineage-collision",
            parent_task_id=first_parent["task_id"],
        )

        with self.assertRaisesRegex(ValueError, "different request"):
            self.control_plane.submit_task(
                request=self.request(),
                idempotency_key="lineage-collision",
                parent_task_id=second_parent["task_id"],
            )

    def test_schema_v1_database_migrates_parent_column(self) -> None:
        legacy_path = self.root / "legacy.sqlite3"
        ControlPlane(legacy_path)
        connection = sqlite3.connect(legacy_path)
        connection.execute("ALTER TABLE tasks DROP COLUMN parent_task_id")
        connection.execute("PRAGMA user_version = 1")
        connection.commit()
        connection.close()

        ControlPlane(legacy_path)

        connection = sqlite3.connect(legacy_path)
        try:
            columns = {row[1] for row in connection.execute("PRAGMA table_info(tasks)")}
            version = connection.execute("PRAGMA user_version").fetchone()[0]
        finally:
            connection.close()
        self.assertIn("parent_task_id", columns)
        self.assertEqual(3, version)

    def test_maintenance_blocks_claims_until_owner_releases_it(self) -> None:
        task = self.submit("maintenance-block")

        self.assertEqual(0, self.control_plane.begin_maintenance("service-a", ttl_seconds=10))
        self.assertIsNone(self.control_plane.claim_next("worker-a", lease_seconds=10))
        self.assertEqual("QUEUED", self.control_plane.get_task(task["task_id"])["state"])
        self.assertTrue(self.control_plane.end_maintenance("service-a"))
        self.assertEqual(
            task["task_id"],
            self.control_plane.claim_next("worker-a", lease_seconds=10)["task_id"],
        )

    def test_active_task_count_is_not_limited_by_list_page_size(self) -> None:
        for index in range(201):
            task = self.submit(f"active-count-{index}")
            claimed = self.control_plane.claim_next(f"worker-{index}", lease_seconds=10)
            self.assertEqual(task["task_id"], claimed["task_id"])

        self.assertEqual(201, self.control_plane.active_task_count())

    def test_maintenance_is_single_owner_and_expires(self) -> None:
        self.control_plane.begin_maintenance("service-a", ttl_seconds=10)

        with self.assertRaisesRegex(RuntimeError, "already held"):
            self.control_plane.begin_maintenance("service-b", ttl_seconds=10)
        self.assertFalse(self.control_plane.end_maintenance("service-b"))
        self.clock.advance(11)
        self.assertEqual(0, self.control_plane.begin_maintenance("service-b", ttl_seconds=10))
        self.assertTrue(self.control_plane.end_maintenance("service-b"))

    def test_priority_controls_claim_order(self) -> None:
        low = self.submit("low", priority=-1)
        high = self.submit("high", priority=10)

        claimed = self.control_plane.claim_next("worker-a", lease_seconds=10)

        self.assertEqual(high["task_id"], claimed["task_id"])
        self.assertEqual("QUEUED", self.control_plane.get_task(low["task_id"])["state"])

    def test_concurrent_claim_has_single_winner(self) -> None:
        task = self.submit("single-winner")
        barrier = threading.Barrier(2)
        results: list[dict | None] = []

        def claim(worker_id: str) -> None:
            control_plane = ControlPlane(self.db_path, clock=self.clock)
            barrier.wait()
            results.append(control_plane.claim_next(worker_id, lease_seconds=10))

        threads = [threading.Thread(target=claim, args=(f"worker-{index}",)) for index in range(2)]
        for thread in threads:
            thread.start()
        for thread in threads:
            thread.join()

        winners = [result for result in results if result is not None]
        self.assertEqual(1, len(winners))
        self.assertEqual(task["task_id"], winners[0]["task_id"])

    def test_expired_lease_requeues_then_exhausts_retry_budget(self) -> None:
        task = self.submit("lease-expiry", max_attempts=2)
        first = self.control_plane.claim_next("worker-a", lease_seconds=10)
        self.assertEqual(1, first["attempts"])

        self.clock.advance(11)
        self.assertEqual(1, self.control_plane.reconcile_expired())
        self.assertEqual("QUEUED", self.control_plane.get_task(task["task_id"])["state"])

        second = self.control_plane.claim_next("worker-b", lease_seconds=10)
        self.assertEqual(2, second["attempts"])
        self.clock.advance(11)
        self.assertEqual(1, self.control_plane.reconcile_expired())
        self.assertEqual("FAILED", self.control_plane.get_task(task["task_id"])["state"])

    def test_heartbeat_rejects_stale_lease_token(self) -> None:
        task = self.submit("heartbeat")
        claimed = self.control_plane.claim_next("worker-a", lease_seconds=10)

        self.assertFalse(
            self.control_plane.heartbeat(
                task["task_id"], "worker-a", "wrong-token", lease_seconds=10
            )
        )
        self.assertTrue(
            self.control_plane.heartbeat(
                task["task_id"],
                "worker-a",
                claimed["lease_token"],
                lease_seconds=10,
            )
        )

    def test_execution_signal_distinguishes_cancel_from_lease_loss(self) -> None:
        task = self.submit("execution-signal")
        claimed = self.control_plane.claim_next("worker-a", lease_seconds=10)
        token = claimed["lease_token"]
        self.control_plane.start_task(task["task_id"], "worker-a", token)

        self.assertEqual(
            "active", self.control_plane.execution_signal(task["task_id"], "worker-a", token)
        )
        self.control_plane.cancel_task(task["task_id"], actor="test", reason="stop")
        self.assertEqual(
            "cancel_requested",
            self.control_plane.execution_signal(task["task_id"], "worker-a", token),
        )
        self.assertEqual(
            "lease_lost",
            self.control_plane.execution_signal(task["task_id"], "worker-b", "wrong"),
        )

    def test_cancel_queued_task_is_terminal(self) -> None:
        task = self.submit("cancel-queued")

        cancelled = self.control_plane.cancel_task(
            task["task_id"], actor="orchestrator", reason="no longer needed"
        )

        self.assertEqual("CANCELLED", cancelled["state"])
        self.assertIsNotNone(cancelled["receipt_hash"])
        self.assertNotIn("prompt", cancelled["request"])
        self.assertEqual("CANCELLED", self.control_plane.build_receipt(task["task_id"])["state"])
        self.assertIsNone(self.control_plane.claim_next("worker-a"))

    def test_running_cancel_is_completed_by_lease_owner(self) -> None:
        task = self.submit("cancel-running")
        claimed = self.control_plane.claim_next("worker-a", lease_seconds=10)
        token = claimed["lease_token"]
        self.assertTrue(self.control_plane.start_task(task["task_id"], "worker-a", token))

        pending = self.control_plane.cancel_task(
            task["task_id"], actor="orchestrator", reason="stop"
        )
        self.assertEqual("RUNNING", pending["state"])
        self.assertTrue(pending["cancel_requested"])

        final = self.control_plane.finish_attempt(
            task["task_id"],
            "worker-a",
            token,
            result={"ok": False, "status": "cancelled"},
            success=False,
            retryable=False,
        )
        self.assertEqual("CANCELLED", final["state"])

    def test_stale_worker_cannot_complete_task(self) -> None:
        task = self.submit("stale-completion")
        claimed = self.control_plane.claim_next("worker-a", lease_seconds=10)
        self.assertTrue(
            self.control_plane.start_task(task["task_id"], "worker-a", claimed["lease_token"])
        )

        with self.assertRaisesRegex(ValueError, "lease ownership lost"):
            self.control_plane.finish_attempt(
                task["task_id"],
                "worker-b",
                "wrong-token",
                result={"ok": True},
                success=True,
                retryable=False,
            )

    def test_receipt_hash_is_reproducible_and_unsigned(self) -> None:
        task = self.submit("receipt")
        claimed = self.control_plane.claim_next("worker-a", lease_seconds=10)
        token = claimed["lease_token"]
        self.control_plane.start_task(task["task_id"], "worker-a", token)
        self.control_plane.finish_attempt(
            task["task_id"],
            "worker-a",
            token,
            result={"ok": True, "findings": []},
            success=True,
            retryable=False,
        )

        receipt = self.control_plane.build_receipt(task["task_id"])
        persisted = self.control_plane.get_task(task["task_id"])
        self.assertNotIn("prompt", persisted["request"])
        self.assertTrue(persisted["request"]["prompt_redacted"])
        unsigned = {key: value for key, value in receipt.items() if key != "receipt_hash"}
        self.assertEqual(content_hash(unsigned), receipt["receipt_hash"])
        self.assertFalse(receipt["signed"])

        paths = self.control_plane.export_task(task["task_id"], self.root / "run")
        self.assertEqual(receipt, json.loads(Path(paths["receipt"]).read_text()))
        exported_task = json.loads(Path(paths["task"]).read_text())
        self.assertNotIn("prompt", exported_task["request"])
        self.assertTrue(exported_task["request"]["prompt_redacted"])
        self.assertRegex(exported_task["request"]["prompt_hash"], r"^[a-f0-9]{64}$")

    def test_retryable_task_keeps_prompt_only_while_requeued(self) -> None:
        task = self.submit("retry-prompt", max_attempts=2)
        claimed = self.control_plane.claim_next("worker-a", lease_seconds=10)
        self.control_plane.start_task(task["task_id"], "worker-a", claimed["lease_token"])

        requeued = self.control_plane.finish_attempt(
            task["task_id"],
            "worker-a",
            claimed["lease_token"],
            result={"ok": False, "status": "temporary_failure"},
            success=False,
            retryable=True,
        )

        self.assertEqual("QUEUED", requeued["state"])
        self.assertIn("prompt", requeued["request"])
        self.assertIsNone(requeued["receipt_hash"])

    def test_workspace_write_and_no_sandbox_are_blocked(self) -> None:
        with self.assertRaisesRegex(ValueError, "workspace_write"):
            self.control_plane.submit_task(
                request=self.request(permission="workspace_write"),
                idempotency_key="write",
            )
        with self.assertRaisesRegex(ValueError, "no_sandbox"):
            self.control_plane.submit_task(
                request=self.request(no_sandbox=True),
                idempotency_key="no-sandbox",
            )

    def test_scope_and_application_boundaries_are_mandatory(self) -> None:
        with self.assertRaisesRegex(ValueError, "explicit scope root"):
            self.control_plane.submit_task(
                request=self.request(add_dirs=[]),
                idempotency_key="empty-scope",
            )
        with self.assertRaisesRegex(ValueError, "custom agy_bin"):
            self.control_plane.submit_task(
                request=self.request(agy_bin="/tmp/not-verified"),
                idempotency_key="custom-binary",
            )
        with self.assertRaisesRegex(ValueError, "denied add-dir"):
            self.control_plane.submit_task(
                request=self.request(add_dirs=[str(Path.home())]),
                idempotency_key="broad-home-scope",
            )

    def test_traversal_to_sensitive_path_is_blocked(self) -> None:
        with self.assertRaisesRegex(ValueError, "denied add-dir"):
            self.control_plane.submit_task(
                request=self.request(add_dirs=[str(self.root / "safe" / ".." / ".." / ".ssh")]),
                idempotency_key="traversal",
            )


if __name__ == "__main__":
    unittest.main()
