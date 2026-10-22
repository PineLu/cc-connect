package acp

import (
 "context"
 "testing"
)

func TestModelExtensionRejectsUnknown(t *testing.T) {
 _, err := New(map[string]any{"command":"go","extension":"unknown"})
 if err == nil { t.Fatal("expected unsupported extension error") }
}

func TestModelExtensionGenericDoesNotReadHermes(t *testing.T) {
 a := &Agent{modelExtension:"generic"}
 t.Setenv("HERMES_HOME", t.TempDir())
 if got := a.ListModelsDetail(context.Background()); len(got) != 0 {
  t.Fatalf("generic ACP should not read Hermes models: %+v",got)
 }
}

func TestModelExtensionLegacyDefault(t *testing.T) {
 a, err := New(map[string]any{"command":"go"})
 if err != nil { t.Fatal(err) }
 impl := a.(*Agent)
 if impl.modelExtension != "hermes" { t.Fatalf("legacy extension = %q",impl.modelExtension) }
 if got := impl.WorkspaceAgentOptions()["extension"]; got != "hermes" { t.Fatalf("workspace extension = %v",got) }
}
