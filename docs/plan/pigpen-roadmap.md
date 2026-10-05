# Pigpen roadmap

**Status: approved for implementation on 2026-09-28.** The owner said,
“Proceed with recommended.” Research snapshots remain dated below.

Current state: source quality gates, neutral wording, the shared porting Skill,
and local staging are implemented. Package and staged-manifest validation and
the complete porter scenario pass on PiG 0.4.0, which is Pigpen's target and CI pin (the print-mode scoping blocker is resolved there: first fixed in core `22755205a`, `04eab6dd2` on the
0.3.0 pre-release line). The 24 ports in the ports list are done or queued (see `ports/README.md`). The herdr reporter (an original Go extension on the public Go SDK, not a
capability port; ported from Pigpen's earlier TypeScript version under the owner rule
of 2026-09-29 that every Piglet extension is Go) is a standalone Package in
`components/herdr`; its `herdr` Piglet is optional and source-only.
`pig-with-batteries` also selects the herdr Package (owner request, 2026-09-29). Both
Piglets build a local Piglet Binary with the reviewed PiG and pass the real-pig,
fake-herdr scenario as a Binary (see RELEASE-BLOCKERS.md). Nothing has been published.

Owner rule, 2026-09-29: every Piglet Pigpen ships is Go only, and each extension it
ports is published as its own installable PiG Package (discovery keyword `pig-package`).
The focused [`pig-extension-porter`](../../piglets/pig-extension-porter/README.md) Piglet
implements the porting workflow; [`pig-porter`](../../piglets/pig-porter/README.md)
extends it. `dirty-repo-guard` (from Pi's examples) is the first port made with it.
See [Extension porter Piglet](#extension-porter-piglet).

### The ports list

Approved 2026-09-30. [`ports/ports.json`](../../ports/README.md) is the dispatch list for every Pigpen port
and move (25 rows at creation: the ports and moves that exist, the games, the pig-stuff moves, the three Jev
lanes with the shared TypeScript SDK client, and the queued ports from
the pigpen-top-installs research). Each row pins its upstream to a commit and
carries license, author, target Package, lane, status and credit text; the table in `ports/README.md` is generated
from it. `npm run check` validates the list against the tree. The porting Skill takes a row as its input
(`port row <id>`) and moves it `porting` then `review`. The roadmap table below keeps the reasons and estimates;
the list is the source for status and pins. See [CONTRIBUTING.md](../../CONTRIBUTING.md#the-ports-list).

Warden (pi-warden by DevMortimer, MIT, pinned `a12b2703`) is ported as
[`components/warden`](../../components/warden/README.md) on the shared Go TypeSafe client
[`components/typesafe`](../../components/typesafe/README.md); the
[`pig-warden`](../../piglets/pig-warden/README.md) Piglet selects it. Done: the four guards (irreversible, off-task,
stuck, done claims), consent-first opt-in, TypeSafe and own-model backends, a 2,552-command differential against the
original, 19 live equivalence scenarios against the original under Pi, and a fused Binary proven against a real
host. Open: the SDK tool-call arguments finding, and the owner's choice of which deferred pi-warden
guards to port next ([`port/PORT.md`](../../components/warden/port/PORT.md), RELEASE-BLOCKERS.md).

Row 15, `ahp`, is ported as the standalone Package `components/ahp` (Go, on the public SDK;
see its `proof/PORT.md`): all 430 pi-ahp test cases have a Go twin or a named skipped twin
(375 run; skipped: dev tunnel 20, live-Pi replay 15, git changesets 15, and 5 others). It serves the
running PiG session over Microsoft AHP 0.9.0 (pinned `296b25e`), with no listener unless
`--ahp` or `/ahp start`, and filesystem, terminal and session deletion as separate opt-ins.
It is proved against a real `pig` and a fused Piglet Binary; it is not yet selected by
`pig-with-batteries` (a later step). Open: git changesets, the differential run against pi-ahp
under Pi, and an interop run with Microsoft's AHP Go client.

### Local staging: approved and implemented

The owner approved generated staging instead of a new PiG workspace-reference
contract. [`npm run stage`](../../README.md#run-locally) materializes selected
local Packages beneath each generated Piglet in `dist/staged/`. It rewrites the
Package source but preserves Resource names, scopes, defaults, and discovery.
Authored Resources retain one owner. Generated copies are not committed.
Published compositions still need approved immutable Package references.

The old sibling-path failure is resolved at the composition build step. PiG's
path checks remain unchanged. The staged porter and existing batteries scaffold
both pass PiG manifest validation on installed build `6805793b`.

### Resolved: print and JSON tool scoping

**Verified on 2026-09-29 UTC:** the unchanged Pigpen scenario passes on core commit
`22755205a7345ff6e690e5bb523f8b024e85dc50`, with `emptyScopeTools: []`. Its two staged
Piglets receive the shared Skill correctly, and the restricted composition sends
no tools to the provider. Package and both staged-manifest validations also pass.

The same scenario fails on base `fa7f35ab3c82288c94911d7a75eb20d539dd5091` with
`read`, `bash`, `edit`, and `write` still exposed. Both runs used unmodified source
archives, Go 1.27.1, Node 24.14.1, a local HTTP fixture, and synthetic credentials.
The installed binary and active core worktrees were not modified. The assertion
remains enabled as a regression guard.

The core owner fixed `bindSessionExtensionActions` in
`cmd/pig/session_extension_actions.go`. The helper supplies Session-backed
`GetAllTools`, `GetActiveTools`, and `SetActiveTools` only when the mode has not
provided them. This is the adopted fix, not Pigpen's earlier print-only probe.
Source inspection confirms that existing mode-specific callbacks remain intact.

**Core-owner report:** print and JSON were affected, RPC and interactive were not.
The new unit regression fails without the fix, and related tests pass under
`-race`. The fix was on a PiG fix branch, queued for the next release candidate.
Pigpen independently retested its print-mode caller surface, not the entire core
mode matrix. Superseded: PiG 0.4.0 contains the fix, CI now pins 0.4.0, and `npm run test:porter` and
`npm run test:tool-scope` pass on it (RPC, print and JSON mode). No hosted CI pass is claimed yet.

Verification command, with `PIG_BIN` pointing to the archived source build:

```sh
PIG_BIN=/path/to/pig-at-22755205a npm run test:porter
PIG_BIN=/path/to/pig-at-22755205a npm run validate
```

Recorded fixed-build run:

| Identity or result | Value |
|---|---|
| PiG / Pi / platform | `0.3.0` / `0.87.1` / `darwin/arm64` |
| PiG Binary SHA-256 | `1a46c21136a8011a55f6ddba82fb94da9fba8289ab1007df889a1eff0092044e` |
| Staging script SHA-256 | `6d091a5db2231ed0f1e389ecb2375d8ded9aa2d0dcbc765d198b3cba1399726f` |
| Authored porter manifest SHA-256 | `15076dd60a0e16e49804f9a4e318de7efbd111fa98d47ade3ea1a4d20ec0dc1b` |
| Staged porter manifest SHA-256 | `ab32bebb93926813850055741c8af3f99931cdd74c38229fc0a4dc5566f8b018` |
| Captured request set SHA-256 | `455e50678365f4d525549aad5dbd4ebee3298b11e8a2cdbfcb1ed846fa89bd61` |
| Provider requests / empty scope | `4` / `[]` |
| Scenario / validation exit | `0` / `0` |

Historical diagnosis: older builds logged `No tools registered yet: skipping
scoping`. Print/JSON setup replaced the in-process extension context without its
tool callbacks. The generated YAML retained `tools: []` correctly. Pigpen's
three-callback print-only change in a disposable `495cd0b6` snapshot established
the cause before the core owner implemented the shared-helper correction.

### Staging verification, 2026-09-28

One repeatable scenario in `scripts/porter.test.mjs` exercises the real PiG CLI
against a local HTTP fixture with synthetic credentials. It confirms:

- Direct Package validation, installation, full Skill expansion, and removal.
- One shared Package selected by two staged Piglets. The second is a test-only
  composition, not an extra production Piglet or a claim about finished batteries.
- Exact Skill delivery, unchanged user settings, and exclusion of unselected
  Package Skills and ambient Skills.
- Unchanged authored files, preserved licenses/credits and executable mode,
  deterministic manifest output, and removal of stale generated files.
- Restaging source edits and running the staged composition without its original
  source directories at their old paths.
- Rejection of escaping/absolute/missing Package paths, duplicate YAML keys,
  source/output symlinks, and generated-destination collisions.

The scenario first failed because the staging CLI did not exist. Mutating the
Package rewrite to retain the sibling path later failed the manifest comparison.
Restoring it returned the diagnostic-runtime run to green. The subsequent core
fix and base comparison are recorded above. The scenario emits source,
stage-script, and request hashes with runtime identity and terminal result.
The provider fixture proves delivery, not agent reasoning.

`npm test` (32 quality cases), `npm run check`, `npm run validate`, target-matrix
validation, and `git diff --check` passed locally at that time (superseded: CI pins PiG v0.4.0 now).
Hosted CI has not run.

### Owner clarifications, 2026-09-28

- Implement the extensions and original games in **Go**. Do not count a Go
  launcher around the existing TypeScript implementation as a native port.
  Skills remain Markdown and manifests remain declarative data.
- ACP, AHP, and A2A are separate extensions. They can remain separate while one
  Piglet selects several. Separate Piglets are optional compositions, not an
  architectural requirement for extension independence.
- AHP means Microsoft's Agent Host Protocol.
- `pig-doom` is an original pig-themed raycaster, not a DOOM engine or WAD port.
- The owner approved the recommended split porter placement, y0usaf Jev port
  foundation and identified corrections, shared components, and neutral wording.
  The initial battery defaults follow the recommendation below. Context licensing
  still requires checking the exact selected dependency contents before copying.

## Recommendation

Build the extension-porting procedure first. Use it to port web access and
subagents, then compose `pig-with-batteries` from independently installable
Packages. MCP is not a battery: it is built into PiG from 0.4.0 (see
[MCP is built into PiG](#mcp-is-built-into-pig)). Keep protocol
servers, context rewriting, telemetry, and games out of automatic activation.

Use Nico Bailon's `pi-web-access` and `pi-subagents` as the primary port sources.
Both lead the relevant candidates in npm downloads and GitHub stars. Treat
`billion-context-pi` as the leading Pi-specific compression candidate, but do not
ship its kernel without reviewing its additional attribution terms.

Keep PiG's repository-bound porter in PiG. Put the general extension-porting
Piglet and Skill in Pigpen. Share procedures by reference, not by maintaining
two copies of the core parity machinery.

Implementation follows these approved recommendations. Local staging resolves
shared-Package selection without a core path-contract change. Core fix `22755205a`
resolves the print-mode runtime blocker. JavaScript workflow compatibility remains
a separate decision because no Node dependency or reduced workflow scope was approved.

## Evidence and ranking method

**Stated** means a first-party source or API reports the value. **Inferred** means
this report draws a conclusion from those sources. **Unverified** identifies an
access limitation, an unresolved identity, or behavior not exercised here.
Recommendations and effort estimates are inferred, not upstream commitments.

Downloads below come from npm's downloads API for **2026-08-28 through
2026-09-26**, unless a row explicitly says “gallery snapshot.” They count
package downloads, including automation and repeat installations, not people.
Stars measure repository interest, not installations. A monorepo's stars do not
belong to every package inside it.

Discovery used [Pi's gallery][gallery], [npm's keyword search][npm-search],
category searches, GitHub repository searches, and web search. The keyword API
returned 10,760 results, while the gallery displayed 5,394 listings. These are
different indexes, not a complete installation census. I inspected the first
250 popularity-weighted npm results and targeted search/context/subagent/Jev
results. “Leading” below means leading in this inspected evidence.

The [pi-in-go.dev Packages page][pig-packages] displayed 27 packages, with a
2026-09-26 snapshot date. It included web access, MCP, memory, context compression,
LSP, questions, todos, and observability. Its disclaimer explicitly denies
released-PiG compatibility and security-audit claims. Inclusion is discovery
evidence only. Its snapshot can lag upstream releases.

### Candidate measurements

All numeric rows are **stated** API observations. The final column is the
**inferred** interpretation. GitHub activity is `pushed_at`, not a reviewed
release date or proof that every recent change is useful.

| Capability / package | npm downloads | GitHub stars / last push | Interpretation |
|---|---:|---|---|
| `pi-web-access` | [443,475][d-web] | [1,546 / Sep 27][web] | Primary search/fetch candidate. |
| `pi-web-search` | [19,490][d-web-alt] | [31 / Sep 20][web-alt] | Smaller alternative. GitHub did not identify its license. |
| `pi-subagents` | [470,277][d-sub] | [3,778 / Sep 27][sub] | Primary delegation candidate. |
| `@tintinweb/pi-subagents` | [32,485][d-tintin] | [1,220 / Sep 3][tintin] | Credible alternative, not the current leader. |
| `pi-interactive-subagents` | Not measured | [712 / May 12][interactive-sub] | Interactive terminal-oriented alternative. |
| `pi-acp` | [283,864][d-acp] | [704 / Sep 24][acp] | Substantial editor-bridge adoption signal. |
| `pi-ahp` | [1,048][d-ahp] | [18 / Sep 24][ahp] | Newer, smaller host adapter. |
| `billion-context` | [241,224][d-billion] | [329 / Sep 27][billion] | Largest inspected compression distribution, but cross-harness. |
| `billion-context-pi` | [59,250][d-billion-pi] | [219 / Sep 26][billion-pi] | Leading inspected Pi-specific context-rewriting package. |
| `context-mode` | [75,165][d-context-mode] | [24,136 / Sep 27][context-mode] | Cross-harness project. Elastic-2.0 licensing limits reuse. |
| `pi-memory` | [35,019][d-memory] | [Repository][memory] | Persistent memory, not a replacement for context compression. |
| `pi-hermes-memory` | [21,003][d-hermes] | [Repository][hermes] | Memory, session search, and secret-scanning alternative. |
| `@henryqw/pi-auto-compact` | [8,741][d-auto-compact] | [Monorepo][auto-compact] | Smaller repeated-read trimming and threshold-compaction option. |
| `pi-context-view` | [8,032][d-context-view] | [69 / Sep 20][context-view] | Inspection UI, not a compression engine. |
| `pi-context` | [2,930][d-context] | [Repository][context] | Smaller agentic-context alternative. |
| `pi-mcp-adapter` | [1,120,235][d-mcp] | [1,552 / Sep 27][mcp] | Largest inspected Pi package. **Not ported: MCP is built into PiG from 0.4.0** (see [MCP is built into PiG](#mcp-is-built-into-pig)). Kept as inventory evidence only. |
| `@juicesharp/rpiv-ask-user-question` | [231,413][d-ask] | [839 / Sep 24, whole monorepo][rpiv] | Structured clarification deserves a battery slot. |
| `pi-lens` | [96,795][d-lens] | [443 / Sep 27][lens] | Code intelligence deserves evaluation before an omp port. |
| `@langfuse/pi-observability-plugin` | [198,579][d-observe] | [10 / Sep 25][observe] | High downloads and low stars disagree. Offer separately and opt-in. |
| `@oh-my-pi/pi-coding-agent` | [560,346][d-omp] | [33,484 / Sep 27][omp] | Popular complete harness, not an extension installation count. |

npm search and the Pi gallery reported 448,201 for web access and 35,255 for
tintinweb, differing from the downloads endpoint. This report uses the explicit
API window consistently rather than silently mixing those snapshots.

### Community discussion

- **Stated, anecdotal:** [HN's omp discussion][hn-omp] had 42 points and 15
  comments. Readers praised integrated search and simple setup. Others questioned
  benchmark discipline and subscription OAuth terms. The [thread API][hn-api]
  exposes those comments. This supports demand for integrated capabilities, not
  a causal explanation for all omp adoption.
- **Stated through a secondary reader:** Nico Bailon's [X announcement][x-sub]
  describes JavaScript orchestration, parallel/sequential phases, and worktree
  isolation. DuckDuckGo found the post, and the [FxTwitter response][fx-sub]
  returned its text. The source README independently describes those features.
  I do not use mirror engagement counts to choose a port.
- **Unverified:** web search found a Reddit [Pi setup discussion][reddit].
  Reddit JSON requests returned HTTP 403. HTML returned a loading/challenge page,
  including through `old.reddit.com`. I could not read the discussion or verify
  votes. No Reddit preference claim enters the ranking.
- **Search limitations:** Google returned a redirect/challenge page. Bing often
  ignored the intended Pi-agent scope. DuckDuckGo returned relevant gallery and
  X results before some queries received bot challenges. Repository and registry
  APIs supplied the comparison evidence instead.

## Ranked roadmap

Rank reflects user value, dependency order, and risk, not just downloads.
Effort is an engineering estimate in person-days, including parity and packaging.
It excludes waiting for credentials, owner decisions, legal review, or core SDK
repairs. Full current upstream implementations are substantial projects.

All runtime implementations in this roadmap target Go. “Package” describes how
users obtain an extension, not its implementation language.

| Rank | Piglet / extension | What it does | Source, author, license, popularity | Port approach | Effort | Main risks |
|---:|---|---|---|---|---|---|
| 1 | `pig-with-batteries` | Recommended daily coding composition. | Original composition under repository MIT. Sources below retain their licenses. No published usage. | Select pinned component Packages. Preserve normal model choice and built-in tools. Integrate after individual ports pass. | 3–5 after dependencies | Name/tool collisions, ambient discovery, hidden paid services, falsely claiming release readiness. |
| 2 | `pig-porter` + extension-port Skill | Guide and verify Pi extension ports. | Adapt procedure from [PiG Porter][porter], Michael Kinsy, MIT. Repository workbench, not an npm popularity contest. | Keep core parity workbench in PiG. Author general `pigpen-pi-extension-port` Skill in a Pigpen Package, selected by this Piglet. | 3–6 | Confusing core parity with arbitrary extension parity. Missing SDK operations must block completion. |
| 3 | `websearch` | Search, fetch, retain sources, and retrieve bounded content. | [pi-web-access][web], Nico Bailon, MIT. 443,475 downloads, 1,546 stars. | Go SDK factory. Preserve provider/config tables, routing, errors, cache semantics, credentials, and extraction behavior. | 15–30 | Browser-cookie privacy, SSRF, redirects, paid fallbacks, PDF/video dependencies, changing provider APIs. |
| 4 | `subagents` | Delegation, background work, supervision, and workflows. | [pi-subagents][sub], Nico Bailon, MIT. 470,277 downloads, 3,778 stars. | Go-owned sessions/processes and persisted run records. Preserve observable workflow and fleet behavior. | 20–40 | JS workflow runtime, child isolation, cancellation, recursion/cost limits, detached work and reload. |
| 5 | ~~`mcp`~~ | **Not needed.** MCP is built into PiG from 0.4.0; users configure MCP servers directly in PiG. | Formerly [pi-mcp-adapter][mcp], Nico Bailon, MIT. Not ported and not shipped. | None. Removed from the port queue. | None (excluded from estimates) | None. |
| 6 | `acp` in `piglets/acp/` | Let editors drive a PiG agent. | [pi-acp][acp], Sergii Kozak, MIT. 283,864 downloads, 704 stars. | Port the stdio JSON-RPC ↔ agent RPC adapter in Go. Keep an explicit protocol entrypoint separate from the TUI. | 8–15 | Capability negotiation, session mapping, permissions, unimplemented upstream features. |
| 7 | `context` | Reversible, searchable context compression. | [billion-context-pi][billion-pi], ranxianglei, MIT wrapper. Kernel adds attribution terms. 59,250 downloads, 219 stars. | Go port of pinned context pipeline and storage contract after license approval. One compression owner. | 15–30 | Transcript/reference drift, forks, cache invalidation, irreversible truncation, bundled kernel license. |
| 8 | `ask-user` | Structured human clarification. | [rpiv question package][rpiv], juicesharp, MIT per package metadata. 231,413 downloads, 839 monorepo stars. | Port questionnaire behavior onto PiG dialogs and headless results. Verify source license before copying. | 3–6 | TUI/RPC mismatch, cancellation mistaken for an answer, duplicate existing tools. |
| 9 | `code-intelligence` | Diagnostics, navigation, format/check feedback. | [pi-lens][lens], Apostolos Mantzaris, MIT. 96,795 downloads, 443 stars. | Port through external language servers. Separate automatic edits from read-only diagnostics. | 12–25 | Server installs, per-language behavior, stale diagnostics, unapproved formatting changes. |
| 10 | `observability` | Trace model calls, tools, cost, and latency. | [Langfuse Pi plugin][observe], Langfuse, MIT. 198,579 downloads, 10 stars. | Go tracing adapter with explicit export configuration. No default telemetry in batteries. | 4–8 | Prompt/secret export, provider-specific usage accounting, endpoint credentials. |
| 11 | `pig-runner` (moved to `components/pig-runner`, full-screen only; mini-mode open) | Pig obstacle runner in mini and full modes. | Reuse [PiG Standard Runner][runner], Michael Kinsy, MIT. No standalone usage evidence. | Extract existing sprite/game support into shared Package, then add mini-mode and background-work proof. | 5–9 plus shared foundation | Existing command UI is not proof that agent work continues in every state. |
| 12 | `pig-tetris` | Falling blocks with the pig cheering on the left. | Original game code, repository MIT. Reuse PiG sprite with credit. | Shared game UI, board logic, and pig animation state. | 5–8 after foundation | Narrow terminals, key ownership, branding and copied artwork. |
| 13 | `angry-pigs` (moved to `components/angry-pigs`, full-screen only; mini-mode open) | Cute pig slingshot/puzzle game. | Existing Go implementation in PiG Standard at `6805793b`, Michael Kinsy, MIT. No standalone usage evidence. | Reuse the existing implementation and shared artwork. Coordinate ownership before moving it into Pigpen. | Re-estimate packaging and missing modes | Demo gameplay does not prove background-work isolation or mini-mode. Do not duplicate the maintained source. |
| 14 | `pig-doom` | Original pig-themed first-person raycaster, confirmed by owner. | Original Go code, repository MIT, using the credited PiG sprite. | Shared presentation/status layer, independent renderer. No DOOM engine or WAD dependency. | 10–20 | Rendering performance, background-work isolation, and release naming. This is not a DOOM port. |
| 15 | `ahp` | Multi-client remote session host. | [pi-ahp][ahp], Bang Lee (Qusic), MIT. 1,048 downloads, 18 stars. [Microsoft AHP][ahp-spec]: 370 stars. | Separate Go host extension targeting the confirmed Microsoft protocol. Pin its protocol version. | 12–25 | Reconnect/reconciliation, auth, remote filesystem/terminal exposure, version churn. |
| 16 | `a2a` | Serve PiG tasks to agents and call remote agents. | Original adapter using [a2a-go][a2a-go], A2A contributors / Linux Foundation, Apache-2.0. SDK: 474 stars. | Use upstream Go client/server SDK. Interoperate with kagent rather than porting Kubernetes orchestration. | 10–20 | Task/context identity, cancellation, auth, tenant boundaries, protocol versions. |
| 17 | `jev` | Typed judgments, tool-call gate, and output judge. | Approved foundation: [y0usaf/pi-jev][jev-y], y0usaf, MIT. 149 stars. Code-review findings below are port blockers. | Native Go port with the approved defect corrections, on the shared Go TypeSafe client (`components/typesafe`) with a second, own-model backend. **Status 2026-09-30:** ported in `components/jev` (12 scenarios identical to the original under Pi 0.87.1, 81 of 81 mutants killed, Binary and end-to-end proofs; see `components/jev/port/PORT.md`); opt-in, off by default, selected by `pig-with-batteries`. | 4–10 | Sensitive content leaves the machine. Preserve and disclose failure policy. Model judgment is not deterministic authorization. |
| 18 | omp-derived capabilities, later | Selected improvements after omp stabilizes. | [oh-my-pi][omp], Can Bölük / Stencil Labs, with Mario Zechner upstream credit. MIT plus third-party notices. | Re-evaluate bounded features, not a wholesale fork or internal-runtime port. | Estimate per feature later | Refactor churn, incompatible runtime internals, benchmark and license claims requiring verification. |

### MCP is built into PiG

Owner ruling, 2026-09-29: MCP is built into PiG from 0.4.0 (family 8a, "MCP in
Go", merged with PiG's Pi 0.99 port). Pigpen does not port or ship
`pi-mcp-adapter`, and no Piglet needs an MCP adapter extension. Users configure
MCP servers directly in PiG. The boundary, configuration and migration plan are in
`docs/design/builtin-mcp.md` (in PiG `v0.4.0`; this repository does not vendor it).
Features of the built-in client are documented there, not here.

The same rule applies to any package whose function PiG already provides:
the extension-porter Skill tells the porter to skip it instead of porting it.

### Proposed battery set and sequence

The first battery composition should select web access, subagents, and structured
questions. It does not include MCP, which PiG provides itself. Add code intelligence when its external requirements are clear.

Offer games through explicit commands, with animations disabled until requested.
Bundling an extension and activating its background behavior are different
choices. A bundled game need not start playing. A bundled protocol adapter need
not open a listener. A bundled telemetry extension need not export anything.

The proposed first defaults enable ordinary coding tools and explicit delegation.
Context rewriting, Jev judgments, protocol listeners, and telemetry require an
explicit choice. They can still be separately installable components or selected
in a larger Piglet. The owner approved these initial defaults. Additional battery
members still need the verification described in their roadmap rows.

Order the work by dependency:

1. Owner approval is recorded above. Review each PiG SDK/runtime pin before porting.
2. Build the porting Skill and prove it on one approved extension slice.
3. Port web access and subagents through that procedure. Resolve SDK gaps in PiG.
4. Port questions, then validate the battery composition. MCP needs no port.
5. Build ACP independently. Follow with approved context management and games.
6. Add AHP, A2A, Jev, and later omp features as separate reviewed work.

A scoped release is acceptable only when the owner approves the scope explicitly.
Do not call a reduced search-only implementation a complete `pi-web-access` port.
Do not drop JavaScript workflows from a “full” subagents port without approval.


### Port-specific constraints

For web access, preserve `web_search`, `fetch_content`, stored-content retrieval,
source-check artifacts, commands, provider routing, and configuration behavior.
Keep browser cookies opt-in and credential headers origin-bound. Test private
addresses, redirected requests, cancellation, cache expiry, and authenticated
fetches before broad provider integration. PDF/video support must name external
runtimes and redistribution terms, not hide them behind “single binary.”

For subagents, preserve delegation authorization, scoped tools/models, child
progress, result artifacts, steering, cancellation, and background supervision.
The current upstream uses in-process SDK sessions for foreground children and a
detached runner for background work. A Go port must reproduce those observable
lifetimes without sharing mutable Session state accidentally. Parent completion,
reload, and shutdown each need an explicit child disposition.

Arbitrary JavaScript code-mode workflows need a JavaScript runtime even if their
supervisor is Go. No Node dependency is approved. Decide whether to preserve
those scripts through an explicit external runtime or replace their authoring
contract with Go-native workflows. Do not hide that choice inside the port or
use a partial interpreter. The extension implementation itself remains Go.

Go implementation also does not mean rewriting remote model services, browsers,
or language servers in Go. List those external requirements separately.

### Additional packages to assess, not automatically bundle

The gallery's popularity snapshot also surfaced these omissions. Numbers here
are **gallery snapshot** downloads, not the explicit-window API measurements
above. These are discovery leads, not verified port or license approvals.

| Package | Source / popularity evidence | Disposition |
|---|---|---|
| `@juicesharp/rpiv-todo` | [juicesharp/rpiv-mono][rpiv], 169.3K/month in [gallery][gallery] | Evaluate persistent task visibility alongside questions. Avoid a second competing task store. |
| `@plannotator/pi-extension` | [backnotprop/plannotator](https://github.com/backnotprop/plannotator), 90.9K/month in [gallery][gallery] | Review/annotation workflow candidate after the basic battery set. |
| `pi-background-tasks` | [ismailsaleekh/pi-background-tasks](https://github.com/ismailsaleekh/pi-background-tasks), 84.6K/month in [gallery][gallery] | Compare with subagents lifecycle before creating another background-job owner. |
| `pi-powerline-footer` | [nicobailon/pi-powerline-footer](https://github.com/nicobailon/pi-powerline-footer), 60.4K/month in [gallery][gallery] | Compare status needs with the shared game/footer design. One footer owner. |
| `@companion-ai/feynman` | [Companion-Inc/feynman](https://github.com/Companion-Inc/feynman), 180.9K/month in [gallery][gallery] | Research-focused harness, not a drop-in general coding battery. |
| `bigpowers` | [danielvm-git/bigpowers](https://github.com/danielvm-git/bigpowers), 95.6K/month in [gallery][gallery] | Review individual useful Skills and license provenance. Do not import an entire methodology or collide with user Skills. |

### Moved from the owner's own PiG configuration, 2026-09-29

The owner's personal Go extensions and Skills are original or adapted work he may
release under this repository's MIT license. Rule applied: move it when it applies and no
far more popular public extension does the same job; where one exists, that public
extension is the port target and the owner's code is only a starting point. 30-day npm
downloads, checked 2026-09-29.

| Owner's code | Decision | Evidence |
|---|---|---|
| `session-ingest` (`read_session`) | **Moved**: [`components/session-ingest`](../../components/session-ingest/README.md). The advertised `turn`, `tools`, `stats` modes were implemented. | Nearest public tools do other jobs: `@moyai/pi-session-hoarder` (39,431, archives sessions), `@astrosheep/pi-context` (8,116, reset windows and history tools). |
| `context-info` (`/context`, `/tools`, `/cost`, `/prompts`, footer) | **Moved**: [`components/context-info`](../../components/context-info/README.md), footer opt-in. `/prompt-edit` and the subagent cost section left behind. | No popular equivalent: `@narumitw/pi-usage` (69,610) reports account rate limits, `pi-powerline-footer` (77,173) is a footer; the context viewers are small (`@astrosheep/pi-context` 8,116, `@mrclrchtr/supi-context` 3,475, `pi-context` 3,174, `pi-context-inspector` 953). |
| `skills/` | **Partly moved**: [`components/dev-skills`](../../components/dev-skills/README.md), eight general Skills with credit. The rest tie into the author's private workflow or wrap third-party CLIs. | Most are adaptations of `mattpocock/skills` (MIT) and `mitsuhiko/agent-stuff` (Apache-2.0). |
| `ask` | **Not shipped.** Go base for the `@juicesharp/rpiv-ask-user-question` port (274,750). | Builds and passes its tests on the public SDK with only the import renamed. The port is separate work. |
| `subagent` | **Not shipped.** Go base for the `pi-subagents` port (536,567). | Builds on the public SDK except five call sites. The target has no top-level chain or parallel input, only a JavaScript `workflowScript`, so the workflow-runtime decision comes first. |
| `web-search` | **Skipped**: `pigpen-websearch` ports `pi-web-access`. | |
| `spec-pig`, `spec-steward`, SpecOps | **Held**, not moved: depend on a SpecOps engine of unconfirmed authorship and license, and on a workflow for one team. | Revisit when the engine's license is settled. |

## Jev identification

Jev is not necessarily a misspelling. [TypeSafe's documentation][typesafe]
identifies Jev as its typed decision model. Its primitives are Choice, Score,
and Noul, which return structured values and probabilities.

| Candidate | Author / license | Evidence | Meaning and disposition |
|---|---|---|---|
| [`@y0usaf/pi-jev`][jev-y] | y0usaf, MIT | [522 downloads][d-jev-y], 149 stars, Sep 25 push | Most-starred inspected dedicated Pi Jev repository. Tool-call gate, output judge, `jev_ask`. Shadow by default and errors fail open. Best candidate if “favorite” means GitHub stars. |
| [`pi-jev`][jev-t] | Theophilo Damiao, MIT | [1,520 downloads][d-jev-t], 56 stars, Sep 24 push | Higher measured npm use than y0usaf. Tool/skill discovery, typed evaluation, opt-in model routing, guard, compaction, and subagent orchestration. |
| [`jev-guard`][jev-guard] | leepokai, MIT | [219 downloads][d-jev-guard], 43 stars, Sep 24 push | Cross-agent security hook with a Pi extension. Relevant if the owner means a guard rather than a router. |
| [`pi-jev-guard`][jev-unknown] | npm maintainer `alucard_24`. License/repository absent from latest metadata. | [3,661 downloads][d-jev-unknown] | Different package from `jev-guard`. Do not infer authorship or redistribution permission. Hold pending source and license verification. |

### pi-typesafe port (owner approved)

[DevMortimer/pi-typesafe](https://github.com/DevMortimer/pi-typesafe) (Ryan Gapac, MIT, pinned `ed439f8`) is ported to Go as two Packages:
[`pi-typesafe`](../../components/pi-typesafe/README.md) (the batched `typesafe_evaluate` tool, `/typesafe` command and renderers) and
[`pi-typesafe-api`](../../components/pi-typesafe-api/README.md) (the typed API for extension authors), over the shared client
[`typesafe`](../../components/typesafe/README.md). The Piglet [`pig-typesafe`](../../piglets/pig-typesafe/README.md) selects it. It is
**off by default**, and content goes to `api.typesafe.ai` unless the own-model backend (the model PiG is configured with) is selected.
Record and differences: [`components/pi-typesafe/port/PORT.md`](../../components/pi-typesafe/port/PORT.md).

### Code, issues, and review follow-up: 2026-09-28

**Approved recommendation:** use y0usaf's focused judgment/gate design as the
first Go port source, with the defects below treated as release blockers. It
adds typed judgments without also owning models, compaction, and subagents.
This is a recommendation about a port foundation, not approval to install its
current npm release unchanged.

If the wanted feature is automatic tool/skill/model routing, TheoOliveira is the
relevant source instead. That broader behavior is not equivalent to y0usaf's
gate. Do not combine all three projects under one name and call it parity.

I inspected the client, policy, configuration, and Pi wiring for the three linked
repositories. I also inspected published `pi-jev-guard@0.7.2` metadata, its complete
archive listing, and its provider-overlay implementation. Sources remain pinned
to the commits in the candidate links. This was a targeted review, not a complete
security audit or live-model accuracy comparison.

| Candidate | Verified source strengths | Current limits and port blockers |
|---|---|---|
| y0usaf | Five runtime source files. Separate HTTP client, gate, output policy, and Pi hooks. Current source rejects missing answers and wrong answer types. No provider monkey-patching. | Verdict cache ignores user intent/cwd. Project config can redirect the endpoint while retaining the environment key. `maxStateChars` is parsed but not applied. No checked-in test suite or `test` script in the inspected tree. |
| TheoOliveira | Separate routing, guard, compaction, and orchestration modules. Checked-in tests and successful CI on the inspected main commit. Tool routing no longer activates arbitrary tools when Jev fails. | Open fixes cover URL detection, API-key precedence, and false-positive Skill selection. Model selection uses regex/heuristics, not Jev. The custom compactor inspects only the first 24 branch entries and truncates each to 900 characters. Do not adopt that compactor as a general replacement. |
| leepokai | Explicit deny/ask/allow policy with user authorization and untrusted-content signals. Pi blocks an ask verdict when no UI can ask. Instruction-cache hits reapply current thresholds. Tests cover several adapter boundaries. | Missing content-scan probabilities become zero and can report a malformed reply as clean. Scans skip short text and truncate long text. Current fixes have not reached npm. The inspected source has no current CI workflow. Broader cross-agent support is extra port scope. |
| `pi-jev-guard` / alucard_24 | Published code implements on-demand review and a provider-output gate. | Neither package metadata nor the 30-file archive supplies a license. Repository identity remains unresolved. Its overlay mutates live provider methods, making this the most coupled PiG port. Do not copy it without permission. |

#### Public reports versus current source

- **y0usaf:** [issue #1][jev-y-issue1] reports a startup failure on older Pi.
  [Issue #3][jev-y-issue3] reports empty answers presented as a clear verdict.
  Both have merged fixes. I confirmed current-source rejection of empty answers.
  npm `latest` still resolves to **0.2.0**, while the inspected source declares
  **0.2.2**. The latest [publish run][jev-y-publish] failed its OIDC preflight,
  not a behavior test. Port the reviewed Git commit, not an assumed fixed npm tag.
- **TheoOliveira:** [issue #1][jev-t-issue1] caught fallback activation with false
  certainty. Current `router.ts` no longer does that. The maintainer also merged
  fixes for loader failure, request batching, token reporting, and mode gating.
  npm **0.6.0** matches the reviewed commit, and its [main CI][jev-t-ci] passed.
  [PR #19][jev-t-pr19], [#20][jev-t-pr20], and [#21][jev-t-pr21] remain unmerged.
  PR #21 reports genuine Skill-routing positives and negatives, but those are
  contributor measurements on that branch, not proof about current main.
- **leepokai:** [issues #1–#3][jev-g-issues] report instruction-file false positives,
  skipped subagent results, and invalid threshold handling. Current source fixes
  those paths. npm **0.3.1** points to an earlier commit without those fixes.
  [PR #5][jev-g-pr5] proposes OpenRouter/custom endpoints and remains open.
  Do not describe that proposed support as shipped.

Closed issue counts alone do not prove correctness. Similarly, pending external
PR workflows marked `action_required` are not failed tests. These projects are
small enough that a few reports can expose a large fraction of their behavior.

A [September 23 comparison][jev-comparison] distinguishes y0usaf's gate from
TheoOliveira's router and emphasizes their short track records. It is useful
orientation, not independent accuracy evidence. Some details are stale or too
broad: current TheoOliveira includes a guard, and failure policies differ by
feature. The most useful review evidence was the reproducible GitHub reports
and maintainer responses. AnswerOverflow returned 403. Further Reddit/X searches
received challenges. No broad reviewer consensus was established.

#### Local probes: observed, without live credentials

The probes execute the reviewed TypeScript/JavaScript after Node's built-in type
transformation, with a fake Pi host and fake HTTP responses. They do not execute
agent shell commands, call Jev, or test full Pi/PiG sessions.

| Input and real source boundary | Observed output | Consequence |
|---|---|---|
| y0usaf `tool_call` hook twice with identical tool arguments but changed user intent | One HTTP request, no confirmation, second status `jev: clear (enforce)` | Cached judgment can outlive the authorization context it assessed. |
| y0usaf config names a project endpoint, environment supplies a synthetic key | Intercepted request targets that endpoint and includes the synthetic key | Credential destinations need a trusted configuration boundary. This was not a live credential disclosure. |
| y0usaf `askJev` receives `{answers:{}}` | Rejects the missing required answer | Confirms the merged fix, not the old npm release. |
| TheoOliveira compactor receives 25 distinct history entries | Considers 24 and returns a nonempty summary without the last entry | Fixed-prefix sampling cannot establish preservation of arbitrary compacted history. |
| leepokai `scanContent` receives an injection choice without its required probability | Returns `flagged:false`, `p:0`, and `clean (injection, p=0.00)` | A malformed response becomes a clean result instead of an unavailable verdict. |

The y0usaf paths are [cache lookup][jev-y-cache], [cache key][jev-y-cache-key],
[state construction][jev-y-state], and [configuration merge][jev-y-config].
Its own [NEXT.md][jev-y-next] acknowledges the unused state cap. The other
reproductions follow [TheoOliveira's compactor][jev-t-compact] and
[leepokai's content scanner][jev-g-scan].

Probe command: `env -i PATH="$PATH" HOME=/tmp/pigpen-jev-review node --experimental-vm-modules /tmp/pigpen-jev-review/probe.mjs`.
Node `v24.14.1`, exit 0 after assertions confirming the observations above.
The temporary probe SHA-256 is
`ee58ac5f0cbcb6f0bd9c377d6ffc0d57a9b4e732f37aeae49f2f1d9d70fcd3b2`.
The inspected npm archive SHA-256 is
`58a6eb207abcb8c7f0498fd9b2a545545705f02de970fd043c5fa2eb0e90382d`.
These checks do not measure false-positive rates, latency, or model calibration.

A Go port must not reproduce known defects silently. Put proposed corrections
in its approved contract: context-bound caching, trusted credential destinations,
strict typed-response validation, and truthful unavailable/error states. Preserve
and disclose the chosen fail-open/fail-closed policy. Jev remains a probabilistic
adviser or gate, not a sandbox or proof that code is correct. The owner approved
this source/scope and these corrections. New observable changes need separate approval.

## Porter placement and the porting Skill

### Existing porter is repository-bound

**Stated:** [PiG's Porter README][porter] and [Skill][porter-skill] bind the
workbench to PiG's pinned Pi release, `parity/*`, `.upstream/current`, Make
commands, behavior families, and divergence records. It already lives at
`piglets/porter/`, with a `pig-porter` extension. It does not own commits or
publishing.

**Recommendation:** use both repositories with different responsibilities.

- PiG retains core parity contracts, provider catalogs, conformance fixtures,
  and its repository-maintenance porter. Moving those into Pigpen would couple
  every PiG upgrade to a separate release without making extension ports easier.
- Pigpen owns a general extension-porting Skill and a Piglet that selects it.
  Name the Skill `pigpen-pi-extension-port` to avoid the existing `pig-porter`
  Skill and common user Skill names.
- Reuse core documentation by pinned reference. Do not copy core parity packages,
  fake their expected checkout, or run Make against an arbitrary directory.
- The Skill is the initial deliverable. Add porter tools only when a repeated
  mechanical step needs a tool. A Skill-only Piglet can run as source. Do not
  add an empty extension just to manufacture a Binary build.

### Placement tradeoffs

“Core” here means the PiG source repository, not a feature automatically loaded
by stock PiG. The current porter already is an ordinary Go extension and Piglet.
Its [extension source][porter-source] directly imports `parity/closure` and
`parity/porter`, then delegates requests to that repository's contracts.

| Placement | Advantages | Costs |
|---|---|---|
| Everything stays in PiG | Runtime, parity engine, tests, and porter change together. Least migration work. | General extension authors inherit PiG-maintenance assumptions. Updating the porting procedure is tied to the core repository. |
| Move everything to Pigpen | One place to find user-facing Piglets and extension-porting tools. Independent packaging. | The core parity engine still depends on PiG internals. Moving it creates cross-repository version coordination or duplicated machinery. |
| Split by job, recommended | PiG keeps its own parity engine/workbench. Pigpen supplies the general extension-porting agent/Skill. Each has one source owner. | Two related workflows need clear names and versioned documentation references. Shared procedures must not drift into copies. |

The proposed split does not require two porter engines. Keep PiG-maintenance
operations where their tests and data live. Put the reusable extension-porting
procedure next to the extensions it will port. Whether both compositions should
use “porter” in their names remains an owner decision.

### Required process for `pigpen-pi-extension-port`

The canonical procedure and completion checklist live in
[`pigpen-pi-extension-port`](../../components/extension-port/skills/pigpen-pi-extension-port/SKILL.md).
Its Package installs independently or through the staged
[`pig-extension-porter`](../../piglets/pig-extension-porter/README.md) composition.
[CONTRIBUTING.md](../../CONTRIBUTING.md) owns contributor obligations, while scripts
and CI enforce the mechanical checks. Do not maintain a second procedure here.

The Skill covers source/license pinning, complete API inventory, SDK mapping,
caller-surface failing cases, Go implementation, Pi/PiG comparison, distribution,
and author attribution. A missing contract or failed case blocks completion.

### Extension porter Piglet

Decided 2026-09-29, with evidence.

**Structure.** PiG supports Piglet composition: `extends` (`docs/pig-piglet-spec.md`,
`coding/piglet/extends.go` at the 0.3.0 pre-release build `63c6ba456`) accepts a `local:` Piglet path
or a contributed scheme, depth at most 8, merges by name identity, and does not inherit
`release`, `build` or `extends`. Checked with the real binary: a derived Piglet validates
(inherited extension and Skill resolve from the base's own directory) and
`pig piglet build --format binary` builds it. So `pig-extension-porter` is the focused base
and `pig-porter` extends it. Shared Packages remain the way to share the Skill and the Go
extension. Limits, input for issue #92 and PiG 0.4.0: `pig piglet add` cannot copy an
`extends` closure (`coding/piglet/main.go`, "cannot copy an extends dependency
closure"), `extends` with `agentEnv` is not implemented, only `local:` and contributed
sources are accepted (no `npm:`, `git:`), and remote add cannot select shared
`components/` by sibling path (see RELEASE-BLOCKERS.md).

**Go only.** Piglets select only Go extensions; `scripts/go-only.mjs` (part of
`npm run check`) enforces it from the authored manifests. `go-only-exceptions.json` lists
the two Piglets that still select the Node herdr Package until the herdr Go port lands.
The porting Skill targets Go only. A Piglet needs at least one extension to build a
Binary, so `pig-extension-porter` carries a real one: the equivalence tools.

**Evidence, not vouching.** The Skill's proof is a differential harness
(`components/extension-equivalence`): the original under Pi, the original under PiG's Node
runtime and the port under PiG run the same scripted scenario in real hosts and must
produce identical traces. It also flags Pi APIs the Go SDK lacks (for example `pi.events`)
statically, because a scenario cannot see them. Worked example and evidence:
`components/dirty-repo-guard/port/PORT.md`.

## Protocols: separate Go extensions

ACP, AHP, and A2A each get their own Go extension and independently installable
Package. A Piglet can select any combination. Separate Piglets can offer useful
launch defaults, but they are not required to keep extensions independent.
Share only proven common session/process code. Do not invent a universal
protocol abstraction first.

### ACP: editor ↔ agent

`pi-acp` bridges ACP JSON-RPC over stdio to `pi --mode rpc`. Its development
focus is Zed. It supports streaming, session mapping, command discovery, and
usage reporting. Its README explicitly excludes filesystem/terminal delegation
and extension slash commands. Accepted MCP parameters do not become running
servers automatically.

Port that contract first, with declared capability limits. Exercise initialize,
new/load session, prompt, streaming, tool updates, cancel, and process exit using
an ACP client. Verify Zed first, then a second editor client such as JetBrains.
Advertise only implemented features. Do not silently turn an editor permission
request into local execution.

**Packaging boundary:** selecting `piglets/acp/` does not make the Piglet Binary
speak ACP on stdout. The proposed Go adapter is a companion entrypoint that
starts the selected PiG agent in RPC mode. Package it with an explicit executable
closure. Prove that handoff before promising a one-command Binary. Fused
extensions must not write protocol bytes directly to the host's stdout.

**Status (2026-09-30):** ported as [`components/acp`](../../components/acp/README.md) (companion `pig-acp` plus a `/acp` extension)
and selected by [`piglets/acp`](../../piglets/acp/README.md). The handoff is proven: the built Piglet Binary works as the
`--pig` target, driven by the ACP reference client with every message checked against schema 0.26.0, and by 11 end-to-end
scenarios. Zed and JetBrains are not yet verified; the one-command Binary is not possible on PiG 0.3.0 or 0.4.0
([RELEASE-BLOCKERS.md](../../RELEASE-BLOCKERS.md#acp-pigletsacp-componentsacp)).

### AHP: client ↔ persistent session host

**Stated:** [Microsoft AHP][ahp-spec] means **Agent Host Protocol**, with synchronized
multi-client state and reconnect/reconciliation. [pi-ahp][ahp] embeds Pi and serves
WebSocket clients, including VS Code Agent Host and Agent Console. It reports
protocol 0.9.0 support, preserved active work across temporary disconnection,
and limits on restart recovery. Microsoft now provides a Go client library.
A client library is not evidence that a complete Go server exists.

**Owner-confirmed identity:** Microsoft Agent Host Protocol. The unrelated
[Agent Harness Protocol project][other-ahp] is not this work. Pin Microsoft's
protocol separately from its language SDK.
Start local/authenticated, and do not bundle Dev Tunnels or expose a listener
without user configuration. Test multiple clients, reconnect, cancellation,
authorization, and host restart without promising unsupported durability.

### A2A: agent ↔ remote agent

Use [A2A's Go SDK][a2a-go] for Agent Cards, task state, streaming, and cancellation.
The inspected SDK targets A2A **v1.0** and uses the `/v2` Go module. Do not mix
SDK SemVer with wire version or copy older `message/send` examples blindly.

[kagent][kagent] is a Kubernetes agent framework, not an editor protocol. Its
[transport contract][kagent-transports] exposes per-agent cards and HTTP JSON-RPC
plus gRPC. Its [Go dependency file][kagent-go] pins `a2a-go/v2 v2.5.0`. Use kagent
as an interoperability target: PiG can call a kagent agent, and a PiG A2A server
can expose work to an A2A client. No Kubernetes dependency belongs in the basic
Piglet. “Interoperability target” means a real external implementation used to
check the A2A extension: can PiG send it a task, stream results, and cancel work?
It does not mean bundling kagent, requiring Kubernetes, or creating a fourth
protocol. Test authorization on discovery and calls, message/task/context
identity, stream resubscription, and cancellation in both directions.

#### Status: implemented as `components/a2a` (2026-09-29, local, unpublished)

Row 16 is built as the Go Package [`components/a2a`](../../components/a2a/README.md) and the source-only Piglet
[`piglets/a2a`](../../piglets/a2a/README.md). It is an **original adapter with no Pi oracle**, so the porting Skill's equivalence
steps are replaced by the evidence listed in `components/a2a/port/PORT.md`. Decisions recorded there:

- The SDK is `a2a-go/v2` **v2.6.0** (the version kagent's checkout pins today; the row above still says v2.5.0). A2A protocol **1.0**
  is pinned on both sides; 0.3 peers are refused rather than bridged (`a2acompat` is a named gap).
- Only the JSON-RPC binding is served (streaming yes; gRPC, REST and push notifications are named gaps).
- Identity: a `contextId` is a PiG session, a task is one worker turn; both are scoped to the caller's principal, and tokens map to
  tenants. Cancellation aborts and reaps the worker process group.
- The listener is off unless configured, refuses to start without tokens, and the worker has no tools unless the operator names
  them (PiG's file tools take absolute paths, so a "read-only" worker could read every file the account can).
- Interop: kagent's own `adk/pkg/a2a/server` package answers PiG's client, and a kagent-shaped client (a2a-go v2, JSON-RPC,
  repeated `A2A-Version` header) drives PiG's server (`scripts/interop-kagent.mjs`). A live kagent cluster was not used.

The `acp_*` compression tools in billion-context are unrelated to editor ACP.
Keep the context Package and `piglets/acp/` distinct even if upstream names clash.

## Context management and omp

### Context selection

`billion-context-pi` supplies compress/decompress/search/status behavior through
Pi's context hook. It retains compression state in a sidecar, handles history
replay, and cancels built-in auto-compaction. Its README warns against two
simultaneous compression plugins. It also ships optional delegation tools.
Disable that delegation when composing with the selected subagents Package.

Preserve the transcript and stable message references. Test compression across
resume, fork, branch, import, abort, missing sidecar, and failed model calls.
Measure retained-information quality and cache behavior with a fixed workload.
Upstream's “billions of tokens” and “5×” claims are not verified here and must
not become Pigpen promises.

**License blocker:** the wrapper's [LICENSE][billion-pi-license] is MIT, but the
current [kernel license][kernel-license] and [proxy license][billion-license]
add visible product-attribution terms. npm still reports MIT for the proxy.
Review the exact bundled kernel at the selected release. Preserve the additional
text and display the required links if approved. Do not relabel the closure as
plain MIT based on metadata.

`context-mode` has much greater repository interest, but its [Elastic-2.0
license][context-license] restricts hosted/managed offerings and requires notices
on modified copies. Do not include it as an unrestricted MIT battery. Its
cross-harness downloads also cannot establish Pi-specific adoption.

Memory and context visibility are separate capabilities. `pi-memory` and
`pi-hermes-memory` deserve later evaluation for persistent recall. `pi-context-view`
can provide inspection without taking over compaction. Keep PiG's existing
compaction active until an explicitly selected replacement passes parity.

### What to learn from omp, later

The owner identifies omp as mid-refactor. Current repository activity confirms
rapid change, but this phase did not establish a refactor completion milestone.
Do not port omp internals now.

Its README and the HN comments support these feature hypotheses:

| Feature | Later action | Why not port it wholesale now? |
|---|---|---|
| LSP on writes and DAP debugging | Evaluate separate code-intelligence/debugger Packages. | External server lifecycle and language behavior need bounded contracts. |
| Subagents, worktrees, typed yields, Agent Hub | Compare against the selected subagents port. | Two orchestrators would duplicate ownership and policy. |
| Integrated web search and document reading | Use web-access port first. | Existing upstream source already covers this need. |
| Hashline edits | Run an independent edit-success evaluation before adoption. | This changes a core model/tool contract, not just UI. |
| Checkpoint/rewind, memory, context inspection | Evaluate as separate context capabilities. | Transcript and compaction semantics must remain coherent. |
| Advisor/review workflows | Offer explicit opt-in review, with usage visible. | Automatic second-model calls add cost and information disclosure. |
| Native search/shell and bitmap compression | Leave in research until measured against PiG. | Runtime-specific Rust/Bun internals and model assumptions do not transfer directly. |

omp's [README][omp] credits Mario Zechner, Can Bölük, and Stencil Labs. It also
identifies third-party/vendored notices. Preserve those component licenses in any
later port. Popularity does not validate its benchmark claims or subscription
provider terms.

## Games and shared pig artwork

**Current ownership, verified during the demo:** PiG Standard at commit
`6805793bae95b68832621613d552833da901a7a1` contains `piglogin`, `pigrunner`, and
`angrypigs`, with shared `internal/arcade`, `internal/pixel`, and `internal/termgame`.
The local `pig-games` Binary reused that composition. It did not add game source
to Pigpen or complete the planned mini-mode and background-work verification.
Those Resources retain one source owner until a coordinated extraction.

The [extension API][pig-api] provides cached widgets and focused custom UI.
Its [parity matrix][pig-parity] lists layout/input limits that need checking
against the runtime selected for game integration.

### Pig Snake (built)

`components/pig-snake/` is an original Go game added to Pigpen: the head is the
PiG pig and every apple adds another pig head, so a herd forms behind the leader. It follows the
Angry Pigs and PiG Runner conventions (explicit command, full-terminal overlay, HUD, pause, strict
high-score file, narrow-terminal handling) and adapts their code and art with credit from PiG
`d86eb93` ([CREDITS](../../components/pig-snake/CREDITS.md)). Pig art is read through one small
interface (`sprites.Source`) so the shared package can replace it. Full mode only: mini mode and the
background-work proof belong to the shared game shell described above. Evidence:
[`port/PORT.md`](../../components/pig-snake/port/PORT.md). `pig-with-batteries` selects it
(bundling registers two commands and nothing else).

### One shared foundation

Create one reviewed `components/pig-play/` Package. Move or extract the canonical
sprite/palette and generic game presentation into a public component, coordinating
with PiG Standard's owner. Until that move is approved, reference the existing
source and record the boundary. Do not leave independently edited sprite copies
in PiG and every game.

The Package should contain:

- The canonical pixel pig and palette, with named idle/run/jump/cheer/hurt poses.
  Preserve the same recognizable pig across all games. Derive scaling/crops from
  this source instead of redrawing it per game.
- Terminal-cell rasterization, width fitting, reduced-motion behavior, and a
  text/limited-color fallback. Keep sprite data independent of SDK UI calls.
- A common mini/full game shell, input focus handling, progress/status display,
  and lifecycle cleanup. Each game owns its simulation state and rules.
- A directly installable sprite preview extension. Developers can also consume
  the Go library and licensed asset files without selecting a game Piglet.

Estimate the shared foundation at **5–8 person-days**, separate from game rows.
Build it once because four requested games already need the same components.
Do not build a general game engine or plugin registry.

### Mode contract

| Mode | Rendering and input | Agent-work invariant |
|---|---|---|
| Footer mini-mode | Small widget adjacent to the footer. Preserve model/context/error status. No focus theft while typing. Explicit game focus or expansion key. | Agent model/tool work continues. Game timers never await the agent or block its handlers. |
| Full-screen | Focused custom UI, with the same game state and a persistent running/waiting/error/finished indicator. | Opening, playing, resizing, losing, winning, and leaving never invoke abort, wait-for-idle, or session switching. |
| Return | Escape closes only the game surface and restores the editor/transcript immediately. | No reload, new session, or cancellation of background work. Completed output is already available. |

Mini-mode Tetris shows a compact board with the pig on the left. Full-screen
Tetris retains the left-side cheering pig. On terminals too narrow for a playable
board and pig, show status and an expand/resize hint instead of corrupting cells.

An approval dialog is a genuine agent wait, not a game-imposed pause. Give it
focus priority, temporarily hiding the game if necessary. Display “waiting for
approval” and restore the game only after the dialog is resolved. A game must
never hold the only focus slot while the agent waits for a hidden question.

Use an owned simulation clock and bounded render snapshots. No blocking IPC,
network, filesystem reads, or full transcript scans on the render loop. Preserve
agent state on game failure. Cleanup detaches timers and input handlers without
cancelling model/tool contexts.

**Required proof before more games:** one repeatable PTY scenario starts a real
PiG session against a deterministic streaming provider and a slow tool. It opens
mini/full modes while both progress, changes size, handles a question, and exits.
Assert continued event/tool progress, no agent abort, intact transcript, and
prompt restoration within 100 ms on the recorded test host. Repeat with a
subprocess extension and a fused Binary. This is a proposed acceptance budget,
not an existing performance claim. If current focus/command dispatch cannot meet
it, request a generic PiG host fix before implementing more games.

### Status: the existing games are in Pigpen (2026-09-29)

Moved from PiG Standard at `d86eb93f217e64b655e9ee6f48c93a9afd107697` (Michael Kinsy, MIT;
credited in each Package). Rows 11 and 13 are done as far as *reuse* goes; the new work in the
mode contract above is not.

| Piece | Package | State |
|---|---|---|
| Sprite, pixel rasterizer, arcade scenery, terminal plumbing | `components/pig-play` (Go module, no extension) | Done. `libraries/{sprite,pixel,arcade,termgame}`, `assets/pig`. The shared foundation the other games (`pig-snake`, later Tetris and Doom) build on. |
| Sprite login and `/sprite` | PiG itself (built in since 0.4.0) | Removed from Pigpen: PiG's built-in login has every sprite `components/pig-login` had and saves the choice in the file the games read. Pigpen-only sprites, if any are drawn later, go through PiG's additive sprite API, not a replacement login. |
| PiG Runner `/runner`, `/pig-runner` | `components/pig-runner` | Done, full-screen only. |
| Angry Pigs `/angry-pigs` | `components/angry-pigs` | Done, full-screen only. |
| Composition | `piglets/pig-games`; also selected by `pig-with-batteries` | Commands only; a Binary of `pig-games` builds and runs. `pig-with-batteries` cannot build a Binary while herdr is a Node extension. |

Evidence per Package is in `port/PORT.md`: every upstream test is a twin (31 runner, 26 Angry
Pigs, 11 login and sprite, 12 pig-play helpers; none skipped), differential traces against the
unmodified originals under PiG, and a mutation check (50 defects, all killed with the layer-1 tests).
One unobservable resource leak was fixed in its own commit (a print-mode command left the
component's ticker goroutine running); it is listed for owner veto.

**Not done, still required by the mode contract:** footer mini-mode, the shared game shell and its
focus rules, and the PTY scenario against a slow tool and a deterministic provider (the terminal
runs so far were with no model, only that the overlay opens, plays, closes and leaves the
transcript intact). "Sprite updates": PiG's history holds no update sprite. The "Update Available"
notice is plain host text (`internal/codingagent/interactive_chat.go`, DIVERGENCES.md) and the
SDK has no hook for it; a sprite there needs a host hook, or an extension that checks for a newer
release itself and shows the pig with `SetHeader`/`Notify` on session start.

### Game licensing

The owner selected an original Go raycaster with PiG's pig artwork. No DOOM
engine, commercial WAD, or third-party game assets are required. Keep names such
as “angry-pigs,” “pig-tetris,” and “pig-doom” subject to release-time trademark
review without reopening the approved original-game design.

## Shared Packages, versions, and installation

PiG's [concept model][pig-concepts] distinguishes Resources, Packages, and
Piglets. A Package distributes Resources. A Piglet selects them. Do not treat a
Piglet as a Package dependency or make one Piglet inherit the entire battery set.

In concrete terms, `components/websearch/` owns the Go implementation once. Users
can install it alone. `pig-with-batteries` selects that same component alongside
subagents and other approved extensions. That is already true of
`components/herdr`, the first battery it selects. Four games select one shared pig asset
and game-UI library, so a sprite fix does not require four source edits.

The benefit is independent installation, testing, upgrades, and attribution.
The cost is explicit dependency versions and composition testing. Updating a
component does not silently change an already built Piglet Binary: rebuild the
composition with the reviewed version. `package.json` is installer metadata here,
not a requirement to use JavaScript or Node. Git distribution is sufficient.

License review is a separate part of this proposal. A Go translation is still a
port and does not erase upstream obligations. For billion-context, the specific
question is carrying its full terms and required product credits, not whether Go
is allowed as the implementation language.

Proposed layout:

```text
components/
  extension-port/
    package.json
    provenance.json
    skills/pigpen-pi-extension-port/SKILL.md
  websearch/
    package.json
    provenance.json
    licenses/
    CREDITS.md
    extensions/websearch/
  subagents/                         same Package shape
  pig-play/
    package.json
    go.mod
    provenance.json
    licenses/
    CREDITS.md
    extensions/pig-sprite/
    libraries/termgame/
    assets/pig/
piglets/
  pig-porter/piglet.yaml
  websearch/piglet.yaml
  subagents/piglet.yaml
  pig-with-batteries/piglet.yaml
  acp/piglet.yaml
  ...
```

Each Piglet retains its catalog metadata and README. Component-owned Skills,
extensions, prompts, and themes stay in their standard directories. Use explicit
`package.json` `pi` membership and additive `pig` fields where supported. Ordinary
libraries/assets are developer resources, not automatically executable extensions.

- Give each component Package its own SemVer. For a Go module rooted at
  `components/pig-play`, use Go-compatible tags such as
  `components/pig-play/v0.1.0`. Piglet tags remain `<piglet>/v<version>`.
- Piglet manifests select Package aliases and named members. Pin Git sources to
  full commits or npm sources to exact versions. Build records retain resolved
  content identities. Do not maintain a second hand-copied dependency list.
- Prefer pinned Git subdirectory sources initially. Add separately published
  scoped npm Packages only after the owner chooses a controlled npm scope.
  Do not assume `@pigpen/*` is owned or available.
- Published Piglets must not use `local:../../components/...`. PiG's remote
  Piglet copy contract confines local resources to the selected Piglet directory.
  Use pinned Package sources for siblings. This is why references, not copies,
  matter in a monorepo.
- Local Piglet validation also rejects parent traversal to sibling Packages.
  Use `npm run stage` and select the generated manifest, not the authored input.
  The source checks reject duplicate authored components, not generated copies.

Future commands, **not installable release claims today**:

```sh
# Local component development, after the component exists:
pig package validate ./components/websearch
pig install ./components/websearch/extensions/websearch --validate-only --json

# Direct Package install from an approved, published commit:
pig install 'git:https://github.com/MichaelKinsy/pigpen.git@<full-commit-sha>#subdirectory=components%2Fwebsearch'

# Separate Piglet selection from the same monorepo:
pig piglet add 'git:https://github.com/MichaelKinsy/pigpen.git@<full-commit-sha>#subdirectory=piglets%2Fwebsearch'
```

The [current Piglet docs][pig-piglets] describe monorepo add, prefixed publish,
and namespace-aware update. Pigpen's CI pins PiG v0.4.0
(`76022638c15e2f68f1cee9c6b8984eee1baba620`), which has them, and its release guard remains
closed. [RELEASE-BLOCKERS.md](../../RELEASE-BLOCKERS.md) records the 2026-09-29 re-check
of that implementation against a 0.3.0 pre-release build and what still blocks publication.
Review the two-Piglet release/update proof against a real release before changing the
pin or lifting the guard.

## Quality gates implemented in Phase 1

Implementation: [`scripts/quality-gates.mjs`](../../scripts/quality-gates.mjs).
It runs locally through `npm run quality`, and as part of `check` and `validate`.
CI also runs its single [CLI scenario](../../scripts/quality-gates.test.mjs).
No write permissions, publishing steps, or signing secrets were added.

| Gate | Enforced now | Limits / later release review |
|---|---|---|
| Skill YAML | Every source `SKILL.md` is parsed as YAML. Reject missing delimiters, duplicate keys, parser warnings/errors, non-mappings, and invalid name/description types. | Generated/dependency trees `.git`, `node_modules`, `.pig-src`, `.pig-bin`, and `dist` are excluded. Release closure needs its own inspection. |
| Skill identity | Require unique `pigpen-<lowercase-hyphenated-name>` names, matching directory names, at most 64 characters. | Prefix prevents common bare names such as `commit` or `research`. It cannot reserve names against unrelated third parties. No upstream compatibility aliases. |
| Duplicate components | Scan conventional `extensions`, `skills`, `prompts`, `themes`, `hooks`, `mcp`, `libraries`, and `assets` under Piglet/Package owners. Reject duplicate names and identical payload trees even when the resource root is renamed. | Ignore legal/readme/dependency metadata when hashing payloads. This is exact-copy lint, not semantic clone detection. Modified copies and unconventional layouts require review. |
| License declaration | Every `piglets/<name>` and `components/<name>` directory needs schema-checked `provenance.json`, authors, a license declaration, and a nonempty local license text. Reject unknown/unlicensed sentinel values. | Declared text cannot establish redistribution rights, validate arbitrary SPDX expressions, or audit dependencies by itself. |
| Port attribution | Ported resources require upstream name, authors, HTTPS source, full Git revision, owned path, license text, and readable credit containing source/author names. Original resources must declare no upstreams. | Exact license matching, extra terms, NOTICE obligations, and undeclared copied code need review. |
| Path safety | Reject source symlinks and escaping metadata paths. | This static check is not a sandbox for extension validation. |

`pig-with-batteries/provenance.json` describes the existing original scaffold.
It retains the repository's MIT license.
No third-party port was imported to satisfy these gates.

The dependency audit found two moderate advisories in the existing validators:
[Ajv `$data` ReDoS][ajv-advisory] and [YAML nesting stack overflow][yaml-advisory].
The new gate parses contributor-controlled YAML, so retaining the affected YAML
version was unsuitable. Ajv and YAML were updated to pinned 8.20.0 and 2.9.1.
The lockfile was regenerated, and `npm audit` now reports zero vulnerabilities.
This is an audit snapshot, not a security certification.

### Phase 1 verification record (before porter implementation)

Base repository commit: `55748b1`. Branch: local `feat/quality-gates`.
Changes are uncommitted and unpushed. No hosted CI run was triggered.

- Node: `v24.14.1`.
- Test-first result: the CLI scenario failed because `quality-gates.mjs` did not
  exist. The implemented CLI subsequently passed all 32 cases. Disabling the
  content-duplicate check made the renamed-copy case fail. Restoring it returned
  the scenario to green.
- Cases cover quoted/folded YAML, unquoted `: `, duplicate keys/names, and invalid
  descriptions. They also cover common names, copied resources, missing licenses
  and credits, mutable revisions, path escapes, and symlinks.
- `npm ci --ignore-scripts`, `npm test`, `npm run check`,
  `node scripts/build-matrix.mjs`, and `npm audit --audit-level=moderate` passed.
- `PIG_BIN=<workspace>/bin/pig npm run validate` passed in a fresh `PIG_HOME`.
  It validated the existing empty `seed-check` extension and scaffold only.
- That Binary reports `0.2.1+0.87.1`, built with Go 1.27.1 from PiG module
  `v0.0.0-20260927210400-ca0e279552c5`. This local run does not claim to execute
  the older CI-pinned validator. No Piglet Binary was built.

SHA-256 identities for the verified local inputs:

| Input | SHA-256 |
|---|---|
| `scripts/quality-gates.mjs` | `34ab6726cc1b0fe865fc7a4a05e1e945eca8a2917fec6d7cb7602ad474f1664b` |
| `scripts/quality-gates.test.mjs` | `537e816ba6dd4b7879cca6a105c6761195c7d8ddebb743b91441a92ca6a1ce27` |
| `package-lock.json` | `d1fb7bb1f2c598d34ce142650fb62460e87ebb6b44d2c312e1bed577ace035ec` |
| `<workspace>/bin/pig` | `15e8109b234bc63cffa612a15d27c0a80af7d1fc63cac421b50ba14eb6e123bb` |

## Neutral wording

Approved GitHub description, retained as a draft for the owner to apply:

> PiG Piglets and reusable components, maintained by Michael Kinsy.

Draft README opening:

> Pigpen is a monorepo of PiG Piglets and reusable components maintained by
> Michael Kinsy. Piglets compose named agents. Component Packages can also be
> installed independently. Planned entries are not installable releases.

The local README, contribution/security guidance, scaffold wording, and generated
catalog no longer claim official status. `official: false` retains the schema's
required Boolean field. `index.json` was regenerated from the authored source.
The GitHub description and deployed site have not been changed.

## Remaining decisions and integration work

1. Done: CI pins PiG v0.4.0, which contains `22755205a`'s fix. Keep the empty-tool-scope
   assertion enabled as a regression guard.
2. Resolve JavaScript workflow compatibility before porting that subagents feature.
   No Node dependency or partial interpreter is approved.
3. Coordinate game Resource ownership before extracting existing PiG Standard code.
4. Fold lessons from the next real port into the Skill (the herdr Go port's are in it).
   Decide whether the Go SDK gets a `pi.events` bridge, `withSession` and Pi's stale-context
   error (RELEASE-BLOCKERS.md, item 6).

The owner approved generated local staging. A new workspace-reference contract
in core is not required for this development workflow.

The recommended porter split, y0usaf Jev foundation and corrections, component
layout, initial battery defaults, and neutral wording are approved. The exact
context dependency license contents still require review before copying.

## Source references

Source READMEs below are pinned to inspected commits where available. Stars and
npm counts are timestamped observations above, not immutable future values.

[jev-y-issue1]: https://github.com/y0usaf/pi-jev/issues/1
[jev-y-issue3]: https://github.com/y0usaf/pi-jev/issues/3
[jev-y-publish]: https://github.com/y0usaf/pi-jev/actions/runs/36156758482
[jev-y-cache]: https://github.com/y0usaf/pi-jev/blob/88e5fb3888948e7065110d47cdf6ac57abb71ba4/src/index.ts#L290-L318
[jev-y-cache-key]: https://github.com/y0usaf/pi-jev/blob/88e5fb3888948e7065110d47cdf6ac57abb71ba4/src/gate.ts#L212-L214
[jev-y-state]: https://github.com/y0usaf/pi-jev/blob/88e5fb3888948e7065110d47cdf6ac57abb71ba4/src/gate.ts#L101-L112
[jev-y-config]: https://github.com/y0usaf/pi-jev/blob/88e5fb3888948e7065110d47cdf6ac57abb71ba4/src/config.ts#L166-L197
[jev-y-next]: https://github.com/y0usaf/pi-jev/blob/88e5fb3888948e7065110d47cdf6ac57abb71ba4/NEXT.md
[jev-t-issue1]: https://github.com/TheoOliveira/pi-jev/issues/1
[jev-t-ci]: https://github.com/TheoOliveira/pi-jev/actions/runs/36005904880
[jev-t-pr19]: https://github.com/TheoOliveira/pi-jev/pull/19
[jev-t-pr20]: https://github.com/TheoOliveira/pi-jev/pull/20
[jev-t-pr21]: https://github.com/TheoOliveira/pi-jev/pull/21
[jev-t-compact]: https://github.com/TheoOliveira/pi-jev/blob/3c6d6c5b8ef3fb583b2d1d71ad04bae07bf616d9/src/compact.ts#L11-L84
[jev-g-issues]: https://github.com/leepokai/jev-guard/issues?q=is%3Aissue
[jev-g-pr5]: https://github.com/leepokai/jev-guard/pull/5
[jev-g-scan]: https://github.com/leepokai/jev-guard/blob/489f528cf19ebdb6954bc2d45652c091f9bccb4a/src/guard.js#L154-L170
[jev-comparison]: https://aiskill.market/blog/pi-jev-vs-pi-typesafe-two-takes-on-one-editor
[porter-source]: https://github.com/MichaelKinsy/PiG/blob/d86eb93f217e64b655e9ee6f48c93a9afd107697/piglets/porter/extensions/pig-porter/extension.go

[gallery]: https://pi.dev/packages
[npm-search]: https://registry.npmjs.org/-/v1/search?text=keywords%3Api-package&size=250&popularity=1.0&quality=0&maintenance=0
[pig-packages]: https://pi-in-go.dev/packages
[web]: https://github.com/nicobailon/pi-web-access/tree/2203e9e2848533c8f01ad9dad196af65092cae93
[web-alt]: https://github.com/ttttmr/pi-web-search
[sub]: https://github.com/nicobailon/pi-subagents/tree/8dc5ce9632f32fdead99a0bf80d543130cd564b0
[tintin]: https://github.com/tintinweb/pi-subagents/tree/e955e29c51b7a6cce37e1108cd2d6c57a77e151c
[interactive-sub]: https://github.com/HazAT/pi-interactive-subagents
[acp]: https://github.com/svkozak/pi-acp/tree/b0581c9c1d675e634234674484247008b03d69b4
[ahp]: https://github.com/Qusic/pi-ahp/tree/4065e98309b03c1d2d916300856c3c21a9c10674
[ahp-spec]: https://github.com/microsoft/agent-host-protocol/tree/296b25e7b698a4a84a0ee5a28d9573e70048a0bf
[other-ahp]: https://github.com/A3S-Lab/AgentHarnessProtocol
[a2a-go]: https://github.com/a2aproject/a2a-go/tree/ebf17c56ef7e63c72883a45454a538bbc0df66b8
[kagent]: https://github.com/kagent-dev/kagent/tree/f5d9b95f5702d77b11ab770033503834cd569942
[kagent-transports]: https://github.com/kagent-dev/kagent/blob/f5d9b95f5702d77b11ab770033503834cd569942/docs/architecture/a2a-transports.md
[kagent-go]: https://github.com/kagent-dev/kagent/blob/f5d9b95f5702d77b11ab770033503834cd569942/go/go.mod
[billion]: https://github.com/ranxianglei/billion-context/tree/86e0b684d58e79a4679aa56fa0b862f516efea6b
[billion-pi]: https://github.com/ranxianglei/billion-context-pi/tree/c12994346ed456e98f352d28a16c63825d2da208
[billion-pi-license]: https://github.com/ranxianglei/billion-context-pi/blob/c12994346ed456e98f352d28a16c63825d2da208/LICENSE
[billion-license]: https://github.com/ranxianglei/billion-context/blob/86e0b684d58e79a4679aa56fa0b862f516efea6b/LICENSE
[kernel-license]: https://github.com/ranxianglei/acp-kernel/blob/3519e081ae49126c791e824de578caaf35d9cd02/LICENSE
[context-mode]: https://github.com/mksglu/context-mode/tree/5d13dc4556a5a5d30eb5ea4653b10a2f9142c8a1
[context-license]: https://github.com/mksglu/context-mode/blob/5d13dc4556a5a5d30eb5ea4653b10a2f9142c8a1/LICENSE
[memory]: https://github.com/jayzeng/pi-memory
[hermes]: https://github.com/chandra447/pi-hermes-memory
[auto-compact]: https://github.com/HenryQW/pi-harness
[context-view]: https://github.com/dimk90/pi-context-view
[context]: https://github.com/ttttmr/pi-context
[mcp]: https://github.com/nicobailon/pi-mcp-adapter/tree/3ce9716fe6982dfafdea947b316521acb1ebbc73
[rpiv]: https://github.com/juicesharp/rpiv-mono
[lens]: https://github.com/apmantza/pi-lens/tree/feae3cf1486901ce3e9c3a8b2fbff122fe5e59c4
[observe]: https://github.com/langfuse/pi-observability-plugin
[omp]: https://github.com/can1357/oh-my-pi/tree/f12a6d769d236e3950312fff3d1e2b3df5b6ea52
[typesafe]: https://docs.typesafe.ai
[jev-y]: https://github.com/y0usaf/pi-jev/tree/88e5fb3888948e7065110d47cdf6ac57abb71ba4
[jev-t]: https://github.com/TheoOliveira/pi-jev/tree/3c6d6c5b8ef3fb583b2d1d71ad04bae07bf616d9
[jev-guard]: https://github.com/leepokai/jev-guard/tree/489f528cf19ebdb6954bc2d45652c091f9bccb4a
[jev-unknown]: https://registry.npmjs.org/pi-jev-guard/0.7.2
[hn-omp]: https://news.ycombinator.com/item?id=48994611
[hn-api]: https://hn.algolia.com/api/v1/items/48994611
[x-sub]: https://x.com/nicopreme/status/2085436528952827981
[fx-sub]: https://api.fxtwitter.com/status/2085436528952827981
[reddit]: https://www.reddit.com/r/PiCodingAgent/comments/1t41thp/my_powerful_pi_agent_setup/
[porter]: https://github.com/MichaelKinsy/PiG/blob/d86eb93f217e64b655e9ee6f48c93a9afd107697/piglets/porter/README.md
[porter-skill]: https://github.com/MichaelKinsy/PiG/blob/d86eb93f217e64b655e9ee6f48c93a9afd107697/piglets/porter/skills/pig-porter/SKILL.md
[runner]: https://github.com/MichaelKinsy/PiG/tree/d86eb93f217e64b655e9ee6f48c93a9afd107697/piglets/standard/extensions/pigrunner
[pig-art]: https://github.com/MichaelKinsy/PiG/tree/d86eb93f217e64b655e9ee6f48c93a9afd107697/piglets/standard/extensions/piglogin
[pig-api]: https://github.com/MichaelKinsy/PiG/blob/d86eb93f217e64b655e9ee6f48c93a9afd107697/internal/pigdocs/content/extension-api.md
[pig-parity]: https://github.com/MichaelKinsy/PiG/blob/d86eb93f217e64b655e9ee6f48c93a9afd107697/docs/extension-api-parity.md
[pig-concepts]: https://github.com/MichaelKinsy/PiG/blob/d86eb93f217e64b655e9ee6f48c93a9afd107697/internal/pigdocs/content/concepts.md
[pig-piglets]: https://github.com/MichaelKinsy/PiG/blob/d86eb93f217e64b655e9ee6f48c93a9afd107697/internal/pigdocs/content/piglets.md
[ajv-advisory]: https://github.com/advisories/GHSA-2g4f-4pwh-qvx6
[yaml-advisory]: https://github.com/advisories/GHSA-48c2-rrv3-qjmp
[d-web]: https://api.npmjs.org/downloads/point/2026-08-28:2026-09-26/pi-web-access
[d-web-alt]: https://api.npmjs.org/downloads/point/2026-08-28:2026-09-26/pi-web-search
[d-sub]: https://api.npmjs.org/downloads/point/2026-08-28:2026-09-26/pi-subagents
[d-tintin]: https://api.npmjs.org/downloads/point/2026-08-28:2026-09-26/@tintinweb%2Fpi-subagents
[d-acp]: https://api.npmjs.org/downloads/point/2026-08-28:2026-09-26/pi-acp
[d-ahp]: https://api.npmjs.org/downloads/point/2026-08-28:2026-09-26/pi-ahp
[d-billion]: https://api.npmjs.org/downloads/point/2026-08-28:2026-09-26/billion-context
[d-billion-pi]: https://api.npmjs.org/downloads/point/2026-08-28:2026-09-26/billion-context-pi
[d-context-mode]: https://api.npmjs.org/downloads/point/2026-08-28:2026-09-26/context-mode
[d-memory]: https://api.npmjs.org/downloads/point/2026-08-28:2026-09-26/pi-memory
[d-hermes]: https://api.npmjs.org/downloads/point/2026-08-28:2026-09-26/pi-hermes-memory
[d-auto-compact]: https://api.npmjs.org/downloads/point/2026-08-28:2026-09-26/@henryqw%2Fpi-auto-compact
[d-context-view]: https://api.npmjs.org/downloads/point/2026-08-28:2026-09-26/pi-context-view
[d-context]: https://api.npmjs.org/downloads/point/2026-08-28:2026-09-26/pi-context
[d-mcp]: https://api.npmjs.org/downloads/point/2026-08-28:2026-09-26/pi-mcp-adapter
[d-ask]: https://api.npmjs.org/downloads/point/2026-08-28:2026-09-26/@juicesharp%2Frpiv-ask-user-question
[d-lens]: https://api.npmjs.org/downloads/point/2026-08-28:2026-09-26/pi-lens
[d-observe]: https://api.npmjs.org/downloads/point/2026-08-28:2026-09-26/@langfuse%2Fpi-observability-plugin
[d-omp]: https://api.npmjs.org/downloads/point/2026-08-28:2026-09-26/@oh-my-pi%2Fpi-coding-agent
[d-jev-y]: https://api.npmjs.org/downloads/point/2026-08-28:2026-09-26/@y0usaf%2Fpi-jev
[d-jev-t]: https://api.npmjs.org/downloads/point/2026-08-28:2026-09-26/pi-jev
[d-jev-guard]: https://api.npmjs.org/downloads/point/2026-08-28:2026-09-26/jev-guard
[d-jev-unknown]: https://api.npmjs.org/downloads/point/2026-08-28:2026-09-26/pi-jev-guard
