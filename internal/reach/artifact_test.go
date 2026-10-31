package reach

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func validArtifact(now time.Time) Artifact {
	body := "sample-body"
	return Artifact{
		URL:        "https://example.com/doc",
		Channel:    "web",
		Action:     "read",
		Backend:    "web-https-get",
		BodySHA256: DigestBody(body),
		FetchedAt:  now.UTC().Format(time.RFC3339),
	}
}

func TestValidateReachCitation_RejectsWithoutArtifact(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	base := validArtifact(now)
	cases := []struct {
		name string
		mut  func(*Artifact)
		want error
	}{
		{"blank url", func(a *Artifact) { a.URL = "  " }, ErrReachCitationURL},
		{"blank sha", func(a *Artifact) { a.BodySHA256 = "" }, ErrReachCitationBodySHA256},
		{"blank backend", func(a *Artifact) { a.Backend = "" }, ErrReachCitationBackend},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := base
			tc.mut(&a)
			err := ValidateReachCitation(a, now, MaxCitationAge)
			if !errors.Is(err, tc.want) {
				t.Fatalf("ValidateReachCitation() = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestValidateReachCitation_AcceptsFullArtifact(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	if err := ValidateReachCitation(validArtifact(now), now, MaxCitationAge); err != nil {
		t.Fatalf("expected valid artifact to pass: %v", err)
	}
}

func TestValidateReachCitation_RejectsStaleArtifact(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	a := validArtifact(now)
	a.FetchedAt = now.Add(-MaxCitationAge - time.Hour).UTC().Format(time.RFC3339)
	if err := ValidateReachCitation(a, now, MaxCitationAge); !errors.Is(err, ErrReachCitationStale) {
		t.Fatalf("expected stale error, got %v", err)
	}
}

func TestValidateReachCitation_RejectsMalformedDigest(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	a := validArtifact(now)
	a.BodySHA256 = strings.Repeat("G", 64)
	if err := ValidateReachCitation(a, now, MaxCitationAge); !errors.Is(err, ErrReachCitationBodySHA256) {
		t.Fatalf("expected digest error, got %v", err)
	}
}

func TestParseReachCiteMarker_FindsAndRejectsMalformedJSON(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	good := validArtifact(now)
	goodJSON := `{"url":"https://example.com/doc","channel":"web","action":"read","backend":"web-https-get","body_sha256":"` + good.BodySHA256 + `","fetched_at":"` + good.FetchedAt + `"}`
	text := "preamble reach-cite:" + goodJSON + "\nreach-cite:{not-json}\n"
	artifacts, errs := ParseReachCiteMarker(text)
	if len(artifacts) != 1 {
		t.Fatalf("expected one parsed artifact, got %d", len(artifacts))
	}
	if artifacts[0].URL != good.URL || artifacts[0].BodySHA256 != good.BodySHA256 {
		t.Fatalf("parsed artifact mismatch: %+v", artifacts[0])
	}
	if len(errs) != 1 {
		t.Fatalf("expected one parse error, got %d: %v", len(errs), errs)
	}
}

func TestDigestBodyDeterministic(t *testing.T) {
	body := "reach-test-body"
	first := DigestBody(body)
	second := DigestBody(body)
	if first != second {
		t.Fatal("DigestBody must be deterministic")
	}
	if len(first) != 64 {
		t.Fatalf("digest length = %d", len(first))
	}
}
