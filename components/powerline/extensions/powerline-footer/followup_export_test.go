package powerline_footer

// SetJevEndpoint points the Jev call at a local server for an external test and returns the restore function. The original
// hardcodes the URL, so a pigeq scenario server cannot stand in for it (see auto-follow-up.ts JEV_ENDPOINT).
func SetJevEndpoint(url string) (restore func()) {
	previous := jevEndpoint
	jevEndpoint = url
	return func() { jevEndpoint = previous }
}
