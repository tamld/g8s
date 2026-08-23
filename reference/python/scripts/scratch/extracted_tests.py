#!/usr/bin/env python3
"""Comprehensive test suite for AGY Dispatch Receipt Delegation System."""

from __future__ import annotations

import json
import os
import sqlite3
import tempfile
import threading
import time
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
from typing import Any, Callable
from unittest import mock
import uuid

import pytest

from agy_control_plane import ControlPlane
from agy_harness import validate_dispatch


# ─────────────────────────────────────────────────────────────────────────────
# Fixtures
# ─────────────────────────────────────────────────────────────────────────────

@pytest.fixture
def temp_dir():
    """Create an isolated temporary directory for test artifacts."""
    with tempfile.TemporaryDirectory() as tmp:
        yield Path(tmp)


@pytest.fixture
def db_path(temp_dir: Path) -> Path:
    """Return an isolated SQLite database path."""
    return temp_dir / "control-plane.sqlite3"


@pytest.fixture
def control_plane(db_path: Path) -> ControlPlane:
    """Return an initialized ControlPlane pointing to the isolated database."""
    return ControlPlane(db_path=db_path)


@pytest.fixture
def configured_env(db_path: Path, monkeypatch: pytest.MonkeyPatch) -> Path:
    """Configure AGY_DISPATCH_STATE_DB environment variable for harness tests."""
    monkeypatch.setenv("AGY_DISPATCH_STATE_DB", str(db_path))
    return db_path


# ─────────────────────────────────────────────────────────────────────────────
# Category 1: Happy Path (5+ tests)
# ─────────────────────────────────────────────────────────────────────────────

class TestCategory1HappyPath:
    def test_issue_and_validate_flow(self, control_plane: ControlPlane) -> None:
        """Brain issues a write receipt and worker validates/consumes it."""
        issuer = "Brain-Opus"
        allowed_paths = ["/workspace/src/*.py", "/workspace/docs/*.md"]
        ttl = 300.0

        receipt = control_plane.issue_write_receipt(
            issuer=issuer,
            allowed_paths=allowed_paths,
            ttl_seconds=ttl,
        )

        assert "receipt_id" in receipt
        assert receipt["issuer"] == issuer
        assert receipt["allowed_paths"] == allowed_paths
        assert receipt["ttl_seconds"] == ttl
        assert receipt["expires_at"] > time.time()

        validated = control_plane.validate_write_receipt(receipt["receipt_id"])
        assert validated["valid"] is True
        assert validated["receipt_id"] == receipt["receipt_id"]
        assert validated["issuer"] == issuer
        assert validated["allowed_paths"] == allowed_paths

    def test_multiple_receipts_consumed_independently(self, control_plane: ControlPlane) -> None:
        """Multiple receipts can coexist and are consumed independently."""
        r1 = control_plane.issue_write_receipt(issuer="Brain", allowed_paths=["/a.py"])
        r2 = control_plane.issue_write_receipt(issuer="Brain", allowed_paths=["/b.py"])

        assert r1["receipt_id"] != r2["receipt_id"]

        # Validate r1 first; r2 must remain unconsumed
        v1 = control_plane.validate_write_receipt(r1["receipt_id"])
        assert v1["valid"] is True

        # Validating r1 again must fail
        with pytest.raises(ValueError, match="already consumed"):
            control_plane.validate_write_receipt(r1["receipt_id"])

        # r2 can still be consumed
        v2 = control_plane.validate_write_receipt(r2["receipt_id"])
        assert v2["valid"] is True

        # Validating r2 again must now fail
        with pytest.raises(ValueError, match="already consumed"):
            control_plane.validate_write_receipt(r2["receipt_id"])

    def test_receipt_with_various_allowed_paths_patterns(self, control_plane: ControlPlane) -> None:
        """Receipt supports single files, nested globs, and diverse path lists."""
        patterns = [
            ["/workspace/main.py"],
            ["/workspace/**/*.rs", "/workspace/**/Cargo.toml"],
            ["/opt/build/bin", "/opt/build/include/*.h", "/var/log/build.log"],
        ]
        for path_list in patterns:
            receipt = control_plane.issue_write_receipt(
                issuer="Architect",
                allowed_paths=path_list,
            )
            validated = control_plane.validate_write_receipt(receipt["receipt_id"])
            assert validated["allowed_paths"] == path_list

    def test_receipt_with_different_ttl_values(self, control_plane: ControlPlane) -> None:
        """Receipt functions properly at minimum (1s) and maximum (3600s) TTL boundaries."""
        r_min = control_plane.issue_write_receipt(issuer="Brain", allowed_paths=["/min"], ttl_seconds=1.0)
        assert r_min["ttl_seconds"] == 1.0
        v_min = control_plane.validate_write_receipt(r_min["receipt_id"])
        assert v_min["valid"] is True

        r_max = control_plane.issue_write_receipt(issuer="Brain", allowed_paths=["/max"], ttl_seconds=3600.0)
        assert r_max["ttl_seconds"] == 3600.0
        v_max = control_plane.validate_write_receipt(r_max["receipt_id"])
        assert v_max["valid"] is True

    def test_validate_records_consumer_task_id_and_db_state(
        self, control_plane: ControlPlane, db_path: Path
    ) -> None:
        """Validation records the consumer_task_id and marks consumed=1 in SQLite."""
        receipt = control_plane.issue_write_receipt(issuer="Brain", allowed_paths=["/data/*"])
        task_id = "task-uuid-4242"

        validated = control_plane.validate_write_receipt(
            receipt["receipt_id"], consumer_task_id=task_id
        )
        assert validated["valid"] is True

        # Direct database verification
        conn = sqlite3.connect(db_path)
        conn.row_factory = sqlite3.Row
        row = conn.execute(
            "SELECT * FROM write_receipts WHERE receipt_id = ?", (receipt["receipt_id"],)
        ).fetchone()
        conn.close()

        assert row is not None
        assert row["consumed"] == 1
        assert row["consumer_task_id"] == task_id
        assert row["issuer"] == "Brain"


# ─────────────────────────────────────────────────────────────────────────────
# Category 2: Security Boundary (8+ tests)
# ─────────────────────────────────────────────────────────────────────────────

class TestCategory2SecurityBoundary:
    def test_reuse_consumed_receipt_raises_value_error(self, control_plane: ControlPlane) -> None:
        """Attempting to consume an already-consumed receipt raises ValueError."""
        receipt = control_plane.issue_write_receipt(issuer="Brain", allowed_paths=["/a"])
        control_plane.validate_write_receipt(receipt["receipt_id"])

        with pytest.raises(ValueError, match="already consumed"):
            control_plane.validate_write_receipt(receipt["receipt_id"])

    def test_expired_receipt_with_mocked_clock(self, db_path: Path) -> None:
        """Mocked clock progression triggers expiration check deterministically."""
        current_time = 1_700_000_000.0

        def fake_clock() -> float:
            return current_time

        cp = ControlPlane(db_path=db_path, clock=fake_clock)
        receipt = cp.issue_write_receipt(issuer="Brain", allowed_paths=["/a"], ttl_seconds=60.0)

        # Advance clock past TTL
        current_time += 61.0

        with pytest.raises(ValueError, match=r"write receipt expired: .* \(expired 1s ago\)"):
            cp.validate_write_receipt(receipt["receipt_id"])

    def test_expired_receipt_with_real_sleep(self, control_plane: ControlPlane) -> None:
        """Real-time sleep past TTL triggers receipt expiration."""
        receipt = control_plane.issue_write_receipt(issuer="Brain", allowed_paths=["/a"], ttl_seconds=1.0)
        time.sleep(1.05)

        with pytest.raises(ValueError, match="write receipt expired"):
            control_plane.validate_write_receipt(receipt["receipt_id"])

    def test_nonexistent_receipt_id_raises_value_error(self, control_plane: ControlPlane) -> None:
        """Unknown receipt UUID raises ValueError."""
        fake_uuid = str(uuid.uuid4())
        with pytest.raises(ValueError, match=f"write receipt not found: {fake_uuid}"):
            control_plane.validate_write_receipt(fake_uuid)

    def test_empty_string_receipt_id_raises_value_error(self, control_plane: ControlPlane) -> None:
        """Empty string receipt ID raises ValueError."""
        with pytest.raises(ValueError, match="write receipt not found: "):
            control_plane.validate_write_receipt("")

    def test_sql_injection_in_receipt_id(self, control_plane: ControlPlane, db_path: Path) -> None:
        """SQL injection via receipt_id is blocked and does not tamper with the database."""
        injection_payload = "'; DROP TABLE write_receipts; --"
        with pytest.raises(ValueError, match="write receipt not found"):
            control_plane.validate_write_receipt(injection_payload)

        # Verify table is intact
        conn = sqlite3.connect(db_path)
        tables = [r[0] for r in conn.execute("SELECT name FROM sqlite_master WHERE type='table'").fetchall()]
        conn.close()
        assert "write_receipts" in tables

    def test_sql_injection_in_issuer(self, control_plane: ControlPlane, db_path: Path) -> None:
        """SQL injection in issuer field is safely parameterized."""
        malicious_issuer = "Brain'; UPDATE write_receipts SET consumed = 0; --"
        receipt = control_plane.issue_write_receipt(issuer=malicious_issuer, allowed_paths=["/p"])
        validated = control_plane.validate_write_receipt(receipt["receipt_id"])

        assert validated["issuer"] == malicious_issuer

        # Verify consumed state remains 1
        conn = sqlite3.connect(db_path)
        consumed = conn.execute(
            "SELECT consumed FROM write_receipts WHERE receipt_id = ?", (receipt["receipt_id"],)
        ).fetchone()[0]
        conn.close()
        assert consumed == 1

    def test_sql_injection_in_allowed_paths(self, control_plane: ControlPlane) -> None:
        """SQL injection strings in allowed_paths are safely serialized as JSON text."""
        malicious_paths = ["/safe', 'malicious'); DROP TABLE tasks; --"]
        receipt = control_plane.issue_write_receipt(issuer="Brain", allowed_paths=malicious_paths)
        validated = control_plane.validate_write_receipt(receipt["receipt_id"])

        assert validated["allowed_paths"] == malicious_paths

    def test_workspace_write_without_receipt_id_harness_blocks(
        self, temp_dir: Path, configured_env: Path
    ) -> None:
        """validate_dispatch blocks workspace_write if receipt_id is None."""
        with pytest.raises(
            ValueError,
            match="permission=workspace_write requires --receipt-id. Brain must issue a write receipt first",
        ):
            validate_dispatch(
                prompt="Update source code",
                role_name="verifier",
                permission_name="workspace_write",
                add_dirs=[str(temp_dir)],
                skip_permissions=True,
                receipt_id=None,
            )

    def test_workspace_write_with_empty_string_receipt_id_harness_blocks(
        self, temp_dir: Path, configured_env: Path
    ) -> None:
        """validate_dispatch blocks workspace_write if receipt_id is empty."""
        with pytest.raises(ValueError, match="permission=workspace_write requires --receipt-id"):
            validate_dispatch(
                prompt="Update source code",
                role_name="verifier",
                permission_name="workspace_write",
                add_dirs=[str(temp_dir)],
                skip_permissions=True,
                receipt_id="",
            )

    def test_concurrent_receipt_consumption(self, control_plane: ControlPlane) -> None:
        """Two concurrent threads attempting to consume the same receipt: exactly one succeeds."""
        receipt = control_plane.issue_write_receipt(issuer="Brain", allowed_paths=["/race"])
        receipt_id = receipt["receipt_id"]

        successes: list[dict[str, Any]] = []
        failures: list[Exception] = []
        barrier = threading.Barrier(2)

        def worker_attempt():
            barrier.wait()
            try:
                res = control_plane.validate_write_receipt(receipt_id)
                successes.append(res)
            except Exception as e:
                failures.append(e)

        t1 = threading.Thread(target=worker_attempt)
        t2 = threading.Thread(target=worker_attempt)

        t1.start()
        t2.start()
        t1.join()
        t2.join()

        assert len(successes) == 1
        assert len(failures) == 1
        assert "already consumed" in str(failures[0])


# ─────────────────────────────────────────────────────────────────────────────
# Category 3: Input Validation (5+ tests)
# ─────────────────────────────────────────────────────────────────────────────

class TestCategory3InputValidation:
    def test_issue_with_empty_allowed_paths_raises_value_error(self, control_plane: ControlPlane) -> None:
        """Empty allowed_paths list is rejected."""
        with pytest.raises(ValueError, match="allowed_paths must not be empty"):
            control_plane.issue_write_receipt(issuer="Brain", allowed_paths=[])

    def test_issue_with_ttl_zero_raises_value_error(self, control_plane: ControlPlane) -> None:
        """ttl_seconds=0 is rejected."""
        with pytest.raises(ValueError, match="ttl_seconds must be between 1 and 3600"):
            control_plane.issue_write_receipt(issuer="Brain", allowed_paths=["/a"], ttl_seconds=0)

    def test_issue_with_ttl_negative_raises_value_error(self, control_plane: ControlPlane) -> None:
        """Negative ttl_seconds is rejected."""
        with pytest.raises(ValueError, match="ttl_seconds must be between 1 and 3600"):
            control_plane.issue_write_receipt(issuer="Brain", allowed_paths=["/a"], ttl_seconds=-10.0)

    def test_issue_with_ttl_exceeds_max_raises_value_error(self, control_plane: ControlPlane) -> None:
        """ttl_seconds > 3600 is rejected."""
        with pytest.raises(ValueError, match="ttl_seconds must be between 1 and 3600"):
            control_plane.issue_write_receipt(issuer="Brain", allowed_paths=["/a"], ttl_seconds=3601.0)

    def test_issue_with_ttl_upper_boundary_3600_ok(self, control_plane: ControlPlane) -> None:
        """ttl_seconds=3600 boundary is accepted."""
        receipt = control_plane.issue_write_receipt(issuer="Brain", allowed_paths=["/a"], ttl_seconds=3600.0)
        assert receipt["ttl_seconds"] == 3600.0

    def test_issue_with_ttl_lower_boundary_1_ok(self, control_plane: ControlPlane) -> None:
        """ttl_seconds=1 boundary is accepted."""
        receipt = control_plane.issue_write_receipt(issuer="Brain", allowed_paths=["/a"], ttl_seconds=1.0)
        assert receipt["ttl_seconds"] == 1.0


# ─────────────────────────────────────────────────────────────────────────────
# Category 4: Integration with Harness (5+ tests)
# ─────────────────────────────────────────────────────────────────────────────

class TestCategory4IntegrationWithHarness:
    def test_validate_dispatch_read_only_no_receipt_needed(
        self, temp_dir: Path, configured_env: Path
    ) -> None:
        """read_only permission profile does not require or process a receipt."""
        gate = validate_dispatch(
            prompt="Analyze codebase",
            role_name="collector",
            permission_name="read_only",
            add_dirs=[str(temp_dir)],
            skip_permissions=False,
            receipt_id=None,
        )
        assert gate["permission"] == "read_only"
        assert gate["mutation_allowed"] is False
        assert "receipt" not in gate

    def test_validate_dispatch_automation_read_no_receipt_needed(
        self, temp_dir: Path, configured_env: Path
    ) -> None:
        """automation_read permission profile does not require a receipt."""
        gate = validate_dispatch(
            prompt="Verify test assertions",
            role_name="verifier",
            permission_name="automation_read",
            add_dirs=[str(temp_dir)],
            skip_permissions=True,
            receipt_id=None,
        )
        assert gate["permission"] == "automation_read"
        assert gate["mutation_allowed"] is False
        assert "receipt" not in gate

    def test_validate_dispatch_workspace_write_with_valid_receipt(
        self, temp_dir: Path, control_plane: ControlPlane, configured_env: Path
    ) -> None:
        """workspace_write succeeds with a valid receipt and returns receipt metadata."""
        receipt = control_plane.issue_write_receipt(
            issuer="Brain-Opus",
            allowed_paths=[str(temp_dir / "*.py")],
            ttl_seconds=600.0,
        )

        gate = validate_dispatch(
            prompt="Implement bugfix",
            role_name="verifier",
            permission_name="workspace_write",
            add_dirs=[str(temp_dir)],
            skip_permissions=True,
            receipt_id=receipt["receipt_id"],
        )

        assert gate["permission"] == "workspace_write"
        assert gate["mutation_allowed"] is True
        assert "receipt" in gate
        assert gate["receipt"]["receipt_id"] == receipt["receipt_id"]
        assert gate["receipt"]["issuer"] == "Brain-Opus"
        assert gate["receipt"]["allowed_paths"] == [str(temp_dir / "*.py")]
        assert gate["receipt"]["valid"] is True

    def test_validate_dispatch_workspace_write_with_expired_receipt(
        self, temp_dir: Path, control_plane: ControlPlane, configured_env: Path
    ) -> None:
        """workspace_write fails when given an expired receipt."""
        receipt = control_plane.issue_write_receipt(
            issuer="Brain",
            allowed_paths=[str(temp_dir)],
            ttl_seconds=1.0,
        )
        time.sleep(1.05)

        with pytest.raises(ValueError, match="write receipt rejected: write receipt expired"):
            validate_dispatch(
                prompt="Implement feature",
                role_name="verifier",
                permission_name="workspace_write",
                add_dirs=[str(temp_dir)],
                skip_permissions=True,
                receipt_id=receipt["receipt_id"],
            )

    def test_validate_dispatch_workspace_write_with_consumed_receipt(
        self, temp_dir: Path, control_plane: ControlPlane, configured_env: Path
    ) -> None:
        """workspace_write fails when given an already consumed receipt."""
        receipt = control_plane.issue_write_receipt(
            issuer="Brain",
            allowed_paths=[str(temp_dir)],
            ttl_seconds=300.0,
        )
        # Pre-consume the receipt
        control_plane.validate_write_receipt(receipt["receipt_id"])

        with pytest.raises(ValueError, match="write receipt rejected: write receipt already consumed"):
            validate_dispatch(
                prompt="Implement feature",
                role_name="verifier",
                permission_name="workspace_write",
                add_dirs=[str(temp_dir)],
                skip_permissions=True,
                receipt_id=receipt["receipt_id"],
            )

    def test_harness_error_message_includes_actionable_guidance(
        self, temp_dir: Path, configured_env: Path
    ) -> None:
        """Harness error message guides the caller on how to obtain a receipt."""
        with pytest.raises(ValueError) as exc_info:
            validate_dispatch(
                prompt="Modify files",
                role_name="verifier",
                permission_name="workspace_write",
                add_dirs=[str(temp_dir)],
                skip_permissions=True,
                receipt_id=None,
            )
        error_msg = str(exc_info.value)
        assert "requires --receipt-id" in error_msg
        assert "ControlPlane.issue_write_receipt()" in error_msg


# ─────────────────────────────────────────────────────────────────────────────
# Category 5: Worst Case / Edge Cases (5+ tests)
# ─────────────────────────────────────────────────────────────────────────────

class TestCategory5WorstCaseEdgeCases:
    def test_db_file_deleted_mid_session(self, control_plane: ControlPlane, db_path: Path) -> None:
        """ControlPlane handles mid-session DB deletion gracefully."""
        receipt = control_plane.issue_write_receipt(issuer="Brain", allowed_paths=["/a"])

        # Delete database file while control_plane instance is alive
        db_path.unlink()

        # Re-connecting on validate should either recreate tables or fail cleanly
        try:
            with pytest.raises(ValueError, match="write receipt not found"):
                control_plane.validate_write_receipt(receipt["receipt_id"])
        except sqlite3.OperationalError:
            pass  # Acceptable SQLite error when WAL/DB is unlinked

    def test_issue_1000_receipts_performance(self, control_plane: ControlPlane, db_path: Path) -> None:
        """Issuing 1,000 receipts completes in under 1.0s without performance degradation."""
        start = time.perf_counter()
        for i in range(1000):
            control_plane.issue_write_receipt(
                issuer=f"Brain-Worker-{i}",
                allowed_paths=[f"/workspace/shard_{i}/*"],
                ttl_seconds=600.0,
            )
        duration = time.perf_counter() - start

        assert duration < 1.0, f"Issuing 1000 receipts took too long: {duration:.3f}s"

        conn = sqlite3.connect(db_path)
        count = conn.execute("SELECT COUNT(*) FROM write_receipts").fetchone()[0]
        conn.close()
        assert count == 1000

    def test_receipt_with_unicode_characters_in_issuer(self, control_plane: ControlPlane) -> None:
        """Receipt correctly handles multi-byte Unicode strings and emojis."""
        unicode_issuer = "🧠 Brain (オーパス) — 測試 / 测试 / 🚀"
        receipt = control_plane.issue_write_receipt(
            issuer=unicode_issuer,
            allowed_paths=["/workspace/utf8.txt"],
        )
        validated = control_plane.validate_write_receipt(receipt["receipt_id"])
        assert validated["issuer"] == unicode_issuer

    def test_receipt_with_very_long_allowed_paths_list(self, control_plane: ControlPlane) -> None:
        """Receipt supports large arrays of allowed paths (100 entries)."""
        long_paths = [f"/workspace/deep/nested/directory_{i}/target_file_{i}.ext" for i in range(100)]
        receipt = control_plane.issue_write_receipt(
            issuer="Brain",
            allowed_paths=long_paths,
        )
        validated = control_plane.validate_write_receipt(receipt["receipt_id"])
        assert len(validated["allowed_paths"]) == 100
        assert validated["allowed_paths"] == long_paths

    def test_receipt_just_expired_subsecond(self, db_path: Path) -> None:
        """Receipt expired by only 1 millisecond is rejected."""
        sim_time = 2_000_000_000.0

        def clock_fn():
            return sim_time

        cp = ControlPlane(db_path=db_path, clock=clock_fn)
        receipt = cp.issue_write_receipt(issuer="Brain", allowed_paths=["/a"], ttl_seconds=10.0)

        # Advance time to 0.001s past expiration
        sim_time += 10.001

        with pytest.raises(ValueError, match="write receipt expired"):
            cp.validate_write_receipt(receipt["receipt_id"])

    def test_two_control_plane_instances_pointing_to_same_db(self, db_path: Path) -> None:
        """Multiple ControlPlane instances safely coordinate over SQLite WAL locking."""
        cp1 = ControlPlane(db_path=db_path)
        cp2 = ControlPlane(db_path=db_path)

        receipt = cp1.issue_write_receipt(issuer="Brain-Instance-1", allowed_paths=["/shared"])
        validated = cp2.validate_write_receipt(receipt["receipt_id"])

        assert validated["valid"] is True
        assert validated["issuer"] == "Brain-Instance-1"

        # cp1 now sees it as consumed
        with pytest.raises(ValueError, match="already consumed"):
            cp1.validate_write_receipt(receipt["receipt_id"])

    def test_schema_migration_fresh_db_creates_write_receipts_table(self, temp_dir: Path) -> None:
        """Fresh ControlPlane initialization establishes the write_receipts schema."""
        fresh_db = temp_dir / "fresh.sqlite3"
        cp = ControlPlane(db_path=fresh_db)

        conn = sqlite3.connect(fresh_db)
        columns = {row[1]: row[2] for row in conn.execute("PRAGMA table_info(write_receipts)").fetchall()}
        conn.close()

        expected_columns = {
            "receipt_id": "TEXT",
            "issuer": "TEXT",
            "allowed_paths_json": "TEXT",
            "expires_at": "REAL",
            "consumed": "INTEGER",
            "consumer_task_id": "TEXT",
            "created_at": "REAL",
        }
        for col_name, col_type in expected_columns.items():
            assert col_name in columns
            assert columns[col_name] == col_type


# ─────────────────────────────────────────────────────────────────────────────
# Category 6: Out of Scope / Abuse (3+ tests)
# ─────────────────────────────────────────────────────────────────────────────

class TestCategory6OutOfScopeAndAbuse:
    def test_worker_can_call_issue_write_receipt_directly(self, control_plane: ControlPlane) -> None:
        """ControlPlane does not enforce auth tokens internally (enforced at orchestrator tier)."""
        # Documenting architectural boundary: any caller with DB access can call issue_write_receipt.
        worker_receipt = control_plane.issue_write_receipt(
            issuer="untrusted-worker",
            allowed_paths=["/etc/passwd"],
        )
        assert worker_receipt["issuer"] == "untrusted-worker"
        # The receipt exists in SQLite, but the harness path filters/orchestrator restrict this.

    def test_passing_receipt_id_to_read_only_permission_is_ignored(
        self, temp_dir: Path, control_plane: ControlPlane, configured_env: Path
    ) -> None:
        """Passing receipt_id to read_only profile is ignored and does not consume the receipt."""
        receipt = control_plane.issue_write_receipt(issuer="Brain", allowed_paths=["/a"])
        gate = validate_dispatch(
            prompt="Read-only analysis",
            role_name="collector",
            permission_name="read_only",
            add_dirs=[str(temp_dir)],
            skip_permissions=False,
            receipt_id=receipt["receipt_id"],
        )

        assert gate["permission"] == "read_only"
        assert "receipt" not in gate

        # Receipt must still be unconsumed
        validated = control_plane.validate_write_receipt(receipt["receipt_id"])
        assert validated["valid"] is True

    def test_manipulating_db_directly_to_reset_consumed_known_limitation(
        self, control_plane: ControlPlane, db_path: Path
    ) -> None:
        """Documented limitation: direct SQLite write access allows resetting consumed flag."""
        receipt = control_plane.issue_write_receipt(issuer="Brain", allowed_paths=["/a"])
        receipt_id = receipt["receipt_id"]

        # Consume receipt legitimately
        control_plane.validate_write_receipt(receipt_id)

        with pytest.raises(ValueError, match="already consumed"):
            control_plane.validate_write_receipt(receipt_id)

        # Bypass via direct SQLite write (trust boundary is SQLite file permissions 0600)
        conn = sqlite3.connect(db_path)
        conn.execute("UPDATE write_receipts SET consumed = 0 WHERE receipt_id = ?", (receipt_id,))
        conn.commit()
        conn.close()

        # Re-consuming now succeeds due to raw DB tampering
        revalidated = control_plane.validate_write_receipt(receipt_id)
        assert revalidated["valid"] is True
