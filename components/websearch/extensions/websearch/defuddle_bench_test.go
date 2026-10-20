package websearch

import (
	"encoding/json"
	"os"
	"testing"
)

// The Defuddle pipeline on representative pages of testdata/defuddle (parse, extractors, removals, scoring,
// standardization, Markdown), against port/bench-defuddle.mjs under Node on the same files.
//
//	go test -run xxx -bench Defuddle -benchmem
var defuddleBenchPages = []struct{ name, file string }{
	{"small", "port--rsc-fallback-null-readability"},
	{"article", "general--daringfireball.net-2025-02-the_iphone_16e"},
	{"large", "general--lesswrong.com-s-N7nDePaNabJdnbXeE-p-vJFdjigzmcXMhNTsx"},
	{"math", "math--mathjax"},
	{"footnotes", "footnotes--maggieappleton.com-xanadu-patterns"},
}

func BenchmarkDefuddle(b *testing.B) {
	for _, p := range defuddleBenchPages {
		src, err := os.ReadFile("testdata/defuddle/" + p.file + ".html")
		if err != nil {
			b.Fatal(err)
		}
		text := decodeText(src, "text/html")
		var want defuddleExpected
		data, err := os.ReadFile("testdata/defuddle/" + p.file + ".expected.json")
		if err != nil {
			b.Fatal(err)
		}
		if err := json.Unmarshal(data, &want); err != nil {
			b.Fatal(err)
		}
		b.Run(p.name, func(b *testing.B) {
			b.SetBytes(int64(len(src)))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := extractWithDefuddle(text, want.URL); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
