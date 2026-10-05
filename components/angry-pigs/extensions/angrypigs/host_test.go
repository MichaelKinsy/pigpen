package angrypigs_test

// Layer-1 cases for the Angry Pigs extension through the fake host. Upstream
// (MichaelKinsy/PiG d86eb93 piglets/standard/extensions/angrypigs) tests the game and
// the component directly and the registration over a raw pipe; these pin the command
// flow (overlay options, result handling, saved high score, no UI) and that merely
// loading the extension activates nothing.

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/angrypigs"
)

func withHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	return home
}

func closesWith(result any) func(string, map[string]any) (map[string]any, string) {
	return func(method string, _ map[string]any) (map[string]any, string) {
		if method == "ui.custom" {
			return map[string]any{"ok": true, "result": result}, ""
		}
		return nil, ""
	}
}

func TestLoadingTheExtensionActivatesNothing(t *testing.T) {
	withHome(t)
	h := StartHost(t, angrypigs.Extension(), HostOptions{})
	// Any event handler, not only the common ones: a bundled game must wait for its command.
	if len(h.handlers) != 0 {
		t.Errorf("registered event handlers %v: a bundled game must wait for its command", h.handlers)
	}
	if len(h.tools) != 0 {
		t.Errorf("tools = %v", h.tools)
	}
	if !h.cmds["angry-pigs"] || len(h.cmds) != 1 {
		t.Errorf("commands = %v", h.cmds)
	}
	if n := len(h.Calls()); n != 0 {
		t.Errorf("%d host calls before any command: %+v", n, h.Calls())
	}
}

func TestCommandOpensAFullTerminalOverlayAndReportsTheScore(t *testing.T) {
	home := withHome(t)
	for _, name := range []string{"angry-pigs"} {
		h := StartHost(t, angrypigs.Extension(), HostOptions{OnCall: closesWith(map[string]any{"score": 1200.0, "highScore": 3000.0})})
		if err := h.Command(name, ""); err != "" {
			t.Fatalf("/%s: %s", name, err)
		}
		custom := h.CallsTo("ui.custom")
		if len(custom) != 1 {
			t.Fatalf("/%s: ui.custom calls = %d", name, len(custom))
		}
		a := custom[0].Args
		if a["title"] != "Angry Pigs" || a["overlay"] != true || a["widthFraction"] != 1.0 || a["heightFraction"] != 1.0 {
			t.Errorf("/%s: overlay options = %v", name, a)
		}
		notes := h.CallsTo("ui.notify")
		if len(notes) != 1 || notes[0].Args["message"] != "Angry Pigs score 1200 · high 3000" || notes[0].Args["level"] != "info" {
			t.Errorf("/%s: notifications = %+v", name, notes)
		}
	}
	data, err := os.ReadFile(filepath.Join(home, "state", "pig-standard", "angrypigs.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved struct{ HighScore int }
	if err := json.Unmarshal(data, &saved); err != nil || saved.HighScore != 3000 {
		t.Fatalf("saved state %q (%v)", data, err)
	}
}

func TestCommandKeepsTheStoredHighScoreWhenTheOverlayReturnsNothing(t *testing.T) {
	home := withHome(t)
	dir := filepath.Join(home, "state", "pig-standard")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "angrypigs.json"), []byte(`{"highScore":4100}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := StartHost(t, angrypigs.Extension(), HostOptions{OnCall: func(method string, _ map[string]any) (map[string]any, string) {
		if method == "ui.custom" {
			return map[string]any{"ok": false}, ""
		}
		return nil, ""
	}})
	if err := h.Command("angry-pigs", ""); err != "" {
		t.Fatal(err)
	}
	notes := h.CallsTo("ui.notify")
	if len(notes) != 1 || !strings.HasSuffix(notes[0].Args["message"].(string), "high 4100") {
		t.Fatalf("notifications = %+v", notes)
	}
}

func TestCommandWithoutUIOpensNoOverlay(t *testing.T) {
	withHome(t)
	off := false
	h := StartHost(t, angrypigs.Extension(), HostOptions{Mode: "print", HasUI: &off})
	if err := h.Command("angry-pigs", ""); err != "" {
		t.Fatal(err)
	}
	if n := len(h.CallsTo("ui.custom")); n != 0 {
		t.Errorf("%d overlays opened without a UI", n)
	}
}

func TestHostErrorFromTheOverlayIsReturned(t *testing.T) {
	withHome(t)
	h := StartHost(t, angrypigs.Extension(), HostOptions{OnCall: func(method string, _ map[string]any) (map[string]any, string) {
		if method == "ui.custom" {
			return nil, "no overlay"
		}
		return nil, ""
	}})
	if err := h.Command("angry-pigs", ""); !strings.Contains(err, "no overlay") {
		t.Fatalf("error = %q", err)
	}
	if n := len(h.CallsTo("ui.notify")); n != 0 {
		t.Errorf("a failed overlay still reported a score (%d)", n)
	}
}

// tickers counts goroutines still running the game's own ticker.
func tickers() int {
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]
	return strings.Count(string(buf), "angrypigs.(*component).run")
}

// A command must leave no ticker behind: with a UI the SDK disposes the component when the
// overlay closes; without a UI ctx.Custom returns at once and never does. Upstream leaked
// the 60 Hz ticker goroutine in that case (approved-correction commit; see port/PORT.md).
func TestCommandLeavesNoTickerRunning(t *testing.T) {
	withHome(t)
	off := false
	for name, opts := range map[string]HostOptions{
		"no UI":   {Mode: "print", HasUI: &off},
		"with UI": {OnCall: closesWith(map[string]any{"score": 1.0, "highScore": 1.0})},
	} {
		before := tickers()
		h := StartHost(t, angrypigs.Extension(), opts)
		if err := h.Command("angry-pigs", ""); err != "" {
			t.Fatalf("%s: %s", name, err)
		}
		if after := tickers(); after != before {
			t.Errorf("%s: %d ticker goroutine(s) still running after the command, want %d", name, after, before)
		}
	}
}

// commandDescriptions reads the register frame the extension sends first, over a raw pipe,
// and returns each command's description (the fake host keeps only names).
func commandDescriptions(t *testing.T, ext *sdk.Extension) map[string]string {
	t.Helper()
	extSide, hostSide := net.Pipe()
	defer func() { _ = hostSide.Close() }()
	go func() { _ = ext.RunWithConn(extSide) }()
	var header [4]byte
	if _, err := io.ReadFull(hostSide, header[:]); err != nil {
		t.Fatal(err)
	}
	frame := make([]byte, binary.BigEndian.Uint32(header[:]))
	if _, err := io.ReadFull(hostSide, frame); err != nil {
		t.Fatal(err)
	}
	var reg struct {
		Register struct {
			Commands []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"commands"`
		} `json:"register"`
	}
	if err := json.Unmarshal(frame, &reg); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, c := range reg.Register.Commands {
		out[c.Name] = c.Description
	}
	return out
}

func TestCommandDescription(t *testing.T) {
	got := commandDescriptions(t, angrypigs.Extension())
	if len(got) != 1 || got["angry-pigs"] != "Play Angry Pigs: launch pigs at the birds." {
		t.Fatalf("commands = %v", got)
	}
}
