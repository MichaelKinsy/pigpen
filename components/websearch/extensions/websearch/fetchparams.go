package websearch

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
)

func jsonMarshal(v any) ([]byte, error)   { return json.Marshal(v) }
func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

// AuthSpec is the `auth` parameter: true, or a profile name.
type AuthSpec struct {
	True bool
	Name string
}

// FetchOptions are the normalised options of a fetch_content call.
type FetchOptions struct {
	ForceClone  *bool
	Prompt      string
	Timestamp   string
	Frames      int
	Model       string
	Mode        string
	AnswerModel string
	Auth        *AuthSpec
	Proxy       *string
}

// NormalizedFetchContentParams is the result of NormalizeFetchContentParams.
type NormalizedFetchContentParams struct {
	URLList []string
	Options FetchOptions
}

// NormalizeFetchContentParams trims and validates the tool arguments: `urls` (deduplicated) wins
// over `url`; blank strings are absent; frames must be an integer 1..12 and is only kept together
// with a timestamp or when more than one; mode, auth and proxy are validated with the original's
// messages.
func NormalizeFetchContentParams(params map[string]any) (NormalizedFetchContentParams, error) {
	var urls []string
	seen := map[string]bool{}
	if list, ok := params["urls"].([]any); ok {
		for _, v := range list {
			for _, u := range singleURL(v) {
				if !seen[u] {
					seen[u] = true
					urls = append(urls, u)
				}
			}
		}
	}
	if len(urls) == 0 {
		urls = singleURL(params["url"])
	}
	out := NormalizedFetchContentParams{URLList: urls}
	o := &out.Options
	o.Prompt = optionalString(params["prompt"])
	o.Timestamp = optionalString(params["timestamp"])
	frames := optionalFrameCount(params["frames"])
	if frames != 0 && (o.Timestamp != "" || frames > 1) {
		o.Frames = frames
	}
	if b, ok := params["forceClone"].(bool); ok {
		o.ForceClone = &b
	}
	o.Model = optionalString(params["model"])
	mode, err := normalizeMode(params["mode"])
	if err != nil {
		return out, err
	}
	o.Mode = mode
	o.AnswerModel = optionalString(params["answerModel"])
	auth, err := normalizeAuth(params["auth"])
	if err != nil {
		return out, err
	}
	o.Auth = auth
	proxy, err := normalizeProxyParam(params["proxy"], params)
	if err != nil {
		return out, err
	}
	o.Proxy = proxy
	return out, nil
}

func singleURL(v any) []string {
	s, ok := v.(string)
	if !ok {
		return nil
	}
	if t := strings.TrimSpace(s); t != "" {
		return []string{t}
	}
	return nil
}

func optionalString(v any) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

func normalizeMode(v any) (string, error) {
	if v == nil {
		return "", nil
	}
	if s, ok := v.(string); ok && (s == "readable" || s == "raw" || s == "answer") {
		return s, nil
	}
	return "", errors.New(`mode must be "readable", "raw", or "answer"`)
}

func normalizeAuth(v any) (*AuthSpec, error) {
	switch a := v.(type) {
	case nil:
		return nil, nil
	case bool:
		if a {
			return &AuthSpec{True: true}, nil
		}
		return nil, nil
	case string:
		if t := strings.TrimSpace(a); t != "" {
			return &AuthSpec{Name: t}, nil
		}
	}
	return nil, errors.New("auth must be a profile name, true, or false")
}

func normalizeProxyParam(v any, params map[string]any) (*string, error) {
	if _, present := params["proxy"]; !present {
		return nil, nil
	}
	if b, ok := v.(bool); ok && !b {
		return nil, nil
	}
	if v == nil {
		return nil, errors.New("proxy must be an http(s) or socks proxy URL string")
	}
	normalized, _, err := NormalizeProxyURL(v, "proxy")
	if err != nil {
		return nil, err
	}
	return &normalized, nil
}

func optionalFrameCount(v any) int {
	f, ok := v.(float64)
	if !ok || f != math.Trunc(f) || f < 1 || f > 12 {
		return 0
	}
	return int(f)
}
