# Credits

This Package is a Go port of two MIT-licensed projects by TypeSafe AI, and its cross-check uses a third (Apache-2.0). It keeps their
copyright notices and licenses (in `port/oracle/<name>/LICENSE`).

## typesafe-sdk-js

`libraries/typesafe` ports **@typesafe-ai/sdk** 0.6.0, the official TypeScript SDK for the
TypeSafe API: https://github.com/typesafe-ai/typesafe-sdk-js, by **TypeSafe**
(package author **evinism**), MIT, Copyright (c) 2026 TypeSafe.

- Pinned commit: `66880ccded6cb642dc1809620c2b108c33730214` (tag/version 0.6.0)
- License: [`port/oracle/typesafe-sdk-js/LICENSE`](port/oracle/typesafe-sdk-js/LICENSE)

## system-one-adapter-python

`libraries/ownmodel` ports **system-one-adapter** 0.2.1, the drop-in `system_one` backend
that answers TypeSafe questions with an LLM: https://github.com/typesafe-ai/system-one-adapter-python,
by **TypeSafe AI** (maintainers **Erik Gafni** and **Daniel Gafni**), MIT, Copyright (c) 2026 TypeSafe AI.

- Pinned commit: `e1d4cc938204b22fc5a3c3aca7044072fe3f712d`
- License: [`port/oracle/system-one-adapter-python/LICENSE`](port/oracle/system-one-adapter-python/LICENSE)

## WorkflowEvals (cross-check only)

https://github.com/typesafe-ai/WorkflowEvals (Apache-2.0, TypeSafe AI; the commits are by Sam) is used only to check the
Go client against, at commit `0ac3b8ad845429f0d8e064ecfb2430a47c5a25cb` (Apache-2.0, license in
[`port/oracle/WorkflowEvals/LICENSE`](port/oracle/WorkflowEvals/LICENSE)). The question sets its workflows
build (`port/crosscheck/workflowevals.json`) and the extraction script that records them are derived from
its code; no dataset content is stored, and the states in that file are synthetic. No other source of it is
copied into this Package.

## What is modified

The Go code, tests and documents were written for this Package by Michael Kinsy; the behavior,
error texts, defaults, prompts and test cases follow the originals. `port/PORT.md` maps every
upstream file to its Go counterpart and lists each deliberate difference.
