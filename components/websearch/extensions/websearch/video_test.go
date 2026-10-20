package websearch

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// mediaCall is one program the port started.
type mediaCall struct {
	Name string
	Args []string
	Opts mediaOptions
}

// useMedia replaces the process runner of yt-dlp, ffmpeg and ffprobe for the test.
func useMedia(t *testing.T, fn func(name string, args []string) ([]byte, error)) *[]mediaCall {
	t.Helper()
	var mu sync.Mutex
	calls := &[]mediaCall{}
	t.Cleanup(SetMediaRunner(func(_ context.Context, name string, args []string, o mediaOptions) ([]byte, error) {
		mu.Lock()
		*calls = append(*calls, mediaCall{name, args, o})
		mu.Unlock()
		return fn(name, args)
	}))
	return calls
}

func enoent(name string) error {
	return &execError{Code: "ENOENT", Message: "spawnSync " + name + " ENOENT"}
}

// noMediaPrograms makes every media program missing.
func noMediaPrograms(t *testing.T) *[]mediaCall {
	t.Helper()
	return useMedia(t, func(name string, _ []string) ([]byte, error) { return nil, enoent(name) })
}

func writeVideo(t *testing.T, dir, name, mime, content string) VideoFileInfo {
	t.Helper()
	p := filepath.Join(dir, name)
	noErr(t, os.WriteFile(p, []byte(content), 0o600))
	return VideoFileInfo{AbsolutePath: p, MimeType: mime, SizeBytes: int64(len(content)), MaxSizeBytes: 50 * 1024 * 1024, WithinUploadLimit: true}
}

// geminiFilesAPI answers the Files API flow of video-extract.ts (start, upload, state, generate,
// delete) from the host base.
func geminiFilesAPI(base, answer string) func(netCall) netReply {
	return func(c netCall) netReply {
		switch {
		case c.Method == "POST" && strings.HasSuffix(c.URL, "/upload/v1beta/files"):
			return netReply{Status: 200, Header: hdr("x-goog-upload-url", base+"/upload/v1beta/files?upload_id=synthetic")}
		case strings.Contains(c.URL, "upload_id=synthetic"):
			return jsonReply(map[string]any{"file": map[string]any{"name": "files/synthetic", "uri": "files/synthetic"}})
		case c.Method == "GET" && strings.HasSuffix(c.URL, "/v1beta/files/synthetic"):
			return jsonReply(map[string]any{"state": "ACTIVE"})
		case strings.Contains(c.URL, ":generateContent"):
			return geminiText(answer)
		case c.Method == "DELETE":
			return reply(204, "")
		}
		return netReply{Err: errors.New("Unexpected fetch: " + c.URL)}
	}
}

func sparseVideo(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "oversized.mp4")
	noErr(t, os.WriteFile(p, nil, 0o600))
	noErr(t, os.Truncate(p, 50*1024*1024+1))
	return p
}

func TestUpstream_local_video_oversize(t *testing.T) {
	const F = "local-video-oversize"
	tw(t, F, "oversized local videos are still recognized for frame extraction", func(t *testing.T) {
		adapted(t, "ffprobe is a canned missing program (the original runs the machine's)")
		_, agentDir := geminiEnv(t)
		path := sparseVideo(t, agentDir)
		noMediaPrograms(t)
		err := errOf(ExtractContent(bg(), path, ExtractOptions{Frames: 1}))
		if err == "Frame extraction only works with YouTube and local video files" {
			t.Fatal(err)
		}
		matchRE(t, `(?i)ffprobe|Cannot determine video duration|failed|not installed`, err)
	})
	tw(t, F, "oversized local videos are still recognized for timestamp extraction", func(t *testing.T) {
		adapted(t, "ffmpeg is a canned missing program (the original runs the machine's)")
		_, agentDir := geminiEnv(t)
		path := sparseVideo(t, agentDir)
		noMediaPrograms(t)
		err := errOf(ExtractContent(bg(), path, ExtractOptions{Timestamp: "1"}))
		if err == "Timestamp extraction only works with YouTube and local video files" {
			t.Fatal(err)
		}
		matchRE(t, `(?i)ffmpeg|failed|not installed`, err)
	})
	tw(t, F, "oversized local video analysis reports the upload size limit", func(t *testing.T) {
		_, agentDir := geminiEnv(t)
		path := sparseVideo(t, agentDir)
		calls := noMediaPrograms(t)
		err := errOf(ExtractContent(bg(), path, ExtractOptions{Prompt: "summarize"}))
		matchRE(t, `above configured video\.maxSizeMB`, err)
		matchRE(t, `timestamp/frames`, err)
		if len(*calls) != 0 {
			t.Fatal(*calls)
		}
	})
}

func TestUpstream_youtube_extract_errors(t *testing.T) {
	const F = "youtube-extract-errors"
	tw(t, F, "YouTube extraction surfaces Gemini API errors", func(t *testing.T) {
		geminiEnv(t)
		t.Setenv("GEMINI_API_KEY", "test-gemini-key")
		t.Setenv("PERPLEXITY_API_KEY", "")
		useNet(t, func(c netCall) netReply {
			if strings.HasPrefix(c.URL, "https://generativelanguage.googleapis.com/") {
				return netReply{Status: 503, Body: `{"error":{"message":"model overloaded"}}`, Header: hdr("content-type", "application/json")}
			}
			return netReply{Err: errors.New("Unexpected fetch: " + c.URL)}
		})
		r := ExtractContent(bg(), "https://www.youtube.com/watch?v=dQw4w9WgXcQ", pageOpts())
		matchRE(t, `Gemini API error 503`, errOf(r))
		noMatchRE(t, `Sign into Google in a supported Chromium browser`, errOf(r))
	})
}

func TestVideoTimestampSpecs(t *testing.T) {
	cases := []struct {
		in   string
		want timestampSpec
		ok   bool
	}{
		{"85", timestampSpec{Seconds: 85}, true},
		{"85.9", timestampSpec{Seconds: 85}, true},
		{"1:23", timestampSpec{Seconds: 83}, true},
		{"1:02:03", timestampSpec{Seconds: 3723}, true},
		{"1e3", timestampSpec{Seconds: 1000}, true},
		{"0x10", timestampSpec{Seconds: 16}, true},
		{" 7 ", timestampSpec{Seconds: 7}, true},
		{"0:10-1:10", timestampSpec{Range: true, Start: 10, End: 70}, true},
		{"10-30", timestampSpec{Range: true, Start: 10, End: 30}, true},
		{"1:00-0:10", timestampSpec{}, false},
		{"30-30", timestampSpec{}, false},
		{"-5", timestampSpec{}, false},
		{"later", timestampSpec{}, false},
		{"1:2:3:4", timestampSpec{}, false},
		{"a:10", timestampSpec{}, false},
		{"1:-5", timestampSpec{}, false},
	}
	for _, c := range cases {
		got, ok := parseTimestampSpec(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("%q: %+v %v, want %+v %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestVideoRangeTimestamps(t *testing.T) {
	cases := []struct {
		start, end float64
		n          int
		want       []float64
	}{
		{0, 125, 3, []float64{0, 63, 125}},
		{0, 20, 6, []float64{0, 5, 10, 15, 20}},
		{10, 70, 6, []float64{10, 22, 34, 46, 58, 70}},
		{5, 50, 1, []float64{5}},
		{20, 30, 5, []float64{20, 25, 30}},
		{0, 0, 2, []float64{0}},
	}
	for _, c := range cases {
		if got := computeRangeTimestamps(c.start, c.end, c.n); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%v-%v x%d: %v, want %v", c.start, c.end, c.n, got, c.want)
		}
	}
}

func TestVideoNumberFormats(t *testing.T) {
	for in, want := range map[float64]string{83: "1:23", 3723: "1:02:03", 59: "0:59", 3600: "1:00:00", 0: "0:00"} {
		if got := formatSecondsJS(in); got != want {
			t.Errorf("formatSeconds(%v) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[float64]string{0.25: "0.3", 0.35: "0.3", 0.05: "0.1", 50.000000954: "50.0", 15.0 / 1048576: "0.0", 12.34: "12.3", 100: "100.0"} {
		if got := jsToFixed1(in); got != want {
			t.Errorf("toFixed(%v) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[float64]string{0: "0", 5: "5", 12.5: "12.5", 1e21: "1e+21", 1e-7: "1e-7", 123456789012: "123456789012"} {
		if got := jsNumberString(in); got != want {
			t.Errorf("String(%v) = %q, want %q", in, got, want)
		}
	}
	if f, ok := jsParseFloat("12.5abc"); !ok || f != 12.5 {
		t.Error(f, ok)
	}
	if _, ok := jsParseFloat("abc"); ok {
		t.Error("abc")
	}
	if _, ok := jsToNumber("1_0"); ok {
		t.Error("1_0")
	}
	if f, ok := jsToNumber(""); !ok || f != 0 {
		t.Error("empty")
	}
}

func TestVideoProgramErrors(t *testing.T) {
	exit := func(stderr string) error {
		return &execError{Stderr: stderr, Message: "Command failed: x\n" + stderr}
	}
	timeout := &execError{Code: "ETIMEDOUT", Message: "spawnSync x ETIMEDOUT", Killed: true}
	for _, c := range []struct {
		name string
		got  string
		want string
	}{
		{"yt-dlp missing", mapYtDlpError(enoent("yt-dlp")), "yt-dlp is not installed. Install with: brew install yt-dlp"},
		{"yt-dlp timeout", mapYtDlpError(timeout), "yt-dlp timed out fetching video info"},
		{"private", mapYtDlpError(exit("ERROR: Private video")), "Video is private or unavailable"},
		{"sign in", mapYtDlpError(exit("Please Sign In")), "Video is age-restricted and requires authentication"},
		{"unavailable", mapYtDlpError(exit("Video not available")), "Video is unavailable in your region or has been removed"},
		{"live", mapYtDlpError(exit("this is a live event")), "Cannot extract frames from a live stream"},
		{"other", mapYtDlpError(exit("  odd\n   thing ")), "yt-dlp failed: odd thing"},
		{"no text", mapYtDlpError(&execError{}), "yt-dlp failed"},
		{"ffmpeg missing", mapFfmpegError(enoent("ffmpeg")), "ffmpeg is not installed. Install with: brew install ffmpeg"},
		{"ffmpeg timeout", mapFfmpegError(timeout), "ffmpeg timed out extracting frame"},
		{"ffmpeg 403", mapFfmpegError(exit("HTTP error 403 Forbidden")), "Stream URL returned 403 — may have expired, try again"},
		{"ffmpeg message", mapFfmpegError(&execError{Message: "spawnSync ffmpeg ENOBUFS", Code: "ENOBUFS"}), "ffmpeg failed: spawnSync ffmpeg ENOBUFS"},
		{"ffprobe missing", mapFfprobeError(enoent("ffprobe")), "ffprobe is not installed. Install ffmpeg which includes ffprobe"},
		{"ffprobe other", mapFfprobeError(exit("moov atom not found")), "ffprobe failed: moov atom not found"},
		{"long stderr", mapFfmpegError(exit(strings.Repeat("é", 300))), "ffmpeg failed: " + strings.Repeat("é", 200)},
	} {
		if c.got != c.want {
			t.Errorf("%s: %q, want %q", c.name, c.got, c.want)
		}
	}
}

func TestVideoRealPrograms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the programs are shell scripts")
	}
	bin := t.TempDir()
	script := func(name, body string) {
		noErr(t, os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755))
	}
	script("binary", `printf '\377\330\377\340\000JFIF'`)
	script("failing", `printf 'first line\nsecond' >&2; exit 3`)
	script("silent", `exit 2`)
	script("loud", `printf 'abcdefgh'`)
	script("slow", `while :; do :; done`)
	t.Setenv("PATH", bin)

	out, err := runMediaProcess(bg(), "binary", []string{"a", "b"}, mediaOptions{Timeout: 5 * time.Second})
	noErr(t, err)
	if base64.StdEncoding.EncodeToString(out) != "/9j/4ABKRklG" {
		t.Fatalf("binary output %q", out)
	}
	_, err = runMediaProcess(bg(), "failing", []string{"x", "y z"}, mediaOptions{Timeout: 5 * time.Second})
	if got := err.Error(); got != "Command failed: failing x y z\nfirst line\nsecond" {
		t.Fatalf("%q", got)
	}
	if code, stderr, _ := readExecError(err); code != "" || stderr != "first line\nsecond" {
		t.Fatal(code, stderr)
	}
	_, err = runMediaProcess(bg(), "silent", nil, mediaOptions{Timeout: 5 * time.Second})
	if got := mapFfmpegError(err); got != "ffmpeg failed: Command failed: silent" {
		t.Fatalf("%q", got)
	}
	_, err = runMediaProcess(bg(), "absent-program", nil, mediaOptions{Timeout: 5 * time.Second})
	if code, _, msg := readExecError(err); code != "ENOENT" || msg != "spawnSync absent-program ENOENT" {
		t.Fatal(code, msg)
	}
	_, err = runMediaProcess(bg(), "loud", nil, mediaOptions{Timeout: 5 * time.Second, MaxBuffer: 4})
	if code, _, _ := readExecError(err); code != "ENOBUFS" {
		t.Fatalf("%v", err)
	}
	start := time.Now()
	_, err = runMediaProcess(bg(), "slow", nil, mediaOptions{Timeout: 100 * time.Millisecond})
	if code, _, _ := readExecError(err); code != "ETIMEDOUT" || !isTimeoutError(err) || time.Since(start) > 5*time.Second {
		t.Fatalf("%v after %v", err, time.Since(start))
	}
	ctx, cancel := context.WithCancel(bg())
	time.AfterFunc(100*time.Millisecond, cancel)
	if _, err = runMediaProcess(ctx, "slow", nil, mediaOptions{Timeout: 30 * time.Second}); err == nil {
		t.Fatal("a cancelled call succeeded")
	}
	if ctx.Err() == nil {
		t.Fatal("not cancelled")
	}
}

// fakeVideoWeb is a Gemini Web client that records the options of each query.
type fakeVideoWeb struct {
	cookies GeminiWebCookies
	err     error
	answer  string
	queryEr error
	opts    []GeminiWebOptions
	prompts []string
}

func (f *fakeVideoWeb) Available(context.Context) (GeminiWebCookies, error) { return f.cookies, f.err }
func (f *fakeVideoWeb) Diagnostic() string                                  { return "" }
func (f *fakeVideoWeb) Query(context.Context, string, GeminiWebCookies, time.Duration) (string, error) {
	return "", errors.New("unexpected Query")
}
func (f *fakeVideoWeb) QueryWithOptions(_ context.Context, prompt string, _ GeminiWebCookies, o GeminiWebOptions) (string, error) {
	f.opts = append(f.opts, o)
	f.prompts = append(f.prompts, prompt)
	return f.answer, f.queryEr
}

func TestYouTubeGeminiWeb(t *testing.T) {
	const yt = "https://youtu.be/dQw4w9WgXcQ"
	const canonical = "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
	noThumb := `{"image":{"enabled":false}}`
	t.Run("the cookies read the video first, with the canonical URL and a two minute budget", func(t *testing.T) {
		_, agentDir := geminiEnv(t)
		writeConfig(t, agentDir, noThumb)
		web := &fakeVideoWeb{cookies: GeminiWebCookies{"c": "v"}, answer: "# Web Title\n\nbody"}
		t.Cleanup(SetGeminiWebClient(web))
		n := useNet(t, func(netCall) netReply { return netReply{Err: errors.New("no network")} })
		r := ExtractContent(bg(), yt, pageOpts())
		if r.Error != nil || r.Title != "Web Title" || r.Content != "# Web Title\n\nbody" || r.URL != yt {
			t.Fatalf("%+v", r)
		}
		want := GeminiWebOptions{YoutubeURL: canonical, Timeout: 120 * time.Second}
		if !reflect.DeepEqual(web.opts, []GeminiWebOptions{want}) || web.prompts[0] != youtubePrompt || n.count() != 0 {
			t.Fatalf("%+v %q %v", web.opts, web.prompts, n.urls())
		}
	})
	t.Run("a model other than the default is passed on, a prompt replaces the default", func(t *testing.T) {
		_, agentDir := geminiEnv(t)
		writeConfig(t, agentDir, `{"image":{"enabled":false},"youtube":{"preferredModel":"gemini-2.5-pro"}}`)
		web := &fakeVideoWeb{cookies: GeminiWebCookies{"c": "v"}, answer: "no heading"}
		t.Cleanup(SetGeminiWebClient(web))
		r := ExtractContent(bg(), yt, ExtractOptions{Lookup: pubLookup, Prompt: "Who speaks?"})
		if r.Title != "YouTube Video" || web.prompts[0] != "Who speaks?" || web.opts[0].Model != "gemini-2.5-pro" {
			t.Fatalf("%+v %+v", r, web.opts)
		}
	})
	t.Run("a failing Gemini Web is the first line of the combined failure", func(t *testing.T) {
		_, agentDir := geminiEnv(t)
		writeConfig(t, agentDir, noThumb)
		t.Setenv("GEMINI_API_KEY", "synthetic-gemini-key")
		web := &fakeVideoWeb{cookies: GeminiWebCookies{"c": "v"}, queryEr: errors.New("Gemini request\nfailed:  500")}
		t.Cleanup(SetGeminiWebClient(web))
		useNet(t, func(netCall) netReply { return netReply{Status: 503, Body: "{}"} })
		got := errOf(ExtractContent(bg(), yt, pageOpts()))
		matchRE(t, `^Could not extract YouTube video content\.\n\n- Gemini Web: Gemini request failed: 500\n- Gemini API: `, got)
	})
	t.Run("an unreadable Gemini Web availability that is a config parse failure ends the fetch", func(t *testing.T) {
		geminiEnv(t)
		web := &fakeVideoWeb{err: errors.New("Failed to parse /x/web-search.json: bad")}
		t.Cleanup(SetGeminiWebClient(web))
		if got := errOf(ExtractContent(bg(), yt, pageOpts())); got != "Failed to parse /x/web-search.json: bad" {
			t.Fatal(got)
		}
	})
	t.Run("another availability failure is one more attempt", func(t *testing.T) {
		geminiEnv(t)
		t.Setenv("GEMINI_API_KEY", "synthetic-gemini-key")
		web := &fakeVideoWeb{err: errors.New("cookie store locked")}
		t.Cleanup(SetGeminiWebClient(web))
		useNet(t, func(netCall) netReply { return netReply{Status: 500, Body: "{}"} })
		matchRE(t, `- Gemini Web: cookie store locked\n- Gemini API: `, errOf(ExtractContent(bg(), yt, pageOpts())))
	})
	t.Run("a cancelled call is Aborted and reports no attempt", func(t *testing.T) {
		geminiEnv(t)
		web := &fakeVideoWeb{cookies: GeminiWebCookies{"c": "v"}, queryEr: errors.New("boom")}
		t.Cleanup(SetGeminiWebClient(web))
		ctx, cancel := context.WithCancel(bg())
		cancel()
		if got := errOf(ExtractContent(ctx, yt, pageOpts())); got != "Aborted" {
			t.Fatal(got)
		}
	})
}

func TestYouTubeRouting(t *testing.T) {
	const yt = "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
	t.Run("the page fetch takes over when youtube.enabled is false", func(t *testing.T) {
		_, agentDir := geminiEnv(t)
		writeConfig(t, agentDir, `{"youtube":{"enabled":false}}`)
		web := &fakeVideoWeb{cookies: GeminiWebCookies{"c": "v"}, answer: "# x"}
		t.Cleanup(SetGeminiWebClient(web))
		n := useNet(t, func(netCall) netReply { return reply(404, "nope") })
		_ = ExtractContent(bg(), yt, pageOpts())
		if len(web.opts) != 0 || n.count() == 0 {
			t.Fatal(web.opts, n.urls())
		}
	})
	t.Run("a configuration that does not parse is the error of every URL", func(t *testing.T) {
		_, agentDir := geminiEnv(t)
		writeConfig(t, agentDir, "{ nope")
		for _, u := range []string{yt, "https://example.com/page"} {
			matchRE(t, `^Failed to parse .*web-search\.json`, errOf(ExtractContent(bg(), u, pageOpts())))
		}
	})
	t.Run("the video id forms", func(t *testing.T) {
		for u, id := range map[string]string{
			"https://youtu.be/dQw4w9WgXcQ":                          "dQw4w9WgXcQ",
			"https://m.youtube.com/shorts/dQw4w9WgXcQ?x=1":          "dQw4w9WgXcQ",
			"https://www.youtube.com/live/dQw4w9WgXcQ":              "dQw4w9WgXcQ",
			"https://youtube.com/embed/dQw4w9WgXcQ":                 "dQw4w9WgXcQ",
			"https://www.youtube.com/watch?list=x&v=dQw4w9WgXcQ&t=": "dQw4w9WgXcQ",
		} {
			if ok, got := IsYouTubeURL(u); !ok || got != id {
				t.Errorf("%s: %v %q", u, ok, got)
			}
		}
		for _, u := range []string{"https://www.youtube.com/playlist?list=PL1234567890a", "https://example.com/", "https://www.youtube.com/watch?v=short"} {
			if ok, _ := IsYouTubeURL(u); ok {
				t.Errorf("%s is not a video", u)
			}
		}
	})
}

func TestLocalVideoFallbacks(t *testing.T) {
	t.Run("Gemini Web reads the file when there is no API key", func(t *testing.T) {
		_, agentDir := geminiEnv(t)
		info := writeVideo(t, agentDir, "clip.mp4", "video/mp4", "synthetic media")
		web := &fakeVideoWeb{cookies: GeminiWebCookies{"c": "v"}, answer: "# Web Clip"}
		t.Cleanup(SetGeminiWebClient(web))
		calls := useMedia(t, func(name string, _ []string) ([]byte, error) { return []byte("JPEG"), nil })
		r := ExtractContent(bg(), info.AbsolutePath, ExtractOptions{Lookup: pubLookup})
		if r.Error != nil || r.Title != "Web Clip" || r.URL != info.AbsolutePath || r.Thumbnail == nil || r.Thumbnail.Data != base64.StdEncoding.EncodeToString([]byte("JPEG")) {
			t.Fatalf("%+v", r)
		}
		if want := (GeminiWebOptions{Files: []string{info.AbsolutePath}, Timeout: 180 * time.Second}); !reflect.DeepEqual(web.opts, []GeminiWebOptions{want}) || web.prompts[0] != defaultVideoPrompt {
			t.Fatalf("%+v", web.opts)
		}
		if len(*calls) != 1 || (*calls)[0].Name != "ffmpeg" || !reflect.DeepEqual((*calls)[0].Args, []string{"-ss", "1", "-i", info.AbsolutePath, "-frames:v", "1", "-f", "image2pipe", "-vcodec", "mjpeg", "pipe:1"}) {
			t.Fatalf("%+v", *calls)
		}
		if (*calls)[0].Opts != (mediaOptions{Timeout: 10 * time.Second, MaxBuffer: 5 * 1024 * 1024}) {
			t.Fatalf("%+v", (*calls)[0].Opts)
		}
	})
	t.Run("the title falls back to the file name", func(t *testing.T) {
		_, agentDir := geminiEnv(t)
		info := writeVideo(t, agentDir, "holiday.MOV", "video/quicktime", "x")
		t.Cleanup(SetGeminiWebClient(&fakeVideoWeb{cookies: GeminiWebCookies{"c": "v"}, answer: "plain"}))
		noMediaPrograms(t)
		r := ExtractContent(bg(), info.AbsolutePath, ExtractOptions{})
		if r.Title != "holiday" || r.Thumbnail != nil {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("no path reading the video names the two ways to get access", func(t *testing.T) {
		_, agentDir := geminiEnv(t)
		info := writeVideo(t, agentDir, "clip.mp4", "video/mp4", "x")
		noMediaPrograms(t)
		want := "Video analysis requires Gemini access. Either:\n  1. Sign into gemini.google.com in Chrome (free, uses cookies)\n  2. Set GEMINI_API_KEY in " + ConfigPath()
		if got := errOf(ExtractContent(bg(), info.AbsolutePath, ExtractOptions{})); got != want {
			t.Fatalf("%q", got)
		}
	})
	t.Run("a failed upload still deletes nothing and falls to Gemini Web", func(t *testing.T) {
		_, agentDir := geminiEnv(t)
		t.Setenv("GEMINI_API_KEY", "synthetic-gemini-key")
		info := writeVideo(t, agentDir, "clip.mp4", "video/mp4", "x")
		web := &fakeVideoWeb{cookies: GeminiWebCookies{"c": "v"}, answer: "# Web"}
		t.Cleanup(SetGeminiWebClient(web))
		noMediaPrograms(t)
		n := useNet(t, func(netCall) netReply { return reply(500, "down") })
		r := ExtractContent(bg(), info.AbsolutePath, ExtractOptions{})
		pendingGeminiDeletes.Wait()
		if r.Title != "Web" || n.count() != 1 {
			t.Fatalf("%+v %v", r, n.urls())
		}
	})
	t.Run("an uploaded file is deleted after a cancelled analysis", func(t *testing.T) {
		_, agentDir := geminiEnv(t)
		t.Setenv("GEMINI_API_KEY", "synthetic-gemini-key")
		info := writeVideo(t, agentDir, "clip.mp4", "video/mp4", "x")
		noMediaPrograms(t)
		ctx, cancel := context.WithCancel(bg())
		inner := geminiFilesAPI("https://generativelanguage.googleapis.com", "# x")
		n := useNet(t, func(c netCall) netReply {
			if strings.Contains(c.URL, ":generateContent") {
				cancel()
				return netReply{Err: context.Canceled}
			}
			return inner(c)
		})
		r := ExtractContent(ctx, info.AbsolutePath, ExtractOptions{})
		pendingGeminiDeletes.Wait()
		if got := errOf(r); got != "Aborted" {
			t.Fatal(got)
		}
		if last := n.calls[len(n.calls)-1]; last.Method != "DELETE" {
			t.Fatalf("%v", n.urls())
		}
	})
	t.Run("a slow file is polled until it is active", func(t *testing.T) {
		_, agentDir := geminiEnv(t)
		t.Setenv("GEMINI_API_KEY", "synthetic-gemini-key")
		info := writeVideo(t, agentDir, "clip.mp4", "video/mp4", "x")
		noMediaPrograms(t)
		prev := geminiFilePollInterval
		geminiFilePollInterval = time.Millisecond
		t.Cleanup(func() { geminiFilePollInterval = prev })
		inner := geminiFilesAPI("https://generativelanguage.googleapis.com", "# Slow")
		polls := 0
		n := useNet(t, func(c netCall) netReply {
			if c.Method == "GET" && strings.HasSuffix(c.URL, "/files/synthetic") {
				if polls++; polls < 3 {
					return jsonReply(map[string]any{"state": "PROCESSING"})
				}
			}
			return inner(c)
		})
		r := ExtractContent(bg(), info.AbsolutePath, ExtractOptions{})
		pendingGeminiDeletes.Wait()
		if r.Title != "Slow" || polls != 3 || n.count() != 7 {
			t.Fatalf("%+v %d %v", r, polls, n.urls())
		}
	})
}

func TestFetchToolShowsFrames(t *testing.T) {
	r, _ := newRuntime(t, "")
	dir := filepath.Dir(ConfigPath())
	info := writeVideo(t, dir, "clip.mp4", "video/mp4", "x")
	useMedia(t, func(name string, args []string) ([]byte, error) {
		if name == "ffprobe" {
			return []byte("30.5\n"), nil
		}
		return []byte("JPEG-" + args[1]), nil
	})
	out, err := execTool(t, r, "fetch_content", map[string]any{"url": info.AbsolutePath, "frames": 3.0})
	noErr(t, err)
	var kinds []string
	for _, b := range out.Content {
		kinds = append(kinds, b.Type+":"+b.Text)
	}
	want := []string{"image:", "text:Frame at 0:00", "image:", "text:Frame at 0:15", "image:", "text:Frame at 0:30"}
	if !reflect.DeepEqual(kinds[:6], want) || !strings.HasPrefix(kinds[6], "text:3 frames extracted from 0:00-0:30") {
		t.Fatalf("%q", kinds)
	}
	if out.Details["imageCount"] != 3 || out.Details["hasImage"] != true || out.Details["frames"] != 3 || out.Details["duration"] != 30.5 {
		t.Fatalf("%v", out.Details)
	}
}

func TestYouTubeProgramOptions(t *testing.T) {
	geminiEnv(t)
	calls := useMedia(t, func(name string, _ []string) ([]byte, error) {
		if name == "yt-dlp" {
			return []byte("100\nhttps://stream.example/v\n"), nil
		}
		return []byte("JPEG"), nil
	})
	r := ExtractContent(bg(), "https://www.youtube.com/watch?v=dQw4w9WgXcQ", ExtractOptions{Lookup: pubLookup, Timestamp: "10"})
	if r.Error != nil || r.Thumbnail == nil {
		t.Fatalf("%+v", r)
	}
	want := []mediaCall{
		{"yt-dlp", []string{"--print", "duration", "-g", "https://www.youtube.com/watch?v=dQw4w9WgXcQ"}, mediaOptions{Timeout: 15 * time.Second}},
		{"ffmpeg", []string{"-ss", "10", "-i", "https://stream.example/v", "-frames:v", "1", "-f", "image2pipe", "-vcodec", "mjpeg", "pipe:1"}, mediaOptions{Timeout: 30 * time.Second, MaxBuffer: 5 * 1024 * 1024}},
	}
	if !reflect.DeepEqual(*calls, want) {
		t.Fatalf("%+v", *calls)
	}
	*calls = nil
	_, agentDir := geminiEnv(t)
	info := writeVideo(t, agentDir, "clip.mp4", "video/mp4", "x")
	_ = ExtractContent(bg(), info.AbsolutePath, ExtractOptions{Frames: 2})
	wantProbe := mediaCall{"ffprobe", []string{"-v", "quiet", "-show_entries", "format=duration", "-of", "csv=p=0", info.AbsolutePath}, mediaOptions{Timeout: 10 * time.Second}}
	if len(*calls) == 0 || !reflect.DeepEqual((*calls)[0], wantProbe) {
		t.Fatalf("%+v", *calls)
	}
}

func TestLocalVideoSizeLimit(t *testing.T) {
	_, agentDir := geminiEnv(t)
	writeConfig(t, agentDir, `{"video":{"maxSizeMB":1}}`)
	for size, within := range map[int64]bool{1024 * 1024: true, 1024*1024 + 1: false} {
		p := filepath.Join(agentDir, "clip.webm")
		noErr(t, os.WriteFile(p, nil, 0o600))
		noErr(t, os.Truncate(p, size))
		info, err := IsVideoFile(p)
		noErr(t, err)
		if info == nil || info.WithinUploadLimit != within || info.MimeType != "video/webm" || info.SizeBytes != size {
			t.Fatalf("%d: %+v", size, info)
		}
	}
	for _, in := range []string{"relative.mp4", "https://example.com/a.mp4", "file:///does/not/exist.mp4", agentDir, filepath.Join(agentDir, "clip.txt")} {
		if info, err := IsVideoFile(in); err != nil || info != nil {
			t.Errorf("%s: %+v %v", in, info, err)
		}
	}
	for ext, mime := range map[string]string{".mp4": "video/mp4", ".mov": "video/quicktime", ".avi": "video/x-msvideo", ".mpeg": "video/mpeg", ".mpg": "video/mpeg", ".wmv": "video/x-ms-wmv", ".flv": "video/x-flv", ".3gp": "video/3gpp", ".3GPP": "video/3gpp"} {
		p := filepath.Join(agentDir, "v"+ext)
		noErr(t, os.WriteFile(p, []byte("x"), 0o600))
		if info, _ := IsVideoFile(p); info == nil || info.MimeType != mime {
			t.Errorf("%s: %+v", ext, info)
		}
	}
	noErr(t, os.WriteFile(filepath.Join(agentDir, ".mp4"), []byte("x"), 0o600))
	if info, _ := IsVideoFile(filepath.Join(agentDir, ".mp4")); info != nil {
		t.Error("a dotfile has no extension")
	}
	for _, v := range []string{`{"video":{"maxSizeMB":"big"}}`, `{"video":{"maxSizeMB":-3}}`, `{"video":{"maxSizeMB":0}}`} {
		writeConfig(t, agentDir, v)
		p := filepath.Join(agentDir, "big.mp4")
		noErr(t, os.WriteFile(p, nil, 0o600))
		noErr(t, os.Truncate(p, 10*1024*1024))
		if info, _ := IsVideoFile(p); info == nil || !info.WithinUploadLimit || info.MaxSizeBytes != 50*1024*1024 {
			t.Errorf("%s: %+v", v, info)
		}
	}
}
