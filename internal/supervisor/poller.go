// Package supervisor — poller.go implements adaptive heartbeat polling and silence
// escalation for worker processes per DEBT-35.
package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/tamld/g8s/internal/cleanup"
	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/heartbeat"
)

// FreshnessColor classifies worker heartbeat health.
type FreshnessColor string

const (
	ColorGreen  FreshnessColor = "green"  // < 60s (active)
	ColorYellow FreshnessColor = "yellow" // 60s - 300s (stale)
	ColorRed    FreshnessColor = "red"    // > 300s (dead)
)

// PollerConfig holds configuration parameters for HeartbeatPoller.
type PollerConfig struct {
	Interval         time.Duration
	StaleThreshold   time.Duration
	SilenceThreshold time.Duration
	HeartbeatDir     string
	NoPoll           bool
	Clock            func() time.Time
	ProcessManager   cleanup.ProcessManager
	Store            Persistence
	BinaryPath       string
	OnWorkerDead     func(taskID string, pid int)
}

// DefaultPollerConfig provides standard production defaults per DEBT-35.
func DefaultPollerConfig() PollerConfig {
	return PollerConfig{
		Interval:         30 * time.Second,
		StaleThreshold:   60 * time.Second,
		SilenceThreshold: 300 * time.Second,
		HeartbeatDir:     heartbeat.DefaultHeartbeatDir,
		NoPoll:           false,
		Clock:            time.Now,
	}
}

// PollerDecisionPayload is the JSON payload for supervisor decisions emitted by the poller.
type PollerDecisionPayload struct {
	TaskID     string         `json:"task_id"`
	SessionID  string         `json:"session_id"`
	Color      FreshnessColor `json:"color"`
	AgeSeconds float64        `json:"age_seconds"`
	PID        int            `json:"pid,omitempty"`
	Detail     string         `json:"detail,omitempty"`
}

// HeartbeatPoller runs background health polling on worker heartbeat files.
type HeartbeatPoller struct {
	cfg          PollerConfig
	hbStore      *heartbeat.Store
	mu           sync.Mutex
	staleEmitted map[string]bool
	deadEmitted  map[string]bool
}

// NewHeartbeatPoller creates a new poller instance with the given configuration.
func NewHeartbeatPoller(cfg PollerConfig) *HeartbeatPoller {
	if cfg.Interval <= 0 {
		cfg.Interval = 30 * time.Second
	}
	if cfg.StaleThreshold <= 0 {
		cfg.StaleThreshold = 60 * time.Second
	}
	if cfg.SilenceThreshold <= 0 {
		cfg.SilenceThreshold = 300 * time.Second
	}
	if cfg.HeartbeatDir == "" {
		cfg.HeartbeatDir = heartbeat.DefaultHeartbeatDir
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}

	hbStore := heartbeat.NewStore(cfg.HeartbeatDir, cfg.Clock)

	return &HeartbeatPoller{
		cfg:          cfg,
		hbStore:      hbStore,
		staleEmitted: make(map[string]bool),
		deadEmitted:  make(map[string]bool),
	}
}

// PollOnce inspects the worker heartbeat and returns the evaluated FreshnessColor.
// If yellow (stale) or red (dead), it records the decision in the persistence store.
// If red (dead), it also triggers ghost process termination and the OnWorkerDead callback.
func (p *HeartbeatPoller) PollOnce(ctx context.Context, taskID, sessionID string, pid int) (FreshnessColor, error) {
	if p.cfg.NoPoll {
		return ColorGreen, nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	now := p.cfg.Clock()
	hb, err := p.hbStore.Status(sessionID)
	if err != nil && errors.Is(err, heartbeat.ErrSessionNotFound) && pid > 0 {
		// Try finding heartbeat by PID if session ID did not match directly
		list, _ := p.hbStore.List()
		for _, item := range list {
			if item != nil && item.PID == pid {
				hb = item
				err = nil
				break
			}
		}
	}

	// If no heartbeat exists yet, treat as green if within stale threshold from start
	if hb == nil {
		return ColorGreen, nil
	}

	age := now.Sub(hb.LastUpdate)
	if age < 0 {
		age = 0
	}
	workerPID := hb.PID
	if workerPID == 0 && pid > 0 {
		workerPID = pid
	}

	// 1. Green (< StaleThreshold, default 60s)
	if age < p.cfg.StaleThreshold {
		return ColorGreen, nil
	}

	// 2. Yellow (StaleThreshold <= age < SilenceThreshold, default 60s - 300s)
	if age < p.cfg.SilenceThreshold {
		if !p.staleEmitted[sessionID] {
			p.staleEmitted[sessionID] = true
			if p.cfg.Store != nil {
				payload := PollerDecisionPayload{
					TaskID:     taskID,
					SessionID:  sessionID,
					Color:      ColorYellow,
					AgeSeconds: age.Seconds(),
					PID:        workerPID,
					Detail:     fmt.Sprintf("worker heartbeat stale (age: %.1fs)", age.Seconds()),
				}
				data, _ := json.Marshal(payload)
				_ = p.cfg.Store.AppendDecision(ctx, controlplane.SupervisorDecisionRow{
					TaskID:      taskID,
					Kind:        "heartbeat_stale",
					PayloadJSON: string(data),
					CreatedAt:   now,
				})
			}
		}
		return ColorYellow, nil
	}

	// 3. Red (>= SilenceThreshold, default 300s) -> worker dead
	if !p.deadEmitted[sessionID] {
		p.deadEmitted[sessionID] = true
		if p.cfg.Store != nil {
			payload := PollerDecisionPayload{
				TaskID:     taskID,
				SessionID:  sessionID,
				Color:      ColorRed,
				AgeSeconds: age.Seconds(),
				PID:        workerPID,
				Detail:     fmt.Sprintf("worker silent for %.1fs (threshold: %.1fs)", age.Seconds(), p.cfg.SilenceThreshold.Seconds()),
			}
			data, _ := json.Marshal(payload)
			_ = p.cfg.Store.AppendDecision(ctx, controlplane.SupervisorDecisionRow{
				TaskID:      taskID,
				Kind:        "worker_dead",
				PayloadJSON: string(data),
				CreatedAt:   now,
			})
		}

		// Cleanup ghost process
		if workerPID > 0 {
			p.reapWorker(ctx, workerPID)
		}

		if p.cfg.OnWorkerDead != nil {
			p.cfg.OnWorkerDead(taskID, workerPID)
		}
	}

	return ColorRed, nil
}

// reapWorker terminates the worker process using g8s cleanup or direct process manager.
func (p *HeartbeatPoller) reapWorker(ctx context.Context, pid int) {
	if p.cfg.BinaryPath != "" {
		cmd := exec.CommandContext(ctx, p.cfg.BinaryPath, "cleanup", "--target", "ghost-process", "--pid", strconv.Itoa(pid), "--force")
		_ = cmd.Run()
	}
	if p.cfg.ProcessManager != nil {
		_ = p.cfg.ProcessManager.KillProcess(pid, syscall.SIGKILL)
	}
}

// Start launches a background goroutine ticking every cfg.Interval until ctx is cancelled.
// It returns a channel emitting any non-green status updates.
func (p *HeartbeatPoller) Start(ctx context.Context, taskID, sessionID string, pid int) <-chan FreshnessColor {
	out := make(chan FreshnessColor, 4)
	if p.cfg.NoPoll {
		close(out)
		return out
	}

	go func() {
		defer close(out)
		ticker := time.NewTicker(p.cfg.Interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				color, err := p.PollOnce(ctx, taskID, sessionID, pid)
				if err != nil {
					continue
				}
				if color != ColorGreen {
					select {
					case out <- color:
					default:
					}
					if color == ColorRed {
						return
					}
				}
			}
		}
	}()

	return out
}
