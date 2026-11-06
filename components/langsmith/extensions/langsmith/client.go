package langsmith

// Port of client.js Client for the calls RunTree makes: createRun, updateRun, awaitPendingTraceBatches, with the
// batching queue, server-info probe, multipart / batch / direct ingestion, retries, gzip, redaction, sampling,
// runtime and environment metadata, headers and authentication (langsmith 0.10.5). OWNER: client lane.

import (
	"context"
	"net/http"
)

// clientOptions is `new Client({apiKey, apiUrl, ...})`. nil pointers are `undefined` (the SDK then reads
// LANGSMITH_API_KEY, LANGSMITH_ENDPOINT, the CLI profile, ...).
type clientOptions struct {
	APIKey *string
	APIURL *string
	// Test seams (upstream's mockClient passes autoBatchTracing: false and fetchImplementation). The extension
	// never sets them.
	AutoBatchTracing *bool
	HTTPClient       *http.Client
}

// destOptions is the per-call {apiKey, apiUrl, workspaceId} a replica supplies.
type destOptions struct {
	APIKey      *string
	APIURL      *string
	WorkspaceID *string
}

type client struct{}

// newClient is `new Client(opts)`; it fails where the constructor throws (sampling rate outside [0,1],
// invalid trace batch concurrency, ...) and refuses the unported modes (OTEL tracing) loudly.
func newClient(o clientOptions) (*client, error) { panic("unported: client.go newClient") }

// createRun is Client.createRun(run, options). run is the `_convertToCreate` / `_remapForProject` result.
func (c *client) createRun(ctx context.Context, run *omap, opts *destOptions) error {
	panic("unported: client.go createRun")
}

// updateRun is Client.updateRun(runId, run, options).
func (c *client) updateRun(ctx context.Context, runID string, run *omap, opts *destOptions) error {
	panic("unported: client.go updateRun")
}

// awaitPendingTraceBatches is Client.awaitPendingTraceBatches(): it returns when every queued run operation has
// been sent (or dropped), or ctx ends.
func (c *client) awaitPendingTraceBatches(ctx context.Context) error {
	panic("unported: client.go awaitPendingTraceBatches")
}

// close stops the client's timers and goroutines after pending work was awaited.
func (c *client) close() { panic("unported: client.go close") }
