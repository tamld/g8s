package review

import (
	"fmt"
	"strings"

	"github.com/tamld/g8s/internal/diffintel"
)

// BuildVerifierPrompt formats the prompt payload for the verifier worker.
func BuildVerifierPrompt(chunk diffintel.ReviewChunk, intent string) string {
	var b strings.Builder
	b.WriteString("You are a specialized Verifier Subagent running under g8s zero-trust harness.\n")
	b.WriteString("Your task is to perform an exact, line-level code review for security, concurrency, and logic defects.\n")

	if strings.TrimSpace(intent) != "" {
		fmt.Fprintf(&b, "\nReview Intent: %s\n", strings.TrimSpace(intent))
	}

	b.WriteString("\nInstructions:\n")
	b.WriteString("1. Inspect only the modified lines ('+') and immediate context.\n")
	b.WriteString("2. Every line has a designated line number (e.g. 'L0042:' means line 42). Anchor your comments to exact line numbers.\n")
	b.WriteString("3. Focus on: Null Pointer Exceptions, Concurrency Data Races, Resource/Goroutine Leaks, SQL/Command Injections, Logic Edge-cases.\n")
	b.WriteString("4. Output MUST be ONLY valid JSON matching this schema with no markdown wrapping or conversational prose:\n")
	b.WriteString(`{
  "findings": [
    {
      "line": 42,
      "severity": "CRITICAL|HIGH|MEDIUM|LOW",
      "category": "CONCURRENCY|SECURITY|CORRECTNESS|PERFORMANCE",
      "message": "Specific explanation of defect",
      "suggestion": "Concrete remediation or fix"
    }
  ]
}
If no issues are found, return {"findings": []}.
`)

	b.WriteString("\n--- TARGET CODE CHUNK ---\n")
	b.WriteString(chunk.FormattedBody)
	b.WriteString("\n--- END CODE CHUNK ---\n")

	return b.String()
}
