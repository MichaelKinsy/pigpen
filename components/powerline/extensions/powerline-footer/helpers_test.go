package powerline_footer

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

// setenv sets (or, for nil, unsets) an environment variable for the test and restores it afterwards.
func setenv(t *testing.T, key string, value *string) {
	t.Helper()
	old, had := os.LookupEnv(key)
	t.Cleanup(func() {
		if had {
			os.Setenv(key, old)
		} else {
			os.Unsetenv(key)
		}
	})
	if value == nil {
		os.Unsetenv(key)
	} else {
		os.Setenv(key, *value)
	}
}

func s(v string) *string { return &v }

func eq(t *testing.T, got, want any, msg string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: got %#v, want %#v", msg, got, want)
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

// js decodes a JSON literal the way the extension decodes settings.
func js(t *testing.T, text string) any {
	t.Helper()
	v, err := parseJSON([]byte(text))
	if err != nil {
		t.Fatalf("parse %s: %v", text, err)
	}
	return v
}

// Bounds- and nil-safe accessors, so a stub (red) run reports failures instead of panicking.
func sv(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

func fv(p *float64) float64 {
	if p == nil {
		return -1
	}
	return *p
}

func itemAt(items []customItem, i int) customItem {
	if i < len(items) {
		return items[i]
	}
	return customItem{}
}

func ctxOpts(o segmentOptions) (context, cacheRead *string) {
	if o.Context != nil {
		context = o.Context.Format
	}
	if o.CacheRead != nil {
		cacheRead = o.CacheRead.Format
	}
	return
}

// jo decodes a JSON object literal; a failed decode yields an empty object so a stub run fails instead of panicking.
func jo(t *testing.T, text string) *jsObject {
	t.Helper()
	if o, ok := js(t, text).(*jsObject); ok {
		return o
	}
	return &jsObject{}
}
