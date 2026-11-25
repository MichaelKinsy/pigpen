package tern

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/dirs"
)

// upstream: packages/tui/test/terminal-capabilities.test.ts: "recognizes tern via TERM_PROGRAM despite its plain xterm TERM"
func TestDetectTerminalID_Tern(t *testing.T) {
	env := mapEnv(map[string]string{
		"TERM_PROGRAM": "tern",
		"TERM":         "xterm-256color",
		"COLORTERM":    "truecolor",
	})
	if got := DetectTerminalID(env); got != TermTern {
		t.Fatalf("DetectTerminalID = %q, want %q", got, TermTern)
	}
	if !IsTernProgramEnv(env) {
		t.Fatalf("IsTernProgramEnv = false, want true")
	}
	if got := NotifyProtocolFor(TermTern); got != NotifyOsc99 {
		t.Fatalf("NotifyProtocolFor(tern) = %q, want OSC 99", got)
	}
}

func TestDetectTerminalID_TernWithKittyWindowID(t *testing.T) {
	// Tern panes may export KITTY_WINDOW_ID, so DetectTerminalID reports kitty
	// (matching omp), while IsTernProgramEnv still identifies Tern.
	env := mapEnv(map[string]string{
		"KITTY_WINDOW_ID": "1",
		"TERM_PROGRAM":    "tern",
	})
	if got := DetectTerminalID(env); got != TermKitty {
		t.Fatalf("DetectTerminalID = %q, want %q", got, TermKitty)
	}
	if !IsTernProgramEnv(env) {
		t.Fatalf("IsTernProgramEnv = false, want true")
	}
}

func TestDetectTerminalID_NonTern(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want TerminalID
		prot NotifyProtocol
	}{
		{"ghostty", map[string]string{"TERM_PROGRAM": "ghostty"}, TermGhostty, NotifyOsc9},
		{"wezterm", map[string]string{"WEZTERM_PANE": "1"}, TermWezTerm, NotifyOsc9},
		{"iterm2", map[string]string{"ITERM_SESSION_ID": "abc"}, TermITerm2, NotifyOsc9},
		{"truecolor", map[string]string{"COLORTERM": "truecolor"}, TermTrueColor, NotifyBell},
		{"base", map[string]string{"TERM": "xterm"}, TermBase, NotifyBell},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := mapEnv(tc.env)
			if got := DetectTerminalID(env); got != tc.want {
				t.Fatalf("DetectTerminalID = %q, want %q", got, tc.want)
			}
			if IsTernProgramEnv(env) {
				t.Fatalf("IsTernProgramEnv = true, want false")
			}
			if got := NotifyProtocolFor(tc.want); got != tc.prot {
				t.Fatalf("NotifyProtocolFor(%q) = %q, want %q", tc.want, got, tc.prot)
			}
		})
	}
}

func TestPaneAndBlobDirAndWSLDetection(t *testing.T) {
	env := mapEnv(map[string]string{
		"TERN_PANE_SOCKET": "/tmp/tern.sock",
		"TERN_PANE":        "5",
		"TERN_BLOB_DIR":    "/tmp/blobs",
	})
	sock, block, ok := PaneFromEnv(env)
	if !ok || sock != "/tmp/tern.sock" || block != 5 {
		t.Fatalf("PaneFromEnv = (%q, %d, %v), want (/tmp/tern.sock, 5, true)", sock, block, ok)
	}
	if !HasBlobDirEnv(env) {
		t.Fatalf("HasBlobDirEnv = false, want true")
	}

	// Non-numeric pane id is rejected.
	bad := mapEnv(map[string]string{"TERN_PANE_SOCKET": "/tmp/tern.sock", "TERN_PANE": "abc"})
	if _, _, ok := PaneFromEnv(bad); ok {
		t.Fatalf("PaneFromEnv accepted non-numeric pane id")
	}
	if HasBlobDirEnv(bad) {
		t.Fatalf("HasBlobDirEnv = true when unset")
	}

	// upstream: packages/utils/src/env.ts:50-52 (WSL requires platform == "linux")
	wslEnv := mapEnv(map[string]string{"WSL_DISTRO_NAME": "Ubuntu"})
	if !IsWSLForPlatform("linux", wslEnv) {
		t.Fatalf("IsWSLForPlatform(linux) = false, want true")
	}
	if IsWSLForPlatform("darwin", wslEnv) || IsWSLForPlatform("windows", wslEnv) {
		t.Fatalf("IsWSLForPlatform must be false outside linux")
	}
}

// upstream: packages/coding-agent/test/tools/read-media-path.test.ts: "mirrors Tern's viewer extensions"
func TestShowsMedia(t *testing.T) {
	wantTrue := []string{
		"a.png", "a.JPG", "a.svg", "house.obj", "s.glb", "s.usdz",
		"doc.pdf", "clip.mov", "a.opus", "scene.wrl.gz", "scene.obj.gz",
	}
	for _, name := range wantTrue {
		if !ShowsMedia("/x/" + name) {
			t.Errorf("ShowsMedia(/x/%s) = false, want true", name)
		}
	}

	wantFalse := []string{
		"house.mtl", "a.txt", "a.bmp", "a.mkv", ".png", "png", "a.usdz.gz", "a.gz",
	}
	for _, name := range wantFalse {
		if ShowsMedia("/x/" + name) {
			t.Errorf("ShowsMedia(/x/%s) = true, want false", name)
		}
	}
}

// upstream: packages/coding-agent/test/tools/read-media-path.test.ts: "names whole reads of viewable files, text for the model or not"
// upstream: packages/coding-agent/test/tools/read-media-path.test.ts: "leaves sidecars and partial reads unnamed"
func TestMediaPathForRead(t *testing.T) {
	if got := MediaPathForRead("/tmp/house.obj", ""); got != "/tmp/house.obj" {
		t.Fatalf("MediaPathForRead(obj, '') = %q", got)
	}
	if got := MediaPathForRead("/tmp/logo.svg", "img"); got != "/tmp/logo.svg" {
		t.Fatalf("MediaPathForRead(svg, img) = %q", got)
	}
	if got := MediaPathForRead("/tmp/house.obj", "raw"); got != "/tmp/house.obj" {
		t.Fatalf("MediaPathForRead(obj, raw) = %q", got)
	}
	// upstream: packages/coding-agent/src/tools/read-selector.ts:117 — only `:img`, not `:image`.
	if got := MediaPathForRead("/tmp/logo.svg", "image"); got != "" {
		t.Fatalf("MediaPathForRead(svg, image) = %q, want empty", got)
	}
	if got := MediaPathForRead("/tmp/house.obj", "2-3"); got != "" {
		t.Fatalf("MediaPathForRead(obj, 2-3) = %q, want empty", got)
	}
	if got := MediaPathForRead("/tmp/house.mtl", ""); got != "" {
		t.Fatalf("MediaPathForRead(mtl, '') = %q, want empty", got)
	}
}

// upstream: packages/coding-agent/test/title-card.test.ts: "parseCardTitleReply"
func TestParseCardTitleReply(t *testing.T) {
	nf := func(name string) string {
		switch name {
		case "nf-md-flask":
			return "\U000f0093"
		case "nf-md-text-box":
			return "\U000f021a"
		default:
			return ""
		}
	}
	reply := `<title nf="nf-md-flask" emoji="🧪" code="FLAKY">Fix flaky park tests</title>`

	// upstream: packages/coding-agent/test/title-card.test.ts: "heads the title with the icon the style allows"
	if got, ok := ParseCardTitleReply(reply, TitleIconsNFEmoji, "", nf); !ok || got != "\U000f0093 FLAKY: Fix flaky park tests" {
		t.Fatalf("nf+emoji = (%q, %v)", got, ok)
	}
	if got, ok := ParseCardTitleReply(reply, TitleIconsEmoji, "", nf); !ok || got != "🧪 FLAKY: Fix flaky park tests" {
		t.Fatalf("emoji = (%q, %v)", got, ok)
	}
	if got, ok := ParseCardTitleReply(reply, TitleIconsBoring, "", nf); !ok || got != "Fix flaky park tests" {
		t.Fatalf("boring = (%q, %v)", got, ok)
	}

	// upstream: packages/coding-agent/test/title-card.test.ts: "falls back to the emoji for a Nerd Fonts name the catalog does not know"
	unknownNF := `<title nf="nf-dev-git_cherry_pick" emoji="🍒" code="CHERRY">Review and cherry-pick commit a6a017e</title>`
	if got, ok := ParseCardTitleReply(unknownNF, TitleIconsNFEmoji, "", nf); !ok || got != "🍒 CHERRY: Review and cherry-pick commit a6a017e" {
		t.Fatalf("unknown nf = (%q, %v)", got, ok)
	}

	// upstream: packages/coding-agent/test/title-card.test.ts: "keeps the plain title when the card has no icon the style shows"
	onlyNF := `<title nf="nf-md-flask" code="FLAKY">Fix flaky park tests</title>`
	if got, ok := ParseCardTitleReply(onlyNF, TitleIconsEmoji, "", nf); !ok || got != "Fix flaky park tests" {
		t.Fatalf("only nf under emoji = (%q, %v)", got, ok)
	}

	// upstream: packages/coding-agent/test/title-card.test.ts: "names no title for %s"
	declines := []string{
		"<title/>",
		`<title emoji="🧪" code="X" />`,
		"I'll start by reading the park tests.",
		`<title emoji="🧪" code="FLAKY">Fix flaky park tests`,
		`<title emoji="🧪" code="DOTS">..</title>`,
		`<title>none</title>`,
	}
	for _, d := range declines {
		if got, ok := ParseCardTitleReply(d, TitleIconsEmoji, "", nf); ok {
			t.Errorf("ParseCardTitleReply(%q) = (%q, true), want false", d, got)
		}
	}

	// upstream: packages/coding-agent/src/tiny/text.ts:201 — only "none" is a sentinel; "null" / "untitled" are not rejected.
	if got, ok := ParseCardTitleReply(`<title>Fix null pointer in parser</title>`, TitleIconsEmoji, "", nf); !ok || got != "Fix null pointer in parser" {
		t.Fatalf("title containing null = (%q, %v)", got, ok)
	}
	if got, ok := ParseCardTitleReply(`<title>untitled</title>`, TitleIconsEmoji, "", nf); !ok || got != "untitled" {
		t.Fatalf("untitled = (%q, %v), want accepted", got, ok)
	}

	// upstream: packages/coding-agent/test/title-card.test.ts: "keeps a plain title for %s"
	plainCases := []string{
		`<title nf="nf-md-flask" emoji="🧪" code="FLAKIEST">Fix flaky tests</title>`,
		`<title emoji="🧪" code="FL-KY">Fix flaky tests</title>`,
		`<title nf="nf-md-not_a_glyph" emoji="flask" code="FLAKY">Fix flaky tests</title>`,
		`<title emoji="#️⃣" code="HASH">Fix flaky tests</title>`,
		`<title emoji="🧪🧪" code="FLAKY">Fix flaky tests</title>`,
	}
	for _, pc := range plainCases {
		if got, ok := ParseCardTitleReply(pc, TitleIconsNFEmoji, "", nf); !ok || got != "Fix flaky tests" {
			t.Errorf("ParseCardTitleReply(%q) = (%q, %v), want (Fix flaky tests, true)", pc, got, ok)
		}
	}

	// upstream: packages/coding-agent/test/title-card.test.ts: "repairs a lowercase code and an emoji missing its variation selector"
	if got, ok := ParseCardTitleReply(`<title emoji="🗄" code="seed">Create seed data</title>`, TitleIconsEmoji, "", nf); !ok || got != "🗄️ SEED: Create seed data" {
		t.Fatalf("repair = (%q, %v)", got, ok)
	}

	// upstream: packages/coding-agent/test/title-card.test.ts: "reads the line card form inside a plain tag, but not an ordinary title with a colon"
	if got, ok := ParseCardTitleReply(`<title>🗄️ Z3: Exclude z3 from bun test</title>`, TitleIconsEmoji, "", nf); !ok || got != "🗄️ Z3: Exclude z3 from bun test" {
		t.Fatalf("line form = (%q, %v)", got, ok)
	}
	if got, ok := ParseCardTitleReply(`<title>Fix: the parser</title>`, TitleIconsEmoji, "", nf); !ok || got != "Fix: the parser" {
		t.Fatalf("plain colon = (%q, %v)", got, ok)
	}

	// upstream: packages/coding-agent/test/title-card.test.ts: "reads a code heading the text when the icons are attributes, but not a plain title's leading word"
	if got, ok := ParseCardTitleReply(`<title emoji="⏳">WAIT Observe agent waits</title>`, TitleIconsEmoji, "", nf); !ok || got != "⏳ WAIT: Observe agent waits" {
		t.Fatalf("leading code = (%q, %v)", got, ok)
	}
	if got, ok := ParseCardTitleReply(`<title>CI pipeline fails on arm64</title>`, TitleIconsEmoji, "", nf); !ok || got != "CI pipeline fails on arm64" {
		t.Fatalf("plain CI = (%q, %v)", got, ok)
	}
	if got, ok := ParseCardTitleReply(`<title emoji="📝">A new parser for imports</title>`, TitleIconsEmoji, "", nf); !ok || got != "A new parser for imports" {
		t.Fatalf("leading A = (%q, %v)", got, ok)
	}

	// upstream: packages/coding-agent/test/title-card.test.ts: "reconciles the title's casing against the user's message"
	if got, ok := ParseCardTitleReply(`<title emoji="🦀" code="VMM">Port tinyvmm to arm64</title>`, TitleIconsEmoji, "port TinyVMM to arm64", nf); !ok || got != "🦀 VMM: Port TinyVMM to arm64" {
		t.Fatalf("reconcile casing = (%q, %v)", got, ok)
	}

	// UTF-16 length check: 40 astral emoji + " a" = 82 UTF-16 units (> 80) despite 42 runes.
	longAstral := strings.Repeat("🧪", 40) + " a"
	if _, ok := ParseCardTitleReply("<title>"+longAstral+"</title>", TitleIconsEmoji, "", nf); ok {
		t.Fatalf("expected UTF-16 length > 80 to be rejected")
	}
}

// upstream: packages/coding-agent/test/title-card.test.ts: "splitCardTitle" & "parseCardReply" & "keepTitleCard"
func TestSplitKeepAndParseCardReply(t *testing.T) {
	nf := func(name string) string {
		switch name {
		case "nf-md-flask":
			return "\U000f0093"
		case "nf-md-text-box":
			return "\U000f021a"
		default:
			return ""
		}
	}

	// upstream: packages/coding-agent/test/title-card.test.ts: "takes apart a card title headed by an emoji or a Nerd Fonts glyph"
	parts, ok := SplitCardTitle("🧪 FLAKY: Fix flaky park tests")
	if !ok || parts.Icon != "🧪" || parts.Code != "FLAKY" || parts.Title != "Fix flaky park tests" {
		t.Fatalf("SplitCardTitle = (%+v, %v)", parts, ok)
	}
	nfParts, ok := SplitCardTitle("\U000f0093 FLAKY: Fix flaky park tests")
	if !ok || nfParts.Icon != "\U000f0093" {
		t.Fatalf("SplitCardTitle NF = (%+v, %v)", nfParts, ok)
	}

	// upstream: packages/coding-agent/test/title-card.test.ts: "leaves %s whole"
	for _, plain := range []string{
		"Fix flaky park tests",
		"Fix BUG: the parser",
		"🧪 flaky: Fix flaky park tests",
		"🧪 FLAKIEST: Fix flaky park tests",
	} {
		if _, ok := SplitCardTitle(plain); ok {
			t.Errorf("SplitCardTitle(%q) = true, want false", plain)
		}
	}

	// upstream: packages/coding-agent/test/title-card.test.ts: "picks the icon the style allows"
	if card, ok := ParseCardReply("nf-md-flask 🧪 FLAKY", TitleIconsNFEmoji, nf); !ok || card.Icon != "\U000f0093" || card.Code != "FLAKY" {
		t.Fatalf("ParseCardReply nf+emoji = (%+v, %v)", card, ok)
	}
	if card, ok := ParseCardReply("nf-md-flask 🧪 FLAKY", TitleIconsEmoji, nf); !ok || card.Icon != "🧪" || card.Code != "FLAKY" {
		t.Fatalf("ParseCardReply emoji = (%+v, %v)", card, ok)
	}
	if card, ok := ParseCardReply("nf-md-not-a-glyph 🧪 FLAKY", TitleIconsNFEmoji, nf); !ok || card.Icon != "🧪" || card.Code != "FLAKY" {
		t.Fatalf("ParseCardReply unknown nf = (%+v, %v)", card, ok)
	}
	// upstream: packages/coding-agent/test/title-card.test.ts: "finds a Nerd Fonts name written with dashes for underscores"
	if card, ok := ParseCardReply("nf-md-text-box 📝 BLUR", TitleIconsNFEmoji, nf); !ok || card.Icon != "\U000f021a" || card.Code != "BLUR" {
		t.Fatalf("ParseCardReply dashes = (%+v, %v)", card, ok)
	}
	// upstream: packages/coding-agent/test/title-card.test.ts: "repairs a lowercase code and ignores the title echoed after it"
	if card, ok := ParseCardReply("🗄 seed: Create seed data", TitleIconsEmoji, nf); !ok || card.Icon != "🗄️" || card.Code != "SEED" {
		t.Fatalf("ParseCardReply repair = (%+v, %v)", card, ok)
	}
	// upstream: packages/coding-agent/test/title-card.test.ts: "names no card for %s"
	for _, invalid := range []string{"FLAKY", "🧪", "🧪 FLAKIEST", "Card 🧪 FLAKY"} {
		if _, ok := ParseCardReply(invalid, TitleIconsEmoji, nf); ok {
			t.Errorf("ParseCardReply(%q) = true, want false", invalid)
		}
	}
	// upstream: packages/coding-agent/test/title-card.test.ts: "names no card when the style is boring"
	if _, ok := ParseCardReply("🧪 FLAKY", TitleIconsBoring, nf); ok {
		t.Fatalf("ParseCardReply boring should return false")
	}

	// upstream: packages/coding-agent/test/title-card.test.ts: "heads the new title with the current card, replacing one the new title brings"
	if got := KeepTitleCard("🧪 CACHE: Fix cache writes", "Repair cache"); got != "🧪 CACHE: Repair cache" {
		t.Fatalf("KeepTitleCard plain = %q", got)
	}
	if got := KeepTitleCard("🧪 CACHE: Fix cache writes", "🔥 NEW: Repair cache"); got != "🧪 CACHE: Repair cache" {
		t.Fatalf("KeepTitleCard replace = %q", got)
	}
	// upstream: packages/coding-agent/test/title-card.test.ts: "leaves the new title alone when the current title has no card"
	if got := KeepTitleCard("Fix cache writes", "🔥 NEW: Repair cache"); got != "🔥 NEW: Repair cache" {
		t.Fatalf("KeepTitleCard no current card = %q", got)
	}
	if got := KeepTitleCard("", "Repair cache"); got != "Repair cache" {
		t.Fatalf("KeepTitleCard empty = %q", got)
	}
}

// upstream: packages/coding-agent/test/terminal-title-state.test.ts: "buildTerminalTitleWithState" (all 10 cases)
func TestBuildTitleWithState_UpstreamCases(t *testing.T) {
	noEnv := mapEnv(nil)
	const label = "my-project"

	// upstream: packages/coding-agent/test/terminal-title-state.test.ts: "separates brand and label with '>' when idle/done (your turn)"
	if got := BuildTitleWithStateForPlatform(label, TitleIdle, 0, true, "linux", SpinnerBraille, noEnv, false); got != "π > my-project" {
		t.Fatalf("idle = %q", got)
	}
	// upstream: packages/coding-agent/test/terminal-title-state.test.ts: "separates brand and label with '!' when the agent needs attention"
	if got := BuildTitleWithStateForPlatform(label, TitleAttention, 0, true, "linux", SpinnerBraille, noEnv, false); got != "π ! my-project" {
		t.Fatalf("attention = %q", got)
	}
	// upstream: packages/coding-agent/test/terminal-title-state.test.ts: "animates spinner frames in the separator slot while working outside Windows"
	f0 := BuildTitleWithStateForPlatform(label, TitleWorking, 0, true, "linux", SpinnerBraille, noEnv, false)
	f1 := BuildTitleWithStateForPlatform(label, TitleWorking, 1, true, "linux", SpinnerBraille, noEnv, false)
	if f0 != "π ⠋ my-project" || f1 != "π ⠙ my-project" {
		t.Fatalf("working frames = (%q, %q)", f0, f1)
	}
	if wrapped := BuildTitleWithStateForPlatform(label, TitleWorking, 9999, true, "linux", SpinnerBraille, noEnv, false); !strings.HasPrefix(wrapped, "π ") || !strings.HasSuffix(wrapped, " my-project") {
		t.Fatalf("wrapped frame = %q", wrapped)
	}
	// upstream: packages/coding-agent/test/terminal-title-state.test.ts: "animates spinner frames in the separator slot while working on Windows"
	if got := BuildTitleWithStateForPlatform(label, TitleWorking, 0, true, "windows", SpinnerBraille, noEnv, false); got != "π ⠋ my-project" {
		t.Fatalf("win frame 0 = %q", got)
	}
	if got := BuildTitleWithStateForPlatform("", TitleWorking, 1, true, "windows", SpinnerBraille, noEnv, false); got != "π ⠙" {
		t.Fatalf("win empty label = %q", got)
	}
	// upstream: packages/coding-agent/test/terminal-title-state.test.ts: "keeps the state visible as a trailing separator when there is no label"
	if got := BuildTitleWithStateForPlatform("", TitleIdle, 0, true, "linux", SpinnerBraille, noEnv, false); got != "π >" {
		t.Fatalf("empty idle = %q", got)
	}
	if got := BuildTitleWithStateForPlatform("", TitleAttention, 0, true, "linux", SpinnerBraille, noEnv, false); got != "π !" {
		t.Fatalf("empty attention = %q", got)
	}
	if got := BuildTitleWithStateForPlatform("", TitleWorking, 0, true, "linux", SpinnerBraille, noEnv, false); got != "π ⠋" {
		t.Fatalf("empty working = %q", got)
	}
	// upstream: packages/coding-agent/test/terminal-title-state.test.ts: "renders the pre-state `π: label` layout when disabled, regardless of state"
	for _, st := range []TitleState{TitleWorking, TitleIdle, TitleAttention} {
		if got := BuildTitleWithStateForPlatform(label, st, 3, false, "linux", SpinnerBraille, noEnv, false); got != "π: my-project" {
			t.Fatalf("disabled %s = %q", st, got)
		}
	}
	if got := BuildTitleWithStateForPlatform("", TitleIdle, 0, false, "linux", SpinnerBraille, noEnv, false); got != "π" {
		t.Fatalf("disabled empty = %q", got)
	}
	// upstream: packages/coding-agent/test/terminal-title-state.test.ts: "cycles the dots and line glyph sets while working"
	if got := BuildTitleWithStateForPlatform(label, TitleWorking, 0, true, "linux", SpinnerDots, noEnv, false); got != "π ⠁ my-project" {
		t.Fatalf("dots 0 = %q", got)
	}
	if got := BuildTitleWithStateForPlatform(label, TitleWorking, 1, true, "linux", SpinnerDots, noEnv, false); got != "π ⠂ my-project" {
		t.Fatalf("dots 1 = %q", got)
	}
	if got := BuildTitleWithStateForPlatform(label, TitleWorking, 0, true, "linux", SpinnerLine, noEnv, false); got != "π - my-project" {
		t.Fatalf("line 0 = %q", got)
	}
	if got := BuildTitleWithStateForPlatform(label, TitleWorking, 1, true, "linux", SpinnerLine, noEnv, false); got != `π \ my-project` {
		t.Fatalf("line 1 = %q", got)
	}
	// upstream: packages/coding-agent/test/terminal-title-state.test.ts: "cycles the pulse glyph set while working"
	if got := BuildTitleWithStateForPlatform(label, TitleWorking, 0, true, "linux", SpinnerPulse, noEnv, false); got != "π ○ my-project" {
		t.Fatalf("pulse 0 = %q", got)
	}
	if got := BuildTitleWithStateForPlatform(label, TitleWorking, 4, true, "linux", SpinnerPulse, noEnv, false); got != "π ● my-project" {
		t.Fatalf("pulse 4 = %q", got)
	}
	if got := BuildTitleWithStateForPlatform(label, TitleWorking, 8, true, "linux", SpinnerPulse, noEnv, false); got != "π ○ my-project" {
		t.Fatalf("pulse 8 = %q", got)
	}
	// upstream: packages/coding-agent/test/terminal-title-state.test.ts: "keeps a static colon under WSL regardless of style"
	wslEnv := mapEnv(map[string]string{"WSL_DISTRO_NAME": "Ubuntu"})
	if got := BuildTitleWithStateForPlatform(label, TitleWorking, 0, true, "linux", SpinnerBraille, wslEnv, false); got != "π : my-project" {
		t.Fatalf("wsl braille = %q", got)
	}
	if got := BuildTitleWithStateForPlatform(label, TitleWorking, 1, true, "linux", SpinnerDots, wslEnv, false); got != "π : my-project" {
		t.Fatalf("wsl dots = %q", got)
	}
	if got := BuildTitleWithStateForPlatform("", TitleWorking, 1, true, "linux", SpinnerLine, wslEnv, false); got != "π :" {
		t.Fatalf("wsl empty = %q", got)
	}

	// Native Tern title: no brand, no state separator, defaults to "omp" when unnamed.
	if got := BuildNativeTitle("", 0); got != "omp" {
		t.Fatalf("native empty = %q", got)
	}
	if got := BuildNativeTitle("🧪 FLAKY: Fix tests", 42); got != "🧪 FLAKY: Fix tests · #42" {
		t.Fatalf("native with PR = %q", got)
	}
}

// helper for testing TitleController spinner ticks, latch, style, and interval deterministically.
func newCapturedTitleController(t *testing.T, lookup EnvLookup) (*TitleController, *[]string, *sync.Mutex, chan string) {
	t.Helper()
	tc := NewTitleController(lookup)
	tc.goos = "linux"
	tc.interval = 10 * time.Millisecond
	var mu sync.Mutex
	var titles []string
	ch := make(chan string, 64)
	tc.setTitleFn = func(title string) {
		mu.Lock()
		titles = append(titles, title)
		mu.Unlock()
		select {
		case ch <- title:
		default:
		}
	}
	t.Cleanup(tc.Dispose)
	return tc, &titles, &mu, ch
}

func waitForTitles(t *testing.T, titles *[]string, mu *sync.Mutex, ch <-chan string, minCount int, timeout time.Duration) []string {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		mu.Lock()
		if len(*titles) >= minCount {
			cp := append([]string(nil), (*titles)...)
			mu.Unlock()
			return cp
		}
		mu.Unlock()
		select {
		case <-ch:
		case <-timer.C:
			mu.Lock()
			cp := append([]string(nil), (*titles)...)
			mu.Unlock()
			return cp
		}
	}
}

// upstream: packages/coding-agent/test/terminal-title-state.test.ts:
// "stops the spinner so no further OSC-title write fires on a tick after dispose"
// "latches: a run-state change after dispose cannot re-arm the spinner"
// "keeps a routine session title update from releasing the latch"
// "releases the latch and re-arms a live spinner when the terminal is claimed again"
func TestTitleController_SpinnerDisposeAndLatch(t *testing.T) {
	tc, titles, mu, ch := newCapturedTitleController(t, mapEnv(map[string]string{"TERM_PROGRAM": "ghostty"}))
	tc.sessionName = "my-project"

	// Enter working -> spinner ticks.
	tc.mu.Lock()
	tc.flowState = TitleWorking
	tc.emitLocked(false)
	tc.mu.Unlock()
	tc.startSpinner()

	got := waitForTitles(t, titles, mu, ch, 3, time.Second)
	if len(got) < 3 {
		t.Fatalf("expected >= 3 spinner titles while live, got %q", got)
	}
	if got[0] != "π ⠋ my-project" || got[1] != "π ⠙ my-project" {
		t.Fatalf("unexpected initial spinner frames: %q", got[:2])
	}

	// Capture spinnerDoneCh before Dispose so we can verify goroutine exit deterministically.
	tc.mu.Lock()
	doneCh := tc.spinnerDoneCh
	tc.mu.Unlock()
	if doneCh == nil {
		t.Fatalf("expected active spinnerDoneCh before Dispose")
	}

	// Dispose blocks until the spinner goroutine exits; doneCh must be closed on return.
	tc.Dispose()
	select {
	case <-doneCh:
	default:
		t.Fatalf("spinner goroutine did not exit before Dispose returned")
	}
	if tc.SpinnerRunning() {
		t.Fatalf("SpinnerRunning = true after Dispose")
	}
	// Drain any buffered notifications emitted prior to Dispose; nothing new may arrive.
	for {
		select {
		case <-ch:
		default:
			goto drained
		}
	}
drained:
	mu.Lock()
	*titles = nil
	mu.Unlock()

	// State changes and setters after Dispose remain latched off and emit nothing.
	tc.SetPullRequest(99)
	tc.SetStateEnabled(false)
	tc.SetStateEnabled(true)
	tc.SetSpinnerStyle("line")
	tc.SetSpinnerInterval(5 * time.Millisecond)
	if tc.SpinnerRunning() {
		t.Fatalf("spinner re-armed while disposed")
	}
	select {
	case late := <-ch:
		t.Fatalf("unexpected title emitted after Dispose: %q", late)
	default:
	}

	// Interval 0 pins static `:` and schedules no goroutine.
	tc.mu.Lock()
	tc.disposed = false
	tc.mu.Unlock()
	tc.SetSpinnerInterval(0)
	if tc.SpinnerRunning() {
		t.Fatalf("interval 0 must not start spinner goroutine")
	}
	if got := tc.CurrentTitle(); got != "π : my-project" {
		t.Fatalf("interval 0 title = %q, want %q", got, "π : my-project")
	}
}

// upstream: packages/coding-agent/test/terminal-title-state.test.ts:
// "emits the selected glyph set on the next spinner tick"
// "falls back to braille for an unknown style"
// "emits pulse frames on live ticks"
func TestTitleController_SpinnerStylesLiveTicks(t *testing.T) {
	tc, titles, mu, ch := newCapturedTitleController(t, mapEnv(map[string]string{"TERM_PROGRAM": "ghostty"}))
	tc.sessionName = "my-project"
	tc.mu.Lock()
	tc.flowState = TitleWorking
	tc.mu.Unlock()

	// Line style ticks.
	tc.SetSpinnerStyle("line")
	got := waitForTitles(t, titles, mu, ch, 3, time.Second)
	if len(got) < 3 {
		t.Fatalf("expected >= 3 line spinner ticks, got %q", got)
	}
	if got[0] != "π - my-project" || got[1] != `π \ my-project` {
		t.Fatalf("line frames = %q", got[:2])
	}

	// Unknown style falls back to braille.
	mu.Lock()
	*titles = nil
	mu.Unlock()
	tc.SetSpinnerStyle("nope")
	got = waitForTitles(t, titles, mu, ch, 2, time.Second)
	if len(got) < 2 || got[0] != "π ⠋ my-project" {
		t.Fatalf("unknown style fallback frames = %q", got)
	}

	// Pulse style ticks.
	mu.Lock()
	*titles = nil
	mu.Unlock()
	tc.SetSpinnerStyle("pulse")
	got = waitForTitles(t, titles, mu, ch, 2, time.Second)
	if len(got) < 2 || got[0] != "π ○ my-project" {
		t.Fatalf("pulse frames = %q", got)
	}
}

func TestExtensionLifecycle_TernUnnamedUsesOmpAndClaimsOwner(t *testing.T) {
	var claimedKey, claimedVal string
	ext := sdk.New("tern-unnamed-test")
	registerWithOptions(ext, registerOptions{
		lookup:    mapEnv(map[string]string{"TERM_PROGRAM": "tern"}),
		notifyCfg: DefaultNotifyConfig(),
		claimPID:  4242,
		setEnv: func(k, v string) error {
			claimedKey, claimedVal = k, v
			return nil
		},
	})

	h := startHost(t, ext, hostOptions{
		Cwd:         t.TempDir(),
		SessionName: "", // unnamed session
	})

	h.emitEvent(sdk.EventSessionStart, nil)
	if claimedKey != terminalOwnerEnv || claimedVal != "4242" {
		t.Fatalf("terminal owner claim = (%q, %q), want (%q, 4242)", claimedKey, claimedVal, terminalOwnerEnv)
	}

	// Unnamed session in Tern MUST use "omp", never the cwd basename.
	titles := h.callsTo("ui.setTitle")
	if len(titles) != 1 || titles[0].Args["title"] != "omp" {
		t.Fatalf("Tern unnamed titles = %+v, want single 'omp'", titles)
	}

	// Non-tick re-emit after host overwrite (same title must NOT be swallowed).
	h.emitEvent(sdk.EventSessionInfoChanged, nil)
	if got := len(h.callsTo("ui.setTitle")); got != 2 {
		t.Fatalf("expected non-tick re-emit after session_info_changed, got %d calls", got)
	}

	// Completion notifies with Bell; error turn is silent by default (`error.notify=off`).
	h.emitEvent(sdk.EventAgentEnd, map[string]any{
		"messages": []any{map[string]any{"role": "assistant", "stopReason": "stop"}},
	})
	h.emitEvent(sdk.EventAgentEnd, map[string]any{
		"messages": []any{map[string]any{"role": "assistant", "stopReason": "error"}},
	})
	if bells := h.callsTo("ui.bell"); len(bells) != 1 {
		t.Fatalf("ui.bell calls = %d, want 1 (completion only, error.notify defaults off)", len(bells))
	}

	h.emitEvent(sdk.EventSessionShutdown, nil)
}

func TestExtensionLifecycle_NonTernDialogHoldAndAskBell(t *testing.T) {
	tc := NewTitleController(mapEnv(map[string]string{"TERM_PROGRAM": "ghostty"}))
	tc.interval = 0 // static `:` so no timer races
	ext := sdk.New("non-tern-test")
	registerWithOptions(ext, registerOptions{
		lookup:    mapEnv(map[string]string{"TERM_PROGRAM": "ghostty"}),
		notifyCfg: DefaultNotifyConfig(),
		titles:    tc,
	})

	h := startHost(t, ext, hostOptions{
		Cwd:         t.TempDir(),
		SessionName: "my-session",
	})

	h.emitEvent(sdk.EventSessionStart, nil)
	h.emitEvent(sdk.EventAgentStart, nil)
	// `ask` tool starts: rings once.
	h.emitEvent(sdk.EventToolExecutionStart, map[string]any{"toolName": "ask"})
	// Multiple dialogs within one ask call: must NOT ring again on ui_prompt_start.
	h.emitEvent(sdk.EventUIPromptStart, nil)
	h.emitEvent(sdk.EventUIPromptStart, nil)
	// agent_end arriving while dialog is open MUST keep `!` attention state.
	h.emitEvent(sdk.EventAgentEnd, map[string]any{
		"messages": []any{map[string]any{"role": "assistant", "stopReason": "aborted"}},
	})
	if got := tc.CurrentTitle(); got != "π ! my-session" {
		t.Fatalf("title during agent_end under open dialog = %q, want %q", got, "π ! my-session")
	}
	// Closing one of two dialogs still keeps `!`.
	h.emitEvent(sdk.EventUIPromptEnd, nil)
	if got := tc.CurrentTitle(); got != "π ! my-session" {
		t.Fatalf("title with 1 dialog still open = %q, want %q", got, "π ! my-session")
	}
	// Closing the last dialog restores underlying flow state (`idle` -> `>`).
	h.emitEvent(sdk.EventUIPromptEnd, nil)
	if got := tc.CurrentTitle(); got != "π > my-session" {
		t.Fatalf("title after last dialog closed = %q, want %q", got, "π > my-session")
	}

	// Only 1 bell from `tool_execution_start(ask)`; none from the 2 `ui_prompt_start`s or aborted `agent_end`.
	if bells := h.callsTo("ui.bell"); len(bells) != 1 {
		t.Fatalf("ui.bell calls = %d, want 1", len(bells))
	}

	// A stale open dialog hold is cleared on a fresh session_start (`Init` resets dialogDepth).
	// upstream: packages/coding-agent/src/utils/run-status.ts:204
	h.emitEvent(sdk.EventUIPromptStart, nil)
	if got := tc.CurrentTitle(); got != "π ! my-session" {
		t.Fatalf("title under open dialog = %q, want %q", got, "π ! my-session")
	}
	h.emitEvent(sdk.EventSessionStart, nil)
	if got := tc.CurrentTitle(); got != "π > my-session" {
		t.Fatalf("title after fresh session_start = %q, want %q", got, "π > my-session")
	}

	h.emitEvent(sdk.EventSessionShutdown, nil)
}

func TestExtensionLifecycle_RPCModeEmitsNoTitleOrBell(t *testing.T) {
	ext := sdk.New("rpc-mode-test")
	registerWithOptions(ext, registerOptions{
		lookup:    mapEnv(map[string]string{"TERM_PROGRAM": "ghostty"}),
		notifyCfg: DefaultNotifyConfig(),
	})

	hasUI := true
	h := startHost(t, ext, hostOptions{
		Mode:        "rpc",
		HasUI:       &hasUI,
		SessionName: "rpc-session",
	})

	h.emitEvent(sdk.EventSessionStart, nil)
	h.emitEvent(sdk.EventAgentStart, nil)
	h.emitEvent(sdk.EventToolExecutionStart, map[string]any{"toolName": "ask"})
	h.emitEvent(sdk.EventAgentEnd, map[string]any{
		"messages": []any{map[string]any{"role": "assistant", "stopReason": "stop"}},
	})
	h.emitEvent(sdk.EventSessionShutdown, nil)

	if titles := h.callsTo("ui.setTitle"); len(titles) != 0 {
		t.Fatalf("RPC mode emitted %d ui.setTitle calls, want 0", len(titles))
	}
	if bells := h.callsTo("ui.bell"); len(bells) != 0 {
		t.Fatalf("RPC mode emitted %d ui.bell calls, want 0", len(bells))
	}
}

func TestNotificationSuppressedByEnv(t *testing.T) {
	ext := sdk.New("suppress-test")
	registerWithOptions(ext, registerOptions{
		lookup: mapEnv(map[string]string{
			"TERM_PROGRAM":     "tern",
			"PI_NOTIFICATIONS": "off",
		}),
		notifyCfg: DefaultNotifyConfig(),
	})
	h := startHost(t, ext, hostOptions{SessionName: "s"})
	h.emitEvent(sdk.EventAgentEnd, map[string]any{
		"messages": []any{map[string]any{"role": "assistant", "stopReason": "stop"}},
	})
	h.emitEvent(sdk.EventToolExecutionStart, map[string]any{"toolName": "ask"})
	if bells := h.callsTo("ui.bell"); len(bells) != 0 {
		t.Fatalf("ui.bell calls = %d, want 0 when PI_NOTIFICATIONS=off", len(bells))
	}
}

func TestToolResultHookAttachesMediaPath(t *testing.T) {
	ext := sdk.New("media-hook-test")
	registerWithOptions(ext, registerOptions{
		lookup:    mapEnv(map[string]string{"TERM_PROGRAM": "tern"}),
		notifyCfg: DefaultNotifyConfig(),
	})
	cwd := t.TempDir()
	writeFixture := func(rel string) string {
		t.Helper()
		full := filepath.Join(cwd, rel)
		if err := os.WriteFile(full, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		return full
	}
	houseObj := writeFixture("house.obj")
	logoSVG := writeFixture("logo.svg")
	brickPNG := writeFixture("brick.png")
	modelGLB := writeFixture("model.glb")
	colonFile := writeFixture("shot:1.png")
	chartSVG := writeFixture("chart.svg")
	_ = writeFixture("house.mtl")

	// A directory whose name has a media extension (`icons.svg`) must NOT get a mediaPath.
	iconsDir := filepath.Join(cwd, "icons.svg")
	if err := os.Mkdir(iconsDir, 0o700); err != nil {
		t.Fatal(err)
	}

	h := startHost(t, ext, hostOptions{Cwd: cwd})

	checkMediaPath := func(name string, event map[string]any, want string) {
		t.Helper()
		raw, failure := h.emitEvent(sdk.EventToolResult, event)
		if failure != "" {
			t.Fatalf("%s: tool_result error: %s", name, failure)
		}
		if want == "" {
			if strings.Contains(string(raw), "mediaPath") {
				t.Fatalf("%s: unexpectedly attached mediaPath: %s", name, raw)
			}
			return
		}
		var res struct {
			ToolResult struct {
				Details map[string]any `json:"details"`
			} `json:"_pigToolResultResult"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			t.Fatalf("%s: unmarshal %s: %v", name, raw, err)
		}
		if got, _ := res.ToolResult.Details["mediaPath"].(string); got != want {
			t.Fatalf("%s: details.mediaPath = %q, want %q (raw=%s)", name, got, want, raw)
		}
	}

	// Whole-file relative read preserves existing details.
	checkMediaPath("relative obj", map[string]any{
		"toolCallId": "c1", "toolName": "read",
		"input": map[string]any{"path": "house.obj"}, "details": map[string]any{"existing": 1}, "isError": false,
	}, dirs.StandardizeMacOSPath(houseObj))

	// Prefers details.resolvedPath when the read tool provides it (covers internal URLs, preserving its spelling).
	checkMediaPath("resolvedPath from internal URL", map[string]any{
		"toolCallId": "c2", "toolName": "read",
		"input":   map[string]any{"path": "artifact://1"},
		"details": map[string]any{"resolvedPath": chartSVG}, "isError": false,
	}, chartSVG)
	// Quoted, @-prefixed, and file:// paths expand the same way internal/files does.
	checkMediaPath("quoted path", map[string]any{
		"toolCallId": "c3", "toolName": "read",
		"input": map[string]any{"path": `"logo.svg":img`}, "isError": false,
	}, dirs.StandardizeMacOSPath(logoSVG))
	checkMediaPath("@-prefixed path", map[string]any{
		"toolCallId": "c4", "toolName": "read",
		"input": map[string]any{"path": "@brick.png"}, "isError": false,
	}, dirs.StandardizeMacOSPath(brickPNG))
	checkMediaPath("file:// URL", map[string]any{
		"toolCallId": "c5", "toolName": "read",
		"input": map[string]any{"path": "file://" + filepath.ToSlash(modelGLB)}, "isError": false,
	}, dirs.StandardizeMacOSPath(modelGLB))

	// Literal file with colon in its name is preserved.
	checkMediaPath("literal colon filename", map[string]any{
		"toolCallId": "c6", "toolName": "read",
		"input": map[string]any{"path": "shot:1.png"}, "isError": false,
	}, dirs.StandardizeMacOSPath(colonFile))
	// Directory named like a media file (`icons.svg`) must NOT attach mediaPath
	// (both via `details.isDirectory` and via `os.Stat` regular-file check).
	checkMediaPath("directory with isDirectory flag", map[string]any{
		"toolCallId": "c7", "toolName": "read",
		"input":   map[string]any{"path": "icons.svg"},
		"details": map[string]any{"isDirectory": true, "resolvedPath": iconsDir}, "isError": false,
	}, "")
	checkMediaPath("directory without isDirectory flag", map[string]any{
		"toolCallId": "c8", "toolName": "read",
		"input": map[string]any{"path": "icons.svg"}, "isError": false,
	}, "")

	// Non-existent media path must NOT attach mediaPath.
	checkMediaPath("non-existent media file", map[string]any{
		"toolCallId": "c9", "toolName": "read",
		"input": map[string]any{"path": "file://" + filepath.ToSlash(filepath.Join(cwd, "missing.glb"))}, "isError": false,
	}, "")

	// Partial line-range read and sidecars must NOT attach mediaPath.
	checkMediaPath("partial range", map[string]any{
		"toolCallId": "c10", "toolName": "read",
		"input": map[string]any{"path": "house.obj:2-3"}, "isError": false,
	}, "")
	checkMediaPath("sidecar mtl", map[string]any{
		"toolCallId": "c11", "toolName": "read",
		"input": map[string]any{"path": "house.mtl"}, "isError": false,
	}, "")
}

func TestSettingHookOverridesTitleAndNotify(t *testing.T) {
	cfg := map[string]any{
		"tui.titleState":      false,
		"tui.titleSpinner":    "line",
		"tui.spinnerInterval": 0,
		"completion.notify":   "off",
		"error.notify":        "on",
		"ask.notify":          "off",
	}
	ext := sdk.New("setting-hook-test")
	registerWithOptions(ext, registerOptions{
		lookup:    mapEnv(map[string]string{"TERM_PROGRAM": "ghostty"}),
		notifyCfg: DefaultNotifyConfig(),
		settingFn: func(_ sdk.Context, key string) any { return cfg[key] },
	})
	h := startHost(t, ext, hostOptions{SessionName: "proj"})

	h.emitEvent(sdk.EventSessionStart, nil)
	h.emitEvent(sdk.EventAgentStart, nil)
	h.emitEvent(sdk.EventToolExecutionStart, map[string]any{"toolName": "ask"})
	h.emitEvent(sdk.EventAgentEnd, map[string]any{
		"messages": []any{map[string]any{"role": "assistant", "stopReason": "stop"}},
	})
	h.emitEvent(sdk.EventAgentEnd, map[string]any{
		"messages": []any{map[string]any{"role": "assistant", "stopReason": "error"}},
	})
	h.emitEvent(sdk.EventSessionShutdown, nil)

	// tui.titleState=false -> classic "π: proj" layout (no ">" or spinner).
	titles := h.callsTo("ui.setTitle")
	if len(titles) == 0 || titles[0].Args["title"] != "π: proj" {
		t.Fatalf("titles with tui.titleState=false = %+v, want 'π: proj'", titles)
	}
	// completion.notify=off, ask.notify=off, error.notify=on -> exactly 1 bell (error turn only).
	if bells := h.callsTo("ui.bell"); len(bells) != 1 {
		t.Fatalf("ui.bell calls = %d, want 1 (error turn only)", len(bells))
	}
}

func TestNoCollidingForkCommandRegistered(t *testing.T) {
	ext := sdk.New("no-fork-collision")
	Register(ext)
	h := startHost(t, ext, hostOptions{})
	if h.commands["fork"] {
		t.Fatalf("Register must not register /fork (conflicts with PiG built-in /fork)")
	}
}

// upstream: packages/utils/src/dirs.ts:148-158 (standardizeMacOSPath),
// packages/coding-agent/src/tools/read.ts:2611
func TestToolResultHookMediaPathStandardizesMacOSPath(t *testing.T) {
	rawDir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(rawDir); err == nil {
		rawDir = resolved
	}
	wantDir := dirs.StandardizeMacOSPath(rawDir)
	if runtime.GOOS == "darwin" && strings.HasPrefix(rawDir, "/private/") && strings.HasPrefix(wantDir, "/private/") {
		t.Fatalf("expected darwin TempDir %q to strip /private, got %q", rawDir, wantDir)
	}
	if runtime.GOOS != "darwin" && wantDir != rawDir {
		t.Fatalf("expected non-darwin path unchanged: got %q, want %q", wantDir, rawDir)
	}

	imgPath := filepath.Join(rawDir, "sample.png")
	if err := os.WriteFile(imgPath, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantImg := filepath.Join(wantDir, "sample.png")

	ext := sdk.New("media-hook-macos-test")
	registerWithOptions(ext, registerOptions{lookup: mapEnv(nil), notifyCfg: DefaultNotifyConfig()})
	h := startHost(t, ext, hostOptions{Cwd: rawDir})

	raw, failure := h.emitEvent(sdk.EventToolResult, map[string]any{
		"toolCallId": "c-macos",
		"toolName":   "read",
		"input":      map[string]any{"path": "sample.png"},
		"details":    map[string]any{},
		"isError":    false,
	})
	if failure != "" {
		t.Fatalf("tool_result error: %s", failure)
	}
	var res struct {
		ToolResult struct {
			Details map[string]any `json:"details"`
		} `json:"_pigToolResultResult"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}
	if got, _ := res.ToolResult.Details["mediaPath"].(string); got != wantImg {
		t.Fatalf("details.mediaPath = %q, want %q", got, wantImg)
	}

	// When details.resolvedPath is already provided (e.g. local:// temp-dir file), preserve its spelling.
	rawLocal, failure := h.emitEvent(sdk.EventToolResult, map[string]any{
		"toolCallId": "c-macos-local",
		"toolName":   "read",
		"input":      map[string]any{"path": "local://sample.png"},
		"details":    map[string]any{"resolvedPath": imgPath},
		"isError":    false,
	})
	if failure != "" {
		t.Fatalf("tool_result local error: %s", failure)
	}
	var resLocal struct {
		ToolResult struct {
			Details map[string]any `json:"details"`
		} `json:"_pigToolResultResult"`
	}
	if err := json.Unmarshal(rawLocal, &resLocal); err != nil {
		t.Fatalf("unmarshal %s: %v", rawLocal, err)
	}
	if got, _ := resLocal.ToolResult.Details["mediaPath"].(string); got != imgPath {
		t.Fatalf("local:// details.mediaPath = %q, want %q", got, imgPath)
	}
}
