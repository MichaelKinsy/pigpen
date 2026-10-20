package websearch

import (
	"context"
	"math"
)

// The video branches of extractContent (extract.ts:560-720): frame and timestamp extraction of
// YouTube and local videos, and the Gemini analysis of a local video.

const videoNeedsGemini = "Video analysis requires Gemini access. Either:\n  1. Sign into gemini.google.com in Chrome (free, uses cookies)\n  2. Set GEMINI_API_KEY in "

// frameFailure is the result of a frame request that failed before any frame was read.
func frameFailure(url, title, msg string) ExtractedContent {
	return ExtractedContent{URL: url, Title: title, Content: msg, Error: errp(msg)}
}

// frameLabel is `<start>-<end>` of a timestamp range.
func frameLabel(start, end float64) string {
	return formatSecondsJS(start) + "-" + formatSecondsJS(end)
}

// extractVideoRoute runs the frames, timestamp and local video branches. handled is false when
// the URL is none of them and extraction continues as for a page.
func extractVideoRoute(ctx context.Context, url string, o ExtractOptions) (res ExtractedContent, handled bool) {
	done := func(r ExtractedContent) (ExtractedContent, bool) {
		if ctx.Err() != nil {
			return abortedResult(url), true
		}
		return r, true
	}
	isYT, videoID := IsYouTubeURL(url)
	ytVideo := isYT && videoID != ""
	switch {
	case o.Frames > 0 && o.Timestamp == "":
		if ytVideo {
			return done(youtubeFrames(ctx, url, videoID, o.Frames))
		}
		info, err := IsVideoFile(url)
		if err != nil {
			return failed(url, err.Error()), true
		}
		if info != nil {
			return done(localFrames(ctx, url, info, o.Frames))
		}
		return failed(url, "Frame extraction only works with YouTube and local video files"), true
	case o.Timestamp != "":
		spec, ok := parseTimestampSpec(o.Timestamp)
		if !ok {
			return failed(url, `Invalid timestamp format: "`+o.Timestamp+`". Use "H:MM:SS", "MM:SS", "85", or "start-end".`), true
		}
		if ytVideo {
			return done(youtubeTimestamp(ctx, url, videoID, o, spec))
		}
		info, err := IsVideoFile(url)
		if err != nil {
			return failed(url, err.Error()), true
		}
		if info != nil {
			return done(localTimestamp(ctx, url, info, o, spec))
		}
		return failed(url, "Timestamp extraction only works with YouTube and local video files"), true
	}
	info, err := IsVideoFile(url)
	if err != nil {
		return failed(url, err.Error()), true
	}
	if info == nil {
		return ExtractedContent{}, false
	}
	result, err := ExtractVideo(ctx, *info, o)
	if ctx.Err() != nil {
		return abortedResult(url), true
	}
	if err != nil {
		return failed(url, err.Error()), true
	}
	if result == nil {
		return failed(url, videoNeedsGemini+ConfigPath()), true
	}
	return *result, true
}

func youtubeFrames(ctx context.Context, url, videoID string, count int) ExtractedContent {
	stream, msg := getYouTubeStreamInfo(ctx, videoID)
	if msg != "" {
		return frameFailure(url, "Frames", msg)
	}
	if stream.Duration == nil {
		return frameFailure(url, "Frames", "Cannot determine video duration. Use a timestamp range instead.")
	}
	dur := math.Floor(*stream.Duration)
	timestamps := computeRangeTimestamps(0, dur, count)
	frames, errMsg := extractFrames(timestamps, func(s float64) (Image, string) { return extractFrameFromStream(ctx, stream.StreamURL, s) })
	return buildFrameResult(url, frameLabel(0, dur), len(timestamps), frames, errMsg, stream.Duration)
}

func localFrames(ctx context.Context, url string, info *VideoFileInfo, count int) ExtractedContent {
	duration, msg := getLocalVideoDuration(ctx, info.AbsolutePath)
	if msg != "" {
		return frameFailure(url, "Frames", msg)
	}
	dur := math.Floor(duration)
	timestamps := computeRangeTimestamps(0, dur, count)
	frames, errMsg := extractFrames(timestamps, func(s float64) (Image, string) { return extractVideoFrame(ctx, info.AbsolutePath, s) })
	return buildFrameResult(url, frameLabel(0, dur), len(timestamps), frames, errMsg, &duration)
}

// frameRange is the range of timestamps a request covers and its label: a range as given, a
// single timestamp with frames as that many frames five seconds apart.
func frameRange(spec timestampSpec, count int) (start, end float64, timestamps []float64) {
	if spec.Range {
		n := count
		if n == 0 {
			n = defaultRangeFrames
		}
		return spec.Start, spec.End, computeRangeTimestamps(spec.Start, spec.End, n)
	}
	end = spec.Seconds + float64(count-1)*minFrameInterval
	return spec.Seconds, end, computeRangeTimestamps(spec.Seconds, end, count)
}

func youtubeTimestamp(ctx context.Context, url, videoID string, o ExtractOptions, spec timestampSpec) ExtractedContent {
	stream, msg := getYouTubeStreamInfo(ctx, videoID)
	single := !spec.Range && o.Frames == 0
	if msg != "" {
		switch {
		case spec.Range:
			return frameFailure(url, "Frames "+frameLabel(spec.Start, spec.End), msg)
		case !single:
			start, end, _ := frameRange(spec, o.Frames)
			return frameFailure(url, "Frames "+frameLabel(start, end), msg)
		}
		return frameFailure(url, "Frame at "+o.Timestamp, msg)
	}
	exceeds := func(t float64) string {
		return "Timestamp " + formatSecondsJS(t) + " exceeds video duration (" + formatSecondsJS(math.Floor(*stream.Duration)) + ")"
	}
	if !single {
		start, end, timestamps := frameRange(spec, o.Frames)
		label := frameLabel(start, end)
		if stream.Duration != nil && end > *stream.Duration {
			return frameFailure(url, "Frames "+label, exceeds(end))
		}
		frames, errMsg := extractFrames(timestamps, func(s float64) (Image, string) { return extractFrameFromStream(ctx, stream.StreamURL, s) })
		return buildFrameResult(url, label, len(timestamps), frames, errMsg, stream.Duration)
	}
	title := "Frame at " + o.Timestamp
	if stream.Duration != nil && spec.Seconds > *stream.Duration {
		return frameFailure(url, title, exceeds(spec.Seconds))
	}
	frame, errMsg := extractFrameFromStream(ctx, stream.StreamURL, spec.Seconds)
	return singleFrame(url, o.Timestamp, frame, errMsg)
}

func localTimestamp(ctx context.Context, url string, info *VideoFileInfo, o ExtractOptions, spec timestampSpec) ExtractedContent {
	if spec.Range || o.Frames > 0 {
		start, end, timestamps := frameRange(spec, o.Frames)
		frames, errMsg := extractFrames(timestamps, func(s float64) (Image, string) { return extractVideoFrame(ctx, info.AbsolutePath, s) })
		return buildFrameResult(url, frameLabel(start, end), len(timestamps), frames, errMsg, nil)
	}
	frame, errMsg := extractVideoFrame(ctx, info.AbsolutePath, spec.Seconds)
	return singleFrame(url, o.Timestamp, frame, errMsg)
}

func singleFrame(url, timestamp string, frame Image, errMsg string) ExtractedContent {
	title := "Frame at " + timestamp
	if errMsg != "" {
		return frameFailure(url, title, errMsg)
	}
	return ExtractedContent{URL: url, Title: title, Content: "Video frame at " + timestamp, Thumbnail: &frame}
}

// extractYouTubeRoute is the YouTube branch of extractContent (extract.ts:709): a YouTube URL
// is read by ExtractYouTube while youtube.enabled is not false. handled is false when the URL
// is not one or the section is off.
func extractYouTubeRoute(ctx context.Context, url string, o ExtractOptions) (ExtractedContent, bool) {
	isYT, _ := IsYouTubeURL(url)
	enabled, err := IsYouTubeEnabled()
	if err != nil {
		return failed(url, err.Error()), true
	}
	if !isYT || !enabled {
		return ExtractedContent{}, false
	}
	res, err := ExtractYouTube(ctx, url, o.Prompt, o.Model)
	switch {
	case err != nil && isAbortError(err):
		return abortedResult(url), true
	case err != nil:
		return failed(url, err.Error()), true
	case res != nil:
		return *res, true
	case ctx.Err() != nil:
		return abortedResult(url), true
	}
	return failed(url, youtubeNoAccess), true
}
