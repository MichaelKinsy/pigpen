# herdr

PiG that reports its state to [herdr](https://github.com/herdrdev/herdr), the terminal workspace manager. Run inside a herdr pane, it shows `pig` with its `idle`, `working` and `blocked` state in herdr's sidebar and `herdr agent list`, notifies you when it finishes or needs a decision, and lets `herdr agent wait` and other automation follow it. Outside herdr it does nothing.

**This Piglet is optional.** The reporter is the standalone extension Package
[`components/herdr`](../../components/herdr/README.md). It works on its own in a
plain `pig` session, without this Piglet: from the checkout root,
`pig install ./components/herdr`. This Piglet only packages the same extension as
a named agent (`pig --piglet ...`).

**Source only, not a published release.** No release Binary is published and no remote install command (release or source add) is offered yet; use the local checkout commands on this page. The extension is Go, so `pig piglet build dist/staged/piglets/herdr/piglet.yaml --format binary --out <path>` builds a Binary with the reviewed PiG (set `PIG_SOURCE_ROOT` to a git checkout of it). See the root [release blockers](../../RELEASE-BLOCKERS.md).

Inspect and run it from source with a compatible PiG (a Go toolchain is required to build the extension; no Node.js is):

```sh
npm run stage
pig piglet validate dist/staged/piglets/herdr/piglet.yaml
pig --piglet dist/staged/piglets/herdr/piglet.yaml
```

## What it reports

The extension does what [herdr-pig](https://github.com/Hai-Fai-Solutions/herdr-pig) does (see [Credits](#credits)): it talks to herdr's socket, as herdr's own Pi integration does, with source `pig` and agent `pig`. It activates only when `HERDR_ENV=1` and `HERDR_SOCKET_PATH` and `HERDR_PANE_ID` are set; the `herdr` binary need not be on `PATH`. Each report is one JSON line, `{"id", "method": "pane.report_agent", "params"}`, on a new connection to the socket (a named pipe on Windows). Checked against herdr 0.9.3.

| PiG event | Report |
| --- | --- |
| `session_start` (TUI only) | the current state (`idle`, or `working` when a reload lands mid-turn), with the resume command |
| `agent_start` | `working` |
| `agent_settled` | `idle`, once no retry, compaction or queued continuation remains and `isIdle()` is true |
| `herdr:blocked` on `pi.events` with `{ active: true, label }` | `blocked`, with the label as the message (as given, whatever its type) |
| `herdr:blocked` with a falsy `active` | back to `working` or `idle` once no other block holds |
| `session_shutdown` with reason `quit` | `pane.release_agent` |
| `ui_prompt_start` / `ui_prompt_end`, **only with `PIGPEN_HERDR_UI_PROMPTS=1`** | `blocked` with the prompt title (or its kind) as the message, then back |

- **Prompts are opt-in.** PiG tells every extension when a `select`, `confirm`, `input`, `editor` or `custom` dialog opens. Pi has no such event and herdr-pig never reports a prompt, so by default the extension reports exactly what herdr-pig does. `PIGPEN_HERDR_UI_PROMPTS=1` in the pane's environment adds the `blocked` state for dialogs; blocks are counted per holder, so a `herdr:blocked` release cannot end a dialog's block, nor the other way round.
- **Order.** Every report has a `seq` seeded from the clock (microseconds), so it increases across sessions and restarts. Requests never overlap; while one is in flight only the newest state is kept. An unchanged state, message and resume command is not sent again.
- **Release.** Only a real quit releases the pane. Reload and session replacement do not, and the next session's first report replaces the state. The release does not wait for a report in flight; herdr drops the older `seq`.
- **Retry.** A request that gets no answer is sent again once with the same content: 0.5 s for the first attempt, 1.5 s for the second. Nothing waits on herdr longer than that, and a failure never reaches the agent.
- **Interactive only.** RPC, JSON and print runs report nothing, because herdr can only show a pane's terminal.

## Resume after a herdr restart

herdr 0.9.2 and later restore a pane after a server restart by running the resume command an agent reported. Every state report carries it as `resume_argv`:

```json
{"id":"pig:state:1792509903106:2cvg0fgoom3","method":"pane.report_agent","params":{"pane_id":"w1:p1","source":"pig","agent":"pig","state":"idle","seq":1792509903106737,"resume_argv":["pig","--session","/home/user/.pig/agent/sessions/one.jsonl"]}}
```

The command is `pig --session <session file>` when the session file is an absolute path without an apostrophe or control character, else `pig --session <session id>` when the id is safe, else no command is sent (herdr rejects a resume command with an apostrophe or control character and drops the whole report). An in-memory session (`pig --no-session`) has an id and no file, so it reports `pig --session <id>`. A `pig --session <id>` asks to fork the session when it is restored from another directory than the session's own; that is herdr-pig's behavior and the port keeps it.

- **A new session.** `/new`, `/resume` and `/fork` report the new session, not a release.
- **Opt out.** `[session] resume_agents_on_restore = false` in herdr's config turns restore off.
- **You need `pig` on `PATH`** in the restored pane, with the same agent directory (`PIG_CODING_AGENT_DIR`) and model settings the session used; herdr starts the command in the pane's shell.
- **The restore runs plain `pig`.** An extension cannot see how PiG was started, so the command carries no `--piglet`, `-e` or other launch flag. A session started with `pig --piglet .../herdr/piglet.yaml`, or with a Piglet Binary under another name, comes back in plain `pig`: the conversation returns, the Piglet's composition does not, and herdr sees the agent again only if that `pig` loads this extension (install the Package with `pig install ./components/herdr`, or put the Binary on `PATH` as `pig`).

## When herdr does not take a report

The reporter never fails the agent. When a request gets no answer after both attempts (herdr not running, a socket that is gone) it writes one line to stderr, which PiG collects as the extension's output, naming the cause: `herdr: pane.report_agent failed: dial unix /run/herdr.sock: connect: no such file or directory`. The same failure is written once until a request is delivered again. herdr-pig fails silently here; the line is an addition that herdr cannot see. It came from the `herdr-agent-state` extension of pigpen pull request 2 (Yu Li, @liyu1981).

## Install from herdr

The [`herdr-plugin/`](herdr-plugin/) directory is a herdr plugin, ported from herdr-pig, that installs the reporter Package
from herdr's side. It needs `pig` on `PATH` and herdr 0.9.2 or later:

```sh
herdr plugin install MichaelKinsy/pigpen/piglets/herdr/herdr-plugin
```

Installing it runs `pig install npm:@pi-in-go/pigpen-herdr`; restart `pig`, or run `/reload`. It adds the herdr actions
`pigpen-herdr.install`, `pigpen-herdr.uninstall` and `pigpen-herdr.status` (with `-windows` variants that run the
PowerShell script), each of which shows its result as a herdr notification. Run the uninstall action before
`herdr plugin uninstall pigpen-herdr`, because herdr has no uninstall hook. The install also removes the TypeScript
extension an earlier herdr-pig plugin copied to `~/.pig/agent/extensions/herdr-pig-agent-state.ts` (only when it carries
herdr-pig's ownership marker), so one pane never has two reporters. Without herdr, `sh herdr-plugin/scripts/pig-support.sh
install` (or `pig-support.ps1` on Windows) does the same. Use the plugin or this Piglet, not both in one pane.
`PIGPEN_HERDR_PACKAGE` in herdr's environment installs another source instead of npm, for example a checkout's
`components/herdr` or an unpacked `npm pack` of it (`pig install` takes a directory, not a `.tgz`).

## Credits

Pigpen's herdr support is a port of herdr-pig. The herdr plugin
in [`herdr-plugin/`](herdr-plugin/) and the reporter's behavior (including the `herdr:blocked` listener) come from
[herdr-pig](https://github.com/Hai-Fai-Solutions/herdr-pig) by Christian Polzer ([@cpolzer](https://github.com/cpolzer))
and Hai-Fai Solutions, ported with their permission
([PiG#204](https://github.com/MichaelKinsy/PiG/issues/204)) at v0.2.1. That code stays under the Apache License 2.0: see
[`herdr-plugin/LICENSE`](herdr-plugin/LICENSE) and [`herdr-plugin/NOTICE`](herdr-plugin/NOTICE), and for the listener
[`components/herdr/NOTICE`](../../components/herdr/NOTICE). herdr-pig is itself based on herdr's Pi integration
(Apache-2.0).

The Go reporter is Pigpen's own (MIT) and a faithful port of herdr-pig's extension: the same socket transport, source, retry, resume command and `herdr:blocked` handling, proven against the unmodified original (see [`components/herdr/port/PORT.md`](../../components/herdr/port/PORT.md)). What it adds, and why:

| Feature | Behavior | Why |
| --- | --- | --- |
| Extension language | Go reporter | No Node.js needed; it fuses into a Piglet Binary |
| `blocked` from UI prompts | Opt-in, `PIGPEN_HERDR_UI_PROMPTS=1` | herdr-pig could not detect PiG's own prompts; by default the port reports what the original does |
| Failure reporting | One stderr line | herdr-pig failed silently |
| Install from herdr | herdr-pig's plugin, adapted | Pigpen had none; it now installs the Go Package with `pig install` |
| Plugin startup refresh | herdr-pig's, adapted: reinstalls the pinned Package version when another one is installed | The plugin pins the Package version it was released with |
| Plugin ownership check | herdr-pig's marker, used to replace its old copy | `pig install` and `pig remove` own the Package itself |
| `PIG_USE_PI_DIRS=1` refusal | herdr-pig's | shared settings would hand the Package to Pi |

## Layout

```text
piglet.yaml                     authored composition, input to staging
catalog.json                    presentation metadata
tests/pig.integration.test.ts   real pig in tmux against a fake herdr (npm run test:pig)
tests/fixtures/ask/             Go fixture: /ask opens a confirmation for the blocked step
../../components/herdr/         the reporter Package: extensions/herdr/ (Go, with its tests)
herdr-plugin/                   herdr plugin ported from herdr-pig (Apache-2.0, LICENSE and NOTICE inside)
```

Run the unit tests with `PIG_BIN=pig npm run test:go`. `pig-with-batteries` selects the same reporter Package alongside its own resources. Both Piglets build a Binary; `npm run test:pig` runs the real-pig scenario from source and, with `PIG_SOURCE_ROOT`, as a built Binary.

`discovery` keeps the user's and the workspace's extensions and skills, because the reporter adds no behavior that would conflict with them.
