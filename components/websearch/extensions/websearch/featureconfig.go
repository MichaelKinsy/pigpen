package websearch

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
)

// Ports of feature-config.ts, the fetch.timeout / fetchRouting readers of extract.ts and the PDF
// gate of pdf-extract.ts: the parts of web-search.json that steer fetch_content.

const (
	defaultFetchTimeoutMs = 30000
	maxConfiguredTimeout  = 2_147_483_647
	defaultPDFMaxSizeMB   = 20
	maxPDFMaxSizeMB       = 50
)

// IsImageEnabled reports image.enabled (default true); an unreadable config is an error.
func IsImageEnabled() (bool, error) {
	root, err := readLenientConfig()
	if err != nil {
		return false, err
	}
	image, _ := root["image"].(map[string]any)
	if image == nil {
		return true, nil
	}
	enabled, ok := image["enabled"].(bool)
	return !ok || enabled, nil
}

// CanAttachImages is IsImageEnabled with a config error meaning "no".
func CanAttachImages() bool {
	ok, err := IsImageEnabled()
	return err == nil && ok
}

// readLenientConfig is ReadConfigRoot, except that a non-object root counts as an empty config
// (feature-config.ts and pdf-extract.ts only fail on a JSON syntax error).
func readLenientConfig() (map[string]any, error) {
	root, err := ReadConfigRoot()
	if err != nil {
		if strings.HasPrefix(err.Error(), "Invalid config in") {
			return map[string]any{}, nil
		}
		return nil, err
	}
	if root == nil {
		root = map[string]any{}
	}
	return root, nil
}

type pdfConfig struct {
	Enabled          bool
	MaxSizeMB        float64
	MaxPages         int
	Provider         string // auto | gemini | datalab | unpdf
	DatalabMode      string // fast | balanced | accurate
	DatalabTimeoutMs float64
}

const (
	defaultPDFMaxPages      = 100
	defaultDatalabTimeoutMs = 120000
	maxDatalabTimeoutMs     = 300000
)

// normalizeDatalabMode is datalab-pdf-extract.ts:116: empty means balanced, an unknown value fails.
func normalizeDatalabMode(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "balanced", nil
	}
	switch mode := strings.ToLower(trimmed); mode {
	case "fast", "balanced", "accurate":
		return mode, nil
	}
	return "", fmt.Errorf(`Failed to parse datalab mode: expected "fast", "balanced", or "accurate", got "%s"`, raw)
}

// loadPDFConfig ports loadPDFConfig (pdf-extract.ts:59): every value is validated and falls back
// to its default. Only enabled and maxSizeMB are used until PDF text extraction is ported.
func loadPDFConfig() (pdfConfig, error) {
	cfg := pdfConfig{Enabled: true, MaxSizeMB: defaultPDFMaxSizeMB, MaxPages: defaultPDFMaxPages, Provider: "auto", DatalabTimeoutMs: defaultDatalabTimeoutMs}
	envMode, err := normalizeDatalabMode(os.Getenv("DATALAB_MODE"))
	if err != nil {
		return cfg, err
	}
	cfg.DatalabMode = envMode
	root, err := readLenientConfig()
	if err != nil {
		return cfg, err
	}
	pdf, _ := root["pdf"].(map[string]any)
	if pdf == nil {
		return cfg, nil
	}
	positive := func(v any) (float64, bool) {
		f, ok := v.(float64)
		return f, ok && !math.IsInf(f, 0) && !math.IsNaN(f) && f > 0
	}
	if e, ok := pdf["enabled"].(bool); ok && !e {
		cfg.Enabled = false
	}
	if f, ok := positive(pdf["maxSizeMB"]); ok {
		cfg.MaxSizeMB = math.Min(f, maxPDFMaxSizeMB)
	}
	if f, ok := positive(pdf["maxPages"]); ok {
		cfg.MaxPages = int(math.Max(1, math.Floor(f)))
	}
	if p, ok := pdf["provider"].(string); ok && sliceHas([]string{"auto", "gemini", "datalab", "unpdf"}, p) {
		cfg.Provider = p
	}
	if m, ok := pdf["datalabMode"].(string); ok && sliceHas([]string{"fast", "balanced", "accurate"}, m) {
		cfg.DatalabMode = m
	}
	if f, ok := positive(pdf["datalabTimeoutMs"]); ok {
		cfg.DatalabTimeoutMs = math.Min(f, maxDatalabTimeoutMs)
	}
	return cfg, nil
}

// ResolveFetchTimeoutMs is the direct HTTP/Jina fetch budget: an explicit per-call value wins over
// fetch.timeout (seconds, rounded up to milliseconds), which wins over 30 s.
func ResolveFetchTimeoutMs(explicit *int64) (int64, error) {
	if explicit != nil {
		return *explicit, nil
	}
	root, err := ReadConfigRoot()
	if err != nil || root == nil {
		return defaultFetchTimeoutMs, err
	}
	path := ConfigPath()
	fetchCfg, present := root["fetch"]
	if !present {
		return defaultFetchTimeoutMs, nil
	}
	obj, ok := fetchCfg.(map[string]any)
	if !ok {
		return 0, fmt.Errorf("fetch in %s must be an object", path)
	}
	value, present := obj["timeout"]
	if !present {
		return defaultFetchTimeoutMs, nil
	}
	seconds, ok := value.(float64)
	if !ok || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 {
		return 0, fmt.Errorf("Invalid fetch.timeout in %s: expected a positive finite number of seconds, got %s", path, jsonValue(value))
	}
	ms := math.Ceil(seconds * 1000)
	if ms < 1 || ms > maxConfiguredTimeout {
		return 0, fmt.Errorf("Invalid fetch.timeout in %s: converted timeout must be a finite safe integer from 1 through %d milliseconds", path, maxConfiguredTimeout)
	}
	return int64(math.Max(1, ms)), nil
}

// fetch providers, in the original's names.
var (
	fetchProviders         = []string{"http", "firecrawl", "crawl4ai", "jina", "tinyfish", "search1api", "querit", "kagi", "ollama", "parallel", "parallel-mcp", "brightdata", "gemini"}
	defaultFetchOrder      = []string{"http", "firecrawl", "crawl4ai", "jina", "tinyfish", "search1api", "querit", "kagi", "ollama", "parallel", "brightdata", "gemini"}
	remoteHostedFetchNames = []string{"jina", "tinyfish", "search1api", "querit", "kagi", "ollama", "parallel", "parallel-mcp", "brightdata", "gemini"}
)

type fetchRouting struct {
	Providers                  []string
	AllowRemoteHostedProviders bool
}

func loadFetchRouting() (fetchRouting, error) {
	def := fetchRouting{Providers: defaultFetchOrder}
	root, err := ReadConfigRoot()
	if err != nil || root == nil {
		return def, err
	}
	raw, present := root["fetchRouting"]
	if !present {
		return def, nil
	}
	path := ConfigPath()
	obj, ok := raw.(map[string]any)
	if !ok {
		return def, fmt.Errorf("fetchRouting in %s must be an object", path)
	}
	out := def
	if list, present := obj["providers"]; present {
		arr, ok := list.([]any)
		if !ok || len(arr) == 0 {
			return def, fmt.Errorf("fetchRouting.providers in %s must be a non-empty array", path)
		}
		out.Providers = nil
		for _, p := range arr {
			s, _ := p.(string)
			n := strings.ToLower(jsTrim(s))
			if !sliceHas(fetchProviders, n) {
				return def, fmt.Errorf("fetchRouting.providers in %s contains an invalid provider: %s", path, jsString(p))
			}
			if sliceHas(out.Providers, n) {
				return def, fmt.Errorf("fetchRouting.providers in %s must not contain duplicates: %s", path, n)
			}
			out.Providers = append(out.Providers, n)
		}
	}
	if v, present := obj["allowRemoteHostedProviders"]; present {
		b, ok := v.(bool)
		if !ok {
			return def, fmt.Errorf("fetchRouting.allowRemoteHostedProviders in %s must be a boolean", path)
		}
		out.AllowRemoteHostedProviders = b
	}
	return out, nil
}

// jsonValue is JSON.stringify(value) for a decoded JSON value.
func jsonValue(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}
