// Package process provides cross-platform process discovery, inspection, and
// termination primitives for g8s lifecycle management per DEBT-36.
package process

import (
	"time"
)

// ProcessInfo represents a snapshot of an OS process.
type ProcessInfo struct {
	PID         int       `json:"pid"`
	PPID        int       `json:"ppid"`
	User        string    `json:"user"`
	Binary      string    `json:"binary"`
	CommandLine string    `json:"command_line"`
	CWD         string    `json:"cwd,omitempty"`
	StartTime   time.Time `json:"start_time,omitempty"`
}

// ProcessLister provides OS-agnostic process listing and termination methods.
type ProcessLister interface {
	List() ([]ProcessInfo, error)
	Kill(pid int) error
	KillForce(pid int) error
	IsAlive(pid int) bool
	ResolveCWD(pid int) string
}

// NewLister returns the platform-appropriate ProcessLister implementation.
func NewLister() ProcessLister {
	return newLister()
}
