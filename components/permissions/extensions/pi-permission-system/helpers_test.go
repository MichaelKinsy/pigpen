package pi_permission_system

import (
	"reflect"
	"testing"
)

func eq(t *testing.T, got, want any, what string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: got %#v, want %#v", what, got, want)
	}
}

func jo(t *testing.T, s string) *jsObject {
	t.Helper()
	v, err := parseJSON([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	o, ok := v.(*jsObject)
	if !ok {
		t.Fatalf("not an object: %s", s)
	}
	return o
}

func sp(s string) *string { return &s }

// rule builds a Rule; layer "" is no layer.
func rule(surface, pattern, action, layer, origin string) Rule {
	return Rule{Surface: surface, Pattern: pattern, Action: action, Layer: layer, Origin: origin}
}

const fakeHome = "/home/testuser"

func useFakeHome(t *testing.T) {
	old := homeDir
	homeDir = func() string { return fakeHome }
	t.Cleanup(func() { homeDir = old })
}

var win = &MatchOptions{CaseInsensitive: true, WindowsSeparators: true}
