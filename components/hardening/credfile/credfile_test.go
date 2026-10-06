package credfile

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/components/hardening/profile"
)

var t0 = time.UnixMilli(1_800_000_000_000)

func clock(at time.Time) func() time.Time { return func() time.Time { return at } }

func ms(t time.Time) int64 { return t.UnixMilli() }

type fixture struct {
	t    *testing.T
	path string
	cfg  profile.CredentialFile
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	return &fixture{t: t, path: filepath.Join(dir, "auth.json"), cfg: profile.CredentialFile{
		Path: filepath.Join(dir, "auth.json"), Provider: "search", Origin: "https://api.example.com:443",
		Header: "Authorization", Scheme: "Bearer", ExpiryMargin: 30 * time.Second}}
}

func (f *fixture) write(content string) {
	f.t.Helper()
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Rename(tmp, f.path); err != nil { // as the host rotates it
		f.t.Fatal(err)
	}
}

func (f *fixture) source() Source {
	f.t.Helper()
	s, err := New("websearch", &f.cfg)
	if err != nil {
		f.t.Fatal(err)
	}
	return s.WithClock(clock(t0))
}

func oauth(access string, expires time.Time) string {
	return fmt.Sprintf(`{"search":{"type":"oauth","access":%q,"refresh":"REFRESH-SECRET","expires":%d}}`, access, ms(expires))
}

func wantCode(t *testing.T, err error, code profile.Code) {
	t.Helper()
	if !profile.IsCode(err, code) {
		t.Fatalf("err = %v, want %s", err, code)
	}
	var pe *profile.Error
	if e, ok := err.(*profile.Error); ok {
		pe = e
	}
	if pe == nil || pe.Flag != profile.FlagCredentialFile || pe.Package != "websearch" {
		t.Fatalf("flag/package not set: %#v", err)
	}
}

func TestOAuthEntry(t *testing.T) {
	f := newFixture(t)
	f.write(oauth("tok-1", t0.Add(time.Hour)))
	s := f.source()
	got, err := s.Token(context.Background())
	if err != nil || got != "tok-1" {
		t.Fatalf("%q %v", got, err)
	}
	name, value, err := s.Header(context.Background())
	if err != nil || name != "Authorization" || value != "Bearer tok-1" {
		t.Fatalf("%q %q %v", name, value, err)
	}
}

func TestAPIKeyEntry(t *testing.T) {
	f := newFixture(t)
	f.write(`{"search":{"type":"api_key","key":"sk-123"},"other":{"type":"api_key","key":"other-key"}}`)
	got, err := f.source().Token(context.Background())
	if err != nil || got != "sk-123" {
		t.Fatalf("%q %v", got, err)
	}
	f.cfg.Header, f.cfg.Scheme = "X-Api-Key", ""
	name, value, err := f.source().Header(context.Background())
	if err != nil || name != "X-Api-Key" || value != "sk-123" {
		t.Fatalf("%q %q %v", name, value, err)
	}
	// An api_key has no expiry: an "expires" next to it is not looked at.
	f.write(`{"search":{"type":"api_key","key":"sk-123","expires":1}}`)
	if _, err := f.source().Token(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestNothingIsCachedARotatedFileIsReadOnTheNextCall(t *testing.T) {
	f := newFixture(t)
	s := f.source()
	f.write(oauth("first", t0.Add(time.Hour)))
	if got, _ := s.Token(context.Background()); got != "first" {
		t.Fatal(got)
	}
	f.write(oauth("second", t0.Add(time.Hour)))
	if got, err := s.Token(context.Background()); err != nil || got != "second" {
		t.Fatalf("%q %v", got, err)
	}
	f.write(`{"search":{"type":"api_key","key":"third"}}`)
	if got, _ := s.Token(context.Background()); got != "third" {
		t.Fatal(got)
	}
	if err := os.Remove(f.path); err != nil {
		t.Fatal(err)
	}
	_, err := s.Token(context.Background())
	wantCode(t, err, profile.CredentialUnavailable) // a token read earlier is not served from memory
}

func TestOneOpenPerCall(t *testing.T) {
	f := newFixture(t)
	f.write(oauth("tok", t0.Add(time.Hour)))
	s := f.source()
	opens := 0
	s.open = func(name string, flag int, perm fs.FileMode) (*os.File, error) {
		opens++
		return os.OpenFile(name, flag, perm)
	}
	for i := 1; i <= 3; i++ {
		if _, err := s.Token(context.Background()); err != nil {
			t.Fatal(err)
		}
		if opens != i {
			t.Fatalf("after call %d: %d opens", i, opens)
		}
	}
}

func TestMissingOrUnusableFileIsUnavailable(t *testing.T) {
	f := newFixture(t)
	_, err := f.source().Token(context.Background())
	wantCode(t, err, profile.CredentialUnavailable)

	// a directory
	f.cfg.Path = t.TempDir()
	_, err = f.source().Token(context.Background())
	wantCode(t, err, profile.CredentialUnavailable)

	// larger than the cap
	f = newFixture(t)
	f.write(`{"search":{"type":"api_key","key":"` + strings.Repeat("k", 64<<10) + `"}}`)
	_, err = f.source().Token(context.Background())
	wantCode(t, err, profile.CredentialUnavailable)
	// exactly at the cap is read (and then rejected only as an oversize key)
	pad := 64<<10 - len(`{"search":{"type":"api_key","key":"k"},"pad":""}`)
	f.write(`{"search":{"type":"api_key","key":"k"},"pad":"` + strings.Repeat("x", pad) + `"}`)
	if info, _ := os.Stat(f.path); info.Size() != 64<<10 {
		t.Fatalf("size %d", info.Size())
	}
	if got, err := f.source().Token(context.Background()); err != nil || got != "k" {
		t.Fatalf("at the cap: %q %v", got, err)
	}
}

func TestUnreadableFileIsUnavailable(t *testing.T) {
	if os.Geteuid() == 0 || runtime.GOOS == "windows" {
		t.Skip("permissions are not enforced")
	}
	f := newFixture(t)
	f.write(oauth("tok", t0.Add(time.Hour)))
	if err := os.Chmod(f.path, 0); err != nil {
		t.Fatal(err)
	}
	_, err := f.source().Token(context.Background())
	wantCode(t, err, profile.CredentialUnavailable)
}

func TestSymlinkToAReadOnlyTargetIsFollowed(t *testing.T) {
	shared := t.TempDir()
	target := filepath.Join(shared, "auth.json")
	if err := os.WriteFile(target, []byte(oauth("shared-token", t0.Add(time.Hour))), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(shared, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(shared, 0o755) })
	f := newFixture(t)
	if err := os.Symlink(target, f.path); err != nil {
		t.Skip("symlinks unavailable")
	}
	got, err := f.source().Token(context.Background())
	if err != nil || got != "shared-token" {
		t.Fatalf("%q %v", got, err)
	}
	// Replacing the link with another (as a rename would) is picked up at once.
	other := filepath.Join(t.TempDir(), "auth2.json")
	if err := os.WriteFile(other, []byte(oauth("rotated", t0.Add(time.Hour))), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(f.path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, f.path); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.source().Token(context.Background()); got != "rotated" {
		t.Fatal(got)
	}
}

func TestMalformedFiles(t *testing.T) {
	cases := map[string]string{
		"empty":                    ``,
		"not json":                 `not json`,
		"truncated (partial read)": `{"search":{"type":"oauth","access":"tok","expi`,
		"array":                    `[]`,
		"null":                     `null`,
		"string":                   `"x"`,
		"duplicate provider":       `{"search":{"type":"api_key","key":"a"},"search":{"type":"api_key","key":"b"}}`,
		"duplicate key in entry":   `{"search":{"type":"api_key","key":"a","key":"b"}}`,
		"trailing value":           `{"search":{"type":"api_key","key":"a"}} {}`,
		"no entry for provider":    `{"other":{"type":"api_key","key":"a"}}`,
		"provider is case-folded":  `{"Search":{"type":"api_key","key":"a"}}`,
		"entry is null":            `{"search":null}`,
		"entry is a string":        `{"search":"sk-123"}`,
		"entry is an array":        `{"search":[]}`,
		"no type":                  `{"search":{"key":"a"}}`,
		"unknown type":             `{"search":{"type":"bearer","key":"a"}}`,
		"legacy type":              `{"search":{"type":"api","key":"a"}}`,
		"type is a number":         `{"search":{"type":1,"key":"a"}}`,
		"type case":                `{"search":{"type":"API_KEY","key":"a"}}`,
		"api_key without key":      `{"search":{"type":"api_key"}}`,
		"api_key empty key":        `{"search":{"type":"api_key","key":""}}`,
		"api_key legacy field":     `{"search":{"type":"api_key","apiKey":"a"}}`,
		"api_key with a command":   `{"search":{"type":"api_key","key":"!cat /etc/passwd"}}`,
		"key has a space":          `{"search":{"type":"api_key","key":"a b"}}`,
		"key has a newline":        `{"search":{"type":"api_key","key":"a\nX-Injected: 1"}}`,
		"key has a NUL":            `{"search":{"type":"api_key","key":"a\u0000b"}}`,
		"key is not ASCII":         `{"search":{"type":"api_key","key":"clé"}}`,
		"key is a number":          `{"search":{"type":"api_key","key":12}}`,
		"key is huge":              `{"search":{"type":"api_key","key":"` + strings.Repeat("k", 9000) + `"}}`,
		"oauth without access":     `{"search":{"type":"oauth","refresh":"r","expires":9999999999999}}`,
		"oauth empty access":       `{"search":{"type":"oauth","access":"","expires":9999999999999}}`,
		"oauth without expires":    `{"search":{"type":"oauth","access":"tok"}}`,
		"oauth expires is string":  `{"search":{"type":"oauth","access":"tok","expires":"9999999999999"}}`,
		"oauth expires is null":    `{"search":{"type":"oauth","access":"tok","expires":null}}`,
		"oauth expires is bool":    `{"search":{"type":"oauth","access":"tok","expires":true}}`,
		"oauth expires overflows":  `{"search":{"type":"oauth","access":"tok","expires":1e999}}`,
		"oauth access with CRLF":   `{"search":{"type":"oauth","access":"tok\r\nX: y","expires":9999999999999}}`,
		"oauth with only refresh":  `{"search":{"type":"oauth","refresh":"REFRESH-SECRET","expires":9999999999999}}`,
		"case-folded field":        `{"search":{"type":"api_key","Key":"a"}}`,
		"case-folded repeat":       `{"search":{"type":"api_key","key":"a","KEY":"b"}}`,
		"case-folded type":         `{"search":{"Type":"api_key","key":"a"}}`,
		"case-folded access":       `{"search":{"type":"oauth","access":"tok","Access":"other","expires":9999999999999}}`,
	}
	for name, content := range cases {
		f := newFixture(t)
		f.write(content)
		got, err := f.source().Token(context.Background())
		if !profile.IsCode(err, profile.CredentialMalformed) || got != "" {
			t.Errorf("%s: %q, %v", name, got, err)
		}
	}
}

func TestExpiryAndMargin(t *testing.T) {
	f := newFixture(t) // margin 30 s
	check := func(name string, expires time.Time, want profile.Code) {
		t.Helper()
		f.write(oauth("tok", expires))
		got, err := f.source().Token(context.Background())
		if want == "" {
			if err != nil || got != "tok" {
				t.Errorf("%s: %q %v", name, got, err)
			}
			return
		}
		if !profile.IsCode(err, want) || got != "" {
			t.Errorf("%s: %q %v, want %s", name, got, err, want)
		}
	}
	check("long valid", t0.Add(time.Hour), "")
	check("just outside the margin", t0.Add(30*time.Second+time.Millisecond), "")
	check("exactly now plus margin", t0.Add(30*time.Second), profile.CredentialExpired)
	check("inside the margin", t0.Add(29*time.Second), profile.CredentialExpired)
	check("exactly now", t0, profile.CredentialExpired)
	check("one millisecond ago", t0.Add(-time.Millisecond), profile.CredentialExpired)
	check("long ago", t0.Add(-24*time.Hour), profile.CredentialExpired)
	f.write(`{"search":{"type":"oauth","access":"tok","expires":0}}`)
	if _, err := f.source().Token(context.Background()); !profile.IsCode(err, profile.CredentialExpired) {
		t.Errorf("expires 0: %v", err)
	}
	f.write(`{"search":{"type":"oauth","access":"tok","expires":-5}}`)
	if _, err := f.source().Token(context.Background()); !profile.IsCode(err, profile.CredentialExpired) {
		t.Errorf("negative expires: %v", err)
	}
	f.write(`{"search":{"type":"oauth","access":"tok","expires":1800000100000.5}}`)
	if got, err := f.source().Token(context.Background()); err != nil || got != "tok" {
		t.Errorf("fractional expires: %q %v", got, err)
	}
	// the margin is the profile's
	f.cfg.ExpiryMargin = 0
	f.write(oauth("tok", t0.Add(time.Millisecond)))
	if _, err := f.source().Token(context.Background()); err != nil {
		t.Errorf("zero margin: %v", err)
	}
	f.cfg.ExpiryMargin = 10 * time.Minute
	f.write(oauth("tok", t0.Add(9*time.Minute)))
	if _, err := f.source().Token(context.Background()); !profile.IsCode(err, profile.CredentialExpired) {
		t.Errorf("ten-minute margin: %v", err)
	}
}

func TestAnExpiredTokenIsNeverRefreshed(t *testing.T) {
	f := newFixture(t)
	f.write(oauth("old", t0.Add(-time.Hour)))
	_, err := f.source().Token(context.Background())
	wantCode(t, err, profile.CredentialExpired)
	if strings.Contains(err.Error(), "REFRESH-SECRET") || strings.Contains(err.Error(), "old") {
		t.Fatalf("error text: %v", err)
	}
}

func TestCancelledContext(t *testing.T) {
	f := newFixture(t)
	f.write(oauth("tok", t0.Add(time.Hour)))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := f.source().Token(ctx)
	wantCode(t, err, profile.Cancelled)
	if !isContextErr(err) {
		t.Fatal("does not unwrap to the context error")
	}
}

func TestErrorsNameNeitherPathNorTokenNorProvider(t *testing.T) {
	f := newFixture(t)
	f.cfg.Provider = "zzprov"
	bad := []string{``, `{"search":{"type":"api_key","key":"!SECRET-KEY"}}`, oauth("SECRET-TOKEN", t0.Add(-time.Hour)), `{"other":{}}`}
	for _, content := range bad {
		f.write(strings.ReplaceAll(content, `"search"`, `"zzprov"`))
		_, err := f.source().Token(context.Background())
		if err == nil {
			t.Fatal("no error")
		}
		for _, leak := range []string{"SECRET", "REFRESH", f.path, filepath.Base(f.path), "zzprov", "api.example.com"} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("error text %q contains %q", err.Error(), leak)
			}
		}
	}
	_ = os.Remove(f.path)
	_, err := f.source().Token(context.Background())
	if strings.Contains(err.Error(), f.path) || strings.Contains(err.Error(), "auth.json") {
		t.Errorf("error text %q names the path", err.Error())
	}
}

func TestSourceNeedsAConfiguration(t *testing.T) {
	if _, err := New("websearch", nil); !profile.IsCode(err, profile.ProfileMisconfigured) {
		t.Fatal(err)
	}
	for _, mutate := range []func(*profile.CredentialFile){
		func(c *profile.CredentialFile) { c.Path = "" },
		func(c *profile.CredentialFile) { c.Provider = "" },
		func(c *profile.CredentialFile) { c.Origin = "" },
		func(c *profile.CredentialFile) { c.Origin = "https://a.example/path" },
		func(c *profile.CredentialFile) { c.Header = "" },
		func(c *profile.CredentialFile) { c.ExpiryMargin = -time.Second },
	} {
		f := newFixture(t)
		mutate(&f.cfg)
		if _, err := New("websearch", &f.cfg); !profile.IsCode(err, profile.ProfileMisconfigured) {
			t.Errorf("accepted a bad configuration: %v", err)
		}
	}
	var zero Source
	if _, err := zero.Token(context.Background()); !profile.IsCode(err, profile.ProfileMisconfigured) {
		t.Fatalf("zero Source: %v", err)
	}
	if _, _, err := zero.Header(context.Background()); !profile.IsCode(err, profile.ProfileMisconfigured) {
		t.Fatalf("zero Source: %v", err)
	}
	if rt := zero.RoundTripper(nil); rt == nil {
		t.Fatal("no round tripper")
	}
}

func TestOrigin(t *testing.T) {
	f := newFixture(t)
	if got := f.source().Origin(); got != "https://api.example.com:443" {
		t.Fatal(got)
	}
}
