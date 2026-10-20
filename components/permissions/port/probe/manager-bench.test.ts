// Manager benchmark: the original's cost of the policy paths, for comparison with the Go benchmarks in pol_bench_test.go (same
// operations, same inputs). Copied into an installed copy of the original by port/bench-manager.sh and never run from here. Writes
// {op: {nsPerOp, bytesPerOp}} to BENCH_OUT.
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { runInNewContext } from "node:vm";
import { setFlagsFromString } from "node:v8";
import { it } from "vitest";
import { PermissionManager } from "#src/policy/permission-manager";
import { PermissionResolver } from "#src/policy/permission-resolver";
import type { ResolvedAccessIntent } from "#src/access-intent/access-intent";
import { SessionApproval } from "#src/session/session-approval";
import { SessionRules } from "#src/session/session-rules";
import type { Ruleset } from "#src/policy/rule";

setFlagsFromString("--expose-gc");
const gc = runInNewContext("gc") as () => void;

function measure(iterations: number, body: () => unknown): { nsPerOp: number; bytesPerOp: number } {
  for (let i = 0; i < 50; i++) body(); // warm up
  gc();
  let total = 0n;
  let bytes = 0;
  for (let i = 0; i < iterations; i++) {
    const heapBefore = process.memoryUsage().heapUsed;
    const t = process.hrtime.bigint();
    body();
    total += process.hrtime.bigint() - t;
    bytes += Math.max(0, process.memoryUsage().heapUsed - heapBefore);
  }
  return { nsPerOp: Number(total) / iterations, bytesPerOp: bytes / iterations };
}

const GLOBAL =
  '{"permission":{"*":"ask","bash":{"*":"ask","git *":"allow","git push*":"ask","rm -rf *":"deny","npm *":"allow","ls":"allow","echo *":"allow","sudo *":"deny","node -e *":"ask","cat *":"allow","grep *":"allow","find *":"ask"},' +
  '"read":{"*":"allow","*.env":"deny","/secret/*":"deny"},"path":{"*":"allow","~/.ssh/*":"deny"},"external_directory":{"/tmp/*":"allow","/work/*":"allow"},' +
  '"mcp":{"*":"ask","exa_*":"allow","danger":"deny"},"skill":{"*":"ask","librarian":"allow"}}}';
const PROJECT = '{"permission":{"bash":{"make *":"allow","git push --force*":"deny"},"write":{"*":"ask","src/*":"allow"}}}';
const AGENT = '---\nname: reviewer\npermission:\n  bash:\n    "git log *": allow\n    "*": ask\n  read: allow\n---\n# body\n';

it("measures the policy paths", { timeout: 300_000 }, () => {
  const output = process.env.BENCH_OUT;
  if (!output) throw new Error("BENCH_OUT is required");
  const base = mkdtempSync(join(tmpdir(), "mgr-bench-"));
  const global = join(base, "pi-permissions.jsonc");
  const agentsDir = join(base, "agents");
  const project = join(base, "project", "pi-permissions.jsonc");
  const files: Record<string, string> = { [global]: GLOBAL, [project]: PROJECT, [join(agentsDir, "reviewer.md")]: AGENT };
  for (const [p, c] of Object.entries(files)) {
    mkdirSync(dirname(p), { recursive: true });
    writeFileSync(p, c);
  }
  const options = (yolo: boolean) => ({
    globalConfigPath: global, agentsDir, projectGlobalConfigPath: project, projectAgentsDir: join(base, "project", "agents"),
    mcpServerNames: ["exa", "danger"], isYoloEnabled: () => yolo,
  });
  const bash = (command: string): ResolvedAccessIntent => ({ kind: "tool", surface: "bash", input: { command }, agentName: "reviewer" });
  const session: Ruleset = ["make test", "make build", "go test *", "go vet *", "cargo *", "pnpm *", "yarn *", "python *", "pytest *", "tsc *"].map(
    (pattern) => ({ surface: "bash", pattern, action: "allow", layer: "session", origin: "session" }),
  );
  const out: Record<string, unknown> = {};
  try {
    const warm = new PermissionManager(options(false));
    const push = bash("git push origin main");
    warm.check(push);
    out.CheckBashWarm = measure(20000, () => warm.check(push));

    const make = bash("make test");
    warm.check(make, session);
    out.CheckBashSessionRules = measure(20000, () => warm.check(make, session));

    const yolo = new PermissionManager(options(true));
    const find = bash("find . -name x");
    yolo.check(find);
    out.CheckBashYolo = measure(20000, () => yolo.check(find));

    const pv: ResolvedAccessIntent = { kind: "path-values", surface: "path_read", values: ["/work/project/src/app.ts", "src/app.ts"], agentName: "reviewer" };
    warm.check(pv);
    out.CheckPathValuesWarm = measure(20000, () => warm.check(pv));

    out.CheckCold = measure(500, () => new PermissionManager(options(false)).check(push));

    warm.getToolPermission("bash", "reviewer");
    out.ToolPermissionAndDenied = measure(20000, () => {
      warm.getToolPermission("bash", "reviewer");
      warm.isToolFullyDenied("bash", "reviewer");
    });

    const rules = new SessionRules();
    rules.approve("external_directory", "/opt/*");
    const resolver = new PermissionResolver(new PermissionManager(options(false)), rules);
    const fold: ResolvedAccessIntent = { kind: "path-values", surface: "external_directory", values: ["/tmp/x/y.txt", "/private/tmp/x/y.txt"], agentName: "reviewer" };
    resolver.resolve(fold);
    out.ResolverFold = measure(20000, () => resolver.resolve(fold));

    out.SessionApprovals = measure(20000, () => {
      const r = new SessionRules();
      for (const p of ["/a/*", "/b/*", "/c/*", "/d/*"]) r.recordSessionApproval(SessionApproval.forPatterns("external_directory", [p, `${p}x`]));
      r.approve("bash", "git *");
      r.getRuleset();
    });
  } finally {
    rmSync(base, { recursive: true, force: true });
  }
  writeFileSync(output, `${JSON.stringify(out)}\n`);
});
