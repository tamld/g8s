package verifier

import (
	"bufio"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tamld/g8s/internal/pathutil"
)

const DefaultGateOutcomeWindow = 20

type GateOutcomeRecord struct {
	TaskID          string `json:"task_id,omitempty"`
	Class           string `json:"class"`
	CheckType       string `json:"check_type"`
	CheckTarget     string `json:"check_target"`
	OK              bool   `json:"ok"`
	Detail          string `json:"detail,omitempty"`
	TreeSHA         string `json:"tree_sha,omitempty"`
	TestsRan        int    `json:"tests_ran,omitempty"`
	TestsFailed     int    `json:"tests_failed,omitempty"`
	FlakeTransition bool   `json:"flake_transition,omitempty"`
	RecordedAt      string `json:"recorded_at"`
}

type GateFlakeSummary struct {
	Class               string  `json:"class"`
	CheckType           string  `json:"check_type"`
	CheckTarget         string  `json:"check_target"`
	WindowSamples       int     `json:"window_samples"`
	PassCount           int     `json:"pass_count"`
	FailCount           int     `json:"fail_count"`
	FlakeTransitions    int     `json:"flake_transitions"`
	FlakeRate           float64 `json:"flake_rate"`
	QuarantineCandidate bool    `json:"quarantine_candidate"`
	LastUpdatedAt       string  `json:"last_updated_at"`
}

func GateOutcomePath(stateDir string) string {
	if override := strings.TrimSpace(os.Getenv("G8S_GATE_OUTCOME_PATH")); override != "" {
		return override
	}
	if strings.TrimSpace(stateDir) == "" {
		stateDir = pathutil.DefaultStateDir()
	}
	return filepath.Join(stateDir, "gate_outcome_events.jsonl")
}

func gateOutcomeKey(class, checkType, checkTarget string) string {
	return class + "\x00" + checkType + "\x00" + checkTarget
}

func loadLatestGateOKByKey(path string) (map[string]bool, error) {
	prevOK := make(map[string]bool)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return prevOK, nil
		}
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec GateOutcomeRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		key := gateOutcomeKey(rec.Class, rec.CheckType, rec.CheckTarget)
		prevOK[key] = rec.OK
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return prevOK, nil
}

// RecordGateOutcomes appends per-check verification outcomes to the gate-outcome ledger.
func RecordGateOutcomes(stateDir string, taskID string, verdict Verdict) error {
	p := GateOutcomePath(stateDir)
	prevOK, err := loadLatestGateOKByKey(p)
	if err != nil {
		return err
	}
	prevExists := make(map[string]bool)
	for k := range prevOK {
		prevExists[k] = true
	}

	checks := verdict.Checks
	if len(checks) == 0 {
		checks = []CheckResult{{
			Type:   "verdict",
			Target: verdict.Class,
			OK:     verdict.Outcome == OutcomePass,
		}}
	}

	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}

	recordedAt := verdict.RecordedAt.UTC().Format(time.RFC3339)
	if verdict.RecordedAt.IsZero() {
		recordedAt = time.Now().UTC().Format(time.RFC3339)
	}

	for _, chk := range checks {
		key := gateOutcomeKey(verdict.Class, chk.Type, chk.Target)
		flake := prevExists[key] && prevOK[key] != chk.OK
		prevOK[key] = chk.OK
		prevExists[key] = true

		rec := GateOutcomeRecord{
			TaskID:          taskID,
			Class:           verdict.Class,
			CheckType:       chk.Type,
			CheckTarget:     chk.Target,
			OK:              chk.OK,
			Detail:          chk.Detail,
			TreeSHA:         verdict.TreeSHA,
			TestsRan:        verdict.TestsRan,
			TestsFailed:     verdict.TestsFailed,
			FlakeTransition: flake,
			RecordedAt:      recordedAt,
		}
		b, merr := json.Marshal(rec)
		if merr != nil {
			if cerr := f.Close(); cerr != nil {
				return cerr
			}
			return merr
		}
		if _, werr := f.Write(append(b, '\n')); werr != nil {
			if cerr := f.Close(); cerr != nil {
				return cerr
			}
			return werr
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	return nil
}

// ComputeGateFlakeSummaries aggregates rolling flake metrics per check target.
func ComputeGateFlakeSummaries(stateDir string, windowSize int) ([]GateFlakeSummary, error) {
	if windowSize <= 0 {
		windowSize = DefaultGateOutcomeWindow
	}
	p := GateOutcomePath(stateDir)
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return []GateFlakeSummary{}, nil
		}
		return nil, err
	}
	defer f.Close()

	type groupKey struct {
		class       string
		checkType   string
		checkTarget string
	}
	grouped := make(map[groupKey][]GateOutcomeRecord)

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec GateOutcomeRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		k := groupKey{class: rec.Class, checkType: rec.CheckType, checkTarget: rec.CheckTarget}
		grouped[k] = append(grouped[k], rec)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	summaries := make([]GateFlakeSummary, 0, len(grouped))
	for k, records := range grouped {
		window := records
		if len(window) > windowSize {
			window = window[len(window)-windowSize:]
		}
		passCount := 0
		failCount := 0
		flakeTransitions := 0
		lastUpdated := ""
		for i, r := range window {
			if r.OK {
				passCount++
			} else {
				failCount++
			}
			if i > 0 && window[i].OK != window[i-1].OK {
				flakeTransitions++
			}
			if r.RecordedAt != "" {
				lastUpdated = r.RecordedAt
			}
		}
		var flakeRate float64
		if len(window) > 1 {
			rate := float64(flakeTransitions) / float64(len(window)-1)
			flakeRate = math.Round(rate*10000) / 10000
		}
		quarantine := flakeTransitions >= 2 || (passCount > 0 && failCount > 0 && flakeRate >= 0.25)
		summaries = append(summaries, GateFlakeSummary{
			Class:               k.class,
			CheckType:           k.checkType,
			CheckTarget:         k.checkTarget,
			WindowSamples:       len(window),
			PassCount:           passCount,
			FailCount:           failCount,
			FlakeTransitions:    flakeTransitions,
			FlakeRate:           flakeRate,
			QuarantineCandidate: quarantine,
			LastUpdatedAt:       lastUpdated,
		})
	}

	sort.SliceStable(summaries, func(i, j int) bool {
		if summaries[i].Class != summaries[j].Class {
			return summaries[i].Class < summaries[j].Class
		}
		if summaries[i].CheckType != summaries[j].CheckType {
			return summaries[i].CheckType < summaries[j].CheckType
		}
		return summaries[i].CheckTarget < summaries[j].CheckTarget
	})

	return summaries, nil
}
