# rpiv-web-tools (Go port)

A Go extension for PiG: `web_search` and `web_fetch` for the model with pluggable providers (Brave, Tavily, Serper,
Exa, You.com, Jina, Firecrawl, Perplexity, SearXNG, Ollama), plus the `/web-tools` command.

It is a port of `@juicesharp/rpiv-web-tools` 2.12.0 (see [CREDITS.md](CREDITS.md)), made with the
[extension porting Skill](../extension-port/README.md). It needs no Node runtime.

## Status: ported

The port is complete: every one of the 274 upstream test titles is claimed by an exact twin or a named skip, and
`pigeq twins check` reports no missing case. What the port does not yet carry is the PiG registration layer — the two
tools and the command are registered with a host this port does not depend on yet.

The per-slice scope, the twin coverage and the named gaps are in [port/PORT.md](port/PORT.md);
`pigeq twins check --ledger port/upstream-tests.json --go extensions/rpiv-web-tools` is the judge, and it currently
reports **272 exact twins and 2 named skips** of the 274 upstream titles.

## What the port gives you

- A fail-soft config read: a missing file, malformed JSON, a directory and a literal `null` all become the empty
  config instead of an error.
- Per-field schema salvage: one wrong-typed leaf costs that field alone, and a bad array element drops the whole
  array field rather than leaving a sparse hole.
- Pass-through of every unknown key at every level, which is what the released `/web-tools` legacy-`apiKey`
  migration depends on.
- The provider table: ten providers in declaration order, their roles, env vars and self-hosted URL defaults, plus
  the three-tier credential lookup (env var, `apiKeys[provider]`, and the legacy top-level `apiKey` for brave alone).

## Use

```sh
pig install ./components/rpiv-web-tools
```
