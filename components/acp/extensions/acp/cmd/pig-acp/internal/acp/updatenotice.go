package acp

import "time"

var updateLookupTimeout = 800 * time.Millisecond

// BuildUpdateNotice is a stub.
func BuildUpdateNotice(piCommand string, piArgs []string) string { return "" }

func isSemver(v string) bool { return false }

func compareSemver(a, b string) int { return 0 }
