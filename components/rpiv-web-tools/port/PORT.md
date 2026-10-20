# Port record: @juicesharp/rpiv-web-tools

| Input | Identity |
|---|---|
| Original | `@juicesharp/rpiv-web-tools` 2.12.0 by juicesharp, https://github.com/juicesharp/rpiv-mono (`packages/rpiv-web-tools`), commit `7c9bc924c5bfd148f36d7ebc9f7bd0a9469d633f`, MIT |
| Oracle | Pi 1.3.0 (build `1402973a`), Node 24.19.0, with the unmodified package at `port/oracle` (its dependency `@juicesharp/rpiv-config` from npm) |
| Target | PiG 0.4.1+1.0.3 (release v0.4.1, commit `3ee745c8cda3c1a9a8d71112c140790c4d64d78d`), go1.27.1 |
| Original's tests | the `it` cases in the vitest files listed in `port/upstream-tests.json`: **COUNTS** |
| Kind | 1: an extension, no CLI built-in, no provider |

## Mapping

| ID | Original | Go | Scenario / test |
|---|---|---|---|
| M1 | `index.ts`: `web_search` and `web_fetch` with schemas, descriptions, `promptGuidelines`, the config `guidance` override | `webtools.go`, `config.go` | `tool-definitions`, `guidance-override`; `tools_test.go`, `guidance_render_test.go` |
| M2 | `providers/*.ts`: ten search backends (Brave, Tavily, Serper, Exa, You.com, Jina, Firecrawl, Perplexity, SearXNG, Ollama), their key and base-URL resolution, request bodies, result shaping, error texts, the 20-redirect limit of `fetch` | `providers.go` | `search-without-key`, `search-config-key-errors`, `searxng-search`, `ollama-search`, `unknown-env-provider`, `fetch-redirect-limit`; `tools_test.go`, `extra_test.go`, `index_twin_test.go`, `index_command_twin_test.go`, `redirect_test.go` |
| M3 | `web_fetch`: URL and private-address guard, provider-native fetch, direct fetch, content-type refusal, HTML to text, truncation and spill file | `fetch.go`, `htmltext.go`, `text.go`, `util.go` | `fetch-url-guard`, `fetch-keyed-provider-without-key`; `differential_test.go` (against the original's `htmlToText` and `truncateHead`, `testdata/differential.json` made by `port/gen-differential.mjs`), `htmltext_reference_test.go` |
| M3a | Node's `new URL()` host parsing, which the guard (and the SearXNG and Ollama base URLs) rely on: domain to ASCII, the IPv4 number forms, IPv6 serialization | `util.go` (`parseJSURL`, `whatwgHost`; `golang.org/x/net/idna`) | `hostparse_test.go`: 35 hosts whose expected `hostname` was taken from Node 24.19.0, the guard on every private form, the request going to the parsed URL |
| M4 | config file read and write (`~/.config/rpiv-web-tools/config.json`, XDG, legacy path, mode 0600), per-field validation | `config.go` | `config_test.go`, `extra_test.go` |
| M5 | `/web-tools` (picker, per-provider flows, `--show`) | `command.go` | `command-cancelled`, `command-pick-key`, `command-searxng`, `command-show`, `command-show-configured`; `command_test.go` |
| M6 | tool call and result renderers | `render.go` | layer-1 render tests |
| M7 | registration, session wiring | `extension.go` | all scenarios |
| M8 | `providers/interceptors/github.ts`: `parseGitHubUrl`, `resolveGitHubOptions`, `GitHubInterceptor` (size probe, shallow clone, clone cache, API view, local tree, README and file rendering, binary and size limits) | `github_interceptor.go`, `jscompat.go`, `hostexec.go` | `github-api-view`, `github-clone-gh`, `github-clone-git`; `github_interceptor_test.go` (78 twins), `github_interceptor_extra_test.go`, `github_host_test.go` |
| M9 | `providers/interceptors/index.ts`, `types.ts`, the chain in `web-tools.ts:441-459` and the `--show` block `web-tools.ts:561-572` | `interceptors.go`, `webtools.go`, `command.go` | `command-show-github`; `interceptor_chain_test.go` (6 twins), `github_host_test.go` |

## Results (this revision)

- `pigeq check` against goldens **recorded from the original under Pi 1.3.0 (1402973a)**: NSCEN of NSCEN scenarios, plus `port-gaps`
  and `exec-coverage`; the record-host check passes. The scenarios put the API config into the lane HOME with `setup`.
  The GitHub scenarios declare `gh` and `git` as canned; the others declare both missing.
- `go test -race ./...`: pass.
- `pigeq mutate --unit`: 125 mutations (see below).
- `pig package validate components/rpiv-web-tools` and `pig install <extension dir> --validate-only --json`: valid.

## Mutations

`port/gen-mutations.py` writes `port/mutations.json` (every `find` string is unique and each mutant compiles). The first
run killed 71 of 98 and 27 survived or did not build; each survivor got a test (`extra_test.go`, `command_test.go`),
the invalid mutation was rewritten, and `config-mode` was removed as an equivalent mutant (the `chmod` that follows
gives the same mode). The final run: **97 mutations, all killed** (90 by the unit tests alone, 7 only by the scenarios:
`fetch-failed-text`, `command-empty-key-saved`, `show-legacy-key`, `show-url-source`, `show-interceptor-hint`,
`tool-description`, `schema-provider-description`). After the review fix (finding 8), with ten mutants of the host
parser and of the request going to the parsed URL: **107 mutations, all killed** (100 by the unit tests, the same 7 by
the scenarios, re-run one at a time).

## Deliberate differences and findings

1. **The GitHub interceptor (opt-in) is ported (slice 3).** `github_interceptor.go` is `providers/interceptors/github.ts`
   (parse, options, probes, clone cache, API view, local rendering), `interceptors.go` is `index.ts` and `types.ts`
   (`buildInterceptors`, `getInterceptors`, `getActiveGitHubInterceptor`), `hostexec.go` is the process seam, and
   `jscompat.go` holds the Node behaviours the output depends on. The 78 cases of `github.test.ts` and the 6 of
   `chain.test.ts` are twins under the upstream titles (`github_interceptor_test.go`, `interceptor_chain_test.go`), written
   first and shown failing in a commit of their own. Differences, each with its test:
   - **Commands run through the host.** The original starts `gh` and `git` with `execFile`; here `Context.Exec` runs them
     (no shell; the request's cancellation aborts them). A command that cannot start, a non-zero exit, a timeout, an
     output past `maxBuffer` (Node's 1 MiB default, 5 MiB for the tree, 2 MiB for a file) and the host's own error are
     one failure, as `err` is for `execFile` (`github_host_test.go`). The twins replace the original's `execFile` mock with a
     canned runner carried in the tool's context (`withExec`), so nothing is global.
   - **The API view reads the tree, then the README.** The original starts both at once (`Promise.all`); two concurrent
     host calls would reach the host in no fixed order, and the harness compares the command sequence, so they run in the
     original's start order. It costs one `gh api` round trip on that path only.
   - **JavaScript string and Node buffer semantics are reproduced where output depends on them** (`jscompat.go`,
     `TestJSCompat`, expected values from Node 24.19): lengths and cuts in UTF-16 units (a README of 4500 emoji is cut at
     8192 units), `Buffer.from(x, "base64")` leniency, WHATWG UTF-8 decoding, `decodeURIComponent` failing on bad
     escapes, `path.extname`, `toFixed(1)` rounding a tie up, `Math.round`, `parseInt`, and the default sort by UTF-16 unit.
     The path of a URL is read as `new URL` reads it (backslashes, `%2e` dot segments, tabs and newlines;
     `TestGitHubURLNormalization`).
   - **The install hint** (`console.error` in the original) is written to the extension's stderr, once per interceptor.
   - **The consumer default** (`registerWebTools(pi, { interceptors: { github: true } })`) is reachable only through
     `buildInterceptors(consumer)`; the Pi package manifest cannot pass options, so the extension passes none. The twins of
     `chain.test.ts` use it.
   - **A URL of a scheme other than http(s)** is not parsed by `parseGitHubURL` (the original would read `ftp://github.com/...`
     as a repository); the tool refuses such a URL before the chain runs, so the tool never sees the difference.
   - **A probe that failed only because the request was cancelled is not remembered**, where the original's probe was never
     cancelled; and a waiting request returns when it is cancelled instead of when the other clone ends (both null).
   - **No real git.** The scenarios cannot put a local repository behind `url.<base>.insteadOf` (the harness fixes
     `GIT_CONFIG_GLOBAL`, `GIT_CONFIG_NOSYSTEM` and `GIT_CONFIG_COUNT`, and `git clone` reads no repository-local config), so
     `gh` and `git` are canned in the scenarios and a populated clone is rendered only by the twins and the layer-1 tests
     (`github_interceptor_extra_test.go`: exact texts, UTF-16 cuts, sorting, symlinks, listings).
2. **Order of work.** Slice 1 wrote the implementation before the twins, so there is no red commit for it. Slice 2 (the
   `index.test.ts` twins and the redirect limit) has one: the twins commit fails only on the redirect limit, the next
   commit makes it pass. Slice 3 (the interceptor and its chain) has one as well: the twins of `github.test.ts` and
   `chain.test.ts` with compilable stubs, shown failing, then the implementation.
3. **The 147 `index.test.ts` cases are twinned one by one** (`index_twin_test.go`, `index_command_twin_test.go`, and the
   cases of slice 1 in `tools_test.go` and `command_test.go`), with these points:
   - the original's consumer opt-in (`registerWebTools(pi, { interceptors: { github: true } })`) is reachable only through
     `buildInterceptors(consumer)` (item 1); the twin of "user config wins" uses the user's `false`;
   - "--show lists 'github: enabled' with token + clonePath when opted in" is twinned against the `--show` block of
     `command.go` (`command-show-github` runs it under Pi as well);
   - the class-level cases (`SearxngProvider`, `configureSearxng`, `SEARXNG_PROVIDER_META`) run against `newSearxng`,
     `configureSearxng` and the provider table.
4. **PiG and Pi word the host's schema-validation error for an unknown `provider` literal differently**; the scenario
   leaves that case out and the Go side rejects it with the original's own message.
5. **Node's `fetch failed` text** is reproduced for transport failures (`fetchFailed`).
6. **Renderers return lines**: the Go SDK cannot return a TUI component.
7. No network call and no process at startup; keys are read from the environment and the config file only.
8. **Review fix (rev-pig-essentials): the private-address guard was weaker than the original's.** The first version read
   the host with Go's `url.Parse`, which leaves `127.1`, `2130706433`, `0x7f000001`, `0177.0.0.1`, `[0::1]`,
   `[0:0:0:0:0:0:0:1]` and full-width `１２７．０．０．１` as written; the guard let them through and Go's resolver (or its
   IDNA mapping) then connected to the loopback address. Node's URL parser turns every one of them into `127.0.0.1` or
   `[::1]`, which the original refuses. The host is now parsed as the URL Standard parses it, and the built-in fetch
   requests that parsed URL. `hostparse_test.go` holds the cases.
9. **Inherited, not added: what the guard does not cover.** As in the original, the guard reads the URL only. A host name
   that resolves to a private address (`127.0.0.1.nip.io`), `localhost.` with a trailing dot, an IPv4-mapped IPv6
   address (`[::ffff:7f00:1]`) and a redirect to a private address are fetched; the body is read whole (`res.text()`)
   before truncation, with no size cap. The README says so.
10. **Redirects: 20, as in the original.** Node's `fetch` (undici) follows 20 redirects and fails on the 21st with
    `fetch failed`; Go's default client stopped at the tenth. `httpClient` now allows 20 (`maxRedirects`,
    `redirect_test.go`), and the scenario `fetch-redirect-limit` runs an Ollama search through 20 redirects (answered)
    and 21 (`fetch failed`) against the original under Pi.
11. **HTML to text is a set of linear scans, not regular expressions.** The first port ran the original's expressions
    (`fetch-helpers.ts:75-82`) with Go's `regexp`, 2.5 ms on a 20 KB page against 0.4 ms in Node, 680 KB allocated. The
    scans of `htmltext.go` replace exactly what each expression matched, pass for pass (0.4 ms, 190 KB for the whole
    `web_fetch`). `htmltext_reference_test.go` keeps the expressions and compares both on 20000 generated pages; the
    differential fixture (now 37 pages) compares with the original. One deliberate change: names match in ASCII case
    only, as JavaScript's `i` flag does, where Go's `(?i)` also folded U+017F onto `s` and U+212A onto `k`
    (the original keeps `<\u017fcript>` as a tag; the fixture has both cases).
