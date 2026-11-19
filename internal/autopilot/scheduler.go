// Package autopilot implements the cron-based supervisor trigger that scans
// GitHub issues, failing CI runs, static analysis findings, and stale
// @agy-fix-me TODOs. It uses a priority queue weighted by severity,
// confidence, and cost-inverse.
package autopilot

import (
	"container/heap"
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/tamld/g8s/internal/sandbox"
	"github.com/tamld/g8s/internal/verifier"
)

// Scheduler is the main autopilot scheduler that runs on a cron schedule.
type Scheduler struct {
	config         Config
	queue          *PriorityQueue
	scanner        *Scanner
	cron           *cron.Cron
	logger         *log.Logger
	handler        WorkHandler
	guard          *verifier.EvolutionGuard
	roundCount     int
	succeededCount int
	mu             sync.RWMutex
	running        bool
	stopCh         chan struct{}
	wg             sync.WaitGroup
}

// WorkHandler is called for each work item that should be processed.
type WorkHandler func(ctx context.Context, item *WorkItem) error

// NewScheduler creates a new autopilot scheduler.
func NewScheduler(config Config, handler WorkHandler) (*Scheduler, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	queue := NewPriorityQueue()
	scanner, err := NewScanner(config, queue)
	if err != nil {
		return nil, fmt.Errorf("create scanner: %w", err)
	}

	budget := verifier.DefaultEvolutionBudget()
	if config.EvolutionBudget != nil {
		budget = *config.EvolutionBudget
	}

	s := &Scheduler{
		config:  config,
		queue:   queue,
		scanner: scanner,
		handler: handler,
		logger:  log.Default(),
		guard:   verifier.NewEvolutionGuard(budget, time.Now),
		stopCh:  make(chan struct{}),
	}

	if config.Cron != "" {
		s.cron = cron.New(cron.WithSeconds())
		_, err := s.cron.AddFunc(config.Cron, s.runTick)
		if err != nil {
			return nil, fmt.Errorf("add cron job: %w", err)
		}
	}

	return s, nil
}

// SetEvolutionGuard replaces the TRUST-G6 EvolutionGuard used by the scheduler (primarily for deterministic clock tests).
func (s *Scheduler) SetEvolutionGuard(g *verifier.EvolutionGuard) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g != nil {
		s.guard = g
	}
}

// EvolutionStatus returns the current TRUST-G6 guard status snapshot for this scheduler.
func (s *Scheduler) EvolutionStatus() verifier.EvolutionGuardStatus {
	s.mu.RLock()
	guard := s.guard
	s.mu.RUnlock()
	if guard == nil {
		return verifier.EvolutionGuardStatus{}
	}
	return guard.Status()
}

// SetLogger sets a custom logger.
func (s *Scheduler) SetLogger(logger *log.Logger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logger = logger
}

// Start starts the scheduler.
func (s *Scheduler) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return nil // Already running
	}

	s.running = true
	s.stopCh = make(chan struct{})

	if s.cron != nil {
		s.cron.Start()
		s.logger.Printf("Autopilot scheduler started with cron: %s", s.config.Cron)
	} else {
		s.logger.Printf("Autopilot scheduler started (cron disabled, manual trigger only)")
	}

	// Run initial scan
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.runTick()
	}()

	return nil
}

// Stop stops the scheduler gracefully.
func (s *Scheduler) Stop() error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = false
	s.mu.Unlock()

	close(s.stopCh)

	if s.cron != nil {
		s.cron.Stop()
	}

	s.wg.Wait()
	s.logger.Printf("Autopilot scheduler stopped")
	return nil
}

// runTick executes one scan cycle and processes discovered items.
func (s *Scheduler) runTick() {
	s.mu.RLock()
	running := s.running
	s.mu.RUnlock()

	if !running {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	s.logger.Printf("Autopilot: starting scan cycle")
	start := time.Now()

	if err := s.scanner.Scan(ctx); err != nil {
		s.logger.Printf("Autopilot: scan error: %v", err)
	}

	s.logger.Printf("Autopilot: scan completed in %v, queue size: %d", time.Since(start), s.queue.Len())

	// Process items if handler is set
	if s.handler != nil {
		s.processQueue(ctx)
	}
}

// processQueue processes items from the priority queue under TRUST-G6 EvolutionGuard bounds.
func (s *Scheduler) processQueue(ctx context.Context) {
	processed := 0
	maxProcess := s.config.MaxItemsPerTick

	for processed < maxProcess {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		default:
		}

		s.mu.RLock()
		guard := s.guard
		s.mu.RUnlock()
		if guard != nil {
			if guardErr := guard.AllowNextRound(); guardErr != nil {
				s.logger.Printf("Autopilot: evolution guard halted queue processing: %v", guardErr)
				return
			}
		}

		var item *WorkItem
		s.queue.mu.Lock()
		if s.queue.Len() > 0 {
			if popped, ok := heap.Pop(s.queue).(*WorkItem); ok {
				item = popped
			}
		}
		s.queue.mu.Unlock()

		if item == nil {
			break
		}

		s.logger.Printf("Autopilot: processing item %s (score=%.3f)", item.ID, item.Score)

		err := s.handler(ctx, item)

		var guardErr error
		if guard != nil {
			s.mu.Lock()
			s.roundCount++
			round := s.roundCount
			rec := verifier.EvolutionRoundRecord{
				Round:      round,
				ScoreClass: sandbox.EpistemicMeasuredMetric,
				RecordedAt: time.Now(),
			}
			if err == nil {
				s.succeededCount++
				rec.HoldoutPassed = true
				rec.HoldoutScore = float64(s.succeededCount)
			} else {
				rec.RetriesUsed = 1
				rec.HoldoutPassed = false
				rec.HoldoutScore = 0
			}
			s.mu.Unlock()

			_, guardErr = guard.RecordRound(rec)
		}

		if err != nil {
			s.logger.Printf("Autopilot: handler error for %s: %v", item.ID, err)
		}
		if guardErr != nil {
			s.logger.Printf("Autopilot: evolution guard halted after item %s: %v", item.ID, guardErr)
			return
		}
		if err != nil {
			// Re-queue with lower score for retry while guard permits
			s.queue.mu.Lock()
			item.Score *= 0.5
			heap.Push(s.queue, item)
			s.queue.mu.Unlock()
		}

		processed++
	}

	if processed > 0 {
		s.logger.Printf("Autopilot: processed %d items", processed)
	}
}

// TriggerScan manually triggers a scan cycle (for testing or manual invocation).
func (s *Scheduler) TriggerScan(ctx context.Context) error {
	s.mu.RLock()
	running := s.running
	s.mu.RUnlock()

	if !running {
		return fmt.Errorf("scheduler not running")
	}

	s.runTick()
	return nil
}

// GetQueue returns the current priority queue (for inspection).
func (s *Scheduler) GetQueue() *PriorityQueue {
	return s.queue
}

// GetConfig returns the current configuration.
func (s *Scheduler) GetConfig() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config
}

// UpdateConfig updates the scheduler configuration.
func (s *Scheduler) UpdateConfig(config Config) error {
	if err := config.Validate(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.config = config

	// Update scanner config
	s.scanner.config = config

	// Update cron if changed
	if s.cron != nil {
		s.cron.Stop()
		s.cron = cron.New(cron.WithSeconds())
		if config.Cron != "" {
			_, err := s.cron.AddFunc(config.Cron, s.runTick)
			if err != nil {
				return fmt.Errorf("add cron job: %w", err)
			}
			s.cron.Start()
		}
	}

	return nil
}

// IsRunning returns whether the scheduler is currently running.
func (s *Scheduler) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}
