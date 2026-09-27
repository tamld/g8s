package brief

// #398 RED-first: L2 deterministic DoR floor (table-driven, one test per
// check class) + L4 advisory skill routing pins.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFloorGoalPresent(t *testing.T) {
	r := CheckDoRFloor("", "scope: internal/x/y.go", "done when x", "")
	if r.OK {
		t.Fatal("empty title must fail goal_present")
	}
	for _, c := range r.Checks {
		if c.Name == "goal_present" && c.Pass {
			t.Fatal("goal_present must fail for empty title")
		}
	}
}

func TestFloorScopeFilesListed(t *testing.T) {
	if r := CheckDoRFloor("title", "no paths here at all", "dod", ""); r.OK {
		t.Fatal("payload without any file path must fail scope_files_listed")
	}
	if r := CheckDoRFloor("title", "touch internal/worker/worker.go", "dod", ""); !r.OK {
		t.Fatalf("payload with a concrete path must pass: %+v", r)
	}
}

func TestFloorDodPresent(t *testing.T) {
	if r := CheckDoRFloor("title", "internal/x/y.go", "   ", ""); r.OK {
		t.Fatal("blank DoD must fail dod_present")
	}
}

func TestFloorReceiptPathForWorkspaceWrite(t *testing.T) {
	r := CheckDoRFloor("title", "write tests/*.go", "done", "workspace_write")
	if r.OK {
		t.Fatal("workspace_write without receipt reference must fail receipt_path_present")
	}
	if r := CheckDoRFloor("title", "write tests/*.go receipt_id=rcp-1", "done", "workspace_write"); !r.OK {
		t.Fatalf("workspace_write with receipt reference must pass: %+v", r)
	}
	// read_only dispatches are exempt from the receipt check
	if r := CheckDoRFloor("title", "write tests/*.go", "done", "read_only"); !r.OK {
		t.Fatalf("read_only must not require receipt: %+v", r)
	}
}

func TestFloorFailureErrorListsChecks(t *testing.T) {
	r := CheckDoRFloor("", "", "", "")
	err := FloorFailureError(r)
	if err == nil || !strings.Contains(err.Error(), "goal_present") || !strings.Contains(err.Error(), "dod_present") {
		t.Fatalf("floor failure must name every failed check, got %v", err)
	}
}

func TestSuggestSkillsKeywordMatch(t *testing.T) {
	bank := []SkillManifest{
		{Name: "autoreview", Description: "review the PR diff quality"},
		{Name: "debug", Description: "diagnose failing tests"},
	}
	got := SuggestSkills("review the auth diff", "", bank, 3)
	if len(got) != 1 || got[0].Name != "autoreview" {
		t.Fatalf("keyword overlap must rank autoreview first, got %+v", got)
	}
}

func TestSuggestSkillsEmptyBankDegrades(t *testing.T) {
	if got := SuggestSkills("anything", "payload", nil, 3); len(got) != 0 {
		t.Fatalf("empty bank must yield no suggestions, got %+v", got)
	}
}

func TestScanSkillBank(t *testing.T) {
	dir := t.TempDir()
	skill := filepath.Join(dir, "autoreview")
	if err := os.MkdirAll(skill, 0o700); err != nil {
		t.Fatal(err)
	}
	md := "---\nname: autoreview\ndescription: review the diff\n---\nbody"
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte(md), 0o600); err != nil {
		t.Fatal(err)
	}
	bank := ScanSkillBank(dir)
	if len(bank) != 1 || bank[0].Name != "autoreview" || bank[0].Description != "review the diff" {
		t.Fatalf("scan: %+v", bank)
	}
	// missing bank dir degrades silently
	if bank := ScanSkillBank(filepath.Join(dir, "nope")); bank != nil {
		t.Fatalf("missing bank must be nil, got %+v", bank)
	}
}
