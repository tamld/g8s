package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

type goTestEvent struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test"`
	Output  string `json:"Output"`
}

// parseGoTest counts Test-level pass/fail events from go test -json output.
// Packages reporting '[no tests to run]' contribute 0.
func parseGoTest(stdout []byte) int {
	scanner := bufio.NewScanner(bytes.NewReader(stdout))
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, len(buf))

	count := 0
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var evt goTestEvent
		if err := json.Unmarshal(line, &evt); err != nil {
			continue
		}
		if (evt.Action == "pass" || evt.Action == "fail") && evt.Test != "" {
			count++
		}
	}
	return count
}

var (
	nodeTestRegex      = regexp.MustCompile(`(?i)(?:ℹ|#)\s+tests\s+(\d+)`)
	vitestSummaryRegex = regexp.MustCompile(`(?i)Tests\s*(?::)?\s*(?:\d+\s+failed\s*(?:\||,)\s*)?(\d+)\s+passed`)
	vitestPassedRegex  = regexp.MustCompile(`(?i)(\d+)\s+passed`)
)

// parseNodeTest returns the sum of tests from node:test, tap, vitest, or jest summaries.
func parseNodeTest(output string) int {
	lower := strings.ToLower(output)
	if strings.Contains(lower, "no test files found") {
		return 0
	}

	matches := nodeTestRegex.FindAllStringSubmatch(output, -1)
	if len(matches) > 0 {
		total := 0
		for _, m := range matches {
			if n, err := strconv.Atoi(m[1]); err == nil {
				total += n
			}
		}
		return total
	}

	vMatches := vitestSummaryRegex.FindAllStringSubmatch(output, -1)
	if len(vMatches) > 0 {
		total := 0
		for _, m := range vMatches {
			if n, err := strconv.Atoi(m[1]); err == nil {
				total += n
			}
		}
		return total
	}

	pMatches := vitestPassedRegex.FindAllStringSubmatch(output, -1)
	if len(pMatches) > 0 {
		total := 0
		for _, m := range pMatches {
			if n, err := strconv.Atoi(m[1]); err == nil {
				total += n
			}
		}
		return total
	}

	return 0
}

var (
	pytestPassedRegex = regexp.MustCompile(`(?i)(\d+)\s+passed`)
	pytestFailedRegex = regexp.MustCompile(`(?i)(\d+)\s+failed`)
)

// parsePytest returns the sum of passed + failed tests from pytest output.
// Returns 0 on 'no tests ran' or 'collected 0 items'.
func parsePytest(output string) int {
	lower := strings.ToLower(output)
	if strings.Contains(lower, "no tests ran") || strings.Contains(lower, "collected 0 items") {
		return 0
	}
	total := 0
	for _, m := range pytestPassedRegex.FindAllStringSubmatch(output, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil {
			total += n
		}
	}
	for _, m := range pytestFailedRegex.FindAllStringSubmatch(output, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil {
			total += n
		}
	}
	return total
}

var cargoTestRegex = regexp.MustCompile(`(?i)test result:[^;\n]*\b(\d+)\s+passed;\s*(\d+)\s+failed`)

// parseCargoTest returns Σ(passed + failed) over all 'test result:' lines.
func parseCargoTest(output string) int {
	matches := cargoTestRegex.FindAllStringSubmatch(output, -1)
	if len(matches) == 0 {
		return 0
	}
	total := 0
	for _, m := range matches {
		passed, _ := strconv.Atoi(m[1])
		failed, _ := strconv.Atoi(m[2])
		total += (passed + failed)
	}
	return total
}

// parseDidVerify counts the number of file arguments after --files.
// If --files is missing or has 0 file args, returns 0.
func parseDidVerify(args []string) int {
	filesIdx := -1
	for i, a := range args {
		if a == "--files" || a == "-files" {
			filesIdx = i
			break
		}
	}
	if filesIdx == -1 {
		return 0
	}
	count := 0
	for i := filesIdx + 1; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			break
		}
		parts := strings.Fields(arg)
		count += len(parts)
	}
	return count
}

var offerCheckRegex = regexp.MustCompile(`(?m)^.*\bexit=\d+\b.*$`)

// parseOfferCheck returns the number of exit= gate lines.
func parseOfferCheck(output string) int {
	matches := offerCheckRegex.FindAllString(output, -1)
	return len(matches)
}

type deliverData struct {
	TaskID         string   `json:"task_id"`
	DeliverableDir string   `json:"deliverable_dir"`
	Applied        []string `json:"applied"`
	Deleted        []string `json:"deleted"`
	Skipped        []string `json:"skipped"`
	ReceiptID      string   `json:"receipt_id"`
	PrunedWorktree bool     `json:"pruned_worktree"`
}

type deliverEnvelope struct {
	V       int          `json:"v"`
	Kind    string       `json:"kind"`
	Cmd     string       `json:"cmd"`
	Data    *deliverData `json:"data"`
	TraceID string       `json:"trace_id,omitempty"`
}

// parseDeliver parses the JSON envelope output of g8s deliver, records dry_run,
// data.applied, data.deleted, data.task_id, data.receipt_id in extra,
// records git blob hashes for applied files in extra.blobs, and returns len(applied)+len(deleted).
func parseDeliver(args []string, stdout []byte, repoRoot string, extra map[string]any) int {
	dryRun := false
	for _, a := range args {
		if a == "--dry-run" || a == "-dry-run" {
			dryRun = true
			break
		}
	}
	if dryRun {
		extra["dry_run"] = true
	}

	start := bytes.IndexByte(stdout, '{')
	end := bytes.LastIndexByte(stdout, '}')
	if start >= 0 && end > start {
		var env deliverEnvelope
		if err := json.Unmarshal(stdout[start:end+1], &env); err == nil && env.Data != nil {
			if env.Data.TaskID != "" {
				extra["task_id"] = env.Data.TaskID
			}
			if env.Data.ReceiptID != "" {
				extra["receipt_id"] = env.Data.ReceiptID
			}
			if env.Data.Applied != nil {
				extra["applied"] = env.Data.Applied
			}
			if env.Data.Deleted != nil {
				extra["deleted"] = env.Data.Deleted
			}

			// Record git blob hashes for applied files in target checkout
			if len(env.Data.Applied) > 0 && repoRoot != "" {
				blobs := make(map[string]string)
				for _, f := range env.Data.Applied {
					cmd := exec.Command("git", "-C", repoRoot, "hash-object", f)
					out, err := cmd.Output()
					if err == nil {
						blobHash := strings.TrimSpace(string(out))
						if blobHash != "" {
							blobs[f] = blobHash
						}
					}
				}
				if len(blobs) > 0 {
					extra["blobs"] = blobs
				}
			}

			return len(env.Data.Applied) + len(env.Data.Deleted)
		}
	}

	return 0
}

type verifyEnvelope struct {
	Data struct {
		Results []any  `json:"results"`
		Status  string `json:"status"`
		Outcome string `json:"outcome"`
	} `json:"data"`
}

// parseG8sVerify parses verification check results from g8s verify.
func parseG8sVerify(stdout []byte) int {
	start := bytes.IndexByte(stdout, '{')
	end := bytes.LastIndexByte(stdout, '}')
	if start >= 0 && end > start {
		var env verifyEnvelope
		if err := json.Unmarshal(stdout[start:end+1], &env); err == nil {
			if len(env.Data.Results) > 0 {
				return len(env.Data.Results)
			}
		}
	}
	re := regexp.MustCompile(`(?m)^\s*\[(PASS|FAIL|SKIP|HARD|SOFT)\]`)
	matches := re.FindAll(stdout, -1)
	if len(matches) > 0 {
		return len(matches)
	}
	return 1
}

var redactRules = []struct {
	re   *regexp.Regexp
	repl string
}{
	{
		re:   regexp.MustCompile(`postgresql://[^\s"\\\x60]+`),
		repl: "postgresql://<REDACTED>",
	},
	{
		re:   regexp.MustCompile(`://[^\s"\\\x60/:]+:[^\s"\\\x60/@]+@`),
		repl: "://<REDACTED>:<REDACTED>@",
	},
	{
		re:   regexp.MustCompile(`specifically \x60[^\x60]+\x60`),
		repl: "specifically `<REDACTED>`",
	},
	{
		re:   regexp.MustCompile(`(?i)\b(password|credential|secret|token|api[_-]?key)(\s*[:=]\s*["']?)([^\s"',;}{\]\[\)\(]{3,})`),
		repl: "$1$2<REDACTED>",
	},
	{
		re:   regexp.MustCompile(`(?i)bearer\s+[a-zA-Z0-9_\-\.]{10,}`),
		repl: "Bearer <REDACTED>",
	},
	{
		re:   regexp.MustCompile(`gh[pousr]_[A-Za-z0-9_]{16,}`),
		repl: "<REDACTED_GH_TOKEN>",
	},
	{
		re:   regexp.MustCompile(`-----BEGIN [A-Z ]+ PRIVATE KEY-----[\s\S]*?-----END [A-Z ]+ PRIVATE KEY-----`),
		repl: "<REDACTED PRIVATE KEY>",
	},
}

// redactSecrets redacts credentials and secret-bearing patterns from text.
func redactSecrets(s string) string {
	for _, r := range redactRules {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	return s
}
