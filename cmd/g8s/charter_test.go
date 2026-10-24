package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestCharterCLIAndS1SubmitFirewall(t *testing.T) {
	bin := buildG8sBinary(t)
	stateDir := t.TempDir()
	baseEnv := append(os.Environ(), "G8S_STATE_DIR="+stateDir, "G8S_TIER=", "G8S_RECEIPT_ID=")

	// 1. Issue charter via CLI
	issueCmd := exec.Command(bin, "charter", "issue", "--repo", "tamld/g8s", "--mandate", "CLI test mandate", "--path", "internal/**", "--json")
	issueCmd.Env = baseEnv
	out, err := issueCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("charter issue failed: %v\n%s", err, out)
	}
	var issueEnv struct {
		Kind string `json:"kind"`
		Data struct {
			CharterID string `json:"charter_id"`
			Epoch     int    `json:"epoch"`
			Fence     string `json:"fence"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &issueEnv); err != nil || issueEnv.Kind != "charter_issue" || issueEnv.Data.CharterID == "" {
		t.Fatalf("unexpected charter issue output: %s (err=%v)", out, err)
	}

	// 2. Verify charter via CLI
	verifyCmd := exec.Command(bin, "charter", "verify", "--id", issueEnv.Data.CharterID, "--fence", issueEnv.Data.Fence, "--json")
	verifyCmd.Env = baseEnv
	vOut, err := verifyCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("charter verify failed: %v\n%s", err, vOut)
	}
	if !strings.Contains(string(vOut), `"valid": true`) && !strings.Contains(string(vOut), `"valid":true`) {
		t.Fatalf("expected valid:true in verify output: %s", vOut)
	}

	// 3. Portfolio projection via CLI
	portCmd := exec.Command(bin, "charter", "portfolio", "--json")
	portCmd.Env = baseEnv
	pOut, err := portCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("charter portfolio failed: %v\n%s", err, pOut)
	}
	if !strings.Contains(string(pOut), issueEnv.Data.CharterID) {
		t.Fatalf("expected charter ID in portfolio projection: %s", pOut)
	}

	// 4. S1 FederationCoordinator must be blocked from calling g8s submit directly
	subCmd := exec.Command(bin, "submit", "--idempotency-key", "s1-bypass-test", "--prompt", "bypass S2", "--json")
	subCmd.Env = append(baseEnv, "G8S_STRATUM=S1")
	subOut, subErr := subCmd.CombinedOutput()
	if subErr == nil || !strings.Contains(string(subOut), "E_STRATUM_ADJACENCY_VIOLATION") {
		t.Fatalf("expected S1 direct submit to fail with E_STRATUM_ADJACENCY_VIOLATION, got err=%v\n%s", subErr, subOut)
	}
}
