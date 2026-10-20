# pi-permission-system (Go port, partial)

A Go extension for PiG: allow, ask and deny rules for tool calls. A rule names a surface (a tool such as `bash`, `read` or one of
your own) and a wildcard pattern; the last matching rule wins; a call that no rule names falls to the top-level `*` (default
`ask`). A denied call is refused with the reason the rule gives, and a tool that is denied outright is not offered to the model.

It is a port of `@gotgenes/pi-permission-system` 39.0.3 (see [CREDITS.md](CREDITS.md)), made with the
[extension porting Skill](../extension-port/README.md). It needs no Node runtime.

## Use

Put the rules in `<agent dir>/extensions/pi-permission-system/config.json` (`PI_CODING_AGENT_DIR`, default `~/.pi/agent`), in the
original's format:

```json
{
  "yoloMode": false,
  "permission": {
    "*": "ask",
    "path": "allow",
    "external_directory": "allow",
    "bash": {
      "*": "ask",
      "git status": "allow",
      "npm *": { "action": "deny", "reason": "Use pnpm instead" }
    },
    "write": "deny"
  }
}
```

```sh
pig install ./components/permissions
```

## What is ported

The wildcard matcher (`*`, `?`, a trailing ` *` that also matches the bare command, `~` and `$HOME`), rule evaluation and
merging, the config's `permission` map and `yoloMode` (comments allowed; a file the original's schema rejects is not used at
all, and a warning says why), the `tool_call` gate for `bash` (simple commands, alone or joined by `&&`, `||`, `;`, `|` or
newlines, each matched on its own; a wrapper such as `sudo`, `env`, `xargs`, `nice`, `eval` or `sh -c` is asked about even when
bash is allowed, as in the original) and for tools whose rules do not depend on a path, reading a listed skill's files under the
`skill` rules, the original's denial text, withholding fully denied tools, and the permissive-`*` warning.

**Not ported, and refused rather than allowed.** Every call the port cannot decide yet is blocked with one text,
`@pi-in-go/pigpen-permissions (Go port) does not support <feature> yet. Install the original extension,
npm:@gotgenes/pi-permission-system@39.0.3, which runs unchanged in PiG.` It is never allowed and never partly run. The features:

- an `ask` (the approval dialog and session approvals; `yoloMode` turns asks into allows, as in the original), including a
  wrapper running a harmless reader (`sudo ls`: the original allows it, the port asks) and a `/skill:<name>` input the skill
  rules ask about (an allowed skill continues and a denied one is handled with the original's notice);
- path-bearing tools (`read`, `write`, `edit`, `find`, `grep`, `ls`) whose rules have path patterns, or whose `path` and
  `external_directory` surfaces do not allow everything (path matching is not ported);
- a bash command the port cannot read (quotes, `$`, redirection, `&`, ...) when bash has patterns or would be allowed, and a
  command that runs another one the original reads by its own rule (`ssh`, `su`, `source`, ...);
- the `shellTools` option, and the MCP and skill tools;
- the project scope (`<project>/.pi/extensions/pi-permission-system/config.json`, read by the original only for a trusted project)
  and the agent scope (`permission` in the frontmatter of `<agent dir>/agents/<name>.md` and `<project>/.pi/agents/<name>.md`, for the
  active agent): while one applies, every call the port would allow is refused, and a warning at session start says which file;
- the config options that nothing reads yet (`debugLog`, `permissionReviewLog`, `doublePressToConfirm`, `permissionDialogKeys`,
  `promptNotifications`, `forwardingTimeoutMs`, the prompt and log width limits, `piInfrastructureReadPaths`, `authorizerChain`):
  a warning at session start names the ones that are set.

Not registered at all, and so absent rather than refused: the `/permission-system` command and its config modal, the cross-extension
service, the review and debug logs and forwarding between sessions. PiG's `powershell` tool is not a bash alias here: deny it, or
name it in `shellTools` so that it is refused. See [port/PORT.md](port/PORT.md).
