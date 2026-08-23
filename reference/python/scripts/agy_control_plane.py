#!/usr/bin/env python3
"""Durable local task registry for application-only AGY dispatch."""

from __future__ import annotations

import hashlib
import json
import os
import sqlite3
import time
import uuid
from pathlib import Path
from typing import Any, Callable

from agy_harness import validate_dispatch


SCHEMA_VERSION = 3
TASK_SCHEMA_VERSION = "agy.task.v1"
RECEIPT_SCHEMA_VERSION = "agy.receipt.v1"
TASK_STATES = {
    "QUEUED",
    "LEASED",
    "RUNNING",
    "NEEDS_INFO",
    "BLOCKED",
    "SUCCEEDED",
    "FAILED",
    "CANCELLED",
}
FINAL_STATES = {"SUCCEEDED", "FAILED", "CANCELLED"}
DEFAULT_STATE_ROOT = Path(
    os.environ.get(
        "AGY_DISPATCH_STATE_ROOT",
        Path.home() / ".local" / "state" / "agy-dispatch",
    )
).expanduser()


def default_db_path() -> Path:
    configured = os.environ.get("AGY_DISPATCH_STATE_DB")
    return Path(configured).expanduser() if configured else DEFAULT_STATE_ROOT / "control-plane.sqlite3"


def canonical_json(value: Any) -> str:
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False)


def content_hash(value: Any) -> str:
    return hashlib.sha256(canonical_json(value).encode("utf-8")).hexdigest()


def redact_request_json(request_json: str) -> str:
    request = json.loads(request_json)
    prompt = request.pop("prompt", None)
    if prompt is not None:
        request["prompt_hash"] = content_hash(prompt)
        request["prompt_redacted"] = True
    return canonical_json(request)


def _atomic_json_write(path: Path, payload: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temp = path.with_suffix(path.suffix + ".tmp")
    temp.write_text(json.dumps(payload, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    temp.chmod(0o600)
    temp.replace(path)


class ControlPlane:
    """SQLite-backed queue with CAS leases and append-only task events."""

    def __init__(self, db_path: str | Path | None = None, *, clock: Callable[[], float] = time.time):
        self.db_path = Path(db_path or default_db_path()).expanduser()
        self.clock = clock
        parent_existed = self.db_path.parent.exists()
        self.db_path.parent.mkdir(parents=True, exist_ok=True)
        if not parent_existed:
            self.db_path.parent.chmod(0o700)
        self._initialize()
        self.db_path.chmod(0o600)

    def _connect(self) -> sqlite3.Connection:
        connection = sqlite3.connect(self.db_path, timeout=30.0, isolation_level=None)
        connection.row_factory = sqlite3.Row
        connection.execute("PRAGMA busy_timeout = 30000")
        connection.execute("PRAGMA foreign_keys = ON")
        connection.execute("PRAGMA journal_mode = WAL")
        connection.execute("PRAGMA synchronous = FULL")
        return connection

    def _initialize(self) -> None:
        connection = self._connect()
        try:
            connection.execute("BEGIN EXCLUSIVE")
            version = int(connection.execute("PRAGMA user_version").fetchone()[0])
            if version not in (0, 1, 2, SCHEMA_VERSION):
                raise RuntimeError(
                    f"unsupported control-plane schema version {version}; expected {SCHEMA_VERSION}"
                )
            connection.execute(
                """
                CREATE TABLE IF NOT EXISTS tasks (
                    task_id TEXT PRIMARY KEY,
                    parent_task_id TEXT REFERENCES tasks(task_id),
                    idempotency_key TEXT NOT NULL UNIQUE,
                    schema_version TEXT NOT NULL,
                    state TEXT NOT NULL,
                    priority INTEGER NOT NULL,
                    request_json TEXT NOT NULL,
                    request_hash TEXT NOT NULL,
                    result_json TEXT,
                    result_hash TEXT,
                    receipt_hash TEXT,
                    attempts INTEGER NOT NULL DEFAULT 0,
                    max_attempts INTEGER NOT NULL,
                    lease_owner TEXT,
                    lease_token TEXT,
                    lease_expires_at REAL,
                    cancel_requested INTEGER NOT NULL DEFAULT 0,
                    created_at REAL NOT NULL,
                    updated_at REAL NOT NULL,
                    completed_at REAL,
                    last_error TEXT
                )
                """
            )
            connection.execute(
                """
                CREATE INDEX IF NOT EXISTS idx_tasks_claim
                    ON tasks(state, priority DESC, created_at ASC)
                """
            )
            connection.execute(
                """
                CREATE TABLE IF NOT EXISTS task_events (
                    event_id INTEGER PRIMARY KEY AUTOINCREMENT,
                    task_id TEXT NOT NULL REFERENCES tasks(task_id) ON DELETE CASCADE,
                    timestamp REAL NOT NULL,
                    event_type TEXT NOT NULL,
                    actor TEXT NOT NULL,
                    details_json TEXT NOT NULL
                )
                """
            )
            connection.execute(
                """
                CREATE INDEX IF NOT EXISTS idx_task_events_task
                    ON task_events(task_id, event_id)
                """
            )
            connection.execute(
                """
                CREATE TABLE IF NOT EXISTS control_plane_maintenance (
                    singleton INTEGER PRIMARY KEY CHECK(singleton = 1),
                    owner TEXT NOT NULL,
                    expires_at REAL NOT NULL,
                    updated_at REAL NOT NULL
                )
                """
            )
            connection.execute(
                """
                CREATE TABLE IF NOT EXISTS write_receipts (
                    receipt_id TEXT PRIMARY KEY,
                    issuer TEXT NOT NULL,
                    allowed_paths_json TEXT NOT NULL,
                    expires_at REAL NOT NULL,
                    consumed INTEGER NOT NULL DEFAULT 0,
                    consumer_task_id TEXT,
                    created_at REAL NOT NULL
                )
                """
            )
            columns = {
                row[1] for row in connection.execute("PRAGMA table_info(tasks)").fetchall()
            }
            if "parent_task_id" not in columns:
                connection.execute(
                    "ALTER TABLE tasks ADD COLUMN parent_task_id TEXT REFERENCES tasks(task_id)"
                )
            connection.execute(f"PRAGMA user_version = {SCHEMA_VERSION}")
            connection.commit()
        except Exception:
            connection.rollback()
            raise
        finally:
            connection.close()

    @staticmethod
    def _begin(connection: sqlite3.Connection) -> None:
        connection.execute("BEGIN IMMEDIATE")

    @staticmethod
    def _event(
        connection: sqlite3.Connection,
        task_id: str,
        event_type: str,
        actor: str,
        details: dict[str, Any] | None,
        timestamp: float,
    ) -> None:
        connection.execute(
            """
            INSERT INTO task_events(task_id, timestamp, event_type, actor, details_json)
            VALUES (?, ?, ?, ?, ?)
            """,
            (task_id, timestamp, event_type, actor, canonical_json(details or {})),
        )

    @staticmethod
    def _decode_task(row: sqlite3.Row | None) -> dict[str, Any] | None:
        if row is None:
            return None
        task = dict(row)
        task["request"] = json.loads(task.pop("request_json"))
        result_json = task.pop("result_json")
        task["result"] = json.loads(result_json) if result_json else None
        task["cancel_requested"] = bool(task["cancel_requested"])
        return task

    @staticmethod
    def validate_request(request: dict[str, Any]) -> dict[str, Any]:
        if not isinstance(request, dict):
            raise ValueError("request must be an object")
        prompt = request.get("prompt")
        if not isinstance(prompt, str) or not prompt.strip():
            raise ValueError("request.prompt is required")
        role = request.get("role", "collector")
        permission = request.get("permission", "read_only")
        model = request.get("model")
        timeout = request.get("timeout", "5m0s")
        add_dirs = request.get("add_dirs", [])
        skip_permissions = request.get("skip_permissions", False)
        no_sandbox = request.get("no_sandbox", False)
        agy_bin = request.get("agy_bin")
        if not isinstance(role, str) or not isinstance(permission, str):
            raise ValueError("request.role and request.permission must be strings")
        if permission == "workspace_write" and os.environ.get("AGY_MCP_ALLOW_WORKSPACE_WRITE") != "1":
            raise ValueError("workspace_write is disabled in control-plane v0.1")
        if not isinstance(model, str) or not model.strip():
            raise ValueError("request.model is required")
        if not isinstance(timeout, str) or not timeout.strip():
            raise ValueError("request.timeout must be a non-empty duration string")
        if not isinstance(add_dirs, list) or any(not isinstance(item, str) for item in add_dirs):
            raise ValueError("request.add_dirs must be a list of strings")
        if not add_dirs:
            raise ValueError("request.add_dirs requires at least one explicit scope root")
        if not isinstance(skip_permissions, bool) or not isinstance(no_sandbox, bool):
            raise ValueError("request skip_permissions and no_sandbox must be booleans")
        if no_sandbox:
            raise ValueError("no_sandbox is disabled in control-plane v0.1")
        if agy_bin is not None:
            raise ValueError("custom agy_bin is disabled in control-plane v0.1")
        grant = validate_dispatch(
            prompt=prompt,
            role_name=role,
            permission_name=permission,
            add_dirs=add_dirs,
            skip_permissions=skip_permissions,
        )
        return {
            "prompt": prompt,
            "role": role,
            "permission": permission,
            "model": model,
            "timeout": timeout,
            "add_dirs": grant["allowed_roots"],
            "skip_permissions": skip_permissions,
            "no_sandbox": False,
            "capability_grant": grant,
        }

    def submit_task(
        self,
        *,
        request: dict[str, Any],
        idempotency_key: str,
        parent_task_id: str | None = None,
        priority: int = 0,
        max_attempts: int = 1,
        actor: str = "orchestrator",
    ) -> dict[str, Any]:
        if not isinstance(idempotency_key, str) or not idempotency_key.strip():
            raise ValueError("idempotency_key is required")
        if len(idempotency_key) > 200:
            raise ValueError("idempotency_key must be at most 200 characters")
        if parent_task_id is not None and (
            not isinstance(parent_task_id, str) or not parent_task_id.strip()
        ):
            raise ValueError("parent_task_id must be a non-empty string when provided")
        if not isinstance(priority, int) or not -100 <= priority <= 100:
            raise ValueError("priority must be an integer between -100 and 100")
        if not isinstance(max_attempts, int) or not 1 <= max_attempts <= 10:
            raise ValueError("max_attempts must be an integer between 1 and 10")
        normalized = self.validate_request(request)
        request_hash = content_hash(normalized)
        now = self.clock()
        connection = self._connect()
        try:
            self._begin(connection)
            if parent_task_id is not None:
                parent = connection.execute(
                    "SELECT 1 FROM tasks WHERE task_id = ?", (parent_task_id,)
                ).fetchone()
                if parent is None:
                    raise ValueError(f"unknown parent task: {parent_task_id}")
            existing = connection.execute(
                "SELECT * FROM tasks WHERE idempotency_key = ?", (idempotency_key,)
            ).fetchone()
            if existing is not None:
                if (
                    existing["request_hash"] != request_hash
                    or existing["parent_task_id"] != parent_task_id
                ):
                    raise ValueError("idempotency_key already exists with a different request")
                connection.commit()
                task = self._decode_task(existing)
                assert task is not None
                task["deduplicated"] = True
                return task
            task_id = str(uuid.uuid4())
            connection.execute(
                """
                INSERT INTO tasks(
                    task_id, parent_task_id, idempotency_key, schema_version, state, priority,
                    request_json, request_hash, max_attempts, created_at, updated_at
                ) VALUES (?, ?, ?, ?, 'QUEUED', ?, ?, ?, ?, ?, ?)
                """,
                (
                    task_id,
                    parent_task_id,
                    idempotency_key,
                    TASK_SCHEMA_VERSION,
                    priority,
                    canonical_json(normalized),
                    request_hash,
                    max_attempts,
                    now,
                    now,
                ),
            )
            self._event(
                connection,
                task_id,
                "task_submitted",
                actor,
                {
                    "request_hash": request_hash,
                    "priority": priority,
                    "parent_task_id": parent_task_id,
                },
                now,
            )
            connection.commit()
            task = self.get_task(task_id)
            assert task is not None
            task["deduplicated"] = False
            return task
        except Exception:
            connection.rollback()
            raise
        finally:
            connection.close()

    def get_task(self, task_id: str) -> dict[str, Any] | None:
        connection = self._connect()
        try:
            return self._decode_task(
                connection.execute("SELECT * FROM tasks WHERE task_id = ?", (task_id,)).fetchone()
            )
        finally:
            connection.close()

    def list_tasks(self, *, state: str | None = None, limit: int = 50) -> list[dict[str, Any]]:
        if state is not None and state not in TASK_STATES:
            raise ValueError(f"unknown task state: {state}")
        if not isinstance(limit, int) or not 1 <= limit <= 200:
            raise ValueError("limit must be between 1 and 200")
        connection = self._connect()
        try:
            if state is None:
                rows = connection.execute(
                    "SELECT * FROM tasks ORDER BY created_at DESC LIMIT ?", (limit,)
                ).fetchall()
            else:
                rows = connection.execute(
                    "SELECT * FROM tasks WHERE state = ? ORDER BY created_at DESC LIMIT ?",
                    (state, limit),
                ).fetchall()
            return [self._decode_task(row) for row in rows if row is not None]
        finally:
            connection.close()

    def active_task_count(self) -> int:
        connection = self._connect()
        try:
            return int(
                connection.execute(
                    "SELECT COUNT(*) FROM tasks WHERE state IN ('LEASED', 'RUNNING')"
                ).fetchone()[0]
            )
        finally:
            connection.close()

    def _reconcile_expired(self, connection: sqlite3.Connection, now: float) -> int:
        rows = connection.execute(
            """
            SELECT * FROM tasks
            WHERE state IN ('LEASED', 'RUNNING')
              AND lease_expires_at IS NOT NULL
              AND lease_expires_at <= ?
            """,
            (now,),
        ).fetchall()
        reconciled = 0
        for row in rows:
            if row["cancel_requested"]:
                next_state = "CANCELLED"
                error = "cancelled after lease expiry"
            elif row["attempts"] >= row["max_attempts"]:
                next_state = "FAILED"
                error = "lease expired and retry budget exhausted"
            else:
                next_state = "QUEUED"
                error = "lease expired; task requeued"
            connection.execute(
                """
                UPDATE tasks
                SET state = ?, lease_owner = NULL, lease_token = NULL,
                    lease_expires_at = NULL, updated_at = ?,
                    completed_at = CASE WHEN ? IN ('FAILED', 'CANCELLED') THEN ? ELSE NULL END,
                    last_error = ?
                WHERE task_id = ? AND lease_token = ?
                """,
                (
                    next_state,
                    now,
                    next_state,
                    now,
                    error,
                    row["task_id"],
                    row["lease_token"],
                ),
            )
            if next_state in FINAL_STATES:
                connection.execute(
                    "UPDATE tasks SET request_json = ? WHERE task_id = ?",
                    (redact_request_json(row["request_json"]), row["task_id"]),
                )
            self._event(
                connection,
                row["task_id"],
                "lease_expired",
                "reconciler",
                {"next_state": next_state, "previous_owner": row["lease_owner"]},
                now,
            )
            if next_state in FINAL_STATES:
                receipt = self._build_receipt(connection, row["task_id"])
                connection.execute(
                    "UPDATE tasks SET receipt_hash = ? WHERE task_id = ?",
                    (receipt["receipt_hash"], row["task_id"]),
                )
            reconciled += 1
        return reconciled

    def reconcile_expired(self) -> int:
        now = self.clock()
        connection = self._connect()
        try:
            self._begin(connection)
            count = self._reconcile_expired(connection, now)
            connection.commit()
            return count
        except Exception:
            connection.rollback()
            raise
        finally:
            connection.close()

    def begin_maintenance(self, owner: str, *, ttl_seconds: float = 300.0) -> int:
        """Pause new claims and atomically report currently active tasks."""
        if not owner.strip():
            raise ValueError("maintenance owner is required")
        if ttl_seconds <= 0:
            raise ValueError("maintenance ttl_seconds must be positive")
        now = self.clock()
        connection = self._connect()
        try:
            self._begin(connection)
            current = connection.execute(
                "SELECT owner, expires_at FROM control_plane_maintenance WHERE singleton = 1"
            ).fetchone()
            if current is not None and current["expires_at"] > now:
                raise RuntimeError(f"control-plane maintenance is already held by {current['owner']}")
            connection.execute("DELETE FROM control_plane_maintenance WHERE singleton = 1")
            connection.execute(
                """
                INSERT INTO control_plane_maintenance(singleton, owner, expires_at, updated_at)
                VALUES (1, ?, ?, ?)
                """,
                (owner, now + ttl_seconds, now),
            )
            active = int(
                connection.execute(
                    "SELECT COUNT(*) FROM tasks WHERE state IN ('LEASED', 'RUNNING')"
                ).fetchone()[0]
            )
            connection.commit()
            return active
        except Exception:
            connection.rollback()
            raise
        finally:
            connection.close()

    def end_maintenance(self, owner: str) -> bool:
        """Release maintenance only when the caller still owns it."""
        connection = self._connect()
        try:
            self._begin(connection)
            cursor = connection.execute(
                "DELETE FROM control_plane_maintenance WHERE singleton = 1 AND owner = ?",
                (owner,),
            )
            connection.commit()
            return cursor.rowcount == 1
        except Exception:
            connection.rollback()
            raise
        finally:
            connection.close()

    def claim_next(self, worker_id: str, *, lease_seconds: float = 30.0) -> dict[str, Any] | None:
        if not worker_id.strip():
            raise ValueError("worker_id is required")
        if lease_seconds <= 0:
            raise ValueError("lease_seconds must be positive")
        now = self.clock()
        connection = self._connect()
        try:
            self._begin(connection)
            self._reconcile_expired(connection, now)
            maintenance = connection.execute(
                "SELECT expires_at FROM control_plane_maintenance WHERE singleton = 1"
            ).fetchone()
            if maintenance is not None and maintenance["expires_at"] > now:
                connection.commit()
                return None
            if maintenance is not None:
                connection.execute("DELETE FROM control_plane_maintenance WHERE singleton = 1")
            row = connection.execute(
                """
                SELECT * FROM tasks
                WHERE state = 'QUEUED' AND cancel_requested = 0 AND attempts < max_attempts
                ORDER BY priority DESC, created_at ASC
                LIMIT 1
                """
            ).fetchone()
            if row is None:
                connection.commit()
                return None
            lease_token = str(uuid.uuid4())
            cursor = connection.execute(
                """
                UPDATE tasks
                SET state = 'LEASED', lease_owner = ?, lease_token = ?,
                    lease_expires_at = ?, attempts = attempts + 1, updated_at = ?
                WHERE task_id = ? AND state = 'QUEUED' AND cancel_requested = 0
                """,
                (
                    worker_id,
                    lease_token,
                    now + lease_seconds,
                    now,
                    row["task_id"],
                ),
            )
            if cursor.rowcount != 1:
                connection.rollback()
                return None
            self._event(
                connection,
                row["task_id"],
                "task_claimed",
                worker_id,
                {"lease_token": lease_token, "lease_seconds": lease_seconds},
                now,
            )
            connection.commit()
            return self.get_task(row["task_id"])
        except Exception:
            connection.rollback()
            raise
        finally:
            connection.close()

    def start_task(self, task_id: str, worker_id: str, lease_token: str) -> bool:
        now = self.clock()
        connection = self._connect()
        try:
            self._begin(connection)
            cursor = connection.execute(
                """
                UPDATE tasks SET state = 'RUNNING', updated_at = ?
                WHERE task_id = ? AND state = 'LEASED'
                  AND lease_owner = ? AND lease_token = ? AND cancel_requested = 0
                """,
                (now, task_id, worker_id, lease_token),
            )
            if cursor.rowcount == 1:
                self._event(connection, task_id, "task_started", worker_id, {}, now)
            connection.commit()
            return cursor.rowcount == 1
        except Exception:
            connection.rollback()
            raise
        finally:
            connection.close()

    def heartbeat(
        self,
        task_id: str,
        worker_id: str,
        lease_token: str,
        *,
        lease_seconds: float,
    ) -> bool:
        now = self.clock()
        connection = self._connect()
        try:
            self._begin(connection)
            cursor = connection.execute(
                """
                UPDATE tasks SET lease_expires_at = ?, updated_at = ?
                WHERE task_id = ? AND state IN ('LEASED', 'RUNNING')
                  AND lease_owner = ? AND lease_token = ? AND cancel_requested = 0
                """,
                (now + lease_seconds, now, task_id, worker_id, lease_token),
            )
            connection.commit()
            return cursor.rowcount == 1
        except Exception:
            connection.rollback()
            raise
        finally:
            connection.close()

    def cancel_task(self, task_id: str, *, actor: str, reason: str) -> dict[str, Any] | None:
        now = self.clock()
        connection = self._connect()
        try:
            self._begin(connection)
            row = connection.execute("SELECT * FROM tasks WHERE task_id = ?", (task_id,)).fetchone()
            if row is None:
                connection.commit()
                return None
            if row["state"] in FINAL_STATES:
                connection.commit()
                return self._decode_task(row)
            next_state = "RUNNING" if row["state"] == "RUNNING" else "CANCELLED"
            connection.execute(
                """
                UPDATE tasks SET state = ?, cancel_requested = 1, updated_at = ?,
                    completed_at = CASE WHEN ? = 'CANCELLED' THEN ? ELSE completed_at END,
                    lease_owner = CASE WHEN ? = 'CANCELLED' THEN NULL ELSE lease_owner END,
                    lease_token = CASE WHEN ? = 'CANCELLED' THEN NULL ELSE lease_token END,
                    lease_expires_at = CASE WHEN ? = 'CANCELLED' THEN NULL ELSE lease_expires_at END,
                    last_error = ?
                WHERE task_id = ?
                """,
                (
                    next_state,
                    now,
                    next_state,
                    now,
                    next_state,
                    next_state,
                    next_state,
                    reason,
                    task_id,
                ),
            )
            if next_state in FINAL_STATES:
                connection.execute(
                    "UPDATE tasks SET request_json = ? WHERE task_id = ?",
                    (redact_request_json(row["request_json"]), task_id),
                )
            self._event(
                connection,
                task_id,
                "cancel_requested",
                actor,
                {"reason": reason, "next_state": next_state},
                now,
            )
            if next_state == "CANCELLED":
                receipt = self._build_receipt(connection, task_id)
                connection.execute(
                    "UPDATE tasks SET receipt_hash = ? WHERE task_id = ?",
                    (receipt["receipt_hash"], task_id),
                )
            connection.commit()
            return self.get_task(task_id)
        except Exception:
            connection.rollback()
            raise
        finally:
            connection.close()

    def execution_signal(self, task_id: str, worker_id: str, lease_token: str) -> str:
        """Return active, cancel_requested, or lease_lost for a running worker."""
        connection = self._connect()
        try:
            row = connection.execute(
                """
                SELECT state, cancel_requested, lease_owner, lease_token
                FROM tasks WHERE task_id = ?
                """,
                (task_id,),
            ).fetchone()
            if (
                row is None
                or row["state"] != "RUNNING"
                or row["lease_owner"] != worker_id
                or row["lease_token"] != lease_token
            ):
                return "lease_lost"
            return "cancel_requested" if row["cancel_requested"] else "active"
        finally:
            connection.close()

    def finish_attempt(
        self,
        task_id: str,
        worker_id: str,
        lease_token: str,
        *,
        result: dict[str, Any],
        success: bool,
        retryable: bool,
        error: str | None = None,
    ) -> dict[str, Any]:
        now = self.clock()
        connection = self._connect()
        try:
            self._begin(connection)
            row = connection.execute("SELECT * FROM tasks WHERE task_id = ?", (task_id,)).fetchone()
            if row is None:
                raise ValueError(f"unknown task: {task_id}")
            if (
                row["state"] != "RUNNING"
                or row["lease_owner"] != worker_id
                or row["lease_token"] != lease_token
            ):
                raise ValueError("lease ownership lost; refusing stale completion")
            if row["cancel_requested"]:
                next_state = "CANCELLED"
            elif success:
                next_state = "SUCCEEDED"
            elif retryable and row["attempts"] < row["max_attempts"]:
                next_state = "QUEUED"
            else:
                next_state = "FAILED"
            result_json = canonical_json(result)
            result_hash = content_hash(result)
            completed_at = now if next_state in FINAL_STATES else None
            connection.execute(
                """
                UPDATE tasks
                SET state = ?, result_json = ?, result_hash = ?, updated_at = ?,
                    completed_at = ?, last_error = ?, lease_owner = NULL,
                    lease_token = NULL, lease_expires_at = NULL, receipt_hash = NULL
                WHERE task_id = ? AND lease_owner = ? AND lease_token = ?
                """,
                (
                    next_state,
                    result_json,
                    result_hash,
                    now,
                    completed_at,
                    error,
                    task_id,
                    worker_id,
                    lease_token,
                ),
            )
            if next_state in FINAL_STATES:
                connection.execute(
                    "UPDATE tasks SET request_json = ? WHERE task_id = ?",
                    (redact_request_json(row["request_json"]), task_id),
                )
            self._event(
                connection,
                task_id,
                "attempt_finished",
                worker_id,
                {
                    "next_state": next_state,
                    "success": success,
                    "retryable": retryable,
                    "result_hash": result_hash,
                },
                now,
            )
            if next_state in FINAL_STATES:
                receipt = self._build_receipt(connection, task_id)
                connection.execute(
                    "UPDATE tasks SET receipt_hash = ? WHERE task_id = ?",
                    (receipt["receipt_hash"], task_id),
                )
            connection.commit()
            task = self.get_task(task_id)
            assert task is not None
            return task
        except Exception:
            connection.rollback()
            raise
        finally:
            connection.close()

    def pause_task(
        self,
        task_id: str,
        worker_id: str,
        lease_token: str,
        *,
        state: str,
        result: dict[str, Any],
        reason: str,
    ) -> dict[str, Any]:
        if state not in {"NEEDS_INFO", "BLOCKED"}:
            raise ValueError("pause state must be NEEDS_INFO or BLOCKED")
        now = self.clock()
        connection = self._connect()
        try:
            self._begin(connection)
            result_json = canonical_json(result)
            result_hash = content_hash(result)
            cursor = connection.execute(
                """
                UPDATE tasks
                SET state = ?, result_json = ?, result_hash = ?, updated_at = ?,
                    last_error = ?, lease_owner = NULL, lease_token = NULL,
                    lease_expires_at = NULL
                WHERE task_id = ? AND state = 'RUNNING'
                  AND lease_owner = ? AND lease_token = ?
                """,
                (
                    state,
                    result_json,
                    result_hash,
                    now,
                    reason,
                    task_id,
                    worker_id,
                    lease_token,
                ),
            )
            if cursor.rowcount != 1:
                raise ValueError("lease ownership lost; refusing stale pause")
            row = connection.execute(
                "SELECT request_json FROM tasks WHERE task_id = ?", (task_id,)
            ).fetchone()
            connection.execute(
                "UPDATE tasks SET request_json = ? WHERE task_id = ?",
                (redact_request_json(row["request_json"]), task_id),
            )
            self._event(
                connection,
                task_id,
                "task_paused",
                worker_id,
                {"state": state, "reason": reason, "result_hash": result_hash},
                now,
            )
            receipt = self._build_receipt(connection, task_id)
            connection.execute(
                "UPDATE tasks SET receipt_hash = ? WHERE task_id = ?",
                (receipt["receipt_hash"], task_id),
            )
            connection.commit()
            task = self.get_task(task_id)
            assert task is not None
            return task
        except Exception:
            connection.rollback()
            raise
        finally:
            connection.close()

    def events(self, task_id: str) -> list[dict[str, Any]]:
        connection = self._connect()
        try:
            return self._events(connection, task_id)
        finally:
            connection.close()

    @staticmethod
    def _events(connection: sqlite3.Connection, task_id: str) -> list[dict[str, Any]]:
        rows = connection.execute(
            "SELECT * FROM task_events WHERE task_id = ? ORDER BY event_id", (task_id,)
        ).fetchall()
        return [
            {
                "event_id": row["event_id"],
                "task_id": row["task_id"],
                "timestamp": row["timestamp"],
                "event_type": row["event_type"],
                "actor": row["actor"],
                "details": json.loads(row["details_json"]),
            }
            for row in rows
        ]

    def _build_receipt(self, connection: sqlite3.Connection, task_id: str) -> dict[str, Any]:
        task = connection.execute(
            "SELECT * FROM tasks WHERE task_id = ?", (task_id,)
        ).fetchone()
        if task is None:
            raise ValueError(f"unknown task: {task_id}")
        events = self._events(connection, task_id)
        payload = {
            "schema_version": RECEIPT_SCHEMA_VERSION,
            "task_id": task_id,
            "parent_task_id": task["parent_task_id"],
            "state": task["state"],
            "request_hash": task["request_hash"],
            "result_hash": task["result_hash"],
            "events_hash": content_hash(events),
            "attempts": task["attempts"],
            "created_at": task["created_at"],
            "completed_at": task["completed_at"],
            "signed": False,
        }
        payload["receipt_hash"] = content_hash(payload)
        return payload

    def build_receipt(self, task_id: str) -> dict[str, Any]:
        connection = self._connect()
        try:
            return self._build_receipt(connection, task_id)
        finally:
            connection.close()

    def export_task(self, task_id: str, run_dir: str | Path) -> dict[str, str]:
        task = self.get_task(task_id)
        if task is None:
            raise ValueError(f"unknown task: {task_id}")
        root = Path(run_dir)
        task_path = root / "task.json"
        events_path = root / "events.jsonl"
        receipt_path = root / "receipt.json"
        exported_task = dict(task)
        exported_request = dict(exported_task["request"])
        prompt = exported_request.pop("prompt", None)
        if prompt is not None:
            exported_request["prompt_hash"] = content_hash(prompt)
            exported_request["prompt_redacted"] = True
        exported_task["request"] = exported_request
        _atomic_json_write(task_path, exported_task)
        root.mkdir(parents=True, exist_ok=True)
        temp_events = events_path.with_suffix(".jsonl.tmp")
        with temp_events.open("w", encoding="utf-8") as handle:
            for event in self.events(task_id):
                handle.write(canonical_json(event) + "\n")
        temp_events.chmod(0o600)
        temp_events.replace(events_path)
        _atomic_json_write(receipt_path, self.build_receipt(task_id))
        return {
            "task": str(task_path),
            "events": str(events_path),
            "receipt": str(receipt_path),
        }

    # ── Write Receipt Delegation ──────────────────────────────────────────────

    def issue_write_receipt(
        self,
        *,
        issuer: str,
        allowed_paths: list[str],
        ttl_seconds: float = 600.0,
    ) -> dict[str, Any]:
        """Issue a time-limited write receipt for worker delegation.

        Only an orchestrator-tier caller (Brain) should invoke this.
        The receipt authorizes a worker to write files matching the
        allowed_paths glob patterns until expiry.
        """
        if not allowed_paths:
            raise ValueError("allowed_paths must not be empty")
        if ttl_seconds <= 0 or ttl_seconds > 3600:
            raise ValueError("ttl_seconds must be between 1 and 3600")
        receipt_id = str(uuid.uuid4())
        now = self.clock()
        expires_at = now + ttl_seconds
        connection = self._connect()
        try:
            self._begin(connection)
            connection.execute(
                """
                INSERT INTO write_receipts(
                    receipt_id, issuer, allowed_paths_json,
                    expires_at, consumed, created_at
                ) VALUES (?, ?, ?, ?, 0, ?)
                """,
                (
                    receipt_id,
                    issuer,
                    canonical_json(allowed_paths),
                    expires_at,
                    now,
                ),
            )
            connection.commit()
        except Exception:
            connection.rollback()
            raise
        finally:
            connection.close()
        return {
            "receipt_id": receipt_id,
            "issuer": issuer,
            "allowed_paths": allowed_paths,
            "expires_at": expires_at,
            "ttl_seconds": ttl_seconds,
        }

    def validate_write_receipt(
        self, receipt_id: str, *, consumer_task_id: str | None = None
    ) -> dict[str, Any]:
        """Validate and consume a write receipt. Returns allowed_paths if valid.

        Raises ValueError if receipt is expired, already consumed, or not found.
        """
        now = self.clock()
        connection = self._connect()
        try:
            self._begin(connection)
            row = connection.execute(
                "SELECT * FROM write_receipts WHERE receipt_id = ?",
                (receipt_id,),
            ).fetchone()
            if row is None:
                raise ValueError(f"write receipt not found: {receipt_id}")
            if row["consumed"]:
                raise ValueError(f"write receipt already consumed: {receipt_id}")
            if row["expires_at"] < now:
                raise ValueError(
                    f"write receipt expired: {receipt_id} "
                    f"(expired {now - row['expires_at']:.0f}s ago)"
                )
            connection.execute(
                "UPDATE write_receipts SET consumed = 1, consumer_task_id = ? WHERE receipt_id = ?",
                (consumer_task_id, receipt_id),
            )
            connection.commit()
            allowed_paths = json.loads(row["allowed_paths_json"])
            return {
                "receipt_id": receipt_id,
                "issuer": row["issuer"],
                "allowed_paths": allowed_paths,
                "valid": True,
            }
        except Exception:
            connection.rollback()
            raise
        finally:
            connection.close()

    def revoke_write_receipt(self, receipt_id: str) -> bool:
        """Revoke an unconsumed receipt. Returns True if revoked, False if already consumed/missing."""
        connection = self._connect()
        try:
            self._begin(connection)
            row = connection.execute(
                "SELECT consumed FROM write_receipts WHERE receipt_id = ?",
                (receipt_id,),
            ).fetchone()
            if row is None:
                return False
            if row["consumed"]:
                return False  # Already consumed — too late to revoke
            connection.execute(
                "DELETE FROM write_receipts WHERE receipt_id = ?",
                (receipt_id,),
            )
            connection.commit()
            return True
        except Exception:
            connection.rollback()
            raise
        finally:
            connection.close()

    def list_active_receipts(self) -> list[dict[str, Any]]:
        """List all unconsumed, unexpired receipts for audit."""
        now = self.clock()
        connection = self._connect()
        try:
            rows = connection.execute(
                "SELECT * FROM write_receipts WHERE consumed = 0 AND expires_at > ?",
                (now,),
            ).fetchall()
            return [
                {
                    "receipt_id": r["receipt_id"],
                    "issuer": r["issuer"],
                    "allowed_paths": json.loads(r["allowed_paths_json"]),
                    "expires_at": r["expires_at"],
                    "remaining_seconds": round(r["expires_at"] - now, 1),
                }
                for r in rows
            ]
        finally:
            connection.close()
