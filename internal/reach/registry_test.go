package reach

import (
	"testing"
)

func TestLookupDeclaredOrder(t *testing.T) {
	t.Parallel()
	backends, err := Lookup(ChannelWeb, ActionRead)
	if err != nil {
		t.Fatalf("Lookup(web, read): %v", err)
	}
	if len(backends) != 1 {
		t.Fatalf("len(backends) = %d, want 1", len(backends))
	}
	if backends[0].Name() != backendWebHTTPS {
		t.Errorf("first backend = %q, want %q", backends[0].Name(), backendWebHTTPS)
	}

	ghBackends, err := Lookup(ChannelGithub, ActionRead)
	if err != nil {
		t.Fatalf("Lookup(github, read): %v", err)
	}
	if len(ghBackends) != 1 || ghBackends[0].Name() != backendGithubGH {
		t.Fatalf("github backends = %+v, want [%q]", ghBackends, backendGithubGH)
	}
}

func TestLookupUnknownChannelAction(t *testing.T) {
	t.Parallel()
	tests := []struct {
		channel Channel
		action  Action
	}{
		{Channel("video"), ActionRead},
		{ChannelWeb, Action("write")},
	}
	for _, tc := range tests {
		_, err := Lookup(tc.channel, tc.action)
		if err == nil {
			t.Fatalf("Lookup(%q, %q) expected error", tc.channel, tc.action)
		}
	}
}

func TestGithubAPIPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want string
	}{
		{"/repos/o/r", "/repos/o/r"},
		{"https://api.github.com/repos/o/r/issues", "/repos/o/r/issues"},
		{"https://github.com/o/r", "/repos/o/r"},
	}
	for _, tc := range tests {
		got, err := githubAPIPath(tc.in)
		if err != nil {
			t.Fatalf("githubAPIPath(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("githubAPIPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseWebURLRejectsNonHTTP(t *testing.T) {
	t.Parallel()
	_, err := parseWebURL("file:///etc/passwd")
	if err == nil {
		t.Fatal("expected error for file:// URL")
	}
}
