package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Port of curator-server.ts: the local HTTP server behind the search curator page. The browser
// page talks to it with a session token: it streams search results over server-sent events, asks
// for summaries and query rewrites, and finally submits or cancels. The server also ends the
// session by itself when the browser goes silent or the user is idle for too long.

const (
	curatorStaleThresholdMs   = 30000
	curatorWatchdogInterval   = time.Second
	curatorMaxBodySize        = 64 * 1024
	curatorSSEKeepaliveEvery  = 15 * time.Second
	curatorShutdownGraceDelay = 5 * time.Second
)

// CuratorServerOptions are the inputs of startCuratorServer.
type CuratorServerOptions struct {
	Queries []string
	// InitialResultIndexCapacity is the first index available to searches added while initial
	// results are still streaming; nil means len(Queries).
	InitialResultIndexCapacity *int
	SessionToken               string
	// Timeout is the idle timeout in seconds.
	Timeout             float64
	AvailableProviders  ProviderAvailability
	DefaultProvider     string
	SearchProvider      string
	SummaryModels       []SummaryModelChoice
	DefaultSummaryModel *string
	// Network is where the server binds and what host its URL names (resolveCuratorNetworkConfig).
	Network CuratorNetwork
}

// CuratorSource is one source of a curator entry.
type CuratorSource struct {
	Title   string  `json:"title"`
	URL     string  `json:"url"`
	Snippet *string `json:"snippet,omitempty"`
	Domain  string  `json:"domain"`
}

// CuratorSearchEntry is one provider's result for a query (CuratorSearchEntry).
type CuratorSearchEntry struct {
	Answer   string          `json:"answer"`
	Results  []CuratorSource `json:"results"`
	Provider string          `json:"provider"`
	Error    string          `json:"error,omitempty"`
}

// IndexedCuratorSearchEntry is an entry with its result index and the query that produced it.
type IndexedCuratorSearchEntry struct {
	CuratorSearchEntry
	QueryIndex int    `json:"queryIndex"`
	Query      string `json:"query"`
}

// CuratorSubmitPayload is what the browser submitted.
type CuratorSubmitPayload struct {
	SelectedQueryIndices []int
	// Summary is the trimmed summary text; "" when none was submitted.
	Summary                      string
	SummaryMeta                  *SummaryMeta
	RawResults                   bool
	AutoApproveRemainingSearches bool
}

// CuratorServerCallbacks are the hooks of startCuratorServer. Each is called on its own goroutine
// after the response that triggered it was written (setImmediate upstream), except the three
// that answer the request itself (OnAddSearch, OnSummarize, OnRewriteQuery).
type CuratorServerCallbacks struct {
	OnSubmit           func(CuratorSubmitPayload)
	OnCancel           func(reason string)
	OnProviderChange   func(provider string)
	OnAddSearch        func(query string, provider *string) ([]CuratorSearchEntry, error)
	OnAddSearchResults func(entries []IndexedCuratorSearchEntry)
	OnSummarize        func(ctx context.Context, selected []int, model, feedback string) (SummaryDraft, error)
	OnRewriteQuery     func(ctx context.Context, query string) (string, error)
}

// CuratorPush is the data of pushResult: an entry with its query and the slot of the query it
// belongs to. Query "" means the query list's entry at that index.
type CuratorPush struct {
	CuratorSearchEntry
	Query     string
	SlotIndex *int
}

// CuratorConnectionState reports the browser connection (getConnectionState).
type CuratorConnectionState struct {
	BrowserConnected   bool
	LastHeartbeatAgeMs int64
}

// curatorStoredEvent is one retained SSE event. The key order of the JSON is the order the
// original's spread produced for the call sites it has.
type curatorStoredEvent struct {
	event string // "result" or "search-error"
	data  any
}

type curatorResultEventData struct {
	CuratorSearchEntry
	Query      string `json:"query"`
	SlotIndex  *int   `json:"slotIndex,omitempty"`
	QueryIndex int    `json:"queryIndex"`
}

type curatorErrorEventData struct {
	QueryIndex int    `json:"queryIndex"`
	Query      string `json:"query"`
	Error      string `json:"error"`
	Provider   string `json:"provider,omitempty"`
	SlotIndex  *int   `json:"slotIndex,omitempty"`
}

// MarshalJSON writes {"event": ..., "data": ...}.
func (e curatorStoredEvent) MarshalJSON() ([]byte, error) {
	data, err := marshalNoEscape(e.data)
	if err != nil {
		return nil, err
	}
	name, _ := marshalNoEscape(e.event)
	return []byte(`{"event":` + string(name) + `,"data":` + string(data) + `}`), nil
}

// curatorSSEClient is the one connected event stream.
type curatorSSEClient struct {
	mu      sync.Mutex
	w       http.ResponseWriter
	flusher http.Flusher
	done    chan struct{}
	closed  bool
}

func (c *curatorSSEClient) write(payload string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false
	}
	if _, err := io.WriteString(c.w, payload); err != nil {
		return false
	}
	c.flusher.Flush()
	return true
}

// end closes the stream: the handler returns and the response ends.
func (c *curatorSSEClient) end() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		close(c.done)
	}
}

// CuratorServerHandle is a running curator server.
type CuratorServerHandle struct {
	// URL is the page address with the session token.
	URL string

	s *curatorServer
}

type curatorServer struct {
	opts      CuratorServerOptions
	callbacks CuratorServerCallbacks
	srv       *http.Server
	pageHTML  string

	mu               sync.Mutex
	browserConnected bool
	lastHeartbeatAt  time.Time
	stateChangedAt   time.Time
	clientIdleMs     *int64
	clientTimeoutSec float64
	completed        bool
	state            string // SEARCHING, RESULT_SELECTION, COMPLETED
	sse              *curatorSSEClient
	events           map[int]curatorStoredEvent
	eventOrder       []int
	searchStreamDone bool
	nextQueryIndex   int
	summarizeCancel  context.CancelFunc
	summarizeSeq     int
	stopWatchdog     chan struct{}
	stopKeepalive    chan struct{}
	watchdogRunning  bool
	keepaliveRunning bool
}

// StartCuratorServer is startCuratorServer: it listens on a free port and returns the handle.
func StartCuratorServer(opts CuratorServerOptions, callbacks CuratorServerCallbacks) (*CuratorServerHandle, error) {
	if opts.Network.Bind == "" {
		opts.Network = CuratorNetwork{Host: "localhost", Bind: "127.0.0.1"}
	}
	s := &curatorServer{
		opts: opts, callbacks: callbacks,
		lastHeartbeatAt: time.Now(), stateChangedAt: time.Now(),
		clientTimeoutSec: opts.Timeout, state: "SEARCHING",
		events:           map[int]curatorStoredEvent{},
		searchStreamDone: len(opts.Queries) == 0,
		nextQueryIndex:   len(opts.Queries),
	}
	if opts.InitialResultIndexCapacity != nil {
		s.nextQueryIndex = max(len(opts.Queries), *opts.InitialResultIndexCapacity)
	}
	s.pageHTML = GenerateCuratorPage(opts.Queries, opts.SessionToken, int(opts.Timeout), opts.AvailableProviders,
		opts.DefaultProvider, opts.SearchProvider, opts.SummaryModels, opts.DefaultSummaryModel)
	s.srv = &http.Server{Handler: http.HandlerFunc(s.handle)}

	listener, err := net.Listen("tcp", net.JoinHostPort(opts.Network.Bind, "0"))
	if err != nil {
		return nil, fmt.Errorf("Curator server failed to start: %s", listenErrorText(err))
	}
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		_ = listener.Close()
		return nil, errors.New("Curator server: invalid address")
	}
	url := "http://" + opts.Network.Host + ":" + strconv.Itoa(addr.Port) + "/?session=" + opts.SessionToken
	go func() { _ = s.srv.Serve(listener) }()
	s.mu.Lock()
	s.startWatchdogLocked()
	s.mu.Unlock()
	return &CuratorServerHandle{URL: url, s: s}, nil
}

// listenErrorText is the message of the underlying error (Node reports `err.message`).
func listenErrorText(err error) string {
	var op *net.OpError
	if errors.As(err, &op) && op.Err != nil {
		var sys *os.SyscallError
		if errors.As(op.Err, &sys) {
			return "listen " + sys.Syscall + " " + sys.Err.Error()
		}
		return "listen " + op.Err.Error()
	}
	return err.Error()
}

// ---- handle -----------------------------------------------------------------------------------

// Close ends the session; a session that was still open reports "stale" through OnCancel.
func (h *CuratorServerHandle) Close() {
	s := h.s
	s.mu.Lock()
	wasOpen := s.markCompletedLocked()
	s.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), curatorShutdownGraceDelay)
		defer cancel()
		if err := s.srv.Shutdown(ctx); err != nil {
			_ = s.srv.Close()
		}
	}()
	if wasOpen {
		s.fire(func() { s.callbacks.OnCancel("stale") })
	}
}

// PushResult streams a result entry for queryIndex (pushResult).
func (h *CuratorServerHandle) PushResult(queryIndex int, data CuratorPush) {
	s := h.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.completed {
		return
	}
	s.nextQueryIndex = max(s.nextQueryIndex, queryIndex+1)
	query := data.Query
	if query == "" && queryIndex >= 0 && queryIndex < len(s.opts.Queries) {
		query = s.opts.Queries[queryIndex]
	}
	if data.Results == nil {
		data.Results = []CuratorSource{}
	}
	ev := curatorStoredEvent{event: "result", data: curatorResultEventData{CuratorSearchEntry: data.CuratorSearchEntry, Query: query, SlotIndex: data.SlotIndex, QueryIndex: queryIndex}}
	s.retainLocked(queryIndex, ev)
	s.sendSSELocked(ev.event, ev.data)
}

// PushError streams a search error for queryIndex (pushError). query and slot may be empty/nil.
func (h *CuratorServerHandle) PushError(queryIndex int, message, provider, query string, slotIndex *int) {
	s := h.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.completed {
		return
	}
	s.nextQueryIndex = max(s.nextQueryIndex, queryIndex+1)
	if query == "" && queryIndex >= 0 && queryIndex < len(s.opts.Queries) {
		query = s.opts.Queries[queryIndex]
	}
	ev := curatorStoredEvent{event: "search-error", data: curatorErrorEventData{QueryIndex: queryIndex, Query: query, Error: message, Provider: provider, SlotIndex: slotIndex}}
	s.retainLocked(queryIndex, ev)
	s.sendSSELocked(ev.event, ev.data)
}

// SearchesDone marks the end of the search stream and moves to result selection (searchesDone).
func (h *CuratorServerHandle) SearchesDone() {
	s := h.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.completed {
		return
	}
	s.searchStreamDone = true
	s.sendSSELocked("done", struct{}{})
	s.state = "RESULT_SELECTION"
	s.stateChangedAt = time.Now()
}

// ConnectionState is getConnectionState.
func (h *CuratorServerHandle) ConnectionState() CuratorConnectionState {
	s := h.s
	s.mu.Lock()
	defer s.mu.Unlock()
	return CuratorConnectionState{BrowserConnected: s.browserConnected, LastHeartbeatAgeMs: time.Since(s.lastHeartbeatAt).Milliseconds()}
}

// ---- state (callers hold s.mu) --------------------------------------------------------------------

// fire runs a callback on its own goroutine (setImmediate); a panic in it is not allowed to take
// the process down.
func (s *curatorServer) fire(fn func()) {
	go func() {
		defer func() { _ = recover() }()
		fn()
	}()
}

func (s *curatorServer) retainLocked(queryIndex int, ev curatorStoredEvent) {
	if _, present := s.events[queryIndex]; !present {
		s.eventOrder = append(s.eventOrder, queryIndex)
	}
	s.events[queryIndex] = ev
}

func (s *curatorServer) streamedEventsLocked() []curatorStoredEvent {
	out := make([]curatorStoredEvent, 0, len(s.eventOrder))
	for _, k := range s.eventOrder {
		out = append(out, s.events[k])
	}
	return out
}

func sseFrame(event string, data any) string {
	b, err := marshalNoEscape(data)
	if err != nil {
		b = []byte("{}")
	}
	return "event: " + event + "\ndata: " + string(b) + "\n\n"
}

// sendSSELocked writes to the connected stream; a stream that cannot be written is dropped.
func (s *curatorServer) sendSSELocked(event string, data any) {
	c := s.sse
	if c == nil {
		return
	}
	if !c.write(sseFrame(event, data)) && s.sse == c {
		s.sse = nil
	}
}

func (s *curatorServer) markCompletedLocked() bool {
	if s.completed {
		return false
	}
	s.completed = true
	s.state = "COMPLETED"
	s.stateChangedAt = time.Now()
	if s.watchdogRunning {
		close(s.stopWatchdog)
		s.watchdogRunning = false
	}
	if s.keepaliveRunning {
		close(s.stopKeepalive)
		s.keepaliveRunning = false
	}
	s.abortSummarizeLocked()
	if s.sse != nil {
		s.sse.end()
		s.sse = nil
	}
	return true
}

func (s *curatorServer) abortSummarizeLocked() {
	if s.summarizeCancel != nil {
		s.summarizeCancel()
		s.summarizeCancel = nil
	}
}

func (s *curatorServer) touchHeartbeatLocked() {
	s.lastHeartbeatAt = time.Now()
	s.browserConnected = true
}

func (s *curatorServer) effectiveTimeoutMsLocked() int64 {
	return int64(max(1000, math.Floor(s.clientTimeoutSec)*1000))
}

func (s *curatorServer) idleTimedOutLocked() bool {
	return s.state == "RESULT_SELECTION" && s.clientIdleMs != nil && *s.clientIdleMs >= s.effectiveTimeoutMsLocked()
}

// startWatchdogLocked ends sessions that nobody works on (the setInterval of listen()).
func (s *curatorServer) startWatchdogLocked() {
	s.stopWatchdog = make(chan struct{})
	s.watchdogRunning = true
	stop := s.stopWatchdog
	go func() {
		t := time.NewTicker(curatorWatchdogInterval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				s.watchdogTick()
			}
		}
	}()
}

func (s *curatorServer) watchdogTick() {
	s.mu.Lock()
	if s.completed {
		s.mu.Unlock()
		return
	}
	var reason string
	switch {
	case !s.browserConnected:
		noBrowserTimeoutMs := max(5000, s.effectiveTimeoutMsLocked())
		if s.state != "RESULT_SELECTION" || time.Since(s.stateChangedAt).Milliseconds() <= noBrowserTimeoutMs {
			s.mu.Unlock()
			return
		}
		reason = "timeout"
	case s.idleTimedOutLocked():
		reason = "timeout"
	case time.Since(s.lastHeartbeatAt).Milliseconds() <= curatorStaleThresholdMs:
		s.mu.Unlock()
		return
	default:
		reason = "stale"
		if s.state == "RESULT_SELECTION" {
			reason = "timeout"
		}
	}
	if !s.markCompletedLocked() {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	s.fire(func() { s.callbacks.OnCancel(reason) })
}

// ---- requests ---------------------------------------------------------------------------------

func curatorJSON(w http.ResponseWriter, status int, payload any) {
	b, err := marshalNoEscape(payload)
	if err != nil {
		status, b = http.StatusInternalServerError, []byte(`{"ok":false,"error":"Server error"}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

type jsonObj = map[string]any

func curatorFail(w http.ResponseWriter, status int, message string) {
	curatorJSON(w, status, ordered2("ok", false, "error", message))
}

// orderedPair is a two-key JSON object with a fixed key order.
type orderedPair struct {
	k1 string
	v1 any
	k2 string
	v2 any
}

func ordered2(k1 string, v1 any, k2 string, v2 any) orderedPair { return orderedPair{k1, v1, k2, v2} }

func (p orderedPair) MarshalJSON() ([]byte, error) {
	a, err := marshalNoEscape(p.v1)
	if err != nil {
		return nil, err
	}
	b, err := marshalNoEscape(p.v2)
	if err != nil {
		return nil, err
	}
	k1, _ := marshalNoEscape(p.k1)
	k2, _ := marshalNoEscape(p.k2)
	return []byte("{" + string(k1) + ":" + string(a) + "," + string(k2) + ":" + string(b) + "}"), nil
}

var errBodyTooLarge = errors.New("Request body too large")

// readCuratorBody is parseJSONBody: at most 64 KiB of JSON.
func readCuratorBody(r *http.Request) (any, error) {
	limited := io.LimitReader(r.Body, curatorMaxBodySize+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(raw) > curatorMaxBodySize {
		return nil, errBodyTooLarge
	}
	var v any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("Invalid JSON: %s", jsonSyntaxText(err, raw))
	}
	if dec.More() {
		return nil, fmt.Errorf("Invalid JSON: Unexpected non-whitespace character after JSON at position %d", dec.InputOffset())
	}
	return v, nil
}

// jsonSyntaxText describes a decode error the way V8's JSON.parse does for the common cases.
func jsonSyntaxText(err error, raw []byte) string {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return "Unexpected end of JSON input"
	}
	var syn *json.SyntaxError
	if errors.As(err, &syn) {
		return syn.Error()
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return "Unexpected end of JSON input"
	}
	return err.Error()
}

// bodyOrFail reads the request body; on a bad body it answers and returns ok=false.
func bodyOrFail(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	v, err := readCuratorBody(r)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errBodyTooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		curatorFail(w, status, err.Error())
		return nil, false
	}
	switch body := v.(type) {
	case map[string]any:
		return body, true
	case []any:
		// An array is an object to typeof: it has no token, so the session check refuses it.
		return map[string]any{}, true
	}
	curatorFail(w, http.StatusBadRequest, "Invalid body")
	return nil, false
}

func (s *curatorServer) validateToken(w http.ResponseWriter, body map[string]any) bool {
	if token, _ := body["token"].(string); token != s.opts.SessionToken || body["token"] == nil {
		curatorFail(w, http.StatusForbidden, "Invalid session")
		return false
	}
	return true
}

// curatorProviderNames are the providers the server accepts (isAvailableProvider, in its order).
// SerpApi is not among them: the original's check has no branch for it.
var curatorProviderNames = []string{
	"all", "openai", "brave", "parallel", "parallel-mcp", "tinyfish", "search1api", "searchinfinity", "querit", "tavily",
	"firecrawl", "jina", "serpdive", "kagi", "bocha", "ollama", "searxng", "duckduckgo", "perplexity", "exa", "gemini",
	"kimi", "anysearch", "xcrawl", "xai", "mistral", "brightdata", "serpbase", "serper", "serply", "you", "baizhi", "valyu",
}

func (s *curatorServer) isAvailableProvider(provider string) bool {
	return sliceHas(curatorProviderNames, provider) && s.opts.AvailableProviders.Get(provider)
}

// normalizeSelectedIndices is normalizeSelectedIndices: integers in [0, maxExclusive), duplicates
// dropped, and at least one unless allowEmpty.
func normalizeSelectedIndices(value any, allowEmpty bool, maxExclusive int) ([]int, bool) {
	list, ok := value.([]any)
	if !ok {
		return nil, false
	}
	if !allowEmpty && len(list) == 0 {
		return nil, false
	}
	seen := map[int]bool{}
	indices := []int{}
	for _, item := range list {
		f, ok := item.(float64)
		if !ok || f != math.Trunc(f) || f < 0 || math.IsInf(f, 0) || f >= float64(maxExclusive) {
			return nil, false
		}
		n := int(f)
		if seen[n] {
			continue
		}
		seen[n] = true
		indices = append(indices, n)
	}
	return indices, true
}

// normalizeSummaryMeta is normalizeSummaryMeta of the server: the browser's summary metadata, or
// nil when it is not exactly well-formed.
func normalizeSummaryMeta(value any) *SummaryMeta {
	meta, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	var out SummaryMeta
	switch m := meta["model"].(type) {
	case nil:
		if _, present := meta["model"]; !present {
			return nil
		}
	case string:
		out.Model = &m
	default:
		return nil
	}
	durationMs, ok := meta["durationMs"].(float64)
	if !ok || math.IsNaN(durationMs) || math.IsInf(durationMs, 0) || durationMs < 0 {
		return nil
	}
	tokenEstimate, ok := meta["tokenEstimate"].(float64)
	if !ok || math.IsNaN(tokenEstimate) || math.IsInf(tokenEstimate, 0) || tokenEstimate < 0 {
		return nil
	}
	fallbackUsed, ok := meta["fallbackUsed"].(bool)
	if !ok {
		return nil
	}
	if v, present := meta["fallbackReason"]; present {
		reason, isString := v.(string)
		if !isString {
			return nil
		}
		out.FallbackReason = reason
	}
	if v, present := meta["phase"]; present {
		phase, _ := v.(string)
		if phase != "summary-model" && phase != "deterministic-fallback" {
			return nil
		}
		if phase == "deterministic-fallback" && !fallbackUsed {
			return nil
		}
		if phase == "summary-model" && fallbackUsed {
			return nil
		}
		out.Phase = phase
	}
	if v, present := meta["edited"]; present {
		edited, isBool := v.(bool)
		if !isBool {
			return nil
		}
		out.Edited = edited
	}
	out.DurationMs = int64(durationMs)
	out.TokenEstimate = int(tokenEstimate)
	out.FallbackUsed = fallbackUsed
	return &out
}

func (s *curatorServer) handle(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if p := recover(); p != nil {
			curatorJSON(w, http.StatusInternalServerError, ordered2("ok", false, "error", fmt.Sprint(p)))
		}
	}()
	path := r.URL.Path
	session := r.URL.Query().Get("session")
	sessionOK := r.URL.Query().Has("session") && session == s.opts.SessionToken
	switch {
	case r.Method == http.MethodGet && path == "/":
		if !sessionOK {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, "Invalid session")
			return
		}
		s.mu.Lock()
		s.touchHeartbeatLocked()
		s.mu.Unlock()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, s.pageHTML)
	case r.Method == http.MethodGet && path == "/events":
		s.handleEvents(w, r, sessionOK)
	case r.Method == http.MethodGet && path == "/state":
		if !sessionOK {
			curatorFail(w, http.StatusForbidden, "Invalid session")
			return
		}
		s.mu.Lock()
		s.touchHeartbeatLocked()
		payload := struct {
			OK     bool                 `json:"ok"`
			Events []curatorStoredEvent `json:"events"`
			Done   bool                 `json:"done"`
		}{true, s.streamedEventsLocked(), s.searchStreamDone}
		s.mu.Unlock()
		curatorJSON(w, http.StatusOK, payload)
	case r.Method == http.MethodPost && path == "/heartbeat":
		s.handleHeartbeat(w, r)
	case r.Method == http.MethodPost && path == "/provider":
		s.handleProvider(w, r)
	case r.Method == http.MethodPost && path == "/search":
		s.handleSearch(w, r)
	case r.Method == http.MethodPost && path == "/summarize":
		s.handleSummarize(w, r)
	case r.Method == http.MethodPost && path == "/rewrite":
		s.handleRewrite(w, r)
	case r.Method == http.MethodPost && path == "/submit":
		s.handleSubmit(w, r)
	case r.Method == http.MethodPost && path == "/cancel":
		s.handleCancel(w, r)
	default:
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, "Not found")
	}
}

func (s *curatorServer) handleEvents(w http.ResponseWriter, r *http.Request, sessionOK bool) {
	if !sessionOK {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, "Invalid session")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		curatorFail(w, http.StatusInternalServerError, "Streaming unsupported")
		return
	}
	client := &curatorSSEClient{w: w, flusher: flusher, done: make(chan struct{})}
	s.mu.Lock()
	if s.state == "COMPLETED" {
		s.mu.Unlock()
		curatorFail(w, http.StatusConflict, "No events available")
		return
	}
	if s.sse != nil {
		s.sse.end()
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	s.sse = client
	// Replay everything the stream carried so far (replaySSE).
	replayed := true
	for _, item := range s.streamedEventsLocked() {
		if !client.write(sseFrame(item.event, item.data)) {
			replayed = false
			break
		}
	}
	if replayed && s.searchStreamDone {
		client.write(sseFrame("done", struct{}{}))
	}
	if s.keepaliveRunning {
		close(s.stopKeepalive)
	}
	s.stopKeepalive = make(chan struct{})
	s.keepaliveRunning = true
	stop := s.stopKeepalive
	s.mu.Unlock()

	go func() {
		t := time.NewTicker(curatorSSEKeepaliveEvery)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-client.done:
				return
			case <-t.C:
				client.write(":keepalive\n\n")
			}
		}
	}()
	select {
	case <-client.done:
	case <-r.Context().Done():
		s.mu.Lock()
		if s.sse == client {
			s.sse = nil
		}
		s.mu.Unlock()
		client.end()
	}
}

func (s *curatorServer) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	body, ok := bodyOrFail(w, r)
	if !ok || !s.validateToken(w, body) {
		return
	}
	s.mu.Lock()
	s.touchHeartbeatLocked()
	if sec, ok := body["timeoutSec"].(float64); ok && !math.IsInf(sec, 0) && sec > 0 {
		s.clientTimeoutSec = math.Min(600, math.Floor(sec))
	}
	if idle, ok := body["idleMs"].(float64); ok && !math.IsInf(idle, 0) && idle >= 0 {
		v := int64(math.Floor(idle))
		s.clientIdleMs = &v
	}
	timedOut := s.idleTimedOutLocked()
	s.mu.Unlock()
	curatorJSON(w, http.StatusOK, ordered1("ok", true))
	if timedOut {
		s.mu.Lock()
		done := s.markCompletedLocked()
		s.mu.Unlock()
		if done {
			s.fire(func() { s.callbacks.OnCancel("timeout") })
		}
	}
}

type orderedOne struct {
	k string
	v any
}

func ordered1(k string, v any) orderedOne { return orderedOne{k, v} }

func (o orderedOne) MarshalJSON() ([]byte, error) {
	v, err := marshalNoEscape(o.v)
	if err != nil {
		return nil, err
	}
	k, _ := marshalNoEscape(o.k)
	return []byte("{" + string(k) + ":" + string(v) + "}"), nil
}

func (s *curatorServer) handleProvider(w http.ResponseWriter, r *http.Request) {
	body, ok := bodyOrFail(w, r)
	if !ok || !s.validateToken(w, body) {
		return
	}
	provider, _ := body["provider"].(string)
	if provider == "" {
		curatorFail(w, http.StatusBadRequest, "Invalid provider")
		return
	}
	if !s.isAvailableProvider(provider) {
		curatorFail(w, http.StatusBadRequest, "Provider unavailable: "+provider)
		return
	}
	s.fire(func() { s.callbacks.OnProviderChange(provider) })
	curatorJSON(w, http.StatusOK, ordered1("ok", true))
}

// searchResponse is {ok: true, ...entries[0], entries}.
type curatorSearchResponse struct {
	IndexedCuratorSearchEntry
	Entries []IndexedCuratorSearchEntry
}

func (c curatorSearchResponse) MarshalJSON() ([]byte, error) {
	head, err := marshalNoEscape(c.IndexedCuratorSearchEntry)
	if err != nil {
		return nil, err
	}
	entries, err := marshalNoEscape(c.Entries)
	if err != nil {
		return nil, err
	}
	// head is `{...}`: splice ok in front and entries behind.
	return []byte(`{"ok":true,` + string(head[1:len(head)-1]) + `,"entries":` + string(entries) + `}`), nil
}

func (s *curatorServer) handleSearch(w http.ResponseWriter, r *http.Request) {
	body, ok := bodyOrFail(w, r)
	if !ok || !s.validateToken(w, body) {
		return
	}
	s.mu.Lock()
	closed := s.state == "COMPLETED"
	s.mu.Unlock()
	if closed {
		curatorFail(w, http.StatusConflict, "Session closed")
		return
	}
	query, isString := body["query"].(string)
	if !isString || jsTrim(query) == "" {
		curatorFail(w, http.StatusBadRequest, "Invalid query")
		return
	}
	var provider *string
	if v, present := body["provider"]; present {
		p, isString := v.(string)
		if !isString || p == "" {
			curatorFail(w, http.StatusBadRequest, "Invalid provider")
			return
		}
		if !s.isAvailableProvider(p) {
			curatorFail(w, http.StatusBadRequest, "Provider unavailable: "+p)
			return
		}
		provider = &p
	}
	s.mu.Lock()
	qi := s.nextQueryIndex
	s.nextQueryIndex++
	s.touchHeartbeatLocked()
	s.mu.Unlock()
	trimmed := jsTrim(query)

	results, err := s.callbacks.OnAddSearch(trimmed, provider)
	if err == nil && len(results) == 0 {
		err = errors.New("Search returned no provider results")
	}
	if err != nil {
		message := err.Error()
		if message == "" {
			message = "Search failed"
		}
		entry := IndexedCuratorSearchEntry{
			CuratorSearchEntry: CuratorSearchEntry{Answer: "", Results: []CuratorSource{}, Error: message, Provider: s.opts.DefaultProvider},
			QueryIndex:         qi, Query: trimmed,
		}
		if provider != nil && *provider != "" {
			entry.Provider = *provider
		}
		s.callbacks.OnAddSearchResults([]IndexedCuratorSearchEntry{entry})
		curatorJSON(w, http.StatusOK, curatorSearchResponse{entry, []IndexedCuratorSearchEntry{entry}})
		return
	}
	entries := make([]IndexedCuratorSearchEntry, len(results))
	for i, result := range results {
		if result.Results == nil {
			result.Results = []CuratorSource{}
		}
		index := qi
		if i > 0 {
			s.mu.Lock()
			index = s.nextQueryIndex
			s.nextQueryIndex++
			s.mu.Unlock()
		}
		entries[i] = IndexedCuratorSearchEntry{CuratorSearchEntry: result, QueryIndex: index, Query: trimmed}
	}
	s.callbacks.OnAddSearchResults(entries)
	curatorJSON(w, http.StatusOK, curatorSearchResponse{entries[0], entries})
}

func (s *curatorServer) handleSummarize(w http.ResponseWriter, r *http.Request) {
	body, ok := bodyOrFail(w, r)
	if !ok || !s.validateToken(w, body) {
		return
	}
	s.mu.Lock()
	closed := s.state == "COMPLETED"
	capacity := s.nextQueryIndex
	s.mu.Unlock()
	if closed {
		curatorFail(w, http.StatusConflict, "Session closed")
		return
	}
	indices, valid := normalizeSelectedIndices(body["selected"], false, capacity)
	if !valid {
		curatorFail(w, http.StatusBadRequest, "Invalid selection")
		return
	}
	var model string
	if v, present := body["model"]; present {
		m, isString := v.(string)
		if !isString {
			curatorFail(w, http.StatusBadRequest, "Invalid model")
			return
		}
		model = jsTrim(m)
	}
	var feedback string
	if f, isString := body["feedback"].(string); isString {
		feedback = jsTrim(f)
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.abortSummarizeLocked()
	s.summarizeCancel = cancel
	s.summarizeSeq++
	requestID := s.summarizeSeq
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		// Only the request that owns the controller clears it (the superseding request replaced it).
		if s.summarizeSeq == requestID && s.summarizeCancel != nil {
			s.summarizeCancel = nil
		}
		s.mu.Unlock()
		cancel()
	}()

	draft, err := s.callbacks.OnSummarize(ctx, indices, model, feedback)
	if err != nil {
		message := err.Error()
		if message == "" {
			message = "Summary generation failed"
		}
		status := http.StatusInternalServerError
		if ctx.Err() != nil {
			status = http.StatusConflict
		}
		curatorFail(w, status, message)
		return
	}
	s.mu.Lock()
	superseded := requestID != s.summarizeSeq || s.completed
	s.mu.Unlock()
	if superseded {
		curatorFail(w, http.StatusConflict, "Summarize request superseded")
		return
	}
	curatorJSON(w, http.StatusOK, struct {
		OK      bool        `json:"ok"`
		Summary string      `json:"summary"`
		Meta    SummaryMeta `json:"meta"`
	}{true, draft.Summary, draft.Meta})
}

func (s *curatorServer) handleRewrite(w http.ResponseWriter, r *http.Request) {
	body, ok := bodyOrFail(w, r)
	if !ok || !s.validateToken(w, body) {
		return
	}
	s.mu.Lock()
	closed := s.state == "COMPLETED"
	s.mu.Unlock()
	if closed {
		curatorFail(w, http.StatusConflict, "Session closed")
		return
	}
	query, isString := body["query"].(string)
	if !isString || jsTrim(query) == "" {
		curatorFail(w, http.StatusBadRequest, "Invalid query")
		return
	}
	ctx := r.Context()
	s.mu.Lock()
	s.touchHeartbeatLocked()
	s.mu.Unlock()
	rewritten, err := s.callbacks.OnRewriteQuery(ctx, jsTrim(query))
	if err != nil {
		message := err.Error()
		if message == "" {
			message = "Rewrite failed"
		}
		status := http.StatusInternalServerError
		if ctx.Err() != nil {
			status = http.StatusConflict
		}
		curatorFail(w, status, message)
		return
	}
	curatorJSON(w, http.StatusOK, ordered2("ok", true, "query", rewritten))
}

func (s *curatorServer) handleSubmit(w http.ResponseWriter, r *http.Request) {
	body, ok := bodyOrFail(w, r)
	if !ok || !s.validateToken(w, body) {
		return
	}
	s.mu.Lock()
	capacity := s.nextQueryIndex
	s.mu.Unlock()
	indices, valid := normalizeSelectedIndices(body["selected"], true, capacity)
	if !valid {
		curatorFail(w, http.StatusBadRequest, "Invalid selection")
		return
	}
	var payload CuratorSubmitPayload
	payload.SelectedQueryIndices = indices
	if v, present := body["summary"]; present {
		summary, isString := v.(string)
		if !isString {
			curatorFail(w, http.StatusBadRequest, "Invalid summary")
			return
		}
		payload.Summary = jsTrim(summary)
	}
	if v, present := body["summaryMeta"]; present {
		meta := normalizeSummaryMeta(v)
		if meta == nil {
			curatorFail(w, http.StatusBadRequest, "Invalid summaryMeta")
			return
		}
		payload.SummaryMeta = meta
	}
	s.mu.Lock()
	if s.state != "SEARCHING" && s.state != "RESULT_SELECTION" {
		s.mu.Unlock()
		curatorFail(w, http.StatusConflict, "Cannot submit in current state")
		return
	}
	if !s.markCompletedLocked() {
		s.mu.Unlock()
		curatorFail(w, http.StatusConflict, "Session closed")
		return
	}
	s.mu.Unlock()
	payload.RawResults = body["rawResults"] == true
	payload.AutoApproveRemainingSearches = body["autoApproveRemainingSearches"] == true
	curatorJSON(w, http.StatusOK, ordered1("ok", true))
	s.fire(func() { s.callbacks.OnSubmit(payload) })
}

func (s *curatorServer) handleCancel(w http.ResponseWriter, r *http.Request) {
	body, ok := bodyOrFail(w, r)
	if !ok || !s.validateToken(w, body) {
		return
	}
	s.mu.Lock()
	done := s.markCompletedLocked()
	s.mu.Unlock()
	if !done {
		curatorJSON(w, http.StatusOK, ordered1("ok", true))
		return
	}
	reason := "user"
	if body["reason"] == "timeout" {
		reason = "timeout"
	}
	curatorJSON(w, http.StatusOK, ordered1("ok", true))
	s.fire(func() { s.callbacks.OnCancel(reason) })
}
