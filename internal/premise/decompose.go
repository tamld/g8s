package premise

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Deterministic decomposition: the input is split into sentences and each
// sentence is matched against claim-family patterns. Sentences that yield no
// repo-probeable claim but read as declarative assertions become prose claims
// (UNVERIFIED or OBSERVATIONAL). Imperative sentences ("Implement X") are
// instructions, not premises, and are skipped.

var sentenceBoundary = regexp.MustCompile(`[.!?;]+(?:\s+|$)|\n+`)

const (
	cmdNoun   = `\s+(?:sub)?commands?\b`
	articleRe = `(?:an?\s+|the\s+|any\s+)?`
)

var (
	negVerbs = `(?:is\s+missing|are\s+missing|missing|lacks|lack|lacking|has\s+no|have\s+no|doesn'?t\s+have|does\s+not\s+have|do\s+not\s+have|don'?t\s+have|without|no\s+longer\s+has)`

	reCmdNegList     = regexp.MustCompile(`(?i)\b` + negVerbs + `\s+` + articleRe + `(.+?)` + cmdNoun)
	reCmdNegTrailing = regexp.MustCompile(`(?i)(?:^|\b(?:the|a|an)\s+)(.+?)` + cmdNoun + `\s+(?:is|are|was|were)\s+(?:still\s+)?(?:missing|absent|not\s+implemented|unimplemented|not\s+available|not\s+present|gone|removed)\b`)
	reCmdNegNo       = regexp.MustCompile(`(?i)\b(?:there\s+is\s+no|there'?s\s+no|there\s+are\s+no)\s+(.+?)` + cmdNoun)
	reCmdNegTick     = regexp.MustCompile("(?i)`(?:g8s\\s+)?([a-z][a-z0-9-]*)`(?:\\s+(?:sub)?command)?\\s+(?:(?:does\\s+not|doesn'?t)\\s+exist|(?:is|are)\\s+(?:missing|absent|not\\s+implemented|unimplemented))")
	reCmdNegBareCLI  = regexp.MustCompile(`(?i)\b(?:cli|g8s)\b.*?\b` + negVerbs + `\s+` + articleRe + `(.+)$`)

	reCmdPosList     = regexp.MustCompile(`(?i)\b(?:has|have|supports?|provides?|exposes?|includes?|ships?|offers?)\s+` + articleRe + `(.+?)` + cmdNoun)
	reCmdPosTrailing = regexp.MustCompile(`(?i)(?:^|\b(?:the|a|an)\s+)(.+?)` + cmdNoun + `\s+(?:already\s+)?(?:exists?|is\s+available|are\s+available|is\s+implemented|are\s+implemented|is\s+registered|are\s+registered|works?)\b`)
	reCmdPosTick     = regexp.MustCompile("(?i)`(?:g8s\\s+)?([a-z][a-z0-9-]*)`(?:\\s+(?:sub)?command)?\\s+(?:already\\s+)?(?:exists|is\\s+available|is\\s+implemented|is\\s+registered|works)\\b")

	reListSplit    = regexp.MustCompile(`(?i)\s*(?:,|\band\b|\bor\b|\bnor\b|&|/|\+)\s*`)
	reCommandToken = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)

	reCommandCount = regexp.MustCompile(`(?i)\b(\d+)\s+(?:top-level\s+|registered\s+|cli\s+)*commands\b`)
	reDeltaCount   = regexp.MustCompile(`(?i)\b(\d+)\s+(?:openspec\s+)?(?:deltas|delta\s+specs|specifications)\b`)

	reNegExist = regexp.MustCompile(`(?i)\b(?:does\s+not\s+exist|doesn'?t\s+exist|do\s+not\s+exist|don'?t\s+exist|is\s+missing|are\s+missing|missing|not\s+found|absent|no\s+such|was\s+deleted|were\s+deleted|has\s+been\s+(?:deleted|removed)|is\s+gone|not\s+registered|unregistered|not\s+defined|undefined|is\s+not\s+implemented|isn'?t\s+implemented)\b`)
	rePosExist = regexp.MustCompile(`(?i)\b(?:exists?|is\s+present|are\s+present|is\s+registered|are\s+registered|is\s+defined|are\s+defined|is\s+applied|is\s+implemented|already\s+(?:exists?|has|contains|ships))\b`)

	rePassVerb = regexp.MustCompile(`(?i)\b(?:pass(?:es|ing|ed)?|green|succeed(?:s|ed)?)\b`)
	reFailVerb = regexp.MustCompile(`(?i)\b(?:fail(?:s|ing|ed)?|red|broken|panic(?:s|ked)?)\b`)

	reTestName    = regexp.MustCompile(`\b(Test[A-Z0-9_][A-Za-z0-9_]*)\b`)
	reFeatureID   = regexp.MustCompile(`\b(FEAT-[A-Z0-9]+(?:-[A-Z0-9]+)*)\b`)
	reDeltaID     = regexp.MustCompile(`\b(DELTA-\d+)\b`)
	reSymbolNoun  = regexp.MustCompile("(?i)\\b(?:func(?:tion)?|method|type|struct|interface|symbol|const(?:ant)?|var(?:iable)?)\\s+(`?)([A-Za-z_][A-Za-z0-9_]*(?:\\.[A-Za-z_][A-Za-z0-9_]*)?)(\\(\\))?`?")
	reSymbolCall  = regexp.MustCompile("`([A-Za-z_][A-Za-z0-9_]*(?:\\.[A-Za-z_][A-Za-z0-9_]*)?)\\(\\)`")
	reBacktick    = regexp.MustCompile("`([^`\\s]+)`")
	reBarePath    = regexp.MustCompile(`(?:^|[\s(\["'])((?:\.{1,2}/)?[A-Za-z0-9_.*-]+(?:/[A-Za-z0-9_.*-]+)+/?|[A-Za-z0-9_*-]+\.(?:go|md|ya?ml|json|sh|toml|txt|py|mod|sum))`)
	reKnownExt    = regexp.MustCompile(`\.(?:go|md|ya?ml|json|sh|toml|txt|py|mod|sum)$`)
	reFileNoun    = regexp.MustCompile(`(?i)\b(?:file|files|directory|directories|dir|folder|path|package|spec)\b`)
	reAssertVerb  = regexp.MustCompile(`(?i)\b(?:is|are|was|were|has|have|had|lacks?|exists?|works?|fails?|passes?|broke|broken|supports?|never|always|already|missing|contains?|returns?)\b`)
	reImperative  = regexp.MustCompile(`(?i)^(?:please\s+)?(?:implement|add|create|fix|write|update|run|make|do|don'?t|ensure|use|return|register|wire|open|read|delete|remove|refactor|build|test|check|verify|let'?s|let|go|try|avoid|keep|start|stop|merge|ship|push|commit|deploy|skip|just|now|then|also|assume|treat|re-?implement|rewrite|harvest|report)\b`)
	reEvidenceTag = regexp.MustCompile(`(?i)(exit\s+code\s+\d+|sha256[:=]?\s*[0-9a-f]{8,}|--- (?:PASS|FAIL)|\bPASS\b|\bFAIL\b|\bok\s+\S+\s+\d|\bCI\s+run\b|\brun\s+#\d+|\blogs?\s+(?:show|shows|showed)\b)`)
)

// commandStopwords are list items that are never command names.
var commandStopwords = map[string]bool{
	"useful": true, "many": true, "several": true, "other": true, "some": true, "all": true,
	"basic": true, "core": true, "more": true, "additional": true, "various": true, "few": true,
	"following": true, "these": true, "those": true, "such": true, "lifecycle": true,
	"management": true, "admin": true, "new": true, "existing": true, "any": true, "both": true,
	"no": true, "required": true, "needed": true, "proper": true, "important": true, "key": true,
	"main": true, "two": true, "three": true, "four": true, "five": true, "six": true,
	"cli": true, "g8s": true, "the": true, "its": true, "their": true,
	"is": true, "are": true, "missing": true, "has": true, "have": true, "with": true,
}

// commandFillers are dropped from list items before the one-token check.
var commandFillers = map[string]bool{
	"the": true, "a": true, "an": true, "g8s": true, "cli": true, "cli's": true, "g8s's": true,
	"its": true, "top-level": true, "any": true, "both": true, "either": true, "also": true,
	"subcommand": true, "subcommands": true, "command": true, "commands": true, "still": true,
}

type sentence struct {
	text     string
	question bool
}

func splitSentences(text string) []sentence {
	var out []sentence
	last := 0
	for _, loc := range sentenceBoundary.FindAllStringIndex(text, -1) {
		seg := text[last:loc[0]]
		term := text[loc[0]:loc[1]]
		out = appendSentence(out, seg, strings.Contains(term, "?"))
		last = loc[1]
	}
	out = appendSentence(out, text[last:], false)
	return out
}

func appendSentence(out []sentence, seg string, question bool) []sentence {
	seg = strings.TrimSpace(seg)
	seg = strings.TrimLeft(seg, "-*•> \t0123456789)")
	seg = strings.TrimSpace(seg)
	if len(seg) < 3 {
		return out
	}
	return append(out, sentence{text: seg, question: question})
}

// Decompose extracts atomic claims from text without any network access.
// Returned claims carry no status; Check assigns it via deterministic probes.
func Decompose(text string) []Claim {
	var claims []Claim
	seen := map[string]bool{}
	add := func(c Claim) {
		c.Source = "deterministic"
		k := claimKey(c)
		if seen[k] {
			return
		}
		seen[k] = true
		claims = append(claims, c)
	}
	for _, s := range splitSentences(text) {
		structured := extractStructured(s.text)
		for _, c := range structured {
			add(c)
		}
		if len(structured) > 0 || s.question {
			continue
		}
		if isDeclarative(s.text) {
			add(Claim{Text: s.text, Sentence: s.text, Kind: KindProse, Asserted: AssertStated})
		}
	}
	return claims
}

func isDeclarative(s string) bool {
	if reImperative.MatchString(s) {
		return false
	}
	return reAssertVerb.MatchString(s)
}

func extractStructured(s string) []Claim {
	var out []Claim
	out = append(out, extractCommandClaims(s)...)
	out = append(out, extractCountClaims(s)...)
	out = append(out, extractTestClaims(s)...)
	out = append(out, extractIDClaims(s)...)
	out = append(out, extractSymbolClaims(s)...)
	out = append(out, extractFileClaims(s)...)
	return out
}

func commandClaim(s, name string, a Assertion) Claim {
	verb := "exists"
	if a == AssertAbsent {
		verb = "is missing"
	}
	return Claim{
		Text:     fmt.Sprintf("CLI command `%s` %s", name, verb),
		Sentence: s,
		Kind:     KindCommandExists,
		Subject:  name,
		Asserted: a,
	}
}

func extractCommandClaims(s string) []Claim {
	var out []Claim
	collect := func(re *regexp.Regexp, a Assertion, list bool) {
		for _, m := range re.FindAllStringSubmatch(s, -1) {
			if list {
				for _, name := range parseCommandList(m[1]) {
					out = append(out, commandClaim(s, name, a))
				}
				continue
			}
			name := strings.ToLower(m[1])
			if reCommandToken.MatchString(name) {
				out = append(out, commandClaim(s, name, a))
			}
		}
	}
	collect(reCmdNegTick, AssertAbsent, false)
	collect(reCmdNegList, AssertAbsent, true)
	collect(reCmdNegTrailing, AssertAbsent, true)
	collect(reCmdNegNo, AssertAbsent, true)
	if len(out) == 0 {
		collect(reCmdNegBareCLI, AssertAbsent, true)
	}
	if len(out) > 0 {
		// A sentence that already asserts absence is not re-read for
		// presence ("doesn't have X" also contains "have X").
		return out
	}
	collect(reCmdPosTick, AssertPresent, false)
	collect(reCmdPosList, AssertPresent, true)
	collect(reCmdPosTrailing, AssertPresent, true)
	return out
}

// parseCommandList turns "init, doctor and `update`" into command tokens.
// Items that do not reduce to exactly one command-shaped token are dropped
// so prose never becomes a fabricated command claim.
func parseCommandList(raw string) []string {
	var out []string
	for _, item := range reListSplit.Split(raw, -1) {
		item = strings.NewReplacer("`", " ", "\"", " ", "'s ", " ", "“", " ", "”", " ").Replace(item + " ")
		var kept []string
		for _, tok := range strings.Fields(strings.ToLower(item)) {
			tok = strings.Trim(tok, "'.,:()[]")
			if tok == "" || commandFillers[tok] {
				continue
			}
			kept = append(kept, tok)
		}
		if len(kept) != 1 {
			continue
		}
		tok := kept[0]
		if commandStopwords[tok] || !reCommandToken.MatchString(tok) {
			continue
		}
		out = append(out, tok)
	}
	return out
}

func extractCountClaims(s string) []Claim {
	var out []Claim
	lower := strings.ToLower(s)
	if strings.Contains(lower, "g8s") || strings.Contains(lower, "cli") {
		for _, m := range reCommandCount.FindAllStringSubmatch(s, -1) {
			if n, err := strconv.Atoi(m[1]); err == nil {
				out = append(out, Claim{
					Text: fmt.Sprintf("CLI exposes %d top-level commands", n), Sentence: s,
					Kind: KindCommandCount, Subject: "commands", Asserted: AssertEquals, Value: intPtr(n),
				})
			}
		}
	}
	for _, m := range reDeltaCount.FindAllStringSubmatch(s, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil {
			out = append(out, Claim{
				Text: fmt.Sprintf("repository registers %d OpenSpec deltas", n), Sentence: s,
				Kind: KindDeltaCount, Subject: "deltas", Asserted: AssertEquals, Value: intPtr(n),
			})
		}
	}
	return out
}

// existencePolarity returns AssertAbsent, AssertPresent, or "" when the
// sentence makes no existence assertion. Negation wins over presence.
func existencePolarity(s string) Assertion {
	if reNegExist.MatchString(s) {
		return AssertAbsent
	}
	if rePosExist.MatchString(s) {
		return AssertPresent
	}
	return ""
}

func extractTestClaims(s string) []Claim {
	var out []Claim
	names := reTestName.FindAllStringSubmatch(s, -1)
	if len(names) == 0 {
		return nil
	}
	var a Assertion
	switch {
	case reFailVerb.MatchString(s):
		a = AssertFails
	case rePassVerb.MatchString(s):
		a = AssertPasses
	}
	for _, m := range names {
		if a != "" {
			out = append(out, Claim{
				Text: fmt.Sprintf("test %s %s", m[1], a), Sentence: s,
				Kind: KindTestPasses, Subject: m[1], Asserted: a,
			})
			continue
		}
		if p := existencePolarity(s); p != "" {
			out = append(out, symbolClaim(s, m[1], p))
		}
	}
	return out
}

func extractIDClaims(s string) []Claim {
	p := existencePolarity(s)
	if p == "" {
		// Bare references ("implement per DELTA-42") are not existence
		// premises: a brief may legitimately create the id it names.
		return nil
	}
	var out []Claim
	verb := "exists"
	if p == AssertAbsent {
		verb = "is missing"
	}
	for _, m := range reFeatureID.FindAllStringSubmatch(s, -1) {
		out = append(out, Claim{Text: fmt.Sprintf("feature %s %s", m[1], verb), Sentence: s, Kind: KindFeatureExists, Subject: m[1], Asserted: p})
	}
	for _, m := range reDeltaID.FindAllStringSubmatch(s, -1) {
		out = append(out, Claim{Text: fmt.Sprintf("OpenSpec %s %s", m[1], verb), Sentence: s, Kind: KindDeltaExists, Subject: m[1], Asserted: p})
	}
	return out
}

func symbolClaim(s, name string, a Assertion) Claim {
	verb := "exists"
	if a == AssertAbsent {
		verb = "is missing"
	}
	return Claim{Text: fmt.Sprintf("Go symbol %s %s", name, verb), Sentence: s, Kind: KindSymbolExists, Subject: name, Asserted: a}
}

var symbolStopwords = map[string]bool{
	"that": true, "which": true, "is": true, "the": true, "a": true, "an": true, "for": true,
	"to": true, "of": true, "in": true, "and": true, "or": true, "safety": true, "check": true,
	"checks": true, "system": true, "named": true, "called": true, "this": true, "it": true,
}

func extractSymbolClaims(s string) []Claim {
	p := existencePolarity(s)
	if p == "" {
		return nil
	}
	var out []Claim
	for _, m := range reSymbolNoun.FindAllStringSubmatch(s, -1) {
		name := m[2]
		ticked := m[1] == "`"
		hasCall := m[3] != ""
		if symbolStopwords[strings.ToLower(name)] || strings.HasPrefix(name, "Test") {
			continue
		}
		if !ticked && !hasCall && !strings.ContainsAny(name, "ABCDEFGHIJKLMNOPQRSTUVWXYZ_") {
			continue
		}
		out = append(out, symbolClaim(s, name, p))
	}
	for _, m := range reSymbolCall.FindAllStringSubmatch(s, -1) {
		if strings.HasPrefix(m[1], "Test") {
			continue
		}
		out = append(out, symbolClaim(s, m[1], p))
	}
	return out
}

func extractFileClaims(s string) []Claim {
	p := existencePolarity(s)
	if p == "" {
		return nil
	}
	fileContext := reFileNoun.MatchString(s)
	candidates := map[string]bool{}
	var order []string
	push := func(tok string) {
		tok = strings.Trim(tok, "\"'()[],:")
		if tok == "" || candidates[tok] || strings.Contains(tok, "://") || strings.HasPrefix(tok, "-") {
			return
		}
		if strings.HasSuffix(tok, "()") || strings.HasPrefix(tok, "g8s ") {
			return
		}
		if !reKnownExt.MatchString(tok) && !strings.Contains(tok, "/") {
			return
		}
		if !reKnownExt.MatchString(tok) && !fileContext && !strings.HasPrefix(tok, "./") && !strings.HasSuffix(tok, "/") {
			return
		}
		candidates[tok] = true
		order = append(order, tok)
	}
	for _, m := range reBacktick.FindAllStringSubmatch(s, -1) {
		push(m[1])
	}
	for _, m := range reBarePath.FindAllStringSubmatch(s, -1) {
		push(m[1])
	}
	var out []Claim
	verb := "exists"
	if p == AssertAbsent {
		verb = "is missing"
	}
	for _, tok := range order {
		out = append(out, Claim{Text: fmt.Sprintf("path %s %s", tok, verb), Sentence: s, Kind: KindFileExists, Subject: tok, Asserted: p})
	}
	return out
}

// classifyProse assigns the evidence tier for an unprobeable assertion.
func classifyProse(text string) (Status, string) {
	if reEvidenceTag.MatchString(text) {
		return StatusObservational, "cites runtime telemetry the gate did not re-execute"
	}
	return StatusUnverified, "prose assertion with no attached evidence; not repo-probeable"
}
