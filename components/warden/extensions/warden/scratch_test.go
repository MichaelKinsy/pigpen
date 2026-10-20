package warden

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// scratchBase is a real directory under /tmp for scratch cases (removed by the test).
func scratchBase(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "pi-warden-scratch-")
	if err != nil {
		t.Skip("no /tmp:", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func realOf(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// records are session records for real paths, with the identity each has now.
func records(t *testing.T, paths ...string) ScratchRecords {
	t.Helper()
	out := ScratchRecords{}
	for _, p := range paths {
		id, ok := scratchIdentity(p)
		if !ok {
			t.Fatalf("no identity for %s", p)
		}
		out[p] = id
	}
	return out
}

func destructiveRm(hits []PatternHit) bool {
	for _, h := range hits {
		if h.ID == "rm-recursive-dangerous-target" && h.Severity == SeverityDestructive {
			return true
		}
	}
	return false
}

func rmHits(command string, scratch ScratchRecords, platform string) []PatternHit {
	return MatchPatterns("bash", map[string]any{"command": command}, cwd, &PatternOptions{Scratch: scratch, Platform: platform})
}

func hitIDs(hits []PatternHit) []string {
	out := []string{}
	for _, h := range hits {
		out = append(out, h.ID)
	}
	return out
}

func mkdirs(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func skipWithoutBirthtime(t *testing.T, dir string) {
	t.Helper()
	if id, ok := scratchIdentity(dir); !ok || !(id.BirthtimeMs > 0) {
		t.Skip("this file system reports no birth time")
	}
}

// twin: tests/guard.test.ts:247 "session scratch: an rm of a recorded temp directory is risky, not destructive"
func TestSessionScratchAnRmOfARecordedTempDirectoryIsRisky(t *testing.T) {
	base := scratchBase(t)
	probe := filepath.Join(base, "probe-abc")
	mkdirs(t, probe)
	skipWithoutBirthtime(t, probe)
	scratch := records(t, realOf(t, probe))
	hits := rmHits("rm -rf "+probe, scratch, "darwin")
	if len(hits) != 1 || hits[0].ID != "rm-session-scratch" || hits[0].Severity != SeverityRisky {
		t.Fatalf("%+v", hits)
	}
	if !destructiveRm(rmHits("rm -rf "+probe, nil, "")) {
		t.Fatal("without the session set it stays destructive")
	}
}

// twin: tests/guard.test.ts:259 "session scratch: a recorded path replaced by a new directory stays destructive"
func TestSessionScratchAReplacedPathStaysDestructive(t *testing.T) {
	base := scratchBase(t)
	probe := filepath.Join(base, "probe-abc")
	mkdirs(t, probe)
	skipWithoutBirthtime(t, probe)
	scratch := records(t, realOf(t, probe))
	if err := os.RemoveAll(probe); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	mkdirs(t, probe)
	if !destructiveRm(rmHits("rm -rf "+probe, scratch, "darwin")) {
		t.Fatal("a different directory at the recorded path")
	}
	pruneScratch(scratch)
	if len(scratch) != 0 {
		t.Fatal("the stale record is not dropped")
	}
}

// twin: tests/guard.test.ts:274 "session scratch: older content moved into a recorded directory stays destructive"
func TestSessionScratchMovedInContentStaysDestructive(t *testing.T) {
	base := scratchBase(t)
	important := filepath.Join(base, "important")
	mkdirs(t, important)
	skipWithoutBirthtime(t, important)
	if err := os.WriteFile(filepath.Join(important, "keep.txt"), []byte("keep me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	dir := filepath.Join(base, "S", "x")
	mkdirs(t, dir)
	scratch := records(t, realOf(t, filepath.Join(base, "S")), realOf(t, dir))
	if destructiveRm(rmHits("rm -rf "+dir, scratch, "darwin")) {
		t.Fatal("empty scratch is not destructive")
	}
	if err := os.Rename(important, filepath.Join(dir, "important")); err != nil {
		t.Fatal(err)
	}
	if !destructiveRm(rmHits("rm -rf "+dir, scratch, "darwin")) {
		t.Fatal("moved content keeps its older birth time")
	}
	if !destructiveRm(rmHits("rm -rf "+filepath.Join(dir, "important"), scratch, "darwin")) {
		t.Fatal("the moved directory itself")
	}
}

// twin: tests/guard.test.ts:291 "session scratch: a symlink inside scratch is checked as a link, never followed"
func TestSessionScratchASymlinkIsCheckedAsALink(t *testing.T) {
	base := scratchBase(t)
	dir := filepath.Join(base, "x")
	mkdirs(t, dir)
	skipWithoutBirthtime(t, dir)
	if err := os.Symlink(cwd, filepath.Join(dir, "out")); err != nil {
		t.Skip("no symlinks:", err)
	}
	scratch := records(t, realOf(t, dir))
	mustEqual(t, hitIDs(rmHits("rm -rf "+dir, scratch, "darwin")), []string{"rm-session-scratch"}, "ids")
}

// twin: tests/guard.test.ts:302 "session scratch: a tree past the walk budget is not scratch"
func TestSessionScratchATreePastTheWalkBudgetIsNotScratch(t *testing.T) {
	base := scratchBase(t)
	skipWithoutBirthtime(t, base)
	for _, name := range []string{"a", "b", "c"} {
		if err := os.WriteFile(filepath.Join(base, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	root := realOf(t, base)
	id, _ := scratchIdentity(root)
	born := id.BirthtimeMs
	if !bornAfter(root, born, &scratchBudget{entries: 4, deadline: time.Now().Add(time.Second)}) {
		t.Fatal("within the budget")
	}
	if bornAfter(root, born, &scratchBudget{entries: 3, deadline: time.Now().Add(time.Second)}) {
		t.Fatal("entry bound")
	}
	if bornAfter(root, born, &scratchBudget{entries: 100, deadline: time.Now().Add(-time.Millisecond)}) {
		t.Fatal("time bound")
	}
	if bornAfter(filepath.Join(base, "missing"), born, nil) {
		t.Fatal("a missing root")
	}
}

// twin: tests/guard.test.ts:314 "session scratch: without birth time, mktemp beside a listing records nothing"
func TestSessionScratchWithoutBirthTimeMktempBesideAListing(t *testing.T) {
	base := scratchBase(t)
	existing := realOf(t, mustMkdirTemp(t, base, "existing-"))
	made := realOf(t, mustMkdirTemp(t, base, "tmp."))
	noBirth := func(string, os.FileInfo) float64 { return 0 }
	started := time.Now().UnixMilli() - 1000
	listing := createdScratch("bash", map[string]any{"command": "mktemp -d; ls -d " + base + "/*"}, made+"\n"+existing+"\n", started, nil, noBirth, "darwin")
	if len(listing) != 0 {
		t.Fatalf("%v", listing)
	}
	if !destructiveRm(rmHits("rm -rf "+existing, listing, "darwin")) {
		t.Fatal("the pre-existing directory stays destructive")
	}
	keys := func(r ScratchRecords) []string {
		out := []string{}
		for k := range r {
			out = append(out, k)
		}
		return out
	}
	mustEqual(t, keys(createdScratch("bash", map[string]any{"command": "mktemp -d"}, made+"\n", started, nil, noBirth, "darwin")), []string{made}, "mktemp alone")
	mustEqual(t, keys(createdScratch("bash", map[string]any{"command": `d=$(mktemp -d) && echo "$d"`}, made+"\n", started, nil, noBirth, "darwin")), []string{made}, "an assignment and an echo of it")
	if n := len(createdScratch("bash", map[string]any{"command": "mktemp -d; echo done"}, made+"\ndone\n", started, nil, noBirth, "darwin")); n != 0 {
		t.Fatalf("anything else in the command: %d", n)
	}
}

func mustMkdirTemp(t *testing.T, dir, pattern string) string {
	t.Helper()
	p, err := os.MkdirTemp(dir, pattern)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// twin: tests/guard.test.ts:330 "session scratch: mktempOnly accepts only commands that make temp paths and print them"
func TestSessionScratchMktempOnly(t *testing.T) {
	if mktempOnly("mktemp -d") != 1 || mktempOnly("mktemp -d -t probe.XXXXXX") != 1 || mktempOnly(`a=$(mktemp -d); b="$(mktemp)"; echo $a ${b}`) != 2 {
		t.Fatal("accepted commands")
	}
	for _, command := range []string{"mktemp -u", "mktemp --dry-run", "mktemp -d; ls /tmp", "a=$(mktemp -d); echo $HOME", "echo /tmp/x", "mktemp -d $TMPDIR/x.XXXX", "a=$(mktemp -d) && cp -r ~/src $a"} {
		if n := mktempOnly(command); n != 0 {
			t.Errorf("%s: %d", command, n)
		}
	}
}

// twin: tests/guard.test.ts:339 "session scratch: a privileged rm of scratch stays destructive"
func TestSessionScratchAPrivilegedRmStaysDestructive(t *testing.T) {
	base := scratchBase(t)
	probe := filepath.Join(base, "probe-abc")
	mkdirs(t, probe)
	skipWithoutBirthtime(t, probe)
	scratch := records(t, realOf(t, probe))
	for _, command := range []string{"sudo rm -rf " + probe, "doas rm -rf " + probe, `su -c "rm -rf ` + probe + `"`, "sudo -u root rm -rf " + probe, "sudo bash <<EOF\nrm -rf " + probe + "\nEOF"} {
		if !destructiveRm(rmHits(command, scratch, "darwin")) {
			t.Errorf("expected destructive for: %s", command)
		}
	}
	if destructiveRm(rmHits("rm -rf "+probe, scratch, "darwin")) {
		t.Fatal("the same rm without sudo is scratch")
	}
}

// twin: tests/guard.test.ts:352 "session scratch: a command that moves, links, copies, or extracts data in before its rm stays destructive"
func TestSessionScratchMoveInCommandsStayDestructive(t *testing.T) {
	base := scratchBase(t)
	probe := filepath.Join(base, "probe-abc")
	mkdirs(t, probe)
	skipWithoutBirthtime(t, probe)
	scratch := records(t, realOf(t, probe))
	for _, command := range []string{
		"mv ~/work " + probe + "/ && rm -rf " + probe,
		"ln -s ~/work " + probe + "/w; rm -rf " + probe + "/w/",
		"rsync -a --remove-source-files ~/work/ " + probe + "/ && rm -rf " + probe,
		"mount -t nfs host:/data " + probe + " && rm -rf " + probe,
		"hdiutil attach disk.dmg -mountpoint " + probe + " && rm -rf " + probe,
		"bindfs ~/work " + probe + " && rm -rf " + probe,
		`\mv ~/work ` + probe + "/ && rm -rf " + probe,
		`"ln" -s ~/work ` + probe + "/w && rm -rf " + probe + "/w/",
		"l''n -s ~/work " + probe + "/w && rm -rf " + probe + "/w/",
		"/bin/mv ~/work " + probe + "/ && rm -rf " + probe,
		"cp -R ~/work " + probe + "/ && rm -rf " + probe,
		"cp -a ~/work " + probe + "/ && rm -rf " + probe,
		"tar -xf ~/work.tar -C " + probe + " && rm -rf " + probe,
		"tar xf ~/work.tar -C " + probe + " && rm -rf " + probe,
		"git clone ~/work " + probe + "/w && rm -rf " + probe,
		"git -C ~ clone ~/work " + probe + "/w && rm -rf " + probe,
		"(cd ~ && mv work " + probe + "/) && rm -rf " + probe,
	} {
		if !destructiveRm(rmHits(command, scratch, "darwin")) {
			t.Errorf("expected destructive for: %s", command)
		}
	}
	mustEqual(t, hitIDs(rmHits("rm -rf "+probe, scratch, "darwin")), []string{"rm-session-scratch"}, "a plain rm of recorded scratch is still released")
}

// twin: tests/guard.test.ts:384 "session scratch: on linux a recorded mkdir then rm -rf stays destructive"
func TestSessionScratchOnLinuxAMkdirThenRmStaysDestructive(t *testing.T) {
	base := scratchBase(t)
	skipWithoutBirthtime(t, base)
	probe := filepath.Join(base, "probe-abc")
	input := map[string]any{"command": "mkdir -p " + probe}
	if c := scratchCandidates("bash", input, cwd, "linux"); len(c) != 0 {
		t.Fatalf("no candidates on linux: %v", c)
	}
	candidates := scratchCandidates("bash", input, cwd, "darwin")
	mkdirs(t, probe)
	started := time.Now().UnixMilli() - 1000
	if n := len(createdScratch("bash", input, "", started, candidates, nil, "linux")); n != 0 {
		t.Fatalf("nothing recorded on linux: %d", n)
	}
	scratch := createdScratch("bash", input, "", started, candidates, nil, "darwin")
	if _, ok := scratch[realOf(t, probe)]; !ok {
		t.Fatalf("%v", scratch)
	}
	if !destructiveRm(rmHits("rm -rf "+probe, scratch, "linux")) {
		t.Fatal("linux keeps the hold")
	}
	if destructiveRm(rmHits("rm -rf "+probe, scratch, "darwin")) {
		t.Fatal("darwin exempts it")
	}
}

// twin: tests/guard.test.ts:400 "session scratch: a parent escape out of a recorded directory stays destructive"
func TestSessionScratchAParentEscapeStaysDestructive(t *testing.T) {
	base := scratchBase(t)
	probe := filepath.Join(base, "probe-abc")
	mkdirs(t, probe)
	skipWithoutBirthtime(t, probe)
	scratch := records(t, realOf(t, probe))
	if !destructiveRm(rmHits("rm -rf "+probe+"/../../etc", scratch, "darwin")) || !destructiveRm(rmHits("rm -rf "+probe+"/../probe-abc", scratch, "darwin")) {
		t.Fatal("any `..` segment keeps the hold")
	}
}

// twin: tests/guard.test.ts:411 "session scratch: a temp root, a variable, or a wildcard stays destructive"
func TestSessionScratchATempRootAVariableOrAWildcardStaysDestructive(t *testing.T) {
	base := scratchBase(t)
	skipWithoutBirthtime(t, base)
	scratch := records(t, realOf(t, base), realOf(t, "/tmp"), realOf(t, os.TempDir()))
	for _, command := range []string{"rm -rf /tmp", "rm -rf /tmp/", `rm -rf "$TMPDIR"`, "rm -rf /tmp/*", "rm -rf " + base + "/*", "rm -rf " + base + "/$NAME", "rm -rf " + base + "/$(echo x)", "rm -rf " + realOf(t, os.TempDir())} {
		if !destructiveRm(rmHits(command, scratch, "darwin")) {
			t.Errorf("expected destructive for: %s", command)
		}
	}
}

// twin: tests/guard.test.ts:421 "session scratch: a relative project path keeps its current classification"
func TestSessionScratchARelativeProjectPathKeepsItsClassification(t *testing.T) {
	base := scratchBase(t)
	skipWithoutBirthtime(t, base)
	scratch := records(t, realOf(t, base))
	for _, c := range [][2]string{{"rm -rf dist", "rm-rf"}, {"rm -r build", "rm-recursive"}, {"rm -rf ../sibling", "rm-recursive-dangerous-target"}} {
		with, without := hitIDs(rmHits(c[0], scratch, "darwin")), hitIDs(rmHits(c[0], nil, ""))
		mustEqual(t, with, without, c[0])
		if !contains(with, c[1]) {
			t.Errorf("%s is %s: %v", c[0], c[1], with)
		}
	}
	if !destructiveRm(rmHits("rm -rf "+base+" dist", scratch, "darwin")) {
		t.Fatal("a relative target next to scratch keeps the hold")
	}
}

// twin: tests/guard.test.ts:433 "session scratch: the judge reads the scratch fact in floor_hits"
func TestSessionScratchTheJudgeReadsTheScratchFact(t *testing.T) {
	base := scratchBase(t)
	skipWithoutBirthtime(t, base)
	scratch := records(t, realOf(t, base))
	judge := actionJudge(0.2, 0.1, "", nil)
	v := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "rm -rf " + base}, Task: "run the tests"}, EvaluateOptions{Config: cfg(), Judge: judge, GuardExtras: GuardExtras{Scratch: scratch, Platform: "darwin"}})
	got, _ := judge.calls()[0].State["floor_hits"].(string)
	if got != "recursive rm of session scratch: every target is under the temp directory and was created in this session [risky]" {
		t.Fatalf("%q", got)
	}
	if v.Level == LevelConfirm {
		t.Fatalf("%v", v.Reasons)
	}
}

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// twin: tests/guard.test.ts:738 "hostPaths reads PI_WARDEN_HOST_PATHS and ignores relative entries, empty entries, and /"
func TestHostPathsReadsTheEnvironment(t *testing.T) {
	host := mustMkdirTemp(t, os.TempDir(), "pi-warden-host-")
	defer os.RemoveAll(host)
	if len(hostPaths(envOf(nil))) != 0 || len(hostPaths(envOf(map[string]string{hostPathsEnv: ""}))) != 0 {
		t.Fatal("unset is off")
	}
	got := hostPaths(envOf(map[string]string{hostPathsEnv: "relative/dir::/:" + host + "/../" + filepath.Base(host)}))
	mustEqual(t, got, []string{realOf(t, host)}, "relative, empty, and / entries are dropped; .. is resolved")
}

func writeAt(path string, extras GuardExtras) Verdict {
	return eval(ActionInput{Tool: "write", Input: map[string]any{"path": path, "content": "v2"}, Task: "revise the draft"}, EvaluateOptions{Config: cfg(), GuardExtras: extras})
}

// twin: tests/guard.test.ts:749 "evaluateAction: an overwrite in a host path is not held by the outside-project rule; outside every host path it still is"
func TestAnOverwriteInAHostPathIsNotHeld(t *testing.T) {
	host := mustMkdirTemp(t, os.TempDir(), "pi-warden-host-")
	other := mustMkdirTemp(t, os.TempDir(), "pi-warden-other-")
	escape := filepath.Join(os.TempDir(), "pi-warden-host-escape-"+itoaInt(os.Getpid()))
	defer os.RemoveAll(host)
	defer os.RemoveAll(other)
	defer os.Remove(escape)
	for _, p := range []string{filepath.Join(host, "draft.md"), filepath.Join(other, "draft.md"), escape} {
		if err := os.WriteFile(p, []byte("v1"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	on := GuardExtras{HostPaths: hostPaths(envOf(map[string]string{hostPathsEnv: "relative:/:" + host}))}
	inside := writeAt(filepath.Join(host, "draft.md"), on)
	if inside.Level != LevelAllow || inside.Summary.Location != "outside_project" {
		t.Fatalf("inside: %s %s", inside.Level, inside.Summary.Location)
	}
	if v := writeAt(filepath.Join(host, "notes", "new.md"), on); v.Level != LevelAllow {
		t.Fatal("a new file in a host path is not warned either")
	}
	if v := writeAt(filepath.Join(other, "draft.md"), on); v.Level != LevelConfirm {
		t.Fatal("outside every host path is still held")
	}
	if v := writeAt(filepath.Join(host, "..", "pi-warden-host-escape-"+itoaInt(os.Getpid())), on); v.Level != LevelConfirm {
		t.Fatal("`..` out of a host path is outside")
	}
	if err := os.Symlink(other, filepath.Join(host, "link")); err != nil {
		t.Skip("no symlinks:", err)
	}
	if v := writeAt(filepath.Join(host, "link", "draft.md"), on); v.Level != LevelConfirm {
		t.Fatal("a symlink out of a host path is outside")
	}
	if v := writeAt(filepath.Join(host, "draft.md"), GuardExtras{}); v.Level != LevelConfirm {
		t.Fatal("without host paths nothing changes")
	}
	if v := writeAt(filepath.Join(host, "draft.md"), GuardExtras{HostPaths: hostPaths(envOf(nil))}); v.Level != LevelConfirm {
		t.Fatal("an unset variable changes nothing")
	}
}

// twin: tests/guard.test.ts:777 "evaluateAction: an overwrite of a /warden index file is not held; the rest of the agent directory still is"
func TestAnOverwriteOfAnIndexFileIsNotHeld(t *testing.T) {
	agent := mustMkdirTemp(t, os.TempDir(), "pi-warden-agent-")
	defer os.RemoveAll(agent)
	mkdirs(t, filepath.Join(agent, "pi-warden", "index", "projects"), filepath.Join(agent, "other-extension"))
	for _, file := range []string{"pi-warden/index/global.json", "pi-warden/index/projects/0123456789ab.json", "pi-warden/config.json", "auth.json", "settings.json", "other-extension/data.json"} {
		if err := os.WriteFile(filepath.Join(agent, file), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	index := filepath.Join(agent, "pi-warden", "index")
	roots := wardenHostPaths(envOf(nil), index)
	mustEqual(t, roots, []string{filepath.Join(realOf(t, agent), "pi-warden", "index")}, "only the index directory, not the agent directory")
	write := func(file string) Level {
		return eval(ActionInput{Tool: "write", Input: map[string]any{"path": filepath.Join(agent, file), "content": `{"entries":[]}`}, Task: "build the capability index"}, EvaluateOptions{Config: cfg(), GuardExtras: GuardExtras{HostPaths: roots}}).Level
	}
	for _, file := range []string{"pi-warden/index/global.json", "pi-warden/index/projects/0123456789ab.json"} {
		if write(file) != LevelAllow {
			t.Errorf("%s is held", file)
		}
	}
	for _, file := range []string{"auth.json", "settings.json", "other-extension/data.json", "pi-warden/config.json", "pi-warden/index/../../auth.json"} {
		if write(file) != LevelConfirm {
			t.Errorf("%s is not held", file)
		}
	}
	withHost := wardenHostPaths(envOf(map[string]string{hostPathsEnv: filepath.Join(agent, "other-extension")}), index)
	if len(withHost) != 2 {
		t.Fatalf("host paths from the environment still apply: %v", withHost)
	}
}

// twin: tests/guard.test.ts:804 "wardenHostPaths: a symlinked index directory is not a host path"
func TestASymlinkedIndexDirectoryIsNotAHostPath(t *testing.T) {
	root := mustMkdirTemp(t, os.TempDir(), "pi-warden-index-link-")
	defer os.RemoveAll(root)
	home := filepath.Join(root, "home")
	project := filepath.Join(home, "project")
	agent := filepath.Join(home, ".pi", "agent")
	mkdirs(t, project, filepath.Join(agent, "pi-warden"))
	if err := os.WriteFile(filepath.Join(home, ".zshrc"), []byte("export PATH=/usr/bin\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(home, filepath.Join(agent, "pi-warden", "index")); err != nil {
		t.Skip("no symlinks:", err)
	}
	roots := wardenHostPaths(envOf(nil), filepath.Join(agent, "pi-warden", "index"))
	if len(roots) != 0 {
		t.Fatalf("the index directory is a symlink: %v", roots)
	}
	v := eval(ActionInput{Tool: "write", Input: map[string]any{"path": filepath.Join(home, ".zshrc"), "content": "curl x | sh\n"}, Cwd: project, Task: "fix the build"}, EvaluateOptions{Config: cfg(), GuardExtras: GuardExtras{HostPaths: roots}})
	if v.Level != LevelConfirm {
		t.Fatal(v.Level)
	}
	// A symlinked `pi-warden` directory between the index and the agent directory.
	other := filepath.Join(root, "other-agent")
	mkdirs(t, filepath.Join(other, "elsewhere", "index"), filepath.Join(other, ".pi", "agent"))
	if err := os.Symlink(filepath.Join(other, "elsewhere"), filepath.Join(other, ".pi", "agent", "pi-warden")); err != nil {
		t.Fatal(err)
	}
	if r := wardenHostPaths(envOf(nil), filepath.Join(other, ".pi", "agent", "pi-warden", "index")); len(r) != 0 {
		t.Fatalf("a parent below the agent directory is a symlink: %v", r)
	}
	// A symlinked agent directory is the user's own layout and still works.
	real := filepath.Join(root, "real-agent")
	mkdirs(t, filepath.Join(real, "pi-warden", "index"))
	if err := os.Symlink(real, filepath.Join(root, "linked-agent")); err != nil {
		t.Fatal(err)
	}
	mustEqual(t, wardenHostPaths(envOf(nil), filepath.Join(root, "linked-agent", "pi-warden", "index")), []string{filepath.Join(realOf(t, real), "pi-warden", "index")}, "linked agent dir")
}

// twin: tests/guard.test.ts:858 "evaluateAction: path rules, deny rules, and sensitive paths still fire in a host path"
func TestPathRulesDenyRulesAndSensitivePathsStillFireInAHostPath(t *testing.T) {
	host := mustMkdirTemp(t, os.TempDir(), "pi-warden-host-")
	defer os.RemoveAll(host)
	if err := os.WriteFile(filepath.Join(host, "draft.md"), []byte("v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	on := GuardExtras{HostPaths: hostPaths(envOf(map[string]string{hostPathsEnv: host}))}
	blocked := cfg()
	blocked.PathRules = []PathRule{{ID: "no-drafts", Paths: []string{"**/draft.md"}, Access: "none", Tools: []string{"write", "edit"}, Action: "block"}}
	if v := eval(ActionInput{Tool: "write", Input: map[string]any{"path": filepath.Join(host, "draft.md"), "content": "v2"}, Task: "x"}, EvaluateOptions{Config: blocked, GuardExtras: on}); v.Level != LevelDeny {
		t.Fatalf("a path rule still blocks: %s", v.Level)
	}
	denyRule := cfg()
	denyRule.CommandDenyRules = []CommandRule{{ID: "no-host-rm", Pattern: `\brm\b`, Severity: "deny"}}
	if v := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "rm " + filepath.Join(host, "draft.md")}, Task: "x"}, EvaluateOptions{Config: denyRule, GuardExtras: on}); v.Level != LevelDeny {
		t.Fatalf("a deny rule still blocks: %s", v.Level)
	}
	if v := eval(ActionInput{Tool: "write", Input: map[string]any{"path": filepath.Join(host, ".env"), "content": "A=1"}, Task: "x"}, EvaluateOptions{Config: cfg(), GuardExtras: on}); v.Level == LevelAllow {
		t.Fatal("a sensitive path is still flagged")
	}
}
