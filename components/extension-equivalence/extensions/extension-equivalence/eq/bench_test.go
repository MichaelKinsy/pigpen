package eq

import "testing"

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
