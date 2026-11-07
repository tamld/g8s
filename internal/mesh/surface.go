package mesh

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

func peerSurfaceTx(ctx context.Context, tx *sql.Tx, node, inc string) (string, error) {
	var state string
	err := tx.QueryRowContext(ctx, `SELECT state FROM transport_surface WHERE node_id=? AND incarnation=?`, node, inc).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return "ready", nil
	}
	return state, err
}

func snapshotMessagesTx(ctx context.Context, tx *sql.Tx) ([]Message, error) {
	rows, err := tx.QueryContext(ctx, `SELECT m.seq,m.message_id,m.sender,m.sender_inc,m.recipient,m.recipient_inc,m.kind,m.payload,m.acked,m.created_at,COALESCE(s.state,'ready') FROM messages m LEFT JOIN transport_surface s ON s.node_id=m.recipient AND s.incarnation=m.recipient_inc ORDER BY m.seq LIMIT 1000`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	messages := []Message{}
	for rows.Next() {
		var m Message
		var payload, state string
		if err := rows.Scan(&m.Seq, &m.MessageID, &m.Sender, &m.SenderInc, &m.Recipient, &m.RecipientInc, &m.Kind, &payload, &m.Acked, &m.CreatedAt, &state); err != nil {
			return nil, err
		}
		m.Payload = json.RawMessage(payload)
		m.Gated = state != "ready" && !m.Acked && !isProbe(m.Kind)
		m.Surfaced = !m.Gated
		messages = append(messages, m)
	}
	return messages, rows.Err()
}
