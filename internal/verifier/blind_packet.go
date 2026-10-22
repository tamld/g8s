package verifier

import (
	"crypto/sha256"
	"encoding/hex"
	"os/exec"
	"regexp"
	"strings"
)

// FalsificationFinding captures an adversarial counterexample finding against a spec contract.
type FalsificationFinding struct {
	ClaimID             string `json:"claim_id"`
	FileLine            string `json:"file_line"`
	CounterexampleInput string `json:"counterexample_input"`
	Severity            string `json:"severity"`
	Verdict             string `json:"verdict"`
}

// BlindReviewPacket bundles verified diffs, specs, and test output while stripping
// author identities and self-reported worker prose to prevent reviewer sycophancy.
type BlindReviewPacket struct {
	PacketID             string        `json:"packet_id"`
	FeatureID            string        `json:"feature_id,omitempty"`
	SpecContract         string        `json:"spec_contract"`
	Invariants           []string      `json:"invariants"`
	RawGitDiff           string        `json:"raw_git_diff"`
	MechanicalTestOutput string        `json:"mechanical_test_output"`
	ChecksPassed         bool          `json:"checks_passed"`
	HoldoutPassed        *bool         `json:"holdout_passed,omitempty"`
	CheckResults         []CheckResult `json:"check_results,omitempty"`
	RedactedChannels     []string      `json:"redacted_channels"`
}

var (
	headerTrailerRegex = regexp.MustCompile(`(?i)^(\+|-)?\s*(Author|Commit|Date|Signed-off-by|Co-authored-by|Reviewed-by):`)
	redactionMarkers   = regexp.MustCompile(`(?i)(worker\.Summary|worker\.ReportedDoD|ReportedDoD|claude-[a-z0-9_.-]+|gpt-[a-z0-9_.-]+|gemini-[a-z0-9_.-]+|deepseek-[a-z0-9_.-]+|9router)`)
)

// SanitizeDiffForBlindReview scrubs metadata headers, commit prose, and identity/persuasion
// markers from a raw unified git diff to produce a neutral review diff.
func SanitizeDiffForBlindReview(rawDiff string) string {
	if strings.TrimSpace(rawDiff) == "" {
		return ""
	}

	lines := strings.Split(rawDiff, "\n")

	// 1. Strip any lines before the first "diff --git ", "--- ", or "@@ " header.
	firstHeaderIdx := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "diff --git ") ||
			strings.HasPrefix(trimmed, "--- ") ||
			strings.HasPrefix(trimmed, "@@ ") {
			firstHeaderIdx = i
			break
		}
	}

	if firstHeaderIdx == -1 {
		return ""
	}

	diffLines := lines[firstHeaderIdx:]
	sanitizedLines := make([]string, 0, len(diffLines))

	for _, line := range diffLines {
		// 2. Remove any lines matching author / commit / trailer metadata.
		if headerTrailerRegex.MatchString(line) {
			continue
		}

		// 3. Redact known persuasion and identity markers.
		redactedLine := redactionMarkers.ReplaceAllString(line, "[REDACTED]")
		sanitizedLines = append(sanitizedLines, redactedLine)
	}

	return strings.Join(sanitizedLines, "\n")
}

// BuildBlindReviewPacket constructs a double-blind review packet for independent verification.
// It ensures that model identities, author names, commit messages, and worker self-reports
// are stripped, leaving only mechanical evidence and formal spec invariants.
func BuildBlindReviewPacket(
	target TaskRef,
	repoRoot string,
	rawGitDiff string,
	mechanicalTestOutput string,
	checksPassed bool,
	holdoutPassed *bool,
	checkResults []CheckResult,
) BlindReviewPacket {
	if rawGitDiff == "" && repoRoot != "" {
		cmd := exec.Command("git", "-C", repoRoot, "diff", "HEAD")
		if out, err := cmd.Output(); err == nil && len(strings.TrimSpace(string(out))) > 0 {
			rawGitDiff = string(out)
		} else {
			cmdShow := exec.Command("git", "-C", repoRoot, "show", "--no-notes", "--format=", "HEAD")
			if outShow, errShow := cmdShow.Output(); errShow == nil {
				rawGitDiff = string(outShow)
			}
		}
	}

	sanitizedDiff := SanitizeDiffForBlindReview(rawGitDiff)

	h := sha256.New()
	h.Write([]byte(target.FeatureID + target.SpecContract + sanitizedDiff))
	packetID := "brp-" + hex.EncodeToString(h.Sum(nil))[:12]

	invariants := target.Invariants
	if invariants == nil {
		invariants = []string{}
	}

	results := checkResults
	if results == nil {
		results = []CheckResult{}
	}

	redactedChannels := []string{
		"worker.Summary",
		"worker.ReportedDoD",
		"model_identity",
		"provider_identity",
		"git_author_commit_prose",
		"f0_jev_commentary",
	}

	return BlindReviewPacket{
		PacketID:             packetID,
		FeatureID:            target.FeatureID,
		SpecContract:         target.SpecContract,
		Invariants:           invariants,
		RawGitDiff:           sanitizedDiff,
		MechanicalTestOutput: mechanicalTestOutput,
		ChecksPassed:         checksPassed,
		HoldoutPassed:        holdoutPassed,
		CheckResults:         results,
		RedactedChannels:     redactedChannels,
	}
}
