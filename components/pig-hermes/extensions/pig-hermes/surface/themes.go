package surface

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const pigThemeSchemaURL = "https://pi.dev/schemas/theme.schema.json"

// ThemeDocument mirrors PiG v0.8.0's theme JSON schema (tui/theme_dark.json).
type ThemeDocument struct {
	Schema     string            `json:"$schema"`
	Name       string            `json:"name"`
	Appearance string            `json:"appearance"`
	Vars       map[string]string `json:"vars"`
	Colors     map[string]string `json:"colors"`
	Export     map[string]string `json:"export"`
}

// HermesThemeName returns the PiG theme name corresponding to a Hermes skin.
func HermesThemeName(skinName string) string {
	sk, ok := GetSkin(skinName)
	if !ok {
		sk, _ = GetSkin("default")
	}
	return "hermes-" + sk.Name
}

// BuildThemeDocument builds a complete, PiG v0.8.0 schema-compliant ThemeDocument
// for the given Hermes skin.
func BuildThemeDocument(sk Skin) ThemeDocument {
	if _, ok := GetSkin(sk.Name); !ok {
		sk, _ = GetSkin("default")
	}
	spec := themeSpecForSkin(sk)

	vars := map[string]string{
		"text":       spec.text,
		"muted":      spec.muted,
		"accent":     spec.accent,
		"secondary":  spec.secondary,
		"border":     spec.border,
		"green":      spec.green,
		"red":        spec.red,
		"yellow":     spec.yellow,
		"selectedBg": spec.selectedBg,
		"surfaceBg":  spec.surfaceBg,
	}

	colors := map[string]string{
		"accent":             "accent",
		"border":             "border",
		"borderAccent":       "accent",
		"borderMuted":        spec.borderMuted,
		"success":            "green",
		"error":              "red",
		"warning":            "yellow",
		"muted":              "muted",
		"dim":                spec.dim,
		"text":               "text",
		"thinkingText":       spec.thinkingText,
		"selectedBg":         "selectedBg",
		"scrollbarTrack":     spec.scrollbarTrack,
		"scrollbarThumb":     spec.scrollbarThumb,
		"searchMatchBg":      spec.searchMatchBg,
		"searchMatchText":    "text",
		"userMessageBg":      "surfaceBg",
		"userMessageText":    "text",
		"customMessageBg":    spec.customMessageBg,
		"customMessageText":  "muted",
		"customMessageLabel": "accent",
		"toolPendingBg":      spec.toolPendingBg,
		"toolSuccessBg":      spec.toolSuccessBg,
		"toolErrorBg":        spec.toolErrorBg,
		"toolTitle":          "text",
		"toolOutput":         "muted",
		"mdHeading":          "accent",
		"mdLink":             "secondary",
		"mdLinkUrl":          "muted",
		"mdCode":             "accent",
		"mdCodeBlock":        "green",
		"mdCodeBlockBorder":  "muted",
		"mdQuote":            "muted",
		"mdQuoteBorder":      "border",
		"mdHr":               "muted",
		"mdListBullet":       "accent",
		"toolDiffAdded":      "green",
		"toolDiffRemoved":    "red",
		"toolDiffContext":    "muted",
		"syntaxComment":      "muted",
		"syntaxKeyword":      "secondary",
		"syntaxFunction":     "accent",
		"syntaxVariable":     spec.syntaxVariable,
		"syntaxString":       spec.syntaxString,
		"syntaxNumber":       "green",
		"syntaxType":         "yellow",
		"syntaxOperator":     "muted",
		"syntaxPunctuation":  "muted",
		"thinkingOff":        spec.thinkingOff,
		"thinkingMinimal":    spec.thinkingMinimal,
		"thinkingLow":        spec.thinkingLow,
		"thinkingMedium":     spec.thinkingMedium,
		"thinkingHigh":       spec.thinkingHigh,
		"thinkingXhigh":      spec.thinkingXhigh,
		"thinkingMax":        spec.thinkingMax,
		"bashMode":           spec.bashMode,
	}

	export := map[string]string{
		"pageBg": spec.pageBg,
		"cardBg": spec.cardBg,
		"infoBg": spec.infoBg,
	}

	return ThemeDocument{
		Schema:     pigThemeSchemaURL,
		Name:       "hermes-" + sk.Name,
		Appearance: spec.appearance,
		Vars:       vars,
		Colors:     colors,
		Export:     export,
	}
}

// GenerateThemeJSON serializes a Hermes skin's PiG theme document as formatted JSON.
func GenerateThemeJSON(sk Skin) ([]byte, error) {
	doc := BuildThemeDocument(sk)
	data, err := json.MarshalIndent(doc, "", "\t")
	if err != nil {
		return nil, fmt.Errorf("marshal theme %q: %w", doc.Name, err)
	}
	return append(data, '\n'), nil
}

// EnsureThemeFiles idempotently writes all 9 Hermes PiG theme JSON files into
// filepath.Join(agentDir, "themes") using atomic temp-file renames.
func EnsureThemeFiles(agentDir string) error {
	agentDir = strings.TrimSpace(agentDir)
	if agentDir == "" {
		return fmt.Errorf("agent directory is empty")
	}
	themesDir := filepath.Join(agentDir, "themes")
	if err := os.MkdirAll(themesDir, 0o755); err != nil {
		return fmt.Errorf("create themes dir %q: %w", themesDir, err)
	}
	for _, name := range builtinSkinOrder {
		sk, ok := GetSkin(name)
		if !ok {
			continue
		}
		data, err := GenerateThemeJSON(sk)
		if err != nil {
			return err
		}
		target := filepath.Join(themesDir, "hermes-"+sk.Name+".json")
		if existing, err := os.ReadFile(target); err == nil && bytes.Equal(existing, data) {
			continue
		}
		if err := writeFileAtomic(themesDir, target, data); err != nil {
			return err
		}
	}
	return nil
}

func writeFileAtomic(dir, target string, data []byte) error {
	tmp, err := os.CreateTemp(dir, ".hermes-theme-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp theme file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp theme file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp theme file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		return fmt.Errorf("chmod temp theme file: %w", err)
	}
	if err := os.Rename(tmpPath, target); err != nil {
		return fmt.Errorf("rename theme file %q: %w", target, err)
	}
	cleanup = false
	return nil
}

// resolveAgentDir resolves the active PiG agent directory, preferring sdk.AgentDir()
// and falling back to PiG environment variables when running outside a host.
func resolveAgentDir() string {
	if dir, err := sdk.AgentDir(); err == nil && strings.TrimSpace(dir) != "" {
		return dir
	}
	if dir := strings.TrimSpace(os.Getenv("PIG_CODING_AGENT_DIR")); dir != "" {
		return dir
	}
	if dir := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR")); dir != "" {
		return dir
	}
	if home := strings.TrimSpace(os.Getenv("PIG_HOME")); home != "" {
		return filepath.Join(home, "agent")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".pig", "agent")
	}
	return ""
}

type skinThemeSpec struct {
	appearance      string
	text            string
	muted           string
	dim             string
	accent          string
	secondary       string
	border          string
	borderMuted     string
	green           string
	red             string
	yellow          string
	surfaceBg       string
	selectedBg      string
	customMessageBg string
	searchMatchBg   string
	toolPendingBg   string
	toolSuccessBg   string
	toolErrorBg     string
	thinkingText    string
	scrollbarTrack  string
	scrollbarThumb  string
	syntaxVariable  string
	syntaxString    string
	thinkingOff     string
	thinkingMinimal string
	thinkingLow     string
	thinkingMedium  string
	thinkingHigh    string
	thinkingXhigh   string
	thinkingMax     string
	bashMode        string
	pageBg          string
	cardBg          string
	infoBg          string
}

func themeSpecForSkin(sk Skin) skinThemeSpec {
	isLight := sk.Name == "daylight" || sk.Name == "warm-lightmode"

	spec := skinThemeSpec{
		appearance:  "dark",
		text:        sk.Color("banner_text", "#FFF8DC"),
		muted:       sk.Color("status_bar_text", "#C0C0C0"),
		dim:         sk.Color("status_bar_dim", "#8A7A4A"),
		accent:      sk.Color("banner_title", "#FFD700"),
		secondary:   sk.Color("shell_dollar", "#4DABF7"),
		border:      sk.Color("banner_border", "#CD7F32"),
		borderMuted: sk.Color("session_border", "#8B8682"),
		green:       sk.Color("ui_ok", "#4CAF50"),
		red:         sk.Color("ui_error", "#EF5350"),
		yellow:      sk.Color("ui_warn", "#FFA726"),
		surfaceBg:   sk.Color("status_bar_bg", "#1A1A2E"),
		selectedBg:  sk.Color("selection_bg", "#3A3A55"),
		bashMode:    sk.Color("shell_dollar", "#4DABF7"),
	}

	if isLight {
		spec.appearance = "light"
		spec.muted = sk.Color("banner_dim", "#475569")
		spec.thinkingText = sk.Color("banner_dim", "#64748B")
		spec.scrollbarTrack = "#E2E8F0"
		spec.scrollbarThumb = sk.Color("session_border", "#94A3B8")
		spec.customMessageBg = sk.Color("completion_menu_current_bg", "#DBEAFE")
		spec.searchMatchBg = "#FEF3C7"
		spec.toolPendingBg = sk.Color("completion_menu_bg", "#F1F5F9")
		spec.toolSuccessBg = "#DCFCE7"
		spec.toolErrorBg = "#FEE2E2"
		spec.syntaxVariable = sk.Color("ui_label", "#0F766E")
		spec.syntaxString = sk.Color("ui_warn", "#B45309")
		spec.thinkingOff = "#CBD5E1"
		spec.thinkingMinimal = "#94A3B8"
		spec.thinkingLow = "#60A5FA"
		spec.thinkingMedium = "#3B82F6"
		spec.thinkingHigh = "#7C3AED"
		spec.thinkingXhigh = "#DB2777"
		spec.thinkingMax = "#DC2626"
		spec.pageBg = sk.Color("completion_menu_bg", "#F8FAFC")
		spec.cardBg = sk.Color("status_bar_bg", "#E5EDF8")
		spec.infoBg = sk.Color("completion_menu_current_bg", "#DBEAFE")
	} else {
		spec.thinkingText = sk.Color("banner_dim", "#B8860B")
		spec.scrollbarTrack = "#2A2A3C"
		spec.scrollbarThumb = sk.Color("session_border", "#8B8682")
		spec.customMessageBg = sk.Color("completion_menu_current_bg", "#333355")
		spec.searchMatchBg = "#4A3F1A"
		spec.toolPendingBg = "#242436"
		spec.toolSuccessBg = "#1B3A2A"
		spec.toolErrorBg = "#3E1F22"
		spec.syntaxVariable = sk.Color("ui_label", "#DAA520")
		spec.syntaxString = sk.Color("banner_accent", "#FFBF00")
		spec.thinkingOff = "#5C5C6E"
		spec.thinkingMinimal = "#6E738D"
		spec.thinkingLow = "#5B8DEF"
		spec.thinkingMedium = "#9B72CF"
		spec.thinkingHigh = "#FFBF00"
		spec.thinkingXhigh = "#FF8C00"
		spec.thinkingMax = "#FF5555"
		spec.pageBg = "#141422"
		spec.cardBg = sk.Color("status_bar_bg", "#1A1A2E")
		spec.infoBg = sk.Color("completion_menu_current_bg", "#333355")
	}

	// Fine-tune signature skins for authentic Hermes look & feel.
	switch sk.Name {
	case "default":
		spec.accent = "#FFD700"
		spec.border = "#CD7F32"
		spec.secondary = "#FFBF00"
		spec.syntaxVariable = "#DAA520"
		spec.syntaxString = "#FFE066"
		spec.customMessageBg = "#2E2614"
		spec.searchMatchBg = "#4A3B12"
		spec.pageBg = "#14131E"
		spec.cardBg = "#1A1A2E"
		spec.infoBg = "#2E2614"
	case "ares":
		spec.accent = "#DD4A3A"
		spec.secondary = "#C7A96B"
		spec.customMessageBg = "#3D1815"
		spec.toolPendingBg = "#2C1616"
		spec.pageBg = "#1D0D0D"
		spec.cardBg = "#2A1212"
		spec.infoBg = "#3D1815"
	case "mono":
		spec.accent = "#E6EDF3"
		spec.secondary = "#AAAAAA"
		spec.customMessageBg = "#2E2E2E"
		spec.toolPendingBg = "#252525"
		spec.toolSuccessBg = "#2A2A2A"
		spec.toolErrorBg = "#333333"
		spec.pageBg = "#141414"
		spec.cardBg = "#1F1F1F"
		spec.infoBg = "#2E2E2E"
	case "slate":
		spec.accent = "#7EB8F6"
		spec.secondary = "#8EA8FF"
		spec.customMessageBg = "#21324D"
		spec.toolPendingBg = "#1B2438"
		spec.pageBg = "#0F1524"
		spec.cardBg = "#151C2F"
		spec.infoBg = "#21324D"
	case "daylight":
		spec.accent = "#2563EB"
		spec.secondary = "#1D4ED8"
		spec.pageBg = "#F8FAFC"
		spec.cardBg = "#E5EDF8"
		spec.infoBg = "#DBEAFE"
	case "warm-lightmode":
		spec.accent = "#8B4513"
		spec.secondary = "#8B6914"
		spec.scrollbarTrack = "#E8DCC8"
		spec.toolPendingBg = "#EFE7D8"
		spec.toolSuccessBg = "#E3EFE2"
		spec.toolErrorBg = "#F6DFDD"
		spec.pageBg = "#FAF6EE"
		spec.cardBg = "#F5F0E8"
		spec.infoBg = "#E8DCC8"
	case "poseidon":
		spec.accent = "#5DB8F5"
		spec.secondary = "#A9DFFF"
		spec.customMessageBg = "#183654"
		spec.toolPendingBg = "#122944"
		spec.pageBg = "#0A192E"
		spec.cardBg = "#0F2440"
		spec.infoBg = "#183654"
	case "sisyphus":
		spec.accent = "#F5F5F5"
		spec.secondary = "#B7B7B7"
		spec.customMessageBg = "#343434"
		spec.toolPendingBg = "#282828"
		spec.toolSuccessBg = "#2E2E2E"
		spec.toolErrorBg = "#3A3A3A"
		spec.pageBg = "#161616"
		spec.cardBg = "#202020"
		spec.infoBg = "#343434"
	case "charizard":
		spec.accent = "#F29C38"
		spec.secondary = "#FFD39A"
		spec.customMessageBg = "#3F1B0B"
		spec.toolPendingBg = "#2F1910"
		spec.pageBg = "#1B0E08"
		spec.cardBg = "#2B160E"
		spec.infoBg = "#3F1B0B"
	}

	return spec
}
