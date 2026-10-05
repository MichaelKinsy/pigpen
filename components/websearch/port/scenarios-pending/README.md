# Scenarios that cannot be compared yet

These scenarios run the original and the port against fake HTTP upstreams, but the harness cannot
compare them:

- `fetch-*` scenarios need the fake server's URL inside the model's tool-call arguments;
  `{{server:NAME}}` is only expanded in `env`, `files`, `args` and `agentFiles`.
- Every tool result carries a random `responseId` (in the text of `web_search` and in `details`), so
  two runs of the same original differ. The harness has no normalisation for it.

They are kept as the starting point for when the harness (`components/extension-equivalence`)
supports both. The behaviours they cover are pinned by the Go
tests instead (`fetch`, `web_search` and `get_search_content` twins).
