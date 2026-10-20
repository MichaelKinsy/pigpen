package websearch

import "context"

// answerPages is the presentation of fetch_content mode "answer". Stub: pages are not answered yet.
func answerPages(_ context.Context, fetched []ExtractedContent, _, _ string) []ExtractedContent {
	return fetched
}
