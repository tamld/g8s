// Package lessons implements schema, append-only ledger, and fail-closed machine checks for retrospective lessons.
package lessons

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tamld/g8s/internal/telemetry"
)

// DefaultRoundBudget is the default cap of lessons permitted per RoundID.
const DefaultRoundBudget = 3

// Check names corresponding to threat model rails.
const (
	CheckCitationResolution = "citation_resolution_T1"
	CheckNoSelfReview       = "no_self_review_T3"
	CheckDedup              = "dedup_T6"
	CheckBudget             = "budget_T6"
	CheckSchemaIntegrity    = "schema_integrity"
)

var (
	ErrRejectedLesson       = errors.New("cannot append rejected lesson to ledger")
	ErrDuplicateCitationSet = errors.New("duplicate lesson: citation set already exists in ledger")
	ErrEmptyCitationSet     = errors.New("cannot append lesson without cited events: no verifiable catch = no lesson")
)

// smuggledMarkerRegex matches recommendation smuggling verbs in observation text.
var smuggledMarkerRegex = regexp.MustCompile(`(?i)\b(recommend\w*|should|propos\w*)\b`)

// LessonStatus represents the lifecycle state of a lesson under governance.
type LessonStatus string

const (
	StatusProposed LessonStatus = "proposed"
	StatusRatified LessonStatus = "ratified"
	StatusRejected LessonStatus = "rejected"
)

// CitedEvent carries evidence of a verifiable catch observed in telemetry.
// The snapshot is carried inline because telemetry DBs are per-state-dir and ephemeral.
type CitedEvent struct {
	EventID   string         `json:"event_id"`
	TaskID    string         `json:"task_id"`
	EventType string         `json:"event_type"`
	Claim     string         `json:"claim"`
	Snapshot  map[string]any `json:"snapshot"`
}

// Observation represents factual, machine-checkable evidence derived from telemetry.
type Observation struct {
	Text        string       `json:"text,omitempty"`
	CitedEvents []CitedEvent `json:"cited_events"`
}

// CheckResult records the outcome of an individual machine check.
type CheckResult struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Message string `json:"message,omitempty"`
}

// Lesson encapsulates a retrospective lesson under the one-way gate model:
// observations are grounded in telemetry catches, while recommendations are LLM opinion.
type Lesson struct {
	ID                         string        `json:"id"`
	RoundID                    string        `json:"round_id"`
	CreatedAt                  time.Time     `json:"created_at"`
	AuthorClass                string        `json:"author_class"`
	ReviewedClasses            []string      `json:"reviewed_classes"`
	Observation                Observation   `json:"observation"`
	Recommendation             string        `json:"recommendation"`
	RecommendationIsLLMOpinion bool          `json:"recommendation_is_llm_opinion"`
	Status                     LessonStatus  `json:"status"`
	Checks                     []CheckResult `json:"checks,omitempty"`
}

// NewProposedLesson constructs a new Lesson with default proposed status and LLM opinion flag.
func NewProposedLesson(
	id string,
	roundID string,
	authorClass string,
	reviewedClasses []string,
	observation Observation,
	recommendation string,
) Lesson {
	if id == "" {
		id = fmt.Sprintf("les-%d", time.Now().UnixNano())
	}
	return Lesson{
		ID:                         id,
		RoundID:                    roundID,
		CreatedAt:                  time.Now().UTC(),
		AuthorClass:                authorClass,
		ReviewedClasses:            reviewedClasses,
		Observation:                observation,
		Recommendation:             recommendation,
		RecommendationIsLLMOpinion: true,
		Status:                     StatusProposed,
	}
}

// CitationSetHash computes a deterministic SHA-256 hash over the sorted unique set of cited event IDs.
func CitationSetHash(events []CitedEvent) string {
	if len(events) == 0 {
		return ""
	}
	idMap := make(map[string]struct{}, len(events))
	for _, ce := range events {
		id := strings.TrimSpace(ce.EventID)
		if id != "" {
			idMap[id] = struct{}{}
		}
	}
	if len(idMap) == 0 {
		return ""
	}
	ids := make([]string, 0, len(idMap))
	for id := range idMap {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	h := sha256.New()
	for _, id := range ids {
		h.Write([]byte(id))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// CitationSetHash returns the citation-set hash for this lesson.
func (l Lesson) CitationSetHash() string {
	return CitationSetHash(l.Observation.CitedEvents)
}

// TelemetryReader provides read access to telemetry events by task ID.
type TelemetryReader interface {
	EventsByTask(ctx context.Context, taskID string) ([]telemetry.TraceEvent, error)
}

// LedgerReader provides read access to existing lessons in the ledger.
type LedgerReader interface {
	Load() ([]Lesson, error)
	List() ([]Lesson, error)
}

// Dependencies contains the external services and parameters needed for machine checks.
type Dependencies struct {
	Context   context.Context
	Telemetry TelemetryReader
	Ledger    LedgerReader
	RoundCap  int
}

// Verdict encapsulates the result of running all machine checks on a lesson.
type Verdict struct {
	Valid  bool          `json:"valid"`
	Status LessonStatus  `json:"status"`
	Checks []CheckResult `json:"checks"`
	Reason string        `json:"reason,omitempty"`
	Lesson Lesson        `json:"lesson"`
}

// Ok returns true if all machine checks passed.
func (v Verdict) Ok() bool {
	return v.Valid
}

// Verify runs all fail-closed machine checks against the given lesson:
// - Check a (T1): Citation resolution against telemetry
// - Check b (T3): No-self-review (author class not in reviewed classes)
// - Check c (T6): Dedup against ledger citation-set hashes
// - Check d (T6): Budget cap per RoundID
// - Check e: Schema integrity (recommendation present, is LLM opinion, observation free of smuggled verbs)
// Any failure marks Status=rejected and records the failing check.
func Verify(l Lesson, deps Dependencies) Verdict {
	ctx := deps.Context
	if ctx == nil {
		ctx = context.Background()
	}

	if l.ID == "" {
		l.ID = fmt.Sprintf("les-%d", time.Now().UnixNano())
	}
	if l.CreatedAt.IsZero() {
		l.CreatedAt = time.Now().UTC()
	}

	var checks []CheckResult
	allPassed := true
	var firstFailure string

	recordCheck := func(name string, passed bool, msg string) {
		checks = append(checks, CheckResult{
			Name:    name,
			Passed:  passed,
			Message: msg,
		})
		if !passed {
			allPassed = false
			if firstFailure == "" {
				firstFailure = msg
			}
		}
	}

	// 1. Citation resolution (T1)
	checkCitationResolution(ctx, l, deps, recordCheck)

	// 2. No-self-review (T3)
	checkNoSelfReview(l, recordCheck)

	// 3. Dedup (T6)
	checkDedup(l, deps, recordCheck)

	// 4. Budget (T6)
	checkBudget(l, deps, recordCheck)

	// 5. Schema integrity
	checkSchemaIntegrity(l, recordCheck)

	v := Verdict{
		Valid:  allPassed,
		Checks: checks,
		Lesson: l,
	}

	if allPassed {
		if l.Status == StatusRatified {
			v.Status = StatusRatified
		} else {
			v.Status = StatusProposed
		}
	} else {
		v.Status = StatusRejected
		v.Reason = firstFailure
	}

	v.Lesson.Status = v.Status
	v.Lesson.Checks = checks
	return v
}

func checkCitationResolution(ctx context.Context, l Lesson, deps Dependencies, record func(string, bool, string)) {
	if len(l.Observation.CitedEvents) == 0 {
		record(CheckCitationResolution, false, "no cited events provided: lessons must cite at least one verifiable catch")
		return
	}

	if deps.Telemetry == nil {
		record(CheckCitationResolution, false, "telemetry reader dependency missing")
		return
	}

	for idx, ce := range l.Observation.CitedEvents {
		eventID := strings.TrimSpace(ce.EventID)
		taskID := strings.TrimSpace(ce.TaskID)
		if eventID == "" || taskID == "" {
			record(CheckCitationResolution, false, fmt.Sprintf("cited event [%d] missing required event_id or task_id", idx))
			return
		}

		events, err := deps.Telemetry.EventsByTask(ctx, taskID)
		if err != nil {
			record(CheckCitationResolution, false, fmt.Sprintf("telemetry lookup failed for task %s: %v", taskID, err))
			return
		}

		var matched *telemetry.TraceEvent
		for i := range events {
			if events[i].ID == eventID {
				matched = &events[i]
				break
			}
		}

		if matched == nil {
			record(CheckCitationResolution, false, fmt.Sprintf("hostile fabrication: cited event %s does not exist in telemetry for task %s", eventID, taskID))
			return
		}

		if ce.EventType != "" && string(matched.EventType) != ce.EventType {
			record(CheckCitationResolution, false, fmt.Sprintf("event type mismatch for event %s: expected %s, found %s", eventID, ce.EventType, matched.EventType))
			return
		}

		if len(ce.Snapshot) > 0 {
			if matched.Payload == nil {
				record(CheckCitationResolution, false, fmt.Sprintf("snapshot mismatch for event %s: telemetry payload is empty", eventID))
				return
			}
			for k, expectedVal := range ce.Snapshot {
				actualVal, exists := matched.Payload[k]
				if !exists {
					record(CheckCitationResolution, false, fmt.Sprintf("snapshot mismatch for event %s: payload missing key %q", eventID, k))
					return
				}
				if !valuesMatch(expectedVal, actualVal) {
					record(CheckCitationResolution, false, fmt.Sprintf("snapshot mismatch for event %s: key %q expected %v, got %v", eventID, k, expectedVal, actualVal))
					return
				}
			}
		}
	}

	record(CheckCitationResolution, true, "all cited events resolved to telemetry fixtures with matching snapshots")
}

func checkNoSelfReview(l Lesson, record func(string, bool, string)) {
	author := strings.TrimSpace(l.AuthorClass)
	if author == "" {
		record(CheckNoSelfReview, false, "author_class must not be empty")
		return
	}
	if len(l.ReviewedClasses) == 0 {
		record(CheckNoSelfReview, false, "reviewed_classes must not be empty")
		return
	}

	for _, rc := range l.ReviewedClasses {
		if strings.EqualFold(strings.TrimSpace(rc), author) {
			record(CheckNoSelfReview, false, fmt.Sprintf("self-review prohibited: author class %q appears in reviewed classes %v", author, l.ReviewedClasses))
			return
		}
	}

	record(CheckNoSelfReview, true, "no self-review: author class does not appear in reviewed classes")
}

func checkDedup(l Lesson, deps Dependencies, record func(string, bool, string)) {
	hash := l.CitationSetHash()
	if hash == "" {
		record(CheckDedup, false, "cannot compute citation set hash: no cited events")
		return
	}

	if deps.Ledger == nil {
		record(CheckDedup, false, "ledger dependency required for dedup check")
		return
	}

	existing, err := deps.Ledger.Load()
	if err != nil {
		record(CheckDedup, false, fmt.Sprintf("failed to load ledger for dedup check: %v", err))
		return
	}

	for _, ex := range existing {
		if ex.CitationSetHash() == hash {
			record(CheckDedup, false, fmt.Sprintf("duplicate citation set: hash %s already exists in ledger (lesson ID %s)", hash, ex.ID))
			return
		}
	}

	record(CheckDedup, true, "dedup passed: unique citation set")
}

func checkBudget(l Lesson, deps Dependencies, record func(string, bool, string)) {
	roundID := strings.TrimSpace(l.RoundID)
	if roundID == "" {
		record(CheckBudget, false, "round_id must not be empty")
		return
	}

	capLimit := deps.RoundCap
	if capLimit <= 0 {
		capLimit = DefaultRoundBudget
	}

	if deps.Ledger == nil {
		record(CheckBudget, false, "ledger dependency required for budget check")
		return
	}

	existing, err := deps.Ledger.Load()
	if err != nil {
		record(CheckBudget, false, fmt.Sprintf("failed to load ledger for budget check: %v", err))
		return
	}

	count := 0
	for _, ex := range existing {
		if ex.RoundID == roundID && ex.Status != StatusRejected {
			count++
		}
	}

	if count >= capLimit {
		record(CheckBudget, false, fmt.Sprintf("round %s exceeded budget cap of %d lessons (found %d existing)", roundID, capLimit, count))
		return
	}

	record(CheckBudget, true, fmt.Sprintf("budget check passed: %d/%d lessons for round %s", count, capLimit, roundID))
}

func checkSchemaIntegrity(l Lesson, record func(string, bool, string)) {
	if strings.TrimSpace(l.Recommendation) == "" {
		record(CheckSchemaIntegrity, false, "recommendation must not be empty")
		return
	}

	if !l.RecommendationIsLLMOpinion {
		record(CheckSchemaIntegrity, false, "recommendation_is_llm_opinion must be true")
		return
	}

	// Check observation text for smuggled recommendations.
	// Fail-closed policy: any occurrence of "recommend", "should", or "propose" in
	// observation text or cited claims is rejected.
	if m := smuggledMarkerRegex.FindString(l.Observation.Text); m != "" {
		record(CheckSchemaIntegrity, false, fmt.Sprintf("observation text contains forbidden recommendation marker %q: observations must contain only factual evidence; recommendations must be placed in the recommendation field", m))
		return
	}

	for idx, ce := range l.Observation.CitedEvents {
		if m := smuggledMarkerRegex.FindString(ce.Claim); m != "" {
			record(CheckSchemaIntegrity, false, fmt.Sprintf("cited event [%d] claim contains forbidden recommendation marker %q: claims must contain only factual evidence; recommendations must be placed in the recommendation field", idx, m))
			return
		}
	}

	record(CheckSchemaIntegrity, true, "schema integrity passed: recommendation valid and observation free of smuggled markers")
}

// Ledger is an append-only JSONL ledger storing verified lessons.
type Ledger struct {
	path string
	mu   sync.RWMutex
}

// NewLedger constructs a new Ledger targeting the given file path.
func NewLedger(path string) *Ledger {
	return &Ledger{path: path}
}

// Path returns the configured ledger file path.
func (l *Ledger) Path() string {
	return l.path
}

// Load reads all lessons from the JSONL file.
// If the file does not exist, it returns an empty slice and nil error.
func (l *Ledger) Load() ([]Lesson, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.loadUnlocked()
}

// List returns all lessons currently in the ledger.
func (l *Ledger) List() ([]Lesson, error) {
	return l.Load()
}

func (l *Ledger) loadUnlocked() ([]Lesson, error) {
	data, err := os.ReadFile(l.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Lesson{}, nil
		}
		return nil, fmt.Errorf("read ledger file %s: %w", l.path, err)
	}

	lines := strings.Split(string(data), "\n")
	var lessons []Lesson
	for idx, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		var item Lesson
		if err := json.Unmarshal([]byte(trimmed), &item); err != nil {
			return nil, fmt.Errorf("corrupt ledger line %d: %w", idx+1, err)
		}
		lessons = append(lessons, item)
	}
	return lessons, nil
}

// Append appends a lesson to the ledger.
// Refusal semantics:
// 1. Rejected lessons are NEVER appended (ErrRejectedLesson).
// 2. Empty citation set is rejected (ErrEmptyCitationSet).
// 3. Duplicate citation-set hash is rejected (ErrDuplicateCitationSet).
func (l *Ledger) Append(lesson Lesson) error {
	if lesson.Status == StatusRejected {
		return ErrRejectedLesson
	}

	hash := lesson.CitationSetHash()
	if hash == "" {
		return ErrEmptyCitationSet
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	existing, err := l.loadUnlocked()
	if err != nil {
		return err
	}

	for _, ex := range existing {
		if ex.CitationSetHash() == hash {
			return fmt.Errorf("%w: hash %s matches existing lesson %s", ErrDuplicateCitationSet, hash, ex.ID)
		}
	}

	line, err := json.Marshal(lesson)
	if err != nil {
		return fmt.Errorf("marshal lesson: %w", err)
	}

	dir := filepath.Dir(l.path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create ledger dir: %w", err)
		}
	}

	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open ledger file: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("append to ledger: %w", err)
	}

	return nil
}

func valuesMatch(expected, actual any) bool {
	if reflect.DeepEqual(expected, actual) {
		return true
	}

	// Handle numeric representations across JSON unmarshaling
	if expNum, ok := toFloat64(expected); ok {
		if actNum, ok := toFloat64(actual); ok {
			return expNum == actNum
		}
	}

	// Handle maps recursively (subset matching for nested maps)
	expMap, expIsMap := expected.(map[string]any)
	actMap, actIsMap := actual.(map[string]any)
	if expIsMap && actIsMap {
		for k, ev := range expMap {
			av, exists := actMap[k]
			if !exists || !valuesMatch(ev, av) {
				return false
			}
		}
		return true
	}

	// Slices comparison
	expSlice, expIsSlice := expected.([]any)
	actSlice, actIsSlice := actual.([]any)
	if expIsSlice && actIsSlice {
		if len(expSlice) != len(actSlice) {
			return false
		}
		for i := range expSlice {
			if !valuesMatch(expSlice[i], actSlice[i]) {
				return false
			}
		}
		return true
	}

	// Fallback to JSON serialization comparison
	b1, err1 := json.Marshal(expected)
	b2, err2 := json.Marshal(actual)
	if err1 == nil && err2 == nil && string(b1) == string(b2) {
		return true
	}

	return false
}

func toFloat64(v any) (float64, bool) {
	switch val := v.(type) {
	case int:
		return float64(val), true
	case int8:
		return float64(val), true
	case int16:
		return float64(val), true
	case int32:
		return float64(val), true
	case int64:
		return float64(val), true
	case uint:
		return float64(val), true
	case uint8:
		return float64(val), true
	case uint16:
		return float64(val), true
	case uint32:
		return float64(val), true
	case uint64:
		return float64(val), true
	case float32:
		return float64(val), true
	case float64:
		return val, true
	case json.Number:
		f, err := val.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}
