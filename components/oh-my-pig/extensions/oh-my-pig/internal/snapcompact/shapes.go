package snapcompact

import (
	"math"
	"regexp"
	"strings"
)

// upstream: packages/snapcompact/src/snapcompact.ts (Shapes, billing, budgets)

const (
	// MinConfiguredFrameSize is the renderer's 64px floor.
	MinConfiguredFrameSize = 64
	// MaxConfiguredFrameSize is the largest edge any variant renders at.
	MaxConfiguredFrameSize = 2576

	// FrameSizeLegacy is the 5x8 legacy edge.
	FrameSizeLegacy = 2576
	// MaxFramesDefault caps archive growth while staying well under wire limits.
	MaxFramesDefault = 80
	// HQEdgeFrames is the high-quality frame count at each chronological edge
	// when foveating a long archive (HQ/LQ/HQ).
	HQEdgeFrames = 3
	// FrameTokenEstimate is the conservative per-frame token estimate.
	FrameTokenEstimate = 5024
	// FrameDataBytesEstimate is the conservative per-frame base64 size estimate.
	FrameDataBytesEstimate = 170_000
	// DefaultFrameDataBytesBudget caps standing base64 frame bytes per request.
	DefaultFrameDataBytesBudget = 3_000_000
	// DefaultProviderImageBudget is the safe floor for uncataloged hosts.
	DefaultProviderImageBudget = 5

	// PreserveKey is the key under Details/PreserveData holding the Archive.
	PreserveKey = "snapcompact"
)

// Shape describes one frame's font, cell geometry, ink variant, and billing.
type Shape struct {
	Font                string `json:"font"`
	CellWidth           int    `json:"cellWidth"`
	CellHeight          int    `json:"cellHeight"`
	Stretch             *bool  `json:"stretch,omitempty"`
	Variant             string `json:"variant"`
	StopwordDim         bool   `json:"stopwordDim,omitempty"`
	Columns             int    `json:"columns,omitempty"`
	LineRepeat          int    `json:"lineRepeat"`
	FrameSize           int    `json:"frameSize"`
	FrameTokenEstimate  int    `json:"frameTokenEstimate"`
	FrameTokensVerified bool   `json:"frameTokensVerified,omitempty"`
	ImageDetail         string `json:"imageDetail,omitempty"`
}

// Geometry holds the character grid dimensions of one frame.
type Geometry struct {
	Cols     int
	Rows     int
	Capacity int
}

// ModelTarget describes the active reading model for shape/billing/budget choices.
type ModelTarget struct {
	API           string
	ID            string
	Provider      string
	ContextWindow int
	Input         []string // e.g. ["text", "image"]
	BaseURL       string
}

// SupportsImages reports whether target advertises image input capability.
func (m ModelTarget) SupportsImages() bool {
	if len(m.Input) == 0 {
		// Fall back to known vision model IDs when Input metadata isn't populated.
		return inferVisionFromID(m.ID)
	}
	for _, in := range m.Input {
		if strings.EqualFold(in, "image") {
			return true
		}
	}
	return false
}

var knownVisionIDRe = regexp.MustCompile(`(?i)(claude|gemini|gpt-4o|gpt-4\.1|gpt-5|gpt-6|codex|o3|o4|qwen.*vl|kimi|glm-4\.\d+v|pixtral|llama-4|sonnet|opus|haiku)`)

func inferVisionFromID(id string) bool {
	return id != "" && knownVisionIDRe.MatchString(id)
}

// ShapeVariants mirrors packages/snapcompact/src/snapcompact.ts SHAPE_VARIANTS.
var ShapeVariants = map[string]Shape{
	"8x8r-bw":            {Font: "8x8", CellWidth: 8, CellHeight: 8, Variant: "bw", LineRepeat: 2, FrameSize: 1568},
	"8x8r-sent":          {Font: "8x8", CellWidth: 8, CellHeight: 8, Variant: "sent", LineRepeat: 2, FrameSize: 1568},
	"8x8u-bw":            {Font: "8x8", CellWidth: 8, CellHeight: 8, Variant: "bw", LineRepeat: 1, FrameSize: 1568},
	"8x8u-sent":          {Font: "8x8", CellWidth: 8, CellHeight: 8, Variant: "sent", LineRepeat: 1, FrameSize: 1568},
	"6x6u-bw":            {Font: "8x8", CellWidth: 6, CellHeight: 6, Variant: "bw", LineRepeat: 1, FrameSize: 1568},
	"6x6u-sent":          {Font: "8x8", CellWidth: 6, CellHeight: 6, Variant: "sent", LineRepeat: 1, FrameSize: 1568},
	"5x8-bw":             {Font: "5x8", CellWidth: 5, CellHeight: 8, Variant: "bw", LineRepeat: 1, FrameSize: 2576},
	"5x8-sent":           {Font: "5x8", CellWidth: 5, CellHeight: 8, Variant: "sent", LineRepeat: 1, FrameSize: 2576},
	"6x12-dim":           {Font: "6x12", CellWidth: 6, CellHeight: 12, Variant: "bw", StopwordDim: true, LineRepeat: 1, FrameSize: 1568},
	"8x13-bw":            {Font: "8x13", CellWidth: 8, CellHeight: 13, Variant: "bw", LineRepeat: 1, FrameSize: 1568},
	"8on16-bw":           {Font: "8x13", CellWidth: 8, CellHeight: 16, Stretch: new(false), Variant: "bw", LineRepeat: 1, FrameSize: 1568},
	"8on22-bw":           {Font: "8x13", CellWidth: 8, CellHeight: 22, Stretch: new(false), Variant: "bw", LineRepeat: 1, FrameSize: 1568},
	"11on16-bw":          {Font: "8x13", CellWidth: 11, CellHeight: 16, Stretch: new(false), Variant: "bw", LineRepeat: 1, FrameSize: 1568},
	"silver16-bw":        {Font: "silver", CellWidth: 16, CellHeight: 16, Variant: "bw", LineRepeat: 1, FrameSize: 1568},
	"doc-8on16-bw":       {Font: "8x13", CellWidth: 8, CellHeight: 16, Stretch: new(false), Variant: "bw", Columns: 2, LineRepeat: 1, FrameSize: 1568},
	"doc-8on16-sent":     {Font: "8x13", CellWidth: 8, CellHeight: 16, Stretch: new(false), Variant: "sent", Columns: 2, LineRepeat: 1, FrameSize: 1568},
	"doc-8on16-sent-dim": {Font: "8x13", CellWidth: 8, CellHeight: 16, Stretch: new(false), Variant: "sent", StopwordDim: true, Columns: 2, LineRepeat: 1, FrameSize: 1568},
}

// IsShapeVariantName reports whether name is a known SHAPE_VARIANTS key.
func IsShapeVariantName(name string) bool {
	_, ok := ShapeVariants[name]
	return ok
}

type apiFamily string

const (
	familyAnthropic apiFamily = "anthropic"
	familyGoogle    apiFamily = "google"
	familyOpenAI    apiFamily = "openai"
	familyUnknown   apiFamily = "unknown"
)

func detectAPIFamily(api string) apiFamily {
	switch api {
	case "anthropic-messages", "bedrock-converse-stream":
		return familyAnthropic
	case "openai-completions", "openai-responses", "openai-codex-responses", "azure-openai-responses":
		return familyOpenAI
	case "google-generative-ai", "google-gemini-cli", "google-vertex":
		return familyGoogle
	default:
		return familyUnknown
	}
}

func familyDefaultVariant(f apiFamily) string {
	switch f {
	case familyAnthropic:
		return "11on16-bw"
	case familyGoogle, familyOpenAI:
		return "8on22-bw"
	default:
		return "8on22-bw"
	}
}

var (
	reClaudeHighRes = regexp.MustCompile(`(?i)claude.*(fable|mythos|opus[-_. ]?(4[-_.]([789]|\d{2})|[5-9]))`)
	reClaude        = regexp.MustCompile(`(?i)claude`)
	reGemini        = regexp.MustCompile(`(?i)gemini`)
	reOpenAI        = regexp.MustCompile(`(?i)(gpt|codex)`)
	reKimi          = regexp.MustCompile(`(?i)kimi`)
	reGLM           = regexp.MustCompile(`(?i)glm`)
)

// IdealShape holds the eval-winning variant and optional frameSize for a model line.
type IdealShape struct {
	Variant   string
	FrameSize int
}

// IdealShapeVariant returns the eval-winning variant for modelID, matching
// packages/snapcompact/src/snapcompact.ts idealShapeVariant.
func IdealShapeVariant(modelID string) (IdealShape, bool) {
	if modelID == "" {
		return IdealShape{}, false
	}
	switch {
	case reClaudeHighRes.MatchString(modelID):
		return IdealShape{Variant: "11on16-bw", FrameSize: 1932}, true
	case reClaude.MatchString(modelID):
		return IdealShape{Variant: "11on16-bw"}, true
	case reGemini.MatchString(modelID):
		return IdealShape{Variant: "8on22-bw", FrameSize: 2048}, true
	case reOpenAI.MatchString(modelID):
		return IdealShape{Variant: "8on22-bw"}, true
	case reKimi.MatchString(modelID):
		return IdealShape{Variant: "8on22-bw"}, true
	case reGLM.MatchString(modelID):
		return IdealShape{Variant: "8on16-bw"}, true
	default:
		return IdealShape{}, false
	}
}

// ReaderIncludesThinking reports whether an archive read by target may carry
// assistant reasoning (`¶think:`). Claude models trip the reasoning_extraction
// classifier when earlier reasoning is quoted back as input, so Claude readers
// return false (matching packages/catalog classifier-cot-extraction).
func ReaderIncludesThinking(target ModelTarget) bool {
	if reClaude.MatchString(target.ID) {
		return false
	}
	if strings.EqualFold(target.Provider, "anthropic") || strings.EqualFold(target.Provider, "gitlab-duo") {
		if target.ID == "" || reClaude.MatchString(target.ID) {
			return false
		}
	}
	return true
}

func resolveHostImageMaxEdge(target ModelTarget) int {
	if strings.EqualFold(target.Provider, "cursor") {
		if reClaude.MatchString(target.ID) || reOpenAI.MatchString(target.ID) || strings.EqualFold(target.ID, "default") {
			return 1024
		}
	}
	return 0
}

// estimateFrameTokens computes the billed-token estimate for a square frame of
// edge size under the reader's billing regime (packages/catalog image-tokenization).
func estimateFrameTokens(target ModelTarget, frameSize int) (tokens int, verified bool) {
	if frameSize <= 0 {
		return FrameTokenEstimate, false
	}
	fam := detectAPIFamily(target.API)
	switch {
	case reGemini.MatchString(target.ID) || fam == familyGoogle:
		// Gemini 3+ bills a fixed media_resolution budget (1,120 tokens/image).
		return 1120, reGemini.MatchString(target.ID)
	case reClaudeHighRes.MatchString(target.ID):
		// Anthropic high-res: 28px patches capped at 4,784 tokens.
		patches := int(math.Ceil(float64(frameSize)/28.0)) * int(math.Ceil(float64(frameSize)/28.0))
		if patches > 4784 {
			patches = 4784
		}
		return patches, true
	case reClaude.MatchString(target.ID) || fam == familyAnthropic:
		// Standard Claude: (w*h)/750 capped at 1,568px edge (~1,600 tokens).
		edge := frameSize
		if edge > 1568 {
			edge = 1568
		}
		return int(math.Ceil(float64(edge*edge) / 750.0)), reClaude.MatchString(target.ID)
	case reOpenAI.MatchString(target.ID) || fam == familyOpenAI:
		// OpenAI 32px patches * 1.2 at detail: "original".
		patches := int(math.Ceil(float64(frameSize)/32.0)) * int(math.Ceil(float64(frameSize)/32.0))
		return int(math.Ceil(float64(patches) * 1.2)), reOpenAI.MatchString(target.ID)
	default:
		return FrameTokenEstimate, false
	}
}

// ResolveShape selects the eval-validated frame Shape for target and an optional
// explicit variant override ("auto" or "" selects automatically).
// upstream: packages/snapcompact/src/snapcompact.ts resolveShape
func ResolveShape(target ModelTarget, variant string) Shape {
	var base Shape
	if variant != "" && variant != "auto" {
		if v, ok := ShapeVariants[variant]; ok {
			base = v
		}
	}
	if base.Font == "" {
		if ideal, ok := IdealShapeVariant(target.ID); ok {
			base = ShapeVariants[ideal.Variant]
			if ideal.FrameSize > 0 {
				base.FrameSize = ideal.FrameSize
			}
		} else {
			base = ShapeVariants[familyDefaultVariant(detectAPIFamily(target.API))]
		}
	}
	if maxEdge := resolveHostImageMaxEdge(target); maxEdge > 0 && base.FrameSize > maxEdge {
		base.FrameSize = maxEdge
	}
	tokens, verified := estimateFrameTokens(target, base.FrameSize)
	base.FrameTokenEstimate = tokens
	base.FrameTokensVerified = verified
	if detectAPIFamily(target.API) == familyOpenAI {
		base.ImageDetail = "original"
	}
	return base
}

// PlannedFrameTokens mirrors packages/snapcompact/src/snapcompact.ts plannedFrameTokens.
func PlannedFrameTokens(shape Shape) int {
	if shape.FrameTokensVerified && shape.FrameTokenEstimate > 0 {
		return shape.FrameTokenEstimate
	}
	if shape.FrameTokenEstimate > FrameTokenEstimate {
		return shape.FrameTokenEstimate
	}
	return FrameTokenEstimate
}

// DenseCompanion returns the denser low-quality companion shape for the
// foveated archive middle (HQ/LQ/HQ).
// upstream: packages/snapcompact/src/snapcompact.ts denseCompanion
func DenseCompanion(high Shape) Shape {
	if high.Columns == 2 || high.Font == "silver" {
		return high
	}
	low := ShapeVariants["8on16-bw"]
	low.FrameSize = high.FrameSize
	low.FrameTokenEstimate = high.FrameTokenEstimate
	low.FrameTokensVerified = high.FrameTokensVerified
	low.ImageDetail = high.ImageDetail
	if ComputeGeometry(low, low.FrameSize).Capacity > ComputeGeometry(high, high.FrameSize).Capacity {
		return low
	}
	return high
}

// ComputeGeometry returns the character grid dimensions for shape at size.
// upstream: packages/snapcompact/src/snapcompact.ts geometry
func ComputeGeometry(shape Shape, size int) Geometry {
	if size <= 0 {
		size = shape.FrameSize
	}
	repeat := shape.LineRepeat
	if repeat < 1 {
		repeat = 1
	}
	cellW := shape.CellWidth
	if cellW < 1 {
		cellW = 8
	}
	cellH := shape.CellHeight
	if cellH < 1 {
		cellH = 16
	}
	gridCols := size / cellW
	rows := size / cellH / repeat
	if shape.Columns == 2 {
		cols := 0
		if gridCols > docGutter {
			cols = (gridCols - docGutter) / 2
		}
		return Geometry{Cols: cols, Rows: rows, Capacity: 2 * cols * rows}
	}
	return Geometry{Cols: gridCols, Rows: rows, Capacity: gridCols * rows}
}

// ProviderImageBudget returns the max images allowed per request for target.
// upstream: packages/catalog/src/compat/request-size.ts + provider KDL rules
func ProviderImageBudget(target ModelTarget) int {
	prov := strings.ToLower(target.Provider)
	switch prov {
	case "anthropic":
		if target.ContextWindow > 0 && target.ContextWindow <= 200_000 {
			return 100
		}
		return 600
	case "openai", "openai-codex":
		return 200
	case "google", "google-gemini-cli", "google-vertex":
		return 200
	case "amazon-bedrock":
		return 90
	case "github-copilot":
		return 80
	case "cursor":
		if reClaude.MatchString(target.ID) || reOpenAI.MatchString(target.ID) {
			return 20
		}
		return 60
	case "google-antigravity":
		return 60
	case "openrouter":
		return 60
	case "groq":
		return 5
	}
	switch detectAPIFamily(target.API) {
	case familyAnthropic:
		return 90
	case familyGoogle, familyOpenAI:
		return 80
	default:
		return DefaultProviderImageBudget
	}
}

// ProviderFrameBudget returns min(ProviderImageBudget, MaxFramesDefault).
func ProviderFrameBudget(target ModelTarget) int {
	b := ProviderImageBudget(target)
	if b > MaxFramesDefault {
		return MaxFramesDefault
	}
	if b < 1 {
		return 1
	}
	return b
}

// FrameDataBytesBudget returns the max standing base64 bytes for archive frames.
// upstream: packages/snapcompact/src/snapcompact.ts frameDataBytesBudget
func FrameDataBytesBudget(target ModelTarget) int {
	switch strings.ToLower(target.Provider) {
	case "anthropic":
		return 60_000_000
	case "deepseek":
		return 20_971_520
	case "devin":
		return 12_000_000
	case "factory-droid":
		return 1_750_000
	default:
		return DefaultFrameDataBytesBudget
	}
}

// MaxFramesForDataBudget estimates how many frames fit within budget base64 bytes.
// upstream: packages/snapcompact/src/snapcompact.ts maxFramesForDataBudget
func MaxFramesForDataBudget(shape Shape, budget int) int {
	if budget <= 0 {
		return 1
	}
	areaPriced := (shape.Font == "8x13" && shape.CellWidth == 8 && shape.CellHeight == 22 && shape.Variant == "bw" && shape.Columns <= 1 && !shape.StopwordDim && shape.LineRepeat <= 1) ||
		(shape.Font == "8x13" && shape.CellWidth == 11 && shape.CellHeight == 16 && shape.Variant == "bw" && shape.Columns <= 1 && !shape.StopwordDim && shape.LineRepeat <= 1)
	areaRatio := 1.0
	if areaPriced && shape.FrameSize > 0 {
		r := float64(shape.FrameSize) / 1932.0
		areaRatio = r * r
	}
	frameBytes := int(math.Ceil(float64(FrameDataBytesEstimate) * areaRatio))
	if frameBytes > FrameDataBytesEstimate {
		frameBytes = FrameDataBytesEstimate
	}
	if frameBytes < 1 {
		frameBytes = 1
	}
	n := budget / frameBytes
	if n < 1 {
		return 1
	}
	return n
}
