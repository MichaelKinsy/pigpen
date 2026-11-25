package ternbrowser

import (
	"fmt"
	"math"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/browser"
)

// NetworkLog implements upstream TernNetworkLog (network-log.ts), plus PointerSpace
// (pointer-space.ts) and normalizeAllowedDomains (network.ts:556).
//
// upstream: packages/coding-agent/src/tools/browser/tern/network-log.ts
// upstream: packages/coding-agent/src/tools/browser/pointer-space.ts

const (
	requestLogLimit  = 500
	heldPageReqLimit = 512
)

// RouteRule is a persistent route rule applied by the page-capture script.
// upstream: packages/coding-agent/src/tools/browser/tern/page-capture.ts:17
type RouteRule struct {
	Source        string            `json:"source"`
	Flags         string            `json:"flags"`
	ResourceTypes []string          `json:"resourceTypes,omitempty"`
	Abort         bool              `json:"abort,omitempty"`
	Delay         int               `json:"delay,omitempty"`
	Fulfill       *RouteFulfillSpec `json:"fulfill,omitempty"`
}

type RouteFulfillSpec struct {
	Status      int               `json:"status"`
	Headers     map[string]string `json:"headers,omitempty"`
	ContentType string            `json:"contentType,omitempty"`
	Body        string            `json:"body,omitempty"`
}

type RouteOptions struct {
	Abort        bool              `json:"abort,omitempty"`
	Status       *int              `json:"status,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	ContentType  string            `json:"contentType,omitempty"`
	Body         *string           `json:"body,omitempty"`
	Delay        *int              `json:"delay,omitempty"`
	ResourceType any               `json:"resourceType,omitempty"` // string or []string
}

type RouteDescription struct {
	Pattern any          `json:"pattern"`
	Options RouteOptions `json:"options"`
}

type RequestRecord struct {
	ID              string            `json:"id"`
	Seq             int               `json:"seq"`
	PageID          string            `json:"-"`
	NativeID        *int              `json:"-"`
	Frame           string            `json:"-"`
	TS              int64             `json:"ts"`
	Method          string            `json:"method"`
	URL             string            `json:"url"`
	ResourceType    string            `json:"resourceType"`
	RequestHeaders  map[string]string `json:"requestHeaders"`
	Status          *int              `json:"status,omitempty"`
	OK              *bool             `json:"ok,omitempty"`
	StatusText      string            `json:"-"`
	ResponseHeaders map[string]string `json:"responseHeaders,omitempty"`
	DurationMs      *float64          `json:"durationMs,omitempty"`
	FailureText     string            `json:"failureText,omitempty"`
	Sizes           RequestSizes      `json:"sizes"`
	SettledSeq      int               `json:"-"`
	Routed          bool              `json:"-"`
}

type RequestSizes struct {
	RequestBody  *int `json:"requestBody,omitempty"`
	ResponseBody *int `json:"responseBody,omitempty"`
}

type storedRoute struct {
	pattern    any
	patternKey string
	rule       RouteRule
	options    RouteOptions
}

type NetworkLog struct {
	mu          sync.Mutex
	product     string
	native      bool
	records     []*RequestRecord
	byPageID    map[string]*RequestRecord
	byNativeID  map[int]*RequestRecord
	inflight    map[int]struct{}
	heldPage    map[string]map[string]any
	heldOrder   []string
	routes      []storedRoute
	nextSeq     int
	settled     int
	harActive   bool
	harContent  string
	harStartSeq int
}

func NewNetworkLog(product string, native bool) *NetworkLog {
	if product == "" {
		product = "Tern"
	}
	return &NetworkLog{
		product:    product,
		native:     native,
		byPageID:   make(map[string]*RequestRecord),
		byNativeID: make(map[int]*RequestRecord),
		inflight:   make(map[int]struct{}),
		heldPage:   make(map[string]map[string]any),
		nextSeq:    1,
	}
}

func (l *NetworkLog) Cursor() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.nextSeq
}

func (l *NetworkLog) SettleCursor() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.settled
}

func (l *NetworkLog) Inflight() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.inflight)
}

func (l *NetworkLog) ForgetInflight() {
	l.mu.Lock()
	defer l.mu.Unlock()
	clear(l.inflight)
}

func headerRecord(v any) map[string]string {
	out := make(map[string]string)
	m, ok := v.(map[string]any)
	if !ok {
		return out
	}
	for k, val := range m {
		name := strings.ToLower(k)
		text := fmt.Sprintf("%v", val)
		if existing, exists := out[name]; exists {
			out[name] = existing + ", " + text
		} else {
			out[name] = text
		}
	}
	return out
}

func numVal(v any) (float64, bool) {
	f, ok := v.(float64)
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

func (l *NetworkLog) IngestPageMessage(msg map[string]any) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.native {
		l.recordPageMessageLocked(msg)
		return true
	}
	id, _ := msg["id"].(string)
	if id == "" {
		return false
	}
	if msg["kind"] == "request" {
		l.heldPage[id] = msg
		l.heldOrder = append(l.heldOrder, id)
		for len(l.heldPage) > heldPageReqLimit && len(l.heldOrder) > 0 {
			evict := l.heldOrder[0]
			l.heldOrder = l.heldOrder[1:]
			delete(l.heldPage, evict)
		}
		return false
	}
	req, ok := l.heldPage[id]
	delete(l.heldPage, id)
	if !ok || msg["local"] != true {
		return false
	}
	l.recordPageMessageLocked(req)
	l.recordPageMessageLocked(msg)
	return true
}

func (l *NetworkLog) recordPageMessageLocked(msg map[string]any) {
	id, _ := msg["id"].(string)
	ts := time.Now().UnixMilli()
	if f, ok := numVal(msg["ts"]); ok {
		ts = int64(f)
	}
	switch msg["kind"] {
	case "request":
		if id == "" {
			return
		}
		method, _ := msg["method"].(string)
		if method == "" {
			method = "GET"
		}
		u, _ := msg["url"].(string)
		rt, _ := msg["resourceType"].(string)
		if rt == "" {
			rt = "fetch"
		}
		frame, _ := msg["frame"].(string)
		bodySize := 0
		if f, ok := numVal(msg["bodySize"]); ok {
			bodySize = int(f)
		}
		l.pushLocked(&RequestRecord{
			PageID:         id,
			Frame:          frame,
			TS:             ts,
			Method:         method,
			URL:            u,
			ResourceType:   rt,
			RequestHeaders: headerRecord(msg["headers"]),
			Sizes:          RequestSizes{RequestBody: new(bodySize)},
		})
	case "response":
		entry := l.byPageID[id]
		if entry == nil {
			return
		}
		if f, ok := numVal(msg["status"]); ok {
			st := int(f)
			entry.Status = &st
			entry.OK = new(st >= 200 && st < 300)
		}
		entry.StatusText, _ = msg["statusText"].(string)
		entry.ResponseHeaders = headerRecord(msg["headers"])
		if f, ok := numVal(msg["durationMs"]); ok {
			entry.DurationMs = new(f)
		}
		entry.Routed = msg["routed"] == true
		l.settled++
		entry.SettledSeq = l.settled
		if u, ok := msg["url"].(string); ok && u != "" {
			entry.URL = u
		}
		if cl, ok := entry.ResponseHeaders["content-length"]; ok {
			if n, err := strconv.Atoi(strings.TrimSpace(cl)); err == nil && n >= 0 {
				entry.Sizes.ResponseBody = new(n)
			}
		}
	case "requestfailed":
		errStr, _ := msg["error"].(string)
		if errStr == "" {
			errStr = "request failed"
		}
		if entry := l.byPageID[id]; entry != nil {
			entry.FailureText = errStr
			if f, ok := numVal(msg["durationMs"]); ok {
				entry.DurationMs = new(f)
			}
			l.settled++
			entry.SettledSeq = l.settled
			return
		}
		u, _ := msg["url"].(string)
		rt, _ := msg["resourceType"].(string)
		if rt == "" {
			rt = "other"
		}
		l.settled++
		l.pushLocked(&RequestRecord{
			TS:             ts,
			Method:         "GET",
			URL:            u,
			ResourceType:   rt,
			RequestHeaders: map[string]string{},
			Sizes:          RequestSizes{RequestBody: new(0)},
			FailureText:    errStr,
			SettledSeq:     l.settled,
		})
	}
}

func (l *NetworkLog) IngestNavigationResponse(ev map[string]any) *RequestRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	status := 0
	if f, ok := numVal(ev["status"]); ok {
		status = int(f)
	}
	headers := headerRecord(ev["headers"])
	if mime, ok := ev["mime"].(string); ok && mime != "" {
		if _, exists := headers["content-type"]; !exists {
			headers["content-type"] = mime
		}
	}
	rt := "document"
	if mainFlag, ok := ev["main"].(bool); ok && !mainFlag {
		rt = "subdocument"
	}
	u, _ := ev["url"].(string)
	l.settled++
	return l.pushLocked(&RequestRecord{
		TS:              time.Now().UnixMilli(),
		Method:          "GET",
		URL:             u,
		ResourceType:    rt,
		RequestHeaders:  map[string]string{},
		Status:          new(status),
		OK:              new(status >= 200 && status < 300),
		ResponseHeaders: headers,
		Sizes:           RequestSizes{RequestBody: new(0)},
		SettledSeq:      l.settled,
	})
}

func (l *NetworkLog) IngestBlocked(rawURL string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.settled++
	l.pushLocked(&RequestRecord{
		TS:             time.Now().UnixMilli(),
		Method:         "GET",
		URL:            rawURL,
		ResourceType:   "document",
		RequestHeaders: map[string]string{},
		Sizes:          RequestSizes{RequestBody: new(0)},
		FailureText:    "blocked by allowed_domains",
		SettledSeq:     l.settled,
	})
}

func (l *NetworkLog) IngestNative(ev map[string]any) *RequestRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	idF, ok := numVal(ev["id"])
	if !ok {
		return nil
	}
	id := int(idF)
	ts := time.Now().UnixMilli()
	if f, ok := numVal(ev["ts"]); ok {
		ts = int64(f)
	}
	entryFor := func() *RequestRecord {
		if e := l.byNativeID[id]; e != nil {
			return e
		}
		u, _ := ev["url"].(string)
		rt := "other"
		if ev["main"] == true {
			rt = "document"
		}
		return l.pushLocked(&RequestRecord{
			NativeID:       new(id),
			TS:             ts,
			Method:         "GET",
			URL:            u,
			ResourceType:   rt,
			RequestHeaders: map[string]string{},
		})
	}
	switch ev["type"] {
	case "request":
		headers := headerRecord(ev["headers"])
		method := "GET"
		if m, ok := ev["method"].(string); ok && m != "" {
			method = strings.ToUpper(m)
		}
		u, _ := ev["url"].(string)
		if known := l.byNativeID[id]; known != nil {
			known.URL = u
			known.Method = method
			known.RequestHeaders = headers
		} else {
			rt := "other"
			if ev["main"] == true {
				rt = "document"
			}
			l.pushLocked(&RequestRecord{
				NativeID:       new(id),
				TS:             ts,
				Method:         method,
				URL:            u,
				ResourceType:   rt,
				RequestHeaders: headers,
			})
		}
		l.inflight[id] = struct{}{}
	case "response":
		entry := entryFor()
		headers := headerRecord(ev["headers"])
		if mime, ok := ev["mime"].(string); ok && mime != "" {
			if _, exists := headers["content-type"]; !exists {
				headers["content-type"] = mime
			}
		}
		st := 0
		if f, ok := numVal(ev["status"]); ok {
			st = int(f)
		}
		entry.Status = new(st)
		entry.OK = new(st >= 200 && st < 300)
		entry.ResponseHeaders = headers
		if u, ok := ev["url"].(string); ok && u != "" {
			entry.URL = u
		}
		if entry.SettledSeq == 0 {
			l.settled++
			entry.SettledSeq = l.settled
		}
	case "requestFinished":
		delete(l.inflight, id)
		entry := l.byNativeID[id]
		if entry != nil {
			if f, ok := numVal(ev["length"]); ok && (f > 0 || entry.ResponseHeaders["content-length"] != "") {
				entry.Sizes.ResponseBody = new(int(f))
			}
			if _, ok := numVal(ev["ts"]); ok {
				d := float64(max(0, ts-entry.TS))
				entry.DurationMs = new(d)
			}
		}
	case "requestFailed":
		entry := entryFor()
		delete(l.inflight, id)
		errStr, _ := ev["error"].(string)
		if errStr == "" {
			errStr = "request failed"
		}
		entry.FailureText = errStr
		if entry.SettledSeq == 0 {
			l.settled++
			entry.SettledSeq = l.settled
		}
		return entry
	}
	return nil
}

func (l *NetworkLog) SettledAfter(settleCursor int) []*RequestRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []*RequestRecord
	for _, r := range l.records {
		if r.SettledSeq > settleCursor {
			cp := *r
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SettledSeq < out[j].SettledSeq })
	return out
}

func (l *NetworkLog) Requests(limit int, clearAfter bool) []RequestRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	src := l.records
	if limit > 0 && len(src) > limit {
		src = src[len(src)-limit:]
	}
	out := make([]RequestRecord, len(src))
	for i, r := range src {
		out[i] = *r
	}
	if clearAfter {
		l.clearLocked()
	}
	return out
}

func (l *NetworkLog) FindRequest(id string) (*RequestRecord, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, r := range l.records {
		if r.ID == id {
			cp := *r
			return &cp, true
		}
	}
	return nil, false
}

func (l *NetworkLog) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.clearLocked()
}

func (l *NetworkLog) clearLocked() {
	l.records = nil
	clear(l.byPageID)
	clear(l.byNativeID)
}

func (l *NetworkLog) pushLocked(r *RequestRecord) *RequestRecord {
	seq := l.nextSeq
	l.nextSeq++
	r.Seq = seq
	r.ID = fmt.Sprintf("request-%d", seq)
	l.records = append(l.records, r)
	if r.PageID != "" {
		l.byPageID[r.PageID] = r
	}
	if r.NativeID != nil {
		l.byNativeID[*r.NativeID] = r
	}
	for len(l.records) > requestLogLimit {
		rem := l.records[0]
		l.records = l.records[1:]
		if rem.PageID != "" {
			delete(l.byPageID, rem.PageID)
		}
		if rem.NativeID != nil {
			delete(l.byNativeID, *rem.NativeID)
		}
	}
	return r
}

var routableTypes = map[string]bool{"fetch": true, "xhr": true}

// Route registers a persistent fetch/XHR route accepting a glob string or a
// serialized RegExp `{__omp_re: {source, flags}}` (N1, N4).
// upstream: packages/coding-agent/src/tools/browser/tern/network-log.ts:449-469
func (l *NetworkLog) Route(pattern any, opts RouteOptions) error {
	var src, flags, key string
	var descPattern any
	switch p := pattern.(type) {
	case string:
		if p == "" {
			return fmt.Errorf("tab.route() requires a non-empty URL pattern")
		}
		src = globToRegexSource(p)
		flags = ""
		key = "s:" + p
		descPattern = p
	case map[string]any:
		reSrc, reFlags, ok := browser.DecodePreludeRegExp(p)
		if !ok {
			return fmt.Errorf("tab.route() requires a URL glob string or RegExp")
		}
		src = reSrc
		flags = stripGYFlags(reFlags)
		key = "r:" + reSrc + "/" + reFlags
		descPattern = map[string]string{"source": reSrc, "flags": reFlags}
	default:
		return fmt.Errorf("tab.route() requires a URL glob string or RegExp")
	}

	var types []string
	switch rt := opts.ResourceType.(type) {
	case string:
		if rt != "" {
			types = []string{rt}
		}
	case []string:
		types = rt
	case []any:
		for _, item := range rt {
			if s, ok := item.(string); ok && s != "" {
				types = append(types, s)
			}
		}
	}
	var unroutable []string
	for _, t := range types {
		if !routableTypes[t] {
			unroutable = append(unroutable, t)
		}
	}
	if len(unroutable) > 0 {
		return fmt.Errorf(
			"tab.route() is not supported on the %s browser backend for resourceType %s: only the page's fetch and xhr requests can be routed",
			l.product,
			strings.Join(unroutable, ", "),
		)
	}
	if opts.Delay != nil && *opts.Delay < 0 {
		return fmt.Errorf("tab.route() delay must be a non-negative number")
	}
	if opts.Status != nil && (*opts.Status < 100 || *opts.Status > 599) {
		return fmt.Errorf("tab.route() status must be an integer from 100 through 599")
	}
	if _, err := regexp.Compile(src); err != nil {
		return fmt.Errorf("invalid route pattern %v: %w", pattern, err)
	}
	rule := RouteRule{
		Source:        src,
		Flags:         flags,
		ResourceTypes: types,
		Abort:         opts.Abort,
	}
	if opts.Delay != nil && *opts.Delay > 0 {
		rule.Delay = *opts.Delay
	}
	if opts.Status != nil || opts.Body != nil || opts.ContentType != "" || len(opts.Headers) > 0 {
		st := 200
		if opts.Status != nil {
			st = *opts.Status
		}
		f := &RouteFulfillSpec{
			Status:      st,
			Headers:     opts.Headers,
			ContentType: opts.ContentType,
		}
		if opts.Body != nil {
			f.Body = *opts.Body
		}
		rule.Fulfill = f
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.routes = append(l.routes, storedRoute{pattern: descPattern, patternKey: key, rule: rule, options: opts})
	return nil
}

func (l *NetworkLog) Unroute(pattern any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if pattern == nil {
		l.routes = nil
		return
	}
	var key string
	switch p := pattern.(type) {
	case string:
		if p == "" {
			l.routes = nil
			return
		}
		key = "s:" + p
	case map[string]any:
		if src, flags, ok := browser.DecodePreludeRegExp(p); ok {
			key = "r:" + src + "/" + flags
		} else {
			return
		}
	default:
		return
	}
	kept := l.routes[:0]
	for _, r := range l.routes {
		if r.patternKey != key {
			kept = append(kept, r)
		}
	}
	l.routes = kept
}

func (l *NetworkLog) Routes() []RouteDescription {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]RouteDescription, len(l.routes))
	for i, r := range l.routes {
		out[i] = RouteDescription{Pattern: r.pattern, Options: r.options}
	}
	return out
}

func (l *NetworkLog) Rules() []RouteRule {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]RouteRule, len(l.routes))
	for i, r := range l.routes {
		out[i] = r.rule
	}
	return out
}

func (l *NetworkLog) HarStart(content string) error {
	if content == "" {
		content = "none"
	}
	if content != "none" && content != "text" && content != "all" {
		return fmt.Errorf(`tab.harStart() content must be "text", "all", or "none"`)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.harActive {
		return fmt.Errorf("A HAR recording is already active on this tab")
	}
	l.harActive = true
	l.harContent = content
	l.harStartSeq = l.nextSeq
	return nil
}

func (l *NetworkLog) HarStop() (map[string]any, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.harActive {
		return nil, fmt.Errorf("No HAR recording is active on this tab")
	}
	start := l.harStartSeq
	l.harActive = false
	entries := make([]map[string]any, 0)
	for _, rec := range l.records {
		if rec.Seq < start {
			continue
		}
		status := 0
		if rec.Status != nil {
			status = *rec.Status
		}
		statusText := rec.StatusText
		if statusText == "" {
			statusText = rec.FailureText
		}
		var reqHeaders, respHeaders []map[string]string
		for k, v := range rec.RequestHeaders {
			reqHeaders = append(reqHeaders, map[string]string{"name": k, "value": v})
		}
		for k, v := range rec.ResponseHeaders {
			respHeaders = append(respHeaders, map[string]string{"name": k, "value": v})
		}
		bodySize := -1
		if rec.Sizes.ResponseBody != nil {
			bodySize = *rec.Sizes.ResponseBody
		}
		wait := 0.0
		if rec.DurationMs != nil {
			wait = *rec.DurationMs
		}
		entries = append(entries, map[string]any{
			"startedDateTime": time.UnixMilli(rec.TS).UTC().Format(time.RFC3339Nano),
			"time":            wait,
			"request": map[string]any{
				"method":      rec.Method,
				"url":         rec.URL,
				"httpVersion": "HTTP/1.1",
				"headers":     reqHeaders,
				"queryString": []any{},
				"cookies":     []any{},
				"headersSize": -1,
				"bodySize":    0,
			},
			"response": map[string]any{
				"status":      status,
				"statusText":  statusText,
				"httpVersion": "HTTP/1.1",
				"headers":     respHeaders,
				"cookies":     []any{},
				"content":     map[string]any{"size": max(0, bodySize), "mimeType": rec.ResponseHeaders["content-type"]},
				"redirectURL": rec.ResponseHeaders["location"],
				"headersSize": -1,
				"bodySize":    bodySize,
			},
			"cache":   map[string]any{},
			"timings": map[string]any{"blocked": 0, "dns": -1, "connect": -1, "send": 0, "wait": wait, "receive": 0, "ssl": -1},
		})
	}
	return map[string]any{
		"log": map[string]any{
			"version": "1.2",
			"creator": map[string]any{"name": "omp-browser", "version": "1"},
			"pages":   []any{},
			"entries": entries,
		},
	}, nil
}

// NormalizeAllowedDomains validates and normalizes allowed_domains.
// upstream: packages/coding-agent/src/tools/browser/network.ts:556-576
func NormalizeAllowedDomains(domains []string) ([]string, error) {
	out := make([]string, 0, len(domains))
	for _, raw := range domains {
		d := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
		if d == "" || strings.Contains(d, "://") || strings.Contains(d, "/") ||
			(strings.Contains(d, "*") && !strings.HasPrefix(d, "*.")) {
			return nil, fmt.Errorf("Invalid allowed_domains pattern %s", jsonQuote(raw))
		}
		suffix := strings.TrimPrefix(d, "*.")
		if suffix == "" || strings.Contains(suffix, "*") {
			return nil, fmt.Errorf("Invalid allowed_domains pattern %s", jsonQuote(raw))
		}
		if !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	return out, nil
}

// SameAddress reports whether requested and current refer to the same URL.
// upstream: packages/coding-agent/src/tools/browser/navigation.ts:8-15
func SameAddress(requested, current string) bool {
	if requested == current {
		return true
	}
	u1, err1 := url.Parse(requested)
	u2, err2 := url.Parse(current)
	if err1 != nil || err2 != nil || u1.Scheme == "" || u2.Scheme == "" {
		return false
	}
	if u1.Path == "" {
		u1.Path = "/"
	}
	if u2.Path == "" {
		u2.Path = "/"
	}
	return u1.String() == u2.String()
}

// globToRegexSource converts a URL glob (`*` within a segment, `**` across
// segments, `?` literal) to an anchored RegExp source.
// upstream: packages/coding-agent/src/tools/browser/network.ts:684-696
func globToRegexSource(glob string) string {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(glob); i++ {
		switch glob[i] {
		case '*':
			if i+1 < len(glob) && glob[i+1] == '*' {
				b.WriteString(".*")
				i++
			} else {
				b.WriteString("[^/]*")
			}
		case '\\', '^', '$', '.', '+', '?', '(', ')', '[', ']', '{', '}', '|':
			b.WriteByte('\\')
			b.WriteByte(glob[i])
		default:
			b.WriteByte(glob[i])
		}
	}
	b.WriteString("$")
	return b.String()
}

// urlPatternRegex widens a lone leading or trailing `*` to `**` before calling
// globToRegexSource (N4).
// upstream: packages/coding-agent/src/tools/browser/network.ts:703-706
func urlPatternRegex(glob string) (*regexp.Regexp, error) {
	widened := glob
	if strings.HasPrefix(widened, "*") && !strings.HasPrefix(widened, "**") {
		widened = "*" + widened
	}
	if strings.HasSuffix(widened, "*") && !strings.HasSuffix(widened, "**") {
		widened = widened + "*"
	}
	return regexp.Compile(globToRegexSource(widened))
}

// compileURLMatcher compiles a string (substring or `*` glob with N4 boundary
// widening and literal `?`) or a serialized `{__omp_re: {source, flags}}` (N1).
// upstream: packages/coding-agent/src/tools/browser/network.ts:703-716
func compileURLMatcher(raw any) (func(string) bool, error) {
	switch v := raw.(type) {
	case string:
		if !strings.Contains(v, "*") {
			return func(u string) bool { return strings.Contains(u, v) }, nil
		}
		re, err := urlPatternRegex(v)
		if err != nil {
			return nil, fmt.Errorf("invalid URL glob %s: %w", jsonQuote(v), err)
		}
		return re.MatchString, nil
	case map[string]any:
		if src, flags, ok := browser.DecodePreludeRegExp(v); ok {
			prefix := ""
			if strings.Contains(flags, "i") {
				prefix += "i"
			}
			if strings.Contains(flags, "m") {
				prefix += "m"
			}
			if strings.Contains(flags, "s") {
				prefix += "s"
			}
			expr := src
			if prefix != "" {
				expr = "(?" + prefix + ")" + src
			}
			re, err := regexp.Compile(expr)
			if err != nil {
				return nil, fmt.Errorf("invalid URL regexp %s: %w", jsonQuote(src), err)
			}
			return re.MatchString, nil
		}
		if _, isFn := browser.DecodePreludeFn(v); isFn {
			return nil, fmt.Errorf("tab.waitForUrl() takes a string the URL contains, a glob with * (**/path), or a RegExp it matches, not a function")
		}
		return nil, fmt.Errorf("tab.waitForUrl() takes a string the URL contains, a glob with * (**/path), or a RegExp it matches, not object")
	case nil:
		return nil, fmt.Errorf("tab.waitForUrl() takes a string the URL contains, a glob with * (**/path), or a RegExp it matches, not null")
	default:
		return nil, fmt.Errorf("tab.waitForUrl() takes a string the URL contains, a glob with * (**/path), or a RegExp it matches, not %T", raw)
	}
}

func stripGYFlags(flags string) string {
	var b strings.Builder
	for _, r := range flags {
		if r != 'g' && r != 'y' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// PointerFrame and PointerSpace map screenshot coordinates to viewport CSS px.
// upstream: packages/coding-agent/src/tools/browser/pointer-space.ts:23-91
type PointerFrame struct {
	ViewportW int
	ViewportH int
	Left      float64
	Top       float64
	Width     float64
	Height    float64
}

type PointerSpace struct {
	frame    *PointerFrame
	resizedW int
	resizedH int
	hasSize  bool
}

func (p *PointerSpace) Use(frame *PointerFrame) {
	p.frame = frame
	p.hasSize = false
}

func (p *PointerSpace) Resized(w, h int) {
	p.resizedW = w
	p.resizedH = h
	p.hasSize = true
}

func (p *PointerSpace) ToViewport(x, y float64, css bool) (float64, float64, error) {
	if p.frame == nil || css {
		return x, y, nil
	}
	if p.hasSize && (absInt(p.resizedW-p.frame.ViewportW) > 1 || absInt(p.resizedH-p.frame.ViewportH) > 1) {
		return 0, 0, fmt.Errorf(
			"The viewport is %dx%d now, and the last screenshot shows it at %dx%d: pointer positions are read on that image. Take a new screenshot, or pass { css: true } to give viewport CSS pixels.",
			p.resizedW, p.resizedH, p.frame.ViewportW, p.frame.ViewportH,
		)
	}
	vx := ((x - p.frame.Left) * float64(p.frame.ViewportW)) / p.frame.Width
	vy := ((y - p.frame.Top) * float64(p.frame.ViewportH)) / p.frame.Height
	return vx, vy, nil
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
