package pi_typesafe

// SetAdmittedHook replaces the seam that runs between a tool call's consent gate and its send, and returns
// the function that restores the previous one. Tests use it to change the backend at that moment.
func SetAdmittedHook(f func()) (restore func()) {
	previous := admitted
	admitted = f
	return func() { admitted = previous }
}
