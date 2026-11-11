package dispatch

import "testing"

func TestSupportsWorktreeReadOnlyFirewall(t *testing.T) {
	for _, provider := range []string{"codex", "cursor", "opencode", "commandcode", "qwen"} {
		if !SupportsWorktreeReadOnlyFirewall(provider, "") {
			t.Fatalf("expected worktree read_only firewall for %q", provider)
		}
	}
	for _, provider := range []string{"agy", "claude", "api", "custom", ""} {
		if SupportsWorktreeReadOnlyFirewall(provider, "") {
			t.Fatalf("did not expect worktree read_only firewall for %q", provider)
		}
	}
	if !SupportsWorktreeReadOnlyFirewall("", "codex") {
		t.Fatal("expected defaultProvider fallback")
	}
}

func TestSupportsReadOnlyPolicy(t *testing.T) {
	if !SupportsReadOnlyPolicy("agy", "") {
		t.Fatal("agy must support read_only policy")
	}
	if !SupportsReadOnlyPolicy("codex", "") {
		t.Fatal("codex must support read_only via worktree firewall")
	}
	if SupportsReadOnlyPolicy("claude", "") {
		t.Fatal("claude lacks both in-process and worktree firewall support in dispatch")
	}
	if !SupportsReadOnlyPolicy("", "agy") {
		t.Fatal("expected default agy read_only support")
	}
}
