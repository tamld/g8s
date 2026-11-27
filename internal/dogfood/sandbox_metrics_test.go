package dogfood

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// TestLoadSandboxEnforcement exercises the shared tasks-table loader used by
// tools/dogfood_provenance and cmd/g8s scorecard (#971 WP2).
func TestLoadSandboxEnforcement(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	schema := `
CREATE TABLE tasks (
	id TEXT PRIMARY KEY,
	request_json TEXT,
	result_json TEXT,
	created_at REAL
);`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	rows := []struct{ perm, result string }{
		{"workspace_write", `{"ok":true,"containment_mode":"kernel_enforced"}`},
		{"workspace_write", `{"ok":true,"containment_mode":"policy_only","sandbox_degraded":true}`},
		{"workspace_write", `{"ok":false,"status":"sandbox_policy_rejected","rejected_before_spawn":true}`},
		{"workspace_write", `{"ok":false,"status":"sandbox_policy_rejected","policy_enforcement":{"evaluated_policy":{"rejected_before_spawn":true}}}`},
		{"workspace_write", `{"ok":true,"containment_mode":"policy_only"}`}, // legacy → unclassified
		{"read_only", `{"ok":true,"containment_mode":"kernel_enforced"}`},   // filtered out
		{"workspace_write", ""}, // empty result → unclassified
	}
	for i, r := range rows {
		if _, err := db.Exec(`INSERT INTO tasks (id, request_json, result_json, created_at) VALUES (?, ?, ?, ?)`,
			"t"+string(rune('a'+i)), `{"permission":"`+r.perm+`"}`, r.result, float64(100+i)); err != nil {
			t.Fatalf("seed task %d: %v", i, err)
		}
	}

	summary, err := LoadSandboxEnforcement(context.Background(), db, 0)
	if err != nil {
		t.Fatalf("LoadSandboxEnforcement: %v", err)
	}
	want := SandboxEnforcementSummary{WorkspaceWriteAttempts: 6, KernelEnforced: 1, DegradedAllowed: 1, Refused: 2, Unclassified: 2}
	if summary != want {
		t.Fatalf("summary = %+v, want %+v", summary, want)
	}

	// Time filter excludes everything.
	filtered, err := LoadSandboxEnforcement(context.Background(), db, 10_000)
	if err != nil {
		t.Fatalf("filtered load: %v", err)
	}
	if filtered.WorkspaceWriteAttempts != 0 {
		t.Fatalf("time filter not applied: %+v", filtered)
	}
}
