package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func init() {
	commands["merge"] = mergeCommand
}

// CredentialStore abstracts retrieval of merge credentials.
type CredentialStore interface {
	GetMergeToken(service, account string) (string, error)
}

// KeychainCredentialStore retrieves tokens from macOS Keychain.
type KeychainCredentialStore struct {
	SecurityPath string
}

func (k *KeychainCredentialStore) GetMergeToken(service, account string) (string, error) {
	if service == "" {
		service = "g8s-f0-merge"
	}
	if account == "" {
		account = "default"
	}
	secPath := k.SecurityPath
	if secPath == "" {
		secPath = "/usr/bin/security"
	}
	cmd := exec.Command(secPath, "find-generic-password", "-s", service, "-a", account, "-w")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("security find-generic-password failed: %w", err)
	}
	token := strings.TrimSpace(string(out))
	if token == "" {
		return "", fmt.Errorf("empty token returned from keychain for %s/%s", service, account)
	}
	return token, nil
}

// StaticCredentialStore provides static tokens for testing.
type StaticCredentialStore struct {
	Token  string
	Tokens map[string]string
}

func (s *StaticCredentialStore) GetMergeToken(service, account string) (string, error) {
	if s.Tokens != nil {
		key := service + ":" + account
		if t, ok := s.Tokens[key]; ok {
			return t, nil
		}
		if t, ok := s.Tokens[service]; ok {
			return t, nil
		}
	}
	if s.Token != "" {
		return s.Token, nil
	}
	return "", fmt.Errorf("no token configured in StaticCredentialStore for %s/%s", service, account)
}

// MergeOptions configures T2 merge execution.
type MergeOptions struct {
	RepoDir         string
	PR              int
	HeadSHA         string
	BaseRef         string
	PRBody          string
	CredStore       CredentialStore
	Forge           ForgeAdapter
	MergeFunc       func(repoDir string, pr int, token, attestationTrailer string) (mergeSHA string, err error)
	PostMergeChecks func(repoDir, mergeSHA string) ([]ForgeCheck, error)
	RevertFunc      func(repoDir, mergeSHA string, token string) (revertSHA string, err error)
}

// MergeResult records the outcome of ExecuteT2Merge.
type MergeResult struct {
	Merged             bool   `json:"merged"`
	MergeSHA           string `json:"merge_sha,omitempty"`
	PreRevertPrepared  bool   `json:"pre_revert_prepared"`
	AttestationTrailer string `json:"attestation_trailer,omitempty"`
	PostMergeStatus    string `json:"post_merge_status"` // "PASS", "REVERTED"
	AutoReverted       bool   `json:"auto_reverted"`
	RevertSHA          string `json:"revert_sha,omitempty"`
	GateEvidenceID     string `json:"gate_evidence_id,omitempty"`
}

// ExecuteT2Merge implements the Phase 07 Wave B (#627) T2 merge executor.
func ExecuteT2Merge(opts MergeOptions) (*MergeResult, error) {
	repoDir := opts.RepoDir
	if repoDir == "" {
		repoDir = "."
	}
	absRepo, err := filepath.Abs(repoDir)
	if err != nil {
		absRepo = repoDir
	}

	// Step 1: Run EvaluateGate
	gateOpts := GateOptions{
		RepoDir: absRepo,
		Action:  "merge",
		PR:      opts.PR,
		HeadSHA: opts.HeadSHA,
		BaseRef: opts.BaseRef,
		PRBody:  opts.PRBody,
	}
	gateOut, err := EvaluateGate(gateOpts)
	if err != nil {
		return nil, fmt.Errorf("gate evaluation error: %w", err)
	}
	if gateOut == nil || gateOut.Overall != "PASS" {
		return nil, fmt.Errorf("refusing merge: f0 gate check failed")
	}

	headSHA := gateOut.HeadSHA
	if headSHA == "" {
		headSHA = opts.HeadSHA
	}

	ledger, err := OpenRepoLedger(absRepo)
	if err != nil {
		return nil, fmt.Errorf("opening repo ledger: %w", err)
	}

	var gateEvidenceID string
	if last, _ := ledger.LastEntry(); last != nil && last.Kind == "gate" {
		gateEvidenceID = last.ID
	}

	// Step 2: Prepare pre-revert record in repo ledger and build attestation trailer
	preRevertEntry := &Entry{
		ID:           newEvidenceID(),
		Ts:           time.Now().UTC().Format(time.RFC3339),
		Kind:         "merge-pre-revert",
		Repo:         absRepo,
		SHA:          headSHA,
		Cmd:          []string{"merge", "pre-revert"},
		Status:       "PASS",
		ItemsChecked: 1,
		Extra: map[string]any{
			"pr":       opts.PR,
			"head_sha": headSHA,
		},
	}
	if err := ledger.Append(preRevertEntry); err != nil {
		return nil, fmt.Errorf("recording merge-pre-revert: %w", err)
	}

	trailer, err := ledger.AnchorLine()
	if err != nil {
		return nil, fmt.Errorf("building attestation trailer: %w", err)
	}

	// Step 3: Retrieve merge credential and execute merge
	credStore := opts.CredStore
	if credStore == nil {
		if envToken := os.Getenv("F0_MERGE_TOKEN"); envToken != "" {
			credStore = &StaticCredentialStore{Token: envToken}
		} else {
			credStore = &KeychainCredentialStore{}
		}
	}
	token, err := credStore.GetMergeToken("g8s-f0-merge", "default")
	if err != nil {
		return nil, fmt.Errorf("retrieving merge credential: %w", err)
	}

	var mergeSHA string
	if opts.MergeFunc != nil {
		mergeSHA, err = opts.MergeFunc(absRepo, opts.PR, token, trailer)
		if err != nil {
			return nil, fmt.Errorf("merge function failed: %w", err)
		}
	} else {
		mergeSHA, err = defaultGHMerge(absRepo, opts.PR, token, trailer)
		if err != nil {
			return nil, fmt.Errorf("gh pr merge failed: %w", err)
		}
	}

	if mergeSHA == "" {
		if out, err := exec.Command("git", "-C", absRepo, "rev-parse", "HEAD").Output(); err == nil {
			mergeSHA = strings.TrimSpace(string(out))
		}
	}

	mergeExecEntry := &Entry{
		ID:           newEvidenceID(),
		Ts:           time.Now().UTC().Format(time.RFC3339),
		Kind:         "merge-exec",
		Repo:         absRepo,
		SHA:          mergeSHA,
		Cmd:          []string{"merge", "exec"},
		Status:       "PASS",
		ItemsChecked: 1,
		Extra: map[string]any{
			"pr":                  opts.PR,
			"merge_sha":           mergeSHA,
			"attestation_trailer": trailer,
		},
	}
	if err := ledger.Append(mergeExecEntry); err != nil {
		return nil, fmt.Errorf("recording merge-exec: %w", err)
	}

	// Step 4: Watch post-merge CI checks on mergeSHA
	var checks []ForgeCheck
	if opts.PostMergeChecks != nil {
		checks, err = opts.PostMergeChecks(absRepo, mergeSHA)
		if err != nil {
			return nil, fmt.Errorf("post-merge checks failed: %w", err)
		}
	} else if opts.Forge != nil {
		checks, err = opts.Forge.ListChecks(absRepo, opts.PR, mergeSHA)
		if err != nil {
			return nil, fmt.Errorf("forge list checks failed: %w", err)
		}
	} else {
		forge, _ := GetForgeAdapter(absRepo)
		if forge != nil {
			checks, _ = forge.ListChecks(absRepo, opts.PR, mergeSHA)
		}
	}

	var failingChecks []string
	for _, c := range checks {
		if strings.EqualFold(c.Bucket, "fail") {
			if !isCheckWaived(ledger, c.Name, opts.PR, mergeSHA) {
				failingChecks = append(failingChecks, c.Name)
			}
		}
	}

	if len(failingChecks) > 0 {
		var revSHA string
		if opts.RevertFunc != nil {
			revSHA, err = opts.RevertFunc(absRepo, mergeSHA, token)
			if err != nil {
				return nil, fmt.Errorf("auto-revert failed: %w", err)
			}
		} else {
			revSHA, err = defaultGitRevert(absRepo, mergeSHA)
			if err != nil {
				return nil, fmt.Errorf("default git revert failed: %w", err)
			}
		}

		revertEntry := &Entry{
			ID:           newEvidenceID(),
			Ts:           time.Now().UTC().Format(time.RFC3339),
			Kind:         "auto-revert",
			Repo:         absRepo,
			SHA:          revSHA,
			Cmd:          []string{"merge", "auto-revert"},
			Status:       "PASS",
			ItemsChecked: len(failingChecks),
			Extra: map[string]any{
				"pr":            opts.PR,
				"merge_sha":     mergeSHA,
				"revert_sha":    revSHA,
				"failed_checks": failingChecks,
			},
		}
		_ = ledger.Append(revertEntry)

		_ = RecordDebit(opts.RepoDir, fmt.Sprintf("auto-revert PR #%d on red post-merge CI", opts.PR), "")

		return &MergeResult{
			Merged:             true,
			MergeSHA:           mergeSHA,
			PreRevertPrepared:  true,
			AttestationTrailer: trailer,
			PostMergeStatus:    "REVERTED",
			AutoReverted:       true,
			RevertSHA:          revSHA,
			GateEvidenceID:     gateEvidenceID,
		}, nil
	}

	return &MergeResult{
		Merged:             true,
		MergeSHA:           mergeSHA,
		PreRevertPrepared:  true,
		AttestationTrailer: trailer,
		PostMergeStatus:    "PASS",
		AutoReverted:       false,
		GateEvidenceID:     gateEvidenceID,
	}, nil
}

func isCheckWaived(ledger *Ledger, checkName string, pr int, sha string) bool {
	if ledger == nil {
		return false
	}
	entries, err := ledger.ReadEntries()
	if err != nil {
		return false
	}
	for i := len(entries) - 1; i >= 0; i-- {
		e := &entries[i]
		if e.Kind != "waiver" {
			continue
		}
		wCheck, _ := e.Extra["check"].(string)
		if !strings.EqualFold(wCheck, checkName) {
			continue
		}
		wPR, okPR := e.Extra["pr"]
		if okPR && pr > 0 {
			prMatch := false
			switch v := wPR.(type) {
			case int:
				prMatch = (v == pr)
			case float64:
				prMatch = (int(v) == pr)
			case string:
				prMatch = (v == strconv.Itoa(pr))
			}
			if !prMatch {
				continue
			}
		}
		return true
	}
	return false
}

func defaultGitRevert(repoDir, mergeSHA string) (string, error) {
	cmd := exec.Command("git", "-C", repoDir, "revert", "--no-edit", mergeSHA)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git revert %s: %s: %w", mergeSHA, string(out), err)
	}
	revCmd := exec.Command("git", "-C", repoDir, "rev-parse", "HEAD")
	revOut, err := revCmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD after revert: %w", err)
	}
	return strings.TrimSpace(string(revOut)), nil
}

func defaultGHMerge(repoDir string, pr int, token, trailer string) (string, error) {
	args := []string{"pr", "merge", strconv.Itoa(pr), "--squash"}
	if trailer != "" {
		args = append(args, "--body", trailer)
	}
	cmd := exec.Command("gh", args...)
	cmd.Dir = repoDir
	if token != "" {
		cmd.Env = append(os.Environ(), "GH_TOKEN="+token)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("gh pr merge %d: %s: %w", pr, string(out), err)
	}
	viewCmd := exec.Command("gh", "pr", "view", strconv.Itoa(pr), "--json", "mergeCommit")
	viewCmd.Dir = repoDir
	if token != "" {
		viewCmd.Env = append(os.Environ(), "GH_TOKEN="+token)
	}
	viewOut, err := viewCmd.Output()
	if err == nil {
		var v struct {
			MergeCommit struct {
				Oid string `json:"oid"`
			} `json:"mergeCommit"`
		}
		if err := json.Unmarshal(viewOut, &v); err == nil && v.MergeCommit.Oid != "" {
			return v.MergeCommit.Oid, nil
		}
	}
	headCmd := exec.Command("git", "-C", repoDir, "rev-parse", "HEAD")
	headOut, err := headCmd.Output()
	if err == nil {
		return strings.TrimSpace(string(headOut)), nil
	}
	return "", nil
}

func mergeCommand(args []string) int {
	var opts MergeOptions
	var jsonOutput bool

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "exec":
			// optional subcommand
		case "--repo":
			if i+1 < len(args) {
				opts.RepoDir = args[i+1]
				i++
			}
		case "--pr":
			if i+1 < len(args) {
				if n, err := strconv.Atoi(args[i+1]); err == nil {
					opts.PR = n
				}
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
		case "--pr-body":
			if i+1 < len(args) {
				opts.PRBody = args[i+1]
				i++
			}
		case "--json":
			jsonOutput = true
		}
	}

	result, err := ExecuteT2Merge(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "f0 merge: %v\n", err)
		return 1
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(result)
	} else {
		fmt.Printf("Merge completed: %s (status: %s)\n", result.MergeSHA, result.PostMergeStatus)
		if result.AutoReverted {
			fmt.Printf("Auto-reverted: %s\n", result.RevertSHA)
		}
	}

	if result.AutoReverted || result.PostMergeStatus != "PASS" {
		return 1
	}
	return 0
}
