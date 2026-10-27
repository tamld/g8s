// Package contextpack implements pure-Go AST and lexical 4-tier context compression (FULL, SKEL, SIGS, TREE), Personalized PageRank + BM25F scoring, and a Multiple-Choice Knapsack token budget optimizer (DELTA-36).
package contextpack

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/tamld/g8s/internal/vault"
)

// Tier represents one of the 4 AST/structural elision levels in DELTA-36.
type Tier string

const (
	TierFull Tier = "FULL"
	TierSkel Tier = "SKEL"
	TierSigs Tier = "SIGS"
	TierTree Tier = "TREE"
)

// PackRequest configures a context compression run.
type PackRequest struct {
	RootDir      string   `json:"root_dir"`
	Paths        []string `json:"paths,omitempty"`
	AllowedPaths []string `json:"allowed_paths,omitempty"`
	FocusQuery   string   `json:"focus_query,omitempty"`
	BudgetTokens int      `json:"budget_tokens"`
	MaxFiles     int      `json:"max_files,omitempty"`
	VaultDBPath  string   `json:"vault_db_path,omitempty"`
}

// FileEntry records the optimization decision and compressed output for a single file.
type FileEntry struct {
	Path             string  `json:"path"`
	SelectedTier     Tier    `json:"selected_tier"`
	OriginalTokens   int     `json:"original_tokens"`
	CompressedTokens int     `json:"compressed_tokens"`
	OriginalLines    int     `json:"original_lines"`
	OutputLines      int     `json:"output_lines"`
	PageRankScore    float64 `json:"pagerank_score"`
	BM25FScore       float64 `json:"bm25f_score"`
	Utility          float64 `json:"utility"`
	SecretsRedacted  int     `json:"secrets_redacted"`
	Pinned           bool    `json:"pinned"`
	Content          string  `json:"content"`
}

// PackResult holds the complete compressed context bundle and telemetry metrics.
type PackResult struct {
	BudgetTokens      int         `json:"budget_tokens"`
	OriginalTokens    int         `json:"original_tokens"`
	TotalTokens       int         `json:"total_tokens"`
	CompressionRatio  float64     `json:"compression_ratio"`
	SavingsPercent    float64     `json:"savings_percent"`
	BudgetUtilization float64     `json:"budget_utilization"`
	SecretsRedacted   int         `json:"secrets_redacted"`
	Files             []FileEntry `json:"files"`
	KnowledgeWhispers []string    `json:"knowledge_whispers,omitempty"`
	RenderedMarkdown  string      `json:"rendered_markdown"`
}

var inlineSecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-(?:proj-|ant-)?[A-Za-z0-9_-]{16,}`),
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`AIza[0-9A-Za-z_-]{20,}`),
	regexp.MustCompile(`xox[baprs]-[0-9A-Za-z-]{16,}`),
	regexp.MustCompile(`(?i)(?:api[_-]?key|secret|password|token)\s*[:=]\s*["'][A-Za-z0-9_\-./+=]{12,}["']`),
}

// SanitizeSecretsPreservingLines redacts credentials and PEM blocks while preserving exact line count.
func SanitizeSecretsPreservingLines(src string) (string, int) {
	if src == "" {
		return "", 0
	}
	lines := strings.Split(src, "\n")
	redactedCount := 0
	inPEM := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "-----BEGIN ") && strings.Contains(trimmed, "PRIVATE KEY-----") {
			inPEM = true
			redactedCount++
			lines[i] = "// [REDACTED_PEM_PRIVATE_KEY_HEADER]"
			continue
		}
		if inPEM {
			if strings.HasPrefix(trimmed, "-----END ") && strings.Contains(trimmed, "PRIVATE KEY-----") {
				inPEM = false
				lines[i] = "// [REDACTED_PEM_PRIVATE_KEY_FOOTER]"
			} else {
				lines[i] = "// [REDACTED_PEM_PRIVATE_KEY_LINE]"
			}
			continue
		}

		updated := line
		for _, re := range inlineSecretPatterns {
			if re.MatchString(updated) {
				updated = re.ReplaceAllStringFunc(updated, func(m string) string {
					redactedCount++
					if idx := strings.IndexAny(m, ":="); idx != -1 {
						return m[:idx+1] + ` "[REDACTED_SECRET]"`
					}
					return "[REDACTED_SECRET]"
				})
			}
		}
		lines[i] = updated
	}
	return strings.Join(lines, "\n"), redactedCount
}

// EstimateTokens approximates BPE token count deterministically (~4 chars/token with word boundary weighting).
func EstimateTokens(text string) int {
	if len(text) == 0 {
		return 0
	}
	charEst := (len(text) + 3) / 4
	fields := len(strings.Fields(text))
	wordEst := int(math.Ceil(float64(fields) * 1.35))
	if wordEst > charEst {
		return (charEst + wordEst) / 2
	}
	return charEst
}

// ElideFile produces the requested Tier representation of a source file in pure Go.
func ElideFile(relPath, sanitizedSrc string, tier Tier) string {
	switch tier {
	case TierFull:
		return sanitizedSrc
	case TierSkel:
		if strings.HasSuffix(relPath, ".go") {
			if out, ok := elideGoSkel(relPath, sanitizedSrc); ok {
				return out
			}
		}
		return elideLexicalSkel(relPath, sanitizedSrc)
	case TierSigs:
		if strings.HasSuffix(relPath, ".go") {
			if out, ok := elideGoSigs(relPath, sanitizedSrc); ok {
				return out
			}
		}
		return elideLexicalSigs(relPath, sanitizedSrc)
	case TierTree:
		return elideTreeSummary(relPath, sanitizedSrc)
	default:
		return sanitizedSrc
	}
}

func elideGoSkel(relPath, src string) (string, bool) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, relPath, src, parser.ParseComments)
	if err != nil {
		return "", false
	}

	lines := strings.Split(src, "\n")
	type span struct {
		startLine int
		endLine   int
		funcName  string
	}
	var spans []span
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		startPos := fset.Position(fn.Body.Lbrace)
		endPos := fset.Position(fn.Body.Rbrace)
		if endPos.Line-startPos.Line >= 3 {
			spans = append(spans, span{
				startLine: startPos.Line,
				endLine:   endPos.Line,
				funcName:  fn.Name.Name,
			})
		}
	}
	if len(spans) == 0 {
		return src, true
	}

	sort.Slice(spans, func(i, j int) bool {
		return spans[i].startLine < spans[j].startLine
	})

	var out []string
	curLine := 1
	for _, sp := range spans {
		if sp.startLine < curLine {
			continue
		}
		for l := curLine; l <= sp.startLine && l <= len(lines); l++ {
			out = append(out, lines[l-1])
		}
		out = append(out, fmt.Sprintf("\t// [SKEL: %s body lines %d..%d elided]", sp.funcName, sp.startLine+1, sp.endLine-1))
		if sp.endLine <= len(lines) {
			out = append(out, lines[sp.endLine-1])
		}
		curLine = sp.endLine + 1
	}
	for l := curLine; l <= len(lines); l++ {
		out = append(out, lines[l-1])
	}
	return strings.Join(out, "\n"), true
}

func elideGoSigs(relPath, src string) (string, bool) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, relPath, src, parser.ParseComments)
	if err != nil {
		return "", false
	}
	lines := strings.Split(src, "\n")
	var out []string
	out = append(out, fmt.Sprintf("// File: %s (package %s)", relPath, file.Name.Name))

	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			pos := fset.Position(d.Pos())
			if d.Doc != nil && len(d.Doc.List) > 0 {
				firstDoc := strings.TrimSpace(d.Doc.List[0].Text)
				out = append(out, firstDoc)
			}
			if pos.Line >= 1 && pos.Line <= len(lines) {
				sigLine := strings.TrimSpace(lines[pos.Line-1])
				sigLine = strings.TrimSuffix(sigLine, "{")
				sigLine = strings.TrimSpace(sigLine)
				out = append(out, fmt.Sprintf("L%d: %s", pos.Line, sigLine))
			}
		case *ast.GenDecl:
			if d.Tok == token.TYPE || d.Tok == token.CONST {
				pos := fset.Position(d.Pos())
				if pos.Line >= 1 && pos.Line <= len(lines) {
					out = append(out, fmt.Sprintf("L%d: %s", pos.Line, strings.TrimSpace(lines[pos.Line-1])))
				}
			}
		}
	}
	return strings.Join(out, "\n"), true
}

var sigHeaderRe = regexp.MustCompile(`^\s*(?:export\s+)?(?:async\s+)?(?:public\s+|private\s+|protected\s+)?(?:func|function|def|class|interface|type|struct|enum|const|let|var|pub\s+fn|fn)\b`)

func elideLexicalSkel(relPath, src string) string {
	lines := strings.Split(src, "\n")
	if len(lines) <= 20 {
		return src
	}
	var out []string
	braceDepth := 0
	elidedStart := 0
	for idx, line := range lines {
		lineNum := idx + 1
		trimmed := strings.TrimSpace(line)
		openBraces := strings.Count(line, "{")
		closeBraces := strings.Count(line, "}")

		if braceDepth == 0 || sigHeaderRe.MatchString(line) || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "import ") || strings.HasPrefix(trimmed, "from ") {
			if elidedStart > 0 {
				out = append(out, fmt.Sprintf("  // [SKEL: lines %d..%d elided]", elidedStart, lineNum-1))
				elidedStart = 0
			}
			out = append(out, line)
		} else {
			if elidedStart == 0 {
				elidedStart = lineNum
			}
		}
		braceDepth += openBraces - closeBraces
		if braceDepth < 0 {
			braceDepth = 0
		}
		if braceDepth == 0 && elidedStart > 0 {
			out = append(out, fmt.Sprintf("  // [SKEL: lines %d..%d elided]", elidedStart, lineNum))
			if closeBraces > 0 {
				out = append(out, line)
			}
			elidedStart = 0
		}
	}
	if elidedStart > 0 {
		out = append(out, fmt.Sprintf("  // [SKEL: lines %d..%d elided]", elidedStart, len(lines)))
	}
	return strings.Join(out, "\n")
}

func elideLexicalSigs(relPath, src string) string {
	lines := strings.Split(src, "\n")
	var out []string
	out = append(out, fmt.Sprintf("// File: %s (lines: %d)", relPath, len(lines)))
	for idx, line := range lines {
		trimmed := strings.TrimSpace(line)
		if sigHeaderRe.MatchString(line) || strings.HasPrefix(trimmed, "# ") || strings.HasPrefix(trimmed, "## ") {
			out = append(out, fmt.Sprintf("L%d: %s", idx+1, trimmed))
		}
	}
	if len(out) == 1 && len(lines) > 0 {
		out = append(out, fmt.Sprintf("L1: %s", strings.TrimSpace(lines[0])))
	}
	return strings.Join(out, "\n")
}

func elideTreeSummary(relPath, src string) string {
	lines := strings.Split(src, "\n")
	symbols := extractDefinedSymbols(relPath, src)
	if len(symbols) > 6 {
		symbols = symbols[:6]
	}
	symList := "none"
	if len(symbols) > 0 {
		symList = strings.Join(symbols, ", ")
	}
	return fmt.Sprintf("// TREE: %s (%d lines, symbols: %s)", relPath, len(lines), symList)
}

var identTokenRe = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]{2,}`)

func extractDefinedSymbols(relPath, src string) []string {
	var syms []string
	seen := make(map[string]bool)
	addSym := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			syms = append(syms, s)
		}
	}
	if strings.HasSuffix(relPath, ".go") {
		fset := token.NewFileSet()
		if file, err := parser.ParseFile(fset, relPath, src, 0); err == nil {
			for _, decl := range file.Decls {
				switch d := decl.(type) {
				case *ast.FuncDecl:
					addSym(d.Name.Name)
				case *ast.GenDecl:
					for _, spec := range d.Specs {
						if ts, ok := spec.(*ast.TypeSpec); ok {
							addSym(ts.Name.Name)
						}
					}
				}
			}
			return syms
		}
	}
	for _, line := range strings.Split(src, "\n") {
		if sigHeaderRe.MatchString(line) {
			tokens := identTokenRe.FindAllString(line, -1)
			if len(tokens) >= 2 {
				addSym(tokens[1])
			}
		}
	}
	return syms
}

type candidateFile struct {
	relPath        string
	sanitizedSrc   string
	originalLines  int
	originalTokens int
	redacted       int
	pinned         bool
	definedSyms    []string
	pprScore       float64
	bm25Score      float64
	baseUtility    float64
	tierContent    map[Tier]string
	tierTokens     map[Tier]int
}

// Pack executes the 5-stage ContextCraft pipeline (Pre-AST Sanitizer, 4-Tier AST Elision,
// Personalized PageRank + BM25F, and Multiple-Choice Knapsack Optimizer) in pure Go.
func Pack(req PackRequest) (*PackResult, error) {
	rootDir := req.RootDir
	if rootDir == "" {
		rootDir = "."
	}
	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, fmt.Errorf("resolve root dir: %w", err)
	}
	budget := req.BudgetTokens
	if budget <= 0 {
		budget = 8000
	}
	maxFiles := req.MaxFiles
	if maxFiles <= 0 {
		maxFiles = 200
	}

	rawPaths, err := collectCandidateFiles(absRoot, req.Paths, maxFiles)
	if err != nil {
		return nil, err
	}

	candidates := make([]*candidateFile, 0, len(rawPaths))
	totalOriginalTokens := 0
	totalRedacted := 0

	for _, absPath := range rawPaths {
		data, err := os.ReadFile(absPath)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(absRoot, absPath)
		if err != nil {
			rel = filepath.Base(absPath)
		}
		rel = filepath.ToSlash(rel)

		sanitized, redacted := SanitizeSecretsPreservingLines(string(data))
		origTokens := EstimateTokens(sanitized)
		origLines := strings.Count(sanitized, "\n") + 1
		pinned := matchesAnyPattern(rel, req.AllowedPaths)

		tierContent := make(map[Tier]string, 4)
		tierTokens := make(map[Tier]int, 4)
		for _, t := range []Tier{TierFull, TierSkel, TierSigs, TierTree} {
			c := ElideFile(rel, sanitized, t)
			// Wrap with file header fence cost (+8 tokens)
			cost := EstimateTokens(c) + 8
			tierContent[t] = c
			tierTokens[t] = cost
		}
		// Ensure monotonic token costs: TREE <= SIGS <= SKEL <= FULL
		if tierTokens[TierSkel] > tierTokens[TierFull] {
			tierContent[TierSkel] = tierContent[TierFull]
			tierTokens[TierSkel] = tierTokens[TierFull]
		}
		if tierTokens[TierSigs] > tierTokens[TierSkel] {
			tierContent[TierSigs] = tierContent[TierSkel]
			tierTokens[TierSigs] = tierTokens[TierSkel]
		}
		if tierTokens[TierTree] > tierTokens[TierSigs] {
			tierContent[TierTree] = tierContent[TierSigs]
			tierTokens[TierTree] = tierTokens[TierSigs]
		}

		candidates = append(candidates, &candidateFile{
			relPath:        rel,
			sanitizedSrc:   sanitized,
			originalLines:  origLines,
			originalTokens: origTokens,
			redacted:       redacted,
			pinned:         pinned,
			definedSyms:    extractDefinedSymbols(rel, sanitized),
			tierContent:    tierContent,
			tierTokens:     tierTokens,
		})
		totalOriginalTokens += origTokens
		totalRedacted += redacted
	}

	if len(candidates) == 0 {
		return &PackResult{
			BudgetTokens: budget,
			Files:        []FileEntry{},
		}, nil
	}

	computeBM25FAndPageRank(candidates, req.FocusQuery)
	selectedTiers := solveMCKP(candidates, budget)

	var entries []FileEntry
	var mdBuilder strings.Builder
	_, _ = fmt.Fprintf(&mdBuilder, "# g8s ContextPack (Budget: %d tokens)\n\n", budget)

	totalCompressedTokens := 0
	for i, c := range candidates {
		chosen, ok := selectedTiers[i]
		if !ok {
			continue
		}
		content := c.tierContent[chosen]
		compTokens := c.tierTokens[chosen]
		if totalCompressedTokens+compTokens > budget {
			// Try falling back to a smaller tier that fits within remaining budget
			fitted := false
			for _, fallback := range []Tier{TierSkel, TierSigs, TierTree} {
				if totalCompressedTokens+c.tierTokens[fallback] <= budget {
					chosen = fallback
					content = c.tierContent[fallback]
					compTokens = c.tierTokens[fallback]
					fitted = true
					break
				}
			}
			if !fitted {
				continue
			}
		}

		totalCompressedTokens += compTokens
		outLines := strings.Count(content, "\n") + 1
		entries = append(entries, FileEntry{
			Path:             c.relPath,
			SelectedTier:     chosen,
			OriginalTokens:   c.originalTokens,
			CompressedTokens: compTokens,
			OriginalLines:    c.originalLines,
			OutputLines:      outLines,
			PageRankScore:    round4(c.pprScore),
			BM25FScore:       round4(c.bm25Score),
			Utility:          round4(c.baseUtility * tierUtilityMultiplier(chosen)),
			SecretsRedacted:  c.redacted,
			Pinned:           c.pinned,
			Content:          content,
		})

		_, _ = fmt.Fprintf(&mdBuilder, "## %s [%s]\n```\n%s\n```\n\n", c.relPath, chosen, content)
	}

	whispers := collectVerifiedVaultWhispers(absRoot, req.VaultDBPath, entries)
	if len(whispers) > 0 {
		_, _ = fmt.Fprintf(&mdBuilder, "## Verified Knowledge Whispers\n")
		for _, w := range whispers {
			_, _ = fmt.Fprintf(&mdBuilder, "- %s\n", w)
		}
		_, _ = fmt.Fprintf(&mdBuilder, "\n")
	}

	ratio := 1.0
	savings := 0.0
	if totalCompressedTokens > 0 && totalOriginalTokens > 0 {
		ratio = float64(totalOriginalTokens) / float64(totalCompressedTokens)
		if totalOriginalTokens > totalCompressedTokens {
			savings = (1.0 - float64(totalCompressedTokens)/float64(totalOriginalTokens)) * 100.0
		}
	}
	utilization := 0.0
	if budget > 0 {
		utilization = (float64(totalCompressedTokens) / float64(budget)) * 100.0
	}

	return &PackResult{
		BudgetTokens:      budget,
		OriginalTokens:    totalOriginalTokens,
		TotalTokens:       totalCompressedTokens,
		CompressionRatio:  round4(ratio),
		SavingsPercent:    round4(savings),
		BudgetUtilization: round4(utilization),
		SecretsRedacted:   totalRedacted,
		Files:             entries,
		KnowledgeWhispers: whispers,
		RenderedMarkdown:  mdBuilder.String(),
	}, nil
}

func collectVerifiedVaultWhispers(absRoot, explicitVaultDB string, entries []FileEntry) []string {
	vdbPath := strings.TrimSpace(explicitVaultDB)
	if vdbPath == "" {
		vdbPath = strings.TrimSpace(os.Getenv("G8S_VAULT_DB"))
	}
	if vdbPath == "" {
		if stateDir := strings.TrimSpace(os.Getenv("G8S_STATE_DIR")); stateDir != "" {
			vdbPath = filepath.Join(stateDir, "vault.db")
		}
	}
	if vdbPath == "" {
		return nil
	}
	if _, err := os.Stat(vdbPath); err != nil {
		return nil
	}
	v, err := vault.NewVault(vdbPath, nil)
	if err != nil {
		return nil
	}
	defer v.Close()

	ctx := context.Background()
	_, _ = v.VerifyAnchors(ctx, absRoot, true)

	maxRules := vault.ResolveMaxActiveRules()
	if maxRules <= 0 {
		return nil
	}

	seen := make(map[string]bool)
	var matched []vault.DistillationRecord
	for _, e := range entries {
		recs, err := v.MatchSpatial(ctx, e.Path, maxRules)
		if err != nil {
			continue
		}
		for _, r := range recs {
			if !seen[r.ID] {
				seen[r.ID] = true
				matched = append(matched, r)
			}
		}
	}
	return vault.FormatJITWhisper(matched)
}

func tierUtilityMultiplier(t Tier) float64 {
	switch t {
	case TierFull:
		return 1.00
	case TierSkel:
		return 0.86
	case TierSigs:
		return 0.58
	case TierTree:
		return 0.20
	default:
		return 0.10
	}
}

func computeBM25FAndPageRank(candidates []*candidateFile, focusQuery string) {
	n := len(candidates)
	if n == 0 {
		return
	}

	// 1. BM25F scoring over path (weight 2.5), symbols (weight 3.0), and body (weight 1.0)
	queryTerms := tokenizeTerms(focusQuery)
	if len(queryTerms) == 0 {
		for _, c := range candidates {
			c.bm25Score = 1.0
		}
	} else {
		avgLen := 0.0
		docLengths := make([]float64, n)
		type loweredCandidate struct {
			path string
			syms string
			body string
		}
		loweredDocs := make([]loweredCandidate, n)

		for i, c := range candidates {
			docLengths[i] = float64(len(c.sanitizedSrc)) + 1.0
			avgLen += docLengths[i]
			loweredDocs[i] = loweredCandidate{
				path: strings.ToLower(c.relPath),
				syms: strings.ToLower(strings.Join(c.definedSyms, " ")),
				body: strings.ToLower(c.sanitizedSrc),
			}
		}
		avgLen /= float64(n)

		df := make(map[string]int, len(queryTerms))
		for _, q := range queryTerms {
			for i := 0; i < n; i++ {
				if strings.Contains(loweredDocs[i].path, q) || strings.Contains(loweredDocs[i].body, q) {
					df[q]++
				}
			}
		}

		const k1 = 1.2
		const b = 0.75
		maxBM25 := 0.0
		for i, c := range candidates {
			dlNorm := 1.0 - b + b*(docLengths[i]/avgLen)

			score := 0.0
			for _, q := range queryTerms {
				tf := 0.0
				if strings.Contains(loweredDocs[i].path, q) {
					tf += 2.5
				}
				if strings.Contains(loweredDocs[i].syms, q) {
					tf += 3.0
				}
				bodyHits := strings.Count(loweredDocs[i].body, q)
				if bodyHits > 20 {
					bodyHits = 20
				}
				tf += float64(bodyHits)
				if tf == 0 {
					continue
				}
				idf := math.Log(1.0 + (float64(n)-float64(df[q])+0.5)/(float64(df[q])+0.5))
				score += idf * ((tf * (k1 + 1.0)) / (tf + k1*dlNorm))
			}
			c.bm25Score = score
			if score > maxBM25 {
				maxBM25 = score
			}
		}
		if maxBM25 > 0 {
			for _, c := range candidates {
				c.bm25Score = c.bm25Score / maxBM25
			}
		}
	}

	// 2. Symbol & Import Dependency Graph + Personalized PageRank (15 iterations)
	symOwner := make(map[string]int)
	dirOwner := make(map[string][]int)
	for i, c := range candidates {
		dir := filepath.ToSlash(filepath.Dir(c.relPath))
		dirOwner[dir] = append(dirOwner[dir], i)
		for _, sym := range c.definedSyms {
			if len(sym) >= 4 {
				symOwner[sym] = i
			}
		}
	}

	adj := make([][]int, n)
	for i, c := range candidates {
		edgeSet := make(map[int]bool)
		for sym, ownerIdx := range symOwner {
			if ownerIdx != i && strings.Contains(c.sanitizedSrc, sym) {
				edgeSet[ownerIdx] = true
			}
		}
		for dir, owners := range dirOwner {
			if dir != "." && strings.Contains(c.sanitizedSrc, dir) {
				for _, ownerIdx := range owners {
					if ownerIdx != i {
						edgeSet[ownerIdx] = true
					}
				}
			}
		}
		for target := range edgeSet {
			adj[i] = append(adj[i], target)
		}
	}

	personal := make([]float64, n)
	sumPersonal := 0.0
	for i, c := range candidates {
		w := 0.1 + c.bm25Score
		if c.pinned {
			w += 2.0
		}
		personal[i] = w
		sumPersonal += w
	}
	for i := range personal {
		personal[i] /= sumPersonal
	}

	ppr := make([]float64, n)
	copy(ppr, personal)
	const damping = 0.85
	for iter := 0; iter < 15; iter++ {
		next := make([]float64, n)
		danglingMass := 0.0
		for i := 0; i < n; i++ {
			if len(adj[i]) == 0 {
				danglingMass += ppr[i]
				continue
			}
			share := damping * ppr[i] / float64(len(adj[i]))
			for _, dst := range adj[i] {
				next[dst] += share
			}
		}
		for i := 0; i < n; i++ {
			next[i] += (1.0-damping)*personal[i] + damping*danglingMass*personal[i]
		}
		ppr = next
	}

	maxPPR := 0.0
	for _, v := range ppr {
		if v > maxPPR {
			maxPPR = v
		}
	}
	for i, c := range candidates {
		if maxPPR > 0 {
			c.pprScore = ppr[i] / maxPPR
		} else {
			c.pprScore = 1.0 / float64(n)
		}
		boost := 1.0
		if c.pinned {
			boost = 3.0
		}
		c.baseUtility = (0.45*c.pprScore + 0.55*c.bm25Score + 0.05) * boost
	}
}

func solveMCKP(candidates []*candidateFile, budget int) map[int]Tier {
	n := len(candidates)
	selected := make(map[int]Tier, n)

	// Sort candidate indices by baseUtility descending so highest-value files enter first
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool {
		if candidates[order[a]].pinned != candidates[order[b]].pinned {
			return candidates[order[a]].pinned
		}
		return candidates[order[a]].baseUtility > candidates[order[b]].baseUtility
	})

	usedTokens := 0
	for _, idx := range order {
		c := candidates[idx]
		minTier := TierTree
		if c.pinned && usedTokens+c.tierTokens[TierSkel] <= budget {
			minTier = TierSkel
		}
		if usedTokens+c.tierTokens[minTier] <= budget {
			selected[idx] = minTier
			usedTokens += c.tierTokens[minTier]
		}
	}

	// Upgrade loop via marginal utility density (DeltaUtility / DeltaTokens)
	tierLadder := []Tier{TierTree, TierSigs, TierSkel, TierFull}
	type upgradeStep struct {
		idx       int
		fromTier  Tier
		toTier    Tier
		deltaCost int
		density   float64
	}

	for {
		var best *upgradeStep
		for idx, curTier := range selected {
			c := candidates[idx]
			curPos := -1
			for p, t := range tierLadder {
				if t == curTier {
					curPos = p
					break
				}
			}
			if curPos < 0 || curPos+1 >= len(tierLadder) {
				continue
			}
			for nextPos := curPos + 1; nextPos < len(tierLadder); nextPos++ {
				nextTier := tierLadder[nextPos]
				deltaCost := c.tierTokens[nextTier] - c.tierTokens[curTier]
				if usedTokens+deltaCost > budget {
					continue
				}
				deltaUtil := c.baseUtility * (tierUtilityMultiplier(nextTier) - tierUtilityMultiplier(curTier))
				costDenom := float64(deltaCost)
				if costDenom < 1.0 {
					costDenom = 1.0
				}
				density := deltaUtil / costDenom
				if best == nil || density > best.density {
					best = &upgradeStep{
						idx:       idx,
						fromTier:  curTier,
						toTier:    nextTier,
						deltaCost: deltaCost,
						density:   density,
					}
				}
			}
		}
		if best == nil {
			break
		}
		selected[best.idx] = best.toTier
		usedTokens += best.deltaCost
	}

	return selected
}

func collectCandidateFiles(absRoot string, explicitPaths []string, maxFiles int) ([]string, error) {
	var files []string
	seen := make(map[string]bool)
	addFile := func(p string) {
		if len(files) >= maxFiles || seen[p] || !isSupportedCodeOrDocFile(p) {
			return
		}
		info, err := os.Stat(p)
		if err != nil || info.IsDir() || info.Size() == 0 || info.Size() > 512*1024 {
			return
		}
		seen[p] = true
		files = append(files, p)
	}

	walkTarget := func(target string) error {
		return filepath.WalkDir(target, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				name := d.Name()
				if name == ".git" || name == "node_modules" || name == "vendor" || name == "dist" || name == "bin" || name == ".g8s" || name == "target" || name == "build" || name == "__pycache__" || name == ".venv" || name == ".next" || name == ".cache" {
					return filepath.SkipDir
				}
				if _, tagErr := os.Stat(filepath.Join(p, "CACHEDIR.TAG")); tagErr == nil {
					return filepath.SkipDir
				}
				return nil
			}
			addFile(p)
			return nil
		})
	}

	if len(explicitPaths) > 0 {
		for _, raw := range explicitPaths {
			p := raw
			if !filepath.IsAbs(p) {
				p = filepath.Join(absRoot, p)
			}
			info, err := os.Stat(p)
			if err != nil {
				continue
			}
			if info.IsDir() {
				_ = walkTarget(p)
			} else {
				addFile(p)
			}
		}
	} else {
		if err := walkTarget(absRoot); err != nil {
			return nil, err
		}
	}

	sort.Strings(files)
	return files, nil
}

func isSupportedCodeOrDocFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go", ".py", ".js", ".ts", ".tsx", ".jsx", ".rs", ".java", ".c", ".cpp", ".h", ".cs", ".rb", ".sh", ".sql", ".md", ".yml", ".yaml":
		return true
	default:
		return false
	}
}

func matchesAnyPattern(relPath string, patterns []string) bool {
	for _, pat := range patterns {
		pat = strings.TrimSpace(filepath.ToSlash(pat))
		if pat == "" {
			continue
		}
		if pat == relPath || strings.HasPrefix(relPath, strings.TrimSuffix(pat, "/**")) || strings.HasPrefix(relPath, strings.TrimSuffix(pat, "/*")) {
			return true
		}
		if ok, _ := filepath.Match(pat, relPath); ok {
			return true
		}
	}
	return false
}

func tokenizeTerms(q string) []string {
	raw := identTokenRe.FindAllString(strings.ToLower(q), -1)
	seen := make(map[string]bool)
	var out []string
	for _, tok := range raw {
		if !seen[tok] {
			seen[tok] = true
			out = append(out, tok)
		}
	}
	return out
}

func round4(v float64) float64 {
	return math.Round(v*10000) / 10000
}
