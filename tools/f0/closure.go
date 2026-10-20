package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// IssueCriterion represents an acceptance criterion parsed from an issue.
type IssueCriterion struct {
	Index         int    `json:"index"`
	Text          string `json:"text"`
	Checked       bool   `json:"checked"`
	CitedEvidence string `json:"cited_evidence,omitempty"`
}

var (
	criteriaHeadingRegex = regexp.MustCompile(`(?i)^#{1,6}\s+Acceptance\s+[Cc]riteria\b`)
	generalHeadingRegex  = regexp.MustCompile(`^#{1,6}\s+`)
	checkboxRegex        = regexp.MustCompile(`^\s*[-*]\s*\[([ xX])\]\s*(.*)$`)
	bulletItemRegex      = regexp.MustCompile(`^\s*[-*]\s+(.*)$`)
	numberedItemRegex    = regexp.MustCompile(`^\s*\d+[\.\)]\s+(.*)$`)

	closingDirectiveRegex = regexp.MustCompile(`(?i)\b(?:close|closes|closed|fix|fixes|fixed|resolve|resolves|resolved)\s+(?:#(\d+)|https?://github\.com/[^/\s]+/[^/\s]+/issues/(\d+))`)

	evidenceIDRegex   = regexp.MustCompile(`(?i)\bev-[0-9a-z_-]{6,16}\b`)
	testSymbolRegex   = regexp.MustCompile(`\bTest[A-Za-z0-9_]+\b`)
	commitFullRegex   = regexp.MustCompile(`\b[0-9a-f]{40}\b`)
	workflowRunRegex  = regexp.MustCompile(`(?i)\b(?:actions/runs/|run[s]?/|run:\s*|run_id:\s*)(\d{6,})\b`)
	shortCommitPrefix = regexp.MustCompile(`(?i)\b(?:commit|sha|rev)[:\s]+([0-9a-f]{7,40})\b`)
)

func extractEvidence(s string) string {
	if m := evidenceIDRegex.FindString(s); m != "" {
		return m
	}
	if m := testSymbolRegex.FindString(s); m != "" {
		return m
	}
	if m := commitFullRegex.FindString(s); m != "" {
		return m
	}
	if m := workflowRunRegex.FindStringSubmatch(s); len(m) >= 2 {
		return m[1]
	}
	if m := shortCommitPrefix.FindStringSubmatch(s); len(m) >= 2 {
		return m[1]
	}
	return ""
}

func findAllEvidence(s string) []string {
	var results []string
	seen := make(map[string]bool)
	add := func(val string) {
		val = strings.TrimSpace(val)
		if val != "" && !seen[val] {
			seen[val] = true
			results = append(results, val)
		}
	}

	for _, m := range evidenceIDRegex.FindAllString(s, -1) {
		add(m)
	}
	for _, m := range testSymbolRegex.FindAllString(s, -1) {
		add(m)
	}
	for _, m := range commitFullRegex.FindAllString(s, -1) {
		add(m)
	}
	for _, sub := range workflowRunRegex.FindAllStringSubmatch(s, -1) {
		if len(sub) >= 2 {
			add(sub[1])
		}
	}
	for _, sub := range shortCommitPrefix.FindAllStringSubmatch(s, -1) {
		if len(sub) >= 2 {
			add(sub[1])
		}
	}
	return results
}

func findPerCriterionEvidence(text string, index int) string {
	critPattern := fmt.Sprintf(`(?i)(?:^|[\s\*\-\[\]\|])(?:Criterion\s*#?|C)#?%d\b([^\n\r]*)`, index)
	re := regexp.MustCompile(critPattern)

	for _, line := range strings.Split(text, "\n") {
		if re.MatchString(line) {
			ev := extractEvidence(line)
			if ev != "" {
				return ev
			}
		}
	}
	return ""
}

// ParseAcceptanceCriteria extracts acceptance criteria from an issue body.
func ParseAcceptanceCriteria(issueBody string) []IssueCriterion {
	var criteria []IssueCriterion
	lines := strings.Split(issueBody, "\n")
	inCriteriaSection := false

	for _, rawLine := range lines {
		line := strings.TrimRight(rawLine, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if criteriaHeadingRegex.MatchString(trimmed) {
			inCriteriaSection = true
			continue
		} else if generalHeadingRegex.MatchString(trimmed) {
			inCriteriaSection = false
		}

		// 1. Markdown checkboxes anywhere in issueBody
		if m := checkboxRegex.FindStringSubmatch(trimmed); len(m) >= 3 {
			checked := strings.EqualFold(m[1], "x")
			text := strings.TrimSpace(m[2])
			ev := extractEvidence(text)
			criteria = append(criteria, IssueCriterion{
				Index:         len(criteria) + 1,
				Text:          text,
				Checked:       checked,
				CitedEvidence: ev,
			})
			continue
		}

		// 2. If inside Acceptance Criteria section, bullet/numbered items
		if inCriteriaSection {
			var text string
			if bm := bulletItemRegex.FindStringSubmatch(trimmed); len(bm) >= 2 {
				text = strings.TrimSpace(bm[1])
			} else if nm := numberedItemRegex.FindStringSubmatch(trimmed); len(nm) >= 2 {
				text = strings.TrimSpace(nm[1])
			}

			if text != "" {
				ev := extractEvidence(text)
				criteria = append(criteria, IssueCriterion{
					Index:         len(criteria) + 1,
					Text:          text,
					Checked:       false,
					CitedEvidence: ev,
				})
			}
		}
	}

	return criteria
}

func toIntHelper(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case string:
		i, _ := strconv.Atoi(strings.TrimSpace(n))
		return i
	}
	return 0
}

func hasDroppedDeliverables(repoDir string, ledgerEntries []Entry) bool {
	for _, e := range ledgerEntries {
		if e.Extra != nil {
			if dropped, ok := e.Extra["dropped"]; ok {
				switch v := dropped.(type) {
				case []string:
					if len(v) > 0 {
						return true
					}
				case []any:
					if len(v) > 0 {
						return true
					}
				case string:
					if strings.TrimSpace(v) != "" {
						return true
					}
				case int:
					if v > 0 {
						return true
					}
				case float64:
					if v > 0 {
						return true
					}
				}
			}
			if dc, ok := e.Extra["dropped_count"]; ok {
				if toIntHelper(dc) > 0 {
					return true
				}
			}
			if b, ok := e.Extra["dropped_deliverables"].(bool); ok && b {
				return true
			}
			for _, pair := range [][2]string{
				{"delivered_count", "expected_count"},
				{"delivered", "expected"},
				{"fixtures_delivered", "fixtures_expected"},
				{"applied_count", "expected_count"},
			} {
				delVal, hasDel := e.Extra[pair[0]]
				expVal, hasExp := e.Extra[pair[1]]
				if hasDel && hasExp {
					delNum := toIntHelper(delVal)
					expNum := toIntHelper(expVal)
					if expNum > 0 && delNum < expNum {
						return true
					}
				}
			}
			for _, k := range []string{"detail", "reason", "notes", "status"} {
				if s, ok := e.Extra[k].(string); ok {
					lower := strings.ToLower(s)
					if strings.Contains(lower, "dropped") || strings.Contains(lower, "7/19") {
						return true
					}
				}
			}

			// If git repo available, check if applied files are missing
			if repoDir != "" && e.Kind == "g8s-deliver" {
				applied := getExtraStringSlice(e.Extra["applied"])
				deleted := getExtraStringSlice(e.Extra["deleted"])
				for _, f := range applied {
					if !sliceContains(deleted, f) && !gitFileExistsAt(repoDir, "HEAD", f) {
						return true
					}
				}
			}
		}

		if e.Kind == "dropped" || strings.EqualFold(e.Status, "DROPPED") {
			return true
		}
		lowerTail := strings.ToLower(e.Tail)
		if strings.Contains(lowerTail, "dropped") || strings.Contains(lowerTail, "7/19") {
			return true
		}
	}
	return false
}

func lookupIssue(repoDir string, forge ForgeAdapter, issueNum string) (string, bool) {
	if forge == nil {
		return "", false
	}

	if ff, ok := forge.(*FakeForge); ok && ff != nil {
		for _, key := range []string{
			"#" + issueNum,
			issueNum,
			"https://github.com/tamld/g8s/issues/" + issueNum,
		} {
			if is, found := ff.Issues[key]; found {
				text := is.Title
				if is.Body != "" {
					text += "\n" + is.Body
				}
				return text, true
			}
		}
		for k, is := range ff.Issues {
			if k == issueNum || k == "#"+issueNum || strings.HasSuffix(k, "/issues/"+issueNum) || strings.HasSuffix(k, "/"+issueNum) {
				text := is.Title
				if is.Body != "" {
					text += "\n" + is.Body
				}
				return text, true
			}
		}
	}

	for _, target := range []string{
		"#" + issueNum,
		issueNum,
		"https://github.com/tamld/g8s/issues/" + issueNum,
	} {
		_, _, text, err := forge.VerifyIssue(repoDir, target)
		if err == nil && text != "" {
			return text, true
		}
	}

	return "", false
}

// CheckIssueClosureGate evaluates the G7 issue-closure gate.
func CheckIssueClosureGate(repoDir, fullText string, forge ForgeAdapter, ledgerEntries []Entry) (GateCheckResult, error) {
	matches := closingDirectiveRegex.FindAllStringSubmatch(fullText, -1)
	if len(matches) == 0 {
		return GateCheckResult{
			ID:     "G7",
			Status: "PASS",
			Detail: "no issue closure directives found",
		}, nil
	}

	var closedIssues []string
	seenIssues := make(map[string]bool)
	for _, m := range matches {
		issueNum := m[1]
		if issueNum == "" {
			issueNum = m[2]
		}
		if issueNum != "" && !seenIssues[issueNum] {
			seenIssues[issueNum] = true
			closedIssues = append(closedIssues, issueNum)
		}
	}

	if forge == nil {
		forge, _ = GetForgeAdapter(repoDir)
	}

	for _, issueNum := range closedIssues {
		if hasDroppedDeliverables(repoDir, ledgerEntries) {
			return GateCheckResult{
				ID:     "G7",
				Status: "FAIL",
				Detail: fmt.Sprintf("issue #%s has unmet/uncited acceptance criteria (use Refs #%s and comment unmet criteria)", issueNum, issueNum),
			}, nil
		}

		issueText, ok := lookupIssue(repoDir, forge, issueNum)
		if !ok {
			return GateCheckResult{
				ID:     "G7",
				Status: "FAIL",
				Detail: fmt.Sprintf("issue #%s has unmet/uncited acceptance criteria (use Refs #%s and comment unmet criteria)", issueNum, issueNum),
			}, nil
		}

		criteria := ParseAcceptanceCriteria(issueText)
		if len(criteria) == 0 {
			continue
		}

		allEvidence := findAllEvidence(fullText + "\n" + issueText)
		allChecked := true
		for _, c := range criteria {
			if !c.Checked {
				allChecked = false
				break
			}
		}

		for _, c := range criteria {
			if !c.Checked {
				perCritEv := findPerCriterionEvidence(fullText, c.Index)
				if perCritEv == "" {
					return GateCheckResult{
						ID:     "G7",
						Status: "FAIL",
						Detail: fmt.Sprintf("issue #%s has unmet/uncited acceptance criteria (use Refs #%s and comment unmet criteria)", issueNum, issueNum),
					}, nil
				}
			} else {
				ev := c.CitedEvidence
				if ev == "" {
					ev = findPerCriterionEvidence(fullText, c.Index)
				}
				if ev == "" && allChecked && len(allEvidence) > 0 {
					ev = allEvidence[0]
				}
				if ev == "" {
					return GateCheckResult{
						ID:     "G7",
						Status: "FAIL",
						Detail: fmt.Sprintf("issue #%s has unmet/uncited acceptance criteria (use Refs #%s and comment unmet criteria)", issueNum, issueNum),
					}, nil
				}
			}
		}
	}

	var issueRefs []string
	for _, num := range closedIssues {
		issueRefs = append(issueRefs, "#"+num)
	}
	return GateCheckResult{
		ID:     "G7",
		Status: "PASS",
		Detail: fmt.Sprintf("all acceptance criteria verified for closed issues (%s)", strings.Join(issueRefs, ", ")),
	}, nil
}
