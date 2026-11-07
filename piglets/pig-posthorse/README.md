# pig-posthorse

PiG with [posthorse](../../components/posthorse/README.md), a Go port of pi-posthorse (Mitch Fultz, MIT): when a long task fills the
context window, PiG starts a fresh one **without a model-written summary**. The model keeps task notes under `.pi/notes`, finds earlier
conversation with the `history` tool, and gets a bounded recovery record instead of a summary. No network call, no Node.

| Tool | Use |
|---|---|
| `new_context({handoff?})` | Request a clean reset with no retained messages once the foreground tools succeed. |
| `get_context_remaining()` | Estimate tokens left until rollover and the configured context limit. |
| `notes({op,...})` | List, read, search, write or append task notes (fork-private copies). |
| `history({op,...})` | Search earlier conversation and read entries, including images; `all: true` spans the project's sessions. |

Settings are Pi's own `compaction.enabled`, `compaction.reserveTokens` and `compaction.keepRecentTokens`. Keep exactly one
compaction-policy extension loaded.

## Run from a checkout

```sh
npm ci --ignore-scripts
npm run stage
pig piglet validate dist/staged/piglets/pig-posthorse/piglet.yaml
pig --piglet dist/staged/piglets/pig-posthorse/piglet.yaml
```

Build a Piglet Binary (needs a git checkout of the PiG source the `pig` was built from; pig 0.7.0 or later):

```sh
PIG_SOURCE_ROOT=/path/to/pig-git-checkout npm run build:piglet -- pig-posthorse
```

The Binary fuses the extension (`extensionRealization: fused`). No release is published and no support is claimed.

## Credits

Posthorse: [components/posthorse/CREDITS.md](../../components/posthorse/CREDITS.md). The composition is MIT, Michael Kinsy.
