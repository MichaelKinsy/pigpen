package frontend

import (
	"time"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/stencil-hq/tern-sdk/go/tern"
	"github.com/stencil-hq/tern-sdk/go/tern/ui"
)

// ompWorkingElement draws PiG's working indicator in omp's shape
// (components/loader.ts:53-89):
//
//	row(role="omp.working", align="center", gap="sm"):
//	  - spinner(key="spinner", style="starburst", tone="accent")  [omitted when spinnerless]
//	  - elapsed(key="elapsed", age=ms, format="short")
//	  - text(key="sep", text="·", role="omp.working.sep")
//	  - shimmer(key="label", role="omp.working.label", spans=[{t, s:"muted"}],
//	      mode="classic", palette={low:"dim", mid:"muted", high:"accent"})
//	  - row(key="fill", grow=1)
//	  - progress(key="progress", value=nil)                       [when Kind == WorkingCompaction]
//
// The Stop (`omp.working.stop` with `kbd`) control is omitted because PiG's
// frontend.Env has no interrupt/abort method. Message and tone updates travel
// as raw ops on the label child (see queueOmpWorking).
func (s *session) ompWorkingElement(id string, working fe.Working, startedAt time.Time) tern.Element {
	h := workingHot(working)
	ageMs := 0.0
	if !startedAt.IsZero() {
		if d := s.now().Sub(startedAt); d > 0 {
			ageMs = float64(d.Milliseconds())
		}
	}
	var children []tern.Element
	if !spinnerless(working) {
		children = append(children, ui.Spinner{
			Common: ui.Common{Key: "spinner", Tone: ui.ToneAccent},
			Style:  ui.SpinnerStarburst,
		})
	}
	children = append(children,
		ui.Elapsed{
			Common: ui.Common{Key: "elapsed"},
			Age:    ageMs,
			Format: ui.ElapsedShort,
		},
		ui.Text{
			Common: ui.Common{Key: "sep", Role: "omp.working.sep"},
			Text:   "·",
		},
		ompWorkingLabel(h),
		ui.Row{Common: ui.Common{Key: "fill", Grow: 1}},
	)
	if working.Kind == fe.WorkingCompaction {
		children = append(children, ui.Progress{Common: ui.Common{Key: "progress"}})
	}
	return ui.Row{
		Common:   ui.Common{Key: id, Role: "omp.working"},
		Align:    ui.AlignCenter,
		Gap:      ui.GapSM,
		Children: children,
	}
}

// ompWorkingLabel builds the shimmering working label (theme/shimmer.ts:68-73, 164-178).
func ompWorkingLabel(h hot) ui.Shimmer {
	return ui.Shimmer{
		Common: ui.Common{Key: "label", Role: "omp.working.label", Tone: h.tone},
		Spans:  ompWorkingLabelSpans(h.text),
		Mode:   ui.ShimmerClassic,
		Palette: &ui.ShimmerPalette{
			Low:  "dim",
			Mid:  "muted",
			High: "accent",
		},
	}
}

func ompWorkingLabelSpans(text string) []ui.Span {
	if text == "" {
		return nil
	}
	return []ui.Span{{T: text, S: "muted"}}
}

// queueOmpWorking queues raw prop updates for omp's shimmering working label.
func (s *session) queueOmpWorking(e *entry, working fe.Working, fresh bool) {
	h := workingHot(working)
	if fresh {
		e.raw.text, e.raw.hot = h.text, h
		return
	}
	if e.raw.hot == h {
		return
	}
	set := make(map[string]any, 2)
	if h.text != e.raw.hot.text {
		set["spans"] = ompWorkingLabelSpans(h.text)
	}
	if h.tone != e.raw.hot.tone {
		set["tone"] = nullable(string(h.tone))
	}
	if len(set) > 0 {
		s.queue(tern.OpSet(e.tid+".label", set))
	}
	e.raw.text, e.raw.hot = h.text, h
}
