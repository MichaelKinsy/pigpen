# Upstream Reference: `internal/ratchet`

Port of omp's `ratchet` eval hillclimb state machine, eval prelude, and `/ratchet` slash command (https://github.com/can1357/oh-my-pi, MIT, release tag `v19.1.1`, commit `31ffee513f`).

## Upstream files followed (`v19.1.1`)

- `packages/coding-agent/src/ratchet/ratchet.ts` → `ratchet.go`
  - Flow path validation (`^[a-z0-9][a-z0-9_-]{0,63}$`), workspace-escape rejection (`normalizeRepoPath`), `_state.json` persistence (`LoadState`, `RequireState`, `SaveState`).
  - `InitFlow` (cases/harness/change/off_limits overlap & existence checks, state preservation on re-init), `ApplyPlan` (goal defaults, target-in-hold guard, post-baseline reps shrink guard, stop rule merge preserving round cap, price override merge), `SplitCases` (mulberry32 PRNG matching upstream's exact sequence, tag-stratified Fisher-Yates shuffle, empty-side rebalancing, post-baseline freeze).
  - `StageDigest` / `GetApprovalStatus` / `RequireApprovals` (SHA-256 over sorted paths + NUL-delimited file contents for `inputs`/`grader`; canonical JSON SHA-256 over goal/reps/stop/command/change_paths/off_limits/train_ids/test_ids/sorted prices for `plan`).
  - JSONL parsing (`results.jsonl`, optional `errors.jsonl`), `NormalizeUsage` (Anthropic, OpenAI with `prompt_tokens_details.cached_tokens` subtraction, and catalog-shaped usage), `rowCost` (`cost_usd` or `model` × `usage` + `judge_model` × `judge_usage`), `rowMetrics` (truncated rows excluded from `grade`, bool/number/dict grades, numeric row fields).
  - `CheckVariant` (variant regex, current approvals, `baseline_inputs_sha` drift check, sequence/rerun check, `{variant}`/`{flow_dir}` command substitution).
  - `GateVariant` (duplicate slot rejection, completeness check, baseline errored-only rejection, held-out trace leakage rejection, stray ID warnings, unpriced model warnings, guardrail measurability/pairing checks, two-sided 95% Student-t paired deltas for df 1..30 + 1.96 normal fallback, baseline ceiling & train/test mismatch warnings, keep/revert/rerun decisions, plateau/done tracking, `status.md` rendering via `RenderStatusTable`).
  - `GetTrainView` (train-only rows, train-only trace paths, per-case target score history across gated rounds).
- `packages/coding-agent/src/ratchet/prelude-definition.ts` → `prelude.go`
  - `PreludeDefinition` (`Name: "ratchet"`, `Exports: ["ratchet"]`, `Enabled` gated by `ratchet.enabled` default `false`, `ApprovalFor: RatchetApproval` returning `"read"` for `status`/`check`/`train` and `"write"` for all mutating/invalid calls, `Status: DescribeCall`).
  - Strict parameter validation matching upstream's `paramsSchema` across all 8 actions (`init`, `plan`, `split`, `approve`, `check`, `gate`, `train`, `status`).
  - Per-flow in-process serialization lock (`withFlowLock`) so concurrent calls (e.g. concurrent stage approvals) never clobber `_state.json`, plus post-review SHA re-check before recording an approval.
  - `catalogPriceLookup` querying `ctx.ModelRegistry().GetAll()` with exact upstream ambiguity/zero-cost exclusion rules.
  - `askApproval` interactive dialog via `ctx.Select` / `ctx.InputWithoutPlaceholder` (rejecting headless sessions with upstream's exact error message).
- `packages/coding-agent/src/ratchet/prelude.js` → `assets/prelude.js` (embedded verbatim)
- `packages/coding-agent/src/ratchet/prelude.py` → `assets/prelude.py` (embedded verbatim)
- `packages/coding-agent/src/prompts/tools/ratchet.md` → `assets/ratchet.md` (embedded verbatim; served at `help://eval/ratchet` when enabled)
- `packages/coding-agent/src/prompts/ratchet-kickoff.md` → `assets/ratchet-kickoff.md` (embedded verbatim)
- `packages/coding-agent/src/slash-commands/builtin-modes.ts:203-222, 691-724` → `register.go` (`PrepareRatchet`, `RenderKickoff`, `/ratchet` slash command registration)
- `packages/coding-agent/src/tools/settings.ts:720-730` → `SettingRatchetEnabled = "ratchet.enabled"` (already defined in `internal/settings/schema.json` with default `false`)

## Settings

| Key | Default | Honoured | Notes |
|---|---|---|---|
| `ratchet.enabled` | `false` | Yes (via `Setting`) | Gates the `ratchet` eval prelude, `<namespaces>` listing, and `help://eval/ratchet`. `/ratchet` sets it via `SetSessionOverride` (runtime override, never persisted to disk) for the current session only. |

## Not Ported

| Feature | Upstream file | Why not ported |
|---|---|---|
| Rich `ui.askDialog` overlay (`kind: "chat"`, `timedOut`, per-option descriptions, `note`) | `packages/coding-agent/src/ratchet/prelude-definition.ts:152-177` | PiG Go SDK v0.8.0 has no `askDialog` UI primitive; `askApproval` uses upstream's `ui.select` / `ui.input` fallback path (`prelude-definition.ts:178-183`) with `Approve` / `Revise` / `Abort` choices and `"What should change?"` follow-up input (matching upstream's exact fallback contract; `PreludeOptions.Prompter` is exposed for tests and future native overlays). |
| Time-based / long-context tiered catalog cost calculation (`calculateUsageCost` `timeBased` / `longContext` / `cttl`) | `packages/catalog/src/models.ts:203-249` via `ratchet.ts:574` | `NormalizeUsage` never populates `cttl` or `orchestration`, and PiG's SDK model registry exposes flat per-million-token `cost.{input,output,cacheRead,cacheWrite}` rates. Flat rate pricing matches upstream for all standard models; scheduled peak/off-peak multipliers and long-context tier switches are not evaluated. |
| Mid-session base system prompt rebuild & hidden prelude-added notice on `ratchet.enabled` flip | `packages/coding-agent/src/session/agent-session.ts:3254, 3326-3348` | PiG extension SDK does not expose an API to rebuild the host's cached base system prompt mid-session. Enabling `ratchet.enabled` via `/ratchet` immediately activates the prelude in `eval.EnabledPreludes(ctx)` (so Python/JS kernels sync `ratchet`, the bridge dispatches calls, and `help://eval/ratchet` resolves on the very next turn), and `/ratchet`'s kickoff message explicitly instructs the model to read `help://eval/ratchet` and use `ratchet(flow)`. |
| ACP `/ratchet` return-value slash command adapter (`executeAcpBuiltinSlashCommand` returning `{ prompt }` / `{ consumed, failed }`) and attached-image forwarding on TUI slash submission | `packages/coding-agent/src/slash-commands/builtin-modes.ts:697-701, 712-722`, `acp-builtins.ts` | PiG's `sdk.Extension.RegisterCommand` handler takes `(ctx, args string) error` with no ACP return-value channel or attached-image argument; `Register` submits the kickoff via `ctx.SendMessage("ratchet-kickoff", ..., TriggerTurn: true, DeliverAs: "followUp")` and surfaces validation errors via `ctx.Notify(..., "warning")`. `PrepareRatchet` is exported directly so callers/tests can inspect `{Kickoff, Error}`. |
| Multi-line / concatenated JSON objects in `parseJSONL` | `packages/coding-agent/src/ratchet/ratchet.ts:465-471` (`Bun.JSONL.parse`) | `parseJSONL` fails closed per line (one JSON object per non-blank line; trailing tokens on a line return a `RatchetError`), whereas `Bun.JSONL.parse` accepts concatenated whitespace-separated values on a single line. Standard runner output writes one JSON object per line. |
| `train(variant)` accepts any variant directory name (matching upstream) | `packages/coding-agent/src/ratchet/ratchet.ts:951-960` | Like upstream, `trainView` does not restrict `variant` with `VARIANT_NAME`; `readVariant` joins `filepath.Join(dir, variant, "results.jsonl")` and only exposes rows/traces matching `state.TrainIDs` (and `GateVariant` / `CheckVariant` strictly enforce `^(baseline\|v[1-9][0-9]*)$`). |
