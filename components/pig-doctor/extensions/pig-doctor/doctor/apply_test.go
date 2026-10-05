//go:build unix

package doctor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// approveAll confirms every group, including credentials (the explicit confirmation the CLI asks for).
func approveAll(Group) (bool, error) { return true, nil }

func safeOnly(g Group) (bool, error) { return g.Safety == SafeAuto, nil }

func protectedSnapshot(t *testing.T, rc *realCase) map[string]string {
	t.Helper()
	return snapshot(t, filepath.Join(rc.agent, "sessions"), filepath.Join(rc.agent, "skills"), filepath.Join(rc.agent, "prompts"),
		filepath.Join(rc.agent, "auth.json"), filepath.Join(rc.agent, "auth.json.lock"), filepath.Join(rc.agent, "trust.json"),
		filepath.Join(rc.agent, "models.json"), filepath.Join(rc.agent, "models-store.json"), filepath.Join(rc.agent, "AGENTS.md"),
		filepath.Join(rc.home, "harness-overlay"), filepath.Join(rc.home, "vendor.json"), filepath.Join(rc.home, "piglet-cli"),
		filepath.Join(rc.home, "receipts"), filepath.Join(rc.home, "marketplace.json"), filepath.Join(rc.home, "marketplace"),
		filepath.Join(rc.home, "recent-agent"), filepath.Join(rc.home, "cache/toolchains"), filepath.Join(rc.home, "state"),
		rc.pkgA, rc.pkgB)
}

func TestDryRunIsTheExactListAndChangesNothing(t *testing.T) {
	rc := newRealCase(t)
	r := mustCheck(t, rc.opts())
	p := mustPlan(t, r)
	before := snapshot(t, rc.root)
	text := p.DryRun()
	if d := diffSnapshots(before, snapshot(t, rc.root)); len(d) != 0 {
		t.Fatalf("dry run changed the tree: %v", d)
	}
	n := 0
	for _, g := range p.Groups {
		for _, op := range g.Ops {
			n++
			if !strings.Contains(text, op.String()) {
				t.Errorf("dry run lacks %q", op.String())
			}
		}
	}
	if n < 10 {
		t.Fatalf("plan has only %d ops", n)
	}
	lines := 0
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "- ") {
			lines++
		}
	}
	if lines != n {
		t.Errorf("dry run lists %d operation lines for %d ops:\n%s", lines, n, text)
	}
	if !strings.Contains(text, "remove packages[0]") || !strings.Contains(text, "settings.json") {
		t.Errorf("edit should be spelled out:\n%s", text)
	}
	if strings.Contains(text, canary) {
		t.Error("credential leaked")
	}
}

func TestFixRealCase(t *testing.T) {
	rc := newRealCase(t)
	o := rc.opts()
	o.IncludePigletCells = true
	r := mustCheck(t, o)
	p := mustPlan(t, r)
	protBefore := protectedSnapshot(t, rc)
	res, err := Apply(o, p, approveAll)
	if err != nil {
		t.Fatal(err)
	}
	if res.Backup == "" {
		t.Fatal("no backup timestamp")
	}
	backup := filepath.Join(rc.user, ".pig-doctor-backup", res.Backup)
	if !exists(filepath.Join(backup, "manifest.json")) {
		t.Fatal("no manifest")
	}

	// settings.json: only the duplicate entry is gone; everything else byte-identical.
	want := `{
    "defaultProvider": "anthropic",
    "theme": "dark",
    "packages": [
        "` + rc.pkgB + `"
    ],
    "pigHighScore": 12
}
`
	if got := rc.readSettings(); got != want {
		t.Errorf("settings.json:\n%s\nwant:\n%s", got, want)
	}

	// legacy files and orphans were moved, not deleted: they are in the backup.
	for _, rel := range []string{"extensions", "extensions.toml", "kagent-pr-agent", "pigpen-agent", "subagent-output", "worktree-claims", "launch-workers-agent"} {
		if exists(filepath.Join(rc.home, rel)) {
			t.Errorf("%s should have been moved away", rel)
		}
	}
	// caches: old entries deleted, protected kept.
	for _, rel := range []string{"cache/cells/go/old1", "cache/cells/go/old2", "cache/cells/go/incomplete-old", "cache/ext/001-aaaa", "cache/ext/api-surface-bbbb"} {
		if exists(filepath.Join(rc.home, rel)) {
			t.Errorf("%s should be pruned", rel)
		}
	}
	for _, rel := range []string{"cache/cells/go/recent", "cache/cells/go/incomplete-new", "cache/ext/recent-cccc"} {
		if !exists(filepath.Join(rc.home, rel)) {
			t.Errorf("%s must be kept", rel)
		}
	}
	if exists(filepath.Join(rc.home, "piglet-binary-cells/isolated/go/subagent-388b")) {
		t.Error("the unused Piglet Binary cell should be moved to the backup")
	}
	// PiG's managed Piglet builds are never moved (review finding: PiG's inventory needs them).
	for _, rel := range []string{"artifacts/piglets/kinsy/d1", "artifacts/piglets/kinsy/d2", "artifacts/piglets/pig-standard/d1", "artifacts/piglets/pig-standard/d2", "artifacts/piglets/pig-standard/d3"} {
		if !exists(filepath.Join(rc.home, rel)) {
			t.Errorf("%s must stay", rel)
		}
	}
	// credentials: the copy is gone and is NOT in the backup; the rest of the dir is.
	if err := filepath.WalkDir(backup, func(p string, d os.DirEntry, err error) error {
		if err == nil && strings.HasPrefix(d.Name(), "auth.json") && !strings.HasSuffix(d.Name(), ".lock") {
			t.Errorf("credential file ended up in the backup: %s", p)
		}
		if err == nil && !d.IsDir() {
			if b, _ := os.ReadFile(p); strings.Contains(string(b), canary) {
				t.Errorf("credential content in the backup: %s", p)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(backup, "files")) {
		t.Error("backup has no files directory")
	}

	// nothing the doctor must not touch changed.
	if d := diffSnapshots(protBefore, protectedSnapshot(t, rc)); len(d) != 0 {
		t.Errorf("protected or unknown items changed: %v", d)
	}
	// a second check finds none of the fixable problems any more.
	r2 := mustCheck(t, o)
	for _, f := range r2.Findings {
		if f.Safety != Manual && f.Safety != "" && len(f.ops) > 0 {
			t.Errorf("still fixable after fix: %s %v", f.ID, f.Paths)
		}
	}
	if len(r2.Find("pkg.duplicate")) != 0 || len(r2.Find("legacy.extensions-dir")) != 0 {
		t.Errorf("findings remain: %v", ids(r2))
	}
}

func TestSafeGroupsOnlyWithYes(t *testing.T) {
	rc := newRealCase(t)
	o := rc.opts()
	p := mustPlan(t, mustCheck(t, o))
	res, err := Apply(o, p, safeOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"legacy", "credentials", "orphans-sessions"} {
		if groupByID(p, id) == nil {
			t.Errorf("plan lacks group %s", id)
		}
	}
	// safe groups ran
	if exists(filepath.Join(rc.home, "kagent-pr-agent")) {
		t.Error("safe groups should have run")
	}
	if !strings.Contains(rc.readSettings(), "packages") || strings.Contains(rc.readSettings(), "utils/pig-stuff") {
		t.Error("the safe package dedupe should have run")
	}
	// confirm and credential groups did not
	// (caches too: the fixture's Go toolchain is broken, so pruning needs confirmation)
	for _, rel := range []string{"extensions", "extensions.toml", "pigpen-agent", "launch-workers-agent/auth.json", "artifacts/piglets/kinsy/d1", "cache/cells/go/old1"} {
		if !exists(filepath.Join(rc.home, rel)) {
			t.Errorf("%s needs confirmation and must not be touched by --yes", rel)
		}
	}
	if len(res.Skipped) == 0 {
		t.Error("skipped groups should be reported")
	}
}

func TestDeclinedGroupChangesNothing(t *testing.T) {
	rc := newRealCase(t)
	before := snapshot(t, rc.root)
	res, err := Apply(rc.opts(), mustPlan(t, mustCheck(t, rc.opts())), func(Group) (bool, error) { return false, nil })
	if err != nil {
		t.Fatal(err)
	}
	if d := diffSnapshots(before, snapshot(t, rc.root)); len(d) != 0 {
		t.Errorf("%v", d)
	}
	if res.Backup != "" {
		t.Errorf("no backup should be created when nothing is applied: %s", res.Backup)
	}
}

func TestCredentialsNeedTheirOwnConfirmation(t *testing.T) {
	rc := newRealCase(t)
	p := mustPlan(t, mustCheck(t, rc.opts()))
	var asked []string
	_, err := Apply(rc.opts(), p, func(g Group) (bool, error) {
		asked = append(asked, g.ID)
		return g.ID != "credentials", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(rc.home, "launch-workers-agent", "auth.json")) || !exists(filepath.Join(rc.home, "launch-workers-agent", "sessions")) {
		t.Error("declining the credentials group must leave the whole directory alone")
	}
	found := false
	for _, a := range asked {
		if a == "credentials" {
			found = true
		}
	}
	if !found {
		t.Errorf("credentials group was never asked: %v", asked)
	}
}

func TestLockedSettingsRefusesTheWholeRun(t *testing.T) {
	rc := newRealCase(t)
	p := mustPlan(t, mustCheck(t, rc.opts()))
	rc.holdLock(filepath.Join(rc.agent, "settings.json.lock"))
	before := snapshot(t, rc.root)
	_, err := Apply(rc.opts(), p, approveAll)
	var le *LockedError
	if !errors.Is(err, ErrLocked) || !errors.As(err, &le) || !strings.HasSuffix(le.Path, "settings.json.lock") {
		t.Fatalf("want a LockedError for settings.json.lock, got %v", err)
	}
	if d := diffSnapshots(before, snapshot(t, rc.root)); len(d) != 0 {
		t.Errorf("a refused run must change nothing: %v", d)
	}
}

func TestLockedCacheGCRefusesTheCacheGroup(t *testing.T) {
	rc := newRealCase(t)
	p := mustPlan(t, mustCheck(t, rc.opts()), "caches")
	rc.holdLock("cache/.gc.lock")
	_, err := Apply(rc.opts(), p, approveAll)
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("want ErrLocked, got %v", err)
	}
	if !exists(filepath.Join(rc.home, "cache/cells/go/old1")) {
		t.Error("nothing may be pruned while GC lock is held")
	}
}

func TestLockHeldInsideOrphanRefuses(t *testing.T) {
	rc := newRealCase(t)
	p := mustPlan(t, mustCheck(t, rc.opts()), "orphans")
	rc.holdLock(filepath.Join(rc.home, "kagent-pr-agent", "settings.json.lock"))
	_, err := Apply(rc.opts(), p, approveAll)
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("want ErrLocked, got %v", err)
	}
	if !exists(filepath.Join(rc.home, "kagent-pr-agent")) {
		t.Error("must not move a directory with a held lock")
	}
}

func TestEntryLeasedBetweenCheckAndFixIsKept(t *testing.T) {
	rc := newRealCase(t)
	p := mustPlan(t, mustCheck(t, rc.opts()), "caches")
	rc.holdLock("cache/cells/go/.locks/old1.usage.lock")
	res, err := Apply(rc.opts(), p, approveAll)
	if err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(rc.home, "cache/cells/go/old1")) {
		t.Error("an entry that gained a usage lease after check must be kept")
	}
	if exists(filepath.Join(rc.home, "cache/cells/go/old2")) {
		t.Error("the rest is still pruned")
	}
	skipped := false
	for _, o := range res.Ops {
		if strings.HasSuffix(o.Op.Path, "old1") && o.Status == "skipped" {
			skipped = true
		}
	}
	if !skipped {
		t.Errorf("skipped op should be reported: %+v", res.Ops)
	}
}

func TestGuardRefusesEverythingProtected(t *testing.T) {
	rc := newRealCase(t)
	o := rc.opts()
	outside := filepath.Join(rc.root, "outside")
	rc.put(filepath.Join(outside, "x"), "x")
	mustSymlink(t, outside, filepath.Join(rc.home, "cache/cells/go/sym"))
	mustSymlink(t, outside, filepath.Join(rc.home, "sneaky-agent"))
	targets := []struct {
		kind OpKind
		path string
	}{
		{OpDelete, filepath.Join(rc.agent, "auth.json")},
		{OpMove, filepath.Join(rc.agent, "auth.json")},
		{OpDelete, filepath.Join(rc.agent, "trust.json")},
		{OpDelete, filepath.Join(rc.agent, "models.json")},
		{OpDelete, filepath.Join(rc.agent, "models-store.json")},
		{OpMove, filepath.Join(rc.agent, "sessions")},
		{OpDelete, filepath.Join(rc.agent, "sessions", "--p--", "s.jsonl")},
		{OpMove, filepath.Join(rc.agent, "skills")},
		{OpMove, filepath.Join(rc.agent, "prompts")},
		{OpMove, rc.agent},
		{OpMove, rc.home},
		{OpDelete, rc.home},
		{OpMove, filepath.Join(rc.home, "harness-overlay")},
		{OpDelete, filepath.Join(rc.home, "vendor.json")},
		{OpMove, filepath.Join(rc.home, "piglet-cli")},
		{OpMove, filepath.Join(rc.home, "receipts")},
		{OpMove, filepath.Join(rc.home, "marketplace")},
		{OpMove, filepath.Join(rc.home, "recent-agent")}, // not old enough / not an orphan candidate at plan time
		{OpMove, filepath.Join(rc.home, "state")},
		{OpDelete, filepath.Join(rc.home, "cache", "toolchains")},
		{OpDelete, filepath.Join(rc.home, "cache", "cells", "go", "sym")},
		{OpMove, filepath.Join(rc.home, "sneaky-agent")},
		{OpDelete, filepath.Join(rc.home, "cache", "cells", "go", "..", "..", "..", "agent", "auth.json")},
		{OpDelete, outside},
		{OpMove, rc.pkgA},
		{OpDelete, filepath.Join(rc.home, "cache")},
		{OpDelete, filepath.Join(rc.home, "cache", "cells")},
	}
	before := snapshot(t, rc.root)
	for _, tg := range targets {
		p := &Plan{Home: rc.home, AgentDir: rc.agent, Groups: []Group{{ID: "caches", Title: "x", Safety: SafeAuto, Ops: []Op{{Kind: tg.kind, Path: tg.path}}}}}
		if tg.kind == OpMove {
			p.Groups[0].ID = "orphans"
		}
		if _, err := Apply(o, p, approveAll); err == nil {
			t.Errorf("%s %s must be refused", tg.kind, tg.path)
		}
	}
	// A settings edit anywhere but the agent's settings.json, and one that is not a package removal.
	for _, op := range []Op{
		{Kind: OpRemovePackage, Path: filepath.Join(rc.agent, "trust.json"), Source: "x"},
		{Kind: OpRemovePackage, Path: filepath.Join(rc.home, "extensions.toml"), Source: "x"},
		{Kind: "chmod", Path: filepath.Join(rc.home, "extensions")},
	} {
		p := &Plan{Home: rc.home, AgentDir: rc.agent, Groups: []Group{{ID: "packages", Title: "x", Safety: SafeAuto, Ops: []Op{op}}}}
		if _, err := Apply(o, p, approveAll); err == nil {
			t.Errorf("%s %s must be refused", op.Kind, op.Path)
		}
	}
	if d := diffSnapshots(before, snapshot(t, rc.root)); len(d) != 0 {
		t.Errorf("a refused op changed the tree: %v", d)
	}
}

func TestPlanForAnotherHomeIsRefused(t *testing.T) {
	rc := newRealCase(t)
	other := newFixture(t)
	p := mustPlan(t, mustCheck(t, rc.opts()))
	if _, err := Apply(other.opts(), p, approveAll); err == nil {
		t.Error("a plan made for one home must not be applied to another")
	}
}

func TestStaleSettingsEntryIsSkipped(t *testing.T) {
	rc := newRealCase(t)
	p := mustPlan(t, mustCheck(t, rc.opts()), "packages")
	rc.settings(`{"packages": ["` + rc.pkgB + `"]}`)
	res, err := Apply(rc.opts(), p, approveAll)
	if err != nil {
		t.Fatal(err)
	}
	if got := rc.readSettings(); got != `{"packages": ["`+rc.pkgB+`"]}` {
		t.Errorf("settings changed: %s", got)
	}
	if len(res.Ops) != 1 || res.Ops[0].Status != "skipped" {
		t.Errorf("%+v", res.Ops)
	}
}

func TestSettingsEditKeepsCommentsAndOtherKeys(t *testing.T) {
	f := newFixture(t)
	a := f.pkg(filepath.Join(f.user, "a", "p"), "p", "ask")
	b := f.pkg(filepath.Join(f.user, "b", "p"), "p", "ask", "grill")
	body := "{\r\n\t\"z\": {\"deep\": [1,2,3]},\r\n\t\"packages\": [ {\"source\": \"" + a + "\", \"extensions\": [\"extensions/ask\"]}, \"" + b + "\" ],\r\n\t\"a\": \"ü\"\r\n}"
	f.settings(body)
	p := mustPlan(t, mustCheck(t, f.opts()), "packages")
	if _, err := Apply(f.opts(), p, approveAll); err != nil {
		t.Fatal(err)
	}
	want := "{\r\n\t\"z\": {\"deep\": [1,2,3]},\r\n\t\"packages\": [ \"" + b + "\" ],\r\n\t\"a\": \"ü\"\r\n}"
	if got := f.readSettings(); got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestBackupDefaultsToUserHome(t *testing.T) {
	rc := newRealCase(t)
	p := mustPlan(t, mustCheck(t, rc.opts()), "legacy")
	res, err := Apply(rc.opts(), p, approveAll)
	if err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(rc.user, ".pig-doctor-backup", res.Backup, "manifest.json")) {
		t.Error("backups belong in ~/.pig-doctor-backup/<timestamp>/")
	}
	bs, err := ListBackups(rc.opts())
	if err != nil || len(bs) != 1 || bs[0].Timestamp != res.Backup {
		t.Errorf("%v %+v", err, bs)
	}
}

func TestRestore(t *testing.T) {
	rc := newRealCase(t)
	o := rc.opts()
	o.IncludePigletCells = true
	before := snapshot(t, rc.root)
	res, err := Apply(o, mustPlan(t, mustCheck(t, o)), approveAll)
	if err != nil {
		t.Fatal(err)
	}
	rr, err := Restore(o, res.Backup, false)
	if err != nil {
		t.Fatal(err)
	}
	after := snapshot(t, rc.root)
	// Deleted caches and the credential copy are not restorable; everything else is back exactly.
	for _, d := range diffSnapshots(before, after) {
		ok := strings.Contains(d, "/cache/cells/go/") || strings.Contains(d, "/cache/ext/") || strings.Contains(d, "launch-workers-agent/auth.json") || strings.Contains(d, ".pig-doctor-backup")
		if !ok {
			t.Errorf("not restored: %s", d)
		}
	}
	if len(rr.NotRestorable) == 0 {
		t.Error("restore should say what could not be restored")
	}
	if exists(filepath.Join(rc.home, "launch-workers-agent", "auth.json")) {
		t.Error("a deleted credential copy must not come back")
	}
	if len(rr.Conflict) != 0 {
		t.Errorf("%v", rr.Conflict)
	}
	for _, rel := range []string{"extensions/ask/go.mod", "pigpen-agent/sessions/--b--/s.jsonl", "artifacts/piglets/kinsy/d1/unversioned/linux-amd64/bin", "agent/extensions/mine/go.mod"} {
		if !exists(filepath.Join(rc.home, rel)) {
			t.Errorf("%s not restored", rel)
		}
	}
	if !strings.Contains(rc.readSettings(), "utils/pig-stuff") {
		t.Error("settings.json not restored")
	}
}

func TestRestoreRefusesToOverwrite(t *testing.T) {
	rc := newRealCase(t)
	res, err := Apply(rc.opts(), mustPlan(t, mustCheck(t, rc.opts()), "legacy", "packages"), approveAll)
	if err != nil {
		t.Fatal(err)
	}
	// The user has since recreated extensions/ and edited settings.json.
	rc.put("extensions/new/x", "mine")
	rc.settings(`{"changed": true}`)
	rr, err := Restore(rc.opts(), res.Backup, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rr.Conflict) < 2 {
		t.Errorf("both should conflict: %+v", rr)
	}
	if b, _ := os.ReadFile(filepath.Join(rc.home, "extensions", "new", "x")); string(b) != "mine" {
		t.Error("existing data was overwritten")
	}
	if rc.readSettings() != `{"changed": true}` {
		t.Error("edited settings.json was overwritten")
	}
	if exists(filepath.Join(rc.home, "extensions", "ask")) {
		t.Error("nothing restored into an occupied path")
	}
	// The other legacy file that had no conflict comes back.
	if !exists(filepath.Join(rc.home, "extensions.toml")) {
		t.Error("non-conflicting entries are restored")
	}
	// --force restores settings.json over the changed one, but never merges into an occupied directory.
	rr, err = Restore(rc.opts(), res.Backup, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rc.readSettings(), "utils/pig-stuff") {
		t.Error("force restores settings.json")
	}
	if exists(filepath.Join(rc.home, "extensions", "ask")) {
		t.Error("even --force never merges into an existing directory")
	}
}

func TestRestoreUnknownTimestamp(t *testing.T) {
	f := newFixture(t)
	for _, ts := range []string{"nope", "../../etc", "", "20260101T000000Z"} {
		if _, err := Restore(f.opts(), ts, false); err == nil {
			t.Errorf("restore %q should fail", ts)
		}
	}
}

func TestRestoreRefusesWhileSettingsLocked(t *testing.T) {
	rc := newRealCase(t)
	res, err := Apply(rc.opts(), mustPlan(t, mustCheck(t, rc.opts()), "packages"), approveAll)
	if err != nil {
		t.Fatal(err)
	}
	rc.holdLock(filepath.Join(rc.agent, "settings.json.lock"))
	if _, err := Restore(rc.opts(), res.Backup, false); !errors.Is(err, ErrLocked) {
		t.Errorf("want ErrLocked, got %v", err)
	}
}

func TestMoveAcrossDevicesCopiesFaithfully(t *testing.T) {
	f := newFixture(t)
	src := filepath.Join(f.home, "moveme")
	f.put(src+"/a/b.txt", "hello")
	f.put(src+"/run.sh", "#!/bin/sh\n")
	_ = os.Chmod(src+"/run.sh", 0o755)
	mustSymlink(t, "a/b.txt", src+"/link")
	before := snapshot(t, src)
	dst := filepath.Join(f.root, "backup", "x")
	renameFn = func(string, string) error { return &os.LinkError{Op: "rename", Err: errCrossDevice} }
	t.Cleanup(func() { renameFn = os.Rename })
	if err := movePath(src, dst); err != nil {
		t.Fatal(err)
	}
	if exists(src) {
		t.Error("source should be gone")
	}
	after := snapshot(t, dst)
	for k, v := range before {
		if after[strings.Replace(k, src, dst, 1)] != v {
			t.Errorf("%s: %q vs %q", k, v, after[strings.Replace(k, src, dst, 1)])
		}
	}
	// a credential inside is never copied
	f.put(f.home+"/withcred/auth.json", canary)
	if err := movePath(f.home+"/withcred", filepath.Join(f.root, "backup", "y")); err == nil {
		t.Error("the copy fallback must refuse to read a credential file")
	}
	if !exists(f.home + "/withcred/auth.json") {
		t.Error("a refused move must leave the source in place")
	}
}

func TestMovingACredentialDirectoryWithoutDeletingTheCredentialsIsRefused(t *testing.T) {
	rc := newRealCase(t)
	dir := filepath.Join(rc.home, "launch-workers-agent")
	p := &Plan{Home: rc.home, AgentDir: rc.agent, Groups: []Group{{ID: "orphans", Title: "x", Safety: SafeAuto, Ops: []Op{{Kind: OpMove, Path: dir}}}}}
	if _, err := Apply(rc.opts(), p, approveAll); err == nil || !strings.Contains(err.Error(), "credential") {
		t.Fatalf("want a refusal mentioning credentials, got %v", err)
	}
	if !exists(filepath.Join(dir, "auth.json")) || exists(filepath.Join(rc.user, ".pig-doctor-backup")) {
		t.Error("nothing may move and no backup may be created")
	}
}

func TestPlanRefusedWhenBackupWouldLiveInsideTheHome(t *testing.T) {
	rc := newRealCase(t)
	o := rc.opts()
	o.BackupRoot = filepath.Join(rc.home, "backup")
	if _, err := Apply(o, mustPlan(t, mustCheck(t, o), "legacy"), approveAll); err == nil {
		t.Fatal("a backup inside the PiG home could be pruned or moved by the next fix")
	}
	if !exists(filepath.Join(rc.home, "extensions")) {
		t.Error("nothing may move")
	}
}

func TestVerifyOnlyPackagesChangedCatchesABadEdit(t *testing.T) {
	before := []byte(`{"a":1,"packages":["x","y"]}`)
	entries, _ := parsePackages(before)
	for name, after := range map[string]string{
		"other key changed":     `{"a":2,"packages":["y"]}`,
		"key dropped":           `{"packages":["y"]}`,
		"wrong package removed": `{"a":1,"packages":["x"]}`,
		"nothing removed":       `{"a":1,"packages":["x","y"]}`,
		"everything removed":    `{"a":1,"packages":[]}`,
		"not json":              `{"a":1,`,
	} {
		if err := verifyOnlyPackagesChanged(before, []byte(after), entries, []int{0}); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
	if err := verifyOnlyPackagesChanged(before, []byte(`{"a":1,"packages":["y"]}`), entries, []int{0}); err != nil {
		t.Errorf("a correct edit passes: %v", err)
	}
}

// The structural guard alone (later layers, such as the candidate lookup, would also refuse most of these).
func TestStructuralGuardAlone(t *testing.T) {
	rc := newRealCase(t)
	inv, err := scan(withDefaults(rc.opts()))
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(rc.root, "outside")
	rc.put(filepath.Join(outside, "x"), "x")
	mustSymlink(t, outside, filepath.Join(rc.home, "cache/cells/go/sym"))
	mustSymlink(t, outside, filepath.Join(rc.home, "sneaky-agent"))
	rc.put(filepath.Join(rc.home, "x-agent", "sessions", "a.jsonl"), "{}")
	rc.put(filepath.Join(rc.home, "x-agent", "trust.json"), "{}")
	rc.put(filepath.Join(rc.home, "x-agent", "models.json"), "{}")
	bad := []Op{
		{Kind: OpDelete, Path: filepath.Join(rc.agent, "auth.json")},
		{Kind: OpMove, Path: filepath.Join(rc.agent, "sessions")},
		{Kind: OpDelete, Path: filepath.Join(rc.agent, "extensions", "mine", "go.mod")},
		{Kind: OpDelete, Path: filepath.Join(rc.home, "x-agent", "trust.json")},
		{Kind: OpMove, Path: filepath.Join(rc.home, "x-agent", "models.json")},
		{Kind: OpDelete, Path: filepath.Join(rc.home, "x-agent", "sessions", "a.jsonl")},
		{Kind: OpDelete, Path: outside},
		{Kind: OpMove, Path: rc.home},
		{Kind: OpDelete, Path: filepath.Join(rc.home, "cache", "cells", "go", "sym")},
		{Kind: OpMove, Path: filepath.Join(rc.home, "sneaky-agent")},
		{Kind: OpDelete, Path: filepath.Join(rc.home, "cache", "cells", "go", "..", "..", "..", "agent", "auth.json")},
		{Kind: OpRemovePackage, Path: filepath.Join(rc.agent, "trust.json"), Source: "x"},
		{Kind: "chmod", Path: filepath.Join(rc.home, "extensions")},
	}
	for _, op := range bad {
		if err := inv.guard(op); err == nil {
			t.Errorf("guard must refuse %s", op)
		}
	}
	good := []Op{
		{Kind: OpDelete, Path: filepath.Join(rc.home, "launch-workers-agent", "auth.json")},
		{Kind: OpDelete, Path: filepath.Join(rc.home, "cache", "cells", "go", "old1")},
		{Kind: OpMove, Path: filepath.Join(rc.home, "extensions")},
		{Kind: OpRemovePackage, Path: filepath.Join(rc.agent, "settings.json"), Source: "x"},
	}
	for _, op := range good {
		if err := inv.guard(op); err != nil {
			t.Errorf("guard must accept %s: %v", op, err)
		}
	}
}

func TestRestoreRejectsATimestampThatIsAPath(t *testing.T) {
	rc := newRealCase(t)
	// A directory outside the backup root that looks like a valid backup for this home.
	evil := filepath.Join(rc.root, "evil")
	rc.put(filepath.Join(evil, "manifest.json"), `{"version":1,"home":"`+rc.home+`","entries":[{"kind":"move","original":"`+filepath.Join(rc.home, "extensions", "planted")+`","backup":"files/x"}]}`)
	rc.put(filepath.Join(evil, "files", "x"), "planted")
	rc.mkdir(filepath.Join(rc.user, ".pig-doctor-backup"))
	if _, err := Restore(rc.opts(), "../../evil", false); err == nil {
		t.Fatal("a path is not a backup timestamp")
	}
	if exists(filepath.Join(rc.home, "extensions", "planted")) {
		t.Error("a planted manifest must never be restored")
	}
}
