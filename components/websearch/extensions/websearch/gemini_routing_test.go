package websearch

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// Tests of the Gemini provider's routing, the URL context fetch step, the Gemini Web seam and the
// service account flow. They are not upstream titles: each cites the upstream line it follows.

// fakeGeminiWeb is a scripted Gemini Web client.
type fakeGeminiWeb struct {
	cookies    GeminiWebCookies
	err        error
	diagnostic string
	answer     string
	queryErr   error
	prompts    []string
}

func (f *fakeGeminiWeb) Available(context.Context) (GeminiWebCookies, error) { return f.cookies, f.err }
func (f *fakeGeminiWeb) Diagnostic() string                                  { return f.diagnostic }
func (f *fakeGeminiWeb) Query(_ context.Context, prompt string, _ GeminiWebCookies, _ time.Duration) (string, error) {
	f.prompts = append(f.prompts, prompt)
	return f.answer, f.queryErr
}

func geminiAuto(t *testing.T, config string) {
	t.Helper()
	_, agentDir := geminiEnv(t)
	if config != "" {
		writeConfig(t, agentDir, config)
	}
}

func TestGeminiAutoRouting(t *testing.T) {
	// gemini-search.ts:827-833 and searchWithGemini(strictErrors false)
	t.Run("auto answers with Gemini when it is the only allowed provider", func(t *testing.T) {
		geminiAuto(t, `{"webSearch":{"allowedProviders":["gemini"]}}`)
		t.Setenv("GEMINI_API_KEY", "k")
		useNet(t, func(netCall) netReply { return geminiText("auto answer") })
		r, err := Search(bg(), "q", FullSearchOptions{Provider: Auto})
		noErr(t, err)
		if r.Provider != "gemini" || r.Answer != "auto answer" {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("auto drops a Gemini API failure and reports that no provider is available", func(t *testing.T) {
		geminiAuto(t, `{"webSearch":{"allowedProviders":["gemini"]}}`)
		t.Setenv("GEMINI_API_KEY", "k")
		useNet(t, func(netCall) netReply { return reply(503, "down") })
		_, err := Search(bg(), "q", FullSearchOptions{Provider: Auto})
		wantErr(t, err, `^No search provider available`)
	})
	t.Run("auto without Gemini credentials makes no Gemini request", func(t *testing.T) {
		geminiAuto(t, `{"webSearch":{"allowedProviders":["gemini"]}}`)
		n := useNet(t, func(netCall) netReply { return reply(500, "x") })
		_, err := Search(bg(), "q", FullSearchOptions{Provider: Auto})
		wantErr(t, err, `^No search provider available`)
		if n.count() != 0 {
			t.Fatal(n.urls())
		}
	})
	t.Run("auto reports a Gemini credential failure among the fallback errors", func(t *testing.T) {
		geminiAuto(t, `{"webSearch":{"allowedProviders":["gemini"]},"geminiApiKey":"$NO_SUCH_GEMINI_KEY"}`)
		useNet(t, func(netCall) netReply { return reply(500, "x") })
		_, err := Search(bg(), "q", FullSearchOptions{Provider: Auto})
		wantErr(t, err, `^Auto provider search failed:\n  - Gemini: Gemini credential resolution failed: environment-empty$`)
	})
	t.Run("auto does not use Gemini when the allowlist excludes it", func(t *testing.T) {
		geminiAuto(t, `{"webSearch":{"allowedProviders":["duckduckgo"]}}`)
		t.Setenv("GEMINI_API_KEY", "k")
		n := useNet(t, func(netCall) netReply { return reply(500, "x") })
		_, err := Search(bg(), "q", FullSearchOptions{Provider: Auto})
		wantErr(t, err, `^No search provider available`)
		if n.count() != 0 {
			t.Fatal(n.urls())
		}
	})
	t.Run("auto falls back to Gemini Web when the API has no credentials", func(t *testing.T) {
		geminiAuto(t, `{"webSearch":{"allowedProviders":["gemini"]}}`)
		web := &fakeGeminiWeb{cookies: GeminiWebCookies{"c": "v"}, answer: "web answer [Src](https://example.com/s)"}
		t.Cleanup(SetGeminiWebClient(web))
		useNet(t, func(netCall) netReply { return reply(500, "x") })
		r, err := Search(bg(), "q", FullSearchOptions{Provider: Auto, SearchOptions: SearchOptions{RecencyFilter: "week", DomainFilter: []string{"a.com", "-b.com"}}})
		noErr(t, err)
		if r.Provider != "gemini" || len(r.Results) != 1 || r.Results[0].URL != "https://example.com/s" || r.Results[0].Title != "Src" {
			t.Fatalf("%+v", r)
		}
		p := web.prompts[0]
		for _, want := range []string{"Question: q", "Only include results from the past week.", "Only cite sources from: a.com", "Do not cite sources from: b.com"} {
			if !strings.Contains(p, want) {
				t.Fatalf("%q missing from %q", want, p)
			}
		}
	})
}

func TestGeminiExplicitRouting(t *testing.T) {
	// gemini-search.ts:424-433 (unavailable text) and searchWithGemini(strictErrors true)
	t.Run("nothing configured gives the four-way setup message", func(t *testing.T) {
		geminiAuto(t, "")
		_, err := geminiSearch(bg(), "q")
		wantErr(t, err, `(?s)^Gemini search unavailable\. Either:\n  1\. Configure geminiApiKey in .*web-search\.json or set GEMINI_API_KEY\n  2\. Set GOOGLE_GEMINI_BASE_URL \+ CLOUDFLARE_API_KEY for Cloudflare AI Gateway routing\n  3\. Set geminiAuth to "adc" in web-search\.json with a Google Cloud ADC \+ project/location\n  4\. Sign into gemini\.google\.com in a supported Chromium-based browser$`)
	})
	t.Run("an API failure and a Gemini Web diagnostic are both listed", func(t *testing.T) {
		geminiAuto(t, "")
		t.Setenv("GEMINI_API_KEY", "k")
		t.Cleanup(SetGeminiWebClient(&fakeGeminiWeb{diagnostic: "no Google session"}))
		useNet(t, func(netCall) netReply { return reply(429, "slow down") })
		_, err := geminiSearch(bg(), "q")
		wantErr(t, err, `^Gemini search failed:\n  - Gemini API: Gemini API error 429: slow down\n  - Gemini Web: no Google session$`)
	})
	t.Run("an empty API answer falls through to Gemini Web", func(t *testing.T) {
		geminiAuto(t, "")
		t.Setenv("GEMINI_API_KEY", "k")
		t.Cleanup(SetGeminiWebClient(&fakeGeminiWeb{cookies: GeminiWebCookies{"c": "v"}, answer: "from the web"}))
		useNet(t, func(netCall) netReply { return reply(200, `{"candidates":[{"content":{"parts":[]}}]}`) })
		r, err := geminiSearch(bg(), "q")
		noErr(t, err)
		if r.Answer != "from the web" {
			t.Fatal(r.Answer)
		}
	})
	t.Run("a Gemini Web failure is attributed to Gemini Web", func(t *testing.T) {
		geminiAuto(t, "")
		t.Cleanup(SetGeminiWebClient(&fakeGeminiWeb{cookies: GeminiWebCookies{"c": "v"}, queryErr: errors.New("boom")}))
		_, err := geminiSearch(bg(), "q")
		wantErr(t, err, `^Gemini search failed:\n  - Gemini Web: boom$`)
	})
	t.Run("an abort stops the search without falling back", func(t *testing.T) {
		geminiAuto(t, "")
		t.Setenv("GEMINI_API_KEY", "k")
		web := &fakeGeminiWeb{cookies: GeminiWebCookies{"c": "v"}, answer: "x"}
		t.Cleanup(SetGeminiWebClient(web))
		useNet(t, func(netCall) netReply { return netReply{Err: context.Canceled} })
		ctx, cancel := context.WithCancel(bg())
		cancel()
		_, err := Search(ctx, "q", FullSearchOptions{Provider: ProviderSelection{Name: "gemini"}})
		if err == nil || !isAbortError(err) || len(web.prompts) != 0 {
			t.Fatal(err, web.prompts)
		}
	})
	t.Run("the default Gemini Web client reads browser cookies only when they are switched on", func(t *testing.T) {
		geminiAuto(t, "")
		if geminiWebOptionallyAvailable(bg()) {
			t.Fatal("available without opt-in")
		}
		_, err := geminiSearch(bg(), "q")
		wantErr(t, err, `^Gemini search unavailable`)
		_, agentDir := isolate(t)
		writeConfig(t, agentDir, `{"allowBrowserCookies":true}`)
		resetCookieState()
		if geminiWebOptionallyAvailable(bg()) {
			t.Fatal("available without a signed-in browser profile")
		}
		_, err = geminiSearch(bg(), "q")
		wantErr(t, err, `^Gemini search failed:\n  - Gemini Web: No detected Chromium profile contains a cookie database\.`)
	})
	t.Run("a configured routing skips an unavailable Gemini and classifies its failure", func(t *testing.T) {
		geminiAuto(t, `{"searchRouting":{"providers":["gemini"],"fallbackOn":["transient"]}}`)
		useNet(t, func(netCall) netReply { return reply(200, "x") })
		_, err := Search(bg(), "q", FullSearchOptions{Provider: Auto})
		wantErr(t, err, `gemini: unavailable`)
		t.Setenv("GEMINI_API_KEY", "k")
		useNet(t, func(c netCall) netReply {
			return reply(503, "busy")
		})
		_, err = Search(bg(), "q", FullSearchOptions{Provider: Auto})
		wantErr(t, err, `gemini \[transient\]: Gemini search failed:\n  - Gemini API: Gemini API error 503: busy`)
	})
	t.Run("grounding redirects resolve to the page they point at and keep the proxy URL when they cannot", func(t *testing.T) {
		geminiAuto(t, "")
		t.Setenv("GEMINI_API_KEY", "k")
		const proxy = "https://vertexaisearch.cloud.google.com/grounding-api-redirect/"
		n := useNet(t, func(c netCall) netReply {
			switch {
			case c.Method == http.MethodHead && strings.HasSuffix(c.URL, "good"):
				return redirectTo(302, "https://example.com/real")
			case c.Method == http.MethodHead:
				return netReply{Err: errors.New("dns failure")}
			}
			return jsonReply(map[string]any{"candidates": []any{map[string]any{
				"content": map[string]any{"parts": []any{map[string]any{"text": "a"}}},
				"groundingMetadata": map[string]any{"groundingChunks": []any{
					map[string]any{"web": map[string]any{"uri": proxy + "good", "title": "Good"}},
					map[string]any{"web": map[string]any{"uri": proxy + "bad", "title": "Bad"}},
					map[string]any{"retrievedContext": map[string]any{}},
					map[string]any{"web": map[string]any{"uri": "https://plain.example/x"}},
				}}}}})
		})
		r, err := geminiSearch(bg(), "q")
		noErr(t, err)
		got := []string{r.Results[0].URL, r.Results[1].URL, r.Results[2].URL}
		if len(r.Results) != 3 || !eqStrings(got, []string{"https://example.com/real", proxy + "bad", "https://plain.example/x"}) || r.Results[2].Title != "" {
			t.Fatalf("%+v", r.Results)
		}
		heads := 0
		for _, c := range n.calls {
			if c.Method == http.MethodHead {
				heads++
			}
		}
		if heads != 2 {
			t.Fatal(heads)
		}
	})
}

func TestGeminiURLContextFetch(t *testing.T) {
	body := strings.Repeat("Gemini extracted this page. ", 5)
	step := func(t *testing.T, geminiReply func() netReply) *fakeNet {
		t.Helper()
		return useNet(t, func(c netCall) netReply {
			if strings.Contains(c.URL, "generativelanguage.googleapis.com") {
				return geminiReply()
			}
			return reply(500, "origin down")
		})
	}
	routing := `{"fetchRouting":{"providers":["http","gemini"],"allowRemoteHostedProviders":true}}`

	t.Run("the URL context answer becomes the page after the direct fetch failed", func(t *testing.T) {
		extractEnv(t, routing)
		t.Setenv("GEMINI_API_KEY", "k")
		n := step(t, func() netReply { return geminiText("# Extracted Title\n\n" + body) })
		r := ExtractContent(bg(), "https://example.com/docs/page", pageOpts())
		if r.Error != nil || r.Title != "Extracted Title" || !strings.Contains(r.Content, "Gemini extracted this page.") {
			t.Fatalf("%+v %s", r, errOf(r))
		}
		var g netCall
		for _, c := range n.calls {
			if strings.Contains(c.URL, "generateContent") {
				g = c
			}
		}
		b := jsonBody(t, g)
		tools, _ := json.Marshal(b["tools"])
		text := b["contents"].([]any)[0].(map[string]any)["parts"].([]any)[0].(map[string]any)["text"].(string)
		if string(tools) != `[{"url_context":{}}]` || !strings.HasSuffix(text, "URL: https://example.com/docs/page") || !strings.HasPrefix(text, "Extract the complete readable content") {
			t.Fatalf("%s %q", tools, text)
		}
	})
	t.Run("the title falls back to the last path segment", func(t *testing.T) {
		if got := geminiExtracted("https://example.com/a/b.html", body).Title; got != "b.html" {
			t.Fatal(got)
		}
	})
	t.Run("an unsafe or failed retrieval, short content and an HTTP error fall through", func(t *testing.T) {
		for name, rep := range map[string]netReply{
			"unsafe": jsonReply(map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": body}}},
				"url_context_metadata": map[string]any{"url_metadata": []any{map[string]any{"url_retrieval_status": "URL_RETRIEVAL_STATUS_UNSAFE"}}}}}}),
			"error": jsonReply(map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": body}}},
				"url_context_metadata": map[string]any{"url_metadata": []any{map[string]any{"url_retrieval_status": "URL_RETRIEVAL_STATUS_ERROR"}}}}}}),
			"short": geminiText("too short"),
			"http":  reply(500, "boom"),
			"empty": reply(200, `{}`),
		} {
			extractEnv(t, routing)
			t.Setenv("GEMINI_API_KEY", "k")
			rep := rep
			step(t, func() netReply { return rep })
			r := ExtractContent(bg(), "https://example.com/p", pageOpts())
			if r.Error == nil || strings.Contains(r.Content, "Gemini extracted") {
				t.Fatalf("%s: %+v", name, r)
			}
		}
	})
	t.Run("without Gemini access no request is made and the fetch fails normally", func(t *testing.T) {
		extractEnv(t, routing)
		n := step(t, func() netReply { return geminiText(body) })
		r := ExtractContent(bg(), "https://example.com/p", pageOpts())
		for _, u := range n.urls() {
			if strings.Contains(u, "generativelanguage") {
				t.Fatal(u)
			}
		}
		if r.Error == nil {
			t.Fatal("expected failure")
		}
	})
	t.Run("a credential failure ends the fetch with its message", func(t *testing.T) {
		extractEnv(t, `{"geminiApiKey":"$NO_SUCH_GEMINI_KEY","fetchRouting":{"providers":["http","gemini"],"allowRemoteHostedProviders":true}}`)
		step(t, func() netReply { return geminiText(body) })
		r := ExtractContent(bg(), "https://example.com/p", pageOpts())
		if errOf(r) != "Gemini credential resolution failed: environment-empty" {
			t.Fatalf("%q", errOf(r))
		}
	})
	t.Run("Gemini Web is the next step when URL context has nothing", func(t *testing.T) {
		extractEnv(t, routing)
		web := &fakeGeminiWeb{cookies: GeminiWebCookies{"c": "v"}, answer: "# Web Title\n\n" + body}
		t.Cleanup(SetGeminiWebClient(web))
		step(t, func() netReply { return geminiText(body) })
		r := ExtractContent(bg(), "https://example.com/p", pageOpts())
		if r.Error != nil || r.Title != "Web Title" || len(web.prompts) != 1 || !strings.HasSuffix(web.prompts[0], "URL: https://example.com/p") {
			t.Fatalf("%+v %v", r, web.prompts)
		}
	})
}

func TestGeminiServiceAccountADC(t *testing.T) {
	// gemini-adc.ts:215-252
	home, agentDir := geminiEnv(t)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	noErr(t, err)
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", writeADC(t, home, map[string]any{
		"type": "service_account", "client_email": "svc@example.iam", "private_key": pemKey, "token_uri": "https://token.example.com/oauth",
		"client_id": nil, "client_secret": nil, "refresh_token": nil}))
	writeConfig(t, agentDir, `{"geminiAuth":"adc","geminiProject":"p","geminiLocation":"l"}`)
	n := useNet(t, func(c netCall) netReply {
		if c.URL == "https://token.example.com/oauth" {
			return reply(200, `{"access_token":"sa-token","expires_in":3600}`)
		}
		return geminiText("ok")
	})
	tok, err := getGeminiADCAccessToken(bg())
	noErr(t, err)
	if tok != "sa-token" {
		t.Fatal(tok)
	}
	form, _ := url.ParseQuery(n.calls[0].Body)
	if form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" || n.calls[0].Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
		t.Fatalf("%v", form)
	}
	parts := strings.Split(form.Get("assertion"), ".")
	if len(parts) != 3 {
		t.Fatal(parts)
	}
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], sig); err != nil {
		t.Fatal("the JWT signature does not verify: ", err)
	}
	var header, claims map[string]any
	h, _ := base64.RawURLEncoding.DecodeString(parts[0])
	c, _ := base64.RawURLEncoding.DecodeString(parts[1])
	_ = json.Unmarshal(h, &header)
	_ = json.Unmarshal(c, &claims)
	if header["alg"] != "RS256" || header["typ"] != "JWT" || claims["iss"] != "svc@example.iam" || claims["aud"] != "https://oauth2.googleapis.com/token" ||
		claims["scope"] != "https://www.googleapis.com/auth/cloud-platform" || claims["exp"].(float64)-claims["iat"].(float64) != 3600 {
		t.Fatalf("%v %v", header, claims)
	}
	// the token is cached: a second call exchanges nothing
	if _, err := getGeminiADCAccessToken(bg()); err != nil || n.count() != 1 {
		t.Fatal(err, n.count())
	}
	// an unsupported credential type
	clearGeminiADCTokenCache()
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", writeADC(t, home, map[string]any{"type": "external_account"}))
	_, err = getGeminiADCAccessToken(bg())
	wantErr(t, err, `Gemini ADC unsupported credential type "external_account" \(expected authorized_user or service_account\)`)
	// a missing file
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", home+"/missing.json")
	_, err = getGeminiADCAccessToken(bg())
	wantErr(t, err, `^Google Application Default Credentials file not found at .*missing\.json$`)
}

func TestGeminiHostAllowList(t *testing.T) {
	// gemini-api.ts:158-162
	geminiEnv(t)
	key := "k"
	n := useNet(t, func(netCall) netReply { return reply(200, "ok") })
	_, err := FetchGeminiAPI(bg(), "https://evil.example.com/v1beta/models/x", RequestInit{}, &key)
	wantErr(t, err, `^Gemini API request host is not allowed$`)
	if n.count() != 0 {
		t.Fatal("the key left for a foreign host")
	}
	// without a credential there is nothing to protect
	empty := ""
	if _, err := FetchGeminiAPI(bg(), "https://evil.example.com/x", RequestInit{}, &empty); err != nil {
		t.Fatal(err)
	}
	// caller-supplied credential headers are replaced
	_, err = FetchGeminiAPI(bg(), geminiPublicAPI+"/models/x", RequestInit{Header: http.Header{"X-Goog-Api-Key": {"caller"}, "Authorization": {"Bearer caller"}}}, &key)
	noErr(t, err)
	last := n.calls[len(n.calls)-1]
	if last.Header.Get("x-goog-api-key") != "k" || last.Header.Get("Authorization") != "" {
		t.Fatalf("%v", last.Header)
	}
}

func TestGeminiADCTokenLifetime(t *testing.T) {
	// gemini-adc.ts:13,149: a token is reused until 60 seconds before it expires
	home, _ := geminiEnv(t)
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", writeADC(t, home, nil))
	now := int64(1_000_000_000_000)
	prev := nowMs
	nowMs = func() int64 { return now }
	t.Cleanup(func() { nowMs = prev })
	n := useNet(t, func(netCall) netReply { return reply(200, `{"access_token":"t","expires_in":3600}`) })
	for _, step := range []struct {
		advance int64
		calls   int
	}{{0, 1}, {3_539_000, 1}, {2_000, 2}} {
		now += step.advance
		_, err := getGeminiADCAccessToken(bg())
		noErr(t, err)
		if n.count() != step.calls {
			t.Fatalf("after %d ms: %d exchanges, want %d", step.advance, n.count(), step.calls)
		}
	}
	// a short lifetime is still kept for a minute
	clearGeminiADCTokenCache()
	n = useNet(t, func(netCall) netReply { return reply(200, `{"access_token":"t","expires_in":10}`) })
	_, _ = getGeminiADCAccessToken(bg())
	now += 59_000
	_, _ = getGeminiADCAccessToken(bg())
	if n.count() != 1 {
		t.Fatal(n.count())
	}
}

func TestGeminiGatewayCredentialFailureEndsTheFetch(t *testing.T) {
	// gemini-url-context.ts:16-19 shouldRethrow: a credential failure is shown, not swallowed
	extractEnv(t, `{"geminiBaseUrl":"https://gateway.ai.cloudflare.com/v1/a/g","cloudflareApiKey":"$NO_SUCH_CLOUDFLARE_KEY","fetchRouting":{"providers":["http","gemini"],"allowRemoteHostedProviders":true}}`)
	useNet(t, func(netCall) netReply { return reply(500, "origin down") })
	r := ExtractContent(bg(), "https://example.com/p", pageOpts())
	if errOf(r) != "Cloudflare credential resolution failed: environment-empty" {
		t.Fatalf("%q", errOf(r))
	}
}

func TestGeminiPDFPageCounts(t *testing.T) {
	geminiEnv(t)
	t.Setenv("GEMINI_API_KEY", "k")
	two := 2
	reply1 := func(text string) {
		useNet(t, func(netCall) netReply {
			return jsonReply(map[string]any{"candidates": []any{map[string]any{"finishReason": "STOP", "content": map[string]any{"parts": []any{map[string]any{"text": text}}}}}})
		})
	}
	reply1("<!-- Page 1 -->\nonly one")
	_, err := ExtractPDFViaGemini(bg(), []byte{0}, GeminiPDFOptions{Pages: &two, MaxPages: 5, Title: "T"})
	wantErr(t, err, `^Gemini PDF extraction returned 1 page markers; expected 2$`)
	reply1("<!-- Page 1 -->\na\n<!-- Page 2 -->\nb\n<!-- Page 3 -->\nc")
	_, err = ExtractPDFViaGemini(bg(), []byte{0}, GeminiPDFOptions{MaxPages: 2, Title: "T"})
	wantErr(t, err, `^Gemini PDF extraction returned 3 page markers; expected at most 2$`)
	reply1("no markers here")
	_, err = ExtractPDFViaGemini(bg(), []byte{0}, GeminiPDFOptions{MaxPages: 2, Title: "T"})
	wantErr(t, err, `^Gemini PDF extraction returned no page markers$`)
	reply1("<!-- Page 1 -->\n# Heading, one!\n\nbody")
	md, err := ExtractPDFViaGemini(bg(), []byte{0}, GeminiPDFOptions{MaxPages: 2, Title: "heading one"})
	noErr(t, err)
	if md != "<!-- Page 1 -->\nbody" {
		t.Fatalf("%q", md)
	}
}

func TestGeminiAllIgnoresGeminiWeb(t *testing.T) {
	// gemini-search.ts:604-606: "all" counts Gemini by its API availability alone
	geminiEnv(t)
	web := &fakeGeminiWeb{cookies: GeminiWebCookies{"c": "v"}, answer: "web answer"}
	t.Cleanup(SetGeminiWebClient(web))
	useNet(t, func(c netCall) netReply {
		if strings.HasPrefix(c.URL, "https://mcp.exa.ai/mcp") {
			return exaMCPText("Title: Exa result\nURL: https://example.com/exa\nText: Exa answer\n---")
		}
		return netReply{Err: errors.New("Unexpected fetch " + c.URL)}
	})
	r, err := Search(bg(), "q", FullSearchOptions{Provider: ProviderSelection{Name: "all"}})
	noErr(t, err)
	if !eqStrings(providerNames(r.ProviderResponses), []string{"exa"}) || len(r.ProviderErrors) != 0 || len(web.prompts) != 0 {
		t.Fatalf("%+v %v", r, web.prompts)
	}
}

func TestGeminiMalformedADCFileEndsTheFetch(t *testing.T) {
	// gemini-url-context.ts:16-19 shouldRethrow: an unreadable credential file is shown, not swallowed
	home, agentDir := geminiEnv(t)
	adc := home + "/adc.json"
	noErr(t, os.WriteFile(adc, []byte("not json"), 0o600))
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", adc)
	writeConfig(t, agentDir, `{"geminiAuth":"adc","geminiProject":"p","geminiLocation":"l","fetchRouting":{"providers":["http","gemini"],"allowRemoteHostedProviders":true}}`)
	useNet(t, func(netCall) netReply { return reply(500, "origin down") })
	r := ExtractContent(bg(), "https://example.com/p", pageOpts())
	wantErr(t, errors.New(errOf(r)), `^Failed to parse Google Application Default Credentials file at .*adc\.json`)
}
