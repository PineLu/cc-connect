package opencode

import (
	"context"
	"strings"

	"github.com/chenhg5/cc-connect/core"
)

var _ core.ModelLister = (*Agent)(nil)

// ListModelsDetail implements core.ModelLister so /models can group entries
// by provider (the "provider/id" prefix OpenCode discovery returns) instead
// of falling back to a flat AvailableModels list.
func (a *Agent) ListModelsDetail(ctx context.Context) []core.ModelDetail {
	models := a.AvailableModels(ctx)
	current := a.GetModel()
	out := make([]core.ModelDetail, 0, len(models))
	for _, m := range models {
		provider, _ := splitProviderModel(m.Name)
		label := provider
		if label == "" {
			label = "默认"
		}
		out = append(out, core.ModelDetail{
			Name:          m.Name,
			Provider:      provider,
			ProviderLabel: label,
			SwitchCommand: "/model " + m.Name,
			Note:          m.Desc,
			Current:       modelDetailCurrent(m.Name, current),
		})
	}
	return out
}

// splitProviderModel splits "provider/id" into its parts. Bare ids yield
// empty provider.
func splitProviderModel(name string) (provider, id string) {
	if i := strings.Index(name, "/"); i > 0 && i < len(name)-1 {
		return name[:i], name[i+1:]
	}
	return "", name
}

// modelDetailCurrent reports whether a discovered "provider/id" matches the
// agent's configured model, tolerating a missing provider prefix on either
// side (config may store a bare id).
func modelDetailCurrent(name, current string) bool {
	if current == "" || name == "" {
		return false
	}
	if name == current {
		return true
	}
	np, nid := splitProviderModel(name)
	cp, cid := splitProviderModel(current)
	if nid != cid {
		return false
	}
	// both have a provider: must match exactly
	if np != "" && cp != "" {
		return np == cp
	}
	// one side is a bare id: match on the bare id
	return true
}
