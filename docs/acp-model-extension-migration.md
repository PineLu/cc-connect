# ACP model discovery extension (migration draft)

ACP protocol transport, sessions, permissions and event mapping remain generic.

Set `agent_options.extension = "generic"` for CodeBuddy or other non-Hermes ACP agents. Generic mode does not read `HERMES_HOME` or `~/.hermes` for model discovery; model listing may be unavailable until a generic capability is implemented.

Set `agent_options.extension = "hermes"` for existing Hermes-specific model discovery. For backward compatibility, omitted extension currently resolves to `hermes`. This legacy default is transitional and must not be changed without migrating all deployed Hermes project configs.

`model_command` and `compress_command` remain opt-in per agent. No runtime ACP compatibility test has been performed.

Follow-up: move Hermes discovery implementation and tests into a dedicated extension package, then migrate production configuration and change the default to generic. Validate with Go tests and an isolated CodeBuddy ACP smoke test before deployment.
