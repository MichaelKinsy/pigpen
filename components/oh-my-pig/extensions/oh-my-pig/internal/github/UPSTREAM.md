# Upstream: `internal/github`

Ported from omp **`v19.1.1`** (`31ffee513f`).

## Upstream files followed

| Upstream path | Role |
|---|---|
| `packages/coding-agent/src/tools/gh.ts` | `GithubTool`, `githubSchema`, `file_read`, op dispatch |
| `packages/coding-agent/src/tools/gh-common.ts` | Repo ref parsing, URL parsing, `resolveGitHubRepo`, `unrelatedOrigin`, `normalizeBlock` (`sanitizeText`), formatting helpers |
| `packages/coding-agent/src/tools/gh-types.ts` | JSON response and view data types |
| `packages/coding-agent/src/tools/gh-view.ts` | `repo_view`, `getOrFetchIssue`, `getOrFetchPr`, `stateReason` fallback, markdown formatters |
| `packages/coding-agent/src/tools/gh-pr-diff.ts` | `parsePrUnifiedDiff`, per-file API fallback on HTTP 406, `getOrFetchPrDiff`, `retainPrDiffGeneration`, `getPinnedPrDiff` |
| `packages/coding-agent/src/tools/gh-pr-checkout.ts` | `pr_create`, `pr_checkout` (dedicated worktrees), `pr_push` (`--no-follow-tags`, cache invalidation) |
| `packages/coding-agent/src/tools/gh-search.ts` | `search_issues`, `search_prs`, `search_code`, `search_commits`, `search_repos`, date qualifier parsing |
| `packages/coding-agent/src/tools/gh-run-watch.ts` | `run_watch` (run ID and commit modes), watching snapshots, grace window notes, failed job log capture |
| `packages/coding-agent/src/tools/glab-mr.ts` | `getOrFetchMrDiff`, `--raw` fallback, env-token host trust check |
| `packages/coding-agent/src/tools/github-cache.ts` | Soft/hard TTL cache, open-state 15s soft TTL cap, background refresh for diffs, pinned generations, auth-key memo |
| `packages/coding-agent/src/utils/{forge-cli,github,gitlab}.ts` | `FORGE_NON_INTERACTIVE_ENV`, 8 MiB capture cap with marker, detached `CwdLookupMemo`, error formatting |
| `packages/coding-agent/src/worktree/paths.ts` | `resolveAvailableWorktreePath` (`<base>`, `<base>-2` … `-100`) |
| `packages/coding-agent/src/internal-urls/{issue-pr-protocol,mr-protocol,forge-diff}.ts` | `issue://`, `pr://`, `mr://` URL handlers |
| `packages/coding-agent/src/prompts/tools/github.md` | Embedded `github.md` |
| `packages/coding-agent/src/prompts/internal-urls/{issue,issue-reference,mr}.md` | Embedded prompt docs |
| `packages/coding-agent/src/tools/settings.ts` | `github.enabled`, `github.cache.{enabled,softTtlSec,hardTtlSec}` |
| `packages/tui/src/tools/{github,gh-format,output-meta}.ts` | `GhToolDetails` (`meta.source`, `watch`, `checkouts`), `formatShortSha`, `pushLine`, run-watch markdown sections |
| `crates/vcs/src/git/cli.rs` | Hardened git env (`LC_MESSAGES=C`, stripped `GIT_*`, askpass), `setsid`, 30m network timeout, `--no-follow-tags` |

## Tool `loadMode` at tag

- `github`: `loadMode = "discoverable"` (default off via `github.enabled: false`; registered with `Exposure: sdk.ToolExposureCodemode`, `DefaultActive: false`, and `toolcmd.Mountable("github", ...)` checking `Enabled(ctx, "github.enabled", false) && ghAvailable()`; `internal/toolcmd` owns mounting and top-level activation when bash transport is off).
- `issue://`, `pr://`, `mr://`: always registered with `internal/urls`; `PromptDoc` / `PromptReference` return empty when `gh` (or `glab` for `mr://`) is not on `PATH`.
- Mutating ops (`pr_create`, `pr_checkout`, `pr_push`) call `gate.Check(ctx, gate.Request{Tool: "github", Op: gate.OpExec, Detail: op})` before mutating.

## Settings

| Key | Default | Honoured |
|---|---|---|
| `github.enabled` | `false` | Yes, via `toolcmd.Mountable` predicate (`Enabled(ctx, "github.enabled", false) && ghAvailable()`) |
| `github.cache.enabled` | `true` | Yes, via `CacheSettingsFn` (reads `Enabled(ctx, "github.cache.enabled", true)`) |
| `github.cache.softTtlSec` | `300` | Default constant `300s` until settings lane replaces `CacheSettingsFn` (open issues/PRs capped at 15s) |
| `github.cache.hardTtlSec` | `604800` | Default constant `7d` until settings lane replaces `CacheSettingsFn` |

## Tests ported

15 Go test functions covering schema/description, error formatting (5 upstream cases), UTF-8 locale pinning, 8 MiB forge stdout capture cap (3 upstream cases), `repo_view` + `file_read` (text, 0-byte empty file, directory array, no-bytes `formatBytes`, PNG image with dimensions, enterprise `--hostname`, error context), all 5 `search_*` ops + date qualifiers + verbatim leading-dash queries, cross-repo & batch `pr_checkout` + `resolveAvailableWorktreePath` + `gate.Check` + `pr_push` (`--no-follow-tags`), `run_watch` (watching snapshots, steps, ANSI sanitization, issue #1949 / PR #1951 repo safety), PR diff 406 fallback + quoted header parser, cache semantics (auth keys, open-state 15s cap, abort propagation, invalidation fencing, kill switch, pinned retention from `retainedAt`), `issue://` & `pr://` handlers, `mr://` handler, mounted `toolcmd` execution via `Context.ExecuteTool` + multi-session `lifecycle.End` (verifying `context.Canceled`, not timeout) + double `Register`, no per-area `session_shutdown` hook + hardened git read/write args, and positional leading-`-` + untrusted GitHub host refusal.

## Not ported

| Feature | Reason |
|---|---|
| SQLite storage backend (`bun:sqlite` WAL + `busy_timeout`) for `github-cache.db` | No third-party Go modules allowed (`go.mod` stays stdlib + SDK only). Replaced with a JSON file (`<configHome>/cache/github-cache.json`, override via `OH_MY_PIG_GITHUB_CACHE`, never touching `OMP_GITHUB_CACHE_DB`) written atomically via unique temp files and reloaded on stat mtime/size change; still whole-file rewrite rather than row-level SQLite concurrency. |
| `run_watch` full-log session artifact saving (`allocateOutputArtifact` / `artifact://`) | No shared artifact allocator contract exists in `internal/` yet; `run_watch` returns the failed-job log tail inline and populates `Details.Watch.FailedLogs` (`ArtifactID` stays empty). Note: embedded `github.md` keeps omp's verbatim text mentioning artifact saving. |
| `ToolResult.useless` compaction hint on empty `search_*` and no-runs `run_watch` | PiG `sdk.ToolResult` has no `Useless` field. |
| Per-op approval tier (`GITHUB_READONLY_OPS` -> `"read"`, else `"exec"`) | PiG `sdk.ToolDefinition` has no per-call approval callback; plan-mode restriction is enforced instead via `gate.Check(..., gate.OpExec)` on `pr_create`, `pr_checkout`, `pr_push`. |
| `file_read` automatic image resizing / WebP exclusion (`cfgImagesAutoResize`, `webpExclusionForModel`) | Depends on model capability inspection; raw decoded image bytes, MIME type, and parsed `Dimensions: WxH` are returned. |
| `bash` command-hook cache invalidation (`gh-cache-invalidation.ts`) | Lives in `internal/shell`; `InvalidateAllForNumber` is exported for wiring if desired. |
| `pr_checkout` CoW/reflink clone isolation backend (`cfgWorktreeClone` / `cfgIsolationBackend`) and `~/.omp/wt` root | Uses standard `git worktree add` under `<configHome>/worktrees` (fallback `<tmpdir>/oh-my-pig-worktrees`), with `resolveAvailableWorktreePath`; worktrees are not auto-pruned. |
| Flow-style YAML in `glab` `config.yml` `hosts:` parser | Stdlib-only parser handles standard block-style `hosts:` maps and fails closed on flow-style YAML. |

## Changed on purpose

| Change | Reason |
|---|---|
| Host-trust guard on GitHub CLI (`gh`) targets (`repo`, `issue://<host>/...`, `pr://<host>/...`, `--hostname`) | `gh help environment` documents that `GH_ENTERPRISE_TOKEN` / `GITHUB_ENTERPRISE_TOKEN` are sent whenever `gh` targets any GitHub Enterprise Server host, and `GH_TOKEN` / `GITHUB_TOKEN` are sent to `github.com` and any `*.ghe.com` subdomain. omp guards only `glab`; this port restricts explicit `gh` hosts to `github.com`, `GH_HOST` when set, and hosts already present in `gh`'s `hosts.yml` (cached per session/mtime), refusing untrusted hosts with an explicit error before spawning `gh`. |
| Leading-`-` guard on model-supplied `repo` parameter (`repo_view`, `file_read`, `search_*`, `pr_create`, `pr_checkout`, `run_watch`) | Prevents flag injection when `repo` is passed as a positional argument (`gh repo view <repo>`) or `--repo`. |
