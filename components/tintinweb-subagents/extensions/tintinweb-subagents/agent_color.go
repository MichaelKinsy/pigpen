package tintinweb_subagents

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Claude Code-compatible agent name badges: the configured color is the background, the text a contrasting
// foreground. upstream: src/agent-color.ts.

var namedAgentColors = map[string]string{
	// Claude Code's eight subagent colors, as its default theme renders them.
	"red": "#DC2626", "blue": "#6A9BCC", "green": "#16A34A", "yellow": "#CA8A04",
	"purple": "#827DBD", "orange": "#D97757", "pink": "#C46686", "cyan": "#0891B2",
	// Agency Agents palette aliases.
	"amber": "#F59E0B", "teal": "#008080", "indigo": "#6366F1", "gold": "#EAB308",
	"neon-green": "#10B981", "neon-cyan": "#06B6D4", "metallic-blue": "#3B82F6", "violet": "#8B5CF6",
	"rose": "#F43F5E", "lime": "#84CC16", "gray": "#6B7280", "grey": "#6B7280",
	"fuchsia": "#D946EF", "slate": "#64748B", "navy": "#1E3A8A",
}

var (
	cubeValues = []int{0, 95, 135, 175, 215, 255}
	grayValues = func() []int {
		v := make([]int, 24)
		for i := range v {
			v[i] = 8 + i*10
		}
		return v
	}()
	hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

type rgb struct{ r, g, b int }

// agentNameStyle is upstream AgentNameStyle.
type agentNameStyle struct {
	// FallbackColor is the existing theme foreground used when no valid agent color is configured.
	FallbackColor string
	// RestoreBackground is reapplied after the badge instead of resetting the background.
	RestoreBackground string
	Bold              bool
}

// resolveAgentColor resolves Claude Code/Agency Agents color syntax to a normalized #RRGGBB.
func resolveAgentColor(value string) (string, bool) {
	if value == "" {
		return "", false
	}
	normalized := strings.ToLower(strings.TrimSpace(value))
	resolved := normalized
	if named, ok := namedAgentColors[normalized]; ok {
		resolved = named
	}
	if !hexColor.MatchString(resolved) {
		return "", false
	}
	return strings.ToUpper(resolved), true
}

func parseHex(hex string) rgb {
	n := func(s string) int { v, _ := strconv.ParseInt(s, 16, 32); return int(v) }
	return rgb{n(hex[1:3]), n(hex[3:5]), n(hex[5:7])}
}

// nearest is the index of the entry in values closest to value (the first, on a tie).
func nearest(values []int, value int) int {
	best := 0
	for i, v := range values {
		if abs(value-v) < abs(value-values[best]) {
			best = i
		}
	}
	return best
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// jsRound is Math.round: halves round toward +Infinity.
func jsRound(x float64) float64 { return math.Floor(x + 0.5) }

// rgbTo256 quantizes to the xterm-256 palette the way pi's own theme does, returning the index to emit and the
// color the terminal will actually show.
func rgbTo256(c rgb) (int, rgb) {
	r, g, b := c.r, c.g, c.b
	ri, gi, bi := nearest(cubeValues, r), nearest(cubeValues, g), nearest(cubeValues, b)
	distance := func(o rgb) float64 {
		return 0.299*math.Pow(float64(r-o.r), 2) + 0.587*math.Pow(float64(g-o.g), 2) + 0.114*math.Pow(float64(b-o.b), 2)
	}
	grayIndex := nearest(grayValues, int(jsRound(0.299*float64(r)+0.587*float64(g)+0.114*float64(b))))
	gv := grayValues[grayIndex]
	gray := rgb{gv, gv, gv}
	cube := rgb{cubeValues[ri], cubeValues[gi], cubeValues[bi]}
	// Only near-neutral colors may take the gray ramp; anything else keeps its tint.
	if max(r, g, b)-min(r, g, b) < 10 && distance(gray) < distance(cube) {
		return 232 + grayIndex, gray
	}
	return 16 + 36*ri + 6*gi + bi, cube
}

func ansiColor(layer string, color rgb, index int, indexed bool) string {
	code := 38
	if layer == "background" {
		code = 48
	}
	if indexed {
		return "\x1b[" + strconv.Itoa(code) + ";5;" + strconv.Itoa(index) + "m"
	}
	return "\x1b[" + strconv.Itoa(code) + ";2;" + strconv.Itoa(color.r) + ";" + strconv.Itoa(color.g) + ";" + strconv.Itoa(color.b) + "m"
}

func relativeLuminance(c rgb) float64 {
	linear := func(v int) float64 {
		ch := float64(v) / 255
		if ch <= 0.04045 {
			return ch / 12.92
		}
		return math.Pow((ch+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(c.r) + 0.7152*linear(c.g) + 0.0722*linear(c.b)
}

// renderAgentNameLabel renders one name as a padded background badge when color is valid; black or white text is
// picked by WCAG contrast. An invalid or omitted color keeps the caller's theme styling.
func renderAgentNameLabel(name, color string, th uiTheme, style agentNameStyle) string {
	resolved, ok := resolveAgentColor(color)
	if !ok {
		text := name
		if style.Bold {
			text = th.Bold(name)
		}
		if style.FallbackColor != "" {
			return th.Fg(style.FallbackColor, text)
		}
		return text
	}
	c := parseHex(resolved)
	shown := c
	quantized := th.GetColorMode() == "256color"
	index := 0
	if quantized {
		index, shown = rgbTo256(c)
	}
	contrasting := rgb{255, 255, 255}
	if relativeLuminance(shown) > 0.179 {
		contrasting = rgb{}
	}
	label := " " + name + " "
	if style.Bold {
		label = th.Bold(label)
	}
	fgIndex := 0
	if quantized {
		fgIndex, _ = rgbTo256(contrasting)
	}
	restore := style.RestoreBackground
	if restore == "" {
		restore = "\x1b[49m"
	}
	return ansiColor("background", c, index, quantized) + ansiColor("foreground", contrasting, fgIndex, quantized) + label + "\x1b[39m" + restore
}

// hasAgentBadge reports whether an agent renders as a badge, that is, has a valid configured color.
func hasAgentBadge(typ string) bool {
	if typ == "" {
		return false
	}
	_, ok := resolveAgentColor(agentConfigView(typ).color)
	return ok
}

// renderAgentName renders a registered agent's display name with its configured color.
func renderAgentName(typ string, th uiTheme, style agentNameStyle) string {
	if typ == "" {
		return renderAgentNameLabel("Agent", "", th, style)
	}
	c := agentConfigView(typ)
	return renderAgentNameLabel(c.displayName, c.color, th, style)
}

// agentConfigViewT is what the surfaces read of an agent type. upstream: agent-types.ts getConfig.
type agentConfigViewT struct {
	displayName, color, promptMode string
}

// agentConfigView is upstream getConfig: the type's display name, color and prompt mode; an unknown or disabled type
// reads as general-purpose.
func agentConfigView(typ string) agentConfigViewT {
	reg := currentRegistry()
	if key, ok := reg.resolveKey(typ); ok {
		if c := reg.m[key]; c != nil && c.Enabled {
			name := c.DisplayName
			if name == "" {
				name = c.Name
			}
			return agentConfigViewT{name, c.Color, c.PromptMode}
		}
	}
	if c := reg.m["general-purpose"]; c != nil && c.Enabled {
		name := c.DisplayName
		if name == "" {
			name = c.Name
		}
		return agentConfigViewT{name, c.Color, c.PromptMode}
	}
	return agentConfigViewT{"Agent", "", "append"} // the absolute fallback
}
