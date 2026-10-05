//go:build windows

package websearch

// Windows has neither flag; the original passes 0 there too.
const (
	oNoFollow  = 0
	oDirectory = 0
)
