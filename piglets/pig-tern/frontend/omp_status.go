package frontend

import (
	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/stencil-hq/tern-sdk/go/tern"
	"github.com/stencil-hq/tern-sdk/go/tern/ui"
	"math"
	"strconv"
	"strings"
)

// ompPathSpans splits a path into its dim parent directories and strong leaf,
// matching omp's pathSpans (status-line/segments.ts:633-638).
func ompPathSpans(value string) []ui.Span {
	if value == "" {
		return nil
	}
	trimmed := value
	if len(trimmed) > 1 && strings.HasSuffix(trimmed, "/") {
		trimmed = trimmed[:len(trimmed)-1]
	}
	slash := strings.LastIndexByte(trimmed, '/')
	if slash < 0 || slash+1 >= len(value) {
		return []ui.Span{{T: value, S: "statusLinePath strong"}}
	}
	return []ui.Span{
		{T: value[:slash+1], S: "statusLinePath dim"},
		{T: value[slash+1:], S: "statusLinePath strong"},
	}
}

// ompFooterElement draws PiG's footer as omp's status strip (role "omp.status")
// with omp's `omp.status.<key>` segment roles, icons, spans, and drop priorities
// (status-line/component.ts:3789-3864).
//
// Segments mapped from PiG's Footer:
//   - cwd      -> omp.status.path         (left, folder icon, dim parent / strong leaf, click opens)
//   - branch   -> omp.status.git          (left, branch icon, statusLineGitClean)
//   - session  -> omp.status.session_name (left, plain text)
//   - status.i -> omp.status.hook         (left, extension statuses)
//   - usage    -> omp.status.cost         (right, usage/cost text)
//   - context  -> omp.status.context_pct  (right, context icon + meter child)
//   - xp       -> (no omp role)           (right, warning tone)
//   - model    -> omp.status.model        (right, model icon + effort child, clicks open /model & /thinking)
func ompFooterElement(id string, footer fe.Footer, run func(command string)) tern.Element {
	slots, statuses := footerHot(footer)
	wide := &ui.Bounds{W: ui.Ch(12)}
	segCommon := func(key, role string, h hot) ui.Common {
		return ui.Common{Key: key, Role: role, Tone: h.tone, Hidden: h.hidden}
	}

	cwd := ui.Seg{
		Common:   segCommon("cwd", "omp.status.path", slots[segCwd]),
		Icon:     ui.IconFolder,
		Spans:    ompPathSpans(slots[segCwd].text),
		Priority: 7,
	}
	cwd.Min = wide
	cwd.Href = cwdHref(slots[segCwd].text)
	cwd.Actions = &ui.Actions{Click: "open", Menu: []ui.MenuEntry{ui.MenuAct("open"), ui.MenuAct("copy")}}

	segs := make([]tern.Element, 0, 7+len(statuses))
	segs = append(segs,
		cwd,
		ui.Seg{
			Common:   segCommon("branch", "omp.status.git", slots[segBranch]),
			Icon:     ui.IconBranch,
			Text:     slots[segBranch].text,
			Priority: 1005,
		},
		ui.Seg{
			Common:   segCommon("session", "omp.status.session_name", slots[segSession]),
			Icon:     ui.IconMessage,
			Text:     slots[segSession].text,
			Priority: 3,
		},
	)
	for i, status := range statuses {
		segs = append(segs, ui.Seg{
			Common:   segCommon("status."+strconv.Itoa(i), "omp.status.hook", status),
			Text:     status.text,
			Priority: 2,
		})
	}
	meter := ui.Meter{Common: segCommon("meter", "", slots[segMeter])}
	if slots[segMeter].hasValue {
		v := math.Float64frombits(slots[segMeter].value)
		meter.Value = &v
	}

	xp := ui.Seg{
		Common:   segCommon("xp", "", slots[segXP]),
		Side:     ui.SideRight,
		Text:     "xp",
		Priority: 1,
	}
	xp.Tone = ui.ToneWarning

	effort := ui.Effort{
		Common: segCommon("effort", "", slots[segEffort]),
		Level:  ui.EffortLevel(slots[segEffort].text),
	}
	effort.Title = "Thinking level (/thinking)"
	effort.OnClick = func(*tern.Action) { run("/thinking") }

	model := ui.Seg{
		Common:   segCommon("model", "omp.status.model", slots[segModel]),
		Side:     ui.SideRight,
		Icon:     ui.IconName("model"),
		Text:     slots[segModel].text,
		Priority: 1009,
		Children: []tern.Element{effort},
	}
	model.Min = wide
	model.Title = "Select model (/model)"
	model.OnClick = func(*tern.Action) { run("/model") }

	segs = append(segs,
		ui.Seg{
			Common:   segCommon("usage", "omp.status.cost", slots[segUsage]),
			Side:     ui.SideRight,
			Text:     slots[segUsage].text,
			Priority: 6,
		},
		ui.Seg{
			Common:   segCommon("context", "omp.status.context_pct", slots[segContext]),
			Side:     ui.SideRight,
			Icon:     ui.IconName("context"),
			Text:     slots[segContext].text,
			Priority: 1008,
			Children: []tern.Element{meter},
		},
		xp,
		model,
	)
	return ui.Status{
		Common:   ui.Common{Key: id, Role: "omp.status"},
		Children: segs,
	}
}
