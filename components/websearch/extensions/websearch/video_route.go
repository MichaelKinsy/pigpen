package websearch

import "context"

// Stub of the video branches of extractContent: the twins fail until they are written.

func extractVideoRoute(ctx context.Context, url string, o ExtractOptions) (ExtractedContent, bool) {
	return ExtractedContent{}, false
}

func extractYouTubeRoute(ctx context.Context, url string, o ExtractOptions) (ExtractedContent, bool) {
	return ExtractedContent{}, false
}
