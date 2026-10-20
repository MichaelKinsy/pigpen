package websearch

import (
	"context"
	"strings"
	"sync"
)

// answerPages is the presentation of fetch_content mode "answer" (index.ts:2627-2646): every
// successfully fetched page is replaced by the model's answer to the prompt, and a page that
// cannot be answered carries the reason as its error. Pages are answered concurrently. The
// stored result keeps the original fetched content, because the caller stores `fetched`, not
// this presentation.
func answerPages(ctx context.Context, fetched []ExtractedContent, question, answerModel string) []ExtractedContent {
	sc := SideCallContextFrom(ctx)
	presented := make([]ExtractedContent, len(fetched))
	var wg sync.WaitGroup
	for i, page := range fetched {
		presented[i] = page
		if page.Error != nil {
			continue
		}
		if page.Thumbnail != nil || strings.HasPrefix(page.MimeType, "image/") {
			presented[i].Error = new("Page answer requires textual fetched content")
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if sc == nil {
				presented[i].Error = new("Page answer failed: no model registry is available")
				return
			}
			answer, err := AnswerFromPage(ctx, PageAnswerInput{Question: question, PageText: page.Content, SourceURL: page.URL, Model: answerModel}, sc)
			if err != nil {
				presented[i].Error = new("Page answer failed: " + err.Error())
				return
			}
			presented[i].Content = answer.Text
		}()
	}
	wg.Wait()
	return presented
}
