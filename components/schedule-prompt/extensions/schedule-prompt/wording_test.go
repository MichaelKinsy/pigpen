package schedule_prompt

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/schedule-prompt/internal/jsdate"
	"github.com/MichaelKinsy/pigpen/schedule-prompt/internal/jsobj"
)

// Cases of the port beyond the original's tests: the wording of every tool result and the side effects the original's tests leave out
// (they stand in a scheduler that does nothing). Each text is the original's, from src/tool.ts.

type recSched struct {
	added, removed []string
	updated        []*jsobj.Object
	next           float64
}

func (r *recSched) addJob(j *jsobj.Object)              { r.added = append(r.added, jobID(j)) }
func (r *recSched) removeJob(id string)                 { r.removed = append(r.removed, id) }
func (r *recSched) updateJob(_ string, j *jsobj.Object) { r.updated = append(r.updated, j) }
func (r *recSched) getNextRun(string) float64           { return r.next }

func recTool(scope string, seed ...*jsobj.Object) (toolDeps, *memStorage, *recSched) {
	st := makeStorage(seed...)
	rs := &recSched{next: math.NaN()}
	return toolDeps{storage: func() jobStore { return st }, scheduler: func() jobScheduler { return rs }, defaultScope: func() string { return scope }}, st, rs
}

func run1(t *testing.T, d toolDeps, p obj) toolOutput {
	t.Helper()
	return callTool(t, d, p, makeCtx("s1"))
}

func TestAddResultShape(t *testing.T) {
	d, st, rs := recTool("session")
	out := run1(t, d, obj{"action": "add", "schedule": "5m", "type": "interval", "prompt": "p", "description": "d"})
	job := detailJobs(out)[0].(*jsobj.Object)
	id := job.Get("id").(string)
	name := job.Get("name").(string)
	if len(id) != 10 || !strings.HasPrefix(name, "job-") || len(name) != 10 {
		t.Fatalf("id %q name %q", id, name)
	}
	if job.Get("intervalMs") != float64(300000) || job.Get("type") != "interval" || job.Get("schedule") != "5m" || job.Get("runCount") != float64(0) {
		t.Fatalf("job = %v", job)
	}
	if created := jsdate.Parse(job.Get("createdAt").(string)); math.Abs(created-jsdate.Now()) > 5000 {
		t.Fatalf("createdAt = %v", job.Get("createdAt"))
	}
	if out.Details.Get("jobId") != id || out.Details.Get("jobName") != name || out.Details.Get("action") != "add" {
		t.Fatalf("details = %v", out.Details)
	}
	if out.Text != `✓ Created cron job "`+name+`" (`+id+")\nType: interval\nSchedule: 5m\nPrompt: p" {
		t.Fatalf("text = %q", out.Text)
	}
	if len(rs.added) != 1 || rs.added[0] != id {
		t.Fatalf("scheduler adds = %v", rs.added)
	}
	if stored, _ := st.GetJob(id); stored == nil || stored.Get("description") != "d" {
		t.Fatalf("stored = %v", stored)
	}
	// Each missing parameter alone is named, in order.
	for p, want := range map[string]string{"schedule": "'schedule'", "prompt": "'prompt'"} {
		params := obj{"action": "add", "schedule": "+10s", "prompt": "x"}
		delete(params, p)
		o := run1(t, d, params)
		if !strings.Contains(o.Text, "parameters for add action: "+want+". You must") {
			t.Errorf("without %s: %q", p, o.Text)
		}
	}
}

func TestRemoveEnableDisableWording(t *testing.T) {
	d, st, rs := recTool("session", toolJob(obj{"id": "j1", "name": "alpha"}))
	if o := run1(t, d, obj{"action": "remove"}); o.Text != "✗ Error: jobId is required for remove action" {
		t.Errorf("%q", o.Text)
	}
	if o := run1(t, d, obj{"action": "remove", "jobId": "zz"}); o.Text != "✗ Error: Job not found: zz" {
		t.Errorf("%q", o.Text)
	}
	if o := run1(t, d, obj{"action": "disable", "jobId": "j1"}); o.Text != `✓ Disabled cron job "alpha" (j1)` || o.Details.Get("jobId") != "j1" || o.Details.Get("jobName") != "alpha" {
		t.Errorf("%q", o.Text)
	}
	if j, _ := st.GetJob("j1"); j.Get("enabled") != false {
		t.Error("disable did not store")
	}
	if len(rs.updated) != 1 || rs.updated[0].Get("enabled") != false {
		t.Errorf("scheduler updates = %v", rs.updated)
	}
	if o := run1(t, d, obj{"action": "enable", "jobId": "j1"}); o.Text != `✓ Enabled cron job "alpha" (j1)` {
		t.Errorf("%q", o.Text)
	}
	if j, _ := st.GetJob("j1"); j.Get("enabled") != true {
		t.Error("enable did not store")
	}
	if len(rs.updated) != 2 || rs.updated[1].Get("enabled") != true {
		t.Errorf("scheduler updates = %v", rs.updated)
	}
	if o := run1(t, d, obj{"action": "enable"}); o.Text != "✗ Error: jobId is required for enable action" {
		t.Errorf("%q", o.Text)
	}
	if o := run1(t, d, obj{"action": "remove", "jobId": "j1"}); o.Text != `✓ Removed cron job "alpha" (j1)` || len(rs.removed) != 1 || rs.removed[0] != "j1" {
		t.Errorf("%q removed %v", o.Text, rs.removed)
	}
	if j, _ := st.GetJob("j1"); j != nil {
		t.Error("remove did not store")
	}
}

func TestCleanupWording(t *testing.T) {
	a := toolJob(obj{"id": "a", "name": "alpha", "enabled": false})
	b := toolJob(obj{"id": "b", "name": "beta", "enabled": false})
	live := toolJob(obj{"id": "c", "name": "gamma"})
	d, _, rs := recTool("session", a, b, live)
	o := run1(t, d, obj{"action": "cleanup"})
	if o.Text != "✓ Removed 2 disabled job(s):\n  - alpha (a)\n  - beta (b)" {
		t.Errorf("%q", o.Text)
	}
	if strings.Join(rs.removed, ",") != "a,b" {
		t.Errorf("scheduler removes = %v", rs.removed)
	}
	if o := run1(t, d, obj{"action": "cleanup"}); o.Text != "No disabled jobs to clean up" || len(detailJobs(o)) != 0 {
		t.Errorf("%q", o.Text)
	}
}

func TestUpdateWording(t *testing.T) {
	d, st, rs := recTool("session", toolJob(obj{"id": "u1", "name": "old", "type": "interval", "schedule": "5m", "intervalMs": float64(300000)}))
	if o := run1(t, d, obj{"action": "update"}); o.Text != "✗ Error: jobId is required for update action" {
		t.Errorf("%q", o.Text)
	}
	if o := run1(t, d, obj{"action": "update", "jobId": "nope"}); o.Text != "✗ Error: Job not found: nope" {
		t.Errorf("%q", o.Text)
	}
	// An empty name is ignored (falsy), a schedule re-resolves the interval.
	o := run1(t, d, obj{"action": "update", "jobId": "u1", "name": "", "schedule": "10m"})
	if o.Text != `✓ Updated cron job "old" (u1)` {
		t.Errorf("%q", o.Text)
	}
	j, _ := st.GetJob("u1")
	if j.Get("name") != "old" || j.Get("schedule") != "10m" || j.Get("intervalMs") != float64(600000) {
		t.Errorf("job = %v", j)
	}
	if len(rs.updated) != 1 || rs.updated[0].Get("intervalMs") != float64(600000) {
		t.Errorf("scheduler updates = %v", rs.updated)
	}
	if o := run1(t, d, obj{"action": "update", "jobId": "u1", "name": "new"}); o.Text != `✓ Updated cron job "new" (u1)` {
		t.Errorf("%q", o.Text)
	}
}

func TestListNextRunAndEmptyStates(t *testing.T) {
	d, _, rs := recTool("session", toolJob(obj{"id": "l1", "name": "n", "lastStatus": "error", "runCount": float64(2)}))
	rs.next = 1_800_000_000_000
	o := run1(t, d, obj{"action": "list"})
	want := "  Never run | Next: 2027-01-15T08:00:00.000Z\n  Runs: 2 | Status: error"
	if !strings.Contains(o.Text, want) {
		t.Errorf("%q", o.Text)
	}
	if len(detailJobs(o)) != 1 {
		t.Error("details.jobs")
	}
}

func TestSettingsEdges(t *testing.T) {
	cwd := settingsCwd(t)
	// a string widgetVisible is not a boolean: dropped
	writeProjectSettings(t, cwd, `{"widgetVisible":"no"}`)
	if loadSettings(cwd).widgetVisible != nil {
		t.Error("a non-boolean widgetVisible was accepted")
	}
	// the project file keeps the other known member when one changes (and nothing else)
	writeProjectSettings(t, cwd, `{"defaultJobScope":"workdir","widgetVisible":false,"other":1}`)
	if !saveSettings(cwd, boolp(true), "") {
		t.Fatal("not saved")
	}
	if got, _ := jsobj.Stringify(readProjectFile(t, cwd)); got != "{\n  \"widgetVisible\": true,\n  \"defaultJobScope\": \"workdir\"\n}" {
		t.Errorf("file = %s", got)
	}
	// a directory in the file's place makes the write fail: reported, not ignored
	bad := t.TempDir()
	if err := os.MkdirAll(filepath.Join(bad, ".pi", "schedule-prompts-settings.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if saveSettings(bad, boolp(true), "") {
		t.Error("a failed write was reported as saved")
	}
}

func TestStopSilencesTheCronAndAMinimumInterval(t *testing.T) {
	job := exampleJob(obj{"type": "cron", "schedule": "0 0 * * * *"})
	s := newSched(makeStorage(job), &mockPi{}, makeCtx(), nil)
	s.scheduleJob(job)
	s.mu.Lock()
	c := s.jobs["job-1"]
	s.mu.Unlock()
	if math.IsNaN(c.NextRun()) {
		t.Fatal("a scheduled cron has no next run")
	}
	s.stop()
	if !math.IsNaN(c.NextRun()) {
		t.Fatal("stop() left the cron alive")
	}
	// a sub-millisecond interval is one millisecond, not a second
	pi := &mockPi{}
	iv := exampleJob(obj{"id": "iv", "type": "interval", "schedule": "x", "intervalMs": 0.5})
	s2 := newSched(makeStorage(iv), pi, makeCtx(), nil)
	s2.scheduleJob(iv)
	waitFor(t, func() bool { pi.mu.Lock(); defer pi.mu.Unlock(); return len(pi.users) >= 2 })
	s2.stop()
	_ = time.Now
}

func TestMarkerTextsOfTheSubagentPath(t *testing.T) {
	pi := &mockPi{sendError: func(call int) error {
		if call == 1 {
			return nil
		}
		return errSimulated
	}}
	logged := captureLog(t)
	job := exampleJob(obj{"model": "haiku", "notify": "yes"})
	st := makeStorage(job)
	s := newSched(st, pi, makeCtx(), (&runnerCalls{}).runner(subagentResult{Error: "bad"}))
	s.executeJobInSubagent(job)
	waitFor(t, func() bool { return len(logged()) > 0 })
	// notify is `=== true`: the string "yes" does not wake the parent
	if m := pi.message(1); m.opts != nil {
		t.Errorf("opts = %+v", m.opts)
	}
	if l := logged()[0]; !strings.HasPrefix(l, "Failed to post subagent_error marker for job job-1: ") {
		t.Errorf("log = %q", l)
	}
	if m := pi.message(0); contentText(t, m.msg)[0] != "🕐 Scheduled (subagent: haiku): demo" {
		t.Errorf("start = %v", contentText(t, m.msg))
	}
}
