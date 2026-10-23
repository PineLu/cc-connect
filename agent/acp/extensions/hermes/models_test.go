package hermes

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chenhg5/cc-connect/core"
)

func TestHermesProfileFromArgs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"short flag", []string{"-p", "tujia", "acp"}, "tujia"},
		{"long flag", []string{"--profile", "tujia", "acp"}, "tujia"},
		{"equals form", []string{"--profile=tujia", "acp"}, "tujia"},
		{"absent", []string{"acp"}, ""},
		{"dangling flag", []string{"-p"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hermesProfileFromArgs(tc.args); got != tc.want {
				t.Fatalf("hermesProfileFromArgs(%v) = %q, want %q", tc.args, got, tc.want)
			}
		})
	}
}

func TestHermesProfileDir(t *testing.T) {
	home := filepath.Join("/tmp", "hermes-home")
	if got, want := hermesProfileDir(home, ""), home; got != want {
		t.Fatalf("default profile dir = %q, want %q", got, want)
	}
	want := filepath.Join(home, "profiles", "tujia")
	if got := hermesProfileDir(home, "tujia"); got != want {
		t.Fatalf("named profile dir = %q, want %q", got, want)
	}
}

// writeHermesProfile lays out a minimal profile: one custom endpoint with a
// cached model list, two config-declared fallbacks, and one cache-only provider.
func writeHermesProfile(t *testing.T, home string) {
	t.Helper()
	dir := hermesProfileDir(home, "tujia")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "model:\n" +
		"  default: muse-spark-1.3\n" +
		"  provider: new-api-01\n" +
		"providers:\n" +
		"  new-api-01:\n" +
		"    default_model: z-ai/glm-5.2\n" +
		"fallback_providers:\n" +
		"  - provider: nous\n" +
		"    model: stepfun/step-3.7-flash:free\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := `{
	  "custom:http://localhost:3003/v1#fp": {"at": 1789196822.1, "models": ["glm-5.3-flash", "cbcn/glm-5.3", "muse-spark-1.3"]},
	  "opencode-free": {"at": 1789196822.9, "models": ["mimo-v2.5-free"]}
	}`
	if err := os.WriteFile(filepath.Join(dir, "provider_models_cache.json"), []byte(cache), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListModelsDetail_Hermes(t *testing.T) {
	home := t.TempDir()
	writeHermesProfile(t, home)
	t.Setenv("HERMES_HOME", home)

	got := ListModels(t.Context(), []string{"-p", "tujia", "acp"})

	byCommand := map[string]core.ModelDetail{}
	for _, d := range got {
		byCommand[d.SwitchCommand] = d
	}

	// A user-defined endpoint must be addressed with the custom: prefix, and a
	// model id containing a colon must survive intact.
	for _, want := range []string{
		"/model custom:new-api-01:glm-5.3-flash",
		"/model custom:new-api-01:cbcn/glm-5.3",
		"/model custom:new-api-01:muse-spark-1.3",
		"/model nous:stepfun/step-3.7-flash:free",
		"/model opencode-free:mimo-v2.5-free",
	} {
		if _, ok := byCommand[want]; !ok {
			t.Errorf("missing switch command %q (got %v)", want, keysOf(byCommand))
		}
	}

	if d := byCommand["/model custom:new-api-01:muse-spark-1.3"]; !d.Current {
		t.Error("default model not marked Current")
	}
	if d := byCommand["/model custom:new-api-01:glm-5.3-flash"]; d.Current {
		t.Error("non-default model marked Current")
	}
	if d := byCommand["/model custom:new-api-01:glm-5.3-flash"]; !d.CustomProvider {
		t.Error("custom endpoint not flagged CustomProvider")
	}
	if d := byCommand["/model nous:stepfun/step-3.7-flash:free"]; d.CustomProvider {
		t.Error("built-in provider wrongly flagged CustomProvider")
	}
}

func TestListModelsDetail_MultipleCustomProvidersDoNotShareCache(t *testing.T) {
	home := t.TempDir()
	dir := hermesProfileDir(home, "tujia")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "model:\n" +
		"  default: shared-model\n" +
		"  provider: provider-b\n" +
		"providers:\n" +
		"  provider-a:\n" +
		"    base_url: https://a.example/v1\n" +
		"  provider-b:\n" +
		"    base_url: https://b.example/v1\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := `{
	  "custom:https://a.example/v1#aaa": {"at": 1789196822.1, "models": ["shared-model", "only-a"]},
	  "custom:https://b.example/v1#bbb": {"at": 1789196823.1, "models": ["shared-model", "only-b"]}
	}`
	if err := os.WriteFile(filepath.Join(dir, "provider_models_cache.json"), []byte(cache), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERMES_HOME", home)

	got := ListModels(t.Context(), []string{"-p", "tujia", "acp"})

	byCommand := map[string]core.ModelDetail{}
	for _, d := range got {
		byCommand[d.SwitchCommand] = d
	}
	for _, want := range []string{
		"/model custom:provider-a:only-a",
		"/model custom:provider-b:only-b",
	} {
		if _, ok := byCommand[want]; !ok {
			t.Fatalf("missing %q; got %v", want, keysOf(byCommand))
		}
	}
	for _, wrong := range []string{
		"/model custom:provider-a:only-b",
		"/model custom:provider-b:only-a",
	} {
		if _, ok := byCommand[wrong]; ok {
			t.Fatalf("provider cache leaked across endpoints: unexpectedly found %q", wrong)
		}
	}
	if byCommand["/model custom:provider-a:shared-model"].Current {
		t.Fatal("provider-a shared model must not be marked current when provider-b is active")
	}
	if !byCommand["/model custom:provider-b:shared-model"].Current {
		t.Fatal("provider-b shared model should be marked current")
	}
}

// A real Hermes profile may declare provider models as a mapping of model IDs
// to options, rather than a YAML sequence. That representation must not make
// model discovery discard otherwise valid cached providers.
func TestListModelsDetail_ProviderModelsMappingAndTwoCaches(t *testing.T) {
	home := t.TempDir()
	dir := hermesProfileDir(home, "tujia")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "model:\n  default: beta\n  provider: cpa\n" +
		"providers:\n" +
		"  gateway:\n    api: https://first.example/v1\n    default_model: alpha\n" +
		"  cpa:\n    base_url: https://second.example/v1\n    model: beta\n" +
		"    models:\n      beta: {}\n      gamma: {}\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	cache := "{\"custom:https://first.example/v1#f1\":{\"at\":1,\"models\":[\"alpha\"]}," +
		"\"custom:https://second.example/v1#f2\":{\"at\":2,\"models\":[\"beta\",\"gamma\"]}}"
	if err := os.WriteFile(filepath.Join(dir, "provider_models_cache.json"), []byte(cache), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERMES_HOME", home)
	got := ListModels(t.Context(), []string{"-p", "tujia", "acp"})
	byCommand := map[string]core.ModelDetail{}
	for _, model := range got {
		byCommand[model.SwitchCommand] = model
	}
	for _, command := range []string{
		"/model custom:gateway:alpha",
		"/model custom:cpa:beta",
		"/model custom:cpa:gamma",
	} {
		if _, ok := byCommand[command]; !ok {
			t.Fatalf("missing %s; got %v", command, keysOf(byCommand))
		}
	}
	if !byCommand["/model custom:cpa:beta"].Current {
		t.Fatal("cpa beta must be current")
	}
	if byCommand["/model custom:gateway:alpha"].Current {
		t.Fatal("gateway alpha must not be current")
	}
}

func TestListModelsDetail_ProviderModelsMappingWithoutCache(t *testing.T) {
	home := t.TempDir()
	dir := hermesProfileDir(home, "tujia")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "model:\n  default: beta\n  provider: cpa\n" +
		"providers:\n  cpa:\n    base_url: https://second.example/v1\n" +
		"    models:\n      beta: {}\n      gamma: {}\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERMES_HOME", home)
	got := ListModels(t.Context(), []string{"-p", "tujia", "acp"})
	byCommand := map[string]core.ModelDetail{}
	for _, model := range got {
		byCommand[model.SwitchCommand] = model
	}
	for _, command := range []string{"/model custom:cpa:beta", "/model custom:cpa:gamma"} {
		if _, ok := byCommand[command]; !ok {
			t.Fatalf("missing static model %s", command)
		}
	}
}

func TestListModelsDetail_MissingProfile(t *testing.T) {
	t.Setenv("HERMES_HOME", filepath.Join(t.TempDir(), "does-not-exist"))
	if got := ListModels(t.Context(), []string{"-p", "tujia", "acp"}); len(got) != 0 {
		t.Fatalf("expected no models for a missing profile, got %d", len(got))
	}
}

func keysOf(m map[string]core.ModelDetail) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
