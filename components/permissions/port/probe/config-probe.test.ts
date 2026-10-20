// Config-system probe: runs port/probe/config-system-cases.json through the original's config functions and writes what they decide,
// for the Go differential (extensions/pi-permission-system/testdata/config-oracle.json). It is copied into an installed copy of the
// original by port/record-config-probe.sh and never runs from here; the port itself has no Node.
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { Key, matchesKey } from "@earendil-works/pi-tui";
import { it } from "vitest";
import { buildPermissionsJsonSchema } from "#src/config/config-schema";
import { loadAndMergeConfigs, loadUnifiedConfig } from "#src/config/config-loader";
import { registerPermissionSystemCommand } from "#src/config/config-modal";
import { ConfigStore } from "#src/config/config-store";
import { BINDABLE_DIALOG_KEY_CHARACTERS, resolveDialogKeys } from "#src/config/dialog-keys";
import { normalizePermissionSystemConfig } from "#src/config/extension-config";
import { FilePolicyLoader } from "#src/config/policy-loader";
import { extractFrontmatter, parseSimpleYamlMap } from "#src/config/yaml-frontmatter";

type Files = Record<string, string | null | undefined>;

function put(root: string, rel: string, content: string | null | undefined): void {
  if (content === undefined || content === null) return;
  const p = join(root, rel);
  mkdirSync(dirname(p), { recursive: true });
  writeFileSync(p, content);
}

it("records the original's config-system decisions", async () => {
  const input = process.env.PROBE_IN;
  const output = process.env.PROBE_OUT;
  if (!input || !output) throw new Error("PROBE_IN and PROBE_OUT are required");
  const cases = JSON.parse(readFileSync(input, "utf8"));
  const out: Record<string, unknown> = { cases };

  // pi-tui facts the dialog-keys tests read from the dependency
  const symbols = Object.values(Key).filter((v): v is string => typeof v === "string" && v.length === 1);
  const probeChars = new Set<string>([...symbols, ...BINDABLE_DIALOG_KEY_CHARACTERS, "+", "A", " ", "é"]);
  out.piTui = {
    symbolKeys: symbols,
    bindable: [...BINDABLE_DIALOG_KEY_CHARACTERS],
    matches: Object.fromEntries([...probeChars].map((c) => [c, matchesKey(c, c as never)])),
  };
  out.schema = buildPermissionsJsonSchema();

  // single files
  out.files = cases.files.map((c: { id: string; raw?: string; rawB64?: string }) => {
    const dir = mkdtempSync(join(tmpdir(), "cfg-probe-"));
    try {
      const p = join(dir, "config.json");
      writeFileSync(p, c.rawB64 !== undefined ? Buffer.from(c.rawB64, "base64") : (c.raw as string));
      const r = loadUnifiedConfig(p);
      return { id: c.id, config: JSON.stringify(r.config), issues: r.issues.map((i) => i.split(p).join("<PATH>")) };
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  });

  // loader scenarios
  out.loaders = cases.loaders.map((c: Record<string, any>) => {
    const tmp = mkdtempSync(join(tmpdir(), "cfg-probe-"));
    try {
      const agentDir = join(tmp, "agent");
      const cwd = join(tmp, "project");
      const ext = join(tmp, "ext");
      put(agentDir, "extensions/pi-permission-system/config.json", c.global);
      put(cwd, ".pi/extensions/pi-permission-system/config.json", c.project);
      put(agentDir, "pi-permissions.jsonc", c.legacyGlobal);
      put(cwd, ".pi/agent/pi-permissions.jsonc", c.legacyProject);
      put(ext, "config.json", c.legacyExt);
      for (const [n, md] of Object.entries((c.agents ?? {}) as Files)) put(agentDir, `agents/${n}.md`, md);
      for (const [n, md] of Object.entries((c.projectAgents ?? {}) as Files)) put(cwd, `.pi/agents/${n}.md`, md);
      put(agentDir, "mcp.json", c.mcpGlobal);
      put(cwd, ".pi/mcp.json", c.mcpProject);
      const scrub = (s: string) => s.split(tmp).join("<TMP>");
      const opts = c.includeProject === null || c.includeProject === undefined ? {} : { includeProjectScope: c.includeProject };
      const merge = loadAndMergeConfigs(agentDir, cwd, ext, opts);
      const runtime = normalizePermissionSystemConfig(merge.merged);

      const trusted = c.includeProject !== false;
      const loader = new FilePolicyLoader({
        globalConfigPath: join(agentDir, "extensions/pi-permission-system/config.json"),
        agentsDir: join(agentDir, "agents"),
        projectGlobalConfigPath: trusted ? join(cwd, ".pi/extensions/pi-permission-system/config.json") : undefined,
        projectAgentsDir: trusted ? join(cwd, ".pi/agents") : undefined,
        globalMcpConfigPath: join(agentDir, "mcp.json"),
        projectMcpConfigPath: trusted ? join(cwd, ".pi/mcp.json") : undefined,
      });
      const scopes = {
        global: JSON.stringify(loader.loadGlobalConfig()),
        project: JSON.stringify(loader.loadProjectConfig()),
        agents: Object.fromEntries(
          ((c.agentNames ?? []) as string[]).map((n) => [
            n,
            { agent: JSON.stringify(loader.loadAgentConfig(n)), projectAgent: JSON.stringify(loader.loadProjectAgentConfig(n)) },
          ]),
        ),
        mcp: loader.getConfiguredMcpServerNames(),
        issues: loader.getConfigIssues().map(scrub),
        paths: JSON.parse(scrub(JSON.stringify(loader.getResolvedPolicyPaths()))),
      };
      return {
        id: c.id,
        global: JSON.stringify(merge.global),
        project: JSON.stringify(merge.project),
        merged: JSON.stringify(merge.merged),
        runtime: JSON.stringify(runtime),
        issues: merge.issues.map(scrub),
        scopes,
      };
    } finally {
      rmSync(tmp, { recursive: true, force: true });
    }
  });

  out.yaml = cases.yaml.map((c: { id: string; markdown: string }) => {
    const fm = extractFrontmatter(c.markdown);
    return { id: c.id, frontmatter: fm, map: JSON.stringify(parseSimpleYamlMap(fm)), whole: JSON.stringify(parseSimpleYamlMap(c.markdown)) };
  });

  out.dialogKeys = cases.dialogKeys.map((c: { id: string; overrides: Record<string, string> }) => {
    const r = resolveDialogKeys({ permissionDialogKeys: c.overrides });
    return { id: c.id, keys: JSON.stringify(r.keys), issues: r.issues };
  });

  // ConfigStore.save against the real file system
  out.saves = cases.saves.map((c: { id: string; existing?: string; next: Record<string, unknown> }) => {
    const tmp = mkdtempSync(join(tmpdir(), "cfg-probe-"));
    try {
      const agentDir = join(tmp, "agent");
      put(agentDir, "extensions/pi-permission-system/config.json", c.existing);
      const notes: unknown[] = [];
      const statuses: unknown[] = [];
      const store = new ConfigStore({
        agentDir,
        policyPaths: { getResolvedPolicyPaths: () => ({}) as never },
        logger: { debug() {}, review() {} } as never,
      });
      const ctx = { ui: { notify: (m: string, l: string) => notes.push([m, l]), setStatus: (k: string, v: unknown) => statuses.push([k, v ?? null]) } };
      store.save(c.next as never, ctx as never);
      let file: string | null = null;
      try {
        file = readFileSync(join(agentDir, "extensions/pi-permission-system/config.json"), "utf-8");
      } catch {
        file = null;
      }
      return { id: c.id, file, notes: JSON.parse(JSON.stringify(notes).split(tmp).join("<TMP>")), statuses, current: JSON.stringify(store.current()) };
    } finally {
      rmSync(tmp, { recursive: true, force: true });
    }
  });

  // the /permission-system command
  out.commands = cases.commands.map((c: { id: string; config?: Record<string, unknown>; rules: unknown[]; args: string[]; hasUI?: boolean }) => {
    const tmp = mkdtempSync(join(tmpdir(), "cfg-probe-"));
    try {
      const agentDir = join(tmp, "agent");
      const store = new ConfigStore({
        agentDir,
        policyPaths: { getResolvedPolicyPaths: () => ({}) as never },
        logger: { debug() {}, review() {} } as never,
      });
      if (c.config) {
        put(agentDir, "extensions/pi-permission-system/config.json", JSON.stringify(c.config));
        store.refresh(undefined, false);
      }
      let def: any;
      registerPermissionSystemCommand({ registerCommand: (_n: string, d: unknown) => (def = d) } as never, {
        config: store,
        configPath: "/cfg/config.json",
        getActiveAgentConfigRules: () => c.rules as never,
      });
      const results: unknown[] = [];
      for (const a of c.args) {
        const notes: unknown[] = [];
        let customCalls = 0;
        const ctx = {
          hasUI: c.hasUI !== false,
          ui: {
            notify: (m: string, l: string) => notes.push([m, l]),
            setStatus() {},
            custom: async () => {
              customCalls++;
            },
          },
        };
        // eslint-disable-next-line no-await-in-loop
        void def.handler(a, ctx);
        results.push({ args: a, notes: JSON.parse(JSON.stringify(notes).split(tmp).join("<TMP>")), customCalls, current: JSON.stringify(store.current()) });
      }
      const completions = ((c as { completions?: string[] }).completions ?? []).map((p) => [p, def.getArgumentCompletions(p)]);
      return { id: c.id, description: def.description, results, completions };
    } finally {
      rmSync(tmp, { recursive: true, force: true });
    }
  });

  writeFileSync(output, `${JSON.stringify(out, null, 1)}\n`);
});
