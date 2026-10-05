package acp

import (
	"strconv"
	"strings"
	"testing"
)

// pig-acp translates pi's events into ACP updates. Pi sends the whole accumulated bash output with every update,
// so the translation is O(output) per update by the input's own shape; this measures that cost, per update.
//
//	go test -run xxx -bench . -benchmem
func BenchmarkBashOutputUpdate(b *testing.B) {
	for _, size := range []int{4 << 10, 256 << 10} {
		prev := strings.Repeat("compiling package line of output\n", size/33)
		result := map[string]any{"content": []any{map[string]any{"type": "text", "text": prev + "one more line\n"}}}
		b.Run(strconv.Itoa(size>>10)+"KiB", func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				text := BashResultText(result)
				delta := BashOutputDelta(prev, text)
				_ = BashTerminalOutputMeta("call1", delta)
			}
		})
	}
}

func BenchmarkPromptToPiMessage(b *testing.B) {
	blocks := []ContentBlock{
		{"type": "text", "text": "Please look at this file and fix the failing test."},
		{"type": "resource_link", "uri": "file:///work/repo/internal/module/file.go", "name": "file.go"},
		{"type": "text", "text": strings.Repeat("More context about the change. ", 40)},
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		PromptToPiMessage(blocks)
	}
}

func BenchmarkToolResultToText(b *testing.B) {
	result := map[string]any{"content": []any{map[string]any{"type": "text", "text": strings.Repeat("file contents line\n", 200)}}, "details": map[string]any{"exitCode": 0}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ToolResultToText(result)
	}
}
