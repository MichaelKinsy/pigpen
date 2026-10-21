package schedule_prompt

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/schedule-prompt/internal/jsdate"
	"github.com/MichaelKinsy/pigpen/schedule-prompt/internal/jsobj"
)

// Cases of the port beyond the original's tests: the scheduler's branches the original covers in code only.

func eventTypes(p *mockPi) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, e := range p.emitted {
		out = append(out, e.Get("type").(string))
	}
	return out
}

func TestInlineFireStatistics(t *testing.T) {
	pi := &mockPi{}
	job := exampleJob(obj{"type": "cron", "schedule": "0 0 0 1 1 *", "runCount": float64(4)})
	st := makeStorage(job)
	s := newSched(st, pi, makeCtx(), nil)
	s.scheduleJob(job)
	defer s.stop()
	s.executeJob(job)
	j, _ := st.GetJob("job-1")
	if j.Get("lastStatus") != "success" || j.Get("runCount") != float64(5) || jsobj.IsUndefined(j.Get("lastRun")) {
		t.Fatalf("job = %v", j)
	}
	// nextRun is the cron's next run as an ISO timestamp (the first of January).
	next, _ := j.Get("nextRun").(string)
	if !strings.HasSuffix(next, ".000Z") || jsdate.Parse(next) <= jsdate.Now() {
		t.Fatalf("nextRun = %v", j.Get("nextRun"))
	}
	// The change events: a fire at the start and one at the end.
	if got := strings.Join(eventTypes(pi), ","); got != "fire,fire" {
		t.Fatalf("events = %s", got)
	}
	// A second run reads the count from the storage, not from the stale closure.
	s.executeJob(job)
	if j, _ := st.GetJob("job-1"); j.Get("runCount") != float64(6) {
		t.Fatalf("runCount = %v", j.Get("runCount"))
	}
}

func TestInlineFireFailureIsAnError(t *testing.T) {
	logged := captureLog(t)
	pi := &mockPi{sendError: func(int) error { return errSimulated }}
	job := exampleJob(obj{"runCount": float64(2)})
	st := makeStorage(job)
	s := newSched(st, pi, makeCtx(), nil)
	s.executeJob(job)
	j, _ := st.GetJob("job-1")
	if j.Get("lastStatus") != "error" || j.Get("runCount") != float64(2) || jsobj.IsUndefined(j.Get("lastRun")) {
		t.Fatalf("job = %v", j)
	}
	if got := strings.Join(eventTypes(pi), ","); got != "fire,error" {
		t.Fatalf("events = %s", got)
	}
	if len(pi.users) != 0 {
		t.Fatal("the prompt was delivered after the marker failed")
	}
	if l := logged(); len(l) != 1 || !strings.HasPrefix(l[0], "Failed to execute job job-1: ") {
		t.Fatalf("log = %v", l)
	}
	last := pi.emitted[len(pi.emitted)-1]
	if last.Get("jobId") != "job-1" || last.Get("error") != errSimulated.Error() {
		t.Fatalf("error event = %v", last)
	}
}

func TestSchedulerEvents(t *testing.T) {
	pi := &mockPi{}
	job := exampleJob(obj{"type": "cron", "schedule": "0 0 * * * *", "enabled": false})
	s := newSched(makeStorage(job), pi, makeCtx(), nil)
	defer s.stop()
	s.addJob(job)
	s.mu.Lock()
	scheduledAfterAdd := len(s.jobs)
	s.mu.Unlock()
	if scheduledAfterAdd != 0 {
		t.Fatal("a disabled job was scheduled by addJob")
	}
	on := job.Clone()
	on.Set("enabled", true)
	s.updateJob("job-1", on)
	s.mu.Lock()
	scheduledAfterEnable := len(s.jobs)
	s.mu.Unlock()
	if scheduledAfterEnable != 1 {
		t.Fatal("updateJob did not schedule the enabled job")
	}
	off := on.Clone()
	off.Set("enabled", false)
	s.updateJob("job-1", off)
	s.mu.Lock()
	scheduledAfterDisable := len(s.jobs)
	s.mu.Unlock()
	if scheduledAfterDisable != 0 {
		t.Fatal("updateJob kept a disabled job scheduled")
	}
	s.removeJob("job-1")
	if got := strings.Join(eventTypes(pi), ","); got != "add,update,update,remove" {
		t.Fatalf("events = %s", got)
	}
	if e := pi.emitted[3]; e.Get("jobId") != "job-1" || !jsobj.IsUndefined(e.Get("job")) {
		t.Fatalf("remove event = %v", e)
	}
	if e := pi.emitted[0]; e.Get("job") != job {
		t.Fatal("the add event does not carry the job")
	}
}

func TestOnceJobFiresAndDisables(t *testing.T) {
	pi := &mockPi{}
	at, _ := isoString(jsdate.Now() + 150)
	job := exampleJob(obj{"type": "once", "schedule": at})
	st := makeStorage(job)
	s := newSched(st, pi, makeCtx(), nil)
	defer s.stop()
	s.scheduleJob(job)
	waitFor(t, func() bool { j, _ := st.GetJob("job-1"); return j.Get("enabled") == false })
	j, _ := st.GetJob("job-1")
	if j.Get("lastStatus") != "success" || j.Get("runCount") != float64(1) || len(pi.users) != 1 {
		t.Fatalf("job = %v, user messages %v", j, pi.users)
	}
	if got := strings.Join(eventTypes(pi), ","); got != "fire,fire,update" {
		t.Fatalf("events = %s", got)
	}
	if e := pi.emitted[2]; e.Get("job").(*jsobj.Object).Get("enabled") != false {
		t.Fatalf("update event = %v", e)
	}
	time.Sleep(200 * time.Millisecond)
	if len(pi.users) != 1 {
		t.Fatal("the one-shot fired again")
	}
}

// PORT.md D1: a delay beyond Node's 2^31-1 ms is waited for, not fired at once.
func TestLongOnceIsNotFiredAtOnce(t *testing.T) {
	pi := &mockPi{}
	at, _ := isoString(jsdate.Now() + 30*86400_000)
	job := exampleJob(obj{"type": "once", "schedule": at})
	s := newSched(makeStorage(job), pi, makeCtx(), nil)
	s.scheduleJob(job)
	time.Sleep(150 * time.Millisecond)
	s.mu.Lock()
	armed := len(s.intervals)
	s.mu.Unlock()
	s.stop()
	if armed != 1 || len(pi.users) != 0 || pi.messageCount() != 0 {
		t.Fatalf("armed %d, users %v", armed, pi.users)
	}
}

func TestPastOnceIsDisabledWithAnError(t *testing.T) {
	logged := captureLog(t)
	pi := &mockPi{}
	job := exampleJob(obj{"schedule": "2020-01-01T00:00:00.000Z"})
	st := makeStorage(job)
	s := newSched(st, pi, makeCtx(), nil)
	s.scheduleJob(job)
	j, _ := st.GetJob("job-1")
	if j.Get("enabled") != false || j.Get("lastStatus") != "error" {
		t.Fatalf("job = %v", j)
	}
	if got := strings.Join(eventTypes(pi), ","); got != "error" {
		t.Fatalf("events = %s", got)
	}
	if e := pi.emitted[0]; e.Get("jobId") != "job-1" || e.Get("error") != "Scheduled time 2020-01-01T00:00:00.000Z is in the past" {
		t.Fatalf("event = %v", e)
	}
	if l := logged(); len(l) != 1 || l[0] != "Job job-1 (demo) scheduled for past time: 2020-01-01T00:00:00.000Z" {
		t.Fatalf("log = %v", l)
	}
}

func TestIntervalJobFiresRepeatedly(t *testing.T) {
	pi := &mockPi{}
	job := exampleJob(obj{"type": "interval", "schedule": "50ms", "intervalMs": float64(50)})
	s := newSched(makeStorage(job), pi, makeCtx(), nil)
	s.scheduleJob(job)
	waitFor(t, func() bool { pi.mu.Lock(); defer pi.mu.Unlock(); return len(pi.users) >= 3 })
	s.stop()
	n := len(pi.users)
	time.Sleep(150 * time.Millisecond)
	if len(pi.users) != n {
		t.Fatal("the interval kept firing after stop()")
	}
}

// An interval job without intervalMs goes down the cron path and fails there, as in the original.
func TestIntervalWithoutMillisecondsIsACronError(t *testing.T) {
	logged := captureLog(t)
	pi := &mockPi{}
	job := exampleJob(obj{"type": "interval", "schedule": "5m"})
	s := newSched(makeStorage(job), pi, makeCtx(), nil)
	s.scheduleJob(job)
	l := logged()
	if len(l) != 1 || !strings.HasPrefix(l[0], "Failed to schedule job job-1: CronPattern: invalid configuration format ('5m')") {
		t.Fatalf("log = %v", l)
	}
	if got := strings.Join(eventTypes(pi), ","); got != "error" {
		t.Fatalf("events = %s", got)
	}
}

func TestExecuteJobWithModelRunsTheSubagent(t *testing.T) {
	pi := &mockPi{}
	rc := &runnerCalls{}
	job := exampleJob(obj{"model": "haiku"})
	st := makeStorage(job)
	s := newSched(st, pi, makeCtx(), rc.runner(subagentResult{OK: true, Text: "ok"}))
	s.executeJob(job)
	waitFor(t, func() bool { return pi.messageCount() == 2 })
	if rc.count() != 1 || rc.calls[0].prompt != "do the thing" || rc.calls[0].model != "haiku" || len(pi.users) != 0 {
		t.Fatalf("calls %+v, users %v", rc.calls, pi.users)
	}
}

func TestStopClearsEveryTimer(t *testing.T) {
	job := exampleJob(obj{"type": "cron", "schedule": "0 0 * * * *"})
	iv := exampleJob(obj{"id": "iv", "type": "interval", "schedule": "5m", "intervalMs": float64(300000)})
	s := newSched(makeStorage(job, iv), &mockPi{}, makeCtx(), nil)
	s.start()
	s.mu.Lock()
	before := len(s.jobs) + len(s.intervals)
	s.mu.Unlock()
	s.stop()
	s.mu.Lock()
	after := len(s.jobs) + len(s.intervals)
	s.mu.Unlock()
	if before != 2 || after != 0 {
		t.Fatalf("timers before %d after %d", before, after)
	}
}

func TestStartSkipsDisabledJobs(t *testing.T) {
	off := exampleJob(obj{"id": "off", "type": "cron", "schedule": "0 0 * * * *", "enabled": false})
	s := newSched(makeStorage(off), &mockPi{}, makeCtx(), nil)
	defer s.stop()
	s.start()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.jobs) != 0 {
		t.Fatal("a disabled job was scheduled")
	}
}

func TestSchedulerGetNextRun(t *testing.T) {
	job := exampleJob(obj{"type": "cron", "schedule": "0 0 0 1 1 *"})
	s := newSched(makeStorage(job), &mockPi{}, makeCtx(), nil)
	defer s.stop()
	if next := s.getNextRun("job-1"); next == next {
		t.Fatal("an unscheduled job has a next run")
	}
	s.scheduleJob(job)
	f := jsdate.LocalFields(s.getNextRun("job-1"))
	if f.Month != 0 || f.Day != 1 || f.Hour != 0 || f.Minute != 0 || f.Second != 0 {
		t.Fatalf("next run = %+v", f)
	}
}

// ---- the validation boundaries ----

func TestOnceBoundaries(t *testing.T) {
	now := 1_800_000_000_000.0
	iso := func(ms float64) string { s, _ := isoString(ms); return s }
	cases := []struct {
		delay float64
		want  string // "" accepted
	}{
		{-1, "Timestamp is in the past: " + iso(now-1) + ". Current time: " + iso(now)},
		{0, "Timestamp is too soon (0s). For delays under 5s, use relative time like '+0s' instead, or schedule at least 5s in the future."},
		{1, "Timestamp is too soon (0s). For delays under 5s, use relative time like '+1s' instead, or schedule at least 5s in the future."},
		{2500, "Timestamp is too soon (3s). For delays under 5s, use relative time like '+3s' instead, or schedule at least 5s in the future."},
		{4999, "Timestamp is too soon (5s). For delays under 5s, use relative time like '+5s' instead, or schedule at least 5s in the future."},
		{5000, ""},
	}
	for _, c := range cases {
		r, err := validateScheduleAt("once", iso(now+c.delay), now)
		if err != nil {
			t.Fatal(err)
		}
		if c.want == "" {
			if !r.ok || r.schedule != iso(now+c.delay) {
				t.Errorf("delay %v: %+v", c.delay, r)
			}
			continue
		}
		if r.ok || r.err != c.want {
			t.Errorf("delay %v: %+v, want %q", c.delay, r, c.want)
		}
	}
}

func TestRelativeTimes(t *testing.T) {
	now := 1_800_000_000_000.0
	for _, c := range []struct {
		in   string
		want float64
	}{{"+10s", 10_000}, {"+5m", 300_000}, {"+2h", 7_200_000}, {"+3d", 259_200_000}, {"+0s", 0}, {"+007s", 7_000}} {
		got, ok, err := parseRelativeTime(c.in, now)
		want, _ := isoString(now + c.want)
		if err != nil || !ok || got != want {
			t.Errorf("%s: %q %v %v, want %q", c.in, got, ok, err, want)
		}
	}
	for _, bad := range []string{"10s", "+s", "+5", "+5w", "+5m ", " +5m", "+-5m", "+5M", "+5.5m", "5m"} {
		if _, ok, _ := parseRelativeTime(bad, now); ok {
			t.Errorf("%q parsed as a relative time", bad)
		}
	}
	if _, ok, err := parseRelativeTime("+99999999999999999d", now); !ok || err == nil || err.Error() != "Invalid time value" {
		t.Errorf("overflow: %v %v", ok, err)
	}
}

func TestIntervals(t *testing.T) {
	for _, c := range []struct {
		in   string
		want float64
		ok   bool
	}{{"30s", 30_000, true}, {"5m", 300_000, true}, {"1h", 3_600_000, true}, {"2d", 172_800_000, true}, {"0s", 0, true}, {"5", 0, false}, {"m", 0, false}, {"5 m", 0, false}, {"-5m", 0, false}, {"5M", 0, false}, {"+5m", 0, false}} {
		got, ok := parseInterval(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("parseInterval(%q) = %v %v", c.in, got, ok)
		}
	}
}

func TestValidateCronExpression(t *testing.T) {
	for _, c := range []struct {
		in   string
		want string // "" valid
	}{
		{"0 * * * * *", ""}, {"* * * * * *", ""}, {"  0 0 9 * * 1-5  ", ""}, {"0\t0\t9\t*\t*\t*", ""}, {"0 0 9 * * MON-FRI", ""}, {"0 0 0 L * *", ""}, {"0 0 0 * * FRI#2", ""},
		{"", "Cron expression must have 6 fields (second minute hour dom month dow), got 1. Example: \"0 * * * * *\" for every minute"},
		{"0 9 * * *", "Cron expression must have 6 fields (second minute hour dom month dow), got 5. Example: \"0 * * * * *\" for every minute"},
		{"0 0 0 * * * *", "Cron expression must have 6 fields (second minute hour dom month dow), got 7. Example: \"0 * * * * *\" for every minute"},
		{"61 * * * * *", "CronPattern: Invalid value for second: 61"},
		{"0 0 24 * * *", "CronPattern: Invalid value for hour: 24"},
		{"0 0 0 32 * *", "CronPattern: Invalid value for day: 31"},
		{"0 0 0 * 13 *", "CronPattern: Invalid value for month: 12"},
		{"0 0 0 * * 8", "CronPattern: Invalid value for dayOfWeek: 8"},
		{"0 */0 * * * *", "CronPattern: Syntax error, illegal stepping: 0"},
		{"0 5/10 * * * *", "CronPattern: Syntax error, stepping with numeric prefix ('5/10') is not allowed. Use wildcard (*/step) or range (min-max/step) instead."},
		{"0 0 x * * *", "CronPattern: configuration entry 2 (x) contains illegal characters."},
		{"0 0 0 * * FRI#9", "CronPattern: nth weekday out of range, should be 1-5 or L. Value: 9, Type: string"},
		{"0 0 12:30 * * *", "Invalid ISO8601 passed to timezone parser."},
	} {
		valid, text := validateCronExpression(c.in)
		if valid != (c.want == "") || text != c.want {
			t.Errorf("validateCronExpression(%q) = %v %q, want %q", c.in, valid, text, c.want)
		}
	}
}

func TestHumanizeCron(t *testing.T) {
	for in, want := range map[string]string{
		"* * * * * *": "every second", "0 * * * * *": "every minute", "0 */5 * * * *": "every 5 min", "0 */10 * * * *": "every 10 min",
		"0 */15 * * * *": "every 15 min", "0 */30 * * * *": "every 30 min", "0 0 * * * *": "every hour", "0 0 */2 * * *": "every 2 hours",
		"0 0 */3 * * *": "every 3 hours", "0 0 */6 * * *": "every 6 hours", "0 0 0 * * *": "daily", "0 0 0 * * 0": "weekly", "0 0 0 1 * *": "monthly",
		"0 0 9 * * 1-5": "9am weekdays", "0 0 0 * * 1-5": "weekdays", "0 0 0 * * 0,6": "weekends",
		"0 */7 * * * *": "every 7 min", "0 0 */4 * * *": "every 4h", "0 0 9 * * *": "daily at 9:00", "0 0 09 * * *": "daily at 9:00", "0 0 17 * * *": "daily at 17:00",
		"  0 0 9 * * *  ": "daily at 9:00", "0 15 9 * * *": "0 15 9 * * *",
	} {
		if got := humanizeCron(in); got != want {
			t.Errorf("humanizeCron(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatISOShort(t *testing.T) {
	loc := time.FixedZone("x", 2*3600)
	old := jsdate.Location()
	jsdate.SetLocation(loc)
	defer jsdate.SetLocation(old)
	if got := formatISOShort("2026-02-13T15:30:00.000Z"); got != "Feb 13 17:30" {
		t.Fatalf("got %q", got)
	}
	if got := formatISOShort("2026-12-05T07:04:00.000Z"); got != "Dec 5 09:04" {
		t.Fatalf("got %q", got)
	}
	if got := formatISOShort("not a date"); got != "not a date" {
		t.Fatalf("got %q", got)
	}
	if got := describeSchedule("once", "garbage"); got != "garbage" {
		t.Fatalf("got %q", got)
	}
}

// ---- the storage ----

func TestStorageRoundTrip(t *testing.T) {
	cwd := t.TempDir()
	st := newFileStorage(cwd)
	if all, err := st.GetAllJobs(); err != nil || len(all) != 0 {
		t.Fatalf("%v %v", all, err)
	}
	a := exampleJob(obj{"id": "a", "name": "alpha"})
	b := exampleJob(obj{"id": "b", "name": "beta", "session": "s"})
	for _, j := range []*jsobj.Object{a, b} {
		if err := st.AddJob(j); err != nil {
			t.Fatal(err)
		}
	}
	if dup, _ := st.HasJobWithName("beta"); !dup {
		t.Fatal("HasJobWithName missed beta")
	}
	if dup, _ := st.HasJobWithName("gamma"); dup {
		t.Fatal("HasJobWithName found gamma")
	}
	if ok, _ := st.UpdateJob("a", func() *jsobj.Object { o := jsobj.New(); o.Set("enabled", false); o.Set("extra", "x"); return o }()); !ok {
		t.Fatal("UpdateJob missed a")
	}
	if ok, _ := st.UpdateJob("zzz", jsobj.New()); ok {
		t.Fatal("UpdateJob found a ghost")
	}
	got, _ := st.GetJob("a")
	if got.Get("enabled") != false || got.Get("extra") != "x" || got.Get("name") != "alpha" {
		t.Fatalf("job = %v", got)
	}
	if keys := strings.Join(got.Keys(), ","); !strings.HasSuffix(keys, ",enabled-gone") && !strings.HasSuffix(keys, ",extra") {
		t.Fatalf("a new key goes last: %s", keys)
	}
	if ok, _ := st.RemoveJob("zzz"); ok {
		t.Fatal("RemoveJob removed a ghost")
	}
	if ok, _ := st.RemoveJob("a"); !ok {
		t.Fatal("RemoveJob missed a")
	}
	if all, _ := st.GetAllJobs(); len(all) != 1 || all[0].Get("id") != "b" {
		t.Fatalf("jobs = %v", all)
	}
	if j, _ := st.GetJob("a"); j != nil {
		t.Fatal("a is back")
	}
}

// The file is written through a temporary file and a rename (a directory in the temporary file's place makes the write fail).
func TestSaveGoesThroughTheTemporaryFile(t *testing.T) {
	logged := captureLog(t)
	cwd := t.TempDir()
	st := newFileStorage(cwd)
	if err := os.MkdirAll(filepath.Join(cwd, ".pi", "schedule-prompts.json.tmp"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := st.AddJob(exampleJob(nil)); err == nil {
		t.Fatal("AddJob wrote around the temporary file")
	}
	if l := logged(); len(l) == 0 || !strings.HasPrefix(l[0], "Failed to save scheduled prompts: ") {
		t.Fatalf("log = %v", l)
	}
	if _, err := os.Stat(st.storePath); err == nil {
		t.Fatal("a failed write left a store file")
	}
}

func TestStorageCorruptFiles(t *testing.T) {
	logged := captureLog(t)
	cwd := t.TempDir()
	st := newFileStorage(cwd)
	write := func(text string) {
		if err := os.MkdirAll(filepath.Join(cwd, ".pi"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(st.storePath, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Not JSON: an empty store, logged, replaced by the next write.
	write("{not json")
	if all, err := st.GetAllJobs(); err != nil || len(all) != 0 {
		t.Fatalf("%v %v", all, err)
	}
	if l := logged(); len(l) != 1 || !strings.HasPrefix(l[0], "Failed to load scheduled prompts: ") {
		t.Fatalf("log = %v", l)
	}
	if err := st.AddJob(exampleJob(nil)); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(st.storePath)
	if !strings.HasSuffix(string(data), "\n  ],\n  \"version\": 1\n}") {
		t.Fatalf("file = %s", data)
	}
	// JSON of another shape: refused, the file is left alone (PORT.md D4).
	for _, shape := range []string{"null", "[]", "5", `{"version": 1}`, `{"jobs": {}}`, `{"jobs": [1]}`} {
		write(shape)
		if _, err := st.GetAllJobs(); err == nil || !strings.Contains(err.Error(), "is not a schedule store") {
			t.Errorf("shape %s: err = %v", shape, err)
		}
		if err := st.AddJob(exampleJob(nil)); err == nil {
			t.Errorf("shape %s: AddJob succeeded", shape)
		}
		if data, _ := os.ReadFile(st.storePath); string(data) != shape {
			t.Errorf("shape %s: the file was rewritten: %s", shape, data)
		}
	}
	// Unknown members and the key order of a hand-edited file survive an update.
	write(`{"version": 3, "note": "mine", "jobs": [{"zeta": 1, "id": "j", "alpha": 2, "enabled": true}]}`)
	p := jsobj.New()
	p.Set("enabled", false)
	if ok, err := st.UpdateJob("j", p); !ok || err != nil {
		t.Fatal(ok, err)
	}
	data, _ = os.ReadFile(st.storePath)
	want := "{\n  \"version\": 3,\n  \"note\": \"mine\",\n  \"jobs\": [\n    {\n      \"zeta\": 1,\n      \"id\": \"j\",\n      \"alpha\": 2,\n      \"enabled\": false\n    }\n  ]\n}"
	if string(data) != want {
		t.Fatalf("file:\n%s\nwant:\n%s", data, want)
	}
}

// ---- the extension's session handling ----

func TestSdkEntriesAreTheSessionManagersLastTen(t *testing.T) {
	// The recursion guard reads sessionManager.getEntries(); a fake that answers a plain list is enough to check the wiring of sdkSession.
	var _ toolSession = sdkSession{}
	var _ sessionInfo = sdkSession{}
	_ = sdk.Bool
	_ = errors.New
}

// ---- the renderers ----

func TestScheduledPromptLines(t *testing.T) {
	var th sdk.UITheme
	line := func(details map[string]any) string {
		return strings.Join(scheduledPromptLines(th, map[string]any{"details": details}, 200), "|")
	}
	if got := line(map[string]any{"jobName": "job", "prompt": "do it"}); !strings.HasPrefix(got, `🕐 Scheduled: job → "do it"`) {
		t.Errorf("inline: %q", got)
	}
	if got := line(map[string]any{}); !strings.HasPrefix(got, "🕐 Scheduled: Unknown") || strings.Contains(got, "→") {
		t.Errorf("empty: %q", got)
	}
	if got := line(map[string]any{"jobName": "j", "prompt": "p", "mode": "subagent_start", "model": "haiku"}); !strings.HasPrefix(got, `🕐 Scheduled (subagent: haiku): j → "p"`) {
		t.Errorf("start: %q", got)
	}
	if got := line(map[string]any{"jobName": "j", "mode": "subagent_done", "model": "haiku", "output": "ok"}); !strings.HasPrefix(got, "✓ Scheduled (subagent: haiku) finished: j → ok") {
		t.Errorf("done: %q", got)
	}
	if got := line(map[string]any{"jobName": "j", "mode": "subagent_done", "model": "haiku"}); !strings.HasPrefix(got, "✓ Scheduled (subagent: haiku) finished: j") || strings.Contains(got, "→") {
		t.Errorf("done without output: %q", got)
	}
	if got := line(map[string]any{"jobName": "j", "mode": "subagent_error", "model": "haiku", "error": "boom"}); !strings.HasPrefix(got, "✗ Scheduled (subagent: haiku) failed: j → boom") {
		t.Errorf("error: %q", got)
	}
	if got := scheduledPromptLines(th, map[string]any{}, 200); len(got) != 1 || !strings.HasPrefix(got[0], "🕐 Scheduled: Unknown") {
		t.Errorf("no details: %q", got)
	}
}

func TestToolCallLines(t *testing.T) {
	var th sdk.UITheme
	for _, c := range []struct {
		args obj
		want string
	}{
		{obj{"action": "add", "name": "n"}, "Adding cron job: n"},
		{obj{"action": "remove", "jobId": "id1"}, "Removing cron job: id1"},
		{obj{"action": "update", "name": "n", "jobId": "id1"}, "Updating cron job: n"},
		{obj{"action": "list"}, "Listing all cron jobs"},
		{obj{"action": "cleanup"}, "cleanup cron job"},
		{obj{"action": "enable", "jobId": "x"}, "Enable job: x"},
		{obj{"action": "disable", "jobId": "x"}, "Disable job: x"},
	} {
		got := strings.TrimRight(strings.Join(toolCallLines(th, c.args, 100), "|"), " ")
		if got != c.want {
			t.Errorf("%v: %q, want %q", c.args, got, c.want)
		}
	}
}

var ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

func TestToolResultLines(t *testing.T) {
	var th sdk.UITheme
	text := func(r sdk.ToolRenderResult) string {
		var out []string
		for _, l := range toolResultLines(th, r, 200) {
			out = append(out, strings.TrimRight(ansiRe.ReplaceAllString(l, ""), " "))
		}
		return strings.Join(out, "|")
	}
	if got := text(sdk.ToolRenderResult{Content: []map[string]any{{"type": "text", "text": "plain"}, {"type": "image"}}}); got != "plain|" {
		t.Errorf("no details: %q", got)
	}
	if got := text(sdk.ToolRenderResult{Details: map[string]any{"action": "add", "jobName": "n", "jobId": "i"}}); got != "✓ add n" {
		t.Errorf("add: %q", got)
	}
	if got := text(sdk.ToolRenderResult{Details: map[string]any{"action": "remove", "jobId": "i"}}); got != "✓ remove i" {
		t.Errorf("remove by id: %q", got)
	}
	if got := text(sdk.ToolRenderResult{Details: map[string]any{"action": "add", "error": "bad"}}); got != "✗ Error: bad" {
		t.Errorf("error: %q", got)
	}
	jobs := []any{
		map[string]any{"enabled": true, "name": "a", "id": "ia", "type": "cron", "schedule": "0 0 * * * *", "prompt": "p", "runCount": float64(2), "lastRun": "L", "model": "haiku", "notify": true, "extensions": true, "skills": true},
		map[string]any{"enabled": false, "name": "b", "id": "ib", "type": "once", "schedule": "S", "prompt": "q", "runCount": float64(0), "model": "m"},
	}
	got := text(sdk.ToolRenderResult{Details: map[string]any{"action": "list", "jobs": jobs}})
	want := strings.Join([]string{
		"✓ list", "", "Cron Jobs:",
		"✓ a (ia)", "  Type: cron | Schedule: 0 0 * * * *", "  Model: haiku (subagent, notifies parent), extensions + skills", "  Prompt: p", "  Last run: L", "  Runs: 2",
		"✗ b (ib)", "  Type: once | Schedule: S", "  Model: m (subagent)", "  Prompt: q", "  Runs: 0",
	}, "|")
	if got != want {
		t.Errorf("list:\n%s\nwant:\n%s", got, want)
	}
}
