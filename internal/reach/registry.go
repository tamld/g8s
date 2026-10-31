package reach

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"
)

// Channel names a logical acquisition surface (e.g. web, github).
type Channel string

// Action names a read-only operation on a channel.
type Action string

const (
	ChannelWeb    Channel = "web"
	ChannelGithub Channel = "github"

	ActionRead Action = "read"
)

// Request carries the target URL and optional parameters for a fetch.
type Request struct {
	URL    string            `json:"url"`
	Params map[string]string `json:"params,omitempty"`
}

// Response is a successful backend payload with provenance metadata.
type Response struct {
	Body       string `json:"body"`
	Status     int    `json:"status"`
	Provenance string `json:"provenance"`
}

// Backend performs a single read-only fetch attempt.
type Backend interface {
	Name() string
	Fetch(ctx context.Context, req Request) (Response, error)
}

const (
	backendWebHTTPS  = "web-https-get"
	backendGithubGH  = "github-gh-api"
	maxResponseBytes = 1 << 20
	webFetchTimeout  = 30 * time.Second
	webUserAgent     = "g8s-reach/1.0 (+https://github.com/tamld/g8s; read-only research fetch)"
)

var channelRegistry = map[Channel]map[Action][]Backend{
	ChannelWeb: {
		ActionRead: {webHTTPSBackend{}},
	},
	ChannelGithub: {
		ActionRead: {githubGHBackend{}},
	},
}

// Lookup returns backends for channel/action in stable declared order.
func Lookup(channel Channel, action Action) ([]Backend, error) {
	actions, ok := channelRegistry[channel]
	if !ok {
		return nil, fmt.Errorf("reach: unknown channel %q (supported: web, github)", channel)
	}
	backends, ok := actions[action]
	if !ok {
		return nil, fmt.Errorf("reach: unknown action %q for channel %q (supported action: read)", action, channel)
	}
	out := make([]Backend, len(backends))
	copy(out, backends)
	return out, nil
}

type webHTTPSBackend struct{}

func (webHTTPSBackend) Name() string { return backendWebHTTPS }

func (webHTTPSBackend) Fetch(ctx context.Context, req Request) (Response, error) {
	target, err := parseWebURL(req.URL)
	if err != nil {
		return Response{}, err
	}

	client := &http.Client{Timeout: webFetchTimeout}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return Response{}, fmt.Errorf("web fetch: build request: %w", err)
	}
	httpReq.Header.Set("User-Agent", webUserAgent)

	resp, err := client.Do(httpReq)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return Response{}, fmt.Errorf("web fetch: read body: %w", err)
	}
	if len(body) > maxResponseBytes {
		return Response{}, &StatusError{
			Status: resp.StatusCode,
			Msg:    fmt.Sprintf("web fetch: response exceeds %d byte cap", maxResponseBytes),
		}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Response{}, &StatusError{
			Status: resp.StatusCode,
			Msg:    strings.TrimSpace(string(body)),
		}
	}

	return Response{
		Body:       string(body),
		Status:     resp.StatusCode,
		Provenance: backendWebHTTPS,
	}, nil
}

func parseWebURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("invalid URL: empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return "", fmt.Errorf("invalid URL: scheme %q not allowed (want http or https)", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("invalid URL: missing host")
	}
	return u.String(), nil
}

type githubGHBackend struct{}

func (githubGHBackend) Name() string { return backendGithubGH }

func (githubGHBackend) Fetch(ctx context.Context, req Request) (Response, error) {
	ghBin, err := exec.LookPath("gh")
	if err != nil {
		return Response{}, fmt.Errorf("github fetch: gh CLI not found in PATH (install GitHub CLI to use github channel)")
	}

	apiPath, err := githubAPIPath(req.URL)
	if err != nil {
		return Response{}, err
	}

	cmd := exec.CommandContext(ctx, ghBin, "api", apiPath)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		if len(out) > 0 {
			return Response{}, fmt.Errorf("github fetch: gh api: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return Response{}, fmt.Errorf("github fetch: gh api: %w", err)
	}
	if len(out) > maxResponseBytes {
		return Response{}, &StatusError{
			Status: http.StatusOK,
			Msg:    fmt.Sprintf("github fetch: response exceeds %d byte cap", maxResponseBytes),
		}
	}

	return Response{
		Body:       string(out),
		Status:     http.StatusOK,
		Provenance: backendGithubGH,
	}, nil
}

func githubAPIPath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("invalid github URL: empty")
	}
	if strings.HasPrefix(raw, "/") {
		return path.Clean(raw), nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid github URL: %w", err)
	}
	host := strings.ToLower(u.Host)
	switch host {
	case "api.github.com":
		p := u.EscapedPath()
		if p == "" {
			p = "/"
		}
		if u.RawQuery != "" {
			p += "?" + u.RawQuery
		}
		return p, nil
	case "github.com":
		segments := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(segments) < 2 {
			return "", fmt.Errorf("invalid github URL: need owner/repo path")
		}
		return "/repos/" + segments[0] + "/" + segments[1], nil
	default:
		return "", fmt.Errorf("invalid github URL: host %q not supported (want github.com or api.github.com)", u.Host)
	}
}

// StatusError carries an HTTP status for chain error classification.
type StatusError struct {
	Status int
	Msg    string
}

func (e *StatusError) Error() string {
	if e.Msg != "" {
		return fmt.Sprintf("HTTP %d: %s", e.Status, e.Msg)
	}
	return fmt.Sprintf("HTTP %d", e.Status)
}

// StatusFromError returns an HTTP status when err is a StatusError.
func StatusFromError(err error) (int, bool) {
	var se *StatusError
	if errors.As(err, &se) {
		return se.Status, true
	}
	return 0, false
}
