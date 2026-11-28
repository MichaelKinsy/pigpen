package ohmyhermes

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/memory"
	hermespaths "github.com/MichaelKinsy/pigpen/components/pig-hermes-core/paths"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/usage"
)

// --- Shared ANSI Truecolor Theme Styling & Visual Width Helpers ---

const (
	ansiReset             = "\x1b[0m"
	omhStatusRunningHex   = "#58A6FF"
	omhStatusCompletedHex = "#3FB950"
	omhStatusFailedHex    = "#F85149"
	omhGaugeWarnHex       = "#F59E0B"
	omhGaugeCritHex       = "#FF3D6E"
)

func parseHexRGB(hex string) (uint8, uint8, uint8, bool) {
	h := strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(h) != 6 {
		return 0, 0, 0, false
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return uint8((v >> 16) & 0xFF), uint8((v >> 8) & 0xFF), uint8(v & 0xFF), true
}

func ansiHex(hex, text string) string {
	if text == "" {
		return ""
	}
	r, g, b, ok := parseHexRGB(hex)
	if !ok {
		return text
	}
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm%s\x1b[0m", r, g, b, text)
}

func ansiBoldHex(hex, text string) string {
	if text == "" {
		return ""
	}
	r, g, b, ok := parseHexRGB(hex)
	if !ok {
		return text
	}
	return fmt.Sprintf("\x1b[1;38;2;%d;%d;%dm%s\x1b[0m", r, g, b, text)
}

func dagStatusColorHex(status string, pal omhThemeSpec) string {
	switch status {
	case "running", "active":
		return omhStatusRunningHex
	case "completed", "done":
		return omhStatusCompletedHex
	case "failed", "error":
		return omhStatusFailedHex
	case "pending", "waiting", "skipped", "interrupted":
		return pal.Muted
	default:
		return pal.Muted
	}
}

func gaugeColorHex(pct int, pal omhThemeSpec) string {
	switch {
	case pct > 90:
		return omhGaugeCritHex
	case pct >= 70:
		return omhGaugeWarnHex
	default:
		return omhStatusCompletedHex
	}
}

func activeThemeSpecForSurface(s *surfaceState) omhThemeSpec {
	if s == nil {
		return lookupThemeSpec("")
	}
	return lookupThemeSpec(s.currentTheme())
}

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
	return roundedBoxStyled(titleLeft, titleRight, body, width, accent, lookupThemeSpec(""))
}

func roundedBoxStyled(titleLeft, titleRight string, body []string, width int, accent bool, pal omhThemeSpec) []string {
	if width < 8 {
		width = 8
	}
	inner := width - 4 // "│ " + inner + " │"
	borderHex := pal.BorderMuted
	topLeft := "╭─"
	topRight := "─╮"
	fillCh := "─"
	side := "│"
	if accent {
		borderHex = pal.Accent
		topLeft = "╭━"
		topRight = "━╮"
		fillCh = "━"
		side = "┃"
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
	topLine := ansiHex(borderHex, topLeft) + leftPart + ansiHex(borderHex, strings.Repeat(fillCh, midFill)) + rightPart + ansiHex(borderHex, topRight)
	lines = append(lines, topLine)
	styledSide := ansiHex(borderHex, side)
	for _, b := range body {
		lines = append(lines, styledSide+" "+padRightCols(b, inner)+" "+styledSide)
	}
	lines = append(lines, ansiHex(borderHex, "╰"+strings.Repeat(fillCh, width-2)+"╯"))
	return lines
}

// roundedBoxTwoColStyled draws a rounded box with a true 2-column internal
// vertical divider (`┬`, `│`, `┴`) separating leftCol and rightCol.
func roundedBoxTwoColStyled(titleLeft, titleRight string, leftRows, rightRows []string, leftW, width int, accent bool, pal omhThemeSpec) []string {
	if width < 16 {
		width = 16
	}
	// Layout: side(1) + ' '(1) + leftW + ' '(1) + div(1) + ' '(1) + rightW + ' '(1) + side(1)
	// Total overhead = 7 columns -> leftW + rightW = width - 7
	if leftW < 6 {
		leftW = 6
	}
	rightW := width - 7 - leftW
	if rightW < 6 {
		rightW = 6
		leftW = max(6, width-7-rightW)
	}
	// Column index (0-based) of internal divider relative to box left edge:
	// 0:side, 1:' ', 2..2+leftW-1:left, 2+leftW:' ', 3+leftW:div
	divCol := 3 + leftW

	borderHex := pal.BorderMuted
	topLeft := "╭─"
	topRight := "─╮"
	fillCh := "─"
	side := "│"
	topTee := "┬"
	botTee := "┴"
	midDiv := "│"
	if accent {
		borderHex = pal.Accent
		topLeft = "╭━"
		topRight = "━╮"
		fillCh = "━"
		side = "┃"
		topTee = "┯"
		botTee = "┷"
	}

	leftPart := ""
	if titleLeft != "" {
		leftPart = " " + titleLeft + " "
	}
	rightPart := ""
	if titleRight != "" {
		rightPart = " " + titleRight + " "
	}
	avail := width - 4
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
	midFill := max(0, avail-lw-rw)
	// Build midFill segment, placing topTee at divCol if it falls within the fill region.
	fillStart := 2 + lw
	var midBuf strings.Builder
	for i := range midFill {
		if fillStart+i == divCol {
			midBuf.WriteString(topTee)
		} else {
			midBuf.WriteString(fillCh)
		}
	}

	rows := max(len(leftRows), len(rightRows))
	lines := make([]string, 0, rows+2)
	lines = append(lines, ansiHex(borderHex, topLeft)+leftPart+ansiHex(borderHex, midBuf.String())+rightPart+ansiHex(borderHex, topRight))

	styledSide := ansiHex(borderHex, side)
	styledDiv := ansiHex(pal.BorderMuted, midDiv)
	if accent {
		styledDiv = ansiHex(pal.Border, midDiv)
	}
	for i := range rows {
		lc := ""
		if i < len(leftRows) {
			lc = leftRows[i]
		}
		rc := ""
		if i < len(rightRows) {
			rc = rightRows[i]
		}
		lines = append(lines, styledSide+" "+padRightCols(lc, leftW)+" "+styledDiv+" "+padRightCols(rc, rightW)+" "+styledSide)
	}

	leftBottomFill := max(0, divCol-1)
	rightBottomFill := max(0, width-2-leftBottomFill-1)
	bottom := "╰" + strings.Repeat(fillCh, leftBottomFill) + botTee + strings.Repeat(fillCh, rightBottomFill) + "╯"
	lines = append(lines, ansiHex(borderHex, bottom))
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

func formatDurationCompact(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
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

func gaugeBarStyled(pct, cells int, pal omhThemeSpec) string {
	if cells <= 0 {
		cells = 10
	}
	pct = clampInt(pct, 0, 100)
	filled := (pct*cells + 50) / 100
	filled = clampInt(filled, 0, cells)
	col := gaugeColorHex(pct, pal)
	return ansiHex(col, strings.Repeat("▰", filled)) + ansiHex(pal.Dim, strings.Repeat("▱", cells-filled))
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

func sparkline24Styled(buckets [24]int, pal omhThemeSpec) string {
	glyphs := []rune("▁▂▃▄▅▆▇█")
	maxVal := 0
	for _, v := range buckets {
		if v > maxVal {
			maxVal = v
		}
	}
	var b strings.Builder
	for _, v := range buckets {
		if maxVal == 0 || v <= 0 {
			b.WriteString(ansiHex(pal.Dim, string(glyphs[0])))
			continue
		}
		idx := (v * (len(glyphs) - 1)) / maxVal
		idx = clampInt(idx, 0, len(glyphs)-1)
		col := pal.Accent
		if idx <= 2 {
			col = pal.Muted
		}
		b.WriteString(ansiHex(col, string(glyphs[idx])))
	}
	return b.String()
}

// renderIdenticonAvatar returns a deterministic 8×4 half-block (`▀`, `▄`, `█`, ` `)
// sha256 identicon avatar (4 rows, each 8 visible cells wide) for a profile name.
func renderIdenticonAvatar(seed string, pal omhThemeSpec) []string {
	sum := sha256.Sum256([]byte("omh-bot-avatar:" + seed))
	// Derive foreground hue from first 3 bytes, nudged bright enough for dark terminals.
	r := uint8(96 + int(sum[0])%150)
	g := uint8(96 + int(sum[1])%150)
	b := uint8(96 + int(sum[2])%150)
	fgHex := fmt.Sprintf("#%02X%02X%02X", r, g, b)
	bgHex := pal.Dim

	// 8x8 vertically & horizontally mirrored pixel matrix packed into 4 half-block rows.
	var grid [8][8]bool
	bitIdx := 24 // start after RGB bytes
	for row := range 8 {
		for col := range 4 {
			byteIdx := (bitIdx / 8) % len(sum)
			bitOff := uint(bitIdx % 8)
			on := ((sum[byteIdx] >> bitOff) & 1) == 1
			// Ensure a recognizable body in the center so avatars are never blank.
			if (row == 3 || row == 4) && col == 3 {
				on = true
			}
			grid[row][col] = on
			grid[row][7-col] = on
			bitIdx++
		}
	}

	rows := make([]string, 4)
	for pair := range 4 {
		topRow := pair * 2
		botRow := pair*2 + 1
		var sb strings.Builder
		for col := range 8 {
			top := grid[topRow][col]
			bot := grid[botRow][col]
			switch {
			case top && bot:
				sb.WriteString(ansiHex(fgHex, "█"))
			case top && !bot:
				sb.WriteString(ansiHex(fgHex, "▀"))
			case !top && bot:
				sb.WriteString(ansiHex(fgHex, "▄"))
			default:
				sb.WriteString(ansiHex(bgHex, " "))
			}
		}
		rows[pair] = sb.String()
	}
	return rows
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
	selectedIdx       int
	needsInitialFocus bool
	folded            map[string]bool
	showDetails       bool
	showEvidence bool
	scrollTop    int
	viewHeight   int
	lastLines    int

	// Live refresh ticker lifecycle (sdk.RemoteComponentInvalidator + Disposer)
	invalidate    func()
	stopCh        chan struct{}
	stopOnce      sync.Once
	tickerRunning bool
	disposed      bool
}

var (
	_ sdk.RemoteComponent            = (*dagPagerComponent)(nil)
	_ sdk.RemoteComponentInvalidator = (*dagPagerComponent)(nil)
	_ sdk.RemoteComponentDisposer    = (*dagPagerComponent)(nil)
)

func newDAGPagerComponent(s *surfaceState, runID string) *dagPagerComponent {
	c := &dagPagerComponent{
		surface:           s,
		runID:             strings.TrimSpace(runID),
		needsInitialFocus: true,
		folded:            make(map[string]bool),
		showDetails:       true,
		viewHeight:        28,
		stopCh:            make(chan struct{}),
	}
	c.reloadLocked()
	return c
}

// SetInvalidate wires the host redraw callback and starts a 1s live-refresh ticker.
func (c *dagPagerComponent) SetInvalidate(fn func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.invalidate = fn
	if fn == nil || c.disposed || c.tickerRunning {
		return
	}
	c.tickerRunning = true
	stopCh := c.stopCh
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				c.mu.Lock()
				if c.disposed {
					c.mu.Unlock()
					return
				}
				c.reloadLocked()
				inv := c.invalidate
				c.mu.Unlock()
				if inv != nil {
					inv()
				}
			}
		}
	}()
}

// Dispose stops the background refresh ticker cleanly (safe to call multiple times).
func (c *dagPagerComponent) Dispose() {
	c.mu.Lock()
	c.disposed = true
	c.invalidate = nil
	c.mu.Unlock()
	c.stopOnce.Do(func() {
		close(c.stopCh)
	})
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
	if c.surface != nil && c.surface.dags != nil {
		if runs, err := c.surface.dags.List(); err == nil && len(runs) > 0 {
			sortDAGRunsDeterministic(runs)
			c.runs = runs
		}
	}
	if c.runID != "" {
		for i, r := range c.runs {
			if r.RunID == c.runID {
				run = r
				c.runIdx = i
				break
			}
		}
		if run == nil && c.surface != nil && c.surface.dags != nil {
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
	// Default initial focus to first running node only on initial load or run switch.
	if c.needsInitialFocus {
		c.needsInitialFocus = false
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

func (c *dagPagerComponent) nodeMetricsSuffixLocked(n DAGNode) string {
	var parts []string
	if n.UpdatedAt != "" {
		if upd, err := time.Parse(time.RFC3339, n.UpdatedAt); err == nil {
			if n.Status == "running" {
				parts = append(parts, formatDurationCompact(time.Since(upd)))
			} else if c.run != nil && c.run.CreatedAt != "" {
				if cr, err2 := time.Parse(time.RFC3339, c.run.CreatedAt); err2 == nil && !upd.Before(cr) {
					if d := upd.Sub(cr); d > 0 {
						parts = append(parts, formatDurationCompact(d))
					}
				}
			}
		}
	}
	// Check matching delegation record for token & duration summary if available.
	if c.surface != nil && c.surface.teams != nil {
		for _, d := range c.surface.teams.Delegations() {
			if d.TaskID == n.ID || d.ChildSessionID == n.ID {
				if d.DurationMs > 0 {
					parts = []string{formatDurationCompact(time.Duration(d.DurationMs) * time.Millisecond)}
				}
				if totalTok := d.InputTokens + d.OutputTokens; totalTok > 0 {
					parts = append(parts, formatCompactTokens(totalTok)+" tok")
				}
				break
			}
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return " · " + strings.Join(parts, " · ")
}

func (c *dagPagerComponent) renderNodeBoxLocked(id string, innerWidth int, pal omhThemeSpec) []string {
	n := c.nodeByID[id]
	selected := id == c.selectedNodeIDLocked()
	glyph, statusLabel := dagStatusGlyph(n.Status)
	statusHex := dagStatusColorHex(n.Status, pal)
	title := strings.TrimSpace(n.Title)
	if title == "" {
		title = n.ID
	}
	ptr := ""
	if selected {
		ptr = "> "
	}
	metrics := c.nodeMetricsSuffixLocked(n)

	var body []string
	if c.folded[id] {
		plain := fmt.Sprintf("[+] %s%s %s · %s%s", ptr, glyph, title, statusLabel, metrics)
		trunc := middleTruncate(plain, innerWidth)
		if selected {
			body = []string{ansiBoldHex(pal.Accent, trunc)}
		} else {
			body = []string{ansiHex(statusHex, trunc)}
		}
	} else {
		line1Plain := middleTruncate(fmt.Sprintf("[-] %s%s", ptr, title), innerWidth)
		line1 := ansiHex(pal.Text, line1Plain)
		if selected {
			line1 = ansiBoldHex(pal.Accent, line1Plain)
		}
		line2 := ansiHex(pal.Muted, fitLine(c.modelLabelForNode(n), innerWidth))
		line3Plain := fitLine(fmt.Sprintf("%s %s%s", glyph, statusLabel, metrics), innerWidth)
		line3 := ansiHex(statusHex, line3Plain)
		line4Plain := "Start node"
		if len(n.DependsOn) > 0 {
			line4Plain = "← " + strings.Join(n.DependsOn, ", ")
		}
		line4 := ansiHex(pal.Dim, fitLine(line4Plain, innerWidth))
		body = []string{line1, line2, line3, line4}
	}

	boxW := innerWidth + 4
	return roundedBoxStyled("", "", body, boxW, selected, pal)
}

type frontierLayout struct {
	inner   int
	gap     int
	perRow  int
	wrapped bool
	centers map[string]int // center X column for each node ID when not wrapped
	leftPad int
}

// computeUniformInnerWidthLocked selects one uniform inner box width in [22, 48]
// across all frontiers in the DAG so boxes align cleanly vertically.
func (c *dagPagerComponent) computeUniformInnerWidthLocked(width int) int {
	const gap = 2
	const minInner = 22
	const maxInner = 48
	maxCols := 1
	for _, layer := range c.layers {
		if len(layer) > maxCols {
			maxCols = len(layer)
		}
	}
	fitCols := maxCols
	for fitCols > 1 {
		avail := width - (fitCols-1)*gap
		cand := avail/fitCols - 4
		if cand >= minInner {
			return clampInt(cand, minInner, maxInner)
		}
		fitCols--
	}
	return clampInt(width-4, minInner, maxInner)
}

func (c *dagPagerComponent) layoutFrontier(layer []string, inner, width int) frontierLayout {
	const gap = 2
	boxW := inner + 4
	perRow := max(1, (width+gap)/(boxW+gap))
	if perRow > len(layer) {
		perRow = len(layer)
	}
	wrapped := perRow < len(layer)
	rowWidth := perRow*boxW + (perRow-1)*gap
	leftPad := max(0, (width-rowWidth)/2)
	centers := make(map[string]int, len(layer))
	if !wrapped {
		for i, id := range layer {
			centers[id] = leftPad + i*(boxW+gap) + boxW/2
		}
	}
	return frontierLayout{
		inner:   inner,
		gap:     gap,
		perRow:  perRow,
		wrapped: wrapped,
		centers: centers,
		leftPad: leftPad,
	}
}

func (c *dagPagerComponent) renderFrontierRowLocked(layer []string, inner, width int, pal omhThemeSpec) ([]string, frontierLayout) {
	if len(layer) == 0 {
		return nil, frontierLayout{}
	}
	lay := c.layoutFrontier(layer, inner, width)
	boxW := inner + 4
	spacer := strings.Repeat(" ", lay.gap)

	var out []string
	for start := 0; start < len(layer); start += lay.perRow {
		end := min(start+lay.perRow, len(layer))
		chunk := layer[start:end]
		boxes := make([][]string, len(chunk))
		maxH := 0
		for i, id := range chunk {
			boxes[i] = c.renderNodeBoxLocked(id, inner, pal)
			if len(boxes[i]) > maxH {
				maxH = len(boxes[i])
			}
		}
		chunkW := len(chunk)*boxW + (len(chunk)-1)*lay.gap
		pad := strings.Repeat(" ", max(0, (width-chunkW)/2))
		for row := range maxH {
			var parts []string
			for i := range boxes {
				if row < len(boxes[i]) {
					parts = append(parts, boxes[i][row])
				} else {
					parts = append(parts, strings.Repeat(" ", boxW))
				}
			}
			out = append(out, fitLine(pad+strings.Join(parts, spacer), width))
		}
	}
	if len(layer) > 1 {
		out = append(out, fitLine(ansiHex(pal.Dim, "  · Same frontier"), width))
	}
	return out, lay
}

type dagEdgePair struct {
	fromX int
	toX   int
}

func edgesCross(edges []dagEdgePair) bool {
	for i := range edges {
		for j := i + 1; j < len(edges); j++ {
			a, b := edges[i], edges[j]
			if (a.fromX < b.fromX && a.toX > b.toX) || (a.fromX > b.fromX && a.toX < b.toX) {
				return true
			}
		}
	}
	return false
}

// renderConnectorRowsLocked computes real parent→child midpoint edge routing
// between adjacent frontiers using `│`, `─`, `╰`, `╯`, `╭`, `╮`, `┬`, `┴`, `┼`, `▼`,
// falling back cleanly to centered `╎`/`▽` when frontiers wrap or edges cross.
func (c *dagPagerComponent) renderConnectorRowsLocked(prevLayer, nextLayer []string, prevLay, nextLay frontierLayout, width int, pal omhThemeSpec) []string {
	if len(prevLayer) == 0 || len(nextLayer) == 0 || width <= 0 {
		return nil
	}
	edgeColor := pal.Border

	// Collect direct edges between prevLayer and nextLayer.
	prevSet := make(map[string]bool, len(prevLayer))
	for _, id := range prevLayer {
		prevSet[id] = true
	}
	var edges []dagEdgePair
	for _, cid := range nextLayer {
		cn := c.nodeByID[cid]
		cx, okC := nextLay.centers[cid]
		if !okC {
			continue
		}
		for _, dep := range cn.DependsOn {
			if !prevSet[dep] {
				continue
			}
			px, okP := prevLay.centers[dep]
			if !okP {
				continue
			}
			edges = append(edges, dagEdgePair{fromX: px, toX: cx})
		}
	}

	// Fallback when wrapped, no direct adjacent edges, or edges cross.
	if prevLay.wrapped || nextLay.wrapped || len(edges) == 0 || edgesCross(edges) {
		mid := clampInt(width/2, 1, max(1, width-1))
		pad := strings.Repeat(" ", mid)
		return []string{
			ansiHex(pal.Muted, pad+"╎"),
			ansiHex(pal.Muted, pad+"▽"),
		}
	}

	// Fast-path: all edges are straight vertical drops (fromX == toX).
	allStraight := true
	for _, e := range edges {
		if e.fromX != e.toX {
			allStraight = false
			break
		}
	}
	if allStraight {
		row1 := make([]rune, width)
		row2 := make([]rune, width)
		for i := range row1 {
			row1[i] = ' '
			row2[i] = ' '
		}
		for _, e := range edges {
			if e.toX >= 0 && e.toX < width {
				row1[e.toX] = '│'
				row2[e.toX] = '▼'
			}
		}
		return []string{
			ansiHex(edgeColor, strings.TrimRight(string(row1), " ")),
			ansiHex(edgeColor, strings.TrimRight(string(row2), " ")),
		}
	}

	// General 3-row bus router:
	//   Row 0: vertical stubs `│` down from each parent center
	//   Row 1: horizontal bus connecting parent and child centers with exact box-drawing junctions
	//   Row 2: arrowheads `▼` centered over each child
	row0 := make([]rune, width)
	row1 := make([]rune, width)
	row2 := make([]rune, width)
	for i := range width {
		row0[i] = ' '
		row1[i] = ' '
		row2[i] = ' '
	}

	const (
		dirU = 1 << iota
		dirD
		dirL
		dirR
	)
	mask := make([]int, width)
	for _, e := range edges {
		fx := clampInt(e.fromX, 0, width-1)
		tx := clampInt(e.toX, 0, width-1)
		row0[fx] = '│'
		row2[tx] = '▼'
		mask[fx] |= dirU
		mask[tx] |= dirD
		lo, hi := fx, tx
		if lo > hi {
			lo, hi = hi, lo
		}
		for x := lo; x <= hi; x++ {
			if x > lo {
				mask[x] |= dirL
			}
			if x < hi {
				mask[x] |= dirR
			}
		}
	}

	for x := range width {
		m := mask[x]
		if m == 0 {
			continue
		}
		switch m {
		case dirU | dirD:
			row1[x] = '│'
		case dirL | dirR:
			row1[x] = '─'
		case dirU | dirR:
			row1[x] = '╰'
		case dirU | dirL:
			row1[x] = '╯'
		case dirD | dirR:
			row1[x] = '╭'
		case dirD | dirL:
			row1[x] = '╮'
		case dirL | dirR | dirD:
			row1[x] = '┬'
		case dirL | dirR | dirU:
			row1[x] = '┴'
		case dirU | dirD | dirL | dirR:
			row1[x] = '┼'
		case dirU | dirD | dirR:
			row1[x] = '├'
		case dirU | dirD | dirL:
			row1[x] = '┤'
		default:
			row1[x] = '─'
		}
	}

	return []string{
		ansiHex(edgeColor, strings.TrimRight(string(row0), " ")),
		ansiHex(edgeColor, strings.TrimRight(string(row1), " ")),
		ansiHex(edgeColor, strings.TrimRight(string(row2), " ")),
	}
}

func (c *dagPagerComponent) buildGraphContentLocked(width int) []string {
	pal := activeThemeSpecForSurface(c.surface)
	if c.run == nil {
		return []string{
			fitLine(ansiBoldHex(pal.Accent, "OMH / DAG · 0 tasks"), width),
			"",
			fitLine(ansiHex(pal.Muted, "No OMH DAG runs recorded."), width),
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
	statusTitle := c.run.Status
	if len(statusTitle) > 0 {
		statusTitle = strings.ToUpper(statusTitle[:1]) + statusTitle[1:]
	}
	summary := fmt.Sprintf("%s · %s",
		ansiHex(dagStatusColorHex(c.run.Status, pal), statusTitle),
		ansiHex(pal.Success, fmt.Sprintf("Done %d/%d", doneCount, len(c.run.Nodes))),
	)
	if runCount > 0 {
		summary += " · " + ansiHex(omhStatusRunningHex, fmt.Sprintf("%d running", runCount))
	}
	if failCount > 0 {
		summary += " · " + ansiHex(omhStatusFailedHex, fmt.Sprintf("%d failed", failCount))
	}

	var lines []string
	lines = append(lines,
		fitLine(ansiBoldHex(pal.Accent, fmt.Sprintf("OMH / DAG · %d %s", len(c.run.Nodes), taskWord)), width),
		fitLine(ansiBoldHex(pal.Text, c.run.Title)+" "+ansiHex(pal.Muted, fmt.Sprintf("(%s)", c.run.RunID)), width),
		fitLine(summary, width),
		"",
	)

	uniformInner := c.computeUniformInnerWidthLocked(width)
	renderedLayers := make([][]string, len(c.layers))
	layouts := make([]frontierLayout, len(c.layers))
	for i, layer := range c.layers {
		renderedLayers[i], layouts[i] = c.renderFrontierRowLocked(layer, uniformInner, width, pal)
	}
	for i := range c.layers {
		lines = append(lines, renderedLayers[i]...)
		if i+1 < len(c.layers) {
			lines = append(lines, c.renderConnectorRowsLocked(c.layers[i], c.layers[i+1], layouts[i], layouts[i+1], width, pal)...)
		}
	}

	// Dependencies summary
	var deps []string
	for _, n := range c.run.Nodes {
		for _, dep := range n.DependsOn {
			deps = append(deps, fmt.Sprintf("  %s %s %s", ansiHex(pal.Text, dep), ansiHex(pal.Accent, "→"), ansiHex(pal.Text, n.ID)))
		}
	}
	if len(deps) > 0 {
		lines = append(lines, "", fitLine(ansiBoldHex(pal.Accent, "Dependencies"), width))
		for _, d := range deps {
			lines = append(lines, fitLine(d, width))
		}
	}

	// Toggleable Node details section
	if c.showDetails {
		lines = append(lines, "", fitLine(ansiBoldHex(pal.Accent, "Node details"), width))
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
			statusHex := dagStatusColorHex(n.Status, pal)
			card := []string{
				ansiHex(pal.Muted, "node:   ") + ansiBoldHex(pal.Text, n.ID) + ansiHex(pal.Muted, " — ") + ansiHex(pal.Text, n.Title),
				ansiHex(pal.Muted, "route:  ") + ansiHex(pal.Text, c.modelLabelForNode(n)),
				ansiHex(pal.Muted, "status: ") + ansiHex(statusHex, fmt.Sprintf("%s %s", glyph, statusLabel)),
			}
			if len(n.DependsOn) > 0 {
				card = append(card, ansiHex(pal.Muted, "deps:   ")+ansiHex(pal.Text, strings.Join(n.DependsOn, ", ")))
			}
			if len(n.WriteScope) > 0 {
				card = append(card, ansiHex(pal.Muted, "scope:  ")+ansiHex(pal.Text, strings.Join(n.WriteScope, ", ")))
			}
			if n.Result != "" {
				card = append(card, ansiHex(pal.Muted, "result: ")+ansiHex(pal.Success, n.Result))
			}
			if n.Error != "" {
				card = append(card, ansiHex(pal.Muted, "error:  ")+ansiHex(pal.Error, n.Error))
			}
			lines = append(lines, roundedBoxStyled(ansiBoldHex(pal.Accent, "detail · "+n.ID), "", card, width, id == selID, pal)...)
		}
	}
	return lines
}

func (c *dagPagerComponent) buildEvidenceContentLocked(width int) []string {
	pal := activeThemeSpecForSurface(c.surface)
	selID := c.selectedNodeIDLocked()
	if c.run == nil || selID == "" {
		return []string{fitLine(ansiHex(pal.Muted, "No node selected for evidence."), width)}
	}
	n := c.nodeByID[selID]
	glyph, statusLabel := dagStatusGlyph(n.Status)
	statusHex := dagStatusColorHex(n.Status, pal)
	var lines []string
	lines = append(lines,
		fitLine(ansiBoldHex(pal.Accent, fmt.Sprintf("OMH / DAG · Node evidence · %s", n.ID)), width),
		fitLine(ansiBoldHex(pal.Text, c.run.Title)+" "+ansiHex(pal.Muted, fmt.Sprintf("(%s)", c.run.RunID)), width),
		"",
	)
	card := []string{
		ansiHex(pal.Muted, "title:  ") + ansiHex(pal.Text, n.Title),
		ansiHex(pal.Muted, "status: ") + ansiHex(statusHex, fmt.Sprintf("%s %s", glyph, statusLabel)),
		ansiHex(pal.Muted, "route:  ") + ansiHex(pal.Text, c.modelLabelForNode(n)),
	}
	if n.Role != "" {
		card = append(card, ansiHex(pal.Muted, "role:   ")+ansiHex(pal.Text, n.Role))
	}
	if len(n.DependsOn) > 0 {
		card = append(card, ansiHex(pal.Muted, "deps:   ")+ansiHex(pal.Text, strings.Join(n.DependsOn, ", ")))
	} else {
		card = append(card, ansiHex(pal.Muted, "deps:   ")+ansiHex(pal.Dim, "Start node"))
	}
	if len(n.WriteScope) > 0 {
		card = append(card, ansiHex(pal.Muted, "scope:  ")+ansiHex(pal.Text, strings.Join(n.WriteScope, ", ")))
	}
	if n.UpdatedAt != "" {
		card = append(card, ansiHex(pal.Muted, "updated: ")+ansiHex(pal.Dim, n.UpdatedAt))
	}
	lines = append(lines, roundedBoxStyled(ansiBoldHex(pal.Accent, "handoff · "+n.ID), ansiHex(statusHex, statusLabel), card, width, true, pal)...)

	lines = append(lines, "")
	if n.Result != "" {
		lines = append(lines, fitLine(ansiBoldHex(pal.Success, "Observed result:"), width))
		for _, wl := range tui.WrapTextWithAnsi(n.Result, max(16, width-4)) {
			lines = append(lines, fitLine("  "+ansiHex(pal.Text, wl), width))
		}
	} else if n.Error != "" {
		lines = append(lines, fitLine(ansiBoldHex(pal.Error, "Recorded error:"), width))
		for _, wl := range tui.WrapTextWithAnsi(n.Error, max(16, width-4)) {
			lines = append(lines, fitLine("  "+ansiHex(pal.Error, wl), width))
		}
	} else {
		lines = append(lines, fitLine(ansiHex(pal.Muted, "No handoff output recorded for this node yet."), width))
	}
	return lines
}

func (c *dagPagerComponent) Render(width int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if width < 24 {
		width = 24
	}
	pal := activeThemeSpecForSurface(c.surface)

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
		ansiHex(pal.BorderMuted, strings.Repeat("─", width)),
		fitLine(ansiHex(pal.Muted, footerHints), width),
		fitLine(ansiHex(pal.Dim, fmt.Sprintf("%s · rows %d-%d/%d", title, firstRow, end, total)), width),
	)
	return visible
}

func (c *dagPagerComponent) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	if tui.IsKeyRelease(data) {
		return sdk.RemoteComponentResult{}, nil
	}
	c.mu.Lock()

	key, _ := tui.ParseKey(data)
	switch {
	case tui.MatchesKey(data, tui.Key.Escape), tui.MatchesKey(data, tui.Key.Ctrl("c")), key == "q", data == "q", data == "Q":
		c.mu.Unlock()
		c.Dispose()
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
			c.needsInitialFocus = true
			c.scrollTop = 0
			c.reloadLocked()
		}
	case key == "]", data == "]":
		if len(c.runs) > 1 {
			c.runIdx = (c.runIdx + 1) % len(c.runs)
			c.runID = c.runs[c.runIdx].RunID
			c.selectedIdx = 0
			c.needsInitialFocus = true
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
	c.mu.Unlock()
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
	JSONLPath string
	Messages  []botMessageRow
}

type botMessageRow struct {
	Timestamp time.Time
	Role      string
	Text      string
}

type botProfileCard struct {
	Name        string
	Home        string
	GatewayUp   bool
	ActiveNow   bool
	Model       string
	Newest      string
	MemoryPct   int
	UserPct     int
	OMHRecords  int
	LifetimeIn  int
	LifetimeOut int
	Hourly      [24]int
	Sessions    []botSessionRow
}

type botsBoardComponent struct {
	mu            sync.Mutex
	surface       *surfaceState
	level         int // 1 = profiles, 2 = profile sessions, 3 = session messages
	profiles      []botProfileCard
	profileIdx    int
	sessionIdx    int
	scrollTop     int
	viewHeight    int
	lastLines     int
	lastRefreshed time.Time

	// Live refresh ticker lifecycle (sdk.RemoteComponentInvalidator + Disposer)
	invalidate    func()
	stopCh        chan struct{}
	stopOnce      sync.Once
	tickerRunning bool
	disposed      bool
}

var (
	_ sdk.RemoteComponent            = (*botsBoardComponent)(nil)
	_ sdk.RemoteComponentInvalidator = (*botsBoardComponent)(nil)
	_ sdk.RemoteComponentDisposer    = (*botsBoardComponent)(nil)
)

func newBotsBoardComponent(s *surfaceState) *botsBoardComponent {
	c := &botsBoardComponent{
		surface:    s,
		level:      1,
		viewHeight: 28,
		stopCh:     make(chan struct{}),
	}
	c.refreshSnapshot()
	return c
}

// SetInvalidate wires the host redraw callback and starts a 5s live-refresh ticker.
func (c *botsBoardComponent) SetInvalidate(fn func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.invalidate = fn
	if fn == nil || c.disposed || c.tickerRunning {
		return
	}
	c.tickerRunning = true
	stopCh := c.stopCh
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				c.mu.Lock()
				disposed := c.disposed
				c.mu.Unlock()
				if disposed {
					return
				}
				c.refreshSnapshot()
				c.mu.Lock()
				if c.disposed {
					c.mu.Unlock()
					return
				}
				inv := c.invalidate
				c.mu.Unlock()
				if inv != nil {
					inv()
				}
			}
		}
	}()
}

// Dispose stops the background refresh ticker cleanly (safe to call multiple times).
func (c *botsBoardComponent) Dispose() {
	c.mu.Lock()
	c.disposed = true
	c.invalidate = nil
	c.mu.Unlock()
	c.stopOnce.Do(func() {
		close(c.stopCh)
	})
}

// --- Read-only Hermes state.db + PiG JSONL fallback loaders ---

type sqliteSessionRecord struct {
	ID           string  `json:"id"`
	Title        string  `json:"title"`
	Model        string  `json:"model"`
	Source       string  `json:"source"`
	StartedAt    float64 `json:"started_at"`
	EndedAt      float64 `json:"ended_at"`
	MessageCount int     `json:"message_count"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
}

type sqliteMessageRecord struct {
	Role      string  `json:"role"`
	Content   string  `json:"content"`
	Timestamp float64 `json:"timestamp"`
}

func runSQLiteReadonlyJSON(dbPath, sqlQuery string) ([]byte, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return nil, err
	}
	sqliteBin, err := exec.LookPath("sqlite3")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, sqliteBin, "-readonly", "-json", dbPath, sqlQuery)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return out, nil
}

func unixSecondsToTime(ts float64) time.Time {
	if ts <= 0 {
		return time.Time{}
	}
	if ts > 1e12 { // milliseconds
		return time.UnixMilli(int64(ts)).UTC()
	}
	sec := int64(ts)
	nsec := int64((ts - float64(sec)) * 1e9)
	return time.Unix(sec, nsec).UTC()
}

func sqlQuoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// loadProfileStateDB queries a Hermes profile's state.db read-only via sqlite3
// for sessions, 24h hourly activity buckets, lifetime token counts, and last-40 messages.
func loadProfileStateDB(profileHome string, now time.Time) (sessions []botSessionRow, hourly [24]int, lifeIn, lifeOut int, ok bool) {
	dbPath := filepath.Join(profileHome, "state.db")
	raw, err := runSQLiteReadonlyJSON(dbPath,
		`SELECT id, COALESCE(title,'') AS title, COALESCE(model,'') AS model, COALESCE(source,'cli') AS source,
		 COALESCE(started_at,0) AS started_at, COALESCE(ended_at,started_at,0) AS ended_at,
		 COALESCE(message_count,0) AS message_count, COALESCE(input_tokens,0) AS input_tokens,
		 COALESCE(output_tokens,0) AS output_tokens
		 FROM sessions ORDER BY COALESCE(ended_at, started_at, 0) DESC LIMIT 40;`)
	if err != nil {
		return nil, hourly, 0, 0, false
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "[]" {
		return nil, hourly, 0, 0, true
	}
	var recs []sqliteSessionRecord
	if err := json.Unmarshal([]byte(trimmed), &recs); err != nil {
		return nil, hourly, 0, 0, false
	}

	// Lifetime token totals across all sessions.
	if totRaw, err := runSQLiteReadonlyJSON(dbPath,
		`SELECT COALESCE(SUM(input_tokens),0) AS input_tokens, COALESCE(SUM(output_tokens),0) AS output_tokens FROM sessions;`); err == nil {
		var totals []struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(string(totRaw))), &totals) == nil && len(totals) > 0 {
			lifeIn = totals[0].InputTokens
			lifeOut = totals[0].OutputTokens
		}
	}

	sessions = make([]botSessionRow, 0, len(recs))
	for _, r := range recs {
		upd := unixSecondsToTime(r.EndedAt)
		if upd.IsZero() {
			upd = unixSecondsToTime(r.StartedAt)
		}
		if !upd.IsZero() {
			hrsAgo := int(now.Sub(upd).Hours())
			if hrsAgo >= 0 && hrsAgo < 24 {
				hourly[23-hrsAgo] += max(1, r.MessageCount)
			}
		}
		if lifeIn == 0 && lifeOut == 0 {
			lifeIn += r.InputTokens
			lifeOut += r.OutputTokens
		}
		title := strings.TrimSpace(r.Title)
		if title == "" {
			title = r.ID
		}
		sessions = append(sessions, botSessionRow{
			ID:        r.ID,
			Title:     title,
			Model:     nonEmpty(r.Model, "default"),
			Source:    nonEmpty(r.Source, "cli"),
			UpdatedAt: upd,
			Msgs:      r.MessageCount,
			InTokens:  r.InputTokens,
			OutTokens: r.OutputTokens,
			Cost:      r.CostUSD,
		})
	}
	return sessions, hourly, lifeIn, lifeOut, true
}

func loadStateDBSessionMessages(profileHome, sessionID string) ([]botMessageRow, bool) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, false
	}
	dbPath := filepath.Join(profileHome, "state.db")
	q := fmt.Sprintf(
		`SELECT COALESCE(role,'assistant') AS role, COALESCE(content,'') AS content, COALESCE(timestamp,0) AS timestamp
		 FROM messages WHERE session_id = %s ORDER BY id DESC LIMIT 40;`,
		sqlQuoteLiteral(sessionID),
	)
	raw, err := runSQLiteReadonlyJSON(dbPath, q)
	if err != nil {
		return nil, false
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "[]" {
		return nil, true
	}
	var recs []sqliteMessageRecord
	if err := json.Unmarshal([]byte(trimmed), &recs); err != nil {
		return nil, false
	}
	// Reverse DESC -> chronological order.
	out := make([]botMessageRow, 0, len(recs))
	for i := len(recs) - 1; i >= 0; i-- {
		out = append(out, botMessageRow{
			Timestamp: unixSecondsToTime(recs[i].Timestamp),
			Role:      nonEmpty(recs[i].Role, "assistant"),
			Text:      strings.TrimSpace(recs[i].Content),
		})
	}
	return out, true
}

const maxJSONLSessionsPerProfile = 40

type jsonlSessionFile struct {
	path    string
	id      string
	modTime time.Time
}

// countJSONLNonEmptyLines performs a lightweight line-count scan without JSON decoding.
func countJSONLNonEmptyLines(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	count := 0
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 512*1024)
	for scanner.Scan() {
		if len(bytes.TrimSpace(scanner.Bytes())) > 0 {
			count++
		}
	}
	return count
}

// loadJSONLSessionMessages parses the last 40 messages from a single JSONL session file on demand.
func loadJSONLSessionMessages(path string) ([]botMessageRow, bool) {
	if strings.TrimSpace(path) == "" {
		return nil, false
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, false
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	var msgs []botMessageRow
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 512*1024)
	for scanner.Scan() {
		var obj map[string]any
		if json.Unmarshal(scanner.Bytes(), &obj) != nil {
			continue
		}
		role, _ := obj["role"].(string)
		text, _ := obj["content"].(string)
		if text == "" {
			text, _ = obj["text"].(string)
		}
		if role == "" && text == "" {
			continue
		}
		ts := info.ModTime().UTC()
		if rawTS, ok := obj["timestamp"].(string); ok {
			if parsed, err := time.Parse(time.RFC3339, rawTS); err == nil {
				ts = parsed.UTC()
			}
		}
		msgs = append(msgs, botMessageRow{
			Timestamp: ts,
			Role:      nonEmpty(role, "message"),
			Text:      strings.TrimSpace(text),
		})
	}
	if len(msgs) > 40 {
		msgs = msgs[len(msgs)-40:]
	}
	return msgs, true
}

// loadProfileJSONLSessions reads PiG JSONL session metadata under <profileHome>/sessions/*.jsonl
// as a graceful fallback when state.db or sqlite3 is absent. It bounds work to the newest 40 files
// and defers full JSON message parsing until Level 3 opens a specific session.
func loadProfileJSONLSessions(profileHome string, now time.Time) ([]botSessionRow, [24]int) {
	var hourly [24]int
	sessDir := filepath.Join(profileHome, "sessions")
	entries, err := os.ReadDir(sessDir)
	if err != nil {
		return nil, hourly
	}
	files := make([]jsonlSessionFile, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, jsonlSessionFile{
			path:    filepath.Join(sessDir, e.Name()),
			id:      strings.TrimSuffix(e.Name(), ".jsonl"),
			modTime: info.ModTime().UTC(),
		})
	}
	sort.Slice(files, func(i, j int) bool {
		if !files[i].modTime.Equal(files[j].modTime) {
			return files[i].modTime.After(files[j].modTime)
		}
		return files[i].id < files[j].id
	})
	if len(files) > maxJSONLSessionsPerProfile {
		files = files[:maxJSONLSessionsPerProfile]
	}
	rows := make([]botSessionRow, 0, len(files))
	for _, sf := range files {
		lineCount := countJSONLNonEmptyLines(sf.path)
		if hrsAgo := int(now.Sub(sf.modTime).Hours()); hrsAgo >= 0 && hrsAgo < 24 {
			hourly[23-hrsAgo] += max(1, lineCount)
		}
		rows = append(rows, botSessionRow{
			ID:        sf.id,
			Title:     sf.id,
			Model:     "jsonl",
			Source:    "pig",
			UpdatedAt: sf.modTime,
			Msgs:      lineCount,
			JSONLPath: sf.path,
		})
	}
	return rows, hourly
}

// refreshSnapshot performs all disk and sqlite3 I/O outside c.mu and swaps the
// freshly built snapshot under lock so Render/HandleInput/Dispose never stall on I/O.
func (c *botsBoardComponent) refreshSnapshot() {
	refreshedAt := time.Now()
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
	activeLifeIn, activeLifeOut := 0, 0
	for _, rec := range usageRecs {
		activeLifeIn += rec.InputTokens
		activeLifeOut += rec.OutputTokens
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
			ActiveNow: isActive,
			Model:     "default",
			Newest:    "none",
			MemoryPct: sum.MemoryPct,
			UserPct:   sum.UserPct,
		}

		// Primary source: read-only Hermes state.db via sqlite3 -readonly -json.
		dbSessions, dbHourly, dbIn, dbOut, dbOK := loadProfileStateDB(sum.Home, now)
		if dbOK && len(dbSessions) > 0 {
			card.Sessions = append(card.Sessions, dbSessions...)
			for i := range 24 {
				card.Hourly[i] += dbHourly[i]
			}
			card.LifetimeIn += dbIn
			card.LifetimeOut += dbOut
		} else {
			// Fallback: PiG JSONL session files under <profileHome>/sessions/*.jsonl.
			jsonlSessions, jsonlHourly := loadProfileJSONLSessions(sum.Home, now)
			if len(jsonlSessions) > 0 {
				card.Sessions = append(card.Sessions, jsonlSessions...)
				for i := range 24 {
					card.Hourly[i] += jsonlHourly[i]
				}
			}
		}

		if isActive {
			card.OMHRecords = omhRecordTotal
			// Merge in-memory usage.Default(), teams, and delegations without duplicating IDs.
			seenID := make(map[string]bool, len(card.Sessions))
			for _, s := range card.Sessions {
				seenID[s.ID] = true
			}
			for _, s := range activeSessions {
				if !seenID[s.ID] {
					card.Sessions = append(card.Sessions, s)
					seenID[s.ID] = true
				}
			}
			for i := range 24 {
				card.Hourly[i] += activeHourly[i]
			}
			card.LifetimeIn += activeLifeIn
			card.LifetimeOut += activeLifeOut
			card.GatewayUp = len(card.Sessions) > 0 || sum.ActiveDAGs > 0 || sum.ActiveTeams > 0
		} else {
			card.MemoryPct = memoryFileFillPercent(filepath.Join(sum.Home, "memories", "MEMORY.md"), memory.DefaultMemoryCharLimit)
			card.UserPct = memoryFileFillPercent(filepath.Join(sum.Home, "memories", "USER.md"), memory.DefaultUserCharLimit)
			card.GatewayUp = len(card.Sessions) > 0
		}

		if len(card.Sessions) > 0 {
			card.Model = card.Sessions[0].Model
			card.Newest = card.Sessions[0].Title
		}
		cards = append(cards, card)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastRefreshed = refreshedAt
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
	pal := activeThemeSpecForSurface(c.surface)
	activeCount := 0
	totalLifeIn, totalLifeOut := 0, 0
	for _, p := range c.profiles {
		if p.GatewayUp || p.ActiveNow {
			activeCount++
		}
		totalLifeIn += p.LifetimeIn
		totalLifeOut += p.LifetimeOut
	}
	headerTitle := ansiBoldHex(pal.Accent, fmt.Sprintf("Hermes bots · %d profiles · %d active", len(c.profiles), activeCount))
	inTok, outTok, cost, turns, _ := usage.Default().Totals()
	if totalLifeIn > inTok {
		inTok = totalLifeIn
	}
	if totalLifeOut > outTok {
		outTok = totalLifeOut
	}
	headerBody := []string{
		fmt.Sprintf("%s %s in · %s out · %s · %d turns",
			ansiHex(pal.Muted, "usage:"),
			ansiHex(pal.Text, formatCompactTokens(inTok)),
			ansiHex(pal.Text, formatCompactTokens(outTok)),
			ansiHex(pal.Success, fmt.Sprintf("$%.4f", cost)),
			turns,
		),
	}
	lines := roundedBoxStyled(headerTitle, "", headerBody, width, false, pal)

	for i, p := range c.profiles {
		selected := i == c.profileIdx
		dot := ansiHex(pal.Muted, "○")
		if p.GatewayUp {
			dot = ansiHex(omhStatusCompletedHex, "●")
		}
		ptr := "  "
		if selected {
			ptr = ansiBoldHex(pal.Accent, "❯ ")
		}
		nameStyled := ansiHex(pal.Text, p.Name)
		if selected {
			nameStyled = ansiBoldHex(pal.Accent, p.Name)
		}
		titleLeft := fmt.Sprintf("%s%s %s", ptr, dot, nameStyled)
		if p.ActiveNow {
			titleLeft += " " + ansiHex(pal.Muted, "(this TUI)")
		}
		titleRight := ansiHex(pal.Muted, fmt.Sprintf("%d sessions", len(p.Sessions)))
		spark := sparkline24Styled(p.Hourly, pal)
		memGauge := fmt.Sprintf("%s %s", gaugeBarStyled(p.MemoryPct, 10, pal), ansiHex(gaugeColorHex(p.MemoryPct, pal), fmt.Sprintf("%3d%%", p.MemoryPct)))
		userGauge := fmt.Sprintf("%s %s", gaugeBarStyled(p.UserPct, 10, pal), ansiHex(gaugeColorHex(p.UserPct, pal), fmt.Sprintf("%3d%%", p.UserPct)))

		lines = append(lines, "")
		if width >= 72 {
			// Wide 2-column card with 8×4 sha256 identicon avatar on the left and internal `┬`/`│`/`┴` divider.
			avatar := renderIdenticonAvatar(p.Name, pal)
			leftW := clampInt((width-7)/2, 26, 46)
			metaW := max(10, leftW-10)
			leftRows := []string{
				avatar[0] + "  " + ansiHex(pal.Muted, "model  ") + ansiHex(pal.Text, fitLine(p.Model, max(4, metaW-7))),
				avatar[1] + "  " + ansiHex(pal.Muted, "newest ") + ansiHex(pal.Text, fitLine(p.Newest, max(4, metaW-7))),
				avatar[2] + "  " + ansiHex(pal.Muted, "home   ") + ansiHex(pal.Dim, fitLine(p.Home, max(4, metaW-7))),
				avatar[3] + "  " + ansiHex(pal.Muted, "OMH    ") + ansiHex(pal.Accent, fmt.Sprintf("%d records", p.OMHRecords)),
			}
			tokSummary := fmt.Sprintf("%s in · %s out", formatCompactTokens(p.LifetimeIn), formatCompactTokens(p.LifetimeOut))
			rightRows := []string{
				ansiHex(pal.Muted, "24 h   ") + spark,
				ansiHex(pal.Muted, "memory ") + memGauge,
				ansiHex(pal.Muted, "user   ") + userGauge,
				ansiHex(pal.Muted, "tokens ") + ansiHex(pal.Text, tokSummary),
			}
			lines = append(lines, roundedBoxTwoColStyled(titleLeft, titleRight, leftRows, rightRows, leftW, width, selected, pal)...)
		} else if width >= 60 {
			// Medium 2-column card: 8×4 identicon avatar column | stacked metrics column.
			avatar := renderIdenticonAvatar(p.Name, pal)
			rightRows := []string{
				ansiHex(pal.Muted, "model  ") + ansiHex(pal.Text, p.Model),
				ansiHex(pal.Muted, "24 h   ") + spark,
				ansiHex(pal.Muted, "mem ") + memGauge + "  " + ansiHex(pal.Muted, "usr ") + userGauge,
				ansiHex(pal.Muted, "OMH    ") + ansiHex(pal.Accent, fmt.Sprintf("%d records", p.OMHRecords)),
			}
			lines = append(lines, roundedBoxTwoColStyled(titleLeft, titleRight, avatar, rightRows, 8, width, selected, pal)...)
		} else {
			body := []string{
				ansiHex(pal.Muted, "model  ") + ansiHex(pal.Text, p.Model),
				ansiHex(pal.Muted, "newest ") + ansiHex(pal.Text, p.Newest),
				ansiHex(pal.Muted, "24 h   ") + spark,
				ansiHex(pal.Muted, "mem ") + memGauge + "  " + ansiHex(pal.Muted, "usr ") + userGauge,
				ansiHex(pal.Muted, "OMH    ") + ansiHex(pal.Accent, fmt.Sprintf("%d records", p.OMHRecords)),
			}
			lines = append(lines, roundedBoxStyled(titleLeft, titleRight, body, width, selected, pal)...)
		}
	}
	return lines
}

func (c *botsBoardComponent) renderLevel2Locked(width int) []string {
	pal := activeThemeSpecForSurface(c.surface)
	if len(c.profiles) == 0 {
		return []string{fitLine(ansiHex(pal.Muted, "No profiles found."), width)}
	}
	p := c.profiles[c.profileIdx]
	dot := ansiHex(pal.Muted, "○")
	if p.GatewayUp {
		dot = ansiHex(omhStatusCompletedHex, "●")
	}
	memGauge := fmt.Sprintf("%s %s", gaugeBarStyled(p.MemoryPct, 10, pal), ansiHex(gaugeColorHex(p.MemoryPct, pal), fmt.Sprintf("%3d%%", p.MemoryPct)))
	userGauge := fmt.Sprintf("%s %s", gaugeBarStyled(p.UserPct, 10, pal), ansiHex(gaugeColorHex(p.UserPct, pal), fmt.Sprintf("%3d%%", p.UserPct)))
	profBody := []string{
		ansiHex(pal.Muted, "home    ") + ansiHex(pal.Text, p.Home),
		ansiHex(pal.Muted, "model   ") + ansiHex(pal.Text, p.Model) + "   " + ansiHex(pal.Muted, "OMH records: ") + ansiHex(pal.Accent, fmt.Sprintf("%d", p.OMHRecords)),
		ansiHex(pal.Muted, "memory  ") + memGauge + "   " + ansiHex(pal.Muted, "user ") + userGauge,
		ansiHex(pal.Muted, "24 h    ") + sparkline24Styled(p.Hourly, pal),
	}
	lines := roundedBoxStyled(fmt.Sprintf("%s %s", dot, ansiBoldHex(pal.Accent, p.Name)), ansiHex(pal.Muted, "profile"), profBody, width, false, pal)
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
	tableBody = append(tableBody, ansiHex(pal.Muted, fitLine(hdr, inner)))
	tableBody = append(tableBody, ansiHex(pal.BorderMuted, strings.Repeat("─", inner)))
	if len(p.Sessions) == 0 {
		tableBody = append(tableBody, ansiHex(pal.Dim, "  (no sessions, teams, or delegations recorded)"))
	} else {
		for i, sess := range p.Sessions {
			selected := i == c.sessionIdx
			ptr := "  "
			titleText := padRightCols(sess.Title, titleCol)
			if selected {
				ptr = ansiBoldHex(pal.Accent, "❯ ")
				titleText = ansiBoldHex(pal.Accent, titleText)
			} else {
				titleText = ansiHex(pal.Text, titleText)
			}
			div := ansiHex(pal.BorderMuted, "│")
			var row string
			if width < 70 {
				row = fmt.Sprintf("%s%s %s %s  %s  %s",
					ptr,
					titleText,
					div,
					ansiHex(pal.Muted, fmt.Sprintf("%-10s", fitLine(sess.Model, 10))),
					ansiHex(pal.Dim, fmt.Sprintf("%4s", formatAge(sess.UpdatedAt))),
					ansiHex(pal.Text, fmt.Sprintf("%4d", sess.Msgs)),
				)
			} else {
				row = fmt.Sprintf("%s%s %s %s  %s  %s  %s",
					ptr,
					titleText,
					div,
					ansiHex(pal.Muted, fmt.Sprintf("%-12s", fitLine(sess.Model, 12))),
					ansiHex(pal.Dim, fmt.Sprintf("%-8s", fitLine(sess.Source, 8))),
					ansiHex(pal.Dim, fmt.Sprintf("%4s", formatAge(sess.UpdatedAt))),
					ansiHex(pal.Text, fmt.Sprintf("%4d", sess.Msgs)),
				)
			}
			tableBody = append(tableBody, fitLine(row, inner))
		}
	}
	lines = append(lines, roundedBoxStyled(ansiBoldHex(pal.Accent, fmt.Sprintf("Sessions & Delegations (%d)", len(p.Sessions))), ansiHex(pal.Muted, p.Name), tableBody, width, true, pal)...)
	return lines
}

func (c *botsBoardComponent) renderLevel3Locked(width int) []string {
	pal := activeThemeSpecForSurface(c.surface)
	if len(c.profiles) == 0 || len(c.profiles[c.profileIdx].Sessions) == 0 {
		return []string{fitLine(ansiHex(pal.Muted, "No session selected."), width)}
	}
	p := c.profiles[c.profileIdx]
	sess := p.Sessions[c.sessionIdx]

	// Lazily load real last-40 messages from Hermes state.db or JSONL file when Level 3 opens this session.
	msgs := sess.Messages
	if len(msgs) == 0 {
		if sess.JSONLPath != "" {
			if jMsgs, ok := loadJSONLSessionMessages(sess.JSONLPath); ok && len(jMsgs) > 0 {
				msgs = jMsgs
				c.profiles[c.profileIdx].Sessions[c.sessionIdx].Messages = jMsgs
			}
		} else if dbMsgs, ok := loadStateDBSessionMessages(p.Home, sess.ID); ok && len(dbMsgs) > 0 {
			msgs = dbMsgs
			c.profiles[c.profileIdx].Sessions[c.sessionIdx].Messages = dbMsgs
		}
	}

	sumBody := []string{
		ansiHex(pal.Text, fmt.Sprintf("%s · %d messages · last active %s", sess.Source, max(sess.Msgs, len(msgs)), formatAge(sess.UpdatedAt))),
		ansiHex(pal.Muted, fmt.Sprintf("%s · %s in · %s out · $%.4f", sess.Model, formatCompactTokens(sess.InTokens), formatCompactTokens(sess.OutTokens), sess.Cost)),
	}
	lines := roundedBoxStyled(ansiBoldHex(pal.Accent, fmt.Sprintf("%s › %s", p.Name, sess.Title)), ansiHex(pal.Muted, sess.Source), sumBody, width, false, pal)
	lines = append(lines, "")

	if len(msgs) > 40 {
		msgs = msgs[len(msgs)-40:]
	}
	inner := max(20, width-4)
	var msgBody []string
	if len(msgs) == 0 {
		msgBody = append(msgBody, ansiHex(pal.Dim, "(no messages recorded)"))
	} else {
		div := ansiHex(pal.BorderMuted, "│")
		for _, m := range msgs {
			ts := "-- -- --:--"
			if !m.Timestamp.IsZero() {
				ts = m.Timestamp.Format("01-02 15:04")
			}
			roleHex := pal.Accent
			switch strings.ToLower(m.Role) {
			case "user":
				roleHex = pal.Success
			case "system", "tool":
				roleHex = pal.Muted
			case "error", "failed":
				roleHex = pal.Error
			}
			leadPlain := fmt.Sprintf("%s %-9s", ts, fitLine(m.Role, 9))
			leadStyled := ansiHex(pal.Dim, ts) + " " + ansiBoldHex(roleHex, fmt.Sprintf("%-9s", fitLine(m.Role, 9)))
			leadW := tui.VisibleWidth(leadPlain)
			textBudget := max(10, inner-leadW-3)
			wrapped := tui.WrapTextWithAnsi(m.Text, textBudget)
			if len(wrapped) == 0 {
				wrapped = []string{""}
			}
			for j, wl := range wrapped {
				if j >= 3 {
					break
				}
				if j == 0 {
					msgBody = append(msgBody, fmt.Sprintf("%s %s %s", leadStyled, div, ansiHex(pal.Text, wl)))
				} else {
					msgBody = append(msgBody, fmt.Sprintf("%s %s %s", strings.Repeat(" ", leadW), div, ansiHex(pal.Text, wl)))
				}
			}
		}
	}
	lines = append(lines, roundedBoxStyled(ansiBoldHex(pal.Accent, fmt.Sprintf("Messages (last %d)", len(msgs))), ansiHex(pal.Muted, sess.ID), msgBody, width, true, pal)...)
	return lines
}

func (c *botsBoardComponent) Render(width int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if width < 28 {
		width = 28
	}
	pal := activeThemeSpecForSurface(c.surface)

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
	readAge := "0s"
	if !c.lastRefreshed.IsZero() {
		readAge = formatAge(c.lastRefreshed)
	}
	footerStatus := fmt.Sprintf("bots · L%d · rows %d-%d/%d · read %s · every 5 s · read-only", c.level, firstRow, end, total, readAge)
	visible = append(visible,
		ansiHex(pal.BorderMuted, strings.Repeat("─", width)),
		fitLine(ansiHex(pal.Muted, hints), width),
		fitLine(ansiHex(pal.Dim, footerStatus), width),
	)
	return visible
}

func (c *botsBoardComponent) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	if tui.IsKeyRelease(data) {
		return sdk.RemoteComponentResult{}, nil
	}
	c.mu.Lock()

	key, _ := tui.ParseKey(data)
	switch {
	case tui.MatchesKey(data, tui.Key.Ctrl("c")):
		c.mu.Unlock()
		c.Dispose()
		return sdk.RemoteComponentResult{Done: true, Value: true}, nil
	case tui.MatchesKey(data, tui.Key.Left), tui.MatchesKey(data, tui.Key.Escape), key == "q", data == "q", data == "Q":
		if c.level > 1 {
			c.level--
			c.scrollTop = 0
			c.mu.Unlock()
			return sdk.RemoteComponentResult{}, nil
		}
		c.mu.Unlock()
		c.Dispose()
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
		c.mu.Unlock()
		c.refreshSnapshot()
		return sdk.RemoteComponentResult{}, nil
	}
	c.mu.Unlock()
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

	// Custom identity row support
	IsIdentity   bool
	IdentityName string
	OrigEnabled  bool
	Enabled      bool
}

type modelTableComponent struct {
	mu            sync.Mutex
	surface       *surfaceState
	rows          []modelTableRow
	ring          []string
	providers     map[string]bool
	providerList  []string
	effortLadders map[string][]string // model -> custom effort ladder (empty slice = fixed)
	cursor        int
	saved         bool
	errMsg        string
}

func newModelTableComponent(s *surfaceState) *modelTableComponent {
	c := &modelTableComponent{
		surface:       s,
		providers:     make(map[string]bool),
		effortLadders: make(map[string][]string),
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

func detectActiveProviders() (map[string]bool, []string) {
	checks := []struct {
		name string
		envs []string
	}{
		{"anthropic", []string{"ANTHROPIC_API_KEY", "CLAUDE_API_KEY"}},
		{"openai", []string{"OPENAI_API_KEY"}},
		{"google", []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"}},
		{"openrouter", []string{"OPENROUTER_API_KEY"}},
		{"copilot", []string{"GITHUB_TOKEN", "GH_TOKEN", "COPILOT_API_KEY"}},
	}
	found := make(map[string]bool)
	var list []string
	for _, ch := range checks {
		for _, env := range ch.envs {
			if strings.TrimSpace(os.Getenv(env)) != "" {
				found[ch.name] = true
				list = append(list, ch.name)
				break
			}
		}
	}
	return found, list
}

func modelProviderServed(model string, providers map[string]bool) bool {
	if len(providers) == 0 {
		// No provider env vars configured: don't flag every model in offline/test setups unless providers are detected.
		return true
	}
	if providers["openrouter"] || providers["copilot"] {
		return true
	}
	m := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(m, "claude"):
		return providers["anthropic"]
	case strings.HasPrefix(m, "gpt"), strings.HasPrefix(m, "o1"), strings.HasPrefix(m, "o3"), strings.HasPrefix(m, "o4"):
		return providers["openai"]
	case strings.HasPrefix(m, "gemini"):
		return providers["google"]
	default:
		return true
	}
}

func loadModelEffortLadders(omhRoutingDir string) map[string][]string {
	out := make(map[string][]string)
	if strings.TrimSpace(omhRoutingDir) == "" {
		return out
	}
	raw, err := os.ReadFile(filepath.Join(omhRoutingDir, "model-efforts.json"))
	if err != nil || len(raw) == 0 {
		return out
	}
	// Support both {"models": {"m": [...]}} and flat {"m": [...]} JSON shapes.
	var wrapped struct {
		Models map[string][]string `json:"models"`
	}
	if json.Unmarshal(raw, &wrapped) == nil && wrapped.Models != nil {
		for k, v := range wrapped.Models {
			out[k] = v
		}
		return out
	}
	var flat map[string][]string
	if json.Unmarshal(raw, &flat) == nil {
		for k, v := range flat {
			out[k] = v
		}
	}
	return out
}

func (c *modelTableComponent) ladderForModelLocked(model string) ([]string, bool) {
	if ladder, ok := c.effortLadders[model]; ok {
		return ladder, true
	}
	return modelEffortLadder, false
}

func (c *modelTableComponent) loadLocked() {
	c.providers, c.providerList = detectActiveProviders()
	if c.surface != nil && c.surface.paths != nil {
		c.effortLadders = loadModelEffortLadders(c.surface.paths().RoutingDir)
	}

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

	c.rows = make([]modelTableRow, 0, len(chains)+4)
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

	// Load custom identities below built-in categories.
	if c.surface != nil && c.surface.router != nil && c.surface.router.identities != nil {
		if profiles, err := c.surface.router.identities.List(); err == nil {
			for _, prof := range profiles {
				var chain []ChainEntry
				for _, hop := range prof.ModelChain {
					chain = append(chain, ChainEntry{Model: hop.ModelID, ThinkingLevel: hop.ReasoningEffort})
					addModel(hop.ModelID)
				}
				if len(chain) == 0 && prof.Category != "" {
					if catChain, _, err := c.surface.router.ChainForCategory(prof.Category); err == nil {
						chain = catChain
					}
				}
				en := prof.IsEnabled()
				purpose := strings.TrimSpace(prof.Role)
				if purpose == "" {
					purpose = "custom identity"
				}
				c.rows = append(c.rows, modelTableRow{
					Category:     "custom:" + prof.Name,
					Purpose:      purpose,
					DefaultChain: cloneChain(chain),
					OrigChain:    cloneChain(chain),
					CurrentChain: cloneChain(chain),
					IsIdentity:   true,
					IdentityName: prof.Name,
					OrigEnabled:  en,
					Enabled:      en,
				})
			}
		}
	}
}

func effortBarStyled(level string, ladder []string, isCustomLadder bool, pal omhThemeSpec) string {
	if isCustomLadder && len(ladder) == 0 {
		return ansiHex(pal.Dim, "[□□□□□□] fixed ")
	}
	if len(ladder) == 0 {
		ladder = modelEffortLadder
	}
	idx := -1
	norm := strings.ToLower(strings.TrimSpace(level))
	if norm == "" || norm == "off" {
		norm = "none"
	}
	for i, l := range ladder {
		if strings.ToLower(strings.TrimSpace(l)) == norm {
			idx = i
			break
		}
	}
	if idx < 0 {
		idx = min(len(ladder)-1, len(ladder)/2)
	}
	filled := 0
	if len(ladder) == 5 && ladder[0] == "none" && ladder[4] == "xhigh" {
		filledByIdx := []int{0, 2, 3, 5, 6}
		filled = filledByIdx[idx]
	} else if len(ladder) == 1 {
		filled = 6
	} else {
		filled = (idx * 6) / (len(ladder) - 1)
	}
	filled = clampInt(filled, 0, 6)
	bar := ansiHex(pal.BorderMuted, "[") +
		ansiHex(pal.Accent, strings.Repeat("■", filled)) +
		ansiHex(pal.Dim, strings.Repeat("□", 6-filled)) +
		ansiHex(pal.BorderMuted, "]")
	return fmt.Sprintf("%s %s", bar, ansiHex(pal.Text, fmt.Sprintf("%-6s", ladder[idx])))
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
	pal := activeThemeSpecForSurface(c.surface)

	showPurpose := width >= 100
	var lines []string
	titleRight := ""
	if c.surface != nil && c.surface.paths != nil {
		titleRight = filepath.Base(c.surface.paths().ModelChainsFile)
	}
	titleLeftPlain := "⚚ OMH · Model chains"
	gap := max(2, width-tui.VisibleWidth(titleLeftPlain)-tui.VisibleWidth(titleRight))
	lines = append(lines, fitLine(ansiBoldHex(pal.Accent, titleLeftPlain)+strings.Repeat(" ", gap)+ansiHex(pal.Muted, titleRight), width))

	// Providers header row
	provLabel := "none detected (offline)"
	if len(c.providerList) > 0 {
		provLabel = strings.Join(c.providerList, " · ")
	}
	lines = append(lines, fitLine(ansiHex(pal.Muted, "providers: ")+ansiHex(pal.Text, provLabel), width))
	lines = append(lines, ansiHex(pal.BorderMuted, strings.Repeat("─", width)))

	if showPurpose {
		hdr := fmt.Sprintf("   %-18s  %-26s  %-22s  %-19s  %s", "CATEGORY", "PURPOSE", "HEAD MODEL", "EFFORT", "STATE")
		lines = append(lines, fitLine(ansiHex(pal.Muted, hdr), width))
	} else {
		hdr := fmt.Sprintf("   %-16s  %-20s  %-17s  %s", "CATEGORY", "HEAD MODEL", "EFFORT", "STATE")
		lines = append(lines, fitLine(ansiHex(pal.Muted, hdr), width))
	}

	printedCustomDivider := false
	for i, r := range c.rows {
		if r.IsIdentity && !printedCustomDivider {
			printedCustomDivider = true
			divText := "── Custom identities " + strings.Repeat("─", max(4, width-21))
			lines = append(lines, fitLine(ansiHex(pal.BorderMuted, divText), width))
		}
		selected := i == c.cursor
		ptr := "   "
		if selected {
			ptr = ansiBoldHex(pal.Accent, " ❯ ")
		}
		headModel := "(unset)"
		headEffort := "none"
		if len(r.CurrentChain) > 0 {
			headModel = r.CurrentChain[0].Model
			if r.CurrentChain[0].ThinkingLevel != "" {
				headEffort = r.CurrentChain[0].ThinkingLevel
			}
		}

		unservedMark := ""
		if headModel != "(unset)" && !modelProviderServed(headModel, c.providers) {
			unservedMark = ansiBoldHex(omhStatusFailedHex, "!") + " "
		}

		var stateStr string
		if r.IsIdentity {
			if r.Enabled {
				stateStr = ansiHex(omhStatusCompletedHex, "● enabled")
			} else {
				stateStr = ansiHex(pal.Dim, "× disabled")
			}
			if r.Edited {
				stateStr += " " + ansiHex(pal.Warning, "*")
			}
		} else if r.Edited {
			stateStr = ansiBoldHex(pal.Warning, "● edited")
		} else if r.WasOverride {
			stateStr = ansiHex(pal.Accent, "◆ customized")
		} else {
			stateStr = ansiHex(pal.Dim, "· built-in")
		}

		ladder, isCustomLadder := c.ladderForModelLocked(headModel)
		bar := effortBarStyled(headEffort, ladder, isCustomLadder, pal)
		var modelCell, effortCell string
		if selected {
			modelCell = ansiHex(pal.Accent, "◂ ") + unservedMark + ansiBoldHex(pal.Text, fitLine(headModel, 16)) + ansiHex(pal.Accent, " ▸")
			effortCell = ansiHex(pal.Accent, "− ") + bar + ansiHex(pal.Accent, " +")
		} else {
			modelCell = "  " + unservedMark + ansiHex(pal.Text, fitLine(headModel, 18))
			effortCell = "  " + bar
		}

		catStyled := padRightCols(r.Category, 18)
		if !showPurpose {
			catStyled = padRightCols(r.Category, 16)
		}
		if selected {
			catStyled = ansiBoldHex(pal.Accent, catStyled)
		} else {
			catStyled = ansiHex(pal.Text, catStyled)
		}

		var rowLine string
		if showPurpose {
			rowLine = fmt.Sprintf("%s%s  %s  %s  %s  %s",
				ptr,
				catStyled,
				ansiHex(pal.Muted, padRightCols(r.Purpose, 26)),
				padRightCols(modelCell, 22),
				padRightCols(effortCell, 19),
				stateStr,
			)
		} else {
			rowLine = fmt.Sprintf("%s%s  %s  %s  %s",
				ptr,
				catStyled,
				padRightCols(modelCell, 20),
				padRightCols(effortCell, 17),
				stateStr,
			)
		}
		lines = append(lines, fitLine(rowLine, width))
	}

	lines = append(lines, ansiHex(pal.BorderMuted, strings.Repeat("─", width)))

	if len(c.rows) > 0 && c.cursor >= 0 && c.cursor < len(c.rows) {
		sel := c.rows[c.cursor]
		lines = append(lines, fitLine(ansiBoldHex(pal.Accent, sel.Category)+ansiHex(pal.Muted, " · "+sel.Purpose), width))
		var hops []string
		for _, h := range sel.CurrentChain {
			if h.ThinkingLevel != "" {
				hops = append(hops, h.Model+":"+h.ThinkingLevel)
			} else {
				hops = append(hops, h.Model)
			}
		}
		lines = append(lines, fitLine(ansiHex(pal.Muted, "chain:  ")+ansiHex(pal.Text, strings.Join(hops, " → ")), width))
		headModel := ""
		if len(sel.CurrentChain) > 0 {
			headModel = sel.CurrentChain[0].Model
		}
		ladder, isCustom := c.ladderForModelLocked(headModel)
		ladderText := strings.Join(ladder, " · ")
		if isCustom && len(ladder) == 0 {
			ladderText = "fixed"
		}
		lines = append(lines, fitLine(ansiHex(pal.Muted, "effort: ")+ansiHex(pal.Text, ladderText), width))
	}

	unsaved := c.unsavedCountLocked()
	if c.errMsg != "" {
		lines = append(lines, fitLine(ansiBoldHex(pal.Error, "error: "+c.errMsg), width))
	} else if unsaved > 0 {
		lines = append(lines, fitLine(ansiHex(pal.Warning, fmt.Sprintf("%d unsaved change(s) · ⏎ writes routing/model-chains.json", unsaved)), width))
	} else {
		lines = append(lines, fitLine(ansiHex(pal.Dim, "no unsaved changes · ⏎ or esc leaves the file as it is"), width))
	}

	lines = append(lines, ansiHex(pal.BorderMuted, strings.Repeat("─", width)))
	lines = append(lines, fitLine(ansiHex(pal.Muted, "↑↓ category   ←→ head model   −/+ effort   Space toggle   d default   ⏎ save   esc cancel"), width))
	return lines
}

func (c *modelTableComponent) cycleHeadModelLocked(delta int) {
	if len(c.rows) == 0 || len(c.ring) == 0 {
		return
	}
	r := &c.rows[c.cursor]
	if r.IsIdentity {
		return
	}
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

	// Clamp effort to the new model's ladder if custom.
	ladder, isCustom := c.ladderForModelLocked(nextModel)
	if isCustom && len(ladder) == 0 {
		curEffort = ""
	} else if len(ladder) > 0 {
		found := false
		for _, l := range ladder {
			if l == curEffort {
				found = true
				break
			}
		}
		if !found {
			curEffort = ladder[min(len(ladder)-1, len(ladder)/2)]
		}
	}

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
	if r.IsIdentity || len(r.CurrentChain) == 0 {
		return
	}
	ladder, isCustom := c.ladderForModelLocked(r.CurrentChain[0].Model)
	if isCustom && len(ladder) == 0 {
		return // fixed effort model
	}
	if len(ladder) == 0 {
		ladder = modelEffortLadder
	}
	cur := strings.ToLower(strings.TrimSpace(r.CurrentChain[0].ThinkingLevel))
	if cur == "" || cur == "off" {
		cur = "none"
	}
	idx := min(len(ladder)-1, len(ladder)/2)
	for i, l := range ladder {
		if strings.ToLower(strings.TrimSpace(l)) == cur {
			idx = i
			break
		}
	}
	nextIdx := clampInt(idx+delta, 0, len(ladder)-1)
	r.CurrentChain[0].ThinkingLevel = ladder[nextIdx]
	r.Edited = !chainsEqual(r.CurrentChain, r.OrigChain)
}

func (c *modelTableComponent) toggleSelectedIdentityLocked() {
	if len(c.rows) == 0 || c.cursor < 0 || c.cursor >= len(c.rows) {
		return
	}
	r := &c.rows[c.cursor]
	if !r.IsIdentity {
		return
	}
	r.Enabled = !r.Enabled
	r.Edited = r.Enabled != r.OrigEnabled
}

func (c *modelTableComponent) resetSelectedLocked() {
	if len(c.rows) == 0 {
		return
	}
	r := &c.rows[c.cursor]
	if r.IsIdentity {
		r.Enabled = true
		r.Edited = r.Enabled != r.OrigEnabled
		return
	}
	r.CurrentChain = cloneChain(r.DefaultChain)
	r.Edited = !chainsEqual(r.CurrentChain, r.OrigChain) || r.WasOverride
}

func (c *modelTableComponent) persistAllLocked() error {
	for _, r := range c.rows {
		if !r.Edited {
			continue
		}
		if r.IsIdentity {
			if c.surface != nil && c.surface.router != nil && c.surface.router.identities != nil {
				if err := c.surface.router.identities.SetEnabled(r.IdentityName, r.Enabled); err != nil {
					return err
				}
			}
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
	case tui.MatchesKey(data, tui.Key.Space), data == " ":
		c.toggleSelectedIdentityLocked()
	case key == "-", key == "_", data == "-", data == "_":
		c.stepEffortLocked(-1)
	case key == "+", key == "=", data == "+", data == "=":
		c.stepEffortLocked(1)
	case key == "d", data == "d", data == "D":
		c.resetSelectedLocked()
	}
	return sdk.RemoteComponentResult{}, nil
}
