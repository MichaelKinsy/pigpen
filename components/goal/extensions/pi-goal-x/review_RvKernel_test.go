package pi_goal_x

import (
	"os"
	"testing"
)

// goal-state.ts loadState awaits readActiveGoalPoolAsync, which rejects for an external pool whose root is gone (a cached pool is
// served without touching the root).
func TestRvKernelLoadStateFailsWhenExternalRootIsGone(t *testing.T) {
	f := newGsrFixture(t)
	ctx := f.context("proj", f.rootJSON())
	if err := os.MkdirAll(f.root, 0o755); err != nil {
		t.Fatal(err)
	}
	h := newKernelHost(ctx.cwd, "proj")
	h.noUI = true
	c := newCore(h)
	if err := c.loadState(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(f.root); err != nil {
		t.Fatal(err)
	}
	if err := c.loadState(); err != nil {
		t.Fatalf("a cached pool is served without reading the root: %v", err)
	}
	invalidateGoalPoolCache()
	gsrErr(t, c.loadState(), `(?i)no such file|ENOENT|not exist`, "a removed external pool")
}
