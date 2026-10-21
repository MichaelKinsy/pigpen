package websearch

import "testing"

// Named skips for every upstream test that has no Go twin: the feature is deferred to a later slice,
// is not applicable to Go, or is covered by a differently titled test. Generated from the ledger
// with `pigeq twins check`; the reasons are summarised in docs/PORT.md.

func TestDeferred_package_typebox_dependency(t *testing.T) {
	const F = "package-typebox-dependency"
	tskip(t, F, "packed installs keep typebox as a peer dependency (hosted by pi at runtime)", "npm packaging concern (typebox peer dependency); the Go port has no npm dependencies; this case (packed installs keep typebox as a peer dependency (hosted by pi at runtime)) ships with the feature")
}

func TestDeferred_publish_wrapper(t *testing.T) {
	const F = "publish-wrapper"
	tskip(t, F, "publish wrapper restores the manifest after npm succeeds", "npm publish tooling of the original; the Go port ships as a PiG Package instead; this case (publish wrapper restores the manifest after npm succeeds) ships with the feature")
	tskip(t, F, "publish wrapper restores the manifest and preserves npm failure status", "npm publish tooling of the original; the Go port ships as a PiG Package instead; this case (publish wrapper restores the manifest and preserves npm failure status) ships with the feature")
	tskip(t, F, "publish wrapper restores the manifest and exits 1 when npm cannot spawn", "npm publish tooling of the original; the Go port ships as a PiG Package instead; this case (publish wrapper restores the manifest and exits 1 when npm cannot spawn) ships with the feature")
	tskip(t, F, "publish wrapper forwards SIGINT to npm, waits, and restores", "npm publish tooling of the original; the Go port ships as a PiG Package instead; this case (publish wrapper forwards SIGINT to npm, waits, and restores) ships with the feature")
	tskip(t, F, "publish wrapper forwards SIGTERM to npm, waits, and restores", "npm publish tooling of the original; the Go port ships as a PiG Package instead; this case (publish wrapper forwards SIGTERM to npm, waits, and restores) ships with the feature")
}

func TestDeferred_search_providers(t *testing.T) {
	const F = "search-providers"
	tskip(t, F, "OpenAI search requires web_search and maps domain filters", "OpenAI/Codex hosted search is deferred (docs/PORT.md); this case (OpenAI search requires web_search and maps domain filters) ships with the feature")
	tskip(t, F, "OpenAI search uses configured Responses endpoint", "OpenAI/Codex hosted search is deferred (docs/PORT.md); this case (OpenAI search uses configured Responses endpoint) ships with the feature")
	tskip(t, F, "auto search prefers Codex-backed OpenAI search when the selected model is openai-codex", "OpenAI/Codex hosted search is deferred (docs/PORT.md); this case (auto search prefers Codex-backed OpenAI search when the selected model is openai-codex) ships with the feature")
	tskip(t, F, "auto search keeps selected Codex-backed OpenAI for result counts and recency filters", "OpenAI/Codex hosted search is deferred (docs/PORT.md); this case (auto search keeps selected Codex-backed OpenAI for result counts and recency filters) ships with the feature")
	tskip(t, F, "auto search falls through to Exa when selected Codex-backed OpenAI fails", "OpenAI/Codex hosted search is deferred (docs/PORT.md); this case (auto search falls through to Exa when selected Codex-backed OpenAI fails) ships with the feature")
	tskip(t, F, "auto search uses Exa before OpenAI when the selected model is not openai-codex", "OpenAI/Codex hosted search is deferred (docs/PORT.md); this case (auto search uses Exa before OpenAI when the selected model is not openai-codex) ships with the feature")
	tskip(t, F, "OpenAI search falls back to API key when model registry cannot enumerate", "OpenAI/Codex hosted search is deferred (docs/PORT.md); this case (OpenAI search falls back to API key when model registry cannot enumerate) ships with the feature")
	tskip(t, F, "OpenAI search uses configured model with selected registry auth", "OpenAI/Codex hosted search is deferred (docs/PORT.md); this case (OpenAI search uses configured model with selected registry auth) ships with the feature")
	tskip(t, F, "OpenAI search honors configured provider priority", "OpenAI/Codex hosted search is deferred (docs/PORT.md); this case (OpenAI search honors configured provider priority) ships with the feature")
	tskip(t, F, "OpenAI search rejects invalid openaiSearchProviders", "OpenAI/Codex hosted search is deferred (docs/PORT.md); this case (OpenAI search rejects invalid openaiSearchProviders) ships with the feature")
}

func TestDeferred_search_routing(t *testing.T) {
	const F = "search-routing"
	tskip(t, F, "configured routing falls back from an xAI 403 quota-exhausted response", "xAI is not ported in the first slice: listed in the provider table, fails with an explicit 'unsupported' error; this case (configured routing falls back from an xAI 403 quota-exhausted response) ships with the feature")
	tskip(t, F, "configured routing keeps an ordinary xAI 403 response classified as auth", "xAI is not ported in the first slice: listed in the provider table, fails with an explicit 'unsupported' error; this case (configured routing keeps an ordinary xAI 403 response classified as auth) ships with the feature")
	tskip(t, F, "current-model routing uses the official OpenAI model, auth, headers, and endpoint", "OpenAI/Codex hosted search is deferred (docs/PORT.md); this case (current-model routing uses the official OpenAI model, auth, headers, and endpoint) ships with the feature")
	tskip(t, F, "non-official and non-GPT current models skip automatic Hosted Search", "OpenAI/Codex hosted search is deferred (docs/PORT.md); this case (non-official and non-GPT current models skip automatic Hosted Search) ships with the feature")
	tskip(t, F, "Codex current models use the official Codex Responses search endpoint", "OpenAI/Codex hosted search is deferred (docs/PORT.md); this case (Codex current models use the official Codex Responses search endpoint) ships with the feature")
	tskip(t, F, "explicit OpenAI provider bypasses current-model restrictions", "OpenAI/Codex hosted search is deferred (docs/PORT.md); this case (explicit OpenAI provider bypasses current-model restrictions) ships with the feature")
	tskip(t, F, "unsupported Hosted Search errors fall back to Tavily", "OpenAI/Codex hosted search is deferred (docs/PORT.md); this case (unsupported Hosted Search errors fall back to Tavily) ships with the feature")
	tskip(t, F, "Hosted authentication errors do not silently fall back", "current-model hosted search routing (OpenAI, xAI) is deferred (docs/PORT.md); this case (Hosted authentication errors do not silently fall back) ships with the feature")
	tskip(t, F, "a Hosted response without web_search_call is invalid and can fall back", "current-model hosted search routing (OpenAI, xAI) is deferred (docs/PORT.md); this case (a Hosted response without web_search_call is invalid and can fall back) ships with the feature")
	tskip(t, F, "useCurrentModel is strictly validated", "current-model hosted search routing (OpenAI, xAI) is deferred (docs/PORT.md); this case (useCurrentModel is strictly validated) ships with the feature")
}
