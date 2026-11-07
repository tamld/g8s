package doctor

import (
	"fmt"
	"os/exec"
	"runtime/debug"
	"strings"
)

// Stage-3 stale-binary guard: the 2026-11-07 campaign had a local gate pass
// against a pre-merge ./bin/g8s. The running binary reports its build commit
// (debug.ReadBuildInfo vcs.revision); doctor compares it with the checkout
// HEAD and warns loudly on drift or on an unattestable binary inside a repo.

// normalizeRevision strips build-metadata noise so two VCS revisions compare
// by content: case, whitespace, and the -dirty suffix the builder appends.
func normalizeRevision(rev string) string {
	return strings.ToLower(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rev), "-dirty")))
}

// binaryRevisionsMatch reports whether the binary revision and the HEAD
// revision refer to the same commit (shared prefix of at least 7 hex chars,
// either side possibly carrying -dirty).
func binaryRevisionsMatch(binRev, headRev string) bool {
	binRev, headRev = normalizeRevision(binRev), normalizeRevision(headRev)
	if len(binRev) < 7 || len(headRev) < 7 {
		return false
	}
	n := len(binRev)
	if len(headRev) < n {
		n = len(headRev)
	}
	if n > 40 {
		n = 40
	}
	return binRev[:n] == headRev[:n]
}

// checkBinaryVsHeadFrom is the pure comparison core, injectable for tests.
func checkBinaryVsHeadFrom(binRev, headRev string) DiagnosticResult {
	if headRev == "" {
		return DiagnosticResult{
			Name:    "Binary vs HEAD Check",
			Status:  "OK",
			Message: "not a git checkout (HEAD unavailable) — staleness unattestable here",
		}
	}
	if binRev == "" {
		return DiagnosticResult{
			Name:    "Binary vs HEAD Check",
			Status:  "WARN",
			Message: "running binary carries no VCS revision (go run, or built outside a repo) — staleness unattestable; rebuild with make local",
			Details: fmt.Sprintf("head=%s", normalizeRevision(headRev)),
		}
	}
	if binaryRevisionsMatch(binRev, headRev) {
		return DiagnosticResult{
			Name:    "Binary vs HEAD Check",
			Status:  "OK",
			Message: fmt.Sprintf("binary revision matches checkout HEAD (%s)", normalizeRevision(binRev)),
		}
	}
	return DiagnosticResult{
		Name:    "Binary vs HEAD Check",
		Status:  "WARN",
		Message: "stale binary: running build predates or diverges from checkout HEAD — rebuild with make local before trusting local gates (the stale binary fooled a local gate once)",
		Details: fmt.Sprintf("binary=%s head=%s", normalizeRevision(binRev), normalizeRevision(headRev)),
	}
}

func checkBinaryVsHead() DiagnosticResult {
	binRev := ""
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				binRev = setting.Value
				break
			}
		}
	}
	headRev, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return checkBinaryVsHeadFrom(binRev, "")
	}
	return checkBinaryVsHeadFrom(binRev, strings.TrimSpace(string(headRev)))
}
