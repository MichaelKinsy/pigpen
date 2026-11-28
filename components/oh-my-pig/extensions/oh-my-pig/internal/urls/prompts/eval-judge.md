```
completion(prompt, *, model?="default"|"smol"|"slow"|"m<N>", system?=None, schema?=None) → handle
    Returns at once; `.wait()` returns text (parsed with `schema`). Stateless, no tools/history.
    `model` is a tier, or a user-tagged model agent name (`m<N>`) to run that pinned model.
    JS: ONE trailing object — `h = await completion(prompt, { model, system, schema })`; without `await`, `.id` is undefined. `.wait()` returns a Promise.
await judge(state, questions, *, images?=[path | ImageContent, …]) → `{id: answer}`
    Typed judgment over one `state` (non-empty str | JSON object | JSON array).
    - Every question sees the same state and is answered independently: batch independent questions into one call.
    - Cheap and fast (TypeSafe when credentialed, else the tiny/smol chat model); prefer over `completion()` for classification, yes/no, ranking.
    - Two or more states → `judge_batch`, never a loop of `judge()`.
    `questions`: `{id: q}` where q is one of
      `{type: "choice", instructions, criteria: {label: rubric | None, …}}` → `{choice, probabilities: {label: p}, confidence}` (≥2 labels)
      `{type: "bool", instructions, criteria?: {true?: str, false?: str}}` → `{bool: P(yes)}`
      `{type: "score", instructions, criteria: [lowest, …, highest]}` → `{score, probabilities: {"0": p, …}, confidence}`
        (≥2 levels; score is the probability-weighted level index)
    JS: ONE trailing object — `await judge(state, questions, { images })`.
judge_batch(states, questions, *, concurrency?=32, retries?=1, min_ok?=1, intent?=None, images?={key: [path | ImageContent, …]}) → JudgmentBatch
    Images accept local paths or embedded `ImageContent`; batch attachments map each state key to its list (array inputs use zero-based index keys).
    Image requests skip candidates without image support, never silently drop images, and fail if no compatible judge remains.
    Same `questions` over every state (`{key: state}` or a list keyed by index), run and owned by the host — it outlives the cell.
    - `concurrency` is capped at 32.
    - `intent` is an optional nonempty progress/job label (default `"Judging"`).
    Returns at once; pull settled items in bounded slices across cells:
    - `await b.drain(timeout?)` (seconds; omitted waits for the next item) → `[(key, item)]` settled since the last drain
      (`[]` on timeout or once the run is done; `item.ok` → `item.answers`, else `item.error`; item failures never raise)
    - `async for k, item in b.drain_iter(timeout)` until timeout or completion
    - `b.status()` → `{id, intent, done, total, failed, cost, running, model?, elapsedS, error?}`
    - `b.results()` → `{key: answers}` so far
    - `b.failed()` → `{key: error}`
    - `b.cancel()`
    - `b.close()` cancels it if running and releases it.
    `drain()` raises only when the run died wholesale (no judge, fewer than `min_ok` answered, or cancelled).
    `b.id` is an async job id:
    - completion auto-delivers a summary
    - the `wait` tool (not kernel `wait()`) blocks when you have nothing else to do
    - `judge_batch.attach(id)` re-creates the ref after a reset.
    JS: ONE trailing object — `b = await judgeBatch(states, questions, { intent, concurrency, retries, minOk, images })`.
    - `await` every `b` method except `drainIter`, which you iterate with `for await (const [k, item] of b.drainIter({ timeout }))`.
    - `drain` yields `[key, item]` arrays.
    - `await judgeBatch.attach(id)`.
```
