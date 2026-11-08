// Package dogfood classifies commit provenance without performing Git I/O.
package dogfood

import (
	"fmt"
	"sort"
	"strings"
)

// E_PROVENANCE_TAG_EMPTY_REASON is returned in provenance Ref when an F0-direct
// tag omits its required reason line.
const E_PROVENANCE_TAG_EMPTY_REASON = "E_PROVENANCE_TAG_EMPTY_REASON"

// Provenance records a receipt task ID or the reason for an F0-direct landing.
type Provenance struct {
	Kind string
	Ref  string
}

// ClassifyCommit reads the final blank-line-separated trailer paragraph.
// Keys are case-insensitive; surrounding whitespace is ignored. Receipt values
// are nonempty single tokens. Provenance values must be F0-direct — <reason>.
// Body mentions, empty values, conflicting or repeated provenance are unclassified.
func ClassifyCommit(msg string) Provenance {
	unknown := Provenance{Kind: "unclassified"}
	msg = strings.TrimSpace(strings.ReplaceAll(msg, "\r\n", "\n"))
	lines := strings.Split(msg, "\n")
	start := len(lines) - 1
	for start > 0 && strings.TrimSpace(lines[start-1]) != "" {
		start--
	}
	// A subject alone is not a trailer paragraph.
	if start == 0 {
		return unknown
	}
	result := unknown
	for _, line := range lines[start:] {
		key, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(key) == "" {
			return unknown
		}
		value = strings.TrimSpace(value)
		var candidate Provenance
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "receipt":
			if len(strings.Fields(value)) != 1 {
				return unknown
			}
			candidate = Provenance{Kind: "receipt", Ref: value}
		case "provenance":
			kind, reason, found := strings.Cut(value, "—")
			if !found || !strings.EqualFold(strings.TrimSpace(kind), "F0-direct") {
				return unknown
			}
			if strings.TrimSpace(reason) == "" {
				return Provenance{Kind: "classification_error", Ref: E_PROVENANCE_TAG_EMPTY_REASON}
			}
			candidate = Provenance{Kind: "f0_direct", Ref: strings.TrimSpace(reason)}
		default:
			continue
		}
		if result.Kind != "unclassified" {
			return unknown
		}
		result = candidate
	}
	return result
}

// Ratio returns counts and receipts/(receipts+f0Direct), over classified commits
// only. Unclassified commits are reported separately as the honesty gap. The
// ratio is zero when no commits are classified; callers must label it undefined.
func Ratio(report []Provenance) (receipts int, f0Direct int, unclassified int, total int, dogfoodRatio float64) {
	for _, p := range report {
		switch p.Kind {
		case "receipt":
			receipts++
		case "f0_direct":
			f0Direct++
		case "unclassified", "classification_error":
			unclassified++
		default:
			unclassified++
		}
	}
	total = len(report)
	if classified := receipts + f0Direct; classified > 0 {
		dogfoodRatio = float64(receipts) / float64(classified)
	}
	return
}

// UnclassifiedBreakdown tallies honesty-gap landings by issue label for visibility reports.
func UnclassifiedBreakdown(report []Provenance) map[string]int {
	counts := make(map[string]int)
	for _, p := range report {
		switch p.Kind {
		case "unclassified":
			counts["subject-without-trailer"]++
		case "classification_error":
			code := strings.TrimSpace(p.Ref)
			if code == "" {
				code = "unknown-classification-error"
			}
			counts[code]++
		}
	}
	return counts
}

// FormatUnclassifiedBreakdown renders a stable markdown section for CI logs.
func FormatUnclassifiedBreakdown(report []Provenance) string {
	breakdown := UnclassifiedBreakdown(report)
	if len(breakdown) == 0 {
		return ""
	}
	keys := make([]string, 0, len(breakdown))
	for k := range breakdown {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("### Unclassified breakdown\n\n| Issue | Count |\n| --- | ---: |\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "| %s | %d |\n", k, breakdown[k])
	}
	b.WriteString("\n")
	return b.String()
}
