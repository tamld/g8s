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

func isASCIIAlnum(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')
}

func isValidTrailerKey(key string) bool {
	key = strings.TrimSpace(key)
	if len(key) == 0 {
		return false
	}
	if !isASCIIAlnum(key[0]) || !isASCIIAlnum(key[len(key)-1]) {
		return false
	}
	for i := 0; i < len(key); i++ {
		b := key[i]
		if !isASCIIAlnum(b) && b != '-' {
			return false
		}
	}
	return true
}

func isValidReceiptID(tok string) bool {
	if len(tok) < 6 {
		return false
	}
	if !isASCIIAlnum(tok[0]) || !isASCIIAlnum(tok[len(tok)-1]) {
		return false
	}
	hasDigit := false
	for i := 0; i < len(tok); i++ {
		b := tok[i]
		if b >= '0' && b <= '9' {
			hasDigit = true
		} else if !isASCIIAlnum(b) && b != '.' && b != '_' && b != '-' {
			return false
		}
	}
	return hasDigit
}

func provenanceAllowsF0DirectUnfold(line string) bool {
	key, value, ok := strings.Cut(line, ":")
	if !ok {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(key), "provenance") {
		return false
	}
	value = strings.TrimSpace(value)
	kind, _, found := strings.Cut(value, "—")
	if !found {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(kind), "F0-direct")
}

func unfoldTrailerLines(trailer []string) ([]string, bool) {
	out := make([]string, 0, len(trailer))
	for _, line := range trailer {
		trimmed := strings.TrimSpace(line)
		if !strings.Contains(line, ":") && trimmed != "" {
			if len(out) == 0 || !provenanceAllowsF0DirectUnfold(out[len(out)-1]) {
				return nil, false
			}
			out[len(out)-1] = out[len(out)-1] + " " + trimmed
			continue
		}
		out = append(out, line)
	}
	return out, true
}

func parseProvenanceValue(value string) (Provenance, bool) {
	kind, reason, found := strings.Cut(value, "—")
	if found && strings.EqualFold(strings.TrimSpace(kind), "F0-direct") {
		reason = strings.TrimSpace(reason)
		if reason == "" {
			return Provenance{Kind: "classification_error", Ref: E_PROVENANCE_TAG_EMPTY_REASON}, true
		}
		return Provenance{Kind: "f0_direct", Ref: strings.Join(strings.Fields(reason), " ")}, true
	}

	v := strings.TrimSpace(value)
	fields := strings.Fields(v)
	var tok string
	var consumed int
	if len(fields) >= 2 && strings.EqualFold(fields[0], "Receipt") {
		tok = fields[1]
		consumed = 2
	} else if len(fields) >= 3 && strings.EqualFold(fields[0], "worker") && strings.EqualFold(fields[1], "task") {
		tok = fields[2]
		consumed = 3
	} else {
		return Provenance{}, false
	}
	if !isValidReceiptID(tok) {
		return Provenance{}, false
	}
	rest := v
	for i := 0; i < consumed; i++ {
		f := fields[i]
		pos := strings.Index(rest, f)
		if pos < 0 {
			return Provenance{}, false
		}
		rest = rest[pos+len(f):]
	}
	rest = strings.TrimSpace(rest)
	if rest != "" {
		via := len(rest) >= 4 && strings.EqualFold(rest[:3], "via") && (rest[3] == ' ' || rest[3] == '\t')
		viaOK := via && strings.TrimSpace(rest[3:]) != ""
		if !strings.HasPrefix(rest, "(") && !strings.HasPrefix(rest, ";") && !viaOK {
			return Provenance{}, false
		}
	}
	return Provenance{Kind: "receipt", Ref: tok}, true
}

// ClassifyCommit reads the final blank-line-separated trailer paragraph.
// Trailer keys must satisfy isValidTrailerKey (git interpret-trailers token
// grammar); colon-bearing prose lines in the trailer render the commit unclassified.
// Keys are case-insensitive; surrounding whitespace is ignored.
// Receipt: <task-id> and Provenance aliases (Provenance: Receipt <task-id> [<annotation>],
// Provenance: worker task <task-id> [<annotation>]) share one receipt-ID grammar
// validated by isValidReceiptID; optional <annotation> must begin with "(", ";", or
// "via " (case-insensitive). Provenance: F0-direct — <reason> may unfold continuation
// lines without colons onto the reason. Body mentions, empty values, conflicting
// or repeated provenance are unclassified.
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
	trailer, ok := unfoldTrailerLines(lines[start:])
	if !ok {
		return unknown
	}
	result := unknown
	for _, line := range trailer {
		key, value, ok := strings.Cut(line, ":")
		if !ok || !isValidTrailerKey(key) {
			return unknown
		}
		value = strings.TrimSpace(value)
		var candidate Provenance
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "receipt":
			if len(strings.Fields(value)) != 1 || !isValidReceiptID(value) {
				return unknown
			}
			candidate = Provenance{Kind: "receipt", Ref: value}
		case "provenance":
			var parsed bool
			candidate, parsed = parseProvenanceValue(value)
			if !parsed {
				return unknown
			}
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
