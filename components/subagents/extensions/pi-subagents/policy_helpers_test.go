package pi_subagents

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// js decodes a JSON literal the way a tool call arrives (objects keep their order).
func js(t *testing.T, s string) any {
	t.Helper()
	v, err := parseJSON([]byte(s))
	if err != nil {
		t.Fatalf("bad JSON %q: %v", s, err)
	}
	return v
}

// matches is assert.match: re is a JavaScript RegExp literal body (flags: i, s).
func polMatches(t *testing.T, text, re string, flags ...string) {
	t.Helper()
	prefix := ""
	for _, f := range flags {
		prefix += "(?" + f + ")"
	}
	if !regexp.MustCompile(prefix + re).MatchString(text) {
		t.Errorf("%q does not match /%s/", text, re)
	}
}

func polNotMatches(t *testing.T, text, re string) {
	t.Helper()
	if regexp.MustCompile(re).MatchString(text) {
		t.Errorf("%q matches /%s/", text, re)
	}
}

func equal(t *testing.T, got, want any, what string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: got %#v, want %#v", what, got, want)
	}
}

func joinLines(s []string) string { return strings.Join(s, "\n") }
