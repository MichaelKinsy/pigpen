# PiG for Paseo

A [Paseo](https://paseo.sh) plugin that adds PiG, a faithful Go implementation of Pi, as a coding agent provider. Paseo lists it as "PiG" with its own icon, checks that it is installed and runs it through `pig-acp`, the Agent Client Protocol adapter in [Pigpen](https://github.com/MichaelKinsy/pigpen/tree/main/components/acp). Each Paseo session starts one `pig --mode rpc` child, so PiG's tools, extensions and models behave as they do in the terminal.

## What you get

- A provider named PiG in the agent picker, with models and thinking levels read from your PiG configuration.
- A version check in the provider status and in `paseo provider diagnostic pig`. It runs `pig-acp --version` and `pig --version`, requires pig-acp 0.2.1 or later, and accepts both the bare Pi version that `pig --version` prints from PiG 0.7.1 and the `<PiG>+<Pi>` form of earlier releases.
- Approvals for extension dialogs (for example an extension that asks before a bash call) as Paseo permission requests.
- Edit and write tool calls drawn as file edits, with the old and new text when the call carries one replacement.

## Requirements

- Paseo 0.11.0 or later with **Enable plugins** on for the daemon you install into.
- `pig-acp` and `pig` on the daemon's `PATH`, or the commands below to point at them.
- A model PiG can reach. PiG keeps its own credentials: run `pig-acp --terminal-login` once in a terminal to sign in or configure keys.

## Install

The plugin lives in this directory of the Pigpen repository. From a checkout:

```bash
paseo plugin install /absolute/path/to/pigpen/components/paseo-provider-pig
paseo plugin ls
paseo provider diagnostic pig
```

`paseo plugin ls` should show `pig-provider` as `running`, and the diagnostic should end with `Status: Ready` and a line such as `pig-acp 0.2.2, Pi 1.5.0`.

## Point it at a different pig

The provider launches `pig-acp`, which drives the `pig` on `PATH`. Override either in `~/.paseo/config.json`, then run `paseo reload`:

```json
{
  "agents": {
    "providers": {
      "pig": { "command": ["pig-acp", "--pig", "/opt/pig/pig"] }
    }
  }
}
```

`PIG_ACP_PIG_COMMAND` in the provider's `env` does the same. `--piglet <name-or-file>` starts a Piglet instead of the stock agent, and `--pig-arg <arg>` adds an argument for pig.

## Without the plugin

Paseo can already run PiG from configuration alone, over either route.

PiG as an ACP agent:

```json
{
  "agents": {
    "providers": {
      "pig": { "extends": "acp", "label": "PiG", "command": ["pig-acp", "--pig", "pig"] }
    }
  }
}
```

PiG through Paseo's Pi provider, which runs `pi --mode rpc`:

```json
{
  "agents": {
    "providers": {
      "pi": { "command": ["pig", "--mode", "rpc"] }
    }
  }
}
```

Paseo does not gate the Pi provider on the version `--version` prints, so the 0.7.0 composite version and the bare version both work. The plugin adds the label, the icon, the install check and the edit rendering over the first route.

## Tests

```bash
node --test components/paseo-provider-pig/test/*.test.mjs
```

The unit tests need only Node 22.18 or later. The end-to-end check starts a scratch Paseo daemon with its own home and a private port, installs this plugin, points pig at a scripted local model and drives a plain turn, a read tool call, an approval that it allows and then denies, and an edit:

```bash
PASEO_BIN=/path/to/paseo PIG_BIN=/path/to/pig PIG_ACP_BIN=/path/to/pig-acp node components/paseo-provider-pig/test/e2e/e2e.mjs
```

To typecheck the plugin, copy it next to the `package.json` and `tsconfig.json` that `paseo plugin init` writes and run `npx tsc --noEmit`. Paseo supplies `@getpaseo/plugin` at run time.

## Verified with

Paseo 0.15.3 (daemon, CLI and SDK), pig-acp 0.2.1 and 0.2.2, and pig 0.7.0 and the 0.7.1 line, on Linux, against a scripted local model. Nothing in this directory is published yet.
