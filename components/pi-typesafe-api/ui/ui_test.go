package ui_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	pitypesafe "github.com/MichaelKinsy/pigpen/components/pi-typesafe-api"
	"github.com/MichaelKinsy/pigpen/components/pi-typesafe-api/ui"
)

func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	agent := filepath.Join(dir, "agent")
	t.Setenv("HOME", dir)
	t.Setenv("PIG_HOME", filepath.Join(dir, "pighome"))
	t.Setenv("PIG_CODING_AGENT_DIR", agent)
	t.Setenv("PIG_USE_PI_DIRS", "")
	for _, name := range []string{"TYPESAFE_API_KEY", "OPENROUTER_API_KEY"} {
		t.Setenv(name, "")
	}
	return agent
}

type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

// loginRig runs a test extension whose /login command calls the helpers under test.
type loginRig struct {
	t          *testing.T
	host       *Host
	custom     any // nil: cancelled; string: typed key; "unsupported": no custom components
	inputText  string
	modelCalls atomic.Int32
	result     *ui.LoginResult
	ensured    *ui.EnsureResult
	err        error
	backend    any
	ensure     bool
}

func newLoginRig(t *testing.T, hasUI bool) *loginRig {
	t.Helper()
	r := &loginRig{t: t}
	opts := ui.LoginOptions{HTTPClient: doerFunc(func(req *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(req.URL.Path, "/v1/models") {
			t.Errorf("unexpected request to %s", req.URL)
			return nil, io.ErrUnexpectedEOF
		}
		r.modelCalls.Add(1)
		body, _ := json.Marshal(map[string]any{"models": []any{map[string]any{"name": "jev-latest", "description": "", "release_date": "2026-01-01"}, map[string]any{"name": "jev-preview", "description": "", "release_date": "2026-01-01"}}})
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(body))}, nil
	})}
	ext := sdk.New("ui-test")
	ext.Command("run", "runs the helper under test", func(ctx sdk.Context, _ string) error {
		if r.ensure {
			r.ensured, r.err = ui.EnsureAPIKey(ctx, r.backend, opts)
		} else {
			r.result, r.err = ui.LoginWithPrompt(ctx, opts)
		}
		return nil
	})
	r.host = StartHost(t, ext, HostOptions{HasUI: &hasUI, OnCall: func(method string, args map[string]any) (map[string]any, string) {
		switch method {
		case "ui.custom":
			switch v := r.custom.(type) {
			case nil:
				return map[string]any{"ok": false}, ""
			case string:
				if v == "unsupported" {
					return nil, "custom components are unavailable"
				}
				return map[string]any{"ok": true, "result": v}, ""
			}
		case "ui.input":
			if r.custom != "unsupported" {
				return nil, "plain input must not be used when custom UI exists"
			}
			return map[string]any{"text": r.inputText, "ok": true}, ""
		}
		return nil, ""
	}})
	return r
}

func (r *loginRig) run() {
	r.t.Helper()
	r.result, r.ensured, r.err = nil, nil, nil
	if failure := r.host.Command("run", ""); failure != "" {
		r.t.Fatalf("command failed: %s", failure)
	}
}

func isCode(err error, code pitypesafe.ErrorCode) bool {
	ie, ok := err.(*pitypesafe.IntegrationError)
	return ok && ie.Code == code
}

func TestLogin(t *testing.T) {
	tw(t, "login", "loginWithPrompt cancels cleanly, rejects bad keys, and stores a verified key with owner-only permissions", func(t *testing.T) {
		agent := isolate(t)
		stored := filepath.Join(agent, "pi-typesafe", "auth.json")
		r := newLoginRig(t, true)
		r.run()
		if r.result != nil || r.err != nil {
			t.Fatalf("cancel: %v %v", r.result, r.err)
		}
		if _, err := os.Stat(stored); err == nil {
			t.Fatal("nothing may be saved on cancel")
		}
		r.custom = "nope"
		r.run()
		if !isCode(r.err, pitypesafe.CodeValidation) || strings.Contains(r.err.Error(), "nope") || r.modelCalls.Load() != 0 {
			t.Fatalf("bad key: %v", r.err)
		}
		r.custom = "ts_live_key_0123456789abcdef"
		r.run()
		if r.err != nil || r.result == nil || *r.result != (ui.LoginResult{Path: stored, Models: 2}) || r.modelCalls.Load() != 1 {
			t.Fatalf("login: %+v %v", r.result, r.err)
		}
		if info, _ := os.Stat(stored); runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
			t.Errorf("mode = %v", info.Mode().Perm())
		}
	})
	tw(t, "login", "loginWithPrompt refuses when the environment key would shadow the store, or without a UI", func(t *testing.T) {
		agent := isolate(t)
		r := newLoginRig(t, true)
		t.Setenv("TYPESAFE_API_KEY", "env-key-0123456789")
		r.custom = "ts_live_key_0123456789abcdef"
		r.run()
		if !isCode(r.err, pitypesafe.CodeConfiguration) || !strings.Contains(r.err.Error(), "TYPESAFE_API_KEY") {
			t.Fatalf("env: %v", r.err)
		}
		t.Setenv("TYPESAFE_API_KEY", "")
		headless := newLoginRig(t, false)
		headless.run()
		if headless.err == nil || !regexp.MustCompile(`interactive`).MatchString(headless.err.Error()) {
			t.Fatalf("headless: %v", headless.err)
		}
		if _, err := os.Stat(filepath.Join(agent, "pi-typesafe", "auth.json")); err == nil {
			t.Fatal("nothing may be saved")
		}
	})
	tw(t, "login", "ensureApiKey reports an existing key without prompting, otherwise logs in", func(t *testing.T) {
		agent := isolate(t)
		stored := filepath.Join(agent, "pi-typesafe", "auth.json")
		r := newLoginRig(t, true)
		r.ensure = true
		t.Setenv("TYPESAFE_API_KEY", "env-key-0123456789")
		r.custom = "must-not-be-used-0123456789"
		r.run()
		if r.err != nil || r.ensured == nil || r.ensured.Source != pitypesafe.SourceEnvironment || r.ensured.Login != nil {
			t.Fatalf("env: %+v %v", r.ensured, r.err)
		}
		if _, err := os.Stat(stored); err == nil {
			t.Fatal("an existing key must not prompt or store")
		}
		t.Setenv("TYPESAFE_API_KEY", "")
		r.custom = nil
		r.run()
		if r.ensured != nil || r.err != nil {
			t.Fatalf("cancelled prompt: %+v %v", r.ensured, r.err)
		}
		r.custom = "ts_live_key_0123456789abcdef"
		r.run()
		want := ui.EnsureResult{Source: pitypesafe.SourceStored, Login: &ui.LoginResult{Path: stored, Models: 2}}
		if r.err != nil || r.ensured == nil || r.ensured.Source != want.Source || *r.ensured.Login != *want.Login {
			t.Fatalf("login: %+v %v", r.ensured, r.err)
		}
		r.custom = "another-key-that-must-not-replace-it"
		r.run()
		if r.err != nil || r.ensured == nil || r.ensured.Source != pitypesafe.SourceStored || r.ensured.Login != nil || r.modelCalls.Load() != 1 {
			t.Fatalf("second call reuses the stored key: %+v %v", r.ensured, r.err)
		}
	})
	tw(t, "login", "ensureApiKey for another backend uses its environment variable and never opens the TypeSafe login", func(t *testing.T) {
		agent := isolate(t)
		r := newLoginRig(t, true)
		r.ensure = true
		// A stored TypeSafe key does not satisfy OpenRouter, and the prompt must not run: it would verify against api.typesafe.ai.
		r.custom = "ts_live_key_0123456789abcdef"
		r.run()
		if r.err != nil || r.ensured == nil || r.ensured.Login == nil || r.ensured.Login.Path != filepath.Join(agent, "pi-typesafe", "auth.json") {
			t.Fatalf("typesafe login: %+v %v", r.ensured, r.err)
		}
		r.backend = "openrouter"
		r.run()
		if !isCode(r.err, pitypesafe.CodeConfiguration) || !strings.Contains(r.err.Error(), "OPENROUTER_API_KEY") || r.modelCalls.Load() != 1 {
			t.Fatalf("openrouter: %v models=%d", r.err, r.modelCalls.Load())
		}
		t.Setenv("OPENROUTER_API_KEY", "sk-or-0123456789abcdef")
		r.run()
		if r.err != nil || r.ensured == nil || r.ensured.Source != pitypesafe.SourceEnvironment || r.modelCalls.Load() != 1 {
			t.Fatalf("openrouter env: %+v %v", r.ensured, r.err)
		}
	})
}

func stripANSI(s string) string { return regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(s, "") }

func TestKeyPrompt(t *testing.T) {
	tw(t, "key-prompt", "typed key is masked on screen and returned on Enter", func(t *testing.T) {
		p := ui.NewKeyPrompt()
		for _, c := range "ts_secret_key_0123456789" {
			if res, err := p.HandleInput(string(c)); err != nil || res.Done {
				t.Fatalf("typing ended early: %+v %v", res, err)
			}
		}
		frame := p.Render(60)
		res, err := p.HandleInput("\r")
		if err != nil || !res.Done || res.Value != "ts_secret_key_0123456789" {
			t.Fatalf("enter: %+v %v", res, err)
		}
		screen := strings.Join(frame, "\n")
		if strings.Contains(screen, "ts_secret") || !strings.Contains(screen, strings.Repeat("•", 24)) || !strings.Contains(screen, "TypeSafe API key") {
			t.Fatalf("screen = %q", screen)
		}
		for _, line := range frame {
			if utf8.RuneCountInString(stripANSI(line)) > 60+8 {
				t.Errorf("line too wide: %q", line)
			}
		}
	})
	tw(t, "key-prompt", "Escape cancels without a value", func(t *testing.T) {
		p := ui.NewKeyPrompt()
		_, _ = p.HandleInput("a")
		res, err := p.HandleInput("\x1b")
		if err != nil || !res.Done || res.Value != nil {
			t.Fatalf("escape: %+v %v", res, err)
		}
	})
	tw(t, "key-prompt", "falls back to Pi's plain input when custom UI is unavailable", func(t *testing.T) {
		isolate(t)
		r := newLoginRig(t, true)
		r.custom = "unsupported"
		r.inputText = "  ts_plain_key_0123456789  "
		var got string
		var ok bool
		ext := sdk.New("prompt-test")
		ext.Command("prompt", "", func(ctx sdk.Context, _ string) error {
			var err error
			got, ok, err = ui.PromptForAPIKey(ctx)
			return err
		})
		host := StartHost(t, ext, HostOptions{OnCall: func(method string, _ map[string]any) (map[string]any, string) {
			switch method {
			case "ui.custom":
				return nil, "custom components are unavailable"
			case "ui.input":
				return map[string]any{"text": r.inputText, "ok": true}, ""
			}
			return nil, ""
		}})
		if failure := host.Command("prompt", ""); failure != "" || !ok || got != r.inputText {
			t.Fatalf("fallback: %q %v %q", got, ok, failure)
		}
	})
}

func TestKeyPromptEditing(t *testing.T) {
	p := ui.NewKeyPrompt()
	_, _ = p.HandleInput("\x1b[200~ts_pasted_0123456789\x1b[201~")
	_, _ = p.HandleInput("X")
	_, _ = p.HandleInput("\x7f")
	_, _ = p.HandleInput("\x1b[A") // an arrow key is ignored
	res, _ := p.HandleInput("\r")
	if res.Value != "ts_pasted_0123456789" {
		t.Fatalf("value = %q", res.Value)
	}
	if got := ui.NewKeyPrompt().Render(4); len(got) < 3 {
		t.Fatalf("a narrow render still draws every line: %v", got)
	}
}
