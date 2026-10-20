package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type StatusOutput struct {
	Repo           string        `json:"repo"`
	Branch         string        `json:"branch"`
	SHA            string        `json:"sha"`
	Dirty          bool          `json:"dirty"`
	Mandate        *StatusMandate `json:"mandate,omitempty"`
	EvidenceAtHead []Entry       `json:"evidence_at_head"`
	Checks         []ForgeCheck  `json:"checks,omitempty"`
}

type StatusMandate struct {
	ID        string `json:"id"`
	Campaign  string `json:"campaign"`
	Spent     int    `json:"spent"`
	Limit     int    `json:"limit"`
	ExpiresAt string `json:"expires_at"`
}

func statusCommand(args []string) int {
	var repoDir string
	var pr int
	var reportBlock bool
	var jsonOutput bool

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--repo":
			if i+1 < len(args) {
				repoDir = args[i+1]
				i++
			}
		case "--pr":
			if i+1 < len(args) {
				if n, err := strconv.Atoi(args[i+1]); err == nil {
					pr = n
				}
				i++
			}
		case "--report-block":
			reportBlock = true
		case "--json":
			jsonOutput = true
		}
	}

	if repoDir == "" {
		repoDir = "."
	}
	absRepo, err := filepath.Abs(repoDir)
	if err != nil {
		absRepo = repoDir
	}

	repoRoot, branch, sha, dirty, _ := getGitState(absRepo)
	cfg, _ := LoadConfig(absRepo)

	// Mandate
	var statusMandate *StatusMandate
	m, err := LoadActiveMandate()
	if err == nil && m != nil {
		spent, _ := MandateBudgetSpent(m)
		statusMandate = &StatusMandate{
			ID:        m.ID,
			Campaign:  m.Campaign,
			Spent:     spent,
			Limit:     m.ErrorBudget.Limit,
			ExpiresAt: m.ExpiresAt,
		}
	}

	// Evidence at HEAD
	var evidenceAtHead []Entry
	if ledger, err := OpenRepoLedger(absRepo); err == nil {
		entries, _ := ledger.ReadEntries()
		for _, e := range entries {
			if sha != "" && (strings.HasPrefix(e.SHA, sha) || strings.HasPrefix(sha, e.SHA)) {
				evidenceAtHead = append(evidenceAtHead, e)
			}
		}
	}

	// CI Checks if PR given and CI != none
	var forgeChecks []ForgeCheck
	if pr > 0 && cfg != nil && cfg.CI != "none" {
		forge, _ := GetForgeAdapter(absRepo)
		if forge != nil {
			checks, _ := forge.ListChecks(absRepo, pr, sha)
			forgeChecks = checks
		}
	}

	if reportBlock {
		fmt.Println("<!-- f0:status -->")
		fmt.Println("### F0 Ground Truth Status")
		fmt.Printf("- **Repo**: `%s`\n", repoRoot)
		fmt.Printf("- **Commit**: `%s`\n", sha)
		if dirty {
			fmt.Println("- **Dirty**: YES")
		} else {
			fmt.Println("- **Dirty**: NO")
		}
		if statusMandate != nil {
			fmt.Printf("- **Mandate**: `%s` (budget %d/%d spent, expires %s)\n", statusMandate.ID, statusMandate.Spent, statusMandate.Limit, statusMandate.ExpiresAt)
		}
		if len(evidenceAtHead) > 0 {
			fmt.Println("- **Evidence**:")
			for _, e := range evidenceAtHead {
				fmt.Printf("  - `%s`: %s %s (items=%d, %dms)\n", e.ID, e.Kind, e.Status, e.ItemsChecked, e.DurationMS)
			}
		}
		if len(forgeChecks) > 0 {
			fmt.Println("- **CI Checks**:")
			for _, c := range forgeChecks {
				fmt.Printf("  - %s: %s (%s)\n", c.Name, c.State, c.Bucket)
			}
		}
		fmt.Println("<!-- /f0:status -->")
		return 0
	}

	st := StatusOutput{
		Repo:           repoRoot,
		Branch:         branch,
		SHA:            sha,
		Dirty:          dirty,
		Mandate:        statusMandate,
		EvidenceAtHead: evidenceAtHead,
		Checks:         forgeChecks,
	}

	if jsonOutput {
		if st.EvidenceAtHead == nil {
			st.EvidenceAtHead = []Entry{}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(st); err != nil {
			fmt.Fprintf(os.Stderr, "f0 status: encoding json: %v\n", err)
			return 1
		}
		return 0
	}

	// Plain text terminal output
	fmt.Printf("Repository:     %s\n", repoRoot)
	fmt.Printf("Branch:         %s\n", branch)
	fmt.Printf("Commit SHA:     %s\n", sha)
	fmt.Printf("Working Tree:   ")
	if dirty {
		fmt.Printf("DIRTY (uncommitted changes)\n")
	} else {
		fmt.Printf("clean\n")
	}
	if statusMandate != nil {
		fmt.Printf("Active Mandate: %s (campaign: %q, error budget: %d/%d, expires: %s)\n",
			statusMandate.ID, statusMandate.Campaign, statusMandate.Spent, statusMandate.Limit, statusMandate.ExpiresAt)
	} else {
		fmt.Printf("Active Mandate: none\n")
	}
	fmt.Printf("Evidence at HEAD: %d entries\n", len(evidenceAtHead))
	for _, e := range evidenceAtHead {
		fmt.Printf("  - %s: %s %s [%s]\n", e.ID, e.Kind, e.Status, strings.Join(e.Cmd, " "))
	}
	if len(forgeChecks) > 0 {
		fmt.Printf("CI Checks (PR #%d):\n", pr)
		for _, c := range forgeChecks {
			fmt.Printf("  - %s: %s (%s)\n", c.Name, c.State, c.Bucket)
		}
	}

	return 0
}
