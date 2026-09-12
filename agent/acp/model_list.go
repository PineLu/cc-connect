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

type hermesModelConfig struct {
	Model struct {
		Default  string `yaml:"default"`
		Provider string `yaml:"provider"`
	} `yaml:"model"`
	Providers map[string]struct {
		DefaultModel string `yaml:"default_model"`
	} `yaml:"providers"`
	FallbackProviders []struct {
		Provider string `yaml:"provider"`
		Model    string `yaml:"model"`
	} `yaml:"fallback_providers"`
}

type hermesModelCacheEntry struct {
	At     float64  `json:"at"`
	Models []string `json:"models"`
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
	// the "custom:*" cache key.
	var customModels []string
	var customAt float64
	for key, entry := range cache {
		if strings.HasPrefix(key, "custom:") {
			customModels, customAt = entry.Models, entry.At
			break
		}
	}
	for prov := range cfg.Providers {
		prefix := "custom:" + prov
		if len(customModels) > 0 {
			for _, m := range customModels {
				out = append(out, core.ModelDetail{
					Name:           m,
					Provider:       prov,
					ProviderLabel:  prov + " 自定源",
					CustomProvider: true,
					SwitchCommand:  acpSwitchCommand(prefix, m),
					Note:           "缓存 " + beijingTime(customAt),
					Current:        m == cfg.Model.Default,
				})
			}
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
			Current:        fb.Model == cfg.Model.Default,
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
				Current:        m == cfg.Model.Default,
			})
		}
	}
	return out
}
