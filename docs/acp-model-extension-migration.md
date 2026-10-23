# ACP model discovery extension (migration draft)

ACP protocol transport, sessions, permissions and event mapping remain generic.

Set `agent_options.extension = "generic"` for CodeBuddy or other non-Hermes ACP agents. Generic mode does not read `HERMES_HOME` or `~/.hermes` for model discovery; model listing may be unavailable until a generic capability is implemented.

Set `agent_options.extension = "hermes"` for existing Hermes-specific model discovery. For backward compatibility, omitted extension currently resolves to `hermes`. This legacy default is transitional and must not be changed without migrating all deployed Hermes project configs.

`model_command` and `compress_command` remain opt-in per agent. No runtime ACP compatibility test has been performed.

The Hermes implementation and tests now live in `agent/acp/extensions/hermes/`. Static Go tests confirm both legacy behavior and Generic-mode isolation, but do not replace an end-to-end ACP session smoke test.

Before considering a future default change to `generic`, audit every deployed Hermes workspace and set `agent_options.extension = "hermes"` explicitly. Then verify `/models` and model switching with an isolated Hermes session; leave the default unchanged until migration is validated. CodeBuddy handshake, permissions, resume, model switching, and reconnect remain separate runtime validation tasks. `go build ./...` requires generated `web/dist` assets; `go build -tags no_web ./...` validates the Go build without the web bundle.
