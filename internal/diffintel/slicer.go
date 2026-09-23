package diffintel

import (
	"fmt"
	"strings"
)

const (
	DefaultMaxTokensPerChunk = 1500
	ApproxCharsPerToken      = 4
)

// ClassifyRisk determines the risk category based on file path and changed symbols.
func ClassifyRisk(path string) string {
	lower := strings.ToLower(path)
	if strings.Contains(lower, "auth") ||
		strings.Contains(lower, "crypto") ||
		strings.Contains(lower, "security") ||
		strings.Contains(lower, "token") ||
		strings.Contains(lower, "secret") ||
		strings.Contains(lower, "concurrency") ||
		strings.Contains(lower, "mutex") ||
		strings.Contains(lower, "sql") {
		return "HIGH"
	}
	if strings.HasSuffix(lower, "_test.go") ||
		strings.HasSuffix(lower, ".md") ||
		strings.HasSuffix(lower, ".txt") ||
		strings.Contains(lower, "testdata/") {
		return "LOW"
	}
	return "MEDIUM"
}

// SliceChunks splits a list of FileDiffs into manageable, context-isolated ReviewChunks.
// If maxTokens is <= 0, DefaultMaxTokensPerChunk is used.
func SliceChunks(files []FileDiff, maxTokens int) []ReviewChunk {
	if maxTokens <= 0 {
		maxTokens = DefaultMaxTokensPerChunk
	}

	var chunks []ReviewChunk
	chunkIndex := 1

	for _, file := range files {
		if len(file.Hunks) == 0 {
			continue
		}

		filePath := file.NewPath
		if filePath == "" {
			filePath = file.OldPath
		}
		risk := ClassifyRisk(filePath)

		var currentHunks []Hunk
		var currentChars int

		flushCurrentChunk := func() {
			if len(currentHunks) == 0 {
				return
			}
			chunkID := fmt.Sprintf("chunk-%03d", chunkIndex)
			chunkIndex++

			formatted := formatChunkBody(filePath, risk, currentHunks)
			tokenEst := (len(formatted) + ApproxCharsPerToken - 1) / ApproxCharsPerToken

			chunks = append(chunks, ReviewChunk{
				ID:            chunkID,
				FilePath:      filePath,
				RiskLevel:     risk,
				Hunks:         currentHunks,
				TokenEstimate: tokenEst,
				FormattedBody: formatted,
			})
			currentHunks = nil
			currentChars = 0
		}

		for _, hunk := range file.Hunks {
			hunkChars := estimateHunkChars(hunk)
			if len(currentHunks) > 0 && (currentChars+hunkChars)/ApproxCharsPerToken > maxTokens {
				flushCurrentChunk()
			}
			currentHunks = append(currentHunks, hunk)
			currentChars += hunkChars
		}
		flushCurrentChunk()
	}

	return chunks
}

func estimateHunkChars(hunk Hunk) int {
	chars := len(hunk.Header) + 30
	for _, l := range hunk.Lines {
		chars += len(l.Content) + 15
	}
	return chars
}

// formatChunkBody generates a clear, anchored code representation
// with explicit line numbers so the LLM has zero chance of position drift.
func formatChunkBody(filePath string, risk string, hunks []Hunk) string {
	var b strings.Builder
	fmt.Fprintf(&b, "File: %s (Risk: %s)\n", filePath, risk)
	for i, h := range hunks {
		fmt.Fprintf(&b, "\n--- Hunk %d (Lines %d-%d) %s ---\n", i+1, h.NewStart, h.NewStart+h.NewLines-1, h.Header)
		for _, l := range h.Lines {
			switch l.Type {
			case LineAdd:
				fmt.Fprintf(&b, "L%04d: + %s\n", l.NewLine, l.Content)
			case LineDelete:
				fmt.Fprintf(&b, "     : - %s\n", l.Content)
			case LineContext:
				fmt.Fprintf(&b, "L%04d:   %s\n", l.NewLine, l.Content)
			}
		}
	}
	return b.String()
}
