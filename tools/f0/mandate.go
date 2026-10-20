package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tamld/g8s/internal/lane"
)

// MandateRepo specifies an authorized repository under a mandate.
type MandateRepo struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	MaxTier string `json:"max_tier"` // T0, T1, T2, T3
}

// ErrorBudget bounds how many debits can be recorded before mandate drops to T0.
type ErrorBudget struct {
	Limit int `json:"limit"`
}

// Mandate is the machine-readable authorization contract issued by F0.
type Mandate struct {
	Version       int           `json:"version"`
	ID            string        `json:"id"` // "md-" + 8 hex
	Issuer        string        `json:"issuer"`
	IssuedAt      string        `json:"issued_at"`  // RFC3339 UTC
	ExpiresAt     string        `json:"expires_at"` // RFC3339 UTC
	Campaign      string        `json:"campaign"`
	Repos         []MandateRepo `json:"repos"`
	ExcludedPaths []string      `json:"excluded_paths"`
	ErrorBudget   ErrorBudget   `json:"error_budget"`
}

// MandateCheckResult represents the verdict of an action authorization check.
type MandateCheckResult struct {
	RequiredTier string `json:"required_tier"`
	MaxTier      string `json:"max_tier"`
	Allowed      bool   `json:"allowed"`
	Reason       string `json:"reason"`
}

func newMandateID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("md-%08x", time.Now().UnixNano()&0xffffffff)
	}
	return "md-" + hex.EncodeToString(b)
}

// MandateCachePath returns the path to the mandate cache file:
// ~/.local/state/g8s/f0/mandate.json
func MandateCachePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home dir: %w", err)
	}
	return filepath.Join(home, ".local", "state", "g8s", "f0", "mandate.json"), nil
}

func normalizeMandate(m *Mandate) {
	if m.Repos == nil {
		m.Repos = []MandateRepo{}
	}
	if m.ExcludedPaths == nil {
		m.ExcludedPaths = []string{}
	}
}

// IssueMandate records the mandate in the global ledger and writes the cache file.
func IssueMandate(m *Mandate) error {
	normalizeMandate(m)

	ledger, err := OpenGlobalLedger()
	if err != nil {
		return fmt.Errorf("opening global ledger: %w", err)
	}

	entry := &Entry{
		ID:           m.ID,
		Ts:           m.IssuedAt,
		Kind:         "mandate-issue",
		Cmd:          []string{"mandate", "issue"},
		Exit:         0,
		Status:       "PASS",
		ItemsChecked: len(m.Repos),
		Extra:        map[string]any{"mandate": m},
	}

	if err := ledger.Append(entry); err != nil {
		return fmt.Errorf("appending mandate to ledger: %w", err)
	}

	cachePath, err := MandateCachePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0755); err != nil {
		return fmt.Errorf("creating mandate cache dir: %w", err)
	}

	cacheData, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling mandate cache: %w", err)
	}

	if err := os.WriteFile(cachePath, append(cacheData, '\n'), 0644); err != nil {
		return fmt.Errorf("writing mandate cache file: %w", err)
	}

	return nil
}

// LoadActiveMandate loads the latest mandate from the global ledger and verifies cache integrity.
func LoadActiveMandate() (*Mandate, error) {
	ledger, err := OpenGlobalLedger()
	if err != nil {
		return nil, fmt.Errorf("opening global ledger: %w", err)
	}

	entries, err := ledger.ReadEntries()
	if err != nil {
		return nil, fmt.Errorf("reading global ledger: %w", err)
	}

	var latestEntry *Entry
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Kind == "mandate-issue" {
			latestEntry = &entries[i]
			break
		}
	}

	cachePath, err := MandateCachePath()
	if err != nil {
		return nil, err
	}

	cacheBytes, cacheErr := os.ReadFile(cachePath)

	if latestEntry == nil {
		if cacheErr == nil {
			return nil, errors.New("mandate cache tampered: cache exists without ledger entry")
		}
		return nil, nil
	}

	mandateRaw, err := json.Marshal(latestEntry.Extra["mandate"])
	if err != nil {
		return nil, fmt.Errorf("marshaling ledger mandate: %w", err)
	}
	var ledgerMandate Mandate
	if err := json.Unmarshal(mandateRaw, &ledgerMandate); err != nil {
		return nil, fmt.Errorf("unmarshaling ledger mandate: %w", err)
	}
	normalizeMandate(&ledgerMandate)

	if cacheErr == nil {
		var cacheMandate Mandate
		if err := json.Unmarshal(cacheBytes, &cacheMandate); err != nil {
			return nil, fmt.Errorf("mandate cache tampered: invalid json in cache: %w", err)
		}
		normalizeMandate(&cacheMandate)

		canonLedger, err := json.Marshal(ledgerMandate)
		if err != nil {
			return nil, fmt.Errorf("marshaling canonical ledger mandate: %w", err)
		}
		canonCache, err := json.Marshal(cacheMandate)
		if err != nil {
			return nil, fmt.Errorf("marshaling canonical cache mandate: %w", err)
		}
		if !bytes.Equal(canonLedger, canonCache) {
			return nil, errors.New("mandate cache tampered: cache file differs from global ledger entry")
		}
	}

	return &ledgerMandate, nil
}

// MandateBudgetSpent calculates the number of debit entries recorded since the mandate was issued.
func MandateBudgetSpent(m *Mandate) (int, error) {
	if m == nil {
		return 0, nil
	}

	ledger, err := OpenGlobalLedger()
	if err != nil {
		return 0, fmt.Errorf("opening global ledger: %w", err)
	}

	entries, err := ledger.ReadEntries()
	if err != nil {
		return 0, fmt.Errorf("reading global ledger: %w", err)
	}

	issuedAt, err := time.Parse(time.RFC3339, m.IssuedAt)
	if err != nil {
		return 0, fmt.Errorf("parsing mandate issued_at: %w", err)
	}

	spent := 0
	for _, e := range entries {
		if e.Kind != "debit" {
			continue
		}

		entryTs, err := time.Parse(time.RFC3339, e.Ts)
		if err != nil {
			if e.Ts < m.IssuedAt {
				continue
			}
		} else {
			if entryTs.Before(issuedAt) {
				continue
			}
		}

		if len(e.Extra) > 0 {
			if mid, ok := e.Extra["mandate_id"].(string); ok && mid != "" {
				if mid != m.ID {
					continue
				}
			}
		}
		spent++
	}

	return spent, nil
}

// RecordDebit appends a debit entry to the global ledger.
func RecordDebit(repo, reason, mandateID string) error {
	ledger, err := OpenGlobalLedger()
	if err != nil {
		return err
	}

	extra := map[string]any{
		"reason": reason,
	}
	if mandateID != "" {
		extra["mandate_id"] = mandateID
	}

	entry := &Entry{
		ID:           newEvidenceID(),
		Ts:           time.Now().UTC().Format(time.RFC3339),
		Kind:         "debit",
		Repo:         repo,
		Cmd:          []string{"mandate", "debit"},
		Status:       "PASS",
		ItemsChecked: 1,
		Extra:        extra,
	}

	return ledger.Append(entry)
}

func pathMatches(cleanFile, pattern string) bool {
	cleanFile = filepath.ToSlash(filepath.Clean(cleanFile))
	cleanPattern := filepath.ToSlash(filepath.Clean(pattern))

	// 1. Exact path match
	if cleanFile == cleanPattern {
		return true
	}

	// 1a. Globstar ("**")
	if strings.Contains(cleanPattern, "**") {
		return globstarMatch(
			strings.Split(cleanFile, "/"),
			strings.Split(cleanPattern, "/"),
		)
	}

	// 2. Directory boundary match: pattern ends in /* or / or is a bare directory name
	if strings.HasSuffix(pattern, "/*") || strings.HasSuffix(pattern, "/") {
		dirPrefix := strings.TrimSuffix(cleanPattern, "*")
		if !strings.HasSuffix(dirPrefix, "/") {
			dirPrefix += "/"
		}
		if strings.HasPrefix(cleanFile, dirPrefix) {
			return true
		}
	} else if !strings.Contains(cleanPattern, "*") {
		if strings.HasPrefix(cleanFile, cleanPattern+"/") {
			return true
		}
	}

	// 3. Glob matching
	m, _ := path.Match(cleanPattern, cleanFile)
	return m
}

func globstarMatch(fileSegs, patSegs []string) bool {
	if len(patSegs) == 0 {
		return len(fileSegs) == 0
	}
	if patSegs[0] == "**" {
		for i := 0; i <= len(fileSegs); i++ {
			if globstarMatch(fileSegs[i:], patSegs[1:]) {
				return true
			}
		}
		return false
	}
	if len(fileSegs) == 0 {
		return false
	}
	m, _ := path.Match(patSegs[0], fileSegs[0])
	if !m {
		return false
	}
	return globstarMatch(fileSegs[1:], patSegs[1:])
}

// ClassifyActionTier determines the required tier for a given action and file set.
func ClassifyActionTier(repoPath, repoName, action string, paths []string, excludedPaths []string) string {
	switch action {
	case "push-branch", "open-pr", "comment", "close-issue", "close":
		return "T1"
	case "merge":
		// Escalated to T3 if ANY path matches excludedPaths
		for _, p := range paths {
			for _, pat := range excludedPaths {
				if pathMatches(p, pat) {
					return "T3"
				}
			}
		}

		// Or escalated to T3 via repo's trust boundaries
		absRepoPath := repoPath
		if abs, err := filepath.Abs(repoPath); err == nil {
			absRepoPath = abs
		}
		tbPath := filepath.Join(absRepoPath, ".g8s", "trust-boundaries.yml")
		if _, err := os.Stat(tbPath); err == nil {
			r := lane.NewRouter(lane.WithTrustBoundariesPath(tbPath))
			if r.Route(lane.Input{Paths: paths}) == lane.LaneSecurity {
				return "T3"
			}
		} else if repoName == "g8s" || filepath.Base(absRepoPath) == "g8s" {
			r := lane.NewRouter(lane.WithTrustBoundaries(lane.DefaultTrustBoundaries))
			if r.Route(lane.Input{Paths: paths}) == lane.LaneSecurity {
				return "T3"
			}
		}

		return "T2"
	case "publish", "release", "tag", "force-push", "delete-ref", "credential", "config", "edit-hooks":
		return "T4"
	default:
		return "T4"
	}
}

func tierRank(tier string) int {
	switch tier {
	case "T0":
		return 0
	case "T1":
		return 1
	case "T2":
		return 2
	case "T3":
		return 3
	case "T4":
		return 4
	default:
		return 999
	}
}

// CheckMandate verifies whether an action is permitted by the active mandate.
func CheckMandate(repoPath, action string, paths []string) (MandateCheckResult, error) {
	absRepoPath, err := filepath.Abs(repoPath)
	if err != nil {
		absRepoPath = repoPath
	}
	absRepoPath = filepath.Clean(absRepoPath)
	repoName := filepath.Base(absRepoPath)

	m, err := LoadActiveMandate()
	if err != nil {
		return MandateCheckResult{
			RequiredTier: ClassifyActionTier(absRepoPath, repoName, action, paths, nil),
			MaxTier:      "T0",
			Allowed:      false,
			Reason:       err.Error(),
		}, err
	}

	if m == nil {
		return MandateCheckResult{
			RequiredTier: ClassifyActionTier(absRepoPath, repoName, action, paths, nil),
			MaxTier:      "T0",
			Allowed:      false,
			Reason:       "no active mandate",
		}, nil
	}

	// Expiry check
	expiresAt, err := time.Parse(time.RFC3339, m.ExpiresAt)
	if err != nil || time.Now().UTC().After(expiresAt) {
		return MandateCheckResult{
			RequiredTier: ClassifyActionTier(absRepoPath, repoName, action, paths, m.ExcludedPaths),
			MaxTier:      "T0",
			Allowed:      false,
			Reason:       "mandate expired",
		}, nil
	}

	// Match repo
	var matchedRepo *MandateRepo
	for i := range m.Repos {
		r := &m.Repos[i]
		rAbs, _ := filepath.Abs(r.Path)
		rAbs = filepath.Clean(rAbs)
		if rAbs == absRepoPath || r.Path == repoPath {
			matchedRepo = r
			break
		}
	}
	if matchedRepo == nil {
		if out, err := exec.Command("git", "-C", absRepoPath, "rev-parse", "--show-toplevel").Output(); err == nil {
			gitRoot := filepath.Clean(strings.TrimSpace(string(out)))
			for i := range m.Repos {
				r := &m.Repos[i]
				rAbs, _ := filepath.Abs(r.Path)
				rAbs = filepath.Clean(rAbs)
				if rAbs == gitRoot {
					matchedRepo = r
					break
				}
			}
		}
	}
	if matchedRepo == nil {
		for i := range m.Repos {
			r := &m.Repos[i]
			if r.Name == repoPath || r.Name == repoName {
				matchedRepo = r
				break
			}
		}
	}

	if matchedRepo == nil {
		return MandateCheckResult{
			RequiredTier: ClassifyActionTier(absRepoPath, repoName, action, paths, m.ExcludedPaths),
			MaxTier:      "T0",
			Allowed:      false,
			Reason:       "repo not in mandate",
		}, nil
	}

	repoName = matchedRepo.Name
	requiredTier := ClassifyActionTier(absRepoPath, repoName, action, paths, m.ExcludedPaths)

	// Budget check
	spent, err := MandateBudgetSpent(m)
	if err != nil {
		return MandateCheckResult{
			RequiredTier: requiredTier,
			MaxTier:      "T0",
			Allowed:      false,
			Reason:       fmt.Sprintf("checking error budget: %v", err),
		}, err
	}
	if spent >= m.ErrorBudget.Limit {
		return MandateCheckResult{
			RequiredTier: requiredTier,
			MaxTier:      "T0",
			Allowed:      false,
			Reason:       "error budget exhausted",
		}, nil
	}

	effectiveMaxTier := matchedRepo.MaxTier
	allowed := tierRank(requiredTier) <= tierRank(effectiveMaxTier) && requiredTier != "T4"
	reason := "action permitted by mandate"
	if !allowed {
		if requiredTier == "T4" {
			reason = "action requires T4 which is never pre-authorized"
		} else {
			reason = fmt.Sprintf("action requires %s exceeding max tier %s", requiredTier, effectiveMaxTier)
		}
	}

	return MandateCheckResult{
		RequiredTier: requiredTier,
		MaxTier:      effectiveMaxTier,
		Allowed:      allowed,
		Reason:       reason,
	}, nil
}

func mandateCommand(args []string) int {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: f0 mandate <issue|show|check> [options]\n")
		return 1
	}

	switch args[0] {
	case "issue":
		return runMandateIssue(args[1:])
	case "show":
		return runMandateShow(args[1:])
	case "check":
		return runMandateCheck(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "f0 mandate: unknown subcommand %q\n", args[0])
		return 1
	}
}

func runMandateIssue(args []string) int {
	var issuer string
	var campaign string
	var repos []MandateRepo
	var excludedPaths []string
	budget := 3
	ttlStr := "24h"

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--issuer":
			if i+1 < len(args) {
				issuer = args[i+1]
				i++
			}
		case "--campaign":
			if i+1 < len(args) {
				campaign = args[i+1]
				i++
			}
		case "--repo":
			if i+1 < len(args) {
				repoArg := args[i+1]
				i++
				eq := strings.Index(repoArg, "=")
				if eq == -1 {
					fmt.Fprintf(os.Stderr, "f0 mandate: invalid --repo format %q (expected <name>=<path>:<tier>)\n", repoArg)
					return 1
				}
				name := repoArg[:eq]
				rest := repoArg[eq+1:]
				colon := strings.LastIndex(rest, ":")
				if colon == -1 {
					fmt.Fprintf(os.Stderr, "f0 mandate: invalid --repo format %q (expected <name>=<path>:<tier>)\n", repoArg)
					return 1
				}
				pathStr := rest[:colon]
				tier := rest[colon+1:]

				if tier == "T4" {
					fmt.Fprintf(os.Stderr, "f0 mandate: tier T4 is never pre-authorized (repo %s)\n", name)
					return 1
				}
				if tier != "T0" && tier != "T1" && tier != "T2" && tier != "T3" {
					fmt.Fprintf(os.Stderr, "f0 mandate: invalid tier %q: valid tiers are T0, T1, T2, T3 (T4 is never pre-authorized)\n", tier)
					return 1
				}

				absPath, err := filepath.Abs(pathStr)
				if err != nil {
					fmt.Fprintf(os.Stderr, "f0 mandate: resolving path for repo %s: %v\n", name, err)
					return 1
				}
				repos = append(repos, MandateRepo{
					Name:    name,
					Path:    filepath.Clean(absPath),
					MaxTier: tier,
				})
			}
		case "--exclude":
			if i+1 < len(args) {
				ex := args[i+1]
				i++
				parts := strings.Split(ex, ",")
				for _, p := range parts {
					p = strings.TrimSpace(p)
					if p != "" {
						excludedPaths = append(excludedPaths, p)
					}
				}
			}
		case "--budget":
			if i+1 < len(args) {
				b, err := strconv.Atoi(args[i+1])
				if err != nil || b < 0 {
					fmt.Fprintf(os.Stderr, "f0 mandate: invalid budget %q\n", args[i+1])
					return 1
				}
				budget = b
				i++
			}
		case "--ttl":
			if i+1 < len(args) {
				ttlStr = args[i+1]
				i++
			}
		}
	}

	if issuer == "" {
		fmt.Fprintf(os.Stderr, "f0 mandate: --issuer is required\n")
		return 1
	}
	if campaign == "" {
		fmt.Fprintf(os.Stderr, "f0 mandate: --campaign is required\n")
		return 1
	}
	if len(repos) == 0 {
		fmt.Fprintf(os.Stderr, "f0 mandate: at least one --repo is required\n")
		return 1
	}

	ttlDuration, err := time.ParseDuration(ttlStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "f0 mandate: invalid --ttl %q: %v\n", ttlStr, err)
		return 1
	}

	now := time.Now().UTC()
	m := &Mandate{
		Version:       1,
		ID:            newMandateID(),
		Issuer:        issuer,
		IssuedAt:      now.Format(time.RFC3339),
		ExpiresAt:     now.Add(ttlDuration).Format(time.RFC3339),
		Campaign:      campaign,
		Repos:         repos,
		ExcludedPaths: excludedPaths,
		ErrorBudget:   ErrorBudget{Limit: budget},
	}

	if err := IssueMandate(m); err != nil {
		fmt.Fprintf(os.Stderr, "f0 mandate issue: %v\n", err)
		return 1
	}

	fmt.Printf("[f0] mandate %s issued (%d repos, budget %d, expires %s)\n", m.ID, len(m.Repos), m.ErrorBudget.Limit, m.ExpiresAt)
	return 0
}

func runMandateShow(args []string) int {
	jsonOutput := false
	for _, a := range args {
		if a == "--json" {
			jsonOutput = true
		}
	}

	m, err := LoadActiveMandate()
	if err != nil {
		fmt.Fprintf(os.Stderr, "f0 mandate show: %v\n", err)
		return 1
	}

	if m == nil {
		if jsonOutput {
			fmt.Println("null")
		} else {
			fmt.Println("(no active mandate)")
		}
		return 0
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(m); err != nil {
			fmt.Fprintf(os.Stderr, "f0 mandate show: encoding json: %v\n", err)
			return 1
		}
		return 0
	}

	spent, _ := MandateBudgetSpent(m)
	fmt.Printf("Mandate:       %s\n", m.ID)
	fmt.Printf("Issuer:        %s\n", m.Issuer)
	fmt.Printf("Campaign:      %s\n", m.Campaign)
	fmt.Printf("Issued:        %s\n", m.IssuedAt)
	fmt.Printf("Expires:       %s\n", m.ExpiresAt)
	fmt.Printf("Error Budget:  %d / %d spent\n", spent, m.ErrorBudget.Limit)
	fmt.Printf("Repos:\n")
	for _, r := range m.Repos {
		fmt.Printf("  - %s: %s (%s)\n", r.Name, r.MaxTier, r.Path)
	}
	if len(m.ExcludedPaths) > 0 {
		fmt.Printf("Excluded Paths:\n")
		for _, p := range m.ExcludedPaths {
			fmt.Printf("  - %s\n", p)
		}
	}
	return 0
}

func runMandateCheck(args []string) int {
	var repoPath string
	var action string
	var paths []string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--repo":
			if i+1 < len(args) {
				repoPath = args[i+1]
				i++
			}
		case "--action":
			if i+1 < len(args) {
				action = args[i+1]
				i++
			}
		case "--paths":
			if i+1 < len(args) {
				pArg := args[i+1]
				i++
				parts := strings.Split(pArg, ",")
				for _, p := range parts {
					p = strings.TrimSpace(p)
					if p != "" {
						paths = append(paths, p)
					}
				}
			}
		case "--json":
			// json output is standard
		}
	}

	if repoPath == "" {
		fmt.Fprintf(os.Stderr, "f0 mandate check: --repo is required\n")
		return 1
	}
	if action == "" {
		fmt.Fprintf(os.Stderr, "f0 mandate check: --action is required\n")
		return 1
	}

	result, err := CheckMandate(repoPath, action, paths)
	if err != nil {
		fmt.Fprintf(os.Stderr, "f0 mandate check: FAIL: %v\n", err)
		return 1
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if encErr := enc.Encode(result); encErr != nil {
		fmt.Fprintf(os.Stderr, "f0 mandate check: encoding json: %v\n", encErr)
		return 1
	}

	if result.Allowed {
		return 0
	}
	return 3
}
