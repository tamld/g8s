package worker

import (
	"fmt"
	"strings"

	"github.com/tamld/g8s/internal/config"
)

// WithProviderRegistry supplies the same registry used to resolve commands.
// Operator entries override built-ins, including their enforcement declaration.
func WithProviderRegistry(entries []config.ProviderEntry) Option {
	return func(s *Supervisor) {
		s.providers = append(config.BuiltinPlatformProviders(), entries...)
	}
}

func (s *Supervisor) resolveTaskProvider(req taskRequest) config.ProviderEntry {
	name := strings.TrimSpace(req.Provider)
	for i := len(s.providers) - 1; i >= 0; i-- {
		p := s.providers[i]
		if name != "" {
			if strings.EqualFold(p.Name, name) {
				return p
			}
		} else if len(p.Args) > 0 && p.ServesModel(req.Model) {
			// Legacy model templates are populated last-entry-wins by the CLI.
			return p
		}
	}
	if name == "" {
		// No model template: command resolution falls back to AGY dispatch.
		return s.resolveTaskProvider(taskRequest{Provider: "agy"})
	}
	// Unknown providers retain the command resolver's distinct resolution error.
	return config.ProviderEntry{Name: name}
}

func checkReadOnlyEnforcement(provider config.ProviderEntry, permission string) error {
	if permission != "read_only" || provider.Class != "platform_dispatch" ||
		provider.EffectiveEnforcement() == config.EnforcementInProcess {
		return nil
	}
	return fmt.Errorf("E_READ_ONLY_UNENFORCEABLE: read_only cannot be mechanically enforced for %s; dispatch with -permission workspace_write (isolated worktree) or use a provider with in-process enforcement", provider.Name)
}
