package doctor

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestRemoveArrayElements(t *testing.T) {
	cases := []struct {
		name, in string
		remove   []int
		want     string
	}{
		{"first of pretty", "{\n  \"packages\": [\n    \"a\",\n    \"b\",\n    \"c\"\n  ],\n  \"k\": 1\n}\n", []int{0}, "{\n  \"packages\": [\n    \"b\",\n    \"c\"\n  ],\n  \"k\": 1\n}\n"},
		{"middle", "{\n  \"packages\": [\n    \"a\",\n    \"b\",\n    \"c\"\n  ]\n}", []int{1}, "{\n  \"packages\": [\n    \"a\",\n    \"c\"\n  ]\n}"},
		{"last", "{\n  \"packages\": [\n    \"a\",\n    \"b\",\n    \"c\"\n  ]\n}", []int{2}, "{\n  \"packages\": [\n    \"a\",\n    \"b\"\n  ]\n}"},
		{"two adjacent at the end", "{\"packages\":[\"a\",\"b\",\"c\"]}", []int{1, 2}, "{\"packages\":[\"a\"]}"},
		{"two non adjacent", "{\"packages\":[\"a\",\"b\",\"c\",\"d\"]}", []int{0, 2}, "{\"packages\":[\"b\",\"d\"]}"},
		{"all", "{\"packages\":[\"a\",\"b\"]}", []int{0, 1}, "{\"packages\":[]}"},
		{"only", "{\"packages\": [ \"a\" ]}", []int{0}, "{\"packages\": [ ]}"},
		{"objects with nested arrays and strings with brackets and commas", `{"packages":[{"source":"a,]","extensions":["x","y"]},"b"],"z":[1]}`, []int{0}, `{"packages":["b"],"z":[1]}`},
		{"escaped quotes", `{"packages":["a\"b","c"],"other":"packages"}`, []int{0}, `{"packages":["c"],"other":"packages"}`},
		{"a packages key deeper down is not the top-level one", `{"x":{"packages":["no"]},"packages":["a","b"]}`, []int{0}, `{"x":{"packages":["no"]},"packages":["b"]}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := removeArrayElements([]byte(c.in), "packages", c.remove)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != c.want {
				t.Errorf("got  %q\nwant %q", got, c.want)
			}
			var a, b map[string]any
			if err := json.Unmarshal(got, &b); err != nil {
				t.Fatalf("result is not JSON: %v", err)
			}
			_ = json.Unmarshal([]byte(c.in), &a)
			delete(a, "packages")
			delete(b, "packages")
			if !reflect.DeepEqual(a, b) {
				t.Errorf("other keys changed")
			}
		})
	}
}

func TestRemoveArrayElementsErrors(t *testing.T) {
	for name, in := range map[string]string{
		"not json":     "{",
		"no key":       `{"a":1}`,
		"not an array": `{"packages":"a"}`,
		"top is array": `["a"]`,
	} {
		if _, err := removeArrayElements([]byte(in), "packages", []int{0}); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if _, err := removeArrayElements([]byte(`{"packages":["a"]}`), "packages", []int{3}); err == nil {
		t.Error("out-of-range index must fail")
	}
}

func TestParsePackages(t *testing.T) {
	entries, err := parsePackages([]byte(`{"packages":["a",{"source":"b","extensions":["x"],"skills":[]},{"source":"c"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 || entries[0].Source != "a" || entries[0].Filtered {
		t.Errorf("%+v", entries)
	}
	if !entries[1].Filtered || !reflect.DeepEqual(entries[1].Extensions, []string{"x"}) || entries[1].Skills == nil || len(entries[1].Skills) != 0 {
		t.Errorf("%+v", entries[1])
	}
	if !entries[2].Filtered || entries[2].Extensions != nil {
		t.Errorf("object form without filters: %+v", entries[2])
	}
	if _, err := parsePackages([]byte(`{"packages":[1]}`)); err == nil {
		t.Error("a number is not a package")
	}
	if e, err := parsePackages([]byte(`{}`)); err != nil || len(e) != 0 {
		t.Errorf("no packages key: %v %v", e, err)
	}
}

func TestPatternsFollowPiG(t *testing.T) {
	members := []string{"extensions/ask", "extensions/grill", "extensions/worktree"}
	cases := []struct {
		patterns []string
		want     []string
	}{
		{nil, members},
		{[]string{}, nil},
		{[]string{"extensions/ask"}, []string{"extensions/ask"}},
		{[]string{"extensions/*", "-extensions/grill"}, []string{"extensions/ask", "extensions/worktree"}},
		{[]string{"+extensions/ask"}, members}, // + alone does not narrow
		{[]string{"extensions/ask", "+extensions/grill"}, []string{"extensions/ask", "extensions/grill"}},
		{[]string{"!extensions/grill"}, members}, // PiG's ResourceEnabled: "!" includes like "+"
	}
	for _, c := range cases {
		got := applyPatterns(members, c.patterns)
		if !reflect.DeepEqual(got, c.want) && !(len(got) == 0 && len(c.want) == 0) {
			t.Errorf("%v: got %v want %v", c.patterns, got, c.want)
		}
	}
}
