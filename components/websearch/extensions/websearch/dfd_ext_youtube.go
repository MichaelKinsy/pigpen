package websearch

import (
	"strings"
)

// Port of defuddle 0.19.3 dist/extractors/youtube.js, synchronous surface only. With useAsync false Defuddle calls
// extract(), which is buildResult of the transcript already rendered in the page (extractTranscriptFromExistingDom);
// the fetch of caption tracks, chapters and player data, the opening of the transcript panel (extractAsync,
// fetchTranscript, fetchPlayerData, fetchChapters, extractTranscriptFromOpenedDom, parseTranscriptXml and the
// language preference helpers that only they call) are reached only through extractAsync and are not ported.
// Everything extract() reads is: the DOM transcript, the chapters and the caption tracks of the inline page data,
// the VideoObject of the page and the grouping of the segments.

type dfdYoutube struct {
	ctx        dfdExtCtx
	videoID    string
	haveVideo  bool
	inlineJSON map[string]any
}

var (
	reYtSentenceEnd  = jsRE(`[.!?\x{3002}\x{FF01}\x{FF1F}]["'\x{2019}\x{201D})\x{300D}\x{300F}\x{FF09}]*\s*$`, "")
	reYtQuestionEnd  = jsRE(`[?\x{FF1F}]["'\x{2019}\x{201D})\x{300D}\x{300F}\x{FF09}]*\s*$`, "")
	reYtSpeakerMark  = jsRE(`^(>>|-\s)`, "")
	reYtSpeakerStrip = jsRE(`^(>>\s*|-\s+)`, "")
	reYtTrailComma   = jsRE(`,\s*$`, "")
	reYtMidBoundary  = jsRE(`^(.*[.!?]["'\x{2019}\x{201D})]*)\s+([A-Z].*)$|^(.*[\x{3002}\x{FF01}\x{FF1F}][\x{300D}\x{300F}\x{FF09}]*)([\x{3040}-\x{309f}\x{30a0}-\x{30ff}\x{3400}-\x{4dbf}\x{4e00}-\x{9fff}\x{f900}-\x{faff}\x{ac00}-\x{d7af}].*)$`, "")
	reYtAffirmative  = jsRE(`^(mhm|yeah|yes|yep|right|okay|ok|absolutely|sure|exactly|uh-huh|mm-hmm)[.!,]?\s+`, "i")
	reYtLabelParens  = jsRE(`\s*\([^)]*\)\s*`, "")
)

const (
	ytGroupGapSeconds      = 20
	ytMaxGroupSeconds      = 30
	ytTurnMergeMaxWords    = 80
	ytTurnMergeMaxSpan     = 45
	ytShortUtteranceWords  = 3
	ytFirstGroupMergeWords = 8
)

type ytSelectors struct{ segments, timestamp, text, chapters string }

var (
	ytDesktop = ytSelectors{segments: "ytd-transcript-segment-renderer", timestamp: ".segment-timestamp", text: ".segment-text"}
	ytMobile  = ytSelectors{
		segments: "transcript-segment-view-model", timestamp: ".ytwTranscriptSegmentViewModelTimestamp",
		text: "span.yt-core-attributed-string", chapters: "timeline-chapter-view-model h3",
	}
)

// ytTranscript is the transcript the extractor finds: its HTML, its text and the language code (empty when unknown).
type ytTranscript struct{ html, text, languageCode string }

func init() {
	dfdRegisterExtractor("youtube", func(c dfdExtCtx) dfdExtractor {
		return &dfdYoutube{ctx: c, inlineJSON: map[string]any{}}
	})
}

func (e *dfdYoutube) name() string { return "youtube" }

func (e *dfdYoutube) canExtract() bool { return true }

func (e *dfdYoutube) extract() dfdExtracted {
	return e.buildResult(e.extractTranscriptFromExistingDOM())
}

// extractTranscriptFromExistingDOM is the transcript rendered in the page; an exception while reading it (the
// original logs it) means no transcript.
func (e *dfdYoutube) extractTranscriptFromExistingDOM() (t *ytTranscript) {
	if th := ldTry(func() {
		container := e.getTranscriptContainer()
		if container == nil {
			return
		}
		t = e.buildTranscriptFromContainer(container, e.getInlineChapters())
	}); th != nil {
		return nil
	}
	return t
}

func (e *dfdYoutube) getTranscriptContainer() *ldNode {
	// Desktop YouTube.
	if c := ldQuerySelector(e.ctx.doc, `ytd-engagement-panel-section-list-renderer[target-id="engagement-panel-searchable-transcript"] #segments-container`); c != nil {
		return c
	}
	// Mobile YouTube (m.youtube.com).
	return ldQuerySelector(e.ctx.doc, "ytm-macro-markers-list-renderer .ytm-macro-markers-list-container")
}

func (e *dfdYoutube) getTranscriptSelectors(container *ldNode) *ytSelectors {
	if len(ldQuerySelectorAll(container, "ytd-transcript-segment-renderer")) > 0 {
		return &ytDesktop
	}
	if len(ldQuerySelectorAll(container, "transcript-segment-view-model")) > 0 {
		return &ytMobile
	}
	return nil
}

func (e *dfdYoutube) buildTranscriptFromContainer(container *ldNode, chapters []dfdChapter) *ytTranscript {
	if len(container.children()) == 0 {
		return nil
	}
	selectors := e.getTranscriptSelectors(container)
	if selectors == nil {
		return nil
	}
	var segments []dfdSegment
	// Chapters of the DOM, where the format has them inline.
	var domChapters []dfdChapter
	if selectors.chapters != "" {
		for _, ch := range ldQuerySelectorAll(container, selectors.chapters) {
			title := extText(ch)
			if title == "" {
				continue
			}
			// The timestamp is in the next sibling of the panel item.
			var timeStr string
			if panelItem := ldClosest(ch, "macro-markers-panel-item-view-model"); panelItem != nil {
				if next := panelItem.nextElementSibling(); next != nil {
					timeStr = extText(ldQuerySelector(next, selectors.timestamp))
				}
			}
			if seconds, ok := ytParseTimestamp(timeStr); ok {
				domChapters = append(domChapters, dfdChapter{title: title, start: seconds})
			}
		}
	}
	for _, seg := range ldQuerySelectorAll(container, selectors.segments) {
		timestampEl := ldQuerySelector(seg, selectors.timestamp)
		textEl := ldQuerySelector(seg, selectors.text)
		if timestampEl == nil || textEl == nil {
			continue
		}
		timeStr := extText(timestampEl)
		text := extText(textEl)
		if text == "" {
			continue
		}
		if seconds, ok := ytParseTimestamp(timeStr); ok {
			segments = append(segments, dfdSegment{start: seconds, text: text})
		}
	}
	if len(segments) == 0 {
		return nil
	}
	effective := chapters
	if len(chapters) == 0 {
		effective = domChapters
	}
	groups := ytGroupTranscriptSegments(segments)
	html, text := dfdBuildTranscript("youtube", groups, effective)
	return &ytTranscript{html: html, text: text, languageCode: e.getTranscriptLanguageCodeFromDOM()}
}

// ytParseTimestamp is parseTimestamp: "H:MM:SS" or "M:SS" in seconds; ok is false for null.
func ytParseTimestamp(ts string) (float64, bool) {
	var nums []float64
	for _, p := range strings.Split(ts, ":") {
		n := jsToNumberNaN(p)
		if jsIsNaN(n) {
			return 0, false
		}
		nums = append(nums, n)
	}
	switch len(nums) {
	case 3:
		return nums[0]*3600 + nums[1]*60 + nums[2], true
	case 2:
		return nums[0]*60 + nums[1], true
	}
	return 0, false
}

func (e *dfdYoutube) getVideoID() string {
	if !e.haveVideo {
		u, ok := newURL(e.ctx.url, "")
		if !ok {
			ldThrowType("Invalid URL")
		}
		switch {
		case u.hostname() == "youtu.be":
			e.videoID = strings.TrimPrefix(u.pathname(), "/")
		case strings.Contains(u.pathname(), "/shorts/"):
			after := strings.SplitN(u.pathname(), "/shorts/", 3)[1]
			e.videoID = strings.SplitN(after, "/", 2)[0]
		default:
			e.videoID, _ = extQueryParam(u.search(), "v")
		}
		e.haveVideo = true
	}
	return e.videoID
}

// parseInlineJSON is the object literal after the first mention of a global in the page's scripts, by brace
// counting (strings are not special), or nil.
func (e *dfdYoutube) parseInlineJSON(globalName string) any {
	if v, ok := e.inlineJSON[globalName]; ok {
		return v
	}
scripts:
	for _, script := range ldQuerySelectorAll(e.ctx.doc, "script") {
		text := script.textContent()
		if !strings.Contains(text, globalName) {
			continue
		}
		startIndex := strings.IndexByte(text[strings.Index(text, globalName):], '{')
		if startIndex == -1 {
			continue
		}
		startIndex += strings.Index(text, globalName)
		depth := 0
		for i := startIndex; i < len(text); i++ {
			switch text[i] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					parsed, err := jsonParse(text[startIndex : i+1])
					if err != nil {
						continue scripts
					}
					e.inlineJSON[globalName] = parsed
					return parsed
				}
			}
		}
	}
	return nil
}

// getValidatedPlayerResponse is ytInitialPlayerResponse only if its video ID is the URL's (it is stale after SPA
// navigation).
func (e *dfdYoutube) getValidatedPlayerResponse() any {
	videoID := e.getVideoID()
	if videoID == "" {
		return nil
	}
	data := e.parseInlineJSON("ytInitialPlayerResponse")
	if data == nil {
		return nil
	}
	if ytIs(extGet(extGet(data, "videoDetails"), "videoId"), videoID) ||
		ytIs(extGet(extGet(extGet(data, "microformat"), "playerMicroformatRenderer"), "externalVideoId"), videoID) {
		return data
	}
	return nil
}

// ytIs is `v === s`.
func ytIs(v any, s string) bool {
	str, ok := v.(string)
	return ok && str == s
}

func ytCaptionTracks(playerData any) []any {
	tracks, _ := extGet(extGet(extGet(playerData, "captions"), "playerCaptionsTracklistRenderer"), "captionTracks").([]any)
	return tracks
}

func ytTrackDisplayName(track any) string {
	name := extGet(track, "name")
	if v := extGet(name, "simpleText"); jsTruthy(v) {
		return rscString(v)
	}
	if runs := extGet(name, "runs"); jsTruthy(runs) {
		list, ok := runs.([]any)
		if !ok {
			ldThrowType("runs.map is not a function")
		}
		parts := make([]string, len(list))
		for i, run := range list {
			if v := extGet(run, "text"); jsTruthy(v) {
				parts[i] = rscString(v)
			}
		}
		if s := jsTrim(strings.Join(parts, "")); s != "" {
			return s
		}
	}
	return ""
}

func ytNormalizeLanguageLabel(label string) string {
	label = reYtLabelParens.ReplaceAllLiteralString(label, " ")
	return jsToLower(jsTrim(jsCollapseWS(label)))
}

func (e *dfdYoutube) getTranscriptLanguageCodeFromDOM() string {
	langButton := ldQuerySelector(e.ctx.doc, `ytd-engagement-panel-section-list-renderer[target-id="engagement-panel-searchable-transcript"] #footer yt-sort-filter-sub-menu-renderer yt-dropdown-menu button`)
	selectedLabel := extText(langButton)
	tracks := ytCaptionTracks(e.getValidatedPlayerResponse())
	var only any
	if len(tracks) == 1 {
		only = tracks[0]
	}
	code := func(track any) string {
		if v := extGet(track, "languageCode"); jsTruthy(v) {
			return rscString(v)
		}
		return ""
	}
	if selectedLabel == "" {
		return code(only)
	}
	normalized := ytNormalizeLanguageLabel(selectedLabel)
	for _, track := range tracks {
		if ytNormalizeLanguageLabel(ytTrackDisplayName(track)) == normalized {
			if c := code(track); c != "" {
				return c
			}
			break
		}
	}
	return code(only)
}

func (e *dfdYoutube) getInlineChapters() []dfdChapter {
	videoID := e.getVideoID()
	inlineData := e.parseInlineJSON("ytInitialData")
	if inlineData == nil {
		return nil
	}
	// After SPA navigation ytInitialData is stale from the previous page load.
	if videoID != "" {
		current := extGet(extGet(extGet(inlineData, "currentVideoEndpoint"), "watchEndpoint"), "videoId")
		endpoint := extGet(extGet(extGet(inlineData, "endpoint"), "watchEndpoint"), "videoId")
		if !ytIs(current, videoID) && !ytIs(endpoint, videoID) {
			return nil
		}
	}
	if chapters := ytChaptersFromPlayerBar(inlineData); len(chapters) > 0 {
		return chapters
	}
	return ytChaptersFromEngagementPanels(inlineData)
}

func ytChaptersFromPlayerBar(data any) []dfdChapter {
	var chapters []dfdChapter
	panels, ok := extPath(data, "playerOverlays", "playerOverlayRenderer", "decoratedPlayerBarRenderer",
		"decoratedPlayerBarRenderer", "playerBar", "multiMarkersPlayerBarRenderer", "markersMap").([]any)
	if !ok {
		return nil
	}
	for _, panel := range panels {
		markers, ok := extPath(panel, "value", "chapters").([]any)
		if !ok {
			continue
		}
		for _, marker := range markers {
			ch := extGet(marker, "chapterRenderer")
			if !jsTruthy(ch) {
				continue
			}
			title := ""
			if v := extPath(ch, "title", "simpleText"); jsTruthy(v) {
				title = rscString(v)
			}
			if startMs, isNum := extGet(ch, "timeRangeStartMillis").(float64); title != "" && isNum {
				chapters = append(chapters, dfdChapter{title: title, start: startMs / 1000})
			}
		}
	}
	return chapters
}

func ytChaptersFromEngagementPanels(data any) []dfdChapter {
	var chapters []dfdChapter
	panels, ok := extGet(data, "engagementPanels").([]any)
	if !ok {
		return nil
	}
	for _, panel := range panels {
		items, ok := extPath(panel, "engagementPanelSectionListRenderer", "content", "macroMarkersListRenderer", "contents").([]any)
		if !ok {
			continue
		}
		for _, item := range items {
			renderer := extGet(item, "macroMarkersListItemRenderer")
			if !jsTruthy(renderer) {
				continue
			}
			title, timeStr := extPath(renderer, "title", "simpleText"), extPath(renderer, "timeDescription", "simpleText")
			if !jsTruthy(title) || !jsTruthy(timeStr) {
				continue
			}
			// parseTimestamp calls split, so a time that is not a string throws.
			if seconds, ok := ytParseTimestamp(extStrCall(timeStr, "split")); ok {
				chapters = append(chapters, dfdChapter{title: rscString(title), start: seconds})
			}
		}
	}
	return chapters
}

// ---- result -----------------------------------------------------------------------------------------------------

func (e *dfdYoutube) buildResult(transcript *ytTranscript) dfdExtracted {
	videoData := e.getVideoData()
	channelName := e.getChannelName(videoData)
	description := ""
	if v := extGet(videoData, "description"); jsTruthy(v) {
		description = extStrCall(v, "replace")
	}
	formatted := "<p>" + strings.ReplaceAll(dfdEscapeHTML(description), "\n", "<br>") + "</p>"
	contentHTML := `<iframe width="560" height="315" src="https://www.youtube.com/embed/` + e.getVideoID() +
		`" title="YouTube video player" frameborder="0" allow="accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture; web-share" referrerpolicy="strict-origin-when-cross-origin" allowfullscreen></iframe>` + formatted
	if transcript != nil && transcript.html != "" {
		contentHTML += transcript.html
	}
	str := func(v any) string {
		if jsTruthy(v) {
			return rscString(v)
		}
		return ""
	}
	image := ""
	if thumbs, ok := extGet(videoData, "thumbnailUrl").([]any); ok {
		image = str(extIdx(thumbs, 0))
	}
	vars := map[string]string{
		"title":       str(extGet(videoData, "name")),
		"author":      channelName,
		"site":        "YouTube",
		"image":       image,
		"published":   str(extGet(videoData, "uploadDate")),
		"description": jsTrim(jsSlice(description, 0, 200)),
	}
	if transcript != nil && transcript.text != "" {
		vars["transcript"] = transcript.text
	}
	if transcript != nil && transcript.languageCode != "" {
		vars["language"] = transcript.languageCode
	}
	return dfdExtracted{content: contentHTML, contentHTML: contentHTML, variables: vars}
}

// ytIncludes is `id.includes(videoId)` for the string or array an @id, url or embedUrl property holds; any other
// value has no includes method.
func ytIncludes(id any, videoID string) bool {
	switch t := id.(type) {
	case string:
		return strings.Contains(t, videoID)
	case []any:
		for _, v := range t {
			if ytIs(v, videoID) {
				return true
			}
		}
		return false
	}
	ldThrowType("id.includes is not a function")
	return false
}

// getVideoData reads the ld+json VideoObject directly from the DOM, to validate it against the current video ID:
// the schema.org data handed to the constructor can be absent or stale after SPA navigation.
func (e *dfdYoutube) getVideoData() any {
	videoID := e.getVideoID()
	var fallback any
	for _, script := range ldQuerySelectorAll(e.ctx.doc, `script[type="application/ld+json"]`) {
		var videoObject any
		if th := ldTry(func() {
			data, err := jsonParse(script.textContent())
			if err != nil {
				ldThrow("JSON")
			}
			items, ok := data.([]any)
			if !ok {
				items = []any{data}
			}
			for _, item := range items {
				if !ytIs(extGetNN(item, "@type"), "VideoObject") {
					continue
				}
				if videoID == "" {
					videoObject = item
					break
				}
				id := extGet(item, "@id")
				if !jsTruthy(id) {
					id = extGet(item, "url")
				}
				if !jsTruthy(id) {
					id = extGet(item, "embedUrl")
				}
				if !jsTruthy(id) {
					id = ""
				}
				if ytIncludes(id, videoID) {
					videoObject = item
					break
				}
			}
			// A VideoObject with a description is the main video metadata block.
			if videoObject != nil && jsTruthy(extGet(videoObject, "description")) {
				return
			}
			// One with a comment describes the first comment on the page and the video's title, thumbnail and
			// upload date: the fallback if nothing better turns up.
			if videoObject != nil && (jsTruthy(extGet(videoObject, "comment")) || fallback == nil) {
				fallback = videoObject
			}
			videoObject = nil
		}); th != nil {
			continue
		}
		if videoObject != nil {
			return videoObject
		}
	}
	if fallback != nil {
		return fallback
	}
	// The og:* meta tags, which YouTube usually does not update after SPA navigation.
	if videoID != "" {
		ogURL := extAttr(ldQuerySelector(e.ctx.doc, `meta[property="og:url"]`), "content")
		if strings.Contains(ogURL, videoID) {
			meta := func(property string) any {
				return extAttr(ldQuerySelector(e.ctx.doc, `meta[property="`+property+`"]`), "content")
			}
			return &jsObj{
				keys: []string{"name", "description", "thumbnailUrl"},
				m:    map[string]any{"name": meta("og:title"), "description": meta("og:description"), "thumbnailUrl": meta("og:image")},
			}
		}
	}
	return &jsObj{m: map[string]any{}}
}

func (e *dfdYoutube) getChannelName(videoData any) string {
	if v := e.getChannelNameFromDOM(); v != "" {
		return v
	}
	if v := e.getChannelNameFromPlayerResponse(); v != "" {
		return v
	}
	if v := extGet(videoData, "author"); jsTruthy(v) {
		return rscString(v)
	}
	return ""
}

func (e *dfdYoutube) getChannelNameFromDOM() string {
	for _, selector := range []string{`ytd-video-owner-renderer #channel-name a[href^="/@"]`, `#owner-name a[href^="/@"]`} {
		if v := extText(ldQuerySelector(e.ctx.doc, selector)); v != "" {
			return v
		}
	}
	return e.getChannelNameFromMicrodata()
}

func (e *dfdYoutube) getChannelNameFromMicrodata() string {
	root := ldQuerySelector(e.ctx.doc, `[itemprop="author"]`)
	if root == nil {
		return ""
	}
	if c := extAttr(ldQuerySelector(root, `meta[itemprop="name"]`), "content"); c != "" {
		return jsTrim(c)
	}
	if c := extAttr(ldQuerySelector(root, `link[itemprop="name"]`), "content"); c != "" {
		return jsTrim(c)
	}
	return extText(ldQuerySelector(root, `[itemprop="name"], a, span`))
}

func (e *dfdYoutube) getChannelNameFromPlayerResponse() string {
	data := e.getValidatedPlayerResponse()
	if data == nil {
		return ""
	}
	for _, v := range []any{
		extPath(data, "videoDetails", "author"),
		extPath(data, "videoDetails", "ownerChannelName"),
		extPath(data, "microformat", "playerMicroformatRenderer", "ownerChannelName"),
	} {
		if jsTruthy(v) {
			return rscString(v)
		}
	}
	return ""
}

// ---- grouping -----------------------------------------------------------------------------------------------------

type ytTurn struct {
	start         float64
	segments      []dfdSegment
	speakerChange bool
	speaker       *int
}

func ytInt(n int) *int { return &n }

// ytGroupTranscriptSegments groups raw segments into readable blocks: by speaker turn when speaker markers (">>" or
// "-") are present, by sentence otherwise.
func ytGroupTranscriptSegments(segments []dfdSegment) []dfdSegment {
	if len(segments) == 0 {
		return nil
	}
	for _, s := range segments {
		if reYtSpeakerMark.MatchString(s.text) {
			return ytGroupBySpeaker(segments)
		}
	}
	return ytGroupBySentence(segments)
}

// ytGroupBySpeaker groups segments by speaker turn (each marker starts one, tracking an alternating speaker 0/1),
// then by sentence within each turn.
func ytGroupBySpeaker(segments []dfdSegment) []dfdSegment {
	var turns []*ytTurn
	var current *ytTurn
	speakerIndex := -1
	prevSegText := ""
	for _, seg := range segments {
		isSpeakerChange := reYtSpeakerMark.MatchString(seg.text)
		cleanText := reYtSpeakerStrip.ReplaceAllLiteralString(seg.text, "")
		// A marker is a speaker change only if the previous segment ended a sentence: otherwise it is a
		// mid-sentence false positive of auto-captions.
		prevEndsWithComma := reYtTrailComma.MatchString(prevSegText)
		prevEndedSentence := (reYtSentenceEnd.MatchString(prevSegText) || prevSegText == "") && !prevEndsWithComma
		if isSpeakerChange && prevEndedSentence {
			if current != nil {
				turns = append(turns, current)
			}
			speakerIndex = (speakerIndex + 1) % 2
			current = &ytTurn{start: seg.start, segments: []dfdSegment{{start: seg.start, text: cleanText}}, speakerChange: true, speaker: ytInt(speakerIndex)}
		} else {
			if current == nil {
				current = &ytTurn{start: seg.start}
			}
			current.segments = append(current.segments, dfdSegment{start: seg.start, text: cleanText})
		}
		prevSegText = cleanText
	}
	if current != nil {
		turns = append(turns, current)
	}
	turns = ytSplitAffirmativeTurns(turns)

	// Each turn splits into sentence groups; the longer contiguous runs of a turn merge, so an interview answer
	// does not get a timestamp per sentence.
	var groups []dfdSegment
	for _, turn := range turns {
		var sentenceGroups []dfdSegment
		if turn.speaker == nil {
			sentenceGroups = ytGroupBySentence(turn.segments)
		} else {
			sentenceGroups = ytMergeSentenceGroupsWithinTurn(ytGroupBySentence(turn.segments))
		}
		for i, g := range sentenceGroups {
			g.speakerChange = i == 0 && turn.speakerChange
			g.speaker = turn.speaker
			groups = append(groups, g)
		}
	}
	return groups
}

// ytSplitAffirmativeTurns splits turns that start with a short affirmative ("Mhm.", "Yeah.") followed by longer
// content: the affirmative belongs to the current speaker, the rest is likely the other one (missed diarization).
func ytSplitAffirmativeTurns(turns []*ytTurn) []*ytTurn {
	for i := 0; i < len(turns); i++ {
		turn := turns[i]
		if turn.speaker == nil || len(turn.segments) == 0 {
			continue
		}
		first := turn.segments[0]
		loc := reYtAffirmative.FindStringIndex(first.text)
		if loc == nil {
			continue
		}
		matched := first.text[loc[0]:loc[1]]
		// An affirmative that ends with a comma is the speaker continuing.
		if reYtTrailComma.MatchString(matched) {
			continue
		}
		// Only a remainder long enough to be another speaker's response splits.
		remainder := jsTrim(first.text[loc[1]:])
		rest := turn.segments[1:]
		restWords := dfdCountWords(remainder)
		for _, s := range rest {
			restWords += dfdCountWords(s.text)
		}
		if restWords < 30 {
			continue
		}
		// The affirmative stays in the current turn; the rest moves to a new turn with the other speaker.
		newRest := rest
		if remainder != "" {
			newRest = append([]dfdSegment{{start: first.start, text: remainder}}, rest...)
		}
		affirmativeTurn := &ytTurn{
			start:         turn.start,
			segments:      []dfdSegment{{start: first.start, text: strings.TrimRightFunc(matched, jsSpace)}},
			speakerChange: turn.speakerChange,
			speaker:       turn.speaker,
		}
		other := 0
		if *turn.speaker == 0 {
			other = 1
		}
		restTurn := &ytTurn{start: newRest[0].start, segments: newRest, speakerChange: true, speaker: ytInt(other)}
		turns = append(turns[:i], append([]*ytTurn{affirmativeTurn, restTurn}, turns[i+1:]...)...)
		i++ // skip the rest turn just inserted
	}
	return turns
}

func ytMergeSentenceGroupsWithinTurn(groups []dfdSegment) []dfdSegment {
	if len(groups) <= 1 {
		return groups
	}
	var merged []dfdSegment
	current := groups[0]
	currentIsFirstInTurn := true
	for i := 1; i < len(groups); i++ {
		next := groups[i]
		if ytShouldMergeSentenceGroups(current, next, currentIsFirstInTurn) {
			current.text = current.text + " " + next.text
			continue
		}
		merged = append(merged, current)
		current = next
		currentIsFirstInTurn = false
	}
	return append(merged, current)
}

func ytShouldMergeSentenceGroups(current, next dfdSegment, currentIsFirstInTurn bool) bool {
	currentWords := dfdCountWords(current.text)
	nextWords := dfdCountWords(next.text)
	if ytIsShortStandaloneUtterance(current.text, currentWords) || ytIsShortStandaloneUtterance(next.text, nextWords) {
		return false
	}
	if currentIsFirstInTurn && currentWords < ytFirstGroupMergeWords {
		return false
	}
	if reYtQuestionEnd.MatchString(current.text) || reYtQuestionEnd.MatchString(next.text) {
		return false
	}
	if currentWords+nextWords > ytTurnMergeMaxWords {
		return false
	}
	return next.start-current.start <= ytTurnMergeMaxSpan
}

func ytIsShortStandaloneUtterance(text string, words int) bool {
	return words > 0 && words <= ytShortUtteranceWords && reYtSentenceEnd.MatchString(text)
}

// ytGroupBySentence accumulates text until a segment ends with sentence-ending punctuation, or until a very large
// time gap between segments.
func ytGroupBySentence(segments []dfdSegment) []dfdSegment {
	var groups []dfdSegment
	var pending []dfdSegment
	pushGroup := func(segs []dfdSegment) {
		texts := make([]string, len(segs))
		for i, s := range segs {
			texts[i] = s.text
		}
		if text := jsTrim(strings.Join(texts, " ")); text != "" {
			groups = append(groups, dfdSegment{start: segs[0].start, text: text})
		}
	}
	flushAll := func() {
		if len(pending) == 0 {
			return
		}
		pushGroup(pending)
		pending = nil
	}
	flushUpTo := func(idx int) {
		if idx <= 0 {
			return
		}
		pushGroup(pending[:idx])
		pending = append([]dfdSegment(nil), pending[idx:]...)
	}
	for _, seg := range segments {
		// YouTube often emits sparse caption windows 10-15s apart while the sentence continues, so only very
		// large gaps are breaks.
		if len(pending) > 0 && seg.start-pending[len(pending)-1].start > ytGroupGapSeconds {
			flushAll()
		}
		pending = append(pending, seg)
		if reYtSentenceEnd.MatchString(seg.text) {
			flushAll()
			continue
		}
		// An unpunctuated ASR transcript breaks at the best natural point once the group exceeds the maximum.
		if seg.start-pending[0].start >= ytMaxGroupSeconds {
			if breakIdx := ytFindNaturalBreak(&pending); breakIdx > 0 && breakIdx < len(pending) {
				flushUpTo(breakIdx)
			} else {
				flushAll()
			}
		}
	}
	flushAll()
	return groups
}

// ytFindNaturalBreak is the best natural break point in the segments: a mid-text sentence boundary (". A") first,
// which splits that segment in two, else the largest gap in the eligible range. It returns the index to break
// before, or -1.
func ytFindNaturalBreak(segments *[]dfdSegment) int {
	segs := *segments
	if len(segs) <= 1 {
		return -1
	}
	minStart := segs[0].start + ytMaxGroupSeconds/2.0
	for i := len(segs) - 1; i >= 0; i-- {
		if segs[i].start < minStart {
			break
		}
		if m := reYtMidBoundary.FindStringSubmatch(segs[i].text); m != nil {
			before, after := m[1], m[2]
			if before == "" {
				before, after = m[3], m[4]
			}
			start := segs[i].start
			out := append([]dfdSegment(nil), segs[:i]...)
			out = append(out, dfdSegment{start: start, text: before}, dfdSegment{start: start, text: after})
			out = append(out, segs[i+1:]...)
			*segments = out
			return i + 1
		}
	}
	bestIdx := -1
	bestGap := 0.0
	for i := 1; i < len(segs); i++ {
		if segs[i].start < minStart {
			continue
		}
		if gap := segs[i].start - segs[i-1].start; gap >= bestGap {
			bestGap = gap
			bestIdx = i
		}
	}
	return bestIdx
}
