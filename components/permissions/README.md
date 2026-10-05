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

**Not ported, and blocked rather than allowed:** an `ask` (the approval dialog is not ported: the call is refused and says so;
`yoloMode` turns asks into allows), path-bearing tools (`read`, `write`, `edit`, `find`, `grep`, `ls`) whose rules have path
patterns, a bash command the port cannot read (quotes, `$`, redirection, `&`, ...) when bash has patterns or would be allowed,
a wrapper running a harmless reader (`sudo ls`: the original allows it, the port asks), tools named in `shellTools`, and MCP and
skill tools. Also not ported: project and agent scopes, session approvals, the review log, the config modal. PiG's `powershell`
tool is not a bash alias here: deny it, or name it in `shellTools` so that it is refused. See [port/PORT.md](port/PORT.md).
