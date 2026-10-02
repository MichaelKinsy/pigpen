package pitypesafe

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const validKey = "ts_test_key_0123456789abcdef"

func isConfigError(err error, pattern string) bool {
	return hasCode(err, CodeConfiguration) && regexpMatch(pattern, err.Error())
}

func TestCredentials(t *testing.T) {
	tw(t, "credentials", "credentials live under Pi's agent directory", func(t *testing.T) {
		dir := isolate(t)
		// PiG's agent directory (PIG_CODING_AGENT_DIR), not Pi's fixed one: divergence D2, see port/PORT.md.
		if CredentialsPath() != filepath.Join(dir, "pi-typesafe", "auth.json") {
			t.Fatalf("path = %s", CredentialsPath())
		}
		t.Setenv("PIG_USE_PI_DIRS", "1")
		if want := filepath.Join(os.Getenv("PI_CODING_AGENT_DIR"), "pi-typesafe", "auth.json"); CredentialsPath() != want {
			t.Fatalf("with PIG_USE_PI_DIRS=1 the path is %s, got %s", want, CredentialsPath())
		}
	})
	tw(t, "credentials", "store, read, and clear with owner-only permissions", func(t *testing.T) {
		dir := isolate(t)
		if got, err := ResolveAPIKey(nil); got != nil || err != nil {
			t.Fatalf("empty store = %v, %v", got, err)
		}
		path, err := StoreAPIKey("  " + validKey + "\n")
		if err != nil || path != CredentialsPath() {
			t.Fatalf("store = %q, %v", path, err)
		}
		if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
			t.Errorf("file mode = %v", info.Mode().Perm())
		}
		if info, _ := os.Stat(filepath.Join(dir, "pi-typesafe")); info.Mode().Perm() != 0o700 {
			t.Errorf("dir mode = %v", info.Mode().Perm())
		}
		var stored map[string]string
		data, _ := os.ReadFile(path)
		_ = json.Unmarshal(data, &stored)
		if len(stored) != 1 || stored["apiKey"] != validKey {
			t.Errorf("stored = %s", data)
		}
		if got, _ := ResolveAPIKey(nil); got == nil || *got != (ResolvedKey{Key: validKey, Source: SourceStored}) {
			t.Errorf("resolved = %+v", got)
		}
		if !ClearStoredAPIKey() || ClearStoredAPIKey() {
			t.Error("clear must report true once, then false")
		}
		if k, _ := ReadStoredAPIKey(); k != "" {
			t.Error("cleared key is still readable")
		}
	})
	tw(t, "credentials", "environment variable takes precedence over the stored key", func(t *testing.T) {
		isolate(t)
		_, _ = StoreAPIKey(validKey)
		t.Setenv("TYPESAFE_API_KEY", "env_key_0123456789abcdef")
		if got, _ := ResolveAPIKey(nil); got == nil || *got != (ResolvedKey{Key: "env_key_0123456789abcdef", Source: SourceEnvironment}) {
			t.Fatalf("resolved = %+v", got)
		}
	})
	tw(t, "credentials", "implausible keys are rejected without saving", func(t *testing.T) {
		isolate(t)
		for _, value := range []any{"", "short", "has space in it 0123456789", "tab\tseparated0123456789", "ключ-with-non-ascii-0123456789", strings.Repeat("x", 513), 42, nil} {
			_, err := StoreAPIKey(value)
			text, _ := value.(string)
			if !hasCode(err, CodeValidation) || (len(text) >= 4 && strings.Contains(err.Error(), text)) {
				t.Errorf("StoreAPIKey(%v) = %v", value, err)
			}
		}
		if k, _ := ReadStoredAPIKey(); k != "" {
			t.Error("nothing may be saved")
		}
		if got, err := NormalizeAPIKey(" " + validKey + " "); err != nil || got != validKey {
			t.Errorf("normalize = %q, %v", got, err)
		}
	})
	tw(t, "credentials", "group- or world-readable credential files are refused", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX permission bits")
		}
		isolate(t)
		_, _ = StoreAPIKey(validKey)
		_ = os.Chmod(CredentialsPath(), 0o644)
		if _, err := ResolveAPIKey(nil); !isConfigError(err, `chmod 600`) {
			t.Fatalf("err = %v", err)
		}
	})
	tw(t, "credentials", "corrupt or unexpected files are treated as no key", func(t *testing.T) {
		isolate(t)
		for _, content := range []string{"not json", "[]", `{"apiKey": 5}`, "{}"} {
			writeFile(t, CredentialsPath(), content, 0o600)
			if k, err := ReadStoredAPIKey(); k != "" || err != nil {
				t.Errorf("%q -> %q, %v", content, k, err)
			}
		}
	})
	tw(t, "credentials", "createTypeSafe uses the stored key and listModels verifies it without spending the request budget", func(t *testing.T) {
		isolate(t)
		_, _ = StoreAPIKey(validKey)
		authorized := false
		client, err := New(Options{MaxRequests: 1, HTTPClient: doerFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.String() != "https://api.typesafe.ai/v1/models" {
				t.Errorf("url = %s", r.URL)
			}
			authorized = strings.Contains(r.Header.Get("Authorization"), validKey)
			return jsonResponse(200, map[string]any{"models": []any{map[string]any{"name": "jev-latest", "description": "", "release_date": "2026-01-01"}, map[string]any{"name": 7}}}, nil), nil
		})})
		if err != nil {
			t.Fatal(err)
		}
		names, err := client.ListModels(context.Background())
		if err != nil || len(names) != 1 || names[0] != "jev-latest" || !authorized || client.GetUsage().RequestsStarted != 0 {
			t.Fatalf("models = %v, %v, authorized=%v", names, err, authorized)
		}
		ClearStoredAPIKey()
		if _, err := New(Options{}); !isConfigError(err, `/typesafe login`) {
			t.Fatalf("err = %v", err)
		}
	})
	tw(t, "credentials", "invalid keys fail verification with a safe message", func(t *testing.T) {
		isolate(t)
		client, err := New(Options{APIKey: validKey, HTTPClient: doerFunc(func(*http.Request) (*http.Response, error) {
			return jsonResponse(401, map[string]any{"detail": "secret-body"}, nil), nil
		})})
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.ListModels(context.Background())
		ie, ok := err.(*IntegrationError)
		if !ok || ie.Status != 401 || strings.Contains(ie.Message, "secret-body") {
			t.Fatalf("err = %v", err)
		}
	})
	tw(t, "credentials", "keySituation is total and names every kind", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX permission bits")
		}
		isolate(t)
		if s, _ := KeySituationFor(nil); s != (KeySituation{Kind: KeyMissing}) {
			t.Fatalf("missing = %+v", s)
		}
		_, _ = StoreAPIKey(validKey)
		if s, _ := KeySituationFor(nil); s != (KeySituation{Kind: KeyStored, Key: validKey, Path: CredentialsPath()}) {
			t.Fatalf("stored = %+v", s)
		}
		_ = os.Chmod(CredentialsPath(), 0o644)
		s, _ := KeySituationFor(nil)
		if s.Kind != KeyUnusable || s.Path != CredentialsPath() || !strings.Contains(s.Reason, "chmod 600") {
			t.Fatalf("unusable = %+v", s)
		}
		if _, err := ResolveAPIKey(nil); err == nil || err.Error() != s.Reason {
			t.Fatalf("resolve error = %v", err)
		}
		t.Setenv("TYPESAFE_API_KEY", "  "+validKey+"  ")
		if s, _ := KeySituationFor(nil); s != (KeySituation{Kind: KeyEnvironment, Key: validKey, KeyEnv: "TYPESAFE_API_KEY"}) {
			t.Fatalf("environment = %+v", s)
		}
		// Environment values are trusted as-is; a wrong key fails at the API with its own advice.
		t.Setenv("TYPESAFE_API_KEY", "short")
		if s, _ := KeySituationFor(nil); s != (KeySituation{Kind: KeyEnvironment, Key: "short", KeyEnv: "TYPESAFE_API_KEY"}) {
			t.Fatalf("short = %+v", s)
		}
	})
	tw(t, "credentials", "keySituation for another backend reads only that backend's variable, never the TypeSafe store", func(t *testing.T) {
		isolate(t)
		_, _ = StoreAPIKey(validKey)
		t.Setenv("TYPESAFE_API_KEY", validKey)
		// A stored or TypeSafe-environment key is not an OpenRouter key.
		if s, _ := KeySituationFor("openrouter"); s != (KeySituation{Kind: KeyMissing}) {
			t.Fatalf("openrouter = %+v", s)
		}
		if got, _ := ResolveAPIKey("openrouter"); got != nil {
			t.Fatalf("resolved = %+v", got)
		}
		t.Setenv("OPENROUTER_API_KEY", "  sk-or-test-0123456789abcdef  ")
		s, _ := KeySituationFor("openrouter")
		if s != (KeySituation{Kind: KeyEnvironment, Key: "sk-or-test-0123456789abcdef", KeyEnv: "OPENROUTER_API_KEY"}) || KeySourceLabel(s) != "OPENROUTER_API_KEY" {
			t.Fatalf("openrouter env = %+v", s)
		}
		if got, _ := ResolveAPIKey("openrouter"); got == nil || *got != (ResolvedKey{Key: "sk-or-test-0123456789abcdef", Source: SourceEnvironment}) {
			t.Fatalf("resolved = %+v", got)
		}
		// The OpenRouter variable does not leak into the TypeSafe resolution either.
		t.Setenv("TYPESAFE_API_KEY", "")
		if s, _ := KeySituationFor(nil); s.Kind != KeyStored {
			t.Fatalf("typesafe = %+v", s)
		}
		if _, err := KeySituationFor("bogus"); !isConfigError(err, `Unknown judgment backend`) {
			t.Fatalf("err = %v", err)
		}
	})
	tw(t, "credentials", "keySourceLabel names each source", func(t *testing.T) {
		cases := map[KeySituation]string{
			{Kind: KeyEnvironment, Key: "k"}:                               "TYPESAFE_API_KEY",
			{Kind: KeyEnvironment, Key: "k", KeyEnv: "OPENROUTER_API_KEY"}: "OPENROUTER_API_KEY",
			{Kind: KeyStored, Key: "k", Path: "/tmp/auth.json"}:            "/typesafe login",
			{Kind: KeyMissing}: "no key",
			{Kind: KeyUnusable, Path: "/tmp/auth.json", Reason: "r"}: "unusable key",
		}
		for s, want := range cases {
			if got := KeySourceLabel(s); got != want {
				t.Errorf("%+v -> %q, want %q", s, got, want)
			}
		}
	})
}
