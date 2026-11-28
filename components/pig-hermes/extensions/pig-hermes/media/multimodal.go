package media

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/config"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/paths"
)

const (
	maxVisionImageBytes   = 20 * 1024 * 1024 // 20 MB hard cap
	maxDownloadedMedia    = 50 * 1024 * 1024 // 50 MB download cap
	defaultVisionModel    = "gpt-4o-mini"
	defaultImageModel     = "gpt-image-1"
	defaultImageSize      = "1024x1024"
	defaultTTSModel       = "tts-1"
	defaultTTSVoice       = "alloy"
	defaultMediaTimeout   = 90 * time.Second
	defaultSayTimeout     = 60 * time.Second
)

// VisionAnalyzeResult is the structured result returned by vision_analyze.
type VisionAnalyzeResult struct {
	Success   bool   `json:"success"`
	Analysis  string `json:"analysis,omitempty"`
	Model     string `json:"model,omitempty"`
	ImagePath string `json:"image_path,omitempty"`
	MimeType  string `json:"mime_type,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
	Error     string `json:"error,omitempty"`
}

// ImageGenerateResult is the structured result returned by image_generate.
type ImageGenerateResult struct {
	Success       bool   `json:"success"`
	OutputPath    string `json:"output_path,omitempty"`
	Model         string `json:"model,omitempty"`
	Size          string `json:"size,omitempty"`
	SizeBytes     int    `json:"size_bytes,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
	Error         string `json:"error,omitempty"`
}

// TextToSpeechResult is the structured result returned by text_to_speech.
type TextToSpeechResult struct {
	Success    bool   `json:"success"`
	OutputPath string `json:"output_path,omitempty"`
	MediaTag   string `json:"media_tag,omitempty"`
	Provider   string `json:"provider,omitempty"`
	Voice      string `json:"voice,omitempty"`
	SizeBytes  int    `json:"size_bytes,omitempty"`
	Error      string `json:"error,omitempty"`
}

func registerVisionAnalyzeTool(ext *sdk.Extension, cfg *config.Config) {
	ext.RegisterTool(sdk.ToolDefinition{
		Name:          "vision_analyze",
		Label:         "Vision Analyze",
		Description:   "Analyze a local image file or http(s) image URL with an OpenAI-compatible vision model and answer a question about it.",
		PromptSnippet: "Analyze an image with a vision-capable model",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"image_path": map[string]any{
					"type":        "string",
					"description": "Local file path or http(s) URL to the image to analyze.",
				},
				"question": map[string]any{
					"type":        "string",
					"description": "Question or instruction describing what to analyze in the image.",
				},
				"model": map[string]any{
					"type":        "string",
					"description": "Optional vision model override (defaults to HERMES_VISION_MODEL or gpt-4o-mini).",
				},
			},
			"required": []string{"image_path", "question"},
		},
		Execute: func(sdkCtx sdk.Context, params map[string]any) (any, error) {
			ctx, cancel := contextFromSDK(sdkCtx, defaultMediaTimeout)
			defer cancel()
			return handleVisionAnalyze(ctx, params, cfg), nil
		},
	})
}

func registerImageGenerateTool(ext *sdk.Extension, p paths.ProfilePaths, cfg *config.Config) {
	ext.RegisterTool(sdk.ToolDefinition{
		Name:          "image_generate",
		Label:         "Image Generate",
		Description:   "Generate an image via an OpenAI-compatible /v1/images/generations endpoint and save it atomically to disk.",
		PromptSnippet: "Generate an image from a text prompt",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"prompt": map[string]any{
					"type":        "string",
					"description": "Text prompt describing the image to generate.",
				},
				"size": map[string]any{
					"type":        "string",
					"description": "Output image dimensions (default '1024x1024').",
					"default":     defaultImageSize,
				},
				"model": map[string]any{
					"type":        "string",
					"description": "Optional image generation model override (defaults to HERMES_IMAGE_MODEL or gpt-image-1).",
				},
				"output_path": map[string]any{
					"type":        "string",
					"description": "Optional destination path for the generated PNG. Defaults to <cache>/images/image_<timestamp>.png.",
				},
			},
			"required": []string{"prompt"},
		},
		Execute: func(sdkCtx sdk.Context, params map[string]any) (any, error) {
			ctx, cancel := contextFromSDK(sdkCtx, defaultMediaTimeout)
			defer cancel()
			return handleImageGenerate(ctx, params, p, cfg), nil
		},
	})
}

func registerTextToSpeechTool(ext *sdk.Extension, p paths.ProfilePaths, cfg *config.Config) {
	ext.RegisterTool(sdk.ToolDefinition{
		Name:          "text_to_speech",
		Label:         "Text To Speech",
		Description:   "Synthesize speech audio from text using an OpenAI-compatible /v1/audio/speech endpoint or macOS native `say` fallback.",
		PromptSnippet: "Convert text to a speech audio file",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{
					"type":        "string",
					"description": "Text to synthesize into speech.",
				},
				"voice": map[string]any{
					"type":        "string",
					"description": "Voice identifier (default 'alloy' for OpenAI, or system voice name for macos_say).",
					"default":     defaultTTSVoice,
				},
				"output_path": map[string]any{
					"type":        "string",
					"description": "Optional output file path. Defaults to <cache>/audio/tts_<timestamp>.mp3 (or .aiff for macos_say).",
				},
				"provider": map[string]any{
					"type":        "string",
					"description": "TTS provider: 'auto' (default), 'openai', or 'macos_say'.",
					"enum":        []string{"auto", "openai", "macos_say"},
					"default":     "auto",
				},
			},
			"required": []string{"text"},
		},
		Execute: func(sdkCtx sdk.Context, params map[string]any) (any, error) {
			ctx, cancel := contextFromSDK(sdkCtx, defaultMediaTimeout)
			defer cancel()
			return handleTextToSpeech(ctx, params, p, cfg), nil
		},
	})
}

func resolveConfigEnv(cfg *config.Config, keys ...string) string {
	for _, key := range keys {
		if cfg != nil {
			if v := strings.TrimSpace(cfg.Getenv(key)); v != "" {
				return v
			}
		} else if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return ""
}

func detectImageMIME(path string, data []byte) string {
	// Check magic bytes first via http.DetectContentType.
	if len(data) > 0 {
		detected := http.DetectContentType(data)
		if strings.HasPrefix(detected, "image/") {
			return detected
		}
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".svg":
		return "image/svg+xml"
	default:
		return "application/octet-stream"
	}
}

func isHTTPURL(s string) bool {
	low := strings.ToLower(strings.TrimSpace(s))
	return strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://")
}

func handleVisionAnalyze(ctx context.Context, params map[string]any, cfg *config.Config) sdk.ToolResult {
	imageRef := strings.TrimSpace(stringParam(params, "image_path"))
	if imageRef == "" {
		// Accept upstream compat_arg "image_url".
		imageRef = strings.TrimSpace(stringParam(params, "image_url"))
	}
	if imageRef == "" {
		return toolErrorResult("vision_analyze: image_path is required")
	}

	question := strings.TrimSpace(stringParam(params, "question"))
	if question == "" {
		return toolErrorResult("vision_analyze: question is required")
	}

	apiKey := resolveConfigEnv(cfg, "HERMES_VISION_API_KEY", "OPENAI_API_KEY")
	baseURL := resolveConfigEnv(cfg, "HERMES_VISION_BASE_URL", "OPENAI_BASE_URL")
	if apiKey == "" {
		msg := "vision_analyze: missing OPENAI_API_KEY (or HERMES_VISION_API_KEY). Set OPENAI_API_KEY in environment or ~/.hermes/.env to enable vision analysis."
		return jsonToolResult(VisionAnalyzeResult{
			Success:   false,
			ImagePath: imageRef,
			Error:     msg,
		}, true)
	}

	model := strings.TrimSpace(stringParam(params, "model"))
	if model == "" {
		model = resolveConfigEnv(cfg, "HERMES_VISION_MODEL")
	}
	if model == "" {
		model = defaultVisionModel
	}

	var imageURLForAPI string
	var mimeType string
	var sizeBytes int64

	if isHTTPURL(imageRef) || strings.HasPrefix(strings.ToLower(imageRef), "data:") {
		imageURLForAPI = imageRef
	} else {
		localPath := expandHome(imageRef)
		info, err := os.Stat(localPath)
		if err != nil {
			return toolErrorResult(fmt.Sprintf("vision_analyze: stat image %q: %v", imageRef, err))
		}
		if info.IsDir() {
			return toolErrorResult(fmt.Sprintf("vision_analyze: %q is a directory, not an image file", imageRef))
		}
		if info.Size() > maxVisionImageBytes {
			return toolErrorResult(fmt.Sprintf("vision_analyze: image %q is %d bytes, exceeding the 20 MB limit", imageRef, info.Size()))
		}
		raw, err := os.ReadFile(localPath)
		if err != nil {
			return toolErrorResult(fmt.Sprintf("vision_analyze: read image %q: %v", imageRef, err))
		}
		if int64(len(raw)) > maxVisionImageBytes {
			return toolErrorResult(fmt.Sprintf("vision_analyze: image %q exceeds the 20 MB limit", imageRef))
		}
		sizeBytes = int64(len(raw))
		mimeType = detectImageMIME(localPath, raw)
		if !strings.HasPrefix(mimeType, "image/") {
			return toolErrorResult(fmt.Sprintf("vision_analyze: %q is not a recognized image file (detected %s)", imageRef, mimeType))
		}
		encoded := base64.StdEncoding.EncodeToString(raw)
		imageURLForAPI = fmt.Sprintf("data:%s;base64,%s", mimeType, encoded)
	}

	reqBody := map[string]any{
		"model": model,
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "text", "text": question},
					{"type": "image_url", "image_url": map[string]any{"url": imageURLForAPI}},
				},
			},
		},
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return toolErrorResult(fmt.Sprintf("vision_analyze: marshal request: %v", err))
	}

	endpoint := apiEndpointURL(baseURL, "/v1/chat/completions")
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return toolErrorResult(fmt.Sprintf("vision_analyze: build request: %v", err))
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: defaultMediaTimeout}
	resp, err := client.Do(httpReq)
	if err != nil {
		return toolErrorResult(fmt.Sprintf("vision_analyze: request failed: %v", err))
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return toolErrorResult(fmt.Sprintf("vision_analyze: read response: %v", err))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return toolErrorResult(fmt.Sprintf("vision_analyze: API returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body))))
	}

	var apiResp struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content any `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return toolErrorResult(fmt.Sprintf("vision_analyze: decode response: %v", err))
	}
	if len(apiResp.Choices) == 0 {
		return toolErrorResult("vision_analyze: API returned no choices")
	}

	analysis := extractMessageContent(apiResp.Choices[0].Message.Content)
	usedModel := apiResp.Model
	if usedModel == "" {
		usedModel = model
	}

	res := VisionAnalyzeResult{
		Success:   true,
		Analysis:  analysis,
		Model:     usedModel,
		ImagePath: imageRef,
		MimeType:  mimeType,
		SizeBytes: sizeBytes,
	}
	return jsonToolResult(res, false)
}

func extractMessageContent(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var parts []string
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				if txt, ok := m["text"].(string); ok && txt != "" {
					parts = append(parts, txt)
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		return fmt.Sprint(v)
	}
}

func handleImageGenerate(ctx context.Context, params map[string]any, p paths.ProfilePaths, cfg *config.Config) sdk.ToolResult {
	prompt := strings.TrimSpace(stringParam(params, "prompt"))
	if prompt == "" {
		return toolErrorResult("image_generate: prompt is required")
	}

	apiKey := resolveConfigEnv(cfg, "HERMES_IMAGE_API_KEY", "OPENAI_API_KEY")
	baseURL := resolveConfigEnv(cfg, "HERMES_IMAGE_BASE_URL", "OPENAI_BASE_URL")
	if apiKey == "" {
		msg := "image_generate: missing OPENAI_API_KEY (or HERMES_IMAGE_API_KEY). Set OPENAI_API_KEY in environment or ~/.hermes/.env to enable image generation."
		return jsonToolResult(ImageGenerateResult{
			Success: false,
			Error:   msg,
		}, true)
	}

	size := strings.TrimSpace(stringParam(params, "size"))
	if size == "" {
		size = defaultImageSize
	}
	model := strings.TrimSpace(stringParam(params, "model"))
	if model == "" {
		model = resolveConfigEnv(cfg, "HERMES_IMAGE_MODEL")
	}
	if model == "" {
		model = defaultImageModel
	}

	outPath := strings.TrimSpace(stringParam(params, "output_path"))
	if outPath == "" {
		cacheDir := p.CacheDir
		if cacheDir == "" {
			cacheDir = filepath.Join(os.TempDir(), "hermes-cache")
		}
		outPath = filepath.Join(cacheDir, "images", fmt.Sprintf("image_%d.png", time.Now().UnixNano()))
	} else {
		outPath = expandHome(outPath)
	}
	if abs, err := filepath.Abs(outPath); err == nil {
		outPath = abs
	}

	reqBody := map[string]any{
		"model":           model,
		"prompt":          prompt,
		"size":            size,
		"n":               1,
		"response_format": "b64_json",
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return toolErrorResult(fmt.Sprintf("image_generate: marshal request: %v", err))
	}

	endpoint := apiEndpointURL(baseURL, "/v1/images/generations")
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return toolErrorResult(fmt.Sprintf("image_generate: build request: %v", err))
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: defaultMediaTimeout}
	resp, err := client.Do(httpReq)
	if err != nil {
		return toolErrorResult(fmt.Sprintf("image_generate: request failed: %v", err))
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDownloadedMedia))
	if err != nil {
		return toolErrorResult(fmt.Sprintf("image_generate: read response: %v", err))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return toolErrorResult(fmt.Sprintf("image_generate: API returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body))))
	}

	var apiResp struct {
		Data []struct {
			B64JSON       string `json:"b64_json"`
			URL           string `json:"url"`
			RevisedPrompt string `json:"revised_prompt"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return toolErrorResult(fmt.Sprintf("image_generate: decode response: %v", err))
	}
	if len(apiResp.Data) == 0 {
		return toolErrorResult("image_generate: API returned no image data")
	}

	item := apiResp.Data[0]
	var imgBytes []byte
	switch {
	case item.B64JSON != "":
		decoded, err := base64.StdEncoding.DecodeString(item.B64JSON)
		if err != nil {
			return toolErrorResult(fmt.Sprintf("image_generate: decode b64_json: %v", err))
		}
		imgBytes = decoded
	case item.URL != "":
		dlReq, err := http.NewRequestWithContext(ctx, http.MethodGet, item.URL, nil)
		if err != nil {
			return toolErrorResult(fmt.Sprintf("image_generate: build image download request: %v", err))
		}
		dlResp, err := client.Do(dlReq)
		if err != nil {
			return toolErrorResult(fmt.Sprintf("image_generate: download image: %v", err))
		}
		defer dlResp.Body.Close()
		if dlResp.StatusCode < 200 || dlResp.StatusCode >= 300 {
			return toolErrorResult(fmt.Sprintf("image_generate: image download returned HTTP %d", dlResp.StatusCode))
		}
		downloaded, err := io.ReadAll(io.LimitReader(dlResp.Body, maxDownloadedMedia))
		if err != nil {
			return toolErrorResult(fmt.Sprintf("image_generate: read downloaded image: %v", err))
		}
		imgBytes = downloaded
	default:
		return toolErrorResult("image_generate: response contained neither b64_json nor url")
	}

	if err := atomicWriteFile(outPath, imgBytes, 0o600); err != nil {
		return toolErrorResult(fmt.Sprintf("image_generate: save image: %v", err))
	}

	res := ImageGenerateResult{
		Success:       true,
		OutputPath:    outPath,
		Model:         model,
		Size:          size,
		SizeBytes:     len(imgBytes),
		RevisedPrompt: item.RevisedPrompt,
	}
	return jsonToolResult(res, false)
}

func handleTextToSpeech(ctx context.Context, params map[string]any, p paths.ProfilePaths, cfg *config.Config) sdk.ToolResult {
	text := strings.TrimSpace(stringParam(params, "text"))
	if text == "" {
		return toolErrorResult("text_to_speech: text is required")
	}

	provider := strings.ToLower(strings.TrimSpace(stringParam(params, "provider")))
	if provider == "" {
		provider = "auto"
	}
	if provider != "auto" && provider != "openai" && provider != "macos_say" {
		return toolErrorResult(fmt.Sprintf("text_to_speech: unsupported provider %q (expected 'auto', 'openai', or 'macos_say')", provider))
	}

	voice := strings.TrimSpace(stringParam(params, "voice"))
	apiKey := resolveConfigEnv(cfg, "HERMES_TTS_API_KEY", "OPENAI_API_KEY")
	baseURL := resolveConfigEnv(cfg, "HERMES_TTS_BASE_URL", "OPENAI_BASE_URL")

	selectedProvider := provider
	if selectedProvider == "auto" {
		if apiKey != "" || baseURL != "" {
			selectedProvider = "openai"
		} else if runtime.GOOS == "darwin" {
			if _, err := exec.LookPath("say"); err == nil {
				selectedProvider = "macos_say"
			}
		}
		if selectedProvider == "auto" {
			msg := "text_to_speech: no TTS backend available. Set OPENAI_API_KEY (and optional OPENAI_BASE_URL/HERMES_TTS_BASE_URL) for OpenAI TTS, or run on macOS with `say`."
			return jsonToolResult(TextToSpeechResult{
				Success: false,
				Error:   msg,
			}, true)
		}
	}

	outPath := strings.TrimSpace(stringParam(params, "output_path"))
	if outPath == "" {
		cacheDir := p.CacheDir
		if cacheDir == "" {
			cacheDir = filepath.Join(os.TempDir(), "hermes-cache")
		}
		ext := ".mp3"
		if selectedProvider == "macos_say" {
			ext = ".aiff"
		}
		outPath = filepath.Join(cacheDir, "audio", fmt.Sprintf("tts_%d%s", time.Now().UnixNano(), ext))
	} else {
		outPath = expandHome(outPath)
	}
	if abs, err := filepath.Abs(outPath); err == nil {
		outPath = abs
	}

	switch selectedProvider {
	case "openai":
		return synthesizeOpenAITTS(ctx, text, voice, outPath, apiKey, baseURL, cfg)
	case "macos_say":
		return synthesizeMacOSSay(ctx, text, voice, outPath)
	default:
		return toolErrorResult(fmt.Sprintf("text_to_speech: unsupported provider %q", selectedProvider))
	}
}

func synthesizeOpenAITTS(ctx context.Context, text, voice, outPath, apiKey, baseURL string, cfg *config.Config) sdk.ToolResult {
	if apiKey == "" {
		msg := "text_to_speech: missing OPENAI_API_KEY (or HERMES_TTS_API_KEY) for provider 'openai'."
		return jsonToolResult(TextToSpeechResult{
			Success:  false,
			Provider: "openai",
			Error:    msg,
		}, true)
	}
	if voice == "" {
		voice = defaultTTSVoice
	}
	model := resolveConfigEnv(cfg, "HERMES_TTS_MODEL")
	if model == "" {
		model = defaultTTSModel
	}

	format := "mp3"
	switch strings.ToLower(filepath.Ext(outPath)) {
	case ".opus", ".ogg":
		format = "opus"
	case ".wav":
		format = "wav"
	case ".flac":
		format = "flac"
	case ".aac":
		format = "aac"
	}

	reqBody := map[string]any{
		"model":           model,
		"input":           text,
		"voice":           voice,
		"response_format": format,
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return toolErrorResult(fmt.Sprintf("text_to_speech: marshal request: %v", err))
	}

	endpoint := apiEndpointURL(baseURL, "/v1/audio/speech")
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return toolErrorResult(fmt.Sprintf("text_to_speech: build request: %v", err))
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: defaultMediaTimeout}
	resp, err := client.Do(httpReq)
	if err != nil {
		return toolErrorResult(fmt.Sprintf("text_to_speech: request failed: %v", err))
	}
	defer resp.Body.Close()

	audioBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxDownloadedMedia))
	if err != nil {
		return toolErrorResult(fmt.Sprintf("text_to_speech: read audio response: %v", err))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return toolErrorResult(fmt.Sprintf("text_to_speech: API returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(audioBytes))))
	}

	if err := atomicWriteFile(outPath, audioBytes, 0o600); err != nil {
		return toolErrorResult(fmt.Sprintf("text_to_speech: save audio: %v", err))
	}

	res := TextToSpeechResult{
		Success:    true,
		OutputPath: outPath,
		MediaTag:   "MEDIA:" + outPath,
		Provider:   "openai",
		Voice:      voice,
		SizeBytes:  len(audioBytes),
	}
	return jsonToolResult(res, false)
}

func synthesizeMacOSSay(ctx context.Context, text, voice, outPath string) sdk.ToolResult {
	if runtime.GOOS != "darwin" {
		return jsonToolResult(TextToSpeechResult{
			Success:  false,
			Provider: "macos_say",
			Error:    "text_to_speech: provider 'macos_say' is only available on macOS (darwin)",
		}, true)
	}
	sayPath, err := exec.LookPath("say")
	if err != nil {
		return jsonToolResult(TextToSpeechResult{
			Success:  false,
			Provider: "macos_say",
			Error:    "text_to_speech: macOS `say` command not found on PATH",
		}, true)
	}

	dir := filepath.Dir(outPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return toolErrorResult(fmt.Sprintf("text_to_speech: create output dir: %v", err))
	}

	ext := filepath.Ext(outPath)
	if ext == "" {
		ext = ".aiff"
	}
	tmp, err := os.CreateTemp(dir, ".hermes-say-*"+ext)
	if err != nil {
		return toolErrorResult(fmt.Sprintf("text_to_speech: create temp audio file: %v", err))
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	args := make([]string, 0, 5)
	if voice != "" && voice != defaultTTSVoice {
		args = append(args, "-v", voice)
	}
	args = append(args, "-o", tmpPath, "--", text)

	sayCtx, cancel := context.WithTimeout(ctx, defaultSayTimeout)
	defer cancel()

	cmd := exec.Command(sayPath, args...)
	setProcGroup(cmd)
	cmd.WaitDelay = 2 * time.Second
	stderrBuf := newBoundedBuffer(8192)
	cmd.Stderr = stderrBuf

	if err := cmd.Start(); err != nil {
		return toolErrorResult(fmt.Sprintf("text_to_speech: start say: %v", err))
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	var waitErr error
	select {
	case waitErr = <-waitCh:
	case <-sayCtx.Done():
		killProcGroup(cmd)
		select {
		case waitErr = <-waitCh:
		case <-time.After(2 * time.Second):
		}
		if errors.Is(sayCtx.Err(), context.DeadlineExceeded) {
			return toolErrorResult("text_to_speech: macOS say timed out")
		}
		return toolErrorResult("text_to_speech: macOS say cancelled")
	}

	if waitErr != nil {
		stderrText, _ := stderrBuf.Result()
		return toolErrorResult(fmt.Sprintf("text_to_speech: say failed: %v (%s)", waitErr, strings.TrimSpace(stderrText)))
	}

	info, err := os.Stat(tmpPath)
	if err != nil {
		return toolErrorResult(fmt.Sprintf("text_to_speech: stat generated audio: %v", err))
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return toolErrorResult(fmt.Sprintf("text_to_speech: chmod audio: %v", err))
	}
	if err := os.Rename(tmpPath, outPath); err != nil {
		return toolErrorResult(fmt.Sprintf("text_to_speech: finalize audio file: %v", err))
	}
	cleanup = false

	usedVoice := voice
	if usedVoice == "" || usedVoice == defaultTTSVoice {
		usedVoice = "system"
	}
	res := TextToSpeechResult{
		Success:    true,
		OutputPath: outPath,
		MediaTag:   "MEDIA:" + outPath,
		Provider:   "macos_say",
		Voice:      usedVoice,
		SizeBytes:  int(info.Size()),
	}
	return jsonToolResult(res, false)
}
