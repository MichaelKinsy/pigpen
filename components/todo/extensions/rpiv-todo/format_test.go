package rpiv_todo

import "testing"

const fFormat = "view/format"

// recordingTheme tags every styled span, like the original test's makeTheme (view/format.test.ts:7-10).
type recordingTheme struct{}

func (recordingTheme) Fg(token, text string) string {
	return "<" + token + ">" + text + "</" + token + ">"
}
func (recordingTheme) Bold(text string) string { return "<bold>" + text + "</bold>" }
func (recordingTheme) Strikethrough(text string) string {
	return "<strike>" + text + "</strike>"
}

func TestFormatOverlayTaskLine(t *testing.T) {
	tw(t, fFormat, "keeps pending subjects primary while rendering IDs quietly", func(t *testing.T) {
		eq(t, formatOverlayTaskLine(tk(1, "quiet task"), recordingTheme{}, true), "<dim>○</dim> <dim>#1</dim> <text>quiet task</text>", "line")
	})
	tw(t, fFormat, "emphasizes the current task while muting its supporting metadata", func(t *testing.T) {
		got := formatOverlayTaskLine(tk(1, "quiet task", withStatus(statusInProgress), withActiveForm("Working"), withBlockedBy(2, 3)), recordingTheme{}, true)
		eq(t, got, "<warning>◐</warning> <dim>#1</dim> <accent>quiet task</accent> <muted>(Working)</muted> <muted>⛓ #2,#3</muted>", "line")
	})
	tw(t, fFormat, "mutes and strikes completed subjects", func(t *testing.T) {
		eq(t, formatOverlayTaskLine(tk(1, "quiet task", withStatus(statusCompleted)), recordingTheme{}, false), "<success>✓</success> <strike><muted>quiet task</muted></strike>", "line")
	})
	tw(t, fFormat, "strips escape sequences from subject and activeForm before theming", func(t *testing.T) {
		got := formatOverlayTaskLine(tk(1, "quiet\u001b[2Jtask", withStatus(statusInProgress), withActiveForm("Work\u009bcing")), recordingTheme{}, false)
		eq(t, got, "<warning>◐</warning> <accent>quiettask</accent> <muted>(Working)</muted>", "line")
	})
}

func TestViewStatusPresentation(t *testing.T) {
	eq(t, overlayStatusGlyph(statusDeleted, recordingTheme{}), "<error>✗</error>", "deleted overlay glyph")
	eq(t, formatStatusLabel(statusInProgress), "in progress", "label")
	eq(t, formatCommandTaskLine(tk(3, "x\u001b[0m", withStatus(statusInProgress), withActiveForm("xing"), withBlockedBy(1, 2)), "◐"), "  ◐ #3 x (xing)    ⛓ #1,#2", "command line")
}

func TestRenderTodoCallAndResult(t *testing.T) {
	st := stateWith(tk(1, "seeded"))
	th := recordingTheme{}
	head := "<toolTitle><bold>todo </bold></toolTitle><muted>"
	cases := []struct {
		name string
		args params
		want string
	}{
		{"create", params{"action": "create", "subject": "new\u001b[1m thing"}, head + "+</muted> <dim>new thing</dim>"},
		{"update unregistered id", params{"action": "update", "id": 9.0}, head + "→</muted> <accent>#9</accent>"},
		{"update seeded", params{"action": "update", "id": 1.0}, head + "→</muted> <accent>seeded</accent>"},
		{"list filtered", params{"action": "list", "status": "in_progress"}, head + "☰</muted> <muted>in progress</muted>"},
		{"clear", params{"action": "clear"}, head + "∅</muted>"},
		{"get", params{"action": "get", "id": 1.0}, head + "›</muted> <accent>seeded</accent>"},
		{"delete", params{"action": "delete", "id": 1.0}, head + "×</muted> <accent>seeded</accent>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { eq(t, renderTodoCall(c.args, th, st), c.want, "call") })
	}
	d := func(action string, p params, tasks ...task) *taskDetails {
		return &taskDetails{Action: action, Params: p, Tasks: tasks}
	}
	results := []struct {
		name string
		in   *taskDetails
		want string
	}{
		{"create", d("create", params{}, tk(1, "a")), "<dim>○ pending</dim>"},
		{"update to in progress", d("update", params{"id": 1.0, "status": "in_progress"}, tk(1, "a", withStatus(statusInProgress))), "<warning>◐ in progress</warning>"},
		{"update without status takes the task's", d("update", params{"id": 1.0}, tk(1, "a", withStatus(statusCompleted))), "<success>● completed</success>"},
		{"delete", d("delete", params{"id": 1.0}, tk(1, "a", withStatus(statusDeleted))), "<muted>⊘ deleted</muted>"},
		{"list", d("list", params{}), "<success>✓</success>"},
		{"get", d("get", params{"id": 1.0}), "<success>✓</success>"},
		{"clear", d("clear", params{}), "<success>✓</success>"},
		{"missing details", nil, "<success>✓</success>"},
	}
	for _, c := range results {
		t.Run("result "+c.name, func(t *testing.T) { eq(t, renderTodoResult(c.in, th), c.want, "result") })
	}
}

// upstream: view/format.ts:140-141: `params.status ?? task.status`. A rejected update still echoes the
// status that was asked for, not the task's.
func TestRenderTodoResultEchoesTheRequestedStatusOfARejectedUpdate(t *testing.T) {
	d := &taskDetails{Action: "update", Params: params{"id": 1.0, "status": "in_progress"}, Tasks: []task{tk(1, "a", withStatus(statusCompleted))}, Error: "illegal transition completed → in_progress"}
	eq(t, renderTodoResult(d, recordingTheme{}), "<warning>◐ in progress</warning>", "result")
}
