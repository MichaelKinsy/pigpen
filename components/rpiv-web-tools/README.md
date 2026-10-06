# rpiv-web-tools (Go port)

A Go extension for PiG: `web_search` and `web_fetch` for the model with pluggable providers (Brave, Tavily, Serper,
Exa, You.com, Jina, Firecrawl, Perplexity, SearXNG, Ollama), plus the `/web-tools` command.

It is a port of `@juicesharp/rpiv-web-tools` 2.12.0 (see [CREDITS.md](CREDITS.md)), made with the
[extension porting Skill](../extension-port/README.md). It needs no Node runtime.

## Status: slice 1 of 6

The port is in progress. Slice 1 — the typed config reader and writer for
`~/.config/rpiv-web-tools/config.json` — is ported and twinned. The tools, the provider contract, the GitHub
interceptor chain and the command are not ported yet.

The per-slice scope, the twin coverage and the named gaps are in [port/PORT.md](port/PORT.md);
`pigeq twins check --ledger port/upstream-tests.json --go extensions/rpiv-web-tools` is the judge, and it currently
reports **20 exact twins and 1 named skip** for the `providers/config` file.

## What slice 1 gives you

- A fail-soft config read: a missing file, malformed JSON, a directory and a literal `null` all become the empty
  config instead of an error.
- Per-field schema salvage: one wrong-typed leaf costs that field alone, and a bad array element drops the whole
  array field rather than leaving a sparse hole.
- Pass-through of every unknown key at every level, which is what the released `/web-tools` legacy-`apiKey`
  migration depends on.

## Use (once the tools land)

```sh
pig install ./components/rpiv-web-tools
```
