package websearch

import (
	"context"
	"encoding/base64"
	"io"
	"regexp"
	"strings"
	"time"
)

// Port of youtube-extract.ts: a YouTube URL is read by Gemini Web, then the Gemini API, then
// Perplexity; yt-dlp and ffmpeg give frames.

const youtubePrompt = `Extract the complete content of this YouTube video. Include:
1. Video title, channel name, and duration
2. A brief summary (2-3 sentences)
3. Full transcript with timestamps
4. Descriptions of any code, terminal commands, diagrams, slides, or UI shown on screen

Format as markdown.`

var youtubeRE = regexp.MustCompile(`(?:(?:www\.|m\.)?youtube\.com/(?:watch\?.*v=|shorts/|live/|embed/|v/)|youtu\.be/)([a-zA-Z0-9_-]{11})`)

const (
	ytInfoTimeout     = 15 * time.Second // youtube-extract.ts:161
	ytFrameTimeout    = 30 * time.Second // youtube-extract.ts:180
	ytThumbnailBudget = 5 * time.Second  // youtube-extract.ts:216
	ytGeminiTimeout   = 120 * time.Second
	youtubeNoAccess   = "Could not extract YouTube video content. Sign into Google in a supported Chromium browser for automatic access, or set GEMINI_API_KEY."
)

// IsYouTubeURL is isYouTubeURL (youtube-extract.ts:79): a watch, shorts, live, embed or youtu.be
// URL (a playlist is not one) and its video id.
func IsYouTubeURL(raw string) (bool, string) {
	if u, err := ParseURL(raw, nil); err == nil && u.Path == "/playlist" {
		return false, ""
	}
	m := youtubeRE.FindStringSubmatch(raw)
	if m == nil {
		return false, ""
	}
	return true, m[1]
}

type youtubeConfig struct {
	Enabled        bool
	PreferredModel string
}

// loadYouTubeConfig is loadYouTubeConfig (youtube-extract.ts:55): the `youtube` section.
func loadYouTubeConfig() (youtubeConfig, error) {
	cfg := youtubeConfig{Enabled: true, PreferredModel: DefaultGeminiModel}
	root, err := readLenientConfig()
	if err != nil {
		return cfg, err
	}
	yt, _ := root["youtube"].(map[string]any)
	if b, ok := yt["enabled"].(bool); ok {
		cfg.Enabled = b
	}
	if s := normalizeGeminiString(yt["preferredModel"]); s != "" {
		cfg.PreferredModel = s
	}
	return cfg, nil
}

// IsYouTubeEnabled is isYouTubeEnabled.
func IsYouTubeEnabled() (bool, error) {
	cfg, err := loadYouTubeConfig()
	return cfg.Enabled, err
}

// attemptErrors collects the failures of the extraction paths (addAttemptError).
type attemptErrors []string

func (a *attemptErrors) add(label string, err error) {
	if msg := jsTrim(collapseJSSpace(err.Error())); msg != "" {
		*a = append(*a, label+": "+msg)
	}
}

// ExtractYouTube is extractYouTube (youtube-extract.ts:97): the result (its Error set when no
// path worked), or nil when the call was cancelled. An error is one the original throws (an
// unreadable configuration).
func ExtractYouTube(ctx context.Context, rawURL, prompt, model string) (*ExtractedContent, error) {
	cfg, err := loadYouTubeConfig()
	if err != nil {
		return nil, err
	}
	_, videoID := IsYouTubeURL(rawURL)
	canonical := rawURL
	if videoID != "" {
		canonical = "https://www.youtube.com/watch?v=" + videoID
	}
	if prompt == "" {
		prompt = youtubePrompt
	}
	if model == "" {
		model = cfg.PreferredModel
	}
	var attempts attemptErrors
	result, err := tryYouTubeGeminiWeb(ctx, canonical, prompt, model, &attempts)
	if err == nil && result == nil {
		result, err = tryYouTubeGeminiAPI(ctx, canonical, prompt, model, &attempts)
	}
	if err == nil && result == nil {
		result, err = tryYouTubePerplexity(ctx, rawURL, prompt, &attempts)
	}
	if err != nil {
		return nil, err
	}
	if result != nil {
		result.URL = rawURL
		if result.Error == nil && videoID != "" && CanAttachImages() {
			if thumb := fetchYouTubeThumbnail(ctx, videoID); thumb != nil {
				result.Thumbnail = thumb
			}
		}
		return result, nil
	}
	if ctx.Err() != nil {
		return nil, nil
	}
	msg := youtubeNoAccess
	if len(attempts) > 0 {
		msg = "Could not extract YouTube video content.\n\n- " + strings.Join(attempts, "\n- ")
	}
	return &ExtractedContent{URL: rawURL, Error: errp(msg)}, nil
}

func youtubeTitle(text string) string {
	if t, ok := ExtractHeadingTitle(text); ok {
		return t
	}
	return "YouTube Video"
}

func tryYouTubeGeminiWeb(ctx context.Context, url, prompt, model string, attempts *attemptErrors) (*ExtractedContent, error) {
	cookies, err := geminiWebClient.Available(ctx)
	if err == nil {
		if cookies == nil || ctx.Err() != nil {
			return nil, nil
		}
		o := GeminiWebOptions{YoutubeURL: url, Timeout: ytGeminiTimeout}
		if model != DefaultGeminiModel {
			o.Model = model
		}
		var text string
		if text, err = geminiWebClient.QueryWithOptions(ctx, prompt, cookies, o); err == nil {
			return &ExtractedContent{URL: url, Title: youtubeTitle(text), Content: text}, nil
		}
	}
	if shouldRethrow(err) {
		return nil, err
	}
	if ctx.Err() == nil {
		attempts.add("Gemini Web", err)
	}
	return nil, nil
}

func tryYouTubeGeminiAPI(ctx context.Context, url, prompt, model string, attempts *attemptErrors) (*ExtractedContent, error) {
	if !IsGeminiAPIAvailableWithVideo() || ctx.Err() != nil {
		return nil, nil
	}
	text, err := QueryGeminiAPIWithVideo(ctx, prompt, url, GeminiAPIOptions{Model: model, Timeout: ytGeminiTimeout})
	if err != nil {
		if shouldRethrow(err) {
			return nil, err
		}
		if ctx.Err() == nil {
			attempts.add("Gemini API", err)
		}
		return nil, nil
	}
	return &ExtractedContent{URL: url, Title: youtubeTitle(text), Content: text}, nil
}

func tryYouTubePerplexity(ctx context.Context, url, prompt string, attempts *attemptErrors) (*ExtractedContent, error) {
	if ctx.Err() != nil || !IsPerplexityAvailable() {
		return nil, nil
	}
	query := prompt + " YouTube video: " + url
	if prompt == youtubePrompt {
		query = "Summarize this YouTube video in detail: " + url
	}
	res, err := SearchWithPerplexity(ctx, query, SearchOptions{})
	if err != nil {
		if shouldRethrow(err) {
			return nil, err
		}
		if ctx.Err() == nil {
			attempts.add("Perplexity", err)
		}
		return nil, nil
	}
	if res.Answer == "" {
		return nil, nil
	}
	const title = "Video Summary (via Perplexity)"
	content := "# " + title + "\n\n" + res.Answer + "\n\n" +
		"*Full video understanding requires Gemini access. Set GEMINI_API_KEY or sign into Google in a supported Chromium browser.*"
	return &ExtractedContent{URL: url, Title: title, Content: content}, nil
}

// StreamInfo is a YouTube stream URL and the video's duration in seconds (nil when unknown).
type StreamInfo struct {
	StreamURL string
	Duration  *float64
}

func mapYtDlpError(err error) string {
	code, stderr, message := readExecError(err)
	if code == "ENOENT" {
		return "yt-dlp is not installed. Install with: brew install yt-dlp"
	}
	if isTimeoutError(err) {
		return "yt-dlp timed out fetching video info"
	}
	lower := strings.ToLower(stderr)
	switch {
	case strings.Contains(lower, "private"):
		return "Video is private or unavailable"
	case strings.Contains(lower, "sign in"):
		return "Video is age-restricted and requires authentication"
	case strings.Contains(lower, "not available"):
		return "Video is unavailable in your region or has been removed"
	case strings.Contains(lower, "live"):
		return "Cannot extract frames from a live stream"
	}
	return failureWithSnippet("yt-dlp", stderr, message)
}

// getYouTubeStreamInfo is getYouTubeStreamInfo (youtube-extract.ts:156): the stream URL and the
// duration, or an error message.
func getYouTubeStreamInfo(ctx context.Context, videoID string) (StreamInfo, string) {
	out, err := runMedia(ctx, "yt-dlp", []string{"--print", "duration", "-g", "https://www.youtube.com/watch?v=" + videoID}, mediaOptions{Timeout: ytInfoTimeout})
	if err != nil {
		return StreamInfo{}, mapYtDlpError(err)
	}
	lines := strings.Split(strings.ReplaceAll(jsTrim(string(out)), "\r\n", "\n"), "\n")
	rawDuration := jsTrim(lines[0])
	streamURL := ""
	if len(lines) > 1 {
		streamURL = jsTrim(lines[1])
	}
	if streamURL == "" {
		return StreamInfo{}, "yt-dlp failed: missing stream URL"
	}
	info := StreamInfo{StreamURL: streamURL}
	if rawDuration != "" && rawDuration != "NA" {
		if d, ok := jsParseFloat(rawDuration); ok && isFinite(d) {
			info.Duration = &d
		}
	}
	return info, ""
}

// extractFrameFromStream is extractFrameFromStream (youtube-extract.ts:174).
func extractFrameFromStream(ctx context.Context, streamURL string, seconds float64) (Image, string) {
	out, err := runMedia(ctx, "ffmpeg", []string{"-ss", jsNumberString(seconds), "-i", streamURL, "-frames:v", "1", "-f", "image2pipe", "-vcodec", "mjpeg", "pipe:1"},
		mediaOptions{Timeout: ytFrameTimeout, MaxBuffer: frameMaxBuffer})
	return frameImage(out, err)
}

// extractFrames is the frame loop of extractYouTubeFrames and extractLocalFrames: one frame per
// timestamp, in order; the error is the first failure and is reported only when no frame came out.
func extractFrames(timestamps []float64, one func(seconds float64) (Image, string)) ([]VideoFrame, string) {
	var frames []VideoFrame
	firstErr := ""
	for _, t := range timestamps {
		img, msg := one(t)
		if msg != "" {
			if firstErr == "" {
				firstErr = msg
			}
			continue
		}
		frames = append(frames, VideoFrame{Data: img.Data, MimeType: img.MimeType, Timestamp: formatSecondsJS(t)})
	}
	if len(frames) > 0 {
		firstErr = ""
	}
	return frames, firstErr
}

// fetchYouTubeThumbnail is fetchYouTubeThumbnail (youtube-extract.ts:214): the video's
// hqdefault thumbnail, or nil.
func fetchYouTubeThumbnail(ctx context.Context, videoID string) *Image {
	tctx, cancel := withTimeout(ctx, ytThumbnailBudget)
	defer cancel()
	res, err := FetchWithCredentialRedirects(tctx, "https://img.youtube.com/vi/"+videoID+"/hqdefault.jpg", RequestInit{}, nil)
	if err != nil {
		return nil
	}
	defer res.Body.Close()
	if !isHTTPOK(res) {
		return nil
	}
	data, err := io.ReadAll(res.Body)
	if err != nil || len(data) == 0 {
		return nil
	}
	return &Image{Data: base64.StdEncoding.EncodeToString(data), MimeType: "image/jpeg"}
}
