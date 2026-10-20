package websearch

import "testing"

// Named skips for every upstream test that has no Go twin: the feature is deferred to a later slice,
// is not applicable to Go, or is covered by a differently titled test. Generated from the ledger
// with `pigeq twins check`; the reasons are summarised in docs/PORT.md.

func TestDeferred_allowed_search_providers(t *testing.T) {
	const F = "allowed-search-providers"
	tskip(t, F, "Curator represents unavailable auto honestly while keeping explicit Serply immediately selectable", "the search curator UI is deferred (docs/PORT.md); this case (Curator represents unavailable auto honestly while keeping explicit Serply immediately sel) ships with the feature")
}

func TestDeferred_auth_fetch(t *testing.T) {
	const F = "auth-fetch"
	tskip(t, F, "authFetch profiles resolve named, single true, and host policy", "authenticated fetch reads browser cookies, which this port never does; auth profiles return an explicit 'not available' error (docs/PORT.md); this case (authFetch profiles resolve named, single true, and host policy) ships with the feature")
	tskip(t, F, "authFetch true selects the only profile", "authenticated fetch reads browser cookies, which this port never does; auth profiles return an explicit 'not available' error (docs/PORT.md); this case (authFetch true selects the only profile) ships with the feature")
	tskip(t, F, "authenticated fetch sends only browser-scoped cookies with raw values", "authenticated fetch reads browser cookies, which this port never does; auth profiles return an explicit 'not available' error (docs/PORT.md); this case (authenticated fetch sends only browser-scoped cookies with raw values) ships with the feature")
	tskip(t, F, "authenticated same-origin redirects recompute path-scoped cookies", "authenticated fetch reads browser cookies, which this port never does; auth profiles return an explicit 'not available' error (docs/PORT.md); this case (authenticated same-origin redirects recompute path-scoped cookies) ships with the feature")
	tskip(t, F, "authenticated fetch failure does not fall through to hosted providers", "authenticated fetch reads browser cookies, which this port never does; auth profiles return an explicit 'not available' error (docs/PORT.md); this case (authenticated fetch failure does not fall through to hosted providers) ships with the feature")
	tskip(t, F, "authenticated Cloudflare challenge stays direct-only", "authenticated fetch reads browser cookies, which this port never does; auth profiles return an explicit 'not available' error (docs/PORT.md); this case (authenticated Cloudflare challenge stays direct-only) ships with the feature")
}

func TestDeferred_auto_summary_source(t *testing.T) {
	const F = "auto-summary-source"
	tskip(t, F, "web_search accepts auto-summary workflow in schema and config resolution", "the auto-summary workflow is deferred with the curator (docs/PORT.md); web_search reports workflow != none as not available; this case (web_search accepts auto-summary workflow in schema and config resolution) ships with the feature")
	tskip(t, F, "auto-summary skips curator and reuses summary model fallback plumbing", "the search curator UI is deferred (docs/PORT.md); this case (auto-summary skips curator and reuses summary model fallback plumbing) ships with the feature")
	tskip(t, F, "README documents auto-summary", "the auto-summary workflow is deferred with the curator (docs/PORT.md); web_search reports workflow != none as not available; this case (README documents auto-summary) ships with the feature")
}

func TestDeferred_brightdata_serp(t *testing.T) {
	const F = "brightdata-serp"
	tskip(t, F, "curator page exposes Bright Data as a manual provider", "the search curator UI is deferred (docs/PORT.md); this case (curator page exposes Bright Data as a manual provider) ships with the feature")
}

func TestDeferred_chrome_cookie_extraction(t *testing.T) {
	const F = "chrome-cookie-extraction"
	tskip(t, F, "cookie extraction remains opt-in even when a browser database exists", "browser cookie extraction is not part of the port: no browser-cookie access by default (docs/PORT.md); this case (cookie extraction remains opt-in even when a browser database exists) ships with the feature")
	tskip(t, F, "auto-discovery finds a non-default Chromium profile", "browser cookie extraction is not part of the port: no browser-cookie access by default (docs/PORT.md); this case (auto-discovery finds a non-default Chromium profile) ships with the feature")
	tskip(t, F, "KDE ${scenario.browser} decrypts v11 cookies with the KWallet safe-storage password", "browser cookie extraction is not part of the port: no browser-cookie access by default (docs/PORT.md); this case (KDE ${scenario.browser} decrypts v11 cookies with the KWallet safe-storage password) ships with the feature")
	tskip(t, F, "KDE keeps Secret Service ahead of KWallet", "browser cookie extraction is not part of the port: no browser-cookie access by default (docs/PORT.md); this case (KDE keeps Secret Service ahead of KWallet) ships with the feature")
	tskip(t, F, "KDE caches both Secret Service and KWallet passwords", "browser cookie extraction is not part of the port: no browser-cookie access by default (docs/PORT.md); this case (KDE caches both Secret Service and KWallet passwords) ships with the feature")
	tskip(t, F, "failed KWallet reads retry and successful passwords are cached", "browser cookie extraction is not part of the port: no browser-cookie access by default (docs/PORT.md); this case (failed KWallet reads retry and successful passwords are cached) ships with the feature")
	tskip(t, F, "auto-discovery finds a Brave profile on macOS", "browser cookie extraction is not part of the port: no browser-cookie access by default (docs/PORT.md); this case (auto-discovery finds a Brave profile on macOS) ships with the feature")
	tskip(t, F, "browser selection checks only the requested preset", "browser cookie extraction is not part of the port: no browser-cookie access by default (docs/PORT.md); this case (browser selection checks only the requested preset) ships with the feature")
	tskip(t, F, "Windows Chrome profiles decrypt v10 cookies with DPAPI keys", "browser cookie extraction is not part of the port: no browser-cookie access by default (docs/PORT.md); this case (Windows Chrome profiles decrypt v10 cookies with DPAPI keys) ships with the feature")
	tskip(t, F, "cookie queries keep high Chromium expiry values safe", "browser cookie extraction is not part of the port: no browser-cookie access by default (docs/PORT.md); this case (cookie queries keep high Chromium expiry values safe) ships with the feature")
	tskip(t, F, "cookie expiry filtering preserves same-second validity", "browser cookie extraction is not part of the port: no browser-cookie access by default (docs/PORT.md); this case (cookie expiry filtering preserves same-second validity) ships with the feature")
	tskip(t, F, "Windows Edge uses the USERPROFILE AppData fallback and reports unsupported v20 cookies", "browser cookie extraction is not part of the port: no browser-cookie access by default (docs/PORT.md); this case (Windows Edge uses the USERPROFILE AppData fallback and reports unsupported v20 cookies) ships with the feature")
	tskip(t, F, "required-cookie preflight avoids password invocation for unrelated profiles", "browser cookie extraction is not part of the port: no browser-cookie access by default (docs/PORT.md); this case (required-cookie preflight avoids password invocation for unrelated profiles) ships with the feature")
	tskip(t, F, "diagnostics report browser profile when password-store access fails", "browser cookie extraction is not part of the port: no browser-cookie access by default (docs/PORT.md); this case (diagnostics report browser profile when password-store access fails) ships with the feature")
	tskip(t, F, "diagnostics distinguish required cookie decryption failure", "browser cookie extraction is not part of the port: no browser-cookie access by default (docs/PORT.md); this case (diagnostics distinguish required cookie decryption failure) ships with the feature")
	tskip(t, F, "browser encryption password is cached within a process", "browser cookie extraction is not part of the port: no browser-cookie access by default (docs/PORT.md); this case (browser encryption password is cached within a process) ships with the feature")
	tskip(t, F, "failed password lookups are retried instead of cached (${targetPlatform})", "browser cookie extraction is not part of the port: no browser-cookie access by default (docs/PORT.md); this case (failed password lookups are retried instead of cached (${targetPlatform})) ships with the feature")
	tskip(t, F, "configured profile names cannot escape the browser profile root", "browser cookie extraction is not part of the port: no browser-cookie access by default (docs/PORT.md); this case (configured profile names cannot escape the browser profile root) ships with the feature")
	tskip(t, F, "explicit profile symlinks cannot escape the browser profile root", "browser cookie extraction is not part of the port: no browser-cookie access by default (docs/PORT.md); this case (explicit profile symlinks cannot escape the browser profile root) ships with the feature")
	tskip(t, F, "sqlite3 CLI fallback reads a copied database when node sqlite is unavailable", "browser cookie extraction is not part of the port: no browser-cookie access by default (docs/PORT.md); this case (sqlite3 CLI fallback reads a copied database when node sqlite is unavailable) ships with the feature")
	tskip(t, F, "unavailable SQLite backends produce actionable sanitized diagnostics", "browser cookie extraction is not part of the port: no browser-cookie access by default (docs/PORT.md); this case (unavailable SQLite backends produce actionable sanitized diagnostics) ships with the feature")
}

func TestDeferred_config_path(t *testing.T) {
	const F = "config-path"
}

func TestDeferred_curator_fallback(t *testing.T) {
	const F = "curator-fallback"
	tskip(t, F, "web_search curator auto-open failures keep the curator alive with a manual URL", "the search curator UI is deferred (docs/PORT.md); this case (web_search curator auto-open failures keep the curator alive with a manual URL) ships with the feature")
	tskip(t, F, "curator add-search receives the active extension context", "the search curator UI is deferred (docs/PORT.md); this case (curator add-search receives the active extension context) ships with the feature")
	tskip(t, F, "curator fallback helper is visible to the browser-open catch block", "the search curator UI is deferred (docs/PORT.md); this case (curator fallback helper is visible to the browser-open catch block) ships with the feature")
	tskip(t, F, "cancel diagnostics include curator URL and browser-open error", "the search curator UI is deferred (docs/PORT.md); this case (cancel diagnostics include curator URL and browser-open error) ships with the feature")
	tskip(t, F, "manual websearch command reports browser-open fallback without closing curator", "the search curator UI is deferred (docs/PORT.md); this case (manual websearch command reports browser-open fallback without closing curator) ships with the feature")
	tskip(t, F, "remote curator mode prints the manual URL unless auto-open is explicit", "the search curator UI is deferred (docs/PORT.md); this case (remote curator mode prints the manual URL unless auto-open is explicit) ships with the feature")
	tskip(t, F, "Linux curator browser launch detaches xdg-open but keeps immediate failures", "the search curator UI is deferred (docs/PORT.md); this case (Linux curator browser launch detaches xdg-open but keeps immediate failures) ships with the feature")
	tskip(t, F, "README documents manual browser fallback", "the search curator UI is deferred (docs/PORT.md); this case (README documents manual browser fallback) ships with the feature")
}

func TestDeferred_curator_run(t *testing.T) {
	const F = "curator-run"
	tskip(t, F, "fresh installs default to no curator while configured and explicit workflows keep their meanings", "the search curator UI is deferred (docs/PORT.md); this case (fresh installs default to no curator while configured and explicit workflows keep their me) ships with the feature")
	tskip(t, F, "approval changes only inherited summary-review workflows for the current prompt", "the search curator UI is deferred (docs/PORT.md); this case (approval changes only inherited summary-review workflows for the current prompt) ships with the feature")
	tskip(t, F, "registered lifecycle preserves approval across internal turns and resets at run boundaries", "the search curator UI is deferred (docs/PORT.md); this case (registered lifecycle preserves approval across internal turns and resets at run boundaries) ships with the feature")
}

func TestDeferred_curator_server_timeout(t *testing.T) {
	const F = "curator-server-timeout"
	tskip(t, F, "curator times out when searches finish but no browser connects", "the search curator UI is deferred (docs/PORT.md); this case (curator times out when searches finish but no browser connects) ships with the feature")
	tskip(t, F, "curator replays search events after SSE reconnect", "the search curator UI is deferred (docs/PORT.md); this case (curator replays search events after SSE reconnect) ships with the feature")
	tskip(t, F, "curator add-search indexes do not collide with reserved initial provider results", "the search curator UI is deferred (docs/PORT.md); this case (curator add-search indexes do not collide with reserved initial provider results) ships with the feature")
	tskip(t, F, "curator submit rejects contradictory summary metadata", "the search curator UI is deferred (docs/PORT.md); this case (curator submit rejects contradictory summary metadata) ships with the feature")
	tskip(t, F, "curator assigns one selectable result index per provider", "the search curator UI is deferred (docs/PORT.md); this case (curator assigns one selectable result index per provider) ships with the feature")
	tskip(t, F, "curator heartbeat timeout finalizes connected idle browser sessions", "the search curator UI is deferred (docs/PORT.md); this case (curator heartbeat timeout finalizes connected idle browser sessions) ships with the feature")
	tskip(t, F, "curator state replay keeps current results and compacts superseded history", "the search curator UI is deferred (docs/PORT.md); this case (curator state replay keeps current results and compacts superseded history) ships with the feature")
	tskip(t, F, "curator state replay keeps all-provider entries that share one slot", "the search curator UI is deferred (docs/PORT.md); this case (curator state replay keeps all-provider entries that share one slot) ships with the feature")
	tskip(t, F, "curator exposes and signals approve remaining searches for this prompt", "the search curator UI is deferred (docs/PORT.md); this case (curator exposes and signals approve remaining searches for this prompt) ships with the feature")
}

func TestDeferred_gemini_web_cookie_opt_in(t *testing.T) {
	const F = "gemini-web-cookie-opt-in"
	tskip(t, F, "Gemini Web never forwards browser cookies across origins", "Gemini is a later slice (docs/PORT.md); this case (Gemini Web never forwards browser cookies across origins) ships with the feature")
	tskip(t, F, "Gemini Web generation rejects automatic redirects", "Gemini is a later slice (docs/PORT.md); this case (Gemini Web generation rejects automatic redirects) ships with the feature")
	tskip(t, F, "Gemini Web rejects unsupported models instead of falling back to 2.5 Flash", "Gemini is a later slice (docs/PORT.md); this case (Gemini Web rejects unsupported models instead of falling back to 2.5 Flash) ships with the feature")
	tskip(t, F, "Gemini Web file uploads read the file and reject automatic redirects", "Gemini is a later slice (docs/PORT.md); this case (Gemini Web file uploads read the file and reject automatic redirects) ships with the feature")
	tskip(t, F, "browser cookie access is disabled unless explicitly allowed", "Gemini Web needs browser cookies, which this port never reads (docs/PORT.md); this case (browser cookie access is disabled unless explicitly allowed) ships with the feature")
	tskip(t, F, "disabled browser cookie access does not validate legacy profile selection", "Gemini Web needs browser cookies, which this port never reads (docs/PORT.md); this case (disabled browser cookie access does not validate legacy profile selection) ships with the feature")
	tskip(t, F, "browser cookie config validates presets and rejects arbitrary profile paths", "Gemini Web needs browser cookies, which this port never reads (docs/PORT.md); this case (browser cookie config validates presets and rejects arbitrary profile paths) ships with the feature")
}

func TestDeferred_gemini_web_header_overflow(t *testing.T) {
	const F = "gemini-web-header-overflow"
	tskip(t, F, "createGeminiFetch wires a dedicated agent with a >16 KiB header budget", "Gemini is a later slice (docs/PORT.md); this case (createGeminiFetch wires a dedicated agent with a >16 KiB header budget) ships with the feature")
	tskip(t, F, "dedicated agent fetches oversized-header responses the pi-style global dispatcher rejects", "Gemini Web needs browser cookies, which this port never reads (docs/PORT.md); this case (dedicated agent fetches oversized-header responses the pi-style global dispatcher rejects) ships with the feature")
	tskip(t, F, "resolveGeminiFetch falls back to the ambient global fetch when undici is not resolvable", "Gemini is a later slice (docs/PORT.md); this case (resolveGeminiFetch falls back to the ambient global fetch when undici is not resolvable) ships with the feature")
	tskip(t, F, "resolveGeminiFetch surfaces dedicated agent construction errors", "Gemini is a later slice (docs/PORT.md); this case (resolveGeminiFetch surfaces dedicated agent construction errors) ships with the feature")
}

func TestDeferred_github_extract(t *testing.T) {
	const F = "github-extract"
	tskip(t, F, "parseGitHubUrl rejects malformed identifiers and preserves normal GitHub paths", "GitHub repository cloning/extraction is deferred (docs/PORT.md); this case (parseGitHubUrl rejects malformed identifiers and preserves normal GitHub paths) ships with the feature")
	tskip(t, F, "malformed GitHub identifiers cannot delete outside the clone cache", "GitHub repository cloning/extraction is deferred (docs/PORT.md); this case (malformed GitHub identifiers cannot delete outside the clone cache) ships with the feature")
	tskip(t, F, "clearCloneCache removes only its runtime directory", "GitHub repository cloning/extraction is deferred (docs/PORT.md); this case (clearCloneCache removes only its runtime directory) ships with the feature")
	tskip(t, F, "clone cleanup unlinks a direct-child symlink without deleting its target", "GitHub repository cloning/extraction is deferred (docs/PORT.md); this case (clone cleanup unlinks a direct-child symlink without deleting its target) ships with the feature")
	tskip(t, F, "a failed clone cannot clean up another process's in-flight destination", "GitHub repository cloning/extraction is deferred (docs/PORT.md); this case (a failed clone cannot clean up another process's in-flight destination) ships with the feature")
	tskip(t, F, "runtime cache reuse and cleanup stay isolated across processes", "GitHub repository cloning/extraction is deferred (docs/PORT.md); this case (runtime cache reuse and cleanup stay isolated across processes) ships with the feature")
	tskip(t, F, "clone runtime creation proceeds while background cleanup is gated", "GitHub repository cloning/extraction is deferred (docs/PORT.md); this case (clone runtime creation proceeds while background cleanup is gated) ships with the feature")
	tskip(t, F, "background cleanup removes dead runtimes after unrelated files and ownerless directories", "GitHub repository cloning/extraction is deferred (docs/PORT.md); this case (background cleanup removes dead runtimes after unrelated files and ownerless directories) ships with the feature")
	tskip(t, F, "oversized malformed owner metadata is preserved during extraction", "GitHub repository cloning/extraction is deferred (docs/PORT.md); this case (oversized malformed owner metadata is preserved during extraction) ships with the feature")
	tskip(t, F, "clone runtime initialization preserves live, unknown, and symlink runtimes", "GitHub repository cloning/extraction is deferred (docs/PORT.md); this case (clone runtime initialization preserves live, unknown, and symlink runtimes) ships with the feature")
	tskip(t, F, "normalizeClonePath expands ~ to HOME", "GitHub repository cloning/extraction is deferred (docs/PORT.md); this case (normalizeClonePath expands ~ to HOME) ships with the feature")
	tskip(t, F, "normalizeClonePath expands $HOME and other env vars", "GitHub repository cloning/extraction is deferred (docs/PORT.md); this case (normalizeClonePath expands $HOME and other env vars) ships with the feature")
	tskip(t, F, "normalizeClonePath handles absolute paths without expansion", "GitHub repository cloning/extraction is deferred (docs/PORT.md); this case (normalizeClonePath handles absolute paths without expansion) ships with the feature")
	tskip(t, F, "GitHub clones disable interactive credential prompts", "GitHub repository cloning/extraction is deferred (docs/PORT.md); this case (GitHub clones disable interactive credential prompts) ships with the feature")
	tskip(t, F, "GitHub clone timeout force-kills the SIGTERM-resistant process group", "GitHub repository cloning/extraction is deferred (docs/PORT.md); this case (GitHub clone timeout force-kills the SIGTERM-resistant process group) ships with the feature")
	tskip(t, F, "githubClone.enabled false skips GitHub clone/API specialization", "GitHub repository cloning/extraction is deferred (docs/PORT.md); this case (githubClone.enabled false skips GitHub clone/API specialization) ships with the feature")
}

func TestDeferred_github_issue_pr(t *testing.T) {
	const F = "github-issue-pr"
	tskip(t, F, "parseGitHubIssuePrUrl accepts PR and issue variants without changing repo parsing", "GitHub issue/PR extraction is deferred (docs/PORT.md); this case (parseGitHubIssuePrUrl accepts PR and issue variants without changing repo parsing) ships with the feature")
	tskip(t, F, "renderer orders sections, marks truncation, and forces anchored comments inline", "GitHub issue/PR extraction is deferred (docs/PORT.md); this case (renderer orders sections, marks truncation, and forces anchored comments inline) ships with the feature")
	tskip(t, F, "renderer marks truncated review verdicts", "GitHub issue/PR extraction is deferred (docs/PORT.md); this case (renderer marks truncated review verdicts) ships with the feature")
	tskip(t, F, "extractGitHubIssuePr retries gh view with core fields on unknown --json field", "GitHub issue/PR extraction is deferred (docs/PORT.md); this case (extractGitHubIssuePr retries gh view with core fields on unknown --json field) ships with the feature")
	tskip(t, F, "gh path fetches an anchored review comment beyond fetched pages", "GitHub issue/PR extraction is deferred (docs/PORT.md); this case (gh path fetches an anchored review comment beyond fetched pages) ships with the feature")
	tskip(t, F, "gh path rejects anchored review comments from another pull request", "GitHub issue/PR extraction is deferred (docs/PORT.md); this case (gh path rejects anchored review comments from another pull request) ships with the feature")
	tskip(t, F, "gh path still attempts an anchored review comment after page failure", "GitHub issue/PR extraction is deferred (docs/PORT.md); this case (gh path still attempts an anchored review comment after page failure) ships with the feature")
	tskip(t, F, "gh review thread page failure renders unavailable instead of none", "GitHub issue/PR extraction is deferred (docs/PORT.md); this case (gh review thread page failure renders unavailable instead of none) ships with the feature")
	tskip(t, F, "extractGitHubIssuePr aborts active gh work with the caller signal", "GitHub issue/PR extraction is deferred (docs/PORT.md); this case (extractGitHubIssuePr aborts active gh work with the caller signal) ships with the feature")
	tskip(t, F, "extractGitHubIssuePr aborts a hung gh availability probe", "GitHub issue/PR extraction is deferred (docs/PORT.md); this case (extractGitHubIssuePr aborts a hung gh availability probe) ships with the feature")
	tskip(t, F, "REST fallback preserves caller cancellation", "GitHub issue/PR extraction is deferred (docs/PORT.md); this case (REST fallback preserves caller cancellation) ships with the feature")
	tskip(t, F, "extractContent returns an actionable GitHub rate-limit result instead of HTML fall-through", "GitHub issue/PR extraction is deferred (docs/PORT.md); this case (extractContent returns an actionable GitHub rate-limit result instead of HTML fall-through) ships with the feature")
	tskip(t, F, "REST fallback fetches an anchored issue comment outside the first page", "GitHub issue/PR extraction is deferred (docs/PORT.md); this case (REST fallback fetches an anchored issue comment outside the first page) ships with the feature")
	tskip(t, F, "REST fallback rejects anchored issue comments from another work item", "GitHub issue/PR extraction is deferred (docs/PORT.md); this case (REST fallback rejects anchored issue comments from another work item) ships with the feature")
	tskip(t, F, "REST fallback fetches an anchored PR review comment outside the first page", "GitHub issue/PR extraction is deferred (docs/PORT.md); this case (REST fallback fetches an anchored PR review comment outside the first page) ships with the feature")
	tskip(t, F, "REST fallback does not render positive changedFiles as none when files are missing", "GitHub issue/PR extraction is deferred (docs/PORT.md); this case (REST fallback does not render positive changedFiles as none when files are missing) ships with the feature")
}

func TestDeferred_jina_search(t *testing.T) {
	const F = "jina-search"
	tskip(t, F, "curator page exposes Jina Search as a manual provider", "the search curator UI is deferred (docs/PORT.md); this case (curator page exposes Jina Search as a manual provider) ships with the feature")
}

func TestDeferred_local_video_oversize(t *testing.T) {
	const F = "local-video-oversize"
	tskip(t, F, "oversized local videos are still recognized for frame extraction", "local video and Gemini/YouTube frame extraction are deferred (docs/PORT.md); this case (oversized local videos are still recognized for frame extraction) ships with the feature")
	tskip(t, F, "oversized local videos are still recognized for timestamp extraction", "local video and Gemini/YouTube frame extraction are deferred (docs/PORT.md); this case (oversized local videos are still recognized for timestamp extraction) ships with the feature")
	tskip(t, F, "oversized local video analysis reports the upload size limit", "local video and Gemini/YouTube frame extraction are deferred (docs/PORT.md); this case (oversized local video analysis reports the upload size limit) ships with the feature")
}

func TestDeferred_new_search_providers(t *testing.T) {
	const F = "new-search-providers"
	tskip(t, F, "Curator page exposes Bocha as a manual provider", "the search curator UI is deferred (docs/PORT.md); this case (Curator page exposes Bocha as a manual provider) ships with the feature")
}

func TestDeferred_package_typebox_dependency(t *testing.T) {
	const F = "package-typebox-dependency"
	tskip(t, F, "packed installs keep typebox as a peer dependency (hosted by pi at runtime)", "npm packaging concern (typebox peer dependency); the Go port has no npm dependencies; this case (packed installs keep typebox as a peer dependency (hosted by pi at runtime)) ships with the feature")
}

func TestDeferred_parallel_curator_state(t *testing.T) {
	const F = "parallel-curator-state"
	tskip(t, F, "web_search curator state is keyed per tool call for parallel calls", "the search curator UI is deferred (docs/PORT.md); this case (web_search curator state is keyed per tool call for parallel calls) ships with the feature")
	tskip(t, F, "manual websearch curator command uses a distinct map key", "the search curator UI is deferred (docs/PORT.md); this case (manual websearch curator command uses a distinct map key) ships with the feature")
}

func TestDeferred_parallel_mcp_provider(t *testing.T) {
	const F = "parallel-mcp-provider"
	tskip(t, F, "Curator page exposes a usable Parallel MCP provider entry", "the search curator UI is deferred (docs/PORT.md); this case (Curator page exposes a usable Parallel MCP provider entry) ships with the feature")
}

func TestDeferred_publish_wrapper(t *testing.T) {
	const F = "publish-wrapper"
	tskip(t, F, "publish wrapper restores the manifest after npm succeeds", "npm publish tooling of the original; the Go port ships as a PiG Package instead; this case (publish wrapper restores the manifest after npm succeeds) ships with the feature")
	tskip(t, F, "publish wrapper restores the manifest and preserves npm failure status", "npm publish tooling of the original; the Go port ships as a PiG Package instead; this case (publish wrapper restores the manifest and preserves npm failure status) ships with the feature")
	tskip(t, F, "publish wrapper restores the manifest and exits 1 when npm cannot spawn", "npm publish tooling of the original; the Go port ships as a PiG Package instead; this case (publish wrapper restores the manifest and exits 1 when npm cannot spawn) ships with the feature")
	tskip(t, F, "publish wrapper forwards SIGINT to npm, waits, and restores", "npm publish tooling of the original; the Go port ships as a PiG Package instead; this case (publish wrapper forwards SIGINT to npm, waits, and restores) ships with the feature")
	tskip(t, F, "publish wrapper forwards SIGTERM to npm, waits, and restores", "npm publish tooling of the original; the Go port ships as a PiG Package instead; this case (publish wrapper forwards SIGTERM to npm, waits, and restores) ships with the feature")
}

func TestDeferred_querit_provider(t *testing.T) {
	const F = "querit-provider"
	tskip(t, F, "curator page exposes Querit as a manual provider", "the search curator UI is deferred (docs/PORT.md); this case (curator page exposes Querit as a manual provider) ships with the feature")
}

func TestDeferred_rsc_fallback(t *testing.T) {
	const F = "rsc-fallback"
	tskip(t, F, "weak Readability output falls back to useful RSC content", "React Server Components payload extraction is not ported: the fallback yields nothing (docs/PORT.md); this case (weak Readability output falls back to useful RSC content) ships with the feature")
	tskip(t, F, "null Readability output falls back to local Defuddle extraction", "React Server Components payload extraction is not ported: the fallback yields nothing (docs/PORT.md); this case (null Readability output falls back to local Defuddle extraction) ships with the feature")
	tskip(t, F, "Defuddle fallback resolves relative canonical URLs against the fetched URL", "React Server Components payload extraction is not ported: the fallback yields nothing (docs/PORT.md); this case (Defuddle fallback resolves relative canonical URLs against the fetched URL) ships with the feature")
	tskip(t, F, "short Readability output falls back to useful Defuddle content", "React Server Components payload extraction is not ported: the fallback yields nothing (docs/PORT.md); this case (short Readability output falls back to useful Defuddle content) ships with the feature")
	tskip(t, F, "Defuddle processing failures stay contained and do not return the fallback body", "React Server Components payload extraction is not ported: the fallback yields nothing (docs/PORT.md); this case (Defuddle processing failures stay contained and do not return the fallback body) ships with the feature")
	tskip(t, F, "short non-RSC pages remain incomplete", "React Server Components payload extraction is not ported: the fallback yields nothing (docs/PORT.md); this case (short non-RSC pages remain incomplete) ships with the feature")
	tskip(t, F, "short RSC payloads remain incomplete", "React Server Components payload extraction is not ported: the fallback yields nothing (docs/PORT.md); this case (short RSC payloads remain incomplete) ships with the feature")
	tskip(t, F, "background fetch notification distinguishes full, partial, and failed content", "React Server Components payload extraction is not ported: the fallback yields nothing (docs/PORT.md); this case (background fetch notification distinguishes full, partial, and failed content) ships with the feature")
}

func TestDeferred_search_error_render(t *testing.T) {
	const F = "search-error-render"
	tskip(t, F, "cancel/error path is NOT a dead-end single line: expanded plan has >1 line", "TUI rendering of search errors is left to the host's default rendering (docs/PORT.md); this case (cancel/error path is NOT a dead-end single line: expanded plan has >1 line) ships with the feature")
	tskip(t, F, "expanded plan surfaces the diagnostics that were previously discarded", "TUI rendering of search errors is left to the host's default rendering (docs/PORT.md); this case (expanded plan surfaces the diagnostics that were previously discarded) ships with the feature")
	tskip(t, F, "collapsed view is a short summary WITH a ctrl+o expand hint (Ctrl+O now does something)", "TUI rendering of search errors is left to the host's default rendering (docs/PORT.md); this case (collapsed view is a short summary WITH a ctrl+o expand hint (Ctrl+O now does something)) ships with the feature")
	tskip(t, F, "plain (non-cancel) error stays a clean single line — no diagnostic noise", "TUI rendering of search errors is left to the host's default rendering (docs/PORT.md); this case (plain (non-cancel) error stays a clean single line — no diagnostic noise) ships with the feature")
	tskip(t, F, "fetch_content-style error (extras, no cancel) is expandable without browser diagnostics", "TUI rendering of search errors is left to the host's default rendering (docs/PORT.md); this case (fetch_content-style error (extras, no cancel) is expandable without browser diagnostics) ships with the feature")
	tskip(t, F, "non-search error with extras shows detail without browser diagnostics", "TUI rendering of search errors is left to the host's default rendering (docs/PORT.md); this case (non-search error with extras shows detail without browser diagnostics) ships with the feature")
	tskip(t, F, "error with NO extras and NO cancel stays single-line", "TUI rendering of search errors is left to the host's default rendering (docs/PORT.md); this case (error with NO extras and NO cancel stays single-line) ships with the feature")
	tskip(t, F, "non-error result yields null so the caller falls through to the success renderer", "TUI rendering of search errors is left to the host's default rendering (docs/PORT.md); this case (non-error result yields null so the caller falls through to the success renderer) ships with the feature")
	tskip(t, F, "index.ts imports buildSearchErrorPlan and wires it into the web_search error path", "TUI rendering of search errors is left to the host's default rendering (docs/PORT.md); this case (index.ts imports buildSearchErrorPlan and wires it into the web_search error path) ships with the feature")
}

func TestDeferred_search_providers(t *testing.T) {
	const F = "search-providers"
	tskip(t, F, "OpenAI search requires web_search and maps domain filters", "OpenAI/Codex hosted search is deferred (docs/PORT.md); this case (OpenAI search requires web_search and maps domain filters) ships with the feature")
	tskip(t, F, "OpenAI search uses configured Responses endpoint", "OpenAI/Codex hosted search is deferred (docs/PORT.md); this case (OpenAI search uses configured Responses endpoint) ships with the feature")
	tskip(t, F, "curator auto default follows the active model provider", "the search curator UI is deferred (docs/PORT.md); this case (curator auto default follows the active model provider) ships with the feature")
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
	tskip(t, F, "Curator auto default follows the same current-model Hosted Search eligibility", "OpenAI/Codex hosted search is deferred (docs/PORT.md); this case (Curator auto default follows the same current-model Hosted Search eligibility) ships with the feature")
}

func TestDeferred_search1api(t *testing.T) {
	const F = "search1api"
	tskip(t, F, "curator page exposes Search1API as a manual provider", "the search curator UI is deferred (docs/PORT.md); this case (curator page exposes Search1API as a manual provider) ships with the feature")
}

func TestDeferred_searchinfinity(t *testing.T) {
	const F = "searchinfinity"
	tskip(t, F, "curator page exposes Searchinfinity as a manual provider", "the search curator UI is deferred (docs/PORT.md); this case (curator page exposes Searchinfinity as a manual provider) ships with the feature")
}

func TestDeferred_tinyfish(t *testing.T) {
	const F = "tinyfish"
	tskip(t, F, "curator page exposes TinyFish as a manual provider", "the search curator UI is deferred (docs/PORT.md); this case (curator page exposes TinyFish as a manual provider) ships with the feature")
}

func TestDeferred_valyu_serper_provider(t *testing.T) {
	const F = "valyu-serper-provider"
	tskip(t, F, "Curator page exposes usable Serper and Valyu provider entries", "the search curator UI is deferred (docs/PORT.md); this case (Curator page exposes usable Serper and Valyu provider entries) ships with the feature")
}

func TestDeferred_youtube_extract_errors(t *testing.T) {
	const F = "youtube-extract-errors"
	tskip(t, F, "YouTube extraction surfaces Gemini API errors", "Gemini is a later slice (docs/PORT.md); this case (YouTube extraction surfaces Gemini API errors) ships with the feature")
}
