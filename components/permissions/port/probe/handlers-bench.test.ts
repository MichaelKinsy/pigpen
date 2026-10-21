// Latency of the service queries and one before_agent_start turn through the original's real stack, on the inputs of
// handlers_bench_test.go. Copied into an installed copy of the original for the run (port/bench-handlers.sh); never runs from here.
import { mkdirSync, mkdtempSync, realpathSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { it } from "vitest";
import { warmBashParser } from "#src/access-intent/bash/parser";
import { DEFAULT_EXTENSION_CONFIG } from "#src/config/extension-config";
import { AgentPrepHandler } from "#src/handlers/before-agent-start";
import { SessionTurnPrep } from "#src/handlers/session-turn-prep";
import { PermissionManager } from "#src/policy/permission-manager";
import { PermissionResolver } from "#src/policy/permission-resolver";
import { LocalPermissionsService } from "#src/service/permissions-service";
import { SessionRules } from "#src/session/session-rules";
import { ToolAccessExtractorRegistry } from "#src/tool-input/tool-access-extractor-registry";
import { ToolInputFormatterRegistry } from "#src/tool-input/tool-input-formatter-registry";
import { AuthorizerRegistry } from "#src/authority/authorizer-registry";
import { makeCtx } from "#test/helpers/handler-fixtures";
import { makeConfigStore, makeRealSession } from "#test/helpers/session-fixtures";

const base = realpathSync(mkdtempSync(join(tmpdir(), "pp-handlers-bench-")));
const cwd = join(base, "cwd");
mkdirSync(join(cwd, "src"), { recursive: true });
const globalConfigPath = join(base, "pi-permissions.jsonc");
const agentsDir = join(base, "agents");
mkdirSync(agentsDir, { recursive: true });
writeFileSync(globalConfigPath, JSON.stringify({ permission: { "*": "allow", skill: { "*": "allow", "secret-*": "deny" }, path: { "*": "allow", "*.env": "deny" }, mcp: { "*": "allow" }, bash: { "*": "allow", "rm *": "deny" }, write: "deny" } }));
const manager = new PermissionManager({ globalConfigPath, agentsDir, mcpServerNames: [] });
const sessionRules = new SessionRules();
const { session } = makeRealSession({ permissionManager: manager as never, sessionRules, configStore: makeConfigStore({ current: () => ({ ...DEFAULT_EXTENSION_CONFIG }) as never }) });
const resolver = new PermissionResolver(manager, sessionRules);
const service = new LocalPermissionsService(resolver, session, new ToolInputFormatterRegistry(), new ToolAccessExtractorRegistry(), new AuthorizerRegistry());
const ctx = makeCtx({ cwd } as never);
session.activate(ctx as never);

const toolNames = ["read", "bash", "edit", "write", "ls", "find", "grep", "weather_lookup", "mcp__srv__list", "mcp__srv__get", "task", "todo"];
let active = [...toolNames];
const registry = { getAll: () => toolNames.map((name) => ({ name })), getActive: () => active, setActive: (n: string[]) => { active = n; } };
const skills = Array.from({ length: 8 }, (_, i) => ({ name: i % 3 === 0 ? `secret-${i}` : `skill-${i}`, description: `Description ${i}`, filePath: `/skills/${i}/SKILL.md`, baseDir: `/skills/${i}` }));
const catalogue = `You are an assistant.\n\n<available_skills>\n${skills.map((s) => `  <skill>\n    <name>${s.name}</name>\n    <description>${s.description}</description>\n    <location>${s.filePath}</location>\n  </skill>`).join("\n")}\n</available_skills>`;
const handler = new AgentPrepHandler(new SessionTurnPrep(session, () => undefined, { announceReady: () => undefined }, { report: () => undefined }), session, resolver, registry as never, { debug: () => undefined } as never, { isSubagent: () => false });
const turn = () => handler.handle({ systemPrompt: catalogue, systemPromptOptions: { toolSnippets: {}, promptGuidelines: [], sections: {}, skills: [...skills] } } as never, ctx as never);

async function timeSync(fn: () => unknown, n: number): Promise<number> {
  for (let i = 0; i < 300; i++) fn();
  const start = process.hrtime.bigint();
  for (let i = 0; i < n; i++) fn();
  return Number(process.hrtime.bigint() - start) / n;
}

it("times the service queries and one agent-prep turn", async () => {
  await warmBashParser();
  const out: Record<string, number> = {};
  out.ServiceSkill = await timeSync(() => service.checkPermission("skill", "skill-1", "agent"), 20000);
  out.ServiceReadPath = await timeSync(() => service.checkPermission("read", join(cwd, "src", "a.ts")), 20000);
  out.ServiceMcp = await timeSync(() => service.checkPermission("mcp", "srv:list"), 20000);
  out.ServiceBashChain = await timeSync(() => service.checkPermission("bash", "git status && ls src | wc -l"), 5000);
  for (let i = 0; i < 300; i++) await turn();
  const start = process.hrtime.bigint();
  for (let i = 0; i < 2000; i++) await turn();
  out.AgentPrepTurn = Number(process.hrtime.bigint() - start) / 2000;
  console.log(`BENCH ${JSON.stringify(out)}`);
});
