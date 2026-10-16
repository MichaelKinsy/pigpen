# Plan: a `herdr-pig` Piglet based on Hai-Fai-Solutions/herdr-pig

Status: plan only (owner decision on issue #204). No code, test, NOTICE or script of herdr-pig is imported until the owner's reply to #204 carries Chris Polzer's answer; he may open the PR himself, and then this plan is the review checklist for it.

## What herdr-pig is (read at 3ec68047, Apache-2.0, version 0.2.1)

- `extensions/herdr-agent-state.ts` (276 lines, TypeScript): reports PiG's `idle`, `working` and `blocked` state to Herdr over the socket Herdr exposes to every pane (`HERDR_SOCKET_PATH`, `HERDR_PANE_ID`, `HERDR_ENV=1`): `pane.report_agent` with a `seq`, a `resume_argv` of `["pig", "--session", <file|id>]` (Herdr rejects an argument with an apostrophe), and `pane.release_agent` when PiG quits. Events: `session_start`, `agent_start`, `agent_settled`, `session_shutdown`. It stays silent outside Herdr and in print, JSON and RPC mode.
- `herdr-plugin.toml` + `scripts/pig-support.sh|.ps1`: a Herdr plugin whose `install`, `uninstall`, `refresh` actions copy the extension into PiG's agent directory (refusing a directory shared with Pi or a PiG never set up).
- `test/` (Bun): 7 extension tests (silent outside Herdr, idle with resume at session start, working-then-idle with increasing seq, silent in non-TUI modes, `herdr:blocked`, release only when PiG quits, resume command Herdr accepts) and 9 plugin tests.
- `NOTICE` says: "herdr-pig, Copyright 2026 Hai-Fai Solutions; includes software developed by the Herdr project (Apache-2.0); extensions/herdr-agent-state.ts is based on Herdr's Pi integration and modified to report PiG's state."

## What Pigpen already has

`components/herdr` (MIT, original work by Michael Kinsy, `upstreams: []`) is a Go extension that reports the same three states through the herdr CLI (`HERDR_BIN_PATH`), with a resume argv, a fake-herdr harness and a wire-level fake PiG host; `piglets/herdr` composes it. It is not derived from herdr-pig and must not be relabelled as derived.

## Layout

The Pigpen is Go-only (`scripts/go-only.mjs`), so the TypeScript extension cannot ship as is; the options are a Go port under Apache-2.0 or the TypeScript file as a Package that the Piglet builds with the Node runtime (which the Go-only gate refuses). The plan takes the Go port:

```
components/herdr-pig/
  LICENSE                 Apache-2.0, verbatim from herdr-pig
  NOTICE                  herdr-pig's NOTICE verbatim, then: "Go port by Pigpen contributors; the Herdr plugin and its scripts are not ported"
  README.md               what it adds over components/herdr, credit: Hai-Fai Solutions and Chris Polzer
  package.json            pig-package manifest, license Apache-2.0
  provenance.json         origin "derived", upstreams: Hai-Fai-Solutions/herdr-pig @ <sha> Apache-2.0, Herdr project (Pi integration) Apache-2.0
  extensions/herdr-pig/   Go module (go.mod, extension.go, fake-herdr socket harness, tests)
piglets/herdr-pig/
  piglet.yaml             name herdr-pig, packages: herdr-pig: local:../../components/herdr-pig, extensions: [herdr-pig]
  catalog.json, README.md credit line and the NOTICE pointer
```

The name `herdr-pig` avoids the existing `herdr`. The two Piglets must not be installed together (both report the same pane); the README of each says so, and `herdr-pig` strips nothing (opt-in strip, owner rule).

## Tests to prepare (red first, no upstream code copied)

Each of these is written from the contract above and Herdr's documented protocol, against a fake Herdr socket and the fake PiG host that components/herdr already uses:

1. silent outside Herdr (no `HERDR_ENV`, no socket, no pane id);
2. `session_start` in a TUI session reports `idle` with `resume_argv` = `pig --session <file|id>`;
3. a turn reports `working` then `idle` with strictly increasing `seq`;
4. print, JSON and RPC mode send nothing;
5. blocked while another extension holds the `herdr:blocked` signal (only if the Go SDK can read it; else recorded as not ported, as components/herdr does);
6. `pane.release_agent` only when PiG quits, not on a session switch;
7. a resume argv with an apostrophe, a control character or more than 8 KiB is not sent;
8. the socket is gone or refuses: the session continues and reports nothing (no retry loop);
9. Piglet level: `scripts/strip-inventory.mjs` is not needed (nothing stripped); `npm run platform-smoke -- --only herdr-pig` and the e2e harness run the Binary inside a fake herdr pane.

## Gate

1. Owner relays Chris's answer (credit text, whether he opens the PR, whether a Go port is welcome under Apache-2.0).
2. If he opens the PR: review it against this layout and tests.
3. If not: port per the layout above; NOTICE and LICENSE verbatim; credit in `README.md`, `piglets/herdr-pig/README.md` and `provenance.json`.
