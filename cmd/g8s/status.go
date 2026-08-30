package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/pterm/pterm"
	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/heartbeat"
)

// WorkerStatusView represents the JSON and UI model for an observed worker.
type WorkerStatusView struct {
	SessionID      string         `json:"session_id"`
	PID            int            `json:"pid"`
	Binary         string         `json:"binary"`
	CommandLine    string         `json:"command_line"`
	StartedAt      time.Time      `json:"started_at"`
	LastUpdate     time.Time      `json:"last_update"`
	Status         string         `json:"status"`
	Freshness      string         `json:"freshness"` // active, stale, dead
	AgeSeconds     float64        `json:"age_seconds"`
	CurrentStep    string         `json:"current_step,omitempty"`
	Recommendation string         `json:"recommendation,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

// StatusReport encapsulates worker heartbeats and summary metrics.
type StatusReport struct {
	Workers        []WorkerStatusView `json:"workers"`
	ActiveCount    int                `json:"active_count"`
	StaleCount     int                `json:"stale_count"`
	DeadCount      int                `json:"dead_count"`
	Recommendation string             `json:"recommendation,omitempty"`
}

// StatusOptions configures the execution of status retrieval.
type StatusOptions struct {
	HeartbeatDir string
	SessionID    string
	WorkerMode   bool
	JSONMode     bool
	Clock        func() time.Time
	Writer       io.Writer
}

func runStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	_ = actor
	workerFlag := fs.Bool("worker", false, "display worker heartbeat and lifecycle status")
	sessionFlag := fs.String("session", "", "filter by specific session ID")
	dirFlag := fs.String("heartbeat-dir", "", "override heartbeat storage directory")
	if err := fs.Parse(args); err != nil {
		exitUsage("status", "", *traceID, err.Error(), "", *jsonl)
	}

	opts := StatusOptions{
		HeartbeatDir: *dirFlag,
		SessionID:    *sessionFlag,
		WorkerMode:   *workerFlag,
		JSONMode:     *jsonMode || *jsonl,
		Clock:        time.Now,
		Writer:       os.Stdout,
	}

	report, err := GenerateStatusReport(context.Background(), opts)
	if err != nil {
		exitRuntime("status", "", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}

	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("status_report", "status", "", report)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}

	renderStatusReport(report, opts.Writer)
}

// GenerateStatusReport collects all heartbeats and builds the structured StatusReport.
func GenerateStatusReport(ctx context.Context, opts StatusOptions) (*StatusReport, error) {
	if opts.Clock == nil {
		opts.Clock = time.Now
	}

	baseDir := opts.HeartbeatDir
	if baseDir == "" {
		cwd, err := os.Getwd()
		if err == nil {
			baseDir = filepath.Join(cwd, heartbeat.DefaultHeartbeatDir)
		} else {
			baseDir = heartbeat.DefaultHeartbeatDir
		}
	}

	store := heartbeat.NewStore(baseDir, opts.Clock)
	list, err := store.List()
	if err != nil {
		return nil, fmt.Errorf("list heartbeats from %s: %w", baseDir, err)
	}

	now := opts.Clock()
	report := &StatusReport{
		Workers: make([]WorkerStatusView, 0, len(list)),
	}

	for _, hb := range list {
		if opts.SessionID != "" && hb.SessionID != opts.SessionID {
			continue
		}

		freshness := heartbeat.CalculateFreshness(hb.LastUpdate, now)
		ageSec := now.Sub(hb.LastUpdate).Seconds()
		if ageSec < 0 {
			ageSec = 0
		}

		var rec string
		switch freshness {
		case heartbeat.FreshnessActive:
			report.ActiveCount++
		case heartbeat.FreshnessStale:
			report.StaleCount++
		case heartbeat.FreshnessDead:
			report.DeadCount++
			rec = "g8s cleanup --target ghost-process"
		}

		view := WorkerStatusView{
			SessionID:      hb.SessionID,
			PID:            hb.PID,
			Binary:         hb.Binary,
			CommandLine:    hb.CommandLine,
			StartedAt:      hb.StartedAt,
			LastUpdate:     hb.LastUpdate,
			Status:         hb.Status,
			Freshness:      freshness,
			AgeSeconds:     ageSec,
			CurrentStep:    hb.CurrentStep,
			Recommendation: rec,
			Metadata:       hb.Metadata,
		}

		report.Workers = append(report.Workers, view)
	}

	if report.DeadCount > 0 {
		report.Recommendation = "g8s cleanup --target ghost-process"
	}

	return report, nil
}

func renderStatusReport(report *StatusReport, w io.Writer) {
	if w == nil {
		w = os.Stdout
	}

	pterm.DefaultHeader.WithWriter(w).WithFullWidth().Println("g8s Worker Observability & Heartbeat Status")

	if len(report.Workers) == 0 {
		pterm.Info.WithWriter(w).Println("No active or recorded worker heartbeats found.")
		return
	}

	var td pterm.TableData
	td = append(td, []string{"Session ID", "PID", "Binary", "Status", "Freshness", "Last Update", "Current Step"})

	for _, wkr := range report.Workers {
		var freshStr string
		switch wkr.Freshness {
		case heartbeat.FreshnessActive:
			freshStr = pterm.Green("🟢 active (<60s)")
		case heartbeat.FreshnessStale:
			freshStr = pterm.Yellow("🟡 stale (60-300s)")
		default:
			freshStr = pterm.Red("🔴 dead (>300s)")
		}

		step := wkr.CurrentStep
		if step == "" {
			step = "-"
		}
		if len(step) > 40 {
			step = step[:37] + "..."
		}

		lastUpdateStr := wkr.LastUpdate.Format("15:04:05")
		if !wkr.LastUpdate.IsZero() {
			age := time.Duration(wkr.AgeSeconds) * time.Second
			lastUpdateStr = fmt.Sprintf("%s (%s ago)", lastUpdateStr, age.Round(time.Second))
		}

		pidStr := "-"
		if wkr.PID > 0 {
			pidStr = fmt.Sprintf("%d", wkr.PID)
		}

		td = append(td, []string{
			wkr.SessionID,
			pidStr,
			wkr.Binary,
			wkr.Status,
			freshStr,
			lastUpdateStr,
			step,
		})
	}

	_ = pterm.DefaultTable.WithWriter(w).WithHasHeader().WithData(td).Render()

	fmt.Fprintln(w)
	pterm.DefaultSection.WithWriter(w).Println("Worker Summary:")
	fmt.Fprintf(w, "  • 🟢 Active: %d | 🟡 Stale: %d | 🔴 Dead: %d\n", report.ActiveCount, report.StaleCount, report.DeadCount)

	if report.DeadCount > 0 {
		fmt.Fprintln(w)
		pterm.Warning.WithWriter(w).Printf("Detected %d dead worker(s). Recommendation: run 'g8s cleanup --target ghost-process' to reap ghost processes.\n", report.DeadCount)
	}
}
