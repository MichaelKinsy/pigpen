package dirs

import (
	"errors"
	"testing"
)

func TestStandardize(t *testing.T) {
	// /private/tmp and /tmp are one place; /private/etc/x exists only under /private/etc
	// here, and /private/other is unrelated to /other.
	real := map[string]string{
		"/private/tmp":       "/private/tmp",
		"/tmp":               "/private/tmp",
		"/private/tmp/w":     "/private/tmp/w",
		"/tmp/w":             "/private/tmp/w",
		"/private/other":     "/private/other",
		"/other":             "/other",
		"/private/other/dir": "/private/other/dir",
		"/other/dir":         "/other/dir",
	}
	realpath := func(p string) (string, error) {
		if r, ok := real[p]; ok {
			return r, nil
		}
		return "", errors.New("no such file")
	}
	for _, c := range []struct{ in, want string }{
		{"/private/tmp/w", "/tmp/w"},
		{"/private/tmp/w/missing/child.txt", "/tmp/w/missing/child.txt"},
		{"/private/tmp", "/tmp"},
		{"/private/other/dir", "/private/other/dir"},
		{"/private/other/dir/missing", "/private/other/dir/missing"},
		{"/private/nowhere/at/all", "/private/nowhere/at/all"},
		{"/private", "/private"},
		{"/privatetmp/w", "/privatetmp/w"},
		{"/tmp/w", "/tmp/w"},
		{"relative/private/tmp", "relative/private/tmp"},
		{"", ""},
	} {
		if got := standardize(c.in, realpath); got != c.want {
			t.Errorf("standardize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
