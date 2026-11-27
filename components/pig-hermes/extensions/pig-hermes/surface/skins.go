package surface

import (
	"fmt"
	"strings"
)

// Skin defines a Hermes TUI visual theme palette and branding tokens.
// Matches hermes-agent/hermes_cli/skin_engine.py (_BUILTIN_SKINS).
type Skin struct {
	Name          string
	Description   string
	Colors        map[string]string
	LightColors   map[string]string
	Branding      map[string]string
	ToolPrefix    string
	PromptSymbol  string
	ResponseLabel string
	Goodbye       string
	Symbol        string
}

// Color returns the named color from the skin or fallback if unset.
func (s Skin) Color(key, fallback string) string {
	if v, ok := s.Colors[key]; ok && v != "" {
		return v
	}
	return fallback
}

// HermesLogo is the 6-line block-letter HERMES-AGENT banner.
const HermesLogo = "" +
	"██╗  ██╗███████╗██████╗ ███╗   ███╗███████╗███████╗       █████╗  ██████╗ ███████╗███╗   ██╗████████╗\n" +
	"██║  ██║██╔════╝██╔══██╗████╗ ████║██╔════╝██╔════╝      ██╔══██╗██╔════╝ ██╔════╝████╗  ██║╚══██╔══╝\n" +
	"███████║█████╗  ██████╔╝██╔████╔██║█████╗  ███████╗█████╗███████║██║  ███╗█████╗  ██╔██╗ ██║   ██║\n" +
	"██╔══██║██╔══╝  ██╔══██╗██║╚██╔╝██║██╔══╝  ╚════██║╚════╝██╔══██║██║   ██║██╔══╝  ██║╚██╗██║   ██║\n" +
	"██║  ██║███████╗██║  ██║██║ ╚═╝ ██║███████╗███████║      ██║  ██║╚██████╔╝███████╗██║ ╚████║   ██║\n" +
	"╚═╝  ╚═╝╚══════╝╚═╝  ╚═╝╚═╝     ╚═╝╚══════╝╚══════╝      ╚═╝  ╚═╝ ╚═════╝ ╚══════╝╚═╝  ╚═══╝   ╚═╝"

var builtinSkinOrder = []string{
	"default",
	"ares",
	"mono",
	"slate",
	"daylight",
	"warm-lightmode",
	"poseidon",
	"sisyphus",
	"charizard",
}

func makeBranding(who, symbol, goodbye, prompt, helpHeader string) map[string]string {
	if prompt == "" {
		prompt = symbol
	}
	if helpHeader == "" {
		helpHeader = fmt.Sprintf("(%s) Available Commands", symbol)
	}
	return map[string]string{
		"agent_name":     who + " Agent",
		"welcome":        fmt.Sprintf("Welcome to %s Agent! Type your message or /help for commands.", who),
		"goodbye":        goodbye,
		"response_label": fmt.Sprintf(" %s %s ", symbol, who),
		"prompt_symbol":  prompt,
		"help_header":    helpHeader,
	}
}

var builtinSkins = map[string]Skin{
	"default": {
		Name:        "default",
		Description: "Classic Hermes — gold and kawaii",
		Colors: map[string]string{
			"banner_border": "#CD7F32", "banner_title": "#FFD700", "banner_accent": "#FFBF00",
			"banner_dim": "#B8860B", "banner_text": "#FFF8DC", "ui_accent": "#FFBF00",
			"ui_label": "#DAA520", "ui_ok": "#4caf50", "ui_error": "#ef5350", "ui_warn": "#ffa726",
			"prompt": "#FFF8DC", "input_rule": "#CD7F32", "response_border": "#FFD700",
			"status_bar_bg": "#1a1a2e", "status_bar_text": "#C0C0C0",
			"status_bar_strong": "#FFD700", "status_bar_dim": "#8A7A4A",
			"status_bar_good": "#8FBC8F", "status_bar_warn": "#FFD700", "status_bar_bad": "#FF8C00",
			"status_bar_critical": "#FF6B6B", "session_label": "#DAA520",
			"session_border": "#8B8682", "completion_menu_bg": "#1a1a2e",
			"completion_menu_current_bg": "#333355", "selection_bg": "#3a3a55",
			"shell_dollar": "#4dabf7", "voice_status_bg": "#1a1a2e",
		},
		LightColors: map[string]string{
			"banner_title": "#C8961E", "banner_accent": "#D89B04", "banner_dim": "#B8860B",
			"banner_text": "#5C4718", "ui_accent": "#D89B04", "ui_label": "#A97E10",
			"ui_ok": "#2E7D32", "ui_error": "#C62828", "ui_warn": "#D97706", "prompt": "#5C4718",
			"response_border": "#C8961E", "session_label": "#A97E10", "status_bar_text": "#6F6F6F",
			"status_bar_strong": "#C8961E", "status_bar_dim": "#9A8A5A",
			"status_bar_good": "#2E7D32", "status_bar_warn": "#C8961E", "status_bar_bad": "#C2410C",
			"status_bar_critical": "#B91C1C", "shell_dollar": "#1E6FC0",
			"completion_menu_bg": "#F5F5F5", "completion_menu_current_bg": "#E0D1BF",
			"selection_bg": "#D4E4F7", "status_bar_bg": "#F5F5F5", "voice_status_bg": "#F5F5F5",
		},
		Branding:      makeBranding("Hermes", "☤", "Goodbye! ☤", "❯", "(^_^)? Available Commands"),
		ToolPrefix:    "┊",
		PromptSymbol:  "❯",
		ResponseLabel: " ☤ Hermes ",
		Goodbye:       "Goodbye! ☤",
		Symbol:        "☤",
	},
	"ares": {
		Name:        "ares",
		Description: "War-god theme — crimson and bronze",
		Colors: map[string]string{
			"banner_border": "#A93333", "banner_title": "#C7A96B", "banner_accent": "#DD4A3A",
			"banner_dim": "#905151", "banner_text": "#F1E6CF", "ui_accent": "#DD4A3A",
			"ui_label": "#C7A96B", "ui_ok": "#4caf50", "ui_error": "#ef5350", "ui_warn": "#ffa726",
			"prompt": "#F1E6CF", "input_rule": "#A93333", "response_border": "#C7A96B",
			"status_bar_bg": "#2A1212", "status_bar_text": "#F1E6CF",
			"status_bar_strong": "#C7A96B", "status_bar_dim": "#756054",
			"status_bar_good": "#7BC96F", "status_bar_warn": "#C7A96B", "status_bar_bad": "#DD4A3A",
			"status_bar_critical": "#EF5350", "session_label": "#C7A96B",
			"session_border": "#6E584B", "completion_menu_bg": "#2A1212",
			"completion_menu_current_bg": "#5C221D", "selection_bg": "#692620",
			"shell_dollar": "#DD4A3A", "voice_status_bg": "#2A1212",
		},
		Branding:      makeBranding("Ares", "⚔", "Farewell, warrior! ⚔", "", ""),
		ToolPrefix:    "╎",
		PromptSymbol:  "⚔",
		ResponseLabel: " ⚔ Ares ",
		Goodbye:       "Farewell, warrior! ⚔",
		Symbol:        "⚔",
	},
	"mono": {
		Name:        "mono",
		Description: "Monochrome — clean grayscale",
		Colors: map[string]string{
			"banner_border": "#5E5E5E", "banner_title": "#e6edf3", "banner_accent": "#aaaaaa",
			"banner_dim": "#606060", "banner_text": "#c9d1d9", "ui_accent": "#aaaaaa",
			"ui_label": "#888888", "ui_ok": "#888888", "ui_error": "#cccccc", "ui_warn": "#999999",
			"prompt": "#c9d1d9", "input_rule": "#606060", "response_border": "#aaaaaa",
			"status_bar_bg": "#1F1F1F", "status_bar_text": "#C9D1D9",
			"status_bar_strong": "#E6EDF3", "status_bar_dim": "#777777",
			"status_bar_good": "#B5B5B5", "status_bar_warn": "#AAAAAA", "status_bar_bad": "#D0D0D0",
			"status_bar_critical": "#F0F0F0", "session_label": "#888888",
			"session_border": "#5E5E5E", "completion_menu_bg": "#1F1F1F",
			"completion_menu_current_bg": "#464646", "selection_bg": "#505050",
			"shell_dollar": "#aaaaaa", "voice_status_bg": "#1F1F1F",
		},
		Branding:      makeBranding("Hermes", "☤", "Goodbye! ☤", "❯", "[?] Available Commands"),
		ToolPrefix:    "┊",
		PromptSymbol:  "❯",
		ResponseLabel: " ☤ Hermes ",
		Goodbye:       "Goodbye! ☤",
		Symbol:        "☤",
	},
	"slate": {
		Name:        "slate",
		Description: "Cool blue — developer-focused",
		Colors: map[string]string{
			"banner_border": "#4169e1", "banner_title": "#7eb8f6", "banner_accent": "#8EA8FF",
			"banner_dim": "#545E6B", "banner_text": "#c9d1d9", "ui_accent": "#7eb8f6",
			"ui_label": "#8EA8FF", "ui_ok": "#63D0A6", "ui_error": "#F7A072", "ui_warn": "#e6a855",
			"prompt": "#c9d1d9", "input_rule": "#4169e1", "response_border": "#7eb8f6",
			"status_bar_bg": "#151C2F", "status_bar_text": "#C9D1D9",
			"status_bar_strong": "#7EB8F6", "status_bar_dim": "#5D6672",
			"status_bar_good": "#63D0A6", "status_bar_warn": "#E6A855", "status_bar_bad": "#F7A072",
			"status_bar_critical": "#FF7A7A", "session_label": "#7eb8f6",
			"session_border": "#545E6B", "completion_menu_bg": "#151C2F",
			"completion_menu_current_bg": "#324867", "selection_bg": "#3A5375",
			"shell_dollar": "#7eb8f6", "voice_status_bg": "#151C2F",
		},
		Branding:      makeBranding("Hermes", "☤", "Goodbye! ☤", "❯", "(^_^)? Available Commands"),
		ToolPrefix:    "┊",
		PromptSymbol:  "❯",
		ResponseLabel: " ☤ Hermes ",
		Goodbye:       "Goodbye! ☤",
		Symbol:        "☤",
	},
	"daylight": {
		Name:        "daylight",
		Description: "Light theme for bright terminals with dark text and cool blue accents",
		Colors: map[string]string{
			"banner_border": "#2563EB", "banner_title": "#0F172A", "banner_accent": "#1D4ED8",
			"banner_dim": "#475569", "banner_text": "#111827", "ui_accent": "#2563EB",
			"ui_label": "#0F766E", "ui_ok": "#15803D", "ui_error": "#B91C1C", "ui_warn": "#B45309",
			"prompt": "#111827", "input_rule": "#6E94BE", "response_border": "#2563EB",
			"status_bar_bg": "#E5EDF8", "status_bar_text": "#111827",
			"status_bar_strong": "#2563EB", "status_bar_dim": "#838890",
			"status_bar_good": "#15803D", "status_bar_warn": "#B45309", "status_bar_bad": "#B45309",
			"status_bar_critical": "#B91C1C", "session_label": "#1D4ED8",
			"session_border": "#64748B", "completion_menu_bg": "#F8FAFC",
			"completion_menu_current_bg": "#DBEAFE", "selection_bg": "#D3E0FB",
			"shell_dollar": "#2563EB", "voice_status_bg": "#E5EDF8",
		},
		Branding:      makeBranding("Hermes", "☤", "Goodbye! ☤", "❯", "[?] Available Commands"),
		ToolPrefix:    "│",
		PromptSymbol:  "❯",
		ResponseLabel: " ☤ Hermes ",
		Goodbye:       "Goodbye! ☤",
		Symbol:        "☤",
	},
	"warm-lightmode": {
		Name:        "warm-lightmode",
		Description: "Warm light mode — dark brown/gold text for light terminal backgrounds",
		Colors: map[string]string{
			"banner_border": "#8B6914", "banner_title": "#5C3D11", "banner_accent": "#8B4513",
			"banner_dim": "#8B7355", "banner_text": "#2C1810", "ui_accent": "#8B4513",
			"ui_label": "#5C3D11", "ui_ok": "#2E7D32", "ui_error": "#C62828", "ui_warn": "#E65100",
			"prompt": "#2C1810", "input_rule": "#8B6914", "response_border": "#8B6914",
			"status_bar_bg": "#F5F0E8", "status_bar_text": "#2C1810",
			"status_bar_strong": "#8B4513", "status_bar_dim": "#8A8F98",
			"status_bar_good": "#2E7D32", "status_bar_warn": "#E65100", "status_bar_bad": "#DA4D00",
			"status_bar_critical": "#C62828", "session_label": "#5C3D11",
			"session_border": "#A0845C", "completion_menu_bg": "#F5EFE0",
			"completion_menu_current_bg": "#E8DCC8", "selection_bg": "#E8DAD0",
			"shell_dollar": "#8B4513", "voice_status_bg": "#F5F0E8",
		},
		Branding:      makeBranding("Hermes", "☤", "Goodbye! ☤", "❯", "(^_^)? Available Commands"),
		ToolPrefix:    "┊",
		PromptSymbol:  "❯",
		ResponseLabel: " ☤ Hermes ",
		Goodbye:       "Goodbye! ☤",
		Symbol:        "☤",
	},
	"poseidon": {
		Name:        "poseidon",
		Description: "Ocean-god theme — deep blue and seafoam",
		Colors: map[string]string{
			"banner_border": "#2A6FB9", "banner_title": "#A9DFFF", "banner_accent": "#5DB8F5",
			"banner_dim": "#44638F", "banner_text": "#EAF7FF", "ui_accent": "#5DB8F5",
			"ui_label": "#A9DFFF", "ui_ok": "#4caf50", "ui_error": "#ef5350", "ui_warn": "#ffa726",
			"prompt": "#EAF7FF", "input_rule": "#2A6FB9", "response_border": "#5DB8F5",
			"status_bar_bg": "#0F2440", "status_bar_text": "#EAF7FF",
			"status_bar_strong": "#A9DFFF", "status_bar_dim": "#52708A",
			"status_bar_good": "#6ED7B0", "status_bar_warn": "#5DB8F5", "status_bar_bad": "#3576BC",
			"status_bar_critical": "#D94F4F", "session_label": "#A9DFFF",
			"session_border": "#496884", "completion_menu_bg": "#0F2440",
			"completion_menu_current_bg": "#254D73", "selection_bg": "#2A587F",
			"shell_dollar": "#5DB8F5", "voice_status_bg": "#0F2440",
		},
		Branding:      makeBranding("Poseidon", "Ψ", "Fair winds! Ψ", "", ""),
		ToolPrefix:    "│",
		PromptSymbol:  "Ψ",
		ResponseLabel: " Ψ Poseidon ",
		Goodbye:       "Fair winds! Ψ",
		Symbol:        "Ψ",
	},
	"sisyphus": {
		Name:        "sisyphus",
		Description: "Sisyphean theme — austere grayscale with persistence",
		Colors: map[string]string{
			"banner_border": "#B7B7B7", "banner_title": "#F5F5F5", "banner_accent": "#E7E7E7",
			"banner_dim": "#5C5C5C", "banner_text": "#D3D3D3", "ui_accent": "#E7E7E7",
			"ui_label": "#D3D3D3", "ui_ok": "#919191", "ui_error": "#E7E7E7", "ui_warn": "#B7B7B7",
			"prompt": "#F5F5F5", "input_rule": "#656565", "response_border": "#B7B7B7",
			"status_bar_bg": "#202020", "status_bar_text": "#D3D3D3",
			"status_bar_strong": "#F5F5F5", "status_bar_dim": "#6D6D6D",
			"status_bar_good": "#B7B7B7", "status_bar_warn": "#D3D3D3", "status_bar_bad": "#E7E7E7",
			"status_bar_critical": "#F5F5F5", "session_label": "#919191",
			"session_border": "#656565", "completion_menu_bg": "#202020",
			"completion_menu_current_bg": "#585858", "selection_bg": "#666666",
			"shell_dollar": "#E7E7E7", "voice_status_bg": "#202020",
		},
		Branding:      makeBranding("Sisyphus", "◉", "The boulder waits. ◉", "", ""),
		ToolPrefix:    "│",
		PromptSymbol:  "◉",
		ResponseLabel: " ◉ Sisyphus ",
		Goodbye:       "The boulder waits. ◉",
		Symbol:        "◉",
	},
	"charizard": {
		Name:        "charizard",
		Description: "Volcanic theme — burnt orange and ember",
		Colors: map[string]string{
			"banner_border": "#C75B1D", "banner_title": "#FFD39A", "banner_accent": "#F29C38",
			"banner_dim": "#C58A45", "banner_text": "#FFF0D4", "ui_accent": "#F29C38",
			"ui_label": "#FFD39A", "ui_ok": "#4caf50", "ui_error": "#ef5350", "ui_warn": "#ffa726",
			"prompt": "#FFF0D4", "input_rule": "#C75B1D", "response_border": "#F29C38",
			"status_bar_bg": "#2B160E", "status_bar_text": "#FFF0D4",
			"status_bar_strong": "#FFD39A", "status_bar_dim": "#826144",
			"status_bar_good": "#6BCB77", "status_bar_warn": "#F29C38", "status_bar_bad": "#E2832B",
			"status_bar_critical": "#EF5350", "session_label": "#FFD39A",
			"session_border": "#7B593A", "completion_menu_bg": "#0B0503",
			"completion_menu_current_bg": "#4A1B07", "selection_bg": "#5A260D",
			"shell_dollar": "#F29C38", "voice_status_bg": "#2B160E",
		},
		Branding:      makeBranding("Charizard", "✦", "Flame out! ✦", "", ""),
		ToolPrefix:    "│",
		PromptSymbol:  "✦",
		ResponseLabel: " ✦ Charizard ",
		Goodbye:       "Flame out! ✦",
		Symbol:        "✦",
	},
}

// BuiltinSkinNames returns the 9 built-in Hermes skin names in canonical order.
func BuiltinSkinNames() []string {
	out := make([]string, len(builtinSkinOrder))
	copy(out, builtinSkinOrder)
	return out
}

// GetSkin returns a built-in skin by name (case-insensitive).
func GetSkin(name string) (Skin, bool) {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		key = "default"
	}
	s, ok := builtinSkins[key]
	return s, ok
}

// FormatStatusText formats the compact Hermes status bar segment.
func FormatStatusText(profile, skinName string, memPct int, yolo bool) string {
	sk, ok := GetSkin(skinName)
	if !ok {
		sk = builtinSkins["default"]
	}
	if profile == "" {
		profile = "default"
	}
	status := fmt.Sprintf("%s %s · %s · mem %d%%", sk.Symbol, profile, sk.Name, memPct)
	if yolo {
		status += " · YOLO"
	}
	return status
}
