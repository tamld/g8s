package premise

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tamld/g8s/internal/featuremap"
)

const (
	defaultTestTimeout = 120 * time.Second
	maxScannedGoFiles  = 20000
	maxSymbolHits      = 5
)

// skipDirs are never walked when scanning Go sources.
var skipDirs = map[string]bool{
	".git": true, "vendor": true, "node_modules": true, "reference": true,
	"testdata": true, "worktrees": true, "bin": true, "dist": true,
}

type prober struct {
	root string
	opts Options

	regLoaded bool
	reg       *commandRegistry
	regErr    error

	goLoaded bool
	goFiles  []string
	goErr    error

	warnings []string
}

func newProber(root string, opts Options) *prober {
	return &prober{root: root, opts: opts}
}

func (p *prober) classify(ctx context.Context, c *Claim) {
	switch c.Kind {
	case KindCommandExists:
		p.probeCommand(c)
	case KindCommandCount:
		p.probeCommandCount(c)
	case KindFileExists:
		p.probeFile(c)
	case KindSymbolExists:
		p.probeSymbol(c)
	case KindTestPasses:
		p.probeTest(ctx, c)
	case KindFeatureExists:
		p.probeFeature(c)
	case KindDeltaExists, KindDeltaCount:
		p.probeDelta(c)
	default:
		c.Kind = KindProse
		if c.Asserted == "" {
			c.Asserted = AssertStated
		}
		c.Status, c.Note = classifyProse(c.Text)
	}
}

// verdict maps an observation onto MECHANICAL_VERIFIED or CONTRADICTED.
func verdict(c *Claim, observed string, matches bool) {
	c.Observed = observed
	if matches {
		c.Status = StatusMechanicalVerified
		return
	}
	c.Status = StatusContradicted
}

func unverified(c *Claim, note string) {
	c.Status = StatusUnverified
	c.Note = note
}

// ---------------------------------------------------------------------------
// CLI command registry (go/ast over cmd/<name>/main.go).

type commandRegistry struct {
	source   string
	commands map[string]int
	clauses  int
	help     bool
	digest   string
	listing  string
	literals map[string][]string
}

func (p *prober) registry() (*commandRegistry, error) {
	if p.regLoaded {
		return p.reg, p.regErr
	}
	p.regLoaded = true
	p.reg, p.regErr = loadCommandRegistry(p.root)
	return p.reg, p.regErr
}

func findRegistryFile(root string) (string, error) {
	preferred := filepath.Join(root, "cmd", "g8s", "main.go")
	if _, err := os.Stat(preferred); err == nil {
		return preferred, nil
	}
	matches, err := filepath.Glob(filepath.Join(root, "cmd", "*", "main.go"))
	if err != nil {
		return "", fmt.Errorf("glob cmd/*/main.go: %w", err)
	}
	if len(matches) == 0 {
		return "", errors.New("no CLI registry (cmd/*/main.go) found under repo")
	}
	sort.Strings(matches)
	return matches[0], nil
}

func loadCommandRegistry(root string) (*commandRegistry, error) {
	mainFile, err := findRegistryFile(root)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, mainFile, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", relPath(root, mainFile), err)
	}
	var mainFn *ast.FuncDecl
	for _, d := range file.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == "main" {
			mainFn = fn
			break
		}
	}
	if mainFn == nil || mainFn.Body == nil {
		return nil, fmt.Errorf("%s has no func main", relPath(root, mainFile))
	}
	best := dispatchSwitch(mainFn.Body)
	if best == nil {
		return nil, fmt.Errorf("%s: func main has no string-literal dispatch switch", relPath(root, mainFile))
	}
	reg := &commandRegistry{
		source:   relPath(root, mainFile),
		commands: map[string]int{},
		literals: map[string][]string{},
	}
	var lines []string
	for _, stmt := range best.Body.List {
		cc, ok := stmt.(*ast.CaseClause)
		if !ok || len(cc.List) == 0 {
			continue
		}
		reg.clauses++
		for _, e := range cc.List {
			lit, ok := stringLiteral(e)
			if !ok {
				continue
			}
			if lit == "help" {
				reg.help = true
			}
			line := fset.Position(e.Pos()).Line
			if _, dup := reg.commands[lit]; !dup {
				reg.commands[lit] = line
				lines = append(lines, fmt.Sprintf("%d:%s", line, lit))
			}
		}
	}
	reg.listing = strings.Join(lines, "\n")
	reg.digest = sha256Hex(reg.listing)
	collectCaseLiterals(root, filepath.Dir(mainFile), reg)
	return reg, nil
}

// dispatchSwitch returns the switch in fn body with the most string cases.
func dispatchSwitch(body *ast.BlockStmt) *ast.SwitchStmt {
	var best *ast.SwitchStmt
	bestCount := 0
	ast.Inspect(body, func(n ast.Node) bool {
		sw, ok := n.(*ast.SwitchStmt)
		if !ok {
			return true
		}
		count := 0
		for _, stmt := range sw.Body.List {
			if cc, ok := stmt.(*ast.CaseClause); ok {
				for _, e := range cc.List {
					if _, ok := stringLiteral(e); ok {
						count++
					}
				}
			}
		}
		if count > bestCount {
			best, bestCount = sw, count
		}
		return true
	})
	return best
}

func stringLiteral(e ast.Expr) (string, bool) {
	bl, ok := e.(*ast.BasicLit)
	if !ok || bl.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(bl.Value)
	if err != nil {
		return "", false
	}
	return s, true
}

// collectCaseLiterals records string case literals of every non-main
// function in the registry package (subcommand awareness for notes only).
func collectCaseLiterals(root, dir string, reg *commandRegistry) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil || (fn.Recv == nil && fn.Name.Name == "main") {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				cc, ok := n.(*ast.CaseClause)
				if !ok {
					return true
				}
				for _, e := range cc.List {
					if lit, ok := stringLiteral(e); ok && lit != "" && !strings.HasPrefix(lit, "-") {
						where := fmt.Sprintf("%s:%d (%s)", relPath(root, path), fset.Position(e.Pos()).Line, fn.Name.Name)
						reg.literals[lit] = append(reg.literals[lit], where)
					}
				}
				return true
			})
		}
	}
}

func (p *prober) probeCommand(c *Claim) {
	reg, err := p.registry()
	if err != nil {
		unverified(c, "command registry unavailable: "+err.Error())
		return
	}
	name := strings.ToLower(c.Subject)
	command := fmt.Sprintf("go/ast parse %s: func main dispatch switch, case literal %q", reg.source, name)
	reproduce := fmt.Sprintf("g8s search -F 'case \"%s\"' %s", name, reg.source)
	line, present := reg.commands[name]
	var output string
	if present {
		output = fmt.Sprintf("%s:%d: case %q (top-level command registered)", reg.source, line, name)
	} else {
		output = fmt.Sprintf("%s: no top-level case %q among %d dispatch clauses (registry sha256=%s)", reg.source, name, reg.clauses, reg.digest)
	}
	c.Evidence = newEvidence("cli_registry_ast", command, reproduce, output, 0)
	observed := "absent"
	if present {
		observed = "present"
	}
	verdict(c, observed, (c.Asserted == AssertPresent) == present)
	if !present {
		if where := reg.literals[name]; len(where) > 0 {
			c.Note = fmt.Sprintf("not a top-level command, but %q is a subcommand/case literal at %s", name, strings.Join(firstN(where, 3), ", "))
		}
	}
	if c.Status == StatusContradicted && present {
		c.Note = fmt.Sprintf("premise is false: `g8s %s` is already registered at %s:%d; do not re-implement it", name, reg.source, line)
	}
}

func (p *prober) probeCommandCount(c *Claim) {
	reg, err := p.registry()
	if err != nil {
		unverified(c, "command registry unavailable: "+err.Error())
		return
	}
	if c.Value == nil {
		unverified(c, "count claim without a numeric value")
		return
	}
	withoutHelp := reg.clauses
	if reg.help {
		withoutHelp--
	}
	output := fmt.Sprintf("%s: %d dispatch clauses (%d excluding help); registry sha256=%s", reg.source, reg.clauses, withoutHelp, reg.digest)
	c.Evidence = newEvidence("cli_registry_ast", "go/ast parse "+reg.source+": count func main dispatch case clauses (aliases collapsed)", "g8s help", output, 0)
	verdict(c, strconv.Itoa(reg.clauses), *c.Value == reg.clauses || *c.Value == withoutHelp)
}

// ---------------------------------------------------------------------------
// Files and globs.

// insideRoot resolves a claim path and rejects anything escaping the repo.
func (p *prober) insideRoot(raw string) (string, bool) {
	clean := filepath.Clean(filepath.FromSlash(strings.TrimPrefix(raw, "./")))
	if filepath.IsAbs(clean) {
		return "", false
	}
	full := filepath.Join(p.root, clean)
	rel, err := filepath.Rel(p.root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return full, true
}

func (p *prober) probeFile(c *Claim) {
	full, ok := p.insideRoot(c.Subject)
	if !ok {
		unverified(c, "path is outside the repository scope; not probed")
		return
	}
	rel := relPath(p.root, full)
	if strings.Contains(c.Subject, "*") {
		matches, err := globRepo(p.root, filepath.ToSlash(rel))
		if err != nil {
			unverified(c, "glob failed: "+err.Error())
			return
		}
		output := fmt.Sprintf("glob %s: %d match(es)", rel, len(matches))
		if len(matches) > 0 {
			output += "\n" + strings.Join(firstN(matches, 10), "\n")
		}
		c.Evidence = newEvidence("repo_glob", "walk repo, match "+filepath.ToSlash(rel), fmt.Sprintf("g8s search --files -g '%s' .", filepath.ToSlash(rel)), output, 0)
		present := len(matches) > 0
		verdict(c, presence(present), (c.Asserted == AssertPresent) == present)
		return
	}
	info, err := os.Lstat(full)
	present := err == nil
	output := fmt.Sprintf("lstat %s: not found", rel)
	if present {
		kind := "file"
		if info.IsDir() {
			kind = "directory"
		}
		output = fmt.Sprintf("lstat %s: %s, %d bytes", rel, kind, info.Size())
	}
	c.Evidence = newEvidence("repo_stat", "os.Lstat("+filepath.ToSlash(rel)+")", "ls -ld -- "+filepath.ToSlash(rel), output, 0)
	verdict(c, presence(present), (c.Asserted == AssertPresent) == present)
}

func presence(b bool) string {
	if b {
		return "present"
	}
	return "absent"
}

// globRepo matches a slash pattern (supporting ** for any depth).
func globRepo(root, pattern string) ([]string, error) {
	re, err := globToRegexp(pattern)
	if err != nil {
		return nil, err
	}
	var out []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() && path != root && (d.Name() == ".git" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		rel := filepath.ToSlash(relPath(root, path))
		if re.MatchString(rel) {
			out = append(out, rel)
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

func globToRegexp(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		switch {
		case ch == '*' && i+1 < len(pattern) && pattern[i+1] == '*':
			b.WriteString(".*")
			i++
			if i+1 < len(pattern) && pattern[i+1] == '/' {
				i++
				b.WriteString("/?")
			}
		case ch == '*':
			b.WriteString("[^/]*")
		case ch == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(ch)))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// ---------------------------------------------------------------------------
// Go symbols and tests.

func (p *prober) goSources() ([]string, error) {
	if p.goLoaded {
		return p.goFiles, p.goErr
	}
	p.goLoaded = true
	err := filepath.WalkDir(p.root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if path != p.root && (skipDirs[name] || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") {
			if len(p.goFiles) >= maxScannedGoFiles {
				return filepath.SkipAll
			}
			p.goFiles = append(p.goFiles, path)
		}
		return nil
	})
	sort.Strings(p.goFiles)
	p.goErr = err
	return p.goFiles, err
}

type symbolHit struct {
	path string
	line int
	text string
}

func (p *prober) findDecl(re *regexp.Regexp, pkg string, testsOnly bool) ([]symbolHit, error) {
	files, err := p.goSources()
	if err != nil {
		return nil, err
	}
	var hits []symbolHit
	for _, f := range files {
		if testsOnly && !strings.HasSuffix(f, "_test.go") {
			continue
		}
		if pkg != "" && filepath.Base(filepath.Dir(f)) != pkg {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for i, line := range bytes.Split(data, []byte("\n")) {
			if re.Match(line) {
				hits = append(hits, symbolHit{path: relPath(p.root, f), line: i + 1, text: strings.TrimSpace(string(line))})
			}
		}
	}
	return hits, nil
}

func formatHits(hits []symbolHit) string {
	var lines []string
	for i, h := range hits {
		if i >= maxSymbolHits {
			lines = append(lines, fmt.Sprintf("... %d more", len(hits)-maxSymbolHits))
			break
		}
		lines = append(lines, fmt.Sprintf("%s:%d: %s", filepath.ToSlash(h.path), h.line, h.text))
	}
	return strings.Join(lines, "\n")
}

func symbolRegexp(name string) *regexp.Regexp {
	q := regexp.QuoteMeta(name)
	pattern := `^\s*(?:func\s+(?:\([^)]*\)\s*)?` + q + `\s*[\[(]|type\s+` + q + `\b|(?:const|var)\s+` + q + `\b)`
	if name != "" && name[0] >= 'A' && name[0] <= 'Z' {
		pattern += `|^\t` + q + `(?:\s+[A-Za-z_][\w.]*)?\s*=`
	}
	return regexp.MustCompile(pattern)
}

func splitQualified(subject string) (pkg, name string) {
	if i := strings.LastIndex(subject, "."); i > 0 {
		return subject[:i], subject[i+1:]
	}
	return "", subject
}

func (p *prober) probeSymbol(c *Claim) {
	pkg, name := splitQualified(c.Subject)
	re := symbolRegexp(name)
	hits, err := p.findDecl(re, pkg, false)
	if err != nil {
		unverified(c, "go source scan failed: "+err.Error())
		return
	}
	scope := "**/*.go"
	if pkg != "" {
		scope = "**/" + pkg + "/*.go"
	}
	output := fmt.Sprintf("declaration scan for %s in %s: %d hit(s)", c.Subject, scope, len(hits))
	if len(hits) > 0 {
		output += "\n" + formatHits(hits)
	}
	c.Evidence = newEvidence("go_decl_scan", "regexp "+re.String()+" over "+scope, fmt.Sprintf("g8s search -g '*.go' '(func|type|const|var).*\\b%s\\b' .", name), output, 0)
	present := len(hits) > 0
	verdict(c, presence(present), (c.Asserted == AssertPresent) == present)
}

func (p *prober) probeTest(ctx context.Context, c *Claim) {
	re := regexp.MustCompile(`^func\s+` + regexp.QuoteMeta(c.Subject) + `\s*\(`)
	hits, err := p.findDecl(re, "", true)
	if err != nil {
		unverified(c, "go source scan failed: "+err.Error())
		return
	}
	locate := fmt.Sprintf("test declaration scan for %s in **/*_test.go: %d hit(s)", c.Subject, len(hits))
	if len(hits) > 0 {
		locate += "\n" + formatHits(hits)
	}
	if len(hits) == 0 {
		c.Evidence = newEvidence("go_test_decl_scan", "regexp "+re.String()+" over **/*_test.go", fmt.Sprintf("g8s search -g '*_test.go' '^func %s\\(' .", c.Subject), locate, 0)
		c.Observed = "absent"
		c.Status = StatusContradicted
		c.Note = fmt.Sprintf("premise is false: no test function %s exists, so it cannot %s", c.Subject, strings.TrimSuffix(string(c.Asserted), "es"))
		return
	}
	if !p.opts.RunTests {
		c.Evidence = newEvidence("go_test_decl_scan", "regexp "+re.String()+" over **/*_test.go", fmt.Sprintf("go test -count=1 -run '^%s$' ./%s", c.Subject, filepath.ToSlash(filepath.Dir(hits[0].path))), locate, 0)
		c.Observed = "exists; not executed"
		unverified(c, "test exists but pass/fail was not executed (use --run-tests)")
		return
	}
	pkgDir := "./" + filepath.ToSlash(filepath.Dir(hits[0].path))
	timeout := p.opts.TestTimeout
	if timeout <= 0 {
		timeout = defaultTestTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := []string{"test", "-count=1", "-run", "^" + c.Subject + "$", pkgDir}
	cmd := exec.CommandContext(runCtx, "go", args...)
	cmd.Dir = p.root
	out, runErr := cmd.CombinedOutput()
	exitCode := 0
	if runErr != nil {
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			exitCode = ee.ExitCode()
		} else {
			exitCode = -1
		}
	}
	text := tail(string(out), 1500)
	c.Evidence = newEvidence("go_test_run", "go "+strings.Join(args, " "), "go "+strings.Join(args, " "), text, exitCode)
	if runCtx.Err() != nil {
		unverified(c, "test execution timed out after "+timeout.String())
		return
	}
	if strings.Contains(string(out), "no tests to run") {
		unverified(c, "go test matched no tests")
		return
	}
	passed := exitCode == 0
	observed := "fails"
	if passed {
		observed = "passes"
	}
	verdict(c, observed, (c.Asserted == AssertPasses) == passed)
}

// ---------------------------------------------------------------------------
// Feature map and OpenSpec deltas.

func (p *prober) probeFeature(c *Claim) {
	rel := filepath.Join(".g8s", "feature-map.yml")
	fm, err := featuremap.Load(filepath.Join(p.root, rel))
	if err != nil {
		unverified(c, "feature map unavailable: "+err.Error())
		return
	}
	ids := make([]string, 0, len(fm.Features))
	for _, f := range fm.Features {
		ids = append(ids, f.ID)
	}
	sort.Strings(ids)
	_, present := fm.GetFeature(c.Subject)
	output := fmt.Sprintf("%s: %d features; %s %s", filepath.ToSlash(rel), len(ids), c.Subject, presence(present))
	c.Evidence = newEvidence("feature_map_lookup", "featuremap.Load("+filepath.ToSlash(rel)+").GetFeature("+c.Subject+")", "g8s feature inspect "+c.Subject+" --json", output+"\nids sha256="+sha256Hex(strings.Join(ids, "\n")), 0)
	verdict(c, presence(present), (c.Asserted == AssertPresent) == present)
}

type manifestSpecs struct {
	Specifications []struct {
		ID   string `json:"id"`
		Path string `json:"path"`
	} `json:"specifications"`
}

func deltaNumber(id string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimPrefix(strings.ToUpper(id), "DELTA-"))
	return n, err == nil
}

func (p *prober) probeDelta(c *Claim) {
	data, err := os.ReadFile(filepath.Join(p.root, "manifest.json"))
	if err != nil {
		unverified(c, "manifest.json unavailable: "+err.Error())
		return
	}
	var m manifestSpecs
	if err := json.Unmarshal(data, &m); err != nil {
		unverified(c, "manifest.json unparsable: "+err.Error())
		return
	}
	ids := make([]string, 0, len(m.Specifications))
	for _, s := range m.Specifications {
		ids = append(ids, s.ID)
	}
	digest := sha256Hex(strings.Join(ids, "\n"))
	if c.Kind == KindDeltaCount {
		if c.Value == nil {
			unverified(c, "count claim without a numeric value")
			return
		}
		output := fmt.Sprintf("manifest.json specifications: %d entries (ids sha256=%s)", len(ids), digest)
		c.Evidence = newEvidence("manifest_specifications", "json manifest.json .specifications | length", "g8s select --file manifest.json specifications", output, 0)
		verdict(c, strconv.Itoa(len(ids)), *c.Value == len(ids))
		return
	}
	want, ok := deltaNumber(c.Subject)
	if !ok {
		unverified(c, "malformed delta id")
		return
	}
	found := ""
	for _, s := range m.Specifications {
		if n, ok := deltaNumber(s.ID); ok && n == want {
			found = s.ID + " -> " + s.Path
			break
		}
	}
	present := found != ""
	output := fmt.Sprintf("manifest.json specifications: %d entries; %s %s", len(ids), c.Subject, presence(present))
	if present {
		output += " (" + found + ")"
	}
	c.Evidence = newEvidence("manifest_specifications", "json manifest.json .specifications[].id == "+c.Subject, "g8s search -F '\""+c.Subject+"\"' manifest.json", output, 0)
	verdict(c, presence(present), (c.Asserted == AssertPresent) == present)
}

// ---------------------------------------------------------------------------
// helpers

func relPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}

func firstN(items []string, n int) []string {
	if len(items) <= n {
		return items
	}
	return items[:n]
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "...[head truncated]\n" + s[len(s)-n:]
}
