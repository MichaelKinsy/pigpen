package sessioningest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// perfSession is a session as Pi writes it: every entry has an id and a parentId, turns of a user message, an assistant
// message with a tool call and a tool result.
func perfSession(b *testing.B, turns int) string {
	b.Helper()
	var s strings.Builder
	s.WriteString(`{"type":"session","id":"0199abcd","version":3,"cwd":"/work/repo","timestamp":"2026-01-01T00:00:00Z"}` + "\n")
	parent := "null"
	for i := 0; i < turns; i++ {
		fmt.Fprintf(&s, `{"type":"message","id":"u%d","parentId":%s,"timestamp":"2026-01-01T00:00:%02dZ","message":{"role":"user","content":[{"type":"text","text":"please fix the failing test number %d"}]}}`+"\n", i, parent, i%60, i)
		fmt.Fprintf(&s, `{"type":"message","id":"a%d","parentId":"u%d","timestamp":"2026-01-01T00:00:%02dZ","message":{"role":"assistant","content":[{"type":"text","text":"Looking at it now."},{"type":"toolCall","id":"c%d","name":"bash","arguments":{"command":"go test ./..."}}],"usage":{"input":1200,"output":80,"cacheRead":900,"cacheWrite":0,"totalTokens":2180,"cost":{"total":0.0123}}}}`+"\n", i, i, i%60, i)
		fmt.Fprintf(&s, `{"type":"message","id":"t%d","parentId":"a%d","timestamp":"2026-01-01T00:00:%02dZ","message":{"role":"toolResult","toolCallId":"c%d","toolName":"bash","content":[{"type":"text","text":%q}]}}`+"\n", i, i, i%60, i, strings.Repeat("ok  \tgithub.com/example/pkg\t0.123s\n", 20))
		parent = fmt.Sprintf(`"t%d"`, i)
	}
	path := filepath.Join(b.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(s.String()), 0o600); err != nil {
		b.Fatal(err)
	}
	return path
}

// BenchmarkReadSession runs each mode through the tool handler on a 2000-turn session (6000 entries, about 2 MB).
//
//	go test -run xxx -bench ReadSession -benchmem
func BenchmarkReadSession(b *testing.B) {
	path := perfSession(b, 2000)
	cases := map[string]map[string]any{
		"toc":   {"mode": "toc", "limit": 50},
		"query": {"mode": "query", "query": "failing test", "limit": 10},
		"slice": {"mode": "slice", "start": 100, "limit": 20},
		"turn":  {"mode": "turn", "turn": 1000},
		"tools": {"mode": "tools"},
		"stats": {"mode": "stats"},
	}
	for name, params := range cases {
		b.Run(name, func(b *testing.B) {
			p := map[string]any{"path": path}
			for k, v := range params {
				p[k] = v
			}
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := handleReadSession(sdk.Context{}, p); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
