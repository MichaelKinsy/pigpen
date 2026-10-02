package eq

import (
	_ "embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

//go:embed surface/go-gaps.md
var embeddedSurface string

// Gap is a Pi API the TypeScript source uses that the Go SDK does not fully
// provide. Severity "missing" blocks a faithful port; "partial" is a stand-in
// whose difference must be reviewed and covered by a scenario.
type Gap struct {
	Symbol   string // surface symbol, for example pi.events.on
	Severity string // "missing" or "partial"
	Note     string // the SDK surface note
	Lines    []int  // 1-based source lines that use it
	Related  int    // further surface rows the same source pattern triggers
	File     string // the source file, when a directory was scanned
}

type surfaceRow struct {
	symbol, goCell string
	triggers       []*regexp.Regexp
}

var (
	identRE   = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
	genericRE = map[string]bool{"execute": true, "model": true, "signal": true, "tool": true, "on": true}
)

// parseSurface reads the rows of PiG's extension SDK surface table and derives,
// for each row whose Go cell is not "implemented", the source pattern that
// means "this extension uses that API".
func parseSurface(markdown string) []surfaceRow {
	var rows []surfaceRow
	for _, line := range strings.Split(markdown, "\n") {
		cells := strings.Split(line, "|")
		if len(cells) < 7 || !strings.HasPrefix(strings.TrimSpace(cells[1]), "`") {
			continue
		}
		symbol := strings.Trim(strings.TrimSpace(cells[1]), "`")
		goCell := strings.TrimSpace(cells[4])
		if !strings.HasPrefix(goCell, "missing") && !strings.HasPrefix(goCell, "stand-in") {
			continue
		}
		trig := triggers(symbol)
		if len(trig) == 0 {
			continue
		}
		rows = append(rows, surfaceRow{symbol: symbol, goCell: goCell, triggers: trig})
	}
	return rows
}

// triggers turns a surface symbol into source regexes that must ALL match
// (per file) for the API to count as used. The rules cover the symbol shapes
// of the surface table:
//
//	pi.a.b            -> .a.b
//	pi.on("event")    -> .on("event"
//	ctx.f(options.o)  -> .f( and the word o
//	event return.o    -> "event" and the word o
//	withSession ctx.x -> the word withSession
func triggers(symbol string) []*regexp.Regexp {
	symbol = strings.SplitN(symbol, " →", 2)[0]
	compile := func(p string) *regexp.Regexp { return regexp.MustCompile(p) }
	q := regexp.QuoteMeta
	if m := regexp.MustCompile(`^pi\.on\("([^"]+)"\)$`).FindStringSubmatch(symbol); m != nil {
		return []*regexp.Regexp{compile(`\.on\(\s*["']` + q(m[1]) + `["']`)}
	}
	if strings.HasPrefix(symbol, "withSession ") {
		return []*regexp.Regexp{compile(`\bwithSession\b`)}
	}
	if m := regexp.MustCompile(`^([a-z_]+) (?:event|return)\.([A-Za-z_]+)`).FindStringSubmatch(symbol); m != nil && strings.Contains(m[1], "_") {
		return []*regexp.Regexp{compile(`["']` + q(m[1]) + `["']`), compile(`\b` + q(m[2]) + `\b`)}
	}
	if strings.HasPrefix(symbol, "tool render context.") {
		return []*regexp.Regexp{compile(`\b` + q(strings.TrimPrefix(symbol, "tool render context.")) + `\b`)}
	}
	base, option := symbol, ""
	if i := strings.Index(symbol, "("); i >= 0 {
		base = symbol[:i]
		if j := strings.LastIndex(symbol, "."); j > i {
			option = strings.TrimRight(symbol[j+1:], ")")
		}
	}
	parts := strings.Split(base, ".")
	if len(parts) < 2 {
		return nil
	}
	var member string
	switch parts[0] {
	case "pi", "ctx":
		member = strings.Join(parts[1:], ".")
	case "provider", "tool":
		last := parts[len(parts)-1]
		if genericRE[last] && option == "" {
			return nil
		}
		member = last
	default:
		return nil
	}
	out := []*regexp.Regexp{}
	if parts[0] == "provider" || parts[0] == "tool" {
		out = append(out, compile(`\b`+q(member)+`\b`))
	} else {
		out = append(out, compile(`\.`+q(member)+`\b`))
	}
	if option != "" && identRE.MatchString(option) {
		out = append(out, compile(`\b`+q(option)+`\b`))
	}
	return out
}

// ScanGaps reports the Pi APIs the TypeScript source uses that the Go SDK does
// not fully provide. surfaceMarkdown may be PiG's full docs/extension-sdk-surface.md
// or empty for the embedded snapshot of its non-implemented rows.
func ScanGaps(tsSource, surfaceMarkdown string) []Gap {
	if surfaceMarkdown == "" {
		surfaceMarkdown = embeddedSurface
	}
	if !LooksLikeExtension(tsSource) {
		return nil // `ctx.model` and friends mean something only on the extension's own parameters
	}
	source := stripComments(tsSource)
	lines := strings.Split(source, "\n")
	var gaps []Gap
	seen := map[string]int{} // identical triggers report once, with the count of related rows
	for _, row := range parseSurface(surfaceMarkdown) {
		all := true
		for _, re := range row.triggers {
			if !re.MatchString(source) {
				all = false
				break
			}
		}
		if !all {
			continue
		}
		var sig []string
		for _, re := range row.triggers {
			sig = append(sig, re.String())
		}
		key := strings.Join(sig, "&") + "|" + row.goCell[:min(7, len(row.goCell))]
		if i, dup := seen[key]; dup {
			gaps[i].Related++
			continue
		}
		seen[key] = len(gaps)
		g := Gap{Symbol: row.symbol, Severity: "partial", Note: noteOf(row.goCell)}
		if strings.HasPrefix(row.goCell, "missing") {
			g.Severity = "missing"
		}
		first := row.triggers[0]
		for i, l := range lines {
			if first.MatchString(l) {
				g.Lines = append(g.Lines, i+1)
			}
		}
		gaps = append(gaps, g)
	}
	gaps = append(gaps, indirectBusGaps(source, lines, surfaceMarkdown, gaps)...)
	sort.SliceStable(gaps, func(i, j int) bool {
		if gaps[i].Severity != gaps[j].Severity {
			return gaps[i].Severity == "missing"
		}
		return gaps[i].Symbol < gaps[j].Symbol
	})
	return gaps
}

var extensionMarkerRE = regexp.MustCompile(`\bExtensionAPI\b|\bExtensionContext\b|\bExtensionCommandContext\b|\b(?:pi|api)\s*\.\s*(?:on|events|exec|register[A-Z]\w*|send(?:User)?Message|appendEntry|setActiveTools|getActiveTools|getAllTools|setModel|setThinkingLevel|setSessionName)\b|\b(?:pi|api)\s*\[|\{[^{}]*\bevents\b[^{}]*\}\s*(?::[^=]*)?=\s*(?:pi|api)\b`)

// LooksLikeExtension reports whether TypeScript or JavaScript source uses the Pi extension API:
// the ExtensionAPI or ExtensionContext types, or a pi.* member an extension registers or calls.
// A standalone adapter, an embedder or a library does not, and the gap check does not apply to it.
func LooksLikeExtension(tsSource string) bool {
	return extensionMarkerRE.MatchString(stripComments(tsSource))
}

// PathLooksLikeExtension reports whether any TypeScript or JavaScript file under path (or path
// itself) looks like an extension.
func PathLooksLikeExtension(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		b, err := os.ReadFile(path)
		return err == nil && LooksLikeExtension(string(b)), err
	}
	found := false
	err = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); n == "node_modules" || n == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if found || !isTSSource(p) {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		found = LooksLikeExtension(string(b))
		return nil
	})
	return found, err
}

// NotAnExtensionNote is printed instead of a clean gap report when nothing scanned uses the Pi
// extension API: a clean report there would read as a pass.
func NotAnExtensionNote() string {
	return "NOT AN EXTENSION: this source is not a Pi extension (no ExtensionAPI, no pi.on/registerTool/registerCommand/events).\n" +
		"        The gap check does not apply. This is a standalone program, an embedder or a library: decide the kind of port (Skill: \"What kind of port is this?\", kind 4).\n"
}

// IndirectBusSymbol is the gap reported for pi.events reached other than as
// pi.events.on/emit: an alias (const bus = pi.events), destructuring
// (const { events } = pi) or an index (pi["events"]). The row patterns cannot
// see those, and the bus is the gap a scenario can never show.
const IndirectBusSymbol = "pi.events"

var indirectBusRE = []*regexp.Regexp{
	regexp.MustCompile(`\b(?:pi|api)\s*\.\s*events\b\s*(?:[^.\s]|$)`),              // const bus = pi.events
	regexp.MustCompile(`\{[^{}]*\bevents\b[^{}]*\}\s*(?::[^=]*)?=\s*(?:pi|api)\b`), // const { events } = pi
	regexp.MustCompile(`\b(?:pi|api)\s*\[\s*["'\x60]events["'\x60]\s*\]`),          // pi["events"]
}

func indirectBusGaps(source string, lines []string, surfaceMarkdown string, found []Gap) []Gap {
	busMissing := false
	for _, row := range parseSurface(surfaceMarkdown) {
		if strings.HasPrefix(row.symbol, "pi.events.") && strings.HasPrefix(row.goCell, "missing") {
			busMissing = true
		}
	}
	if !busMissing {
		return nil
	}
	g := Gap{Symbol: IndirectBusSymbol, Severity: "missing",
		Note: "pi.events reached through an alias, destructuring or an index: the Go SDK has no event bus (pi.events.on/emit are missing for Go)"}
	for i, l := range lines {
		for _, re := range indirectBusRE {
			if re.MatchString(l) {
				g.Lines = append(g.Lines, i+1)
				break
			}
		}
	}
	if len(g.Lines) == 0 {
		return nil
	}
	// A line already reported as pi.events.on/emit is not reported twice.
	reported := map[int]bool{}
	for _, f := range found {
		if strings.HasPrefix(f.Symbol, "pi.events.") {
			for _, l := range f.Lines {
				reported[l] = true
			}
		}
	}
	var keep []int
	for _, l := range g.Lines {
		if !reported[l] {
			keep = append(keep, l)
		}
	}
	if len(keep) == 0 {
		return nil
	}
	g.Lines = keep
	return []Gap{g}
}

// ScanGapsPath scans a TypeScript file, or every TypeScript and JavaScript
// source under a directory (node_modules excluded), so a multi-file extension
// is covered. Gaps found in a directory carry their file.
func ScanGapsPath(path, surfaceMarkdown string) ([]Gap, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return ScanGaps(string(b), surfaceMarkdown), nil
	}
	var gaps []Gap
	err = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); n == "node_modules" || n == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if !isTSSource(p) {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(path, p)
		for _, g := range ScanGaps(string(b), surfaceMarkdown) {
			g.File = filepath.ToSlash(rel)
			gaps = append(gaps, g)
		}
		return nil
	})
	sort.SliceStable(gaps, func(i, j int) bool {
		if gaps[i].Severity != gaps[j].Severity {
			return gaps[i].Severity == "missing"
		}
		return gaps[i].Symbol < gaps[j].Symbol
	})
	return gaps, err
}

// Unaccepted returns the blocking gaps that have no owner approval.
func Unaccepted(gaps []Gap, accepted map[string]string) []Gap {
	var out []Gap
	for _, g := range gaps {
		if _, ok := accepted[g.Symbol]; !ok && g.Severity == "missing" {
			out = append(out, g)
		}
	}
	return out
}

func noteOf(cell string) string {
	cell = strings.ReplaceAll(cell, "[exception](#exceptions)", "")
	cell = strings.Join(strings.Fields(cell), " ")
	if cell == "missing" {
		cell = "missing: the Go SDK has no symbol for it (reviewed exception in PiG's sdk-surface-exceptions.toml)"
	}
	return cell
}

// stripComments blanks // and /* */ comments and keeps line numbers. String
// literals are respected well enough for this purpose.
func stripComments(src string) string {
	var out strings.Builder
	var quote byte
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case quote != 0:
			out.WriteByte(c)
			if c == '\\' && i+1 < len(src) {
				i++
				out.WriteByte(src[i])
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'' || c == '`':
			quote = c
			out.WriteByte(c)
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
			i--
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			i += 2
			for i+1 < len(src) && !(src[i] == '*' && src[i+1] == '/') {
				if src[i] == '\n' {
					out.WriteByte('\n')
				}
				i++
			}
			i++
		default:
			out.WriteByte(c)
		}
	}
	return out.String()
}

// FormatGaps renders a gap report for a human or an agent.
func FormatGaps(gaps []Gap) string {
	if len(gaps) == 0 {
		return "no Go SDK gaps found for the APIs this source uses\n"
	}
	var b strings.Builder
	blocking := 0
	for _, g := range gaps {
		if g.Severity == "missing" {
			blocking++
		}
		more := ""
		if g.Related > 0 {
			more = fmt.Sprintf(" and %d related surface row(s)", g.Related)
		}
		where := "lines"
		if g.File != "" {
			where = g.File + " lines"
		}
		fmt.Fprintf(&b, "%-7s %s%s (%s %v)\n        %s\n", strings.ToUpper(g.Severity), g.Symbol, more, where, g.Lines, g.Note)
	}
	fmt.Fprintf(&b, "%d gap(s), %d blocking. A blocking gap needs a PiG SDK feature or an owner-approved exclusion; never a private import or a silent no-op.\n", len(gaps), blocking)
	return b.String()
}

// Blocking reports whether any gap is missing rather than partial.
func Blocking(gaps []Gap) bool {
	for _, g := range gaps {
		if g.Severity == "missing" {
			return true
		}
	}
	return false
}
