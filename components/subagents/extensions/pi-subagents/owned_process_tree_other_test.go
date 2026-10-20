//go:build !unix

package pi_subagents

import "testing"

func ownedTreeTermResistant(t *testing.T)      { t.Skip("POSIX process groups") }
func ownedTreeDetachedDescendant(t *testing.T) { t.Skip("POSIX process groups") }
