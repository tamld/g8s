package premise

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// CompleteFunc performs one bounded LLM completion. It is injected by the
// caller (e.g. a 9router-backed provider) so this package stays offline and
// network-free by default.
type CompleteFunc func(ctx context.Context, system, prompt string) (string, error)

// maxLLMClaims bounds how many candidate claims an LLM may contribute.
const maxLLMClaims = 64

// LLMSystemPrompt instructs the decomposer. The model only extracts claims;
// it never decides truth — every candidate is re-probed deterministically.
const LLMSystemPrompt = `You split operator requests and supervisor briefs into atomic factual claims.
Do NOT judge whether a claim is true. Do NOT add commentary.
Return ONLY a JSON object: {"claims":[{"text":"...","kind":"...","subject":"...","asserted":"...","value":0}]}
kind is one of: command_exists, file_exists, symbol_exists, test_passes, feature_exists, delta_exists, command_count, delta_count, prose.
asserted is one of: present, absent, passes, fails, equals, stated.
subject: command name, repo-relative path, Go symbol, test function name, feature id (FEAT-...), or delta id (DELTA-NN).
value: only for *_count kinds. Instructions (imperatives) are not claims.`

type llmClaim struct {
	Text     string `json:"text"`
	Kind     string `json:"kind"`
	Subject  string `json:"subject"`
	Asserted string `json:"asserted"`
	Value    *int   `json:"value"`
}

var validKinds = map[Kind]bool{
	KindCommandExists: true, KindFileExists: true, KindSymbolExists: true, KindTestPasses: true,
	KindFeatureExists: true, KindDeltaExists: true, KindCommandCount: true, KindDeltaCount: true, KindProse: true,
}

var validAssertions = map[Kind][]Assertion{
	KindCommandExists: {AssertPresent, AssertAbsent},
	KindFileExists:    {AssertPresent, AssertAbsent},
	KindSymbolExists:  {AssertPresent, AssertAbsent},
	KindFeatureExists: {AssertPresent, AssertAbsent},
	KindDeltaExists:   {AssertPresent, AssertAbsent},
	KindTestPasses:    {AssertPasses, AssertFails},
	KindCommandCount:  {AssertEquals},
	KindDeltaCount:    {AssertEquals},
	KindProse:         {AssertStated},
}

// LLMDecompose asks the injected completion function for candidate claims
// and returns only schema-valid ones (statuses unset, Source "llm").
func LLMDecompose(ctx context.Context, complete CompleteFunc, text string) ([]Claim, error) {
	if complete == nil {
		return nil, errors.New("no completion function configured")
	}
	raw, err := complete(ctx, LLMSystemPrompt, text)
	if err != nil {
		return nil, err
	}
	obj, err := extractJSONObject(raw)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Claims []llmClaim `json:"claims"`
	}
	if err := json.Unmarshal([]byte(obj), &parsed); err != nil {
		return nil, fmt.Errorf("decode llm claims: %w", err)
	}
	var out []Claim
	for _, lc := range parsed.Claims {
		if len(out) >= maxLLMClaims {
			break
		}
		if c, ok := sanitizeLLMClaim(lc); ok {
			out = append(out, c)
		}
	}
	return out, nil
}

func sanitizeLLMClaim(lc llmClaim) (Claim, bool) {
	kind := Kind(strings.TrimSpace(strings.ToLower(lc.Kind)))
	if !validKinds[kind] {
		return Claim{}, false
	}
	asserted := Assertion(strings.TrimSpace(strings.ToLower(lc.Asserted)))
	allowed := false
	for _, a := range validAssertions[kind] {
		if a == asserted {
			allowed = true
		}
	}
	if !allowed {
		return Claim{}, false
	}
	text := oneLine(lc.Text, 300)
	subject := oneLine(lc.Subject, 200)
	if text == "" {
		return Claim{}, false
	}
	switch kind {
	case KindCommandExists:
		subject = strings.ToLower(strings.TrimPrefix(strings.Trim(subject, "` "), "g8s "))
		if !reCommandToken.MatchString(subject) {
			return Claim{}, false
		}
	case KindCommandCount, KindDeltaCount:
		if lc.Value == nil || *lc.Value < 0 {
			return Claim{}, false
		}
	case KindProse:
		subject = ""
	default:
		subject = strings.Trim(subject, "` ")
		if subject == "" {
			return Claim{}, false
		}
	}
	c := Claim{Text: text, Sentence: text, Kind: kind, Subject: subject, Asserted: asserted, Source: "llm"}
	if kind == KindCommandCount || kind == KindDeltaCount {
		c.Value = intPtr(*lc.Value)
	}
	return c, true
}

func oneLine(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > limit {
		s = s[:limit]
	}
	return s
}

// extractJSONObject tolerates code fences and prose around one JSON object.
func extractJSONObject(raw string) (string, error) {
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end <= start {
		return "", errors.New("llm response contains no JSON object")
	}
	return raw[start : end+1], nil
}
