package websearch

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"testing"
)

func TestScratchLdom(t *testing.T) {
	data, err := os.ReadFile("testdata/ldom-scratch.json")
	if err != nil {
		t.Fatal(err)
	}
	var exp map[string]struct {
		HTML *string                    `json:"html"`
		Sel  map[string]json.RawMessage `json:"sel"`
	}
	if err := json.Unmarshal(data, &exp); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(exp))
	for n := range exp {
		names = append(names, n)
	}
	sort.Strings(names)
	bad := 0
	for _, name := range names {
		e := exp[name]
		src, _ := os.ReadFile("testdata/defuddle/" + name)
		doc := ldParseDocument(string(src))
		de := doc.documentElement()
		got := ""
		if de != nil {
			got = ldOuterHTML(de)
		}
		want := ""
		if e.HTML != nil {
			want = *e.HTML
		}
		if got != want {
			bad++
			i := 0
			for i < len(got) && i < len(want) && got[i] == want[i] {
				i++
			}
			lo := i - 60
			if lo < 0 {
				lo = 0
			}
			hiG, hiW := i+80, i+80
			if hiG > len(got) {
				hiG = len(got)
			}
			if hiW > len(want) {
				hiW = len(want)
			}
			t.Errorf("%s: html differs at %d\n  got:  %q\n  want: %q", name, i, got[lo:hiG], want[lo:hiW])
			continue
		}
		all := ldQuerySelectorAll(doc, "*")
		idx := map[*ldNode]int{}
		for i, n := range all {
			idx[n] = i
		}
		sels := make([]string, 0, len(e.Sel))
		for s := range e.Sel {
			sels = append(sels, s)
		}
		sort.Strings(sels)
		for _, s := range sels {
			var res string
			func() {
				var out []int
				th := ldTry(func() {
					for _, n := range ldQuerySelectorAll(doc, s) {
						out = append(out, idx[n])
					}
				})
				if th != nil {
					res = "ERR " + th.msg
					return
				}
				b, _ := json.Marshal(out)
				if out == nil {
					b = []byte("[]")
				}
				res = string(b)
			}()
			w := string(e.Sel[s])
			var ws string
			if json.Unmarshal(e.Sel[s], &ws) == nil {
				w = ws
			} else {
				var wi []int
				_ = json.Unmarshal(e.Sel[s], &wi)
				bb, _ := json.Marshal(wi)
				if wi == nil {
					bb = []byte("[]")
				}
				w = string(bb)
			}
			if res != w {
				bad++
				if len(res) > 200 {
					res = res[:200]
				}
				if len(w) > 200 {
					w = w[:200]
				}
				t.Errorf("%s: selector %q: got %s want %s", name, s, res, w)
				break
			}
		}
	}
	fmt.Println("scratch: bad =", bad, "of", len(names))
}
