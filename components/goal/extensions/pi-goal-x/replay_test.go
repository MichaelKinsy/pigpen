package pi_goal_x

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// Layer 1: scripted sessions run through the Go core and compared, event by event and file by file, with what the ORIGINAL
// extension did in the same session (port/drive/drive.mjs records testdata/drive-golden.json by running the original in-process).
// Time is the step's clock, the goal ids are the ones the original generated, the scheduler generation is masked.

type fakeHost struct {
	cwd       string
	events    [][]any
	entries   []any
	confirm   []bool
	noUI      bool
	busy      bool
	aborted   int
	rich      bool
	pick      string
	onConfirm func()
}

func (h *fakeHost) Cwd() string       { return h.cwd }
func (h *fakeHost) SessionID() string { return "drive-session" }
func (h *fakeHost) HasUI() bool       { return !h.noUI }
func (h *fakeHost) Rich() bool        { return h.rich }
func (h *fakeHost) Notify(m, l string) {
	h.events = append(h.events, []any{"notify", l, m})
}
func (h *fakeHost) SetStatus(k, t string) {
	if t == "" {
		h.events = append(h.events, []any{"status", k, nil})
		return
	}
	h.events = append(h.events, []any{"status", k, t})
}
func (h *fakeHost) ClearWidget(k string) { h.events = append(h.events, []any{"widget", k, "clear"}) }
func (h *fakeHost) Confirm(t, m string) bool {
	if h.onConfirm != nil {
		h.onConfirm()
	}
	h.events = append(h.events, []any{"confirm", t, m})
	if len(h.confirm) == 0 {
		return false
	}
	a := h.confirm[0]
	h.confirm = h.confirm[1:]
	return a
}
func (h *fakeHost) Select(_ string, opts []string) (string, bool) {
	for _, o := range opts {
		if h.pick != "" && strings.Contains(o, h.pick) {
			return o, true
		}
	}
	return "", false
}
func (h *fakeHost) Branch() []any { return h.entries }
func (h *fakeHost) AppendEntry(t string, d *jsObject) {
	h.events = append(h.events, []any{"entry", t, marshalJSON(d, "")})
	e := newObject()
	e.set("type", "custom")
	e.set("customType", t)
	v, _ := parseJSON([]byte(marshalJSON(d, "")))
	e.set("data", v)
	h.entries = append(h.entries, e)
}
func (h *fakeHost) IsIdle() bool { return !h.busy }
func (h *fakeHost) Abort() {
	h.aborted++
	h.events = append(h.events, []any{"abort"})
}

type step struct {
	Cmd     string            `json:"cmd"`
	Args    string            `json:"args"`
	Event   string            `json:"event"`
	Reason  string            `json:"reason"`
	At      *int64            `json:"at"`
	Confirm *bool             `json:"confirm"`
	Write   map[string]string `json:"write"`
	Remove  []string          `json:"remove"`
	Before  *struct {
		Remove []string `json:"remove"`
	} `json:"before"`
	// DuringConfirm changes the workspace while a confirmation dialog is open (another process at work).
	DuringConfirm *struct {
		Remove []string `json:"remove"`
	} `json:"duringConfirm"`
}

type driveCase struct {
	Name      string            `json:"name"`
	Files     map[string]string `json:"files"`
	AgentFile map[string]string `json:"agentFiles"`
	Env       map[string]string `json:"env"`
	Entries   []any             `json:"entries"`
	Steps     []step            `json:"steps"`
	NoUI      bool              `json:"noUI"` // a host without a user interface (print or json mode)
	Busy      bool              `json:"busy"` // the agent is streaming: isIdle() is false
}

type goldenStep struct {
	Step   string  `json:"step"`
	Events [][]any `json:"events"`
}

type goldenCase struct {
	Steps []goldenStep      `json:"steps"`
	Files map[string]string `json:"files"`
}

// The scheduler stamps its pause events with the wall clock (new Date()), which no test can pin.
var agentAtRE = regexp.MustCompile(`"source":"agent","at":"[^"]*"`)

var uuidRE = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// idsOf lists the goal ids the original created, in order: the "created" focus entries of the session.
func idsOf(g goldenCase) []string {
	var ids []string
	for _, s := range g.Steps {
		for _, e := range s.Events {
			if len(e) == 3 && e[0] == "entry" && e[1] == focusEntryType {
				var d struct {
					ID     string `json:"focusedGoalId"`
					Reason string `json:"reason"`
				}
				_ = json.Unmarshal([]byte(e[2].(string)), &d)
				if d.Reason == "created" {
					ids = append(ids, d.ID)
				}
			}
		}
	}
	return ids
}

func readTree(t *testing.T, root string) map[string]string {
	out := map[string]string{}
	base := filepath.Join(root, ".pi")
	_ = filepath.Walk(base, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if fi.Name() == ".goals-pool-snapshot.json" {
			return nil
		}
		if fi.IsDir() {
			if rel != ".pi" {
				out[rel+"/"] = ""
			}
			return nil
		}
		b, _ := os.ReadFile(p)
		out[rel] = string(b)
		return nil
	})
	return out
}

func normEvent(e []any) string {
	b, _ := json.Marshal(e)
	return string(b)
}

func TestReplayDriveCases(t *testing.T) {
	var cases []driveCase
	var golden map[string]goldenCase
	readJSON(t, "testdata/drive-cases.json", &cases)
	readJSON(t, "testdata/drive-golden.json", &golden)
	for _, c := range cases {
		c := c
		t.Run(c.Name, func(t *testing.T) { replayCase(t, c, golden[c.Name]) })
	}
}

func readJSON(t *testing.T, file string, into any) {
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, into); err != nil {
		t.Fatal(err)
	}
}

func replayCase(t *testing.T, c driveCase, g goldenCase) {
	cwd, agent := t.TempDir(), t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agent)
	for _, k := range os.Environ() {
		if strings.HasPrefix(k, "PI_GOAL_") {
			os.Unsetenv(strings.SplitN(k, "=", 2)[0])
		}
	}
	for k, v := range c.Env {
		t.Setenv(k, v)
	}
	write := func(root, rel, text string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for rel, text := range c.Files {
		write(cwd, rel, text)
	}
	for rel, text := range c.AgentFile {
		write(agent, rel, text)
	}
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC).UnixMilli()
	now := base
	ids := idsOf(g)
	oldClock, oldID, oldUUID, oldZone := clockMs, newGoalId, newUUID, localZone
	clockMs = func() int64 { return now }
	newGoalId = func() string {
		if len(ids) == 0 {
			t.Errorf("the port created a goal the original did not create")
			return "extra-goal"
		}
		id := ids[0]
		ids = ids[1:]
		return id
	}
	newUUID = func() string { return "00000000-0000-4000-8000-000000000000" }
	localZone = time.UTC
	ledgerDirKnown = map[string]bool{}
	invalidateGoalPoolCache()
	t.Cleanup(func() { clockMs, newGoalId, newUUID, localZone = oldClock, oldID, oldUUID, oldZone })

	h := &fakeHost{cwd: cwd, noUI: c.NoUI, busy: c.Busy}
	for _, e := range c.Entries {
		v, _ := json.Marshal(e)
		o, _ := parseJSON(v)
		h.entries = append(h.entries, o)
	}
	co := newCore(h)
	run := func(fn func()) { fn(); co.flush() }
	start := func() { invalidateGoalPoolCache(); run(co.sessionStart) }
	for i, s := range c.Steps {
		if s.At != nil {
			now = base + *s.At
		}
		h.events = nil
		h.confirm = nil
		if s.Confirm != nil {
			h.confirm = []bool{*s.Confirm}
		}
		h.onConfirm = nil
		if s.DuringConfirm != nil {
			remove := s.DuringConfirm.Remove
			h.onConfirm = func() {
				for _, rel := range remove {
					os.Remove(filepath.Join(cwd, rel))
				}
			}
		}
		for rel, text := range s.Write {
			write(cwd, rel, text)
		}
		for _, rel := range s.Remove {
			os.Remove(filepath.Join(cwd, rel))
		}
		if s.Before != nil {
			for _, rel := range s.Before.Remove {
				os.Remove(filepath.Join(cwd, rel))
			}
		}
		switch {
		case s.Cmd != "":
			found := false
			for _, d := range commandDefs {
				if d.name == s.Cmd {
					found = true
					run(func() { _ = d.fn(co, s.Args) })
				}
			}
			if !found {
				t.Fatalf("no command %s", s.Cmd)
			}
		case s.Event == "new_session":
			run(co.shutdown)
			h.entries = nil
			start()
		case s.Event == "session_shutdown":
			run(co.shutdown)
		case s.Event == "session_start":
			start()
		}
		want := g.Steps[i].Events
		var wantEvents []string
		for _, e := range want {
			if e[0] == "widget" && e[2] == "factory" {
				continue // a component the SDK cannot carry; Pi's RPC host drops it too
			}
			wantEvents = append(wantEvents, normEvent(e))
		}
		var got []string
		for _, e := range h.events {
			if e[0] == "notify" && strings.Contains(e[2].(string), "(Go port)") {
				continue // this port's notice that automatic continuation is not ported; the original starts a run there
			}
			got = append(got, normEvent(e))
		}
		if strings.Join(got, "\n") != strings.Join(wantEvents, "\n") {
			t.Errorf("step %d (%s%s): events differ\n got: %s\nwant: %s", i, s.Cmd, s.Event, strings.Join(got, "\n      "), strings.Join(wantEvents, "\n      "))
		}
	}
	gotFiles, wantFiles := readTree(t, cwd), g.Files
	var names []string
	for k := range wantFiles {
		names = append(names, k)
	}
	for k := range gotFiles {
		if _, ok := wantFiles[k]; !ok {
			names = append(names, k)
		}
	}
	sort.Strings(names)
	for _, k := range names {
		w, wok := wantFiles[k]
		gt, gok := gotFiles[k]
		if k == ".pi/pi-goal-x-settings.json" || strings.HasPrefix(k, ".pi/alt") {
			continue
		}
		if wok != gok {
			t.Errorf("file %s: present in Go %v, in the original %v", k, gok, wok)
			continue
		}
		w, gt = uuidRE.ReplaceAllString(w, "<uuid>"), uuidRE.ReplaceAllString(gt, "<uuid>")
		w, gt = agentAtRE.ReplaceAllString(w, `"source":"agent","at":"<now>"`), agentAtRE.ReplaceAllString(gt, `"source":"agent","at":"<now>"`)
		if gt != w {
			t.Errorf("file %s differs\n got: %q\nwant: %q", k, gt, w)
		}
	}
}
