// Package ansi implements omp's terminal (ANSI) and Tern-native tool renderers,
// status line, welcome header, and working indicator/messages for oh-my-pig on
// PiG's extension SDK.
//
// Each tool has two builders registered via builders.go:
//   - Terminal builders (term_*.go): port of omp's renderCall/renderResult.
//     Every emitted row is measured and wrapped/truncated using PiG SDK's own
//     tui.VisibleWidth / tui.WrapTextWithAnsi / tui.TruncateToWidth so rows can
//     never overflow the host terminal.
//   - Native builders (native_*.go): port of omp's describeCall/describeResult
//     carrying raw payloads with omp role IDs for pig-tern's native frontend.
package ansi

import (
	"fmt"
	"math"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/dirs"
)

// unicodeSymbols is transcribed mechanically from
// packages/tui/src/theme/symbols.ts (UNICODE_SYMBOLS) at omp v19.1.1.
var unicodeSymbols = struct {
	// Status
	statusSuccess  string
	statusError    string
	statusWarning  string
	statusInfo     string
	statusPending  string
	statusDisabled string
	statusEnabled  string
	statusRunning  string
	statusShadowed string
	statusAborted  string
	statusDone     string

	// Navigation
	navCursor   string
	navSelected string
	navExpand   string
	navCollapse string
	navBack     string

	// Tree
	treeBranch string
	treeLast   string
	treeVert   string
	treeHoriz  string
	treeHook   string

	// Progress bar & context gauge
	progressFilled     string
	progressEmpty      string
	contextSpeculation string
	contextCompaction  string
	boxTopLeft         string
	boxTopRight        string
	boxBottomLeft      string
	boxBottomRight     string
	boxHoriz           string
	boxVert            string
	boxTeeRight        string
	boxTeeLeft         string

	// Separators
	sepPowerline      string
	sepPowerlineThin  string
	sepPowerlineLeft  string
	sepPowerlineRight string
	sepPowerlineThinL string
	sepPowerlineThinR string
	sepDot            string
	sepSlash          string
	sepPipe           string

	// Thinking levels
	thinkingMinimal     string
	thinkingLow         string
	thinkingMedium      string
	thinkingHigh        string
	thinkingXhigh       string
	thinkingMax         string
	thinkingAutoPending string

	// Checkboxes & radio
	checkboxChecked   string
	checkboxUnchecked string
	radioSelected     string
	radioUnselected   string

	// Formatting & markdown
	formatBullet       string
	formatDash         string
	formatBracketLeft  string
	formatBracketRight string
	mdQuoteBorder      string
	mdHrChar           string
	mdBullet           string

	// Icons
	iconModel         string
	iconPlan          string
	iconPrewalk       string
	iconGoal          string
	iconPause         string
	iconLoop          string
	iconFolder        string
	iconWorktree      string
	iconSearch        string
	iconScratchFolder string
	iconFile          string
	iconGit           string
	iconBranch        string
	iconPR            string
	iconTokens        string
	iconContext       string
	iconCost          string
	iconAdvisor       string
	iconAdvisorClosed string
	iconTime          string
	iconOmp           string
	iconGhost         string
	iconAgents        string
	iconJob           string
	iconCache         string
	iconInput         string
	iconOutput        string
	iconThroughput    string
	iconHost          string
	iconServer        string
	iconSession       string
	iconPID           string
	iconPackage       string
	iconWarning       string
	iconRewind        string
	iconMinimize      string
	iconAuto          string
	iconFast          string
	iconExtTool       string

	// Tool identity icons
	toolWrite     string
	toolEdit      string
	toolBash      string
	toolSsh       string
	toolLsp       string
	toolGh        string
	toolWebSearch string
	toolExa       string
	toolBrowser   string
	toolEval      string
	toolDebug     string
	toolMcp       string
	toolJob       string
	toolLaunch    string
	toolTask      string
	toolTodo      string
	toolMemory    string
	toolAsk       string
	toolResolve   string
	toolReview    string
	toolGoal      string
	toolIrc       string
	toolDelete    string
	toolMove      string
}{
	statusSuccess:  "✔",
	statusError:    "✘",
	statusWarning:  "⚠",
	statusInfo:     "ⓘ",
	statusPending:  "⏳",
	statusDisabled: "⦸",
	statusEnabled:  "●",
	statusRunning:  "⟳",
	statusShadowed: "○",
	statusAborted:  "⏹",
	statusDone:     "✔",

	navCursor:   "❯",
	navSelected: "➤",
	navExpand:   "▸",
	navCollapse: "▾",
	navBack:     "⟵",

	treeBranch: "├─",
	treeLast:   "└─",
	treeVert:   "│",
	treeHoriz:  "─",
	treeHook:   "└",

	progressFilled:     "━",
	progressEmpty:      "─",
	contextSpeculation: "╎",
	contextCompaction:  "┃",
	boxTopLeft:         "╭",
	boxTopRight:        "╮",
	boxBottomLeft:      "╰",
	boxBottomRight:     "╯",
	boxHoriz:           "─",
	boxVert:            "│",
	boxTeeRight:        "├",
	boxTeeLeft:         "┤",

	sepPowerline:      "▕",
	sepPowerlineThin:  "┆",
	sepPowerlineLeft:  "▶",
	sepPowerlineRight: "◀",
	sepPowerlineThinL: ">",
	sepPowerlineThinR: "<",
	sepDot:            " · ",
	sepSlash:          " / ",
	sepPipe:           " │ ",

	thinkingMinimal:     "○ min",
	thinkingLow:         "◔ low",
	thinkingMedium:      "◑ med",
	thinkingHigh:        "◒ high",
	thinkingXhigh:       "◕ xhigh",
	thinkingMax:         "◉ max",
	thinkingAutoPending: "⟳",

	checkboxChecked:   "☑",
	checkboxUnchecked: "☐",
	radioSelected:     "◉",
	radioUnselected:   "○",

	formatBullet:       "•",
	formatDash:         "—",
	formatBracketLeft:  "⟦",
	formatBracketRight: "⟧",
	mdQuoteBorder:      "▏",
	mdHrChar:           "─",
	mdBullet:           "•",

	iconModel:         "⬢",
	iconPlan:          "🗺",
	iconPrewalk:       "🏃",
	iconGoal:          "🎯",
	iconPause:         "⏸",
	iconLoop:          "↻",
	iconFolder:        "📁",
	iconWorktree:      "🌳",
	iconSearch:        "🔍",
	iconScratchFolder: "🗑",
	iconFile:          "📄",
	iconGit:           "⎇",
	iconBranch:        "⑂",
	iconPR:            "⤴",
	iconTokens:        "🪙",
	iconContext:       "◫",
	iconCost:          "💲",
	iconAdvisor:       "👁",
	iconAdvisorClosed: "🙈",
	iconTime:          "⏱",
	iconOmp:           "π",
	iconGhost:         "👻",
	iconAgents:        "👥",
	iconJob:           "⚙",
	iconCache:         "💾",
	iconInput:         "⤵",
	iconOutput:        "⤴",
	iconThroughput:    "⚡",
	iconHost:          "🖥",
	iconServer:        "🗄",
	iconSession:       "🆔",
	iconPID:           "#",
	iconPackage:       "📦",
	iconWarning:       "⚠",
	iconRewind:        "↶",
	iconMinimize:      "⇲",
	iconAuto:          "⟲",
	iconFast:          "⚡",
	iconExtTool:       "🛠",

	toolWrite:     "✎",
	toolEdit:      "✎",
	toolBash:      "❯",
	toolSsh:       "⇄",
	toolLsp:       "💡",
	toolGh:        "⎇",
	toolWebSearch: "⌕",
	toolExa:       "🔭",
	toolBrowser:   "🌐",
	toolEval:      "▶",
	toolDebug:     "🐞",
	toolMcp:       "🔌",
	toolJob:       "⚙",
	toolLaunch:    "🚀",
	toolTask:      "⇶",
	toolTodo:      "☑",
	toolMemory:    "🧠",
	toolAsk:       "?",
	toolResolve:   "✓",
	toolReview:    "◉",
	toolGoal:      "◎",
	toolIrc:       "✉",
	toolDelete:    "🗑",
	toolMove:      "➜",
}

// langSymbols mirrors packages/tui/src/theme/symbols.ts (UNICODE_SYMBOLS lang.*).
var langSymbols = map[string]string{
	"lang.default":    "⌘",
	"lang.typescript": "🟦",
	"lang.javascript": "🟨",
	"lang.python":     "🐍",
	"lang.rust":       "🦀",
	"lang.go":         "🐹",
	"lang.java":       "☕",
	"lang.c":          "Ⓒ",
	"lang.cpp":        "➕",
	"lang.csharp":     "♯",
	"lang.ruby":       "💎",
	"lang.julia":      "Ⓙ",
	"lang.php":        "🐘",
	"lang.swift":      "🕊",
	"lang.kotlin":     "🅺",
	"lang.shell":      "💻",
	"lang.html":       "🌐",
	"lang.css":        "🎨",
	"lang.json":       "🧾",
	"lang.yaml":       "📋",
	"lang.markdown":   "📝",
	"lang.sql":        "🗄",
	"lang.docker":     "🐳",
	"lang.lua":        "🌙",
	"lang.text":       "🗒",
	"lang.env":        "🔧",
	"lang.toml":       "🧾",
	"lang.xml":        "⟨⟩",
	"lang.ini":        "⚙",
	"lang.conf":       "⚙",
	"lang.log":        "📜",
	"lang.csv":        "📑",
	"lang.tsv":        "📑",
	"lang.image":      "🖼",
	"lang.pdf":        "📕",
	"lang.archive":    "🗜",
	"lang.binary":     "⚙",
}

// langIDToSymbol mirrors packages/tui/src/theme/theme-class.ts langMap.
var langIDToSymbol = map[string]string{
	"typescript": "lang.typescript",
	"ts":         "lang.typescript",
	"tsx":        "lang.typescript",
	"javascript": "lang.javascript",
	"js":         "lang.javascript",
	"jsx":        "lang.javascript",
	"mjs":        "lang.javascript",
	"cjs":        "lang.javascript",
	"python":     "lang.python",
	"py":         "lang.python",
	"rust":       "lang.rust",
	"rs":         "lang.rust",
	"go":         "lang.go",
	"java":       "lang.java",
	"c":          "lang.c",
	"cpp":        "lang.cpp",
	"c++":        "lang.cpp",
	"cc":         "lang.cpp",
	"cxx":        "lang.cpp",
	"csharp":     "lang.csharp",
	"cs":         "lang.csharp",
	"ruby":       "lang.ruby",
	"rb":         "lang.ruby",
	"julia":      "lang.julia",
	"jl":         "lang.julia",
	"php":        "lang.php",
	"swift":      "lang.swift",
	"kotlin":     "lang.kotlin",
	"kt":         "lang.kotlin",
	"bash":       "lang.shell",
	"sh":         "lang.shell",
	"zsh":        "lang.shell",
	"fish":       "lang.shell",
	"powershell": "lang.shell",
	"just":       "lang.shell",
	"shell":      "lang.shell",
	"html":       "lang.html",
	"htm":        "lang.html",
	"astro":      "lang.html",
	"vue":        "lang.html",
	"svelte":     "lang.html",
	"css":        "lang.css",
	"scss":       "lang.css",
	"sass":       "lang.css",
	"less":       "lang.css",
	"json":       "lang.json",
	"jsonc":      "lang.json",
	"yaml":       "lang.yaml",
	"yml":        "lang.yaml",
	"markdown":   "lang.markdown",
	"md":         "lang.markdown",
	"sql":        "lang.sql",
	"dockerfile": "lang.docker",
	"docker":     "lang.docker",
	"lua":        "lang.lua",
	"text":       "lang.text",
	"txt":        "lang.text",
	"plain":      "lang.text",
	"log":        "lang.log",
	"env":        "lang.env",
	"dotenv":     "lang.env",
	"toml":       "lang.toml",
	"xml":        "lang.xml",
	"ini":        "lang.ini",
	"conf":       "lang.conf",
	"cfg":        "lang.conf",
	"config":     "lang.conf",
	"properties": "lang.conf",
	"csv":        "lang.csv",
	"tsv":        "lang.tsv",
	"image":      "lang.image",
	"img":        "lang.image",
	"png":        "lang.image",
	"jpg":        "lang.image",
	"jpeg":       "lang.image",
	"gif":        "lang.image",
	"webp":       "lang.image",
	"svg":        "lang.image",
	"ico":        "lang.image",
	"bmp":        "lang.image",
	"tiff":       "lang.image",
	"pdf":        "lang.pdf",
	"zip":        "lang.archive",
	"tar":        "lang.archive",
	"gz":         "lang.archive",
	"tgz":        "lang.archive",
	"bz2":        "lang.archive",
	"xz":         "lang.archive",
	"7z":         "lang.archive",
	"exe":        "lang.binary",
	"dll":        "lang.binary",
	"so":         "lang.binary",
	"dylib":      "lang.binary",
	"wasm":       "lang.binary",
	"bin":        "lang.binary",
}

// extToLangID mirrors packages/tui/src/lang-from-path.ts EXTENSION_LANG[ext][0].
var extToLangID = map[string]string{
	"ts": "typescript", "cts": "typescript", "mts": "typescript", "tsx": "tsx",
	"js": "javascript", "jsx": "javascript", "mjs": "javascript", "cjs": "javascript",
	"rs": "rust", "go": "go", "c": "c", "h": "c",
	"cpp": "cpp", "cc": "cpp", "cxx": "cpp", "hh": "cpp", "hpp": "cpp", "hxx": "cpp",
	"inl": "cpp", "ipp": "cpp", "tpp": "cpp", "cppm": "cpp", "ixx": "cpp",
	"cu": "cpp", "cuh": "cpp", "ino": "cpp", "zig": "zig",
	"py": "python", "pyi": "python",
	"rb": "ruby", "rbw": "ruby", "gemspec": "ruby", "erb": "erb",
	"lua": "lua",
	"sh":  "bash", "bash": "bash", "zsh": "bash", "ksh": "bash", "bats": "bash",
	"tmux": "bash", "cgi": "bash", "fcgi": "bash", "command": "bash", "tool": "bash",
	"fish": "fish", "pl": "perl", "pm": "perl", "perl": "perl", "php": "php",
	"java": "java", "kt": "kotlin", "ktm": "kotlin", "kts": "kotlin",
	"scala": "scala", "sc": "scala", "sbt": "scala", "groovy": "groovy",
	"clj": "clojure", "cljc": "clojure", "cljs": "clojure", "edn": "clojure",
	"el": "emacs-lisp",
	"cs": "csharp", "fs": "fsharp", "vb": "vb",
	"html": "html", "htm": "html", "xhtml": "html",
	"css": "css", "scss": "scss", "sass": "sass", "less": "less",
	"vue": "vue", "svelte": "svelte", "astro": "astro",
	"json": "json", "jsonc": "jsonc", "yaml": "yaml", "yml": "yaml", "toml": "toml",
	"xml": "xml", "xsl": "xml", "xslt": "xml", "svg": "xml", "plist": "xml", "ini": "ini",
	"md": "markdown", "markdown": "markdown", "mdx": "markdown", "mdc": "markdown",
	"mkd": "markdown", "mdown": "markdown", "rst": "restructuredtext", "adoc": "asciidoc", "tex": "latex",
	"sql": "sql", "graphql": "graphql", "gql": "graphql", "proto": "protobuf",
	"dockerfile": "dockerfile", "containerfile": "dockerfile",
	"tf": "hcl", "hcl": "hcl", "tfvars": "hcl", "nix": "nix",
	"ex": "elixir", "exs": "elixir", "erl": "erlang", "hrl": "erlang",
	"hs": "haskell", "ml": "ocaml", "mli": "ocaml",
	"swift": "swift", "r": "r", "jl": "julia", "dart": "dart", "elm": "elm",
	"v": "verilog", "nim": "nim", "cr": "crystal", "d": "d", "pas": "pascal", "pp": "pascal",
	"lisp": "lisp", "lsp": "lisp", "rkt": "racket", "scm": "scheme",
	"ps1": "powershell", "psm1": "powershell", "bat": "bat", "cmd": "bat",
	"tla": "tlaplus", "tlaplus": "tlaplus", "m": "objc", "mm": "objc",
	"sol": "solidity", "odin": "odin", "star": "starlark", "bzl": "starlark",
	"sv": "verilog", "svh": "verilog", "vh": "verilog", "vim": "vim",
	"ipynb": "ipynb", "hbs": "handlebars", "hsb": "handlebars", "handlebars": "handlebars",
	"diff": "diff", "patch": "diff",
	"makefile": "make", "mk": "make", "mak": "make", "cmake": "cmake", "justfile": "just",
	"txt": "text", "text": "text", "log": "log", "csv": "csv", "tsv": "tsv",
	"cfg": "conf", "conf": "conf", "config": "conf", "properties": "conf", "env": "env",
	"gitignore": "conf", "gitattributes": "conf", "gitmodules": "conf", "editorconfig": "conf",
	"npmrc": "conf", "prettierrc": "conf", "eslintrc": "conf", "prettierignore": "conf", "eslintignore": "conf",
	"png": "png", "jpg": "jpg", "jpeg": "jpeg", "gif": "gif", "webp": "webp",
	"ico": "ico", "bmp": "bmp", "tiff": "tiff", "pdf": "pdf",
	"zip": "zip", "tar": "tar", "gz": "gz", "tgz": "tgz", "bz2": "bz2", "xz": "xz", "7z": "7z",
	"exe": "exe", "dll": "dll", "so": "so", "dylib": "dylib", "wasm": "wasm", "bin": "bin",
}

// getLanguageFromPath mirrors packages/tui/src/lang-from-path.ts getLanguageFromPath.
func getLanguageFromPath(filePath string) string {
	clean := strings.ReplaceAll(filePath, "\\", "/")
	baseName := strings.ToLower(path.Base(clean))
	if strings.HasPrefix(baseName, ".env.") {
		return "env"
	}
	if baseName == "dockerfile" || strings.HasPrefix(baseName, "dockerfile.") || baseName == "containerfile" {
		return "dockerfile"
	}
	if baseName == ".emacs" {
		return "emacs-lisp"
	}
	if baseName == "justfile" {
		return "just"
	}
	if baseName == "cmakelists.txt" {
		return "cmake"
	}
	extKey := strings.ToLower(clean)
	if idx := strings.LastIndexByte(clean, '.'); idx != -1 {
		extKey = strings.ToLower(clean[idx+1:])
	}
	if lang, ok := extToLangID[extKey]; ok {
		return lang
	}
	return ""
}

// langIconForLang mirrors packages/tui/src/theme/theme-class.ts Theme.getLangIcon.
func langIconForLang(lang string) string {
	if lang == "" {
		return langSymbols["lang.default"]
	}
	key, ok := langIDToSymbol[strings.ToLower(lang)]
	if !ok {
		return langSymbols["lang.default"]
	}
	if sym, ok := langSymbols[key]; ok {
		return sym
	}
	return langSymbols["lang.default"]
}

// langIcon returns omp's unicode language glyph for a file path.
// upstream: packages/tui/src/theme/theme-class.ts getLangIcon(getLanguageFromPath(filePath))
func langIcon(filePath string) string {
	return langIconForLang(getLanguageFromPath(filePath))
}

// langIconStyled mirrors packages/tui/src/theme/theme-class.ts:887-894:
// tints language icons with their brand color (LANG_BRAND_COLORS) in truecolor/256color,
// falling back to the muted theme color for languages without a brand entry.
func (p palette) langIconStyled(lang string) string {
	icon := langIconForLang(lang)
	if icon == "" {
		return ""
	}
	if lang != "" {
		if key, ok := langIDToSymbol[strings.ToLower(lang)]; ok {
			if brand, hasBrand := langBrandColors[key]; hasBrand {
				return rgbFgAnsi(brand, p.mode) + icon + "\x1b[39m"
			}
		}
	}
	return p.fg("muted", icon)
}

var ompActivitySpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Preview limits matching packages/tui/src/render/render-utils.ts PREVIEW_LIMITS and TRUNCATE_LENGTHS.
const (
	previewCollapsedLines  = 3
	previewExpandedLines   = 12
	previewCollapsedItems  = 8
	previewLocationsClamp  = 2
	previewOutputCollapse  = 3
	previewOutputExpand    = 10
	previewDiffHunks       = 8
	previewDiffLines       = 40
	defaultTerminalLines   = 10
	previewWindowFallback  = 10 // max(6, 30-20) per packages/tui/src/render/render-utils.ts:352-362
	readPreviewLines       = 12
	truncateTitle          = 60
	truncateContent        = 80
	truncateLong           = 100
	truncateLine           = 110
	truncateShort          = 40
	collapsedAgentLimit    = 4
	maxExpandedOutputLines = 2000 // defensive cap so 100k-line payloads don't blow IPC/memory
	hostMaxViewNodes       = 4096 // PiG view_kit.go viewMaxNodes
)

// palette resolves omp theme tokens using omp's titanium dark palette when
// the host theme is dark/unspecified (respecting GetColorMode()), falling back
// to the host's sdk.UITheme on light themes or unknown tokens.
type palette struct {
	theme    sdk.UITheme
	titanium bool
	mode     string
}

func newPalette(ctx sdk.Context) (p palette) {
	defer func() {
		if recover() != nil {
			p = palette{}
		}
	}()
	if !ctx.HasUI() {
		return palette{}
	}
	return paletteFromTheme(ctx.UITheme())
}

func paletteFromTheme(t sdk.UITheme) palette {
	return palette{
		theme:    t,
		titanium: useTitaniumPalette(t),
		mode:     t.GetColorMode(),
	}
}

func (p palette) fgAnsi(token string) string {
	if p.titanium {
		if c, ok := titaniumFgColors[token]; ok {
			return rgbFgAnsi(c, p.mode)
		}
		if titaniumDefaultFgTokens[token] {
			return "\x1b[39m"
		}
	}
	if ansi, err := p.theme.GetFgAnsi(token); err == nil {
		return ansi
	}
	return ""
}

func (p palette) bgAnsi(token string) string {
	if p.titanium {
		if c, ok := titaniumBgColors[token]; ok {
			return rgbBgAnsi(c, p.mode)
		}
	}
	if ansi, err := p.theme.GetBgAnsi(token); err == nil {
		return ansi
	}
	return ""
}

// wrapForeground mirrors packages/tui/src/theme/theme-class.ts:147-150:
// restores the outer foreground ANSI after any nested `\x1b[0m` or `\x1b[39m`
// so inner styled spans inside a wrapped description (e.g. `langIcon + " " + path`
// or `subject + dim("in") + scope`) do not drop the outer `muted` foreground on
// trailing text/spaces.
func wrapForeground(ansi, text string) string {
	if strings.Contains(text, "\x1b[") {
		text = strings.ReplaceAll(text, "\x1b[0m", "\x1b[0m"+ansi)
		text = strings.ReplaceAll(text, "\x1b[39m", "\x1b[39m"+ansi)
	}
	return ansi + text + "\x1b[39m"
}

func (p palette) fg(token, text string) string {
	if text == "" {
		return ""
	}
	if p.titanium {
		if c, ok := titaniumFgColors[token]; ok {
			return wrapForeground(rgbFgAnsi(c, p.mode), text)
		}
		if titaniumDefaultFgTokens[token] {
			return wrapForeground("\x1b[39m", text)
		}
	}
	if ansi, err := p.theme.GetFgAnsi(token); err == nil && ansi != "" {
		return wrapForeground(ansi, text)
	}
	if _, err := p.theme.GetFgAnsi(token); err != nil {
		return text
	}
	return p.theme.Fg(token, text)
}
func (p palette) bg(token, text string) string {
	if text == "" {
		return ""
	}
	if p.titanium {
		if c, ok := titaniumBgColors[token]; ok {
			return rgbBgAnsi(c, p.mode) + text + "\x1b[49m"
		}
	}
	if _, err := p.theme.GetBgAnsi(token); err != nil {
		return text
	}
	return p.theme.Bg(token, text)
}
func (p palette) bold(text string) string          { return p.theme.Bold(text) }
func (p palette) italic(text string) string        { return p.theme.Italic(text) }
func (p palette) strike(text string) string        { return p.theme.Strikethrough(text) }
func (p palette) underline(text string) string     { return p.theme.Underline(text) }
func (p palette) inverse(text string) string       { return p.theme.Inverse(text) }
func (p palette) styledSym(sym, tok string) string { return p.fg(tok, sym) }

// statusIcon formats omp's status glyph in its semantic theme color.
// upstream: packages/tui/src/render/render-utils.ts formatStatusIcon
func (p palette) statusIcon(status string) string {
	switch status {
	case "success", "done":
		return p.fg("success", unicodeSymbols.statusSuccess)
	case "error":
		return p.fg("error", unicodeSymbols.statusError)
	case "warning":
		return p.fg("warning", unicodeSymbols.statusWarning)
	case "info":
		return p.fg("accent", unicodeSymbols.statusInfo)
	case "pending":
		return p.fg("muted", unicodeSymbols.statusPending)
	case "running":
		return p.fg("accent", unicodeSymbols.statusRunning)
	case "aborted":
		return p.fg("error", unicodeSymbols.statusAborted)
	case "disabled":
		return p.fg("dim", unicodeSymbols.statusDisabled)
	default:
		return p.fg("muted", unicodeSymbols.statusPending)
	}
}

type statusLineOpts struct {
	icon         string
	iconOverride string
	title        string
	titleColor   string
	description  string
	badgeLabel   string
	badgeColor   string
	meta         []string
}

// renderStatusLine renders omp's single-line status header.
// upstream: packages/tui/src/render/status-line.ts renderStatusLine
func (p palette) renderStatusLine(opts statusLineOpts) string {
	icon := opts.iconOverride
	if icon == "" && opts.icon != "" {
		icon = p.statusIcon(opts.icon)
	}
	titleColor := opts.titleColor
	if titleColor == "" {
		titleColor = "accent"
	}
	title := p.fg(titleColor, flattenHeader(opts.title))
	line := title
	if icon != "" {
		line = icon + " " + title
	}
	if opts.description != "" {
		line += ": " + p.fg("muted", flattenHeader(opts.description))
	}
	if opts.badgeLabel != "" {
		bc := opts.badgeColor
		if bc == "" {
			bc = "accent"
		}
		line += " " + p.fg(bc, unicodeSymbols.formatBracketLeft+flattenHeader(opts.badgeLabel)+unicodeSymbols.formatBracketRight)
	}
	var meta []string
	for _, m := range opts.meta {
		fm := flattenHeader(m)
		if strings.TrimSpace(tui.StripTerminalSequences(fm)) != "" {
			meta = append(meta, fm)
		}
	}
	if len(meta) > 0 {
		line += " " + p.fg("dim", strings.Join(meta, unicodeSymbols.sepDot))
	}
	return line
}

func (p palette) wrapBrackets(text string) string {
	return unicodeSymbols.formatBracketLeft + text + unicodeSymbols.formatBracketRight
}

func (p palette) formatBadge(label, color string) string {
	if color == "" {
		color = "accent"
	}
	return p.fg(color, p.wrapBrackets(flattenHeader(label)))
}

// expandKeyHint mirrors packages/tui/src/render/render-utils.ts expandKeyHint:
// expands `ctrl+o` using platform-aware unicode keycaps (`⌃O` on macOS,
// `Ctrl+O` elsewhere).
func expandKeyHint() string {
	return expandTipKeys("{key:ctrl+o}", runtime.GOOS)
}

// formatExpandHint mirrors packages/tui/src/render/render-utils.ts:321-325.
func (p palette) formatExpandHint(expanded, hasMore bool) string {
	if expanded || !hasMore {
		return ""
	}
	return p.fg("dim", p.wrapBrackets(expandKeyHint()+": Expand"))
}

// sanitizeErrorText mirrors packages/tui/src/render/render-utils.ts:391-394.
func sanitizeErrorText(msg string) string {
	clean := sanitizeDisplayText(msg)
	clean = strings.TrimSpace(trimErrorPrefix(clean))
	if clean == "" {
		return "Unknown error"
	}
	return replaceTabs(truncateWidth(clean, truncateLine))
}

func trimErrorPrefix(s string) string {
	if strings.HasPrefix(s, "Error:") {
		return strings.TrimLeft(s[len("Error:"):], " \t")
	}
	return s
}

// formatErrorMessage mirrors packages/tui/src/render/render-utils.ts:412-414.
func (p palette) formatErrorMessage(msg string) string {
	return p.statusIcon("error") + " " + p.fg("error", "Error: "+sanitizeErrorText(msg))
}

// formatErrorDetail mirrors packages/tui/src/render/render-utils.ts:423-425.
func (p palette) formatErrorDetail(msg string) string {
	return "  " + p.fg("error", sanitizeErrorText(msg))
}

// formatEmptyMessage mirrors packages/tui/src/render/render-utils.ts:428-430.
func (p palette) formatEmptyMessage(msg string) string {
	return p.statusIcon("warning") + " " + p.fg("muted", sanitizeDisplayText(msg))
}

// pathNameStart mirrors packages/tui/src/render/render-utils.ts:443-447.
func pathNameStart(filePath string) int {
	end := len(filePath)
	for end > 0 && (filePath[end-1] == '/' || filePath[end-1] == '\\') {
		end--
	}
	if end == 0 {
		return 0
	}
	i1 := strings.LastIndexByte(filePath[:end], '/')
	i2 := strings.LastIndexByte(filePath[:end], '\\')
	return max(i1, i2) + 1
}

// formatStyledPath mirrors packages/tui/src/render/render-utils.ts:454-458.
func (p palette) formatStyledPath(rawPath string) string {
	cleaned := sanitizeDisplayText(rawPath)
	cut := pathNameStart(cleaned)
	name := p.fg("customMessageLabel", cleaned[cut:])
	if cut > 0 {
		return p.fg("dim", cleaned[:cut]) + name
	}
	return name
}

// ── Text / Sanitisation / SDK Width helpers ──────────────────────────────────

// StripANSI removes terminal escape sequences using PiG SDK's tui.StripTerminalSequences.
func StripANSI(s string) string {
	return tui.StripTerminalSequences(s)
}

// sanitizeDisplayText mirrors packages/utils/src/sanitize-text.ts:
// strips CSI/OSC/APC/ESC sequences, removes C0 controls (except \t and \n),
// DEL (0x7F), and C1 controls (0x80-0x9F), and normalizes CRLF / lone CR.
func sanitizeDisplayText(s string) string {
	if s == "" {
		return ""
	}
	if strings.ContainsRune(s, 0x1b) {
		s = tui.StripTerminalSequences(s)
	}
	// Fast path: check if any control byte or CR or invalid UTF-8 is present.
	needsClean := false
	for i := 0; i < len(s); {
		b := s[i]
		if b < 0x80 {
			if b != '\n' && b != '\t' && (b < 0x20 || b == 0x7f) {
				needsClean = true
				break
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			needsClean = true
			break
		}
		if r >= 0x80 && r <= 0x9f {
			needsClean = true
			break
		}
		i += size
	}
	if !needsClean {
		return s
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		if r == utf8.RuneError && size == 1 {
			continue
		}
		if r == '\r' {
			continue
		}
		if r == '\n' || r == '\t' || (r >= 0x20 && r != 0x7f && (r < 0x80 || r > 0x9f)) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// sanitizeContentText strips ANSI/OSC/CSI escape sequences, C0 controls
// (except \t, \n, and \r so downstream sanitizeDisplayLines can still collapse
// bare-CR progress overwrites), DEL (0x7F), and C1 controls (U+0080-U+009F).
func sanitizeContentText(s string) string {
	if s == "" {
		return ""
	}
	if strings.ContainsRune(s, 0x1b) {
		s = tui.StripTerminalSequences(s)
	}
	needsClean := false
	for i := 0; i < len(s); {
		b := s[i]
		if b < 0x80 {
			if b != '\n' && b != '\t' && b != '\r' && (b < 0x20 || b == 0x7f) {
				needsClean = true
				break
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if (r == utf8.RuneError && size == 1) || (r >= 0x80 && r <= 0x9f) {
			needsClean = true
			break
		}
		i += size
	}
	if !needsClean {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		if r == utf8.RuneError && size == 1 {
			continue
		}
		if r == '\n' || r == '\t' || r == '\r' || (r >= 0x20 && r != 0x7f && (r < 0x80 || r > 0x9f)) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// sanitizeDisplayLines mirrors packages/tui/src/render/render-utils.ts:404-409:
// splits CRLF/LF, collapses bare CR progress overwrites to the final segment,
// sanitises controls/ANSI, and expands tabs.
func sanitizeDisplayLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	raw := strings.Split(text, "\n")
	out := make([]string, len(raw))
	for i, line := range raw {
		if idx := strings.LastIndexByte(line, '\r'); idx >= 0 {
			line = line[idx+1:]
		}
		out[i] = replaceTabs(sanitizeDisplayText(line))
	}
	return out
}

// sanitizeDeep applies sanitizeDisplayText to every string inside a JSON-shaped
// value (maps, slices, strings), returning a sanitized copy without mutating
// the caller's structures. Mirrors packages/utils/src/sanitize-text.ts sanitizeTextDeep.
func sanitizeDeep(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case string:
		return sanitizeDisplayText(x)
	case map[string]any:
		if len(x) == 0 {
			return x
		}
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = sanitizeDeep(val)
		}
		return out
	case []any:
		if len(x) == 0 {
			return x
		}
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = sanitizeDeep(val)
		}
		return out
	case []string:
		if len(x) == 0 {
			return x
		}
		out := make([]string, len(x))
		for i, s := range x {
			out[i] = sanitizeDisplayText(s)
		}
		return out
	default:
		return v
	}
}

func sanitizeMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out, _ := sanitizeDeep(m).(map[string]any)
	return out
}

func replaceTabs(s string) string {
	return strings.ReplaceAll(s, "\t", "   ")
}

func flattenHeader(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

func flattenLine(s string) string {
	s = replaceTabs(sanitizeDisplayText(s))
	return strings.Join(strings.Fields(s), " ")
}

// visibleWidth delegates directly to PiG SDK's grapheme-based tui.VisibleWidth.
func visibleWidth(s string) int {
	return tui.VisibleWidth(s)
}

// truncateWidth truncates s (plain or ANSI) to at most maxWidth cells with "…" using the SDK.
func truncateWidth(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if tui.VisibleWidth(s) <= maxWidth {
		return s
	}
	if maxWidth == 1 {
		return "…"
	}
	res := tui.TruncateToWidth(s, maxWidth, "…", false)
	if tui.VisibleWidth(res) > maxWidth {
		res = tui.TruncateToWidth(s, maxWidth, "", false)
	}
	return res
}

// truncateOmit truncates s to at most maxWidth cells without an ellipsis using the SDK.
func truncateOmit(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if tui.VisibleWidth(s) <= maxWidth {
		return s
	}
	return tui.TruncateToWidth(s, maxWidth, "", false)
}

// clampRow guarantees tui.VisibleWidth(out) <= width for any width >= 1
// and strips any embedded CR/LF so one element is always one terminal row.
func clampRow(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if strings.ContainsAny(s, "\r\n") {
		s = flattenHeader(s)
	}
	if tui.VisibleWidth(s) <= width {
		return s
	}
	return tui.TruncateToWidth(s, width, "", false)
}

// clampRows splits any embedded newlines so every output element is strictly
// one terminal row (no '\n' or '\r') and guarantees tui.VisibleWidth(row) <= width.
func clampRows(rows []string, width int) []string {
	if width <= 0 {
		return nil
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if !strings.ContainsAny(r, "\r\n") {
			out = append(out, clampRow(r, width))
			continue
		}
		r = strings.ReplaceAll(r, "\r\n", "\n")
		for _, part := range strings.Split(r, "\n") {
			if idx := strings.LastIndexByte(part, '\r'); idx >= 0 {
				part = part[idx+1:]
			}
			out = append(out, clampRow(part, width))
		}
	}
	return out
}

// wrapAnsi wraps text to width columns using PiG SDK's tui.WrapTextWithAnsi
// and clamps every resulting row defensively to width.
func wrapAnsi(text string, width int) []string {
	if width <= 0 {
		return nil
	}
	if text == "" {
		return []string{""}
	}
	wrapped := tui.WrapTextWithAnsi(text, width)
	if len(wrapped) == 0 {
		return []string{""}
	}
	for i, line := range wrapped {
		if tui.VisibleWidth(line) > width {
			wrapped[i] = tui.TruncateToWidth(line, width, "", false)
		}
	}
	return wrapped
}

// wrapPrefixedLine wraps content under a first-line prefix and a continuation prefix,
// guaranteeing every emitted row fits within width.
// upstream: packages/tui/src/render/hanging-wrap.ts wrapPrefixedLine
func wrapPrefixedLine(prefix, contPrefix, content string, width int) []string {
	if width <= 0 {
		return nil
	}
	pw := tui.VisibleWidth(prefix)
	cw := tui.VisibleWidth(contPrefix)
	bodyW := width - max(pw, cw)
	if bodyW < 1 {
		return []string{clampRow(prefix+content, width)}
	}
	wrapped := wrapAnsi(content, bodyW)
	out := make([]string, len(wrapped))
	for i, seg := range wrapped {
		p := contPrefix
		if i == 0 {
			p = prefix
		}
		out[i] = clampRow(p+seg, width)
	}
	return out
}

// fitHeaderedRows mirrors packages/tui/src/render/render-utils.ts:1183-1185:
// soft-wraps the header so trailing meta counts stay visible, and truncates
// each body row to width with no ellipsis.
func fitHeaderedRows(header string, rows []string, width int) []string {
	if width <= 0 {
		return nil
	}
	out := wrapAnsi(header, width)
	for _, r := range rows {
		out = append(out, truncateOmit(r, width))
	}
	return out
}

// clipCollapsed mirrors packages/tui/src/render/render-utils.ts:259-261.
func clipCollapsed(text string, maxWidth int, expanded bool) string {
	if expanded {
		return text
	}
	return truncateWidth(text, maxWidth)
}

// previewLine mirrors packages/tui/src/render/render-utils.ts:250-252.
func previewLine(text string, maxWidth int) string {
	return truncateWidth(flattenLine(text), maxWidth)
}

// truncateMiddleToWidth mirrors packages/tui/src/render/render-utils.ts:217-224.
func truncateMiddleToWidth(text string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if tui.VisibleWidth(text) <= maxWidth {
		return text
	}
	if maxWidth <= 2 {
		return truncateWidth(text, maxWidth)
	}
	headW := (maxWidth - 1) / 2
	tailW := maxWidth - 1 - headW
	head := tui.SliceByColumn(text, 0, headW, true)
	totalW := tui.VisibleWidth(text)
	tail := tui.SliceByColumn(text, max(0, totalW-tailW), tailW, true)
	return clampRow(head+"…"+tail, maxWidth)
}

// padRight pads s on the right with spaces to exact width, or truncates if wider.
func padRight(s string, width int) string {
	if width <= 0 {
		return ""
	}
	w := tui.VisibleWidth(s)
	if w > width {
		s = tui.TruncateToWidth(s, width, "", false)
		w = tui.VisibleWidth(s)
	}
	if w < width {
		s += strings.Repeat(" ", width-w)
	}
	return s
}

// shortenPath replaces a leading home directory prefix with "~", matching omp's
// `shortenPath` (`packages/tui/src/render/render-utils.ts:973-986`). It NEVER
// strips `/private` on arbitrary paths (e.g. `local://` or temp artifacts keep
// their exact spelling); macOS `/private` standardization happens once on the
// session working directory (`dirs.StandardizeMacOSPath(cwd)`), not here.
func shortenPath(p string) string {
	if p == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	return shortenPathWithHome(p, home)
}

// shortenPathWithHome mirrors packages/tui/src/render/render-utils.ts:927-986:
// strips a trailing separator from home (keeping roots `/` and `C:\`), matches
// leading `home` bounded by end-of-string or `/` / `\` (case-insensitive on
// Windows-style home paths), and replaces Windows separators in the suffix with `/`.
func shortenPathWithHome(p, home string) string {
	if p == "" || home == "" {
		return p
	}
	windowsStyle := (len(home) >= 3 && ((home[0] >= 'A' && home[0] <= 'Z') || (home[0] >= 'a' && home[0] <= 'z')) && home[1] == ':' && (home[2] == '\\' || home[2] == '/')) ||
		strings.HasPrefix(home, `\\`)
	trimmedHome := home
	for len(trimmedHome) > 1 && (trimmedHome[len(trimmedHome)-1] == '/' || trimmedHome[len(trimmedHome)-1] == '\\') {
		if windowsStyle && len(trimmedHome) == 3 && trimmedHome[1] == ':' {
			break
		}
		trimmedHome = trimmedHome[:len(trimmedHome)-1]
	}
	if len(p) < len(trimmedHome) {
		return p
	}
	prefix := p[:len(trimmedHome)]
	matched := false
	if windowsStyle {
		matched = strings.EqualFold(strings.ReplaceAll(prefix, `\`, "/"), strings.ReplaceAll(trimmedHome, `\`, "/"))
	} else {
		matched = prefix == trimmedHome
	}
	if !matched {
		return p
	}
	suffix := p[len(trimmedHome):]
	if suffix != "" && suffix[0] != '/' && suffix[0] != '\\' {
		return p
	}
	return "~" + strings.ReplaceAll(suffix, `\`, "/")
}

// formatToolWorkingDirectory mirrors packages/tui/src/render/render-utils.ts:1078-1094:
// standardizes projectDir once (`dirs.StandardizeMacOSPath`, matching omp's
// startup `getProjectDir()`), then returns empty when workdir equals projectDir,
// a relative path when strictly inside projectDir, else shortenPath(resolvedWorkdir).
// An already-absolute workdir outside projectDir keeps its own spelling.
func formatToolWorkingDirectory(workdir, projectDir string) string {
	if workdir == "" {
		return ""
	}
	if projectDir == "" {
		return replaceTabs(shortenPath(workdir))
	}
	stdProject := dirs.StandardizeMacOSPath(projectDir)
	resolvedProject, err1 := filepath.Abs(stdProject)
	if err1 != nil {
		resolvedProject = filepath.Clean(stdProject)
	}
	resolvedProject = dirs.StandardizeMacOSPath(resolvedProject)
	var resolvedWork string
	if filepath.IsAbs(workdir) {
		resolvedWork = filepath.Clean(workdir)
	} else {
		resolvedWork = filepath.Clean(filepath.Join(resolvedProject, workdir))
	}
	if resolvedWork == resolvedProject || dirs.StandardizeMacOSPath(resolvedWork) == resolvedProject {
		return ""
	}
	rel, err := filepath.Rel(resolvedProject, resolvedWork)
	if err == nil && rel != "" && !filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return replaceTabs(rel)
	}
	return replaceTabs(shortenPath(resolvedWork))
}

// pluralize mirrors packages/utils/src/format.ts pluralize.
func pluralize(singular string, n int) string {
	if n == 1 {
		return singular
	}
	switch {
	case strings.HasSuffix(singular, "ch"), strings.HasSuffix(singular, "sh"),
		strings.HasSuffix(singular, "s"), strings.HasSuffix(singular, "x"):
		return singular + "es"
	case strings.HasSuffix(singular, "y") && len(singular) > 1 && !isVowel(singular[len(singular)-2]):
		return singular[:len(singular)-1] + "ies"
	default:
		return singular + "s"
	}
}

func isVowel(b byte) bool {
	switch b {
	case 'a', 'e', 'i', 'o', 'u', 'A', 'E', 'I', 'O', 'U':
		return true
	}
	return false
}

// formatCount returns "1 <singular>" or "<n> <plural>".
func formatCount(singular string, n int) string {
	return fmt.Sprintf("%d %s", n, pluralize(singular, n))
}

func formatMoreItems(hidden int, singular string) string {
	return fmt.Sprintf("… %d more %s", hidden, pluralize(singular, hidden))
}

// formatDuration mirrors packages/utils/src/format.ts formatDuration.
func formatDuration(ms int64) string {
	const (
		sec  = 1_000
		min  = 60 * sec
		hour = 60 * min
		day  = 24 * hour
	)
	switch {
	case ms <= 0:
		return "0ms"
	case ms < sec:
		return fmt.Sprintf("%dms", ms)
	case ms < min:
		tenths := int64(math.Round(float64(ms) / 100))
		if tenths < 600 {
			return fmt.Sprintf("%.1fs", float64(tenths)/10)
		}
		return "1m"
	case ms < hour:
		m := ms / min
		s := (ms % min) / sec
		if s > 0 {
			return fmt.Sprintf("%dm%ds", m, s)
		}
		return fmt.Sprintf("%dm", m)
	case ms < day:
		h := ms / hour
		m := (ms % hour) / min
		if m > 0 {
			return fmt.Sprintf("%dh%dm", h, m)
		}
		return fmt.Sprintf("%dh", h)
	default:
		d := ms / day
		h := (ms % day) / hour
		if h > 0 {
			return fmt.Sprintf("%dd%dh", d, h)
		}
		return fmt.Sprintf("%dd", d)
	}
}

// formatAge mirrors packages/utils/src/format.ts formatAge.
func formatAge(ageSeconds float64) string {
	if ageSeconds <= 0 || math.IsNaN(ageSeconds) || math.IsInf(ageSeconds, 0) {
		return ""
	}
	sec := int64(ageSeconds)
	switch {
	case sec < 60:
		return fmt.Sprintf("%ds ago", sec)
	case sec < 3600:
		return fmt.Sprintf("%dm ago", sec/60)
	case sec < 86400:
		return fmt.Sprintf("%dh ago", sec/3600)
	case sec < 86400*30:
		return fmt.Sprintf("%dd ago", sec/86400)
	case sec < 86400*365:
		return fmt.Sprintf("%dmo ago", sec/(86400*30))
	default:
		return fmt.Sprintf("%dy ago", sec/(86400*365))
	}
}

// formatNumber formats integers compactly (e.g. 1200 -> 1.2K, 1500000 -> 1.5M).
func formatNumber(n int) string {
	if n < 0 {
		return "-" + formatNumber(-n)
	}
	switch {
	case n < 1000:
		return strconv.Itoa(n)
	case n < 1_000_000:
		v := float64(n) / 1000.0
		if v >= 100 || math.Abs(v-math.Round(v)) < 0.05 {
			return fmt.Sprintf("%.0fK", v)
		}
		return fmt.Sprintf("%.1fK", v)
	default:
		v := float64(n) / 1_000_000.0
		if v >= 100 || math.Abs(v-math.Round(v)) < 0.05 {
			return fmt.Sprintf("%.0fM", v)
		}
		return fmt.Sprintf("%.1fM", v)
	}
}

// formatBytes mirrors packages/utils/src/format.ts:62-67.
func formatBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%dB", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(n)/1024.0)
	case n < 1024*1024*1024:
		return fmt.Sprintf("%.1fMB", float64(n)/(1024.0*1024.0))
	default:
		return fmt.Sprintf("%.1fGB", float64(n)/(1024.0*1024.0*1024.0))
	}
}

// formatCodeFrameLine mirrors packages/tui/src/render/render-utils.ts:468-478.
func formatCodeFrameLine(marker, lineNumber, content string, lineNumberWidth int) string {
	markerText := strings.TrimSpace(marker)
	lineNumText := strings.TrimSpace(lineNumber)
	var gutter string
	switch {
	case markerText != "" && lineNumText != "":
		gutter = markerText + lineNumText
	case lineNumText != "":
		gutter = lineNumText
	default:
		gutter = markerText
	}
	padW := lineNumberWidth + 1
	if len(gutter) < padW {
		gutter = strings.Repeat(" ", padW-len(gutter)) + gutter
	}
	return gutter + "│" + content
}

// formatProgressBar mirrors packages/tui/src/components/progress-bar.ts.
func formatProgressBar(fraction float64, cells int, p palette, fillToken string) string {
	if cells <= 0 {
		return ""
	}
	f := math.Max(0, math.Min(1, fraction))
	filled := int(math.Round(f * float64(cells)))
	if filled > cells {
		filled = cells
	}
	return p.fg(fillToken, strings.Repeat(unicodeSymbols.progressFilled, filled)) +
		p.fg("dim", strings.Repeat(unicodeSymbols.progressEmpty, cells-filled))
}

// ── Map/Details extraction helpers (tolerate missing or mistyped fields) ─────

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

func strField(m map[string]any, keys ...string) string {
	if m == nil {
		return ""
	}
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return ""
}

func boolField(m map[string]any, key string) (bool, bool) {
	if m == nil {
		return false, false
	}
	v, ok := m[key]
	if !ok || v == nil {
		return false, false
	}
	b, ok := v.(bool)
	return b, ok
}

func intField(m map[string]any, key string) (int, bool) {
	f, ok := floatField(m, key)
	if !ok {
		return 0, false
	}
	return int(f), true
}

func int64Field(m map[string]any, key string) (int64, bool) {
	f, ok := floatField(m, key)
	if !ok {
		return 0, false
	}
	return int64(f), true
}

func floatField(m map[string]any, key string) (float64, bool) {
	if m == nil {
		return 0, false
	}
	v, ok := m[key]
	if !ok || v == nil {
		return 0, false
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
	case uint64:
		return float64(n), true
	}
	return 0, false
}

func stringSliceField(m map[string]any, key string) []string {
	if m == nil {
		return nil
	}
	raw, ok := m[key]
	if !ok || raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		if v != "" {
			return []string{v}
		}
	}
	return nil
}

func resultText(res sdk.ToolRenderResult) string {
	var parts []string
	for _, block := range res.Content {
		if strField(block, "type") == "text" {
			if txt := strField(block, "text"); txt != "" {
				parts = append(parts, txt)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func hasImageBlock(res sdk.ToolRenderResult) bool {
	for _, block := range res.Content {
		if strField(block, "type") == "image" {
			return true
		}
	}
	return false
}

func sortedMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// ── Frame & Tree layout helpers ──────────────────────────────────────────────

type blockSection struct {
	label     string
	lines     []string
	separator bool
}

type blockOpts struct {
	header              string
	headerMeta          string
	footerMeta          string
	state               string // "", "pending", "running", "partial", "success", "warning", "error"
	noBg                bool   // mirrors omp's applyBg: false (bash, todo, debug, fetch, github)
	borderColor         string
	bgToken             string
	contentPaddingLeft  *int
	contentPaddingRight *int
	sections            []blockSection
	width               int
}

// stateBgToken mirrors getStateBgColor in packages/tui/src/render/utils.ts:105-109
// and toolCardState in packages/tui/src/render/tool-card.ts:83-87.
func stateBgToken(state string) string {
	switch state {
	case "success":
		return "toolSuccessBg"
	case "error":
		return "toolErrorBg"
	case "pending", "running", "partial", "warning":
		return "toolPendingBg"
	default:
		return ""
	}
}

// stateBorderColor mirrors packages/tui/src/render/output-block.ts:84-92.
func stateBorderColor(state string) string {
	switch state {
	case "error":
		return "error"
	case "warning":
		return "warning"
	case "running", "pending", "partial":
		return "accent"
	default:
		return "dim"
	}
}

// outputBlockContentWidth mirrors packages/tui/src/render/output-block.ts:66-74.
func outputBlockContentWidth(width int, padLeft, padRight *int) int {
	l := 1
	if padLeft != nil && *padLeft >= 0 {
		l = *padLeft
	}
	r := l
	if padRight != nil && *padRight >= 0 {
		r = *padRight
	}
	return max(1, width-2-l-r)
}

// renderOutputBlock draws omp's rounded box frame (`╭─── header ───╮ ... ╰─── footer ───╯`).
// Guarantees every emitted row has tui.VisibleWidth(row) <= width for ANY width >= 1.
// upstream: packages/tui/src/render/output-block.ts renderOutputBlock
func (p palette) renderOutputBlock(opts blockOpts) []string {
	width := opts.width
	if width <= 0 {
		return nil
	}
	borderColor := opts.borderColor
	if borderColor == "" {
		borderColor = stateBorderColor(opts.state)
	}
	h := unicodeSymbols.boxHoriz
	v := unicodeSymbols.boxVert
	capStr := strings.Repeat(h, 3)
	bdr := func(s string) string { return p.fg(borderColor, s) }

	padL := 1
	if opts.contentPaddingLeft != nil && *opts.contentPaddingLeft >= 0 {
		padL = *opts.contentPaddingLeft
	}
	padR := padL
	if opts.contentPaddingRight != nil && *opts.contentPaddingRight >= 0 {
		padR = *opts.contentPaddingRight
	}
	leftPad := strings.Repeat(" ", padL)
	rightPad := strings.Repeat(" ", padR)
	vW := tui.VisibleWidth(v)
	contentWidth := max(0, width-vW-padL-padR-vW)

	renderBar := func(leftCh, rightCh, label, meta string) string {
		leftGlyphs := leftCh + capStr
		rightGlyph := rightCh
		labelText := label
		if meta != "" {
			if labelText != "" {
				// packages/tui/src/render/output-block.ts:189: `[row.label, row.meta].filter(Boolean).join(theme.sep.dot)`
				// leaves the separator dot in default fg (not wrapped in dim).
				labelText += unicodeSymbols.sepDot + meta
			} else {
				labelText = meta
			}
		}
		leftW := tui.VisibleWidth(leftGlyphs)
		rightW := tui.VisibleWidth(rightGlyph)
		if labelText == "" {
			fill := max(0, width-leftW-rightW)
			return clampRow(bdr(leftGlyphs)+bdr(strings.Repeat(h, fill))+bdr(rightGlyph), width)
		}
		rawLabel := " " + labelText + " "
		maxLabel := max(0, width-leftW-rightW)
		trimmed := truncateWidth(rawLabel, maxLabel)
		labelW := tui.VisibleWidth(trimmed)
		fill := max(0, width-leftW-labelW-rightW)
		return clampRow(bdr(leftGlyphs)+trimmed+bdr(strings.Repeat(h, fill))+bdr(rightGlyph), width)
	}

	renderContent := func(line string) string {
		if contentWidth <= 0 {
			return clampRow(bdr(v)+leftPad+rightPad+bdr(v), width)
		}
		padded := padRight(line, contentWidth)
		return clampRow(bdr(v)+leftPad+padded+rightPad+bdr(v), width)
	}

	var rows []string
	rows = append(rows, renderBar(unicodeSymbols.boxTopLeft, unicodeSymbols.boxTopRight, opts.header, opts.headerMeta))

	sections := opts.sections
	if len(sections) == 0 {
		sections = []blockSection{{}}
	}
	for i, sec := range sections {
		if sec.label != "" {
			rows = append(rows, renderBar(unicodeSymbols.boxTeeRight, unicodeSymbols.boxTeeLeft, sec.label, ""))
		} else if sec.separator && i > 0 {
			rows = append(rows, renderBar(unicodeSymbols.boxTeeRight, unicodeSymbols.boxTeeLeft, "", ""))
		}
		for _, raw := range sec.lines {
			for _, line := range strings.Split(raw, "\n") {
				trimmed := strings.TrimRightFunc(line, unicode.IsSpace)
				if contentWidth <= 0 {
					rows = append(rows, renderContent(""))
					continue
				}
				for _, wrapped := range wrapAnsi(trimmed, contentWidth) {
					rows = append(rows, renderContent(wrapped))
				}
			}
		}
	}

	// Footer overflow rule (output-block.ts:165-181): if footerMeta does not fit whole
	// in the bottom border, wrap it into content rows above a plain bottom border.
	footerMeta := opts.footerMeta
	footerCap := width - tui.VisibleWidth(unicodeSymbols.boxBottomLeft+capStr) - tui.VisibleWidth(unicodeSymbols.boxBottomRight)
	footerFits := footerMeta == "" || tui.VisibleWidth(" "+footerMeta+" ") <= footerCap
	if footerMeta != "" && !footerFits {
		if contentWidth > 0 {
			for _, wrapped := range wrapAnsi(footerMeta, contentWidth) {
				rows = append(rows, renderContent(wrapped))
			}
		}
		rows = append(rows, renderBar(unicodeSymbols.boxBottomLeft, unicodeSymbols.boxBottomRight, "", ""))
	} else {
		rows = append(rows, renderBar(unicodeSymbols.boxBottomLeft, unicodeSymbols.boxBottomRight, footerMeta, ""))
	}

	// Block background rule (upstream: output-block.ts:94-105, render/utils.ts:105-109,
	// tool-card.ts:233-246): when !opts.noBg (omp's applyBg !== false), paint
	// stateBgToken(opts.state) (or explicit opts.bgToken; borderColor=="error"
	// defaults to toolErrorBg if state was omitted).
	bgTok := ""
	if !opts.noBg {
		bgTok = opts.bgToken
		if bgTok == "" && opts.state != "" {
			bgTok = stateBgToken(opts.state)
		}
		if bgTok == "" && borderColor == "error" {
			bgTok = "toolErrorBg"
		}
	}
	bgOpen := ""
	if bgTok != "" {
		bgOpen = p.bgAnsi(bgTok)
	}
	for i, row := range rows {
		if tui.VisibleWidth(row) > width {
			row = clampRow(row, width)
		}
		if bgOpen != "" {
			row = strings.ReplaceAll(row, "\x1b[0m", "\x1b[0m"+bgOpen)
			row = strings.ReplaceAll(row, "\x1b[m", "\x1b[m"+bgOpen)
			row = strings.ReplaceAll(row, "\x1b[49m", "\x1b[49m"+bgOpen)
			row = bgOpen + row + "\x1b[49m"
		}
		rows[i] = row
	}
	return rows
}

// renderTreeGroups formats multi-line item groups with `├─`/`└─` tree connectors
// and `│  `/`   ` continuation prefixes, plus an optional trailing summary row.
// When summary != "", all item groups use `├─` and the summary uses `└─` (tree-list.ts:78-92).
func (p palette) renderTreeGroups(groups [][]string, trailingSummary string) []string {
	totalRoots := len(groups)
	if trailingSummary != "" {
		totalRoots++
	}
	if totalRoots == 0 {
		return nil
	}
	var out []string
	for i, grp := range groups {
		isLast := i == totalRoots-1
		branch := unicodeSymbols.treeBranch
		cont := unicodeSymbols.treeVert + "  "
		if isLast {
			branch = unicodeSymbols.treeLast
			cont = "   "
		}
		branchPrefix := p.fg("dim", branch) + " "
		contPrefix := p.fg("dim", cont)
		for j, line := range grp {
			if j == 0 {
				out = append(out, branchPrefix+line)
			} else {
				out = append(out, contPrefix+line)
			}
		}
	}
	if trailingSummary != "" {
		out = append(out, p.fg("dim", unicodeSymbols.treeLast)+" "+p.fg("muted", trailingSummary))
	}
	return out
}

// renderTreeLines formats single-line items with `├─`/`└─` connectors and
// omp's `… N more <itemType>` summary without an expand hint (render/tree-list.ts:164-173).
func (p palette) renderTreeLines(items []string, expanded bool, maxCollapsed int, itemType string) []string {
	if len(items) == 0 {
		return nil
	}
	limit := len(items)
	if !expanded && maxCollapsed > 0 && limit > maxCollapsed {
		limit = maxCollapsed
	}
	hidden := len(items) - limit
	groups := make([][]string, limit)
	for i := range limit {
		groups[i] = []string{items[i]}
	}
	summary := ""
	if hidden > 0 {
		summary = formatMoreItems(hidden, itemType)
	}
	return p.renderTreeGroups(groups, summary)
}

// capPreviewLines mirrors packages/tui/src/render/render-utils.ts:376-389:
// caps lines to a tail window when collapsed, prepending `… N earlier lines ⟦Ctrl+O: Expand⟧`.
func (p palette) capPreviewLines(lines []string, maxLines int, expanded bool, prefix string, showExpandHint bool) []string {
	if expanded {
		return lines
	}
	if maxLines <= 0 {
		maxLines = previewWindowFallback
	}
	if len(lines) <= maxLines {
		return lines
	}
	var visible []string
	if maxLines > 1 {
		visible = lines[len(lines)-(maxLines-1):]
	}
	hidden := len(lines) - len(visible)
	hint := ""
	if showExpandHint {
		hint = p.formatExpandHint(false, true)
	}
	marker := fmt.Sprintf("… %d earlier %s", hidden, pluralize("line", hidden))
	if hint != "" {
		marker += " " + hint
	}
	out := make([]string, 0, 1+len(visible))
	out = append(out, prefix+p.fg("dim", marker))
	out = append(out, visible...)
	return out
}

// formatOutputPaneLines mirrors packages/tui/src/render/output-pane.ts:66-118.
type outputPaneOpts struct {
	lines                 []string
	expanded              bool
	collapsedMaxLines     int
	expandedMaxLines      int    // 0 means uncapped (bounded defensively by maxExpandedOutputLines)
	edge                  string // "head" or "tail"
	visual                bool
	width                 int
	styleLine             func(string) string
	hideMarker            bool
	hideExpandHint        bool
	showExpandHintWhenFit bool
	formatHidden          func(hidden, shown, total int, edge string) string
}

func (p palette) formatOutputPaneLines(opts outputPaneOpts) ([]string, int) {
	edge := opts.edge
	if edge == "" {
		edge = "head"
	}
	rawLines := opts.lines
	// Defensive cap on extreme payloads before wrapping/styling.
	var droppedExtra int
	if len(rawLines) > maxExpandedOutputLines {
		if edge == "tail" {
			droppedExtra = len(rawLines) - maxExpandedOutputLines
			rawLines = rawLines[droppedExtra:]
		} else {
			droppedExtra = len(rawLines) - maxExpandedOutputLines
			rawLines = rawLines[:maxExpandedOutputLines]
		}
	}
	styled := make([]string, len(rawLines))
	for i, l := range rawLines {
		if opts.styleLine != nil {
			styled[i] = opts.styleLine(l)
		} else {
			styled[i] = l
		}
	}

	limit := -1
	if opts.expanded {
		if opts.expandedMaxLines > 0 {
			limit = opts.expandedMaxLines
		}
	} else if opts.collapsedMaxLines >= 0 {
		limit = opts.collapsedMaxLines
	}

	var visible []string
	hidden := droppedExtra
	if limit >= 0 {
		if limit == 0 {
			visible = nil
			hidden += len(styled)
		} else if opts.visual && opts.width > 0 {
			var wrapped []string
			for _, l := range styled {
				wrapped = append(wrapped, wrapAnsi(l, opts.width)...)
			}
			if len(wrapped) > limit {
				hidden += len(wrapped) - limit
				if edge == "tail" {
					visible = wrapped[len(wrapped)-limit:]
				} else {
					visible = wrapped[:limit]
				}
			} else {
				visible = wrapped
			}
		} else if len(styled) > limit {
			hidden += len(styled) - limit
			if edge == "tail" {
				visible = styled[len(styled)-limit:]
			} else {
				visible = styled[:limit]
			}
		} else {
			visible = styled
		}
	} else {
		visible = styled
	}

	out := append([]string(nil), visible...)
	if hidden > 0 && !opts.hideMarker {
		var label string
		if opts.formatHidden != nil {
			label = opts.formatHidden(hidden, len(visible), hidden+len(visible), edge)
		} else if edge == "tail" {
			label = fmt.Sprintf("… (%d earlier lines, showing %d of %d)", hidden, len(visible), hidden+len(visible))
		} else {
			label = formatMoreItems(hidden, "line")
		}
		hint := ""
		if !opts.hideExpandHint {
			hint = p.formatExpandHint(opts.expanded, true)
		}
		marker := label
		if hint != "" {
			marker += " " + hint
		}
		styledMarker := p.fg("dim", marker)
		if edge == "tail" {
			out = append([]string{styledMarker}, out...)
		} else {
			out = append(out, styledMarker)
		}
	} else if !opts.expanded && opts.showExpandHintWhenFit {
		if hint := p.formatExpandHint(false, true); hint != "" {
			out = append(out, hint)
		}
	}
	return out, hidden
}

// spillURL formats an artifact/spill id as omp's `local://<id>.log` URL.
// Preserves values that already carry a URL scheme or `.log` suffix.
// upstream: packages/utils/src/spill-url.ts:17
func spillURL(artifactID string) string {
	if artifactID == "" {
		return ""
	}
	if strings.Contains(artifactID, "://") {
		return artifactID
	}
	if strings.HasSuffix(artifactID, ".log") || strings.Contains(artifactID, "/") {
		return "local://" + artifactID
	}
	return "local://" + artifactID + ".log"
}

// formatFullOutputReference mirrors packages/tui/src/tools/output-meta.ts:174-179.
func formatFullOutputReference(artifactID string, elidedBytes int64) string {
	u := spillURL(artifactID)
	if elidedBytes > 0 {
		return fmt.Sprintf("Read %s for a head/tail sample of the output; %s from its middle was not saved", u, formatBytes(elidedBytes))
	}
	return fmt.Sprintf("Read %s for full output", u)
}

// formatTruncationMetaNotice mirrors packages/tui/src/tools/output-meta.ts:272-335.
func formatTruncationMetaNotice(trunc map[string]any, source map[string]any) string {
	if trunc == nil {
		return ""
	}
	aid := strField(trunc, "artifactId")
	elidedBytes, _ := int64Field(trunc, "artifactElidedBytes")
	var artRef string
	if aid != "" {
		if strField(source, "type") == "report" {
			artRef = fmt.Sprintf("Read %s for full report (%s)", spillURL(aid), strField(source, "value"))
		} else {
			artRef = formatFullOutputReference(aid, elidedBytes)
		}
	}

	dir := strField(trunc, "direction")
	totLines, _ := intField(trunc, "totalLines")
	outLines, _ := intField(trunc, "outputLines")
	totBytes, _ := int64Field(trunc, "totalBytes")
	outBytes, _ := int64Field(trunc, "outputBytes")
	nextOff, hasNextOff := intField(trunc, "nextOffset")

	var notice string
	if dir == "middle" {
		head := asMap(trunc["headRange"])
		tail := asMap(trunc["tailRange"])
		elidedB, hasEB := int64Field(trunc, "elidedBytes")
		if !hasEB {
			elidedB = max(int64(0), totBytes-outBytes)
		}
		elidedL, hasEL := intField(trunc, "elidedLines")
		if !hasEL {
			elidedL = max(0, totLines-outLines)
		}
		headPart := ""
		if head != nil {
			s, _ := intField(head, "start")
			e, _ := intField(head, "end")
			headPart = fmt.Sprintf("lines %d-%d", s, e)
		}
		tailPart := ""
		if tail != nil {
			s, _ := intField(tail, "start")
			e, _ := intField(tail, "end")
			tailPart = fmt.Sprintf("lines %d-%d", s, e)
		}
		switch {
		case headPart != "" && tailPart != "":
			notice = fmt.Sprintf("Showing %s and %s of %d; %d middle %s (%s) elided",
				headPart, tailPart, totLines, elidedL, pluralize("line", elidedL), formatBytes(elidedB))
		case elidedB > 0:
			notice = fmt.Sprintf("Showing head and tail bytes of %d %s; %s elided",
				totLines, pluralize("line", totLines), formatBytes(elidedB))
		default:
			notice = fmt.Sprintf("Showing %d of %d lines; middle elided", min(outLines, totLines), totLines)
		}
		if hasNextOff {
			notice += fmt.Sprintf(". Use :%d- to continue", nextOff)
		}
		if artRef != "" {
			notice += ". " + artRef
		}
		return notice
	}

	if partial, _ := boolField(trunc, "partialLine"); partial {
		line := 1
		if sr := asMap(trunc["shownRange"]); sr != nil {
			if s, ok := intField(sr, "start"); ok {
				line = s
			}
		}
		notice = fmt.Sprintf("Showing line %d (partial, %s of %s) of %d",
			line, formatBytes(outBytes), formatBytes(totBytes), totLines)
		if artRef != "" {
			notice += ". " + artRef
		}
		return notice
	}

	if sr := asMap(trunc["shownRange"]); sr != nil {
		s, okS := intField(sr, "start")
		e, okE := intField(sr, "end")
		if okS && okE && e >= s {
			notice = fmt.Sprintf("Showing lines %d-%d of %d", s, e, totLines)
		}
	}
	if notice == "" {
		if totLines > 0 {
			notice = fmt.Sprintf("Showing %d of %d lines", outLines, totLines)
		} else if totBytes > 0 {
			notice = fmt.Sprintf("Showing %s of %s", formatBytes(outBytes), formatBytes(totBytes))
		} else {
			notice = "Output truncated"
		}
	}
	if strField(trunc, "truncatedBy") == "bytes" {
		maxB, ok := int64Field(trunc, "maxBytes")
		if !ok || maxB <= 0 {
			maxB = outBytes
		}
		notice += fmt.Sprintf(" (%s limit)", formatBytes(maxB))
	}
	if hasNextOff {
		notice += fmt.Sprintf(". Use :%d- to continue", nextOff)
	}
	if artRef != "" {
		notice += ". " + artRef
	}
	return notice
}

// formatMetaTruncation extracts truncation/artifact warning text from details.meta (or top-level truncation).
// upstream: packages/tui/src/tools/output-meta.ts formatStyledTruncationWarning
func (p palette) formatMetaTruncation(meta map[string]any) string {
	if meta == nil {
		return ""
	}
	var parts []string
	if trunc := asMap(meta["truncation"]); trunc != nil {
		if s := formatTruncationMetaNotice(trunc, asMap(meta["source"])); s != "" {
			parts = append(parts, s)
		}
	}
	if artErr := strField(meta, "artifactError"); artErr != "" {
		parts = append(parts, fmt.Sprintf("Full output was not saved completely (artifact %s failed)", artErr))
	}
	if len(parts) == 0 {
		return ""
	}
	return p.fg("warning", p.wrapBrackets(strings.Join(parts, ". ")))
}

// formatMinimizedMeta mirrors packages/tui/src/tools/output-meta.ts:355-359.
func formatMinimizedMeta(m map[string]any) string {
	if m == nil {
		return ""
	}
	filter := truncateWidth(flattenLine(strField(m, "filter")), truncateShort)
	inB, okIn := int64Field(m, "inputBytes")
	outB, okOut := int64Field(m, "outputBytes")
	if !okIn || !okOut {
		if filter == "" {
			return ""
		}
		return filter
	}
	if filter == "" {
		return fmt.Sprintf("%s → %s", formatBytes(inB), formatBytes(outB))
	}
	return fmt.Sprintf("%s %s → %s", filter, formatBytes(inB), formatBytes(outB))
}
