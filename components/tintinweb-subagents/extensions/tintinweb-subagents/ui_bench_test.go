package tintinweb_subagents

import (
	"fmt"
	"testing"
)

// Benchmarks of the paths a frame runs through: the widget, the fleet bar and the conversation viewer (the original's
// test/perf/*.bench.ts). They count allocations; none touches the disk or the host.

func benchWidget(b *testing.B, running, queued, finished int) {
	records := makeFleet(running, queued, finished)
	w := newAgentWidget(&fixedList{agents: records}, makeActivityFor(records), func() widgetMode { return widgetModeAll }, func() bool { return true }, func() bool { return true })
	ui := &fakeWidgetUI{}
	w.setUI(ui)
	w.update()
	th := plainTheme()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ui.render(120, th)
	}
}

func BenchmarkWidgetRender5(b *testing.B)  { benchWidget(b, 5, 3, 2) }
func BenchmarkWidgetRender50(b *testing.B) { benchWidget(b, 50, 10, 10) }

func BenchmarkFleetBarRender(b *testing.B) {
	records := makeFleet(20, 0, 0)
	f := newFleetList(&stubFleetManager{fixedList: &fixedList{agents: records}}, nil, func() bool { return true }, nil, nil)
	ui := &fakeFleetUI{}
	f.setUI(ui)
	f.update()
	th := plainTheme()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ui.render(120, th)
	}
}

func benchViewer(b *testing.B, n int, mode viewerMarkdownMode) {
	var messages []obj
	for i := 0; i < n; i++ {
		switch i % 3 {
		case 0:
			messages = append(messages, userMsg(fmt.Sprintf("question %d about the code", i)))
		case 1:
			messages = append(messages, assistantMsg(fmt.Sprintf("## Answer %d\n\nsome **bold** text and `code` for message %d", i, i)))
		default:
			messages = append(messages, resultMsg(fmt.Sprintf("result line %d\nsecond line", i)))
		}
	}
	v, _ := newViewer(40, mockSession(messages...), mockRecordView(func(r *agentView) { r.Status = "completed" }), nil, plainTheme(), viewerOptions{ViewerMarkdown: func() viewerMarkdownMode { return mode }})
	v.Render(120)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = v.Render(120)
	}
}

func BenchmarkViewerRender300Raw(b *testing.B)      { benchViewer(b, 300, viewerMarkdownOff) }
func BenchmarkViewerRender300Markdown(b *testing.B) { benchViewer(b, 300, viewerMarkdownAssistant) }

func BenchmarkViewerScrollKey(b *testing.B) {
	var messages []obj
	for i := 0; i < 300; i++ {
		messages = append(messages, assistantMsg(fmt.Sprintf("## Answer %d\n\nsome **bold** text for message %d", i, i)))
	}
	v, _ := newViewer(40, mockSession(messages...), mockRecordView(nil), nil, plainTheme(), viewerOptions{})
	v.Render(120)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v.HandleInput("k")
		v.HandleInput("j")
	}
}

func BenchmarkAgentResultRender(b *testing.B) {
	d := agentDetails{Status: "completed", ModelName: "opus 4.6", Tags: []string{"twin", "thinking: high"}, ToolUses: 7, Tokens: "12.3k token", DurationMs: 4200}
	th := plainTheme()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = renderAgentResult("text", &d, false, false, false, th, true, 120)
	}
}

func BenchmarkNotificationRender(b *testing.B) {
	d := notificationDetails{ID: "a", Description: "did it", Status: "completed", ToolUses: 2, TotalTokens: 1500, DurationMs: 2000, ResultPreview: "line one\nline two"}
	th := plainTheme()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = renderNotification(d, false, th, true, 120)
	}
}

func BenchmarkObserveMessageUpdate(b *testing.B) {
	u := newUIAgent(nil, nil)
	u.observe(obj{"type": "message_start", "message": obj{"role": "assistant", "content": []any{}}}, nil)
	ev := obj{"type": "message_update", "assistantMessageEvent": obj{"type": "text_delta", "contentIndex": float64(0), "delta": "x"}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		u.observe(ev, nil)
	}
}
