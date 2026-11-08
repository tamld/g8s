package main

import (
	"strings"
	"testing"
)

func TestComputeYieldView(t *testing.T) {
	for _, tc := range []struct {
		name        string
		in          YieldCounts
		wantMeaning bool
		wantSubstr  string
	}{
		{
			name:        "zero division",
			in:          YieldCounts{},
			wantMeaning: false,
			wantSubstr:  "no finished tasks",
		},
		{
			name:        "below min window",
			in:          YieldCounts{FinishedTotal: 19, FirstPassOK: 15},
			wantMeaning: false,
			wantSubstr:  "not yet meaningful",
		},
		{
			name:        "single attempt success",
			in:          YieldCounts{FinishedTotal: 20, FirstPassOK: 20},
			wantMeaning: true,
			wantSubstr:  "100.00%",
		},
		{
			name:        "fifteen twenty one",
			in:          YieldCounts{FinishedTotal: 21, FirstPassOK: 15},
			wantMeaning: true,
			wantSubstr:  "71.43%",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ComputeYieldView(tc.in, minWindow)
			if got.Meaningful != tc.wantMeaning {
				t.Fatalf("meaningful=%v want %v ratio=%q", got.Meaningful, tc.wantMeaning, got.RatioPct)
			}
			if tc.wantSubstr != "" && !strings.Contains(got.RatioPct, tc.wantSubstr) {
				t.Fatalf("ratio %q missing %q", got.RatioPct, tc.wantSubstr)
			}
		})
	}
}

func TestLaneSortAndLimit(t *testing.T) {
	rows := []LaneRow{
		{Provider: "a", Model: "m1", Count: 3},
		{Provider: "b", Model: "m2", Count: 10},
		{Provider: "c", Model: "m3", Count: 10},
		{Provider: "d", Model: "m4", Count: 1},
	}
	SortLanesDescByCount(rows)
	if rows[0].Count != 10 || rows[2].Count != 3 {
		t.Fatalf("sort order wrong: %+v", rows)
	}
	lim := LimitLanes(rows, 2)
	if len(lim) != 2 || lim[0].Count != 10 {
		t.Fatalf("limit wrong: %+v", lim)
	}
}

func TestLaneOKRateText(t *testing.T) {
	if got := LaneOKRateText(0, 0); got != "— (no finished tasks)" {
		t.Fatalf("got %q", got)
	}
	if got := LaneOKRateText(4, 3); got != "75.00%" {
		t.Fatalf("got %q", got)
	}
}

func TestClassifyJevFromRouteSource(t *testing.T) {
	if got := ClassifyJevFromRouteSource("jev"); got != "jev" {
		t.Fatalf("got %q", got)
	}
	if got := ClassifyJevFromRouteSource(""); got != "manual" {
		t.Fatalf("got %q", got)
	}
}
