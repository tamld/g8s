package reflection

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/tamld/g8s/internal/routing"
	"github.com/tamld/g8s/internal/telemetry"
)

// Judge observes artifacts and returns evidenced verdicts. It never acts on or
// prunes artifacts; receipt assembly and later ACT/PRUNE policy stay with callers.
type Judge interface {
	Judge(context.Context) ([]Verdict, error)
}

// TelemetryJudge reads the S1 model-quality and CI-gate JSONL ledgers. RepoRoot
// must contain both resolved ledger paths so evidence stays repo-relative.
// StaleAfter defaults to 30 days. Now can be supplied for reproducible cycles.
type TelemetryJudge struct {
	RepoRoot   string
	StateDir   string
	StaleAfter time.Duration
	Now        func() time.Time
}

var _ Judge = TelemetryJudge{}

func (j TelemetryJudge) Judge(ctx context.Context) ([]Verdict, error) {
	if j.RepoRoot == "" {
		return nil, fmt.Errorf("reflection judge requires RepoRoot")
	}
	if j.StaleAfter < 0 {
		return nil, fmt.Errorf("StaleAfter must be nonnegative")
	}
	staleAfter := j.StaleAfter
	if staleAfter == 0 {
		staleAfter = 30 * 24 * time.Hour
	}
	now := time.Now()
	if j.Now != nil {
		now = j.Now()
	}
	modelPath, gatePath := routing.ScorecardPath(j.StateDir), routing.CIGateHistoryPath(j.StateDir)
	modelArtifact, err := ledgerArtifact(j.RepoRoot, modelPath)
	if err != nil {
		return nil, err
	}
	gateArtifact, err := ledgerArtifact(j.RepoRoot, gatePath)
	if err != nil {
		return nil, err
	}
	models, err := telemetry.ReadLedger[routing.ModelOutcomeRecord](ctx, modelPath)
	if err != nil {
		return nil, fmt.Errorf("model ledger: %w", err)
	}
	gates, err := telemetry.ReadLedger[routing.CIGateRunRecord](ctx, gatePath)
	if err != nil {
		return nil, fmt.Errorf("gate ledger: %w", err)
	}
	out := []Verdict{}
	appendRows := func(artifact string, offset int64, recorded string, drift, theater, assessTheater bool) error {
		at, err := time.Parse(time.RFC3339Nano, recorded)
		if err != nil {
			return fmt.Errorf("%s@%d recorded_at: %w", artifact, offset, err)
		}
		ref := fmt.Sprintf("file:%s@%d", artifact, offset)
		for _, item := range []struct {
			dimension, bad string
			failed         bool
		}{
			{"staleness", "stale", now.Sub(at) > staleAfter},
			{"drift", "drift", drift},
			{"theater", "theater", theater},
		} {
			if item.dimension == "theater" && !assessTheater {
				continue
			}
			verdict := "healthy"
			if item.failed {
				verdict = item.bad
			}
			out = append(out, Verdict{ArtifactPath: artifact, Dimension: item.dimension, Verdict: verdict, EvidenceRefs: []string{ref}})
		}
		return nil
	}
	for _, row := range models {
		r := row.Record
		if r.Provider == "" || r.ModelRequested == "" {
			return nil, fmt.Errorf("model ledger byte %d missing provider/model", row.Offset)
		}
		if err := appendRows(modelArtifact, row.Offset, r.RecordedAt,
			!r.Success || r.Stagnated || (r.VerifyChecked && !r.VerifyPassed),
			r.Success && r.VerifyChecked && !r.VerifyPassed, r.VerifyChecked); err != nil {
			return nil, err
		}
	}
	for _, row := range gates {
		r := row.Record
		if r.Workflow == "" || r.Name == "" || r.SHA == "" || r.Conclusion == "" {
			return nil, fmt.Errorf("gate ledger byte %d missing identity/outcome", row.Offset)
		}
		if err := appendRows(gateArtifact, row.Offset, r.RecordedAt, r.Conclusion != "success", false, false); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func ledgerArtifact(root, filename string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	filename, err = filepath.Abs(filename)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, filename)
	if err != nil {
		return "", err
	}
	rel = filepath.ToSlash(rel)
	if !ValidArtifactPath(rel) {
		return "", fmt.Errorf("ledger must be repo-relative: %s", rel)
	}
	return rel, nil
}
