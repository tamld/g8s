package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/tamld/g8s/internal/lockfile"
)

// Entry defines the machine-readable evidence entry schema for the F0 ledger.
type Entry struct {
	ID           string         `json:"id"`
	Seq          uint64         `json:"seq"`
	Ts           string         `json:"ts"`
	Kind         string         `json:"kind"`
	Repo         string         `json:"repo"`
	Branch       string         `json:"branch"`
	SHA          string         `json:"sha"`
	Dirty        bool           `json:"dirty"`
	DirtyHash    string         `json:"dirty_hash,omitempty"`
	Cmd          []string       `json:"cmd"`
	Exit         int            `json:"exit"`
	DurationMS   int64          `json:"duration_ms"`
	StdoutSHA256 string         `json:"stdout_sha256"`
	StderrSHA256 string         `json:"stderr_sha256"`
	Tail         string         `json:"tail"`
	ItemsChecked int            `json:"items_checked"`
	Status       string         `json:"status"`
	Extra        map[string]any `json:"extra,omitempty"`
	PrevHash     string         `json:"prev_hash"`
	Hash         string         `json:"hash"`
}

type entryWithoutHash struct {
	ID           string         `json:"id"`
	Seq          uint64         `json:"seq"`
	Ts           string         `json:"ts"`
	Kind         string         `json:"kind"`
	Repo         string         `json:"repo"`
	Branch       string         `json:"branch"`
	SHA          string         `json:"sha"`
	Dirty        bool           `json:"dirty"`
	DirtyHash    string         `json:"dirty_hash,omitempty"`
	Cmd          []string       `json:"cmd"`
	Exit         int            `json:"exit"`
	DurationMS   int64          `json:"duration_ms"`
	StdoutSHA256 string         `json:"stdout_sha256"`
	StderrSHA256 string         `json:"stderr_sha256"`
	Tail         string         `json:"tail"`
	ItemsChecked int            `json:"items_checked"`
	Status       string         `json:"status"`
	Extra        map[string]any `json:"extra,omitempty"`
	PrevHash     string         `json:"prev_hash"`
}

// computeHash calculates sha256(prev_hash || canonical_json(entry without hash)).
func computeHash(e *Entry) (string, error) {
	cmd := e.Cmd
	if cmd == nil {
		cmd = []string{}
	}
	var extra map[string]any
	if len(e.Extra) > 0 {
		extra = e.Extra
	}

	raw := entryWithoutHash{
		ID:           e.ID,
		Seq:          e.Seq,
		Ts:           e.Ts,
		Kind:         e.Kind,
		Repo:         e.Repo,
		Branch:       e.Branch,
		SHA:          e.SHA,
		Dirty:        e.Dirty,
		DirtyHash:    e.DirtyHash,
		Cmd:          cmd,
		Exit:         e.Exit,
		DurationMS:   e.DurationMS,
		StdoutSHA256: e.StdoutSHA256,
		StderrSHA256: e.StderrSHA256,
		Tail:         e.Tail,
		ItemsChecked: e.ItemsChecked,
		Status:       e.Status,
		Extra:        extra,
		PrevHash:     e.PrevHash,
	}

	canonJSON, err := json.Marshal(raw)
	if err != nil {
		return "", fmt.Errorf("canonical json marshal: %w", err)
	}

	h := sha256.New()
	h.Write([]byte(e.PrevHash))
	h.Write(canonJSON)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// acquireLock wraps lockfile.TryLock in a retry loop with backoff (10ms -> 250ms, 10s deadline).
func acquireLock(lockPath string) (*lockfile.Lock, error) {
	deadline := time.Now().Add(10 * time.Second)
	backoff := 10 * time.Millisecond
	maxBackoff := 250 * time.Millisecond

	for {
		l, err := lockfile.TryLock(lockPath)
		if err == nil {
			return l, nil
		}
		if !errors.Is(err, lockfile.ErrContended) {
			return nil, fmt.Errorf("lockfile error on %s: %w", lockPath, err)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timeout waiting for lockfile %s after 10s (contended): %w", lockPath, err)
		}
		time.Sleep(backoff)
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// RepoLedgerPath returns the per-repo ledger path:
// $(git rev-parse --git-common-dir)/f0/ledger.jsonl (resolved to absolute).
// This deliberately ignores G8S_STATE_DIR.
func RepoLedgerPath(repoDir string) (string, error) {
	if repoDir == "" {
		repoDir = "."
	}
	cmd := exec.Command("git", "rev-parse", "--git-common-dir")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("resolving git common dir for %s: %w", repoDir, err)
	}
	commonDir := strings.TrimSpace(string(out))
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(repoDir, commonDir)
	}
	absCommonDir, err := filepath.Abs(commonDir)
	if err != nil {
		return "", fmt.Errorf("resolving absolute path for %s: %w", commonDir, err)
	}
	return filepath.Join(absCommonDir, "f0", "ledger.jsonl"), nil
}

func resolveHomeDir() (string, error) {
	if h := strings.TrimSpace(os.Getenv("HOME")); h != "" {
		return h, nil
	}
	return os.UserHomeDir()
}

// GlobalLedgerPath returns ~/.local/state/g8s/f0/global-ledger.jsonl.
// This deliberately ignores G8S_STATE_DIR.
func GlobalLedgerPath() (string, error) {
	home, err := resolveHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home dir: %w", err)
	}
	return filepath.Join(home, ".local", "state", "g8s", "f0", "global-ledger.jsonl"), nil
}

// Ledger provides access to an append-only, tamper-evident JSONL evidence store.
type Ledger struct {
	Path     string
	LockPath string
}

// OpenRepoLedger opens the evidence ledger for a given repository.
func OpenRepoLedger(repoDir string) (*Ledger, error) {
	p, err := RepoLedgerPath(repoDir)
	if err != nil {
		return nil, err
	}
	return OpenLedgerAtPath(p), nil
}

// OpenGlobalLedger opens the global mandate/debit/waiver ledger.
func OpenGlobalLedger() (*Ledger, error) {
	p, err := GlobalLedgerPath()
	if err != nil {
		return nil, err
	}
	return OpenLedgerAtPath(p), nil
}

// OpenLedgerAtPath returns a Ledger pointing directly to the given path.
func OpenLedgerAtPath(p string) *Ledger {
	return &Ledger{
		Path:     p,
		LockPath: p + ".lock",
	}
}

// Append adds a new entry to the ledger under an exclusive advisory lock with fsync.
func (l *Ledger) Append(entry *Entry) error {
	if err := os.MkdirAll(filepath.Dir(l.Path), 0o700); err != nil {
		return fmt.Errorf("creating ledger directory: %w", err)
	}

	lock, err := acquireLock(l.LockPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = lock.Release()
	}()

	// Read last entry under lock to determine next seq and prev_hash
	last, err := l.lastEntryUnlocked()
	if err != nil {
		return fmt.Errorf("reading last ledger entry: %w", err)
	}

	if last == nil {
		entry.Seq = 1
		entry.PrevHash = ""
	} else {
		entry.Seq = last.Seq + 1
		entry.PrevHash = last.Hash
	}

	hash, err := computeHash(entry)
	if err != nil {
		return fmt.Errorf("computing entry hash: %w", err)
	}
	entry.Hash = hash

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshaling entry: %w", err)
	}

	f, err := os.OpenFile(l.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("opening ledger for append: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("writing entry line: %w", err)
	}

	if err := f.Sync(); err != nil {
		return fmt.Errorf("fsyncing ledger file: %w", err)
	}

	return nil
}

// ReadEntries reads all entries from the ledger.
func (l *Ledger) ReadEntries() ([]Entry, error) {
	if _, err := os.Stat(l.Path); os.IsNotExist(err) {
		return nil, nil
	}
	data, err := os.ReadFile(l.Path)
	if err != nil {
		return nil, fmt.Errorf("reading ledger: %w", err)
	}

	var entries []Entry
	scanner := bufio.NewScanner(bytes.NewReader(data))
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, len(buf))

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("corrupt ledger line %d: %w", lineNum, err)
		}
		entries = append(entries, e)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning ledger: %w", err)
	}
	return entries, nil
}

// LastEntry returns the last entry from the ledger, or nil if empty.
func (l *Ledger) LastEntry() (*Entry, error) {
	entries, err := l.ReadEntries()
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}
	return &entries[len(entries)-1], nil
}

func (l *Ledger) lastEntryUnlocked() (*Entry, error) {
	return l.LastEntry()
}

// FindByID returns the entry matching the given ID.
func (l *Ledger) FindByID(id string) (*Entry, error) {
	entries, err := l.ReadEntries()
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.ID == id || (!strings.HasPrefix(id, "ev-") && e.ID == "ev-"+id) {
			return &e, nil
		}
	}
	return nil, nil
}

// AnchorLine returns the anchor trailer string for the current ledger head.
// Used by Phase 04 and git commit trailer generation.
func (l *Ledger) AnchorLine() (string, error) {
	last, err := l.LastEntry()
	if err != nil {
		return "", err
	}
	if last == nil {
		return "", errors.New("ledger is empty: no head entry to anchor")
	}
	return fmt.Sprintf("F0-Ledger-Head: %d:%s", last.Seq, last.Hash), nil
}

// AnchorLine returns the anchor line for the repo ledger in repoDir.
func AnchorLine(repoDir string) (string, error) {
	ledger, err := OpenRepoLedger(repoDir)
	if err != nil {
		return "", err
	}
	return ledger.AnchorLine()
}

// Anchor represents an anchor extracted from a git commit.
type Anchor struct {
	Seq  uint64
	Hash string
}

var anchorRegex = regexp.MustCompile(`(?m)^F0-Ledger-Head:\s*(\d+):([0-9a-fA-F]+)\s*$`)

func findDefaultBranch(repoDir string) string {
	// 1. origin/HEAD
	if err := exec.Command("git", "-C", repoDir, "rev-parse", "--verify", "origin/HEAD").Run(); err == nil {
		return "origin/HEAD"
	}
	// 2. refs/heads/main or main
	if err := exec.Command("git", "-C", repoDir, "rev-parse", "--verify", "main").Run(); err == nil {
		return "main"
	}
	// 3. refs/heads/master or master
	if err := exec.Command("git", "-C", repoDir, "rev-parse", "--verify", "master").Run(); err == nil {
		return "master"
	}
	// 4. Fallback to current HEAD
	out, err := exec.Command("git", "-C", repoDir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err == nil {
		b := strings.TrimSpace(string(out))
		if b != "" {
			return b
		}
	}
	return "HEAD"
}

// collectGitAnchors scans commit messages on the default branch for F0-Ledger-Head trailers.
func collectGitAnchors(repoDir string) ([]Anchor, error) {
	branch := findDefaultBranch(repoDir)
	cmd := exec.Command("git", "-C", repoDir, "log", "--format=%B", branch)
	out, err := cmd.Output()
	if err != nil {
		// If git log fails (e.g. no commits exist yet), return empty
		return nil, nil
	}

	matches := anchorRegex.FindAllStringSubmatch(string(out), -1)
	var anchors []Anchor
	for _, m := range matches {
		seq, err := strconv.ParseUint(m[1], 10, 64)
		if err != nil {
			continue
		}
		anchors = append(anchors, Anchor{
			Seq:  seq,
			Hash: m[2],
		})
	}
	return anchors, nil
}

// VerifyReport holds verification metrics.
type VerifyReport struct {
	EntriesCount int
	AnchorsCount int
}

// Verify checks the ledger hash chain integrity and validates any git anchors.
func (l *Ledger) Verify(repoDir string) (*VerifyReport, error) {
	entries, err := l.ReadEntries()
	if err != nil {
		return nil, err
	}

	// 1. Recompute the hash chain
	var prevHash string
	for i, entry := range entries {
		expectedSeq := uint64(i + 1)
		if entry.Seq != expectedSeq {
			return nil, fmt.Errorf("seq mismatch at entry %d: got %d, expected %d", i, entry.Seq, expectedSeq)
		}
		if entry.PrevHash != prevHash {
			return nil, fmt.Errorf("prev_hash mismatch at seq %d: got %s, expected %s", entry.Seq, entry.PrevHash, prevHash)
		}
		computed, err := computeHash(&entry)
		if err != nil {
			return nil, fmt.Errorf("hashing entry at seq %d: %w", entry.Seq, err)
		}
		if entry.Hash != computed {
			return nil, fmt.Errorf("tamper detected at seq %d: hash mismatch (got %s, expected %s)", entry.Seq, entry.Hash, computed)
		}
		prevHash = entry.Hash
	}

	// 2. Validate external git anchors if repoDir is provided
	anchorsChecked := 0
	if repoDir != "" {
		anchors, err := collectGitAnchors(repoDir)
		if err != nil {
			return nil, fmt.Errorf("collecting git anchors: %w", err)
		}
		anchorsChecked = len(anchors)
		for _, anchor := range anchors {
			if anchor.Seq == 0 || anchor.Seq > uint64(len(entries)) {
				return nil, fmt.Errorf("anchor verification failed: sequence %d anchored in git log but missing from ledger (ledger truncated, only %d entries)", anchor.Seq, len(entries))
			}
			ledgerEntry := entries[anchor.Seq-1]
			if ledgerEntry.Hash != anchor.Hash {
				return nil, fmt.Errorf("anchor verification failed: sequence %d hash mismatch: ledger has %s, anchor has %s (ledger rewritten)", anchor.Seq, ledgerEntry.Hash, anchor.Hash)
			}
		}
	}

	return &VerifyReport{
		EntriesCount: len(entries),
		AnchorsCount: anchorsChecked,
	}, nil
}

// ledgerCommand dispatches f0 ledger subcommands.
func ledgerCommand(args []string) int {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: f0 ledger <verify|show|list> [options]\n")
		return 1
	}

	switch args[0] {
	case "verify":
		return runLedgerVerify(args[1:])
	case "show":
		return runLedgerShow(args[1:])
	case "list":
		return runLedgerList(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "f0 ledger: unknown subcommand %q\n", args[0])
		return 1
	}
}

func runLedgerVerify(args []string) int {
	repoDir := "."
	for i := 0; i < len(args); i++ {
		if args[i] == "--repo" && i+1 < len(args) {
			repoDir = args[i+1]
			i++
		}
	}

	ledger, err := OpenRepoLedger(repoDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "f0 ledger verify: %v\n", err)
		return 1
	}

	report, err := ledger.Verify(repoDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "f0 ledger verify: FAIL: %v\n", err)
		return 1
	}

	fmt.Printf("[f0] ledger verified: %d entries, %d anchors checked\n", report.EntriesCount, report.AnchorsCount)
	return 0
}

func runLedgerShow(args []string) int {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: f0 ledger show <id> [--repo P]\n")
		return 1
	}

	var id string
	repoDir := "."
	for i := 0; i < len(args); i++ {
		if args[i] == "--repo" && i+1 < len(args) {
			repoDir = args[i+1]
			i++
		} else if !strings.HasPrefix(args[i], "-") && id == "" {
			id = args[i]
		}
	}

	if id == "" {
		fmt.Fprintf(os.Stderr, "usage: f0 ledger show <id> [--repo P]\n")
		return 1
	}

	var entry *Entry
	ledger, err := OpenRepoLedger(repoDir)
	if err == nil {
		entry, _ = ledger.FindByID(id)
	}
	if entry == nil {
		// Fallback to global ledger
		gLedger, gErr := OpenGlobalLedger()
		if gErr == nil {
			entry, _ = gLedger.FindByID(id)
		}
	}

	if entry == nil {
		fmt.Fprintf(os.Stderr, "f0 ledger: entry %q not found\n", id)
		return 1
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(entry); err != nil {
		fmt.Fprintf(os.Stderr, "f0 ledger: encoding entry: %v\n", err)
		return 1
	}
	return 0
}

func runLedgerList(args []string) int {
	repoDir := "."
	shaFilter := ""
	kindFilter := ""
	jsonOutput := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--repo":
			if i+1 < len(args) {
				repoDir = args[i+1]
				i++
			}
		case "--sha":
			if i+1 < len(args) {
				shaFilter = args[i+1]
				i++
			}
		case "--kind":
			if i+1 < len(args) {
				kindFilter = args[i+1]
				i++
			}
		case "--json":
			jsonOutput = true
		}
	}

	ledger, err := OpenRepoLedger(repoDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "f0 ledger list: %v\n", err)
		return 1
	}

	entries, err := ledger.ReadEntries()
	if err != nil {
		fmt.Fprintf(os.Stderr, "f0 ledger list: %v\n", err)
		return 1
	}

	var filtered []Entry
	for _, e := range entries {
		if shaFilter != "" && !strings.HasPrefix(e.SHA, shaFilter) {
			continue
		}
		if kindFilter != "" && e.Kind != kindFilter {
			continue
		}
		filtered = append(filtered, e)
	}

	if jsonOutput {
		if filtered == nil {
			filtered = []Entry{}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(filtered); err != nil {
			fmt.Fprintf(os.Stderr, "f0 ledger list: %v\n", err)
			return 1
		}
		return 0
	}

	if len(filtered) == 0 {
		fmt.Println("(no entries found)")
		return 0
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSEQ\tSTATUS\tKIND\tSHA\tDURATION\tCMD")
	for _, e := range filtered {
		shortSHA := e.SHA
		if len(shortSHA) > 7 {
			shortSHA = shortSHA[:7]
		}
		if shortSHA == "" {
			shortSHA = "none"
		}
		cmdStr := strings.Join(e.Cmd, " ")
		fmt.Fprintf(w, "%s\t%d\t%s\t%s\t%s\t%dms\t%s\n", e.ID, e.Seq, e.Status, e.Kind, shortSHA, e.DurationMS, cmdStr)
	}
	_ = w.Flush()
	return 0
}
