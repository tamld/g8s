package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tamld/g8s/internal/pathutil"
	_ "modernc.org/sqlite"
)

type metricsDBInfo struct {
	StateDir string
	DBPath   string
}

// resolveMetricsDB picks G8S_STATE_DIR when set, else pathutil default; reports both when they differ.
func resolveMetricsDB() (primary metricsDBInfo, alt *metricsDBInfo) {
	envDir := strings.TrimSpace(os.Getenv("G8S_STATE_DIR"))
	defDir := pathutil.DefaultStateDir()
	pick := defDir
	label := "default state dir (pathutil)"
	if envDir != "" {
		pick = envDir
		label = "G8S_STATE_DIR"
	}
	primary = metricsDBInfo{
		StateDir: pick + " [" + label + "]",
		DBPath:   filepath.Join(pick, "g8s.db"),
	}
	if envDir != "" && defDir != "" && filepath.Clean(envDir) != filepath.Clean(defDir) {
		alt = &metricsDBInfo{
			StateDir: defDir + " [pathutil default]",
			DBPath:   filepath.Join(defDir, "g8s.db"),
		}
	}
	return primary, alt
}

func openTasksDBRO(dbPath string) (*sql.DB, error) {
	if st, err := os.Stat(dbPath); err != nil || st.IsDir() {
		return nil, fmt.Errorf("tasks database missing at %s", dbPath)
	}
	dsn := pathutil.SQLiteURI(dbPath, "mode=ro")
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func tasksTablePresent(ctx context.Context, db *sql.DB) bool {
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='tasks'`).Scan(&n)
	return err == nil && n > 0
}

func loadYieldCounts(ctx context.Context, db *sql.DB, sinceUnix float64) (YieldCounts, error) {
	var c YieldCounts
	err := db.QueryRowContext(ctx, `
SELECT COUNT(*),
       COALESCE(SUM(CASE WHEN attempts = 1 AND json_extract(result_json,'$.ok') = 1 THEN 1 ELSE 0 END), 0)
FROM tasks
WHERE completed_at IS NOT NULL
  AND result_json IS NOT NULL
  AND TRIM(result_json) != ''
  AND (? <= 0 OR completed_at >= ?)`,
		sinceUnix, sinceUnix,
	).Scan(&c.FinishedTotal, &c.FirstPassOK)
	return c, err
}

func loadYieldTrend(ctx context.Context, db *sql.DB, now time.Time) (thisWeek, priorWeek YieldCounts, spanned bool, err error) {
	end := now.UTC()
	thisStart := end.Add(-7 * 24 * time.Hour).Unix()
	priorStart := end.Add(-14 * 24 * time.Hour).Unix()
	priorEnd := thisStart

	var earliest sql.NullFloat64
	if qErr := db.QueryRowContext(ctx, `
SELECT MIN(completed_at) FROM tasks
WHERE completed_at IS NOT NULL AND result_json IS NOT NULL AND TRIM(result_json) != ''`).Scan(&earliest); qErr != nil {
		err = qErr
		return
	}
	if earliest.Valid && earliest.Float64 > float64(priorStart) {
		spanned = false
	} else if earliest.Valid {
		spanned = true
	}

	err = db.QueryRowContext(ctx, `
SELECT COUNT(*),
       COALESCE(SUM(CASE WHEN attempts = 1 AND json_extract(result_json,'$.ok') = 1 THEN 1 ELSE 0 END), 0)
FROM tasks
WHERE completed_at IS NOT NULL
  AND result_json IS NOT NULL
  AND TRIM(result_json) != ''
  AND completed_at >= ? AND completed_at < ?`,
		float64(thisStart), float64(end.Unix()),
	).Scan(&thisWeek.FinishedTotal, &thisWeek.FirstPassOK)
	if err != nil {
		return
	}
	if !spanned {
		return
	}
	err = db.QueryRowContext(ctx, `
SELECT COUNT(*),
       COALESCE(SUM(CASE WHEN attempts = 1 AND json_extract(result_json,'$.ok') = 1 THEN 1 ELSE 0 END), 0)
FROM tasks
WHERE completed_at IS NOT NULL
  AND result_json IS NOT NULL
  AND TRIM(result_json) != ''
  AND completed_at >= ? AND completed_at < ?`,
		float64(priorStart), float64(priorEnd),
	).Scan(&priorWeek.FinishedTotal, &priorWeek.FirstPassOK)
	return
}

func loadLaneRows(ctx context.Context, db *sql.DB, sinceUnix float64) ([]LaneRow, error) {
	rows, err := db.QueryContext(ctx, `
SELECT COALESCE(NULLIF(TRIM(json_extract(request_json,'$.provider')), ''), '(unknown)'),
       COALESCE(NULLIF(TRIM(json_extract(request_json,'$.model')), ''), '(unknown)'),
       COALESCE(NULLIF(TRIM(json_extract(request_json,'$.effort_applied')), ''),
                NULLIF(TRIM(json_extract(request_json,'$.effort')), ''), '(unknown)'),
       COALESCE(NULLIF(TRIM(json_extract(request_json,'$.role')), ''), '(unknown)'),
       COUNT(*),
       COALESCE(SUM(CASE WHEN completed_at IS NOT NULL AND result_json IS NOT NULL AND TRIM(result_json) != '' THEN 1 ELSE 0 END), 0),
       COALESCE(SUM(CASE WHEN completed_at IS NOT NULL AND result_json IS NOT NULL AND TRIM(result_json) != ''
                         AND json_extract(result_json,'$.ok') = 1 THEN 1 ELSE 0 END), 0)
FROM tasks
WHERE request_json IS NOT NULL AND TRIM(request_json) != ''
  AND (? <= 0 OR created_at >= ?)
GROUP BY 1, 2, 3, 4`,
		sinceUnix, sinceUnix,
	)
	if err != nil {
		return nil, err
	}
	var out []LaneRow
	for rows.Next() {
		var r LaneRow
		if scanErr := rows.Scan(&r.Provider, &r.Model, &r.Effort, &r.Role, &r.Count, &r.Finished, &r.OKCount); scanErr != nil {
			return nil, scanErr
		}
		r.OKRateText = LaneOKRateText(r.Finished, r.OKCount)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if closeErr := rows.Close(); closeErr != nil {
		return nil, closeErr
	}
	return out, nil
}

func loadJevCounts(ctx context.Context, db *sql.DB, stateDir string, sinceUnix float64) (JevCounts, error) {
	var c JevCounts
	var routeSourceSeen int
	err := db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM tasks
WHERE request_json IS NOT NULL AND TRIM(request_json) != ''
  AND json_extract(request_json,'$.route_source') IS NOT NULL`).Scan(&routeSourceSeen)
	if err != nil {
		return c, err
	}

	err = db.QueryRowContext(ctx, `
SELECT COALESCE(SUM(CASE WHEN json_extract(request_json,'$.route_source') = 'jev' THEN 1 ELSE 0 END), 0),
       COALESCE(SUM(CASE WHEN json_extract(request_json,'$.route_source') = 'deterministic' THEN 1 ELSE 0 END), 0),
       COALESCE(SUM(CASE WHEN json_extract(request_json,'$.route_source') IS NULL THEN 1 ELSE 0 END), 0),
       COUNT(*)
FROM tasks
WHERE request_json IS NOT NULL AND TRIM(request_json) != ''
  AND (? <= 0 OR created_at >= ?)`,
		sinceUnix, sinceUnix,
	).Scan(&c.JevAssisted, &c.Deterministic, &c.ManualExplicit, &c.Total)
	if err != nil {
		return c, err
	}

	if routeSourceSeen > 0 {
		c.Measurable = true
		return c, nil
	}
	if tele := countJevFromRoutingTelemetry(stateDir, sinceUnix); tele.measurable {
		c = tele.counts
		c.Note = tele.note
		c.Measurable = true
		return c, nil
	}
	if c.Total > 0 {
		// route_source never persisted; classify via absence as manual_explicit.
		c.Measurable = true
		return c, nil
	}
	c.Measurable = false
	c.Note = "field absent — unmeasurable today (no dispatches; route_source never observed; no routing telemetry file)"
	return c, nil
}

type jevTelemetryResult struct {
	counts     JevCounts
	measurable bool
	note       string
}

func countJevFromRoutingTelemetry(stateDir string, sinceUnix float64) jevTelemetryResult {
	candidates := []string{
		filepath.Join(stateDir, "routing_telemetry.jsonl"),
		filepath.Join(stateDir, "routing_events.jsonl"),
	}
	for _, p := range candidates {
		st, err := os.Stat(p)
		if err != nil || st.IsDir() {
			continue
		}
		counts, readErr := scanRoutingTelemetryFile(p, sinceUnix)
		if readErr != nil {
			continue
		}
		counts.Total = counts.JevAssisted + counts.Deterministic + counts.ManualExplicit
		return jevTelemetryResult{counts: counts, measurable: true, note: "source: " + filepath.Base(p)}
	}
	return jevTelemetryResult{}
}

func scanRoutingTelemetryFile(path string, sinceUnix float64) (JevCounts, error) {
	f, err := os.Open(path)
	if err != nil {
		return JevCounts{}, err
	}
	var c JevCounts
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec struct {
			RouterMode  string `json:"router_mode"`
			RouteSource string `json:"route_source"`
			RecordedAt  string `json:"recorded_at"`
		}
		if jsonErr := json.Unmarshal([]byte(line), &rec); jsonErr != nil {
			continue
		}
		if sinceUnix > 0 && rec.RecordedAt != "" {
			if ts, parseErr := time.Parse(time.RFC3339, rec.RecordedAt); parseErr == nil && ts.Unix() < int64(sinceUnix) {
				continue
			}
		}
		mode := strings.TrimSpace(rec.RouterMode)
		src := strings.TrimSpace(rec.RouteSource)
		switch {
		case mode == "jev_assisted" || src == "jev":
			c.JevAssisted++
		case src == "deterministic":
			c.Deterministic++
		default:
			c.ManualExplicit++
		}
	}
	scanErr := sc.Err()
	closeErr := f.Close()
	if scanErr != nil {
		return c, scanErr
	}
	if closeErr != nil {
		return c, closeErr
	}
	return c, nil
}

func conventionAnchorUnix(ref string, baseline bool, gitFn func(...string) (string, error)) (since float64, anchored bool, windowNote string, err error) {
	if baseline {
		windowNote = "historical baseline (no convention anchor)"
		return 0, false, windowNote, nil
	}
	if _, err = gitFn("rev-parse", "--verify", ref+"^{commit}"); err != nil {
		return 0, false, "", err
	}
	anchor, aErr := gitFn("log", ref, "--first-parent", "--diff-merges=first-parent", "--no-patch", "--diff-filter=A", "--format=%H", "--", conventionPath)
	if aErr != nil {
		return 0, false, "", aErr
	}
	if anchor == "" {
		if _, catErr := gitFn("cat-file", "-e", ref+":"+conventionPath); catErr == nil {
			return 0, false, "", fmt.Errorf("convention exists but its main-landing anchor is missing")
		}
		windowNote = "before convention: ADR-0039 has not landed on main"
		return 0, false, windowNote, nil
	}
	anchors := strings.Fields(anchor)
	anchor = anchors[len(anchors)-1]
	tsStr, tsErr := gitFn("show", "-s", "--format=%ct", anchor)
	if tsErr != nil {
		return 0, false, "", tsErr
	}
	var ts int64
	if _, scanErr := fmt.Sscanf(tsStr, "%d", &ts); scanErr != nil {
		return 0, false, "", fmt.Errorf("parse anchor timestamp: %w", scanErr)
	}
	windowNote = "tasks since convention anchor `" + anchor + "`"
	return float64(ts), true, windowNote, nil
}

func renderMetricsSections(ref string, baseline bool, gitFn func(...string) (string, error)) (string, error) {
	since, _, windowNote, err := conventionAnchorUnix(ref, baseline, gitFn)
	if err != nil {
		return "", err
	}
	primary, alt := resolveMetricsDB()
	var b strings.Builder
	fmt.Fprintf(&b, "\n## Worker metrics (tasks table)\n\nState: %s\n", primary.StateDir)
	if alt != nil {
		fmt.Fprintf(&b, "Alternate state dir (not merged; preference %s): %s\n", "G8S_STATE_DIR", alt.StateDir)
	}
	fmt.Fprintf(&b, "Window: %s\n\n", windowNote)

	ctx := context.Background()
	db, openErr := openTasksDBRO(primary.DBPath)
	if openErr != nil {
		fmt.Fprintf(&b, "YIELD: database unavailable — %v\n", openErr)
		fmt.Fprint(&b, "LANES: database unavailable\n")
		fmt.Fprint(&b, "JEV: database unavailable\n")
		return b.String(), nil
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			fmt.Fprintf(&b, "\n(db close: %v)\n", closeErr)
		}
	}()
	if !tasksTablePresent(ctx, db) {
		fmt.Fprint(&b, "YIELD: tasks table absent — zero-row section\n")
		fmt.Fprint(&b, "LANES: tasks table absent — zero-row section\n")
		fmt.Fprint(&b, "JEV: tasks table absent — zero-row section\n")
		return b.String(), nil
	}

	// YIELD
	yield, yErr := loadYieldCounts(ctx, db, since)
	if yErr != nil {
		fmt.Fprintf(&b, "YIELD: query error — %v\n\n", yErr)
	} else {
		view := ComputeYieldView(yield, minWindow)
		fmt.Fprintf(&b, "### YIELD\n\n")
		fmt.Fprintf(&b, "| Metric | Value |\n| --- | ---: |\n")
		fmt.Fprintf(&b, "| attempts_total (finished tasks) | %d |\n", view.FinishedTotal)
		fmt.Fprintf(&b, "| first_pass_ok | %d |\n", view.FirstPassOK)
		fmt.Fprintf(&b, "| yield %% | %s |\n\n", view.RatioPct)

		thisW, priorW, spanned, tErr := loadYieldTrend(ctx, db, time.Now())
		if tErr != nil {
			fmt.Fprintf(&b, "Trend: unavailable (%v)\n\n", tErr)
		} else if !spanned {
			fmt.Fprintf(&b, "Trend: single window (data does not span prior week)\n\n")
		} else {
			thisV := ComputeYieldView(thisW, minWindow)
			priorV := ComputeYieldView(priorW, minWindow)
			fmt.Fprintf(&b, "Trend: this week %s (n=%d) vs prior week %s (n=%d)\n\n",
				thisV.RatioPct, thisV.FinishedTotal, priorV.RatioPct, priorV.FinishedTotal)
		}
	}

	// LANES
	lanes, lErr := loadLaneRows(ctx, db, since)
	if lErr != nil {
		fmt.Fprintf(&b, "### LANES\n\nquery error: %v\n\n", lErr)
	} else {
		SortLanesDescByCount(lanes)
		lanes = LimitLanes(lanes, 15)
		fmt.Fprintf(&b, "### LANES\n\n")
		if len(lanes) == 0 {
			fmt.Fprint(&b, "No dispatches in window.\n\n")
		} else {
			fmt.Fprintf(&b, "| provider | model | effort | role | count | ok-rate |\n")
			fmt.Fprintf(&b, "| --- | --- | --- | --- | ---: | ---: |\n")
			for _, r := range lanes {
				fmt.Fprintf(&b, "| %s | %s | %s | %s | %d | %s |\n",
					r.Provider, r.Model, r.Effort, r.Role, r.Count, r.OKRateText)
			}
			fmt.Fprint(&b, "\n")
		}
	}

	// JEV
	jev, jErr := loadJevCounts(ctx, db, filepath.Dir(primary.DBPath), since)
	if jErr != nil {
		fmt.Fprintf(&b, "### JEV\n\nquery error: %v\n\n", jErr)
	} else {
		jv := ComputeJevView(jev)
		fmt.Fprintf(&b, "### JEV\n\n")
		if !jev.Measurable {
			fmt.Fprintf(&b, "| Mode | Count |\n| --- | ---: |\n")
			fmt.Fprintf(&b, "| %s | — |\n\n", jev.Note)
		} else {
			fmt.Fprintf(&b, "| Mode | Count |\n| --- | ---: |\n")
			fmt.Fprintf(&b, "| jev_assisted (route_source=jev) | %d |\n", jv.JevAssisted)
			fmt.Fprintf(&b, "| deterministic_auto (route_source=deterministic) | %d |\n", jv.Deterministic)
			fmt.Fprintf(&b, "| manual_explicit (route_source absent) | %d |\n", jv.ManualExplicit)
			fmt.Fprintf(&b, "| total dispatches | %d |\n", jv.Total)
			fmt.Fprintf(&b, "| jev adoption %% | %s |\n", jv.AdoptionPct)
			if jev.Note != "" {
				fmt.Fprintf(&b, "\nNote: %s\n", jev.Note)
			}
			fmt.Fprint(&b, "\n")
		}
	}

	return b.String(), nil
}
