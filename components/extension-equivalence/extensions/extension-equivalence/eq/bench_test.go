package eq

import (
	"strings"
	"testing"
)

// The gap scan checks a TypeScript extension against PiG's SDK surface table. `pigeq gaps` runs it per file, so
// anything that depends only on the table should not be redone for each file.
//
//	go test -run xxx -bench . -benchmem
const benchExtension = `import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
export default function (pi: ExtensionAPI) {
  pi.on("session_start", async (_e, ctx) => { ctx.ui.notify("hi", "info"); });
  pi.on("tool_call", async (e, ctx) => { if (e.toolName === "bash") return { block: true, reason: "no" }; });
  pi.registerCommand("x", { description: "x", handler: async (args, ctx) => { await ctx.ui.select("t", ["a", "b"]); } });
  pi.registerTool({ name: "t", label: "t", description: "t", parameters: {}, execute: async () => ({ content: [] }) });
  pi.sendUserMessage("go");
}
`

func BenchmarkScanGaps(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ScanGaps(benchExtension, "")
	}
}

// Every string of every event goes through the normalizer. The scenario's N7 rules add one regexp pass per
// rule and only when the scenario lists any.
var benchEvent = map[string]any{
	"type": "tool_execution_end", "toolName": "fetch_content",
	"result": map[string]any{"content": []any{map[string]any{"type": "text", "text": strings.Repeat("Plain page served by the fake web server. ", 50) + "responseId mfx3k2a9jq7w8e"}},
		"details": map[string]any{"responseId": "mfx3k2a9jq7w8e", "urls": []any{"http://127.0.0.1:41234/page"}}},
}

func BenchmarkNormalizerData(b *testing.B) {
	for name, rules := range map[string][]NormalizeRule{
		"no-rules": nil,
		"one-rule": {{Pattern: `\b[0-9a-z]{14}\b`, Replacement: "<response-id>"}},
	} {
		b.Run(name, func(b *testing.B) {
			n := NewNormalizer("/tmp/eq1/work", "/tmp/eq1", "/tmp/eq1/home")
			if err := n.AddRules(rules); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				n.Data(benchEvent)
			}
		})
	}
}
