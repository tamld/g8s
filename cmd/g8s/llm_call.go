package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/tamld/g8s/internal/brief"
	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/config"
	"github.com/tamld/g8s/internal/dispatch"
	"github.com/tamld/g8s/internal/fsops"
	"github.com/tamld/g8s/internal/harness"
	"github.com/tamld/g8s/internal/pathutil"
	"github.com/tamld/g8s/internal/provider"
	"github.com/tamld/g8s/internal/receipt"
	"github.com/tamld/g8s/internal/routing"
	"github.com/tamld/g8s/internal/vault"
)

type fileBlock struct {
	path    string
	content string
}

type fileSnapshot struct {
	existed bool
	data    []byte
	mode    os.FileMode
}

func restoreSnapshots(snapshots map[string]fileSnapshot) {
	for path, snap := range snapshots {
		if snap.existed {
			mode := snap.mode
			if mode == 0 {
				mode = 0o600
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				fmt.Fprintf(os.Stderr, "[warn] llm-call: restore mkdir %s: %v\n", path, err)
			}
			if err := os.WriteFile(path, snap.data, mode); err != nil {
				fmt.Fprintf(os.Stderr, "[warn] llm-call: restore file %s: %v\n", path, err)
			}
		} else {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				fmt.Fprintf(os.Stderr, "[warn] llm-call: remove failed-turn file %s: %v\n", path, err)
			}
		}
	}
}

var reVolatileErrorTokens = regexp.MustCompile(`(\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})?|\b\d+(?:\.\d+)?(?:ns|us|µs|ms|s)\b|0x[0-9a-fA-F]{4,}|/var/folders/[^\s:]+|/tmp/[^\s:]+)`)

// NormalizeVerificationError strips volatile runtime noise (timestamps, Go test
// elapsed durations, hex memory addresses, temporary directory paths) so
// identical failures across turns produce a stable signature (#663).
func NormalizeVerificationError(raw string) string {
	replaced := reVolatileErrorTokens.ReplaceAllString(strings.TrimSpace(raw), "<VOLATILE>")
	return strings.Join(strings.Fields(replaced), " ")
}

// ErrorSignatureHash computes a 16-char SHA-256 fingerprint of the normalized
// verification error output (#663).
func ErrorSignatureHash(raw string) string {
	norm := NormalizeVerificationError(raw)
	sum := sha256.Sum256([]byte(norm))
	return hex.EncodeToString(sum[:8])
}

// FileTreeHash computes a deterministic 16-char SHA-256 fingerprint of all
// emitted file blocks sorted by relative path (#663).
func FileTreeHash(blocks []fileBlock) string {
	if len(blocks) == 0 {
		return ""
	}
	sorted := make([]fileBlock, len(blocks))
	copy(sorted, blocks)
	sort.Slice(sorted, func(i, j int) bool {
		return filepath.ToSlash(sorted[i].path) < filepath.ToSlash(sorted[j].path)
	})
	h := sha256.New()
	for _, b := range sorted {
		_, _ = h.Write([]byte(filepath.ToSlash(b.path)))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(b.content))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)[:8])
}

// scrubVerifyEnv removes sensitive credential environment variables before
// executing untrusted --verify-cmd shell subprocesses (#664).
func scrubVerifyEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		key := kv
		if idx := strings.IndexByte(kv, '='); idx >= 0 {
			key = kv[:idx]
		}
		upper := strings.ToUpper(strings.TrimSpace(key))
		if strings.HasSuffix(upper, "_API_KEY") ||
			strings.HasSuffix(upper, "_TOKEN") ||
			strings.HasSuffix(upper, "_SECRET") ||
			strings.HasSuffix(upper, "_PASSWORD") ||
			strings.HasSuffix(upper, "_CREDENTIAL") ||
			strings.HasSuffix(upper, "_PRIVATE_KEY") ||
			upper == "AWS_SECRET_ACCESS_KEY" {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func openOptionalVaultForGrounding() *vault.Vault {
	vdb := strings.TrimSpace(os.Getenv("G8S_VAULT_DB"))
	if vdb == "" {
		vdb = filepath.Join(pathutil.DefaultStateDir(), "vault.db")
	}
	if _, err := os.Stat(vdb); err != nil {
		return nil
	}
	v, err := vault.NewVault(vdb, nil)
	if err != nil {
		return nil
	}
	return v
}

func extractFileBlocks(text string) []fileBlock {
	var blocks []fileBlock
	lines := strings.Split(text, "\n")
	inBlock := false
	var currentPath string
	var currentLines []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !inBlock {
			if strings.HasPrefix(trimmed, "```file:") {
				path := strings.TrimPrefix(trimmed, "```file:")
				path = strings.Trim(path, "`\"' ")
				if path != "" {
					inBlock = true
					currentPath = path
					currentLines = nil
				}
			}
		} else {
			if trimmed == "```" || strings.HasPrefix(trimmed, "```") {
				content := strings.Join(currentLines, "\n")
				if len(currentLines) > 0 {
					content += "\n"
				}
				blocks = append(blocks, fileBlock{
					path:    currentPath,
					content: content,
				})
				inBlock = false
				currentPath = ""
				currentLines = nil
			} else {
				currentLines = append(currentLines, line)
			}
		}
	}
	return blocks
}

func emitWorkerError(startTime time.Time, durationMs int64, msg string) {
	durationSec := float64(durationMs) / 1000.0
	if durationSec == 0 {
		durationSec = time.Since(startTime).Seconds()
	}
	out := map[string]any{
		"event": "result",
		"result": map[string]any{
			"status":           "ERROR",
			"error":            msg,
			"duration_seconds": durationSec,
		},
	}
	if b, err := json.Marshal(out); err == nil {
		fmt.Println(string(b))
	}
	os.Exit(1)
}

// runLLMCall executes a direct synchronous LLM call against any configured api_call provider.
func runLLMCall(args []string) {
	fs := flag.NewFlagSet("llm-call", flag.ContinueOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	_ = actor

	providerFlag := fs.String("provider", "", "target provider declared in providers.json")
	modelFlag := fs.String("model", "", "model or combo ID")
	promptFlag := fs.String("prompt", "", "user prompt text")
	promptFileFlag := fs.String("prompt-file", "", "read prompt from file")
	systemFlag := fs.String("system", "", "optional system prompt")
	timeoutFlag := fs.String("timeout", "60s", "HTTP request timeout")
	effortFlag := fs.String("effort", "", "optional reasoning effort level")
	maxTokensFlag := fs.Int("max-tokens", 0, "optional max output tokens")
	baseURLFlag := fs.String("base-url", "", "optional direct base URL override")
	protocolFlag := fs.String("protocol", "", "optional protocol override (openai, anthropic, 9router)")
	authEnvFlag := fs.String("auth-env", "", "optional auth env var override")
	applyFilesFlag := fs.Bool("apply-files", false, "apply fenced file blocks from LLM response to workspace")
	workerEnvelopeFlag := fs.Bool("worker-envelope", false, "emit single-line worker result JSON envelope on stdout")
	contextFilesFlag := fs.String("context-files", "", "comma-separated file paths to hydrate as bounded context relative to scope root")
	scopeRootFlag := fs.String("scope-root", "", "base directory for context files and write scope")
	receiptIDFlag := fs.String("receipt-id", "", "write receipt ID for delegation boundary validation")
	maxTurnsFlag := fs.Int("max-turns", 1, "maximum turns for bounded verify-and-repair loop (1..5)")
	verifyCmdFlag := fs.String("verify-cmd", "", "optional shell command run inside scope root after applying files to verify changes")
	skillsFlag := fs.String("skills", "", "comma-separated skill names or skill://g8s/ URIs to hydrate with SHA-256 digest verification (DELTA-34)")

	fs.Usage = func() {
		fmt.Println("Usage: g8s llm-call [flags]")
		fmt.Println("\nDirect synchronous LLM call consumer against configured providers.")
		fmt.Println("\nFlags:")
		fs.PrintDefaults()
	}
	fs.SetOutput(os.Stdout)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		exitUsage("llm-call", "", *traceID, err.Error(), "Run 'g8s llm-call --help' for usage", *jsonl)
	}

	var prompt string
	if *promptFileFlag != "" {
		if err := harness.ValidateScopePath(*promptFileFlag); err != nil {
			exitRuntime("llm-call", "", *traceID, cli.CodeHarness, fmt.Errorf("prompt file rejected: %w", err), "Choose a prompt file outside denied/sensitive paths", *jsonl)
		}
		data, err := os.ReadFile(*promptFileFlag)
		if err != nil {
			exitRuntime("llm-call", "", *traceID, cli.CodeIO, fmt.Errorf("read prompt file %q: %w", *promptFileFlag, err), "Ensure file exists and is readable", *jsonl)
		}
		prompt = string(data)
	} else if *promptFlag != "" {
		prompt = *promptFlag
	} else if !term.IsTerminal(int(os.Stdin.Fd())) {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			exitRuntime("llm-call", "", *traceID, cli.CodeIO, fmt.Errorf("read stdin prompt: %w", err), "", *jsonl)
		}
		prompt = string(data)
	}

	if strings.TrimSpace(prompt) == "" {
		exitUsage("llm-call", "prompt", *traceID, "prompt must be provided via --prompt, --prompt-file, or stdin", "Specify --prompt <text> or --prompt-file <path>", *jsonl)
	}

	var contextFileList []string
	if strings.TrimSpace(*contextFilesFlag) != "" {
		hydrateTaskAddDirsEnv()
		explicitReceipt := strings.TrimSpace(*receiptIDFlag) != ""
		rcID, scope, dErr := fsops.ResolveDelegationContext(*receiptIDFlag, *scopeRootFlag, false)
		if dErr != nil {
			exitRuntime("llm-call", "", *traceID, cli.CodeHarness, dErr, "Delegation boundary violation", *jsonl)
		}
		var allowedPaths []string
		if rcID != "" && explicitReceipt {
			rcDbPath, rErr := receiptDatabasePath()
			if rErr != nil {
				exitRuntime("llm-call", "", *traceID, cli.CodeDenied, fmt.Errorf("locate receipt database: %w", rErr), "Ensure G8S_STATE_DIR is set properly", *jsonl)
			}
			rcMgr, mErr := receipt.NewReceiptManager(rcDbPath, nil)
			if mErr != nil {
				exitRuntime("llm-call", "", *traceID, cli.CodeDenied, fmt.Errorf("open receipt database: %w", mErr), "Ensure receipt database is accessible", *jsonl)
			}
			defer rcMgr.Close()

			rec, vErr := rcMgr.VerifyReceipt(rcID)
			if vErr != nil {
				exitRuntime("llm-call", "", *traceID, cli.CodeDenied, fmt.Errorf("verify receipt %s: %w", rcID, vErr), "Provide a valid receipt ID", *jsonl)
			}
			if rec.Consumed {
				exitRuntime("llm-call", "", *traceID, cli.CodeDenied, fmt.Errorf("receipt %s already consumed", rcID), "Provide a fresh write receipt", *jsonl)
			}
			allowedPaths = rec.AllowedPaths
		}

		parts := strings.Split(*contextFilesFlag, ",")
		var contextBlocks strings.Builder
		for _, rawPath := range parts {
			p := strings.TrimSpace(rawPath)
			if p == "" {
				continue
			}
			readPath := p
			if !filepath.IsAbs(p) && scope != "" && scope != "." {
				if _, vErr := fsops.VerifyScope(p, scope, nil); vErr != nil {
					readPath = filepath.Join(scope, p)
				}
			}
			res, rErr := fsops.Read(context.Background(), fsops.ReadOptions{
				Path:         readPath,
				ScopeRoot:    scope,
				AllowedPaths: allowedPaths,
			})
			if rErr != nil {
				exitRuntime("llm-call", "", *traceID, cli.CodeDenied, fmt.Errorf("read context file %q: %w", p, rErr), "Ensure context file is within scope and allowed paths", *jsonl)
			}
			_, _ = fmt.Fprintf(&contextBlocks, "--- FILE: %s (sha256: %s) ---\n%s\n", p, res.SHA256, res.Content)
			contextFileList = append(contextFileList, p)
		}
		if contextBlocks.Len() > 0 {
			prompt = contextBlocks.String() + prompt
		}
	}

	timeoutDur, err := time.ParseDuration(*timeoutFlag)
	if err != nil {
		exitUsage("llm-call", "timeout", *traceID, fmt.Sprintf("invalid --timeout %q: %v", *timeoutFlag, err), "Use duration format like 60s or 2m", *jsonl)
	}
	if timeoutDur <= 0 {
		timeoutDur = 60 * time.Second
	}

	providersPath := os.Getenv("G8S_PROVIDERS")
	if providersPath == "" {
		home, _ := os.UserHomeDir()
		if home != "" {
			providersPath = filepath.Join(home, ".config", "g8s", "providers.json")
		}
	}
	var cfgFile *config.File
	if providersPath != "" {
		if _, statErr := os.Stat(providersPath); statErr == nil {
			cfgFile, _ = config.Load(providersPath)
		}
	}

	var entry config.ProviderEntry
	var resolveErr error
	if cfgFile != nil {
		resolved, rErr := config.ResolveProviderForModel(cfgFile, *providerFlag, *modelFlag)
		if rErr == nil && resolved != nil {
			entry = *resolved
		} else {
			resolveErr = rErr
		}
	}

	if entry.Name == "" {
		if strings.EqualFold(*providerFlag, "cliproxyapi") || strings.EqualFold(*providerFlag, "cliproxy") {
			proto := *protocolFlag
			if proto == "" {
				proto = "openai"
			}
			bURL := *baseURLFlag
			if bURL == "" {
				bURL = "http://127.0.0.1:8317/v1"
			}
			entry = config.ProviderEntry{
				Class:               "api_call",
				Name:                *providerFlag,
				Protocol:            proto,
				BaseURL:             bURL,
				BaseURLEnv:          "CLIPROXYAPI_URL",
				AuthEnv:             "CLIPROXY_API_KEY",
				AllowUnlistedModels: true,
			}
		} else if strings.EqualFold(*providerFlag, "9router") || *baseURLFlag != "" || *providerFlag == "" {
			name := *providerFlag
			if name == "" {
				name = "9router"
			}
			proto := *protocolFlag
			if proto == "" {
				if strings.EqualFold(name, "9router") {
					proto = "9router"
				} else {
					proto = "openai"
				}
			}
			bURL := *baseURLFlag
			if bURL == "" {
				bURL = "http://localhost:20128/v1"
			}
			entry = config.ProviderEntry{
				Class:               "api_call",
				Name:                name,
				Protocol:            proto,
				BaseURL:             bURL,
				BaseURLEnv:          "NINEROUTER_URL",
				AuthEnv:             "OPENAI_API_KEY",
				AllowUnlistedModels: true,
			}
		} else {
			if resolveErr == nil {
				resolveErr = fmt.Errorf("provider %q not found", *providerFlag)
			}
			exitUsage("llm-call", "provider", *traceID, resolveErr.Error(), "Declare provider in providers.json or use --provider 9router / cliproxyapi / --base-url", *jsonl)
		}
	}

	if *baseURLFlag != "" {
		entry.BaseURL = *baseURLFlag
		entry.BaseURLEnv = ""
	}
	if *protocolFlag != "" {
		entry.Protocol = *protocolFlag
	}
	if *authEnvFlag != "" {
		entry.AuthEnv = *authEnvFlag
	}

	modelID := *modelFlag
	if modelID == "" {
		if strings.TrimSpace(entry.DefaultModel) != "" {
			modelID = strings.TrimSpace(entry.DefaultModel)
		} else if len(entry.Models) > 0 {
			modelID = entry.Models[0].ID
		} else if strings.EqualFold(entry.Name, "9router") || entry.Protocol == "9router" {
			modelID = "reasoning-free"
		} else {
			exitUsage("llm-call", "model", *traceID, "model ID must be specified via --model or default_model in providers.json", "Specify --model", *jsonl)
		}
	}

	isWorkerEnvelope := *workerEnvelopeFlag || (!*jsonMode && !*jsonl && os.Getenv("G8S_TASK_ID") != "")
	if *workerEnvelopeFlag {
		isWorkerEnvelope = true
	}
	startTime := time.Now()

	p := provider.NewHTTPProviderFromConfig(entry)
	spec := provider.Spec{
		Brief:        prompt,
		Model:        modelID,
		Effort:       *effortFlag,
		MaxTokens:    *maxTokensFlag,
		SystemPrompt: *systemFlag,
		Timeout:      timeoutDur,
		TraceID:      *traceID,
	}

	maxTurns := *maxTurnsFlag
	if maxTurns <= 1 {
		if envTurns := strings.TrimSpace(os.Getenv("G8S_MAX_TURNS")); envTurns != "" {
			if parsed, pErr := strconv.Atoi(envTurns); pErr == nil && parsed > 1 {
				maxTurns = parsed
			}
		}
	}
	if maxTurns < 1 {
		maxTurns = 1
	}
	if maxTurns > 5 {
		maxTurns = 5
	}

	verifyCmd := strings.TrimSpace(*verifyCmdFlag)
	if verifyCmd == "" {
		verifyCmd = strings.TrimSpace(os.Getenv("G8S_VERIFY_CMD"))
	}

	applyFilesEnabled := *applyFilesFlag || (os.Getenv("G8S_PERMISSION") == "workspace_write" && os.Getenv("G8S_RECEIPT_ID") != "")
	appliedFiles := make([]string, 0)
	appliedFileSet := make(map[string]bool)

	scopeRoot := strings.TrimSpace(*scopeRootFlag)
	if scopeRoot == "" {
		scopeRoot = os.Getenv("G8S_SCOPE_ROOT")
	}
	if scopeRoot == "" {
		if cwd, cErr := os.Getwd(); cErr == nil {
			scopeRoot = cwd
		} else {
			scopeRoot = "."
		}
	}

	var allowedPaths []string
	var activeReceiptID string
	var activeReceiptMgr *receipt.Manager
	if applyFilesEnabled {
		receiptID := strings.TrimSpace(*receiptIDFlag)
		if receiptID == "" {
			receiptID = os.Getenv("G8S_RECEIPT_ID")
		}
		if receiptID != "" {
			rcDbPath, rErr := receiptDatabasePath()
			if rErr != nil {
				if isWorkerEnvelope {
					emitWorkerError(startTime, 0, fmt.Sprintf("locate receipt database: %v", rErr))
				}
				exitRuntime("llm-call", "", *traceID, cli.CodeDenied, fmt.Errorf("locate receipt database: %w", rErr), "Ensure G8S_STATE_DIR is set", *jsonl)
			}
			rcMgr, mErr := receipt.NewReceiptManager(rcDbPath, nil)
			if mErr != nil {
				if isWorkerEnvelope {
					emitWorkerError(startTime, 0, fmt.Sprintf("open receipt database: %v", mErr))
				}
				exitRuntime("llm-call", "", *traceID, cli.CodeDenied, fmt.Errorf("open receipt database: %w", mErr), "Ensure receipt database is accessible", *jsonl)
			}
			defer rcMgr.Close()

			rec, vErr := rcMgr.VerifyReceipt(receiptID)
			if vErr != nil {
				if isWorkerEnvelope {
					emitWorkerError(startTime, 0, fmt.Sprintf("verify receipt %s: %v", receiptID, vErr))
				}
				exitRuntime("llm-call", "", *traceID, cli.CodeDenied, fmt.Errorf("verify receipt %s: %w", receiptID, vErr), "Provide a valid receipt ID", *jsonl)
			}
			if rec.Consumed {
				if isWorkerEnvelope {
					emitWorkerError(startTime, 0, fmt.Sprintf("receipt %s already consumed", receiptID))
				}
				exitRuntime("llm-call", "", *traceID, cli.CodeDenied, fmt.Errorf("receipt %s already consumed", receiptID), "Provide a fresh write receipt", *jsonl)
			}
			allowedPaths = rec.AllowedPaths
			activeReceiptID = receiptID
			activeReceiptMgr = rcMgr
		}
	}

	// #661: Pre-Execution Grounding Hydration (toolchain versions + repo-scoped Vault whispers)
	if applyFilesEnabled || verifyCmd != "" || strings.TrimSpace(os.Getenv("G8S_VAULT_DB")) != "" {
		v := openOptionalVaultForGrounding()
		if v != nil {
			defer v.Close()
		}
		if grounding := vault.BuildPreExecutionGrounding(context.Background(), v, scopeRoot, 0); grounding != "" {
			prompt = "--- PRE-EXECUTION GROUNDING ---\n" + grounding + "\n\n" + prompt
			spec.Brief = prompt
		}
	}

	// DELTA-34: F0/F1/F2 Digest-Pinned Skill Hydration (--skills or G8S_ACTIVE_SKILLS)
	var hydratedSkills []brief.SkillManifest
	rawSkills := strings.TrimSpace(*skillsFlag)
	if rawSkills == "" {
		rawSkills = strings.TrimSpace(os.Getenv("G8S_ACTIVE_SKILLS"))
	}
	if rawSkills != "" {
		skillParts := strings.Split(rawSkills, ",")
		skillBlock, manifests, sErr := brief.HydrateSkillsBlock(skillParts, 4096)
		if sErr != nil {
			if isWorkerEnvelope {
				emitWorkerError(startTime, 0, sErr.Error())
			}
			exitRuntime("llm-call", "", *traceID, cli.CodeDenied, sErr, "Verify skill names and F0/F1/F2 tier access", *jsonl)
		}
		if skillBlock != "" {
			prompt = skillBlock + prompt
			spec.Brief = prompt
			hydratedSkills = manifests
		}
	}

	var rc provider.Receipt
	var totalDurationMs int64
	var totalInputTokens, totalOutputTokens, totalTokens int
	turnsUsed := 0
	verifyPassed := false
	stagnationDetected := false
	stagnationReason := ""
	baselineSnapshots := make(map[string]fileSnapshot)
	seenErrorSigs := make(map[string]int)
	seenTreeHashes := make(map[string]int)
	var falsifiedLedger []string

	for turn := 1; turn <= maxTurns; turn++ {
		turnsUsed = turn
		turnCtx, turnCancel := context.WithTimeout(context.Background(), timeoutDur)
		handle, spawnErr := p.Spawn(turnCtx, spec)
		if spawnErr != nil {
			turnCancel()
			_ = routing.RecordModelOutcome(pathutil.DefaultStateDir(), routing.ModelOutcomeRecord{
				Provider:       entry.Name,
				ModelRequested: modelID,
				Success:        false,
				DurationMs:     totalDurationMs,
				ErrorReason:    dispatch.SanitizeOutput(spawnErr.Error()),
			})
			if isWorkerEnvelope {
				emitWorkerError(startTime, totalDurationMs, spawnErr.Error())
			}
			exitRuntime("llm-call", "", *traceID, cli.CodeRuntime, fmt.Errorf("spawn failed: %w", spawnErr), "", *jsonl)
		}

		turnRes, waitErr := handle.Wait(turnCtx)
		turnCancel()
		totalDurationMs += turnRes.DurationMs
		totalInputTokens += turnRes.InputTokens
		totalOutputTokens += turnRes.OutputTokens
		totalTokens += turnRes.TotalTokens
		rc = turnRes
		rc.DurationMs = totalDurationMs
		rc.InputTokens = totalInputTokens
		rc.OutputTokens = totalOutputTokens
		rc.TotalTokens = totalTokens

		if waitErr != nil {
			_ = routing.RecordModelOutcome(pathutil.DefaultStateDir(), routing.ModelOutcomeRecord{
				Provider:       entry.Name,
				ModelRequested: modelID,
				ModelResolved:  turnRes.ModelResolved,
				Success:        false,
				DurationMs:     totalDurationMs,
				TotalTokens:    totalTokens,
				ErrorReason:    dispatch.SanitizeOutput(waitErr.Error()),
			})
			if isWorkerEnvelope {
				emitWorkerError(startTime, totalDurationMs, waitErr.Error())
			}
			exitRuntime("llm-call", "", *traceID, cli.CodeRuntime, fmt.Errorf("call failed: %w", waitErr), "", *jsonl)
		}

		var turnBlocks []fileBlock
		var turnApplied []string
		if applyFilesEnabled {
			turnBlocks = extractFileBlocks(rc.Stdout)
			for _, b := range turnBlocks {
				targetPath := filepath.Join(scopeRoot, filepath.FromSlash(b.path))
				if _, vErr := fsops.VerifyWriteScope(targetPath, scopeRoot, allowedPaths); vErr != nil {
					if isWorkerEnvelope {
						emitWorkerError(startTime, totalDurationMs, fmt.Sprintf("file %s denied: %v", b.path, vErr))
					}
					exitRuntime("llm-call", "", *traceID, cli.CodeDenied, fmt.Errorf("file %q denied: %w", b.path, vErr), "Path must be within scopeRoot and allowedPaths", *jsonl)
				}
				if _, captured := baselineSnapshots[targetPath]; !captured {
					if info, statErr := os.Stat(targetPath); statErr == nil {
						origBytes, _ := os.ReadFile(targetPath)
						baselineSnapshots[targetPath] = fileSnapshot{
							existed: true,
							data:    origBytes,
							mode:    info.Mode(),
						}
					} else {
						baselineSnapshots[targetPath] = fileSnapshot{existed: false}
					}
				}
				if dirErr := os.MkdirAll(filepath.Dir(targetPath), 0o700); dirErr != nil {
					if isWorkerEnvelope {
						emitWorkerError(startTime, totalDurationMs, fmt.Sprintf("mkdir %s: %v", b.path, dirErr))
					}
					exitRuntime("llm-call", "", *traceID, cli.CodeIO, fmt.Errorf("mkdir for %q: %w", b.path, dirErr), "", *jsonl)
				}
				if wErr := os.WriteFile(targetPath, []byte(b.content), 0o600); wErr != nil {
					if isWorkerEnvelope {
						emitWorkerError(startTime, totalDurationMs, fmt.Sprintf("write %s: %v", b.path, wErr))
					}
					exitRuntime("llm-call", "", *traceID, cli.CodeIO, fmt.Errorf("write %q: %w", b.path, wErr), "", *jsonl)
				}
				relSlash := filepath.ToSlash(b.path)
				turnApplied = append(turnApplied, relSlash)
				if !appliedFileSet[relSlash] {
					appliedFileSet[relSlash] = true
					appliedFiles = append(appliedFiles, relSlash)
				}
			}
		}

		if verifyCmd == "" {
			break
		}

		vTimeout := timeoutDur
		if vTimeout > 60*time.Second {
			vTimeout = 60 * time.Second
		}
		vCtx, vCancel := context.WithTimeout(context.Background(), vTimeout)
		vCmd := exec.CommandContext(vCtx, "sh", "-c", verifyCmd)
		vCmd.Dir = scopeRoot
		vCmd.Env = scrubVerifyEnv(os.Environ())
		vOut, vErr := vCmd.CombinedOutput()
		vCancel()
		if vErr == nil {
			verifyPassed = true
			break
		}

		verifyPassed = false
		// Sanitize verification stdout/stderr via DLP before hashing or re-injecting into LLM prompt (#664)
		vOutStr := dispatch.SanitizeOutput(strings.TrimSpace(string(vOut)))
		if len(vOutStr) > 4096 {
			vOutStr = vOutStr[:4096] + "\n...[truncated]"
		}

		// #663: Atomic rollback of failed turn's file writes so broken artifacts never poison subsequent turns or workspace
		if applyFilesEnabled && len(baselineSnapshots) > 0 {
			restoreSnapshots(baselineSnapshots)
			appliedFiles = nil
			appliedFileSet = make(map[string]bool)
		}

		errSig := ErrorSignatureHash(vOutStr)
		treeHash := FileTreeHash(turnBlocks)

		if treeHash != "" {
			if prevTurn, seen := seenTreeHashes[treeHash]; seen {
				stagnationDetected = true
				stagnationReason = fmt.Sprintf("oscillating file state %s (matches turn %d)", treeHash, prevTurn)
				break
			}
			seenTreeHashes[treeHash] = turn
		}
		if vOutStr != "" || treeHash == "" {
			if prevTurn, seen := seenErrorSigs[errSig]; seen {
				stagnationDetected = true
				stagnationReason = fmt.Sprintf("repeated error signature %s (matches turn %d)", errSig, prevTurn)
				break
			}
			seenErrorSigs[errSig] = turn
		}

		if turn < maxTurns {
			entrySummary := fmt.Sprintf(
				"Turn %d/%d [files=%v tree=%s err_sig=%s]:\n%s",
				turn, maxTurns, turnApplied, treeHash, errSig, vOutStr,
			)
			falsifiedLedger = append(falsifiedLedger, entrySummary)
			spec.Brief = fmt.Sprintf(
				"%s\n\n--- FALSIFIED ATTEMPTS LEDGER (DO NOT REPEAT THESE STATES OR ERRORS) ---\n%s\n\n--- LATEST FAILED RESPONSE (turn %d/%d) ---\n%s\nPlease diagnose the root cause, avoid all previously falsified states, and emit complete updated ```file:path``` blocks.",
				prompt, strings.Join(falsifiedLedger, "\n\n"), turn, maxTurns, rc.Stdout,
			)
		}
	}

	// #664: Consume standalone write receipt after applying files when not managed by a worker task lifecycle
	if activeReceiptMgr != nil && activeReceiptID != "" && len(appliedFiles) > 0 && strings.TrimSpace(os.Getenv("G8S_TASK_ID")) == "" {
		_, _ = activeReceiptMgr.ValidateAndConsume(activeReceiptID, "standalone-llm-call")
	}

	modelResolved := rc.ModelResolved
	if modelResolved == "" {
		modelResolved = modelID
	}

	outcomeErrReason := ""
	if verifyCmd != "" && !verifyPassed {
		if stagnationReason != "" {
			outcomeErrReason = stagnationReason
		} else {
			outcomeErrReason = "verify command failed"
		}
	}
	_ = routing.RecordModelOutcome(pathutil.DefaultStateDir(), routing.ModelOutcomeRecord{
		Provider:       entry.Name,
		ModelRequested: modelID,
		ModelResolved:  modelResolved,
		Success:        true,
		VerifyChecked:  verifyCmd != "",
		VerifyPassed:   verifyCmd == "" || verifyPassed,
		Stagnated:      stagnationDetected,
		DurationMs:     rc.DurationMs,
		TotalTokens:    rc.TotalTokens,
		ErrorReason:    outcomeErrReason,
	})

	if envTaskID := strings.TrimSpace(os.Getenv("G8S_TASK_ID")); envTaskID != "" && !*workerEnvelopeFlag {
		recordF2CallTelemetry(envTaskID, *traceID, entry.Name, modelID, modelResolved, rc.DurationMs, rc.InputTokens, rc.OutputTokens, rc.TotalTokens, len(contextFileList), len(appliedFiles))
	}

	if isWorkerEnvelope {
		durationSec := float64(rc.DurationMs) / 1000.0
		if durationSec == 0 {
			durationSec = time.Since(startTime).Seconds()
		}
		out := map[string]any{
			"event": "result",
			"result": map[string]any{
				"status":           "SUCCESS",
				"state":            "succeeded",
				"response":         rc.Stdout,
				"duration_seconds": durationSec,
				"usage": map[string]any{
					"input_tokens":  rc.InputTokens,
					"output_tokens": rc.OutputTokens,
					"total_tokens":  rc.TotalTokens,
				},
			},
		}
		b, mErr := json.Marshal(out)
		if mErr != nil {
			exitRuntime("llm-call", "", *traceID, cli.CodeRuntime, mErr, "", *jsonl)
		}
		fmt.Println(string(b))
		return
	}

	if *jsonMode || *jsonl {
		data := map[string]any{
			"provider":        entry.Name,
			"model_requested": modelID,
			"model_resolved":  modelResolved,
			"response":        rc.Stdout,
			"duration_ms":     rc.DurationMs,
			"input_tokens":    rc.InputTokens,
			"output_tokens":   rc.OutputTokens,
			"total_tokens":    rc.TotalTokens,
			"applied_files":   appliedFiles,
			"turns_used":      turnsUsed,
		}
		if verifyCmd != "" {
			data["verify_passed"] = verifyPassed
			data["stagnation_detected"] = stagnationDetected
			if stagnationReason != "" {
				data["stagnation_reason"] = stagnationReason
			}
		}
		if len(contextFileList) > 0 {
			data["context_files"] = contextFileList
		}
		if len(hydratedSkills) > 0 {
			data["hydrated_skills"] = hydratedSkills
		}
		env := cli.NewEnvelope("llm_call", "llm-call", "", data)
		env.TraceID = *traceID
		if err := cli.WriteResponse(os.Stdout, env, *jsonl); err != nil {
			exitRuntime("llm-call", "", *traceID, cli.CodeIO, err, "", *jsonl)
		}
		return
	}

	fmt.Print(rc.Stdout)
	if !strings.HasSuffix(rc.Stdout, "\n") {
		fmt.Println()
	}
}

func recordF2CallTelemetry(taskID, traceID, providerName, modelRequested, modelResolved string, durationMs int64, inputTokens, outputTokens, totalTokens, contextFilesCount, appliedFilesCount int) {
	safeTaskID := filepath.Base(strings.TrimSpace(taskID))
	if safeTaskID == "" || safeTaskID == "." || safeTaskID == ".." || strings.ContainsAny(safeTaskID, `/\`) {
		return
	}
	stateDir := pathutil.DefaultStateDir()
	f2Dir := filepath.Join(stateDir, "f2_telemetry")
	if err := os.MkdirAll(f2Dir, 0o700); err != nil {
		return
	}
	lineObj := map[string]any{
		"task_id":             safeTaskID,
		"trace_id":            traceID,
		"provider":            providerName,
		"model_requested":     modelRequested,
		"model_resolved":      modelResolved,
		"duration_ms":         durationMs,
		"input_tokens":        inputTokens,
		"output_tokens":       outputTokens,
		"total_tokens":        totalTokens,
		"context_files_count": contextFilesCount,
		"applied_files_count": appliedFilesCount,
		"recorded_at":         time.Now().UTC().Format(time.RFC3339),
	}
	b, err := json.Marshal(lineObj)
	if err != nil {
		return
	}
	fPath := filepath.Join(f2Dir, safeTaskID+".jsonl")
	f, err := os.OpenFile(fPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}
