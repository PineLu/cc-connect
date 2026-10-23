package acp

import (
	"context"
	"strings"

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
	if extension == "hermes" {
		return hermes.ListModels(ctx, args)
	}
	a.modelsMu.RLock()
	defer a.modelsMu.RUnlock()
	return append([]core.ModelDetail(nil), a.modelsCache...)
}

// acpModelDetails maps protocol modelId and display name without conflating
// the two. The ID is used for RPC, while the label is used for cards/footer.
func acpModelDetails(models []acpModelInfo, current string) []core.ModelDetail {
	out := make([]core.ModelDetail, 0, len(models))
	for _, m := range models {
		id := strings.TrimSpace(m.ID)
		if id == "" {
			continue
		}
		out = append(out, core.ModelDetail{
			Name: id, DisplayName: strings.TrimSpace(m.Name),
			Note:          strings.TrimSpace(m.Description),
			Current:       id == current,
			SwitchCommand: "/model " + id,
		})
	}
	return out
}

func (a *Agent) reportModels(block acpModelBlock) {
	a.mu.RLock()
	extension := a.modelExtension
	a.mu.RUnlock()
	if extension != "generic" {
		return
	}
	a.modelsMu.Lock()
	defer a.modelsMu.Unlock()
	if block.AvailableModels != nil {
		a.modelsCache = acpModelDetails(block.AvailableModels, block.CurrentModelID)
	}
	if block.CurrentModelID != "" {
		a.modelsCurrent = block.CurrentModelID
	}
}
