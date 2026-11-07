package mesh

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"time"
)

func isProbe(kind string) bool { return kind == "ping" || kind == "pong" }

func touchPeerTx(ctx context.Context, tx *sql.Tx, node, inc string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `UPDATE nodes SET observed_at=? WHERE node_id=? AND incarnation=?`, now.UTC().Format(time.RFC3339), node, inc); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE messages SET acked=1 WHERE recipient=? AND recipient_inc=? AND kind='ping' AND acked=0`, node, inc)
	return err
}

// Polling is the shared-ledger transport pump; no process-local timer state.
func keepaliveTx(ctx context.Context, tx *sql.Tx, node, inc string, now time.Time) error {
	rows, err := tx.QueryContext(ctx, `SELECT node_id,incarnation,observed_at FROM nodes WHERE node_id<>?`, node)
	if err != nil {
		return err
	}
	var peers []Node
	for rows.Next() {
		var n Node
		if err := rows.Scan(&n.NodeID, &n.Incarnation, &n.ObservedAt); err != nil {
			rows.Close()
			return err
		}
		peers = append(peers, n)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, n := range peers {
		var total int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM messages").Scan(&total); err != nil {
			return err
		}
		if total >= 10000 {
			return nil
		}
		p, err := peerPolicyTx(ctx, tx, n.NodeID, n.Incarnation)
		if err != nil {
			return err
		}
		observed, err := time.Parse(time.RFC3339, n.ObservedAt)
		if err != nil {
			return err
		}
		if now.Sub(observed) < time.Duration(p.KeepaliveSeconds)*time.Second {
			continue
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM messages WHERE sender=? AND sender_inc=? AND recipient=? AND recipient_inc=? AND kind='ping' AND acked=0`, node, inc, n.NodeID, n.Incarnation).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			continue
		}
		id := fmt.Sprintf("ping-%x", sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%s|%d", node, inc, n.NodeID, n.Incarnation, now.UnixNano()))))
		digest := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%s|ping|{}", node, inc, n.NodeID, n.Incarnation))))
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO messages(message_id,sender,sender_inc,recipient,recipient_inc,kind,payload,digest,created_at) VALUES(?,?,?,?,?,'ping','{}',?,?)`, id, node, inc, n.NodeID, n.Incarnation, digest, now.UTC().Format(time.RFC3339)); err != nil {
			return err
		}
	}
	return nil
}

func peerStaleTx(ctx context.Context, tx *sql.Tx, n Node, now time.Time) (bool, error) {
	p, err := peerPolicyTx(ctx, tx, n.NodeID, n.Incarnation)
	if err != nil {
		return false, err
	}
	var oldest sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT min(m.created_at) FROM messages m JOIN nodes s ON s.node_id=m.sender AND s.incarnation=m.sender_inc WHERE m.recipient=? AND m.recipient_inc=? AND m.kind='ping' AND m.acked=0`, n.NodeID, n.Incarnation).Scan(&oldest)
	if err != nil || !oldest.Valid {
		return false, err
	}
	at, err := time.Parse(time.RFC3339, oldest.String)
	if err != nil {
		return false, err
	}
	return now.Sub(at) >= time.Duration(p.KeepaliveSeconds*p.KeepaliveThreshold)*time.Second, nil
}
