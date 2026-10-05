# Open issue and pull request resolution plan

Assessment date: 2026-09-22. Repository: [tmonk/pi-goal-x](https://github.com/tmonk/pi-goal-x).

This plan covers all **six open issues and five open pull requests**, checked with `gh`, and uses closed issues, merged changes, current source, and existing local specifications to assess project fit. It recommends implementation and disposition; it does not close issues, update contributor branches, or authorize a release by itself.

## Recommendation

Fix the runtime and compatibility problems first, finish budget recovery through the existing tweak flow, then integrate the two caching fixes together with clearer model-facing token accounting. Accept independent control of the unfocused prompt and capability-based questionnaire routing. Implement configurable goal storage as a separate, deliberately scoped feature after the reliability work.

All six issues identify useful work. They do **not** justify accepting every proposed implementation or changing every default. Four open PRs are candidates for inclusion after changes and validation; #74 needs substantial adaptation to the existing project design. None should merge solely on the contributor's reported passing tests.

Priority definitions: P1 is a runtime, integration, or material reliability problem for the next stabilization cycle; P2 is a valuable UX or storage improvement that must not delay the stabilization release. There is no independently established P0 outage in this assessment.

## 1. How necessity is assessed

For each item, check the following before implementing it:

1. Does the current default branch still exhibit the reported code path? Distinguish a code defect, an observed symptom, a compatibility declaration, and a feature preference.
2. Does it advance persistent goals, user-owned focus, reliable autonomous continuation, recoverable state, or useful goal interaction?
3. Has it already been addressed by a merged change or unfinished local implementation? Preserve the newer behavior rather than reintroducing an older contract.
4. Is the smallest effective fix in this extension, or does part belong to Pi or a provider adapter?
5. What does it cost in new commands, settings, prompt size, filesystem complexity, host assumptions, and maintenance?
6. Can an observable acceptance test distinguish the old failure from the intended result, including the relevant host/provider boundary?

Disposition choices are accept, adapt, defer, or reject/supersede. An accepted problem is not automatic acceptance of its suggested patch. Close only when the accepted scope has evidence of resolution and remaining upstream work is explicitly tracked.

## 2. Complete open-issue assessment

| Issue | Necessity and evidence | Decision | Priority / implementation |
| --- | --- | --- | --- |
| [#78 — stale widget context after reload](https://github.com/tmonk/pi-goal-x/issues/78) | Both focused and unfocused widget factories on current main close over `ctx` in deferred settings/ledger getters. This is a credible lifecycle defect. The reporter explicitly did not reproduce a standalone goal-x crash, so the title overstates the available reproduction evidence. | Accept the minimal lifetime fix and add a regression that invalidates the original context before rendering. Investigate queued UI callbacks separately. | P1; small new fix, coordinated with #77. |
| [#77 — Pi peer range excludes 0.86](https://github.com/tmonk/pi-goal-x/issues/77) | Main declares `>=0.83.0 <0.85.0`; the reported 0.85/0.86 hosts fall outside it. The metadata mismatch is confirmed. Functional failure and the claimed dependency-pruning causal chain are not established by this assessment. | Accept compatibility validation and a tested range update. Do not replace the range with `*` merely because other extensions do. | P1; host compatibility work and package/README update. |
| [#76 — cumulative usage looks like context exhaustion](https://github.com/tmonk/pi-goal-x/issues/76) | Main injects `Usage: ... tokens` using lifetime goal usage. Similar ambiguous totals reach tools, compaction and the auditor. The presentation ambiguity is confirmed; model behavior and cost effects are reporter observations. | Accept presentation changes across model-visible surfaces. Preserve actual accounting, enforcement and human-facing totals. | P1; integrate with budget recovery and #79's state/counter split. |
| [#59 — Responses optional arguments and budget recovery](https://github.com/tmonk/pi-goal-x/issues/59) | Creation feedback and recovery are extension concerns; reported wire-level optionality normalization is a separate adapter/provider concern. Local unfinished work already implements an existing-flow alternative to #74. | Accept visible budgets, confirmed set/remove through `/goal-tweak`, warning coalescing and provider-boundary coverage. Keep upstream normalization explicitly tracked. | P1; adapt #74 to the local specification or supersede it with the integrated implementation. |
| [#72 — unwanted unfocused prompt](https://github.com/tmonk/pi-goal-x/issues/72) | Main deliberately makes `hideUnfocusedBanner` UI-only but injects a directive reminder on every unfocused request. This is a valid missing user control, not a breach of the original setting's documented contract. | Accept #75's independent `hideUnfocusedPrompt`, default false, plus grammar correction. Preserve focus ownership and stale-checkpoint rejection. | P2; small independent UX change. |
| [#73 — configurable goals root](https://github.com/tmonk/pi-goal-x/issues/73) | Cwd-based storage fragments pools across worktrees. Paths also occur in ledger, locks, snapshots, recovery and debug files, so changing two constants is insufficient. | Accept an explicit root override and worktree-sharing documentation. Defer automatic global storage, project-key inference and migration to a later design. Keep the existing default. | P2; separate storage feature after stabilization. |

## 3. Complete open-PR assessment and required updates

### #66 — capable RPC hosts should get the rich questionnaire

[PR #66](https://github.com/tmonk/pi-goal-x/pull/66): **include after targeted cleanup and compatibility checks**.

The capability-based approach fits the project better than treating every RPC host as unable to render a TUI. It extends the fallback work from #45/#47/#52 without making native select/input obsolete.

Required updates:

- Replace the top-level fork changelog and downstream preset pin with a normal upstream `Unreleased` entry.
- Preserve rich-dialog cancellation as a defined cancelled result; never interpret Escape or failed rendering as permission to create a goal.
- Verify missing `custom`, unavailable TUI, synchronous throw, asynchronous rejection, successful rich rendering, and primitive fallback. Preserve actionable errors when a disconnected host cannot render either route.
- Keep questionnaire, proposal confirmation and task approval behavior consistent where they share capabilities; do not expand this into a general UI rewrite.
- Re-run the supported Linux test environment. The author's “same 14 Windows failures as baseline” is useful comparison evidence, not a passing upstream check.

Exit: rich RPC host gets the questionnaire and confirmation; primitive RPC host remains usable; cancellation leaves the draft intact; no silent confirmation.

### #71 — cache relocation past effort-only messages

[PR #71](https://github.com/tmonk/pi-goal-x/pull/71): **include early as a focused fix**.

Current main reads `messages.at(-1)` and therefore declines relocation when a trailing effort-only system message follows the live state. The patch targets that specific failure.

Required updates:

- Keep content-bearing trailing messages from other extensions untouched; validate the last-content search against supported host payloads.
- Preserve existing marker ownership, TTL, valid placement, and tool pairing for Anthropic and Bedrock.
- Correct the PR/spec statement that an ephemeral tail automatically preserves implicit prefix growth. #79 demonstrates the advancing-history case that statement overlooks. Describe #71 as fixing explicit-marker placement only.
- Add the trailing-effort cases to the combined #71/#79 suite so a later rewrite cannot erase this fix.

Exit: marker moves to eligible history with trailing effort metadata present, and an unrelated content-bearing suffix is unchanged.

### #74 — visible budgets and recovery

[PR #74](https://github.com/tmonk/pi-goal-x/pull/74): **adapt substantially; do not merge as written**.

Its visibility, warning and provider-boundary work is useful. Its new `/goal-budget` command and `set_goal_budget` tool conflict with the existing local [budget recovery product specification](../2026-09-16-budget-visibility-and-recovery/PRODUCT.md), which explicitly requires no new command, tool or setting. Its always-paused recovery semantics also differ from that specification's confirmed, scheduler-controlled recovery.

Required updates:

- Use the local `/goal-tweak` confirmation design as the integration baseline. A positive safe integer sets the lifetime budget, explicit null removes it, omission retains the current tweak budget, and an unlimited new goal remains unlimited.
- Preserve ID, objective, tasks, evidence, usage and consumed run allowance. Show old/proposed limits before confirmation and persisted effective limits afterwards.
- Reject stale confirmation after focus/revision changes; cancelled or failed proposals must not mutate anything.
- Recover only through the existing scheduler admission rules. A still-exhausted limit remains stopped; foreign ownership, interrupted dispatch and exhausted allowance keep their recovery requirements. Lowering a limit below current usage must stop active work promptly.
- Retain and adapt useful contributor tests and warning coalescing. Remove command/tool registration and surface-count churn from the revised diff.
- Recheck the Responses workaround on each newly supported SDK. Do not reinterpret positive budgets as unlimited or silently resolve conflicting task-update forms.

Preferred disposition is a reworked #74 if contribution permissions allow it. Otherwise ship one maintainer replacement with clear attribution and supersede #74 after the replacement is validated. Do not ship both implementations. The extension's recovery UX can be complete while the upstream normalization problem remains open and linked.

### #75 — independent unfocused prompt control

[PR #75](https://github.com/tmonk/pi-goal-x/pull/75), currently draft: **include with its two-setting design**.

The separate setting preserves the established UI-only meaning of `hideUnfocusedBanner` and allows users to choose either surface independently. One-time injection is not needed to satisfy this request and adds lifecycle state.

Required updates:

- Record the independent-setting decision in its spec and remove the unresolved design question when preparing it for review.
- Preserve project > global > default precedence, live setting changes, `/goal-status` provenance and the settings menu.
- Add combined tests with #79: hiding/unfocusing must remove previously retained unfocused instructions, not only suppress newly appended text.
- Verify focused active, paused, blocked and budget-limited guidance remains intact, as does rejection of stale checkpoints.
- Document interaction with intentional `autoSelectSingleGoal`: the prompt setting does not disable automatic focus if that separate behavior is enabled.

Exit: repeated unrelated requests have no reminder when opted out; no goal, focus or ledger mutations occur merely from hiding it.

### #79 — advancing implicit-cache prefixes

[PR #79](https://github.com/tmonk/pi-goal-x/pull/79): **accept the problem; hold inclusion until integration and performance conditions pass**.

Appending a request-only tail does not alone make request N a prefix of N+1: an assistant response occupies the former tail position in subsequent history. The PR's retained-tail approach addresses a real gap in #68. It also adds meaningful complexity: whole-history hashing, in-memory session tracking, transient replay and periodic resets.

Required updates:

- Rebase onto #71 and preserve its last-content lookup. The current #79 head still uses `messages.at(-1)`. A direct offline probe confirms that it leaves the marker on live state when an effort-only message follows.
- Treat changes to the token-budget limit and autonomous-run limit as policy changes that invalidate retained instructions. Keep only usage/run consumption and clearly historical observations in append-only counters. A direct retention-layer probe shows an omitted new budget line leaves the old line visible; removal-by-omission is unsafe without explicit supersession/reset behavior.
- Incorporate #76's wording and telemetry without moving dynamic values into the stable policy block. Ensure old context readings are visibly historical and cannot masquerade as current occupancy.
- Bound both retained content and auxiliary state. The retention class limits sessions to 16 and tails to 32, but the separate `liveTransients` map is not governed by that eviction mechanism. Coordinate eviction/cleanup and verify session identity fallbacks do not mix separate sessions sharing a cwd.
- Measure repeated full-history hashing on long histories, including large tool outputs and image-bearing messages. A 32-tail count bounds neither total history-hashing cost nor arbitrarily large individual state blocks.
- Prove compaction, session switching, aborted/retried requests, model/provider switches, state edits, settings toggles and parallel tool results preserve safety. Record resets rather than claiming universal prefix continuity.
- Assess whether retention should apply to every provider. Keep the provider-agnostic design only if the explicit-cache tests and measured overhead justify it; otherwise scope it to paths that benefit without weakening correctness.
- Update provider captures and the context baseline with a reviewed rationale. Do not use three serialized requests as evidence of live provider hit rates or net savings.

Exit: #71 and #79 pass together; no obsolete policy survives a reset/removal; message pairing is valid; growth and hot-path costs meet recorded bounds. If this takes longer, release the independent small fixes first.

## 4. Execution order and release boundaries

| Stage | Work | Dependency / completion condition |
| --- | --- | --- |
| 0 — establish integration baseline | Preserve existing dirty work and compare it with remote main; review fork CI jobs before allowing execution. | Local HEAD is `5a7c4cd` / 0.31.5, remote main is `8b077aa` / 0.31.6. Start implementation from current remote main in isolation; do not reset this checkout. |
| 1 — host and lifecycle stabilization | #78; #77 compatibility matrix; #71; #66 cleanup and host checks. | Widen peer bounds only after target host tests pass. #71 is a prerequisite for the final #79 version. |
| 2 — clear and recoverable control | Integrate local #59 work / adapt #74; #76 presentation; #75 setting. | Preserve existing product decisions. Reconcile shared edits in state, events, prompts, command reporting and tests. |
| 3 — caching integration | Rework #79 on the accepted Stage 1/2 behavior; run combined provider/lifecycle/performance gates. | Stale-limit removal, effort-marker handling and bounded memory/cost are merge conditions. |
| Stabilization release | Release validated fixes from Stages 1–3, or Stages 1–2 plus #71 if #79 needs longer. | Package, changelog, peer range, docs, CI and installed smoke tests agree. Choose the next version from actual release state at execution time. |
| 4 — storage feature | Implement the scoped #73 override and shared-root safety. | Separate feature release; unchanged default location and no automatic migration. |
| 5 — preventive hardening | Add the focused recurring regression gates below. | Integrate directly relevant tests with each patch; schedule broader measurement work after stabilization. |

Use one coherent PR per problem where possible. #59 and #76 share terminology but differ in mutation vs presentation; review their semantics separately. #72 does not depend on #73. Do not hold a reload fix or supported-host update for the storage feature.

## 5. Look forward: fixes suggested by the history

| Evidence from past/current work | Preventive change | Scope and acceptance |
| --- | --- | --- |
| #30 → #32, #67 → #68, now #71/#79 | Make advancing multi-request sequences part of cache/context validation. | Check actual SDK serialization for growth, stable system/tools, changed state, retries and compaction; include long runs past retention eviction. Keep persisted-history growth separate from request-only growth. |
| #45/#47/#52/#66 and #24/#78 | Establish a small host-capability and lifecycle test matrix. | Cover terminal, primitive RPC, rich RPC, headless, unavailable UI, reload and session replacement. Deferred callbacks must not retain invalid host contexts. |
| #59/#74/#76 | Define distinct model-facing concepts for cumulative usage, optional lifetime budget, current context occupancy and run allowance. | Shared formatting and structured diagnostics; unknown telemetry stays unknown; budget mutation and presentation remain separate. Test omitted/null/value semantics at provider boundaries. |
| #53/#55 → #58 → #63/#64 | Preserve autonomy policy as a deliberate user choice. | Keep implicit continuation as default and strict contracts opt-in. Add lifecycle regression scenarios, diagnostics and existing-limit guidance; do not silently restore mandatory contracts or a new default stop heuristic. |
| #48 and #79 | Treat tool-call/result adjacency as a protocol invariant. | Test multiple parallel calls, delayed results, audit messages, retained tails and other-extension suffixes through serializers, not only message-array unit tests. |
| #49, #73 and scheduler ownership work | Exercise multiple sessions and worktrees against one pool. | Unique ownership/admission; stale revisions rejected; shared locks and refresh; no cwd-based cross-session cache confusion. |
| Closed #70 describes nonconvergent task reviews and deleted runtime artifacts | First audit what equivalent behavior exists on current main; then isolate any reproducible gaps. | Protect runtime artifacts from being presented as cleanup targets; distinguish environment failure from failed task evidence; carry relevant prior auditor findings. Do not revive its broad single-task gate or fixed three-rejection stop automatically. No closure rationale was available in PR comments. |
| #77 and single-SDK Linux CI | Add a bounded compatibility gate tied to declared support. | Test supported minimum and newly admitted SDK minors, supported Node floor and primary CI Node. Catch metadata/code drift before releases. |
| Closed #60/#61 | Respect the explicit maintenance boundary on test portability. | Do not reopen a full Windows test-suite port or OS CI expansion. Target a platform-specific runtime bug when demonstrated, with narrow regression coverage. |

Prefer extending existing context, provider, health and benchmark tools over adding new production commands. A useful diagnostic snapshot would show the effective root, host version, current context availability, cumulative usage, budget/allowance state and cache-reset reason without recording private prompt content. Each field should be added only where it helps reproduce these failures.

## 6. Definition of done

- Every accepted issue has an implementation, regression evidence, user-visible documentation where needed, and an explicit disposition of any remaining upstream portion.
- Every included PR is tested at its final integrated revision, not only its original contributor head. Current GitHub `MERGEABLE` status is not test approval.
- No duplicate budget interface, unreviewed peer wildcard, surprise storage migration, automatic focus change, or default autonomy-policy reversal is introduced.
- Deterministic tests substantiate payload construction and state transitions. Live cache savings, standalone reload crashes and third-party provider behavior are claimed only when separately observed.
- The release records the included fixes and compatibility range. Post-release issue/PR comments should identify the version, validation and remaining limitations; publishing those messages is a subsequent maintainer action.

Implementation details and validation gates are in [TECH.md](TECH.md). The assessment evidence and limits are in [MILESTONES.md](MILESTONES.md) and [EVIDENCE.md](EVIDENCE.md).

## Implementation steering — 2026-09-22

Implement the accepted scope in one reviewable PR, including the scoped storage override. Add no commands or tools. Reuse the existing tweak, refresh, settings and status surfaces; no generic diagnostic framework or speculative feature expansion. Keep implicit continuation defaults unchanged. Prefer compact shared helpers and bound all retained context and memory. Measure hot-path speed and prompt overhead against current main, and preserve caching correctness over claimed savings. Incorporate useful open-PR contributions with attribution; do not merge or close those PRs during implementation. Release publication remains outside this PR.

Compatibility refresh: npm now lists Pi 0.87.0. Validate it alongside reported 0.86.0 and retain a bounded peer range through the tested current minor, rather than shipping another already-outdated ceiling. The extension Node floor stays unchanged; newer Pi hosts have their own >=22.19 requirement.

Forward-looking completeness follow-up: verify every preventive row against implementation and executable coverage in this PR. Treat an unsafe retained tool boundary as a full cache reset, preserving exactly one current policy block. Add missing lifecycle/serializer, shared-pool conflict and large tool/image-history regression coverage without extending commands, tools, defaults or model prompts.
