package pi_background_tasks

// Glyph styles of the UI chrome. upstream: extensions/glyphs.ts.

const (
	localConfigID  = "@vanillagreen/pi-background-tasks"
	globalConfigID = "@vanillagreen/pi-tool-renderer"
)

type frameGlyph struct{ tl, tr, bl, br, h, v string }

type treeGlyphs struct{ mid, last, stem, blank string }

type glyphSet struct {
	frame                                             frameGlyph
	line                                              string
	tree                                              treeGlyphs
	bullet, emptyBullet, dot, ok, fail, warn, diamond string
	prompt, ellipsis, arrow, codeBar                  string
}

var unicodeGlyphs = &glyphSet{
	frame: frameGlyph{"┏", "┓", "┗", "┛", "━", "┃"}, line: "─",
	tree:   treeGlyphs{"├─ ", "└─ ", "│  ", "   "},
	bullet: "● ", emptyBullet: "○ ", dot: " · ", ok: "✓", fail: "✗", warn: "▲", diamond: "◆", prompt: "π", ellipsis: "…", arrow: "→", codeBar: "▌",
}

var asciiGlyphs = &glyphSet{
	frame: frameGlyph{"+", "+", "+", "+", "-", "|"}, line: "-",
	tree:   treeGlyphs{"|-- ", "`-- ", "|  ", "   "},
	bullet: "* ", emptyBullet: "o ", dot: " - ", ok: "+", fail: "x", warn: "!", diamond: "*", prompt: "pi", ellipsis: "...", arrow: "->", codeBar: "|",
}

func asGlyphStyle(v any) string {
	if s, ok := v.(string); ok && (s == "unicode" || s == "ascii") {
		return s
	}
	return ""
}

// glyphStyle is the style the global tool-renderer override forces, else this package's own `glyphStyle` or the older
// `treeStyle`, else unicode.
func glyphStyle(cwd string) string {
	if forced := asGlyphStyle(settings.config(globalConfigID, cwd)["globalGlyphStyleOverride"]); forced != "" {
		return forced
	}
	local := settings.config(localConfigID, cwd)
	if s := asGlyphStyle(local["glyphStyle"]); s != "" {
		return s
	}
	if s := asGlyphStyle(local["treeStyle"]); s != "" {
		return s
	}
	return "unicode"
}

func glyphsFor(cwd string) *glyphSet {
	if glyphStyle(cwd) == "ascii" {
		return asciiGlyphs
	}
	return unicodeGlyphs
}

// treeGlyph is the branch glyph of a tree row: "├", "└" or "│".
func treeGlyph(branch, cwd string) string {
	tree := glyphsFor(cwd).tree
	switch branch {
	case "│":
		return tree.stem
	case "└":
		return tree.last
	}
	return tree.mid
}
