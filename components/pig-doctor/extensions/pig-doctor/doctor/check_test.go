//go:build unix

package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func ids(r *Report) []string {
	seen := map[string]bool{}
	for _, f := range r.Findings {
		seen[f.ID] = true
	}
	var out []string
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func one(t *testing.T, r *Report, id string) Finding {
	t.Helper()
	fs := r.Find(id)
	if len(fs) != 1 {
		t.Fatalf("want exactly one %s finding, got %d (all: %v)", id, len(fs), ids(r))
	}
	return fs[0]
}

func TestCheckRealCase(t *testing.T) {
	rc := newRealCase(t)
	r := mustCheck(t, rc.opts())

	for _, id := range []string{"pkg.duplicate", "ext.duplicate", "ext.broken", "go.mismatch",
		"cache.cells", "cache.ext", "cache.piglet-artifacts", "orphan.agent-dir", "orphan.credentials", "orphan.automation-dir",
		"legacy.extensions-dir", "legacy.extensions-toml"} {
		if len(r.Find(id)) == 0 {
			t.Errorf("missing finding %s; got %v", id, ids(r))
		}
	}
	// Every finding says what, why, the exact fix and whether it is safe to auto-fix.
	for _, f := range r.Findings {
		if f.Title == "" || f.Why == "" || f.Fix == "" || f.Safety == "" || f.Severity == "" {
			t.Errorf("finding %s incomplete: %+v", f.ID, f)
		}
		if f.Safety != Manual && f.Group == "" {
			t.Errorf("finding %s is auto-fixable but has no group", f.ID)
		}
	}

	// Broken extensions: one line per extension with its cause.
	broken := one(t, r, "ext.broken")
	if broken.Severity != SevError {
		t.Errorf("ext.broken severity %s", broken.Severity)
	}
	joined := strings.Join(broken.Detail, "\n")
	for _, name := range thirteen {
		if !strings.Contains(joined, name+":") {
			t.Errorf("ext.broken does not list %s:\n%s", name, joined)
		}
	}
	if !strings.Contains(joined, "no Go source files") || !strings.Contains(joined, `does not match go tool version`) {
		t.Errorf("ext.broken causes missing:\n%s", joined)
	}

	// Toolchain mismatch reproduces the message from the audit.
	mm := one(t, r, "go.mismatch")
	if mm.Severity != SevError || !strings.Contains(strings.Join(mm.Detail, " "), "go1.26.7") || !strings.Contains(strings.Join(mm.Detail, " "), "go1.26.1") {
		t.Errorf("go.mismatch: %+v", mm)
	}
	if mm.Safety != Manual {
		t.Errorf("toolchain findings are manual")
	}

	// Duplicate package: severity error, subset of the other so it can be removed safely.
	dup := one(t, r, "pkg.duplicate")
	if dup.Safety != SafeAuto || dup.Group != "packages" {
		t.Errorf("pkg.duplicate should be safe (strict subset of the same package): %+v", dup)
	}
	if !strings.Contains(dup.Fix, rc.pkgA) {
		t.Errorf("fix should name the entry to remove: %s", dup.Fix)
	}

	// Caches: sizes are reported and the in-use / recent entries are named.
	cells := one(t, r, "cache.cells")
	// The audited toolchain is broken, so pruning is confirm-only (review finding).
	if cells.Safety != NeedsConfirm || cells.Group != "caches" {
		t.Errorf("cache.cells: %+v", cells)
	}
	if !strings.Contains(strings.Join(cells.Detail, "\n"), "recent") && !strings.Contains(strings.Join(cells.Detail, "\n"), "kept") {
		t.Errorf("cache.cells should say what is kept: %v", cells.Detail)
	}

	// Orphans.
	cred := one(t, r, "orphan.credentials")
	if cred.Safety != Credentials || cred.Severity != SevWarn || !strings.Contains(strings.Join(cred.Paths, " "), "launch-workers-agent") {
		t.Errorf("orphan.credentials: %+v", cred)
	}
	agents := r.Find("orphan.agent-dir")
	got := map[string]Safety{}
	for _, f := range agents {
		for _, p := range f.Paths {
			got[filepath.Base(p)] = f.Safety
		}
	}
	if got["kagent-pr-agent"] != SafeAuto || got["pigpen-agent"] != NeedsConfirm {
		t.Errorf("orphan agent dirs (no sessions safe, sessions confirm): %v", got)
	}
	if _, ok := got["recent-agent"]; ok {
		t.Errorf("a young agent dir must not be reported as orphaned")
	}
	if _, ok := got["agent"]; ok {
		t.Errorf("the agent dir is never an orphan")
	}

	// Unknown items are listed, never classified.
	want := []string{"harness-overlay", "marketplace", "marketplace.json", "piglet-cli", "receipts", "vendor.json"}
	if strings.Join(r.Unknown, ",") != strings.Join(want, ",") {
		t.Errorf("unknown = %v, want %v", r.Unknown, want)
	}
	text := r.Text()
	if !strings.Contains(text, "unknown, left alone") {
		t.Errorf("report must say 'unknown, left alone':\n%s", text)
	}
	for _, name := range want {
		if !strings.Contains(text, name) {
			t.Errorf("report should list unknown item %s", name)
		}
	}
	if strings.Contains(text, canary) {
		t.Error("report leaked a credential")
	}
}

func TestReportGroupedBySeverity(t *testing.T) {
	rc := newRealCase(t)
	text := mustCheck(t, rc.opts()).Text()
	e, w, i := strings.Index(text, "ERRORS"), strings.Index(text, "WARNINGS"), strings.Index(text, "NOTES")
	if e < 0 || w < 0 || i < 0 || !(e < w && w < i) {
		t.Fatalf("expected ERRORS, WARNINGS, NOTES sections in order:\n%s", text)
	}
	for _, s := range []string{"why:", "fix:", "auto-fix:"} {
		if !strings.Contains(text, s) {
			t.Errorf("report lacks %q lines", s)
		}
	}
}

func TestCheckIsReadOnlyAndNeverReadsProtectedFiles(t *testing.T) {
	rc := newRealCase(t)
	var reads []string
	readHook = func(p string) { reads = append(reads, p) }
	t.Cleanup(func() { readHook = nil })

	before := snapshot(t, rc.root)
	_ = mustCheck(t, rc.opts())
	if d := diffSnapshots(before, snapshot(t, rc.root)); len(d) != 0 {
		t.Errorf("check changed the tree: %v", d)
	}
	if len(reads) == 0 {
		t.Fatal("read hook never fired; the test proves nothing")
	}
	for _, p := range reads {
		b := filepath.Base(p)
		if strings.HasPrefix(b, "auth.json") || b == "trust.json" || strings.HasPrefix(b, "models") || strings.Contains(p, "/sessions/") || strings.Contains(p, "/skills/") || strings.Contains(p, "/prompts/") {
			t.Errorf("doctor read a protected file: %s", p)
		}
	}
}

func TestReadGuardRefusesProtectedNames(t *testing.T) {
	f := newFixture(t)
	for _, name := range []string{"auth.json", "auth.json.bak-20260927", "oauth.json", "trust.json", "models.json", "models.json.bak2", "models-store.json", "server.pem"} {
		p := f.put(filepath.Join(f.agent, name), canary)
		if _, err := readFileGuarded(p); err == nil {
			t.Errorf("readFileGuarded(%s) must refuse", name)
		}
	}
	p := f.put(filepath.Join(f.agent, "settings.json"), "{}")
	if b, err := readFileGuarded(p); err != nil || string(b) != "{}" {
		t.Errorf("settings.json should be readable: %v", err)
	}
}

func TestSettingsInvalidIsReportedNotGuessed(t *testing.T) {
	f := newFixture(t)
	f.settings("{ not json")
	r := mustCheck(t, f.opts())
	s := one(t, r, "settings.invalid")
	if s.Safety != Manual || s.Severity != SevError {
		t.Errorf("%+v", s)
	}
	if len(r.Find("pkg.duplicate")) != 0 {
		t.Error("no package findings from an unreadable settings.json")
	}
}

func TestCleanHomeHasNoFindings(t *testing.T) {
	f := newFixture(t)
	f.settings(`{"theme":"dark"}`)
	r := mustCheck(t, f.opts())
	for _, fd := range r.Findings {
		if fd.Severity != SevInfo {
			t.Errorf("clean home produced %s %s", fd.Severity, fd.ID)
		}
	}
	if len(r.Unknown) != 0 {
		t.Errorf("unknown: %v", r.Unknown)
	}
}

func TestDuplicatePackageVariants(t *testing.T) {
	type tc struct {
		name   string
		setup  func(f *fixture) string // returns settings body
		safety Safety
		remove string // substring expected in the removed entry, "" for none
		none   bool
	}
	cases := []tc{
		{"identical source with trailing slash", func(f *fixture) string {
			a := f.pkg(filepath.Join(f.user, "p"), "p", "ask")
			return fmt.Sprintf(`{"packages":[%q,%q]}`, a, a+"/")
		}, SafeAuto, "/p/", false},
		{"strict subset of the same package", func(f *fixture) string {
			a := f.pkg(filepath.Join(f.user, "a", "p"), "p", "ask", "grill")
			b := f.pkg(filepath.Join(f.user, "b", "p"), "p", "ask", "grill", "worktree")
			return fmt.Sprintf(`{"packages":[{"source":%q,"extensions":["extensions/ask"]},%q]}`, a, b)
		}, SafeAuto, "/a/p", false},
		{"equal coverage needs a decision, first listed kept", func(f *fixture) string {
			a := f.pkg(filepath.Join(f.user, "a", "p"), "p", "ask")
			b := f.pkg(filepath.Join(f.user, "b", "p"), "p", "ask")
			return fmt.Sprintf(`{"packages":[%q,%q]}`, a, b)
		}, NeedsConfirm, "/b/p", false},
		{"different package names are not duplicates", func(f *fixture) string {
			a := f.pkg(filepath.Join(f.user, "a", "p"), "one", "ask")
			b := f.pkg(filepath.Join(f.user, "b", "p"), "two", "ask")
			return fmt.Sprintf(`{"packages":[%q,%q]}`, a, b)
		}, "", "", true},
		{"unreadable sources match by folder name only: confirm", func(f *fixture) string {
			return fmt.Sprintf(`{"packages":[%q,%q]}`, filepath.Join(f.user, "gone1", "pig-stuff"), filepath.Join(f.user, "gone2", "pig-stuff"))
		}, NeedsConfirm, "/gone2/", false},
		{"npm versions are the same package", func(f *fixture) string {
			return `{"packages":["npm:@scope/pkg@1.0.0","npm:@scope/pkg@2.0.0"]}`
		}, NeedsConfirm, "@2.0.0", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			f.settings(c.setup(f))
			r := mustCheck(t, f.opts())
			fs := r.Find("pkg.duplicate")
			if c.none {
				if len(fs) != 0 {
					t.Fatalf("unexpected duplicate: %+v", fs)
				}
				return
			}
			if len(fs) != 1 {
				t.Fatalf("want 1 pkg.duplicate, got %d", len(fs))
			}
			if fs[0].Safety != c.safety {
				t.Errorf("safety = %s, want %s", fs[0].Safety, c.safety)
			}
			p := mustPlan(t, r, "packages")
			g := groupByID(p, "packages")
			if g == nil || len(g.Ops) != 1 || g.Ops[0].Kind != OpRemovePackage || !strings.Contains(g.Ops[0].Source, c.remove) {
				t.Errorf("plan = %+v, want removal of an entry containing %q", g, c.remove)
			}
		})
	}
}

func TestPlusOnlyFilterIsReportedAsNotNarrowing(t *testing.T) {
	f := newFixture(t)
	a := f.pkg(filepath.Join(f.user, "a", "p"), "p", "ask", "grill")
	b := f.pkg(filepath.Join(f.user, "b", "p"), "p", "ask", "grill")
	f.settings(fmt.Sprintf(`{"packages":[{"source":%q,"extensions":["+extensions/ask"]},%q]}`, a, b))
	r := mustCheck(t, f.opts())
	n := one(t, r, "pkg.filter-noop")
	if n.Safety != Manual || !strings.Contains(n.Fix, "extensions/ask") {
		t.Errorf("%+v", n)
	}
	// "+extensions/ask" alone keeps every member, so both entries cover the same members: a decision, not a safe fix.
	if d := one(t, r, "pkg.duplicate"); d.Safety != NeedsConfirm {
		t.Errorf("equal coverage must need confirmation: %+v", d)
	}
}

func TestKeepOptionChoosesTheSurvivor(t *testing.T) {
	f := newFixture(t)
	a := f.pkg(filepath.Join(f.user, "a", "p"), "p", "ask")
	b := f.pkg(filepath.Join(f.user, "b", "p"), "p", "ask")
	f.settings(fmt.Sprintf(`{"packages":[%q,%q]}`, a, b))
	o := f.opts()
	o.Keep = b
	r := mustCheck(t, o)
	g := groupByID(mustPlan(t, r, "packages"), "packages")
	if g == nil || len(g.Ops) != 1 || g.Ops[0].Source != a {
		t.Errorf("--keep %s should remove %s: %+v", b, a, g)
	}
}

func TestExtensionsLoadedTwice(t *testing.T) {
	rc := newRealCase(t)
	r := mustCheck(t, rc.opts())
	d := one(t, r, "ext.duplicate")
	all := strings.Join(d.Detail, "\n")
	if !strings.Contains(all, "ask") || !strings.Contains(all, filepath.Join(rc.home, "extensions", "ask")) {
		t.Errorf("ext.duplicate should name ask and both paths:\n%s", all)
	}
	if d.Safety != Manual {
		t.Errorf("ext.duplicate is resolved by the package and legacy fixes: %+v", d)
	}
}

func f2(rc *realCase) string { return filepath.Join(rc.home, "extensions") }

func TestToolchain(t *testing.T) {
	t.Run("healthy", func(t *testing.T) {
		f := newFixture(t)
		r := mustCheck(t, f.opts())
		for _, id := range []string{"go.mismatch", "go.goroot-inherited", "go.gotoolchain", "go.mise-multiple", "go.missing"} {
			if len(r.Find(id)) != 0 {
				t.Errorf("unexpected %s", id)
			}
		}
	})
	t.Run("no go on PATH", func(t *testing.T) {
		f := newFixture(t)
		_ = os.Remove(filepath.Join(f.bin, "go"))
		o := f.opts()
		o.Env = []string{"PATH=" + f.bin, "HOME=" + f.user}
		r := mustCheck(t, o)
		if m := one(t, r, "go.missing"); m.Safety != Manual {
			t.Errorf("%+v", m)
		}
	})
	t.Run("inherited GOROOT of another version", func(t *testing.T) {
		f := newFixture(t)
		f.fakeGo("go1.26.1", "go1.26.7", "")
		o := f.opts()
		o.Env = append(o.Env, "GOROOT=/opt/mise/go/1.26.7")
		r := mustCheck(t, o)
		g := one(t, r, "go.goroot-inherited")
		if g.Severity != SevWarn || !strings.Contains(g.Fix, "unset GOROOT") {
			t.Errorf("%+v", g)
		}
		mm := one(t, r, "go.mismatch")
		if !strings.Contains(mm.Why, "GOROOT") && !strings.Contains(strings.Join(mm.Detail, " "), "GOROOT") {
			t.Errorf("mismatch should mention the inherited GOROOT: %+v", mm)
		}
	})
	t.Run("GOTOOLCHAIN pinned to another version", func(t *testing.T) {
		f := newFixture(t)
		o := f.opts()
		o.Env = append(o.Env, "GOTOOLCHAIN=go1.26.7")
		r := mustCheck(t, o)
		g := one(t, r, "go.gotoolchain")
		if !strings.Contains(g.Fix, "unset GOTOOLCHAIN") {
			t.Errorf("%+v", g)
		}
		for _, v := range []string{"local", "auto", "go1.26.1", "go1.26.1+auto"} {
			o.Env = append(f.opts().Env, "GOTOOLCHAIN="+v)
			if len(mustCheck(t, o).Find("go.gotoolchain")) != 0 {
				t.Errorf("GOTOOLCHAIN=%s is harmless", v)
			}
		}
	})
	t.Run("several mise Go versions", func(t *testing.T) {
		f := newFixture(t)
		for _, v := range []string{"1.26.1", "1.26.7", "1.25.3"} {
			f.mkdir(filepath.Join(f.user, ".local", "share", "mise", "installs", "go", v, "bin"))
		}
		r := mustCheck(t, f.opts())
		m := one(t, r, "go.mise-multiple")
		if m.Safety != Manual || !strings.Contains(strings.Join(m.Detail, " "), "1.26.7") || !strings.Contains(m.Fix, "mise") {
			t.Errorf("%+v", m)
		}
		// One version is fine.
		g := newFixture(t)
		g.mkdir(filepath.Join(g.user, ".local", "share", "mise", "installs", "go", "1.26.1"))
		if len(mustCheck(t, g.opts()).Find("go.mise-multiple")) != 0 {
			t.Error("a single mise version is fine")
		}
	})
	t.Run("go.mod newer than the toolchain", func(t *testing.T) {
		f := newFixture(t)
		f.put(filepath.Join(f.agent, "extensions", "new", "go.mod"), "module x/new\n\ngo 1.28\n")
		f.put(filepath.Join(f.agent, "extensions", "new", "main.go"), "package new\n")
		r := mustCheck(t, f.opts())
		b := one(t, r, "ext.broken")
		if !strings.Contains(strings.Join(b.Detail, "\n"), "requires go 1.28") {
			t.Errorf("%v", b.Detail)
		}
	})
}

func TestCacheClassification(t *testing.T) {
	rc := newRealCase(t)
	r := mustCheck(t, rc.opts())
	p := mustPlan(t, r, "caches")
	g := groupByID(p, "caches")
	if g == nil {
		t.Fatal("no caches group")
	}
	prune := func(rel string) bool { return hasOp(g, OpDelete, filepath.Join(rc.home, rel)) }
	for _, rel := range []string{"cache/cells/go/old1", "cache/cells/go/old2", "cache/cells/go/incomplete-old", "cache/cells/go/inuse", "cache/ext/001-aaaa", "cache/ext/api-surface-bbbb"} {
		if !prune(rel) {
			t.Errorf("%s should be pruned", rel)
		}
	}
	for _, rel := range []string{"cache/cells/go/recent", "cache/cells/go/incomplete-new", "cache/ext/recent-cccc", "cache/cells/go/.locks", "cache/toolchains", "cache/toolchains/x.json"} {
		if prune(rel) {
			t.Errorf("%s must be kept", rel)
		}
	}
	for _, o := range g.Ops {
		if o.Kind != OpDelete {
			t.Errorf("cache ops are deletes: %v", o)
		}
	}
	// Sizes reported.
	found := false
	for _, s := range r.CacheSizes {
		if s.Path == filepath.Join(rc.home, "cache", "cells") && s.Bytes > 0 && s.Entries == 6 && s.Prune == 4 {
			found = true
		}
	}
	if !found {
		t.Errorf("cache sizes: %+v", r.CacheSizes)
	}
}

func TestCacheEntryInUseIsKept(t *testing.T) {
	rc := newRealCase(t)
	release := rc.holdLock("cache/cells/go/.locks/inuse.usage.lock")
	defer release()
	r := mustCheck(t, rc.opts())
	g := groupByID(mustPlan(t, r, "caches"), "caches")
	if hasOp(g, OpDelete, filepath.Join(rc.home, "cache/cells/go/inuse")) {
		t.Fatal("an entry whose usage lease is held must be kept")
	}
	if !hasOp(g, OpDelete, filepath.Join(rc.home, "cache/cells/go/old1")) {
		t.Error("other old entries are still pruned")
	}
	if !strings.Contains(strings.Join(one(t, r, "cache.cells").Detail, "\n"), "in use: 1") {
		t.Errorf("report should say what is in use: %v", one(t, r, "cache.cells").Detail)
	}
}

func TestCacheEntryUsedByRunningProcessIsKept(t *testing.T) {
	rc := newRealCase(t)
	rc.procs.list = []Proc{{PID: 4242, Args: []string{"/usr/bin/pig"}, Exe: filepath.Join(rc.home, "cache/cells/go/old2/runner"), Env: map[string]string{}}}
	r := mustCheck(t, rc.opts())
	g := groupByID(mustPlan(t, r, "caches"), "caches")
	if hasOp(g, OpDelete, filepath.Join(rc.home, "cache/cells/go/old2")) {
		t.Fatal("an entry executing in a running process must be kept")
	}
	if len(r.Processes) != 1 || r.Processes[0].PID != 4242 {
		t.Errorf("processes: %+v", r.Processes)
	}
}

func TestPigletArtifactsAreReportedPerPiglet(t *testing.T) {
	rc := newRealCase(t)
	f := one(t, mustCheck(t, rc.opts()), "cache.piglet-artifacts")
	detail := strings.Join(f.Detail, "\n")
	if f.Safety != Manual || f.Group != "" || !strings.Contains(detail, "kinsy: 8.0 KiB in 2") || !strings.Contains(detail, "pig-standard: 12.0 KiB in 3") {
		t.Errorf("%+v", f)
	}
}

func TestPigletCellsAreOptIn(t *testing.T) {
	rc := newRealCase(t)
	r := mustCheck(t, rc.opts())
	if len(r.Find("cache.piglet-binary-cells")) == 0 {
		t.Fatal("size of piglet-binary-cells must be reported")
	}
	if groupByID(mustPlan(t, r), "piglet-cells") != nil {
		t.Error("piglet cells are not planned unless asked for")
	}
	o := rc.opts()
	o.IncludePigletCells = true
	g := groupByID(mustPlan(t, mustCheck(t, o)), "piglet-cells")
	if g == nil || !hasOp(g, OpMove, filepath.Join(rc.home, "piglet-binary-cells/isolated/go/subagent-388b")) || g.Safety != NeedsConfirm {
		t.Errorf("%+v", g)
	}
}

func TestOrphanRules(t *testing.T) {
	old := func(f *fixture, rel string) { f.put(rel+"/settings.json", "{}"); f.age(rel, 60) }
	t.Run("old unreferenced is orphaned", func(t *testing.T) {
		f := newFixture(t)
		old(f, "foo-agent")
		r := mustCheck(t, f.opts())
		if len(r.Find("orphan.agent-dir")) != 1 {
			t.Fatalf("%v", ids(r))
		}
	})
	t.Run("younger than the threshold", func(t *testing.T) {
		f := newFixture(t)
		f.put("foo-agent/settings.json", "{}")
		f.age("foo-agent", 13)
		if len(mustCheck(t, f.opts()).Find("orphan.agent-dir")) != 0 {
			t.Error("13 days old is not older than 14")
		}
		o := f.opts()
		o.OrphanDays = 10
		if len(mustCheck(t, o).Find("orphan.agent-dir")) != 1 {
			t.Error("--orphan-days 10 makes it orphaned")
		}
	})
	t.Run("one recently touched file keeps the dir", func(t *testing.T) {
		f := newFixture(t)
		old(f, "foo-agent")
		f.put("foo-agent/sessions/new.jsonl", "{}")
		if len(mustCheck(t, f.opts()).Find("orphan.agent-dir")) != 0 {
			t.Error("recent activity inside means in use")
		}
	})
	t.Run("referenced by settings", func(t *testing.T) {
		f := newFixture(t)
		old(f, "foo-agent")
		f.settings(fmt.Sprintf(`{"sessionDir":%q}`, filepath.Join(f.home, "foo-agent", "sessions")))
		if len(mustCheck(t, f.opts()).Find("orphan.agent-dir")) != 0 {
			t.Error("a directory settings.json points into is not an orphan")
		}
	})
	t.Run("active agent dir", func(t *testing.T) {
		f := newFixture(t)
		old(f, "foo-agent")
		o := f.opts()
		o.AgentDir = filepath.Join(f.home, "foo-agent")
		if len(mustCheck(t, o).Find("orphan.agent-dir")) != 0 {
			t.Error("the active agent dir is never an orphan")
		}
	})
	t.Run("a running process uses it", func(t *testing.T) {
		f := newFixture(t)
		old(f, "foo-agent")
		f.procs.list = []Proc{{PID: 7, Args: []string{"pig"}, Env: map[string]string{"PIG_CODING_AGENT_DIR": filepath.Join(f.home, "foo-agent")}}}
		r := mustCheck(t, f.opts())
		if len(r.Find("orphan.agent-dir")) != 0 {
			t.Error("a dir a running pig uses is not an orphan")
		}
		if len(r.Processes) != 1 || r.Processes[0].AgentDir == "" {
			t.Errorf("%+v", r.Processes)
		}
	})
	t.Run("a running process with unreadable environment makes it confirm-only", func(t *testing.T) {
		f := newFixture(t)
		old(f, "foo-agent")
		f.procs.list = []Proc{{PID: 7, Args: []string{"pig"}}} // Env nil: unknown
		fs := mustCheck(t, f.opts()).Find("orphan.agent-dir")
		if len(fs) != 1 || fs[0].Safety != NeedsConfirm {
			t.Errorf("%+v", fs)
		}
	})
	t.Run("other automation processes do not count", func(t *testing.T) {
		f := newFixture(t)
		old(f, "foo-agent")
		f.procs.list = []Proc{{PID: 8, Args: []string{"/usr/bin/vim", "x"}}}
		if len(mustCheck(t, f.opts()).Find("orphan.agent-dir")) != 1 {
			t.Error("a non-pig process is irrelevant")
		}
	})
	t.Run("held lock inside", func(t *testing.T) {
		f := newFixture(t)
		old(f, "foo-agent")
		f.holdLock("foo-agent/settings.json.lock")
		f.age("foo-agent", 60)
		fs := mustCheck(t, f.opts()).Find("orphan.agent-dir")
		if len(fs) != 0 {
			t.Error("a directory with a held lock is in use")
		}
	})
	t.Run("automation output dirs by age", func(t *testing.T) {
		f := newFixture(t)
		f.put("subagent-output/a.txt", "x")
		f.age("subagent-output", 30)
		f.put("worktree-claims/a.json", "x")
		f.age("worktree-claims", 3)
		r := mustCheck(t, f.opts())
		fs := r.Find("orphan.automation-dir")
		if len(fs) != 1 || !strings.Contains(fs[0].Paths[0], "subagent-output") {
			t.Errorf("%+v", fs)
		}
	})
	t.Run("a directory that only looks like an agent dir is unknown", func(t *testing.T) {
		f := newFixture(t)
		f.put("notes-agent/readme.md", "x")
		f.age("notes-agent", 60)
		r := mustCheck(t, f.opts())
		if len(r.Find("orphan.agent-dir")) != 0 || strings.Join(r.Unknown, ",") != "notes-agent" {
			t.Errorf("%v %v", ids(r), r.Unknown)
		}
	})
	t.Run("credential copies are called out, never read", func(t *testing.T) {
		f := newFixture(t)
		old(f, "foo-agent")
		f.put("foo-agent/auth.json", canary)
		f.put("foo-agent/auth.json.bak-1", canary)
		f.age("foo-agent", 60)
		r := mustCheck(t, f.opts())
		c := one(t, r, "orphan.credentials")
		if len(r.Find("orphan.agent-dir")) != 0 {
			t.Error("a dir with credentials is reported once, as a credential finding")
		}
		if !strings.Contains(strings.Join(c.Detail, "\n"), "auth.json") || strings.Contains(strings.Join(c.Detail, "\n"), canary) {
			t.Errorf("%v", c.Detail)
		}
	})
}

func TestSymlinksAreNeverFollowed(t *testing.T) {
	f := newFixture(t)
	outside := filepath.Join(f.root, "outside")
	f.put(filepath.Join(outside, "precious.txt"), "keep")
	f.age(outside, 400)
	// an "orphan agent dir" that is a symlink out of the home, a cache entry that is one, a legacy dir that is one
	mustSymlink(t, outside, filepath.Join(f.home, "evil-agent"))
	f.mkdir("cache/cells/go")
	mustSymlink(t, outside, filepath.Join(f.home, "cache/cells/go/linked"))
	mustSymlink(t, outside, filepath.Join(f.home, "extensions"))
	mustSymlink(t, outside, filepath.Join(f.home, "subagent-output"))
	mustSymlink(t, outside, filepath.Join(f.home, "artifacts"))
	r := mustCheck(t, f.opts())
	if len(r.Find("orphan.agent-dir"))+len(r.Find("orphan.automation-dir"))+len(r.Find("legacy.extensions-dir")) != 0 {
		t.Errorf("a symlink is not a candidate: %v", ids(r))
	}
	text := r.Text()
	for _, p := range []string{"evil-agent", "extensions", "subagent-output", "artifacts", "cache/cells/go/linked"} {
		if !strings.Contains(text, "symlink, left alone: "+filepath.Join(f.home, p)) {
			t.Errorf("symlink %s should be reported as left alone:\n%s", p, text)
		}
	}
	before := snapshot(t, f.root)
	p := mustPlan(t, r)
	if _, err := Apply(f.opts(), p, yes); err != nil {
		t.Fatal(err)
	}
	if d := diffSnapshots(before, snapshot(t, f.root)); len(d) != 0 {
		t.Errorf("fix changed something around symlinks: %v", d)
	}
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownItemsNeverPlanned(t *testing.T) {
	rc := newRealCase(t)
	r := mustCheck(t, rc.opts())
	o := rc.opts()
	o.IncludePigletCells = true
	p := mustPlan(t, mustCheck(t, o))
	for _, g := range p.Groups {
		for _, op := range g.Ops {
			for _, u := range r.Unknown {
				if strings.HasPrefix(op.Path, filepath.Join(rc.home, u)) {
					t.Errorf("op %s touches unknown item %s", op, u)
				}
			}
			for _, prot := range []string{"auth.json", "trust.json", "models.json", "models-store.json", "sessions", "skills", "prompts", "AGENTS.md"} {
				if strings.HasPrefix(op.Path, filepath.Join(rc.agent, prot)) {
					t.Errorf("op %s touches protected %s", op, prot)
				}
			}
		}
	}
}
