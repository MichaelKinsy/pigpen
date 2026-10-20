# Port record: @gotgenes/pi-permission-system (partial: the rule engine and tool-name gating)

| Input | Identity |
|---|---|
| Original | `@gotgenes/pi-permission-system` 39.0.3 by MasuRii and Christopher D. Lasher, https://github.com/gotgenes/pi-packages (`packages/pi-permission-system`), commit `c5bc74712cd2fe808d70246d93d26a092f736676`, MIT (its LICENSE file) |
| Oracle | Pi 1.0.0, Node 24.19.0; the original package and the repository files it builds with, unmodified, at `port/oracle` (installed with pnpm from its own lockfile, see `port/oracle/UPSTREAM.md`); its own suite passes there: 177 files, 5418 tests |
| Target | PiG 0.4.0+1.0.0, go1.27.1 |
| Original's tests | 3949 titles in 177 files. Slice: 229 exact twins in 10 files (all of `detect-permissive-bash-fallback`, part of `wrapper-analysis` and `skill-prompt-sanitizer`); the other 167 files, and the rest of those two, are deferred by name in `port/slices.json` |
| Original's decisions | `port/record-probe.sh` runs the original's own functions on `port/probe/cases.json` inside an installed copy of the oracle and writes `extensions/pi-permission-system/testdata/oracle.json`: the bash surface's verdict for 646 commands under 11 configs, the schema's verdict and output for 91 config files, the wrapper classification for 48 units |
| Kind | 1: an extension, no CLI built-in, no provider |

## Scope: what is and is not ported

The original is a 28,900-line permission system: a rule engine, a layered config (global, project, agent and project-agent scopes,
validated with zod), a bash parser built on tree-sitter-bash through WebAssembly, path canonicalization and external-directory
gates, MCP and skill gates, an approval dialog with session grants, forwarding of approvals between sessions, a review log, a
config modal and a cross-extension service. This port takes the part that is pure logic and the part that can be checked
against the original without a person: the rules and the tool gate for what they decide by name.

**Ported**

- the wildcard matcher: `*`, `?` (one UTF-16 unit), the trailing ` *` that also matches the bare prefix, `~`/`$HOME`/`${HOME}`
  expansion on the pattern, Windows case and separator folding;
- rule evaluation (last match wins), `evaluateAnyValue`, `evaluateMostRestrictive`, the yolo and fail-closed rewrites,
  `isSurfaceFullyDenied`, restrictiveness, flat-permission and scope merging with origins, directional sugar for `path` and
  `external_directory`, config normalization (deny with reason), MCP tool-key relocation;
- the global config (`<agent dir>/extensions/pi-permission-system/config.json`: `permission`, `yoloMode`), read as the original
  reads it: `//` and `/* */` comments stripped, the whole file refused (with the original's messages) when the schema rejects
  any part of it, and the permission map composed in the schema's output order (the surfaces the schema names first, which
  decides the last rule to match when a wildcard surface key also names `bash`); the composed ruleset (the top-level `*` is the
  default rule, the rest are config rules);
- the `tool_call` gate: `bash`, for a simple command or simple commands joined by `&&`, `||`, `;`, `|` or newlines (the units the
  original's parse gives such a command): each unit is matched against the bash patterns, the most restrictive unit wins (the
  first of equals is named), and a wrapper unit (`classifyWrapperWords`: `eval`, a shell with a `-c` flag cluster, `sudo`, `env`,
  `xargs`, `nice`, `timeout`, `flock`, ... and `find`/`fd` with an exec flag, by basename) has its allow floored to ask, which
  `yoloMode` grants; tools whose rules have no value patterns; extension tools by name; the original's denial text
  (`[pi-permission-system] Denied by policy: 'bash' (rule 'npm *'). Reason: ...`);
- the skill-read gate: a `read` of a file inside a skill that the system prompt lists is decided by the `skill` surface for that
  skill's name, so it is refused while that surface asks (with the default `*: ask`, every listed skill);
- withholding tools the policy denies outright from the model at `before_agent_start`, and restoring them (the tool-surface
  baseline), the `yolo` status, and the permissive-`*` warning at session start.

**Blocked, fail-closed, with a visible reason** (the original decides these by machinery that is not ported):

- an `ask`: the approval dialog (and its session grants) is not ported, so the call is refused and the message says so.
  `yoloMode` turns asks into allows, as in the original;
- a path-bearing tool (`read`, `write`, `edit`, `find`, `grep`, `ls`) when its rules have path patterns, or when the `path` and
  `external_directory` surfaces have patterns or do not allow everything: path matching (aliases, canonicalization, symlinks) is
  not ported;
- a `bash` call when the path surfaces are not wide open (the original checks the paths in the command);
- a bash command that is not simple commands joined by `&&`, `||`, `;`, `|` or newlines (quoting, expansion, substitution,
  redirection, `&`, an assignment prefix, non-ASCII), when bash has value patterns, or when the catch-all would allow it (it could
  hide a wrapper the original asks about); with no bash patterns, a deny or ask catch-all decides it, and `yoloMode` allows it;
- a wrapper running a pure reader (`sudo ls`, `nice echo`, `xargs grep`): the original's floor exemption lets the inner command's
  rule decide; the port asks (it is stricter here). A unit starting with a command that runs another one which the original
  reads by its own rule (`ssh`, `su`, `source`, `command`, `bash script.sh`, ...) is refused when allowed. Both are listed with
  every other refusal in `testdata/port-differences.json`;
- a tool that `shellTools` routes through the bash surface (the aliases are not ported);
- MCP and skill tools.

**Not ported at all:** project and agent scopes (the project scope is withheld in the original until the project is trusted;
the port reads the global scope only), session approvals and rules, the review and debug logs, forwarding and subagent authority,
the config modal, the cross-extension service, removing denied skills from the system prompt (`withoutDeniedSkills`: a denied
skill stays listed, and its files are not gated by the skill-read gate, as in the original). A file that does not parse warns
with the original's lead text but Go's parse error, not JavaScript's; a schema violation warns once with the original's
messages, where the original warns twice. PiG's `powershell` tool is a shell the original does not know: it is decided by its own
name (or the `*` fallback), not by the bash rules; deny it, or name it in `shellTools`, which makes the port refuse it.

## Mapping

| ID | Original | Go | Checked by |
|---|---|---|---|
| M1 | `policy/wildcard-matcher.ts`, `path/expand-home.ts` | `wildcard.go` (a UTF-16 glob matcher, not a regular expression) | 67 twins; `extras_test.go` |
| M2 | `policy/rule.ts`, `restrictiveness.ts`, `permission-merge.ts`, `scope-merge.ts`, `normalize.ts` | `rule.go` | 122 twins; `extras_test.go` |
| M3 | `exposure/tool-surface-baseline.ts`, `handlers/before-agent-start.ts` (the exposure part) | `exposure.go`, `extension.go` | 14 twins; `adapter_test.go`; the scenario with a universal deny |
| M4 | `config/*` (global scope), `policy/permission-manager.ts` (composition), `handlers/gates/tool.ts` (the decision), `presentation/agent-renderer.ts` (the denial text) | `config.go` | 7 Pi-recorded scenarios; `extras_test.go` |
| M5 | `index.ts`, `handlers/lifecycle.ts` | `extension.go` | scenarios; `adapter_test.go` (a fake interactive host) |
| M6 | JavaScript semantics: ordered objects, UTF-16 units, numbers beyond the double range | `jsjson.go`, `jsutil.go` (copies from the powerline port) | everything that merges or normalizes |
| M7 | `access-intent/bash/wrapper-analysis.ts` (`classifyWrapperWords`), `handlers/gates/bash-command.ts` (units, floors, most restrictive) | `bash.go` | 4 twins and the upstream tables; 646 recorded decisions (`oracle_test.go`) |
| M8 | `config/config-loader.ts` (`stripJsonComments`, `validateUnifiedConfig`), `config/config-schema.ts` | `schema.go`, `config.go` | 91 recorded files (`oracle_test.go`); scenario `config-comments` |
| M9 | `handlers/gates/skill-read.ts`, `exposure/skill-prompt-sanitizer.ts` (parsing, visibility, path match) | `skills.go` | 15 twins; `skills_test.go`; `adapter_test.go` |

## Process: what was written before its tests

The rule engine is tests first: the 189 twins were written from the original's tests, with the upstream inputs and assertions,
against stubs that panic (commit `bcb4fec`, red run in `port/red-run.log`), and the engine was written after. The gate, the config
reader and the exposure are **not** tests first: the seven scenarios were recorded from the original under Pi 1.0.0 first (so the
goldens are the original's, never the port's), the code was then written against them, and its own Go tests (`extras_test.go`,
`adapter_test.go`, the 14 exposure twins) were written afterwards. The first two scenario probes also showed how much of the
original sits behind a path-bearing call, which decided the slice: they are why path tools and compound bash are blocked, not
evaluated.

The review (rev-port-popular-4) found that the gate allowed what the original asks about or refuses, and fixed it tests first:
`8f75007` and `8652375` are red on the code before them (`port/review-red-run.log`: 53, then 2, commands the original asks
about or denies ran unasked), `227df1c` and `987d19a` are green. The red tests compare with the original's own decisions,
recorded by `port/record-probe.sh`, not with the port.

## Results

- `go test -race`: pass. 229 twins (`pigeq twins check --deferred port/slices.json`: exit 0). Against the original's recorded
  decisions: none of the 646 commands is allowed where the original asks or denies; 498 get the original's decision and text,
  148 are refusals listed in `testdata/port-differences.json` (121 commands the port cannot read or guards, 27 floor
  exemptions); all 91 config files get the original's verdict, messages (a parse error: Go's message) and output map; 48 of 48
  wrapper units.
- `pigeq check` against the Pi 1.0.0 goldens: 8 of 8 scenarios + port-gaps + exec-coverage, three runs in a row. The scenarios
  cover bash patterns with a reason, last-match-wins across allow and deny, allowed built-in tools, a universal allow (with its
  warning) and one with an explicit bash policy, a universal deny with a bash exception (the other tools are withheld, visible in
  the model request), yoloMode (status and denies), and a config file with comments. Every scenario runs one tool call per turn (parallel calls finish in no
  fixed order, G4).
- `pigeq mutate --unit`: **171 mutations, all killed** (the review added 62 for the wrapper floors, chains, the schema, comments,
  shellTools and the skill-read gate, and rewrote 7 for the changed code; one it wrote, a line comment's kept newline, was
  equivalent and dropped). The lane's 109 at `92d6572`: The first run of 113 killed 95: 4 mutants were
  equivalent and removed (a `Join` that normalizes a doubled slash; a probe set that adds only values another probe already
  covers; two baseline resets that change nothing observable), 3 did not compile and were rebuilt, and the rest got tests (home
  expansion forms, JavaScript's case folding of non-ASCII letters, blank names, MCP name shapes, the external-directory write
  surface, the first of two asks).
- `pig install extensions/pi-permission-system --validate-only --json`: valid (4 handlers, no commands or tools of its own);
  on the Package directory 0.4.0 answers "no extension entry file" (so does every port's), while `pig install
  ./components/permissions` installs it.
- Piglet: `piglets/pig-popular` now selects five ports and `pig-0.4.0 piglet build --targets linux/amd64` fuses them. A Binary
  built from this member alone gives traces equal to the Pi goldens in every event outside the `llm` channel (8 of 8). The `llm` channel differs by the host, not
  the port: a Binary activates `powershell`, `grep`, `find` and `ls` by default where the extension-mode lanes (and Pi) activate
  `read`, `bash`, `edit` and `write`, so `pigeq check --builtin` cannot pass for a port that sits in front of tools; extension
  mode compares the model request and passes.
- tmux, detached, in an interactive pig 0.4.0 (`port/demo/pane-*.txt`): `echo hello` runs, `rm important.txt` is refused with the
  rule's reason. After the review (`pane-review.txt`, with `config-review.json`, which has comments, and `turns-review.json`):
  `nice rm important.txt` is asked about (blocked), `ls && /bin/sh -c 'rm important.txt'` is refused, `ls | wc -l` runs.
- Size: 2120 hand-written Go lines (non-test, 379 of them copied helpers; the review added 682 in `bash.go`, `schema.go`,
  `skills.go`), 2528 test lines; recorded, not written: `testdata/oracle.json`.

## Findings (to file against PiG, and notes on the porter)

1. **Scenarios need `agentFiles` for a global config, and `commands` for a command the original runs that the port does not.**
   The original runs `npm root -g` when it starts from a checkout; declaring `npm` as `missing` keeps `exec-coverage` green and
   leaves no event in the trace.
2. **The project scope is withheld without trust.** A scenario that writes the config into the project sees no rules at all; the
   original needs a trusted project. The Go SDK has no project-trust query, so the port reads only the global scope.
3. **A fully denied tool is not "denied", it is missing.** The original removes it from the model's tools, so a call to it never
   reaches the gate ("Tool write not found"). A scenario cannot show a denial of a path tool that is denied outright; the
   exposure had to be ported (`SetActiveTools`, `GetAllTools`) for the model request to match.
4. **G4 again:** parallel tool results arrive in no fixed order; every scenario calls one tool per turn.
5. **JavaScript semantics, again:** `?` is one UTF-16 unit, `i`-flag case folding refuses non-ASCII to ASCII mappings, object key
   order puts integer-like keys first (`jsjson.go`, now in five ports).
6. **The ask dialog is the heart of the original and is not ported.** What the port gives up is the interactive path: its title
   and options are visible in the Pi trace (`Permission Required`, `Yes`, `Yes, allow bash "echo *" for this session`, `No`,
   `No, provide reason`). Porting it needs the preview formatters and the session-grant suggestion logic; a good next slice.
7. **The ledger's parametrized titles.** `pigeq twins list` skips `test.each` titles (two in `rule.test.ts`), which run as plain
   subtests here.

8. **Scenarios could not show the gate's gaps.** Every original ask is a dialog and the port's is a refusal, so no scenario of an
   ask can pass; the lane's scenarios held only decisions both sides make without a dialog. Running the original's own
   functions on a table of inputs (`port/record-probe.sh`) is what showed the wrapper floors, the schema and its key order.
9. **The pigeq harness starts every host with `--no-skills`**, so no scenario can reach the skill-read gate: it is tested on the
   fake host only.

## Where a generated skeleton (`pig-codegen extension`) would have saved hand work

- Module, `Extension()`, the four event registrations and the status/notify plumbing.
- The JS helper layer again (`jsjson.go`, `jsutil.go`: 375 lines copied from the powerline port).
- The twin ledger (203 titles by hand, from a `twins list` that cannot see `test.each`) and the slice file (170 entries by hand).
- The fake interactive host: `adapter_test.go` could be generated from the registered events.

## Forwarding and subagent authority (`src/authority`: forwarding, serving, liveness, subagent detection)

Lane `forwarding` (branch `perm-forwarding-1`). A child (subagent) session forwards an ask to its parent through request and
response files in the forwarding directory; the parent drains its inbox on a 250 ms poll, resolves each request against recorded
authority, escalates an `ask` to its authorizer, and answers with a response file; a heartbeat file tells a child whether anyone is
draining. This lane ports the whole layer as library code in the module root (`fwd_*.go`, `subagent_*.go`); `Extension()` does not
call it yet (the lead wires it at integration), so nothing the extension does today changes.

| ID | Original (`src/authority/`) | Go | Checked by |
|---|---|---|---|
| F1 | `transient-fs-retry.ts` | `fwd_fs_retry.go` | 6 twins; `fwd_retry_test.go` (os errors) |
| F2 | `forwarding-io.ts` | `fwd_io.go`, `fwd_prompt_payload.go` | 51 + 7 twins; the differential (195 request files, 119 response files) |
| F3 | `permission-forwarding.ts` | `fwd_permission_forwarding.go` | 26 twins; the differential (648 target resolutions, 30 session ids) |
| F4 | `serving-registry.ts` | `fwd_serving_registry.go` | 14 twins |
| F5 | `forwarding-liveness.ts` | `fwd_liveness.go`, `fwd_proc_unix.go`, `fwd_proc_windows.go` | 41 twins; the differential (43 records, the sweep); the two-process test |
| F6 | `forwarding-manager.ts` | `fwd_manager.go` | 23 twins on a hand-advanced scheduler; one real-ticker test |
| F7 | `forwarded-request-server.ts`, `forwarder-context.ts` (and `forwarded-ask-payload.ts`, `decision-resolution.ts`, `decision-source.ts` as far as the server reads them) | `fwd_server.go`, `fwd_context.go`, `fwd_stub_authority.go` | 42 twins (6 named skips); the differential (843 serving-node scenarios); the two-process test |
| F8 | `subagent-registry.ts`, `subagent-context.ts`, `subagent-detection.ts` | `subagent_registry.go`, `subagent_context.go`, `subagent_detection.go` | 15 + 49 + 4 twins |
| F9 | `subagent-lifecycle-events.ts` | `subagent_lifecycle.go` (the bus adapter is over the SDK's `EventBus`) | 9 twins |
| F10 | `inherited-registrations.ts`, `child-node-audit.ts`, `delegation-envelope.ts` | `subagent_inherited.go`, `subagent_audit.go`, `fwd_envelope.go` | 14 + 8 + 9 twins; the differential (153 envelope verdicts) |

**Twins:** `pigeq twins check --files <the 15 files> --deferred port/slices.json`: 312 exact twins, 6 named skips, the 15 entries
removed from `port/slices.json`. The skips are the cases that drive the real `AuthorizerSelection`/`AuthorizerRegistry`/
`PermissionPrompter` (1) or the real `PermissionResolver` over a filesystem-backed `PermissionManager` (5); each gives its own reason
and is to be ported with the lane that owns the collaborator. The ledger holds only literal titles: the `it.each` cases (subagent env
hints, the `%s` surfaces and heartbeat states, the malformed session approvals, `EBUSY`/`EACCES`) run as plain subtests.

**Differential against the original's own functions** (`port/probe/gen-forwarding-cases.py` makes `forwarding-cases.json`;
`port/record-forwarding-probe.sh` runs `port/probe/forwarding-probe.test.ts` inside an installed copy of the oracle and writes
`testdata/forwarding_oracle.json`; `fwd_oracle_test.go` checks the port against it): the tolerant reads, the heartbeat states and
the dead-record sweep, target resolution, session id encoding, `resolutionFor` and `asDecisionSource`, the delegation envelope, and
843 serving-node scenarios (request shape x recorded authority x escalator outcome, plus a mixed inbox) run through the original's
`ForwardedRequestServer` and the port's. For each scenario the response file bytes (the read time aside), the files left behind,
the details escalated, the intents the policy saw, the broadcast, the recorded grants and every review and debug entry with its keys
in order are equal. The differences: the broadcast event is the logging lane's struct, so its key order is its own (compared as an
object); the messages of fs errors and of a malformed JSON file are worded as Node and V8 word them (`NodeFsError`, and a copy of the
config lane's `v8JSONError`).

**Two-process test** (`fwd_twoprocess_test.go`): the test binary re-executed as a serving parent (a `ForwardingManager` polling a real
directory on the real clock, publishing a heartbeat, answering through a `ForwardedRequestServer`), and the test process as the
forwarding child. An auto-approve by rule, an auto-deny with the rule's reason, an escalated ask approved for the session at family
width and a refused ask all cross the file protocol; the heartbeat reads alive across the process boundary, a clean stop withdraws it,
and a `kill -9` leaves a record that reads `dead_pid` without waiting out staleness (#735).

### What the Go process model cannot reproduce

The original shares three registries through `globalThis` between extension instances that Pi's loader (jiti) builds inside ONE
process: the subagent session registry, the serving session registry, and the permission service slots. PiG runs each extension in its
own process, so a `globalThis` is shared by nothing but one extension's own code. What ports, and what does not:

| Original behaviour | In Go |
|---|---|
| `getSubagentSessionRegistry()`: the parent registers children from `subagents:child:session-created`, a child's own instance reads the entry to detect itself and to resolve its target with `source: "registry"` | The registry is process-local (`subagent_registry.go`). The lifecycle events do reach every extension process of the parent's session through the SDK bus (`sdk.EventBus` is shared across realms, and `Emit` returns after every listener), so the parent's process fills its own registry, but a child session has its own bus and its own extension process, which never sees them. A child therefore cannot learn that it is a registered child: detection falls to the env hints and the session directory, and the target to the env candidates (`source: "env"`, the heartbeat channel). The original has no filesystem route for this, so none is invented here. **Owner decision:** either the spawner exports `PI_SUBAGENT_PARENT_SESSION` into an in-process child (it owes one variable by the adapter convention), or the parent writes a `children/<session id>.json` registration beside the heartbeats for the child to read. |
| `getServingSessionRegistry()`: an in-process child asks whether its target is marked serving | Process-local (`fwd_serving_registry.go`). A `registry`-sourced target can only arise when parent and child share a process, which PiG never does outside tests; every real forward is `env`-sourced and answered by the heartbeat file (`fwd_liveness.go`), which is the original's own route for out-of-process children. The two-process test shows both: the heartbeat answers where the registry cannot. |
| `AncestorNodes` and the inheriting extractor and formatter lookups (#793): a child reaches an ancestor's published `PermissionsService` through `globalThis` and runs its extractor closure | Ported as written over an injected locator, so it works when the ancestor is in the same process and answers nothing otherwise. A closure cannot be sent to another process and the original has no file route, so a split-provider child (the tool from one package, its extractor from an excluded one) loses the ancestor's extractor: its `path` and `external_directory` gates do not run for that tool, exactly the weakening the original's inheritance closed. **Owner decision:** treat a tool whose extractor is unknown in a child as `ask` in the child. |
| `ChildNodeAudit`: the parent asks whether the bound child's node published a service (`hasNode` reads the service slots) | The audit is ported with an injected `NodePresenceLookup`. In Go the lookup needs a cross-process signal (a marker file the child publishes under its session id); until the lead provides one, every child reads as having no node and the audit alarms, which is the safe direction. |
| `process.env` hints and the session-directory heuristic | Ported as written; both work across processes. |
| Request and response files, heartbeats, the poll, the sweep | Ported as written; this is the channel the two-process test exercises. |

Another difference of the model: Pi's `ExtensionContext` lives as long as the session, and `ForwardingManager` keeps it between
events. The SDK's `Context` is scoped to the request that carried it, so the lead's wiring has to hand `ForwarderContext` a
session-manager reader that does not depend on a finished request (a cached session id refreshed on each event); `ForwarderContext`
is a plain struct of interfaces so it can.

### Lane-local stand-ins (delete at integration)

The authority core, the session rules and the logging lane were not in this lane, so `fwd_stub_*.go` carry the smallest copies of
what the forwarding code reads, with the original's names: `fwd_stub_logging.go` (verbatim from the logging lane: `DebugReviewLogger`,
the decision event, the prompt payload types), `fwd_stub_paths.go` (`SurfaceFamilyOf`, `SurfaceFamilyMembers`, from the paths lane),
`fwd_stub_jsonerr.go` (the config lane's `v8JSONError`, verbatim) and `fwd_stub_authority.go` (decision source and its guard,
decision states and `PermissionPromptDecision`, `resolutionFor`, `PromptPermissionDetails`, `AskEscalator`, the authorizer verdict,
`SessionApproval`, `SessionRules`, `PermissionCheckResult`, the tool extractor and formatter registries). The twins in
`subagent_test.go` model the platform path flavors with a small `fwFlavor`; `SubagentPathFlavor` is the paths lane's `PathFlavor`
plus `Normalize`.

### Findings

1. **The request write races the serving node's cleanup, in the original too.** `ParentAuthorizer` ensures the child's directories
   and then writes once; the serving node removes an empty `requests/` after each drain. A write that lands between the two fails (ENOENT
   on Linux, EINVAL on macOS) and the ask is refused as undeliverable. The two-process test's child retries the pair; the original has
   no retry. Reported, not changed.
2. **A fixture lesson:** scenario files named `b.json` and `B.json` collide on a case-insensitive filesystem (macOS), so a corpus must
   not rely on them.
3. **Escalation can block for as long as a human takes.** `ProcessInbox` runs on its own goroutine, one at a time, as the original's
   un-awaited promise does, and the heartbeat refresh runs ahead of that guard so a session deliberating at a dialog still reads as
   serving.

### Results (forwarding lane)

- `go test -race ./...` passes; `-race -count=24` over the file protocol, liveness, manager, server and retry tests (the two-process
  tests included, 24 spawned parents) passes under the pinned CPU set of the shared host (201 s).
- `go vet` is clean for linux, darwin and windows.
- `pigeq mutate --unit-only` over `port/mutations-forwarding.json` (`port/gen-mutations-forwarding.py`): **178 mutations, all killed**.
  The first run killed 153 of 180: 21 did not compile (an unused variable once the defect was applied) and 6 survived. The 21 were
  rebuilt, and two mutants proved equivalent and are gone (a walk without a session id that ends the same way, and an `!ok` guard
  whose zero value already fails the check after it). The 6 survivors and 2 more that survived once rebuilt each got a test: the retry
  budget of an empty delay list, the walk over a hop that published no service and over a node with no parent, the one-hop canary
  for a registered requester without a parent, the context's session-id reader (trim and the `unknown` fallback), the real ticker's
  stop, and a session id that arrives together with an error. The mutants that only the differential kills are the response key
  order, the sweep rules, the heartbeat record shape and the serving-node log entries.
- The original's own tests for the 15 files pass in the oracle copy (`vitest run`, in the same log as the twin check).
- Not done in this lane: a Pi scenario (`pigeq run`) and a fused-binary check, because the layer has no effect in a trace until the
  lead wires it into `Extension()`; the interleaved benchmark against the original (`port/bench-forwarding.sh`), which the load rule
  holds back; `npm run check` and `npm run generate` (the lead runs them with the assembled versions).
