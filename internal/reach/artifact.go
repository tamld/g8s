package reach

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const MaxCitationAge = 7 * 24 * time.Hour

var (
	ErrReachCitationURL        = errors.New("reach citation: url missing or not absolute http(s)")
	ErrReachCitationChannel    = errors.New("reach citation: channel blank")
	ErrReachCitationBackend    = errors.New("reach citation: backend blank")
	ErrReachCitationBodySHA256 = errors.New("reach citation: body_sha256 not 64-char lowercase hex")
	ErrReachCitationFetchedAt  = errors.New("reach citation: fetched_at unparsable")
	ErrReachCitationStale      = errors.New("reach citation: fetched_at older than max age")
	ErrReachCitationAction     = errors.New("reach citation: action must be read")
)

// Artifact is machine-fetched provenance bound to a reach-sourced citation marker.
type Artifact struct {
	URL        string `json:"url"`
	Channel    string `json:"channel"`
	Action     string `json:"action"`
	Backend    string `json:"backend"`
	BodySHA256 string `json:"body_sha256"`
	FetchedAt  string `json:"fetched_at"`
}

// DigestBody returns the lowercase hex SHA-256 digest of body (single SSoT for CLI and validation).
func DigestBody(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// ValidateReachCitation enforces the machine-artifact floor for one parsed marker.
func ValidateReachCitation(a Artifact, now time.Time, maxAge time.Duration) error {
	rawURL := strings.TrimSpace(a.URL)
	if rawURL == "" {
		return ErrReachCitationURL
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || strings.TrimSpace(parsed.Host) == "" {
		return ErrReachCitationURL
	}
	if strings.TrimSpace(a.Channel) == "" {
		return ErrReachCitationChannel
	}
	if strings.TrimSpace(a.Backend) == "" {
		return ErrReachCitationBackend
	}
	if !isLowerHex64(a.BodySHA256) {
		return ErrReachCitationBodySHA256
	}
	fetched, err := time.Parse(time.RFC3339, strings.TrimSpace(a.FetchedAt))
	if err != nil {
		return ErrReachCitationFetchedAt
	}
	if now.Sub(fetched) > maxAge {
		return ErrReachCitationStale
	}
	if a.Action != "read" {
		return ErrReachCitationAction
	}
	return nil
}

func isLowerHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if c >= '0' && c <= '9' || c >= 'a' && c <= 'f' {
			continue
		}
		return false
	}
	return true
}

const reachCiteMarker = "reach-cite:"

// ParseReachCiteMarker extracts every reach-cite: JSON object from text.
// Malformed JSON yields an error entry; callers must not treat errors as fatal panics.
func ParseReachCiteMarker(text string) ([]Artifact, []error) {
	var artifacts []Artifact
	var errs []error
	searchFrom := 0
	for {
		rel := strings.Index(text[searchFrom:], reachCiteMarker)
		if rel < 0 {
			break
		}
		pos := searchFrom + rel + len(reachCiteMarker)
		for pos < len(text) && (text[pos] == ' ' || text[pos] == '\t') {
			pos++
		}
		if pos >= len(text) || text[pos] != '{' {
			errs = append(errs, fmt.Errorf("reach-cite: expected JSON object after marker"))
			searchFrom = pos
			continue
		}
		end, ok := jsonObjectEnd(text, pos)
		if !ok {
			errs = append(errs, fmt.Errorf("reach-cite: unclosed JSON object"))
			break
		}
		chunk := text[pos:end]
		var a Artifact
		if err := json.Unmarshal([]byte(chunk), &a); err != nil {
			errs = append(errs, err)
		} else {
			artifacts = append(artifacts, a)
		}
		searchFrom = end
	}
	return artifacts, errs
}

func jsonObjectEnd(text string, start int) (end int, ok bool) {
	if start >= len(text) || text[start] != '{' {
		return start, false
	}
	depth := 0
	inString := false
	escape := false
	for i := start; i < len(text); i++ {
		c := text[i]
		if inString {
			if escape {
				escape = false
				continue
			}
			if c == '\\' {
				escape = true
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i + 1, true
			}
		}
	}
	return len(text), false
}
