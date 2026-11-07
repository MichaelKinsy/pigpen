package langsmith

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The scenarios of port/tools/gen/profile-differential.mjs, run by the real ProfileAuth of langsmith 0.10.5 against a
// loopback server (Date.now pinned to t0) and replayed here against the Go port: the same requests must reach the
// server, the same headers come back and the same bytes land in the config file.

type diffRoute struct {
	Method     string            `json:"method"`
	Path       string            `json:"path"`
	Status     int               `json:"status"`
	Body       string            `json:"body"`
	BodyBase64 string            `json:"bodyBase64"`
	CT         string            `json:"ct"`
	Headers    map[string]string `json:"headers"`
}

type diffOp struct {
	Op    string `json:"op"`
	Value string `json:"value"`
	Text  string `json:"text"`
	N     int    `json:"n"`
}

type diffScenario struct {
	Name         string            `json:"name"`
	Env          map[string]string `json:"env"`
	Config       *string           `json:"config"`
	ConfigBase64 string            `json:"configBase64"`
	ConfigIsDir  bool              `json:"configIsDir"`
	TmpIsDir     bool              `json:"tmpIsDir"`
	UseHome      bool              `json:"useHome"`
	PreFiles     map[string]string `json:"preFiles"`
	Routes       []diffRoute       `json:"routes"`
	Ops          []diffOp          `json:"ops"`
}

type diffHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type diffOpResult struct {
	Op     string `json:"op"`
	Value  string `json:"value"`
	Result any    `json:"result"`
	Error  string `json:"error"`
}

type diffRequest struct {
	Method      string  `json:"method"`
	Path        string  `json:"path"`
	Accept      *string `json:"accept"`
	ContentType *string `json:"contentType"`
	Body        string  `json:"body"`
}

type diffLoad struct {
	APIURL            *string `json:"apiUrl"`
	APIKey            *string `json:"apiKey"`
	WorkspaceID       *string `json:"workspaceId"`
	OAuthAccessToken  *string `json:"oauthAccessToken"`
	OAuthRefreshToken *string `json:"oauthRefreshToken"`
	HasAuth           bool    `json:"hasAuth"`
	Error             string  `json:"error"`
}

type diffResult struct {
	Name     string         `json:"name"`
	Load     diffLoad       `json:"load"`
	Ops      []diffOpResult `json:"ops"`
	Requests []diffRequest  `json:"requests"`
	Config   *string        `json:"config"`
	Files    []string       `json:"files"`
}

type diffFile struct {
	T0        int64          `json:"t0"`
	DateTable map[string]any `json:"dateTable"`
	Scenarios []diffScenario `json:"scenarios"`
	Results   []diffResult   `json:"results"`
}

// pinProfileClock pins Date.now() for the profile code and answers the SDK's Date.parse from the table the real run
// recorded (text -> milliseconds, null for NaN); a text the run never parsed fails the test.
func pinProfileClock(t *testing.T, now int64, table map[string]any) {
	t.Helper()
	prevNow := profileNowMs
	profileNowMs = func() int64 { return now }
	var mu sync.Mutex
	sdk.SetHostHelpers(&sdk.HostHelpers{ParseDate: func(text string) (float64, bool) {
		mu.Lock()
		defer mu.Unlock()
		v, ok := table[text]
		if !ok {
			t.Errorf("Date.parse(%q) was never made by the real run", text)
			return 0, false
		}
		if v == nil {
			return 0, false
		}
		return v.(float64), true
	}})
	t.Cleanup(func() {
		sdk.SetHostHelpers(nil)
		profileNowMs = prevNow
	})
}

func profUnsetenv(t *testing.T, k string) {
	t.Helper()
	t.Setenv(k, "")
	os.Unsetenv(k)
}

var t0Placeholder = regexp.MustCompile(`\{\{T0([+-]\d+)?\}\}`)

func TestProfileDifferential(t *testing.T) {
	var f diffFile
	loadProfileGolden(t, "profile_differential.json", &f)
	if len(f.Scenarios) != len(f.Results) || len(f.Scenarios) < 140 {
		t.Fatalf("golden: %d scenarios, %d results", len(f.Scenarios), len(f.Results))
	}
	for i := range f.Scenarios {
		sc, want := f.Scenarios[i], f.Results[i]
		if sc.Name != want.Name {
			t.Fatalf("scenario %d is %q, result %q", i, sc.Name, want.Name)
		}
		t.Run(sc.Name, func(t *testing.T) {
			pinProfileClock(t, f.T0, f.DateTable)
			runDiffScenario(t, f.T0, sc, want)
		})
	}
}

func runDiffScenario(t *testing.T, t0 int64, sc diffScenario, want diffResult) {
	dir := t.TempDir()
	var (
		mu       sync.Mutex
		requests []diffRequest
		base     string
	)
	fill := func(s string) string {
		s = strings.ReplaceAll(s, "{{BASE}}", base)
		s = strings.ReplaceAll(s, "{{BASES}}", strings.Replace(base, "http:", "https:", 1))
		s = strings.ReplaceAll(s, "{{BASEUSER}}", strings.Replace(base, "//", "//u:p@", 1))
		s = strings.ReplaceAll(s, "{{DIR}}", dir)
		return t0Placeholder.ReplaceAllStringFunc(s, func(m string) string {
			d := int64(0)
			if inner := m[len("{{T0") : len(m)-2]; inner != "" {
				d, _ = strconv.ParseInt(inner, 10, 64)
			}
			return jsISOString(t0 + d)
		})
	}
	norm := func(s string) string {
		return strings.ReplaceAll(strings.ReplaceAll(s, base, "{{BASE}}"), dir, "{{DIR}}")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		req := diffRequest{Method: r.Method, Path: r.RequestURI, Body: string(body)}
		if a, ok := r.Header["Accept"]; ok {
			req.Accept = &a[0]
		}
		if c, ok := r.Header["Content-Type"]; ok {
			req.ContentType = &c[0]
		}
		mu.Lock()
		requests = append(requests, req)
		mu.Unlock()
		for _, rt := range sc.Routes {
			if rt.Method != r.Method || fill(rt.Path) != r.URL.Path {
				continue
			}
			payload := []byte(fill(rt.Body))
			if rt.BodyBase64 != "" {
				payload, _ = base64.StdEncoding.DecodeString(rt.BodyBase64)
			}
			ct := rt.CT
			if ct == "" {
				ct = "application/json"
			}
			w.Header().Set("Content-Type", ct)
			for k, v := range rt.Headers {
				w.Header().Set(k, fill(v))
			}
			w.WriteHeader(rt.Status)
			w.Write(payload)
			return
		}
		w.WriteHeader(404)
	}))
	t.Cleanup(srv.Close)
	base = srv.URL

	configRel := "config.json"
	if sc.UseHome {
		configRel = filepath.Join(".langsmith", "config.json")
	}
	configPath := filepath.Join(dir, configRel)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Dir(configPath), 0o755))
	switch {
	case sc.ConfigIsDir:
		must(os.Mkdir(configPath, 0o755))
	case sc.ConfigBase64 != "":
		b, err := base64.StdEncoding.DecodeString(sc.ConfigBase64)
		must(err)
		must(os.WriteFile(configPath, b, 0o644))
	case sc.Config != nil:
		must(os.WriteFile(configPath, []byte(fill(*sc.Config)), 0o644))
	}
	for rel, text := range sc.PreFiles {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		must(os.MkdirAll(filepath.Dir(p), 0o755))
		must(os.WriteFile(p, []byte(fill(text)), 0o644))
	}
	if sc.TmpIsDir {
		must(os.Mkdir(configPath+".tmp", 0o755))
	}
	for _, k := range []string{"LANGSMITH_CONFIG_FILE", "LANGSMITH_PROFILE", "HOME", "USERPROFILE"} {
		profUnsetenv(t, k)
	}
	if sc.UseHome {
		t.Setenv("HOME", dir)
	} else {
		t.Setenv("LANGSMITH_CONFIG_FILE", configPath)
	}
	for k, v := range sc.Env {
		t.Setenv(k, fill(v))
	}

	// load
	var got diffLoad
	cfg, err := loadProfileClientConfig()
	if err != nil {
		got.Error = err.Error()
	} else {
		fixup := func(p *string) *string {
			if p == nil {
				return nil
			}
			s := norm(*p)
			return &s
		}
		got = diffLoad{APIURL: fixup(cfg.APIURL), APIKey: fixup(cfg.APIKey), WorkspaceID: fixup(cfg.WorkspaceID),
			OAuthAccessToken: fixup(cfg.OAuthAccessToken), OAuthRefreshToken: fixup(cfg.OAuthRefreshToken), HasAuth: cfg.Auth != nil}
	}
	if !equalDiffLoad(got, want.Load) {
		t.Errorf("load:\n got %+v\nwant %+v", got, want.Load)
	}

	// ops
	auth := cfg.Auth
	var ops []diffOpResult
	hdr := func(h *authHeader) any {
		if h == nil {
			return nil
		}
		return map[string]any{"name": h.Name, "value": norm(h.Value)}
	}
	if auth != nil {
		t.Cleanup(auth.close)
		hc := srv.Client()
		for _, op := range sc.Ops {
			switch op.Op {
			case "current":
				h, err := auth.currentAuthHeader()
				ops = append(ops, profResultOf("current", hdr(h), err))
			case "auth":
				h, err := auth.getAuthHeader(context.Background(), hc)
				ops = append(ops, profResultOf("auth", hdr(h), err))
			case "authAll":
				res := make([]any, op.N)
				errs := make([]error, op.N)
				var wg sync.WaitGroup
				for i := range res {
					wg.Add(1)
					go func() {
						defer wg.Done()
						h, err := auth.getAuthHeader(context.Background(), hc)
						res[i], errs[i] = hdr(h), err
					}()
				}
				wg.Wait()
				for _, e := range errs {
					if e != nil {
						t.Errorf("authAll: %v", e)
					}
				}
				ops = append(ops, diffOpResult{Op: "authAll", Result: res})
			case "is":
				ops = append(ops, diffOpResult{Op: "is", Value: op.Value, Result: auth.isProfileAuthorizationHeader(op.Value)})
			case "write":
				must(os.WriteFile(configPath, []byte(fill(op.Text)), 0o644))
			case "delete":
				must(os.Remove(configPath))
			default:
				t.Fatalf("unknown op %q", op.Op)
			}
		}
		auth.close()
	}
	if !profEqualOps(ops, want.Ops) {
		t.Errorf("ops:\n got %s\nwant %s", profFmtOps(ops), profFmtOps(want.Ops))
	}

	// requests
	mu.Lock()
	gotReqs := slices.Clone(requests)
	mu.Unlock()
	for i := range gotReqs {
		gotReqs[i].Body = norm(gotReqs[i].Body)
	}
	if len(gotReqs) != len(want.Requests) {
		t.Errorf("requests: got %d want %d:\n got %+v\nwant %+v", len(gotReqs), len(want.Requests), gotReqs, want.Requests)
	} else {
		// The concurrent scenario may interleave nothing: one refresh; order is deterministic otherwise.
		for i := range gotReqs {
			g, w := gotReqs[i], want.Requests[i]
			if g.Method != w.Method || g.Path != w.Path || g.Body != w.Body || !profEqPtr(g.Accept, w.Accept) || !profEqPtr(g.ContentType, w.ContentType) {
				t.Errorf("request %d:\n got %+v (accept %v ct %v)\nwant %+v (accept %v ct %v)", i, g, profDeref(g.Accept), profDeref(g.ContentType), w, profDeref(w.Accept), profDeref(w.ContentType))
			}
		}
	}

	// the file left behind
	var gotConfig *string
	if b, err := os.ReadFile(configPath); err == nil {
		s := norm(decodeUTF8Replacing(b)) // the real run read the file as UTF-8 text
		gotConfig = &s
	}
	if !profEqPtr(gotConfig, want.Config) {
		t.Errorf("config file:\n got %v\nwant %v", profDeref(gotConfig), profDeref(want.Config))
	}
	if files := profListFiles(t, dir); !slices.Equal(files, want.Files) {
		t.Errorf("files: got %q want %q", files, want.Files)
	}
}

func profEqPtr(a, b *string) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }

func equalDiffLoad(a, b diffLoad) bool {
	return profEqPtr(a.APIURL, b.APIURL) && profEqPtr(a.APIKey, b.APIKey) && profEqPtr(a.WorkspaceID, b.WorkspaceID) &&
		profEqPtr(a.OAuthAccessToken, b.OAuthAccessToken) && profEqPtr(a.OAuthRefreshToken, b.OAuthRefreshToken) && a.HasAuth == b.HasAuth && a.Error == b.Error
}

func profResultOf(op string, result any, err error) diffOpResult {
	if err != nil {
		return diffOpResult{Op: op, Error: err.Error()}
	}
	return diffOpResult{Op: op, Result: result}
}

func profEqualOps(a, b []diffOpResult) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Op != b[i].Op || a[i].Error != b[i].Error || a[i].Value != b[i].Value || profFmtAny(a[i].Result) != profFmtAny(b[i].Result) {
			return false
		}
	}
	return true
}

func profFmtAny(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case map[string]any:
		return "{" + x["name"].(string) + ":" + x["value"].(string) + "}"
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = profFmtAny(e)
		}
		return "[" + strings.Join(parts, ",") + "]"
	case bool:
		return strconv.FormatBool(x)
	}
	return "?"
}

func profFmtOps(ops []diffOpResult) string {
	var sb strings.Builder
	for _, o := range ops {
		sb.WriteString(o.Op + "=" + profFmtAny(o.Result) + o.Error + "; ")
	}
	return sb.String()
}

func profListFiles(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	var walk func(rel string)
	walk = func(rel string) {
		entries, err := os.ReadDir(filepath.Join(dir, rel))
		if err != nil {
			t.Fatal(err)
		}
		names := make([]string, 0, len(entries))
		byName := map[string]os.DirEntry{}
		for _, e := range entries {
			names = append(names, e.Name())
			byName[e.Name()] = e
		}
		sort.Strings(names)
		for _, n := range names {
			r := n
			if rel != "" {
				r = rel + "/" + n
			}
			if byName[n].IsDir() {
				out = append(out, r+"/")
				walk(r)
			} else {
				out = append(out, r)
			}
		}
	}
	walk("")
	return out
}
