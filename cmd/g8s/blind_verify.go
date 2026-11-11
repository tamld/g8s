package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pterm/pterm"

	"github.com/tamld/g8s/internal/brief"
	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/config"
	"github.com/tamld/g8s/internal/provider"
)

// BlindReviewPacket is the g8s-blind-verifier input payload (DELTA-34).
// Contains ONLY SpecContract, Invariants, RawGitDiff, and MechanicalTestOutput.
// Worker self-report and model identity are stripped to defeat reviewer sycophancy.
type BlindReviewPacket struct {
	SpecContract         string `json:"spec_contract"`
	Invariants           string `json:"invariants"`
	RawGitDiff           string `json:"raw_git_diff"`
	MechanicalTestOutput string `json:"mechanical_test_output"`
}

// BlindVerdict is the structured S3-A adjudication verdict from blind verification.
type BlindVerdict struct {
	Verdict         string   `json:"verdict"`
	Passed          bool     `json:"passed"`
	Summary         string   `json:"summary,omitempty"`
	Findings        []string `json:"findings,omitempty"`
	Counterexamples []string `json:"counterexamples,omitempty"`
	ModelResolved   string   `json:"model_resolved,omitempty"`
	DurationMs      int64    `json:"duration_ms,omitempty"`
	TraceID         string   `json:"trace_id,omitempty"`
}

// inspectJSONObject validates that candidate is a single complete JSON object
// and rejects duplicate keys (case-insensitively, recursively across nested
// objects/arrays) so duplicate fields cannot overwrite contradictory values.
func inspectJSONObject(candidate string) (isJSONObject bool, dupErr error) {
	dec := json.NewDecoder(strings.NewReader(candidate))
	firstTok, err := dec.Token()
	if err != nil {
		return false, nil
	}
	if firstTok != json.Delim('{') {
		return false, nil
	}

	var walkObject func() (bool, error)
	var walkValue func() (bool, error)

	walkObject = func() (bool, error) {
		seen := make(map[string]struct{})
		for dec.More() {
			kTok, kErr := dec.Token()
			if kErr != nil {
				return false, nil
			}
			key, ok := kTok.(string)
			if !ok {
				return false, nil
			}
			norm := strings.ToLower(key)
			if trimmedKey := strings.TrimSpace(norm); trimmedKey != norm && (trimmedKey == "verdict" || trimmedKey == "passed") {
				if dupErr == nil {
					dupErr = fmt.Errorf("malformed whitespace-padded JSON key %q in blind verdict", key)
				}
			}
			if _, exists := seen[norm]; exists {
				if dupErr == nil {
					dupErr = fmt.Errorf("duplicate JSON key %q in blind verdict", key)
				}
			}
			seen[norm] = struct{}{}
			if valid, vErr := walkValue(); !valid {
				return false, vErr
			}
		}
		endTok, eErr := dec.Token()
		if eErr != nil || endTok != json.Delim('}') {
			return false, nil
		}
		return true, dupErr
	}

	walkValue = func() (bool, error) {
		tok, tErr := dec.Token()
		if tErr != nil {
			return false, nil
		}
		delim, isDelim := tok.(json.Delim)
		if !isDelim {
			return true, nil
		}
		switch delim {
		case '{':
			return walkObject()
		case '[':
			for dec.More() {
				if valid, vErr := walkValue(); !valid {
					return false, vErr
				}
			}
			endTok, eErr := dec.Token()
			if eErr != nil || endTok != json.Delim(']') {
				return false, nil
			}
			return true, dupErr
		default:
			return false, nil
		}
	}

	valid, wErr := walkObject()
	if !valid {
		return false, nil
	}
	if _, tailErr := dec.Token(); !errors.Is(tailErr, io.EOF) {
		return false, nil
	}
	return true, wErr
}

func parseBlindVerdict(raw string) (BlindVerdict, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return BlindVerdict{}, errors.New("empty model response")
	}

	// Strip optional leading <think>...</think> block emitted by reasoning models.
	if strings.HasPrefix(trimmed, "<think>") {
		endThink := strings.Index(trimmed, "</think>")
		if endThink < 0 {
			return BlindVerdict{}, errors.New("unclosed <think> block in model response")
		}
		trimmed = strings.TrimSpace(trimmed[endThink+len("</think>"):])
	}

	// Strip optional outer markdown code fence (```json ... ```) fail-closed.
	if strings.HasPrefix(trimmed, "```") {
		nl := strings.IndexByte(trimmed, '\n')
		if nl < 0 {
			return BlindVerdict{}, errors.New("unclosed markdown code fence in model response")
		}
		rest := strings.TrimSpace(trimmed[nl+1:])
		if !strings.HasSuffix(rest, "```") {
			return BlindVerdict{}, errors.New("unclosed markdown code fence in model response")
		}
		trimmed = strings.TrimSpace(strings.TrimSuffix(rest, "```"))
	}

	tryParse := func(candidate string) (BlindVerdict, bool, error) {
		isObj, dupErr := inspectJSONObject(candidate)
		if !isObj {
			return BlindVerdict{}, false, nil
		}
		if dupErr != nil {
			return BlindVerdict{}, true, dupErr
		}
		var rawFields map[string]json.RawMessage
		if err := json.Unmarshal([]byte(candidate), &rawFields); err != nil {
			return BlindVerdict{}, false, nil
		}

		var (
			hasVerdictKey bool
			verdictVal    string
			hasPassedKey  bool
			passedVal     bool
			out           BlindVerdict
		)
		for k, rawVal := range rawFields {
			trimmedRaw := strings.TrimSpace(string(rawVal))
			switch strings.ToLower(k) {
			case "verdict":
				hasVerdictKey = true
				if trimmedRaw == "null" {
					return BlindVerdict{}, true, errors.New("blind verdict field \"verdict\" cannot be null")
				}
				if err := json.Unmarshal(rawVal, &verdictVal); err != nil {
					return BlindVerdict{}, true, fmt.Errorf("invalid blind verdict \"verdict\" field: %w", err)
				}
			case "passed":
				hasPassedKey = true
				if trimmedRaw == "null" {
					return BlindVerdict{}, true, errors.New("blind verdict field \"passed\" cannot be null")
				}
				if err := json.Unmarshal(rawVal, &passedVal); err != nil {
					return BlindVerdict{}, true, fmt.Errorf("invalid blind verdict \"passed\" field: %w", err)
				}
			case "summary":
				if trimmedRaw != "null" {
					if err := json.Unmarshal(rawVal, &out.Summary); err != nil {
						return BlindVerdict{}, true, fmt.Errorf("invalid blind verdict \"summary\" field: %w", err)
					}
				}
			case "findings":
				if trimmedRaw != "null" {
					if err := json.Unmarshal(rawVal, &out.Findings); err != nil {
						return BlindVerdict{}, true, fmt.Errorf("invalid blind verdict \"findings\" field: %w", err)
					}
				}
			case "counterexamples":
				if trimmedRaw != "null" {
					if err := json.Unmarshal(rawVal, &out.Counterexamples); err != nil {
						return BlindVerdict{}, true, fmt.Errorf("invalid blind verdict \"counterexamples\" field: %w", err)
					}
				}
			}
		}
		if !hasVerdictKey && !hasPassedKey {
			return BlindVerdict{}, false, nil
		}

		if hasVerdictKey {
			verdictClean := strings.TrimSpace(verdictVal)
			switch verdictClean {
			case "ACCEPTED":
				if hasPassedKey && !passedVal {
					return BlindVerdict{}, true, fmt.Errorf("contradictory blind verdict fields: verdict=%q passed=false", verdictClean)
				}
				out.Verdict = "ACCEPTED"
				out.Passed = true
			case "REJECTED":
				if hasPassedKey && passedVal {
					return BlindVerdict{}, true, fmt.Errorf("contradictory blind verdict fields: verdict=%q passed=true", verdictClean)
				}
				out.Verdict = "REJECTED"
				out.Passed = false
			default:
				return BlindVerdict{}, true, fmt.Errorf("unrecognized blind verdict value %q", verdictClean)
			}
			return out, true, nil
		}

		if passedVal {
			out.Verdict = "ACCEPTED"
			out.Passed = true
		} else {
			out.Verdict = "REJECTED"
			out.Passed = false
		}
		return out, true, nil
	}

	v, matched, pErr := tryParse(trimmed)
	if pErr != nil {
		return BlindVerdict{}, pErr
	}
	if !matched {
		return BlindVerdict{}, errors.New("failed to parse verdict JSON from model response")
	}
	return v, nil
}

func executeBlindVerify(
	ctx context.Context,
	httpProv provider.Provider,
	packet BlindReviewPacket,
	model string,
	effort string,
	maxTokens int,
	timeout time.Duration,
	traceID string,
	bankDirs ...string,
) (BlindVerdict, error) {
	effectiveBanks := bankDirs
	if len(effectiveBanks) == 0 {
		effectiveBanks = brief.DefaultSkillDirs("")
		repoFallback := filepath.Clean(filepath.Join("..", "..", "skills"))
		if _, err := os.Stat(filepath.Join(repoFallback, "g8s-blind-verifier", "SKILL.md")); err == nil {
			effectiveBanks = append(effectiveBanks, repoFallback)
		}
	}
	skillBlock, _, hErr := brief.HydrateSkillsBlock([]string{"g8s-blind-verifier"}, 0, effectiveBanks...)
	if hErr != nil {
		return BlindVerdict{}, fmt.Errorf("hydrate g8s-blind-verifier skill: %w", hErr)
	}
	if strings.TrimSpace(skillBlock) == "" {
		return BlindVerdict{}, errors.New("hydrate g8s-blind-verifier skill returned empty block")
	}

	var sb strings.Builder
	sb.WriteString(skillBlock)
	sb.WriteString("\n\n")
	sb.WriteString(`You are an S3-A Double-Blind Falsification & Counterexample Verifier (g8s-blind-verifier).
Your input is a BlindReviewPacket containing ONLY SpecContract, Invariants, RawGitDiff (untruncated), and MechanicalTestOutput.
Worker self-reports, commit messages, and authoring model identity are stripped to defeat reviewer sycophancy.

Respond with a JSON object matching:
{
  "verdict": "ACCEPTED" | "REJECTED",
  "passed": true | false,
  "summary": "concise falsification verdict",
  "findings": ["specific finding"],
  "counterexamples": ["concrete counterexample when REJECTED"]
}

=== SPEC CONTRACT ===
`)
	sb.WriteString(packet.SpecContract)
	sb.WriteString("\n\n=== INVARIANTS ===\n")
	sb.WriteString(packet.Invariants)
	sb.WriteString("\n\n=== RAW GIT DIFF ===\n")
	sb.WriteString(packet.RawGitDiff)
	sb.WriteString("\n\n=== MECHANICAL TEST OUTPUT ===\n")
	sb.WriteString(packet.MechanicalTestOutput)
	sb.WriteString("\n=== END PACKET ===\n")

	if maxTokens <= 0 {
		maxTokens = 4096
	}
	start := time.Now()
	handle, err := httpProv.Spawn(ctx, provider.Spec{
		Brief:     sb.String(),
		Model:     model,
		Effort:    effort,
		MaxTokens: maxTokens,
		Timeout:   timeout,
		TraceID:   traceID,
	})
	if err != nil {
		return BlindVerdict{}, fmt.Errorf("spawn verifier failed: %w", err)
	}
	res, err := handle.Wait(ctx)
	if err != nil {
		return BlindVerdict{}, fmt.Errorf("verifier call failed: %w", err)
	}
	verdict, err := parseBlindVerdict(res.Stdout)
	if err != nil {
		return BlindVerdict{}, err
	}
	verdict.ModelResolved = res.ModelResolved
	verdict.DurationMs = time.Since(start).Milliseconds()
	verdict.TraceID = traceID
	return verdict, nil
}

func runBlindVerify(args []string) {
	fs := flag.NewFlagSet("blind-verify", flag.ContinueOnError)
	fs.SetOutput(os.Stdout)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	_ = actor

	baseFlag := fs.String("base", "origin/main", "git base ref when --diff is omitted")
	headFlag := fs.String("head", "", "optional git head ref when --diff is omitted")
	specFile := fs.String("spec", "", "optional path to SpecContract markdown file")
	specText := fs.String("spec-text", "", "inline SpecContract summary when --spec is omitted")
	invariantsFile := fs.String("invariants", "", "optional path to Invariants file (defaults to spec/constitution.md if present)")
	diffFile := fs.String("diff", "", "optional path to git diff file (or '-' for stdin; defaults to untruncated git diff --base)")
	testOutputFile := fs.String("test-output", "", "optional path to mechanical test output file (or '-' for stdin)")
	modelFlag := fs.String("model", "fast-agent", "verifier model identifier (default: fast-agent)")
	effortFlag := fs.String("effort", "low", "reasoning effort level for verifier (default: low)")
	maxTokensFlag := fs.Int("max-tokens", 4096, "maximum output tokens for verifier response")
	timeoutFlag := fs.String("timeout", "180s", "verification timeout")
	providerFlag := fs.String("provider", "9router", "provider to use for verification")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		exitUsage("blind-verify", "", *traceID, err.Error(), "usage: g8s blind-verify [--base origin/main] [--spec <path>|--spec-text <text>] [--test-output <path>] [--json]", *jsonl)
	}

	timeoutDur, err := time.ParseDuration(*timeoutFlag)
	if err != nil || timeoutDur <= 0 {
		timeoutDur = 180 * time.Second
	}

	// 1. SpecContract
	var specContract string
	if strings.TrimSpace(*specFile) != "" {
		b, rErr := os.ReadFile(*specFile)
		if rErr != nil {
			exitRuntime("blind-verify", "", *traceID, cli.CodeIO, fmt.Errorf("read spec: %w", rErr), "", *jsonl)
		}
		specContract = string(b)
	} else if strings.TrimSpace(*specText) != "" {
		specContract = *specText
	} else {
		specContract = "Verify the raw git diff satisfies repository contracts, introduces no regressions or security flaws, and matches mechanical test evidence."
	}

	// 2. Invariants
	var invariants string
	if strings.TrimSpace(*invariantsFile) != "" {
		b, rErr := os.ReadFile(*invariantsFile)
		if rErr != nil {
			exitRuntime("blind-verify", "", *traceID, cli.CodeIO, fmt.Errorf("read invariants: %w", rErr), "", *jsonl)
		}
		invariants = string(b)
	} else if b, rErr := os.ReadFile("spec/constitution.md"); rErr == nil {
		invariants = string(b)
	} else {
		invariants = "Zero-CGO (CGO_ENABLED=0), zero secret/path leaks, receipt-scoped mutations, explicit error handling."
	}

	// 3. RawGitDiff (untruncated — NO 20KB cap!)
	var rawDiff string
	if strings.TrimSpace(*diffFile) == "-" {
		b, rErr := io.ReadAll(os.Stdin)
		if rErr != nil {
			exitRuntime("blind-verify", "", *traceID, cli.CodeIO, fmt.Errorf("read diff from stdin: %w", rErr), "", *jsonl)
		}
		rawDiff = string(b)
	} else if strings.TrimSpace(*diffFile) != "" {
		b, rErr := os.ReadFile(*diffFile)
		if rErr != nil {
			exitRuntime("blind-verify", "", *traceID, cli.CodeIO, fmt.Errorf("read diff: %w", rErr), "", *jsonl)
		}
		rawDiff = string(b)
	} else {
		gitArgs := []string{"diff", strings.TrimSpace(*baseFlag)}
		if h := strings.TrimSpace(*headFlag); h != "" {
			gitArgs = append(gitArgs, h)
		}
		gitCtx, gitCancel := context.WithTimeout(context.Background(), 30*time.Second)
		// #nosec G204 -- fixed git binary with bounded ref arguments
		out, gErr := exec.CommandContext(gitCtx, "git", gitArgs...).Output()
		gitCancel()
		if gErr != nil {
			exitRuntime("blind-verify", "", *traceID, cli.CodeIO, fmt.Errorf("git diff %s: %w", *baseFlag, gErr), "Pass --diff <file> or verify --base ref exists", *jsonl)
		}
		rawDiff = string(out)
	}

	// 4. MechanicalTestOutput
	var testOutput string
	if strings.TrimSpace(*testOutputFile) == "-" {
		b, rErr := io.ReadAll(os.Stdin)
		if rErr != nil {
			exitRuntime("blind-verify", "", *traceID, cli.CodeIO, fmt.Errorf("read test output from stdin: %w", rErr), "", *jsonl)
		}
		testOutput = string(b)
	} else if strings.TrimSpace(*testOutputFile) != "" {
		b, rErr := os.ReadFile(*testOutputFile)
		if rErr != nil {
			exitRuntime("blind-verify", "", *traceID, cli.CodeIO, fmt.Errorf("read test output: %w", rErr), "", *jsonl)
		}
		testOutput = string(b)
	} else {
		testOutput = "PASS (mechanical test output not supplied; evaluate diff directly against SpecContract and Invariants)"
	}

	packet := BlindReviewPacket{
		SpecContract:         specContract,
		Invariants:           invariants,
		RawGitDiff:           rawDiff,
		MechanicalTestOutput: testOutput,
	}

	providersPath := os.Getenv("G8S_PROVIDERS")
	if providersPath == "" {
		if home, hErr := os.UserHomeDir(); hErr == nil && home != "" {
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
	found := false
	if cfgFile != nil {
		if p, ok := config.FindProvider(cfgFile, *providerFlag); ok && p.Class == "api_call" {
			entry = *p
			found = true
		}
	}
	if !found && strings.EqualFold(*providerFlag, "9router") {
		entry = config.ProviderEntry{
			Class:               "api_call",
			Name:                "9router",
			Protocol:            "9router",
			BaseURL:             "http://127.0.0.1:20128/v1",
			BaseURLEnv:          "NINEROUTER_URL",
			AuthEnv:             "OPENAI_API_KEY",
			AllowUnlistedModels: true,
		}
		found = true
	}
	if !found {
		exitUsage("blind-verify", "", *traceID, fmt.Sprintf("provider %q not found", *providerFlag), "Configure provider in providers.json", *jsonl)
	}

	httpProv := provider.NewHTTPProviderFromConfig(entry)
	ctx, cancel := context.WithTimeout(context.Background(), timeoutDur)
	defer cancel()

	verdict, vErr := executeBlindVerify(ctx, httpProv, packet, *modelFlag, *effortFlag, *maxTokensFlag, timeoutDur, *traceID)
	if vErr != nil {
		exitRuntime("blind-verify", "", *traceID, cli.CodeRuntime, vErr, "Ensure verifier model returns valid S3-A verdict JSON", *jsonl)
	}

	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("blind_verify", "blind-verify", "", verdict)
		env.TraceID = *traceID
		if wErr := cli.WriteResponse(os.Stdout, env, *jsonl); wErr != nil {
			exitRuntime("blind-verify", "", *traceID, cli.CodeIO, wErr, "", *jsonl)
		}
		if !verdict.Passed {
			os.Exit(1)
		}
		return
	}

	if verdict.Passed {
		pterm.Success.Printf("BLIND VERIFICATION %s\n", verdict.Verdict)
		if verdict.Summary != "" {
			fmt.Printf("Summary: %s\n", verdict.Summary)
		}
		for _, f := range verdict.Findings {
			fmt.Printf("  - %s\n", f)
		}
		return
	}

	pterm.Error.Printf("BLIND VERIFICATION %s\n", verdict.Verdict)
	if verdict.Summary != "" {
		fmt.Printf("Summary: %s\n", verdict.Summary)
	}
	for _, c := range verdict.Counterexamples {
		fmt.Printf("  [counterexample] %s\n", c)
	}
	for _, f := range verdict.Findings {
		fmt.Printf("  - %s\n", f)
	}
	os.Exit(1)
}
