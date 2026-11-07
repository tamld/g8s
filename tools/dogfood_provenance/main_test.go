package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/tamld/g8s/internal/dogfood"
)

func TestCollectEpoch(t *testing.T) {
	for _, tc := range []struct {
		name, anchor, shallow  string
		baseline, broken, fail bool
		wantRange              string
		wantCount              int
	}{
		{name: "before convention", shallow: "false"},
		{name: "merge landing", anchor: "merge-sha", shallow: "false", wantRange: "merge-sha..origin/main", wantCount: 2},
		{name: "readded", anchor: "new-sha\nfirst-sha", shallow: "false", wantRange: "first-sha..origin/main", wantCount: 2},
		{name: "baseline", baseline: true, shallow: "false", wantRange: "origin/main", wantCount: 2},
		{name: "shallow", shallow: "true", fail: true},
		{name: "git failure", broken: true, fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logRange string
			fakeGit := func(args ...string) (string, error) {
				if tc.broken {
					return "", fmt.Errorf("Git unavailable")
				}
				call := strings.Join(args, " ")
				switch {
				case strings.Contains(call, "--verify"):
					return "tip", nil
				case strings.Contains(call, "--is-shallow-repository"):
					return tc.shallow, nil
				case strings.Contains(call, "--diff-filter=A"):
					if !strings.Contains(call, "--first-parent --diff-merges=first-parent --no-patch") {
						t.Fatal("anchor must use first-parent landing diff without patch output")
					}
					return tc.anchor, nil
				case strings.HasPrefix(call, "cat-file"):
					return "", fmt.Errorf("ADR not present")
				case strings.HasPrefix(call, "log -200 --format=%B%x00 "):
					logRange = args[len(args)-1]
					return "subject\n\nReceipt: task-1\n\x00\nsubject\n\nProvenance: F0-direct — wiring\n\x00", nil
				default:
					t.Fatalf("unexpected Git args %v", args)
					return "", nil
				}
			}
			report, _, err := collectFrom("origin/main", tc.baseline, fakeGit)
			if (err != nil) != tc.fail || logRange != tc.wantRange || len(report) != tc.wantCount {
				t.Fatalf("range=%q count=%d err=%v", logRange, len(report), err)
			}
			if len(report) > 0 && (report[0].Kind != "receipt" || report[1].Kind != "f0_direct") {
				t.Fatalf("multiline framing lost provenance: %+v", report)
			}
		})
	}
}

func TestHonestyBudget(t *testing.T) {
	for _, tc := range []struct {
		name           string
		n, unknown     int
		baseline, fail bool
	}{
		{"empty", 0, 0, false, false},
		{"small", 19, 19, false, false},
		{"at budget", 20, 6, false, false},
		{"over budget", 20, 7, false, true},
		{"baseline", 200, 200, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := make([]dogfood.Provenance, tc.n)
			for i := range report {
				report[i].Kind = "receipt"
				if i < tc.unknown {
					report[i].Kind = "unclassified"
				}
			}
			text, failed := render(report, "test", tc.baseline)
			if failed != tc.fail || !strings.Contains(text, "| Total |") {
				t.Fatalf("failed=%v, want %v; report=%s", failed, tc.fail, text)
			}
			if tc.n < minWindow && !strings.Contains(text, "ratio not yet meaningful") {
				t.Fatal("small window not labeled")
			}
		})
	}
}
