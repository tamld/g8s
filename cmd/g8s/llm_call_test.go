package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func isolatedEnv(extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "G8S_TASK_ID=") ||
			strings.HasPrefix(kv, "G8S_RECEIPT_ID=") ||
			strings.HasPrefix(kv, "G8S_PERMISSION=") ||
			strings.HasPrefix(kv, "G8S_SCOPE_ROOT=") ||
			strings.HasPrefix(kv, "G8S_STATE_DIR=") ||
			strings.HasPrefix(kv, "G8S_PROVIDERS=") ||
			strings.HasPrefix(kv, "NINEROUTER_URL=") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, extra...)
}

func TestLLMCallDirect(t *testing.T) {
	binPath := buildG8sBinary(t)

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"reasoning-free"}]}`))
		case "/v1/chat/completions":
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"id": "chatcmpl-test-1",
				"model": "upstream-reasoning-free",
				"choices": [
					{
						"message": {
							"role": "assistant",
							"content": "Hello from 9router test mock!"
						}
					}
				],
				"usage": {
					"prompt_tokens": 15,
					"completion_tokens": 30,
					"total_tokens": 45
				}
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer mockServer.Close()

	cmd := exec.Command(binPath, "llm-call",
		"--provider", "9router",
		"--model", "reasoning-free",
		"--prompt", "Hello there",
		"--base-url", mockServer.URL+"/v1",
		"--json",
	)
	cmd.Env = isolatedEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("llm-call failed: %v\nOutput: %s", err, string(out))
	}

	var env struct {
		Kind string `json:"kind"`
		Data struct {
			Provider       string `json:"provider"`
			ModelRequested string `json:"model_requested"`
			ModelResolved  string `json:"model_resolved"`
			Response       string `json:"response"`
			InputTokens    int    `json:"input_tokens"`
			OutputTokens   int    `json:"output_tokens"`
			TotalTokens    int    `json:"total_tokens"`
		} `json:"data"`
	}

	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("unmarshal json response: %v\nRaw: %s", err, string(out))
	}

	if env.Kind != "llm_call" {
		t.Errorf("got kind %q, want %q", env.Kind, "llm_call")
	}
	if env.Data.Provider != "9router" {
		t.Errorf("got provider %q, want 9router", env.Data.Provider)
	}
	if env.Data.ModelRequested != "reasoning-free" {
		t.Errorf("got model_requested %q, want reasoning-free", env.Data.ModelRequested)
	}
	if env.Data.ModelResolved != "upstream-reasoning-free" {
		t.Errorf("got model_resolved %q, want upstream-reasoning-free", env.Data.ModelResolved)
	}
	if env.Data.Response != "Hello from 9router test mock!" {
		t.Errorf("got response %q", env.Data.Response)
	}
	if env.Data.InputTokens != 15 || env.Data.OutputTokens != 30 || env.Data.TotalTokens != 45 {
		t.Errorf("got tokens in=%d out=%d tot=%d, want 15/30/45", env.Data.InputTokens, env.Data.OutputTokens, env.Data.TotalTokens)
	}
}

func TestLLMCallQueueWorkerExecution(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	stateDir := filepath.Join(tempDir, "state")
	configDir := filepath.Join(tempDir, "config")
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		t.Fatalf("mkdir state: %v", err)
	}
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"fast-agent"}]}`))
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"id": "chatcmpl-queue-1",
				"model": "fast-agent-upstream",
				"choices": [
					{
						"message": {
							"role": "assistant",
							"content": "Work completed by fast-agent"
						}
					}
				],
				"usage": {
					"prompt_tokens": 20,
					"completion_tokens": 40,
					"total_tokens": 60
				}
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer mockServer.Close()

	providersPath := filepath.Join(configDir, "providers.json")
	providerJSON := fmt.Sprintf(`{
		"version": "1.0",
		"providers": [
			{
				"name": "9router",
				"class": "api_call",
				"protocol": "9router",
				"slots": 1,
				"base_url": "%s/v1",
				"models": [
					{"id": "fast-agent"}
				]
			}
		]
	}`, mockServer.URL)
	if err := os.WriteFile(providersPath, []byte(providerJSON), 0600); err != nil {
		t.Fatalf("write providers.json: %v", err)
	}

	envVars := isolatedEnv(
		"G8S_STATE_DIR="+stateDir,
		"G8S_PROVIDERS="+providersPath,
		"NINEROUTER_URL="+mockServer.URL+"/v1",
	)

	// 1. Submit task targeting 9router fast-agent
	submitCmd := exec.Command(binPath, "submit",
		"--idempotency-key", "e2e-9router-fast-1",
		"--prompt", "execute autonomous task",
		"--provider", "9router",
		"--model", "fast-agent",
		"--permission", "supplied_text",
		"--json",
	)
	submitCmd.Env = envVars
	submitOut, err := submitCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("submit failed: %v\nOutput: %s", err, string(submitOut))
	}

	var submitEnv struct {
		Data struct {
			TaskID string `json:"task_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(submitOut, &submitEnv); err != nil {
		t.Fatalf("unmarshal submit output: %v\nOutput: %s", err, string(submitOut))
	}
	taskID := submitEnv.Data.TaskID
	if taskID == "" {
		t.Fatalf("empty task_id in submit output: %s", string(submitOut))
	}

	// 2. Run worker --once
	workerCmd := exec.Command(binPath, "worker", "--once", "--json")
	workerCmd.Env = envVars
	workerOut, err := workerCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("worker failed: %v\nOutput: %s", err, string(workerOut))
	}

	// 3. Inspect task via g8s get
	getCmd := exec.Command(binPath, "get", taskID, "--json")
	getCmd.Env = envVars
	getOut, err := getCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("get failed: %v\nOutput: %s", err, string(getOut))
	}

	var getEnv struct {
		Data struct {
			TaskID string `json:"task_id"`
			State  string `json:"state"`
			Result struct {
				Response string `json:"response"`
				Usage    struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
					TotalTokens  int `json:"total_tokens"`
				} `json:"usage"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(getOut, &getEnv); err != nil {
		t.Fatalf("unmarshal get output: %v\nOutput: %s", err, string(getOut))
	}

	if getEnv.Data.State != "WORKER_COMPLETED" {
		t.Errorf("task state = %q, want WORKER_COMPLETED (worker output: %s)", getEnv.Data.State, string(workerOut))
	}
	if getEnv.Data.Result.Response != "Work completed by fast-agent" {
		t.Errorf("task response = %q, want 'Work completed by fast-agent'", getEnv.Data.Result.Response)
	}
	u := getEnv.Data.Result.Usage
	if u.InputTokens != 20 || u.OutputTokens != 40 || u.TotalTokens != 60 {
		t.Errorf("tokens in=%d out=%d tot=%d, want 20/40/60", u.InputTokens, u.OutputTokens, u.TotalTokens)
	}
}

func TestProvidersDiscoverLiveModels(t *testing.T) {
	binPath := buildG8sBinary(t)

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"data": [
					{"id": "reasoning-free"},
					{"id": "reasoning-high"},
					{"id": "fast-agent"},
					{"id": "deep-coding"}
				]
			}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer mockServer.Close()

	tempDir := t.TempDir()
	providersPath := filepath.Join(tempDir, "providers.json")
	providerJSON := fmt.Sprintf(`{
		"version": "1.0",
		"providers": [
			{
				"name": "9router",
				"class": "api_call",
				"protocol": "9router",
				"base_url": "%s/v1",
				"base_url_env": "NINEROUTER_URL",
				"allow_unlisted_models": true
			}
		]
	}`, mockServer.URL)
	if err := os.WriteFile(providersPath, []byte(providerJSON), 0600); err != nil {
		t.Fatalf("write providers.json: %v", err)
	}

	cmd := exec.Command(binPath, "providers", "discover",
		"--provider", "9router",
		"--json",
	)
	cmd.Env = isolatedEnv(
		"G8S_PROVIDERS="+providersPath,
		"NINEROUTER_URL="+mockServer.URL+"/v1",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("providers discover failed: %v\nOutput: %s", err, string(out))
	}

	var env struct {
		Kind string `json:"kind"`
		Data struct {
			Provider string   `json:"provider"`
			Models   []string `json:"models"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("unmarshal json: %v\nRaw: %s", err, string(out))
	}

	if env.Kind != "models_discovery" {
		t.Errorf("got kind %q, want models_discovery", env.Kind)
	}
	if env.Data.Provider != "9router" {
		t.Errorf("got provider %q, want 9router", env.Data.Provider)
	}
	expected := []string{"deep-coding", "fast-agent", "reasoning-free", "reasoning-high"}
	if len(env.Data.Models) != len(expected) {
		t.Fatalf("got models %v, want %v", env.Data.Models, expected)
	}
	for i, m := range expected {
		if env.Data.Models[i] != m {
			t.Errorf("model[%d] = %q, want %q", i, env.Data.Models[i], m)
		}
	}
}

func TestLLMCallApplyFiles(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"reasoning-free"}]}`))
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			respContent := "Here is the deliverable:\n```file:gen/result.txt\nline 1\nline 2\n```\nDone."
			payload := map[string]any{
				"id":    "chatcmpl-apply-1",
				"model": "reasoning-free",
				"choices": []map[string]any{
					{"message": map[string]string{"role": "assistant", "content": respContent}},
				},
				"usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 20, "total_tokens": 30},
			}
			b, _ := json.Marshal(payload)
			_, _ = w.Write(b)
		default:
			http.NotFound(w, r)
		}
	}))
	defer mockServer.Close()

	cmd := exec.Command(binPath, "llm-call",
		"--provider", "9router",
		"--model", "reasoning-free",
		"--prompt", "Generate code",
		"--base-url", mockServer.URL+"/v1",
		"--apply-files",
		"--json",
	)
	cmd.Dir = tempDir
	cmd.Env = isolatedEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("llm-call --apply-files failed: %v\nOutput: %s", err, string(out))
	}

	targetFile := filepath.Join(tempDir, "gen", "result.txt")
	content, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatalf("read applied file: %v", err)
	}
	expected := "line 1\nline 2\n"
	if string(content) != expected {
		t.Errorf("got file content %q, want %q", string(content), expected)
	}
}
