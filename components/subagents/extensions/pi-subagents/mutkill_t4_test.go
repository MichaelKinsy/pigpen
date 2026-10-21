package pi_subagents

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A refresh rediscovers the agents even when the watched files look unchanged: same size, same modification time.
func TestAdvertisementRefreshDropsTheDiscoveryCache(t *testing.T) {
	home := withTempHome(t)
	t.Setenv("PI_CODING_AGENT_DIR", home)
	t.Setenv("PI_OFFLINE", "true")
	clearAgentDiscoveryCache()
	t.Cleanup(clearAgentDiscoveryCache)
	file := filepath.Join(home, "agents", "alpha.md")
	body := func(description string) string {
		return "---\nname: alpha\ndescription: " + description + "\nadvertise: true\n---\nAct narrowly.\n"
	}
	writeFile(t, file, body("aaaa"))
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	a := newAdvertisement()
	a.lookup = func(context.Context) string { return "" }
	a.begin(filepath.Join(home, "project"), "")
	if err := a.wait(make(chan struct{})); err != nil {
		t.Fatal(err)
	}
	if got := a.snapshot(); len(got) != 1 || got[0].Description != "aaaa" {
		t.Fatalf("first catalog: %+v", got)
	}
	writeFile(t, file, body("bbbb"))
	if err := os.Chtimes(file, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	a.refresh()
	if got := a.snapshot(); len(got) != 1 || got[0].Description != "bbbb" {
		t.Errorf("a refresh served the cached discovery: %+v", got)
	}
}

// The fast-mode rewrite never changes the payload it was given.
func TestFastModeRewriteLeavesTheInputAlone(t *testing.T) {
	in := map[string]any{"a": 1}
	rewriteFastModeProviderRequest(in)
	eq(t, in, map[string]any{"a": 1}, "the input map")
}
