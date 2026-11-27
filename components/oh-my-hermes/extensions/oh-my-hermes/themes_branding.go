package ohmyhermes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const omhThemeSchemaURL = "https://pi.dev/schemas/theme.schema.json"

// omhThemeSpec holds the full color palette used to generate a complete PiG v0.8.0 theme JSON.
type omhThemeSpec struct {
	ThemeName   string // e.g. "omh-sky"
	ShortName   string // e.g. "sky"
	Accent      string
	Border      string
	BorderMuted string
	Success     string
	Error       string
	Warning     string
	Text        string
	Muted       string
	Dim         string
	SelectedBg  string
	UserBg      string
	CustomBg    string
	PendingBg   string
	SuccessBg   string
	ErrorBg     string
	SearchBg    string
	SyntaxKw    string
	SyntaxFn    string
	SyntaxVar   string
	SyntaxStr   string
	SyntaxNum   string
	SyntaxType  string
	PageBg      string
	CardBg      string
	InfoBg      string
}

var omhThemeSpecs = []omhThemeSpec{
	{
		ThemeName:   "omh-sky",
		ShortName:   "sky",
		Accent:      "#38BDF8",
		Border:      "#0EA5E9",
		BorderMuted: "#1E4E68",
		Success:     "#34D399",
		Error:       "#F87171",
		Warning:     "#FBBF24",
		Text:        "#E2F1F8",
		Muted:       "#8BB3C7",
		Dim:         "#5B8196",
		SelectedBg:  "#123548",
		UserBg:      "#0F2B3A",
		CustomBg:    "#132D42",
		PendingBg:   "#142834",
		SuccessBg:   "#0E332B",
		ErrorBg:     "#3B1A22",
		SearchBg:    "#3B3214",
		SyntaxKw:    "#38BDF8",
		SyntaxFn:    "#FBBF24",
		SyntaxVar:   "#7DD3FC",
		SyntaxStr:   "#A7F3D0",
		SyntaxNum:   "#34D399",
		SyntaxType:  "#C084FC",
		PageBg:      "#0B1E28",
		CardBg:      "#102836",
		InfoBg:      "#15364A",
	},
	{
		ThemeName:   "omh-amber",
		ShortName:   "amber",
		Accent:      "#F59E0B",
		Border:      "#D97706",
		BorderMuted: "#5C421C",
		Success:     "#4ADE80",
		Error:       "#F87171",
		Warning:     "#FDE047",
		Text:        "#FEF3C7",
		Muted:       "#C7A876",
		Dim:         "#8A6E43",
		SelectedBg:  "#3D2B10",
		UserBg:      "#33230E",
		CustomBg:    "#3B2612",
		PendingBg:   "#2B2012",
		SuccessBg:   "#173320",
		ErrorBg:     "#3D1B1B",
		SearchBg:    "#4A3810",
		SyntaxKw:    "#F59E0B",
		SyntaxFn:    "#FDE047",
		SyntaxVar:   "#FDBA74",
		SyntaxStr:   "#A7F3D0",
		SyntaxNum:   "#4ADE80",
		SyntaxType:  "#FCD34D",
		PageBg:      "#1F160A",
		CardBg:      "#291D0D",
		InfoBg:      "#3A2912",
	},
	{
		ThemeName:   "omh-crimson",
		ShortName:   "crimson",
		Accent:      "#F43F5E",
		Border:      "#E11D48",
		BorderMuted: "#5E2330",
		Success:     "#34D399",
		Error:       "#FB7185",
		Warning:     "#FBBF24",
		Text:        "#FFE4E8",
		Muted:       "#C7929C",
		Dim:         "#8C5A65",
		SelectedBg:  "#421621",
		UserBg:      "#36121C",
		CustomBg:    "#3E1728",
		PendingBg:   "#2E141B",
		SuccessBg:   "#143328",
		ErrorBg:     "#471620",
		SearchBg:    "#423115",
		SyntaxKw:    "#F43F5E",
		SyntaxFn:    "#FBBF24",
		SyntaxVar:   "#FDA4AF",
		SyntaxStr:   "#A7F3D0",
		SyntaxNum:   "#34D399",
		SyntaxType:  "#F472B6",
		PageBg:      "#220D10",
		CardBg:      "#2C1116",
		InfoBg:      "#3D1821",
	},
	{
		ThemeName:   "omh-mono",
		ShortName:   "mono",
		Accent:      "#D4D4D8",
		Border:      "#A1A1AA",
		BorderMuted: "#3F3F46",
		Success:     "#A3E635",
		Error:       "#F87171",
		Warning:     "#FACC15",
		Text:        "#F4F4F5",
		Muted:       "#A1A1AA",
		Dim:         "#71717A",
		SelectedBg:  "#27272A",
		UserBg:      "#222225",
		CustomBg:    "#2A2A2E",
		PendingBg:   "#1F1F22",
		SuccessBg:   "#1D2B1E",
		ErrorBg:     "#2E1C1C",
		SearchBg:    "#332E18",
		SyntaxKw:    "#E4E4E7",
		SyntaxFn:    "#FAFAFA",
		SyntaxVar:   "#D4D4D8",
		SyntaxStr:   "#A1A1AA",
		SyntaxNum:   "#E4E4E7",
		SyntaxType:  "#D4D4D8",
		PageBg:      "#111111",
		CardBg:      "#18181B",
		InfoBg:      "#27272A",
	},
	{
		ThemeName:   "omh-kanagawa",
		ShortName:   "kanagawa",
		Accent:      "#7E9CD8",
		Border:      "#658594",
		BorderMuted: "#363646",
		Success:     "#98BB6C",
		Error:       "#E46876",
		Warning:     "#E6C384",
		Text:        "#DCD7BA",
		Muted:       "#9CABCA",
		Dim:         "#727169",
		SelectedBg:  "#2D4F67",
		UserBg:      "#2A2A37",
		CustomBg:    "#2E2B3E",
		PendingBg:   "#252535",
		SuccessBg:   "#2B3328",
		ErrorBg:     "#43242B",
		SearchBg:    "#49443C",
		SyntaxKw:    "#957FB8",
		SyntaxFn:    "#7E9CD8",
		SyntaxVar:   "#DCD7BA",
		SyntaxStr:   "#98BB6C",
		SyntaxNum:   "#D27E99",
		SyntaxType:  "#7AA89F",
		PageBg:      "#1F1F28",
		CardBg:      "#2A2A37",
		InfoBg:      "#2D4F67",
	},
}

type pigThemeDocument struct {
	Schema     string            `json:"$schema"`
	Name       string            `json:"name"`
	Appearance string            `json:"appearance"`
	Vars       map[string]string `json:"vars"`
	Colors     map[string]string `json:"colors"`
	Export     map[string]string `json:"export"`
}

func buildThemeDocument(spec omhThemeSpec) pigThemeDocument {
	return pigThemeDocument{
		Schema:     omhThemeSchemaURL,
		Name:       spec.ThemeName,
		Appearance: "dark",
		Vars: map[string]string{
			"accent":     spec.Accent,
			"text":       spec.Text,
			"muted":      spec.Muted,
			"dim":        spec.Dim,
			"success":    spec.Success,
			"error":      spec.Error,
			"warning":    spec.Warning,
			"selectedBg": spec.SelectedBg,
		},
		Colors: map[string]string{
			"accent":             "accent",
			"border":             spec.Border,
			"borderAccent":       "accent",
			"borderMuted":        spec.BorderMuted,
			"success":            "success",
			"error":              "error",
			"warning":            "warning",
			"muted":              "muted",
			"dim":                "dim",
			"text":               "text",
			"thinkingText":       "muted",
			"selectedBg":         "selectedBg",
			"scrollbarTrack":     spec.BorderMuted,
			"scrollbarThumb":     spec.Border,
			"searchMatchBg":      spec.SearchBg,
			"searchMatchText":    "text",
			"userMessageBg":      spec.UserBg,
			"userMessageText":    "text",
			"customMessageBg":    spec.CustomBg,
			"customMessageText":  "muted",
			"customMessageLabel": "accent",
			"toolPendingBg":      spec.PendingBg,
			"toolSuccessBg":      spec.SuccessBg,
			"toolErrorBg":        spec.ErrorBg,
			"toolTitle":          "text",
			"toolOutput":         "muted",
			"mdHeading":          "warning",
			"mdLink":             "accent",
			"mdLinkUrl":          "muted",
			"mdCode":             "accent",
			"mdCodeBlock":        "success",
			"mdCodeBlockBorder":  "muted",
			"mdQuote":            "muted",
			"mdQuoteBorder":      "muted",
			"mdHr":               "muted",
			"mdListBullet":       "accent",
			"toolDiffAdded":      "success",
			"toolDiffRemoved":    "error",
			"toolDiffContext":    "muted",
			"syntaxComment":      "dim",
			"syntaxKeyword":      spec.SyntaxKw,
			"syntaxFunction":     spec.SyntaxFn,
			"syntaxVariable":     spec.SyntaxVar,
			"syntaxString":       spec.SyntaxStr,
			"syntaxNumber":       spec.SyntaxNum,
			"syntaxType":         spec.SyntaxType,
			"syntaxOperator":     "muted",
			"syntaxPunctuation":  "muted",
			"thinkingOff":        "dim",
			"thinkingMinimal":    spec.BorderMuted,
			"thinkingLow":        spec.Border,
			"thinkingMedium":     "accent",
			"thinkingHigh":       "warning",
			"thinkingXhigh":      "error",
			"thinkingMax":        "error",
			"bashMode":           "success",
		},
		Export: map[string]string{
			"pageBg": spec.PageBg,
			"cardBg": spec.CardBg,
			"infoBg": spec.InfoBg,
		},
	}
}

// OMHThemeJSON builds the PiG v0.8.0 theme JSON document for one of the 5 OMH themes
// (accepting either short names like "kanagawa" or full names like "omh-kanagawa").
func OMHThemeJSON(name string) ([]byte, error) {
	th, ok := LookupTheme(name)
	if !ok {
		return nil, fmt.Errorf("unknown OMH theme %q", name)
	}
	for _, spec := range omhThemeSpecs {
		if spec.ShortName == th.Name {
			doc := buildThemeDocument(spec)
			raw, err := json.MarshalIndent(doc, "", "\t")
			if err != nil {
				return nil, err
			}
			return append(raw, '\n'), nil
		}
	}
	return nil, fmt.Errorf("missing theme spec for %q", th.Name)
}

// resolvePiGAgentDir resolves the active PiG agent directory for writing themes.
func resolvePiGAgentDir() string {
	if dir, err := sdk.AgentDir(); err == nil && strings.TrimSpace(dir) != "" {
		return filepath.Clean(dir)
	}
	if env := strings.TrimSpace(os.Getenv("PIG_AGENT_DIR")); env != "" {
		return filepath.Clean(env)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".", ".pig", "agent")
	}
	return filepath.Join(home, ".pig", "agent")
}

// EnsureOMHThemeFiles idempotently writes all 5 OMH PiG theme JSON files into
// filepath.Join(agentDir, "themes"). If agentDir is empty, it resolves the PiG agent dir.
func EnsureOMHThemeFiles(agentDir string) error {
	if strings.TrimSpace(agentDir) == "" {
		agentDir = resolvePiGAgentDir()
	}
	themesDir := filepath.Join(agentDir, "themes")
	if err := os.MkdirAll(themesDir, 0o755); err != nil {
		return fmt.Errorf("create themes dir: %w", err)
	}
	for _, spec := range omhThemeSpecs {
		data, err := OMHThemeJSON(spec.ShortName)
		if err != nil {
			return err
		}
		target := filepath.Join(themesDir, spec.ThemeName+".json")
		if existing, err := os.ReadFile(target); err == nil && bytes.Equal(existing, data) {
			continue
		}
		if err := atomicWriteBytes(target, data, 0o644); err != nil {
			return fmt.Errorf("write theme %s: %w", spec.ThemeName, err)
		}
	}
	return nil
}

// --- Crowned Winged-Pig Sprite & Login Branding ---

var omhMascotGrid = []string{
	"....Y..YY..Y....",
	"....GYGRRGYG....",
	"....GGGGGGGG....",
	"..OeOOOOOOOOeO..",
	"WWOPPPpPPpPPPOWW",
	"WCOPPWWPPWWPPOCW",
	".COPPWKPPKWPPOC.",
	"..OPbPssssPbPO..",
	"..OPPsKssKsPPO..",
	"..OPPPssssPPPO..",
	"..OPPPPPPPPPPO..",
	"...OPPPPPPPPO...",
	"....OOOOOOOO....",
	"................",
}

// omhBrandGrid spells "OH-MY-HERMES" in exact 41x5 pixel art.
var omhBrandGrid = []string{
	".CCC.C.C.C.C.C.C.C.C.CCC.CC..C.C.CCC.CCC.",
	".C.C.C.C.CCC.C.C.C.C.C...C.C.CCC.C...C...",
	".C.C.CCCGC.C.CCCGCCC.CC..CC..C.C.CC..CCC.",
	".C.C.C.C.C.C..C..C.C.C...C.C.C.C.C.....C.",
	".CCC.C.C.C.C..C..C.C.CCC.C.C.C.C.CCC.CCC.",
}

// omhHeroGrid depicts the crowned winged OMH pig in exact 32x14 pixel art.
var omhHeroGrid = []string{
	"............Y..YY..Y............",
	"............GYGRRGYG............",
	"............GGGGGGGG............",
	".WWWWW....OeOOOOOOOOeO....WWWWW.",
	"WWWWCCW..OPPPpPPPPpPPPO..WCCWWWW",
	".WWWCCCW.OPPWWPPPPWWPPO.WCCCWWW.",
	"..WWCCCW.OPPWKPPPPKWPPO.WCCCWW..",
	"...WCCCW.OPbPPssssPPbPO.WCCCW...",
	"....WCC..OPPPsKssKsPPPO..CCW....",
	".....CC..OPPPPssssPPPPO..CC.....",
	".........OPPPPPPPPPPPPO.........",
	"..........OPPPPPPPPPPO..........",
	"...........OOOOOOOOOO...........",
	"................................",
}

func omhBrandingPalette() map[string]string {
	return map[string]string{
		"Y": "#FDE047", // bright crown gold
		"G": "#F59E0B", // crown gold
		"R": "#F43F5E", // crown ruby
		"O": "#18151D", // classic PiG dark outline
		"K": "#18151D", // eyes & nostrils
		"P": "#48A381", // classic PiG emerald green body (pig-default)
		"p": "#64BE9E", // classic PiG green highlight
		"s": "#32775E", // classic PiG deep teal snout
		"b": "#82DBBA", // classic PiG mint cheek blush
		"e": "#48A381", // classic PiG inner ear
		"W": "#FEFEFE", // crisp white wing feathers & eye gleam
		"C": "#38BDF8", // OMH sky-cyan accent & wing trim
	}
}

// OMHSpriteDefinition returns the 16x14 crowned winged-pig sprite registered with PiG's /sprite catalogue.
func OMHSpriteDefinition() sdk.SpriteDefinition {
	mascot := make([]string, len(omhMascotGrid))
	copy(mascot, omhMascotGrid)
	return sdk.SpriteDefinition{
		ID:      "omh-pig",
		Name:    "Oh My Hermes",
		Tagline: "Crowned winged pig for multi-agent orchestration",
		Mascot:  mascot,
		Palette: omhBrandingPalette(),
	}
}

// OMHLoginDefinition returns the exact-dimension (41x5 brand, 32x14 hero, 16x14 mascot)
// crowned winged-pig login definition for oh-my-hermes-pig.
func OMHLoginDefinition() sdk.LoginDefinition {
	brand := make([]string, len(omhBrandGrid))
	copy(brand, omhBrandGrid)
	hero := make([]string, len(omhHeroGrid))
	copy(hero, omhHeroGrid)
	mascot := make([]string, len(omhMascotGrid))
	copy(mascot, omhMascotGrid)
	return sdk.LoginDefinition{
		Brand:       brand,
		Hero:        hero,
		Mascot:      mascot,
		Palette:     omhBrandingPalette(),
		Name:        "oh-my-hermes-pig",
		Description: "Multi-agent DAG, team & routing harness",
		Tagline:     "Crowned winged pig — category routing, DAGs, teams & evidence",
	}
}
