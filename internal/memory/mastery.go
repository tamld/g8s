package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/tamld/g8s/internal/vault"
)

const (
	// ReviewFlagReadyForDoctrine marks an active memory entry that has achieved
	// >= 3 first-pass verified successes and is ready to compound into DOCTRINE (#690).
	ReviewFlagReadyForDoctrine = "READY_FOR_DOCTRINE"

	// ReviewFlagNeedsDialecticalReview marks a memory entry that was present
	// during a failed task attempt (counter-example falsification) so it is
	// reviewed rather than calcifying into dogma (#690).
	ReviewFlagNeedsDialecticalReview = "NEEDS_DIALECTICAL_REVIEW"

	// MinCorroborationsForDoctrine is the minimum number of first-pass verified
	// successes required before an operational memory entry is marked READY_FOR_DOCTRINE.
	MinCorroborationsForDoctrine = 3
)

// OutcomeReport summarizes the result of RecordOutcome (#690).
type OutcomeReport struct {
	SessionID      string         `json:"session_id"`
	PassedFirstTry bool           `json:"passed_first_try"`
	Updated        []*MemoryEntry `json:"updated"`
}

// CompoundOptions configures fallback anchors when compounding READY_FOR_DOCTRINE
// entries into the semantic Knowledge Vault (#690).
type CompoundOptions struct {
	DefaultPackage  string `json:"default_package,omitempty"`
	DefaultFile     string `json:"default_file,omitempty"`
	DefaultSymbol   string `json:"default_symbol,omitempty"`
	DefaultTestFile string `json:"default_test_file,omitempty"`
	DefaultTestCase string `json:"default_test_case,omitempty"`
}

// RecordOutcome closes the experiential application feedback loop (#690:
// "Ứng dụng -> Giỏi hơn -> Trở thành chuyên gia"):
//   - On passedFirstTry=true: increments AppliedCount & FirstPassSuccessCount,
//     records distinct out-of-sample corroborating sessions, bumps Salience (+2),
//     and flags READY_FOR_DOCTRINE once FirstPassSuccessCount >= 3.
//   - On passedFirstTry=false (counter-example): increments AppliedCount &
//     FailureAfterRecallCount, decrements Salience (-2, floored at 0), and flags
//     NEEDS_DIALECTICAL_REVIEW.
func (a *LocalSQLiteMemoryAdapter) RecordOutcome(ctx context.Context, entryIDs []string, sessionID string, passedFirstTry bool) (*OutcomeReport, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	sessionID = strings.TrimSpace(sessionID)
	now := float64(time.Now().UnixNano()) / 1e9
	report := &OutcomeReport{
		SessionID:      sessionID,
		PassedFirstTry: passedFirstTry,
	}

	for _, rawID := range entryIDs {
		id := strings.TrimSpace(rawID)
		if id == "" {
			continue
		}
		e, err := a.getEntryLocked(ctx, id)
		if err != nil {
			return nil, err
		}

		e.AppliedCount++
		e.LastUsedAt = now
		e.UpdatedAt = now

		if passedFirstTry {
			e.FirstPassSuccessCount++
			if sessionID != "" && sessionID != e.SessionID && !containsString(e.CorroboratingSessions, sessionID) {
				e.CorroboratingSessions = append(e.CorroboratingSessions, sessionID)
			}
			e.Salience += 2
			if e.FirstPassSuccessCount >= MinCorroborationsForDoctrine && e.FailureAfterRecallCount == 0 {
				e.ReviewFlag = ReviewFlagReadyForDoctrine
			} else if e.ReviewFlag == ReviewFlagNeedsDialecticalReview && e.FirstPassSuccessCount > e.FailureAfterRecallCount*2 {
				e.ReviewFlag = ""
			}
		} else {
			e.FailureAfterRecallCount++
			e.Salience -= 2
			if e.Salience < 0 {
				e.Salience = 0
			}
			e.ReviewFlag = ReviewFlagNeedsDialecticalReview
		}

		sessJSON := marshalStringSlice(e.CorroboratingSessions)
		if _, err := a.db.ExecContext(ctx, `
			UPDATE memory_entries
			SET salience = ?, last_used_at = ?, applied_count = ?, first_pass_success_count = ?,
			    failure_after_recall_count = ?, corroborating_sessions = ?, review_flag = ?, updated_at = ?
			WHERE id = ?`,
			e.Salience, e.LastUsedAt, e.AppliedCount, e.FirstPassSuccessCount,
			e.FailureAfterRecallCount, sessJSON, e.ReviewFlag, e.UpdatedAt, e.ID); err != nil {
			return nil, fmt.Errorf("memory: update outcome for %s: %w", e.ID, err)
		}
		report.Updated = append(report.Updated, e)
	}

	return report, nil
}

// CompoundReadyEntries promotes all active/distilled memory entries marked
// READY_FOR_DOCTRINE into the self-contained Knowledge Vault as DOCTRINE (#690).
func (a *LocalSQLiteMemoryAdapter) CompoundReadyEntries(ctx context.Context, opts CompoundOptions) ([]*vault.DistillationRecord, error) {
	if a.vault == nil {
		return nil, fmt.Errorf("memory: semantic vault not configured")
	}
	entries, err := a.ListEntries(ctx, MemoryFilter{Lifecycle: LifecycleActive})
	if err != nil {
		return nil, err
	}

	var promoted []*vault.DistillationRecord
	for _, e := range entries {
		if e.ReviewFlag != ReviewFlagReadyForDoctrine {
			continue
		}
		file := strings.TrimSpace(e.Preconditions["file"])
		if file == "" {
			file = strings.TrimSpace(opts.DefaultFile)
		}
		testFile := strings.TrimSpace(e.Preconditions["test_file"])
		if testFile == "" {
			testFile = strings.TrimSpace(opts.DefaultTestFile)
		}
		if file == "" || testFile == "" {
			continue
		}
		pkg := strings.TrimSpace(e.Preconditions["package"])
		if pkg == "" {
			pkg = strings.TrimSpace(opts.DefaultPackage)
		}
		if pkg == "" {
			pkg = filepath.ToSlash(filepath.Dir(file))
		}
		sym := strings.TrimSpace(e.Preconditions["symbol"])
		if sym == "" {
			sym = strings.TrimSpace(opts.DefaultSymbol)
		}
		tc := strings.TrimSpace(e.Preconditions["test_case"])
		if tc == "" {
			tc = strings.TrimSpace(opts.DefaultTestCase)
		}

		rec := vault.DistillationRecord{
			ID:     e.ID,
			Title:  fmt.Sprintf("Compounded Doctrine (%s): %s", e.Kind, truncateTitle(e.Payload, 64)),
			Status: "DOCTRINE",
			Causality: vault.CausalityAnchor{
				Problem:   fmt.Sprintf("Corroborated across %d first-pass verified executions", e.FirstPassSuccessCount),
				TradeOff:  e.Payload,
				RootCause: "Experiential mastery compounding (#690)",
			},
			SpatialCoordinates: vault.SpatialAnchor{
				Package: pkg,
				File:    file,
				Symbol:  sym,
			},
			ForensicVerification: vault.ForensicAnchor{
				TestFile: testFile,
				TestCase: tc,
			},
		}
		stored, err := a.PromoteToVault(ctx, e.ID, rec)
		if err != nil {
			return nil, err
		}
		promoted = append(promoted, stored)
	}

	return promoted, nil
}

func containsString(slice []string, target string) bool {
	for _, s := range slice {
		if s == target {
			return true
		}
	}
	return false
}

func marshalStringSlice(slice []string) string {
	if len(slice) == 0 {
		return "[]"
	}
	b, err := json.Marshal(slice)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func truncateTitle(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
