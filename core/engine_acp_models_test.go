package core

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type testACPModelAgent struct {
	stubAgent
	catalog []ModelDetail
}

func (a *testACPModelAgent) UsesSessionModelSwitch() bool { return true }
func (a *testACPModelAgent) ListModelsDetail(_ context.Context) []ModelDetail {
	return append([]ModelDetail(nil), a.catalog...)
}

type testACPModelSession struct {
	stubAgentSession
	catalog       []ModelDetail
	lastModelID   string
	nativeCommand string
	err           error
}

func (s *testACPModelSession) NativeCompressCommand() string { return s.nativeCommand }

func (s *testACPModelSession) SessionModelDetails() []ModelDetail {
	return append([]ModelDetail(nil), s.catalog...)
}
func (s *testACPModelSession) SwitchSessionModel(modelID string) error {
	if s.err != nil {
		return s.err
	}
	for i, d := range s.catalog {
		if d.Name == modelID {
			s.lastModelID = modelID
			for j := range s.catalog {
				s.catalog[j].Current = j == i
			}
			return nil
		}
	}
	return errors.New("unknown model")
}
func attachACPModelSession(e *Engine, key string, a *testACPModelAgent, s *testACPModelSession) {
	iKey := e.interactiveKeyForSessionKey(key)
	e.interactiveMu.Lock()
	e.interactiveStates[iKey] = &interactiveState{agent: a, agentSession: s}
	e.interactiveMu.Unlock()
}

func TestACPGenericModelCardUsesDisplayNameButRPCUsesRawID(t *testing.T) {
	agent := &testACPModelAgent{catalog: []ModelDetail{{Name: "qfmodel", DisplayName: "Qwen3.8-Flash", Current: true, SwitchCommand: "/model qfmodel"}, {Name: "dfmodel", DisplayName: "DeepSeek-Flash", SwitchCommand: "/model dfmodel"}}}
	e := NewEngine("test", agent, nil, "", LangChinese)
	key := "feishu:chat:a"
	live := &testACPModelSession{catalog: append([]ModelDetail(nil), agent.catalog...)}
	attachACPModelSession(e, key, agent, live)
	card := e.renderModelCard(key)
	if card == nil || !strings.Contains(card.RenderText(), "Qwen3.8-Flash") {
		t.Fatalf("model display failed: %v", card)
	}
	e.interactiveMu.Lock()
	st := e.interactiveStates[e.interactiveKeyForSessionKey(key)]
	e.interactiveMu.Unlock()
	st.mu.Lock()
	count := len(st.modelsList.items)
	st.mu.Unlock()
	if count != 2 {
		t.Fatalf("model choices=%d", count)
	}
	result := e.handleModelsCardAction("switch 2", key)
	if live.lastModelID != "dfmodel" {
		t.Fatalf("wire model=%q", live.lastModelID)
	}
	if result == nil || !strings.Contains(result.RenderText(), "DeepSeek-Flash") {
		t.Fatalf("result did not show display name: %v", result)
	}
}

func TestACPGenericModelSwitchIsPerSessionAndNoGlobalSave(t *testing.T) {
	agent := &testACPModelAgent{catalog: []ModelDetail{{Name: "qfmodel", DisplayName: "Qwen3.8-Flash", SwitchCommand: "/model qfmodel"}, {Name: "dfmodel", DisplayName: "DeepSeek-Flash", SwitchCommand: "/model dfmodel"}}}
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)
	first := &testACPModelSession{catalog: append([]ModelDetail(nil), agent.catalog...)}
	second := &testACPModelSession{catalog: append([]ModelDetail(nil), agent.catalog...)}
	attachACPModelSession(e, "test:one", agent, first)
	attachACPModelSession(e, "test:two", agent, second)
	saved := false
	e.SetModelSaveFunc(func(string) error { saved = true; return nil })
	e.cmdModel(p, &Message{SessionKey: "test:one", ReplyCtx: "ctx"}, []string{"dfmodel"})
	if first.lastModelID != "dfmodel" || second.lastModelID != "" || saved {
		t.Fatalf("cross-session model drift: first=%q second=%q saved=%v", first.lastModelID, second.lastModelID, saved)
	}
	if err := func() error { _, err := e.switchLiveSessionModel("test:two", "unknown"); return err }(); err == nil {
		t.Fatal("unknown model accepted")
	}
}

func TestACPGenericCardWithoutActiveSessionFailsClosed(t *testing.T) {
	agent := &testACPModelAgent{catalog: []ModelDetail{{Name: "dfmodel", DisplayName: "DeepSeek-Flash", SwitchCommand: "/model dfmodel"}}}
	e := NewEngine("test", agent, nil, "", LangChinese)
	key := "feishu:chat:a"
	e.renderModelsCard(key, agent)
	result := e.handleModelsCardAction("switch 1", key)
	if result == nil || !strings.Contains(result.RenderText(), e.i18n.T(MsgModelNoSession)) {
		t.Fatalf("missing session must fail: %v", result)
	}
}

func TestACPGenericCompressCapabilityIsPerLiveChat(t *testing.T) {
	agent := &testACPModelAgent{}
	e := NewEngine("test", agent, nil, "", LangEnglish)
	one := &testACPModelSession{nativeCommand: "/compact"}
	two := &testACPModelSession{}
	attachACPModelSession(e, "test:one", agent, one)
	attachACPModelSession(e, "test:two", agent, two)
	e.interactiveMu.Lock()
	stateOne := e.interactiveStates[e.interactiveKeyForSessionKey("test:one")]
	stateTwo := e.interactiveStates[e.interactiveKeyForSessionKey("test:two")]
	e.interactiveMu.Unlock()
	if got := e.compressCommandForState(stateOne); got != "/compact" {
		t.Fatalf("one compact command=%q", got)
	}
	if got := e.compressCommandForState(stateTwo); got != "" {
		t.Fatalf("two must not inherit compact=%q", got)
	}
}

func TestACPGenericEmptyLiveCatalogOverridesSharedFallback(t *testing.T) {
	a := &testACPModelAgent{catalog: []ModelDetail{{Name: "qfmodel", DisplayName: "Qwen3.8-Flash"}}}
	e := NewEngine("test", a, nil, "", LangEnglish)
	key := "test:one"
	attachACPModelSession(e, key, a, &testACPModelSession{catalog: []ModelDetail{}})
	groups, total := e.collectSessionModelDetails(key, a)
	if total != 0 || len(groups) != 0 {
		t.Fatalf("empty live models leaked shared cache: %+v", groups)
	}
}

func TestACPGenericTextModelListIncludesDisplayName(t *testing.T) {
	a := &testACPModelAgent{catalog: []ModelDetail{{Name: "qfmodel", DisplayName: "Qwen3.8-Flash", SwitchCommand: "/model qfmodel"}}}
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", a, []Platform{p}, "", LangEnglish)
	e.cmdModelsText(p, &Message{SessionKey: "test:one", ReplyCtx: "ctx"}, a)
	sent := p.getSent()
	if len(sent) != 1 || !strings.Contains(sent[0], "Qwen3.8-Flash") || !strings.Contains(sent[0], "/model qfmodel") {
		t.Fatalf("text fallback missing display name or raw ID: %v", sent)
	}
}
