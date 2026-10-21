//go:build windows

package pirpc

// processExists is unused on Windows: TestDisposeReapsTheChild skips there.
func processExists(int) error { return nil }
