package memory

// Memory lifecycle layer (#395, ADR-0023): entry FSM, label schema, promotion
// gate with payload-hash tombstones (G1), naked-payload sensor gate (G3),
// meta-entry rejection (G4), and provenance bulk revocation (C9). Storage and
// retrieval engines (DELTA-11/21) are untouched — this adds states, labels,
// and gates on top.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// MemoryKind classifies the entry (ADR-0023 §2): the kind determines sharing
// speed. v1 freezes exactly four kinds — new ones need an ADR amendment.
type MemoryKind string

const (
	KindFact      MemoryKind = "fact"
	KindJudgment  MemoryKind = "judgment"
	KindDecision  MemoryKind = "decision"
	KindProcedure MemoryKind = "procedure"
)

// MemoryLifecycle is the entry-level FSM state (ADR-0023 §1).
type MemoryLifecycle string

const (
	LifecycleWorking   MemoryLifecycle = "working"
	LifecycleScratch   MemoryLifecycle = "scratch"
	LifecycleActive    MemoryLifecycle = "active"
	LifecycleDistilled MemoryLifecycle = "distilled"
	LifecycleArchived  MemoryLifecycle = "archived"
	LifecycleRevoked   MemoryLifecycle = "revoked"
	LifecycleDoctrine  MemoryLifecycle = "doctrine"
)

// MemoryTrust is the trust label carried by an entry.
type MemoryTrust string

const (
	TrustUnverified       MemoryTrust = "unverified"
	TrustJevChecked       MemoryTrust = "jev_checked"
	TrustOperatorRatified MemoryTrust = "operator_ratified"
)

// MemoryScope bounds where the entry applies.
type MemoryScope string

const (
	ScopeTask    MemoryScope = "task"
	ScopeSession MemoryScope = "session"
	ScopeGlobal  MemoryScope = "global"
)

// MemoryEntry is one lifecycle-tracked memory item (MemoryEntry v1).
type MemoryEntry struct {
	ID          string
	Kind        MemoryKind
	Lifecycle   MemoryLifecycle
	Trust       MemoryTrust
	Salience    int
	LastUsedAt  float64
	Scope       MemoryScope
	SessionID   string
	TaskID      string
	PromotedBy  string
	Payload     string
	PayloadHash string
	CreatedAt   float64
	UpdatedAt   float64
}

// GateSensor scores the NAKED payload (trust labels stripped — G3). The
// production sensor is the Jev/reflex gate; tests inject stubs. Verdict
// contract (allowlist, #395 review): only "clean" passes; "blocked" rejects;
// ANY other non-error verdict is a sensor fault and fails CLOSED — a verdict
// drift must disable promotion loudly, never silently. An error from the
// sensor means sensor-down (DEGRADED): the gate fails OPEN with
// trust=unverified and a broker_failure event — fail-open never grants trust.
type GateSensor func(nakedPayload string) (verdict string, err error)

// GateEventSink receives gate lifecycle events ("broker_failure" on sensor
// down, "gate_rejected" on rejection) so DEGRADED is observable. Never nil
// after the adapter is constructed.
type GateEventSink func(event, detail string)

// Lifecycle gate errors.
var (
	ErrGateRequired      = errors.New("memory: promotion requires the gate (ProposeEntry), not a bare transition")
	ErrTombstoned        = errors.New("memory: payload hash is tombstoned (revoked payload cannot re-enter)")
	ErrMetaEntry         = errors.New("memory: meta-entry rejected (memory-about-memory is forbidden, recursion depth 1)")
	ErrGateBlocked       = errors.New("memory: promotion blocked by gate sensor")
	ErrSensorFault       = errors.New("memory: gate sensor returned an unrecognized verdict (fail-closed)")
	ErrInvalidEntry      = errors.New("memory: entry fails deterministic checks")
	ErrUnknownEntry      = errors.New("memory: unknown entry")
	ErrIllegalTransition = errors.New("memory: illegal FSM transition")
)

// legalTransitions encodes the ADR-0023 trust DAG. Revocation is special:
// bulk revoke sweeps every non-terminal state (RevokeBySession), so `to ==
// revoked` bypasses this map for everything except the absorbing states.
// working→scratch is the capture edge (ADR §1); scratch→active exists only
// through ProposeEntry.
var legalTransitions = map[MemoryLifecycle][]MemoryLifecycle{
	LifecycleWorking:   {LifecycleScratch},
	LifecycleActive:    {LifecycleDistilled, LifecycleArchived, LifecycleRevoked},
	LifecycleArchived:  {LifecycleActive},
	LifecycleDistilled: {LifecycleDoctrine},
}

func legalTransition(from, to MemoryLifecycle) bool {
	if to == LifecycleRevoked && from != LifecycleRevoked && from != LifecycleDoctrine {
		return true // bulk revocation sweeps any non-terminal state
	}
	for _, t := range legalTransitions[from] {
		if t == to {
			return true
		}
	}
	return false
}

const maxMetaScanEntries = 10000

func (a *LocalSQLiteMemoryAdapter) SetGateSensor(sensor GateSensor) {
	a.mu.Lock()
	a.gateSensor = sensor
	a.mu.Unlock()
}

func payloadHash(payload string) string {
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

// validateLabels runs the deterministic pre-gate checks (ADR-0023 §3.1):
// required labels present, enums in range, payload non-empty. (Tri-anchor
// verification is deferred per the v1 scope — see the S3a plan ledger.)
func validateLabels(e *MemoryEntry) error {
	if e == nil || e.ID == "" || e.Payload == "" {
		return fmt.Errorf("%w: id and payload are required", ErrInvalidEntry)
	}
	switch e.Kind {
	case KindFact, KindJudgment, KindDecision, KindProcedure:
	default:
		return fmt.Errorf("%w: unknown kind %q", ErrInvalidEntry, e.Kind)
	}
	switch e.Scope {
	case ScopeTask, ScopeSession, ScopeGlobal:
	default:
		return fmt.Errorf("%w: unknown scope %q", ErrInvalidEntry, e.Scope)
	}
	return nil
}

// metaEntryRef checks G4: a payload that references another entry's ID is
// memory-about-memory — recursion depth is 1 by fiat. The scan is capped
// (v1); G1 blocks exact-payload resurrection only, near-duplicate poison is
// the sensor wall's problem.
func (a *LocalSQLiteMemoryAdapter) metaEntryRef(payload string) (bool, error) {
	rows, err := a.db.Query(`SELECT id FROM memory_entries LIMIT ?`, maxMetaScanEntries)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return false, err
		}
		if id != "" && strings.Contains(payload, id) {
			return true, nil
		}
	}
	return false, rows.Err()
}

// ProposeEntry runs the promotion gate (scratch → active, ADR §3.1). Workers
// never call this — promotion is Brain-side. It promotes an EXISTING
// scratch/working row when one matches by ID or by (payload_hash, session_id)
// — the true FSM edge — and otherwise inserts a fresh active entry. On
// success the row is active with the gate-derived trust label.
func (a *LocalSQLiteMemoryAdapter) ProposeEntry(ctx context.Context, e *MemoryEntry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := validateLabels(e); err != nil {
		return err
	}
	hash := payloadHash(e.Payload)

	// G1: tombstones are keyed by payload hash — a revoked payload cannot
	// re-enter under a fresh ID.
	var tombstoned int
	if err := a.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM memory_tombstones WHERE payload_hash = ?`, hash).Scan(&tombstoned); err != nil {
		return fmt.Errorf("memory: tombstone lookup: %w", err)
	}
	if tombstoned > 0 {
		return ErrTombstoned
	}

	// G4: no meta-entries.
	meta, err := a.metaEntryRef(e.Payload)
	if err != nil {
		return fmt.Errorf("memory: meta-entry scan: %w", err)
	}
	if meta {
		a.sink("gate_rejected", "meta-entry: "+e.ID)
		return ErrMetaEntry
	}

	// G3: the sensor sees the NAKED payload — no trust labels, no provenance.
	// Verdict allowlist: only "clean" grants trust; "blocked" rejects; any
	// other verdict is a sensor fault (fail-closed). Sensor-down errors fail
	// OPEN with trust=unverified and a broker_failure event (DEGRADED).
	trust := TrustUnverified
	if a.gateSensor != nil {
		verdict, serr := a.gateSensor(e.Payload)
		switch {
		case serr != nil:
			a.sink("broker_failure", "sensor down: "+serr.Error())
		case strings.EqualFold(verdict, "blocked"):
			a.sink("gate_rejected", "sensor blocked: "+e.ID)
			return ErrGateBlocked
		case strings.EqualFold(verdict, "clean"):
			trust = TrustJevChecked
		default:
			a.sink("broker_failure", "unrecognized sensor verdict: "+verdict)
			return fmt.Errorf("%w: %q", ErrSensorFault, verdict)
		}
	}

	now := float64(time.Now().UnixNano()) / 1e9
	e.Lifecycle = LifecycleActive
	e.Trust = trust
	e.PayloadHash = hash
	// Resolve the promotion target: the entry's own scratch/working row, else
	// an already-captured scratch row with the same payload in the same
	// session (capture → promote, ADR §4), else a fresh insert.
	var rowID string
	if err := a.db.QueryRowContext(ctx,
		`SELECT id FROM memory_entries WHERE id = ? AND lifecycle IN ('scratch', 'working')`,
		e.ID).Scan(&rowID); errors.Is(err, sql.ErrNoRows) {
		err = a.db.QueryRowContext(ctx,
			`SELECT id FROM memory_entries WHERE payload_hash = ? AND session_id = ? AND lifecycle IN ('scratch', 'working')`,
			hash, e.SessionID).Scan(&rowID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("memory: resolve promotion target: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("memory: resolve promotion target: %w", err)
	}

	if rowID != "" {
		if _, err := a.db.ExecContext(ctx, `
			UPDATE memory_entries
			SET lifecycle = ?, trust = ?, payload_hash = ?, promoted_by = ?, updated_at = ?
			WHERE id = ?`,
			string(LifecycleActive), string(trust), hash, e.PromotedBy, now, rowID); err != nil {
			return fmt.Errorf("memory: promote %s: %w", rowID, err)
		}
		e.ID = rowID
	} else {
		if _, err := a.db.ExecContext(ctx, `
			INSERT INTO memory_entries
				(id, kind, lifecycle, trust, salience, last_used_at, scope,
				 session_id, task_id, promoted_by, payload, payload_hash, created_at, updated_at)
			VALUES (?, ?, ?, ?, 0, 0, ?, ?, ?, ?, ?, ?, ?, ?)`,
			e.ID, string(e.Kind), string(LifecycleActive), string(trust), string(e.Scope),
			e.SessionID, e.TaskID, e.PromotedBy, e.Payload, hash, now, now); err != nil {
			return fmt.Errorf("memory: insert entry: %w", err)
		}
	}
	e.CreatedAt = now
	e.UpdatedAt = now
	return nil
}

// CaptureScratch records a freshly distilled entry in the scratch state
// (capture flow: task output → scratch). It performs no gate checks —
// scratch entries are untrusted by construction.
func (a *LocalSQLiteMemoryAdapter) CaptureScratch(ctx context.Context, e *MemoryEntry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if e == nil || e.ID == "" || e.Payload == "" {
		return fmt.Errorf("%w: id and payload are required", ErrInvalidEntry)
	}
	now := float64(time.Now().UnixNano()) / 1e9
	e.Lifecycle = LifecycleScratch
	e.Trust = TrustUnverified
	e.PayloadHash = payloadHash(e.Payload)
	e.CreatedAt = now
	e.UpdatedAt = now
	_, err := a.db.ExecContext(ctx, `
		INSERT INTO memory_entries
			(id, kind, lifecycle, trust, salience, last_used_at, scope,
			 session_id, task_id, promoted_by, payload, payload_hash, created_at, updated_at)
		VALUES (?, ?, ?, ?, 0, 0, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, string(e.Kind), string(e.Lifecycle), string(e.Trust), string(e.Scope),
		e.SessionID, e.TaskID, e.PromotedBy, e.Payload, e.PayloadHash, e.CreatedAt, e.UpdatedAt)
	if err != nil {
		return fmt.Errorf("memory: capture scratch: %w", err)
	}
	return nil
}

// TransitionEntry applies one FSM transition. Doctrine and revoked are
// absorbing; the trust path never re-enters upward (G5).
func (a *LocalSQLiteMemoryAdapter) TransitionEntry(ctx context.Context, id string, to MemoryLifecycle) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	e, err := a.getEntryLocked(ctx, id)
	if err != nil {
		return err
	}
	if to == LifecycleActive && (e.Lifecycle == LifecycleScratch || e.Lifecycle == LifecycleWorking) {
		return ErrGateRequired
	}
	if !legalTransition(e.Lifecycle, to) {
		return fmt.Errorf("%w: %s -> %s", ErrIllegalTransition, e.Lifecycle, to)
	}
	_, err = a.db.ExecContext(ctx,
		`UPDATE memory_entries SET lifecycle = ?, updated_at = ? WHERE id = ?`,
		string(to), float64(time.Now().UnixNano())/1e9, id)
	if err != nil {
		return fmt.Errorf("memory: transition: %w", err)
	}
	e.Lifecycle = to
	return nil
}

// RevokeBySession bulk-revokes every entry promoted by sessionID in a single
// immediate transaction (a concurrent propose cannot resurrect between scan
// and tombstone — #395 review), writes one content-hash tombstone per swept
// payload (G1: monotone set — un-revoke is operator-only and outside this
// API), and reconciles same-hash active entries from OTHER sessions so a
// late-arriving copy cannot survive its own tombstone. Returns swept count.
func (a *LocalSQLiteMemoryAdapter) RevokeBySession(ctx context.Context, sessionID string) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("memory: revoke tx: %w", err)
	}
	defer tx.Rollback()
	now := float64(time.Now().UnixNano()) / 1e9
	rows, err := tx.Query(
		`SELECT id, payload_hash FROM memory_entries
		 WHERE session_id = ? AND lifecycle NOT IN ('revoked', 'doctrine')`, sessionID)
	if err != nil {
		return 0, fmt.Errorf("memory: revoke scan: %w", err)
	}
	type victim struct{ id, hash string }
	var victims []victim
	for rows.Next() {
		var v victim
		if err := rows.Scan(&v.id, &v.hash); err != nil {
			rows.Close()
			return 0, err
		}
		victims = append(victims, v)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, v := range victims {
		if _, err := tx.Exec(
			`UPDATE memory_entries SET lifecycle = 'revoked', updated_at = ? WHERE id = ?`, now, v.id); err != nil {
			return 0, fmt.Errorf("memory: revoke %s: %w", v.id, err)
		}
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO memory_tombstones (payload_hash, reason, created_at) VALUES (?, ?, ?)`,
			v.hash, "revoked session "+sessionID, now); err != nil {
			return 0, fmt.Errorf("memory: tombstone %s: %w", v.id, err)
		}
	}
	// Reconcile: tombstoned payloads must not stay active anywhere.
	for _, v := range victims {
		if _, err := tx.Exec(
			`UPDATE memory_entries SET lifecycle = 'revoked', updated_at = ? WHERE payload_hash = ? AND lifecycle NOT IN ('revoked', 'doctrine')`,
			now, v.hash); err != nil {
			return 0, fmt.Errorf("memory: reconcile %s: %w", v.hash, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("memory: revoke commit: %w", err)
	}
	return len(victims), nil
}

// RecordReuse bumps salience for OUT-OF-SAMPLE corroboration only (G2's v1
// deterministic subset): the promoting session re-reading its own entry is a
// no-op, an empty reader session is in-sample, and only active entries
// accumulate salience — revoked/archived entries must not heat back up.
func (a *LocalSQLiteMemoryAdapter) RecordReuse(ctx context.Context, id, readerSessionID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	e, err := a.getEntryLocked(ctx, id)
	if err != nil {
		return err
	}
	if readerSessionID == "" || readerSessionID == e.SessionID || e.Lifecycle != LifecycleActive {
		return nil // in-sample or non-active: no-op
	}
	now := float64(time.Now().UnixNano()) / 1e9
	_, err = a.db.ExecContext(ctx,
		`UPDATE memory_entries SET salience = salience + 1, last_used_at = ?, updated_at = ? WHERE id = ?`,
		now, now, id)
	return err
}

// GetEntry fetches one entry by ID.
func (a *LocalSQLiteMemoryAdapter) GetEntry(ctx context.Context, id string) (*MemoryEntry, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.getEntryLocked(ctx, id)
}

func (a *LocalSQLiteMemoryAdapter) getEntryLocked(ctx context.Context, id string) (*MemoryEntry, error) {
	row := a.db.QueryRowContext(ctx, `SELECT `+entryColumns+` FROM memory_entries WHERE id = ?`, id)
	return scanEntry(row)
}

// MemoryFilter narrows ListEntries (CLI `memory list`).
type MemoryFilter struct {
	SessionID string
	Lifecycle MemoryLifecycle
	Kind      MemoryKind
}

// ListEntries returns entries matching the filter, newest first.
func (a *LocalSQLiteMemoryAdapter) ListEntries(ctx context.Context, filter MemoryFilter) ([]*MemoryEntry, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	q := `SELECT ` + entryColumns + ` FROM memory_entries WHERE 1=1`
	var args []any
	if filter.SessionID != "" {
		q += ` AND session_id = ?`
		args = append(args, filter.SessionID)
	}
	if filter.Lifecycle != "" {
		q += ` AND lifecycle = ?`
		args = append(args, string(filter.Lifecycle))
	}
	if filter.Kind != "" {
		q += ` AND kind = ?`
		args = append(args, string(filter.Kind))
	}
	q += ` ORDER BY updated_at DESC LIMIT 200`
	rows, err := a.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("memory: list: %w", err)
	}
	defer rows.Close()
	var out []*MemoryEntry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

const entryColumns = `id, kind, lifecycle, trust, salience, last_used_at, scope, session_id, task_id, promoted_by, payload, payload_hash, created_at, updated_at`

type rowScanner interface{ Scan(dest ...any) error }

func scanEntry(row rowScanner) (*MemoryEntry, error) {
	e := &MemoryEntry{}
	var kind, lifecycle, trust, scope string
	err := row.Scan(&e.ID, &kind, &lifecycle, &trust, &e.Salience, &e.LastUsedAt, &scope,
		&e.SessionID, &e.TaskID, &e.PromotedBy, &e.Payload, &e.PayloadHash, &e.CreatedAt, &e.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUnknownEntry
	}
	if err != nil {
		return nil, fmt.Errorf("memory: scan entry: %w", err)
	}
	e.Kind = MemoryKind(kind)
	e.Lifecycle = MemoryLifecycle(lifecycle)
	e.Trust = MemoryTrust(trust)
	e.Scope = MemoryScope(scope)
	return e, nil
}

// SetGateEventSink wires the DEGRADED observability sink (e.g. telemetry
// broker_failure ingestion). Must be called before any gate evaluation to be
// meaningful; the default sink is a no-op.
func (a *LocalSQLiteMemoryAdapter) SetGateEventSink(sink GateEventSink) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if sink != nil {
		a.sinkFn = sink
	}
}

func (a *LocalSQLiteMemoryAdapter) sink(event, detail string) {
	if a.sinkFn != nil {
		a.sinkFn(event, detail)
	}
}
