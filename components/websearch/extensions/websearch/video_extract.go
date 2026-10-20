package websearch

import (
	"context"
	"sync"
	"time"
)

// Stub of video-extract.ts: the twins fail until it is written.

const defaultVideoPrompt = ""

type VideoFileInfo struct {
	AbsolutePath      string
	MimeType          string
	SizeBytes         int64
	MaxSizeBytes      float64
	WithinUploadLimit bool
}

func IsVideoFile(input string) (*VideoFileInfo, error) { return nil, nil }

func ExtractVideo(ctx context.Context, info VideoFileInfo, o ExtractOptions) (*ExtractedContent, error) {
	return nil, nil
}

func extractVideoFrame(ctx context.Context, filePath string, seconds float64) (Image, string) {
	return Image{}, "not implemented"
}

func mapFfprobeError(err error) string { return "" }

func getLocalVideoDuration(ctx context.Context, filePath string) (float64, string) {
	return 0, "not implemented"
}

func shouldRethrow(err error) bool { return false }

var geminiFilePollInterval = 5 * time.Second

var pendingGeminiDeletes sync.WaitGroup
