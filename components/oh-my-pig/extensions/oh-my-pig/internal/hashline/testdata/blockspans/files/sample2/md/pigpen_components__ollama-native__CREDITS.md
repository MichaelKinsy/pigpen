# Credits

The idea, the first working extension and the choice of Ollama's native `/api/chat`
endpoint (with the model list read from `/api/tags`) are by **Peder Munksgaard**
(<peder1981@gmail.com>, GitHub @peder1981), contributed as pigpen pull request 3,
"feat: add ollama-native extension with dynamic model listing". MIT.

This Package is a rewrite of that extension for Pigpen's rules, and keeps its
design and its `ollama-native/<model>` naming. What changed, and why:

- **No network at startup.** The first version asked Ollama for its models while
  the extension was being constructed, so a pig start waited for (or failed on) a
  server that might not be running. Here the catalog is read when PiG refreshes
  provider catalogs, stored, and restored from the store at the next start.
- **More than the first tool call.** Ollama sends no tool-call ids and may send
  several calls in one answer; every call is kept and gets an id.
- **No 120-second cap on a whole request.** A cold model or a long answer outlasts
  it; only connecting is bounded. Cancelling a turn cancels the request.
- **The whole conversation goes to the model**: the system prompt, earlier tool
  calls and their results, and images.
- **Context window, thinking and image support** come from `/api/show` instead of
  fixed numbers.
- Tests with recorded-format fixtures, a README, and a `go.mod` pinned like the
  other Pigpen modules.
