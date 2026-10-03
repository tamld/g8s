package tools_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type mergerSandbox struct {
	t          *testing.T
	tmpDir     string
	mockDir    string
	mockBin    string
	fixtureDir string
	mergerBin  string
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	dir := cwd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("could not find repo root containing go.mod")
	return ""
}

func runCmd(t *testing.T, dir string, env []string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if len(env) > 0 {
		cmd.Env = env
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command %s %v in %s failed: %v\noutput: %s", name, args, dir, err, string(out))
	}
	return strings.TrimSpace(string(out))
}

func setupMergerSandbox(t *testing.T) *mergerSandbox {
	t.Helper()

	root := findRepoRoot(t)
	mergerSrc := filepath.Join(root, "tools", "merger.sh")
	detectSrc := filepath.Join(root, "tools", "ci_lane_detect.sh")

	if _, err := os.Stat(mergerSrc); err != nil {
		t.Fatalf("merger.sh not found at %s", mergerSrc)
	}
	if _, err := os.Stat(detectSrc); err != nil {
		t.Fatalf("ci_lane_detect.sh not found at %s", detectSrc)
	}

	tmpDir := t.TempDir()
	mockDir := filepath.Join(tmpDir, "mock")
	if err := os.MkdirAll(mockDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mockBin := filepath.Join(tmpDir, "bin")
	if err := os.MkdirAll(mockBin, 0o755); err != nil {
		t.Fatal(err)
	}

	// 1. Mock g8s binary (bash 3.2 compatible)
	mockG8s := filepath.Join(mockBin, "g8s")
	g8sScript := `#!/usr/bin/env bash
set -euo pipefail
MOCK_DIR="${MOCK_DIR:-/tmp}"

cmd="${1:-}"
shift || true

# D3 boundary audit: log any attempt to mutate receipt or task state
case "$cmd" in
    receipt|receipts)
        echo "g8s $cmd $*" >> "${MOCK_DIR}/illegal_d3_calls.log"
        exit 1
        ;;
    task|tasks)
        sub="${1:-}"
        if [ "$sub" = "submit" ] || [ "$sub" = "resubmit" ]; then
            echo "g8s $cmd $*" >> "${MOCK_DIR}/illegal_d3_calls.log"
            exit 1
        fi
        ;;
esac

case "$cmd" in
    config)
        sub="${1:-}"
        shift || true
        key="${1:-}"
        if [ "$sub" = "get" ] && [ "$key" = "autonomy_level" ]; then
            if [ -f "${MOCK_DIR}/autonomy_level" ]; then
                cat "${MOCK_DIR}/autonomy_level"
            else
                echo "1"
            fi
            exit 0
        fi
        echo "mock g8s: unknown config subcommand $sub" >&2
        exit 1
        ;;
    verify)
        task_id=""
        while [ $# -gt 0 ]; do
            case "$1" in
                --task)
                    task_id="$2"
                    shift 2
                    ;;
                *)
                    shift
                    ;;
            esac
        done

        if [ -f "${MOCK_DIR}/verifier_envelope.json" ]; then
            cat "${MOCK_DIR}/verifier_envelope.json"
            exit 0
        fi

        # Default all-green docs verifier envelope
        cat <<JSON
{
  "v": 1,
  "kind": "verdict",
  "cmd": "verify",
  "sub": "${task_id}",
  "data": {
    "class": "docs",
    "registered": true,
    "status": "advisory",
    "outcome": "pass",
    "checks": [],
    "scope": true,
    "recorded_at": "2026-10-03T12:00:00Z"
  },
  "at": "2026-10-03T12:00:00Z"
}
JSON
        exit 0
        ;;
    *)
        echo "mock g8s: unknown command $cmd" >&2
        exit 1
        ;;
esac
`
	if err := os.WriteFile(mockG8s, []byte(g8sScript), 0o755); err != nil {
		t.Fatal(err)
	}

	// 2. Mock gh binary (bash 3.2 compatible)
	mockGh := filepath.Join(mockBin, "gh")
	ghScript := `#!/usr/bin/env bash
set -euo pipefail
MOCK_DIR="${MOCK_DIR:-/tmp}"

cmd="${1:-}"
shift || true

case "$cmd" in
    pr)
        sub="${1:-}"
        shift || true
        case "$sub" in
            checks)
                pr_num="${1:-}"
                if [ -f "${MOCK_DIR}/gh_checks" ]; then
                    cat "${MOCK_DIR}/gh_checks"
                else
                    echo "docs-battery pass 10s https://github.com/tamld/g8s/actions/runs/1"
                fi
                exit 0
                ;;
            merge)
                pr_num="${1:-}"
                echo "$*" >> "${MOCK_DIR}/merge_calls.log"
                echo "Squashed and merged PR #${pr_num}"
                exit 0
                ;;
            view)
                pr_num="${1:-}"
                if [ -f "${MOCK_DIR}/pr_commit" ]; then
                    commit=$(cat "${MOCK_DIR}/pr_commit")
                    echo "{\"headRefOid\":\"${commit}\"}"
                else
                    echo "{}"
                fi
                exit 0
                ;;
            *)
                echo "mock gh pr: unknown subcommand $sub" >&2
                exit 1
                ;;
        esac
        ;;
    *)
        echo "mock gh: unknown command $cmd" >&2
        exit 1
        ;;
esac
`
	if err := os.WriteFile(mockGh, []byte(ghScript), 0o755); err != nil {
		t.Fatal(err)
	}

	// 3. Fixture Git repository
	fixtureDir := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(fixtureDir, 0o755); err != nil {
		t.Fatal(err)
	}

	gitEnv := append(os.Environ(),
		"GIT_AUTHOR_NAME=CI Tester",
		"GIT_AUTHOR_EMAIL=ci-test@example.com",
		"GIT_COMMITTER_NAME=CI Tester",
		"GIT_COMMITTER_EMAIL=ci-test@example.com",
	)

	runCmd(t, fixtureDir, gitEnv, "git", "init", "-q")
	runCmd(t, fixtureDir, gitEnv, "git", "config", "user.email", "ci-test@example.com")
	runCmd(t, fixtureDir, gitEnv, "git", "config", "user.name", "CI Tester")
	runCmd(t, fixtureDir, gitEnv, "git", "checkout", "-q", "-b", "main")

	if err := os.WriteFile(filepath.Join(fixtureDir, "README.md"), []byte("# Initial fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	toolsDir := filepath.Join(fixtureDir, "tools")
	if err := os.MkdirAll(toolsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(fixtureDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Copy scripts
	copyFile(t, detectSrc, filepath.Join(toolsDir, "ci_lane_detect.sh"), 0o755)
	copyFile(t, mergerSrc, filepath.Join(toolsDir, "merger.sh"), 0o755)
	copyFile(t, mockG8s, filepath.Join(binDir, "g8s"), 0o755)

	runCmd(t, fixtureDir, gitEnv, "git", "add", ".")
	runCmd(t, fixtureDir, gitEnv, "git", "commit", "-q", "-m", "initial commit with tools")
	runCmd(t, fixtureDir, gitEnv, "git", "update-ref", "refs/remotes/origin/main", "HEAD")

	// PR 1: Docs only
	runCmd(t, fixtureDir, gitEnv, "git", "checkout", "-q", "-b", "branch-docs", "main")
	if err := os.WriteFile(filepath.Join(fixtureDir, "README.md"), []byte("# Updated Docs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runCmd(t, fixtureDir, gitEnv, "git", "commit", "-q", "-am", "docs update")
	runCmd(t, fixtureDir, gitEnv, "git", "update-ref", "refs/remotes/origin/pr/1", "HEAD")

	// PR 2: Code change (build lane)
	runCmd(t, fixtureDir, gitEnv, "git", "checkout", "-q", "-b", "branch-code", "main")
	if err := os.WriteFile(filepath.Join(fixtureDir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runCmd(t, fixtureDir, gitEnv, "git", "add", "main.go")
	runCmd(t, fixtureDir, gitEnv, "git", "commit", "-q", "-m", "code change")
	runCmd(t, fixtureDir, gitEnv, "git", "update-ref", "refs/remotes/origin/pr/2", "HEAD")

	// Return to main
	runCmd(t, fixtureDir, gitEnv, "git", "checkout", "-q", "main")

	return &mergerSandbox{
		t:          t,
		tmpDir:     tmpDir,
		mockDir:    mockDir,
		mockBin:    mockBin,
		fixtureDir: fixtureDir,
		mergerBin:  filepath.Join(toolsDir, "merger.sh"),
	}
}

func copyFile(t *testing.T, src, dst string, mode os.FileMode) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("failed to read %s: %v", src, err)
	}
	if err := os.WriteFile(dst, data, mode); err != nil {
		t.Fatalf("failed to write %s: %v", dst, err)
	}
}

func (s *mergerSandbox) runMerger(args ...string) (string, int) {
	s.t.Helper()
	cmdArgs := append([]string{s.mergerBin}, args...)
	cmd := exec.Command("bash", cmdArgs...)
	cmd.Dir = s.fixtureDir
	cmd.Env = append(os.Environ(),
		"PATH="+s.mockBin+":"+os.Getenv("PATH"),
		"MOCK_DIR="+s.mockDir,
		"GIT_AUTHOR_NAME=CI Tester",
		"GIT_AUTHOR_EMAIL=ci-test@example.com",
		"GIT_COMMITTER_NAME=CI Tester",
		"GIT_COMMITTER_EMAIL=ci-test@example.com",
	)
	out, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}
	return strings.TrimSpace(string(out)), exitCode
}

func (s *mergerSandbox) hasMergeCalls() bool {
	s.t.Helper()
	p := filepath.Join(s.mockDir, "merge_calls.log")
	info, err := os.Stat(p)
	if err != nil {
		return false
	}
	return info.Size() > 0
}

func (s *mergerSandbox) hasIllegalD3Calls() bool {
	s.t.Helper()
	p := filepath.Join(s.mockDir, "illegal_d3_calls.log")
	info, err := os.Stat(p)
	if err != nil {
		return false
	}
	return info.Size() > 0
}

// =============================================================================
// Guarantee 5: Merger gates resist crafted PR states.
// Against the PATH-SHIM sandbox (never network):
// autonomy_level read returning "1 " (trailing space), "01", "true", an empty
// string; lane detector emitting "lane=docs" with extra whitespace/suffix lines;
// verifier envelope with outcome "pass" but registered=false; CI checks with
// pass=15 pending=1; a gate order swap attempt (verifier output arriving
// before autonomy is checked). Verify: refuse in every case, the four GATE lines
// still print in order, and NO merge command is invoked — including when --dry-run
// is passed with all gates green.
// =============================================================================

// BUG(REDTEST): merger.sh accepts autonomy_level="1 " with trailing space instead of
// refusing, because line 119 performs `autonomy_val=$(echo "$autonomy_val" | tr -d '[:space:]')`
// which strips all whitespace and turns "1 " into "1".
// Repro:
// 1. Write "1 " to autonomy_level mock
// 2. Run tools/merger.sh --pr 1 --task task-001 --dry-run
// 3. Observed: GATE autonomy: pass — autonomy_level=1 (operator flip recorded)
// 4. Expected per Brief Guarantee 5: refuse on autonomy_level read returning "1 " (trailing space).
func TestRedtest_Guarantee5_AutonomyLevel_TrailingSpace(t *testing.T) {
	s := setupMergerSandbox(t)
	if err := os.WriteFile(filepath.Join(s.mockDir, "autonomy_level"), []byte("1 "), 0o644); err != nil {
		t.Fatal(err)
	}

	out, code := s.runMerger("--pr", "1", "--task", "task-001")
	if code != 3 {
		t.Fatalf("expected exit code 3 for trailing space '1 ', got %d\noutput: %s", code, out)
	}
	if !strings.Contains(out, "GATE autonomy: fail") {
		t.Fatalf("expected GATE autonomy: fail, got:\n%s", out)
	}
	if s.hasMergeCalls() {
		t.Fatal("merge command was invoked despite refusal")
	}
}

func TestRedtest_Guarantee5_AutonomyLevel_CraftedRefusals(t *testing.T) {
	craftedValues := []struct {
		name string
		val  string
	}{
		{name: "leading zero 01", val: "01"},
		{name: "boolean true", val: "true"},
		{name: "empty string", val: ""},
		{name: "negative one", val: "-1"},
		{name: "level 2", val: "2"},
		{name: "level 0", val: "0"},
		{name: "word one", val: "one"},
	}

	for _, tc := range craftedValues {
		t.Run(tc.name, func(t *testing.T) {
			s := setupMergerSandbox(t)
			if err := os.WriteFile(filepath.Join(s.mockDir, "autonomy_level"), []byte(tc.val), 0o644); err != nil {
				t.Fatal(err)
			}

			out, code := s.runMerger("--pr", "1", "--task", "task-001")
			if code != 3 {
				t.Fatalf("value %q: expected exit code 3, got %d\noutput: %s", tc.val, code, out)
			}
			if !strings.Contains(out, "GATE autonomy: fail") {
				t.Fatalf("value %q: expected GATE autonomy: fail, got:\n%s", tc.val, out)
			}
			if strings.Contains(out, "GATE lane:") || strings.Contains(out, "GATE verifier:") || strings.Contains(out, "GATE ci:") {
				t.Fatalf("value %q: executed subsequent gates after autonomy refusal:\n%s", tc.val, out)
			}
			if s.hasMergeCalls() {
				t.Fatalf("value %q: merge command was invoked despite refusal", tc.val)
			}
		})
	}
}

func TestRedtest_Guarantee5_LaneDetector_ExtraWhitespaceAndSuffix(t *testing.T) {
	cases := []struct {
		name   string
		output string
	}{
		{
			name:   "lane=docs with trailing space",
			output: "lane=docs ",
		},
		{
			name:   "lane=docs with secondary warning lines",
			output: "lane=docs\nwarning: secondary output line\ninfo: another line",
		},
		{
			name:   "lane=docs with extra tabs and spaces",
			output: "lane=docs\t ",
		},
		{
			name:   "lane=docs with prefix",
			output: "info: starting\nlane=docs",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := setupMergerSandbox(t)
			// Ensure autonomy passes
			if err := os.WriteFile(filepath.Join(s.mockDir, "autonomy_level"), []byte("1"), 0o644); err != nil {
				t.Fatal(err)
			}

			// Override ci_lane_detect.sh to emit crafted output
			customDetect := filepath.Join(s.fixtureDir, "tools", "ci_lane_detect.sh")
			script := fmt.Sprintf("#!/usr/bin/env bash\ncat <<'EOF'\n%s\nEOF\nexit 0\n", tc.output)
			if err := os.WriteFile(customDetect, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}

			out, code := s.runMerger("--pr", "1", "--task", "task-001")
			if code != 1 {
				t.Fatalf("expected exit code 1 on crafted lane detector output, got %d\noutput: %s", code, out)
			}
			if !strings.Contains(out, "GATE lane: fail") {
				t.Fatalf("expected GATE lane: fail, got:\n%s", out)
			}
			if strings.Contains(out, "GATE verifier:") || strings.Contains(out, "GATE ci:") {
				t.Fatalf("executed verifier or ci gate after lane gate failure:\n%s", out)
			}
			if s.hasMergeCalls() {
				t.Fatal("merge command was invoked despite lane failure")
			}
		})
	}
}

func TestRedtest_Guarantee5_VerifierEnvelope_OutcomePassButUnregistered(t *testing.T) {
	s := setupMergerSandbox(t)

	// Autonomy = 1
	if err := os.WriteFile(filepath.Join(s.mockDir, "autonomy_level"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Hostile verifier envelope: outcome pass, but registered=false
	envelope := `{
  "v": 1,
  "kind": "verdict",
  "cmd": "verify",
  "sub": "task-unreg-pass",
  "data": {
    "class": "docs",
    "registered": false,
    "status": "advisory",
    "outcome": "pass",
    "checks": [],
    "scope": true,
    "recorded_at": "2026-10-03T12:00:00Z"
  },
  "at": "2026-10-03T12:00:00Z"
}`
	if err := os.WriteFile(filepath.Join(s.mockDir, "verifier_envelope.json"), []byte(envelope), 0o644); err != nil {
		t.Fatal(err)
	}

	out, code := s.runMerger("--pr", "1", "--task", "task-unreg-pass")
	if code != 1 {
		t.Fatalf("expected exit code 1 for registered=false, got %d\noutput: %s", code, out)
	}
	if !strings.Contains(out, "GATE verifier: fail — unregistered task class") {
		t.Fatalf("expected verifier gate refusal for registered=false, got:\n%s", out)
	}
	if strings.Contains(out, "GATE ci:") {
		t.Fatalf("executed CI gate despite verifier failure:\n%s", out)
	}
	if s.hasMergeCalls() {
		t.Fatal("merge command was invoked despite verifier failure")
	}
}

func TestRedtest_Guarantee5_CIChecks_Pass15Pending1(t *testing.T) {
	s := setupMergerSandbox(t)

	// Autonomy = 1
	if err := os.WriteFile(filepath.Join(s.mockDir, "autonomy_level"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 15 passing checks + 1 pending check
	var buf bytes.Buffer
	for i := 1; i <= 15; i++ {
		fmt.Fprintf(&buf, "check-%02d pass 10s https://github.com/tamld/g8s/actions/runs/%d\n", i, i)
	}
	buf.WriteString("check-16 pending 12s https://github.com/tamld/g8s/actions/runs/16\n")

	if err := os.WriteFile(filepath.Join(s.mockDir, "gh_checks"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	out, code := s.runMerger("--pr", "1", "--task", "task-001")
	if code != 1 {
		t.Fatalf("expected exit code 1 for pending CI check, got %d\noutput: %s", code, out)
	}
	if !strings.Contains(out, "GATE ci: fail — checks pending (pending=1, failure=0)") {
		t.Fatalf("expected CI pending failure, got:\n%s", out)
	}
	if s.hasMergeCalls() {
		t.Fatal("merge command was invoked despite CI pending check")
	}
}

func TestRedtest_Guarantee5_GateOrderStrictness(t *testing.T) {
	s := setupMergerSandbox(t)

	// Case 1: Autonomy fails -> exactly 1 gate line
	if err := os.WriteFile(filepath.Join(s.mockDir, "autonomy_level"), []byte("0"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := s.runMerger("--pr", "1", "--task", "task-001")
	if code != 3 {
		t.Fatalf("expected exit code 3, got %d", code)
	}
	lines := strings.Split(out, "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "GATE autonomy: fail") {
		t.Fatalf("expected only GATE autonomy line on autonomy failure, got: %s", out)
	}

	// Case 2: Lane fails (PR 2 is code) -> exactly 2 gate lines
	if err := os.WriteFile(filepath.Join(s.mockDir, "autonomy_level"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code = s.runMerger("--pr", "2", "--task", "task-002")
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	lines = strings.Split(out, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected exactly 2 lines on lane failure, got: %v", lines)
	}
	if !strings.HasPrefix(lines[0], "GATE autonomy: pass") {
		t.Errorf("line 0 must be GATE autonomy: pass, got: %s", lines[0])
	}
	if !strings.HasPrefix(lines[1], "GATE lane: fail") {
		t.Errorf("line 1 must be GATE lane: fail, got: %s", lines[1])
	}

	// Case 3: Verifier fails -> exactly 3 gate lines
	badEnvelope := `{
  "v": 1,
  "kind": "verdict",
  "cmd": "verify",
  "sub": "task-001",
  "data": {
    "class": "docs",
    "registered": true,
    "status": "advisory",
    "outcome": "fail",
    "checks": [],
    "scope": true,
    "recorded_at": "2026-10-03T12:00:00Z"
  },
  "at": "2026-10-03T12:00:00Z"
}`
	if err := os.WriteFile(filepath.Join(s.mockDir, "verifier_envelope.json"), []byte(badEnvelope), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code = s.runMerger("--pr", "1", "--task", "task-001")
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	lines = strings.Split(out, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected exactly 3 lines on verifier failure, got: %v", lines)
	}
	if !strings.HasPrefix(lines[0], "GATE autonomy: pass") {
		t.Errorf("line 0 must be GATE autonomy: pass, got: %s", lines[0])
	}
	if !strings.HasPrefix(lines[1], "GATE lane: pass") {
		t.Errorf("line 1 must be GATE lane: pass, got: %s", lines[1])
	}
	if !strings.HasPrefix(lines[2], "GATE verifier: fail") {
		t.Errorf("line 2 must be GATE verifier: fail, got: %s", lines[2])
	}

	// Case 4: All gates pass
	_ = os.Remove(filepath.Join(s.mockDir, "verifier_envelope.json"))
	out, code = s.runMerger("--pr", "1", "--task", "task-001", "--dry-run")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d\noutput: %s", code, out)
	}
	lines = strings.Split(out, "\n")
	if len(lines) < 5 {
		t.Fatalf("expected at least 5 lines (4 gates + DRY-RUN), got: %v", lines)
	}
	if !strings.HasPrefix(lines[0], "GATE autonomy: pass") {
		t.Errorf("line 0 must be GATE autonomy: pass, got: %s", lines[0])
	}
	if !strings.HasPrefix(lines[1], "GATE lane: pass") {
		t.Errorf("line 1 must be GATE lane: pass, got: %s", lines[1])
	}
	if !strings.HasPrefix(lines[2], "GATE verifier: pass") {
		t.Errorf("line 2 must be GATE verifier: pass, got: %s", lines[2])
	}
	if !strings.HasPrefix(lines[3], "GATE ci: pass") {
		t.Errorf("line 3 must be GATE ci: pass, got: %s", lines[3])
	}
}

func TestRedtest_Guarantee5_DryRunNeverMergesAllGatesGreen(t *testing.T) {
	s := setupMergerSandbox(t)

	// All gates green
	if err := os.WriteFile(filepath.Join(s.mockDir, "autonomy_level"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, code := s.runMerger("--pr", "1", "--task", "task-001", "--dry-run")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d\noutput: %s", code, out)
	}

	if !strings.Contains(out, "GATE autonomy: pass") {
		t.Errorf("missing GATE autonomy: pass")
	}
	if !strings.Contains(out, "GATE lane: pass") {
		t.Errorf("missing GATE lane: pass")
	}
	if !strings.Contains(out, "GATE verifier: pass") {
		t.Errorf("missing GATE verifier: pass")
	}
	if !strings.Contains(out, "GATE ci: pass") {
		t.Errorf("missing GATE ci: pass")
	}
	if !strings.Contains(out, "DRY-RUN: would execute: gh pr merge 1 --squash --delete-branch") {
		t.Errorf("missing DRY-RUN message, got:\n%s", out)
	}

	if s.hasMergeCalls() {
		t.Fatal("merge command was invoked in --dry-run mode")
	}
}

// =============================================================================
// Guarantee 6: Merger D3 boundary under stress.
// Grep-verify the script never invokes receipt issue / submit / resubmit
// even in the mock environment where such commands exist on PATH
// (the shim logs invocations — assert zero).
// =============================================================================

func TestRedtest_Guarantee6_GrepVerifyD3Boundary(t *testing.T) {
	root := findRepoRoot(t)
	mergerSrc := filepath.Join(root, "tools", "merger.sh")
	data, err := os.ReadFile(mergerSrc)
	if err != nil {
		t.Fatalf("failed to read %s: %v", mergerSrc, err)
	}

	content := string(data)

	// Disallowed commands / mutation invocations
	disallowed := []string{
		"receipt issue",
		"receipt submit",
		"receipt resubmit",
		"receipts issue",
		"task submit",
		"tasks submit",
		"task resubmit",
		"tasks resubmit",
	}

	for _, pattern := range disallowed {
		if strings.Contains(content, pattern) {
			t.Fatalf("tools/merger.sh must not contain %q (violates D3 boundary)", pattern)
		}
	}
}

func TestRedtest_Guarantee6_PathShimAssertZeroD3Invocations(t *testing.T) {
	s := setupMergerSandbox(t)

	// Ensure autonomy = 1
	if err := os.WriteFile(filepath.Join(s.mockDir, "autonomy_level"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Run all scenarios:
	// 1. Dry run
	s.runMerger("--pr", "1", "--task", "task-001", "--dry-run")
	// 2. Real merge
	s.runMerger("--pr", "1", "--task", "task-001")
	// 3. Lane failure
	s.runMerger("--pr", "2", "--task", "task-002")

	// Assert zero illegal D3 calls
	if s.hasIllegalD3Calls() {
		data, _ := os.ReadFile(filepath.Join(s.mockDir, "illegal_d3_calls.log"))
		t.Fatalf("detected illegal D3 boundary calls: %s", string(data))
	}
}
