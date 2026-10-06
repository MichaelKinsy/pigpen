# Port record: @dietrichgebert/ponytail (Pi extension and Skills)

| Input | Identity |
|---|---|
| Original | `@dietrichgebert/ponytail` 4.10.0 by Dietrich Gebert, https://github.com/DietrichGebert/ponytail, commit `1d95ff7d39de12d87014ea40d4e22201bddc501b`, MIT |
| Oracle | Pi 1.0.3, Node 24.19.0, with the unmodified `pi-extension/`, `hooks/` and `skills/` at `port/oracle` (the extension requires the two helper modules in `hooks/`) |
| Target | PiG 0.4.1+1.0.3 (release v0.4.1, commit `3ee745c8cda3c1a9a8d71112c140790c4d64d78d`), go1.27.1 |
| Original's tests | `pi-extension/test`: 23 cases (12 extension, 11 helpers): **22 exact twins, 1 named skip** |
| Not ported | the project's other platform adapters (Claude Code hooks, OpenCode, Cursor, Gemini, Qoder, Copilot, Hermes and the MCP server) and their tests under `tests/`: they are not a Pi extension and are not in the oracle |
| Kind | 1: an extension and six Skills, no CLI built-in, no provider |

## Mapping

| ID | Original | Go | Scenario / test |
|---|---|---|---|
| M1 | `hooks/ponytail-config.js`: modes, default level, quiet and hide flags, config file read and write | `config.go` | `config-default-ultra`, `config-quiet-hide`, `config-default-written`; `helpers_twin_test.go`, `extra_test.go` |
| M2 | `hooks/ponytail-instructions.js`: the ruleset text of a level from `skills/ponytail/SKILL.md` | `instructions.go`, `ponytail_skill.md` (the shipped Skill) | `ruleset-full`, `mode-lite`, `mode-ultra` (the system prompt of every request); twins |
| M3 | `pi-extension/index.js`: `/ponytail` and its subcommands, session mode records, the input phrases, the status indicator, the before-agent injection | `extension.go` | `mode-*`, `bare-command`, `command-*`, `normal-mode`, `stop-ponytail`; `extension_twin_test.go`, `extra_test.go` |
| M4 | the five alias commands | `extension.go` | `extension_twin_test.go` (named after the renamed Skills) |
| M5 | `skills/*/SKILL.md` | `skills/pigpen-ponytail*`, built by `port/adapt.py` | `npm run check` (Skill gate); [ADAPTATIONS.md](ADAPTATIONS.md) |

## SDK gap check (`pigeq gaps --ts port/oracle/pi-extension/index.js`)

No `MISSING`. The original reads `ctx.ui.theme`, which the Go SDK has no counterpart for (G1).

## Results (this revision)

- Red commit `feec457b`: 22 twins against zero-value stubs, all failing for the right reason (`port/red-run.log`).
- Green: `go test -race ./...` passes.
- `pigeq check` against goldens **recorded from the original under Pi 1.0.3**: 13 of 13 scenarios, plus `port-gaps`
  and `exec-coverage`. The scenarios compare the system prompt of the model request at each level, the notices, the
  session records and the config file the original writes (field order and indentation included).
- `pigeq mutate --unit`: 69 mutations, all killed (the first run killed 59; 9 survived and 3 did not build; each
  survivor got a test, each invalid mutation was rewritten, and `label-case` was removed as an equivalent mutant:
  `normalizeMode` trims by itself).
- `pig package validate components/ponytail` and `pig install <extension dir> --validate-only --json`: valid.

## Differences and findings

1. **G1: no theme, so the indicator is plain text.** Pi draws it with `theme.fg(...)`, whose output depends on the
   terminal's colour mode (the recorded golden holds `\e[39m\e[2m○\e[22;39m ...`). An extension process cannot
   reproduce it, so the scenarios keep the indicator hidden (`hideStatus` in the config) and the indicator is a layer-1
   twin. The named skip is `status bar stays silent when ui lacks a theme`: with no theme object there is nothing to be
   absent.
2. **F2: PiG's host reports an empty status text as a missing one.** Turning ponytail off clears the indicator with
   `setStatus("ponytail", "")`; Pi's RPC mode reports `statusText: ""`, PiG's omits it. This shows even with the original
   on both hosts (`host check: pig-ts differs from pi-ts`), so it is a host difference, not a port one. The three
   scenarios that turn ponytail off also hide the indicator; the clearing is tested in `TestStatusIndicator`.
3. **The aliases name the renamed Skills** (`/skill:pigpen-ponytail-review`), so they are layer-1 twins, not scenarios.
4. **No fallback ruleset** (the Skill is embedded and cannot be missing).
5. **A null event cannot reach a Go handler**, so the twin of `before_agent_start guards missing event` checks the
   missing and empty system prompt only.
6. **The config file is rewritten with the original's field order and indentation** (an ordered rewrite); a value
   with unusual escapes is kept as written, where JavaScript would re-serialize it.
7. The extension reads the session's records with `SessionManager().GetBranch`, as the original reads `getBranch`.
