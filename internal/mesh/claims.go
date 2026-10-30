package mesh

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
)

func init() {
	handlers["claim"] = claimHandler
	handlers["release"] = claimReleaseHandler
	handlers["accept"] = claimAcceptHandler
}

type claimRequest struct {
	NodeID          string   `json:"node_id"`
	Incarnation     string   `json:"incarnation"`
	TaskID          string   `json:"task_id"`
	Resource        string   `json:"resource"`
	Paths           []string `json:"paths"`
	ContextRevision int64    `json:"context_revision"`
	LeaseSeconds    int      `json:"lease_seconds,omitempty"`
}

type claimResponse struct {
	Claim Claim `json:"claim"`
}

type claimReleaseRequest struct {
	NodeID      string `json:"node_id"`
	Incarnation string `json:"incarnation"`
	TaskID      string `json:"task_id"`
}

type claimAcceptRequest struct {
	NodeID          string `json:"node_id"`
	Incarnation     string `json:"incarnation"`
	TaskID          string `json:"task_id"`
	ContextRevision int64  `json:"context_revision"`
	ArtifactRef     string `json:"artifact_ref"`
	ArtifactSHA256  string `json:"artifact_sha256"`
}

type claimAcceptResponse struct {
	Revision int64 `json:"revision"`
}

func claimNormalizePaths(paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, errors.New("paths empty")
	}
	cleaned := make([]string, 0, len(paths))
	for _, p := range paths {
		if p == "" || p == "." || strings.ContainsRune(p, 0) || strings.Contains(p, "\\") || strings.ContainsAny(p, "*?[]") || strings.HasPrefix(p, "/") {
			return nil, errors.New("invalid path format")
		}
		for _, seg := range strings.Split(p, "/") {
			if seg == ".." {
				return nil, errors.New("path traversal")
			}
		}
		c := path.Clean(p)
		if c == "." || c == ".." || strings.HasPrefix(c, "../") || strings.HasPrefix(c, "/") {
			return nil, errors.New("invalid clean path")
		}
		cleaned = append(cleaned, c)
	}
	sort.Strings(cleaned)
	dedup := make([]string, 0, len(cleaned))
	for _, cp := range cleaned {
		if len(dedup) == 0 || dedup[len(dedup)-1] != cp {
			dedup = append(dedup, cp)
		}
	}
	return dedup, nil
}

func claimPathsOverlap(p1, p2 string) bool {
	return p1 == p2 || strings.HasPrefix(p1, p2+"/") || strings.HasPrefix(p2, p1+"/")
}

func claimPathsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func claimHandler(ctx context.Context, s *Store, input json.RawMessage) (any, error) {
	var req claimRequest
	if err := decode(input, &req); err != nil {
		return nil, err
	}
	if req.NodeID == "" || req.Incarnation == "" || req.TaskID == "" || req.Resource == "" {
		return nil, errors.New("missing required field")
	}
	if req.LeaseSeconds == 0 {
		req.LeaseSeconds = 60
	}
	if req.LeaseSeconds < 1 || req.LeaseSeconds > 300 {
		return nil, errors.New("lease_seconds must be 1..300")
	}
	normPaths, err := claimNormalizePaths(req.Paths)
	if err != nil {
		return nil, err
	}

	var out Claim
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		if err := actorTx(ctx, tx, req.NodeID, req.Incarnation); err != nil {
			return err
		}
		var metaRev int64
		if err := tx.QueryRowContext(ctx, "SELECT revision FROM meta WHERE id = 1").Scan(&metaRev); err != nil {
			return err
		}
		if req.ContextRevision != metaRev {
			return fmt.Errorf("stale revision: %d != %d", req.ContextRevision, metaRev)
		}

		nowSec := s.now().Unix()
		var exNode, exInc, exRes, exPJSON, exState, exRef, exSHA string
		var exRev, exExp int64
		qErr := tx.QueryRowContext(ctx, "SELECT node_id, incarnation, resource, paths, context_revision, expires_at, state, artifact_ref, artifact_sha256 FROM claims WHERE task_id = ?", req.TaskID).
			Scan(&exNode, &exInc, &exRes, &exPJSON, &exRev, &exExp, &exState, &exRef, &exSHA)
		if qErr == nil {
			if exState == "accepted" {
				return errors.New("accepted task cannot be reclaimed")
			}
			if exState != "active" || exExp <= nowSec {
				return errors.New("expired/inactive claim cannot transfer; need new task_id")
			}
			var exPaths []string
			if err := json.Unmarshal([]byte(exPJSON), &exPaths); err != nil {
				return err
			}
			if exNode == req.NodeID && exInc == req.Incarnation && exRes == req.Resource && exRev == req.ContextRevision && claimPathsEqual(exPaths, normPaths) {
				out = Claim{TaskID: req.TaskID, NodeID: exNode, Incarnation: exInc, Resource: exRes, Paths: exPaths, ContextRevision: exRev, ExpiresAt: exExp, State: exState, ArtifactRef: exRef, ArtifactSHA256: exSHA}
				return nil
			}
			return errors.New("claim conflict: changed request on active task")
		} else if !errors.Is(qErr, sql.ErrNoRows) {
			return qErr
		}

		rows, err := tx.QueryContext(ctx, "SELECT task_id, paths FROM claims WHERE resource = ? AND state = 'active' AND expires_at > ?", req.Resource, nowSec)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var oID, oJSON string
			if err := rows.Scan(&oID, &oJSON); err != nil {
				return err
			}
			var oPaths []string
			if err := json.Unmarshal([]byte(oJSON), &oPaths); err != nil {
				return err
			}
			for _, p1 := range normPaths {
				for _, p2 := range oPaths {
					if claimPathsOverlap(p1, p2) {
						return fmt.Errorf("claim conflict: path %s overlaps with task %s", p1, oID)
					}
				}
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		rows.Close()

		expAt := nowSec + int64(req.LeaseSeconds)
		rawPaths, _ := json.Marshal(normPaths)
		if _, err := tx.ExecContext(ctx, "INSERT INTO claims(task_id, node_id, incarnation, resource, paths, context_revision, expires_at, state) VALUES(?, ?, ?, ?, ?, ?, ?, 'active')",
			req.TaskID, req.NodeID, req.Incarnation, req.Resource, string(rawPaths), req.ContextRevision, expAt); err != nil {
			return err
		}
		out = Claim{TaskID: req.TaskID, NodeID: req.NodeID, Incarnation: req.Incarnation, Resource: req.Resource, Paths: normPaths, ContextRevision: req.ContextRevision, ExpiresAt: expAt, State: "active"}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claimResponse{Claim: out}, nil
}

func claimReleaseHandler(ctx context.Context, s *Store, input json.RawMessage) (any, error) {
	var req claimReleaseRequest
	if err := decode(input, &req); err != nil {
		return nil, err
	}
	if req.TaskID == "" || req.NodeID == "" || req.Incarnation == "" {
		return nil, errors.New("missing required field")
	}
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if err := actorTx(ctx, tx, req.NodeID, req.Incarnation); err != nil {
			return err
		}
		var node, inc, state string
		var exp int64
		if err := tx.QueryRowContext(ctx, "SELECT node_id, incarnation, expires_at, state FROM claims WHERE task_id = ?", req.TaskID).Scan(&node, &inc, &exp, &state); err != nil {
			return err
		}
		if node != req.NodeID || inc != req.Incarnation || state != "active" || exp <= s.now().Unix() {
			return errors.New("cannot release claim")
		}
		_, err := tx.ExecContext(ctx, "UPDATE claims SET state = 'released' WHERE task_id = ?", req.TaskID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]string{"status": "released"}, nil
}

func claimAcceptHandler(ctx context.Context, s *Store, input json.RawMessage) (any, error) {
	var req claimAcceptRequest
	if err := decode(input, &req); err != nil {
		return nil, err
	}
	if req.TaskID == "" || req.NodeID == "" || req.Incarnation == "" || len(req.ArtifactRef) == 0 || len(req.ArtifactRef) > 2048 || len(req.ArtifactSHA256) != 64 {
		return nil, errors.New("invalid accept parameters")
	}
	for i := 0; i < 64; i++ {
		c := req.ArtifactSHA256[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return nil, errors.New("artifact_sha256 must be hex")
		}
	}
	var retRev int64
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if err := actorTx(ctx, tx, req.NodeID, req.Incarnation); err != nil {
			return err
		}
		var node, inc, state, ref, sha string
		var rev, exp int64
		if err := tx.QueryRowContext(ctx, "SELECT node_id, incarnation, context_revision, expires_at, state, artifact_ref, artifact_sha256 FROM claims WHERE task_id = ?", req.TaskID).
			Scan(&node, &inc, &rev, &exp, &state, &ref, &sha); err != nil {
			return err
		}
		if node != req.NodeID || inc != req.Incarnation {
			return errors.New("actor mismatch")
		}
		if state == "accepted" {
			if req.ContextRevision == rev && ref == req.ArtifactRef && strings.EqualFold(sha, req.ArtifactSHA256) {
				retRev = rev + 1
				return nil
			}
			return errors.New("accepted artifact mismatch")
		}
		if state != "active" || exp <= s.now().Unix() {
			return errors.New("claim not active or expired")
		}
		var metaRev int64
		if err := tx.QueryRowContext(ctx, "SELECT revision FROM meta WHERE id = 1").Scan(&metaRev); err != nil {
			return err
		}
		if req.ContextRevision != rev || req.ContextRevision != metaRev {
			return errors.New("context revision mismatch")
		}
		retRev = metaRev + 1
		if _, err := tx.ExecContext(ctx, "UPDATE meta SET revision = ? WHERE id = 1", retRev); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE claims SET state = 'accepted', artifact_ref = ?, artifact_sha256 = ? WHERE task_id = ?", req.ArtifactRef, req.ArtifactSHA256, req.TaskID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return claimAcceptResponse{Revision: retRev}, nil
}
