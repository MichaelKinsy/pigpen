package powerline_footer

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Color resolution, user overrides (theme.json) and icons. upstream: theme.ts, colors.ts, icons.ts, separators.ts.

var rainbowColors = []string{"#b281d6", "#d787af", "#febc38", "#e4c00f", "#89d281", "#00afaf", "#178fb9", "#b281d6"}

const themeCacheTTLMs = 5000

type themeConfigCache struct {
	value     *jsObject
	at        int64
	key       string
	populated bool
}

var (
	themeMu    sync.Mutex // the footer renderer reads the theme on the SDK's goroutine
	themeCache themeConfigCache
)

func themePaths() []string {
	paths := []string{getAgentPath("extensions", "powerline-footer", "theme.json")}
	// The original also looks beside its own module (join(extDir, "theme.json")); a Go extension has no such directory.
	return paths
}

// loadThemeConfig reads theme.json (colors and icons), cached for five seconds. A missing or malformed file is an empty config.
func loadThemeConfig() *jsObject {
	themeMu.Lock()
	defer themeMu.Unlock()
	now := clock()
	key := strings.Join(themePaths(), "\x00")
	if themeCache.populated && themeCache.key == key && now-themeCache.at < themeCacheTTLMs {
		return themeCache.value
	}
	themeCache = themeConfigCache{value: newObject(), at: now, key: key, populated: true}
	for _, p := range themePaths() {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		parsed, err := parseJSON(data)
		if err != nil {
			continue
		}
		if o, ok := parsed.(*jsObject); ok {
			themeCache.value = o
		}
		return themeCache.value
	}
	return themeCache.value
}

// userThemeColors is sanitizeUserThemeOverrides over the theme.json colors.
func userThemeColors() colorScheme {
	out := colorScheme{}
	cfg := loadThemeConfig()
	colors, ok := cfg.vals["colors"].(*jsObject)
	if !ok {
		return out
	}
	for _, k := range colors.order() {
		if _, known := defaultColors[k]; !known {
			continue
		}
		raw, ok := colors.vals[k].(string)
		if !ok {
			continue
		}
		if c := jsTrim(raw); c != "" {
			out[k] = c
		}
	}
	return out
}

// resolveColor: user overrides, then the preset's colors, then the defaults.
func resolveColor(semantic string, preset colorScheme) string {
	if c, ok := userThemeColors()[semantic]; ok {
		return c
	}
	if c, ok := preset[semantic]; ok {
		return c
	}
	return defaultColors[semantic]
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func hexToAnsi(hex string) string {
	r, _ := strconv.ParseUint(hex[1:3], 16, 8)
	g, _ := strconv.ParseUint(hex[3:5], 16, 8)
	b, _ := strconv.ParseUint(hex[5:7], 16, 8)
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, b)
}

// applyColor colors text with a theme token, a #rrggbb value or the rainbow. A token the theme rejects falls back to "text".
func applyColor(th theme, color, text string) string {
	if color == "rainbow" {
		return rainbow(text)
	}
	if hexColor.MatchString(color) {
		return hexToAnsi(color) + text + "\x1b[0m"
	}
	if out, err := th.Fg(color, text); err == nil {
		return out
	}
	out, _ := th.Fg("text", text)
	return out
}

func fg(th theme, semantic, text string, preset colorScheme) string {
	return applyColor(th, resolveColor(semantic, preset), text)
}

// rainbow gives each character but spaces and colons the next color of the gradient.
func rainbow(text string) string {
	var b strings.Builder
	i := 0
	for _, ch := range text {
		if ch == ' ' || ch == ':' {
			b.WriteRune(ch)
			continue
		}
		b.WriteString(hexToAnsi(rainbowColors[i%len(rainbowColors)]))
		b.WriteRune(ch)
		i++
	}
	return b.String() + "\x1b[0m"
}

// Chrome colors of the separator (colors.ts): the gray 256-color code the original uses between segments.
const (
	ansiReset     = "\x1b[0m"
	separatorAnsi = "\x1b[38;5;244m"
)

func getIcons() iconSet {
	icons := asciiIcons
	if hasNerdFonts() {
		icons = nerdIcons
	}
	if over, ok := loadThemeConfig().vals["icons"].(*jsObject); ok {
		for _, k := range iconKeys {
			if v, ok := over.vals[k].(string); ok {
				*icons.field(k) = v
			}
		}
	}
	return icons
}

func getSeparatorChars() separatorChars {
	if hasNerdFonts() {
		return nerdSeparators
	}
	return asciiSeparators
}

type separatorDef struct{ Left, Right string }

func getSeparator(style string) separatorDef {
	c := getSeparatorChars()
	switch style {
	case "powerline":
		return separatorDef{c.PowerlineLeft, c.PowerlineRight}
	case "powerline-thin":
		return separatorDef{c.PowerlineThinLeft, c.PowerlineThinRight}
	case "slash":
		return separatorDef{c.Slash, c.Slash}
	case "pipe":
		return separatorDef{c.Pipe, c.Pipe}
	case "block":
		return separatorDef{c.Block, c.Block}
	case "none":
		return separatorDef{c.Space, c.Space}
	case "ascii":
		return separatorDef{c.AsciiLeft, c.AsciiRight}
	case "dot":
		return separatorDef{c.Dot, c.Dot}
	case "chevron":
		return separatorDef{"›", "‹"}
	case "star":
		return separatorDef{"✦", "✦"}
	}
	return getSeparator("powerline-thin")
}

const sepDot = " · "

func getThinkingText(level string) (string, bool) {
	table := thinkingTextUnicode
	if hasNerdFonts() {
		table = thinkingTextNerd
	}
	t, ok := table[level]
	return t, ok
}

var _ = filepath.Join
