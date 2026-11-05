# Pigtorio build plan: summary and platform facts (2026-11-04)

Copied from the maintainer's message of 2026-11-04. The full plan lives in a Claude artifact (https://claude.ai/artifact/JeXrvGnsRY9GQ1Qi9ZJrmv) that is not readable from this machine; this summary is the working copy.

Design: proposal doc https://claude.ai/artifact/TF1uG9Xu7yw99LwV8dKwwg and storyboard canvas https://claude.ai/artifact/FS9r1UkVRzYYPMZwvp9RXs (exported copy: `~/Downloads/Storyboard (scroll)-html.zip`).

## Platform facts (verified from source, 4 Nov 2026: herdr-app 0.1.13, herdr-core 0.1.18, PiG 0.5.3)

- Herdr app mods are folders under `$HERDR_APP_HOME/mods/` with `main.js`; names are global. Saving a mod hot-reloads every copy of that mod; broken saves keep the last good version.
- No dev-vs-installed mode and no loading mods from another path. Symlinked mods are found; hot reload through symlinks on macOS is unverified.
- A different `HERDR_APP_HOME` is not isolation; spaces/tabs live in the core document. Dev and test windows need a private core session too.
- `exec`/`hc()` from mods: 30 s and 8 MiB caps, no streaming. Long-running work must be a separate helper (`hc run --bg --keep --name`).
- `shared()` changes re-render every mod and rewrite the whole file; outside writes are not watched.
- `herdr-app do <mod>.<command>` routes to the mod's `nextCommand()`; `shot` saves the last frame and works headless; `check` needs a display and runs `init()` for real.
- herdr-core: `hc events` JSON stream; OSC 7501 fields state, kind (permission/question/auth), progress, id, app, title, msg.
- In the Herdr app, PiG writes OSC 7501 itself. The pigpen `herdr` Piglet reports only to the Herdr TUI socket.
- PiG removed `/cost`; use `/session` or RPC `get_session_stats`.
- PiG supports `--session` and `--fork`; sessions are JSONL under `~/.pig/agent/sessions/`. Go extensions get `agent_settled` (the real done signal) and can read the branch.
- pigpen sub-agents already run child pigs in herdr-core panes and read the last assistant message from the session file.

## Key plan decisions (proposals)

- Architecture: plain-JS mods only draw and ask; a Go `pigtorio-engine` helper owns belts, rules, crate log; mods→engine by localhost fetch, engine→mods by a watched state folder (M0 spike decides).
- Three homes: play (installed, changed only by `pigtorio promote`), dev (`pigtorio dev`, stage-and-swap hot reload, private core), CI sandbox (headless, golden shots).
- Self-hosting stages: S0 at G0 (built from source), S1 at G1 (team's agents watched in Pigtorio), S2 after M3-01..05 (all plan tasks run as crates through Pigtorio), S3 in M4 (Runt shapes Pigtorio's own factory).

## Checked against Warp's software-factory model

- Matches: closed loop triage→spec→implement→review→verify→ship→monitor; human pauses at spec approval, unclear questions, merge; review agent advisory; factory defined as code (`factory.yaml` + agents, automations, routers, scorers, benchmarks, skills) reviewed by PR; foreman agent ≈ the Runt/Drover; measure output vs human time and tokens; "two jobs" (product engineering + factory engineering).
- Changes added to the plan: (1) replayable crate records from M3 (base commit, Mandate, Piglet/model, environment); (2) County Fair benchmarks replaying own crates across breeds/Piglets/harnesses with scorers → routing rules; (3) scorers + observer drafting PRs for recurring failures, never auto-applied; (4) Sorter triage station and evidence (screenshots/video) on UI changes; work may enter at issue, spec or draft PR; (5) intake via `pigtorio crate add` CLI and an MCP server, not only the Pigtorio UI; (6) security boundaries outside the agent: pigs hold no push credentials, human-only merge, worktree per pig, container runner + egress allowlist as a spike.
- New metrics: human touches per PR, factory share of merged PRs, time to first human review, revert rate. Inspector defaults to a different model than Builder.
- Deliberate differences: Pigtorio is local/multi-machine first (not cloud-first); no individual scoring by default.

## Open decisions for the maintainer

Repo home (new repo recommended), licence (MIT vs Apache-2.0), name, character sprites in public material, first-class agents at launch, minimum versions, who may run `promote`.

PiG's own AGENTS.md says not to claim HPE sponsorship or community governance until recorded publicly; the project description "HPE sponsored but community owned" needs reconciling before anything is public.
