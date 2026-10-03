// Package routing — routes tasks to providers, models, and roles using deterministic rules and optional LLM assistance.
package routing

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tamld/g8s/internal/analyzer"
	"github.com/tamld/g8s/internal/config"
)

// Decision represents the task router's placement outcome.
type Decision struct {
	Provider   string  `json:"provider"`
	Model      string  `json:"model"`
	Role       string  `json:"role"`
	Source     string  `json:"source"` // "deterministic" | "jev"
	IsFallback bool    `json:"is_fallback"`
	Reason     string  `json:"reason"` // human-readable rule/suggestion basis
	Confidence float64 `json:"confidence"`
}

// RouteRequest carries task context for provider/model/role assignment.
type RouteRequest struct {
	Prompt      string       `json:"prompt"`
	Paths       []string     `json:"paths"` // payload/target paths, may be empty
	Summary     string       `json:"summary"`
	TimeoutHint string       `json:"timeout_hint"`
	Manifest    *config.File `json:"manifest,omitempty"` // providers.json, may be nil → catalog defaults
}

type candidateModel struct {
	provider      string
	model         string
	contextWindow int
}

// DefaultManifest returns the baseline catalog configuration when no manifest is provided.
func DefaultManifest() *config.File {
	return &config.File{
		Providers: []config.ProviderEntry{
			{
				Name:  "agy",
				Class: "platform_dispatch",
				Models: []config.ModelEntry{
					{ID: "gemini-3.8-flash-high", ContextWindow: 1000000},
				},
			},
			{
				Name:  "claude",
				Class: "platform_dispatch",
				Models: []config.ModelEntry{
					{ID: "claude-haiku-4-5", ContextWindow: 200000},
				},
			},
			{
				Name:  "ollama",
				Class: "platform_dispatch",
				Models: []config.ModelEntry{
					{ID: "llama3.1", ContextWindow: 128000},
				},
			},
		},
	}
}

func extractCandidates(manifest *config.File) []candidateModel {
	if manifest == nil || len(manifest.Providers) == 0 {
		manifest = DefaultManifest()
	}

	var candidates []candidateModel
	for _, p := range manifest.Providers {
		for _, m := range p.Models {
			candidates = append(candidates, candidateModel{
				provider:      p.Name,
				model:         m.ID,
				contextWindow: m.ContextWindow,
			})
		}
	}

	if len(candidates) == 0 {
		candidates = append(candidates, candidateModel{
			provider:      "agy",
			model:         "gemini-3.8-flash-high",
			contextWindow: 1000000,
		})
	}
	return candidates
}

func modelCostRank(modelID string) int {
	id := strings.ToLower(modelID)
	switch {
	case strings.Contains(id, "flash-lite") || strings.Contains(id, "flash_lite"):
		return 10
	case strings.Contains(id, "haiku") || strings.Contains(id, "mini") || strings.Contains(id, "nano"):
		return 20
	case strings.Contains(id, "flash") || strings.Contains(id, "small") || strings.Contains(id, "lite"):
		return 30
	case strings.Contains(id, "llama") || strings.Contains(id, "local"):
		return 40
	case strings.Contains(id, "pro") || strings.Contains(id, "sonnet"):
		return 70
	case strings.Contains(id, "opus") || strings.Contains(id, "large") || strings.Contains(id, "high"):
		return 90
	default:
		return 50
	}
}

func cheapestModel(candidates []candidateModel) candidateModel {
	sorted := make([]candidateModel, len(candidates))
	copy(sorted, candidates)

	sort.SliceStable(sorted, func(i, j int) bool {
		rI := modelCostRank(sorted[i].model)
		rJ := modelCostRank(sorted[j].model)
		if rI != rJ {
			return rI < rJ
		}
		// If rank is equal, prefer smaller positive context window
		cwI := sorted[i].contextWindow
		cwJ := sorted[j].contextWindow
		if cwI > 0 && cwJ > 0 && cwI != cwJ {
			return cwI < cwJ
		}
		if sorted[i].model != sorted[j].model {
			return sorted[i].model < sorted[j].model
		}
		return sorted[i].provider < sorted[j].provider
	})

	return sorted[0]
}

func largestContextModel(candidates []candidateModel) candidateModel {
	sorted := make([]candidateModel, len(candidates))
	copy(sorted, candidates)

	sort.SliceStable(sorted, func(i, j int) bool {
		cwI := sorted[i].contextWindow
		cwJ := sorted[j].contextWindow
		if cwI != cwJ {
			return cwI > cwJ
		}
		if sorted[i].model != sorted[j].model {
			return sorted[i].model < sorted[j].model
		}
		return sorted[i].provider < sorted[j].provider
	})

	return sorted[0]
}

func defaultModel(candidates []candidateModel) candidateModel {
	for _, c := range candidates {
		if c.provider == "agy" && c.model == "gemini-3.8-flash-high" {
			return c
		}
	}
	for _, c := range candidates {
		if c.provider == "agy" {
			return c
		}
	}
	return candidates[0]
}

func isDocsPath(p string) bool {
	clean := filepath.ToSlash(filepath.Clean(p))
	clean = strings.TrimPrefix(clean, "./")
	clean = strings.TrimPrefix(clean, "/")
	lower := strings.ToLower(clean)
	if strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".txt") || strings.HasSuffix(lower, ".rst") {
		return true
	}
	if clean == "docs" || strings.HasPrefix(clean, "docs/") {
		return true
	}
	return false
}

func isTrustBoundaryPath(p string) bool {
	clean := filepath.ToSlash(filepath.Clean(p))
	clean = strings.TrimPrefix(clean, "./")
	clean = strings.TrimPrefix(clean, "/")
	for _, prefix := range []string{"internal/harness", "internal/receipt", "internal/controlplane"} {
		if clean == prefix || strings.HasPrefix(clean, prefix+"/") {
			return true
		}
	}
	return false
}

// RouteDeterministic evaluates Layer 1 zero-network deterministic routing rules.
func RouteDeterministic(req RouteRequest) Decision {
	candidates := extractCandidates(req.Manifest)

	// Rule 1: Trust-boundary paths (highest governance priority)
	hasTrustPath := false
	for _, p := range req.Paths {
		if isTrustBoundaryPath(p) {
			hasTrustPath = true
			break
		}
	}
	if !hasTrustPath && len(req.Paths) == 0 {
		promptLower := strings.ToLower(req.Prompt)
		if strings.Contains(promptLower, "internal/harness") ||
			strings.Contains(promptLower, "internal/receipt") ||
			strings.Contains(promptLower, "internal/controlplane") {
			hasTrustPath = true
		}
	}
	if hasTrustPath {
		best := largestContextModel(candidates)
		return Decision{
			Provider:   best.provider,
			Model:      best.model,
			Role:       "test-runner",
			Source:     "deterministic",
			IsFallback: false,
			Reason:     "trust-boundary path requires largest-context model and test-runner role",
			Confidence: 1.0,
		}
	}

	// Rule 2: Docs-only paths
	if len(req.Paths) > 0 {
		allDocs := true
		for _, p := range req.Paths {
			if !isDocsPath(p) {
				allDocs = false
				break
			}
		}
		if allDocs {
			best := cheapestModel(candidates)
			return Decision{
				Provider:   best.provider,
				Model:      best.model,
				Role:       "summarizer",
				Source:     "deterministic",
				IsFallback: false,
				Reason:     "documentation paths route to summarizer role with lightweight model",
				Confidence: 1.0,
			}
		}
	}

	// Rule 3: High blast radius code changes (fail-open check on existing files)
	hasHighBlastRadius := false
	for _, p := range req.Paths {
		if _, err := os.Stat(p); err == nil {
			if an, err := analyzer.NewAnalyzer(""); err == nil {
				if rep, err := an.AnalyzeFileImpact(p); err == nil {
					if rep.RiskLevel == "HIGH" || rep.RiskLevel == "CRITICAL" {
						hasHighBlastRadius = true
						break
					}
				}
			}
		}
	}
	if hasHighBlastRadius {
		best := largestContextModel(candidates)
		return Decision{
			Provider:   best.provider,
			Model:      best.model,
			Role:       "collector",
			Source:     "deterministic",
			IsFallback: false,
			Reason:     "high blast radius code impact requires large-context model",
			Confidence: 0.9,
		}
	}

	// Rule 4: Huge timeout hint
	if req.TimeoutHint != "" {
		if d, err := time.ParseDuration(req.TimeoutHint); err == nil && d >= 30*time.Minute {
			best := defaultModel(candidates)
			return Decision{
				Provider:   best.provider,
				Model:      best.model,
				Role:       "collector",
				Source:     "deterministic",
				IsFallback: false,
				Reason:     fmt.Sprintf("extended timeout hint %s requires high-capacity model", req.TimeoutHint),
				Confidence: 0.9,
			}
		}
	}

	// Rule 5: Manifest default fallback
	def := defaultModel(candidates)
	return Decision{
		Provider:   def.provider,
		Model:      def.model,
		Role:       "collector",
		Source:     "deterministic",
		IsFallback: false,
		Reason:     "manifest default routing",
		Confidence: 0.85,
	}
}

// Route assigns provider, model, and role using deterministic rules first,
// with optional Jev assistance if configured.
func Route(ctx context.Context, req RouteRequest) (Decision, error) {
	layer1 := RouteDeterministic(req)

	mode := getRouterMode(ctx)
	if mode != "jev_assisted" {
		return layer1, nil
	}

	candidates := extractCandidates(req.Manifest)
	return jevAssist(ctx, req, layer1, candidates)
}
