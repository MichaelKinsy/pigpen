package langsmith

// Batch export of client.js (langsmith 0.10.5): _processBatch, multipartIngestRuns and its body assembly,
// _sendMultipartRequest, batchIngestRuns / _postBatchIngestRuns, and the failed-trace fallback directory.
//
// The multipart body is assembled once, in one pooled buffer, in its final wire form (boundary lines, part
// headers, payloads). The streamed (chunked) body, the buffered body, the gzip stream and the fallback file all read
// that buffer through a segment cursor; no second copy of the body is made. Each part's header is written after
// its payload (the Content-Type carries the payload length) and the segment list puts them in wire order.

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"math"
	mrand "math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// ---- pooled buffers ----

const maxPooledBuf = 4 << 20

type seg struct{ off, end int }

// buf is a pooled byte buffer with the segment list of a multipart body.
type buf struct {
	b    []byte
	segs []seg
}

var bufPool = sync.Pool{New: func() any { return &buf{b: make([]byte, 0, 8<<10)} }}

func getBuf() *buf {
	x := bufPool.Get().(*buf)
	x.b = x.b[:0]
	x.segs = x.segs[:0]
	return x
}

func putBuf(x *buf) {
	if cap(x.b) > maxPooledBuf {
		return
	}
	bufPool.Put(x)
}

// serializeForTracing is serialize() of utils/fast-safe-stringify (index.js:263-306): JSON.stringify of v appended
// to dst. A value JSON.stringify throws on is replaced by "[Unserializable]" after a warning (index.js:268-270);
// the circular-structure branch cannot occur for Go values.
func serializeForTracing(dst []byte, v any, errorContext func() string) []byte {
	if isUndef(v) {
		return append(dst, "undefined"...) // encodeString(undefined)
	}
	start := len(dst)
	out, err := appendValue(dst, v)
	if err != nil {
		warnf("[WARNING]: LangSmith received unserializable value.\nContext: %s", errorContext())
		return append(out[:start], "[Unserializable]"...)
	}
	return out
}

// ---- processBatch ----

// sendOpts is the `options` of multipartIngestRuns / batchIngestRuns: destination, gzip and the batch size.
type sendOpts struct {
	apiURL, apiKey, workspaceID *string
	useGzip                     bool
	sizeBytes                   int64
}

// processBatch is _processBatch (client.js:1490-1552) without the OTEL branch (OTEL mode is refused at construction).
// The worker that runs it does the server-info wait and the body assembly (the CPU part); the send, which is
// where the original hands the batch to batchIngestCaller.callWithOptions({sizeBytes}) and so reserves its bytes in
// the ingest budget, runs on a goroutine of its own (goSend). Every assembled batch therefore reaches the budget
// check at once, as in the original, and the goroutines that exist are the ones the budget admitted plus the refused
// calls that return immediately.
func (c *client) processBatch(j batchJob) {
	if len(j.items) == 0 {
		c.jobDone(j)
		return
	}
	var batchSizeBytes int64
	var creates, updates []*omap
	for _, it := range j.items {
		batchSizeBytes += it.size
		if it.action == actionCreate {
			creates = append(creates, it.payload)
		} else {
			updates = append(updates, it.payload)
		}
	}
	ctx := c.ctx
	si, err := c.ensureServerInfo(ctx)
	if err != nil {
		c.finishBatch(j, err)
		return
	}
	o := sendOpts{apiURL: j.dest.APIURL, apiKey: j.dest.APIKey, workspaceID: j.dest.WorkspaceID, sizeBytes: batchSizeBytes}
	if c.multipartDisabled.Load() || !jsTruthy(si.useMultipart) {
		raw := c.buildBatchBody(creates, updates)
		if raw == nil {
			c.finishBatch(j, nil)
			return
		}
		c.goSend(j, func() error {
			defer raw.release()
			return c.postBatchBody(ctx, raw, o)
		})
		return
	}
	o.useGzip = !c.runCompressionDisabled && jsTruthy(si.gzipEnabled)
	mp, err := c.buildMultipart(creates, updates)
	if err != nil || mp == nil {
		c.finishBatch(j, err)
		return
	}
	c.goSend(j, func() error {
		err := c.sendMultipartRequest(ctx, mp, o)
		mp.release()
		if err != nil && isLangSmithNotFoundError(err) {
			// Fallback to batch ingest if the multipart endpoint returns 404; disable multipart for future requests.
			c.multipartDisabled.Store(true)
			o.useGzip = false
			return c.batchIngestRuns(ctx, creates, updates, o)
		}
		return err
	})
}

// goSend runs the send half of a batch on its own goroutine (tracked by the client's WaitGroup); on a closed client
// it runs inline, where the cancelled context ends it at once.
func (c *client) goSend(j batchJob, send func() error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		c.finishBatch(j, send())
		return
	}
	c.wg.Add(1)
	c.mu.Unlock()
	go func() {
		defer c.wg.Done()
		c.finishBatch(j, send())
	}()
}

// finishBatch is the catch of _processBatch (client.js:1549-1551) followed by the resolution of the batch.
func (c *client) finishBatch(j batchJob, err error) {
	if err != nil && c.ctx.Err() == nil {
		errorf("Error exporting batch: %s", errString(err))
	}
	c.jobDone(j)
}

// mergeCreatesAndUpdates is the createById reduce of batchIngestRuns / multipartIngestRuns (client.js:1786-1808,
// 1900-1922): an update whose id matches a create is spread over it; the rest stay standalone. A create without a
// truthy id is dropped by the reduce (client.js:1788), which is kept. The merge happens in place: the items are
// owned by the batch and the spread is idempotent.
func mergeCreatesAndUpdates(creates, updates []*omap) ([]*omap, []*omap) {
	if len(creates) == 0 || len(updates) == 0 {
		return creates, updates
	}
	order := make([]string, 0, len(creates))
	byID := make(map[string]*omap, len(creates))
	for _, r := range creates {
		id := r.val("id")
		if !jsTruthy(id) {
			continue
		}
		k := jsString(id)
		if _, ok := byID[k]; !ok {
			order = append(order, k)
		}
		byID[k] = r
	}
	var standalone []*omap
	for _, u := range updates {
		if id := u.val("id"); !isUndef(id) {
			if cr, ok := byID[jsString(id)]; ok {
				cr.spread(u)
				continue
			}
		}
		standalone = append(standalone, u)
	}
	merged := make([]*omap, 0, len(order))
	for _, k := range order {
		merged = append(merged, byID[k])
	}
	return merged, standalone
}

// ---- multipart ----

type multipartBody struct {
	buf      *buf
	total    int64
	boundary string
	context  string
	refs     atomic.Int32
}

func (mp *multipartBody) release() {
	if mp.refs.Add(-1) == 0 {
		putBuf(mp.buf)
		mp.buf = nil
	}
}

// segCursor walks the segments of the body without copying.
type segCursor struct {
	mp  *multipartBody
	i   int
	off int
}

// next returns up to max bytes of the body, or nil at its end.
func (c *segCursor) next(max int) []byte {
	segs := c.mp.buf.segs
	for c.i < len(segs) {
		s := segs[c.i]
		start := s.off + c.off
		remaining := s.end - start
		if remaining <= 0 {
			c.i++
			c.off = 0
			continue
		}
		n := min(remaining, max)
		c.off += n
		if start+n >= s.end {
			c.i++
			c.off = 0
		}
		return c.mp.buf.b[start : start+n]
	}
	return nil
}

// bodyReader is the request body of one attempt; Close may race with a Read the transport still runs.
type bodyReader struct {
	mu     sync.Mutex
	mp     *multipartBody
	cur    segCursor
	closed bool
	// gzip (streamed attempt with gzip_body_enabled): the compressed bytes are produced on demand.
	zw   *gzip.Writer
	out  bytes.Buffer
	done bool
}

var gzPool = sync.Pool{New: func() any {
	zw, _ := gzip.NewWriterLevel(io.Discard, flate.DefaultCompression) // CompressionStream("gzip") default level
	return zw
}}

func (mp *multipartBody) newReader(compress bool) *bodyReader {
	mp.refs.Add(1)
	r := &bodyReader{mp: mp, cur: segCursor{mp: mp}}
	if compress {
		r.zw = gzPool.Get().(*gzip.Writer)
		r.zw.Reset(&r.out)
	}
	return r
}

func (r *bodyReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return 0, errors.New("http: read on closed request body")
	}
	if len(p) == 0 {
		return 0, nil
	}
	if r.zw == nil {
		piece := r.cur.next(len(p))
		if piece == nil {
			return 0, io.EOF
		}
		return copy(p, piece), nil
	}
	for r.out.Len() == 0 {
		if r.done {
			return 0, io.EOF
		}
		piece := r.cur.next(32 << 10)
		if piece == nil {
			r.zw.Close()
			r.done = true
			continue
		}
		r.zw.Write(piece)
	}
	return r.out.Read(p)
}

func (r *bodyReader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	if r.zw != nil {
		r.zw.Reset(io.Discard)
		gzPool.Put(r.zw)
		r.zw = nil
	}
	r.mp.release()
	return nil
}

// randomBase36 is `Math.random().toString(36).slice(2)`.
func randomBase36() string {
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	var out [11]byte
	r := mrand.Float64()
	n := 0
	for n < len(out) && r > 0 {
		r *= 36
		d := int(math.Floor(r))
		r -= float64(d)
		out[n] = digits[d]
		n++
	}
	if n == 0 {
		return "0"
	}
	return string(out[:n])
}

// payloadOnlyKeys are the fields multipartIngestRuns sends as parts of their own (client.js:1936).
func isFieldKey(k string) bool {
	switch k {
	case "inputs", "outputs", "events", "extra", "error", "serialized", "attachments":
		return true
	}
	return false
}

var multipartFieldKeys = [...]string{"inputs", "outputs", "events", "extra", "error", "serialized"}

// appendMainPayload is the JSON of `{...rest}` after `{inputs, outputs, events, extra, error, serialized,
// attachments, ...rest} = payload`.
func appendMainPayload(dst []byte, m *omap) ([]byte, error) {
	dst = append(dst, '{')
	first := true
	for _, k := range m.ownKeys() {
		if isFieldKey(k) {
			continue
		}
		val := m.vals[k]
		if isUndef(val) {
			continue
		}
		if !first {
			dst = append(dst, ',')
		}
		first = false
		dst = json.AppendStringifyString(dst, k)
		dst = append(dst, ':')
		var err error
		if dst, err = appendValue(dst, val); err != nil {
			return dst, err
		}
	}
	return append(dst, '}'), nil
}

// addPart appends one part: payload bytes at [start, end) are already in the buffer; the header goes after them
// and the segments are recorded in wire order.
func (mp *multipartBody) addPart(start, end int, method, id, key, contentType string, withLength bool, lengthText string) {
	b := mp.buf.b
	hs := len(b)
	b = append(b, "--"...)
	b = append(b, mp.boundary...)
	b = append(b, "\r\nContent-Disposition: form-data; name=\""...)
	b = append(b, method...)
	b = append(b, '.')
	b = append(b, id...)
	if key != "" {
		b = append(b, '.')
		b = append(b, key...)
	}
	b = append(b, "\"\r\nContent-Type: "...)
	b = append(b, contentType...)
	b = append(b, "; length="...)
	if withLength {
		b = strconv.AppendInt(b, int64(end-start), 10)
	} else {
		b = append(b, lengthText...)
	}
	b = append(b, "\r\n\r\n"...)
	he := len(b)
	mp.buf.b = b
	mp.buf.segs = append(mp.buf.segs, seg{hs, he}, seg{start, end}, seg{0, 2})
	mp.total += int64(he-hs) + int64(end-start) + 2
}

// multipartIngestRuns is client.js:1866-1994.
func (c *client) multipartIngestRuns(ctx context.Context, creates, updates []*omap, o sendOpts) error {
	mp, err := c.buildMultipart(creates, updates)
	if err != nil || mp == nil {
		return err
	}
	defer mp.release()
	return c.sendMultipartRequest(ctx, mp, o)
}

// buildMultipart prepares the runs and assembles the multipart body (client.js:1866-1992); nil means there is
// nothing to send. The caller releases the body.
func (c *client) buildMultipart(creates, updates []*omap) (*multipartBody, error) {
	var allAttachments map[string]any
	for _, cr := range creates {
		c.prepareRunInPlace(cr)
		if id, att := cr.val("id"), cr.val("attachments"); !isUndef(id) && !isUndef(att) {
			if allAttachments == nil {
				allAttachments = map[string]any{}
			}
			allAttachments[jsString(id)] = att
		}
	}
	for _, u := range updates {
		c.prepareRunInPlace(u)
	}
	// require trace_id and dotted_order
	for _, cr := range creates {
		if isUndef(cr.val("trace_id")) || isUndef(cr.val("dotted_order")) {
			return nil, errors.New(`Multipart ingest requires "trace_id" and "dotted_order" to be set when creating a run`)
		}
	}
	for _, u := range updates {
		if isUndef(u.val("trace_id")) || isUndef(u.val("dotted_order")) {
			return nil, errors.New(`Multipart ingest requires "trace_id" and "dotted_order" to be set when updating a run`)
		}
	}
	creates, updates = mergeCreatesAndUpdates(creates, updates)
	if len(creates) == 0 && len(updates) == 0 {
		return nil, nil
	}

	mp := &multipartBody{buf: getBuf(), boundary: "----LangSmithFormBoundary" + randomBase36()}
	mp.refs.Store(1)
	mp.buf.b = append(mp.buf.b, "\r\n"...) // the segment every part ends with
	var ctxText []byte
	for _, group := range [...]struct {
		method   string
		payloads []*omap
	}{{"post", creates}, {"patch", updates}} {
		for _, payload := range group.payloads {
			id := jsString(payload.val("id"))
			// the main run payload
			start := len(mp.buf.b)
			out, err := appendMainPayload(mp.buf.b, payload)
			if err != nil {
				warnf("[WARNING]: LangSmith received unserializable value.\nContext: Serializing for multipart ingestion of run with id: %s", id)
				out = append(out[:start], "[Unserializable]"...)
			}
			mp.buf.b = out
			mp.addPart(start, len(out), group.method, id, "", "application/json", true, "")
			// the fields collected as separate parts
			for _, key := range multipartFieldKeys {
				value := payload.val(key)
				if isUndef(value) {
					continue
				}
				start := len(mp.buf.b)
				mp.buf.b = serializeForTracing(mp.buf.b, value, func() string {
					return "Serializing " + key + " for multipart ingestion of run with id: " + id
				})
				mp.addPart(start, len(mp.buf.b), group.method, id, key, "application/json", true, "")
			}
			// the attachments
			if allAttachments != nil && !isUndef(payload.val("id")) {
				if att, ok := allAttachments[id]; ok {
					delete(allAttachments, id)
					mp.addAttachments(group.method, id, att)
				}
			}
			if len(ctxText) > 0 {
				ctxText = append(ctxText, "; "...)
			}
			ctxText = append(ctxText, "trace="...)
			ctxText = append(ctxText, jsString(payload.val("trace_id"))...)
			ctxText = append(ctxText, ",id="...)
			ctxText = append(ctxText, id...)
		}
	}
	mp.context = string(ctxText)
	closeStart := len(mp.buf.b)
	mp.buf.b = append(mp.buf.b, "--"...)
	mp.buf.b = append(mp.buf.b, mp.boundary...)
	mp.buf.b = append(mp.buf.b, "--\r\n"...)
	mp.buf.segs = append(mp.buf.segs, seg{closeStart, len(mp.buf.b)})
	mp.total += int64(len(mp.buf.b) - closeStart)
	return mp, nil
}

// addAttachments is the attachment loop of client.js:1960-1988 for the value shapes `[contentType, content]` and
// `{mimeType, data}`; the name must not contain a period.
func (mp *multipartBody) addAttachments(method, id string, attachments any) {
	m, ok := toOmap(attachments)
	if !ok {
		return
	}
	for _, name := range m.ownKeys() {
		var contentType string
		var content any
		switch a := m.val(name).(type) {
		case []any:
			if len(a) > 0 {
				contentType = jsString(a[0])
			} else {
				contentType = "undefined"
			}
			content = undef
			if len(a) > 1 {
				content = a[1]
			}
		default:
			rec, _ := toOmap(a)
			contentType, content = jsString(rec.val("mimeType")), rec.val("data")
		}
		if strings.Contains(name, ".") {
			warnf("Skipping attachment '%s' for run %s: Invalid attachment name. Attachment names must not contain periods ('.'). Please rename the attachment and try again.", name, id)
			continue
		}
		start := len(mp.buf.b)
		length := "undefined" // a string has no byteLength
		switch x := content.(type) {
		case []byte:
			mp.buf.b = append(mp.buf.b, x...)
			length = strconv.Itoa(len(x))
		default:
			mp.buf.b = append(mp.buf.b, jsString(x)...)
		}
		// part name `attachment.<id>.<name>` (the method prefix is not used for attachments)
		hs := len(mp.buf.b)
		b := append(mp.buf.b, "--"...)
		b = append(b, mp.boundary...)
		b = append(b, "\r\nContent-Disposition: form-data; name=\"attachment."...)
		b = append(b, id...)
		b = append(b, '.')
		b = append(b, name...)
		b = append(b, "\"\r\nContent-Type: "...)
		b = append(b, contentType...)
		b = append(b, "; length="...)
		b = append(b, length...)
		b = append(b, "\r\n\r\n"...)
		mp.buf.b = b
		mp.buf.segs = append(mp.buf.segs, seg{hs, len(b)}, seg{start, hs}, seg{0, 2})
		mp.total += int64(len(b)-hs) + int64(hs-start) + 2
	}
}

// sendMultipartRequest is _sendMultipartRequest (client.js:2058-2136). The original's 422 retry with a buffered
// body (2110-2120) tests `res.status === 422` on a response raiseForStatus already accepted, so it can never
// run; it is not ported (port/lane-client.md, finding C1).
func (c *client) sendMultipartRequest(ctx context.Context, mp *multipartBody, o sendOpts) error {
	streamed := !c.multipartStreamingDisabled.Load()
	_, err := c.batchCaller.callWithOptions(ctx, o.sizeBytes, func(ctx context.Context) (*http.Response, error) {
		h, err := c.mergedHeaders()
		if err != nil {
			return nil, err
		}
		h.Set("Content-Type", "multipart/form-data; boundary="+mp.boundary)
		withDest(h, o.apiKey, o.workspaceID)
		// Only a stream is compressed (client.js:2077-2082): a buffered body is sent as is.
		gz := o.useGzip && streamed
		if gz {
			h.Set("Content-Encoding", "gzip")
		}
		length := mp.total
		if streamed {
			length = -1 // chunked
		}
		url := c.apiURL
		if o.apiURL != nil {
			url = *o.apiURL
		}
		return c.attempt(ctx, c.timeout, "POST", url+"/runs/multipart", h, mp.newReader(gz), length, "Failed to send multipart request", true, nil)
	})
	if err == nil {
		return nil
	}
	// Re-throw 404 errors so the caller can fall back to batch ingest.
	if isLangSmithNotFoundError(err) {
		return err
	}
	warnf("%s\n\nContext: %s", jsTrim(err.Error()), mp.context)
	if c.failedTracesDir != "" {
		c.writeTraceToFallbackDir(c.failedTracesDir, mp, "runs/multipart", c.failedTracesMaxBytes)
	}
	return nil
}

// ---- /runs/batch ----

// batchIngestRuns is client.js:1780-1861 (the deprecated JSON endpoint): prepare, merge, serialise, post.
func (c *client) batchIngestRuns(ctx context.Context, creates, updates []*omap, o sendOpts) error {
	raw := c.buildBatchBody(creates, updates)
	if raw == nil {
		return nil
	}
	defer raw.release()
	return c.postBatchBody(ctx, raw, o)
}

// buildBatchBody is the first half of batchIngestRuns (client.js:1780-1836); nil means there is nothing to send. The
// caller releases the body.
func (c *client) buildBatchBody(creates, updates []*omap) *multipartBody {
	for _, cr := range creates {
		c.prepareRunInPlace(cr)
	}
	for _, u := range updates {
		c.prepareRunInPlace(u)
	}
	creates, updates = mergeCreatesAndUpdates(creates, updates)
	if len(creates) == 0 && len(updates) == 0 {
		return nil
	}
	body := getBuf()
	chunks := obj("post", creates, "patch", updates)
	body.b = serializeForTracing(body.b, chunks, func() string {
		var ids []string
		for _, r := range creates {
			ids = append(ids, jsString(r.val("id")))
		}
		for _, r := range updates {
			ids = append(ids, jsString(r.val("id")))
		}
		return "Ingesting runs with ids: " + strings.Join(ids, ",")
	})
	return newRawBody(body)
}

// postBatchBody is _postBatchIngestRuns (client.js:1838-1861).
func (c *client) postBatchBody(ctx context.Context, raw *multipartBody, o sendOpts) error {
	_, err := c.batchCaller.callWithOptions(ctx, o.sizeBytes, func(ctx context.Context) (*http.Response, error) {
		h, err := c.mergedHeaders()
		if err != nil {
			return nil, err
		}
		h.Set("Content-Type", "application/json")
		h.Set("Accept", "application/json")
		withDest(h, o.apiKey, o.workspaceID)
		url := c.apiURL
		if o.apiURL != nil {
			url = *o.apiURL
		}
		return c.attempt(ctx, c.timeout, "POST", url+"/runs/batch", h, raw.newReader(false), raw.total, "batch create run", true, nil)
	})
	return err
}

// newRawBody wraps a serialised buffer as a request body with the ownership rules of the multipart body: the buffer
// goes back to the pool when the owner released it and every reader the transport was given is closed (net/http may
// read or close a body after RoundTrip returned, e.g. when the server answers before reading it).
func newRawBody(b *buf) *multipartBody {
	mp := &multipartBody{buf: b, total: int64(len(b.b))}
	b.segs = append(b.segs[:0], seg{0, len(b.b)})
	mp.refs.Store(1)
	return mp
}

// ---- failed-trace fallback directory ----

var (
	fallbackDirsMu      sync.Mutex
	fallbackDirsCreated = map[string]struct{}{} // Client._fallbackDirsCreated
)

type appendWriter struct{ b *[]byte }

func (w appendWriter) Write(p []byte) (int, error) { *w.b = append(*w.b, p...); return len(p), nil }

// writeTraceToFallbackDir is Client._writeTraceToFallbackDir (client.js:1445-1489): a self-contained JSON file with
// the endpoint, the replay headers and the base64 body, written atomically (mode 0600) unless the directory is
// over its budget.
func (c *client) writeTraceToFallbackDir(directory string, mp *multipartBody, endpoint string, maxBytes int64) {
	env := make([]byte, 0, int(mp.total)*4/3+256)
	env = append(env, `{"version":1,"endpoint":`...)
	env = json.AppendStringifyString(env, endpoint)
	env = append(env, `,"headers":{"Content-Type":`...)
	env = json.AppendStringifyString(env, "multipart/form-data; boundary="+mp.boundary)
	env = append(env, `},"body_base64":"`...)
	enc := base64.NewEncoder(base64.StdEncoding, appendWriter{&env})
	cur := segCursor{mp: mp}
	for piece := cur.next(32 << 10); piece != nil; piece = cur.next(32 << 10) {
		enc.Write(piece)
	}
	enc.Close()
	env = append(env, `"}`...)

	var rnd [4]byte
	rand.Read(rnd[:]) // uuid.v4().slice(0, 8): the first 8 hex digits are random
	filename := "trace_" + strconv.FormatInt(nowMs(), 10) + "_" + hex.EncodeToString(rnd[:]) + ".json"
	filePath := filepath.Join(directory, filename)

	fallbackDirsMu.Lock()
	_, created := fallbackDirsCreated[directory]
	fallbackDirsMu.Unlock()
	if !created {
		if err := os.MkdirAll(directory, 0o777); err != nil {
			errorf("LangSmith tracing error: could not write trace to fallback dir %s: %s", directory, errString(err))
			return
		}
		fallbackDirsMu.Lock()
		fallbackDirsCreated[directory] = struct{}{}
		fallbackDirsMu.Unlock()
	}
	// Check the budget before writing; drop the new trace if over the limit.
	if maxBytes > 0 {
		if total, ok := fallbackDirSize(directory); ok && total >= maxBytes {
			warnf("Could not write trace to fallback dir %s as it's already over size limit (%d bytes >= %d bytes). Increase LANGSMITH_FAILED_TRACES_MAX_MB if possible.", directory, total, maxBytes)
			return
		}
	}
	tmp := filePath + ".tmp"
	if err := os.WriteFile(tmp, env, 0o600); err != nil {
		errorf("LangSmith tracing error: could not write trace to fallback dir %s: %s", directory, errString(err))
		return
	}
	if err := os.Rename(tmp, filePath); err != nil {
		errorf("LangSmith tracing error: could not write trace to fallback dir %s: %s", directory, errString(err))
		return
	}
	warnf("LangSmith trace upload failed; data saved to %s for later replay.", filePath)
}

// fallbackDirSize sums the sizes of trace_*.json in dir; ok is false when the check itself fails (the original
// then writes anyway).
func fallbackDirSize(dir string) (int64, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, false
	}
	var total int64
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "trace_") || !strings.HasSuffix(name, ".json") {
			continue
		}
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			return 0, false
		}
		total += info.Size()
	}
	return total, true
}
