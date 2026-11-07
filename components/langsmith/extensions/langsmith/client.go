package langsmith

// Port of client.js Client for the calls RunTree makes: createRun, updateRun, awaitPendingTraceBatches, with the
// batching queue, server-info probe, multipart / batch / direct ingestion, retries, gzip, redaction, sampling,
// runtime and environment metadata, headers and authentication (langsmith 0.10.5). OWNER: client lane.
//
// Files: client.go (construction, headers, authentication, createRun/updateRun, stderr helpers),
// client_prepare.go (payload preparation, runtime merge, size estimate), client_queue.go (AutoBatchQueue, timer,
// drain, workers, server info, awaitPendingTraceBatches), client_ingest.go (processBatch, multipart, batch ingest,
// failed-trace fallback directory), client_jsutil.go (JavaScript value helpers).
//
// Differences that follow from Go, all listed in port/lane-client.md:
//   - the single-threaded event loop becomes one mutex (client.mu) plus bounded goroutines: at most
//     traceBatchConcurrency batch workers, one server-info probe, and the AfterFunc timers; all are stopped and
//     waited for by close();
//   - console.log (LANGSMITH_DEBUG) goes to stderr: stdout belongs to the PiG host;
//   - circular structures cannot be built from Go payload values, so the circular-JSON branch of serialize()
//     (fast-safe-stringify decirc) has no counterpart.

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultBatchSizeLimit                  = 100                // client.js:161
	defaultUncompressedBatchSizeLimitBytes = 24 * 1024 * 1024   // client.js:156
	defaultMaxSizeBytes                    = 1024 * 1024 * 1024 // client.js:158
	serverInfoRequestTimeout               = 10 * time.Second   // client.js:159
	defaultRequestTimeout                  = 90 * time.Second   // client.js (`config.timeout_ms ?? 90_000`)
	defaultAutoBatchDelay                  = 250 * time.Millisecond
	defaultTraceBatchConcurrency           = 5
	defaultFailedTracesMaxBytes            = 100 * 1024 * 1024

	// userAgent is `langsmith-js/<version>` (client.js:1027) describing the real sender (divergence D1).
	userAgent = "pigpen-langsmith-go/" + portVersion
)

// ---- stderr helpers (console.warn / console.error) ----

var (
	stderrMu  sync.Mutex
	stderrOut io.Writer = os.Stderr
)

func writeStderrLine(s string) {
	stderrMu.Lock()
	io.WriteString(stderrOut, s+"\n")
	stderrMu.Unlock()
}

// swapStderr replaces the writer warnf/errorf use and returns the previous one (tests).
func swapStderr(w io.Writer) io.Writer {
	stderrMu.Lock()
	defer stderrMu.Unlock()
	old := stderrOut
	stderrOut = w
	return old
}

// warnf is console.warn: the formatted text and a newline on stderr.
func warnf(format string, args ...any) { writeStderrLine(fmt.Sprintf(format, args...)) }

// errorf is console.error.
func errorf(format string, args ...any) { writeStderrLine(fmt.Sprintf(format, args...)) }

var (
	warnedMu       sync.Mutex
	warnedMessages = map[string]struct{}{}
)

// warnOnce is utils/warn.js warnOnce (without the process.emitWarning branch): the first call prints.
func warnOnce(message string) {
	warnedMu.Lock()
	_, seen := warnedMessages[message]
	warnedMessages[message] = struct{}{}
	warnedMu.Unlock()
	if !seen {
		warnf("%s", message)
	}
}

// errString renders an error the way console.error prints an Error without its stack: `Name: message`.
func errString(err error) string {
	if err == nil {
		return "undefined"
	}
	name := "Error"
	var n namedError
	if errors.As(err, &n) {
		name = n.ErrorName()
	}
	return name + ": " + err.Error()
}

// jsNamedError is an error with a JavaScript name: the TimeoutError / AbortError DOMExceptions fetch rejects with.
type jsNamedError struct {
	name, msg string
	cause     error
}

func (e *jsNamedError) Error() string     { return e.msg }
func (e *jsNamedError) ErrorName() string { return e.name }
func (e *jsNamedError) Unwrap() error     { return e.cause }

// ---- construction ----

// clientOptions is `new Client({apiKey, apiUrl, ...})`. nil pointers are `undefined` (the SDK then reads
// LANGSMITH_API_KEY, LANGSMITH_ENDPOINT, the CLI profile, ...). The extension sets APIKey and APIURL only; the
// other fields are the constructor options of the SDK that tests and embedders use.
type clientOptions struct {
	APIKey *string
	APIURL *string
	// Test seams (upstream's mockClient passes autoBatchTracing: false and fetchImplementation).
	AutoBatchTracing *bool
	HTTPClient       *http.Client

	WorkspaceID                *string
	TracingSamplingRate        *float64
	TimeoutMs                  *int
	TraceBatchConcurrency      *int
	MaxIngestMemoryBytes       *int64
	BatchSizeLimit             *int
	BatchSizeBytesLimit        *int64
	BlockOnRootRunFinalization *bool
	ManualFlushMode            *bool
	OmitTracedRuntimeInfo      *bool
	Debug                      *bool
	HideInputs                 *bool
	HideOutputs                *bool
	HideMetadata               *bool
	TracingMode                *string
	// AutoBatchAggregationDelay is the client's autoBatchAggregationDelayMs field (default 250 ms).
	AutoBatchAggregationDelay *time.Duration
}

// destOptions is the per-call {apiKey, apiUrl, workspaceId} a replica supplies.
type destOptions struct {
	APIKey      *string
	APIURL      *string
	WorkspaceID *string
}

// defaultClientConfig is Client.getDefaultClientConfig's result.
type defaultClientConfig struct {
	apiURL       string
	apiURLSource string
	apiKey       *string
	workspaceID  *string
	hideInputs   bool
	hideOutputs  bool
	hideMetadata bool
	profileAuth  *profileAuth
}

func envPtr(name string) *string {
	if v, ok := getLangSmithEnvironmentVariable(name); ok {
		return &v
	}
	return nil
}

// getDefaultClientConfig is client.js:913-947 (Client.getDefaultClientConfig).
func getDefaultClientConfig() (defaultClientConfig, error) {
	profile, err := loadProfileClientConfig()
	if err != nil {
		return defaultClientConfig{}, err
	}
	envAPIKey := envPtr("API_KEY")
	envAPIURL := envPtr("ENDPOINT")
	envWorkspaceID := envPtr("WORKSPACE_ID")
	envAuthSet := hasValue(envAPIKey)
	cfg := defaultClientConfig{}
	switch {
	case envAPIURL != nil:
		cfg.apiURL, cfg.apiURLSource = *envAPIURL, "LANGSMITH_ENDPOINT / LANGCHAIN_ENDPOINT environment variable"
	case profile.APIURL != nil:
		cfg.apiURL, cfg.apiURLSource = *profile.APIURL, "profile config"
	default:
		cfg.apiURL, cfg.apiURLSource = defaultAPIURL, "built-in default"
	}
	cfg.workspaceID = envWorkspaceID
	if cfg.workspaceID == nil {
		cfg.workspaceID = profile.WorkspaceID
	}
	cfg.apiKey = envAPIKey
	cfg.hideInputs = envIsTrue("HIDE_INPUTS")
	cfg.hideOutputs = envIsTrue("HIDE_OUTPUTS")
	cfg.hideMetadata = envIsTrue("HIDE_METADATA")
	if !envAuthSet {
		cfg.profileAuth = profile.Auth
	}
	return cfg, nil
}

func envIsTrue(name string) bool {
	v, ok := getLangSmithEnvironmentVariable(name)
	return ok && v == "true"
}

// getTracingSamplingRate is client.js:77-88: parseFloat of the option or LANGSMITH_TRACING_SAMPLING_RATE; NaN
// passes the range check (both comparisons are false) and then samples nothing.
func getTracingSamplingRate(configRate *float64) (*float64, error) {
	var s string
	if configRate != nil {
		s = jsNumberString(*configRate)
	} else if v, ok := getLangSmithEnvironmentVariable("TRACING_SAMPLING_RATE"); ok {
		s = v
	} else {
		return nil, nil
	}
	r := jsParseFloat(s)
	if r < 0 || r > 1 {
		return nil, fmt.Errorf("LANGSMITH_TRACING_SAMPLING_RATE must be between 0 and 1 if set. Got: %s", jsNumberString(r))
	}
	return &r, nil
}

// client is a LangSmith Client.
type client struct {
	apiURL                     string
	apiKey                     *string
	workspaceID                *string
	profileAuth                *profileAuth
	timeout                    time.Duration
	hideInputs                 bool
	hideOutputs                bool
	hideMetadata               bool
	omitTracedRuntimeInfo      bool
	tracingSampleRate          *float64
	autoBatchTracing           bool
	blockOnRootRunFinalization bool
	manualFlushMode            bool
	debug                      bool
	fetchImplementation        bool
	autoBatchDelay             time.Duration
	batchSizeBytesLimit        *float64
	batchSizeLimit             *float64
	failedTracesDir            string
	failedTracesMaxBytes       int64
	runCompressionDisabled     bool
	traceBatchConcurrency      int
	cachedEnvVars              *omap

	multipartStreamingDisabled atomic.Bool
	multipartDisabled          atomic.Bool

	hc           *http.Client
	ownTransport *http.Transport
	caller       *asyncCaller
	batchCaller  *asyncCaller

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// mu guards everything below.
	mu            sync.Mutex
	closed        bool
	queue         autoBatchQueue
	timer         *time.Timer // autoBatchTimeout
	timers        map[*time.Timer]struct{}
	serverInfo    *serverInfo
	infoFlight    *infoFlight
	jobs          []batchJob
	workers       int
	pendingDrains int
	nWaiters      int
	stateCh       chan struct{}
}

// newClient is `new Client(opts)`; it fails where the constructor throws (sampling rate outside [0,1],
// invalid trace batch concurrency, an invalid tracing mode, ...) and refuses the unported modes (OTEL tracing)
// loudly.
func newClient(o clientOptions) (*client, error) {
	cfg, err := getDefaultClientConfig()
	if err != nil {
		return nil, err
	}
	c := &client{}
	rate, err := getTracingSamplingRate(o.TracingSamplingRate)
	if err != nil {
		return nil, err
	}
	c.tracingSampleRate = rate

	apiURL := cfg.apiURL
	if o.APIURL != nil {
		apiURL = *o.APIURL
	}
	c.apiURL = strings.TrimSuffix(trimQuotes(apiURL), "/")
	configuredKey := cfg.apiKey
	if o.APIKey != nil {
		configuredKey = o.APIKey
	}
	if configuredKey != nil {
		configuredKey = new(trimQuotes(*configuredKey))
	}
	if hasValue(configuredKey) {
		c.apiKey = configuredKey
	}
	if c.apiKey == nil {
		c.profileAuth = cfg.profileAuth
	}
	ws := cfg.workspaceID
	if o.WorkspaceID != nil {
		ws = o.WorkspaceID
	}
	if ws != nil {
		c.workspaceID = new(trimQuotes(*ws))
	}
	c.timeout = defaultRequestTimeout
	if o.TimeoutMs != nil {
		c.timeout = time.Duration(*o.TimeoutMs) * time.Millisecond
	}
	c.traceBatchConcurrency = defaultTraceBatchConcurrency
	if bg, ok := getEnvironmentVariable("LANGSMITH_TRACING_BACKGROUND"); ok && bg == "false" {
		c.blockOnRootRunFinalization = true
	}
	if o.TraceBatchConcurrency != nil {
		c.traceBatchConcurrency = *o.TraceBatchConcurrency
	}
	if c.traceBatchConcurrency < 1 {
		return nil, errors.New("Trace batch concurrency must be positive.")
	}
	if dbg, ok := getEnvironmentVariable("LANGSMITH_DEBUG"); ok && dbg == "true" {
		c.debug = true
	}
	if o.Debug != nil {
		c.debug = *o.Debug
	}
	if c.debug {
		source := cfg.apiURLSource
		if o.APIURL != nil {
			source = "apiUrl option"
		}
		writeStderrLine("LangSmith API URL " + c.apiURL + " resolved from " + source)
	}
	c.fetchImplementation = o.HTTPClient != nil

	// Failed trace dump configuration (client.js:836-845).
	if d, ok := getLangSmithEnvironmentVariable("FAILED_TRACES_DIR"); ok {
		c.failedTracesDir = d
	}
	c.failedTracesMaxBytes = defaultFailedTracesMaxBytes
	if mb, ok := getLangSmithEnvironmentVariable("FAILED_TRACES_MAX_MB"); ok && mb != "" {
		if n, ok := jsParseInt10(mb); ok && n > 0 {
			c.failedTracesMaxBytes = int64(n * 1024 * 1024)
		}
	}

	maxMemory := int64(defaultMaxSizeBytes)
	if o.MaxIngestMemoryBytes != nil {
		maxMemory = *o.MaxIngestMemoryBytes
	}
	c.caller = newAsyncCaller(callerParams{MaxRetries: 4, MaxQueueSizeBytes: -1, Debug: c.debug})
	c.batchCaller = newAsyncCaller(callerParams{
		MaxRetries: 4, MaxConcurrency: c.traceBatchConcurrency, MaxQueueSizeBytes: maxMemory,
		OnFailedResponse: handle429, Debug: c.debug,
	})

	c.hideInputs, c.hideOutputs, c.hideMetadata = cfg.hideInputs, cfg.hideOutputs, cfg.hideMetadata
	if o.HideInputs != nil {
		c.hideInputs = *o.HideInputs
	}
	if o.HideOutputs != nil {
		c.hideOutputs = *o.HideOutputs
	}
	if o.HideMetadata != nil {
		c.hideMetadata = *o.HideMetadata
	}
	if o.OmitTracedRuntimeInfo != nil {
		c.omitTracedRuntimeInfo = *o.OmitTracedRuntimeInfo
	}
	c.autoBatchTracing = true
	if o.AutoBatchTracing != nil {
		c.autoBatchTracing = *o.AutoBatchTracing
	}
	c.autoBatchDelay = defaultAutoBatchDelay
	if o.AutoBatchAggregationDelay != nil {
		c.autoBatchDelay = *o.AutoBatchAggregationDelay
	}
	c.queue.maxSizeBytes = maxMemory
	if o.BlockOnRootRunFinalization != nil {
		c.blockOnRootRunFinalization = *o.BlockOnRootRunFinalization
	}
	if o.BatchSizeBytesLimit != nil {
		f := float64(*o.BatchSizeBytesLimit)
		c.batchSizeBytesLimit = &f
	}
	if o.BatchSizeLimit != nil {
		f := float64(*o.BatchSizeLimit)
		c.batchSizeLimit = &f
	}
	if o.ManualFlushMode != nil {
		c.manualFlushMode = *o.ManualFlushMode
	}
	c.multipartStreamingDisabled.Store(envIsTrue("DISABLE_MULTIPART_STREAMING"))
	c.runCompressionDisabled = envIsTrue("DISABLE_RUN_COMPRESSION")

	mode, err := resolveTracingMode(o.TracingMode)
	if err != nil {
		return nil, err
	}
	if mode == "otel" {
		return nil, errors.New("OTEL tracing mode is not supported by the Go port of langsmith-pi-extension " +
			"(LANGSMITH_TRACING_MODE=otel, LANGSMITH_OTEL_ENABLED or OTEL_ENABLED requested it): " +
			"unset the option to trace with LangSmith")
	}
	// Cached once so process.env is not scanned for every run (client.js:884).
	c.cachedEnvVars = getLangSmithEnvVarsMetadata()

	if o.HTTPClient != nil {
		c.hc = o.HTTPClient
	} else {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.MaxIdleConnsPerHost = max(8, c.traceBatchConcurrency)
		// Node 24's fetch speaks HTTP/1.1 (a chunked multipart body); over TLS Go would negotiate HTTP/2.
		tr.ForceAttemptHTTP2 = false
		tr.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
		var protocols http.Protocols
		protocols.SetHTTP1(true)
		tr.Protocols = &protocols
		c.ownTransport = tr
		c.hc = &http.Client{Transport: tr}
	}
	c.ctx, c.cancel = context.WithCancel(context.Background())
	c.timers = map[*time.Timer]struct{}{}
	c.stateCh = make(chan struct{})
	return c, nil
}

// handle429 is client.js:111-122. The SDK's errors carry no response, so the hook always sees nil.
func handle429(resp *http.Response) bool {
	if resp != nil && resp.StatusCode == 429 {
		v := "10"
		if h := resp.Header.Get("retry-after"); h != "" {
			v = h
		}
		if n, ok := jsParseInt10(v); ok && n*1000 > 0 {
			time.Sleep(time.Duration(n*1000) * time.Millisecond)
			return true
		}
	}
	return false
}

// ---- headers and authentication ----

// mergedHeaders is _mergedHeaders (client.js:1024-1045): User-Agent, the credential and the tenant. The extension
// supplies no custom headers, so the caller-header channels are empty.
func (c *client) mergedHeaders() (http.Header, error) {
	h := make(http.Header, 6)
	h.Set("User-Agent", userAgent)
	if c.apiKey != nil {
		h.Set("x-api-key", *c.apiKey)
	} else if c.profileAuth != nil {
		ah, err := c.profileAuth.currentAuthHeader()
		if err != nil {
			return nil, err
		}
		if ah != nil {
			h.Set(ah.Name, ah.Value)
		}
	}
	if c.workspaceID != nil && *c.workspaceID != "" {
		h.Set("x-tenant-id", *c.workspaceID)
	}
	return h, nil
}

// withDest applies a destination override to headers: options.apiKey and options.workspaceId (client.js:1757-1762).
func withDest(h http.Header, apiKey, workspaceID *string) {
	if apiKey != nil {
		h.Set("x-api-key", *apiKey)
	}
	if workspaceID != nil {
		h.Set("x-tenant-id", *workspaceID)
	}
}

func hasValueStr(s string) bool { return jsTrim(s) != "" }

func (c *client) isProfileManagedAuthorization(value string, profileManaged *string) bool {
	if profileManaged != nil && value == *profileManaged {
		return true
	}
	return c.profileAuth != nil && c.profileAuth.isProfileAuthorizationHeader(value)
}

// hasExplicitAuthHeader is client.js:362-375.
func (c *client) hasExplicitAuthHeader(h http.Header, profileManaged *string) bool {
	if hasValueStr(h.Get("x-api-key")) {
		return true
	}
	authorization := h.Get("Authorization")
	if !hasValueStr(authorization) {
		return false
	}
	return !c.isProfileManagedAuthorization(authorization, profileManaged)
}

// applyCurrentAuthHeaders is the plain-object branch of client.js:376-466 (our headers are always a plain object).
func (c *client) applyCurrentAuthHeaders(h http.Header, ah *authHeader, profileManaged *string) {
	if ah == nil {
		return
	}
	hasAPIKey := hasValueStr(h.Get("x-api-key"))
	authorization := h.Get("Authorization")
	hasExplicitAuthorization := hasValueStr(authorization) && !c.isProfileManagedAuthorization(authorization, profileManaged)
	if c.apiKey != nil && ah.Name == "x-api-key" {
		h.Del("Authorization")
		if !hasAPIKey {
			h.Set("x-api-key", ah.Value)
		}
		return
	}
	if ah.Name == "Authorization" {
		if !hasAPIKey && !hasExplicitAuthorization {
			h.Set("Authorization", ah.Value)
		}
		return
	}
	if !hasExplicitAuthorization {
		h.Del("Authorization")
		if !hasAPIKey {
			h.Set("x-api-key", ah.Value)
		}
	}
}

// fetch is Client._fetch (client.js:332-345) plus singletons/fetch.js _getFetchImplementation: it resolves the
// credential at request time (profile tokens refresh here) and logs with LANGSMITH_DEBUG. tctx carries the
// request's AbortSignal.timeout.
func (c *client) fetch(ctx, tctx context.Context, method, url string, h http.Header, body io.ReadCloser, contentLength int64) (*http.Response, error) {
	var profileManaged *string
	if c.profileAuth != nil {
		if a := h.Get("Authorization"); hasValueStr(a) && c.profileAuth.isProfileAuthorizationHeader(a) {
			profileManaged = &a
		}
	}
	var ah *authHeader
	if c.apiKey != nil {
		ah = &authHeader{Name: "x-api-key", Value: *c.apiKey}
	} else if !c.hasExplicitAuthHeader(h, profileManaged) && c.profileAuth != nil {
		var err error
		if ah, err = c.profileAuth.getAuthHeader(tctx, c.hc); err != nil {
			if body != nil {
				body.Close()
			}
			return nil, err
		}
	}
	c.applyCurrentAuthHeaders(h, ah, profileManaged)

	req, err := http.NewRequestWithContext(tctx, method, url, nil)
	if err != nil {
		if body != nil {
			body.Close()
		}
		return nil, &fetchTypeError{Message: "Failed to parse URL from " + url, Cause: err}
	}
	if body != nil {
		req.Body = body
		req.ContentLength = contentLength
	}
	req.Header = h
	logReq := !c.fetchImplementation && (c.debug || envIsTrue("DEBUG"))
	if logReq {
		writeStderrLine("→ " + method + " " + url)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fetchError(ctx, tctx, err)
	}
	if logReq {
		writeStderrLine(fmt.Sprintf("← %d %s %s", resp.StatusCode, statusText(resp), url))
	}
	return resp, nil
}

// fetchError maps a transport error to the error fetch rejects with.
func fetchError(ctx, tctx context.Context, err error) error {
	switch {
	case ctx.Err() != nil:
		return &jsNamedError{name: "AbortError", msg: "This operation was aborted", cause: err}
	case errors.Is(tctx.Err(), context.DeadlineExceeded):
		return &jsNamedError{name: "TimeoutError", msg: "The operation was aborted due to timeout", cause: err}
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() && errors.Is(err, context.DeadlineExceeded) {
		return &jsNamedError{name: "TimeoutError", msg: "The operation was aborted due to timeout", cause: err}
	}
	return &fetchTypeError{Message: "fetch failed", Cause: err}
}

// attempt is one `await this._fetch(...)` + `await raiseForStatus(res, op, consume)` of the original, with the
// AbortSignal.timeout(timeout) that covers both. onOK reads the body of a 2xx response before the timeout ends.
func (c *client) attempt(ctx context.Context, timeout time.Duration, method, url string, h http.Header, body io.ReadCloser, contentLength int64, op string, consume bool, onOK func(*http.Response) error) (*http.Response, error) {
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resp, err := c.fetch(ctx, tctx, method, url, h, body, contentLength)
	if err != nil {
		return nil, err
	}
	if err := raiseForStatus(resp, op, consume); err != nil {
		return nil, err
	}
	if onOK != nil {
		if err := onOK(resp); err != nil {
			return nil, err
		}
	}
	return resp, nil
}

// ---- createRun / updateRun ----

const (
	actionCreate = iota
	actionUpdate
)

func isDefined(v any) bool { return !isUndef(v) }

// createRun is Client.createRun(run, options) (client.js:1727-1775). run is the `_convertToCreate` /
// `_remapForProject` result; the client takes it over (the original deletes project_name from it too).
func (c *client) createRun(ctx context.Context, run *omap, opts *destOptions) error {
	if !c.shouldSample(run, run.val("id")) {
		return nil
	}
	sessionName := run.val("project_name")
	run.del("project_name")
	runCreate := newOmapCap(run.size() + 2)
	runCreate.set("session_name", sessionName)
	runCreate.spread(run)
	if st := runCreate.val("start_time"); st == nil || isUndef(st) { // `run.start_time ?? Date.now()`
		runCreate.set("start_time", nowMs())
	}
	c.prepareRunInPlace(runCreate)
	if c.autoBatchTracing && isDefined(runCreate.val("trace_id")) && isDefined(runCreate.val("dotted_order")) {
		c.processRunOperation(actionCreate, runCreate, opts)
		return nil
	}
	merged := mergeRuntimeEnvIntoRun(runCreate, c.cachedEnvVars, c.omitTracedRuntimeInfo, c.tracingSampleRate)
	if !c.omitTracedRuntimeInfo {
		merged = c.maskRunMetadata(merged)
	}
	buf := getBuf()
	buf.b = serializeForTracing(buf.b, merged, func() string {
		return "Creating run with id: " + jsString(merged.val("id"))
	})
	body := newRawBody(buf)
	defer body.release()
	return c.postJSON(ctx, "POST", "/runs", opts, body, "create run")
}

// postJSON is the direct `caller.call` of createRun / updateRun (client.js:1764-1774, 2207-2217).
func (c *client) postJSON(ctx context.Context, method, path string, opts *destOptions, body *multipartBody, op string) error {
	h, err := c.mergedHeaders()
	if err != nil {
		return err
	}
	h.Set("Content-Type", "application/json")
	url := c.apiURL
	if opts != nil {
		withDest(h, opts.APIKey, opts.WorkspaceID)
		if opts.APIURL != nil {
			url = *opts.APIURL
		}
	}
	url += path
	_, err = c.caller.call(ctx, func(ctx context.Context) (*http.Response, error) {
		return c.attempt(ctx, c.timeout, method, url, h.Clone(), body.newReader(false), body.total, op, true, nil)
	})
	return err
}

// updateRun is Client.updateRun(runId, run, options) (client.js:2137-2218); run is taken over.
func (c *client) updateRun(ctx context.Context, runID string, run *omap, opts *destOptions) error {
	if err := assertUUID(runID); err != nil {
		return err
	}
	if v := run.val("inputs"); jsTruthy(v) {
		run.set("inputs", c.processInputs(v))
	}
	if v := run.val("outputs"); jsTruthy(v) {
		run.set("outputs", c.processOutputs(v))
	}
	if v := run.val("error"); jsTruthy(v) {
		run.set("error", c.processError(v))
	}
	if c.hideMetadata {
		if extra, ok := toOmap(run.val("extra")); ok && extra.has("metadata") {
			run.set("extra", extra.clone().set("metadata", c.processMetadata(extra.val("metadata"))))
		}
	}
	if v := run.val("events"); jsTruthy(v) {
		run.set("events", filterNewTokenEvents(v))
	}
	if !c.shouldSample(run, runID) {
		return nil
	}
	if c.autoBatchTracing && isDefined(run.val("trace_id")) && isDefined(run.val("dotted_order")) {
		run.set("id", runID) // `{...run, id: runId}`
		block := isDefined(run.val("end_time")) && isUndef(run.val("parent_run_id")) &&
			c.blockOnRootRunFinalization && !c.manualFlushMode
		it := c.processRunOperation(actionUpdate, run, opts)
		if block && it != nil {
			// Trigger batches as soon as a root trace ends and wait to ensure trace finishes (client.js:2170).
			return c.waitUntil(ctx, func() bool { return it.resolved })
		}
		return nil
	}
	out := run
	if jsTruthy(run.val("extra")) {
		out = mergeRuntimeEnvIntoRun(run, c.cachedEnvVars, c.omitTracedRuntimeInfo, c.tracingSampleRate)
	}
	buf := getBuf()
	buf.b = serializeForTracing(buf.b, out, func() string { return "Serializing payload to update run with id: " + runID })
	body := newRawBody(buf)
	defer body.release()
	return c.postJSON(ctx, "PATCH", "/runs/"+runID, opts, body, "update run")
}
