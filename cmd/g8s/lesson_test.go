package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/lessons"
	"github.com/tamld/g8s/internal/telemetry"
)

func setupTestTelemetryDB(t *testing.T) (string, func()) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "telemetry.db")
	cfg := telemetry.DefaultTelemetryConfig()
	cfg.DBPath = dbPath
	cfg.BatchSize = 1
	cfg.FlushInterval = 5 * time.Millisecond
	cfg.EnableDistillation = false

	engine, err := telemetry.NewTelemetryEngine(cfg)
	require.NoError(t, err)

	ctx := context.Background()
	ev := telemetry.TraceEvent{
		ID:        "evt-fix-001",
		TaskID:    "task-worker-101",
		EventType: telemetry.TraceEventTaskFailed,
		Timestamp: time.Now().Add(-10 * time.Minute),
		Payload: map[string]any{
			"package":   "internal/worker",
			"exit_code": 1,
		},
	}
	err = engine.IngestEvent(ctx, ev)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		found, err := engine.EventsByTask(ctx, "task-worker-101")
		return err == nil && len(found) == 1
	}, 2*time.Second, 10*time.Millisecond)

	cleanup := func() {
		_ = engine.Close()
	}
	return dbPath, cleanup
}

func makeValidLessonDraftJSON(eventID, taskID string) string {
	l := map[string]any{
		"round_id":         "round-20261004",
		"author_class":     "scout",
		"reviewed_classes": []string{"worker"},
		"observation": map[string]any{
			"text": "Observed worker failure due to nil pointer dereference in harness.",
			"cited_events": []map[string]any{
				{
					"event_id":   eventID,
					"task_id":    taskID,
					"event_type": string(telemetry.TraceEventTaskFailed),
					"claim":      "worker exited with nil pointer dereference",
					"snapshot": map[string]any{
						"package":   "internal/worker",
						"exit_code": 1,
					},
				},
			},
		},
		"recommendation":                "Add defensive pointer validation prior to calling into worker harness.",
		"recommendation_is_llm_opinion": true,
	}
	b, _ := json.Marshal(l)
	return string(b)
}

func writeTempDraft(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "draft.json")
	err := os.WriteFile(p, []byte(content), 0o600)
	require.NoError(t, err)
	return p
}

func TestLessonCLI_CreatePassAppends(t *testing.T) {
	dbPath, cleanup := setupTestTelemetryDB(t)
	defer cleanup()

	ledgerPath := filepath.Join(t.TempDir(), "ledger.jsonl")
	draftPath := writeTempDraft(t, makeValidLessonDraftJSON("evt-fix-001", "task-worker-101"))

	var stdout, stderr bytes.Buffer
	code := runLessonWithIO([]string{
		"create",
		"--file", draftPath,
		"--db", dbPath,
		"--ledger", ledgerPath,
		"--json",
	}, &stdout, &stderr)

	require.Equal(t, 0, code, "expected exit code 0 on pass; stderr: %s", stderr.String())

	var env cli.Envelope
	err := json.Unmarshal(stdout.Bytes(), &env)
	require.NoError(t, err, "failed to unmarshal stdout envelope: %s", stdout.String())
	assert.Equal(t, "verdict", env.Kind)
	assert.Equal(t, "lesson", env.Command)
	assert.Equal(t, "create", env.Subcommand)

	vBytes, _ := json.Marshal(env.Data)
	var v lessons.Verdict
	err = json.Unmarshal(vBytes, &v)
	require.NoError(t, err)
	assert.True(t, v.Valid)
	assert.Equal(t, lessons.StatusProposed, v.Status)
	assert.NotEmpty(t, v.Lesson.ID)

	// Verify ledger file has been appended to
	ledger := lessons.NewLedger(ledgerPath)
	loaded, err := ledger.List()
	require.NoError(t, err)
	require.Len(t, loaded, 1)
	assert.Equal(t, v.Lesson.ID, loaded[0].ID)
	assert.Equal(t, "round-20261004", loaded[0].RoundID)
	assert.Equal(t, "scout", loaded[0].AuthorClass)
}

func TestLessonCLI_CreateFailFabricatedEventExitsNonZeroAndAppendsNothing(t *testing.T) {
	dbPath, cleanup := setupTestTelemetryDB(t)
	defer cleanup()

	ledgerPath := filepath.Join(t.TempDir(), "ledger.jsonl")
	// Fabricated event ID not in telemetry DB
	draftPath := writeTempDraft(t, makeValidLessonDraftJSON("evt-fake-hallucination-999", "task-worker-101"))

	var stdout, stderr bytes.Buffer
	code := runLessonWithIO([]string{
		"create",
		"--file", draftPath,
		"--db", dbPath,
		"--ledger", ledgerPath,
		"--json",
	}, &stdout, &stderr)

	require.NotEqual(t, 0, code, "expected non-zero exit code on failure")

	// Parse verdict from stdout
	var env cli.Envelope
	err := json.Unmarshal(stdout.Bytes(), &env)
	require.NoError(t, err, "failed to unmarshal stdout envelope: %s", stdout.String())
	assert.Equal(t, "verdict", env.Kind)
	assert.Equal(t, "lesson", env.Command)
	assert.Equal(t, "create", env.Subcommand)

	vBytes, _ := json.Marshal(env.Data)
	var v lessons.Verdict
	err = json.Unmarshal(vBytes, &v)
	require.NoError(t, err)
	assert.False(t, v.Valid)
	assert.Equal(t, lessons.StatusRejected, v.Status)
	assert.Contains(t, v.Reason, "hostile fabrication")

	// Verify ledger appends NOTHING
	ledger := lessons.NewLedger(ledgerPath)
	loaded, err := ledger.List()
	require.NoError(t, err)
	assert.Empty(t, loaded, "ledger must remain completely empty after failed check")
}

func TestLessonCLI_VerifyDryRunNeverAppends(t *testing.T) {
	dbPath, cleanup := setupTestTelemetryDB(t)
	defer cleanup()

	ledgerPath := filepath.Join(t.TempDir(), "ledger.jsonl")

	// 1. Valid lesson on verify: passes but appends nothing
	draftPassPath := writeTempDraft(t, makeValidLessonDraftJSON("evt-fix-001", "task-worker-101"))

	var stdout, stderr bytes.Buffer
	code := runLessonWithIO([]string{
		"verify",
		"--file", draftPassPath,
		"--db", dbPath,
		"--ledger", ledgerPath,
		"--json",
	}, &stdout, &stderr)

	require.Equal(t, 0, code, "verify should pass for valid draft; stderr: %s", stderr.String())

	var env cli.Envelope
	err := json.Unmarshal(stdout.Bytes(), &env)
	require.NoError(t, err)
	assert.Equal(t, "verdict", env.Kind)
	assert.Equal(t, "lesson", env.Command)
	assert.Equal(t, "verify", env.Subcommand)

	vBytes, _ := json.Marshal(env.Data)
	var v lessons.Verdict
	err = json.Unmarshal(vBytes, &v)
	require.NoError(t, err)
	assert.True(t, v.Valid)

	// Ledger MUST remain empty (dry run)
	ledger := lessons.NewLedger(ledgerPath)
	loaded, err := ledger.List()
	require.NoError(t, err)
	assert.Empty(t, loaded, "verify must never append to ledger on pass")

	// 2. Hostile / failing lesson on verify: fails and appends nothing
	stdout.Reset()
	stderr.Reset()
	draftFailPath := writeTempDraft(t, makeValidLessonDraftJSON("evt-fake-hallucination-999", "task-worker-101"))

	code = runLessonWithIO([]string{
		"verify",
		"--file", draftFailPath,
		"--db", dbPath,
		"--ledger", ledgerPath,
		"--json",
	}, &stdout, &stderr)

	require.NotEqual(t, 0, code, "verify should exit non-zero for invalid draft")

	loaded, err = ledger.List()
	require.NoError(t, err)
	assert.Empty(t, loaded, "verify must never append to ledger on fail")
}

func TestLessonCLI_ListRoundTrips(t *testing.T) {
	ledgerPath := filepath.Join(t.TempDir(), "ledger.jsonl")
	ledger := lessons.NewLedger(ledgerPath)

	lesson1 := lessons.NewProposedLesson(
		"les-001",
		"round-101",
		"scout",
		[]string{"worker"},
		lessons.Observation{
			Text: "Observed failure 1",
			CitedEvents: []lessons.CitedEvent{
				{EventID: "evt-1", TaskID: "t-1"},
			},
		},
		"Recommendation 1",
	)
	lesson1.Status = lessons.StatusProposed
	err := ledger.Append(lesson1)
	require.NoError(t, err)

	lesson2 := lessons.NewProposedLesson(
		"les-002",
		"round-102",
		"analyzer",
		[]string{"worker"},
		lessons.Observation{
			Text: "Observed failure 2",
			CitedEvents: []lessons.CitedEvent{
				{EventID: "evt-2", TaskID: "t-2"},
			},
		},
		"Recommendation 2",
	)
	lesson2.Status = lessons.StatusRatified
	err = ledger.Append(lesson2)
	require.NoError(t, err)

	// 1. List all with JSON envelope
	var stdout, stderr bytes.Buffer
	code := runLessonWithIO([]string{
		"list",
		"--ledger", ledgerPath,
		"--json",
	}, &stdout, &stderr)
	require.Equal(t, 0, code)

	var env cli.Envelope
	err = json.Unmarshal(stdout.Bytes(), &env)
	require.NoError(t, err)
	assert.Equal(t, "lessons", env.Kind)
	assert.Equal(t, "lesson", env.Command)
	assert.Equal(t, "list", env.Subcommand)

	var items []lessons.Lesson
	dBytes, _ := json.Marshal(env.Data)
	err = json.Unmarshal(dBytes, &items)
	require.NoError(t, err)
	require.Len(t, items, 2)
	assert.Equal(t, "les-001", items[0].ID)
	assert.Equal(t, "les-002", items[1].ID)

	// 2. Filter by status=ratified
	stdout.Reset()
	stderr.Reset()
	code = runLessonWithIO([]string{
		"list",
		"--ledger", ledgerPath,
		"--status", "ratified",
		"--json",
	}, &stdout, &stderr)
	require.Equal(t, 0, code)

	err = json.Unmarshal(stdout.Bytes(), &env)
	require.NoError(t, err)
	dBytes, _ = json.Marshal(env.Data)
	err = json.Unmarshal(dBytes, &items)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "les-002", items[0].ID)
	assert.Equal(t, lessons.StatusRatified, items[0].Status)

	// 3. Filter by status=proposed
	stdout.Reset()
	stderr.Reset()
	code = runLessonWithIO([]string{
		"list",
		"--ledger", ledgerPath,
		"--status", "proposed",
		"--json",
	}, &stdout, &stderr)
	require.Equal(t, 0, code)

	err = json.Unmarshal(stdout.Bytes(), &env)
	require.NoError(t, err)
	dBytes, _ = json.Marshal(env.Data)
	err = json.Unmarshal(dBytes, &items)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "les-001", items[0].ID)

	// 4. Raw JSONL passthrough
	stdout.Reset()
	stderr.Reset()
	code = runLessonWithIO([]string{
		"list",
		"--ledger", ledgerPath,
		"--passthrough",
	}, &stdout, &stderr)
	require.Equal(t, 0, code)
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	require.Len(t, lines, 2)

	// 5. Compact table output
	stdout.Reset()
	stderr.Reset()
	code = runLessonWithIO([]string{
		"list",
		"--ledger", ledgerPath,
		"--json=false",
	}, &stdout, &stderr)
	require.Equal(t, 0, code)
	tableOut := stdout.String()
	assert.Contains(t, tableOut, "LESSON ID")
	assert.Contains(t, tableOut, "les-001")
	assert.Contains(t, tableOut, "les-002")
}

func TestLessonCLI_MainRegistrationRoutes(t *testing.T) {
	// Assert in-process routing for help
	var stdout, stderr bytes.Buffer
	code := runLessonWithIO([]string{"help"}, &stdout, &stderr)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout.String(), "g8s lesson <create|verify|list>")

	// Assert in-process routing for unknown subcommand
	stdout.Reset()
	stderr.Reset()
	code = runLessonWithIO([]string{"invalid-subcmd"}, &stdout, &stderr)
	assert.Equal(t, 2, code)

	// Verify main.go source file contains one-line switch routing for lesson
	mainContent, err := os.ReadFile("main.go")
	require.NoError(t, err)
	mainStr := string(mainContent)

	assert.Contains(t, mainStr, "case \"lesson\":")
	assert.Contains(t, mainStr, "runLesson(os.Args[2:])")
}

func TestLessonCLI_FlagOverrides(t *testing.T) {
	dbPath, cleanup := setupTestTelemetryDB(t)
	defer cleanup()

	ledgerPath := filepath.Join(t.TempDir(), "ledger.jsonl")
	draftPath := writeTempDraft(t, makeValidLessonDraftJSON("evt-fix-001", "task-worker-101"))

	var stdout, stderr bytes.Buffer
	code := runLessonWithIO([]string{
		"create",
		"--file", draftPath,
		"--db", dbPath,
		"--ledger", ledgerPath,
		"--round", "round-override-999",
		"--author-class", "custom-auditor",
		"--json",
	}, &stdout, &stderr)

	require.Equal(t, 0, code)

	ledger := lessons.NewLedger(ledgerPath)
	loaded, err := ledger.List()
	require.NoError(t, err)
	require.Len(t, loaded, 1)
	assert.Equal(t, "round-override-999", loaded[0].RoundID)
	assert.Equal(t, "custom-auditor", loaded[0].AuthorClass)
}

func TestLessonCLI_UsageErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer

	// 1. No arguments
	code := runLessonWithIO([]string{}, &stdout, &stderr)
	assert.Equal(t, 2, code)

	// 2. create missing --file
	stdout.Reset()
	stderr.Reset()
	code = runLessonWithIO([]string{"create"}, &stdout, &stderr)
	assert.Equal(t, 2, code)

	// 3. verify missing --file
	stdout.Reset()
	stderr.Reset()
	code = runLessonWithIO([]string{"verify"}, &stdout, &stderr)
	assert.Equal(t, 2, code)

	// 4. Non-existent draft file
	stdout.Reset()
	stderr.Reset()
	code = runLessonWithIO([]string{"create", "--file", "nonexistent-draft.json", "--json"}, &stdout, &stderr)
	assert.Equal(t, 1, code)

	// 5. Corrupt JSON in draft file
	corruptPath := writeTempDraft(t, "{not valid json}")
	stdout.Reset()
	stderr.Reset()
	code = runLessonWithIO([]string{"create", "--file", corruptPath, "--json"}, &stdout, &stderr)
	assert.Equal(t, 1, code)
}
