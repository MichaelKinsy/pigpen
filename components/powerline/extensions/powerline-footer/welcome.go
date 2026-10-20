package powerline_footer

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

// The welcome screen: the layout of the header and the startup overlay, the counts of what is loaded and the recent sessions.
// upstream: welcome.ts. The lifecycle (when it is shown and dismissed) is welcome_ext.go.

// recentSession is one line of "Recent sessions".
type recentSession struct {
	Name    string
	TimeAgo string
}

// loadedCounts is what the "Loaded" block counts.
type loadedCounts struct {
	ContextFiles    int
	Skills          int
	PromptTemplates int
}

// slashCommand is the part of Pi's SlashCommandInfo the counts read.
type slashCommand struct {
	Name   string
	Source string // "extension", "prompt" or "skill"
}

// welcomeLogo and welcomeGradient: welcome.ts PI_LOGO and GRADIENT_COLORS.
var welcomeLogo = []string{
	"██████████    ",
	"████  ████    ",
	"████  ████    ",
	"████████  ████",
	"████      ████",
	"████      ████",
}

var welcomeGradient = []string{
	"\x1b[38;5;199m",
	"\x1b[38;5;171m",
	"\x1b[38;5;135m",
	"\x1b[38;5;99m",
	"\x1b[38;5;75m",
	"\x1b[38;5;51m",
}

// sessionHeaderReadBytes is how much of a session file is read to find its project (welcome.ts SESSION_HEADER_READ_BYTES).
const sessionHeaderReadBytes = 8192

// welcomeColors is the chrome palette of colors.ts THEME: the escape code a name resolves to.
var welcomeColors = map[string]string{
	"sep":      "\x1b[38;5;244m",
	"model":    "\x1b[38;2;215;135;175m", // #d787af
	"path":     "\x1b[38;2;0;175;175m",   // #00afaf
	"gitClean": "\x1b[38;2;95;175;95m",   // #5faf5f
	"accent":   "\x1b[38;2;254;188;56m",  // #febc38
}

// welcomeFgOnly is colors.ts fgOnly: the color code and the text, with no reset (the caller manages it).
func welcomeFgOnly(color, text string) string { return welcomeColors[color] + text }

func welcomeBold(text string) string { return "\x1b[1m" + text + "\x1b[22m" }

func welcomeDim(text string) string { return welcomeColors["sep"] + text + ansiReset }

// gradientLine colors each non-space character of a logo row with the gradient step it falls in (welcome.ts gradientLine). The
// logo is plain BMP text, so a rune is one UTF-16 code unit as in the original.
func gradientLine(line string) string {
	chars := []rune(line)
	var b strings.Builder
	colorIdx := 0
	step := max(1, len(chars)/len(welcomeGradient))
	for i, ch := range chars {
		if i > 0 && i%step == 0 && colorIdx < len(welcomeGradient)-1 {
			colorIdx++
		}
		if ch != ' ' {
			b.WriteString(welcomeGradient[colorIdx])
			b.WriteRune(ch)
			b.WriteString(ansiReset)
		} else {
			b.WriteRune(ch)
		}
	}
	return b.String()
}

// centerText centers text in width cells, or truncates it with an ellipsis when it is wider (welcome.ts centerText).
func centerText(text string, width int) string {
	visLen := visibleWidth(text)
	if visLen > width {
		return truncateToWidth(text, width, "…")
	}
	if visLen == width {
		return text
	}
	leftPad := (width - visLen) / 2
	rightPad := width - visLen - leftPad
	return strings.Repeat(" ", leftPad) + text + strings.Repeat(" ", rightPad)
}

// fitToWidth pads text to width cells, or truncates it with an ellipsis (welcome.ts fitToWidth).
func fitToWidth(str string, width int) string {
	visLen := visibleWidth(str)
	if visLen > width {
		return truncateToWidth(str, width, "…")
	}
	return str + strings.Repeat(" ", width-visLen)
}

// welcomeData is what the box shows. InitialContextTokens is nil for "no estimate".
type welcomeData struct {
	ModelName            string
	ProviderName         string
	RecentSessions       []recentSession
	LoadedCounts         loadedCounts
	InitialContextTokens *float64
}

func buildLeftColumn(data welcomeData, colWidth int) []string {
	lines := []string{"", centerText(welcomeBold("Welcome back!"), colWidth), ""}
	for _, l := range welcomeLogo {
		lines = append(lines, centerText(gradientLine(l), colWidth))
	}
	return append(lines, "",
		centerText(welcomeFgOnly("model", data.ModelName), colWidth),
		centerText(welcomeDim(data.ProviderName), colWidth))
}

func welcomePlural(n int) string {
	if n != 1 {
		return "s"
	}
	return ""
}

func buildRightColumn(data welcomeData, colWidth int) []string {
	const hChar = "─"
	separator := " " + welcomeDim(strings.Repeat(hChar, max(0, colWidth-2)))

	var sessionLines []string
	if len(data.RecentSessions) == 0 {
		sessionLines = append(sessionLines, " "+welcomeDim("No recent sessions"))
	} else {
		for _, s := range data.RecentSessions[:min(3, len(data.RecentSessions))] {
			sessionLines = append(sessionLines, " "+welcomeDim("• ")+welcomeFgOnly("path", s.Name)+welcomeDim(" ("+s.TimeAgo+")"))
		}
	}

	var countLines []string
	c := data.LoadedCounts
	itemPrefix := welcomeDim("- ")
	if c.ContextFiles > 0 || c.Skills > 0 || c.PromptTemplates > 0 {
		if c.ContextFiles > 0 {
			countLines = append(countLines, " "+itemPrefix+welcomeFgOnly("gitClean", strconv.Itoa(c.ContextFiles))+" context file"+welcomePlural(c.ContextFiles))
		}
		if c.Skills > 0 {
			countLines = append(countLines, " "+itemPrefix+welcomeFgOnly("gitClean", strconv.Itoa(c.Skills))+" skill"+welcomePlural(c.Skills))
		}
		if c.PromptTemplates > 0 {
			countLines = append(countLines, " "+itemPrefix+welcomeFgOnly("gitClean", strconv.Itoa(c.PromptTemplates))+" prompt template"+welcomePlural(c.PromptTemplates))
		}
	} else {
		countLines = append(countLines, " "+welcomeDim("No context files, skills, or prompts loaded"))
	}

	if t := data.InitialContextTokens; t != nil && finite(*t) && *t > 0 {
		countLines = append(countLines, " "+itemPrefix+welcomeFgOnly("gitClean", "≈ "+formatTokens(*t))+" initial prompt tokens")
	}

	lines := []string{
		" " + welcomeBold(welcomeFgOnly("accent", "Tips")),
		" " + welcomeDim("/") + " for commands",
		" " + welcomeDim("!") + " to run bash",
		" " + welcomeDim("Shift+Tab") + " cycle thinking",
		separator,
		" " + welcomeBold(welcomeFgOnly("accent", "Loaded")),
	}
	lines = append(lines, countLines...)
	lines = append(lines, separator, " "+welcomeBold(welcomeFgOnly("accent", "Recent sessions")))
	lines = append(lines, sessionLines...)
	return append(lines, "")
}

const (
	welcomeMinLayoutWidth = 44 // leftCol(26) + separator(3) + minRightCol(15)
	welcomeMinWidth       = 76
	welcomeMaxWidth       = 96
	welcomeLeftCol        = 26
)

// welcomeBoxWidth clamps the box to the terminal (welcome.ts renderWelcomeBox and both components).
func welcomeBoxWidth(termWidth int) int {
	return min(termWidth, max(welcomeMinWidth, min(termWidth-2, welcomeMaxWidth)))
}

func renderWelcomeBox(data welcomeData, termWidth int, bottomLine string) []string {
	// Too narrow for the two columns: no box at all.
	if termWidth < welcomeMinLayoutWidth {
		return []string{}
	}
	boxWidth := welcomeBoxWidth(termWidth)
	leftCol := welcomeLeftCol
	rightCol := max(1, boxWidth-leftCol-3)

	const hChar = "─"
	v := welcomeDim("│")
	tl, tr, bl, br := welcomeDim("╭"), welcomeDim("╮"), welcomeDim("╰"), welcomeDim("╯")

	leftLines := buildLeftColumn(data, leftCol)
	rightLines := buildRightColumn(data, rightCol)

	var lines []string
	const title = " pi agent "
	titleStyled := welcomeDim(strings.Repeat(hChar, 3)) + welcomeFgOnly("model", title)
	titleVisLen := 3 + visibleWidth(title)
	afterTitle := boxWidth - 2 - titleVisLen
	afterTitleText := ""
	if afterTitle > 0 {
		afterTitleText = welcomeDim(strings.Repeat(hChar, afterTitle))
	}
	lines = append(lines, tl+titleStyled+afterTitleText+tr)

	maxRows := max(len(leftLines), len(rightLines))
	for i := range maxRows {
		var left, right string
		if i < len(leftLines) {
			left = leftLines[i]
		}
		if i < len(rightLines) {
			right = rightLines[i]
		}
		lines = append(lines, v+fitToWidth(left, leftCol)+v+fitToWidth(right, rightCol)+v)
	}
	return append(lines, bl+bottomLine+br)
}

// welcomeComponent is the startup overlay: the box with a countdown in its bottom border. upstream: WelcomeComponent.
type welcomeComponent struct {
	data      welcomeData
	countdown int
}

func newWelcomeComponent(modelName, providerName string, recentSessions []recentSession, counts loadedCounts, initialContextTokens *float64) *welcomeComponent {
	return &welcomeComponent{
		data:      welcomeData{modelName, providerName, recentSessions, counts, initialContextTokens},
		countdown: 30,
	}
}

func (w *welcomeComponent) setCountdown(seconds int) { w.countdown = seconds }

func (w *welcomeComponent) render(termWidth int) []string {
	if termWidth < welcomeMinLayoutWidth {
		return []string{}
	}
	boxWidth := welcomeBoxWidth(termWidth)
	countdownText := " Press any key to continue (" + strconv.Itoa(w.countdown) + "s) "
	bottomContentWidth := boxWidth - 2
	countdownVisLen := visibleWidth(countdownText)
	leftPad := floorDiv(bottomContentWidth-countdownVisLen, 2)
	rightPad := bottomContentWidth - countdownVisLen - leftPad
	const hChar = "─"
	bottomLine := welcomeDim(strings.Repeat(hChar, max(0, leftPad))) + welcomeDim(countdownText) + welcomeDim(strings.Repeat(hChar, max(0, rightPad)))
	return renderWelcomeBox(w.data, termWidth, bottomLine)
}

// floorDiv is Math.floor(a / b) for b > 0.
func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && a < 0 {
		q--
	}
	return q
}

// welcomeHeader is the same box, persistent and with no countdown (quietStartup). upstream: WelcomeHeader.
type welcomeHeader struct{ data welcomeData }

func newWelcomeHeader(modelName, providerName string, recentSessions []recentSession, counts loadedCounts, initialContextTokens *float64) *welcomeHeader {
	return &welcomeHeader{welcomeData{modelName, providerName, recentSessions, counts, initialContextTokens}}
}

func (h *welcomeHeader) render(termWidth int) []string {
	if termWidth < welcomeMinLayoutWidth {
		return []string{}
	}
	boxWidth := welcomeBoxWidth(termWidth)
	const hChar = "─"
	rightCol := max(1, boxWidth-welcomeLeftCol-3)
	bottomLine := welcomeDim(strings.Repeat(hChar, welcomeLeftCol)) + welcomeDim("┴") + welcomeDim(strings.Repeat(hChar, rightCol))
	lines := renderWelcomeBox(h.data, termWidth, bottomLine)
	if len(lines) > 0 {
		lines = append(lines, "") // spacing, only when there is a box
	}
	return lines
}

// Discovery.

// countLoadedCommands counts Pi's effective prompt and skill commands after resource resolution (welcome.ts countLoadedCommands).
func countLoadedCommands(commands []slashCommand) (skills, promptTemplates int) {
	for _, c := range commands {
		if c.Source == "skill" {
			skills++
		}
		if c.Source == "prompt" {
			promptTemplates++
		}
	}
	return skills, promptTemplates
}

// discoverLoadedCounts finds the context files and takes the rest from Pi's resource snapshot (welcome.ts discoverLoadedCounts).
// The project directory is the process's working directory, as process.cwd() in the original.
func discoverLoadedCounts(commands []slashCommand) loadedCounts {
	homeDir := getHomeDir()
	cwd, _ := os.Getwd()
	paths := []string{
		getAgentPath("AGENTS.md"),
		filepath.Join(homeDir, ".claude", "AGENTS.md"),
		filepath.Join(cwd, "AGENTS.md"),
		filepath.Join(cwd, ".pi", "AGENTS.md"),
		filepath.Join(cwd, ".claude", "AGENTS.md"),
	}
	contextFiles := 0
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			contextFiles++
		}
	}
	skills, prompts := countLoadedCommands(commands)
	return loadedCounts{contextFiles, skills, prompts}
}

// welcomeFSHook, when set, runs before each filesystem operation of getRecentSessions (op: "realpath", "opendir", "stat", "open",
// "read"). The tests use it to hold discovery at a known point.
var welcomeFSHook func(op, path string)

func welcomeFS(op, path string) {
	if welcomeFSHook != nil {
		welcomeFSHook(op, path)
	}
}

// nodeBasename is Node's path.basename for the platform: trailing separators are ignored and "/" has the empty name. On Windows
// both separators and a drive prefix count.
func nodeBasename(p string) string {
	if runtime.GOOS == "windows" {
		if len(p) >= 2 && p[1] == ':' && (p[0]|0x20 >= 'a' && p[0]|0x20 <= 'z') {
			p = p[2:]
		}
		p = strings.TrimRight(p, `\/`)
		if i := strings.LastIndexAny(p, `\/`); i >= 0 {
			return p[i+1:]
		}
		return p
	}
	p = strings.TrimRight(p, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// readSessionHeaderProjectName is the basename of the cwd in the first line of a session file; ok is false when the header is
// unusable. A non-nil error is the abort of ctx.
func readSessionHeaderProjectName(ctx context.Context, filePath string) (name string, ok bool, err error) {
	abort := func() error { return context.Cause(ctx) }
	if ctx.Err() != nil {
		return "", false, abort()
	}
	welcomeFS("open", filePath)
	f, openErr := os.Open(filePath)
	if openErr != nil {
		if ctx.Err() != nil {
			return "", false, abort()
		}
		return "", false, nil
	}
	defer f.Close()
	if ctx.Err() != nil {
		return "", false, abort()
	}
	welcomeFS("read", filePath)
	buf := make([]byte, sessionHeaderReadBytes)
	n, _ := f.ReadAt(buf, 0)
	if ctx.Err() != nil {
		return "", false, abort()
	}
	text := strings.ToValidUTF8(string(buf[:n]), "\uFFFD")
	firstLine, _, _ := strings.Cut(text, "\n")
	firstLine = strings.TrimSuffix(firstLine, "\r")
	firstLine = jsTrim(firstLine)
	if firstLine == "" {
		return "", false, nil
	}
	header, parseErr := parseJSON([]byte(firstLine))
	obj, isObj := header.(*jsObject)
	if parseErr != nil || !isObj {
		return "", false, nil
	}
	cwd, isString := obj.vals["cwd"].(string)
	if !isString || jsTrim(cwd) == "" {
		return "", false, nil
	}
	if base := nodeBasename(cwd); base != "" {
		return base, true, nil
	}
	return cwd, true, nil
}

// sessionProjectNameFromDirectory names the project from its encoded sessions directory ("--Users-nico-dev-app--" is "app").
func sessionProjectNameFromDirectory(dir string) string {
	parentName := nodeBasename(dir)
	if !strings.HasPrefix(parentName, "--") {
		return parentName
	}
	var parts []string
	for _, p := range strings.Split(parentName, "-") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return parentName
}

type welcomeSessionFile struct {
	filePath string
	dir      string
	mtime    float64 // milliseconds, as Node's mtimeMs
}

type pendingDir struct {
	dir       string
	ancestors []string
}

// getRecentSessions collects the session files' metadata, then reads bounded headers newest first. I/O is serial. An abort of ctx
// returns its cause after the current operation, with no partial result. upstream: welcome.ts getRecentSessions.
func getRecentSessions(ctx context.Context, maxCount int) ([]recentSession, error) {
	abort := func() ([]recentSession, error) { return nil, context.Cause(ctx) }
	if ctx.Err() != nil {
		return abort()
	}
	if maxCount == 0 {
		return []recentSession{}, nil
	}
	dirs := []string{getAgentPath("sessions")}
	if legacy := getLegacyPiPath("sessions"); legacy != dirs[0] {
		dirs = append(dirs, legacy)
	}
	var pending []pendingDir
	for i := len(dirs) - 1; i >= 0; i-- { // .reverse(): the stack pops the agent directory first
		pending = append(pending, pendingDir{dir: dirs[i]})
	}
	var sessions []welcomeSessionFile

	for len(pending) > 0 {
		if ctx.Err() != nil {
			return abort()
		}
		cur := pending[len(pending)-1]
		pending = pending[:len(pending)-1]

		welcomeFS("realpath", cur.dir)
		canonicalDir, err := filepath.EvalSymlinks(cur.dir)
		if ctx.Err() != nil {
			return abort()
		}
		if err != nil {
			continue
		}
		cycle := false
		for _, a := range cur.ancestors {
			if a == canonicalDir {
				cycle = true
				break
			}
		}
		if cycle {
			continue
		}
		childAncestors := append(append([]string(nil), cur.ancestors...), canonicalDir)

		welcomeFS("opendir", cur.dir)
		d, err := os.Open(cur.dir)
		if err != nil {
			if ctx.Err() != nil {
				return abort()
			}
			continue
		}
		entries, err := d.ReadDir(-1) // directory order, as the original's iterator
		d.Close()
		if ctx.Err() != nil {
			return abort()
		}
		if err != nil && len(entries) == 0 {
			continue
		}
		for _, entry := range entries {
			if ctx.Err() != nil {
				return abort()
			}
			entryPath := filepath.Join(cur.dir, entry.Name())
			welcomeFS("stat", entryPath)
			st, err := os.Stat(entryPath)
			if ctx.Err() != nil {
				return abort()
			}
			if err != nil {
				continue
			}
			if st.IsDir() {
				pending = append(pending, pendingDir{dir: entryPath, ancestors: childAncestors})
			} else if st.Mode().IsRegular() && strings.HasSuffix(entry.Name(), ".jsonl") {
				sessions = append(sessions, welcomeSessionFile{entryPath, cur.dir, float64(st.ModTime().UnixNano()) / 1e6})
			}
		}
	}

	if ctx.Err() != nil {
		return abort()
	}
	sort.SliceStable(sessions, func(i, j int) bool { return sessions[i].mtime > sessions[j].mtime })

	type unique struct {
		name  string
		mtime float64
	}
	seen := map[string]bool{}
	var uniqueSessions []unique
	for _, s := range sessions {
		if ctx.Err() != nil {
			return abort()
		}
		name, ok, err := readSessionHeaderProjectName(ctx, s.filePath)
		if err != nil {
			return nil, err
		}
		if !ok {
			name = sessionProjectNameFromDirectory(s.dir)
		}
		if ctx.Err() != nil {
			return abort()
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		uniqueSessions = append(uniqueSessions, unique{name, s.mtime})
		if maxCount > 0 && len(uniqueSessions) >= maxCount {
			break
		}
	}

	now := float64(clock())
	out := []recentSession{}
	for _, s := range jsSliceTo(uniqueSessions, maxCount) {
		name := s.name
		if u := utf16.Encode([]rune(name)); len(u) > 20 {
			name = string(utf16.Decode(u[:17])) + "…"
		}
		out = append(out, recentSession{name, formatTimeAgo(now - s.mtime)})
	}
	return out, nil
}

// jsSliceTo is list.slice(0, end), with a negative end counting from the back.
func jsSliceTo[T any](list []T, end int) []T {
	if end < 0 {
		end += len(list)
	}
	end = max(0, min(end, len(list)))
	return list[:end]
}

func formatTimeAgo(ms float64) string {
	seconds := math.Floor(ms / 1000)
	minutes := math.Floor(seconds / 60)
	hours := math.Floor(minutes / 60)
	days := math.Floor(hours / 24)
	switch {
	case days > 0:
		return jsNumber(days) + "d ago"
	case hours > 0:
		return jsNumber(hours) + "h ago"
	case minutes > 0:
		return jsNumber(minutes) + "m ago"
	}
	return "just now"
}
