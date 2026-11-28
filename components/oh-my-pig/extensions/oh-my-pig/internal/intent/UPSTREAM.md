# internal/intent — Upstream Mapping

Upstream pin: **omp `v19.1.1`** (`31ffee513f`).

## Upstream Files & Lines Followed

- `packages/wire/src/index.ts:412-418` — `INTENT_FIELD = "i"` (`intent.Field`), `toolIntentLabel` (`intent.Label`).
- `packages/agent/src/agent-loop.ts:987-1072` — `DEFAULT_INTENT_FIELD_DESCRIPTION = "concise intent"`, `INTENT_SCHEMA_UNION_KEYS = ["anyOf", "oneOf"]`, `injectIntentIntoSchema` (`intent.InjectSchema`: `i` placed first via `*sdk.OrderedObject`, per-branch injection for pure `anyOf`/`oneOf` roots, existing `i` reordering / required promotion, bare injection when description is empty).
- `packages/agent/src/agent-loop.ts:1083-1135` — `normalizeTools` (`PI_NO_INTENT=1`, `PI_INTENT_DESC`, `resolveIntentMode`, skipping injection when mode is `"omit"` or `schemaDefinesProperty` is true), `extractIntent` (`intent.Extract` / `intent.Normalize`: trims whitespace and strips trailing periods).
- `packages/agent/src/agent-loop.ts:3198-3226` — `prepareToolCallDispatch` (schema-owned `i` is kept as a tool argument and never stripped as harness intent; missing/malformed `i` does not block tool execution; stray `i` on omit/function-intent tools is stripped before validation; fallback to tool's `intent(args)` function with error swallowing).
- `packages/agent/src/types.ts:1240-1247` — `AgentTool.intent` (`"omit" | "optional" | "require" | ((args) => string | undefined)`).
- `packages/ai/src/utils/schema/json-schema-validator.ts:56,168-326` — `MAX_REF_DEPTH = 64`, `resolveLocalRef` (objects and arrays), `schemaDefinesProperty` (`intent.SchemaDefinesProperty`: walks `properties` with immediate return, `required`, `const`, `enum`, `propertyNames`, `patternProperties`, `additionalProperties`/`unevaluatedProperties` objects, `anyOf`/`oneOf`/`allOf`, `if`/`then`/`else`, `not` except presence-only `not: { required: [key] }`, `dependentSchemas`/`dependentRequired`/`dependencies`, local `$ref`).
- `packages/coding-agent/src/tools/settings.ts:993-1005` — `cfgToolsIntentTracing` (`tools.intentTracing`, default `true`, env `PI_INTENT_TRACING`).
- `packages/coding-agent/src/sdk.ts:492-499` and `packages/utils/src/env.ts:493-505` — `intentTracingActive` and `isInteractiveHost` (`intent.DefaultEnabled`: configured setting wins; when unset, on only in interactive TUI and inherited by child subagents via `ParentSession`; off in print, JSON, RPC, ACP, SDK).
- `packages/coding-agent/src/tools/wait.ts:321` — `wait`: `intent = "optional"`.
- `packages/coding-agent/src/tools/resolve.ts:342,359,377` — `resolve`, `reject`, `propose`: `intent = "omit"`.
- `packages/coding-agent/src/tools/report-tool-issue.ts:712` — `report_issue`: `intent = "omit"`.
- `packages/coding-agent/src/tools/think.ts:59` — `think`: `intent = "omit"`.
- `packages/coding-agent/src/tools/yield.ts:318` — `yield`: `intent = "omit"`.
- `packages/coding-agent/src/goals/tools/goal-tool.ts:71` — `goal`: `intent = "omit"`.
- `packages/coding-agent/src/advisor/advise-tool.ts:210` — `advise`: `intent = "omit"`.
- `packages/coding-agent/src/tools/eval.ts:283-287,386-390` — `eval`: dynamic `intent(args)` (`title || "running " + formatEvalInputLanguage(language)` -> mode `"omit"`).
- `packages/coding-agent/src/tools/checkpoint.ts:62,98` — `checkpoint`: `args.goal ? "checkpointing: " + args.goal : "checkpointing"` -> mode `"omit"`; `rewind`: `"rewinding"` -> mode `"omit"`.
- `packages/coding-agent/src/modes/controllers/event-controller.ts:187-199,951-956,2129-2134` — `normalizeIntent`, `workingIntentLabel` (`intent.WorkingLabel`, `intent.WorkingLabelFromEvent`: cuts colon-space label before dropping trailing periods).
- `packages/ai/src/utils/validation.ts:46-2290`, `packages/agent/src/tool-arguments.ts:16-51`, `packages/omptype/src/{errors,ir,interp}.ts`, and `packages/ai/src/utils/schema/json-schema-validator.ts:77-109` — full port of omp's argument validation, normalization, coercion, and error-formatting pipeline (`internal/intent/validate.go`). See the function-by-function mapping table below.
- `packages/coding-agent/src/sdk.ts:4655` vs `packages/agent/src/agent-loop.ts:1085` — `PromptEnabled(ctx)` (checks `Enabled(ctx)` only, keeping the prompt line under `PI_NO_INTENT=1`) vs `SchemaInjectionEnabled(ctx)` (checks `Enabled(ctx) && !DisabledByEnv()`).
- `packages/coding-agent/src/sdk.ts:5660-5666` — `NotifySettingChange(sessionID, key)` + `SyncSessionTools(ctx)` (invoked automatically on `before_agent_start` via `RegisterLifecycle` / `RegisterSync`) to re-register tool schemas when `tools.intentTracing` changes mid-session.

## PiG Host Mechanics (Verified Against the PiG host source (`$PIG_SRC`))

1. **Validation vs `PrepareArguments` order**:
   `agent/tool_execution.go:376-402` runs `PrepareArguments(call.args)` **BEFORE** `ai.ValidateToolArgumentsJSON(call.name, parameters, args)`. PiG's validator (`ai/validate.go:44-95`, `schema/coerce.go:684-889,1122-1239`, `schema/explain.go:88-133,306-350`) coerces `null`->`"null"`, `5`->`"5"`, `true`->`"true"` for `type: "string"`, rejects objects/arrays with `expected string, got object`, rejects missing required fields with `missing`, and rejects undeclared keys on closed schemas with `unknown field`.
   - Therefore, for EVERY tool that does not own `i`, `intent.Tool` **always** installs a `PrepareArguments` wrapper (chaining `def.PrepareArguments` when present):
     - The tool's own `PrepareArguments` (such as `glob`'s strict unknown-key check in `internal/search/register.go:253`) never sees `i`.
     - When `doInject` is true, the wrapper returns `i` as the model's string, or `""` when `i` was absent or not a string. Thus missing `i` (including in-process `ExecuteTool` callers like `internal/eval/bridge.go:434` and `internal/toolcmd`) and malformed `i` (`null`, `5`, `{...}`, `[...]`) pass host validation without coercion pollution, matching omp's strip-then-validate behavior (`agent-loop.ts:3202-3226`).
     - When `doInject` is false (`omit`, function-intent, `PI_NO_INTENT=1`, or `tools.intentTracing: false`), the wrapper drops any stray `i` before host validation so closed (`additionalProperties: false`) schemas (`resolve`, `reject`, `checkpoint`, `rewind`, `propose`, `goal`) accept calls where a model sent `i` out of habit.
   - `intent.Tool` then strips `i` immediately before calling `def.Execute`, recording any non-empty normalized/derived intent in the per-session table for the duration of `Execute` and deleting that call's entry via `defer` when `Execute` returns.

2. **Which arguments `tool_execution_start` events and tool renderers carry**:
   - `agent/tool_execution.go:85,127,640-646` emits `ToolExecutionStartEvent` **BEFORE** `prepareToolCall`, carrying the raw model arguments `call.args` (containing `i`).
   - `agent/tool_execution.go:435-442` emits `ToolExecutionUpdateEvent` with `rawArgs := prepared.call.args` (still carrying `i`).
   - `internal/codingagent/event_bridge.go:331-363` forwards those raw `Args` / `WireArgs` to extension `tool_execution_start` / `tool_execution_update` listeners.
   - `internal/codingagent/interactive_events.go:389-419,520-557` and `internal/codingagent/tool_definition_renderers.go:18-27,201-256` pass those raw args (including `i`) to `ToolExecutionComponent` and `RenderCall`/`RenderResult`.
   - Because PiG passes raw args (including `i`) directly to renderers, omp's `renderArgsWithIntent` (`event-controller.ts:226-230`) is not needed for card rendering.
   - A per-session side table (`intent.For(ctx, toolCallID)`, deleted on `Execute` return and guarded against late recreation after `lifecycle.End`) is maintained so tools with dynamic `intent(args)` derivers (`eval`, `checkpoint`, `rewind`) and in-tool callers (`bash`, `eval`, `task`) can read the call's intent via `intent.For(ctx, ctx.ToolCallID())`.

3. **Per-session schemas (`tools.intentTracing: false` and non-interactive hosts)**:
   - Each PiG session runs its own extension instance (`coding/extension/host/fusepack/fusepack.go:34-54`, `coding/cli/child_sessions.go:97-154`).
   - Calling `ctx.RegisterTool(def)` inside `OnSessionStart` sends a `registerTool` host call that replaces the tool definition in place (`coding/extension/host/subprocess/tool_registration.go:14-43`, `coding/extension/tool_registry.go:34-59`) and triggers `refreshTools` (`coding/session_tool_registry.go:504-591`) before the first turn reaches the model.
   - `intent.DefaultEnabled` matches omp's `intentTracingActive` (`sdk.ts:492-499`, `packages/utils/src/env.ts:493-505`): an explicit setting/env wins; unset, tracing is ON only when the top-level host is interactive TUI (`ctx.HasUI() && ctx.Mode() == "tui"`, NOT RPC where `ctx.HasUI()` is also true per `extensions/sdk/context.go:2384`), inherited by child sessions via `intent.ParentSession` and cached per session ID.

## Per-Tool Mode Table (Every Tool Registered in `oh-my-pig`)

| Tool | Registration File | omp `intent` Declaration (`v19.1.1`) | Mode | Fallback Deriver |
|---|---|---|---|---|
| `read` | `internal/files/register.go` | default | `require` | — |
| `write` | `internal/files/register.go` | default | `require` | — |
| `edit` | `internal/files/register.go` | default | `require` | — |
| `grep` | `internal/search/register.go` | default | `require` | — |
| `glob` | `internal/search/register.go` | default | `require` | — |
| `find` | `internal/search/register.go` | default | `require` | — |
| `bash` | `internal/shell/bash.go` | default | `require` | — |
| `ask` | `internal/ask/ask.go` | default | `require` | — |
| `todo` | `internal/todo/register.go` | default | `require` | — |
| `web_search` | `internal/web/search.go` | default | `require` | — |
| `task` | `internal/agents/register.go` | default | `require` | — |
| `wait` | `internal/agents/register.go` | `tools/wait.ts:321` (`"optional"`) | `optional` | — |
| `yield` | `internal/agents/yield.go` (`CustomTools`) | `tools/yield.ts:318` (`"omit"`) | `omit` | — |
| `ast_grep` | `internal/ast/register.go` | default | `require` | — |
| `ast_edit` | `internal/ast/register.go` | default | `require` | — |
| `resolve` | `internal/ast/register.go` | `tools/resolve.ts:342` (`"omit"`) | `omit` | — |
| `reject` | `internal/ast/register.go` | `tools/resolve.ts:359` (`"omit"`) | `omit` | — |
| `checkpoint` | `internal/checkpoint/checkpoint.go` | `tools/checkpoint.ts:62` (fn) | `omit` | `args.goal ? "checkpointing: " + goal : "checkpointing"` |
| `rewind` | `internal/checkpoint/checkpoint.go` | `tools/checkpoint.ts:98` (fn) | `omit` | `"rewinding"` |
| `debug` | `internal/debug/tool.go` | default | `require` | — |
| `eval` | `internal/eval/register.go` | `tools/eval.ts:386` (fn) | `omit` | `title \|\| "running " + formatEvalInputLanguage(language)` |
| `github` | `internal/github/register.go` | default | `require` | — |
| `lsp` | `internal/lsp/register.go` | default | `require` | — |
| `propose` | `internal/modes/register.go` | `tools/resolve.ts:377` (`"omit"`) | `omit` | — |
| `goal` | `internal/modes/register.go` | `goals/tools/goal-tool.ts:71` (`"omit"`) | `omit` | — |

Additional omp tools present in `toolModes` for completeness if later ported: `report_issue` (`omit`), `think` (`omit`), `advise` (`omit`).

## Settings Read

| Key | Upstream default | Env | Honoured |
|---|---|---|---|
| `tools.intentTracing` | `true` (when unset: on in interactive TUI + its subagents, off in print/JSON/RPC/ACP/SDK per `sdk.ts:492-499`) | `PI_INTENT_TRACING` | Yes via `intent.DefaultEnabled` + `intent.Configured` + `intent.ParentSession` hooks |
| `PI_NO_INTENT` | unset (`"1"` disables wire schema injection only; prompt follows `tools.intentTracing` per `sdk.ts:4655`) | `PI_NO_INTENT` | Yes (`intent.DisabledByEnv()`, `intent.SchemaInjectionEnabled`, `intent.PromptEnabled`) |
| `PI_INTENT_DESC` | `"concise intent"` | `PI_INTENT_DESC` | Yes (`intent.Description()`) |

## Validation Pipeline Function-by-Function Mapping (`validation.ts` / `tool-arguments.ts` / `omptype`)

| Upstream function (lines) | Go function (`validate.go` / `intent.go`) | Ported / Not ported (why) |
|---|---|---|
| `validateAgentToolArguments` (`tool-arguments.ts:23-42`) | `toolWithOptions` `PrepareArguments` (`intent.go`) | Ported: runs `validateAndNormalizeArgsOpts` with `keepUnknownRootKeys: true` for lenient tools, passing normalized args on success and raw args (minus sentinels) on failure. |
| `withoutSentinels` (`tool-arguments.ts:44-51`) | `withoutSentinels` (`validate.go`) | Ported. |
| `matchesExpectedType` (`validation.ts:56-77`) | `matchesExpectedType` / `matchesSchemaType` (`validate.go`) | Ported. |
| `tryParseNumberString` (`validation.ts:79-99`) | `tryParseNumberString` (`validate.go`) | Ported. |
| `tryCoerceBoolean` (`validation.ts:101-130`) | `tryCoerceBoolean` (`validate.go`) | Ported. |
| `tryCoerceBooleanToNumber` (`validation.ts:132-140`) | `tryCoerceBooleanToNumber` (`validate.go`) | Ported. |
| `tryCoerceString` (`validation.ts:142-170`) | `tryCoerceString` (`validate.go`) | Ported (incl. container-to-string `JSON.stringify` without HTML escaping, JS `Number.prototype.toString`, and `allowLossy` gating). |
| `tryCoerceForExpectedTypes` (`validation.ts:177-195`) | `tryCoerceForExpectedTypes` (`validate.go`) | Ported. |
| `tryParseLeadingJsonContainer` (`validation.ts:197-261`) | `tryParseLeadingJSONContainer` (`validate.go`) | Ported. |
| `cleanLiteralEscapes` (`validation.ts:268-304`) | `cleanLiteralEscapes` (`validate.go`) | Ported. |
| `escapeRawControlsInJsonStrings` (`validation.ts:315-371`) | `escapeRawControlsInJSONStrings` (`validate.go`) | Ported. |
| `tryHealMalformedJson` (`validation.ts:387-418`) | `tryHealMalformedJSON` (`validate.go`) | Ported. |
| `acceptParsedJsonForTypes` (`validation.ts:422-438`) | `acceptParsedJSONForTypes` (`validate.go`) | Ported (incl. padded `"null "` and nested JSON string parsing up to depth 3). |
| `looksLikeJsonContainerString` (`validation.ts:440-459`) | `looksLikeJSONContainerString` (`validate.go`) | Ported. |
| `tryParseJsonForTypes` (`validation.ts:474-527`) | `tryParseJSONForTypes` (`validate.go`) | Ported. |
| `pathToPointer` / `decodeJsonPointer` (`validation.ts:538-553`) | `pathToPointer` / `decodeJSONPointer` (`validate.go`) | Ported. |
| `getValueAtPointer` / `setValueAtPointer` / `deleteValueAtPointer` (`validation.ts:559-660`) | `getValueAtPointer` / `setValueAtPointer` / `deleteValueAtPointer` (`validate.go`) | Ported. |
| `branchMatchesSchema` (`validation.ts:671-673`) | `branchMatchesSchema` (`validate.go`) | Ported. |
| `normalizeOptionalNullsForSchema` (`validation.ts:675-908`) | `normalizeOptionalNullsForSchema` (`validate.go`) | Ported (incl. union branch recovery, `default` substitution for required nulls, and nested closed-object unknown null stripping). |
| `resolveLocalJsonSchemaRef` (`validation.ts:910-919`) | `resolveLocalRef` (`schema.go`) | Ported. |
| `normalizeEnumStringWhitespace` (`validation.ts:921-1037`) | `normalizeEnumStringWhitespace` (`validate.go`) | Ported (supports `[]any`, `[]string`, and reflect slice enums). |
| `trimTrailingLineTerminators` / `trimIdentifierStringLeaf` / `normalizeIdentifierStringWhitespace` (`validation.ts:1084-1160`) | `trimTrailingLineTerminators` / `trimIdentifierStringLeaf` / `normalizeIdentifierStringWhitespace` (`validate.go`) | Ported (skips content-carrying keys). |
| `decodeDoubleEncodedKey` / `normalizeDoubleEncodedKeys` (`validation.ts:1186-1259`) | `decodeDoubleEncodedKey` / `normalizeDoubleEncodedKeys` (`validate.go`) | Ported. |
| `schemaAcceptsStringAndArray` / `schemaNodeAcceptsArray` / `parsedArrayMatchesArrayBranch` / `normalizeStringEncodedArrayUnions` (`validation.ts:1272-1403`) | `schemaAcceptsStringAndArray` / `schemaNodeAcceptsArray` / `parsedArrayMatchesArrayBranch` / `normalizeStringEncodedArrayUnions` (`validate.go`) | Ported. |
| `singleRequiredStringKey` / `normalizeSingleStringField` (`validation.ts:1410-1463`) | `singleRequiredStringKey` / `normalizeSingleStringField` (`validate.go`) | Ported. |
| `parseFlattenedPath` / `buildFlattenedPath` / `normalizeFlattenedArrayProperties` (`validation.ts:1511-1641`) | `parseFlattenedPath` / `buildFlattenedPath` / `normalizeFlattenedArrayProperties` (`validate.go`) | Ported (preserves `undefinedSlot` for sparse holes so error wording prints `(was undefined)`). |
| `coerceArgsFromIssues` (`validation.ts:1672-1733`) | `coerceArgsFromIssues` (`validate.go`) | Ported (incl. single-value-to-array wrapping and failed-union-branch type repairs). |
| `getValidationContext` (`validation.ts:1761-1767`) | — | Not ported: Go tools already carry wire JSON Schemas (`sdk.Schema`); no ArkType runtime compilation needed. |
| `preserveUnknownRootFields` (`validation.ts:1773-1776`) | — | Not ported for strict tools: omp's open omptype schemas keep unknown keys on open objects, whereas PiG's host validator rejects unknown keys on `additionalProperties: false` wire schemas; the port drops undeclared keys on closed objects so valid calls pass PiG's host validator. |
| `restoreDroppedRootKeys` (`validation.ts:1779-1789`) | `restoreDroppedRootKeys` (`validate.go`) | Ported (used when `keepUnknownRootKeys: true` for lenient tools). |
| `flattenJsonSchemaIssues` / `validateContext` (`validation.ts:1791-1844`) | `validateContext` / `validateNodeFull` (`validate.go`) | Ported (matches omptype's `typeof [] === "object"` acceptance for `type: "object"`). |
| `skipSpillWhitespace` / `isSpillPairStart` / `findSpillValueEnd` / `parseSpilledPairs` / `splitSpilledValue` / `healInbandArgSpill` (`validation.ts:1862-1999`) | `skipSpillWhitespace` / `isSpillPairStart` / `findSpillValueEnd` / `parseSpilledPairs` / `splitSpilledValue` / `healInbandArgSpill` (`validate.go`) | Ported. |
| `validateToolCall` (`validation.ts:2010-2016`) | — | Not ported: PiG dispatches tool calls by name before `PrepareArguments`. |
| `truncateArgsForError` (`validation.ts:2021-2033`) | `truncateArgsForError` (`validate.go`) | Ported (UTF-16 code-unit length for 256-char cap). |
| `validateToolArguments` (`validation.ts:2052-2189`) | `validateAndNormalizeArgsOpts` (`validate.go`) | Ported in exact upstream order of application; emits `original` before `normalized` in schema property order. `__parseError` sentinel check omitted because PiG's SDK parses tool-call JSON before calling `PrepareArguments`. |
| `runCoercionPasses` (`validation.ts:2197-2290`) | `runCoercionPasses` (`validate.go`) | Ported (up to 5 passes). |
| `OmpError` / `describeValue` / `describeKind` / `formatPath` / `errorCode` (`omptype/src/errors.ts:90-290`) | `formatOmpErrorMessage` / `describeValue` / `describeKind` / `formatDotPath` / `ompErrorCode` (`validate.go`) | Ported (UTF-16 length for 40-char threshold, non-HTML-escaped JSON quoting with verbatim U+2028/U+2029, JS `Number.prototype.toString`, described boolean/enum/union `errorCode` classification). |
| `expectedOf` (`omptype/src/ir.ts:1726-1798`) | `expectedOfSchema` (`validate.go`) | Ported. |

## Tests Ported

32 test functions across `internal/intent/intent_test.go` (17), `internal/intent/validate_test.go` (12), and `internal/intent/ompdiff_test.go` (3, including a checked-in 1,199-case reduced differential corpus `testdata/omp_diff_reduced.jsonl` generated from real omp `v19.1.1`'s `validateAgentToolArguments` across all 24 tools and all defect/residual classes):
- `TestWireSchemaInjectsIntentFirst` (`agent-loop.test.ts`, `append-only-context.test.ts`): plain Go map schema, `*sdk.OrderedObject` / `sdk.Type.Object` schema, and `optional` (`wait`) tool over the real SDK wire encoder (`fakehost_test.go`).
- `TestMissingMalformedAndEmptyIntentOnRequireTool` (`agent-loop.ts:1126-1128,3202-3226`): missing `i`, `""`, whitespace/dots-only, `null`, `5`, `true`, object, and array all pass host validation without coercion pollution and record nothing (`For`, `FromEvent`, `WorkingLabelFromEvent` all empty).
- `TestStrayIntentOnClosedOmitToolsStrippedBeforeValidation` (`agent-loop.ts:3202-3226`): stray `i` on closed (`additionalProperties: false`) omit/function-intent tools (`resolve`, `reject`, `checkpoint`, `rewind`, `propose`, `goal`) is stripped before host validation while surfacing in `WorkingLabelFromEvent`.
- `TestPureUnionRootInjectsPerBranch` (`normalize-tools-prune.test.ts`): pure `anyOf` root injects `i` first into each branch without root sibling `properties`/`required`, validates, and strips `i`.
- `TestExistingSchemaOwnedIntentUntouched` (`normalize-tools-prune.test.ts`, `agent-loop.test.ts`): tool-owned `i` is not reordered, not made required, not stripped, and not recorded as harness intent.
- `TestDerivesIntentForToolThatOwnsIArgument` (`agent-loop.test.ts:485`): tool owning `i` as an argument still derives intent via its deriver function while receiving `{label, i}` unchanged.
- `TestOmitModeDoesNotInjectIntent` (`normalize-tools-prune.test.ts`): `omit` and function-intent tools (`resolve`, `reject`, `propose`, `goal`, `yield`, `eval`, `checkpoint`, `rewind`) receive no `i`.
- `TestEnvPINoIntentDisablesInjection`: `PI_NO_INTENT=1` suppresses injection.
- `TestEnvPIIntentDescOverridesDescription` (`normalize-tools-prune.test.ts`): `PI_INTENT_DESC` overrides `"concise intent"`.
- `TestExecuteStripsIntentWithStrictParserAndPrepareArguments` (`agent-loop.test.ts`): strict `additionalProperties: false` tool with strict `PrepareArguments` (like `glob`) and strict `Execute` parser never sees `i`, while `FromEvent` and `For(ctx, toolCallID)` read the normalized intent, and the entry is removed when `Execute` returns.
- `TestFallbackIntentDerivers`: `eval` (`py`, `js`, `title`, default), `checkpoint` (with/without `goal`), and `rewind` derive fallback intents when `i` is absent.
- `TestSessionIsolationDeleteOnReturnAndNoRecreateAfterRelease`: concurrent sessions with identical `toolCallID` never see each other's intents; completed calls are deleted on `Execute` return; `lifecycle.End` and `session_shutdown` release the session and prevent late `Record` calls from recreating its table.
- `TestReloadAndResumeClearTombstoneAndRecomputeEnabled`: `/reload` and resume clear tombstones and recompute `Enabled`.
- `TestDefaultEnabledInteractiveHostAndSubagentInheritance` (`sdk.ts:492-499`, `env.ts:493-505`): TUI enables by default; RPC (`HasUI=true, Mode="rpc"`), print, and JSON disable by default and drop `i` via `RegisterSync`; child sessions inherit top-level parent's interactive-host decision via `ParentSession`; explicit `Configured` overrides both directions.
- `TestToolIntentLabelAndWorkingLabel` (`packages/wire/src/index.ts`, `event-controller.ts:187-199`): `Label` colon-space splitting preserving `file:line`/URLs; `WorkingLabel` and `WorkingLabelFromEvent` cut label first then normalize (`"Running tests...: unit"` -> `"Running tests"`).
- `TestInjectSchemaEdgeCases`: bare injection (no description), direct `InjectSchema` reordering of existing `i`, pure `oneOf` roots.
- `TestSchemaDefinesProperty` (`json-schema-validator.ts:168-326`): direct `properties.i`, immediate false return on positive `properties.i: false` even with matching `patternProperties`, negated `not.properties.i: false`, `not.required: ["i"]`, `anyOf`, object `$ref`, array-indexed `$ref` (`#/items/1`), `propertyNames`, `patternProperties`, `if`/`then`/`else`, `dependentSchemas`, `dependentRequired`, `dependencies`.
- `TestValidationDropsUndeclaredKeys`, `TestValidationDropsNestedUndeclaredKeys` (`validation.ts:1661-1699`): undeclared keys on closed root and nested schemas are dropped so PiG's host validator accepts the call.
- `TestValidationMatchesOmpWordingTable` (`validation.ts:1814-1834,2172-2188`, `omptype/src/{errors,ir,interp}.ts`): 20-case wording table verified against real omp `v19.1.1`'s `validateAgentToolArguments` (including described booleans, non-HTML-escaped quoting, and UTF-16 length thresholds).
- `TestValidationCoercionsAndRepairs` (`validation.ts:79-2290`): container-to-string, single-value-to-array, union scalar/container coercion, string-encoded array unions, identifier trailing-newline trim, double-encoded keys, flattened array properties, single-string field remap, padded `"null "`, malformed JSON healing, in-band spill healing, JS number formatting, and `original`-before-`normalized` error layout.
- `TestValidationStringSliceEnum`: Go `[]string` enum validation, whitespace trimming, and optional-empty stripping (matching `debug`, `github`, `goal` schemas).
- `TestValidationLenientToolsBypassGenericValidator`, `TestValidationLenientToolsNormalizeBeforePrepare` (`tool-arguments.ts:16-42`): lenient tools receive normalized arguments on validation success and raw arguments on failure.
- `TestMidSessionIntentTracingToggleReSyncsSchemas`, `TestSyncSessionIsolationAndNoSpuriousReRegister`, `TestAllRegisteredInjectableToolsAreTracked`, `TestReleasedSessionsTombstoneRingBounded`, `TestUnstartedExtensionCleanupFreesTrackedDefs` (`sdk.ts:5660-5666`): mid-session toggle re-syncs only that session's tools (never leaking subagent `yield`), 0 `registerTool` calls on unchanged `before_agent_start`, every injectable tool on `ohmypig.Extension()` is tracked, shutdown releases all tracked state, `releasedSessions` is capped at `maxEndedSessionTombstones` (4096), and unstarted `*sdk.Extension` entries are weak-keyed and freed via `runtime.AddCleanup`.
- `TestPINoIntentSplitsPromptAndSchemaInjection` (`sdk.ts:4655`, `agent-loop.ts:1085`): `PI_NO_INTENT=1` disables `SchemaInjectionEnabled` while leaving `PromptEnabled` true.
- `TestOmpDifferentialReduced`, `TestOmpDifferentialFull`: differential harness against omp `v19.1.1`'s `validateAgentToolArguments` (1,199-case reduced corpus by default; full 20,065-case corpus via `OMP_INTENT_DIFF_CORPUS`). Regenerate `testdata/omp_diff_reduced.jsonl` via `piglets/oh-my-pig/proof/diff/intent/make-corpus.sh`, `measure.sh`, and `python3 piglets/oh-my-pig/proof/diff/intent/select-reduced.py "$DIFF_OUT/corpus.jsonl" components/oh-my-pig/extensions/oh-my-pig/internal/intent/testdata/omp_diff_reduced.jsonl "$DIFF_OUT/results/port-prepare-only.jsonl"`.

## Not Ported

| Upstream feature | Why omitted |
|---|---|
| `i` injection on MCP and third-party extension tools (`agent-loop.ts:1083-1113`) | PiG has no extension hook that rewrites wire schemas of all tools on outbound provider requests; only tools explicitly wrapped through `intent.Tool` receive `i`. |
| Stripping `i` when host writes arguments back as text or compares calls (`session-history-format.ts:220`, `session-dump-format.ts:205-212`, `snapcompact.ts:1139-1147` compaction labels, `tool-call-loop-guard.ts:44`) | PiG's host owns session history formatting, dumps, compaction, and loop guards and retains the model's raw tool arguments (`args.i` stays present). |
| `<examples>` block `i="…"` placeholder injection (`agent-loop.ts:1109`, `packages/ai/src/dialect/examples.ts:17-30`) | None of the port's tool descriptions (`ast_grep`, `ast_edit`, `debug`) embed omp's structured `examples` arrays (`internal/ast/UPSTREAM.md:80`), so `intent.Tool` does not rewrite `Description`. |
| Per-caller `intentDescription` override (`agent-loop.ts:1086` middle term, `sdk.ts:5231` spotlight experiment) | `Description()` resolves `PI_INTENT_DESC` or `DefaultDescription` (`"concise intent"`); spotlight experiment custom intent descriptions are not ported. |
| Original call key order inside `Received arguments:` of validation errors (`validation.ts:2186`) | PiG's Go SDK `ToolPrepareArgumentsFunc` signature receives `map[string]any` (decoded without `ObjectOrder` in `extensions/sdk/extension.go:1662-1670`), so key order is already lost before `PrepareArguments` runs. The port orders `original` before `normalized` and serializes argument keys in schema property order, then sorted unknown keys. |
| Retaining unknown keys on open omptype object schemas (`validation.ts:1775-1778`) | Most omp tools have open omptype schemas (retaining unknown root/nested keys), whereas their wire schemas declare `additionalProperties: false` and PiG's host validator rejects unknown keys. The port drops undeclared keys on closed objects for strict tools so valid calls pass PiG's host validator (lenient tools retain unknown root keys via `keepUnknownRootKeys: true`). |
| Passing raw `[]` where `type: "object"` is declared (`omptype/src/interp.ts:391`) | In JS `typeof [] === "object"`, so omptype accepts arrays for `type: "object"` (`bash {ready: []}`, `debug {arguments: []}`, 29 cases) and passes them with no named properties; PiG's host validator rejects arrays for `type: "object"`, so the port accepts them and converts them to `{}` so PiG's host validator accepts them too (0 host refusals on strict tools). |
| Skipping PiG host validation on lenient tool fallback (`tool-arguments.ts:32-42`) | PiG's SDK always runs `ai.ValidateToolArgumentsJSON` after `PrepareArguments` with no opt-out. When a lenient tool's raw fallback fails schema validation, the tool itself (`ask`, `task`) must emit its refusal from `PrepareArguments` if it wants custom wording instead of PiG's host validator. |
| Per-request `pruneDescriptions` stripping of all wire descriptions (`agent-loop.ts:1097-1103`) | PiG has no non-native inline tool catalog; `InjectSchema(..., "")` supports bare `i` injection if ever needed. |
| `toolCall.intent` field on persisted assistant messages & OpenTelemetry `omp.gen_ai.tool.call.intent` span attribute (`agent-loop.ts:3209,3682`) | PiG's `ai.ToolCall` has no `Intent` field and the extension SDK has no OTel span hook; raw `args.i` persists on the call and `intent.For(ctx, toolCallID)` exposes live/derived intent during `Execute`. |
| Streaming partial-JSON working-message intent updates (`event-controller.ts:1886-1912`) | PiG's extension `message_update` does not expose per-delta parsed tool args to `internal/intent`; working messages update at `tool_execution_start`. |
| `beforeToolCall` hook seeing stripped `args` (`agent-loop.ts:3207,3242`) | PiG runs `tool_call` before-hooks after validation (`agent/tool_execution.go:403-426`) and before `Execute`, so `EventToolCall` handlers still see `i` in `event["input"]`; `PrepareArguments` and `Execute` never see `i`. |
| JavaScript RegExp features (`pattern`, `patternProperties`, `propertyNames.pattern`) (`json-schema-validator.ts:265,273,713`) | Patterns compile with Go `regexp` (RE2), so JS-only syntax (lookahead, backreferences) and Unicode `\s` (e.g. `\u00a0`) follow RE2 semantics rather than ECMAScript RegExp. |
| `__parseError` / `__rawJson` provider parse-error formatting (`validation.ts:2058-2064`) | PiG's host parses tool-call JSON before calling `tool_prepare_arguments`; malformed provider JSON never reaches `PrepareArguments`. |
## Integrator Instructions

### 1. Wire `intent.Configured`, `intent.ParentSession`, and lifecycle in `extension.go`

`intent.Enabled` defaults to `intent.DefaultEnabled`, which already implements omp's `isInteractiveHost()` fallback (`ctx.HasUI() && ctx.Mode() == "tui"`, inherited by child sessions, off in RPC/print/json). Wire the two hooks once at startup:

```go
// Explicit settings / env override for tools.intentTracing (any layer beats fallback).
intent.Configured = func(ctx sdk.Context) (bool, bool) {
    store := settings.StoreFor(ctx)
    if store.Provenance("tools.intentTracing") == settings.ProvenanceDefault {
        return false, false
    }
    return store.Bool("tools.intentTracing"), true
}

// Subagents inherit the top-level session's interactive-TUI host decision.
intent.ParentSession = agents.ParentSession

// Propagate mid-session tools.intentTracing changes to the next agent run (sdk.ts:5660-5666).
settings.OnChange(intent.NotifySettingChange)
```

`intent.RegisterLifecycle(e)` (already called in `extension.go`) hooks `before_agent_start` to call `SyncSessionTools` whenever `tools.intentTracing` changes.

### 2. Working message & in-tool intent consumers

- **`internal/ansi/register.go:179`**: already updated by `AnsiTerm2` to `intent.WorkingLabelFromEvent(ctx, data)`.
- **In-tool consumers** (omp's `session.getToolCallIntent(toolCallId)`):
  - `internal/shell` (`bash.ts:2068,2123` — background job / service intent label): call `intent.For(ctx, ctx.ToolCallID())` inside `BashTool.Execute`.
  - `internal/eval` (`eval.ts:662` — background eval job intent): call `intent.For(ctx, ctx.ToolCallID())` inside `Manager.Execute`.
  - `internal/agents` (`task/index.ts:1688` — background task job intent): call `intent.For(ctx, ctx.ToolCallID())` inside `Runtime.ExecuteTask`.
- **`internal/toolcmd/cli.go:1734-1736`**: the `if schemaHasHarnessIntent(def.Schema) { args["i"] = "" }` shim is now redundant because `intent.Tool`'s `PrepareArguments` supplies `""` automatically when `i` is absent (safe to leave or remove).

### 3. One-line change at EVERY tool registration site

Wrap each `sdk.ToolDefinition` passed to `e.RegisterTool(...)` with `intent.Tool(...)` (and `ctx.RegisterTool(...)` inside `OnSessionStart` with `intent.ToolForContext(ctx, ...)`). For tools that do NOT already re-register on `session_start`, call `intent.RegisterSync(e, defs...)` so non-interactive sessions (print/JSON/RPC) and `tools.intentTracing: false` sessions drop `i` from their wire schemas at `session_start`.

| File | Line(s) | Exact Change |
|---|---|---|
| `internal/agents/register.go` | `:43` | `e.RegisterTool(intent.Tool(waitToolDef(rt, false, false)))` |
| `internal/agents/register.go` | `:75` | `ctx.RegisterTool(intent.ToolForContext(ctx, taskToolDef(rt, cfg, disc.Agents, spawns)))` |
| `internal/agents/register.go` | `:80` | `ctx.RegisterTool(intent.ToolForContext(ctx, waitToolDef(rt, true, false)))` |
| `internal/agents/register.go` | `:90` | `ctx.RegisterTool(intent.ToolForContext(ctx, waitToolDef(rt, false, endTurn)))` |
| `internal/agents/register.go` | `:94` | `ctx.RegisterTool(intent.ToolForContext(ctx, taskToolDef(rt, cfg, disc.Agents, "*")))` |
| `internal/agents/register.go` | `:111` | `e.RegisterTool(intent.Tool(taskToolDef(rt, cfg, agents, spawns)))` |
| `internal/agents/executor.go` | `:799` | `yieldTool := intent.Tool(BuildYieldToolDefinition(...))` (mode `omit`; strips stray `i`) |
| `internal/ask/ask.go` | `:75` | `e.RegisterTool(intent.Tool(sdk.ToolDefinition{ ... }))` |
| `internal/ast/register.go` | `:287` | `e.RegisterTool(intent.Tool(sdk.ToolDefinition{ ... }))` (`ast_grep`) |
| `internal/ast/register.go` | `:299` | `e.RegisterTool(intent.Tool(sdk.ToolDefinition{ ... }))` (`ast_edit`) |
| `internal/ast/register.go` | `:314` | `e.RegisterTool(intent.Tool(sdk.ToolDefinition{ ... }))` (`resolve`) |
| `internal/ast/register.go` | `:325` | `e.RegisterTool(intent.Tool(sdk.ToolDefinition{ ... }))` (`reject`) |
| `internal/checkpoint/checkpoint.go` | `:916` | `e.RegisterTool(intent.Tool(sdk.ToolDefinition{ ... }))` (`checkpoint`) |
| `internal/checkpoint/checkpoint.go` | `:926` | `e.RegisterTool(intent.Tool(sdk.ToolDefinition{ ... }))` (`rewind`) |
| `internal/debug/tool.go` | `:192` | `e.RegisterTool(intent.Tool(tool.Definition()))` |
| `internal/eval/register.go` | `:54` | `e.RegisterTool(intent.Tool(sdk.ToolDefinition{ ... }))` |
| `internal/files/register.go` | `:31` | `e.RegisterTool(intent.Tool(sdk.ToolDefinition{ ... }))` (`read`) |
| `internal/files/register.go` | `:55` | `e.RegisterTool(intent.Tool(sdk.ToolDefinition{ ... }))` (`write`) |
| `internal/files/register.go` | `:90` | `e.RegisterTool(intent.Tool(sdk.ToolDefinition{ ... }))` (`edit`) |
| `internal/github/register.go` | `:265` | `e.RegisterTool(intent.Tool(sdk.ToolDefinition{ ... }))` |
| `internal/lsp/register.go` | `:86` | `e.RegisterTool(intent.Tool(sdk.ToolDefinition{ ... }))` |
| `internal/modes/register.go` | `:49` | `e.RegisterTool(intent.Tool(sdk.ToolDefinition{ ... }))` (`propose`) |
| `internal/modes/register.go` | `:60` | `e.RegisterTool(intent.Tool(sdk.ToolDefinition{ ... }))` (`goal`) |
| `internal/search/register.go` | `:159` | `e.RegisterTool(intent.Tool(sdk.ToolDefinition{ ... }))` (`grep`) |
| `internal/search/register.go` | `:183` | `e.RegisterTool(intent.Tool(sdk.ToolDefinition{ ... }))` (`glob`) |
| `internal/search/register.go` | `:208` | `e.RegisterTool(intent.Tool(sdk.ToolDefinition{ ... }))` (`find`) |
| `internal/shell/bash.go` | `:249` | `e.RegisterTool(intent.Tool(tool.Definition()))` |
| `internal/shell/bash.go` | `:262` | `ctx.RegisterTool(intent.ToolForContext(ctx, tool.DefinitionForUI(false)))` |
| `internal/todo/register.go` | `:683` | `e.RegisterTool(intent.Tool(sdk.ToolDefinition{ ... }))` |
| `internal/web/search.go` | `:489` | `e.RegisterTool(intent.Tool(webSearchToolDef()))` |

### 4. Wire `internal/prompt` system prompt guidance

Enable `IntentTracing` in `internal/prompt`'s live `before_agent_start` hook using `intent.PromptEnabled(ctx)` (NOT `intent.Enabled(ctx) && !intent.DisabledByEnv()`) so the system prompt follows omp (`sdk.ts:4655`, where `PI_NO_INTENT=1` suppresses schema injection only).
