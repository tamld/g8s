package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func manualCommand(args []string) int {
	var repoDir string
	var file string
	var reason string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--repo":
			if i+1 < len(args) {
				repoDir = args[i+1]
				i++
			}
		case "--file":
			if i+1 < len(args) {
				file = args[i+1]
				i++
			}
		case "--reason":
			if i+1 < len(args) {
				reason = args[i+1]
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

	if file == "" {
		fmt.Fprintf(os.Stderr, "f0 manual: --file <rel> is required\n")
		return 1
	}
	if reason == "" {
		fmt.Fprintf(os.Stderr, "f0 manual: --reason <text> is required\n")
		return 1
	}

	// Compute git blob hash
	cmd := exec.Command("git", "-C", absRepo, "hash-object", file)
	out, err := cmd.Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "f0 manual: computing blob hash for %s: %v\n", file, err)
		return 1
	}
	blobHash := strings.TrimSpace(string(out))

	var headSHA string
	headOut, err := exec.Command("git", "-C", absRepo, "rev-parse", "HEAD").Output()
	if err == nil {
		headSHA = strings.TrimSpace(string(headOut))
	}

	ledger, err := OpenRepoLedger(absRepo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "f0 manual: opening repo ledger: %v\n", err)
		return 1
	}

	entry := &Entry{
		ID:           newEvidenceID(),
		Ts:           time.Now().UTC().Format(time.RFC3339),
		Kind:         "manual",
		Repo:         absRepo,
		SHA:          headSHA,
		Cmd:          []string{"manual", "--file", file},
		Status:       "PASS",
		ItemsChecked: 1,
		Extra: map[string]any{
			"file":   file,
			"blob":   blobHash,
			"reason": reason,
		},
	}

	if err := ledger.Append(entry); err != nil {
		fmt.Fprintf(os.Stderr, "f0 manual: appending to ledger: %v\n", err)
		return 1
	}

	// Check manual_free policy
	cfg, err := LoadConfig(absRepo)
	if err != nil {
		cfg = &Config{}
	}

	isFree := false
	for _, pat := range cfg.ManualFree {
		if pathMatches(file, pat) {
			isFree = true
			break
		}
	}

	if !isFree {
		var mandateID string
		if m, _ := LoadActiveMandate(); m != nil {
			mandateID = m.ID
		}
		debitReason := fmt.Sprintf("manual: %s: %s", file, reason)
		if err := RecordDebit(absRepo, debitReason, mandateID); err != nil {
			fmt.Fprintf(os.Stderr, "f0 manual: warning: failed to record debit: %v\n", err)
		}
	}

	fmt.Printf("[f0] manual tag recorded: %s for %s (blob %s, free=%v)\n", entry.ID, file, blobHash, isFree)
	return 0
}
