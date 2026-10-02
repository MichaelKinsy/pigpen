package pitypesafe

import (
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

func mustAuth(t *testing.T, backend any) AuthState {
	t.Helper()
	s, err := GetAuthState(AuthOptions{Backend: backend})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func mustDescribe(t *testing.T, s AuthState) AuthReport {
	t.Helper()
	r, err := DescribeAuth(s)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestAuth(t *testing.T) {
	tw(t, "auth", "no key at all is an error the consumer cannot mistake for a working setup", func(t *testing.T) {
		isolate(t)
		s := mustAuth(t, nil)
		if s.Kind != KeyMissing || s.KeyName != "no key" || s.Usable || s.Verified {
			t.Fatalf("state = %+v", s)
		}
		r := mustDescribe(t, s)
		if r.Level != LevelError || !strings.Contains(r.Text, "every Jev judgment is skipped") {
			t.Fatalf("report = %+v", r)
		}
	})
	tw(t, "auth", "another backend reports its own key and never the TypeSafe login hint", func(t *testing.T) {
		isolate(t)
		t.Setenv("TYPESAFE_API_KEY", "env-key-0123456789abcdef")
		if mustAuth(t, nil).Backend != "typesafe" {
			t.Error("the default backend is typesafe")
		}
		missing := mustAuth(t, "openrouter")
		if missing.Backend != "openrouter" || missing.Kind != KeyMissing || missing.Usable {
			t.Fatalf("missing = %+v", missing)
		}
		r := mustDescribe(t, missing)
		if r.Level != LevelError || !strings.HasPrefix(r.Text, "OpenRouter key: missing") || !strings.Contains(r.Text, "OPENROUTER_API_KEY is set") || strings.Contains(r.Text, "/typesafe login") {
			t.Fatalf("report = %+v", r)
		}
		t.Setenv("OPENROUTER_API_KEY", "sk-or-0123456789abcdef")
		present := mustAuth(t, "openrouter")
		if present.Kind != KeyEnvironment || present.KeyName != "OPENROUTER_API_KEY" || !present.Usable || !strings.HasPrefix(mustDescribe(t, present).Text, "OpenRouter key: OPENROUTER_API_KEY") {
			t.Fatalf("present = %+v", present)
		}
	})
	tw(t, "auth", "an environment key is usable but unverified until something proves it", func(t *testing.T) {
		isolate(t)
		t.Setenv("TYPESAFE_API_KEY", "env-key-0123456789abcdef")
		before := mustDescribe(t, mustAuth(t, nil))
		if before.Level != LevelWarning || !strings.Contains(before.Text, "TYPESAFE_API_KEY") || !strings.Contains(before.Text, "not verified yet") {
			t.Fatalf("before = %+v", before)
		}
		RecordAuthVerified(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
		s := mustAuth(t, nil)
		if !s.Usable || !s.Verified || s.VerifiedAt != "2026-01-01T00:00:00.000Z" || mustDescribe(t, s).Level != LevelOK {
			t.Fatalf("state = %+v", s)
		}
		if info, _ := os.Stat(AuthStatePath()); runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
			t.Errorf("mode = %v", info.Mode().Perm())
		}
	})
	tw(t, "auth", "a rejected key degrades loudly and a later success clears it", func(t *testing.T) {
		isolate(t)
		_, _ = StoreAPIKey("stored-key-0123456789abcdef")
		RecordAuthVerified(time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))
		msg := "TypeSafe returned HTTP 401. Check TYPESAFE_API_KEY. No automatic retry was made."
		RecordAuthFailure(&IntegrationError{Code: CodeHTTP, Message: msg, Status: 401}, time.Date(2026, 1, 2, 1, 0, 0, 0, time.UTC))
		d := mustAuth(t, nil)
		want := AuthFailure{Code: CodeHTTP, Status: 401, Message: msg, At: "2026-01-02T01:00:00.000Z"}
		if d.Kind != KeyStored || d.Usable || d.Verified || d.LastFailure == nil || *d.LastFailure != want {
			t.Fatalf("degraded = %+v", d)
		}
		if r := mustDescribe(t, d); r.Level != LevelError || !strings.Contains(r.Text, "was rejected") {
			t.Fatalf("report = %+v", r)
		}
		// A successful request proves the key again and drops the failure.
		RecordAuthVerified(time.Date(2026, 1, 2, 2, 0, 0, 0, time.UTC))
		if r := mustAuth(t, nil); !r.Usable || !r.Verified || r.LastFailure != nil {
			t.Fatalf("recovered = %+v", r)
		}
	})
	tw(t, "auth", "a non-authentication failure degrades the signal without pretending the key is gone", func(t *testing.T) {
		isolate(t)
		_, _ = StoreAPIKey("stored-key-0123456789abcdef")
		RecordAuthVerified(time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC))
		RecordAuthFailure(&IntegrationError{Code: CodeTimeout, Message: "TypeSafe request timed out; it was not retried and may still be billed."}, time.Date(2026, 1, 3, 0, 5, 0, 0, time.UTC))
		s := mustAuth(t, nil)
		r := mustDescribe(t, s)
		if !s.Usable || !s.Verified || r.Level != LevelOK || !strings.Contains(r.Text, "Last failure") {
			t.Fatalf("state = %+v report = %+v", s, r)
		}
	})
	tw(t, "auth", "a stored key readable by other users is reported as unusable, never as enabled", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX permission bits")
		}
		isolate(t)
		writeFile(t, CredentialsPath(), `{"apiKey":"stored-key-0123456789abcdef"}`, 0o644)
		s := mustAuth(t, nil)
		if s.Kind != KeyUnusable || s.Usable || s.Reason == "" || mustDescribe(t, s).Level != LevelError {
			t.Fatalf("state = %+v", s)
		}
	})
	tw(t, "auth", "a corrupt auth record is ignored and clearing forgets both facts", func(t *testing.T) {
		isolate(t)
		writeFile(t, AuthStatePath(), "{ not json", 0o600)
		if s := mustAuth(t, nil); s.Verified || s.LastFailure != nil {
			t.Fatalf("corrupt = %+v", s)
		}
		RecordAuthVerified(time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC))
		ClearAuthState()
		if s := mustAuth(t, nil); s.Verified || s.LastFailure != nil {
			t.Fatalf("cleared = %+v", s)
		}
	})
}
