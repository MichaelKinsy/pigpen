---
name: pigpen-derive-a-harness
description: Derive a new PiG piglet that works like an existing agent harness, CLI or terminal app, from REA evidence. Inventory the target, write its behavior down, build the piglet from that, measure the two side by side, and re-check the inventory before calling it done. Use when the user asks to build a version of a harness, an agent CLI or an app's agent features as a piglet, or asks how close such a piglet is to its target.
---

# Derive a harness as a piglet

REA's loop is "see a feature you like, understand how it works, build a version". Here the version is a PiG piglet. You
gather evidence with REA, list everything the target offers, write the behavior down in your own words, author the piglet
only from that, and measure the piglet against the target.

Three failures are common, and the steps below exist to prevent them:

1. **A result that looks empty and was only partial.** A tool can exit 0 and still have skipped the file that matters.
   Absence in a partial result says nothing about the target.
2. **A piglet that looks like the target and does not act like it.** A setting that is accepted, saved and shown, a
   dialog with every tab, a status line that says "enabled": all of these pass a screen comparison with nothing behind
   them.
3. **"Done" taken from a score.** A comparison score says the compared rows match. Only a list of the target's whole
   surface, re-checked against the current build, says what is missing.

## Workspace

Work in the current project directory:

- `evidence/`: REA output and captures. Keep it out of version control (`evidence/` in `.gitignore`).
- `spec/`: what the target does, in your words. One file per area. Every claim names the REA `evidence_id` (or the
  evidence file and field) it rests on and is marked `observed`, `static` or `assumption`.
- `spec/inventory.md`: one row per item of the target's surface, and the piglet's status on it.
- `piglets/<name>/`: the derived piglet, written from `spec/` only.
- `compare/`: the scripts that run the same steps on the target and on the piglet, and their output.

## The loop

| Step | What it produces | Read |
|---|---|---|
| 1. Gather evidence | `evidence/`, with each result's coverage and limits written down | [references/evidence.md](references/evidence.md) |
| 2. Inventory the surface | `spec/inventory.md`; the scope agreed with the user | [references/inventory.md](references/inventory.md) |
| 3. Write the spec | `spec/*.md`, each claim with its evidence and kind | below |
| 4. Author the piglet | `piglets/<name>/` | [references/clean-room.md](references/clean-room.md) |
| 5. Measure | rows that match, rows that differ and why; effect checks | [references/measure.md](references/measure.md) |
| 6. Close | every inventory row re-checked on the current build, with proof | [references/closing.md](references/closing.md) |

Steps 4 to 6 repeat. Step 6 is not optional and is not the same as step 5.

For the REA calls themselves follow the `pigpen-reverse-engineer-anything` skill (REA's own).

## Map the target onto piglet primitives

| Target part | Piglet primitive |
|---|---|
| agent or subagent definitions (name, description, model, effort, tools, prompt) | a subagents component plus `model:` |
| built-in skills | `skills:` |
| plugins, hooks, scripts | Go extensions (events and tools) plus `skills:` |
| built-in tools | the root `tools:` list over PiG's built-ins, plus Go extension tools for the rest |
| slash commands, dialogs, settings, the command line | a Go extension |
| the screen (layout, footer, popups, keys) | a frontend member, or extension widgets when PiG's own screen is close enough |
| system prompt | `systemPrompt.file`, written from `spec/` |
| session events, API schemas | compare with PiG's RPC events; gaps become extension work or PiG issues |
| model access | a PiG provider (`pig --list-models` lists them) plus a `model:` preference |

Read the bundled PiG documentation before writing the manifest: `pig docs` prints the directory it syncs the docs to;
read `piglets.md`, `piglet-strip.md`, `piglet-lifecycle.md` and `mcp.md` there.

## PiG rules that are easy to get wrong

- A piglet that has an `extensions:` section but no `- name: pig-mcp-adapter` entry hides every MCP tool. Add the entry
  when the harness needs MCP tools; give it a `tools:` list to narrow them.
- The root `tools:` list is a ceiling, not an activation list. A run activates only the default tools (`read`, `bash`,
  `edit`, `write`) that the list keeps.
- `systemPrompt` replaces PiG's prompt. To add a paragraph instead, use an extension's `before_agent_start` handler.
- A Go member must export `func Extension() *sdk.Extension`; `pig piglet validate` rejects a package without it.
- `discovery: {extensions: [], skills: []}` loads no ambient resources. A Binary that is not `build.closed` accepts an
  overlay piglet at launch (`--piglet`), which is how users adapt it without a rebuild.
- `strip` entries marked `stripped(binary)` in `pig piglet show` cannot be brought back by an overlay.
- Building a Binary needs Go and a checkout of the PiG source the `pig` was built from (`PIG_SOURCE_ROOT`), or
  `--builder container`. Check both before you build, and say which is missing.

## Iterate

1. Run the piglet file directly with `pig --piglet ./piglets/<name>/piglet.yaml` until it behaves; building is slow.
2. End every iteration with:

   ```sh
   pig piglet validate ./piglets/<name>/piglet.yaml
   pig piglet show ./piglets/<name>/piglet.yaml
   ```

   Read the scope lines (tools kept, MCP line, strip rows) and fix what does not match `spec/`.
3. When it behaves, build: `pig piglet build ./piglets/<name>/piglet.yaml --format binary --out ./dist/<name>`.
4. Measure the Binary against the target (step 5) and write the differences into `spec/` as the next iteration's work.

## Report

After each iteration say what changed, which `spec/` claims are now met and which remain open with their evidence. When
you report progress, give the inventory's counts per status and the date of the last re-check beside any comparison
score, and name what was not checked. Never give a comparison score as the share of the target that is built.
