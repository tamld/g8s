package verifier

// Pure-Go Net-Delta Diff Guard (issue #595, Hypothesis H2).
// Evaluates strictly the added lines (+) in a git unified diff across a task's
// AllowedPaths at the handoff boundary (g8s verify).

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tamld/g8s/internal/vault"
)

// CheckTypeDiffGuard evaluates the git Net-Delta (After - Before) across AllowedPaths.
const CheckTypeDiffGuard CheckType = "diff-guard"

// AddedLine represents a single added line (+) extracted from a unified diff.
type AddedLine struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

// DiffAdvisory represents a finding produced by the Net-Delta Diff Guard (#595).
type DiffAdvisory struct {
	RuleID            string `json:"rule_id"`
	File              string `json:"file"`
	Line              int    `json:"line"`
	Severity          string `json:"severity"` // "hard" or "advisory"
	Message           string `json:"message"`
	DormantReawakened bool   `json:"dormant_reawakened,omitempty"`
}

var (
	hunkHeaderRegex = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

	// Hard Invariant patterns: high-confidence secret literals in added lines.
	hardSecretPatterns = []struct {
		id  string
		re  *regexp.Regexp
		msg string
	}{
		{
			id:  "HARD-SECRET-OPENAI",
			re:  regexp.MustCompile(`sk-[A-Za-z0-9]{20,}`),
			msg: "hardcoded API key literal (sk-...) in added line",
		},
		{
			id:  "HARD-SECRET-GITHUB",
			re:  regexp.MustCompile(`ghp_[A-Za-z0-9]{36}`),
			msg: "hardcoded GitHub token literal (ghp_...) in added line",
		},
		{
			id:  "HARD-SECRET-AWS",
			re:  regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
			msg: "hardcoded AWS access key literal (AKIA...) in added line",
		},
		{
			id:  "HARD-SECRET-PRIVKEY",
			re:  regexp.MustCompile(`-----BEGIN (?:RSA |OPENSSH |EC )?PRIVATE KEY-----`),
			msg: "hardcoded private key header in added line",
		},
	}

	// Tier-0 Advisory patterns: silent error swallowing, hollow placeholders, and trivial assertions (#644).
	tier0EmptyErrIfRegex         = regexp.MustCompile(`^\s*if\s+err\s*!=\s*nil\s*\{\s*\}\s*$`)
	tier0TheaterPlaceholderRegex = regexp.MustCompile(`(?i)(?:\bFILL IN HERE\b|<Empty>|\[Insert Here\]|panic\("(?:todo|not implemented))`)
	tier0TrivialAssertRegex      = regexp.MustCompile(`(?:assert\.True\s*\(\s*t\s*,\s*true\s*\)|require\.True\s*\(\s*t\s*,\s*true\s*\)|assert\.Nil\s*\(\s*t\s*,\s*nil\s*\)|if\s+true\s*==\s*true\b)`)
)

// ParseUnifiedDiffAddedLines extracts only added lines (+) and their 1-indexed
// destination line numbers per file from a unified git diff.
func ParseUnifiedDiffAddedLines(diffText string) map[string][]AddedLine {
	result := make(map[string][]AddedLine)
	scanner := bufio.NewScanner(strings.NewReader(diffText))

	var currentFile string
	currentLine := 0

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "+++ ") {
			rawPath := strings.TrimSpace(strings.TrimPrefix(line, "+++ "))
			if rawPath == "/dev/null" {
				currentFile = ""
				continue
			}
			rawPath = strings.TrimPrefix(rawPath, "b/")
			currentFile = filepath.ToSlash(filepath.Clean(rawPath))
			currentLine = 0
			continue
		}
		if strings.HasPrefix(line, "--- ") {
			continue
		}
		if m := hunkHeaderRegex.FindStringSubmatch(line); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil {
				currentLine = n
			}
			continue
		}
		if currentFile == "" || currentLine == 0 {
			continue
		}
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			result[currentFile] = append(result[currentFile], AddedLine{
				File: currentFile,
				Line: currentLine,
				Text: line[1:],
			})
			currentLine++
		} else if strings.HasPrefix(line, " ") {
			currentLine++
		}
	}
	return result
}

// EvaluateNetDelta inspects added lines in diffText across allowedPaths against
// Hard Invariants (secrets) and Advisory Signals (Vault SpatialAnchor.DeniedFragments
// including dormant/Cold rules for Hypothesis H2, plus Tier-0 Go error swallowing).
func EvaluateNetDelta(ctx context.Context, diffText string, allowedPaths []string, v *vault.Vault) (hardViolations []DiffAdvisory, advisories []DiffAdvisory) {
	addedByFile := ParseUnifiedDiffAddedLines(diffText)
	if len(addedByFile) == 0 {
		return nil, nil
	}

	for file, lines := range addedByFile {
		if len(allowedPaths) > 0 && !matchesAnyAllowedPath(file, allowedPaths) {
			continue
		}

		var spatialRecords []vault.DistillationRecord
		if v != nil {
			recs, err := v.MatchSpatialAllStates(ctx, file)
			if err == nil {
				spatialRecords = recs
			}
		}

		isProdGo := strings.HasSuffix(file, ".go") && !strings.HasSuffix(file, "_test.go")
		isTestGo := strings.HasSuffix(file, "_test.go")

		for _, added := range lines {
			trimmed := strings.TrimSpace(added.Text)
			if trimmed == "" || strings.HasPrefix(trimmed, "//") {
				continue
			}

			// 1. Check Hard Invariants (non-overridable)
			for _, hp := range hardSecretPatterns {
				if hp.re.MatchString(added.Text) {
					hardViolations = append(hardViolations, DiffAdvisory{
						RuleID:   hp.id,
						File:     file,
						Line:     added.Line,
						Severity: "hard",
						Message:  hp.msg,
					})
				}
			}

			// 2. Check Vault SpatialAnchor.DeniedFragments (both Hot and Cold/Dormant rules)
			for _, rec := range spatialRecords {
				isCold := rec.Status == "DEPRECATED" || rec.Status == "DORMANT" || rec.Status == "COLD"
				for _, frag := range rec.SpatialCoordinates.DeniedFragments {
					frag = strings.TrimSpace(frag)
					if frag == "" {
						continue
					}
					if strings.Contains(added.Text, frag) {
						msg := fmt.Sprintf("matched denied fragment %q from rule %s (%s)", frag, rec.ID, rec.Title)
						if isCold {
							msg += " [dormant rule re-awakened]"
						}
						advisories = append(advisories, DiffAdvisory{
							RuleID:            rec.ID,
							File:              file,
							Line:              added.Line,
							Severity:          "advisory",
							Message:           msg,
							DormantReawakened: isCold,
						})
					}
				}
			}

			// 3. Check Tier-0 silent error swallowing in production Go files
			if isProdGo && tier0EmptyErrIfRegex.MatchString(added.Text) {
				advisories = append(advisories, DiffAdvisory{
					RuleID:   "TIER0-EMPTY-ERR-BRANCH",
					File:     file,
					Line:     added.Line,
					Severity: "advisory",
					Message:  "empty 'if err != nil {}' block swallows error silently",
				})
			}

			// 4. Check Tier-0 hollow theater placeholders (#644)
			if tier0TheaterPlaceholderRegex.MatchString(added.Text) {
				advisories = append(advisories, DiffAdvisory{
					RuleID:   "TIER0-THEATER-PLACEHOLDER",
					File:     file,
					Line:     added.Line,
					Severity: "advisory",
					Message:  "hollow theater placeholder or unimplemented panic in added line",
				})
			}

			// 5. Check Tier-0 trivial tautological assertions in Go test files (#644)
			if isTestGo && tier0TrivialAssertRegex.MatchString(added.Text) {
				advisories = append(advisories, DiffAdvisory{
					RuleID:   "TIER0-TRIVIAL-ASSERT",
					File:     file,
					Line:     added.Line,
					Severity: "advisory",
					Message:  "tautological test assertion provides zero verification signal",
				})
			}
		}
	}

	return hardViolations, advisories
}

func matchesAnyAllowedPath(file string, allowedPaths []string) bool {
	for _, pat := range allowedPaths {
		if pathMatches(file, pat) {
			return true
		}
	}
	return false
}

func (v *Verifier) runDiffGuardCheck(ctx context.Context, runner CommandRunner, target TaskRef) (CheckResult, []DiffAdvisory, []string) {
	timeoutCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	args := []string{"diff", "--unified=0", "HEAD", "--"}
	args = append(args, target.AllowedPaths...)
	targetStr := strings.Join(target.AllowedPaths, " ")

	stdout, stderr, exitCode, err := runner(timeoutCtx, "git", args...)
	if err != nil {
		return CheckResult{
			Type:   string(CheckTypeDiffGuard),
			Target: targetStr,
			OK:     false,
			Detail: fmt.Sprintf("runner error: %v", err),
		}, nil, nil
	}
	if exitCode != 0 {
		detail := fmt.Sprintf("runner error: git diff exit code %d", exitCode)
		if trimmed := strings.TrimSpace(string(stderr)); trimmed != "" {
			detail += ": " + trimmed
		}
		return CheckResult{
			Type:   string(CheckTypeDiffGuard),
			Target: targetStr,
			OK:     false,
			Detail: detail,
		}, nil, nil
	}

	var vStore *vault.Vault
	vdbPath := strings.TrimSpace(os.Getenv("G8S_VAULT_DB"))
	if vdbPath == "" {
		if stateDir := strings.TrimSpace(os.Getenv("G8S_STATE_DIR")); stateDir != "" {
			vdbPath = filepath.Join(stateDir, "vault.db")
		}
	}
	if vdbPath != "" {
		if _, statErr := os.Stat(vdbPath); statErr == nil {
			if opened, oerr := vault.NewVault(vdbPath, nil); oerr == nil {
				vStore = opened
				defer vStore.Close()
			}
		}
	}

	hardViolations, advisories := EvaluateNetDelta(ctx, string(stdout), target.AllowedPaths, vStore)

	var allFindings []DiffAdvisory
	allFindings = append(allFindings, hardViolations...)
	allFindings = append(allFindings, advisories...)

	var reawakened []string
	seenReawakened := make(map[string]bool)
	for _, adv := range advisories {
		if adv.DormantReawakened && !seenReawakened[adv.RuleID] {
			seenReawakened[adv.RuleID] = true
			reawakened = append(reawakened, adv.RuleID)
		}
	}

	// Hard invariants cannot be overridden by F0 OverrideReason
	if len(hardViolations) > 0 {
		first := hardViolations[0]
		return CheckResult{
			Type:   string(CheckTypeDiffGuard),
			Target: targetStr,
			OK:     false,
			Detail: fmt.Sprintf("hard invariant violation: %s (%s:%d)", first.Message, first.File, first.Line),
		}, allFindings, reawakened
	}

	if len(advisories) > 0 {
		if reason := strings.TrimSpace(target.OverrideReason); reason != "" {
			return CheckResult{
				Type:   string(CheckTypeDiffGuard),
				Target: targetStr,
				OK:     true,
				Detail: fmt.Sprintf("advisory overridden (%d signal(s)): %s", len(advisories), reason),
			}, allFindings, reawakened
		}
		first := advisories[0]
		return CheckResult{
			Type:   string(CheckTypeDiffGuard),
			Target: targetStr,
			OK:     false,
			Detail: fmt.Sprintf("advisory signal: %s (%s:%d)", first.Message, first.File, first.Line),
		}, allFindings, reawakened
	}

	return CheckResult{
		Type:   string(CheckTypeDiffGuard),
		Target: targetStr,
		OK:     true,
		Detail: "ok",
	}, nil, nil
}
