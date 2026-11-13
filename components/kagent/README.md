# kagent: PiG policy extension for the kagent harness

The PiG side of running PiG as a [kagent](https://github.com/kagent-dev/kagent) native harness. A harness adapter writes a
policy file and starts `pig --mode rpc`; this extension turns the policy into PiG behaviour:

- registers each MCP server the kagent Agent binds, every tool `direct`, or only the allowlisted tools;
- asks for approval before a call of a tool of a server that `require_approval`, through one RPC `confirm` request;
- offers an `ask_user` tool that puts questions to the user through one RPC `confirm` request.

It does nothing unless `KAGENT_PIG_POLICY` names a file, so it is safe to fuse into any Binary. kagent is built by its own
authors (Apache-2.0); this Package is an independent work that follows kagent's published harness contract and is not
endorsed by the kagent project. It runs on [PiG](https://github.com/MichaelKinsy/PiG), a Go port of
[Pi](https://github.com/earendil-works/pi) by Mario Zechner and contributors; no Pi or kagent code is copied here.

```sh
pig install ./components/kagent     # builds from source on first use (Go toolchain), or fuses into the Piglet Binary
```

The [`kagent` Piglet](../../piglets/kagent/README.md) fuses it into one Binary (about 45 ms to start); see there for the build command.

## Licences

| Path | Licence |
|---|---|
| this Package (`extensions/kagent-policy`, `README.md`, `provenance.json`) | MIT, [LICENSE](LICENSE) |
| `harness/` (the kagent-facing adapter, image, chart, installer; not part of the npm Package) | Apache-2.0, as the byo-ext harness tree it is exported to; the licence text is in that directory |

## Policy file

`$KAGENT_PIG_POLICY` names a JSON file. It is validated strictly, because it is compiled from a Kubernetes spec: an unknown
key (keys are case-sensitive: `REQUIRE_APPROVAL` is unknown, not an override), a duplicate key, a `null`, a wrong type or an out-of-range value is an error that names the field.

```json
{"mcp_servers":[{"name":"kagent-tools","url":"https://tools.kagent.svc.cluster.local/mcp",
  "headers":{"Authorization":"${KAGENT_PIG_HDR_1}"},"timeout_seconds":60,
  "tools":["kagent_probe"],"require_approval":true}],
 "ask_user":true}
```

| Field | Rule |
|---|---|
| `mcp_servers[].name` | 1 to 40 characters: letters and digits in words joined by single `-` or `_` (no `__`, no leading or trailing separator, so no policy server's `mcp__<server>__` prefix is the start of another's); at most 64 servers; names that differ only in `-` against `_` collide (PiG reads them as one). PiG cuts a tool name longer than 64 characters to 55 and a hash suffix, which would remove the end of the `mcp__<server>__` prefix of a name of 49 or more characters, so the limit is 40 |
| `url` | absolute `http` or `https`, a host, no credentials, no whitespace, **no `$`** (PiG expands environment variables in an MCP url) |
| `headers` | each value is exactly one `${VAR}` reference and `VAR` must be set in pig's environment. PiG expands `$VAR` anywhere in a header value and runs a value that starts with `!` as a shell command, so a literal from the policy is refused; the variable's content is delivered as it is. Values are never echoed in errors |
| `timeout_seconds` | greater than 0, at most 3600; default PiG's 60 |
| `tools` | allowlist of raw MCP tool names, at most 256, no `*` (PiG reads it as a wildcard); empty exposes every tool |
| `require_approval` | gates every call of every tool of the server (default false) |
| `ask_user` | registers the `ask_user` tool (default false) |

Exposure: an empty `tools` registers the server with exposure `direct`. A non-empty one registers it `hidden` with each
allowlisted tool `direct`, so a tool outside the list is `not found` and never reaches the server. Direct tools are
top-level, replayable calls, which is why a harness can park on them. Unknown allowlisted names are not an error (server
catalogs drift).

**Fail closed.** If `KAGENT_PIG_POLICY` is set and the file is missing, unreadable, larger than 1 MiB or invalid, building
the extension panics with `kagent-policy: refusing to start ungated: <reason>`; PiG then exits non-zero before it reads a
prompt (measured: PiG v0.7.0 fused Binary exits 1 and prints that line on stderr). With the variable unset the extension
registers nothing.

## Approval gate

For a `tool_call` of a tool named `mcp__<server>__...` of a server with `require_approval` (the server name with `-` read as
`_`, any tool name after the prefix), the extension calls `ctx.Confirm("kagent-approval", message)` with this compact JSON:

```json
{"v":1,"kind":"approval","toolCallId":"call_1","toolName":"mcp__kagent_tools__kagent_probe","input":{"note":"basket"}}
```

PiG writes it as `extension_ui_request{method:"confirm", title:"kagent-approval", message:<that text>}`. The client answers
`extension_ui_response{confirmed:true}` to allow. Anything else (false, `cancelled`, a string, a number, no answer) blocks the
call with `Tool call was rejected by user.`; the MCP server is never contacted. The gate keys on the tool name, so a call made
from inside a `codemode` script (the event carries `parentToolCallId`) is held like a direct one. A failing dialog is an error
of the handler, which PiG reads as a refusal. Other tools pass through untouched. Object keys of `input` are in sorted order.

**Default deny.** With a policy loaded, a call of any `mcp__*` tool whose name does not start with the prefix of a policy server is blocked with `MCP server is not part of this agent.` A server added through `mcp.json` or by another extension is therefore neither exposed nor ungated. An event without an `input` object is an error, never an approval of empty arguments.

With any approval server, PiG's `list_mcp_resources`, `list_mcp_resource_templates` and `read_mcp_resource` are refused too:
they reach the resources of every server and would read from a gated one without asking.

## ask_user

Registered only when the policy sets `ask_user`. PiG offers an extension tool to the model only if `--tools` names it (a harness adds `ask_user`, and one `mcp__<server>__*` entry per server: without an `mcp__` entry a non-empty `--tools` offers no MCP tool). Input: `questions` (1 to 8) of `{question (1 to 2000 characters),
choices (0 to 16 strings of 1 to 500 characters), multiple (boolean)}`. The extension validates the arguments itself, then
asks from its `tool_call` hook with `ctx.Confirm("kagent-ask-user", message)`, not from `execute`: PiG evaluates every hook of an assistant message before it runs any call of it, so a harness that parks on the dialog leaves nothing of the batch executed (a dialog in `execute`, or a sequential execution mode, would run the calls listed before `ask_user` first). The message is

```json
{"v":1,"kind":"ask_user","toolCallId":"call_2","toolName":"ask_user","input":{"questions":[{"question":"Which env?","choices":["dev","prod"],"multiple":false}]}}
```

A harness adapter that parks never answers. In a plain PiG run the hook records the `confirmed` value the client sent and
`execute` returns it: a string as it is, any other JSON value as compact JSON. For false, a cancelled dialog or an empty
answer the hook blocks the call and the model reads `The user declined to answer.` as the error result.

## Limits to know

- Header values reach pig as environment variables (`KAGENT_PIG_HDR_n`), and PiG's `bash` tool inherits them. A credential
  placeholder is inert, but a header built from a real secret is readable by the model through `bash`.
- `codemode` is not activated by servers registered `direct` or `hidden`; whether a script can reach a `hidden` tool when
  `codemode` is enabled by other means is not measured (nested calls are gated by name).
- A tool allowlist and the approval gate are model-facing filters, not network controls: PiG's `bash` can reach an origin
  the process can reach.
- A `.pig/mcp.json` in the working directory can configure a server of the same name; the gate still keys on the tool name
  prefix, but run the harness in a directory you control.
- Verified against PiG v0.7.0 with a hermetic mock model and a local MCP server only; no real model provider was used.

## Tests

`go test ./...` in `extensions/kagent-policy` runs the unit tests (fake PiG host). With `PIG_BIN` (and `PIG_SOURCE_ROOT`, a PiG
checkout at the same tag) it also builds the Piglet Binary from `dist/staged/piglets/kagent` (`npm run stage` first) and
drives a real `pig --mode rpc`; `KAGENT_PIG_BINARY` skips the build and `KAGENT_MOCK_MODEL_URL` uses the hermetic mock
model server in place of the in-process one.
