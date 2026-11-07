package reflection

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/routing"
	"github.com/tamld/g8s/internal/telemetry"
)

const goldenReceipt = `{
  "receipt_id": "reflection-001",
  "cycle": {"timestamp": "2026-11-07T00:00:00Z", "trigger": "manual"},
  "judge": {"name": "telemetry", "version": "1"},
  "verdicts": [{"artifact_path": "docs/process.md", "dimension": "drift", "verdict": "healthy",
    "evidence_refs": ["file:docs/process.md@abcdef12", "run:gate-123", "event:model-42"]}],
  "add_list": [], "prune_list": [], "prune_ratio": 0, "self_tombstone_after": 3
}`

func goldenDoc(t *testing.T) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(goldenReceipt), &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func verdictDoc(doc map[string]any) map[string]any {
	return doc["verdicts"].([]any)[0].(map[string]any)
}

func TestReceiptSchemaSelfConformance(t *testing.T) {
	onDisk, err := os.ReadFile("process-reflection-receipt.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(onDisk, receiptSchemaJSON) {
		t.Fatal("embedded schema differs from tracked schema")
	}
	schema, err := compiledSchema()
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(goldenDoc(t)); err != nil {
		t.Fatalf("schema rejects golden: %v", err)
	}
	r, err := ValidateProcessReflectionReceipt([]byte(goldenReceipt))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"missing", "empty", "null"} {
		t.Run(mode, func(t *testing.T) {
			doc := goldenDoc(t)
			switch mode {
			case "missing":
				delete(verdictDoc(doc), "evidence_refs")
			case "empty":
				verdictDoc(doc)["evidence_refs"] = []any{}
			case "null":
				verdictDoc(doc)["evidence_refs"] = nil
			}
			if err := schema.Validate(doc); err == nil {
				t.Fatal("schema accepted unevidenced verdict")
			}
			raw, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ValidateProcessReflectionReceipt(raw); !errors.Is(err, ErrUnevidencedVerdict) {
				t.Fatalf("want E_REFLEX_VERDICT_UNEVIDENCED, got %v", err)
			}
		})
	}
}

func TestReceiptHostileFields(t *testing.T) {
	mutations := map[string]func(map[string]any){
		"unknown root":       func(d map[string]any) { d["execute"] = true },
		"unknown cycle":      func(d map[string]any) { d["cycle"].(map[string]any)["execute"] = true },
		"unknown judge":      func(d map[string]any) { d["judge"].(map[string]any)["execute"] = true },
		"unknown verdict":    func(d map[string]any) { verdictDoc(d)["execute"] = true },
		"bad trigger":        func(d map[string]any) { d["cycle"].(map[string]any)["trigger"] = "autopilot" },
		"bad timestamp":      func(d map[string]any) { d["cycle"].(map[string]any)["timestamp"] = "yesterday" },
		"bad dimension":      func(d map[string]any) { verdictDoc(d)["dimension"] = "confidence" },
		"bad verdict":        func(d map[string]any) { verdictDoc(d)["verdict"] = "approved" },
		"null verdicts":      func(d map[string]any) { d["verdicts"] = nil },
		"wrong refs type":    func(d map[string]any) { verdictDoc(d)["evidence_refs"] = "run:123" },
		"empty identity":     func(d map[string]any) { d["receipt_id"] = "  " },
		"ratio mismatch":     func(d map[string]any) { d["prune_ratio"] = 5 },
		"negative ratio":     func(d map[string]any) { d["prune_ratio"] = -1 },
		"negative tombstone": func(d map[string]any) { d["self_tombstone_after"] = -1 },
		"fraction tombstone": func(d map[string]any) { d["self_tombstone_after"] = 1.5 },
		"duplicate proposal": func(d map[string]any) { d["add_list"] = []string{"a.md", "a.md"} },
		"traversal artifact": func(d map[string]any) { verdictDoc(d)["artifact_path"] = "docs/../secret" },
		"absolute prune":     func(d map[string]any) { d["prune_list"] = []string{"/tmp/a"} },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			doc := goldenDoc(t)
			mutate(doc)
			raw, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ValidateProcessReflectionReceipt(raw); err == nil {
				t.Fatal("hostile receipt accepted")
			}
		})
	}
	for key := range goldenDoc(t) {
		t.Run("required "+key, func(t *testing.T) {
			doc := goldenDoc(t)
			delete(doc, key)
			raw, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ValidateProcessReflectionReceipt(raw); err == nil {
				t.Fatalf("accepted missing %s", key)
			}
		})
	}
	for _, raw := range []string{"null", "{} {}", goldenReceipt + " true", "{"} {
		if _, err := ValidateProcessReflectionReceipt([]byte(raw)); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
}

func TestEvidenceRefShapes(t *testing.T) {
	valid := []string{"file:docs/a.md@aBc012", "file:.g8s/ledger.jsonl@0", "file:ledger.jsonl@1234", "run:123", "event:row-1"}
	invalid := []string{"", " ", "run:", "event:", "run:bad id", "https://example.com", "file:/tmp/a@abc", "file:../a@abc", "file:a/../b@1", "file:./a@1", "file:a//b@1", "file:C:/a@123", `file:a\b@1`, "file:a@", "file:a@xyz", "file:a@-1", "file:a@1:2", "file:a@" + strings.Repeat("a", 65)}
	for _, group := range []struct {
		refs  []string
		valid bool
	}{{valid, true}, {invalid, false}} {
		for _, ref := range group.refs {
			t.Run(ref, func(t *testing.T) {
				doc := goldenDoc(t)
				verdictDoc(doc)["evidence_refs"] = []string{ref}
				raw, err := json.Marshal(doc)
				if err != nil {
					t.Fatal(err)
				}
				_, err = ValidateProcessReflectionReceipt(raw)
				if (err == nil) != group.valid {
					t.Fatalf("valid=%v, error=%v", group.valid, err)
				}
			})
		}
	}
}

func TestComputedPruneRatio(t *testing.T) {
	for _, tc := range []struct {
		add, prune int
		ratio      float64
	}{{0, 0, 0}, {0, 2, 2}, {2, 1, 0.5}, {3, 2, 2.0 / 3.0}} {
		r, err := ValidateProcessReflectionReceipt([]byte(goldenReceipt))
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < tc.add; i++ {
			r.AddList = append(r.AddList, fmt.Sprintf("add-%d.md", i))
		}
		for i := 0; i < tc.prune; i++ {
			r.PruneList = append(r.PruneList, fmt.Sprintf("prune-%d.md", i))
		}
		r.PruneRatio = r.ComputedPruneRatio()
		if r.PruneRatio != tc.ratio {
			t.Fatalf("ratio=%g, want %g", r.PruneRatio, tc.ratio)
		}
		if err := r.Validate(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPromptManifestCoverage(t *testing.T) {
	entries, err := os.ReadDir("prompts")
	if err != nil {
		t.Fatal(err)
	}
	manifest := PromptManifest()
	count := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		count++
		name := "prompts/" + entry.Name()
		raw, err := PromptFiles.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		disk, err := os.ReadFile(filepath.FromSlash(name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(raw, disk) {
			t.Fatal("embedded prompt differs from disk")
		}
		sum := sha256.Sum256(raw)
		if got := hex.EncodeToString(sum[:]); manifest[name] != got {
			t.Fatalf("manifest %s=%q, want %s", name, manifest[name], got)
		}
	}
	if count == 0 || len(manifest) != count {
		t.Fatalf("manifest=%d, prompts=%d", len(manifest), count)
	}
	manifest["prompts/judge-v1.md"] = "tampered"
	if PromptManifest()["prompts/judge-v1.md"] == "tampered" {
		t.Fatal("manifest caller mutated pinned hashes")
	}
}

func fixtureJudge(t *testing.T) TelemetryJudge {
	t.Helper()
	root := t.TempDir()
	j := TelemetryJudge{RepoRoot: root, StateDir: filepath.Join(root, "ledgers"), Now: func() time.Time { return time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC) }}
	t.Setenv("G8S_MODEL_SCORECARD_PATH", filepath.Join(j.StateDir, "model_quality_events.jsonl"))
	t.Setenv("G8S_CI_GATE_HISTORY_PATH", filepath.Join(j.StateDir, "gate_ci_run_events.jsonl"))
	return j
}

func TestTelemetryJudgeEvidencedVerdicts(t *testing.T) {
	j := fixtureJudge(t)
	if err := routing.RecordModelOutcome(j.StateDir, routing.ModelOutcomeRecord{Provider: "fixture", ModelRequested: "fixture", Success: true, VerifyChecked: true, VerifyPassed: false, RecordedAt: "2026-09-01T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if err := routing.RecordCIGateRuns(j.StateDir, []routing.CIGateRunRecord{{Workflow: "quality.yml", Name: "test", Conclusion: "success", SHA: "abc123", StartedAt: "2026-11-07T00:00:00Z", RecordedAt: "2026-11-07T00:00:00Z"}}); err != nil {
		t.Fatal(err)
	}
	rows, err := j.Judge(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) < 2 {
		t.Fatalf("want both ledgers, got %d verdicts", len(rows))
	}
	seen := map[string]bool{}
	for _, row := range rows {
		seen[row.Verdict] = true
		if len(row.EvidenceRefs) != 1 {
			t.Fatalf("unevidenced: %+v", row)
		}
		ref := strings.TrimPrefix(row.EvidenceRefs[0], "file:")
		parts := strings.Split(ref, "@")
		offset, err := strconv.Atoi(parts[1])
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(j.RepoRoot, filepath.FromSlash(parts[0])))
		if err != nil {
			t.Fatal(err)
		}
		line := bytes.SplitN(b[offset:], []byte{'\n'}, 2)[0]
		if !json.Valid(line) {
			t.Fatalf("ref does not resolve to a row: %s", ref)
		}
	}
	for _, v := range []string{"stale", "drift", "theater", "healthy"} {
		if !seen[v] {
			t.Errorf("missing %s verdict", v)
		}
	}
	r, err := ValidateProcessReflectionReceipt([]byte(goldenReceipt))
	if err != nil {
		t.Fatal(err)
	}
	r.Verdicts = rows
	if err := r.Validate(); err != nil {
		t.Fatalf("judge output violates receipt contract: %v", err)
	}
}

func TestLedgerOffsetsAndFailureBoundaries(t *testing.T) {
	j := fixtureJudge(t)
	filename := routing.ScorecardPath(j.StateDir)
	if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
		t.Fatal(err)
	}
	const row = `{"provider":"fixture","model_requested":"fixture","success":true,"recorded_at":"2026-11-07T00:00:00Z"}`
	content := "\r\n" + row + "\r\n\n" + row // CRLF, blank lines, and no final newline.
	if err := os.WriteFile(filename, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	rows, err := telemetry.ReadLedger[routing.ModelOutcomeRecord](context.Background(), filename)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Offset != 2 || rows[1].Offset != int64(2+len(row)+3) {
		t.Fatalf("wrong physical offsets: %+v", rows)
	}
	verdicts, err := j.Judge(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(verdicts[len(verdicts)-1].EvidenceRefs[0], fmt.Sprintf("@%d", rows[1].Offset)) {
		t.Fatal("judge lost row offset")
	}
	for _, hostile := range []string{"{", "null", `{}`, strings.Repeat("x", 1024*1024+1)} {
		if err := os.WriteFile(filename, []byte(hostile), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := j.Judge(context.Background()); err == nil {
			t.Fatalf("accepted corrupt ledger (%d bytes)", len(hostile))
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := j.Judge(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error=%v", err)
	}
}

func TestTelemetryJudgeEmptyAndOutsideRoot(t *testing.T) {
	j := fixtureJudge(t)
	rows, err := j.Judge(context.Background())
	if err != nil || len(rows) != 0 {
		t.Fatalf("missing ledgers produced verdicts: %v %v", rows, err)
	}
	t.Setenv("G8S_MODEL_SCORECARD_PATH", filepath.Join(t.TempDir(), "outside.jsonl"))
	if _, err := j.Judge(context.Background()); err == nil {
		t.Fatal("accepted ledger outside repository")
	}
}
