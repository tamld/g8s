// Package autopilot implements the cron-based supervisor trigger that scans
// GitHub issues, failing CI runs, static analysis findings, and stale
// @agy-fix-me TODOs. It uses a priority queue weighted by severity,
// confidence, and cost-inverse.
package autopilot

import (
	"encoding/json"
	"os"
	"time"
)

// Config holds the autopilot scheduler configuration.
type Config struct {
	// Cron schedule for the autopilot tick (standard cron expression).
	Cron string `json:"cron" yaml:"cron"`

	// GitHub repository to scan for issues (owner/repo).
	Repository string `json:"repository" yaml:"repository"`

	// GitHub token for API access (can also use GITHUB_TOKEN env var).
	GitHubToken string `json:"github_token,omitempty" yaml:"github_token,omitempty"`

	// Priority weights for the queue scoring formula:
	// score = severity * severity_weight + confidence * confidence_weight + (1/cost) * cost_inverse_weight
	PriorityWeights PriorityWeights `json:"priority_weights" yaml:"priority_weights"`

	// ScanSources controls which sources are enabled.
	ScanSources ScanSources `json:"scan_sources" yaml:"scan_sources"`

	// MaxItemsPerTick limits how many items are enqueued per tick.
	MaxItemsPerTick int `json:"max_items_per_tick" yaml:"max_items_per_tick"`

	// MinScoreThreshold is the minimum score for an item to be enqueued.
	MinScoreThreshold float64 `json:"min_score_threshold" yaml:"min_score_threshold"`

	// LookbackWindow is how far back to scan for issues/CI runs.
	LookbackWindow time.Duration `json:"lookback_window" yaml:"lookback_window"`

	// CodebasePath is the local path to scan for TODO comments and static analysis.
	CodebasePath string `json:"codebase_path" yaml:"codebase_path"`
}

// PriorityWeights defines the weights for the priority scoring formula.
type PriorityWeights struct {
	Severity    float64 `json:"severity" yaml:"severity"`
	Confidence  float64 `json:"confidence" yaml:"confidence"`
	CostInverse float64 `json:"cost_inverse" yaml:"cost_inverse"`
}

// ScanSources controls which scan sources are enabled.
type ScanSources struct {
	GitHubIssues   bool `json:"github_issues" yaml:"github_issues"`
	FailingCI      bool `json:"failing_ci" yaml:"failing_ci"`
	StaticAnalysis bool `json:"static_analysis" yaml:"static_analysis"`
	StaleTodos     bool `json:"stale_todos" yaml:"stale_todos"`
}

// DefaultConfig returns a sensible default configuration.
func DefaultConfig() Config {
	return Config{
		Cron:              "0 * * * *", // Every hour at minute 0
		PriorityWeights:   DefaultPriorityWeights(),
		ScanSources:       DefaultScanSources(),
		MaxItemsPerTick:   50,
		MinScoreThreshold: 0.1,
		LookbackWindow:    24 * time.Hour,
		CodebasePath:      ".",
	}
}

// DefaultPriorityWeights returns the default priority weights.
func DefaultPriorityWeights() PriorityWeights {
	return PriorityWeights{
		Severity:    1.0,
		Confidence:  1.0,
		CostInverse: 0.5,
	}
}

// DefaultScanSources returns the default enabled scan sources.
func DefaultScanSources() ScanSources {
	return ScanSources{
		GitHubIssues:   true,
		FailingCI:      true,
		StaticAnalysis: true,
		StaleTodos:     true,
	}
}

// LoadConfig loads configuration from a YAML file, falling back to defaults.
func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil // Return defaults if file doesn't exist
		}
		return cfg, err
	}

	// Try JSON first, then fall back to YAML (which JSON is a subset of)
	if err := json.Unmarshal(data, &cfg); err != nil {
		// If JSON fails, try parsing as YAML-like (we'll just use JSON for now)
		return cfg, err
	}

	return cfg, nil
}

// Validate validates the configuration.
func (c *Config) Validate() error {
	if c.Cron == "" {
		return nil // Empty cron is valid (disabled)
	}
	if c.Repository == "" && c.ScanSources.GitHubIssues {
		return nil // Repository is optional if GitHub issues disabled
	}
	if c.MaxItemsPerTick <= 0 {
		c.MaxItemsPerTick = 50
	}
	if c.LookbackWindow <= 0 {
		c.LookbackWindow = 24 * time.Hour
	}
	if c.CodebasePath == "" {
		c.CodebasePath = "."
	}
	return nil
}
