// Gate core probe: runs testdata/gatecore-cases.json through the original's real gate stack (PermissionManager over real config and
// agent files, PermissionSession, PermissionResolver, ToolCallGatePipeline, SkillInputGatePipeline, GateRunner, PermissionGateHandler,
// GateDecisionReporter) and writes what each tool_call and input answers, the prompts it raised, the review-log entries and the
// decision events, for the Go differential (extensions/pi-permission-system/testdata/gatecore-oracle.json). Copied into an installed
// copy of the original by port/record-gatecore-probe.sh and never runs from here; the port itself has no Node.
import { mkdirSync, mkdtempSync, readFileSync, realpathSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { it } from "vitest";
import type { AskEscalator } from "#src/authority/authorizer-selection";
import { DEFAULT_EXTENSION_CONFIG } from "#src/config/extension-config";
import { GateRunner } from "#src/handlers/gates/runner";
import { SkillInputGatePipeline } from "#src/handlers/gates/skill-input-gate-pipeline";
import { ToolCallGatePipeline } from "#src/handlers/gates/tool-call-gate-pipeline";
import { PermissionGateHandler } from "#src/handlers/permission-gate-handler";
import { GateDecisionReporter } from "#src/logging/decision-reporter";
import { PermissionManager } from "#src/policy/permission-manager";
import { PermissionResolver } from "#src/policy/permission-resolver";
import { SessionRules } from "#src/session/session-rules";
import { makeCtx, makeToolRegistry } from "#test/helpers/handler-fixtures";
import { makeConfigStore, makeRealSession } from "#test/helpers/session-fixtures";

const must = (v: string | undefined, name: string): string => {
  if (!v) throw new Error(`${name} is required`);
  return v;
};

type Call =
  | { kind: "tool_call"; event: unknown; hasUI?: boolean }
  | { kind: "input"; text: string; hasUI?: boolean };

type Case = {
  id: string;
  config: string | null;
  agents: Record<string, string>;
  fs: { path: string; kind: "file" | "dir" | "symlink"; target?: string }[];
  sessionRules: { surface: string; pattern: string }[];
  tools: unknown[] | null;
  skills: unknown[];
  shellTools: unknown;
  yolo: boolean;
  agent: string | null;
  prompts: Record<string, unknown>[];
  calls: Call[];
};

function wf(value: unknown): unknown {
  if (typeof value === "string") return value.toWellFormed();
  if (Array.isArray(value)) return value.map(wf);
  if (value !== null && typeof value === "object") {
    return Object.fromEntries(Object.entries(value).map(([k, v]) => [k.toWellFormed(), wf(v)]));
  }
  return value;
}

it("records the gate core corpus", async () => {
  const cases: Case[] = JSON.parse(readFileSync(must(process.env.PROBE_CASES, "PROBE_CASES"), "utf8"));
  const results: unknown[] = [];
  for (const c of cases) {
    const base = realpathSync(mkdtempSync(join(tmpdir(), "pp-gatecore-probe-")));
    const dirs = { $CWD: join(base, "cwd"), $OUT: join(base, "out"), $ELSE: join(base, "else"), $AGENT: join(base, "agent"), $CFG: join(base, "cfg") };
    for (const d of Object.values(dirs)) mkdirSync(d, { recursive: true });
    const sub = (text: string): string => {
      let out = text;
      for (const [k, v] of Object.entries(dirs)) out = out.split(k).join(v);
      return out;
    };
    const scrub = (v: unknown): unknown => {
      let text = JSON.stringify(v);
      for (const [k, d] of Object.entries(dirs)) text = text.split(JSON.stringify(d).slice(1, -1)).join(k);
      // Each request id is replaced by its order of first appearance, so the join between a prompt, its review entries and its decision
      // event is kept while the random id is not.
      const ids = new Map<string, string>();
      text = text.replace(/perm-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/g, (id) => {
        if (!ids.has(id)) ids.set(id, `REQ${ids.size + 1}`);
        return ids.get(id) as string;
      });
      return JSON.parse(text);
    };
    try {
      const spec: Case = JSON.parse(sub(JSON.stringify(c)));
      for (const f of spec.fs) {
        mkdirSync(dirname(f.path), { recursive: true });
        if (f.kind === "dir") mkdirSync(f.path, { recursive: true });
        else if (f.kind === "file") writeFileSync(f.path, "x");
        else symlinkSync(f.target as string, f.path);
      }
      const globalConfigPath = join(dirs.$CFG, "pi-permissions.jsonc");
      const agentsDir = join(dirs.$CFG, "agents");
      mkdirSync(agentsDir, { recursive: true });
      if (spec.config !== null) writeFileSync(globalConfigPath, spec.config);
      for (const [n, text] of Object.entries(spec.agents)) writeFileSync(join(agentsDir, `${n}.md`), text);
      const yolo = spec.yolo;
      const manager = new PermissionManager({ globalConfigPath, agentsDir, mcpServerNames: [], isYoloEnabled: () => yolo });
      const sessionRules = new SessionRules();
      for (const r of spec.sessionRules) sessionRules.approve(r.surface, r.pattern);
      const configStore = makeConfigStore({
        current: () => ({ ...DEFAULT_EXTENSION_CONFIG, ...(spec.shellTools === null ? {} : { shellTools: spec.shellTools }) }) as never,
      });
      const { session } = makeRealSession({
        permissionManager: manager as never,
        sessionRules,
        configStore,
        paths: { piInfrastructureDirs: [dirs.$AGENT, join(dirs.$AGENT, "git")], piInfrastructureExcludedDirs: [join(dirs.$AGENT, "logs")] },
      });
      session.setActiveSkillEntries(spec.skills as never);
      const resolver = new PermissionResolver(manager, sessionRules);

      const reviews: unknown[] = [];
      const emitted: unknown[] = [];
      const logger = {
        debug: () => undefined,
        warn: () => undefined,
        review: (event: string, details: unknown) => {
          reviews.push([event, details]);
        },
      };
      const events = {
        emit: (channel: string, data: unknown) => {
          emitted.push([channel, data]);
        },
        on: () => () => undefined,
      };
      const prompts: unknown[] = [];
      let promptIndex = 0;
      const prompter: AskEscalator = {
        escalate: (details) => {
          // `origin` rides the tool gate's prompt details by an object spread of its log context; it is not a PromptPermissionDetails field.
          const { payload, origin: _origin, ...rest } = details as typeof details & { origin?: unknown };
          void _origin;
          prompts.push({ ...rest, payload });
          const answer = spec.prompts[Math.min(promptIndex, spec.prompts.length - 1)] ?? {
            approved: true,
            state: "approved",
            decidedBy: { kind: "user", via: "dialog" },
          };
          promptIndex += 1;
          return Promise.resolve(answer as never);
        },
      };
      const runner = new GateRunner(resolver, sessionRules, prompter, new GateDecisionReporter(logger as never, events as never), () => yolo);
      const handler = new PermissionGateHandler(
        session,
        makeToolRegistry({ getAll: () => (spec.tools ?? [{ name: "read" }, { name: "bash" }]) as never }),
        new ToolCallGatePipeline(resolver, session),
        new SkillInputGatePipeline(resolver),
        runner,
      );

      const notes: unknown[] = [];
      const answers: unknown[] = [];
      for (const call of spec.calls) {
        const entries = spec.agent === null ? [] : [{ type: "custom", customType: "active_agent", data: { name: spec.agent } }];
        const ctx = makeCtx({
          cwd: dirs.$CWD,
          hasUI: call.hasUI ?? true,
          ui: { setStatus: () => undefined, notify: (m: string, l: string) => notes.push([m, l]), select: () => undefined, input: () => undefined },
          sessionManager: { getEntries: () => entries, getSessionDir: () => "/sessions/test", getSessionId: () => "test-session", getSessionName: () => undefined, addEntry: () => undefined },
        } as never);
        try {
          if (call.kind === "tool_call") answers.push({ result: await handler.handleToolCall(call.event, ctx) });
          else answers.push({ result: await handler.handleInput({ text: call.text }, ctx) });
        } catch (error) {
          answers.push({ throws: String((error as Error).message) });
        }
      }
      results.push(scrub(wf({ id: c.id, answers, prompts, reviews, decisions: emitted, notes, rules: sessionRules.getRuleset() })));
    } finally {
      rmSync(base, { recursive: true, force: true });
    }
  }
  writeFileSync(must(process.env.PROBE_OUT, "PROBE_OUT"), `${JSON.stringify({ cases: results })}\n`);
});
