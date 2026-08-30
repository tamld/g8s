package conv

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Solution represents a single worker's design proposal parsed from solution.md.
type Solution struct {
	Path          string            `json:"path"`
	WorkerID      string            `json:"worker_id"`
	Title         string            `json:"title"`
	RawText       string            `json:"raw_text"`
	Sections      map[string]string `json:"sections"`       // Normalized Heading -> Body
	RawSections   map[string]string `json:"raw_sections"`   // Original Heading -> Body
	Headings      []string          `json:"headings"`       // List of normalized headings in order
	KeyComponents []string          `json:"key_components"` // Extracted architectural components/bullets
	Assumptions   []string          `json:"assumptions"`    // Explicit or implicit assumptions
	Tradeoffs     []string          `json:"tradeoffs"`      // Listed tradeoffs or decisions
}

var headingRegex = regexp.MustCompile(`^(#{1,6})\s+(.+)$`)

// NormalizeHeading converts a markdown heading into a canonical lookup key.
func NormalizeHeading(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	// Strip leading numbers like "1. ", "1.1 ", "step 1:"
	h = regexp.MustCompile(`^(\d+[\.\)]\s*|step\s*\d+[:\.]?\s*)`).ReplaceAllString(h, "")
	// Remove punctuation and extra spaces
	h = regexp.MustCompile(`[^\w\s-]`).ReplaceAllString(h, "")
	return strings.Join(strings.Fields(h), " ")
}

// ParseSolutionFile reads and parses a solution.md file from disk.
func ParseSolutionFile(path, workerID string) (*Solution, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("conv: read solution file %s: %w", path, err)
	}
	sol := ParseSolutionContent(string(data), workerID)
	sol.Path = path
	return sol, nil
}

// ParseSolutionContent parses the raw text of a solution markdown document.
func ParseSolutionContent(content, workerID string) *Solution {
	sol := &Solution{
		WorkerID:    workerID,
		RawText:     content,
		Sections:    make(map[string]string),
		RawSections: make(map[string]string),
	}

	scanner := bufio.NewScanner(strings.NewReader(content))
	var currentNormHeading string
	var currentRawHeading string
	var currentBody []string

	flushSection := func() {
		if currentNormHeading != "" && len(currentBody) > 0 {
			bodyText := strings.TrimSpace(strings.Join(currentBody, "\n"))
			sol.Sections[currentNormHeading] = bodyText
			sol.RawSections[currentRawHeading] = bodyText
		}
		currentBody = nil
	}

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if matches := headingRegex.FindStringSubmatch(trimmed); len(matches) == 3 {
			level := len(matches[1])
			headingText := strings.TrimSpace(matches[2])

			if level == 1 && sol.Title == "" {
				sol.Title = headingText
				continue
			}

			flushSection()
			currentRawHeading = headingText
			currentNormHeading = NormalizeHeading(headingText)
			sol.Headings = append(sol.Headings, currentNormHeading)
			continue
		}

		if currentNormHeading != "" {
			currentBody = append(currentBody, line)
		} else if sol.Title == "" && strings.HasPrefix(trimmed, "# ") {
			sol.Title = strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
		}
	}
	flushSection()

	if sol.Title == "" {
		sol.Title = fmt.Sprintf("Solution by %s", workerID)
	}

	// Extract key components, assumptions, tradeoffs from sections
	for normKey, body := range sol.Sections {
		lines := strings.Split(body, "\n")
		for _, l := range lines {
			t := strings.TrimSpace(l)
			if strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ") {
				item := strings.TrimSpace(t[2:])
				if strings.Contains(normKey, "assumption") {
					sol.Assumptions = append(sol.Assumptions, item)
				} else if strings.Contains(normKey, "tradeoff") || strings.Contains(normKey, "pros and cons") {
					sol.Tradeoffs = append(sol.Tradeoffs, item)
				} else {
					sol.KeyComponents = append(sol.KeyComponents, item)
				}
			}
		}
	}

	return sol
}
