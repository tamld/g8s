package worker

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/receipt"
)

func TestClaimHarvestIntersection(t *testing.T) {
	for _, tc := range []struct {
		name, file string
		expired    bool
		want       error
	}{
		{"allowed", "src/allowed.go", false, nil},
		{"outside_claim", "src/b.go", false, receipt.ErrClaimFenceOutOfScope},
		{"expired", "src/allowed.go", true, receipt.ErrClaimFenceStale},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := initGitRepoForDiffFirewall(t)
			b := &receipt.ClaimBinding{TaskID: "claim", NodeID: "node", Incarnation: "inc", Resource: "repo", Paths: []string{"src/allowed.go"}, ExpiresAt: time.Now().Add(time.Minute)}
			if tc.expired {
				b.ExpiresAt = time.Now().Add(-time.Second)
			}
			if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(tc.file)), []byte("package changed\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			violations, err := VerifyWorktreeDiffScope(dir, "workspace_write", []string{"src/**"}, b)
			if tc.want == nil && err != nil {
				t.Fatal(err)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("violations=%v error=%v want=%v", violations, err, tc.want)
			}
		})
	}
}

func TestClaimHarvestUnavailableWorktree(t *testing.T) {
	b := &receipt.ClaimBinding{TaskID: "claim", NodeID: "n", Incarnation: "i", Resource: "r", Paths: []string{"src/a.go"}, ExpiresAt: time.Now().Add(time.Minute)}
	if _, err := VerifyWorktreeDiffScope(t.TempDir(), "workspace_write", []string{"src/**"}, b); !errors.Is(err, receipt.ErrClaimFenceStale) {
		t.Fatalf("fail-open worktree: %v", err)
	}
}

func TestClaimFenceExportProjection(t *testing.T) {
	v := &controlplane.ClaimFenceVerdict{TaskID: "task", ClaimTaskID: "claim", ReceiptID: "receipt", Verdict: "rejected", Code: "E_CLAIM_FENCE_OUT_OF_SCOPE"}
	raw := mustResultJSON(workerResult{OK: false, Status: "contract_violation", ClaimFence: v}, "", "")
	task := &controlplane.Task{TaskID: "task", Result: raw}
	got := controlplane.TaskClaimFenceVerdict(task)
	if got == nil || *got != *v {
		t.Fatalf("export lost fence verdict: %s", raw)
	}
	status := controlplane.ReceiptExportStatus{ClaimFence: got}
	data, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	var reloaded controlplane.ReceiptExportStatus
	if err := json.Unmarshal(data, &reloaded); err != nil || reloaded.ClaimFence == nil || *reloaded.ClaimFence != *v {
		t.Fatalf("status round-trip: %s %v", data, err)
	}
}
