package schedule_prompt

import (
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
	"github.com/MichaelKinsy/pigpen/schedule-prompt/internal/jsdate"
	"github.com/MichaelKinsy/pigpen/schedule-prompt/internal/jsobj"
)

// Twin of the original's test/cron-widget.test.ts (1 case), and the widget's other behavior.

type setWidgetCall struct {
	key       string
	view      *kit.View
	placement string
}

type fakeWidgetUI struct{ calls []setWidgetCall }

func (f *fakeWidgetUI) SetWidget(key string, view *kit.View, placement string) {
	f.calls = append(f.calls, setWidgetCall{key, view, placement})
}
func (f *fakeWidgetUI) Theme() sdk.UITheme { return sdk.UITheme{} }

func TestCronWidgetRender(t *testing.T) {
	tw(t, "cron-widget", "handles jobs with undefined runCount without crashing", func(t *testing.T) {
		job := exampleJob(obj{"id": "test-1", "name": "Test Job", "schedule": "0 * * * * *", "type": "cron", "prompt": "say hello", "runCount": jsobj.Undefined})
		w := newCronWidget(makeStorage(job), stubScheduler{}, func() bool { return true }, "test-session")
		ui := &fakeWidgetUI{}
		w.show(ui)
		defer w.destroy()
		if len(ui.calls) != 1 || ui.calls[0].view == nil {
			t.Fatalf("setWidget calls = %+v", ui.calls)
		}
		// The original renders the component at 100 columns without throwing: build the rows at the same state.
		_ = widgetRows(sdk.UITheme{}, []*jsobj.Object{job}, stubScheduler{}, jsdate.Now())
	})
}

func TestWidgetLifecycle(t *testing.T) {
	visible := true
	st := makeStorage()
	w := newCronWidget(st, stubScheduler{}, func() bool { return visible }, "s1")
	ui := &fakeWidgetUI{}
	// No job: the widget hides itself (a removal call), as show does in the original.
	w.show(ui)
	if len(ui.calls) != 1 || ui.calls[0].view != nil || ui.calls[0].key != "schedule-prompts" {
		t.Fatalf("calls = %+v", ui.calls)
	}
	// A job appears: a change event refreshes the widget below the editor.
	_ = st.AddJob(exampleJob(obj{"session": "s1"}))
	w.refresh()
	if n := len(ui.calls); n != 2 || ui.calls[1].view == nil || ui.calls[1].placement != "belowEditor" {
		t.Fatalf("calls = %+v", ui.calls)
	}
	// A job of another session does not count.
	other := makeStorage(exampleJob(obj{"session": "s2"}))
	w2 := newCronWidget(other, stubScheduler{}, func() bool { return true }, "s1")
	ui2 := &fakeWidgetUI{}
	w2.show(ui2)
	if len(ui2.calls) != 1 || ui2.calls[0].view != nil {
		t.Fatalf("calls = %+v", ui2.calls)
	}
	// Not visible: hidden even with jobs.
	visible = false
	w.refresh()
	if last := ui.calls[len(ui.calls)-1]; last.view != nil {
		t.Fatalf("calls = %+v", ui.calls)
	}
	w.destroy()
	w.refresh() // after destroy a refresh does nothing
	if len(ui.calls) != 3 {
		t.Fatalf("calls = %d", len(ui.calls))
	}
}

func TestWidgetRows(t *testing.T) {
	running := exampleJob(obj{"id": "a", "name": "a-very-long-job-name", "type": "cron", "schedule": "0 0 9 * * 1-5", "prompt": "a prompt that is longer than twenty-five characters", "lastStatus": "running", "model": "claude-haiku-4-5", "notify": true, "lastRun": "2020-01-01T00:00:00.000Z", "runCount": float64(12)})
	off := exampleJob(obj{"id": "b", "name": "off", "enabled": false, "type": "once", "schedule": "2026-02-13T15:30:00.000Z", "prompt": "p"})
	rows := widgetRows(sdk.UITheme{}, []*jsobj.Object{running, off}, stubScheduler{}, jsdate.Now())
	plain := func(s string) string { return s }
	_ = plain
	want0 := " ⟳ a-very-long-... 9am weekdays    a prompt that is longe... -          "
	if !strings.HasPrefix(rows[0], want0) || !strings.HasSuffix(rows[0], " [claude-ha...!]") {
		t.Fatalf("row 0 = %q", rows[0])
	}
	if !strings.Contains(rows[0], "12 ") {
		t.Fatalf("row 0 = %q", rows[0])
	}
	if !strings.HasPrefix(rows[1], " ✗ off ") || !strings.Contains(rows[1], "never") {
		t.Fatalf("row 1 = %q", rows[1])
	}
}

func TestFormatRelativeTime(t *testing.T) {
	now := 1_000_000_000_000.0
	for _, c := range []struct {
		target float64
		want   string
	}{
		{now + 45_000, "in 45s"}, {now + 5*60_000, "in 5m"}, {now + 3*3600_000, "in 3h"}, {now + 2*86400_000, "in 2d"},
		{now - 30_000, "30s ago"}, {now - 90*60_000, "1h ago"}, {now, "0s ago"}, {now - 3*86400_000, "3d ago"},
	} {
		if got := formatRelativeTime(c.target, now); got != c.want {
			t.Errorf("formatRelativeTime(%v) = %q, want %q", c.target-now, got, c.want)
		}
	}
}
