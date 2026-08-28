// Package supervisor — optimizer_stub.go is a placeholder for T022 (real
// optimizer that consumes metrics and proposes new SupervisorConfig values).
// Until then the stub is the no-op default: it returns the config unchanged.
package supervisor

// Optimizer proposes a new SupervisorConfig from a run history. T022 will
// replace the stub with a bayesian- or bandit-style search.
type Optimizer interface {
	Propose(currentConfig SupervisorConfig, metrics []Metrics) SupervisorConfig
}

// StubOptimizer is the deterministic no-op default. It returns currentConfig
// unchanged; tests use it to verify the supervisor pipeline runs without an
// optimizer attached.
type StubOptimizer struct{}

// NewStubOptimizer returns a ready-to-use optimizer.
func NewStubOptimizer() *StubOptimizer { return &StubOptimizer{} }

// Propose returns currentConfig verbatim. The metrics argument is accepted
// to satisfy the Optimizer interface and to make the call site readable.
func (s *StubOptimizer) Propose(currentConfig SupervisorConfig, metrics []Metrics) SupervisorConfig {
	_ = metrics
	return currentConfig
}
