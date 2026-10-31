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
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const schemaSQL = `CREATE TABLE IF NOT EXISTS nodes(node_id TEXT PRIMARY KEY, incarnation TEXT NOT NULL, capabilities TEXT NOT NULL, observed_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS quotas(node_id TEXT PRIMARY KEY, incarnation TEXT NOT NULL, observation TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS messages(seq INTEGER PRIMARY KEY AUTOINCREMENT, message_id TEXT UNIQUE NOT NULL, sender TEXT NOT NULL, sender_inc TEXT NOT NULL, recipient TEXT NOT NULL, recipient_inc TEXT NOT NULL, kind TEXT NOT NULL, payload TEXT NOT NULL, digest TEXT NOT NULL, acked INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS claims(task_id TEXT PRIMARY KEY, node_id TEXT NOT NULL, incarnation TEXT NOT NULL, resource TEXT NOT NULL, paths TEXT NOT NULL, context_revision INTEGER NOT NULL, expires_at INTEGER NOT NULL, state TEXT NOT NULL, artifact_ref TEXT NOT NULL DEFAULT '', artifact_sha256 TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS meta(id INTEGER PRIMARY KEY CHECK(id=1), revision INTEGER NOT NULL);
INSERT OR IGNORE INTO meta(id, revision) VALUES(1, 0);`

type Store struct {
	db  *sql.DB
	now func() time.Time
}

var handlers = map[string]func(context.Context, *Store, json.RawMessage) (any, error){}

func Open(dir string) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("directory path must not be empty")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create dir: %w", err)
	}
	dbPath := filepath.Join(dir, "ledger.db")

	dsn := fmt.Sprintf(
		"file:%s?_txlock=immediate&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)",
		sqlitePathEscape(dbPath),
	)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	if err := os.Chmod(dbPath, 0600); err != nil {
		db.Close()
		return nil, fmt.Errorf("chmod ledger db: %w", err)
	}
	return &Store{db: db, now: time.Now}, nil
}

func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
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
