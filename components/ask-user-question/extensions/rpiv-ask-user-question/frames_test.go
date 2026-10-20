package ask_user_question

// Differential frame-equivalence test (slice B). The goldens in port/frames/golden were recorded from the UNMODIFIED
// original (rpiv-ask-user-question 2.11.0, port/oracle) under Pi's real pi-tui and pi-coding-agent by
// port/frames/recorder (see record.ts for what is real and what is faked there); the scenarios in port/frames/scenarios
// are authored by port/frames/gen-scenarios.py. Pi's real TUI renders the questionnaire through ctx.ui.custom, which
// pigeq's RPC mode cannot exercise, so this is the equivalence evidence for the questionnaire's rendering and key
// handling: every scenario is replayed through the Go tool (`app.run`, the same entry the host calls) with a test host
// that does for the Go component what the recorder's fake ctx.ui.custom does for the original, and every frame, the
// done() result, the tool result, the events and the notifications are compared with the golden.
//
// Where Go legitimately differs, port/frames/known-differences.json records the exact difference with a reason; the test
// asserts that exact set, so a difference can neither grow nor go stale silently.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/ask-user-question/internal/ui"
	tui "github.com/MichaelKinsy/pigpen/components/pitui"
)

const framesDir = "../../port/frames"

// ---- scenario and golden files ---------------------------------------------------------------------------------

type frStep struct {
	Input  string `json:"input"`
	Label  string `json:"label"`
	Render *bool  `json:"render"`
	Settle string `json:"settle"`
	Resize *struct {
		Width int `json:"width"`
		Rows  int `json:"rows"`
	} `json:"resize"`
}

type frScenario struct {
	Name        string                     `json:"name"`
	Params      map[string]any             `json:"params"`
	Width       int                        `json:"width"`
	Rows        int                        `json:"rows"`
	CollapseKey *string                    `json:"collapseKey"`
	Locale      string                     `json:"locale"`
	Keybindings map[string]json.RawMessage `json:"keybindings"`
	RawInput    *bool                      `json:"rawInput"`
	Hyperlinks  bool                       `json:"hyperlinks"`
	Steps       []frStep                   `json:"steps"`
}

type frFrame struct {
	Step     int      `json:"step"`
	Label    *string  `json:"label"`
	Lines    []string `json:"lines"`
	Hidden   bool     `json:"hidden"`
	Renders  int      `json:"renders"`
	Consumed bool     `json:"consumed"`
}

type frEvent struct {
	Channel string `json:"channel"`
	Payload any    `json:"payload"`
}

type frNote struct {
	Message string `json:"message"`
	Level   string `json:"level"`
}

type frGolden struct {
	Scenario   string    `json:"scenario"`
	Frames     []frFrame `json:"frames"`
	Result     any       `json:"result"`
	ToolResult struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Details any `json:"details"`
	} `json:"toolResult"`
	Events        []frEvent `json:"events"`
	Notifications []frNote  `json:"notifications"`
	Listener      struct {
		Registered bool `json:"registered"`
		Removed    bool `json:"removed"`
	} `json:"listener"`
}

func frReadJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// ---- the recording theme (the Go twin of record.ts) ------------------------------------------------------------

type frPalette struct {
	Fg []string `json:"fg"`
	Bg []string `json:"bg"`
}

func frLoadPalette(t *testing.T) frPalette {
	var p frPalette
	frReadJSON(t, filepath.Join(framesDir, "theme-tokens.json"), &p)
	return p
}

type frTheme struct{ fg, bg map[string]int }

func newFrTheme(p frPalette) frTheme {
	th := frTheme{fg: map[string]int{}, bg: map[string]int{}}
	for i, n := range p.Fg {
		th.fg[n] = 16 + i
	}
	for i, n := range p.Bg {
		th.bg[n] = 16 + i
	}
	return th
}

func frSGR(open, text, closeCode string) string {
	return "\x1b[" + open + "m" + text + "\x1b[" + closeCode + "m"
}

func (th frTheme) Fg(color, text string) string {
	n, ok := th.fg[color]
	if !ok {
		panic(fmt.Sprintf("recording theme: unknown fg token %q", color))
	}
	return frSGR("38;5;"+strconv.Itoa(n), text, "39")
}

func (th frTheme) Bg(color, text string) string {
	n, ok := th.bg[color]
	if !ok {
		panic(fmt.Sprintf("recording theme: unknown bg token %q", color))
	}
	return frSGR("48;5;"+strconv.Itoa(n), text, "49")
}
func (frTheme) Bold(text string) string          { return frSGR("1", text, "22") }
func (frTheme) Italic(text string) string        { return frSGR("3", text, "23") }
func (frTheme) Underline(text string) string     { return frSGR("4", text, "24") }
func (frTheme) Strikethrough(text string) string { return frSGR("9", text, "29") }
func (frTheme) Inverse(text string) string       { return frSGR("7", text, "27") }

var frNamedSGR = map[string]string{
	"0": "<reset>", "1": "<b>", "22": "</b>", "3": "<i>", "23": "</i>", "4": "<u>", "24": "</u>", "9": "<s>", "29": "</s>",
	"7": "<inv>", "27": "</inv>", "39": "</fg>", "49": "</bg>",
}

var frSGRPattern = regexp.MustCompile("\x1b\\[([0-9;]*)m")

// frSGRTags is sgrTags of record.ts.
func frSGRTags(p frPalette, params string) string {
	if params == "" {
		params = "0"
	}
	parts := strings.Split(params, ";")
	var out []string
	for i := 0; i < len(parts); {
		c := parts[i]
		switch {
		case (c == "38" || c == "48") && i+2 < len(parts) && parts[i+1] == "5":
			list, kind := p.Bg, "bg"
			if c == "38" {
				list, kind = p.Fg, "fg"
			}
			name := "?" + parts[i+2]
			if n, err := strconv.Atoi(parts[i+2]); err == nil && n >= 16 && n-16 < len(list) {
				name = list[n-16]
			}
			out = append(out, "<"+kind+":"+name+">")
			i += 3
		case (c == "38" || c == "48") && i+4 < len(parts) && parts[i+1] == "2":
			out = append(out, "<sgr:"+strings.Join(parts[i:i+5], ";")+">")
			i += 5
		default:
			if tag, ok := frNamedSGR[c]; ok {
				out = append(out, tag)
			} else {
				out = append(out, "<sgr:"+c+">")
			}
			i++
		}
	}
	if len(out) == 1 {
		return out[0]
	}
	return "<+" + strings.Join(out, "") + "+>"
}

// frTagify is tagify of record.ts: the stored form of a frame line.
func frTagify(p frPalette, line string) string {
	return frSGRPattern.ReplaceAllStringFunc(line, func(m string) string {
		return frSGRTags(p, frSGRPattern.FindStringSubmatch(m)[1])
	})
}

// ---- the test host ---------------------------------------------------------------------------------------------

type frHandle struct{ hidden bool }

func (h *frHandle) SetHidden(hidden bool) { h.hidden = hidden }
func (h *frHandle) IsHidden() bool        { return h.hidden }
func (h *frHandle) IsFocused() bool       { return !h.hidden }

// frRun is what one replay observed.
type frRun struct {
	frames        []frFrame
	result        any
	events        []frEvent
	notifications []frNote
	registered    bool
	removed       bool
	toolText      string
	toolDetails   any
	done          bool
}

// frameHost is a `host` that replays a scenario inside Custom, the way record.ts's fake ctx.ui.custom does for the
// original: raw listeners first, then nothing to a hidden overlay, then the component.
type frameHost struct {
	t       *testing.T
	sc      *frScenario
	palette frPalette
	theme   frTheme
	kb      tui.Keybindings
	width   int
	rows    int

	run      frRun
	renders  int
	listener func(string) sdk.TerminalInputResult
}

func (h *frameHost) HasUI() bool                  { return true }
func (h *frameHost) Mode() string                 { return "tui" }
func (h *frameHost) Width() int                   { return h.width }
func (h *frameHost) Height() int                  { return h.rows }
func (h *frameHost) Theme() ui.Theme              { return h.theme }
func (h *frameHost) Keybindings() tui.Keybindings { return h.kb }

func (h *frameHost) Select(string, []string) (string, bool, error) {
	h.t.Fatal("unexpected Select: the questionnaire must not fall back to dialogs")
	return "", false, nil
}

func (h *frameHost) Input(string, string) (string, bool, error) {
	h.t.Fatal("unexpected Input: the questionnaire must not fall back to dialogs")
	return "", false, nil
}

func (h *frameHost) Emit(channel string, payload any) {
	var round any
	data, err := json.Marshal(payload)
	if err != nil || json.Unmarshal(data, &round) != nil {
		h.t.Fatalf("event %s payload %#v does not round-trip through JSON", channel, payload)
	}
	h.run.events = append(h.run.events, frEvent{channel, round})
}

func (h *frameHost) Notify(message, level string) {
	h.run.notifications = append(h.run.notifications, frNote{message, level})
}

func (h *frameHost) OnTerminalInput(handler func(string) sdk.TerminalInputResult) (func(), bool) {
	if h.sc.RawInput != nil && !*h.sc.RawInput {
		return nil, false
	}
	h.run.registered = true
	h.listener = handler
	return func() {
		h.listener = nil
		h.run.removed = true
	}, true
}

func (h *frameHost) snap(base frFrame, c sdk.RemoteComponent, handle *frHandle) {
	lines := c.Render(h.width)
	base.Lines = make([]string, len(lines))
	for i, l := range lines {
		base.Lines[i] = frTagify(h.palette, l)
	}
	base.Hidden = handle.hidden
	base.Renders = h.renders
	h.run.frames = append(h.run.frames, base)
}

func (h *frameHost) Custom(component sdk.RemoteComponent, onHandle func(ui.OverlayHandle)) (any, error) {
	inv, ok := component.(sdk.RemoteComponentInvalidator)
	if !ok {
		h.t.Fatal("the component does not take a render-invalidation callback")
	}
	inv.SetInvalidate(func() { h.renders++ })
	handle := &frHandle{}
	onHandle(handle)
	h.snap(frFrame{Step: 0, Label: new("initial")}, component, handle)
	for i, step := range h.sc.Steps {
		if h.run.done {
			h.t.Fatalf("step %d follows done()", i+1)
		}
		if step.Resize != nil {
			h.width, h.rows = step.Resize.Width, step.Resize.Rows
		}
		consumed := false
		var res sdk.RemoteComponentResult
		// pi-tui handleInput: raw listeners first; a hidden overlay gets no input; key releases are dropped.
		if h.listener != nil && h.listener(step.Input).Consume {
			consumed = true
		} else if !handle.hidden && !tui.IsKeyRelease(step.Input) {
			var err error
			if res, err = component.HandleInput(step.Input); err != nil {
				h.t.Fatalf("step %d HandleInput: %v", i+1, err)
			}
		}
		if res.Done {
			h.run.done = true
			data, err := json.Marshal(res.Value)
			if err != nil || json.Unmarshal(data, &h.run.result) != nil {
				h.t.Fatalf("done value %#v does not round-trip through JSON", res.Value)
			}
		}
		if step.Render != nil && !*step.Render {
			continue
		}
		var label *string
		if step.Label != "" {
			label = new(step.Label)
		}
		h.snap(frFrame{Step: i + 1, Label: label, Consumed: consumed}, component, handle)
	}
	if !h.run.done {
		return nil, fmt.Errorf("the scenario ended before the questionnaire completed")
	}
	return h.run.result, nil
}

// ---- replay and comparison -------------------------------------------------------------------------------------

func frRawKeybindings(t *testing.T, raw map[string]json.RawMessage) map[string][]string {
	if len(raw) == 0 {
		return nil
	}
	out := map[string][]string{}
	for id, v := range raw {
		var one string
		var many []string
		switch {
		case json.Unmarshal(v, &one) == nil:
			out[id] = []string{one}
		case json.Unmarshal(v, &many) == nil:
			out[id] = many
		default:
			t.Fatalf("keybinding %s: %s", id, v)
		}
	}
	return out
}

func frReplay(t *testing.T, sc *frScenario, palette frPalette) frRun {
	t.Helper()
	// Environment the recorder had: scratch HOME, no LANG, a config home that holds only this scenario's config.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("LANG", "")
	t.Setenv("LC_ALL", "")
	if sc.CollapseKey != nil {
		dir := filepath.Join(home, "rpiv-ask-user-question")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(map[string]string{"collapseKey": *sc.CollapseKey})
		if err := os.WriteFile(filepath.Join(dir, "config.json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ui.SetLocale(sc.Locale)
	t.Cleanup(func() { ui.SetLocale("") })
	// OSC 8 hyperlink support is detected from the environment; pin it as the recorder does.
	tui.SetHyperlinksSupported(new(sc.Hyperlinks))
	t.Cleanup(func() { tui.SetHyperlinksSupported(nil) })

	kb := tui.NewKeybindings(frRawKeybindings(t, sc.Keybindings))
	previous := tui.GetTUIKeybindings()
	tui.SetTUIKeybindings(kb)
	t.Cleanup(func() { tui.SetTUIKeybindings(previous) })

	h := &frameHost{t: t, sc: sc, palette: palette, theme: newFrTheme(palette), kb: kb, width: sc.Width, rows: sc.Rows}
	out, err := (&app{}).run(h, frJSONClone(t, sc.Params).(map[string]any))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	res, ok := out.(sdk.ToolResult)
	if !ok {
		t.Fatalf("run returned %T, want sdk.ToolResult", out)
	}
	h.run.toolText = res.Content
	h.run.toolDetails = frJSONClone(t, res.Details)
	return h.run
}

func frJSONClone(t *testing.T, v any) any {
	t.Helper()
	if v == nil {
		return nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// frDiff is one difference between the golden and the Go run. For lines, Line is the line index; for the other kinds
// Step is -1 and Golden/Go are the JSON of the compared values.
type frDiff struct {
	Scenario string `json:"scenario"`
	Kind     string `json:"kind"`
	Step     int    `json:"step"`
	Line     int    `json:"line"`
	Golden   string `json:"golden"`
	Go       string `json:"go"`
}

func (d frDiff) key() string {
	return fmt.Sprintf("%s|%s|%d|%d|%s|%s", d.Scenario, d.Kind, d.Step, d.Line, d.Golden, d.Go)
}

func frJSON(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}

func frCompare(name string, g *frGolden, r frRun) []frDiff {
	var diffs []frDiff
	whole := func(kind string, golden, got any) {
		if !reflect.DeepEqual(golden, got) {
			diffs = append(diffs, frDiff{Scenario: name, Kind: kind, Step: -1, Line: -1, Golden: frJSON(golden), Go: frJSON(got)})
		}
	}
	if len(g.Frames) != len(r.frames) {
		diffs = append(diffs, frDiff{Scenario: name, Kind: "frame-count", Step: -1, Line: -1, Golden: strconv.Itoa(len(g.Frames)), Go: strconv.Itoa(len(r.frames))})
	}
	// Render requests are counted from the first frame: the original's constructor requests a render before any
	// invalidation callback exists on the Go side (it is wired by Custom), and nothing is drawn before the first frame.
	var gBase, rBase int
	if len(g.Frames) > 0 && len(r.frames) > 0 {
		gBase, rBase = g.Frames[0].Renders, r.frames[0].Renders
	}
	for i := 0; i < len(g.Frames) && i < len(r.frames); i++ {
		gf, rf := g.Frames[i], r.frames[i]
		if gf.Step != rf.Step {
			diffs = append(diffs, frDiff{Scenario: name, Kind: "step", Step: gf.Step, Line: -1, Golden: strconv.Itoa(gf.Step), Go: strconv.Itoa(rf.Step)})
			continue
		}
		for l := 0; l < len(gf.Lines) || l < len(rf.Lines); l++ {
			gl, rl := "<missing>", "<missing>"
			if l < len(gf.Lines) {
				gl = gf.Lines[l]
			}
			if l < len(rf.Lines) {
				rl = rf.Lines[l]
			}
			if gl != rl {
				diffs = append(diffs, frDiff{Scenario: name, Kind: "lines", Step: gf.Step, Line: l, Golden: gl, Go: rl})
			}
		}
		if gf.Hidden != rf.Hidden {
			diffs = append(diffs, frDiff{Scenario: name, Kind: "hidden", Step: gf.Step, Line: -1, Golden: frJSON(gf.Hidden), Go: frJSON(rf.Hidden)})
		}
		if gf.Consumed != rf.Consumed {
			diffs = append(diffs, frDiff{Scenario: name, Kind: "consumed", Step: gf.Step, Line: -1, Golden: frJSON(gf.Consumed), Go: frJSON(rf.Consumed)})
		}
		if gf.Renders-gBase != rf.Renders-rBase {
			diffs = append(diffs, frDiff{Scenario: name, Kind: "renders", Step: gf.Step, Line: -1, Golden: strconv.Itoa(gf.Renders - gBase), Go: strconv.Itoa(rf.Renders - rBase)})
		}
	}
	whole("result", g.Result, r.result)
	goldenText := ""
	if len(g.ToolResult.Content) > 0 {
		goldenText = g.ToolResult.Content[0].Text
	}
	whole("tool-text", goldenText, r.toolText)
	whole("tool-details", g.ToolResult.Details, r.toolDetails)
	whole("events", frJSON(g.Events), frJSON(r.events))
	whole("notifications", frJSON(emptyNotes(g.Notifications)), frJSON(emptyNotes(r.notifications)))
	whole("listener", [2]bool{g.Listener.Registered, g.Listener.Removed}, [2]bool{r.registered, r.removed})
	return diffs
}

func emptyNotes(n []frNote) []frNote {
	if n == nil {
		return []frNote{}
	}
	return n
}

// ---- known differences -----------------------------------------------------------------------------------------

type frKnown struct {
	// Status is "known" (a legitimate difference of the port) or "bug" (a divergence reported to the lead, to be fixed).
	Status string   `json:"status"`
	Reason string   `json:"reason"`
	Diffs  []frDiff `json:"diffs"`
}

func frLoadKnown(t *testing.T) map[string]frKnown {
	t.Helper()
	path := filepath.Join(framesDir, "known-differences.json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	var all map[string]frKnown // keyed by scenario
	frReadJSON(t, path, &all)
	return all
}

func frDescribe(d frDiff, steps map[int]string) string {
	if d.Kind == "lines" {
		return fmt.Sprintf("scenario %s step %d (%s) line %d\n  golden: %s\n  go:     %s", d.Scenario, d.Step, steps[d.Step], d.Line, d.Golden, d.Go)
	}
	return fmt.Sprintf("scenario %s %s", d.Scenario, d.Kind) + func() string {
		if d.Step >= 0 {
			return fmt.Sprintf(" step %d (%s)", d.Step, steps[d.Step])
		}
		return ""
	}() + fmt.Sprintf("\n  golden: %s\n  go:     %s", d.Golden, d.Go)
}

func TestFramesAgainstOriginal(t *testing.T) {
	// `pigeq mutate` runs the extension directory alone, without the port directory beside it; the committed
	// goldens are always present in a checkout, where this test runs.
	if _, err := os.Stat(filepath.Join(framesDir, "theme-tokens.json")); os.IsNotExist(err) {
		t.Skip("port/frames is not beside the extension directory (a mutation run copies the extension only)")
	}
	scenarioFiles, err := filepath.Glob(filepath.Join(framesDir, "scenarios", "*.json"))
	if err != nil || len(scenarioFiles) == 0 {
		t.Fatalf("no scenarios under %s (%v)", framesDir, err)
	}
	sort.Strings(scenarioFiles)
	goldenFiles, _ := filepath.Glob(filepath.Join(framesDir, "golden", "*.json"))
	if len(goldenFiles) != len(scenarioFiles) {
		t.Fatalf("%d scenarios but %d goldens: run the recorder (port/frames/recorder/record.sh)", len(scenarioFiles), len(goldenFiles))
	}
	palette := frLoadPalette(t)
	known := frLoadKnown(t)
	exists := map[string]bool{}
	var frames, scenarios, knownCount int
	dump := map[string][]frDiff{}

	for _, file := range scenarioFiles {
		var sc frScenario
		frReadJSON(t, file, &sc)
		name := sc.Name
		exists[name] = true
		var g frGolden
		frReadJSON(t, filepath.Join(framesDir, "golden", name+".json"), &g)
		scenarios++
		t.Run(name, func(t *testing.T) {
			run := frReplay(t, &sc, palette)
			frames += len(run.frames)
			diffs := frCompare(name, &g, run)
			steps := map[int]string{0: "initial"}
			for i, s := range sc.Steps {
				steps[i+1] = s.Label
			}
			dump[name] = diffs
			want := map[string]bool{}
			if k, ok := known[name]; ok {
				for _, d := range k.Diffs {
					want[d.key()] = true
				}
			}
			got := map[string]bool{}
			var unexpected []frDiff
			for _, d := range diffs {
				got[d.key()] = true
				if !want[d.key()] {
					unexpected = append(unexpected, d)
				}
			}
			knownCount += len(diffs) - len(unexpected)
			for _, d := range unexpected[:min(len(unexpected), 3)] {
				t.Errorf("%s", frDescribe(d, steps))
			}
			if len(unexpected) > 3 {
				t.Errorf("scenario %s: %d more differences", name, len(unexpected)-3)
			}
			for _, d := range known[name].Diffs {
				if !got[d.key()] {
					t.Errorf("stale known difference (the Go output now matches the original, or differs otherwise): %s", frDescribe(d, steps))
				}
			}
		})
	}
	for name := range known {
		if !exists[name] {
			t.Errorf("known-differences.json names scenario %q, which does not exist", name)
		}
	}
	if path := os.Getenv("AUQ_FRAMES_DUMP"); path != "" {
		data, _ := json.MarshalIndent(dump, "", "\t")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("%d scenarios, %d frames compared with the original's goldens, %d known differences asserted", scenarios, frames, knownCount)
}
