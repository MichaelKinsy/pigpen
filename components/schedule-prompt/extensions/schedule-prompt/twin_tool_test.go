package schedule_prompt

import (
	"math"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/schedule-prompt/internal/jsdate"
	"github.com/MichaelKinsy/pigpen/schedule-prompt/internal/jsobj"
)

// Twins of the original's test/tool.test.ts (23 cases). The scheduler is the original's stub (no-op add, remove and update; no next run).

type stubScheduler struct{}

func (stubScheduler) addJob(*jsobj.Object)            {}
func (stubScheduler) removeJob(string)                {}
func (stubScheduler) updateJob(string, *jsobj.Object) {}
func (stubScheduler) getNextRun(string) float64       { return math.NaN() }

func buildTool(defaultScope string, seed ...*jsobj.Object) (toolDeps, *memStorage) {
	st := makeStorage(seed...)
	return toolDeps{
		storage:      func() jobStore { return st },
		scheduler:    func() jobScheduler { return stubScheduler{} },
		defaultScope: func() string { return defaultScope },
	}, st
}

func toolJob(overrides obj) *jsobj.Object {
	j := exampleJob(overrides)
	if _, ok := overrides["id"]; !ok {
		j.Set("id", "abc1234567")
	}
	if _, ok := overrides["name"]; !ok {
		j.Set("name", "demo")
	}
	j.Set("prompt", "test")
	return j
}

func detailsError(out toolOutput) any { return out.Details.Get("error") }

func detailJobs(out toolOutput) []any { return out.Details.Get("jobs").([]any) }

const tool_ = "tool"

func callTool(t *testing.T, d toolDeps, params obj, sess fakeSession) toolOutput {
	t.Helper()
	out, err := d.execute(params, sess)
	if err != nil {
		t.Fatalf("execute returned the error %v", err)
	}
	return out
}

func requireNoError(t *testing.T, out toolOutput) {
	t.Helper()
	if e := detailsError(out); !jsobj.IsUndefined(e) {
		t.Fatalf("details.error = %v", e)
	}
}

func addParams(extra obj) obj {
	p := obj{"action": "add", "schedule": "+10s", "type": "once", "prompt": "test"}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

func TestToolNotify(t *testing.T) {
	tw(t, tool_, "accepts notify=true without model (no-op for inline jobs)", func(t *testing.T) {
		d, _ := buildTool("session")
		out := callTool(t, d, addParams(obj{"notify": true}), makeCtx())
		requireNoError(t, out)
		job := detailJobs(out)[0].(*jsobj.Object)
		if job.Get("notify") != true || !jsobj.IsUndefined(job.Get("model")) {
			t.Fatalf("job = %v", job)
		}
	})
	tskip(t, tool_, "accepts notify=true with model", "slice 4: the call passes model=haiku, which the per-task model mode refuses until the SDK has agent sessions (TestModelIsRefused)")
	tw(t, tool_, "accepts notify=false without model", func(t *testing.T) {
		d, _ := buildTool("session")
		requireNoError(t, callTool(t, d, addParams(obj{"notify": false}), makeCtx()))
	})
	tskip(t, tool_, "accepts model without notify (defaults to silent)", "slice 4: model=haiku is refused for now; the notify default of a refused job cannot be observed")

	tw(t, tool_, "accepts setting notify=true on an existing inline (no-model) job (no-op)", func(t *testing.T) {
		d, _ := buildTool("session", toolJob(obj{"id": "j1"}))
		requireNoError(t, callTool(t, d, obj{"action": "update", "jobId": "j1", "notify": true}, makeCtx()))
	})
	tskip(t, tool_, "accepts setting notify=true alongside model in the same call", "slice 4: the update passes model=haiku, which is refused until the SDK has agent sessions")
	tw(t, tool_, "accepts setting notify=true on a job that already has model", func(t *testing.T) {
		d, _ := buildTool("session", toolJob(obj{"id": "j3", "model": "haiku"}))
		requireNoError(t, callTool(t, d, obj{"action": "update", "jobId": "j3", "notify": true}, makeCtx()))
	})
	tw(t, tool_, "rejects empty-string model on update (must remove + re-add to clear)", func(t *testing.T) {
		d, _ := buildTool("session", toolJob(obj{"id": "j4", "model": "haiku", "notify": true}))
		out := callTool(t, d, obj{"action": "update", "jobId": "j4", "model": ""}, makeCtx())
		if e, _ := detailsError(out).(string); !strings.Contains(e, "'model' must be a non-empty string") {
			t.Fatalf("error = %v", detailsError(out))
		}
	})
	tw(t, tool_, "rejects empty-string model on add", func(t *testing.T) {
		d, _ := buildTool("session")
		out := callTool(t, d, addParams(obj{"model": ""}), makeCtx())
		if e, _ := detailsError(out).(string); !strings.Contains(e, "'model' must be a non-empty string") {
			t.Fatalf("error = %v", detailsError(out))
		}
	})
}

func TestToolUpdateSchedule(t *testing.T) {
	tw(t, tool_, "accepts schedule='+5m' on update for a once job (resolves to ISO)", func(t *testing.T) {
		d, st := buildTool("session", toolJob(obj{"id": "j6", "type": "once", "schedule": "2099-01-01T00:00:00.000Z"}))
		out := callTool(t, d, obj{"action": "update", "jobId": "j6", "schedule": "+5m"}, makeCtx())
		requireNoError(t, out)
		// The stored schedule is now an ISO timestamp about five minutes away, not the literal "+5m".
		j, _ := st.GetJob("j6")
		sched := j.Get("schedule").(string)
		if sched == "+5m" {
			t.Fatal("the relative time was stored as it is")
		}
		parsed := jsdate.Parse(sched)
		if parsed != parsed {
			t.Fatalf("not a date: %q", sched)
		}
		if delta := parsed - jsdate.Now(); delta <= 4*60*1000 || delta >= 6*60*1000 {
			t.Fatalf("delta %v", delta)
		}
	})
	tw(t, tool_, "rejects schedule='+5m' on update for a cron job (relative time only valid for once)", func(t *testing.T) {
		d, _ := buildTool("session", toolJob(obj{"id": "j7", "type": "cron", "schedule": "0 * * * * *"}))
		out := callTool(t, d, obj{"action": "update", "jobId": "j7", "schedule": "+5m"}, makeCtx())
		// For the cron type the schedule must be a valid cron expression: +5m is not.
		if jsobj.IsUndefined(detailsError(out)) {
			t.Fatal("no error")
		}
	})
}

func TestToolSessionBinding(t *testing.T) {
	tw(t, tool_, "add with default scope ('session') stamps session = current sessionId", func(t *testing.T) {
		d, st := buildTool("session")
		out := callTool(t, d, obj{"action": "add", "schedule": "+10s", "type": "once", "prompt": "hi"}, makeCtx("session-A"))
		requireNoError(t, out)
		id := detailJobs(out)[0].(*jsobj.Object).Get("id").(string)
		if j, _ := st.GetJob(id); j.Get("session") != "session-A" {
			t.Fatalf("session = %v", j.Get("session"))
		}
	})
	tw(t, tool_, "add with scope='workdir' omits the session field", func(t *testing.T) {
		d, st := buildTool("workdir")
		out := callTool(t, d, obj{"action": "add", "schedule": "+10s", "type": "once", "prompt": "hi"}, makeCtx("session-A"))
		requireNoError(t, out)
		id := detailJobs(out)[0].(*jsobj.Object).Get("id").(string)
		// The persisted job has no session: every pi loads it.
		if j, _ := st.GetJob(id); !jsobj.IsUndefined(j.Get("session")) {
			t.Fatalf("session = %v", j.Get("session"))
		}
	})
	tw(t, tool_, "list filters out foreign-session jobs but keeps unbound jobs", func(t *testing.T) {
		mine := toolJob(obj{"id": "mine", "name": "mine", "session": "session-A"})
		foreign := toolJob(obj{"id": "foreign", "name": "foreign", "session": "session-B"})
		unbound := toolJob(obj{"id": "unbound", "name": "unbound"})
		d, _ := buildTool("session", mine, foreign, unbound)
		out := callTool(t, d, obj{"action": "list"}, makeCtx("session-A"))
		var ids []string
		for _, j := range detailJobs(out) {
			ids = append(ids, j.(*jsobj.Object).Get("id").(string))
		}
		if strings.Join(ids, ",") != "mine,unbound" {
			t.Fatalf("ids = %v", ids)
		}
	})
	tw(t, tool_, "cleanup removes only this session's disabled jobs (foreign disabled jobs preserved)", func(t *testing.T) {
		mine := toolJob(obj{"id": "mine", "name": "mine", "enabled": false, "session": "session-A"})
		foreign := toolJob(obj{"id": "foreign", "name": "foreign", "enabled": false, "session": "session-B"})
		unbound := toolJob(obj{"id": "unbound", "name": "unbound", "enabled": false})
		live := toolJob(obj{"id": "live", "name": "live", "enabled": true, "session": "session-A"})
		d, st := buildTool("session", mine, foreign, unbound, live)
		out := callTool(t, d, obj{"action": "cleanup"}, makeCtx("session-A"))
		requireNoError(t, out)
		// Mine and the unbound disabled job were removed; the foreign disabled job was left for its owner.
		for id, want := range map[string]bool{"mine": false, "unbound": false, "foreign": true, "live": true} {
			if j, _ := st.GetJob(id); (j != nil) != want {
				t.Fatalf("job %s present = %v, want %v", id, j != nil, want)
			}
		}
	})
}

func TestToolExtensionsSkills(t *testing.T) {
	tskip(t, tool_, "accepts extensions=true and stores it on the job", "slice 4: the add passes model=haiku with extensions=true; the model mode is refused until the SDK has agent sessions")
	tskip(t, tool_, "accepts skills=true and stores it on the job", "slice 4: the add passes model=haiku with skills=true; the model mode is refused until the SDK has agent sessions")
	tskip(t, tool_, "defaults extensions to undefined when not provided", "slice 4: the add passes model=haiku, so the job it would create is refused; the default of extensions is covered through an inline add in TestInlineAddDefaults")
	tskip(t, tool_, "defaults skills to undefined when not provided", "slice 4: the add passes model=haiku, so the job it would create is refused; the default of skills is covered through an inline add in TestInlineAddDefaults")

	update := func(t *testing.T, id string, seed obj, key string, value bool) {
		d, st := buildTool("session", toolJob(merge(obj{"id": id, "model": "haiku"}, seed)))
		requireNoError(t, callTool(t, d, obj{"action": "update", "jobId": id, key: value}, makeCtx()))
		if j, _ := st.GetJob(id); j.Get(key) != value {
			t.Fatalf("%s = %v", key, j.Get(key))
		}
	}
	tw(t, tool_, "accepts setting extensions=true on an existing subagent job", func(t *testing.T) { update(t, "j1", nil, "extensions", true) })
	tw(t, tool_, "accepts setting extensions=false on an existing subagent job", func(t *testing.T) {
		update(t, "j1", obj{"extensions": true}, "extensions", false)
	})
	tw(t, tool_, "accepts setting skills=true on an existing subagent job", func(t *testing.T) { update(t, "j2", nil, "skills", true) })
	tw(t, tool_, "accepts setting skills=false on an existing subagent job", func(t *testing.T) {
		update(t, "j2", obj{"skills": true}, "skills", false)
	})
}

func merge(a, b obj) obj {
	out := obj{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}
