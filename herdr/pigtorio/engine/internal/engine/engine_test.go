package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pigtorio/internal/hc"
)

type sentMsg struct{ addr, text string }

type fakeHC struct {
	terms  map[string]*hc.Term
	sent   []sentMsg
	screen map[string]string
}

func newFakeHC() *fakeHC {
	return &fakeHC{terms: map[string]*hc.Term{}, screen: map[string]string{}}
}

func (f *fakeHC) add(addr, cwd string) *hc.Term {
	t := &hc.Term{Address: addr, Name: addr, Cwd: cwd, Agent: "pig", State: "live", Status: "idle"}
	f.terms[addr] = t
	return t
}

func (f *fakeHC) List() ([]hc.Term, error) {
	var out []hc.Term
	for _, t := range f.terms {
		out = append(out, *t)
	}
	return out, nil
}
func (f *fakeHC) Get(addr string) (hc.Term, error) {
	if t, ok := f.terms[addr]; ok {
		return *t, nil
	}
	return hc.Term{}, fmt.Errorf("no %s", addr)
}
func (f *fakeHC) Send(addr, text, ifStatus string) error {
	t := f.terms[addr]
	if t != nil && t.Status == "blocked" {
		return hc.ErrBlocked
	}
	if ifStatus != "" && (t == nil || t.Status != ifStatus) {
		return hc.ErrNotReady
	}
	f.sent = append(f.sent, sentMsg{addr, text})
	return nil
}
func (f *fakeHC) Read(addr string, lines int) (string, error) { return f.screen[addr], nil }
func (f *fakeHC) Follow(ctx context.Context, wake func()) error {
	<-ctx.Done()
	return nil
}

type rig struct {
	t    *testing.T
	e    *Engine
	hc   *fakeHC
	repo string
	n    int
}

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"config", "user.email", "t@example.com"}, {"config", "user.name", "Test"}, {"config", "commit.gpgsign", "false"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	os.WriteFile(filepath.Join(dir, "README"), []byte("hi\n"), 0o644)
	exec.Command("git", "-C", dir, "add", "-A").Run()
	if out, err := exec.Command("git", "-C", dir, "commit", "-q", "-m", "init").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v %s", err, out)
	}
	return dir
}

func newRig(t *testing.T, test string, stops ...Stop) *rig {
	t.Helper()
	f := newFakeHC()
	repo := gitRepo(t)
	e := New(t.TempDir(), f)
	e.PigSessions = ""
	e.GoneAfter = 0
	e.Load()
	for _, s := range stops {
		if s.Kind == "pig" {
			f.add(s.Pig, repo)
		}
	}
	e.SetFarm(Farm{
		Pens:  map[string]Pen{"p1": {Test: test}},
		Pigs:  map[string]FarmPig{"t1": {Name: "Sandy"}, "t2": {Name: "Slate"}},
		Lines: []Line{{ID: "l1", Pen: "p1", Stops: stops}},
	})
	return &rig{t: t, e: e, hc: f, repo: repo}
}

func (r *rig) cmd(v map[string]any) Ack {
	r.t.Helper()
	r.n++
	b, _ := json.Marshal(v)
	ack := r.e.HandleCommand(fmt.Sprintf("cmd%d", r.n), b)
	r.e.WaitJobs() // briefs and ovens started by the command settle before the next step
	return ack
}

func (r *rig) must(v map[string]any) {
	r.t.Helper()
	if a := r.cmd(v); !a.OK {
		r.t.Fatalf("%v refused: %s", v["op"], a.Error)
	}
}

func (r *rig) addCrate() string {
	r.t.Helper()
	r.must(map[string]any{"op": "crate.add", "crate": map[string]any{"title": "Make it", "text": "do it", "doneWhen": "file exists"}})
	cs := r.e.State().Crates
	return cs[len(cs)-1].ID
}

func (r *rig) reconcile() { r.e.Refresh(); r.e.WaitJobs() }

// pigWorks makes pig addr finish a turn and leave a session file with the given reply.
func (r *rig) pigWorks(addr, reply string, tokens int) {
	r.t.Helper()
	dir := filepath.Join(r.e.Dir, "sessions", addr)
	os.MkdirAll(dir, 0o755)
	r.n++
	line := fmt.Sprintf(`{"type":"message","timestamp":"2026-01-01T00:00:00Z","message":{"role":"assistant","content":[{"type":"text","text":%q},{"type":"toolCall","name":"bash"}],"usage":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":%d,"cost":{"total":0.5}},"timestamp":%d}}`+"\n",
		reply, tokens, time.Now().UnixMilli()+1000)
	os.WriteFile(filepath.Join(dir, fmt.Sprintf("s%d.jsonl", r.n)), []byte(line), 0o644)
	tm := r.hc.terms[addr]
	tm.Status = "done"
	tm.Turn++
	r.reconcile()
}

func (r *rig) crate(id string) *Crate {
	r.t.Helper()
	c := r.e.crate(id)
	if c == nil {
		r.t.Fatalf("no crate %s", id)
	}
	return c
}

func (r *rig) events() []string {
	r.t.Helper()
	b, _ := os.ReadFile(filepath.Join(r.e.Dir, "crates.jsonl"))
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var ev Event
		if json.Unmarshal([]byte(l), &ev) == nil {
			out = append(out, ev.Event)
		}
	}
	return out
}

func wantEvents(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("events\n got %v\nwant %v", got, want)
	}
}

func TestRefusesCrateWithoutDoneWhen(t *testing.T) {
	r := newRig(t, "true", Stop{Kind: "pig", Pig: "t1"})
	for _, done := range []string{"", "   "} {
		a := r.cmd(map[string]any{"op": "crate.add", "crate": map[string]any{"title": "x", "doneWhen": done}})
		if a.OK || !strings.Contains(a.Error, "done-when") {
			t.Fatalf("ack = %+v", a)
		}
	}
	if len(r.e.State().Crates) != 0 {
		t.Fatal("a refused crate was added")
	}
	// a crate that lost its done-when by update is refused too
	id := r.addCrate()
	if a := r.cmd(map[string]any{"op": "crate.update", "id": id, "patch": map[string]any{"doneWhen": ""}}); a.OK {
		t.Fatal("update to empty doneWhen accepted")
	}
	if a := r.cmd(map[string]any{"op": "crate.update", "id": id, "patch": map[string]any{"title": "New"}}); !a.OK || r.crate(id).Title != "New" {
		t.Fatalf("update: %+v", a)
	}
	if a := r.cmd(map[string]any{"op": "crate.remove", "id": id}); !a.OK || len(r.e.State().Crates) != 0 {
		t.Fatalf("remove: %+v", a)
	}
}

func TestLineEndToEnd(t *testing.T) {
	r := newRig(t, "test -f made.txt", Stop{Kind: "pig", Pig: "t1"}, Stop{Kind: "pig", Pig: "t2"}, Stop{Kind: "oven"}, Stop{Kind: "review"}, Stop{Kind: "barn"})
	id := r.addCrate()
	r.must(map[string]any{"op": "crate.run", "id": id, "line": "l1"})
	r.reconcile()
	c := r.crate(id)
	if c.State != StWorking || c.At.Pig != "t1" || !c.Sent {
		t.Fatalf("after run: %+v", c)
	}
	if len(r.hc.sent) != 1 || r.hc.sent[0].addr != "t1" {
		t.Fatalf("sent %v", r.hc.sent)
	}
	for _, want := range []string{"Crate " + id, "Asked by: you", "Done when: file exists", "do it", "Goal:"} {
		if !strings.Contains(r.hc.sent[0].text, want) {
			t.Errorf("brief lacks %q:\n%s", want, r.hc.sent[0].text)
		}
	}
	// the pig is still working: nothing moves
	r.hc.terms["t1"].Status = "working"
	r.reconcile()
	if r.crate(id).At.Pig != "t1" {
		t.Fatal("moved while the pig works")
	}
	// first pig finishes -> crate goes to the second pig with the hand-off
	r.pigWorks("t1", "Goal: a\nDone: planned", 100)
	c = r.crate(id)
	if c.At.Pig != "t2" || c.Report != "Goal: a\nDone: planned" || c.Tokens != 100 || c.Cost != 0.5 {
		t.Fatalf("after t1: %+v", c)
	}
	if len(r.hc.sent) != 2 || !strings.Contains(r.hc.sent[1].text, "Previous hand-off (from Sandy)") || !strings.Contains(r.hc.sent[1].text, "Done: planned") {
		t.Fatalf("second brief: %v", r.hc.sent)
	}
	os.WriteFile(filepath.Join(r.repo, "made.txt"), []byte("x"), 0o644)
	r.pigWorks("t2", "Goal: b\nDone: built", 50)
	c = r.crate(id)
	if c.State != StHolding || c.At.Kind != "review" || c.Tokens != 150 {
		t.Fatalf("expected review: %+v", c)
	}
	if !strings.Contains(c.Log, "") || c.Burns != 0 {
		t.Fatal("oven")
	}
	r.must(map[string]any{"op": "review.approve", "id": id})
	r.e.WaitJobs()
	c = r.crate(id)
	if c.State != StShipped || c.Commit == "" {
		t.Fatalf("expected shipped with commit: %+v", c)
	}
	out, _ := exec.Command("git", "-C", r.repo, "log", "-1", "--format=%s").Output()
	if strings.TrimSpace(string(out)) != "Make it" {
		t.Fatalf("commit subject %q", out)
	}
	wantEvents(t, r.events(), "added", "moved", "done", "moved", "done", "moved", "oven-green", "moved", "holding", "approved", "moved", "shipped")
	// state.json
	var st State
	b, err := os.ReadFile(filepath.Join(r.e.Dir, "state", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatal(err)
	}
	if st.Stats.CratesIn24h != 1 || st.Stats.TrufflesOut24h != 1 || st.Stats.Tokens24h != 150 || st.Engine.PID == 0 {
		t.Fatalf("stats %+v engine %+v", st.Stats, st.Engine)
	}
	if len(st.Acks) < 3 || st.Acks[0].ID != "cmd1" || !st.Acks[0].OK {
		t.Fatalf("acks %+v", st.Acks)
	}
}

func TestOvenBurntReturnsThenHolds(t *testing.T) {
	r := newRig(t, "echo oops-from-oven; exit 3", Stop{Kind: "pig", Pig: "t1"}, Stop{Kind: "pig", Pig: "t2"}, Stop{Kind: "oven"}, Stop{Kind: "review"})
	id := r.addCrate()
	r.must(map[string]any{"op": "crate.run", "id": id, "line": "l1"})
	r.reconcile()
	r.pigWorks("t1", "one", 1)
	r.pigWorks("t2", "two", 1)
	// burn 1: back to the LAST pig stop (t2) with the log
	c := r.crate(id)
	if c.Burns != 1 || c.State != StWorking || c.At.Pig != "t2" || c.Stop != 1 {
		t.Fatalf("after burn 1: %+v", c)
	}
	last := r.hc.sent[len(r.hc.sent)-1]
	if last.addr != "t2" || !strings.Contains(last.text, "oops-from-oven") || !strings.Contains(last.text, "burnt") {
		t.Fatalf("return brief: %+v", last)
	}
	if !strings.Contains(c.Log, "oops-from-oven") {
		t.Fatalf("log %q", c.Log)
	}
	// burn 2: held for the person
	r.pigWorks("t2", "two again", 1)
	c = r.crate(id)
	if c.Burns != 2 || c.State != StBurnt || c.At.Kind != "oven" {
		t.Fatalf("after burn 2: %+v", c)
	}
	n := len(r.hc.sent)
	r.reconcile()
	if len(r.hc.sent) != n {
		t.Fatal("a held crate was sent again")
	}
	wantEvents(t, r.events(), "added", "moved", "done", "moved", "done", "moved", "oven-burnt", "returned", "moved", "done", "moved", "oven-burnt")
	// the person fixes the test and re-runs the Oven: green, on to Review
	r.e.SetFarm(Farm{Pens: map[string]Pen{"p1": {Test: "true"}}, Lines: r.e.farm.Lines})
	r.must(map[string]any{"op": "oven.run", "id": id})
	r.e.WaitJobs()
	if c = r.crate(id); c.State != StHolding || c.At.Kind != "review" {
		t.Fatalf("after oven.run: %+v", c)
	}
}

func TestReviewSendBack(t *testing.T) {
	r := newRig(t, "true", Stop{Kind: "pig", Pig: "t1"}, Stop{Kind: "oven"}, Stop{Kind: "review"}, Stop{Kind: "barn"})
	id := r.addCrate()
	r.must(map[string]any{"op": "crate.run", "id": id, "line": "l1"})
	r.reconcile()
	r.pigWorks("t1", "first try", 1)
	if c := r.crate(id); c.State != StHolding {
		t.Fatalf("want review: %+v", c)
	}
	if a := r.cmd(map[string]any{"op": "barn.ship", "id": id}); a.OK {
		t.Fatal("barn.ship accepted for a crate at Review")
	}
	r.must(map[string]any{"op": "review.sendback", "id": id, "note": "needs a test"})
	c := r.crate(id)
	if c.State != StWorking || c.At.Pig != "t1" || c.Stop != 0 || c.Burns != 0 {
		t.Fatalf("after sendback: %+v", c)
	}
	last := r.hc.sent[len(r.hc.sent)-1]
	if !strings.Contains(last.text, "needs a test") || !strings.Contains(last.text, "Previous hand-off (from Sandy)") {
		t.Fatalf("brief: %s", last.text)
	}
	r.pigWorks("t1", "second try", 1)
	if c = r.crate(id); c.State != StHolding || c.Report != "second try" {
		t.Fatalf("back at review: %+v", c)
	}
	r.must(map[string]any{"op": "review.approve", "id": id})
	r.e.WaitJobs()
	if c = r.crate(id); c.State != StShipped {
		t.Fatalf("shipped: %+v", c)
	}
	wantEvents(t, r.events(), "added", "moved", "done", "moved", "oven-green", "moved", "holding", "sent-back", "returned", "moved", "done", "moved", "oven-green", "moved", "holding", "approved", "moved", "shipped")
}

func TestBarnWaitsForShipWithoutReview(t *testing.T) {
	r := newRig(t, "", Stop{Kind: "pig", Pig: "t1"}, Stop{Kind: "barn"})
	id := r.addCrate()
	r.must(map[string]any{"op": "crate.run", "id": id, "line": "l1"})
	r.reconcile()
	os.WriteFile(filepath.Join(r.repo, "new.txt"), []byte("n"), 0o644)
	r.pigWorks("t1", "done", 1)
	c := r.crate(id)
	if c.State != StBarn || c.At.Kind != "barn" {
		t.Fatalf("should wait at the barn: %+v", c)
	}
	r.must(map[string]any{"op": "barn.ship", "id": id})
	r.e.WaitJobs()
	if c = r.crate(id); c.State != StShipped || c.Commit == "" {
		t.Fatalf("shipped: %+v", c)
	}
	if out, _ := exec.Command("git", "-C", r.repo, "status", "--porcelain").Output(); len(out) != 0 {
		t.Fatalf("dirty tree after ship: %s", out)
	}
}

func TestShipCommitsOnlyWithoutRemote(t *testing.T) {
	repo := gitRepo(t)
	before, _ := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	// nothing changed: no commit, no branch, a note
	res, err := Ship(repo, "empty", "c1")
	if err != nil || res.Commit != "" || res.PR != "" || len(res.Notes) != 1 || res.Notes[0] != "nothing to commit" {
		t.Fatalf("res %+v err %v", res, err)
	}
	after, _ := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if string(before) != string(after) {
		t.Fatal("committed with nothing to commit")
	}
	if b, _ := git(repo, "branch", "--show-current"); b != "main" {
		t.Fatalf("switched to %q with nothing to commit", b)
	}
	os.WriteFile(filepath.Join(repo, "f.txt"), []byte("f"), 0o644)
	res, err = Ship(repo, "add f", "c7")
	if err != nil || res.Commit == "" || res.PR != "" || len(res.Notes) != 0 || res.Branch != "pig/c7" {
		t.Fatalf("res %+v err %v", res, err)
	}
	// the default branch stays where it was; the commit is on pig/c7
	if out, _ := git(repo, "rev-parse", "main"); out != strings.TrimSpace(string(before)) {
		t.Fatalf("main moved: %s", out)
	}
	if b, _ := git(repo, "branch", "--show-current"); b != "pig/c7" {
		t.Fatalf("on %q", b)
	}
	out, _ := exec.Command("git", "-C", repo, "log", "-1", "--format=%s").Output()
	if strings.TrimSpace(string(out)) != "add f" {
		t.Fatalf("subject %q", out)
	}
	if _, err := Ship(t.TempDir(), "x", "c1"); err == nil {
		t.Fatal("shipping a non-git folder should fail")
	}
}

func TestHandDirectHolds(t *testing.T) {
	r := newRig(t, "true", Stop{Kind: "pig", Pig: "t1"})
	id := r.addCrate()
	r.must(map[string]any{"op": "crate.hand", "id": id, "to": "t1"})
	r.reconcile()
	c := r.crate(id)
	if c.State != StWorking || c.At.Pig != "t1" || c.Line != "" || !c.Sent {
		t.Fatalf("handed: %+v", c)
	}
	r.pigWorks("t1", "Goal: x\nDone: y", 7)
	c = r.crate(id)
	if c.State != StHolding || c.Report != "Goal: x\nDone: y" || c.Tokens != 7 {
		t.Fatalf("holding: %+v", c)
	}
	wantEvents(t, r.events(), "added", "handed", "done", "holding")
	r.must(map[string]any{"op": "review.sendback", "id": id, "note": "again"})
	r.reconcile()
	if last := r.hc.sent[len(r.hc.sent)-1]; !strings.Contains(last.text, "again") {
		t.Fatalf("resent: %s", last.text)
	}
}

func TestBlockedPigBlocksCrate(t *testing.T) {
	r := newRig(t, "true", Stop{Kind: "pig", Pig: "t1"}, Stop{Kind: "review"})
	id := r.addCrate()
	r.must(map[string]any{"op": "crate.run", "id": id, "line": "l1"})
	r.reconcile()
	// working -> blocked bumps the turn counter, which must not count as finishing
	tm := r.hc.terms["t1"]
	tm.Status, tm.Turn = "blocked", 1
	r.reconcile()
	if c := r.crate(id); c.State != StBlocked {
		t.Fatalf("want blocked: %+v", c)
	}
	tm.Status = "working"
	r.reconcile()
	if c := r.crate(id); c.State != StWorking {
		t.Fatalf("want working again: %+v", c)
	}
	r.pigWorks("t1", "answered", 1)
	if c := r.crate(id); c.State != StHolding || c.Report != "answered" {
		t.Fatalf("%+v", c)
	}
	if ev := r.events(); !contains(ev, "blocked") {
		t.Fatalf("events %v", ev)
	}
}

func contains(s []string, x string) bool {
	for _, v := range s {
		if v == x {
			return true
		}
	}
	return false
}

func TestBusyPigQueuesCrates(t *testing.T) {
	r := newRig(t, "true", Stop{Kind: "pig", Pig: "t1"}, Stop{Kind: "review"})
	a, b := r.addCrate(), r.addCrate()
	r.must(map[string]any{"op": "crate.run", "id": a, "line": "l1"})
	r.must(map[string]any{"op": "crate.run", "id": b, "line": "l1"})
	r.reconcile()
	if len(r.hc.sent) != 1 || !r.crate(a).Sent || r.crate(b).Sent {
		t.Fatalf("sent %v", r.hc.sent)
	}
	r.pigWorks("t1", "a done", 1)
	if len(r.hc.sent) != 2 || !r.crate(b).Sent || r.crate(a).State != StHolding {
		t.Fatalf("sent %v a=%+v b=%+v", r.hc.sent, r.crate(a), r.crate(b))
	}
}

func TestBlockedByCrateStaysOnBoard(t *testing.T) {
	r := newRig(t, "true", Stop{Kind: "pig", Pig: "t1"})
	a := r.addCrate()
	r.must(map[string]any{"op": "crate.add", "crate": map[string]any{"title": "B", "doneWhen": "x", "blockedBy": []string{a}}})
	b := r.e.State().Crates[1].ID
	if ack := r.cmd(map[string]any{"op": "crate.run", "id": b, "line": "l1"}); ack.OK || !strings.Contains(ack.Error, "blocked by") {
		t.Fatalf("%+v", ack)
	}
}

// The mod names a crate and puts it on a belt in two back-to-back commands.
func TestCallerNamedCrate(t *testing.T) {
	r := newRig(t, "true", Stop{Kind: "pig", Pig: "t1"})
	r.must(map[string]any{"op": "crate.add", "crate": map[string]any{"id": "cmine", "title": "T", "doneWhen": "D"}})
	r.must(map[string]any{"op": "crate.run", "id": "cmine", "line": "l1"})
	if ack := r.cmd(map[string]any{"op": "crate.add", "crate": map[string]any{"id": "cmine", "title": "T", "doneWhen": "D"}}); ack.OK || !strings.Contains(ack.Error, "already exists") {
		t.Fatalf("duplicate id: %+v", ack)
	}
	r.must(map[string]any{"op": "crate.add", "crate": map[string]any{"title": "U", "doneWhen": "D"}})
	if cs := r.e.State().Crates; cs[0].ID != "cmine" || cs[1].ID == "cmine" {
		t.Fatalf("ids: %s %s", cs[0].ID, cs[1].ID)
	}
}

func TestPigGoneFailsCrate(t *testing.T) {
	r := newRig(t, "true", Stop{Kind: "pig", Pig: "t1"})
	id := r.addCrate()
	r.must(map[string]any{"op": "crate.run", "id": id, "line": "l1"})
	r.reconcile()
	delete(r.hc.terms, "t1")
	r.reconcile()
	if r.crate(id).State == StFailed {
		t.Fatal("failed after one missed listing")
	}
	r.reconcile()
	if r.crate(id).State != StFailed {
		t.Fatalf("%+v", r.crate(id))
	}
}

func TestReportFallsBackToScreen(t *testing.T) {
	r := newRig(t, "true", Stop{Kind: "pig", Pig: "t1"}, Stop{Kind: "review"})
	id := r.addCrate()
	r.must(map[string]any{"op": "crate.run", "id": id, "line": "l1"})
	r.reconcile()
	r.hc.screen["t1"] = "$ work\nall done here\n"
	tm := r.hc.terms["t1"]
	tm.Status, tm.Turn = "idle", 1
	r.reconcile()
	if c := r.crate(id); c.State != StHolding || c.Report != "$ work\nall done here" {
		t.Fatalf("%+v", c)
	}
}

func TestReportPrefersPigSessionDirFromArgvThenPigSessions(t *testing.T) {
	// (2): newest PiG session for the cwd, only if written after the crate arrived
	root := t.TempDir()
	cwd := "/work/repo"
	dir := PigSessionDir(root, cwd)
	if filepath.Base(dir) != "--work-repo--" {
		t.Fatalf("slug %s", dir)
	}
	os.MkdirAll(dir, 0o755)
	old := filepath.Join(dir, "old.jsonl")
	os.WriteFile(old, []byte(`{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":"stale"}]}}`+"\n"), 0o644)
	os.Chtimes(old, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour))

	f := newFakeHC()
	e := New(t.TempDir(), f)
	e.PigSessions = root
	c := &Crate{ID: "c1", SentAt: time.Now().Add(-time.Minute).UnixMilli()}
	rep := e.readReport(c, "t9", pigTerm{cwd: cwd})
	if rep.Text == "stale" {
		t.Fatal("used a session older than the crate")
	}
	fresh := filepath.Join(dir, "fresh.jsonl")
	os.WriteFile(fresh, []byte(`{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":"fresh"}]}}`+"\n"), 0o644)
	if rep := e.readReport(c, "t9", pigTerm{cwd: cwd}); rep.Text != "fresh" {
		t.Fatalf("got %q", rep.Text)
	}
	// (1): --session-dir from argv wins
	own := t.TempDir()
	os.WriteFile(filepath.Join(own, "a.jsonl"), []byte(`{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":"mine"}]}}`+"\n"), 0o644)
	if rep := e.readReport(c, "t9", pigTerm{cwd: cwd, argv: []string{"pig", "--session-dir", own}}); rep.Text != "mine" {
		t.Fatalf("got %q", rep.Text)
	}
	if got := SessionDirFromArgv([]string{"pig", "--session-dir=/x/y"}); got != "/x/y" {
		t.Fatalf("got %q", got)
	}
}

func TestReadSessionExtractsLastAssistantText(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	lines := []string{
		`{"type":"session","version":3,"id":"s","cwd":"/x"}`,
		`{"type":"message","timestamp":"2026-01-01T00:00:00.5Z","message":{"role":"user","content":[{"type":"text","text":"do it"}],"timestamp":1000}}`,
		`{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":"early"},{"type":"toolCall","name":"bash"}],"usage":{"input":1,"output":2,"totalTokens":3,"cost":{"total":0.25}},"timestamp":1000}}`,
		`{"type":"message","message":{"role":"toolResult","content":[{"type":"text","text":"tool output"}]}}`,
		`{"type":"message","message":{"role":"assistant","content":[{"type":"thinking","thinking":"hmm"},{"type":"text","text":"Goal: g"},{"type":"text","text":"Done: d"}],"usage":{"input":10,"output":20,"cacheRead":5,"cacheWrite":5,"cost":{"total":0.5}},"timestamp":5000}}`,
		`not json`,
	}
	os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	rep, err := ReadSession(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Text != "Goal: g\nDone: d" || rep.Tokens != 3+40 || rep.Cost != 0.75 {
		t.Fatalf("%+v", rep)
	}
	rep, _ = ReadSession(p, 2000) // only the later message counts for usage
	if rep.Text != "Goal: g\nDone: d" || rep.Tokens != 40 || rep.Cost != 0.5 {
		t.Fatalf("since: %+v", rep)
	}
}

func TestLockAllowsOneEngine(t *testing.T) {
	home := t.TempDir()
	a := New(home, newFakeHC())
	unlock, err := a.Lock()
	if err != nil {
		t.Fatal(err)
	}
	b := New(home, newFakeHC())
	if _, err := b.Lock(); err != ErrLocked {
		t.Fatalf("second lock: %v", err)
	}
	unlock()
	if u, err := b.Lock(); err != nil {
		t.Fatalf("after release: %v", err)
	} else {
		u()
	}
}

func TestInboxAndFarmReload(t *testing.T) {
	home := t.TempDir()
	f := newFakeHC()
	e := New(home, f)
	e.PigSessions = ""
	e.Load()
	os.WriteFile(filepath.Join(e.Dir, "farm.json"), []byte(`{"pens":{"p":{"test":"true"}},"lines":[{"id":"l9","pen":"p","stops":[{"kind":"review"}]}],"extra":1}`), 0o644)
	e.loadFarm()
	if e.line("l9") == nil {
		t.Fatal("farm.json not read")
	}
	os.WriteFile(filepath.Join(e.Dir, "farm.json"), []byte(`{"lines":[{"id":"l10","stops":[]}]}`), 0o644)
	os.Chtimes(filepath.Join(e.Dir, "farm.json"), time.Now().Add(time.Second), time.Now().Add(time.Second))
	e.loadFarm()
	if e.line("l10") == nil || e.line("l9") != nil {
		t.Fatal("farm.json not re-read")
	}
	inbox := filepath.Join(e.Dir, "inbox")
	os.WriteFile(filepath.Join(inbox, "1-a.json"), []byte(`{"op":"crate.add","crate":{"title":"T","doneWhen":"D"}}`), 0o644)
	os.WriteFile(filepath.Join(inbox, "2-b.json"), []byte(`{"op":"crate.add","crate":{"title":"T"}}`), 0o644)
	os.WriteFile(filepath.Join(inbox, "3-c.json"), []byte(`{"op":"nope"}`), 0o644)
	e.PollInbox()
	if ents, _ := os.ReadDir(inbox); len(ents) != 0 {
		t.Fatalf("inbox not emptied: %v", ents)
	}
	acks := e.State().Acks
	if len(acks) != 3 || !acks[0].OK || acks[1].OK || acks[2].OK || acks[0].ID != "1-a" || acks[2].Op != "nope" {
		t.Fatalf("acks %+v", acks)
	}
	if len(e.State().Crates) != 1 || e.State().Crates[0].State != StBoard {
		t.Fatalf("crates %+v", e.State().Crates)
	}
	// a restart keeps crates and numbering
	e2 := New(home, f)
	e2.Load()
	e2.PollInbox()
	os.WriteFile(filepath.Join(inbox, "4-d.json"), []byte(`{"op":"crate.add","crate":{"title":"U","doneWhen":"D"}}`), 0o644)
	e2.PollInbox()
	cs := e2.State().Crates
	if len(cs) != 2 || cs[1].ID != "c2" {
		t.Fatalf("after restart %+v", cs)
	}
}

func TestOvenHasTimeLimit(t *testing.T) {
	start := time.Now()
	out, code := RunShell(t.TempDir(), "sleep 30", 300*time.Millisecond)
	if code == 0 || time.Since(start) > 10*time.Second || !strings.Contains(out, "time limit") {
		t.Fatalf("code %d out %q after %v", code, out, time.Since(start))
	}
}

func TestPigStatusSince(t *testing.T) {
	r := newRig(t, "true", Stop{Kind: "pig", Pig: "t1"}, Stop{Kind: "review"})
	clock := time.UnixMilli(1_000_000)
	r.e.Now = func() time.Time { return clock }
	r.hc.add("t1", r.repo).StatusMs = 123_000 // from `hc get`
	r.hc.add("t5", r.repo).StatusMs = 456_000
	r.hc.terms["t5"].Status = "blocked"
	r.hc.add("t6", r.repo).Status = "" // reports no status: not listed
	slot := func(addr string) (PigSlot, bool) { s, ok := r.e.State().Pigs[addr]; return s, ok }

	r.reconcile()
	if s, _ := slot("t1"); s.Status != "idle" || s.StatusSince != 123_000 {
		t.Fatalf("t1 first sighting: %+v", s)
	}
	if s, _ := slot("t5"); s.Status != "blocked" || s.StatusSince != 456_000 {
		t.Fatalf("t5 first sighting: %+v", s)
	}
	if _, ok := slot("t6"); ok {
		t.Fatal("a terminal without status is listed")
	}

	// the word did not change: the start stays, however late the reconcile
	clock = clock.Add(time.Minute)
	r.reconcile()
	if s, _ := slot("t1"); s.StatusSince != 123_000 {
		t.Fatalf("since moved without a change: %+v", s)
	}

	// a change is stamped with the time the engine saw it; a crate adds its fields
	id := r.addCrate()
	r.must(map[string]any{"op": "crate.run", "id": id, "line": "l1"})
	r.reconcile() // brief sent
	r.hc.terms["t1"].Status = "working"
	clock = clock.Add(time.Second)
	r.reconcile()
	s, _ := slot("t1")
	if s.Status != "working" || s.StatusSince != clock.UnixMilli() || s.Crate != id || s.Since == 0 {
		t.Fatalf("t1 after change: %+v", s)
	}

	// gone terminals drop out; a restart re-reads status_ms
	delete(r.hc.terms, "t5")
	r.reconcile()
	if _, ok := slot("t5"); ok {
		t.Fatal("gone terminal kept")
	}
	e2 := New(r.e.Home, r.hc)
	e2.PigSessions = ""
	e2.Load()
	e2.Refresh()
	if s := e2.State().Pigs["t1"]; s.Status != "working" || s.StatusSince != 123_000 {
		t.Fatalf("after restart: %+v", s)
	}
}
