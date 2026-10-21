// Manager probe: runs the original's PermissionManager over a corpus of worlds (config files, agent files, session rules, yolo, MCP
// servers) and writes every verdict it returns, for the Go differential test
// (extensions/pi-permission-system/testdata/manager-oracle.json). It is copied into an installed copy of the original by
// port/record-manager-probe.sh and never runs from here; the port itself has no Node.
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { homedir, tmpdir } from "node:os";
import { join } from "node:path";
import { it } from "vitest";
import type { ResolvedAccessIntent } from "#src/access-intent/access-intent";
import { getActiveAgentName, getActiveAgentNameFromSystemPrompt } from "#src/session/active-agent";
import { PermissionManager } from "#src/policy/permission-manager";
import { PermissionResolver } from "#src/policy/permission-resolver";
import type { Ruleset } from "#src/policy/rule";

const must = (v: string | undefined, name: string): string => {
  if (!v) throw new Error(`${name} is required`);
  return v;
};

type World = {
  id: string;
  global: string | null;
  project: string | null;
  agents: Record<string, string>;
  projectAgents: Record<string, string>;
  unreadableAgents: string[];
  unreadableProjectAgents: string[];
  mcp: string[];
  yolo: boolean;
  sessionRules: Ruleset;
  agentNames: (string | null)[];
  tools: string[];
  intents: (ResolvedAccessIntent & { agentName?: string })[];
};

const scrub = (v: unknown, base: string): unknown => JSON.parse(JSON.stringify(v).split(base).join("<BASE>"));

it("records the manager corpus", () => {
  const cases: World[] = JSON.parse(
    readFileSync(must(process.env.PROBE_CASES, "PROBE_CASES"), "utf8"),
  );
  const worlds: unknown[] = [];
  for (const w of cases) {
    const base = mkdtempSync(join(tmpdir(), "pp-manager-probe-"));
    try {
      const globalConfigPath = join(base, "pi-permissions.jsonc");
      const agentsDir = join(base, "agents");
      const projectRoot = join(base, "project");
      const projectConfigPath = join(projectRoot, "pi-permissions.jsonc");
      const projectAgentsDir = join(projectRoot, "agents");
      mkdirSync(agentsDir, { recursive: true });
      mkdirSync(projectAgentsDir, { recursive: true });
      if (w.global !== null) writeFileSync(globalConfigPath, w.global);
      if (w.project !== null) writeFileSync(projectConfigPath, w.project);
      for (const [n, c] of Object.entries(w.agents)) writeFileSync(join(agentsDir, `${n}.md`), c);
      for (const [n, c] of Object.entries(w.projectAgents)) writeFileSync(join(projectAgentsDir, `${n}.md`), c);
      for (const n of w.unreadableAgents) mkdirSync(join(agentsDir, `${n}.md`));
      for (const n of w.unreadableProjectAgents) mkdirSync(join(projectAgentsDir, `${n}.md`));

      const manager = new PermissionManager({
        globalConfigPath,
        agentsDir,
        projectGlobalConfigPath: projectConfigPath,
        projectAgentsDir,
        mcpServerNames: w.mcp,
        isYoloEnabled: () => w.yolo,
      });

      const results = w.intents.map((intent) => {
        try {
          return { result: manager.check(intent, w.sessionRules) };
        } catch (e) {
          return { throws: String(e) };
        }
      });
      // The resolver over the same manager: the family fold, the access-path unwrap and the pre-resolved (path-values) wire. A path-values
      // intent is also run as an access-path intent whose matchValues() is the same list.
      const resolver = new PermissionResolver(manager, { getRuleset: () => w.sessionRules });
      const resolved = w.intents.map((intent) => {
        const run = (i: Parameters<PermissionResolver["resolve"]>[0]) => {
          try {
            return { result: resolver.resolve(i) };
          } catch (e) {
            return { throws: String(e) };
          }
        };
        const direct = run(intent);
        if (intent.kind !== "path-values") return { direct };
        const accessPath = { kind: "access-path", surface: intent.surface, agentName: intent.agentName, path: { matchValues: () => intent.values } };
        return { direct, viaAccessPath: run(accessPath as unknown as Parameters<PermissionResolver["resolve"]>[0]) }; // a duck-typed AccessPath: the resolver reads only matchValues()
      });
      const display: Record<string, unknown> = {};
      for (const agent of w.agentNames) {
        const key = agent ?? "<none>";
        const a = agent ?? undefined;
        display[key] = {
          toolPermission: Object.fromEntries(w.tools.map((t) => [t, manager.getToolPermission(t, a)])),
          fullyDenied: Object.fromEntries(w.tools.map((t) => [t, manager.isToolFullyDenied(t, a)])),
          issues: manager.getConfigIssues(a),
          composed: manager.getComposedConfigRules(a),
        };
      }
      worlds.push(scrub({ id: w.id, results, resolved, display }, base));
    } finally {
      rmSync(base, { recursive: true, force: true });
    }
  }
  writeFileSync(
    must(process.env.PROBE_OUT, "PROBE_OUT"),
    `${JSON.stringify({ home: homedir(), worlds })}\n`,
  );
});

it("records the active-agent corpus", () => {
  const cases = JSON.parse(readFileSync(must(process.env.PROBE_AGENT_CASES, "PROBE_AGENT_CASES"), "utf8")) as {
    prompts: string[];
    entries: { type: string; customType?: string; data?: unknown }[][];
  };
  const prompts = cases.prompts.map((p) => getActiveAgentNameFromSystemPrompt(p));
  const entries = cases.entries.map((es) => {
    try {
      return { name: getActiveAgentName({ sessionManager: { getEntries: () => es } }) };
    } catch (e) {
      return { throws: String(e) };
    }
  });
  writeFileSync(must(process.env.PROBE_AGENT_OUT, "PROBE_AGENT_OUT"), `${JSON.stringify({ prompts, entries })}\n`);
});
