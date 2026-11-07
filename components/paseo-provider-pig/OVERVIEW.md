Adds PiG, a faithful Go implementation of Pi, to Paseo as a coding agent provider.

Paseo starts `pig-acp`, the Agent Client Protocol adapter for PiG, and talks to it over standard input and output. `pig-acp` starts one `pig --mode rpc` process for each Paseo session, so PiG's tools, extensions, skills and models work as they do in the terminal. The provider appears as PiG in the agent picker, with the models and thinking levels from your PiG configuration.

## Setup

- Paseo 0.11.0 or later, with plugins enabled on the daemon.
- `pig-acp` 0.2.1 or later and `pig` on the daemon's command path. To use other locations, set the provider command in the daemon configuration, for example `["pig-acp", "--pig", "/opt/pig/pig"]`.
- PiG keeps its own credentials. Run `pig-acp --terminal-login` once in a terminal to sign in or configure keys.

## Capabilities and limits

- The provider status runs `pig-acp --version` and `pig --version`. It reports the versions it found, or what is missing. `pig --version` may print Pi's bare version or the PiG and Pi versions joined with a plus sign.
- Extension dialogs, such as an extension that asks before a bash call, arrive as Paseo permission requests. Allowing or denying one answers the dialog in PiG.
- Edit and write tool calls are drawn as file edits.
- The plugin sends no data anywhere and downloads nothing. Prompts and tool results go to whichever model provider your PiG configuration selects.
- Paseo's provider settings cover the model and thinking level. Other PiG settings stay in PiG's own configuration.
