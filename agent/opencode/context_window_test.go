package opencode

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpencodeContextWindow_FromModelsJSON(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	modelsPath := filepath.Join(dir, "opencode", "models.json")
	if err := os.MkdirAll(filepath.Dir(modelsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := `{
	  "opencode": {
	    "models": {
	      "mimo-v2.6-flash-free": {"limit": {"context": 200000, "output": 32000}},
	      "other-model": {"limit": {"context": 128000}}
	    }
	  },
	  "anthropic": {
	    "models": {
	      "claude-sonnet-4": {"limit": {"context": 200000}}
	    }
	  }
	}`
	if err := os.WriteFile(modelsPath, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	ctxWindowCacheMu.Lock()
	ctxWindowCache = map[string]int{}
	ctxWindowCacheMu.Unlock()

	cases := []struct {
		model string
		want  int
	}{
		{"opencode/mimo-v2.6-flash-free", 200000},
		{"mimo-v2.6-flash-free", 200000},
		{"other-model", 128000},
		{"anthropic/claude-sonnet-4", 200000},
		{"unknown/model", 0},
		{"", 0},
	}
	for _, tc := range cases {
		if got := lookupOpencodeContextWindow(tc.model); got != tc.want {
			t.Errorf("lookupOpencodeContextWindow(%q) = %d, want %d", tc.model, got, tc.want)
		}
	}
}

func TestOpencodeContextWindow_MissingFile(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	ctxWindowCacheMu.Lock()
	ctxWindowCache = map[string]int{}
	ctxWindowCacheMu.Unlock()
	if got := lookupOpencodeContextWindow("opencode/mimo-v2.6-flash-free"); got != 0 {
		t.Errorf("lookupOpencodeContextWindow() = %d, want 0 when file missing", got)
	}
}
