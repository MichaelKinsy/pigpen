# pig-essentials

A fast, single-binary PiG with the essentials, and no Node: web search and fetch, sub-agents, task tracking,
structured questions, the ponytail mode and the Superpowers skills. It is for a user who runs PiG where Node plugins
start slowly (Termux, a small VPS, a laptop on battery) and wants the plugins they use every day in one Binary that
needs nothing else to start. **Everything stays quiet until used**: no network call and no process runs at start-up.

| Member | Original | What it adds |
|---|---|---|
| `rpiv-web-tools` | `@juicesharp/rpiv-web-tools` 2.12.0 by juicesharp, MIT | `web_search` over ten providers, `web_fetch`, `/web-tools` (no GitHub URL interceptor) |
| `tintinweb-subagents` | `@tintinweb/pi-subagents` 0.19.0 by tintinweb, MIT | `Agent`, `get_subagent_result`, `steer_subagent`, `/agents`; each agent is a child `pig` process (a tested slice: see its PORT.md) |
| `tintinweb-tasks` | `@tintinweb/pi-tasks` 0.9.0 by tintinweb, MIT | `TaskCreate`, `TaskList`, `TaskGet`, `TaskUpdate`, `TaskOutput`, `TaskStop`, `TaskExecute`, `/tasks`, a task widget |
| `ask-user-question` | `@juicesharp/rpiv-ask-user-question` 2.11.0 by juicesharp, MIT | the `ask_user_question` tool (partial: dialogs) |
| `ponytail` | `@dietrichgebert/ponytail` 4.10.0 by Dietrich Gebert, MIT | the "lazy senior dev" mode: `/ponytail`, five more commands and six skills |
| `superpowers` (skills) | `obra/superpowers` v6.4.2 by Jesse Vincent, MIT | fifteen skills: brainstorming, plans, TDD, debugging, review, verification |

## Install

On Linux, macOS and Windows, the signed Binary for the platform (once the release `pig-essentials/v0.1.0` is published):

```sh
pig piglet pull 'github:MichaelKinsy/pigpen/pig-essentials@0.1.0'
```

**Termux (Android).** There is no android Binary yet, so `pig piglet pull` has nothing to install there. Build it on
the device with Go (no Node), from the Piglet's npm source:

```sh
pkg install golang git
pig piglet add npm:@pi-in-go/pigpen-piglet-pig-essentials
pig piglet build pig-essentials --format binary --targets android/arm64 --out ~/pig-essentials
```

The first build compiles PiG and every member on the phone and takes several minutes. Two things are not done yet:
the npm source resolves only after the first npm publish (see the repository README), and this build has not been run
on an Android device (the four Go modules pass `go vet` for `android/arm64`). Until then, stage the Piglet on a machine
with Node (`npm ci --ignore-scripts && npm run stage`), copy `dist/staged/piglets/pig-essentials` to the phone and point
`pig piglet build` at its `piglet.yaml` with the same `--format`, `--targets` and `--out`.

## What it does not do

It does not replace what a Node plugin does when that needs Node: the visual companion of the `brainstorming` skill and
the flowchart renderer of `writing-skills` are Node scripts and need Node on the machine. The ports are partial where
their PORT.md says so (sub-agents: no scripted workflows, scheduling, worktrees or @mentions, and an agent costs a process start; web tools: no GitHub URL interceptor; ask-user-question: dialogs, not the tabbed questionnaire; ponytail: the Pi extension and Skills only).

## Start-up, measured

Time until PiG 0.4.1 answers its first `get_state` in `--mode rpc` (median of seven runs, wall clock, linux/amd64 on a
shared 196-thread Xeon, so read the ratio and not the milliseconds; a phone is slower):

| | to first answer | one print-mode turn against the faux model |
|---|---|---|
| `pig` alone | 41 ms | 50 ms |
| **`pig-essentials` Binary** (all members built in) | **196 ms** | **302 ms** |
| `pig` plus the five Node plugins (`npm:@juicesharp/rpiv-web-tools`, `@tintinweb/pi-subagents`, `@tintinweb/pi-tasks`, `@juicesharp/rpiv-ask-user-question`, `@dietrichgebert/ponytail`) | 2313 ms | 2470 ms |

A second measurement with the same script on the same host under heavier load (load average about 150) gave 48 ms,
243 ms and 2549 ms to the first answer, and 57, 335 and 2638 ms for the print turn: the same picture. The Binary's
start-up costs more CPU time than `pig` alone (about 0.75 s against 0.15 s summed over cores, median), which matters on a
phone more than the wall-clock figure suggests. `--version` takes about 30 ms for all three. At idle the Binary runs no child process and opens no network connection;
a child process starts only when the model calls `Agent` or runs `TaskExecute`. The Binary is 65 MB. The runs time `--version`, an rpc
`get_state` and `-p` with `PIG_TEST_FAUX=1`, in an empty `HOME`, after one warm-up run each.

## Build it yourself

`npm run stage`, then `PIG_BIN=<pig 0.4.1> npm run build:piglet -- pig-essentials` (see the repository README).
