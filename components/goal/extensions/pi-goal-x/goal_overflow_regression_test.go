package pi_goal_x

// Twins of tests/overflow-regression.test.ts (pi-tui width handling, here the shared Pigpen widthx package).

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

const (
	orFile     = "overflow-regression"
	orPipe     = "│   "
	orPipeWide = 4
)

// orFixedAddWrappedPipe wraps at (width - pipeWidth) and prepends the pipe prefix to continuations.
func orFixedAddWrappedPipe(content string, safeWidth int) []string {
	wrapWidth := max(1, safeWidth-orPipeWide)
	wrapped := widthx.WrapTextWithAnsi(content, wrapWidth)
	out := make([]string, len(wrapped))
	for i, line := range wrapped {
		if i == 0 {
			out[i] = line
		} else {
			out[i] = orPipe + line
		}
	}
	return out
}

func orFits(t *testing.T, lines []string, limit int, what string) {
	t.Helper()
	for i, line := range lines {
		if w := widthx.VisibleWidth(line); w > limit {
			t.Errorf("%s, line %d: visibleWidth=%d > %d", what, i, w, limit)
		}
	}
}

func TestOverflowRegression(t *testing.T) {
	tw(t, orFile, "addWrappedPipe fix: pipe prefix is exactly 4 visible chars", func(t *testing.T) {
		if w := widthx.VisibleWidth(orPipe); w != 4 {
			t.Errorf("pipe prefix width %d", w)
		}
	})
	tw(t, orFile, "addWrappedPipe fix: no overflow at any width from 20 to 120", func(t *testing.T) {
		content := strings.Repeat("aaabbbcccdddeeefffggghhhiiijjjkkklllmmmnnnooopppqqqrrrssstttuuuvvvwwwxxxyyyzzz ", 20)
		for w := 20; w <= 120; w++ {
			orFits(t, orFixedAddWrappedPipe(content, w), w, fmt.Sprintf("safeWidth=%d", w))
		}
	})
	tw(t, orFile, "addWrappedPipe fix: first line without prefix never exceeds width", func(t *testing.T) {
		for w := 20; w <= 120; w += 10 {
			lines := orFixedAddWrappedPipe(strings.Repeat("a", w*2), w)
			if len(lines) < 2 {
				t.Errorf("safeWidth=%d: %d lines", w, len(lines))
			}
			orFits(t, lines, w, fmt.Sprintf("safeWidth=%d", w))
		}
	})
	tw(t, orFile, "addWrappedPipe fix: styled content with ANSI codes", func(t *testing.T) {
		for w := 30; w <= 120; w += 10 {
			parts := []string{
				"\x1b[1m\x1b[38;2;138;190;183mStyled header\x1b[22m\x1b[39m",
				"\x1b[38;2;128;128;128mMuted description that continues with enough text to trigger wrapping at the given width\x1b[39m",
				"\x1b[38;2;206;145;120mWarning: this is a very long warning message that should wrap properly at narrow terminal widths without causing an overflow crash\x1b[39m",
				"\x1b[38;2;86;156;214mInfo: additional contextual information that might appear in a status display\x1b[39m",
			}
			orFits(t, orFixedAddWrappedPipe(strings.Join(parts, " "), w), w, fmt.Sprintf("safeWidth=%d styled", w))
		}
	})
	tw(t, orFile, "addWrappedPipe fix: content with theme.fg styling patterns", func(t *testing.T) {
		for w := 40; w <= 120; w += 10 {
			for _, styled := range []string{
				"\x1b[38;2;128;128;128mMode:\x1b[39m \x1b[38;2;212;212;212mNormal goal with extra description that pushes this beyond the terminal width at narrow settings\x1b[39m",
				"\x1b[38;2;128;128;128mSome topic text that describes what the user wants to accomplish and may be very long requiring wrapping at narrow widths\x1b[39m",
				"\x1b[38;2;128;128;128m│   Already-prefixed content that should be treated as-is but still needs wrapping and must not overflow even at very narrow terminal width settings\x1b[39m",
			} {
				orFits(t, orFixedAddWrappedPipe(styled, w), w, fmt.Sprintf("safeWidth=%d styled", w))
			}
		}
	})
	tw(t, orFile, "addWrappedPipe fix: CJK wide character content", func(t *testing.T) {
		cjk := strings.Repeat("設定目標並持續追蹤進度以確保代理程式能夠有效執行長期任務。此功能允許使用者定義明確的目標。", 5)
		for w := 30; w <= 120; w += 10 {
			orFits(t, orFixedAddWrappedPipe(cjk, w), w, fmt.Sprintf("safeWidth=%d CJK", w))
		}
	})
	tw(t, orFile, "addWrappedPipe fix: mixed CJK and ASCII content", func(t *testing.T) {
		mixed := strings.Repeat("pi-goal-x 是一個目標管理擴展套件，用於 pi 編碼代理程式。This extension provides long-running goal management with auto-continue and Sisyphus mode support for complex multi-step tasks. ", 5)
		for w := 40; w <= 120; w += 10 {
			orFits(t, orFixedAddWrappedPipe(mixed, w), w, fmt.Sprintf("safeWidth=%d mixed", w))
		}
	})
	tw(t, orFile, "addWrappedPipe fix: single long word (no word breaks)", func(t *testing.T) {
		word := strings.Repeat("Supercalifragilisticexpialidocious", 10)
		for w := 30; w <= 120; w += 10 {
			orFits(t, orFixedAddWrappedPipe(word, w), w, fmt.Sprintf("safeWidth=%d long word", w))
		}
	})
	tw(t, orFile, "addWrappedPipe fix: content at exact wrap boundary", func(t *testing.T) {
		for w := 30; w <= 120; w += 10 {
			exact := strings.Repeat("x", max(1, w-orPipeWide))
			orFits(t, orFixedAddWrappedPipe(strings.Join([]string{exact, exact, exact, exact, exact}, " "), w), w, fmt.Sprintf("safeWidth=%d exact", w))
		}
	})
	tw(t, orFile, "addWrappedPipe fix: content with leading/trailing whitespace", func(t *testing.T) {
		padded := strings.Repeat("   A line with leading spaces and trailing spaces that should still wrap correctly at narrow widths without overflow   ", 3)
		for w := 30; w <= 120; w += 10 {
			orFits(t, orFixedAddWrappedPipe(padded, w), w, fmt.Sprintf("safeWidth=%d whitespace", w))
		}
	})
	tw(t, orFile, "addWrappedPipe fix: content exactly at minimum safeWidth=20", func(t *testing.T) {
		content := strings.Repeat("x", 80) + " " + strings.Repeat("y", 80)
		orFits(t, orFixedAddWrappedPipe(content, 20), 20, "safeWidth=20")
	})
	tw(t, orFile, "truncateToWidth safety net at edge case widths", func(t *testing.T) {
		for w := 1; w <= 120; w++ {
			if got := widthx.VisibleWidth(widthx.TruncateToWidth(strings.Repeat("x", w*4), w, "...", false)); got > w {
				t.Errorf("width=%d produced visibleWidth=%d", w, got)
			}
		}
	})
	tw(t, orFile, "truncateToWidth with ANSI codes at every width", func(t *testing.T) {
		for w := 5; w <= 120; w += 5 {
			styled := "\x1b[1m\x1b[38;2;138;190;183m" + strings.Repeat("a", w*3) + "\x1b[22m\x1b[39m"
			if got := widthx.VisibleWidth(widthx.TruncateToWidth(styled, w, "...", false)); got > w {
				t.Errorf("width=%d produced visibleWidth=%d", w, got)
			}
		}
	})
	tw(t, orFile, "truncateToWidth with CJK characters at every width", func(t *testing.T) {
		for w := 4; w <= 120; w += 4 {
			if got := widthx.VisibleWidth(widthx.TruncateToWidth(strings.Repeat("界", w*2), w, "...", false)); got > w {
				t.Errorf("width=%d produced visibleWidth=%d", w, got)
			}
		}
	})
	tw(t, orFile, "wrapTextWithAnsi at minimum width 1 never overflows", func(t *testing.T) {
		orFits(t, widthx.WrapTextWithAnsi("Hello world this is a test of wrapping at minimum width settings", 1), 1, "width 1")
	})
	tw(t, orFile, "wrapTextWithAnsi at width 0 (clamped edge case)", func(t *testing.T) {
		orFits(t, widthx.WrapTextWithAnsi("Test content at zero width", 0), 5, "width 0")
	})
	tw(t, orFile, "addWrappedPipe fix: full crash scenario simulation", func(t *testing.T) {
		crash := strings.Join([]string{
			"│   Achieve full end-to-end test suite pass on Linux x86_64 with 100% vendor parity — all e2e pass (no skips). The constraints should be exactly those as per the design document and the previous goals.",
			"│   We need to dissassemble the vendor's implementation live, stepping through, to ensure we implement this in full.",
		}, " ")
		orFits(t, orFixedAddWrappedPipe(crash, 109), 109, "width 109")
	})
	tw(t, orFile, "addWrappedPipe fix: all widths from 20 to 120 with crash scenario content", func(t *testing.T) {
		crash := strings.Join([]string{
			"Achieve full end-to-end test suite pass on Linux x86_64 with 100% vendor parity — all e2e pass (no skips). The constraints should be exactly those as per the design document and the previous goals.",
			"We need to dissassemble the vendor's implementation live, stepping through, to ensure we implement this in full.",
		}, " ")
		for w := 20; w <= 120; w++ {
			orFits(t, orFixedAddWrappedPipe(crash, w), w, fmt.Sprintf("safeWidth=%d crash", w))
		}
	})
	tw(t, orFile, "addWrappedPipe fix: empty content produces single empty list", func(t *testing.T) {
		lines := orFixedAddWrappedPipe("", 80)
		if !(len(lines) == 0 || (len(lines) == 1 && lines[0] == "")) {
			t.Errorf("lines %q", lines)
		}
	})
}
