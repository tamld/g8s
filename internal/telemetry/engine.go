package telemetry

import (
	"context"
	cryptorand "crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/pathutil"
	_ "modernc.org/sqlite"
)

var (
	ErrEventNotFound   = errors.New("telemetry event not found")
	ErrPatternNotFound = errors.New("negative pattern not found")
)

// TelemetryEngine implements TelemetryIngestor, NegativeKnowledgeDistiller, and PreflightInjectionService
type TelemetryEngine struct {
	db        *sql.DB
	config    *TelemetryConfig
	clock     func() time.Time
	mu        sync.RWMutex
	eventChan chan TraceEvent
	stopChan  chan struct{}
	wg        sync.WaitGroup
	seq       atomic.Uint64
}

func NewTelemetryEngine(config *TelemetryConfig) (*TelemetryEngine, error) {
	if config == nil {
		config = DefaultTelemetryConfig()
	}

	if err := config.Validate(); err != nil {
		return nil, err
	}

	dsn := pathutil.SQLiteURI(config.DBPath, "_txlock=immediate&_pragma=busy_timeout(30000)&_pragma=foreign_keys(ON)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)")
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open telemetry db: %w", err)
	}
	db.SetMaxOpenConns(1)

	engine := &TelemetryEngine{
		db:        db,
		config:    config,
		clock:     time.Now,
		eventChan: make(chan TraceEvent, config.BatchSize),
		stopChan:  make(chan struct{}),
	}

	if err := engine.initSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("init telemetry schema: %w", err)
	}

	engine.wg.Add(1)
	go engine.batchProcessor()

	return engine, nil
}

func (c *TelemetryConfig) Validate() error {
	if c.BatchSize <= 0 {
		return errors.New("batch size must be positive")
	}
	if c.FlushInterval <= 0 {
		return errors.New("flush interval must be positive")
	}
	if c.RetentionPeriod <= 0 {
		return errors.New("retention period must be positive")
	}
	if c.DistillationThreshold <= 0 {
		return errors.New("distillation threshold must be positive")
	}
	return nil
}

func (e *TelemetryEngine) initSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS telemetry_events (
		id TEXT PRIMARY KEY,
		task_id TEXT NOT NULL,
		supervisor_task_id TEXT,
		event_type TEXT NOT NULL,
		timestamp INTEGER NOT NULL,
		payload TEXT NOT NULL,
		exit_code INTEGER,
		error TEXT,
		duration INTEGER,
		tags TEXT
	);

	CREATE INDEX IF NOT EXISTS idx_events_task_id ON telemetry_events(task_id);
	CREATE INDEX IF NOT EXISTS idx_events_supervisor_task_id ON telemetry_events(supervisor_task_id);
	CREATE INDEX IF NOT EXISTS idx_events_type ON telemetry_events(event_type);
	CREATE INDEX IF NOT EXISTS idx_events_timestamp ON telemetry_events(timestamp);
	CREATE INDEX IF NOT EXISTS idx_events_exit_code ON telemetry_events(exit_code);

	CREATE TABLE IF NOT EXISTS negative_patterns (
		id TEXT PRIMARY KEY,
		pattern_type TEXT NOT NULL,
		title TEXT NOT NULL,
		description TEXT NOT NULL,
		root_cause TEXT NOT NULL,
		remediation TEXT NOT NULL,
		occurrence_count INTEGER NOT NULL DEFAULT 1,
		first_seen INTEGER NOT NULL,
		last_seen INTEGER NOT NULL,
		affected_packages TEXT NOT NULL,
		example_contexts TEXT NOT NULL,
		confidence_score REAL NOT NULL DEFAULT 0.0,
		status TEXT NOT NULL DEFAULT 'DRAFT'
	);

	CREATE INDEX IF NOT EXISTS idx_patterns_type ON negative_patterns(pattern_type);
	CREATE INDEX IF NOT EXISTS idx_patterns_status ON negative_patterns(status);
	CREATE INDEX IF NOT EXISTS idx_patterns_packages ON negative_patterns(affected_packages);
	CREATE INDEX IF NOT EXISTS idx_patterns_confidence ON negative_patterns(confidence_score);
	CREATE INDEX IF NOT EXISTS idx_patterns_last_seen ON negative_patterns(last_seen);
	`
	_, err := e.db.Exec(schema)
	return err
}

func (e *TelemetryEngine) SetClock(clock func() time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if clock != nil {
		e.clock = clock
	}
}

func (e *TelemetryEngine) getClock() time.Time {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.clock()
}

func (e *TelemetryEngine) Close() error {
	close(e.stopChan)
	e.wg.Wait()
	return e.db.Close()
}

func (e *TelemetryEngine) IngestEvent(ctx context.Context, event TraceEvent) error {
	if event.ID == "" {
		seq := e.seq.Add(1)
		event.ID = fmt.Sprintf("evt-%d-%06d-%s", e.getClock().UnixNano(), seq%1000000, randomString(8))
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = e.getClock()
	}
	select {
	case e.eventChan <- event:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *TelemetryEngine) IngestEvents(ctx context.Context, events []TraceEvent) error {
	for _, event := range events {
		if err := e.IngestEvent(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

func (e *TelemetryEngine) batchProcessor() {
	defer e.wg.Done()
	ticker := time.NewTicker(e.config.FlushInterval)
	defer ticker.Stop()

	batch := make([]TraceEvent, 0, e.config.BatchSize)

	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := e.persistBatch(ctx, batch)
		batch = batch[:0]
		return err
	}

	for {
		select {
		case event := <-e.eventChan:
			batch = append(batch, event)
			if len(batch) >= e.config.BatchSize {
				// Best-effort flush; failures surface via engine metrics
				// rather than killing the ingest loop.
				_ = flush()
			}
		case <-ticker.C:
			_ = flush()
		case <-e.stopChan:
			// Drain buffered events before the final flush: a bare flush()
			// here races the event channel and silently loses everything
			// still buffered (the #342 Windows batch-loss symptom).
			for {
				select {
				case event := <-e.eventChan:
					batch = append(batch, event)
					if len(batch) >= e.config.BatchSize {
						_ = flush()
					}
				default:
					_ = flush()
					return
				}
			}
		}
	}
}

func (e *TelemetryEngine) persistBatch(ctx context.Context, events []TraceEvent) error {
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO telemetry_events (
			id, task_id, supervisor_task_id, event_type, timestamp,
			payload, exit_code, error, duration, tags
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			task_id=excluded.task_id,
			supervisor_task_id=excluded.supervisor_task_id,
			event_type=excluded.event_type,
			timestamp=excluded.timestamp,
			payload=excluded.payload,
			exit_code=excluded.exit_code,
			error=excluded.error,
			duration=excluded.duration,
			tags=excluded.tags
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, event := range events {
		payload, _ := json.Marshal(event.Payload)
		tags := strings.Join(event.Tags, ",")
		var exitCode *int
		if event.ExitCode != nil {
			exitCode = event.ExitCode
		}
		_, err := stmt.ExecContext(ctx,
			event.ID,
			event.TaskID,
			event.SupervisorTaskID,
			string(event.EventType),
			event.Timestamp.UnixNano(),
			string(payload),
			exitCode,
			event.Error,
			event.Duration.Nanoseconds(),
			tags,
		)
		if err != nil {
			return err
		}
	}

	if e.config.EnableDistillation {
		// Best-effort distillation; a failure must not abort the ingest tx.
		_ = e.distillFromBatch(ctx, tx, events)
	}

	return tx.Commit()
}

func (e *TelemetryEngine) distillFromBatch(ctx context.Context, tx *sql.Tx, events []TraceEvent) error {
	failureEvents := filterFailureEvents(events)
	if len(failureEvents) == 0 {
		return nil
	}

	for _, event := range failureEvents {
		pattern, err := e.extractPattern(ctx, tx, event)
		if err != nil {
			continue
		}
		if pattern != nil {
			if err := e.upsertPattern(ctx, tx, *pattern); err != nil {
				continue
			}
		}
	}
	return nil
}

func filterFailureEvents(events []TraceEvent) []TraceEvent {
	var failures []TraceEvent
	for _, e := range events {
		if e.EventType == TraceEventTaskFailed ||
			e.EventType == TraceEventWorkerFailed ||
			e.EventType == TraceEventReceiptRejected ||
			e.EventType == TraceEventPolicyViolation ||
			e.EventType == TraceEventBlockedCommand ||
			e.EventType == TraceEventAnomalyDetected ||
			e.Error != "" ||
			(e.ExitCode != nil && *e.ExitCode != 0) {
			failures = append(failures, e)
		}
	}
	return failures
}

func (e *TelemetryEngine) extractPattern(ctx context.Context, tx *sql.Tx, event TraceEvent) (*NegativePattern, error) {
	failureMode := classifyFailureMode(event)
	if failureMode == FailureModeUnknown {
		return nil, nil
	}

	patternID := generatePatternID(failureMode, event)

	existing, err := e.getPatternInTx(ctx, tx, patternID)
	if err == nil {
		existing.OccurrenceCount++
		existing.LastSeen = e.getClock()
		if existing.ConfidenceScore < 0.95 {
			newScore := existing.ConfidenceScore + 0.05
			if newScore > 0.95 {
				newScore = 0.95
			}
			existing.ConfidenceScore = newScore
		}
		if len(existing.ExampleContexts) < 5 {
			existing.ExampleContexts = append(existing.ExampleContexts, summarizeEvent(event))
		}
		return &existing, nil
	}

	if !errors.Is(err, ErrPatternNotFound) {
		return nil, err
	}

	packages := extractPackages(event)
	pattern := &NegativePattern{
		ID:               patternID,
		PatternType:      failureMode,
		Title:            generatePatternTitle(failureMode, event),
		Description:      generatePatternDescription(failureMode, event),
		RootCause:        inferRootCause(failureMode, event),
		Remediation:      suggestRemediation(failureMode, event),
		OccurrenceCount:  1,
		FirstSeen:        e.getClock(),
		LastSeen:         e.getClock(),
		AffectedPackages: packages,
		ExampleContexts:  []string{summarizeEvent(event)},
		ConfidenceScore:  0.3,
		Status:           PatternStatusDraft,
	}
	return pattern, nil
}

func classifyFailureMode(event TraceEvent) FailureMode {
	if event.ExitCode != nil {
		switch *event.ExitCode {
		case 1:
			return FailureModeCrash
		case 2:
			return FailureModeTimeout
		case 3:
			return FailureModeReadOnlyViolation
		case 4:
			return FailureModeBlockedCommand
		}
	}
	switch event.EventType {
	case TraceEventReceiptRejected:
		return FailureModeReceiptRejected
	case TraceEventPolicyViolation:
		return FailureModePolicyViolation
	case TraceEventBlockedCommand:
		return FailureModeBlockedCommand
	case TraceEventAnomalyDetected:
		if strings.Contains(strings.ToLower(event.Error), "ambiguous") {
			return FailureModeAmbiguousPrompt
		}
	}
	return FailureModeUnknown
}

func generatePatternID(mode FailureMode, event TraceEvent) string {
	key := fmt.Sprintf("%s-%s", mode, hashString(summarizeEvent(event))[:12])
	return fmt.Sprintf("neg-%s", key)
}

func hashString(s string) string {
	h := make([]byte, 16)
	for i, c := range s {
		h[i%16] ^= byte(c)
	}
	return fmt.Sprintf("%x", h)
}

func generatePatternTitle(mode FailureMode, event TraceEvent) string {
	taskIDShort := event.TaskID
	if len(taskIDShort) > 8 {
		taskIDShort = taskIDShort[:8]
	}
	switch mode {
	case FailureModeCrash:
		return fmt.Sprintf("Worker crash in task %s", taskIDShort)
	case FailureModeTimeout:
		return fmt.Sprintf("Task timeout: %s", taskIDShort)
	case FailureModeReadOnlyViolation:
		return fmt.Sprintf("Read-only violation in %s", extractPath(event))
	case FailureModeBlockedCommand:
		return fmt.Sprintf("Blocked command attempt: %s", extractCommand(event))
	case FailureModePolicyViolation:
		return fmt.Sprintf("Policy violation: %s", event.Error[:min(50, len(event.Error))])
	case FailureModeReceiptRejected:
		return fmt.Sprintf("Receipt rejected: %s", event.Error[:min(50, len(event.Error))])
	case FailureModeAmbiguousPrompt:
		return fmt.Sprintf("Ambiguous prompt pattern: %s", extractPromptSummary(event))
	default:
		return fmt.Sprintf("Unknown failure: %s", taskIDShort)
	}
}

func generatePatternDescription(mode FailureMode, event TraceEvent) string {
	return fmt.Sprintf("Failure mode %s detected during task %s. Context: %s", mode, event.TaskID, summarizeEvent(event))
}

func inferRootCause(mode FailureMode, event TraceEvent) string {
	switch mode {
	case FailureModeCrash:
		return "Unhandled error or panic in worker execution"
	case FailureModeTimeout:
		return "Task exceeded configured timeout threshold"
	case FailureModeReadOnlyViolation:
		return "Worker attempted write operation with read-only permissions"
	case FailureModeBlockedCommand:
		return "Harness blocked dangerous command (rm -rf, shutdown, etc.)"
	case FailureModePolicyViolation:
		return fmt.Sprintf("Policy violation: %s", event.Error)
	case FailureModeReceiptRejected:
		return "Receipt validation failed (missing, expired, wrong scope)"
	case FailureModeAmbiguousPrompt:
		return "Prompt lacks sufficient context for deterministic execution"
	default:
		return "Root cause unknown, requires manual investigation"
	}
}

func suggestRemediation(mode FailureMode, event TraceEvent) string {
	switch mode {
	case FailureModeCrash:
		return "Add error handling, check worker logs, validate input"
	case FailureModeTimeout:
		return "Increase timeout, optimize worker logic, add progress checkpoints"
	case FailureModeReadOnlyViolation:
		return "Ensure worker has correct permission receipt, use read-only role"
	case FailureModeBlockedCommand:
		return "Review task prompt for dangerous commands, use safer alternatives"
	case FailureModePolicyViolation:
		return "Align task with policy constraints, use permitted operations"
	case FailureModeReceiptRejected:
		return "Validate receipt before submission, check scope and permissions"
	case FailureModeAmbiguousPrompt:
		return "Add explicit instructions, specify target files, define success criteria"
	default:
		return "Investigate manually, add specific handling for this pattern"
	}
}

func extractPackages(event TraceEvent) []string {
	var packages []string
	if pkg, ok := event.Payload["package"].(string); ok {
		packages = append(packages, pkg)
	}
	if path, ok := event.Payload["file_path"].(string); ok {
		packages = append(packages, path)
	}
	return packages
}

func extractPath(event TraceEvent) string {
	if path, ok := event.Payload["file_path"].(string); ok {
		return path
	}
	return "unknown"
}

func extractCommand(event TraceEvent) string {
	if cmd, ok := event.Payload["command"].(string); ok {
		return cmd
	}
	if cmd, ok := event.Payload["blocked_command"].(string); ok {
		return cmd
	}
	return "unknown"
}

func extractPromptSummary(event TraceEvent) string {
	if prompt, ok := event.Payload["prompt"].(string); ok {
		if len(prompt) > 60 {
			return prompt[:60] + "..."
		}
		return prompt
	}
	return "unknown"
}

func summarizeEvent(event TraceEvent) string {
	parts := []string{
		fmt.Sprintf("task=%s", event.TaskID),
		fmt.Sprintf("type=%s", event.EventType),
	}
	if event.Error != "" {
		parts = append(parts, fmt.Sprintf("error=%s", event.Error[:min(30, len(event.Error))]))
	}
	if event.ExitCode != nil {
		parts = append(parts, fmt.Sprintf("exit=%d", *event.ExitCode))
	}
	return strings.Join(parts, ", ")
}

func (e *TelemetryEngine) getPatternInTx(ctx context.Context, tx *sql.Tx, id string) (NegativePattern, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id, pattern_type, title, description, root_cause, remediation,
		       occurrence_count, first_seen, last_seen, affected_packages,
		       example_contexts, confidence_score, status
		FROM negative_patterns WHERE id = ?
	`, id)

	var p NegativePattern
	var firstSeen, lastSeen int64
	var packages, contexts string

	err := row.Scan(
		&p.ID, &p.PatternType, &p.Title, &p.Description, &p.RootCause, &p.Remediation,
		&p.OccurrenceCount, &firstSeen, &lastSeen, &packages, &contexts,
		&p.ConfidenceScore, &p.Status,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NegativePattern{}, ErrPatternNotFound
		}
		return NegativePattern{}, err
	}

	p.FirstSeen = time.Unix(0, firstSeen)
	p.LastSeen = time.Unix(0, lastSeen)
	// Legacy columns may hold pre-validation JSON; malformed payloads
	// degrade to empty package/context lists instead of failing the scan.
	_ = json.Unmarshal([]byte(packages), &p.AffectedPackages)
	_ = json.Unmarshal([]byte(contexts), &p.ExampleContexts)
	return p, nil
}

func (e *TelemetryEngine) upsertPattern(ctx context.Context, tx *sql.Tx, pattern NegativePattern) error {
	packages, _ := json.Marshal(pattern.AffectedPackages)
	contexts, _ := json.Marshal(pattern.ExampleContexts)

	_, err := tx.ExecContext(ctx, `
		INSERT INTO negative_patterns (
			id, pattern_type, title, description, root_cause, remediation,
			occurrence_count, first_seen, last_seen, affected_packages,
			example_contexts, confidence_score, status
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			pattern_type=excluded.pattern_type,
			title=excluded.title,
			description=excluded.description,
			root_cause=excluded.root_cause,
			remediation=excluded.remediation,
			occurrence_count=excluded.occurrence_count,
			last_seen=excluded.last_seen,
			affected_packages=excluded.affected_packages,
			example_contexts=excluded.example_contexts,
			confidence_score=excluded.confidence_score,
			status=excluded.status
	`,
		pattern.ID, pattern.PatternType, pattern.Title, pattern.Description,
		pattern.RootCause, pattern.Remediation, pattern.OccurrenceCount,
		pattern.FirstSeen.UnixNano(), pattern.LastSeen.UnixNano(),
		string(packages), string(contexts), pattern.ConfidenceScore, pattern.Status,
	)
	return err
}

func (e *TelemetryEngine) QueryEvents(ctx context.Context, filter TraceFilter) ([]TraceEvent, error) {
	var conditions []string
	var args []any

	if filter.TaskID != nil {
		conditions = append(conditions, "task_id = ?")
		args = append(args, *filter.TaskID)
	}
	if filter.SupervisorTaskID != nil {
		conditions = append(conditions, "supervisor_task_id = ?")
		args = append(args, *filter.SupervisorTaskID)
	}
	if len(filter.EventTypes) > 0 {
		placeholders := make([]string, len(filter.EventTypes))
		for i := range filter.EventTypes {
			placeholders[i] = "?"
			args = append(args, string(filter.EventTypes[i]))
		}
		conditions = append(conditions, "event_type IN ("+strings.Join(placeholders, ",")+")")
	}
	if filter.Since != nil {
		conditions = append(conditions, "timestamp >= ?")
		args = append(args, filter.Since.UnixNano())
	}
	if filter.Until != nil {
		conditions = append(conditions, "timestamp <= ?")
		args = append(args, filter.Until.UnixNano())
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	query := fmt.Sprintf(`
		SELECT id, task_id, supervisor_task_id, event_type, timestamp,
		       payload, exit_code, error, duration, tags
		FROM telemetry_events %s
		ORDER BY timestamp DESC LIMIT ? OFFSET ?
	`, whereClause)
	args = append(args, limit, filter.Offset)

	rows, err := e.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []TraceEvent
	for rows.Next() {
		var e TraceEvent
		var ts, dur int64
		var payload, tags, supTaskID sql.NullString
		var exitCode sql.NullInt64
		var errStr sql.NullString

		err := rows.Scan(&e.ID, &e.TaskID, &supTaskID, &e.EventType, &ts,
			&payload, &exitCode, &errStr, &dur, &tags)
		if err != nil {
			continue
		}

		e.Timestamp = time.Unix(0, ts)
		e.Duration = time.Duration(dur)
		if supTaskID.Valid {
			e.SupervisorTaskID = &supTaskID.String
		}
		if exitCode.Valid {
			val := int(exitCode.Int64)
			e.ExitCode = &val
		}
		if errStr.Valid {
			e.Error = errStr.String
		}
		if tags.Valid && tags.String != "" {
			e.Tags = strings.Split(tags.String, ",")
		}
		// Legacy payload column may hold malformed JSON; degrade to nil map.
		_ = json.Unmarshal([]byte(payload.String), &e.Payload)

		events = append(events, e)
	}
	return events, rows.Err()
}

func (e *TelemetryEngine) GetEvent(ctx context.Context, id string) (*TraceEvent, error) {
	filter := TraceFilter{Limit: 1}
	events, err := e.QueryEvents(ctx, filter)
	if err != nil {
		return nil, err
	}
	for _, e := range events {
		if e.ID == id {
			return &e, nil
		}
	}
	return nil, ErrEventNotFound
}

func (e *TelemetryEngine) DistillFailure(ctx context.Context, event TraceEvent) (*NegativePattern, error) {
	return e.extractPattern(ctx, nil, event)
}

func (e *TelemetryEngine) DistillBatch(ctx context.Context, events []TraceEvent) ([]NegativePattern, error) {
	var patterns []NegativePattern
	for _, event := range events {
		p, err := e.DistillFailure(ctx, event)
		if err == nil && p != nil {
			patterns = append(patterns, *p)
		}
	}
	return patterns, nil
}

func (e *TelemetryEngine) GetPatterns(ctx context.Context, filter PatternFilter) ([]NegativePattern, error) {
	var conditions []string
	var args []any

	if len(filter.PatternTypes) > 0 {
		placeholders := make([]string, len(filter.PatternTypes))
		for i := range filter.PatternTypes {
			placeholders[i] = "?"
			args = append(args, string(filter.PatternTypes[i]))
		}
		conditions = append(conditions, "pattern_type IN ("+strings.Join(placeholders, ",")+")")
	}
	if len(filter.Statuses) > 0 {
		placeholders := make([]string, len(filter.Statuses))
		for i := range filter.Statuses {
			placeholders[i] = "?"
			args = append(args, string(filter.Statuses[i]))
		}
		conditions = append(conditions, "status IN ("+strings.Join(placeholders, ",")+")")
	}
	if filter.MinConfidence > 0 {
		conditions = append(conditions, "confidence_score >= ?")
		args = append(args, filter.MinConfidence)
	}
	if filter.Since != nil {
		conditions = append(conditions, "last_seen >= ?")
		args = append(args, filter.Since.UnixNano())
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	query := fmt.Sprintf(`
		SELECT id, pattern_type, title, description, root_cause, remediation,
		       occurrence_count, first_seen, last_seen, affected_packages,
		       example_contexts, confidence_score, status
		FROM negative_patterns %s
		ORDER BY confidence_score DESC, last_seen DESC LIMIT ? OFFSET ?
	`, whereClause)
	args = append(args, limit, filter.Offset)

	rows, err := e.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var patterns []NegativePattern
	for rows.Next() {
		var p NegativePattern
		var firstSeen, lastSeen int64
		var packages, contexts string

		err := rows.Scan(
			&p.ID, &p.PatternType, &p.Title, &p.Description, &p.RootCause, &p.Remediation,
			&p.OccurrenceCount, &firstSeen, &lastSeen, &packages, &contexts,
			&p.ConfidenceScore, &p.Status,
		)
		if err != nil {
			continue
		}

		p.FirstSeen = time.Unix(0, firstSeen)
		p.LastSeen = time.Unix(0, lastSeen)
		_ = json.Unmarshal([]byte(packages), &p.AffectedPackages)
		_ = json.Unmarshal([]byte(contexts), &p.ExampleContexts)
		patterns = append(patterns, p)
	}
	return patterns, rows.Err()
}

func (e *TelemetryEngine) UpdatePattern(ctx context.Context, pattern NegativePattern) error {
	_, err := e.db.ExecContext(ctx, `
		UPDATE negative_patterns SET
			title=?, description=?, root_cause=?, remediation=?,
			occurrence_count=?, last_seen=?, affected_packages=?,
			example_contexts=?, confidence_score=?, status=?
		WHERE id=?
	`,
		pattern.Title, pattern.Description, pattern.RootCause, pattern.Remediation,
		pattern.OccurrenceCount, pattern.LastSeen.UnixNano(),
		jsonMarshal(pattern.AffectedPackages), jsonMarshal(pattern.ExampleContexts),
		pattern.ConfidenceScore, pattern.Status, pattern.ID,
	)
	return err
}

func (e *TelemetryEngine) GetRelevantPatterns(ctx context.Context, role string, path string, prompt string) ([]NegativePattern, error) {
	filters := PatternFilter{
		Statuses:      []PatternStatus{PatternStatusValidated, PatternStatusApplied},
		MinConfidence: 0.5,
		Limit:         10,
	}

	patterns, err := e.GetPatterns(ctx, filters)
	if err != nil {
		return nil, err
	}

	// Filter by relevance to role, path, and prompt
	relevant := make([]NegativePattern, 0)
	for _, p := range patterns {
		score := relevanceScore(p, role, path, prompt)
		if score > 0.3 {
			relevant = append(relevant, p)
		}
	}
	return relevant, nil
}

func relevanceScore(pattern NegativePattern, role, path, prompt string) float64 {
	score := 0.0
	pkgStr := strings.Join(pattern.AffectedPackages, " ")
	if strings.Contains(pkgStr, path) {
		score += 0.4
	}
	if strings.Contains(strings.ToLower(string(pattern.PatternType)), strings.ToLower(role)) {
		score += 0.2
	}
	for _, ctx := range pattern.ExampleContexts {
		if strings.Contains(strings.ToLower(ctx), strings.ToLower(prompt[:min(20, len(prompt))])) {
			score += 0.3
			break
		}
	}
	return score
}

func (e *TelemetryEngine) InjectPreflightContext(ctx context.Context, brief *controlplane.BriefRow, patterns []NegativePattern) (string, error) {
	if len(patterns) == 0 {
		return "", nil
	}

	var sb strings.Builder
	sb.WriteString("\n\n## Relevant Failure Patterns (Pre-flight Injection)\n\n")
	for _, p := range patterns {
		fmt.Fprintf(&sb, "### %s\n", p.Title)
		fmt.Fprintf(&sb, "- **Type**: %s\n", p.PatternType)
		fmt.Fprintf(&sb, "- **Root Cause**: %s\n", p.RootCause)
		fmt.Fprintf(&sb, "- **Remediation**: %s\n", p.Remediation)
		fmt.Fprintf(&sb, "- **Confidence**: %.0f%% (%d occurrences)\n\n", p.ConfidenceScore*100, p.OccurrenceCount)
	}
	return sb.String(), nil
}

func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	var buf [32]byte
	var b []byte
	if n <= len(buf) {
		b = buf[:n]
	} else {
		b = make([]byte, n)
	}
	if _, err := cryptorand.Read(b); err != nil {
		h := uint64(time.Now().UnixNano()) ^ (uint64(os.Getpid()) << 32)
		for i := range b {
			h = h*6364136223846793005 + 1442695040888963407
			b[i] = byte(h >> 32)
		}
	}
	for i := range b {
		b[i] = letters[int(b[i])%len(letters)]
	}
	return string(b)
}

func jsonMarshal(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
