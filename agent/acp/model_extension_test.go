package acp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestModelExtensionRejectsUnknown(t *testing.T) {
	_, err := New(map[string]any{"command": "go", "extension": "unknown"})
	if err == nil {
		t.Fatal("expected unsupported extension error")
	}
}

func TestModelExtensionGenericDoesNotReadHermes(t *testing.T) {
	home := t.TempDir()
	profileDir := filepath.Join(home, "profiles", "isolated")
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		t.Fatal(err)
	}
	config := "model:\n  default: primary\n  provider: demo\nproviders:\n  demo:\n    base_url: https://models.example/v1\n"
	if err := os.WriteFile(filepath.Join(profileDir, "config.yaml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := "{\"custom:https://models.example/v1#fingerprint\":{\"at\":1,\"models\":[\"primary\"]}}"
	if err := os.WriteFile(filepath.Join(profileDir, "provider_models_cache.json"), []byte(cache), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERMES_HOME", home)
	opts := map[string]any{"command": "go", "args": []string{"--profile", "isolated", "acp"}, "extension": "hermes"}
	hermesAgent, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := hermesAgent.(*Agent).ListModelsDetail(context.Background()); len(got) == 0 {
		t.Fatal("Hermes fixture must expose models, otherwise generic isolation is not verified")
	}
	opts["extension"] = "generic"
	genericAgent, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := genericAgent.(*Agent).ListModelsDetail(context.Background()); len(got) != 0 {
		t.Fatalf("generic ACP must not read populated Hermes models: %+v", got)
	}
	if got := genericAgent.(*Agent).WorkspaceAgentOptions()["extension"]; got != "generic" {
		t.Fatalf("generic workspace extension = %v", got)
	}
}

func TestModelExtensionLegacyDefault(t *testing.T) {
	a, err := New(map[string]any{"command": "go"})
	if err != nil {
		t.Fatal(err)
	}
	impl := a.(*Agent)
	if impl.modelExtension != "hermes" {
		t.Fatalf("legacy extension = %q", impl.modelExtension)
	}
	if got := impl.WorkspaceAgentOptions()["extension"]; got != "hermes" {
		t.Fatalf("workspace extension = %v", got)
	}
}
