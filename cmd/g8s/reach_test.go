package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/reach"
)

func TestReachReadJSONEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		_, _ = w.Write([]byte("reach-test-body"))
	}))
	defer srv.Close()

	binPath := buildG8sBinary(t)
	cmd := execCommand(t, binPath, "reach", "read", srv.URL, "--json")
	out := cmdCombinedOutput(t, cmd)

	var env struct {
		V    int          `json:"v"`
		Kind string       `json:"kind"`
		Cmd  string       `json:"cmd"`
		Data reachPayload `json:"data"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("unmarshal: %v\nout=%s", err, string(out))
	}
	if env.V != 1 || env.Kind != "reach" || env.Cmd != "reach" {
		t.Fatalf("headers: %+v", env)
	}
	if env.Data.Channel != "web" || env.Data.Action != "read" {
		t.Fatalf("data channel/action: %+v", env.Data)
	}
	if env.Data.Body != "reach-test-body" {
		t.Fatalf("body = %q", env.Data.Body)
	}
	if env.Data.Backend != "web-https-get" || len(env.Data.Attempts) != 1 || env.Data.Attempts[0].Class != "ok" {
		t.Fatalf("attempts/backend: %+v", env.Data)
	}
	if env.Data.URL != srv.URL {
		t.Fatalf("url = %q", env.Data.URL)
	}
	if env.Data.BodySHA256 != reach.DigestBody("reach-test-body") {
		t.Fatalf("body_sha256 = %q, want %q", env.Data.BodySHA256, reach.DigestBody("reach-test-body"))
	}
	if env.Data.FetchedAt == "" {
		t.Fatal("fetched_at must be set")
	}
	if _, err := time.Parse(time.RFC3339, env.Data.FetchedAt); err != nil {
		t.Fatalf("fetched_at not RFC3339: %v", err)
	}
}

func TestReachUsageMissingURL(t *testing.T) {
	binPath := buildG8sBinary(t)
	cmd := execCommand(t, binPath, "reach", "read", "--json")
	out := cmdCombinedOutput(t, cmd)
	if len(out) == 0 {
		t.Fatal("expected stderr envelope")
	}
}

// execCommand/build helpers live in envelope_commands_test.go (shared TestMain binary).

func execCommand(t *testing.T, binPath string, args ...string) *exec.Cmd {
	t.Helper()
	return exec.Command(binPath, args...)
}

func cmdCombinedOutput(t *testing.T, cmd *exec.Cmd) []byte {
	t.Helper()
	out, err := cmd.CombinedOutput()
	if err == nil {
		return out
	}
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 2 {
		return out
	}
	t.Fatalf("command failed: %v\n%s", err, string(out))
	return nil
}
