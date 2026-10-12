# Equivalence of the ext10 bench Piglets

`node scripts/bench-equivalence.mjs --pig <pig> --exts <dir of bench-ext-NN.ts> --binary go=<bench-ext10> --binary aot=<bench-ext10-aot> --binary split=<bench-ext10-split>` runs one scripted session per arm through `--mode rpc` and compares the hash of each section with the reference arm, Stock PiG running PiG's ten TypeScript originals with `-e` (they load unchanged in Pi). Run 2026-10-12 with a pig built from PiG `team/imladris/piglet-single-file-lock` (integrate-042 `a220c94a53` plus the single-file lock fix), linux/amd64, `PIG_TS_RUNTIME=node`:

| section | originals | bench-ext10 (Go) | bench-ext10-aot | bench-ext10-split |
|---|---|---|---|---|
| commands | ea85edf8464e6ef9 | ea85edf8464e6ef9 | ea85edf8464e6ef9 | ea85edf8464e6ef9 |
| calls | f81c68435002fe1a | f81c68435002fe1a | f81c68435002fe1a | f81c68435002fe1a |
| first_request_tools | 9ad6e860a55ef908 | 9ad6e860a55ef908 | 9ad6e860a55ef908 | 9ad6e860a55ef908 |
| tool_messages_to_the_model | 46f43d60b1463ef1 | 46f43d60b1463ef1 | 46f43d60b1463ef1 | 46f43d60b1463ef1 |

Sections: the `bench-` commands listed, every `bench_tool_NN` call's result and details, each `/bench-NN` command's response, the tools the first model request declares, and the tool messages sent back to the model. The calls include a second round after every handler has run, so the cumulative event counts match too. The first run found a real fault: the three Piglets listed each extension with `tools: []`, which allows none of its tools (0 of 10 declared); `scripts/bench-pins.test.mjs` now fails on it.
