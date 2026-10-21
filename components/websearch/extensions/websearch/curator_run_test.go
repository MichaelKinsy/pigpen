package websearch

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Twins of curator-run and auto-summary-source, and the command tests.

func TestUpstream_curator_run(t *testing.T) {
	const F = "curator-run"
	resolve := func(s *CuratorRunState, requested any, hasRequested bool, configured any, ui bool) string {
		return s.Resolve(requested, hasRequested, configured, ui)
	}
	tw(t, F, "fresh installs default to no curator while configured and explicit workflows keep their meanings", func(t *testing.T) {
		var s CuratorRunState
		if resolve(&s, nil, false, nil, true) != "none" || resolve(&s, nil, false, "invalid", true) != "none" {
			t.Fatal("an unconfigured or invalid workflow is not none")
		}
		for _, workflow := range []string{"none", "auto-summary", "summary-review"} {
			if got := resolve(&s, nil, false, workflow, true); got != workflow {
				t.Errorf("configured %s: %s", workflow, got)
			}
			if got := resolve(&s, workflow, true, "none", true); got != workflow {
				t.Errorf("requested %s: %s", workflow, got)
			}
		}
		if resolve(&s, nil, false, "summary-review", false) != "none" {
			t.Fatal("summary-review without a UI")
		}
		if resolve(&s, "auto-summary", true, "summary-review", false) != "auto-summary" {
			t.Fatal("auto-summary without a UI")
		}
	})
	tw(t, F, "approval changes only inherited summary-review workflows for the current prompt", func(t *testing.T) {
		var s CuratorRunState
		s.ApproveRemainingSearches()
		for _, c := range []struct {
			configured any
			want       string
		}{{nil, "none"}, {"summary-review", "auto-summary"}, {"none", "none"}, {"auto-summary", "auto-summary"}} {
			if got := resolve(&s, nil, false, c.configured, true); got != c.want {
				t.Errorf("configured %v: %s, want %s", c.configured, got, c.want)
			}
		}
		for _, explicit := range []string{"none", "auto-summary", "summary-review"} {
			if got := resolve(&s, explicit, true, "summary-review", true); got != explicit {
				t.Errorf("explicit %s: %s", explicit, got)
			}
		}
	})
	tw(t, F, "registered lifecycle preserves approval across internal turns and resets at run boundaries", func(t *testing.T) {
		// The runs the lifecycle ends are exactly the original's five events, and the extension
		// registers a handler for each (TestExtensionResetsTheApprovalAtRunBoundaries drives them).
		if !reflect.DeepEqual(curatorRunResetEvents, []string{"before_agent_start", "agent_settled", "session_start", "session_tree", "session_shutdown"}) {
			t.Fatal(curatorRunResetEvents)
		}
		var s CuratorRunState
		s.Reset()
		s.ApproveRemainingSearches()
		if resolve(&s, nil, false, "summary-review", true) != "auto-summary" || resolve(&s, "summary-review", true, "none", true) != "summary-review" {
			t.Fatal("the approval is not kept across internal turns")
		}
		s.Reset() // agent_settled
		if resolve(&s, nil, false, "summary-review", true) != "summary-review" {
			t.Fatal("agent_settled did not end the approval")
		}
		s.ApproveRemainingSearches()
		s.Reset() // session_tree
		if resolve(&s, nil, false, "summary-review", true) != "summary-review" {
			t.Fatal("session_tree did not end the approval")
		}
	})
}

func TestUpstream_auto_summary_source(t *testing.T) {
	const F = "auto-summary-source"
	tw(t, F, "web_search accepts auto-summary workflow in schema and config resolution", func(t *testing.T) {
		r, _ := newRuntime(t, ``)
		workflow := prop(t, mustTool(t, r, "web_search"), "workflow")
		if !reflect.DeepEqual(workflow["enum"], []any{"none", "summary-review", "auto-summary"}) {
			t.Fatal(workflow)
		}
		if ResolveWebSearchWorkflow("auto-summary", true) != "auto-summary" {
			t.Fatal("auto-summary")
		}
		// /curator takes the same three values.
		e := newCuratorEnv(t, ``)
		for _, arg := range []string{"none", "summary-review", "auto-summary"} {
			e.rt.CuratorCommand(arg)
			if got := ResolveWebSearchWorkflow(currentRoot()["workflow"], true); got != arg {
				t.Errorf("/curator %s saved %s", arg, got)
			}
		}
	})
	tw(t, F, "auto-summary skips curator and reuses summary model fallback plumbing", func(t *testing.T) {
		// Behaviour: auto-summary opens no curator, resolves the summary model from the same choices
		// the curator offers (a configured selector with a thinking suffix is kept as it was
		// written), and returns an auto-summary result.
		e := newCuratorEnv(t, `{"summaryModel":"anthropic/claude-haiku-4-5:high"}`)
		choices, def, err := loadSummaryModelChoices(e.side)
		noErr(t, err)
		if def == nil || *def != "anthropic/claude-haiku-4-5:high" {
			t.Fatalf("%v", def)
		}
		var values []string
		for _, c := range choices {
			values = append(values, c.Value)
		}
		if !reflect.DeepEqual(values, []string{"anthropic/claude-haiku-4-5", "anthropic/claude-haiku-4-5:high"}) {
			t.Fatal(values)
		}
		useFakeProvider(t, "brave", answerWith("a", source("S", "https://example.com/s")))
		out := e.runWebSearch("c", map[string]any{"query": "q", "provider": "brave", "workflow": "auto-summary"}).result()
		summary, _ := out.Details["summary"].(map[string]any)
		if summary["workflow"] != "auto-summary" || out.Text() != "generated summary" {
			t.Fatalf("%q %v", out.Text(), out.Details)
		}
		noURL(t, e.opened)
	})
	tw(t, F, "README documents auto-summary", func(t *testing.T) {
		readme := readPackageReadme(t)
		matchRE(t, `workflow: "auto-summary"`, readme)
		matchRE(t, `generate a summary without opening the curator`, readme)
	})
}

func TestSummaryModelChoicesFollowTheScopeAndTheCurrentModel(t *testing.T) {
	e := newCuratorEnv(t, ``)
	other := scModel("openai", "gpt-5-mini")
	hidden := scModel("google", "gemini-3.6-flash")
	e.reg.models = []map[string]any{scModel("anthropic", curatorSummaryModel), other, hidden}
	e.side.scoped = []map[string]any{scModel("anthropic", curatorSummaryModel), other}
	e.side.model = scModel("deepseek", "deepseek-v4-flash")
	choices, def, err := loadSummaryModelChoices(e.side)
	noErr(t, err)
	var values []string
	for _, c := range choices {
		values = append(values, c.Value+"="+c.Label)
	}
	// The scope hides the third model; the current model is outside the scope, so it is not added.
	if !reflect.DeepEqual(values, []string{"anthropic/claude-haiku-4-5=anthropic/claude-haiku-4-5", "openai/gpt-5-mini=openai/gpt-5-mini"}) || def == nil || *def != "anthropic/claude-haiku-4-5" {
		t.Fatalf("%v %v", values, def)
	}
	// A configured model that is not available is ignored; the first preferred one wins.
	_, dir := isolate(t)
	writeConfig(t, dir, `{"summaryModel":"nobody/nothing"}`)
	_, def, err = loadSummaryModelChoices(e.side)
	noErr(t, err)
	if def == nil || *def != "anthropic/claude-haiku-4-5" {
		t.Fatal(def)
	}
}

func TestCuratorCommandTogglesAndReportsTheWorkflow(t *testing.T) {
	e := newCuratorEnv(t, `{"commands":{"curator":{"enabled":true}},"workflow":"summary-review","other":1}`)
	path := filepath.Join(ConfigDir(), "web-search.json")
	check := func(arg, workflow, label string) {
		t.Helper()
		before := len(e.host.messages())
		e.rt.CuratorCommand(arg)
		msgs := e.host.messages()
		if len(msgs) != before+1 {
			t.Fatalf("%q: %d messages", arg, len(msgs))
		}
		m := msgs[len(msgs)-1]
		if m.customType != "curator-config" || m.text != label || m.details["workflow"] != workflow || m.triggerTurn || m.deliverAs != "followUp" {
			t.Fatalf("%q: %+v", arg, m)
		}
		if got := currentRoot()["workflow"]; got != workflow {
			t.Fatalf("saved %v", got)
		}
	}
	// No argument toggles: summary-review (configured) becomes none and back.
	check("", "none", "Curator disabled — web_search will return raw results")
	check("  ", "summary-review", "Curator enabled — web_search will open curator and auto-generate a summary draft")
	check("OFF", "none", "Curator disabled — web_search will return raw results")
	check("on", "summary-review", "Curator enabled — web_search will open curator and auto-generate a summary draft")
	check("auto-summary", "auto-summary", "Auto-summary enabled — web_search will generate a summary without opening the curator")
	// The other keys of the file are kept, in place.
	if got := readFile(t, path); !strings.HasPrefix(got, "{\n  \"commands\": {\n    \"curator\": {\n      \"enabled\": true\n    }\n  },\n  \"workflow\": \"auto-summary\",\n  \"other\": 1\n}") {
		t.Fatalf("%q", got)
	}
	before := len(e.host.messages())
	e.rt.CuratorCommand("sideways")
	notes := e.host.notifications()
	if n := notes[len(notes)-1]; n.level != "error" || n.message != "Unknown option: sideways. Use on, off, summary-review, or auto-summary." || len(e.host.messages()) != before {
		t.Fatalf("%+v", n)
	}
}

func TestCuratorCommandReportsAConfigThatCannotBeSaved(t *testing.T) {
	e := newCuratorEnv(t, ``)
	writeConfig(t, ConfigDir(), `[1]`)
	e.rt.CuratorCommand("on")
	notes := e.host.notifications()
	if len(notes) != 1 || notes[0].level != "error" || !strings.HasPrefix(notes[0].message, "Failed to save config: Invalid config in ") || len(e.host.messages()) != 0 {
		t.Fatalf("%+v", notes)
	}
}

// fakeGoogle is a GoogleAccountSource.
type fakeGoogle struct {
	allowed    bool
	cookies    any
	diagnostic string
	details    *BrowserCookieDiagnosticDetails
	email      string
}

func (f fakeGoogle) CookieAccessAllowed() (bool, error)                 { return f.allowed, nil }
func (f fakeGoogle) Available(context_ context.Context) (any, error)    { return f.cookies, nil }
func (f fakeGoogle) Diagnostic() string                                 { return f.diagnostic }
func (f fakeGoogle) DiagnosticDetails() *BrowserCookieDiagnosticDetails { return f.details }
func (f fakeGoogle) ActiveEmail(context_ context.Context, _ any) string { return f.email }

func TestGoogleAccountCommand(t *testing.T) {
	e := newCuratorEnv(t, ``)
	last := func() hostCustomMessage {
		t.Helper()
		msgs := e.host.messages()
		if len(msgs) == 0 {
			t.Fatal("no message")
		}
		return msgs[len(msgs)-1]
	}
	run := func(src GoogleAccountSource) hostCustomMessage {
		t.Helper()
		defer SetGoogleAccountSource(src)()
		noErr(t, e.rt.GoogleAccountCommand(bg()))
		m := last()
		if m.customType != "google-account" || !m.triggerTurn || m.deliverAs != "followUp" {
			t.Fatalf("%+v", m)
		}
		return m
	}
	m := run(fakeGoogle{})
	if m.text != "Gemini Web browser cookie access is disabled. Set allowBrowserCookies: true in "+ConfigPath()+" to enable it." ||
		m.details["available"] != false || m.details["cookieAccessAllowed"] != false {
		t.Fatalf("%+v", m)
	}
	m = run(fakeGoogle{allowed: true})
	if m.text != "Gemini Web is unavailable. Sign into gemini.google.com in a supported Chromium-based browser." || m.details["available"] != false || m.details["cookieAccessAllowed"] != true {
		t.Fatalf("%+v", m)
	}
	m = run(fakeGoogle{allowed: true, diagnostic: "no cookies", details: &BrowserCookieDiagnosticDetails{Message: "no cookies", Attempts: []BrowserCookieAttempt{{"chrome", "Default", "no-cookies"}, {"brave", "Profile 1", "locked"}}}})
	if m.text != "Gemini Web is unavailable: no cookies Attempted browser profiles: chrome/Default (no-cookies), brave/Profile 1 (locked)." || m.details["diagnostic"] != "no cookies" || m.details["cookieDiagnostic"] == nil {
		t.Fatalf("%+v", m)
	}
	m = run(fakeGoogle{allowed: true, cookies: map[string]string{"a": "b"}, email: "me@example.com"})
	if m.text != "Active Google account: me@example.com" || m.details["available"] != true || m.details["email"] != "me@example.com" {
		t.Fatalf("%+v", m)
	}
	m = run(fakeGoogle{allowed: true, cookies: map[string]string{"a": "b"}})
	if m.text != "Gemini Web is available, but the active Google account could not be determined." || m.details["email"] != nil {
		t.Fatalf("%+v", m)
	}
	// A diagnostic-free refusal reports null for both, as JSON.stringify of the original's details does.
	m = run(fakeGoogle{allowed: true})
	if v, ok := m.details["diagnostic"]; !ok || v != nil {
		t.Fatalf("%+v", m.details)
	}
	if v, ok := m.details["cookieDiagnostic"]; !ok || v != nil {
		t.Fatalf("%+v", m.details)
	}
}

// The default source of /google-account is the cookie reader of the authenticated-fetch slice (chrome-cookies.ts,
// gemini-web.ts): the command finds the signed-in profile through the host command runner and asks Gemini for the
// account, with nothing installed by the test.
func TestGoogleAccountCommandReadsTheBrowserCookies(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the canned profile is the macOS and Linux layout; the Windows key store has its own twins")
	}
	e := newCuratorEnv(t, ``)
	home, _ := isolate(t)
	t.Setenv("PI_ALLOW_BROWSER_COOKIES", "1")
	db := filepath.Join(browserBase(home, "Chrome", runtime.GOOS), "Default", "Cookies")
	noErr(t, os.MkdirAll(filepath.Dir(db), 0o755))
	noErr(t, os.WriteFile(db, []byte("placeholder"), 0o600))
	rows := `[{"name":"__Secure-1PSID","value":"sid","host_key":".google.com","path":"/","expires_utc":0,"encrypted_value_hex":""},` +
		`{"name":"__Secure-1PSIDTS","value":"ts","host_key":".google.com","path":"/","expires_utc":0,"encrypted_value_hex":""}]`
	t.Cleanup(SetExecRunner(func(_ context.Context, command string, _ []string, _ time.Duration) (ExecResult, error) {
		switch command {
		case "sqlite3":
			return ExecResult{Stdout: rows}, nil
		case "security", "secret-tool":
			return ExecResult{Stdout: "peanuts"}, nil
		}
		return ExecResult{Code: 1}, nil
	}))
	calls := useGeminiWeb(t, func(r GeminiWebRequest) (*http.Response, error) {
		if r.URL == "https://gemini.google.com/app" {
			return webResp(200, `<html>"oPEP7c":"Me@Example.com"</html>`, nil), nil
		}
		t.Errorf("unexpected request %s", r.URL)
		return nil, http.ErrNotSupported
	})
	noErr(t, e.rt.GoogleAccountCommand(bg()))
	msgs := e.host.messages()
	if len(msgs) != 1 || msgs[0].customType != "google-account" || msgs[0].text != "Active Google account: Me@Example.com" ||
		msgs[0].details["available"] != true || msgs[0].details["email"] != "Me@Example.com" {
		t.Fatalf("%+v", msgs)
	}
	if len(*calls) != 1 || !strings.Contains((*calls)[0].Cookie, "__Secure-1PSID=sid") {
		t.Fatalf("calls %+v", *calls)
	}

	// Cookie access on but no profile with the Google cookies: the diagnostic and every attempt are reported.
	noErr(t, os.Remove(db))
	resetCookieState()
	noErr(t, e.rt.GoogleAccountCommand(bg()))
	msgs = e.host.messages()
	last := msgs[len(msgs)-1]
	if last.details["available"] != false || last.details["cookieAccessAllowed"] != true || !strings.HasPrefix(last.text, "Gemini Web is unavailable") {
		t.Fatalf("%+v", last)
	}
	if _, ok := last.details["cookieDiagnostic"]; !ok {
		t.Fatalf("%+v", last.details)
	}

	// Opt-in off: the command only says so.
	t.Setenv("PI_ALLOW_BROWSER_COOKIES", "0")
	t.Setenv("FEYNMAN_ALLOW_BROWSER_COOKIES", "")
	noErr(t, e.rt.GoogleAccountCommand(bg()))
	msgs = e.host.messages()
	if last = msgs[len(msgs)-1]; last.details["cookieAccessAllowed"] != false || !strings.HasPrefix(last.text, "Gemini Web browser cookie access is disabled.") {
		t.Fatalf("%+v", last)
	}
}
