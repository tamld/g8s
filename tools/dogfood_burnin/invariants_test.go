package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// loadFixtureSection decodes the recorded scorecard fixture into the
// compliance_vs_quality section (fresh copy per test so mutations never leak).
func loadFixtureSection(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "scorecard_fixture.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	sec, err := extractSection(raw)
	if err != nil {
		t.Fatalf("extract section: %v", err)
	}
	return sec
}

// loadFixtureEnvelope returns the raw fixture bytes (full envelope form).
func loadFixtureEnvelope(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "scorecard_fixture.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return raw
}

// testRows fetches a metric array through the same rowList conversion the
// engine uses; the returned row maps are shared references, so mutations
// reach checkSection.
func testRows(t *testing.T, sec map[string]any, field string) []map[string]any {
	t.Helper()
	rows, ok := rowList(sec, field)
	if !ok {
		t.Fatalf("field %s is not an array of objects", field)
	}
	return rows
}

func violationsByInvariant(vs []Violation) map[string][]Violation {
	out := map[string][]Violation{}
	for _, v := range vs {
		out[v.Invariant] = append(out[v.Invariant], v)
	}
	return out
}

// TestCheckSectionFixtureClean asserts the realistic fixture section — built
// to mirror the merged WP2 output shape, including a zero-rate measured lane
// (rate omitted via omitempty) and an insufficient_sample hotfix lane — is
// invariant-clean.
func TestCheckSectionFixtureClean(t *testing.T) {
	sec := loadFixtureSection(t)
	if vs := checkSection(sec); len(vs) != 0 {
		t.Fatalf("fixture section must be invariant-clean, got: %+v", vs)
	}
}

// TestExtractSection covers envelope extraction, bare-section acceptance, and
// explicit errors for malformed inputs.
func TestExtractSection(t *testing.T) {
	envelope := loadFixtureEnvelope(t)

	sec, err := extractSection(envelope)
	if err != nil {
		t.Fatalf("envelope form must parse: %v", err)
	}
	if sec["issue"] != "#971 WP2" {
		t.Fatalf("unexpected section: %v", sec["issue"])
	}

	// Bare section form.
	bare, err := json.Marshal(sec)
	if err != nil {
		t.Fatalf("marshal bare section: %v", err)
	}
	sec2, err := extractSection(bare)
	if err != nil || sec2["issue"] != "#971 WP2" {
		t.Fatalf("bare section must parse: sec=%v err=%v", sec2, err)
	}

	if _, err := extractSection([]byte("not json")); err == nil {
		t.Fatal("garbage input must error")
	}
	if _, err := extractSection([]byte(`{"data":{"axes":{}}}`)); err == nil {
		t.Fatal("envelope without compliance_vs_quality must error")
	}
	if _, err := extractSection([]byte(`{"unrelated":true}`)); err == nil {
		t.Fatal("neither envelope nor section must error")
	}
}

// TestInvariantFinite asserts NaN/±Inf never escape into the metrics. Strict
// encoding/json cannot decode NaN literals, so the cases are injected in
// memory — the engine is the gate for any upstream lenient producer.
func TestInvariantFinite(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value float64
	}{
		{"nan", math.NaN()},
		{"inf", math.Inf(1)},
		{"neg-inf", math.Inf(-1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sec := loadFixtureSection(t)
			cohort := testRows(t, sec, "cohort_metrics")[0]
			cohort["revert_fixup_rate"] = tc.value
			vs := checkSection(sec)
			byInv := violationsByInvariant(vs)
			if len(byInv[invFinite]) == 0 {
				t.Fatalf("non-finite rate must trip %s, got %+v", invFinite, vs)
			}
		})
	}
}

// TestInvariantMeasuredDenom enforces the merged WP2 sample semantics without
// inventing thresholds: measured ⇔ n ≥ min_sample (>0); insufficient_sample ⇔
// n < min_sample with no rate emitted.
func TestInvariantMeasuredDenom(t *testing.T) {
	t.Run("measured below min_sample", func(t *testing.T) {
		sec := loadFixtureSection(t)
		cohort := testRows(t, sec, "cohort_metrics")[0]
		cohort["commits"] = float64(19)
		cohort["revert_fixup_rate"] = 0.1
		vs := violationsByInvariant(checkSection(sec))
		if len(vs[invMeasuredDenom]) == 0 {
			t.Fatalf("measured with n<min_sample must trip %s", invMeasuredDenom)
		}
	})
	t.Run("measured with zero denominator", func(t *testing.T) {
		sec := loadFixtureSection(t)
		cohort := testRows(t, sec, "cohort_metrics")[0]
		cohort["commits"] = float64(0)
		delete(cohort, "revert_fixup_rate")
		cohort["rate_label"] = "measured"
		vs := violationsByInvariant(checkSection(sec))
		if len(vs[invMeasuredDenom]) == 0 {
			t.Fatalf("measured with zero denominator must trip %s", invMeasuredDenom)
		}
	})
	t.Run("insufficient_sample emitting a rate", func(t *testing.T) {
		sec := loadFixtureSection(t)
		hotfix := testRows(t, sec, "escape_rate_by_lane")[0]
		hotfix["escape_rate"] = 0.25
		vs := violationsByInvariant(checkSection(sec))
		if len(vs[invMeasuredDenom]) == 0 {
			t.Fatalf("insufficient_sample with a rate must trip %s", invMeasuredDenom)
		}
	})
	t.Run("insufficient_sample above min_sample", func(t *testing.T) {
		sec := loadFixtureSection(t)
		hotfix := testRows(t, sec, "escape_rate_by_lane")[0]
		hotfix["total"] = float64(25)
		vs := violationsByInvariant(checkSection(sec))
		if len(vs[invMeasuredDenom]) == 0 {
			t.Fatalf("insufficient_sample with n>=min_sample must trip %s", invMeasuredDenom)
		}
	})
}

// TestInvariantRateBounds asserts emitted rates stay within [0,1].
func TestInvariantRateBounds(t *testing.T) {
	for _, rate := range []float64{1.5, -0.2} {
		sec := loadFixtureSection(t)
		cohort := testRows(t, sec, "cohort_metrics")[0]
		cohort["revert_fixup_rate"] = rate
		vs := violationsByInvariant(checkSection(sec))
		if len(vs[invRateBounds]) == 0 {
			t.Fatalf("rate %v must trip %s", rate, invRateBounds)
		}
	}
}

// TestInvariantRateLabel rejects labels outside the merged WP2 set.
func TestInvariantRateLabel(t *testing.T) {
	sec := loadFixtureSection(t)
	cohort := testRows(t, sec, "cohort_metrics")[0]
	cohort["rate_label"] = "banana"
	vs := violationsByInvariant(checkSection(sec))
	if len(vs[invRateLabel]) == 0 {
		t.Fatalf("unknown rate_label must trip %s", invRateLabel)
	}
}

// TestInvariantLaneSet pins the canonical 5-lane 0/D/R/F/S set and order.
func TestInvariantLaneSet(t *testing.T) {
	t.Run("wrong lane value", func(t *testing.T) {
		sec := loadFixtureSection(t)
		testRows(t, sec, "lane_ledger")[4]["lane"] = "X"
		vs := violationsByInvariant(checkSection(sec))
		if len(vs[invLaneSet]) == 0 {
			t.Fatalf("renamed lane must trip %s", invLaneSet)
		}
	})
	t.Run("wrong order", func(t *testing.T) {
		sec := loadFixtureSection(t)
		rows := testRows(t, sec, "lane_ledger")
		rows[0]["lane"], rows[1]["lane"] = rows[1]["lane"], rows[0]["lane"]
		vs := violationsByInvariant(checkSection(sec))
		if len(vs[invLaneSet]) == 0 {
			t.Fatalf("reordered lanes must trip %s", invLaneSet)
		}
	})
	t.Run("escape table too", func(t *testing.T) {
		sec := loadFixtureSection(t)
		testRows(t, sec, "escape_rate_by_lane")[4]["lane"] = "X"
		vs := violationsByInvariant(checkSection(sec))
		if len(vs[invLaneSet]) == 0 {
			t.Fatalf("renamed escape lane must trip %s", invLaneSet)
		}
	})
}

// TestInvariantCohortSet pins the canonical 3 provenance cohorts.
func TestInvariantCohortSet(t *testing.T) {
	sec := loadFixtureSection(t)
	testRows(t, sec, "cohort_metrics")[1]["cohort"] = "surprise"
	vs := violationsByInvariant(checkSection(sec))
	if len(vs[invCohortSet]) == 0 {
		t.Fatalf("renamed cohort must trip %s", invCohortSet)
	}
}

// TestInvariantConservation covers counter conservation: lane buckets sum to
// the lane total, cohorts and lanes describe the same landing set, escape
// rows mirror the ledger, and counters are non-negative integers.
func TestInvariantConservation(t *testing.T) {
	t.Run("lane bucket sum mismatch", func(t *testing.T) {
		sec := loadFixtureSection(t)
		testRows(t, sec, "lane_ledger")[3]["f0_direct"] = float64(26)
		vs := violationsByInvariant(checkSection(sec))
		if len(vs[invConservation]) == 0 {
			t.Fatalf("lane bucket drift must trip %s", invConservation)
		}
	})
	t.Run("landing set mismatch", func(t *testing.T) {
		sec := loadFixtureSection(t)
		testRows(t, sec, "lane_ledger")[3]["total"] = float64(61)
		vs := violationsByInvariant(checkSection(sec))
		if len(vs[invConservation]) == 0 {
			t.Fatalf("Σ lane totals vs Σ cohort commits drift must trip %s", invConservation)
		}
	})
	t.Run("escape mirror mismatch", func(t *testing.T) {
		sec := loadFixtureSection(t)
		testRows(t, sec, "escape_rate_by_lane")[3]["revert_fixup_commits"] = float64(4)
		vs := violationsByInvariant(checkSection(sec))
		if len(vs[invConservation]) == 0 {
			t.Fatalf("escape/ledger divergence must trip %s", invConservation)
		}
	})
	t.Run("rework set mismatch", func(t *testing.T) {
		sec := loadFixtureSection(t)
		testRows(t, sec, "cohort_metrics")[0]["revert_fixup_commits"] = float64(4)
		vs := violationsByInvariant(checkSection(sec))
		if len(vs[invConservation]) == 0 {
			t.Fatalf("Σ lane vs Σ cohort revert/fixup drift must trip %s", invConservation)
		}
	})
	t.Run("negative counter", func(t *testing.T) {
		sec := loadFixtureSection(t)
		testRows(t, sec, "lane_ledger")[0]["receipt_backed"] = float64(-1)
		vs := violationsByInvariant(checkSection(sec))
		if len(vs[invConservation]) == 0 {
			t.Fatalf("negative counter must trip %s", invConservation)
		}
	})
	t.Run("non-integral counter", func(t *testing.T) {
		sec := loadFixtureSection(t)
		testRows(t, sec, "cohort_metrics")[0]["commits"] = 52.5
		vs := violationsByInvariant(checkSection(sec))
		if len(vs[invConservation]) == 0 {
			t.Fatalf("non-integral counter must trip %s", invConservation)
		}
	})
}

// TestInvariantSandboxSum covers the sandbox enforcement metric contract:
// conserving buckets, or an explicit error — never both, never silent.
func TestInvariantSandboxSum(t *testing.T) {
	t.Run("bucket sum mismatch", func(t *testing.T) {
		sec := loadFixtureSection(t)
		sandbox, ok := sec["sandbox_enforcement"].(map[string]any)
		if !ok {
			t.Fatalf("fixture sandbox_enforcement not an object")
		}
		sandbox["refused"] = float64(6)
		vs := violationsByInvariant(checkSection(sec))
		if len(vs[invSandboxSum]) == 0 {
			t.Fatalf("bucket drift must trip %s", invSandboxSum)
		}
	})
	t.Run("summary and error together", func(t *testing.T) {
		sec := loadFixtureSection(t)
		sec["sandbox_enforcement_error"] = "tasks database missing"
		vs := violationsByInvariant(checkSection(sec))
		if len(vs[invStructure]) == 0 {
			t.Fatal("sandbox_enforcement + sandbox_enforcement_error must trip I_STRUCTURE")
		}
	})
	t.Run("silently absent", func(t *testing.T) {
		sec := loadFixtureSection(t)
		delete(sec, "sandbox_enforcement")
		vs := violationsByInvariant(checkSection(sec))
		if len(vs[invStructure]) == 0 {
			t.Fatal("silent sandbox metric drop must trip I_STRUCTURE")
		}
	})
	t.Run("explicit error alone is clean", func(t *testing.T) {
		sec := loadFixtureSection(t)
		delete(sec, "sandbox_enforcement")
		sec["sandbox_enforcement_error"] = "tasks database missing at /tmp/g8s.db"
		for _, v := range checkSection(sec) {
			if v.Invariant == invStructure || v.Invariant == invSandboxSum {
				t.Fatalf("explicit sandbox degrade must be clean of %s: %+v", v.Invariant, v)
			}
		}
	})
}

// TestInvariantMinSampleDeclared pins the merged WP2 threshold (20) — the
// harness never invents thresholds.
func TestInvariantMinSampleDeclared(t *testing.T) {
	t.Run("drifted threshold", func(t *testing.T) {
		sec := loadFixtureSection(t)
		sec["min_sample"] = float64(10)
		vs := violationsByInvariant(checkSection(sec))
		if len(vs[invMinSample]) == 0 {
			t.Fatalf("min_sample drift must trip %s", invMinSample)
		}
	})
	t.Run("missing threshold", func(t *testing.T) {
		sec := loadFixtureSection(t)
		delete(sec, "min_sample")
		vs := violationsByInvariant(checkSection(sec))
		if len(vs[invMinSample]) == 0 {
			t.Fatalf("missing min_sample must trip %s", invMinSample)
		}
	})
}

// TestInvariantDegradeExplicit pins the explicit-degradation contract: a
// git_log_error must pair with empty metric arrays; arrays without an error
// must not be empty.
func TestInvariantDegradeExplicit(t *testing.T) {
	t.Run("error with non-empty arrays", func(t *testing.T) {
		sec := loadFixtureSection(t)
		sec["git_log_error"] = "git log provenance window: exit 128"
		vs := violationsByInvariant(checkSection(sec))
		if len(vs[invDegradeExplicit]) == 0 {
			t.Fatalf("git_log_error with data must trip %s", invDegradeExplicit)
		}
	})
	t.Run("error with empty arrays is clean", func(t *testing.T) {
		sec := loadFixtureSection(t)
		sec["git_log_error"] = "git log provenance window: exit 128"
		sec["lane_ledger"] = []any{}
		sec["cohort_metrics"] = []any{}
		sec["escape_rate_by_lane"] = []any{}
		for _, v := range checkSection(sec) {
			if v.Invariant == invDegradeExplicit {
				t.Fatalf("explicit git degrade must be clean of %s: %+v", invDegradeExplicit, v)
			}
		}
	})
	t.Run("empty arrays without error", func(t *testing.T) {
		sec := loadFixtureSection(t)
		sec["lane_ledger"] = []any{}
		sec["cohort_metrics"] = []any{}
		sec["escape_rate_by_lane"] = []any{}
		vs := violationsByInvariant(checkSection(sec))
		if len(vs[invDegradeExplicit]) == 0 {
			t.Fatalf("empty arrays without git_log_error must trip %s", invDegradeExplicit)
		}
	})
}

// TestCheckCrossRun covers the over-time invariants: frozen git window at the
// same HEAD, append-only sandbox counters, and tolerated HEAD movement.
func TestCheckCrossRun(t *testing.T) {
	base := runState{
		Head:          "abcdef123456",
		LaneTotals:    map[string]int64{"0": 4, "D": 30, "R": 22, "F": 60, "S": 24},
		CohortCommits: map[string]int64{"receipt_backed": 52, "f0_direct": 55, "unclassified": 33},
		Sandbox:       &sandboxCounts{Attempts: 100, Kernel: 60, Degraded: 10, Refused: 5, Unclassified: 25},
	}
	t.Run("frozen window holds", func(t *testing.T) {
		if vs := checkCrossRun(base, base); len(vs) != 0 {
			t.Fatalf("identical runs must be clean: %+v", vs)
		}
	})
	t.Run("frozen window drift", func(t *testing.T) {
		cur := base
		cur.LaneTotals = map[string]int64{"0": 4, "D": 30, "R": 22, "F": 61, "S": 24}
		vs := violationsByInvariant(checkCrossRun(base, cur))
		if len(vs[invWindowFrozen]) == 0 {
			t.Fatalf("frozen-window drift must trip %s", invWindowFrozen)
		}
	})
	t.Run("sandbox shrink", func(t *testing.T) {
		cur := base
		cur.Sandbox = &sandboxCounts{Attempts: 90, Kernel: 55, Degraded: 10, Refused: 5, Unclassified: 20}
		vs := violationsByInvariant(checkCrossRun(base, cur))
		if len(vs[invCounterMonotone]) == 0 {
			t.Fatalf("shrinking append-only counters must trip %s", invCounterMonotone)
		}
	})
	t.Run("sandbox growth tolerated", func(t *testing.T) {
		cur := base
		cur.Sandbox = &sandboxCounts{Attempts: 110, Kernel: 65, Degraded: 10, Refused: 5, Unclassified: 30}
		if vs := checkCrossRun(base, cur); len(vs) != 0 {
			t.Fatalf("append-only growth must be clean: %+v", vs)
		}
	})
	t.Run("head moved tolerated", func(t *testing.T) {
		cur := base
		cur.Head = "ffffff000000"
		cur.LaneTotals = map[string]int64{"0": 5, "D": 31, "R": 22, "F": 61, "S": 24}
		cur.CohortCommits = map[string]int64{"receipt_backed": 53, "f0_direct": 55, "unclassified": 34}
		if vs := checkCrossRun(base, cur); len(vs) != 0 {
			t.Fatalf("HEAD movement must skip the frozen-window check: %+v", vs)
		}
	})
}
