package brief

// #398 RED-first: L2 deterministic DoR floor (table-driven, one test per
// check class) + L4 advisory skill routing pins.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/reach"
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

func TestFloorPreExecutionResearchDebateGateByLane(t *testing.T) {
	basePayload := "touch internal/worker/worker.go receipt_id=rcp-1"
	baseDoD := "go test ./internal/worker/..."

	// Lane 0 and Lane D bypass the heavy pre-execution research/debate gate
	for _, exemptLane := range []string{"", "0", "D", "d"} {
		res := CheckDoRFloorForLane("fix typo", basePayload, baseDoD, "workspace_write", exemptLane)
		if !res.OK {
			t.Fatalf("lane %q must bypass heavy pre-execution gate, got failures: %+v", exemptLane, res.Checks)
		}
	}

	// Lane F, R, S fail closed when research, Socratic debate, or spec contract references are missing
	for _, strictLane := range []string{"F", "R", "S"} {
		res := CheckDoRFloorForLane("implement feature", basePayload, baseDoD, "workspace_write", strictLane)
		if res.OK {
			t.Fatalf("lane %q without pre-execution research/debate/spec must fail closed", strictLane)
		}
		errMsg := FloorFailureError(res).Error()
		for _, wantCheck := range []string{"research_world_state_present", "socratic_debate_present", "spec_contract_present"} {
			if !strings.Contains(errMsg, wantCheck) {
				t.Fatalf("lane %q failure error missing %q: %s", strictLane, wantCheck, errMsg)
			}
		}
	}

	// Lane F passes when World-State research, Socratic debate (hypothesis + falsification), and Spec refs exist
	validPayloadF := `touch internal/worker/worker.go receipt_id=rcp-1
Research: Local: internal/worker/worker.go, Upstream: stdlib context cancellation.
Socratic Debate: Hypothesis H1 uses channel close; Counterexample / Falsification: concurrent writers panic, so Alternative Rejected in favor of sync.Once.
Spec Contract: DELTA-35, SRS REQ-FUNC-01, AC-01, FSM state transition READY -> RUNNING.`
	if res := CheckDoRFloorForLane("implement worker gate", validPayloadF, baseDoD, "workspace_write", "F"); !res.OK {
		t.Fatalf("valid Lane F brief must pass: %+v", res.Checks)
	}

	// Lane S additionally requires explicit CVE/CWE/STRIDE/OWASP/trust-boundary evidence
	if res := CheckDoRFloorForLane("security fix", validPayloadF, baseDoD, "workspace_write", "S"); res.OK {
		t.Fatalf("Lane S without CVE/CWE/STRIDE/OWASP/trust-boundary citation must fail")
	}
	validPayloadS := validPayloadF + "\nSecurity Advisory: CWE-22 path traversal & trust-boundary check (no-known-cve)."
	if res := CheckDoRFloorForLane("security fix", validPayloadS, baseDoD, "workspace_write", "S"); !res.OK {
		t.Fatalf("valid Lane S brief with CWE & trust-boundary evidence must pass: %+v", res.Checks)
	}
}

func laneFValidBriefBase() string {
	return `touch internal/worker/worker.go receipt_id=rcp-1
Research: Local: internal/worker/worker.go, Upstream: stdlib context cancellation.
Socratic Debate: Hypothesis H1 uses channel close; Counterexample / Falsification: concurrent writers panic, so Alternative Rejected in favor of sync.Once.
Spec Contract: DELTA-35, SRS REQ-FUNC-01, AC-01, FSM state transition READY -> RUNNING.`
}

func reachCiteMarker(a reach.Artifact) string {
	return `reach-cite:{"url":"` + a.URL + `","channel":"` + a.Channel + `","action":"` + a.Action + `","backend":"` + a.Backend + `","body_sha256":"` + a.BodySHA256 + `","fetched_at":"` + a.FetchedAt + `"}`
}

func TestDoRFloor_ReachCiteInvalidArtifactFails(t *testing.T) {
	baseDoD := "go test ./internal/worker/..."
	payload := laneFValidBriefBase() + "\n" + `reach-cite:{"url":"","channel":"web","action":"read","backend":"web-https-get","body_sha256":"","fetched_at":"2026-10-31T00:00:00Z"}`
	res := CheckDoRFloorForLane("implement worker gate", payload, baseDoD, "workspace_write", "F")
	if res.OK {
		t.Fatal("brief with invalid reach-cite artifact must fail floor")
	}
	var citeCheck *FloorCheck
	for i := range res.Checks {
		if res.Checks[i].Name == "reach_citation_valid" {
			citeCheck = &res.Checks[i]
			break
		}
	}
	if citeCheck == nil || citeCheck.Pass {
		t.Fatalf("expected reach_citation_valid failure, checks=%+v", res.Checks)
	}
}

func TestDoRFloor_NonReachCitationPassthrough(t *testing.T) {
	baseDoD := "go test ./internal/worker/..."
	res := CheckDoRFloorForLane("implement worker gate", laneFValidBriefBase(), baseDoD, "workspace_write", "F")
	if !res.OK {
		t.Fatalf("unmarked Upstream research prose must pass reach_citation_valid passthrough: %+v", res.Checks)
	}
	for _, c := range res.Checks {
		if c.Name == "reach_citation_valid" && !c.Pass {
			t.Fatalf("reach_citation_valid must pass with zero markers: %+v", c)
		}
	}
}

func TestDoRFloor_ReachCiteValidArtifactPasses(t *testing.T) {
	baseDoD := "go test ./internal/worker/..."
	now := time.Now().UTC()
	a := reach.Artifact{
		URL:        "https://example.com/evidence",
		Channel:    "web",
		Action:     "read",
		Backend:    "web-https-get",
		BodySHA256: reach.DigestBody("evidence-body"),
		FetchedAt:  now.Format(time.RFC3339),
	}
	payload := laneFValidBriefBase() + "\n" + reachCiteMarker(a)
	res := CheckDoRFloorForLane("implement worker gate", payload, baseDoD, "workspace_write", "F")
	if !res.OK {
		t.Fatalf("valid reach-cite artifact must pass floor: %+v", res.Checks)
	}
}

func TestReadVerifiedSkillDigestPinningAndTierContainment(t *testing.T) {
	dir := t.TempDir()
	for _, s := range []struct {
		name string
		desc string
		body string
	}{
		{"g8s-red-prove", "Red to green proof", "Always run red test before green fix."},
		{"g8s-supervisor", "F0 exclusive charter", "Supervisor only instructions."},
	} {
		sDir := filepath.Join(dir, s.name)
		if err := os.MkdirAll(sDir, 0o700); err != nil {
			t.Fatal(err)
		}
		content := "---\nname: " + s.name + "\ndescription: " + s.desc + "\n---\n" + s.body
		if err := os.WriteFile(filepath.Join(sDir, "SKILL.md"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// 1. F0 (default unset tier) sees both skills
	t.Setenv("G8S_TIER", "")
	listF0 := ListVerifiedSkills(dir)
	if len(listF0) != 2 {
		t.Fatalf("expected 2 skills for F0, got %d: %+v", len(listF0), listF0)
	}

	// 2. ReadVerifiedSkill succeeds with matching digest and fails closed on mismatch
	res, err := ReadVerifiedSkill("g8s-red-prove", "", dir)
	if err != nil {
		t.Fatalf("ReadVerifiedSkill failed: %v", err)
	}
	if !strings.HasPrefix(res.Digest, "sha256:") || res.URI != "skill://g8s/shared/g8s-red-prove" {
		t.Fatalf("unexpected digest or URI: %+v", res)
	}
	if _, err := ReadVerifiedSkill("skill://g8s/shared/g8s-red-prove", res.Digest, dir); err != nil {
		t.Fatalf("ReadVerifiedSkill with matching expected_digest failed: %v", err)
	}
	if _, err := ReadVerifiedSkill("g8s-red-prove", "sha256:0000000000000000", dir); err == nil || !strings.Contains(err.Error(), "E_SKILL_DIGEST_MISMATCH") {
		t.Fatalf("expected E_SKILL_DIGEST_MISMATCH on wrong digest, got %v", err)
	}

	// 3. F1 / F2 worker tier cannot list or read F0-only g8s-supervisor
	t.Setenv("G8S_TIER", "F1")
	listF1 := ListVerifiedSkills(dir)
	if len(listF1) != 1 || listF1[0].Name != "g8s-red-prove" {
		t.Fatalf("F1 tier must filter out F0 skills, got %+v", listF1)
	}
	if _, err := ReadVerifiedSkill("g8s-supervisor", "", dir); err == nil || !strings.Contains(err.Error(), "E_SKILL_TIER_FORBIDDEN") {
		t.Fatalf("expected E_SKILL_TIER_FORBIDDEN when F1 reads g8s-supervisor, got %v", err)
	}

	// 4. HydrateSkillsBlock formats skill content and returns manifests
	block, manifests, err := HydrateSkillsBlock([]string{"g8s-red-prove"}, 1024, dir)
	if err != nil {
		t.Fatalf("HydrateSkillsBlock failed: %v", err)
	}
	if len(manifests) != 1 || !strings.Contains(block, "HYDRATED SKILL CONTRACTS") || !strings.Contains(block, res.Digest) {
		t.Fatalf("unexpected hydrated block: %s", block)
	}
}
