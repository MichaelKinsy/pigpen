package schedule_prompt

import (
	"github.com/MichaelKinsy/pigpen/schedule-prompt/internal/croner"
	"github.com/MichaelKinsy/pigpen/schedule-prompt/internal/jsobj"
)

// The add flow of `/schedule-prompt` (upstream: ui/add-flow.ts runAddFlow): name, type, schedule (asked again on a validation
// failure), prompt, scope, confirm. It saves and schedules the new job, or returns silently on cancellation.

// flowUI is the part of ctx.ui the flow uses. A cancelled dialog answers ok false.
type flowUI interface {
	Input(title, placeholder string) (value string, ok bool, err error)
	Select(title string, options []string) (value string, ok bool, err error)
	Confirm(title, message string) (bool, error)
	Notify(message, level string)
}

const (
	scopeSession = "Bind to this session — only this pi fires it"
	scopeShared  = "Shared — every pi in this cwd fires it (accepts duplicate fires)"
)

var addFlowTypes = []struct{ label, typ string }{
	{"Cron (recurring)", "cron"},
	{"Once (one-shot)", "once"},
	{"Interval (periodic)", "interval"},
}

var addFlowPlaceholders = map[string]string{
	"cron":     "6-field cron, e.g. '0 0 9 * * *' for 9am daily",
	"once":     "ISO timestamp or relative time (+10s, +5m, +1h)",
	"interval": "Duration, e.g. '5m', '1h', '30s'",
}

func runAddFlow(ui flowUI, storage jobStore, sched jobScheduler, st settings, mySessionID string) error {
	// `if (!x) return`: a cancelled dialog and an empty answer both leave the flow.
	name, ok, err := ui.Input("Job Name", "Enter a name for this scheduled prompt")
	if err != nil || !ok || name == "" {
		return err
	}
	dup, err := storage.HasJobWithName(name)
	if err != nil {
		return err
	}
	if dup {
		ui.Notify(`A job named "`+name+`" already exists`, "error")
		return nil
	}

	labels := make([]string, len(addFlowTypes))
	for i, t := range addFlowTypes {
		labels[i] = t.label
	}
	typeChoice, ok, err := ui.Select("Job Type", labels)
	if err != nil || !ok || typeChoice == "" {
		return err
	}
	var jobType string
	for _, t := range addFlowTypes {
		if t.label == typeChoice {
			jobType = t.typ
		}
	}

	// The schedule is asked again on a validation failure so the user does not lose the name and type.
	var schedule string
	var intervalMs *float64
	placeholder := addFlowPlaceholders[jobType]
	for {
		raw, ok, err := ui.Input("Schedule", placeholder)
		if err != nil || !ok || raw == "" {
			return err
		}
		result, err := validateSchedule(jobType, croner.JSTrim(raw))
		if err != nil {
			return err
		}
		if result.ok {
			schedule, intervalMs = result.schedule, result.intervalMs
			break
		}
		placeholder = result.err
	}

	prompt, ok, err := ui.Input("Prompt", "Enter the prompt to execute")
	if err != nil || !ok || prompt == "" {
		return err
	}

	// The last decision before saving. The default option is listed first, so enter takes the configured default.
	scopes := []string{scopeShared, scopeSession}
	if st.defaultJobScope == "" || st.defaultJobScope == "session" {
		scopes = []string{scopeSession, scopeShared}
	}
	scopeChoice, ok, err := ui.Select("Scope", scopes)
	if err != nil || !ok || scopeChoice == "" {
		return err
	}
	var session any = jsobj.Undefined
	if scopeChoice == scopeSession {
		session = mySessionID
	}

	human := describeSchedule(jobType, schedule)
	scopeLabel := "shared (any pi here)"
	if truthy(session) {
		scopeLabel = "session-bound"
	}
	confirmed, err := ui.Confirm("Confirm", `Save "`+name+`"?`+"\nSchedule: "+human+"\nScope: "+scopeLabel+"\nPrompt: "+prompt)
	if err != nil || !confirmed {
		return err
	}

	job := jsobj.New()
	job.Set("id", nanoid(10))
	job.Set("name", name)
	job.Set("schedule", schedule)
	job.Set("prompt", prompt)
	job.Set("enabled", true)
	job.Set("type", jobType)
	if intervalMs != nil {
		job.Set("intervalMs", *intervalMs)
	} else {
		job.Set("intervalMs", jsobj.Undefined)
	}
	job.Set("createdAt", isoNow())
	job.Set("runCount", float64(0))
	job.Set("session", session)

	if err := storage.AddJob(job); err != nil {
		return err
	}
	sched.addJob(job)
	ui.Notify("Created scheduled prompt: "+name+" ("+human+")", "info")
	return nil
}
