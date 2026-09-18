diffstat only, patch not produced. Full patch: git show -p <rev>
// Package worker implements the DELTA-09 task supervisor: it claims leased
// tasks from the control plane, runs each attempt inside a private run
// directory and a killable POSIX process group, enforces timeouts against an
// injectable clock, and exports sealed evidence without ever persisting raw
// worker output.
package worker
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/dispatch"
)
// WorkerControlPlane is the narrow control-plane surface the supervisor needs.
// *controlplane.Store satisfies it directly.
type WorkerControlPlane interface {
	ClaimTask(ctx context.Context, workerID string, leaseDurationSeconds int) (*controlplane.Task, error)
	StartTask(taskID, workerID, leaseToken string) bool
	RenewHeartbeat(ctx context.Context, taskID, workerID string, extensionSeconds int) error
	FinishAttempt(taskID, workerID, leaseToken string, params controlplane.FinishAttemptParams) (*controlplane.Task, error)
	PauseTask(taskID, workerID, leaseToken, pauseState string, result json.RawMessage, reason string) (*controlplane.Task, error)
	GetTask(ctx context.Context, taskID string) (*controlplane.Task, error)
}
// taskRequest mirrors the worker-facing payload stored on every task.
type taskRequest struct {
	Prompt      string   `json:"prompt"`
	Model       string   `json:"model"`
	Role        string   `json:"role"`
	Permission  string   `json:"permission"`
	Timeout     string   `json:"timeout"`
	AddDirs     []string `json:"add_dirs"`
	NoSandbox   bool     `json:"no_sandbox"`
	SkipPermiss bool     `json:"skip_permissions"`
	// Effort is the reasoning-effort level passed to the worker CLI via --effort.
	Effort string `json:"effort,omitempty"`
... more lines (truncated by snip)
