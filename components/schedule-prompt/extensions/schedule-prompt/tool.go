package schedule_prompt

import (
	"errors"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/pigpen/schedule-prompt/internal/jsobj"
)

// The schedule_prompt tool (upstream: tool.ts createCronTool).

// toolSession is the part of the extension context the tool uses (ctx.sessionManager).
type toolSession interface {
	SessionID() string
	// Entries are the session entries (ctx.sessionManager.getEntries()), each a decoded JSON object.
	Entries() ([]map[string]any, error)
}

// jobScheduler is what the tool needs of the scheduler (the original's tests stand in an object with these four methods).
type jobScheduler interface {
	addJob(job *jsobj.Object)
	removeJob(id string)
	updateJob(id string, updated *jsobj.Object)
	getNextRun(id string) float64
}

type toolDeps struct {
	storage      func() jobStore
	scheduler    func() jobScheduler
	defaultScope func() string
}

type toolOutput struct {
	Text    string
	Details *jsobj.Object
}

func pOpt(params map[string]any, key string) (any, bool) {
	v, ok := params[key]
	if !ok || v == nil {
		return nil, false
	}
	return v, true
}

func pStr(params map[string]any, key string) string {
	v, _ := pOpt(params, key)
	if s, ok := v.(string); ok {
		return s
	}
	if v == nil {
		return ""
	}
	return jsStr(v)
}

func pTruthy(params map[string]any, key string) bool {
	v, ok := pOpt(params, key)
	return ok && truthy(v)
}

func newDetails(action string) *jsobj.Object {
	d := jsobj.New()
	d.Set("action", action)
	d.Set("jobs", []any{})
	return d
}

// recent is the last 10 session entries (`entries.slice(-10)`).
func recent(entries []map[string]any) []map[string]any {
	if len(entries) > 10 {
		return entries[len(entries)-10:]
	}
	return entries
}

func jobsAny(jobs []*jsobj.Object) []any {
	out := make([]any, len(jobs))
	for i, j := range jobs {
		out[i] = j
	}
	return out
}

// execute runs one call. The error return is an exception the original lets escape (a tool error); everything else it catches and
// returns as result text with `details.error`.
func (d toolDeps) execute(params map[string]any, sess toolSession) (toolOutput, error) {
	storage := d.storage()
	sched := d.scheduler()
	action := pStr(params, "action")

	// Prevent recursive scheduling from within scheduled prompts.
	if action == "add" {
		entries, err := sess.Entries()
		if err != nil {
			return toolOutput{}, err
		}
		for _, entry := range recent(entries) {
			if entry["type"] == "custom" && entry["customType"] == "scheduled_prompt" {
				return toolOutput{}, errors.New("Cannot create scheduled prompts from within a scheduled prompt execution. This prevents infinite loops.")
			}
		}
	}

	details := newDetails(action)
	out, err := d.run(action, params, sess, storage, sched, details)
	if err != nil {
		details.Set("error", err.Error())
		return toolOutput{Text: "✗ Error: " + err.Error(), Details: details}, nil
	}
	return out, nil
}

// modelDescription is "Model: m (runs in subagent, notifies parent, extensions, skills)", as the add result and the list show a job
// that has a model.
func modelDescription(job *jsobj.Object) string {
	notify, ext, skills := "", "", ""
	if truthy(job.Get("notify")) {
		notify = ", notifies parent"
	}
	if truthy(job.Get("extensions")) {
		ext = ", extensions"
	}
	if truthy(job.Get("skills")) {
		skills = ", skills"
	}
	return "Model: " + jsStr(job.Get("model")) + " (runs in subagent" + notify + ext + skills + ")"
}

func (d toolDeps) run(action string, params map[string]any, sess toolSession, storage jobStore, sched jobScheduler, details *jsobj.Object) (toolOutput, error) {
	switch action {
	case "add":
		return d.add(params, sess, storage, sched, details)
	case "remove":
		return d.remove(params, storage, sched, details)
	case "enable", "disable":
		return d.enable(action, params, storage, sched, details)
	case "cleanup":
		return d.cleanup(sess, storage, sched, details)
	case "update":
		return d.update(params, storage, sched, details)
	case "list":
		return d.list(sess, storage, sched, details)
	}
	return toolOutput{}, errors.New("Unknown action: " + action)
}

func (d toolDeps) add(params map[string]any, sess toolSession, storage jobStore, sched jobScheduler, details *jsobj.Object) (toolOutput, error) {
	if !pTruthy(params, "schedule") || !pTruthy(params, "prompt") {
		var missing []string
		if !pTruthy(params, "schedule") {
			missing = append(missing, "'schedule'")
		}
		if !pTruthy(params, "prompt") {
			missing = append(missing, "'prompt'")
		}
		return toolOutput{}, errors.New("Missing required parameters for add action: " + strings.Join(missing, " and ") +
			". You must provide both schedule (e.g., '+10s', '*/5 * * * * *') and prompt (the text to execute).")
	}
	// Defense in depth: the schema also enforces this with minLength: 1, but a caller can bypass the schema.
	if m, ok := pOpt(params, "model"); ok {
		if s, isStr := m.(string); isStr && s == "" {
			return toolOutput{}, errors.New("'model' must be a non-empty string. Omit the field for inline (no-model) jobs.")
		}
	}

	jobName := pStr(params, "name")
	if !pTruthy(params, "name") {
		jobName = "job-" + nanoid(6)
	}
	dup, err := storage.HasJobWithName(jobName)
	if err != nil {
		return toolOutput{}, err
	}
	if dup {
		return toolOutput{}, errors.New(`A job named "` + jobName + `" already exists. Please use a different name or remove the existing job first.`)
	}

	typ := "cron"
	if pTruthy(params, "type") {
		typ = pStr(params, "type")
	}
	v, err := validateSchedule(typ, pStr(params, "schedule"))
	if err != nil {
		return toolOutput{}, err
	}
	if !v.ok {
		return toolOutput{}, errors.New(v.err)
	}

	var session any = jsobj.Undefined
	if d.defaultScope() == "session" {
		session = sess.SessionID()
	}
	job := jsobj.New()
	job.Set("id", nanoid(10))
	job.Set("name", jobName)
	job.Set("schedule", v.schedule)
	job.Set("prompt", params["prompt"])
	job.Set("enabled", true)
	job.Set("type", typ)
	if v.intervalMs != nil {
		job.Set("intervalMs", *v.intervalMs)
	} else {
		job.Set("intervalMs", jsobj.Undefined)
	}
	job.Set("createdAt", isoNow())
	job.Set("runCount", float64(0))
	for _, key := range []string{"description", "model", "notify", "extensions", "skills"} {
		if p, ok := pOpt(params, key); ok {
			job.Set(key, normalizeParam(p))
		} else {
			job.Set(key, jsobj.Undefined)
		}
	}
	job.Set("session", session)

	if err := storage.AddJob(job); err != nil {
		return toolOutput{}, err
	}
	sched.addJob(job)
	details.Set("jobs", []any{job})
	details.Set("jobId", job.Get("id"))
	details.Set("jobName", job.Get("name"))

	text := `✓ Created cron job "` + jsStr(job.Get("name")) + `" (` + jsStr(job.Get("id")) + ")\nType: " + jsStr(job.Get("type")) +
		"\nSchedule: " + jsStr(job.Get("schedule")) + "\nPrompt: " + jsStr(job.Get("prompt"))
	if truthy(job.Get("model")) {
		text += "\n" + modelDescription(job)
	}
	return toolOutput{Text: text, Details: details}, nil
}

// normalizeParam turns a decoded JSON value into the stored form: arrays become []any of strings, objects ordered objects.
func normalizeParam(v any) any {
	switch t := v.(type) {
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = normalizeParam(e)
		}
		return out
	case []string:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = e
		}
		return out
	case map[string]any:
		o := jsobj.New()
		for k, e := range t {
			o.Set(k, normalizeParam(e))
		}
		return o
	case int:
		return float64(t)
	}
	return v
}

func (d toolDeps) remove(params map[string]any, storage jobStore, sched jobScheduler, details *jsobj.Object) (toolOutput, error) {
	if !pTruthy(params, "jobId") {
		return toolOutput{}, errors.New("jobId is required for remove action")
	}
	jobIDStr := pStr(params, "jobId")
	job, err := storage.GetJob(jobIDStr)
	if err != nil {
		return toolOutput{}, err
	}
	if job == nil {
		return toolOutput{}, errors.New("Job not found: " + jobIDStr)
	}
	removed, err := storage.RemoveJob(jobIDStr)
	if err != nil {
		return toolOutput{}, err
	}
	if removed {
		sched.removeJob(jobIDStr)
		details.Set("jobId", jobIDStr)
		details.Set("jobName", job.Get("name"))
		return toolOutput{Text: `✓ Removed cron job "` + jsStr(job.Get("name")) + `" (` + jobIDStr + ")", Details: details}, nil
	}
	return toolOutput{}, errors.New("Failed to remove job: " + jobIDStr)
}

func (d toolDeps) enable(action string, params map[string]any, storage jobStore, sched jobScheduler, details *jsobj.Object) (toolOutput, error) {
	if !pTruthy(params, "jobId") {
		return toolOutput{}, errors.New("jobId is required for " + action + " action")
	}
	id := pStr(params, "jobId")
	job, err := storage.GetJob(id)
	if err != nil {
		return toolOutput{}, err
	}
	if job == nil {
		return toolOutput{}, errors.New("Job not found: " + id)
	}
	enabled := action == "enable"
	partial := jsobj.New()
	partial.Set("enabled", enabled)
	if _, err := storage.UpdateJob(id, partial); err != nil {
		return toolOutput{}, err
	}
	updated := job.Clone()
	updated.Set("enabled", enabled)
	sched.updateJob(id, updated)
	details.Set("jobs", []any{updated})
	details.Set("jobId", id)
	details.Set("jobName", job.Get("name"))
	word := "Disabled"
	if enabled {
		word = "Enabled"
	}
	return toolOutput{Text: "✓ " + word + ` cron job "` + jsStr(job.Get("name")) + `" (` + id + ")", Details: details}, nil
}

func (d toolDeps) cleanup(sess toolSession, storage jobStore, sched jobScheduler, details *jsobj.Object) (toolOutput, error) {
	// Only jobs this session can see: foreign-session jobs belong to other pis. Unbound disabled jobs are fair game.
	mySessionID := sess.SessionID()
	all, err := storage.GetAllJobs()
	if err != nil {
		return toolOutput{}, err
	}
	var disabled []*jsobj.Object
	for _, j := range all {
		if !truthy(j.Get("enabled")) && isLoadedFor(j, mySessionID) {
			disabled = append(disabled, j)
		}
	}
	if len(disabled) == 0 {
		details.Set("jobs", []any{})
		return toolOutput{Text: "No disabled jobs to clean up", Details: details}, nil
	}
	for _, j := range disabled {
		if _, err := storage.RemoveJob(jobID(j)); err != nil {
			return toolOutput{}, err
		}
		sched.removeJob(jobID(j))
	}
	details.Set("jobs", jobsAny(disabled))
	lines := make([]string, len(disabled))
	for i, j := range disabled {
		lines[i] = "  - " + jsStr(j.Get("name")) + " (" + jsStr(j.Get("id")) + ")"
	}
	return toolOutput{Text: "✓ Removed " + strconv.Itoa(len(disabled)) + " disabled job(s):\n" + strings.Join(lines, "\n"), Details: details}, nil
}

func (d toolDeps) update(params map[string]any, storage jobStore, sched jobScheduler, details *jsobj.Object) (toolOutput, error) {
	if !pTruthy(params, "jobId") {
		return toolOutput{}, errors.New("jobId is required for update action")
	}
	id := pStr(params, "jobId")
	job, err := storage.GetJob(id)
	if err != nil {
		return toolOutput{}, err
	}
	if job == nil {
		return toolOutput{}, errors.New("Job not found: " + id)
	}
	// An empty model is rejected; to switch a job from subagent back to inline mode, remove and re-add it without a model.
	if m, ok := pOpt(params, "model"); ok {
		if s, isStr := m.(string); isStr && s == "" {
			return toolOutput{}, errors.New("'model' must be a non-empty string. To switch a job from subagent back to inline, remove and re-add it without a model.")
		}
	}

	updates := jsobj.New()
	if pTruthy(params, "name") {
		updates.Set("name", params["name"])
	}
	if pTruthy(params, "prompt") {
		updates.Set("prompt", params["prompt"])
	}
	for _, key := range []string{"description", "model", "notify", "extensions", "skills"} {
		if p, ok := pOpt(params, key); ok {
			updates.Set(key, normalizeParam(p))
		}
	}
	if pTruthy(params, "schedule") {
		// Same resolution as add: relative time becomes an ISO timestamp, an ISO timestamp is accepted as it is, a cron expression is
		// validated.
		v, err := validateSchedule(jsStr(job.Get("type")), pStr(params, "schedule"))
		if err != nil {
			return toolOutput{}, err
		}
		if !v.ok {
			return toolOutput{}, errors.New(v.err)
		}
		updates.Set("schedule", v.schedule)
		if v.intervalMs != nil {
			updates.Set("intervalMs", *v.intervalMs)
		}
	}
	if _, err := storage.UpdateJob(id, updates); err != nil {
		return toolOutput{}, err
	}
	updated := job.Clone()
	updated.Assign(updates)
	sched.updateJob(id, updated)
	details.Set("jobs", []any{updated})
	details.Set("jobId", id)
	details.Set("jobName", updated.Get("name"))
	return toolOutput{Text: `✓ Updated cron job "` + jsStr(updated.Get("name")) + `" (` + id + ")", Details: details}, nil
}

func (d toolDeps) list(sess toolSession, storage jobStore, sched jobScheduler, details *jsobj.Object) (toolOutput, error) {
	mySessionID := sess.SessionID()
	all, err := storage.GetAllJobs()
	if err != nil {
		return toolOutput{}, err
	}
	var jobs []*jsobj.Object
	for _, j := range all {
		if isLoadedFor(j, mySessionID) {
			jobs = append(jobs, j)
		}
	}
	details.Set("jobs", jobsAny(jobs))
	if len(jobs) == 0 {
		return toolOutput{Text: "No cron jobs configured.", Details: details}, nil
	}
	lines := []string{"Configured cron jobs:", ""}
	for _, job := range jobs {
		status := "✗"
		if truthy(job.Get("enabled")) {
			status = "✓"
		}
		nextStr := ""
		if next := sched.getNextRun(jobID(job)); next == next { // not NaN
			iso, ok := isoString(next)
			if !ok {
				return toolOutput{}, errors.New("Invalid time value")
			}
			nextStr = "Next: " + iso
		}
		lastStr := "Never run"
		if truthy(job.Get("lastRun")) {
			lastStr = "Last: " + jsStr(job.Get("lastRun"))
		}
		lines = append(lines, status+" "+jsStr(job.Get("name"))+" ("+jsStr(job.Get("id"))+")")
		lines = append(lines, "  Type: "+jsStr(job.Get("type"))+" | Schedule: "+jsStr(job.Get("schedule")))
		if truthy(job.Get("model")) {
			lines = append(lines, "  "+modelDescription(job))
		}
		lines = append(lines, "  Prompt: "+jsStr(job.Get("prompt")))
		tail := ""
		if nextStr != "" {
			tail = "| " + nextStr
		}
		lines = append(lines, "  "+lastStr+" "+tail)
		status2 := "pending"
		if truthy(job.Get("lastStatus")) {
			status2 = jsStr(job.Get("lastStatus"))
		}
		lines = append(lines, "  Runs: "+jsStr(job.Get("runCount"))+" | Status: "+status2)
		if truthy(job.Get("description")) {
			lines = append(lines, "  Description: "+jsStr(job.Get("description")))
		}
		lines = append(lines, "")
	}
	return toolOutput{Text: strings.Join(lines, "\n"), Details: details}, nil
}
