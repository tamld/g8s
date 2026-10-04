package lessons

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tamld/g8s/internal/telemetry"
)

// setupTestTelemetryEngine creates a real TelemetryEngine backed by an isolated SQLite DB in t.TempDir().
func setupTestTelemetryEngine(t *testing.T) (*telemetry.TelemetryEngine, func()) {
	t.Helper()
	cfg := telemetry.DefaultTelemetryConfig()
	cfg.DBPath = filepath.Join(t.TempDir(), "telemetry.db")
	cfg.BatchSize = 1
	cfg.FlushInterval = 5 * time.Millisecond
	cfg.EnableDistillation = false

	engine, err := telemetry.NewTelemetryEngine(cfg)
	require.NoError(t, err)

	ctx := context.Background()

	// Seed fixture events
	events := []telemetry.TraceEvent{
		{
			ID:        "evt-fix-001",
			TaskID:    "task-worker-101",
			EventType: telemetry.TraceEventTaskFailed,
			Timestamp: time.Now().Add(-10 * time.Minute),
			Payload: map[string]any{
				"package":   "internal/worker",
				"exit_code": 1,
				"error":     "nil pointer dereference in harness",
			},
		},
		{
			ID:        "evt-fix-002",
			TaskID:    "task-worker-101",
			EventType: telemetry.TraceEventReceiptRejected,
			Timestamp: time.Now().Add(-5 * time.Minute),
			Payload: map[string]any{
				"scope":  "read_only",
				"reason": "attempted write to protected file",
			},
		},
		{
			ID:        "evt-fix-003",
			TaskID:    "task-scout-202",
			EventType: telemetry.TraceEventTaskCompleted,
			Timestamp: time.Now().Add(-2 * time.Minute),
			Payload: map[string]any{
				"candidate_count": 5,
				"duration_ms":     1200,
			},
		},
	}

	for _, ev := range events {
		err := engine.IngestEvent(ctx, ev)
		require.NoError(t, err)
	}

	// Verify events are persisted into SQLite and readable via EventsByTask
	require.Eventually(t, func() bool {
		found, err := engine.EventsByTask(ctx, "task-worker-101")
		return err == nil && len(found) == 2
	}, 2*time.Second, 10*time.Millisecond)

	cleanup := func() {
		_ = engine.Close()
	}
	return engine, cleanup
}

func TestTelemetryEngine_EventsByTask(t *testing.T) {
	engine, cleanup := setupTestTelemetryEngine(t)
	defer cleanup()

	ctx := context.Background()

	// Existing task with multiple events (must be ordered chronologically)
	events, err := engine.EventsByTask(ctx, "task-worker-101")
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, "evt-fix-001", events[0].ID)
	assert.Equal(t, "evt-fix-002", events[1].ID)
	assert.True(t, events[0].Timestamp.Before(events[1].Timestamp))

	// Non-existent task ID returns empty slice and nil error
	emptyEvents, err := engine.EventsByTask(ctx, "task-nonexistent-999")
	require.NoError(t, err)
	assert.Empty(t, emptyEvents)
}

func makeValidObservation() Observation {
	return Observation{
		Text: "Observed worker failure due to nil pointer dereference in harness.",
		CitedEvents: []CitedEvent{
			{
				EventID:   "evt-fix-001",
				TaskID:    "task-worker-101",
				EventType: string(telemetry.TraceEventTaskFailed),
				Claim:     "worker exited with nil pointer dereference",
				Snapshot: map[string]any{
					"package":   "internal/worker",
					"exit_code": 1,
				},
			},
		},
	}
}

func TestRT_I_HostileRefusals_And_Acceptance(t *testing.T) {
	engine, cleanup := setupTestTelemetryEngine(t)
	defer cleanup()

	ctx := context.Background()

	tests := []struct {
		name          string
		mutateLesson  func(l *Lesson, ledger *Ledger)
		expectPassed  bool
		expectedCheck string
		expectedMsg   string
	}{
		{
			name: "hostile_fabrication_unregistered_event_id",
			mutateLesson: func(l *Lesson, ledger *Ledger) {
				// Operator's challenge: LLM hallucinates/fabricates a cited event ID that does not exist in telemetry.
				l.Observation.CitedEvents = []CitedEvent{
					{
						EventID:   "evt-hostile-fabrication-666",
						TaskID:    "task-worker-101",
						EventType: string(telemetry.TraceEventTaskFailed),
						Claim:     "hallucinated failure event",
						Snapshot:  map[string]any{"exit_code": 1},
					},
				}
			},
			expectPassed:  false,
			expectedCheck: CheckCitationResolution,
			expectedMsg:   "hostile fabrication: cited event evt-hostile-fabrication-666 does not exist in telemetry for task task-worker-101",
		},
		{
			name: "snapshot_mismatch_payload_value_differs",
			mutateLesson: func(l *Lesson, ledger *Ledger) {
				// Event exists, but snapshot claims exit_code = 99 when actual payload has exit_code = 1
				l.Observation.CitedEvents = []CitedEvent{
					{
						EventID:   "evt-fix-001",
						TaskID:    "task-worker-101",
						EventType: string(telemetry.TraceEventTaskFailed),
						Claim:     "worker crashed with code 99",
						Snapshot: map[string]any{
							"package":   "internal/worker",
							"exit_code": 99,
						},
					},
				}
			},
			expectPassed:  false,
			expectedCheck: CheckCitationResolution,
			expectedMsg:   "snapshot mismatch for event evt-fix-001: key \"exit_code\" expected 99, got 1",
		},
		{
			name: "snapshot_mismatch_missing_key",
			mutateLesson: func(l *Lesson, ledger *Ledger) {
				// Event exists, but snapshot claims a non-existent key
				l.Observation.CitedEvents = []CitedEvent{
					{
						EventID:   "evt-fix-001",
						TaskID:    "task-worker-101",
						EventType: string(telemetry.TraceEventTaskFailed),
						Claim:     "worker failed with non-existent property",
						Snapshot: map[string]any{
							"fabricated_key": "unreal_value",
						},
					},
				}
			},
			expectPassed:  false,
			expectedCheck: CheckCitationResolution,
			expectedMsg:   "snapshot mismatch for event evt-fix-001: payload missing key \"fabricated_key\"",
		},
		{
			name: "event_type_mismatch",
			mutateLesson: func(l *Lesson, ledger *Ledger) {
				// Event exists but has event_type = task_failed; lesson claims task_started
				l.Observation.CitedEvents = []CitedEvent{
					{
						EventID:   "evt-fix-001",
						TaskID:    "task-worker-101",
						EventType: string(telemetry.TraceEventTaskStarted),
						Claim:     "worker started",
						Snapshot:  map[string]any{"package": "internal/worker"},
					},
				}
			},
			expectPassed:  false,
			expectedCheck: CheckCitationResolution,
			expectedMsg:   "event type mismatch for event evt-fix-001",
		},
		{
			name: "empty_citations_refused_no_verifiable_catch",
			mutateLesson: func(l *Lesson, ledger *Ledger) {
				l.Observation.CitedEvents = nil
			},
			expectPassed:  false,
			expectedCheck: CheckCitationResolution,
			expectedMsg:   "no cited events provided: lessons must cite at least one verifiable catch",
		},
		{
			name: "self_review_refused_author_in_reviewed_classes",
			mutateLesson: func(l *Lesson, ledger *Ledger) {
				// T3: author class is "retrospective-worker", which is also in ReviewedClasses
				l.AuthorClass = "retrospective-worker"
				l.ReviewedClasses = []string{"scout", "retrospective-worker", "analyzer"}
			},
			expectPassed:  false,
			expectedCheck: CheckNoSelfReview,
			expectedMsg:   "self-review prohibited: author class \"retrospective-worker\" appears in reviewed classes",
		},
		{
			name: "self_review_refused_case_insensitive",
			mutateLesson: func(l *Lesson, ledger *Ledger) {
				l.AuthorClass = "Worker"
				l.ReviewedClasses = []string{"worker"}
			},
			expectPassed:  false,
			expectedCheck: CheckNoSelfReview,
			expectedMsg:   "self-review prohibited",
		},
		{
			name: "duplicate_citation_set_refused",
			mutateLesson: func(l *Lesson, ledger *Ledger) {
				// Pre-seed an existing lesson with the exact same citation set (both events) into ledger
				seedLesson := NewProposedLesson(
					"les-prior-001",
					"round-prior",
					"retro-auditor",
					[]string{"worker"},
					Observation{
						CitedEvents: []CitedEvent{
							{EventID: "evt-fix-002", TaskID: "task-worker-101"},
							{EventID: "evt-fix-001", TaskID: "task-worker-101"},
						},
					},
					"Recommendation: audit permissions.",
				)
				err := ledger.Append(seedLesson)
				require.NoError(t, err)

				// Candidate lesson cites the same events in reverse order
				l.Observation.CitedEvents = []CitedEvent{
					{
						EventID:   "evt-fix-001",
						TaskID:    "task-worker-101",
						EventType: string(telemetry.TraceEventTaskFailed),
						Claim:     "crashed",
						Snapshot:  map[string]any{"exit_code": 1},
					},
					{
						EventID:   "evt-fix-002",
						TaskID:    "task-worker-101",
						EventType: string(telemetry.TraceEventReceiptRejected),
						Claim:     "receipt rejected",
						Snapshot:  map[string]any{"scope": "read_only"},
					},
				}
			},
			expectPassed:  false,
			expectedCheck: CheckDedup,
			expectedMsg:   "duplicate citation set: hash",
		},
		{
			name: "over_budget_round_refused",
			mutateLesson: func(l *Lesson, ledger *Ledger) {
				l.RoundID = "round-budget-full"
				// Populate ledger up to the cap (3 lessons) for this RoundID
				for i := 1; i <= 3; i++ {
					item := NewProposedLesson(
						filepath.Join("les-budget", string(rune('0'+i))),
						"round-budget-full",
						"retro-auditor",
						[]string{"worker"},
						Observation{
							CitedEvents: []CitedEvent{
								{EventID: filepath.Join("evt-dummy", string(rune('0'+i))), TaskID: "task-worker-101"},
							},
						},
						"A recommendation.",
					)
					err := ledger.Append(item)
					require.NoError(t, err)
				}
			},
			expectPassed:  false,
			expectedCheck: CheckBudget,
			expectedMsg:   "round round-budget-full exceeded budget cap of 3 lessons (found 3 existing)",
		},
		{
			name: "schema_integrity_empty_recommendation",
			mutateLesson: func(l *Lesson, ledger *Ledger) {
				l.Recommendation = ""
			},
			expectPassed:  false,
			expectedCheck: CheckSchemaIntegrity,
			expectedMsg:   "recommendation must not be empty",
		},
		{
			name: "schema_integrity_recommendation_not_llm_opinion",
			mutateLesson: func(l *Lesson, ledger *Ledger) {
				l.RecommendationIsLLMOpinion = false
			},
			expectPassed:  false,
			expectedCheck: CheckSchemaIntegrity,
			expectedMsg:   "recommendation_is_llm_opinion must be true",
		},
		{
			name: "schema_integrity_smuggled_recommendation_in_observation_text",
			mutateLesson: func(l *Lesson, ledger *Ledger) {
				// Marker "recommend" smuggled into observation text
				l.Observation.Text = "We recommend that the harness inject nil guards."
			},
			expectPassed:  false,
			expectedCheck: CheckSchemaIntegrity,
			expectedMsg:   "observation text contains forbidden recommendation marker \"recommend\"",
		},
		{
			name: "schema_integrity_smuggled_recommendation_should_in_claim",
			mutateLesson: func(l *Lesson, ledger *Ledger) {
				// Marker "should" smuggled into cited event claim
				l.Observation.CitedEvents[0].Claim = "Harness should prevent crashes on nil deref"
			},
			expectPassed:  false,
			expectedCheck: CheckSchemaIntegrity,
			expectedMsg:   "claim contains forbidden recommendation marker \"should\"",
		},
		{
			name: "schema_integrity_smuggled_recommendation_propose_in_observation_text",
			mutateLesson: func(l *Lesson, ledger *Ledger) {
				l.Observation.Text = "We propose adding retry logic."
			},
			expectPassed:  false,
			expectedCheck: CheckSchemaIntegrity,
			expectedMsg:   "observation text contains forbidden recommendation marker \"propose\"",
		},
		{
			name: "valid_lesson_accepted",
			mutateLesson: func(l *Lesson, ledger *Ledger) {
				// Clean valid lesson grounded in real fixture
			},
			expectPassed: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ledgerPath := filepath.Join(t.TempDir(), "ledger.jsonl")
			ledger := NewLedger(ledgerPath)

			candidate := NewProposedLesson(
				"les-candidate-001",
				"round-20261004",
				"retrospective",
				[]string{"worker"},
				makeValidObservation(),
				"Add defensive pointer validation prior to calling into worker harness.",
			)

			tc.mutateLesson(&candidate, ledger)

			deps := Dependencies{
				Context:   ctx,
				Telemetry: engine,
				Ledger:    ledger,
				RoundCap:  3,
			}

			verdict := Verify(candidate, deps)

			if tc.expectPassed {
				assert.True(t, verdict.Valid, "expected verdict to be valid")
				assert.Equal(t, StatusProposed, verdict.Status)
				assert.Empty(t, verdict.Reason)

				// Valid lesson must append cleanly to ledger
				err := ledger.Append(verdict.Lesson)
				require.NoError(t, err)

				// Reload and verify snapshots are intact
				loaded, err := ledger.Load()
				require.NoError(t, err)
				require.Len(t, loaded, 1)

				reloaded := loaded[0]
				assert.Equal(t, candidate.ID, reloaded.ID)
				assert.Equal(t, candidate.RoundID, reloaded.RoundID)
				assert.Equal(t, StatusProposed, reloaded.Status)
				require.Len(t, reloaded.Observation.CitedEvents, 1)
				assert.Equal(t, "internal/worker", reloaded.Observation.CitedEvents[0].Snapshot["package"])
			} else {
				assert.False(t, verdict.Valid, "expected verdict to be invalid/rejected")
				assert.Equal(t, StatusRejected, verdict.Status)
				assert.NotEmpty(t, verdict.Reason)
				assert.Contains(t, verdict.Reason, tc.expectedMsg)

				// Failing check must be recorded in Checks
				var foundCheck *CheckResult
				for _, chk := range verdict.Checks {
					if chk.Name == tc.expectedCheck {
						foundCheck = &chk
						break
					}
				}
				require.NotNil(t, foundCheck, "expected failing check %s in checks", tc.expectedCheck)
				assert.False(t, foundCheck.Passed)
				assert.Contains(t, foundCheck.Message, tc.expectedMsg)

				// Rejected lesson must NEVER be appendable to ledger
				err := ledger.Append(verdict.Lesson)
				assert.ErrorIs(t, err, ErrRejectedLesson)
			}
		})
	}
}

func TestLedger_AppendSafeguards(t *testing.T) {
	ledgerPath := filepath.Join(t.TempDir(), "ledger.jsonl")
	ledger := NewLedger(ledgerPath)

	// 1. Rejected lesson refused
	rejectedLesson := Lesson{
		ID:      "les-rej",
		RoundID: "r-1",
		Status:  StatusRejected,
		Observation: Observation{
			CitedEvents: []CitedEvent{{EventID: "evt-1"}},
		},
	}
	err := ledger.Append(rejectedLesson)
	assert.ErrorIs(t, err, ErrRejectedLesson)

	// 2. Empty citation set refused
	emptyCitationLesson := Lesson{
		ID:      "les-empty",
		RoundID: "r-1",
		Status:  StatusProposed,
		Observation: Observation{
			CitedEvents: []CitedEvent{},
		},
	}
	err = ledger.Append(emptyCitationLesson)
	assert.ErrorIs(t, err, ErrEmptyCitationSet)

	// 3. Valid append then duplicate refusal
	validLesson1 := Lesson{
		ID:          "les-1",
		RoundID:     "r-1",
		Status:      StatusProposed,
		AuthorClass: "retro",
		Observation: Observation{
			CitedEvents: []CitedEvent{
				{EventID: "evt-a", TaskID: "t-1"},
				{EventID: "evt-b", TaskID: "t-1"},
			},
		},
		Recommendation:             "recommendation",
		RecommendationIsLLMOpinion: true,
	}
	err = ledger.Append(validLesson1)
	require.NoError(t, err)

	// Re-appending same citation set (even with different ID) must fail
	validLesson2 := Lesson{
		ID:          "les-2",
		RoundID:     "r-2",
		Status:      StatusProposed,
		AuthorClass: "retro-2",
		Observation: Observation{
			CitedEvents: []CitedEvent{
				{EventID: "evt-b", TaskID: "t-1"},
				{EventID: "evt-a", TaskID: "t-1"},
			},
		},
		Recommendation:             "different recommendation",
		RecommendationIsLLMOpinion: true,
	}
	err = ledger.Append(validLesson2)
	assert.ErrorIs(t, err, ErrDuplicateCitationSet)

	// Verify ledger content
	items, err := ledger.List()
	require.NoError(t, err)
	assert.Len(t, items, 1)
	assert.Equal(t, "les-1", items[0].ID)
}
