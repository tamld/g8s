package verifier

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tamld/g8s/internal/sandbox"
)

const (
	// CodeHoldoutNotOutOfWorktree is returned when a holdout evaluation directory
	// overlaps or lives inside a candidate worker's worktree.
	CodeHoldoutNotOutOfWorktree = "E_HOLDOUT_NOT_OUT_OF_WORKTREE"
	// CodeHoldoutMutated is returned when holdout contents change during evaluation.
	CodeHoldoutMutated = "E_HOLDOUT_MUTATED"
)

func isSameOrNestedPath(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// ValidateHoldoutPartition enforces TRUST-G3 out-of-worktree immutability:
// holdoutDir must exist as a directory and remain strictly disjoint from worktreeDir
// after symlink canonicalization.
func ValidateHoldoutPartition(worktreeDir, holdoutDir string) error {
	trimmedHoldout := strings.TrimSpace(holdoutDir)
	if trimmedHoldout == "" {
		return fmt.Errorf("%s: holdout directory path is empty", CodeHoldoutNotOutOfWorktree)
	}
	canonHoldout := sandbox.CanonicalPath(trimmedHoldout)
	if canonHoldout == "" {
		return fmt.Errorf("%s: invalid holdout directory %q", CodeHoldoutNotOutOfWorktree, holdoutDir)
	}
	st, err := os.Stat(canonHoldout)
	if err != nil {
		return fmt.Errorf("%s: stat holdout directory %q: %w", CodeHoldoutNotOutOfWorktree, holdoutDir, err)
	}
	if !st.IsDir() {
		return fmt.Errorf("%s: holdout path %q is not a directory", CodeHoldoutNotOutOfWorktree, holdoutDir)
	}

	trimmedWT := strings.TrimSpace(worktreeDir)
	if trimmedWT == "" {
		return nil
	}
	canonWT := sandbox.CanonicalPath(trimmedWT)
	if canonWT == "" {
		return fmt.Errorf("%s: invalid worktree directory %q", CodeHoldoutNotOutOfWorktree, worktreeDir)
	}
	if isSameOrNestedPath(canonWT, canonHoldout) || isSameOrNestedPath(canonHoldout, canonWT) {
		return fmt.Errorf("%s: holdout directory %q overlaps candidate worktree %q", CodeHoldoutNotOutOfWorktree, holdoutDir, worktreeDir)
	}
	return nil
}

// HashHoldoutPartition computes a deterministic SHA-256 manifest digest over all
// regular files inside holdoutDir so pre- and post-evaluation integrity can be verified.
func HashHoldoutPartition(holdoutDir string) (string, error) {
	trimmed := strings.TrimSpace(holdoutDir)
	if trimmed == "" {
		return "", fmt.Errorf("holdout directory is empty")
	}
	canonRoot := sandbox.CanonicalPath(trimmed)
	if canonRoot == "" {
		return "", fmt.Errorf("invalid holdout directory %q", holdoutDir)
	}

	type fileEntry struct {
		rel string
		abs string
	}
	var entries []fileEntry
	if err := filepath.Walk(canonRoot, func(p string, info os.FileInfo, wErr error) error {
		if wErr != nil {
			return wErr
		}
		if info == nil || info.IsDir() || !info.Mode().IsRegular() {
			return nil
		}
		rel, rErr := filepath.Rel(canonRoot, p)
		if rErr != nil {
			return rErr
		}
		entries = append(entries, fileEntry{
			rel: filepath.ToSlash(rel),
			abs: p,
		})
		return nil
	}); err != nil {
		return "", fmt.Errorf("walk holdout partition %q: %w", holdoutDir, err)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].rel < entries[j].rel
	})

	h := sha256.New()
	for _, ent := range entries {
		cleanAbs := filepath.Clean(ent.abs)
		// #nosec G304 -- cleanAbs is constrained to walked regular files inside canonRoot.
		f, err := os.Open(cleanAbs)
		if err != nil {
			return "", fmt.Errorf("open holdout file %q: %w", ent.rel, err)
		}
		info, statErr := f.Stat()
		if statErr != nil {
			if closeErr := f.Close(); closeErr != nil {
				return "", fmt.Errorf("stat holdout file %q: %w (close: %v)", ent.rel, statErr, closeErr)
			}
			return "", fmt.Errorf("stat holdout file %q: %w", ent.rel, statErr)
		}
		_, _ = fmt.Fprintf(h, "%s\x00%d\x00", ent.rel, info.Size())
		if _, copyErr := io.Copy(h, f); copyErr != nil {
			if closeErr := f.Close(); closeErr != nil {
				return "", fmt.Errorf("hash holdout file %q: %w (close: %v)", ent.rel, copyErr, closeErr)
			}
			return "", fmt.Errorf("hash holdout file %q: %w", ent.rel, copyErr)
		}
		if closeErr := f.Close(); closeErr != nil {
			return "", fmt.Errorf("close holdout file %q: %w", ent.rel, closeErr)
		}
		_, _ = h.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}
