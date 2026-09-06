package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestCompareSemVer(t *testing.T) {
	tests := []struct {
		name string
		v1   string
		v2   string
		want int
	}{
		{"equal basic", "0.7.0", "0.7.0", 0},
		{"equal with v", "v0.7.0", "0.7.0", 0},
		{"minor newer", "0.8.0", "0.7.0", 1},
		{"major newer", "1.0.0", "0.7.0", 1},
		{"patch newer", "0.7.1", "0.7.0", 1},
		{"minor older", "0.6.1", "0.7.0", -1},
		{"patch older", "0.6.0", "0.6.1", -1},
		{"prerelease older than release", "0.8.0", "0.8.0-rc.1", 1},
		{"release older than next", "0.8.0-rc.1", "0.8.0", -1},
		{"prerelease comparison", "0.8.0-rc.2", "0.8.0-rc.1", 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compareSemVer(tt.v1, tt.v2)
			if got != tt.want {
				t.Errorf("compareSemVer(%q, %q) = %d, want %d", tt.v1, tt.v2, got, tt.want)
			}
		})
	}
}

type mockHTTPClient struct {
	doFunc func(req *http.Request) (*http.Response, error)
}

func (m *mockHTTPClient) Do(req *http.Request) (*http.Response, error) {
	return m.doFunc(req)
}

func TestCheckLatestRelease(t *testing.T) {
	t.Run("update available", func(t *testing.T) {
		client := &mockHTTPClient{
			doFunc: func(req *http.Request) (*http.Response, error) {
				body := `{"tag_name":"v0.8.0","html_url":"https://github.com/tamld/g8s/releases/tag/v0.8.0","published_at":"2026-09-06T12:00:00Z"}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(body)),
				}, nil
			},
		}

		info := checkLatestRelease(context.Background(), client, "http://mock", "0.7.0")
		if !info.Checked {
			t.Errorf("Checked = false, want true")
		}
		if !info.UpdateAvailable {
			t.Errorf("UpdateAvailable = false, want true")
		}
		if info.LatestVersion != "0.8.0" {
			t.Errorf("LatestVersion = %q, want 0.8.0", info.LatestVersion)
		}
		if info.ReleaseURL != "https://github.com/tamld/g8s/releases/tag/v0.8.0" {
			t.Errorf("ReleaseURL = %q, unexpected", info.ReleaseURL)
		}
		if info.Error != "" {
			t.Errorf("unexpected Error: %s", info.Error)
		}
	})

	t.Run("up to date", func(t *testing.T) {
		client := &mockHTTPClient{
			doFunc: func(req *http.Request) (*http.Response, error) {
				body := `{"tag_name":"v0.7.0","html_url":"https://github.com/tamld/g8s/releases/tag/v0.7.0"}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(body)),
				}, nil
			},
		}

		info := checkLatestRelease(context.Background(), client, "http://mock", "0.7.0")
		if info.UpdateAvailable {
			t.Errorf("UpdateAvailable = true, want false")
		}
		if info.LatestVersion != "0.7.0" {
			t.Errorf("LatestVersion = %q, want 0.7.0", info.LatestVersion)
		}
	})

	t.Run("http error", func(t *testing.T) {
		client := &mockHTTPClient{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusBadGateway,
					Body:       io.NopCloser(strings.NewReader("bad gateway")),
				}, nil
			},
		}

		info := checkLatestRelease(context.Background(), client, "http://mock", "0.7.0")
		if info.Error == "" || !strings.Contains(info.Error, "502") {
			t.Errorf("expected 502 error, got %q", info.Error)
		}
		if info.UpdateAvailable {
			t.Errorf("UpdateAvailable should be false on error")
		}
	})

	t.Run("network transport error", func(t *testing.T) {
		client := &mockHTTPClient{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return nil, fmt.Errorf("connection refused")
			},
		}

		info := checkLatestRelease(context.Background(), client, "http://mock", "0.7.0")
		if info.Error == "" || !strings.Contains(info.Error, "connection refused") {
			t.Errorf("expected connection error, got %q", info.Error)
		}
	})

	t.Run("malformed json", func(t *testing.T) {
		client := &mockHTTPClient{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("not json")),
				}, nil
			},
		}

		info := checkLatestRelease(context.Background(), client, "http://mock", "0.7.0")
		if info.Error == "" || !strings.Contains(info.Error, "decode release payload") {
			t.Errorf("expected json decode error, got %q", info.Error)
		}
	})
}

func TestRunVersionCaptureStdout(t *testing.T) {
	origClient := defaultHTTPClient
	defer func() { defaultHTTPClient = origClient }()

	defaultHTTPClient = &mockHTTPClient{
		doFunc: func(req *http.Request) (*http.Response, error) {
			body := `{"tag_name":"v0.9.0","html_url":"https://github.com/tamld/g8s/releases/tag/v0.9.0"}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		},
	}

	// Capture stdout by redirecting os.Stdout
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe failed: %v", err)
	}
	os.Stdout = w

	runVersion([]string{"--check-update", "--json"})

	_ = w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	output := buf.String()

	var env struct {
		V       int         `json:"v"`
		Kind    string      `json:"kind"`
		Command string      `json:"cmd"`
		Error   interface{} `json:"error"`
		Data    struct {
			Version     string      `json:"version"`
			CheckUpdate *UpdateInfo `json:"check_update"`
		} `json:"data"`
	}

	if err := json.Unmarshal([]byte(output), &env); err != nil {
		t.Fatalf("failed to parse json output: %v, raw:\n%s", err, output)
	}

	if env.Error != nil {
		t.Errorf("env.Error = %v, want nil", env.Error)
	}
	if env.Command != "version" {
		t.Errorf("env.Command = %q, want version", env.Command)
	}
	if env.Data.CheckUpdate == nil {
		t.Fatalf("check_update is nil in json envelope")
	}
	if !env.Data.CheckUpdate.UpdateAvailable {
		t.Errorf("UpdateAvailable = false, want true")
	}
	if env.Data.CheckUpdate.LatestVersion != "0.9.0" {
		t.Errorf("LatestVersion = %q, want 0.9.0", env.Data.CheckUpdate.LatestVersion)
	}
}
