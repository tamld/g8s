package probe

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/routing"
)

// benchmarkFixture represents a single task routing benchmark fixture.
type benchmarkFixture struct {
	Name         string
	Prompt       string
	Paths        []string
	Summary      string
	ExpectedRole string // empty when deterministic rule is ambiguous (record-only)
}

// BenchmarkSummary holds the aggregated metrics of the M3 real-Jev benchmark.
type BenchmarkSummary struct {
	Fixtures             int     `json:"fixtures"`
	DeterministicMeanMs  int64   `json:"deterministic_mean_ms"`
	RealJevMeanMs        int64   `json:"real_jev_mean_ms"`
	JevAcceptanceRate    float64 `json:"jev_acceptance_rate"`
	FallbackRate         float64 `json:"fallback_rate"`
	PlacementDiffersRate float64 `json:"placement_differs_rate"`
	WrongRoleRate        float64 `json:"wrong_role_rate"`
	Errors               int     `json:"errors"`
}

func checkEnvGuard(t *testing.T) (string, []string) {
	t.Helper()

	var missing []string
	if os.Getenv("G8S_M3_REAL_JEV") != "1" {
		missing = append(missing, "G8S_M3_REAL_JEV=1")
	}

	endpoint := strings.TrimSpace(os.Getenv("TYPESAFE_ENDPOINT"))
	if endpoint == "" {
		missing = append(missing, "TYPESAFE_ENDPOINT")
	}

	apiKeys := strings.TrimSpace(os.Getenv("TYPESAFE_API_KEYS"))
	apiKey := strings.TrimSpace(os.Getenv("TYPESAFE_API_KEY"))
	if apiKeys == "" && apiKey == "" {
		missing = append(missing, "TYPESAFE_API_KEYS or TYPESAFE_API_KEY")
	}

	if len(missing) > 0 {
		t.Skipf("skipping M3 real Jev benchmark: missing required environment variable(s): %s", strings.Join(missing, ", "))
	}

	var keys []string
	addKey := func(k string) {
		k = strings.TrimSpace(k)
		if k == "" {
			return
		}
		for _, existing := range keys {
			if existing == k {
				return
			}
		}
		keys = append(keys, k)
	}

	if apiKeys != "" {
		for _, k := range strings.Split(apiKeys, ",") {
			addKey(k)
		}
	}
	addKey(apiKey)
	addKey(os.Getenv("TYPESAFE_API_KEY_FALLBACK"))

	return endpoint, keys
}

func truncatePrompt(p string) string {
	p = strings.TrimSpace(p)
	const maxLen = 60
	if len(p) <= maxLen {
		return p
	}
	return p[:maxLen-3] + "..."
}

func sanitize(s string, keys []string) string {
	for _, k := range keys {
		if k != "" {
			s = strings.ReplaceAll(s, k, "[REDACTED]")
		}
	}
	return s
}

func roundRate(r float64) float64 {
	return math.Round(r*10000) / 10000
}

func m3BenchmarkFixtures() []benchmarkFixture {
	return []benchmarkFixture{
		// --- 2.a Synthetic fixtures (from RoutingProbes' shapes) ---
		{
			Name:         "syn-docs-only",
			Prompt:       "Review and update documentation",
			Paths:        []string{"docs/decisions/0030-context-router.md", "README.md"},
			Summary:      "Update documentation for context routing architecture",
			ExpectedRole: "summarizer", // Rule 2 unambiguous: docs-only paths route to summarizer
		},
		{
			Name:         "syn-trust-boundary-receipt",
			Prompt:       "Verify receipt issuance security checks",
			Paths:        []string{"internal/receipt/verifier.go", "internal/receipt/receipt.go"},
			Summary:      "Harden receipt signature verification logic",
			ExpectedRole: "test-runner", // Rule 1 unambiguous: trust-boundary internal/receipt/* routes to test-runner
		},
		{
			Name:         "syn-test-file-paths",
			Prompt:       "Update unit test cases for configuration loading",
			Paths:        []string{"internal/config/config_test.go", "internal/routing/router_test.go"},
			Summary:      "Add test coverage for config loading edge cases",
			ExpectedRole: "", // router.go deterministic rule table has no dedicated rule for test files; ambiguous (record-only)
		},
		{
			Name:         "syn-mixed-docs-code",
			Prompt:       "CLI entrypoint documentation and handler updates",
			Paths:        []string{"docs/index.md", "cmd/g8s/main.go"},
			Summary:      "Sync CLI documentation with main entrypoint changes",
			ExpectedRole: "", // Mixed docs and code paths; ambiguous (record-only)
		},
		{
			Name:         "syn-empty-paths-prose",
			Prompt:       "General repository inspection and discovery",
			Paths:        []string{},
			Summary:      "Perform repository-wide audit of deprecated packages",
			ExpectedRole: "", // Empty path inventory with prose summary; ambiguous (record-only)
		},
		{
			Name:         "syn-unknown-extensions",
			Prompt:       "Analyze unmatched filesystem paths",
			Paths:        []string{"nonexistent/unmatched/file.xyz"},
			Summary:      "Inspect files with unknown extensions",
			ExpectedRole: "", // Unknown file extensions with fallback; ambiguous (record-only)
		},
		{
			Name:         "syn-hotfix-marker-summary",
			Prompt:       "Apply urgent patch to production worker process",
			Paths:        []string{"internal/worker/worker.go"},
			Summary:      "HOTFIX: emergency fix for zombie worker process leaks",
			ExpectedRole: "", // Deterministic router ignores summary text; ambiguous (record-only)
		},
		{
			Name:         "syn-refactor-declared-summary",
			Prompt:       "Refactor telemetry pipeline interfaces",
			Paths:        []string{"internal/telemetry/telemetry.go"},
			Summary:      "REFACTOR: decouple event distillation from database transactions",
			ExpectedRole: "", // Deterministic router ignores summary text; ambiguous (record-only)
		},

		// --- 2.b Real campaign corpus (Wave G/H receipt path shapes, verbatim) ---
		{
			Name:         "real-docs-round",
			Prompt:       "Read the brief at plans/261002-wave-g/brief-docs-verifier.md and execute it exactly. Synchronize verifier autonomy guide.",
			Paths:        []string{"docs/user-guide/verifier-and-autonomy.md", "README.md"},
			Summary:      "Wave G docs round: update verifier autonomy guide and root README",
			ExpectedRole: "", // Record-only as required
		},
		{
			Name:         "real-test-slice",
			Prompt:       "Read the brief at plans/261002-wave-g/brief-test-resubmit.md and execute it exactly. Add redtest coverage for retry and resubmit.",
			Paths:        []string{"internal/autopilot/redtest_retry_test.go", "cmd/g8s/redtest_resubmit_test.go"},
			Summary:      "Wave G test slice: redteam autopilot retry and resubmit flows",
			ExpectedRole: "", // Record-only as required
		},
		{
			Name:         "real-fix-slice",
			Prompt:       "Read the brief at plans/261003-wave-h/brief-fix-controlplane.md and execute it exactly. Fix controlplane race in resubmit.",
			Paths:        []string{"internal/controlplane/*", "cmd/g8s/resubmit.go", "internal/autopilot/redtest_retry_test.go", "cmd/g8s/redtest_resubmit_test.go"},
			Summary:      "Wave H fix slice: stabilize controlplane signals and resubmit retry loop",
			ExpectedRole: "", // Record-only as required
		},
		{
			Name:         "real-harness-slice",
			Prompt:       "Read the brief at plans/261003-wave-h/brief-harness-isolation.md and execute it exactly. Harden worker harness containment.",
			Paths:        []string{"internal/harness/*"},
			Summary:      "Wave H harness slice: enforce process isolation in worker harness",
			ExpectedRole: "", // Record-only as required
		},
		{
			Name:         "real-wc-slice",
			Prompt:       "Read the brief at plans/261003-wave-h/brief-settings-merger.md and execute it exactly. Update settings loader and merger script.",
			Paths:        []string{"internal/settings/settings.go", "internal/settings/settings_test.go", "tools/merger.sh", "tools/merger_test.sh"},
			Summary:      "Wave H W+C slice: synchronize settings configuration with merger tooling",
			ExpectedRole: "", // Record-only as required
		},
		{
			Name:         "real-multi-slice",
			Prompt:       "Read the brief at plans/261004-wave-h/brief-verify-lane.md and execute it exactly. Verify release gates across CLI and lane detection.",
			Paths:        []string{"internal/verifier/verifier.go", "cmd/g8s/verify.go", "internal/verifier/redtest_verifier_test.go", "tools/merger.sh", "tools/ci_lane_detect.sh"},
			Summary:      "Wave H multi-slice: end-to-end receipt verification and lane router integration",
			ExpectedRole: "", // Record-only as required
		},
	}
}

func TestM3RealJevBenchmark(t *testing.T) {
	endpoint, keys := checkEnvGuard(t)

	fixtures := m3BenchmarkFixtures()
	totalFixtures := len(fixtures)
	if totalFixtures == 0 {
		t.Fatal("no benchmark fixtures defined")
	}

	manifest := routing.DefaultManifest()

	var (
		totalDetLatencyMs  int64
		totalJevLatencyMs  int64
		jevAcceptedCount   int
		fallbackCount      int
		placementDiffCount int
		wrongRoleCount     int
		errorCount         int
	)

	for i, fix := range fixtures {
		req := routing.RouteRequest{
			Prompt:   fix.Prompt,
			Paths:    fix.Paths,
			Summary:  fix.Summary,
			Manifest: manifest,
		}

		// 1. Deterministic pass
		detStart := time.Now()
		detDec, detErr := routing.Route(context.Background(), req)
		detLatencyMs := time.Since(detStart).Milliseconds()
		if detErr != nil {
			t.Fatalf("fixture %q deterministic Route failed: %v", fix.Name, detErr)
		}
		totalDetLatencyMs += detLatencyMs

		// 2. Real-Jev pass with 10s context timeout
		jevCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		jevCtx = routing.WithJevAssist(jevCtx, endpoint, keys...)
		jevStart := time.Now()
		jevDec, jevErr := routing.Route(jevCtx, req)
		jevLatencyMs := time.Since(jevStart).Milliseconds()
		cancel()
		totalJevLatencyMs += jevLatencyMs

		var jevErrStr string
		if jevErr != nil {
			jevErrStr = sanitize(jevErr.Error(), keys)
			errorCount++
			// Transport error: count fixture as fallback, deterministic decision stands
			jevDec = detDec
			jevDec.IsFallback = true
			jevDec.Source = "deterministic"
		} else if jevDec.IsFallback {
			if strings.Contains(jevDec.Reason, "Jev assist unavailable") {
				jevErrStr = sanitize(jevDec.Reason, keys)
				errorCount++
			}
		}

		jevAccepted := (jevDec.Source == "jev")
		isFallback := jevDec.IsFallback || !jevAccepted
		placementDiffers := (jevDec.Provider != detDec.Provider || jevDec.Model != detDec.Model)

		if jevAccepted {
			jevAcceptedCount++
			if fix.ExpectedRole != "" && jevDec.Role != fix.ExpectedRole {
				wrongRoleCount++
			}
		}
		if isFallback {
			fallbackCount++
		}
		if placementDiffers {
			placementDiffCount++
		}

		// Per-fixture log: prompt strictly truncated to <= 60 chars, secrets sanitized
		t.Logf("[%02d/%02d] %s: det=[%s/%s/%s %dms] jev=[%s/%s/%s %dms accepted=%v fallback=%v placement_differs=%v] prompt=%q",
			i+1, totalFixtures, fix.Name,
			detDec.Provider, detDec.Model, detDec.Role, detLatencyMs,
			jevDec.Provider, jevDec.Model, jevDec.Role, jevLatencyMs,
			jevAccepted, isFallback, placementDiffers,
			truncatePrompt(fix.Prompt),
		)
		if jevErrStr != "" {
			t.Logf("[%02d/%02d] %s: jev_error=%s", i+1, totalFixtures, fix.Name, jevErrStr)
		}
	}

	detMeanMs := int64(math.Round(float64(totalDetLatencyMs) / float64(totalFixtures)))
	jevMeanMs := int64(math.Round(float64(totalJevLatencyMs) / float64(totalFixtures)))

	summary := BenchmarkSummary{
		Fixtures:             totalFixtures,
		DeterministicMeanMs:  detMeanMs,
		RealJevMeanMs:        jevMeanMs,
		JevAcceptanceRate:    roundRate(float64(jevAcceptedCount) / float64(totalFixtures)),
		FallbackRate:         roundRate(float64(fallbackCount) / float64(totalFixtures)),
		PlacementDiffersRate: roundRate(float64(placementDiffCount) / float64(totalFixtures)),
		WrongRoleRate:        roundRate(float64(wrongRoleCount) / float64(totalFixtures)),
		Errors:               errorCount,
	}

	summaryBytes, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("failed to marshal benchmark summary: %v", err)
	}

	// Summary JSON printed via t.Logf (one compact object)
	t.Logf("%s", string(summaryBytes))
}
