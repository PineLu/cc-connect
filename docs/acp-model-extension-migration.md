# ACP model discovery extension (generic default)

ACP protocol transport, sessions, permissions and event mapping remain generic.

ACP defaults to `extension = "generic"` when omitted or empty. Generic mode does not read Hermes local model configuration; an explicit generic value remains valid.

Existing Hermes ACP projects must explicitly set `extension = "hermes"` under [projects.agent.options] to preserve local model discovery.

`model_command` and `compress_command` remain opt-in per agent. No runtime ACP compatibility test has been performed.

The Hermes implementation and tests now live in `agent/acp/extensions/hermes/`. Static Go tests confirm both legacy behavior and Generic-mode isolation, but do not replace an end-to-end ACP session smoke test.

Before deployment, back up the live configuration and explicitly set `extension = "hermes"` for every deployed Hermes ACP project, then verify Hermes model discovery.

## Command capability boundaries

- `/models` calls the vendor-specific offline `ModelLister` interface; `generic` returns no locally discovered models and the engine displays the existing "agent does not report a model list" message on text/card platforms. It is not a live ACP model-list probe.
- `/model` is independent of the extension selector. A nonempty `model_command` forwards the native slash command to the active session. Without it (or a separate `ModelSwitcher` implementation) the engine says model switching is unsupported. Do not invent `/model` support for new vendors.
- `/compress` is also opt-in using `compress_command`. The engine reports unsupported or no active session when appropriate. In multi-workspace mode the live session's agent determines the automatic compression command, not the engine default.
- `extension` is required to be a string when provided; unknown values and non-string values are configuration errors. Missing or empty values select `generic`.

## Example configuration

```toml
# Existing Hermes project (explicit opt-in required)
[projects.agent.options]
cmd = "hermes"
args = ["acp"]
extension = "hermes"
model_command = "/model"
compress_command = "/compress"
```

For a different project using a non-Hermes ACP server, set `extension = "generic"` and provide only native slash commands verified against that server. Do not copy the Hermes command options merely to make the controls appear available. A missing model list is not proof that the ACP handshake, chat or session capabilities are unavailable.

## Validation gates

Static tests verify option parsing, model isolation and workspace-specific compression dispatch. Real Hermes/CodeBuddy ACP handshake, permission, resume and reconnection tests are separate P2 gates. Live Hermes configuration must be migrated and backed up before deployment; static Go tests do not establish runtime compatibility.

## Hermes profile model discovery compatibility

Hermes provider configuration can declare models either as a YAML sequence of model IDs or as a mapping from model ID to options. Both shapes must be accepted; a single mapping must not prevent reading the other providers' model caches. For named profiles, the cache is read from the profile directory. If the cache is missing or invalid, explicitly declared models (and the provider default) remain available without querying any external endpoint. Regression tests use synthetic configurations and a separate read-only local profile probe verified the two cached custom providers plus fallbacks.

## Hermes slash model-switch compatibility (2026-10-23)

For named custom providers the Hermes extension constructs
`/model <model-id> --provider <provider-name>`,
instead of the ACP picker ID representation `custom:<provider-name>:<model-id>`.
Gateway ACP `slash_mutation` otherwise forwards the latter as a model string
and can cause the upstream to receive a bogus provider prefix. This is independent of
`extension=generic` and of the compiled Web frontend.
A previously pinned invalid Hermes session model must be switched again explicitly.

## Generic ACP model state (2026-10-23)

Generic ACP discovers `models.availableModels` from `session/new|load`.
The agent caches a catalog, while each active session preserves its own models/current
ID. The picker renders the human-readable `name` but uses the raw `modelId`
for `session/set_model`. Failed RPCs do not claim success. Hermes keeps its
provider-aware extension. Generic ACP exposes `/compact` only when an actual ACP
`available_commands_update` advertises the native command.
