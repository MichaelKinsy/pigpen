# pig-rea-sealed: the REA harness, closed

[`pig-rea`](../pig-rea/README.md) in a closed Binary (`build.closed: true`): the same REA members, skills and dashboard, and
nothing else. It refuses overlay Piglets (`--piglet`, `PIG_PIGLET_PATH`), `-e`, `settings.json` extensions, servers from
`mcp.json` and installs, and it carries no TypeScript or Go sidecar runtime. Every machine gets the same tool surface, which
is what CI and evidence compared across runs need. REA's own server still connects: it is registered by the Binary's `rea`
member, not read from `mcp.json`.

```sh
npm run payload:rea
npm run build:piglet -- pig-rea-sealed
dist/bin/pig-rea-sealed -p "Inspect ./target with REA and summarize what it is."
```

To change it, derive another Piglet from `pig-rea` and rebuild.

Known PiG issue: a project with its own `.pig/mcp.json` makes PiG 0.7.0 load the user's `~/.pig/agent/extensions` in its
pre-trust pass, which fails startup here because the Binary has no TypeScript runtime. See the `pig-rea` README.

Only darwin/arm64 has been built and run (101.5 MB).
