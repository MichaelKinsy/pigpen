# Port record: @gotgenes/pi-permission-system (partial: the rule engine, the ask dialog and name/path gating)

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
- the **ask**: the approval dialog (`authority/permission-prompt-decision.ts` and `local-user-authorizer.ts`'s decisions,
  rendered with PiG's `ui.select`/`ui.input` instead of the original's inline keybind component) — Approve, Approve for this
  session, Deny, Deny with a reason — with the user-denial and no-UI renders of `presentation/agent-renderer.ts` in the port's
  compact clause style; the session grants (`session/session-rules.ts`: allow rules appended after the config's at check time,
  a bare path family expanded to both members, cleared with the session; `presentation/pattern-suggest.ts`'s labels, with
  `<first token> *` where the original's arity table can suggest a longer exact prefix); a dismissal, a missing UI or a dialog
  failure denies fail-closed; `yoloMode` grants every ask, floors included;
- the **path and external-directory gates** for what the port can read (`handlers/gates/path.ts`,
  `external-directory.ts`, `access-intent/tool-input-path.ts`, `path-surfaces.ts`): a call's `path` input and the words of a
  simple bash command resolve against the working directory; inside it, explicit `path` rules decide (the original's
  backward-compatibility skip when only the universal default fired, #58); outside it, `external_directory_read` /
  `external_directory_write` decide per the direction the tool (or a small command table for a bash word) proves, and the bare
  family folds both members most-restrictive for what proves nothing (`edit`, an unknown command);
- the skill-read gate: a `read` of a file inside a skill that the system prompt lists is decided by the `skill` surface for that
  skill's name, so it asks while that surface asks (with the default `*: ask`, every listed skill), and without a session option,
  as in the original (its skill gates carry no SessionApproval);
- withholding tools the policy denies outright from the model at `before_agent_start`, and restoring them (the tool-surface
  baseline), the `yolo` status, and the permissive-`*` warning at session start.

**Refused, never allowed** (each is machinery the original has and the port does not; blocked with the Package's one refusal text, `@pi-in-go/pigpen-permissions (Go port) does not support <feature> yet. Install the original extension, npm:@gotgenes/pi-permission-system@39.0.3, which runs unchanged in PiG.` — `refusal.go`; `refusal_test.go` has one exact-text test per refused option and checks the Package name and the pinned original):

- a bash command that is not simple commands joined by `&&`, `||`, `;`, `|` or newlines (quoting, expansion, substitution,
  redirection, `&`, an assignment prefix, non-ASCII) **while bash has value patterns**: the raw text cannot be matched against
  rules the original matches against its parse. With no patterns the command's catch-all decides it — a deny or ask from the
  rule, and a floor ask (granted by `yoloMode`) when it allows, since it could hide a wrapper or a pattern match;
- a path-bearing tool (`read`, `write`, `edit`, `find`, `grep`, `ls`) whose own rules have path patterns: the tool-surface path
  matching (lexical plus canonical aliases) is not ported. The cross-cutting `path` and `external_directory` gates are (above);
- a tool that `shellTools` routes through the bash surface (the aliases are not ported);
- the MCP and skill surfaces (`mcp`, `skill` and `mcp__server__tool` tool calls);
- the project scope while the project is trusted (`ctx.IsProjectTrusted`; unknown counts as trusted) and a project config file exists,
  and the agent scope while the active agent (the last `active_agent` session entry, else the `<active_agent name="...">` tag of the
  system prompt) has a definition file with a `permission` frontmatter block: every call the port would allow is refused
  (`unported.go`, `scope_test.go`); a deny the global scope gives stays a deny. Before this the scopes were ignored, which let a call
  through that a project or agent rule denies;
- a `/skill:<name>` input: the `input` handler decides it by the skill surface (allow continues, deny is `handled` with the original's
  notice, an ask is put to the dialog and a dismissal denies, an unreadable scope is `handled` with the refusal text). Before this
  every input continued, so a denied skill could be invoked;
- config options the schema accepts and nothing reads yet (dialog keys, double-press, forwarding, logging, limits,
  `piInfrastructureReadPaths`, `authorizerChain`): a warning at session start names the ones that are set.

A wrapper running a pure reader (`sudo ls`, `nice echo`, `xargs grep`) asks: the original's floor exemption lets the inner
command's rule allow it; a unit starting with a command that runs another one the original reads by its own rule (`ssh`, `su`,
`source`, `command`, `bash script.sh`, ...) asks too. Both are stricter than the original and are listed, with every other
difference, in `testdata/port-differences.json`.

**Not ported at all, and diverging rather than refused:** path canonicalization (a pattern that names only a symlink-resolved or
otherwise spelled alias of a path is not matched; the port matches the path resolved against the working directory), the
original's inline keybind dialog and its `permissionDialogKeys`/`doublePressToConfirm` (PiG's picker instead), the bash arity
behind the exact-prefix session suggestion, forwarding of approvals between sessions and the authorizer chain, the review and
debug logs, the config modal, the cross-extension service, removing denied skills from the system prompt
(`withoutDeniedSkills`: a denied skill stays listed, and its files are not gated by the skill-read gate, as in the original).
A file that does not parse warns with the original's lead text but Go's parse error, not JavaScript's; a schema violation warns
once with the original's messages, where the original warns twice. PiG's `powershell` tool is a shell the original does not know:
it is decided by its own name (or the `*` fallback), not by the bash rules; deny it, or name it in `shellTools`, which makes the
port refuse it.

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
| M10 | `authority/permission-prompt-decision.ts`, `authority/local-user-authorizer.ts` (the decisions), `policy/permission-gate.ts`, `session/session-rules.ts`, `presentation/pattern-suggest.ts`, `presentation/agent-renderer.ts` (the user and unavailable renders) | `ask.go` | `ask_test.go`; the dialog flow through a fake interactive host (`askflow_test.go`) |
| M11 | `handlers/gates/path.ts`, `handlers/gates/external-directory.ts`, `access-intent/tool-input-path.ts`, `access-intent/path-surfaces.ts`, and the word-level part of the bash path gates | `paths.go` | `extras_test.go` (the gates); `ask_test.go` (the word scan); the harness-shape scenarios |

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

The ask and the path gates came later, driven by a real configuration that leans on both (a universal `*: ask` with the day's
tools allowed, `external_directory_read: allow` / `external_directory_write: ask`, and `bash: allow`): the refusals for an ask,
for path-bearing tools behind a non-open `external_directory`, and for the floors became asks and gate checks (`ask.go`,
`paths.go`), the oracle comparison learned to tell a port ask from a refusal (`TestBashGateAgainstOriginal`), and
`testdata/port-differences.json` was regenerated: 134 entries, 74 asks where the original decides and 60 commands still refused
against bash patterns. `isWithinDir` (the skill-read gate's containment test) learned the platform's `..` prefix, without which
a Windows session gated every read in the working directory as a skill file.

## Results

- `go test -race`: pass. 229 twins (`pigeq twins check --deferred port/slices.json`: exit 0). Against the original's recorded
  decisions: none of the 646 commands is allowed where the original asks or denies; 512 get the original's decision and text,
  134 are differences listed in `testdata/port-differences.json` (60 commands the port still refuses against bash patterns, 74
  asks where the original decides); all 91 config files get the original's verdict, messages (a parse error: Go's message) and
  output map; 48 of 48 wrapper units.
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
