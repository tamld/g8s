// Package routing — continuous model quality evaluation & dynamic quarantine (DELTA-34).
package routing

import (
	"bufio"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/tamld/g8s/internal/pathutil"
)

// ModelHealthStatus classifies a model's rolling operational reliability.
type ModelHealthStatus string

const (
	ModelHealthHealthy     ModelHealthStatus = "HEALTHY"
	ModelHealthDegraded    ModelHealthStatus = "DEGRADED"
	ModelHealthQuarantined ModelHealthStatus = "QUARANTINED"
	ModelHealthUnproven    ModelHealthStatus = "UNPROVEN"
)

// DefaultScorecardWindow is the default rolling sample window per (provider, model).
const DefaultScorecardWindow = 20

// FailureType classifies the root cause of a model execution or verification failure (DELTA-34).
type FailureType string

const (
	FailureNone             FailureType = ""
	FailureRateLimit        FailureType = "rate_limit"
	FailureTransientNetwork FailureType = "transient_network"
	FailureTimeout          FailureType = "timeout"
	FailureAuth             FailureType = "auth"
	FailurePermission       FailureType = "permission"
	FailureStagnation       FailureType = "stagnation"
	FailureTestFailure      FailureType = "test_failure"
	FailureCodeError        FailureType = "code_error"
	FailureUnknown          FailureType = "unknown"
)

// ClassifyFailureDiagnosis deterministically maps an error reason, stagnation flag,
// and verification outcome to a typed FailureType (DELTA-34).
func ClassifyFailureDiagnosis(errReason string, stagnated bool, verifyFailed bool) FailureType {
	if stagnated {
		return FailureStagnation
	}
	lower := strings.ToLower(strings.TrimSpace(errReason))
	switch {
	case strings.Contains(lower, "429") || strings.Contains(lower, "rate limit") || strings.Contains(lower, "rate_limit") || strings.Contains(lower, "too many requests") || strings.Contains(lower, "quota exceeded"):
		return FailureRateLimit
	case strings.Contains(lower, "401") || strings.Contains(lower, "403") || strings.Contains(lower, "unauthorized") || strings.Contains(lower, "invalid api key") || strings.Contains(lower, "authentication"):
		return FailureAuth
	case strings.Contains(lower, "permission") || strings.Contains(lower, "denied") || strings.Contains(lower, "out of scope") || strings.Contains(lower, "blocked_by_"):
		return FailurePermission
	case strings.Contains(lower, "deadline exceeded") || strings.Contains(lower, "timed out") || strings.Contains(lower, "timeout"):
		return FailureTimeout
	case strings.Contains(lower, "502") || strings.Contains(lower, "503") || strings.Contains(lower, "504") || strings.Contains(lower, "bad gateway") || strings.Contains(lower, "connection reset") || strings.Contains(lower, "connection refused") || strings.Contains(lower, "dial tcp") || strings.Contains(lower, "eof"):
		return FailureTransientNetwork
	case strings.Contains(lower, "compile") || strings.Contains(lower, "syntax error") || strings.Contains(lower, "undefined:") || strings.Contains(lower, "build failed"):
		return FailureCodeError
	case verifyFailed || strings.Contains(lower, "verify") || strings.Contains(lower, "test failed") || strings.Contains(lower, "--- fail:"):
		return FailureTestFailure
	case lower != "":
		return FailureUnknown
	default:
		return FailureNone
	}
}

// ModelOutcomeRecord represents one observed execution outcome for a (provider, model) pair.
type ModelOutcomeRecord struct {
	TaskID           string      `json:"task_id,omitempty"`
	Provider         string      `json:"provider"`
	ModelRequested   string      `json:"model_requested"`
	ModelResolved    string      `json:"model_resolved,omitempty"`
	Success          bool        `json:"success"`
	VerifyChecked    bool        `json:"verify_checked,omitempty"`
	VerifyPassed     bool        `json:"verify_passed,omitempty"`
	Stagnated        bool        `json:"stagnated,omitempty"`
	DurationMs       int64       `json:"duration_ms"`
	TotalTokens      int         `json:"total_tokens,omitempty"`
	ErrorReason      string      `json:"error_reason,omitempty"`
	FailureDiagnosis FailureType `json:"failure_diagnosis,omitempty"`
	RecordedAt       string      `json:"recorded_at"`
}

// ModelQualityScore summarizes the rolling quality metrics and routing recommendation for a model.
type ModelQualityScore struct {
	Provider             string              `json:"provider"`
	Model                string              `json:"model"`
	LastResolvedModel    string              `json:"last_resolved_model,omitempty"`
	WindowSamples        int                 `json:"window_samples"`
	SuccessCount         int                 `json:"success_count"`
	SuccessRate          float64             `json:"success_rate"`
	VerifyCheckedCount   int                 `json:"verify_checked_count"`
	VerifyPassRate       float64             `json:"verify_pass_rate"`
	StagnationRate       float64             `json:"stagnation_rate"`
	ConsecutiveFailures  int                 `json:"consecutive_failures"`
	P50LatencyMs         int64               `json:"p50_latency_ms"`
	P95LatencyMs         int64               `json:"p95_latency_ms"`
	CompositeScore       float64             `json:"composite_score"`
	HealthStatus         ModelHealthStatus   `json:"health_status"`
	Recommendation       string              `json:"recommendation"`
	LastError            string              `json:"last_error,omitempty"`
	LastFailureDiagnosis FailureType         `json:"last_failure_diagnosis,omitempty"`
	FailureBreakdown     map[FailureType]int `json:"failure_breakdown,omitempty"`
	LastUpdatedAt        string              `json:"last_updated_at"`
}

// ScorecardPath resolves the JSONL ledger path for model quality observations.
func ScorecardPath(stateDir string) string {
	if override := strings.TrimSpace(os.Getenv("G8S_MODEL_SCORECARD_PATH")); override != "" {
		return override
	}
	if strings.TrimSpace(stateDir) == "" {
		stateDir = pathutil.DefaultStateDir()
	}
	return filepath.Join(stateDir, "model_quality_events.jsonl")
}

var rfc1918IPv4Pattern = regexp.MustCompile(`\b(?:10\.\d{1,3}\.\d{1,3}\.\d{1,3}|192\.168\.\d{1,3}\.\d{1,3}|172\.(?:1[6-9]|2\d|3[01])\.\d{1,3}\.\d{1,3})\b`)

// SanitizeScorecardError redacts RFC1918 private LAN IPv4 addresses from scorecard error strings.
func SanitizeScorecardError(s string) string {
	if s == "" {
		return ""
	}
	return rfc1918IPv4Pattern.ReplaceAllString(s, "<redacted-lan-ip>")
}

func isDuplicateLegacyWorkerRecord(path string, rec ModelOutcomeRecord) bool {
	if rec.TaskID != "" || rec.VerifyChecked || rec.DurationMs != 0 || rec.TotalTokens != 0 {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	var lastLine string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			lastLine = line
		}
	}
	if lastLine == "" {
		return false
	}
	var last ModelOutcomeRecord
	if err := json.Unmarshal([]byte(lastLine), &last); err != nil {
		return false
	}
	return last.TaskID != "" && !last.VerifyChecked &&
		last.Provider == rec.Provider &&
		last.ModelRequested == rec.ModelRequested &&
		last.Success == rec.Success
}

// RecordModelOutcome appends one model execution observation to the rolling quality ledger.
func RecordModelOutcome(stateDir string, rec ModelOutcomeRecord) error {
	rec.Provider = strings.TrimSpace(rec.Provider)
	rec.ModelRequested = strings.TrimSpace(rec.ModelRequested)
	if rec.Provider == "" || rec.ModelRequested == "" {
		return nil
	}
	if rec.RecordedAt == "" {
		rec.RecordedAt = time.Now().UTC().Format(time.RFC3339)
	}
	rec.ErrorReason = SanitizeScorecardError(rec.ErrorReason)
	if len(rec.ErrorReason) > 240 {
		rec.ErrorReason = rec.ErrorReason[:240]
	}
	if rec.FailureDiagnosis == "" && (!rec.Success || (rec.VerifyChecked && !rec.VerifyPassed) || rec.Stagnated) {
		rec.FailureDiagnosis = ClassifyFailureDiagnosis(rec.ErrorReason, rec.Stagnated, rec.VerifyChecked && !rec.VerifyPassed)
	}

	p := ScorecardPath(stateDir)
	if isDuplicateLegacyWorkerRecord(p, rec) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

// ComputeScorecard aggregates the rolling window of observations per (provider, model)
// and computes composite quality scores, health statuses, and routing recommendations.
func ComputeScorecard(stateDir string, providerFilter string, windowSize int) ([]ModelQualityScore, error) {
	if windowSize <= 0 {
		windowSize = DefaultScorecardWindow
	}
	p := ScorecardPath(stateDir)
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return []ModelQualityScore{}, nil
		}
		return nil, err
	}
	defer f.Close()

	type modelKey struct {
		provider string
		model    string
	}
	grouped := make(map[modelKey][]ModelOutcomeRecord)

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec ModelOutcomeRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if providerFilter != "" && !strings.EqualFold(rec.Provider, providerFilter) {
			continue
		}
		k := modelKey{provider: rec.Provider, model: rec.ModelRequested}
		grouped[k] = append(grouped[k], rec)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	scores := make([]ModelQualityScore, 0, len(grouped))
	for k, records := range grouped {
		if len(records) > windowSize {
			records = records[len(records)-windowSize:]
		}
		scores = append(scores, evaluateModelWindow(k.provider, k.model, records))
	}

	sort.SliceStable(scores, func(i, j int) bool {
		if scores[i].CompositeScore != scores[j].CompositeScore {
			return scores[i].CompositeScore > scores[j].CompositeScore
		}
		if scores[i].Provider != scores[j].Provider {
			return scores[i].Provider < scores[j].Provider
		}
		return scores[i].Model < scores[j].Model
	})

	return scores, nil
}

func evaluateModelWindow(providerName, modelName string, records []ModelOutcomeRecord) ModelQualityScore {
	n := len(records)
	if n == 0 {
		return ModelQualityScore{
			Provider:       providerName,
			Model:          modelName,
			HealthStatus:   ModelHealthUnproven,
			Recommendation: "PROBE_REQUIRED",
		}
	}

	successCount := 0
	verifyCheckedCount := 0
	verifyPassCount := 0
	stagnationCount := 0
	var latencies []int64
	lastResolved := ""
	lastErr := ""
	var lastDiagnosis FailureType
	breakdown := make(map[FailureType]int)
	lastUpdated := ""

	for _, r := range records {
		failed := !r.Success || (r.VerifyChecked && !r.VerifyPassed) || r.Stagnated
		if r.Success {
			successCount++
		} else if r.ErrorReason != "" {
			lastErr = SanitizeScorecardError(r.ErrorReason)
		}
		if failed {
			diag := r.FailureDiagnosis
			if diag == "" {
				diag = ClassifyFailureDiagnosis(r.ErrorReason, r.Stagnated, r.VerifyChecked && !r.VerifyPassed)
			}
			if diag != "" {
				breakdown[diag]++
				lastDiagnosis = diag
			}
		}
		if r.VerifyChecked {
			verifyCheckedCount++
			if r.VerifyPassed {
				verifyPassCount++
			}
		}
		if r.Stagnated {
			stagnationCount++
		}
		if r.DurationMs > 0 {
			latencies = append(latencies, r.DurationMs)
		}
		if r.ModelResolved != "" {
			lastResolved = r.ModelResolved
		}
		if r.RecordedAt != "" {
			lastUpdated = r.RecordedAt
		}
	}

	consecutiveFailures := 0
	allConsecutiveRateLimited := true
	for i := len(records) - 1; i >= 0; i-- {
		r := records[i]
		failed := !r.Success || (r.VerifyChecked && !r.VerifyPassed)
		if failed {
			consecutiveFailures++
			diag := r.FailureDiagnosis
			if diag == "" {
				diag = ClassifyFailureDiagnosis(r.ErrorReason, r.Stagnated, r.VerifyChecked && !r.VerifyPassed)
			}
			if diag != FailureRateLimit {
				allConsecutiveRateLimited = false
			}
		} else {
			break
		}
	}

	successRate := float64(successCount) / float64(n)
	verifyPassRate := 1.0
	if verifyCheckedCount > 0 {
		verifyPassRate = float64(verifyPassCount) / float64(verifyCheckedCount)
	}
	stagnationRate := float64(stagnationCount) / float64(n)

	p50 := percentileInt64(latencies, 0.50)
	p95 := percentileInt64(latencies, 0.95)

	// Latency score: <= 3000ms = 1.0, 30000ms+ = 0.2, linear interpolation between
	latencyFactor := 1.0
	if p50 > 3000 {
		latencyFactor = 1.0 - float64(p50-3000)/33750.0
		if latencyFactor < 0.2 {
			latencyFactor = 0.2
		}
	}

	rawComposite := (50.0 * successRate) + (35.0 * verifyPassRate) + (15.0 * latencyFactor) - (20.0 * stagnationRate)
	if consecutiveFailures >= 2 {
		rawComposite -= float64(consecutiveFailures) * 8.0
	}
	composite := math.Round(math.Max(0, math.Min(100, rawComposite))*10) / 10

	status := ModelHealthHealthy
	recommendation := "PREFER"

	if consecutiveFailures >= 3 && allConsecutiveRateLimited && successCount > 0 {
		status = ModelHealthDegraded
		recommendation = "BACKOFF_RATE_LIMIT"
	} else if consecutiveFailures >= 3 || (n >= 2 && composite < 45.0) || (n >= 2 && successRate < 0.40) {
		status = ModelHealthQuarantined
		recommendation = "DEMOTE_OR_FALLBACK"
	} else if composite < 75.0 || consecutiveFailures >= 1 || verifyPassRate < 0.75 {
		status = ModelHealthDegraded
		recommendation = "ALLOW_WITH_VERIFY"
	}

	if len(breakdown) == 0 {
		breakdown = nil
	}

	return ModelQualityScore{
		Provider:             providerName,
		Model:                modelName,
		LastResolvedModel:    lastResolved,
		WindowSamples:        n,
		SuccessCount:         successCount,
		SuccessRate:          math.Round(successRate*1000) / 1000,
		VerifyCheckedCount:   verifyCheckedCount,
		VerifyPassRate:       math.Round(verifyPassRate*1000) / 1000,
		StagnationRate:       math.Round(stagnationRate*1000) / 1000,
		ConsecutiveFailures:  consecutiveFailures,
		P50LatencyMs:         p50,
		P95LatencyMs:         p95,
		CompositeScore:       composite,
		HealthStatus:         status,
		Recommendation:       recommendation,
		LastError:            lastErr,
		LastFailureDiagnosis: lastDiagnosis,
		FailureBreakdown:     breakdown,
		LastUpdatedAt:        lastUpdated,
	}
}

func percentileInt64(vals []int64, q float64) int64 {
	if len(vals) == 0 {
		return 0
	}
	sorted := make([]int64, len(vals))
	copy(sorted, vals)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := int(math.Ceil(q*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// filterQuarantinedCandidates removes candidates whose rolling scorecard status is QUARANTINED,
// provided at least one non-quarantined candidate remains (fail-safe fallback).
func filterQuarantinedCandidates(candidates []candidateModel) []candidateModel {
	if len(candidates) <= 1 {
		return candidates
	}
	scores, err := ComputeScorecard("", "", DefaultScorecardWindow)
	if err != nil || len(scores) == 0 {
		return candidates
	}
	quarantined := make(map[string]bool)
	for _, s := range scores {
		if s.HealthStatus == ModelHealthQuarantined {
			key := strings.ToLower(s.Provider) + "::" + strings.ToLower(s.Model)
			quarantined[key] = true
		}
	}
	if len(quarantined) == 0 {
		return candidates
	}
	var healthy []candidateModel
	for _, c := range candidates {
		key := strings.ToLower(c.provider) + "::" + strings.ToLower(c.model)
		if !quarantined[key] {
			healthy = append(healthy, c)
		}
	}
	if len(healthy) == 0 {
		return candidates
	}
	return healthy
}
