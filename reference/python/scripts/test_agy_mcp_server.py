#!/usr/bin/env python3
"""Focused tests for the dependency-light AGY MCP server."""

from __future__ import annotations

import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest import mock

import agy_dispatch
import agy_mcp_server


def _tool_payload(result: dict) -> dict:
    text = result["content"][0]["text"]
    return json.loads(text)


class AgyMcpServerTest(unittest.TestCase):
    def test_tool_list_exposes_minimum_surface(self) -> None:
        names = {tool["name"] for tool in agy_mcp_server.tool_list()["tools"]}

        self.assertEqual(
            {
                "agy_list_roles",
                "agy_list_permissions",
                "agy_self_awareness",
                "agy_dispatch_task",
                "agy_submit_task",
                "agy_get_task",
                "agy_list_tasks",
                "agy_cancel_task",
            },
            names,
        )

    def test_list_permissions_marks_workspace_write_disabled(self) -> None:
        result = agy_mcp_server.call_tool({"name": "agy_list_permissions", "arguments": {}})
        payload = _tool_payload(result)
        workspace = next(item for item in payload["permissions"] if item["name"] == "workspace_write")

        self.assertFalse(result["isError"])
        self.assertFalse(workspace["mcp_enabled"])
        self.assertIn("workspace_write", payload["disabled_reason"])

    def test_workspace_write_is_blocked_before_dispatch(self) -> None:
        with mock.patch("agy_mcp_server.agy_dispatch.run_agy_command") as run_mock:
            result = agy_mcp_server.call_tool(
                {
                    "name": "agy_dispatch_task",
                    "arguments": {
                        "prompt": "Edit the workspace.",
                        "permission": "workspace_write",
                    },
                }
            )

        payload = _tool_payload(result)
        self.assertTrue(result["isError"])
        self.assertEqual("blocked_by_policy", payload["status"])
        run_mock.assert_not_called()

    def test_read_only_skip_permissions_is_blocked_by_harness(self) -> None:
        with mock.patch("agy_mcp_server.agy_dispatch.run_agy_command") as run_mock:
            result = agy_mcp_server.call_tool(
                {
                    "name": "agy_dispatch_task",
                    "arguments": {
                        "prompt": "Collect paths only.",
                        "permission": "read_only",
                        "skip_permissions": True,
                        "add_dirs": [str(Path.cwd())],
                    },
                }
            )

        payload = _tool_payload(result)
        self.assertTrue(result["isError"])
        self.assertEqual("blocked_by_harness", payload["status"])
        self.assertIn("skip-permissions", payload["reason"])
        run_mock.assert_not_called()

    def test_missing_agy_binary_returns_setup_required(self) -> None:
        with mock.patch("agy_mcp_server.agy_dispatch.resolve_agy_binary", return_value=None):
            result = agy_mcp_server.call_tool(
                {
                    "name": "agy_dispatch_task",
                    "arguments": {
                        "prompt": "Collect paths only.",
                        "permission": "automation_read",
                        "skip_permissions": True,
                        "add_dirs": [str(Path.cwd())],
                    },
                }
            )

        payload = _tool_payload(result)
        self.assertTrue(result["isError"])
        self.assertEqual("setup_required", payload["status"])
        self.assertIn("setup_hint", payload)

    def test_synchronous_dispatch_requires_scope_and_sandbox(self) -> None:
        for arguments, expected in (
            ({"prompt": "Collect paths only."}, "explicit add_dirs"),
            (
                {
                    "prompt": "Collect paths only.",
                    "add_dirs": [str(Path.cwd())],
                    "no_sandbox": True,
                },
                "no_sandbox",
            ),
        ):
            result = agy_mcp_server.call_tool(
                {"name": "agy_dispatch_task", "arguments": arguments}
            )
            payload = _tool_payload(result)
            self.assertTrue(result["isError"])
            self.assertEqual("blocked_by_policy", payload["status"])
            self.assertIn(expected, payload["reason"])

    def test_fake_automation_read_dispatch_keeps_sandbox(self) -> None:
        completed = subprocess.CompletedProcess(
            args=[],
            returncode=0,
            stdout='{"findings": []}',
            stderr="",
        )
        with mock.patch(
            "agy_mcp_server.agy_dispatch.resolve_agy_binary",
            return_value=Path("/tmp/agy"),
        ), mock.patch(
            "agy_mcp_server.agy_dispatch.run_agy_command", return_value=completed
        ) as run_mock:
            result = agy_mcp_server.call_tool(
                {
                    "name": "agy_dispatch_task",
                    "arguments": {
                        "prompt": "Collect paths only.",
                        "permission": "automation_read",
                        "skip_permissions": True,
                        "model": "Gemini 3.5 Flash (High)",
                        "add_dirs": [str(Path.cwd())],
                    },
                }
            )

        payload = _tool_payload(result)
        cmd = run_mock.call_args.args[0]
        self.assertFalse(result["isError"])
        self.assertTrue(payload["ok"])
        self.assertIn("--dangerously-skip-permissions", cmd)
        self.assertIn("--sandbox", cmd)
        self.assertEqual("automation_read", payload["permission"])

    def test_contract_violation_turns_tool_result_error(self) -> None:
        completed = subprocess.CompletedProcess(
            args=[],
            returncode=0,
            stdout="Session logged to log.md\n",
            stderr="",
        )
        with mock.patch(
            "agy_mcp_server.agy_dispatch.resolve_agy_binary",
            return_value=Path("/tmp/agy"),
        ), mock.patch(
            "agy_mcp_server.agy_dispatch.run_agy_command", return_value=completed
        ):
            result = agy_mcp_server.call_tool(
                {
                    "name": "agy_dispatch_task",
                    "arguments": {
                        "prompt": "Collect paths only.",
                        "add_dirs": [str(Path.cwd())],
                    },
                }
            )

        payload = _tool_payload(result)
        self.assertTrue(result["isError"])
        self.assertEqual(agy_dispatch.READ_ONLY_CONTRACT_EXIT, payload["harness_returncode"])
        self.assertEqual("wiki_reflect_side_effect", payload["contract_violation"]["violations"][0]["type"])

    def test_jsonrpc_initialize_and_tools_list(self) -> None:
        init_response = agy_mcp_server.handle_request(
            {"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {}}
        )
        list_response = agy_mcp_server.handle_request(
            {"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": {}}
        )

        self.assertEqual("agy_dispatch_mcp", init_response["result"]["serverInfo"]["name"])
        self.assertEqual("2025-06-18", init_response["result"]["protocolVersion"])
        self.assertEqual(8, len(list_response["result"]["tools"]))

    def test_initialize_negotiates_supported_client_version(self) -> None:
        response = agy_mcp_server.handle_request(
            {
                "jsonrpc": "2.0",
                "id": 1,
                "method": "initialize",
                "params": {"protocolVersion": "2024-11-05"},
            }
        )

        self.assertEqual("2024-11-05", response["result"]["protocolVersion"])

    def test_durable_submit_get_list_and_cancel(self) -> None:
        with tempfile.TemporaryDirectory() as tmp, mock.patch.dict(
            os.environ,
            {"AGY_DISPATCH_STATE_DB": str(Path(tmp) / "state.sqlite3")},
        ):
            submit_result = agy_mcp_server.call_tool(
                {
                    "name": "agy_submit_task",
                    "arguments": {
                        "prompt": "Collect paths only.",
                        "idempotency_key": "mcp-durable-task",
                        "model": "Gemini 3.5 Flash (Low)",
                        "add_dirs": [tmp],
                    },
                }
            )
            submitted = _tool_payload(submit_result)["task"]
            self.assertEqual("QUEUED", submitted["state"])
            self.assertNotIn("prompt", submitted["request"])

            duplicate_result = agy_mcp_server.call_tool(
                {
                    "name": "agy_submit_task",
                    "arguments": {
                        "prompt": "Collect paths only.",
                        "idempotency_key": "mcp-durable-task",
                        "model": "Gemini 3.5 Flash (Low)",
                        "add_dirs": [tmp],
                    },
                }
            )
            duplicate = _tool_payload(duplicate_result)["task"]
            self.assertEqual(submitted["task_id"], duplicate["task_id"])

            get_result = agy_mcp_server.call_tool(
                {
                    "name": "agy_get_task",
                    "arguments": {"task_id": submitted["task_id"]},
                }
            )
            self.assertEqual("QUEUED", _tool_payload(get_result)["task"]["state"])

            list_result = agy_mcp_server.call_tool(
                {"name": "agy_list_tasks", "arguments": {"state": "QUEUED"}}
            )
            self.assertEqual(1, len(_tool_payload(list_result)["tasks"]))

            cancel_result = agy_mcp_server.call_tool(
                {
                    "name": "agy_cancel_task",
                    "arguments": {
                        "task_id": submitted["task_id"],
                        "reason": "test complete",
                    },
                }
            )
            self.assertEqual("CANCELLED", _tool_payload(cancel_result)["task"]["state"])

    def test_durable_submit_blocks_workspace_write_and_no_sandbox(self) -> None:
        with tempfile.TemporaryDirectory() as tmp, mock.patch.dict(
            os.environ,
            {"AGY_DISPATCH_STATE_DB": str(Path(tmp) / "state.sqlite3")},
        ):
            for extra in ({"permission": "workspace_write"}, {"no_sandbox": True}):
                result = agy_mcp_server.call_tool(
                    {
                        "name": "agy_submit_task",
                        "arguments": {
                            "prompt": "Collect paths only.",
                            "idempotency_key": f"blocked-{sorted(extra)}",
                            "model": "Gemini 3.5 Flash (Low)",
                            "add_dirs": [tmp],
                            **extra,
                        },
                    }
                )
                self.assertTrue(result["isError"])


if __name__ == "__main__":
    unittest.main()
