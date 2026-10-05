package jev

import (
	"strings"
	"testing"
)

// The judge path runs once per tool call when Jev is on: build the state document, ask, apply the thresholds.
// Building the document is the part that scales with the call's arguments; asking is a network call.
//
//	go test -run xxx -bench . -benchmem
func benchInput(bodyBytes int) map[string]any {
	return map[string]any{
		"path":    "/home/user/project/src/internal/module/file.go",
		"content": strings.Repeat("line of source code with some words in it\n", bodyBytes/42),
		"edits":   []any{map[string]any{"oldText": strings.Repeat("a", 300), "newText": strings.Repeat("b", 300)}},
	}
}

func BenchmarkGateStateSmallCall(b *testing.B) {
	input := map[string]any{"command": "go test ./... -count=1"}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		gateStateJSON("/work/repo", "bash", input, "please run the tests", 400, 8000)
	}
}

func BenchmarkGateStateWrite50KB(b *testing.B) {
	input := benchInput(50_000)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		gateStateJSON("/work/repo", "write", input, strings.Repeat("please write this file. ", 80), 400, 8000)
	}
}

// A document that never fits drives fitState through every shrink step.
func BenchmarkGateStateDoesNotFit(b *testing.B) {
	wide := map[string]any{}
	for i := 0; i < 200; i++ {
		wide[strings.Repeat("k", i%17+1)+string(rune('a'+i%26))+strings.Repeat("z", i/26)] = strings.Repeat("v", 90)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		gateStateJSON("/work/repo", "write", wide, "do the thing", 400, 2000)
	}
}

func BenchmarkEvaluateGate(b *testing.B) {
	cfg := defaultConfig()
	r := &response{Answers: map[string]answer{"destructive": {Noul: 0.4}, "exfiltration": {Noul: 0.1}, "beyond_scope": {Noul: 0.2}, "impact": {Type: "score", Score: 1.2, Confidence: 0.9, HasConfidence: true}}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		evaluateGate(r, cfg)
	}
}

// The memo key of one call (what is asked is memoised for CacheSeconds, so repeated calls cost one judge request).
func BenchmarkGateKeyWrite50KB(b *testing.B) {
	input := benchInput(50_000)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		gateKey("write", input, "/work/repo", "please write this file")
	}
}
