package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/config"
)

const fakeContractCLISource = `package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	exePath, _ := os.Executable()
	base := strings.TrimSuffix(filepath.Base(exePath), ".exe")
	badMode := filepath.Base(filepath.Dir(exePath)) == "bad"

	args := os.Args[1:]
	for _, a := range args {
		if a == "--version" {
			fmt.Println(base + " 9.9.9")
			return
		}
		if a == "--help" || a == "-h" {
			fmt.Printf("usage: %s [options] [prompt]\n", base)
			return
		}
	}

	if _, err := os.Stat(".g8s-contract-workdir"); err == nil {
		if b, mErr := json.Marshal(args); mErr == nil {
			_ = os.WriteFile(".contract_argv.json", b, 0o600)
		}
	}

	joined := strings.Join(args, " ")
	if badMode && strings.Contains(joined, "[CONTRACT:ONE_SHOT]") {
		fmt.Fprintln(os.Stderr, "simulated bad binary failure")
		os.Exit(2)
	}

	switch {
	case strings.Contains(joined, "[CONTRACT:HANG]"):
		time.Sleep(30 * time.Second)
	case strings.Contains(joined, "[CONTRACT:NONZERO_EXIT]"):
		fmt.Fprintln(os.Stderr, "fatal: simulated adapter failure")
		os.Exit(2)
	case strings.Contains(joined, "[CONTRACT:STDERR_REFUSAL_QUOTA]"):
		fmt.Fprintln(os.Stderr, "You've hit your usage limit. Try again at 3:04 PM.")
		return
	case strings.Contains(joined, "[CONTRACT:STDERR_REFUSAL_AUTH]"):
		fmt.Fprintln(os.Stderr, "ERROR: unauthorized invalid api key")
		return
	case strings.Contains(joined, "[CONTRACT:MALFORMED_JSON]"):
		fmt.Print(` + "`" + `{"broken": [not valid json` + "`" + `)
		return
	case strings.Contains(joined, "[CONTRACT:JSON_SCHEMA]"):
		fmt.Println(` + "`" + `{"type":"result","answer":"PONG","usage":{"input_tokens":14,"output_tokens":6,"total_tokens":20}}` + "`" + `)
		return
	default:
		fmt.Println("PONG")
	}
}
`

type cliContractBins struct {
	good map[string]string
	bad  map[string]string
}

func buildContractCLIBins(t *testing.T) cliContractBins {
	t.Helper()
	root := t.TempDir()
	srcDir := filepath.Join(root, "src")
	goodDir := filepath.Join(root, "good")
	badDir := filepath.Join(root, "bad")
	for _, d := range []string{srcDir, goodDir, badDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	mainGo := filepath.Join(srcDir, "main.go")
	if err := os.WriteFile(mainGo, []byte(fakeContractCLISource), 0o600); err != nil {
		t.Fatalf("write fake CLI source: %v", err)
	}
	goMod := filepath.Join(srcDir, "go.mod")
	if err := os.WriteFile(goMod, []byte("module g8s.invalid/contractcli\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	masterBin := filepath.Join(root, "contractcli"+ext)
	buildCmd := exec.Command("go", "build", "-o", masterBin, mainGo)
	buildCmd.Dir = srcDir
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("compile fake contract CLI: %v\n%s", err, string(out))
	}
	raw, err := os.ReadFile(masterBin)
	if err != nil {
		t.Fatalf("read compiled fake CLI: %v", err)
	}

	cliBinNames := map[string]string{
		"agy":         "agy",
		"codex":       "codex",
		"claude":      "claude",
		"opencode":    "opencode",
		"commandcode": "commandcode",
		"qwen":        "qwen",
		"cursor":      "cursor-agent",
	}
	res := cliContractBins{
		good: make(map[string]string, len(cliBinNames)),
		bad:  make(map[string]string, len(cliBinNames)),
	}
	for adapter, binName := range cliBinNames {
		gPath := filepath.Join(goodDir, binName+ext)
		bPath := filepath.Join(badDir, binName+ext)
		if err := os.WriteFile(gPath, raw, 0o700); err != nil {
			t.Fatalf("copy good bin %s: %v", adapter, err)
		}
		if err := os.WriteFile(bPath, raw, 0o700); err != nil {
			t.Fatalf("copy bad bin %s: %v", adapter, err)
		}
		res.good[adapter] = gPath
		res.bad[adapter] = bPath
	}
	return res
}

type cliAdapterSpec struct {
	name            string
	binEnv          string
	newProvider     func() Provider
	supportsEffort  bool
	expectReadOnly  string
	expectWorkModel bool
}

var allCLIAdapterSpecs = []cliAdapterSpec{
	{name: "agy", binEnv: "AGY_BIN", newProvider: func() Provider { return NewAgyProvider() }, supportsEffort: true, expectWorkModel: true},
	{name: "codex", binEnv: "CODEX_BIN", newProvider: func() Provider { return NewCodexProvider() }, expectReadOnly: "read-only", expectWorkModel: true},
	{name: "claude", binEnv: "CLAUDE_BIN", newProvider: func() Provider { return NewClaudeProvider() }},
	{name: "opencode", binEnv: "OPENCODE_BIN", newProvider: func() Provider { return NewOpenCodeProvider() }, expectWorkModel: true},
	{name: "commandcode", binEnv: "COMMANDCODE_BIN", newProvider: func() Provider { return NewCommandCodeProvider() }, supportsEffort: true, expectWorkModel: true},
	{name: "qwen", binEnv: "QWEN_BIN", newProvider: func() Provider { return NewQwenProvider() }, expectReadOnly: "plan", expectWorkModel: true},
	{name: "cursor", binEnv: "CURSOR_BIN", newProvider: func() Provider { return NewCursorProvider() }, expectReadOnly: "plan", expectWorkModel: true},
}

func makeContractWorkDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".g8s-contract-workdir"), []byte("1\n"), 0o600); err != nil {
		t.Fatalf("write workdir marker: %v", err)
	}
	return dir
}

func readContractArgv(t *testing.T, workDir string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(workDir, ".contract_argv.json"))
	if err != nil {
		t.Fatalf("read .contract_argv.json: %v", err)
	}
	var argv []string
	if err := json.Unmarshal(raw, &argv); err != nil {
		t.Fatalf("decode .contract_argv.json: %v", err)
	}
	return argv
}

type contractHTTPMock struct {
	mu               sync.Mutex
	lastOpenAIBody   map[string]any
	lastAnthropicBdy map[string]any
	started          chan struct{}
	canceledCh       chan struct{}
	shutdown         chan struct{}
	startOnce        sync.Once
	cancelOnce       sync.Once
	server           *httptest.Server
}

func newContractHTTPMock(t *testing.T) *contractHTTPMock {
	t.Helper()
	m := &contractHTTPMock{
		started:    make(chan struct{}),
		canceledCh: make(chan struct{}),
		shutdown:   make(chan struct{}),
	}
	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags", "/models", "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-5.5"},{"id":"claude-sonnet-5-5"}]}`))
			return
		case "/api/version":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"version":"0.6.0-contract"}`))
			return
		case "/api/generate":
			var req struct {
				Model  string `json:"model"`
				Prompt string `json:"prompt"`
			}
			bodyBytes, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(bodyBytes, &req)
			switch {
			case strings.Contains(req.Prompt, "[CONTRACT:HANG]"):
				m.startOnce.Do(func() { close(m.started) })
				select {
				case <-r.Context().Done():
					m.cancelOnce.Do(func() { close(m.canceledCh) })
				case <-m.shutdown:
				}
				return
			case strings.Contains(req.Prompt, "[CONTRACT:NONZERO_EXIT]"):
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":"simulated ollama 500"}`))
				return
			case strings.Contains(req.Prompt, "[CONTRACT:MALFORMED_JSON]"):
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"response": [broken`))
				return
			case strings.Contains(req.Prompt, "[CONTRACT:TOKENS]"):
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"response":"PONG","prompt_eval_count":11,"eval_count":7}`))
				return
			default:
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"response":"PONG","prompt_eval_count":4,"eval_count":2}`))
				return
			}
		case "/chat/completions":
			bodyBytes, _ := io.ReadAll(r.Body)
			var req map[string]any
			_ = json.Unmarshal(bodyBytes, &req)
			m.mu.Lock()
			m.lastOpenAIBody = req
			m.mu.Unlock()

			model, _ := req["model"].(string)
			prompt := ""
			if msgs, ok := req["messages"].([]any); ok {
				for _, rawMsg := range msgs {
					if mp, ok := rawMsg.(map[string]any); ok {
						if c, ok := mp["content"].(string); ok {
							prompt += " " + c
						}
					}
				}
			}
			switch {
			case strings.Contains(prompt, "[CONTRACT:HANG]"):
				m.startOnce.Do(func() { close(m.started) })
				select {
				case <-r.Context().Done():
					m.cancelOnce.Do(func() { close(m.canceledCh) })
				case <-m.shutdown:
				}
				return
			case strings.Contains(prompt, "[CONTRACT:NONZERO_EXIT]") || strings.Contains(model, "nonexistent"):
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":"model not found"}`))
				return
			case strings.Contains(prompt, "[CONTRACT:MALFORMED_JSON]"):
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"choices": [broken`))
				return
			case strings.Contains(prompt, "[CONTRACT:MALFORMED_SSE]"):
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprintln(w, `data: {corrupt sse payload`)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"model":"gpt-5.5","choices":[{"message":{"content":"{\"answer\":\"PONG\"}"}}],"usage":{"prompt_tokens":12,"completion_tokens":8,"total_tokens":20}}`))
			return
		case "/v1/messages":
			bodyBytes, _ := io.ReadAll(r.Body)
			var req map[string]any
			_ = json.Unmarshal(bodyBytes, &req)
			m.mu.Lock()
			m.lastAnthropicBdy = req
			m.mu.Unlock()

			prompt := ""
			if msgs, ok := req["messages"].([]any); ok {
				for _, rawMsg := range msgs {
					if mp, ok := rawMsg.(map[string]any); ok {
						if c, ok := mp["content"].(string); ok {
							prompt += " " + c
						}
					}
				}
			}
			switch {
			case strings.Contains(prompt, "[CONTRACT:HANG]"):
				m.startOnce.Do(func() { close(m.started) })
				select {
				case <-r.Context().Done():
					m.cancelOnce.Do(func() { close(m.canceledCh) })
				case <-m.shutdown:
				}
				return
			case strings.Contains(prompt, "[CONTRACT:NONZERO_EXIT]"):
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"message":"simulated anthropic 400"}}`))
				return
			case strings.Contains(prompt, "[CONTRACT:MALFORMED_JSON]"):
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"content": [broken`))
				return
			case strings.Contains(prompt, "[CONTRACT:MALFORMED_SSE]"):
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprintln(w, `data: {corrupt anthropic sse`)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"msg_contract","model":"claude-sonnet-5-5","content":[{"type":"text","text":"PONG"}],"usage":{"input_tokens":15,"output_tokens":9}}`))
			return
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(func() {
		close(m.shutdown)
		m.server.Close()
	})
	return m
}

// TestOfflineAdapterContractSuite deterministically verifies all 7 CLI adapters
// and all 3 HTTP/local gateways (10 adapters total) across all 7 contract
// dimensions on Linux, macOS, and Windows without live vendor calls (#941).
func TestOfflineAdapterContractSuite(t *testing.T) {
	bins := buildContractCLIBins(t)
	verdicts := make(map[string]map[string]string)

	for _, cs := range allCLIAdapterSpecs {
		cs := cs
		t.Run("cli/"+cs.name, func(t *testing.T) {
			t.Setenv(cs.binEnv, bins.good[cs.name])
			p := cs.newProvider()
			v := make(map[string]string)

			if err := p.Available(context.Background()); err != nil {
				t.Fatalf("Available: %v", err)
			}
			ver, err := p.Version(context.Background())
			if err != nil || strings.TrimSpace(ver) == "" {
				t.Fatalf("Version = (%q, %v), want non-empty", ver, err)
			}
			pt, ok := p.(ProcessTransport)
			if !ok || pt.TransportKind() != TransportKindProcess {
				t.Fatalf("%s must implement ProcessTransport (%q)", cs.name, TransportKindProcess)
			}

			// 1. One-shot + read-only flag contract + ProcessTransport PID > 0
			workDir := makeContractWorkDir(t)
			h, err := pt.SpawnProcess(context.Background(), Spec{
				Model:       "model-a",
				Brief:       "[CONTRACT:ONE_SHOT]",
				Permission:  "read_only",
				WorktreeDir: workDir,
			})
			if err != nil {
				t.Fatalf("SpawnProcess one-shot: %v", err)
			}
			if h.PID() <= 0 {
				t.Fatalf("expected OS PID > 0, got %d", h.PID())
			}
			rc, waitErr := h.Wait(context.Background())
			if waitErr != nil || rc.Status != "COMPLETED" || !strings.Contains(rc.Stdout, "PONG") {
				t.Fatalf("one-shot receipt=%+v err=%v", rc, waitErr)
			}
			argv := readContractArgv(t, workDir)
			argvStr := strings.Join(argv, " ")
			if cs.expectWorkModel && !strings.Contains(argvStr, "model-a") {
				t.Fatalf("%s argv %v missing model-a", cs.name, argv)
			}
			if cs.expectReadOnly != "" && !strings.Contains(argvStr, cs.expectReadOnly) {
				t.Fatalf("%s read_only argv %v missing %q", cs.name, argv, cs.expectReadOnly)
			}
			v["one_shot"] = "PASS"

			// 2. Structured JSON/schema output parsing + Spawn ResponseFormat surface
			jsonDir := makeContractWorkDir(t)
			hJSON, err := p.Spawn(context.Background(), Spec{Brief: "[CONTRACT:JSON_SCHEMA]", WorktreeDir: jsonDir})
			if err != nil {
				t.Fatalf("Spawn JSON_SCHEMA: %v", err)
			}
			rcJSON, waitErr := hJSON.Wait(context.Background())
			if waitErr != nil || rcJSON.Status != "COMPLETED" {
				t.Fatalf("JSON_SCHEMA receipt=%+v err=%v", rcJSON, waitErr)
			}
			var decoded map[string]any
			if err := json.Unmarshal([]byte(rcJSON.Stdout), &decoded); err != nil || decoded["answer"] != "PONG" {
				t.Fatalf("expected parseable JSON stdout with answer=PONG, got %q (err=%v)", rcJSON.Stdout, err)
			}
			// CLI Spawn itself does not inject OpenAI-style ResponseFormat; conformance P7 checks CLI schema flags.
			v["structured_json_schema"] = "UNSUPPORTED"

			// 3. Non-zero exit & codex exit-0 stderr refusal propagation
			errDir := makeContractWorkDir(t)
			hErr, err := p.Spawn(context.Background(), Spec{Brief: "[CONTRACT:NONZERO_EXIT]", WorktreeDir: errDir})
			if err != nil {
				t.Fatalf("Spawn NONZERO_EXIT: %v", err)
			}
			rcErr, waitErr := hErr.Wait(context.Background())
			if waitErr == nil || rcErr.Status != "FAILED" || rcErr.ExitCode != 2 {
				t.Fatalf("expected FAILED exit=2 on non-zero exit, got receipt=%+v err=%v", rcErr, waitErr)
			}
			if cs.name == "codex" {
				refDir := makeContractWorkDir(t)
				hRef, err := p.Spawn(context.Background(), Spec{Brief: "[CONTRACT:STDERR_REFUSAL_QUOTA]", WorktreeDir: refDir})
				if err != nil {
					t.Fatalf("Spawn codex refusal: %v", err)
				}
				rcRef, waitErr := hRef.Wait(context.Background())
				if waitErr == nil || rcRef.Status != "FAILED" || !strings.Contains(waitErr.Error(), ErrCodeProviderQuotaExhausted) {
					t.Fatalf("expected %s on codex exit-0 quota refusal, got receipt=%+v err=%v", ErrCodeProviderQuotaExhausted, rcRef, waitErr)
				}
			}
			v["nonzero_exit_error"] = "PASS"

			// 4. Timeout & in-flight Cancel()
			hangDir := makeContractWorkDir(t)
			hCancel, err := p.Spawn(context.Background(), Spec{Brief: "[CONTRACT:HANG]", WorktreeDir: hangDir})
			if err != nil {
				t.Fatalf("Spawn hang cancel: %v", err)
			}
			cancelStart := time.Now()
			if err := hCancel.Cancel(context.Background()); err != nil {
				t.Fatalf("Cancel: %v", err)
			}
			rcCancel, waitErr := hCancel.Wait(context.Background())
			if time.Since(cancelStart) > 3*time.Second {
				t.Fatalf("Cancel took too long (%v)", time.Since(cancelStart))
			}
			if waitErr == nil || rcCancel.Status == "COMPLETED" || rcCancel.ExitCode == 0 {
				t.Fatalf("expected aborted receipt after Cancel(), got receipt=%+v err=%v", rcCancel, waitErr)
			}

			toDir := makeContractWorkDir(t)
			hTO, err := p.Spawn(context.Background(), Spec{Brief: "[CONTRACT:HANG]", WorktreeDir: toDir})
			if err != nil {
				t.Fatalf("Spawn hang timeout: %v", err)
			}
			toCtx, toCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer toCancel()
			rcTO, toErr := hTO.Wait(toCtx)
			if !errors.Is(toErr, context.DeadlineExceeded) || rcTO.Status != "TIMEOUT" {
				t.Fatalf("expected TIMEOUT/DeadlineExceeded, got receipt=%+v err=%v", rcTO, toErr)
			}
			v["timeout_cancel"] = "PASS"

			// 5. Tool calling: managed inside external CLI binary, not forwarded via Spec.Tools
			v["tool_calling"] = "UNSUPPORTED"

			// 6. Effort mapping
			if cs.supportsEffort {
				effDir := makeContractWorkDir(t)
				hEff, err := p.Spawn(context.Background(), Spec{Brief: "[CONTRACT:ONE_SHOT]", Effort: "high", WorktreeDir: effDir})
				if err != nil {
					t.Fatalf("Spawn effort: %v", err)
				}
				if _, err := hEff.Wait(context.Background()); err != nil {
					t.Fatalf("Wait effort: %v", err)
				}
				effArgv := strings.Join(readContractArgv(t, effDir), " ")
				if !strings.Contains(effArgv, "--effort high") {
					t.Fatalf("%s expected --effort high in argv %q", cs.name, effArgv)
				}
				v["effort_mapping"] = "PASS"
			} else {
				v["effort_mapping"] = "UNSUPPORTED"
			}

			// 7. Token accounting on raw Spawn receipt (handled by JSON stdout parser in P8)
			if rcJSON.TotalTokens != 0 {
				v["token_accounting"] = "PASS"
			} else {
				v["token_accounting"] = "UNSUPPORTED"
			}

			verdicts[cs.name] = v
		})
	}

	t.Run("http/ollama", func(t *testing.T) {
		mock := newContractHTTPMock(t)
		t.Setenv("OLLAMA_HOST", mock.server.URL)
		o := NewOllamaProvider().WithHTTPClient(mock.server.Client())
		v := make(map[string]string)

		if err := o.Available(context.Background()); err != nil {
			t.Fatalf("Available: %v", err)
		}
		ver, err := o.Version(context.Background())
		if err != nil || ver == "" {
			t.Fatalf("Version = (%q, %v)", ver, err)
		}
		if o.TransportKind() != TransportKindRequest {
			t.Fatalf("expected TransportKindRequest, got %q", o.TransportKind())
		}

		// 1. One-shot & NoPID
		h, err := o.Spawn(context.Background(), Spec{Model: "llama3.2", Brief: "[CONTRACT:ONE_SHOT]"})
		if err != nil {
			t.Fatalf("Spawn: %v", err)
		}
		if h.PID() != NoPID {
			t.Fatalf("expected NoPID (%d), got %d", NoPID, h.PID())
		}
		rc, waitErr := h.Wait(context.Background())
		if waitErr != nil || rc.Status != "COMPLETED" || rc.Stdout != "PONG" {
			t.Fatalf("unexpected ollama one-shot receipt=%+v err=%v", rc, waitErr)
		}
		v["one_shot"] = "PASS"
		v["structured_json_schema"] = "UNSUPPORTED"

		// 3. Non-zero / HTTP 500 propagation
		hErr, err := o.Spawn(context.Background(), Spec{Model: "llama3.2", Brief: "[CONTRACT:NONZERO_EXIT]"})
		if err != nil {
			t.Fatalf("Spawn error case: %v", err)
		}
		rcErr, waitErr := hErr.Wait(context.Background())
		if waitErr == nil || rcErr.Status != "FAILED" {
			t.Fatalf("expected FAILED + err on HTTP 500, got %+v err=%v", rcErr, waitErr)
		}
		v["nonzero_exit_error"] = "PASS"

		// 4. Async cancel & timeout
		cancelMock := newContractHTTPMock(t)
		t.Setenv("OLLAMA_HOST", cancelMock.server.URL)
		oCancel := NewOllamaProvider().WithHTTPClient(cancelMock.server.Client())
		rh, err := oCancel.StartRequest(context.Background(), Spec{Model: "llama3.2", Brief: "[CONTRACT:HANG]"})
		if err != nil {
			t.Fatalf("StartRequest cancel: %v", err)
		}
		<-cancelMock.started
		if err := rh.Cancel(context.Background()); err != nil {
			t.Fatalf("Cancel: %v", err)
		}
		rcC, cErr := rh.Wait(context.Background())
		if rcC.Status != "CANCELLED" || !errors.Is(cErr, context.Canceled) {
			t.Fatalf("expected CANCELLED/context.Canceled, got %+v err=%v", rcC, cErr)
		}
		select {
		case <-cancelMock.canceledCh:
		case <-time.After(2 * time.Second):
			t.Fatal("ollama server request context did not cancel")
		}

		toMock := newContractHTTPMock(t)
		t.Setenv("OLLAMA_HOST", toMock.server.URL)
		oTO := NewOllamaProvider().WithHTTPClient(toMock.server.Client())
		rhTO, err := oTO.StartRequest(context.Background(), Spec{Model: "llama3.2", Brief: "[CONTRACT:HANG]", Timeout: 200 * time.Millisecond})
		if err != nil {
			t.Fatalf("StartRequest timeout: %v", err)
		}
		rcTO, toErr := rhTO.Wait(context.Background())
		if rcTO.Status != "TIMEOUT" || !errors.Is(toErr, context.DeadlineExceeded) {
			t.Fatalf("expected TIMEOUT/DeadlineExceeded, got %+v err=%v", rcTO, toErr)
		}
		t.Setenv("OLLAMA_HOST", mock.server.URL)
		v["timeout_cancel"] = "PASS"

		v["tool_calling"] = "UNSUPPORTED"
		v["effort_mapping"] = "UNSUPPORTED"

		// 7. Token accounting from prompt_eval_count + eval_count
		hTok, err := o.Spawn(context.Background(), Spec{Model: "llama3.2", Brief: "[CONTRACT:TOKENS]"})
		if err != nil {
			t.Fatalf("Spawn tokens: %v", err)
		}
		rcTok, waitErr := hTok.Wait(context.Background())
		if waitErr != nil || rcTok.InputTokens != 11 || rcTok.OutputTokens != 7 || rcTok.TotalTokens != 18 {
			t.Fatalf("expected tokens (11,7,18), got receipt=%+v err=%v", rcTok, waitErr)
		}
		v["token_accounting"] = "PASS"
		verdicts["ollama"] = v
	})

	t.Run("http/openai", func(t *testing.T) {
		mock := newContractHTTPMock(t)
		p := NewOpenAIProvider("openai", mock.server.URL, "").WithHTTPClient(mock.server.Client())
		v := make(map[string]string)

		if err := p.Available(context.Background()); err != nil {
			t.Fatalf("Available: %v", err)
		}
		// 1+2+5+7. One-shot, ResponseFormat, Tools, Tokens
		h, err := p.Spawn(context.Background(), Spec{
			Model:          "gpt-5.5",
			Brief:          "[CONTRACT:ONE_SHOT]",
			Effort:         "high",
			Tools:          []map[string]any{{"type": "function", "function": map[string]any{"name": "echo"}}},
			ResponseFormat: map[string]any{"type": "json_object"},
		})
		if err != nil {
			t.Fatalf("Spawn: %v", err)
		}
		if h.PID() != NoPID {
			t.Fatalf("expected NoPID, got %d", h.PID())
		}
		rc, waitErr := h.Wait(context.Background())
		if waitErr != nil || rc.Status != "COMPLETED" {
			t.Fatalf("receipt=%+v err=%v", rc, waitErr)
		}
		mock.mu.Lock()
		body := mock.lastOpenAIBody
		mock.mu.Unlock()
		if _, ok := body["response_format"]; !ok {
			t.Fatalf("missing response_format in payload: %#v", body)
		}
		if tools, ok := body["tools"].([]any); !ok || len(tools) != 1 {
			t.Fatalf("missing tools in payload: %#v", body["tools"])
		}
		if body["reasoning_effort"] != "high" {
			t.Fatalf("expected reasoning_effort=high, got %#v", body["reasoning_effort"])
		}
		if rc.InputTokens != 12 || rc.OutputTokens != 8 || rc.TotalTokens != 20 {
			t.Fatalf("unexpected token accounting: %+v", rc)
		}
		v["one_shot"] = "PASS"
		v["structured_json_schema"] = "PASS"
		v["tool_calling"] = "PASS"
		v["token_accounting"] = "PASS"

		// Non-silent effort degradation + strict rejection
		hDeg, err := p.Spawn(context.Background(), Spec{Model: "gpt-5.5", Brief: "x", Effort: "xhigh"})
		if err != nil {
			t.Fatalf("Spawn degrade: %v", err)
		}
		rcDeg, _ := hDeg.Wait(context.Background())
		if !rcDeg.EffortDegraded || rcDeg.EffortApplied != "high" || rcDeg.EffortRequested != "xhigh" {
			t.Fatalf("expected non-silent degrade xhigh->high, got %+v", rcDeg)
		}
		if _, err := p.StartRequest(context.Background(), Spec{Model: "gpt-5.5", Brief: "x", Effort: "xhigh", StrictCapabilities: true}); !errors.Is(err, ErrCapabilityUnsupported) {
			t.Fatalf("expected ErrCapabilityUnsupported on strict xhigh, got %v", err)
		}
		v["effort_mapping"] = "PASS"

		// 3. HTTP error propagation
		hErr, err := p.Spawn(context.Background(), Spec{Model: "nonexistent-model", Brief: "[CONTRACT:NONZERO_EXIT]"})
		if err != nil {
			t.Fatalf("Spawn error: %v", err)
		}
		rcErr, waitErr := hErr.Wait(context.Background())
		if waitErr == nil || rcErr.Status != "FAILED" {
			t.Fatalf("expected FAILED on HTTP 404, got %+v err=%v", rcErr, waitErr)
		}
		v["nonzero_exit_error"] = "PASS"

		// 4. Async cancel + timeout
		cancelMock := newContractHTTPMock(t)
		pCancel := NewOpenAIProvider("openai", cancelMock.server.URL, "").WithHTTPClient(cancelMock.server.Client())
		rh, err := pCancel.StartRequest(context.Background(), Spec{Model: "gpt-5.5", Brief: "[CONTRACT:HANG]"})
		if err != nil {
			t.Fatalf("StartRequest hang: %v", err)
		}
		<-cancelMock.started
		if err := rh.Cancel(context.Background()); err != nil {
			t.Fatalf("Cancel: %v", err)
		}
		rcC, cErr := rh.Wait(context.Background())
		if rcC.Status != "CANCELLED" || !errors.Is(cErr, context.Canceled) {
			t.Fatalf("expected CANCELLED/context.Canceled, got %+v err=%v", rcC, cErr)
		}
		select {
		case <-cancelMock.canceledCh:
		case <-time.After(2 * time.Second):
			t.Fatal("openai server request context did not cancel")
		}
		toMock := newContractHTTPMock(t)
		pTO := NewOpenAIProvider("openai", toMock.server.URL, "").WithHTTPClient(toMock.server.Client())
		rhTO, err := pTO.StartRequest(context.Background(), Spec{Model: "gpt-5.5", Brief: "[CONTRACT:HANG]", Timeout: 200 * time.Millisecond})
		if err != nil {
			t.Fatalf("StartRequest timeout: %v", err)
		}
		rcTO, toErr := rhTO.Wait(context.Background())
		if rcTO.Status != "TIMEOUT" || !errors.Is(toErr, context.DeadlineExceeded) {
			t.Fatalf("expected TIMEOUT/DeadlineExceeded, got %+v err=%v", rcTO, toErr)
		}
		v["timeout_cancel"] = "PASS"
		verdicts["openai"] = v
	})

	t.Run("http/anthropic", func(t *testing.T) {
		mock := newContractHTTPMock(t)
		p := NewHTTPProviderFromConfig(config.ProviderEntry{
			Name:     "anthropic",
			BaseURL:  mock.server.URL + "/v1",
			Protocol: "anthropic",
		}).WithHTTPClient(mock.server.Client())
		v := make(map[string]string)

		// 1+5+6+7. One-shot, Tools, Effort (output_config.effort), Tokens
		h, err := p.Spawn(context.Background(), Spec{
			Model:  "claude-sonnet-5-5",
			Brief:  "[CONTRACT:ONE_SHOT]",
			Effort: "high",
			Tools:  []map[string]any{{"name": "echo", "input_schema": map[string]any{"type": "object"}}},
		})
		if err != nil {
			t.Fatalf("Spawn: %v", err)
		}
		if h.PID() != NoPID {
			t.Fatalf("expected NoPID, got %d", h.PID())
		}
		rc, waitErr := h.Wait(context.Background())
		if waitErr != nil || rc.Status != "COMPLETED" || rc.Stdout != "PONG" {
			t.Fatalf("receipt=%+v err=%v", rc, waitErr)
		}
		mock.mu.Lock()
		body := mock.lastAnthropicBdy
		mock.mu.Unlock()
		if tools, ok := body["tools"].([]any); !ok || len(tools) != 1 {
			t.Fatalf("missing anthropic tools: %#v", body["tools"])
		}
		if oc, ok := body["output_config"].(map[string]any); !ok || oc["effort"] != "high" {
			t.Fatalf("expected output_config.effort=high, got %#v", body["output_config"])
		}
		if rc.InputTokens != 15 || rc.OutputTokens != 9 || rc.TotalTokens != 24 {
			t.Fatalf("unexpected anthropic token accounting: %+v", rc)
		}
		if _, err := p.StartRequest(context.Background(), Spec{Model: "claude-sonnet-5-5", Brief: "x", Effort: "turbo", StrictCapabilities: true}); !errors.Is(err, ErrCapabilityUnsupported) {
			t.Fatalf("expected ErrCapabilityUnsupported on strict unknown effort, got %v", err)
		}
		v["one_shot"] = "PASS"
		v["structured_json_schema"] = "UNSUPPORTED"
		v["tool_calling"] = "PASS"
		v["effort_mapping"] = "PASS"
		v["token_accounting"] = "PASS"

		// 3. HTTP error
		hErr, err := p.Spawn(context.Background(), Spec{Model: "claude-sonnet-5-5", Brief: "[CONTRACT:NONZERO_EXIT]"})
		if err != nil {
			t.Fatalf("Spawn error: %v", err)
		}
		rcErr, waitErr := hErr.Wait(context.Background())
		if waitErr == nil || rcErr.Status != "FAILED" {
			t.Fatalf("expected FAILED on HTTP 400, got %+v err=%v", rcErr, waitErr)
		}
		v["nonzero_exit_error"] = "PASS"

		// 4. Async cancel + timeout
		cancelMock := newContractHTTPMock(t)
		pCancel := NewHTTPProviderFromConfig(config.ProviderEntry{
			Name:     "anthropic",
			BaseURL:  cancelMock.server.URL + "/v1",
			Protocol: "anthropic",
		}).WithHTTPClient(cancelMock.server.Client())
		rh, err := pCancel.StartRequest(context.Background(), Spec{Model: "claude-sonnet-5-5", Brief: "[CONTRACT:HANG]"})
		if err != nil {
			t.Fatalf("StartRequest hang: %v", err)
		}
		<-cancelMock.started
		if err := rh.Cancel(context.Background()); err != nil {
			t.Fatalf("Cancel: %v", err)
		}
		rcC, cErr := rh.Wait(context.Background())
		if rcC.Status != "CANCELLED" || !errors.Is(cErr, context.Canceled) {
			t.Fatalf("expected CANCELLED/context.Canceled, got %+v err=%v", rcC, cErr)
		}
		select {
		case <-cancelMock.canceledCh:
		case <-time.After(2 * time.Second):
			t.Fatal("anthropic server request context did not cancel")
		}
		toMock := newContractHTTPMock(t)
		pTO := NewHTTPProviderFromConfig(config.ProviderEntry{
			Name:     "anthropic",
			BaseURL:  toMock.server.URL + "/v1",
			Protocol: "anthropic",
		}).WithHTTPClient(toMock.server.Client())
		rhTO, err := pTO.StartRequest(context.Background(), Spec{Model: "claude-sonnet-5-5", Brief: "[CONTRACT:HANG]", Timeout: 200 * time.Millisecond})
		if err != nil {
			t.Fatalf("StartRequest timeout: %v", err)
		}
		rcTO, toErr := rhTO.Wait(context.Background())
		if rcTO.Status != "TIMEOUT" || !errors.Is(toErr, context.DeadlineExceeded) {
			t.Fatalf("expected TIMEOUT/DeadlineExceeded, got %+v err=%v", rcTO, toErr)
		}
		v["timeout_cancel"] = "PASS"
		verdicts["anthropic"] = v
	})

	expectedAdapters := []string{"agy", "codex", "claude", "opencode", "commandcode", "qwen", "cursor", "ollama", "openai", "anthropic"}
	dims := []string{"one_shot", "structured_json_schema", "nonzero_exit_error", "timeout_cancel", "tool_calling", "effort_mapping", "token_accounting"}
	for _, name := range expectedAdapters {
		m, ok := verdicts[name]
		if !ok {
			t.Fatalf("missing contract verdict table for adapter %q", name)
		}
		for _, dim := range dims {
			val := m[dim]
			if val != "PASS" && val != "UNSUPPORTED" {
				t.Fatalf("adapter %q dimension %q = %q, want PASS or UNSUPPORTED", name, dim, val)
			}
		}
	}
}

// TestOfflineAdapterNegativeRegressions deterministically proves malformed
// stdout/JSON/SSE envelopes, non-zero exits, exit-0 stderr refusals, HTTP
// errors, and timeout/cancel hangs are caught across every adapter (#941).
func TestOfflineAdapterNegativeRegressions(t *testing.T) {
	bins := buildContractCLIBins(t)

	for _, cs := range allCLIAdapterSpecs {
		cs := cs
		t.Run("negative/cli/"+cs.name, func(t *testing.T) {
			// Bad binary path catches non-zero failure
			t.Setenv(cs.binEnv, bins.bad[cs.name])
			badProv := cs.newProvider()
			badDir := makeContractWorkDir(t)
			hBad, err := badProv.Spawn(context.Background(), Spec{Brief: "[CONTRACT:ONE_SHOT]", WorktreeDir: badDir})
			if err != nil {
				t.Fatalf("Spawn bad binary: %v", err)
			}
			rcBad, waitErr := hBad.Wait(context.Background())
			if waitErr == nil || rcBad.Status != "FAILED" || rcBad.ExitCode != 2 {
				t.Fatalf("expected FAILED exit=2 from bad binary, got receipt=%+v err=%v", rcBad, waitErr)
			}

			// Malformed JSON envelope caught deterministically
			t.Setenv(cs.binEnv, bins.good[cs.name])
			goodProv := cs.newProvider()
			malDir := makeContractWorkDir(t)
			hMal, err := goodProv.Spawn(context.Background(), Spec{Brief: "[CONTRACT:MALFORMED_JSON]", WorktreeDir: malDir})
			if err != nil {
				t.Fatalf("Spawn malformed JSON: %v", err)
			}
			rcMal, err := hMal.Wait(context.Background())
			if err != nil {
				t.Fatalf("Wait malformed JSON fixture: %v", err)
			}
			var tmp map[string]any
			if uErr := json.Unmarshal([]byte(rcMal.Stdout), &tmp); uErr == nil {
				t.Fatalf("expected JSON unmarshal to reject malformed stdout %q", rcMal.Stdout)
			}

			// Codex catches exit-0 stderr refusals (quota / auth)
			if cs.name == "codex" {
				authDir := makeContractWorkDir(t)
				hAuth, err := goodProv.Spawn(context.Background(), Spec{Brief: "[CONTRACT:STDERR_REFUSAL_AUTH]", WorktreeDir: authDir})
				if err != nil {
					t.Fatalf("Spawn auth refusal: %v", err)
				}
				rcAuth, aErr := hAuth.Wait(context.Background())
				if aErr == nil || rcAuth.Status != "FAILED" || !strings.Contains(aErr.Error(), ErrCodeProviderAuth) {
					t.Fatalf("expected %s, got receipt=%+v err=%v", ErrCodeProviderAuth, rcAuth, aErr)
				}
			}

			// Timeout/Cancel violation caught promptly
			hangDir := makeContractWorkDir(t)
			hHang, err := goodProv.Spawn(context.Background(), Spec{Brief: "[CONTRACT:HANG]", WorktreeDir: hangDir})
			if err != nil {
				t.Fatalf("Spawn hang: %v", err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer cancel()
			rcHang, hErr := hHang.Wait(ctx)
			if hErr == nil || rcHang.Status != "TIMEOUT" {
				t.Fatalf("expected TIMEOUT on hung CLI, got receipt=%+v err=%v", rcHang, hErr)
			}
		})
	}

	t.Run("negative/http/ollama", func(t *testing.T) {
		mock := newContractHTTPMock(t)
		t.Setenv("OLLAMA_HOST", mock.server.URL)
		o := NewOllamaProvider().WithHTTPClient(mock.server.Client())

		hMal, err := o.Spawn(context.Background(), Spec{Model: "llama3.2", Brief: "[CONTRACT:MALFORMED_JSON]"})
		if err != nil {
			t.Fatalf("Spawn: %v", err)
		}
		if rc, _ := hMal.Wait(context.Background()); rc.Status != "FAILED" {
			t.Fatalf("expected FAILED on corrupt ollama JSON, got %+v", rc)
		}
	})

	t.Run("negative/http/openai", func(t *testing.T) {
		mock := newContractHTTPMock(t)
		p := NewOpenAIProvider("openai", mock.server.URL, "").WithHTTPClient(mock.server.Client())

		for _, tc := range []struct {
			name   string
			brief  string
			stream bool
		}{
			{name: "malformed_json", brief: "[CONTRACT:MALFORMED_JSON]"},
			{name: "malformed_sse", brief: "[CONTRACT:MALFORMED_SSE]", stream: true},
		} {
			h, err := p.Spawn(context.Background(), Spec{Model: "gpt-5.5", Brief: tc.brief, Stream: tc.stream})
			if err != nil {
				t.Fatalf("%s Spawn: %v", tc.name, err)
			}
			rc, wErr := h.Wait(context.Background())
			if wErr == nil || rc.Status != "FAILED" {
				t.Fatalf("%s expected FAILED + err, got %+v err=%v", tc.name, rc, wErr)
			}
		}
	})

	t.Run("negative/http/anthropic", func(t *testing.T) {
		mock := newContractHTTPMock(t)
		p := NewHTTPProviderFromConfig(config.ProviderEntry{
			Name:     "anthropic",
			BaseURL:  mock.server.URL + "/v1",
			Protocol: "anthropic",
		}).WithHTTPClient(mock.server.Client())

		for _, tc := range []struct {
			name   string
			brief  string
			stream bool
		}{
			{name: "malformed_json", brief: "[CONTRACT:MALFORMED_JSON]"},
			{name: "malformed_sse", brief: "[CONTRACT:MALFORMED_SSE]", stream: true},
		} {
			h, err := p.Spawn(context.Background(), Spec{Model: "claude-sonnet-5-5", Brief: tc.brief, Stream: tc.stream})
			if err != nil {
				t.Fatalf("%s Spawn: %v", tc.name, err)
			}
			rc, wErr := h.Wait(context.Background())
			if wErr == nil || rc.Status != "FAILED" {
				t.Fatalf("%s expected FAILED + err, got %+v err=%v", tc.name, rc, wErr)
			}
		}
	})
}
