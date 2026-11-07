# pig-langsmith

PiG with [langsmith](../../components/langsmith/README.md), a Go port of LangChain's `@langchain/langsmith-pi-extension`
(MIT): traces each agent run, turn, LLM call and tool call of your sessions to LangSmith. **Nothing is sent until you set
`TRACE_TO_LANGSMITH=true` and an API key.** `/langsmith-tracing` shows the state.

```sh
export TRACE_TO_LANGSMITH=true
export LANGSMITH_PI_API_KEY="<your-langsmith-api-key>"
```

The configuration, the differences from the original and the proof of equivalence are in the
[Package README](../../components/langsmith/README.md) and its [`port/PORT.md`](../../components/langsmith/port/PORT.md).

## Run from a checkout

```sh
npm ci --ignore-scripts
npm run stage
pig piglet validate dist/staged/piglets/pig-langsmith/piglet.yaml
pig --piglet dist/staged/piglets/pig-langsmith/piglet.yaml
```

Build a Piglet Binary (needs a git checkout of the PiG source the `pig` was built from):

```sh
PIG_SOURCE_ROOT=/path/to/pig-git-checkout \
  pig piglet build dist/staged/piglets/pig-langsmith/piglet.yaml --format binary --targets <os>/<arch> --out ./pig-langsmith
```

The Binary fuses the extension (`extensionRealization: fused`). No release is published and no support is claimed.

## Credits

[components/langsmith/CREDITS.md](../../components/langsmith/CREDITS.md). The composition is MIT, Michael Kinsy.
