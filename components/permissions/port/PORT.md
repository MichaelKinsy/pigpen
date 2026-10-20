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

**Refused, fail-closed** (the original decides these by machinery that is not ported). Each is blocked with the Package's one refusal text, `@pi-in-go/pigpen-permissions (Go port) does not support <feature> yet. Install the original extension, npm:@gotgenes/pi-permission-system@39.0.3, which runs unchanged in PiG.` (`refusal.go`; `refusal_test.go` has one exact-text test per refused option and checks the Package name and the pinned original):

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
- MCP and skill tools;
- the project scope while the project is trusted (`ctx.IsProjectTrusted`; unknown counts as trusted) and a project config file exists,
  and the agent scope while the active agent (the last `active_agent` session entry, else the `<active_agent name="...">` tag of the
  system prompt) has a definition file with a `permission` frontmatter block: every call the port would allow is refused
  (`unported.go`, `scope_test.go`); a deny the global scope gives stays a deny. Before this the scopes were ignored, which let a call
  through that a project or agent rule denies;
- a `/skill:<name>` input: the `input` handler decides it by the skill surface (allow continues, deny is `handled` with the original's
  notice, an ask or an unreadable scope is `handled` with the refusal text). Before this every input continued, so a denied skill
  could be invoked;
- config options the schema accepts and nothing reads yet (dialog, forwarding, logging, limits, `piInfrastructureReadPaths`,
  `authorizerChain`): a warning at session start names the ones that are set.

**Not ported at all:** the project and agent scopes of the permission policy itself (the config system reads them, see "The config
system" below; the policy that consumes `FilePolicyLoader` is the policy lane's), session approvals and rules, the review and debug logs, forwarding and subagent authority,
the cross-extension service, removing denied skills from the system prompt (`withoutDeniedSkills`: a denied
skill stays listed, and its files are not gated by the skill-read gate, as in the original). A file that does not parse warns with the original's text, V8's JSON.parse message included. PiG's `powershell` tool is a shell the original does not know: it is decided by its own
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
| M8 | `config/config-loader.ts` (`stripJsonComments`, `validateUnifiedConfig`), `config/config-schema.ts` | `schema.go`, `config.go`, `jsonerr.go` | 91 recorded files (`oracle_test.go`); 1061 more (`cfg_oracle_test.go`); scenario `config-comments` |
| M10 | `config/*` (loader, merge, scopes, paths, store, issue reporter, reporter, status, yaml frontmatter, dialog keys, extension config and paths, policy loader, modal), the config parts of `index.ts`, `handlers/lifecycle.ts`, `session/permission-session.ts`, `handlers/session-turn-prep.ts` | `cfg*.go`, `policyloader.go`, `extconfig.go`, `yamlfm.go`, `dialogkeys.go`, `icucompare.go`, `schemas.go`, `extension.go` | 305 twins + 6 named skips in 15 files; `cfg_oracle_test.go` (recorded decisions); `cfg_adapter_test.go`; `cfg_modal_component_test.go`; `mutations-config.json` |
| M8 | `access-intent/bash/{parser,async-cache,heredoc-tails,redirect-arguments,parse-health,heredoc-free-lines,node-text,shell-variable-expansion,unresolved-salvage,nested-execution,redirect-analysis}.ts`: tree-sitter-bash 0.25.1 through web-tree-sitter 0.26.9 and the two corrections | `bashparse/` (pure Go: `internal/ts` ports the tree-sitter 0.26.9 runtime, the grammar tables and scanner) | 117 twins and 2 named skips (`pigeq twins check`); differential against the original's recorded parse trees, every node (`bashparse/differential_test.go`, `port/bashparse-diff/`): 4302 original-test strings with full dumps, 5753 other test strings, 52043 hand-built shapes, 212925 prefix and deletion variants, 219159 fuzz inputs, zero differences (one input, an 1800-deep nest of empty subshells, makes the original's wasm trap) |
| M9 | `handlers/gates/skill-read.ts`, `exposure/skill-prompt-sanitizer.ts` (parsing, visibility, path match) | `skills.go` | 15 twins; `skills_test.go`; `adapter_test.go` |
| M10 | `access-intent/bash/{command-enumeration,wrapper-analysis,command-effects,bash-arity,sed-invocation,awk-invocation,sync-commands,token-classification,token-collection}.ts` | `bashanalysis/` (subpackage; reads trees through `bashparse/`; `wrapper-analysis.ts` moves here from M7's `bash.go` once the lead swaps callers; `path/path-flavor.ts`'s `hasPathSeparator` is a minimal copy for the classifiers) | 298 twins in 9 ledger files; 42,038 recorded inputs compared field by field with the original (`bashanalysis/differential_test.go`, `port/bashanalysis-diff/`); `bashanalysis/extras_test.go`; 383 mutations (`port/gen-mutations-bashanalysis.py`) |
| M11 | `logging/*` (debug and review JSONL writers, redaction, field cap, JSON-safe stringify, file modes, session logger, decision audit and reporter, command masking), `presentation/review-log-renderer.ts`, `presentation/fact-vocabulary.ts`, `handlers/tool-call-boundary.ts` | `logjsonsafe.go`, `logredaction.go`, `logfieldcap.go`, `logperms.go`, `logging.go`, `sessionlogger.go`, `decisionaudit.go`, `decisionreporter.go`, `permissionevents.go`, `commandredaction.go`, `reviewlogrenderer.go`, `factvocabulary.go`, `toolcallboundary.go` (lane-local stand-ins in `logging_deps.go`) | 135 twins (`logging/*`, the two presentation files, `handlers/tool-call-boundary`); the differential `testdata/logging-oracle.json` |

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

## Bash parser: performance (M8)

`bashparse` is new in this release, so there is no earlier release to compare with; the baseline is the original's WASM parser
(web-tree-sitter 0.26.9 + tree-sitter-bash 0.25.1) under Node 24.19. Latency is the median of 12 interleaved rounds (a Go round,
then a WASM round) of `bench-rounds.sh`, both pinned to the same four CPUs of one host (`port/bashparse-diff/bench-inputs.json`;
the Go figure is `BenchmarkParse`, tree conversion and both corrections included):

| input | Go ns/op | WASM ns/op | Go / WASM | Go B/op | Go allocs/op |
|---|---|---|---|---|---|
| 41-byte command | 16 297 | 47 966 | 0.34 | 2 322 | 7 |
| 509-byte pipeline with a heredoc | 653 837 | 1 010 090 | 0.65 | 34 378 | 112 |
| 5.2 KB script | 1 950 225 | 2 162 238 | 0.90 | 252 693 | 397 |

Cold start (tables load, then the first parse): 6.3 ms against 40 ms (median of 12) for the WASM parser's init, load and first
parse. Binary: +1.42 MiB (1 490 944 bytes) over an empty `main` (`-ldflags="-s -w"`, linux/amd64), of which 142 KB are the
gzipped tables.

Before the change recorded here the 509-byte and 5.2 KB inputs ran at 3.9 times the WASM time on a comparable idle four-CPU set
of the same host (3 882 us and 8 283 us, 6 721 and 24 162 allocations per parse, 10 interleaved rounds): the cost was the garbage collector's work on the
parse's garbage (the same code ran 2 to 2.6 times faster given 56 CPUs than given four; the new code with the collector off is a quarter to a
third faster than with it on). The fix keeps a parser's subtrees, stack nodes and
child arrays in slabs it reuses between parses (parsers come from a `sync.Pool`; a tree is converted before its parser is handed
back, and a parser lets go of slabs a one-off huge input grew), builds the node tree in two allocations sized from the tree,
expands a small parse state to a dense row on first use, counts references for balancing in a field instead of a map, and copies
children in the correction walk only where something changed. Every step was held to the differential: zero differences on every
corpus, the twins green, the `bp-*` mutations killed. `TestTreeSurvivesTheNextParse` and `TestParallelParsesMatchSerialOnes`
(under `-race`) pin the two things reuse could break: a tree held while another parse runs, and parallel callers.

## Findings (to file against PiG, and notes on the porter)

1. **Scenarios need `agentFiles` for a global config, and `commands` for a command the original runs that the port does not.**
   The original runs `npm root -g` when it starts from a checkout; declaring `npm` as `missing` keeps `exec-coverage` green and
   leaves no event in the trace.
2. **The project scope is withheld without trust.** A scenario that writes the config into the project sees no rules at all; the
   original needs a trusted project. `Context.IsProjectTrusted()` (PiG 0.5) is the query; the config system uses it, the permission policy still reads the global scope only.
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

## The config system (lane PermConfig)

Everything under `src/config` is ported, with the original's own tests as twins and its own functions as the oracle.

**Files.** `cfgpaths.go` (config-paths, extension-paths), `jsonerr.go` (the wording of V8's `JSON.parse` errors, because the loader reports
`error.message`), `cfgfs.go` (UTF-8 decoding as Node's `readFileSync` does it, Node's error wording, `String(mtimeMs)` stamps),
`schema.go` (the zod schema, extended: zod 4's safe-integer bound, the second issue zod reports for an empty array where a string is
expected, and its silence about a `__proto__` key), `schemas.go` (the committed JSON Schema, embedded), `cfgloader.go` (`UnifiedConfig`,
`loadUnifiedConfig`, `mergeUnifiedConfigs`, `loadAndMergeConfigs`, the three detectors), `extconfig.go`, `dialogkeys.go`, `yamlfm.go`,
`policyloader.go` (`PolicyLoader`, `FilePolicyLoader`), `cfgstore.go` (`ConfigStore`, `ConfigIssueReporter`, the resolved-paths log entry,
status), `cfgmodal.go` (`/permission-system`), `icucompare.go` (`localeCompare` for the MCP server name order).

**Lifecycle (`extension.go`).** `session_start` asks the host `IsProjectTrusted()` (a failed answer is not trust) and reads the merged
config for the session's cwd, with the project scope only when trusted (#644); then it syncs the status bar, logs the resolved paths,
says what is wrong with the config once per issue (`ConfigIssueReporter`, #933) and, for an untrusted project, warns that only the global
policy applies. `before_agent_start` re-reads the config under the current trust and reports what is new; `resources_discover` with
reason `reload` rebuilds the loader paths. The factory-time priming load reads no project. What this does not yet do is feed the
project, agent and project-agent scopes into the permission decision: `Policy` still composes the global scope only, until the policy
lane consumes `FilePolicyLoader`.

**Exported Go API for the policy lane.** `PolicyLoader` (`LoadGlobalConfig`, `LoadProjectConfig`, `LoadAgentConfig(name)`,
`LoadProjectAgentConfig(name)`, `GetConfiguredMcpServerNames`, `GetCacheStamp(name)`, `GetConfigIssues`, `GetResolvedPolicyPaths`),
`FilePolicyLoader` built by `NewFilePolicyLoader(PolicyLoaderOptions)` (`derivePolicyLoaderOptions(agentDir, cwd)` gives the manager's
paths, `cwd == ""` for a withheld project), `ScopeConfig{Permission *jsObject, Invalid bool}` (`Invalid` drives the fail-closed
allow-to-ask clamp), `ResolvedPolicyPaths`, `ConfigStore` (`NewConfigStore`, `Refresh(cwd, trusted)`, `Current()`, `GetConfigIssues()`,
`Save`, `LogResolvedPaths`), `PermissionSystemExtensionConfig`, `UnifiedConfig`, `loadAndMergeConfigs`, `resolveDialogKeys`.

**Checked against the original.** `port/record-config-probe.sh` runs `port/probe/config-system-cases.json` (generated by
`gen-config-system-cases.py`) through the original's `loadUnifiedConfig`, `loadAndMergeConfigs`, `FilePolicyLoader`, `ConfigStore.save`,
the `/permission-system` handlers, `resolveDialogKeys` and the frontmatter parser and records `testdata/config-oracle.json`:
1061 config files (every field with edge values, JSONC, duplicate keys, key order, `__proto__`, huge numbers, unicode, raw invalid UTF-8
bytes, every prefix of a document, a seeded fuzz), 86 loader scenarios (project and agent scopes, trust on and off, invalid scopes,
legacy files, MCP name discovery and its collation order), 94 frontmatter texts, 426 dialog-key maps, 17 saves, 12 command scenarios, and
pi-tui's key facts. Zero unexplained differences.

**Explained differences.**

1. A lone UTF-16 surrogate in a string (a `"\ud800"` escape) is held as U+FFFD, since a Go string is UTF-8; the original's
   `JSON.stringify` would write the escape back. The recorded case is skipped for that field only.
2. The frontmatter parser drops a `__proto__` key where the original, for a nested one (`__proto__:` with children), would change the
   object's prototype and could make `parsed.permission` read an inherited value. The agent file's author holds the same authority
   either way.
3. `localeCompare` (the order of same-length MCP server names) is ICU's root collation in the original; the port reproduces it for
   ASCII and Latin-1 accents and approximates other scripts (`icucompare.go`). The order of two same-length names never changes a decision.
4. The legacy extension config (`<npm package>/config.json`) has no Go counterpart: there is no install directory, so `extensionRoot`
   is empty and that file is not looked for. `loadAndMergeConfigs` takes the root as an argument and the twins pass one.
5. An astral character at the position of a `JSON.parse` error shows as U+FFFD in the message (V8 shows its lone surrogate).
6. `ConfigStore.Save` takes the typed config, so the original's `normalizePermissionSystemConfig(next)` on an untyped object is the
   caller's.

**Upstream quirks kept.** `save()` writes only `debugLog`, `permissionReviewLog` and `yoloMode`, so the modal's double-press toggle is
not persisted; an existing key keeps its place in the file and a missing one is appended. A rejected existing global file is replaced by
`{ debugLog, permissionReviewLog, yoloMode }` alone (it loads as empty), dropping what the file held.

**SDK gaps.** Project trust: `Context.IsProjectTrusted()` exists and is used. `ctx.ui.custom`: `Context.Custom` takes a
`ViewComponent`; the modal is a `kit.SettingsList` in an overlay with the original's options (`anchor: center`, `width: 82`,
`maxHeight: 85%`, `margin: 1`, 10 visible rows). Partial: the host owns a settings list's values, so the original's
`syncSettingValues` (`SettingsList.updateValue` after a save, which reverts a row when the save failed) has no counterpart; the next
frame shows `ConfigStore.Current()`, but a failed save leaves the row on the value the user chose until the modal closes. The input
and rendering of the modal are the host's, so the fake host cannot drive them: the component is tested directly
(`cfg_modal_component_test.go`), the command's routing through `Context.Custom` by the twins.

**Mutations.** `port/mutations-config.json` (`gen-config-mutations.py`), judged by the Go tests alone
(`pigeq mutate --unit-only --go extensions/pi-permission-system --sdk-dir <PiG sdk> --mutations port/mutations-config.json`).

## Path layer (lane `paths`)

The shared layer the gates build on: Node's `path` module for both platforms, the path flavors, symlink canonicalization,
`AccessPath`, `PathNormalizer`, the access-intent types, tool kinds, MCP targets, input normalization and the shared types. Twins
first (all 472 upstream twins of the files below), then the code, then a differential against the original's own
functions.

| ID | Original | Go | Checked by |
|---|---|---|---|
| P1 | Node's `path.posix` / `path.win32` (v24.19: resolve, normalize, join, relative, isAbsolute, dirname, basename, parse root), the win32 reserved device names and `\\.\` roots | `nodepath.go` | 34,626 recorded results (`paths_oracle_test.go`), none different |
| P2 | `String.prototype.toLowerCase` (the win32 fold) | `jscase.go` | all 1,112,064 scalar values and 20 sigma strings against Node |
| P3 | `fileURLToPath(url, { windows })` over WHATWG URL parsing for `file:` | `fileurl.go` | 248 URLs, both platforms |
| P4 | `path/path-flavor.ts`, `access-intent/bash/msys-bash-tokens.ts`, `path/safe-system-paths.ts` | `pathflavor.go`, `msysbash.go`, `safepaths.go` | twins; recorded `toolShellPath` against Pi's `normalizeWindowsShellPath`, `bashTokenShape`, folding, containment |
| P5 | `path/canonicalize-path.ts`, `path/path-containment.ts`, `path/approval-pattern.ts`, `path/pi-infrastructure-read.ts`, `path/node-modules-discovery.ts`, `path/expand-home.ts` | `canonicalize.go`, `pathcontainment.go`, `approvalpattern.go`, `infraread.go`, `nodemodules.go`, `wildcard.go` | twins; 202 approval rows per flavor; 783 infrastructure-read rows |
| P6 | `path/native-tool-target.ts` | `nativetarget.go` | twins; 5,560 recorded targets (existence sets, read fallbacks, both flavors); Pi's `resolveReadPath` / `resolveToCwd` on a real layout (`testdata/native-target.json`) |
| P7 | `access-intent/{path-normalization,access-path}.ts`, `path/path-normalizer.ts` | `pathnormalization.go`, `accesspath.go`, `pathnormalizer.go` | twins; 1,212 rows per flavor; 6,384 intents |
| P8 | `access-intent/{access-intent,effect,path-surfaces,tool-kind,tool-input-path,input-normalizer,mcp-targets}.ts` | `accessintent.go`, `effect.go`, `pathsurfaces.go`, `toolkind.go`, `toolinputpath.go`, `inputnormalizer.go`, `mcptargets.go` | twins; 1,020 tool kinds, 740 MCP target lists, 593 normalizations |
| P9 | `types.ts`, `value-guards.ts`, `permission-request-id.ts`, `handlers/gates/types.ts` | `types.go`, `valueguards.go`, `requestid.go` | twins |

How it is checked:

- `port/record-paths-probe.sh <installed oracle>` runs `port/probe/paths-probe.test.ts` inside the original's package and writes
  `testdata/paths-oracle.json` and `testdata/native-target.json`; `paths_oracle_test.go` replays them. Every difference is a
  failure unless listed with its reason in `testdata/paths-differences.json` (one: `/dev/stdin` is a symlink into `/proc` on the
  recording host).
- Mutations: `port/gen-mutations-paths.py` writes `port/mutations-paths.json` (280 mutations: the first run of 302 left 63 alive; 22 were equivalent and are
  left out with the reason in the generator, the rest got tests or a corpus row and are killed, the last 6 that did not compile were rebuilt); run
  `pigeq mutate --unit-only --go extensions/pi-permission-system --sdk-dir <PiG sdk> --mutations port/mutations-paths.json`.
- The win32 flavor is pure string logic and is tested on every OS; only the real-filesystem tests (`entryExists`, the parity
  layout, symlinks) depend on the host.

Differences from the original, on purpose:

- Indexes are bytes where the original counts UTF-16 units (`LastSeparatorIndex`); every caller slices the same string with it.
- `fileURLToPath` takes a host through the mapping part of UTS #46 (NFKC, case, ignored characters, `xn--` labels, IPv4, IPv6) but
  not its validity tables, so a host the original rejects for a reason in those tables is accepted: the result is a UNC prefix on
  win32 and a failure on POSIX either way.
- `getPackageDir()` (Pi's install directory) has no counterpart in PiG's Go SDK (`sdk.AgentDir()` is the only host path): the
  caller that builds the infrastructure roots must pass it, and until the SDK has it that root is missing.
- `ScopeConfig` lives with the config layer; `ShellToolsConfig` here is a map of `ShellToolAlias`.

## Approval dialog and authorizer chain (lane `authority`: `src/authority`, the approval-dialog half)

Lane `authority` (branch `perm-authority-1`). The original asks a human with an inline `ctx.ui.custom` dialog (or `select`/`input` outside
the TUI), serializes those dialogs, runs the operator's `authorizerChain` before the terminal authorizer, and a subagent escalates to
its parent. This lane ports that whole half as library code in the module root (`auth_*.go`, `uiprompt.go`); `Extension()` does not
call it yet (the gate lane wires it), so the root gate still refuses what it cannot read. It replaces the authority stand-ins of
`fwd_stub_authority.go` (decision source, decision resolution, prompt decision, `AskEscalator`, authorizer types) in the same
commit; the forwarding twins pass unchanged.

| ID | Original (`src/authority/`) | Go | Checked by |
|---|---|---|---|
| A1 | `decision-source.ts`, `decision-resolution.ts` | `auth_decision_source.go`, `auth_resolution.go` | 10 + 18 twins; the forwarding differential |
| A2 | `permission-dialog.ts` | `auth_permission_dialog.go` | 31 twins; the differential (975 scripted answers) |
| A3 | `permission-prompt-decision.ts` | `auth_prompt_model.go` | 32 twins; the differential (7,200 event sequences, 16 configs) |
| A4 | `permission-prompt-component.ts`, `bracketed-paste.ts`, pi-tui `Input` | `auth_prompt_component.go`, `auth_input.go`, `auth_bracketed_paste.go` | 39 + 8 twins; the differential (368 key-sequence scenarios, every frame at two widths; pending the recording) |
| A5 | `authorizer.ts`, `denying-authorizer.ts`, `authorizer-chain.ts`, `authorizer-chain-audit.ts`, `authorizer-registry.ts`, `authorizer-selection.ts` | `auth_authorizer.go`, `auth_chain.go`, `auth_chain_audit.go`, `auth_registry.go`, `auth_selection.go` | 15 + 2 + 7 + 8 + 12 + 34 twins; the differential (800 chains, 72 selections) |
| A6 | `local-user-authorizer.ts`, `ask-dialog-queue.ts`, `permission-prompter.ts` | `auth_local_user.go`, `auth_queue.go`, `auth_prompter.go` | 24 + 8 + 17 twins; the differential (458 asks, 180 review entries) |
| A7 | `approval-escalator.ts` (`ParentAuthorizer`) | `auth_escalator.go` | 26 twins on the real filesystem and clock |
| A8 | `service/permission-ui-prompt.ts`, the `permissions:ui_prompt` half of `service/permission-events.ts` | `uiprompt.go` | 8 + 3 twins; the differential (96 events) |
| A9 | the host seam of `ctx.ui.custom` | `auth_sdk_ui.go` (a `sdk.RemoteComponent` over `Context.Custom`) | `auth_sdk_ui_test.go` |

**Twins:** `pigeq twins check --files <the 16 authority files and service/permission-ui-prompt> --deferred port/slices.json`: 299 exact
twins, 0 skips; the 16 `authority/*` entries and `service/permission-ui-prompt` are removed from `port/slices.json`, and
`service/permission-events` is narrowed (its `ui_prompt` cases are twinned, the ready and decision halves are not). The `it.each` rows
with a `%s` title (malformed decision sources, "returns a %s source unchanged") run as plain subtests.

**Differential against the original's own modules (generator, probe and replay written; the recording is pending)** (`port/probe/gen-authority-cases.py` makes `authority-cases.json`;
`port/record-authority-probe.sh` runs `port/probe/authority-probe.test.ts` inside an installed copy of the oracle and writes
`testdata/authority_oracle.json`; `auth_oracle_test.go` replays it): the dialog model, the `select`/`input` fallback, the chain, the
bracketed-paste collapse, `LocalUserAuthorizer` (the `permissions:ui_prompt` event, the notice, the request options), the prompter's
review entries, `selectAuthorizer`, `buildUiPrompt`, `requestPermissionDecision`'s fallback, and the inline component under key
sequences: for each key, the rendered lines at widths 30 and 80 (a tag theme marks every colored span with its token), the render
requests, the tool-expansion toggles, the terminal writes and the decision. The sequences include the decision keys under every key
set, uppercase, control and alt chords, Kitty CSI-u presses, shifted presses and releases, bracketed pastes (stray, multi-line,
partial), and the reason editor's keys (cursor, word, kill, yank, undo, wide and combining characters, a paste longer than the row).

**Mutations:** `port/gen-mutations-authority.py` writes `port/mutations-authority.json` (287 mutations, one per behaviour); run
`pigeq mutate --unit-only --go extensions/pi-permission-system --sdk-dir <PiG sdk> --mutations port/mutations-authority.json`.

### How the original's promises and callbacks map

- A promise is a blocking call. `AskDialogQueue` hands out an `AdmittedAsk` (admission order is the call order) whose `Wait` blocks;
  `ReleaseAll` settles every outstanding ask with its own released value, a stale presentation that returns after its release is
  discarded, and a panicking presentation rejects only its own ask.
- `ctx.ui.custom(factory)` is `PermissionPromptUi.Custom(factory, options)`: the factory receives the same four things (the TUI's
  render request and terminal write, the theme, the keybindings, `done`) and the call blocks until `done`. A dialog the host closed
  without a decision is an error (`errDialogClosed`), never an approval; the gate turns an error into a block.
- Every host effect of the component (render request, tool-expansion toggle, terminal write) runs outside its lock, and `Render` is a
  pure function of the state (no host call): a host that repaints synchronously from the render request cannot deadlock.
- `UnattributedDecision` is a type of its own (the original's `Omit<PermissionPromptDecision, "decidedBy">`): a producer that only
  states an outcome cannot be handed on as a decided one, so the "decider is never missing" guarantee is the compiler's, as in TypeScript.

### Differences from the original

- A chain link's error (the original's rejected promise) is returned as an error and decides nothing; the original's `undefined`
  verdict throws a `TypeError` where a Go zero verdict (`Kind` empty) defers. Both end at the terminal or at a block, never at an allow.
- `AuthorizerRegistry`'s disposer is guarded by the registration, not by the function value: a stale disposer cannot evict a later
  registration even of the same function (the original evicts it).
- `PromptPermissionDetails.Surface` and `.Value` are `*string`: an explicit `null` display override on a direct ask (the original
  distinguishes it from "derive") is not representable. No producer sets it: the serving node builds details with no tool, command,
  path, target or skill name, so deriving yields the same `null`. Recorded by the differential's excluded corpus.
- `AuthorizerSelection.Activate` returns the error of a failing subagent detection (a session-directory read) instead of throwing, and
  stores no selection then.
- The original's `Input` reads pi-tui's global keybindings manager on every key; so does `LineInput` (`tui.GetKeybindings`). A host
  adapter that wants the user's keybinding overrides must `tui.SetKeybindings` once.
- pi-tui has no Go counterpart for `Input` (pitui ports the `Editor` only), so `LineInput` (`auth_input.go`) is a port of `input.ts`
  from the pinned Pi over pitui's kill ring, key matcher, word navigation and width helpers; its mouse handler is not ported (the dialog
  never routes a mouse event to it).

### SDK gaps (reported to the Tern lead, not worked around)

- `tui.terminal.write` (the prompt notification's raw bytes: bell, OSC 9, OSC 777): the SDK has no terminal write. `NewSDKPromptUi`
  takes an optional `write` callback; with `nil` the notification is dropped. The gate lane must refuse a configured
  `promptNotifications` until the SDK has it, or take the gap.
- The host's `KeybindingsManager.matches` (the `app.tools.expand` action and pi-tui's global manager with the user's overrides): the
  adapter takes a `tui.Keybindings`; the caller builds it from `<agent dir>/keybindings.json` as the questionnaire does.
- `ctx.ui.custom`'s theme is `Context.UITheme()` (present), its `tui.requestRender` is the remote component's invalidation callback
  (present).

### Findings

- Not yet established: the differential's recording and replay were queued on the shared CPU set when this section was written (`testdata/authority_oracle.json` and `auth_oracle_test.go` are added by the commit that records them). Until then nothing here claims the port equals the original beyond the twins.
- Inherited from the original, by design: the decision step opens with `approve` highlighted and Enter confirms the highlight in one
  press even with double-press confirmation on, so a stray Enter at the moment the dialog opens approves. The port keeps it (a
  difference would be a policy change); see the security review in the PR body.
