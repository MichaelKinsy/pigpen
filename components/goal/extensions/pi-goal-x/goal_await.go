package pi_goal_x

// awaiting wraps a blocking host dialog (Select, Confirm, Input, Custom) in a handler. The host call itself releases the core's lock
// while it waits (sdkHost.await), so key events that answer the dialog run; awaiting adds the two things the original's `await` also
// does: the UI update the handler queued runs first (the microtask), and after the wait the entry's own host is bound again (another
// entry may have bound its context meanwhile). A wait that is not a host dialog (an auditor or model run) uses core.await.
func (c *core) awaiting(fn func()) {
	h, st := c.h, c.st
	c.flush()
	fn()
	c.h, c.st = h, st
}
