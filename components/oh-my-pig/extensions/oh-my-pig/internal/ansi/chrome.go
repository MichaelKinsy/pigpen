package ansi

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/brand"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/dirs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/surface"
)

// ═══════════════════════════════════════════════════════════════════════════
// Per-session chrome state table (released via lifecycle.OnEnd("ansi", ...))
// ═══════════════════════════════════════════════════════════════════════════

// Setting resolves omp settings for `internal/ansi` (e.g.
// `statusLine.compactThinkingLevel`, `statusLine.contextLine`,
// `read.toolResultPreview`, `composer.shape`). Assigned by the integrator
// (`ansi.Setting = settings.Any`); nil falls back to omp v19.1.1 defaults.
var Setting func(ctx sdk.Context, key string) any

// IsSubagent reports whether ctx belongs to a spawned child subagent session
// (wired by wiring.go to internal/agents.IsSubagent). Defaults to false when
// nil so top-level sessions publish surface turn usage and composer metadata.
var IsSubagent func(ctx sdk.Context) bool

func isTopLevelSession(ctx sdk.Context) bool {
	return IsSubagent == nil || !IsSubagent(ctx)
}

// PigletVersion is the oh-my-pig Piglet release version shown on the welcome
// banner's version row (matching piglets/oh-my-pig/piglet.yaml `release.version`).
var PigletVersion = "0.1.0"

func settingBool(ctx sdk.Context, key string, fallback bool) bool {
	if Setting == nil {
		return fallback
	}
	if b, ok := Setting(ctx, key).(bool); ok {
		return b
	}
	return fallback
}

func settingString(ctx sdk.Context, key string, fallback string) string {
	if Setting == nil {
		return fallback
	}
	if s, ok := Setting(ctx, key).(string); ok && strings.TrimSpace(s) != "" {
		return strings.TrimSpace(s)
	}
	return fallback
}

func settingFloat(ctx sdk.Context, key string, fallback float64) (float64, bool) {
	if Setting == nil {
		return fallback, false
	}
	v := Setting(ctx, key)
	if v == nil {
		return fallback, false
	}
	switch n := v.(type) {
	case float64:
		if !math.IsNaN(n) && !math.IsInf(n, 0) {
			return n, true
		}
	case float32:
		f := float64(n)
		if !math.IsNaN(f) && !math.IsInf(f, 0) {
			return f, true
		}
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint64:
		return float64(n), true
	}
	return fallback, false
}

func settingStringSlice(ctx sdk.Context, key string) ([]string, bool) {
	if Setting == nil {
		return nil, false
	}
	v := Setting(ctx, key)
	if v == nil {
		return nil, false
	}
	switch arr := v.(type) {
	case []string:
		out := make([]string, len(arr))
		copy(out, arr)
		return out, true
	case []any:
		out := make([]string, 0, len(arr))
		for _, el := range arr {
			if s, ok := el.(string); ok {
				out = append(out, s)
			}
		}
		return out, true
	}
	return nil, false
}

var (
	chromeMu          sync.Mutex
	chromeSessions    = map[string]*chromeState{}
	endedSessions     = map[string]struct{}{}
	endedOrder        []string
	surfaceUnsubFn    func()
	surfaceListenerMu sync.Mutex
)

const maxEndedSessionTombstones = 4096

func sessionKeyFromCtx(ctx sdk.Context) string {
	var zero sdk.Context
	if ctx == zero {
		return ""
	}
	if id, err := ctx.GetSessionID(); err == nil && id != "" {
		return id
	}
	return ""
}

// beginChromeSession marks ctx's session live (clearing any prior tombstone
// from a reload or resume) and installs a fresh chromeState for it.
// Called first thing in OnSessionStart so a reloaded/resumed session with the
// same ID gets fresh header, footer, status line, and card invalidation.
func beginChromeSession(ctx sdk.Context) *chromeState {
	key := sessionKeyFromCtx(ctx)
	if key == "" {
		return nil
	}
	chromeMu.Lock()
	if _, wasEnded := endedSessions[key]; wasEnded {
		delete(endedSessions, key)
		for i, id := range endedOrder {
			if id == key {
				endedOrder = append(endedOrder[:i], endedOrder[i+1:]...)
				break
			}
		}
	}
	prev := chromeSessions[key]
	st := newChromeState()
	chromeSessions[key] = st
	chromeMu.Unlock()
	if prev != nil {
		prev.close()
	}
	return st
}

// chromeSession returns the per-session state for ctx, or nil when the session
// ID cannot be resolved or has already ended via lifecycle.End (never creating
// a shared default or resurrecting a dead session on a late event/render).
func chromeSession(ctx sdk.Context) *chromeState {
	key := sessionKeyFromCtx(ctx)
	if key == "" {
		return nil
	}
	chromeMu.Lock()
	defer chromeMu.Unlock()
	if _, ended := endedSessions[key]; ended {
		return nil
	}
	st, ok := chromeSessions[key]
	if !ok {
		st = newChromeState()
		chromeSessions[key] = st
	}
	return st
}

// ensureSurfaceListener registers one package-level surface.OnChange handler
// that broadcasts surface flips only to currently live sessions in chromeSessions.
func ensureSurfaceListener() {
	surfaceListenerMu.Lock()
	if surfaceUnsubFn != nil {
		surfaceListenerMu.Unlock()
		return
	}
	surfaceUnsubFn = surface.OnChange(handleSurfaceChange)
	surfaceListenerMu.Unlock()
}

// resetSurfaceListener re-subscribes after tests call surface.ResetForTests().
func resetSurfaceListener() {
	surfaceListenerMu.Lock()
	if surfaceUnsubFn != nil {
		surfaceUnsubFn()
		surfaceUnsubFn = nil
	}
	surfaceUnsubFn = surface.OnChange(handleSurfaceChange)
	surfaceListenerMu.Unlock()
}

func handleSurfaceChange(native bool) {
	chromeMu.Lock()
	live := make([]*chromeState, 0, len(chromeSessions))
	for _, st := range chromeSessions {
		live = append(live, st)
	}
	chromeMu.Unlock()

	for _, st := range live {
		st.onSurfaceChange(native)
	}
}

func releaseChromeSession(sessionID string) {
	if sessionID == "" {
		return
	}
	chromeMu.Lock()
	st := chromeSessions[sessionID]
	delete(chromeSessions, sessionID)
	if _, already := endedSessions[sessionID]; !already {
		endedSessions[sessionID] = struct{}{}
		endedOrder = append(endedOrder, sessionID)
		if len(endedOrder) > maxEndedSessionTombstones {
			evict := endedOrder[0]
			endedOrder = endedOrder[1:]
			delete(endedSessions, evict)
		}
	}
	chromeMu.Unlock()
	if st != nil {
		st.close()
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// Welcome header
// upstream: packages/tui/src/prompt/welcome.ts, packages/tui/src/prompt/tips.txt
// ═══════════════════════════════════════════════════════════════════════════

// ompWordmark is omp's half-block wordmark placed beside the mashup logo (welcome.ts:445).
var ompWordmark = []string{
	"▄▀▀▄ █▀▄▀▄ █▀▀▄",
	"▀▄▄▀ █ █ █ █▄▄▀",
	"           █",
}

const (
	welcomeLogoWidth     = brand.CanvasWidth // 24 cells (24×22 pixel mashup in half blocks -> 11 rows)
	welcomeLogoRows      = brand.CanvasHeight / 2
	welcomeWordmarkWidth = 15
	welcomeLockupGap     = 4
	welcomeTipMeasure    = 72
	welcomeTipMinCols    = 50
)

// welcomeTips holds omp's non-Tern tips from packages/tui/src/prompt/tips.txt,
// filtered strictly to commands, CLI features, and shortcuts that exist in
// oh-my-pig (unported commands like /shake, /omfg, /collab, /join,
// /usage reset, and omp CLI subcommands like `omp stats`, `omp cleanse`,
// or `omp auth-broker` are omitted). Key placeholders `{key:...}` and
// `{tap:...}` are expanded at render time through expandTipKeys
// (upstream: prompt/welcome.ts:109-119).
var welcomeTips = []string{
	`Tired of typing "keep going"? Just send a '.'`,
	`Type / at the start of your prompt to browse every command`,
	`Start your prompt with ! to run it as bash — !! runs it without adding the output to context`,
	`You can /btw to ask a side question`,
	`Use /tan to fork the current conversation into a background agent`,
	`With default keybindings, {key:ctrl+d} deletes the character under the cursor instead of quitting your draft — see /hotkeys for your exit key`,
	`Try task isolation to create CoW worktrees`,
	`Need a cheap nested model call? Use completion(x...). Have a big batch of tasks? Ask the agent to use it!`,
	`Drop the word ultrathink in your message for harder multi-step reasoning`,
	`Agent overthinking? Say chillax — on models with per-message effort, thinking drops to the lowest level, even mid-turn`,
	`Say orchestrate in your message to drive a multi-phase task with parallel subagents`,
	`Say workflowz in your message to drive the task with parallel subagents in eval`,
	`Bulk data to classify or triage? Say jevify in your message — the eval kernel's judge() processes the batch and you read only what it flags`,
	`Use /switch to switch model, and {key:ctrl+p} to cycle models`,
	`Press {key:ctrl+r} to search your prompt history and reuse a past message`,
	`/force read pins the next turn to one specific tool when the model keeps reaching for the wrong one`,
	`/copy code grabs the last code block to your clipboard — /copy cmd grabs the last shell/python command`,
	`Turn on /advisor to attach a second model that reviews every turn and quietly injects advice`,
	`Try starting your prompt with a ->, and writing a list (1. Do X, 2. Do Y)`,
	`Press {key:shift+tab} to cycle through reasoning effort levels`,
	`Lint/type errors piling up? /cleanse hunts project diagnostics and fixes them with parallel subagents — {key:escape} cancels`,
}

// expandTipKeys expands omp's `{key:...}` and `{tap:...}` placeholders using
// platform-aware unicode keycap glyphs (`⌃D` on macOS, `Ctrl+D` elsewhere;
// `⇧⇥`, `⎋`, `←←`).
// upstream: packages/tui/src/prompt/welcome.ts:109-119,
//
//	packages/tui/src/key-hint-format.ts:41-137
func expandTipKeys(tip string, goos string) string {
	if !strings.Contains(tip, "{key:") && !strings.Contains(tip, "{tap:") {
		return tip
	}
	mac := goos == "darwin"
	var b strings.Builder
	for {
		kIdx := strings.Index(tip, "{key:")
		tIdx := strings.Index(tip, "{tap:")
		start := -1
		kind := ""
		switch {
		case kIdx >= 0 && (tIdx < 0 || kIdx < tIdx):
			start = kIdx
			kind = "key"
		case tIdx >= 0:
			start = tIdx
			kind = "tap"
		}
		if start < 0 {
			b.WriteString(tip)
			break
		}
		b.WriteString(tip[:start])
		prefixLen := len("{key:")
		rest := tip[start+prefixLen:]
		end := strings.IndexByte(rest, '}')
		if end < 0 {
			b.WriteString(tip[start:])
			break
		}
		formatted := formatKeyHintForOS(rest[:end], mac)
		if kind == "tap" {
			if hasASCIIAlnum(formatted) {
				b.WriteString(formatted + " " + formatted)
			} else {
				b.WriteString(formatted + formatted)
			}
		} else {
			b.WriteString(formatted)
		}
		tip = rest[end+1:]
	}
	return b.String()
}

func hasASCIIAlnum(s string) bool {
	for i := range len(s) {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			return true
		}
	}
	return false
}

func formatKeyHintForOS(key string, mac bool) string {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(key)), "+")
	if len(parts) == 0 {
		return key
	}
	base := parts[len(parts)-1]
	mods := parts[:len(parts)-1]
	hasMod := func(name string) bool {
		for _, m := range mods {
			if m == name {
				return true
			}
		}
		return false
	}
	var b strings.Builder
	modOrder := []struct {
		name     string
		macGlyph string
		pcWord   string
	}{
		{"ctrl", "⌃", "Ctrl"},
		{"alt", "⌥", "Alt"},
		{"shift", "⇧", "⇧"},
		{"super", "⌘", "Super"},
	}
	for _, m := range modOrder {
		if !hasMod(m.name) {
			continue
		}
		if mac {
			b.WriteString(m.macGlyph)
		} else {
			b.WriteString(m.pcWord)
			if m.pcWord != "⇧" {
				b.WriteByte('+')
			}
		}
	}
	switch base {
	case "esc", "escape":
		b.WriteString("⎋")
	case "enter", "return":
		b.WriteString("⏎")
	case "tab":
		b.WriteString("⇥")
	case "space":
		b.WriteString("␣")
	case "backspace":
		b.WriteString("⌫")
	case "delete":
		b.WriteString("⌦")
	case "up":
		b.WriteString("↑")
	case "down":
		b.WriteString("↓")
	case "left":
		b.WriteString("←")
	case "right":
		b.WriteString("→")
	default:
		if len(base) == 1 {
			if len(mods) > 0 {
				b.WriteString(strings.ToUpper(base))
			} else {
				b.WriteString(base)
			}
		} else if len(base) > 0 {
			b.WriteString(strings.ToUpper(base[:1]) + base[1:])
		}
	}
	return b.String()
}

// noColorActive reports whether the NO_COLOR convention (non-empty NO_COLOR
func noColorActive() bool {
	return os.Getenv("NO_COLOR") != ""
}

// welcomeAnsiRGB formats a foreground (isBg=false) or background (isBg=true) SGR
// sequence for an RGB triplet in truecolor or xterm-256 mode using PiG SDK's
// tui colour conversion (matching PiG's coding/piglogin/head.go HeadLines).
func welcomeAnsiRGB(r, g, b uint8, isBg, trueColor bool) string {
	mode := tui.TerminalColorModeTrueColor
	if !trueColor {
		mode = tui.TerminalColorMode256
	}
	c := tui.RgbColorValue{R: float64(r), G: float64(g), B: float64(b)}
	if isBg {
		return tui.BackgroundAnsi(c, mode)
	}
	return tui.ForegroundAnsi(c, mode)
}

// mashupLogoHalfBlocks renders oh-my-pig's 24×22 pixel-art mashup (Candidate 3)
// as 11 rows of 24 terminal cells using Unicode half-block characters (`▀`, `▄`, `█`)
// so two art-pixel rows pack into each terminal row.
//
// Colours:
//   - Pig head: PiG's default green sprite palette (`pig-default`: outline/pupils
//     #18151d, body/inner-ear #48a381, highlight #64be9e, snout #32775e,
//     blush #82dbba, eye whites #fefefe), foreground = upper pixel and
//     background = lower pixel on each half-block cell.
//   - π gate: flat #EEDAAD (omp's dark-theme cream) on dark/unspecified themes,
//     or flat #1B1C20 on light themes.
//   - Truecolor terminals emit 24-bit SGR (38;2 / 48;2); 256-color terminals
//     (and 16-color terminals, which PiG's UITheme reports as "256color") map
//     every pixel through PiG SDK's tui.ForegroundAnsi/BackgroundAnsi xterm-256
//     quantiser (38;5 / 48;5); when NO_COLOR is set (noColor=true), the same
//     half-block silhouette is drawn with no SGR colour sequences at all.
//
// Transparent pixels leave the terminal background untouched; each coloured cell
// resets cleanly.
func mashupLogoHalfBlocks(head brand.Head, piHex string, trueColor, noColor bool) []string {
	pixels := brand.Compose(head, piHex)
	var grid [brand.CanvasHeight][brand.CanvasWidth]string
	for _, p := range pixels {
		grid[p.Y][p.X] = p.Color
	}
	rows := make([]string, welcomeLogoRows)
	for row := range welcomeLogoRows {
		topY := row * 2
		botY := topY + 1
		var b strings.Builder
		for x := range brand.CanvasWidth {
			tc := grid[topY][x]
			bc := grid[botY][x]
			if noColor {
				switch {
				case tc == "" && bc == "":
					b.WriteByte(' ')
				case tc != "" && bc == "":
					b.WriteRune('▀')
				case tc == "" && bc != "":
					b.WriteRune('▄')
				default:
					b.WriteRune('█')
				}
				continue
			}
			switch {
			case tc == "" && bc == "":
				b.WriteByte(' ')
			case tc != "" && bc == "":
				r, g, bl, _ := brand.ParseHexRGB(tc)
				b.WriteString(welcomeAnsiRGB(r, g, bl, false, trueColor))
				b.WriteRune('▀')
				b.WriteString("\x1b[0m")
			case tc == "" && bc != "":
				r, g, bl, _ := brand.ParseHexRGB(bc)
				b.WriteString(welcomeAnsiRGB(r, g, bl, false, trueColor))
				b.WriteRune('▄')
				b.WriteString("\x1b[0m")
			case tc == bc:
				r, g, bl, _ := brand.ParseHexRGB(tc)
				b.WriteString(welcomeAnsiRGB(r, g, bl, false, trueColor))
				b.WriteRune('█')
				b.WriteString("\x1b[0m")
			default:
				tr, tg, tb, _ := brand.ParseHexRGB(tc)
				br, bg, bb, _ := brand.ParseHexRGB(bc)
				b.WriteString(welcomeAnsiRGB(tr, tg, tb, false, trueColor))
				b.WriteString(welcomeAnsiRGB(br, bg, bb, true, trueColor))
				b.WriteRune('▀')
				b.WriteString("\x1b[0m")
			}
		}
		rows[row] = b.String()
	}
	return rows
}

// RenderWelcomeLines builds oh-my-pig's centered welcome banner (Candidate-3
// half-block π-plus-pig mashup + wordmark + version + wrapped tip at 72 cols)
// with every row guaranteed <= width.
// upstream: packages/tui/src/prompt/welcome.ts WelcomeComponent.#renderLines
func RenderWelcomeLines(theme sdk.UITheme, version string, width int, tipIndex int) []string {
	noColor := noColorActive()
	p := paletteFromTheme(theme)
	if width <= 0 {
		return nil
	}
	room := width - 2
	if room < 4 {
		return nil
	}
	if version == "" {
		version = PigletVersion
	}
	if version != "" && !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	styledVersion := version
	if !noColor {
		styledVersion = p.fg("dim", version)
	}
	lockupWidth := welcomeLogoWidth + welcomeLockupGap + max(welcomeWordmarkWidth, tui.VisibleWidth(styledVersion))

	trueColor := theme.GetColorMode() != "256color"
	piHex := brand.PiDarkHex
	if theme.Appearance() == "light" {
		piHex = brand.PiLightHex
	}
	styledLogo := mashupLogoHalfBlocks(brand.DefaultHead(), piHex, trueColor, noColor)

	var art []string
	switch {
	case room >= lockupWidth:
		// Vertically centre the 4-row wordmark+version block (3 wordmark rows + 1 version row)
		// beside the 11-row half-block mashup on rows 3..6 (centred alongside the pig + bar).
		beside := make([]string, welcomeLogoRows)
		if noColor {
			beside[3] = ompWordmark[0]
			beside[4] = ompWordmark[1]
			beside[5] = ompWordmark[2]
			beside[6] = styledVersion
		} else {
			beside[3] = p.bold(p.fg("text", ompWordmark[0]))
			beside[4] = p.bold(p.fg("text", ompWordmark[1]))
			beside[5] = p.bold(p.fg("text", ompWordmark[2]))
			beside[6] = styledVersion
		}
		gap := strings.Repeat(" ", welcomeLockupGap)
		for i, row := range styledLogo {
			if beside[i] != "" {
				art = append(art, row+gap+beside[i])
			} else {
				art = append(art, row)
			}
		}
	case room >= welcomeLogoWidth:
		art = styledLogo
	}

	lines := centerLines(art, width)
	if width >= welcomeTipMinCols && len(welcomeTips) > 0 {
		tip := expandTipKeys(welcomeTips[absMod(tipIndex, len(welcomeTips))], runtime.GOOS)
		measure := min(welcomeTipMeasure, max(10, room))
		tipLine := "Tip: " + tip
		if !noColor {
			tipLine = p.fg("accent", "Tip: ") + p.fg("muted", tip)
		}
		wrapped := wrapAnsi(tipLine, measure)
		lines = append(lines, "")
		for _, wline := range wrapped {
			lines = append(lines, centerLines([]string{wline}, width)...)
		}
	}
	return clampRows(lines, width)
}

func centerLines(lines []string, width int) []string {
	if len(lines) == 0 || width <= 0 {
		return nil
	}
	widest := 0
	for _, l := range lines {
		if w := tui.VisibleWidth(l); w > widest {
			widest = w
		}
	}
	indent := strings.Repeat(" ", max(0, (width-widest)/2))
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = clampRow(indent+l, width)
	}
	return out
}

func absMod(a, m int) int {
	r := a % m
	if r < 0 {
		r += m
	}
	return r
}

// ═══════════════════════════════════════════════════════════════════════════
// Status line (footer)
// upstream: packages/tui/src/status-line/{presets,segments,component}.ts
// ═══════════════════════════════════════════════════════════════════════════

// StatusLineData holds all cached inputs needed to render omp's default status line
// without making any host IPC calls inside the render callback.
type StatusLineData struct {
	ModelName            string
	ModelReasoning       bool
	ThinkingLevel        string
	CompactThinkingLevel *bool // nil = omp default true (statusLine.compactThinkingLevel)
	ContextLineMode      string
	AutoCompactDisabled  bool
	ModeLabel            string
	Cwd                  string
	GitBranch            string
	PRNumber             int
	SessionName          string
	ContextTokens        *int
	ContextWindow        int
	ContextPercent       *float64
	EstimatedCost        float64
	Working              bool
	WorkingMessage       string
	WorkingStartedAt     time.Time
	SpinnerGlyph         string
	PinnedError          string
	Now                  time.Time // optional clock override for deterministic tests
}

// getProjectDirDisplay mirrors packages/tui/src/status-line/segments.ts:137-201:
// classifies scratch directories (/tmp, /var/tmp, $TMPDIR, ~/tmp) and strips
// standard work roots (~/Projects, ~/repos, /work).
func getProjectDirDisplay(projectDir string) (scratch bool, displayPath string) {
	if projectDir == "" {
		return false, ""
	}
	clean := filepath.Clean(dirs.StandardizeMacOSPath(projectDir))
	home, _ := os.UserHomeDir()
	scratchRoots := []string{os.TempDir(), dirs.StandardizeMacOSPath(os.TempDir())}
	if home != "" {
		scratchRoots = append(scratchRoots, filepath.Join(home, "tmp"))
	}
	if runtime.GOOS == "windows" {
		if t := os.Getenv("TEMP"); t != "" {
			scratchRoots = append(scratchRoots, t)
		}
		if t := os.Getenv("TMP"); t != "" {
			scratchRoots = append(scratchRoots, t)
		}
		if sr := os.Getenv("SystemRoot"); sr != "" {
			scratchRoots = append(scratchRoots, filepath.Join(sr, "Temp"))
		}
	} else {
		scratchRoots = append(scratchRoots, "/tmp", "/var/tmp")
		if runtime.GOOS == "darwin" {
			scratchRoots = append(scratchRoots, "/private/tmp", "/private/var/tmp")
		}
	}
	for _, root := range scratchRoots {
		if rel, ok := relWithinRoot(root, clean); ok {
			if rel == "" {
				return true, clean
			}
			return true, filepath.ToSlash(rel)
		}
	}
	workRoots := []string{"/work"}
	if home != "" {
		workRoots = append([]string{filepath.Join(home, "Projects"), filepath.Join(home, "repos")}, workRoots...)
	}
	for _, root := range workRoots {
		if rel, ok := relWithinRoot(root, clean); ok && rel != "" {
			return false, filepath.ToSlash(rel)
		}
	}
	return false, clean
}

func relWithinRoot(root, target string) (string, bool) {
	if root == "" {
		return "", false
	}
	cr := filepath.Clean(root)
	ct := filepath.Clean(target)
	if runtime.GOOS == "windows" {
		if strings.EqualFold(cr, ct) {
			return "", true
		}
		prefix := cr
		if !strings.HasSuffix(prefix, string(filepath.Separator)) {
			prefix += string(filepath.Separator)
		}
		if len(ct) >= len(prefix) && strings.EqualFold(ct[:len(prefix)], prefix) {
			return ct[len(prefix):], true
		}
		return "", false
	}
	if cr == ct {
		return "", true
	}
	prefix := cr
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	if strings.HasPrefix(ct, prefix) {
		return ct[len(prefix):], true
	}
	return "", false
}

// formatContextUsage mirrors packages/tui/src/chrome/context-thresholds.ts:83-93:
// `<percent.1f>%/<window>` when window > 0, or `<tokens>/?` when unknown.
func formatContextUsage(pct *float64, window int, tokens *int) string {
	if window <= 0 {
		used := 0
		if tokens != nil {
			used = *tokens
		}
		return fmt.Sprintf("%s/?", formatNumber(used))
	}
	if pct == nil {
		return formatNumber(window)
	}
	return fmt.Sprintf("%.1f%%/%s", *pct, formatNumber(window))
}

// contextUsageColor mirrors packages/tui/src/chrome/context-thresholds.ts:31-56,108-119.
func contextUsageColor(pct float64, window int) string {
	reaches := func(pctThresh float64, tokThresh int) bool {
		if pct <= 0 || math.IsNaN(pct) {
			return false
		}
		if window <= 0 {
			return pct >= pctThresh
		}
		tokPct := (float64(tokThresh) / float64(window)) * 100.0
		return pct >= math.Min(pctThresh, tokPct)
	}
	switch {
	case reaches(90, 500_000):
		return "error"
	case reaches(70, 270_000):
		return "thinkingHigh"
	case reaches(50, 150_000):
		return "warning"
	default:
		return "muted"
	}
}

// formatThinkingDisplay mirrors packages/tui/src/theme/symbols.ts UNICODE_SYMBOLS thinking.*.
func formatThinkingDisplay(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "off":
		return unicodeSymbols.statusDisabled + " off"
	case "minimal":
		return unicodeSymbols.thinkingMinimal
	case "low":
		return unicodeSymbols.thinkingLow
	case "medium":
		return unicodeSymbols.thinkingMedium
	case "high":
		return unicodeSymbols.thinkingHigh
	case "xhigh":
		return unicodeSymbols.thinkingXhigh
	case "max":
		return unicodeSymbols.thinkingMax
	case "auto":
		return unicodeSymbols.thinkingAutoPending + " auto"
	default:
		return sanitizeDisplayText(level)
	}
}

// leadingGlyph returns the leading glyph of a thinking-level display string
// (e.g. "◑ med" -> "◑", "⦸ off" -> "⦸").
// upstream: packages/tui/src/status-line/segments.ts:102-105
func leadingGlyph(display string) string {
	if sp := strings.IndexByte(display, ' '); sp >= 0 {
		return display[:sp]
	}
	return display
}

func formatPathSegment(p palette, cwd string, maxLen int) string {
	scratch, displayPath := getProjectDirDisplay(cwd)
	pwd := clampLeftPath(shortenPath(displayPath), maxLen)
	icon := unicodeSymbols.iconFolder
	if scratch {
		icon = unicodeSymbols.iconScratchFolder
	}
	tok := "statusLinePath"
	if p.fgAnsi(tok) == "" {
		tok = "muted"
	}
	return p.fg(tok, icon+" "+sanitizeDisplayText(pwd))
}

// buildStatusLineLeftSegments builds omp's default preset left segments in order.
// When includeContext is false (embedded gauge mode), the standalone context_pct
// segment is omitted because the gauge carries the percentage and window label.
// upstream: packages/tui/src/status-line/segments.ts:207-413, presets.ts:5-14
func buildStatusLineLeftSegments(p palette, d StatusLineData, includeContext bool) (parts []string, ids []string) {
	// 1. pi brand: idle `π` (dim); working `<spinner> <elapsed>` (accent).
	// upstream: packages/tui/src/status-line/segments.ts:218-232
	if d.Working {
		glyph := d.SpinnerGlyph
		if glyph == "" {
			glyph = ompActivitySpinnerFrames[0]
		}
		now := d.Now
		if now.IsZero() {
			now = time.Now()
		}
		var elapsed time.Duration
		if !d.WorkingStartedAt.IsZero() {
			elapsed = now.Sub(d.WorkingStartedAt)
		}
		parts = append(parts, p.fg("accent", glyph+" "+formatBrandTimer(elapsed)))
	} else {
		parts = append(parts, p.fg("dim", unicodeSymbols.iconOmp))
	}
	ids = append(ids, "pi")

	// 2. model (+ thinking level).
	// upstream: packages/tui/src/status-line/segments.ts:359-391,
	//           packages/coding-agent/src/modes/settings.ts:257-260
	modelName := sanitizeDisplayText(d.ModelName)
	if modelName == "" {
		modelName = "no-model"
	}
	thinkingDisplay := ""
	if d.ModelReasoning && d.ThinkingLevel != "" {
		thinkingDisplay = formatThinkingDisplay(d.ThinkingLevel)
	}
	compactThinking := true
	if d.CompactThinkingLevel != nil {
		compactThinking = *d.CompactThinkingLevel
	}
	compact := compactThinking && thinkingDisplay != ""
	modelIcon := unicodeSymbols.iconModel
	if compact {
		modelIcon = leadingGlyph(thinkingDisplay)
	}
	modelStr := modelIcon + " " + modelName
	if !compact && thinkingDisplay != "" {
		modelStr += unicodeSymbols.sepDot + thinkingDisplay
	}
	modelTok := "statusLineModel"
	if p.fgAnsi(modelTok) == "" {
		modelTok = "accent"
	}
	parts = append(parts, p.fg(modelTok, modelStr))
	ids = append(ids, "model")

	// 3. mode (if active via extension status["mode"], formatted with omp's mode icons)
	// upstream: packages/tui/src/status-line/segments.ts:507-555
	if modeText, modeColor := formatModeSegment(d.ModeLabel); modeText != "" {
		parts = append(parts, p.fg(modeColor, modeText))
		ids = append(ids, "mode")
	}
	// 4. path (scratch detection + work-root stripping + ~ abbreviation + 40-char clamp)
	if d.Cwd != "" {
		parts = append(parts, formatPathSegment(p, d.Cwd, 40))
		ids = append(ids, "path")
	}

	// 5. git
	if br := flattenLine(d.GitBranch); br != "" {
		gitTok := "statusLineGitClean"
		if p.fgAnsi(gitTok) == "" {
			gitTok = "success"
		}
		parts = append(parts, p.fg(gitTok, unicodeSymbols.iconBranch+" "+br))
		ids = append(ids, "git")
	}

	// 6. pr
	if d.PRNumber > 0 {
		parts = append(parts, p.fg("accent", fmt.Sprintf("%s #%d", unicodeSymbols.iconPR, d.PRNumber)))
		ids = append(ids, "pr")
	}

	// 7. context_pct (omp formatContextUsage: one decimal `0.4%/128K`)
	if includeContext && (d.ContextWindow > 0 || d.ContextPercent != nil) {
		pctVal := 0.0
		if d.ContextPercent != nil {
			pctVal = *d.ContextPercent
		}
		ctxColor := contextUsageColor(pctVal, d.ContextWindow)
		ctxText := formatContextUsage(d.ContextPercent, d.ContextWindow, d.ContextTokens)
		parts = append(parts, unicodeSymbols.iconContext+" "+p.fg(ctxColor, ctxText))
		ids = append(ids, "context_pct")
	}

	// 8. cost
	if d.EstimatedCost > 0 {
		costTok := "statusLineCost"
		if p.fgAnsi(costTok) == "" {
			costTok = "muted"
		}
		parts = append(parts, p.fg(costTok, fmt.Sprintf("$%.2f", d.EstimatedCost)))
		ids = append(ids, "cost")
	}

	return parts, ids
}

// RenderStatusLine draws omp's standalone status line (used when band composer
// is disabled or rendered standalone in tests) at width columns.
// Default preset (packages/tui/src/status-line/presets.ts:5-14):
//
//	left:  pi, vim, model, mode, collab, stream, path, git, pr, context_pct, cost
//	right: session_name
//	sep:   powerline-thin (`>` left, `<` right in unicode preset)
//
// Non-preset segments (such as `token_total` or arbitrary extension statuses)
// are NOT included in the default preset. Every row satisfies tui.VisibleWidth(row) <= width.
func RenderStatusLine(theme sdk.UITheme, d StatusLineData, width int) []string {
	p := paletteFromTheme(theme)
	if width <= 0 {
		return nil
	}

	left, _ := buildStatusLineLeftSegments(p, d, true)

	var right []string
	if sn := flattenLine(d.SessionName); sn != "" {
		right = append(right, p.fg("accent", sn))
	}

	sepTok := "statusLineSep"
	if p.fgAnsi(sepTok) == "" {
		sepTok = "dim"
	}
	leftSep := " " + p.fg(sepTok, unicodeSymbols.sepPowerlineThinL) + " "
	rightSep := " " + p.fg(sepTok, unicodeSymbols.sepPowerlineThinR) + " "

	buildLeft := func(segs []string) string {
		if len(segs) == 0 {
			return ""
		}
		return " " + strings.Join(segs, leftSep) + " "
	}
	buildRight := func(segs []string) string {
		if len(segs) == 0 {
			return ""
		}
		return " " + strings.Join(segs, rightSep) + " "
	}

	rightStr := buildRight(right)
	rightW := tui.VisibleWidth(rightStr)
	for len(left) > 2 && tui.VisibleWidth(buildLeft(left))+rightW+1 > width {
		left = left[:len(left)-1]
	}
	leftStr := buildLeft(left)
	leftW := tui.VisibleWidth(leftStr)

	if rightStr == "" {
		return []string{truncateWidth(leftStr, width)}
	}
	if leftW+rightW+1 > width {
		return []string{truncateWidth(leftStr+" "+rightStr, width)}
	}
	gap := width - leftW - rightW
	return []string{clampRow(leftStr+strings.Repeat(" ", gap)+rightStr, width)}
}
func clampLeftPath(p string, maxLen int) string {
	runes := []rune(p)
	if len(runes) <= maxLen {
		return p
	}
	if maxLen <= 1 {
		return "…"
	}
	return "…" + string(runes[len(runes)-(maxLen-1):])
}

// ═══════════════════════════════════════════════════════════════════════════
// Working indicator & intent-only working messages
// upstream: packages/coding-agent/src/modes/interactive-mode.ts, event-controller.ts
// ═══════════════════════════════════════════════════════════════════════════

const defaultWorkingMessage = "Working…"

// chromeState caches session status-line data incrementally so the footer
// renderer callback makes ZERO host IPC calls per paint and survives
// cancellation of short-lived event handler contexts.
type chromeState struct {
	mu           sync.Mutex
	closed       bool
	sessionCtx   sdk.Context
	hasSession   bool
	chromeLoaded bool
	tipIndex     int
	version      string
	lastIntent   string

	// Per-session tool-card invalidators (keyed by card slot ID).
	nextCardID uint64
	cards      map[uint64]func()

	// Displaceable `todo` snapshot card tracking (upstream:
	// packages/coding-agent/src/modes/controllers/event-controller.ts:2376-2386,
	// packages/tui/src/chat/tool-execution.ts:119-128).
	// A later successful non-partial todo snapshot with `details.phases`
	// displaces the previous live todo card by marking it displaced and
	// invoking its `Invalidate` callback so it re-renders empty.
	activeTodoCardID    uint64
	displacedTodoCardID map[uint64]bool
	// Per-session subagent arrival slots for agent-identity foreground colors
	// (upstream: packages/tui/src/overlays/agent-identity.ts:146-183).
	agentSlots map[string]int
	// using sessionCtx whenever cached StatusLineData changes).
	footerRepaintQueued  bool
	footerRepaintRunning bool
	bandOrderDirty       bool
	// Lightweight working-turn ticker that advances the active brand spinner
	// glyph and elapsed seconds while `data.Working` is true; stopped on
	// `endTurn()` and `close()`.
	workingTickStop chan struct{}

	// Cached status-line snapshot + incremental cost accounting
	data              StatusLineData
	inCostPer1M       float64
	outCostPer1M      float64
	cacheReadPer1M    float64
	cacheWritePer1M   float64
	countedUsageEntry int

	// Per-turn usage & live composer metadata (Wave 7 surface publishers)
	modelProvider      string
	modelID            string
	modelWindow        int
	modelHasVision     bool
	modelPremiumMult   float64
	turnSeq            int
	turnStartedAt      time.Time
	turnInput          int
	turnOutput         int
	turnCacheRead      int
	turnCacheWrite     int
	turnCost           float64
	turnHasUsage       bool
	turnPremiumCounted bool
	premiumRequests    float64
	advisorCost        float64
	advisorSub         bool
	lastTokPerSec      float64
}

func newChromeState() *chromeState {
	return &chromeState{
		tipIndex:   int(math.Abs(float64(time.Now().UnixNano() % int64(len(welcomeTips))))),
		version:    PigletVersion,
		cards:      map[uint64]func(){},
		agentSlots: map[string]int{},
	}
}

func (s *chromeState) stopWorkingTickerLocked() {
	if s.workingTickStop != nil {
		close(s.workingTickStop)
		s.workingTickStop = nil
	}
}

func (s *chromeState) close() {
	s.mu.Lock()
	s.closed = true
	s.hasSession = false
	s.chromeLoaded = false
	s.cards = nil
	s.activeTodoCardID = 0
	s.displacedTodoCardID = nil
	s.agentSlots = nil
	s.footerRepaintQueued = false
	s.stopWorkingTickerLocked()
	s.mu.Unlock()
}

// agentSlot returns a stable per-session arrival slot for subagent id
// (upstream: packages/tui/src/overlays/agent-identity.ts:162-177).
func (s *chromeState) agentSlot(agentID string) int {
	if s == nil || agentID == "" {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.agentSlots == nil {
		s.agentSlots = map[string]int{}
	}
	if slot, ok := s.agentSlots[agentID]; ok {
		return slot
	}
	slot := len(s.agentSlots)
	s.agentSlots[agentID] = slot
	return slot
}

func (s *chromeState) trackCardInvalidate(rc sdk.ToolRenderContext) uint64 {
	if s == nil || rc.State == nil {
		return 0
	}
	if existing, ok := rc.State["ansiInvID"].(uint64); ok && existing != 0 {
		if rc.Invalidate != nil {
			s.mu.Lock()
			if !s.closed {
				if s.cards == nil {
					s.cards = map[uint64]func(){}
				}
				s.cards[existing] = rc.Invalidate
			}
			s.mu.Unlock()
		}
		return existing
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return 0
	}
	if s.cards == nil {
		s.cards = map[uint64]func(){}
	}
	s.nextCardID++
	id := s.nextCardID
	if rc.Invalidate != nil {
		s.cards[id] = rc.Invalidate
	}
	s.mu.Unlock()
	rc.State["ansiInvID"] = id
	return id
}

// recordTodoSnapshot registers a completed successful todo snapshot card
// (`!isError && !isPartial && details.phases != nil`) and displaces any
// earlier live todo snapshot card in this session by invalidating it so it
// re-renders empty (upstream: event-controller.ts:2376-2386,
// tool-execution.ts:119-128). Returns whether cardID itself is displaced.
func (s *chromeState) recordTodoSnapshot(cardID uint64, displaceable bool) bool {
	if s == nil || cardID == 0 {
		return false
	}
	var prevInv func()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return false
	}
	if s.displacedTodoCardID[cardID] {
		s.mu.Unlock()
		return true
	}
	if displaceable && s.activeTodoCardID != cardID {
		if prev := s.activeTodoCardID; prev != 0 {
			if s.displacedTodoCardID == nil {
				s.displacedTodoCardID = map[uint64]bool{}
			}
			s.displacedTodoCardID[prev] = true
			prevInv = s.cards[prev]
		}
		s.activeTodoCardID = cardID
	}
	s.mu.Unlock()
	if prevInv != nil {
		prevInv()
	}
	return false
}

func (s *chromeState) isTodoDisplaced(cardID uint64) bool {
	if s == nil || cardID == 0 {
		return false
	}
	s.mu.Lock()
	displaced := s.displacedTodoCardID[cardID]
	s.mu.Unlock()
	return displaced
}
func (s *chromeState) onSurfaceChange(native bool) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	c := s.sessionCtx
	hasSess := s.hasSession
	wasLoaded := s.chromeLoaded
	pinnedErr := s.data.PinnedError
	fns := make([]func(), 0, len(s.cards))
	for _, fn := range s.cards {
		fns = append(fns, fn)
	}
	if native {
		s.stopWorkingTickerLocked()
		if wasLoaded {
			s.chromeLoaded = false
		}
	}
	s.mu.Unlock()

	if hasSess {
		if native {
			if wasLoaded {
				_ = c.SetHeaderRenderer(nil)
				_ = c.SetFooterRenderer(nil)
				_ = c.SetWidgetRenderer(statusBandWidgetKey, nil)
				c.SetWorkingVisible(true)
			}
			if pinnedErr != "" {
				s.syncNativeErrorBanner(c, pinnedErr)
			}
		} else {
			_ = c.SetWidget(nativeErrorBannerWidgetKey, nil)
			s.installTerminalHeaderFooter(c)
		}
	}
	for _, fn := range fns {
		fn()
	}
}

// syncNativeErrorBanner publishes or clears the structured `omp-error-banner`
// above-editor widget on the native Tern surface (`omp.errorBanner` kit.View).
// No-op when surface.Native() is false.
func (s *chromeState) syncNativeErrorBanner(ctx sdk.Context, pinnedErr string) {
	if !surface.Native() || !ctx.HasUI() {
		return
	}
	if view, ok := BuildNativeErrorBannerView(pinnedErr); ok {
		_ = ctx.SetWidget(nativeErrorBannerWidgetKey, view)
		return
	}
	_ = ctx.SetWidget(nativeErrorBannerWidgetKey, nil)
}

// formatModeSegment turns extension status["mode"] labels (set by
// internal/modes/state.go: "PLAN", "PLAN (paused)", "GOAL ...", "GOAL (paused)")
// into omp's status-line mode segment (`🗺 Plan`, `🗺 Plan ⏸`, `🎯 Goal ...`).
// upstream: packages/tui/src/status-line/segments.ts:453-555
func formatModeSegment(raw string) (text string, color string) {
	m := flattenLine(raw)
	if m == "" {
		return "", ""
	}
	upper := strings.ToUpper(m)
	switch {
	case upper == "PLAN":
		return unicodeSymbols.iconPlan + " Plan", "accent"
	case upper == "PLAN (PAUSED)":
		return unicodeSymbols.iconPlan + " Plan " + unicodeSymbols.iconPause, "warning"
	case upper == "GOAL (PAUSED)":
		return unicodeSymbols.iconPause + " Goal", "warning"
	case upper == "GOAL" || strings.HasPrefix(upper, "GOAL "):
		rest := strings.TrimSpace(m[len("GOAL"):])
		if rest != "" {
			return unicodeSymbols.iconGoal + " Goal " + rest, "accent"
		}
		return unicodeSymbols.iconGoal + " Goal", "accent"
	default:
		return m, "accent"
	}
}

func (s *chromeState) startTurn() {
	s.mu.Lock()
	now := time.Now()
	s.data.Working = true
	s.data.WorkingMessage = defaultWorkingMessage
	s.data.WorkingStartedAt = now
	s.data.SpinnerGlyph = ompActivitySpinnerFrames[0]
	hadPinnedErr := s.data.PinnedError != ""
	s.data.PinnedError = ""
	s.lastIntent = ""
	s.turnStartedAt = now
	s.turnInput = 0
	s.turnOutput = 0
	s.turnCacheRead = 0
	s.turnCacheWrite = 0
	s.turnCost = 0
	s.turnHasUsage = false
	s.turnPremiumCounted = false
	s.lastTokPerSec = 0
	s.stopWorkingTickerLocked()
	c := s.sessionCtx
	hasSess := s.hasSession
	var stopCh chan struct{}
	if !s.closed && s.chromeLoaded && !surface.Native() {
		stopCh = make(chan struct{})
		s.workingTickStop = stopCh
	}
	s.mu.Unlock()
	if stopCh != nil {
		go s.runWorkingTicker(stopCh)
	}
	if hadPinnedErr && hasSess && surface.Native() {
		s.syncNativeErrorBanner(c, "")
	}
	s.requestFooterRepaint()
}

func (s *chromeState) endTurn(ctx sdk.Context) {
	s.mu.Lock()
	s.data.Working = false
	s.data.WorkingMessage = ""
	s.data.WorkingStartedAt = time.Time{}
	s.data.SpinnerGlyph = ""
	s.lastIntent = ""
	s.stopWorkingTickerLocked()

	var (
		shouldPublish bool
		rec           surface.TurnUsageRecord
	)
	if s.turnHasUsage {
		s.turnSeq++
		var elapsedMs int64
		if !s.turnStartedAt.IsZero() {
			elapsedMs = time.Since(s.turnStartedAt).Milliseconds()
			if elapsedMs < 0 {
				elapsedMs = 0
			}
		}
		rec = surface.TurnUsageRecord{
			TurnSeq:     s.turnSeq,
			Input:       s.turnInput,
			Output:      s.turnOutput,
			CacheRead:   s.turnCacheRead,
			CacheWrite:  s.turnCacheWrite,
			Cost:        s.turnCost,
			ElapsedMs:   elapsedMs,
			TimestampMs: time.Now().UnixMilli(),
		}
		shouldPublish = true
	}
	s.turnHasUsage = false
	s.mu.Unlock()

	if shouldPublish && isTopLevelSession(ctx) {
		surface.RecordTurnUsage(rec)
	}
	s.requestFooterRepaint()
}

// runWorkingTicker advances the active brand spinner glyph and elapsed timer
// in the above-editor `omp-status-band` widget while a turn is running.
func (s *chromeState) runWorkingTicker(stopCh <-chan struct{}) {
	select {
	case <-stopCh:
		return
	default:
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	frame := 0
	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			frame = (frame + 1) % len(ompActivitySpinnerFrames)
			s.mu.Lock()
			if s.closed || !s.data.Working || surface.Native() {
				s.mu.Unlock()
				return
			}
			s.data.SpinnerGlyph = ompActivitySpinnerFrames[frame]
			s.mu.Unlock()
			s.requestFooterRepaint()
		}
	}
}

// markBandOrderDirty flags `omp-status-band` to be re-anchored at the bottom
// of PiG's above-editor widget container (below any newly-mounted TODO HUD)
// on the next status-band repaint.
func (s *chromeState) markBandOrderDirty() {
	s.mu.Lock()
	s.bandOrderDirty = true
	s.mu.Unlock()
	s.requestFooterRepaint()
}

func (s *chromeState) setIntent(intent string) bool {
	s.mu.Lock()
	if intent == "" || intent == s.lastIntent {
		s.mu.Unlock()
		return false
	}
	s.lastIntent = intent
	s.data.WorkingMessage = intent
	s.mu.Unlock()
	s.requestFooterRepaint()
	return true
}
func (s *chromeState) installChrome(ctx sdk.Context) {
	if !ctx.HasUI() {
		return
	}
	_ = ctx.SetWorkingIndicator(sdk.WorkingIndicatorOptions{
		"frames":     ompActivitySpinnerFrames,
		"intervalMs": 80,
	})

	s.mu.Lock()
	s.sessionCtx = ctx
	s.hasSession = true
	s.data.Cwd = dirs.StandardizeMacOSPath(ctx.Cwd())
	s.mu.Unlock()
	s.refreshAll(ctx)
	if !surface.Native() {
		s.installTerminalHeaderFooter(ctx)
	}
}

func (s *chromeState) installTerminalHeaderFooter(ctx sdk.Context) {
	s.mu.Lock()
	if s.closed || s.chromeLoaded {
		s.mu.Unlock()
		return
	}
	s.chromeLoaded = true
	tipIdx := s.tipIndex
	ver := s.version
	var stopCh chan struct{}
	if s.data.Working && s.workingTickStop == nil && !surface.Native() {
		stopCh = make(chan struct{})
		s.workingTickStop = stopCh
	}
	s.mu.Unlock()

	_ = ctx.SetHeaderRenderer(func(width int) []string {
		return RenderWelcomeLines(ctx.UITheme(), ver, width, tipIdx)
	})
	// Suppress PiG's built-in working loader/border row so only omp's
	// `  ⎋ Working…` row + active `⠋ Ns` brand in `omp-status-band` render.
	ctx.SetWorkingVisible(false)
	// Draw omp's status band (with embedded context gauge, working row, and
	// pinned provider error banner) in an above-editor widget (`omp-status-band`)
	// directly above PiG's own stock editor (no Context.SetEditorComponent).
	// Install a zero-row footer renderer to suppress PiG's duplicate stock
	// footer while still observing host FooterData changes (git branch, `/plan`).
	_ = ctx.SetWidgetRenderer(statusBandWidgetKey, func(width int) []string {
		return RenderStatusBandWidget(ctx.UITheme(), s.snapshot(ctx), width)
	})
	_ = ctx.SetFooterRenderer(func(_ int) []string {
		s.onFooterDataChanged(ctx)
		return []string{}
	})
	if stopCh != nil {
		go s.runWorkingTicker(stopCh)
	}
}

// modeStatusFromFooter reads the active mode label published by internal/modes
// via ctx.SetStatus("omp:mode", ...), falling back to "mode" for callers that
// use the unprefixed key.
func modeStatusFromFooter(fd sdk.FooterData) string {
	statuses := fd.ExtensionStatuses()
	if len(statuses) == 0 {
		return ""
	}
	if m := strings.TrimSpace(statuses["omp:mode"]); m != "" {
		return m
	}
	return strings.TrimSpace(statuses["mode"])
}

// onFooterDataChanged compares the latest FooterData snapshot (git branch,
// mode status) against what the status band last rendered and nudges a
// band repaint when anything changed (e.g. `/plan` toggled via SetStatus).
func (s *chromeState) onFooterDataChanged(ctx sdk.Context) {
	fd := ctx.FooterData()
	br, _ := fd.GitBranch()
	mode := modeStatusFromFooter(fd)
	s.mu.Lock()
	changed := br != s.data.GitBranch || mode != s.data.ModeLabel
	if changed {
		s.data.GitBranch = br
		s.data.ModeLabel = mode
	}
	s.mu.Unlock()
	if changed {
		s.requestFooterRepaint()
	}
}

// requestFooterRepaint refreshes the above-editor `omp-status-band` widget
// without ever touching editor text (`SetEditorText`) or delegating the
// editor (`SetEditorComponent`).
//
// Ordering: `SetWidgetRenderer` updates the existing `omp-status-band` proxy
// in place; when `omp-todo` is mounted above the editor, clearing and
// re-setting `omp-status-band` only when `s.bandOrderDirty` is set (after a
// `todo` tool finishes) keeps `omp-status-band` directly above PiG's editor
// and below the TODO HUD without ever racing user keystrokes or paste.
func (s *chromeState) requestFooterRepaint() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.closed || !s.hasSession || !s.chromeLoaded || surface.Native() {
		s.mu.Unlock()
		return
	}
	s.footerRepaintQueued = true
	if s.footerRepaintRunning {
		s.mu.Unlock()
		return
	}
	s.footerRepaintRunning = true
	c := s.sessionCtx
	s.mu.Unlock()

	go func() {
		for {
			s.mu.Lock()
			if s.closed || !s.hasSession || !s.chromeLoaded || !s.footerRepaintQueued || surface.Native() {
				s.footerRepaintRunning = false
				s.footerRepaintQueued = false
				s.mu.Unlock()
				return
			}
			s.footerRepaintQueued = false
			c = s.sessionCtx
			reorder := s.bandOrderDirty
			s.bandOrderDirty = false
			s.mu.Unlock()

			if reorder {
				_ = c.SetWidgetRenderer(statusBandWidgetKey, nil)
			}
			_ = c.SetWidgetRenderer(statusBandWidgetKey, func(width int) []string {
				return RenderStatusBandWidget(c.UITheme(), s.snapshot(c), width)
			})
		}
	}()
}

// snapshot returns the cached StatusLineData merged with cheap in-process
// FooterData reads (no host IPC calls per refresh).
func (s *chromeState) snapshot(ctx sdk.Context) StatusLineData {
	s.mu.Lock()
	d := s.data
	s.mu.Unlock()
	compact := settingBool(ctx, "statusLine.compactThinkingLevel", true)
	d.CompactThinkingLevel = &compact
	d.ContextLineMode = settingString(ctx, "statusLine.contextLine", "embedded")
	d.AutoCompactDisabled = !settingBool(ctx, "compaction.enabled", true)
	fd := ctx.FooterData()
	if br, ok := fd.GitBranch(); ok {
		d.GitBranch = br
	}
	if m := modeStatusFromFooter(fd); m != "" {
		d.ModeLabel = m
	}
	return d
}

// refreshAll runs once at session_start to seed model, thinking, context,
// session name, model catalog metadata, and initial branch cost before
// installing the chrome.
func (s *chromeState) refreshAll(ctx sdk.Context) {
	s.updateModel(ctx)
	s.publishModelCatalog(ctx)
	s.installThinkingApplier(ctx)
	s.updateThinking(ctx)
	s.updateContext(ctx)
	s.updateSessionInfo(ctx)
	s.seedBranchCost(ctx)
	s.publishComposerMeta(ctx)
}

// installThinkingApplier registers the active session's thinking-level setter
// so Tern's model picker slider can apply thinking effort immediately.
func (s *chromeState) installThinkingApplier(ctx sdk.Context) {
	surface.SetThinkingApplier(func(level string) {
		if level == "" {
			return
		}
		s.mu.Lock()
		closed := s.closed
		c := s.sessionCtx
		s.mu.Unlock()
		if closed {
			return
		}
		go func() {
			c.SetThinkingLevel(level)
		}()
	})
}

// publishModelCatalog snapshots ModelRegistry.GetAll() into surface.SetModels
// so the Tern frontend's model picker can display Ctx, $/M, free/ctx badges,
// and each model's supported thinking slider steps.
func (s *chromeState) publishModelCatalog(ctx sdk.Context) {
	models, err := ctx.ModelRegistry().GetAll()
	if err != nil || len(models) == 0 {
		return
	}
	out := make([]surface.ModelMeta, 0, len(models))
	for _, raw := range models {
		id := strField(raw, "id")
		if id == "" {
			continue
		}
		provider := strField(raw, "provider")
		name := strField(raw, "name")
		if name == "" {
			name = id
		}
		shown := strings.TrimPrefix(name, "Claude ")
		reasoning, _ := raw["reasoning"].(bool)
		ctxWin, _ := intField(raw, "contextWindow")
		var inCost, outCost, cacheRead, cacheWrite float64
		if costMap, ok := raw["cost"].(map[string]any); ok {
			inCost, _ = floatField(costMap, "input")
			outCost, _ = floatField(costMap, "output")
			cacheRead, _ = floatField(costMap, "cacheRead")
			cacheWrite, _ = floatField(costMap, "cacheWrite")
		}
		var levels []string
		if reasoning {
			levels = sdk.GetSupportedThinkingLevels(raw)
		}
		out = append(out, surface.ModelMeta{
			ID:                  id,
			Provider:            provider,
			Name:                shown,
			ContextWindow:       ctxWin,
			InputCostPer1M:      inCost,
			OutputCostPer1M:     outCost,
			CacheReadCostPer1M:  cacheRead,
			CacheWriteCostPer1M: cacheWrite,
			Reasoning:           reasoning,
			ThinkingLevels:      levels,
		})
	}
	if len(out) > 0 {
		surface.SetModels(out)
	}
}

func (s *chromeState) updateModel(ctx sdk.Context) bool {
	model, err := ctx.GetModelInfo()
	if err != nil || model == nil {
		return false
	}
	name := model.Name
	if name == "" {
		name = model.ID
	}
	shown := strings.TrimPrefix(name, "Claude ")
	// The Tern composer gets only the model id from the host; the frontend
	// member looks the shown name up here.
	surface.SetModelDisplayName(model.ID, shown)
	var levels []string
	var rawModel map[string]any
	if raw, rerr := ctx.ModelRegistry().Find(model.Provider, model.ID); rerr == nil && raw != nil {
		rawModel = raw
		if model.Reasoning {
			levels = sdk.GetSupportedThinkingLevels(raw)
		}
	}
	hasVision := modelSendsImageInputOnWire(rawModel)
	var premMult float64
	if rawModel != nil {
		if m, ok := floatField(rawModel, "premiumRequestMultiplier"); ok && m > 0 {
			premMult = m
		}
	}
	surface.UpsertModel(surface.ModelMeta{
		ID:                  model.ID,
		Provider:            model.Provider,
		Name:                shown,
		ContextWindow:       model.ContextWindow,
		InputCostPer1M:      model.InputCostPer1M,
		OutputCostPer1M:     model.OutputCostPer1M,
		CacheReadCostPer1M:  model.CacheReadCostPer1M,
		CacheWriteCostPer1M: model.CacheWriteCostPer1M,
		Reasoning:           model.Reasoning,
		ThinkingLevels:      levels,
	})
	s.mu.Lock()
	s.data.ModelName = shown
	s.data.ModelReasoning = model.Reasoning
	s.inCostPer1M = model.InputCostPer1M
	s.outCostPer1M = model.OutputCostPer1M
	s.cacheReadPer1M = model.CacheReadCostPer1M
	s.cacheWritePer1M = model.CacheWriteCostPer1M
	s.modelProvider = model.Provider
	s.modelID = model.ID
	s.modelWindow = model.ContextWindow
	s.modelHasVision = hasVision
	s.modelPremiumMult = premMult
	s.mu.Unlock()
	return true
}

func (s *chromeState) refreshModel(ctx sdk.Context) {
	if pending := surface.ConsumePendingThinkingLevel(); pending != "" {
		ctx.SetThinkingLevel(pending)
	}
	if s.updateModel(ctx) {
		s.requestFooterRepaint()
	}
	s.publishModelCatalog(ctx)
	s.publishComposerMeta(ctx)
}

func (s *chromeState) updateThinking(ctx sdk.Context) bool {
	lvl, err := ctx.GetThinkingLevel()
	if err != nil {
		return false
	}
	s.mu.Lock()
	s.data.ThinkingLevel = lvl
	s.mu.Unlock()
	return true
}

func (s *chromeState) refreshThinking(ctx sdk.Context) {
	if s.updateThinking(ctx) {
		s.requestFooterRepaint()
	}
}

func (s *chromeState) updateContext(ctx sdk.Context) bool {
	var zero sdk.Context
	if ctx == zero {
		return false
	}
	usage, err := ctx.GetContextUsage()
	if err != nil || usage == nil {
		return false
	}
	s.mu.Lock()
	s.data.ContextTokens = usage.Tokens
	s.data.ContextWindow = usage.ContextWindow
	s.data.ContextPercent = usage.Percent
	s.mu.Unlock()
	s.publishComposerMeta(ctx)
	return true
}

func (s *chromeState) updateSessionInfo(ctx sdk.Context) bool {
	name, err := ctx.GetSessionName()
	if err != nil {
		return false
	}
	s.mu.Lock()
	if name == nil {
		s.data.SessionName = ""
	} else {
		s.data.SessionName = *name
	}
	s.mu.Unlock()
	return true
}

func (s *chromeState) refreshSessionInfo(ctx sdk.Context) {
	if s.updateSessionInfo(ctx) {
		s.requestFooterRepaint()
	}
}

func (s *chromeState) seedBranchCost(ctx sdk.Context) {
	branch, err := ctx.GetBranch()
	if err != nil {
		return
	}
	s.mu.Lock()
	inP, outP, crP, cwP := s.inCostPer1M, s.outCostPer1M, s.cacheReadPer1M, s.cacheWritePer1M
	s.mu.Unlock()

	var totalCost float64
	if inP > 0 || outP > 0 || crP > 0 || cwP > 0 {
		for _, entry := range branch {
			if entry.Usage != nil {
				totalCost += (float64(entry.Usage.Input)*inP +
					float64(entry.Usage.Output)*outP +
					float64(entry.Usage.CacheRead)*crP +
					float64(entry.Usage.CacheWrite)*cwP) / 1_000_000.0
			}
		}
	}
	s.mu.Lock()
	s.data.EstimatedCost = totalCost
	s.countedUsageEntry = len(branch)
	s.mu.Unlock()
}

// onTurnEnd updates context usage and accumulates this turn's usage cost
// incrementally from the event payload (avoiding O(session) GetBranch IPC),
// then repaints the status line.
func (s *chromeState) onTurnEnd(ctx sdk.Context, data map[string]any) {
	s.updateContext(ctx)
	if msg := asMap(data["message"]); msg != nil {
		if usage := asMap(msg["usage"]); usage != nil {
			inTok, _ := floatField(usage, "input")
			outTok, _ := floatField(usage, "output")
			crTok, _ := floatField(usage, "cacheRead")
			cwTok, _ := floatField(usage, "cacheWrite")
			premReq, _ := floatField(usage, "premiumRequests")
			var explicitCost float64
			if costMap := asMap(usage["cost"]); costMap != nil {
				explicitCost, _ = floatField(costMap, "total")
			}

			s.mu.Lock()
			var stepCost float64
			if explicitCost > 0 {
				stepCost = explicitCost
			} else if s.inCostPer1M > 0 || s.outCostPer1M > 0 || s.cacheReadPer1M > 0 || s.cacheWritePer1M > 0 {
				stepCost = (inTok*s.inCostPer1M +
					outTok*s.outCostPer1M +
					crTok*s.cacheReadPer1M +
					cwTok*s.cacheWritePer1M) / 1_000_000.0
			}
			s.data.EstimatedCost += stepCost
			if inTok > 0 || outTok > 0 || crTok > 0 || cwTok > 0 || stepCost > 0 {
				s.turnInput += int(math.Round(inTok))
				s.turnOutput += int(math.Round(outTok))
				s.turnCacheRead += int(math.Round(crTok))
				s.turnCacheWrite += int(math.Round(cwTok))
				s.turnCost += stepCost
				s.turnHasUsage = true
			}
			if premReq > 0 {
				s.premiumRequests += premReq
				s.turnPremiumCounted = true
			} else if !s.turnPremiumCounted && s.modelPremiumMult > 0 && (inTok > 0 || outTok > 0) {
				s.premiumRequests += s.modelPremiumMult
				s.turnPremiumCounted = true
			}
			if s.turnOutput > 0 && !s.turnStartedAt.IsZero() {
				elapsedMs := time.Since(s.turnStartedAt).Milliseconds()
				if elapsedMs >= 100 {
					s.lastTokPerSec = float64(s.turnOutput) / (float64(elapsedMs) / 1000.0)
				}
			}
			s.mu.Unlock()
		}
		// Pin omp's error banner above the band (terminal) or as a structured
		// `omp.errorBanner` dock widget (native Tern) when a turn ends with a
		// provider error (upstream: interactive-mode.ts:9128-9139,
		// overlays/error-banner.ts:26-93). Cleared on next agent_start.
		if strings.EqualFold(strField(msg, "stopReason"), "error") {
			errMsg := strings.TrimSpace(strField(msg, "errorMessage"))
			if errMsg == "" {
				errMsg = "Request failed"
			}
			s.mu.Lock()
			s.data.PinnedError = errMsg
			s.mu.Unlock()
			if surface.Native() {
				s.syncNativeErrorBanner(ctx, errMsg)
			}
		}
	}
	s.publishComposerMeta(ctx)
	s.requestFooterRepaint()
}
func (s *chromeState) refreshFooter(ctx sdk.Context) {
	s.updateContext(ctx)
	s.requestFooterRepaint()
}

// ═══════════════════════════════════════════════════════════════════════════
// Compaction boundaries & live composer metadata
// upstream:
//   packages/agent/src/compaction/compaction.ts:341-437
//   packages/coding-agent/src/session/context-usage-runtime.ts:20-37
//   packages/coding-agent/src/session/speculation-lead.ts:13-24
//   packages/coding-agent/src/session/compaction-methods.ts:45-51,119-136
// ═══════════════════════════════════════════════════════════════════════════

const (
	defaultCompactionReserveTokens = 16384
	speculationLeadFraction        = 0.125
	speculationLeadMinTokens       = 8192
	speculationLeadMaxTokens       = 32000
)

var defaultCompactionMethodOrder = []string{
	"remote",
	"snapcompact",
	"handoff",
	"shake",
	"soft",
}

type compactionPolicy struct {
	Enabled          bool
	Strategy         string
	ThresholdTokens  int
	ThresholdPercent float64
	ReserveTokens    int
	HasReserveTokens bool
	BaseWindowTokens int
	AsyncEnabled     bool
	MethodOrder      []string
	RemoteEndpoint   string
}

// modelSendsImageInputOnWire mirrors sendsImageInputOnWire in
// packages/ai/src/providers/vision-guard.ts:58-62.
func modelSendsImageInputOnWire(raw map[string]any) bool {
	if raw == nil {
		return false
	}
	hasImage := false
	switch in := raw["input"].(type) {
	case []string:
		for _, m := range in {
			if m == "image" {
				hasImage = true
				break
			}
		}
	case []any:
		for _, el := range in {
			if s, ok := el.(string); ok && s == "image" {
				hasImage = true
				break
			}
		}
	}
	if !hasImage {
		return false
	}
	if transport, _ := raw["transport"].(string); transport == "pi-native" {
		return true
	}
	if compat, ok := raw["compat"].(map[string]any); ok {
		if strip, _ := compat["stripImageInput"].(bool); strip {
			return false
		}
	}
	return true
}

func readCompactionPolicy(ctx sdk.Context) compactionPolicy {
	p := compactionPolicy{
		Enabled:          settingBool(ctx, "compaction.enabled", true),
		Strategy:         settingString(ctx, "compaction.strategy", ""),
		ThresholdTokens:  -1,
		ThresholdPercent: -1,
		AsyncEnabled:     settingBool(ctx, "compaction.asyncEnabled", true),
		RemoteEndpoint:   settingString(ctx, "compaction.remoteEndpoint", ""),
	}
	if v, ok := settingFloat(ctx, "compaction.thresholdTokens", -1); ok {
		p.ThresholdTokens = int(math.Floor(v))
	}
	if v, ok := settingFloat(ctx, "compaction.thresholdPercent", -1); ok {
		p.ThresholdPercent = v
	}
	if v, ok := settingFloat(ctx, "compaction.reserveTokens", defaultCompactionReserveTokens); ok {
		p.ReserveTokens = int(math.Floor(v))
		p.HasReserveTokens = true
	}
	if order, ok := settingStringSlice(ctx, "compaction.methodOrder"); ok && len(order) > 0 {
		p.MethodOrder = order
	} else {
		p.MethodOrder = defaultCompactionMethodOrder
	}
	return p
}

// effectiveReserveTokens mirrors compaction.ts:341-343.
func effectiveReserveTokens(contextWindow int, p compactionPolicy) int {
	reserve := defaultCompactionReserveTokens
	if p.HasReserveTokens {
		reserve = p.ReserveTokens
	}
	prop := int(math.Floor(float64(contextWindow) * 0.15))
	if prop > reserve {
		return prop
	}
	return reserve
}

// resolveBudgetReserveTokens mirrors compaction.ts:357-366.
func resolveBudgetReserveTokens(contextWindow int, p compactionPolicy) int {
	reserveTokens := effectiveReserveTokens(contextWindow, p)
	proportionalReserveTokens := max(1, int(math.Floor(float64(contextWindow)*0.15)))
	reserveWasDefaulted := !p.HasReserveTokens
	defaultReserveIsEffectivelyImpossible := reserveWasDefaulted && reserveTokens >= contextWindow-proportionalReserveTokens
	reserveExceedsWindow := reserveTokens >= contextWindow
	if defaultReserveIsEffectivelyImpossible || reserveExceedsWindow {
		return proportionalReserveTokens
	}
	return reserveTokens
}

// promptBudgetTokens mirrors compaction.ts:403-405.
func promptBudgetTokens(contextWindow int, p compactionPolicy) int {
	return max(0, min(contextWindow-1, contextWindow-resolveBudgetReserveTokens(contextWindow, p)))
}

// resolveThresholdTokens mirrors compaction.ts:407-437.
func resolveThresholdTokens(contextWindow int, p compactionPolicy) int {
	if p.ThresholdTokens > 0 {
		if p.ThresholdTokens >= contextWindow {
			return promptBudgetTokens(contextWindow, p)
		}
		return min(contextWindow-1, max(1, p.ThresholdTokens))
	}
	if p.BaseWindowTokens > 0 && p.BaseWindowTokens < contextWindow {
		contextWindow = p.BaseWindowTokens
	}
	if math.IsNaN(p.ThresholdPercent) || math.IsInf(p.ThresholdPercent, 0) || p.ThresholdPercent <= 0 {
		return promptBudgetTokens(contextWindow, p)
	}
	clamped := min(99.0, max(1.0, p.ThresholdPercent))
	return int(math.Floor(float64(contextWindow) * (clamped / 100.0)))
}

// resolveSpeculationLeadTokens mirrors speculation-lead.ts:19-24.
func resolveSpeculationLeadTokens(thresholdTokens int) int {
	scaled := int(math.Floor(float64(thresholdTokens) * speculationLeadFraction))
	return min(speculationLeadMaxTokens, max(speculationLeadMinTokens, scaled))
}

// resolveSpeculationMethod mirrors compaction-methods.ts:119-136.
func resolveSpeculationMethod(hasVision bool, p compactionPolicy) string {
	seen := make(map[string]bool, len(p.MethodOrder))
	for _, candidate := range p.MethodOrder {
		switch candidate {
		case "remote", "snapcompact", "handoff", "shake", "soft":
		default:
			continue
		}
		if seen[candidate] {
			continue
		}
		seen[candidate] = true

		var available bool
		switch candidate {
		case "remote":
			available = strings.TrimSpace(p.RemoteEndpoint) != ""
		case "snapcompact":
			available = hasVision
		default:
			available = true
		}
		if !available {
			continue
		}
		if candidate == "remote" || candidate == "handoff" || candidate == "soft" {
			return candidate
		}
		return ""
	}
	return ""
}

// computeCompactionBoundaries mirrors getSessionCompactionBoundaries +
// computeCompactionBoundaries (context-usage-runtime.ts:20-37, context-usage.ts:99-115).
func computeCompactionBoundaries(p compactionPolicy, contextWindow int, hasVision bool) (hasBoundaries bool, thresholdPct float64, specPct float64) {
	if contextWindow <= 0 || !p.Enabled || p.Strategy == "off" {
		return false, 0, 0
	}
	threshold := resolveThresholdTokens(contextWindow, p)
	if threshold <= 0 || threshold > contextWindow {
		return false, 0, 0
	}
	thresholdPct = (float64(threshold) / float64(contextWindow)) * 100.0
	if p.AsyncEnabled && resolveSpeculationMethod(hasVision, p) != "" {
		lead := resolveSpeculationLeadTokens(threshold)
		specPct = (float64(max(0, threshold-lead)) / float64(contextWindow)) * 100.0
	}
	return true, thresholdPct, specPct
}

func (s *chromeState) publishComposerMeta(ctx sdk.Context) {
	if !isTopLevelSession(ctx) {
		return
	}
	s.mu.Lock()
	cw := s.data.ContextWindow
	if cw <= 0 {
		cw = s.modelWindow
	}
	hasVision := s.modelHasVision
	prem := s.premiumRequests
	tokPerSec := s.lastTokPerSec
	s.mu.Unlock()

	p := readCompactionPolicy(ctx)
	hasBounds, threshPct, specPct := computeCompactionBoundaries(p, cw, hasVision)
	specState := ""
	if hasBounds && specPct > 0 {
		specState = "idle"
	}
	surface.UpdateComposerMeta(func(m *surface.ComposerMeta) {
		m.HasBoundaries = hasBounds
		m.AutoCompactEnabled = p.Enabled && p.Strategy != "off"
		m.ThresholdPercent = threshPct
		m.SpeculationPercent = specPct
		m.SpeculationState = specState
		m.PremiumRequests = prem
		m.TokensPerSec = tokPerSec
	})
}
