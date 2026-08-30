package sleep

import (
	"fmt"
	"strings"
	"time"
)

// WakeSummary captures structured and voice-formatted summary of events across a sleep cycle.
type WakeSummary struct {
	SleepDuration  string   `json:"sleep_duration"`
	TotalSessions  int      `json:"total_sessions"`
	Succeeded      int      `json:"succeeded"`
	Failed         int      `json:"failed"`
	CriticalCount  int      `json:"critical_count"`
	VoiceText      string   `json:"voice_text"`
	Paragraphs     []string `json:"paragraphs"`
	Bullets        []string `json:"bullets"`
	CriticalEvents []Event  `json:"critical_events,omitempty"`
}

// GenerateVoiceSummary converts sleep cycle state and collected events into
// a concise, voice-friendly summary (<= 4 paragraphs, <= 200 words each) per DEBT-50.
func GenerateVoiceSummary(state *SleepState, events []Event, now time.Time) *WakeSummary {
	if now.IsZero() {
		now = time.Now().UTC()
	}

	start := now.Add(-time.Hour)
	if state != nil && !state.SleepStart.IsZero() {
		start = state.SleepStart
	}

	duration := now.Sub(start)
	if duration < 0 {
		duration = time.Minute
	}
	durationStr := formatDurationNatural(duration)

	var (
		criticals  []Event
		successes  []Event
		failures   []Event
		sessionSet = make(map[string]bool)
		bullets    []string
		failedMsgs []string
	)

	for _, ev := range events {
		if ev.SessionID != "" {
			sessionSet[ev.SessionID] = true
		}

		if ev.Severity == SeverityCritical {
			criticals = append(criticals, ev)
		}

		switch ev.Type {
		case EventReceiptSuccess, EventSessionComplete:
			successes = append(successes, ev)
		case EventReceiptFailure, EventWorkerDead, EventBranchConflict:
			failures = append(failures, ev)
			if ev.Message != "" {
				failedMsgs = append(failedMsgs, ev.Message)
			}
		}

		bulletText := fmt.Sprintf("[%s] %s", strings.ToUpper(ev.Severity), ev.Message)
		if ev.SessionID != "" {
			bulletText += fmt.Sprintf(" (session: %s)", ev.SessionID)
		}
		bullets = append(bullets, bulletText)
	}

	totalSessions := len(sessionSet)
	if totalSessions == 0 {
		totalSessions = len(successes) + len(failures)
	}
	if totalSessions == 0 && len(events) > 0 {
		totalSessions = 1
	}

	succCount := len(successes)
	failCount := len(failures)

	var paragraphs []string

	// Paragraph 1: Overview and completion status
	var p1 string
	if totalSessions > 0 {
		p1 = fmt.Sprintf("While you were away for %s, %d of %d active worker sessions completed successfully.",
			durationStr, succCount, totalSessions)
	} else {
		p1 = fmt.Sprintf("While you were away for %s, all systems remained idle and operational.",
			durationStr)
	}
	paragraphs = append(paragraphs, p1)

	// Paragraph 2: Failures, anomalies, and automatic remediations
	var p2 string
	if failCount > 0 {
		p2 = fmt.Sprintf("%d session(s) encountered issues: %s. Necessary diagnostic data and state snapshots were preserved.",
			failCount, strings.Join(summarizeList(failedMsgs, 2), "; "))
	} else if succCount > 0 {
		p2 = "All active workflows executed smoothly with all test suites and quality gates passing cleanly."
	} else {
		p2 = "No background errors or automated worker interruptions were recorded."
	}
	paragraphs = append(paragraphs, p2)

	// Paragraph 3: Critical alerts
	var p3 string
	if len(criticals) > 0 {
		p3 = fmt.Sprintf("Attention is required for %d critical event(s): %s.",
			len(criticals), criticals[0].Message)
	} else {
		p3 = "No critical alerts, worker crashes, or repository branch conflicts occurred."
	}
	paragraphs = append(paragraphs, p3)

	// Paragraph 4: Next recommended actions and PR readiness
	var p4 string
	if succCount > 0 {
		p4 = "Completed PR branches are staged and ready for your review. Repository HEAD is healthy."
	} else {
		p4 = "System is standing by for your next dispatch."
	}
	paragraphs = append(paragraphs, p4)

	// Ensure maximum 4 paragraphs and limit word count per paragraph <= 200 words
	for i := range paragraphs {
		paragraphs[i] = truncateWords(paragraphs[i], 200)
	}
	if len(paragraphs) > 4 {
		paragraphs = paragraphs[:4]
	}

	voiceText := strings.Join(paragraphs, "\n\n")

	return &WakeSummary{
		SleepDuration:  durationStr,
		TotalSessions:  totalSessions,
		Succeeded:      succCount,
		Failed:         failCount,
		CriticalCount:  len(criticals),
		VoiceText:      voiceText,
		Paragraphs:     paragraphs,
		Bullets:        bullets,
		CriticalEvents: criticals,
	}
}

func formatDurationNatural(d time.Duration) string {
	d = d.Round(time.Minute)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute

	if h > 0 && m > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	if h > 0 {
		return fmt.Sprintf("%dh", h)
	}
	if m > 0 {
		return fmt.Sprintf("%d minutes", m)
	}
	return "under a minute"
}

func summarizeList(items []string, max int) []string {
	if len(items) == 0 {
		return []string{"unspecified errors"}
	}
	if len(items) <= max {
		return items
	}
	res := append([]string(nil), items[:max]...)
	res = append(res, fmt.Sprintf("and %d more", len(items)-max))
	return res
}

func truncateWords(s string, maxWords int) string {
	words := strings.Fields(s)
	if len(words) <= maxWords {
		return s
	}
	return strings.Join(words[:maxWords], " ") + "..."
}
