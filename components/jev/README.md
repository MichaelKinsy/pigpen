# Jev for PiG

A second opinion on what your agent is about to do, and on what it just printed. Jev asks a judge
typed questions ("is this destructive?", "does this send local data off the machine?", "does this
output contain a secret?") and gets back numbers, not prose, so the answer is something PiG can branch on.

```text
 ⚠ Jev flagged bash (shadow mode: reported, not blocked)
   ⚠ destructive   ██████████  0.99  ≥ 0.90
   ⚠ exfiltration  ████████··  0.79  ≥ 0.70
   ⚠ beyond scope  ██████████  0.98  ≥ 0.85
   ⚠ impact        ██████████  3.00/3  ≥ 2.50  (confidence 0.91)
```

**It is off until you turn it on.** Nothing is judged, nothing is sent, and it prints nothing at start-up
until you opt in. Judging sends parts of your session to a judge, so `/jev on` shows exactly what and where
before it does anything.

Three surfaces, from y0usaf's [pi-jev](https://github.com/y0usaf/pi-jev) (this is its Go port; see
[CREDITS.md](CREDITS.md)):

| Surface | What it does |
|---|---|
| **Gate** | Before `bash`, `write` or `edit` runs, judges the call: destructive? sends data off the machine? beyond what you asked? how much damage? In **shadow** mode (default) it only reports. In **enforce** mode a flagged call asks you first. |
| **Output judge** | After `bash` finishes, reads what it printed: does it carry a secret? What kind of failure is it? It adds one line to the result the model reads ("do not repeat the value", "fix the environment before retrying"). It never blocks. |
| **`jev_ask`** | A tool the model can call to ask typed questions (yes/no probability, pick one, rubric score) about a piece of text. Exists only while Jev is on. |

## Turn it on

```sh
pig install ./components/jev            # or select the Package from a Piglet (pig-with-batteries does)
```

then, in a session:

```text
/jev on
```

```text
 Turn on Jev for this session?
 What leaves this machine for each judgment:
   • the working directory and the tool name
   • your last message (first 1200 characters)
   • the tool arguments (bash/write/edit; long fields are cut at 400 characters, and write/edit arguments are file content)
   • bash output (first 2000 characters)
 Also sent: text the model passes to jev_ask (up to 8000 characters) and text you pass to /jev check.
 Destination: the model acme/judge-1 (through PiG, the provider that already receives your conversation)
 If the judge is unavailable, tool calls run unjudged (it fails open). A judgment is probabilistic advice, not a sandbox.
 Turn Jev on?
```

To keep it on for every session, edit **your own** `<agent dir>/pi-jev.json` (`~/.pig/agent/pi-jev.json`;
`PIG_CODING_AGENT_DIR` moves it):

```json
{ "enabled": true }
```

Jev then shows the notice above at every start-up until you add `"acknowledged": true`.
`/jev off` turns it off again for the session. Nothing turns it on for you: not a key in the environment,
not a file in a repository you opened (see [What a project can change](#what-a-project-can-change)).

## Who judges

| Backend | `"backend"` | Where the content goes | Needs |
|---|---|---|---|
| **The model PiG is configured with** (default) | `"model"` | The provider of your current model, through PiG's own model access and credentials. When you switch models, the judge follows (Jev says so) and earlier verdicts are dropped. Set `"model": "provider/id"` to use another model of your registry. | nothing else |
| **The TypeSafe API** | `"typesafe"` | The endpoint you configure. There is no built-in default and no default model name. | `endpoint`, `model`, and a key (`TYPESAFE_API_KEY`, `apiKey` or `apiKeyFile`) |

Both come from the shared Go client in [`components/typesafe`](../typesafe/README.md): the model backend asks the
model for typed answers in prompted JSON mode and validates them strictly; the TypeSafe backend speaks TypeSafe's
API. **Calibration caveat:** the thresholds below were measured by y0usaf against TypeSafe's Jev on a handful of
states ("a smoke calibration, not a labelled evaluation set", in the original's words). Nobody has measured them
against an arbitrary chat model. Read them as starting points; that is one reason shadow mode is the default.

```json
{
  "enabled": true,
  "backend": "typesafe",
  "endpoint": "https://<your TypeSafe API root>",
  "model": "<a TypeSafe model name>",
  "apiKeyFile": "~/keys/typesafe.txt"
}
```

The key is sent only to that endpoint, which must be `https` (or `http` to `localhost`). `TYPESAFE_BASE_URL` and the
other `TYPESAFE_*` variables are ignored.

## The gate

| Question | Type | Reads | Threshold |
|---|---|---|---|
| Is this action destructive? | noul | `destructive` | 0.90 |
| Does this send local data or secrets off-machine? | noul | `exfiltration` | 0.70 |
| Does this affect anything beyond what the user asked for? | noul | `beyond_scope` | 0.85 |
| How much damage if the user did not want it? | score (4 levels) | `impact` | 2.50 |

All four go in one request. **Shadow mode is the default:** a flagged call produces a notification and a footer
status. In **enforce** mode (`/jev mode enforce`) a flagged call opens a dialog:

```text
 Jev flagged this tool call
 bash  rm -rf src && git push --force origin main

   ⚠ destructive   ██████████  0.99  ≥ 0.90
   ⚠ exfiltration  ████████··  0.79  ≥ 0.70
   ⚠ beyond scope  ██████████  0.98  ≥ 0.85
   ⚠ impact        ██████████  3.00/3  ≥ 2.50  (confidence 0.91)

 Judged by the model acme/judge-1 (through PiG, the provider that already receives your conversation)
 Run it anyway?
```

Print and JSON runs have no UI (RPC and the terminal do), so enforcement there degrades to the same warning unless you set
`gate.blockWithoutUI`. If the dialog cannot be shown (the UI call fails), the flagged call is blocked, as in the original:
fail-open covers an unavailable judge, not a missing approval.

## Failure policy: it fails open

**If the judge is unavailable, tool calls run unjudged.** A missing key, a timeout, a 429, a malformed or incomplete
answer produces no verdict and the tool call proceeds; a dead endpoint never stops your agent. The footer says so
(`⚖ jev ✗ unavailable (failing open)`) instead of keeping an old "clear", and errors are reported at most once a minute.
This is y0usaf's policy, kept on purpose, and stated at start-up and in `/jev`. Do not treat a clear verdict as
authorization: a model's judgment is probabilistic advice, not a sandbox, and the judge can be wrong or steered by the
very content it reads.

## The output judge

`tool_result` asks two questions in one request and appends one line to the result when either fires:

| Question | Type | Reads | Threshold |
|---|---|---|---|
| Does this output contain a secret or credential? | noul | `leaks_secret` | 0.90 |
| What kind of failure is this? | choice (6 options) | `failure_class` | confidence 0.60 |

A leak appends "Do not repeat the value in a reply, a file, or a command; refer to it by name instead" and raises a
notification. A failure class appends what to do about it (retry a `transient` failure, fix the environment for
`environment`, fix the code for `code_bug`, do not retry `permission`, fix the invocation for `user_error`). `no_failure`
says nothing. Judged tools default to `["bash"]`.

## `jev_ask`

```json
{ "state": "the tool output, diff, or message to judge",
  "questions": [
    { "id": "relevant", "type": "noul",   "instructions": "Is this relevant to the user's question?" },
    { "id": "label",    "type": "choice", "instructions": "Which bucket?",
      "options": [{ "name": "bug", "description": "Defect in existing behaviour" }, { "name": "feature" }] },
    { "id": "quality",  "type": "score",  "instructions": "How thorough is this?",
      "levels": ["Superficial", "Adequate", "Thorough"] } ] }
```

It is registered when Jev is on and refuses ("Jev is off") after `/jev off`. The text is cut to `maxStateChars`.

## Commands

- `/jev` shows the judge, what is sent, the failure policy and the last verdicts
- `/jev on` and `/jev off` toggle both judges for the session (`on` asks first, with the disclosure)
- `/jev mode shadow|enforce`
- `/jev last` prints the last gate verdict with all four answers
- `/jev output` prints the last judged output (also when it was clean)
- `/jev check <text>` runs the gate questions on text you supply (it sends that text, so it needs Jev on)

```text
 Jev is ON — shadow mode, output judge on
   Judge     the model acme/judge-1 (through PiG, the provider that already receives your conversation)
   Gate      bash, write, edit (on)
   Output    bash (on)
   Sends     working directory, tool name, your last message, tool arguments, tool output, jev_ask text — cut to your limits
   If down   tool calls run unjudged (fails open)
   Last      bash: destructive 0.99, exfiltration 0.79, beyond_scope 0.98, impact 3.00/3 at confidence 0.91
   Commands  /jev on|off · /jev mode shadow|enforce · /jev last · /jev output · /jev check <text>
```

## Configure

`<agent dir>/pi-jev.json` is yours. A project's `<cwd>/.pig/pi-jev.json` may tune the gate. Only keys you set override; the
rest are the defaults below.

```json
{
  "enabled": false, "acknowledged": false, "backend": "model", "display": "rich",
  "endpoint": "", "model": "", "apiKey": "", "apiKeyFile": "", "timeoutMs": 20000, "retries": 2,
  "maxStateChars": 8000,
  "gate": { "enabled": true, "mode": "shadow", "tools": ["bash", "write", "edit"], "argumentChars": 400,
            "cacheSeconds": 120, "minConfidence": 0.5, "blockWithoutUI": false,
            "blockOn": { "destructive": 0.9, "exfiltration": 0.7, "beyondScope": 0.85, "impact": 2.5 } },
  "output": { "enabled": true, "tools": ["bash"], "outputChars": 2000, "leakThreshold": 0.9, "minConfidence": 0.6 }
}
```

`"display": "plain"` uses the original's exact wording and no glyphs (the equivalence scenarios run in it). The API key
resolves in the order `TYPESAFE_API_KEY`, `apiKey`, `apiKeyFile` (`~/` expanded); `/jev` reports which one is in use.
`blockOn.impact` is on the 0 to 3 damage rubric, and `minConfidence` gates that dimension only (the three noul
questions return a probability and no confidence).

### What a project can change

A repository you open must not be able to send your data somewhere or switch judging on. So a project file **cannot** set
`enabled`, `acknowledged`, `backend`, `endpoint`, `model`, `apiKey`, `apiKeyFile`, `timeoutMs`, `retries` or `display`
(each is reported once as "ignored project setting"), and it can only **lower** what leaves the machine:
`maxStateChars`, `gate.argumentChars` and `output.outputChars` can shrink, and `gate.tools` / `output.tools` can pick among
the tools your own config already judges. Thresholds, mode and the rest may be tuned: they cannot leak anything. A project
that relaxes the protection your own config sets up (turns the gate or the output judge off, `enforce` into `shadow`,
`blockWithoutUI` off, raises a threshold, or judges fewer tools) is applied, since it only sends less, but never silently:
each start-up warns "`<project file>` relaxes the gate your own `pi-jev.json` sets: ..." with the settings it changed.

## What leaves the machine

Each judgment sends, to the judge named in `/jev`: the working directory, the tool name, your last message (first 1200
characters) and the tool's arguments. For `write` and `edit` those arguments contain file content. The output judge sends the
first `output.outputChars` characters of a `bash` result plus the same arguments. Any string longer than `gate.argumentChars`
(400) is cut at any nesting depth and replaced with `…[N chars elided]`; the omitted text never leaves. Set `gate.tools` to
`["bash"]` to keep file content out of the gate entirely, or lower a limit. The whole document is held to `maxStateChars`.
`jev_ask` sends the text the model passes to it (cut to `maxStateChars`), and `/jev check` sends the text you type after it.
The API key is sent only in the `Authorization` header to your endpoint, and notification text is scrubbed of it.

## Differences from pi-jev

This port follows y0usaf's behavior and differs only where the roadmap's code review found defects or the owner's rules
require it. Each is a test in [`port/PORT.md`](port/PORT.md) (C1 to C10): a verdict cache bound to the user's request and
the judge (C1), no project-chosen credential destination and no built-in endpoint (C2), `maxStateChars` applied (C3),
opt-in and disclosure (C4), strict typed-response validation (C5), a truthful "unavailable" status (C6), long strings
elided at every depth (C7), duplicate `jev_ask` ids refused (C8), `/jev` reporting the real key source and clean outputs
(C9), and `jev_ask` refusing while off (C10). The review of this port added R1 to R5 (same file): a failed enforce dialog
blocks, the default judge follows a model switch, the disclosure names `jev_ask` and `/jev check`, a project that relaxes
the gate is reported, and more trust-boundary tests.

## Proof

`port/` holds the evidence: the unmodified original (`port/oracle/`, MIT), the scenarios, the recorded Pi traces, the
mutation list and [`port/PORT.md`](port/PORT.md). Go tests drive the real SDK through a fake PiG host; the differential
scenarios run the original under Pi 0.87.1 and the port under PiG 0.3.0 against a fake judge endpoint and compare every
request and UI effect.

MIT. Original © y0usaf; port © Michael Kinsy. Depends on the shared client Package `components/typesafe`.
