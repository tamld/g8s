package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// YieldCounts holds finished-task totals for first-pass yield.
type YieldCounts struct {
	FinishedTotal int
	FirstPassOK   int
}

// YieldView is the rendered view of yield with ratio semantics.
type YieldView struct {
	YieldCounts
	RatioPct   string
	Meaningful bool
}

// ComputeYieldView applies the min-window rule before emitting a ratio.
func ComputeYieldView(c YieldCounts, minFinished int) YieldView {
	v := YieldView{YieldCounts: c}
	if c.FinishedTotal <= 0 {
		v.RatioPct = "undefined (no finished tasks)"
		return v
	}
	if c.FinishedTotal < minFinished {
		v.RatioPct = fmt.Sprintf("n=%d (<%d); ratio not yet meaningful", c.FinishedTotal, minFinished)
		return v
	}
	v.Meaningful = true
	pct := float64(c.FirstPassOK) * 100 / float64(c.FinishedTotal)
	v.RatioPct = fmt.Sprintf("%.2f%%", pct)
	return v
}

// LaneRow is one provider×model×effort×role aggregate.
type LaneRow struct {
	Provider   string
	Model      string
	Effort     string
	Role       string
	Count      int
	Finished   int
	OKCount    int
	OKRateText string
}

// LaneOKRateText formats ok-rate among finished tasks in the lane.
func LaneOKRateText(finished, ok int) string {
	if finished <= 0 {
		return "— (no finished tasks)"
	}
	return fmt.Sprintf("%.2f%%", float64(ok)*100/float64(finished))
}

// SortLanesDescByCount sorts lane rows by count descending (stable tie-break).
func SortLanesDescByCount(rows []LaneRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Count != rows[j].Count {
			return rows[i].Count > rows[j].Count
		}
		if rows[i].Provider != rows[j].Provider {
			return rows[i].Provider < rows[j].Provider
		}
		if rows[i].Model != rows[j].Model {
			return rows[i].Model < rows[j].Model
		}
		return rows[i].Role < rows[j].Role
	})
}

// LimitLanes keeps at most max rows after sorting.
func LimitLanes(rows []LaneRow, max int) []LaneRow {
	if max <= 0 || len(rows) <= max {
		return rows
	}
	return rows[:max]
}

// JevCounts classifies dispatches by durable route_source metadata.
type JevCounts struct {
	JevAssisted    int
	Deterministic  int
	ManualExplicit int
	Total          int
	Measurable     bool
	Note           string
}

// ClassifyJevFromRouteSource maps route_source values to adoption buckets.
func ClassifyJevFromRouteSource(routeSource string) string {
	switch strings.TrimSpace(routeSource) {
	case "jev":
		return "jev"
	case "deterministic":
		return "deterministic"
	case "":
		return "manual"
	default:
		return "manual"
	}
}

// JevView merges counts with an honest measurability note.
type JevView struct {
	JevCounts
	AdoptionPct string
}

// ComputeJevView summarizes Jev adoption when route_source is durable on tasks.
func ComputeJevView(c JevCounts) JevView {
	v := JevView{JevCounts: c}
	if !c.Measurable {
		v.AdoptionPct = "—"
		return v
	}
	if c.Total <= 0 {
		v.AdoptionPct = "undefined (no dispatches)"
		return v
	}
	pct := float64(c.JevAssisted) * 100 / float64(c.Total)
	if math.IsNaN(pct) {
		v.AdoptionPct = "undefined"
		return v
	}
	v.AdoptionPct = fmt.Sprintf("%.2f%%", pct)
	return v
}
