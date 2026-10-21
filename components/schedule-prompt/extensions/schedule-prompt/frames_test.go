package schedule_prompt

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/schedule-prompt/internal/jsdate"
	"github.com/MichaelKinsy/pigpen/schedule-prompt/internal/jsobj"
)

// Replays port/frames/scenarios (recorded from the unmodified original's JobsView and runAddFlow under Pi 1402973a9d, see
// port/frames/recorder/record.ts) against the port's jobsView and runAddFlow and compares every frame, store file and call.

type tagTheme struct{}

func (tagTheme) Fg(token, text string) string { return "<fg:" + token + ">" + text + "</fg>" }
func (tagTheme) Bold(text string) string      { return "<b>" + text + "</b>" }

type frameSchedule struct {
	mu    sync.Mutex
	next  map[string]string
	calls []any
}

func (s *frameSchedule) addJob(*jsobj.Object) {}
func (s *frameSchedule) removeJob(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, map[string]any{"op": "removeJob", "id": id})
}
func (s *frameSchedule) updateJob(id string, job *jsobj.Object) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, _ := json.Marshal(job)
	s.calls = append(s.calls, map[string]any{"op": "updateJob", "id": id, "job": string(b)})
}
func (s *frameSchedule) getNextRun(id string) float64 {
	if iso, ok := s.next[id]; ok {
		return jsdate.Parse(iso)
	}
	return nan()
}

func nan() float64 { return math.NaN() }

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return m
}

// seedStore writes {jobs, version: 1} the way JSON.stringify(..., null, 2) does, keeping the scenario's key order.
func seedStore(t *testing.T, cwd, scenarioFile string) {
	t.Helper()
	text, err := os.ReadFile(scenarioFile)
	if err != nil {
		t.Fatal(err)
	}
	v, err := jsobj.Parse(string(text))
	if err != nil {
		t.Fatal(err)
	}
	jobs, _ := v.(*jsobj.Object).Get("jobs").([]any)
	if len(jobs) == 0 {
		return
	}
	st := jsobj.New()
	st.Set("jobs", jobs)
	st.Set("version", float64(1))
	out, err := jsobj.Stringify(st)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cwd, ".pi"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".pi", "schedule-prompts.json"), []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
}

func storeFileText(cwd string) any {
	b, err := os.ReadFile(filepath.Join(cwd, ".pi", "schedule-prompts.json"))
	if err != nil {
		return nil
	}
	return string(b)
}

// toPlain turns a Go value into what encoding/json gives back, so it compares with the golden's decoded form.
func toPlain(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestFramesAgainstOriginal(t *testing.T) {
	jsdate.SetLocation(time.UTC)
	defer jsdate.SetLocation(time.Local)
	files, _ := filepath.Glob("testdata/frames/scenarios/*.json")
	if len(files) == 0 {
		t.Fatal("no frame scenarios")
	}
	for _, file := range files {
		name := filepath.Base(file)
		golden := readJSON(t, filepath.Join("testdata/frames/golden", name))
		sc := readJSON(t, file)
		t.Run(name[:len(name)-5], func(t *testing.T) {
			cwd := t.TempDir()
			seedStore(t, cwd, file)
			var got map[string]any
			if sc["kind"] == "jobs" {
				got = replayJobs(t, sc, cwd, file)
			} else {
				got = replayFlow(t, sc, cwd)
			}
			for k, want := range golden {
				if k == "recordedWith" || k == "scenario" {
					continue
				}
				if g := toPlain(t, got[k]); !reflect.DeepEqual(g, want) {
					t.Errorf("%s differs:\n%s", k, firstDifference(g, want, k))
				}
			}
		})
	}
}

func firstDifference(got, want any, path string) string {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return fmt.Sprintf("%s: got %v, want an object", path, got)
		}
		for k, wv := range w {
			if d := firstDifference(g[k], wv, path+"."+k); d != "" {
				return d
			}
		}
		for k := range g {
			if _, ok := w[k]; !ok {
				return fmt.Sprintf("%s.%s: the port has it, the original does not: %v", path, k, g[k])
			}
		}
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return fmt.Sprintf("%s: got %d entries (%v), want %d (%v)", path, len(g), got, len(w), want)
		}
		for i := range w {
			if d := firstDifference(g[i], w[i], fmt.Sprintf("%s[%d]", path, i)); d != "" {
				return d
			}
		}
	default:
		if !reflect.DeepEqual(got, want) {
			return fmt.Sprintf("%s:\n   got  %#v\n   want %#v", path, got, want)
		}
	}
	return ""
}

func replayJobs(t *testing.T, sc map[string]any, cwd, file string) map[string]any {
	storage := newFileStorage(cwd)
	next := map[string]string{}
	if m, ok := sc["nextRuns"].(map[string]any); ok {
		for k, v := range m {
			next[k] = v.(string)
		}
	}
	sched := &frameSchedule{next: next}
	var mu sync.Mutex
	addRuns, renders := 0, 0
	runAdd := func() error {
		mu.Lock()
		addRuns++
		mu.Unlock()
		switch sc["runAdd"] {
		case "fail":
			return errors.New("add flow failed")
		case "adds":
			text, _ := os.ReadFile(file) // the job in the scenario's own key order
			whole, err := jsobj.Parse(string(text))
			if err != nil {
				t.Fatal(err)
			}
			return storage.AddJob(whole.(*jsobj.Object).Get("addJob").(*jsobj.Object))
		}
		return nil
	}
	defer captureLog(t)()
	view := newJobsView(storage, sched, sc["session"].(string), runAdd, tagTheme{})
	view.SetInvalidate(func() { mu.Lock(); renders++; mu.Unlock() })
	done := false
	snap := func(input any) map[string]any {
		frames := map[string]any{}
		for _, w := range sc["widths"].([]any) {
			frames[fmt.Sprint(int(w.(float64)))] = view.Render(int(w.(float64)))
		}
		sched.mu.Lock()
		calls := append([]any{}, sched.calls...)
		sched.mu.Unlock()
		mu.Lock()
		defer mu.Unlock()
		return map[string]any{"input": input, "done": done, "frames": frames, "store": storeFileText(cwd), "calls": calls, "addRuns": addRuns, "renders": renders}
	}
	steps := []any{snap(nil)}
	for _, in := range sc["steps"].([]any) {
		view.mu.Lock()
		confirmOpen := view.confirm != nil
		view.mu.Unlock()
		mu.Lock()
		r0 := renders
		mu.Unlock()
		res, err := view.HandleInput(in.(string))
		if err != nil {
			t.Fatal(err)
		}
		if res.Done {
			done = true
		}
		if in == "a" && !confirmOpen {
			deadline := time.Now().Add(3 * time.Second)
			for {
				mu.Lock()
				r := renders
				mu.Unlock()
				if r > r0 || time.Now().After(deadline) {
					break
				}
				time.Sleep(2 * time.Millisecond)
			}
		}
		steps = append(steps, snap(in))
	}
	return map[string]any{"steps": steps}
}

type scriptedUI struct {
	t       *testing.T
	answers []any
	ui      []any
}

func (u *scriptedUI) next(rec map[string]any) (any, error) {
	u.ui = append(u.ui, rec)
	if len(u.answers) == 0 {
		u.ui = append(u.ui, map[string]any{"m": "exhausted"})
		return nil, errors.New("the scenario ran out of answers")
	}
	a := u.answers[0]
	u.answers = u.answers[1:]
	return a, nil
}

func (u *scriptedUI) str(m string, rec map[string]any) (string, bool, error) {
	rec["m"] = m
	a, err := u.next(rec)
	if err != nil {
		return "", false, err
	}
	s, ok := a.(string)
	return s, ok, nil
}

var currentTime = regexp.MustCompile(`Current time: \S+`)

func (u *scriptedUI) Input(title, placeholder string) (string, bool, error) {
	return u.str("input", map[string]any{"title": title, "placeholder": currentTime.ReplaceAllString(placeholder, "Current time: <now>")})
}
func (u *scriptedUI) Select(title string, options []string) (string, bool, error) {
	return u.str("select", map[string]any{"title": title, "options": options})
}
func (u *scriptedUI) Confirm(title, message string) (bool, error) {
	a, err := u.next(map[string]any{"m": "confirm", "title": title, "message": message})
	if err != nil {
		return false, err
	}
	b, _ := a.(bool)
	return b, nil
}
func (u *scriptedUI) Notify(message, level string) {
	u.ui = append(u.ui, map[string]any{"m": "notify", "message": message, "level": level})
}

// normJob is the recorder's norm: the made-up id and createdAt, in place.
func normJob(j *jsobj.Object) *jsobj.Object {
	out := j.Clone()
	if s, ok := out.Get("id").(string); ok && len(s) == 10 {
		out.Set("id", "<id>")
	}
	if out.Has("createdAt") {
		out.Set("createdAt", "<now>")
	}
	return out
}

type addRecorder struct{ calls []any }

func (r *addRecorder) addJob(j *jsobj.Object) {
	r.calls = append(r.calls, map[string]any{"op": "addJob", "job": normJob(j)})
}
func (r *addRecorder) removeJob(string)                {}
func (r *addRecorder) updateJob(string, *jsobj.Object) {}
func (r *addRecorder) getNextRun(string) float64       { return nan() }

func replayFlow(t *testing.T, sc map[string]any, cwd string) map[string]any {
	storage := newFileStorage(cwd)
	ui := &scriptedUI{t: t, answers: sc["answers"].([]any), ui: []any{}}
	var st settings
	if m, ok := sc["settings"].(map[string]any); ok {
		if s, ok := m["defaultJobScope"].(string); ok {
			st.defaultJobScope = s
		}
	}
	rec := &addRecorder{calls: []any{}}
	err := runAddFlow(ui, storage, rec, st, sc["session"].(string))
	var thrown any
	if err != nil {
		thrown = err.Error()
	}
	var store any
	if text, ok := storeFileText(cwd).(string); ok {
		v, perr := jsobj.Parse(text)
		if perr != nil {
			t.Fatal(perr)
		}
		o := v.(*jsobj.Object)
		var jobs []any
		for _, j := range o.Get("jobs").([]any) {
			jobs = append(jobs, normJob(j.(*jsobj.Object)))
		}
		o.Set("jobs", jobs)
		s, _ := jsobj.Stringify(o)
		store = s
	}
	return map[string]any{"ui": ui.ui, "calls": rec.calls, "store": store, "thrown": thrown, "unusedAnswers": len(ui.answers)}
}
