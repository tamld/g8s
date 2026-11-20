package mesh

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrInvalidPruneOlderThan is returned when older_than is not a valid duration of at least 1h.
var ErrInvalidPruneOlderThan = errors.New("invalid prune older_than")

const defaultPruneOlderThan = 168 * time.Hour

type msgPruneReq struct {
	OlderThan string `json:"older_than"`
	DryRun    bool   `json:"dry_run"`
}

type msgPruneResp struct {
	Pruned           int64  `json:"pruned"`
	TombstonesPruned int64  `json:"tombstones_pruned"`
	ClaimsPruned     int64  `json:"claims_pruned"`
	DryRun           bool   `json:"dry_run"`
	Cutoff           string `json:"cutoff"`
}

func parsePruneOlderThan(raw string) (time.Duration, error) {
	if raw == "" {
		return defaultPruneOlderThan, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrInvalidPruneOlderThan, err)
	}
	if d < time.Hour {
		return 0, fmt.Errorf("%w: must be at least 1h", ErrInvalidPruneOlderThan)
	}
	return d, nil
}

// msgPrune deletes ACKed messages whose created_at is before the retention cutoff.
// The messages table has no ack timestamp column; created_at is the retention clock.
//
// C-8 (#984): the same cutoff also ages out the ledger's auxiliary rows that
// previously grew unboundedly — message_tombstones (pruned_at before the
// cutoff) and dead claims (lease expired before the cutoff, any state).
// Claims safety: a claim's lease is live iff expires_at > now; since the
// cutoff epoch is <= now, `expires_at < cutoff` can never match a live lease,
// so active-but-unexpired claims (including accepted tasks whose idempotent
// re-accept is still reachable) always survive the window.
func msgPrune(ctx context.Context, s *Store, input json.RawMessage) (any, error) {
	var r msgPruneReq
	if err := decode(input, &r); err != nil {
		return nil, err
	}
	olderThan, err := parsePruneOlderThan(r.OlderThan)
	if err != nil {
		return nil, err
	}
	cutoff := s.now().UTC().Add(-olderThan)
	cutoffStr := cutoff.Format(time.RFC3339)
	cutoffEpoch := cutoff.Unix()

	var pruned, tombstonesPruned, claimsPruned int64
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM messages WHERE acked = 1 AND created_at < ?`, cutoffStr,
		).Scan(&pruned); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM message_tombstones WHERE pruned_at < ?`, cutoffStr,
		).Scan(&tombstonesPruned); err != nil {
			return err
		}
		// Live leases (expires_at > now >= cutoffEpoch) can never match.
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM claims WHERE expires_at < ?`, cutoffEpoch,
		).Scan(&claimsPruned); err != nil {
			return err
		}
		if r.DryRun {
			return nil
		}
		prunedAt := s.now().UTC().Format(time.RFC3339)
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO message_tombstones(message_id, digest, seq, pruned_at)
			SELECT message_id, digest, seq, ? FROM messages WHERE acked = 1 AND created_at < ?`,
			prunedAt, cutoffStr,
		); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx,
			`DELETE FROM messages WHERE acked = 1 AND created_at < ?`, cutoffStr,
		)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		pruned = n
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM message_tombstones WHERE pruned_at < ?`, cutoffStr,
		); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM claims WHERE expires_at < ?`, cutoffEpoch,
		); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return msgPruneResp{Pruned: pruned, TombstonesPruned: tombstonesPruned, ClaimsPruned: claimsPruned, DryRun: r.DryRun, Cutoff: cutoffStr}, nil
}
