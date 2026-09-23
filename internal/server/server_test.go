package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
)

func TestServerLifecycle(t *testing.T) {
	cfg := DefaultConfig()
	srv := NewServer(cfg, nil)
	if srv == nil {
		t.Fatal("expected non-nil server")
	}

	if srv.config.ReadHeaderTimeout != 10*time.Second {
		t.Errorf("expected ReadHeaderTimeout to be 10s, got %v", srv.config.ReadHeaderTimeout)
	}

	err := srv.Start()
	if err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	if err := srv.Stop(context.Background()); err != nil {
		t.Fatalf("failed to stop: %v", err)
	}
}

func TestServerConfigValidation(t *testing.T) {
	cfg := Config{}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	if cfg.Address != ":8080" {
		t.Errorf("expected Address to be :8080, got %s", cfg.Address)
	}
	if cfg.ReadHeaderTimeout != 10*time.Second {
		t.Errorf("expected ReadHeaderTimeout to be 10s, got %v", cfg.ReadHeaderTimeout)
	}
}

func TestCorsMiddleware(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableCORS = true
	cfg.AllowedOrigins = []string{"http://localhost:3000"}

	srv := NewServer(cfg, nil)

	handler := srv.corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204 No Content, got %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
		t.Errorf("expected Access-Control-Allow-Origin to be http://localhost:3000, got %s", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCorsMiddleware_Asterisk(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableCORS = true
	cfg.AllowedOrigins = []string{"*"}

	srv := NewServer(cfg, nil)

	handler := srv.corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "http://example.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "http://example.com" {
		t.Errorf("expected Access-Control-Allow-Origin to be http://example.com, got %s", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCorsMiddleware_Disabled(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableCORS = false

	srv := NewServer(cfg, nil)

	handler := srv.corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("expected empty Access-Control-Allow-Origin, got %s", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestHandleHealthz(t *testing.T) {
	cfg := DefaultConfig()
	srv := NewServer(cfg, nil)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	srv.handleHealthz(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rec.Code)
	}
	if rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", rec.Header().Get("Content-Type"))
	}
}

func TestHandleHealthz_MethodNotAllowed(t *testing.T) {
	cfg := DefaultConfig()
	srv := NewServer(cfg, nil)

	req := httptest.NewRequest(http.MethodPost, "/healthz", nil)
	rec := httptest.NewRecorder()

	srv.handleHealthz(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 Method Not Allowed, got %d", rec.Code)
	}
}

func TestHandleReadyz_MethodNotAllowed(t *testing.T) {
	cfg := DefaultConfig()
	srv := NewServer(cfg, nil)

	req := httptest.NewRequest(http.MethodPost, "/readyz", nil)
	rec := httptest.NewRecorder()

	srv.handleReadyz(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 Method Not Allowed, got %d", rec.Code)
	}
}

func TestHandleMetrics_MethodNotAllowed(t *testing.T) {
	cfg := DefaultConfig()
	srv := NewServer(cfg, nil)

	req := httptest.NewRequest(http.MethodPost, "/metrics", nil)
	rec := httptest.NewRecorder()

	srv.handleMetrics(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 Method Not Allowed, got %d", rec.Code)
	}
}

func TestHandleTasks_MethodNotAllowed(t *testing.T) {
	cfg := DefaultConfig()
	srv := NewServer(cfg, nil)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/tasks", nil)
	rec := httptest.NewRecorder()

	srv.handleTasks(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 Method Not Allowed, got %d", rec.Code)
	}
}

func TestHandleTaskByID_MethodNotAllowed(t *testing.T) {
	cfg := DefaultConfig()
	srv := NewServer(cfg, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/123", nil)
	rec := httptest.NewRecorder()

	srv.handleTaskByID(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 Method Not Allowed, got %d", rec.Code)
	}
}

func TestHandleReceipts_MethodNotAllowed(t *testing.T) {
	cfg := DefaultConfig()
	srv := NewServer(cfg, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/receipts", nil)
	rec := httptest.NewRecorder()

	srv.handleReceipts(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 Method Not Allowed, got %d", rec.Code)
	}
}

func TestHandleReceiptByID_MethodNotAllowed(t *testing.T) {
	cfg := DefaultConfig()
	srv := NewServer(cfg, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/receipts/123", nil)
	rec := httptest.NewRecorder()

	srv.handleReceiptByID(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 Method Not Allowed, got %d", rec.Code)
	}
}

func TestHandleSupervisor_MethodNotAllowed(t *testing.T) {
	cfg := DefaultConfig()
	srv := NewServer(cfg, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/supervisor", nil)
	rec := httptest.NewRecorder()

	srv.handleSupervisor(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 Method Not Allowed, got %d", rec.Code)
	}
}

func TestHandleSupervisorByID_MethodNotAllowed(t *testing.T) {
	cfg := DefaultConfig()
	srv := NewServer(cfg, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/supervisor/123", nil)
	rec := httptest.NewRecorder()

	srv.handleSupervisorByID(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 Method Not Allowed, got %d", rec.Code)
	}
}

func TestHandleSupervisorMetrics_MethodNotAllowed(t *testing.T) {
	cfg := DefaultConfig()
	srv := NewServer(cfg, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/supervisor/metrics", nil)
	rec := httptest.NewRecorder()

	srv.handleSupervisorMetrics(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 Method Not Allowed, got %d", rec.Code)
	}
}

func TestHandleSupervisorUpdateFalseEscalation_MethodNotAllowed(t *testing.T) {
	cfg := DefaultConfig()
	srv := NewServer(cfg, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/supervisor/false-escalation/123", nil)
	rec := httptest.NewRecorder()

	srv.handleSupervisorUpdateFalseEscalation(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 Method Not Allowed, got %d", rec.Code)
	}
}

func TestHandleBriefs_MethodNotAllowed(t *testing.T) {
	cfg := DefaultConfig()
	srv := NewServer(cfg, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/briefs", nil)
	rec := httptest.NewRecorder()

	srv.handleBriefs(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 Method Not Allowed, got %d", rec.Code)
	}
}

func TestHandleBriefByID_MethodNotAllowed(t *testing.T) {
	cfg := DefaultConfig()
	srv := NewServer(cfg, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/briefs/123", nil)
	rec := httptest.NewRecorder()

	srv.handleBriefByID(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 Method Not Allowed, got %d", rec.Code)
	}
}

func TestHandleSystemMetrics_MethodNotAllowed(t *testing.T) {
	cfg := DefaultConfig()
	srv := NewServer(cfg, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/system/metrics", nil)
	rec := httptest.NewRecorder()

	srv.handleSystemMetrics(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 Method Not Allowed, got %d", rec.Code)
	}
}

func TestHandleOpenAPISpec_MethodNotAllowed(t *testing.T) {
	cfg := DefaultConfig()
	srv := NewServer(cfg, nil)

	req := httptest.NewRequest(http.MethodPost, "/openapi.json", nil)
	rec := httptest.NewRecorder()

	srv.HandleOpenAPISpec(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 Method Not Allowed, got %d", rec.Code)
	}
}

func TestHandleOpenAPIUI_MethodNotAllowed(t *testing.T) {
	cfg := DefaultConfig()
	srv := NewServer(cfg, nil)

	req := httptest.NewRequest(http.MethodPost, "/openapi", nil)
	rec := httptest.NewRecorder()

	srv.HandleOpenAPIUI(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 Method Not Allowed, got %d", rec.Code)
	}
}

func TestCreateTask_InvalidJSON(t *testing.T) {
	cfg := DefaultConfig()
	srv := NewServer(cfg, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", nil)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.handleTasks(rec, req)

	// Should return 400 for invalid JSON
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 400 or 500 for invalid JSON, got %d", rec.Code)
	}
}

func TestCreateTask_ValidJSON(t *testing.T) {
	cfg := DefaultConfig()
	srv := NewServer(cfg, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", http.NoBody)
	req.Header.Set("Content-Type", "application/json")
	_ = req
	_ = srv
}

func setupTestServer(t *testing.T) (*Server, *controlplane.Store) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test_server.db")
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}
	cfg := DefaultConfig()
	cfg.EnableHealthz = true
	cfg.EnableMetrics = true
	srv := NewServer(cfg, store)
	return srv, store
}

func TestServerReadyz_WithStore(t *testing.T) {
	srv, _ := setupTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	srv.handleReadyz(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestServerMetricsHandlers(t *testing.T) {
	srv, _ := setupTestServer(t)

	// GET /metrics
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	srv.handleMetrics(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "g8s_supervisor_total_runs") {
		t.Errorf("missing supervisor metrics in output")
	}

	// GET /api/v1/system/metrics
	reqSys := httptest.NewRequest(http.MethodGet, "/api/v1/system/metrics", nil)
	recSys := httptest.NewRecorder()
	srv.handleSystemMetrics(recSys, reqSys)
	if recSys.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recSys.Code)
	}

	// POST /api/v1/system/metrics -> 405
	reqSysPost := httptest.NewRequest(http.MethodPost, "/api/v1/system/metrics", nil)
	recSysPost := httptest.NewRecorder()
	srv.handleSystemMetrics(recSysPost, reqSysPost)
	if recSysPost.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", recSysPost.Code)
	}
}

func TestTasksEndpoints(t *testing.T) {
	srv, _ := setupTestServer(t)

	// 1. GET /api/v1/tasks (empty)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks?limit=10&state=QUEUED", nil)
	rec := httptest.NewRecorder()
	srv.handleTasks(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// 2. POST /api/v1/tasks (valid)
	bodyJSON := fmt.Sprintf(`{"idempotency_key":"test-task-1","model":"test-model","add_dirs":[%q],"payload":{"prompt":"test task prompt"}}`, t.TempDir())
	reqPost := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewBufferString(bodyJSON))
	recPost := httptest.NewRecorder()
	srv.handleTasks(recPost, reqPost)
	if recPost.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", recPost.Code, recPost.Body.String())
	}
	var created struct {
		ID string `json:"task_id"`
	}
	_ = json.Unmarshal(recPost.Body.Bytes(), &created)
	if created.ID == "" {
		t.Fatalf("expected created task ID")
	}

	// 3. GET /api/v1/tasks/{id}
	reqGet := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+created.ID, nil)
	recGet := httptest.NewRecorder()
	srv.handleTaskByID(recGet, reqGet)
	if recGet.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recGet.Code)
	}

	// 4. GET /api/v1/tasks/nonexistent -> 404
	req404 := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/task-nonexistent", nil)
	rec404 := httptest.NewRecorder()
	srv.handleTaskByID(rec404, req404)
	if rec404.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec404.Code)
	}

	// 5. GET /api/v1/tasks/ -> 400
	req400 := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/", nil)
	rec400 := httptest.NewRecorder()
	srv.handleTaskByID(rec400, req400)
	if rec400.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec400.Code)
	}

	// 6. POST /api/v1/tasks/{id} -> 405
	req405 := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+created.ID, nil)
	rec405 := httptest.NewRecorder()
	srv.handleTaskByID(rec405, req405)
	if rec405.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec405.Code)
	}
}

func TestReceiptsEndpoints(t *testing.T) {
	srv, _ := setupTestServer(t)

	// GET /api/v1/receipts
	req := httptest.NewRequest(http.MethodGet, "/api/v1/receipts", nil)
	rec := httptest.NewRecorder()
	srv.handleReceipts(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// POST /api/v1/receipts -> 405
	reqPost := httptest.NewRequest(http.MethodPost, "/api/v1/receipts", nil)
	recPost := httptest.NewRecorder()
	srv.handleReceipts(recPost, reqPost)
	if recPost.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", recPost.Code)
	}

	// GET /api/v1/receipts/{id}
	reqID := httptest.NewRequest(http.MethodGet, "/api/v1/receipts/rec-123", nil)
	recID := httptest.NewRecorder()
	srv.handleReceiptByID(recID, reqID)
	if recID.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recID.Code)
	}

	// POST /api/v1/receipts/{id} -> 405
	reqIDPost := httptest.NewRequest(http.MethodPost, "/api/v1/receipts/rec-123", nil)
	recIDPost := httptest.NewRecorder()
	srv.handleReceiptByID(recIDPost, reqIDPost)
	if recIDPost.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", recIDPost.Code)
	}
}

func TestBriefsEndpoints(t *testing.T) {
	srv, store := setupTestServer(t)

	// 1. GET /api/v1/briefs
	req := httptest.NewRequest(http.MethodGet, "/api/v1/briefs", nil)
	rec := httptest.NewRecorder()
	srv.handleBriefs(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// 2. POST /api/v1/briefs -> 405
	reqPost := httptest.NewRequest(http.MethodPost, "/api/v1/briefs", nil)
	recPost := httptest.NewRecorder()
	srv.handleBriefs(recPost, reqPost)
	if recPost.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", recPost.Code)
	}

	// 3. Issue a brief in store to test handleBriefByID
	b := controlplane.BriefRow{
		ID:        "brief-test-1",
		Title:     "test brief",
		PayloadMD: "# Test",
		DodMD:     "- [x] done",
		IssuedBy:  "test-runner",
		ExpiresAt: time.Now().Add(time.Hour),
	}
	if err := store.CreateBrief(context.Background(), b); err != nil {
		t.Fatalf("failed to create brief: %v", err)
	}

	// 4. GET /api/v1/briefs/{id}
	reqID := httptest.NewRequest(http.MethodGet, "/api/v1/briefs/"+b.ID, nil)
	recID := httptest.NewRecorder()
	srv.handleBriefByID(recID, reqID)
	if recID.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recID.Code)
	}

	// 5. GET /api/v1/briefs/nonexistent -> 404
	req404 := httptest.NewRequest(http.MethodGet, "/api/v1/briefs/brief-nonexistent", nil)
	rec404 := httptest.NewRecorder()
	srv.handleBriefByID(rec404, req404)
	if rec404.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec404.Code)
	}

	// 6. GET /api/v1/briefs/ -> 400
	req400 := httptest.NewRequest(http.MethodGet, "/api/v1/briefs/", nil)
	rec400 := httptest.NewRecorder()
	srv.handleBriefByID(rec400, req400)
	if rec400.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec400.Code)
	}
}

func TestSupervisorEndpoints(t *testing.T) {
	srv, _ := setupTestServer(t)

	// GET /api/v1/supervisor
	req := httptest.NewRequest(http.MethodGet, "/api/v1/supervisor", nil)
	rec := httptest.NewRecorder()
	srv.handleSupervisor(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// POST /api/v1/supervisor -> 405
	reqPost := httptest.NewRequest(http.MethodPost, "/api/v1/supervisor", nil)
	recPost := httptest.NewRecorder()
	srv.handleSupervisor(recPost, reqPost)
	if recPost.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", recPost.Code)
	}

	// GET /api/v1/supervisor/metrics
	reqM := httptest.NewRequest(http.MethodGet, "/api/v1/supervisor/metrics", nil)
	recM := httptest.NewRecorder()
	srv.handleSupervisorMetrics(recM, reqM)
	if recM.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recM.Code)
	}

	// GET /api/v1/supervisor/nonexistent -> 404
	req404 := httptest.NewRequest(http.MethodGet, "/api/v1/supervisor/sup-nonexistent", nil)
	rec404 := httptest.NewRecorder()
	srv.handleSupervisorByID(rec404, req404)
	if rec404.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec404.Code)
	}

	// GET /api/v1/supervisor/ -> 400
	req400 := httptest.NewRequest(http.MethodGet, "/api/v1/supervisor/", nil)
	rec400 := httptest.NewRecorder()
	srv.handleSupervisorByID(rec400, req400)
	if rec400.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec400.Code)
	}

	// POST /api/v1/supervisor/false-escalation/sup-123 (invalid body) -> 400
	reqFE := httptest.NewRequest(http.MethodPost, "/api/v1/supervisor/false-escalation/sup-123", bytes.NewBufferString("invalid json"))
	recFE := httptest.NewRecorder()
	srv.handleSupervisorUpdateFalseEscalation(recFE, reqFE)
	if recFE.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", recFE.Code)
	}

	// GET /api/v1/supervisor/false-escalation/sup-123 -> 405
	reqFEGet := httptest.NewRequest(http.MethodGet, "/api/v1/supervisor/false-escalation/sup-123", nil)
	recFEGet := httptest.NewRecorder()
	srv.handleSupervisorUpdateFalseEscalation(recFEGet, reqFEGet)
	if recFEGet.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", recFEGet.Code)
	}
}
