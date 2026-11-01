package reach

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tamld/g8s/internal/pathutil"
)

// CooldownDefault is the health-ledger demotion window after a fallback outcome.
const CooldownDefault = 300 * time.Second

type healthRecord struct {
	Code          string `json:"code"`
	At            string `json:"at"`
	Fingerprint   string `json:"fingerprint,omitempty"`
	CooldownUntil string `json:"cooldown_until,omitempty"`
}

type healthSnapshot struct {
	Records map[string]healthRecord `json:"records"`
}

// HealthLedger persists backend health evidence and reorders declared backends.
type HealthLedger struct {
	mu      sync.Mutex
	path    string
	records map[string]healthRecord
}

// NewHealthLedger returns a zero-value-usable ledger bound to the default snapshot path.
func NewHealthLedger() *HealthLedger {
	return &HealthLedger{
		path:    HealthLedgerPath(""),
		records: make(map[string]healthRecord),
	}
}

// HealthLedgerPath resolves the JSON snapshot path for reach backend health.
func HealthLedgerPath(stateDir string) string {
	if override := strings.TrimSpace(os.Getenv("G8S_REACH_HEALTH_PATH")); override != "" {
		return override
	}
	if strings.TrimSpace(stateDir) == "" {
		stateDir = pathutil.DefaultStateDir()
	}
	return filepath.Join(stateDir, "reach_backend_health.json")
}

// Load reads the snapshot; a missing file leaves an empty ledger.
func (h *HealthLedger) Load() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	data, err := os.ReadFile(h.path) // #nosec G304 -- operator state path
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			h.records = make(map[string]healthRecord)
			return nil
		}
		return fmt.Errorf("reach: load health ledger %s: %w", h.path, err)
	}
	var snap healthSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("reach: parse health ledger %s: %w", h.path, err)
	}
	if snap.Records == nil {
		snap.Records = make(map[string]healthRecord)
	}
	h.records = snap.Records
	return nil
}

// Save atomically replaces the snapshot on disk.
func (h *HealthLedger) Save() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.saveLocked()
}

func (h *HealthLedger) saveLocked() error {
	snap := healthSnapshot{Records: h.records}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("reach: marshal health ledger: %w", err)
	}
	return writeFileAtomic(h.path, data)
}

func recordKey(channel, action, backend string) string {
	return channel + "::" + action + "::" + backend
}

func (h *HealthLedger) inCooldownLocked(key string, now time.Time) bool {
	rec, ok := h.records[key]
	if !ok || rec.CooldownUntil == "" {
		return false
	}
	until, err := time.Parse(time.RFC3339, rec.CooldownUntil)
	if err != nil {
		return false
	}
	return now.Before(until)
}

// Order implements Orderer: healthy backends first, then cooling backends.
func (h *HealthLedger) Order(channel, action string, declared []string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()

	now := time.Now().UTC()
	var healthy, cooling []string
	for _, name := range declared {
		key := recordKey(channel, action, name)
		if h.inCooldownLocked(key, now) {
			cooling = append(cooling, name)
		} else {
			healthy = append(healthy, name)
		}
	}
	out := make([]string, 0, len(declared))
	out = append(out, healthy...)
	out = append(out, cooling...)
	return out
}

// Record implements Recorder: upserts evidence for one backend attempt.
func (h *HealthLedger) Record(channel, action, backend string, rank int, code, fingerprint string) {
	_ = rank
	h.mu.Lock()
	defer h.mu.Unlock()

	at := time.Now().UTC()
	rec := healthRecord{
		Code:        code,
		At:          at.Format(time.RFC3339),
		Fingerprint: fingerprint,
	}
	switch code {
	case "fallback":
		rec.CooldownUntil = at.Add(CooldownDefault).Format(time.RFC3339)
	case "ok":
		rec.CooldownUntil = ""
	case "terminal":
		if prev, ok := h.records[recordKey(channel, action, backend)]; ok {
			rec.CooldownUntil = prev.CooldownUntil
		}
	}
	h.records[recordKey(channel, action, backend)] = rec
}

func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename %s: %w", tmp, err)
	}
	return nil
}
