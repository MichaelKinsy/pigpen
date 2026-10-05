//go:build !linux && !darwin

package svc

// SpawnPty is unavailable here: the standard library has no ConPTY or BSD pty support, so the
// terminal service refuses to start (a documented gap, see PORT.md).
func SpawnPty(string, []string, PtyOptions, PtyHandlers) (PtyProcess, error) {
	return nil, ErrPtyUnsupported
}
