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

The extension follows herdr's published guide, [Add Herdr support to your agent](https://herdr.dev/docs/add-herdr-support/) (and the [Socket API](https://herdr.dev/docs/socket-api/#agent-state-reporting) it builds on), with source `custom:pig` and agent `pig`. It runs `"$HERDR_BIN_PATH" pane report-agent "$HERDR_PANE_ID" ...` and activates only when `HERDR_ENV=1` and `HERDR_PANE_ID`, `HERDR_BIN_PATH` and `HERDR_SOCKET_PATH` are all set. Checked against herdr 0.9.3.

| PiG event | Report |
| --- | --- |
| `session_start` | the current state (`idle`, or `working` when a reload lands mid-turn), with the session |
| `agent_start` | `working` |
| `agent_settled` | `idle`, once no retry, compaction or queued continuation remains |
| `ui_prompt_start` | `blocked`, with the prompt title as the message: `select`, `confirm`, `input`, `editor` and `custom` dialogs from any extension |
| `ui_prompt_end` | back to `working` or `idle` |
| `herdr:blocked` on `pi.events` with `{ active: true, label }` | `blocked`, with the label as the message |
| `herdr:blocked` with `{ active: false }` | back to `working` or `idle` once no other block holds |
| `session_shutdown` with reason `quit` | `pane release-agent` |

- **Session.** Every report carries `--agent-session-path` (an absolute session file) and `--agent-session-id`. A new, resumed or forked session is reported as soon as it starts.
- **Message.** A `blocked` report's `--message` is the prompt title, sent as `--message <text>`, the only form herdr before 0.9.0 parses (every herdr takes the next argument as the value, even one that starts with `-`). Only a title that is exactly `--` is sent as `--message=--`, because herdr 0.9.2+ splits the resume command off at the first `--`.
- **Order.** Every report has a `--seq` seeded from the clock, so it increases across sessions and restarts. Calls never overlap; while one is in flight only the newest state is kept.
- **Release.** Only a real quit releases the pane. Reload and session replacement do not, so the successor's first report cannot race a release.
- **Best effort.** Each call has a three-second limit and a failure is ignored. It never slows the agent down.
- **Interactive only.** RPC, JSON and print runs report nothing, because herdr can only show a pane's terminal.
- **`herdr:blocked` bus event.** Another extension can mark a block that is not a UI prompt by emitting `herdr:blocked` with `{ active, label }` on `pi.events`, the convention herdr's own Pi integration uses. Blocks are counted per holder, so a bus release cannot end an open prompt's block, nor the other way round. The listener is ported from herdr-pig (see [Credits](#credits)).

## Resume after a herdr restart

herdr 0.9.2 and later restore a pane after a server restart by running the resume command an agent reported. Every state report carries it after `--`, together with `--agent-session-id`:

```text
"$HERDR_BIN_PATH" pane report-agent "$HERDR_PANE_ID" --source custom:pig --agent pig --state idle --seq N \
  --agent-session-path <session file> --agent-session-id <id> -- pig --session <session file>
```

herdr opens the pane in the same directory and runs that command, so the pig session comes back in the same pane. This mirrors herdr's own Pi integration, which restores `pi --session <session file>`; like it, the command has no model flag (a resumed session restores the model it recorded). `pig --session <file>` opens that exact file from any directory and session dir on PiG 0.3.x and 0.4.0. `--session <id>` would ask to fork the session when the directory differs, so the extension never reports it.

- **Rules.** The first word is `pig`, a plain command on `PATH`; no argument has an apostrophe or control character; at most 64 arguments and 8 KiB. When the session file breaks a rule (for example an apostrophe in a directory name), the command is `pig --session-id <id>`, which resumes the id in the pane's project session directory and never prompts. With neither usable, only the state is reported. An in-memory session (`pig --no-session`) has no file and reports no command: `--session-id` would create a new, saved session instead of bringing it back.
- **A new session.** `/new`, `/resume` and `/fork` report the new session, not a release.
- **Older herdr.** herdr before 0.9.2 cannot parse the `--` separator (`unknown option: --`) and 0.9.2+ answers `invalid_resume_argv` for a command it refuses. Either way the extension sends the same report once more without the command, silently, and stops attaching one. State reports and the release keep working.
- **Opt out.** `[session] resume_agents_on_restore = false` in herdr's config turns restore off.
- **You need `pig` on `PATH`** in the restored pane, with the same agent directory (`PIG_CODING_AGENT_DIR`) and model settings the session used; herdr starts the command in the pane's shell.
- **The restore runs plain `pig`.** An extension cannot see how PiG was started, so the command carries no `--piglet`, `-e` or other launch flag. A session started with `pig --piglet .../herdr/piglet.yaml`, or with a Piglet Binary under another name, comes back in plain `pig`: the conversation returns, the Piglet's composition does not, and herdr sees the agent again only if that `pig` loads this extension (install the Package with `pig install ./components/herdr`, or put the Binary on `PATH` as `pig`).

## When herdr does not take a report

The reporter never waits on herdr and never fails the agent. When a call fails (herdr not running, a pane herdr no longer
knows, a `herdr` binary that is gone) it writes one line to stderr, which PiG collects as the extension's output, naming the
cause: `herdr: pane report-agent failed: exit status 1: error: pane p_9 not found`. The same failure is written once until a
call succeeds again. A report that only an old herdr refuses because of the resume command is retried without it and stays
silent. This diagnostic came from the `herdr-agent-state` extension of pigpen pull request 2 (Yu Li, @liyu1981).

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

Pigpen's herdr support merges two implementations. The reporter and this Piglet are Pigpen's own (MIT). The herdr plugin
in [`herdr-plugin/`](herdr-plugin/) and the `herdr:blocked` listener come from
[herdr-pig](https://github.com/Hai-Fai-Solutions/herdr-pig) by Christian Polzer ([@cpolzer](https://github.com/cpolzer))
and Hai-Fai Solutions, ported with their permission
([PiG#204](https://github.com/MichaelKinsy/PiG/issues/204)) at v0.2.1. That code stays under the Apache License 2.0: see
[`herdr-plugin/LICENSE`](herdr-plugin/LICENSE) and [`herdr-plugin/NOTICE`](herdr-plugin/NOTICE), and for the listener
[`components/herdr/NOTICE`](../../components/herdr/NOTICE). herdr-pig is itself based on herdr's Pi integration
(Apache-2.0).

Where the two differed, Pigpen kept the better behaviour per feature:

| Feature | Kept | Why |
| --- | --- | --- |
| Extension language | Pigpen's Go reporter | No Node.js needed; it fuses into a Piglet Binary |
| Transport | Pigpen's `herdr pane report-agent` CLI calls | herdr's documented contract; the CLI finds the socket or pipe itself on every platform |
| `blocked` from UI prompts | Pigpen's | herdr-pig could not detect PiG's own prompts |
| `blocked` from `herdr:blocked` | herdr-pig's | Pigpen's Go reporter had no listener; now ported, counted per holder |
| Resume fallback | Pigpen's `pig --session-id <id>` | herdr-pig's `pig --session <id>` asks to fork from another directory |
| In-memory sessions | Pigpen's (no resume command) | resuming the id would create a new session |
| Old herdr | Pigpen's (retry without the resume command) | state keeps working on herdr before 0.9.2 |
| Failure reporting | Pigpen's one-line stderr diagnostic | herdr-pig failed silently |
| Retry | Pigpen's single call with a 3 s limit | herdr-pig retried once (0.5 s, then 1.5 s); either keeps PiG unblocked |
| Install from herdr | herdr-pig's plugin, adapted | Pigpen had none; it now installs the Go Package with `pig install` |
| Plugin startup refresh | dropped | `pig update` updates the Package; a refresh would hit npm on every herdr start |
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
