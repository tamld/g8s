import contextlib
import importlib.util
import io
import json
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).parents[1] / "scripts" / "validate_g8s_pilot.py"
SPEC = importlib.util.spec_from_file_location("validate_g8s_pilot", SCRIPT)
validator = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(validator)


def record(**changes):
    value = {
        "pilot_id": "pilot-1", "previous_state": "IDENTITY_CAPTURED",
        "state": "QUALIFIED_CURRENT", "objective": "Verify bounded scan",
        "expected_result": "One sanitized evidence report",
        "admission": {"state": "QUALIFIED_CURRENT", "executable_path": "/opt/g8s",
                      "digest": "sha256:abc", "provenance": "immutable-build",
                      "capability": "captured", "upstream_ref": "v0.5.0+build.1",
                      "qualified_at": "2026-08-31T00:00:00Z",
                      "evidence_refs": ["evidence://binary-identity"]},
        "permission": "read_only", "allowed_paths": ["plans/reports/g8s/pilot-1.md"],
        "authorization": "auth://pilot-1-dispatch", "worker_role": "collector",
        "timeout_seconds": 30, "acceptance_checks": ["output has one evidence reference"],
        "stop_conditions": ["unexpected scope"], "evidence_sink": "evidence://pilot-1",
        "competing_hypotheses": ["binary drift"], "redaction": "declared",
        "evidence_refs": ["evidence://pilot-1"],
        "transition": {"at": "2026-08-31T12:00:00Z", "evidence_ref": "evidence://transition-1"},
        "learning": "retest",
    }
    value.update(changes)
    return value


def substance(result="pass"):
    return {"receipt": "evidence://receipt", "output": "evidence://output",
            "scope": "evidence://scope",
            "acceptance_results": {"output has one evidence reference": result}}


def issue(kind="fix", **changes):
    value = {"issue_type": kind, "title": "no-op", "severity": "high",
             "impact": "pilot blocked", "reproduction": "sanitized command",
             "expected": "bounded report", "actual": "error envelope",
             "receipt_verdict": "THEATER", "requested_direction": "fix argv",
             "redactions": "none", "unknowns": "none"}
    value.update(changes)
    return value


def gate(status="clear", **changes):
    value = {"status": status, "checked_at": "2026-08-31T11:00:00Z",
             "valid_until": "2026-08-31T13:00:00Z", "upstream_ref": "v0.5.0+build.1",
             "evidence_ref": "evidence://upstream-check"}
    value.update(changes)
    return value


def proposal(**changes):
    value = {"proposal_basis": "new_scope", "title": "new scope", "target_scope": "report",
             "user_outcome": "faster work", "desired_behavior": "new flow",
             "manual_baseline": "manual", "constraints": "read only", "success_criteria": "report",
             "evidence_plan": "sanitized receipt", "risk_rollback": "stop", "metrics": "time",
             "non_goals": "repair", "redactions": "none", "unknowns": "none"}
    value.update(changes)
    return value


class ValidatePilotTests(unittest.TestCase):
    def errors(self, **changes):
        return validator.validate(record(**changes))

    def test_qualified_current_admits_specification(self):
        self.assertEqual([], self.errors())

    def test_admission_and_dispatch_guards(self):
        self.assertIn("transition UNQUALIFIED -> DISPATCHED is forbidden", self.errors(
            previous_state="UNQUALIFIED", state="DISPATCHED",
            admission={**record()["admission"], "state": "UNQUALIFIED"}))
        self.assertIn("QUALIFIED_LIMITED permits read_only only", self.errors(
            previous_state="QUALIFIED_LIMITED", state="PILOT_SPECIFIED", permission="workspace_write",
            admission={**record()["admission"], "state": "QUALIFIED_LIMITED"}))
        errors = self.errors(previous_state="AWAIT_DISPATCH_AUTH", state="DISPATCHED",
                             authorization="", allowed_paths=[])
        self.assertIn("dispatch requires authorization", errors)
        self.assertIn("dispatch requires allowed_paths", errors)

    def test_error_route_requires_fix_issue_and_clear_gate(self):
        theater = record(previous_state="RECEIPT_CAPTURED", state="THEATER", receipt_kind="error")
        self.assertEqual([], validator.validate(theater))
        drafted = record(previous_state="THEATER", state="ISSUE_DRAFTED", receipt_kind="error",
                         issue=issue(), upstream_issue_check=gate())
        self.assertEqual([], validator.validate(drafted))

    def test_error_cannot_be_optimization(self):
        errors = self.errors(previous_state="THEATER", state="ISSUE_DRAFTED", receipt_kind="error",
                             issue=issue("optimization"), upstream_issue_check=gate())
        self.assertIn("THEATER requires issue_type fix", errors)

    def test_successful_failed_requirement_routes_to_optimization(self):
        violation = record(previous_state="RECEIPT_CAPTURED", state="REQUIREMENT_VIOLATION",
                           receipt_kind="success", substance_evidence=substance("fail"))
        self.assertEqual([], validator.validate(violation))
        drafted = record(previous_state="REQUIREMENT_VIOLATION", state="ISSUE_DRAFTED",
                         receipt_kind="success", substance_evidence=substance("fail"),
                         issue=issue("optimization", actual="requirement failed"), upstream_issue_check=gate())
        self.assertEqual([], validator.validate(drafted))

    def test_successful_violation_cannot_be_fix(self):
        errors = self.errors(previous_state="REQUIREMENT_VIOLATION", state="ISSUE_DRAFTED",
                             receipt_kind="success", substance_evidence=substance("fail"),
                             issue=issue("fix"), upstream_issue_check=gate())
        self.assertIn("REQUIREMENT_VIOLATION requires issue_type optimization", errors)

    def test_all_passes_verify_and_only_new_scope_proposes(self):
        verified = record(previous_state="RECEIPT_CAPTURED", state="SUBSTANCE_VERIFIED",
                          receipt_kind="success", substance_evidence=substance())
        self.assertEqual([], validator.validate(verified))
        drafted = record(previous_state="SUBSTANCE_VERIFIED", state="PROPOSAL_DRAFTED",
                         receipt_kind="success", substance_evidence=substance(), proposal=proposal())
        self.assertEqual([], validator.validate(drafted))

    def test_unknown_or_extra_results_stay_evidence_gap(self):
        unknown = record(previous_state="RECEIPT_CAPTURED", state="EVIDENCE_GAP", receipt_kind="success",
                         substance_evidence=substance("unknown"))
        self.assertEqual([], validator.validate(unknown))
        extra = substance("fail")
        extra["acceptance_results"]["undeclared"] = "fail"
        errors = self.errors(previous_state="RECEIPT_CAPTURED", state="REQUIREMENT_VIOLATION",
                             receipt_kind="success", substance_evidence=extra)
        self.assertIn("acceptance_results must exactly match acceptance_checks", errors)
        mixed = {"receipt": "evidence://receipt", "output": "evidence://output", "scope": "evidence://scope",
                 "acceptance_results": {"output has one evidence reference": "fail", "scope is bounded": "unknown"}}
        errors = self.errors(previous_state="RECEIPT_CAPTURED", state="REQUIREMENT_VIOLATION",
                             receipt_kind="success", acceptance_checks=["output has one evidence reference", "scope is bounded"],
                             substance_evidence=mixed)
        self.assertIn("REQUIREMENT_VIOLATION requires successful failed acceptance evidence", errors)

    def test_evidence_gap_and_failure_cannot_draft_proposal_or_issue(self):
        self.assertIn("transition EVIDENCE_GAP -> ISSUE_DRAFTED is forbidden", self.errors(
            previous_state="EVIDENCE_GAP", state="ISSUE_DRAFTED", issue=issue(), upstream_issue_check=gate()))
        self.assertIn("transition THEATER -> PROPOSAL_DRAFTED is forbidden", self.errors(
            previous_state="THEATER", state="PROPOSAL_DRAFTED", proposal=proposal()))

    def test_nonclear_gate_suppresses_issue(self):
        suppressed = record(previous_state="THEATER", state="ISSUE_SUPPRESSED", receipt_kind="error",
                            upstream_issue_check=gate("duplicate"))
        self.assertEqual([], validator.validate(suppressed))
        errors = self.errors(previous_state="THEATER", state="ISSUE_DRAFTED", receipt_kind="error",
                             issue=issue(), upstream_issue_check=gate("stale"))
        self.assertIn("ISSUE_DRAFTED requires upstream_issue_check status clear", errors)

    def test_issue_gate_requires_fresh_matching_evidence(self):
        base = dict(previous_state="THEATER", state="ISSUE_DRAFTED", receipt_kind="error", issue=issue())
        self.assertIn("ISSUE_DRAFTED requires upstream_issue_check", self.errors(**base))
        mismatched = gate(upstream_ref="v0.4.0")
        self.assertIn("upstream_issue_check upstream_ref must match admission upstream_ref",
                      self.errors(**base, upstream_issue_check=mismatched))
        expired = gate(valid_until="2026-08-31T11:30:00Z")
        self.assertIn("upstream_issue_check is expired before transition", self.errors(**base, upstream_issue_check=expired))

    def test_schema_and_privacy_guards(self):
        self.assertIn("receipt_kind must be error or success", self.errors(receipt_kind="weird"))
        self.assertIn("unknown field: undocumented_field", self.errors(undocumented_field="no"))
        errors = self.errors(previous_state="THEATER", state="ISSUE_DRAFTED", receipt_kind="error",
                             issue=issue(reproduction="Authorization: Basic YWxwaGE6YmV0YQ=="), upstream_issue_check=gate())
        self.assertIn("sensitive value forbidden: issue.reproduction", errors)
        for secret in ("Cookie: session=alpha", "-----BEGIN RSA PRIVATE KEY-----"):
            errors = self.errors(previous_state="THEATER", state="ISSUE_DRAFTED", receipt_kind="error",
                                 issue=issue(reproduction=secret), upstream_issue_check=gate())
            self.assertIn("sensitive value forbidden: issue.reproduction", errors)
        self.assertIn("admission unknown field: drift", self.errors(admission={**record()["admission"], "drift": "no"}))

    def test_drafts_revalidate_branch_and_evidence_gap_cannot_render(self):
        forged = self.errors(previous_state="THEATER", state="ISSUE_DRAFTED", receipt_kind="success",
                             issue=issue(), upstream_issue_check=gate())
        self.assertIn("THEATER requires receipt_kind error", forged)
        mixed = substance("fail")
        mixed["acceptance_results"]["other"] = "unknown"
        errors = self.errors(previous_state="REQUIREMENT_VIOLATION", state="ISSUE_DRAFTED",
                             receipt_kind="success", substance_evidence=mixed,
                             issue=issue("optimization"), upstream_issue_check=gate())
        self.assertIn("acceptance_results must exactly match acceptance_checks", errors)
        gap = record(previous_state="RECEIPT_CAPTURED", state="EVIDENCE_GAP", receipt_kind="success",
                     substance_evidence=substance("unknown"), issue=issue())
        self.assertIn("issue packet requires ISSUE_DRAFTED", validator.validate(gap))

    def test_gate_rejects_non_rfc3339_without_crashing(self):
        errors = self.errors(previous_state="THEATER", state="ISSUE_DRAFTED", receipt_kind="error",
                             issue=issue(), upstream_issue_check=gate(checked_at="2026-08-31"))
        self.assertIn("upstream_issue_check timestamps must be RFC 3339", errors)

    def test_renderer_and_cli_reject_packet_kind_mismatch(self):
        value = record(previous_state="THEATER", state="ISSUE_DRAFTED", receipt_kind="error",
                       issue=issue(), upstream_issue_check=gate())
        self.assertIn("## Issue type\n\nfix", validator.render(value, "issue"))
        theater = record(previous_state="RECEIPT_CAPTURED", state="THEATER", receipt_kind="error", issue=issue())
        self.assertIn("issue packet requires ISSUE_DRAFTED", validator.validate(theater))
        verified = record(previous_state="RECEIPT_CAPTURED", state="SUBSTANCE_VERIFIED", receipt_kind="success",
                          substance_evidence=substance(), proposal=proposal())
        self.assertIn("proposal packet requires PROPOSAL_DRAFTED", validator.validate(verified))
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "pilot.json"
            path.write_text(json.dumps(value), encoding="utf-8")
            stdout = io.StringIO()
            with contextlib.redirect_stdout(stdout):
                result = validator.main(["--input", str(path), "--kind", "proposal"])
            self.assertEqual(1, result)
            self.assertIn("ERROR: record has no proposal packet", stdout.getvalue())
        self.assertRaises(ValueError, validator.render, theater, "issue")
        self.assertRaises(ValueError, validator.render, verified, "proposal")


if __name__ == "__main__":
    unittest.main()
