// Package dogfood classifies commit provenance without performing Git I/O.
package dogfood

import "strings"

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
			if !found || !strings.EqualFold(strings.TrimSpace(kind), "F0-direct") || strings.TrimSpace(reason) == "" {
				return unknown
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
