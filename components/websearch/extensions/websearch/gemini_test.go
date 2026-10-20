package websearch

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// Twins of gemini-adc-auth, gemini-api-transport, gemini-pdf-extract and the Gemini cases of
// config-path, search-providers, provider-credential-source and all-provider. The upstream tests
// run a child process with a stubbed `fetch`; here `useNet` is the stub and an isolated temp home
// is the child's environment.

const (
	geminiTokenURL  = "https://oauth2.googleapis.com/token"
	geminiPublicAPI = "https://generativelanguage.googleapis.com/v1beta"
	geminiGatewayAI = "https://gateway.ai.cloudflare.com/v1/account/gateway/google-ai-studio"
)

// geminiEnv isolates the config and clears every variable the Gemini files read.
func geminiEnv(t *testing.T) (home, agentDir string) {
	t.Helper()
	home, agentDir = isolate(t)
	unsetenv(t, "GOOGLE_APPLICATION_CREDENTIALS", "GOOGLE_CLOUD_PROJECT", "GOOGLE_CLOUD_LOCATION", "GCLOUD_PROJECT",
		"PI_ALLOW_BROWSER_COOKIES", "FEYNMAN_ALLOW_BROWSER_COOKIES")
	clearGeminiADCTokenCache()
	t.Cleanup(clearGeminiADCTokenCache)
	return home, agentDir
}

func writeADC(t *testing.T, dir string, extra map[string]any) string {
	t.Helper()
	doc := map[string]any{"client_id": "synthetic-client-id", "client_secret": "synthetic-client-secret",
		"refresh_token": "synthetic-refresh-token", "type": "authorized_user", "universe_domain": "googleapis.com"}
	for k, v := range extra {
		doc[k] = v
	}
	b, _ := json.Marshal(doc)
	path := filepath.Join(dir, "adc.json")
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func geminiText(text string) netReply {
	return jsonReply(map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": text}}}}}})
}

func geminiSearch(ctx context.Context, q string) (*AttributedSearchResponse, error) {
	return Search(ctx, q, FullSearchOptions{Provider: ProviderSelection{Name: "gemini"}})
}

func credentialErr(err error) *CredentialResolutionError {
	var c *CredentialResolutionError
	if errors.As(err, &c) {
		return c
	}
	return nil
}

func TestUpstream_gemini_adc_auth(t *testing.T) {
	const f = "gemini-adc-auth"
	adcConfig := `{"geminiAuth":"adc","geminiProject":"ai-eng-sandbox","geminiLocation":"global"}`

	tw(t, f, "Gemini search uses Vertex AI + ADC bearer auth when geminiAuth is adc", func(t *testing.T) {
		home, agentDir := geminiEnv(t)
		adcPath := writeADC(t, home, nil)
		t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", adcPath)
		writeConfig(t, agentDir, adcConfig)
		n := useNet(t, func(c netCall) netReply {
			switch {
			case strings.HasPrefix(c.URL, geminiTokenURL):
				return jsonReply(map[string]any{"access_token": "synthetic-adc-token", "expires_in": 3599})
			case strings.Contains(c.URL, ":generateContent"):
				return jsonReply(map[string]any{"candidates": []any{map[string]any{
					"content":           map[string]any{"parts": []any{map[string]any{"text": "Paris is the capital of France."}}},
					"groundingMetadata": map[string]any{"groundingChunks": []any{map[string]any{"web": map[string]any{"uri": "https://en.wikipedia.org/wiki/Paris", "title": "Paris"}}}},
				}}})
			}
			return netReply{Err: errors.New("Unexpected fetch: " + c.URL)}
		})
		res, err := geminiSearch(bg(), "what is the capital of France?")
		noErr(t, err)
		if res.Answer != "Paris is the capital of France." || res.Provider != "gemini" {
			t.Fatalf("%+v", res)
		}
		var tokens, generates []netCall
		for _, c := range n.calls {
			if strings.HasPrefix(c.URL, geminiTokenURL) {
				tokens = append(tokens, c)
			} else if strings.Contains(c.URL, ":generateContent") {
				generates = append(generates, c)
			}
		}
		if len(tokens) != 1 || len(generates) != 1 {
			t.Fatalf("%d token exchanges, %d generate calls", len(tokens), len(generates))
		}
		g := generates[0]
		if g.URL != "https://aiplatform.googleapis.com/v1/projects/ai-eng-sandbox/locations/global/publishers/google/models/gemini-3.6-flash:generateContent" {
			t.Fatal(g.URL)
		}
		if g.Header.Get("Authorization") != "Bearer synthetic-adc-token" || g.Header.Get("x-goog-api-key") != "" {
			t.Fatalf("%v", g.Header)
		}
		form := tokens[0].Body
		for _, want := range []string{"client_id=synthetic-client-id", "client_secret=synthetic-client-secret", "refresh_token=synthetic-refresh-token", "grant_type=refresh_token"} {
			if !strings.Contains(form, want) {
				t.Fatalf("%q missing from the token request %q", want, form)
			}
		}
		if len(res.Results) != 1 || res.Results[0].URL != "https://en.wikipedia.org/wiki/Paris" || res.Results[0].Title != "Paris" {
			t.Fatalf("%+v", res.Results)
		}
	})

	tw(t, f, "Gemini ADC availability requires geminiAuth adc, project, location, and an ADC file", func(t *testing.T) {
		home, agentDir := geminiEnv(t)
		t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(home, "adc.json"))
		writeConfig(t, agentDir, `{"geminiAuth":"adc","geminiProject":"p","geminiLocation":"l"}`)
		if a, err := geminiADCAvailable(); err != nil || a || IsGeminiAPIAvailable() {
			t.Fatalf("without the file: adc=%v err=%v api=%v", a, err, IsGeminiAPIAvailable())
		}
		writeADC(t, home, nil)
		if a, err := geminiADCAvailable(); err != nil || !a || !IsGeminiAPIAvailable() {
			t.Fatalf("with the file: adc=%v err=%v api=%v", a, err, IsGeminiAPIAvailable())
		}
		// each requirement on its own
		for _, c := range []string{`{"geminiProject":"p","geminiLocation":"l"}`, `{"geminiAuth":"adc","geminiLocation":"l"}`, `{"geminiAuth":"adc","geminiProject":"p"}`,
			`{"geminiAuth":"adc","geminiProject":"p","geminiLocation":"l","geminiApiKey":"k"}`,
			`{"geminiAuth":"adc","geminiProject":"p","geminiLocation":"l","geminiBaseUrl":"https://relay.example.com"}`} {
			writeConfig(t, agentDir, c)
			if a, err := geminiADCAvailable(); err != nil || a {
				t.Fatalf("%s: adc=%v err=%v", c, a, err)
			}
		}
		// the project and location fall back to the environment
		writeConfig(t, agentDir, `{"geminiAuth":" ADC "}`)
		t.Setenv("GCLOUD_PROJECT", "env-project")
		t.Setenv("GOOGLE_CLOUD_LOCATION", "env-location")
		if a, err := geminiADCAvailable(); err != nil || !a {
			t.Fatalf("env fallback: adc=%v err=%v", a, err)
		}
		if p, _ := geminiADCProject(); p != "env-project" {
			t.Fatal(p)
		}
	})

	tw(t, f, "Gemini ADC config parse errors stay visible", func(t *testing.T) {
		_, agentDir := geminiEnv(t)
		writeConfig(t, agentDir, "not valid JSON")
		_, err := geminiADCAvailable()
		wantErr(t, err, `Failed to parse .*web-search\.json`)
	})

	tw(t, f, "Gemini ADC credential parse errors include the file path", func(t *testing.T) {
		home, _ := geminiEnv(t)
		path := filepath.Join(home, "adc.json")
		if err := os.WriteFile(path, []byte("not valid JSON"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", path)
		_, err := getGeminiADCAccessToken(bg())
		wantErr(t, err, `Failed to parse Google Application Default Credentials file at `)
		wantErr(t, err, `adc\.json`)
	})

	tw(t, f, "Gemini ADC credential shape errors include the file path", func(t *testing.T) {
		home, _ := geminiEnv(t)
		path := filepath.Join(home, "adc.json")
		if err := os.WriteFile(path, []byte(`{"type":"authorized_user","client_id":"id","client_secret":"secret"}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", path)
		_, err := getGeminiADCAccessToken(bg())
		wantErr(t, err, `Failed to parse Google Application Default Credentials file at `)
		wantErr(t, err, `authorized_user file is missing refresh_token`)
	})

	tw(t, f, "Gemini ADC rejects malformed token success responses", func(t *testing.T) {
		home, _ := geminiEnv(t)
		t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", writeADC(t, home, nil))
		useNet(t, func(netCall) netReply { return reply(200, `{"access_token":true,"expires_in":"bad"}`) })
		_, err := getGeminiADCAccessToken(bg())
		wantErr(t, err, `Gemini ADC token exchange returned no access_token`)
		useNet(t, func(netCall) netReply { return reply(200, `{"access_token":"token","expires_in":"bad"}`) })
		_, err = getGeminiADCAccessToken(bg())
		wantErr(t, err, `Gemini ADC token exchange returned invalid expires_in`)
	})

	tw(t, f, "Gemini ADC never leaks the access token in errors", func(t *testing.T) {
		home, agentDir := geminiEnv(t)
		t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", writeADC(t, home, nil))
		writeConfig(t, agentDir, `{"geminiAuth":"adc","geminiProject":"p","geminiLocation":"l"}`)
		useNet(t, func(c netCall) netReply {
			if strings.HasPrefix(c.URL, geminiTokenURL) {
				return reply(200, `{"access_token":"super-secret-adc-token-12345","expires_in":3599}`)
			}
			return reply(500, `{"error":{"message":"boom super-secret-adc-token-12345"}}`)
		})
		_, err := geminiSearch(bg(), "query")
		if err == nil {
			t.Fatal("expected an error")
		}
		if strings.Contains(err.Error(), "super-secret-adc-token-12345") || !strings.Contains(err.Error(), "[redacted]") {
			t.Fatalf("access token must not leak in error messages: %q", err.Error())
		}
	})

	tw(t, f, "Gemini ADC classifies only credential rejections as CredentialResolutionError", func(t *testing.T) {
		home, _ := geminiEnv(t)
		t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", writeADC(t, home, nil))
		classify := func(status int, body string) error {
			clearGeminiADCTokenCache()
			useNet(t, func(netCall) netReply { return reply(status, body) })
			_, err := getGeminiADCAccessToken(bg())
			if err == nil {
				t.Fatalf("status %d: expected an error", status)
			}
			return err
		}
		for _, status := range []int{400, 401, 403} {
			err := classify(status, `{"error":"denied"}`)
			c := credentialErr(err)
			if c == nil || c.Category != "oauth-credential-rejected" || !strings.Contains(err.Error(), "OAuth token exchange rejected") {
				t.Fatalf("status %d: %v", status, err)
			}
		}
		for _, status := range []int{429, 404, 500, 502, 503} {
			err := classify(status, "upstream boom")
			if credentialErr(err) != nil || !regexp.MustCompile(fmt.Sprintf(`token exchange failed \(%d\)`, status)).MatchString(err.Error()) {
				t.Fatalf("status %d: %v", status, err)
			}
		}
	})

	tw(t, f, "Gemini ADC does not advertise the Files-API (YouTube/video) path", func(t *testing.T) {
		home, agentDir := geminiEnv(t)
		t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", writeADC(t, home, nil))
		writeConfig(t, agentDir, `{"geminiAuth":"adc","geminiProject":"p","geminiLocation":"l"}`)
		if a, _ := geminiADCAvailable(); !a || !IsGeminiAPIAvailable() || IsGeminiAPIAvailableWithVideo() {
			t.Fatalf("adc only: adc=%v api=%v video=%v", a, IsGeminiAPIAvailable(), IsGeminiAPIAvailableWithVideo())
		}
		writeConfig(t, agentDir, `{"geminiAuth":"adc","geminiProject":"p","geminiLocation":"l","geminiApiKey":"synthetic-key"}`)
		if !IsGeminiAPIAvailable() || !IsGeminiAPIAvailableWithVideo() {
			t.Fatalf("adc and key: api=%v video=%v", IsGeminiAPIAvailable(), IsGeminiAPIAvailableWithVideo())
		}
	})
}

func TestUpstream_gemini_api_transport(t *testing.T) {
	const f = "gemini-api-transport"
	// The upstream cases drive video-extract.ts (upload, status, generate, delete). That file is the
	// video slice; the Gemini transport it uses is covered here through the generate call.
	tw(t, f, "Gemini generate, upload, status, and delete use only shared header auth", func(t *testing.T) {
		adapted(t, "only the generate call of the transport (gemini-api.ts); the upload, status and delete requests are video-extract.ts, a later slice")
		_, agentDir := geminiEnv(t)
		writeConfig(t, agentDir, `{"geminiApiKey":"synthetic-gemini-key"}`)
		n := useNet(t, func(netCall) netReply { return geminiText("# Synthetic video") })
		text, err := QueryGeminiAPIWithVideo(bg(), "describe", "files/synthetic", GeminiAPIOptions{MimeType: "video/webm"})
		noErr(t, err)
		if text != "# Synthetic video" || len(n.calls) != 1 {
			t.Fatal(text, len(n.calls))
		}
		c := n.calls[0]
		if c.URL != geminiPublicAPI+"/models/gemini-3.6-flash:generateContent" || c.Header.Get("x-goog-api-key") != "synthetic-gemini-key" ||
			strings.Contains(c.URL, "key") || strings.Contains(c.URL, "synthetic-gemini-key") {
			t.Fatalf("%v %v", c.URL, c.Header)
		}
		if GetUploadBase() != "https://generativelanguage.googleapis.com/upload/v1beta" {
			t.Fatal(GetUploadBase())
		}
	})
	tw(t, f, "Gemini video upload, status, and delete respect a configured geminiBaseUrl relay", func(t *testing.T) {
		adapted(t, "only the generate call and the upload base of the transport (gemini-api.ts); the upload, status and delete requests are video-extract.ts, a later slice")
		_, agentDir := geminiEnv(t)
		writeConfig(t, agentDir, `{"geminiApiKey":"synthetic-gemini-key","geminiBaseUrl":"https://relay.example.com"}`)
		n := useNet(t, func(netCall) netReply { return geminiText("# Relay video") })
		text, err := QueryGeminiAPIWithVideo(bg(), "describe", "files/synthetic", GeminiAPIOptions{})
		noErr(t, err)
		c := n.calls[0]
		if text != "# Relay video" || c.URL != "https://relay.example.com/v1beta/models/gemini-3.6-flash:generateContent" || c.Header.Get("x-goog-api-key") != "synthetic-gemini-key" {
			t.Fatalf("%q %v %v", text, c.URL, c.Header)
		}
		if GetUploadBase() != "https://relay.example.com/upload/v1beta" {
			t.Fatal(GetUploadBase())
		}
	})
}

func TestUpstream_gemini_pdf_extract(t *testing.T) {
	const f = "gemini-pdf-extract"
	setup := func(t *testing.T) {
		geminiEnv(t)
		t.Setenv("GEMINI_API_KEY", "synthetic-gemini-key")
	}
	pdfReply := func(finish, text string) netReply {
		return jsonReply(map[string]any{"candidates": []any{map[string]any{"finishReason": finish, "content": map[string]any{"parts": []any{map[string]any{"text": text}}}}}})
	}
	pages := func(n int) *int { return &n }

	tw(t, f, "Gemini PDF conversion sends inline PDF data and normalizes valid Markdown", func(t *testing.T) {
		setup(t)
		n := useNet(t, func(netCall) netReply {
			return pdfReply("STOP", "```markdown\n<!-- Page 1 -->\n# Sample Document\n\nFirst page.\n\n<!-- Page 2 -->\n\nSecond page.\n```")
		})
		input := []byte{1, 2, 3, 4}
		md, err := ExtractPDFViaGemini(bg(), input, GeminiPDFOptions{Pages: pages(5), MaxPages: 2, Title: "Sample Document"})
		noErr(t, err)
		c := n.calls[0]
		if !strings.HasSuffix(c.URL, "gemini-3.6-flash:generateContent") || c.Header.Get("x-goog-api-key") != "synthetic-gemini-key" {
			t.Fatalf("%v %v", c.URL, c.Header)
		}
		body := jsonBody(t, c)
		part0 := body["contents"].([]any)[0].(map[string]any)
		if part0["role"] != "user" {
			t.Fatal(part0)
		}
		parts := part0["parts"].([]any)
		inline := parts[0].(map[string]any)["inlineData"].(map[string]any)
		if inline["mimeType"] != "application/pdf" || inline["data"] != base64.StdEncoding.EncodeToString(input) {
			t.Fatal(inline)
		}
		if !strings.Contains(parts[1].(map[string]any)["text"].(string), "pages 1 through 2") {
			t.Fatal(parts[1])
		}
		if !strings.HasPrefix(md, "<!-- Page 1 -->") || regexp.MustCompile(`(?m)^<!-- Page 1 -->\s*\n# Sample Document`).MatchString(md) || !strings.Contains(md, "<!-- Page 2 -->") {
			t.Fatalf("%q", md)
		}
	})

	tw(t, f, "Gemini PDF conversion supports Cloudflare AI Gateway without a direct Gemini key", func(t *testing.T) {
		setup(t)
		unsetenv(t, "GEMINI_API_KEY")
		t.Setenv("GOOGLE_GEMINI_BASE_URL", "https://gateway.ai.cloudflare.com/v1/example/gemini")
		t.Setenv("CLOUDFLARE_API_KEY", "synthetic-cloudflare-key")
		n := useNet(t, func(netCall) netReply { return pdfReply("STOP", "<!-- Page 1 -->\nGateway result") })
		md, err := ExtractPDFViaGemini(bg(), []byte{0}, GeminiPDFOptions{Pages: pages(1), MaxPages: 1, Title: "Document"})
		noErr(t, err)
		c := n.calls[0]
		if !strings.HasPrefix(c.URL, "https://gateway.ai.cloudflare.com/") || c.Header.Get("cf-aig-authorization") != "Bearer synthetic-cloudflare-key" || c.Header.Get("x-goog-api-key") != "" {
			t.Fatalf("%v %v", c.URL, c.Header)
		}
		if !strings.Contains(md, "Gateway result") {
			t.Fatal(md)
		}
	})

	tw(t, f, "Gemini PDF conversion rejects truncated output", func(t *testing.T) {
		setup(t)
		useNet(t, func(netCall) netReply { return pdfReply("MAX_TOKENS", "<!-- Page 1 -->\nPartial") })
		_, err := ExtractPDFViaGemini(bg(), []byte{0}, GeminiPDFOptions{Pages: pages(1), MaxPages: 1, Title: "Document"})
		wantErr(t, err, `MAX_TOKENS`)
	})

	tw(t, f, "Gemini PDF conversion rejects missing or out-of-sequence page markers", func(t *testing.T) {
		setup(t)
		useNet(t, func(netCall) netReply { return pdfReply("STOP", "<!-- Page 1 -->\nFirst\n\n<!-- Page 3 -->\nThird") })
		_, err := ExtractPDFViaGemini(bg(), []byte{0}, GeminiPDFOptions{Pages: pages(2), MaxPages: 2, Title: "Document"})
		wantErr(t, err, `out of sequence`)
	})

	tw(t, f, "Gemini PDF conversion surfaces safety blocks and empty responses", func(t *testing.T) {
		setup(t)
		useNet(t, func(netCall) netReply { return reply(200, `{"promptFeedback":{"blockReason":"SAFETY"}}`) })
		_, err := ExtractPDFViaGemini(bg(), []byte{0}, GeminiPDFOptions{Pages: pages(1), MaxPages: 1, Title: "Document"})
		wantErr(t, err, `blocked PDF extraction: SAFETY`)
		useNet(t, func(netCall) netReply {
			return reply(200, `{"candidates":[{"finishReason":"STOP","content":{"parts":[]}}]}`)
		})
		_, err = ExtractPDFViaGemini(bg(), []byte{0}, GeminiPDFOptions{Pages: pages(1), MaxPages: 1, Title: "Document"})
		wantErr(t, err, `empty PDF extraction`)
	})
}

// writeScript writes an executable shell script and returns its path.
func writeScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestUpstream_config_path_gemini(t *testing.T) {
	const f = "config-path"

	tw(t, f, "Gemini base URL and Cloudflare auth use env before config", func(t *testing.T) {
		geminiEnv(t)
		_, agentDir := isolate(t)
		writeConfig(t, agentDir, `{"geminiBaseUrl":"https://config.example.com/gemini/","cloudflareApiKey":"config-cf-key"}`)
		t.Setenv("GOOGLE_GEMINI_BASE_URL", "https://gateway.ai.cloudflare.com/v1/account/gateway/google-ai-studio/")
		t.Setenv("CLOUDFLARE_API_KEY", "env-cf-key")
		if GetAPIHost() != geminiGatewayAI {
			t.Fatal(GetAPIHost())
		}
		if GetVersionedAPIBase() != geminiGatewayAI+"/v1beta" {
			t.Fatal(GetVersionedAPIBase())
		}
		gw, _ := isGeminiCloudflareGateway()
		cf, _ := geminiLegacyCloudflareKey()
		h := geminiBuildAuthHeaders("", cf, gw)
		if len(h) != 1 || h.Get("cf-aig-authorization") != "Bearer env-cf-key" || !IsGeminiAPIAvailable() {
			t.Fatalf("%v %v", h, IsGeminiAPIAvailable())
		}
	})

	tw(t, f, "Gemini command source is lazy, overrides stale env, rotates, and uses header auth", func(t *testing.T) {
		home, agentDir := geminiEnv(t)
		counter := filepath.Join(home, "counter")
		script := writeScript(t, home, "read-key.sh", "count=0\n[ ! -f \"$1\" ] || count=$(cat \"$1\")\ncount=$((count + 1))\nprintf '%s' \"$count\" >\"$1\"\nprintf 'synthetic-gemini-%s\\n' \"$count\"\n")
		writeConfig(t, agentDir, mustJSON(map[string]any{"geminiApiKey": "!" + script + " " + counter}))
		t.Setenv("GEMINI_API_KEY", "stale-gemini-environment-value")
		n := useNet(t, func(netCall) netReply { return geminiText("ok") })
		available := IsGeminiAPIAvailable()
		_, statErr := os.Stat(counter)
		if !available || !os.IsNotExist(statErr) {
			t.Fatalf("available=%v lazy=%v", available, os.IsNotExist(statErr))
		}
		for _, p := range []string{"first", "second"} {
			if _, err := QueryGeminiAPIWithVideo(bg(), p, "files/one", GeminiAPIOptions{Timeout: 15e9}); err != nil {
				t.Fatal(err)
			}
		}
		for i, want := range []string{"synthetic-gemini-1", "synthetic-gemini-2"} {
			c := n.calls[i]
			if c.Header.Get("x-goog-api-key") != want || query(c.URL, "key") != "" || query(c.URL, "api_key") != "" || strings.Contains(c.URL, "stale-gemini-environment-value") {
				t.Fatalf("%d: %v %v", i, c.URL, c.Header)
			}
		}
	})

	tw(t, f, "Gemini API helper rejects credential query parameters before fetch", func(t *testing.T) {
		geminiEnv(t)
		n := useNet(t, func(netCall) netReply { return reply(200, "ok") })
		key := "synthetic-secret"
		_, err := FetchGeminiAPI(bg(), "https://generativelanguage.googleapis.com/v1beta/models/test?API_KEY=synthetic-secret", RequestInit{}, &key)
		wantErr(t, err, `query parameters are not allowed`)
		if n.count() != 0 || strings.Contains(err.Error(), key) {
			t.Fatal(n.count(), err)
		}
	})

	tw(t, f, "Gemini provider and transport errors redact the resolved credential", func(t *testing.T) {
		geminiEnv(t)
		const secret = "SYNTHETIC_GEMINI_SECRET_MUST_NOT_ESCAPE"
		t.Setenv("GEMINI_API_KEY", secret)
		var mu sync.Mutex
		call := 0
		useNet(t, func(netCall) netReply {
			mu.Lock()
			defer mu.Unlock()
			call++
			if call == 1 {
				return reply(400, "provider echoed "+secret)
			}
			return netReply{Err: errors.New("transport echoed " + secret)}
		})
		for i := 0; i < 2; i++ {
			_, err := QueryGeminiAPIWithVideo(bg(), "describe", "files/test", GeminiAPIOptions{Timeout: 1e9})
			if err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "[redacted]") {
				t.Fatalf("%d: %v", i, err)
			}
		}
	})

	tw(t, f, "Gemini API requests include role and gateway auth headers", func(t *testing.T) {
		geminiEnv(t)
		t.Setenv("GOOGLE_GEMINI_BASE_URL", geminiGatewayAI)
		t.Setenv("CLOUDFLARE_API_KEY", "env-cf-key")
		n := useNet(t, func(netCall) netReply { return geminiText("ok") })
		text, err := QueryGeminiAPIWithVideo(bg(), "Describe", "files/test", GeminiAPIOptions{Model: "gemini-test", Timeout: 1e9})
		noErr(t, err)
		c := n.calls[0]
		if text != "ok" || c.URL != geminiGatewayAI+"/v1beta/models/gemini-test:generateContent" ||
			c.Header.Get("cf-aig-authorization") != "Bearer env-cf-key" || c.Header.Get("content-type") != "application/json" {
			t.Fatalf("%q %v %v", text, c.URL, c.Header)
		}
		want := `{"contents":[{"role":"user","parts":[{"fileData":{"fileUri":"files/test"}},{"text":"Describe"}]}]}`
		if c.Body != want {
			t.Fatal(c.Body)
		}
	})
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func TestUpstream_provider_credential_source_gemini(t *testing.T) {
	const f = "provider-credential-source"
	tw(t, f, "Cloudflare gateway response redacts the credential used by the request", func(t *testing.T) {
		home, agentDir := geminiEnv(t)
		counter := filepath.Join(home, "count")
		script := writeScript(t, home, "cloudflare-key.sh", "count=0\n[ ! -f \"$1\" ] || count=$(cat \"$1\")\ncount=$((count + 1))\nprintf '%s' \"$count\" >\"$1\"\nprintf 'cf-rotating-secret-%s' \"$count\"\n")
		writeConfig(t, agentDir, mustJSON(map[string]any{"cloudflareApiKey": "!" + script + " " + counter, "geminiBaseUrl": geminiGatewayAI}))
		useNet(t, func(c netCall) netReply {
			sent := strings.TrimPrefix(c.Header.Get("cf-aig-authorization"), "Bearer ")
			if sent == "" {
				sent = "missing"
			}
			return reply(400, mustJSON(map[string]any{"error": sent}))
		})
		_, err := QueryGeminiAPIWithVideo(bg(), "prompt", "files/synthetic", GeminiAPIOptions{})
		if err == nil || strings.Contains(err.Error(), "cf-rotating-secret-1") || !strings.Contains(err.Error(), "[redacted]") {
			t.Fatalf("%v", err)
		}
		if b, _ := os.ReadFile(counter); string(b) != "1" {
			t.Fatalf("the command ran %q times", b)
		}
	})
}

func TestUpstream_search_providers_gemini(t *testing.T) {
	const f = "search-providers"
	tw(t, f, "failed Gemini command source is redacted and blocks browser fallback", func(t *testing.T) {
		home, agentDir := geminiEnv(t)
		script := writeScript(t, home, "fail-key.sh", "printf 'SYNTHETIC_GEMINI_SECRET_MUST_NOT_ESCAPE\\n' >&2\nexit 9\n")
		writeConfig(t, agentDir, mustJSON(map[string]any{"geminiApiKey": "!" + script}))
		t.Setenv("GEMINI_API_KEY", "stale-gemini-environment-value")
		n := useNet(t, func(netCall) netReply { return netReply{Err: errors.New("unexpected fetch")} })
		_, err := geminiSearch(bg(), "must fail closed")
		wantErr(t, err, `^Gemini credential resolution failed: command-failed$`)
		if n.count() != 0 || strings.Contains(err.Error(), "SYNTHETIC_GEMINI_SECRET_MUST_NOT_ESCAPE") || strings.Contains(err.Error(), script) {
			t.Fatal(n.count(), err)
		}
	})

	tw(t, f, "Gemini API search uses its search-only default model", func(t *testing.T) {
		geminiEnv(t)
		t.Setenv("GEMINI_API_KEY", "gemini-test-key")
		n := useNet(t, func(netCall) netReply { return geminiText("Gemini answer") })
		res, err := geminiSearch(bg(), "latest TypeScript version")
		noErr(t, err)
		c := n.calls[0]
		tools, _ := json.Marshal(jsonBody(t, c)["tools"])
		if c.URL != geminiPublicAPI+"/models/gemini-3.6-flash:generateContent" || c.Header.Get("x-goog-api-key") != "gemini-test-key" ||
			string(tools) != `[{"google_search":{}}]` || res.Provider != "gemini" {
			t.Fatalf("%v %v %s %s", c.URL, c.Header, tools, res.Provider)
		}
	})

	tw(t, f, "Gemini API search preserves the configured searchModel", func(t *testing.T) {
		_, agentDir := geminiEnv(t)
		writeConfig(t, agentDir, `{"searchModel":"custom-gemini-model"}`)
		t.Setenv("GEMINI_API_KEY", "gemini-test-key")
		n := useNet(t, func(netCall) netReply { return geminiText("Gemini answer") })
		_, err := geminiSearch(bg(), "configured model")
		noErr(t, err)
		c := n.calls[0]
		if c.URL != geminiPublicAPI+"/models/custom-gemini-model:generateContent" || c.Header.Get("x-goog-api-key") != "gemini-test-key" {
			t.Fatalf("%v %v", c.URL, c.Header)
		}
	})
}

func TestUpstream_all_provider_gemini(t *testing.T) {
	const f = "all-provider"
	tw(t, f, `provider "all" uses Gemini API without falling back to Gemini Web`, func(t *testing.T) {
		geminiEnv(t)
		t.Setenv("GEMINI_API_KEY", "synthetic-gemini-key")
		// A Gemini Web client that must not be reached: "all" counts Gemini by its API alone.
		t.Cleanup(SetGeminiWebClient(failingGeminiWeb{t}))
		useNet(t, func(c netCall) netReply {
			switch {
			case strings.HasPrefix(c.URL, "https://mcp.exa.ai/mcp"):
				return exaMCPText("Title: Exa result\nURL: https://example.com/exa\nText: Exa answer\n---")
			case strings.Contains(c.URL, "generativelanguage.googleapis.com"):
				return reply(503, "api unavailable")
			}
			return netReply{Err: errors.New("Unexpected fetch " + c.URL)}
		})
		r, err := Search(bg(), "gemini api failure", FullSearchOptions{Provider: ProviderSelection{Name: "all"}})
		noErr(t, err)
		if r.Provider != "all" || !eqStrings(providerNames(r.ProviderResponses), []string{"exa"}) || len(r.ProviderErrors) != 1 || r.ProviderErrors[0].Provider != "gemini" ||
			!regexp.MustCompile(`Gemini API error 503`).MatchString(r.ProviderErrors[0].Error) || strings.Contains(r.ProviderErrors[0].Error, "Gemini Web") {
			t.Fatalf("%+v", r)
		}
	})
}

// failingGeminiWeb fails the test when anything reaches Gemini Web.
type failingGeminiWeb struct{ t *testing.T }

func (f failingGeminiWeb) Available(context.Context) (GeminiWebCookies, error) {
	f.t.Error("Gemini Web was consulted")
	return nil, nil
}
func (f failingGeminiWeb) Diagnostic() string { f.t.Error("Gemini Web was consulted"); return "" }
func (f failingGeminiWeb) Query(context.Context, string, GeminiWebCookies, time.Duration) (string, error) {
	f.t.Error("Gemini Web was queried")
	return "", nil
}
