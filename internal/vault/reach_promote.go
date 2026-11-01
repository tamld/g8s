package vault

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var ErrEmptyReachReceiptHash = errors.New("reach promote: empty receipt hash")

var reachCredentialPatterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`ghp_[A-Za-z0-9]{36}`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`-----BEGIN (?:RSA |OPENSSH |EC )?PRIVATE KEY-----`),
}

const reachRedacted = "[REDACTED]"

func scrubReachSummary(body string) string {
	out := body
	for _, re := range reachCredentialPatterns {
		out = re.ReplaceAllString(out, reachRedacted)
	}
	return out
}

func reachBodySHA256(summary string) string {
	sum := sha256.Sum256([]byte(summary))
	return hex.EncodeToString(sum[:])
}

// PromoteReachArtifact upserts a reach-sourced distillation into the vault.
// Summary text in Causality.Problem is credential-scrubbed before persist.
// Status is always PROPOSED (never the Store APPLIED default). Record id is
// reach:<sha256(scrubbed summary)> for idempotent re-promotion.
func PromoteReachArtifact(ctx context.Context, v *Vault, rec DistillationRecord) (*DistillationRecord, error) {
	if v == nil {
		return nil, fmt.Errorf("%w: nil vault", ErrInvalidRecord)
	}
	if strings.TrimSpace(rec.ForensicVerification.ReceiptHash) == "" {
		return nil, ErrEmptyReachReceiptHash
	}
	if strings.TrimSpace(rec.Causality.Problem) == "" {
		return nil, fmt.Errorf("%w: missing causality problem", ErrInvalidRecord)
	}

	rec.Causality.Problem = scrubReachSummary(rec.Causality.Problem)
	rec.Causality.TradeOff = scrubReachSummary(rec.Causality.TradeOff)
	rec.Causality.RootCause = scrubReachSummary(rec.Causality.RootCause)
	rec.Title = scrubReachSummary(rec.Title)

	rec.ID = "reach:" + reachBodySHA256(rec.Causality.Problem)
	rec.Status = "PROPOSED"

	return v.Store(ctx, rec)
}
