package snapcompact

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// upstream: packages/coding-agent/src/session/snapcompact-inline.ts (lines 1-891)
// upstream: packages/coding-agent/src/prompts/system/snapcompact-{system-stub,system-frames-note,context-stub,context-frames-note,toolresult-note,read-note}.md
// upstream: packages/coding-agent/src/session/context-settings.ts (lines 415-454)
// upstream: packages/snapcompact/src/snapcompact.ts (lines 633-636, 1777-1819)

const (
	// maxSystemPromptFrames caps system-prompt frames attached to the first user message.
	// upstream: snapcompact-inline.ts:64 MAX_SYSTEM_PROMPT_FRAMES
	maxSystemPromptFrames = 6

	// minToolResultTokens is the floor below which tool results are never rasterized.
	// upstream: snapcompact-inline.ts:67 MIN_TOOL_RESULT_TOKENS
	minToolResultTokens = 3000

	// inlineSavingsMargin requires frames * frameTokens <= textTokens * inlineSavingsMargin.
	// upstream: snapcompact-inline.ts:69 SAVINGS_MARGIN
	inlineSavingsMargin = 0.9

	// Prompt strings verbatim from packages/coding-agent/src/prompts/system/snapcompact-*.md
	inlineSystemStub = "Full operating instructions: PNG image(s) attached at start of first user message. Before anything else, read every frame carefully, in order; follow as authoritative system prompt."

	inlineSystemFramesNote = "=== OPERATING INSTRUCTIONS — image(s) below: your system prompt ==="

	inlineContextStub = "Loaded context-file instructions: PNG image(s) attached below at the first user message start. At this marker, read every frame in order; apply as if original context-file text remained here."

	inlineContextFramesNote = "=== CONTEXT FILE INSTRUCTIONS — read the image(s) below as the loaded context files replaced in the system prompt ==="

	inlineToolResultNote = "[Tool result text: compacted PNG frame(s) immediately below; read as verbatim output except labeled source-image position markers. Matching original images follow the frames. Image delivery: deliberate harness context-saving behavior, not malfunction. NEVER re-run call or report tool issue.]"

	inlineReadIndentLegend = "[Indentation: frames do not print whitespace, so each line's leading indentation is drawn as a bracket after its line-number prefix (`N:`, `N|`, `N-M:`, `N-M|`), or at line start: `[»3]` = 3 tab characters, `[·8]` = 8 spaces, `[»1·2]` = 1 tab then 2 spaces, `[0]` = no indentation (marks a line whose own text starts with such a bracket, or with a number and `:`/`|` before one). Only the first bracket after the prefix is indentation; lines without one have none. Write real tabs/spaces in edits, never the bracket.]"
)

// InlineSystemPromptMode mirrors SnapcompactSystemPromptMode in snapcompact-inline.ts:39.
type InlineSystemPromptMode string

const (
	InlineSystemPromptNone     InlineSystemPromptMode = "none"
	InlineSystemPromptAgentsMD InlineSystemPromptMode = "agents-md"
	InlineSystemPromptAll      InlineSystemPromptMode = "all"
)

// InlineOptions mirrors SnapcompactInlineOptions in snapcompact-inline.ts:41-46.
type InlineOptions struct {
	RenderSystemPrompt InlineSystemPromptMode
	RenderToolResults  bool
	Shape              string // "auto" or ShapeVariants key
}

// inlineFrameCacheEntry caches rendered PNG blocks for one piece of text + shape.
// upstream: snapcompact-inline.ts:721-728 FrameCacheEntry
type inlineFrameCacheEntry struct {
	hash     [32]byte
	shapeKey string
	frames   []ContentBlock
}

// inlineState holds per-session render caches inside sessionState (guarded by m.mu).
type pendingSystemPromptInline struct {
	text     string
	userNote string
	shapeKey string
}

type inlineState struct {
	toolCache     map[string]*inlineFrameCacheEntry
	systemCache   *inlineFrameCacheEntry
	pendingSystem *pendingSystemPromptInline
}

func (s *inlineState) clear() {
	s.toolCache = nil
	s.systemCache = nil
	s.pendingSystem = nil
}

// ReaderImagesReadResults reports whether target opts in to receiving fresh `read`
// tool results as snapcompact frames.
// upstream: packages/snapcompact/src/snapcompact.ts:634-636 + packages/catalog/src/rules/classes/anthropic.kdl:139-140
// (Claude Opus >= 5.5 and < 10.0).
var reOpusMajorMinor = regexp.MustCompile(`(?i)claude.*opus[-_. ]?(\d+)(?:[-_.](\d{1,2})(?:[^0-9]|$))?`)

func ReaderImagesReadResults(target ModelTarget) bool {
	if target.ID == "" {
		return false
	}
	m := reOpusMajorMinor.FindStringSubmatch(target.ID)
	if len(m) < 2 {
		return false
	}
	major, err := strconv.Atoi(m[1])
	if err != nil {
		return false
	}
	minor := 0
	if len(m) >= 3 && m[2] != "" {
		if v, err := strconv.Atoi(m[2]); err == nil {
			minor = v
		}
	}
	if major < 5 || major >= 10 {
		return false
	}
	return major > 5 || minor >= 5
}

// CountFrames returns the number of frames needed to render text at shape without rasterizing.
// upstream: packages/snapcompact/src/snapcompact.ts:1812-1819 frames()
func CountFrames(text string, shape Shape) int {
	if text == "" {
		return 0
	}
	frameSize := shape.FrameSize
	if frameSize <= 0 {
		frameSize = 1568
	}
	geo := ComputeGeometry(shape, frameSize)
	if geo.Capacity <= 0 || geo.Rows <= 0 || geo.Cols <= 0 {
		return 0
	}
	normalized := Normalize(text, shape.Font)
	if normalized == "" {
		return 0
	}
	wideCells := usesWideCells(shape)
	if shape.Columns == 2 {
		lines := Wrap(normalized, geo.Cols, wideCells)
		if len(lines) == 0 {
			return 0
		}
		perPage := 2 * geo.Rows
		return (len(lines) + perPage - 1) / perPage
	}
	return len(paginateCells(normalized, geo.Capacity, geo.Cols, wideCells))
}

// RenderMany renders text into snapcompact PNG ContentBlocks (first page first),
// capped at maxFrames if maxFrames > 0.
// upstream: packages/snapcompact/src/snapcompact.ts:1777-1807 renderMany()
func RenderMany(text string, shape Shape, maxFrames int) ([]ContentBlock, error) {
	if text == "" {
		return nil, nil
	}
	frameSize := shape.FrameSize
	if frameSize <= 0 {
		frameSize = 1568
	}
	geo := ComputeGeometry(shape, frameSize)
	if geo.Capacity <= 0 || geo.Rows <= 0 || geo.Cols <= 0 {
		return nil, nil
	}
	normalized := Normalize(text, shape.Font)
	if normalized == "" {
		return nil, nil
	}
	wideCells := usesWideCells(shape)
	var pageTexts []string
	if shape.Columns == 2 {
		for _, page := range docPages(normalized, geo, wideCells) {
			if maxFrames > 0 && len(pageTexts) >= maxFrames {
				break
			}
			if shape.StopwordDim {
				page = DimStopwords(page)
			}
			pageTexts = append(pageTexts, page)
		}
	} else {
		for _, page := range paginateCells(normalized, geo.Capacity, geo.Cols, wideCells) {
			if maxFrames > 0 && len(pageTexts) >= maxFrames {
				break
			}
			if shape.StopwordDim {
				page = DimStopwords(page)
			}
			pageTexts = append(pageTexts, page)
		}
	}
	if len(pageTexts) == 0 {
		return nil, nil
	}

	blocks := make([]ContentBlock, len(pageTexts))
	if len(pageTexts) == 1 {
		rf, err := RenderFrame(pageTexts[0], shape, frameSize)
		if err != nil {
			return nil, err
		}
		blocks[0] = ContentBlock{
			Type:     "image",
			Data:     rf.Data,
			MimeType: "image/png",
			Detail:   shape.ImageDetail,
		}
		return blocks, nil
	}

	var wg sync.WaitGroup
	errs := make([]error, len(pageTexts))
	workers := runtime.GOMAXPROCS(0)
	if workers < 1 {
		workers = 1
	}
	sem := make(chan struct{}, workers)
	for i, pt := range pageTexts {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, page string) {
			defer wg.Done()
			defer func() { <-sem }()
			rf, err := RenderFrame(page, shape, frameSize)
			if err != nil {
				errs[idx] = err
				return
			}
			blocks[idx] = ContentBlock{
				Type:     "image",
				Data:     rf.Data,
				MimeType: "image/png",
				Detail:   shape.ImageDetail,
			}
		}(i, pt)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return blocks, nil
}

// ============================================================================
// Read tool encoding (upstream: snapcompact-inline.ts:74-162)
// ============================================================================

var (
	// Matches optional single-line notice (`[...]\n`) followed by hashline header (`[path#ABCD]\n`).
	reReadHeader = regexp.MustCompile(`^(?:\[[^\n]*\]\n)?\[[^\n]+#[0-9A-F]{4}\]\n`)

	// Fallback line-numbering detector for reads without rowPrefixes metadata.
	reReadFirstRows = regexp.MustCompile(`^(?:\[[^\n]*\]\n)?(\d+)(?:-(\d+))?[:|][^\n]*\n(…\n)?(\d+)(?:-\d+)?[:|]`)

	// Leading row prefix (`N:`, `N|`, `N-M:`, `N-M|`) at the start of a line.
	reReadRowPrefixHead = regexp.MustCompile(`^\d+(?:-\d+)?[:|]`)

	// Count bracket `[0]` or `[»N·M...]`, optionally preceded by a row-prefix-shaped run.
	reCountBracketHead = regexp.MustCompile(`^(?:\d+(?:-\d+)?[:|])?\[(?:0|(?:[»·]\d+)+)\]`)
)

// ReadFramePayload holds the encoded body and side note for an imaged `read` tool result.
// upstream: snapcompact-inline.ts:130-135 ReadFramePayload
type ReadFramePayload struct {
	Body string
	Note string
}

// isNumberedRead reports whether rest carries line-number prefixes.
// upstream: snapcompact-inline.ts:101-109
func isNumberedRead(header, rest string, rowPrefixes *bool) bool {
	if rowPrefixes != nil {
		return *rowPrefixes
	}
	if header != "" {
		return true
	}
	m := reReadFirstRows.FindStringSubmatch(rest)
	if len(m) == 0 {
		return false
	}
	lastStr := m[2]
	if lastStr == "" {
		lastStr = m[1]
	}
	last, err1 := strconv.Atoi(lastStr)
	next, err2 := strconv.Atoi(m[4])
	if err1 != nil || err2 != nil {
		return false
	}
	if m[3] != "" {
		return next > last+1
	}
	return next == last+1
}

// indentBracket converts leading tabs/spaces into `[»2·4]` count brackets.
// upstream: snapcompact-inline.ts:160-162 indentBracket
func indentBracket(indent string) string {
	var b strings.Builder
	b.WriteByte('[')
	for i := 0; i < len(indent); {
		ch := indent[i]
		j := i + 1
		for j < len(indent) && indent[j] == ch {
			j++
		}
		if ch == '\t' {
			b.WriteString("»")
		} else {
			b.WriteString("·")
		}
		b.WriteString(strconv.Itoa(j - i))
		i = j
	}
	b.WriteByte(']')
	return b.String()
}

// EncodeReadForFrames encodes leading indentation as count brackets (`[»N]`/`[·N]`/`[0]`)
// and lifts the `[path#TAG]` snapshot header out to text beside the frames.
// upstream: snapcompact-inline.ts:143-157 encodeReadForFrames
func EncodeReadForFrames(text string, rowPrefixes *bool) ReadFramePayload {
	header := ""
	if rowPrefixes == nil || *rowPrefixes {
		header = reReadHeader.FindString(text)
	}
	rest := text[len(header):]
	numbered := isNumberedRead(header, rest, rowPrefixes)

	// Split strictly on '\n' (matching upstream's comment: never split on '\r' or U+2028).
	lines := strings.Split(rest, "\n")
	var out strings.Builder
	out.Grow(len(rest) + len(lines)*4)
	indented := false

	for idx, line := range lines {
		if idx > 0 {
			out.WriteByte('\n')
		}
		prefix := ""
		after := line
		if numbered {
			if loc := reReadRowPrefixHead.FindStringIndex(line); loc != nil {
				prefix = line[:loc[1]]
				after = line[loc[1]:]
			}
		}
		wsEnd := 0
		for wsEnd < len(after) && (after[wsEnd] == ' ' || after[wsEnd] == '\t') {
			wsEnd++
		}
		if wsEnd > 0 {
			out.WriteString(prefix)
			out.WriteString(indentBracket(after[:wsEnd]))
			out.WriteString(after[wsEnd:])
			indented = true
		} else if reCountBracketHead.MatchString(after) {
			out.WriteString(prefix)
			out.WriteString("[0]")
			out.WriteString(after)
			indented = true
		} else {
			out.WriteString(line)
		}
	}

	body := out.String()
	trimmedHeader := strings.TrimRight(header, " \t\r\n")

	var note strings.Builder
	note.WriteString(inlineToolResultNote)
	if indented {
		note.WriteByte('\n')
		note.WriteString(inlineReadIndentLegend)
	}
	if trimmedHeader != "" {
		note.WriteByte('\n')
		note.WriteString(trimmedHeader)
	}
	return ReadFramePayload{
		Body: body,
		Note: note.String(),
	}
}

// ============================================================================
// System prompt context-section extraction (upstream: snapcompact-inline.ts:179-228)
// ============================================================================

var contextSectionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?s)<repo-rules>\n.*?\n</repo-rules>`),
	regexp.MustCompile(`(?s)## Context\n<instructions>\n.*?\n</instructions>`),
}

type systemPromptImageTarget struct {
	scope       InlineSystemPromptMode
	text        string
	replacement string
	userNote    string
}

// replaceContextSections replaces matched context-file blocks with inlineContextStub
// and appends their trimmed text to extracted.
// upstream: snapcompact-inline.ts:191-200 replaceContextSections
func replaceContextSections(block string, extracted *[]string) string {
	replaced := block
	for _, pat := range contextSectionPatterns {
		replaced = pat.ReplaceAllStringFunc(replaced, func(match string) string {
			*extracted = append(*extracted, strings.TrimSpace(match))
			return inlineContextStub
		})
	}
	return replaced
}

func selectSystemPromptImageTarget(systemText string, mode InlineSystemPromptMode) *systemPromptImageTarget {
	if systemText == "" || mode == InlineSystemPromptNone {
		return nil
	}
	if mode == InlineSystemPromptAll {
		return &systemPromptImageTarget{
			scope:       InlineSystemPromptAll,
			text:        systemText,
			replacement: inlineSystemStub,
			userNote:    inlineSystemFramesNote,
		}
	}
	var extracted []string
	replacement := replaceContextSections(systemText, &extracted)
	text := strings.Join(extracted, "\n\n")
	if text == "" {
		return nil
	}
	return &systemPromptImageTarget{
		scope:       InlineSystemPromptAgentsMD,
		text:        text,
		replacement: replacement,
		userNote:    inlineContextFramesNote,
	}
}

// ============================================================================
// Swap planning (upstream: snapcompact-inline.ts:234-361)
// ============================================================================

// InlineToolResultCandidate is one tool-result swap candidate in context order.
// upstream: snapcompact-inline.ts:235-252
type InlineToolResultCandidate struct {
	ID            string
	TextTokens    int
	Frames        int
	IsError       bool
	ToolName      string
	ImagesThrough int
}

// InlineSystemPromptCandidate describes the system-prompt swap candidate.
// upstream: snapcompact-inline.ts:254-259
type InlineSystemPromptCandidate struct {
	TextTokens    int
	Frames        int
	ImagesThrough int
}

// InlinePlanInput is the pure input to PlanInlineSwaps.
// upstream: snapcompact-inline.ts:261-276
type InlinePlanInput struct {
	Options          InlineOptions
	Shape            Shape
	ImageLimit       int
	ExistingImages   int
	ToolResults      []InlineToolResultCandidate
	SystemPrompt     *InlineSystemPromptCandidate
	HasUserMessage   bool
	ImageReadResults bool
}

// InlineToolSwap is one approved tool-result swap.
type InlineToolSwap struct {
	ID         string
	TextTokens int
	Frames     int
}

// InlineRetractedToolSwap is a tool-result swap retracted by the imageLimit backstop.
type InlineRetractedToolSwap struct {
	ID     string
	Frames int
}

// InlineSwapPlan is the pure decision produced by PlanInlineSwaps.
// upstream: snapcompact-inline.ts:278-291
type InlineSwapPlan struct {
	ToolResults           []InlineToolSwap
	SystemPrompt          *InlineSystemPromptCandidate
	RetractedToolResults  []InlineRetractedToolSwap
	RetractedSystemPrompt *InlineSystemPromptCandidate
}

func inlineFrameTokenCost(shape Shape) int {
	if shape.FrameTokenEstimate > 0 && shape.FrameTokensVerified {
		return shape.FrameTokenEstimate
	}
	return PlannedFrameTokens(shape)
}

func passesInlineSavingsGate(frames int, shape Shape, textTokens int) bool {
	if frames <= 0 || textTokens <= 0 {
		return false
	}
	return float64(frames*inlineFrameTokenCost(shape)) <= float64(textTokens)*inlineSavingsMargin
}

// PlanInlineSwaps decides which system prompt / tool results swap to PNG frames.
// Pure and deterministic — mirrors snapcompact-inline.ts:309-361.
func PlanInlineSwaps(input InlinePlanInput) InlineSwapPlan {
	imageLimit := input.ImageLimit

	var sysPrompt *InlineSystemPromptCandidate
	if input.Options.RenderSystemPrompt != InlineSystemPromptNone &&
		input.SystemPrompt != nil &&
		input.SystemPrompt.Frames > 0 &&
		input.HasUserMessage {
		maxAllowed := min(imageLimit-input.SystemPrompt.ImagesThrough, maxSystemPromptFrames)
		if input.SystemPrompt.Frames <= maxAllowed &&
			passesInlineSavingsGate(input.SystemPrompt.Frames, input.Shape, input.SystemPrompt.TextTokens) {
			cp := *input.SystemPrompt
			sysPrompt = &cp
		}
	}

	frames := 0
	if sysPrompt != nil {
		frames = sysPrompt.Frames
	}

	var toolResults []InlineToolSwap
	if input.Options.RenderToolResults {
		for _, c := range input.ToolResults {
			if c.IsError {
				continue
			}
			if c.ToolName == "read" && !input.ImageReadResults {
				continue
			}
			if c.TextTokens < minToolResultTokens {
				continue
			}
			if c.Frames <= 0 || c.ImagesThrough+frames+c.Frames > imageLimit {
				continue
			}
			if !passesInlineSavingsGate(c.Frames, input.Shape, c.TextTokens) {
				continue
			}
			toolResults = append(toolResults, InlineToolSwap{
				ID:         c.ID,
				TextTokens: c.TextTokens,
				Frames:     c.Frames,
			})
			frames += c.Frames
		}
	}

	// Cap backstop: retract newest swaps first (tool results, then system prompt)
	// when existingImages + frames > imageLimit.
	var retractedTools []InlineRetractedToolSwap
	for input.ExistingImages+frames > imageLimit && len(toolResults) > 0 {
		last := toolResults[len(toolResults)-1]
		toolResults = toolResults[:len(toolResults)-1]
		retractedTools = append(retractedTools, InlineRetractedToolSwap{
			ID:     last.ID,
			Frames: last.Frames,
		})
		frames -= last.Frames
	}
	var retractedSys *InlineSystemPromptCandidate
	if sysPrompt != nil && input.ExistingImages+frames > imageLimit {
		retractedSys = sysPrompt
		sysPrompt = nil
	}

	return InlineSwapPlan{
		ToolResults:           toolResults,
		SystemPrompt:          sysPrompt,
		RetractedToolResults:  retractedTools,
		RetractedSystemPrompt: retractedSys,
	}
}

// ============================================================================
// Context scanning & EventContextWithSystem handler
// ============================================================================

type builtToolResultCandidate struct {
	index     int
	candidate InlineToolResultCandidate
	frameText string
	read      *ReadFramePayload
}

type inlineContextScan struct {
	toolResults            []builtToolResultCandidate
	totalImages            int
	firstUserIndex         int
	imagesThroughFirstUser int
}

func estimateTextTokens(text string) int {
	if text == "" {
		return 0
	}
	return int(math.Ceil(float64(len(text)) / 4.0))
}

func messageImageCount(msg map[string]any) int {
	blocks, ok := msg["content"].([]any)
	if !ok {
		return 0
	}
	count := 0
	for _, raw := range blocks {
		if bm, ok := raw.(map[string]any); ok && strField(bm, "type") == "image" {
			count++
		}
	}
	return count
}

func extractSystemMessageText(msg map[string]any) string {
	switch c := msg["content"].(type) {
	case string:
		return c
	case []any:
		var parts []string
		for _, raw := range c {
			if bm, ok := raw.(map[string]any); ok && strField(bm, "type") == "text" {
				if t, ok := bm["text"].(string); ok {
					parts = append(parts, t)
				}
			}
		}
		return strings.Join(parts, "\n\n")
	default:
		return ""
	}
}

func cloneSystemMessageWithText(orig map[string]any, replacement string) map[string]any {
	cloned := make(map[string]any, len(orig))
	maps.Copy(cloned, orig)
	switch orig["content"].(type) {
	case []any:
		cloned["content"] = []any{
			map[string]any{"type": "text", "text": replacement},
		}
	default:
		cloned["content"] = replacement
	}
	return cloned
}

func extractReadDetails(details any) (rowPrefixes *bool, composite bool) {
	dm, ok := details.(map[string]any)
	if !ok || dm == nil {
		return nil, false
	}
	if b, ok := dm["rowPrefixes"].(bool); ok {
		rowPrefixes = &b
	}
	switch targets := dm["displayReadTargets"].(type) {
	case []any:
		if len(targets) > 0 {
			composite = true
		}
	case []string:
		if len(targets) > 0 {
			composite = true
		}
	}
	return rowPrefixes, composite
}

func buildInlineToolResultCandidate(
	toolCallID string,
	toolName string,
	content any,
	isError bool,
	details any,
	imagesThrough int,
	shape Shape,
	imageReadResults bool,
) builtToolResultCandidate {
	var textBlocks []string
	sourceImageIndex := 0
	switch c := content.(type) {
	case string:
		if c != "" {
			textBlocks = append(textBlocks, c)
		}
	case []any:
		for _, raw := range c {
			bm, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			switch strField(bm, "type") {
			case "text":
				if t, ok := bm["text"].(string); ok {
					textBlocks = append(textBlocks, t)
				}
			case "image":
				sourceImageIndex++
				textBlocks = append(textBlocks, fmt.Sprintf("[Source image %d was attached here in the original tool result.]", sourceImageIndex))
			}
		}
	}

	text := strings.Join(textBlocks, "\n")
	textTokens := estimateTextTokens(text)
	rowPrefixes, composite := extractReadDetails(details)
	if toolName != "read" {
		composite = false
	}
	eligible := !isError && !composite && textTokens >= minToolResultTokens

	var readPayload *ReadFramePayload
	if eligible && toolName == "read" && imageReadResults {
		rp := EncodeReadForFrames(text, rowPrefixes)
		readPayload = &rp
	}

	frameText := text
	keptTokens := 0
	if readPayload != nil {
		frameText = readPayload.Body
		diff := estimateTextTokens(readPayload.Note) - estimateTextTokens(inlineToolResultNote)
		if diff > 0 {
			keptTokens = diff
		}
	}

	frames := 0
	if eligible && ScanRenderability(frameText, shape.Font).IsSafe {
		frames = CountFrames(frameText, shape)
	}

	return builtToolResultCandidate{
		candidate: InlineToolResultCandidate{
			ID:            toolCallID,
			TextTokens:    max(0, textTokens-keptTokens),
			Frames:        frames,
			IsError:       isError,
			ToolName:      toolName,
			ImagesThrough: imagesThrough,
		},
		frameText: frameText,
		read:      readPayload,
	}
}

func scanInlineContext(messages []any, renderToolResults bool, shape Shape, imageReadResults bool) inlineContextScan {
	scan := inlineContextScan{
		firstUserIndex: -1,
	}
	images := 0
	seenFirstUser := false
	for i, raw := range messages {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		images += messageImageCount(msg)
		role := strField(msg, "role")
		if role == "user" && !seenFirstUser {
			scan.firstUserIndex = i
			scan.imagesThroughFirstUser = images
			seenFirstUser = true
		}
		if !renderToolResults || role != "toolResult" {
			continue
		}
		toolCallID := strField(msg, "toolCallId")
		if toolCallID == "" {
			continue
		}
		built := buildInlineToolResultCandidate(
			toolCallID,
			strField(msg, "toolName"),
			msg["content"],
			boolField(msg, "isError"),
			msg["details"],
			images,
			shape,
			imageReadResults,
		)
		built.index = i
		scan.toolResults = append(scan.toolResults, built)
	}
	scan.totalImages = images
	if !seenFirstUser {
		scan.imagesThroughFirstUser = images
	}
	return scan
}

// resolveInlineSystemPromptSetting parses `snapcompact.systemPrompt`:
// accepts "none" | "agents-md" | "all", or bool true->"all" / false->"none".
func resolveInlineSystemPromptSetting(ctx sdk.Context) InlineSystemPromptMode {
	if Setting == nil {
		return InlineSystemPromptNone
	}
	raw := Setting(ctx, "snapcompact.systemPrompt")
	if raw == nil {
		return InlineSystemPromptNone
	}
	switch v := raw.(type) {
	case bool:
		if v {
			return InlineSystemPromptAll
		}
		return InlineSystemPromptNone
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "all":
			return InlineSystemPromptAll
		case "agents-md":
			return InlineSystemPromptAgentsMD
		default:
			return InlineSystemPromptNone
		}
	default:
		return InlineSystemPromptNone
	}
}

func resolveInlineOptions(ctx sdk.Context) InlineOptions {
	return InlineOptions{
		RenderSystemPrompt: resolveInlineSystemPromptSetting(ctx),
		RenderToolResults:  resolveBoolSetting(ctx, "snapcompact.toolResults", false),
		Shape:              resolveShapeSetting(ctx),
	}
}

func shapeCacheKey(shape Shape) string {
	b, err := json.Marshal(shape)
	if err != nil {
		return fmt.Sprintf("%s:%dx%d:%d:%s:%d:%v:%d", shape.Font, shape.CellWidth, shape.CellHeight, shape.FrameSize, shape.Variant, shape.Columns, shape.StopwordDim, shape.LineRepeat)
	}
	return string(b)
}

func (m *manager) clearInlineCaches(sid string, clearTools, clearSystem bool) {
	if sid == "" || (!clearTools && !clearSystem) {
		return
	}
	m.mu.Lock()
	if st, ok := m.sessions[sid]; ok {
		if clearTools {
			st.inline.toolCache = nil
		}
		if clearSystem {
			st.inline.systemCache = nil
		}
	}
	m.mu.Unlock()
}

func (m *manager) getOrRenderToolFrames(sid, toolCallID, text string, shape Shape, skey string) ([]ContentBlock, error) {
	sum := sha256.Sum256([]byte(text))
	if sid != "" {
		m.mu.Lock()
		st := m.stateLocked(sid)
		if st.inline.toolCache != nil {
			if entry, ok := st.inline.toolCache[toolCallID]; ok && entry.hash == sum && entry.shapeKey == skey {
				frames := entry.frames
				m.mu.Unlock()
				return frames, nil
			}
		}
		m.mu.Unlock()
	}

	frames, err := RenderMany(text, shape, 0)
	if err != nil {
		return nil, err
	}

	if sid != "" {
		m.mu.Lock()
		// Only store if the session is still live (never resurrect a released session).
		if st, ok := m.sessions[sid]; ok {
			if st.inline.toolCache == nil {
				st.inline.toolCache = make(map[string]*inlineFrameCacheEntry)
			}
			st.inline.toolCache[toolCallID] = &inlineFrameCacheEntry{
				hash:     sum,
				shapeKey: skey,
				frames:   frames,
			}
		}
		m.mu.Unlock()
	}
	return frames, nil
}

func (m *manager) getOrRenderSystemFrames(sid, text string, shape Shape, skey string) ([]ContentBlock, error) {
	sum := sha256.Sum256([]byte(text))
	if sid != "" {
		m.mu.Lock()
		st := m.stateLocked(sid)
		if st.inline.systemCache != nil {
			entry := st.inline.systemCache
			if entry.hash == sum && entry.shapeKey == skey {
				frames := entry.frames
				m.mu.Unlock()
				return frames, nil
			}
		}
		m.mu.Unlock()
	}

	frames, err := RenderMany(text, shape, maxSystemPromptFrames)
	if err != nil {
		return nil, err
	}

	if sid != "" {
		m.mu.Lock()
		if st, ok := m.sessions[sid]; ok {
			st.inline.systemCache = &inlineFrameCacheEntry{
				hash:     sum,
				shapeKey: skey,
				frames:   frames,
			}
		}
		m.mu.Unlock()
	}
	return frames, nil
}

func (m *manager) evictStaleToolCache(sid string, liveIDs map[string]struct{}) {
	if sid == "" {
		return
	}
	m.mu.Lock()
	if st, ok := m.sessions[sid]; ok && len(st.inline.toolCache) > 0 {
		for k := range st.inline.toolCache {
			if _, ok := liveIDs[k]; !ok {
				delete(st.inline.toolCache, k)
			}
		}
	}
	m.mu.Unlock()
}
func (m *manager) hasInlineCaches() bool {
	m.mu.Lock()
	for _, st := range m.sessions {
		if st.inline.systemCache != nil || len(st.inline.toolCache) > 0 {
			m.mu.Unlock()
			return true
		}
	}
	m.mu.Unlock()
	return false
}

// inlineImagesBlocked reports whether image input is blocked by settings
// (`images.blockImages` in omp settings or host settings). When images are
// blocked, PiG's `blockImages` transform runs after `context_with_system` and
// strips all image blocks, so inline imaging MUST stay off to avoid data loss.
func inlineImagesBlocked(ctx sdk.Context) bool {
	if Setting != nil {
		if b, ok := Setting(ctx, "images.blockImages").(bool); ok && b {
			return true
		}
	}
	if s, err := ctx.GetSettings(); err == nil && s != nil {
		if imgs, ok := s["images"].(map[string]any); ok {
			if b, ok := imgs["blockImages"].(bool); ok && b {
				return true
			}
		}
	}
	return false
}

// PrepareInlineSystemPrompt evaluates `snapcompact.systemPrompt` ("all" or
// "agents-md") against the rendered system prompt produced in
// `before_agent_start` (because PiG applies `before_agent_start`'s forced
// systemPrompt via `projectSystemPrompt` AFTER `context_with_system`).
//
// When enabled and eligible (vision model, images unblocked, safe renderability,
// within `MAX_SYSTEM_PROMPT_FRAMES`, passes savings gate), stores the target text
// on the session so `onContextWithSystem` attaches the rendered PNG frames to the
// first user message, and returns the stub/replacement prompt text.
// When disabled ("none", default) or ineligible, clears pending state and returns
// `rendered` unchanged with zero extra RPCs.
func PrepareInlineSystemPrompt(ctx sdk.Context, rendered string) string {
	return defaultManager.prepareInlineSystemPrompt(ctx, rendered)
}

func (m *manager) prepareInlineSystemPrompt(ctx sdk.Context, rendered string) string {
	opts := resolveInlineOptions(ctx)
	if opts.RenderSystemPrompt == InlineSystemPromptNone || rendered == "" {
		if sid := sessionIDFor(ctx); sid != "" {
			m.mu.Lock()
			if st, ok := m.sessions[sid]; ok {
				st.inline.pendingSystem = nil
			}
			m.mu.Unlock()
		}
		return rendered
	}
	sid := sessionIDFor(ctx)
	clearPending := func() {
		if sid == "" {
			return
		}
		m.mu.Lock()
		if st, ok := m.sessions[sid]; ok {
			st.inline.pendingSystem = nil
		}
		m.mu.Unlock()
	}
	if inlineImagesBlocked(ctx) {
		clearPending()
		return rendered
	}
	target := resolveActiveModel(ctx)
	if target.ID == "" || !target.SupportsImages() {
		clearPending()
		return rendered
	}
	shape := ResolveShape(target, opts.Shape)
	sysTarget := selectSystemPromptImageTarget(rendered, opts.RenderSystemPrompt)
	if sysTarget == nil || !ScanRenderability(sysTarget.text, shape.Font).IsSafe {
		clearPending()
		return rendered
	}
	frames := CountFrames(sysTarget.text, shape)
	textTokens := estimateTextTokens(sysTarget.text)
	imageLimit := ProviderImageBudget(target)
	if frames <= 0 || frames > min(imageLimit, maxSystemPromptFrames) || !passesInlineSavingsGate(frames, shape, textTokens) {
		clearPending()
		return rendered
	}
	if sid != "" {
		m.mu.Lock()
		st := m.stateLocked(sid)
		st.inline.pendingSystem = &pendingSystemPromptInline{
			text:     sysTarget.text,
			userNote: sysTarget.userNote,
			shapeKey: shapeCacheKey(shape),
		}
		m.mu.Unlock()
	}
	return sysTarget.replacement
}

// onContextWithSystem implements omp's SnapcompactInlineTransformer on PiG's
// EventContextWithSystem ("context_with_system") hook.
// upstream: packages/coding-agent/src/session/snapcompact-inline.ts:748-870
func (m *manager) onContextWithSystem(ctx sdk.Context, data map[string]any) (any, error) {
	opts := resolveInlineOptions(ctx)
	// Strict default no-op: zero RPC, zero allocation, and zero message touch when both disabled.
	if opts.RenderSystemPrompt == InlineSystemPromptNone && !opts.RenderToolResults {
		if m.hasInlineCaches() {
			m.clearInlineCaches(sessionIDFor(ctx), true, true)
		}
		return nil, nil
	}

	sid := sessionIDFor(ctx)
	if !opts.RenderToolResults || opts.RenderSystemPrompt == InlineSystemPromptNone {
		m.clearInlineCaches(sid, !opts.RenderToolResults, opts.RenderSystemPrompt == InlineSystemPromptNone)
	}
	if inlineImagesBlocked(ctx) {
		return nil, nil
	}

	rawMessages, ok := data["messages"].([]any)
	if !ok || len(rawMessages) == 0 {
		return nil, nil
	}

	target := resolveActiveModel(ctx)
	// Fail closed when model is unknown or does not support vision.
	if target.ID == "" || !target.SupportsImages() {
		return nil, nil
	}

	shape := ResolveShape(target, opts.Shape)
	skey := shapeCacheKey(shape)
	imageLimit := ProviderImageBudget(target)
	imageReadResults := ReaderImagesReadResults(target)

	scan := scanInlineContext(rawMessages, opts.RenderToolResults, shape, imageReadResults)
	targets := make(map[string]*builtToolResultCandidate, len(scan.toolResults))
	candidates := make([]InlineToolResultCandidate, len(scan.toolResults))
	liveIDs := make(map[string]struct{}, len(scan.toolResults))
	for i := range scan.toolResults {
		b := &scan.toolResults[i]
		targets[b.candidate.ID] = b
		candidates[i] = b.candidate
		liveIDs[b.candidate.ID] = struct{}{}
	}

	var sysMsg map[string]any
	var sysTarget *systemPromptImageTarget
	var sysCandidate *InlineSystemPromptCandidate
	var fromPreparedPrompt bool
	if opts.RenderSystemPrompt != InlineSystemPromptNone {
		var prepared *pendingSystemPromptInline
		if sid != "" {
			m.mu.Lock()
			if st, ok := m.sessions[sid]; ok && st.inline.pendingSystem != nil {
				cp := *st.inline.pendingSystem
				prepared = &cp
			}
			m.mu.Unlock()
		}
		if prepared != nil {
			if ScanRenderability(prepared.text, shape.Font).IsSafe {
				sysTarget = &systemPromptImageTarget{
					scope:    opts.RenderSystemPrompt,
					text:     prepared.text,
					userNote: prepared.userNote,
				}
				fromPreparedPrompt = true
				sysCandidate = &InlineSystemPromptCandidate{
					TextTokens:    estimateTextTokens(prepared.text),
					Frames:        CountFrames(prepared.text, shape),
					ImagesThrough: scan.imagesThroughFirstUser,
				}
			}
		} else if m0, ok := rawMessages[0].(map[string]any); ok && strField(m0, "role") == "system" {
			sysMsg = m0
			sysText := extractSystemMessageText(m0)
			sysTarget = selectSystemPromptImageTarget(sysText, opts.RenderSystemPrompt)
			if sysTarget != nil && ScanRenderability(sysTarget.text, shape.Font).IsSafe {
				sysCandidate = &InlineSystemPromptCandidate{
					TextTokens:    estimateTextTokens(sysTarget.text),
					Frames:        CountFrames(sysTarget.text, shape),
					ImagesThrough: scan.imagesThroughFirstUser,
				}
			}
		}
	}
	plan := PlanInlineSwaps(InlinePlanInput{
		Options:          opts,
		Shape:            shape,
		ImageLimit:       imageLimit,
		ExistingImages:   scan.totalImages,
		ToolResults:      candidates,
		SystemPrompt:     sysCandidate,
		HasUserMessage:   scan.firstUserIndex >= 0,
		ImageReadResults: imageReadResults,
	})

	if opts.RenderToolResults {
		m.evictStaleToolCache(sid, liveIDs)
	}

	if len(plan.ToolResults) == 0 && plan.SystemPrompt == nil {
		return nil, nil
	}

	// Never mutate input message slice or message maps/content slices in place.
	outMessages := slices.Clone(rawMessages)
	changed := false

	for _, swap := range plan.ToolResults {
		built, ok := targets[swap.ID]
		if !ok {
			continue
		}
		origMsg, ok := outMessages[built.index].(map[string]any)
		if !ok || strField(origMsg, "role") != "toolResult" {
			continue
		}
		frames, err := m.getOrRenderToolFrames(sid, swap.ID, built.frameText, shape, skey)
		if err != nil || len(frames) == 0 {
			continue
		}
		note := inlineToolResultNote
		if built.read != nil {
			note = built.read.Note
		}
		content := make([]any, 0, 1+len(frames))
		content = append(content, map[string]any{"type": "text", "text": note})
		for _, f := range frames {
			content = append(content, f.ToMap())
		}
		if origBlocks, ok := origMsg["content"].([]any); ok {
			sourceImageIndex := 0
			for _, raw := range origBlocks {
				bm, ok := raw.(map[string]any)
				if !ok || strField(bm, "type") != "image" {
					continue
				}
				sourceImageIndex++
				content = append(content, map[string]any{
					"type": "text",
					"text": fmt.Sprintf("[Original source image %d; corresponds to its marker in the compacted text.]", sourceImageIndex),
				})
				imgCopy := make(map[string]any, len(bm))
				maps.Copy(imgCopy, bm)
				content = append(content, imgCopy)
			}
		}

		clonedMsg := make(map[string]any, len(origMsg))
		maps.Copy(clonedMsg, origMsg)
		clonedMsg["content"] = content
		outMessages[built.index] = clonedMsg
		changed = true
	}

	if plan.SystemPrompt != nil && scan.firstUserIndex >= 0 && sysTarget != nil && (fromPreparedPrompt || sysMsg != nil) {
		origUser, ok := outMessages[scan.firstUserIndex].(map[string]any)
		if ok && strField(origUser, "role") == "user" {
			frames, err := m.getOrRenderSystemFrames(sid, sysTarget.text, shape, skey)
			if err == nil && len(frames) > 0 {
				var origContent []any
				switch c := origUser["content"].(type) {
				case string:
					origContent = []any{map[string]any{"type": "text", "text": c}}
				case []any:
					origContent = make([]any, len(c))
					copy(origContent, c)
				}
				newContent := make([]any, 0, 1+len(frames)+len(origContent))
				newContent = append(newContent, map[string]any{"type": "text", "text": sysTarget.userNote})
				for _, f := range frames {
					newContent = append(newContent, f.ToMap())
				}
				newContent = append(newContent, origContent...)

				clonedUser := make(map[string]any, len(origUser))
				maps.Copy(clonedUser, origUser)
				clonedUser["content"] = newContent
				outMessages[scan.firstUserIndex] = clonedUser

				// When transforming messages[0] directly (non-forced system prompt),
				// keep role "system" with the stub text. When driven by
				// PrepareInlineSystemPrompt, messages[0] is untouched because PiG
				// projects before_agent_start's forced stub after context_with_system.
				if !fromPreparedPrompt && sysMsg != nil {
					outMessages[0] = cloneSystemMessageWithText(sysMsg, sysTarget.replacement)
				}
				changed = true
			}
		}
	}

	if !changed {
		return nil, nil
	}
	return map[string]any{"messages": outMessages}, nil
}
