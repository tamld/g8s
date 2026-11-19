package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tamld/g8s/internal/doctor"
)

func TestCLI_DoctorEmitsStructuredNonCompensatoryTrustAssuranceStatus(t *testing.T) {
	headOut, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	headCommit := strings.TrimSpace(string(headOut))

	binName := "g8s"
	if runtime.GOOS == "windows" {
		binName = "g8s.exe"
	}
	binPath := filepath.Join(t.TempDir(), binName)
	ldflags := "-X main.Version=0.25.0-e2e -X main.Commit=" + headCommit + " -X main.BuildTime=2026-11-20T00:00:00Z -X main.BuildSource=release"
	buildCmd := exec.Command("go", "build", "-ldflags", ldflags, "-o", binPath, ".")
	if out, bErr := buildCmd.CombinedOutput(); bErr != nil {
		t.Fatalf("go build cmd/g8s: %v\n%s", bErr, string(out))
	}

	stateDir := t.TempDir()
	env := append(os.Environ(),
		"G8S_STATE_DIR="+stateDir,
		"G8S_DB="+filepath.Join(stateDir, "controlplane.db"),
	)

	jsonCmd := exec.Command(binPath, "doctor", "--json")
	jsonCmd.Env = env
	jsonOut, err := jsonCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("g8s doctor --json failed: %v\nOutput: %s", err, string(jsonOut))
	}

	var parsed struct {
		OK   bool `json:"ok"`
		Data struct {
			TrustAssurance *doctor.TrustAssuranceStatus `json:"trust_assurance_status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(jsonOut, &parsed); err != nil {
		t.Fatalf("unmarshal doctor json: %v\nRaw: %s", err, string(jsonOut))
	}
	ta := parsed.Data.TrustAssurance
	if ta == nil {
		t.Fatalf("expected non-nil trust_assurance_status in doctor --json output: %s", string(jsonOut))
	}
	if ta.Schema != doctor.TrustAssuranceSchema {
		t.Fatalf("schema = %q, want %q", ta.Schema, doctor.TrustAssuranceSchema)
	}
	if ta.BinaryVersion != "0.25.0-e2e" {
		t.Fatalf("binary_version = %q, want 0.25.0-e2e", ta.BinaryVersion)
	}
	if ta.BinaryCommit != strings.ToLower(headCommit) {
		t.Fatalf("binary_commit = %q, want %q", ta.BinaryCommit, strings.ToLower(headCommit))
	}
	if ta.BinaryMatchesSource == nil || !*ta.BinaryMatchesSource {
		t.Fatalf("expected binary_matches_source=true, got %v", ta.BinaryMatchesSource)
	}
	if ta.VCSModified {
		t.Fatalf("expected vcs_modified=false on release-ldflags binary")
	}
	if len(ta.BinarySHA256) != 64 {
		t.Fatalf("binary_sha256 length = %d (%q), want 64 hex chars", len(ta.BinarySHA256), ta.BinarySHA256)
	}
	if len(ta.Gates) != 6 {
		t.Fatalf("gates count = %d, want 6", len(ta.Gates))
	}
	if ta.OverallVerdict != doctor.TrustGateUnverified || !ta.NonCompensatoryVeto {
		t.Fatalf("fast doctor mode must report non-compensatory UNVERIFIED + veto until live assurance benchmark runs, got verdict=%s veto=%t", ta.OverallVerdict, ta.NonCompensatoryVeto)
	}

	byGate := make(map[string]doctor.TrustGateAssessment, len(ta.Gates))
	for _, g := range ta.Gates {
		byGate[g.Gate] = g
	}
	for _, id := range []string{"TRUST-G2", "TRUST-G6"} {
		if byGate[id].Status != doctor.TrustGateEnforced {
			t.Fatalf("%s = %+v, want ENFORCED", id, byGate[id])
		}
	}
	for _, id := range []string{"TRUST-G3", "TRUST-G4", "TRUST-G5"} {
		if byGate[id].Status != doctor.TrustGateUnverified {
			t.Fatalf("%s = %+v, want UNVERIFIED in fast doctor mode", id, byGate[id])
		}
	}
	if runtime.GOOS == "windows" && byGate["TRUST-G1"].Status != doctor.TrustGateMediatedOnly {
		t.Fatalf("Windows TRUST-G1 = %+v, want MEDIATED_ONLY", byGate["TRUST-G1"])
	}
	if runtime.GOOS == "darwin" && byGate["TRUST-G1"].Status != doctor.TrustGateEnforced {
		t.Fatalf("macOS TRUST-G1 = %+v, want ENFORCED", byGate["TRUST-G1"])
	}

	humanCmd := exec.Command(binPath, "doctor")
	humanCmd.Env = env
	humanOutBytes, err := humanCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("g8s doctor human output failed: %v\nOutput: %s", err, string(humanOutBytes))
	}
	humanOut := string(humanOutBytes)
	for _, want := range []string{"TRUST Assurance Status (ADR-0042)", "TRUST-G1", "TRUST-G2", "TRUST-G3", "TRUST-G4", "TRUST-G5", "TRUST-G6"} {
		if !strings.Contains(humanOut, want) {
			t.Fatalf("human doctor output missing %q\nOutput:\n%s", want, humanOut)
		}
	}
}
