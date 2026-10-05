# ollama-native Package

A PiG model provider for [Ollama](https://ollama.com), through Ollama's **native**
`/api/chat` endpoint (not the OpenAI-compatible `/v1` one), so tool calls, thinking
and images use Ollama's own message format. Models appear as `ollama-native/<model>`.
Go extension on PiG's public SDK; PiG builds it from source with a Go toolchain, and a
Piglet Binary can fuse it. It was started by Peder Munksgaard; see [CREDITS.md](CREDITS.md).

**It does nothing until you use it.** It is not part of any Piglet. It makes no network
call at start-up, so pig starts the same with Ollama stopped. It asks Ollama for
anything only when PiG refreshes provider catalogs, when you run `/ollama refresh`, or
when you send a message to an `ollama-native` model.

## Set up

1. Install and start Ollama, and pull a model that supports tools:
   `ollama pull granite4.1:3b` (or `qwen3:8b`, `llama3.2`, and so on).
2. Install the Package from the Pigpen checkout root:

   ```sh
   pig package validate ./components/ollama-native
   pig install ./components/ollama-native
   ```

   The local install references this checkout; keep it where it is and remove it with
   `pig remove` using the same path. To try it without installing, load the source
   directly: `pig -e ./components/ollama-native/extensions/ollama-native`.
3. Load the model list once. Start `pig` interactively (PiG refreshes provider
   catalogs when a session starts) or run `/ollama refresh`. The list is stored, so
   later runs, including one-shot `pig -p`, find the models with Ollama not asked:

   ```sh
   pig --list-models ollama
   pig --model ollama-native/granite4.1:3b -p "Say hello"
   ```

Run `/ollama refresh` again after `ollama pull` or `ollama rm`.

## Configuration

| Variable | Meaning | Default |
| --- | --- | --- |
| `OLLAMA_HOST` | Where Ollama listens: `host`, `host:port`, `:port` or a full `http(s)://` URL, as Ollama's own tools read it. | `http://127.0.0.1:11434` |
| `OLLAMA_API_KEY` | Sent as `Authorization: Bearer` to a protected server (a proxy, Ollama's hosted service). A local Ollama needs none, and no key is ever sent unless this is set. | unset |

`PI_OFFLINE=1` (or `--offline`) keeps PiG from refreshing catalogs on its own.

## What it does

- **Models** come from `/api/tags`; none is written down in the code. For each model
  `/api/show` gives the context length and capabilities: `thinking` marks a reasoning
  model, `vision` adds image input, and a model without `completion` (an embedding
  model) is left out. When `/api/show` fails the model is still listed, with a 32,768
  token context window.
- **Chat** streams `/api/chat`: text, thinking, and any number of tool calls per answer.
  Ollama sends no tool-call ids, so each call gets one. Tool results go back as `tool`
  messages. Token counts come from Ollama's final message. Cancelling a turn closes the
  request.
- **Errors** say what to do: Ollama not reachable (start it with `ollama serve` or set
  `OLLAMA_HOST`), a model that is not installed (`ollama pull <model>`), or the message
  Ollama returned, including one that arrives in the middle of a stream.
- `think` is sent only to models marked as reasoning, and only when a thinking level
  is chosen.

## Limits

- Tool calls need a model that supports them. Ollama says so in the error when it does not.
- PiG 0.4.0 gives a provider the system prompt's named sections as an unordered map,
  so they are sent in name order, not PiG's authored order. The text is all there.
- The catalog's `maxTokens` is 8,192 (or the context window if smaller); PiG sends a
  limit only when you set one.
- Only chat is covered: no embeddings, no model management (pull, delete).

## Tests

`extensions/ollama-native/testdata/` holds the Ollama responses the tests replay from a
local HTTP server: `/api/tags`, `/api/show` and the `/api/chat` streams (text,
thinking, tool calls, length stop, a mid-stream error, a truncated body, a 404). They
are written to the wire format in Ollama's API documentation, not recorded from a live
server. `go test ./...` in `extensions/ollama-native` runs them; nothing needs Ollama.
A run against a real Ollama is the owner's check; it has not been done here.
