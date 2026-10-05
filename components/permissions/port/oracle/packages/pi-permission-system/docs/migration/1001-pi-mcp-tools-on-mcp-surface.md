# Migration guide: Pi's built-in MCP tools are gated on the `mcp` surface

Starting with the release that closes [#1001], `mcp` permission rules apply to the tools Pi's built-in MCP registers, `mcp__<server>__<tool>`.
Before it, those tools were gated like any other extension tool, under their own name, and no `mcp` rule reached them.

This is a **breaking change**: a config that already has `mcp` rules changes how Pi's MCP tools resolve on upgrade, with no edit.
A config with no `mcp` rules and no top-level `mcp__…` keys is unaffected — both paths fall back to `"*"`.

## What changes

Each row is a policy for a server configured as `danger-srv` in `mcp.json`, whose `wipe` tool Pi registers as `mcp__danger_srv__wipe`.

| Policy                                                        | Before  | After                                            |
| ------------------------------------------------------------- | ------- | ------------------------------------------------ |
| `"*": "allow"`, `"mcp": {"*": "allow", "danger-srv": "deny"}` | allowed | **denied**, by `danger-srv`                      |
| `"*": "ask"`, `"mcp": "allow"`                                | asked   | **allowed**, by `mcp`'s catch-all                |
| `"*": "allow"`, `"mcp__danger_srv__wipe": "deny"`             | denied  | denied, plus a notice asking you to port the key |
| `"*": "allow"`, `"mcp__danger_srv__*": "deny"`                | denied  | denied, plus a notice asking you to port the key |
| `"*": "ask"`                                                  | asked   | asked                                            |

The *After* column is each policy resolved through the released permission manager; the first and third *Before* entries are the reproduction in [#1001], and the others follow from the tool falling back to `"*"` when no top-level key names it.

A denied tool is also withheld from the model, so the first row now hides `mcp__danger_srv__wipe` where it used to be offered.

A server rule matches in either spelling — `danger-srv` as in `mcp.json`, or `danger_srv` as in the tool name — because Pi refuses two servers whose names differ only in `-` and `_`.
The full list of names a call is looked up under is in [Pi's built-in MCP tools](../configuration.md#pis-built-in-mcp-tools).

## Porting a top-level `mcp__…` key

A top-level key naming a Pi MCP tool was the only rule that reached it, so it keeps working: it is also applied as an `mcp` rule with the key as its pattern, after every other `mcp` rule.
The key keeps its original meaning too, so `"mcp__*"` still covers a tool from another extension whose name merely starts with `mcp__`; a key that can name no Pi MCP tool, such as `"mcp__foo"`, is left alone and raises no notice.
At session start you see a notice naming each such key:

```text
Top-level permission keys naming Pi MCP tools are applied as "mcp" rules: "mcp__danger_srv__wipe". Move them under "mcp" — see https://github.com/gotgenes/pi-packages/blob/main/packages/pi-permission-system/docs/migration/1001-pi-mcp-tools-on-mcp-surface.md
```

To port it, move the key under `mcp` unchanged — the full Pi name is one of the tool's `mcp` targets:

```jsonc
// Before
{
  "permission": {
    "*": "allow",
    "mcp__danger_srv__wipe": "deny"
  }
}

// After
{
  "permission": {
    "*": "allow",
    "mcp": {
      "*": "allow",
      "mcp__danger_srv__wipe": "deny"
    }
  }
}
```

Inside `mcp`, rule position decides: put the key **after** any `"*"` catch-all, or the catch-all wins.

When the rule was meant for the whole server, a server rule says so directly and survives a tool being renamed:

```jsonc
{
  "permission": {
    "mcp": {
      "*": "allow",
      "danger-srv": "deny"
    }
  }
}
```

A wildcard key such as `"mcp__danger_srv__*"` ports the same way, or becomes the server rule above.

## Other top-level wildcards

Only keys starting with `mcp__` carry over.
A top-level wildcard of another shape that used to match a Pi MCP tool's name, such as `"mcp_*"` or `"*__wipe"`, no longer reaches it, because the tool now resolves on the `mcp` surface rather than under its own name.
It raises no notice; rewrite it as an `mcp` rule (`"mcp": {"*__wipe": "deny"}`) if you relied on it.

## Rules written for Pi 0.99.0 and 0.99.1

Pi 0.99.0 and 0.99.1 kept `-` in the tool name (`mcp__danger-srv__wipe`); Pi 0.99.2 replaced it with `_`.
A top-level key written with the old spelling has matched nothing since 0.99.2.
It raises the notice like any other `mcp__…` key, but relocating it does not make it match: no tool carries that name anymore.
Replace it with a server rule as above, which matches `danger-srv` in either spelling.

[#1001]: https://github.com/gotgenes/pi-packages/issues/1001
