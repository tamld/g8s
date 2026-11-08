package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// fixtureTasksDB builds a minimal tasks table with the columns the metrics
// queries touch, inserts rows, and returns the read-only handle.
func fixtureTasksDB(t *testing.T, rows []map[string]any) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "g8s.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	schema := `CREATE TABLE tasks (
		task_id TEXT PRIMARY KEY,
		request_json TEXT,
		result_json TEXT,
		attempts INTEGER,
		created_at REAL,
		completed_at REAL
	)`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("create fixture schema: %v", err)
	}
	for i, r := range rows {
		req, _ := json.Marshal(r["request"])
		res, _ := json.Marshal(r["result"])
		created, _ := r["created_at"].(float64)
		completed, _ := r["completed_at"].(float64)
		attempts, _ := r["attempts"].(int)
		if _, err := db.Exec(
			`INSERT INTO tasks (task_id, request_json, result_json, attempts, created_at, completed_at) VALUES (?,?,?,?,?,?)`,
			r["task_id"], string(req), string(res), attempts, created, completed,
		); err != nil {
			t.Fatalf("insert fixture row %d: %v", i, err)
		}
	}
	return db
}

func fixtureRow(id string, req map[string]any, ok bool, attempts int, created, completed float64) map[string]any {
	result := map[string]any{"ok": ok}
	if !ok {
		result["ok"] = false
	}
	return map[string]any{
		"task_id": id, "request": req, "result": result,
		"attempts": attempts, "created_at": created, "completed_at": completed,
	}
}

func TestLoadYieldCountsFiltersAndCounts(t *testing.T) {
	now := float64(time.Now().Unix())
	rows := []map[string]any{
		fixtureRow("t1", map[string]any{"provider": "codex"}, true, 1, now-100, now-90),
		fixtureRow("t2", map[string]any{"provider": "codex"}, true, 1, now-80, now-70),
		fixtureRow("t3", map[string]any{"provider": "codex"}, false, 2, now-60, now-50),
		fixtureRow("t4", map[string]any{"provider": "codex"}, true, 1, now-40, now-30),
	}
	db := fixtureTasksDB(t, rows)
	ctx := context.Background()

	all, err := loadYieldCounts(ctx, db, 0)
	if err != nil {
		t.Fatalf("loadYieldCounts(0): %v", err)
	}
	if all.FinishedTotal != 4 || all.FirstPassOK != 3 {
		t.Fatalf("all-window: got %d/%d, want 4 total 3 first-pass", all.FirstPassOK, all.FinishedTotal)
	}

	since := now - 70
	filtered, err := loadYieldCounts(ctx, db, since)
	if err != nil {
		t.Fatalf("loadYieldCounts(since): %v", err)
	}
	if filtered.FinishedTotal != 3 || filtered.FirstPassOK != 2 {
		t.Fatalf("filtered: got %d/%d, want 3 total 2 first-pass", filtered.FirstPassOK, filtered.FinishedTotal)
	}
}

func TestLoadYieldTrendSpannedAndSingle(t *testing.T) {
	now := time.Now()
	thisWeek := now.Unix() - 3600

	rows := []map[string]any{
		fixtureRow("w1", map[string]any{"provider": "codex"}, true, 1, float64(thisWeek), float64(thisWeek+10)),
		fixtureRow("p1", map[string]any{"provider": "agy"}, false, 2, float64(now.Add(-10*24*time.Hour).Unix()), float64(now.Add(-10*24*time.Hour).Unix()+10)),
		// Anchor older than the prior-week start: spanned requires history
		// reaching before priorStart, while counts only cover the windows.
		fixtureRow("old", map[string]any{"provider": "agy"}, true, 1, float64(now.Add(-20*24*time.Hour).Unix()), float64(now.Add(-20*24*time.Hour).Unix())),
	}
	db := fixtureTasksDB(t, rows)
	ctx := context.Background()

	this, priorW, spanned, err := loadYieldTrend(ctx, db, now)
	if err != nil {
		t.Fatalf("loadYieldTrend: %v", err)
	}
	if !spanned {
		t.Fatalf("two-week data must report spanned")
	}
	if this.FinishedTotal != 1 || priorW.FinishedTotal != 1 {
		t.Fatalf("spanned windows: this=%d prior=%d, want 1/1", this.FinishedTotal, priorW.FinishedTotal)
	}

	// Single-window: drop the prior-week row.
	db2 := fixtureTasksDB(t, rows[:1])
	_, _, spanned2, err := loadYieldTrend(ctx, db2, now)
	if err != nil {
		t.Fatalf("loadYieldTrend single: %v", err)
	}
	if spanned2 {
		t.Fatalf("one-week data must NOT report spanned")
	}
}

func TestLoadLaneRowsGroupsAndRate(t *testing.T) {
	now := float64(time.Now().Unix())
	rows := []map[string]any{
		fixtureRow("a", map[string]any{"provider": "codex", "model": "gpt-6.1-sol", "effort": "high", "role": "implementer"}, true, 1, now, now),
		fixtureRow("b", map[string]any{"provider": "codex", "model": "gpt-6.1-sol", "effort": "high", "role": "implementer"}, false, 2, now, now),
		fixtureRow("c", map[string]any{"provider": "cursor", "model": "composer-2.5", "role": "implementer"}, true, 1, now, now),
		fixtureRow("d", map[string]any{"provider": "  ", "model": "", "role": ""}, true, 1, now, now),
	}
	db := fixtureTasksDB(t, rows)
	lanes, err := loadLaneRows(context.Background(), db, 0)
	if err != nil {
		t.Fatalf("loadLaneRows: %v", err)
	}
	if len(lanes) != 3 {
		t.Fatalf("got %d lane rows, want 3 (codex, cursor, unknown-fallback)", len(lanes))
	}
	for _, l := range lanes {
		switch l.Provider {
		case "codex":
			if l.Count != 2 || l.OKCount != 1 {
				t.Fatalf("codex lane: count=%d ok=%d, want 2/1", l.Count, l.OKCount)
			}
			if l.OKRateText != "50.00%" {
				t.Fatalf("codex ok-rate text: %q", l.OKRateText)
			}
		case "cursor":
			if l.Model != "composer-2.5" || l.Effort != "(unknown)" {
				t.Fatalf("cursor lane defaults: %+v", l)
			}
		case "(unknown)":
			// blank provider falls back to the unknown bucket
		default:
			t.Fatalf("unexpected provider %q", l.Provider)
		}
	}
}

func TestLoadJevCountsRoutesAndFallbacks(t *testing.T) {
	now := float64(time.Now().Unix())
	mk := func(route any) map[string]any { return map[string]any{"route_source": route} }
	rows := []map[string]any{
		fixtureRow("j1", mk("jev"), true, 1, now, now),
		fixtureRow("j2", mk("deterministic"), true, 1, now, now),
		fixtureRow("j3", mk(nil), true, 1, now, now),
	}

	db := fixtureTasksDB(t, rows)
	stateDir := t.TempDir() // no telemetry files
	c, err := loadJevCounts(context.Background(), db, stateDir, 0)
	if err != nil {
		t.Fatalf("loadJevCounts: %v", err)
	}
	if !c.Measurable || c.JevAssisted != 1 || c.Deterministic != 1 || c.ManualExplicit != 1 || c.Total != 3 {
		t.Fatalf("route_source present: %+v", c)
	}

	// Absent route_source, no telemetry file -> manual_explicit via absence.
	rows2 := []map[string]any{
		fixtureRow("m1", map[string]any{"provider": "codex"}, true, 1, now, now),
		fixtureRow("m2", map[string]any{"provider": "codex"}, true, 1, now, now),
	}
	db2 := fixtureTasksDB(t, rows2)
	c2, err := loadJevCounts(context.Background(), db2, t.TempDir(), 0)
	if err != nil {
		t.Fatalf("loadJevCounts absent: %v", err)
	}
	if !c2.Measurable || c2.ManualExplicit != 2 || c2.Total != 2 {
		t.Fatalf("route_source absent: %+v", c2)
	}

	// Absent everywhere (no rows, no telemetry) -> unmeasurable, honest note.
	db3 := fixtureTasksDB(t, nil)
	c3, err := loadJevCounts(context.Background(), db3, t.TempDir(), 0)
	if err != nil {
		t.Fatalf("loadJevCounts empty: %v", err)
	}
	if c3.Measurable || c3.Note == "" {
		t.Fatalf("empty state must be unmeasurable with a note: %+v", c3)
	}
}

func TestLoadJevCountsRoutingTelemetryFallback(t *testing.T) {
	now := float64(time.Now().Unix())
	db := fixtureTasksDB(t, []map[string]any{
		fixtureRow("x", map[string]any{"provider": "codex"}, true, 1, now, now),
	})
	stateDir := t.TempDir()
	tele := `{"router_mode":"jev_assisted","recorded_at":"2026-11-07T10:00:00Z"}` + "\n" +
		`{"route_source":"deterministic","recorded_at":"2026-11-07T11:00:00Z"}` + "\n" +
		"not-json-at-all\n"
	if err := os.WriteFile(filepath.Join(stateDir, "routing_telemetry.jsonl"), []byte(tele), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := loadJevCounts(context.Background(), db, stateDir, 0)
	if err != nil {
		t.Fatalf("loadJevCounts telemetry: %v", err)
	}
	if !c.Measurable || c.JevAssisted != 1 || c.Deterministic != 1 {
		t.Fatalf("telemetry fallback: %+v", c)
	}
	if c.Note == "" || !strings.Contains(c.Note, "routing_telemetry.jsonl") {
		t.Fatalf("telemetry note: %q", c.Note)
	}

	// since filter drops the older record entirely.
	old, err := loadJevCounts(context.Background(), db, stateDir, float64(time.Now().Add(-time.Hour).Unix())+1)
	if err != nil {
		t.Fatalf("loadJevCounts since: %v", err)
	}
	_ = old
}

func TestScanRoutingTelemetryFileMalformedAndSince(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "routing_events.jsonl")
	tele := `{"route_source":"jev","recorded_at":"2026-11-01T00:00:00Z"}` + "\n" +
		`{"route_source":"jev","recorded_at":"2026-11-07T00:00:00Z"}` + "\n" +
		"{broken\n"
	if err := os.WriteFile(p, []byte(tele), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := scanRoutingTelemetryFile(p, 0)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if c.JevAssisted != 2 {
		t.Fatalf("no-since scan: %+v", c)
	}
	since := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC).Unix()
	c2, err := scanRoutingTelemetryFile(p, float64(since))
	if err != nil {
		t.Fatalf("scan since: %v", err)
	}
	if c2.JevAssisted != 1 {
		t.Fatalf("since-filtered scan: %+v", c2)
	}
}

func TestTasksTablePresentAndOpenRO(t *testing.T) {
	dir := t.TempDir()
	if _, err := openTasksDBRO(filepath.Join(dir, "absent.db")); err == nil {
		t.Fatal("openTasksDBRO must fail on a missing db")
	}
	db := fixtureTasksDB(t, nil)
	if !tasksTablePresent(context.Background(), db) {
		t.Fatal("tasks table must be present in fixture")
	}
	noTable, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "empty.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = noTable.Close() }()
	if tasksTablePresent(context.Background(), noTable) {
		t.Fatal("tasks table absent in a bare db")
	}
}

func TestConventionAnchorUnixInjectableGit(t *testing.T) {
	if _, _, note, err := conventionAnchorUnix("v1", true, func(...string) (string, error) { return "", nil }); err != nil || note == "" {
		t.Fatalf("baseline: %v %q", err, note)
	}
	missing := func(args ...string) (string, error) {
		if strings.Contains(strings.Join(args, " "), "rev-parse") {
			return "", os.ErrNotExist
		}
		return "", nil
	}
	if _, _, _, err := conventionAnchorUnix("v1", false, missing); err == nil {
		t.Fatal("unresolvable ref must error")
	}
}

func TestRenderMetricsSectionsEndToEnd(t *testing.T) {
	now := time.Now()
	stateDir := t.TempDir()
	t.Setenv("G8S_STATE_DIR", stateDir)

	// Fixture db directly under stateDir so resolveMetricsDB finds it.
	db, err := sql.Open("sqlite", filepath.Join(stateDir, "g8s.db"))
	if err != nil {
		t.Fatal(err)
	}
	schema := `CREATE TABLE tasks (
		task_id TEXT PRIMARY KEY,
		request_json TEXT,
		result_json TEXT,
		attempts INTEGER,
		created_at REAL,
		completed_at REAL
	)`
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{
		fixtureRow("r1", map[string]any{"provider": "codex", "model": "gpt-6.1-sol", "effort": "high", "role": "implementer"}, true, 1, float64(now.Add(-24*time.Hour).Unix()), float64(now.Add(-23*time.Hour).Unix())),
		fixtureRow("r2", map[string]any{"provider": "cursor", "model": "composer-2.5", "effort": "medium", "role": "implementer", "route_source": "jev"}, true, 1, float64(now.Add(-2*time.Hour).Unix()), float64(now.Add(-1*time.Hour).Unix())),
	}
	for i, r := range rows {
		req, _ := json.Marshal(r["request"])
		res, _ := json.Marshal(r["result"])
		created, _ := r["created_at"].(float64)
		completed, _ := r["completed_at"].(float64)
		attempts, _ := r["attempts"].(int)
		if _, err := db.Exec(`INSERT INTO tasks (task_id, request_json, result_json, attempts, created_at, completed_at) VALUES (?,?,?,?,?,?)`,
			r["task_id"], string(req), string(res), attempts, created, completed); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	anchorTS := fmt.Sprintf("%d", now.Add(-48*time.Hour).Unix())
	fakeGit := func(args ...string) (string, error) {
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "rev-parse"):
			return "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", nil
		case strings.Contains(joined, "--format=%ct"):
			return anchorTS, nil
		case strings.Contains(joined, "--diff-filter=A"):
			return "cccccccccccccccccccccccccccccccccccccccc", nil
		default:
			return "", nil
		}
	}

	out, err := renderMetricsSections("v1", false, fakeGit)
	if err != nil {
		t.Fatalf("renderMetricsSections: %v", err)
	}
	for _, want := range []string{"### YIELD", "### LANES", "### JEV", "jev adoption", "Window: tasks since convention anchor"} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered output missing %q:\n%s", want, out)
		}
	}
}
