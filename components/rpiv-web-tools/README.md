# rpiv-web-tools (Go port)

A Go extension for PiG that gives the model the live web: `web_search`, which queries a search API and returns titled
results with URLs and snippets, `web_fetch`, which reads an http(s) page as text, and `/web-tools`, which picks the
backend and stores its key. Ten backends are supported: **Brave, Tavily, Serper, Exa, You.com, Jina, Firecrawl,
Perplexity, SearXNG and Ollama** (SearXNG and Ollama can be your own instance, so queries never leave your network).
It is a port of `@juicesharp/rpiv-web-tools` 2.12.0 by juicesharp (see [CREDITS.md](CREDITS.md)), made with the
[extension porting Skill](../extension-port/README.md). It needs no Node runtime, and **starts no process and makes no
network call until a tool runs**.

## Install

```sh
pig install ./components/rpiv-web-tools
```

or take the [`pig-essentials`](../../piglets/pig-essentials/README.md) Piglet, which selects it.

## Use

`web_search` needs a key. Run `/web-tools`, pick a provider and paste its key (SearXNG and Ollama ask for a base URL,
then an optional key), or export the provider's variable: `BRAVE_SEARCH_API_KEY`, `TAVILY_API_KEY`, `SERPER_API_KEY`,
`EXA_API_KEY`, `YOUCOM_API_KEY`, `JINA_API_KEY`, `FIRECRAWL_API_KEY`, `PERPLEXITY_API_KEY`, `SEARXNG_API_KEY` with
`SEARXNG_URL`, `OLLAMA_API_KEY` with `OLLAMA_HOST`. Keys are read from the environment and the config file, never from
anywhere else; `web_fetch` alone needs no key. `/web-tools --show` prints the resolved configuration with every key masked.

The active provider is `WEB_SEARCH_PROVIDER`, else the config's `provider`, else `brave`; a single `web_search` call
can name another with `provider`, which needs its own credentials (there is no silent fallback). Keys are stored per
provider in `~/.config/rpiv-web-tools/config.json` (`$XDG_CONFIG_HOME` when set) with mode `0600`, and an environment
variable wins over the file. The config also takes `baseUrls.<provider>`, `guidance.<tool>` (the prompt snippet and
guidelines the model sees) and `interceptors.github`. A malformed file reads as empty, and a wrong-typed field costs
that field alone.

`web_fetch` refuses anything but http and https, and a URL whose host is a private, loopback, link-local or metadata
address however it is written (`127.1`, `0x7f000001`, `[0::1]`, full-width digits: the host is read as Node's URL
parser reads it, and the request goes to that address). Like the original, it does not resolve host names first: a name
that resolves to a private address, `localhost.` with its trailing dot, an IPv4-mapped IPv6 address and a redirect to a
private address are fetched, up to 20 redirects are followed (as Node does), and the body is read whole before it is truncated. A provider
with its own fetch (Tavily, Exa, You.com, Jina, Firecrawl, Ollama) uses it; otherwise the page is fetched directly,
HTML is turned into text and large bodies are truncated to 2000 lines or 50 KB, the full text being written to a temp
file whose path is in the result.

### GitHub URLs (opt-in)

With `"interceptors": { "github": true }` (or an object with `maxRepoSizeMB`, `cloneTimeoutSeconds` and `clonePath`, which
implies the opt-in; `"enabled": false` inside it turns it off), `web_fetch` answers a `github.com` repository root,
`/tree/<ref>/...` or `/blob/<ref>/...` URL itself instead of fetching the page. It reads the repository size with
`gh api repos/<owner>/<repo>`; a repository over `maxRepoSizeMB` (350), and any URL naming a commit SHA, get an API view
(tree, README or file, through `gh api`); anything else is shallow-cloned (`gh repo clone`, else `git clone --depth 1`,
into `clonePath`, default `<tmp>/pi-github-repos`, with `cloneTimeoutSeconds`, 30) and shown as a tree and README, a
directory listing, or the file (binary files and files over 100K characters are summarised or cut). A clone is kept
for the session and reused. When nothing works the URL is fetched like any other page. Issues, pull requests and the
other non-code pages of a repository are never intercepted. `GITHUB_TOKEN` is the token `gh` reads; `/web-tools --show`
prints it masked with the active limits. The interceptor is built when the extension loads, so a change to the
option takes effect on the next session.

## Differences from the original

- **The GitHub interceptor runs `gh` and `git` through the host.** The original starts them with Node's `execFile`; this
  port asks the host to run them (`Context.Exec`, no shell), so a command that cannot start, a non-zero exit, a
  timeout, an output past Node's buffer limit and a cancelled request are all the same failure, as they are for
  `execFile`. Smaller differences: the one-time "install `gh`" hint goes to the extension's stderr; the original's
  programmatic consumer opt-in (`registerWebTools(pi, { interceptors: { github: true } })`) has no counterpart in the Pi
  package manifest, so only the user's config switches the interceptor on; and a URL of a scheme other than http(s)
  never reaches the interceptor (the tool refuses it first). See [port/PORT.md](port/PORT.md).
- **Renderers return lines** (the Go SDK cannot return a TUI component), laid out at the width the host gives.
- **Network failures read as in Node.** A transport failure reads `fetch failed`, as Node's fetch reports it.

The proof that it matches the original is in [port/PORT.md](port/PORT.md).
