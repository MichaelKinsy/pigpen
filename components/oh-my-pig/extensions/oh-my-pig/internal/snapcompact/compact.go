package snapcompact

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// upstream: packages/snapcompact/src/snapcompact.ts (Archive, HistoryBlocks, planArchive, compact)
// upstream: packages/snapcompact/src/prompts/snapcompact-summary.md

// Frame is one developed snapcompact PNG frame persisted inside Archive.
type Frame struct {
	Data        string `json:"data"`
	MimeType    string `json:"mimeType"`
	Cols        int    `json:"cols"`
	Rows        int    `json:"rows"`
	Chars       int    `json:"chars"`
	Font        string `json:"font,omitempty"`
	Variant     string `json:"variant,omitempty"`
	LineRepeat  int    `json:"lineRepeat,omitempty"`
	Columns     int    `json:"columns,omitempty"`
	StopwordDim bool   `json:"stopwordDim,omitempty"`
	Detail      string `json:"detail,omitempty"`
}

// RenderedFrame is the result of rendering one frame.
type RenderedFrame struct {
	Data  string
	Cols  int
	Rows  int
	Chars int
}

// Archive is persisted inside CompactionEntry.Details["snapcompact"] (and read
// from omp sessions' preserveData["snapcompact"]).
type Archive struct {
	Frames         []Frame `json:"frames"`
	TotalChars     int     `json:"totalChars"`
	TruncatedChars int     `json:"truncatedChars"`
	Text           string  `json:"text,omitempty"`
	TextHead       string  `json:"textHead,omitempty"`
	TextTail       string  `json:"textTail,omitempty"`
}

// ContentBlock is a text or image block sent to the LLM inside a user message.
type ContentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

// ToMap returns the wire-format map expected by PiG's EventContext / ai.UserContentBlock.
func (b ContentBlock) ToMap() map[string]any {
	if b.Type == "image" {
		m := map[string]any{
			"type":     "image",
			"data":     b.Data,
			"mimeType": b.MimeType,
		}
		if b.Detail != "" {
			m["detail"] = b.Detail
		}
		return m
	}
	return map[string]any{
		"type": "text",
		"text": b.Text,
	}
}

// FileOps mirrors PiG's CompactionPreparation.fileOps.
type FileOps struct {
	Read    []string
	Written []string
	Edited  []string
}

// Preparation mirrors the subset of CompactionPreparation needed by Compact.
type Preparation struct {
	FirstKeptEntryID    string
	MessagesToSummarize []map[string]any
	TurnPrefixMessages  []map[string]any
	TokensBefore        int
	PreviousSummary     string
	PreviousArchive     *Archive
	FileOps             FileOps
}

// CompactOptions configures one Compact() invocation.
type CompactOptions struct {
	SerializeOptions
	Model     ModelTarget
	Shape     *Shape
	Variant   string // "auto" or SHAPE_VARIANTS key
	FrameSize int
	MaxFrames int
}

// Result is returned by Compact() and maps onto PiG's CompactionResult.
type Result struct {
	Summary          string
	ShortSummary     string
	FirstKeptEntryID string
	TokensBefore     int
	ReadFiles        []string
	ModifiedFiles    []string
	Archive          Archive
	Shape            Shape
}

// FrameDataBytes returns the sum of base64 lengths across frames.
func FrameDataBytes(frames []Frame) int {
	total := 0
	for _, f := range frames {
		total += len(f.Data)
	}
	return total
}

func formatFrameDataBytes(n int) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1f MB", float64(n)/1_000_000.0)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1f KB", float64(n)/1_000.0)
	}
	return fmt.Sprintf("%d B", n)
}

func formatIntComma(n int) string {
	if n < 0 {
		return "-" + formatIntComma(-n)
	}
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var out strings.Builder
	rem := len(s) % 3
	if rem > 0 {
		out.WriteString(s[:rem])
	}
	for i := rem; i < len(s); i += 3 {
		if out.Len() > 0 {
			out.WriteByte(',')
		}
		out.WriteString(s[i : i+3])
	}
	return out.String()
}

func omittedFrameNotice(omittedFrames, omittedBytes int) string {
	budgetNote := ""
	if omittedBytes > 0 {
		budgetNote = fmt.Sprintf(" %s of base64 exceeded the per-request snapcompact payload budget.", formatFrameDataBytes(omittedBytes))
	}
	plural := "s"
	if omittedFrames == 1 {
		plural = ""
	}
	return strings.Join([]string{
		"-------------- snapcompact image middle omitted",
		fmt.Sprintf("%s archived image frame%s could not be included.%s The compacted summary and visible text edges remain available.", formatIntComma(omittedFrames), plural, budgetNote),
		"--------------",
	}, "\n")
}

const blobReferencePrefix = "blob:sha256:"

// HistoryBlocks reconstructs the ordered prompt blocks for a snapcompact archive:
// textHead -> image middle (capped newest-first by maxFrameDataBytes) -> textTail.
// upstream: packages/snapcompact/src/snapcompact.ts historyBlocks
func HistoryBlocks(archive Archive, maxFrameDataBytes int) []ContentBlock {
	type slot struct {
		frame        *Frame
		unavailable  bool
		omittedBytes int
	}

	slots := make([]slot, len(archive.Frames))
	usedBytes := 0
	for i := len(archive.Frames) - 1; i >= 0; i-- {
		f := archive.Frames[i]
		if f.Data == "" || strings.HasPrefix(f.Data, blobReferencePrefix) {
			slots[i] = slot{unavailable: true}
			continue
		}
		if maxFrameDataBytes > 0 && usedBytes+len(f.Data) > maxFrameDataBytes {
			slots[i] = slot{omittedBytes: len(f.Data)}
			continue
		}
		usedBytes += len(f.Data)
		frameCopy := f
		slots[i] = slot{frame: &frameCopy}
	}

	var middle []ContentBlock
	pendingGap := ""
	pendingFrames := 0
	pendingBytes := 0
	flushGap := func() {
		if pendingFrames == 0 {
			return
		}
		if pendingGap == "unavailable" {
			plural := "s"
			if pendingFrames == 1 {
				plural = ""
			}
			middle = append(middle, ContentBlock{
				Type: "text",
				Text: fmt.Sprintf("-------------- %s archived image frame%s unavailable here --------------", formatIntComma(pendingFrames), plural),
			})
		} else {
			middle = append(middle, ContentBlock{
				Type: "text",
				Text: omittedFrameNotice(pendingFrames, pendingBytes),
			})
		}
		pendingGap = ""
		pendingFrames = 0
		pendingBytes = 0
	}
	for _, s := range slots {
		if s.frame != nil {
			flushGap()
			mime := s.frame.MimeType
			if mime == "" {
				mime = "image/png"
			}
			middle = append(middle, ContentBlock{
				Type:     "image",
				Data:     s.frame.Data,
				MimeType: mime,
				Detail:   s.frame.Detail,
			})
			continue
		}
		gap := "budget"
		if s.unavailable {
			gap = "unavailable"
		}
		if pendingGap != "" && pendingGap != gap {
			flushGap()
		}
		pendingGap = gap
		pendingFrames++
		pendingBytes += s.omittedBytes
	}
	flushGap()

	hasImages := false
	hasOmitted := false
	for _, b := range middle {
		if b.Type == "image" {
			hasImages = true
		} else if b.Type == "text" {
			hasOmitted = true
		}
	}

	var blocks []ContentBlock
	if archive.TextHead != "" {
		suffix := ""
		if hasImages {
			suffix = "\n-------------- imaged middle below\n"
		}
		blocks = append(blocks, ContentBlock{
			Type: "text",
			Text: ElideDataUrls(ToPlainText(archive.TextHead), true) + suffix,
		})
	}
	blocks = append(blocks, middle...)
	if archive.TextTail != "" {
		prefix := ""
		if hasImages {
			prefix = "-------------- imaged middle above\n"
		} else if archive.TruncatedChars > 0 || hasOmitted {
			prefix = "\n-------------- middle history omitted above\n"
		}
		tail := prefix + ElideDataUrls(ToPlainText(archive.TextTail), true)
		if len(blocks) > 0 && blocks[len(blocks)-1].Type == "text" {
			blocks[len(blocks)-1].Text += tail
		} else {
			blocks = append(blocks, ContentBlock{Type: "text", Text: tail})
		}
	}
	return blocks
}

// ArchiveSourceText extracts the full kept plain-text transcript from archive.
func ArchiveSourceText(archive Archive) string {
	raw := archive.Text
	if raw == "" {
		var parts []string
		if archive.TextHead != "" {
			parts = append(parts, archive.TextHead)
		}
		if archive.TextTail != "" {
			parts = append(parts, archive.TextTail)
		}
		raw = strings.Join(parts, NewlineGlyph)
	}
	if raw == "" {
		return ""
	}
	return ElideDataUrls(ToPlainText(raw), true)
}

// ParseArchive validates and decodes a persisted archive from a JSON map/value.
func ParseArchive(raw any) (*Archive, bool) {
	if raw == nil {
		return nil, false
	}
	if a, ok := raw.(Archive); ok {
		return validateArchive(&a)
	}
	if a, ok := raw.(*Archive); ok && a != nil {
		return validateArchive(a)
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, false
	}
	var a Archive
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, false
	}
	return validateArchive(&a)
}

func validateArchive(a *Archive) (*Archive, bool) {
	if a == nil {
		return nil, false
	}
	validFrames := make([]Frame, 0, len(a.Frames))
	for _, f := range a.Frames {
		if f.Data != "" && f.MimeType != "" && f.Cols > 0 && f.Rows > 0 {
			validFrames = append(validFrames, f)
		}
	}
	if len(validFrames) == 0 && a.Text == "" && a.TextHead == "" && a.TextTail == "" {
		return nil, false
	}
	out := *a
	out.Frames = validFrames
	return &out, true
}

// ExtractArchiveFromDetails extracts a snapcompact Archive from CompactionEntry.Details
// or PreserveData (checks details["snapcompact"] and details["preserveData"]["snapcompact"]).
func ExtractArchiveFromDetails(details any) (*Archive, bool) {
	m, ok := details.(map[string]any)
	if !ok || m == nil {
		return nil, false
	}
	if slot, ok := m[PreserveKey]; ok {
		if a, valid := ParseArchive(slot); valid {
			return a, true
		}
	}
	if pd, ok := m["preserveData"].(map[string]any); ok {
		if slot, ok := pd[PreserveKey]; ok {
			if a, valid := ParseArchive(slot); valid {
				return a, true
			}
		}
	}
	return nil, false
}

// SummaryIncludesThinking reports whether a summary declares `¶think:` scopes.
func SummaryIncludesThinking(summary string) bool {
	return strings.Contains(summary, thinkLabel)
}

// ArchiveIncludesThinking reports whether summary or archive carries `¶think:` reasoning.
func ArchiveIncludesThinking(summary string, archive Archive) bool {
	if SummaryIncludesThinking(summary) {
		return true
	}
	check := func(s string) bool {
		return strings.Contains(s, thinkLabel) && StripThinkingSections(s) != s
	}
	if archive.Text != "" {
		return check(archive.Text)
	}
	return check(archive.TextHead) || check(archive.TextTail)
}

type planFrame struct {
	text  string
	shape Shape
}

type archiveLayout struct {
	frames         []planFrame
	textHead       string
	textTail       string
	keptText       string
	truncatedChars int
}

// planArchive lays out normalized archive text (oldest->newest) with verbatim
// text edges and an imaged middle, foveating (HQ/LQ/HQ) when exceeding maxFrames.
// upstream: packages/snapcompact/src/snapcompact.ts planArchive
func planArchive(text string, high, low Shape, maxFrames int) archiveLayout {
	runes := []rune(text)
	capHi := ComputeGeometry(high, high.FrameSize).Capacity
	edgeCap := textEdgePages * capHi
	if len(runes) <= 2*edgeCap {
		return archiveLayout{textHead: text, keptText: text}
	}
	if maxFrames < 1 {
		head := string(runes[:edgeCap])
		tail := string(runes[len(runes)-edgeCap:])
		return archiveLayout{
			textHead:       head,
			textTail:       tail,
			keptText:       head + tail,
			truncatedChars: len(runes) - len([]rune(head)) - len([]rune(tail)),
		}
	}
	head := string(runes[:edgeCap])
	tail := string(runes[len(runes)-edgeCap:])
	imageText := string(runes[edgeCap : len(runes)-edgeCap])
	if imageText == "" {
		return archiveLayout{textHead: text, keptText: text}
	}

	makePlan := func(pages []string, sh Shape) []planFrame {
		out := make([]planFrame, len(pages))
		for i, p := range pages {
			out[i] = planFrame{text: p, shape: sh}
		}
		return out
	}

	if high.Columns == 2 {
		pages := docPages(imageText, ComputeGeometry(high, high.FrameSize), usesWideCells(high))
		kept := pages
		truncated := 0
		if len(pages) > maxFrames {
			if maxFrames == 1 {
				for _, p := range pages[:len(pages)-1] {
					truncated += len([]rune(p))
				}
				kept = pages[len(pages)-1:]
			} else {
				dropEnd := len(pages) - (maxFrames - 1)
				for _, p := range pages[1:dropEnd] {
					truncated += len([]rune(p))
				}
				kept = append([]string{pages[0]}, pages[dropEnd:]...)
			}
		}
		flatParts := make([]string, len(kept))
		for i, p := range kept {
			flatParts[i] = strings.ReplaceAll(p, "\n", " ")
		}
		flat := strings.Join(flatParts, " ")
		return archiveLayout{
			frames:         makePlan(kept, high),
			textHead:       head,
			textTail:       tail,
			keptText:       head + flat + tail,
			truncatedChars: truncated,
		}
	}

	geoHi := ComputeGeometry(high, high.FrameSize)
	hiPages := paginateCells(imageText, capHi, geoHi.Cols, usesWideCells(high))
	if len(hiPages) <= maxFrames {
		return archiveLayout{
			frames:         makePlan(hiPages, high),
			textHead:       head,
			textTail:       tail,
			keptText:       head + imageText + tail,
			truncatedChars: 0,
		}
	}

	// Foveate: HQ head + LQ dense middle + HQ tail, dropping oldest dense slice.
	geoLo := ComputeGeometry(low, low.FrameSize)
	imageEdgeFrames := HQEdgeFrames
	if half := (maxFrames - 1) / 2; half < imageEdgeFrames {
		imageEdgeFrames = half
	}
	if imageEdgeFrames < 0 {
		imageEdgeFrames = 0
	}
	headPages := hiPages[:imageEdgeFrames]
	var tailPages []string
	if imageEdgeFrames > 0 {
		tailPages = hiPages[len(hiPages)-imageEdgeFrames:]
	}
	imageHead := strings.Join(headPages, "")
	imageTail := strings.Join(tailPages, "")
	middleSource := imageText[len(imageHead) : len(imageText)-len(imageTail)]
	middlePages := paginateCells(middleSource, geoLo.Capacity, geoLo.Cols, usesWideCells(low))
	middleBudget := maxFrames - 2*imageEdgeFrames
	if middleBudget < 0 {
		middleBudget = 0
	}
	truncated := 0
	middleText := middleSource
	if len(middlePages) > middleBudget {
		dropCount := len(middlePages) - middleBudget
		dropped := strings.Join(middlePages[:dropCount], "")
		truncated = len([]rune(dropped))
		middleText = middleSource[len(dropped):]
		middlePages = middlePages[dropCount:]
	}

	allFrames := make([]planFrame, 0, len(headPages)+len(middlePages)+len(tailPages))
	allFrames = append(allFrames, makePlan(headPages, high)...)
	allFrames = append(allFrames, makePlan(middlePages, low)...)
	allFrames = append(allFrames, makePlan(tailPages, high)...)
	return archiveLayout{
		frames:         allFrames,
		textHead:       head,
		textTail:       tail,
		keptText:       head + imageHead + middleText + imageTail + tail,
		truncatedChars: truncated,
	}
}

type summaryParams struct {
	FrameCount              int
	DocColumns              bool
	Grids                   []gridDim
	SentenceInk             bool
	StopwordDimmed          bool
	LineRepeated            bool
	TruncatedChars          int
	IncludedPreviousSummary bool
	Files                   string
	IncludeThinking         bool
}

type gridDim struct {
	Cols int
	Rows int
}

// renderSnapcompactSummary mirrors packages/snapcompact/src/prompts/snapcompact-summary.md verbatim.
func renderSnapcompactSummary(p summaryParams) string {
	var b strings.Builder
	b.WriteString("Resume prior conversation. Earlier turns archived under HISTORY below, oldest→newest. Read HISTORY fully; continue the live conversation following it.\n\n")
	b.WriteString("Archived transcript scopes:\n")
	if p.IncludeThinking {
		b.WriteString("- `¶user:`, `¶think:`, `¶ai:`, `¶call:`: user, assistant reasoning, assistant reply, tool call.\n")
	} else {
		b.WriteString("- `¶user:`, `¶ai:`, `¶call:`: user, assistant reply, tool call.\n")
	}
	b.WriteString("- Unprefixed following lines: current scope. Consecutive same-kind blocks omit repeated prefix.\n")
	b.WriteString("- Tool call: `¶call:name(args)//intent`; trailing `//intent` optional. `<out>…</out>`: tool output.\n\n")
	b.WriteString("Reading HISTORY:\n")
	b.WriteString("- Plain text: verbatim transcript; rely on it exactly.\n")
	if p.FrameCount > 0 {
		b.WriteString("- Some middle sections: images, not text. Each image: one page of that transcript, in reading order between marked delimiters. Solid black cell: newline; runs of spaces collapse to one.\n")
		var gridParts []string
		for _, g := range p.Grids {
			gridParts = append(gridParts, fmt.Sprintf("%d characters wide, up to %d rows tall", g.Cols, g.Rows))
		}
		gridDesc := strings.Join(gridParts, ", or ")
		if p.DocColumns {
			b.WriteString("  - Frame: two side-by-side columns, each " + gridDesc + "; read left top→bottom, then right.\n")
		} else {
			b.WriteString("  - Frame: one grid " + gridDesc + "; read left→right, top→bottom. No word wrap; words may break across rows.\n")
		}
		if p.SentenceInk {
			b.WriteString("  - Ink: six colors, one per sentence.\n")
		}
		if p.StopwordDimmed {
			b.WriteString("  - Function words: dim gray; content words: full ink.\n")
		}
		if p.LineRepeated {
			b.WriteString("  - Each line printed twice (white, then pale-yellow band); copies identical.\n")
		}
	}
	if p.IncludedPreviousSummary {
		b.WriteString("- HISTORY opens with a condensed digest of still-older context predating archived turns.\n")
	}
	if p.TruncatedChars > 0 {
		b.WriteString(fmt.Sprintf("- About %d characters of older middle history dropped to fit archive budget.\n", p.TruncatedChars))
	}
	b.WriteString("- If an exact earlier detail matters and a section is unclear, re-derive from workspace (re-read files, re-run commands), rather than guess.\n\n")
	if p.Files != "" {
		b.WriteString(filesSectionHead)
		b.WriteString(p.Files)
		b.WriteByte('\n')
	}
	b.WriteString(historySectionHead)
	return b.String()
}

func computeFileLists(ops FileOps) (readFiles, modifiedFiles []string, readSet map[string]bool) {
	readSet = make(map[string]bool, len(ops.Read))
	for _, p := range ops.Read {
		if !reURLScheme.MatchString(p) {
			readSet[p] = true
		}
	}
	modSet := make(map[string]bool, len(ops.Written)+len(ops.Edited))
	for _, p := range ops.Written {
		if !reURLScheme.MatchString(p) {
			modSet[p] = true
		}
	}
	for _, p := range ops.Edited {
		if !reURLScheme.MatchString(p) {
			modSet[p] = true
		}
	}
	for p := range readSet {
		if !modSet[p] {
			readFiles = append(readFiles, p)
		}
	}
	for p := range modSet {
		modifiedFiles = append(modifiedFiles, p)
	}
	sort.Strings(readFiles)
	sort.Strings(modifiedFiles)
	return readFiles, modifiedFiles, readSet
}

// Compact runs a fully local snapcompact pass over prep, rendering frames in parallel.
// upstream: packages/snapcompact/src/snapcompact.ts compact
func Compact(prep Preparation, opts CompactOptions) (Result, error) {
	if prep.FirstKeptEntryID == "" {
		return Result{}, errors.New("snapcompact: firstKeptEntryId is empty")
	}

	includeThink := opts.includeThinking()
	if opts.IncludeThinking == nil {
		includeThink = ReaderIncludesThinking(opts.Model)
	}
	serOpts := opts.SerializeOptions
	serOpts.IncludeThinking = new(includeThink)

	allMsgs := make([]map[string]any, 0, len(prep.MessagesToSummarize)+len(prep.TurnPrefixMessages))
	allMsgs = append(allMsgs, prep.MessagesToSummarize...)
	allMsgs = append(allMsgs, prep.TurnPrefixMessages...)
	serialized := SerializeConversation(allMsgs, serOpts)

	prevArchive := prep.PreviousArchive
	prevTextRaw := ""
	if prevArchive != nil {
		if prevArchive.Text != "" {
			prevTextRaw = prevArchive.Text
		} else {
			var parts []string
			if prevArchive.TextHead != "" {
				parts = append(parts, prevArchive.TextHead)
			}
			if prevArchive.TextTail != "" {
				parts = append(parts, prevArchive.TextTail)
			}
			prevTextRaw = strings.Join(parts, NewlineGlyph)
		}
	}
	prevTextHealed := ElideDataUrls(prevTextRaw, true)
	prevText := prevTextHealed
	if !includeThink && prevText != "" {
		prevText = TextWithoutThinking(prevText)
	}
	hasPrevText := prevText != ""
	includedPrevSummary := !hasPrevText && strings.TrimSpace(prep.PreviousSummary) != ""

	var baseShape Shape
	if opts.Shape != nil {
		baseShape = *opts.Shape
	} else {
		baseShape = ResolveShape(opts.Model, opts.Variant)
	}
	if opts.FrameSize > 0 {
		baseShape.FrameSize = opts.FrameSize
	}
	high := baseShape
	low := DenseCompanion(high)

	maxFrames := opts.MaxFrames
	if maxFrames <= 0 || maxFrames > MaxFramesDefault {
		maxFrames = MaxFramesDefault
	}

	// Renderability preflight (<=5% '?' fallback ceiling).
	probeText := serialized
	if prevText != "" {
		probeText = prevText + NewlineGlyph + serialized
	} else if includedPrevSummary {
		probeText = prep.PreviousSummary + NewlineGlyph + serialized
	}
	scan := ScanRenderability(probeText, high.Font)
	if !scan.IsSafe {
		return Result{}, fmt.Errorf("snapcompact: unsupported characters for font %q (%.1f%%)", high.Font, scan.UnrenderableRatio*100)
	}

	archiveText := Normalize(serialized, high.Font)
	if includedPrevSummary {
		lead := prep.PreviousSummary
		if !includeThink {
			lead = TextWithoutThinking(lead)
		}
		head := previousSummaryHead + " " + Normalize(lead, high.Font)
		if archiveText != "" {
			archiveText = head + " [Recent conversation] " + archiveText
		} else {
			archiveText = head
		}
	}

	droppedChars := 0
	if prevArchive != nil {
		droppedChars += prevArchive.TruncatedChars
		if prevArchive.Text == "" {
			for _, f := range prevArchive.Frames {
				droppedChars += f.Chars
			}
		}
	}
	if hasPrevText {
		if archiveText != "" {
			archiveText = prevText + NewlineGlyph + archiveText
		} else {
			archiveText = prevText
		}
	}
	archiveText = ElideDataUrls(archiveText, false)

	layout := planArchive(archiveText, high, low, maxFrames)

	// Prepare page texts sequentially so dim state carries across frame boundaries.
	dimOpen := strings.LastIndex(layout.textHead, DimOn) > strings.LastIndex(layout.textHead, DimOff)
	pageTexts := make([]string, len(layout.frames))
	for i, pf := range layout.frames {
		pt := pf.text
		if dimOpen {
			pt = DimOn + pt
		}
		dimOpen = strings.LastIndex(pt, DimOn) > strings.LastIndex(pt, DimOff)
		if pf.shape.StopwordDim {
			pt = DimStopwords(pt)
		}
		pageTexts[i] = pt
	}

	// Render PNG frames concurrently across available CPUs.
	frames := make([]Frame, len(layout.frames))
	if len(layout.frames) > 0 {
		workers := runtime.GOMAXPROCS(0)
		if workers > len(layout.frames) {
			workers = len(layout.frames)
		}
		if workers < 1 {
			workers = 1
		}
		type job struct{ idx int }
		jobs := make(chan job, len(layout.frames))
		for i := range layout.frames {
			jobs <- job{idx: i}
		}
		close(jobs)

		var (
			wg       sync.WaitGroup
			errOnce  sync.Once
			firstErr error
		)
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := range jobs {
					pf := layout.frames[j.idx]
					rf, err := RenderFrame(pageTexts[j.idx], pf.shape, pf.shape.FrameSize)
					if err != nil {
						errOnce.Do(func() { firstErr = err })
						return
					}
					f := Frame{
						Data:       rf.Data,
						MimeType:   "image/png",
						Cols:       rf.Cols,
						Rows:       rf.Rows,
						Chars:      rf.Chars,
						Font:       pf.shape.Font,
						Variant:    pf.shape.Variant,
						LineRepeat: pf.shape.LineRepeat,
						Detail:     pf.shape.ImageDetail,
					}
					if pf.shape.Columns == 2 {
						f.Columns = 2
					}
					if pf.shape.StopwordDim {
						f.StopwordDim = true
					}
					frames[j.idx] = f
				}
			}()
		}
		wg.Wait()
		if firstErr != nil {
			return Result{}, firstErr
		}
	}

	truncatedChars := droppedChars + layout.truncatedChars
	textHead := layout.textHead
	textTail := layout.textTail
	if textTail != "" && dimOpen {
		textTail = DimOn + textTail
	}
	textChars := len([]rune(textHead)) + len([]rune(textTail))
	totalChars := textChars
	var grids []gridDim
	for _, f := range frames {
		totalChars += f.Chars
		seen := false
		for _, g := range grids {
			if g.Cols == f.Cols && g.Rows == f.Rows {
				seen = true
				break
			}
		}
		if !seen {
			grids = append(grids, gridDim{Cols: f.Cols, Rows: f.Rows})
		}
	}

	readFiles, modifiedFiles, readSet := computeFileLists(prep.FileOps)
	filesText := FormatFileList(readFiles, modifiedFiles, readSet)

	var summary string
	if len(frames) == 0 && textHead == "" && textTail == "" && filesText == "" {
		summary = "No prior history."
	} else {
		summary = renderSnapcompactSummary(summaryParams{
			FrameCount:              len(frames),
			DocColumns:              high.Columns == 2,
			Grids:                   grids,
			SentenceInk:             high.Variant == "sent",
			StopwordDimmed:          high.StopwordDim,
			LineRepeated:            high.LineRepeat > 1,
			TruncatedChars:          truncatedChars,
			IncludedPreviousSummary: includedPrevSummary,
			Files:                   filesText,
			IncludeThinking:         includeThink,
		})
	}

	persistedText := layout.keptText
	if persistedText != "" && layout.textTail != "" && strings.HasSuffix(persistedText, layout.textTail) {
		persistedText = persistedText[:len(persistedText)-len(layout.textTail)] + textTail
	}

	archive := Archive{
		Frames:         frames,
		TotalChars:     totalChars,
		TruncatedChars: truncatedChars,
		Text:           persistedText,
		TextHead:       textHead,
		TextTail:       textTail,
	}

	plural := "s"
	if len(frames) == 1 {
		plural = ""
	}
	textNote := ""
	if textChars > 0 {
		textNote = fmt.Sprintf(" (+%s chars as text)", formatIntComma(textChars))
	}
	shortSummary := fmt.Sprintf("Archived %s chars of history onto %d snapcompact frame%s%s", formatIntComma(totalChars), len(frames), plural, textNote)

	return Result{
		Summary:          summary,
		ShortSummary:     shortSummary,
		FirstKeptEntryID: prep.FirstKeptEntryID,
		TokensBefore:     prep.TokensBefore,
		ReadFiles:        readFiles,
		ModifiedFiles:    modifiedFiles,
		Archive:          archive,
		Shape:            high,
	}, nil
}

// ComputeMaxFrames computes the frame budget for snapcompact given model window,
// reserve tokens, kept recent tokens, and provider limits.
// upstream: packages/coding-agent/src/session/session-maintenance.ts #computeSnapcompactMaxFrames
func ComputeMaxFrames(target ModelTarget, shape Shape, reserveTokens, recentTokens int) int {
	provCap := ProviderFrameBudget(target)
	dataCap := MaxFramesForDataBudget(shape, FrameDataBytesBudget(target))
	capFrames := min(MaxFramesDefault, min(provCap, dataCap))
	if capFrames < 1 {
		capFrames = 1
	}
	if target.ContextWindow <= 0 {
		return capFrames
	}
	if reserveTokens <= 0 {
		reserveTokens = max(int(math.Floor(float64(target.ContextWindow)*0.15)), 16384)
	}
	threshold := target.ContextWindow - reserveTokens
	if threshold <= 0 {
		return 1
	}
	edgeCap := ComputeGeometry(shape, shape.FrameSize).Capacity
	textEdgeTokens := int(math.Ceil(float64(2*edgeCap) * 1.15 / 4.0))
	const summaryTemplateTokens = 2000
	fixedTokens := recentTokens + textEdgeTokens + summaryTemplateTokens
	// Target 60% of threshold post-compaction, 50% of remaining room.
	roomBudget := min(int(0.5*float64(threshold-fixedTokens)), int(0.6*float64(threshold))-fixedTokens)
	frameCost := PlannedFrameTokens(shape)
	if frameCost <= 0 {
		frameCost = FrameTokenEstimate
	}
	if roomBudget < frameCost {
		return 1
	}
	tokenCap := roomBudget / frameCost
	if tokenCap < 1 {
		tokenCap = 1
	}
	return min(capFrames, tokenCap)
}

// ViewWithoutFrames returns a text-only fallback of a snapcompact summary + archive
// for non-vision readers (e.g. after switching from a vision model to a text-only model).
// upstream: packages/snapcompact/src/snapcompact.ts viewWithoutFrames
func ViewWithoutFrames(summary string, archive Archive, includeThinking bool) (string, string) {
	src := ArchiveSourceText(archive)
	if !includeThinking {
		src = TextWithoutThinking(src)
	}
	files := ""
	if start := strings.Index(summary, filesSectionHead); start >= 0 {
		if end := strings.LastIndex(summary, historySectionHead); end > start {
			files = strings.TrimRight(summary[start+len(filesSectionHead):end], "\n")
		}
	}
	reRendered := renderSnapcompactSummary(summaryParams{
		FrameCount:              0,
		TruncatedChars:          archive.TruncatedChars,
		IncludedPreviousSummary: strings.HasPrefix(src, previousSummaryHead),
		Files:                   files,
		IncludeThinking:         SummaryIncludesThinking(summary) && includeThinking,
	})
	if !strings.HasSuffix(strings.TrimRight(summary, " \t\r\n"), historySectionHead) {
		own := summary
		if !includeThinking {
			own = TextWithoutThinking(own)
		}
		own = strings.TrimSpace(own)
		if own != "" {
			reRendered = own + "\n\n" + reRendered
		}
	}
	return reRendered, src
}
