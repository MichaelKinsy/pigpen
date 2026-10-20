package websearch

import (
	"context"
	"errors"
)

// Port of page-query.ts. Stub: no page is answered yet.

var errAnswerNotPorted = errors.New("not ported yet")

// PageAnswer is the answer of one page and what went into it.
type PageAnswer struct {
	Text               string
	Model              string
	InputChars         int
	OriginalInputChars int
	Truncated          bool
}

// PageAnswerInput is the question, the page and an optional provider/model-id override.
type PageAnswerInput struct {
	Question  string
	PageText  string
	SourceURL string
	Model     string
}

func modelLabel(model map[string]any) string {
	provider, _ := model["provider"].(string)
	id, _ := model["id"].(string)
	return provider + "/" + id
}

func AnswerFromPage(_ context.Context, _ PageAnswerInput, _ SideCallContext) (PageAnswer, error) {
	return PageAnswer{}, errAnswerNotPorted
}
