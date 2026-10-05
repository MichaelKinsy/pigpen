# Upstream report draft: Responses optional arguments and strict opt-out

Local reproduction: pi-goal-x 0.31.5 development with supported @earendil-works/pi-ai 0.84.1. Run `npm run context:provider-check`. The first three captures use real registered execution and drafting tools through the Responses stream adapter. onPayload throws before transmission; no provider credentials, private data or live goal mutations are needed.

With model compat omitted or supportsStrictMode=false, outgoing function tools omit strict while retaining the original optional fields. With supportsStrictMode=true, ordinary tools serialize strict=false and identical parameter schemas. The Responses caller defaults the capability to false; testing convertResponsesTools alone (whose capability default is true) misses this boundary.

OpenAI documentation says omitted strict attempts normalization to strict mode with possible fallback; explicit strict=false preserves best-effort optional arguments: https://developers.openai.com/api/docs/guides/function-calling . Upstream should distinguish support for an explicit non-strict opt-out from a request for constrained strict sampling and test resolved provider capabilities at the outgoing boundary. Some compatible endpoints may reject the field, so do not assume a universal default is safe.

Issue #59 reports one live OpenCode gpt-6-astra A/B pair; that result has not been independently reproduced. Its Pi 0.85.1 environment is outside this project's declared supported range. The local 0.84.1 capture demonstrates the serialization behavior, not backend normalization or model reliability. Per-model supportsStrictMode=true is a documented workaround, not a global extension rewrite.

Budget visibility/recovery and coalesced warnings are extension improvements. They do not resolve the upstream outgoing-contract concern or warrant claiming it fixed.
