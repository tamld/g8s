package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pterm/pterm"
	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/lessons"
	"github.com/tamld/g8s/internal/pathutil"
	"github.com/tamld/g8s/internal/telemetry"
)

// runLesson routes retrospective lesson subcommands (issue #519, SCORECARD S-9).
func runLesson(args []string) {
	code := runLessonWithIO(args, os.Stdout, os.Stderr)
	if code != 0 {
		os.Exit(code)
	}
}

// runLessonWithIO executes the lesson command group and writes output to stdout/stderr.
func runLessonWithIO(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		exitUsageWithWriter(stderr, "lesson", "", "", "usage: g8s lesson <create|verify|list> [flags]", "Use 'g8s lesson create', 'g8s lesson verify', or 'g8s lesson list'", false)
		return 2
	}

	sub := args[0]
	switch sub {
	case "create":
		return runLessonCreateWithIO(args[1:], stdout, stderr)
	case "verify":
		return runLessonVerifyWithIO(args[1:], stdout, stderr)
	case "list":
		return runLessonListWithIO(args[1:], stdout, stderr)
	case "--help", "-h", "help":
		printLessonUsage(stdout)
		return 0
	default:
		exitUsageWithWriter(stderr, "lesson", sub, "", fmt.Sprintf("unknown lesson subcommand %q (valid: create, verify, list)", sub), "Run 'g8s lesson help' for usage", false)
		return 2
	}
}

func printLessonUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: g8s lesson <create|verify|list> [flags]")
	fmt.Fprintln(w, "\nSubcommands:")
	fmt.Fprintln(w, "  create   Verify and append a lesson draft to the ledger (g8s lesson create --file <json>)")
	fmt.Fprintln(w, "  verify   Verify a lesson draft without appending (dry-run) (g8s lesson verify --file <json>)")
	fmt.Fprintln(w, "  list     List lessons recorded in the ledger (g8s lesson list [--status <f>])")
}

// readOnlyTelemetryReader queries telemetry events directly using SQLite in read-only mode,
// ensuring the CLI never writes telemetry and never executes schema migrations against the telemetry DB.
type readOnlyTelemetryReader struct {
	db *sql.DB
}

func openReadOnlyTelemetryDB(dbPath string) (*readOnlyTelemetryReader, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("telemetry db not found: %w", err)
	}
	uri := pathutil.SQLiteURI(dbPath, "mode=ro")
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return nil, fmt.Errorf("open read-only telemetry db: %w", err)
	}
	db.SetMaxOpenConns(1)
	return &readOnlyTelemetryReader{db: db}, nil
}

func (r *readOnlyTelemetryReader) Close() error {
	if r.db != nil {
		return r.db.Close()
	}
	return nil
}

func (r *readOnlyTelemetryReader) EventsByTask(ctx context.Context, taskID string) ([]telemetry.TraceEvent, error) {
	query := `
		SELECT id, task_id, supervisor_task_id, event_type, timestamp,
		       payload, exit_code, error, duration, tags
		FROM telemetry_events
		WHERE task_id = ?
		ORDER BY timestamp ASC
	`
	rows, err := r.db.QueryContext(ctx, query, taskID)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()

	var events []telemetry.TraceEvent
	for rows.Next() {
		var ev telemetry.TraceEvent
		var ts, dur int64
		var payload, tags, supTaskID sql.NullString
		var exitCode sql.NullInt64
		var errStr sql.NullString

		err := rows.Scan(&ev.ID, &ev.TaskID, &supTaskID, &ev.EventType, &ts,
			&payload, &exitCode, &errStr, &dur, &tags)
		if err != nil {
			return nil, err
		}

		ev.Timestamp = time.Unix(0, ts)
		ev.Duration = time.Duration(dur)
		if supTaskID.Valid {
			ev.SupervisorTaskID = &supTaskID.String
		}
		if exitCode.Valid {
			val := int(exitCode.Int64)
			ev.ExitCode = &val
		}
		if errStr.Valid {
			ev.Error = errStr.String
		}
		if tags.Valid && tags.String != "" {
			ev.Tags = strings.Split(tags.String, ",")
		}
		if payload.Valid && payload.String != "" {
			_ = json.Unmarshal([]byte(payload.String), &ev.Payload)
		}
		events = append(events, ev)
	}
	return events, rows.Err()
}

func resolveTelemetryDBPath(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if p := os.Getenv("G8S_TELEMETRY_DB"); p != "" {
		return p
	}
	if p, err := databasePath(); err == nil {
		return filepath.Join(filepath.Dir(p), "telemetry.db")
	}
	return filepath.Join(pathutil.DefaultStateDir(), "telemetry.db")
}

func parseLessonDraft(data []byte, roundOverride, authorClassOverride string) (lessons.Lesson, error) {
	var lesson lessons.Lesson
	if err := json.Unmarshal(data, &lesson); err != nil {
		return lesson, fmt.Errorf("unmarshal lesson draft: %w", err)
	}

	var rawMap map[string]any
	if err := json.Unmarshal(data, &rawMap); err == nil {
		if _, exists := rawMap["recommendation_is_llm_opinion"]; !exists {
			lesson.RecommendationIsLLMOpinion = true
		}
	}

	if roundOverride != "" {
		lesson.RoundID = roundOverride
	}
	if authorClassOverride != "" {
		lesson.AuthorClass = authorClassOverride
	}
	if lesson.Status == "" {
		lesson.Status = lessons.StatusProposed
	}

	return lesson, nil
}

func runLessonCreateWithIO(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("lesson create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, true)

	fileFlag := fs.String("file", "", "path to lesson draft JSON file (required)")
	dbFlag := fs.String("db", "", "path to state-dir telemetry database")
	ledgerFlag := fs.String("ledger", "docs/lessons/ledger.jsonl", "path to lessons ledger file")
	roundFlag := fs.String("round", "", "round ID override")
	authorClassFlag := fs.String("author-class", "", "author class override")
	roundCapFlag := fs.Int("round-cap", lessons.DefaultRoundBudget, "round lesson budget cap")

	if err := fs.Parse(args); err != nil {
		exitUsageWithWriter(stderr, "lesson", "create", *traceID, err.Error(), "", *jsonl)
		return 2
	}

	if *fileFlag == "" {
		exitUsageWithWriter(stderr, "lesson", "create", *traceID, "usage: g8s lesson create --file <json> [--db <path>] [--ledger <path>] [--round <id>] [--author-class <name>]", "Provide a lesson draft JSON file via --file", *jsonl)
		return 2
	}

	data, err := os.ReadFile(*fileFlag)
	if err != nil {
		exitRuntimeWithWriter(stderr, "lesson", "create", *traceID, cli.CodeIO, fmt.Errorf("failed to read draft file %s: %w", *fileFlag, err), "", *jsonl)
		return 1
	}

	lesson, err := parseLessonDraft(data, *roundFlag, *authorClassFlag)
	if err != nil {
		exitRuntimeWithWriter(stderr, "lesson", "create", *traceID, cli.CodeInvalid, err, "Ensure draft file contains valid JSON", *jsonl)
		return 1
	}

	dbPath := resolveTelemetryDBPath(*dbFlag)
	telReader, err := openReadOnlyTelemetryDB(dbPath)
	if err != nil {
		exitRuntimeWithWriter(stderr, "lesson", "create", *traceID, cli.CodeNotFound, fmt.Errorf("telemetry database error: %w", err), "Ensure telemetry database exists at "+dbPath, *jsonl)
		return 1
	}
	defer telReader.Close()

	ledger := lessons.NewLedger(*ledgerFlag)
	deps := lessons.Dependencies{
		Context:   context.Background(),
		Telemetry: telReader,
		Ledger:    ledger,
		RoundCap:  *roundCapFlag,
	}

	verdict := lessons.Verify(lesson, deps)

	if !verdict.Valid {
		if *jsonMode || *jsonl {
			env := cli.NewEnvelope("verdict", "lesson", "create", verdict)
			env.TraceID = *traceID
			_ = cli.WriteResponse(stdout, env, *jsonl)
		} else {
			fmt.Fprintf(stderr, "VERDICT: %s\n", verdict.Status)
			if verdict.Reason != "" {
				fmt.Fprintf(stderr, "Reason: %s\n", verdict.Reason)
			}
			fmt.Fprintf(stderr, "Checks:\n")
			for _, chk := range verdict.Checks {
				res := "PASS"
				if !chk.Passed {
					res = "FAIL"
				}
				fmt.Fprintf(stderr, "  [%s] %s: %s\n", res, chk.Name, chk.Message)
			}
		}
		return 1
	}

	if err := ledger.Append(verdict.Lesson); err != nil {
		exitRuntimeWithWriter(stderr, "lesson", "create", *traceID, cli.CodeIO, fmt.Errorf("append to ledger: %w", err), "", *jsonl)
		return 1
	}

	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("verdict", "lesson", "create", verdict)
		env.TraceID = *traceID
		if err := cli.WriteResponse(stdout, env, *jsonl); err != nil {
			exitRuntimeWithWriter(stderr, "lesson", "create", *traceID, cli.CodeIO, err, "", *jsonl)
			return 1
		}
	} else {
		fmt.Fprintf(stdout, "VERDICT: %s\n", verdict.Status)
		fmt.Fprintf(stdout, "Lesson ID: %s (appended to %s)\n", verdict.Lesson.ID, *ledgerFlag)
	}

	return 0
}

func runLessonVerifyWithIO(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("lesson verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, true)

	fileFlag := fs.String("file", "", "path to lesson draft JSON file (required)")
	dbFlag := fs.String("db", "", "path to state-dir telemetry database")
	ledgerFlag := fs.String("ledger", "docs/lessons/ledger.jsonl", "path to lessons ledger file")
	roundFlag := fs.String("round", "", "round ID override")
	authorClassFlag := fs.String("author-class", "", "author class override")
	roundCapFlag := fs.Int("round-cap", lessons.DefaultRoundBudget, "round lesson budget cap")

	if err := fs.Parse(args); err != nil {
		exitUsageWithWriter(stderr, "lesson", "verify", *traceID, err.Error(), "", *jsonl)
		return 2
	}

	if *fileFlag == "" {
		exitUsageWithWriter(stderr, "lesson", "verify", *traceID, "usage: g8s lesson verify --file <json> [--db <path>] [--ledger <path>] [--round <id>] [--author-class <name>]", "Provide a lesson draft JSON file via --file", *jsonl)
		return 2
	}

	data, err := os.ReadFile(*fileFlag)
	if err != nil {
		exitRuntimeWithWriter(stderr, "lesson", "verify", *traceID, cli.CodeIO, fmt.Errorf("failed to read draft file %s: %w", *fileFlag, err), "", *jsonl)
		return 1
	}

	lesson, err := parseLessonDraft(data, *roundFlag, *authorClassFlag)
	if err != nil {
		exitRuntimeWithWriter(stderr, "lesson", "verify", *traceID, cli.CodeInvalid, err, "Ensure draft file contains valid JSON", *jsonl)
		return 1
	}

	dbPath := resolveTelemetryDBPath(*dbFlag)
	telReader, err := openReadOnlyTelemetryDB(dbPath)
	if err != nil {
		exitRuntimeWithWriter(stderr, "lesson", "verify", *traceID, cli.CodeNotFound, fmt.Errorf("telemetry database error: %w", err), "Ensure telemetry database exists at "+dbPath, *jsonl)
		return 1
	}
	defer telReader.Close()

	ledger := lessons.NewLedger(*ledgerFlag)
	deps := lessons.Dependencies{
		Context:   context.Background(),
		Telemetry: telReader,
		Ledger:    ledger,
		RoundCap:  *roundCapFlag,
	}

	verdict := lessons.Verify(lesson, deps)

	if !verdict.Valid {
		if *jsonMode || *jsonl {
			env := cli.NewEnvelope("verdict", "lesson", "verify", verdict)
			env.TraceID = *traceID
			_ = cli.WriteResponse(stdout, env, *jsonl)
		} else {
			fmt.Fprintf(stderr, "VERDICT: %s\n", verdict.Status)
			if verdict.Reason != "" {
				fmt.Fprintf(stderr, "Reason: %s\n", verdict.Reason)
			}
			fmt.Fprintf(stderr, "Checks:\n")
			for _, chk := range verdict.Checks {
				res := "PASS"
				if !chk.Passed {
					res = "FAIL"
				}
				fmt.Fprintf(stderr, "  [%s] %s: %s\n", res, chk.Name, chk.Message)
			}
		}
		return 1
	}

	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("verdict", "lesson", "verify", verdict)
		env.TraceID = *traceID
		if err := cli.WriteResponse(stdout, env, *jsonl); err != nil {
			exitRuntimeWithWriter(stderr, "lesson", "verify", *traceID, cli.CodeIO, err, "", *jsonl)
			return 1
		}
	} else {
		fmt.Fprintf(stdout, "VERDICT: %s (dry-run, not appended)\n", verdict.Status)
	}

	return 0
}

func runLessonListWithIO(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("lesson list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, true)

	ledgerFlag := fs.String("ledger", "docs/lessons/ledger.jsonl", "path to lessons ledger file")
	statusFlag := fs.String("status", "", "filter by lesson status (proposed, ratified, rejected)")
	passthroughFlag := fs.Bool("passthrough", false, "output raw JSONL lines without envelope")

	if err := fs.Parse(args); err != nil {
		exitUsageWithWriter(stderr, "lesson", "list", *traceID, err.Error(), "", *jsonl)
		return 2
	}

	ledger := lessons.NewLedger(*ledgerFlag)
	items, err := ledger.List()
	if err != nil {
		exitRuntimeWithWriter(stderr, "lesson", "list", *traceID, cli.CodeIO, fmt.Errorf("read ledger: %w", err), "", *jsonl)
		return 1
	}

	var filtered []lessons.Lesson
	statusFilter := strings.ToLower(strings.TrimSpace(*statusFlag))
	for _, item := range items {
		if statusFilter == "" || strings.EqualFold(string(item.Status), statusFilter) {
			filtered = append(filtered, item)
		}
	}
	if filtered == nil {
		filtered = []lessons.Lesson{}
	}

	if *passthroughFlag {
		for _, item := range filtered {
			b, err := json.Marshal(item)
			if err != nil {
				exitRuntimeWithWriter(stderr, "lesson", "list", *traceID, cli.CodeRuntime, err, "", *jsonl)
				return 1
			}
			fmt.Fprintln(stdout, string(b))
		}
		return 0
	}

	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("lessons", "lesson", "list", filtered)
		env.TraceID = *traceID
		if err := cli.WriteResponse(stdout, env, *jsonl); err != nil {
			exitRuntimeWithWriter(stderr, "lesson", "list", *traceID, cli.CodeIO, err, "", *jsonl)
			return 1
		}
		return 0
	}

	// Compact table output
	var td pterm.TableData
	td = append(td, []string{"LESSON ID", "ROUND", "STATUS", "AUTHOR", "REVIEWED", "RECOMMENDATION"})
	for _, l := range filtered {
		rec := l.Recommendation
		if len(rec) > 50 {
			rec = rec[:47] + "..."
		}
		td = append(td, []string{
			l.ID,
			l.RoundID,
			string(l.Status),
			l.AuthorClass,
			strings.Join(l.ReviewedClasses, ","),
			rec,
		})
	}
	_ = pterm.DefaultTable.WithWriter(stdout).WithHasHeader().WithData(td).Render()
	return 0
}
