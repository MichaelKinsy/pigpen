package pi_goal_x

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Helpers ported from goal-service.test.ts: an in-memory ref mirroring the extension's closure state, and a fixture.

type gsHookLog struct {
	goalLost     []string
	reconciled   []string
	focusChanges [][2]string
	diagnostics  []goalDiagnostic
}

type gsRef struct {
	pool      *goalPool
	focusedId string
	revision  int
	log       *gsHookLog
}

func (r *gsRef) getFocused() *jsObject {
	if r.focusedId == "" {
		return nil
	}
	return r.pool.get(r.focusedId)
}

func (r *gsRef) setFocused(next *jsObject) {
	if next != nil {
		r.pool.set(gstr(next, "id"), next)
		r.focusedId = gstr(next, "id")
		return
	}
	if r.focusedId != "" {
		r.pool.delete(r.focusedId)
	}
	r.focusedId = ""
}
func (r *gsRef) getPool() *goalPool         { return r.pool }
func (r *gsRef) replacePool(next *goalPool) { r.pool = next }
func (r *gsRef) getFocusedGoalId() string   { return r.focusedId }
func (r *gsRef) assignFocusedGoalId(id string) {
	if r.focusedId != id {
		r.revision++
		r.focusedId = id
	}
}
func (r *gsRef) focusToken(goalId string) goalFocusToken { return goalFocusToken{goalId, r.revision} }
func (r *gsRef) isTokenCurrent(t goalFocusToken) bool {
	return r.focusedId == t.goalId && r.revision == t.revision
}
func (r *gsRef) appendFocusEntry(string, string) {}
func (r *gsRef) onFocusedGoalLost(lost string, _ goalServiceContext) {
	r.log.goalLost = append(r.log.goalLost, lost)
}
func (r *gsRef) onReconciled(g *jsObject) { r.log.reconciled = append(r.log.reconciled, gstr(g, "id")) }
func (r *gsRef) onFocusChanged(from, to string) {
	r.log.focusChanges = append(r.log.focusChanges, [2]string{from, to})
}
func (r *gsRef) onDiagnostic(d goalDiagnostic) { r.log.diagnostics = append(r.log.diagnostics, d) }

func gsMakeRef(goal *jsObject) (*gsRef, *gsHookLog) {
	pool := newPool()
	pool.set(gstr(goal, "id"), goal)
	log := &gsHookLog{}
	return &gsRef{pool: pool, focusedId: gstr(goal, "id"), log: log}, log
}

type gsFixture struct {
	t       *testing.T
	cwd     string
	ctx     storage
	written *jsObject
	ref     *gsRef
	log     *gsHookLog
	service *goalService
}

func gsNewFixture(t *testing.T) *gsFixture {
	t.Helper()
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".pi", "goals", "archived"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := storage{cwd: cwd}
	invalidateGoalPoolCache()
	goal := createGoal("=== Goal ===\nObjective: Service test", true, false, false, ms(2026, 8, 1, 9, 0, 0))
	written, err := ctx.writeActiveGoalFile(goal)
	if err != nil {
		t.Fatal(err)
	}
	ref, log := gsMakeRef(written)
	return &gsFixture{t: t, cwd: cwd, ctx: ctx, written: written, ref: ref, log: log, service: newGoalService(ref)}
}

func (f *gsFixture) activeFiles() []string {
	entries, _ := os.ReadDir(filepath.Join(f.cwd, ".pi", "goals"))
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "active_goal_") {
			out = append(out, e.Name())
		}
	}
	return out
}

func (f *gsFixture) activePath(i int) string {
	return filepath.Join(f.cwd, ".pi", "goals", f.activeFiles()[i])
}

func (f *gsFixture) ledgerEvents() []*jsObject {
	raw, err := os.ReadFile(goalLedgerPath(f.ctx))
	if err != nil {
		return nil
	}
	var out []*jsObject
	for _, line := range strings.Split(string(raw), "\n") {
		if line != "" {
			out = append(out, jo(f.t, line))
		}
	}
	return out
}

// secondWriter is a second goalService + ref on the same cwd (a separate session).
func (f *gsFixture) secondWriter() (*goalService, *gsRef, *gsHookLog) {
	disk := parseGoalFile(f.activePath(0))
	ref, log := gsMakeRef(disk)
	return newGoalService(ref), ref, log
}

func gsMust[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func gsSetObjective(obj string) func(*jsObject) *jsObject {
	return func(g *jsObject) *jsObject { return goalWith(g, "objective", obj) }
}

func gsTaskList(id, title string, block bool) *jsObject {
	task := newObject()
	task.set("id", id)
	task.set("title", title)
	task.set("status", "pending")
	tl := newObject()
	tl.set("tasks", []any{task})
	tl.set("blockCompletion", block)
	tl.set("proposedAt", nowIso())
	return tl
}

func gsPausedEvent(goalId, reason, at string) *jsObject {
	return ledgerEvent("goal_paused", "goalId", goalId, "reason", reason, "status", "paused", "at", at)
}

func gsReadFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
