package langsmith

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestProfileJSONParserRejectsMore(t *testing.T) {
	for _, text := range []string{`{"a":1 x`, `[1 x`, "-x", "-", "--1", "\"\\n\t\"", "\"\\", `"\u00`, `{"a":1,"b"`, "[1,", "tru", "nul", "fals", "1e5.5", "0x10", "[1]]", "{}{}", "\u0000"} {
		if v, ok := parseJSONOrdered(text); ok {
			t.Errorf("parse(%q) = %#v; JSON.parse throws", text, v)
		}
	}
	deep := ""
	for range profileJSONMaxDepth + 2 {
		deep += "["
	}
	if _, ok := parseJSONOrdered(deep); ok {
		t.Error("document nested past the limit accepted")
	}
}

func TestProfileStringifyEdgeValues(t *testing.T) {
	out, err := stringifyIndent2(nil, (*omap)(nil))
	if err != nil || string(out) != "null" {
		t.Fatalf("%q %v", out, err)
	}
	m := obj("keep", 1.0, "gone", undef, "nested", obj("x", undef), "arr", []any{undef, nil, "s"})
	out, err = stringifyIndent2(nil, m)
	want := "{\n  \"keep\": 1,\n  \"nested\": {},\n  \"arr\": [\n    null,\n    null,\n    \"s\"\n  ]\n}"
	if err != nil || string(out) != want {
		t.Fatalf("%q %v", out, err)
	}
}

func TestProfileEndsInANumberEmpty(t *testing.T) {
	if endsInANumber("") || endsInANumber(".") {
		t.Fatal("endsInANumber edge cases")
	}
}

func TestProfileFetchWithoutClientUsesDefaultClient(t *testing.T) {
	prev := http.DefaultClient
	var got string
	http.DefaultClient = &http.Client{Transport: profRT(func(r *http.Request) (*http.Response, error) {
		got = r.URL.String()
		return profTextResponse(200, "{}"), nil
	})}
	defer func() { http.DefaultClient = prev }()
	resp, err := profileFetch(context.Background(), nil, http.MethodGet, "http://default.example/x", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got != "http://default.example/x" {
		t.Fatalf("%q", got)
	}
}

func needsUnwritableDir(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory the user cannot empty")
	}
}

func TestLockStaleRemovalFailureIsReturned(t *testing.T) {
	needsUnwritableDir(t)
	cfg := filepath.Join(t.TempDir(), "config.json")
	dir := foreignLock(t, cfg, new(jsISOString(nowMs()-60_000)+"\nholder\n"))
	sub := filepath.Join(dir, "sub")
	os.Mkdir(sub, 0o700)
	os.WriteFile(filepath.Join(sub, "f"), []byte("x"), 0o600)
	os.Chmod(sub, 0o500) // its entries cannot be unlinked
	defer os.Chmod(sub, 0o700)
	_, err := acquireOAuthRefreshLock(context.Background(), cfg, nowMs()+500)
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("err = %v", err)
	}
}

func TestLockMetadataFailureThenRemovalFailure(t *testing.T) {
	needsUnwritableDir(t)
	cfg := filepath.Join(t.TempDir(), "config.json")
	var locked string
	prev := lockWriteMetadata
	lockWriteMetadata = func(path string, _ []byte) error {
		dir := filepath.Dir(path)
		sub := filepath.Join(dir, "sub")
		os.Mkdir(sub, 0o700)
		os.WriteFile(filepath.Join(sub, "f"), []byte("x"), 0o600)
		os.Chmod(sub, 0o500)
		locked = sub
		return errors.New("disk full")
	}
	defer func() {
		lockWriteMetadata = prev
		os.Chmod(locked, 0o700)
	}()
	_, err := acquireOAuthRefreshLock(context.Background(), cfg, nowMs()+500)
	// The removal error replaces the write error, as `await rmRecursive(lockDir)` throws before `throw err`.
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("err = %v", err)
	}
}
