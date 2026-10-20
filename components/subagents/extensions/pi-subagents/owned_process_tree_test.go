package pi_subagents

import "testing"

// Twins of owned-process-tree (runs/background/owned-process-tree.ts). The POSIX cases start real process groups
// (owned_process_tree_unix_test.go); the unsupported-platform case is the controller of a platform without process groups.

func TestOwnedProcessTreeTwins(t *testing.T) {
	const f = "owned-process-tree"
	tw(t, f, "owned process tree fails closed when process-group ownership is unsupported", func(t *testing.T) {
		c := newOwnedProcessTreeController(999_999, ownedProcessTreeOptions{})
		c.posix = false
		eq(t, jsonOf(c.Terminate()), `{"state":"unknown","reason":"unsupported-platform"}`, "proof")
	})
	tw(t, f, "owned process tree kills descendants and verifies a TERM-resistant POSIX group", func(t *testing.T) { ownedTreeTermResistant(t) })
	tw(t, f, "owned process tree does not claim observed while a detached descendant remains", func(t *testing.T) { ownedTreeDetachedDescendant(t) })
}
