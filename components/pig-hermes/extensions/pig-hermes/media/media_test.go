package media

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/config"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/paths"
)

func sampleTools() []sdk.ToolInfo {
	return []sdk.ToolInfo{
		{
			Name:        "web_search",
			Description: "Search the web via Tavily, Brave, or SearXNG",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`),
			Source:      "pig-hermes",
		},
		{
			Name:        "image_generate",
			Description: "Generate an image from a text prompt and save PNG output",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string"},"size":{"type":"string"}},"required":["prompt"]}`),
			Source:      "pig-hermes",
		},
		{
			Name:        "vision_analyze",
			Description: "Analyze an image file or URL with a vision model",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"image_path":{"type":"string"},"question":{"type":"string"}},"required":["image_path","question"]}`),
			Source:      "pig-hermes",
		},
		{
			Name:             "browser_exec",
			Description:      "Drive a browser via the browser-use CLI",
			Parameters:       json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}`),
			PromptGuidelines: []string{"Close browser sessions when finished."},
			Source:           "pig-hermes",
		},
		{
			Name:        "text_to_speech",
			Description: "Synthesize speech audio from text",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`),
			Source:      "pig-hermes",
		},
	}
}

func TestRegisterAttachesAllSevenTools(t *testing.T) {
	ext := sdk.New("test-media")
	p := paths.ProfilePaths{CacheDir: t.TempDir()}
	Register(ext, p, config.Default())

	// Nil extension should be a safe no-op.
	Register(nil, p, nil)
}

func TestToolSearchRankingAndLimit(t *testing.T) {
	fetch := func() ([]sdk.ToolInfo, error) { return sampleTools(), nil }

	res := handleToolSearch(map[string]any{
		"query": "image",
		"limit": 2,
	}, fetch)
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	sr, ok := res.StructuredContent.(ToolSearchResult)
	if !ok {
		t.Fatalf("expected ToolSearchResult, got %T", res.StructuredContent)
	}
	if sr.TotalAvailable != 5 {
		t.Fatalf("expected 5 total available, got %d", sr.TotalAvailable)
	}
	if len(sr.Matches) != 2 {
		t.Fatalf("expected 2 matches, got %d: %+v", len(sr.Matches), sr.Matches)
	}
	// image_generate has "image" in its name, so it must outrank vision_analyze.
	if sr.Matches[0].Name != "image_generate" {
		t.Fatalf("expected image_generate first, got %q (all: %+v)", sr.Matches[0].Name, sr.Matches)
	}
	if sr.Matches[1].Name != "vision_analyze" {
		t.Fatalf("expected vision_analyze second, got %q", sr.Matches[1].Name)
	}
	if len(sr.Matches[0].Required) != 1 || sr.Matches[0].Required[0] != "prompt" {
		t.Fatalf("expected required=[prompt], got %v", sr.Matches[0].Required)
	}
}

func TestToolSearchValidationAndCompatQueries(t *testing.T) {
	fetch := func() ([]sdk.ToolInfo, error) { return sampleTools(), nil }

	res := handleToolSearch(map[string]any{}, fetch)
	if !res.IsError {
		t.Fatalf("expected error on missing query")
	}

	res = handleToolSearch(map[string]any{"queries": []any{"browser", "cli"}}, fetch)
	if res.IsError {
		t.Fatalf("unexpected error with queries compat arg: %s", res.Content)
	}
	sr := res.StructuredContent.(ToolSearchResult)
	if len(sr.Matches) == 0 || sr.Matches[0].Name != "browser_exec" {
		t.Fatalf("expected browser_exec match, got %+v", sr.Matches)
	}
}

func TestToolDescribeSingleAndArrayAndNotFound(t *testing.T) {
	fetch := func() ([]sdk.ToolInfo, error) { return sampleTools(), nil }

	res := handleToolDescribe(map[string]any{
		"names": []any{"browser_exec", "nonexistent_tool"},
		"name":  "web_search",
	}, fetch)
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	dr, ok := res.StructuredContent.(ToolDescribeResult)
	if !ok {
		t.Fatalf("expected ToolDescribeResult, got %T", res.StructuredContent)
	}
	if len(dr.Tools) != 2 {
		t.Fatalf("expected 2 described tools, got %d", len(dr.Tools))
	}
	if dr.Tools[0].Name != "browser_exec" || len(dr.Tools[0].PromptGuidelines) != 1 {
		t.Fatalf("unexpected first described tool: %+v", dr.Tools[0])
	}
	if dr.Tools[1].Name != "web_search" {
		t.Fatalf("expected web_search second, got %q", dr.Tools[1].Name)
	}
	if len(dr.NotFound) != 1 || dr.NotFound[0] != "nonexistent_tool" {
		t.Fatalf("expected not_found=[nonexistent_tool], got %v", dr.NotFound)
	}

	errRes := handleToolDescribe(map[string]any{}, fetch)
	if !errRes.IsError {
		t.Fatalf("expected error when neither name nor names provided")
	}
}

func TestToolCallDelegatesAndBlocksRecursion(t *testing.T) {
	called := false
	execFn := func(name string, args any, options *sdk.ExecuteToolOptions) (sdk.AgentToolCallOutcome, error) {
		called = true
		m, ok := args.(map[string]any)
		if !ok || m["query"] != "golang" {
			t.Fatalf("unexpected args passed to ExecuteTool: %#v", args)
		}
		dur := int64(12)
		return sdk.AgentToolCallOutcome{
			Result: sdk.AgentToolResult{
				Content: []sdk.ToolResultContent{
					{Type: "text", Text: "search ok"},
				},
			},
			DurationMs: &dur,
		}, nil
	}

	// Recursion guard must reject tool_call without invoking ExecuteTool.
	recRes := handleToolCall(map[string]any{
		"name":      "tool_call",
		"arguments": map[string]any{"name": "web_search"},
	}, execFn)
	if !recRes.IsError || !strings.Contains(recRes.Content, "recursive") {
		t.Fatalf("expected recursion guard error, got: %+v", recRes)
	}
	if called {
		t.Fatalf("ExecuteTool should not be called when recursion guard triggers")
	}

	// Case-insensitive recursion check.
	recRes2 := handleToolCall(map[string]any{"name": "TOOL_CALL"}, execFn)
	if !recRes2.IsError {
		t.Fatalf("expected recursion guard error for uppercase TOOL_CALL")
	}

	// Normal delegation succeeds.
	okRes := handleToolCall(map[string]any{
		"name":      "web_search",
		"arguments": map[string]any{"query": "golang"},
	}, execFn)
	if okRes.IsError || okRes.Content != "search ok" {
		t.Fatalf("unexpected tool_call result: %+v", okRes)
	}
	if !called {
		t.Fatalf("expected ExecuteTool to be invoked")
	}

	// Host error surfaces cleanly.
	errExec := func(name string, args any, options *sdk.ExecuteToolOptions) (sdk.AgentToolCallOutcome, error) {
		return sdk.AgentToolCallOutcome{}, errors.New("host failure")
	}
	failRes := handleToolCall(map[string]any{"name": "web_search"}, errExec)
	if !failRes.IsError || !strings.Contains(failRes.Content, "host failure") {
		t.Fatalf("expected host error to propagate, got: %+v", failRes)
	}
}

func TestBrowserExecWithFakeCLI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fake CLI test runs on POSIX")
	}

	dir := t.TempDir()
	fakeCLI := filepath.Join(dir, "fake-browser-use")
	script := `#!/bin/sh
echo "SESSION=$BU_NAME"
echo "ARGS=$*"
echo "warn-line" >&2
if [ "$1" = "fail" ]; then
  exit 7
fi
if [ "$1" = "sleep" ]; then
  sleep 10
fi
exit 0
`
	if err := os.WriteFile(fakeCLI, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake cli: %v", err)
	}

	t.Setenv("HERMES_BROWSER_CLI", fakeCLI)

	// Happy path with session and quoted argument.
	res := handleBrowserExec(context.Background(), map[string]any{
		"command": `type 4 "hello world"`,
		"session": "sess_1",
	}, config.Default(), 10)
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	br, ok := res.StructuredContent.(BrowserExecResult)
	if !ok {
		t.Fatalf("expected BrowserExecResult, got %T", res.StructuredContent)
	}
	if br.Status != "ok" || br.ExitCode != 0 {
		t.Fatalf("unexpected status/exit: %+v", br)
	}
	if !strings.Contains(br.Stdout, "SESSION=sess_1") || !strings.Contains(br.Stdout, "ARGS=--session sess_1 type 4 hello world") {
		t.Fatalf("unexpected stdout:\n%s", br.Stdout)
	}
	if !strings.Contains(br.Stderr, "warn-line") {
		t.Fatalf("unexpected stderr:\n%s", br.Stderr)
	}

	// Non-zero exit code.
	failRes := handleBrowserExec(context.Background(), map[string]any{
		"command": "fail",
	}, config.Default(), 10)
	if !failRes.IsError {
		t.Fatalf("expected error on non-zero exit")
	}
	failBR := failRes.StructuredContent.(BrowserExecResult)
	if failBR.ExitCode != 7 || failBR.Status != "error" {
		t.Fatalf("expected exit code 7, got %+v", failBR)
	}

	// Timeout cleans up process group cleanly.
	toCtx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	toRes := handleBrowserExec(toCtx, map[string]any{
		"command": "sleep",
	}, config.Default(), 1)
	if !toRes.IsError {
		t.Fatalf("expected timeout error")
	}
	toBR := toRes.StructuredContent.(BrowserExecResult)
	if toBR.Status != "timeout" {
		t.Fatalf("expected timeout status, got %+v", toBR)
	}
}

func TestBrowserExecMissingCLIGivesInstallHint(t *testing.T) {
	t.Setenv("HERMES_BROWSER_CLI", "definitely-not-a-real-browser-cli-xyz123")
	res := handleBrowserExec(context.Background(), map[string]any{
		"command": "open https://example.com",
	}, config.Default(), 10)
	if !res.IsError {
		t.Fatalf("expected error when CLI is missing")
	}
	if !strings.Contains(res.Content, "not found on PATH") || !strings.Contains(res.Content, "browser-use") {
		t.Fatalf("expected install hint in error, got:\n%s", res.Content)
	}
}

func TestVisionAnalyzeLocalFileAndMissingKey(t *testing.T) {
	// Create a minimal valid 1x1 PNG file.
	pngBytes, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatalf("decode fixture png: %v", err)
	}
	imgPath := filepath.Join(t.TempDir(), "sample.png")
	if err := os.WriteFile(imgPath, pngBytes, 0o600); err != nil {
		t.Fatalf("write png: %v", err)
	}

	// Missing API key produces clear diagnostic.
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("HERMES_VISION_API_KEY", "")
	missRes := handleVisionAnalyze(context.Background(), map[string]any{
		"image_path": imgPath,
		"question":   "What is in this image?",
	}, config.Default())
	if !missRes.IsError || !strings.Contains(missRes.Content, "OPENAI_API_KEY") {
		t.Fatalf("expected missing-key diagnostic, got: %+v", missRes)
	}

	// Mock OpenAI chat completions vision endpoint.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Method != http.MethodPost {
			http.Error(w, "bad route", http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-vision-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Content []struct {
					Type     string `json:"type"`
					Text     string `json:"text"`
					ImageURL struct {
						URL string `json:"url"`
					} `json:"image_url"`
				} `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.Model != "gpt-4o" {
			http.Error(w, "unexpected model", http.StatusBadRequest)
			return
		}
		if len(body.Messages) != 1 || len(body.Messages[0].Content) != 2 {
			http.Error(w, "unexpected message structure", http.StatusBadRequest)
			return
		}
		imgURL := body.Messages[0].Content[1].ImageURL.URL
		if !strings.HasPrefix(imgURL, "data:image/png;base64,") {
			http.Error(w, "expected base64 png data url, got "+imgURL, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"gpt-4o","choices":[{"message":{"content":"A 1x1 transparent pixel."}}]}`))
	}))
	defer srv.Close()

	t.Setenv("OPENAI_API_KEY", "test-vision-key")
	t.Setenv("HERMES_VISION_BASE_URL", srv.URL)

	res := handleVisionAnalyze(context.Background(), map[string]any{
		"image_path": imgPath,
		"question":   "Describe this image.",
		"model":      "gpt-4o",
	}, config.Default())
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	vr, ok := res.StructuredContent.(VisionAnalyzeResult)
	if !ok {
		t.Fatalf("expected VisionAnalyzeResult, got %T", res.StructuredContent)
	}
	if !vr.Success || vr.Analysis != "A 1x1 transparent pixel." || vr.MimeType != "image/png" {
		t.Fatalf("unexpected vision result: %+v", vr)
	}
}

func TestImageGenerateB64AndURL(t *testing.T) {
	wantPNG := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0x00, 0x01, 0x02}
	b64PNG := base64.StdEncoding.EncodeToString(wantPNG)

	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/images/generations":
			if r.Header.Get("Authorization") != "Bearer test-img-key" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			var req struct {
				Prompt string `json:"prompt"`
				Size   string `json:"size"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if req.Prompt == "url-mode" {
				_, _ = w.Write([]byte(`{"data":[{"url":"` + srvURL + `/download/img.png","revised_prompt":"revised url"}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":[{"b64_json":"` + b64PNG + `","revised_prompt":"a cozy cabin"}]}`))
		case "/download/img.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(wantPNG)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()
	srvURL = srv.URL

	t.Setenv("OPENAI_API_KEY", "test-img-key")
	t.Setenv("HERMES_IMAGE_BASE_URL", srv.URL)

	cacheDir := t.TempDir()
	p := paths.ProfilePaths{CacheDir: cacheDir}

	// b64_json mode saving to default <CacheDir>/images/
	res := handleImageGenerate(context.Background(), map[string]any{
		"prompt": "cozy cabin in snow",
		"size":   "512x512",
	}, p, config.Default())
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	ir, ok := res.StructuredContent.(ImageGenerateResult)
	if !ok {
		t.Fatalf("expected ImageGenerateResult, got %T", res.StructuredContent)
	}
	if !ir.Success || ir.Size != "512x512" || ir.RevisedPrompt != "a cozy cabin" {
		t.Fatalf("unexpected image result: %+v", ir)
	}
	if !strings.HasPrefix(ir.OutputPath, filepath.Join(cacheDir, "images")) {
		t.Fatalf("expected output under cacheDir/images, got %s", ir.OutputPath)
	}
	gotBytes, err := os.ReadFile(ir.OutputPath)
	if err != nil {
		t.Fatalf("read saved image: %v", err)
	}
	if !bytes.Equal(gotBytes, wantPNG) {
		t.Fatalf("saved image bytes mismatch")
	}

	// URL download mode saving to explicit output_path
	explicitOut := filepath.Join(t.TempDir(), "nested", "custom.png")
	res2 := handleImageGenerate(context.Background(), map[string]any{
		"prompt":      "url-mode",
		"output_path": explicitOut,
	}, p, config.Default())
	if res2.IsError {
		t.Fatalf("unexpected url-mode error: %s", res2.Content)
	}
	ir2 := res2.StructuredContent.(ImageGenerateResult)
	if ir2.OutputPath != explicitOut {
		t.Fatalf("expected output path %s, got %s", explicitOut, ir2.OutputPath)
	}
	gotBytes2, err := os.ReadFile(explicitOut)
	if err != nil || !bytes.Equal(gotBytes2, wantPNG) {
		t.Fatalf("url-mode image bytes mismatch: err=%v", err)
	}
}

func TestTextToSpeechOpenAI(t *testing.T) {
	wantAudio := []byte("ID3\x03\x00\x00\x00\x00\x00\x00fake-mp3-audio-payload")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/speech" || r.Method != http.MethodPost {
			http.Error(w, "bad route", http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-tts-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var req struct {
			Model          string `json:"model"`
			Input          string `json:"input"`
			Voice          string `json:"voice"`
			ResponseFormat string `json:"response_format"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Input != "Hello Hermes" || req.Voice != "nova" || req.ResponseFormat != "mp3" {
			http.Error(w, "unexpected tts request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write(wantAudio)
	}))
	defer srv.Close()

	t.Setenv("OPENAI_API_KEY", "test-tts-key")
	t.Setenv("HERMES_TTS_BASE_URL", srv.URL)

	outPath := filepath.Join(t.TempDir(), "hello.mp3")
	res := handleTextToSpeech(context.Background(), map[string]any{
		"text":        "Hello Hermes",
		"voice":       "nova",
		"output_path": outPath,
		"provider":    "auto",
	}, paths.ProfilePaths{CacheDir: t.TempDir()}, config.Default())

	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	tr, ok := res.StructuredContent.(TextToSpeechResult)
	if !ok {
		t.Fatalf("expected TextToSpeechResult, got %T", res.StructuredContent)
	}
	if !tr.Success || tr.Provider != "openai" || tr.Voice != "nova" || tr.OutputPath != outPath {
		t.Fatalf("unexpected tts result: %+v", tr)
	}
	if tr.MediaTag != "MEDIA:"+outPath {
		t.Fatalf("unexpected media tag: %q", tr.MediaTag)
	}
	gotAudio, err := os.ReadFile(outPath)
	if err != nil || !bytes.Equal(gotAudio, wantAudio) {
		t.Fatalf("audio bytes mismatch: err=%v", err)
	}
}

func TestTextToSpeechMissingKeyOpenAIProvider(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("HERMES_TTS_API_KEY", "")
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("HERMES_TTS_BASE_URL", "")

	res := handleTextToSpeech(context.Background(), map[string]any{
		"text":     "Hello",
		"provider": "openai",
	}, paths.ProfilePaths{CacheDir: t.TempDir()}, config.Default())
	if !res.IsError || !strings.Contains(res.Content, "OPENAI_API_KEY") {
		t.Fatalf("expected missing key error for openai provider, got: %+v", res)
	}
}
