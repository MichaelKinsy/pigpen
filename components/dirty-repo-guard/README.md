# dirty-repo-guard (Go port)

A Go extension for PiG that stops you from starting a new session, switching
session or forking while your git working tree has uncommitted changes, unless you
say it is fine:

```text
You have 2 uncommitted file(s). new session anyway?
  Yes, proceed anyway
  No, let me commit first
```

Choosing the second option (or dismissing the dialog) cancels the action and shows
"Commit your changes first". Outside a git repository, or when git fails, the action
is allowed. Without a UI (print and JSON mode) a dirty tree cancels without asking.

This is a port of Pi's `dirty-repo-guard.ts` example (see [CREDITS.md](CREDITS.md)),
made with the [extension porting Skill](../extension-port/README.md) as the worked
example of its workflow. It needs no Node runtime.

## Install

```sh
pig install ./components/dirty-repo-guard
```

The extension builds from source on first use (Go toolchain required), or fuses into
a Piglet Binary. It is a Package: `pig package validate ./components/dirty-repo-guard`.
Package discovery uses the `pig-package` keyword in `package.json`. The Package is
`private` until the owner publishes it.

## Proof that it behaves like the original

`port/` holds the evidence and it ships with the Package:

| Path | What |
|---|---|
| `port/oracle/dirty-repo-guard.ts`, `port/oracle/LICENSE` | The unmodified original and Pi's license. |
| `port/scenarios/*.json` | 11 scripted event sequences: decline, proceed, dismissed dialog, clean, not a repository, git missing, canned git output (blank lines, U+00A0/U+FEFF, exit 128), fork decline and proceed. |
| `port/golden/*.jsonl` | The traces the **original recorded under Pi 0.87.1** for those scenarios: every dialog, notification, `git` call and exit, model request, event and command response, in order. Each is also identical under PiG's Node runtime. |
| `port/mutations.json` | 26 deliberate defects. Every one must be caught. |
| [`port/PORT.md`](port/PORT.md) | The mapping table, the SDK gap check, and the results. |

Re-check the port against the recorded Pi traces (no Pi needed):

```sh
PIG_BIN=/path/to/pig npm run test:port         # from the Pigpen root
```

Re-run the original under Pi as well: add `PI_BIN=/path/to/pi`. Go tests (fake PiG
host, `hasUI == false`, wording, helpers): `PIG_BIN=/path/to/pig npm run test:go-ports -- -race`.

## Differences from the original

None observable in the recorded scenarios. Two deliberate implementation notes:
`git status` output is trimmed and counted with JavaScript's `trim()` whitespace set
(not Go's), and a failure of the host call itself (as opposed to git exiting non-zero)
is returned as an error rather than treated as "not a repository".

MIT. Original © Mario Zechner (Pi); port © Michael Kinsy.
