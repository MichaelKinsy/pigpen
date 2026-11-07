package langsmith

// AutoBatchQueue, the aggregation timer, drainAutoBatchQueue, the batch workers, the /info probe
// (_ensureServerInfo) and awaitPendingTraceBatches of client.js (langsmith 0.10.5).
//
// The original is single-threaded: processRunOperation runs synchronously up to its first `await`, then continues
// in microtasks. Here the synchronous part runs on the caller's goroutine under client.mu, and the continuation is
// a callback (withServerInfo) that runs inline when the server info is cached and otherwise on the probe's
// goroutine, in registration order, which is the order the promise handlers of the original run in. No goroutine
// is spawned per operation.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// ---- queue ----

type queueItem struct {
	action   int
	payload  *omap
	dest     destOptions
	size     int64
	resolved bool // guarded by client.mu
}

// autoBatchQueue is client.js AutoBatchQueue (228-327); guarded by client.mu.
type autoBatchQueue struct {
	items        []*queueItem
	head         int
	sizeBytes    int64
	maxSizeBytes int64
}

func (q *autoBatchQueue) len() int { return len(q.items) - q.head }

func (q *autoBatchQueue) peekSize() float64 {
	if q.len() == 0 {
		return 0
	}
	return float64(q.items[q.head].size)
}

// push is AutoBatchQueue.push (253-288): false means the item was dropped for exceeding the soft size limit
// (an empty queue always accepts, to support large single traces).
func (q *autoBatchQueue) push(it *queueItem) bool {
	if q.sizeBytes+it.size > q.maxSizeBytes && q.len() > 0 {
		return false
	}
	q.items = append(q.items, it)
	q.sizeBytes += it.size
	return true
}

// pop is AutoBatchQueue.pop (289-326), minus the promise plumbing.
func (q *autoBatchQueue) pop(upToSizeBytes, upToSize float64) ([]*queueItem, error) {
	if upToSizeBytes < 1 {
		return nil, errors.New("Number of bytes to pop off may not be less than 1.")
	}
	var popped []*queueItem
	var poppedBytes float64
	for poppedBytes+q.peekSize() < upToSizeBytes && q.len() > 0 && float64(len(popped)) < upToSize {
		popped = append(popped, q.shift())
		poppedBytes += float64(popped[len(popped)-1].size)
	}
	// If there is an item on the queue we were unable to pop, just return it as a single batch.
	if len(popped) == 0 && q.len() > 0 {
		popped = append(popped, q.shift())
	}
	return popped, nil
}

func (q *autoBatchQueue) shift() *queueItem {
	it := q.items[q.head]
	q.items[q.head] = nil
	q.head++
	q.sizeBytes -= it.size
	if q.head == len(q.items) {
		q.items, q.head = q.items[:0], 0
	} else if q.head >= 64 && q.head*2 >= len(q.items) {
		n := copy(q.items, q.items[q.head:])
		clear(q.items[n:])
		q.items, q.head = q.items[:n], 0
	}
	return it
}

// ---- batches and workers ----

// drainState is one drainAutoBatchQueue promise: it finishes when all its batches are sent.
type drainState struct {
	remaining int
	tracked   bool
}

// batchState is one pop of the queue; the items resolve when every destination group of it is processed.
type batchState struct {
	items     []*queueItem
	remaining int
	drain     *drainState
}

// batchJob is one destination group of a batch (the `_processBatch(batch, options)` call).
type batchJob struct {
	items []*queueItem
	dest  destOptions
	bs    *batchState
}

func eqPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func ptrOr(a, b *string) *string {
	if a != nil {
		return a
	}
	return b
}

func optString(p *string) string { // `${p}` for a string | undefined
	if p == nil {
		return "undefined"
	}
	return *p
}

// drainLocked is drainAutoBatchQueue (client.js:1388-1432): it pops the whole queue in batches and splits each by
// destination. The jobs are returned for enqueueJobsLocked. Caller holds c.mu.
func (c *client) drainLocked(bytesLimit, limit float64, track bool) (*drainState, []batchJob, error) {
	ds := &drainState{tracked: track}
	var jobs []batchJob
	for c.queue.len() > 0 {
		batch, err := c.queue.pop(bytesLimit, limit)
		if err != nil {
			return nil, nil, err
		}
		if len(batch) == 0 {
			break
		}
		bs := &batchState{items: batch, drain: ds}
		ds.remaining++
		type group struct {
			key   string
			items []*queueItem
		}
		var groups []group
		for _, it := range batch {
			apiURL := c.apiURL
			if it.dest.APIURL != nil {
				apiURL = *it.dest.APIURL
			}
			apiKey := optString(ptrOr(it.dest.APIKey, c.apiKey))
			ws := ""
			if w := ptrOr(it.dest.WorkspaceID, c.workspaceID); w != nil {
				ws = *w
			}
			// client.js:1403-1408: the key is "default" only when the item carries exactly the client's own
			// destination; an item without options never does (item.apiUrl is undefined, this.apiUrl is not).
			isDefault := eqPtr(it.dest.APIKey, c.apiKey) && eqPtr(it.dest.APIURL, &c.apiURL) && eqPtr(it.dest.WorkspaceID, c.workspaceID)
			key := "default"
			if !isDefault {
				key = apiURL + "|" + apiKey + "|" + ws
			}
			found := false
			for i := range groups {
				if groups[i].key == key {
					groups[i].items = append(groups[i].items, it)
					found = true
					break
				}
			}
			if !found {
				groups = append(groups, group{key: key, items: []*queueItem{it}})
			}
		}
		for _, g := range groups {
			j := batchJob{items: g.items, bs: bs}
			if g.key != "default" {
				parts := strings.Split(g.key, "|") // client.js:1418: a "|" inside a value splits it, as in the original
				j.dest.APIURL = &parts[0]
				j.dest.APIKey = &parts[1]
				if parts[2] != "" {
					j.dest.WorkspaceID = &parts[2]
				}
			}
			bs.remaining++
			jobs = append(jobs, j)
		}
	}
	if track && ds.remaining > 0 {
		c.pendingDrains++
	}
	return ds, jobs, nil
}

// enqueueJobsLocked hands jobs to the batch workers: at most traceBatchConcurrency goroutines, each exiting when
// the job list is empty.
func (c *client) enqueueJobsLocked(jobs []batchJob) {
	if c.closed {
		return
	}
	c.jobs = append(c.jobs, jobs...)
	for range jobs {
		if c.workers < c.traceBatchConcurrency {
			c.workers++
			c.wg.Add(1)
			go c.worker()
		}
	}
}

func (c *client) worker() {
	defer c.wg.Done()
	for {
		c.mu.Lock()
		if c.closed || len(c.jobs) == 0 {
			c.workers--
			c.mu.Unlock()
			return
		}
		j := c.jobs[0]
		c.jobs[0] = batchJob{}
		c.jobs = c.jobs[1:]
		if len(c.jobs) == 0 {
			c.jobs = nil
		}
		c.mu.Unlock()
		c.processBatch(j)
	}
}

// jobDone resolves the items of a batch when its last group is processed, and finishes the drain with its last batch.
func (c *client) jobDone(j batchJob) {
	c.mu.Lock()
	bs := j.bs
	bs.remaining--
	if bs.remaining == 0 {
		for _, it := range bs.items {
			it.resolved = true
		}
		bs.drain.remaining--
		if bs.drain.remaining == 0 && bs.drain.tracked {
			c.pendingDrains--
		}
		c.broadcastLocked()
	}
	c.mu.Unlock()
}

func (c *client) broadcastLocked() {
	if c.nWaiters > 0 {
		close(c.stateCh)
		c.stateCh = make(chan struct{})
	}
}

// waitUntil blocks until cond (evaluated under c.mu) holds, ctx ends, or the client closes.
func (c *client) waitUntil(ctx context.Context, cond func() bool) error {
	for {
		c.mu.Lock()
		if cond() {
			c.mu.Unlock()
			return nil
		}
		c.nWaiters++
		ch := c.stateCh
		c.mu.Unlock()
		var err error
		select {
		case <-ch:
		case <-ctx.Done():
			err = ctx.Err()
		case <-c.ctx.Done():
			err = c.ctx.Err()
		}
		c.mu.Lock()
		c.nWaiters--
		c.mu.Unlock()
		if err != nil {
			return err
		}
	}
}

// ---- processRunOperation ----

func (c *client) clearTimerLocked() {
	if c.timer != nil {
		c.timer.Stop()
		delete(c.timers, c.timer)
		c.timer = nil
	}
}

// processRunOperation is client.js:1600-1628. It returns the queued item (nil when the queue dropped it for size
// or the client is closed); the rest of the original's async body continues in the server-info callbacks.
func (c *client) processRunOperation(action int, payload *omap, opts *destOptions) *queueItem {
	c.mu.Lock()
	c.clearTimerLocked()
	c.mu.Unlock()
	payload = mergeRuntimeEnvIntoRun(payload, c.cachedEnvVars, c.omitTracedRuntimeInfo, c.tracingSampleRate)
	it := &queueItem{action: action, payload: payload, size: estimateSerializedSize(payload)}
	if opts != nil {
		it.dest = *opts
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	before := c.queue.sizeBytes
	ok := c.queue.push(it)
	c.mu.Unlock()
	if !ok {
		// Resolve immediately to avoid blocking the caller (client.js:267-273).
		warnf("AutoBatchQueue size limit (%d bytes) exceeded. Dropping run with id: %s. Current queue size: %d bytes, attempted addition: %d bytes.",
			c.queue.maxSizeBytes, jsString(payload.val("id")), before, it.size)
		it = nil
	}
	if c.manualFlushMode {
		// Rely on manual flushing in serverless environments.
		return it
	}
	c.withServerInfo(func(si *serverInfo) {
		bytesLimit := c.sizeLimitBytes(si)
		c.withServerInfo(func(si2 *serverInfo) {
			c.afterLimits(bytesLimit, c.sizeLimit(si2))
		})
	})
	return it
}

// sizeLimitBytes is _getBatchSizeLimitBytes (client.js:1369-1374).
func (c *client) sizeLimitBytes(si *serverInfo) float64 {
	if c.batchSizeBytesLimit != nil {
		return *c.batchSizeBytesLimit
	}
	if si.sizeLimitBytes != nil {
		return *si.sizeLimitBytes
	}
	return defaultUncompressedBatchSizeLimitBytes
}

// sizeLimit is _getBatchSizeLimit (client.js:1378-1383).
func (c *client) sizeLimit(si *serverInfo) float64 {
	if c.batchSizeLimit != nil {
		return *c.batchSizeLimit
	}
	if si.sizeLimit != nil {
		return *si.sizeLimit
	}
	return defaultBatchSizeLimit
}

// afterLimits is the tail of processRunOperation (client.js:1611-1626).
func (c *client) afterLimits(bytesLimit, limit float64) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	var jobs []batchJob
	var err error
	if float64(c.queue.sizeBytes) > bytesLimit || float64(c.queue.len()) > limit {
		_, jobs, err = c.drainLocked(bytesLimit, limit, true)
	}
	if err == nil && c.queue.len() > 0 {
		h := &timerHolder{}
		h.t = time.AfterFunc(c.autoBatchDelay, func() { c.timerFired(h, bytesLimit, limit) })
		c.timer = h.t
		c.timers[h.t] = struct{}{}
	}
	c.enqueueJobsLocked(jobs)
	c.mu.Unlock()
	if err != nil {
		errorf("%s", errString(err)) // `.catch(console.error)` of the caller
	}
}

// timerHolder lets the timer callback find its own timer once c.mu is held (the field is written under c.mu).
type timerHolder struct{ t *time.Timer }

func (c *client) timerFired(h *timerHolder, bytesLimit, limit float64) {
	c.mu.Lock()
	delete(c.timers, h.t)
	c.timer = nil // client.js:1620: unconditional, as in the original
	if c.closed {
		c.mu.Unlock()
		return
	}
	_, jobs, err := c.drainLocked(bytesLimit, limit, true)
	c.enqueueJobsLocked(jobs)
	c.mu.Unlock()
	if err != nil {
		errorf("%s", errString(err))
	}
}

// ---- server info ----

// serverInfo is the part of the GET /info document the client reads.
type serverInfo struct {
	sizeLimit      *float64 // batch_ingest_config.size_limit
	sizeLimitBytes *float64 // batch_ingest_config.size_limit_bytes
	useMultipart   any      // batch_ingest_config.use_multipart_endpoint ?? true
	gzipEnabled    any      // instance_flags.gzip_body_enabled
}

func parseServerInfo(v any) *serverInfo {
	si := &serverInfo{useMultipart: true}
	root, _ := v.(map[string]any)
	if cfg, ok := root["batch_ingest_config"].(map[string]any); ok {
		if f, ok := cfg["size_limit"].(float64); ok {
			si.sizeLimit = &f
		}
		if f, ok := cfg["size_limit_bytes"].(float64); ok {
			si.sizeLimitBytes = &f
		}
		if u, ok := cfg["use_multipart_endpoint"]; ok && u != nil {
			si.useMultipart = u
		}
	}
	if fl, ok := root["instance_flags"].(map[string]any); ok {
		si.gzipEnabled = fl["gzip_body_enabled"]
	}
	return si
}

// infoFlight is the `_getServerInfoPromise`: the callbacks wait for the probe that is running.
type infoFlight struct {
	waiters []func(*serverInfo)
}

// withServerInfo is `await this._ensureServerInfo()` (client.js:1660-1680) in callback form: cb runs inline when
// the info is known, else after the probe. A failed probe resets the flight (before any callback runs, as the
// promise handlers of the original do) and hands cb an empty info (`this._serverInfo ?? {}`).
func (c *client) withServerInfo(cb func(*serverInfo)) {
	c.mu.Lock()
	if c.serverInfo != nil {
		si := c.serverInfo
		c.mu.Unlock()
		cb(si)
		return
	}
	if c.closed {
		c.mu.Unlock()
		return
	}
	f := c.infoFlight
	if f == nil {
		f = &infoFlight{}
		c.infoFlight = f
		c.wg.Add(1)
		go c.probe(f)
	}
	f.waiters = append(f.waiters, cb)
	c.mu.Unlock()
}

func (c *client) probe(f *infoFlight) {
	defer c.wg.Done()
	si, err := c.getServerInfo(c.ctx)
	if err != nil && c.ctx.Err() == nil {
		status := "Unspecified status code"
		var se *statusError
		if errors.As(err, &se) {
			status = jsNumberString(float64(se.Status))
		}
		warnf("[LANGSMITH]: Failed to fetch info on supported operations. Falling back to batch operations and default limits. Info: %s %s", status, err.Error())
	}
	c.mu.Lock()
	if err == nil {
		c.serverInfo = si
	}
	result := c.serverInfo
	if result == nil {
		c.infoFlight = nil
		result = &serverInfo{useMultipart: true}
	}
	waiters := f.waiters
	f.waiters = nil
	c.mu.Unlock()
	for _, cb := range waiters {
		cb(result)
	}
}

// ensureServerInfo is the blocking form for the workers.
func (c *client) ensureServerInfo(ctx context.Context) (*serverInfo, error) {
	c.mu.Lock()
	si := c.serverInfo
	c.mu.Unlock()
	if si != nil {
		return si, nil
	}
	ch := make(chan *serverInfo, 1)
	c.withServerInfo(func(si *serverInfo) { ch <- si })
	select {
	case si := <-ch:
		return si, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.ctx.Done():
		return nil, c.ctx.Err()
	}
}

// getServerInfo is _getServerInfo (client.js:1629-1647): GET /info through the retrying caller, 10 s per attempt.
func (c *client) getServerInfo(ctx context.Context) (*serverInfo, error) {
	h, err := c.mergedHeaders()
	if err != nil {
		return nil, err
	}
	h.Set("Accept", "application/json")
	var body []byte
	var readErr error
	_, err = c.caller.call(ctx, func(ctx context.Context) (*http.Response, error) {
		return c.attempt(ctx, serverInfoRequestTimeout, "GET", c.apiURL+"/info", h.Clone(), nil, 0, "get server info", false,
			func(resp *http.Response) error {
				body, readErr = io.ReadAll(resp.Body)
				resp.Body.Close()
				return nil
			})
	})
	if err != nil {
		return nil, err
	}
	if readErr != nil {
		return nil, readErr
	}
	var doc any
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	if c.debug {
		var pretty bytes.Buffer
		if json.Indent(&pretty, body, "", "  ") != nil {
			pretty.Reset()
			pretty.Write(body)
		}
		writeStderrLine("\n=== LangSmith Server Configuration ===\n" + pretty.String() + "\n")
	}
	return parseServerInfo(doc), nil
}

// ---- flush / awaitPendingTraceBatches / close ----

// flush is Client.flush (client.js:1708-1715).
func (c *client) flush(ctx context.Context) error {
	si, err := c.ensureServerInfo(ctx)
	if err != nil {
		return err
	}
	bytesLimit := c.sizeLimitBytes(si)
	si, err = c.ensureServerInfo(ctx)
	if err != nil {
		return err
	}
	c.mu.Lock()
	ds, jobs, err := c.drainLocked(bytesLimit, c.sizeLimit(si), false)
	c.enqueueJobsLocked(jobs)
	c.mu.Unlock()
	if err != nil {
		return err
	}
	return c.waitUntil(ctx, func() bool { return ds.remaining == 0 })
}

// awaitPendingTraceBatches is Client.awaitPendingTraceBatches() (client.js:5634-5674): it returns when every queued
// run operation has been sent (or dropped), or ctx ends.
func (c *client) awaitPendingTraceBatches(ctx context.Context) error {
	if c.manualFlushMode {
		warnf("[WARNING]: When tracing in manual flush mode, you must call `await client.flush()` manually to submit trace batches.")
		return nil
	}
	// Traceables use a backgrounded promise before updating runs; waiting a little lets them enqueue (5653-5655).
	t := time.NewTimer(time.Millisecond)
	select {
	case <-t.C:
	case <-ctx.Done():
		t.Stop()
		return ctx.Err()
	}
	// Wait for any in-flight drains before checking queue idleness (5656-5661).
	if err := c.waitUntil(ctx, func() bool { return c.pendingDrains == 0 }); err != nil {
		return err
	}
	c.mu.Lock()
	pending := append([]*queueItem(nil), c.queue.items[c.queue.head:]...)
	c.mu.Unlock()
	err := c.waitUntil(ctx, func() bool {
		for _, it := range pending {
			if !it.resolved {
				return false
			}
		}
		return true
	})
	if err != nil {
		return err
	}
	return c.batchCaller.onIdle(ctx)
}

// close stops the client's timers and goroutines: pending timers are stopped, queued items are abandoned (await
// awaitPendingTraceBatches first), in-flight requests are cancelled and every goroutine is waited for.
func (c *client) close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	for t := range c.timers {
		t.Stop()
	}
	c.timers = map[*time.Timer]struct{}{}
	c.timer = nil
	c.jobs = nil
	c.broadcastLocked()
	c.mu.Unlock()
	c.cancel()
	c.wg.Wait()
	if c.ownTransport != nil {
		c.ownTransport.CloseIdleConnections()
	}
	if c.profileAuth != nil {
		c.profileAuth.close()
	}
}
