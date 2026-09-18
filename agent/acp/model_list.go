package acp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chenhg5/cc-connect/core"
	"gopkg.in/yaml.v3"
)

// hermesProfileFromArgs extracts the Hermes profile name from the ACP agent's
// launch args (e.g. ["-p", "tujia", "acp"]). Empty when the agent was not
// started with an explicit profile.
func hermesProfileFromArgs(args []string) string {
	for i, a := range args {
		if (a == "-p" || a == "--profile") && i+1 < len(args) {
			return strings.TrimSpace(args[i+1])
		}
		if strings.HasPrefix(a, "--profile=") {
			return strings.TrimSpace(strings.TrimPrefix(a, "--profile="))
		}
	}
	return ""
}

// hermesHomeDir resolves the Hermes home directory the same way the CLI does:
// $HERMES_HOME wins, otherwise ~/.hermes.
func hermesHomeDir() string {
	if h := strings.TrimSpace(os.Getenv("HERMES_HOME")); h != "" {
		return h
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".hermes")
	}
	return ""
}

// hermesProfileDir returns the config/cache directory for a profile. Profiles
// live under $HERMES_HOME/profiles/<name>/; an empty profile name means the
// default profile rooted at $HERMES_HOME itself.
func hermesProfileDir(home, profile string) string {
	if profile == "" {
		return home
	}
	return filepath.Join(home, "profiles", profile)
}

type hermesProviderConfig struct {
	DefaultModel string   `yaml:"default_model"`
	API          string   `yaml:"api"`
	BaseURL      string   `yaml:"base_url"`
	URL          string   `yaml:"url"`
	Models       []string `yaml:"models"`
}

type hermesModelConfig struct {
	Model struct {
		Default  string `yaml:"default"`
		Provider string `yaml:"provider"`
		BaseURL  string `yaml:"base_url"`
	} `yaml:"model"`
	Providers map[string]hermesProviderConfig `yaml:"providers"`
	FallbackProviders []struct {
		Provider string `yaml:"provider"`
		Model    string `yaml:"model"`
	} `yaml:"fallback_providers"`
}

type hermesModelCacheEntry struct {
	At     float64  `json:"at"`
	Models []string `json:"models"`
}

func normalizeHermesEndpoint(v string) string {
	return strings.TrimRight(strings.TrimSpace(v), "/")
}

func hermesProviderEndpoint(p hermesProviderConfig) string {
	for _, v := range []string{p.API, p.BaseURL, p.URL} {
		if v = normalizeHermesEndpoint(v); v != "" {
			return v
		}
	}
	return ""
}

func customCacheEndpoint(key string) string {
	if !strings.HasPrefix(key, "custom:") {
		return ""
	}
	v := strings.TrimPrefix(key, "custom:")
	if i := strings.LastIndex(v, "#"); i >= 0 {
		v = v[:i]
	}
	return normalizeHermesEndpoint(v)
}

func hermesCurrentModel(cfg hermesModelConfig, providerName, providerEndpoint, model string) bool {
	if model == "" || model != cfg.Model.Default {
		return false
	}
	if cfg.Model.Provider == providerName {
		return true
	}
	return cfg.Model.Provider == "custom" &&
		normalizeHermesEndpoint(cfg.Model.BaseURL) != "" &&
		normalizeHermesEndpoint(cfg.Model.BaseURL) == normalizeHermesEndpoint(providerEndpoint)
}

func beijingTime(ts float64) string {
	t := time.Unix(int64(ts), 0).In(time.FixedZone("CST", 8*3600))
	return t.Format("2006-01-02 15:04:05")
}

// acpSwitchCommand builds the copy-pasteable switch command for the ACP
// (Hermes) switch path: acp_adapter parses "provider:model" with no flag
// support, and user-defined endpoints must carry the "custom:" prefix.
func acpSwitchCommand(prefix, model string) string {
	return "/model " + prefix + ":" + model
}

var _ core.ModelLister = (*Agent)(nil)

// ListModelsDetail implements core.ModelLister for Hermes-backed ACP agents.
// It reads the Hermes profile's own config.yaml and provider_models_cache.json
// directly — no network, no LLM — so it still answers while the currently
// selected model is unreachable.
func (a *Agent) ListModelsDetail(ctx context.Context) []core.ModelDetail {
	a.mu.RLock()
	args := append(append([]string(nil), a.cliExtraArgs...), a.args...)
	a.mu.RUnlock()

	profile := hermesProfileFromArgs(args)
	dir := hermesProfileDir(hermesHomeDir(), profile)
	if dir == "" {
		return nil
	}

	var cfg hermesModelConfig
	raw, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil {
		return nil
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil
	}

	rawCache, err := os.ReadFile(filepath.Join(dir, "provider_models_cache.json"))
	if err != nil {
		return nil
	}
	var cache map[string]hermesModelCacheEntry
	if err := json.Unmarshal(rawCache, &cache); err != nil {
		return nil
	}

	var out []core.ModelDetail

	// Custom (user-defined) endpoints carry the live /v1/models listing under
	// cache keys shaped like "custom:<endpoint>#<fingerprint>". Match those
	// entries back to the named provider's configured endpoint instead of
	// reusing the first custom cache entry for every provider.
	customByEndpoint := make(map[string]hermesModelCacheEntry)
	var singleCustom *hermesModelCacheEntry
	customCount := 0
	for key, entry := range cache {
		endpoint := customCacheEndpoint(key)
		if endpoint == "" {
			continue
		}
		customByEndpoint[endpoint] = entry
		entryCopy := entry
		singleCustom = &entryCopy
		customCount++
	}
	for prov, providerCfg := range cfg.Providers {
		prefix := "custom:" + prov
		endpoint := hermesProviderEndpoint(providerCfg)
		entry, ok := customByEndpoint[endpoint]
		// Backward-compatible fallback for older/minimal Hermes configs that
		// omit the endpoint: it is only safe when both sides are unambiguous.
		if !ok && endpoint == "" && len(cfg.Providers) == 1 && customCount == 1 && singleCustom != nil {
			entry = *singleCustom
			ok = true
		}
		models := entry.Models
		if !ok || len(models) == 0 {
			// A declared static model list/default is still useful when model
			// discovery is disabled or its cache is unavailable.
			models = append([]string(nil), providerCfg.Models...)
			if len(models) == 0 && providerCfg.DefaultModel != "" {
				models = []string{providerCfg.DefaultModel}
			}
		}
		for _, m := range models {
			out = append(out, core.ModelDetail{
				Name:           m,
				Provider:       prov,
				ProviderLabel:  prov + " 自定源",
				CustomProvider: true,
				SwitchCommand:  acpSwitchCommand(prefix, m),
				Note:           func() string { if ok { return "缓存 " + beijingTime(entry.At) }; return "config 指定" }(),
				Current:        hermesCurrentModel(cfg, prov, endpoint, m),
			})
		}
	}

	for _, fb := range cfg.FallbackProviders {
		if fb.Model == "" || fb.Provider == "" {
			continue
		}
		out = append(out, core.ModelDetail{
			Name:           fb.Model,
			Provider:       fb.Provider,
			ProviderLabel:  fb.Provider,
			SwitchCommand:  acpSwitchCommand(fb.Provider, fb.Model),
			Note:           "fallback，config 指定",
			Current:        fb.Model == cfg.Model.Default && fb.Provider == cfg.Model.Provider,
		})
	}

	declared := map[string]bool{}
	for prov := range cfg.Providers {
		declared[prov] = true
	}
	for key, entry := range cache {
		if strings.HasPrefix(key, "custom:") || declared[key] {
			continue
		}
		for _, m := range entry.Models {
			out = append(out, core.ModelDetail{
				Name:           m,
				Provider:       key,
				ProviderLabel:  key,
				SwitchCommand:  acpSwitchCommand(key, m),
				Note:           "仅缓存有、config 未声明，能否切换不确定，缓存 " + beijingTime(entry.At),
				Current:        m == cfg.Model.Default && key == cfg.Model.Provider,
			})
		}
	}
	return out
}
