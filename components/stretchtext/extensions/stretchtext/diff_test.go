package stretchtext

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// The differential test: port/golden/states.json is what the unmodified original (port/oracle, under
// Node, with Pi 1.3.0's pi-tui) produced for port/drive/cases.json. Every case is run through the Go
// port and the results must be equal, byte for byte.

type cases struct {
	Parse    []string                       `json:"parse"`
	Grow     []struct{ Short, Long string } `json:"grow"`
	Progress []float64                      `json:"progress"`
	Themes   map[string]paletteSpec         `json:"themes"`
	Sessions []sessionCase                  `json:"sessions"`
}

type paletteSpec struct {
	Appearance string                    `json:"appearance"`
	Mode       string                    `json:"mode"`
	Colors     map[string]map[string]any `json:"colors"`
}

type sessionCase struct {
	Name  string `json:"name"`
	Theme string `json:"theme"`
	Steps []struct {
		Op   string `json:"op"`
		Md   string `json:"md"`
		Type string `json:"type"`
		Ms   int    `json:"ms"`
		Link int    `json:"link"`
		Key  string `json:"key"`
		Data string `json:"data"`
	} `json:"steps"`
}

type golden struct {
	Guide string `json:"guide"`
	Parse []struct {
		Segments []any   `json:"segments"`
		Kinds    [][]any `json:"kinds"`
	} `json:"parse"`
	Grow []struct {
		Length int      `json:"length"`
		Frames []string `json:"frames"`
	} `json:"grow"`
	Sessions []struct {
		Name  string           `json:"name"`
		Trace []map[string]any `json:"trace"`
	} `json:"sessions"`
}

func loadJSON(t *testing.T, path string, into any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatal(err)
	}
}

func loadCases(t *testing.T) (cases, golden) {
	var c cases
	var g golden
	loadJSON(t, "../../port/drive/cases.json", &c)
	loadJSON(t, "../../port/golden/states.json", &g)
	return c, g
}

func serialize(segments []Segment) []any {
	out := make([]any, len(segments))
	for i, s := range segments {
		if s.Stretch == nil {
			out[i] = s.Text
			continue
		}
		var long any
		if s.Stretch.HasLong {
			long = serialize(s.Stretch.Long)
		}
		out[i] = map[string]any{"key": s.Stretch.Key, "short": s.Stretch.Short, "long": long, "closed": s.Stretch.Closed}
	}
	return out
}

func normalize(t *testing.T, v any) any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestGuideIsTheOriginals(t *testing.T) {
	_, g := loadCases(t)
	if Guide != g.Guide {
		t.Fatalf("guide differs from the original's GUIDE")
	}
}

func TestParseMatchesOriginal(t *testing.T) {
	c, g := loadCases(t)
	if len(g.Parse) != len(c.Parse) {
		t.Fatal("golden and cases disagree")
	}
	for i, text := range c.Parse {
		segments := Parse(text)
		want := g.Parse[i]
		if got := normalize(t, serialize(segments)); !reflect.DeepEqual(got, normalize(t, want.Segments)) {
			t.Fatalf("parse case %d %q:\n got %v\nwant %v", i, text, got, want.Segments)
		}
		kinds := [][]any{}
		for _, s := range Stretches(segments) {
			kinds = append(kinds, []any{map[DrawnAs]string{DrawnText: "text", DrawnOpen: "open", DrawnLink: "link"}[s.DrawnAs()], s.IsLinkable()})
		}
		if !reflect.DeepEqual(normalize(t, kinds), normalize(t, want.Kinds)) {
			t.Fatalf("parse case %d %q kinds:\n got %v\nwant %v", i, text, kinds, want.Kinds)
		}
	}
}

func TestGrowMatchesOriginal(t *testing.T) {
	c, g := loadCases(t)
	segmented := 0
	for i, gc := range c.Grow {
		long := Parse(gc.Long)
		want := g.Grow[i]
		if got := LengthOf(long); got != want.Length {
			t.Fatalf("grow case %d (%q, %q) length %d, want %d", i, gc.Short, gc.Long, got, want.Length)
		}
		for j, p := range c.Progress {
			got := Grow(gc.Short, long, p, func(n *Stretch) string { return "<" + n.Key + ">" }, func(w string) string { return "[" + w + "]" })
			if got != want.Frames[j] {
				t.Fatalf("grow case %d (%q, %q) at %v:\n got %q\nwant %q", i, gc.Short, gc.Long, p, got, want.Frames[j])
			}
		}
		if hasSegmented(gc.Short) || hasSegmented(gc.Long) {
			segmented++
		}
	}
	if segmented == 0 {
		t.Fatal("no grow case holds Thai, Lao, Khmer or Myanmar: the dictionary segmenter is not compared with the original's Intl.Segmenter")
	}
	t.Logf("%d of %d grow cases hold Thai, Lao, Khmer or Myanmar, compared with the original frame by frame", segmented, len(c.Grow))
}

// fakeClock is the test's time: Every callbacks run in due order as Advance moves it.
type fakeClock struct {
	now    time.Time
	timers []*fakeTimer
	nextID int
}

type fakeTimer struct {
	id   int
	due  time.Time
	d    time.Duration
	f    func()
	dead bool
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Unix(1_000_000_000, 0)} }

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) Every(d time.Duration, f func()) func() {
	c.nextID++
	timer := &fakeTimer{id: c.nextID, due: c.now.Add(d), d: d, f: f}
	c.timers = append(c.timers, timer)
	return func() { timer.dead = true }
}

func (c *fakeClock) Advance(d time.Duration) {
	end := c.now.Add(d)
	for {
		var due []*fakeTimer
		for _, t := range c.timers {
			if !t.dead && !t.due.After(end) {
				due = append(due, t)
			}
		}
		if len(due) == 0 {
			break
		}
		sort.Slice(due, func(i, j int) bool {
			if !due[i].due.Equal(due[j].due) {
				return due[i].due.Before(due[j].due)
			}
			return due[i].id < due[j].id
		})
		t := due[0]
		c.now = t.due
		t.due = t.due.Add(t.d)
		t.f()
	}
	c.now = end
	live := c.timers[:0]
	for _, t := range c.timers {
		if !t.dead {
			live = append(live, t)
		}
	}
	c.timers = live
}

// fakeTheme is the original driver's theme: tokens of the palette paint as pi-tui does, any other
// token is wrapped in a visible marker.
type fakeTheme struct{ spec paletteSpec }

func colorOf(m map[string]any) Color {
	num := func(k string) float64 { f, _ := m[k].(float64); return f }
	switch m["kind"] {
	case "indexed":
		return Indexed(int(num("index")))
	case "rgb":
		return RGB{num("r"), num("g"), num("b")}
	}
	return OKLCH{num("l"), num("c"), num("h")}
}

func (f fakeTheme) ColorOf(token string) (Color, bool) {
	m, ok := f.spec.Colors[token]
	if !ok {
		return nil, false
	}
	return colorOf(m), true
}
func (f fakeTheme) Dark() bool        { return f.spec.Appearance == "dark" }
func (f fakeTheme) ColorMode() string { return f.spec.Mode }
func (f fakeTheme) Fg(token, text string) string {
	if c, ok := f.ColorOf(token); ok {
		if i, isIndexed := c.(Indexed); isIndexed {
			return "\x1b[38;5;" + itoa(int(i)) + "m" + text + "\x1b[39m"
		}
		return foregroundAnsi(c.rgb(), f.spec.Mode) + text + "\x1b[39m"
	}
	return "⟦" + token + "⟧" + text + "⟦/⟧"
}

// fakeSections records Set and Delete in order, as the driver's plain object does.
type fakeSections struct{ entries [][2]string }

func (s *fakeSections) Set(name, value string) {
	for i := range s.entries {
		if s.entries[i][0] == name {
			s.entries[i][1] = value
			return
		}
	}
	s.entries = append(s.entries, [2]string{name, value})
}

func (s *fakeSections) Delete(name string) {
	for i := range s.entries {
		if s.entries[i][0] == name {
			s.entries = append(s.entries[:i], s.entries[i+1:]...)
			return
		}
	}
}

// visibleWidthBefore is the column of byte offset at in line: the original measures the text before a
// link with pi-tui's visibleWidth; the click only needs to land on the link, which the port is told by
// address, so the column is not needed. The line index is.
func linkAddresses(out string) []struct {
	line    int
	address string
} {
	var found []struct {
		line    int
		address string
	}
	for y, line := range strings.Split(out, "\n") {
		for rest := line; ; {
			at := strings.Index(rest, "\x1b]8;;stretchtext:")
			if at == -1 {
				break
			}
			rest = rest[at+len("\x1b]8;;"):]
			end := strings.IndexByte(rest, 0x07)
			found = append(found, struct {
				line    int
				address string
			}{y, rest[:end]})
		}
	}
	return found
}

func TestSessionsMatchOriginal(t *testing.T) {
	c, g := loadCases(t)
	for si, session := range c.Sessions {
		want := g.Sessions[si]
		if want.Name != session.Name {
			t.Fatalf("session %d is %q, golden has %q", si, session.Name, want.Name)
		}
		t.Run(session.Name, func(t *testing.T) {
			clock := newFakeClock()
			redraws := 0
			var theme Theme
			if session.Theme != "" {
				theme = fakeTheme{c.Themes[session.Theme]}
			}
			core := NewCore(clock, func() Theme { return theme }, func() { redraws++ })
			outputs := map[string]string{}
			var prompt *LetterPrompt
			promptOpen := false
			for i, step := range session.Steps {
				before := redraws
				got := map[string]any{"op": step.Op}
				var notices []any = []any{}
				switch step.Op {
				case "session_start":
					if theme != nil {
						core.Attach()
					}
				case "transform":
					mt := step.Type
					if mt == "" {
						mt = "assistant"
					}
					out := core.Transform(step.Md, mt)
					outputs[step.Md] = out
					got["out"] = out
				case "tick":
					clock.Advance(time.Duration(step.Ms) * time.Millisecond)
				case "click":
					links := linkAddresses(outputs[step.Md])
					address := "https://example.com/"
					if step.Link >= 0 && step.Link < len(links) {
						address = links[step.Link].address
					} else if step.Link == 99 {
						address = "stretchtext:99"
					}
					// A press is taken by the host and starts no toggle; only a click does.
					var handled bool
					if step.Type == "press" {
						_, handled = core.links.Resolve(address)
					} else {
						handled = core.Click(address)
					}
					got["handled"] = handled
				case "alt":
					mode := ModeOpen
					if step.Key == "alt+c" {
						mode = ModeClose
					}
					p, notice := core.Alt(mode)
					if p != nil {
						prompt, promptOpen = p, true
					}
					if notice != "" {
						notices = append(notices, []any{notice, "info"})
					}
					got["opened"] = promptOpen
					if promptOpen && p != nil {
						got["prompt"] = []string{prompt.Render(80)}
					}
				case "input":
					if promptOpen {
						prompt.HandleInput(step.Data)
						promptOpen = false
					}
					got["closed"] = !promptOpen
					if prompt == nil {
						got["closed"] = nil
					}
				case "before_agent_start":
					sections := &fakeSections{entries: [][2]string{{"base", "base prompt"}}}
					core.BeforeAgentStart(sections)
					entries := make([]any, len(sections.entries))
					for k, e := range sections.entries {
						entries[k] = []any{e[0], e[1]}
					}
					got["sections"] = entries
				case "shutdown":
					core.Stop()
				}
				got["notices"] = notices
				got["redraw"] = redraws > before
				if !reflect.DeepEqual(normalize(t, got), normalize(t, want.Trace[i])) {
					a, b := got["out"], want.Trace[i]["out"]
					if as, ok := a.(string); ok {
						bs, _ := b.(string)
						n := 0
						for n < len(as) && n < len(bs) && as[n] == bs[n] {
							n++
						}
						t.Fatalf("step %d (%s) output differs at byte %d:\n got …%q\nwant …%q", i, step.Op, n, as[max(0, n-30):min(len(as), n+60)], bs[max(0, n-30):min(len(bs), n+60)])
					}
					t.Fatalf("step %d (%s):\n got %s\nwant %s", i, step.Op, mustJSON(got), mustJSON(want.Trace[i]))
				}
			}
			clock.Advance(10 * time.Second)
		})
	}
}

func mustJSON(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}
