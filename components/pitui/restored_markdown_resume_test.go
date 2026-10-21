package tui

import (
	"slices"
	"testing"
)

// A long answer streamed 24 bytes at a time renders every sampled frame as a new component does, and nearly every frame
// resumes from a point.
func TestResumedMarkdownLongAnswerEqualsAFreshRender(t *testing.T) {
	mdCaseSetup(t)
	answer := longAnswer(30000)
	streamed := testMDWithOptions("", 0, 0, nil, nil, nil)
	frames, resumed := 0, 0
	for end := 24; ; end += 24 {
		end = min(end, len(answer))
		text := answer[:end]
		if streamed.resume != nil && streamed.resume.pick(text) != nil {
			resumed++
		}
		streamed.SetText(text)
		got := streamed.Render(100)
		frames++
		if frames%9 == 0 || end == len(answer) {
			if want := testNewMarkdown(text).Render(100); !slices.Equal(got, want) {
				t.Fatalf("%d bytes:\n got %q\nwant %q", end, got, want)
			}
		}
		if end == len(answer) {
			break
		}
	}
	if resumed < frames*9/10 {
		t.Fatalf("%d of %d frames resumed", resumed, frames)
	}
}
