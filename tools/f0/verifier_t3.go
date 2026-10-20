package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func init() {
	commands["verify-t3"] = verifyT3Command
	commands["t3-verify"] = verifyT3Command
}

// T3VerifyOptions configures T3 independent second verification.
type T3VerifyOptions struct {
	RepoDir            string
	HeadSHA            string
	BaseRef            string
	F0Provider         string
	AvailableProviders []string
	ProviderRunner     func(provider, repoDir, diff string) (passed bool, detail string, err error)
	EvalProbeRunner    func(repoDir string) (passed bool, detail string, err error)
}

// T3Verdict captures the outcome of T3 verification.
type T3Verdict struct {
	Status           string `json:"status"` // "PASS", "VETO_ESCALATE"
	Approved         bool   `json:"approved"`
	Veto             bool   `json:"veto"`
	EscalateToPiC    bool   `json:"escalate_to_pic"`
	SelectedVerifier string `json:"selected_verifier,omitempty"`
	Reason           string `json:"reason"`
}

// SelectIndependentProvider chooses an available provider that is independent from F0's provider.
// Provider names are normalized (trimmed and lowercased). F0's provider, "none", and "" are excluded.
// The first matching independent provider is returned.
func SelectIndependentProvider(f0Provider string, available []string) (string, bool) {
	normF0 := strings.ToLower(strings.TrimSpace(f0Provider))
	for _, p := range available {
		trimmed := strings.TrimSpace(p)
		normP := strings.ToLower(trimmed)
		if normP == "" || normP == "none" {
			continue
		}
		if normP == normF0 {
			continue
		}
		return trimmed, true
	}
	return "", false
}

// RunT3Verification executes the T3 independent second verifier workflow (#628).
func RunT3Verification(opts T3VerifyOptions) (T3Verdict, error) {
	repoDir := opts.RepoDir
	if repoDir == "" {
		repoDir = "."
	}
	absRepo, err := filepath.Abs(repoDir)
	if err != nil {
		absRepo = repoDir
	}

	headSHA := opts.HeadSHA
	if headSHA == "" {
		if out, err := exec.Command("git", "-C", absRepo, "rev-parse", "HEAD").Output(); err == nil {
			headSHA = strings.TrimSpace(string(out))
		}
	}

	baseRef := opts.BaseRef
	if baseRef == "" {
		baseRef = findDefaultBranch(absRepo)
	}

	// Compute diff
	var diff string
	if headSHA != "" && baseRef != "" {
		diffCmd := exec.Command("git", "-C", absRepo, "diff", baseRef+"..."+headSHA)
		diffOut, err := diffCmd.Output()
		if err != nil {
			diffCmd = exec.Command("git", "-C", absRepo, "diff", baseRef, headSHA)
			diffOut, _ = diffCmd.Output()
		}
		diff = string(diffOut)
	}

	var selectedVerifier string
	var passed bool
	var detail string
	var verifierRan bool

	independentProvider, ok := SelectIndependentProvider(opts.F0Provider, opts.AvailableProviders)
	if ok && opts.ProviderRunner != nil {
		selectedVerifier = independentProvider
		p, d, err := opts.ProviderRunner(independentProvider, absRepo, diff)
		if err != nil {
			p = false
			if d == "" {
				d = err.Error()
			}
		}
		passed = p
		detail = d
		verifierRan = true
	} else if opts.EvalProbeRunner != nil {
		selectedVerifier = "eval-probes"
		p, d, err := opts.EvalProbeRunner(absRepo)
		if err != nil {
			p = false
			if d == "" {
				d = err.Error()
			}
		}
		passed = p
		detail = d
		verifierRan = true
	}

	if !verifierRan {
		return T3Verdict{
			Status:        "VETO_ESCALATE",
			Approved:      false,
			Veto:          true,
			EscalateToPiC: true,
			Reason:        "no independent second verifier available (automatic veto + escalate to PiC per D9)",
		}, nil
	}

	ledger, _ := OpenRepoLedger(absRepo)

	if !passed {
		if detail == "" {
			detail = fmt.Sprintf("independent verifier %q rejected changes", selectedVerifier)
		}
		verdict := T3Verdict{
			Status:           "VETO_ESCALATE",
			Approved:         false,
			Veto:             true,
			EscalateToPiC:    true,
			SelectedVerifier: selectedVerifier,
			Reason:           detail,
		}
		if ledger != nil {
			entry := &Entry{
				ID:           newEvidenceID(),
				Ts:           time.Now().UTC().Format(time.RFC3339),
				Kind:         "t3-verify",
				Repo:         absRepo,
				SHA:          headSHA,
				Cmd:          []string{"verify-t3"},
				Status:       "FAIL",
				ItemsChecked: 1,
				Extra: map[string]any{
					"selected_verifier": selectedVerifier,
					"reason":            detail,
					"approved":          false,
					"veto":              true,
					"escalate_to_pic":   true,
				},
			}
			_ = ledger.Append(entry)
		}
		return verdict, nil
	}

	if detail == "" {
		detail = fmt.Sprintf("independent verifier %q approved changes", selectedVerifier)
	}
	verdict := T3Verdict{
		Status:           "PASS",
		Approved:         true,
		Veto:             false,
		EscalateToPiC:    false,
		SelectedVerifier: selectedVerifier,
		Reason:           detail,
	}
	if ledger != nil {
		entry := &Entry{
			ID:           newEvidenceID(),
			Ts:           time.Now().UTC().Format(time.RFC3339),
			Kind:         "t3-verify",
			Repo:         absRepo,
			SHA:          headSHA,
			Cmd:          []string{"verify-t3"},
			Status:       "PASS",
			ItemsChecked: 1,
			Extra: map[string]any{
				"selected_verifier": selectedVerifier,
				"reason":            detail,
				"approved":          true,
				"veto":              false,
				"escalate_to_pic":   false,
			},
		}
		_ = ledger.Append(entry)
	}

	return verdict, nil
}

func verifyT3Command(args []string) int {
	var opts T3VerifyOptions
	var jsonOutput bool

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "check", "run":
			// optional subcommand
		case "--repo":
			if i+1 < len(args) {
				opts.RepoDir = args[i+1]
				i++
			}
		case "--head-sha":
			if i+1 < len(args) {
				opts.HeadSHA = args[i+1]
				i++
			}
		case "--base":
			if i+1 < len(args) {
				opts.BaseRef = args[i+1]
				i++
			}
		case "--f0-provider":
			if i+1 < len(args) {
				opts.F0Provider = args[i+1]
				i++
			}
		case "--available-providers":
			if i+1 < len(args) {
				for _, p := range strings.Split(args[i+1], ",") {
					p = strings.TrimSpace(p)
					if p != "" {
						opts.AvailableProviders = append(opts.AvailableProviders, p)
					}
				}
				i++
			}
		case "--json":
			jsonOutput = true
		}
	}

	if opts.F0Provider == "" {
		if envP := os.Getenv("F0_PROVIDER"); envP != "" {
			opts.F0Provider = envP
		} else if envP := os.Getenv("G8S_F0_PROVIDER"); envP != "" {
			opts.F0Provider = envP
		}
	}
	if len(opts.AvailableProviders) == 0 {
		if envAvail := os.Getenv("G8S_AVAILABLE_PROVIDERS"); envAvail != "" {
			for _, p := range strings.Split(envAvail, ",") {
				p = strings.TrimSpace(p)
				if p != "" {
					opts.AvailableProviders = append(opts.AvailableProviders, p)
				}
			}
		}
	}

	verdict, err := RunT3Verification(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "f0 verify-t3: %v\n", err)
		return 1
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(verdict)
	} else {
		fmt.Printf("T3 Verification: %s\n", verdict.Status)
		if verdict.SelectedVerifier != "" {
			fmt.Printf("Verifier: %s\n", verdict.SelectedVerifier)
		}
		if verdict.Reason != "" {
			fmt.Printf("Reason: %s\n", verdict.Reason)
		}
	}

	if verdict.Approved {
		return 0
	}
	return 1
}
