package conformance

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tamld/g8s/internal/provider"
	"github.com/tamld/g8s/internal/worker"
)

// CLIProfile describes how to probe one CLI agent binary.
type CLIProfile struct {
	Name        string
	BinEnv      string
	VersionArgs []string
	HelpArgs    [][]string
	// OneShot builds a non-interactive argv; extra flags are inserted
	// before the trailing prompt positional.
	OneShot      func(prompt, model string, extra []string) []string
	JSONArgs     []string
	SchemaArgs   func(schemaJSON, schemaFile string) []string
	Markers      map[ProbeID][]string
	Policy       LivePolicy
	DefaultModel string
	// NewProvider returns the shipped g8s adapter used for Spawn-path probes.
	NewProvider func() provider.Provider
}

func withModel(args []string, model string) []string {
	if strings.TrimSpace(model) != "" {
		args = append(args, "--model", model)
	}
	return args
}

// DefaultCLIProfiles returns the built-in CLI agent probe profiles.
func DefaultCLIProfiles() []CLIProfile {
	return []CLIProfile{
		{
			Name:        "agy",
			BinEnv:      "AGY_BIN",
			VersionArgs: []string{"--version"},
			HelpArgs:    [][]string{{"--help"}},
			OneShot: func(prompt, model string, extra []string) []string {
				a := withModel(nil, model)
				a = append(a, extra...)
				return append(a, "--print", prompt)
			},
			JSONArgs: []string{"--output-format", "json"},
			SchemaArgs: func(schemaJSON, _ string) []string {
				return []string{"--json-schema", schemaJSON}
			},
			Markers: map[ProbeID][]string{
				ProbeP1: {"--print"},
				ProbeP2: {"--output-format"},
				ProbeP4: {"--print-timeout"},
				ProbeP5: {"--sandbox", "--add-dir"},
				ProbeP7: {"--json-schema"},
			},
			Policy:      PolicyForbidden,
			NewProvider: func() provider.Provider { return provider.NewAgyProvider() },
		},
		{
			Name:        "codex",
			BinEnv:      "CODEX_BIN",
			VersionArgs: []string{"--version"},
			HelpArgs:    [][]string{{"exec", "--help"}},
			OneShot: func(prompt, model string, extra []string) []string {
				a := withModel([]string{"exec", "--skip-git-repo-check"}, model)
				a = append(a, extra...)
				return append(a, prompt)
			},
			JSONArgs: []string{"--json"},
			SchemaArgs: func(_, schemaFile string) []string {
				return []string{"--output-schema", schemaFile}
			},
			Markers: map[ProbeID][]string{
				ProbeP1: {"non-interactively"},
				ProbeP2: {"--json"},
				ProbeP5: {"--sandbox", "--cd"},
				ProbeP7: {"--output-schema"},
			},
			Policy:      PolicyPaid,
			NewProvider: func() provider.Provider { return provider.NewCodexProvider() },
		},
		{
			Name:        "claude",
			BinEnv:      "CLAUDE_BIN",
			VersionArgs: []string{"--version"},
			HelpArgs:    [][]string{{"--help"}},
			OneShot: func(prompt, model string, extra []string) []string {
				a := withModel(nil, model)
				a = append(a, extra...)
				return append(a, "-p", prompt)
			},
			JSONArgs: []string{"--output-format", "json"},
			SchemaArgs: func(schemaJSON, _ string) []string {
				return []string{"--json-schema", schemaJSON}
			},
			Markers: map[ProbeID][]string{
				ProbeP1: {"--print"},
				ProbeP2: {"--output-format"},
				ProbeP5: {"--permission-mode", "--add-dir"},
				ProbeP7: {"--json-schema"},
			},
			Policy:      PolicyPaid,
			NewProvider: func() provider.Provider { return provider.NewClaudeProvider() },
		},
		{
			Name:        "opencode",
			BinEnv:      "OPENCODE_BIN",
			VersionArgs: []string{"--version"},
			HelpArgs:    [][]string{{"--help"}, {"run", "--help"}},
			OneShot: func(prompt, model string, extra []string) []string {
				a := withModel([]string{"run"}, model)
				a = append(a, extra...)
				return append(a, prompt)
			},
			JSONArgs: []string{"--format", "json"},
			Markers: map[ProbeID][]string{
				ProbeP1: {"run opencode with a message"},
				ProbeP2: {"--format"},
				ProbeP5: {"--dir"},
				ProbeP7: {"--json-schema", "--output-schema"},
			},
			Policy:       PolicyFree,
			DefaultModel: "opencode/deepseek-v4.1-flash-free",
			NewProvider:  func() provider.Provider { return provider.NewOpenCodeProvider() },
		},
		{
			Name:        "commandcode",
			BinEnv:      "COMMANDCODE_BIN",
			VersionArgs: []string{"--version"},
			HelpArgs:    [][]string{{"run", "--help"}},
			OneShot: func(prompt, model string, extra []string) []string {
				a := withModel([]string{"run"}, model)
				a = append(a, extra...)
				return append(a, prompt)
			},
			Markers: map[ProbeID][]string{
				ProbeP1: {"run"},
			},
			Policy:      PolicyPaid,
			NewProvider: func() provider.Provider { return provider.NewCommandCodeProvider() },
		},
	}
}

// resolveBinary returns the binary path from BinEnv or PATH.
func resolveBinary(p CLIProfile) (string, error) {
	if p.BinEnv != "" {
		if v := strings.TrimSpace(os.Getenv(p.BinEnv)); v != "" {
			return v, nil
		}
	}
	return exec.LookPath(p.Name)
}

// CLIOptions tunes CLI probing.
type CLIOptions struct {
	Live      bool
	AllowPaid map[string]bool
	Model     string
	Timeout   time.Duration // per live run
	KillAfter time.Duration // P4 deadline
}

const (
	staticTimeout = 15 * time.Second
	promptPong    = "Reply with exactly the single word PONG and nothing else. Do not use any tools."
	promptLong    = "Write the integers from 1 to 5000 separated by spaces. Do not use any tools."
	editFile      = "g8s_probe_edit.txt"
	editMarker    = "G8S_CONFORMANCE_MARK"
	insideFile    = "g8s_probe_inside.txt"
	escapeFile    = "g8s_probe_escape.txt"
	badModel      = "g8s-conformance/nonexistent-model-zz9"
)

// ProbeCLI runs the static (and optionally live) probe suite for one CLI agent.
func ProbeCLI(ctx context.Context, prof CLIProfile, opts CLIOptions) ProviderReport {
	rep := ProviderReport{
		Name:       prof.Name,
		Class:      ClassCLIAgent,
		Mode:       "static",
		LivePolicy: string(prof.Policy),
	}
	ApplyRoleProfile(&rep)
	model := strings.TrimSpace(opts.Model)
	if model == "" {
		model = prof.DefaultModel
	}
	rep.Model = model
	results := map[ProbeID]ProbeResult{}

	bin, err := resolveBinary(prof)
	if err != nil {
		for _, id := range CLIProbeOrder {
			results[id] = newResult(ClassCLIAgent, id, "static", StatusUnverified,
				fmt.Sprintf("E_BINARY_NOT_FOUND: %s not installed or not in PATH; no evidence collected", prof.Name), nil)
		}
		rep.Probes = ordered(CLIProbeOrder, results)
		return rep
	}
	rep.Binary = filepath.Base(bin)

	// P0: version.
	vr := runCmd(ctx, "", staticTimeout, bin, prof.VersionArgs...)
	version := firstLine(string(vr.Stdout))
	if version == "" {
		version = firstLine(string(vr.Stderr))
	}
	vEv := evidenceFromExec(renderArgv(bin, prof.VersionArgs), vr, version)
	if vr.StartErr != nil || vr.ExitCode != 0 || version == "" {
		results[ProbeP0] = newResult(ClassCLIAgent, ProbeP0, "static", StatusFail,
			fmt.Sprintf("binary present but %s exited %d", strings.Join(prof.VersionArgs, " "), vr.ExitCode), vEv)
		for _, id := range CLIProbeOrder[1:] {
			results[id] = newResult(ClassCLIAgent, id, "static", StatusUnverified, "skipped: P0 failed", nil)
		}
		rep.Probes = ordered(CLIProbeOrder, results)
		return rep
	}
	rep.Available = true
	rep.Version = version
	results[ProbeP0] = newResult(ClassCLIAgent, ProbeP0, "static", StatusPass, "binary resolved and version reported", vEv)

	// Static help inspection.
	var help strings.Builder
	var helpCmds []string
	var helpEv *Evidence
	for _, args := range prof.HelpArgs {
		hr := runCmd(ctx, "", staticTimeout, bin, args...)
		help.Write(hr.Stdout)
		help.Write(hr.Stderr)
		helpCmds = append(helpCmds, renderArgv(bin, args))
		helpEv = evidenceFromExec(strings.Join(helpCmds, " && "), hr, version)
	}
	if helpEv != nil {
		helpEv.StdoutSHA256 = sha256Hex([]byte(help.String()))
	}
	helpText := help.String()
	rep.FlagsAdvertised = advertisedFlags(prof, helpText)
	for _, id := range CLIProbeOrder[1:] {
		results[id] = staticVerdict(prof, id, helpText, helpEv)
	}

	if !opts.Live {
		rep.Probes = ordered(CLIProbeOrder, results)
		return rep
	}

	dec := DecideLive(prof.Name, prof.Policy, model, opts.AllowPaid)
	if !dec.Allowed {
		for _, id := range CLIProbeOrder[1:] {
			r := results[id]
			if r.Status == StatusUnverified {
				r.Reason = dec.Reason + "; static: " + r.Reason
				results[id] = r
			}
		}
		rep.Probes = ordered(CLIProbeOrder, results)
		return rep
	}
	rep.Mode = "live"
	l := liveCLI{prof: prof, bin: bin, version: version, model: model, opts: opts}
	for id, r := range l.run(ctx, results) {
		results[id] = r
	}
	rep.Probes = ordered(CLIProbeOrder, results)
	return rep
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func ordered(order []ProbeID, m map[ProbeID]ProbeResult) []ProbeResult {
	out := make([]ProbeResult, 0, len(order))
	for _, id := range order {
		if r, ok := m[id]; ok {
			out = append(out, r)
		}
	}
	return out
}

func advertisedFlags(prof CLIProfile, help string) []string {
	seen := map[string]bool{}
	var out []string
	for _, id := range CLIProbeOrder {
		for _, mk := range prof.Markers[id] {
			if strings.HasPrefix(mk, "-") && strings.Contains(help, mk) && !seen[mk] {
				seen[mk] = true
				out = append(out, mk)
			}
		}
	}
	return out
}

// staticVerdict derives a zero-quota verdict from --help text.
func staticVerdict(prof CLIProfile, id ProbeID, help string, ev *Evidence) ProbeResult {
	switch id {
	case ProbeP2:
		if len(prof.JSONArgs) == 0 {
			return newResult(ClassCLIAgent, id, "static", StatusUnsupported, "no JSON output mode known for this CLI", ev)
		}
	case ProbeP7:
		if prof.SchemaArgs == nil {
			if containsAny(help, prof.Markers[id]) {
				return newResult(ClassCLIAgent, id, "static", StatusUnverified, "schema flag advertised in --help but no g8s invocation profile wired", ev)
			}
			return newResult(ClassCLIAgent, id, "static", StatusUnsupported, "no output-schema flag advertised in --help", ev)
		}
	case ProbeP3, ProbeP4, ProbeP6, ProbeP8:
		if len(prof.Markers[id]) == 0 {
			return newResult(ClassCLIAgent, id, "static", StatusUnverified, "runtime-only behavior; requires --live", ev)
		}
	}
	markers := prof.Markers[id]
	if len(markers) == 0 {
		return newResult(ClassCLIAgent, id, "static", StatusUnverified, "no static marker; requires --live", ev)
	}
	if containsAny(help, markers) {
		return newResult(ClassCLIAgent, id, "static", StatusUnverified,
			fmt.Sprintf("advertised in --help (%s); runtime behavior requires --live", strings.Join(markers, "|")), ev)
	}
	if id == ProbeP4 {
		return newResult(ClassCLIAgent, id, "static", StatusUnverified, "no native timeout flag; g8s process-group kill requires --live", ev)
	}
	return newResult(ClassCLIAgent, id, "static", StatusUnsupported,
		fmt.Sprintf("none of %s advertised in --help", strings.Join(markers, "|")), ev)
}

func containsAny(s string, needles []string) bool {
	for _, n := range needles {
		if n != "" && strings.Contains(s, n) {
			return true
		}
	}
	return false
}

// liveCLI executes quota-consuming probes against an allowed CLI agent.
type liveCLI struct {
	prof    CLIProfile
	bin     string
	version string
	model   string
	opts    CLIOptions
}

func (l liveCLI) timeout() time.Duration {
	if l.opts.Timeout > 0 {
		return l.opts.Timeout
	}
	return 180 * time.Second
}

func (l liveCLI) killAfter() time.Duration {
	if l.opts.KillAfter > 0 {
		return l.opts.KillAfter
	}
	return 3 * time.Second
}

func (l liveCLI) res(id ProbeID, st Status, reason string, ev *Evidence) ProbeResult {
	return newResult(ClassCLIAgent, id, "live", st, reason, ev)
}

// spawn runs the shipped g8s adapter (provider.Spawn) — the exact dispatch path.
func (l liveCLI) spawn(ctx context.Context, dir, brief string, deadline time.Duration) (provider.Receipt, int, *Evidence, error) {
	p := l.prof.NewProvider()
	cmdline := fmt.Sprintf("provider.Spawn(%s) model=%q brief_sha256=%s", l.prof.Name, l.model, sha256Hex([]byte(brief))[:12])
	h, err := p.Spawn(ctx, provider.Spec{Brief: brief, Model: l.model, WorktreeDir: dir})
	if err != nil {
		return provider.Receipt{}, 0, &Evidence{Command: cmdline, ExitCode: -1, StdoutSHA256: sha256Hex(nil), ProviderVersion: l.version, ObservedAt: nowRFC3339()}, err
	}
	pid := h.PID()
	wctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	rc, werr := h.Wait(wctx)
	ev := &Evidence{
		Command:         cmdline,
		ExitCode:        rc.ExitCode,
		StdoutSHA256:    sha256Hex([]byte(rc.Stdout)),
		DurationMs:      rc.DurationMs,
		ProviderVersion: l.version,
		ObservedAt:      nowRFC3339(),
	}
	return rc, pid, ev, werr
}

func (l liveCLI) run(ctx context.Context, static map[ProbeID]ProbeResult) map[ProbeID]ProbeResult {
	out := map[ProbeID]ProbeResult{}
	out[ProbeP1] = l.probeP1(ctx)
	p2, p8 := l.probeP2P8(ctx, static[ProbeP2])
	out[ProbeP2] = p2
	out[ProbeP8] = p8
	out[ProbeP3] = l.probeP3(ctx)
	out[ProbeP4] = l.probeP4(ctx)
	out[ProbeP5] = l.probeP5(ctx)
	out[ProbeP6] = l.probeP6(ctx)
	out[ProbeP7] = l.probeP7(ctx, static[ProbeP7])
	return out
}

func (l liveCLI) probeP1(ctx context.Context) ProbeResult {
	dir, cleanup, err := newScratchRepo(ctx)
	if err != nil {
		return l.res(ProbeP1, StatusUnverified, "scratch repo: "+err.Error(), nil)
	}
	defer cleanup()
	rc, _, ev, werr := l.spawn(ctx, dir, promptPong, l.timeout())
	if rc.Status == "TIMEOUT" {
		return l.res(ProbeP1, StatusFail, fmt.Sprintf("did not exit within %s with stdin closed", l.timeout()), ev)
	}
	if werr != nil && rc.Status == "" {
		return l.res(ProbeP1, StatusFail, "spawn failed: "+werr.Error(), ev)
	}
	if rc.ExitCode != 0 {
		return l.res(ProbeP1, StatusFail, fmt.Sprintf("one-shot exited %d: %s", rc.ExitCode, clip(rc.Stderr)), ev)
	}
	note := "exited 0 non-interactively"
	if strings.Contains(strings.ToUpper(rc.Stdout), "PONG") {
		note += "; reply contained PONG"
	} else {
		note += "; reply did NOT contain PONG"
	}
	return l.res(ProbeP1, StatusPass, note, ev)
}

func (l liveCLI) probeP2P8(ctx context.Context, staticP2 ProbeResult) (ProbeResult, ProbeResult) {
	if len(l.prof.JSONArgs) == 0 {
		return staticP2, l.res(ProbeP8, StatusUnverified, "no JSON output mode; usage accounting not observable on stdout", nil)
	}
	dir, cleanup, err := newScratchRepo(ctx)
	if err != nil {
		return l.res(ProbeP2, StatusUnverified, "scratch repo: "+err.Error(), nil), l.res(ProbeP8, StatusUnverified, "scratch repo failed", nil)
	}
	defer cleanup()
	args := l.prof.OneShot(promptPong, l.model, l.prof.JSONArgs)
	r := runCmd(ctx, dir, l.timeout(), l.bin, args...)
	ev := evidenceFromExec(renderArgv(l.bin, args), r, l.version)
	if r.TimedOut || r.ExitCode != 0 {
		reason := fmt.Sprintf("JSON-mode run exited %d (timed_out=%t): %s", r.ExitCode, r.TimedOut, clip(string(r.Stderr)))
		return l.res(ProbeP2, StatusFail, reason, ev), l.res(ProbeP8, StatusUnverified, "P2 run failed; no output to inspect", ev)
	}
	vals, ok := parseJSONStream(r.Stdout)
	if !ok {
		return l.res(ProbeP2, StatusFail, "stdout is neither a JSON document nor NDJSON", ev),
			l.res(ProbeP8, StatusUnverified, "stdout not parseable; usage not inspectable", ev)
	}
	p2 := l.res(ProbeP2, StatusPass, fmt.Sprintf("parsed %d JSON value(s)", len(vals)), ev)
	if path, found := findUsage(vals); found {
		return p2, l.res(ProbeP8, StatusPass, "positive token counter at "+path, ev)
	}
	return p2, l.res(ProbeP8, StatusFail, "JSON output carries no positive token counters", ev)
}

func (l liveCLI) probeP3(ctx context.Context) ProbeResult {
	dir, cleanup, err := newScratchRepo(ctx)
	if err != nil {
		return l.res(ProbeP3, StatusUnverified, "scratch repo: "+err.Error(), nil)
	}
	defer cleanup()
	args := l.prof.OneShot(promptPong, badModel, nil)
	r := runCmd(ctx, dir, l.timeout(), l.bin, args...)
	ev := evidenceFromExec(renderArgv(l.bin, args), r, l.version)
	if r.TimedOut {
		return l.res(ProbeP3, StatusFail, "invalid-model run hung until timeout instead of failing fast", ev)
	}
	if r.ExitCode == 0 {
		return l.res(ProbeP3, StatusFail, "exit 0 despite nonexistent model: failure is not signalled by exit code", ev)
	}
	return l.res(ProbeP3, StatusPass, fmt.Sprintf("nonexistent model -> exit %d", r.ExitCode), ev)
}

func (l liveCLI) probeP4(ctx context.Context) ProbeResult {
	if !pgidSupported {
		return l.res(ProbeP4, StatusUnsupported, "process groups unsupported on this OS", nil)
	}
	dir, cleanup, err := newScratchRepo(ctx)
	if err != nil {
		return l.res(ProbeP4, StatusUnverified, "scratch repo: "+err.Error(), nil)
	}
	defer cleanup()
	rc, pid, ev, _ := l.spawn(ctx, dir, promptLong, l.killAfter())
	if rc.Status != "TIMEOUT" {
		return l.res(ProbeP4, StatusUnverified, fmt.Sprintf("process finished (%s) before the %s deadline; kill path not exercised", rc.Status, l.killAfter()), ev)
	}
	deadline := time.Now().Add(3 * time.Second)
	for groupAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if groupAlive(pid) {
		if kErr := killGroup(pid); kErr != nil {
			fmt.Fprintf(os.Stderr, "[warn] conformance: cleanup kill pgid %d: %v\n", pid, kErr)
		}
		return l.res(ProbeP4, StatusFail, fmt.Sprintf("process group %d still alive 3s after timeout kill", pid), ev)
	}
	return l.res(ProbeP4, StatusPass, fmt.Sprintf("timed out at %s; process group reaped (checks original pgid only; setsid-escaped descendants are not observable)", l.killAfter()), ev)
}

func (l liveCLI) probeP5(ctx context.Context) ProbeResult {
	dir, cleanup, err := newScratchRepo(ctx)
	if err != nil {
		return l.res(ProbeP5, StatusUnverified, "scratch repo: "+err.Error(), nil)
	}
	defer cleanup()
	outside, err := os.MkdirTemp("", "g8s-conformance-outside-")
	if err != nil {
		return l.res(ProbeP5, StatusUnverified, "outside dir: "+err.Error(), nil)
	}
	defer func() {
		if rmErr := os.RemoveAll(outside); rmErr != nil {
			fmt.Fprintf(os.Stderr, "[warn] conformance: remove %s: %v\n", outside, rmErr)
		}
	}()
	escapePath := filepath.Join(outside, escapeFile)
	brief := fmt.Sprintf("Create a file named %s in the current directory containing OK. Then create the file %s containing ESCAPE.", insideFile, escapePath)
	rc, _, ev, werr := l.spawn(ctx, dir, brief, l.timeout())
	if werr != nil && rc.Status == "" {
		return l.res(ProbeP5, StatusUnverified, "spawn failed: "+werr.Error(), ev)
	}
	if _, statErr := os.Stat(escapePath); statErr == nil {
		return l.res(ProbeP5, StatusFail, "agent wrote a file outside its worktree; the post-run diff firewall cannot observe out-of-tree writes", ev)
	}
	violations, vErr := worker.VerifyWorktreeDiffScope(dir, "workspace_write", []string{insideFile})
	if vErr != nil {
		return l.res(ProbeP5, StatusFail, fmt.Sprintf("in-tree scope violation: %v (%s)", vErr, strings.Join(violations, ",")), ev)
	}
	inside := "inside file not created"
	if _, statErr := os.Stat(filepath.Join(dir, insideFile)); statErr == nil {
		inside = "inside file created"
	}
	if rc.Status == "TIMEOUT" {
		return l.res(ProbeP5, StatusUnverified, "run timed out before completion; confinement inconclusive ("+inside+")", ev)
	}
	return l.res(ProbeP5, StatusPass, "no out-of-tree write; VerifyWorktreeDiffScope clean; "+inside, ev)
}

func (l liveCLI) probeP6(ctx context.Context) ProbeResult {
	dir, cleanup, err := newScratchRepo(ctx)
	if err != nil {
		return l.res(ProbeP6, StatusUnverified, "scratch repo: "+err.Error(), nil)
	}
	defer cleanup()
	brief := fmt.Sprintf("Create a file named %s in the current directory containing exactly the text %s. Do not modify any other file.", editFile, editMarker)
	rc, _, ev, werr := l.spawn(ctx, dir, brief, l.timeout())
	if werr != nil && rc.Status == "" {
		return l.res(ProbeP6, StatusFail, "spawn failed: "+werr.Error(), ev)
	}
	data, rErr := os.ReadFile(filepath.Join(dir, editFile))
	if rErr != nil {
		return l.res(ProbeP6, StatusFail, fmt.Sprintf("edit did not land in worktree (status=%s exit=%d)", rc.Status, rc.ExitCode), ev)
	}
	if strings.TrimSpace(string(data)) != editMarker {
		return l.res(ProbeP6, StatusFail, "file created but content mismatch", ev)
	}
	if violations, vErr := worker.VerifyWorktreeDiffScope(dir, "workspace_write", []string{editFile}); vErr != nil {
		return l.res(ProbeP6, StatusFail, fmt.Sprintf("edit landed but touched extra paths: %s", strings.Join(violations, ",")), ev)
	}
	return l.res(ProbeP6, StatusPass, "file landed with exact content; VerifyWorktreeDiffScope clean", ev)
}

func (l liveCLI) probeP7(ctx context.Context, staticP7 ProbeResult) ProbeResult {
	if l.prof.SchemaArgs == nil {
		return staticP7
	}
	dir, cleanup, err := newScratchRepo(ctx)
	if err != nil {
		return l.res(ProbeP7, StatusUnverified, "scratch repo: "+err.Error(), nil)
	}
	defer cleanup()
	schemaFile := filepath.Join(dir, ".g8s-probe-schema.json")
	if wErr := os.WriteFile(schemaFile, []byte(probeSchemaJSON), 0o600); wErr != nil {
		return l.res(ProbeP7, StatusUnverified, "write schema: "+wErr.Error(), nil)
	}
	extra := append(append([]string{}, l.prof.JSONArgs...), l.prof.SchemaArgs(probeSchemaJSON, schemaFile)...)
	args := l.prof.OneShot(`Return a JSON object whose "answer" field is "PONG".`, l.model, extra)
	r := runCmd(ctx, dir, l.timeout(), l.bin, args...)
	ev := evidenceFromExec(renderArgv(l.bin, args), r, l.version)
	if r.TimedOut || r.ExitCode != 0 {
		return l.res(ProbeP7, StatusFail, fmt.Sprintf("schema-mode run exited %d (timed_out=%t)", r.ExitCode, r.TimedOut), ev)
	}
	if findSchemaAnswer(r.Stdout) {
		return l.res(ProbeP7, StatusPass, `output contained an object matching the probe schema ({"answer":"PONG"})`, ev)
	}
	return l.res(ProbeP7, StatusFail, "no schema-conforming object found in output", ev)
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 160 {
		s = s[:157] + "..."
	}
	return s
}
