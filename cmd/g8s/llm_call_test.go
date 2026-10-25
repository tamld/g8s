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
	"sync"
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
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatalf("mkdir state: %v", err)
	}
	if err := os.MkdirAll(configDir, 0o700); err != nil {
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
	if err := os.WriteFile(providersPath, []byte(providerJSON), 0o600); err != nil {
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
	if err := os.WriteFile(providersPath, []byte(providerJSON), 0o600); err != nil {
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

func TestLLMCallContextFiles(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	ctxFile := filepath.Join(tempDir, "notes.md")
	if err := os.WriteFile(ctxFile, []byte("hello-context-payload\n"), 0o600); err != nil {
		t.Fatalf("write notes.md: %v", err)
	}

	var receivedPrompt string
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"fast-agent"}]}`))
		case "/v1/chat/completions":
			var reqBody struct {
				Messages []struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"messages"`
			}
			_ = json.NewDecoder(r.Body).Decode(&reqBody)
			for _, m := range reqBody.Messages {
				if m.Role == "user" {
					receivedPrompt = m.Content
				}
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"chatcmpl-ctx","model":"fast-agent","choices":[{"message":{"role":"assistant","content":"VERIFIED"}}],"usage":{"prompt_tokens":15,"completion_tokens":5,"total_tokens":20}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer mockServer.Close()

	cmd := exec.Command(binPath, "llm-call",
		"--provider", "9router",
		"--model", "fast-agent",
		"--prompt", "Summarize notes",
		"--base-url", mockServer.URL+"/v1",
		"--scope-root", tempDir,
		"--context-files", "notes.md",
		"--json",
	)
	cmd.Dir = tempDir
	cmd.Env = isolatedEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("llm-call --context-files failed: %v\nOutput: %s", err, string(out))
	}
	if !strings.Contains(receivedPrompt, "--- FILE: notes.md") || !strings.Contains(receivedPrompt, "hello-context-payload") {
		t.Errorf("expected hydrated file content in prompt, got %q", receivedPrompt)
	}

	// Out-of-scope file must be rejected
	outsideCmd := exec.Command(binPath, "llm-call",
		"--provider", "9router",
		"--model", "fast-agent",
		"--prompt", "Summarize",
		"--base-url", mockServer.URL+"/v1",
		"--scope-root", tempDir,
		"--context-files", "../outside.txt",
		"--json",
	)
	outsideCmd.Dir = tempDir
	outsideCmd.Env = isolatedEnv()
	if _, err := outsideCmd.CombinedOutput(); err == nil {
		t.Fatalf("expected out-of-scope --context-files to fail")
	}
}

func TestLLMCallCLIProxyAPIAndDefaultModel(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()

	var mu sync.Mutex
	var requestedModel string
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"gemini-2.5-pro"}]}`))
		case "/v1/chat/completions":
			var reqBody struct {
				Model string `json:"model"`
			}
			_ = json.NewDecoder(r.Body).Decode(&reqBody)
			mu.Lock()
			requestedModel = reqBody.Model
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"chatcmpl-cliproxy","model":"gemini-2.5-pro","choices":[{"message":{"role":"assistant","content":"CLIPROXY_OK"}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer mockServer.Close()

	// 1. Zero-config cliproxyapi with CLIPROXYAPI_URL env
	cmdZero := exec.Command(binPath, "llm-call",
		"--provider", "cliproxyapi",
		"--model", "gemini-2.5-pro",
		"--prompt", "Ping cliproxyapi",
		"--json",
	)
	cmdZero.Env = isolatedEnv("CLIPROXYAPI_URL=" + mockServer.URL + "/v1")
	outZero, err := cmdZero.CombinedOutput()
	if err != nil {
		t.Fatalf("zero-config cliproxyapi failed: %v\nOutput: %s", err, string(outZero))
	}
	if !strings.Contains(string(outZero), "CLIPROXY_OK") {
		t.Errorf("expected CLIPROXY_OK in output, got %s", string(outZero))
	}

	// 2. Custom provider in providers.json using default_model without --model flag
	providersPath := filepath.Join(tempDir, "providers.json")
	providerJSON := fmt.Sprintf(`{
		"providers": [
			{
				"name": "cliproxyapi",
				"class": "api_call",
				"protocol": "openai",
				"slots": 2,
				"base_url": "%s/v1",
				"default_model": "gemini-2.5-pro",
				"allow_unlisted_models": true
			}
		]
	}`, mockServer.URL)
	if err := os.WriteFile(providersPath, []byte(providerJSON), 0o600); err != nil {
		t.Fatalf("write providers.json: %v", err)
	}

	mu.Lock()
	requestedModel = ""
	mu.Unlock()
	cmdDef := exec.Command(binPath, "llm-call",
		"--provider", "cliproxyapi",
		"--prompt", "Ping via default_model",
		"--json",
	)
	cmdDef.Env = isolatedEnv("G8S_PROVIDERS=" + providersPath)
	outDef, err := cmdDef.CombinedOutput()
	if err != nil {
		t.Fatalf("default_model resolution failed: %v\nOutput: %s", err, string(outDef))
	}
	mu.Lock()
	gotModel := requestedModel
	mu.Unlock()
	if gotModel != "gemini-2.5-pro" {
		t.Errorf("expected requestedModel gemini-2.5-pro from default_model, got %q", gotModel)
	}
}

func TestLLMCallBoundedAgenticLoop(t *testing.T) {
	binPath := buildG8sBinary(t)
	scopeDir := t.TempDir()

	turnCount := 0
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"fast-agent"}]}`))
		case "/v1/chat/completions":
			turnCount++
			w.Header().Set("Content-Type", "application/json")
			if turnCount == 1 {
				_, _ = w.Write([]byte(`{
					"id": "turn-1",
					"model": "fast-agent",
					"choices": [{
						"message": {
							"role": "assistant",
							"content": "Here is draft 1:\n` + "```file:status.txt\\nBROKEN\\n```" + `"
						}
					}],
					"usage": {"prompt_tokens": 10, "completion_tokens": 10, "total_tokens": 20}
				}`))
				return
			}
			_, _ = w.Write([]byte(`{
				"id": "turn-2",
				"model": "fast-agent",
				"choices": [{
					"message": {
						"role": "assistant",
						"content": "Fixed after verification failure:\n` + "```file:status.txt\\nVERIFIED_GREEN\\n```" + `"
					}
				}],
				"usage": {"prompt_tokens": 25, "completion_tokens": 15, "total_tokens": 40}
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer mockServer.Close()

	cmd := exec.Command(binPath, "llm-call",
		"--provider", "9router",
		"--model", "fast-agent",
		"--prompt", "Write VERIFIED_GREEN to status.txt",
		"--base-url", mockServer.URL+"/v1",
		"--scope-root", scopeDir,
		"--apply-files",
		"--max-turns", "3",
		"--verify-cmd", "grep -q VERIFIED_GREEN status.txt",
		"--json",
	)
	cmd.Env = isolatedEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bounded agentic loop failed: %v\nOutput: %s", err, string(out))
	}

	var env struct {
		Data struct {
			TurnsUsed    int      `json:"turns_used"`
			VerifyPassed bool     `json:"verify_passed"`
			TotalTokens  int      `json:"total_tokens"`
			AppliedFiles []string `json:"applied_files"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("unmarshal json: %v\nRaw: %s", err, string(out))
	}
	if env.Data.TurnsUsed != 2 {
		t.Errorf("turns_used = %d, want 2", env.Data.TurnsUsed)
	}
	if !env.Data.VerifyPassed {
		t.Errorf("verify_passed = false, want true")
	}
	if env.Data.TotalTokens != 60 {
		t.Errorf("total_tokens = %d, want 60 (20 + 40)", env.Data.TotalTokens)
	}
	content, rErr := os.ReadFile(filepath.Join(scopeDir, "status.txt"))
	if rErr != nil {
		t.Fatalf("read status.txt: %v", rErr)
	}
	if strings.TrimSpace(string(content)) != "VERIFIED_GREEN" {
		t.Errorf("status.txt content = %q, want VERIFIED_GREEN", string(content))
	}
}

func TestNormalizeVerificationErrorSignature(t *testing.T) {
	errRun1 := "2026-10-24T21:00:01Z FAIL\tgithub.com/tamld/g8s/pkg\t0.015s panic at 0xc00012a400 in /tmp/go-build111/b001"
	errRun2 := "2026-10-24T21:05:59Z FAIL\tgithub.com/tamld/g8s/pkg\t1.842s panic at 0xc00099f880 in /tmp/go-build999/b001"
	sig1 := ErrorSignatureHash(errRun1)
	sig2 := ErrorSignatureHash(errRun2)
	if sig1 != sig2 {
		t.Fatalf("expected identical normalized error signature across runs, got %s vs %s", sig1, sig2)
	}
}

func TestLLMCallOscillationBreakerAndAtomicRollback(t *testing.T) {
	binPath := buildG8sBinary(t)
	scopeDir := t.TempDir()

	// Pre-existing file that should be restored to its original content on failed loop
	existingFile := filepath.Join(scopeDir, "state.txt")
	if err := os.WriteFile(existingFile, []byte("ORIGINAL_CLEAN\n"), 0o600); err != nil {
		t.Fatalf("write initial state.txt: %v", err)
	}

	turnCount := 0
	var turn3Prompt string
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"fast-agent"}]}`))
		case "/v1/chat/completions":
			turnCount++
			var reqBody struct {
				Messages []struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"messages"`
			}
			_ = json.NewDecoder(r.Body).Decode(&reqBody)
			if turnCount == 3 {
				for _, m := range reqBody.Messages {
					if m.Role == "user" {
						turn3Prompt = m.Content
					}
				}
			}

			w.Header().Set("Content-Type", "application/json")
			var content string
			switch turnCount {
			case 1:
				// State A + extra poison file
				content = "Turn 1:\n```file:state.txt\nSTATE_A\n```\n```file:poison.txt\nBROKEN_RESIDUE\n```"
			case 2:
				// State B (different state, different error)
				content = "Turn 2:\n```file:state.txt\nSTATE_B\n```"
			default:
				// Turn 3 oscillates back to State A + poison.txt (A -> B -> A cycle!)
				content = "Turn 3:\n```file:state.txt\nSTATE_A\n```\n```file:poison.txt\nBROKEN_RESIDUE\n```"
			}
			payload := map[string]any{
				"id":    fmt.Sprintf("turn-%d", turnCount),
				"model": "fast-agent",
				"choices": []map[string]any{
					{"message": map[string]string{"role": "assistant", "content": content}},
				},
				"usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 10, "total_tokens": 20},
			}
			b, _ := json.Marshal(payload)
			_, _ = w.Write(b)
		default:
			http.NotFound(w, r)
		}
	}))
	defer mockServer.Close()

	// verify-cmd prints state.txt content and exits 1 unless it equals VERIFIED_GREEN
	cmd := exec.Command(binPath, "llm-call",
		"--provider", "9router",
		"--model", "fast-agent",
		"--prompt", "Update state.txt",
		"--base-url", mockServer.URL+"/v1",
		"--scope-root", scopeDir,
		"--apply-files",
		"--max-turns", "5",
		"--verify-cmd", "cat state.txt && grep -q VERIFIED_GREEN state.txt",
		"--json",
	)
	cmd.Env = isolatedEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("llm-call failed: %v\nOutput: %s", err, string(out))
	}

	var env struct {
		Data struct {
			TurnsUsed          int    `json:"turns_used"`
			VerifyPassed       bool   `json:"verify_passed"`
			StagnationDetected bool   `json:"stagnation_detected"`
			StagnationReason   string `json:"stagnation_reason"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("unmarshal json: %v\nRaw: %s", err, string(out))
	}

	// Must abort on Turn 3 (A -> B -> A oscillation) instead of running Turns 4 and 5
	if env.Data.TurnsUsed != 3 {
		t.Errorf("turns_used = %d, want 3 (early abort on A->B->A cycle)", env.Data.TurnsUsed)
	}
	if !env.Data.StagnationDetected {
		t.Errorf("expected stagnation_detected = true, got false (reason: %q)", env.Data.StagnationReason)
	}
	// Turn 3 prompt must include the cumulative Falsified Attempts Ledger containing BOTH Turn 1 and Turn 2
	if !strings.Contains(turn3Prompt, "FALSIFIED ATTEMPTS LEDGER") ||
		!strings.Contains(turn3Prompt, "Turn 1/5") ||
		!strings.Contains(turn3Prompt, "Turn 2/5") {
		t.Errorf("expected Turn 3 prompt to contain cumulative ledger for Turns 1 & 2, got:\n%s", turn3Prompt)
	}

	// Verify atomic rollback: state.txt restored to ORIGINAL_CLEAN and poison.txt removed!
	restored, err := os.ReadFile(existingFile)
	if err != nil {
		t.Fatalf("read restored state.txt: %v", err)
	}
	if string(restored) != "ORIGINAL_CLEAN\n" {
		t.Errorf("state.txt after failed loop = %q, want ORIGINAL_CLEAN", string(restored))
	}
	if _, statErr := os.Stat(filepath.Join(scopeDir, "poison.txt")); !os.IsNotExist(statErr) {
		t.Errorf("expected poison.txt from failed turn to be rolled back and removed, got statErr=%v", statErr)
	}
}

func TestLLMCallVerifyCmdSecretScrubbingAndProtectedPaths(t *testing.T) {
	binPath := buildG8sBinary(t)
	scopeDir := t.TempDir()

	turnCount := 0
	var turn2Prompt string
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"fast-agent"}]}`))
		case "/v1/chat/completions":
			turnCount++
			var reqBody struct {
				Messages []struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"messages"`
			}
			_ = json.NewDecoder(r.Body).Decode(&reqBody)
			if turnCount == 2 {
				for _, m := range reqBody.Messages {
					if m.Role == "user" {
						turn2Prompt = m.Content
					}
				}
			}
			w.Header().Set("Content-Type", "application/json")
			content := fmt.Sprintf("Turn %d:\n```file:app.txt\ntry-%d\n```", turnCount, turnCount)
			payload := map[string]any{
				"id":    fmt.Sprintf("sec-turn-%d", turnCount),
				"model": "fast-agent",
				"choices": []map[string]any{
					{"message": map[string]string{"role": "assistant", "content": content}},
				},
				"usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 10, "total_tokens": 20},
			}
			b, _ := json.Marshal(payload)
			_, _ = w.Write(b)
		default:
			http.NotFound(w, r)
		}
	}))
	defer mockServer.Close()

	secretKey := "sk-live-super-secret-exfil-999999"
	secretTok := "g8s-serve-token-secret-888888"

	cmd := exec.Command(binPath, "llm-call",
		"--provider", "9router",
		"--model", "fast-agent",
		"--prompt", "Write app.txt",
		"--base-url", mockServer.URL+"/v1",
		"--scope-root", scopeDir,
		"--apply-files",
		"--max-turns", "2",
		"--verify-cmd", "echo EXFIL_KEY=$OPENAI_API_KEY EXFIL_TOK=$G8S_SERVE_TOKEN && exit 1",
		"--json",
	)
	cmd.Env = isolatedEnv(
		"OPENAI_API_KEY="+secretKey,
		"G8S_SERVE_TOKEN="+secretTok,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("llm-call failed: %v\nOutput: %s", err, string(out))
	}

	if strings.Contains(turn2Prompt, secretKey) || strings.Contains(turn2Prompt, secretTok) {
		t.Fatalf("SECURITY FAILURE: verify-cmd leaked environment secrets into Turn 2 LLM prompt:\n%s", turn2Prompt)
	}

	// Verify that attempting to write to .git/hooks/pre-commit is denied
	gitHookServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/models" {
			_, _ = w.Write([]byte(`{"data":[{"id":"fast-agent"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"git-hook","model":"fast-agent","choices":[{"message":{"role":"assistant","content":"` + "```file:.git/hooks/pre-commit\\n#!/bin/sh\\npwned\\n```" + `"}}],"usage":{"prompt_tokens":5,"completion_tokens":5,"total_tokens":10}}`))
	}))
	defer gitHookServer.Close()

	hookCmd := exec.Command(binPath, "llm-call",
		"--provider", "9router",
		"--model", "fast-agent",
		"--prompt", "Write hook",
		"--base-url", gitHookServer.URL+"/v1",
		"--scope-root", scopeDir,
		"--apply-files",
		"--json",
	)
	hookCmd.Env = isolatedEnv()
	hookOut, hookErr := hookCmd.CombinedOutput()
	if hookErr == nil {
		t.Fatalf("expected .git/hooks/pre-commit write to fail, but succeeded: %s", string(hookOut))
	}
	if !strings.Contains(string(hookOut), "protected git metadata") {
		t.Errorf("expected protected git metadata error, got: %s", string(hookOut))
	}
}
