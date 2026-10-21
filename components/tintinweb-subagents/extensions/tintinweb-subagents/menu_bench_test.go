package tintinweb_subagents

import (
	"strconv"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The render and input path of the dialogs is bounded work: a spinner frame costs the visible rows (at most
// defaultPaneBodyRows per pane), not a fold of the run's log, and the settings list renders its rows.

func benchRun(agents int) []workflowEntry {
	progress := []workflowEntry{newPhaseEntry(0, "Review")}
	for i := range agents {
		progress = append(progress, dEntry(i, func(e *workflowAgentEntry) {
			e.Label, e.PhaseIndex, e.State, e.Tokens, e.ToolCalls, e.DurationMs = "agent-"+strconv.Itoa(i), intp(0), entryDone, intp(1000+i), intp(3), i64p(4000)
		}))
	}
	return progress
}

// BenchmarkWorkflowDialogFrame is one spinner frame of a 1000-agent run with the groups cached, as the component renders it.
func BenchmarkWorkflowDialogFrame(b *testing.B) {
	progress := benchRun(1000)
	source := func() workflowDialogSource {
		return workflowDialogSource{Progress: progress, Version: 1, Task: workflowCardTask{Status: runRunning, WorkflowName: "wf", StartTime: dlgStart}}
	}
	d := newWorkflowDialog(&fakeDialogHost{}, source, tagTheme(), func() {}, workflowDialogActions{OnKill: func() {}}, 0)
	defer d.Dispose()
	d.Render(100)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		d.Render(100)
	}
}

// BenchmarkWorkflowDialogKey is one key (down) through the pure key handler.
func BenchmarkWorkflowDialogKey(b *testing.B) {
	in := dlgInput(dialogOver{Progress: benchRun(1000)})
	view := resolveWorkflowDialog(in)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		handleWorkflowDialogKey("j", in.State, view)
	}
}

func BenchmarkWorkflowCardLayout(b *testing.B) {
	in := workflowCardInput{Progress: benchRun(200), Width: 120, Now: dlgStart + 1000, Task: workflowCardTask{Status: runRunning, StartTime: dlgStart}}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		layoutWorkflowCard(in)
	}
}

func BenchmarkSettingsListRender(b *testing.B) {
	m, _, _ := menuRigB(b)
	list := newSettingsList(m.a.settingsItems(), 25, settingsListThemeFor(tagTheme()), func(string, string) {}, func() {})
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		list.Render(100)
	}
}

func menuRigB(b *testing.B) (*agentsMenu, *fakeMenuUI, struct{}) {
	b.Helper()
	b.Setenv("PIG_CODING_AGENT_DIR", b.TempDir())
	a := newApp(sdk.New("menu-bench"), nil)
	ui := &fakeMenuUI{}
	return &agentsMenu{a: a, ui: ui, cwd: b.TempDir()}, ui, struct{}{}
}
