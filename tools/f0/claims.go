package main

import (
	"fmt"
	"regexp"
	"strings"
)

// ClaimViolation represents an uncited or unverifiable status claim.
type ClaimViolation struct {
	LineNum int    `json:"line_num"`
	Line    string `json:"line"`
	Reason  string `json:"reason"`
}

var (
	evIDRegex  = regexp.MustCompile(`\bev-[0-9a-zA-Z_-]+\b`)
	runIDRegex = regexp.MustCompile(`actions/runs/(\d+)`)
	urlRegex   = regexp.MustCompile(`https?://\S+`)
	codeRegex  = regexp.MustCompile("`[^`]*`")
	tokenRegex = regexp.MustCompile(`[\p{L}\p{N}_]+|✅`)
)

var subjectTokens = map[string]bool{
	"ci":       true,
	"check":    true,
	"checks":   true,
	"test":     true,
	"tests":    true,
	"build":    true,
	"lint":     true,
	"gate":     true,
	"verify":   true,
	"audit":    true,
	"trivy":    true,
	"clippy":   true,
	"cargo":    true,
	"pipeline": true,
	"main":     true,
}

var verdictTokens = map[string]bool{
	"pass":       true,
	"passed":     true,
	"passes":     true,
	"green":      true,
	"xanh":       true,
	"ok":         true,
	"succeed":    true,
	"succeeded":  true,
	"succeeds":   true,
	"success":    true,
	"successful": true,
	"fixed":      true,
	"resolved":   true,
	"đạt":        true,
	"thành_công": true,
	"✅":          true,
}

var negationTokens = map[string]bool{
	"not":     true,
	"no":      true,
	"never":   true,
	"chưa":    true,
	"không":   true,
	"fail":    true,
	"failed":  true,
	"failing": true,
	"đỏ":      true,
	"pending": true,
}

// LintClaims scans text for status claims and validates that every claim has a valid citation.
// mode is "gate" (requires ev- sha == headSHA and clean worktree) or "report" (requires ev- PASS, any sha).
func LintClaims(text string, mode string, repoDir string, headSHA string, ciMode string, ledger *Ledger, forge ForgeAdapter) ([]ClaimViolation, error) {
	if text == "" {
		return nil, nil
	}

	if ledger == nil && repoDir != "" {
		var err error
		ledger, err = OpenRepoLedger(repoDir)
		if err != nil {
			return nil, fmt.Errorf("opening repo ledger: %w", err)
		}
	}
	if forge == nil && repoDir != "" {
		var err error
		forge, err = GetForgeAdapter(repoDir)
		if err != nil {
			return nil, fmt.Errorf("getting forge adapter: %w", err)
		}
	}

	lines := strings.Split(text, "\n")
	cleanLines := make([]string, len(lines))
	copy(cleanLines, lines)

	var violations []ClaimViolation

	// Step 1: Process and strip trusted status blocks <!-- f0:status --> ... <!-- /f0:status -->
	inStatusBlock := false
	var statusBlockLines []int

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if strings.Contains(line, "<!-- f0:status -->") {
			inStatusBlock = true
			statusBlockLines = []int{i}
			continue
		}
		if inStatusBlock {
			statusBlockLines = append(statusBlockLines, i)
			if strings.Contains(line, "<!-- /f0:status -->") {
				inStatusBlock = false
				// Verify all ev- IDs inside this status block
				blockHasError := false
				for _, lineIdx := range statusBlockLines {
					evMatches := evIDRegex.FindAllString(lines[lineIdx], -1)
					for _, evID := range evMatches {
						entry, err := ledger.FindByID(evID)
						if err != nil || entry == nil {
							violations = append(violations, ClaimViolation{
								LineNum: lineIdx + 1,
								Line:    lines[lineIdx],
								Reason:  fmt.Sprintf("tampered status block: evidence %s not found in ledger", evID),
							})
							blockHasError = true
							continue
						}
						if entry.Status != "PASS" {
							violations = append(violations, ClaimViolation{
								LineNum: lineIdx + 1,
								Line:    lines[lineIdx],
								Reason:  fmt.Sprintf("tampered status block: evidence %s status is %s (expected PASS)", evID, entry.Status),
							})
							blockHasError = true
							continue
						}
						if mode == "gate" {
							shaMatch := (strings.HasPrefix(entry.SHA, headSHA) || strings.HasPrefix(headSHA, entry.SHA))
							if !shaMatch {
								violations = append(violations, ClaimViolation{
									LineNum: lineIdx + 1,
									Line:    lines[lineIdx],
									Reason:  fmt.Sprintf("tampered status block: evidence %s sha %s does not match head %s", evID, entry.SHA, headSHA),
								})
								blockHasError = true
								continue
							}
							if entry.Dirty {
								violations = append(violations, ClaimViolation{
									LineNum: lineIdx + 1,
									Line:    lines[lineIdx],
									Reason:  fmt.Sprintf("tampered status block: evidence %s recorded on dirty worktree", evID),
								})
								blockHasError = true
								continue
							}
						}
					}
				}
				if !blockHasError {
					// Clear status block lines so they are trusted and skipped
					for _, lineIdx := range statusBlockLines {
						cleanLines[lineIdx] = ""
					}
				}
				statusBlockLines = nil
			}
		}
	}

	// Step 2: Strip fenced code blocks (``` ... ```)
	inFenced := false
	for i := 0; i < len(cleanLines); i++ {
		trimmed := strings.TrimSpace(cleanLines[i])
		if strings.HasPrefix(trimmed, "```") {
			inFenced = !inFenced
			cleanLines[i] = ""
			continue
		}
		if inFenced {
			cleanLines[i] = ""
		}
	}

	// Step 3: Strip quoted lines (lines starting with >)
	for i := 0; i < len(cleanLines); i++ {
		trimmed := strings.TrimSpace(cleanLines[i])
		if strings.HasPrefix(trimmed, ">") {
			cleanLines[i] = ""
		}
	}

	// Step 4: Tokenize each line, detect claims, and validate citations
	for i := 0; i < len(cleanLines); i++ {
		rawLine := lines[i]
		line := cleanLines[i]
		if strings.TrimSpace(line) == "" {
			continue
		}

		// Extract run IDs and ev IDs from raw line BEFORE stripping URLs
		extractedRuns := extractRunIDs(rawLine)
		extractedEVs := evIDRegex.FindAllString(rawLine, -1)

		// Strip inline code and URLs for tokenization
		lineWithoutCode := codeRegex.ReplaceAllString(line, " ")
		lineWithoutURLs := urlRegex.ReplaceAllString(lineWithoutCode, " ")

		// Unicode lowercase
		lineLower := strings.ToLower(lineWithoutURLs)

		// Replace two-word phrase "thành công" with single token "thành_công"
		lineLower = strings.ReplaceAll(lineLower, "thành công", "thành_công")

		tokens := tokenRegex.FindAllString(lineLower, -1)
		if len(tokens) == 0 {
			continue
		}

		// Detect if this line is a status claim
		isClaim := false
		for sIdx, sTok := range tokens {
			if !subjectTokens[sTok] {
				continue
			}
			for vIdx, vTok := range tokens {
				if !verdictTokens[vTok] {
					continue
				}
				dist := sIdx - vIdx
				if dist < 0 {
					dist = -dist
				}
				if dist <= 4 {
					// Check if negated
					negated := false
					for nIdx, nTok := range tokens {
						if !negationTokens[nTok] {
							continue
						}
						// within 3 tokens before verdict token: nIdx < vIdx && vIdx - nIdx <= 3
						// OR between subject and verdict
						minIdx, maxIdx := sIdx, vIdx
						if vIdx < sIdx {
							minIdx, maxIdx = vIdx, sIdx
						}
						if (nIdx < vIdx && vIdx-nIdx <= 3) || (nIdx > minIdx && nIdx < maxIdx) {
							negated = true
							break
						}
					}
					if !negated {
						isClaim = true
						break
					}
				}
			}
			if isClaim {
				break
			}
		}

		if !isClaim {
			continue
		}

		// Line is a status claim! Validate that it has at least one valid citation
		validCitations := 0
		var citeErrors []string

		for _, evID := range extractedEVs {
			if ledger == nil {
				citeErrors = append(citeErrors, "repo ledger not available")
				continue
			}
			entry, err := ledger.FindByID(evID)
			if err != nil || entry == nil {
				citeErrors = append(citeErrors, fmt.Sprintf("evidence %s not found in repo ledger", evID))
				continue
			}
			if entry.Status != "PASS" {
				citeErrors = append(citeErrors, fmt.Sprintf("evidence %s status is %s (expected PASS)", evID, entry.Status))
				continue
			}
			if mode == "gate" {
				shaMatch := (strings.HasPrefix(entry.SHA, headSHA) || strings.HasPrefix(headSHA, entry.SHA))
				if !shaMatch {
					citeErrors = append(citeErrors, fmt.Sprintf("evidence %s sha %s does not match head %s", evID, entry.SHA, headSHA))
					continue
				}
				if entry.Dirty {
					citeErrors = append(citeErrors, fmt.Sprintf("evidence %s was recorded on dirty worktree", evID))
					continue
				}
			}
			validCitations++
		}

		for _, runID := range extractedRuns {
			if ciMode == "none" {
				citeErrors = append(citeErrors, "workflow run citation not allowed when ci: none")
				continue
			}
			if forge == nil {
				citeErrors = append(citeErrors, "forge adapter not available to verify workflow run")
				continue
			}
			runSHA, conclusion, err := forge.VerifyWorkflowRun(repoDir, runID)
			if err != nil {
				citeErrors = append(citeErrors, fmt.Sprintf("failed to verify workflow run %s: %v", runID, err))
				continue
			}
			if !strings.EqualFold(conclusion, "success") {
				citeErrors = append(citeErrors, fmt.Sprintf("workflow run %s conclusion is %s (expected success)", runID, conclusion))
				continue
			}
			if mode == "gate" {
				shaMatch := (strings.HasPrefix(runSHA, headSHA) || strings.HasPrefix(headSHA, runSHA))
				if !shaMatch {
					citeErrors = append(citeErrors, fmt.Sprintf("workflow run %s sha %s does not match head %s", runID, runSHA, headSHA))
					continue
				}
			}
			validCitations++
		}

		if validCitations == 0 {
			reason := "status claim without valid evidence or workflow citation"
			if len(citeErrors) > 0 {
				reason = strings.Join(citeErrors, "; ")
			}
			violations = append(violations, ClaimViolation{
				LineNum: i + 1,
				Line:    rawLine,
				Reason:  reason,
			})
		}
	}

	return violations, nil
}

func extractRunIDs(s string) []string {
	matches := runIDRegex.FindAllStringSubmatch(s, -1)
	var runIDs []string
	for _, m := range matches {
		if len(m) >= 2 {
			runIDs = append(runIDs, m[1])
		}
	}
	return runIDs
}
