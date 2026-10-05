# Migration guide: the Pi infrastructure read bypass is narrowed

Read-only tools (`read`, `find`, `grep`, `ls`) skip the `external_directory` gate for Pi infrastructure paths.
Before this change, that bypass covered **everything** under `~/.pi/agent/` (or `$PI_CODING_AGENT_DIR`), including the credential store, and it ran before policy was consulted, so no `external_directory` rule could stop it.

This is a **breaking change** in three ways, each taking effect on upgrade with no config edit.

## What changes

### Only Pi's harness entries stay auto-allowed

The bypass now covers only these entries under `~/.pi/agent/`:

- the directories `agents/`, `extensions/`, `git/`, `npm/`, `prompts/`, `skills/`, and `themes/`;
- the files `settings.json`, `SYSTEM.md`, `APPEND_SYSTEM.md`, and `AGENTS.md`.

Everything else there now goes through the `external_directory_read` gate like any other outside path.
That includes `auth.json`, `models.json`, `mcp.json`, `mcp-oauth/`, `trust.json`, `sessions/`, `subagent-sessions/`, `bin/`, and crash logs.

The global `node_modules` root, Pi's own install directory, project-local `.pi/npm/` and `.pi/git/`, and `piInfrastructureReadPaths` entries are unchanged.

### This package's logs are never auto-allowed

`~/.pi/agent/extensions/pi-permission-system/logs/` holds the review and debug logs, which record tool input.
It sits inside `extensions/`, but the bypass now excludes it, even when a `piInfrastructureReadPaths` entry covers it.

### A deny naming an infrastructure path now blocks

Policy is resolved before the bypass.
A deny from an `external_directory` or `external_directory_read` rule whose pattern names the path now blocks the read.
A catch-all `"*"` deny, the universal `permission["*"]` fallback, and any `ask` still leave the bypass in place, so a deny-by-default policy keeps its skill and package reads.

## Before and after

Each row is a `read` of the named path under `~/.pi/agent/`.

| Policy                                                                    | Path                       | Before  | After       |
| ------------------------------------------------------------------------- | -------------------------- | ------- | ----------- |
| `"external_directory_read": {"*": "deny"}`                                | `auth.json`                | allowed | **denied**  |
| `"external_directory_read": {"*": "deny"}`                                | `sessions/<id>.jsonl`      | allowed | **denied**  |
| `"external_directory_read": {"*": "ask"}`                                 | `sessions/<id>.jsonl`      | allowed | **asked**   |
| `"external_directory_read": {"*": "deny"}`                                | `extensions/…/logs/…`      | allowed | **denied**  |
| `"external_directory_read": {"*": "deny"}`                                | `skills/<name>/SKILL.md`   | allowed | allowed     |
| `"external_directory_read": {"*": "deny"}`                                | `settings.json`            | allowed | allowed     |
| `"external_directory_read": {"~/.pi/agent/git/x/SKILL.md": "deny"}`       | `git/x/SKILL.md`           | allowed | **denied**  |
| `"external_directory": {"*": "deny"}`                                     | `git/x/SKILL.md`           | allowed | allowed     |

## Restoring a read

Grant the path on `external_directory_read`, where the grant is visible in your config:

```jsonc
"permission": {
  "external_directory_read": { "~/.pi/agent/sessions/*": "allow" }
}
```

A `piInfrastructureReadPaths` entry also restores the bypass for any path except this package's logs directory.
For the logs, only an `external_directory_read` rule re-allows the read.

## Writing a targeted deny

Rules are last-match-wins, so put a catch-all first and the targeted deny after it:

```jsonc
"permission": {
  "external_directory_read": {
    "*": "ask",
    "~/.pi/agent/skills/untrusted/*": "deny"
  }
}
```

Written the other way round, the catch-all is the matching rule, and a catch-all does not override the bypass.
