// Times the original's path-layer hot calls on the operations of extensions/pi-permission-system/paths_bench_test.go (an AccessPath per
// path argument, a tool path, the win32 comparable form, input normalization of an MCP call, the Pi MCP tool targets). realpathSync is
// the identity, as the Go benchmark's mockRealpathB makes it, so neither side measures the filesystem. Copied into an installed copy of
// the original by bench-paths.sh for the run and removed after; the result goes to BENCH_OUT (a JSON object of ns per operation).
import { writeFileSync } from "node:fs";
import { it, vi } from "vitest";

vi.mock("node:fs", async (importOriginal) => {
  const real = await importOriginal<typeof import("node:fs")>();
  const realpathSync = (p: string) => p;
  return { ...real, realpathSync, default: { ...real, realpathSync } };
});

import { normalizeInput } from "#src/access-intent/input-normalizer";
import { createPiMcpToolTargets } from "#src/access-intent/mcp-targets";
import { posixPathFlavor, win32PathFlavor } from "#src/path/path-flavor";
import { PathNormalizer } from "#src/path/path-normalizer";

it("times the original's path layer", () => {
  const n = new PathNormalizer(posixPathFlavor, "/projects/my-app");
  const mcpInput = { tool: "github_search_code" };
  const servers = ["github", "todoist", "exa"];
  const piServers = ["danger-srv", "github", "todoist"];
  const time = (iters: number, fn: () => void): number => {
    for (let i = 0; i < 2000; i++) fn();
    const runs: number[] = [];
    for (let r = 0; r < 15; r++) {
      const t0 = process.hrtime.bigint();
      for (let i = 0; i < iters; i++) fn();
      runs.push(Number(process.hrtime.bigint() - t0) / iters);
    }
    runs.sort((a, b) => a - b);
    return runs[Math.floor(runs.length / 2)];
  };
  const out: Record<string, number> = {};
  out.AccessPathForPath = time(20000, () => n.forPath("src/components/button.tsx").matchValues());
  out.ForToolPathRead = time(20000, () => n.forToolPath("read", "src/components/button.tsx"));
  out.Win32Comparable = time(20000, () => win32PathFlavor.comparable("src\\Components\\Button.tsx", "C:\\Projects\\My-App"));
  out.NormalizeInputMcp = time(20000, () => normalizeInput("mcp", mcpInput, servers));
  out.CreatePiMcpToolTargets = time(20000, () => createPiMcpToolTargets("mcp__danger_srv__wipe", piServers));
  writeFileSync(process.env.BENCH_OUT as string, `${JSON.stringify(out)}\n`);
});
