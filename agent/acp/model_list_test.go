package acp

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

	agent, err := New(map[string]any{
		"command": "sh",
		"args":    []any{"-p", "tujia", "acp"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	lister, ok := agent.(core.ModelLister)
	if !ok {
		t.Fatal("acp agent does not implement core.ModelLister")
	}
	got := lister.ListModelsDetail(t.Context())

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

func TestListModelsDetail_MissingProfile(t *testing.T) {
	t.Setenv("HERMES_HOME", filepath.Join(t.TempDir(), "does-not-exist"))
	agent, err := New(map[string]any{"command": "sh", "args": []any{"-p", "tujia", "acp"}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := agent.(core.ModelLister).ListModelsDetail(t.Context()); len(got) != 0 {
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
