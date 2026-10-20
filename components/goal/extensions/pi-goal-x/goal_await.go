package pi_goal_x

// awaiting wraps a blocking host dialog (Select, Confirm, Input, Custom) in a handler. The host call itself releases the core's lock
// while it waits (sdkHost.awaitDialog), so key events that answer the dialog run; awaiting adds the two things the original's `await`
// also does: the UI update the handler queued runs (the microtask), and after the wait the entry's own host is bound again (another
// entry may have bound its context meanwhile). A host that runs the queued update itself once the dialog's request is out (the SDK
// host: Pi's RPC mode shows the request first) is not flushed here; any other host is flushed before the dialog. A wait that is not
// a host dialog (an auditor or model run) uses core.await.
func (c *core) awaiting(fn func()) {
	h, st := c.h, c.st
	if _, own := h.(dialogFlusher); !own {
		c.flush()
	}
	fn()
	c.h, c.st = h, st
}

// dialogFlusher is a host whose dialogs run the queued UI update after the request (sdkHost.awaitDialog).
type dialogFlusher interface{ flushesAfterDialogRequest() }
