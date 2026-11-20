package mesh

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/tamld/g8s/internal/pathutil"

	_ "modernc.org/sqlite"
)

const schemaSQL = `CREATE TABLE IF NOT EXISTS nodes(node_id TEXT PRIMARY KEY, incarnation TEXT NOT NULL, capabilities TEXT NOT NULL, observed_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS quotas(node_id TEXT PRIMARY KEY, incarnation TEXT NOT NULL, observation TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS messages(seq INTEGER PRIMARY KEY AUTOINCREMENT, message_id TEXT UNIQUE NOT NULL, sender TEXT NOT NULL, sender_inc TEXT NOT NULL, recipient TEXT NOT NULL, recipient_inc TEXT NOT NULL, kind TEXT NOT NULL, payload TEXT NOT NULL, digest TEXT NOT NULL, acked INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS message_tombstones(message_id TEXT PRIMARY KEY, digest TEXT NOT NULL, seq INTEGER NOT NULL, pruned_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS transport_policy(node_id TEXT NOT NULL, incarnation TEXT NOT NULL, config TEXT NOT NULL, PRIMARY KEY(node_id, incarnation));
CREATE TABLE IF NOT EXISTS transport_surface(node_id TEXT NOT NULL, incarnation TEXT NOT NULL, state TEXT NOT NULL, PRIMARY KEY(node_id, incarnation));
CREATE TABLE IF NOT EXISTS transport_retry(sender TEXT NOT NULL, sender_inc TEXT NOT NULL, recipient TEXT NOT NULL, recipient_inc TEXT NOT NULL, failures INTEGER NOT NULL, degraded_until INTEGER NOT NULL, PRIMARY KEY(sender,sender_inc,recipient,recipient_inc));
CREATE TABLE IF NOT EXISTS claims(task_id TEXT PRIMARY KEY, node_id TEXT NOT NULL, incarnation TEXT NOT NULL, resource TEXT NOT NULL, paths TEXT NOT NULL, context_revision INTEGER NOT NULL, expires_at INTEGER NOT NULL, state TEXT NOT NULL, artifact_ref TEXT NOT NULL DEFAULT '', artifact_sha256 TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS meta(id INTEGER PRIMARY KEY CHECK(id=1), revision INTEGER NOT NULL);
INSERT OR IGNORE INTO meta(id, revision) VALUES(1, 0);`

// meshLedgerSchemaVersion is the current mesh ledger schema version. It starts
// at 1 = the schema above (C-5, #984). Bump it when schemaSQL changes and add
// an entry to ledgerColumnMigrations (for columns) or rely on the idempotent
// CREATE TABLE IF NOT EXISTS statements (for whole tables) so pre-existing
// ledger.db files migrate on reopen instead of bricking every INSERT.
const meshLedgerSchemaVersion = 1

// meshLedgerBusyTimeoutMs is the mesh ledger busy_timeout. 15s sits between
// the 5s used by non-mesh side ledgers (cleanup/charter) and the 30s used by
// the controlplane/receipt/telemetry stores: mesh READ paths perform writes
// (keepalive inserts inside every inbox poll, messages.go msgGetPending) and
// msgWait polls every 100ms, so transient write-write contention is the norm.
// 5s was observed to surface SQLITE_BUSY under concurrent poll + prune/check-
// point spikes (C-6, #984); 30s would stall interactive CLI callers before
// they can report contention. 15s absorbs WAL checkpoint + prune sweeps
// without turning a contended poll into a multi-second CLI hang.
const meshLedgerBusyTimeoutMs = 15000

// ledgerColumnMigration declares one defensively-added column: when an
// existing ledger.db predates the column, PRAGMA table_info misses it and the
// migrator adds it with ALTER TABLE ADD COLUMN. def MUST carry a DEFAULT so
// the ALTER succeeds on populated tables (mirrors the controlplane pattern,
// internal/controlplane/store.go migrateTasksTable).
type ledgerColumnMigration struct {
	table  string
	column string
	def    string
}

// ledgerColumnMigrations lists columns added after meshLedgerSchemaVersion 1.
// The v1 schema above already contains every column the handlers reference,
// so the registry starts empty; append one entry per future column bump.
var ledgerColumnMigrations = []ledgerColumnMigration{}

// migrateLedger brings an opened ledger.db up to meshLedgerSchemaVersion:
// fresh databases (user_version 0) get the base schema, pre-versioning
// databases get any missing columns added, and both are stamped with the
// current version. Defensive column migrations re-run even on a
// current-version database so a hand-mangled or partially-written ledger
// heals on reopen. Newer-than-code databases are rejected (no downgrade).
func migrateLedger(db *sql.DB, colMigrations []ledgerColumnMigration) error {
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read mesh ledger schema version: %w", err)
	}
	if version > meshLedgerSchemaVersion {
		return fmt.Errorf("unsupported mesh ledger schema version %d; expected <= %d (database written by a newer binary)", version, meshLedgerSchemaVersion)
	}
	if version == meshLedgerSchemaVersion && len(colMigrations) == 0 {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin schema migration: %w", err)
	}
	defer tx.Rollback()

	if version < meshLedgerSchemaVersion {
		if _, err := tx.Exec(schemaSQL); err != nil {
			return fmt.Errorf("init schema: %w", err)
		}
	}
	for _, m := range colMigrations {
		rows, err := tx.Query("PRAGMA table_info(" + m.table + ")")
		if err != nil {
			return fmt.Errorf("inspect %s columns: %w", m.table, err)
		}
		present := false
		for rows.Next() {
			var cid, notNull, pk int
			var name, ctype string
			var dflt any
			if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
				rows.Close()
				return fmt.Errorf("scan %s columns: %w", m.table, err)
			}
			if name == m.column {
				present = true
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("inspect %s columns: %w", m.table, err)
		}
		rows.Close()
		if present {
			continue
		}
		if _, err := tx.Exec("ALTER TABLE " + m.table + " ADD COLUMN " + m.column + " " + m.def); err != nil {
			return fmt.Errorf("migrate %s.%s: %w", m.table, m.column, err)
		}
	}

	if version < meshLedgerSchemaVersion {
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", meshLedgerSchemaVersion)); err != nil {
			return fmt.Errorf("record mesh ledger schema version: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit schema migration: %w", err)
	}
	return nil
}

type Store struct {
	db  *sql.DB
	now func() time.Time
}

var handlers = map[string]func(context.Context, *Store, json.RawMessage) (any, error){}

func Open(dir string) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("directory path must not be empty")
	}
	if err := pathutil.EnsureStateDir(dir); err != nil {
		return nil, fmt.Errorf("create dir: %w", err)
	}
	dbPath := filepath.Join(dir, "ledger.db")

	dsn := fmt.Sprintf(
		"file:%s?_txlock=immediate&_pragma=busy_timeout(%d)&_pragma=foreign_keys(ON)&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)",
		sqlitePathEscape(dbPath), meshLedgerBusyTimeoutMs,
	)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}
	db.SetMaxOpenConns(1)

	if err := migrateLedger(db, ledgerColumnMigrations); err != nil {
		db.Close()
		return nil, err
	}
	if err := pathutil.HardenSQLiteFiles(dbPath); err != nil {
		db.Close()
		return nil, fmt.Errorf("harden mesh ledger: %w", err)
	}
	return &Store{db: db, now: time.Now}, nil
}

func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Operations returns the registered mesh operation names in stable sorted order.
func Operations() []string {
	ops := make([]string, 0, len(handlers))
	for op := range handlers {
		ops = append(ops, op)
	}
	slices.Sort(ops)
	return ops
}

// ValidOperation reports whether op is a registered mesh operation.
func ValidOperation(op string) bool {
	_, ok := handlers[op]
	return ok
}

func (s *Store) Execute(ctx context.Context, op string, input json.RawMessage) (json.RawMessage, error) {
	if len(input) == 0 || len(input) > 16384 {
		return nil, errors.New("input must be non-empty and at most 16384 bytes")
	}
	dec := json.NewDecoder(bytes.NewReader(input))
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		return nil, fmt.Errorf("invalid json object: %w", err)
	}
	if obj == nil {
		return nil, errors.New("input must be a json object, got null")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, errors.New("trailing bytes after json object")
	}
	h, ok := handlers[op]
	if !ok {
		return nil, fmt.Errorf("unknown operation: %q", op)
	}
	res, err := h(ctx, s, input)
	if err != nil {
		return nil, err
	}
	return encode(res)
}

func decode(raw json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return errors.New("trailing bytes after json value")
	}
	return nil
}

func encode(v any) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(b), nil
}

func validID(s string) bool {
	if len(s) < 1 || len(s) > 128 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		isValid := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '.' || c == '-'
		if !isValid {
			return false
		}
	}
	return true
}

func (s *Store) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func sqlitePathEscapeFor(goos, path string) string {
	if goos != "windows" {
		return url.PathEscape(path)
	}
	p := strings.ReplaceAll(path, "%", "%25")
	p = strings.ReplaceAll(p, "?", "%3F")
	p = strings.ReplaceAll(p, "#", "%23")
	return p
}

func sqlitePathEscape(path string) string {
	return sqlitePathEscapeFor(runtime.GOOS, path)
}
