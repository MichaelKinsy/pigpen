package lsp

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// upstream: packages/coding-agent/src/lsp/utils.ts, packages/tui/src/lang-from-path.ts,
//           packages/tui/src/tools/output-meta.ts, packages/tui/src/tools/grouped-file-output.ts

// =============================================================================
// Path & URI Handling (Cross-Platform)
// =============================================================================

func resolveToCwd(filePath, cwd string) string {
	if filePath == "" {
		return cwd
	}
	if strings.HasPrefix(filePath, "~/") || filePath == "~" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			if filePath == "~" {
				filePath = home
			} else {
				filePath = filepath.Join(home, filePath[2:])
			}
		}
	}
	if filepath.IsAbs(filePath) {
		return filepath.Clean(filePath)
	}
	return filepath.Clean(filepath.Join(cwd, filePath))
}

func formatPathRelativeToCwd(filePath, cwd string) string {
	if cwd != "" {
		if rel, err := filepath.Rel(cwd, filePath); err == nil && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(filePath)
}

func shortenPath(filePath string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		cleanHome := filepath.Clean(home)
		cleanPath := filepath.Clean(filePath)
		if cleanPath == cleanHome {
			return "~"
		}
		if strings.HasPrefix(cleanPath, cleanHome+string(filepath.Separator)) {
			return "~/" + filepath.ToSlash(cleanPath[len(cleanHome)+1:])
		}
	}
	return filepath.ToSlash(filePath)
}

// fileToUri converts an OS file path to a file:// URI with percent-encoding.
// upstream: packages/coding-agent/src/lsp/utils.ts:fileToUri
func fileToUri(filePath string) string {
	abs, err := filepath.Abs(filePath)
	if err != nil {
		abs = filePath
	}
	slashed := filepath.ToSlash(abs)
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	u := &url.URL{Scheme: "file", Path: slashed}
	return u.String()
}

// uriToFile converts a file:// URI to a local file path, tolerating both
// percent-encoded URIs and lax servers that send raw `#`/`?`.
// upstream: packages/coding-agent/src/lsp/utils.ts:uriToFile
func uriToFile(uri string) string {
	if !strings.HasPrefix(uri, "file://") {
		return uri
	}
	if strings.ContainsAny(uri, "#?") {
		return laxUriToFile(uri)
	}
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return laxUriToFile(uri)
	}
	p := u.Path
	if u.Host != "" && u.Host != "localhost" {
		if runtime.GOOS == "windows" {
			return filepath.FromSlash("//" + u.Host + p)
		}
		return filepath.FromSlash("/" + u.Host + p)
	}
	if runtime.GOOS == "windows" && len(p) >= 3 && p[0] == '/' && isASCIIAlpha(p[1]) && p[2] == ':' {
		p = p[1:]
	}
	return filepath.FromSlash(p)
}

func laxUriToFile(uri string) string {
	p := strings.TrimPrefix(uri, "file://")
	if decoded, err := url.PathUnescape(p); err == nil {
		p = decoded
	}
	if runtime.GOOS == "windows" && len(p) >= 3 && p[0] == '/' && isASCIIAlpha(p[1]) && p[2] == ':' {
		p = p[1:]
	}
	return filepath.FromSlash(p)
}

func isASCIIAlpha(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

// canonicalURIKey normalizes equivalent file:// URI spellings into one map key
// (matching EquivalentUriMap in utils.ts).
func canonicalURIKey(uri string) string {
	if !strings.HasPrefix(uri, "file://") {
		return uri
	}
	p := filepath.Clean(uriToFile(uri))
	if runtime.GOOS == "windows" {
		return strings.ToLower(p)
	}
	return p
}

// readTextFromDisk reads a regular file, refusing FIFOs, devices, and sockets.
// upstream: packages/coding-agent/src/lsp/utils.ts:readTextFromDisk
func readTextFromDisk(filePath string) (string, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return "", err
	}
	if kind := specialFileKind(info); kind != "" {
		return "", fmt.Errorf("Cannot open '%s': it is a %s, not a regular file or directory.", filePath, kind)
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// =============================================================================
// Language ID Detection
// =============================================================================

// upstream: packages/tui/src/lang-from-path.ts:EXTENSION_LANG + detectLanguageId
var extensionLanguageID = map[string]string{
	"ts": "typescript", "cts": "typescript", "mts": "typescript",
	"tsx": "typescriptreact",
	"js":  "javascript", "mjs": "javascript", "cjs": "javascript",
	"jsx": "javascriptreact",
	"rs":  "rust", "go": "go",
	"c": "c", "h": "c",
	"cpp": "cpp", "cc": "cpp", "cxx": "cpp", "hh": "cpp", "hpp": "cpp", "hxx": "cpp",
	"inl": "cpp", "ipp": "cpp", "tpp": "cpp", "cppm": "cpp", "ixx": "cpp",
	"cu": "cpp", "cuh": "cpp", "ino": "cpp",
	"zig": "zig",
	"py":  "python", "pyi": "python",
	"rb": "ruby", "rbw": "ruby", "gemspec": "ruby", "erb": "erb",
	"lua": "lua",
	"sh":  "shellscript", "bash": "shellscript", "zsh": "shellscript", "ksh": "shellscript",
	"bats": "shellscript", "tmux": "shellscript", "cgi": "shellscript", "fcgi": "shellscript",
	"command": "shellscript", "tool": "shellscript",
	"fish": "fish",
	"pl":   "perl", "pm": "perl", "perl": "perl",
	"php":  "php",
	"java": "java",
	"kt":   "kotlin", "ktm": "kotlin", "kts": "kotlin",
	"scala": "scala", "sc": "scala", "sbt": "scala",
	"groovy": "groovy",
	"clj":    "clojure", "cljc": "clojure", "cljs": "clojure", "edn": "clojure",
	"el": "emacs-lisp",
	"cs": "csharp", "fs": "fsharp", "vb": "vb",
	"html": "html", "htm": "html", "xhtml": "html",
	"css": "css", "scss": "scss", "sass": "sass", "less": "less",
	"vue": "vue", "svelte": "svelte", "astro": "astro",
	"json": "json", "jsonc": "jsonc",
	"yaml": "yaml", "yml": "yaml",
	"toml": "toml",
	"xml":  "xml", "xsl": "xml", "xslt": "xml", "svg": "xml", "plist": "xml",
	"ini": "ini",
	"md":  "markdown", "markdown": "markdown", "mdx": "markdown", "mdc": "markdown", "mkd": "markdown", "mdown": "markdown",
	"rst": "restructuredtext", "adoc": "asciidoc", "tex": "latex",
	"sql": "sql", "graphql": "graphql", "gql": "graphql", "proto": "protobuf",
	"dockerfile": "dockerfile", "containerfile": "dockerfile",
	"tf": "terraform", "hcl": "hcl", "tfvars": "hcl",
	"nix": "nix",
	"ex":  "elixir", "exs": "elixir",
	"erl": "erlang", "hrl": "erlang",
	"hs": "haskell", "ml": "ocaml", "mli": "ocaml",
	"swift": "swift", "r": "r", "jl": "julia", "dart": "dart", "elm": "elm",
	"v": "v", "nim": "nim", "cr": "crystal", "d": "d", "pas": "pascal", "pp": "pascal",
	"lisp": "lisp", "lsp": "lisp", "rkt": "racket", "scm": "scheme",
	"ps1": "powershell", "psm1": "powershell", "bat": "bat", "cmd": "bat",
	"tla": "tlaplus", "tlaplus": "tlaplus",
	"cmake": "cmake",
}

// detectLanguageId maps a file path to its LSP languageId, falling back to "plaintext".
// upstream: packages/tui/src/lang-from-path.ts:detectLanguageId
func detectLanguageId(filePath string) string {
	base := strings.ToLower(filepath.Base(filePath))
	if base == "dockerfile" || strings.HasPrefix(base, "dockerfile.") || base == "containerfile" {
		return "dockerfile"
	}
	if base == ".emacs" {
		return "emacs-lisp"
	}
	if base == "makefile" || base == "gnumakefile" {
		return "makefile"
	}
	if base == "justfile" {
		return "just"
	}
	ext := strings.ToLower(filepath.Ext(filePath))
	ext = strings.TrimPrefix(ext, ".")
	if base == "cmakelists.txt" || ext == "cmake" {
		return "cmake"
	}
	if id, ok := extensionLanguageID[ext]; ok {
		return id
	}
	return "plaintext"
}

// =============================================================================
// Diagnostic Formatting & Grouping
// =============================================================================

func severityToString(sev DiagnosticSeverity) string {
	switch sev {
	case 0, SeverityError:
		return "error"
	case SeverityWarning:
		return "warning"
	case SeverityInfo:
		return "info"
	case SeverityHint:
		return "hint"
	default:
		return "unknown"
	}
}

func sortDiagnostics(diags []Diagnostic) {
	sort.SliceStable(diags, func(i, j int) bool {
		a, b := diags[i], diags[j]
		as, bs := a.Severity, b.Severity
		if as == 0 {
			as = SeverityError
		}
		if bs == 0 {
			bs = SeverityError
		}
		if as != bs {
			return as < bs
		}
		if a.Range.Start.Line != b.Range.Start.Line {
			return a.Range.Start.Line < b.Range.Start.Line
		}
		if a.Range.Start.Character != b.Range.Start.Character {
			return a.Range.Start.Character < b.Range.Start.Character
		}
		return a.Message < b.Message
	})
}

func diagnosticFingerprint(d Diagnostic) string {
	return fmt.Sprintf("%d:%d:%d:%d:%s:%s",
		d.Range.Start.Line, d.Range.Start.Character,
		d.Range.End.Line, d.Range.End.Character,
		d.Source, d.Message)
}

func mergeDiagnosticLists(primary, secondary []Diagnostic) []Diagnostic {
	if len(primary) == 0 {
		return append([]Diagnostic(nil), secondary...)
	}
	if len(secondary) == 0 {
		return append([]Diagnostic(nil), primary...)
	}
	seen := make(map[string]struct{}, len(primary)+len(secondary))
	merged := make([]Diagnostic, 0, len(primary)+len(secondary))
	for _, d := range primary {
		k := diagnosticFingerprint(d)
		if _, ok := seen[k]; !ok {
			seen[k] = struct{}{}
			merged = append(merged, d)
		}
	}
	for _, d := range secondary {
		k := diagnosticFingerprint(d)
		if _, ok := seen[k]; !ok {
			seen[k] = struct{}{}
			merged = append(merged, d)
		}
	}
	return merged
}

func deduplicateDiagnostics(diags []Diagnostic) []Diagnostic {
	seen := make(map[string]struct{}, len(diags))
	unique := make([]Diagnostic, 0, len(diags))
	for _, d := range diags {
		key := fmt.Sprintf("%d:%d:%d:%d:%s",
			d.Range.Start.Line, d.Range.Start.Character,
			d.Range.End.Line, d.Range.End.Character,
			d.Message)
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			unique = append(unique, d)
		}
	}
	return unique
}

var bareURLRe = regexp.MustCompile(`^https?://`)

func stripDiagnosticNoise(message string) string {
	lines := strings.Split(message, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "for further information visit") {
			continue
		}
		if bareURLRe.MatchString(trimmed) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func formatDiagnosticCode(code any) string {
	if code == nil {
		return ""
	}
	switch v := code.(type) {
	case string:
		if v == "" {
			return ""
		}
		return " (" + v + ")"
	case float64:
		if v == float64(int64(v)) {
			return fmt.Sprintf(" (%d)", int64(v))
		}
		return fmt.Sprintf(" (%v)", v)
	case int:
		if v == 0 {
			return ""
		}
		return fmt.Sprintf(" (%d)", v)
	default:
		s := fmt.Sprint(v)
		if s == "" || s == "0" {
			return ""
		}
		return " (" + s + ")"
	}
}

// formatDiagnostic matches packages/coding-agent/src/lsp/utils.ts:formatDiagnostic.
func formatDiagnostic(d Diagnostic, filePath string) string {
	sev := severityToString(d.Severity)
	line := d.Range.Start.Line + 1
	col := d.Range.Start.Character + 1
	source := ""
	if d.Source != "" {
		source = "[" + d.Source + "] "
	}
	code := formatDiagnosticCode(d.Code)
	msg := stripDiagnosticNoise(d.Message)
	return fmt.Sprintf("%s:%d:%d [%s] %s%s%s", filePath, line, col, sev, source, msg, code)
}

func formatDiagnosticsSummary(diags []Diagnostic) string {
	var errs, warns, infos, hints int
	for _, d := range diags {
		switch severityToString(d.Severity) {
		case "error":
			errs++
		case "warning":
			warns++
		case "info":
			infos++
		case "hint":
			hints++
		}
	}
	return formatCountsSummary(errs, warns, infos, hints)
}

var diagSeverityTagRe = regexp.MustCompile(`(?i)\[(error|warning|info|hint)\]`)

func summarizeDiagnosticMessages(messages []string) (summary string, errored bool) {
	var errs, warns, infos, hints int
	for _, msg := range messages {
		m := diagSeverityTagRe.FindStringSubmatch(msg)
		if len(m) < 2 {
			continue
		}
		switch strings.ToLower(m[1]) {
		case "error":
			errs++
		case "warning":
			warns++
		case "info":
			infos++
		case "hint":
			hints++
		}
	}
	return formatCountsSummary(errs, warns, infos, hints), errs > 0
}

func formatCountsSummary(errs, warns, infos, hints int) string {
	var parts []string
	if errs > 0 {
		parts = append(parts, fmt.Sprintf("%d error(s)", errs))
	}
	if warns > 0 {
		parts = append(parts, fmt.Sprintf("%d warning(s)", warns))
	}
	if infos > 0 {
		parts = append(parts, fmt.Sprintf("%d info(s)", infos))
	}
	if hints > 0 {
		parts = append(parts, fmt.Sprintf("%d hint(s)", hints))
	}
	if len(parts) == 0 {
		return "no issues"
	}
	return strings.Join(parts, ", ")
}

// =============================================================================
// Grouped Diagnostic Formatting (prefix-folded `# dir/` / `## file`)
// upstream: packages/tui/src/tools/output-meta.ts:formatGroupedDiagnosticMessages
// =============================================================================

var diagPathRe = regexp.MustCompile(`^(.+?):(\d+:\d+\s+.*)$`)

type diagTreeFile struct {
	name string
	key  string
}

type diagTreeSubdir struct {
	name string
	node *diagTreeNode
}

type diagTreeNode struct {
	files     []diagTreeFile
	fileNames map[string]struct{}
	subdirs   []diagTreeSubdir
	dirIndex  map[string]*diagTreeNode
}

func newDiagTreeNode() *diagTreeNode {
	return &diagTreeNode{
		fileNames: make(map[string]struct{}),
		dirIndex:  make(map[string]*diagTreeNode),
	}
}

func (n *diagTreeNode) addFile(name, key string) {
	if _, ok := n.fileNames[name]; ok {
		return
	}
	n.fileNames[name] = struct{}{}
	n.files = append(n.files, diagTreeFile{name: name, key: key})
}

func buildDiagPathTree(files []string) *diagTreeNode {
	root := newDiagTreeNode()
	for _, fp := range files {
		normalized := strings.ReplaceAll(fp, "\\", "/")
		trimmed := strings.TrimSuffix(normalized, "/")
		if trimmed == "" {
			continue
		}
		segs := strings.Split(trimmed, "/")
		node := root
		for i := range len(segs) - 1 {
			seg := segs[i]
			child, ok := node.dirIndex[seg]
			if !ok {
				child = newDiagTreeNode()
				node.dirIndex[seg] = child
				node.subdirs = append(node.subdirs, diagTreeSubdir{name: seg, node: child})
			}
			node = child
		}
		node.addFile(segs[len(segs)-1], fp)
	}
	return root
}

func walkDiagPathTree(node *diagTreeNode, depth int, emit func(kind string, depth int, name, key string)) {
	for _, f := range node.files {
		emit("file", depth, f.name, f.key)
	}
	for _, sub := range node.subdirs {
		dirNode := sub.node
		parts := []string{sub.name}
		for len(dirNode.files) == 0 && len(dirNode.subdirs) == 1 {
			only := dirNode.subdirs[0]
			parts = append(parts, only.name)
			dirNode = only.node
		}
		emit("dir", depth, strings.Join(parts, "/"), "")
		walkDiagPathTree(dirNode, depth+1, emit)
	}
}

// formatGroupedDiagnosticMessages formats `path:line:col [sev] msg` entries into
// omp's prefix-folded directory/file tree.
func formatGroupedDiagnosticMessages(messages []string) string {
	byFile := make(map[string][]string)
	var fileOrder []string
	var ungrouped []string
	for _, msg := range messages {
		m := diagPathRe.FindStringSubmatch(msg)
		if len(m) < 3 {
			ungrouped = append(ungrouped, msg)
			continue
		}
		fp := strings.ReplaceAll(m[1], "\\", "/")
		if _, ok := byFile[fp]; !ok {
			fileOrder = append(fileOrder, fp)
		}
		byFile[fp] = append(byFile[fp], m[2])
	}
	if len(byFile) == 0 {
		return strings.Join(ungrouped, "\n")
	}
	tree := buildDiagPathTree(fileOrder)
	var lines []string
	emitted := false
	walkDiagPathTree(tree, 0, func(kind string, depth int, name, key string) {
		hashes := strings.Repeat("#", depth+1)
		if emitted && (depth == 0 || kind == "dir") {
			lines = append(lines, "")
		}
		emitted = true
		if kind == "dir" {
			lines = append(lines, hashes+" "+name+"/")
			return
		}
		lines = append(lines, hashes+" "+name)
		for _, diag := range byFile[key] {
			lines = append(lines, "  "+diag)
		}
	})
	if len(ungrouped) > 0 {
		lines = append(lines, "")
		lines = append(lines, ungrouped...)
	}
	return strings.Join(lines, "\n")
}

// =============================================================================
// Locations, WorkspaceEdit, Symbols, Hover, CodeActions Formatting
// =============================================================================

func formatLocation(loc Location, cwd string) string {
	file := formatPathRelativeToCwd(uriToFile(loc.URI), cwd)
	return fmt.Sprintf("%s:%d:%d", file, loc.Range.Start.Line+1, loc.Range.Start.Character+1)
}

func formatWorkspaceEdit(edit *WorkspaceEdit, cwd string) []string {
	if edit == nil {
		return nil
	}
	var results []string
	if len(edit.Changes) > 0 {
		for _, uri := range edit.orderedChangesURIs() {
			edits := edit.Changes[uri]
			file := formatPathRelativeToCwd(uriToFile(uri), cwd)
			plural := ""
			if len(edits) > 1 {
				plural = "s"
			}
			results = append(results, fmt.Sprintf("%s: %d edit%s", file, len(edits), plural))
		}
	}
	for _, change := range edit.DocumentChanges {
		if change.TextDocument != nil && len(change.Edits) > 0 {
			file := formatPathRelativeToCwd(uriToFile(change.TextDocument.URI), cwd)
			plural := ""
			if len(change.Edits) > 1 {
				plural = "s"
			}
			results = append(results, fmt.Sprintf("%s: %d edit%s", file, len(change.Edits), plural))
		} else if change.Kind != "" {
			switch change.Kind {
			case "create":
				results = append(results, "CREATE: "+formatPathRelativeToCwd(uriToFile(change.URI), cwd))
			case "rename":
				results = append(results, fmt.Sprintf("RENAME: %s → %s",
					formatPathRelativeToCwd(uriToFile(change.OldURI), cwd),
					formatPathRelativeToCwd(uriToFile(change.NewURI), cwd)))
			case "delete":
				results = append(results, "DELETE: "+formatPathRelativeToCwd(uriToFile(change.URI), cwd))
			}
		}
	}
	return results
}

// symbolKindIcons holds the symbol-kind glyph table for a symbol preset.
// upstream: packages/coding-agent/src/lsp/utils.ts:359-408, packages/tui/src/theme/symbols.ts:477-603,846-1079,1284-1408
type symbolKindGlyphs struct {
	bullet string
	file   string
	folder string
	pkg    string
	model  string
	fn     string
}

func symbolGlyphsForPreset(preset string) symbolKindGlyphs {
	switch strings.ToLower(strings.TrimSpace(preset)) {
	case "nerd":
		return symbolKindGlyphs{
			bullet: "\uf111",
			file:   "\uf15b",
			folder: "\uf115",
			pkg:    "\uf487",
			model:  "\uec19",
			fn:     "\U000f0068",
		}
	case "ascii":
		return symbolKindGlyphs{
			bullet: "*",
			file:   "[F]",
			folder: "[D]",
			pkg:    "[P]",
			model:  "[M]",
			fn:     "[A]",
		}
	default: // "unicode" (omp default, packages/coding-agent/src/modes/settings.ts:92-96)
		return symbolKindGlyphs{
			bullet: "•",
			file:   "📄",
			folder: "📁",
			pkg:    "📦",
			model:  "⬢",
			fn:     "⟲",
		}
	}
}

// getSymbolKindIcons returns omp's SymbolKind -> icon map for the given preset.
// upstream: packages/coding-agent/src/lsp/utils.ts:359-399
func getSymbolKindIcons(preset string) map[SymbolKind]string {
	g := symbolGlyphsForPreset(preset)
	return map[SymbolKind]string{
		1:  g.file,   // File
		2:  g.folder, // Module
		3:  g.folder, // Namespace
		4:  g.pkg,    // Package
		5:  g.model,  // Class
		6:  g.fn,     // Method
		7:  g.bullet, // Property
		8:  g.bullet, // Field
		9:  g.fn,     // Constructor
		10: g.bullet, // Enum
		11: g.model,  // Interface
		12: g.fn,     // Function
		13: g.bullet, // Variable
		14: g.bullet, // Constant
		15: g.bullet, // String
		16: g.bullet, // Number
		17: g.bullet, // Boolean
		18: g.bullet, // Array
		19: g.bullet, // Object
		20: g.bullet, // Key
		21: g.bullet, // Null
		22: g.bullet, // EnumMember
		23: g.folder, // Struct
		24: g.bullet, // Event
		25: g.bullet, // Operator
		26: g.bullet, // TypeParameter
	}
}

// symbolKindToIcon returns the icon for a symbol kind using the given symbol preset.
// upstream: packages/coding-agent/src/lsp/utils.ts:404-408
func symbolKindToIcon(kind SymbolKind, preset string) string {
	icons := getSymbolKindIcons(preset)
	if icon, ok := icons[kind]; ok {
		return icon
	}
	return symbolGlyphsForPreset(preset).bullet
}

func formatDocumentSymbol(sym DocumentSymbol, indent int, preset string) []string {
	prefix := strings.Repeat("  ", indent)
	icon := symbolKindToIcon(sym.Kind, preset)
	line := sym.SelectionRange.Start.Line + 1
	detail := ""
	if sym.Detail != "" {
		detail = " " + sym.Detail
	}
	out := []string{fmt.Sprintf("%s%s %s%s @ line %d", prefix, icon, sym.Name, detail, line)}
	for _, child := range sym.Children {
		out = append(out, formatDocumentSymbol(child, indent+1, preset)...)
	}
	return out
}

func formatMatchingDocumentSymbols(symbols []DocumentSymbol, query string, preset string) []string {
	needle := strings.ToLower(strings.TrimSpace(query))
	var lines []string
	var visit func(sym DocumentSymbol, containers []string)
	visit = func(sym DocumentSymbol, containers []string) {
		if strings.Contains(strings.ToLower(sym.Name), needle) {
			formatted := formatDocumentSymbol(sym, 0, preset)
			if len(formatted) > 0 {
				head := formatted[0]
				if len(containers) > 0 {
					head = fmt.Sprintf("%s (in %s)", head, strings.Join(containers, "."))
				}
				lines = append(lines, head)
				lines = append(lines, formatted[1:]...)
			}
			return
		}
		next := append(append([]string(nil), containers...), sym.Name)
		for _, child := range sym.Children {
			visit(child, next)
		}
	}
	for _, sym := range symbols {
		visit(sym, nil)
	}
	return lines
}

func formatSymbolInformation(sym SymbolInformation, cwd string, preset string) string {
	icon := symbolKindToIcon(sym.Kind, preset)
	loc := formatLocation(sym.Location, cwd)
	container := ""
	if sym.ContainerName != "" {
		container = " (" + sym.ContainerName + ")"
	}
	return fmt.Sprintf("%s %s%s @ %s", icon, sym.Name, container, loc)
}

func filterWorkspaceSymbols(symbols []SymbolInformation, query string) []SymbolInformation {
	needle := strings.ToLower(strings.TrimSpace(query))
	if needle == "" {
		return symbols
	}
	out := make([]SymbolInformation, 0, len(symbols))
	for _, sym := range symbols {
		if strings.Contains(strings.ToLower(sym.Name), needle) ||
			strings.Contains(strings.ToLower(sym.ContainerName), needle) ||
			strings.Contains(strings.ToLower(uriToFile(sym.Location.URI)), needle) {
			out = append(out, sym)
		}
	}
	return out
}

func dedupeWorkspaceSymbols(symbols []SymbolInformation) []SymbolInformation {
	seen := make(map[string]struct{}, len(symbols))
	out := make([]SymbolInformation, 0, len(symbols))
	for _, sym := range symbols {
		key := fmt.Sprintf("%s:%s:%d:%s:%d:%d",
			sym.Name, sym.ContainerName, sym.Kind,
			sym.Location.URI, sym.Location.Range.Start.Line, sym.Location.Range.Start.Character)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, sym)
	}
	return out
}

func formatCodeAction(action CodeActionOrCommand, index int) string {
	kind := action.Kind
	if kind == "" {
		kind = "action"
	}
	preferred := ""
	if action.IsPreferred {
		preferred = " (preferred)"
	}
	disabled := ""
	if action.Disabled != nil {
		disabled = fmt.Sprintf(" (disabled: %s)", action.Disabled.Reason)
	}
	return fmt.Sprintf("%d: [%s] %s%s%s", index, kind, action.Title, preferred, disabled)
}

func mergeCodeActions(base, extra []CodeActionOrCommand) []CodeActionOrCommand {
	merged := append([]CodeActionOrCommand(nil), base...)
	seen := make(map[string]struct{}, len(merged))
	for _, a := range merged {
		seen[string(a.Raw)] = struct{}{}
	}
	for _, a := range extra {
		if _, ok := seen[string(a.Raw)]; !ok {
			merged = append(merged, a)
		}
	}
	return merged
}

// extractHoverText extracts plain text from LSP Hover.contents (string, MarkedString,
// MarkupContent, or array of MarkedString).
// upstream: packages/coding-agent/src/lsp/utils.ts:extractHoverText
func extractHoverText(raw []byte) string {
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err == nil {
		parts := make([]string, 0, len(arr))
		for _, item := range arr {
			parts = append(parts, extractHoverText(item))
		}
		return strings.Join(parts, "\n\n")
	}
	var obj struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return obj.Value
	}
	return string(raw)
}

// =============================================================================
// Symbol Column Resolution & Location Context
// =============================================================================

var (
	bareIdentifierRe = regexp.MustCompile(`^[$A-Za-z_][\w$]*$`)
	symbolSpecRe     = regexp.MustCompile(`^(.+)#(\d+)$`)
)

func isIdentifierByte(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == '_' || b == '$'
}

// parseSymbolSpec parses `name` or `name#N` (1-indexed occurrence).
// upstream: packages/coding-agent/src/lsp/utils.ts:parseSymbolSpec
func parseSymbolSpec(spec string) (symbol string, occurrence int) {
	m := symbolSpecRe.FindStringSubmatch(spec)
	if len(m) != 3 {
		return spec, 1
	}
	n, err := strconv.Atoi(m[2])
	if err != nil || n < 1 {
		n = 1
	}
	return m[1], n
}

// byteOffsetToUTF16 converts a UTF-8 byte offset in lineText into UTF-16 code units
// as required by LSP Position.character (matching JS string indexes in lsp/utils.ts).
func byteOffsetToUTF16(lineText string, byteOffset int) int {
	if byteOffset <= 0 {
		return 0
	}
	if byteOffset > len(lineText) {
		byteOffset = len(lineText)
	}
	units := 0
	for _, r := range lineText[:byteOffset] {
		if r > 0xFFFF {
			units += 2
		} else {
			units++
		}
	}
	return units
}

func firstNonWhitespaceColumn(lineText string) int {
	for i, r := range lineText {
		if r != ' ' && r != '\t' && r != '\r' && r != '\u00a0' {
			return byteOffsetToUTF16(lineText, i)
		}
	}
	return 0
}

// findSymbolMatchIndexes returns 0-based UTF-16 code-unit columns of symbol
// matches in lineText (matching JS string.indexOf in lsp/utils.ts:721-745).
func findSymbolMatchIndexes(lineText, symbol string, caseInsensitive bool) []int {
	if symbol == "" {
		return nil
	}
	haystack := lineText
	needle := symbol
	if caseInsensitive {
		haystack = strings.ToLower(haystack)
		needle = strings.ToLower(needle)
	}
	requireWordBoundary := bareIdentifierRe.MatchString(symbol)
	var indexes []int
	from := 0
	for from <= len(haystack)-len(needle) {
		idx := strings.Index(haystack[from:], needle)
		if idx == -1 {
			break
		}
		matchIdx := from + idx
		if requireWordBoundary {
			beforeBad := matchIdx > 0 && isIdentifierByte(haystack[matchIdx-1])
			afterIdx := matchIdx + len(needle)
			afterBad := afterIdx < len(haystack) && isIdentifierByte(haystack[afterIdx])
			if beforeBad || afterBad {
				from = matchIdx + 1
				continue
			}
		}
		indexes = append(indexes, byteOffsetToUTF16(haystack, matchIdx))
		from = matchIdx + len(needle)
	}
	return indexes
}

// resolveSymbolColumn resolves the 0-based column of symbolSpec on 1-indexed line in filePath.
// upstream: packages/coding-agent/src/lsp/utils.ts:resolveSymbolColumn
func resolveSymbolColumn(filePath string, line int, symbolSpec string) (int, error) {
	lineNumber := line
	if lineNumber < 1 {
		lineNumber = 1
	}
	text, err := readTextFromDisk(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, fmt.Errorf("File not found: %s", filePath)
		}
		return 0, err
	}
	lines := strings.Split(text, "\n")
	targetLine := ""
	if lineNumber-1 < len(lines) {
		targetLine = lines[lineNumber-1]
	}
	if symbolSpec == "" {
		return firstNonWhitespaceColumn(targetLine), nil
	}
	symbol, occurrence := parseSymbolSpec(symbolSpec)
	matches := findSymbolMatchIndexes(targetLine, symbol, false)
	if len(matches) == 0 {
		matches = findSymbolMatchIndexes(targetLine, symbol, true)
	}
	if len(matches) == 0 {
		return 0, fmt.Errorf(`Symbol "%s" not found on line %d`, symbol, lineNumber)
	}
	if occurrence > len(matches) {
		return 0, fmt.Errorf(`Symbol "%s" occurrence %d is out of bounds on line %d (found %d)`,
			symbol, occurrence, lineNumber, len(matches))
	}
	return matches[occurrence-1], nil
}

// findSymbolInFile finds the 1-indexed line and 0-indexed character of symbolSpec
// when no line was supplied.
// upstream: packages/coding-agent/src/lsp/utils.ts:findSymbolInFile
func findSymbolInFile(filePath, symbolSpec string) (line int, character int, err error) {
	symbol, occurrence := parseSymbolSpec(symbolSpec)
	text, err := readTextFromDisk(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, fmt.Errorf("File not found: %s", filePath)
		}
		return 0, 0, err
	}
	lines := strings.Split(text, "\n")
	for _, ci := range []bool{false, true} {
		seen := 0
		for i, l := range lines {
			for _, col := range findSymbolMatchIndexes(l, symbol, ci) {
				seen++
				if seen == occurrence {
					return i + 1, col, nil
				}
			}
		}
		if seen > 0 {
			return 0, 0, fmt.Errorf(`Symbol "%s" occurrence %d is out of bounds in %s (found %d)`,
				symbol, occurrence, filePath, seen)
		}
	}
	return 0, 0, fmt.Errorf(`Symbol "%s" not found in %s`, symbol, filePath)
}

// readLocationContext reads surrounding source lines around 1-indexed line,
// stripping CRLF line endings.
// upstream: packages/coding-agent/src/lsp/utils.ts:readLocationContext
func readLocationContext(filePath string, line, contextLines int) ([]string, error) {
	if line < 1 {
		line = 1
	}
	if contextLines < 0 {
		contextLines = 0
	}
	info, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if specialFileKind(info) != "" || info.IsDir() {
		return nil, nil
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) == 0 {
		return nil, nil
	}
	startLine := line - contextLines
	if startLine < 1 {
		startLine = 1
	}
	endLine := line + contextLines
	if endLine > len(lines) {
		endLine = len(lines)
	}
	out := make([]string, 0, endLine-startLine+1)
	for cur := startLine; cur <= endLine; cur++ {
		out = append(out, fmt.Sprintf("%d: %s", cur, lines[cur-1]))
	}
	return out, nil
}

// =============================================================================
// Glob & Diagnostic Target Resolution
// =============================================================================

var globPatternCharsRe = regexp.MustCompile(`[*?\[{]`)

func hasGlobPattern(value string) bool {
	return globPatternCharsRe.MatchString(value)
}

// resolveDiagnosticTargets resolves a path, glob, or `;`-separated list of targets.
// upstream: packages/coding-agent/src/lsp/utils.ts:resolveDiagnosticTargets
func resolveDiagnosticTargets(file, cwd string, maxMatches int) (matches []string, truncated bool, err error) {
	var entries []string
	for _, raw := range strings.Split(file, ";") {
		trimmed := strings.TrimSpace(raw)
		if trimmed != "" {
			entries = append(entries, trimmed)
		}
	}
	if len(entries) > 1 {
		if _, statErr := os.Stat(resolveToCwd(file, cwd)); os.IsNotExist(statErr) {
			seen := make(map[string]struct{})
			var out []string
			for _, entry := range entries {
				if len(out) >= maxMatches {
					return out, true, nil
				}
				sub, subTrunc, subErr := resolveDiagnosticTarget(entry, cwd, maxMatches-len(out))
				if subErr != nil {
					return nil, false, subErr
				}
				for _, m := range sub {
					if _, ok := seen[m]; !ok {
						seen[m] = struct{}{}
						out = append(out, m)
					}
				}
				if subTrunc {
					return out, true, nil
				}
			}
			return out, false, nil
		}
	}
	return resolveDiagnosticTarget(file, cwd, maxMatches)
}

func resolveDiagnosticTarget(file, cwd string, maxMatches int) ([]string, bool, error) {
	if !hasGlobPattern(file) {
		return []string{file}, false, nil
	}
	resolved := resolveToCwd(file, cwd)
	if info, err := os.Stat(resolved); err == nil && !info.IsDir() {
		return []string{file}, false, nil
	}
	return collectGlobMatches(file, cwd, maxMatches)
}

func collectGlobMatches(pattern, cwd string, maxMatches int) ([]string, bool, error) {
	if maxMatches < 1 {
		maxMatches = 1
	}
	normPat := filepath.ToSlash(pattern)
	var matches []string
	truncated := false
	errStop := fmt.Errorf("stop")
	walkErr := filepath.WalkDir(cwd, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if p != cwd && (name == ".git" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(cwd, p)
		if relErr != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		if matchGlob(normPat, relSlash) {
			if len(matches) >= maxMatches {
				truncated = true
				return errStop
			}
			matches = append(matches, relSlash)
		}
		return nil
	})
	if walkErr != nil && walkErr != errStop {
		return nil, false, walkErr
	}
	return matches, truncated, nil
}

// matchGlob matches a glob pattern supporting `*`, `?`, `[...]`, `{a,b}`, and `**`.
func matchGlob(pattern, target string) bool {
	for _, expanded := range expandBraces(pattern) {
		if matchGlobSimple(expanded, target) {
			return true
		}
	}
	return false
}

func expandBraces(pattern string) []string {
	open := strings.IndexByte(pattern, '{')
	if open == -1 {
		return []string{pattern}
	}
	depth := 0
	closeIdx := -1
	var parts []string
	partStart := open + 1
	for i := open; i < len(pattern); i++ {
		switch pattern[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				parts = append(parts, pattern[partStart:i])
				closeIdx = i
			}
		case ',':
			if depth == 1 {
				parts = append(parts, pattern[partStart:i])
				partStart = i + 1
			}
		}
		if closeIdx != -1 {
			break
		}
	}
	if closeIdx == -1 {
		return []string{pattern}
	}
	prefix := pattern[:open]
	suffix := pattern[closeIdx+1:]
	var out []string
	for _, p := range parts {
		out = append(out, expandBraces(prefix+p+suffix)...)
	}
	return out
}

func matchGlobSimple(pattern, target string) bool {
	patSegs := strings.Split(pattern, "/")
	tgtSegs := strings.Split(target, "/")
	return matchSegments(patSegs, tgtSegs)
}

func matchSegments(pat, tgt []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			pat = pat[1:]
			if len(pat) == 0 {
				return true
			}
			for i := range len(tgt) + 1 {
				if matchSegments(pat, tgt[i:]) {
					return true
				}
			}
			return false
		}
		if len(tgt) == 0 {
			return false
		}
		ok, err := filepath.Match(pat[0], tgt[0])
		if err != nil || !ok {
			return false
		}
		pat = pat[1:]
		tgt = tgt[1:]
	}
	return len(tgt) == 0
}
