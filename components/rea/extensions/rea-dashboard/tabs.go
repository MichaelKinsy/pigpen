package readashboard

import (
	"fmt"
	"strings"
	"time"
)

// view is one frame of a tab: its rows, the selected row (-1 for a pane that scrolls) and the key hints.
type view struct {
	rows [][]seg
	sel  int
	hint string
}

func (v *view) add(segs ...seg) { v.rows = append(v.rows, segs) }

func (v *view) blank() { v.rows = append(v.rows, nil) }

// wrapped adds text hard-wrapped to w with an indent, in style.
func (v *view) wrapped(indent, text, style string, w int) {
	for _, l := range wrapText(text, max(w-textWidth(indent), 1)) {
		v.add(seg{indent + l, style})
	}
}

const (
	hintList     = "Tab/←→ tabs · ↑↓ select · Enter open · Esc/q close"
	hintTargets  = "Tab/←→ tabs · ↑↓ select · Enter actions · i investigate · v inspect · d derive · q close"
	hintProv     = "Tab/←→ tabs · ↑↓ select · Enter open · r recheck · q close"
	hintScroll   = "↑↓ scroll · Esc back · q close"
	hintAbout    = "Tab/←→ tabs · ↑↓ scroll · q close"
	hintMenu     = "↑↓ choose · Enter run · Esc back · q close"
	hintResult   = "↑↓ scroll · Enter fold/expand raw · r rerun · Esc back · q close"
	rawIndent    = "  "
	notRegistred = "REA is not registered: there is no \"rea\" MCP server (started with --no-mcp?)."
)

// build draws the current tab and mode, and clamps the selection to what exists.
func (o *overlay) build(m model, w int) view {
	switch o.tab {
	case tabInvestigation:
		if o.mode == modeDetail && len(m.Calls) > 0 {
			return o.callDetail(m, w)
		}
		o.mode = modeList
		return o.investigation(m, w)
	case tabTargets:
		t, ok := o.selectedTarget(m)
		switch {
		case ok && o.mode == modeResult:
			return o.inspectView(m, t, w)
		case ok && o.mode == modeMenu:
			return o.menuView(m, t, w)
		}
		o.mode = modeList
		return o.targetList(m, w)
	case tabProviders:
		if o.mode == modeDetail && len(providersFor(m)) > 0 {
			return o.providerDetail(m, w)
		}
		o.mode = modeList
		return o.providerList(m, w)
	}
	o.mode = modeList
	return o.about(m, w)
}

func (o *overlay) clamp(n int) int {
	if n == 0 {
		o.sel[o.tab] = 0
		return -1
	}
	o.sel[o.tab] = min(max(o.sel[o.tab], 0), n-1)
	return o.sel[o.tab]
}

// ── Investigation ────────────────────────────────────────────────────────────

func (o *overlay) investigation(m model, w int) view {
	v := view{hint: hintList}
	if len(m.Calls) == 0 {
		v.sel = -1
		v.add(seg{"No REA calls in this session yet.", styDim})
		v.blank()
		v.add(seg{"Calls the agent makes with REA's tools appear here as they run, and are kept with the session.", styDim})
		return v
	}
	if o.follow {
		o.sel[o.tab] = len(m.Calls) - 1
	}
	v.sel = o.clamp(len(m.Calls))
	for _, c := range m.Calls {
		v.add(callLine(c, m.Now, w, true)...)
	}
	return v
}

// callDetail shows what the dashboard kept of one call, REA's evidence and limitations first. The full result
// is not stored: it stays in the transcript.
func (o *overlay) callDetail(m model, w int) view {
	v := view{sel: -1, hint: hintScroll}
	c := m.Calls[o.clamp(len(m.Calls))]
	glyph, style := callStatusGlyph(c, m.Now)
	v.add(seg{c.Tool, styBold}, seg{"  " + glyph + " " + statusWord(c.Status), style}, seg{"  " + fmtDuration(callElapsed(c, m.Now)), styDim})
	v.blank()
	switch {
	case c.Status == statusRunning:
		v.add(seg{"Running: the result has not arrived.", styCyan})
	case !c.Counted:
		v.add(seg{"Evidence and limitations: not recorded (the result carried none, or was not JSON).", styDim})
	default:
		v.add(seg{fmt.Sprintf("Evidence (%d)", c.Evidence), styBold})
		if len(c.EvidenceSample) == 0 {
			v.add(seg{"  none", styDim})
		}
		for _, e := range c.EvidenceSample {
			v.wrapped("  • ", e, "", w)
		}
		if c.Evidence > len(c.EvidenceSample) {
			v.add(seg{fmt.Sprintf("  … %d more not kept", c.Evidence-len(c.EvidenceSample)), styDim})
		}
		v.blank()
		v.add(seg{fmt.Sprintf("Limitations (%d)", c.Limitations), styBold})
		if len(c.LimitationSample) == 0 {
			v.add(seg{"  none", styDim})
		}
		for _, l := range c.LimitationSample {
			v.wrapped("  • ", l, styYellow, w)
		}
		if c.Limitations > len(c.LimitationSample) {
			v.add(seg{fmt.Sprintf("  … %d more not kept", c.Limitations-len(c.LimitationSample)), styDim})
		}
	}
	if c.Error != "" {
		v.blank()
		v.add(seg{"Error", styRed})
		v.wrapped("  ", c.Error, "", w)
	}
	v.blank()
	for _, f := range [][2]string{{"Target", c.Target}, {"Arguments", c.Args}, {"Call id", c.ID}} {
		if f[1] != "" {
			v.add(seg{f[0], styDim})
			v.wrapped("  ", f[1], "", w)
		}
	}
	if c.nested() {
		v.add(seg{"Made inside a codemode script (call " + c.Parent + ")", styDim})
	}
	if !c.Started.IsZero() {
		v.add(seg{"Started " + c.Started.Local().Format("15:04:05"), styDim})
	}
	v.blank()
	v.add(seg{"Only this summary is stored with the session; the full result is in the transcript.", styDim})
	return v
}

func statusWord(s callStatus) string {
	switch s {
	case statusOK:
		return "ok"
	case statusError:
		return "error"
	}
	return "running"
}

// ── Targets ──────────────────────────────────────────────────────────────────

func (o *overlay) targetList(m model, w int) view {
	v := view{hint: hintTargets}
	ts := targets(m.Calls)
	if len(ts) == 0 {
		v.sel = -1
		v.hint = hintList
		v.add(seg{"No targets yet.", styDim})
		v.blank()
		v.add(seg{"Binaries, apps and URLs that REA calls name in their arguments are listed here.", styDim})
		return v
	}
	v.sel = o.clamp(len(ts))
	for _, t := range ts {
		count := fmt.Sprintf("%3d× ", t.Count)
		space := w - textWidth(count) - 2
		v.add(seg{count, styDim}, seg{tailFit(t.Value, space), ""})
	}
	return v
}

// menuView lists the actions of the selected target.
func (o *overlay) menuView(m model, t target, w int) view {
	v := view{sel: -1, hint: hintMenu}
	v.add(seg{"Target", styDim})
	v.wrapped("  ", t.Value, styBold, w)
	v.add(seg{fmt.Sprintf("  %d call(s) · %s", t.Count, strings.Join(t.Tools, ", ")), styDim})
	v.blank()
	menuStart := len(v.rows)
	for i, name := range actionNames {
		cursor := "  "
		if i == o.menuSel {
			cursor = "▸ "
		}
		label := name
		switch i {
		case actInspect:
			switch job, ok := m.Inspect[t.Value]; {
			case !t.local():
				label += "  (needs a local path)"
			case ok && job.Running:
				label += "  (running…)"
			case ok:
				label += "  (last result: Enter to open)"
			}
		case actInvestigate:
			label += "  ask a question; the agent drives REA"
		case actDerive:
			label += "  derive a piglet that works like it"
		}
		v.add(seg{cursor + label, ""})
	}
	// The menu cursor is the selected row, so a short terminal keeps it in view.
	v.sel = menuStart + o.menuSel
	return v
}

// inspectView shows a quick inspect: the summary, then the raw output folded.
func (o *overlay) inspectView(m model, t target, w int) view {
	v := view{sel: -1, hint: hintResult}
	v.add(seg{"Quick inspect", styBold})
	v.wrapped("  ", t.Value, styDim, w)
	v.blank()
	job, ok := m.Inspect[t.Value]
	switch {
	case !ok:
		v.add(seg{"Not run yet. Press r to run rea inspect-artifact --json.", styDim})
		return v
	case job.Running:
		v.add(seg{"Running rea inspect-artifact --json …", styCyan})
		return v
	}
	for _, s := range job.Result.Summary {
		v.wrapped("", s, "", w)
	}
	v.blank()
	fold := "▸"
	if o.rawOpen {
		fold = "▾"
	}
	size := fmt.Sprintf("%d bytes", job.Result.Bytes)
	if job.Result.Truncated {
		size = fmt.Sprintf("first %d KB", inspectLimit>>10)
	}
	v.add(seg{fmt.Sprintf("%s raw output (%s, %d lines) · Enter to %s", fold, size, len(job.Result.Raw), map[bool]string{true: "fold", false: "expand"}[o.rawOpen]), styCyan})
	if o.rawOpen {
		for _, l := range job.Result.Raw {
			for _, part := range wrapText(l, max(w-textWidth(rawIndent), 1)) {
				v.add(seg{rawIndent + part, styDim})
			}
		}
	}
	return v
}

// ── Providers ────────────────────────────────────────────────────────────────

// providersFor is the doctor's providers, or placeholders until the doctor reported so the variables each
// provider reads are readable before then.
func providersFor(m model) []provider {
	if m.Doctor.Report != nil && len(m.Doctor.Report.Providers) > 0 {
		return m.Doctor.Report.Providers
	}
	out := make([]provider, 0, len(providerOrder))
	for _, id := range providerOrder {
		out = append(out, provider{ID: id, Label: labelOf(id)})
	}
	return out
}

func stateWord(s provState) string {
	switch s {
	case provReady:
		return "ready"
	case provFailing:
		return "failing"
	case provNotConfigured:
		return "not configured"
	}
	return "unknown"
}

func (o *overlay) providerList(m model, w int) view {
	v := view{hint: hintProv}
	if !m.Server.Present {
		v.add(seg{notRegistred, styYellow})
		v.blank()
	}
	// Status of the doctor run itself, above the rows.
	var header [][]seg
	switch m.Doctor.Status {
	case doctorRunning:
		header = append(header, []seg{{"Checking providers with rea doctor --json …", styCyan}})
	case doctorFailed:
		header = append(header, []seg{{"rea doctor failed: " + m.Doctor.Err + " (r to retry)", styRed}})
	case doctorIdle:
		if m.Server.Present {
			header = append(header, []seg{{"Providers not checked yet (r to run rea doctor).", styDim}})
		}
	}
	if m.Doctor.Status == doctorDone && m.Doctor.Report != nil && m.Doctor.Report.FailedOther > 0 {
		header = append(header, []seg{{fmt.Sprintf("%d other doctor check(s) fail: registrations of other clients and the skill; not shown here.", m.Doctor.Report.FailedOther), styDim}})
	}
	for _, h := range header {
		v.add(h...)
	}
	base := len(v.rows)
	ps := providersFor(m)
	sel := o.clamp(len(ps))
	for _, p := range ps {
		glyph, style := providerMark(p.State)
		label := p.Label
		row := []seg{{" " + glyph + " ", style}, {padTo(label, 10), styBold}, {stateWord(p.State), style}}
		if p.Detail != "" && p.State != provNotConfigured {
			row = append(row, seg{" · " + p.Detail, styDim})
		}
		v.add(row...)
	}
	v.sel = base + sel
	return v
}

func (o *overlay) providerDetail(m model, w int) view {
	v := view{sel: -1, hint: hintScroll}
	ps := providersFor(m)
	p := ps[o.clamp(len(ps))]
	glyph, style := providerMark(p.State)
	v.add(seg{p.Label, styBold}, seg{"  " + glyph + " " + stateWord(p.State), style})
	if p.Version != "" {
		v.add(seg{"Version " + p.Version, styDim})
	}
	if p.Detail != "" {
		v.blank()
		v.wrapped("", p.Detail, "", w)
	}
	if p.Remediation != "" && p.Remediation != p.Detail {
		v.add(seg{"Fix", styDim})
		v.wrapped("  ", p.Remediation, styYellow, w)
	}
	if len(p.Checks) > 0 {
		v.blank()
		v.add(seg{"Doctor checks", styBold})
		for _, c := range p.Checks {
			mark, st := "✓", styGreen
			if !c.OK {
				mark, st = "✗", styRed
			}
			v.add(seg{"  " + mark + " ", st}, seg{c.Name, ""}, seg{orEmpty(" · ", c.Code), styDim})
		}
	}
	v.blank()
	v.add(seg{"Environment variables REA reads (names only; the dashboard never sets them)", styBold})
	for _, e := range providerEnv[p.ID] {
		v.add(envRow(e)...)
	}
	v.blank()
	v.add(seg{"For every provider", styDim})
	for _, e := range generalEnv {
		v.add(envRow(e)...)
	}
	v.blank()
	v.add(seg{"Set them in the environment pig starts in; the harness installs nothing.", styDim})
	return v
}

func envRow(e envVar) []seg {
	state, style := "unset", styDim
	if envSet(e.Name) {
		state, style = "set  ", styGreen
	}
	return []seg{{"  " + state + " ", style}, {e.Name, ""}, {"  " + e.Note, styDim}}
}

// ── About ────────────────────────────────────────────────────────────────────

func (o *overlay) about(m model, w int) view {
	v := view{sel: -1, hint: hintAbout}
	if !m.Server.Present {
		v.add(seg{notRegistred, styYellow})
		v.blank()
	}
	field := func(name, value string) {
		if value == "" {
			return
		}
		v.add(seg{padTo(name, 13), styDim}, seg{value, ""})
	}
	p := m.Server.Payload
	field("REA", orElse(m.version(), "unknown"))
	if p.Rea.Package != "" {
		field("Package", p.Rea.Package+orEmpty(" · tag ", p.Rea.Tag))
		field("Commit", p.Rea.Commit)
		if p.Rea.Dev != "" {
			field("Build", "development, REA commit "+p.Rea.Dev)
		}
	}
	// The version of the Package that supplied the dashboard; the SDK exposes no Piglet version.
	field("components/rea", m.Harness)
	field("Node", p.Node.Version)
	field("Target", p.Target)
	field("Payload", m.Server.Dir)
	field("Tree sha256", p.TreeSha256)
	if p.Files > 0 {
		field("Files", fmt.Sprintf("%d · %s", p.Files, humanBytes(p.Bytes)))
	}
	if m.Server.Present {
		field("MCP server", strings.TrimSpace(m.Server.Node+" "+m.Server.Entry+" mcp"))
	}
	if m.Server.Err != "" {
		field("Payload info", "unavailable: "+m.Server.Err)
	}
	if m.Doctor.Report != nil {
		field("Tools", fmt.Sprintf("%d MCP tools", m.Doctor.Report.Tools))
	}
	v.blank()
	v.add(seg{"Integrity: run /rea-runtime verify to check the unpacked tree against its digest.", styCyan})
	v.add(seg{"This dashboard reads REA only through events and one-shot CLI runs; it never sets REA's environment or opens a second MCP session.", styDim})
	// Rows that are not wrapped would be cut; wrap the long ones.
	var rows [][]seg
	for _, r := range v.rows {
		if segsWidth(r) <= w || len(r) != 1 {
			rows = append(rows, r)
			continue
		}
		for _, l := range wrapText(r[0].text, w) {
			rows = append(rows, []seg{{l, r[0].style}})
		}
	}
	v.rows = rows
	return v
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

func msDuration(ms int64) time.Duration { return time.Duration(ms) * time.Millisecond }
