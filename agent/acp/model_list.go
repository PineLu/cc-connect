package acp

import (
	"context"
	"github.com/chenhg5/cc-connect/agent/acp/extensions/hermes"
	"github.com/chenhg5/cc-connect/core"
)

var _ core.ModelLister = (*Agent)(nil)

// ListModelsDetail dispatches vendor-specific model discovery.
func (a *Agent) ListModelsDetail(ctx context.Context) []core.ModelDetail {
	a.mu.RLock()
	extension := a.modelExtension
	args := append(append([]string(nil), a.cliExtraArgs...), a.args...)
	a.mu.RUnlock()
	if extension != "hermes" {
		return nil
	}
	return hermes.ListModels(ctx, args)
}
