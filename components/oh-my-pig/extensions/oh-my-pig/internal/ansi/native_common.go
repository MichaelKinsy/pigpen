package ansi

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/dirs"
)

// Native view builders for the Tern surface (surface.Native() == true).
// Every builder ports omp's describeCall / describeResult hooks at v19.1.1
// (31ffee513f): one semantic kit.Node per omp native node, ID = omp role
// (<role> or <role>#<index>, nested #<i >.<j>), raw payload in the node's own
// fields, no terminal borders, padding, gutters, or ANSI escapes.
//
// upstream: packages/tui/src/tools/native-view.ts, packages/tui/src/native/describe.ts

const (
	// nvMaxNodes keeps every native kit.View well under PiG host's 4096-node limit
	// (coding/extension/host/subprocess/view_kit.go:27).
	nvMaxNodes = 3600

	// nvMaxLinesPerNode caps raw line slices on single kit.Lines nodes so huge
	// tool outputs do not bloat wire frames.
	nvMaxLinesPerNode = 2000

	// nvErrorGistChars matches ERROR_GIST_CHARS in tools/native-view.ts:108.
	nvErrorGistChars = 120

	// nvDiagnosticRows matches DIAGNOSTIC_ROWS in tools/native-view.ts:306.
	nvDiagnosticRows = 5

	// nvCollapsedPreviewLines / nvExpandedPreviewLines mirror PREVIEW_LIMITS.
	nvCollapsedPreviewLines = 3
	nvExpandedPreviewLines  = 12
	nvOutputCollapsedLines  = 3
	nvOutputExpandedLines   = 10
	nvCollapsedItemsLimit   = 8
)

// nvScope tracks unique node IDs and enforces the per-view node budget.
type nvScope struct {
	used  map[string]int
	count int
}

func newNVScope() *nvScope {
	return &nvScope{used: make(map[string]int)}
}

// claim returns base if unused, otherwise <role>#<n> (never producing double
// `#` suffixes like `role#0#0`), guaranteeing uniqueness across one kit.View.
func (s *nvScope) claim(base string) string {
	if base == "" {
		return ""
	}
	if _, exists := s.used[base]; !exists {
		s.used[base] = 0
		return base
	}
	role := base
	if hash := strings.IndexByte(role, '#'); hash >= 0 {
		role = role[:hash]
	}
	for {
		n := s.used[role]
		s.used[role] = n + 1
		cand := fmt.Sprintf("%s#%d", role, n)
		if _, exists := s.used[cand]; !exists {
			s.used[cand] = 0
			return cand
		}
	}
}

func (s *nvScope) indexed(role string, index int) string {
	return s.claim(fmt.Sprintf("%s#%d", role, index))
}

func (s *nvScope) subIndexed(role string, parent, child int) string {
	cand := fmt.Sprintf("%s#%d.%d", role, parent, child)
	if _, exists := s.used[cand]; !exists {
		s.used[cand] = 0
		return cand
	}
	for next := child + 1; ; next++ {
		alt := fmt.Sprintf("%s#%d.%d", role, parent, next)
		if _, exists := s.used[alt]; !exists {
			s.used[alt] = 0
			return alt
		}
	}
}
func (s *nvScope) canAdd(n int) bool {
	return s.count+n <= nvMaxNodes
}

func (s *nvScope) add(n int) {
	s.count += n
}

// nvAnsiRE strips CSI, OSC, DCS/APC/PM/SOS, and two-byte ESC sequences.
// upstream: packages/tui/src/native/spans.ts:58-59
var nvAnsiRE = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\)|[P_^X][^\x07\x1b]*(?:\x07|\x1b\\)|[@-Z\\-_])`)

// nvSanitize strips ANSI escape sequences, C0 controls (except \t and \n),
// CR, DEL, C1 controls (U+0080-U+009F), and invalid UTF-8 bytes.
// upstream: packages/utils/src/sanitize-text.ts:21-40
func nvSanitize(s string) string {
	if s == "" {
		return ""
	}
	if strings.ContainsRune(s, '\x1b') {
		s = nvAnsiRE.ReplaceAllString(s, "")
	}
	if strings.ContainsRune(s, '\r') {
		s = strings.ReplaceAll(s, "\r\n", "\n")
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			i++
			continue
		}
		i += size
		if r == '\t' || r == '\n' {
			b.WriteRune(r)
			continue
		}
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// nvPlainText sanitizes text and expands tabs to 3 spaces, matching omp's
// native/spans.ts plainText() + replaceTabs().
// upstream: packages/tui/src/native/spans.ts:219-221, 265-267
func nvPlainText(s string) string {
	return strings.ReplaceAll(nvSanitize(s), "\t", "   ")
}

// nvPlainLine collapses whitespace and line breaks into one clean line.
// upstream: packages/tui/src/native/spans.ts:270-275
func nvPlainLine(s string) string {
	return strings.Join(strings.Fields(nvPlainText(s)), " ")
}

// nvSanitizeLines splits s into sanitized lines with tabs expanded (for prose/tables),
// capping at nvMaxLinesPerNode.
func nvSanitizeLines(s string) []string {
	clean := nvPlainText(s)
	if clean == "" {
		return nil
	}
	lines := strings.Split(clean, "\n")
	if len(lines) > nvMaxLinesPerNode {
		lines = lines[:nvMaxLinesPerNode]
	}
	return lines
}

// nvRawLines splits s into sanitized lines while preserving literal tabs
// (matching omp's code()/diff()/ansi() native payloads: tools/read.ts:864,
// tools/write.ts:153, tools/edit.ts:1442, tools/eval.ts:922).
func nvRawLines(s string) []string {
	clean := nvSanitize(s)
	if clean == "" {
		return nil
	}
	lines := strings.Split(clean, "\n")
	if len(lines) > nvMaxLinesPerNode {
		lines = lines[:nvMaxLinesPerNode]
	}
	return lines
}

// ── Kit node builders (raw payloads, zero terminal chrome) ───────────────────

func (s *nvScope) text(id, content string) *kit.Text {
	s.add(1)
	return &kit.Text{ID: id, Text: content}
}

func (s *nvScope) lines(id string, rows []string) *kit.Lines {
	s.add(1)
	if len(rows) > nvMaxLinesPerNode {
		rows = rows[:nvMaxLinesPerNode]
	}
	return &kit.Lines{ID: id, Lines: rows}
}

func (s *nvScope) md(id, content string) *kit.Markdown {
	s.add(1)
	return &kit.Markdown{ID: id, Text: content}
}

func (s *nvScope) diff(id, diffText, filePath string) *kit.Diff {
	s.add(1)
	return &kit.Diff{ID: id, Diff: diffText, FilePath: filePath}
}

func (s *nvScope) container(id string, children ...kit.Node) *kit.Container {
	out := make([]kit.Node, 0, len(children))
	for _, ch := range children {
		if ch != nil {
			out = append(out, ch)
		}
	}
	s.add(1)
	return &kit.Container{ID: id, Children: out}
}

func (s *nvScope) vstack(id string, gap int, children ...kit.Node) *kit.VStack {
	out := make([]kit.StackChild, 0, len(children))
	for _, ch := range children {
		if ch != nil {
			out = append(out, kit.StackEntry{Node: ch})
		}
	}
	s.add(1)
	return &kit.VStack{ID: id, Gap: gap, Children: out}
}

func (s *nvScope) hstack(id string, gap int, children ...kit.Node) *kit.HStack {
	out := make([]kit.StackChild, 0, len(children))
	for _, ch := range children {
		if ch != nil {
			out = append(out, kit.StackEntry{Node: ch})
		}
	}
	s.add(1)
	return &kit.HStack{ID: id, Gap: gap, Children: out}
}

// listLines creates a *kit.Lines node with 1:1 kit.List items for structured
// lists/tables (proc://, irc receipts, eval tables, debug kv snapshots).
// PiG's host validator requires len(Lines) == len(List.Items).
func (s *nvScope) listLines(id string, items []kit.ListItem) *kit.Lines {
	if len(items) > nvMaxLinesPerNode {
		items = items[:nvMaxLinesPerNode]
	}
	rows := make([]string, len(items))
	for i, it := range items {
		parts := []string{it.Label}
		if it.Detail != "" {
			parts = append(parts, it.Detail)
		}
		for _, col := range it.Columns {
			if col != "" {
				parts = append(parts, col)
			}
		}
		rows[i] = strings.Join(parts, " · ")
	}
	s.add(1)
	if len(items) == 0 {
		return &kit.Lines{ID: id, Lines: []string{}}
	}
	return &kit.Lines{
		ID:    id,
		Lines: rows,
		List:  &kit.List{Items: items, Selected: -1},
	}
}

func (s *nvScope) progressLines(id, label string, value, maxVal float64) *kit.Lines {
	if maxVal <= 0 {
		maxVal = 1
	}
	if value < 0 {
		value = 0
	}
	if value > maxVal {
		value = maxVal
	}
	s.add(1)
	return &kit.Lines{
		ID:       id,
		Lines:    []string{label},
		Progress: &kit.Progress{Value: value, Max: maxVal},
	}
}

// ── Shared semantic nodes (tools/native-view.ts) ─────────────────────────────

// nvErrorNode builds omp.tool.error: multiline JSON uses *kit.Lines, plain
// text uses *kit.Text.
// upstream: packages/tui/src/tools/native-view.ts:152-157
func nvErrorNode(sc *nvScope, id, message string) kit.Node {
	body := strings.TrimSpace(nvPlainText(message))
	if body == "" {
		body = "failed"
	}
	if id == "" {
		id = sc.claim("omp.tool.error")
	}
	if strings.Contains(body, "\n") && (strings.HasPrefix(body, "{") || strings.HasPrefix(body, "[")) {
		return sc.lines(id, nvSanitizeLines(body))
	}
	return sc.text(id, body)
}

// nvStatsNode builds omp.tool.stats (`a · b · c`), or nil if parts is empty.
// upstream: packages/tui/src/tools/native-view.ts:174-177
func nvStatsNode(sc *nvScope, parts []string) kit.Node {
	clean := nvCompactStrings(parts)
	if len(clean) == 0 {
		return nil
	}
	return sc.text(sc.claim("omp.tool.stats"), strings.Join(clean, " · "))
}

// nvNoticeNode builds omp.tool.notice, or nil if message is empty.
// upstream: packages/tui/src/tools/native-view.ts:180-186
func nvNoticeNode(sc *nvScope, message string) kit.Node {
	clean := strings.TrimSpace(nvPlainText(message))
	if clean == "" {
		return nil
	}
	return sc.text(sc.claim("omp.tool.notice"), clean)
}

// nvFootnoteNode builds the final quiet line of a tool body: omp.tool.notice
// when meta has truncation/artifactError, else omp.tool.stats.
// upstream: packages/tui/src/tools/native-view.ts:192-202
func nvFootnoteNode(sc *nvScope, parts []string, meta map[string]any) kit.Node {
	all := nvCompactStrings(parts)
	hasNotice := false
	if trunc := nvMap(meta, "truncation"); trunc != nil {
		if msg := nvFormatTruncationMetaNotice(trunc, nvMap(meta, "source")); msg != "" {
			all = append(all, msg)
			hasNotice = true
		}
	}
	if artErr := nvMap(meta, "artifactError"); artErr != nil {
		if msg := nvFormatArtifactErrorNotice(artErr); msg != "" {
			all = append(all, msg)
			hasNotice = true
		}
	}
	if len(all) == 0 {
		return nil
	}
	role := "omp.tool.stats"
	if hasNotice {
		role = "omp.tool.notice"
	}
	return sc.text(sc.claim(role), strings.Join(all, " · "))
}

// nvRunState represents omp's RunState for run boxes (bash, eval).
// upstream: packages/tui/src/tools/native-view.ts:212-221
type nvRunState string

const (
	nvRunStateRunning    nvRunState = "running"
	nvRunStateBackground nvRunState = "background"
	nvRunStateDone       nvRunState = "done"
	nvRunStateFailed     nvRunState = "failed"
	nvRunStateCancelled  nvRunState = "cancelled"
	nvRunStateTimedOut   nvRunState = "timed-out"
)

type nvRunFootInput struct {
	state     nvRunState
	exitCode  *int
	elapsedMs *int64
	facts     []string
	meta      map[string]any
	indexed   bool
	index     int
}

// nvRunFoot builds omp.run.foot > omp.run.state, optional omp.run.time,
// optional omp.run.facts.
// upstream: packages/tui/src/tools/native-view.ts:241-278
func nvRunFoot(sc *nvScope, in nvRunFootInput) kit.Node {
	word := "Done"
	switch in.state {
	case nvRunStateRunning:
		word = "Running"
	case nvRunStateBackground:
		word = "In background"
	case nvRunStateDone:
		word = "Done"
	case nvRunStateFailed:
		if in.exitCode != nil && *in.exitCode != 0 {
			word = fmt.Sprintf("exit %d", *in.exitCode)
		} else {
			word = "Failed"
		}
	case nvRunStateCancelled:
		word = "Cancelled"
	case nvRunStateTimedOut:
		word = "Timed out"
	}

	claimRole := func(role string) string {
		if in.indexed {
			return sc.indexed(role, in.index)
		}
		return sc.claim(role)
	}

	children := []kit.Node{
		sc.text(claimRole("omp.run.state"), word),
	}

	if in.elapsedMs != nil {
		ms := *in.elapsedMs
		if ms < 0 {
			ms = 0
		}
		children = append(children, sc.text(claimRole("omp.run.time"), nvFormatDuration(ms)))
	}

	factParts := nvCompactStrings(in.facts)
	if in.meta != nil {
		if trunc := nvMap(in.meta, "truncation"); trunc != nil {
			if msg := nvFormatTruncationMetaNotice(trunc, nvMap(in.meta, "source")); msg != "" {
				factParts = append(factParts, msg)
			}
		}
		if artErr := nvMap(in.meta, "artifactError"); artErr != nil {
			if msg := nvFormatArtifactErrorNotice(artErr); msg != "" {
				factParts = append(factParts, msg)
			}
		}
	}
	if len(factParts) > 0 {
		children = append(children, sc.text(claimRole("omp.run.facts"), strings.Join(factParts, " · ")))
	}

	return sc.hstack(claimRole("omp.run.foot"), 1, children...)
}

// nvFileDiffSection builds a borderless section container (<role>#<index>)
// with a dedicated head child (<role>.head#<index>) over body nodes.
// upstream: packages/tui/src/tools/native-view.ts:50-60
func nvFileDiffSection(sc *nvScope, role string, index int, filePath, rename string, added, removed int, cwd string, body ...kit.Node) kit.Node {
	var headParts []string
	disp := nvDisplayPath(nvPlainText(filePath), cwd)
	if disp != "" {
		headParts = append(headParts, disp)
	}
	if rename != "" {
		headParts = append(headParts, "→ "+nvDisplayPath(nvPlainText(rename), cwd))
	}
	if stat := nvDiffStatsMeta(added, removed); stat != "" {
		headParts = append(headParts, stat)
	}
	headID := sc.indexed(role+".head", index)
	children := make([]kit.Node, 0, 1+len(body))
	children = append(children, sc.text(headID, strings.Join(headParts, " ")))
	children = append(children, body...)
	return sc.vstack(sc.indexed(role, index), 0, children...)
}

// nvParsedDiag represents a parsed LSP diagnostic message.
// upstream: packages/tui/src/render/render-utils.ts parseDiagnosticMessage
type nvParsedDiag struct {
	filePath string
	line     int
	col      int
	severity string // "error" | "warning" | "info" | "hint"
	message  string
	source   string
	code     string
}

var nvDiagLineRE = regexp.MustCompile(`^(.+?):(\d+):(\d+)\s+\[(error|warning|warn|info|hint)\]\s+(.+)$`)
var nvDiagSourceTailRE = regexp.MustCompile(`^(.*?)\s+\(([^()]+)\)$`)

func nvParseDiagnosticMessage(raw string) *nvParsedDiag {
	m := nvDiagLineRE.FindStringSubmatch(strings.TrimSpace(raw))
	if m == nil {
		return nil
	}
	line, _ := strconv.Atoi(m[2])
	col, _ := strconv.Atoi(m[3])
	sev := strings.ToLower(m[4])
	if sev == "warn" {
		sev = "warning"
	}
	msg := strings.TrimSpace(m[5])
	source := ""
	if sm := nvDiagSourceTailRE.FindStringSubmatch(msg); sm != nil {
		msg = strings.TrimSpace(sm[1])
		source = strings.TrimSpace(sm[2])
	}
	return &nvParsedDiag{
		filePath: m[1],
		line:     line,
		col:      col,
		severity: sev,
		message:  msg,
		source:   source,
	}
}

func nvSeverityRank(sev string) int {
	switch sev {
	case "error":
		return 0
	case "warning":
		return 1
	case "info":
		return 2
	case "hint":
		return 3
	default:
		return 4
	}
}

// nvDiagnosticsSection builds omp.tool.diagnostics with dedicated head
// omp.tool.diagnostics.head and up to 5 omp.tool.diagnostic rows.
// When fileIdx >= 0 (multi-file edit), IDs use #<fileIdx> / #<fileIdx>.<diagIdx>
// to keep every ID strictly single-hash per VIEW-IDS.md.
// upstream: packages/tui/src/tools/native-view.ts:353-390
func nvDiagnosticsSection(sc *nvScope, diag map[string]any, withPath bool, cwd string, fileIdx int) kit.Node {
	if diag == nil {
		return nil
	}
	messages := nvStrSlice(diag, "messages")
	if len(messages) == 0 {
		return nil
	}
	var parsed []*nvParsedDiag
	var unparsed []string
	fileSet := map[string]struct{}{}
	for _, raw := range messages {
		clean := strings.TrimSpace(nvPlainText(raw))
		if clean == "" {
			continue
		}
		if d := nvParseDiagnosticMessage(clean); d != nil {
			parsed = append(parsed, d)
			fileSet[d.filePath] = struct{}{}
		} else {
			unparsed = append(unparsed, clean)
		}
	}
	// Sort parsed diagnostics by severity, line, col.
	for i := 1; i < len(parsed); i++ {
		for j := i; j > 0; j-- {
			a, b := parsed[j-1], parsed[j]
			if nvSeverityRank(a.severity) > nvSeverityRank(b.severity) ||
				(nvSeverityRank(a.severity) == nvSeverityRank(b.severity) && (a.line > b.line || (a.line == b.line && a.col > b.col))) {
				parsed[j-1], parsed[j] = b, a
			} else {
				break
			}
		}
	}
	showPath := withPath || len(fileSet) > 1
	var rows []kit.Node
	total := len(parsed) + len(unparsed)
	diagID := func(i int) string {
		if fileIdx >= 0 {
			return sc.subIndexed("omp.tool.diagnostic", fileIdx, i)
		}
		return sc.indexed("omp.tool.diagnostic", i)
	}
	idx := 0
	for _, d := range parsed {
		if len(rows) >= nvDiagnosticRows {
			break
		}
		where := fmt.Sprintf("%d:%d", d.line, d.col)
		if showPath {
			where = nvDisplayPath(d.filePath, cwd) + ":" + where
		}
		msg := d.message
		if d.source != "" {
			msg += " " + d.source
		}
		rows = append(rows, sc.text(diagID(idx), fmt.Sprintf("[%s] %s %s", d.severity, where, msg)))
		idx++
	}
	for _, u := range unparsed {
		if len(rows) >= nvDiagnosticRows {
			break
		}
		rows = append(rows, sc.text(diagID(idx), u))
		idx++
	}
	if len(rows) == 0 {
		return nil
	}
	if hidden := total - len(rows); hidden > 0 {
		var moreID string
		if fileIdx >= 0 {
			moreID = sc.indexed("omp.tool.stats", fileIdx)
		} else {
			moreID = sc.claim("omp.tool.stats")
		}
		rows = append(rows, sc.text(moreID, fmt.Sprintf("+%d more", hidden)))
	}
	var headID, secID string
	if fileIdx >= 0 {
		headID = sc.indexed("omp.tool.diagnostics.head", fileIdx)
		secID = sc.indexed("omp.tool.diagnostics", fileIdx)
	} else {
		headID = sc.claim("omp.tool.diagnostics.head")
		secID = sc.claim("omp.tool.diagnostics")
	}
	children := make([]kit.Node, 0, 1+len(rows))
	children = append(children, sc.text(headID, "Diagnostics"))
	children = append(children, rows...)
	return sc.vstack(secID, 0, children...)
}

// ── Truncation & artifact notice formatting (tools/output-meta.ts) ───────────

// nvFormatTruncationMetaNotice mirrors formatTruncationMetaNotice in
// packages/tui/src/tools/output-meta.ts:242-310.
func nvFormatTruncationMetaNotice(trunc map[string]any, source map[string]any) string {
	if trunc == nil {
		return ""
	}
	direction := nvStr(trunc, "direction")
	truncBy := nvStr(trunc, "truncatedBy")
	totalLines := nvInt(trunc, "totalLines")
	totalBytes := nvInt64(trunc, "totalBytes")
	outputLines := nvInt(trunc, "outputLines")
	outputBytes := nvInt64(trunc, "outputBytes")
	maxBytes := nvInt64(trunc, "maxBytes")
	artifactID := nvStr(trunc, "artifactId")
	artifactElided := nvInt64(trunc, "artifactElidedBytes")
	partialLine := nvBool(trunc, "partialLine")

	var parts []string
	switch {
	case partialLine:
		parts = append(parts, fmt.Sprintf("Showing %s of %s (partial line)", nvFormatBytes(outputBytes), nvFormatBytes(totalBytes)))
	case direction == "middle":
		elidedLines := nvInt(trunc, "elidedLines")
		elidedBytes := nvInt64(trunc, "elidedBytes")
		if elidedLines > 0 {
			parts = append(parts, fmt.Sprintf("Elided %s (%s) from middle", nvFormatCount("line", elidedLines), nvFormatBytes(elidedBytes)))
		} else {
			parts = append(parts, fmt.Sprintf("Elided %s from middle", nvFormatBytes(elidedBytes)))
		}
	default:
		shownRange := nvMap(trunc, "shownRange")
		start := nvInt(shownRange, "start")
		end := nvInt(shownRange, "end")
		limitNote := ""
		if truncBy == "bytes" && maxBytes > 0 {
			limitNote = fmt.Sprintf(" (%s limit)", nvFormatBytes(maxBytes))
		}
		if start > 0 && end >= start && totalLines > 0 {
			parts = append(parts, fmt.Sprintf("Showing lines %d-%d of %d%s", start, end, totalLines, limitNote))
		} else if outputLines > 0 && totalLines > 0 {
			parts = append(parts, fmt.Sprintf("Showing %d of %d lines%s", outputLines, totalLines, limitNote))
		} else if totalBytes > 0 {
			parts = append(parts, fmt.Sprintf("Output truncated (%s of %s)", nvFormatBytes(outputBytes), nvFormatBytes(totalBytes)))
		} else {
			parts = append(parts, "Output truncated")
		}
	}
	if artifactID != "" {
		parts = append(parts, nvFormatFullOutputRef(artifactID, artifactElided))
	} else if nextOffset := nvInt(trunc, "nextOffset"); nextOffset > 0 {
		srcVal := nvStr(source, "value")
		if srcVal != "" {
			parts = append(parts, fmt.Sprintf("Use %s:%d- to continue", nvShortenPath(srcVal), nextOffset))
		} else {
			parts = append(parts, fmt.Sprintf("Use :%d- to continue", nextOffset))
		}
	}
	return strings.Join(parts, ". ")
}

func nvFormatArtifactErrorNotice(artErr map[string]any) string {
	if artErr == nil {
		return ""
	}
	msg := nvStr(artErr, "message")
	if msg == "" {
		return "Full output capture failed"
	}
	return "Full output capture failed: " + nvPlainText(msg)
}

// nvFormatFullOutputRef mirrors formatFullOutputReference in
// packages/tui/src/tools/output-meta.ts:174-179 and spillUrl in
// packages/utils/src/spill-url.ts:17 (`local://<id>.log`).
func nvFormatFullOutputRef(artifactID string, elidedBytes int64) string {
	urlStr := artifactID
	if !strings.Contains(urlStr, "://") {
		urlStr = "local://" + artifactID + ".log"
	}
	if elidedBytes > 0 {
		return fmt.Sprintf("Read %s for a head/tail sample of the output; %s from its middle was not saved", urlStr, nvFormatBytes(elidedBytes))
	}
	return fmt.Sprintf("Read %s for full output", urlStr)
}

// ── Details / argument extraction helpers ────────────────────────────────────

// nvToMap converts any Details payload (map[string]any or typed Go struct)
// into a map[string]any using JSON tags so native renderers work both with
// in-process typed structs and JSON-roundtripped subprocess Details.
func nvToMap(v any) map[string]any {
	if v == nil {
		return nil
	}
	if m, ok := v.(map[string]any); ok {
		return m
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil
	}
	return out
}

func nvMap(m map[string]any, keys ...string) map[string]any {
	if m == nil {
		return nil
	}
	for _, k := range keys {
		if raw, ok := m[k]; ok && raw != nil {
			if sub := nvToMap(raw); sub != nil {
				return sub
			}
		}
	}
	return nil
}

func nvStr(m map[string]any, keys ...string) string {
	if m == nil {
		return ""
	}
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return ""
}

func nvBool(m map[string]any, keys ...string) bool {
	if m == nil {
		return false
	}
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			if b, ok := v.(bool); ok {
				return b
			}
		}
	}
	return false
}

func nvHasKey(m map[string]any, key string) bool {
	if m == nil {
		return false
	}
	_, ok := m[key]
	return ok
}

func nvFloat(m map[string]any, keys ...string) (float64, bool) {
	if m == nil {
		return 0, false
	}
	for _, k := range keys {
		v, ok := m[k]
		if !ok || v == nil {
			continue
		}
		switch n := v.(type) {
		case float64:
			return n, true
		case float32:
			return float64(n), true
		case int:
			return float64(n), true
		case int64:
			return float64(n), true
		case int32:
			return float64(n), true
		case json.Number:
			if f, err := n.Float64(); err == nil {
				return f, true
			}
		}
	}
	return 0, false
}

func nvInt(m map[string]any, keys ...string) int {
	f, ok := nvFloat(m, keys...)
	if !ok {
		return 0
	}
	return int(math.Round(f))
}

func nvIntPtr(m map[string]any, keys ...string) *int {
	f, ok := nvFloat(m, keys...)
	if !ok {
		return nil
	}
	v := int(math.Round(f))
	return &v
}

func nvInt64(m map[string]any, keys ...string) int64 {
	f, ok := nvFloat(m, keys...)
	if !ok {
		return 0
	}
	return int64(math.Round(f))
}

func nvInt64Ptr(m map[string]any, keys ...string) *int64 {
	f, ok := nvFloat(m, keys...)
	if !ok {
		return nil
	}
	v := int64(math.Round(f))
	return &v
}

func nvSlice(m map[string]any, keys ...string) []any {
	if m == nil {
		return nil
	}
	for _, k := range keys {
		raw, ok := m[k]
		if !ok || raw == nil {
			continue
		}
		if s, ok := raw.([]any); ok {
			return s
		}
		b, err := json.Marshal(raw)
		if err != nil {
			continue
		}
		var out []any
		if err := json.Unmarshal(b, &out); err == nil {
			return out
		}
	}
	return nil
}

func nvStrSlice(m map[string]any, keys ...string) []string {
	if m == nil {
		return nil
	}
	for _, k := range keys {
		raw, ok := m[k]
		if !ok || raw == nil {
			continue
		}
		if ss, ok := raw.([]string); ok {
			return ss
		}
		if s, ok := raw.([]any); ok {
			out := make([]string, 0, len(s))
			for _, item := range s {
				if str, ok := item.(string); ok {
					out = append(out, str)
				}
			}
			return out
		}
	}
	return nil
}

func nvResultText(res sdk.ToolRenderResult) string {
	var b strings.Builder
	for _, block := range res.Content {
		if nvStr(block, "type") != "text" {
			continue
		}
		t := nvStr(block, "text")
		if t == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(t)
	}
	return b.String()
}

func nvExtractImages(res sdk.ToolRenderResult) []nvImageBlock {
	var out []nvImageBlock
	for _, block := range res.Content {
		if nvStr(block, "type") != "image" {
			continue
		}
		raw := nvStr(block, "data")
		mime := nvStr(block, "mimeType")
		if raw == "" || mime == "" {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(raw)
		if err != nil || len(decoded) == 0 {
			continue
		}
		out = append(out, nvImageBlock{Data: decoded, MimeType: mime})
	}
	return out
}

type nvImageBlock struct {
	Data     []byte
	MimeType string
}

// ── Formatting & path helpers ────────────────────────────────────────────────

func nvCompactStrings(parts []string) []string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(nvPlainText(p)); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func nvFormatCount(noun string, n int) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	switch {
	case strings.HasSuffix(noun, "ch") || strings.HasSuffix(noun, "sh") || strings.HasSuffix(noun, "x") || strings.HasSuffix(noun, "s"):
		return fmt.Sprintf("%d %ses", n, noun)
	default:
		return fmt.Sprintf("%d %ss", n, noun)
	}
}

func nvFormatNumber(n int) string {
	if n < 1000 && n > -1000 {
		return strconv.Itoa(n)
	}
	neg := n < 0
	if neg {
		n = -n
	}
	switch {
	case n >= 1_000_000:
		s := fmt.Sprintf("%.1fM", float64(n)/1_000_000)
		s = strings.TrimSuffix(s, ".0M")
		if !strings.HasSuffix(s, "M") {
			s += "M"
		}
		if neg {
			return "-" + s
		}
		return s
	case n >= 1000:
		s := fmt.Sprintf("%.1fK", float64(n)/1000)
		s = strings.TrimSuffix(s, ".0K")
		if !strings.HasSuffix(s, "K") {
			s += "K"
		}
		if neg {
			return "-" + s
		}
		return s
	}
	return strconv.Itoa(n)
}

func nvFormatDuration(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	if ms < 60_000 {
		sec := float64(ms) / 1000.0
		if sec < 10 {
			return fmt.Sprintf("%.1fs", sec)
		}
		return fmt.Sprintf("%ds", int(math.Round(sec)))
	}
	mins := ms / 60_000
	secs := (ms % 60_000) / 1000
	if mins < 60 {
		if secs == 0 {
			return fmt.Sprintf("%dm", mins)
		}
		return fmt.Sprintf("%dm%ds", mins, secs)
	}
	hours := mins / 60
	remMin := mins % 60
	if remMin == 0 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dh%dm", hours, remMin)
}

// nvFormatBytes mirrors formatBytes in packages/utils/src/format.ts:62-67.
func nvFormatBytes(n int64) string {
	if n < 0 {
		n = 0
	}
	if n < 1024 {
		return fmt.Sprintf("%dB", n)
	}
	if n < 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(n)/1024.0)
	}
	if n < 1024*1024*1024 {
		return fmt.Sprintf("%.1fMB", float64(n)/(1024.0*1024.0))
	}
	return fmt.Sprintf("%.1fGB", float64(n)/(1024.0*1024.0*1024.0))
}

func nvDiffStatsMeta(added, removed int) string {
	var parts []string
	if added > 0 {
		parts = append(parts, fmt.Sprintf("+%d", added))
	}
	if removed > 0 {
		parts = append(parts, fmt.Sprintf("\u2212%d", removed))
	}
	return strings.Join(parts, " ")
}

func nvCountDiffStats(diffText string) (added, removed int) {
	for line := range strings.SplitSeq(diffText, "\n") {
		if strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") || strings.HasPrefix(line, "@@") {
			continue
		}
		if strings.HasPrefix(line, "+") {
			added++
		} else if strings.HasPrefix(line, "-") {
			removed++
		}
	}
	return added, removed
}

func nvShortenPath(p string) string {
	return shortenPath(p)
}

// nvDisplayPath returns filePath relative to cwd when inside cwd, else ~-shortened.
// Standardizes the working directory cwd once (`dirs.StandardizeMacOSPath`) like
// omp's startup `getProjectDir()`, without rewriting absolute paths outside cwd.
// upstream: packages/tui/src/tools/native-view.ts:18-24
func nvDisplayPath(filePath, cwd string) string {
	if filePath == "" {
		return ""
	}
	if cwd != "" && filepath.IsAbs(filePath) {
		stdCwd := dirs.StandardizeMacOSPath(cwd)
		if rel, err := filepath.Rel(stdCwd, filePath); err == nil && rel != "" && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
			return filepath.ToSlash(rel)
		}
		if stdCwd != cwd {
			if rel, err := filepath.Rel(cwd, filePath); err == nil && rel != "" && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
				return filepath.ToSlash(rel)
			}
		}
	}
	return nvShortenPath(filePath)
}
func nvSplitURLScheme(raw string) (scheme, rest string, ok bool) {
	idx := strings.Index(raw, "://")
	if idx <= 0 {
		return "", "", false
	}
	cand := raw[:idx]
	for i := range len(cand) {
		c := cand[i]
		isAlpha := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		isDigit := c >= '0' && c <= '9'
		if i == 0 && !isAlpha {
			return "", "", false
		}
		if !isAlpha && !isDigit && c != '+' && c != '-' && c != '.' {
			return "", "", false
		}
	}
	return strings.ToLower(cand), raw[idx+3:], true
}

func nvIsHTTPURL(raw string) bool {
	scheme, _, ok := nvSplitURLScheme(strings.TrimSpace(raw))
	return ok && (scheme == "http" || scheme == "https")
}

func nvGetDomain(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Hostname()
}

func nvFormatAge(ageSec int) string {
	if ageSec <= 0 {
		return ""
	}
	switch {
	case ageSec < 60:
		return fmt.Sprintf("%ds", ageSec)
	case ageSec < 3600:
		return fmt.Sprintf("%dm", ageSec/60)
	case ageSec < 86400:
		return fmt.Sprintf("%dh", ageSec/3600)
	case ageSec < 86400*30:
		return fmt.Sprintf("%dd", ageSec/86400)
	case ageSec < 86400*365:
		return fmt.Sprintf("%dmo", ageSec/(86400*30))
	default:
		return fmt.Sprintf("%dy", ageSec/(86400*365))
	}
}

// nvSplitPathAndSel peels trailing :N-M or :raw/:conflicts/:img selectors.
func nvSplitPathAndSel(raw string) (base, sel string) {
	if raw == "" {
		return "", ""
	}
	if _, _, isURL := nvSplitURLScheme(raw); isURL {
		idx := strings.LastIndexByte(raw, ':')
		if idx > strings.Index(raw, "://")+2 {
			cand := raw[idx+1:]
			if nvIsSelToken(cand) {
				return raw[:idx], cand
			}
		}
		return raw, ""
	}
	start := 0
	if len(raw) >= 2 && raw[1] == ':' && ((raw[0] >= 'a' && raw[0] <= 'z') || (raw[0] >= 'A' && raw[0] <= 'Z')) {
		start = 2
	}
	idx := strings.IndexByte(raw[start:], ':')
	if idx < 0 {
		return raw, ""
	}
	cut := start + idx
	if cut == 0 || cut+1 >= len(raw) {
		return raw, ""
	}
	candSel := raw[cut+1:]
	for tok := range strings.SplitSeq(candSel, ":") {
		if tok == "" || !nvIsSelToken(tok) {
			return raw, ""
		}
	}
	return raw[:cut], candSel
}

func nvIsSelToken(tok string) bool {
	lower := strings.ToLower(tok)
	if lower == "raw" || lower == "conflicts" || lower == "img" {
		return true
	}
	for part := range strings.SplitSeq(tok, ",") {
		if part == "" {
			return false
		}
		s := part
		if strings.HasPrefix(s, "-") {
			if !nvIsDigits(s[1:]) {
				return false
			}
			continue
		}
		s = strings.TrimPrefix(strings.TrimPrefix(s, "L"), "l")
		i := 0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == 0 {
			return false
		}
		rest := s[i:]
		if rest == "" {
			continue
		}
		if rest[0] == '-' || rest[0] == '+' {
			rest = rest[1:]
			if rest != "" && !nvIsDigits(rest) {
				return false
			}
			continue
		}
		return false
	}
	return true
}

func nvIsDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// nvStripTrailingNotice strips the last occurrence of a literal notice or
// matching trailing line, matching tools/output-meta.ts:182-203.
func nvStripTrailingNotice(text string, matchLine func(string) bool) string {
	trimmed := strings.TrimRight(text, " \t\r\n")
	if trimmed == "" {
		return text
	}
	start := strings.LastIndexByte(trimmed, '\n') + 1
	lastLine := trimmed[start:]
	if !matchLine(lastLine) {
		return text
	}
	if start > 0 && trimmed[start-1] == '\n' {
		start--
	}
	return strings.TrimRight(trimmed[:start], " \t\r\n")
}

// nvStripOutputNotice removes trailing bracketed output-meta truncation/notice
// lines emitted by tools.
// upstream: packages/tui/src/tools/output-meta.ts stripOutputNotice
func nvStripOutputNotice(text string, meta map[string]any) string {
	if text == "" {
		return ""
	}
	cur := text
	for range 3 {
		next := nvStripTrailingNotice(cur, func(line string) bool {
			t := strings.TrimSpace(line)
			if !strings.HasPrefix(t, "[") || !strings.HasSuffix(t, "]") {
				return false
			}
			return strings.Contains(t, "Showing lines ") ||
				strings.Contains(t, "more lines") ||
				strings.Contains(t, "Output truncated") ||
				strings.Contains(t, "Some lines truncated") ||
				strings.Contains(t, "results limit reached") ||
				strings.Contains(t, "Full output")
		})
		if next == cur {
			break
		}
		cur = next
	}
	return cur
}

// nvFileExt returns the lowercase file extension without the leading dot.
func nvFileExt(p string) string {
	ext := strings.TrimPrefix(path.Ext(strings.TrimSpace(p)), ".")
	return strings.ToLower(ext)
}

// nvPrettyJSON formats arbitrary JSON-compatible data as indented JSON lines.
func nvPrettyJSON(v any) []string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nvSanitizeLines(fmt.Sprint(v))
	}
	return nvSanitizeLines(string(b))
}

// nvTruncateRunes truncates s to maxRunes, adding '…' if cut.
func nvTruncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	if maxRunes == 1 {
		return "…"
	}
	return strings.TrimRight(string(r[:maxRunes-1]), " \t") + "…"
}
