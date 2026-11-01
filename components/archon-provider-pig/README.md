# archon-provider-pig

An [Archon](https://github.com/coleam00/Archon) agent provider that runs Archon sessions through the
`pig` CLI, so a workflow can name `provider: pig`.

Archon loads providers as separate processes over newline-delimited JSON-RPC on stdio; this component
implements that wire and translates PiG's own event stream into the provider contract's chunks.

## Why this lives in Pigpen

Archon keeps only Claude Code, Codex and Pi in its own repository. Its contributor guide,
"Build a community provider", says a community provider "ships from its authors' repository as an
optional plugin" and attaches "without an import in Archon's registry", and its plugin guide says no
Archon pull request is required. Archon's plugin index walks a repository tree for
`archon-plugin.json` and derives the plugin id from the containing directory, so this path is listed
as `MichaelKinsy/pigpen/components/archon-provider-pig` once the repository carries the
`archon-plugin` topic.

## Install

```bash
archon plugin add MichaelKinsy/pigpen/components/archon-provider-pig@components/archon-provider-pig/v0.1.0
```

Then set `provider: pig` and a model such as `pig/openrouter/anthropic/claude-haiku-5.5` in a workflow
node. Pi already owns unprefixed model refs, so a pig model is always prefixed.

PiG owns its own authentication (`pig login`, `pig auth check`); this provider carries no credentials
and never replaces `~/.pig`.

## Development

```bash
bun test          # event mapping against recorded PiG turns in fixtures/
```
