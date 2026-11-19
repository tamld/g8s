package conformance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const offlineConformanceCLISource = `package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func helpFor(base string) string {
	switch base {
	case "agy":
		return "agy CLI\n  --print --output-format json --print-timeout --sandbox --add-dir --json-schema"
	case "codex":
		return "codex exec runs non-interactively\n  --json --sandbox --cd --output-schema"
	case "claude":
		return "claude CLI\n  --print --output-format json --permission-mode --add-dir --json-schema"
	case "opencode":
		return "opencode run with a message\n  --format json --dir --model"
	case "commandcode":
		return "commandcode CLI\n  --print --output-format json --permission-mode --add-dir --worktree"
	case "qwen":
		return "qwen CLI\n  --prompt --output-format json --max-wall-time --sandbox --approval-mode --include-directories --json-schema"
	case "cursor-agent":
		return "cursor-agent CLI\n  --print --output-format json --workspace --add-dir --json-schema"
	default:
		return "usage: " + base + " --print --output-format"
	}
}

func main() {
	exePath, _ := os.Executable()
	base := strings.TrimSuffix(filepath.Base(exePath), ".exe")
	badMode := filepath.Base(filepath.Dir(exePath)) == "bad"
	args := os.Args[1:]

	for _, a := range args {
		if a == "--version" {
			fmt.Println("9.9.9")
			return
		}
		if a == "--help" || a == "-h" {
			fmt.Println(helpFor(base))
			return
		}
	}

	var model string
	isJSON := false
	hasSchema := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--model":
			if i+1 < len(args) {
				model = args[i+1]
			}
		case "--output-format", "--format", "-o":
			if i+1 < len(args) && args[i+1] == "json" {
				isJSON = true
			}
		case "--json":
			isJSON = true
		case "--json-schema", "--output-schema":
			hasSchema = true
		}
	}

	joined := strings.Join(args, " ")

	if badMode {
		switch {
		case strings.Contains(joined, "g8s_probe_inside.txt"):
			const prefix = "create the file "
			const suffix = " containing ESCAPE"
			if sIdx := strings.Index(joined, prefix); sIdx >= 0 {
				rest := joined[sIdx+len(prefix):]
				if eIdx := strings.Index(rest, suffix); eIdx >= 0 {
					escPath := strings.TrimSpace(rest[:eIdx])
					_ = os.WriteFile(escPath, []byte("ESCAPE"), 0o600)
				}
			}
			return
		case strings.Contains(joined, "g8s_probe_edit.txt"):
			_ = os.WriteFile("other.txt", []byte("x"), 0o600)
			return
		case hasSchema || isJSON:
			fmt.Println("not json at all")
			return
		default:
			fmt.Println("I refuse")
			return
		}
	}

	if strings.Contains(model, "nonexistent") {
		fmt.Fprintln(os.Stderr, "model not found")
		os.Exit(3)
	}

	switch {
	case strings.Contains(joined, "integers from 1 to 5000"):
		time.Sleep(30 * time.Second)
	case strings.Contains(joined, "g8s_probe_edit.txt"):
		if err := os.WriteFile("g8s_probe_edit.txt", []byte("G8S_CONFORMANCE_MARK"), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("done")
	case strings.Contains(joined, "g8s_probe_inside.txt"):
		if err := os.WriteFile("g8s_probe_inside.txt", []byte("OK"), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("done")
	case hasSchema:
		fmt.Println(` + "`" + `{"type":"result","result":"{\"answer\":\"PONG\"}"}` + "`" + `)
	case isJSON:
		fmt.Println(` + "`" + `{"type":"text","part":{"text":"PONG"}}` + "`" + `)
		fmt.Println(` + "`" + `{"type":"step_finish","usage":{"input_tokens":12,"output_tokens":3,"total_tokens":15}}` + "`" + `)
	default:
		fmt.Println("PONG")
	}
}
`

func compileConformanceContractBins(t *testing.T) (goodBins map[string]string, badBins map[string]string) {
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
	if err := os.WriteFile(mainGo, []byte(offlineConformanceCLISource), 0o600); err != nil {
		t.Fatalf("write conformance fake CLI: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "go.mod"), []byte("module g8s.invalid/conformancecli\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	masterBin := filepath.Join(root, "conformancecli"+ext)
	buildCmd := exec.Command("go", "build", "-o", masterBin, mainGo)
	buildCmd.Dir = srcDir
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("compile conformance fake CLI: %v\n%s", err, string(out))
	}
	raw, err := os.ReadFile(masterBin)
	if err != nil {
		t.Fatalf("read compiled binary: %v", err)
	}

	goodBins = make(map[string]string)
	badBins = make(map[string]string)
	for _, prof := range DefaultCLIProfiles() {
		binName := prof.BinaryName
		if binName == "" {
			binName = prof.Name
		}
		gPath := filepath.Join(goodDir, binName+ext)
		bPath := filepath.Join(badDir, binName+ext)
		if err := os.WriteFile(gPath, raw, 0o700); err != nil {
			t.Fatalf("write good bin %s: %v", prof.Name, err)
		}
		if err := os.WriteFile(bPath, raw, 0o700); err != nil {
			t.Fatalf("write bad bin %s: %v", prof.Name, err)
		}
		goodBins[prof.Name] = gPath
		badBins[prof.Name] = bPath
	}
	return goodBins, badBins
}

// TestConformanceOfflineCLIContracts_AllProfiles exercises the full ProbeCLI
// suite across all 7 DefaultCLIProfiles() (agy, codex, claude, opencode,
// commandcode, qwen, cursor) on Linux, macOS, and Windows (#941).
func TestConformanceOfflineCLIContracts_AllProfiles(t *testing.T) {
	goodBins, badBins := compileConformanceContractBins(t)

	for _, prof := range DefaultCLIProfiles() {
		prof := prof
		t.Run("good/"+prof.Name, func(t *testing.T) {
			t.Setenv(prof.BinEnv, goodBins[prof.Name])
			rep := ProbeCLI(context.Background(), prof, CLIOptions{
				Live:      true,
				AllowPaid: ParseAllowPaid(prof.Name),
				Model:     "docs-retriever",
				Timeout:   20 * time.Second,
				KillAfter: 1 * time.Second,
			})
			if !rep.Available || rep.Mode != "live" {
				t.Fatalf("%s expected available live report, got available=%t mode=%s", prof.Name, rep.Available, rep.Mode)
			}
			for _, id := range []ProbeID{ProbeP0, ProbeP1, ProbeP2, ProbeP3, ProbeP5, ProbeP6, ProbeP8, ProbeP9} {
				r := statusOf(t, rep, id)
				if r.Status != StatusPass {
					t.Errorf("%s probe %s = %s (%s), want PASS", prof.Name, id, r.Status, r.Reason)
				}
			}
			p4 := statusOf(t, rep, ProbeP4)
			if pgidSupported {
				if p4.Status != StatusPass {
					t.Errorf("%s probe P4 = %s (%s), want PASS on POSIX", prof.Name, p4.Status, p4.Reason)
				}
			} else if p4.Status != StatusUnsupported {
				t.Errorf("%s probe P4 = %s (%s), want UNSUPPORTED on Windows", prof.Name, p4.Status, p4.Reason)
			}
			p7 := statusOf(t, rep, ProbeP7)
			if prof.SchemaArgs != nil {
				if p7.Status != StatusPass {
					t.Errorf("%s probe P7 = %s (%s), want PASS", prof.Name, p7.Status, p7.Reason)
				}
			} else if p7.Status != StatusUnsupported {
				t.Errorf("%s probe P7 = %s (%s), want UNSUPPORTED", prof.Name, p7.Status, p7.Reason)
			}
			if failed := rep.FailedRequired(); len(failed) != 0 {
				t.Fatalf("%s FailedRequired = %v, want empty", prof.Name, failed)
			}
		})

		t.Run("bad/"+prof.Name, func(t *testing.T) {
			t.Setenv(prof.BinEnv, badBins[prof.Name])
			rep := ProbeCLI(context.Background(), prof, CLIOptions{
				Live:      true,
				AllowPaid: ParseAllowPaid(prof.Name),
				Model:     "docs-retriever",
				Timeout:   20 * time.Second,
				KillAfter: 1 * time.Second,
			})
			for _, id := range []ProbeID{ProbeP2, ProbeP3, ProbeP5, ProbeP6} {
				r := statusOf(t, rep, id)
				if r.Status != StatusFail {
					t.Errorf("%s bad binary probe %s = %s (%s), want FAIL", prof.Name, id, r.Status, r.Reason)
				}
			}
			if prof.SchemaArgs != nil {
				if r := statusOf(t, rep, ProbeP7); r.Status != StatusFail {
					t.Errorf("%s bad binary probe P7 = %s (%s), want FAIL", prof.Name, r.Status, r.Reason)
				}
			}
			if len(rep.FailedRequired()) == 0 {
				t.Fatalf("%s bad binary expected non-empty FailedRequired()", prof.Name)
			}
		})
	}
}

// TestConformanceOfflineHTTPContracts_AllGateways exercises ProbeHTTP in both
// positive and negative regression modes across 9router, openai, and ollama (#941).
func TestConformanceOfflineHTTPContracts_AllGateways(t *testing.T) {
	goodSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"docs-retriever","context_length":100},{"id":"gpt-5.5","context_length":100},{"id":"llama3.2","context_length":100}]}`))
		case "/chat/completions":
			var req struct {
				Model          string           `json:"model"`
				Tools          []map[string]any `json:"tools"`
				ResponseFormat map[string]any   `json:"response_format"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.Model == badModel {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":{"message":"model not found"}}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if len(req.Tools) > 0 {
				_, _ = w.Write([]byte(`{"model":"` + req.Model + `","choices":[{"message":{"tool_calls":[{"function":{"name":"g8s_probe_echo","arguments":"{\"text\":\"PONG\"}"}}]}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`))
				return
			}
			if req.ResponseFormat != nil {
				_, _ = w.Write([]byte(`{"model":"` + req.Model + `","choices":[{"message":{"content":"{\"answer\":\"PONG\"}"}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`))
				return
			}
			_, _ = w.Write([]byte(`{"model":"` + req.Model + `","choices":[{"message":{"content":"PONG"}}],"usage":{"prompt_tokens":120,"completion_tokens":1,"total_tokens":121}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer goodSrv.Close()

	targets := []HTTPTarget{
		{Name: "9router", BaseURL: goodSrv.URL, Model: "docs-retriever", Policy: PolicyFree, Client: goodSrv.Client()},
		{Name: "openai", BaseURL: goodSrv.URL, Model: "gpt-5.5", Policy: PolicyPaid, Client: goodSrv.Client()},
		{Name: "ollama", BaseURL: goodSrv.URL, Model: "llama3.2", Policy: PolicyFree, Client: goodSrv.Client()},
	}
	for _, tgt := range targets {
		rep := ProbeHTTP(context.Background(), tgt, HTTPOptions{
			Live:               true,
			AllowPaid:          ParseAllowPaid("openai"),
			ContextProbeTokens: 120,
		})
		for _, id := range []ProbeID{ProbeP0, ProbeH1, ProbeP1, ProbeP2, ProbeP3, ProbeP7, ProbeP8, ProbeH2, ProbeH3} {
			if r := statusOf(t, rep, id); r.Status != StatusPass {
				t.Errorf("%s probe %s = %s (%s), want PASS", tgt.Name, id, r.Status, r.Reason)
			}
		}
		for _, id := range []ProbeID{ProbeP4, ProbeP5, ProbeP6} {
			if r := statusOf(t, rep, id); r.Status != StatusUnsupported {
				t.Errorf("%s probe %s = %s, want UNSUPPORTED", tgt.Name, id, r.Status)
			}
		}
	}

	// Negative HTTP regression server: invalid /models JSON, invalid chat JSON, silent 200 on badModel
	badSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`not-valid-models-json`))
		case "/chat/completions":
			var req struct {
				Model          string         `json:"model"`
				ResponseFormat map[string]any `json:"response_format"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			w.Header().Set("Content-Type", "application/json")
			if req.Model == badModel {
				_, _ = w.Write([]byte(`{"model":"silent-fallback","choices":[{"message":{"content":"PONG"}}]}`))
				return
			}
			if req.ResponseFormat != nil {
				_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"not-matching-schema"}}]}`))
				return
			}
			_, _ = w.Write([]byte(`corrupt-chat-json`))
		}
	}))
	defer badSrv.Close()

	badRep := ProbeHTTP(context.Background(), HTTPTarget{
		Name:    "9router",
		BaseURL: badSrv.URL,
		Model:   "docs-retriever",
		Policy:  PolicyFree,
		Client:  badSrv.Client(),
	}, HTTPOptions{Live: true})
	for _, id := range []ProbeID{ProbeH1, ProbeP2, ProbeP3, ProbeP7} {
		if r := statusOf(t, badRep, id); r.Status != StatusFail {
			t.Errorf("negative HTTP probe %s = %s (%s), want FAIL", id, r.Status, r.Reason)
		}
	}
}

// TestProviderConformanceMatrix_ContractParity guards .g8s/provider-conformance.yml
// and docs/user-guide/provider-matrix.md against schema drift and verifies
// clean separation between contract_status and live_status (#941).
func TestProviderConformanceMatrix_ContractParity(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	matrixPath, ok := FindMatrixFile(wd)
	if !ok {
		t.Fatalf("could not locate %s upward from %s", DefaultMatrixPath, wd)
	}
	repoRoot := filepath.Dir(filepath.Dir(matrixPath))
	docPath := filepath.Join(repoRoot, filepath.FromSlash(DefaultDocPath))

	m, err := LoadMatrix(matrixPath)
	if err != nil {
		t.Fatalf("LoadMatrix(%s): %v", matrixPath, err)
	}
	if m.SchemaVersion != SchemaVersion {
		t.Fatalf("SchemaVersion = %q, want %q", m.SchemaVersion, SchemaVersion)
	}

	for _, name := range ContractBaselineProviders() {
		rep, found := m.Find(name)
		if !found {
			t.Fatalf("missing provider %q in %s", name, matrixPath)
		}
		if len(rep.Probes) == 0 {
			t.Fatalf("provider %q has zero probes", name)
		}
		for _, pr := range rep.Probes {
			if pr.ContractStatus != StatusPass && pr.ContractStatus != StatusUnsupported {
				t.Errorf("%s.%s ContractStatus = %q, want PASS or UNSUPPORTED", name, pr.ID, pr.ContractStatus)
			}
			spec, specOK := LookupContractBaseline(name, pr.ID)
			if !specOK {
				t.Errorf("%s.%s missing from ApplyOfflineContractBaseline", name, pr.ID)
			} else if pr.ContractStatus != spec.Status {
				t.Errorf("%s.%s ContractStatus = %q, want %q", name, pr.ID, pr.ContractStatus, spec.Status)
			}
			if pr.LiveStatus == "" || pr.LiveStatus != pr.Status {
				t.Errorf("%s.%s LiveStatus=%q must be non-empty and match Status=%q", name, pr.ID, pr.LiveStatus, pr.Status)
			}
		}
	}

	if v := m.CheckDispatch("opencode"); !v.Refuse {
		t.Fatalf("expected CheckDispatch(opencode) to refuse on live P5 FAIL, got %+v", v)
	}
	if v := m.CheckDispatch("agy"); v.Refuse {
		t.Fatalf("expected CheckDispatch(agy) not to refuse on UNVERIFIED live probes, got %+v", v)
	}

	docBytes, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read %s: %v", docPath, err)
	}
	gotDoc := strings.TrimSpace(strings.ReplaceAll(string(docBytes), "\r\n", "\n"))
	wantDoc := strings.TrimSpace(strings.ReplaceAll(RenderMarkdown(*m), "\r\n", "\n"))
	if gotDoc != wantDoc {
		t.Fatalf("%s is out of sync with %s; regenerate with RenderMarkdown", DefaultDocPath, DefaultMatrixPath)
	}
}
