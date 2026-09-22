package opencode

import (
	"context"
	"testing"
)

func TestSplitProviderModel(t *testing.T) {
	cases := []struct {
		in         string
		wantProv   string
		wantID     string
	}{
		{"opencode/mimo-v2.6-flash-free", "opencode", "mimo-v2.6-flash-free"},
		{"bare-model", "", "bare-model"},
		{"a/b/c", "a", "b/c"},
		{"", "", ""},
		{"/leading", "", "/leading"},
		{"trailing/", "", "trailing/"},
	}
	for _, tc := range cases {
		p, id := splitProviderModel(tc.in)
		if p != tc.wantProv || id != tc.wantID {
			t.Errorf("splitProviderModel(%q) = (%q, %q), want (%q, %q)", tc.in, p, id, tc.wantProv, tc.wantID)
		}
	}
}

func TestModelDetailCurrent(t *testing.T) {
	cases := []struct {
		name    string
		current string
		want    bool
	}{
		{"opencode/mimo", "opencode/mimo", true},
		{"opencode/mimo", "mimo", true},
		{"mimo", "opencode/mimo", true},
		{"opencode/mimo", "anthropic/mimo", false},
		{"opencode/mimo", "other", false},
		{"opencode/mimo", "", false},
		{"", "opencode/mimo", false},
	}
	for _, tc := range cases {
		if got := modelDetailCurrent(tc.name, tc.current); got != tc.want {
			t.Errorf("modelDetailCurrent(%q, %q) = %v, want %v", tc.name, tc.current, got, tc.want)
		}
	}
}

func TestListModelsDetail_GroupsByProvider(t *testing.T) {
	bin := writeFakeModelsBin(t, []string{"anthropic/claude-3-5-sonnet", "openai/gpt-4o", "bare-model"}, 0)
	a := &Agent{cmd: bin, activeIdx: -1, model: "openai/gpt-4o"}

	got := a.ListModelsDetail(context.Background())
	if len(got) != 3 {
		t.Fatalf("ListModelsDetail() len = %d, want 3", len(got))
	}

	byName := map[string]int{}
	for i, d := range got {
		byName[d.Name] = i
	}
	for _, name := range []string{"anthropic/claude-3-5-sonnet", "openai/gpt-4o", "bare-model"} {
		if _, ok := byName[name]; !ok {
			t.Fatalf("missing detail for %q: %+v", name, got)
		}
	}

	anth := got[byName["anthropic/claude-3-5-sonnet"]]
	if anth.Provider != "anthropic" || anth.ProviderLabel != "anthropic" {
		t.Errorf("anthropic detail provider = %q/%q, want anthropic/anthropic", anth.Provider, anth.ProviderLabel)
	}
	if anth.SwitchCommand != "/model anthropic/claude-3-5-sonnet" {
		t.Errorf("SwitchCommand = %q, want /model anthropic/claude-3-5-sonnet", anth.SwitchCommand)
	}
	if anth.Current {
		t.Error("anthropic detail Current = true, want false")
	}

	bare := got[byName["bare-model"]]
	if bare.Provider != "" || bare.ProviderLabel != "默认" {
		t.Errorf("bare detail provider = %q/%q, want empty/默认", bare.Provider, bare.ProviderLabel)
	}

	cur := got[byName["openai/gpt-4o"]]
	if !cur.Current {
		t.Error("openai/gpt-4o Current = false, want true (agent model matches)")
	}
}
