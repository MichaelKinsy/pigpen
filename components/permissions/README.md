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
merging, the config's `permission` map and `yoloMode` (comments and a `$schema` key allowed; a file the original's schema
rejects is not used at all, and a warning says why), the `tool_call` gate for `bash` (simple commands, alone or joined by
`&&`, `||`, `;`, `|` or newlines, each matched on its own; a wrapper such as `sudo`, `env`, `xargs`, `nice`, `eval` or
`sh -c` has its allow floored to **ask** unless its payload provably only reads (`sudo ls` is allowed by the inner
command's rule, `sudo rm x` asks), while a command that merely runs another one without a wrapper flag — `ssh`, `source`,
`bash script.sh` — and a command the port reads by neither its patterns nor its parser are decided by the rules alone, as
in the original) and for the other tools by name, reading a listed skill's files under the `skill` rules, the original's denial text,
withholding fully denied tools, and the permissive-`*` warning.

**The ask** is the approval dialog, in PiG's select dialog: *Approve*, *Approve for this session*, *Deny* and *Deny with a
reason*. A dismissal, a missing UI or a dialog failure denies, fail-closed; an approval for the session records a rule that
answers the next matching call, cleared with the session; `yoloMode` turns asks into allows, as in the original. The skill
gates (a listed skill's files, a `/skill:<name>` input) ask without the session option, as in the original, and a denied
`/skill:` input is handled with the original's notice.

**Paths.** A call's `path` input and the words of a simple `bash` command are judged against the working directory: a path
inside it is decided by the tool's rules (and by explicit `path` rules, which the original's backward-compatibility rule
makes fire only when the config writes them), and a path outside it consults `external_directory_read` /
`external_directory_write` (or the bare `external_directory`, folded most-restrictive, when the tool proves no direction:
`edit`, an unknown command). Reads and writes of your project work; an external write asks.

**Refused, and never allowed** (`refusal.go`; each is machinery the original has and the port does not):

- a `bash` command the port cannot read (quotes, `$`, redirection, `&`, ...) while the config has bash *patterns*: the raw
text cannot be matched against rules the original matches against its parse. With no patterns the command asks instead;
- a path-bearing tool (`read`, `write`, `edit`, `find`, `grep`, `ls`) whose own rules have path patterns;
- a tool that `shellTools` routes through the bash surface, and the MCP and skill *surfaces* (a `mcp`, `skill` or
`mcp__server__tool` tool call);
- the project scope (`<project>/.pi/extensions/pi-permission-system/config.json`, read by the original only for a trusted
project) and the agent scope (`permission` in the frontmatter of an agent definition): while one applies, every call the
port would allow is refused, and a warning at session start says which file;
- the config options that nothing reads yet (`debugLog`, `permissionReviewLog`, `doublePressToConfirm`,
`permissionDialogKeys`, `promptNotifications`, `forwardingTimeoutMs`, the prompt and log width limits,
`piInfrastructureReadPaths`, `authorizerChain`): a warning at session start names the ones that are set.

**Not ported at all, and diverging rather than refused:** path *canonicalization* (a pattern that names only a symlink-
resolved or differently spelled alias of a path is not matched; the port matches the path as resolved against the working
directory), the original's inline keybind dialog (PiG's picker instead), the bash arity table (a session grant suggests
`<first word> *` where the original can suggest a longer exact prefix), forwarding of approvals between sessions, the
review and debug logs, the config modal and the cross-extension service. PiG's `powershell` tool is not a bash alias here:
deny it, or name it in `shellTools` so that it is refused. See [port/PORT.md](port/PORT.md).
