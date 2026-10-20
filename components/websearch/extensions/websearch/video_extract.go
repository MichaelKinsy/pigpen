package websearch

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Port of video-extract.ts: local video files, by Gemini (the Files API, then Gemini Web) and
// by ffmpeg and ffprobe for frames and durations.

const defaultVideoPrompt = `Extract the complete content of this video. Include:
1. Video title (infer from content if not explicit), duration
2. A brief summary (2-3 sentences)
3. Full transcript with timestamps
4. Descriptions of any code, terminal commands, diagrams, slides, or UI shown on screen

Format as markdown.`

// videoMimeTypes is VIDEO_EXTENSIONS (video-extract.ts:22).
var videoMimeTypes = map[string]string{
	".mp4":  "video/mp4",
	".mov":  "video/quicktime",
	".webm": "video/webm",
	".avi":  "video/x-msvideo",
	".mpeg": "video/mpeg",
	".mpg":  "video/mpeg",
	".wmv":  "video/x-ms-wmv",
	".flv":  "video/x-flv",
	".3gp":  "video/3gpp",
	".3gpp": "video/3gpp",
}

const (
	videoFrameTimeout    = 10 * time.Second // video-extract.ts:216
	videoProbeTimeout    = 10 * time.Second // video-extract.ts:231
	frameMaxBuffer       = 5 * 1024 * 1024  // video-extract.ts:215 and youtube-extract.ts:180
	videoGeminiTimeout   = 120 * time.Second
	videoWebTimeout      = 180 * time.Second
	filePollTimeout      = 120 * time.Second
	geminiDeleteDeadline = 5 * time.Minute
)

// VideoFileInfo is VideoFileInfo upstream.
type VideoFileInfo struct {
	AbsolutePath      string
	MimeType          string
	SizeBytes         int64
	MaxSizeBytes      float64
	WithinUploadLimit bool
}

type videoConfig struct {
	Enabled        bool
	PreferredModel string
	MaxSizeMB      float64
}

// loadVideoConfig is loadVideoConfig (video-extract.ts:77): the `video` section of web-search.json.
func loadVideoConfig() (videoConfig, error) {
	cfg := videoConfig{Enabled: true, PreferredModel: DefaultGeminiModel, MaxSizeMB: 50}
	root, err := readLenientConfig()
	if err != nil {
		return cfg, err
	}
	v, _ := root["video"].(map[string]any)
	if b, ok := v["enabled"].(bool); ok {
		cfg.Enabled = b
	}
	if s := normalizeGeminiString(v["preferredModel"]); s != "" {
		cfg.PreferredModel = s
	}
	if n, ok := v["maxSizeMB"].(float64); ok && isFinite(n) && n > 0 {
		cfg.MaxSizeMB = n
	}
	return cfg, nil
}

// nodeExtname is path.extname: a name that is only a leading dot has no extension.
func nodeExtname(p string) string {
	base := filepath.Base(p)
	i := strings.LastIndexByte(base, '.')
	if i <= 0 {
		return ""
	}
	return base[i:]
}

// IsVideoFile is isVideoFile (video-extract.ts:102): input names an existing local video file
// (an absolute or ./ ../ path or a file: URL with a video extension) and video.enabled is not
// false. A configuration that does not parse is an error.
func IsVideoFile(input string) (*VideoFileInfo, error) {
	cfg, err := loadVideoConfig()
	if err != nil {
		return nil, err
	}
	if !cfg.Enabled {
		return nil, nil
	}
	if !(strings.HasPrefix(input, "/") || strings.HasPrefix(input, "./") || strings.HasPrefix(input, "../") || strings.HasPrefix(input, "file://")) {
		return nil, nil
	}
	path := input
	if strings.HasPrefix(input, "file://") {
		u, err := ParseURL(input, nil)
		if err != nil {
			return nil, nil
		}
		decoded, err := url.PathUnescape(u.EscapedPath())
		if err != nil || !utf8.ValidString(decoded) {
			return nil, nil
		}
		path = decoded
	}
	mime, ok := videoMimeTypes[strings.ToLower(nodeExtname(path))]
	if !ok {
		return nil, nil
	}
	abs := resolveVideoPath(path)
	if abs == "" {
		return nil, nil
	}
	st, err := os.Stat(abs)
	if err != nil || !st.Mode().IsRegular() {
		return nil, nil
	}
	maxBytes := cfg.MaxSizeMB * 1024 * 1024
	return &VideoFileInfo{AbsolutePath: abs, MimeType: mime, SizeBytes: st.Size(), MaxSizeBytes: maxBytes, WithinUploadLimit: float64(st.Size()) <= maxBytes}, nil
}

// resolveVideoPath is resolveFilePath: the absolute path, or the sibling whose name matches
// with its unusual spaces normalised ("" when there is none).
func resolveVideoPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return ""
	}
	if _, err := os.Stat(abs); err == nil {
		return abs
	}
	dir, base := filepath.Dir(abs), filepath.Base(abs)
	if _, err := os.Stat(dir); err != nil {
		return ""
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	want := normalizeSpaces(base)
	for _, e := range entries {
		if normalizeSpaces(e.Name()) == want {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}

// normalizeSpaces maps the unusual space characters of file names to a plain space.
func normalizeSpaces(s string) string {
	return strings.Map(func(r rune) rune {
		if r == 0xA0 || r >= 0x2000 && r <= 0x200B || r == 0x202F || r == 0x205F || r == 0x3000 || r == 0xFEFF {
			return ' '
		}
		return r
	}, s)
}

// ExtractVideo is extractVideo (video-extract.ts:164): the result, or nil when no path could
// read the video; an error is one the original throws (an unreadable configuration or a
// credential failure).
func ExtractVideo(ctx context.Context, info VideoFileInfo, o ExtractOptions) (*ExtractedContent, error) {
	cfg, err := loadVideoConfig()
	if err != nil {
		return nil, err
	}
	prompt := o.Prompt
	if prompt == "" {
		prompt = defaultVideoPrompt
	}
	model := o.Model
	if model == "" {
		model = cfg.PreferredModel
	}
	display := filepath.Base(info.AbsolutePath)
	if !info.WithinUploadLimit {
		sizeMB := jsToFixed1(float64(info.SizeBytes) / 1024 / 1024)
		maxMB := jsToFixed1(info.MaxSizeBytes / 1024 / 1024)
		msg := "Local video " + display + " is " + sizeMB + " MiB, above configured video.maxSizeMB (" + maxMB + " MiB) for Gemini analysis. Use timestamp/frames for ffmpeg frame extraction, increase video.maxSizeMB, or compress the file."
		return &ExtractedContent{URL: info.AbsolutePath, Title: display, Content: msg, Error: errp(msg)}, nil
	}
	result, err := tryVideoGeminiAPI(ctx, info, prompt, model)
	if err != nil {
		return nil, err
	}
	if result == nil {
		if result, err = tryVideoGeminiWeb(ctx, info, prompt, model); err != nil {
			return nil, err
		}
	}
	if result == nil {
		return nil, nil
	}
	if CanAttachImages() {
		if thumb, msg := extractVideoFrame(ctx, info.AbsolutePath, 1); msg == "" {
			result.Thumbnail = &thumb
		}
	}
	return result, nil
}

// extractVideoFrame is extractVideoFrame (video-extract.ts:211): one JPEG frame, or an error message.
func extractVideoFrame(ctx context.Context, filePath string, seconds float64) (Image, string) {
	out, err := runMedia(ctx, "ffmpeg", []string{"-ss", jsNumberString(seconds), "-i", filePath, "-frames:v", "1", "-f", "image2pipe", "-vcodec", "mjpeg", "pipe:1"},
		mediaOptions{Timeout: videoFrameTimeout, MaxBuffer: frameMaxBuffer})
	return frameImage(out, err)
}

// frameImage turns the output of an ffmpeg frame extraction into an image or an error message.
func frameImage(out []byte, err error) (Image, string) {
	if err != nil {
		return Image{}, mapFfmpegError(err)
	}
	if len(out) == 0 {
		return Image{}, "ffmpeg failed: empty output"
	}
	return Image{Data: base64.StdEncoding.EncodeToString(out), MimeType: "image/jpeg"}, ""
}

func mapFfprobeError(err error) string {
	code, stderr, message := readExecError(err)
	if code == "ENOENT" {
		return "ffprobe is not installed. Install ffmpeg which includes ffprobe"
	}
	return failureWithSnippet("ffprobe", stderr, message)
}

// getLocalVideoDuration is getLocalVideoDuration (video-extract.ts:224): the duration in seconds,
// or an error message.
func getLocalVideoDuration(ctx context.Context, filePath string) (float64, string) {
	out, err := runMedia(ctx, "ffprobe", []string{"-v", "quiet", "-show_entries", "format=duration", "-of", "csv=p=0", filePath}, mediaOptions{Timeout: videoProbeTimeout})
	if err != nil {
		return 0, mapFfprobeError(err)
	}
	d, ok := jsParseFloat(jsTrim(string(out)))
	if !ok || !isFinite(d) {
		return 0, "ffprobe failed: invalid duration output"
	}
	return d, ""
}

// shouldRethrow is shouldRethrow: only an unreadable configuration ends the fallback chain.
func shouldRethrow(err error) bool { return strings.HasPrefix(err.Error(), "Failed to parse ") }

func videoTitle(text, filePath string) string {
	if t, ok := ExtractHeadingTitle(text); ok {
		return t
	}
	base := filepath.Base(filePath)
	return strings.TrimSuffix(base, nodeExtname(base))
}

func tryVideoGeminiWeb(ctx context.Context, info VideoFileInfo, prompt, model string) (*ExtractedContent, error) {
	cookies, err := geminiWebClient.Available(ctx)
	if err == nil && cookies != nil && ctx.Err() == nil {
		o := GeminiWebOptions{Files: []string{info.AbsolutePath}, Timeout: videoWebTimeout}
		if model != DefaultGeminiModel {
			o.Model = model
		}
		var text string
		if text, err = geminiWebClient.QueryWithOptions(ctx, prompt, cookies, o); err == nil {
			return &ExtractedContent{URL: info.AbsolutePath, Title: videoTitle(text, info.AbsolutePath), Content: text}, nil
		}
	}
	if err != nil && shouldRethrow(err) {
		return nil, err
	}
	return nil, nil
}

// geminiFilePollInterval is the pause between file state checks (video-extract.ts:376).
var geminiFilePollInterval = 5 * time.Second

// pendingGeminiDeletes counts the file deletions still running (a test waits on it).
var pendingGeminiDeletes sync.WaitGroup

func tryVideoGeminiAPI(ctx context.Context, info VideoFileInfo, prompt, model string) (*ExtractedContent, error) {
	apiKey, err := GetAPIKey(ctx)
	if err != nil {
		return nil, err
	}
	if apiKey == "" || ctx.Err() != nil {
		return nil, nil
	}
	uploaded, err := uploadToFilesAPI(ctx, info, apiKey)
	if err != nil {
		if shouldRethrow(err) {
			return nil, err
		}
		return nil, nil
	}
	if uploaded.Name != "" {
		defer deleteGeminiFile(ctx, uploaded.Name, apiKey)
	}
	if err := pollFileState(ctx, uploaded.Name, apiKey, filePollTimeout); err != nil {
		if shouldRethrow(err) {
			return nil, err
		}
		return nil, nil
	}
	text, err := QueryGeminiAPIWithVideo(ctx, prompt, uploaded.URI, GeminiAPIOptions{APIKey: apiKey, Model: model, MimeType: info.MimeType, Timeout: videoGeminiTimeout})
	if err != nil {
		if shouldRethrow(err) {
			return nil, err
		}
		return nil, nil
	}
	return &ExtractedContent{URL: info.AbsolutePath, Title: videoTitle(text, info.AbsolutePath), Content: text}, nil
}

type geminiFile struct{ Name, URI string }

// uploadToFilesAPI is uploadToFilesApi: the resumable upload of the file.
func uploadToFilesAPI(ctx context.Context, info VideoFileInfo, apiKey string) (geminiFile, error) {
	display := filepath.Base(info.AbsolutePath)
	meta, err := jsStringify(map[string]any{"file": map[string]any{"display_name": display}})
	if err != nil {
		return geminiFile{}, err
	}
	initRes, err := FetchGeminiAPI(ctx, GetUploadBase()+"/files", RequestInit{Method: "POST", Header: http.Header{
		"X-Goog-Upload-Protocol":              {"resumable"},
		"X-Goog-Upload-Command":               {"start"},
		"X-Goog-Upload-Header-Content-Length": {strconv.FormatInt(info.SizeBytes, 10)},
		"X-Goog-Upload-Header-Content-Type":   {info.MimeType},
		"Content-Type":                        {"application/json"},
	}, Body: []byte(meta)}, &apiKey)
	if err != nil {
		return geminiFile{}, err
	}
	if !isHTTPOK(initRes) {
		text, _ := readAllClose(initRes)
		return geminiFile{}, fmt.Errorf("File upload init failed: %d (%s)", initRes.StatusCode, jsSlice(RedactGeminiAPIResponse(initRes, text, apiKey), 0, 200))
	}
	initRes.Body.Close()
	uploadURL := initRes.Header.Get("x-goog-upload-url")
	if uploadURL == "" {
		return geminiFile{}, errors.New("No upload URL in response headers")
	}
	data, err := os.ReadFile(info.AbsolutePath)
	if err != nil {
		return geminiFile{}, err
	}
	uploadRes, err := FetchGeminiAPI(ctx, uploadURL, RequestInit{Method: "PUT", Header: http.Header{
		"Content-Length":        {strconv.FormatInt(info.SizeBytes, 10)},
		"X-Goog-Upload-Offset":  {"0"},
		"X-Goog-Upload-Command": {"upload, finalize"},
	}, Body: data}, &apiKey)
	if err != nil {
		return geminiFile{}, err
	}
	if !isHTTPOK(uploadRes) {
		text, _ := readAllClose(uploadRes)
		return geminiFile{}, fmt.Errorf("File upload failed: %d (%s)", uploadRes.StatusCode, jsSlice(RedactGeminiAPIResponse(uploadRes, text, apiKey), 0, 200))
	}
	var result struct {
		File *struct{ Name, URI string } `json:"file"`
	}
	body, err := readAllClose(uploadRes)
	if err != nil {
		return geminiFile{}, err
	}
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		return geminiFile{}, err
	}
	if result.File == nil {
		return geminiFile{}, errors.New("Cannot read properties of undefined (reading 'name')")
	}
	return geminiFile{Name: result.File.Name, URI: result.File.URI}, nil
}

func readAllClose(resp *http.Response) (string, error) {
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return string(b), err
}

// pollFileState is pollFileState: waits until the uploaded file is ACTIVE.
func pollFileState(ctx context.Context, fileName, apiKey string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return errors.New("Aborted")
		}
		res, err := FetchGeminiAPI(ctx, GetVersionedAPIBase()+"/"+fileName, RequestInit{}, &apiKey)
		if err != nil {
			return err
		}
		if !isHTTPOK(res) {
			res.Body.Close()
			return fmt.Errorf("File state check failed: %d", res.StatusCode)
		}
		body, err := readAllClose(res)
		if err != nil {
			return err
		}
		var data struct {
			State string `json:"state"`
		}
		if err := json.Unmarshal([]byte(body), &data); err != nil {
			return err
		}
		switch data.State {
		case "ACTIVE":
			return nil
		case "FAILED":
			return errors.New("File processing failed")
		}
		select {
		case <-time.After(geminiFilePollInterval):
		case <-ctx.Done():
		}
	}
	return errors.New("File processing timed out")
}

// deleteGeminiFile is deleteGeminiFile: the request is not awaited and ignores the caller's
// cancellation, so an aborted fetch still removes its upload; a failure is dropped.
func deleteGeminiFile(ctx context.Context, fileName, apiKey string) {
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), geminiDeleteDeadline)
	pendingGeminiDeletes.Add(1)
	go func() {
		defer pendingGeminiDeletes.Done()
		defer cancel()
		res, err := FetchGeminiAPI(dctx, GetVersionedAPIBase()+"/"+fileName, RequestInit{Method: "DELETE"}, &apiKey)
		if err == nil {
			res.Body.Close()
		}
	}()
}
