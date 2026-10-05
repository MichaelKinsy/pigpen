package pi_goal_x

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

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

func eq(t *testing.T, got, want any, what string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: got %#v, want %#v", what, got, want)
	}
}

// show renders an ordered object compactly, undefined keys left out.
func show(g *jsObject) string { return marshalJSON(g, "") }

func ms(y int, m time.Month, d, h, mi, s int) int64 {
	return time.Date(y, m, d, h, mi, s, 0, time.UTC).UnixMilli()
}

func baseGoal(objective string, sisyphus bool, at int64) *jsObject {
	return createGoal(objective, true, sisyphus, false, at)
}

func useTimeZone(t *testing.T) {
	old := localZone
	localZone = time.UTC
	t.Cleanup(func() { localZone = old })
}

func poolOf(goals ...*jsObject) *goalPool {
	p := newPool()
	for _, g := range goals {
		if gstr(g, "status") != "complete" {
			p.set(gstr(g, "id"), cloneGoal(g))
		}
	}
	return p
}

func ids(goals []*jsObject) []string {
	out := []string{}
	for _, g := range goals {
		out = append(out, gstr(g, "id"))
	}
	return out
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(fmt.Sprint(err))
	}
	return v
}
