package opencode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// opencodeModelsJSON is OpenCode's local models.dev cache. Shape:
//
//	{
//	  "opencode": {
//	    "models": {
//	      "mimo-v2.6-flash-free": {"limit": {"context": 200000, "output": 32000}}
//	    }
//	  }
//	}
type opencodeModelsJSON map[string]struct {
	Models map[string]struct {
		Limit struct {
			Context int `json:"context"`
		} `json:"limit"`
	} `json:"models"`
}

var (
	ctxWindowCacheMu sync.Mutex
	ctxWindowCache   = map[string]int{} // model id -> window (>0) ; missing key = not found
)

// opencodeModelsCachePath returns the models.dev cache file OpenCode maintains
// under XDG_CACHE_HOME (or ~/.cache on darwin/linux).
func opencodeModelsCachePath() string {
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "opencode", "models.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".cache", "opencode", "models.json")
}

// lookupOpencodeContextWindow resolves the model's context window from
// OpenCode's models.json cache. model may be "provider/id" or bare "id".
// Returns 0 when unknown. Results (including misses) are cached in-process.
func lookupOpencodeContextWindow(model string) int {
	model = strings.TrimSpace(model)
	if model == "" {
		return 0
	}

	ctxWindowCacheMu.Lock()
	if w, ok := ctxWindowCache[model]; ok {
		ctxWindowCacheMu.Unlock()
		return w
	}
	ctxWindowCacheMu.Unlock()

	window := 0
	if path := opencodeModelsCachePath(); path != "" {
		if data, err := os.ReadFile(path); err == nil {
			var providers opencodeModelsJSON
			if json.Unmarshal(data, &providers) == nil {
				window = contextWindowFromProviders(providers, model)
			}
		}
	}

	ctxWindowCacheMu.Lock()
	ctxWindowCache[model] = window
	ctxWindowCacheMu.Unlock()
	return window
}

// contextWindowFromProviders picks limit.context for model across the
// provider-keyed cache. Accepts "provider/id" (exact provider match first)
// and bare "id" (search every provider).
func contextWindowFromProviders(providers opencodeModelsJSON, model string) int {
	provider, id := "", model
	if i := strings.Index(model, "/"); i > 0 && i < len(model)-1 {
		provider, id = model[:i], model[i+1:]
	}

	if provider != "" {
		if p, ok := providers[provider]; ok {
			if m, ok := p.Models[id]; ok && m.Limit.Context > 0 {
				return m.Limit.Context
			}
		}
	}

	// Bare id, or provider/id not found under that provider: search all.
	for _, p := range providers {
		if m, ok := p.Models[id]; ok && m.Limit.Context > 0 {
			return m.Limit.Context
		}
	}
	return 0
}
