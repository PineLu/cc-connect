package opencode

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/chenhg5/cc-connect/core"
)

func TestGetContextUsage_NilBeforeTokens(t *testing.T) {
	s, err := newOpencodeSession(context.Background(), "echo", nil, "/tmp", "", "default", "", "", nil)
	if err != nil {
		t.Fatalf("newOpencodeSession: %v", err)
	}
	defer s.Close()

	if got := s.GetContextUsage(); got != nil {
		t.Fatalf("GetContextUsage() before any step_finish = %v, want nil", got)
	}
}

func TestHandleStepFinishAbsorbsTokens(t *testing.T) {
	s, err := newOpencodeSession(context.Background(), "echo", nil, "/tmp", "opencode/mimo-v2.6-flash-free", "default", "", "", nil)
	if err != nil {
		t.Fatalf("newOpencodeSession: %v", err)
	}
	defer s.Close()

	// isolate models.json so ContextWindow is deterministic
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	modelsPath := filepath.Join(dir, "opencode", "models.json")
	if err := os.MkdirAll(filepath.Dir(modelsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := `{"opencode":{"models":{"mimo-v2.6-flash-free":{"limit":{"context":200000,"output":32000}}}}}`
	if err := os.WriteFile(modelsPath, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	// drop any cached lookup from other tests
	ctxWindowCacheMu.Lock()
	ctxWindowCache = map[string]int{}
	ctxWindowCacheMu.Unlock()

	s.handleEvent(map[string]any{
		"type": "step_finish",
		"part": map[string]any{
			"reason": "stop",
			"tokens": map[string]any{
				"total":     float64(56221),
				"input":     float64(52326),
				"output":    float64(55),
				"reasoning": float64(0),
				"cache": map[string]any{
					"write": float64(0),
					"read":  float64(3840),
				},
			},
		},
	})

	u := s.GetContextUsage()
	if u == nil {
		t.Fatal("GetContextUsage() = nil after step_finish, want snapshot")
	}
	// used = input + cache.read + cache.write = 52326 + 3840 + 0 = 56166
	if u.UsedTokens != 56166 {
		t.Errorf("UsedTokens = %d, want 56166", u.UsedTokens)
	}
	if u.InputTokens != 52326 {
		t.Errorf("InputTokens = %d, want 52326", u.InputTokens)
	}
	if u.OutputTokens != 55 {
		t.Errorf("OutputTokens = %d, want 55", u.OutputTokens)
	}
	if u.CachedInputTokens != 3840 {
		t.Errorf("CachedInputTokens = %d, want 3840", u.CachedInputTokens)
	}
	if u.CacheCreationInputTokens != 0 {
		t.Errorf("CacheCreationInputTokens = %d, want 0", u.CacheCreationInputTokens)
	}
	if u.TotalTokens != 56221 {
		t.Errorf("TotalTokens = %d, want 56221", u.TotalTokens)
	}
	if u.CumulativeInputTokens {
		t.Error("CumulativeInputTokens = true, want false (per-step snapshot)")
	}
	if u.ContextWindow != 200000 {
		t.Errorf("ContextWindow = %d, want 200000", u.ContextWindow)
	}
	// used+output == total
	if u.UsedTokens+u.OutputTokens != u.TotalTokens {
		t.Errorf("UsedTokens+OutputTokens = %d, want TotalTokens %d", u.UsedTokens+u.OutputTokens, u.TotalTokens)
	}
}

func TestSendEventResultIncludesTokenFields(t *testing.T) {
	s, err := newOpencodeSession(context.Background(), "echo", nil, "/tmp", "", "default", "", "", nil)
	if err != nil {
		t.Fatalf("newOpencodeSession: %v", err)
	}
	defer s.Close()

	s.absorbStepTokens(map[string]any{
		"tokens": map[string]any{
			"total":  float64(1000),
			"input":  float64(900),
			"output": float64(100),
			"cache": map[string]any{
				"write": float64(10),
				"read":  float64(40),
			},
		},
	})

	s.sendEventResult()

	select {
	case evt := <-s.events:
		if evt.Type != core.EventResult {
			t.Fatalf("evt.Type = %v, want EventResult", evt.Type)
		}
		if !evt.Done {
			t.Error("evt.Done = false, want true")
		}
		if evt.InputTokens != 900 {
			t.Errorf("evt.InputTokens = %d, want 900", evt.InputTokens)
		}
		if evt.OutputTokens != 100 {
			t.Errorf("evt.OutputTokens = %d, want 100", evt.OutputTokens)
		}
		if evt.CacheCreationInputTokens != 10 {
			t.Errorf("evt.CacheCreationInputTokens = %d, want 10", evt.CacheCreationInputTokens)
		}
		if evt.CacheReadInputTokens != 40 {
			t.Errorf("evt.CacheReadInputTokens = %d, want 40", evt.CacheReadInputTokens)
		}
	default:
		t.Fatal("no EventResult emitted")
	}
}

func TestGetContextUsage_ReturnsClone(t *testing.T) {
	s, err := newOpencodeSession(context.Background(), "echo", nil, "/tmp", "", "default", "", "", nil)
	if err != nil {
		t.Fatalf("newOpencodeSession: %v", err)
	}
	defer s.Close()

	s.absorbStepTokens(map[string]any{
		"tokens": map[string]any{"total": float64(10), "input": float64(8), "output": float64(2)},
	})
	first := s.GetContextUsage()
	second := s.GetContextUsage()
	if first == nil || second == nil {
		t.Fatal("GetContextUsage() returned nil")
	}
	if first == second {
		t.Fatal("GetContextUsage() returned the same pointer twice, want clones")
	}
	first.InputTokens = -1
	if second.InputTokens == -1 {
		t.Fatal("mutating first clone affected second")
	}
}

func TestAbsorbStepTokens_IgnoresMissingTokens(t *testing.T) {
	s, err := newOpencodeSession(context.Background(), "echo", nil, "/tmp", "", "default", "", "", nil)
	if err != nil {
		t.Fatalf("newOpencodeSession: %v", err)
	}
	defer s.Close()

	s.absorbStepTokens(map[string]any{"reason": "stop"})
	if got := s.GetContextUsage(); got != nil {
		t.Fatalf("GetContextUsage() after empty part = %v, want nil", got)
	}
}
