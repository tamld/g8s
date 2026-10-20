package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func waiveCommand(args []string) int {
	var repoDir string
	var pr int
	var checkName string
	var issueURL string
	var reason string
	var headSHA string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--repo":
			if i+1 < len(args) {
				repoDir = args[i+1]
				i++
			}
		case "--pr":
			if i+1 < len(args) {
				n, err := strconv.Atoi(args[i+1])
				if err != nil {
					fmt.Fprintf(os.Stderr, "f0 waive: invalid --pr %q\n", args[i+1])
					return 1
				}
				pr = n
				i++
			}
		case "--check":
			if i+1 < len(args) {
				checkName = args[i+1]
				i++
			}
		case "--issue":
			if i+1 < len(args) {
				issueURL = args[i+1]
				i++
			}
		case "--reason":
			if i+1 < len(args) {
				reason = args[i+1]
				i++
			}
		case "--head-sha":
			if i+1 < len(args) {
				headSHA = args[i+1]
				i++
			}
		}
	}

	if repoDir == "" {
		repoDir = "."
	}
	absRepo, err := filepath.Abs(repoDir)
	if err != nil {
		absRepo = repoDir
	}

	if pr <= 0 {
		fmt.Fprintf(os.Stderr, "f0 waive: --pr <N> is required\n")
		return 1
	}
	if checkName == "" {
		fmt.Fprintf(os.Stderr, "f0 waive: --check <name> is required\n")
		return 1
	}
	if issueURL == "" {
		fmt.Fprintf(os.Stderr, "f0 waive: --issue <url> is required\n")
		return 1
	}
	if reason == "" {
		fmt.Fprintf(os.Stderr, "f0 waive: --reason <text> is required\n")
		return 1
	}

	// Resolve head SHA if not explicitly provided
	if headSHA == "" {
		forge, _ := GetForgeAdapter(absRepo)
		if forge != nil {
			sha, _, _, _ := forge.ResolvePR(absRepo, pr)
			headSHA = sha
		}
	}
	if headSHA == "" {
		out, err := exec.Command("git", "-C", absRepo, "rev-parse", "HEAD").Output()
		if err == nil {
			headSHA = strings.TrimSpace(string(out))
		}
	}

	ledger, err := OpenRepoLedger(absRepo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "f0 waive: opening repo ledger: %v\n", err)
		return 1
	}

	entry := &Entry{
		ID:           newEvidenceID(),
		Ts:           time.Now().UTC().Format(time.RFC3339),
		Kind:         "waiver",
		Repo:         absRepo,
		SHA:          headSHA,
		Cmd:          []string{"waive", "--pr", strconv.Itoa(pr), "--check", checkName},
		Status:       "PASS",
		ItemsChecked: 1,
		Extra: map[string]any{
			"pr":       pr,
			"check":    checkName,
			"issue":    issueURL,
			"reason":   reason,
			"head_sha": headSHA,
		},
	}

	if err := ledger.Append(entry); err != nil {
		fmt.Fprintf(os.Stderr, "f0 waive: appending to ledger: %v\n", err)
		return 1
	}

	// Record 1 debit in global ledger
	var mandateID string
	if m, _ := LoadActiveMandate(); m != nil {
		mandateID = m.ID
	}
	debitReason := fmt.Sprintf("waiver: %s (%s)", checkName, issueURL)
	if err := RecordDebit(absRepo, debitReason, mandateID); err != nil {
		fmt.Fprintf(os.Stderr, "f0 waive: warning: failed to record debit: %v\n", err)
	}

	fmt.Printf("[f0] waiver recorded: %s for PR #%d check %q (%s)\n", entry.ID, pr, checkName, issueURL)
	return 0
}
