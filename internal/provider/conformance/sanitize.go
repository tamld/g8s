package conformance

import (
	"regexp"
	"strings"
)

var (
	reHomeMac     = regexp.MustCompile(`/Users/[A-Za-z0-9_.-]+/`)
	reHomeLinux   = regexp.MustCompile(`/home/[A-Za-z0-9_.-]+/`)
	reTmpMac      = regexp.MustCompile(`(/private)?/var/folders/[^\s"']*`)
	reTmpGeneric  = regexp.MustCompile(`/tmp/g8s-conformance[^\s"']*`)
	reURL         = regexp.MustCompile(`https?://[^\s"'/]+`)
	reIPv4        = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	reBearer      = regexp.MustCompile(`(?i)(bearer\s+|api[_-]?key["'=:\s]+|sk-)[A-Za-z0-9._\-]{6,}`)
	reWindowsHome = regexp.MustCompile(`(?i)[A-Z]:\\Users\\[^\\\s]+\\`)
)

// Sanitize strips host topology, home paths, temp paths and credential-like
// tokens from free text before it is persisted to the public matrix
// (R-Zero-Leakage-DLP-Protocol; AGENTS.md two-layer separation rule).
func Sanitize(s string) string {
	if s == "" {
		return s
	}
	s = reBearer.ReplaceAllString(s, "${1}<redacted>")
	s = reURL.ReplaceAllString(s, "<url>")
	s = reIPv4.ReplaceAllString(s, "<ip>")
	s = reTmpMac.ReplaceAllString(s, "<tmp>")
	s = reTmpGeneric.ReplaceAllString(s, "<tmp>")
	s = reHomeMac.ReplaceAllString(s, "~/")
	s = reHomeLinux.ReplaceAllString(s, "~/")
	s = reWindowsHome.ReplaceAllString(s, `~\`)
	return strings.TrimSpace(s)
}
