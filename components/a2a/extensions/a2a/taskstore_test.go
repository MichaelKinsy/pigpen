package a2aext

// The persistent task store (roadmap gap "persistent task store": tasks survive a restart of the listener,
// SubscribeToTask answers for stored tasks, and the per-principal ownership checks keep holding across a restart).

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv/taskstore"
)

type userKey struct{}

// userAuth names the task owner from the context, so one store can be driven as several principals.
func userAuth(ctx context.Context) (string, error) {
	u, _ := ctx.Value(userKey{}).(string)
	if u == "" {
		return "", a2a.ErrUnauthenticated
	}
	return u, nil
}

func asUser(u string) context.Context { return context.WithValue(context.Background(), userKey{}, u) }

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newClock() *clock { return &clock{t: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)} }

func openStore(t *testing.T, dir string, clk *clock, retention time.Duration) (*fileStore, *[]string) {
	t.Helper()
	var logs []string
	var mu sync.Mutex
	s, err := openTaskStore(storeOptions{Dir: dir, Retention: retention, Now: clk.now, Auth: userAuth,
		Logf: func(f string, a ...any) {
			mu.Lock()
			logs = append(logs, strings.TrimSpace(fmt.Sprintf(f, a...)))
			mu.Unlock()
		}})
	if err != nil {
		t.Fatalf("openTaskStore: %v", err)
	}
	return s, &logs
}

func task(id, ctxID string, state a2a.TaskState) *a2a.Task {
	return &a2a.Task{ID: a2a.TaskID(id), ContextID: ctxID, Status: a2a.TaskStatus{State: state}}
}

func withText(t *a2a.Task, text string) *a2a.Task {
	t.Artifacts = []*a2a.Artifact{{ID: "art", Parts: a2a.ContentParts{a2a.NewTextPart(text)}}}
	return t
}

func mustGet(t *testing.T, s taskstore.Store, user, id string) *a2a.Task {
	t.Helper()
	got, err := s.Get(asUser(user), a2a.TaskID(id))
	if err != nil {
		t.Fatalf("Get(%s as %s): %v", id, user, err)
	}
	return got.Task
}

func TestFileStoreTasksSurviveReopen(t *testing.T) {
	dir := t.TempDir()
	clk := newClock()
	s, _ := openStore(t, dir, clk, 0)
	ver, err := s.Create(asUser("alice"), withText(task("t1", "c1", a2a.TaskStateCompleted), "the answer"))
	if err != nil || ver == taskstore.TaskVersionMissing {
		t.Fatalf("Create: %v %v", ver, err)
	}
	again, _ := openStore(t, dir, clk, 0)
	got := mustGet(t, again, "alice", "t1")
	if got.ContextID != "c1" || got.Status.State != a2a.TaskStateCompleted || len(got.Artifacts) != 1 || got.Artifacts[0].Parts[0].Text() != "the answer" {
		t.Fatalf("the stored task came back changed: %+v", got)
	}
	if again.interrupted() != 0 {
		t.Fatalf("a finished task is not an interrupted one: %d", again.interrupted())
	}
}

func TestFileStoreUpdatesSurviveReopenWithoutAClose(t *testing.T) {
	dir := t.TempDir()
	clk := newClock()
	s, _ := openStore(t, dir, clk, 0)
	ctx := asUser("alice")
	v, _ := s.Create(ctx, task("t1", "c1", a2a.TaskStateSubmitted))
	done := withText(task("t1", "c1", a2a.TaskStateCompleted), "final")
	if _, err := s.Update(ctx, &taskstore.UpdateRequest{Task: done, PrevVersion: v}); err != nil {
		t.Fatal(err)
	}
	// A state change is on disk when Update returns: nothing else flushes it.
	again, _ := openStore(t, dir, clk, 0)
	if got := mustGet(t, again, "alice", "t1"); got.Status.State != a2a.TaskStateCompleted || got.Artifacts[0].Parts[0].Text() != "final" {
		t.Fatalf("%+v", got)
	}
}

func TestFileStoreOwnershipIsMaskedAsNotFoundAndSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	clk := newClock()
	s, _ := openStore(t, dir, clk, 0)
	if _, err := s.Create(asUser("alice"), task("t1", "c1", a2a.TaskStateCompleted)); err != nil {
		t.Fatal(err)
	}
	for name, st := range map[string]*fileStore{"same process": s, "after reopen": func() *fileStore { x, _ := openStore(t, dir, clk, 0); return x }()} {
		if _, err := st.Get(asUser("bob"), "t1"); !errors.Is(err, a2a.ErrTaskNotFound) {
			t.Errorf("%s: another principal's Get = %v, want task-not-found", name, err)
		}
		if _, err := st.Update(asUser("bob"), &taskstore.UpdateRequest{Task: task("t1", "c1", a2a.TaskStateFailed)}); !errors.Is(err, a2a.ErrTaskNotFound) {
			t.Errorf("%s: another principal's Update = %v, want task-not-found", name, err)
		}
		list, err := st.List(asUser("bob"), &a2a.ListTasksRequest{})
		if err != nil || len(list.Tasks) != 0 {
			t.Errorf("%s: bob lists %v %v", name, list, err)
		}
		if mine, err := st.List(asUser("alice"), &a2a.ListTasksRequest{}); err != nil || len(mine.Tasks) != 1 {
			t.Errorf("%s: alice lists %v %v", name, mine, err)
		}
	}
	if got := mustGet(t, s, "alice", "t1"); got.Status.State != a2a.TaskStateCompleted {
		t.Fatalf("bob's refused Update changed alice's task: %+v", got)
	}
}

func TestFileStoreRefusesAnUnauthenticatedCaller(t *testing.T) {
	s, _ := openStore(t, t.TempDir(), newClock(), 0)
	if _, err := s.Create(context.Background(), task("t1", "c1", a2a.TaskStateSubmitted)); err == nil {
		t.Fatal("a task with no owner was stored")
	}
	if _, err := s.List(context.Background(), &a2a.ListTasksRequest{}); !errors.Is(err, a2a.ErrUnauthenticated) {
		t.Fatalf("List with no owner: %v", err)
	}
}

func TestFileStoreCreateTwiceUpdateMissingAndStaleVersion(t *testing.T) {
	s, _ := openStore(t, t.TempDir(), newClock(), 0)
	ctx := asUser("alice")
	v, err := s.Create(ctx, task("t1", "c1", a2a.TaskStateSubmitted))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, task("t1", "c1", a2a.TaskStateSubmitted)); !errors.Is(err, taskstore.ErrTaskAlreadyExists) {
		t.Errorf("second Create = %v", err)
	}
	if _, err := s.Update(ctx, &taskstore.UpdateRequest{Task: task("nope", "c1", a2a.TaskStateWorking)}); !errors.Is(err, a2a.ErrTaskNotFound) {
		t.Errorf("Update of a missing task = %v", err)
	}
	v2, err := s.Update(ctx, &taskstore.UpdateRequest{Task: task("t1", "c1", a2a.TaskStateWorking), PrevVersion: v})
	if err != nil || !v2.After(v) {
		t.Fatalf("Update = %v %v", v2, err)
	}
	if _, err := s.Update(ctx, &taskstore.UpdateRequest{Task: task("t1", "c1", a2a.TaskStateCompleted), PrevVersion: v}); !errors.Is(err, taskstore.ErrConcurrentModification) {
		t.Errorf("stale Update = %v", err)
	}
	if got := mustGet(t, s, "alice", "t1"); got.Status.State != a2a.TaskStateWorking {
		t.Errorf("a refused update was applied: %v", got.Status.State)
	}
}

func TestFileStoreGetReturnsACopy(t *testing.T) {
	s, _ := openStore(t, t.TempDir(), newClock(), 0)
	ctx := asUser("alice")
	_, _ = s.Create(ctx, withText(task("t1", "c1", a2a.TaskStateCompleted), "keep"))
	got := mustGet(t, s, "alice", "t1")
	got.Status.State = a2a.TaskStateFailed
	got.Artifacts[0].Parts[0] = a2a.NewTextPart("changed")
	if again := mustGet(t, s, "alice", "t1"); again.Status.State != a2a.TaskStateCompleted || again.Artifacts[0].Parts[0].Text() != "keep" {
		t.Fatalf("the store handed out its own copy: %+v", again)
	}
}

func TestFileStoreListFiltersAndPages(t *testing.T) {
	clk := newClock()
	s, _ := openStore(t, t.TempDir(), clk, 0)
	ctx := asUser("alice")
	for i, id := range []string{"t1", "t2", "t3", "t4"} {
		state := a2a.TaskStateCompleted
		if i == 3 {
			state = a2a.TaskStateWorking
		}
		c := "c1"
		if i%2 == 1 {
			c = "c2"
		}
		clk.advance(time.Second)
		if _, err := s.Create(ctx, withText(task(id, c, state), "x")); err != nil {
			t.Fatal(err)
		}
	}
	ids := func(r *a2a.ListTasksResponse) string {
		var out []string
		for _, x := range r.Tasks {
			out = append(out, string(x.ID))
		}
		return strings.Join(out, ",")
	}
	all, err := s.List(ctx, &a2a.ListTasksRequest{})
	if err != nil || ids(all) != "t4,t3,t2,t1" || all.TotalSize != 4 {
		t.Fatalf("newest first: %v %v", ids(all), err)
	}
	if all.Tasks[0].Artifacts != nil {
		t.Error("artifacts were not asked for")
	}
	if withArt, _ := s.List(ctx, &a2a.ListTasksRequest{IncludeArtifacts: true}); len(withArt.Tasks[0].Artifacts) != 1 {
		t.Error("artifacts were asked for")
	}
	if r, _ := s.List(ctx, &a2a.ListTasksRequest{ContextID: "c2"}); ids(r) != "t4,t2" {
		t.Errorf("by context: %s", ids(r))
	}
	if r, _ := s.List(ctx, &a2a.ListTasksRequest{Status: a2a.TaskStateWorking}); ids(r) != "t4" {
		t.Errorf("by status: %s", ids(r))
	}
	p1, err := s.List(ctx, &a2a.ListTasksRequest{PageSize: 3})
	if err != nil || ids(p1) != "t4,t3,t2" || p1.NextPageToken == "" {
		t.Fatalf("page 1: %s %q %v", ids(p1), p1.NextPageToken, err)
	}
	p2, err := s.List(ctx, &a2a.ListTasksRequest{PageSize: 3, PageToken: p1.NextPageToken})
	if err != nil || ids(p2) != "t1" || p2.NextPageToken != "" {
		t.Fatalf("page 2: %s %q %v", ids(p2), p2.NextPageToken, err)
	}
	if _, err := s.List(ctx, &a2a.ListTasksRequest{PageSize: 101}); !errors.Is(err, a2a.ErrInvalidRequest) {
		t.Errorf("page size 101: %v", err)
	}
	if _, err := s.List(ctx, &a2a.ListTasksRequest{PageToken: "not a token"}); err == nil {
		t.Error("a bad page token was accepted")
	}
}

func TestFileStoreOrdersByLastUpdateAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	clk := newClock()
	s, _ := openStore(t, dir, clk, 0)
	ctx := asUser("alice")
	clk.advance(time.Second)
	v1, _ := s.Create(ctx, task("old", "c", a2a.TaskStateCompleted))
	clk.advance(time.Second)
	_, _ = s.Create(ctx, task("new", "c", a2a.TaskStateCompleted))
	clk.advance(time.Second)
	if _, err := s.Update(ctx, &taskstore.UpdateRequest{Task: withText(task("old", "c", a2a.TaskStateCompleted), "touched"), PrevVersion: v1}); err != nil {
		t.Fatal(err)
	}
	again, _ := openStore(t, dir, clk, 0)
	r, err := again.List(ctx, &a2a.ListTasksRequest{})
	if err != nil || len(r.Tasks) != 2 || r.Tasks[0].ID != "old" {
		t.Fatalf("the update time is part of the stored task: %+v %v", r, err)
	}
}

func TestFileStoreMarksInterruptedTasksFailedOnOpen(t *testing.T) {
	dir := t.TempDir()
	clk := newClock()
	s, _ := openStore(t, dir, clk, 0)
	ctx := asUser("alice")
	for id, state := range map[string]a2a.TaskState{"sub": a2a.TaskStateSubmitted, "work": a2a.TaskStateWorking, "done": a2a.TaskStateCompleted, "cancelled": a2a.TaskStateCanceled} {
		if _, err := s.Create(ctx, task(id, "c", state)); err != nil {
			t.Fatal(err)
		}
	}
	again, _ := openStore(t, dir, clk, 0)
	if again.interrupted() != 2 {
		t.Fatalf("interrupted = %d, want the two unfinished tasks", again.interrupted())
	}
	for _, id := range []string{"sub", "work"} {
		got := mustGet(t, again, "alice", id)
		if got.Status.State != a2a.TaskStateFailed {
			t.Errorf("%s: state %s, want failed", id, got.Status.State)
		}
		if msg := got.Status.Message; msg == nil || !strings.Contains(msg.Parts[0].Text(), "restarted") || msg.Role != a2a.MessageRoleAgent {
			t.Errorf("%s: the status must say why: %+v", id, msg)
		}
		if got.Status.Timestamp == nil || !got.Status.Timestamp.Equal(clk.now()) {
			t.Errorf("%s: failure time %v", id, got.Status.Timestamp)
		}
	}
	if got := mustGet(t, again, "alice", "done"); got.Status.State != a2a.TaskStateCompleted {
		t.Errorf("a finished task changed: %s", got.Status.State)
	}
	third, _ := openStore(t, dir, clk, 0)
	if third.interrupted() != 0 || mustGet(t, third, "alice", "work").Status.State != a2a.TaskStateFailed {
		t.Errorf("the failure is persisted, not recomputed: %d", third.interrupted())
	}
}

func TestFileStoreFinishedTasksExpireAfterTheRetention(t *testing.T) {
	dir := t.TempDir()
	clk := newClock()
	s, _ := openStore(t, dir, clk, 48*time.Hour)
	ctx := asUser("alice")
	_, _ = s.Create(ctx, task("old", "c", a2a.TaskStateCompleted))
	clk.advance(24 * time.Hour)
	_, _ = s.Create(ctx, task("mid", "c", a2a.TaskStateFailed))
	clk.advance(30 * time.Hour) // old is 54h old, mid is 30h old
	again, _ := openStore(t, dir, clk, 48*time.Hour)
	if _, err := again.Get(ctx, "old"); !errors.Is(err, a2a.ErrTaskNotFound) {
		t.Errorf("a task past the retention was kept: %v", err)
	}
	if mustGet(t, again, "alice", "mid").ID != "mid" {
		t.Error("a task inside the retention was dropped")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*"))
	if len(files) != 1 {
		t.Errorf("the expired task's file must be removed: %v", files)
	}
	// Creating a task prunes too, so a listener that never restarts does not grow without bound.
	clk.advance(30 * time.Hour)
	_, _ = again.Create(ctx, task("fresh", "c", a2a.TaskStateSubmitted))
	if _, err := again.Get(ctx, "mid"); !errors.Is(err, a2a.ErrTaskNotFound) {
		t.Errorf("Create did not prune: %v", err)
	}
}

func TestFileStoreNeverExpiresAnUnfinishedTask(t *testing.T) {
	clk := newClock()
	dir := t.TempDir()
	s, _ := openStore(t, dir, clk, time.Hour)
	ctx := asUser("alice")
	_, _ = s.Create(ctx, task("running", "c", a2a.TaskStateWorking))
	clk.advance(10 * time.Hour)
	_, _ = s.Create(ctx, task("other", "c", a2a.TaskStateSubmitted))
	if mustGet(t, s, "alice", "running").ID != "running" {
		t.Fatal("a running task was pruned")
	}
}

func TestFileStoreSkipsACorruptFileAndSaysSo(t *testing.T) {
	dir := t.TempDir()
	clk := newClock()
	s, _ := openStore(t, dir, clk, 0)
	_, _ = s.Create(asUser("alice"), task("good", "c", a2a.TaskStateCompleted))
	if err := os.WriteFile(filepath.Join(dir, "garbage.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	again, logs := openStore(t, dir, clk, 0)
	if mustGet(t, again, "alice", "good").ID != "good" {
		t.Fatal("one bad file lost the good task")
	}
	if len(*logs) == 0 || !strings.Contains(strings.Join(*logs, "\n"), "garbage.json") {
		t.Fatalf("a skipped file is reported: %v", *logs)
	}
}

func TestFileStoreFilesAreOwnerOnlyAndNamedByDigest(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	dir := filepath.Join(t.TempDir(), "tasks")
	s, _ := openStore(t, dir, newClock(), 0)
	hostile := "../../escape"
	if _, err := s.Create(asUser("alice"), task(hostile, "c", a2a.TaskStateCompleted)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("directory %v %v", info, err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*"))
	if len(files) != 1 || strings.Contains(filepath.Base(files[0]), "escape") {
		t.Fatalf("files %v: a task id must never reach a file name", files)
	}
	if fi, _ := os.Stat(files[0]); fi.Mode().Perm() != 0o600 {
		t.Fatalf("task file mode %v", fi.Mode())
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape.json")); err == nil {
		t.Fatal("a hostile task id wrote outside the store")
	}
	if mustGet(t, s, "alice", hostile).ID != a2a.TaskID(hostile) {
		t.Fatal("a hostile id must still round-trip")
	}
}

func TestFileStoreDoesNotWriteEveryArtifactChunk(t *testing.T) {
	dir := t.TempDir()
	clk := newClock()
	s, _ := openStore(t, dir, clk, 0)
	ctx := asUser("alice")
	v, _ := s.Create(ctx, task("t1", "c", a2a.TaskStateWorking))
	before := s.writes()
	text := ""
	for range 200 {
		text += "chunk "
		nv, err := s.Update(ctx, &taskstore.UpdateRequest{Task: withText(task("t1", "c", a2a.TaskStateWorking), text), PrevVersion: v})
		if err != nil {
			t.Fatal(err)
		}
		v = nv
	}
	if n := s.writes() - before; n > 2 {
		t.Fatalf("%d writes for 200 streamed chunks inside one interval: every chunk rewrote the task file", n)
	}
	// The latest text is still served from memory, and a later interval writes it.
	if got := mustGet(t, s, "alice", "t1"); got.Artifacts[0].Parts[0].Text() != text {
		t.Fatal("the in-memory task lags the stream")
	}
	clk.advance(time.Second)
	if _, err := s.Update(ctx, &taskstore.UpdateRequest{Task: withText(task("t1", "c", a2a.TaskStateWorking), text+"more"), PrevVersion: v}); err != nil {
		t.Fatal(err)
	}
	again, _ := openStore(t, dir, clk, 0)
	if got := mustGet(t, again, "alice", "t1"); !strings.HasSuffix(got.Artifacts[0].Parts[0].Text(), "more") && got.Status.State != a2a.TaskStateFailed {
		t.Fatalf("%+v", got)
	}
}

func TestFileStoreStateChangeIsWrittenEvenInsideTheInterval(t *testing.T) {
	dir := t.TempDir()
	clk := newClock()
	s, _ := openStore(t, dir, clk, 0)
	ctx := asUser("alice")
	v, _ := s.Create(ctx, task("t1", "c", a2a.TaskStateWorking))
	if _, err := s.Update(ctx, &taskstore.UpdateRequest{Task: withText(task("t1", "c", a2a.TaskStateCompleted), "end"), PrevVersion: v}); err != nil {
		t.Fatal(err)
	}
	again, _ := openStore(t, dir, clk, 0)
	if got := mustGet(t, again, "alice", "t1"); got.Status.State != a2a.TaskStateCompleted || got.Artifacts[0].Parts[0].Text() != "end" {
		t.Fatalf("the terminal state, with the whole reply, is on disk when Update returns: %+v", got)
	}
}

func TestFileStoreWithoutADirectoryIsInMemory(t *testing.T) {
	s, err := openTaskStore(storeOptions{Auth: userAuth, Now: newClock().now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(asUser("alice"), task("t1", "c", a2a.TaskStateCompleted)); err != nil {
		t.Fatal(err)
	}
	if mustGet(t, s, "alice", "t1").ID != "t1" {
		t.Fatal("in-memory store lost a task")
	}
}

func TestFileStoreConcurrentUpdatesAreSerialised(t *testing.T) {
	s, _ := openStore(t, t.TempDir(), newClock(), 0)
	ctx := asUser("alice")
	var wins atomic.Int32
	v, _ := s.Create(ctx, task("t1", "c", a2a.TaskStateWorking))
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Update(ctx, &taskstore.UpdateRequest{Task: task("t1", "c", a2a.TaskStateCompleted), PrevVersion: v}); err == nil {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("%d updates with the same previous version won, want exactly 1", wins.Load())
	}
}
