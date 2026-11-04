package reflex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// BatchQuestion is one non-autoregressive Jev question with item payload embedded in instructions.
type BatchQuestion struct {
	ID               string
	Type             string
	BaseInstructions string
	ItemPayload      string
	Criteria         any
}

// BatchResult aggregates batch evaluation telemetry.
type BatchResult struct {
	Answers        map[string]Answer
	CacheHits      int
	RequestsSent   int
	InputTokens    int
	OutputTokens   int
	ModelResolved  string
	IsFallback     bool
	FallbackReason string
}

// FormatBatchQuestionID builds a short ASCII question key.
func FormatBatchQuestionID(index int, suffix string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "q_%04d", index)
	if suffix != "" {
		b.WriteByte('_')
		for _, r := range suffix {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
				b.WriteRune(r)
			} else if unicode.IsSpace(r) || r == '-' {
				b.WriteByte('_')
			}
		}
	}
	return b.String()
}

// EstimateTokens returns a conservative token estimate for budgeting.
func EstimateTokens(s string) int {
	return (len(s) + 3) / 4
}

func truncateForTokenBudget(payload string, maxTokens int) string {
	if EstimateTokens(payload) <= maxTokens {
		return payload
	}
	// Binary search by rune-safe truncation on bytes.
	low, high := 0, len(payload)
	for low < high {
		mid := (low + high + 1) / 2
		candidate := payload[:mid]
		if EstimateTokens(candidate) <= maxTokens {
			low = mid
		} else {
			high = mid - 1
		}
	}
	if low <= 0 {
		return "...[truncated]"
	}
	for low > 0 && !utf8.RuneStart(payload[low]) {
		low--
	}
	if low <= 0 {
		return "...[truncated]"
	}
	return payload[:low] + "\n...[truncated]"
}

// BuildInstruction embeds ItemPayload inside the question instructions (never by state index).
func (q BatchQuestion) BuildInstruction(sharedStateJSON string, maxQuestionTokens int) string {
	sep := "\n\n--- ARTIFACT ---\n"
	overhead := EstimateTokens(sharedStateJSON) + EstimateTokens(q.BaseInstructions+sep+"\n...[truncated]") +
		EstimateTokens(mustJSON(map[string]any{"type": q.Type, "instructions": "", "criteria": q.Criteria}))
	budget := maxQuestionTokens - overhead
	if budget < 64 {
		budget = 64
	}
	payload := truncateForTokenBudget(q.ItemPayload, budget)
	return q.BaseInstructions + sep + payload
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func questionJSONTokens(sharedStateJSON string, q BatchQuestion, maxQuestionTokens int) int {
	instr := q.BuildInstruction(sharedStateJSON, maxQuestionTokens)
	qJSON := mustJSON(map[string]any{
		"type":         q.Type,
		"instructions": instr,
		"criteria":     q.Criteria,
	})
	return EstimateTokens(qJSON)
}

func questionWithinSingleLimit(sharedStateJSON string, q BatchQuestion, maxQuestionTokens int) bool {
	instr := q.BuildInstruction(sharedStateJSON, maxQuestionTokens)
	qJSON := mustJSON(map[string]any{
		"type":         q.Type,
		"instructions": instr,
		"criteria":     q.Criteria,
	})
	return EstimateTokens(sharedStateJSON)+EstimateTokens(qJSON) <= maxQuestionTokens
}

// PackQuestionBatches greedily packs questions into batches under maxReqTokens.
func PackQuestionBatches(sharedState map[string]any, questions []BatchQuestion, maxReqTokens, maxQuestionTokens int) [][]BatchQuestion {
	if len(questions) == 0 {
		return nil
	}
	stateJSON := mustJSON(sharedState)
	stateTokens := EstimateTokens(stateJSON)

	var batches [][]BatchQuestion
	for i := 0; i < len(questions); {
		var batch []BatchQuestion
		questionSum := 0
		for i < len(questions) {
			q := questions[i]
			if !questionWithinSingleLimit(stateJSON, q, maxQuestionTokens) {
				// BuildInstruction truncates until within limit; re-check.
				if !questionWithinSingleLimit(stateJSON, q, maxQuestionTokens) {
					i++
					if len(batch) == 0 {
						batch = append(batch, q)
					}
					break
				}
			}
			qt := questionJSONTokens(stateJSON, q, maxQuestionTokens)
			if len(batch) == 0 {
				batch = append(batch, q)
				questionSum = qt
				i++
				continue
			}
			if stateTokens+questionSum+qt > maxReqTokens {
				break
			}
			batch = append(batch, q)
			questionSum += qt
			i++
		}
		if len(batch) == 0 {
			// Always place at least one question per batch.
			batch = append(batch, questions[i])
			i++
		}
		batches = append(batches, batch)
	}
	return batches
}

// ComputeJevCacheKey derives a content-addressed cache key for one question evaluation.
func ComputeJevCacheKey(model, promptSHA256, questionType, baseInstructions string, criteria any, itemPayload string) string {
	criteriaJSON := mustJSON(criteria)
	parts := []string{
		strings.TrimSpace(model),
		strings.TrimSpace(promptSHA256),
		strings.TrimSpace(questionType),
		strings.TrimSpace(baseInstructions),
		strings.TrimSpace(criteriaJSON),
		strings.TrimSpace(itemPayload),
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])
}

type jevCacheFile struct {
	Entries map[string]jevCacheEntry `json:"entries"`
}

type jevCacheEntry struct {
	Answer   Answer    `json:"answer"`
	StoredAt time.Time `json:"stored_at"`
}

func (g *ReflexGate) cachePathResolved() string {
	if g.cachePath != "" {
		return g.cachePath
	}
	return strings.TrimSpace(os.Getenv("G8S_JEV_CACHE_PATH"))
}

func (g *ReflexGate) loadCache() (jevCacheFile, error) {
	path := g.cachePathResolved()
	if path == "" {
		return jevCacheFile{Entries: map[string]jevCacheEntry{}}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return jevCacheFile{Entries: map[string]jevCacheEntry{}}, nil
		}
		return jevCacheFile{}, err
	}
	var file jevCacheFile
	if err := json.Unmarshal(data, &file); err != nil {
		return jevCacheFile{Entries: map[string]jevCacheEntry{}}, nil
	}
	if file.Entries == nil {
		file.Entries = map[string]jevCacheEntry{}
	}
	return file, nil
}

func (g *ReflexGate) saveCache(file jevCacheFile) error {
	path := g.cachePathResolved()
	if path == "" {
		return nil
	}
	if file.Entries == nil {
		file.Entries = map[string]jevCacheEntry{}
	}
	data, err := json.Marshal(file)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "jev-cache-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		closeErr := tmp.Close()
		_ = os.Remove(tmpName)
		if closeErr != nil {
			return fmt.Errorf("write cache: %w (close: %v)", err, closeErr)
		}
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

func (g *ReflexGate) cacheTTL() time.Duration {
	if g.cacheTTLOverride > 0 {
		return g.cacheTTLOverride
	}
	sec := g.promptsConfig.BatchLimits.CacheTTLSeconds
	if sec <= 0 {
		sec = 86400
	}
	return time.Duration(sec) * time.Second
}

func (g *ReflexGate) lookupCachedAnswer(file jevCacheFile, key string) (Answer, bool) {
	entry, ok := file.Entries[key]
	if !ok {
		return Answer{}, false
	}
	if g.cacheTTL() > 0 && time.Since(entry.StoredAt) > g.cacheTTL() {
		return Answer{}, false
	}
	return entry.Answer, true
}

func (g *ReflexGate) postJevBatch(ctx context.Context, sharedState map[string]any, batch []BatchQuestion) (JevResponse, error) {
	activeKey := g.currentKey()
	if activeKey == "" {
		return JevResponse{}, fmt.Errorf("no api key configured")
	}

	stateJSON := mustJSON(sharedState)
	maxQ := g.promptsConfig.BatchLimits.MaxQuestionTokens
	if maxQ <= 0 {
		maxQ = 28000
	}

	questionsMap := map[string]any{}
	for _, q := range batch {
		entry := map[string]any{
			"type":         q.Type,
			"instructions": q.BuildInstruction(stateJSON, maxQ),
		}
		if q.Criteria != nil {
			entry["criteria"] = q.Criteria
		}
		questionsMap[q.ID] = entry
	}

	payload := map[string]any{
		"model":     g.model,
		"state":     sharedState,
		"questions": questionsMap,
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return JevResponse{}, err
	}

	resp, err := g.postJevHTTPWithRetry(ctx, bodyBytes)
	if err != nil {
		return JevResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return JevResponse{}, fmt.Errorf("HTTP %d from Jev", resp.StatusCode)
	}

	var jevResp JevResponse
	if err := json.NewDecoder(resp.Body).Decode(&jevResp); err != nil {
		return JevResponse{}, err
	}
	return jevResp, nil
}

// BatchEvaluate evaluates questions with greedy batching and content-addressed caching.
func (g *ReflexGate) BatchEvaluate(ctx context.Context, sharedState map[string]any, questions []BatchQuestion) (BatchResult, error) {
	result := BatchResult{
		Answers:       map[string]Answer{},
		ModelResolved: g.model,
	}

	if len(questions) == 0 {
		return result, nil
	}

	maxReq := g.promptsConfig.BatchLimits.MaxRequestTokens
	if maxReq <= 0 {
		maxReq = 50000
	}
	maxQ := g.promptsConfig.BatchLimits.MaxQuestionTokens
	if maxQ <= 0 {
		maxQ = 28000
	}

	cacheFile, cacheErr := g.loadCache()
	if cacheErr != nil {
		cacheFile = jevCacheFile{Entries: map[string]jevCacheEntry{}}
	}

	var pending []BatchQuestion
	for _, q := range questions {
		key := ComputeJevCacheKey(g.model, g.promptSHA256, q.Type, q.BaseInstructions, q.Criteria, q.ItemPayload)
		if ans, ok := g.lookupCachedAnswer(cacheFile, key); ok {
			result.Answers[q.ID] = ans
			result.CacheHits++
			continue
		}
		pending = append(pending, q)
	}

	if len(pending) == 0 {
		return result, nil
	}

	activeKey := g.currentKey()
	if activeKey == "" {
		result.IsFallback = true
		result.FallbackReason = "keyless offline fallback"
		return result, nil
	}

	batches := PackQuestionBatches(sharedState, pending, maxReq, maxQ)
	for _, batch := range batches {
		jevResp, err := g.postJevBatch(ctx, sharedState, batch)
		if err != nil {
			result.IsFallback = true
			result.FallbackReason = err.Error()
			return result, nil
		}
		result.RequestsSent++
		result.InputTokens += jevResp.Usage.InputTokens
		result.OutputTokens += jevResp.Usage.OutputTokens
		if jevResp.Model != "" {
			result.ModelResolved = jevResp.Model
		}
		for _, q := range batch {
			ans, ok := jevResp.Answers[q.ID]
			if !ok {
				result.IsFallback = true
				result.FallbackReason = "Jev response missing answer for " + q.ID
				return result, nil
			}
			result.Answers[q.ID] = ans
			key := ComputeJevCacheKey(g.model, g.promptSHA256, q.Type, q.BaseInstructions, q.Criteria, q.ItemPayload)
			cacheFile.Entries[key] = jevCacheEntry{Answer: ans, StoredAt: time.Now().UTC()}
		}
	}

	if err := g.saveCache(cacheFile); err != nil {
		// Non-fatal: answers still returned.
		if result.FallbackReason == "" {
			result.FallbackReason = "cache persist: " + err.Error()
		}
	}

	return result, nil
}
