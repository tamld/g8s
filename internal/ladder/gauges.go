package ladder

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"

	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/telemetry"
	_ "modernc.org/sqlite"
)

// ClassEffortGauge measures task pass-rate for a specific (class, effort) pair.
type ClassEffortGauge struct {
	Class      string  `json:"class"`
	Effort     string  `json:"effort"`
	PassCount  int     `json:"pass_count"`
	FailCount  int     `json:"fail_count"`
	TotalCount int     `json:"total_count"`
	PassRate   float64 `json:"pass_rate"` // 0.0 to 1.0
}

// ClassEscalationGauge measures escalation frequency (rungs fired / tasks) per class (P4 gauge).
type ClassEscalationGauge struct {
	Class          string  `json:"class"`
	TasksCount     int     `json:"tasks_count"`     // total root tasks
	RungsFired     int     `json:"rungs_fired"`     // ladder rungs executed
	EscalationRate float64 `json:"escalation_rate"` // rungs_fired / tasks_count
}

// HITLMetric tracks the system-level Human-in-the-Loop frequency (P6 metric).
type HITLMetric struct {
	TotalRounds int     `json:"total_rounds"` // total rounds (root tasks)
	HITLPackets int     `json:"hitl_packets"` // total HITL packets / rounds that escalated to HITL
	HITLRate    float64 `json:"hitl_rate"`    // hitl_packets / total_rounds
}

// GaugesReport aggregates the quality-ladder gauges across telemetry events and tasks.
type GaugesReport struct {
	FilterClass     string                 `json:"filter_class,omitempty"`
	PassRates       []ClassEffortGauge     `json:"pass_rates"`
	EscalationRates []ClassEscalationGauge `json:"escalation_rates"`
	HITL            HITLMetric             `json:"hitl"`
	TotalTokens     int                    `json:"total_tokens,omitempty"`
}

func isInvalidTokenEvent(ev telemetry.TraceEvent, lastTaskCumTokens map[string]int) bool {
	if ev.InputTokens < 0 || ev.OutputTokens < 0 {
		return true
	}
	if ev.Payload != nil {
		for _, key := range []string{"input_tokens", "output_tokens", "tokens", "total_tokens", "cumulative_tokens"} {
			if val, ok := ev.Payload[key]; ok {
				switch v := val.(type) {
				case int:
					if v < 0 {
						return true
					}
				case int64:
					if v < 0 {
						return true
					}
				case float64:
					if v < 0 {
						return true
					}
				}
			}
		}
		for _, key := range []string{"cumulative_tokens", "cum_tokens"} {
			if val, ok := ev.Payload[key]; ok {
				var cum int
				switch v := val.(type) {
				case int:
					cum = v
				case int64:
					cum = int(v)
				case float64:
					cum = int(v)
				}
				if ev.TaskID != "" && lastTaskCumTokens != nil {
					if prev, exists := lastTaskCumTokens[ev.TaskID]; exists && cum < prev {
						return true
					}
				}
			}
		}
	}
	return false
}

func eventTokens(ev telemetry.TraceEvent) int {
	inTok := ev.InputTokens
	outTok := ev.OutputTokens
	if inTok == 0 && outTok == 0 && ev.Payload != nil {
		if v, ok := ev.Payload["input_tokens"].(float64); ok && v > 0 {
			inTok = int(v)
		}
		if v, ok := ev.Payload["output_tokens"].(float64); ok && v > 0 {
			outTok = int(v)
		}
		if inTok == 0 && outTok == 0 {
			if v, ok := ev.Payload["tokens"].(float64); ok && v > 0 {
				inTok = int(v)
			}
		}
	}
	return inTok + outTok
}

func extractEventClass(ev telemetry.TraceEvent) string {
	if ev.Class != "" {
		return ev.Class
	}
	if ev.EffortClass != "" {
		return ev.EffortClass
	}
	if ev.Payload != nil {
		if c, ok := ev.Payload["class"].(string); ok && c != "" {
			return c
		}
		if c, ok := ev.Payload["effort_class"].(string); ok && c != "" {
			return c
		}
	}
	return "unregistered"
}

func extractEventEffort(ev telemetry.TraceEvent) string {
	if ev.EffortApplied != "" {
		return ev.EffortApplied
	}
	if ev.EffortRequested != "" {
		return ev.EffortRequested
	}
	if ev.Payload != nil {
		if e, ok := ev.Payload["effort_applied"].(string); ok && e != "" {
			return e
		}
		if e, ok := ev.Payload["effort_requested"].(string); ok && e != "" {
			return e
		}
	}
	return "medium"
}

func isPassEvent(ev telemetry.TraceEvent) bool {
	if ev.EventType == telemetry.TraceEventTaskCompleted {
		return true
	}
	if ev.ExitCode != nil && *ev.ExitCode == 0 {
		return true
	}
	return false
}

func isFailEvent(ev telemetry.TraceEvent) bool {
	if ev.EventType == telemetry.TraceEventTaskFailed || ev.EventType == telemetry.TraceEventTaskTimeout {
		return true
	}
	if ev.ExitCode != nil && *ev.ExitCode != 0 {
		return true
	}
	return false
}

func round4(val float64) float64 {
	if math.IsNaN(val) || math.IsInf(val, 0) {
		return 0.0
	}
	return math.Round(val*10000) / 10000
}

// ComputeGauges calculates the pass-rate, escalation-rate, and HITL-rate gauges
// from synthetic or loaded trace events and task queue records.
func ComputeGauges(events []telemetry.TraceEvent, tasks []*controlplane.Task, filterClass string) *GaugesReport {
	type pairKey struct {
		class  string
		effort string
	}

	// Build map of tasks by task_id to inspect lineage
	taskMap := make(map[string]*controlplane.Task)
	classByTask := make(map[string]string)
	for _, t := range tasks {
		if t != nil {
			taskMap[t.TaskID] = t
			// Extract class from task request payload if present
			if len(t.Request) > 0 {
				var req struct {
					Class       string `json:"class"`
					EffortClass string `json:"effort_class"`
				}
				if json.Unmarshal(t.Request, &req) == nil {
					if req.Class != "" {
						classByTask[t.TaskID] = req.Class
					} else if req.EffortClass != "" {
						classByTask[t.TaskID] = req.EffortClass
					}
				}
			}
		}
	}

	// Resolve root task for every task in taskMap
	rootTaskForTask := make(map[string]string)
	for id, t := range taskMap {
		cur := t
		visited := make(map[string]bool)
		for cur.ParentTaskID != nil && *cur.ParentTaskID != "" && !visited[cur.TaskID] {
			visited[cur.TaskID] = true
			parent, ok := taskMap[*cur.ParentTaskID]
			if !ok {
				rootTaskForTask[id] = *cur.ParentTaskID
				break
			}
			cur = parent
		}
		if rootTaskForTask[id] == "" {
			rootTaskForTask[id] = cur.TaskID
		}
	}

	// Propagate class from root tasks to child tasks if missing
	for id, rootID := range rootTaskForTask {
		if classByTask[id] == "" && classByTask[rootID] != "" {
			classByTask[id] = classByTask[rootID]
		}
	}

	// Escalation and HITL rate calculations
	rootTasksByClass := make(map[string]map[string]bool)
	rungsByClass := make(map[string]int)

	// Scan tasks from control plane
	for _, t := range tasks {
		if t == nil {
			continue
		}
		cls := classByTask[t.TaskID]
		if cls == "" {
			cls = "unregistered"
		}
		if filterClass != "" && !strings.EqualFold(cls, filterClass) {
			continue
		}

		if t.ParentTaskID == nil || *t.ParentTaskID == "" {
			// Root task
			if rootTasksByClass[cls] == nil {
				rootTasksByClass[cls] = make(map[string]bool)
			}
			rootTasksByClass[cls][t.TaskID] = true
		} else {
			// Escalation rung
			rungsByClass[cls]++
		}
	}

	// Helper to resolve the root task ID for an event
	resolveRootTaskID := func(ev telemetry.TraceEvent, cls string) string {
		if ev.SupervisorTaskID != nil && *ev.SupervisorTaskID != "" {
			supID := *ev.SupervisorTaskID
			if root, ok := rootTaskForTask[supID]; ok && root != "" {
				return root
			}
			return supID
		}
		if ev.Payload != nil {
			for _, k := range []string{"root_task_id", "supervisor_task_id", "parent_task_id"} {
				if v, ok := ev.Payload[k].(string); ok && v != "" {
					if root, ok := rootTaskForTask[v]; ok && root != "" {
						return root
					}
					return v
				}
			}
		}
		if ev.TaskID != "" {
			if root, ok := rootTaskForTask[ev.TaskID]; ok && root != "" {
				return root
			}
		}
		if roots, ok := rootTasksByClass[cls]; ok && len(roots) == 1 {
			for rID := range roots {
				return rID
			}
		}
		return ev.TaskID
	}

	// 1. Filter events with token monotonicity validation (R3-9)
	var validEvents []telemetry.TraceEvent
	var cumulativeTokens int
	lastTaskCumTokens := make(map[string]int)

	for _, ev := range events {
		if isInvalidTokenEvent(ev, lastTaskCumTokens) {
			// R3-9: reject event that would move cumulative counter backwards
			// Skip event, keep counter, do not panic, do not go negative
			continue
		}

		cls := extractEventClass(ev)
		if cls == "unregistered" && ev.TaskID != "" && classByTask[ev.TaskID] != "" {
			cls = classByTask[ev.TaskID]
		}
		matchesFilter := filterClass == "" || strings.EqualFold(cls, filterClass)

		tokDelta := eventTokens(ev)
		if tokDelta > 0 && matchesFilter {
			cumulativeTokens += tokDelta
		}

		if ev.Payload != nil && ev.TaskID != "" {
			for _, key := range []string{"cumulative_tokens", "cum_tokens"} {
				if val, ok := ev.Payload[key]; ok {
					switch v := val.(type) {
					case int:
						if v > lastTaskCumTokens[ev.TaskID] {
							lastTaskCumTokens[ev.TaskID] = v
						}
					case float64:
						if int(v) > lastTaskCumTokens[ev.TaskID] {
							lastTaskCumTokens[ev.TaskID] = int(v)
						}
					}
				}
			}
		}

		validEvents = append(validEvents, ev)
	}

	// Tally pass and fail events per (class, effort)
	passCounts := make(map[pairKey]int)
	failCounts := make(map[pairKey]int)

	for _, ev := range validEvents {
		cls := extractEventClass(ev)
		if cls == "unregistered" && ev.TaskID != "" && classByTask[ev.TaskID] != "" {
			cls = classByTask[ev.TaskID]
		}
		if filterClass != "" && !strings.EqualFold(cls, filterClass) {
			continue
		}

		eff := extractEventEffort(ev)
		key := pairKey{class: cls, effort: eff}

		if isPassEvent(ev) {
			passCounts[key]++
		} else if isFailEvent(ev) {
			failCounts[key]++
		}
	}

	var passRates []ClassEffortGauge
	allKeys := make(map[pairKey]bool)
	for k := range passCounts {
		allKeys[k] = true
	}
	for k := range failCounts {
		allKeys[k] = true
	}

	for k := range allKeys {
		p := passCounts[k]
		f := failCounts[k]
		tot := p + f
		rate := 0.0
		if tot > 0 {
			rate = round4(float64(p) / float64(tot))
		}
		passRates = append(passRates, ClassEffortGauge{
			Class:      k.class,
			Effort:     k.effort,
			PassCount:  p,
			FailCount:  f,
			TotalCount: tot,
			PassRate:   rate,
		})
	}

	sort.Slice(passRates, func(i, j int) bool {
		if passRates[i].Class != passRates[j].Class {
			return passRates[i].Class < passRates[j].Class
		}
		return ladderIndex(passRates[i].Effort) < ladderIndex(passRates[j].Effort)
	})

	// Scan events for ladder rungs and HITL markers (deduped by root task ID per R3-10)
	hitlRootTasks := make(map[string]bool)

	for _, ev := range validEvents {
		cls := extractEventClass(ev)
		if cls == "unregistered" && ev.TaskID != "" && classByTask[ev.TaskID] != "" {
			cls = classByTask[ev.TaskID]
		}
		if filterClass != "" && !strings.EqualFold(cls, filterClass) {
			continue
		}

		isLadderRung := false
		isHITL := false

		for _, tag := range ev.Tags {
			if strings.EqualFold(tag, "ladder") || strings.EqualFold(tag, "ladder-rung") {
				isLadderRung = true
			}
			if strings.EqualFold(tag, "hitl") || strings.EqualFold(tag, "hitl-packet") {
				isHITL = true
			}
		}
		if ev.Payload != nil {
			if act, ok := ev.Payload["action"].(string); ok && strings.EqualFold(act, "hitl") {
				isHITL = true
			}
			if fv, ok := ev.Payload["final_verdict"].(string); ok && (len(fv) >= 4 && strings.EqualFold(fv[:4], "hitl")) {
				isHITL = true
			}
			if r, ok := ev.Payload["ladder_rung"].(float64); ok && r > 0 {
				isLadderRung = true
			}
		}

		if isLadderRung && len(tasks) == 0 {
			// If tasks table was not supplied, count from events
			rungsByClass[cls]++
		}
		if isHITL {
			rootID := resolveRootTaskID(ev, cls)
			if rootID != "" {
				hitlRootTasks[rootID] = true
			}
		}
	}

	// Ensure all classes in events or tasks have an escalation gauge
	allClasses := make(map[string]bool)
	for cls := range rootTasksByClass {
		allClasses[cls] = true
	}
	for cls := range rungsByClass {
		allClasses[cls] = true
	}
	for _, g := range passRates {
		allClasses[g.Class] = true
	}

	var escalationRates []ClassEscalationGauge
	totalRounds := 0
	for cls := range allClasses {
		taskCount := len(rootTasksByClass[cls])
		if taskCount == 0 {
			// Fallback: estimate from distinct root task IDs in events for this class
			distinct := make(map[string]bool)
			for _, ev := range validEvents {
				evCls := extractEventClass(ev)
				if evCls == "unregistered" && ev.TaskID != "" && classByTask[ev.TaskID] != "" {
					evCls = classByTask[ev.TaskID]
				}
				if strings.EqualFold(evCls, cls) {
					distinct[resolveRootTaskID(ev, cls)] = true
				}
			}
			taskCount = len(distinct)
			if taskCount == 0 {
				taskCount = 1
			}
		}
		totalRounds += taskCount
		rungs := rungsByClass[cls]
		escRate := 0.0
		if taskCount > 0 {
			escRate = round4(float64(rungs) / float64(taskCount))
		}
		escalationRates = append(escalationRates, ClassEscalationGauge{
			Class:          cls,
			TasksCount:     taskCount,
			RungsFired:     rungs,
			EscalationRate: escRate,
		})
	}

	sort.Slice(escalationRates, func(i, j int) bool {
		return escalationRates[i].Class < escalationRates[j].Class
	})

	// R3-11: Never leak unfiltered events/tasks into TotalRounds when filterClass is set
	if filterClass == "" && totalRounds == 0 {
		totalRounds = len(tasks)
		if totalRounds == 0 {
			totalRounds = len(validEvents)
		}
	}

	hitlCount := len(hitlRootTasks)
	if totalRounds > 0 && hitlCount > totalRounds {
		hitlCount = totalRounds
	}
	hitlRate := 0.0
	if totalRounds > 0 {
		hitlRate = round4(float64(hitlCount) / float64(totalRounds))
	}
	if hitlRate > 1.0 {
		hitlRate = 1.0
	}
	if hitlRate < 0.0 {
		hitlRate = 0.0
	}

	return &GaugesReport{
		FilterClass:     filterClass,
		PassRates:       passRates,
		EscalationRates: escalationRates,
		HITL: HITLMetric{
			TotalRounds: totalRounds,
			HITLPackets: hitlCount,
			HITLRate:    hitlRate,
		},
		TotalTokens: cumulativeTokens,
	}
}

// LoadTelemetryEvents queries trace events from a telemetry SQLite database in read-only mode.
func LoadTelemetryEvents(ctx context.Context, dbPath string) ([]telemetry.TraceEvent, error) {
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return nil, nil // fail-open: return empty slice if db missing
		}
		return nil, fmt.Errorf("stat telemetry db %s: %w", dbPath, err)
	}

	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=ro", dbPath))
	if err != nil {
		return nil, fmt.Errorf("open telemetry db: %w", err)
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, `
		SELECT id, task_id, supervisor_task_id, event_type, timestamp,
		       payload, exit_code, error, duration, tags
		FROM telemetry_events
		ORDER BY timestamp ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("query telemetry events: %w", err)
	}
	defer rows.Close()

	var events []telemetry.TraceEvent
	for rows.Next() {
		var ev telemetry.TraceEvent
		var rawTS int64
		var payloadJSON string
		var tagsStr string
		var durNano int64

		if err := rows.Scan(
			&ev.ID,
			&ev.TaskID,
			&ev.SupervisorTaskID,
			&ev.EventType,
			&rawTS,
			&payloadJSON,
			&ev.ExitCode,
			&ev.Error,
			&durNano,
			&tagsStr,
		); err != nil {
			return nil, fmt.Errorf("scan telemetry event: %w", err)
		}

		if payloadJSON != "" {
			var p map[string]any
			if json.Unmarshal([]byte(payloadJSON), &p) == nil {
				ev.Payload = p
			}
		}
		if tagsStr != "" {
			ev.Tags = strings.Split(tagsStr, ",")
		}

		// Hydrate W2 effort fields from payload if present
		if ev.Payload != nil {
			if v, ok := ev.Payload["class"].(string); ok {
				ev.Class = v
			}
			if v, ok := ev.Payload["effort_class"].(string); ok {
				ev.EffortClass = v
			}
			if v, ok := ev.Payload["effort_requested"].(string); ok {
				ev.EffortRequested = v
			}
			if v, ok := ev.Payload["effort_applied"].(string); ok {
				ev.EffortApplied = v
			}
			if v, ok := ev.Payload["effort_mismatch"].(bool); ok {
				ev.EffortMismatch = v
			}
			if v, ok := ev.Payload["input_tokens"].(float64); ok {
				ev.InputTokens = int(v)
			}
			if v, ok := ev.Payload["output_tokens"].(float64); ok {
				ev.OutputTokens = int(v)
			}
			if v, ok := ev.Payload["duration_seconds"].(float64); ok {
				ev.DurationSeconds = v
			}
		}

		events = append(events, ev)
	}

	return events, rows.Err()
}
