// Latency of one tool_call through the original's real gate stack, on the inputs of gatecore_bench_test.go. Copied into an installed copy
// of the original for the run (port/bench-gatecore.sh) and never runs from here.
import { mkdirSync, mkdtempSync, realpathSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
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

const base = realpathSync(mkdtempSync(join(tmpdir(), "pp-gatecore-bench-")));
const cwd = join(base, "cwd");
mkdirSync(join(cwd, "src"), { recursive: true });
const globalConfigPath = join(base, "pi-permissions.jsonc");
const agentsDir = join(base, "agents");
mkdirSync(agentsDir, { recursive: true });
writeFileSync(globalConfigPath, JSON.stringify({ permission: { "*": "allow", external_directory: { "*": "ask" }, path: { "*": "allow", "*.env": "deny" }, mcp: { "*": "allow" }, bash: { "*": "allow", "rm *": "deny" } } }));
const manager = new PermissionManager({ globalConfigPath, agentsDir, mcpServerNames: [] });
const sessionRules = new SessionRules();
const { session } = makeRealSession({ permissionManager: manager as never, sessionRules, configStore: makeConfigStore({ current: () => ({ ...DEFAULT_EXTENSION_CONFIG }) as never }) });
const resolver = new PermissionResolver(manager, sessionRules);
const logger = { debug: () => undefined, warn: () => undefined, review: () => undefined };
const events = { emit: () => undefined, on: () => () => undefined };
const prompter: AskEscalator = { escalate: () => Promise.resolve({ approved: true, state: "approved", decidedBy: { kind: "user", via: "dialog" } } as never) };
const runner = new GateRunner(resolver, new SessionRules(), prompter, new GateDecisionReporter(logger as never, events as never), () => false);
const tools = ["read", "write", "bash", "weather_lookup", "mcp__srv__list"].map((name) => ({ name }));
const handler = new PermissionGateHandler(session, makeToolRegistry({ getAll: () => tools as never }), new ToolCallGatePipeline(resolver, session), new SkillInputGatePipeline(resolver), runner);
const ctx = makeCtx({ cwd } as never);
const ev = (toolName: string, input: unknown) => ({ type: "tool_call", toolCallId: "tc-1", toolName, input });

const inputs: Record<string, unknown> = {
  ReadInside: ev("read", { path: "src/a.ts" }),
  ReadOutsideAsk: ev("read", { path: "/outside/project/file.ts" }),
  WriteDeniedByPath: ev("write", { path: "src/secret.env", content: "x" }),
  ExtensionTool: ev("weather_lookup", { city: "Chicago", units: "metric" }),
  McpTool: ev("mcp__srv__list", { target: "prod" }),
  BashChain: ev("bash", { command: "git status && ls src | wc -l" }),
  BashDeny: ev("bash", { command: "echo ok && rm -rf build" }),
};

async function time(event: unknown, n: number): Promise<number> {
  for (let i = 0; i < 200; i++) await handler.handleToolCall(event, ctx);
  const start = process.hrtime.bigint();
  for (let i = 0; i < n; i++) await handler.handleToolCall(event, ctx);
  return Number(process.hrtime.bigint() - start) / n;
}

it("times one tool_call through the gate pipeline", async () => {
  const out: Record<string, number> = {};
  for (const [name, event] of Object.entries(inputs)) out[name] = await time(event, 2000);
  console.log(`BENCH ${JSON.stringify(out)}`);
});
