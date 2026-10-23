package acp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/chenhg5/cc-connect/core"
)

func TestGenericACPModelCatalogAndHumanReadableFooter(t *testing.T) {
	agent := &Agent{modelExtension: "generic"}
	s, _, _ := newTestSession(t, agent)
	s.absorbModel(&acpModelBlock{
		CurrentModelID: "qfmodel",
		AvailableModels: []acpModelInfo{
			{ID: "qfmodel", Name: "Qwen3.8-Flash", Description: "0.00x Credit"},
			{ID: "dfmodel", Name: "DeepSeek-Flash", Description: "0.00x Credit"},
		},
	})
	if got := s.GetModel(); got != "Qwen3.8-Flash" {
		t.Fatalf("display model = %q", got)
	}
	details := s.SessionModelDetails()
	if len(details) != 2 || details[0].Name != "qfmodel" || details[0].DisplayName != "Qwen3.8-Flash" || !details[0].Current {
		t.Fatalf("session catalog = %+v", details)
	}
	agentList := agent.ListModelsDetail(s.ctx)
	if len(agentList) != 2 || agentList[1].Name != "dfmodel" {
		t.Fatalf("agent discovery = %+v", agentList)
	}
	details[0].Name = "evil"
	if s.SessionModelDetails()[0].Name != "qfmodel" {
		t.Fatal("session catalog returned mutable alias")
	}
	hermes := &Agent{modelExtension: "hermes"}
	if !hermes.UsesSessionModelSwitch() { /* must stay false */
	} else {
		t.Fatal("hermes must retain native model command")
	}
	if !agent.UsesSessionModelSwitch() {
		t.Fatal("generic ACP session model capability missing")
	}
}

func TestGenericACPSessionSetModel_UsesRawIDAndUpdatesAfterSuccess(t *testing.T) {
	a := &Agent{modelExtension: "generic"}
	s, wResp, rReq := newTestSession(t, a)
	s.absorbModel(&acpModelBlock{
		CurrentModelID: "qfmodel",
		AvailableModels: []acpModelInfo{
			{ID: "qfmodel", Name: "Qwen3.8-Flash"},
			{ID: "dfmodel", Name: "DeepSeek-Flash"},
		},
	})
	if err := s.SwitchSessionModel("DeepSeek-Flash"); err == nil {
		t.Fatal("display name must not be sent as modelId")
	}
	if s.GetModel() != "Qwen3.8-Flash" {
		t.Fatal("failed validation changed model")
	}
	request := make(chan string, 1)
	go func() {
		scan := bufio.NewScanner(rReq)
		for scan.Scan() {
			var env struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params struct {
					SessionID string `json:"sessionId"`
					ModelID   string `json:"modelId"`
				} `json:"params"`
			}
			if json.Unmarshal(scan.Bytes(), &env) != nil {
				continue
			}
			request <- env.Method + "|" + env.Params.SessionID + "|" + env.Params.ModelID
			_, _ = fmt.Fprintf(wResp, `{"jsonrpc":"2.0","id":%s,"result":{}}`+"\n", env.ID)
			return
		}
	}()
	if err := s.SwitchSessionModel("dfmodel"); err != nil {
		t.Fatal(err)
	}
	if got := <-request; got != "session/set_model|test-session-id|dfmodel" {
		t.Fatalf("RPC = %q", got)
	}
	if got := s.GetModel(); got != "DeepSeek-Flash" {
		t.Fatalf("after switch = %q", got)
	}
	if got := s.SessionModelDetails(); !got[1].Current || got[0].Current {
		t.Fatalf("current after switch %+v", got)
	}
}

func TestGenericACPSessionSetModel_RPCFailureKeepsOldModel(t *testing.T) {
	s, wResp, rReq := newTestSession(t, nil)
	s.absorbModel(&acpModelBlock{CurrentModelID: "a", AvailableModels: []acpModelInfo{{ID: "a"}, {ID: "b"}}})
	go func() {
		scan := bufio.NewScanner(rReq)
		if scan.Scan() {
			var env struct {
				ID json.RawMessage `json:"id"`
			}
			_ = json.Unmarshal(scan.Bytes(), &env)
			_, _ = fmt.Fprintf(wResp, `{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"unsupported"}}`+"\n", env.ID)
		}
	}()
	err := s.SwitchSessionModel("b")
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("RPC should fail: %v", err)
	}
	if s.GetModel() != "a" {
		t.Fatal("model must remain unchanged after failed RPC")
	}
}

func TestACPAdvertisedCompactAvailability(t *testing.T) {
	generic := &Agent{modelExtension: "generic"}
	s, _, _ := newTestSession(t, generic)
	if got := generic.CompressCommand(); got != "" {
		t.Fatalf("unadvertised command=%q", got)
	}
	s.maybeAbsorbAvailableCommands([]byte(`{"update":{"sessionUpdate":"available_commands_update","availableCommands":[{"name":"context"},{"name":"compact"}]}}`))
	if got := s.NativeCompressCommand(); got != "/compact" {
		t.Fatalf("advertised command=%q", got)
	}
	s.maybeAbsorbAvailableCommands([]byte(`{"update":{"sessionUpdate":"available_commands_update","availableCommands":[{"name":"context"}]}}`))
	if got := s.NativeCompressCommand(); got != "" {
		t.Fatalf("removed command still enabled=%q", got)
	}
	// Native Hermes config remains authoritative; no automatic assumptions.
	hermes := &Agent{modelExtension: "hermes", compressCommand: "/compress"}
	hermesSession, _, _ := newTestSession(t, hermes)
	hermesSession.maybeAbsorbAvailableCommands([]byte(`{"update":{"sessionUpdate":"available_commands_update","availableCommands":[{"name":"compact"}]}}`))
	if got := hermes.CompressCommand(); got != "/compress" {
		t.Fatalf("hermes command changed=%q", got)
	}
	custom := &Agent{modelExtension: "generic", compressCommand: "/summarize"}
	customSession, _, _ := newTestSession(t, custom)
	customSession.maybeAbsorbAvailableCommands([]byte(`{"update":{"sessionUpdate":"available_commands_update","availableCommands":[{"name":"compact"}]}}`))
	if got := custom.CompressCommand(); got != "/summarize" {
		t.Fatalf("explicit command overwritten=%q", got)
	}
}

func TestACPAvailableCommandsAreIsolatedPerSession(t *testing.T) {
	a := &Agent{modelExtension: "generic"}
	one, _, _ := newTestSession(t, a)
	two, _, _ := newTestSession(t, a)
	one.maybeAbsorbAvailableCommands([]byte(`{"update":{"sessionUpdate":"available_commands_update","availableCommands":[{"name":"compact"}]}}`))
	two.maybeAbsorbAvailableCommands([]byte(`{"update":{"sessionUpdate":"available_commands_update","availableCommands":[{"name":"context"}]}}`))
	if one.NativeCompressCommand() != "/compact" {
		t.Fatal("session one lost compact capability")
	}
	if two.NativeCompressCommand() != "" {
		t.Fatal("session two inherited compact from session one")
	}
}

func TestGenericACPExplicitEmptyModelListRevokesChoices(t *testing.T) {
	a := &Agent{modelExtension: "generic"}
	s, _, _ := newTestSession(t, a)
	s.absorbModel(&acpModelBlock{CurrentModelID: "old", AvailableModels: []acpModelInfo{{ID: "old", Name: "Old"}}})
	s.maybeAbsorbModelUpdate([]byte(`{"update":{"sessionUpdate":"available_models_update","availableModels":[]}}`))
	if got := s.SessionModelDetails(); len(got) != 0 {
		t.Fatalf("session retained withdrawn models: %+v", got)
	}
	if got := a.ListModelsDetail(s.ctx); len(got) != 0 {
		t.Fatalf("agent retained withdrawn models: %+v", got)
	}
}

func TestGenericACPModelCurrentNotification(t *testing.T) {
	s, _, _ := newTestSession(t, nil)
	s.absorbModel(&acpModelBlock{CurrentModelID: "qfmodel", AvailableModels: []acpModelInfo{{ID: "qfmodel", Name: "Qwen3.8-Flash"}, {ID: "dfmodel", Name: "DeepSeek-Flash"}}})
	s.maybeAbsorbModelUpdate([]byte(`{"update":{"sessionUpdate":"current_model_update","currentModelId":"dfmodel"}}`))
	if s.GetModel() != "DeepSeek-Flash" {
		t.Fatalf("notification current model=%q", s.GetModel())
	}
}

var _ core.LiveSessionModelSwitcher = (*acpSession)(nil)
var _ core.SessionModelSwitchAgent = (*Agent)(nil)
