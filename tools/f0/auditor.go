package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// DigestAction summarizes a discrete action recorded in the evidence ledgers.
type DigestAction struct {
	ID           string `json:"id"`
	Ts           string `json:"ts"`
	Kind         string `json:"kind"`
	Repo         string `json:"repo,omitempty"`
	SHA          string `json:"sha,omitempty"`
	Status       string `json:"status"`
	Tier         string `json:"tier,omitempty"`
	ItemsChecked int    `json:"items_checked,omitempty"`
	Detail       string `json:"detail,omitempty"`
}

// AuditClaimResult represents the verification status of an individual audited claim.
type AuditClaimResult struct {
	Claim  string `json:"claim"`
	Status string `json:"status"` // "PASS" or "FAIL"
	Reason string `json:"reason,omitempty"`
}

// AuditVerdict records the aggregate outcome of an auditor run.
type AuditVerdict struct {
	Provider string             `json:"provider"`
	Status   string             `json:"status"` // "PASS", "FAIL", "UNAUDITED"
	Claims   []AuditClaimResult `json:"claims,omitempty"`
}

// AuditorProvider is a pluggable auditor interface evaluated during session digest generation.
type AuditorProvider interface {
	Name() string
	Audit(digest *SessionDigest, repoEntries []Entry) (AuditVerdict, error)
}

// SessionDigest represents a machine-readable summary of session activities and attestations.
type SessionDigest struct {
	MandateID    string         `json:"mandate_id,omitempty"`
	CurrentTier  string         `json:"current_tier"`
	BudgetSpent  int            `json:"budget_spent"`
	BudgetLimit  int            `json:"budget_limit"`
	EvidenceIDs  []string       `json:"evidence_ids"`
	Waivers      []string       `json:"waivers"`
	ManualTags   []string       `json:"manual_tags"`
	Overrides    []string       `json:"overrides"`
	Reverts      []string       `json:"reverts"`
	Actions      []DigestAction `json:"actions"`
	AuditStamp   string         `json:"audit_stamp"` // "PASS", "FAIL", "UNAUDITED"
	AuditVerdict *AuditVerdict  `json:"audit_verdict,omitempty"`
}

// BuildSessionDigest constructs a SessionDigest strictly from the repo and global ledgers.
func BuildSessionDigest(repoDir string, auditors []AuditorProvider) (*SessionDigest, error) {
	absRepo := repoDir
	if repoDir != "" {
		if abs, err := filepath.Abs(repoDir); err == nil {
			absRepo = abs
		}
	}

	var repoEntries []Entry
	if absRepo != "" {
		if rLedger, err := OpenRepoLedger(absRepo); err == nil {
			repoEntries, _ = rLedger.ReadEntries()
		}
	}

	var globalEntries []Entry
	if gLedger, err := OpenGlobalLedger(); err == nil {
		globalEntries, _ = gLedger.ReadEntries()
	}

	m, _ := LoadActiveMandate()
	var mandateID string
	currentTier := "T0"
	budgetSpent := 0
	budgetLimit := 0

	if m != nil {
		mandateID = m.ID
		budgetLimit = m.ErrorBudget.Limit
		if brk, err := EvaluateCircuitBreaker(m, absRepo, 0); err == nil {
			currentTier = brk.EffectiveMaxTier
			budgetSpent = brk.Spent
			budgetLimit = brk.Limit
		}
	}

	// Merge all entries for chronological action extraction
	combined := make([]Entry, 0, len(repoEntries)+len(globalEntries))
	combined = append(combined, repoEntries...)
	combined = append(combined, globalEntries...)

	sort.SliceStable(combined, func(i, j int) bool {
		ti := parseEntryTime(combined[i].Ts)
		tj := parseEntryTime(combined[j].Ts)
		return ti.Before(tj)
	})

	seenIDs := make(map[string]bool)
	evidenceIDs := []string{}
	waivers := []string{}
	manualTags := []string{}
	overrides := []string{}
	reverts := []string{}
	actions := []DigestAction{}

	for _, e := range combined {
		if e.ID != "" {
			if seenIDs[e.ID] {
				continue
			}
			seenIDs[e.ID] = true
		}

		if strings.HasPrefix(e.ID, "ev-") || e.Kind == "gate" || e.Kind == "run" || e.Kind == "g8s-deliver" || e.Kind == "go-test" {
			evidenceIDs = append(evidenceIDs, e.ID)
		}

		if e.Kind == "waiver" {
			waivers = append(waivers, e.ID)
		}

		if e.Kind == "manual" {
			manualTags = append(manualTags, e.ID)
		}

		if e.Kind == "override" || (len(e.Extra) > 0 && e.Extra["override"] != nil) || strings.HasPrefix(e.Kind, "override") {
			overrides = append(overrides, e.ID)
		}

		if e.Kind == "revert" || (len(e.Extra) > 0 && e.Extra["revert"] != nil) || strings.HasPrefix(e.Kind, "revert") {
			reverts = append(reverts, e.ID)
		}

		act := DigestAction{
			ID:           e.ID,
			Ts:           e.Ts,
			Kind:         e.Kind,
			Repo:         e.Repo,
			SHA:          e.SHA,
			Status:       e.Status,
			ItemsChecked: e.ItemsChecked,
		}
		if t, ok := e.Extra["tier"].(string); ok {
			act.Tier = t
		} else if t, ok := e.Extra["required_tier"].(string); ok {
			act.Tier = t
		}
		if reason, ok := e.Extra["reason"].(string); ok && reason != "" {
			act.Detail = reason
		} else if len(e.Cmd) > 0 {
			act.Detail = strings.Join(e.Cmd, " ")
		} else if e.Tail != "" {
			act.Detail = e.Tail
		}
		actions = append(actions, act)
	}

	digest := &SessionDigest{
		MandateID:    mandateID,
		CurrentTier:  currentTier,
		BudgetSpent:  budgetSpent,
		BudgetLimit:  budgetLimit,
		EvidenceIDs:  evidenceIDs,
		Waivers:      waivers,
		ManualTags:   manualTags,
		Overrides:    overrides,
		Reverts:      reverts,
		Actions:      actions,
		AuditStamp:   "UNAUDITED",
		AuditVerdict: &AuditVerdict{Status: "UNAUDITED"},
	}

	// Pluggable auditor execution
	var finalVerdict *AuditVerdict
	var ranAny bool
	for _, auditor := range auditors {
		if auditor == nil {
			continue
		}
		ranAny = true
		v, err := auditor.Audit(digest, repoEntries)
		if err != nil {
			v = AuditVerdict{
				Provider: auditor.Name(),
				Status:   "FAIL",
				Claims: []AuditClaimResult{{
					Claim:  "auditor_execution",
					Status: "FAIL",
					Reason: err.Error(),
				}},
			}
		}
		if v.Provider == "" {
			v.Provider = auditor.Name()
		}

		if v.Status == "FAIL" {
			finalVerdict = &v
			digest.AuditStamp = "FAIL"
			digest.AuditVerdict = &v

			// Automatically debit error budget
			debitReason := "auditor FAIL: " + v.Provider
			_ = RecordDebit(absRepo, debitReason, digest.MandateID)

			// Update spent and tier from re-evaluation
			if m != nil {
				if brk, err := EvaluateCircuitBreaker(m, absRepo, 0); err == nil {
					digest.BudgetSpent = brk.Spent
					digest.CurrentTier = brk.EffectiveMaxTier
				}
			} else {
				digest.BudgetSpent++
			}
			break
		} else if v.Status == "PASS" {
			if finalVerdict == nil || finalVerdict.Status != "FAIL" {
				finalVerdict = &v
				digest.AuditStamp = "PASS"
				digest.AuditVerdict = &v
			}
		}
	}

	if !ranAny || finalVerdict == nil {
		digest.AuditStamp = "UNAUDITED"
		digest.AuditVerdict = &AuditVerdict{Status: "UNAUDITED"}
	}

	return digest, nil
}

// scorecardAuditor is a deterministic read-only auditor verifying scorecard markdown tables.
type scorecardAuditor struct {
	repoDir            string
	scorecardPath      string
	expectedDeliveries map[string]int
	expectedSHA        string
}

// NewScorecardAuditor creates an AuditorProvider that checks scorecard tables against expected delivery counts and head SHA.
func NewScorecardAuditor(repoDir, scorecardPath string, expectedDeliveries map[string]int, expectedSHA string) AuditorProvider {
	return &scorecardAuditor{
		repoDir:            repoDir,
		scorecardPath:      scorecardPath,
		expectedDeliveries: expectedDeliveries,
		expectedSHA:        expectedSHA,
	}
}

func (s *scorecardAuditor) Name() string {
	return "scorecard-auditor"
}

func parseScorecardDeliveries(content string) map[string]int {
	counts := make(map[string]int)
	lines := strings.Split(content, "\n")
	keyCol := 0
	valCol := 1

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "|") || !strings.HasSuffix(trimmed, "|") {
			continue
		}
		cells := strings.Split(trimmed, "|")
		if len(cells) < 3 {
			continue
		}

		var row []string
		for _, c := range cells[1 : len(cells)-1] {
			row = append(row, strings.TrimSpace(c))
		}
		if len(row) < 2 {
			continue
		}

		isSep := false
		for i, c := range row {
			lower := strings.ToLower(c)
			if strings.HasPrefix(c, "---") || strings.HasPrefix(c, ":---") || strings.HasPrefix(c, "--:") {
				isSep = true
				break
			}
			if lower == "module" || lower == "project" || lower == "key" || lower == "name" {
				keyCol = i
			}
			if lower == "delivered" || lower == "deliveries" || lower == "count" {
				valCol = i
			}
		}
		if isSep {
			continue
		}

		if keyCol < len(row) && valCol < len(row) {
			rawKey := row[keyCol]
			cleanKey := strings.Trim(rawKey, "*_[] ")
			valStr := row[valCol]
			fields := strings.Fields(valStr)
			if len(fields) > 0 {
				valStr = fields[0]
			}
			if n, err := strconv.Atoi(valStr); err == nil {
				counts[rawKey] = n
				counts[cleanKey] = n
			} else {
				// Search other columns if primary value column was non-numeric
				for colIdx := 1; colIdx < len(row); colIdx++ {
					cv := row[colIdx]
					cfields := strings.Fields(cv)
					if len(cfields) > 0 {
						cv = cfields[0]
					}
					if n, err := strconv.Atoi(cv); err == nil {
						counts[rawKey] = n
						counts[cleanKey] = n
						break
					}
				}
			}
		}
	}
	return counts
}

func (s *scorecardAuditor) Audit(digest *SessionDigest, repoEntries []Entry) (AuditVerdict, error) {
	fullPath := s.scorecardPath
	if !filepath.IsAbs(fullPath) && s.repoDir != "" {
		fullPath = filepath.Join(s.repoDir, s.scorecardPath)
	}

	contentBytes, err := os.ReadFile(fullPath)
	if err != nil {
		return AuditVerdict{
			Provider: s.Name(),
			Status:   "FAIL",
			Claims: []AuditClaimResult{{
				Claim:  "scorecard_read",
				Status: "FAIL",
				Reason: fmt.Sprintf("unable to read scorecard at %s: %v", fullPath, err),
			}},
		}, nil
	}
	content := string(contentBytes)

	var claims []AuditClaimResult

	// 1. Verify expected SHA if specified
	if s.expectedSHA != "" {
		shaMatched := false
		if s.repoDir != "" {
			cmd := exec.Command("git", "-C", s.repoDir, "rev-parse", "HEAD")
			if out, err := cmd.Output(); err == nil {
				head := strings.TrimSpace(string(out))
				if head != "" && (strings.HasPrefix(head, s.expectedSHA) || strings.HasPrefix(s.expectedSHA, head)) {
					shaMatched = true
				}
			}
		}
		if strings.Contains(content, s.expectedSHA) {
			shaMatched = true
		}

		if !shaMatched {
			claims = append(claims, AuditClaimResult{
				Claim:  "scorecard_sha",
				Status: "FAIL",
				Reason: fmt.Sprintf("expected SHA %s not verified in repo HEAD or scorecard text", s.expectedSHA),
			})
		} else {
			claims = append(claims, AuditClaimResult{
				Claim:  "scorecard_sha",
				Status: "PASS",
			})
		}
	}

	// 2. Verify delivery counts against table
	tableCounts := parseScorecardDeliveries(content)

	if len(s.expectedDeliveries) > 0 {
		keys := make([]string, 0, len(s.expectedDeliveries))
		for k := range s.expectedDeliveries {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, mod := range keys {
			expected := s.expectedDeliveries[mod]
			claimed, ok := tableCounts[mod]
			claimName := "delivery_count_" + mod
			if !ok {
				claims = append(claims, AuditClaimResult{
					Claim:  claimName,
					Status: "FAIL",
					Reason: fmt.Sprintf("scorecard missing module/key %s", mod),
				})
			} else if claimed != expected {
				claims = append(claims, AuditClaimResult{
					Claim:  claimName,
					Status: "FAIL",
					Reason: fmt.Sprintf("scorecard claims %d deliveries for %s, expected %d", claimed, mod, expected),
				})
			} else {
				claims = append(claims, AuditClaimResult{
					Claim:  claimName,
					Status: "PASS",
				})
			}
		}
	} else {
		claimName := "scorecard_table_presence"
		if len(tableCounts) == 0 {
			claims = append(claims, AuditClaimResult{
				Claim:  claimName,
				Status: "FAIL",
				Reason: "no delivery table rows found in scorecard",
			})
		} else {
			claims = append(claims, AuditClaimResult{
				Claim:  claimName,
				Status: "PASS",
			})
		}
	}

	overall := "PASS"
	for _, c := range claims {
		if c.Status == "FAIL" {
			overall = "FAIL"
			break
		}
	}
	if len(claims) == 0 {
		overall = "UNAUDITED"
	}

	return AuditVerdict{
		Provider: s.Name(),
		Status:   overall,
		Claims:   claims,
	}, nil
}

func digestCommand(args []string) int {
	var repoDir string
	var jsonOutput bool

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--repo":
			if i+1 < len(args) {
				repoDir = args[i+1]
				i++
			}
		case "--json":
			jsonOutput = true
		}
	}

	if repoDir == "" {
		repoDir = "."
	}
	absRepo, err := filepath.Abs(repoDir)
	if err != nil {
		absRepo = repoDir
	}

	digest, err := BuildSessionDigest(absRepo, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "f0 digest: %v\n", err)
		return 1
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(digest); err != nil {
			fmt.Fprintf(os.Stderr, "f0 digest: encoding json: %v\n", err)
			return 1
		}
		return 0
	}

	fmt.Println("Session Digest:")
	fmt.Printf("  Mandate ID:     %s\n", digest.MandateID)
	fmt.Printf("  Current Tier:   %s\n", digest.CurrentTier)
	fmt.Printf("  Budget:         %d / %d spent\n", digest.BudgetSpent, digest.BudgetLimit)
	fmt.Printf("  Audit Stamp:    %s\n", digest.AuditStamp)
	fmt.Printf("  Evidence Count: %d\n", len(digest.EvidenceIDs))
	fmt.Printf("  Actions Count:  %d\n", len(digest.Actions))
	fmt.Printf("  Waivers:        %d\n", len(digest.Waivers))
	fmt.Printf("  Manual Tags:    %d\n", len(digest.ManualTags))
	fmt.Printf("  Overrides:      %d\n", len(digest.Overrides))
	fmt.Printf("  Reverts:        %d\n", len(digest.Reverts))
	if digest.AuditVerdict != nil && len(digest.AuditVerdict.Claims) > 0 {
		fmt.Println("  Audit Claims:")
		for _, c := range digest.AuditVerdict.Claims {
			fmt.Printf("    - %s: %s", c.Claim, c.Status)
			if c.Reason != "" {
				fmt.Printf(" (%s)", c.Reason)
			}
			fmt.Println()
		}
	}
	return 0
}

func init() {
	commands["digest"] = digestCommand
}
