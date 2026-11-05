package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"pigtorio/internal/hc"
)

// gateHC lets a test hold `hc send` and override what a fresh `hc get` says.
type gateHC struct {
	*fakeHC
	mu     sync.Mutex
	fresh  map[string]string // Get status override
	gate   chan struct{}     // Send waits for it when set
	lastIf string
}

func (g *gateHC) Get(addr string) (hc.Term, error) {
	t, err := g.fakeHC.Get(addr)
	g.mu.Lock()
	defer g.mu.Unlock()
	if s, ok := g.fresh[addr]; ok {
		t.Status = s
	}
	return t, err
}

func (g *gateHC) Send(addr, text, ifStatus string) error {
	g.mu.Lock()
	gate := g.gate
	g.lastIf = ifStatus
	g.mu.Unlock()
	if gate != nil {
		<-gate
	}
	return g.fakeHC.Send(addr, text, ifStatus)
}

func TestBriefGoesOnlyToAgentsAtThePrompt(t *testing.T) {
	r := newRig(t, "true", Stop{Kind: "pig", Pig: "t1"}, Stop{Kind: "review"})
	r.e.GoneAfter = time.Hour
	id := r.addCrate()
	tm := r.hc.terms["t1"]
	r.must(map[string]any{"op": "crate.run", "id": id, "line": "l1"})
	for _, c := range []struct{ agent, status string }{
		{"pig", ""},        // an agent that reports nothing
		{"zsh", ""},        // a plain shell, e.g. after the agent exited
		{"zsh", "idle"},    // even a status does not make a shell an agent
		{"pig", "working"}, // busy
		{"pig", "blocked"}, // waiting on a dialog
	} {
		tm.Agent, tm.Status = c.agent, c.status
		r.reconcile()
		if len(r.hc.sent) != 0 || r.crate(id).Sent {
			t.Fatalf("typed a brief into %+v", c)
		}
		if r.crate(id).State == StFailed {
			t.Fatalf("failed at once for %+v", c)
		}
	}
	for _, status := range []string{"done", "idle"} {
		r.hc.sent, r.crate(id).Sent = nil, false
		tm.Agent, tm.Status = "pig", status
		r.reconcile()
		if len(r.hc.sent) != 1 {
			t.Fatalf("%s agent did not get the brief", status)
		}
	}
}

func TestFreshLookBeforeSend(t *testing.T) {
	r := newRig(t, "true", Stop{Kind: "pig", Pig: "t1"}, Stop{Kind: "review"})
	g := &gateHC{fakeHC: r.hc, fresh: map[string]string{"t1": "working"}}
	r.e.HC = g
	id := r.addCrate()
	r.must(map[string]any{"op": "crate.run", "id": id, "line": "l1"})
	r.reconcile() // the list says idle, hc get says working
	if len(r.hc.sent) != 0 || r.crate(id).Sent {
		t.Fatal("sent although a fresh look shows the pig working")
	}
	g.mu.Lock()
	g.fresh["t1"] = "idle"
	g.mu.Unlock()
	r.reconcile()
	if len(r.hc.sent) != 1 || !r.crate(id).Sent || g.lastIf != "idle" {
		t.Fatalf("sent %d, if=%q", len(r.hc.sent), g.lastIf)
	}
	g.mu.Lock()
	g.fresh = map[string]string{}
	g.mu.Unlock()
	// a done pig has no --if (hc only knows idle/working); the fresh look still guards it
	r.pigWorks("t1", "ok", 1)
	r.hc.sent = nil
	id2 := r.addCrate()
	r.must(map[string]any{"op": "crate.run", "id": id2, "line": "l1"})
	r.reconcile()
	if len(r.hc.sent) != 1 || g.lastIf != "" {
		t.Fatalf("done pig: sent %d, if=%q", len(r.hc.sent), g.lastIf)
	}
}

func TestPigThatStopsBeingAnAgentFailsItsCrate(t *testing.T) {
	r := newRig(t, "true", Stop{Kind: "pig", Pig: "t1"}, Stop{Kind: "review"})
	r.e.GoneAfter = time.Hour
	id := r.addCrate()
	r.must(map[string]any{"op": "crate.run", "id": id, "line": "l1"})
	tm := r.hc.terms["t1"]
	tm.Agent, tm.Status = "bash", "" // the agent exited, a shell is left
	r.reconcile()
	r.reconcile()
	if r.crate(id).State == StFailed {
		t.Fatal("failed before the grace period passed")
	}
	r.e.GoneAfter = 0
	r.reconcile()
	c := r.crate(id)
	if c.State != StFailed || !strings.Contains(c.History[len(c.History)-1].Detail, "no longer an agent") {
		t.Fatalf("%+v", c)
	}
	if len(r.hc.sent) != 0 {
		t.Fatal("typed into a shell")
	}
}

func TestUnsentCrateFailsWhenItsPigIsGone(t *testing.T) {
	r := newRig(t, "true", Stop{Kind: "pig", Pig: "t1"}, Stop{Kind: "review"})
	tm := r.hc.terms["t1"]
	tm.Status = "working" // busy: the brief stays queued
	id := r.addCrate()
	r.must(map[string]any{"op": "crate.run", "id": id, "line": "l1"})
	r.reconcile()
	delete(r.hc.terms, "t1")
	r.reconcile()
	if r.crate(id).State == StFailed {
		t.Fatal("failed after one missed listing")
	}
	r.reconcile()
	if c := r.crate(id); c.State != StFailed || c.Sent {
		t.Fatalf("%+v", c)
	}
}

func TestSendDoesNotStallTheLoop(t *testing.T) {
	r := newRig(t, "true", Stop{Kind: "pig", Pig: "t1"}, Stop{Kind: "review"})
	g := &gateHC{fakeHC: r.hc, gate: make(chan struct{})}
	r.e.HC = g
	id := r.addCrate()
	r.e.HandleCommand("x", []byte(fmt.Sprintf(`{"op":"crate.run","id":%q,"line":"l1"}`, id)))
	done := make(chan struct{})
	go func() { r.e.Refresh(); r.e.Flush(true); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the loop waited for hc send")
	}
	if r.crate(id).Sent {
		t.Fatal("marked sent before hc send returned")
	}
	// a second reconcile while the send is out must not start another send
	r.e.Refresh()
	close(g.gate)
	r.e.WaitJobs()
	if len(r.hc.sent) != 1 || !r.crate(id).Sent {
		t.Fatalf("sent %d", len(r.hc.sent))
	}
}

func TestBarnSingleFlight(t *testing.T) {
	r := newRig(t, "", Stop{Kind: "pig", Pig: "t1"}, Stop{Kind: "barn"})
	release := make(chan struct{})
	var mu sync.Mutex
	calls := 0
	r.e.ShipFunc = func(dir, title, id string) (ShipResult, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		<-release
		return ShipResult{Commit: "abc1234", Branch: "pig/" + id}, nil
	}
	id := r.addCrate()
	r.must(map[string]any{"op": "crate.run", "id": id, "line": "l1"})
	r.reconcile()
	r.pigWorks("t1", "done", 1)
	if r.crate(id).State != StBarn {
		t.Fatalf("%+v", r.crate(id))
	}
	r.e.HandleCommand("s1", []byte(fmt.Sprintf(`{"op":"barn.ship","id":%q}`, id)))
	ack := r.e.HandleCommand("s2", []byte(fmt.Sprintf(`{"op":"barn.ship","id":%q}`, id)))
	if ack.OK || !strings.Contains(ack.Error, "already shipping") {
		t.Fatalf("second ship accepted: %+v", ack)
	}
	close(release)
	r.e.WaitJobs()
	if c := r.crate(id); c.State != StShipped || c.Commit != "abc1234" {
		t.Fatalf("%+v", c)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("the Barn ran %d times", calls)
	}
}

func TestBarnResultIgnoredWhenCrateLeftTheBarn(t *testing.T) {
	r := newRig(t, "", Stop{Kind: "pig", Pig: "t1"}, Stop{Kind: "barn"})
	release := make(chan struct{})
	r.e.ShipFunc = func(dir, title, id string) (ShipResult, error) { <-release; return ShipResult{Commit: "abc"}, nil }
	id := r.addCrate()
	r.must(map[string]any{"op": "crate.run", "id": id, "line": "l1"})
	r.reconcile()
	r.pigWorks("t1", "done", 1)
	r.e.HandleCommand("s1", []byte(fmt.Sprintf(`{"op":"barn.ship","id":%q}`, id)))
	r.crate(id).State = StFailed // the person moved it meanwhile
	close(release)
	r.e.WaitJobs()
	if c := r.crate(id); c.State != StFailed || c.Commit != "" {
		t.Fatalf("a late Barn result changed the crate: %+v", c)
	}
	if r.e.shipping[id] {
		t.Fatal("shipping flag leaked")
	}
}

func TestOvenWithoutTestCommandHolds(t *testing.T) {
	r := newRig(t, "", Stop{Kind: "pig", Pig: "t1"}, Stop{Kind: "oven"}, Stop{Kind: "review"})
	id := r.addCrate()
	r.must(map[string]any{"op": "crate.run", "id": id, "line": "l1"})
	r.reconcile()
	r.pigWorks("t1", "done", 1)
	c := r.crate(id)
	if c.State != StBurnt || c.At.Kind != "oven" || c.Log != NoTestCommand {
		t.Fatalf("%+v", c)
	}
	if contains(r.events(), "oven-green") {
		t.Fatalf("green without a test: %v", r.events())
	}
	r.e.SetFarm(Farm{Pens: map[string]Pen{"p1": {Test: "true"}}, Lines: r.e.farm.Lines})
	r.must(map[string]any{"op": "oven.run", "id": id})
	if c = r.crate(id); c.State != StHolding || c.At.Kind != "review" {
		t.Fatalf("after setting a command: %+v", c)
	}
}

func TestStateStaysSmall(t *testing.T) {
	r := newRig(t, "true", Stop{Kind: "pig", Pig: "t1"})
	big := strings.Repeat("x", 9000)
	for i := 0; i < 80; i++ {
		c := &Crate{ID: fmt.Sprintf("c%d", 100+i), Title: "t", DoneWhen: "d", State: StShipped, Report: big, Log: big, Note: big, Updated: int64(1000 + i), History: []Hist{}}
		for j := 0; j < 150; j++ {
			c.History = append(c.History, Hist{T: int64(j), What: "x"})
		}
		r.e.st.Crates = append(r.e.st.Crates, c)
	}
	live := r.addCrate()
	r.e.Flush(true)
	b, _ := os.ReadFile(filepath.Join(r.e.Dir, "state", "state.json"))
	if len(b) > 1<<20 {
		t.Fatalf("state.json is %d bytes", len(b))
	}
	if strings.Contains(string(b), "\n") {
		t.Fatal("state.json is indented")
	}
	if strings.Contains(string(b), `"handoff"`) {
		t.Fatal("hand-off stored twice")
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatal(err)
	}
	finished := 0
	for _, c := range st.Crates {
		if c.State == StShipped {
			finished++
			if n := len([]rune(c.Report)); n > 4000 || len([]rune(c.Log)) > 4000 || len([]rune(c.Note)) > 4000 || len(c.History) > 100 {
				t.Fatalf("crate %s not capped: %d", c.ID, n)
			}
		}
	}
	if finished != 60 {
		t.Fatalf("kept %d finished crates", finished)
	}
	// the most recent ones stay: c179 is newest, c120 the oldest kept
	var ids = map[string]bool{}
	for _, c := range st.Crates {
		ids[c.ID] = true
	}
	if !ids["c179"] || !ids["c120"] || ids["c119"] || !ids[live] {
		t.Fatalf("wrong crates kept: %v", ids)
	}
}

func TestStateNeverPassesTheModsReadLimit(t *testing.T) {
	r := newRig(t, "true", Stop{Kind: "pig", Pig: "t1"})
	big := strings.Repeat("é", 9000) // 2 bytes a rune: 60 crates of it would pass 1 MiB
	for i := 0; i < 60; i++ {
		r.e.st.Crates = append(r.e.st.Crates, &Crate{ID: fmt.Sprintf("c%d", 100+i), Title: "t", DoneWhen: "d", State: StShipped, Report: big, Log: big, Note: big, Updated: int64(1000 + i), History: []Hist{}})
	}
	live := r.addCrate()
	r.e.Flush(true)
	b, _ := os.ReadFile(filepath.Join(r.e.Dir, "state", "state.json"))
	if len(b) > 1<<20 {
		t.Fatalf("state.json is %d bytes", len(b))
	}
	if r.e.crate(live) == nil || r.e.crate("c159") == nil || r.e.crate("c100") != nil {
		t.Fatal("the oldest finished crates should go first, live ones stay")
	}
}

func TestCrateFolderAndUpdated(t *testing.T) {
	r := newRig(t, "true", Stop{Kind: "pig", Pig: "t1"}, Stop{Kind: "review"})
	clock := time.UnixMilli(5_000_000)
	r.e.Now = func() time.Time { return clock }
	id := r.addCrate()
	r.e.Flush(false)
	c := r.crate(id)
	if c.Updated != 5_000_000 || c.Folder != "" {
		t.Fatalf("new board crate: updated=%d folder=%q", c.Updated, c.Folder)
	}
	clock = clock.Add(time.Minute)
	r.e.Flush(false)
	if r.crate(id).Updated != 5_000_000 {
		t.Fatal("updated moved without a change")
	}
	r.must(map[string]any{"op": "crate.run", "id": id, "line": "l1"})
	r.reconcile()
	c = r.crate(id)
	if c.Folder != r.repo || c.Updated != clock.UnixMilli() {
		t.Fatalf("folder=%q (want %q) updated=%d (want %d)", c.Folder, r.repo, c.Updated, clock.UnixMilli())
	}
	// both land in state.json
	b, _ := os.ReadFile(filepath.Join(r.e.Dir, "state", "state.json"))
	var raw struct {
		Crates []map[string]any `json:"crates"`
	}
	json.Unmarshal(b, &raw)
	if raw.Crates[0]["folder"] != r.repo || raw.Crates[0]["updated"] == nil {
		t.Fatalf("%v", raw.Crates[0])
	}
	// a restart keeps `updated` for crates that did not change
	clock = clock.Add(time.Hour)
	e2 := New(r.e.Home, r.hc)
	e2.Now = func() time.Time { return clock }
	e2.PigSessions = ""
	e2.Load()
	e2.Flush(false)
	if got := e2.crate(id).Updated; got != c.Updated {
		t.Fatalf("restart changed updated: %d vs %d", got, c.Updated)
	}
}

func TestLockIgnoresStaleLockFileAndEngineStarts(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "pigtorio")
	os.MkdirAll(filepath.Join(dir, "state"), 0o755)
	os.WriteFile(filepath.Join(dir, "engine.lock"), []byte("99999999\n"), 0o644) // a dead engine's lock
	os.WriteFile(filepath.Join(dir, "state", "state.json"), []byte(`{"engine":{"pid":99999999,"started":1,"beat":1,"version":"0"},"crates":[],"pigs":{},"acks":[]}`), 0o644)
	e := New(home, newFakeHC())
	e.PigSessions = ""
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- e.Run(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, _ := os.ReadFile(filepath.Join(dir, "state", "state.json"))
		var st State
		if json.Unmarshal(b, &st) == nil && st.Engine.PID == os.Getpid() && st.Engine.Beat > 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("engine did not come up over a stale lock")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
}

func fakeBin(t *testing.T, gh string) {
	t.Helper()
	dir := t.TempDir()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	os.Symlink(gitPath, filepath.Join(dir, "git"))
	if gh != "" {
		os.WriteFile(filepath.Join(dir, "gh"), []byte(gh), 0o755)
	}
	t.Setenv("PATH", dir)
}

func TestShipPushesItsOwnBranchNeverTheDefault(t *testing.T) {
	fakeBin(t, "#!/bin/sh\necho https://example.com/pr/1\n")
	remote := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", "--bare", "-b", "main", remote).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	repo := gitRepo(t)
	git(repo, "remote", "add", "origin", remote)
	if out, err := git(repo, "push", "-q", "-u", "origin", "main"); err != nil {
		t.Fatal(out)
	}
	mainBefore, _ := git(remote, "rev-parse", "main")
	os.WriteFile(filepath.Join(repo, "x.txt"), []byte("x"), 0o644)
	res, err := Ship(repo, "feature x", "c3")
	if err != nil || res.Branch != "pig/c3" || res.PR != "https://example.com/pr/1" || res.Commit == "" {
		t.Fatalf("%+v %v", res, err)
	}
	if got, _ := git(remote, "rev-parse", "main"); got != mainBefore {
		t.Fatal("the default branch was pushed")
	}
	if _, err := git(remote, "rev-parse", "--verify", "pig/c3"); err != nil {
		t.Fatal("pig/c3 was not pushed")
	}
	// nothing new: no push, no PR, a note
	git(repo, "switch", "main")
	res, err = Ship(repo, "again", "c4")
	if err != nil || res.Commit != "" || res.PR != "" || res.Branch != "main" || len(res.Notes) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := git(remote, "rev-parse", "--verify", "pig/c4"); err == nil {
		t.Fatal("pushed with nothing to commit")
	}
	// origin/HEAD names the default branch, whatever it is called; a feature branch is left alone
	git(repo, "switch", "-c", "develop")
	git(repo, "push", "-q", "-u", "origin", "develop")
	git(repo, "remote", "set-head", "origin", "develop")
	for branch, want := range map[string]bool{"develop": true, "main": true, "master": true, "": true, "feature/y": false} {
		if got := protectedBranch(repo, branch); got != want {
			t.Errorf("protectedBranch(%q) = %v", branch, got)
		}
	}
	git(repo, "switch", "-c", "feature/y")
	os.WriteFile(filepath.Join(repo, "y.txt"), []byte("y"), 0o644)
	res, err = Ship(repo, "y", "c5")
	if err != nil || res.Branch != "feature/y" {
		t.Fatalf("%+v %v", res, err)
	}
	// detached HEAD gets its own branch
	git(repo, "checkout", "-q", "--detach")
	os.WriteFile(filepath.Join(repo, "z.txt"), []byte("z"), 0o644)
	res, err = Ship(repo, "z", "c6")
	if err != nil || res.Branch != "pig/c6" {
		t.Fatalf("%+v %v", res, err)
	}
}
