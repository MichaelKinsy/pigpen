package schedule_prompt

import (
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/schedule-prompt/internal/jsobj"
)

// Cases of the port beyond the original's tests: the refusal of the per-task model mode and the behavior the original has in code only.

// TestModelIsRefused: add and update with a model are refused with the program's message as a tool error, and nothing is stored.
func TestModelIsRefused(t *testing.T) {
	want := "@pi-in-go/pigpen-schedule-prompt (Go port) does not support the per-task model mode (the model parameter: a prompt run in a separate agent session) yet. Install the original extension, npm:pi-schedule-prompt@0.4.1, which runs unchanged in PiG."
	d, st := buildTool("session", toolJob(obj{"id": "j1"}))
	for _, params := range []obj{
		addParams(obj{"model": "haiku", "notify": true}),
		addParams(obj{"model": "sonnet"}),
		{"action": "update", "jobId": "j1", "model": "haiku"},
		{"action": "update", "jobId": "j1", "model": "haiku", "notify": true},
	} {
		_, err := d.execute(params, makeCtx())
		if err == nil || err.Error() != want {
			t.Fatalf("params %v: err = %v", params, err)
		}
		var r *refusalError
		if !errors.As(err, &r) {
			t.Fatal("the refusal is not a tool error")
		}
	}
	all, _ := st.GetAllJobs()
	if len(all) != 1 {
		t.Fatalf("a refused call stored a job: %d", len(all))
	}
	if j, _ := st.GetJob("j1"); !jsobj.IsUndefined(j.Get("model")) || !jsobj.IsUndefined(j.Get("notify")) {
		t.Fatalf("a refused update changed the job: %v", j)
	}
}

// TestInlineAddDefaults: extensions and skills stay undefined when the call does not pass them (the cases of the original that add with a
// model are in slice 4).
func TestInlineAddDefaults(t *testing.T) {
	d, _ := buildTool("session")
	out := callTool(t, d, addParams(nil), makeCtx())
	job := detailJobs(out)[0].(*jsobj.Object)
	for _, k := range []string{"extensions", "skills", "model", "notify", "description"} {
		if !jsobj.IsUndefined(job.Get(k)) {
			t.Fatalf("%s = %v", k, job.Get(k))
		}
	}
	out = callTool(t, d, addParams(obj{"name": "x", "extensions": true, "skills": []any{"git"}}), makeCtx())
	job = detailJobs(out)[0].(*jsobj.Object)
	if job.Get("extensions") != true {
		t.Fatalf("extensions = %v", job.Get("extensions"))
	}
	if s, _ := jsobj.Stringify(job.Get("skills")); s != "[\n  \"git\"\n]" {
		t.Fatalf("skills = %s", s)
	}
}

func TestAddValidation(t *testing.T) {
	d, _ := buildTool("session", toolJob(obj{"id": "j1", "name": "taken"}))
	cases := []struct {
		name   string
		params obj
		want   string
	}{
		{"missing both", obj{"action": "add"}, "Missing required parameters for add action: 'schedule' and 'prompt'. You must provide both schedule (e.g., '+10s', '*/5 * * * * *') and prompt (the text to execute)."},
		{"missing prompt", obj{"action": "add", "schedule": "+5s"}, "Missing required parameters for add action: 'prompt'. You must provide both schedule (e.g., '+10s', '*/5 * * * * *') and prompt (the text to execute)."},
		{"duplicate name", addParams(obj{"name": "taken"}), `A job named "taken" already exists. Please use a different name or remove the existing job first.`},
		{"bad cron", addParams(obj{"type": "cron", "schedule": "* * *"}), `Invalid cron expression: Cron expression must have 6 fields (second minute hour dom month dow), got 3. Example: "0 * * * * *" for every minute`},
		{"huge relative", addParams(obj{"schedule": "+99999999999999d"}), "Invalid time value"},
		{"zero interval", addParams(obj{"type": "interval", "schedule": "0s"}), "Invalid interval format: 0s. Use format like '5m', '1h', '30s'"},
		{"unknown action", obj{"action": "nope"}, "Unknown action: nope"},
	}
	for _, c := range cases {
		out := callTool(t, d, c.params, makeCtx())
		if out.Text != "✗ Error: "+c.want || out.Details.Get("error") != c.want {
			t.Errorf("%s: %q", c.name, out.Text)
		}
	}
}

// The recursion guard is an exception, not a result: the call fails as a tool error. As in the original, it looks for a `custom` entry
// (not the `custom_message` entry that pi.sendMessage writes), so only an entry written with appendEntry trips it (PORT.md U2).
func TestRecursionGuard(t *testing.T) {
	d, _ := buildTool("session")
	entry := map[string]any{"type": "custom", "customType": "scheduled_prompt"}
	sess := fakeSession{id: "s", entries: append([]map[string]any{entry}, make([]map[string]any, 9)...)}
	_, err := d.execute(addParams(nil), sess)
	if err == nil || err.Error() != "Cannot create scheduled prompts from within a scheduled prompt execution. This prevents infinite loops." {
		t.Fatalf("err = %v", err)
	}
	// Only the last ten entries count.
	sess.entries = append([]map[string]any{entry}, make([]map[string]any, 10)...)
	if _, err := d.execute(addParams(nil), sess); err != nil {
		t.Fatalf("an old entry tripped the guard: %v", err)
	}
	// The message entry pi.sendMessage writes does not trip it, as in the original.
	sess.entries = []map[string]any{{"type": "custom_message", "customType": "scheduled_prompt"}}
	if _, err := d.execute(addParams(nil), sess); err != nil {
		t.Fatalf("custom_message tripped the guard: %v", err)
	}
	// list and the other actions are not guarded.
	sess.entries = []map[string]any{entry}
	empty, _ := buildTool("session")
	if out, err := empty.execute(obj{"action": "list"}, sess); err != nil || out.Text != "No cron jobs configured." {
		t.Fatalf("%v %v", out, err)
	}
}

func TestListText(t *testing.T) {
	job := toolJob(obj{"id": "a1", "name": "n1", "type": "cron", "schedule": "0 0 * * * *", "model": "haiku", "notify": true, "extensions": true, "skills": true, "lastRun": "2026-01-01T00:00:00.000Z", "lastStatus": "success", "runCount": float64(3), "description": "d"})
	d, _ := buildTool("session", job, toolJob(obj{"id": "b2", "name": "n2", "enabled": false}))
	out := callTool(t, d, obj{"action": "list"}, makeCtx())
	want := strings.Join([]string{
		"Configured cron jobs:",
		"",
		"✓ n1 (a1)",
		"  Type: cron | Schedule: 0 0 * * * *",
		"  Model: haiku (runs in subagent, notifies parent, extensions, skills)",
		"  Prompt: test",
		"  Last: 2026-01-01T00:00:00.000Z ",
		"  Runs: 3 | Status: success",
		"  Description: d",
		"",
		"✗ n2 (b2)",
		"  Type: once | Schedule: +10s",
		"  Prompt: test",
		"  Never run ",
		"  Runs: 0 | Status: pending",
		"",
	}, "\n")
	if out.Text != want {
		t.Fatalf("list text:\n%s\nwant:\n%s", out.Text, want)
	}
}
