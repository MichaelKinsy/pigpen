# archon-provider-pig

An [Archon](https://github.com/coleam00/Archon) agent provider that runs Archon sessions through the
`pig` CLI, so a workflow can name `provider: pig`.

Archon loads providers as separate processes over newline-delimited JSON-RPC on stdio; this component
implements that wire and translates PiG's own event stream into the provider contract's chunks.

## Where it lives

The source is `components/archon-provider-pig` in [MichaelKinsy/pigpen](https://github.com/MichaelKinsy/pigpen).
Archon installs from `owner/repo[/path][@tag]`, and its release tags cannot contain `/`, so pigpen's own
component tags (`components/archon-provider-pig/v0.1.0`) can never be named in an install. Without a tag
Archon takes the repository's latest release, which in a monorepo belongs to some other component. So the
installable plugin is a separate repository, [MichaelKinsy/archon-provider-pig](https://github.com/MichaelKinsy/archon-provider-pig),
that pigpen's release workflow generates from this directory: pushing the tag
`components/archon-provider-pig/vX.Y.Z` here commits this directory to that repository's `main`, tags it
`vX.Y.Z` and publishes a release there with the six `archon-provider-pig-<os>-<arch>` binaries
(linux, darwin and windows, on x64 and arm64) and a `checksums.txt`. Change the plugin here, never there.

Archon keeps only Claude Code, Codex and Pi in its own repository. Its contributor guide,
"Build a community provider", says a community provider "ships from its authors' repository as an
optional plugin" and attaches "without an import in Archon's registry", and its plugin guide says no
Archon pull request is required. The generated repository carries the `archon-plugin` topic, which is
what lists it in Archon's plugin index.

## Install

```bash
archon plugin add MichaelKinsy/archon-provider-pig
```

Then name the provider and a model in a workflow. Pi already owns unprefixed model refs, so a model
ref that must resolve *without* a provider node is written `pig/<vendor>/<model>`; inside a node, or
at workflow level, `provider: pig` is explicit and the model carries no prefix:

```yaml
provider: pig
model: openrouter/anthropic/claude-haiku-5.5

nodes:
  - id: hello
    prompt: "Reply with exactly PIG_OK and nothing else."
```

A node that reads or writes files needs no extra configuration; PiG's own tool set applies. The
plugin strips a leading `pig/` before handing a model to `pig`, so both spellings reach PiG as
`openrouter/anthropic/claude-haiku-5.5`.

PiG owns its own authentication (`pig login`, `pig auth check`); this provider carries no credentials
and never replaces `~/.pig`.

## Development

```bash
bun test          # event mapping, failure classes and vendor parsing; candidates in map.ts
```

Archon resolves providers from receipts under `$ARCHON_HOME/plugins`, so a plugin can be tested
before anything is published. Stage it by hand:

```bash
AH=${ARCHON_HOME:-$HOME/.archon}
mkdir -p "$AH/plugins/installed/MichaelKinsy/archon-provider-pig"
printf '#!/bin/sh\nexec %s %s "$@"\n' "$(command -v bun)" "$PWD/src/main.ts" \
  > "$AH/plugins/archon-provider-pig"
chmod 755 "$AH/plugins/archon-provider-pig"
```

and write `$AH/plugins/installed/MichaelKinsy/archon-provider-pig/receipt.json`
with the descriptor the plugin answers `initialize` with, `tag: dev`, the repository commit, and
`files: [{ path: "archon-provider-pig", sha256 }]` over the staged executable. The executable lives
at the plugins root, not inside `installed/`: Archon spawns `plugins/<executable>` and stages the
receipt separately. `archon doctor providers` then lists `pig`, and a workflow can name
`provider: pig`.

Proven on a macOS arm64 host against Archon `dev` (bun 1.4.2, SQLite): `doctor providers` reports
`pig (0.1.0)` with a live `pig auth check`, `runProviderConformance` passes over the executable, a
live turn returns a thought block, a reply block, the PiG session id and `tokens`, and a two-node
workflow (`prompt` + real `read` tool) completed with its assertions passing. Cancelling mid-turn
yields `stopReason: cancelled` and no `settled`, with no `pig` process left behind.
