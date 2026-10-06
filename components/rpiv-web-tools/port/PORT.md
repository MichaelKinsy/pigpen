# Port record: @juicesharp/rpiv-web-tools

| Input | Identity |
|---|---|
| Original | `@juicesharp/rpiv-web-tools` 2.12.0 by juicesharp, https://github.com/juicesharp/rpiv-mono (`packages/rpiv-web-tools`), commit `7c9bc924c5bfd148f36d7ebc9f7bd0a9469d633f`, MIT |
| Oracle | Pi 1.0.3, Node 24.19.0, with the unmodified package at `port/oracle` (its dependency `@juicesharp/rpiv-config` from npm) |
| Target | PiG 0.4.1+1.0.3 (release v0.4.1, commit `3ee745c8cda3c1a9a8d71112c140790c4d64d78d`), go1.27.1 |
| Original's tests | the `it` cases in the vitest files listed in `port/upstream-tests.json`: **82 exact twins, 2 named skips, 190 deferred by name** (`index` 106, GitHub interceptor 78, interceptor chain 6; `port/slices.json`) |
| Kind | 1: an extension, no CLI built-in, no provider |

## Mapping

| ID | Original | Go | Scenario / test |
|---|---|---|---|
| M1 | `index.ts`: `web_search` and `web_fetch` with schemas, descriptions, `promptGuidelines`, the config `guidance` override | `webtools.go`, `config.go` | `tool-definitions`, `guidance-override`; `tools_test.go`, `guidance_render_test.go` |
| M2 | `providers/*.ts`: ten search backends (Brave, Tavily, Serper, Exa, You.com, Jina, Firecrawl, Perplexity, SearXNG, Ollama), their key and base-URL resolution, request bodies, result shaping, error texts | `providers.go` | `search-without-key`, `search-config-key-errors`, `searxng-search`, `ollama-search`, `unknown-env-provider`; `tools_test.go`, `extra_test.go` |
| M3 | `web_fetch`: URL and private-address guard, provider-native fetch, direct fetch, content-type refusal, HTML to text, truncation and spill file | `fetch.go`, `text.go`, `util.go` | `fetch-url-guard`, `fetch-keyed-provider-without-key`; `differential_test.go` (against the original's `htmlToText` and `truncateHead`, `testdata/differential.json` made by `port/gen-differential.mjs`) |
| M3a | Node's `new URL()` host parsing, which the guard (and the SearXNG and Ollama base URLs) rely on: domain to ASCII, the IPv4 number forms, IPv6 serialization | `util.go` (`parseJSURL`, `whatwgHost`; `golang.org/x/net/idna`) | `hostparse_test.go`: 35 hosts whose expected `hostname` was taken from Node 24.19.0, the guard on every private form, the request going to the parsed URL |
| M4 | config file read and write (`~/.config/rpiv-web-tools/config.json`, XDG, legacy path, mode 0600), per-field validation | `config.go` | `config_test.go`, `extra_test.go` |
| M5 | `/web-tools` (picker, per-provider flows, `--show`) | `command.go` | `command-cancelled`, `command-pick-key`, `command-searxng`, `command-show`, `command-show-configured`; `command_test.go` |
| M6 | tool call and result renderers | `render.go` | layer-1 render tests |
| M7 | registration, session wiring | `extension.go` | all scenarios |

## Results (this revision)

- `pigeq check` against goldens **recorded from the original under Pi 1.0.3**: 14 of 14 scenarios, plus `port-gaps`
  and `exec-coverage`; the record-host check passes. The scenarios put the API config into the lane HOME with `setup`
  and declare `gh` and `git` as missing.
- `go test -race ./...`: pass.
- `pigeq mutate --unit`: 107 mutations (see below).
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

1. **The GitHub interceptor (890 lines, opt-in) is not ported.** It answers `github.com` URLs from a cached shallow
   clone made with `gh` or `git`. Slice 2: the 84 interceptor cases are deferred by name. With the option on, such a URL
   is fetched like any page and `/web-tools --show` says so.
2. **Order of work.** Unlike the tintinweb-tasks port, the implementation was written before the twins, so there is no
   red commit. The substitute proof is the 14 Pi-recorded scenarios, the differential test and the mutation run.
3. **106 cases of `index.test.ts` are deferred** (the per-provider matrix, the `--show` variants and so on). Their
   behavior is covered by this port's own layer-1 tests and the scenarios, not twinned one by one.
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
   address (`[::ffff:7f00:1]`) and a redirect to a private address are fetched; redirects are followed (Go's limit of
   10, Node's 20); the body is read whole (`res.text()`) before truncation, with no size cap. The README says so.
