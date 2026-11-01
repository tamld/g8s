package reach

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func tempHealthLedger(t *testing.T) (*HealthLedger, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "reach_backend_health.json")
	t.Setenv("G8S_REACH_HEALTH_PATH", path)
	ledger := NewHealthLedger()
	return ledger, path
}

func TestHealthLedgerLoadSaveRoundTrip(t *testing.T) {
	ledger, path := tempHealthLedger(t)
	ledger.Record("web", "read", "alpha", 0, "fallback", "fp1")
	if err := ledger.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "web::read::alpha") {
		t.Fatalf("snapshot missing key: %s", string(data))
	}

	ledger2 := NewHealthLedger()
	if err := ledger2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := ledger2.Order("web", "read", []string{"alpha", "beta"})
	if len(got) != 2 || got[0] != "beta" || got[1] != "alpha" {
		t.Fatalf("Order after reload = %v", got)
	}
}

func TestHealthLedgerRecorderUpdatesLedger(t *testing.T) {
	ledger, _ := tempHealthLedger(t)

	ledger.Record("web", "read", "b", 0, "fallback", "f1")
	ledger.mu.Lock()
	rec := ledger.records[recordKey("web", "read", "b")]
	ledger.mu.Unlock()
	if rec.Code != "fallback" || rec.CooldownUntil == "" {
		t.Fatalf("fallback record = %+v", rec)
	}
	until, err := time.Parse(time.RFC3339, rec.CooldownUntil)
	if err != nil {
		t.Fatalf("parse cooldown: %v", err)
	}
	at, err := time.Parse(time.RFC3339, rec.At)
	if err != nil {
		t.Fatalf("parse at: %v", err)
	}
	if until.Sub(at) != CooldownDefault {
		t.Fatalf("cooldown delta = %v, want %v", until.Sub(at), CooldownDefault)
	}

	ledger.Record("web", "read", "b", 1, "ok", "f2")
	ledger.mu.Lock()
	rec = ledger.records[recordKey("web", "read", "b")]
	ledger.mu.Unlock()
	if rec.Code != "ok" || rec.CooldownUntil != "" {
		t.Fatalf("ok should clear cooldown: %+v", rec)
	}

	ledger.Record("web", "read", "c", 0, "fallback", "f3")
	ledger.Record("web", "read", "c", 0, "terminal", "f4")
	ledger.mu.Lock()
	rec = ledger.records[recordKey("web", "read", "c")]
	ledger.mu.Unlock()
	if rec.Code != "terminal" {
		t.Fatalf("code = %q", rec.Code)
	}
	until2 := rec.CooldownUntil
	if until2 == "" {
		t.Fatal("terminal should preserve existing cooldown from prior fallback")
	}
	ledger.Record("web", "read", "d", 0, "terminal", "f5")
	ledger.mu.Lock()
	rec = ledger.records[recordKey("web", "read", "d")]
	ledger.mu.Unlock()
	if rec.CooldownUntil != "" {
		t.Fatalf("terminal on fresh key must not set cooldown: %+v", rec)
	}
}

func TestHealthLedgerDemoteOnFallbackCooldown(t *testing.T) {
	ledger, _ := tempHealthLedger(t)
	ledger.Record("web", "read", "first", 0, "fallback", "fp")
	got := ledger.Order("web", "read", []string{"first", "second", "third"})
	want := []string{"second", "third", "first"}
	if len(got) != len(want) {
		t.Fatalf("Order = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Order = %v, want %v", got, want)
		}
	}
}

func TestHealthLedgerCooldownExpiresRestoresOrder(t *testing.T) {
	ledger, path := tempHealthLedger(t)
	past := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	snap := healthSnapshot{
		Records: map[string]healthRecord{
			recordKey("web", "read", "slow"): {
				Code:          "fallback",
				At:            past,
				Fingerprint:   "abc",
				CooldownUntil: past,
			},
		},
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := ledger.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := ledger.Order("web", "read", []string{"slow", "fast"})
	if len(got) != 2 || got[0] != "slow" || got[1] != "fast" {
		t.Fatalf("Order = %v, want [slow fast]", got)
	}
}

func TestHealthLedgerOrdererStableAmongHealthy(t *testing.T) {
	ledger, _ := tempHealthLedger(t)
	declared := []string{"a", "b", "c", "d"}
	for i := 0; i < 5; i++ {
		got := ledger.Order("web", "read", declared)
		if len(got) != len(declared) {
			t.Fatalf("len = %d", len(got))
		}
		for j := range declared {
			if got[j] != declared[j] {
				t.Fatalf("iteration %d: Order = %v", i, got)
			}
		}
	}
}

func TestHealthLedgerLoadMissingFileEmpty(t *testing.T) {
	ledger, _ := tempHealthLedger(t)
	if err := ledger.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(ledger.records) != 0 {
		t.Fatalf("records = %v", ledger.records)
	}
}
