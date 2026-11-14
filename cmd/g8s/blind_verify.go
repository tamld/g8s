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
	"github.com/tamld/g8s/internal/dispatch"
	"github.com/tamld/g8s/internal/provider"
	"github.com/tamld/g8s/internal/verifier"
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

const (
	// maxBlindPromptBytes bounds each individual verifier prompt safely below
	// Linux kernel MAX_ARG_STRLEN (131072 bytes) and CLI adapter user-message
	// intake limits (~200KB) so large multi-file diffs are never silently truncated.
	maxBlindPromptBytes = 100 * 1024
	maxBlindTestBytes   = 24 * 1024
)

func boundMechanicalTestOutput(out string, limit int) string {
	if limit <= 0 || len(out) <= limit {
		return out
	}
	headLen := limit / 3
	tailLen := limit - headLen - 96
	if tailLen < 0 {
		return out[:limit]
	}
	head := out[:headLen]
	if nl := strings.LastIndexByte(head, '\n'); nl > 0 {
		head = head[:nl]
	}
	tail := out[len(out)-tailLen:]
	if nl := strings.IndexByte(tail, '\n'); nl >= 0 && nl+1 < len(tail) {
		tail = tail[nl+1:]
	}
	return head + "\n...[middle mechanical test lines omitted; final summary preserved below]...\n" + tail
}

func splitOversizedFileBlock(block string, maxBytes int) []string {
	if len(block) <= maxBytes {
		return []string{block}
	}
	firstHunk := strings.Index(block, "\n@@ ")
	var header, body string
	if firstHunk > 0 {
		header = block[:firstHunk]
		body = block[firstHunk+1:]
	} else {
		body = block
	}
	budget := maxBytes - len(header) - 1
	if budget < 1024 {
		budget = 1024
	}

	// Split along hunk boundaries first, then line boundaries if a single hunk is huge.
	var rawHunks []string
	rem := body
	for {
		idx := strings.Index(rem, "\n@@ ")
		if idx < 0 {
			rawHunks = append(rawHunks, rem)
			break
		}
		rawHunks = append(rawHunks, rem[:idx])
		rem = rem[idx+1:]
	}

	var pieces []string
	for _, h := range rawHunks {
		if len(h) <= budget {
			pieces = append(pieces, h)
			continue
		}
		var lineBuf strings.Builder
		for _, ln := range strings.Split(h, "\n") {
			for len(ln) > budget {
				if lineBuf.Len() > 0 {
					pieces = append(pieces, lineBuf.String())
					lineBuf.Reset()
				}
				pieces = append(pieces, ln[:budget])
				ln = ln[budget:]
			}
			if lineBuf.Len() > 0 && lineBuf.Len()+1+len(ln) > budget {
				pieces = append(pieces, lineBuf.String())
				lineBuf.Reset()
			}
			if lineBuf.Len() > 0 {
				lineBuf.WriteByte('\n')
			}
			lineBuf.WriteString(ln)
		}
		if lineBuf.Len() > 0 {
			pieces = append(pieces, lineBuf.String())
		}
	}

	var chunks []string
	var cur strings.Builder
	for _, p := range pieces {
		if cur.Len() > 0 && cur.Len()+1+len(p) > budget {
			if header != "" {
				chunks = append(chunks, header+"\n"+cur.String())
			} else {
				chunks = append(chunks, cur.String())
			}
			cur.Reset()
		}
		if cur.Len() > 0 {
			cur.WriteByte('\n')
		}
		cur.WriteString(p)
	}
	if cur.Len() > 0 {
		if header != "" {
			chunks = append(chunks, header+"\n"+cur.String())
		} else {
			chunks = append(chunks, cur.String())
		}
	}
	return chunks
}

func splitDiffIntoFileShards(rawDiff string, maxDiffBytes int) []string {
	if maxDiffBytes < 4096 {
		maxDiffBytes = 4096
	}
	if len(rawDiff) <= maxDiffBytes {
		return []string{rawDiff}
	}

	var fileBlocks []string
	rem := rawDiff
	for {
		idx := strings.Index(rem, "\ndiff --git ")
		if idx < 0 {
			fileBlocks = append(fileBlocks, rem)
			break
		}
		fileBlocks = append(fileBlocks, rem[:idx])
		rem = rem[idx+1:]
	}

	var normalized []string
	for _, fb := range fileBlocks {
		normalized = append(normalized, splitOversizedFileBlock(fb, maxDiffBytes)...)
	}

	var shards []string
	var cur strings.Builder
	for _, blk := range normalized {
		if cur.Len() > 0 && cur.Len()+1+len(blk) > maxDiffBytes {
			shards = append(shards, cur.String())
			cur.Reset()
		}
		if cur.Len() > 0 {
			cur.WriteByte('\n')
		}
		cur.WriteString(blk)
	}
	if cur.Len() > 0 {
		shards = append(shards, cur.String())
	}
	if len(shards) == 0 {
		return []string{rawDiff}
	}
	return shards
}

func validateDisjointVerifierFamily(authorFamily, verifierFamily string) error {
	af := strings.ToLower(strings.TrimSpace(authorFamily))
	if af == "" {
		return nil
	}
	vf := strings.ToLower(strings.TrimSpace(verifierFamily))
	if vf == "" {
		return fmt.Errorf("E_DISJOINT_FAMILY: verifier model family is unknown; declare family in catalog/providers.json to verify against author family %q", af)
	}
	if vf != "multi" && af == vf {
		return fmt.Errorf("E_DISJOINT_FAMILY: S3-A verifier family %q must be disjoint from author family %q", vf, af)
	}
	return nil
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

	if maxTokens <= 0 {
		maxTokens = 4096
	}
	boundedTestOutput := boundMechanicalTestOutput(packet.MechanicalTestOutput, maxBlindTestBytes)
	overhead := len(skillBlock) + len(packet.SpecContract) + len(packet.Invariants) + len(boundedTestOutput) + 2048
	maxDiffBytes := maxBlindPromptBytes - overhead
	if maxDiffBytes < 24*1024 {
		maxDiffBytes = 24 * 1024
	}
	shards := splitDiffIntoFileShards(packet.RawGitDiff, maxDiffBytes)

	runShard := func(shardDiff string, shardIdx, shardCount int) (BlindVerdict, error) {
		var sb strings.Builder
		sb.WriteString(skillBlock)
		sb.WriteString("\n\n")
		sb.WriteString(`You are an S3-A Double-Blind Falsification & Counterexample Verifier (g8s-blind-verifier).
Your input is a BlindReviewPacket containing ONLY SpecContract, Invariants, RawGitDiff (untruncated), and MechanicalTestOutput.
Worker self-reports, commit messages, and authoring model identity are stripped to defeat reviewer sycophancy.
Do NOT invoke any tools, shell commands, or file reads; evaluate ONLY the BlindReviewPacket supplied inline below and emit ONLY the JSON object.

Respond with a JSON object matching:
{
  "verdict": "ACCEPTED" | "REJECTED",
  "passed": true | false,
  "summary": "concise falsification verdict",
  "findings": ["specific finding"],
  "counterexamples": ["concrete counterexample when REJECTED"]
}
`)
		if shardCount > 1 {
			fmt.Fprintf(&sb, "\nNOTE: RawGitDiff is partitioned cleanly along file/hunk boundaries into %d untruncated shards (this is Shard %d of %d) to prevent CLI/OS prompt truncation.\nEvaluate ONLY whether the diff hunks present in Shard %d/%d introduce bugs, regressions, security/concurrency flaws, or violate SpecContract/Invariants.\nDo NOT reject solely because other files named in SpecContract reside in sibling shards; all %d shards are combined via fail-closed AND conjunction.\n", shardCount, shardIdx+1, shardCount, shardIdx+1, shardCount, shardCount)
		}
		sb.WriteString("\n=== SPEC CONTRACT ===\n")
		sb.WriteString(packet.SpecContract)
		sb.WriteString("\n\n=== INVARIANTS ===\n")
		sb.WriteString(packet.Invariants)
		sb.WriteString("\n\n=== RAW GIT DIFF ===\n")
		sb.WriteString(shardDiff)
		sb.WriteString("\n\n=== MECHANICAL TEST OUTPUT ===\n")
		sb.WriteString(boundedTestOutput)
		sb.WriteString("\n=== END PACKET ===\n")

		spec := provider.Spec{
			Brief:     sb.String(),
			Model:     model,
			Effort:    effort,
			MaxTokens: maxTokens,
			Timeout:   timeout,
			TraceID:   traceID,
		}
		var cleanup func()
		if _, isHTTP := httpProv.(*provider.OpenAIProvider); !isHTTP {
			hookDir, _, cl, wsErr := dispatch.SetupToolPolicyNoneWorkspace()
			if wsErr != nil {
				return BlindVerdict{}, fmt.Errorf("setup tool-denied verifier workspace: %w", wsErr)
			}
			cleanup = cl
			spec.WorktreeDir = hookDir
			spec.AddDirs = []string{hookDir}
			spec.Env = map[string]string{"G8S_SANDBOX": "1"}
		}
		if cleanup != nil {
			defer cleanup()
		}

		start := time.Now()
		handle, err := httpProv.Spawn(ctx, spec)
		if err != nil {
			return BlindVerdict{}, fmt.Errorf("spawn verifier failed: %w", err)
		}
		res, err := handle.Wait(ctx)
		if err != nil {
			return BlindVerdict{}, fmt.Errorf("verifier call failed: %w", err)
		}
		v, err := parseBlindVerdict(res.Stdout)
		if err != nil {
			return BlindVerdict{}, err
		}
		v.ModelResolved = res.ModelResolved
		v.DurationMs = time.Since(start).Milliseconds()
		v.TraceID = traceID
		return v, nil
	}

	if len(shards) == 1 {
		return runShard(shards[0], 0, 1)
	}

	allPassed := true
	var (
		summaries       []string
		findings        []string
		counterexamples []string
		seenFinding     = make(map[string]bool)
		seenCE          = make(map[string]bool)
		modelResolved   string
		totalDurationMs int64
	)
	for i, shardDiff := range shards {
		v, sErr := runShard(shardDiff, i, len(shards))
		if sErr != nil {
			return BlindVerdict{}, fmt.Errorf("shard %d/%d: %w", i+1, len(shards), sErr)
		}
		totalDurationMs += v.DurationMs
		if modelResolved == "" && v.ModelResolved != "" {
			modelResolved = v.ModelResolved
		}
		if !v.Passed || v.Verdict != "ACCEPTED" {
			allPassed = false
		}
		if strings.TrimSpace(v.Summary) != "" {
			summaries = append(summaries, fmt.Sprintf("[shard %d/%d %s] %s", i+1, len(shards), v.Verdict, strings.TrimSpace(v.Summary)))
		}
		for _, f := range v.Findings {
			tag := fmt.Sprintf("[shard %d/%d] %s", i+1, len(shards), f)
			if !seenFinding[tag] {
				seenFinding[tag] = true
				findings = append(findings, tag)
			}
		}
		for _, ce := range v.Counterexamples {
			tag := fmt.Sprintf("[shard %d/%d] %s", i+1, len(shards), ce)
			if !seenCE[tag] {
				seenCE[tag] = true
				counterexamples = append(counterexamples, tag)
			}
		}
	}

	verdictStr := "ACCEPTED"
	if !allPassed {
		verdictStr = "REJECTED"
	}
	return BlindVerdict{
		Verdict:         verdictStr,
		Passed:          allPassed,
		Summary:         strings.Join(summaries, "; "),
		Findings:        findings,
		Counterexamples: counterexamples,
		ModelResolved:   modelResolved,
		DurationMs:      totalDurationMs,
		TraceID:         traceID,
	}, nil
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
	authorFamilyFlag := fs.String("author-family", "", "optional author model family slug (e.g. google, nvidia, anthropic, openai) to enforce disjoint S3-A verification")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		exitUsage("blind-verify", "", *traceID, err.Error(), "usage: g8s blind-verify [--base origin/main] [--spec <path>|--spec-text <text>] [--test-output <path>] [--author-family <slug>] [--json]", *jsonl)
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

	rawDiff = verifier.SanitizeDiffForBlindReview(rawDiff)

	packet := BlindReviewPacket{
		SpecContract:         specContract,
		Invariants:           invariants,
		RawGitDiff:           rawDiff,
		MechanicalTestOutput: testOutput,
	}

	modelExplicit := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "model" {
			modelExplicit = true
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), timeoutDur)
	defer cancel()

	resolvedProv, err := resolveCallableProvider(ctx, *providerFlag)
	if err != nil {
		exitUsage("blind-verify", "", *traceID, fmt.Sprintf("provider %q: %v", *providerFlag, err), "Configure provider in providers.json or use a registered platform_dispatch provider (agy, claude, opencode, codex, cursor)", *jsonl)
	}

	cfgFile := loadProvidersManifestFile()
	cat, _ := config.LoadDefaultCatalog()
	modelID := *modelFlag
	if !modelExplicit && !strings.EqualFold(resolvedProv.Name(), "9router") {
		if defModel := config.ResolveProviderDefaultModel(cfgFile, cat, resolvedProv.Name()); defModel != "" {
			modelID = defModel
		}
	}

	if strings.TrimSpace(*authorFamilyFlag) != "" {
		vFam, _, _ := lookupDeclarativeModelMeta(cfgFile, cat, resolvedProv.Name(), modelID)
		if dErr := validateDisjointVerifierFamily(*authorFamilyFlag, vFam); dErr != nil {
			exitUsage("blind-verify", "", *traceID, dErr.Error(), "Select a verifier --provider/--model from a vendor family disjoint from --author-family", *jsonl)
		}
	}

	verdict, vErr := executeBlindVerify(ctx, resolvedProv, packet, modelID, *effortFlag, *maxTokensFlag, timeoutDur, *traceID)
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
