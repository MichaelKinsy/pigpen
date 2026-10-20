// Forwarding probe: runs port/probe/forwarding-cases.json through the original's own src/authority functions and writes what they
// do, for the Go oracle test (extensions/pi-permission-system/fwd_oracle_test.go reads testdata/forwarding_oracle.json). It is copied
// into an installed copy of the original by port/record-forwarding-probe.sh and never runs from here; the port itself has no Node.
import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  readdirSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { it } from "vitest";
import { encloseInDelegationEnvelope } from "#src/authority/delegation-envelope";
import { resolutionFor } from "#src/authority/decision-resolution";
import { asDecisionSource } from "#src/authority/decision-source";
import { ForwardedRequestServer } from "#src/authority/forwarded-request-server";
import {
  readForwardedPermissionRequest,
  readForwardedPermissionResponse,
} from "#src/authority/forwarding-io";
import {
  ServingHeartbeatStore,
  servingHeartbeatDir,
  servingHeartbeatPath,
} from "#src/authority/forwarding-liveness";
import {
  createPermissionForwardingLocation,
  encodeSessionIdForPath,
  normalizePermissionForwardingSessionId,
  resolvePermissionForwardingTarget,
} from "#src/authority/permission-forwarding";
import { SubagentSessionRegistry } from "#src/authority/subagent-registry";

type Json = unknown;

function tryParse(raw: string): Json {
  try {
    return JSON.parse(raw);
  } catch {
    return undefined;
  }
}

/** What JSON.stringify would write: undefined members dropped. */
function plain(value: Json): Json {
  return value === undefined ? null : JSON.parse(JSON.stringify(value));
}

it("records the original's forwarding behaviour for the forwarding cases", { timeout: 120_000 }, async () => {
  const input = process.env.PROBE_IN;
  const output = process.env.PROBE_OUT;
  if (!input || !output) throw new Error("PROBE_IN and PROBE_OUT are required");
  const cases = JSON.parse(readFileSync(input, "utf8"));
  const root = mkdtempSync(join(tmpdir(), "fwd-probe-"));
  const silent = { review: () => undefined, debug: () => undefined };
  const out: Record<string, Json> = {};

  try {
    // ── tolerant reads of the files another process wrote ───────────────────
    out.requests = (cases.requests as string[]).map((raw, i) => {
      const path = join(root, `req-${i}.json`);
      writeFileSync(path, raw);
      return { raw, result: plain(readForwardedPermissionRequest(null, path)) };
    });
    out.responses = (cases.responses as string[]).map((raw, i) => {
      const path = join(root, `res-${i}.json`);
      writeFileSync(path, raw);
      const result = readForwardedPermissionResponse(null, path);
      const parsed = tryParse(raw) as { respondedAt?: unknown } | null;
      const plainResult = plain(result) as { respondedAt?: unknown } | null;
      // respondedAt falls back to the read time, which no two runs share.
      if (plainResult && typeof parsed?.respondedAt !== "number") {
        plainResult.respondedAt = "NOW";
      }
      return { raw, result: plainResult };
    });

    // ── serving heartbeats ──────────────────────────────────────────────────
    const hb = cases.heartbeats as { now: number; alivePid: number; raws: string[] };
    const forwardingDir = join(root, "hb");
    mkdirSync(servingHeartbeatDir(forwardingDir), { recursive: true });
    const reader = new ServingHeartbeatStore({
      forwardingDir,
      logger: silent,
      now: () => hb.now,
      pid: hb.alivePid,
      isProcessAlive: (pid) => pid === hb.alivePid,
    });
    const states = hb.raws.map((raw, i) => {
      writeFileSync(servingHeartbeatPath(forwardingDir, `h${i}`), raw);
      return { raw, state: reader.read(`h${i}`) };
    });
    const servingIds = [...reader.servingIds()].sort();
    // The first announcement sweeps records of dead processes and unusable ones; what survives is the rule's observable result.
    new ServingHeartbeatStore({
      forwardingDir,
      logger: silent,
      now: () => hb.now,
      pid: hb.alivePid,
      isProcessAlive: (pid) => pid === hb.alivePid,
    }).markServing("me");
    const afterSweep = readdirSync(servingHeartbeatDir(forwardingDir)).sort();
    out.heartbeats = { now: hb.now, alivePid: hb.alivePid, states, servingIds, afterSweep };

    // ── target resolution ───────────────────────────────────────────────────
    out.targets = (cases.targets as Array<Record<string, unknown>>).map((c) => {
      let registry: SubagentSessionRegistry | undefined;
      if (c.registry) {
        registry = new SubagentSessionRegistry();
        for (const [id, info] of Object.entries(c.registry as Record<string, { parentSessionId?: string }>)) {
          registry.register(id, info);
        }
      }
      const result = resolvePermissionForwardingTarget({
        isSubagent: c.isSubagent as boolean,
        currentSessionId: c.currentSessionId as string | null,
        env: c.env as NodeJS.ProcessEnv,
        sessionId: (c.sessionId as string | null) ?? undefined,
        registry,
      });
      return { case: c, result: plain(result) };
    });

    // ── session ids on disk ─────────────────────────────────────────────────
    out.sessionIds = (cases.sessionIds as string[]).map((id) => {
      let location: Json;
      try {
        location = plain(createPermissionForwardingLocation("/root", id));
      } catch (error) {
        location = { error: (error as Error).message };
      }
      return {
        id,
        normalized: normalizePermissionForwardingSessionId(id),
        encoded: encodeSessionIdForPath(id),
        location,
      };
    });

    // ── resolution naming and the delegation envelope ───────────────────────
    out.resolutions = (cases.resolutions as Array<{ decidedBy: unknown; approved: boolean; forSession: boolean }>).map((c) => {
      // resolutionFor takes a narrowed source; the guard is what reads one off disk, so it decides which cases reach it.
      const decider = asDecisionSource(c.decidedBy);
      return {
        ...c,
        decider: plain(decider),
        resolution: decider ? resolutionFor(decider, { approved: c.approved, forSession: c.forSession }) : null,
      };
    });
    out.envelope = await Promise.all(
      (cases.envelope as Array<{ accessIntentSurface: string | null; displaySurface: string | null; verdict: string }>).map(async (c) => {
        const details = {
          requestId: "x",
          source: "tool_call",
          agentName: null,
          payload: {},
          surface: c.displaySurface,
          ...(c.accessIntentSurface === null
            ? {}
            : { accessIntent: { surface: c.accessIntentSurface, matchValues: [], boundaryValue: null } }),
        };
        const link = () =>
          Promise.resolve(c.verdict === "deny" ? { kind: "deny" as const, reason: "r" } : { kind: c.verdict as "allow" | "defer" });
        const enclosed = encloseInDelegationEnvelope(link);
        return { ...c, result: plain(await enclosed(details as never, {} as never, {} as never)) };
      }),
    );

    // ── the serving node, end to end ────────────────────────────────────────
    const server: Json[] = [];
    for (const [n, c] of (cases.server as Array<Record<string, any>>).entries()) {
      const dir = join(root, `srv-${n}`);
      const forwardingRoot = join(dir, "forwarding");
      const location = createPermissionForwardingLocation(forwardingRoot, "parent-session");
      mkdirSync(location.requestsDir, { recursive: true });
      mkdirSync(location.responsesDir, { recursive: true });
      for (const file of c.files as Array<{ name: string; raw: string }>) {
        writeFileSync(join(location.requestsDir, file.name), file.raw);
      }
      const review: Json[] = [];
      const debug: Json[] = [];
      const escalated: Json[] = [];
      const policyCalls: Json[] = [];
      const broadcast: Json[] = [];
      const recorded: Json[] = [];
      const instance = new ForwardedRequestServer({
        forwardingDir: forwardingRoot,
        logger: {
          review: (event: string, details?: Record<string, unknown>) => review.push([event, plain(details)]),
          debug: (event: string, details?: Record<string, unknown>) => debug.push([event, plain(details)]),
        },
        policy: {
          resolve: (intent) => {
            policyCalls.push(plain(intent));
            const p = c.policy ?? { state: "ask" };
            return { toolName: "read", source: "tool", origin: p.origin ?? "builtin", state: p.state, matchedPattern: p.matchedPattern, reason: p.reason } as never;
          },
        },
        escalator: {
          escalate: (details) => {
            escalated.push(plain(details));
            if ("throw" in c.escalator) return Promise.reject(new Error(c.escalator.throw));
            return Promise.resolve(JSON.parse(JSON.stringify(c.escalator.decision)));
          },
        },
        broadcaster: { emitDecision: (event) => broadcast.push(plain(event)) },
        recorder: { recordSessionApproval: (approval) => recorded.push(plain(approval.grants)) },
      });
      await instance.processInbox({
        hasUI: true,
        ui: { select: () => Promise.resolve(undefined), input: () => Promise.resolve(undefined) },
        cwd: "/repo",
        sessionManager: { getSessionId: () => "parent-session", getSessionDir: () => "", getEntries: () => [] },
      });
      const responses: Record<string, string> = {};
      if (existsSync(location.responsesDir)) {
        for (const name of readdirSync(location.responsesDir).sort()) {
          responses[name] = readFileSync(join(location.responsesDir, name), "utf8");
        }
      }
      const result = {
        responses,
        requestsLeft: existsSync(location.requestsDir) ? readdirSync(location.requestsDir).sort() : null,
        responsesDirExists: existsSync(location.responsesDir),
        sessionRootExists: existsSync(location.sessionRootDir),
        escalated,
        policyCalls,
        broadcast,
        recorded,
        review,
        debug,
      };
      // Paths and the read time differ between runs; everything else is the original's own output.
      const text = JSON.stringify(result).split(dir).join("<DIR>").replace(/\\"respondedAt\\":\d+/g, '\\"respondedAt\\":\\"NOW\\"');
      server.push({ name: c.name, files: c.files, policy: c.policy, escalator: c.escalator, result: JSON.parse(text) });
    }
    out.server = server;
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
  writeFileSync(output, JSON.stringify(out));
});
