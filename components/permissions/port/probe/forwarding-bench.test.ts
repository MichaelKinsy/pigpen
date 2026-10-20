// Forwarding benchmark: the original's cost of the paths the forwarding layer runs on a timer or per ask, for comparison with the
// Go benchmarks in fwd_bench_test.go (same operations, same inputs). Copied into an installed copy of the original by
// port/bench-forwarding.sh and never run from here. Writes {op: {nsPerOp, bytesPerOp}} to BENCH_OUT.
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { runInNewContext } from "node:vm";
import { setFlagsFromString } from "node:v8";
import { it } from "vitest";
import { ForwardedRequestServer } from "#src/authority/forwarded-request-server";
import { ServingHeartbeatStore } from "#src/authority/forwarding-liveness";
import { createPermissionForwardingLocation } from "#src/authority/permission-forwarding";

setFlagsFromString("--expose-gc");
const gc = runInNewContext("gc") as () => void;

function measure(iterations: number, setup: () => void, body: () => unknown): { nsPerOp: number; bytesPerOp: number } {
  const bodyOnly = async () => {
    for (let i = 0; i < 50; i++) { setup(); await body(); } // warm up
    gc();
    let total = 0n;
    let bytes = 0;
    for (let i = 0; i < iterations; i++) {
      setup();
      const heapBefore = process.memoryUsage().heapUsed;
      const t = process.hrtime.bigint();
      await body();
      total += process.hrtime.bigint() - t;
      bytes += Math.max(0, process.memoryUsage().heapUsed - heapBefore);
    }
    return { nsPerOp: Number(total) / iterations, bytesPerOp: bytes / iterations };
  };
  return bodyOnly() as never;
}

it("measures the forwarding paths", { timeout: 300_000 }, async () => {
  const output = process.env.BENCH_OUT;
  if (!output) throw new Error("BENCH_OUT is required");
  const root = mkdtempSync(join(tmpdir(), "fwd-bench-"));
  const silent = { review: () => undefined, debug: () => undefined };
  const ctx = {
    hasUI: true,
    ui: { select: () => Promise.resolve(undefined), input: () => Promise.resolve(undefined) },
    cwd: "/repo",
    sessionManager: { getSessionId: () => "parent-session", getSessionDir: () => "", getEntries: () => [] },
  };
  const makeServer = (forwardingDir: string) =>
    new ForwardedRequestServer({
      forwardingDir,
      logger: silent,
      policy: { resolve: () => ({ toolName: "read", source: "tool", origin: "builtin", state: "allow" }) as never },
      escalator: { escalate: () => Promise.resolve({ approved: true, state: "approved", decidedBy: { kind: "user", via: "dialog" } }) },
      broadcaster: { emitDecision: () => undefined },
      recorder: { recordSessionApproval: () => undefined },
    });
  const out: Record<string, unknown> = {};
  try {
    const idle = makeServer(join(root, "idle"));
    out.ProcessInboxIdle = await measure(2000, () => undefined, () => idle.processInbox(ctx));

    const hbDir = join(root, "hb");
    const store = new ServingHeartbeatStore({ forwardingDir: hbDir, logger: silent, now: () => 1_700_000_000_000, pid: 4242, isProcessAlive: (pid) => pid === 4242 });
    store.markServing("sess-1");
    out.MarkServingThrottled = await measure(20000, () => undefined, () => store.markServing("sess-1"));
    out.HeartbeatRead = await measure(2000, () => undefined, () => store.read("sess-1"));

    const serveDir = join(root, "serve");
    const location = createPermissionForwardingLocation(serveDir, "parent-session");
    mkdirSync(location.requestsDir, { recursive: true });
    mkdirSync(location.responsesDir, { recursive: true });
    const request = JSON.stringify({
      id: "req-1", createdAt: 1000, requesterSessionId: "child-session", targetSessionId: "parent-session", requesterAgentName: "Explore",
      payload: { kind: "tool", request: { requester: { agentName: null, forwarded: false, sessionId: null }, surface: "read", toolName: "read", invokedToolName: null, value: "read", matchedPattern: null, commandContext: null, executedUnit: null }, evidence: [], annotations: [] },
      source: "tool_call", surface: "bash", value: "git status",
      accessIntent: { surface: "bash", matchValues: ["git status"], boundaryValue: null, requesterCwd: "/worktree/issue-42", principal: { sessionId: "child-session", agentName: "Explore" } },
    });
    const server = makeServer(serveDir);
    out.ServeOneRequest = await measure(300, () => {
      mkdirSync(location.requestsDir, { recursive: true });
      writeFileSync(join(location.requestsDir, "req-1.json"), request, { mode: 0o600 });
    }, () => server.processInbox(ctx));
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
  writeFileSync(output, JSON.stringify(out));
});
