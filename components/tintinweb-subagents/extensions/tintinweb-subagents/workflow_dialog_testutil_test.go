package tintinweb_subagents

import (
	"regexp"
	"strings"
	"testing"
)

// Helpers shared by the twins of test/workflow-dialog.test.ts: the original's own fixtures and readers of the framed
// layout, in Go.

const dlgStart int64 = 1_000_000

// dialogOver is a DialogOverrides: everything optional but the progress.
type dialogOver struct {
	Progress     []workflowEntry
	Task         func(*workflowCardTask)
	State        func(*workflowDialogState)
	Meta         *workflowMeta
	Width        int
	Now          int64
	SpinnerFrame int
	ASCII        bool
	AgentCount   int
	BodyRows     int
	Available    map[string]bool
}

func dlgInput(over dialogOver) workflowDialogInput {
	task := workflowCardTask{Status: runRunning, WorkflowName: "review-changes", StartTime: dlgStart}
	if over.Task != nil {
		over.Task(&task)
	}
	state := initialWorkflowDialogState(0)
	if over.State != nil {
		over.State(&state)
	}
	width, now := over.Width, over.Now
	if width == 0 {
		width = 86
	}
	if now == 0 {
		now = dlgStart + 1000
	}
	return workflowDialogInput{workflowDialogSource: workflowDialogSource{Progress: over.Progress, Task: task, Meta: over.Meta, AgentCount: over.AgentCount},
		State: state, Available: over.Available, Now: now, Width: width, ASCII: over.ASCII, SpinnerFrame: over.SpinnerFrame, BodyRows: over.BodyRows}
}

func dialogLines(over dialogOver) []string {
	return plainWorkflowDialogLines(layoutWorkflowDialog(dlgInput(over)))
}

func styledDialog(over dialogOver) []string {
	return styleWorkflowCardLines(layoutWorkflowDialog(dlgInput(over)), tagTheme())
}

var markupPattern = regexp.MustCompile(`</?[a-zA-Z]+>|\*`)

// stripMarkup strips the fake theme's markup, keeping the text exactly as it is laid out.
func stripMarkup(line string) string { return markupPattern.ReplaceAllString(line, "") }

var bareLead = regexp.MustCompile(`^[\s▸>]+`)

// bare strips the markup and a heading's focus marker / padding.
func bare(line string) string { return bareLead.ReplaceAllString(stripMarkup(line), "") }

var borderPattern = regexp.MustCompile(`(?:<[a-zA-Z]+>)?[│|](?:</[a-zA-Z]+>)?`)

type cellPair struct{ left, right string }

// cellsOf reads the frame's body rows as (left, right) cell pairs. Split on the border rather than sliced by index, and
// the border is matched with its optional theme markup, so the same helper reads a plain layout and a styled one.
func cellsOf(lines []string) []cellPair {
	var rows []cellPair
	for _, line := range lines {
		parts := borderPattern.Split(line, -1)
		if len(parts) == 4 && strings.TrimSpace(parts[0]) == "" && strings.TrimSpace(parts[3]) == "" {
			rows = append(rows, cellPair{parts[1], parts[2]})
		}
	}
	return rows
}

// trimTrailing drops the padding rows the frame adds below the content.
func trimTrailing(rows []string) []string {
	out := append([]string(nil), rows...)
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return out
}

func leftRows(lines []string) []string {
	var rows []string
	for _, c := range cellsOf(lines) {
		rows = append(rows, c.left)
	}
	return trimTrailing(rows)
}

func rightRows(lines []string) []string {
	var rows []string
	for _, c := range cellsOf(lines) {
		rows = append(rows, c.right)
	}
	return trimTrailing(rows)
}

var paneTitleFrame = regexp.MustCompile(`^[╭+](.*?)[┬+](.*)[╮+]$`)
var titleRule = regexp.MustCompile(`[─-]+\s*$`)

// paneTitles are the two pane titles from the frame's top edge.
func paneTitles(t *testing.T, lines []string) (left, right string) {
	t.Helper()
	for _, line := range lines {
		if m := paneTitleFrame.FindStringSubmatch(bare(line)); m != nil {
			strip := func(part string) string { return strings.TrimSpace(titleRule.ReplaceAllString(part, "")) }
			return strip(m[1]), strip(m[2])
		}
	}
	t.Fatalf("no frame in:\n%s", strings.Join(lines, "\n"))
	return "", ""
}

// section is the rows of a detail section: everything between its heading and the next blank.
func section(t *testing.T, lines []string, heading string) []string {
	t.Helper()
	var rows []string
	for _, r := range rightRows(lines) {
		rows = append(rows, strings.TrimRight(stripMarkup(r), " \t"))
	}
	start := -1
	for i, r := range rows {
		if strings.HasPrefix(strings.TrimSpace(r), heading) {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("no %s section in:\n%s", heading, strings.Join(lines, "\n"))
	}
	rest := rows[start+1:]
	end := len(rest)
	for i, r := range rest {
		if strings.TrimSpace(r) == "" {
			end = i
			break
		}
	}
	// Body rows are indented three columns under their heading; the recovered copy carries its own two, so one is
	// dropped to compare against it.
	var out []string
	for _, r := range rest[:end] {
		out = append(out, leadingThree.ReplaceAllString(r, "  "))
	}
	return out
}

var leadingThree = regexp.MustCompile(`^ {3}`)

// detailLines is the dialog in its agent subview, which is where the detail sections live.
func detailLines(over dialogOver) []string {
	prev := over.State
	over.State = func(s *workflowDialogState) {
		if prev != nil {
			prev(s)
		}
		s.Level = dialogLevelAgent
	}
	return dialogLines(over)
}

func withState(mut func(*workflowDialogState)) func(*workflowDialogState) { return mut }

func taskStatus(status string) func(*workflowCardTask) {
	return func(t *workflowCardTask) { t.Status = status }
}

func startedAt(n int64) *int64 { return &n }

// aEntryWith builds an agent entry (the original's agentEntry) from a mutation.
func aEntryWith(index int, mut func(*workflowAgentEntry)) *workflowAgentEntry {
	e := &workflowAgentEntry{Type: "workflow_agent", Index: index, Label: "agent-" + itoa(index), PhaseIndex: intp(0), State: entryStart}
	if mut != nil {
		mut(e)
	}
	return e
}
