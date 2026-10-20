package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
)

// UnattestedCommit represents a commit lacking both a gate PASS entry and an anchor trailer.
type UnattestedCommit struct {
	SHA     string   `json:"sha"`
	Parents []string `json:"parents"`
	Subject string   `json:"subject"`
}

func auditCommand(args []string) int {
	var repoDir string
	var since string
	var jsonOutput bool
	var strict bool

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--repo":
			if i+1 < len(args) {
				repoDir = args[i+1]
				i++
			}
		case "--since":
			if i+1 < len(args) {
				since = args[i+1]
				i++
			}
		case "--json":
			jsonOutput = true
		case "--strict":
			strict = true
		}
	}

	if repoDir == "" {
		repoDir = "."
	}
	absRepo, err := filepath.Abs(repoDir)
	if err != nil {
		absRepo = repoDir
	}

	branch := findDefaultBranch(absRepo)

	gitArgs := []string{"-C", absRepo, "log", "--format=%H%x00%P%x00%s%x00%B%x1f"}
	if since != "" {
		gitArgs = append(gitArgs, "--since="+since)
	}
	gitArgs = append(gitArgs, branch)

	cmd := exec.Command("git", gitArgs...)
	out, err := cmd.Output()
	if err != nil {
		// No commits or git log error
		if jsonOutput {
			fmt.Println("[]")
		} else {
			fmt.Println("[f0] audit: no commits found or error reading git log")
		}
		return 0
	}

	// Read repo ledger for gate PASS entries
	var gatePassSHAs []string
	ledger, err := OpenRepoLedger(absRepo)
	if err == nil {
		entries, _ := ledger.ReadEntries()
		for _, e := range entries {
			if e.Kind == "gate" && e.Status == "PASS" && e.SHA != "" {
				gatePassSHAs = append(gatePassSHAs, e.SHA)
			}
		}
	}

	matchesGatePass := func(sha string) bool {
		for _, gSHA := range gatePassSHAs {
			if strings.HasPrefix(sha, gSHA) || strings.HasPrefix(gSHA, sha) {
				return true
			}
		}
		return false
	}

	var unattested []UnattestedCommit
	rawCommits := bytes.Split(out, []byte{0x1f})

	for _, raw := range rawCommits {
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 {
			continue
		}

		parts := bytes.Split(raw, []byte{0x00})
		if len(parts) < 4 {
			continue
		}

		sha := string(parts[0])
		parentStr := string(parts[1])
		subject := string(parts[2])
		body := string(parts[3])

		var parents []string
		if strings.TrimSpace(parentStr) != "" {
			parents = strings.Fields(parentStr)
		}

		// 1. Check anchor trailer in commit body
		if anchorRegex.MatchString(body) {
			continue // Attested via trailer
		}

		// 2. Check gate PASS entry for commit SHA
		if matchesGatePass(sha) {
			continue // Attested via direct gate entry
		}

		// 3. Check gate PASS entry for any parent SHA
		parentMatched := false
		for _, p := range parents {
			if matchesGatePass(p) {
				parentMatched = true
				break
			}
		}
		if parentMatched {
			continue // Attested via parent gate entry
		}

		unattested = append(unattested, UnattestedCommit{
			SHA:     sha,
			Parents: parents,
			Subject: subject,
		})
	}

	if jsonOutput {
		if unattested == nil {
			unattested = []UnattestedCommit{}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(unattested); err != nil {
			fmt.Fprintf(os.Stderr, "f0 audit: encoding json: %v\n", err)
			return 1
		}
	} else {
		if len(unattested) == 0 {
			fmt.Println("[f0] audit: all default-branch commits attested")
		} else {
			fmt.Printf("[f0] audit: %d un-attested commit(s) found on %s:\n", len(unattested), branch)
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "SHA\tPARENTS\tSUBJECT")
			for _, c := range unattested {
				shortSHA := c.SHA
				if len(shortSHA) > 7 {
					shortSHA = shortSHA[:7]
				}
				parentsStr := strings.Join(c.Parents, ",")
				if parentsStr == "" {
					parentsStr = "root"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\n", shortSHA, parentsStr, c.Subject)
			}
			_ = w.Flush()
		}
	}

	if len(unattested) > 0 && strict {
		return 1
	}
	return 0
}
