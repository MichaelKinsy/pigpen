//go:build unix

package doctor

// Findings of the adversarial review (rev-pigpen-pig-doctor). Each test was red
// on the reviewed branch before its fix.

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// A legacy extensions.toml inside the agent directory must not make the guard
// refuse the whole fix: the agent directory is never modified, so that file is
// reported with a manual fix and the rest of the plan still applies.
func TestAgentExtensionsTomlDoesNotBreakTheFix(t *testing.T) {
	f := newFixture(t)
	inAgent := f.put(filepath.Join(f.agent, "extensions.toml"), "[extensions.ask]\nhandler_timeout = \"30s\"\n")
	inHome := f.put("extensions.toml", "[extensions.ask]\nhandler_timeout = \"30s\"\n")
	o := f.opts()
	r := mustCheck(t, o)
	found := false
	for _, fd := range r.Find("legacy.extensions-toml") {
		if len(fd.Paths) == 1 && fd.Paths[0] == inAgent {
			found = true
			if fd.Safety != Manual || len(fd.ops) != 0 {
				t.Errorf("the agent directory's extensions.toml must be a manual finding, got %s with %d ops", fd.Safety, len(fd.ops))
			}
		}
	}
	if !found {
		t.Error("the agent directory's extensions.toml must still be reported")
	}
	if _, err := Apply(o, mustPlan(t, r), approveAll); err != nil {
		t.Fatalf("one file in the agent directory refused the whole fix: %v", err)
	}
	if !exists(inAgent) {
		t.Error("a file in the agent directory was moved")
	}
	if exists(inHome) {
		t.Error("the home's extensions.toml should have moved to the backup")
	}
}

// Command lines of running processes can hold secrets (pig --api-key ...). The
// report goes to the model through the pig_doctor tool, so it names processes
// by PID and executable only.
func TestReportNeverPrintsProcessArguments(t *testing.T) {
	f := newFixture(t)
	f.procs.list = []Proc{{PID: 4242, Args: []string{"/usr/local/bin/pig", "--api-key", "sk-SECRET-XYZ", "-p", "private prompt"}, Env: map[string]string{}}}
	r := mustCheck(t, f.opts())
	for _, out := range []string{r.Text(), r.JSON()} {
		if strings.Contains(out, "sk-SECRET-XYZ") || strings.Contains(out, "private prompt") {
			t.Fatalf("process arguments leaked into the report:\n%s", out)
		}
		if !strings.Contains(out, "4242") || !strings.Contains(out, "/usr/local/bin/pig") {
			t.Errorf("the process should still be named by PID and executable:\n%s", out)
		}
	}
}

// PiG's ResourceEnabled treats "!x" like "+x" (a force-include), not as an
// exclusion. The doctor must not think an entry with "!x" enables less than it
// does and then remove it as "safe".
func TestBangPatternFollowsPiG(t *testing.T) {
	members := []string{"extensions/ask", "extensions/grill", "extensions/worktree"}
	if got := applyPatterns(members, []string{"!extensions/grill"}); !reflect.DeepEqual(got, members) {
		t.Errorf("PiG enables every member for [\"!extensions/grill\"], got %v", got)
	}
	f := newFixture(t)
	p := f.pkg(filepath.Join(f.user, "p"), "p", "ask", "grill", "worktree")
	f.settings(`{"packages":[{"source":"` + p + `","extensions":["-extensions/grill"]},{"source":"` + p + `","extensions":["!extensions/grill"]}]}`)
	before := f.readSettings()
	o := f.opts()
	if _, err := Apply(o, mustPlan(t, mustCheck(t, o), "packages"), safeOnly); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.readSettings(), `"!extensions/grill"`) {
		t.Errorf("the entry that enables grill in PiG was removed as safe:\n%s", f.readSettings())
	}
	// Pi and the docs read "!" as an exclusion, PiG as an inclusion: a "!" filter is never auto-fixed.
	if f.readSettings() != before {
		t.Errorf("a duplicate with a \"!\" filter was removed without confirmation:\n%s", f.readSettings())
	}
}

// An agent directory outside the PiG home (PIG_CODING_AGENT_DIR elsewhere): the
// settings.json edit must restore like any other.
func TestRestoreRoundTripsSettingsOutsideTheHome(t *testing.T) {
	f := newFixture(t)
	p := f.pkg(filepath.Join(f.user, "p"), "p", "ask")
	o := f.opts()
	o.AgentDir = filepath.Join(f.root, "elsewhere-agent")
	before := "{\n  \"theme\": \"dark\",\n  \"packages\": [\"" + p + "\", \"" + p + "\"]\n}\n"
	f.put(filepath.Join(o.AgentDir, "settings.json"), before)
	res, err := Apply(o, mustPlan(t, mustCheck(t, o), "packages"), approveAll)
	if err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(o.AgentDir, "settings.json")
	if b, _ := os.ReadFile(settings); string(b) == before {
		t.Fatal("the duplicate was not removed")
	}
	rr, err := Restore(o, res.Backup, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rr.Conflict) != 0 {
		t.Errorf("conflicts: %v", rr.Conflict)
	}
	if b, _ := os.ReadFile(settings); string(b) != before {
		t.Errorf("settings.json not restored exactly:\n%s", b)
	}
}

// PiG keeps a cache entry whose usage metadata is missing or malformed
// ("retained conservatively", runtimecell/cache_lifecycle.go classifyCacheEntry).
func TestCacheEntryWithoutUsageMetadataIsKept(t *testing.T) {
	f := newFixture(t)
	f.put("cache/cells/go/nousage/payload", "z")
	f.put("cache/cells/go/nousage/ready.json", `{"artifact":"payload"}`)
	f.put("cache/cells/go/badusage/payload", "z")
	f.put("cache/cells/go/badusage/ready.json", `{"artifact":"payload"}`)
	f.put("cache/cells/go/badusage/usage.json", `{not json`)
	f.cell("cache/cells/go/expired", 100, true)
	f.age("cache", 100)
	g := groupByID(mustPlan(t, mustCheck(t, f.opts())), "caches")
	for _, n := range []string{"nousage", "badusage"} {
		if hasOp(g, OpDelete, filepath.Join(f.home, "cache/cells/go", n)) {
			t.Errorf("%s has no usable usage metadata; PiG keeps it, the doctor must too", n)
		}
	}
	if !hasOp(g, OpDelete, filepath.Join(f.home, "cache/cells/go/expired")) {
		t.Error("an expired entry with usage metadata is still prunable")
	}
}

// While the Go toolchain is broken a pruned Go extension cannot be rebuilt, so
// cache pruning is not "safe" (not applied by --yes) in that state.
func TestCachePruneNeedsConfirmationWhileTheToolchainIsBroken(t *testing.T) {
	rc := newRealCase(t) // go1.26.1 command with a go1.26.7 compiler
	r := mustCheck(t, rc.opts())
	g := groupByID(mustPlan(t, r), "caches")
	if g == nil || g.Safety == SafeAuto {
		t.Fatalf("caches group must need confirmation while the toolchain is broken: %+v", g)
	}
	f := newFixture(t) // a working toolchain
	f.cell("cache/cells/go/expired", 100, true)
	if g := groupByID(mustPlan(t, mustCheck(t, f.opts())), "caches"); g == nil || g.Safety != SafeAuto {
		t.Errorf("with a working toolchain pruning stays safe: %+v", g)
	}
}

// The user removes the entry that was to be kept while the confirmation is
// open: the doctor must not then remove the last copy of the Package.
func TestKeptPackageRemovedDuringConfirmationStopsTheEdit(t *testing.T) {
	f := newFixture(t)
	p := f.pkg(filepath.Join(f.user, "p"), "p", "ask")
	f.settings(`{"packages":["` + p + `","` + p + `"]}`)
	o := f.opts()
	plan := mustPlan(t, mustCheck(t, o), "packages")
	confirm := func(Group) (bool, error) {
		f.settings(`{"packages":["` + p + `"]}`)
		return true, nil
	}
	if _, err := Apply(o, plan, confirm); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.readSettings(), p) {
		t.Errorf("the last entry of the Package was removed:\n%s", f.readSettings())
	}
}

// Restore trusts nothing in a manifest: it never writes a protected file, and
// never replaces something that is not a regular file.
func TestRestoreNeverWritesProtectedOrSymlinkedTargets(t *testing.T) {
	f := newFixture(t)
	p := f.pkg(filepath.Join(f.user, "p"), "p", "ask")
	f.settings(`{"packages":["` + p + `","` + p + `"]}`)
	o := f.opts()
	res, err := Apply(o, mustPlan(t, mustCheck(t, o), "packages"), approveAll)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(o.BackupRoot, res.Backup)
	if o.BackupRoot == "" {
		dir = filepath.Join(f.user, ".pig-doctor-backup", res.Backup)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	// planted entries: an unreadable auth.json and a missing one
	unreadable := f.put("x-agent/auth.json", canary)
	if err := os.Chmod(unreadable, 0); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(f.home, "y-agent", "auth.json")
	f.mkdir("y-agent")
	if err := os.WriteFile(filepath.Join(dir, "files", "999-evil"), []byte("EVIL"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.Entries = append(m.Entries,
		manifestEntry{Kind: "edit", Original: unreadable, Backup: "files/999-evil", SHAAfter: sha([]byte(canary))},
		manifestEntry{Kind: "edit", Original: missing, Backup: "files/999-evil"})
	data, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	// settings.json became a symlink to an identical file kept elsewhere (dotfiles)
	settings := filepath.Join(f.agent, "settings.json")
	cur, _ := os.ReadFile(settings)
	dot := f.put(filepath.Join(f.root, "dotfiles", "settings.json"), string(cur))
	_ = os.Remove(settings)
	mustSymlink(t, dot, settings)

	_, _ = Restore(o, res.Backup, false)
	_ = os.Chmod(unreadable, 0o600)
	if b, _ := os.ReadFile(unreadable); !bytes.Equal(b, []byte(canary)) {
		t.Error("restore overwrote a protected file")
	}
	if exists(missing) {
		t.Error("restore created a protected file")
	}
	if info, err := os.Lstat(settings); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Error("restore replaced a symlinked settings.json with a regular file")
	}
	if b, _ := os.ReadFile(dot); !bytes.Equal(b, cur) {
		t.Error("restore wrote through a symlink")
	}
}

// Anything that runs in an automation directory counts as using it, not only
// processes named pig: a worker script, a node wrapper about to start pig. A
// "~" in its PIG_CODING_AGENT_DIR is expanded the way PiG does.
func TestOrphanUsedByAnyProcessIsKept(t *testing.T) {
	rc := newRealCase(t)
	rc.procs.list = []Proc{
		{PID: 50, Args: []string{"/bin/bash", "loop.sh"}, Cwd: filepath.Join(rc.home, "kagent-pr-agent"), Env: map[string]string{}},
		{PID: 51, Args: []string{"node", "w.js"}, Env: map[string]string{"HOME": rc.root, "PIG_CODING_AGENT_DIR": "~/pig/pigpen-agent"}},
	}
	p := mustPlan(t, mustCheck(t, rc.opts()))
	for _, g := range p.Groups {
		for _, op := range g.Ops {
			if strings.HasSuffix(op.Path, "/kagent-pr-agent") || strings.HasSuffix(op.Path, "/pigpen-agent") {
				t.Errorf("%s is in use but planned: %s", op.Path, op)
			}
		}
	}
}

// PiG expands a leading "~" in PIG_HOME, XDG_CONFIG_HOME and PIG_CODING_AGENT_DIR
// (codingagent.ExpandTildePath), and the doctor's paths are absolute.
func TestHomeAndAgentDirAreExpandedAndAbsolute(t *testing.T) {
	o, err := optionsFromEnv([]string{"HOME=/u", "PIG_HOME=~/p", "PIG_CODING_AGENT_DIR=~/.pig/x-agent"})
	if err != nil || o.Home != "/u/p" || o.AgentDir != "/u/.pig/x-agent" {
		t.Errorf("tilde not expanded: %q %q %v", o.Home, o.AgentDir, err)
	}
	o, _ = optionsFromEnv([]string{"HOME=/u", "XDG_CONFIG_HOME=~/cfg"})
	if o.Home != "/u/cfg/pig" {
		t.Errorf("XDG_CONFIG_HOME tilde not expanded: %q", o.Home)
	}
	f := newFixture(t)
	t.Chdir(f.root)
	o = f.opts()
	o.Home, o.AgentDir = "pig", "pig/agent"
	r := mustCheck(t, o)
	if r.Home != f.home || r.AgentDir != f.agent {
		t.Errorf("relative paths must be made absolute: %q %q", r.Home, r.AgentDir)
	}
}

// When a group fails half way, the CLI still prints the backup that holds what
// was already moved.
func TestCLIPrintsTheBackupWhenAGroupFails(t *testing.T) {
	rc := newRealCase(t)
	orphan := filepath.Join(rc.home, "kagent-pr-agent")
	renameFn = func(a, b string) error {
		if a == orphan {
			return errors.New("injected failure")
		}
		return os.Rename(a, b)
	}
	t.Cleanup(func() { renameFn = os.Rename })
	code, out, _ := runCLI(t, rc, "y\ny\n", "fix", "--group", "legacy,orphans")
	if code == 0 {
		t.Fatal("the failure must be reported")
	}
	if !strings.Contains(out, "Restore with: pig-doctor restore ") {
		t.Errorf("the backup of the completed part is not named:\n%s", out)
	}
}

// Probes run outside the caller's directory, so a go.mod there cannot make the
// go command switch or download a toolchain during a read-only check.
func TestProbesRunInANeutralDirectory(t *testing.T) {
	t.Chdir(t.TempDir())
	out, err := execCommand(nil, "/bin/sh", "-c", "pwd")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "/" {
		t.Errorf("probe ran in %q", strings.TrimSpace(out))
	}
}

// artifacts/piglets is PiG's managed Piglet Binary store: every build has a
// record in receipts/piglets, and PiG's piglet.List (coding/piglet/resolve.go)
// fails the whole inventory ("references missing managed artifact") when a
// build is gone, which breaks `pig piglet list/show/remove`. The doctor reports
// sizes and PiG's own command; it never moves a build.
func TestPigletArtifactsAreNeverMoved(t *testing.T) {
	rc := newRealCase(t)
	o := rc.opts()
	r := mustCheck(t, o)
	f := one(t, r, "cache.piglet-artifacts")
	if f.Safety != Manual || len(f.ops) != 0 || !strings.Contains(f.Fix, "pig piglet remove") {
		t.Errorf("artifacts must be a manual finding pointing at pig piglet remove: %+v", f)
	}
	p := mustPlan(t, r)
	for _, g := range p.Groups {
		for _, op := range g.Ops {
			if strings.Contains(op.Path, string(filepath.Separator)+"artifacts"+string(filepath.Separator)) {
				t.Errorf("a managed Piglet build is planned: %s", op)
			}
		}
	}
	before := snapshot(t, filepath.Join(rc.home, "artifacts"))
	if _, err := Apply(o, p, approveAll); err != nil {
		t.Fatal(err)
	}
	if d := diffSnapshots(before, snapshot(t, filepath.Join(rc.home, "artifacts"))); len(d) != 0 {
		t.Errorf("the managed Piglet store changed: %v", d)
	}
}

// Other processes' environments hold API keys. The process sources keep only
// the variables the doctor uses, so no secret is held in memory or passed on.
func TestProcSourcesKeepOnlyTheVariablesTheDoctorUses(t *testing.T) {
	root := t.TempDir()
	d := filepath.Join(root, "100")
	_ = os.MkdirAll(d, 0o755)
	_ = os.WriteFile(filepath.Join(d, "cmdline"), []byte("pig\x00"), 0o644)
	_ = os.WriteFile(filepath.Join(d, "environ"), []byte("ANTHROPIC_API_KEY=sk-SECRET\x00PIG_CODING_AGENT_DIR=/h/x-agent\x00HOME=/h\x00PIG_HOME=/h/.pig\x00"), 0o644)
	ps, err := (&procFS{Root: root}).List()
	if err != nil || len(ps) != 1 {
		t.Fatalf("%v %+v", err, ps)
	}
	want := map[string]string{"PIG_CODING_AGENT_DIR": "/h/x-agent", "HOME": "/h", "PIG_HOME": "/h/.pig"}
	if !reflect.DeepEqual(ps[0].Env, want) {
		t.Errorf("linux: %v", ps[0].Env)
	}
	exec := func(env []string, name string, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "eww" {
			return "/usr/local/bin/pig ANTHROPIC_API_KEY=sk-SECRET PIG_CODING_AGENT_DIR=/h/x-agent HOME=/h PIG_HOME=/h/.pig\n", nil
		}
		return "  100 /usr/local/bin/pig\n", nil
	}
	ps, err = (&psProcs{Exec: exec}).List()
	if err != nil || len(ps) != 1 {
		t.Fatalf("%v %+v", err, ps)
	}
	if !reflect.DeepEqual(ps[0].Env, want) {
		t.Errorf("ps: %v", ps[0].Env)
	}
}

// When settings.json cannot be read (invalid JSON, or a symlink the doctor does
// not follow), a directory it may reference is not an orphan.
func TestOrphansNeedReadableSettings(t *testing.T) {
	for name, write := range map[string]func(f *fixture){
		"invalid": func(f *fixture) {
			f.settings(`{"subagentDir": "` + filepath.Join(f.home, "foo-agent") + `",}`)
		},
		"symlink": func(f *fixture) {
			dot := f.put(filepath.Join(f.root, "dotfiles", "settings.json"), `{"subagentDir": "`+filepath.Join(f.home, "foo-agent")+`"}`)
			mustSymlink(t, dot, filepath.Join(f.agent, "settings.json"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.put("foo-agent/settings.json", "{}")
			f.age("foo-agent", 60)
			write(f)
			if fs := mustCheck(t, f.opts()).Find("orphan.agent-dir"); len(fs) != 0 {
				t.Errorf("an agent dir settings.json may reference is planned: %+v", fs)
			}
		})
	}
}

// A cross-device move whose copy succeeded but whose source could not be fully
// removed is still recorded, so the backup copy can be found and restored.
func TestPartialCrossDeviceMoveIsRecorded(t *testing.T) {
	f := newFixture(t)
	f.put("subagent-output/locked/out.txt", "output")
	f.age("subagent-output", 60)
	renameFn = func(string, string) error { return &os.LinkError{Op: "rename", Err: errCrossDevice} }
	t.Cleanup(func() { renameFn = os.Rename })
	locked := filepath.Join(f.home, "subagent-output", "locked")
	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = filepath.WalkDir(f.root, func(p string, d os.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				_ = os.Chmod(p, 0o755)
			}
			return nil
		})
	})
	o := f.opts()
	res, err := Apply(o, mustPlan(t, mustCheck(t, o), "orphans"), approveAll)
	if err == nil {
		t.Fatal("the incomplete removal must be reported")
	}
	if res == nil || res.Backup == "" {
		t.Fatalf("no backup named: %+v", res)
	}
	m, merr := readManifest(filepath.Join(f.user, ".pig-doctor-backup", res.Backup))
	if merr != nil || len(m.Entries) != 1 || m.Entries[0].Original != filepath.Join(f.home, "subagent-output") {
		t.Fatalf("the copied directory is not in the manifest: %+v %v", m, merr)
	}
	if !exists(filepath.Join(f.user, ".pig-doctor-backup", res.Backup, m.Entries[0].Backup, "locked", "out.txt")) {
		t.Error("the backup copy is missing")
	}
}

// settings.json may name a directory with "~" (PiG expands it); that is a reference too.
func TestOrphanReferencedWithTilde(t *testing.T) {
	f := newFixture(t)
	f.put("foo-agent/settings.json", "{}")
	f.age("foo-agent", 60)
	o := f.opts()
	o.UserHome = f.root // the PiG home is <user home>/pig
	f.settings(`{"subagentDir": "~/pig/foo-agent"}`)
	if fs := mustCheck(t, o).Find("orphan.agent-dir"); len(fs) != 0 {
		t.Errorf("a directory settings.json names as ~/pig/foo-agent is planned: %+v", fs)
	}
}
