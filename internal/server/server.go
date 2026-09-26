// Package server implements the g8s daemon mode with HTTP API server.
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/supervisor"
)

// Server is the HTTP API server for g8s daemon mode.
type Server struct {
	config  Config
	store   *controlplane.Store
	httpSrv *http.Server
	mu      sync.RWMutex
	running bool
}

// maxRequestBodyBytes bounds JSON request bodies on the unauthenticated API
// surface (#358): 1 MiB is generous for task submissions and prevents a
// single oversized request from exhausting memory.
const maxRequestBodyBytes = 1 << 20

// NewServer creates a new HTTP API server.
func NewServer(config Config, store *controlplane.Store) *Server {
	if err := config.Validate(); err != nil {
		// Should not happen with defaults, but handle anyway
		log.Printf("Server config validation warning: %v", err)
	}

	s := &Server{
		config: config,
		store:  store,
	}

	mux := http.NewServeMux()
	s.registerRoutes(mux)

	s.httpSrv = &http.Server{
		Addr:              config.Address,
		Handler:           s.authMiddleware(s.corsMiddleware(mux)),
		ReadHeaderTimeout: config.ReadHeaderTimeout,
		ReadTimeout:       config.ReadTimeout,
		WriteTimeout:      config.WriteTimeout,
		IdleTimeout:       config.IdleTimeout,
	}

	return s
}

// registerRoutes registers all HTTP routes.
func (s *Server) registerRoutes(mux *http.ServeMux) {
	// OpenAPI spec and Swagger UI
	mux.HandleFunc("/openapi.json", s.HandleOpenAPISpec)
	mux.HandleFunc("/openapi", s.HandleOpenAPIUI)

	// Health check
	if s.config.EnableHealthz {
		mux.HandleFunc("/healthz", s.handleHealthz)
		mux.HandleFunc("/readyz", s.handleReadyz)
	}

	// Metrics
	if s.config.EnableMetrics {
		mux.HandleFunc("/metrics", s.handleMetrics)
	}

	// API v1 routes
	mux.HandleFunc("/api/v1/tasks", s.handleTasks)
	mux.HandleFunc("/api/v1/tasks/", s.handleTaskByID)
	mux.HandleFunc("/api/v1/receipts", s.handleReceipts)
	mux.HandleFunc("/api/v1/receipts/", s.handleReceiptByID)
	mux.HandleFunc("/api/v1/supervisor/false-escalation/", s.handleSupervisorUpdateFalseEscalation)
	mux.HandleFunc("/api/v1/supervisor", s.handleSupervisor)
	mux.HandleFunc("/api/v1/supervisor/", s.handleSupervisorByID)
	mux.HandleFunc("/api/v1/supervisor/metrics", s.handleSupervisorMetrics)
	mux.HandleFunc("/api/v1/system/metrics", s.handleSystemMetrics)
	mux.HandleFunc("/api/v1/briefs", s.handleBriefs)
	mux.HandleFunc("/api/v1/briefs/", s.handleBriefByID)
}

// authMiddleware enforces bearer-token auth on the API and OpenAPI surfaces
// when a token is configured (#controlled-access). healthz/readyz/metrics
// stay open for liveness probes. Requests without a valid token receive a
// consistent JSON error envelope.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	if s.config.ApiToken == "" {
		return next
	}
	token := s.config.ApiToken
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		open := path == "/healthz" || path == "/readyz" || path == "/metrics"
		if open {
			next.ServeHTTP(w, r)
			return
		}
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(auth, prefix) || subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(auth, prefix)), []byte(token)) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="g8s"`)
			writeJSONError(w, http.StatusUnauthorized, "E_UNAUTHORIZED", "missing or invalid bearer token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// writeJSONError emits the CLI-consistent error envelope.
func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"v":     1,
		"kind":  "error",
		"error": map[string]any{"code": code, "message": message},
	})
}

// corsMiddleware adds CORS headers if enabled.
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	if !s.config.EnableCORS {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowed := false
		for _, o := range s.config.AllowedOrigins {
			if o == "*" || o == origin {
				allowed = true
				break
			}
		}

		if allowed {
			w.Header().Set("Access-Control-Allow-Origin", origin)
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Max-Age", "86400")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// Start starts the HTTP server.
func (s *Server) Start() error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("server already running")
	}
	s.running = true
	s.mu.Unlock()

	// Start server in a goroutine
	go func() {
		log.Printf("Starting g8s daemon on %s", s.config.Address)
		if err := s.httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("Server error: %v", err)
		}
	}()

	return nil
}

// Stop gracefully stops the HTTP server.
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = false
	s.mu.Unlock()

	log.Println("Shutting down g8s daemon...")
	return s.httpSrv.Shutdown(ctx)
}

// Run starts the server and blocks until a shutdown signal is received.
func (s *Server) Run() error {
	if err := s.Start(); err != nil {
		return err
	}

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	return s.Stop(ctx)
}

// IsRunning returns whether the server is running.
func (s *Server) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

// handleHealthz handles the /healthz endpoint.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		apiError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"service": "g8s",
	})
}

// handleReadyz handles the /readyz endpoint.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		apiError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Check if store is accessible
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	_, err := s.store.ListTasks(ctx, controlplane.TaskFilter{Limit: 1})
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "not ready",
			"error":  err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status": "ready",
	})
}

// handleMetrics handles the /metrics endpoint (Prometheus text format).
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		apiError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	ctx := r.Context()

	// Collect supervisor metrics
	agg, err := supervisor.Aggregate(s.store, ctx, supervisor.AggregateOptions{})
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Collect system-wide metrics
	collector := supervisor.NewSystemMetricsCollector(s.store, nil)
	sysMetrics, err := collector.Collect(ctx)
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4")

	// Supervisor metrics
	fmt.Fprintf(w, "# HELP g8s_supervisor_total_runs Total number of supervisor runs\n")
	fmt.Fprintf(w, "# TYPE g8s_supervisor_total_runs counter\n")
	fmt.Fprintf(w, "g8s_supervisor_total_runs %d\n", agg.TotalRuns)

	fmt.Fprintf(w, "# HELP g8s_supervisor_first_attempt_success_rate First attempt success rate\n")
	fmt.Fprintf(w, "# TYPE g8s_supervisor_first_attempt_success_rate gauge\n")
	fmt.Fprintf(w, "g8s_supervisor_first_attempt_success_rate %.4f\n", agg.FirstAttemptSuccessRate)

	fmt.Fprintf(w, "# HELP g8s_supervisor_avg_attempts_to_success Average attempts to success\n")
	fmt.Fprintf(w, "# TYPE g8s_supervisor_avg_attempts_to_success gauge\n")
	fmt.Fprintf(w, "g8s_supervisor_avg_attempts_to_success %.4f\n", agg.AvgAttemptsToSuccess)

	fmt.Fprintf(w, "# HELP g8s_supervisor_avg_approaches_to_success Average approaches to success\n")
	fmt.Fprintf(w, "# TYPE g8s_supervisor_avg_approaches_to_success gauge\n")
	fmt.Fprintf(w, "g8s_supervisor_avg_approaches_to_success %.4f\n", agg.AvgApproachesToSuccess)

	fmt.Fprintf(w, "# HELP g8s_supervisor_escalation_rate Escalation rate\n")
	fmt.Fprintf(w, "# TYPE g8s_supervisor_escalation_rate gauge\n")
	fmt.Fprintf(w, "g8s_supervisor_escalation_rate %.4f\n", agg.EscalationRate)

	fmt.Fprintf(w, "# HELP g8s_supervisor_avg_cycle_seconds Average cycle duration in seconds\n")
	fmt.Fprintf(w, "# TYPE g8s_supervisor_avg_cycle_seconds gauge\n")
	fmt.Fprintf(w, "g8s_supervisor_avg_cycle_seconds %.4f\n", agg.AvgCycleSeconds)

	// System-wide effectiveness metrics
	fmt.Fprintf(w, "# HELP g8s_tasks_completed_total Total number of tasks completed\n")
	fmt.Fprintf(w, "# TYPE g8s_tasks_completed_total counter\n")
	fmt.Fprintf(w, "g8s_tasks_completed_total %d\n", sysMetrics.TasksCompletedTotal)

	fmt.Fprintf(w, "# HELP g8s_tasks_failed_total Total number of tasks failed\n")
	fmt.Fprintf(w, "# TYPE g8s_tasks_failed_total counter\n")
	fmt.Fprintf(w, "g8s_tasks_failed_total %d\n", sysMetrics.TasksFailedTotal)

	fmt.Fprintf(w, "# HELP g8s_tasks_cancelled_total Total number of tasks cancelled\n")
	fmt.Fprintf(w, "# TYPE g8s_tasks_cancelled_total counter\n")
	fmt.Fprintf(w, "g8s_tasks_cancelled_total %d\n", sysMetrics.TasksCancelledTotal)

	fmt.Fprintf(w, "# HELP g8s_tasks_queued_current Current number of queued tasks\n")
	fmt.Fprintf(w, "# TYPE g8s_tasks_queued_current gauge\n")
	fmt.Fprintf(w, "g8s_tasks_queued_current %d\n", sysMetrics.TasksQueuedCurrent)

	fmt.Fprintf(w, "# HELP g8s_tasks_running_current Current number of running tasks\n")
	fmt.Fprintf(w, "# TYPE g8s_tasks_running_current gauge\n")
	fmt.Fprintf(w, "g8s_tasks_running_current %d\n", sysMetrics.TasksRunningCurrent)

	fmt.Fprintf(w, "# HELP g8s_task_success_rate Task success rate (0-1)\n")
	fmt.Fprintf(w, "# TYPE g8s_task_success_rate gauge\n")
	fmt.Fprintf(w, "g8s_task_success_rate %.4f\n", sysMetrics.TaskSuccessRate)

	fmt.Fprintf(w, "# HELP g8s_task_failure_rate Task failure rate (0-1)\n")
	fmt.Fprintf(w, "# TYPE g8s_task_failure_rate gauge\n")
	fmt.Fprintf(w, "g8s_task_failure_rate %.4f\n", sysMetrics.TaskFailureRate)

	fmt.Fprintf(w, "# HELP g8s_avg_queue_latency_seconds Average queue latency in seconds\n")
	fmt.Fprintf(w, "# TYPE g8s_avg_queue_latency_seconds gauge\n")
	fmt.Fprintf(w, "g8s_avg_queue_latency_seconds %.4f\n", sysMetrics.AvgQueueLatencySeconds)

	fmt.Fprintf(w, "# HELP g8s_avg_execution_seconds Average execution time in seconds\n")
	fmt.Fprintf(w, "# TYPE g8s_avg_execution_seconds gauge\n")
	fmt.Fprintf(w, "g8s_avg_execution_seconds %.4f\n", sysMetrics.AvgExecutionSeconds)

	fmt.Fprintf(w, "# HELP g8s_active_workers Number of active workers\n")
	fmt.Fprintf(w, "# TYPE g8s_active_workers gauge\n")
	fmt.Fprintf(w, "g8s_active_workers %d\n", sysMetrics.ActiveWorkers)

	fmt.Fprintf(w, "# HELP g8s_active_sessions Number of active sessions\n")
	fmt.Fprintf(w, "# TYPE g8s_active_sessions gauge\n")
	fmt.Fprintf(w, "g8s_active_sessions %d\n", sysMetrics.ActiveSessions)
}

// handleTasks handles GET /api/v1/tasks and POST /api/v1/tasks.
func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listTasks(w, r)
	case http.MethodPost:
		s.createTask(w, r)
	default:
		apiError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// handleTaskByID handles GET /api/v1/tasks/{id}.
func (s *Server) handleTaskByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		apiError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Extract task ID from path
	path := r.URL.Path
	if len(path) <= len("/api/v1/tasks/") {
		apiError(w, http.StatusBadRequest, "Task ID required")
		return
	}
	taskID := path[len("/api/v1/tasks/"):]

	ctx := r.Context()
	task, err := s.store.GetTask(ctx, taskID)
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if task == nil {
		apiError(w, http.StatusNotFound, "Task not found")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(task)
}

// handleReceipts handles GET /api/v1/receipts.
func (s *Server) handleReceipts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		apiError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// For now, return empty list - receipts are in a separate DB
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode([]string{})
}

// handleReceiptByID handles GET /api/v1/receipts/{id}.
func (s *Server) handleReceiptByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		apiError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "not implemented"})
}

// handleSupervisor handles GET /api/v1/supervisor.
func (s *Server) handleSupervisor(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		apiError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	ctx := r.Context()
	tasks, err := s.store.ListSupervisorTasks(ctx)
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(tasks)
}

// handleSupervisorByID handles GET /api/v1/supervisor/{id}.
func (s *Server) handleSupervisorByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		apiError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	path := r.URL.Path
	if len(path) <= len("/api/v1/supervisor/") {
		apiError(w, http.StatusBadRequest, "Supervisor task ID required")
		return
	}
	taskID := path[len("/api/v1/supervisor/"):]

	ctx := r.Context()
	task, err := s.store.GetSupervisorTask(ctx, taskID)
	if err != nil {
		if errors.Is(err, controlplane.ErrUnknownSupervisorTask) {
			apiError(w, http.StatusNotFound, "Supervisor task not found")
			return
		}
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if task.ID == "" {
		apiError(w, http.StatusNotFound, "Supervisor task not found")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(task)
}

// handleSupervisorMetrics handles GET /api/v1/supervisor/metrics.
func (s *Server) handleSupervisorMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		apiError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	ctx := r.Context()
	agg, err := supervisor.Aggregate(s.store, ctx, supervisor.AggregateOptions{})
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(agg)
}

// handleSupervisorUpdateFalseEscalation handles POST /api/v1/supervisor/false-escalation/{id}.
func (s *Server) handleSupervisorUpdateFalseEscalation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		apiError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	path := r.URL.Path
	prefix := "/api/v1/supervisor/false-escalation/"
	if len(path) <= len(prefix) {
		apiError(w, http.StatusBadRequest, "Supervisor task ID required")
		return
	}
	if !strings.HasPrefix(path, prefix) {
		apiError(w, http.StatusBadRequest, "Invalid path")
		return
	}
	taskID := path[len(prefix):]

	var req struct {
		IsFalse bool `json:"is_false"`
	}
	// #358: bound request bodies (unauthenticated endpoint).
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apiError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	ctx := r.Context()
	if err := s.store.UpdateFalseEscalationRate(ctx, taskID, req.IsFalse); err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"task_id":  taskID,
		"is_false": req.IsFalse,
		"updated":  true,
	})
}

// handleBriefs handles GET /api/v1/briefs.
func (s *Server) handleBriefs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		apiError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	ctx := r.Context()
	briefs, err := s.store.ListBriefs(ctx, controlplane.BriefFilter{Limit: 100})
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(briefs)
}

// handleBriefByID handles GET /api/v1/briefs/{id}.
func (s *Server) handleBriefByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		apiError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	path := r.URL.Path
	if len(path) <= len("/api/v1/briefs/") {
		apiError(w, http.StatusBadRequest, "Brief ID required")
		return
	}
	briefID := path[len("/api/v1/briefs/"):]

	ctx := r.Context()
	brief, err := s.store.GetBrief(ctx, briefID)
	if err != nil {
		if errors.Is(err, controlplane.ErrUnknownBrief) {
			apiError(w, http.StatusNotFound, "Brief not found")
			return
		}
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if brief.ID == "" {
		apiError(w, http.StatusNotFound, "Brief not found")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(brief)
}

// listTasks handles GET /api/v1/tasks.
func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Parse query parameters
	state := r.URL.Query().Get("state")
	limitStr := r.URL.Query().Get("limit")
	limit := 50
	if limitStr != "" {
		_, _ = fmt.Sscanf(limitStr, "%d", &limit)
	}

	filter := controlplane.TaskFilter{Limit: limit}
	if state != "" {
		filter.State = &state
	}

	tasks, err := s.store.ListTasks(ctx, filter)
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"tasks": tasks,
		"count": len(tasks),
	})
}

// createTask handles POST /api/v1/tasks.
func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	// #358: bound request bodies — an unbounded Decode lets a single
	// request exhaust memory (DoS) on the unauthenticated API surface.
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	var req controlplane.SubmitTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apiError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	ctx := r.Context()
	task, err := s.store.SubmitTask(ctx, req)
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(task)
}

// handleSystemMetrics handles GET /api/v1/system/metrics.
func (s *Server) handleSystemMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		apiError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	ctx := r.Context()
	collector := supervisor.NewSystemMetricsCollector(s.store, nil)
	metrics, err := collector.Collect(ctx)
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(metrics)
}

// apiError writes the CLI-consistent JSON error envelope with a code derived
// from the HTTP status. All handlers route errors through this for a uniform
// machine-readable contract (#controlled-access upgrade).
func apiError(w http.ResponseWriter, status int, msg string) {
	code := "E_ERROR"
	switch status {
	case http.StatusBadRequest:
		code = "E_BAD_REQUEST"
	case http.StatusUnauthorized:
		code = "E_UNAUTHORIZED"
	case http.StatusNotFound:
		code = "E_NOT_FOUND"
	case http.StatusMethodNotAllowed:
		code = "E_METHOD_NOT_ALLOWED"
	case http.StatusInternalServerError:
		code = "E_INTERNAL"
	}
	writeJSONError(w, status, code, msg)
}
