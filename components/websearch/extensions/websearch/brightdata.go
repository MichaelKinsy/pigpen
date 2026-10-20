package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Port of brightdata.ts: Bright Data SERP search through a provisioned `serp` zone. Every message
// that quotes foreign text passes redactCredential and then untrustedText, in that order.

const (
	brightdataAPIURL        = "https://api.brightdata.com/request"
	brightdataSearchTimeout = 60 * time.Second
)

var brightdataZoneRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

var brightdataRecencyTbs = map[string]string{"day": "qdr:d", "week": "qdr:w", "month": "qdr:m", "year": "qdr:y"}

var (
	brightdataStatusShaped = regexp.MustCompile(`(?i)\b(?:error|status|http)[\s:=-]{1,4}(\d{3})\b`)
	brightdataQuotaShaped  = regexp.MustCompile(`(?i)rate limit|quota|too many requests`)
)

// brightdataConfig reads web-search.json. A parse failure quotes only a position, never the
// parser's text (brightdata.ts configParseDetail): the file is the credential store.
func brightdataConfig() (map[string]any, error) {
	return bdConfig("not valid JSON, ", "not valid JSON")
}

// bdConfig reads the config; on a syntax error the message is prefix+position, or bare when the
// parser reports none. The SERP file says "not valid JSON, at position N", the Unlocker file
// "invalid JSON at position N".
func bdConfig(prefix, bare string) (map[string]any, error) {
	path := ConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	var parsed any
	if err := json.Unmarshal(data, &parsed); err != nil {
		detail := bare
		var se *json.SyntaxError
		// V8 words a bad token at a value position as `Unexpected token ...` without a position and
		// reports one for structural mistakes; Go's messages split the same way.
		if errors.As(err, &se) && !strings.Contains(err.Error(), "looking for beginning of value") && !strings.Contains(err.Error(), "in literal") {
			off := max(int(se.Offset)-1, 0)
			line, col := 1, 1
			for _, c := range data[:min(off, len(data))] {
				if c == '\n' {
					line++
					col = 1
				} else {
					col++
				}
			}
			detail = fmt.Sprintf("%sat position %d (line %d column %d)", prefix, off, line, col)
		}
		return nil, fmt.Errorf("Failed to parse %s: %s", path, detail)
	}
	root, ok := parsed.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("Invalid config in %s: expected a JSON object", path)
	}
	return root, nil
}

func bdCredentialOptions(root map[string]any) CredentialOptions {
	return CredentialOptions{Provider: "Bright Data", ConfiguredValue: root["brightdataApiKey"], EnvironmentValue: envOrNil("BRIGHTDATA_API_KEY")}
}

func bdKey(ctx context.Context, root map[string]any) (string, error) {
	key, err := ResolveCredential(ctx, bdCredentialOptions(root))
	if err != nil {
		return "", err
	}
	if key == "" {
		return "", missingKey("Bright Data", "brightdataApiKey", "BRIGHTDATA_API_KEY", "Get a key at https://brightdata.com/cp/setting/users")
	}
	return key, nil
}

func brightdataNormalizeZone(s string) (string, bool) {
	t := jsTrim(s)
	if t == "" || !brightdataZoneRE.MatchString(t) {
		return "", false
	}
	return t, true
}

type zoneSetting struct{ raw, label string }

// zoneSettingOf resolves a zone by presence, not validity: a malformed environment value must not
// hand the request to the config file's zone.
func zoneSettingOf(root map[string]any, env, cfgKey string) *zoneSetting {
	if v := jsTrim(os.Getenv(env)); v != "" {
		return &zoneSetting{v, env}
	}
	if s, ok := root[cfgKey].(string); ok && jsTrim(s) != "" {
		return &zoneSetting{jsTrim(s), cfgKey + " in " + ConfigPath()}
	}
	return nil
}

func brightdataRequireSerpZone(root map[string]any) (string, error) {
	setting := zoneSettingOf(root, "BRIGHTDATA_SERP_ZONE", "brightdataSerpZone")
	if setting != nil {
		if zone, ok := brightdataNormalizeZone(setting.raw); ok {
			return zone, nil
		}
		return "", fmt.Errorf("Bright Data SERP zone is invalid: %s must be a zone name of letters, digits, \"-\", or \"_\" (got \"%s\").\n"+
			"The zone must be of Bright Data type `serp`; a Web Unlocker zone (type `unblocker`) is a different product and does not return SERP JSON.\n"+
			"Create or rename one at https://brightdata.com/cp/zones", setting.label, untrustedText(setting.raw, 60))
	}
	return "", fmt.Errorf("Bright Data SERP zone is invalid or missing. Either:\n  1. Create %s with { \"brightdataSerpZone\": \"your_serp_zone\" }\n  2. Set BRIGHTDATA_SERP_ZONE environment variable\n"+
		"The zone must be of Bright Data type `serp`; a Web Unlocker zone is a different product and does not return SERP JSON.\n"+
		"Create one at https://brightdata.com/cp/zones", ConfigPath())
}

// IsBrightDataAvailable needs a valid SERP zone and a credential source, and never fails or spends.
func IsBrightDataAvailable() bool {
	root, err := brightdataConfig()
	if err != nil {
		return false
	}
	setting := zoneSettingOf(root, "BRIGHTDATA_SERP_ZONE", "brightdataSerpZone")
	if setting == nil {
		return false
	}
	if _, ok := brightdataNormalizeZone(setting.raw); !ok {
		return false
	}
	return HasCredentialSource(bdCredentialOptions(root))
}

// untrustedText quotes foreign text: whitespace collapsed, cut to limit, and only then rewritten so
// that it cannot read as our HTTP status or as a rate limit to the routing classifier. The order is
// the point: slicing after the rewrite could re-create a status the rewrite left alone.
func untrustedText(text string, limit int) string {
	t := jsSlice(jsTrim(whitespaceRun.ReplaceAllString(text, " ")), 0, limit)
	t = brightdataStatusShaped.ReplaceAllString(t, "upstream ${1}")
	return brightdataQuotaShaped.ReplaceAllString(t, "upstream rate-limit notice")
}

func brightdataQuery(query string, f domainFilters) string {
	parts := []string{query}
	if len(f.allowed) == 1 {
		parts = append(parts, "site:"+f.allowed[0])
	} else if len(f.allowed) > 1 {
		sites := make([]string, len(f.allowed))
		for i, d := range f.allowed {
			sites[i] = "site:" + d
		}
		parts = append(parts, strings.Join(sites, " OR "))
	}
	for _, d := range f.blocked {
		parts = append(parts, "-site:"+d)
	}
	return strings.Join(parts, " ")
}

func brightdataSerpURL(searchQuery string, n int, recency string) string {
	params := []string{"q=" + formEscape(searchQuery), "num=" + strconv.Itoa(min(n+5, 20))}
	if tbs := brightdataRecencyTbs[recency]; tbs != "" {
		params = append(params, "tbs="+formEscape(tbs))
	}
	params = append(params, "brd_json=1")
	return "https://www.google.com/search?" + strings.Join(params, "&")
}

func brightdataInvalid(zone, msg string) error {
	return fmt.Errorf("Bright Data API returned invalid response for zone %s: %s", zone, msg)
}

// brightdataEnvelopeError is envelopeError: Bright Data's own error report inside a 200.
func brightdataEnvelopeError(env map[string]json.RawMessage, key string) string {
	var parts []string
	if raw, ok := env["error"]; ok {
		var s string
		if json.Unmarshal(raw, &s) == nil && len(raw) > 0 && raw[0] == '"' {
			if jsTrim(s) != "" {
				parts = append(parts, jsTrim(s))
			}
		} else if len(raw) > 0 && (raw[0] == '{' || raw[0] == '[') {
			parts = append(parts, compactJSON(raw))
		}
	}
	if raw, ok := env["errors"]; ok {
		var s string
		var list []json.RawMessage
		if json.Unmarshal(raw, &list) == nil && len(raw) > 0 && raw[0] == '[' && len(list) > 0 {
			parts = append(parts, compactJSON(raw))
		} else if json.Unmarshal(raw, &s) == nil && len(raw) > 0 && raw[0] == '"' && jsTrim(s) != "" {
			parts = append(parts, jsTrim(s))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	for _, k := range []string{"code", "error_code"} {
		raw, ok := env[k]
		if !ok || len(raw) == 0 {
			continue
		}
		var s string
		var n json.Number
		switch {
		case raw[0] == '"' && json.Unmarshal(raw, &s) == nil:
			if jsTrim(s) != "" {
				parts = append(parts, k+" "+jsTrim(s))
			}
		case json.Unmarshal(raw, &n) == nil && raw[0] != '"':
			if f, err := n.Float64(); err == nil {
				parts = append(parts, k+" "+jsNumber(f))
			}
		}
	}
	return untrustedText(RedactCredential(strings.Join(parts, ", "), key), 200)
}

// SearchWithBrightData runs one Google SERP through the configured Bright Data zone.
func SearchWithBrightData(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	root, err := brightdataConfig()
	if err != nil {
		return nil, err
	}
	// Zone before key: a config mistake must not spawn a `!command` credential resolver.
	zone, err := brightdataRequireSerpZone(root)
	if err != nil {
		return nil, err
	}
	key, err := bdKey(ctx, root)
	if err != nil {
		return nil, err
	}
	n := NormalizeSearchResultCount(o.NumResults)
	f := normalizeDomainFilters(o.DomainFilter)
	body := map[string]any{"url": brightdataSerpURL(brightdataQuery(query, f), n, o.RecencyFilter), "zone": zone, "format": "raw", "data_format": "parsed_light"}
	status, text, err := hostedSend(ctx, hostedCall{URL: brightdataAPIURL, Body: jsonBytes(body), Key: key, Timeout: brightdataSearchTimeout,
		Header: http.Header{"Authorization": {"Bearer " + key}, "Content-Type": {"application/json"}}, Credentials: []string{"Authorization"}})
	if err != nil {
		return nil, err
	}
	if status < 200 || status > 299 {
		return nil, fmt.Errorf("Bright Data API error %d for zone %s: %s", status, zone, untrustedText(RedactCredential(text, key), 300))
	}
	if jsTrim(text) == "" {
		return nil, fmt.Errorf("Bright Data API returned empty response for zone %s", zone)
	}
	var raw json.RawMessage
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		// The parser's message is not quoted: it embeds a prefix of the body, which a whole-value
		// redaction cannot remove.
		return nil, fmt.Errorf("Bright Data API returned invalid JSON for zone %s: %s", zone, untrustedText(RedactCredential(text, key), 300))
	}
	var env map[string]json.RawMessage
	if len(raw) == 0 || raw[0] != '{' || json.Unmarshal(raw, &env) != nil {
		return nil, brightdataInvalid(zone, "expected an object envelope")
	}
	if msg := brightdataEnvelopeError(env, key); msg != "" {
		return nil, brightdataInvalid(zone, "Bright Data reported an error instead of a SERP: "+msg)
	}
	organicRaw, ok := env["organic"]
	if !ok || string(organicRaw) == "null" {
		return nil, brightdataInvalid(zone, "expected an organic array and the envelope carried none. A `serp` zone queried with brd_json=1 "+
			"returns { organic: [...] }; a zone of type `unblocker`, or a missing brd_json=1, is the usual cause")
	}
	var organic []json.RawMessage
	if len(organicRaw) == 0 || organicRaw[0] != '[' || json.Unmarshal(organicRaw, &organic) != nil {
		return nil, brightdataInvalid(zone, "expected organic array")
	}
	entries := make([]map[string]any, len(organic))
	for i, e := range organic {
		var m map[string]any
		if len(e) == 0 || e[0] != '{' || json.Unmarshal(e, &m) != nil {
			return nil, brightdataInvalid(zone, fmt.Sprintf("expected organic[%d] object", i))
		}
		entries[i] = m
	}
	results := []SearchResult{}
	for _, item := range entries {
		l, _ := strField(item, "link")
		u := jsTrim(l)
		if u == "" || !f.matches(u) {
			continue
		}
		t, _ := strField(item, "title")
		title := jsTrim(t)
		if title == "" {
			title = fmt.Sprintf("Source %d", len(results)+1)
		}
		d, _ := strField(item, "description")
		results = append(results, SearchResult{Title: title, URL: u, Snippet: collapseWS(d)})
		if len(results) >= n {
			break
		}
	}
	return &SearchResponse{Answer: sourceLines(results), Results: results}, nil
}
