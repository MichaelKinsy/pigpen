package readashboard

import "fmt"

// providerMark is the glyph, label style and meaning of a provider state.
func providerMark(s provState) (glyph, style string) {
	switch s {
	case provReady:
		return "✓", styGreen
	case provFailing:
		return "✗", styRed
	case provNotConfigured:
		return "○", styDim
	}
	return "?", styYellow
}

// headerLines is the header: the REA mark and versions, then one row of providers. With no rea server it is
// one line saying so.
func headerLines(m model, width int) []string {
	width = max(width, 1)
	mark := seg{" ◆ REA", styBold + ";" + styCyan}
	var lines [][]seg
	switch {
	case !m.Server.Present && m.Starting:
		lines = [][]seg{{mark, {" starting…", styDim}}}
	case !m.Server.Present:
		lines = [][]seg{{mark, {" not registered", styYellow}, {" · no \"rea\" MCP server (started with --no-mcp?)", styDim}}}
	default:
		title := []seg{mark}
		if v := m.version(); v != "" {
			title = append(title, seg{" " + v, styBold})
		}
		if m.Doctor.Report != nil && m.Doctor.Report.Tools > 0 {
			title = append(title, seg{fmt.Sprintf(" · %d tools", m.Doctor.Report.Tools), styDim})
		}
		lines = append(lines, title)
		lines = append(lines, providerRow(m))
		if m.Doctor.Status == doctorDone && m.Doctor.Report != nil {
			lines = append(lines, []seg{{" ✓ ready  ✗ failing  ○ not configured · /rea · alt+r", styDim}})
		}
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = renderSegs(l, width)
	}
	return out
}

func providerRow(m model) []seg {
	switch {
	case m.Server.Disabled:
		return []seg{{" server disabled in its registration", styYellow}}
	case m.Doctor.Status == doctorFailed:
		return []seg{{" providers: doctor failed · " + m.Doctor.Err, styRed}}
	case m.Doctor.Status != doctorDone || m.Doctor.Report == nil:
		return []seg{{" providers: checking…", styDim}}
	}
	row := []seg{{" ", ""}}
	for i, p := range m.Doctor.Report.Providers {
		glyph, style := providerMark(p.State)
		if i > 0 {
			row = append(row, seg{"  ", ""})
		}
		row = append(row, seg{p.Label + " ", ""}, seg{glyph, style})
	}
	return row
}
