// Package fsops implements safe, receipt-bounded file system operations
// including bounded reads, DLP sanitization, atomic edits, and delegation context resolution.
package fsops

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/tamld/g8s/internal/charter"
	"github.com/tamld/g8s/internal/contextpack"
	"github.com/tamld/g8s/internal/dispatch"
	"github.com/tamld/g8s/internal/search"
	"github.com/tamld/g8s/internal/vault"
)

var editMu sync.Mutex

// ReadOptions configures a bounded file read operation.
type ReadOptions struct {
	Path         string   `json:"path"`
	ScopeRoot    string   `json:"scope_root"`
	StartLine    int      `json:"start_line"`
	EndLine      int      `json:"end_line"`
	MaxBytes     int      `json:"max_bytes"`
	Tier         string   `json:"tier,omitempty"`
	AllowedPaths []string `json:"allowed_paths"`
}

// LineEntry represents a 1-indexed line of text read from a file.
type LineEntry struct {
	Line int    `json:"line"`
	Text string `json:"text"`
}

// ReadResult represents the result of a bounded file read operation.
type ReadResult struct {
	File       string      `json:"file"`
	Tier       string      `json:"tier,omitempty"`
	TotalLines int         `json:"total_lines"`
	StartLine  int         `json:"start_line"`
	EndLine    int         `json:"end_line"`
	SHA256     string      `json:"sha256"`
	Truncated  bool        `json:"truncated,omitempty"`
	Lines      []LineEntry `json:"lines"`
	Content    string      `json:"content"`
	JITWhisper []string    `json:"jit_whisper,omitempty"`
}

// EditOptions configures an atomic file edit operation.
type EditOptions struct {
	Path         string   `json:"path"`
	ScopeRoot    string   `json:"scope_root"`
	OldString    string   `json:"old_string"`
	NewString    string   `json:"new_string"`
	ReplaceAll   bool     `json:"replace_all"`
	IfSHA256     string   `json:"if_sha256"`
	AllowedPaths []string `json:"allowed_paths"`
}

// EditResult represents the result of an atomic file edit operation.
type EditResult struct {
	File         string   `json:"file"`
	Replacements int      `json:"replacements"`
	OldSHA256    string   `json:"old_sha256"`
	NewSHA256    string   `json:"new_sha256"`
	JITWhisper   []string `json:"jit_whisper,omitempty"`
}

// evalCanonicalPath resolves symlinks for existing paths or the deepest existing
// ancestor directory for non-existent paths, including dangling symlinks.
func evalCanonicalPath(p string) (string, error) {
	return evalCanonicalPathDepth(p, 0)
}

func evalCanonicalPathDepth(p string, depth int) (string, error) {
	if depth > 32 {
		return "", fmt.Errorf("too many symlink levels resolving %q", p)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(resolved), nil
	}

	rootPrefix := filepath.VolumeName(abs) + string(filepath.Separator)
	cur := abs
	var tail []string
	for {
		if !strings.HasPrefix(cur, rootPrefix) {
			return "", fmt.Errorf("non-canonical path %q", cur)
		}
		if linfo, lerr := os.Lstat(cur); lerr == nil && linfo.Mode()&os.ModeSymlink != 0 {
			target, rerr := os.Readlink(cur)
			if rerr != nil {
				return "", rerr
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(cur), target)
			}
			resolvedTarget, terr := evalCanonicalPathDepth(target, depth+1)
			if terr != nil {
				return "", terr
			}
			for i := len(tail) - 1; i >= 0; i-- {
				resolvedTarget = filepath.Join(resolvedTarget, tail[i])
			}
			return filepath.Clean(resolvedTarget), nil
		}

		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return filepath.Clean(resolved), nil
		}

		parent := filepath.Dir(cur)
		if parent == cur {
			return filepath.Clean(abs), nil
		}
		tail = append(tail, filepath.Base(cur))
		cur = parent
	}
}

func isEscapingRel(rel string) bool {
	relSlash := filepath.ToSlash(filepath.Clean(rel))
	return relSlash == ".." || strings.HasPrefix(relSlash, "../") || strings.HasPrefix(relSlash, "/")
}

func verifySubScope(childScope, ceilingScope string) error {
	absCeiling, err := filepath.Abs(ceilingScope)
	if err != nil {
		return err
	}
	absChild, err := filepath.Abs(childScope)
	if err != nil {
		return err
	}
	canonCeiling, err := evalCanonicalPath(absCeiling)
	if err != nil {
		return err
	}
	canonChild, err := evalCanonicalPath(absChild)
	if err != nil {
		return err
	}
	relLex, errLex := filepath.Rel(absCeiling, absChild)
	relToCanon, errToCanon := filepath.Rel(canonCeiling, absChild)
	if (errLex != nil || isEscapingRel(relLex)) && (errToCanon != nil || isEscapingRel(relToCanon)) {
		return fmt.Errorf("path %q escapes ceiling %q", childScope, ceilingScope)
	}
	canonRel, err := filepath.Rel(canonCeiling, canonChild)
	if err != nil || isEscapingRel(canonRel) {
		return fmt.Errorf("resolved path %q escapes ceiling %q via symlink", childScope, ceilingScope)
	}
	return nil
}

func splitScopeRoots(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// ResolveDelegationContext checks environment variables and flags to determine
// the active receipt ID and scope root, preventing privilege escalation.
// When G8S_SCOPE_ROOT (or G8S_TIER=F1/F2) is set, the environment scope acts
// as an immutable ceiling: flagScopeRoot may preserve or narrow it, but never
// expand beyond it (#598). Comma-separated roots in G8S_SCOPE_ROOT and
// G8S_ADD_DIRS (for read-only operations) are supported for multi-repo fan-out (#643).
func ResolveDelegationContext(flagReceiptID, flagScopeRoot string, isMutation bool) (receiptID string, scopeRoot string, err error) {
	envTier := strings.TrimSpace(os.Getenv("G8S_TIER"))
	envPerm := strings.TrimSpace(os.Getenv("G8S_PERMISSION"))
	envReceipt := strings.TrimSpace(os.Getenv("G8S_RECEIPT_ID"))
	envScope := strings.TrimSpace(os.Getenv("G8S_SCOPE_ROOT"))
	envAddDirs := strings.TrimSpace(os.Getenv("G8S_ADD_DIRS"))
	isDelegatedTier := strings.EqualFold(envTier, "F1") || strings.EqualFold(envTier, "F2")

	if isMutation && (envPerm == "read_only" || envPerm == "automation_read") {
		return "", "", fmt.Errorf("permission=%s environment forbids file mutation", envPerm)
	}
	if isMutation {
		if err := charter.VerifyMutationAllowed(charter.CurrentStratum(), flagScopeRoot); err != nil {
			return "", "", err
		}
	}

	if envReceipt != "" && flagReceiptID != "" && flagReceiptID != envReceipt {
		return "", "", fmt.Errorf("receipt mismatch: flag %q conflicts with delegated worker environment receipt %q", flagReceiptID, envReceipt)
	}

	receiptID = flagReceiptID
	if receiptID == "" {
		receiptID = envReceipt
	}
	if isMutation && isDelegatedTier && receiptID == "" {
		return "", "", fmt.Errorf("tier=%s environment requires a write receipt for file mutation", envTier)
	}

	scopeRoots := splitScopeRoots(envScope)
	addDirRoots := splitScopeRoots(envAddDirs)
	ceilingRoots := scopeRoots
	if !isMutation && len(addDirRoots) > 0 {
		ceilingRoots = append(append([]string(nil), scopeRoots...), addDirRoots...)
	}

	if len(ceilingRoots) > 0 {
		if flagScopeRoot == "" {
			scopeRoot = ceilingRoots[0]
		} else {
			matched := false
			var lastErr error
			for _, ceiling := range ceilingRoots {
				if err := verifySubScope(flagScopeRoot, ceiling); err == nil {
					matched = true
					break
				} else {
					lastErr = err
				}
			}
			if !matched {
				return "", "", fmt.Errorf("scope_root mismatch: flag %q escapes delegated worker environment scope root %q (%v)", flagScopeRoot, envScope, lastErr)
			}
			scopeRoot = flagScopeRoot
		}
	} else if isDelegatedTier {
		if flagScopeRoot == "" {
			scopeRoot = "."
		} else {
			if err := verifySubScope(flagScopeRoot, "."); err != nil {
				return "", "", fmt.Errorf("scope_root mismatch: flag %q escapes delegated worker cwd ceiling (%v)", flagScopeRoot, err)
			}
			scopeRoot = flagScopeRoot
		}
	} else {
		scopeRoot = flagScopeRoot
		if scopeRoot == "" {
			scopeRoot = "."
		}
	}

	return receiptID, scopeRoot, nil
}

// VerifyScope checks that filePath resides within scopeRoot without path traversal
// or symlink escape (#598), and satisfies allowedPaths if any are specified.
// Returns normalized relative path with forward slashes on success.
func VerifyScope(filePath, scopeRoot string, allowedPaths []string) (string, error) {
	if strings.TrimSpace(filePath) == "" {
		return "", fmt.Errorf("path must not be empty")
	}
	if strings.ContainsRune(filePath, 0) || strings.ContainsRune(scopeRoot, 0) {
		return "", fmt.Errorf("path must not contain NUL bytes")
	}
	if scopeRoot == "" {
		scopeRoot = "."
	}
	absScope, err := filepath.Abs(scopeRoot)
	if err != nil {
		return "", fmt.Errorf("resolve abs scope root %q: %w", scopeRoot, err)
	}
	absFile, err := filepath.Abs(filePath)
	if err != nil {
		return "", fmt.Errorf("resolve abs file path %q: %w", filePath, err)
	}
	canonScope, err := evalCanonicalPath(absScope)
	if err != nil {
		return "", fmt.Errorf("resolve canonical scope root %q: %w", scopeRoot, err)
	}
	canonFile, err := evalCanonicalPath(absFile)
	if err != nil {
		return "", fmt.Errorf("resolve canonical file path %q: %w", filePath, err)
	}

	rel, errRel := filepath.Rel(absScope, absFile)
	relToCanon, errRelCanon := filepath.Rel(canonScope, absFile)
	relSlash := filepath.ToSlash(filepath.Clean(rel))
	if errRel != nil || isEscapingRel(relSlash) {
		if errRelCanon != nil || isEscapingRel(relToCanon) {
			return "", fmt.Errorf("path %q escapes scope root %q", filePath, scopeRoot)
		}
		relSlash = filepath.ToSlash(filepath.Clean(relToCanon))
	}

	canonRel, err := filepath.Rel(canonScope, canonFile)
	if err != nil {
		return "", fmt.Errorf("compute canonical relative path: %w", err)
	}
	canonRelSlash := filepath.ToSlash(filepath.Clean(canonRel))
	if isEscapingRel(canonRelSlash) {
		return "", fmt.Errorf("path %q escapes scope root %q via symlink", filePath, scopeRoot)
	}

	if len(allowedPaths) > 0 {
		if !search.MatchesAllowedPaths(relSlash, allowedPaths) || !search.MatchesAllowedPaths(canonRelSlash, allowedPaths) {
			return "", fmt.Errorf("path %q (relative %q) is outside receipt allowed_paths=%v (scope_root=%q)", filePath, relSlash, allowedPaths, scopeRoot)
		}
	}
	return canonRelSlash, nil
}

// hasExplicitPrefixGrant reports whether allowedPaths contains an explicit
// pattern targeting prefix (e.g. ".git/" or ".g8s/") rather than a generic
// wildcard ("*", "**", "./**").
func hasExplicitPrefixGrant(prefix string, allowedPaths []string) bool {
	prefix = strings.TrimSuffix(prefix, "/")
	for _, raw := range allowedPaths {
		p := filepath.ToSlash(filepath.Clean(strings.TrimSpace(raw)))
		p = strings.TrimPrefix(p, "./")
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	return false
}

// VerifyWriteScope enforces all VerifyScope checks and additionally protects
// repository metadata and governance directories (.git/** and .g8s/**) from
// unauthorized mutation (#664). Writes to .git/** are always denied unless
// explicitly granted in allowedPaths; writes to .g8s/** are denied when
// operating under a receipt/wildcard scope or delegated F1/F2 tier unless
// allowedPaths contains an explicit .g8s/** pattern.
func VerifyWriteScope(filePath, scopeRoot string, allowedPaths []string) (string, error) {
	if err := charter.VerifyMutationAllowed(charter.CurrentStratum(), filePath); err != nil {
		return "", err
	}
	relSlash, err := VerifyScope(filePath, scopeRoot, allowedPaths)
	if err != nil {
		return "", err
	}
	clean := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(relSlash)), "./")
	if clean == ".git" || strings.HasPrefix(clean, ".git/") || strings.Contains(clean, "/.git/") || strings.HasSuffix(clean, "/.git") {
		if !hasExplicitPrefixGrant(".git", allowedPaths) {
			return "", fmt.Errorf("path %q targets protected git metadata (.git/**); write denied", filePath)
		}
	}
	if clean == ".g8s" || strings.HasPrefix(clean, ".g8s/") || strings.Contains(clean, "/.g8s/") || strings.HasSuffix(clean, "/.g8s") {
		envTier := strings.TrimSpace(os.Getenv("G8S_TIER"))
		isDelegated := strings.EqualFold(envTier, "F1") || strings.EqualFold(envTier, "F2")
		if (len(allowedPaths) > 0 || isDelegated) && !hasExplicitPrefixGrant(".g8s", allowedPaths) {
			return "", fmt.Errorf("path %q targets protected governance directory (.g8s/**) without explicit receipt grant", filePath)
		}
	}
	return relSlash, nil
}

// Read safely reads and bounds text file content, validating scope, checking for binary
// content, computing SHA-256, slicing requested line ranges, and applying DLP sanitization.
func Read(ctx context.Context, opts ReadOptions) (*ReadResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	relSlash, err := VerifyScope(opts.Path, opts.ScopeRoot, opts.AllowedPaths)
	if err != nil {
		return nil, err
	}
	canonTarget, err := evalCanonicalPath(opts.Path)
	if err != nil {
		return nil, err
	}
	if _, err := VerifyScope(canonTarget, opts.ScopeRoot, opts.AllowedPaths); err != nil {
		return nil, err
	}
	scopeRoot := opts.ScopeRoot
	if scopeRoot == "" {
		scopeRoot = "."
	}
	canonScope, err := evalCanonicalPath(scopeRoot)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(canonTarget, canonScope) {
		return nil, fmt.Errorf("path %q escapes scope root %q", opts.Path, opts.ScopeRoot)
	}

	raw, err := os.ReadFile(canonTarget)
	if err != nil {
		return nil, err
	}

	checkLen := len(raw)
	if checkLen > 512 {
		checkLen = 512
	}
	if bytes.IndexByte(raw[:checkLen], 0) >= 0 {
		return nil, fmt.Errorf("binary file cannot be read as text")
	}

	sum := sha256.Sum256(raw)
	fileSHA := hex.EncodeToString(sum[:])

	var resolvedTier string
	sourceText := string(raw)
	if rawTier := strings.TrimSpace(opts.Tier); rawTier != "" {
		upperTier := contextpack.Tier(strings.ToUpper(rawTier))
		switch upperTier {
		case contextpack.TierFull, contextpack.TierSkel, contextpack.TierSigs, contextpack.TierTree:
			resolvedTier = string(upperTier)
			if upperTier != contextpack.TierFull {
				sourceText = contextpack.ElideFile(relSlash, sourceText, upperTier)
			}
		default:
			return nil, fmt.Errorf("unsupported read tier %q (expected full, skel, sigs, or tree)", opts.Tier)
		}
	}

	var rawLines []string
	var totalLines int
	if len(sourceText) == 0 {
		totalLines = 0
	} else {
		contentStr := sourceText
		if strings.HasSuffix(contentStr, "\r\n") {
			contentStr = strings.TrimSuffix(contentStr, "\r\n")
		} else if strings.HasSuffix(contentStr, "\n") {
			contentStr = strings.TrimSuffix(contentStr, "\n")
		}
		split := strings.Split(contentStr, "\n")
		rawLines = make([]string, len(split))
		for i, l := range split {
			rawLines[i] = strings.TrimSuffix(l, "\r")
		}
		totalLines = len(rawLines)
	}

	startLine := opts.StartLine
	if startLine <= 0 {
		startLine = 1
	}
	endLine := opts.EndLine
	if endLine <= 0 || endLine > totalLines {
		endLine = totalLines
	}

	maxBytes := opts.MaxBytes
	if maxBytes <= 0 {
		maxBytes = dispatch.MaxCaptureBytes
	}

	lines := make([]LineEntry, 0)
	var contentBuilder strings.Builder
	currentBytes := 0
	truncated := false

	if totalLines > 0 && startLine <= endLine {
		for lineNum := startLine; lineNum <= endLine; lineNum++ {
			rawLine := rawLines[lineNum-1]
			sanitized := dispatch.SanitizeOutput(rawLine)

			lineLen := len(sanitized)
			sepLen := 0
			if len(lines) > 0 {
				sepLen = 1
			}

			if currentBytes+sepLen+lineLen > maxBytes {
				truncated = true
				if len(lines) == 0 {
					avail := maxBytes
					if avail > 0 && avail < len(sanitized) {
						sanitized = sanitized[:avail]
					}
					lines = append(lines, LineEntry{Line: lineNum, Text: sanitized})
					contentBuilder.WriteString(sanitized)
				}
				break
			}

			if len(lines) > 0 {
				contentBuilder.WriteByte('\n')
				currentBytes++
			}
			lines = append(lines, LineEntry{Line: lineNum, Text: sanitized})
			contentBuilder.WriteString(sanitized)
			currentBytes += lineLen
		}
	}

	return &ReadResult{
		File:       filepath.ToSlash(opts.Path),
		Tier:       resolvedTier,
		TotalLines: totalLines,
		StartLine:  startLine,
		EndLine:    endLine,
		SHA256:     fileSHA,
		Truncated:  truncated,
		Lines:      lines,
		Content:    contentBuilder.String(),
		JITWhisper: lookupJITWhisper(ctx, relSlash),
	}, nil
}

// Edit replaces occurrences of OldString with NewString in the specified file atomically,
// verifying scope, guarding against binary files, checking optimistic concurrency via IfSHA256,
// preserving CRLF line endings, and returning the updated SHA-256.
func Edit(ctx context.Context, opts EditOptions) (*EditResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	editMu.Lock()
	defer editMu.Unlock()

	relSlash, err := VerifyWriteScope(opts.Path, opts.ScopeRoot, opts.AllowedPaths)
	if err != nil {
		return nil, err
	}
	if opts.OldString == "" {
		return nil, fmt.Errorf("old_string must not be empty")
	}

	canonTarget, err := evalCanonicalPath(opts.Path)
	if err != nil {
		return nil, err
	}
	if _, err := VerifyWriteScope(canonTarget, opts.ScopeRoot, opts.AllowedPaths); err != nil {
		return nil, err
	}
	scopeRoot := opts.ScopeRoot
	if scopeRoot == "" {
		scopeRoot = "."
	}
	canonScope, err := evalCanonicalPath(scopeRoot)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(canonTarget, canonScope) {
		return nil, fmt.Errorf("path %q escapes scope root %q", opts.Path, opts.ScopeRoot)
	}

	info, err := os.Stat(canonTarget)
	if err != nil {
		return nil, err
	}

	raw, err := os.ReadFile(canonTarget)
	if err != nil {
		return nil, err
	}

	checkLen := len(raw)
	if checkLen > 512 {
		checkLen = 512
	}
	if bytes.IndexByte(raw[:checkLen], 0) >= 0 {
		return nil, fmt.Errorf("binary file cannot be edited as text")
	}

	oldSum := sha256.Sum256(raw)
	oldHex := hex.EncodeToString(oldSum[:])

	if opts.IfSHA256 != "" && !strings.EqualFold(opts.IfSHA256, oldHex) {
		return nil, fmt.Errorf("sha256 conflict on %s: expected %s, got %s", opts.Path, opts.IfSHA256, oldHex)
	}

	isCRLF := bytes.Contains(raw, []byte("\r\n"))
	normContent := strings.ReplaceAll(string(raw), "\r\n", "\n")
	normOld := strings.ReplaceAll(opts.OldString, "\r\n", "\n")
	normNew := strings.ReplaceAll(opts.NewString, "\r\n", "\n")

	count := strings.Count(normContent, normOld)
	if count == 0 {
		return nil, formatOldStringNotFoundError(opts.Path, normContent, normOld, oldHex)
	}
	if count > 1 && !opts.ReplaceAll {
		matchLines := findMatchLineNumbers(normContent, normOld)
		return nil, fmt.Errorf("old_string found %d times at lines %s; set --all to replace all occurrences or include surrounding lines", count, formatIntSlice(matchLines))
	}

	var replaced string
	var replacedCount int
	if opts.ReplaceAll {
		replaced = strings.ReplaceAll(normContent, normOld, normNew)
		replacedCount = count
	} else {
		replaced = strings.Replace(normContent, normOld, normNew, 1)
		replacedCount = 1
	}

	if isCRLF {
		replaced = strings.ReplaceAll(replaced, "\n", "\r\n")
	}

	dir := filepath.Dir(canonTarget)
	if !strings.HasPrefix(dir, canonScope) {
		return nil, fmt.Errorf("directory %q escapes scope root %q", dir, opts.ScopeRoot)
	}
	tmpFile, err := os.CreateTemp(dir, ".g8s-edit-*")
	if err != nil {
		return nil, err
	}
	tmpName := tmpFile.Name()
	cleaned := false
	defer func() {
		if !cleaned {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmpFile.WriteString(replaced); err != nil {
		tmpFile.Close()
		return nil, err
	}
	if err := tmpFile.Chmod(info.Mode().Perm()); err != nil {
		tmpFile.Close()
		return nil, err
	}
	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		return nil, err
	}
	if err := tmpFile.Close(); err != nil {
		return nil, err
	}

	if err := os.Rename(tmpName, canonTarget); err != nil {
		return nil, err
	}
	cleaned = true

	newSum := sha256.Sum256([]byte(replaced))
	newHex := hex.EncodeToString(newSum[:])

	return &EditResult{
		File:         filepath.ToSlash(opts.Path),
		Replacements: replacedCount,
		OldSHA256:    oldHex,
		NewSHA256:    newHex,
		JITWhisper:   lookupJITWhisper(ctx, relSlash),
	}, nil
}

func findMatchLineNumbers(content, sub string) []int {
	if sub == "" {
		return nil
	}
	var lines []int
	offset := 0
	for {
		idx := strings.Index(content[offset:], sub)
		if idx < 0 {
			break
		}
		absIdx := offset + idx
		lineNum := strings.Count(content[:absIdx], "\n") + 1
		lines = append(lines, lineNum)
		offset = absIdx + len(sub)
		if offset >= len(content) {
			break
		}
	}
	return lines
}

func formatIntSlice(nums []int) string {
	var parts []string
	for _, n := range nums {
		parts = append(parts, fmt.Sprintf("%d", n))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func formatOldStringNotFoundError(path, content, needle, shaHex string) error {
	lines := strings.Split(content, "\n")
	totalLines := len(lines)
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		totalLines = len(lines) - 1
	}
	nearLine, nearPreview := findNearestMatchLine(lines, needle)
	if nearLine > 0 {
		startHint := nearLine - 3
		if startHint < 1 {
			startHint = 1
		}
		endHint := nearLine + 5
		return fmt.Errorf("old_string not found in file %s (total_lines=%d sha256=%s; nearest match at line %d: %q; run 'g8s read %s --lines %d:%d' to inspect exact content)",
			path, totalLines, shaHex, nearLine, nearPreview, path, startHint, endHint)
	}
	return fmt.Errorf("old_string not found in file %s (total_lines=%d sha256=%s)", path, totalLines, shaHex)
}

func findNearestMatchLine(fileLines []string, needle string) (int, string) {
	needleFirst := ""
	for _, nl := range strings.Split(needle, "\n") {
		if trimmed := strings.TrimSpace(nl); trimmed != "" {
			needleFirst = trimmed
			break
		}
	}
	if needleFirst == "" {
		return 0, ""
	}
	bestLine := 0
	bestScore := 0
	bestText := ""
	needleLower := strings.ToLower(needleFirst)
	needleTrigrams := extractTrigrams(needleLower)

	for i, rawLine := range fileLines {
		trimmed := strings.TrimSpace(rawLine)
		if trimmed == "" {
			continue
		}
		lineLower := strings.ToLower(trimmed)
		score := 0
		for tri := range needleTrigrams {
			if strings.Contains(lineLower, tri) {
				score++
			}
		}
		if score > bestScore {
			bestScore = score
			bestLine = i + 1
			bestText = trimmed
		}
	}
	if len(bestText) > 80 {
		bestText = bestText[:80] + "..."
	}
	return bestLine, bestText
}

func extractTrigrams(s string) map[string]bool {
	out := make(map[string]bool)
	runes := []rune(s)
	if len(runes) < 3 {
		out[s] = true
		return out
	}
	for i := 0; i <= len(runes)-3; i++ {
		out[string(runes[i:i+3])] = true
	}
	return out
}

// ApplyBatchEdits validates and applies a slice of EditOptions atomically.
// Phase 1 validates every edit in memory (scope, binary check, IfSHA256, OldString match)
// so that if any edit fails, zero files on disk are mutated. Phase 2 writes temp files
// and renames them into place, rolling back any prior renames if an OS write error occurs.
func ApplyBatchEdits(ctx context.Context, edits []EditOptions) ([]*EditResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(edits) == 0 {
		return nil, fmt.Errorf("batch edits must not be empty")
	}
	editMu.Lock()
	defer editMu.Unlock()

	type stagedFile struct {
		canonTarget string
		canonScope  string
		scopeRoot   string
		mode        os.FileMode
		origBytes   []byte
		curBytes    []byte
		curHex      string
	}

	stagedByTarget := make(map[string]*stagedFile)
	var targetOrder []string
	results := make([]*EditResult, 0, len(edits))

	for idx, opts := range edits {
		relSlash, err := VerifyWriteScope(opts.Path, opts.ScopeRoot, opts.AllowedPaths)
		if err != nil {
			return nil, fmt.Errorf("batch edit[%d] (%s): %w", idx, opts.Path, err)
		}
		if opts.OldString == "" {
			return nil, fmt.Errorf("batch edit[%d] (%s): old_string must not be empty", idx, opts.Path)
		}
		canonTarget, err := evalCanonicalPath(opts.Path)
		if err != nil {
			return nil, fmt.Errorf("batch edit[%d] (%s): %w", idx, opts.Path, err)
		}
		if _, err := VerifyWriteScope(canonTarget, opts.ScopeRoot, opts.AllowedPaths); err != nil {
			return nil, fmt.Errorf("batch edit[%d] (%s): %w", idx, opts.Path, err)
		}
		scopeRoot := opts.ScopeRoot
		if scopeRoot == "" {
			scopeRoot = "."
		}
		canonScope, err := evalCanonicalPath(scopeRoot)
		if err != nil {
			return nil, fmt.Errorf("batch edit[%d] (%s): %w", idx, opts.Path, err)
		}
		if !strings.HasPrefix(canonTarget, canonScope) {
			return nil, fmt.Errorf("batch edit[%d]: path %q escapes scope root %q", idx, opts.Path, opts.ScopeRoot)
		}

		sf, exists := stagedByTarget[canonTarget]
		if !exists {
			info, err := os.Stat(canonTarget)
			if err != nil {
				return nil, fmt.Errorf("batch edit[%d] (%s): %w", idx, opts.Path, err)
			}
			raw, err := os.ReadFile(canonTarget)
			if err != nil {
				return nil, fmt.Errorf("batch edit[%d] (%s): %w", idx, opts.Path, err)
			}
			checkLen := len(raw)
			if checkLen > 512 {
				checkLen = 512
			}
			if bytes.IndexByte(raw[:checkLen], 0) >= 0 {
				return nil, fmt.Errorf("batch edit[%d] (%s): binary file cannot be edited as text", idx, opts.Path)
			}
			sum := sha256.Sum256(raw)
			sf = &stagedFile{
				canonTarget: canonTarget,
				canonScope:  canonScope,
				scopeRoot:   opts.ScopeRoot,
				mode:        info.Mode().Perm(),
				origBytes:   raw,
				curBytes:    raw,
				curHex:      hex.EncodeToString(sum[:]),
			}
			stagedByTarget[canonTarget] = sf
			targetOrder = append(targetOrder, canonTarget)
		}

		oldHex := sf.curHex
		if opts.IfSHA256 != "" && !strings.EqualFold(opts.IfSHA256, oldHex) {
			return nil, fmt.Errorf("batch edit[%d] sha256 conflict on %s: expected %s, got %s", idx, opts.Path, opts.IfSHA256, oldHex)
		}

		isCRLF := bytes.Contains(sf.curBytes, []byte("\r\n"))
		normContent := strings.ReplaceAll(string(sf.curBytes), "\r\n", "\n")
		normOld := strings.ReplaceAll(opts.OldString, "\r\n", "\n")
		normNew := strings.ReplaceAll(opts.NewString, "\r\n", "\n")

		count := strings.Count(normContent, normOld)
		if count == 0 {
			return nil, fmt.Errorf("batch edit[%d] (%s): %w", idx, opts.Path, formatOldStringNotFoundError(opts.Path, normContent, normOld, oldHex))
		}
		if count > 1 && !opts.ReplaceAll {
			matchLines := findMatchLineNumbers(normContent, normOld)
			return nil, fmt.Errorf("batch edit[%d] (%s): old_string found %d times at lines %s; set --all to replace all occurrences or include surrounding lines", idx, opts.Path, count, formatIntSlice(matchLines))
		}

		var replaced string
		var replacedCount int
		if opts.ReplaceAll {
			replaced = strings.ReplaceAll(normContent, normOld, normNew)
			replacedCount = count
		} else {
			replaced = strings.Replace(normContent, normOld, normNew, 1)
			replacedCount = 1
		}
		if isCRLF {
			replaced = strings.ReplaceAll(replaced, "\n", "\r\n")
		}

		newBytes := []byte(replaced)
		newSum := sha256.Sum256(newBytes)
		newHex := hex.EncodeToString(newSum[:])
		sf.curBytes = newBytes
		sf.curHex = newHex

		results = append(results, &EditResult{
			File:         filepath.ToSlash(opts.Path),
			Replacements: replacedCount,
			OldSHA256:    oldHex,
			NewSHA256:    newHex,
			JITWhisper:   lookupJITWhisper(ctx, relSlash),
		})
	}

	var committed []*stagedFile
	rollback := func() {
		for _, c := range committed {
			if rbErr := os.WriteFile(c.canonTarget, c.origBytes, c.mode); rbErr != nil {
				fmt.Fprintf(os.Stderr, "[warn] fsops: rollback %s: %v\n", c.canonTarget, rbErr)
			}
		}
	}

	for _, target := range targetOrder {
		sf := stagedByTarget[target]
		dir := filepath.Dir(sf.canonTarget)
		if !strings.HasPrefix(dir, sf.canonScope) {
			rollback()
			return nil, fmt.Errorf("directory %q escapes scope root %q", dir, sf.scopeRoot)
		}
		tmpFile, err := os.CreateTemp(dir, ".g8s-batch-edit-*")
		if err != nil {
			rollback()
			return nil, err
		}
		tmpName := tmpFile.Name()
		if _, err := tmpFile.Write(sf.curBytes); err != nil {
			tmpFile.Close()
			_ = os.Remove(tmpName)
			rollback()
			return nil, err
		}
		if err := tmpFile.Chmod(sf.mode); err != nil {
			tmpFile.Close()
			_ = os.Remove(tmpName)
			rollback()
			return nil, err
		}
		if err := tmpFile.Sync(); err != nil {
			tmpFile.Close()
			_ = os.Remove(tmpName)
			rollback()
			return nil, err
		}
		if err := tmpFile.Close(); err != nil {
			_ = os.Remove(tmpName)
			rollback()
			return nil, err
		}
		if err := os.Rename(tmpName, sf.canonTarget); err != nil {
			_ = os.Remove(tmpName)
			rollback()
			return nil, err
		}
		committed = append(committed, sf)
	}

	return results, nil
}

// lookupJITWhisper performs a non-blocking, fail-open spatial query against the
// Knowledge Vault when G8S_VAULT_DB (or G8S_STATE_DIR/vault.db) exists on disk.
// It never creates a new database file and never fails Read or Edit (#594).
func lookupJITWhisper(ctx context.Context, relPath string) []string {
	if os.Getenv("G8S_JIT_WHISPER") == "0" {
		return nil
	}
	maxRules := vault.ResolveMaxActiveRules()
	if maxRules <= 0 {
		return nil
	}
	vdbPath := strings.TrimSpace(os.Getenv("G8S_VAULT_DB"))
	if vdbPath == "" {
		if stateDir := strings.TrimSpace(os.Getenv("G8S_STATE_DIR")); stateDir != "" {
			vdbPath = filepath.Join(stateDir, "vault.db")
		}
	}
	if vdbPath == "" {
		return nil
	}
	if _, err := os.Stat(vdbPath); err != nil {
		return nil
	}
	v, err := vault.NewVault(vdbPath, nil)
	if err != nil {
		return nil
	}
	defer v.Close()

	records, err := v.MatchSpatial(ctx, relPath, maxRules)
	if err != nil || len(records) == 0 {
		return nil
	}
	return vault.FormatJITWhisper(records)
}

// SelectJSON evaluates a JSON dot-path selector against raw JSON bytes
// and returns the matching value formatted as a valid JSON string (DELTA-23).
func SelectJSON(rawJSON []byte, expr string) (string, error) {
	trimmedJSON := strings.TrimSpace(string(rawJSON))
	if trimmedJSON == "" {
		return "", fmt.Errorf("empty JSON input")
	}
	var root any
	dec := json.NewDecoder(bytes.NewReader([]byte(trimmedJSON)))
	dec.UseNumber()
	if err := dec.Decode(&root); err != nil {
		return "", fmt.Errorf("invalid json: %w", err)
	}

	node, err := evalJSONNode(root, strings.TrimSpace(expr))
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(node)
	if err != nil {
		return "", fmt.Errorf("marshal selected node: %w", err)
	}
	return string(b), nil
}

func evalJSONNode(curr any, expr string) (any, error) {
	expr = strings.TrimSpace(expr)
	if strings.HasPrefix(expr, ".") {
		expr = strings.TrimSpace(strings.TrimPrefix(expr, "."))
	}
	if expr == "" {
		return curr, nil
	}

	for expr != "" {
		if strings.HasPrefix(expr, ".") {
			expr = strings.TrimSpace(strings.TrimPrefix(expr, "."))
			if expr == "" {
				return curr, nil
			}
		}

		if strings.HasPrefix(expr, "[") {
			closeIdx := strings.Index(expr, "]")
			if closeIdx == -1 {
				return nil, fmt.Errorf("unmatched '[' in selector expression %q", expr)
			}
			inside := strings.TrimSpace(expr[1:closeIdx])
			remaining := strings.TrimSpace(expr[closeIdx+1:])

			if inside == "" {
				// Array projection: []
				slice, ok := curr.([]any)
				if !ok {
					return nil, fmt.Errorf("cannot project across non-array (%T)", curr)
				}
				results := make([]any, 0, len(slice))
				for _, item := range slice {
					res, err := evalJSONNode(item, remaining)
					if err != nil {
						return nil, err
					}
					results = append(results, res)
				}
				return results, nil
			}

			idx, err := strconv.Atoi(inside)
			if err != nil {
				return nil, fmt.Errorf("invalid array index %q", inside)
			}
			slice, ok := curr.([]any)
			if !ok {
				return nil, fmt.Errorf("cannot index non-array (%T)", curr)
			}
			if idx < 0 || idx >= len(slice) {
				return nil, fmt.Errorf("index %d out of bounds (len %d)", idx, len(slice))
			}
			curr = slice[idx]
			expr = remaining
			continue
		}

		// Field extraction
		end := len(expr)
		dotIdx := strings.Index(expr, ".")
		bracketIdx := strings.Index(expr, "[")

		if dotIdx != -1 && bracketIdx != -1 {
			if dotIdx < bracketIdx {
				end = dotIdx
			} else {
				end = bracketIdx
			}
		} else if dotIdx != -1 {
			end = dotIdx
		} else if bracketIdx != -1 {
			end = bracketIdx
		}

		fieldName := strings.TrimSpace(expr[:end])
		if fieldName == "" {
			return nil, fmt.Errorf("empty field name in selector expression")
		}
		expr = strings.TrimSpace(expr[end:])

		if slice, ok := curr.([]any); ok {
			idx, err := strconv.Atoi(fieldName)
			if err != nil {
				return nil, fmt.Errorf("cannot access non-integer index %q on array (%T)", fieldName, curr)
			}
			if idx < 0 || idx >= len(slice) {
				return nil, fmt.Errorf("index %d out of bounds (len %d)", idx, len(slice))
			}
			curr = slice[idx]
			continue
		}

		obj, ok := curr.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("cannot access field %q on non-object (%T)", fieldName, curr)
		}
		val, exists := obj[fieldName]
		if !exists {
			keys := make([]string, 0, len(obj))
			for k := range obj {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			return nil, fmt.Errorf("field %q not found (available keys: [%s])", fieldName, strings.Join(keys, ", "))
		}
		curr = val
	}

	return curr, nil
}
