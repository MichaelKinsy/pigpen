package ohmyhermes

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/memory"
	hermespaths "github.com/MichaelKinsy/pigpen/components/pig-hermes-core/paths"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/usage"
)

// --- Shared terminal / box / text helpers ---

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func padRightCols(s string, width int) string {
	if width <= 0 {
		return ""
	}
	w := tui.VisibleWidth(s)
	if w > width {
		return tui.TruncateToWidth(s, width, "…", true)
	}
	if w == width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}

func padLeftCols(s string, width int) string {
	if width <= 0 {
		return ""
	}
	w := tui.VisibleWidth(s)
	if w > width {
		return tui.TruncateToWidth(s, width, "…", false)
	}
	if w == width {
		return s
	}
	return strings.Repeat(" ", width-w) + s
}

func middleTruncate(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if tui.VisibleWidth(s) <= maxWidth {
		return s
	}
	runes := []rune(s)
	if maxWidth <= 3 {
		return tui.TruncateToWidth(s, maxWidth, "", false)
	}
	leftLen := (maxWidth - 3) / 2
	rightLen := maxWidth - 3 - leftLen
	if leftLen < 1 {
		leftLen = 1
	}
	if rightLen < 1 {
		rightLen = 1
	}
	if leftLen+rightLen >= len(runes) {
		return tui.TruncateToWidth(s, maxWidth, "…", false)
	}
	return string(runes[:leftLen]) + " … " + string(runes[len(runes)-rightLen:])
}

func fitLine(s string, width int) string {
	if width <= 0 {
		return s
	}
	if tui.VisibleWidth(s) <= width {
		return s
	}
	return tui.TruncateToWidth(s, width, "…", false)
}

func roundedBox(titleLeft, titleRight string, body []string, width int, accent bool) []string {
	if width < 8 {
		width = 8
	}
	inner := width - 4 // "│ " + inner + " │"
	topLeft := "╭─"
	topRight := "─╮"
	if accent {
		topLeft = "╭━"
		topRight = "━╮"
	}
	fillCh := "─"
	if accent {
		fillCh = "━"
	}

	leftPart := ""
	if titleLeft != "" {
		leftPart = " " + titleLeft + " "
	}
	rightPart := ""
	if titleRight != "" {
		rightPart = " " + titleRight + " "
	}
	avail := width - 4 // minus topLeft(2) and topRight(2)
	lw := tui.VisibleWidth(leftPart)
	rw := tui.VisibleWidth(rightPart)
	if lw+rw > avail {
		if rw > avail/2 {
			rightPart = tui.TruncateToWidth(rightPart, avail/2, "…", false)
			rw = tui.VisibleWidth(rightPart)
		}
		if lw+rw > avail {
			leftPart = tui.TruncateToWidth(leftPart, max(0, avail-rw), "…", false)
			lw = tui.VisibleWidth(leftPart)
		}
	}
	midFill := avail - lw - rw
	if midFill < 0 {
		midFill = 0
	}
	lines := make([]string, 0, len(body)+2)
	lines = append(lines, topLeft+leftPart+strings.Repeat(fillCh, midFill)+rightPart+topRight)
	side := "│"
	if accent {
		side = "┃"
	}
	for _, b := range body {
		lines = append(lines, side+" "+padRightCols(b, inner)+" "+side)
	}
	botLeft, botRight := "╰", "╯"
	if accent {
		botLeft, botRight = "╰", "╯"
	}
	lines = append(lines, botLeft+strings.Repeat(fillCh, width-2)+botRight)
	return lines
}

func formatCompactTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000.0)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000.0)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func formatAge(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func gaugeBar(pct, cells int) string {
	if cells <= 0 {
		cells = 10
	}
	pct = clampInt(pct, 0, 100)
	filled := (pct*cells + 50) / 100
	filled = clampInt(filled, 0, cells)
	return strings.Repeat("▰", filled) + strings.Repeat("▱", cells-filled)
}

func sparkline24(buckets [24]int) string {
	glyphs := []rune("▁▂▃▄▅▆▇█")
	maxVal := 0
	for _, v := range buckets {
		if v > maxVal {
			maxVal = v
		}
	}
	var b strings.Builder
	b.Grow(24 * 3)
	for _, v := range buckets {
		if maxVal == 0 || v <= 0 {
			b.WriteRune(glyphs[0])
			continue
		}
		idx := (v * (len(glyphs) - 1)) / maxVal
		idx = clampInt(idx, 0, len(glyphs)-1)
		b.WriteRune(glyphs[idx])
	}
	return b.String()
}

func dagStatusGlyph(status string) (string, string) {
	switch status {
	case "completed", "done":
		return "✓", "Done"
	case "running":
		return "●", "Running"
	case "failed":
		return "✗", "Failed"
	case "skipped", "interrupted":
		return "▪", "Skipped"
	case "pending", "waiting":
		return "○", "Waiting"
	default:
		return "◇", status
	}
}

// ============================================================================
// 1. Fullscreen /dag Box-Frontier Graph Pager (dagPagerComponent)
// ============================================================================

type dagPagerComponent struct {
	mu           sync.Mutex
	surface      *surfaceState
	runID        string
	runs         []*DAGRun
	runIdx       int
	run          *DAGRun
	layers       [][]string
	nodeByID     map[string]DAGNode
	order        []string
	selectedIdx  int
	folded       map[string]bool
	showDetails  bool
	showEvidence bool
	scrollTop    int
	viewHeight   int
	lastLines    int
}

func newDAGPagerComponent(s *surfaceState, runID string) *dagPagerComponent {
	c := &dagPagerComponent{
		surface:     s,
		runID:       strings.TrimSpace(runID),
		folded:      make(map[string]bool),
		showDetails: true,
		viewHeight:  28,
	}
	c.reloadLocked()
	return c
}

func sortDAGRunsDeterministic(runs []*DAGRun) {
	sort.SliceStable(runs, func(i, j int) bool {
		ti := runs[i].UpdatedAt
		if ti == "" {
			ti = runs[i].CreatedAt
		}
		tj := runs[j].UpdatedAt
		if tj == "" {
			tj = runs[j].CreatedAt
		}
		if ti != tj {
			return ti < tj
		}
		return runs[i].RunID < runs[j].RunID
	})
}

func (c *dagPagerComponent) reloadLocked() {
	var run *DAGRun
	c.runs = nil
	c.runIdx = 0
	if runs, err := c.surface.dags.List(); err == nil && len(runs) > 0 {
		sortDAGRunsDeterministic(runs)
		c.runs = runs
	}
	if c.runID != "" {
		for i, r := range c.runs {
			if r.RunID == c.runID {
				run = r
				c.runIdx = i
				break
			}
		}
		if run == nil {
			if r, err := c.surface.dags.Get(c.runID); err == nil {
				run = r
			}
		}
	}
	if run == nil && len(c.runs) > 0 {
		// Prefer the latest active run by UpdatedAt/CreatedAt (RunID tie-break), otherwise the latest run.
		for i := len(c.runs) - 1; i >= 0; i-- {
			if c.runs[i].Status == "active" {
				run = c.runs[i]
				c.runIdx = i
				break
			}
		}
		if run == nil {
			c.runIdx = len(c.runs) - 1
			run = c.runs[c.runIdx]
		}
		c.runID = run.RunID
	}
	c.run = run
	c.nodeByID = make(map[string]DAGNode)
	c.layers = nil
	c.order = nil
	if run == nil {
		c.selectedIdx = 0
		return
	}
	for _, n := range run.Nodes {
		c.nodeByID[n.ID] = n
	}
	c.layers = computeLongestPathFrontiers(run.Nodes)
	for _, layer := range c.layers {
		c.order = append(c.order, layer...)
	}
	if len(c.order) == 0 {
		c.selectedIdx = 0
		return
	}
	if c.selectedIdx >= len(c.order) {
		c.selectedIdx = len(c.order) - 1
	}
	// Default initial focus to first running node when starting fresh.
	if c.selectedIdx == 0 {
		for i, id := range c.order {
			if c.nodeByID[id].Status == "running" {
				c.selectedIdx = i
				break
			}
		}
	}
}

// computeLongestPathFrontiers assigns each node to layer = 1 + max(layer(parent)),
// falling back cleanly to a single layer if a graph is somehow cyclic or malformed.
func computeLongestPathFrontiers(nodes []DAGNode) [][]string {
	if len(nodes) == 0 {
		return nil
	}
	byID := make(map[string]DAGNode, len(nodes))
	orderIdx := make(map[string]int, len(nodes))
	for i, n := range nodes {
		byID[n.ID] = n
		orderIdx[n.ID] = i
	}

	memo := make(map[string]int, len(nodes))
	visiting := make(map[string]bool, len(nodes))
	cyclic := false

	var depthOf func(id string) int
	depthOf = func(id string) int {
		if d, ok := memo[id]; ok {
			return d
		}
		if visiting[id] {
			cyclic = true
			return 0
		}
		visiting[id] = true
		n := byID[id]
		best := 0
		for _, dep := range n.DependsOn {
			if _, exists := byID[dep]; !exists {
				continue
			}
			cand := depthOf(dep) + 1
			if cand > best {
				best = cand
			}
		}
		delete(visiting, id)
		memo[id] = best
		return best
	}

	maxDepth := 0
	for _, n := range nodes {
		d := depthOf(n.ID)
		if d > maxDepth {
			maxDepth = d
		}
	}
	if cyclic {
		ids := make([]string, len(nodes))
		for i, n := range nodes {
			ids[i] = n.ID
		}
		return [][]string{ids}
	}

	layers := make([][]string, maxDepth+1)
	for _, n := range nodes {
		d := memo[n.ID]
		layers[d] = append(layers[d], n.ID)
	}
	for i := range layers {
		sort.Slice(layers[i], func(a, b int) bool {
			return orderIdx[layers[i][a]] < orderIdx[layers[i][b]]
		})
	}
	return layers
}

func (c *dagPagerComponent) selectedNodeIDLocked() string {
	if c.selectedIdx < 0 || c.selectedIdx >= len(c.order) {
		return ""
	}
	return c.order[c.selectedIdx]
}

func (c *dagPagerComponent) modelLabelForNode(n DAGNode) string {
	cat := strings.TrimSpace(n.Category)
	if cat == "" && n.Role != "" {
		if role, ok := LookupBuiltinRole(n.Role); ok {
			cat = role.DefaultCategory
		}
	}
	if cat == "" {
		cat = "default"
	}
	if c.surface != nil && c.surface.router != nil {
		if chain, _, err := c.surface.router.ChainForCategory(cat); err == nil && len(chain) > 0 {
			head := chain[0]
			if head.ThinkingLevel != "" {
				return fmt.Sprintf("%s · %s:%s", cat, head.Model, head.ThinkingLevel)
			}
			return fmt.Sprintf("%s · %s", cat, head.Model)
		}
	}
	return cat
}

func (c *dagPagerComponent) renderNodeBoxLocked(id string, innerWidth int) []string {
	n := c.nodeByID[id]
	selected := id == c.selectedNodeIDLocked()
	glyph, statusLabel := dagStatusGlyph(n.Status)
	title := strings.TrimSpace(n.Title)
	if title == "" {
		title = n.ID
	}
	ptr := ""
	if selected {
		ptr = "> "
	}

	var body []string
	if c.folded[id] {
		line := fmt.Sprintf("[+] %s%s %s · %s", ptr, glyph, title, statusLabel)
		body = []string{middleTruncate(line, innerWidth)}
	} else {
		line1 := fmt.Sprintf("[-] %s%s", ptr, title)
		line2 := c.modelLabelForNode(n)
		line3 := fmt.Sprintf("%s %s", glyph, statusLabel)
		line4 := "Start node"
		if len(n.DependsOn) > 0 {
			line4 = "← " + strings.Join(n.DependsOn, ", ")
		}
		body = []string{
			middleTruncate(line1, innerWidth),
			fitLine(line2, innerWidth),
			fitLine(line3, innerWidth),
			fitLine(line4, innerWidth),
		}
	}

	boxW := innerWidth + 4
	return roundedBox("", "", body, boxW, selected)
}

func (c *dagPagerComponent) renderFrontierRowLocked(layer []string, width int) []string {
	if len(layer) == 0 {
		return nil
	}
	const gap = 2
	const minInner = 22
	const maxInner = 48

	// Fit all boxes in one row when possible, otherwise wrap rows cleanly.
	perRow := len(layer)
	inner := maxInner
	for perRow > 1 {
		avail := width - (perRow-1)*gap
		cand := avail/perRow - 4
		if cand >= minInner {
			inner = clampInt(cand, minInner, maxInner)
			break
		}
		perRow--
	}
	if perRow == 1 {
		inner = clampInt(width-4, 14, maxInner)
	}

	var out []string
	for start := 0; start < len(layer); start += perRow {
		end := min(start+perRow, len(layer))
		chunk := layer[start:end]
		boxes := make([][]string, len(chunk))
		maxH := 0
		for i, id := range chunk {
			boxes[i] = c.renderNodeBoxLocked(id, inner)
			if len(boxes[i]) > maxH {
				maxH = len(boxes[i])
			}
		}
		boxW := inner + 4
		spacer := strings.Repeat(" ", gap)
		for row := range maxH {
			var parts []string
			for i := range boxes {
				if row < len(boxes[i]) {
					parts = append(parts, boxes[i][row])
				} else {
					parts = append(parts, strings.Repeat(" ", boxW))
				}
			}
			out = append(out, fitLine(strings.Join(parts, spacer), width))
		}
	}
	if len(layer) > 1 {
		out = append(out, fitLine("  · Same frontier", width))
	}
	return out
}

func (c *dagPagerComponent) renderConnectorRows(prevCount, nextCount, width int) []string {
	if prevCount <= 0 || nextCount <= 0 {
		return nil
	}
	mid := clampInt(width/4, 4, 24)
	if prevCount == 1 && nextCount == 1 {
		pad := strings.Repeat(" ", mid)
		return []string{
			pad + "│",
			pad + "▼",
		}
	}
	span := clampInt(width/3, 6, 30)
	leftPad := strings.Repeat(" ", max(2, mid-span/2))
	bar := leftPad + "╰" + strings.Repeat("─", max(1, span/2-1)) + "┬" + strings.Repeat("─", max(1, span/2-1)) + "╯"
	arrow := strings.Repeat(" ", max(2, mid-span/2)+max(1, span/2)) + "▼"
	return []string{
		fitLine(strings.Repeat(" ", max(2, mid-span/2))+"│"+strings.Repeat(" ", max(1, span-1))+"│", width),
		fitLine(bar, width),
		fitLine(arrow, width),
	}
}

func (c *dagPagerComponent) buildGraphContentLocked(width int) []string {
	if c.run == nil {
		return []string{
			fitLine("OMH / DAG · 0 tasks", width),
			"",
			fitLine("No OMH DAG runs recorded.", width),
		}
	}

	doneCount, failCount, runCount := 0, 0, 0
	for _, n := range c.run.Nodes {
		switch n.Status {
		case "completed":
			doneCount++
		case "failed":
			failCount++
		case "running":
			runCount++
		}
	}

	taskWord := "tasks"
	if len(c.run.Nodes) == 1 {
		taskWord = "task"
	}
	summary := fmt.Sprintf("%s · Done %d/%d", strings.ToUpper(c.run.Status[:1])+c.run.Status[1:], doneCount, len(c.run.Nodes))
	if runCount > 0 {
		summary += fmt.Sprintf(" · %d running", runCount)
	}
	if failCount > 0 {
		summary += fmt.Sprintf(" · %d failed", failCount)
	}

	var lines []string
	lines = append(lines,
		fitLine(fmt.Sprintf("OMH / DAG · %d %s", len(c.run.Nodes), taskWord), width),
		fitLine(fmt.Sprintf("%s (%s)", c.run.Title, c.run.RunID), width),
		fitLine(summary, width),
		"",
	)

	for i, layer := range c.layers {
		lines = append(lines, c.renderFrontierRowLocked(layer, width)...)
		if i+1 < len(c.layers) {
			lines = append(lines, c.renderConnectorRows(len(layer), len(c.layers[i+1]), width)...)
		}
	}

	// Dependencies summary
	var deps []string
	for _, n := range c.run.Nodes {
		for _, dep := range n.DependsOn {
			deps = append(deps, fmt.Sprintf("  %s → %s", dep, n.ID))
		}
	}
	if len(deps) > 0 {
		lines = append(lines, "", fitLine("Dependencies", width))
		for _, d := range deps {
			lines = append(lines, fitLine(d, width))
		}
	}

	// Toggleable Node details section
	if c.showDetails {
		lines = append(lines, "", fitLine("Node details", width))
		selID := c.selectedNodeIDLocked()
		seen := make(map[string]bool)
		var detailIDs []string
		if selID != "" {
			detailIDs = append(detailIDs, selID)
			seen[selID] = true
		}
		for _, n := range c.run.Nodes {
			if n.Status == "running" && !seen[n.ID] {
				detailIDs = append(detailIDs, n.ID)
				seen[n.ID] = true
			}
		}
		for _, id := range detailIDs {
			n := c.nodeByID[id]
			glyph, statusLabel := dagStatusGlyph(n.Status)
			card := []string{
				fmt.Sprintf("node:   %s — %s", n.ID, n.Title),
				fmt.Sprintf("route:  %s", c.modelLabelForNode(n)),
				fmt.Sprintf("status: %s %s", glyph, statusLabel),
			}
			if len(n.DependsOn) > 0 {
				card = append(card, fmt.Sprintf("deps:   %s", strings.Join(n.DependsOn, ", ")))
			}
			if len(n.WriteScope) > 0 {
				card = append(card, fmt.Sprintf("scope:  %s", strings.Join(n.WriteScope, ", ")))
			}
			if n.Result != "" {
				card = append(card, fmt.Sprintf("result: %s", n.Result))
			}
			if n.Error != "" {
				card = append(card, fmt.Sprintf("error:  %s", n.Error))
			}
			lines = append(lines, roundedBox("detail · "+n.ID, "", card, width, id == selID)...)
		}
	}
	return lines
}

func (c *dagPagerComponent) buildEvidenceContentLocked(width int) []string {
	selID := c.selectedNodeIDLocked()
	if c.run == nil || selID == "" {
		return []string{fitLine("No node selected for evidence.", width)}
	}
	n := c.nodeByID[selID]
	glyph, statusLabel := dagStatusGlyph(n.Status)
	var lines []string
	lines = append(lines,
		fitLine(fmt.Sprintf("OMH / DAG · Node evidence · %s", n.ID), width),
		fitLine(fmt.Sprintf("%s (%s)", c.run.Title, c.run.RunID), width),
		"",
	)
	card := []string{
		fmt.Sprintf("title:  %s", n.Title),
		fmt.Sprintf("status: %s %s", glyph, statusLabel),
		fmt.Sprintf("route:  %s", c.modelLabelForNode(n)),
	}
	if n.Role != "" {
		card = append(card, fmt.Sprintf("role:   %s", n.Role))
	}
	if len(n.DependsOn) > 0 {
		card = append(card, fmt.Sprintf("deps:   %s", strings.Join(n.DependsOn, ", ")))
	} else {
		card = append(card, "deps:   Start node")
	}
	if len(n.WriteScope) > 0 {
		card = append(card, fmt.Sprintf("scope:  %s", strings.Join(n.WriteScope, ", ")))
	}
	if n.UpdatedAt != "" {
		card = append(card, fmt.Sprintf("updated: %s", n.UpdatedAt))
	}
	lines = append(lines, roundedBox("handoff · "+n.ID, statusLabel, card, width, true)...)

	lines = append(lines, "")
	if n.Result != "" {
		lines = append(lines, fitLine("Observed result:", width))
		for _, wl := range tui.WrapTextWithAnsi(n.Result, max(16, width-4)) {
			lines = append(lines, fitLine("  "+wl, width))
		}
	} else if n.Error != "" {
		lines = append(lines, fitLine("Recorded error:", width))
		for _, wl := range tui.WrapTextWithAnsi(n.Error, max(16, width-4)) {
			lines = append(lines, fitLine("  "+wl, width))
		}
	} else {
		lines = append(lines, fitLine("No handoff output recorded for this node yet.", width))
	}
	return lines
}

func (c *dagPagerComponent) Render(width int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if width < 24 {
		width = 24
	}

	var content []string
	title := "delegation graph"
	footerHints := "e Evidence  Tab/n next  Space/Enter fold  d Details  ↑↓ Scroll  q Close"
	if len(c.runs) > 1 {
		footerHints = fmt.Sprintf("e Evidence  [/] Run %d/%d  Tab/n next  Space/Enter fold  d Details  ↑↓ Scroll  q Close", c.runIdx+1, len(c.runs))
	}
	if c.showEvidence {
		content = c.buildEvidenceContentLocked(width)
		title = "node evidence"
		footerHints = "e Graph  r Refresh  ↑↓ Scroll  q Close"
		if len(c.runs) > 1 {
			footerHints = fmt.Sprintf("e Graph  [/] Run %d/%d  r Refresh  ↑↓ Scroll  q Close", c.runIdx+1, len(c.runs))
		}
	} else {
		content = c.buildGraphContentLocked(width)
	}

	total := len(content)
	c.lastLines = total
	bodyRows := c.viewHeight
	if bodyRows < 6 {
		bodyRows = 6
	}
	maxScroll := max(0, total-bodyRows)
	c.scrollTop = clampInt(c.scrollTop, 0, maxScroll)

	end := min(total, c.scrollTop+bodyRows)
	visible := make([]string, 0, bodyRows+3)
	visible = append(visible, content[c.scrollTop:end]...)
	firstRow := 0
	if total > 0 {
		firstRow = c.scrollTop + 1
	}
	visible = append(visible,
		strings.Repeat("─", width),
		fitLine(footerHints, width),
		fitLine(fmt.Sprintf("%s · rows %d-%d/%d", title, firstRow, end, total), width),
	)
	return visible
}

func (c *dagPagerComponent) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	if tui.IsKeyRelease(data) {
		return sdk.RemoteComponentResult{}, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	key, _ := tui.ParseKey(data)
	switch {
	case tui.MatchesKey(data, tui.Key.Escape), tui.MatchesKey(data, tui.Key.Ctrl("c")), key == "q", data == "q", data == "Q":
		return sdk.RemoteComponentResult{Done: true, Value: true}, nil
	case key == "e", data == "e", data == "E":
		c.showEvidence = !c.showEvidence
		c.scrollTop = 0
	case key == "r", data == "r", data == "R":
		c.reloadLocked()
	case key == "[", data == "[":
		if len(c.runs) > 1 {
			c.runIdx = (c.runIdx - 1 + len(c.runs)) % len(c.runs)
			c.runID = c.runs[c.runIdx].RunID
			c.selectedIdx = 0
			c.scrollTop = 0
			c.reloadLocked()
		}
	case key == "]", data == "]":
		if len(c.runs) > 1 {
			c.runIdx = (c.runIdx + 1) % len(c.runs)
			c.runID = c.runs[c.runIdx].RunID
			c.selectedIdx = 0
			c.scrollTop = 0
			c.reloadLocked()
		}
	case !c.showEvidence && (tui.MatchesKey(data, tui.Key.Tab) || key == "n" || data == "n"):
		if len(c.order) > 0 {
			c.selectedIdx = (c.selectedIdx + 1) % len(c.order)
		}
	case !c.showEvidence && (tui.MatchesKey(data, tui.Key.Shift(tui.Key.Tab)) || key == "p" || data == "p"):
		if len(c.order) > 0 {
			c.selectedIdx = (c.selectedIdx - 1 + len(c.order)) % len(c.order)
		}
	case !c.showEvidence && (tui.MatchesKey(data, tui.Key.Space) || tui.MatchesKey(data, tui.Key.Enter) || data == " "):
		if id := c.selectedNodeIDLocked(); id != "" {
			c.folded[id] = !c.folded[id]
		}
	case !c.showEvidence && (key == "d" || data == "d" || data == "D"):
		c.showDetails = !c.showDetails
	case tui.MatchesKey(data, tui.Key.Down), key == "j", data == "j":
		if !c.showEvidence && len(c.order) > 0 {
			if c.selectedIdx+1 < len(c.order) {
				c.selectedIdx++
			} else {
				c.scrollTop++
			}
		} else {
			c.scrollTop++
		}
	case tui.MatchesKey(data, tui.Key.Up), key == "k", data == "k":
		if !c.showEvidence && len(c.order) > 0 {
			if c.selectedIdx > 0 {
				c.selectedIdx--
			} else if c.scrollTop > 0 {
				c.scrollTop--
			}
		} else if c.scrollTop > 0 {
			c.scrollTop--
		}
	case tui.MatchesKey(data, tui.Key.PageDown):
		c.scrollTop += max(1, c.viewHeight/2)
	case tui.MatchesKey(data, tui.Key.PageUp):
		c.scrollTop = max(0, c.scrollTop-max(1, c.viewHeight/2))
	case tui.MatchesKey(data, tui.Key.Home), key == "g", data == "g":
		c.scrollTop = 0
	case tui.MatchesKey(data, tui.Key.End), data == "G":
		c.scrollTop = max(0, c.lastLines-c.viewHeight)
	}
	return sdk.RemoteComponentResult{}, nil
}

// ============================================================================
// 2. Fullscreen /bots 3-Level Live Multi-Profile Board (botsBoardComponent)
// ============================================================================

type botSessionRow struct {
	ID        string
	Title     string
	Model     string
	Source    string
	UpdatedAt time.Time
	Msgs      int
	InTokens  int
	OutTokens int
	Cost      float64
	Messages  []botMessageRow
}

type botMessageRow struct {
	Timestamp time.Time
	Role      string
	Text      string
}

type botProfileCard struct {
	Name       string
	Home       string
	GatewayUp  bool
	ActiveNow  bool
	Model      string
	Newest     string
	MemoryPct  int
	UserPct    int
	OMHRecords int
	Hourly     [24]int
	Sessions   []botSessionRow
}

type botsBoardComponent struct {
	mu           sync.Mutex
	surface      *surfaceState
	level        int // 1 = profiles, 2 = profile sessions, 3 = session messages
	profiles     []botProfileCard
	profileIdx   int
	sessionIdx   int
	scrollTop    int
	viewHeight   int
	lastLines    int
	lastRefreshed time.Time
}

func newBotsBoardComponent(s *surfaceState) *botsBoardComponent {
	c := &botsBoardComponent{
		surface:    s,
		level:      1,
		viewHeight: 28,
	}
	c.refreshLocked()
	return c
}

func (c *botsBoardComponent) refreshLocked() {
	c.lastRefreshed = time.Now()
	summaries := c.surface.collectProfileSummaries()
	activeProfile := hermespaths.ResolveProfilePaths("").ProfileName

	usageRecs := usage.Default().Snapshot()
	sort.Slice(usageRecs, func(i, j int) bool {
		if !usageRecs[i].UpdatedAt.Equal(usageRecs[j].UpdatedAt) {
			return usageRecs[i].UpdatedAt.After(usageRecs[j].UpdatedAt)
		}
		return usageRecs[i].SessionID < usageRecs[j].SessionID
	})

	teams, _ := c.surface.teams.List()
	dags, _ := c.surface.dags.List()
	loops, _ := c.surface.loops.List()
	delegations := c.surface.teams.Delegations()
	omhRecordTotal := len(dags) + len(teams) + len(loops) + len(delegations)

	now := time.Now().UTC()
	var activeHourly [24]int
	var activeSessions []botSessionRow
	for _, rec := range usageRecs {
		if !rec.UpdatedAt.IsZero() {
			hrsAgo := int(now.Sub(rec.UpdatedAt).Hours())
			if hrsAgo >= 0 && hrsAgo < 24 {
				activeHourly[23-hrsAgo] += max(1, rec.Turns)
			}
		}
		src := "tui"
		if rec.ParentID != "" {
			src = "subagent"
		}
		msgs := []botMessageRow{
			{
				Timestamp: rec.StartedAt,
				Role:      "system",
				Text:      fmt.Sprintf("session %s started (model=%s, source=%s)", rec.SessionID, nonEmpty(rec.Model, "default"), src),
			},
			{
				Timestamp: rec.UpdatedAt,
				Role:      "assistant",
				Text:      fmt.Sprintf("turns=%d tool_calls=%d tokens=%d/%d cost=$%.4f", rec.Turns, rec.ToolCalls, rec.InputTokens, rec.OutputTokens, rec.Cost),
			},
		}
		activeSessions = append(activeSessions, botSessionRow{
			ID:        rec.SessionID,
			Title:     rec.SessionID,
			Model:     nonEmpty(rec.Model, "default"),
			Source:    src,
			UpdatedAt: rec.UpdatedAt,
			Msgs:      max(rec.Turns*2, len(msgs)),
			InTokens:  rec.InputTokens,
			OutTokens: rec.OutputTokens,
			Cost:      rec.Cost,
			Messages:  msgs,
		})
	}
	for _, tm := range teams {
		var msgs []botMessageRow
		for _, lane := range tm.Lanes {
			msgs = append(msgs, botMessageRow{
				Timestamp: now,
				Role:      lane.Status,
				Text:      fmt.Sprintf("lane %s (%s): %s %s", lane.LaneID, nonEmpty(lane.Category, "default"), lane.Title, lane.Result),
			})
		}
		if len(msgs) == 0 {
			msgs = append(msgs, botMessageRow{Timestamp: now, Role: "team", Text: tm.Title})
		}
		activeSessions = append(activeSessions, botSessionRow{
			ID:        tm.TeamID,
			Title:     fmt.Sprintf("[team] %s", nonEmpty(tm.Title, tm.TeamID)),
			Model:     "multi-lane",
			Source:    "team",
			UpdatedAt: now,
			Msgs:      len(tm.Lanes),
			Messages:  msgs,
		})
	}
	for _, d := range delegations {
		activeSessions = append(activeSessions, botSessionRow{
			ID:        d.TaskID,
			Title:     fmt.Sprintf("[deleg] %s", d.TaskID),
			Model:     nonEmpty(d.Model, "default"),
			Source:    "delegation",
			UpdatedAt: now,
			Msgs:      1,
			InTokens:  d.InputTokens,
			OutTokens: d.OutputTokens,
			Messages: []botMessageRow{
				{
					Timestamp: now,
					Role:      d.Status,
					Text:      fmt.Sprintf("child=%s cat=%s model=%s dur=%dms", d.ChildSessionID, d.Category, d.Model, d.DurationMs),
				},
			},
		})
	}

	cards := make([]botProfileCard, 0, len(summaries))
	for _, sum := range summaries {
		isActive := sum.Name == activeProfile
		card := botProfileCard{
			Name:      sum.Name,
			Home:      sum.Home,
			GatewayUp: isActive && (len(activeSessions) > 0 || sum.ActiveDAGs > 0 || sum.ActiveTeams > 0),
			ActiveNow: isActive,
			Model:     "default",
			Newest:    "none",
			MemoryPct: sum.MemoryPct,
			UserPct:   sum.UserPct,
		}
		// Count profile memory files if not active, or full OMH records on active profile.
		if isActive {
			card.OMHRecords = omhRecordTotal
			card.Hourly = activeHourly
			card.Sessions = activeSessions
			if len(activeSessions) > 0 {
				card.Model = activeSessions[0].Model
				card.Newest = activeSessions[0].Title
			}
		} else {
			card.MemoryPct = memoryFileFillPercent(filepath.Join(sum.Home, "memories", "MEMORY.md"), memory.DefaultMemoryCharLimit)
			card.UserPct = memoryFileFillPercent(filepath.Join(sum.Home, "memories", "USER.md"), memory.DefaultUserCharLimit)
		}
		cards = append(cards, card)
	}
	c.profiles = cards
	if len(c.profiles) == 0 {
		c.profileIdx = 0
		c.sessionIdx = 0
		c.level = 1
		return
	}
	c.profileIdx = clampInt(c.profileIdx, 0, len(c.profiles)-1)
	sessCount := len(c.profiles[c.profileIdx].Sessions)
	if sessCount == 0 {
		c.sessionIdx = 0
		if c.level == 3 {
			c.level = 2
		}
	} else {
		c.sessionIdx = clampInt(c.sessionIdx, 0, sessCount-1)
	}
}

func nonEmpty(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

func (c *botsBoardComponent) renderLevel1Locked(width int) []string {
	activeCount := 0
	for _, p := range c.profiles {
		if p.GatewayUp || p.ActiveNow {
			activeCount++
		}
	}
	headerTitle := fmt.Sprintf("Hermes bots · %d profiles · %d active", len(c.profiles), activeCount)
	inTok, outTok, cost, turns, _ := usage.Default().Totals()
	headerBody := []string{
		fmt.Sprintf("usage: %s in · %s out · $%.4f · %d turns", formatCompactTokens(inTok), formatCompactTokens(outTok), cost, turns),
	}
	lines := roundedBox(headerTitle, "", headerBody, width, false)

	for i, p := range c.profiles {
		selected := i == c.profileIdx
		dot := "○"
		if p.GatewayUp {
			dot = "●"
		}
		ptr := "  "
		if selected {
			ptr = "❯ "
		}
		titleLeft := fmt.Sprintf("%s%s %s", ptr, dot, p.Name)
		if p.ActiveNow {
			titleLeft += " (this TUI)"
		}
		titleRight := fmt.Sprintf("%d sessions", len(p.Sessions))
		spark := sparkline24(p.Hourly)
		memGauge := fmt.Sprintf("%s %3d%%", gaugeBar(p.MemoryPct, 10), p.MemoryPct)
		userGauge := fmt.Sprintf("%s %3d%%", gaugeBar(p.UserPct, 10), p.UserPct)

		body := []string{
			fmt.Sprintf("model   %-22s  24 h   %s", fitLine(p.Model, 22), spark),
			fmt.Sprintf("newest  %-22s  memory %s", fitLine(p.Newest, 22), memGauge),
			fmt.Sprintf("home    %-22s  user   %s", fitLine(p.Home, 22), userGauge),
			fmt.Sprintf("OMH     %d records", p.OMHRecords),
		}
		if width < 72 {
			body = []string{
				fmt.Sprintf("model  %s", p.Model),
				fmt.Sprintf("newest %s", p.Newest),
				fmt.Sprintf("24 h   %s", spark),
				fmt.Sprintf("mem %s  usr %s", memGauge, userGauge),
				fmt.Sprintf("OMH    %d records", p.OMHRecords),
			}
		}
		lines = append(lines, "")
		lines = append(lines, roundedBox(titleLeft, titleRight, body, width, selected)...)
	}
	return lines
}

func (c *botsBoardComponent) renderLevel2Locked(width int) []string {
	if len(c.profiles) == 0 {
		return []string{fitLine("No profiles found.", width)}
	}
	p := c.profiles[c.profileIdx]
	dot := "○"
	if p.GatewayUp {
		dot = "●"
	}
	profBody := []string{
		fmt.Sprintf("home    %s", p.Home),
		fmt.Sprintf("model   %s   OMH records: %d", p.Model, p.OMHRecords),
		fmt.Sprintf("memory  %s %3d%%   user %s %3d%%", gaugeBar(p.MemoryPct, 10), p.MemoryPct, gaugeBar(p.UserPct, 10), p.UserPct),
		fmt.Sprintf("24 h    %s", sparkline24(p.Hourly)),
	}
	lines := roundedBox(fmt.Sprintf("%s %s", dot, p.Name), "profile", profBody, width, false)
	lines = append(lines, "")

	inner := max(24, width-4)
	titleCol := clampInt(inner-38, 14, 34)
	if width < 70 {
		titleCol = clampInt(inner-24, 12, 22)
	}
	var tableBody []string
	hdr := fmt.Sprintf("  %s │ %-12s  %-8s  %4s  %4s", padRightCols("title", titleCol), "model", "source", "age", "msgs")
	if width < 70 {
		hdr = fmt.Sprintf("  %s │ %-10s  %4s  %4s", padRightCols("title", titleCol), "model", "age", "msgs")
	}
	tableBody = append(tableBody, fitLine(hdr, inner))
	tableBody = append(tableBody, strings.Repeat("─", inner))
	if len(p.Sessions) == 0 {
		tableBody = append(tableBody, "  (no sessions, teams, or delegations recorded)")
	} else {
		for i, sess := range p.Sessions {
			ptr := "  "
			if i == c.sessionIdx {
				ptr = "❯ "
			}
			var row string
			if width < 70 {
				row = fmt.Sprintf("%s%s │ %-10s  %4s  %4d",
					ptr,
					padRightCols(sess.Title, titleCol),
					fitLine(sess.Model, 10),
					formatAge(sess.UpdatedAt),
					sess.Msgs,
				)
			} else {
				row = fmt.Sprintf("%s%s │ %-12s  %-8s  %4s  %4d",
					ptr,
					padRightCols(sess.Title, titleCol),
					fitLine(sess.Model, 12),
					fitLine(sess.Source, 8),
					formatAge(sess.UpdatedAt),
					sess.Msgs,
				)
			}
			tableBody = append(tableBody, fitLine(row, inner))
		}
	}
	lines = append(lines, roundedBox(fmt.Sprintf("Sessions & Delegations (%d)", len(p.Sessions)), p.Name, tableBody, width, true)...)
	return lines
}

func (c *botsBoardComponent) renderLevel3Locked(width int) []string {
	if len(c.profiles) == 0 || len(c.profiles[c.profileIdx].Sessions) == 0 {
		return []string{fitLine("No session selected.", width)}
	}
	p := c.profiles[c.profileIdx]
	sess := p.Sessions[c.sessionIdx]

	sumBody := []string{
		fmt.Sprintf("%s · %d messages · last active %s", sess.Source, sess.Msgs, formatAge(sess.UpdatedAt)),
		fmt.Sprintf("%s · %s in · %s out · $%.4f", sess.Model, formatCompactTokens(sess.InTokens), formatCompactTokens(sess.OutTokens), sess.Cost),
	}
	lines := roundedBox(fmt.Sprintf("%s › %s", p.Name, sess.Title), sess.Source, sumBody, width, false)
	lines = append(lines, "")

	msgs := sess.Messages
	if len(msgs) > 40 {
		msgs = msgs[len(msgs)-40:]
	}
	inner := max(20, width-4)
	var msgBody []string
	if len(msgs) == 0 {
		msgBody = append(msgBody, "(no messages recorded)")
	} else {
		for _, m := range msgs {
			ts := "-- -- --:--"
			if !m.Timestamp.IsZero() {
				ts = m.Timestamp.Format("01-02 15:04")
			}
			lead := fmt.Sprintf("%s %-9s", ts, fitLine(m.Role, 9))
			textBudget := max(10, inner-tui.VisibleWidth(lead)-3)
			wrapped := tui.WrapTextWithAnsi(m.Text, textBudget)
			if len(wrapped) == 0 {
				wrapped = []string{""}
			}
			for j, wl := range wrapped {
				if j >= 3 {
					break
				}
				if j == 0 {
					msgBody = append(msgBody, fmt.Sprintf("%s │ %s", lead, wl))
				} else {
					msgBody = append(msgBody, fmt.Sprintf("%s │ %s", strings.Repeat(" ", tui.VisibleWidth(lead)), wl))
				}
			}
		}
	}
	lines = append(lines, roundedBox(fmt.Sprintf("Messages (last %d)", len(msgs)), sess.ID, msgBody, width, true)...)
	return lines
}

func (c *botsBoardComponent) Render(width int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if width < 28 {
		width = 28
	}

	var content []string
	hints := "↑↓ move  Enter open  ← back  r refresh  q close"
	switch c.level {
	case 2:
		content = c.renderLevel2Locked(width)
	case 3:
		content = c.renderLevel3Locked(width)
		hints = "↑↓ scroll  g/G ends  ← back  r refresh  q close"
	default:
		content = c.renderLevel1Locked(width)
	}

	total := len(content)
	c.lastLines = total
	bodyRows := c.viewHeight
	if bodyRows < 6 {
		bodyRows = 6
	}
	maxScroll := max(0, total-bodyRows)
	c.scrollTop = clampInt(c.scrollTop, 0, maxScroll)

	end := min(total, c.scrollTop+bodyRows)
	visible := make([]string, 0, bodyRows+3)
	visible = append(visible, content[c.scrollTop:end]...)
	firstRow := 0
	if total > 0 {
		firstRow = c.scrollTop + 1
	}
	visible = append(visible,
		strings.Repeat("─", width),
		fitLine(hints, width),
		fitLine(fmt.Sprintf("bots · L%d · rows %d-%d/%d · read-only", c.level, firstRow, end, total), width),
	)
	return visible
}

func (c *botsBoardComponent) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	if tui.IsKeyRelease(data) {
		return sdk.RemoteComponentResult{}, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	key, _ := tui.ParseKey(data)
	switch {
	case tui.MatchesKey(data, tui.Key.Ctrl("c")):
		return sdk.RemoteComponentResult{Done: true, Value: true}, nil
	case tui.MatchesKey(data, tui.Key.Left), tui.MatchesKey(data, tui.Key.Escape), key == "q", data == "q", data == "Q":
		if c.level > 1 {
			c.level--
			c.scrollTop = 0
			return sdk.RemoteComponentResult{}, nil
		}
		return sdk.RemoteComponentResult{Done: true, Value: true}, nil
	case tui.MatchesKey(data, tui.Key.Enter), tui.MatchesKey(data, tui.Key.Right):
		if c.level == 1 && len(c.profiles) > 0 {
			c.level = 2
			c.sessionIdx = 0
			c.scrollTop = 0
		} else if c.level == 2 && len(c.profiles) > 0 && len(c.profiles[c.profileIdx].Sessions) > 0 {
			c.level = 3
			c.scrollTop = 0
		}
	case tui.MatchesKey(data, tui.Key.Up), key == "k", data == "k":
		switch c.level {
		case 1:
			if c.profileIdx > 0 {
				c.profileIdx--
			}
		case 2:
			if c.sessionIdx > 0 {
				c.sessionIdx--
			}
		case 3:
			if c.scrollTop > 0 {
				c.scrollTop--
			}
		}
	case tui.MatchesKey(data, tui.Key.Down), key == "j", data == "j":
		switch c.level {
		case 1:
			if c.profileIdx+1 < len(c.profiles) {
				c.profileIdx++
			}
		case 2:
			if len(c.profiles) > 0 && c.sessionIdx+1 < len(c.profiles[c.profileIdx].Sessions) {
				c.sessionIdx++
			}
		case 3:
			c.scrollTop++
		}
	case tui.MatchesKey(data, tui.Key.Home), key == "g", data == "g":
		switch c.level {
		case 1:
			c.profileIdx = 0
		case 2:
			c.sessionIdx = 0
		case 3:
			c.scrollTop = 0
		}
	case tui.MatchesKey(data, tui.Key.End), data == "G":
		switch c.level {
		case 1:
			if len(c.profiles) > 0 {
				c.profileIdx = len(c.profiles) - 1
			}
		case 2:
			if len(c.profiles) > 0 && len(c.profiles[c.profileIdx].Sessions) > 0 {
				c.sessionIdx = len(c.profiles[c.profileIdx].Sessions) - 1
			}
		case 3:
			c.scrollTop = max(0, c.lastLines-c.viewHeight)
		}
	case key == "r", data == "r", data == "R":
		c.refreshLocked()
	}
	return sdk.RemoteComponentResult{}, nil
}

// ============================================================================
// 3. Single-Screen Arrow-Key /omh-model Table (modelTableComponent)
// ============================================================================

var modelEffortLadder = []string{"none", "low", "medium", "high", "xhigh"}

type modelTableRow struct {
	Category     string
	Purpose      string
	DefaultChain []ChainEntry
	OrigChain    []ChainEntry
	CurrentChain []ChainEntry
	WasOverride  bool
	Edited       bool
}

type modelTableComponent struct {
	mu      sync.Mutex
	surface *surfaceState
	rows    []modelTableRow
	ring    []string
	cursor  int
	saved   bool
	errMsg  string
}

func newModelTableComponent(s *surfaceState) *modelTableComponent {
	c := &modelTableComponent{
		surface: s,
	}
	c.loadLocked()
	return c
}

func cloneChain(in []ChainEntry) []ChainEntry {
	out := make([]ChainEntry, len(in))
	copy(out, in)
	return out
}

func chainsEqual(a, b []ChainEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Model != b[i].Model || a[i].ThinkingLevel != b[i].ThinkingLevel {
			return false
		}
	}
	return true
}

func (c *modelTableComponent) loadLocked() {
	defaultByCat := make(map[string]CategorySpec, len(defaultCategories))
	for _, spec := range defaultCategories {
		defaultByCat[spec.Category] = spec
	}

	chains, overridden, err := c.surface.router.AllChains()
	if err != nil {
		c.errMsg = err.Error()
		return
	}

	seenModels := make(map[string]bool)
	addModel := func(m string) {
		m = strings.TrimSpace(m)
		if m != "" && !seenModels[m] {
			seenModels[m] = true
			c.ring = append(c.ring, m)
		}
	}
	for _, p := range omhModelPresets {
		addModel(p.Model)
	}
	for _, spec := range chains {
		for _, hop := range spec.Chain {
			addModel(hop.Model)
		}
	}

	c.rows = make([]modelTableRow, 0, len(chains))
	for _, spec := range chains {
		defChain := spec.Chain
		purpose := spec.Purpose
		if d, ok := defaultByCat[spec.Category]; ok {
			defChain = d.Chain
			if purpose == "" {
				purpose = d.Purpose
			}
		}
		c.rows = append(c.rows, modelTableRow{
			Category:     spec.Category,
			Purpose:      purpose,
			DefaultChain: cloneChain(defChain),
			OrigChain:    cloneChain(spec.Chain),
			CurrentChain: cloneChain(spec.Chain),
			WasOverride:  overridden[spec.Category],
		})
	}
}

func effortBarString(level string) string {
	idx := -1
	norm := strings.ToLower(strings.TrimSpace(level))
	if norm == "" || norm == "off" {
		norm = "none"
	}
	for i, l := range modelEffortLadder {
		if l == norm {
			idx = i
			break
		}
	}
	if idx < 0 {
		idx = 2 // medium fallback
	}
	// 6-cell bar matching upstream aesthetic: none=0, low=2, medium=3, high=5, xhigh=6
	filledByIdx := []int{0, 2, 3, 5, 6}
	filled := filledByIdx[idx]
	return fmt.Sprintf("[%s%s] %-6s", strings.Repeat("■", filled), strings.Repeat("□", 6-filled), modelEffortLadder[idx])
}

func (c *modelTableComponent) unsavedCountLocked() int {
	n := 0
	for _, r := range c.rows {
		if r.Edited {
			n++
		}
	}
	return n
}

func (c *modelTableComponent) Render(width int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if width < 48 {
		width = 48
	}

	showPurpose := width >= 100
	var lines []string
	titleRight := ""
	if c.surface != nil && c.surface.paths != nil {
		titleRight = filepath.Base(c.surface.paths().ModelChainsFile)
	}
	titleLeft := "⚚ OMH · Model chains"
	gap := max(2, width-tui.VisibleWidth(titleLeft)-tui.VisibleWidth(titleRight))
	lines = append(lines, fitLine(titleLeft+strings.Repeat(" ", gap)+titleRight, width))
	lines = append(lines, strings.Repeat("─", width))

	if showPurpose {
		hdr := fmt.Sprintf("   %-18s  %-26s  %-22s  %-19s  %s", "CATEGORY", "PURPOSE", "HEAD MODEL", "EFFORT", "STATE")
		lines = append(lines, fitLine(hdr, width))
	} else {
		hdr := fmt.Sprintf("   %-16s  %-20s  %-17s  %s", "CATEGORY", "HEAD MODEL", "EFFORT", "STATE")
		lines = append(lines, fitLine(hdr, width))
	}

	for i, r := range c.rows {
		selected := i == c.cursor
		ptr := "   "
		if selected {
			ptr = " ❯ "
		}
		headModel := "(unset)"
		headEffort := "none"
		if len(r.CurrentChain) > 0 {
			headModel = r.CurrentChain[0].Model
			if r.CurrentChain[0].ThinkingLevel != "" {
				headEffort = r.CurrentChain[0].ThinkingLevel
			}
		}

		stateStr := "· built-in"
		if r.Edited {
			stateStr = "● edited"
		} else if r.WasOverride {
			stateStr = "◆ customized"
		}

		bar := effortBarString(headEffort)
		var modelCell, effortCell string
		if selected {
			modelCell = fmt.Sprintf("◂ %s ▸", fitLine(headModel, 18))
			effortCell = fmt.Sprintf("− %s +", bar)
		} else {
			modelCell = fmt.Sprintf("  %s", fitLine(headModel, 20))
			effortCell = fmt.Sprintf("  %s", bar)
		}

		var rowLine string
		if showPurpose {
			rowLine = fmt.Sprintf("%s%-18s  %-26s  %-22s  %-19s  %s",
				ptr,
				fitLine(r.Category, 18),
				fitLine(r.Purpose, 26),
				padRightCols(modelCell, 22),
				padRightCols(effortCell, 19),
				stateStr,
			)
		} else {
			rowLine = fmt.Sprintf("%s%-16s  %-20s  %-17s  %s",
				ptr,
				fitLine(r.Category, 16),
				padRightCols(modelCell, 20),
				padRightCols(effortCell, 17),
				stateStr,
			)
		}
		lines = append(lines, fitLine(rowLine, width))
	}

	lines = append(lines, strings.Repeat("─", width))

	if len(c.rows) > 0 && c.cursor >= 0 && c.cursor < len(c.rows) {
		sel := c.rows[c.cursor]
		lines = append(lines, fitLine(fmt.Sprintf("%s · %s", sel.Category, sel.Purpose), width))
		var hops []string
		for _, h := range sel.CurrentChain {
			if h.ThinkingLevel != "" {
				hops = append(hops, h.Model+":"+h.ThinkingLevel)
			} else {
				hops = append(hops, h.Model)
			}
		}
		lines = append(lines, fitLine("chain:  "+strings.Join(hops, " → "), width))
		lines = append(lines, fitLine("effort: "+strings.Join(modelEffortLadder, " · "), width))
	}

	unsaved := c.unsavedCountLocked()
	if c.errMsg != "" {
		lines = append(lines, fitLine("error: "+c.errMsg, width))
	} else if unsaved > 0 {
		lines = append(lines, fitLine(fmt.Sprintf("%d unsaved change(s) · ⏎ writes routing/model-chains.json", unsaved), width))
	} else {
		lines = append(lines, fitLine("no unsaved changes · ⏎ or esc leaves the file as it is", width))
	}

	lines = append(lines, strings.Repeat("─", width))
	lines = append(lines, fitLine("↑↓ category   ←→ head model   −/+ effort   d default   ⏎ save   esc cancel", width))
	return lines
}

func (c *modelTableComponent) cycleHeadModelLocked(delta int) {
	if len(c.rows) == 0 || len(c.ring) == 0 {
		return
	}
	r := &c.rows[c.cursor]
	curModel := ""
	curEffort := "medium"
	if len(r.CurrentChain) > 0 {
		curModel = r.CurrentChain[0].Model
		if r.CurrentChain[0].ThinkingLevel != "" {
			curEffort = r.CurrentChain[0].ThinkingLevel
		}
	}
	idx := 0
	for i, m := range c.ring {
		if m == curModel {
			idx = i
			break
		}
	}
	nextIdx := (idx + delta + len(c.ring)) % len(c.ring)
	nextModel := c.ring[nextIdx]

	newChain := []ChainEntry{{Model: nextModel, ThinkingLevel: curEffort}}
	for i := 1; i < len(r.CurrentChain); i++ {
		if r.CurrentChain[i].Model != nextModel {
			newChain = append(newChain, r.CurrentChain[i])
		}
	}
	r.CurrentChain = newChain
	r.Edited = !chainsEqual(r.CurrentChain, r.OrigChain)
}

func (c *modelTableComponent) stepEffortLocked(delta int) {
	if len(c.rows) == 0 {
		return
	}
	r := &c.rows[c.cursor]
	if len(r.CurrentChain) == 0 {
		return
	}
	cur := strings.ToLower(strings.TrimSpace(r.CurrentChain[0].ThinkingLevel))
	if cur == "" || cur == "off" {
		cur = "none"
	}
	idx := 2
	for i, l := range modelEffortLadder {
		if l == cur {
			idx = i
			break
		}
	}
	nextIdx := clampInt(idx+delta, 0, len(modelEffortLadder)-1)
	r.CurrentChain[0].ThinkingLevel = modelEffortLadder[nextIdx]
	r.Edited = !chainsEqual(r.CurrentChain, r.OrigChain)
}

func (c *modelTableComponent) resetSelectedLocked() {
	if len(c.rows) == 0 {
		return
	}
	r := &c.rows[c.cursor]
	r.CurrentChain = cloneChain(r.DefaultChain)
	r.Edited = !chainsEqual(r.CurrentChain, r.OrigChain) || r.WasOverride
}

func (c *modelTableComponent) persistAllLocked() error {
	for _, r := range c.rows {
		if !r.Edited {
			continue
		}
		if chainsEqual(r.CurrentChain, r.DefaultChain) {
			if err := c.surface.router.ClearOverride(r.Category); err != nil {
				return err
			}
		} else {
			if err := c.surface.router.SetOverride(r.Category, r.CurrentChain); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *modelTableComponent) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	if tui.IsKeyRelease(data) {
		return sdk.RemoteComponentResult{}, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	key, _ := tui.ParseKey(data)
	switch {
	case tui.MatchesKey(data, tui.Key.Escape), tui.MatchesKey(data, tui.Key.Ctrl("c")), key == "q", data == "q", data == "Q":
		return sdk.RemoteComponentResult{Done: true, Value: false}, nil
	case tui.MatchesKey(data, tui.Key.Enter):
		if c.unsavedCountLocked() > 0 {
			if err := c.persistAllLocked(); err != nil {
				c.errMsg = err.Error()
				return sdk.RemoteComponentResult{}, nil
			}
			c.saved = true
		}
		return sdk.RemoteComponentResult{Done: true, Value: c.saved}, nil
	case tui.MatchesKey(data, tui.Key.Up), key == "k", data == "k":
		if c.cursor > 0 {
			c.cursor--
		}
	case tui.MatchesKey(data, tui.Key.Down), key == "j", data == "j":
		if c.cursor+1 < len(c.rows) {
			c.cursor++
		}
	case tui.MatchesKey(data, tui.Key.Left), key == "h", data == "h":
		c.cycleHeadModelLocked(-1)
	case tui.MatchesKey(data, tui.Key.Right), key == "l", data == "l":
		c.cycleHeadModelLocked(1)
	case key == "-", key == "_", data == "-", data == "_":
		c.stepEffortLocked(-1)
	case key == "+", key == "=", data == "+", data == "=":
		c.stepEffortLocked(1)
	case key == "d", data == "d", data == "D":
		c.resetSelectedLocked()
	}
	return sdk.RemoteComponentResult{}, nil
}
