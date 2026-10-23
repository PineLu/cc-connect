# ACP model discovery extension (migration draft)

ACP protocol transport, sessions, permissions and event mapping remain generic.

Under [projects.agent.options], set `extension = "generic"` for CodeBuddy or other non-Hermes ACP agents. Generic mode does not read `HERMES_HOME` or `~/.hermes` for model discovery; model listing may be unavailable until a generic capability is implemented.

Under [projects.agent.options], set `extension = "hermes"` for existing Hermes-specific model discovery. For backward compatibility, omitted extension currently resolves to `hermes`. This legacy default is transitional and must not be changed without migrating all deployed Hermes project configs.

`model_command` and `compress_command` remain opt-in per agent. No runtime ACP compatibility test has been performed.

The Hermes implementation and tests now live in `agent/acp/extensions/hermes/`. Static Go tests confirm both legacy behavior and Generic-mode isolation, but do not replace an end-to-end ACP session smoke test.

Before considering a future default change to `generic`, audit every deployed Hermes workspace and set `extension = "hermes"` under [projects.agent.options] explicitly. Then verify `/models` and model switching with an isolated Hermes session; leave the default unchanged until migration is validated. CodeBuddy handshake, permissions, resume, model switching, and reconnect remain separate runtime validation tasks. `go build ./...` requires generated `web/dist` assets; `go build -tags no_web ./...` validates the Go build without the web bundle.

## Command capability boundaries

- `/models` calls the vendor-specific offline `ModelLister` interface; `generic` returns no locally discovered models and the engine displays the existing "agent does not report a model list" message on text/card platforms. It is not a live ACP model-list probe.
- `/model` is independent of the extension selector. A nonempty `model_command` forwards the native slash command to the active session. Without it (or a separate `ModelSwitcher` implementation) the engine says model switching is unsupported. Do not invent `/model` support for new vendors.
- `/compress` is also opt-in using `compress_command`. The engine reports unsupported or no active session when appropriate. In multi-workspace mode the live session's agent determines the automatic compression command, not the engine default.
- `extension` is required to be a string when provided; unknown values and non-string values are configuration errors. Missing or empty values preserve the transitional `hermes` default.

## Example configuration

```toml
# Existing Hermes project (migrate explicitly before changing defaults)
[projects.agent.options]
cmd = "hermes"
args = ["acp"]
extension = "hermes"
model_command = "/model"
compress_command = "/compress"
```

For a different project using a non-Hermes ACP server, set `extension = "generic"` and provide only native slash commands verified against that server. Do not copy the Hermes command options merely to make the controls appear available. A missing model list is not proof that the ACP handshake, chat or session capabilities are unavailable.

## Validation gates

Static tests verify option parsing, model isolation and workspace-specific compression dispatch. Real Hermes/CodeBuddy ACP handshake, permission, resume and reconnection tests are separate P2 gates. Never change live Hermes configuration or the default extension as part of a code-only migration.
