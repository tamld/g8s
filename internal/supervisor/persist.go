package supervisor

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
)

// Persistence is the abstract store the supervisor writes to. WU3's
// controlplane migration provides a concrete *controlplane.Store-backed
// implementation that satisfies this contract.
type Persistence interface {
	CreateSupervisorTask(ctx context.Context, st controlplane.SupervisorTaskRow) error
	AppendDecision(ctx context.Context, dec controlplane.SupervisorDecisionRow) error
	UpdateSupervisorTask(ctx context.Context, st controlplane.SupervisorTaskRow) error
	GetSupervisorTask(ctx context.Context, id string) (controlplane.SupervisorTaskRow, error)
	ListSupervisorTasks(ctx context.Context) ([]controlplane.SupervisorTaskRow, error)
}

// ErrUnknownSupervisorTask is returned when GetSupervisorTask / UpdateSupervisorTask
// cannot find the requested id.
var ErrUnknownSupervisorTask = errors.New("supervisor: unknown supervisor task")

// StubPersistence is the in-process implementation used by tests. It is safe for concurrent use.
type StubPersistence struct {
	mu         sync.Mutex
	tasks      map[string]controlplane.SupervisorTaskRow
	decisions  map[string][]controlplane.SupervisorDecisionRow
	nextDecID  int
	nextTaskID int
	clock      func() time.Time
}

// NewStubPersistence returns an empty in-memory store.
func NewStubPersistence() *StubPersistence {
	return &StubPersistence{
		tasks:     map[string]controlplane.SupervisorTaskRow{},
		decisions: map[string][]controlplane.SupervisorDecisionRow{},
		clock:     time.Now,
	}
}

// CreateSupervisorTask inserts a new row. Duplicate id returns an error.
func (s *StubPersistence) CreateSupervisorTask(ctx context.Context, st controlplane.SupervisorTaskRow) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.tasks[st.ID]; dup {
		return errors.New("supervisor: supervisor task id already exists: " + st.ID)
	}
	if st.CreatedAt.IsZero() {
		st.CreatedAt = s.clock()
	}
	if st.UpdatedAt.IsZero() {
		st.UpdatedAt = st.CreatedAt
	}
	s.tasks[st.ID] = st
	return nil
}

// AppendDecision writes a decision row. The id field is generated if empty.
func (s *StubPersistence) AppendDecision(ctx context.Context, dec controlplane.SupervisorDecisionRow) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextDecID++
	dec.ID = fmtInt(s.nextDecID)
	if dec.CreatedAt.IsZero() {
		dec.CreatedAt = s.clock()
	}
	s.decisions[dec.TaskID] = append(s.decisions[dec.TaskID], dec)
	return nil
}

// UpdateSupervisorTask overwrites an existing row by id.
func (s *StubPersistence) UpdateSupervisorTask(ctx context.Context, st controlplane.SupervisorTaskRow) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tasks[st.ID]; !ok {
		return ErrUnknownSupervisorTask
	}
	st.UpdatedAt = s.clock()
	s.tasks[st.ID] = st
	return nil
}

// GetSupervisorTask returns a copy of the row, or ErrUnknownSupervisorTask.
func (s *StubPersistence) GetSupervisorTask(ctx context.Context, id string) (controlplane.SupervisorTaskRow, error) {
	if err := ctx.Err(); err != nil {
		return controlplane.SupervisorTaskRow{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return controlplane.SupervisorTaskRow{}, ErrUnknownSupervisorTask
	}
	return t, nil
}

// ListSupervisorTasks returns every row in id-sorted order. Test-only.
func (s *StubPersistence) ListSupervisorTasks(ctx context.Context) ([]controlplane.SupervisorTaskRow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]controlplane.SupervisorTaskRow, 0, len(s.tasks))
	for _, t := range s.tasks {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// DecisionsFor returns every decision attached to a supervisor task in
// insertion order. Test-only; the real Persistence will expose a query API.
func (s *StubPersistence) DecisionsFor(taskID string) []controlplane.SupervisorDecisionRow {
	s.mu.Lock()
	defer s.mu.Unlock()
	src := s.decisions[taskID]
	out := make([]controlplane.SupervisorDecisionRow, len(src))
	copy(out, src)
	return out
}

func fmtInt(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
