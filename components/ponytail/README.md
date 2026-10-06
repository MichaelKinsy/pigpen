# ponytail (Go port)

The "lazy senior dev" mode for PiG: a ruleset added to the system prompt that makes the model reach for the simplest
thing that works (do not build it, reuse it, use the standard library, one line before fifty), a `/ponytail` command to
choose the level, and five more commands that run the review, audit, gain, debt and help Skills. It is a port of
`@dietrichgebert/ponytail` 4.10.0 by Dietrich Gebert (see [CREDITS.md](CREDITS.md)), made with the
[extension porting Skill](../extension-port/README.md). It needs no Node runtime and starts no process or network call.

## Install

```sh
pig install ./components/ponytail
```

or take the [`pig-essentials`](../../piglets/pig-essentials/README.md) Piglet, which selects it.

## Use

The default level is `full`. `/ponytail lite|full|ultra|off` sets the level for the session (recorded in the session, so
a resumed session keeps it), `/ponytail` alone sets the default level, `/ponytail status` shows the current and the default
level, and `/ponytail default <level>` saves a default (`review` is a session level only, never a default). Saying
`stop ponytail` or `normal mode` as a whole message turns it off; a sentence that merely mentions normal mode does not.
`/ponytail-review`, `/ponytail-audit`, `/ponytail-gain`, `/ponytail-debt` and `/ponytail-help` run the Skills of the same
name (`pigpen-ponytail-review` and so on).

The default level comes from `PONYTAIL_DEFAULT_MODE`, else `defaultMode` in `$XDG_CONFIG_HOME/ponytail/config.json`
(`~/.config/ponytail/config.json`, `%APPDATA%\ponytail\config.json` on Windows), else `full`. `quietStartup` (or
`PONYTAIL_QUIET_STARTUP`) silences the "Ponytail loaded" notice and `hideStatus` (or `PONYTAIL_HIDE_STATUS`) hides the
status-bar indicator; the ruleset stays active.

## Differences from the original

- **The Skills are named `pigpen-ponytail*`** (Pigpen's Skill gate requires the prefix), so the five alias commands run
  `/skill:pigpen-ponytail-review` and so on, not `/skill:ponytail-review`.
- **The status indicator is plain text.** The original colours it with the host's theme; the Go SDK has no theme.
- **No fallback ruleset.** The original falls back to a built-in copy when it cannot read the Skill; the Skill is embedded
  here, so it cannot be missing.

The other platforms the project supports (Claude Code hooks, OpenCode, Cursor, Gemini and the rest) are not part of this
port. The proof that it matches the original is in [port/PORT.md](port/PORT.md).
