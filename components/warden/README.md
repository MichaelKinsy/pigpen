# warden (Go port of pi-warden)

Guardrails that **steer instead of interrupt**. Warden watches what the agent is about to do and, when it
matters, tells the agent why and lets it re-plan, instead of stopping you with a dialog:

| Guard | What it catches | What happens |
|---|---|---|
| **Irreversible** | offline: force-pushes, `rm -rf` of absolute, home or parent paths, `DROP`/`TRUNCATE`, `git reset --hard` (touching a secrets file is flagged with a warning). With a judge, those patterns are evidence and the judge's irreversible score decides (0.9 holds, 0.5 warns), as in the original's default `action.floor: "evidence"`; set `"floor": "level"` under `action` in the config file to hold every pattern hit regardless of the judge | the call is **held before it runs**; the agent is told why and what to do instead (a recoverable alternative, or ask you) |
| **Off-task / off-plan** | a call unrelated to your request, or at odds with what the agent just said it would do | the call runs; the agent is told in one line to justify it or return to the task |
| **Stuck** | the same call repeated, failing over and over, churning the same file. With a judge, the request carries the parsed failure of each run (test names, error lines, location), what each edit changed and a digest (`stuck.evidence`, on by default; `"stuck": {"evidence": false}` sends the output tails only) | the agent is nudged to change approach; a failed result that repeats an earlier failure is replaced by a short diff against it |
| **Call waste** | sleep polling, paging one file in small reads, searching one file again and again, re-running a check through a different output filter | one advisory sentence on the tool result that triggers it (no extra call, no steer, no request); `waste.tip` adds a one-time system prompt tip |
| **Subagent reports** | a report of an async subagent that names a failure, a blocker or a question | read in code first; only a report that names trouble reaches the judge; a high answer wakes the agent once, with a pointer to the reports (`subagent.*`) |
| **Done claims** | "done, tests pass" when nothing was verified | a follow-up asks for the evidence |

It is a port of [pi-warden](https://github.com/DevMortimer/pi-warden) by Ryan Gapac
([CREDITS.md](CREDITS.md)), pinned at `a12b2703`. **It is off until you turn it on**, and it tells you exactly what
leaves your machine before anything does.

## Try it (60 seconds)

```text
/warden enable
```

A dialog asks which judge to use. Each choice names its destination and lists what is sent and what never is:

```text
Warden makes two kinds of checks.

Offline checks run on this machine and send nothing: …

Judged checks (irreversible, off-task, stuck, done-claims) ask a model. Each request sends:
  • the tool name and the command or path of the call (credentials redacted, at most 2,000 characters)
  • …
Never sent: files the call does not name, environment variables, your PiG auth and settings files, and API keys …

Destination: your session model (anthropic/claude-…), through PiG's model registry: the same provider, account
and terms as this chat. Nothing goes to TypeSafe.
```

| Choice | Judge | Sends to |
|---|---|---|
| TypeSafe Jev | typed questions answered by TypeSafe's Jev model (TypeSafe states about a quarter of a second a request; not measured here, no live check was run) | `https://api.typesafe.ai` (or `TYPESAFE_BASE_URL`), key from `TYPESAFE_API_KEY` |
| Your session model | the model you are chatting with | the provider you already use, via PiG's model registry |
| Offline only | fixed patterns, no model | nowhere |

Then see it work, without running anything:

```text
/warden test
warden test — synthetic calls, nothing was run.
Judge: none (offline patterns only). /warden enable adds a judge for off-task, off-plan and irreversible-by-context calls.

  a force push
    $ git push --force origin main
    HELD (0 ms): destructive: git force push
    the agent would read: pi-warden held this bash call before it ran: destructive: git force push. Do not retry it unchanged. Either (1) …
  a recursive delete of the home directory
    $ rm -rf ~/projects
    HELD (0 ms): destructive: recursive rm on an absolute, home, variable, or parent path
  a secrets file
    $ cat .env
    would warn (0 ms): sensitive: touches a secrets or credentials file
  an ordinary test run
    $ npm test
    allowed (0 ms)
```

(With a judge selected, the first line names it, for example `Judge: TypeSafe (Jev) at https://api.typesafe.ai.`)

The status line always shows the state: `warden: typesafe · steer · 12 checked, 1 held`, `warden: off · /warden
enable`, or, when the chosen judge is not in use, `warden: offline (typesafe: no TYPESAFE_API_KEY) · …` or
`warden: offline (typesafe not agreed to) · …`.

## Commands

| Command | Does |
|---|---|
| `/warden` or `/warden status` | state, backend and its destination, counts, what is sent |
| `/warden enable [typesafe\|own-model\|offline]` | opt in (asks for consent, shows the data flow) |
| `/warden disable` | off, immediately; nothing is sent afterwards |
| `/warden mode steer\|confirm\|advise` | `steer`: hold and tell the agent; `confirm`: ask **you** in a dialog; `advise`: never hold, only warn |
| `/warden backend typesafe\|own-model\|offline` | change the judge (asks for consent again if it sends somewhere new) |
| `/warden test` | run synthetic dangerous calls through the guard; executes nothing |
| `/warden trace` | the recent decisions and why |
| `/warden recommend` | what the hold history says about the thresholds, the patterns that are mostly false alarms and how well each kind of steer works |
| `/warden unmute <kind> [model]` | send a steer kind again that a model's history had made trace-only |
| `/warden prefs [forget <n>]` | the standing preferences read from earlier sessions of the project (and the agent's own lessons), each with its status and why it is or is not injected; `forget <n>` drops one for good |
| `/warden loops` | the open and closed loops of this session |
| `/warden index` | build the local capability index: the session model writes one entry for each skill and tool, warden validates and sanitizes them (interactive sessions; asks first, uses real tokens) |

Headless runs (no UI) cannot answer the consent dialog, so they use environment variables. Setting
`PIGPEN_WARDEN_BACKEND` (with `PIGPEN_WARDEN_ENABLED=1`) **is** the consent to that backend, for runs with that
environment only: the variables are never written to the config file.

| Variable | Meaning |
|---|---|
| `PIGPEN_WARDEN_ENABLED=1` | switch on (offline patterns unless a backend is set) |
| `PIGPEN_WARDEN_BACKEND=typesafe\|own-model\|offline` | the judge, and consent to its data flow |
| `PIGPEN_WARDEN_MODE=steer\|confirm\|advise` | how a held call is handled |
| `PIGPEN_WARDEN_STEER_VISIBLE=0\|1` | hide or show the steer messages in the transcript (default shown) |
| `PIGPEN_WARDEN_DB`, `PIGPEN_WARDEN_STEER_STATS` | where the learning database (default `<PIG_HOME>/pigpen-warden/holds.db`, the same file the original writes) and the per-model steer counts (`steer-stats.json`) live |
| `TYPESAFE_API_KEY`, `TYPESAFE_BASE_URL` | the TypeSafe key and endpoint (the key is never written to disk) |

Settings live in `<PIG_HOME>/pigpen-warden/config.json` (mode 0600, contains no key).

## What happens when a call is held

The agent reads a message like this and re-plans:

> pi-warden held this bash call before it ran: … Do not retry it unchanged. Either (1) reach the goal with a
> recoverable alternative that stays inside the project (a targeted path, a dry run, a move instead of a delete, a
> normal push), or (2) if this exact action is genuinely required, stop and tell the user … If the user's reply
> approves it, retry the same call and pi-warden will let it through.

Your reply approves it; nothing else does. A judge that fails or times out never blocks a call: the offline patterns
decide.

## Install

```sh
pig install ./components/warden
```

It is a Package: `pig package validate ./components/warden`. It builds from source on first use (Go toolchain
required) or fuses into a Piglet Binary (see `piglets/pig-warden`). It shares the TypeSafe client in
[`components/typesafe`](../typesafe/README.md).

## What is and is not ported

Ported: the four guards above, call waste and subagent triage, the hold feedback loop (what you do next labels each hold and each allowed call, a per-session log, a learning database that carries over between sessions), per-model steer adaptation, the judge cooldown, the project rules guard (`pi-warden.md`, `/warden rules`, `/warden report`, `/warden init`, end-of-run turn rules), the offline pattern floor, sibling prejudging, user `commandRules` / `denyRules` /
`exemptRules`, user path rules and arming rules, session scratch, host paths, SQL target classes, violation escalation, the large-output steer, the steer budget, both judge backends, the runaway guard (a reply that repeats its block is stopped, with one recovery turn per prompt), the prose check and the restatement record at the end of a run, the security-write and slop steers, desktop notifications (`notify`, off by default), and secret detection with its masking helpers. Not ported (the config loader and panel, the audit and bench commands, …) and every deliberate deviation are listed in
[`port/PORT.md`](port/PORT.md); every upstream test without a Go twin is a named skipped test. The upstream `/warden` actions that are not ported are refused with an error that names the original (`npm:pi-warden@0.80.0`).

Also ported: the **conscience** (off unless `conscience.enabled`): before the agent acts on a prompt it asks the judge whether one skill or tool of the session would serve the request, and recommends it in one message (`recommend` mode), or supplies the skill's text after seven safety checks (`load` mode). A recommendation goes out only when the judge that answered is the one the measured policy names (`jev-1.13.0`), so the own-model backend traces the assessment and delivers nothing; the first prompt after a stale or missing index is told once to run `/warden index`. Candidates, thresholds, the reminder at the end of an unused run and the trace lines are the original's; see PORT.md for what differs (no saved key-rejection state, a YAML subset for skill frontmatter).

Also ported, and registered as agent tools: **`warden_remember`** (a standing lesson for the project, recorded only in a standing form, confirmed in a later session before it is injected), **`warden_loops`** (open loops: promises the agent makes to do something later, kept per session and project, shown again after compaction, at the end of a run and on resume) and **`warden_recall`** (what was already tried in this session: failed attempts with their error lines, the last passing check, saved outputs). With them: standing preferences (the corrections you repeated in earlier sessions of the project, injected as one quoted message only when they pass seven rules), the compaction evidence appendix, `/warden prefs [forget <n>]` and `/warden loops`. They send nothing to any judge and make no model call.

Also ported: the **context saver**. A large tool output is judged (with a judge selected) for how much of it the agent needs: the rest is replaced by an exact excerpt (a parser's for test runs, compiler and linter output, diffs, logs and installs; otherwise head, diagnostics and tail) and the full text is stored in an owner-only file the footer points to. An output identical to an earlier one becomes a one-line duplicate note, and a block of lines the agent already has in context is cut to one pointer line (opt-in for messages: `context.dedupeMessages`). Credentials in tool output are masked before the model reads them, with a banner that says so (offline, no judge needed); injection and exfiltration are judged and flagged in the banner, never steered. The opt-in context filter (`context.filter.enabled`) keeps the passages that matter to the current task instead of the blind excerpt, and the opt-in relevance compaction (`compaction.enabled`) replaces the host's own summary with the exact parts of the discarded span the task still needs. `/warden status` adds the saver's ledger line. Every excerpt and summary is exact text from the output; nothing is paraphrased.

## Proof

| Path | What |
|---|---|
| `port/oracle/` | the unmodified original at the pinned commit, and its license |
| `extensions/warden/testdata/diff_corpus.json` | 2,552 commands; the original's and the port's offline decisions are compared on every one |
| `extensions/warden/testdata/prefs_corpus.json` | 5,594 user messages and 2,500 raw clauses; the original's and the port's standing-preference detectors are compared on every one |
| `extensions/warden/*_test.go` | 577 twins of the original's own test cases, the fake TypeSafe server, the fake model registry and fake-host tests against PiG's event protocol |
| `port/scenarios/`, `port/golden/` | 62 live scenarios, traces recorded from the original under Pi; the port produces the same trace, event for event (the 19 guard scenarios also as the built Binary) |
| `port/scenarios-b4/`, `port/golden-b4/` | one more scenario (`warden_remember` across three sessions) that fails on a known PiG host bug (B4: response order of a slash command), kept as the repro |
| `port/mutations.json`, `port/mutate.py` | deliberate defects that the scenarios or the layer-1 tests must catch |
| `port/PORT.md` | file map, scope, deviations, equivalence results, **named gaps** |

```sh
cd extensions/warden && go test -race -count=1 .
```

No test reaches the real TypeSafe service or reads a credential.

MIT. The port: [LICENSE](LICENSE). The original: [port/oracle/LICENSE](port/oracle/LICENSE), © 2026 Ryan Gapac.
Credits: [CREDITS.md](CREDITS.md).
