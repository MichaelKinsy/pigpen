package langsmith

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// loadProfileGolden reads testdata/<name> with the SDK codec, which keeps lone surrogates (WTF-8) that
// encoding/json would turn into U+FFFD.
func loadProfileGolden(t testing.TB, name string, v any) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

type profRT func(*http.Request) (*http.Response, error)

func (f profRT) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func profTextResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
}

type profileVectors struct {
	JSON []struct {
		Text  string  `json:"text"`
		Out   *string `json:"out"`
		Error string  `json:"error"`
	} `json:"json"`
	URLs []struct {
		Input    string `json:"input"`
		Href     string `json:"href"`
		Origin   string `json:"origin"`
		Protocol string `json:"protocol"`
		Host     string `json:"host"`
		Pathname string `json:"pathname"`
		Username string `json:"username"`
		Password string `json:"password"`
		Error    bool   `json:"error"`
	} `json:"urls"`
	Discovery []struct {
		APIURL   string `json:"apiUrl"`
		Endpoint string `json:"endpoint"`
		Requests []struct {
			Method string  `json:"method"`
			URL    string  `json:"url"`
			Accept *string `json:"accept"`
		} `json:"requests"`
		Rejected int `json:"rejected"`
	} `json:"discovery"`
	Trust []struct {
		APIURL   string         `json:"apiUrl"`
		Doc      map[string]any `json:"doc"`
		Endpoint string         `json:"endpoint"`
		Requests int            `json:"requests"`
	} `json:"trust"`
	OddBodies []struct {
		Body     string `json:"body"`
		Endpoint string `json:"endpoint"`
		Requests int    `json:"requests"`
	} `json:"oddBodies"`
	Form []struct {
		Input string `json:"input"`
		Out   string `json:"out"`
	} `json:"form"`
	HasValue []struct {
		Input string `json:"input"`
		Out   bool   `json:"out"`
	} `json:"hasValue"`
	Trim []struct {
		Input   string  `json:"input"`
		APIKey  *string `json:"apiKey"`
		Access  *string `json:"access"`
		Refresh *string `json:"refresh"`
		HasAuth bool    `json:"hasAuth"`
	} `json:"trim"`
}

func loadProfileVectors(t testing.TB) *profileVectors {
	var v profileVectors
	loadProfileGolden(t, "profile_vectors.json", &v)
	if len(v.JSON) < 350 || len(v.URLs) < 150 || len(v.Trust) < 900 || len(v.Trim) < 250 {
		t.Fatalf("vectors truncated: json=%d urls=%d trust=%d trim=%d", len(v.JSON), len(v.URLs), len(v.Trust), len(v.Trim))
	}
	return &v
}

// JSON.parse then JSON.stringify(v, null, 2): the bytes the refresh writes back for the user's file.
func TestProfileJSONRoundTripVectors(t *testing.T) {
	v := loadProfileVectors(t)
	failures := 0
	for _, c := range v.JSON {
		parsed, ok := parseJSONOrdered(c.Text)
		if c.Out == nil {
			if ok {
				t.Errorf("parse(%q) succeeded; JSON.parse throws %s", c.Text, c.Error)
			}
			continue
		}
		if !ok {
			t.Errorf("parse(%q) failed; JSON.parse accepts it", c.Text)
			continue
		}
		got, err := stringifyIndent2(nil, parsed)
		if err != nil {
			t.Errorf("stringify(%q): %v", c.Text, err)
			continue
		}
		if string(got) != *c.Out {
			failures++
			if failures < 10 {
				t.Errorf("round trip of %q:\n got %q\nwant %q", c.Text, got, *c.Out)
			}
		}
	}
}

func TestProfileURLVectors(t *testing.T) {
	v := loadProfileVectors(t)
	for _, c := range v.URLs {
		u, ok := parseWhatwgURL(c.Input)
		if c.Error {
			if ok {
				t.Errorf("parse(%q) = %q; `new URL` throws", c.Input, u.href())
			}
			continue
		}
		if !ok {
			t.Errorf("parse(%q) failed; want %q", c.Input, c.Href)
			continue
		}
		if u.href() != c.Href || u.origin() != c.Origin || u.protocol() != c.Protocol || u.hostPort() != c.Host || u.path != c.Pathname || u.username != c.Username || u.password != c.Password {
			t.Errorf("parse(%q):\n got href=%q origin=%q protocol=%q host=%q path=%q user=%q pass=%q\nwant href=%q origin=%q protocol=%q host=%q path=%q user=%q pass=%q",
				c.Input, u.href(), u.origin(), u.protocol(), u.hostPort(), u.path, u.username, u.password,
				c.Href, c.Origin, c.Protocol, c.Host, c.Pathname, c.Username, c.Password)
		}
	}
}

func TestProfileFormEncodingVectors(t *testing.T) {
	v := loadProfileVectors(t)
	for _, c := range v.Form {
		body := append([]byte("grant_type=refresh_token&client_id="), formURLEncode(nil, oauthClientID)...)
		body = append(body, "&refresh_token="...)
		body = formURLEncode(body, c.Input)
		if string(body) != c.Out {
			t.Errorf("encode(%q) = %q want %q", c.Input, body, c.Out)
		}
	}
}

func TestProfileHasValueVectors(t *testing.T) {
	v := loadProfileVectors(t)
	for _, c := range v.HasValue {
		in := c.Input
		if got := hasValue(&in); got != c.Out {
			t.Errorf("hasValue(%q) = %v want %v", c.Input, got, c.Out)
		}
	}
	if hasValue(nil) {
		t.Error("hasValue(undefined) is false in the original")
	}
}

// trimConfigValue as loadProfileClientConfig applies it to api_key and the OAuth tokens.
func TestProfileTrimVectors(t *testing.T) {
	v := loadProfileVectors(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	t.Setenv("LANGSMITH_CONFIG_FILE", cfgPath)
	t.Setenv("LANGSMITH_PROFILE", "")
	for _, c := range v.Trim {
		doc, err := json.Marshal(map[string]any{"current_profile": "p", "profiles": map[string]any{"p": map[string]any{
			"api_key": c.Input, "workspace_id": c.Input, "oauth": map[string]any{"access_token": c.Input, "refresh_token": c.Input}}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cfgPath, doc, 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := loadProfileClientConfig()
		if err != nil {
			t.Fatalf("%q: %v", c.Input, err)
		}
		eq := func(name string, got, want *string) {
			if (got == nil) != (want == nil) || (got != nil && *got != *want) {
				t.Errorf("%s of %q: got %v want %v", name, c.Input, profDeref(got), profDeref(want))
			}
		}
		eq("apiKey", cfg.APIKey, c.APIKey)
		eq("access", cfg.OAuthAccessToken, c.Access)
		eq("refresh", cfg.OAuthRefreshToken, c.Refresh)
		if (cfg.Auth != nil) != c.HasAuth {
			t.Errorf("hasAuth of %q: got %v want %v", c.Input, cfg.Auth != nil, c.HasAuth)
		}
		if cfg.Auth != nil {
			cfg.Auth.close()
		}
	}
}

func profDeref(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

// resolveTokenEndpoint against a fake fetch: the metadata URLs asked for and the trust decision.
func TestProfileDiscoveryVectors(t *testing.T) {
	v := loadProfileVectors(t)
	for _, c := range v.Discovery {
		var got []string
		hc := &http.Client{Transport: profRT(func(r *http.Request) (*http.Response, error) {
			got = append(got, r.Method+" "+r.URL.String()+" "+r.Header.Get("Accept"))
			return profTextResponse(404, ""), nil
		})}
		endpoint := resolveTokenEndpoint(context.Background(), c.APIURL, hc)
		var want []string
		for _, r := range c.Requests {
			a := ""
			if r.Accept != nil {
				a = *r.Accept
			}
			want = append(want, r.Method+" "+r.URL+" "+a)
		}
		if endpoint != c.Endpoint || strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("apiUrl %q:\n got %q -> %q\nwant %q -> %q", c.APIURL, got, endpoint, want, c.Endpoint)
		}
	}
}

func TestProfileTrustVectors(t *testing.T) {
	v := loadProfileVectors(t)
	trusted := 0
	for _, c := range v.Trust {
		docJSON, err := json.Marshal(c.Doc)
		if err != nil {
			t.Fatal(err)
		}
		requests := 0
		hc := &http.Client{Transport: profRT(func(r *http.Request) (*http.Response, error) {
			requests++
			if requests == 1 {
				return profTextResponse(200, string(docJSON)), nil
			}
			return profTextResponse(404, ""), nil
		})}
		endpoint := resolveTokenEndpoint(context.Background(), c.APIURL, hc)
		if endpoint != c.Endpoint || requests != c.Requests {
			t.Errorf("apiUrl %q doc %s: got %q after %d requests; want %q after %d", c.APIURL, docJSON, endpoint, requests, c.Endpoint, c.Requests)
		}
		if c.Requests == 1 {
			trusted++
		}
	}
	if trusted == 0 || trusted == len(v.Trust) {
		t.Fatalf("vectors do not exercise both outcomes: %d trusted of %d", trusted, len(v.Trust))
	}
	for _, c := range v.OddBodies {
		requests := 0
		hc := &http.Client{Transport: profRT(func(r *http.Request) (*http.Response, error) {
			requests++
			if requests == 1 {
				return profTextResponse(200, c.Body), nil
			}
			return profTextResponse(404, ""), nil
		})}
		endpoint := resolveTokenEndpoint(context.Background(), "https://host.example", hc)
		if endpoint != c.Endpoint || requests != c.Requests {
			t.Errorf("body %q: got %q after %d; want %q after %d", c.Body, endpoint, requests, c.Endpoint, c.Requests)
		}
	}
}
