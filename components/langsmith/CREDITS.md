# Credits

`extensions/langsmith` is a Go port of **@langchain/langsmith-pi-extension** 0.2.1,
https://github.com/langchain-ai/langsmith-pi-extension, by **LangChain** (MIT, Copyright 2026 LangChain).

- Commit: `b0d83c3c3bc994fc7570f2bfc7b12561b8282bef` (release `@langchain/langsmith-pi-extension@0.2.1`).
- The original's sources (`src/`), tests, README, changelog, `package.json` and license are kept unmodified at
  [`port/oracle/`](port/oracle) as the equivalence oracle, with the license at
  [`port/oracle/LICENSE`](port/oracle/LICENSE). `port/oracle/dist/` is the published npm build of the same release
  (tarball sha1 `7ef996ee6eabeba41a52b2110378e0a1d0a4e321`); it is what Pi users run and what the harness executes.
- The extension sends its traces through the **LangSmith JavaScript SDK** (`langsmith` 0.10.5,
  https://github.com/langchain-ai/langsmith-sdk, by LangChain, MIT). The Go code reimplements the parts of that SDK the
  extension reaches (run trees, replicas, the batching client, multipart ingestion, sampling, redaction, profile
  authentication) so the requests on the wire match. Each Go file names the SDK source it follows.

The Go code, the scenarios and the layer-1 tests were written for this Package by Michael Kinsy. Test cases of the
original are ported as twins (`port/upstream-tests.json` lists their titles). What differs is listed in
[README.md](README.md) and [port/PORT.md](port/PORT.md).
