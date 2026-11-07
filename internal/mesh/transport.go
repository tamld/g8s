package mesh

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const (
	DefaultFlowWindow         = 1000
	DefaultRetryBudget        = 3
	DefaultCoolOffSeconds     = 30
	DefaultKeepaliveSeconds   = 30
	DefaultKeepaliveThreshold = 3
)

// TransportConfig is a recipient's per-sender transport policy.
type TransportConfig struct {
	WindowSize         int `json:"window_size"`
	RetryBudget        int `json:"retry_budget"`
	CoolOffSeconds     int `json:"cool_off_seconds"`
	KeepaliveSeconds   int `json:"keepalive_seconds"`
	KeepaliveThreshold int `json:"keepalive_threshold"`
}

func (p *TransportConfig) validate() error {
	if p.KeepaliveSeconds == 0 {
		p.KeepaliveSeconds = DefaultKeepaliveSeconds
	}
	if p.KeepaliveThreshold == 0 {
		p.KeepaliveThreshold = DefaultKeepaliveThreshold
	}
	if p.KeepaliveSeconds < 1 || p.KeepaliveSeconds > 3600 || p.KeepaliveThreshold < 1 || p.KeepaliveThreshold > 100 {
		return errors.New("invalid keepalive interval or threshold")
	}
	if p.WindowSize == 0 {
		p.WindowSize = DefaultFlowWindow
	}
	if p.WindowSize < 1 || p.WindowSize > 1000 {
		return errors.New("window_size must be 1..1000")
	}
	if p.RetryBudget == 0 {
		p.RetryBudget = DefaultRetryBudget
	}
	if p.CoolOffSeconds == 0 {
		p.CoolOffSeconds = DefaultCoolOffSeconds
	}
	if p.RetryBudget < 1 || p.RetryBudget > 100 {
		return errors.New("retry_budget must be 1..100")
	}
	if p.CoolOffSeconds < 1 || p.CoolOffSeconds > 3600 {
		return errors.New("cool_off_seconds must be 1..3600")
	}
	return nil
}

// FlowWindowError refuses admission without enqueueing a message.
type FlowWindowError struct {
	Peer   string
	Window int
}

func (e *FlowWindowError) Error() string {
	return fmt.Sprintf("FLOW_WINDOW_FULL: peer %s max %d pending messages reached (per-peer window)", e.Peer, e.Window)
}

func peerPolicyTx(ctx context.Context, tx *sql.Tx, node, inc string) (TransportConfig, error) {
	p := TransportConfig{WindowSize: DefaultFlowWindow}
	var raw string
	err := tx.QueryRowContext(ctx, "SELECT config FROM transport_policy WHERE node_id=? AND incarnation=?", node, inc).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return p, p.validate()
	}
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return p, err
	}
	return p, p.validate()
}

// PeerDegradedError reports a stream suspended until RetryAt.
type PeerDegradedError struct {
	Peer    string
	RetryAt time.Time
}

func (e *PeerDegradedError) Error() string {
	return fmt.Sprintf("PEER_DEGRADED: peer %s retry at %s", e.Peer, e.RetryAt.UTC().Format(time.RFC3339Nano))
}

func retryGateTx(ctx context.Context, tx *sql.Tx, r msgSendReq, now time.Time) error {
	var until int64
	err := tx.QueryRowContext(ctx, `SELECT degraded_until FROM transport_retry WHERE sender=? AND sender_inc=? AND recipient=? AND recipient_inc=?`, r.NodeID, r.Incarnation, r.ToNode, r.ToIncarnation).Scan(&until)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if until > now.UnixNano() {
		return &PeerDegradedError{Peer: r.ToNode, RetryAt: time.Unix(0, until)}
	}
	if until != 0 {
		return ackProgressTx(ctx, tx, r.NodeID, r.Incarnation, r.ToNode, r.ToIncarnation)
	}
	return nil
}

// A refusal is committed as state, then surfaced outside the transaction.
func refuseSendTx(ctx context.Context, tx *sql.Tx, r msgSendReq, p TransportConfig, now time.Time, cause error) (refusal error, txErr error) {
	var attempts int
	err := tx.QueryRowContext(ctx, `SELECT failures FROM transport_retry WHERE sender=? AND sender_inc=? AND recipient=? AND recipient_inc=?`, r.NodeID, r.Incarnation, r.ToNode, r.ToIncarnation).Scan(&attempts)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	attempts++
	var until int64
	if attempts >= p.RetryBudget {
		until = now.Add(time.Duration(p.CoolOffSeconds) * time.Second).UnixNano()
		cause = &PeerDegradedError{Peer: r.ToNode, RetryAt: time.Unix(0, until)}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO transport_retry(sender,sender_inc,recipient,recipient_inc,failures,degraded_until) VALUES(?,?,?,?,?,?) ON CONFLICT(sender,sender_inc,recipient,recipient_inc) DO UPDATE SET failures=excluded.failures,degraded_until=excluded.degraded_until`, r.NodeID, r.Incarnation, r.ToNode, r.ToIncarnation, attempts, until)
	return cause, err
}

func ackProgressTx(ctx context.Context, tx *sql.Tx, sender, senderInc, recipient, recipientInc string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM transport_retry WHERE sender=? AND sender_inc=? AND recipient=? AND recipient_inc=?`, sender, senderInc, recipient, recipientInc)
	return err
}
