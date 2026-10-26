package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// HTTPTarget describes an OpenAI-compatible HTTP LLM endpoint to probe.
// BaseURL and credentials are never persisted to the matrix.
type HTTPTarget struct {
	Name     string
	BaseURL  string
	AuthEnv  string
	Headers  map[string]string
	Model    string
	Policy   LivePolicy
	Client   *http.Client
	Protocol string
}

// HTTPOptions tunes HTTP probing.
type HTTPOptions struct {
	Live               bool
	AllowPaid          map[string]bool
	ContextProbeTokens int
}

type httpProber struct {
	t       HTTPTarget
	version string
}

type httpResp struct {
	status   int
	body     []byte
	duration time.Duration
	err      error
}

func (h httpProber) client() *http.Client {
	if h.t.Client != nil {
		return h.t.Client
	}
	return &http.Client{Timeout: 120 * time.Second}
}

func (h httpProber) do(ctx context.Context, method, path string, payload any) httpResp {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return httpResp{err: err}
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(h.t.BaseURL, "/")+path, body)
	if err != nil {
		return httpResp{err: err}
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if h.t.AuthEnv != "" {
		if tok := os.Getenv(h.t.AuthEnv); tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	for k, v := range h.t.Headers {
		req.Header.Set(k, v)
	}
	start := time.Now()
	resp, err := h.client().Do(req)
	if err != nil {
		return httpResp{err: err, duration: time.Since(start)}
	}
	defer func() {
		if cErr := resp.Body.Close(); cErr != nil {
			fmt.Fprintf(os.Stderr, "[warn] conformance: close body: %v\n", cErr)
		}
	}()
	raw, rErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return httpResp{status: resp.StatusCode, body: raw, duration: time.Since(start), err: rErr}
}

func (h httpProber) ev(cmd string, r httpResp) *Evidence {
	exit := 0
	if r.err != nil || r.status < 200 || r.status >= 300 {
		exit = 1
	}
	return &Evidence{
		Command:         cmd,
		ExitCode:        exit,
		HTTPStatus:      r.status,
		StdoutSHA256:    sha256Hex(r.body),
		DurationMs:      r.duration.Milliseconds(),
		ProviderVersion: h.version,
		ObservedAt:      nowRFC3339(),
	}
}

func (h httpProber) res(id ProbeID, mode string, st Status, reason string, ev *Evidence) ProbeResult {
	return newResult(ClassHTTPLLM, id, mode, st, reason, ev)
}

type modelInfo struct {
	ids    []string
	claims map[string]int
}

func parseModels(body []byte) (modelInfo, error) {
	var data struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return modelInfo{}, err
	}
	mi := modelInfo{claims: map[string]int{}}
	for _, m := range data.Data {
		id, ok := m["id"].(string)
		if !ok || id == "" {
			continue
		}
		mi.ids = append(mi.ids, id)
		if c := contextClaim(m); c > 0 {
			mi.claims[id] = c
		}
	}
	return mi, nil
}

// contextClaim extracts a declared context window from a /models entry.
func contextClaim(m map[string]any) int {
	for _, k := range []string{"context_length", "context_window", "max_context_length", "max_model_len", "max_input_tokens"} {
		if v, ok := m[k].(float64); ok && v > 0 {
			return int(v)
		}
	}
	if tp, ok := m["top_provider"].(map[string]any); ok {
		if v, ok := tp["context_length"].(float64); ok && v > 0 {
			return int(v)
		}
	}
	return 0
}

// ProbeHTTP runs the HTTP LLM probe suite. Static mode issues only the
// zero-quota GET /models request; live mode adds chat-completion probes.
func ProbeHTTP(ctx context.Context, t HTTPTarget, opts HTTPOptions) ProviderReport {
	h := httpProber{t: t, version: "openai-compatible-v1"}
	rep := ProviderReport{
		Name:       t.Name,
		Class:      ClassHTTPLLM,
		Binary:     "http",
		Version:    h.version,
		Mode:       "static",
		LivePolicy: string(t.Policy),
	}
	ApplyRoleProfile(&rep)
	results := map[ProbeID]ProbeResult{}

	mr := h.do(ctx, http.MethodGet, "/models", nil)
	mEv := h.ev("GET {base_url}/models", mr)
	if mr.err != nil && mr.status == 0 {
		for _, id := range HTTPProbeOrder {
			results[id] = h.res(id, "static", StatusUnverified, "E_ENDPOINT_UNREACHABLE: no response from endpoint; no evidence collected", nil)
		}
		results[ProbeP0] = h.res(ProbeP0, "static", StatusUnverified, "E_ENDPOINT_UNREACHABLE: "+mr.err.Error(), mEv)
		rep.Probes = ordered(HTTPProbeOrder, results)
		return rep
	}
	if mr.status >= 500 {
		results[ProbeP0] = h.res(ProbeP0, "static", StatusFail, fmt.Sprintf("endpoint responded %d", mr.status), mEv)
	} else {
		results[ProbeP0] = h.res(ProbeP0, "static", StatusPass, fmt.Sprintf("endpoint responded %d", mr.status), mEv)
		rep.Available = true
	}

	var mi modelInfo
	if mr.status == http.StatusOK {
		var err error
		mi, err = parseModels(mr.body)
		switch {
		case err != nil:
			results[ProbeH1] = h.res(ProbeH1, "static", StatusFail, "GET /models body is not an OpenAI model list: "+err.Error(), mEv)
		case len(mi.ids) == 0:
			results[ProbeH1] = h.res(ProbeH1, "static", StatusFail, "GET /models returned zero models", mEv)
		default:
			results[ProbeH1] = h.res(ProbeH1, "static", StatusPass, fmt.Sprintf("listed %d models; target model %q listed=%t", len(mi.ids), t.Model, contains(mi.ids, t.Model)), mEv)
		}
	} else {
		results[ProbeH1] = h.res(ProbeH1, "static", StatusFail, fmt.Sprintf("GET /models returned %d", mr.status), mEv)
	}

	model := t.Model
	if model == "" && len(mi.ids) > 0 && t.Policy == PolicyFree && !strings.EqualFold(t.Name, "9router") {
		model = mi.ids[0]
	}
	rep.Model = model
	claim := mi.claims[model]

	results[ProbeP4] = h.res(ProbeP4, "static", StatusUnsupported, "no subprocess; request deadline is enforced client-side by the g8s HTTP client", nil)
	results[ProbeP5] = h.res(ProbeP5, "static", StatusUnsupported, "HTTP LLM has no filesystem access; edits are applied by g8s under a WriteReceipt", nil)
	results[ProbeP6] = h.res(ProbeP6, "static", StatusUnsupported, "HTTP LLM cannot edit files directly (see g8s llm-call --apply-files)", nil)
	claimNote := "no context window claim exposed by /models"
	if claim > 0 {
		claimNote = fmt.Sprintf("claimed context window %d tokens", claim)
	}
	for _, id := range []ProbeID{ProbeP1, ProbeP2, ProbeP3, ProbeP7, ProbeP8, ProbeH2} {
		results[id] = h.res(id, "static", StatusUnverified, "runtime-only; requires --live", nil)
	}
	results[ProbeH3] = h.res(ProbeH3, "static", StatusUnverified, claimNote+"; observation requires --live --context-probe-tokens", nil)

	if !opts.Live || !rep.Available {
		rep.Probes = ordered(HTTPProbeOrder, results)
		return rep
	}
	dec := DecideLive(t.Name, t.Policy, model, opts.AllowPaid)
	if !dec.Allowed || model == "" {
		reason := dec.Reason
		if model == "" {
			reason = "no model selected for live probes; pass --model"
		}
		for _, id := range []ProbeID{ProbeP1, ProbeP2, ProbeP3, ProbeP7, ProbeP8, ProbeH2, ProbeH3} {
			r := results[id]
			r.Reason = Sanitize(reason + "; static: " + r.Reason)
			results[id] = r
		}
		rep.Probes = ordered(HTTPProbeOrder, results)
		return rep
	}
	rep.Mode = "live"
	for id, r := range h.live(ctx, model, claim, opts.ContextProbeTokens) {
		results[id] = r
	}
	rep.Probes = ordered(HTTPProbeOrder, results)
	return rep
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

type chatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			ToolCalls        []struct {
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	Error any `json:"error"`
}

func (h httpProber) chat(ctx context.Context, payload map[string]any) (httpResp, *chatResponse, error) {
	r := h.do(ctx, http.MethodPost, "/chat/completions", payload)
	if r.err != nil {
		return r, nil, r.err
	}
	var cr chatResponse
	if err := json.Unmarshal(r.body, &cr); err != nil {
		return r, nil, err
	}
	return r, &cr, nil
}

func pongPayload(model string) map[string]any {
	return map[string]any{
		"model":      model,
		"messages":   []map[string]string{{"role": "user", "content": promptPong}},
		"max_tokens": 256,
		"stream":     false,
	}
}

func (h httpProber) live(ctx context.Context, model string, claim, ctxTokens int) map[ProbeID]ProbeResult {
	out := map[ProbeID]ProbeResult{}
	cmd := func(probe string) string {
		return fmt.Sprintf("POST {base_url}/chat/completions model=%s probe=%s", model, probe)
	}

	// P1 + P2 + P8 share one minimal completion.
	r, cr, err := h.chat(ctx, pongPayload(model))
	ev := h.ev(cmd("P1"), r)
	switch {
	case r.err != nil:
		out[ProbeP1] = h.res(ProbeP1, "live", StatusFail, "request failed: "+r.err.Error(), ev)
		out[ProbeP2] = h.res(ProbeP2, "live", StatusUnverified, "no response body", ev)
		out[ProbeP8] = h.res(ProbeP8, "live", StatusUnverified, "no response body", ev)
	case err != nil:
		out[ProbeP1] = h.res(ProbeP1, "live", StatusFail, fmt.Sprintf("status %d", r.status), ev)
		out[ProbeP2] = h.res(ProbeP2, "live", StatusFail, "response body is not JSON: "+err.Error(), ev)
		out[ProbeP8] = h.res(ProbeP8, "live", StatusUnverified, "response not parseable", ev)
	default:
		out[ProbeP2] = h.res(ProbeP2, "live", StatusPass, "chat completion body parsed as JSON", ev)
		content := ""
		if len(cr.Choices) > 0 {
			content = cr.Choices[0].Message.Content
			if content == "" {
				content = cr.Choices[0].Message.ReasoningContent
			}
		}
		if r.status == http.StatusOK && content != "" {
			note := fmt.Sprintf("200 with non-empty completion (resolved model %q)", cr.Model)
			if strings.Contains(strings.ToUpper(content), "PONG") {
				note += "; reply contained PONG"
			}
			out[ProbeP1] = h.res(ProbeP1, "live", StatusPass, note, ev)
		} else {
			out[ProbeP1] = h.res(ProbeP1, "live", StatusFail, fmt.Sprintf("status %d, empty completion", r.status), ev)
		}
		if cr.Usage != nil && cr.Usage.TotalTokens > 0 {
			out[ProbeP8] = h.res(ProbeP8, "live", StatusPass, fmt.Sprintf("usage reported: prompt=%d completion=%d total=%d", cr.Usage.PromptTokens, cr.Usage.CompletionTokens, cr.Usage.TotalTokens), ev)
		} else {
			out[ProbeP8] = h.res(ProbeP8, "live", StatusFail, "no positive usage.total_tokens in response", ev)
		}
	}

	// P3: nonexistent model must yield a non-2xx status or an error object.
	r3, cr3, err3 := h.chat(ctx, pongPayload(badModel))
	ev3 := h.ev(cmd("P3")+" (model overridden to nonexistent id)", r3)
	switch {
	case r3.err != nil:
		out[ProbeP3] = h.res(ProbeP3, "live", StatusUnverified, "request failed: "+r3.err.Error(), ev3)
	case r3.status < 200 || r3.status >= 300:
		out[ProbeP3] = h.res(ProbeP3, "live", StatusPass, fmt.Sprintf("nonexistent model -> HTTP %d", r3.status), ev3)
	case err3 == nil && cr3.Error != nil:
		out[ProbeP3] = h.res(ProbeP3, "live", StatusPass, "nonexistent model -> 2xx with error object", ev3)
	default:
		out[ProbeP3] = h.res(ProbeP3, "live", StatusFail, fmt.Sprintf("nonexistent model -> HTTP %d without error object (silent fallback)", r3.status), ev3)
	}

	// P7: response_format json_schema.
	var schema any
	if uErr := json.Unmarshal([]byte(probeSchemaJSON), &schema); uErr != nil {
		out[ProbeP7] = h.res(ProbeP7, "live", StatusUnverified, "internal schema decode: "+uErr.Error(), nil)
	} else {
		p7 := pongPayload(model)
		p7["messages"] = []map[string]string{{"role": "user", "content": `Return a JSON object whose "answer" field is "PONG".`}}
		p7["response_format"] = map[string]any{
			"type":        "json_schema",
			"json_schema": map[string]any{"name": "g8s_probe", "strict": true, "schema": schema},
		}
		r7, cr7, err7 := h.chat(ctx, p7)
		ev7 := h.ev(cmd("P7"), r7)
		switch {
		case r7.err != nil || err7 != nil:
			out[ProbeP7] = h.res(ProbeP7, "live", StatusFail, fmt.Sprintf("schema request failed (status %d)", r7.status), ev7)
		case r7.status != http.StatusOK || len(cr7.Choices) == 0:
			out[ProbeP7] = h.res(ProbeP7, "live", StatusFail, fmt.Sprintf("schema request returned %d", r7.status), ev7)
		case findSchemaAnswer([]byte(cr7.Choices[0].Message.Content)):
			out[ProbeP7] = h.res(ProbeP7, "live", StatusPass, `content matched probe schema ({"answer":"PONG"})`, ev7)
		default:
			out[ProbeP7] = h.res(ProbeP7, "live", StatusFail, "content did not match probe schema", ev7)
		}
	}

	// H2: forced tool call.
	h2 := pongPayload(model)
	h2["messages"] = []map[string]string{{"role": "user", "content": "Call the g8s_probe_echo tool with text set to PONG."}}
	h2["tools"] = []map[string]any{{
		"type": "function",
		"function": map[string]any{
			"name":        "g8s_probe_echo",
			"description": "Echo text back.",
			"parameters": map[string]any{
				"type":       "object",
				"properties": map[string]any{"text": map[string]any{"type": "string"}},
				"required":   []string{"text"},
			},
		},
	}}
	h2["tool_choice"] = map[string]any{"type": "function", "function": map[string]any{"name": "g8s_probe_echo"}}
	rh, crh, errh := h.chat(ctx, h2)
	evh := h.ev(cmd("H2"), rh)
	switch {
	case rh.err != nil || errh != nil:
		out[ProbeH2] = h.res(ProbeH2, "live", StatusFail, fmt.Sprintf("tool-call request failed (status %d)", rh.status), evh)
	case rh.status != http.StatusOK:
		out[ProbeH2] = h.res(ProbeH2, "live", StatusFail, fmt.Sprintf("tool-call request returned %d", rh.status), evh)
	case len(crh.Choices) > 0 && len(crh.Choices[0].Message.ToolCalls) > 0 && crh.Choices[0].Message.ToolCalls[0].Function.Name == "g8s_probe_echo":
		out[ProbeH2] = h.res(ProbeH2, "live", StatusPass, "forced tool_choice produced a g8s_probe_echo tool call", evh)
	default:
		out[ProbeH2] = h.res(ProbeH2, "live", StatusFail, "no tool_calls in response despite forced tool_choice", evh)
	}

	out[ProbeH3] = h.probeContext(ctx, model, claim, ctxTokens)
	return out
}

func (h httpProber) probeContext(ctx context.Context, model string, claim, n int) ProbeResult {
	claimNote := "no claim exposed by /models"
	if claim > 0 {
		claimNote = fmt.Sprintf("claim=%d", claim)
	}
	if n <= 0 {
		return h.res(ProbeH3, "live", StatusUnverified, claimNote+"; observation skipped (pass --context-probe-tokens N to send an N-token prompt)", nil)
	}
	filler := strings.Repeat("lorem ", n)
	payload := map[string]any{
		"model":      model,
		"messages":   []map[string]string{{"role": "user", "content": filler + "\nReply OK."}},
		"max_tokens": 8,
		"stream":     false,
	}
	r, cr, err := h.chat(ctx, payload)
	ev := h.ev(fmt.Sprintf("POST {base_url}/chat/completions model=%s probe=H3 filler_tokens~%d", model, n), r)
	if r.err != nil || err != nil {
		return h.res(ProbeH3, "live", StatusUnverified, fmt.Sprintf("%s; request failed (status %d)", claimNote, r.status), ev)
	}
	if r.status != http.StatusOK {
		if claim > 0 && n <= claim {
			return h.res(ProbeH3, "live", StatusFail, fmt.Sprintf("%s; rejected a ~%d-token prompt with HTTP %d", claimNote, n, r.status), ev)
		}
		return h.res(ProbeH3, "live", StatusUnverified, fmt.Sprintf("%s; ~%d-token prompt rejected with HTTP %d", claimNote, n, r.status), ev)
	}
	observed := 0
	if cr.Usage != nil {
		observed = cr.Usage.PromptTokens
	}
	if claim > 0 && n >= claim {
		return h.res(ProbeH3, "live", StatusPass, fmt.Sprintf("%s; accepted prompt of %d observed tokens", claimNote, observed), ev)
	}
	return h.res(ProbeH3, "live", StatusUnverified, fmt.Sprintf("%s; accepted ~%d-token prompt (observed prompt_tokens=%d) — below claim, claim not falsified nor confirmed", claimNote, n, observed), ev)
}
