package modes

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// upstream: packages/coding-agent/src/modes/magic-keywords.ts
// upstream: packages/coding-agent/src/modes/turn-budget.ts
// upstream: packages/tui/src/prompt/magic-keywords.ts
// upstream: packages/tui/src/prompt/markdown-prose.ts

// MagicKeywordContext holds the session facts a keyword notice renders against.
// upstream: packages/coding-agent/src/modes/magic-keywords.ts:20-37
type MagicKeywordContext struct {
	Tools          []string
	TaskBatch      bool
	ScoutAvailable bool
	EvalTools      bool
	Py             bool
	JS             bool
	FreshAgents    bool
	PerCallModel   bool
}

// MagicKeyword defines one magic keyword trigger and its hidden notice.
// upstream: packages/coding-agent/src/modes/magic-keywords.ts:40-57
type MagicKeyword struct {
	ID       string
	Word     string
	Requires []string
	Notice   func(ctx MagicKeywordContext) string
}

// MagicKeywordNotice is a hidden notice produced for a user message.
type MagicKeywordNotice struct {
	CustomType string
	Content    string
}

// DefaultMagicKeywordContext returns omp's default feature flags for a given tool list.
func DefaultMagicKeywordContext(tools []string) MagicKeywordContext {
	return MagicKeywordContext{
		Tools:          tools,
		TaskBatch:      true,
		ScoutAvailable: true,
		EvalTools:      true,
		Py:             true,
		JS:             true,
		FreshAgents:    false,
		PerCallModel:   false,
	}
}

// RenderOrchestrateNotice renders omp's orchestrate-notice.md against active tools.
// upstream: packages/coding-agent/src/modes/magic-keywords.ts:69-71
func RenderOrchestrateNotice(tools []string) string {
	return renderTemplate(orchestrateNoticeTemplate, templateData{
		Lists: map[string][]string{"tools": tools},
	})
}

// RenderWorkflowNotice renders omp's workflow-notice.md against session capabilities.
// upstream: packages/coding-agent/src/modes/magic-keywords.ts:74-89
func RenderWorkflowNotice(ctx MagicKeywordContext) string {
	return renderTemplate(workflowNoticeTemplate, templateData{
		Bools: map[string]bool{
			"taskBatch":      ctx.TaskBatch,
			"scoutAvailable": ctx.ScoutAvailable,
			"evalTools":      ctx.EvalTools,
			"py":             ctx.Py,
			"js":             ctx.JS,
			"freshAgents":    ctx.FreshAgents,
			"perCallModel":   ctx.PerCallModel,
		},
	})
}

// MagicKeywords is omp's ordered table of magic keywords.
// upstream: packages/coding-agent/src/modes/magic-keywords.ts:91-142
var MagicKeywords = []MagicKeyword{
	{
		ID:       "ultrathink",
		Word:     "ultrathink",
		Requires: nil,
		Notice:   func(MagicKeywordContext) string { return strings.TrimSpace(ultrathinkNoticeRaw) },
	},
	{
		ID:       "chillax",
		Word:     "chillax",
		Requires: nil,
		Notice:   func(MagicKeywordContext) string { return strings.TrimSpace(chillaxNoticeRaw) },
	},
	{
		ID:       "orchestrate",
		Word:     "orchestrate",
		Requires: []string{"task"},
		Notice:   func(ctx MagicKeywordContext) string { return RenderOrchestrateNotice(ctx.Tools) },
	},
	{
		ID:       "workflow",
		Word:     "workflowz",
		Requires: []string{"task", "eval"},
		Notice:   RenderWorkflowNotice,
	},
	{
		ID:       "jevify",
		Word:     "jevify",
		Requires: []string{"eval"},
		Notice:   func(MagicKeywordContext) string { return strings.TrimSpace(jevifyNoticeRaw) },
	},
}

// MagicKeywordSettings controls which magic keywords are enabled.
type MagicKeywordSettings struct {
	Enabled  bool
	Disabled map[string]bool // keyed by keyword ID (e.g. "ultrathink", "workflow")
}

func (s MagicKeywordSettings) isEnabled(id string) bool {
	if !s.Enabled {
		return false
	}
	return !s.Disabled[id]
}

// DefaultMagicKeywordSettings enables all magic keywords (omp default).
func DefaultMagicKeywordSettings() MagicKeywordSettings {
	return MagicKeywordSettings{Enabled: true}
}

// BuildMagicKeywordNotices returns the hidden notices triggered by standalone
// prose magic keywords in text, skipping keywords whose required tools are not active.
// upstream: packages/coding-agent/src/session/agent-session.ts:9089-9121
func BuildMagicKeywordNotices(text string, ctx MagicKeywordContext, settings MagicKeywordSettings) []MagicKeywordNotice {
	if !settings.Enabled {
		return nil
	}
	var notices []MagicKeywordNotice
	for _, kw := range MagicKeywords {
		if !settings.isEnabled(kw.ID) || !ContainsMagicKeyword(text, kw.Word) {
			continue
		}
		if !hasAllTools(ctx.Tools, kw.Requires) {
			continue
		}
		notices = append(notices, MagicKeywordNotice{
			CustomType: kw.ID + "-notice",
			Content:    kw.Notice(ctx),
		})
	}
	return notices
}

func hasAllTools(active, required []string) bool {
	for _, req := range required {
		found := false
		for _, a := range active {
			if a == req {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// ContainsMagicKeyword reports whether text contains word as standalone
// lowercase prose — never inside fenced code blocks, inline code spans, or
// HTML/XML sections, and respecting omp's left/right boundary rules.
// upstream: packages/tui/src/prompt/magic-keywords.ts:23-93
func ContainsMagicKeyword(text, word string) bool {
	if word == "" || !strings.Contains(text, word) {
		return false
	}
	if !hasWordBoundaryMatch(text, word) {
		return false
	}
	return hasWordBoundaryMatch(MaskNonProse(text), word)
}

func hasWordBoundaryMatch(text, word string) bool {
	start := 0
	for {
		idx := strings.Index(text[start:], word)
		if idx < 0 {
			return false
		}
		pos := start + idx
		end := pos + len(word)
		if validLeftBoundary(text, pos) && validRightBoundary(text, end) {
			return true
		}
		start = pos + 1
	}
}

func validLeftBoundary(text string, pos int) bool {
	if pos == 0 {
		return true
	}
	if pos >= 2 && text[pos-2:pos] == "::" {
		return false
	}
	r, _ := lastRune(text[:pos])
	if unicode.IsLetter(r) || unicode.IsNumber(r) {
		return false
	}
	switch r {
	case '_', '.', '/', '\\', '-':
		return false
	}
	return true
}

func validRightBoundary(text string, end int) bool {
	if end >= len(text) {
		return true
	}
	r, size := firstRune(text[end:])
	if unicode.IsLetter(r) || unicode.IsNumber(r) {
		return false
	}
	switch r {
	case '_', '/', '\\', '-', '(':
		return false
	case '.':
		if end+size < len(text) {
			next, _ := firstRune(text[end+size:])
			if unicode.IsLetter(next) || unicode.IsNumber(next) || next == '_' || next == '-' {
				return false
			}
		}
	}
	return true
}

func firstRune(s string) (rune, int) {
	for _, r := range s {
		return r, len(string(r))
	}
	return 0, 0
}

func lastRune(s string) (rune, int) {
	var last rune
	var size int
	for _, r := range s {
		last = r
		size = len(string(r))
	}
	return last, size
}

// fenceRE mirrors packages/tui/src/render/render-utils.ts:124 FENCE_RE.
var fenceRE = regexp.MustCompile("^( {0,3})([`~]{3,})")

// MaskNonProse returns a copy of text where characters inside fenced code
// blocks, inline code spans, and HTML/XML tags/sections are replaced with spaces
// (preserving newlines).
// upstream: packages/tui/src/prompt/markdown-prose.ts:160-234
func MaskNonProse(text string) string {
	if !strings.ContainsAny(text, "`<") && !strings.Contains(text, "~~~") {
		return text
	}
	n := len(text)
	masked := make([]bool, n)

	// Phase 1: fenced code blocks, line by line.
	var fenceChar byte
	fenceLen := 0
	lineStart := 0
	for lineStart <= n {
		nl := strings.IndexByte(text[lineStart:], '\n')
		if nl < 0 {
			nl = n
		} else {
			nl += lineStart
		}
		line := text[lineStart:nl]
		open := fenceRE.FindStringSubmatch(line)
		if fenceChar != 0 {
			for p := lineStart; p < nl; p++ {
				masked[p] = true
			}
			if open != nil && open[2][0] == fenceChar && len(open[2]) >= fenceLen &&
				strings.TrimSpace(line[len(open[1])+len(open[2]):]) == "" {
				fenceChar = 0
				fenceLen = 0
			}
		} else if open != nil {
			marker := open[2]
			ch := marker[0]
			if !(ch == '`' && strings.ContainsRune(line[len(open[1])+len(marker):], '`')) {
				fenceChar = ch
				fenceLen = len(marker)
				for p := lineStart; p < nl; p++ {
					masked[p] = true
				}
			}
		}
		if nl == n {
			break
		}
		lineStart = nl + 1
	}

	// Phase 2: inline code spans and HTML/XML tags/sections.
	i := 0
	for i < n {
		if masked[i] {
			i++
			continue
		}
		c := text[i]
		if c == '`' {
			runEnd := backtickRunEnd(text, i)
			closePos := findBacktickClose(text, runEnd, runEnd-i, masked)
			if closePos >= 0 {
				for p := i; p < closePos; p++ {
					masked[p] = true
				}
				i = closePos
			} else {
				i = runEnd
			}
			continue
		}
		if c == '<' {
			end := maskTagAt(text, i, masked)
			if end > i {
				i = end
			} else {
				i++
			}
			continue
		}
		i++
	}

	out := []byte(text)
	for p := range n {
		if masked[p] && out[p] != '\n' {
			out[p] = ' '
		}
	}
	return string(out)
}

func backtickRunEnd(text string, i int) int {
	j := i
	for j < len(text) && text[j] == '`' {
		j++
	}
	return j
}

func findBacktickClose(text string, from, runLen int, masked []bool) int {
	k := from
	n := len(text)
	for k < n {
		if masked[k] {
			k++
			continue
		}
		if text[k] == '`' {
			e := backtickRunEnd(text, k)
			if e-k == runLen {
				return e
			}
			k = e
			continue
		}
		k++
	}
	return -1
}

func readTagName(text string, start int) (name string, end int) {
	if start >= len(text) {
		return "", start
	}
	c := text[start]
	if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')) {
		return "", start
	}
	j := start + 1
	for j < len(text) {
		ch := text[j]
		if (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-' {
			j++
		} else {
			break
		}
	}
	return text[start:j], j
}

func findTagEnd(text string, j int) int {
	var quote byte
	for k := j; k < len(text); k++ {
		ch := text[k]
		if quote != 0 {
			if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '"' || ch == '\'' {
			quote = ch
			continue
		}
		if ch == '>' {
			return k
		}
		if ch == '<' {
			return -1
		}
	}
	return -1
}

func findMatchingClose(text string, start int, name string, masked []bool) int {
	lname := strings.ToLower(name)
	depth := 1
	k := start
	n := len(text)
	for k < n {
		if masked[k] || text[k] != '<' {
			k++
			continue
		}
		m := k + 1
		isClose := false
		if m < n && text[m] == '/' {
			isClose = true
			m++
		}
		nm, afterName := readTagName(text, m)
		if nm == "" {
			k++
			continue
		}
		gt := findTagEnd(text, afterName)
		if gt < 0 {
			k++
			continue
		}
		if strings.ToLower(nm) == lname {
			if isClose {
				depth--
				if depth == 0 {
					return gt + 1
				}
			} else if text[gt-1] != '/' {
				depth++
			}
		}
		k = gt + 1
	}
	return -1
}

func maskTagAt(text string, i int, masked []bool) int {
	n := len(text)
	if strings.HasPrefix(text[i:], "<!--") {
		end := strings.Index(text[i+4:], "-->")
		stop := n
		if end >= 0 {
			stop = i + 4 + end + 3
		}
		for p := i; p < stop; p++ {
			masked[p] = true
		}
		return stop
	}
	j := i + 1
	closing := false
	if j < n && text[j] == '/' {
		closing = true
		j++
	}
	nm, afterName := readTagName(text, j)
	if nm == "" {
		return i
	}
	gt := findTagEnd(text, afterName)
	if gt < 0 {
		return i
	}
	tagEnd := gt + 1
	selfClosing := text[gt-1] == '/'
	for p := i; p < tagEnd; p++ {
		masked[p] = true
	}
	if closing || selfClosing {
		return tagEnd
	}
	closeEnd := findMatchingClose(text, tagEnd, nm, masked)
	if closeEnd < 0 {
		return tagEnd
	}
	for p := tagEnd; p < closeEnd; p++ {
		masked[p] = true
	}
	return closeEnd
}

// TurnBudget is a parsed `+Nk`/`+Nm`/`+N`(`!`) directive from a user message.
// upstream: packages/coding-agent/src/modes/turn-budget.ts:15-20
type TurnBudget struct {
	Total int
	Hard  bool
}

var turnBudgetRE = regexp.MustCompile(`(?i)(?:^|\s)\+(\d+(?:\.\d+)?)([km])?(!)?(?:\s|$)`)

// ParseTurnBudget parses a standalone `+Nk`/`+N`/`+Nm`(`!`) turn-budget directive.
// upstream: packages/coding-agent/src/modes/turn-budget.ts:23-31
func ParseTurnBudget(text string) *TurnBudget {
	m := turnBudgetRE.FindStringSubmatch(text)
	if m == nil {
		return nil
	}
	val, err := strconv.ParseFloat(m[1], 64)
	if err != nil || math.IsNaN(val) || math.IsInf(val, 0) || val <= 0 {
		return nil
	}
	mult := 1.0
	switch strings.ToLower(m[2]) {
	case "k":
		mult = 1_000
	case "m":
		mult = 1_000_000
	}
	return &TurnBudget{
		Total: int(math.Round(val * mult)),
		Hard:  m[3] == "!",
	}
}

// thinkingLevelRank orders PiG/omp thinking levels from lowest to highest.
var thinkingLevelRank = map[string]int{
	"off":     0,
	"minimal": 1,
	"low":     2,
	"medium":  3,
	"high":    4,
	"xhigh":   5,
	"max":     6,
}

// modelTakesPerMessageEffort mirrors omp's `takesPerMessageEffort(model)`.
// upstream: packages/coding-agent/src/session/model-controls.ts:65-74
func modelTakesPerMessageEffort(model map[string]any) bool {
	if model == nil {
		return false
	}
	compat, ok := model["compat"].(map[string]any)
	if !ok || compat == nil {
		return false
	}
	if v, ok := compat["supportsPerMessageEffort"].(bool); ok && v {
		return true
	}
	if v, ok := compat["supportsConfigurationUpdate"].(bool); ok && v {
		if rm, _ := model["reasoningMode"].(string); rm != "pro" {
			return true
		}
	}
	return false
}

// resolveChillaxThinkingChange computes omp's `applyChillax` thinking level
// reduction for pinned levels:
//   - Only applies when `chillax` is enabled and present as standalone prose.
//   - Only applies to models with `takesPerMessageEffort(model)` (changes
//     effort without busting the cached prefix).
//   - Lowers a pinned level to the model's lowest non-off effort; never raises
//     and leaves `off` alone.
//
// `ultrathink` NEVER mutates a pinned thinking level in omp — it only bypasses
// the auto-thinking classifier when `auto` mode is active (`model-controls.ts:671-675`),
// which PiG does not have; for pinned levels, `ultrathink` injects only its notice.
// upstream: packages/coding-agent/src/session/model-controls.ts:730-745
func resolveChillaxThinkingChange(promptText, currentLevel string, model map[string]any, supported []string, settings MagicKeywordSettings) (string, bool) {
	if !settings.isEnabled("chillax") || !ContainsMagicKeyword(promptText, "chillax") {
		return "", false
	}
	if !modelTakesPerMessageEffort(model) {
		return "", false
	}
	if currentLevel == "off" || currentLevel == "" {
		return "", false
	}
	floor := lowestThinkingEffort(supported)
	if floor == "" {
		return "", false
	}
	curRank, curOK := thinkingLevelRank[currentLevel]
	floorRank, floorOK := thinkingLevelRank[floor]
	if curOK && floorOK && floorRank < curRank {
		return floor, true
	}
	return "", false
}

func lowestThinkingEffort(supported []string) string {
	best := ""
	bestRank := math.MaxInt
	for _, lv := range supported {
		if lv == "off" {
			continue
		}
		rank, ok := thinkingLevelRank[lv]
		if ok && rank < bestRank {
			best = lv
			bestRank = rank
		}
	}
	return best
}
