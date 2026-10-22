package stretchtext_test

// Layer 1 of the port: the extension against a fake PiG host over the wire (fakehost_test.go), so each
// test crosses the boundary a real host does. The behaviour of the core (parse, grow, draw, state) is
// proved against the original in diff_test.go; these tests prove what only the adapter does: which
// events, shortcuts and Markdown transformer it registers, when it claims the link scheme, what it asks of
// the host's TUI, and what it leaves behind at shutdown.

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	stretchtext "github.com/MichaelKinsy/pigpen/stretchtext"
)

var escapes = regexp.MustCompile("\x1b\\[[0-9;]*m|\x1b\\]8;;[^\x07]*\x07")

// visible is what the reader sees of drawn text: no colour and no link.
func visible(s string) string { return escapes.ReplaceAllString(s, "") }

const reply = "Short {{answer|a much longer answer}} and {{more|even more detail here}}."

// transform is the host asking the extension's Markdown transformer to draw a message.
func (h *Host) transform(markdown, messageType string) string {
	h.t.Helper()
	args, _ := json.Marshal(map[string]any{"markdown": markdown, "context": map[string]any{"messageType": messageType}})
	result, failure := h.roundTrip(map[string]any{"method": "markdown_transform", "args": json.RawMessage(args)})
	if failure != "" {
		h.t.Fatalf("markdown_transform failed: %s", failure)
	}
	var out string
	if err := json.Unmarshal(result, &out); err != nil {
		h.t.Fatalf("markdown_transform result %s: %v", result, err)
	}
	return out
}

// shortcut is the user pressing a registered shortcut key.
func (h *Host) shortcut(key string) string {
	h.t.Helper()
	_, failure := h.roundTrip(map[string]any{"method": "shortcut", "tool": key})
	return failure
}

// click is a left click on an OSC 8 link of the claimed scheme, as the fullscreen TUI reports it.
func (h *Host) click(url string) {
	scheme, _, _ := strings.Cut(url, ":")
	h.write(map[string]any{"type": "notify", "notify": map[string]any{"method": "link_click", "args": map[string]any{"scheme": scheme, "url": url}}})
}

// theme hands the extension the host's theme, as a theme_change notify does.
func (h *Host) theme() {
	h.write(map[string]any{"type": "notify", "notify": map[string]any{"method": "theme_change", "args": map[string]any{
		"name":       "test",
		"mode":       "truecolor",
		"appearance": "dark",
		"foregrounds": map[string]any{
			"warning": "\x1b[38;2;255;200;0m", "accent": "\x1b[38;2;180;120;255m", "success": "\x1b[38;2;0;200;120m",
			"mdLink": "\x1b[38;2;80;160;255m", "dim": "\x1b[2m", "text": "\x1b[39m",
		},
		"colors": map[string]any{
			"warning": map[string]any{"type": "rgb", "r": 255, "g": 200, "b": 0},
			"accent":  map[string]any{"type": "rgb", "r": 180, "g": 120, "b": 255},
			"success": map[string]any{"type": "rgb", "r": 0, "g": 200, "b": 120},
			"mdLink":  map[string]any{"type": "rgb", "r": 80, "g": 160, "b": 255},
			"text":    map[string]any{"type": "rgb", "r": 220, "g": 220, "b": 220},
		},
	}}})
}

// waitFor polls until cond holds, failing the test after the deadline.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func count(h *Host, method string) func() bool {
	return func() bool { return len(h.CallsTo(method)) > 0 }
}

func startExtension(t *testing.T, opts HostOptions) *Host {
	t.Helper()
	return StartHost(t, stretchtext.Extension(), opts)
}

func TestRegistersTheEventsShortcutsAndTransformerItNeeds(t *testing.T) {
	h := startExtension(t, HostOptions{})
	for _, event := range []string{"before_agent_start", "session_start", "session_shutdown"} {
		if !h.Registered(event) {
			t.Errorf("no handler for %s", event)
		}
	}
	for _, key := range []string{"alt+o", "alt+c"} {
		if failure := h.shortcut(key); failure != "" && strings.Contains(failure, "unknown shortcut") {
			t.Errorf("shortcut %s is not registered: %s", key, failure)
		}
	}
}

func TestTheTransformerDrawsAssistantRepliesAndLeavesTheRestAlone(t *testing.T) {
	h := startExtension(t, HostOptions{})
	drawn := h.transform(reply, "assistant")
	if drawn == reply || strings.Contains(drawn, "{{") {
		t.Fatalf("an assistant reply with stretches was not drawn: %q", drawn)
	}
	if !strings.Contains(drawn, "\x1b]8;;stretchtext:0\x07") {
		t.Errorf("the closed stretch has no OSC 8 link of the claimed scheme: %q", drawn)
	}
	if got := h.transform(reply, "user"); got != reply {
		t.Errorf("a user message was drawn: %q", got)
	}
	const plain = "no stretches here"
	if got := h.transform(plain, "assistant"); got != plain {
		t.Errorf("a reply with no stretch changed: %q", got)
	}
}

func TestInATerminalSessionTheExtensionClaimsItsLinkSchemeAndLeavesItAtShutdown(t *testing.T) {
	h := startExtension(t, HostOptions{})
	h.Fire("session_start", map[string]any{"reason": "startup"})
	claims := h.CallsTo("ui.onLinkClick")
	if len(claims) != 1 || claims[0].Args["scheme"] != "stretchtext" {
		t.Fatalf("link claims = %+v, want one claim of stretchtext", claims)
	}
	h.Fire("session_shutdown", map[string]any{"reason": "quit"})
	waitFor(t, "the scheme to be given back", count(h, "ui.offLinkClick"))
	if off := h.CallsTo("ui.offLinkClick"); len(off) != 1 || off[0].Args["scheme"] != "stretchtext" {
		t.Fatalf("link releases = %+v, want one release of stretchtext", off)
	}
}

// The original attaches in a widget factory, which only a terminal session runs (rpc, json and print never draw it).
func TestOutsideATerminalSessionNothingIsClaimedAndNothingIsAsked(t *testing.T) {
	for _, mode := range []string{"rpc", "json", "print"} {
		t.Run(mode, func(t *testing.T) {
			h := startExtension(t, HostOptions{Mode: mode})
			h.Fire("session_start", map[string]any{"reason": "startup"})
			if failure := h.shortcut("alt+o"); failure != "" {
				t.Fatalf("alt+o failed: %s", failure)
			}
			h.Fire("session_shutdown", map[string]any{"reason": "quit"})
			for _, call := range h.Calls() {
				if strings.HasPrefix(call.Method, "ui.") && call.Method != "ui.notify" {
					t.Errorf("host call %s with no terminal", call.Method)
				}
			}
		})
	}
}

func TestAttachingDrawsTheRepliesShownBeforeItAgain(t *testing.T) {
	h := startExtension(t, HostOptions{})
	h.theme()
	h.Fire("session_start", map[string]any{"reason": "startup"})
	waitFor(t, "the first redraw", count(h, "ui.tui.invalidate"))
	waitFor(t, "the first repaint", count(h, "ui.tui.requestRender"))
	calls := h.Calls()
	var order []string
	for _, c := range calls {
		if c.Method == "ui.tui.invalidate" || c.Method == "ui.tui.requestRender" {
			order = append(order, c.Method)
		}
	}
	if len(order) < 2 || order[0] != "ui.tui.invalidate" || order[1] != "ui.tui.requestRender" {
		t.Fatalf("a redraw is invalidate then requestRender, got %v", order)
	}
	if force := h.CallsTo("ui.tui.requestRender")[0].Args["force"]; force != false {
		t.Errorf("the repaint is not forced, got %v", force)
	}
}

func TestAClickOnAClosedStretchOpensItAndTheTranscriptIsDrawnAgain(t *testing.T) {
	h := startExtension(t, HostOptions{})
	h.theme()
	h.Fire("session_start", map[string]any{"reason": "startup"})
	closed := h.transform(reply, "assistant")
	waitFor(t, "the attach redraw", count(h, "ui.tui.invalidate"))
	before := len(h.CallsTo("ui.tui.invalidate"))

	h.click("stretchtext:0")
	waitFor(t, "a redraw after the click", func() bool { return len(h.CallsTo("ui.tui.invalidate")) > before })
	// It grows word by word over the length of its animation, then shows the whole long form.
	waitFor(t, "the stretch to be open", func() bool {
		shown := visible(h.transform(reply, "assistant"))
		return strings.Contains(shown, "a much longer answer") && strings.Contains(shown, "«")
	})
	if open := h.transform(reply, "assistant"); open == closed {
		t.Fatalf("the opened stretch is not drawn as open, with its « to close: %q", open)
	}
}

func TestAClickOnAnUnknownStretchChangesNothing(t *testing.T) {
	h := startExtension(t, HostOptions{})
	h.Fire("session_start", map[string]any{"reason": "startup"})
	closed := h.transform(reply, "assistant")
	waitFor(t, "the attach redraw", count(h, "ui.tui.invalidate"))
	time.Sleep(100 * time.Millisecond) // drawing the reply for the first time asks for a redraw too
	before := len(h.CallsTo("ui.tui.invalidate"))
	h.click("stretchtext:41")
	h.click("stretchtext:-1")
	h.click("stretchtext:abc")
	time.Sleep(100 * time.Millisecond)
	if after := len(h.CallsTo("ui.tui.invalidate")); after != before {
		t.Errorf("a click on no stretch asked for a redraw (%d -> %d)", before, after)
	}
	if got := h.transform(reply, "assistant"); got != closed {
		t.Errorf("a click on no stretch changed the drawing")
	}
}

func TestBeforeAgentStartPutsTheGuideInTheSystemPromptAndTheReadNoteOnlyOnce(t *testing.T) {
	h := startExtension(t, HostOptions{})
	h.theme()
	h.Fire("session_start", map[string]any{"reason": "startup"})
	start := func() map[string]string {
		raw := h.Fire("before_agent_start", map[string]any{
			"prompt":              "hello",
			"systemPrompt":        "base",
			"systemPromptOptions": map[string]any{"sections": map[string]any{"existing": "kept"}},
		})
		var result struct {
			SystemPromptOptions struct {
				Sections map[string]string `json:"sections"`
			} `json:"_pigPromptOptions"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatalf("before_agent_start result %s: %v", raw, err)
		}
		return result.SystemPromptOptions.Sections
	}
	first := start()
	if first["existing"] != "kept" {
		t.Errorf("the extension dropped another section: %v", first)
	}
	if first["stretchtext"] == "" || !strings.Contains(first["stretchtext"], "{{") {
		t.Errorf("the guide is not in the system prompt: %v", first)
	}
	if _, ok := first["stretchtext_read"]; ok {
		t.Errorf("a read note before the reader opened anything: %v", first)
	}

	h.transform(reply, "assistant")
	h.click("stretchtext:0")
	waitFor(t, "the stretch to open", func() bool { return strings.Contains(visible(h.transform(reply, "assistant")), "a much longer answer") })
	second := start()
	if !strings.Contains(second["stretchtext_read"], "answer") {
		t.Errorf("the note on what the reader opened is missing: %v", second)
	}
	if third := start(); third["stretchtext_read"] != "" {
		t.Errorf("the note came twice: %v", third)
	}
}

func TestTheLetterShortcutOpensAPromptOrSaysThereIsNothingToDo(t *testing.T) {
	h := startExtension(t, HostOptions{OnCall: func(method string, _ map[string]any) (map[string]any, string) {
		if method == "ui.custom" {
			return CustomResult(map[string]any{}), ""
		}
		return nil, ""
	}})
	h.theme()
	h.Fire("session_start", map[string]any{"reason": "startup"})
	if failure := h.shortcut("alt+o"); failure != "" {
		t.Fatalf("alt+o failed: %s", failure)
	}
	notices := h.CallsTo("ui.notify")
	if len(notices) != 1 || notices[0].Args["message"] != "stretchtext: nothing to open" {
		t.Fatalf("with no stretch on the screen alt+o says %+v", notices)
	}
	if len(h.CallsTo("ui.custom")) != 0 {
		t.Fatal("a prompt was opened with nothing to open")
	}

	h.transform(reply, "assistant")
	if failure := h.shortcut("alt+o"); failure != "" {
		t.Fatalf("alt+o failed: %s", failure)
	}
	waitFor(t, "the letter prompt", count(h, "ui.custom"))
	// Nothing is open yet, so alt+c has nothing to close.
	before := len(h.CallsTo("ui.notify"))
	if failure := h.shortcut("alt+c"); failure != "" {
		t.Fatalf("alt+c failed: %s", failure)
	}
	if got := h.CallsTo("ui.notify"); len(got) != before+1 || got[before].Args["message"] != "stretchtext: nothing to close" {
		t.Fatalf("alt+c with nothing open says %+v", got)
	}
}

// Pi emits session_shutdown before the next session's session_start (/new, /resume, /fork, reload): the
// extension must take the new session's context and not keep the old one's worker.
func TestASessionReplacementTakesTheLinkSchemeAgain(t *testing.T) {
	h := startExtension(t, HostOptions{})
	h.theme()
	for _, reason := range []string{"new", "resume", "fork", "reload"} {
		h.Fire("session_start", map[string]any{"reason": "startup"})
		h.Fire("session_shutdown", map[string]any{"reason": reason})
	}
	h.Fire("session_start", map[string]any{"reason": "resume"})
	if got := len(h.CallsTo("ui.onLinkClick")); got != 5 {
		t.Errorf("link claims = %d, want one per session (5)", got)
	}
	waitFor(t, "the releases", func() bool { return len(h.CallsTo("ui.offLinkClick")) == 4 })
}
