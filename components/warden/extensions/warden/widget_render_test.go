package warden

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// Twins of tests/widget.test.ts.

// marksTheme marks every tone in the text, so an assertion can check the hierarchy and not only the words.
type marksTheme struct{}

var marks = map[string]string{"text": "T", "muted": "m", "dim": "d", "success": "S", "warning": "W", "error": "E"}

func (marksTheme) Fg(color, text string) string {
	m := marks[color]
	if m == "" {
		m = color
	}
	return "<" + m + ">" + text + "</>"
}
func (marksTheme) Bold(text string) string { return "<b>" + text + "</>" }

type bareTheme struct{}

func (bareTheme) Fg(_, text string) string { return text }
func (bareTheme) Bold(text string) string  { return text }

var reMark = regexp.MustCompile(`</?[A-Za-z]*>`)

func plain(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = reMark.ReplaceAllString(l, "")
	}
	return out
}

func entries(pairs ...string) []WidgetEntry {
	var out []WidgetEntry
	for i := 0; i < len(pairs); i += 2 {
		out = append(out, WidgetEntry{pairs[i], pairs[i+1]})
	}
	return out
}

const allowLine = "warden · bash · irreversible 0.20 · off-task 0.10 · expected step · allow"

func wantLines(t *testing.T, name string, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s:\n got %q\nwant %q", name, got, want)
	}
}

// twin: tests/widget.test.ts:14
func TestOutsideProjectFloorWarningDoesNotClaimHighIrreversibility(t *testing.T) {
	tokens := Tokens{"tool": "write", "level": "warn", "irreversible": "0.11", "reasons": "creates a file outside the project", "slop": "stub 0.90", "offTask": "0.90"}
	sentence := RenderTemplate(PickSentenceTemplate("action", tokens), tokens)
	if !strings.Contains(sentence, "creates a file outside the project") {
		t.Errorf("sentence %q", sentence)
	}
	if regexp.MustCompile(`high irreversibility|slop|off-task`).MatchString(sentence) {
		t.Errorf("sentence %q claims what the floor did not", sentence)
	}
}

// twin: tests/widget.test.ts:21
func TestIrreversibilitySentencesRequireAThresholdReason(t *testing.T) {
	for _, level := range []string{"warn", "confirm"} {
		tokens := Tokens{"tool": "write", "level": level, "irreversible": "0.90", "reasons": "changes a file outside the project"}
		if strings.Contains(RenderTemplate(PickSentenceTemplate("action", tokens), tokens), "irreversibility") {
			t.Errorf("%s: a score alone is not a threshold reason", level)
		}
	}
	eq(t, "warn", PickSentenceTemplate("action", Tokens{"level": "warn", "irreversible": "0.20", "reasons": "possibly irreversible 20%"}), SentenceTemplates["action"]["warn_irreversible"])
	eq(t, "hold", PickSentenceTemplate("action", Tokens{"level": "confirm", "irreversible": "0.60", "reasons": "irreversible 60%"}), SentenceTemplates["action"]["hold_irreversible"])
}

// twin: tests/widget.test.ts:30
func TestVerdictLeadsTheStatusLineAsAChip(t *testing.T) {
	lines := WidgetLines(entries("action", allowLine), marksTheme{}, 0)
	wantLines(t, "plain", plain(lines), []string{"ALLOW action"})
	eq(t, "allow colour", LevelColor["allow"], "success")
	if !regexp.MustCompile(`^<b><S>ALLOW</></> <m>action</>$`).MatchString(lines[0]) {
		t.Errorf("chip %q", lines[0])
	}
}

// twin: tests/widget.test.ts:37
func TestQuietVerdictsFoldIntoOneLinePerVerdict(t *testing.T) {
	lines := plain(WidgetLines(entries("action", allowLine,
		"rules", "warden · rules · write src/clean.ts · 2 rules · none · ok",
		"prose", "warden · prose · wordy 0.36 · clichés 0.09 · jargon 0.19 · ok"), marksTheme{}, 0))
	wantLines(t, "lines", lines, []string{"ALLOW action", "OK    rules · prose"})
	if strings.Contains(strings.Join(lines, "\n"), "wordy") {
		t.Error("the folded line drops the scores the guard found nothing in")
	}
}

// twin: tests/widget.test.ts:47
func TestQuietVerdictWithACaveatKeepsItsOwnLine(t *testing.T) {
	lines := plain(WidgetLines(entries("action", "warden · bash · typesafe error · allow",
		"prose", "warden · prose · typesafe error · ok",
		"rules", "warden · rules · write src/r.ts · 2 rules · none · ok"), marksTheme{}, 0))
	wantLines(t, "caveats", lines, []string{"OK    rules", "ALLOW action bash · typesafe error", "OK    prose  typesafe error"})
	wantLines(t, "approval", plain(WidgetLines(entries("action", "warden · bash · user approved · allow"), marksTheme{}, 0)), []string{"ALLOW action bash · user approved"})
	slop := plain(WidgetLines(entries("action", "warden · write · slop: stub 0.92, hedging 0.75 · allow", "action", "warden · write · slop: none · allow"), marksTheme{}, 0))
	wantLines(t, "slop", slop, []string{"ALLOW action", "ALLOW action write · slop: stub 0.92, hedging 0.75"})
}

// twin: tests/widget.test.ts:62
func TestWorstVerdictEndsNearestTheEditor(t *testing.T) {
	lines := plain(WidgetLines(entries("stuck", "warden · stuck · 3 failures · exact repeat · stuck",
		"action", "warden · bash · irreversible 0.90 · off-task 0.95 · unrelated · warn",
		"done", "warden · done-check · 1 changes · 0/0 checks passed · claims done 0.92 · claims verified 0.10 · checks apply 0.90 · complete · unverified"), marksTheme{}, 0))
	var heads []string
	for _, l := range lines {
		heads = append(heads, strings.Split(l, " ")[0])
	}
	wantLines(t, "order", heads, []string{"WARN", "UNVERIFIED", "STUCK"})
}

// twin: tests/widget.test.ts:71
func TestLineWithoutAVerdictKeepsTheGuardInTheRail(t *testing.T) {
	lines := WidgetLines(entries("context", "warden · context · bash · duplicate · saved 1024 bytes"), marksTheme{}, 0)
	wantLines(t, "plain", plain(lines), []string{"context bash · duplicate · saved 1024 bytes"})
	if !regexp.MustCompile(`^<m>context</> <T>bash</>`).MatchString(lines[0]) {
		t.Errorf("line %q", lines[0])
	}
}

// twin: tests/widget.test.ts:77
func TestRailAndGuardColumnAreAsWideAsTheirWidestEntry(t *testing.T) {
	lines := plain(WidgetLines(entries("action", "warden · bash · off-task 0.95 · unrelated · warn",
		"done", "warden · done-check · 1 changes · claims done 0.92 · complete · unverified"), marksTheme{}, 0))
	wantLines(t, "lines", lines, []string{"WARN       action bash · off-task 0.95 · unrelated", "UNVERIFIED done   done-check · 1 changes · claims done 0.92 · complete"})
}

// twin: tests/widget.test.ts:85
func TestBodySegmentsReadAsData(t *testing.T) {
	line := WidgetLines(entries("action", "warden · bash · irreversible 0.90 · unrelated · warn"), marksTheme{}, 0)[0]
	if !regexp.MustCompile(`<T>bash</><d> · </><m>irreversible </><T>0\.90</><d> · </><m>unrelated</>`).MatchString(line) {
		t.Errorf("line %q", line)
	}
}

// twin: tests/widget.test.ts:90
func TestTwoWordVerdictOverflowsItsColumn(t *testing.T) {
	lines := plain(WidgetLines(entries("action", "warden · bash · off-task 0.95 · unrelated · warn",
		"runaway", "warden · runaway · text · 7× repeated · 520 chars · block · stopped, recovering"), marksTheme{}, 0))
	wantLines(t, "lines", lines, []string{"WARN action  bash · off-task 0.95 · unrelated", "STOPPED, RECOVERING runaway text · 7× repeated · 520 chars · block"})
}

// twin: tests/widget.test.ts:98
func TestNarrowPaneWrapsABodyUnderItsOwnColumn(t *testing.T) {
	lines := WidgetLines(entries("action", "warden · write · irreversible 0.09 · off-task 0.95 · unrelated · off task · warn"), bareTheme{}, 40)
	wantLines(t, "lines", lines, []string{"WARN action write · irreversible 0.09 ·", "            off-task 0.95 · unrelated ·", "            off task"})
	for _, l := range lines {
		if utf16Len(l) > 40 {
			t.Errorf("%q is wider than the pane", l)
		}
	}
}

// twin: tests/widget.test.ts:105
func TestParseVerdictLine(t *testing.T) {
	cases := []struct {
		line, guard, status string
		body                []string
	}{
		{"warden · rules · write src/r.ts · 2 rules · none · ok", "rules", "ok", []string{"write src/r.ts", "2 rules", "none"}},
		{"warden · bash · irreversible 0.33 · allow", "action", "allow", []string{"bash", "irreversible 0.33"}},
		{"warden · context · bash · saved 12 bytes", "context", "", []string{"bash", "saved 12 bytes"}},
		{"22:44:54 bash → allow · irr 0.33", "action", "", []string{"22:44:54 bash → allow", "irr 0.33"}},
	}
	for _, c := range cases {
		status, body := ParseVerdictLine(c.line, c.guard)
		eq(t, c.line, []any{status, body}, []any{c.status, c.body})
	}
}

// twin: tests/widget.test.ts:112
func TestDenyVerdictRendersAChip(t *testing.T) {
	eq(t, "deny colour", LevelColor["deny"], "error")
	line := "warden · bash · never-reset · deny"
	status, body := ParseVerdictLine(line, "action")
	eq(t, "parsed", []any{status, body}, []any{"deny", []string{"bash", "never-reset"}})
	lines := WidgetLines(entries("action", line), marksTheme{}, 0)
	if !strings.Contains(lines[0], "DENY") {
		t.Errorf("line %q", lines[0])
	}
}
