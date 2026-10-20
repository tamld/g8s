package main

import (
	"testing"
)

func TestParseAcceptanceCriteria(t *testing.T) {
	issueBody := `
### Context
We are implementing Slice B1.

## Acceptance Criteria
- [ ] Criterion 1: pure Go implementation
- [x] Criterion 2: test coverage (ev-11223344)
* [ ] Criterion 3: markdown parsing
* [x] Criterion 4: verified by TestAppeasement
- Criterion 5: plain bullet item under heading
1. Criterion 6: numbered item under heading
2) Criterion 7: paren numbered item

## Additional Deliverables
- [x] Bonus deliverable outside criteria section
- Plain bullet outside criteria section
`

	criteria := ParseAcceptanceCriteria(issueBody)
	if len(criteria) != 8 {
		t.Fatalf("expected 8 criteria, got %d: %+v", len(criteria), criteria)
	}

	// Verify Index, Checked, Text, CitedEvidence
	expected := []struct {
		idx     int
		checked bool
		text    string
		ev      string
	}{
		{1, false, "Criterion 1: pure Go implementation", ""},
		{2, true, "Criterion 2: test coverage (ev-11223344)", "ev-11223344"},
		{3, false, "Criterion 3: markdown parsing", ""},
		{4, true, "Criterion 4: verified by TestAppeasement", "TestAppeasement"},
		{5, false, "Criterion 5: plain bullet item under heading", ""},
		{6, false, "Criterion 6: numbered item under heading", ""},
		{7, false, "Criterion 7: paren numbered item", ""},
		{8, true, "Bonus deliverable outside criteria section", ""},
	}

	for i, exp := range expected {
		c := criteria[i]
		if c.Index != exp.idx {
			t.Errorf("criteria[%d].Index = %d, expected %d", i, c.Index, exp.idx)
		}
		if c.Checked != exp.checked {
			t.Errorf("criteria[%d].Checked = %v, expected %v", i, c.Checked, exp.checked)
		}
		if c.Text != exp.text {
			t.Errorf("criteria[%d].Text = %q, expected %q", i, c.Text, exp.text)
		}
		if c.CitedEvidence != exp.ev {
			t.Errorf("criteria[%d].CitedEvidence = %q, expected %q", i, c.CitedEvidence, exp.ev)
		}
	}
}

func TestG7_C7_ClosesIssueCriterionUnmet(t *testing.T) {
	fakeForge := NewFakeForge()
	fakeForge.Issues["#617"] = FakeIssue{
		SameRepo: true,
		Open:     true,
		Title:    "G4/G7 implementation slice",
		Body: `
## Acceptance Criteria
- [ ] Criterion 1: pure Go appeasement detector
- [x] Criterion 2: issue closure gate with evidence (ev-c7pass01)
`,
	}

	// Case 1: Closes #617 with unmet criterion 1 -> G7 FAIL
	fullText := "Closes #617\n<!-- f0:status -->\n<!-- /f0:status -->"
	res, err := CheckIssueClosureGate("", fullText, fakeForge, nil)
	if err != nil {
		t.Fatalf("CheckIssueClosureGate error: %v", err)
	}
	if res.Status != "FAIL" {
		t.Fatalf("expected G7 FAIL, got %s (%s)", res.Status, res.Detail)
	}
	expectedDetail := "issue #617 has unmet/uncited acceptance criteria (use Refs #617 and comment unmet criteria)"
	if res.Detail != expectedDetail {
		t.Fatalf("expected detail %q, got %q", expectedDetail, res.Detail)
	}

	// Case 2: Refs #617 passing (does NOT block closure)
	refsText := "Refs #617\n<!-- f0:status -->\n<!-- /f0:status -->"
	resRefs, err := CheckIssueClosureGate("", refsText, fakeForge, nil)
	if err != nil {
		t.Fatalf("CheckIssueClosureGate error: %v", err)
	}
	if resRefs.Status != "PASS" {
		t.Fatalf("expected G7 PASS for Refs #617, got %s (%s)", resRefs.Status, resRefs.Detail)
	}

	// Case 3: Closes #617 with explicit per-criterion citation for criterion 1 -> PASS
	explicitText := "Closes #617\nCriterion 1: ev-11223344\nCriterion 2: ev-c7pass01"
	resExplicit, err := CheckIssueClosureGate("", explicitText, fakeForge, nil)
	if err != nil {
		t.Fatalf("CheckIssueClosureGate error: %v", err)
	}
	if resExplicit.Status != "PASS" {
		t.Fatalf("expected G7 PASS with explicit citation, got %s (%s)", resExplicit.Status, resExplicit.Detail)
	}
}

func TestG7_DefenseInDepth144_DroppedDeliverables(t *testing.T) {
	fakeForge := NewFakeForge()
	fakeForge.Issues["144"] = FakeIssue{
		SameRepo: true,
		Open:     true,
		Title:    "Defense in depth fixtures delivery",
		Body: `
## Acceptance Criteria
- [x] Deliver 19 fixtures for policy engine (ev-fix14401)
`,
	}

	// Ledger shows dropped deliverables: only 7 of 19 fixtures delivered
	ledgerEntries := []Entry{
		{
			ID:     "ev-deliver-144",
			Kind:   "g8s-deliver",
			Status: "PASS",
			Extra: map[string]any{
				"task_id":            "task-did-144",
				"fixtures_delivered": 7,
				"fixtures_expected":  19,
				"notes":              "only 7/19 fixtures were delivered",
			},
		},
	}

	// Case 1: Closes #144 with dropped deliverables -> G7 FAIL
	fullText := "Closes #144\nEvidence: ev-fix14401"
	res, err := CheckIssueClosureGate("", fullText, fakeForge, ledgerEntries)
	if err != nil {
		t.Fatalf("CheckIssueClosureGate error: %v", err)
	}
	if res.Status != "FAIL" {
		t.Fatalf("expected G7 FAIL for dropped deliverables, got %s (%s)", res.Status, res.Detail)
	}
	expectedDetail := "issue #144 has unmet/uncited acceptance criteria (use Refs #144 and comment unmet criteria)"
	if res.Detail != expectedDetail {
		t.Fatalf("expected detail %q, got %q", expectedDetail, res.Detail)
	}

	// Case 2: Refs #144 does NOT block
	refsText := "Refs #144\nStill working on remaining 12 fixtures."
	resRefs, err := CheckIssueClosureGate("", refsText, fakeForge, ledgerEntries)
	if err != nil {
		t.Fatalf("CheckIssueClosureGate error: %v", err)
	}
	if resRefs.Status != "PASS" {
		t.Fatalf("expected G7 PASS for Refs #144, got %s (%s)", resRefs.Status, resRefs.Detail)
	}
}

func TestG7_AllCriteriaChecked_WithAndWithoutEvidence(t *testing.T) {
	fakeForge := NewFakeForge()
	fakeForge.Issues["#618"] = FakeIssue{
		SameRepo: true,
		Open:     true,
		Title:    "All criteria checked issue",
		Body: `
## Acceptance Criteria
- [x] Sub-task A
- [x] Sub-task B
`,
	}

	// Case 1: All checked with evidence in PR body -> PASS
	prWithEvidence := "Fixes #618\nVerified by TestAppeasement and ev-12345678."
	resPass, err := CheckIssueClosureGate("", prWithEvidence, fakeForge, nil)
	if err != nil {
		t.Fatalf("CheckIssueClosureGate error: %v", err)
	}
	if resPass.Status != "PASS" {
		t.Fatalf("expected G7 PASS with evidence, got %s (%s)", resPass.Status, resPass.Detail)
	}

	// Case 2: All checked but NO evidence anywhere -> FAIL
	prWithoutEvidence := "Fixes #618\nLooks good to me."
	resFail, err := CheckIssueClosureGate("", prWithoutEvidence, fakeForge, nil)
	if err != nil {
		t.Fatalf("CheckIssueClosureGate error: %v", err)
	}
	if resFail.Status != "FAIL" {
		t.Fatalf("expected G7 FAIL without evidence, got %s (%s)", resFail.Status, resFail.Detail)
	}
}

func TestG7_ForgeLookupFormats(t *testing.T) {
	fakeForge := NewFakeForge()
	fakeForge.Issues["https://github.com/tamld/g8s/issues/625"] = FakeIssue{
		SameRepo: true,
		Open:     true,
		Title:    "G4 appeasement detector",
		Body: `
## Acceptance Criteria
- [x] Detect token rewrites (ev-62500001)
- [x] Detect unread YAML keys (ev-62500002)
`,
	}

	// Directive uses full URL
	fullURLText := "Fixes https://github.com/tamld/g8s/issues/625"
	resURL, err := CheckIssueClosureGate("", fullURLText, fakeForge, nil)
	if err != nil {
		t.Fatalf("CheckIssueClosureGate error: %v", err)
	}
	if resURL.Status != "PASS" {
		t.Fatalf("expected G7 PASS for full URL directive, got %s (%s)", resURL.Status, resURL.Detail)
	}

	// Directive uses #625 (resolved via suffix match in fakeForge)
	shortText := "Resolved #625"
	resShort, err := CheckIssueClosureGate("", shortText, fakeForge, nil)
	if err != nil {
		t.Fatalf("CheckIssueClosureGate error: %v", err)
	}
	if resShort.Status != "PASS" {
		t.Fatalf("expected G7 PASS for #625, got %s (%s)", resShort.Status, resShort.Detail)
	}
}

func TestG7_NoClosingDirective(t *testing.T) {
	text := "This is a PR with no issue references at all."
	res, err := CheckIssueClosureGate("", text, nil, nil)
	if err != nil {
		t.Fatalf("CheckIssueClosureGate error: %v", err)
	}
	if res.Status != "PASS" {
		t.Fatalf("expected PASS when no closing directive, got %s", res.Status)
	}
	if res.Detail != "no issue closure directives found" {
		t.Fatalf("unexpected detail: %s", res.Detail)
	}
}
