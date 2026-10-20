package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// DefaultUpgradeCleanStreak is the number of clean evidenced runs/gates required
// to restore max tier after budget exhaustion.
const DefaultUpgradeCleanStreak = 3

// BreakerStatus represents the error-budget circuit breaker evaluation state.
type BreakerStatus struct {
	MandateID             string `json:"mandate_id"`
	ConfiguredMaxTier     string `json:"configured_max_tier"`
	EffectiveMaxTier      string `json:"effective_max_tier"`
	Spent                 int    `json:"spent"`
	Limit                 int    `json:"limit"`
	Tripped               bool   `json:"tripped"`
	RestoredByStreak      bool   `json:"restored_by_streak"`
	CleanStreak           int    `json:"clean_streak"`
	UpgradeStreakRequired int    `json:"upgrade_streak_required"`
	Reason                string `json:"reason"`
}

func parseEntryTime(ts string) time.Time {
	if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t
	}
	return time.Time{}
}

func resolveConfiguredMaxTier(m *Mandate, repoPath string) string {
	if m == nil || len(m.Repos) == 0 {
		return "T0"
	}
	if repoPath != "" {
		absRepo, _ := filepath.Abs(repoPath)
		for _, r := range m.Repos {
			rAbs, _ := filepath.Abs(r.Path)
			if filepath.Clean(rAbs) == filepath.Clean(absRepo) || r.Name == repoPath || filepath.Clean(r.Path) == filepath.Clean(repoPath) {
				return r.MaxTier
			}
		}
	}
	// Fall back to highest configured tier among repos
	maxRank := -1
	bestTier := "T0"
	for _, r := range m.Repos {
		if tierRank(r.MaxTier) > maxRank {
			maxRank = tierRank(r.MaxTier)
			bestTier = r.MaxTier
		}
	}
	return bestTier
}

// EvaluateCircuitBreaker determines the live circuit breaker status for mandate m.
func EvaluateCircuitBreaker(m *Mandate, repoPath string, upgradeStreakRequired int) (BreakerStatus, error) {
	if upgradeStreakRequired <= 0 {
		upgradeStreakRequired = DefaultUpgradeCleanStreak
	}

	if m == nil {
		var err error
		m, err = LoadActiveMandate()
		if err != nil {
			return BreakerStatus{
				ConfiguredMaxTier:     "T0",
				EffectiveMaxTier:      "T0",
				UpgradeStreakRequired: upgradeStreakRequired,
				Reason:                fmt.Sprintf("loading active mandate: %v", err),
			}, err
		}
		if m == nil {
			return BreakerStatus{
				ConfiguredMaxTier:     "T0",
				EffectiveMaxTier:      "T0",
				UpgradeStreakRequired: upgradeStreakRequired,
				Reason:                "no active mandate",
			}, nil
		}
	}

	configuredMaxTier := resolveConfiguredMaxTier(m, repoPath)

	gLedger, err := OpenGlobalLedger()
	if err != nil {
		return BreakerStatus{}, fmt.Errorf("opening global ledger: %w", err)
	}
	gEntries, err := gLedger.ReadEntries()
	if err != nil {
		return BreakerStatus{}, fmt.Errorf("reading global ledger: %w", err)
	}

	// Find the most recent breaker-reset for this mandate
	lastResetIdx := -1
	for i := len(gEntries) - 1; i >= 0; i-- {
		e := &gEntries[i]
		if e.Kind == "breaker-reset" {
			mid, _ := e.Extra["mandate_id"].(string)
			if mid == "" || mid == m.ID {
				lastResetIdx = i
				break
			}
		}
	}

	var relevantGlobal []Entry
	var cutoffTime time.Time
	if lastResetIdx >= 0 {
		relevantGlobal = gEntries[lastResetIdx+1:]
		cutoffTime = parseEntryTime(gEntries[lastResetIdx].Ts)
	} else {
		cutoffTime = parseEntryTime(m.IssuedAt)
		for _, e := range gEntries {
			if e.Kind == "mandate-issue" {
				continue
			}
			eTime := parseEntryTime(e.Ts)
			if !eTime.Before(cutoffTime) {
				relevantGlobal = append(relevantGlobal, e)
			}
		}
	}

	var relevantRepo []Entry
	if repoPath != "" {
		rLedger, rErr := OpenRepoLedger(repoPath)
		if rErr == nil {
			rEntries, _ := rLedger.ReadEntries()
			for _, re := range rEntries {
				reTime := parseEntryTime(re.Ts)
				if lastResetIdx >= 0 {
					if reTime.After(cutoffTime) {
						relevantRepo = append(relevantRepo, re)
					}
				} else {
					if !reTime.Before(cutoffTime) {
						relevantRepo = append(relevantRepo, re)
					}
				}
			}
		}
	}

	allEntries := make([]Entry, 0, len(relevantGlobal)+len(relevantRepo))
	allEntries = append(allEntries, relevantGlobal...)
	allEntries = append(allEntries, relevantRepo...)

	sort.SliceStable(allEntries, func(i, j int) bool {
		ti := parseEntryTime(allEntries[i].Ts)
		tj := parseEntryTime(allEntries[j].Ts)
		return ti.Before(tj)
	})

	spent := 0
	cleanStreak := 0

	for _, e := range allEntries {
		switch e.Kind {
		case "debit":
			if len(e.Extra) > 0 {
				if mid, ok := e.Extra["mandate_id"].(string); ok && mid != "" {
					if mid != m.ID {
						continue
					}
				}
			}
			spent++
			cleanStreak = 0

		case "breaker-clean":
			if len(e.Extra) > 0 {
				if mid, ok := e.Extra["mandate_id"].(string); ok && mid != "" {
					if mid != m.ID {
						continue
					}
				}
			}
			cleanStreak++

		case "gate":
			if e.Status == "PASS" {
				cleanStreak++
			}

		case "run":
			if e.Status == "PASS" && !e.Dirty && e.ItemsChecked > 0 {
				cleanStreak++
			}
		}
	}

	status := BreakerStatus{
		MandateID:             m.ID,
		ConfiguredMaxTier:     configuredMaxTier,
		Spent:                 spent,
		Limit:                 m.ErrorBudget.Limit,
		CleanStreak:           cleanStreak,
		UpgradeStreakRequired: upgradeStreakRequired,
	}

	if spent >= m.ErrorBudget.Limit {
		if cleanStreak >= upgradeStreakRequired {
			status.EffectiveMaxTier = configuredMaxTier
			status.Tripped = false
			status.RestoredByStreak = true
			status.Reason = "tier restored after clean evidenced streak"
		} else {
			status.EffectiveMaxTier = "T0"
			status.Tripped = true
			status.RestoredByStreak = false
			status.Reason = "circuit breaker tripped: error budget exhausted"
		}
	} else {
		status.EffectiveMaxTier = configuredMaxTier
		status.Tripped = false
		status.RestoredByStreak = false
		status.Reason = fmt.Sprintf("circuit breaker nominal: %d/%d budget spent", spent, m.ErrorBudget.Limit)
	}

	return status, nil
}

// ResetCircuitBreaker appends a breaker-reset entry to the global ledger.
func ResetCircuitBreaker(mandateID, reason string) error {
	ledger, err := OpenGlobalLedger()
	if err != nil {
		return fmt.Errorf("opening global ledger: %w", err)
	}

	if mandateID == "" {
		if m, _ := LoadActiveMandate(); m != nil {
			mandateID = m.ID
		}
	}

	extra := map[string]any{
		"reason": reason,
	}
	if mandateID != "" {
		extra["mandate_id"] = mandateID
	}

	entry := &Entry{
		ID:           newEvidenceID(),
		Ts:           time.Now().UTC().Format(time.RFC3339),
		Kind:         "breaker-reset",
		Cmd:          []string{"breaker", "reset"},
		Status:       "PASS",
		ItemsChecked: 1,
		Extra:        extra,
	}

	return ledger.Append(entry)
}

// RecordCleanActionForBreaker appends a breaker-clean entry to the global ledger.
func RecordCleanActionForBreaker(repoPath, action, evidenceID, mandateID string) error {
	ledger, err := OpenGlobalLedger()
	if err != nil {
		return fmt.Errorf("opening global ledger: %w", err)
	}

	if mandateID == "" {
		if m, _ := LoadActiveMandate(); m != nil {
			mandateID = m.ID
		}
	}

	extra := map[string]any{
		"action":      action,
		"evidence_id": evidenceID,
	}
	if mandateID != "" {
		extra["mandate_id"] = mandateID
	}

	entry := &Entry{
		ID:           newEvidenceID(),
		Ts:           time.Now().UTC().Format(time.RFC3339),
		Kind:         "breaker-clean",
		Repo:         repoPath,
		Cmd:          []string{"breaker", "clean", action},
		Status:       "PASS",
		ItemsChecked: 1,
		Extra:        extra,
	}

	return ledger.Append(entry)
}

func breakerCommand(args []string) int {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: f0 breaker <status|reset> [options]\n")
		return 1
	}

	switch args[0] {
	case "status":
		var repoPath string
		var jsonOutput bool
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--repo":
				if i+1 < len(args) {
					repoPath = args[i+1]
					i++
				}
			case "--json":
				jsonOutput = true
			}
		}

		m, err := LoadActiveMandate()
		if err != nil {
			fmt.Fprintf(os.Stderr, "f0 breaker status: error loading mandate: %v\n", err)
			return 1
		}

		st, err := EvaluateCircuitBreaker(m, repoPath, 0)
		if err != nil {
			fmt.Fprintf(os.Stderr, "f0 breaker status: %v\n", err)
			return 1
		}

		if jsonOutput {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(st); err != nil {
				fmt.Fprintf(os.Stderr, "f0 breaker status: encoding json: %v\n", err)
				return 1
			}
			return 0
		}

		fmt.Println("Circuit Breaker Status:")
		fmt.Printf("  Mandate ID:              %s\n", st.MandateID)
		fmt.Printf("  Configured Max Tier:     %s\n", st.ConfiguredMaxTier)
		fmt.Printf("  Effective Max Tier:      %s\n", st.EffectiveMaxTier)
		fmt.Printf("  Spent / Limit:           %d / %d\n", st.Spent, st.Limit)
		fmt.Printf("  Tripped:                 %t\n", st.Tripped)
		fmt.Printf("  Clean Streak:            %d / %d\n", st.CleanStreak, st.UpgradeStreakRequired)
		fmt.Printf("  Restored by Streak:      %t\n", st.RestoredByStreak)
		fmt.Printf("  Reason:                  %s\n", st.Reason)
		return 0

	case "reset":
		var reason string
		for i := 1; i < len(args); i++ {
			if args[i] == "--reason" && i+1 < len(args) {
				reason = args[i+1]
				i++
			}
		}
		if reason == "" {
			fmt.Fprintf(os.Stderr, "f0 breaker reset: --reason <text> is required\n")
			return 1
		}

		m, _ := LoadActiveMandate()
		var mandateID string
		if m != nil {
			mandateID = m.ID
		}
		if err := ResetCircuitBreaker(mandateID, reason); err != nil {
			fmt.Fprintf(os.Stderr, "f0 breaker reset: %v\n", err)
			return 1
		}
		fmt.Printf("[f0] circuit breaker reset: %s (mandate %s)\n", reason, mandateID)
		return 0

	default:
		fmt.Fprintf(os.Stderr, "f0 breaker: unknown subcommand %q\n", args[0])
		return 1
	}
}

func init() {
	commands["breaker"] = breakerCommand
}
