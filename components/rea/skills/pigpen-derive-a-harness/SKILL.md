---
name: pigpen-derive-a-harness
description: Derive a new PiG piglet that works like an existing agent harness or CLI (for example GitHub Copilot CLI), from REA evidence. Use when the user asks to build a version of a harness, an agent CLI or an app's agent features as a piglet.
---

# Derive a harness as a piglet

REA's loop is "see a feature you like, understand how it works, build a version". Here the version is a PiG piglet. You
gather evidence with REA, write the behavior down in your own words, author the piglet only from that spec, and check the
result with PiG's own tools. Every iteration ends with `pig piglet validate` and `pig piglet show`.

## Workspace

Work in the current project directory:

- `evidence/`: REA output (inspect, extract, analyze, captures). Keep it out of version control (`evidence/` in
  `.gitignore`).
- `spec/`: what the target does, in your words. One file per area (agents, tools, prompt, skills, hooks, events, models).
  Every claim names the REA `evidence_id` (or the evidence file and field) it rests on. A claim REA could not establish is
  marked as an assumption, with the limitation REA reported.
- `piglets/<name>/`: the derived piglet, written from `spec/` only. Never paste target text from `evidence/` into it.

## 1. Gather evidence with REA

Follow the `pigpen-reverse-engineer-anything` skill (REA's own) for the REA calls themselves. For an agent CLI the usual order is:

1. Get the artifact the user runs (for an npm CLI: `npm pack <package>`, then the platform package it loads).
2. `inspect-artifact` on the executable: format (Node SEA, Bun, Electron, native), entry points, embedded assets.
3. `extract-artifact` when it is a container (a SEA blob, an asar, a tarball); confirm `containment_verified`.
4. `analyze-javascript-application` (or the native tools) on the extracted app. Large apps produce large output; read the
   result through the MCP tools, page by page, and keep the full output in `evidence/`.
5. When behavior depends on a model request (system prompt, tool schemas), capture one request with REA's capture tools
   (HAR/SAZ, or the proxy capture on Linux) instead of guessing.

Record each step's `evidence_id`s and `limitations` in `spec/` as you go.

## 2. Map the target onto piglet primitives

| Target part | Piglet primitive |
|---|---|
| agent or subagent definitions (name, description, model, effort, tools, prompt) | a subagents component plus `model:` |
| built-in skills | `skills:` |
| plugins, hooks, scripts | Go extensions (events and tools) plus `skills:` |
| built-in tools | the root `tools:` list over PiG's built-ins, plus Go extension tools for the rest |
| system prompt | `systemPrompt.file`, written from `spec/` |
| session events, API schemas | compare with PiG's RPC events; gaps become extension work or PiG issues |
| model access | a PiG provider (`pig --list-models` lists them, e.g. `github-copilot`) plus a `model:` preference |

Read the bundled PiG documentation before writing the manifest: `pig docs` prints the directory it syncs the docs to;
read `piglets.md`, `piglet-strip.md`, `piglet-lifecycle.md` and `mcp.md` there.

## 3. Rules that are easy to get wrong

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

## 4. Iterate

1. Run the piglet file directly with `pig --piglet ./piglets/<name>/piglet.yaml` until it behaves; building is slow.
2. End every iteration with:

   ```sh
   pig piglet validate ./piglets/<name>/piglet.yaml
   pig piglet show ./piglets/<name>/piglet.yaml
   ```

   Read the scope lines (tools kept, MCP line, strip rows) and fix what does not match `spec/`.
3. When it behaves, build: `pig piglet build ./piglets/<name>/piglet.yaml --format binary --out ./dist/<name>`.
4. Compare: run the same scripted tasks on the target and on the derived Binary, capture both with REA, and write the
   differences into `spec/` as the next iteration's work.

Report what each iteration changed, which `spec/` claims are now met, and which remain open with their evidence.
