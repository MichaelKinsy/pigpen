package websearch

import (
	"math"
	"strconv"
	"strings"
)

// Port of defuddle 0.19.3 dist/utils/transcript.js: the transcript markup of the video extractors.

// dfdSegment is a TranscriptSegment; speaker is nil when the original leaves it undefined.
type dfdSegment struct {
	start         float64
	text          string
	speaker       *int
	speakerChange bool
}

// dfdChapter is a chapter heading with its start time.
type dfdChapter struct {
	start float64
	title string
}

func dfdPad2(n float64) string {
	s := strconv.FormatInt(int64(n), 10)
	if len(s) < 2 {
		return "0" + s
	}
	return s
}

// dfdFormatTimestamp is formatTimestamp: M:SS or H:MM:SS.
func dfdFormatTimestamp(seconds float64) string {
	h := math.Floor(seconds / 3600)
	m := math.Floor(math.Mod(seconds, 3600) / 60)
	s := math.Floor(math.Mod(seconds, 60))
	if h > 0 {
		return jsNumberString(h) + ":" + dfdPad2(m) + ":" + dfdPad2(s)
	}
	return jsNumberString(m) + ":" + dfdPad2(s)
}

// dfdBuildTranscript is buildTranscript: the HTML and the text of the segments with the chapter headings placed
// before the segment they precede.
func dfdBuildTranscript(site string, segments []dfdSegment, chapters []dfdChapter) (html, text string) {
	sorted := append([]dfdChapter(nil), chapters...)
	sortStable(len(sorted), func(i, j int) bool { return sorted[i].start < sorted[j].start }, func(i, j int) { sorted[i], sorted[j] = sorted[j], sorted[i] })
	idx := 0
	var htmlParts, textParts []string
	for _, seg := range segments {
		for idx < len(sorted) && sorted[idx].start <= seg.start {
			title := sorted[idx].title
			htmlParts = append(htmlParts, "<h3>"+dfdEscapeHTML(title)+"</h3>")
			if len(textParts) > 0 {
				textParts = append(textParts, "")
			}
			textParts = append(textParts, "### "+title, "")
			idx++
		}
		ts := dfdFormatTimestamp(seg.start)
		speakerClass := ""
		if seg.speaker != nil {
			speakerClass = " speaker-" + strconv.Itoa(*seg.speaker)
		}
		tsHTML := `<strong><span class="timestamp" data-timestamp="` + jsNumberString(seg.start) + `">` + ts + `</span></strong>`
		htmlParts = append(htmlParts, `<p class="transcript-segment`+speakerClass+`">`+tsHTML+" · "+dfdEscapeHTML(seg.text)+"</p>")
		if seg.speakerChange && len(textParts) > 0 {
			textParts = append(textParts, "")
		}
		textParts = append(textParts, "**"+ts+"** · "+seg.text)
	}
	html = `<div class="` + site + ` transcript">` + "\n<h2>Transcript</h2>\n" + strings.Join(htmlParts, "\n") + "\n</div>"
	return html, strings.Join(textParts, "\n")
}
