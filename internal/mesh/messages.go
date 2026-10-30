package mesh

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"time"
)

func init() {
	handlers["register"] = msgRegister
	handlers["quota-publish"] = msgQuotaPublish
	handlers["send"] = msgSend
	handlers["inbox"] = msgInbox
	handlers["ack"] = msgAck
	handlers["snapshot"] = msgSnapshot
	handlers["wait"] = msgWait
}

func actorTx(ctx context.Context, tx *sql.Tx, node, inc string) error {
	if node == "" || inc == "" {
		return errors.New("node and incarnation must not be empty")
	}
	var cur string
	if err := tx.QueryRowContext(ctx, "SELECT incarnation FROM nodes WHERE node_id = ?", node).Scan(&cur); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("node %q not found", node)
		}
		return err
	}
	if cur != inc {
		return fmt.Errorf("incarnation mismatch for node %q: expected %q, got %q", node, cur, inc)
	}
	return nil
}

type (
	msgRegReq struct {
		NodeID       string         `json:"node_id"`
		Incarnation  string         `json:"incarnation"`
		Capabilities map[string]any `json:"capabilities"`
	}
	msgQuotaReq struct {
		NodeID      string           `json:"node_id"`
		Incarnation string           `json:"incarnation"`
		Observation QuotaObservation `json:"observation"`
	}
	msgSendReq struct {
		NodeID        string          `json:"node_id"`
		Incarnation   string          `json:"incarnation"`
		MessageID     string          `json:"message_id"`
		ToNode        string          `json:"to_node"`
		ToIncarnation string          `json:"to_incarnation"`
		Kind          string          `json:"kind"`
		Payload       json.RawMessage `json:"payload"`
	}
	msgInboxReq struct {
		NodeID      string `json:"node_id"`
		Incarnation string `json:"incarnation"`
		Limit       *int   `json:"limit"`
	}
	msgAckReq struct {
		NodeID      string `json:"node_id"`
		Incarnation string `json:"incarnation"`
		MessageID   string `json:"message_id"`
	}
	msgSnapshotReq struct{}
	msgSnapQuota   struct {
		NodeID      string           `json:"node_id"`
		Incarnation string           `json:"incarnation"`
		Observation QuotaObservation `json:"observation"`
	}
	msgSnapResp struct {
		Revision int64          `json:"revision"`
		Nodes    []Node         `json:"nodes"`
		Quotas   []msgSnapQuota `json:"quotas"`
		Claims   []Claim        `json:"claims"`
	}
	msgWaitReq struct {
		NodeID      string `json:"node_id"`
		Incarnation string `json:"incarnation"`
		TimeoutMS   int    `json:"timeout_ms"`
	}
	msgWaitResp struct {
		Messages []Message `json:"messages"`
		TimedOut bool      `json:"timed_out"`
	}
)

func msgRegister(ctx context.Context, s *Store, input json.RawMessage) (any, error) {
	var r msgRegReq
	if err := decode(input, &r); err != nil {
		return nil, err
	}
	if !validID(r.NodeID) || !validID(r.Incarnation) {
		return nil, errors.New("invalid id")
	}
	capB, _ := json.Marshal(r.Capabilities)
	if r.Capabilities == nil {
		capB = []byte("{}")
	}
	obsAt := s.now().UTC().Format(time.RFC3339)
	return map[string]any{}, s.withTx(ctx, func(tx *sql.Tx) error {
		var cur string
		err := tx.QueryRowContext(ctx, "SELECT incarnation FROM nodes WHERE node_id = ?", r.NodeID).Scan(&cur)
		if errors.Is(err, sql.ErrNoRows) {
			var n int
			if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM nodes").Scan(&n); err != nil {
				return err
			}
			if n >= 64 {
				return errors.New("max 64 nodes reached")
			}
			_, err = tx.ExecContext(ctx, "INSERT INTO nodes(node_id, incarnation, capabilities, observed_at) VALUES(?, ?, ?, ?)", r.NodeID, r.Incarnation, string(capB), obsAt)
			return err
		}
		if err != nil || cur == r.Incarnation {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE nodes SET incarnation=?, capabilities=?, observed_at=? WHERE node_id=?", r.Incarnation, string(capB), obsAt, r.NodeID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM quotas WHERE node_id=?", r.NodeID)
		return err
	})
}

func msgQuotaPublish(ctx context.Context, s *Store, input json.RawMessage) (any, error) {
	var r msgQuotaReq
	if err := decode(input, &r); err != nil {
		return nil, err
	}
	if !validID(r.NodeID) || !validID(r.Incarnation) || r.Observation.Source == "" {
		return nil, errors.New("invalid fields")
	}
	if _, err := time.Parse(time.RFC3339, r.Observation.ObservedAt); err != nil {
		return nil, errors.New("invalid observed_at")
	}
	if len(r.Observation.Windows) < 1 || len(r.Observation.Windows) > 16 {
		return nil, errors.New("windows must be 1..16")
	}
	for _, w := range r.Observation.Windows {
		if w.Name == "" || (w.Status != "observed" && w.Status != "unknown") {
			return nil, errors.New("invalid window")
		}
		if w.ResetsAt != nil {
			if _, err := time.Parse(time.RFC3339, *w.ResetsAt); err != nil {
				return nil, errors.New("invalid resets_at")
			}
		}
		if w.Status == "observed" {
			if w.WindowMinutes <= 0 || (w.UsedPercent == nil && w.RemainingPercent == nil) {
				return nil, errors.New("observed window requires minutes>0 and >=1 percent")
			}
			for _, p := range []*float64{w.UsedPercent, w.RemainingPercent} {
				if p != nil && (math.IsNaN(*p) || math.IsInf(*p, 0) || *p < 0 || *p > 100) {
					return nil, errors.New("percent must be 0..100")
				}
			}
			if w.UsedPercent != nil && w.RemainingPercent != nil && math.Abs((*w.UsedPercent+*w.RemainingPercent)-100.0) > 1e-6 {
				return nil, errors.New("percents must sum to 100")
			}
		} else if w.WindowMinutes < 0 || w.UsedPercent != nil || w.RemainingPercent != nil {
			return nil, errors.New("unknown window percents must be nil")
		}
	}
	obsB, err := json.Marshal(r.Observation)
	if err != nil {
		return nil, err
	}
	return map[string]any{}, s.withTx(ctx, func(tx *sql.Tx) error {
		if err := actorTx(ctx, tx, r.NodeID, r.Incarnation); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO quotas(node_id, incarnation, observation) VALUES(?, ?, ?)
			ON CONFLICT(node_id) DO UPDATE SET incarnation=excluded.incarnation, observation=excluded.observation`,
			r.NodeID, r.Incarnation, string(obsB))
		return err
	})
}

func msgSend(ctx context.Context, s *Store, input json.RawMessage) (any, error) {
	var r msgSendReq
	if err := decode(input, &r); err != nil {
		return nil, err
	}
	if !validID(r.NodeID) || !validID(r.Incarnation) || !validID(r.MessageID) || !validID(r.ToNode) || !validID(r.ToIncarnation) || r.Kind == "" {
		return nil, errors.New("invalid fields")
	}
	if len(r.Payload) == 0 || len(r.Payload) > 16384 {
		return nil, errors.New("payload size 1..16KiB")
	}
	var pObj map[string]any
	dec := json.NewDecoder(bytes.NewReader(r.Payload))
	dec.UseNumber()
	if err := dec.Decode(&pObj); err != nil || pObj == nil {
		return nil, errors.New("payload must be json object")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, errors.New("trailing payload bytes")
	}
	canonP, err := json.Marshal(pObj)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%s|%s|%s|%s", r.NodeID, r.Incarnation, r.ToNode, r.ToIncarnation, r.Kind, canonP)
	digest := hex.EncodeToString(h.Sum(nil))

	var seq int64
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		if err := actorTx(ctx, tx, r.NodeID, r.Incarnation); err != nil {
			return err
		}
		var recInc string
		if err := tx.QueryRowContext(ctx, "SELECT incarnation FROM nodes WHERE node_id = ?", r.ToNode).Scan(&recInc); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("recipient %q not found", r.ToNode)
			}
			return err
		}
		if recInc != r.ToIncarnation {
			return fmt.Errorf("recipient incarnation mismatch: expected %q, got %q", recInc, r.ToIncarnation)
		}
		var exSeq int64
		var exDig string
		err := tx.QueryRowContext(ctx, "SELECT seq, digest FROM messages WHERE message_id = ?", r.MessageID).Scan(&exSeq, &exDig)
		if err == nil {
			if exDig == digest {
				seq = exSeq
				return nil
			}
			return errors.New("ID_CONFLICT: message_id exists with different digest")
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var total, pending int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM messages").Scan(&total); err != nil {
			return err
		}
		if total >= 10000 {
			return errors.New("max 10000 total messages reached")
		}
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM messages WHERE recipient = ? AND recipient_inc = ? AND acked = 0", r.ToNode, r.ToIncarnation).Scan(&pending); err != nil {
			return err
		}
		if pending >= 1000 {
			return errors.New("max 1000 pending messages reached")
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO messages(message_id, sender, sender_inc, recipient, recipient_inc, kind, payload, digest, acked, created_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, 0, ?)`, r.MessageID, r.NodeID, r.Incarnation, r.ToNode, r.ToIncarnation, r.Kind, string(canonP), digest, s.now().UTC().Format(time.RFC3339))
		if err != nil {
			return err
		}
		seq, err = res.LastInsertId()
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"seq": seq}, nil
}

func msgGetPending(ctx context.Context, s *Store, node, inc string, limit int) ([]Message, error) {
	var msgs []Message
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if err := actorTx(ctx, tx, node, inc); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT seq, message_id, sender, sender_inc, recipient, recipient_inc, kind, payload, acked, created_at
			FROM messages WHERE recipient = ? AND recipient_inc = ? AND acked = 0 ORDER BY seq ASC LIMIT ?`, node, inc, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var m Message
			var pStr string
			var acked int
			if err := rows.Scan(&m.Seq, &m.MessageID, &m.Sender, &m.SenderInc, &m.Recipient, &m.RecipientInc, &m.Kind, &pStr, &acked, &m.CreatedAt); err != nil {
				return err
			}
			m.Payload, m.Acked = json.RawMessage(pStr), acked != 0
			msgs = append(msgs, m)
		}
		return rows.Err()
	})
	if msgs == nil {
		msgs = []Message{}
	}
	return msgs, err
}

func msgInbox(ctx context.Context, s *Store, input json.RawMessage) (any, error) {
	var r msgInboxReq
	if err := decode(input, &r); err != nil {
		return nil, err
	}
	if !validID(r.NodeID) || !validID(r.Incarnation) {
		return nil, errors.New("invalid id")
	}
	lim := 50
	if r.Limit != nil {
		if *r.Limit < 1 || *r.Limit > 50 {
			return nil, errors.New("limit 1..50")
		}
		lim = *r.Limit
	}
	msgs, err := msgGetPending(ctx, s, r.NodeID, r.Incarnation, lim)
	if err != nil {
		return nil, err
	}
	return map[string]any{"messages": msgs}, nil
}

func msgAck(ctx context.Context, s *Store, input json.RawMessage) (any, error) {
	var r msgAckReq
	if err := decode(input, &r); err != nil {
		return nil, err
	}
	if !validID(r.NodeID) || !validID(r.Incarnation) || !validID(r.MessageID) {
		return nil, errors.New("invalid id")
	}
	return map[string]any{}, s.withTx(ctx, func(tx *sql.Tx) error {
		if err := actorTx(ctx, tx, r.NodeID, r.Incarnation); err != nil {
			return err
		}
		var rec, recInc string
		var acked int
		if err := tx.QueryRowContext(ctx, "SELECT recipient, recipient_inc, acked FROM messages WHERE message_id = ?", r.MessageID).Scan(&rec, &recInc, &acked); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("message %q not found", r.MessageID)
			}
			return err
		}
		if rec != r.NodeID || recInc != r.Incarnation {
			return errors.New("only recipient can ack")
		}
		if acked == 1 {
			return nil
		}
		_, err := tx.ExecContext(ctx, "UPDATE messages SET acked = 1 WHERE message_id = ?", r.MessageID)
		return err
	})
}

func msgSnapshot(ctx context.Context, s *Store, input json.RawMessage) (any, error) {
	var req msgSnapshotReq
	if err := decode(input, &req); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var rev int64
	if err := tx.QueryRowContext(ctx, "SELECT revision FROM meta WHERE id = 1").Scan(&rev); err != nil {
		return nil, err
	}
	nodes := []Node{}
	nR, err := tx.QueryContext(ctx, "SELECT node_id, incarnation, capabilities, observed_at FROM nodes ORDER BY node_id ASC")
	if err != nil {
		return nil, err
	}
	for nR.Next() {
		var n Node
		var c string
		if err := nR.Scan(&n.NodeID, &n.Incarnation, &c, &n.ObservedAt); err != nil {
			nR.Close()
			return nil, err
		}
		_ = json.Unmarshal([]byte(c), &n.Capabilities)
		if n.Capabilities == nil {
			n.Capabilities = map[string]any{}
		}
		nodes = append(nodes, n)
	}
	if err := nR.Err(); err != nil {
		nR.Close()
		return nil, err
	}
	nR.Close()

	quotas := []msgSnapQuota{}
	qR, err := tx.QueryContext(ctx, "SELECT node_id, incarnation, observation FROM quotas ORDER BY node_id ASC")
	if err != nil {
		return nil, err
	}
	for qR.Next() {
		var q msgSnapQuota
		var o string
		if err := qR.Scan(&q.NodeID, &q.Incarnation, &o); err != nil {
			qR.Close()
			return nil, err
		}
		_ = json.Unmarshal([]byte(o), &q.Observation)
		quotas = append(quotas, q)
	}
	if err := qR.Err(); err != nil {
		qR.Close()
		return nil, err
	}
	qR.Close()

	claims := []Claim{}
	cR, err := tx.QueryContext(ctx, "SELECT task_id, node_id, incarnation, resource, paths, context_revision, expires_at, state, artifact_ref, artifact_sha256 FROM claims ORDER BY task_id ASC LIMIT 1000")
	if err != nil {
		return nil, err
	}
	for cR.Next() {
		var c Claim
		var p string
		if err := cR.Scan(&c.TaskID, &c.NodeID, &c.Incarnation, &c.Resource, &p, &c.ContextRevision, &c.ExpiresAt, &c.State, &c.ArtifactRef, &c.ArtifactSHA256); err != nil {
			cR.Close()
			return nil, err
		}
		_ = json.Unmarshal([]byte(p), &c.Paths)
		if c.Paths == nil {
			c.Paths = []string{}
		}
		claims = append(claims, c)
	}
	if err := cR.Err(); err != nil {
		cR.Close()
		return nil, err
	}
	cR.Close()

	return msgSnapResp{Revision: rev, Nodes: nodes, Quotas: quotas, Claims: claims}, nil
}

func msgWait(ctx context.Context, s *Store, input json.RawMessage) (any, error) {
	var r msgWaitReq
	if err := decode(input, &r); err != nil {
		return nil, err
	}
	if !validID(r.NodeID) || !validID(r.Incarnation) {
		return nil, errors.New("invalid id")
	}
	if r.TimeoutMS < 0 || r.TimeoutMS > 60000 {
		return nil, errors.New("timeout_ms 0..60000")
	}
	msgs, err := msgGetPending(ctx, s, r.NodeID, r.Incarnation, 50)
	if err != nil {
		return nil, err
	}
	if len(msgs) > 0 {
		return msgWaitResp{Messages: msgs}, nil
	}
	if r.TimeoutMS == 0 {
		return msgWaitResp{Messages: []Message{}, TimedOut: true}, nil
	}
	timer := time.NewTimer(time.Duration(r.TimeoutMS) * time.Millisecond)
	defer timer.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			return msgWaitResp{Messages: []Message{}, TimedOut: true}, nil
		case <-ticker.C:
			msgs, err := msgGetPending(ctx, s, r.NodeID, r.Incarnation, 50)
			if err != nil {
				return nil, err
			}
			if len(msgs) > 0 {
				return msgWaitResp{Messages: msgs}, nil
			}
		}
	}
}
