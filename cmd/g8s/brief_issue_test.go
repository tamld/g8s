package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/pathutil"
	"github.com/tamld/g8s/internal/telemetry"
	_ "modernc.org/sqlite"
)

// TestQueryTopPatterns_ExcludesDraftAndLowConfidenceBeforeLimit reproduces the scenario from #633:
// Preflight fallback in brief_issue must exclude DRAFT and low-confidence (<0.5) patterns before limit.
func TestQueryTopPatterns_ExcludesDraftAndLowConfidenceBeforeLimit(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "telemetry.db")
	t.Setenv("G8S_TELEMETRY_DB", dbPath)
	t.Setenv("G8S_TELEMETRY", "1")

	cfg := telemetry.DefaultTelemetryConfig()
	cfg.DBPath = dbPath
	eng, err := telemetry.NewTelemetryEngine(cfg)
	require.NoError(t, err)
	defer eng.Close()

	ctx := context.Background()
	now := time.Now().UnixNano()

	dsn := pathutil.SQLiteURI(dbPath, "_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)")
	db, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	defer db.Close()

	seedPattern := func(id string, occ int, conf float64, status string) {
		pkg, _ := json.Marshal([]string{"internal/unrelated"})
		_, err := db.Exec(`INSERT INTO negative_patterns
			(id, pattern_type, title, description, root_cause, remediation, occurrence_count, first_seen, last_seen, affected_packages, example_contexts, confidence_score, status)
			VALUES (?, 'timeout', ?, 'desc', 'root cause', 'remediate', ?, ?, ?, ?, ?, ?, ?)`,
			id, id, occ, now, now, pkg, pkg, conf, status)
		require.NoError(t, err)
	}

	seedPattern("draft-high", 100, 0.99, "DRAFT")
	seedPattern("validated-low-confidence", 90, 0.01, "VALIDATED")
	seedPattern("validated-ok", 4, 0.8, "VALIDATED")
	seedPattern("applied-ok", 2, 0.8, "APPLIED")

	patterns, err := eng.QueryTopPatterns(ctx, 3)
	require.NoError(t, err)
	require.Len(t, patterns, 2)
	assert.Equal(t, "validated-ok", patterns[0].ID)
	assert.Equal(t, "applied-ok", patterns[1].ID)

	// Preflight fallback with all 4 seeded patterns: only validated-ok and applied-ok are injected
	section := preflightSection("Unrelated Task Title", "Unrelated task payload")
	assert.NotEmpty(t, section)
	assert.Contains(t, section, "validated-ok")
	assert.Contains(t, section, "applied-ok")
	assert.NotContains(t, section, "draft-high")
	assert.NotContains(t, section, "validated-low-confidence")

	// Delete eligible patterns: only DRAFT and low-confidence patterns exist in the DB
	_, err = db.Exec(`DELETE FROM negative_patterns WHERE id IN ('validated-ok', 'applied-ok')`)
	require.NoError(t, err)

	patternsEmpty, err := eng.QueryTopPatterns(ctx, 3)
	require.NoError(t, err)
	assert.Empty(t, patternsEmpty)

	// When only DRAFT and low-confidence patterns exist in the DB, preflightSection returns ""
	sectionEmpty := preflightSection("Unrelated Task Title", "Unrelated task payload")
	assert.Equal(t, "", sectionEmpty, "preflightSection must return empty string when only draft/low-confidence patterns exist")
}

// TestBriefIssue_PreflightFallbackExcludesIneligible verifies that executeBriefIssue
// does not inject DRAFT or low-confidence patterns into the issued brief payload.
func TestBriefIssue_PreflightFallbackExcludesIneligible(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "g8s-brief-test.db")
	t.Setenv("G8S_DB", dbPath)
	store, err := controlplane.NewControlPlane(dbPath, nil)
	require.NoError(t, err)
	defer store.Close()

	telemDBPath := filepath.Join(tempDir, "telemetry.db")
	t.Setenv("G8S_TELEMETRY_DB", telemDBPath)
	t.Setenv("G8S_TELEMETRY", "1")

	cfg := telemetry.DefaultTelemetryConfig()
	cfg.DBPath = telemDBPath
	eng, err := telemetry.NewTelemetryEngine(cfg)
	require.NoError(t, err)
	defer eng.Close()

	dsn := pathutil.SQLiteURI(telemDBPath, "_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)")
	db, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	defer db.Close()

	now := time.Now().UnixNano()
	pkg, _ := json.Marshal([]string{"internal/unrelated"})
	_, err = db.Exec(`INSERT INTO negative_patterns
		(id, pattern_type, title, description, root_cause, remediation, occurrence_count, first_seen, last_seen, affected_packages, example_contexts, confidence_score, status)
		VALUES ('draft-high', 'timeout', 'Draft High Title', 'desc', 'root cause', 'remediate', 100, ?, ?, ?, ?, 0.99, 'DRAFT')`,
		now, now, pkg, pkg)
	require.NoError(t, err)

	var buf bytes.Buffer
	b, err := executeBriefIssue(&buf, store, "Unrelated Brief Title", "Payload content", "- [x] DoD item", "actor-test", 2*time.Hour)
	require.NoError(t, err)
	assert.NotContains(t, b.PayloadMD, "Draft High Title")
	assert.NotContains(t, b.PayloadMD, "Relevant Failure Patterns")
}
