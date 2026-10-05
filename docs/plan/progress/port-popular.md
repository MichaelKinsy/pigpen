# Progress: port-popular (the porter on six popular Pi extensions)

Lane `port-popular`. Task: port popular real Pi extensions to native Go SDK extensions with the Pigpen porter, one commit series
each, and measure the porter. Run on pig 0.4.0 + Pi 1.0.0, go1.27.1, Node 24.19.0. Every golden trace is recorded from the
ORIGINAL under Pi, never from the port. The task named five ports; the lead added a sixth (pi-subagents).

## Status

| # | Port | Original (license) | Status | Scope |
|---|---|---|---|---|
| 1 | `components/todo` | `@juicesharp/rpiv-todo` 2.11.0 (MIT) | reviewed (ACCEPT-WITH-FIXES), merged | tool, `/todos`, overlay, collapse key; English only (E1) |
| 2 | `components/ask-user-question` | `@juicesharp/rpiv-ask-user-question` 2.11.0 (MIT) | reviewed (ACCEPT-WITH-FIXES), merged | **partial** (slice A: the dialog walker); the tabbed questionnaire is not ported |
| 3 | `components/powerline` | `pi-powerline-footer` 0.19.1 (MIT as declared in its package.json) | reviewed (ACCEPT-WITH-FIXES), merged | status bar and `/powerline` (no custom editor) |
| 4 | `components/goal` | `pi-goal-x` 0.32.3 (MIT) | reviewed (ACCEPT-WITH-FIXES), merged | **partial**: goal files, pool, ledger, scheduler restore, direct commands; no continuation, drafting or auditor |
| 5 | `components/permissions` | `@gotgenes/pi-permission-system` 39.0.3 (MIT) | reviewed (ACCEPT-WITH-FIXES), merged | **partial**: rule engine, the `tool_call` gate by tool name and for simple bash chains (wrapper floors), the skill-read gate; no approval dialog, full bash parsing or path rules |
| 6 | `components/subagents` | `pi-subagents` 0.73.1 (MIT) | reviewed (ACCEPT-WITH-FIXES, rev-port-popular-5), to merge | **partial**: definitions, discovery, `subagent` list/get, the loader, plus a Go-native single-agent and chain run (depth 2, 4 at once, cancellable) |

All six licenses permit a port; none was skipped. All six `ports/ports.json` rows are at `review`; `piglets/pig-popular` carries
all six as members, and its catalog notes credit all six.

## Measured, per port

Wall time is from a port's first commit to the next port's first commit (oracle and ports row to oracle and ports row), reviews and
merges excluded; port 1 includes about 20 minutes of setup (pigeq, staged SDK, isolated environment, clones) before its first
commit. The lane has no model-turn counter, so **model turns are not available**; the count of commits and of scripted
Pi sessions stands in. Lines are hand-written non-test Go (the helpers `jsjson.go` and `jsutil.go`, about 375 lines, are copies in
ports 3 to 6) and test Go (the two Skill templates included), at the merged or final state.

| Port | Wall | Twins (exact / named skips) | Scenarios (Pi) | Mutations killed | Non-test Go | Test Go | Generated (data, not Go) | `pigeq gaps`, Go side |
|---|---|---|---|---|---|---|---|---|
| 1 todo | ~1h10m + setup | 209 / 22 | 16 | 105 of 105 | 2,293 | 3,073 | none | 0 blocking |
| 2 ask-user-question | ~1h20m | 148 / 25 | 16 | 87 of 87 | 1,006 | 2,501 | none | 1 accepted (E2 overlay handle) |
| 3 powerline | ~2h00m | 82 / 25 | 6 (+178 render states) | 152 of 152 | 4,103 | 2,347 | render tables from the oracle | 0 blocking |
| 4 goal | ~1h35m | 41 / 4 | 17 (+86 replayed sessions) | 210 of 210 | 3,482 | 2,181 | recorded cases and goldens | 1 PARTIAL, 0 blocking |
| 5 permissions | ~1h25m | 229 / 0 | 8 | 171 of 171 | 2,120 | 2,528 | none | 1 PARTIAL, 0 blocking |
| 6 subagents | ~1h10m (+15m merges) | 41 / 0 | 5 (+1 host-bound) | 116 of 116 | 2,880 | 2,128 | `tooldefs.json` (read from the original's request), `builtin/*.md` | 1 PARTIAL, 0 blocking |

The mutation counts are the final runs after review (ports 1 to 6; port 6 had 74 of 74 before its review, which added 42 for the
run path, and 364 non-test and 566 test lines). "Named skips" are upstream cases with no Go twin and a specific reason; whole upstream files outside a slice are deferred by name in each port's `port/slices.json`
(`pigeq twins check --deferred`, exit 0 for every port).

Every port ran in an interactive pig 0.4.0 in a detached tmux (`port/demo/pane-*.txt` per port; machine paths replaced by `<run>`).

## Friction, ordered by cost

1. **Behavior that is invisible over RPC** (goal, powerline, ask). The Pi RPC trace shows host, llm, ui, response and exit only.
   For goal-x, whose product is files and a ledger, the equivalence check needed a second layer: a driver that runs the ORIGINAL
   in-process with a scripted host and records 70 to 86 sessions (`port/drive`, `gen-cases.py`), plus a replay test. This was the
   largest hand cost of the lane (the goal port). A skeleton for "invisible over RPC" ports would save the most.
2. **Slicing a large original** (permissions 28.9k lines, subagents 102k lines, goal 1,053 tests). No tool helps choose the slice;
   it came from reading tests. The twin ledger is per upstream file, so a partially twinned file needs a whole-file deferral with a
   reason. `pigeq twins list` skips parametrized (`test.each`) titles.
3. **Oracle environments.** Node packages needing a lockfile install, a monorepo (permissions needed the root workspace files), a
   `profiles` test that fails at the pin in a pristine clone (subagents), SKILL.md files in an oracle that the skill-name gate
   rejected, large images left out. The oracle subset and its install must be written down (`UPSTREAM.md`).
4. **JavaScript semantics.** Regex `.` and `\s` (U+2028/9), UTF-16 units, `Number` formatting, `trim`, `localeCompare`, object key
   order (integer-like keys first), V8 JSON error text. Every port that parsed text paid it; `jsjson.go` and `jsutil.go` were
   copied into four ports. A helper library would remove 375 lines per port and a class of review findings.
5. **Review findings by lesson** (applied to later ports): `twins check` must exit 0 with no blanket skip reasons; never claim "N/N"
   without re-running (flaky mutants need several runs); keep `fakehost_test.go` and `twin_test.go` byte-identical to the Skill
   templates; twin inputs and assertions are upstream's, not paraphrased; `ports.json` notes carry no planning text; run
   `npm test` and `npm run check` before `review`.
6. **Fused Binary traces include sibling members.** A Piglet Binary of several ports fails `pigeq check --builtin` for each member
   because every member sees the others' startup events (powerline's `setStatus`), and a Binary's default active tools
   (`powershell`, `grep`, `find`, `ls`) differ from Pi's (`read`, `bash`, `edit`, `write`) in the model request. Each port is therefore
   checked with a Binary of that member alone, outside the `llm` channel. A per-member filter and a tool-set pin are needed.
7. **Running child processes** (subagents). The lane's run path had no test against a real process and no mutation over it;
   its review found a task read as pig options (`- fix it` failed with `Unknown option`), no nesting bound (a Piglet Binary keeps
   its fused members under `--no-extensions`, so a Binary that starts itself nested without end), no fan-out cap, children that
   outlived an aborted run or a killed host, and the host's internal variables passed on. A Skill section on starting processes
   (stdin, depth, cap, cancellation, environment, output bound) would have prevented each.
8. **Harness defects found and fixed here:** the normalizer skipped exec args and request messages (fixed with a test);
   `commands` mode `missing` for `git` hides `/usr/bin` from the host launcher (use `canned`); `exec-coverage` and `sdk-gaps` fail for
   the whole original and cannot be scoped to a slice; `scripts/pig-requirement.json` asks for pig 0.3.x while 0.4.0 is used
   (worked around by hand).
9. **Honest process.** Tests-first held for the rule engines and pure-logic modules (red commits for permissions, subagents, the
   run addition). It did not hold where the observable is recorded from the original first (scenarios, then code): todo, ask,
   powerline cores; goal (oracle driver and cases first, replay test after); the gate, config and exposure of permissions; the
   extension surface of subagents. Each PORT.md says which.

## PiG SDK and host gaps to file

The ids are the lane's running numbering (G9 was never filed); "seen in" lists the ports whose PORT.md or review names the gap, and is best effort.

| Id | Gap | Seen in |
|---|---|---|
| G1 | No component-widget renderer: `SetWidget` takes lines only | todo, powerline, goal |
| G2 | No pi-tui text helpers (width, wrap, truncate) | todo, ask, powerline |
| G3 | PiG's RPC emits factory-widget rows that Pi's drops; ports drop non-clear widget pushes in rpc mode | todo, powerline |
| G4 | Parallel tool end-event order differs; scenarios call one tool per turn | todo, permissions, subagents |
| G5 | U+2028/9 are escaped in request bodies | todo |
| G6 | `ctx.ToolCallID()` and the session-read round trip | todo |
| G7 | The template fake host has no shortcut driver and a fixed tool_call_id | todo, ask |
| G8 | Compaction is unreachable over RPC | todo, goal |
| G10 | No bell or terminal write from a subprocess extension | powerline |
| G11 | Parallel dialogs: ordering needs a dialog-sent hook | ask |
| G12 | No settings write API (nor for foreign settings files) | powerline, goal |
| G13 | `Theme.fg` token check | powerline |
| G14 | Stale-context error text | todo, ask |
| G15 | "No footer data provider" (powerline's own clash with the reviewer's G11, renumbered) | powerline |
| E2 | `Context.Custom` result handle (SetHidden/IsHidden/IsFocused) | ask (owner ruling: lead files it after 0.4.1) |
| G16 | No `registerMessageRenderer`; no active-tools prompt injection; no new-session pre-shutdown event | goal |
| G17 | No project-trust query: a project scope that Pi withholds until trust cannot be mirrored | permissions |
| G18 | A Binary's default active tools differ from Pi's | permissions, subagents |
| G19 | `ToolResult.IsError` is omitted when false (the original sends `isError: false`); a returned `IsError: true` differs from Pi's failed tool call (`sdk.NewToolError` matches) | subagents |
| G20 | No pi-package resource model for ports that ship skills and prompts under a non-`pigpen-` name | subagents |
| G21 | `--no-extensions` does not remove a Piglet Binary's fused members, so a child of a Binary has them again | subagents (review) |
| G22 | A Piglet Binary refuses `--system-prompt`: a `replace`-mode agent cannot run in a Binary child | subagents (review) |
| G23 | An extension process's environment carries `PIG_EXT_*` and `PIG_HARNESS_*`, which anything it starts inherits unless removed | subagents (review) |

## Where a generated skeleton (`pig-codegen extension`) would have saved hand work

Collected from the "where" sections of the PORT.md files:

- **Module and `Extension()` skeleton**, event and tool registration, command table, status and notify plumbing (every port).
- **TypeBox to JSON Schema** for a tool's parameters (todo, ask); a tool-definition importer that reads a recorded model request
  replaced hand work in subagents (`gen-tooldefs.py`) and would serve all.
- **The JS helper library** (`jsjson.go`, `jsutil.go`: about 375 lines copied into four ports).
- **A config and settings loader** with the original's search order (powerline, goal, permissions).
- **Generated tables** from the oracle (powerline's segments and icons; subagents' tool schemas).
- **Fakehost and twin scaffolding**, the **twin ledger and slice file first draft** from `twins list` (264 entries by name in
  subagents), the **mutation generator** (hand-written pairs: 74 to 210 per port), the **scenario generator**.
- **The layer-1 scripted-host driver and replay test** for "invisible over RPC" ports (goal): the single biggest saving.

## Publish readiness (keyword `pig-package`, pi-in-go.dev)

All six `package.json` files carry the keyword `pig-package` and a `pi.extensions` entry, a `LICENSE` naming the original and the Go
port, `CREDITS.md` with the upstream name, url and author, and `provenance.json` that passes the quality gate; `pig install
--validate-only --json` is valid for each, and a Piglet member exists for each (`piglets/pig-popular`).

| Port | Ready to publish as | Why not fully |
|---|---|---|
| todo | yes, after the owner's go-ahead | English only (E1, owner-approved) |
| ask-user-question | yes, labelled partial | tabbed questionnaire not ported; no overlay handle (E2) |
| powerline | yes, labelled partial | license is MIT as declared in package.json only (E3, owner-approved notice); no custom editor |
| goal | yes, labelled partial | no continuation, drafting or auditor: the default limit-unset path warns that automatic continuation is not ported |
| permissions | yes, labelled partial | an `ask` is refused (no dialog); path rules, full bash parsing, MCP and skill tools not ported (simple bash chains and the skill-read gate are), so it fails closed |
| subagents | yes, labelled partial, with the review's fixes | running is a Go addition that the original's trace cannot check (its own tests run a real child); in pig-popular, the permission member's default `*: ask` refuses `subagents_enable` until the config allows it |

Each says "partial" in its README and `PORT.md`. Nothing was pushed or published.

## The owner's subagent and pi-subagents

`components/subagents/port/PORT.md` compares the port with `kinsy-subagent` (read only, nothing copied). The owner's has what pi-subagents
lacks: **`dispatch_chain` over `chains.yaml`**, **`teams.yaml` with `dispatch_team`**, **piglet-first member resolution**
(`pig --piglet <name>`, else the agent file), durable chain runs with resume and human requests, interactive runs in a terminal
tab, grid and pane views. pi-subagents has what the owner's lacks: the richer frontmatter (aliases, packages, runners, context,
acceptance), the nearest-project-root rule and the builtin set, the `subagents_enable` activation, model-facing `list` and `get`.
Both have project and user agent directories, tool allowlists, model and thinking per agent. Those four owner features can be
added as a PiG layer over this port's definitions, discovery and activation, which is the suggested merge.
