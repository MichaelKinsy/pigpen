# bend-sdk (experimental)

An extension SDK for [Bend 2](https://github.com/bendlang/bend), written in Bend. A Bend extension is one `.bend` file that
speaks the PiG subprocess wire. PiG runs it as an exact standalone: an executable file, no build step.

**Status: experimental.** It is not a Go extension and is not selected by any Piglet. Pigpen's Go-only gate reads Piglet
manifests only, so a Package that no Piglet selects needs no entry in `go-only-exceptions.json` (the gate rejects an entry
that no Piglet needs). Keep it out of every Piglet.

## Requirements

- **Bend 2.0.38, exactly.** Bend's effect ABI has no compatibility promise and it ships breaking changes in patch releases
  (its [CHANGELOG](https://github.com/bendlang/bend/blob/main/CHANGELOG.md), [EFFECTS.md](https://github.com/bendlang/bend/blob/main/guide/EFFECTS.md)).
  Install with `curl -fsSL https://bend-lang.com/install.sh | sh`. Set `BEND_NO_TELEMETRY=1` for runs under PiG: the `bend`
  command asks bend-lang.com for the latest version once a day.
- Linux or macOS. Bend has no Windows support (use WSL).
- PiG 0.5.2 or later (tested with 0.5.2).
- Native binaries (`bend ext.bend -o ext`) also need clang 19. The script form needs no compiler.

## Write an extension

```python
#!/usr/bin/env bend
import Base
import ./pig_sdk/pig.bend as Pig
import ./pig_sdk/json.bend as J

def schema() -> String:
  "{\"type\":\"object\",\"properties\":{\"who\":{\"type\":\"string\"}}}"

def hello(args: J.Json, ctx: Pig.Ctx) -> Pig.H(J.Json):
  do Pig.H<J.Json>:
    Pig.notify(ctx, "hello called", "info")
    return Pig.Result.text("hello " ++ J.Json.field_str(args, "who", "world"))

def new_extension() -> Pig.Ext:
  Pig.Ext.tools(Pig.Ext.new("hello", "0.1.0"), [Pig.Tool.new("hello", "Say hello", schema(), hello)])

def main() -> IO(Unit):
  Pig.run(~(u => new_extension()))
```

Rules that come from the host and from Bend:

- The file name (without `.bend`) is the extension's identity and must equal the name you register. `chmod +x` it and select it
  with `pig -e path/to/hello.bend`.
- Copy `pig_sdk/` next to your extension, or import it by a path. Imports are file paths; `import ../pig_sdk/pig.bend as Pig` works.
- `new_extension` is called again for every request, because Bend closures can be called once. Keep it pure and cheap.
- A handler is a plain function. A `+` parameter makes it a different type in Bend (WONTFIX #1111): take reused values in a helper, as `examples/bend-hello.bend` does.
- A failed host call ends the handler: `Pig.H` is an IO that can fail, so the host error becomes the request's error response.
- `bend` checks your file on every start (about 0.2 s). `bend ext.bend -o ext` builds a native binary that starts in about 1 ms.

## What works

Wire: register, ready, requests, responses, host calls, ping and pong, request state, cancel, shutdown, the 128 MiB frame cap.
Registration: tools (label, execution mode, prompt snippet and guidelines), commands, shortcuts, flags, event handlers.
Events: every generic event, and `tool_call` with its block result.
Host calls: `notify`, `set_status`, `set_title`, `set_working_message`, `set_working_visible`, `set_working_indicator`,
`set_hidden_thinking_label`, `select`, `confirm`, `input`, `editor`, `get/set_editor_text`, `paste_to_editor`,
`get/set_tools_expanded`, `get_all_themes`, `get_theme`, `send_message`, `send_user_message`, `append_entry`,
`set_session_name`, `get_session_name`, `set_label`, `get_flag`, `get/set_thinking_level`, `set_model`, `get/set_active_tools`,
`get_all_tools`, `get_commands`, `refresh_tools`, `get_context_usage`, `get_system_prompt`, `get_system_prompt_options`,
`get_session_id`, `get_session_file`, `get_leaf_id`, `is_idle`, `is_project_trusted`, `has_pending_messages`, `abort`,
`shutdown`, `reload`, `wait_for_idle`, `compact`, `exec`, `cancelled`, `tool_call_id`.

The test (`npm run test:bend-sdk`) proves the tool round trip, a host call whose answer only the host can give
(`getSessionID`), and one loud gap. The other host calls are written from the Go SDK's wire shapes but have no row yet.

## Limits (each fails loudly or is a documented Bend limit)

| Limit | What happens |
|---|---|
| More than about 4 s of pure computation between two effects | Bend runs one cooperative event loop, so the SDK cannot answer the host's heartbeat. After 5 s PiG kills the extension with `extension_unresponsive`. Split work with `IO.sleep(0)` or a host call. |
| `before_agent_start`, `agent_before_settle`, `turn_end`, `user_bash`, `context`, `context_with_system`, editing a `tool_call` input | The SDK stops at load with `event X is not supported by the Bend SDK` and exit 1. |
| Providers, OAuth, streaming, image and classifier APIs, virtual models, MCP servers, `complete`, model registry calls | No API: an extension that uses them does not check. |
| Message, entry and tool renderers, markdown transformer, argument completions, `prepareArguments`, `prepareLoadout` | No API. |
| Autocomplete providers, terminal input, custom components | No API. |
| `newSession`, `fork`, `navigateTree`, `switchSession`, session log (`getEntries`, `getBranch`), event bus | No API. |
| `ctx.signal` | `Pig.cancelled(ctx)` is a poll. Pure code is not interrupted. |
| Windows | Bend does not run there. |
| Packed and fused realizations | Not built. A Bend process shares one loop, so packing would let one extension starve another's heartbeat; fusing needs a Bend library target that does not exist (Bend issue #813). |
| Speed | JSON is parsed at about 1 MB/s on the script form and 3.4 MB/s native; large session replies are slow. |

## Run the test

```sh
PIG_BIN=/path/to/pig BEND_HOME=$HOME/.bend npm run test:bend-sdk
```

It runs the real `pig` with PiG's test-faux provider and a temporary HOME, `PIG_HOME`, `PI_HOME`, `PIG_CODING_AGENT_DIR` and
`PI_CODING_AGENT_DIR`. It needs no network and no credentials. To see it fail, copy the component, break `Wire.be32` in
`pig_sdk/pig.bend` (write the length header little endian) and set `BEND_SDK_UNDER_TEST` to the copy.

CI does not run it: the CI images do not carry Bend 2.0.38 and Pigpen has no hermetic way to install it (the installer
downloads from bend-lang.com and GitHub). Add a CI step only with a checksum-pinned download of the release archive
(`bend-2.0.38-linux-x64.tar.gz` and its sha256 from `install.sh`).

## Layout

- `pig_sdk/pig.bend`: the wire client and the author API. `json.bend`, `utf8.bend`: JSON and UTF-8 in Bend (numbers keep their
  spelling, lone surrogates survive). `unix.bend` with `unix_connect.js` and `unix_connect.c`: the Unix-socket effect Bend's Base lacks.
- `examples/bend-hello.bend`: tool plus two host calls. `examples/bend-gap.bend`: an unsupported event.
- `tests/bend.integration.test.mjs`: the behavioral test.

MIT, like the rest of Pigpen. No Bend source is copied: the foreign effects follow the documented contract and are original.
