package reflex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	jevStatusOverloaded     = 529
	defaultContextPacketCap = 32768
)

func jevRetryBackoff() []time.Duration {
	if raw := strings.TrimSpace(os.Getenv("G8S_JEV_RETRY_BACKOFF_MS")); raw != "" {
		parts := strings.Split(raw, ",")
		out := make([]time.Duration, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			ms, err := strconv.Atoi(p)
			if err != nil || ms < 0 {
				continue
			}
			out = append(out, time.Duration(ms)*time.Millisecond)
		}
		if len(out) > 0 {
			return out
		}
	}
	return []time.Duration{50 * time.Millisecond, 120 * time.Millisecond}
}

func isJevRetryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || code == jevStatusOverloaded
}

// capContextPacketState bounds serialized context broker payloads before Jev state attachment.
func capContextPacketState(cp any, maxBytes int) any {
	if cp == nil {
		return nil
	}
	if maxBytes <= 0 {
		maxBytes = defaultContextPacketCap
	}
	raw, err := json.Marshal(cp)
	if err != nil {
		return cp
	}
	if len(raw) <= maxBytes {
		return cp
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(raw[cut]) {
		cut--
	}
	return map[string]any{
		"context_packet_truncated": true,
		"context_packet_bytes":     len(raw),
		"context_packet_preview":   string(raw[:cut]),
	}
}

func (g *ReflexGate) postJevHTTPWithRetry(ctx context.Context, body []byte) (*http.Response, error) {
	backoffs := jevRetryBackoff()
	maxAttempts := 1 + len(backoffs)
	var lastErr error

	for attempt := 0; attempt < maxAttempts; attempt++ {
		activeKey := g.currentKey()
		if activeKey == "" {
			return nil, fmt.Errorf("no api key configured")
		}

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, g.endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("Authorization", "Bearer "+activeKey)
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := g.client.Do(httpReq)
		if err != nil {
			lastErr = err
			if len(g.keys) > 1 {
				g.rotateKey()
			}
		} else if isJevRetryableStatus(resp.StatusCode) {
			resp.Body.Close()
			if len(g.keys) > 1 {
				g.rotateKey()
			}
			lastErr = fmt.Errorf("HTTP %d from Jev", resp.StatusCode)
		} else {
			return resp, nil
		}

		if attempt >= len(backoffs) {
			break
		}
		delay := backoffs[attempt]
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("jev request failed after retries")
}
