package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// ParserRule specifies a declarative command parser rule for .g8s/f0.yml.
type ParserRule struct {
	Name           string   `yaml:"name" json:"name"`
	ArgvPrefix     []string `yaml:"argv_prefix" json:"argv_prefix"`
	CountRegex     string   `yaml:"count_regex,omitempty" json:"count_regex,omitempty"`
	VacuousRegex   string   `yaml:"vacuous_regex,omitempty" json:"vacuous_regex,omitempty"`
	PassOnExitZero bool     `yaml:"pass_on_exit_zero,omitempty" json:"pass_on_exit_zero,omitempty"`
}

// Config represents repository-level policy loaded from .g8s/f0.yml.
type Config struct {
	CI             string       `yaml:"ci" json:"ci"`
	VerifyCommands [][]string   `yaml:"verify_commands" json:"verify_commands"`
	ManualFree     []string     `yaml:"manual_free" json:"manual_free"`
	Parsers        []ParserRule `yaml:"parsers,omitempty" json:"parsers,omitempty"`
}

func defaultCI(repoDir string) string {
	out, err := exec.Command("git", "-C", repoDir, "remote", "get-url", "origin").Output()
	if err == nil && strings.Contains(string(out), "github.com") {
		return "github"
	}
	return "none"
}

func defaultVerifyCommands(repoDir string) [][]string {
	var cmds [][]string
	if _, err := os.Stat(filepath.Join(repoDir, "go.mod")); err == nil {
		cmds = append(cmds, []string{"go", "test", "./..."})
	}
	if _, err := os.Stat(filepath.Join(repoDir, "Cargo.toml")); err == nil {
		cmds = append(cmds, []string{"cargo", "test"})
	}
	hasPyproject := false
	if _, err := os.Stat(filepath.Join(repoDir, "pyproject.toml")); err == nil {
		hasPyproject = true
	}
	if _, err := os.Stat(filepath.Join(repoDir, "pytest.ini")); err == nil {
		hasPyproject = true
	}
	if hasPyproject {
		cmds = append(cmds,
			[]string{"pytest"},
			[]string{"python3", "-m", "pytest"},
			[]string{"uv", "run", "pytest"},
		)
	}
	if _, err := os.Stat(filepath.Join(repoDir, "package.json")); err == nil {
		cmds = append(cmds,
			[]string{"npm", "test"},
			[]string{"pnpm", "test"},
			[]string{"node", "--test"},
			[]string{"npx", "vitest", "run"},
			[]string{"npx", "jest"},
		)
	}
	if len(cmds) == 0 {
		return [][]string{}
	}
	return cmds
}

func defaultManualFree(repoDir string) []string {
	if filepath.Base(repoDir) == "g8s" {
		return []string{"plans/**", "docs/**"}
	}
	return []string{}
}

// LoadConfig loads the repo policy file .g8s/f0.yml or applies defaults.
func LoadConfig(repoDir string) (*Config, error) {
	if repoDir == "" {
		repoDir = "."
	}
	absRepo, err := filepath.Abs(repoDir)
	if err == nil {
		repoDir = absRepo
	}

	cfgPath := filepath.Join(repoDir, ".g8s", "f0.yml")
	data, err := os.ReadFile(cfgPath)
	if os.IsNotExist(err) {
		return &Config{
			CI:             defaultCI(repoDir),
			VerifyCommands: defaultVerifyCommands(repoDir),
			ManualFree:     defaultManualFree(repoDir),
			Parsers:        []ParserRule{},
		}, nil
	} else if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	if cfg.CI == "" {
		cfg.CI = defaultCI(repoDir)
	}
	if len(cfg.VerifyCommands) == 0 {
		cfg.VerifyCommands = defaultVerifyCommands(repoDir)
	}
	if cfg.ManualFree == nil {
		cfg.ManualFree = []string{}
	}
	if cfg.Parsers == nil {
		cfg.Parsers = []ParserRule{}
	}

	return &cfg, nil
}
