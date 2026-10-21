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
