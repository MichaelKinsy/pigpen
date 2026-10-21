package pi_subagents

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A refresh after a management change rediscovers from the files even when the cached snapshot still looks current (an edit that
// keeps the size and the modification time).
func TestAdvertisementRefreshBypassesTheDiscoveryCache(t *testing.T) {
	e := newMgmtEnv(t)
	a := newAdvertisement()
	a.hasCtx, a.cwd = true, e.dir
	path := filepath.Join(e.dir, ".pi", "agents", "a.md")
	writeFile(t, path, "---\nname: a\ndescription: A\nadvertise: false\n---\n\nA.\n")
	a.refresh()
	eq(t, len(a.snapshot()), 0, "not advertised")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, "---\nname: a\ndescription: A\nadvertise: true \n---\n\nA.\n")
	if err := os.Chtimes(path, time.Now(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	a.refresh()
	eq(t, len(a.snapshot()), 1, "the same-size, same-time edit is seen")
}

// The fast mode rewrite answers a copy; the request it was given keeps its own fields.
func TestFastModeRewriteLeavesThePayloadAlone(t *testing.T) {
	in := map[string]any{"model": "m"}
	out := rewriteFastModeProviderRequest(in).(map[string]any)
	eq(t, out["service_tier"], any("priority"), "the copy")
	if _, ok := in["service_tier"]; ok {
		t.Errorf("the given payload was changed: %v", in)
	}
}

// A model that already carries a thinking level keeps it unless the level is replaced.
func TestThinkingSuffixOfAModelThatHasOne(t *testing.T) {
	eq(t, applyThinkingSuffix("gpt-5:high", "low", false), "gpt-5:high", "kept")
	eq(t, applyThinkingSuffix("gpt-5:high", "low", true), "gpt-5:low", "replaced")
	eq(t, applyThinkingSuffix("gpt-5", "low", false), "gpt-5:low", "added")
}
