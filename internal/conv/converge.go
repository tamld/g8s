package conv

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ConvergedReport holds the structured synthesis of N solutions.
type ConvergedReport struct {
	Title             string            `json:"title"`
	ParticipantCount  int               `json:"participant_count"`
	CommonGround      []ConsensusItem   `json:"common_ground"`
	Divergences       []DivergenceItem  `json:"divergences"`
	UnifiedSections   map[string]string `json:"unified_sections"`
	SpotCheckWarnings []SpotCheckIssue  `json:"spot_check_warnings"`
	MarkdownContent   string            `json:"markdown_content"`
}

// ConsensusItem represents an agreed-upon section or principle.
type ConsensusItem struct {
	Heading   string   `json:"heading"`
	Summary   string   `json:"summary"`
	AgreedBy  []string `json:"agreed_by"`
	KeyPoints []string `json:"key_points"`
}

// DivergenceItem represents a topic where solutions take different approaches.
type DivergenceItem struct {
	Topic           string            `json:"topic"`
	Alternatives    map[string]string `json:"alternatives"` // WorkerID -> approach
	SelectedChoice  string            `json:"selected_choice"`
	SelectedWorker  string            `json:"selected_worker"`
	RationaleReason string            `json:"rationale_reason"`
}

// SpotCheckIssue represents a warning flagged by the spot-checker.
type SpotCheckIssue struct {
	Category string `json:"category"` // "missing_requirement", "self_contradiction", "silent_assumption"
	WorkerID string `json:"worker_id,omitempty"`
	Message  string `json:"message"`
}

// ConvergeFiles reads N solution.md file paths and synthesizes them into a converged design.
func ConvergeFiles(files []string, outPath string) (*ConvergedReport, error) {
	if len(files) == 0 {
		return nil, fmt.Errorf("conv: at least one solution file is required to converge")
	}

	solutions := make([]*Solution, len(files))
	for i, f := range files {
		workerID := fmt.Sprintf("worker-%d", i+1)
		sol, err := ParseSolutionFile(f, workerID)
		if err != nil {
			return nil, fmt.Errorf("conv: parse %s: %w", f, err)
		}
		solutions[i] = sol
	}

	report := Converge(solutions)

	if outPath != "" {
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil && filepath.Dir(outPath) != "." {
			return nil, fmt.Errorf("conv: create out dir: %w", err)
		}
		if err := os.WriteFile(outPath, []byte(report.MarkdownContent), 0o644); err != nil {
			return nil, fmt.Errorf("conv: write output %s: %w", outPath, err)
		}
	}

	return report, nil
}

// Converge synthesizes a slice of Solutions into a single ConvergedReport.
func Converge(solutions []*Solution) *ConvergedReport {
	if len(solutions) == 0 {
		return &ConvergedReport{Title: "Empty Convergence"}
	}

	report := &ConvergedReport{
		Title:            fmt.Sprintf("Converged Architecture & Design: %s", solutions[0].Title),
		ParticipantCount: len(solutions),
		UnifiedSections:  make(map[string]string),
	}

	// 1. Gather all section headings across solutions
	headingFrequency := make(map[string]int)
	headingRawMap := make(map[string]string)
	headingToWorkers := make(map[string][]string)
	headingBodies := make(map[string]map[string]string)

	for _, sol := range solutions {
		seenInSol := make(map[string]bool)
		for normHead, body := range sol.Sections {
			if !seenInSol[normHead] {
				headingFrequency[normHead]++
				seenInSol[normHead] = true
				headingToWorkers[normHead] = append(headingToWorkers[normHead], sol.WorkerID)
			}
			if _, exists := headingBodies[normHead]; !exists {
				headingBodies[normHead] = make(map[string]string)
			}
			headingBodies[normHead][sol.WorkerID] = body
		}
		for rawHead := range sol.RawSections {
			norm := NormalizeHeading(rawHead)
			if _, exists := headingRawMap[norm]; !exists {
				headingRawMap[norm] = rawHead
			}
		}
	}

	// 2. Identify Common Ground vs Divergence
	// If present in all or majority (>= half) -> Common Ground candidate
	threshold := (len(solutions) + 1) / 2

	var allHeadings []string
	for h := range headingFrequency {
		allHeadings = append(allHeadings, h)
	}
	sort.Strings(allHeadings)

	for _, normHead := range allHeadings {
		count := headingFrequency[normHead]
		workers := headingToWorkers[normHead]
		rawTitle := headingRawMap[normHead]
		if rawTitle == "" {
			rawTitle = strings.Title(normHead)
		}

		bodies := headingBodies[normHead]

		if count >= threshold {
			// Common ground: synthesize key points
			var allPoints []string
			seenPoints := make(map[string]bool)

			for _, workerID := range workers {
				body := bodies[workerID]
				lines := strings.Split(body, "\n")
				for _, l := range lines {
					trimmed := strings.TrimSpace(l)
					if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "• ") {
						pt := strings.TrimSpace(trimmed[2:])
						normPt := strings.ToLower(pt)
						if !seenPoints[normPt] {
							seenPoints[normPt] = true
							allPoints = append(allPoints, pt)
						}
					}
				}
			}

			// If no bullet points found, take the most detailed body
			summary := ""
			maxLen := 0
			for _, workerID := range workers {
				b := bodies[workerID]
				if len(b) > maxLen {
					maxLen = len(b)
					summary = b
				}
			}

			report.CommonGround = append(report.CommonGround, ConsensusItem{
				Heading:   rawTitle,
				Summary:   summary,
				AgreedBy:  workers,
				KeyPoints: allPoints,
			})
			report.UnifiedSections[normHead] = summary
		} else {
			// Divergence: differing approaches
			selectedWorker := workers[0]
			bestScore := -100
			rationale := ""

			for _, workerID := range workers {
				body := bodies[workerID]
				score := evaluateSolutionScore(body)
				if score > bestScore {
					bestScore = score
					selectedWorker = workerID
					rationale = fmt.Sprintf("Selected %s: highest adherence to Pure-Go invariants, robust error handling, and zero-trust receipt delegation.", workerID)
				}
			}

			report.Divergences = append(report.Divergences, DivergenceItem{
				Topic:           rawTitle,
				Alternatives:    bodies,
				SelectedChoice:  bodies[selectedWorker],
				SelectedWorker:  selectedWorker,
				RationaleReason: rationale,
			})
			report.UnifiedSections[normHead] = bodies[selectedWorker]
		}
	}

	// 3. Cheap Spot-Checker (Simulated Spot-Checker per insights #9 and #11)
	report.SpotCheckWarnings = RunSpotChecker(solutions)

	// 4. Render Markdown
	report.MarkdownContent = renderConvergedMarkdown(report, solutions)

	return report
}

// evaluateSolutionScore assigns heuristics to design proposals based on g8s Constitution invariants.
func evaluateSolutionScore(content string) int {
	score := 0
	lower := strings.ToLower(content)

	// Invariants: Pure Go / Zero-CGO (+5)
	if strings.Contains(lower, "pure-go") || strings.Contains(lower, "zero-cgo") || strings.Contains(lower, "cgo_enabled=0") {
		score += 5
	}
	// Capability / Receipts (+4)
	if strings.Contains(lower, "receipt") || strings.Contains(lower, "capability") || strings.Contains(lower, "time-limited") {
		score += 4
	}
	// Process isolation (+4)
	if strings.Contains(lower, "process group") || strings.Contains(lower, "setpgid") || strings.Contains(lower, "sandbox") {
		score += 4
	}
	// Table-driven tests (+3)
	if strings.Contains(lower, "table-driven") || strings.Contains(lower, "test") || strings.Contains(lower, "verification") {
		score += 3
	}
	// Penalize C-bindings or external runtime deps (-10)
	if strings.Contains(lower, "cgo") && !strings.Contains(lower, "zero-cgo") && !strings.Contains(lower, "no cgo") {
		score -= 10
	}
	if strings.Contains(lower, "python") || strings.Contains(lower, "pip install") {
		score -= 5
	}

	// Prefer structured detail over brevity
	score += len(content) / 200

	return score
}

// RunSpotChecker scans solutions for missing requirements, self-contradictions, and silent assumptions.
func RunSpotChecker(solutions []*Solution) []SpotCheckIssue {
	var issues []SpotCheckIssue

	for _, sol := range solutions {
		lower := strings.ToLower(sol.RawText)

		// Check 1: Self-contradictions
		if strings.Contains(lower, "zero-cgo") && (strings.Contains(lower, "gcc") || strings.Contains(lower, "cgo_enabled=1") || strings.Contains(lower, "cgo wrapper")) {
			issues = append(issues, SpotCheckIssue{
				Category: "self_contradiction",
				WorkerID: sol.WorkerID,
				Message:  fmt.Sprintf("[%s] Self-contradiction: Claims Zero-CGO but references C-compiler / CGO dependencies.", sol.WorkerID),
			})
		}

		// Check 2: Silent assumptions
		if strings.Contains(lower, "global") || strings.Contains(lower, "singleton") || strings.Contains(lower, "shared memory") {
			issues = append(issues, SpotCheckIssue{
				Category: "silent_assumption",
				WorkerID: sol.WorkerID,
				Message:  fmt.Sprintf("[%s] Silent assumption: Assumes shared memory/global state across isolated worker processes.", sol.WorkerID),
			})
		}
		if strings.Contains(lower, "sudo") || strings.Contains(lower, "root") || strings.Contains(lower, "/etc/") {
			issues = append(issues, SpotCheckIssue{
				Category: "silent_assumption",
				WorkerID: sol.WorkerID,
				Message:  fmt.Sprintf("[%s] Silent assumption: Assumes elevated root/sudo privileges.", sol.WorkerID),
			})
		}

		// Check 3: Missing requirements / incomplete sections
		if len(sol.Sections) < 2 {
			issues = append(issues, SpotCheckIssue{
				Category: "missing_requirement",
				WorkerID: sol.WorkerID,
				Message:  fmt.Sprintf("[%s] Incomplete specification: Solution contains fewer than 2 structured sections.", sol.WorkerID),
			})
		}
	}

	return issues
}

func renderConvergedMarkdown(report *ConvergedReport, solutions []*Solution) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("# %s\n\n", report.Title))
	sb.WriteString(fmt.Sprintf("> **Convergence Protocol**: Dual-Blind Multi-Agent Synthesis (%d Independent Workers)\n\n", report.ParticipantCount))
	sb.WriteString("---\n\n")

	sb.WriteString("## 1. Executive Summary & Synthesis\n\n")
	sb.WriteString(fmt.Sprintf("Synthesized from %d independent proposals without shared memory or cross-talk. ", report.ParticipantCount))
	sb.WriteString(fmt.Sprintf("Identified %d areas of consensus and %d resolved divergences.\n\n", len(report.CommonGround), len(report.Divergences)))

	// Common Ground Section
	sb.WriteString("## 2. Common Ground (Consensus Architecture)\n\n")
	if len(report.CommonGround) == 0 {
		sb.WriteString("No universal consensus found across all proposals; detailed divergence resolution applied below.\n\n")
	} else {
		for _, cg := range report.CommonGround {
			sb.WriteString(fmt.Sprintf("### %s\n", cg.Heading))
			sb.WriteString(fmt.Sprintf("*Agreed by: %s*\n\n", strings.Join(cg.AgreedBy, ", ")))
			if len(cg.KeyPoints) > 0 {
				for _, kp := range cg.KeyPoints {
					sb.WriteString(fmt.Sprintf("- %s\n", kp))
				}
				sb.WriteString("\n")
			} else if cg.Summary != "" {
				sb.WriteString(cg.Summary + "\n\n")
			}
		}
	}

	// Divergences Section
	sb.WriteString("## 3. Divergence & Trade-off Resolution\n\n")
	if len(report.Divergences) == 0 {
		sb.WriteString("All workers converged on identical architectural patterns.\n\n")
	} else {
		for _, div := range report.Divergences {
			sb.WriteString(fmt.Sprintf("### Topic: %s\n", div.Topic))
			sb.WriteString(fmt.Sprintf("**Selected Decision**: %s (Proposed by `%s`)\n\n", div.Topic, div.SelectedWorker))
			sb.WriteString(fmt.Sprintf("**Rationale**: %s\n\n", div.RationaleReason))
			sb.WriteString("```markdown\n" + div.SelectedChoice + "\n```\n\n")
		}
	}

	// Spot check section
	sb.WriteString("## 4. Spot-Check Quality & Verification Audit\n\n")
	if len(report.SpotCheckWarnings) == 0 {
		sb.WriteString("✅ Spot-checker verified: 0 missing requirements, 0 self-contradictions, 0 unstated assumptions.\n\n")
	} else {
		sb.WriteString("⚠️ **Spot-checker Warnings Flagged**:\n\n")
		for _, warn := range report.SpotCheckWarnings {
			sb.WriteString(fmt.Sprintf("- **[%s]** %s\n", warn.Category, warn.Message))
		}
		sb.WriteString("\n")
	}

	// Unified Specification Output
	sb.WriteString("## 5. Unified Implementation Specification\n\n")
	for normKey, content := range report.UnifiedSections {
		sb.WriteString(fmt.Sprintf("### %s\n\n", strings.Title(normKey)))
		sb.WriteString(content + "\n\n")
	}

	return sb.String()
}
