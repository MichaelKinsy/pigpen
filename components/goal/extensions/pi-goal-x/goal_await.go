package pi_goal_x

// awaiting is `await` for a blocking host call made inside a handler: the original yields the event loop there, so terminal input and
// other events run while a dialog is open. It lets the UI update the handler queued run, releases the core's lock for the call, and
// takes it back with the entry's own host bound again (another entry may have bound its context meanwhile).
func (c *core) awaiting(fn func()) {
	h, st := c.h, c.st
	c.flush()
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.h, c.st = h, st
	}()
	fn()
}
