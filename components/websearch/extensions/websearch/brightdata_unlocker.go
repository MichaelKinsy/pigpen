package websearch

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Port of brightdata-unlocker.ts: Bright Data Web Unlocker as a hosted fetch provider. The zone is
// its own required setting and never defaults to the SERP zone.

const (
	brightdataRequestURL    = "https://api.brightdata.com/request"
	brightdataUnlockTimeout = 60 * time.Second
)

var brightdataUnlockerHeading = regexp.MustCompile(`(?m)^#{1,2}\s+(.+)`)

func brightdataUnlockerConfig() (map[string]any, error) {
	return bdConfig("invalid JSON ", "invalid JSON")
}

func brightdataUnlockerRequireZone(root map[string]any) (string, error) {
	setting := zoneSettingOf(root, "BRIGHTDATA_UNLOCKER_ZONE", "brightdataUnlockerZone")
	if setting != nil {
		if zone, ok := brightdataNormalizeZone(setting.raw); ok {
			return zone, nil
		}
		return "", fmt.Errorf("Invalid Bright Data Unlocker zone: %s must be a zone name of letters, digits, \"-\", or \"_\". "+
			"The zone must be of type \"unblocker\"; a SERP zone will not serve Web Unlocker requests.", setting.label)
	}
	return "", fmt.Errorf("Bright Data Web Unlocker zone not configured. Either:\n  1. Set brightdataUnlockerZone in %s\n  2. Set BRIGHTDATA_UNLOCKER_ZONE environment variable\n"+
		"The zone must be of type \"unblocker\"; a SERP zone will not serve Web Unlocker requests.", ConfigPath())
}

// IsBrightDataUnlockerAvailable needs an Unlocker zone and a credential source.
func IsBrightDataUnlockerAvailable() bool {
	root, err := brightdataUnlockerConfig()
	if err != nil {
		return false
	}
	setting := zoneSettingOf(root, "BRIGHTDATA_UNLOCKER_ZONE", "brightdataUnlockerZone")
	if setting == nil {
		return false
	}
	if _, ok := brightdataNormalizeZone(setting.raw); !ok {
		return false
	}
	return HasCredentialSource(bdCredentialOptions(root))
}

// brightdataUnlockerAvailableErr is the availability check of the original, which reads the config
// and so can throw a parse error that the fetch routing reports.
func brightdataUnlockerAvailableErr() (bool, error) {
	if _, err := brightdataUnlockerConfig(); err != nil {
		return false, err
	}
	return IsBrightDataUnlockerAvailable(), nil
}

// ExtractWithBrightDataUnlocker fetches one URL through the Web Unlocker zone as markdown.
func ExtractWithBrightDataUnlocker(ctx context.Context, rawURL string, o ExtractOptions, ssrf SsrfConfig) (*ExtractedContent, error) {
	root, err := brightdataUnlockerConfig()
	if err != nil {
		return nil, err
	}
	zone, err := brightdataUnlockerRequireZone(root)
	if err != nil {
		return nil, err
	}
	// The unlocker takes only the allow ranges and the proxy trust of the SSRF config.
	if err := validateTarget(ctx, rawURL, ssrf, o.Lookup, false); err != nil {
		return nil, err
	}
	key, err := bdKey(ctx, root)
	if err != nil {
		return nil, err
	}
	tctx, cancel := withTimeout(ctx, timeoutMsOr(o.TimeoutMs, brightdataUnlockTimeout))
	defer cancel()
	body := jsonBytes(map[string]any{"url": rawURL, "zone": zone, "format": "raw", "data_format": "markdown"})
	resp, err := fetchAPIManual(tctx, brightdataRequestURL, RequestInit{Method: "POST", Body: body,
		Header: http.Header{"Content-Type": {"application/json"}, "Authorization": {"Bearer " + key}}}, ssrfValidation(ssrf, o.Lookup, false))
	if err != nil {
		return nil, redactErr(timeoutFix(ctx, tctx, err), key)
	}
	text, rerr := readBody(resp)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		if rerr != nil {
			text = ""
		}
		return nil, fmt.Errorf("Bright Data Web Unlocker error %d: %s", resp.StatusCode, jsSlice(RedactCredential(text, key), 0, 300))
	}
	if rerr != nil {
		return nil, redactErr(timeoutFix(ctx, tctx, rerr), key)
	}
	// Bright Data bills a request whatever its length, so short content is returned, not dropped.
	content := jsTrim(text)
	if content == "" {
		return nil, nil
	}
	title := ""
	if m := brightdataUnlockerHeading.FindStringSubmatch(content); m != nil {
		title = jsTrim(strings.ReplaceAll(m[1], "*", ""))
	}
	return &ExtractedContent{URL: rawURL, Title: title, Content: content}, nil
}
