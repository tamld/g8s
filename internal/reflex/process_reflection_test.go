package reflex

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func sha256FileHex(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	normalized := bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	sum := sha256.Sum256(normalized)
	return hex.EncodeToString(sum[:]), nil
}

func TestJevPromptsRegistryParityAndModelPinning(t *testing.T) {
	promptsPath := filepath.Join("..", "..", ".g8s", "jev-prompts.yml")
	if _, err := os.Stat(promptsPath); err != nil {
		t.Fatalf("expected %s: %v", promptsPath, err)
	}
	diskSHA, err := sha256FileHex(promptsPath)
	if err != nil {
		t.Fatalf("sha256 prompts file: %v", err)
	}

	gate := NewReflexGate(WithKeys(nil), WithEnvFile(false))
	if gate.Model() != "jev-1.14.0" {
		t.Fatalf("expected default model jev-1.14.0, got %q", gate.Model())
	}
	if gate.PromptSHA256() != diskSHA {
		t.Fatalf("prompt SHA mismatch: gate=%s disk=%s", gate.PromptSHA256(), diskSHA)
	}
	if gate.PromptSHA256() != DefaultPromptSHA256() {
		t.Fatalf("embedded default SHA mismatch")
	}

	// Verify Windows core.autocrlf=true (\r\n) checkout produces the identical canonical SHA-256.
	crlfPrompts := filepath.Join(t.TempDir(), "jev-prompts-crlf.yml")
	rawPrompts, err := os.ReadFile(promptsPath)
	if err != nil {
		t.Fatalf("read prompts: %v", err)
	}
	lfPrompts := bytes.ReplaceAll(rawPrompts, []byte("\r\n"), []byte("\n"))
	if err := os.WriteFile(crlfPrompts, bytes.ReplaceAll(lfPrompts, []byte("\n"), []byte("\r\n")), 0o600); err != nil {
		t.Fatalf("write crlf prompts: %v", err)
	}
	crlfGate := NewReflexGate(WithPromptsFile(crlfPrompts), WithKeys(nil), WithEnvFile(false))
	if crlfGate.PromptSHA256() != DefaultPromptSHA256() {
		t.Fatalf("CRLF prompt SHA drift: got %s, want %s", crlfGate.PromptSHA256(), DefaultPromptSHA256())
	}

	cfg := gate.PromptsConfig()
	if len(cfg.Questions.L3OutputQuality["output_quality"].Criteria) != 6 {
		t.Fatalf("expected 6 rungs for output_quality, got %d", len(cfg.Questions.L3OutputQuality["output_quality"].Criteria))
	}

	origModel := os.Getenv("G8S_JEV_MODEL")
	t.Setenv("G8S_JEV_MODEL", "jev-custom-env")
	envGate := NewReflexGate(WithKeys(nil), WithEnvFile(false))
	if envGate.Model() != "jev-custom-env" {
		t.Fatalf("G8S_JEV_MODEL not honored: %q", envGate.Model())
	}
	if origModel != "" {
		t.Setenv("G8S_JEV_MODEL", origModel)
	} else {
		os.Unsetenv("G8S_JEV_MODEL")
	}

	optGate := NewReflexGate(WithModel("jev-opt-override"), WithKeys(nil), WithEnvFile(false))
	if optGate.Model() != "jev-opt-override" {
		t.Fatalf("WithModel not honored: %q", optGate.Model())
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := JevResponse{
			Model: "jev-1.14.0",
			Answers: map[string]Answer{
				"output_quality":      {Type: "score", Score: 5.0, Confidence: 0.95},
				"output_authenticity": {Type: "noul", Noul: 0.01, Confidence: 0.95},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	liveGate := NewReflexGate(WithEndpoint(ts.URL), WithKeys([]string{"test-key"}), WithEnvFile(false))
	sig, err := liveGate.EmitOutputQualitySignal(context.Background(), OutputQualityRequest{
		TaskID:        "quality-pin",
		OutputExcerpt: "verified stdout",
		ExpectedShape: "docs",
		ExitCode:      0,
		DurationSec:   1,
	})
	if err != nil {
		t.Fatalf("EmitOutputQualitySignal: %v", err)
	}
	if sig.RiskScore != 0.0 {
		t.Fatalf("Score 5.0 should invert to risk 0.0, got %.2f", sig.RiskScore)
	}
	verdict := EvaluateOutputQuality(sig)
	if verdict.Quality != 5.0 {
		t.Fatalf("expected quality 5.0, got %.2f", verdict.Quality)
	}
}

func TestBatchEvaluateItemInQuestionAndCache(t *testing.T) {
	var requestBodies []map[string]any
	var mu sync.Mutex
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		requestBodies = append(requestBodies, body)
		mu.Unlock()

		questions, ok := body["questions"].(map[string]any)
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		answers := map[string]Answer{}
		for id := range questions {
			if strings.Contains(id, "staleness") {
				answers[id] = Answer{Type: "score", Score: 1.0, Confidence: 0.9}
			} else {
				answers[id] = Answer{Type: "noul", Noul: 0.1, Confidence: 0.9}
			}
		}
		resp := JevResponse{
			Model:   "jev-1.14.0",
			Answers: answers,
			Usage:   Usage{InputTokens: 100, OutputTokens: 20},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	gate := NewReflexGate(
		WithEndpoint(ts.URL),
		WithKeys([]string{"test-key"}),
		WithEnvFile(false),
	)

	sharedState := map[string]any{
		"system_policy": gate.PromptsConfig().Context.SystemPolicy,
		"cycle_id":      "batch-test",
	}

	var questions []BatchQuestion
	largePad := strings.Repeat("x", 20000)
	for i := 0; i < 8; i++ {
		payload := "artifact-" + string(rune('A'+i)) + "\n" + largePad
		questions = append(questions,
			BatchQuestion{
				ID:               FormatBatchQuestionID(i, "staleness"),
				Type:             "score",
				BaseInstructions: "Rate staleness",
				ItemPayload:      payload,
				Criteria:         []string{"fresh", "active", "aging", "stale", "dead"},
			},
			BatchQuestion{
				ID:               FormatBatchQuestionID(i, "theater"),
				Type:             "noul",
				BaseInstructions: "Rate theater",
				ItemPayload:      payload,
			},
		)
	}

	ctx := context.Background()
	first, err := gate.BatchEvaluate(ctx, sharedState, questions)
	if err != nil {
		t.Fatalf("BatchEvaluate first: %v", err)
	}
	if first.RequestsSent < 2 {
		t.Fatalf("expected multiple HTTP batches for large payload set, got RequestsSent=%d", first.RequestsSent)
	}
	if len(first.Answers) != len(questions) {
		t.Fatalf("expected %d answers, got %d", len(questions), len(first.Answers))
	}

	mu.Lock()
	bodies := append([]map[string]any(nil), requestBodies...)
	mu.Unlock()
	for _, body := range bodies {
		state, ok := body["state"].(map[string]any)
		if !ok {
			t.Fatalf("missing state: %v", body)
		}
		for k := range state {
			if strings.Contains(k, "artifact") {
				t.Fatalf("state must not contain per-item keys, found %q", k)
			}
		}
		questionsMap, ok := body["questions"].(map[string]any)
		if !ok {
			t.Fatalf("missing questions map")
		}
		for id, raw := range questionsMap {
			qm, ok := raw.(map[string]any)
			if !ok {
				t.Fatalf("question %s not object", id)
			}
			instr, ok := qm["instructions"].(string)
			if !ok || !strings.Contains(instr, "--- ARTIFACT ---") {
				t.Fatalf("question %s instructions must embed artifact payload", id)
			}
			if !strings.Contains(id, "q_") {
				t.Fatalf("expected short ASCII id, got %q", id)
			}
		}
	}

	cachePath := filepath.Join(t.TempDir(), "jev_cache.json")
	cachedGate := NewReflexGate(
		WithEndpoint(ts.URL),
		WithKeys([]string{"test-key"}),
		WithEnvFile(false),
		WithCachePath(cachePath),
	)
	_, err = cachedGate.BatchEvaluate(ctx, sharedState, questions)
	if err != nil {
		t.Fatalf("BatchEvaluate seed cache: %v", err)
	}
	info, err := os.Stat(cachePath)
	if err != nil {
		t.Fatalf("cache file missing: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("cache file mode want 0600, got %o", info.Mode().Perm())
	}

	mu.Lock()
	requestBodies = nil
	mu.Unlock()

	second, err := cachedGate.BatchEvaluate(ctx, sharedState, questions)
	if err != nil {
		t.Fatalf("BatchEvaluate cached: %v", err)
	}
	if second.RequestsSent != 0 {
		t.Fatalf("expected 0 requests on cache hit, got %d", second.RequestsSent)
	}
	if second.CacheHits != len(questions) {
		t.Fatalf("expected %d cache hits, got %d", len(questions), second.CacheHits)
	}
}

func TestProcessReflectionReceiptSchemaAndEvidenceRefGate(t *testing.T) {
	schemaPath := filepath.Join("..", "..", "schemas", "process-reflection-receipt-v1.schema.json")
	diskSHA, err := sha256FileHex(schemaPath)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	if ProcessReflectionSchemaSHA256() != diskSHA {
		t.Fatalf("embedded schema SHA mismatch: embedded=%s disk=%s", ProcessReflectionSchemaSHA256(), diskSHA)
	}

	known := map[string]bool{"gate:ci-green": true}
	valid := ProcessReflectionReceipt{
		SchemaVersion:               "1.0",
		CycleID:                     "cycle-1",
		EvaluatedAt:                 time.Now().UTC().Format(time.RFC3339),
		JevModel:                    "jev-1.14.0",
		JevPromptSHA256:             DefaultPromptSHA256(),
		ConsecutiveEmptyPruneCycles: 0,
		PruneRatio:                  0,
		Verdicts: []ArtifactVerdict{
			{
				ArtifactPath:   "docs/foo.md",
				ArtifactSHA256: strings.Repeat("a", 64),
				Score:          0.5,
				TheaterProb:    0.1,
				Classification: "keep",
				EvidenceRefs:   []string{"gate:ci-green"},
			},
		},
		PruneList: []PruneEntry{},
		AddList:   []AddProposal{},
	}
	raw, err := json.Marshal(valid)
	if err != nil {
		t.Fatalf("marshal valid: %v", err)
	}
	if _, err := ValidateProcessReflectionReceipt(raw, known); err != nil {
		t.Fatalf("valid receipt rejected: %v", err)
	}

	emptyRefs := valid
	emptyRefs.Verdicts = []ArtifactVerdict{{
		ArtifactPath:   "docs/foo.md",
		ArtifactSHA256: strings.Repeat("a", 64),
		Score:          0.5,
		TheaterProb:    0.1,
		Classification: "keep",
		EvidenceRefs:   []string{},
	}}
	rawEmpty, err := json.Marshal(emptyRefs)
	if err != nil {
		t.Fatalf("marshal empty refs: %v", err)
	}
	err = errEmptyRefs(rawEmpty, known)
	if !errors.Is(err, ErrUnverifiedVerdict) || !strings.Contains(err.Error(), CodeReflectionUnverifiedVerdict) {
		t.Fatalf("expected ErrUnverifiedVerdict for empty evidence_refs, got %v", err)
	}

	unknownRef := valid
	unknownRef.Verdicts[0].EvidenceRefs = []string{"gate:nonexistent"}
	rawUnknown, err := json.Marshal(unknownRef)
	if err != nil {
		t.Fatalf("marshal unknown ref: %v", err)
	}
	err = errEmptyRefs(rawUnknown, known)
	if !errors.Is(err, ErrUnverifiedVerdict) {
		t.Fatalf("expected ErrUnverifiedVerdict for unknown ref, got %v", err)
	}

	var extra map[string]any
	if err := json.Unmarshal(raw, &extra); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	extra["unexpected_field"] = true
	rawExtra, err := json.Marshal(extra)
	if err != nil {
		t.Fatalf("marshal extra: %v", err)
	}
	if _, err := ValidateProcessReflectionReceipt(rawExtra, known); err == nil {
		t.Fatalf("expected schema failure for additionalProperties")
	}
}

func errEmptyRefs(raw []byte, known map[string]bool) error {
	_, err := ValidateProcessReflectionReceipt(raw, known)
	return err
}

func TestJudgeProcessArtifactsPromptManifestAudit(t *testing.T) {
	known := map[string]bool{
		"gate:keep":  true,
		"gate:amend": true,
		"gate:prune": true,
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		questions, ok := body["questions"].(map[string]any)
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		answers := map[string]Answer{}
		for id := range questions {
			switch {
			case strings.Contains(id, "_0000_"):
				if strings.Contains(id, "staleness") {
					answers[id] = Answer{Type: "score", Score: 0.5, Confidence: 0.9}
				} else {
					answers[id] = Answer{Type: "noul", Noul: 0.05, Confidence: 0.9}
				}
			case strings.Contains(id, "_0001_"):
				if strings.Contains(id, "staleness") {
					answers[id] = Answer{Type: "score", Score: 2.3, Confidence: 0.9}
				} else {
					answers[id] = Answer{Type: "noul", Noul: 0.2, Confidence: 0.9}
				}
			default:
				if strings.Contains(id, "staleness") {
					answers[id] = Answer{Type: "score", Score: 4.0, Confidence: 0.9}
				} else {
					answers[id] = Answer{Type: "noul", Noul: 0.7, Confidence: 0.9}
				}
			}
		}
		resp := JevResponse{Model: "jev-1.14.0", Answers: answers}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	gate := NewReflexGate(WithEndpoint(ts.URL), WithKeys([]string{"test-key"}), WithEnvFile(false))
	artifacts := []SenseArtifact{
		{ArtifactPath: "docs/keep.md", ArtifactSHA256: strings.Repeat("1", 64), Category: "doc", Excerpt: "fresh", EvidenceRefs: []string{"gate:keep"}},
		{ArtifactPath: "docs/amend.md", ArtifactSHA256: strings.Repeat("2", 64), Category: "doc", Excerpt: "aging", EvidenceRefs: []string{"gate:amend"}},
		{ArtifactPath: "docs/prune.md", ArtifactSHA256: strings.Repeat("3", 64), Category: "doc", Excerpt: "dead", EvidenceRefs: []string{"gate:prune"}},
	}

	receipt, err := gate.JudgeProcessArtifacts(context.Background(), artifacts, JudgeOptions{
		CycleID:           "cycle-judge",
		KnownEvidenceRefs: known,
	})
	if err != nil {
		t.Fatalf("JudgeProcessArtifacts: %v", err)
	}
	if len(receipt.Verdicts) != 3 {
		t.Fatalf("expected 3 verdicts, got %d", len(receipt.Verdicts))
	}
	classByPath := map[string]string{}
	for _, v := range receipt.Verdicts {
		classByPath[v.ArtifactPath] = v.Classification
	}
	if classByPath["docs/keep.md"] != "keep" || classByPath["docs/amend.md"] != "amend" || classByPath["docs/prune.md"] != "prune" {
		t.Fatalf("unexpected classifications: %v", classByPath)
	}
	if len(receipt.PruneList) != 1 {
		t.Fatalf("expected 1 prune_list entry, got %d", len(receipt.PruneList))
	}
	if receipt.ConsecutiveEmptyPruneCycles != 0 {
		t.Fatalf("non-empty prune_list should reset consecutive_empty_prune_cycles, got %d", receipt.ConsecutiveEmptyPruneCycles)
	}

	emptyPruneReceipt, err := gate.JudgeProcessArtifacts(context.Background(), []SenseArtifact{artifacts[0]}, JudgeOptions{
		CycleID:               "cycle-empty",
		PriorEmptyPruneCycles: 2,
		KnownEvidenceRefs:     known,
	})
	if err != nil {
		t.Fatalf("empty prune judge: %v", err)
	}
	if emptyPruneReceipt.ConsecutiveEmptyPruneCycles != 3 {
		t.Fatalf("expected consecutive_empty_prune_cycles increment to 3, got %d", emptyPruneReceipt.ConsecutiveEmptyPruneCycles)
	}

	_, err = gate.JudgeProcessArtifacts(context.Background(), []SenseArtifact{{
		ArtifactPath: "bad.md", ArtifactSHA256: strings.Repeat("9", 64), EvidenceRefs: []string{},
	}}, JudgeOptions{CycleID: "bad", KnownEvidenceRefs: known})
	if !errors.Is(err, ErrUnverifiedVerdict) {
		t.Fatalf("expected unverified artifact rejection, got %v", err)
	}

	promptsPath := filepath.Join("..", "..", ".g8s", "jev-prompts.yml")
	diskSHA, err := sha256FileHex(promptsPath)
	if err != nil {
		t.Fatalf("prompts sha: %v", err)
	}
	manifest := map[string]string{filepath.ToSlash(promptsPath): diskSHA}
	if err := VerifyPromptAuditManifest(promptsPath, manifest); err != nil {
		t.Fatalf("VerifyPromptAuditManifest should pass: %v", err)
	}
	if err := VerifyPromptAuditManifest(promptsPath, map[string]string{}); err == nil {
		t.Fatalf("expected failure when manifest entry missing")
	}
	tampered := map[string]string{filepath.ToSlash(promptsPath): strings.Repeat("f", 64)}
	if err := VerifyPromptAuditManifest(promptsPath, tampered); err == nil {
		t.Fatalf("expected failure when manifest SHA tampered")
	}
}
