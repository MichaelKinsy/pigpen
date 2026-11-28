package ansi

import (
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func TestDiffWordsTokenizationAndSemantics(t *testing.T) {
	t.Run("simple word replacement", func(t *testing.T) {
		got := diffWords("status: draft", "status: verified")
		want := []wordChange{
			{value: "status: "},
			{value: "draft", removed: true},
			{value: "verified", added: true},
		}
		if len(got) != len(want) {
			t.Fatalf("got %#v, want %#v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("part[%d] = %#v, want %#v", i, got[i], want[i])
			}
		}
	})

	t.Run("punctuation and whitespace grouping", func(t *testing.T) {
		got := diffWords("foo(a, b)", "foo(a, c)")
		var rem, add, common strings.Builder
		for _, p := range got {
			switch {
			case p.removed:
				rem.WriteString(p.value)
			case p.added:
				add.WriteString(p.value)
			default:
				common.WriteString(p.value)
			}
		}
		if rem.String() != "b" || add.String() != "c" {
			t.Fatalf("expected removed=%q added=%q, got removed=%q added=%q (%#v)", "b", "c", rem.String(), add.String(), got)
		}
		if common.String() != "foo(a, )" {
			t.Fatalf("expected common=%q, got %q", "foo(a, )", common.String())
		}
	})

	t.Run("pure insertion and deletion", func(t *testing.T) {
		ins := diffWords("hello world", "hello brave world")
		var added []string
		for _, p := range ins {
			if p.added {
				added = append(added, p.value)
			}
		}
		if len(added) != 1 || strings.TrimSpace(added[0]) != "brave" {
			t.Fatalf("expected inserted 'brave', got %#v", ins)
		}

		del := diffWords("hello brave world", "hello world")
		var removed []string
		for _, p := range del {
			if p.removed {
				removed = append(removed, p.value)
			}
		}
		if len(removed) != 1 || strings.TrimSpace(removed[0]) != "brave" {
			t.Fatalf("expected removed 'brave', got %#v", del)
		}
	})
}

func TestRenderIntraLineDiffLeadingWhitespaceNotInversed(t *testing.T) {
	p := newPalette(sdk.Context{})
	rem, add := p.renderIntraLineDiff("    alpha beta", "    gamma beta")

	if !strings.HasPrefix(rem, "    \x1b[7malpha\x1b[27m") {
		t.Fatalf("expected leading spaces outside inverse on removed line, got %q", rem)
	}
	if !strings.HasPrefix(add, "    \x1b[7mgamma\x1b[27m") {
		t.Fatalf("expected leading spaces outside inverse on added line, got %q", add)
	}
	if !strings.HasSuffix(rem, " beta") || !strings.HasSuffix(add, " beta") {
		t.Fatalf("expected unchanged trailing word outside inverse, got rem=%q add=%q", rem, add)
	}
}

func TestRenderDiffLinesIntraLineAndPairing(t *testing.T) {
	p := palette{titanium: true, mode: "truecolor"}
	t.Run("1-to-1 paired replacement emits inverse and preserves fg", func(t *testing.T) {
		diff := strings.Join([]string{
			" 1|project: oh-my-pig",
			"-2|status: draft",
			"+2|status: verified",
			" 3|owner: proof-harness",
		}, "\n")
		rows := p.renderDiffLines(diff, true, 78)
		if len(rows) != 4 {
			t.Fatalf("expected 4 rows, got %d: %#v", len(rows), rows)
		}

		remRow := rows[1]
		addRow := rows[2]

		if !strings.Contains(remRow, "\x1b[7mdraft\x1b[27m") {
			t.Fatalf("removed row missing inverse on 'draft': %q", remRow)
		}
		if !strings.Contains(addRow, "\x1b[7mverified\x1b[27m") {
			t.Fatalf("added row missing inverse on 'verified': %q", addRow)
		}

		// Verify fg color is applied around the whole line and not reset before/by \x1b[27m.
		remFg := p.fgAnsi("toolDiffRemoved")
		addFg := p.fgAnsi("toolDiffAdded")
		if !strings.HasPrefix(remRow, remFg) || !strings.HasSuffix(remRow, "\x1b[39m") {
			t.Fatalf("removed row should wrap with toolDiffRemoved fg (%q): %q", remFg, remRow)
		}
		if !strings.HasPrefix(addRow, addFg) || !strings.HasSuffix(addRow, "\x1b[39m") {
			t.Fatalf("added row should wrap with toolDiffAdded fg (%q): %q", addFg, addRow)
		}
		// Ensure no \x1b[0m or premature \x1b[39m before the end of the row.
		if strings.Contains(remRow[:len(remRow)-len("\x1b[39m")], "\x1b[39m") {
			t.Fatalf("removed row prematurely reset fg: %q", remRow)
		}
		if strings.Contains(addRow[:len(addRow)-len("\x1b[39m")], "\x1b[39m") {
			t.Fatalf("added row prematurely reset fg: %q", addRow)
		}
	})

	t.Run("unequal -/+ run falls back to plain line diff without inverse", func(t *testing.T) {
		diff := strings.Join([]string{
			"-1|old line one",
			"-2|old line two",
			"+1|new line only",
		}, "\n")
		rows := p.renderDiffLines(diff, true, 78)
		if len(rows) != 3 {
			t.Fatalf("expected 3 rows, got %d", len(rows))
		}
		for i, r := range rows {
			if strings.Contains(r, "\x1b[7m") {
				t.Fatalf("row[%d] should not contain inverse for unequal -/+ run: %q", i, r)
			}
		}
	})

	t.Run("equal multi-line -/+ run (2-to-2) matches chrome/diff.ts plain fallback", func(t *testing.T) {
		diff := strings.Join([]string{
			"-1|alpha one",
			"-2|beta two",
			"+1|alpha ONE",
			"+2|beta TWO",
		}, "\n")
		rows := p.renderDiffLines(diff, true, 78)
		if len(rows) != 4 {
			t.Fatalf("expected 4 rows, got %d", len(rows))
		}
		for i, r := range rows {
			if strings.Contains(r, "\x1b[7m") {
				t.Fatalf("row[%d] should not contain inverse for 2-to-2 run (chrome/diff.ts:248): %q", i, r)
			}
		}
	})
}
