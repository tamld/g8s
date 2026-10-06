package ladder

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/telemetry"
)

func TestComputeGaugesSynthetic(t *testing.T) {
	exit0 := 0
	exit1 := 1

	now := time.Now()

	// Synthetic telemetry events
	events := []telemetry.TraceEvent{
		// docs class: low effort (3 passes, 1 failure -> 0.75 pass rate)
		{
			ID:            "ev-1",
			TaskID:        "task-docs-1",
			EventType:     telemetry.TraceEventTaskCompleted,
			Timestamp:     now,
			Class:         "docs",
			EffortApplied: "low",
			ExitCode:      &exit0,
		},
		{
			ID:            "ev-2",
			TaskID:        "task-docs-2",
			EventType:     telemetry.TraceEventTaskCompleted,
			Timestamp:     now,
			Class:         "docs",
			EffortApplied: "low",
			ExitCode:      &exit0,
		},
		{
			ID:            "ev-3",
			TaskID:        "task-docs-3",
			EventType:     telemetry.TraceEventTaskCompleted,
			Timestamp:     now,
			Class:         "docs",
			EffortApplied: "low",
			ExitCode:      &exit0,
		},
		{
			ID:            "ev-4",
			TaskID:        "task-docs-4",
			EventType:     telemetry.TraceEventTaskFailed,
			Timestamp:     now,
			Class:         "docs",
			EffortApplied: "low",
			ExitCode:      &exit1,
		},

		// docs class: medium effort (2 passes, 0 failures -> 1.0 pass rate)
		{
			ID:            "ev-5",
			TaskID:        "task-docs-5",
			EventType:     telemetry.TraceEventTaskCompleted,
			Timestamp:     now,
			Class:         "docs",
			EffortApplied: "medium",
			ExitCode:      &exit0,
		},
		{
			ID:            "ev-6",
			TaskID:        "task-docs-6",
			EventType:     telemetry.TraceEventTaskCompleted,
			Timestamp:     now,
			Class:         "docs",
			EffortApplied: "medium",
			ExitCode:      &exit0,
		},

		// feature class: high effort (1 pass, 1 failure -> 0.50 pass rate)
		{
			ID:            "ev-7",
			TaskID:        "task-feat-1",
			EventType:     telemetry.TraceEventTaskCompleted,
			Timestamp:     now,
			Class:         "feature",
			EffortApplied: "high",
			ExitCode:      &exit0,
		},
		{
			ID:            "ev-8",
			TaskID:        "task-feat-2",
			EventType:     telemetry.TraceEventTaskFailed,
			Timestamp:     now,
			Class:         "feature",
			EffortApplied: "high",
			ExitCode:      &exit1,
			Tags:          []string{"hitl"},
		},
	}

	parentFeat1 := "task-feat-root-1"
	parentFeat2 := "task-feat-root-2"
	parentDocs4 := "task-docs-root-4"

	// Synthetic tasks to establish root tasks and rungs
	tasks := []*controlplane.Task{
		// docs root tasks: 4 roots, 1 child rung -> escalation rate = 1 / 4 = 0.25
		{TaskID: "task-docs-root-1", Request: []byte(`{"class":"docs"}`)},
		{TaskID: "task-docs-root-2", Request: []byte(`{"class":"docs"}`)},
		{TaskID: "task-docs-root-3", Request: []byte(`{"class":"docs"}`)},
		{TaskID: "task-docs-root-4", Request: []byte(`{"class":"docs"}`)},
		{TaskID: "task-docs-child-1", ParentTaskID: &parentDocs4, Request: []byte(`{"class":"docs"}`)},

		// feature root tasks: 2 roots, 4 child rungs -> escalation rate = 4 / 2 = 2.0
		{TaskID: "task-feat-root-1", Request: []byte(`{"class":"feature"}`)},
		{TaskID: "task-feat-root-2", Request: []byte(`{"class":"feature"}`)},
		{TaskID: "task-feat-child-1", ParentTaskID: &parentFeat1, Request: []byte(`{"class":"feature"}`)},
		{TaskID: "task-feat-child-2", ParentTaskID: &parentFeat1, Request: []byte(`{"class":"feature"}`)},
		{TaskID: "task-feat-child-3", ParentTaskID: &parentFeat2, Request: []byte(`{"class":"feature"}`)},
		{TaskID: "task-feat-child-4", ParentTaskID: &parentFeat2, Request: []byte(`{"class":"feature"}`)},
	}

	report := ComputeGauges(events, tasks, "")

	// 1. Verify PassRates math
	if len(report.PassRates) != 3 {
		t.Fatalf("PassRates count = %d, want 3", len(report.PassRates))
	}

	findPassRate := func(class, effort string) *ClassEffortGauge {
		for _, g := range report.PassRates {
			if g.Class == class && g.Effort == effort {
				return &g
			}
		}
		return nil
	}

	docsLow := findPassRate("docs", "low")
	if docsLow == nil || docsLow.PassRate != 0.75 || docsLow.TotalCount != 4 {
		t.Errorf("docs low pass rate = %+v, want rate 0.75 and total 4", docsLow)
	}

	docsMed := findPassRate("docs", "medium")
	if docsMed == nil || docsMed.PassRate != 1.0 || docsMed.TotalCount != 2 {
		t.Errorf("docs medium pass rate = %+v, want rate 1.0 and total 2", docsMed)
	}

	featHigh := findPassRate("feature", "high")
	if featHigh == nil || featHigh.PassRate != 0.5 || featHigh.TotalCount != 2 {
		t.Errorf("feature high pass rate = %+v, want rate 0.5 and total 2", featHigh)
	}

	// 2. Verify EscalationRates math
	findEscalation := func(class string) *ClassEscalationGauge {
		for _, g := range report.EscalationRates {
			if g.Class == class {
				return &g
			}
		}
		return nil
	}

	docsEsc := findEscalation("docs")
	if docsEsc == nil || docsEsc.EscalationRate != 0.25 || docsEsc.TasksCount != 4 || docsEsc.RungsFired != 1 {
		t.Errorf("docs escalation = %+v, want rate 0.25, tasks 4, rungs 1", docsEsc)
	}

	featEsc := findEscalation("feature")
	if featEsc == nil || featEsc.EscalationRate != 2.0 || featEsc.TasksCount != 2 || featEsc.RungsFired != 4 {
		t.Errorf("feature escalation = %+v, want rate 2.0, tasks 2, rungs 4", featEsc)
	}

	// 3. Verify HITL metric
	// Total rounds = 4 docs + 2 feat = 6 root tasks. HITL packets = 1.
	if report.HITL.TotalRounds != 6 {
		t.Errorf("HITL.TotalRounds = %d, want 6", report.HITL.TotalRounds)
	}
	if report.HITL.HITLPackets != 1 {
		t.Errorf("HITL.HITLPackets = %d, want 1", report.HITL.HITLPackets)
	}
	if report.HITL.HITLRate < 0.166 || report.HITL.HITLRate > 0.167 {
		t.Errorf("HITL.HITLRate = %f, want ~0.1667", report.HITL.HITLRate)
	}

	// 4. Test Class Filtering
	filtered := ComputeGauges(events, tasks, "docs")
	for _, g := range filtered.PassRates {
		if g.Class != "docs" {
			t.Errorf("filtered PassRate contains non-docs class %s", g.Class)
		}
	}
	for _, g := range filtered.EscalationRates {
		if g.Class != "docs" {
			t.Errorf("filtered EscalationRate contains non-docs class %s", g.Class)
		}
	}
}

func TestLoadTelemetryEventsFromDB(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "telemetry.db")

	// missing DB: fail-open empty slice, no error
	events, err := LoadTelemetryEvents(ctx, dbPath)
	if err != nil {
		t.Fatalf("missing db must fail open, got error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("missing db events = %d, want 0", len(events))
	}

	cfg := telemetry.DefaultTelemetryConfig()
	cfg.DBPath = dbPath
	eng, err := telemetry.NewTelemetryEngine(cfg)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	exit0 := 0
	exit1 := 1
	_ = eng.IngestEvent(ctx, telemetry.TraceEvent{
		ID: "ev-load-1", TaskID: "t-load-1", EventType: telemetry.TraceEventTaskCompleted,
		Class: "docs", EffortApplied: "low", ExitCode: &exit0, Timestamp: time.Now(),
	})
	_ = eng.IngestEvent(ctx, telemetry.TraceEvent{
		ID: "ev-load-2", TaskID: "t-load-2", EventType: telemetry.TraceEventTaskFailed,
		Class: "docs", EffortApplied: "medium", ExitCode: &exit1, Timestamp: time.Now(),
	})
	_ = eng.Close()

	loaded, err := LoadTelemetryEvents(ctx, dbPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("loaded events = %d, want 2", len(loaded))
	}
	// ascending timestamp order as written
	if loaded[0].ID == "" || loaded[0].TaskID == "" {
		t.Errorf("loaded event missing identity fields: %+v", loaded[0])
	}
}
