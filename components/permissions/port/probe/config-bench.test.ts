// Times the original's config functions on the fixture of extensions/pi-permission-system/cfg_bench_test.go. Copied into an installed
// copy of the original for the run (see port/record-config-probe.sh for the pattern); prints one JSON line per measurement.
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { it } from "vitest";
import { loadAndMergeConfigs, loadUnifiedConfig } from "#src/config/config-loader";
import { FilePolicyLoader } from "#src/config/policy-loader";

function put(p: string, c: string) {
  mkdirSync(dirname(p), { recursive: true });
  writeFileSync(p, c);
}

it("times the original's config system", () => {
  const tmp = mkdtempSync(join(tmpdir(), "cfg-bench-"));
  try {
    const agentDir = join(tmp, "agent");
    const cwd = join(tmp, "project");
    put(join(agentDir, "extensions/pi-permission-system/config.json"), `{"yoloMode":false,"permissionReviewLog":true,"promptMaxRows":24,"permission":{"*":"ask","read":"allow","write":{"*":"deny","src/*":"allow"},"bash":{"*":"ask","git status":"allow","git diff *":"allow","npm *":{"action":"deny","reason":"Use pnpm instead"},"rm *":"deny"},"mcp":{"exa:*":"allow"},"skill":{"*":"ask","librarian":"allow"},"path":{"*":"allow","*.env":"deny"},"external_directory_read":{"~/dev/*":"allow"}}}`);
    put(join(cwd, ".pi/extensions/pi-permission-system/config.json"), `{"permission":{"bash":{"pnpm *":"allow"},"edit":"ask"}}`);
    put(join(agentDir, "agents/coder.md"), "---\nname: coder\ndescription: Does things\npermission:\n  bash:\n    \"git *\": allow\n    \"rm *\": deny\n  read: allow\n---\n# Coder\n");
    put(join(agentDir, "mcp.json"), `{"mcpServers":{"exa":{},"research":{},"danger-srv":{}}}`);
    const time = (name: string, n: number, fn: () => void) => {
      for (let i = 0; i < 200; i++) fn();
      const runs: number[] = [];
      for (let r = 0; r < 10; r++) {
        const t0 = process.hrtime.bigint();
        for (let i = 0; i < n; i++) fn();
        runs.push(Number(process.hrtime.bigint() - t0) / n);
      }
      runs.sort((a, b) => a - b);
      console.log(JSON.stringify({ name, medianNsPerOp: runs[5] }));
    };
    time("LoadAndMergeConfigs", 2000, () => loadAndMergeConfigs(agentDir, cwd, join(tmp, "ext")));
    const loader = new FilePolicyLoader({
      globalConfigPath: join(agentDir, "extensions/pi-permission-system/config.json"),
      agentsDir: join(agentDir, "agents"),
      projectGlobalConfigPath: join(cwd, ".pi/extensions/pi-permission-system/config.json"),
      projectAgentsDir: join(cwd, ".pi/agents"),
      globalMcpConfigPath: join(agentDir, "mcp.json"),
      projectMcpConfigPath: join(cwd, ".pi/mcp.json"),
    });
    loader.loadGlobalConfig();
    time("PolicyLoaderCached", 20000, () => {
      loader.getCacheStamp("coder");
      loader.loadGlobalConfig();
      loader.loadProjectConfig();
      loader.loadAgentConfig("coder");
      loader.loadProjectAgentConfig("coder");
      loader.getConfiguredMcpServerNames();
    });
    time("LoadUnifiedConfig", 5000, () => loadUnifiedConfig(join(agentDir, "extensions/pi-permission-system/config.json")));
  } finally {
    rmSync(tmp, { recursive: true, force: true });
  }
});
