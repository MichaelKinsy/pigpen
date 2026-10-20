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
