package typesafe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

// HTTPDoer is the HTTP transport (the TypeScript `fetch` option); *http.Client
// satisfies it. The request carries the attempt's context.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Config configures a [Client]. Explicit values take precedence over environment
// variables, then the defaults; a zero value means "not set".
type Config struct {
	// APIKey is required; falls back to TYPESAFE_API_KEY.
	APIKey string
	// BaseURL is the API root; falls back to TYPESAFE_BASE_URL, then DefaultBaseURL.
	// Trailing slashes are removed.
	BaseURL string
	// DefaultModel falls back to TYPESAFE_DEFAULT_MODEL, then DefaultModel.
	DefaultModel string
	// LogLevel falls back to TYPESAFE_LOG_LEVEL, then DefaultLogLevel.
	LogLevel LogLevel
	// Logger is filtered to LogLevel and above; nil uses [NewStderrLogger].
	Logger Logger
	// Retry overrides the retry defaults field by field.
	Retry RetryOverrides
	// Timeout is per attempt (there is no total retry budget); zero means DefaultTimeout,
	// a negative value is an error.
	Timeout time.Duration
	// DefaultHeaders are sent with every request; per-call headers take precedence.
	// The SDK's own headers (Authorization, Content-Type, Accept, User-Agent,
	// X-TypeSafe-*) cannot be replaced.
	DefaultHeaders map[string]string
	// HTTPClient is the transport; nil uses a client with no overall timeout.
	HTTPClient HTTPDoer
	// Getenv reads the environment; nil uses os.Getenv.
	Getenv func(string) string
}

// RequestOptions are per-call overrides. Cancellation and deadlines come from the
// context passed to the call.
type RequestOptions struct {
	// Timeout per attempt; zero inherits the client's, a negative value is an error.
	Timeout time.Duration
	// Retry overrides the client's policy for this call, field by field.
	Retry RetryOverrides
	// Headers are merged over the client's default headers.
	Headers map[string]string
}

// Evaluator answers typed questions. [Client] (the TypeSafe API) and the own-model
// backend implement it; code that only needs answers should depend on this interface.
type Evaluator interface {
	SystemOne(ctx context.Context, req SystemOneRequest, opts *RequestOptions) (*SystemOneResult, error)
}

// Client is a client for the TypeSafe AI API. It is safe for concurrent use. Its
// String, GoString and Format methods never reveal the API key.
type Client struct {
	// apiKey is a closure so that no formatting of the struct can print the key.
	apiKey         func() string
	baseURL        string
	defaultModel   string
	logLevel       LogLevel
	logger         Logger
	retry          RetryPolicy
	timeout        time.Duration
	defaultHeaders map[string]string
	doer           HTTPDoer
	models         *Models
	requestCount   atomic.Int64

	// random supplies jitter in [0,1); tests replace it. nil uses math/rand.
	random func() float64
}

var _ Evaluator = (*Client)(nil)

func readEnv(getenv func(string) string, name string) string { return strings.TrimSpace(getenv(name)) }

// NewClient validates the configuration and returns a client. It returns a
// *TypeSafeError when the API key is missing (naming TYPESAFE_API_KEY), the log level
// is invalid, or a timeout or retry field is out of range.
func NewClient(cfg Config) (*Client, error) {
	getenv := cfg.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	fromConfigOrEnv := func(explicit, env string) string {
		if explicit != "" {
			return explicit
		}
		return readEnv(getenv, env)
	}
	key := fromConfigOrEnv(cfg.APIKey, EnvAPIKey)
	if key == "" {
		return nil, errorf("No API key was provided. Set `APIKey` in the Config passed to NewClient, or set the %s environment variable.", EnvAPIKey)
	}
	baseURL := fromConfigOrEnv(cfg.BaseURL, EnvBaseURL)
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	model := fromConfigOrEnv(cfg.DefaultModel, EnvDefaultModel)
	if model == "" {
		model = DefaultModel
	}
	level := DefaultLogLevel
	var err error
	if cfg.LogLevel != "" {
		level, err = ParseLogLevel(string(cfg.LogLevel), "the `LogLevel` option")
	} else if env := readEnv(getenv, EnvLogLevel); env != "" {
		level, err = ParseLogLevel(env, EnvLogLevel)
	}
	if err != nil {
		return nil, err
	}
	sink := cfg.Logger
	if sink == nil {
		sink = NewStderrLogger()
	}
	retry, err := DefaultRetryPolicy().Resolve(cfg.Retry)
	if err != nil {
		return nil, err
	}
	timeout, err := resolveTimeout("Timeout", cfg.Timeout, DefaultTimeout)
	if err != nil {
		return nil, err
	}
	headers := make(map[string]string, len(cfg.DefaultHeaders))
	for k, v := range cfg.DefaultHeaders {
		headers[k] = v
	}
	doer := cfg.HTTPClient
	if doer == nil {
		doer = &http.Client{}
	}
	c := &Client{
		apiKey:         func() string { return key },
		baseURL:        baseURL,
		defaultModel:   model,
		logLevel:       level,
		logger:         WithLevel(sink, level),
		retry:          retry,
		timeout:        timeout,
		defaultHeaders: headers,
		doer:           doer,
	}
	c.models = &Models{c: c}
	return c, nil
}

// resolveTimeout applies the zero-means-inherit rule and rejects negative values.
func resolveTimeout(name string, v, inherit time.Duration) (time.Duration, error) {
	if v < 0 {
		return 0, errorf("`%s` must be a positive duration, got %s.", name, v)
	}
	if v == 0 {
		return inherit, nil
	}
	return v, nil
}

// BaseURL returns the API root without trailing slashes.
func (c *Client) BaseURL() string { return c.baseURL }

// DefaultModel returns the model used when a request omits one.
func (c *Client) DefaultModel() string { return c.defaultModel }

// LogLevel returns the configured verbosity.
func (c *Client) LogLevel() LogLevel { return c.logLevel }

// Logger returns the configured logger, filtered to the log level.
func (c *Client) Logger() Logger { return c.logger }

// RetryPolicy returns a copy of the resolved retry policy.
func (c *Client) RetryPolicy() RetryPolicy {
	p, _ := c.retry.Resolve(RetryOverrides{})
	return p
}

// Timeout returns the per-attempt timeout.
func (c *Client) Timeout() time.Duration { return c.timeout }

// DefaultHeaders returns a copy of the additional default headers.
func (c *Client) DefaultHeaders() map[string]string {
	out := make(map[string]string, len(c.defaultHeaders))
	for k, v := range c.defaultHeaders {
		out[k] = v
	}
	return out
}

// String returns a description without the API key.
func (c *Client) String() string {
	return fmt.Sprintf("typesafe.Client{BaseURL: %s, DefaultModel: %s, LogLevel: %s}", c.baseURL, c.defaultModel, c.logLevel)
}

// GoString returns a description without the API key.
func (c *Client) GoString() string { return c.String() }

// Models returns the Models resource.
func (c *Client) Models() *Models { return c.models }

// SystemOne answers named questions about text or structured state. Questions are
// validated before anything is sent (*TypeSafeError). It returns an *APIError
// subclass for a non-2xx response after retries, an *APIConnectionError (or
// *APITimeoutError) for transport failures after retries, and an *APIUserAbortError
// when ctx ends.
func (c *Client) SystemOne(ctx context.Context, req SystemOneRequest, opts *RequestOptions) (*SystemOneResult, error) {
	res, err := c.SystemOneWithResponse(ctx, req, opts)
	if err != nil {
		return nil, err
	}
	return res.Data, nil
}

// Response is a parsed result with the HTTP response metadata.
type Response[T any] struct {
	Data T
	RawResponse
}

// RawResponse is the buffered HTTP response of a successful request.
type RawResponse struct {
	Status int
	Header http.Header
	// Body is the complete response body (buffered under the attempt timeout).
	Body []byte
	// URL is the requested URL.
	URL string
	// RequestID is the x-typesafe-request-id header, or "".
	RequestID string

	tag string // the log tag of the request
}

// logBody logs a parsed response body at debug level, like the TypeScript parse step.
func (c *Client) logBody(raw *RawResponse) {
	if c.logLevel != LogDebug {
		return // the logger drops it; do not parse the body to build it
	}
	parsed, _ := parseBody(raw.Body)
	c.logger.Debug(raw.tag+" <- body", parsed)
}

func (c *Client) systemOnePayload(req SystemOneRequest) ([]byte, error) {
	if err := req.Questions.Validate(); err != nil {
		return nil, err
	}
	if req.Model == "" {
		req.Model = c.defaultModel
	}
	body, err := marshalPlain(req)
	if err != nil {
		return nil, unwrapMarshalError(err)
	}
	return body, nil
}

// SystemOneWithResponse is [Client.SystemOne] with the response metadata.
func (c *Client) SystemOneWithResponse(ctx context.Context, req SystemOneRequest, opts *RequestOptions) (*Response[*SystemOneResult], error) {
	body, err := c.systemOnePayload(req)
	if err != nil {
		return nil, err
	}
	raw, err := c.request(ctx, http.MethodPost, "/v1/systemone", body, opts)
	if err != nil {
		return nil, err
	}
	c.logBody(raw)
	result := &SystemOneResult{}
	if len(bytes.TrimSpace(raw.Body)) > 0 {
		if err := json.Unmarshal(raw.Body, result); err != nil {
			return nil, &TypeSafeError{Message: "Unexpected response shape from POST /v1/systemone; expected { model, answers, usage }.", Cause: err}
		}
	}
	return &Response[*SystemOneResult]{Data: result, RawResponse: *raw}, nil
}

// SystemOneRaw sends the request and returns the response without parsing its body
// (the TypeScript asResponse()). A non-2xx status is still an *APIError.
func (c *Client) SystemOneRaw(ctx context.Context, req SystemOneRequest, opts *RequestOptions) (*RawResponse, error) {
	body, err := c.systemOnePayload(req)
	if err != nil {
		return nil, err
	}
	return c.request(ctx, http.MethodPost, "/v1/systemone", body, opts)
}

// Models is the Models API resource.
type Models struct {
	c *Client
}

// List lists the models available to the account (GET /v1/models). A body that is not
// {"models": [...]} is a *TypeSafeError "Unexpected response shape from GET /v1/models; expected { models: [...] }."
func (m *Models) List(ctx context.Context, opts *RequestOptions) ([]ModelCard, error) {
	res, err := m.ListWithResponse(ctx, opts)
	if err != nil {
		return nil, err
	}
	return res.Data, nil
}

// ListWithResponse is [Models.List] with the response metadata.
func (m *Models) ListWithResponse(ctx context.Context, opts *RequestOptions) (*Response[[]ModelCard], error) {
	raw, err := m.c.request(ctx, http.MethodGet, "/v1/models", nil, opts)
	if err != nil {
		return nil, err
	}
	m.c.logBody(raw)
	shapeErr := &TypeSafeError{Message: "Unexpected response shape from GET /v1/models; expected { models: [...] }."}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(raw.Body, &wire); err != nil || wire == nil {
		return nil, shapeErr
	}
	list := bytes.TrimSpace(wire["models"])
	if len(list) == 0 || list[0] != '[' {
		return nil, shapeErr
	}
	cards := []ModelCard{}
	if err := json.Unmarshal(list, &cards); err != nil {
		return nil, &TypeSafeError{Message: shapeErr.Message, Cause: err}
	}
	return &Response[[]ModelCard]{Data: cards, RawResponse: *raw}, nil
}

// ListRaw returns the unparsed response, which keeps fields the typed card drops.
func (m *Models) ListRaw(ctx context.Context, opts *RequestOptions) (*RawResponse, error) {
	return m.c.request(ctx, http.MethodGet, "/v1/models", nil, opts)
}

// MapResponse transforms the data of a response and keeps its metadata.
func MapResponse[T, U any](r *Response[T], fn func(T) U) *Response[U] {
	return &Response[U]{Data: fn(r.Data), RawResponse: r.RawResponse}
}

// header is one request header in the spelling the SDK uses.
type header struct{ name, value string }

// mergeHeaders merges sources in order: a later value wins regardless of the name's
// casing, and an entry with remove set deletes a protected header.
func mergeHeaders(sources ...[]headerEdit) []header {
	var out []header
	index := map[string]int{}
	for _, src := range sources {
		for _, e := range src {
			lower := strings.ToLower(e.name)
			i, exists := index[lower]
			switch {
			case e.remove && exists:
				out[i].name = ""
				delete(index, lower)
			case e.remove:
			case exists:
				out[i] = header{e.name, e.value}
			default:
				index[lower] = len(out)
				out = append(out, header{e.name, e.value})
			}
		}
	}
	kept := out[:0]
	for _, h := range out {
		if h.name != "" {
			kept = append(kept, h)
		}
	}
	return kept
}

type headerEdit struct {
	name, value string
	remove      bool
}

func editsOf(m map[string]string) []headerEdit {
	out := make([]headerEdit, 0, len(m))
	for k, v := range m {
		out = append(out, headerEdit{name: k, value: v})
	}
	return out
}

// request sends one API request with retries and returns its buffered response.
func (c *Client) request(ctx context.Context, method, path string, body []byte, opts *RequestOptions) (*RawResponse, error) {
	if opts == nil {
		opts = &RequestOptions{}
	}
	timeout, err := resolveTimeout("Timeout", opts.Timeout, c.timeout)
	if err != nil {
		return nil, err
	}
	policy, err := c.retry.Resolve(opts.Retry)
	if err != nil {
		return nil, err
	}
	// Numbered so concurrent requests, and the attempts within one, can be told apart in the logs.
	tag := fmt.Sprintf("#%d %s %s", c.requestCount.Add(1), method, path)
	url := c.baseURL + path
	// User-supplied headers go first so they cannot clobber auth or the JSON content type.
	protected := []headerEdit{
		{name: "Authorization", value: "Bearer " + c.apiKey()},
		{name: "Accept", value: "application/json"},
		{name: "User-Agent", value: "typesafe-sdk-go/" + Version},
		{name: "X-TypeSafe-SDK", value: "typesafe-sdk-go/" + Version},
		{name: "X-TypeSafe-Runtime", value: describeRuntime()},
		{name: "X-TypeSafe-Retry-Count", remove: true},
	}
	if body == nil {
		protected = append(protected, headerEdit{name: "Content-Type", remove: true})
	} else {
		protected = append(protected, headerEdit{name: "Content-Type", value: "application/json"})
	}
	base := mergeHeaders(editsOf(c.defaultHeaders), editsOf(opts.Headers), protected)

	hooks := RetryHooks{
		Random: c.random,
		OnRetry: func(retry, total int, delay time.Duration, reason string) {
			c.logger.Info(fmt.Sprintf("%s retrying in %dms (retry %d/%d) after %s", tag, delay.Milliseconds(), retry, total, reason))
		},
		OnAbortDuringWait: func() { c.logger.Info(tag + " aborted by caller while waiting to retry") },
	}
	return Retry(ctx, policy, hooks, func(ctx context.Context, attempt int) (*RawResponse, error) {
		headers := base
		if attempt > 0 {
			headers = append(append([]header(nil), base...), header{"X-TypeSafe-Retry-Count", fmt.Sprint(attempt)})
		}
		if c.logLevel == LogDebug { // only debug prints headers and body; the other levels would drop them
			logged := make(map[string]string, len(headers))
			for _, h := range headers {
				logged[h.name] = h.value
			}
			var bodyLog any
			if body != nil {
				bodyLog = json.RawMessage(body)
			}
			c.logger.Debug(fmt.Sprintf("%s -> %s", tag, url), map[string]any{"headers": RedactHeaders(logged), "body": bodyLog})
		}

		started := time.Now()
		raw, err := c.attempt(ctx, tag, method, url, headers, body, timeout, started)
		if err != nil {
			return nil, err
		}
		requestID := ""
		if raw.RequestID != "" {
			requestID = fmt.Sprintf(" (request %s)", raw.RequestID)
		}
		c.logger.Info(fmt.Sprintf("%s <- %d in %dms%s", tag, raw.Status, time.Since(started).Milliseconds(), requestID))
		if raw.Status >= 200 && raw.Status < 300 {
			return raw, nil
		}
		parsed, text := parseBody(raw.Body)
		c.logger.Debug(tag+" <- error body", parsed)
		return nil, newAPIError(raw.Status, parsed, text, raw.Header)
	})
}

// parseBody parses a response body leniently: JSON when it is JSON (whatever the
// content type), else the text; nil for an empty body. The second value is the raw JSON
// text when the body parsed.
func parseBody(body []byte) (parsed any, rawJSON string) {
	if len(body) == 0 {
		return nil, ""
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return string(body), ""
	}
	return v, string(body)
}

// attempt is one HTTP round trip including body delivery, under the attempt timeout. The
// caller's context and the timer are told apart to choose the error class.
func (c *Client) attempt(ctx context.Context, tag, method, url string, headers []header, body []byte, timeout time.Duration, started time.Time) (*RawResponse, error) {
	actx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	elapsed := func() string { return fmt.Sprintf("%dms", time.Since(started).Milliseconds()) }
	fail := func(cause error) error {
		if ctx.Err() != nil {
			c.logger.Info(fmt.Sprintf("%s aborted by caller after %s", tag, elapsed()))
			return newAbortError(context.Cause(ctx))
		}
		if errors.Is(actx.Err(), context.DeadlineExceeded) {
			c.logger.Info(fmt.Sprintf("%s timed out after %s", tag, elapsed()))
			return newTimeoutError(timeout, cause)
		}
		c.logger.Info(fmt.Sprintf("%s connection error after %s", tag, elapsed()), cause)
		return newConnectionError(cause)
	}

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(actx, method, url, reader)
	if err != nil {
		return nil, fail(err)
	}
	for _, h := range headers {
		req.Header.Set(h.name, h.value)
	}
	resp, err := c.doer.Do(req)
	if err != nil {
		return nil, fail(err)
	}
	data, err := readBody(actx, resp.Body)
	if err != nil {
		return nil, fail(err)
	}
	if ctx.Err() != nil { // the caller gave up while the body was arriving
		return nil, fail(context.Cause(ctx))
	}
	return &RawResponse{
		Status:    resp.StatusCode,
		Header:    resp.Header.Clone(),
		Body:      data,
		URL:       url,
		RequestID: resp.Header.Get(requestIDHeader),
		tag:       tag,
	}, nil
}

// readBody reads and closes body, giving up when ctx ends even if the body ignores
// cancellation (a transport that does not honor the request's context).
func readBody(ctx context.Context, body io.ReadCloser) ([]byte, error) {
	if body == nil {
		return nil, nil
	}
	type result struct {
		data []byte
		err  error
	}
	done := make(chan result, 1)
	go func() {
		data, err := io.ReadAll(body)
		done <- result{data, err}
	}()
	select {
	case r := <-done:
		_ = body.Close()
		return r.data, r.err
	case <-ctx.Done():
		_ = body.Close()
		return nil, context.Cause(ctx)
	}
}
