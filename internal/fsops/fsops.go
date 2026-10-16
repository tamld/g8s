// Package fsops implements safe, receipt-bounded file system operations
// including bounded reads, DLP sanitization, atomic edits, and delegation context resolution.
package fsops

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tamld/g8s/internal/dispatch"
	"github.com/tamld/g8s/internal/search"
	"github.com/tamld/g8s/internal/vault"
)

// ReadOptions configures a bounded file read operation.
type ReadOptions struct {
	Path         string   `json:"path"`
	ScopeRoot    string   `json:"scope_root"`
	StartLine    int      `json:"start_line"`
	EndLine      int      `json:"end_line"`
	MaxBytes     int      `json:"max_bytes"`
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

// ResolveDelegationContext checks environment variables and flags to determine
// the active receipt ID and scope root, preventing privilege escalation.
func ResolveDelegationContext(flagReceiptID, flagScopeRoot string, isMutation bool) (receiptID string, scopeRoot string, err error) {
	envPerm := strings.TrimSpace(os.Getenv("G8S_PERMISSION"))
	envReceipt := strings.TrimSpace(os.Getenv("G8S_RECEIPT_ID"))
	envScope := strings.TrimSpace(os.Getenv("G8S_SCOPE_ROOT"))

	if isMutation && (envPerm == "read_only" || envPerm == "automation_read") {
		return "", "", fmt.Errorf("permission=%s environment forbids file mutation", envPerm)
	}

	if envReceipt != "" && flagReceiptID != "" && flagReceiptID != envReceipt {
		return "", "", fmt.Errorf("receipt mismatch: flag %q conflicts with delegated worker environment receipt %q", flagReceiptID, envReceipt)
	}

	receiptID = flagReceiptID
	if receiptID == "" {
		receiptID = envReceipt
	}

	scopeRoot = flagScopeRoot
	if scopeRoot == "" {
		scopeRoot = envScope
	}
	if scopeRoot == "" {
		scopeRoot = "."
	}

	return receiptID, scopeRoot, nil
}

// VerifyScope checks that filePath resides within scopeRoot without path traversal,
// and satisfies allowedPaths if any are specified.
// Returns normalized relative path with forward slashes on success.
func VerifyScope(filePath, scopeRoot string, allowedPaths []string) (string, error) {
	if strings.TrimSpace(filePath) == "" {
		return "", fmt.Errorf("path must not be empty")
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
	rel, err := filepath.Rel(absScope, absFile)
	if err != nil {
		return "", fmt.Errorf("compute relative path: %w", err)
	}
	relSlash := filepath.ToSlash(filepath.Clean(rel))
	if relSlash == ".." || strings.HasPrefix(relSlash, "../") || strings.HasPrefix(relSlash, "/") {
		return "", fmt.Errorf("path %q escapes scope root %q", filePath, scopeRoot)
	}
	if len(allowedPaths) > 0 && !search.MatchesAllowedPaths(relSlash, allowedPaths) {
		return "", fmt.Errorf("path %q (relative %q) is outside receipt allowed_paths", filePath, relSlash)
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

	raw, err := os.ReadFile(opts.Path)
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

	var rawLines []string
	var totalLines int
	if len(raw) == 0 {
		totalLines = 0
	} else {
		contentStr := string(raw)
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
	relSlash, err := VerifyScope(opts.Path, opts.ScopeRoot, opts.AllowedPaths)
	if err != nil {
		return nil, err
	}
	if opts.OldString == "" {
		return nil, fmt.Errorf("old_string must not be empty")
	}

	info, err := os.Stat(opts.Path)
	if err != nil {
		return nil, err
	}

	raw, err := os.ReadFile(opts.Path)
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
		return nil, fmt.Errorf("old_string not found in file")
	}
	if count > 1 && !opts.ReplaceAll {
		return nil, fmt.Errorf("old_string found %d times; set --all to replace all occurrences", count)
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

	dir := filepath.Dir(opts.Path)
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

	if err := os.Rename(tmpName, opts.Path); err != nil {
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
