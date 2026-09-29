package cleanup

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/process"
)

// #465: a stale heartbeat keyed by PID is not identity evidence — PIDs are
// recycled. A heartbeat-stale agy process whose CWD and command line carry
// no reference to the invoking repo must NOT be declared a ghost (this
// killed live workers of a concurrent g8s session on the same host), while
// a process with CWD or command-line corroboration still is.
func TestFindGhostProcesses_StaleHeartbeatRequiresIdentityEvidence(t *testing.T) {
	tempDir := t.TempDir()  // project repo
	otherDir := t.TempDir() // another project on the same host
	heartbeatDir := filepath.Join(tempDir, ".heartbeat")
	agyDir := filepath.Join(heartbeatDir, "agy")
	if err := os.MkdirAll(agyDir, 0o755); err != nil {
		t.Fatalf("mkdir heartbeat dir: %v", err)
	}

	now := time.Now()
	writeHB := func(pid int, session string) {
		hb := HeartbeatData{
			SessionID:   session,
			PID:         pid,
			Binary:      "agy",
			CommandLine: "agy worker --task=recycled",
			StartedAt:   now.Add(-30 * time.Minute).Format(time.RFC3339),
			LastUpdate:  now.Add(-10 * time.Minute).Format(time.RFC3339), // 10m > 5m maxAge
			Status:      "running",
		}
		data, _ := json.Marshal(hb)
		_ = os.WriteFile(filepath.Join(agyDir, session+".json"), data, 0o644)
	}
	writeHB(7101, "sess-recycled-foreign")
	writeHB(7102, "sess-stale-own-cwd")
	writeHB(7103, "sess-stale-own-cmdline")

	lister := &mockCleanupProcessLister{
		processes: []process.ProcessInfo{
			// Stale heartbeat + zero identity evidence -> PID recycle case,
			// belongs to some other session: NOT a ghost.
			{PID: 7101, Binary: "agy", CommandLine: "agy --model m --prompt /tmp/other/prompt.txt", CWD: otherDir},
			// Stale heartbeat + CWD inside the project -> ghost (unchanged).
			{PID: 7102, Binary: "agy", CommandLine: "agy --model m", CWD: tempDir},
			// Stale heartbeat + command line references the project -> ghost.
			{PID: 7103, Binary: "agy", CommandLine: "agy --model m --add-dir " + tempDir, CWD: otherDir},
		},
	}

	pm := &DefaultProcessManager{RepoDir: tempDir, Lister: lister}
	ghosts, err := pm.FindGhostProcesses(context.Background(), heartbeatDir, 5*time.Minute, nil)
	if err != nil {
		t.Fatalf("FindGhostProcesses: %v", err)
	}

	ghostMap := make(map[int]ProcessInfo)
	for _, g := range ghosts {
		ghostMap[g.PID] = g
	}
	if g, killed := ghostMap[7101]; killed {
		t.Errorf("PID 7101 (stale heartbeat, no identity evidence) must not be a ghost, got: %+v", g)
	}
	if _, ok := ghostMap[7102]; !ok {
		t.Errorf("PID 7102 (stale heartbeat, CWD in project) must be a ghost")
	}
	if _, ok := ghostMap[7103]; !ok {
		t.Errorf("PID 7103 (stale heartbeat, cmdline references project) must be a ghost")
	}
}
