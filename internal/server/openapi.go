// Package server implements the g8s daemon mode with HTTP API server.
package server

import (
	"encoding/json"
	"net/http"
)

// OpenAPISpec returns the OpenAPI 3.0 specification for the g8s API.
func OpenAPISpec() map[string]any {
	return map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":       "g8s API",
			"version":     "0.3.0",
			"description": "The Gatekeepers (g8s) - Zero-Trust Process & Capability Harness for AI Agents",
		},
		"servers": []map[string]any{
			{"url": "http://localhost:8080", "description": "Development server"},
		},
		"paths": map[string]any{
			"/healthz": map[string]any{
				"get": map[string]any{
					"summary":     "Health check",
					"description": "Returns the health status of the service",
					"operationId": "healthz",
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Service is healthy",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"type": "object",
										"properties": map[string]any{
											"status":  map[string]any{"type": "string", "example": "ok"},
											"service": map[string]any{"type": "string", "example": "g8s"},
										},
									},
								},
							},
						},
					},
				},
			},
			"/readyz": map[string]any{
				"get": map[string]any{
					"summary":     "Readiness check",
					"description": "Returns the readiness status of the service",
					"operationId": "readyz",
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Service is ready",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"type": "object",
										"properties": map[string]any{
											"status": map[string]any{"type": "string", "example": "ready"},
										},
									},
								},
							},
						},
						"503": map[string]any{
							"description": "Service is not ready",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"type": "object",
										"properties": map[string]any{
											"status": map[string]any{"type": "string", "example": "not ready"},
											"error":  map[string]any{"type": "string"},
										},
									},
								},
							},
						},
					},
				},
			},
			"/metrics": map[string]any{
				"get": map[string]any{
					"summary":     "Prometheus metrics",
					"description": "Returns metrics in Prometheus text format",
					"operationId": "metrics",
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Metrics in Prometheus text format",
							"content": map[string]any{
								"text/plain": map[string]any{
									"schema": map[string]any{
										"type": "string",
									},
								},
							},
						},
					},
				},
			},
			"/api/v1/tasks": map[string]any{
				"get": map[string]any{
					"summary":     "List tasks",
					"description": "Returns a list of tasks with optional filtering",
					"operationId": "listTasks",
					"parameters": []map[string]any{
						{"name": "state", "in": "query", "schema": map[string]any{"type": "string"}, "description": "Filter by task state"},
						{"name": "limit", "in": "query", "schema": map[string]any{"type": "integer", "default": 50}, "description": "Maximum number of tasks to return"},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "List of tasks",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"type": "object",
										"properties": map[string]any{
											"tasks": map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/Task"}},
											"count": map[string]any{"type": "integer"},
										},
									},
								},
							},
						},
					},
				},
				"post": map[string]any{
					"summary":     "Create a new task",
					"description": "Submits a new task to the queue",
					"operationId": "createTask",
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{"$ref": "#/components/schemas/SubmitTaskRequest"},
							},
						},
					},
					"responses": map[string]any{
						"201": map[string]any{
							"description": "Task created",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/Task"},
								},
							},
						},
						"400": map[string]any{"description": "Invalid request"},
						"500": map[string]any{"description": "Internal server error"},
					},
				},
			},
			"/api/v1/tasks/{id}": map[string]any{
				"get": map[string]any{
					"summary":     "Get task by ID",
					"description": "Returns a single task by its ID",
					"operationId": "getTask",
					"parameters": []map[string]any{
						{"name": "id", "in": "path", "required": true, "schema": map[string]any{"type": "string"}, "description": "Task ID"},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Task found",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/Task"},
								},
							},
						},
						"404": map[string]any{"description": "Task not found"},
					},
				},
			},
			"/api/v1/receipts": map[string]any{
				"get": map[string]any{
					"summary":     "List receipts",
					"description": "Returns a list of write delegation receipts",
					"operationId": "listReceipts",
					"responses": map[string]any{
						"200": map[string]any{
							"description": "List of receipts",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/Receipt"}},
								},
							},
						},
					},
				},
			},
			"/api/v1/receipts/{id}": map[string]any{
				"get": map[string]any{
					"summary":     "Get receipt by ID",
					"description": "Returns a single receipt by its ID",
					"operationId": "getReceipt",
					"parameters": []map[string]any{
						{"name": "id", "in": "path", "required": true, "schema": map[string]any{"type": "string"}, "description": "Receipt ID"},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Receipt found",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/Receipt"},
								},
							},
						},
						"404": map[string]any{"description": "Receipt not found"},
					},
				},
			},
			"/api/v1/supervisor": map[string]any{
				"get": map[string]any{
					"summary":     "List supervisor tasks",
					"description": "Returns a list of supervisor tasks",
					"operationId": "listSupervisorTasks",
					"responses": map[string]any{
						"200": map[string]any{
							"description": "List of supervisor tasks",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/SupervisorTask"}},
								},
							},
						},
					},
				},
			},
			"/api/v1/supervisor/{id}": map[string]any{
				"get": map[string]any{
					"summary":     "Get supervisor task by ID",
					"description": "Returns a single supervisor task by its ID",
					"operationId": "getSupervisorTask",
					"parameters": []map[string]any{
						{"name": "id", "in": "path", "required": true, "schema": map[string]any{"type": "string"}, "description": "Supervisor task ID"},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Supervisor task found",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/SupervisorTask"},
								},
							},
						},
						"404": map[string]any{"description": "Supervisor task not found"},
					},
				},
			},
			"/api/v1/supervisor/metrics": map[string]any{
				"get": map[string]any{
					"summary":     "Get supervisor aggregate metrics",
					"description": "Returns aggregated supervisor telemetry metrics",
					"operationId": "getSupervisorMetrics",
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Aggregate metrics",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/AggregateMetrics"},
								},
							},
						},
					},
				},
			},
			"/api/v1/supervisor/false-escalation/{id}": map[string]any{
				"post": map[string]any{
					"summary":     "Update false escalation rate",
					"description": "Marks a supervisor task escalation as false (the task should have succeeded without escalation)",
					"operationId": "updateSupervisorFalseEscalation",
					"parameters": []map[string]any{
						{"name": "id", "in": "path", "required": true, "schema": map[string]any{"type": "string"}, "description": "Supervisor task ID"},
					},
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"type":     "object",
									"required": []string{"is_false"},
									"properties": map[string]any{
										"is_false": map[string]any{"type": "boolean", "description": "Whether the escalation was false"},
									},
								},
							},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "False escalation rate updated",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"type": "object",
										"properties": map[string]any{
											"task_id":  map[string]any{"type": "string"},
											"is_false": map[string]any{"type": "boolean"},
											"updated":  map[string]any{"type": "boolean"},
										},
									},
								},
							},
						},
						"400": map[string]any{"description": "Invalid request"},
						"404": map[string]any{"description": "Supervisor task not found"},
						"500": map[string]any{"description": "Internal server error"},
					},
				},
			},
			"/api/v1/system/metrics": map[string]any{
				"get": map[string]any{
					"summary":     "Get system-wide effectiveness metrics",
					"description": "Returns aggregated system-wide effectiveness metrics including task throughput, latency, success rates, worker utilization, and session metrics",
					"operationId": "getSystemMetrics",
					"responses": map[string]any{
						"200": map[string]any{
							"description": "System-wide metrics",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/SystemMetrics"},
								},
							},
						},
					},
				},
			},
			"/api/v1/briefs": map[string]any{
				"get": map[string]any{
					"summary":     "List briefs",
					"description": "Returns a list of structured task briefs",
					"operationId": "listBriefs",
					"parameters": []map[string]any{
						{"name": "status", "in": "query", "schema": map[string]any{"type": "string"}, "description": "Filter by brief status"},
						{"name": "limit", "in": "query", "schema": map[string]any{"type": "integer", "default": 100}, "description": "Maximum number of briefs to return"},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "List of briefs",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/Brief"}},
								},
							},
						},
					},
				},
			},
			"/api/v1/briefs/{id}": map[string]any{
				"get": map[string]any{
					"summary":     "Get brief by ID",
					"description": "Returns a single brief by its ID",
					"operationId": "getBrief",
					"parameters": []map[string]any{
						{"name": "id", "in": "path", "required": true, "schema": map[string]any{"type": "string"}, "description": "Brief ID"},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Brief found",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/Brief"},
								},
							},
						},
						"404": map[string]any{"description": "Brief not found"},
					},
				},
			},
		},
		"components": map[string]any{
			"schemas": map[string]any{
				"Task": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"task_id":          map[string]any{"type": "string"},
						"parent_task_id":   map[string]any{"type": "string", "nullable": true},
						"idempotency_key":  map[string]any{"type": "string"},
						"schema_version":   map[string]any{"type": "string"},
						"state":            map[string]any{"type": "string"},
						"priority":         map[string]any{"type": "integer"},
						"request":          map[string]any{"type": "object"},
						"request_hash":     map[string]any{"type": "string"},
						"result":           map[string]any{"type": "object", "nullable": true},
						"result_hash":      map[string]any{"type": "string", "nullable": true},
						"receipt_hash":     map[string]any{"type": "string", "nullable": true},
						"attempts":         map[string]any{"type": "integer"},
						"max_attempts":     map[string]any{"type": "integer"},
						"lease_owner":      map[string]any{"type": "string", "nullable": true},
						"lease_token":      map[string]any{"type": "string", "nullable": true},
						"lease_expires_at": map[string]any{"type": "number", "nullable": true},
						"cancel_requested": map[string]any{"type": "boolean"},
						"created_at":       map[string]any{"type": "number"},
						"updated_at":       map[string]any{"type": "number"},
						"completed_at":     map[string]any{"type": "number", "nullable": true},
						"last_error":       map[string]any{"type": "string", "nullable": true},
						"orchestrator_id":  map[string]any{"type": "string", "nullable": true},
						"worktree_id":      map[string]any{"type": "string", "nullable": true},
						"worker_name":      map[string]any{"type": "string", "nullable": true},
						"iter":             map[string]any{"type": "integer"},
					},
				},
				"SubmitTaskRequest": map[string]any{
					"type":     "object",
					"required": []string{"idempotency_key"},
					"properties": map[string]any{
						"idempotency_key":  map[string]any{"type": "string"},
						"priority":         map[string]any{"type": "integer"},
						"max_attempts":     map[string]any{"type": "integer"},
						"parent_task_id":   map[string]any{"type": "string", "nullable": true},
						"payload":          map[string]any{"type": "object"},
						"role":             map[string]any{"type": "string"},
						"permission":       map[string]any{"type": "string"},
						"model":            map[string]any{"type": "string"},
						"timeout":          map[string]any{"type": "string"},
						"add_dirs":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						"skip_permissions": map[string]any{"type": "boolean"},
						"no_sandbox":       map[string]any{"type": "boolean"},
					},
				},
				"Receipt": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"receipt_id":       map[string]any{"type": "string"},
						"issuer":           map[string]any{"type": "string"},
						"allowed_paths":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						"expires_at":       map[string]any{"type": "string", "format": "date-time"},
						"consumed":         map[string]any{"type": "boolean"},
						"consumer_task_id": map[string]any{"type": "string", "nullable": true},
						"created_at":       map[string]any{"type": "string", "format": "date-time"},
					},
				},
				"SupervisorTask": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":             map[string]any{"type": "string"},
						"state":          map[string]any{"type": "string"},
						"envelope_json":  map[string]any{"type": "string"},
						"approach_idx":   map[string]any{"type": "integer"},
						"attempt_idx":    map[string]any{"type": "integer"},
						"parent_task_id": map[string]any{"type": "string", "nullable": true},
						"created_at":     map[string]any{"type": "string", "format": "date-time"},
						"updated_at":     map[string]any{"type": "string", "format": "date-time"},
					},
				},
				"AggregateMetrics": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"total_runs":                 map[string]any{"type": "integer"},
						"first_attempt_success_rate": map[string]any{"type": "number"},
						"avg_attempts_to_success":    map[string]any{"type": "number"},
						"avg_approaches_to_success":  map[string]any{"type": "number"},
						"escalation_rate":            map[string]any{"type": "number"},
						"avg_cycle_duration_seconds": map[string]any{"type": "number"},
					},
				},
				"SystemMetrics": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"tasks_completed_total":                 map[string]any{"type": "integer", "format": "int64"},
						"tasks_failed_total":                    map[string]any{"type": "integer", "format": "int64"},
						"tasks_cancelled_total":                 map[string]any{"type": "integer", "format": "int64"},
						"tasks_queued_current":                  map[string]any{"type": "integer"},
						"tasks_running_current":                 map[string]any{"type": "integer"},
						"avg_queue_latency_seconds":             map[string]any{"type": "number"},
						"avg_execution_seconds":                 map[string]any{"type": "number"},
						"task_success_rate":                     map[string]any{"type": "number"},
						"task_failure_rate":                     map[string]any{"type": "number"},
						"task_cancellation_rate":                map[string]any{"type": "number"},
						"active_workers":                        map[string]any{"type": "integer"},
						"worker_utilization":                    map[string]any{"type": "number"},
						"active_sessions":                       map[string]any{"type": "integer"},
						"tasks_per_session":                     map[string]any{"type": "number"},
						"supervisor_total_runs":                 map[string]any{"type": "integer"},
						"supervisor_first_attempt_success_rate": map[string]any{"type": "number"},
						"supervisor_avg_attempts_to_success":    map[string]any{"type": "number"},
						"supervisor_avg_approaches_to_success":  map[string]any{"type": "number"},
						"supervisor_escalation_rate":            map[string]any{"type": "number"},
						"supervisor_avg_cycle_seconds":          map[string]any{"type": "number"},
						"collected_at":                          map[string]any{"type": "string", "format": "date-time"},
					},
				},
				"Brief": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":         map[string]any{"type": "string"},
						"title":      map[string]any{"type": "string"},
						"payload_md": map[string]any{"type": "string"},
						"dod_md":     map[string]any{"type": "string"},
						"issued_by":  map[string]any{"type": "string"},
						"issued_at":  map[string]any{"type": "string", "format": "date-time"},
						"expires_at": map[string]any{"type": "string", "format": "date-time"},
						"status":     map[string]any{"type": "string"},
					},
				},
				"Error": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"error": map[string]any{"type": "string"},
					},
				},
			},
		},
	}
}

// HandleOpenAPISpec handles GET /openapi.json.
func (s *Server) HandleOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(OpenAPISpec())
}

// HandleOpenAPIUI handles GET /openapi (serves a simple HTML page with Swagger UI).
func (s *Server) HandleOpenAPIUI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	html := `<!DOCTYPE html>
<html>
<head>
    <title>g8s API Documentation</title>
    <link rel="stylesheet" type="text/css" href="https://unpkg.com/swagger-ui-dist@5.9.0/swagger-ui.css" />
    <style>
        html, body { margin: 0; padding: 0; height: 100%; }
        #swagger-ui { height: 100%; }
    </style>
</head>
<body>
    <div id="swagger-ui"></div>
    <script src="https://unpkg.com/swagger-ui-dist@5.9.0/swagger-ui-bundle.js"></script>
    <script>
        window.onload = function() {
            SwaggerUIBundle({
                url: '/openapi.json',
                dom_id: '#swagger-ui',
                deepLinking: true,
                presets: [SwaggerUIBundle.presets.apis],
                layout: "BaseLayout"
            });
        };
    </script>
</body>
</html>`
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(html))
}
